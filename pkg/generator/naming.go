package generator

import (
	"strings"
	"unicode"
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
	first := rune(result[0])
	if unicode.IsDigit(first) {
		result = "X" + result
	}

	// Avoid Go reserved keywords.
	if goKeywords[strings.ToLower(result)] {
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
func JSONPropertyToGoName(name string) string {
	words := splitWords(name)
	var result strings.Builder
	for _, w := range words {
		result.WriteString(capitalizeWord(w))
	}
	return sanitizeGoIdentifier(result.String())
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
