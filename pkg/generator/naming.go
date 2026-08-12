package generator

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// commonAcronyms maps lowercase acronyms to their canonical upper-case form.
var commonAcronyms = map[string]string{
	"id":    "ID",
	"url":   "URL",
	"http":  "HTTP",
	"https": "HTTPS",
	"api":   "API",
	"json":  "JSON",
	"xml":   "XML",
	"sql":   "SQL",
	"html":  "HTML",
	"css":   "CSS",
	"uri":   "URI",
	"ip":    "IP",
	"tcp":   "TCP",
	"udp":   "UDP",
	"tls":   "TLS",
	"ssl":   "SSL",
	"ssh":   "SSH",
	"cpu":   "CPU",
	"gpu":   "GPU",
	"ram":   "RAM",
	"dns":   "DNS",
	"ttl":   "TTL",
	"uuid":  "UUID",
	"uid":   "UID",
	"ascii": "ASCII",
	"utf":   "UTF",
	"acl":   "ACL",
	"eof":   "EOF",
}

// goKeywords is the set of Go reserved keywords that cannot be used as identifiers.
var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

// splitWords splits a string on underscores, hyphens, and camelCase boundaries.
// Non-alphanumeric characters (other than _ and -) are treated as word separators
// and stripped from the output.
func splitWords(s string) []string {
	// First, replace separators and non-identifier characters with spaces.
	var buf strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		// Treat underscores, hyphens, and any non-letter/non-digit as separators.
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			buf.WriteRune(' ')
			continue
		}
		if i > 0 {
			prev := runes[i-1]
			prevIsIdent := unicode.IsLetter(prev) || unicode.IsDigit(prev)
			if prevIsIdent {
				// Upper after lower → new word boundary
				if unicode.IsUpper(r) && unicode.IsLower(prev) {
					buf.WriteRune(' ')
				}
				// Upper followed by lower, but preceded by upper → "URLParser" → "URL" "Parser"
				if i+1 < len(runes) && unicode.IsUpper(prev) && unicode.IsUpper(r) && unicode.IsLower(runes[i+1]) {
					buf.WriteRune(' ')
				}
			}
		}
		buf.WriteRune(r)
	}

	raw := strings.Fields(buf.String())
	return raw
}

// capitalizeWord capitalizes a word, handling common acronyms.
func capitalizeWord(word string) string {
	lower := strings.ToLower(word)
	if acronym, ok := commonAcronyms[lower]; ok {
		return acronym
	}
	if len(word) == 0 {
		return word
	}
	runes := []rune(lower)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// sanitizeGoIdentifier ensures the result is a valid, non-empty Go identifier.
// It strips any remaining non-identifier characters, ensures the name starts with
// a letter or underscore, and avoids Go reserved keywords.
func sanitizeGoIdentifier(name string) string {
	if name == "" {
		return "X"
	}

	// Strip characters that are not valid in Go identifiers.
	var buf strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			buf.WriteRune(r)
		}
	}
	result := buf.String()

	if result == "" {
		return "X"
	}

	// Ensure the first character is a letter or underscore.
	//
	// The first *rune*, decoded: result[0] is a byte, and for any name whose
	// first character is multi-byte that byte is not a character in the string
	// at all -- the lead byte of 日 is 0xE6, and rune(0xE6) is æ. That misread
	// the one input this branch exists for whenever it was non-ASCII: a property
	// named "१२३" (Devanagari digits, category Nd) begins 0xE0, which is à and
	// not a digit, so no "X" was prefixed and the emitter built a field named
	// १२३ -- not a legal Go identifier, because an identifier must *start* with
	// a letter. gofmt then refused the file it had been handed and generation
	// died on "illegal character U+0967 '१'", naming the emitter rather than the
	// schema. Not silent, but not generation either: every such document was
	// ungeneratable.
	first, _ := utf8.DecodeRuneInString(result)
	if unicode.IsDigit(first) {
		result = "X" + result
	}

	// Avoid Go reserved keywords. The check is case-SENSITIVE: only an
	// identifier that is literally a keyword needs escaping. Callers capitalize
	// output for exported fields/types ("type" → "Type"), and a capitalized name
	// can never equal an (all-lowercase) Go keyword, so this fires only for the
	// hypothetical unexported case and serves purely as a safety net.
	if goKeywords[result] {
		result = result + "_"
	}

	return result
}

// JSONPropertyToGoName converts a JSON property name to a Go exported field name.
//
// Examples:
//
//	"first_name" → "FirstName"
//	"firstName"  → "FirstName"
//	"id"         → "ID"
//	"api_url"    → "APIURL"
//	"$ref"       → "Ref"
//	"foo\"bar"   → "FooBar"
//	"日本語"        → "X日本語"
func JSONPropertyToGoName(name string) string {
	words := splitWords(name)
	var result strings.Builder
	for _, w := range words {
		result.WriteString(capitalizeWord(w))
	}
	return exportedGoName(sanitizeGoIdentifier(result.String()))
}

// exportedGoName is the step that makes JSONPropertyToGoName's promise of "a Go
// exported field name" true: capitalizing is not that promise, it is only an
// attempt at it.
//
// Capitalizing has nothing to reach in a script without case. unicode.ToUpper
// is the identity on 日, on ا, on ก and on every other letter in a caseless
// script, so a property named 日本語 became a Go field named 日本語 -- a perfectly
// legal identifier, and an unexported one. encoding/json ignores an unexported
// field however good its struct tag is, so the property was dropped in both
// directions: {"日本語":"v"} decoded to nothing and encoded back as nothing, with
// Validate passing either way. go vet said so on the generated file ("struct
// field 日本語 has json tag but is not exported") while generation reported
// success. That is issue #254, and it reaches Japanese, Chinese, Korean,
// Arabic, Hebrew, Thai and Devanagari alike.
//
// The repair is the "X" prefix sanitizeGoIdentifier already uses for a name that
// cannot start an identifier ("1a" → "X1a"), applied to a name that cannot start
// an *exported* one. It is total and mechanical, and it adds exactly one pair to
// the set of names this file already folds together: 日本語 and a property
// spelled X日本語 outright, since "X"+y can only equal z when z was already
// spelled that way. That is a name clash like any other -- the derivation has
// folded "a-b" and "ab" onto "AB" since long before this -- and it is resolved
// where every other one is, by the numeric-suffix deduplication in
// generateStruct, which reports an error rather than merging when a third
// property has taken the numbered name.
//
// Transliteration was the alternative and was rejected. It is not a function of
// the name alone -- 日本語 is "Nihongo" in Japanese and "Riben yu" in Chinese, so
// it needs language detection before it can even be unique, kanji readings are
// contextual, and the mapping is lossy across the whole name space rather than
// on one pair of it. It also wants an ICU-scale table this generator has no
// reason to carry.
//
// The predicate is isExportedGoIdentifier, the same one that already rejects an
// unexported --field-map override. Sharing it is the point: the rule the
// generator makes its users obey is now the rule it obeys itself, from one
// definition, so the two cannot drift apart.
func exportedGoName(name string) string {
	if isExportedGoIdentifier(name) {
		return name
	}
	return "X" + name
}

// SchemaNameToGoName converts a JSON Schema definition name to a Go type name.
//
// Examples:
//
//	"my-type"  → "MyType"
//	"my_type"  → "MyType"
//	"MyType"   → "MyType"
func SchemaNameToGoName(name string) string {
	return JSONPropertyToGoName(name)
}

// ToOneOfInterfaceName creates an unexported interface name for a oneOf group.
//
// Example: ("Parent", "Field") → "isParent_Field"
func ToOneOfInterfaceName(parent, field string) string {
	return "is" + parent + "_" + field
}

// ToOneOfWrapperName creates a wrapper struct name for a oneOf variant.
//
// Example: ("Parent", "Variant") → "Parent_Variant"
func ToOneOfWrapperName(parent, variant string) string {
	return parent + "_" + variant
}
