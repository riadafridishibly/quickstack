package slug

import (
	"strings"
	"testing"
)

func TestMake(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Acme Robotics":           "acme-robotics",
		"  R&D -- Team 2 ":        "r-d-team-2",
		"Café":                    "caf",
		"日本":                      "",
		strings.Repeat("ab ", 30): strings.Repeat("ab-", 13) + "a",
	} {
		got := Make(in)
		if got != want {
			t.Errorf("Make(%q) = %q, want %q", in, got, want)
		}
		if got != "" && !Valid(got) {
			t.Errorf("Make(%q) = %q, which isn't Valid", in, got)
		}
	}
	for _, bad := range []string{"", "-a", "a-", "a--b", "A", "a b"} {
		if Valid(bad) {
			t.Errorf("Valid(%q) = true", bad)
		}
	}
}
