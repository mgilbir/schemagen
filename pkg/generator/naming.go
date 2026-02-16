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

// splitWords splits a string on underscores, hyphens, and camelCase boundaries.
func splitWords(s string) []string {
	// First, replace underscores and hyphens with spaces so we can split on them.
	var buf strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if r == '_' || r == '-' {
			buf.WriteRune(' ')
			continue
		}
		if i > 0 {
			prev := runes[i-1]
			if prev != '_' && prev != '-' {
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

// JSONPropertyToGoName converts a JSON property name to a Go exported field name.
//
// Examples:
//
//	"first_name" → "FirstName"
//	"firstName"  → "FirstName"
//	"id"         → "ID"
//	"api_url"    → "APIURL"
func JSONPropertyToGoName(name string) string {
	words := splitWords(name)
	var result strings.Builder
	for _, w := range words {
		result.WriteString(capitalizeWord(w))
	}
	return result.String()
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
