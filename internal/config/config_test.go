package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeDomain(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "pier.yaml")
	input := "version: 1\nname: demo\nservices:\n  api:\n    target: localhost:4000\n    local: My-App.local.\n"
	if err := os.WriteFile(configPath, []byte(input), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	raw, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	project, err := Normalize(raw)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if project.Services[0].Domain != "my-app.local" {
		t.Fatalf("domain = %q, want my-app.local", project.Services[0].Domain)
	}
	if errors := Validate(project); len(errors) != 0 {
		t.Fatalf("Validate() = %v, want none", errors)
	}
}

func TestValidateDomainLength(t *testing.T) {
	long := strings.Repeat("a", 64)
	project := Project{Version: 1, Name: "demo", Services: []ResolvedService{{
		Name: "api", Target: "http://127.0.0.1:4000", Host: "127.0.0.1", Port: 4000,
		Path: "/", Protocol: ProtocolHTTP, Domain: long + ".local",
	}}}
	errors := Validate(project)
	if len(errors) != 1 || errors[0].Field != "local" {
		t.Fatalf("Validate() = %#v, want one local error", errors)
	}
}

func TestValidateDomain(t *testing.T) {
	project := Project{Version: 1, Name: "demo", Services: []ResolvedService{{
		Name: "api", Target: "http://127.0.0.1:4000", Host: "127.0.0.1", Port: 4000,
		Path: "/", Protocol: ProtocolHTTP, Domain: "192.168.1.182",
	}}}
	errors := Validate(project)
	if len(errors) != 1 || errors[0].Field != "local" {
		t.Fatalf("Validate() = %#v, want one local error", errors)
	}
}

func TestNormalizeDefaults(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  ResolvedService
	}{
		{
			name:  "defaults localhost target and root path",
			input: "version: 1\nname: demo\nservices:\n  web:\n    target: localhost:3000\n",
			want: ResolvedService{
				Name: "web", Target: "http://127.0.0.1:3000",
				Host: "127.0.0.1", Port: 3000, HTTPSPort: 8443, Path: "/",
				Protocol: ProtocolHTTP, Public: false,
			},
		},
		{
			name:  "preserves explicit public path",
			input: "version: 1\nname: demo\nservices:\n  api:\n    target: localhost:4000\n    path: /api\n    public: true\n",
			want: ResolvedService{
				Name: "api", Target: "http://127.0.0.1:4000",
				Host: "127.0.0.1", Port: 4000, HTTPSPort: 443, Path: "/api",
				Protocol: ProtocolHTTP, Public: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "pier.yaml")
			if err := os.WriteFile(configPath, []byte(tt.input), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			raw, err := Load(configPath)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			project, err := Normalize(raw)
			if err != nil {
				t.Fatalf("Normalize() error = %v", err)
			}
			if len(project.Services) != 1 {
				t.Fatalf("Normalize() services = %d, want 1", len(project.Services))
			}
			if got := project.Services[0]; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Normalize() service = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestLoadRejectsInvalidYAML(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "unknown field",
			input: "version: 1\nname: demo\nservices:\n  web:\n    target: localhost:3000\n    targte: localhost:4000\n",
		},
		{
			name:  "non-boolean public value",
			input: "version: 1\nname: demo\nservices:\n  web:\n    target: localhost:3000\n    public: yes\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "pier.yaml")
			if err := os.WriteFile(configPath, []byte(tt.input), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			if _, err := Load(configPath); err == nil {
				t.Fatal("Load() error = nil, want decoding error")
			}
		})
	}
}

func TestNormalizeSortsServicesAndInheritsDefaults(t *testing.T) {
	public := false
	cfg := Config{
		Version: 1,
		Name:    "demo",
		Defaults: Defaults{
			Public:   true,
			Protocol: "https",
		},
		Services: map[string]Service{
			"web": {Target: "localhost:3000", Public: PublicFlag(public)},
			"api": {Target: "localhost:4000"},
		},
	}

	project, err := Normalize(cfg)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if got := []string{project.Services[0].Name, project.Services[1].Name}; !reflect.DeepEqual(got, []string{"api", "web"}) {
		t.Fatalf("Normalize() names = %v, want [api web]", got)
	}
	if !project.Services[0].Public || project.Services[0].HTTPSPort != 443 {
		t.Errorf("inherited public service = %#v", project.Services[0])
	}
	if project.Services[1].Public || project.Services[1].HTTPSPort != 8443 {
		t.Errorf("explicit private service = %#v", project.Services[1])
	}
	if got := project.Services[0].Target; got != "https://127.0.0.1:4000" {
		t.Errorf("inherited protocol target = %q, want https://127.0.0.1:4000", got)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantField string
	}{
		{
			name:      "unsupported version",
			input:     "version: 2\nname: demo\nservices:\n  web:\n    target: localhost:3000\n",
			wantField: "version",
		},
		{
			name:      "empty name",
			input:     "version: 1\nservices:\n  web:\n    target: localhost:3000\n",
			wantField: "name",
		},
		{
			name:      "empty services",
			input:     "version: 1\nname: demo\nservices: {}\n",
			wantField: "services",
		},
		{
			name:      "invalid service name",
			input:     "version: 1\nname: demo\nservices:\n  Bad_Name:\n    target: localhost:3000\n",
			wantField: "Bad_Name.name",
		},
		{
			name:      "bad port",
			input:     "version: 1\nname: demo\nservices:\n  web:\n    target: localhost:nope\n",
			wantField: "web.target",
		},
		{
			name:      "non-loopback target",
			input:     "version: 1\nname: demo\nservices:\n  web:\n    target: 192.0.2.1:3000\n",
			wantField: "web.target",
		},
		{
			name:      "path without leading slash",
			input:     "version: 1\nname: demo\nservices:\n  web:\n    target: localhost:3000\n    path: api\n",
			wantField: "web.path",
		},
		{
			name:      "path with query",
			input:     "version: 1\nname: demo\nservices:\n  web:\n    target: localhost:3000\n    path: /api?debug=true\n",
			wantField: "web.path",
		},
		{
			name:      "path with fragment",
			input:     "version: 1\nname: demo\nservices:\n  web:\n    target: localhost:3000\n    path: /api#debug\n",
			wantField: "web.path",
		},
		{
			name:      "path is not clean",
			input:     "version: 1\nname: demo\nservices:\n  web:\n    target: localhost:3000\n    path: /api/../v1\n",
			wantField: "web.path",
		},
		{
			name:      "duplicate normalized path",
			input:     "version: 1\nname: demo\nservices:\n  api:\n    target: localhost:3000\n    path: /api/\n  web:\n    target: localhost:4000\n    path: /api\n",
			wantField: "web.path",
		},
		{
			name:      "invalid protocol",
			input:     "version: 1\nname: demo\nservices:\n  web:\n    target: localhost:3000\n    protocol: tcp\n    public: true\n",
			wantField: "web.protocol",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project := loadAndNormalize(t, tt.input)
			errors := Validate(project)
			if !hasValidationField(errors, tt.wantField) {
				t.Fatalf("Validate() fields = %v, want %q", validationFields(errors), tt.wantField)
			}
		})
	}
}

func TestValidateReturnsAllErrorsInStableOrder(t *testing.T) {
	project := loadAndNormalize(t, "version: 2\nservices:\n  bad_name:\n    target: example.com:nope\n    path: relative?query=true\n    protocol: tcp\n")
	want := []string{"name", "version", "bad_name.name", "bad_name.path", "bad_name.protocol", "bad_name.target"}
	if got := validationFields(Validate(project)); !reflect.DeepEqual(got, want) {
		t.Fatalf("Validate() fields = %v, want %v", got, want)
	}
}

func TestValidateAllowsSamePathOnDifferentListeners(t *testing.T) {
	project := loadAndNormalize(t, "version: 1\nname: demo\nservices:\n  private:\n    target: localhost:3000\n    path: /\n  exposed:\n    target: localhost:4000\n    path: /\n    public: true\n")
	if errors := Validate(project); len(errors) != 0 {
		t.Fatalf("Validate() errors = %v, want none", errors)
	}
}

func loadAndNormalize(t *testing.T, input string) Project {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "pier.yaml")
	if err := os.WriteFile(configPath, []byte(input), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	raw, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	project, err := Normalize(raw)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	return project
}

func hasValidationField(errors []ValidationError, want string) bool {
	for _, err := range errors {
		if validationField(err) == want {
			return true
		}
	}
	return false
}

func validationFields(errors []ValidationError) []string {
	fields := make([]string, 0, len(errors))
	for _, err := range errors {
		fields = append(fields, validationField(err))
	}
	return fields
}

func validationField(err ValidationError) string {
	return strings.TrimPrefix(err.Service+"."+err.Field, ".")
}
