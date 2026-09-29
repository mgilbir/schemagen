package generator

import (
	"unicode"
	"unicode/utf8"
)

//go:generate env GOTOOLCHAIN=go1.25.5 go run ./internal/unicodepin/gen unicode_tables.go

// Every question this generator asks about a rune on its way into generated
// source -- may it stand in an identifier, does it start an exported one, what
// is its upper or lower case, may encoding/json read it in a struct tag's name
// -- is asked of the tables in unicode_tables.go, which are pinned to the
// Unicode version of the oldest Go the generated code supports (go.mod's go
// directive), not of the Go the generator happens to run under.
//
// The generated code is compiled, and its tags read by encoding/json, under any
// supported Go. Go 1.27 knows Unicode 17 and Go 1.25 knows Unicode 15, so a
// generator built with 1.27 used to emit, for a property spelled in a script
// Unicode 16 added, a field name Go 1.25 refuses ("invalid character in
// identifier") and a struct tag Go 1.25's encoding/json ignores in favour of
// the field name -- silently reading and writing the property under another
// key. A case mapping Unicode 16 added did the same through capitalization:
// ToUpper(U+0264) is U+A7CB under 1.27 and U+0264 under 1.25, so "ɤ" became an
// exported field on the newer Go spelled with a letter the older one lacks.
//
// Asking the oldest Go's tables answers the question for every supported Go,
// because Unicode does not take letters or case pairs away (the stability
// policy; TestUnicodeTablesArePinned holds the running Go to it), so what is a
// letter to the oldest is a letter to every later one. A rune newer than the
// pin is treated as punctuation: dropped from a derived name, and routed to the
// hand-written JSON path in a tag.

// identLetter reports whether r is a letter every supported Go accepts in an
// identifier. The underscore is not a letter here; callers that accept it say so.
func identLetter(r rune) bool { return unicode.Is(pinnedLetter, r) }

// identDigit reports whether r is a decimal digit every supported Go accepts
// after an identifier's first rune.
func identDigit(r rune) bool { return unicode.Is(pinnedDigit, r) }

// identUpper reports whether r is an upper-case letter every supported Go
// reads as exporting the identifier it starts.
func identUpper(r rune) bool { return unicode.Is(pinnedUpper, r) }

// identLower reports whether r is a lower-case letter by the pinned tables.
func identLower(r rune) bool { return unicode.Is(pinnedLower, r) }

// identToUpper is unicode.ToUpper by the pinned case mapping.
func identToUpper(r rune) rune { return pinnedTo(unicode.UpperCase, r) }

// identToLower is unicode.ToLower by the pinned case mapping.
func identToLower(r rune) rune { return pinnedTo(unicode.LowerCase, r) }

// identStringToLower is strings.ToLower by the pinned case mapping.
func identStringToLower(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		out = append(out, identToLower(r))
	}
	return string(out)
}

// pinnedTo maps r to case c through pinnedCaseRanges, by the rule unicode.To
// applies to its own table: a range holds a delta per case, or the UpperLower
// sentinel for a run that alternates upper and lower case.
func pinnedTo(c int, r rune) rune {
	if r < 0 || r > unicode.MaxRune {
		return r
	}
	lo, hi := 0, len(pinnedCaseRanges)
	for lo < hi {
		m := lo + (hi-lo)/2
		cr := pinnedCaseRanges[m]
		switch {
		case r < rune(cr.Lo):
			hi = m
		case r > rune(cr.Hi):
			lo = m + 1
		default:
			d := cr.Delta[c]
			if d > unicode.MaxRune {
				// UpperLower: even offsets from Lo are upper case, odd ones
				// lower; UpperCase and TitleCase are even, LowerCase is odd.
				return rune(cr.Lo) + ((r-rune(cr.Lo))&^1 | rune(c&1))
			}
			return r + d
		}
	}
	return r
}

// IsIdentifier reports whether s is a Go identifier under every Go the
// generated code supports: a letter or underscore, then letters, digits and
// underscores, and not a keyword. The emitter and the CLI ask this of a
// package name, an import alias and a --root-name, so that a name a newer Go
// would accept is not handed to an older one that refuses it.
func IsIdentifier(s string) bool {
	if s == "" || goKeywords[s] {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || identLetter(r):
		case i > 0 && identDigit(r):
		default:
			return false
		}
	}
	return true
}

// IsExportedIdentifier reports whether s is an exported Go identifier under
// every Go the generated code supports.
func IsExportedIdentifier(s string) bool {
	first, _ := utf8.DecodeRuneInString(s)
	return IsIdentifier(s) && identUpper(first)
}

// IdentifierToLower lowers r by the pinned case mapping, for the emitter's
// receiver names: a receiver is the first letter of a type name lowered, and a
// newer Go's mapping can land on a letter the oldest supported Go lacks.
func IdentifierToLower(r rune) rune { return identToLower(r) }
