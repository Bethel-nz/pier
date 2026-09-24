package tailscale

import (
	"reflect"
	"testing"
)

func TestParseStatusReadsTCPForwards(t *testing.T) {
	status, err := ParseStatus([]byte(`{
		"TCP": {
			"443":  {"HTTPS": true},
			"5432": {"TCPForward": "127.0.0.1:5432"}
		},
		"Web": {
			"box.tail1.ts.net:443": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:3000"}}}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Route{
		{HTTPSPort: 443, Path: "/", Target: "http://127.0.0.1:3000"},
		{HTTPSPort: 5432, Target: "tcp://127.0.0.1:5432", TCP: true},
	}
	if !reflect.DeepEqual(status.Routes, want) {
		t.Fatalf("routes = %+v\nwant %+v", status.Routes, want)
	}
}

func TestTCPArgs(t *testing.T) {
	route := Route{HTTPSPort: 5432, Target: "tcp://127.0.0.1:5432", TCP: true}
	up, err := UpArgs(route)
	if err != nil || !reflect.DeepEqual(up, []string{"serve", "--bg", "--yes", "--tcp=5432", "tcp://127.0.0.1:5432"}) {
		t.Fatalf("up = %v, %v", up, err)
	}
	down, err := DownArgs(route)
	if err != nil || !reflect.DeepEqual(down, []string{"serve", "--tcp=5432", "off"}) {
		t.Fatalf("down = %v, %v", down, err)
	}
	route.Public = true
	if _, err := UpArgs(route); err == nil {
		t.Fatal("a public TCP route must be refused")
	}
}
