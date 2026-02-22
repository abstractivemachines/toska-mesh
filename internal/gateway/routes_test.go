package gateway

import (
	"testing"
)

func TestNormalizePrefix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "/"},
		{"/", "/"},
		{"/api", "/api/"},
		{"/api/", "/api/"},
		{"api", "/api/"},
		{"api/", "/api/"},
	}

	for _, tt := range tests {
		got := normalizePrefix(tt.input)
		if got != tt.expected {
			t.Errorf("normalizePrefix(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestParseServiceFromPath(t *testing.T) {
	tests := []struct {
		prefix      string
		path        string
		wantService string
		wantRest    string
		wantOK      bool
	}{
		{"/api/", "/api/my-service/foo/bar", "my-service", "/foo/bar", true},
		{"/api/", "/api/my-service", "my-service", "/", true},
		{"/api/", "/api/my-service/", "my-service", "/", true},
		{"/api/", "/api/", "", "", false},
		{"/api/", "/other/path", "", "", false},
		{"/", "/my-service/hello", "my-service", "/hello", true},
		{"/", "/my-service", "my-service", "/", true},
	}

	for _, tt := range tests {
		svc, rest, ok := ParseServiceFromPath(tt.prefix, tt.path)
		if ok != tt.wantOK || svc != tt.wantService || rest != tt.wantRest {
			t.Errorf("ParseServiceFromPath(%q, %q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.prefix, tt.path, svc, rest, ok, tt.wantService, tt.wantRest, tt.wantOK)
		}
	}
}

func TestBuildBackendURL(t *testing.T) {
	tests := []struct {
		addr      string
		remainder string
		query     string
		want      string
	}{
		{"http://10.0.0.1:8080", "/hello", "", "http://10.0.0.1:8080/hello"},
		{"http://10.0.0.1:8080", "/hello", "q=1", "http://10.0.0.1:8080/hello?q=1"},
		{"https://svc.local:443", "/api/v1/data", "page=2&limit=10", "https://svc.local:443/api/v1/data?page=2&limit=10"},
	}

	for _, tt := range tests {
		got := BuildBackendURL(tt.addr, tt.remainder, tt.query)
		if got != tt.want {
			t.Errorf("BuildBackendURL(%q, %q, %q) = %q, want %q",
				tt.addr, tt.remainder, tt.query, got, tt.want)
		}
	}
}
