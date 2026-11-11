package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestRealIP(t *testing.T) {
	t.Parallel()
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("10.0.0.0/8")}
	var got string
	h := RealIP(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.RemoteAddr }))

	for _, tt := range []struct {
		name, remote, xff, want string
	}{
		{"trusted proxy", "127.0.0.1:5000", "203.0.113.9", "203.0.113.9:0"},
		{"chain of proxies", "127.0.0.1:5000", "203.0.113.9, 10.1.2.3", "203.0.113.9:0"},
		{"spoofed left entry", "127.0.0.1:5000", "1.1.1.1, 203.0.113.9", "203.0.113.9:0"},
		{"untrusted peer", "198.51.100.7:5000", "203.0.113.9", "198.51.100.7:5000"},
		{"no header", "127.0.0.1:5000", "", "127.0.0.1:5000"},
		{"garbage", "127.0.0.1:5000", "not-an-ip", "127.0.0.1:5000"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = tt.remote
		if tt.xff != "" {
			r.Header.Set("X-Forwarded-For", tt.xff)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		if got != tt.want {
			t.Errorf("%s: RemoteAddr %q, want %q", tt.name, got, tt.want)
		}
	}
}
