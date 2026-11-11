// Package slug makes URL segments such as a workspace's "acme-robotics".
package slug

import (
	"regexp"
	"strings"
)

const MaxLen = 40

var valid = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Make lowercases s and joins its runs of ASCII letters and digits with
// hyphens. Other characters are dropped, so the result can be empty.
func Make(s string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if gap && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			gap = false
			if b.Len() >= MaxLen {
				break
			}
		} else {
			gap = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// Valid reports whether s is a slug Make could have made.
func Valid(s string) bool { return len(s) <= MaxLen && valid.MatchString(s) }
