package reconcile

import (
	"reflect"
	"testing"
)

func TestBuild(t *testing.T) {
	a := Route{
		Service:   "web",
		HTTPSPort: 8443,
		Path:      "/",
		Target:    "http://127.0.0.1:3000",
		Public:    false,
	}
	b := Route{
		Service:   "web",
		HTTPSPort: 8443,
		Path:      "/",
		Target:    "http://127.0.0.1:8080",
		Public:    false,
	}
	api := Route{
		Service:   "api",
		HTTPSPort: 8443,
		Path:      "/api",
		Target:    "http://127.0.0.1:4000",
		Public:    false,
	}
	webhook := Route{
		Service:   "webhook",
		HTTPSPort: 443,
		Path:      "/hooks",
		Target:    "http://127.0.0.1:8787",
		Public:    true,
	}
	newPath := Route{
		Service:   "web",
		HTTPSPort: 8443,
		Path:      "/app",
		Target:    "http://127.0.0.1:3000",
		Public:    false,
	}
	privateAPI := Route{
		Service:   "api",
		HTTPSPort: 8443,
		Path:      "/api",
		Target:    "http://127.0.0.1:4000",
		Public:    false,
	}
	publicAPI := Route{
		Service:   "api",
		HTTPSPort: 443,
		Path:      "/api",
		Target:    "http://127.0.0.1:4000",
		Public:    true,
	}

	tests := []struct {
		name    string
		desired []Route
		actual  []Route
		owned   []Route
		force   bool
		want    Plan
	}{
		{
			name: "absent desired actual and owned yields no operation",
		},
		{
			name:    "desired A with absent actual creates A",
			desired: []Route{a},
			want: Plan{Operations: []Operation{
				{Kind: KindCreate, After: a},
			}},
		},
		{
			name:    "matching unmanaged actual keeps A without claiming ownership",
			desired: []Route{a},
			actual:  []Route{a},
			want: Plan{Operations: []Operation{
				{Kind: KindKeep, Before: a, After: a},
			}},
		},
		{
			name:    "matching owned actual keeps A",
			desired: []Route{a},
			actual:  []Route{a},
			owned:   []Route{a},
			want: Plan{Operations: []Operation{
				{Kind: KindKeep, Before: a, After: a},
			}},
		},
		{
			name:    "unmanaged A versus B is a conflict",
			desired: []Route{a},
			actual:  []Route{b},
			want: Plan{Conflicts: []Conflict{
				{Desired: a, Actual: b},
			}},
		},
		{
			name:    "owned B updates to desired A",
			desired: []Route{a},
			actual:  []Route{b},
			owned:   []Route{b},
			want: Plan{Operations: []Operation{
				{Kind: KindUpdate, Before: b, After: a},
			}},
		},
		{
			name:   "owned actual with no desired route is deleted",
			actual: []Route{a},
			owned:  []Route{a},
			want: Plan{Operations: []Operation{
				{Kind: KindDelete, Before: a},
			}},
		},
		{
			name:   "unmanaged actual with no desired route is left untouched",
			actual: []Route{a},
		},
		{
			name:    "force updates unmanaged B to A and records takeover",
			desired: []Route{a},
			actual:  []Route{b},
			force:   true,
			want: Plan{
				Operations: []Operation{
					{Kind: KindUpdate, Before: b, After: a},
				},
				Conflicts: []Conflict{
					{Desired: a, Actual: b, Takeover: true},
				},
			},
		},
		{
			name:    "operations are ordered by route key",
			desired: []Route{api, a, webhook},
			want: Plan{Operations: []Operation{
				{Kind: KindCreate, After: webhook},
				{Kind: KindCreate, After: a},
				{Kind: KindCreate, After: api},
			}},
		},
		{
			name:    "changed path deletes the old identity and creates the new one",
			desired: []Route{newPath},
			actual:  []Route{a},
			owned:   []Route{a},
			want: Plan{Operations: []Operation{
				{Kind: KindDelete, Before: a},
				{Kind: KindCreate, After: newPath},
			}},
		},
		{
			name:    "changed target on an owned route is an update",
			desired: []Route{a},
			actual:  []Route{b},
			owned:   []Route{b},
			want: Plan{Operations: []Operation{
				{Kind: KindUpdate, Before: b, After: a},
			}},
		},
		{
			name:  "stale owned route that already disappeared yields no operation",
			owned: []Route{a},
		},
		{
			name:    "public change deletes the old listener and creates the new listener",
			desired: []Route{publicAPI},
			actual:  []Route{privateAPI},
			owned:   []Route{privateAPI},
			want: Plan{Operations: []Operation{
				{Kind: KindCreate, After: publicAPI},
				{Kind: KindDelete, Before: privateAPI},
			}},
		},
		{
			name:    "all conflicts are returned",
			desired: []Route{a, api},
			actual:  []Route{b, {Service: "api", HTTPSPort: 8443, Path: "/api", Target: "http://127.0.0.1:9000", Public: false}},
			want: Plan{Conflicts: []Conflict{
				{Desired: a, Actual: b},
				{Desired: api, Actual: Route{Service: "api", HTTPSPort: 8443, Path: "/api", Target: "http://127.0.0.1:9000", Public: false}},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(tt.desired, tt.actual, tt.owned, tt.force)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Build() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
