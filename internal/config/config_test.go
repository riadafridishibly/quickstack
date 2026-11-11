package config

import "testing"

func TestDefaultPublicURL(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		addr string
		dev  bool
		want string
	}{
		{"localhost:8360", false, "http://localhost:8360"},
		{":8360", false, "http://localhost:8360"},
		{"0.0.0.0:80", false, "http://localhost:80"},
		{"localhost:8360", true, "http://localhost:7360"},
	} {
		if got := defaultPublicURL(tt.addr, tt.dev); got != tt.want {
			t.Errorf("defaultPublicURL(%q, %v) = %q, want %q", tt.addr, tt.dev, got, tt.want)
		}
	}
}

func TestParseOrigin(t *testing.T) {
	t.Parallel()
	if got, err := parseOrigin("https://app.example.com/"); err != nil || got != "https://app.example.com" {
		t.Errorf("parseOrigin = %q, %v", got, err)
	}
	for _, bad := range []string{"app.example.com", "https://app.example.com/x", "ftp://a.b", "https://u:p@a.b"} {
		if _, err := parseOrigin(bad); err == nil {
			t.Errorf("parseOrigin(%q) succeeded", bad)
		}
	}
}
