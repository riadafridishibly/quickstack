package web

import (
	"net/http"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	sweepEvery = time.Minute
	maxBuckets = 100_000
)

// Limiter is a token bucket per key, held in memory. Each process has its
// own, which is enough for one instance.
type Limiter struct {
	mu        sync.Mutex
	every     time.Duration
	burst     int
	max       int
	buckets   map[string]*rate.Limiter
	lastSweep time.Time
}

// NewLimiter allows a key burst requests at once, refilling one every
// every.
func NewLimiter(every time.Duration, burst int) *Limiter {
	return &Limiter{every: every, burst: burst, max: maxBuckets, buckets: map[string]*rate.Limiter{}, lastSweep: time.Now()}
}

// Allow reports whether key may make a request now, and counts it.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.lastSweep) > sweepEvery {
		l.sweep(now)
	}
	b, ok := l.buckets[key]
	if !ok {
		// Memory stays bounded under a flood of new keys; keys already
		// tracked keep their own limits.
		if len(l.buckets) >= l.max {
			return false
		}
		b = rate.NewLimiter(rate.Every(l.every), l.burst)
		l.buckets[key] = b
	}
	return b.AllowN(now, 1)
}

// sweep forgets buckets that have refilled, which behave like new ones.
func (l *Limiter) sweep(now time.Time) {
	l.lastSweep = now
	for k, b := range l.buckets {
		if b.TokensAt(now) >= float64(l.burst) {
			delete(l.buckets, k)
		}
	}
}

// ClientKey identifies the client for rate limiting. IPv6 clients usually
// hold a whole /64, so they are limited per /64.
func ClientKey(r *http.Request) string {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	addr := ap.Addr().Unmap()
	if addr.Is6() {
		p, _ := addr.Prefix(64)
		return p.String()
	}
	return addr.String()
}
