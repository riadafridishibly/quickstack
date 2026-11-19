package config

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	Addr        string
	DatabaseURL string
	MigrateURL  string
	// PublicURL is the origin links in emails point to, such as
	// https://app.example.com.
	PublicURL string
	// ViteURL is the Vite dev server origin. Empty serves the embedded build.
	ViteURL string
	// SMTPURL is smtp://user:password@host:port. Without it, mail is logged.
	SMTPURL  string
	MailFrom string
	// MailOutbox keeps sent mail in memory and shows it at /dev/mail. Anyone
	// who can reach the server can read it there, so it is for development
	// and e2e tests only.
	MailOutbox bool
	// TrustedProxies are the reverse proxies whose X-Forwarded-For header
	// names the client.
	TrustedProxies []netip.Prefix
	// DebugAddr serves net/http/pprof when set. It has no auth, so keep it
	// off public interfaces.
	DebugAddr string
	// BlobURL is where uploaded files go: file:///dir or
	// s3://key:secret@bucket/prefix?endpoint=host:port (see blob.Open).
	BlobURL string
	// SecretKey seals secrets the database holds, such as authenticator
	// app keys: 32 bytes, given in base64. Changing it makes them unreadable.
	SecretKey []byte
}

func Load() (Config, error) {
	c := Config{
		Addr:        env("QUICKSTACK_ADDR", "localhost:8360"),
		DatabaseURL: os.Getenv("QUICKSTACK_DATABASE_URL"),
		SMTPURL:     os.Getenv("QUICKSTACK_SMTP_URL"),
		MailFrom:    env("QUICKSTACK_MAIL_FROM", "Quickstack <noreply@localhost>"),
		MailOutbox:  os.Getenv("QUICKSTACK_MAIL_OUTBOX") == "1",
		DebugAddr:   os.Getenv("QUICKSTACK_DEBUG_ADDR"),
		BlobURL:     os.Getenv("QUICKSTACK_BLOB_URL"),
	}
	if c.DatabaseURL == "" {
		return c, errors.New("QUICKSTACK_DATABASE_URL is required")
	}
	c.MigrateURL = env("QUICKSTACK_MIGRATE_URL", c.DatabaseURL)
	proxies, err := parsePrefixes(os.Getenv("QUICKSTACK_TRUSTED_PROXIES"))
	if err != nil {
		return c, fmt.Errorf("QUICKSTACK_TRUSTED_PROXIES: %w", err)
	}
	c.TrustedProxies = proxies
	if os.Getenv("QUICKSTACK_DEV") == "1" {
		c.ViteURL = env("QUICKSTACK_VITE_URL", "http://localhost:8361")
		c.MailOutbox = true
	}
	if c.BlobURL == "" {
		c.BlobURL = "file:///var/lib/quickstack/media"
		if c.ViteURL != "" {
			c.BlobURL = "file://./tmp/media"
		}
	}
	if c.SecretKey, err = parseKey(os.Getenv("QUICKSTACK_SECRET_KEY")); err != nil {
		return c, fmt.Errorf("QUICKSTACK_SECRET_KEY: %w", err)
	}
	if c.SecretKey == nil && c.ViteURL != "" {
		c.SecretKey = devSecretKey[:]
	}
	public := os.Getenv("QUICKSTACK_PUBLIC_URL")
	if public == "" {
		public = defaultPublicURL(c.Addr, c.ViteURL != "")
	}
	if c.PublicURL, err = parseOrigin(public); err != nil {
		return c, fmt.Errorf("QUICKSTACK_PUBLIC_URL: %w", err)
	}
	return c, nil
}

// ErrNoSecretKey is returned by commands that need QUICKSTACK_SECRET_KEY,
// such as serve, when it isn't set.
var ErrNoSecretKey = errors.New("QUICKSTACK_SECRET_KEY is required: 32 random bytes in base64, such as from `openssl rand -base64 32`")

// devSecretKey is the key in development, so `make dev` needs no setup.
var devSecretKey = sha256.Sum256([]byte("quickstack development secret key"))

func parseKey(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, errors.New("not base64")
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%d bytes, want 32", len(key))
	}
	return key, nil
}

// defaultPublicURL is the listen address as an http origin. In dev, pages
// are opened through templ's proxy on :7360.
func defaultPublicURL(addr string, dev bool) string {
	if dev {
		return "http://localhost:7360"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parsePrefixes parses a comma-separated list of IP addresses and CIDR
// ranges.
func parsePrefixes(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP address or CIDR range", part)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

// parseOrigin checks an http or https URL with no path and returns it
// without a trailing slash.
func parseOrigin(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("%q is not an origin such as https://app.example.com", s)
	}
	return u.Scheme + "://" + u.Host, nil
}
