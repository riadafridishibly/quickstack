package auth

import (
	"bytes"
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// TOTPCode is the code an authenticator app shows at t for key, as the setup
// page prints it.
func TOTPCode(key string, t time.Time) string {
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ReplaceAll(key, " ", ""))
	if err != nil {
		panic(err)
	}
	return hotp(secret, t.Unix()/totpStep)
}

func TestHOTP(t *testing.T) {
	t.Parallel()
	// RFC 6238, appendix B, SHA-1, keeping the last six of eight digits.
	secret := []byte("12345678901234567890")
	for _, tt := range []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	} {
		if got := hotp(secret, tt.unix/totpStep); got != tt.want {
			t.Errorf("at %d: %s, want %s", tt.unix, got, tt.want)
		}
	}
}

func TestTOTPMatch(t *testing.T) {
	t.Parallel()
	secret := []byte("12345678901234567890")
	now := time.Unix(1111111111, 0)
	step := now.Unix() / totpStep
	for d, want := range map[int64]bool{-2: false, -1: true, 0: true, 1: true, 2: false} {
		code := hotp(secret, step+d)
		got, ok := totpMatch(secret, code[:3]+" "+code[3:], now)
		if ok != want || ok && got != step+d {
			t.Errorf("code %d steps away: step %d, %v", d, got, ok)
		}
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		if _, ok := totpMatch(secret, bad, now); ok {
			t.Errorf("%q matched", bad)
		}
	}
}

func TestNormalizeRecoveryCode(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"abcd-efgh-2345":   "ABCDEFGH2345",
		" ABCD EFGH 2345 ": "ABCDEFGH2345",
		"abcd-efgh-234":    "",
		"abcd-efgh-2341":   "", // 1 isn't in base32
		"abcd-efgh-23456":  "",
	} {
		if got := normalizeRecoveryCode(in); got != want {
			t.Errorf("normalizeRecoveryCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSealer(t *testing.T) {
	t.Parallel()
	s := newSealer(bytes.Repeat([]byte{7}, KeySize))
	sealed := s.seal([]byte("secret"), []byte("for 1"))
	if got, err := s.open(sealed, []byte("for 1")); err != nil || string(got) != "secret" {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := s.open(sealed, []byte("for 2")); err == nil {
		t.Error("opened with another aad")
	}
	if _, err := newSealer(bytes.Repeat([]byte{8}, KeySize)).open(sealed, []byte("for 1")); err == nil {
		t.Error("opened with another key")
	}
}
