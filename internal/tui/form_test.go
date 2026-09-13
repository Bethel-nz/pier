package tui

import (
	"strings"
	"testing"
)

func TestValidateServiceNameRejectsExisting(t *testing.T) {
	validate := validateServiceName([]string{"web", "api"})

	if err := validate("hooks"); err != nil {
		t.Fatalf("new name error = %v", err)
	}
	err := validate("web")
	if err == nil || !strings.Contains(err.Error(), `service "web" already exists`) {
		t.Fatalf("existing name error = %v, want already exists", err)
	}
	if err := validate("WEB"); err == nil {
		t.Fatal("invalid name error = nil")
	}
	if err := validate(" "); err == nil {
		t.Fatal("empty name error = nil")
	}
}

func TestAddServiceFormShowsProtocolChoice(t *testing.T) {
	values := &AddServiceValues{}
	form := newAddServiceForm(values, nil)

	for range 3 {
		form.NextField()
	}
	field := form.GetFocusedField()
	if field.GetKey() != "protocol" || field.GetValue() != "http" {
		t.Fatalf("add service form field = %q value = %#v, want protocol=http", field.GetKey(), field.GetValue())
	}
}
