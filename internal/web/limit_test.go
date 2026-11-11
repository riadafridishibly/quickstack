package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimiterCapsKeys(t *testing.T) {
	t.Parallel()
	l := NewLimiter(time.Hour, 1)
	l.max = 2
	if !l.Allow("a") || !l.Allow("b") {
		t.Fatal("first use of a key was refused")
	}
	if l.Allow("c") {
		t.Error("new key allowed past the cap")
	}
	if l.Allow("a") {
		t.Error("tracked key ignored its own limit")
	}
}

func TestClientKeyGroupsIPv6By64(t *testing.T) {
	t.Parallel()
	key := func(remote string) string {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		return ClientKey(r)
	}
	if a, b := key("[2001:db8:1:2::1]:443"), key("[2001:db8:1:2:ffff::9]:443"); a != b {
		t.Errorf("same /64 gave keys %q and %q", a, b)
	}
	if a, b := key("[2001:db8:1:2::1]:443"), key("[2001:db8:1:3::1]:443"); a == b {
		t.Errorf("different /64s share key %q", a)
	}
	if got := key("[::ffff:192.0.2.1]:80"); got != "192.0.2.1" {
		t.Errorf("IPv4-mapped address: key %q", got)
	}
}
