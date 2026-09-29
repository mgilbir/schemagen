package testsupport

// Backquote renders a JSON document as a Go raw string literal. No document in
// this file contains a backquote, and one arriving later must not be pasted into
// source that would no longer compile.
func Backquote(s string) string {
	for _, r := range s {
		if r == '`' {
			panic("forbidden-zero case contains a backquote: " + s)
		}
	}
	return "`" + s + "`"
}
