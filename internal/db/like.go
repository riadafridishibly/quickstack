package db

import "strings"

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// EscapeLike makes s match itself in a LIKE or ILIKE pattern, so a search
// for "50%" doesn't match everything that starts with 50.
func EscapeLike(s string) string { return likeEscaper.Replace(s) }
