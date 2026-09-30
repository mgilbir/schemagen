package emitter

import (
	"io/fs"
	"sort"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// The emitter half of the name registry's promise (generator/names.go): every
// identifier a generated package declares at package level, or as a method of a
// generated type, is one the generator named -- whole -- and the templates only
// spell it. A template that composed a declared name out of a field and some
// text of its own ("{{.Name}}AccessRules", "Get{{.FieldName}}") declared a name
// the registry never saw, and that is how a definition keyed "FooAccessRules",
// or a property named "getCat", declared the same identifier twice.
//
// So: every top-level declaration in a template, and every method declaration,
// names its identifier either as fixed text -- a helper the registry reserves,
// see TestReservedHelperIdentifiersMatchTheTemplates -- or as one action whose
// value is a registry-minted name, never a mixture of the two.

// declaredNameActions are the actions a template may declare a package-level
// identifier or a method with, and where the generator gets each from.
var declaredNameActions = map[string]string{
	"{{.Name}}":                "a TypeDef's own name, or an enum constant's, or a compiled node's -- each claimed by the registry",
	"{{.WrapperName}}":         "OneOfVariant.WrapperName, claimed by claimVariantMemberNames",
	"{{$oneof.InterfaceName}}": "OneOfDef.InterfaceName, claimed in generateOneOfForProperty",
	"{{.GetterName}}":          "OneOfVariant.GetterName, claimed in the parent's member scope",
	"{{.AccessRulesVar}}":      "claimed by claimCarriedIdents",
	"{{.SchemaVar}}":           "claimed by claimCarriedIdents",
	"{{.Var}}":                 "ElementNode.Var, claimed by elementNode",
	"{{.EncodeKeysVar}}":       "claimed by resolveEncodePlans",
	"{{.StripRulesVar}}":       "claimed by resolveEncodePlans",
	"{{$allowedVar}}":          "EnumDef.AllowedVar, claimed where the raw enum is built",
	"{{.AccessorName}}":        "a fixed accessor per inferred JSON type (Float64, StringValue, ...), a method of a type with no fields",
	"{{.TypeCheckName}}":       "a fixed predicate per inferred JSON type (IsNumber, IsString, ...), a method of a type with no fields",
}

// declaredNameToken returns the identifier a Go declaration line declares, and
// whether the line is one: "type X", "var X", "const X", "func X(" and
// "func (r T) X(" at the start of a line, and a member line inside a
// package-level var or const block.
func declaredNameToken(line string, inBlock bool) (string, bool) {
	rest := line
	switch {
	case inBlock:
		trimmed := strings.TrimLeft(line, "\t")
		if trimmed == line || trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "{{") && !strings.HasPrefix(trimmed, "{{.") && !strings.HasPrefix(trimmed, "{{$") {
			return "", false
		}
		rest = trimmed
	case strings.HasPrefix(line, "type "), strings.HasPrefix(line, "var "), strings.HasPrefix(line, "const "):
		rest = line[strings.Index(line, " ")+1:]
		if strings.HasPrefix(rest, "(") {
			return "", false
		}
	case strings.HasPrefix(line, "func ("):
		close := matchingParen(line, len("func "))
		if close < 0 {
			return "", false
		}
		rest = strings.TrimLeft(line[close+1:], " ")
	case strings.HasPrefix(line, "func "):
		rest = line[len("func "):]
	default:
		return "", false
	}
	return identToken(rest), true
}

// matchingParen finds the parenthesis closing the one at open, skipping
// template actions.
func matchingParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		if strings.HasPrefix(s[i:], "{{") {
			end := strings.Index(s[i:], "}}")
			if end < 0 {
				return -1
			}
			i += end + 1
			continue
		}
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// identToken reads the identifier at the start of s: identifier characters and
// whole template actions, run together, up to the first character that is
// neither.
func identToken(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "{{") {
			end := strings.Index(s[i:], "}}")
			if end < 0 {
				break
			}
			b.WriteString(s[i : i+end+2])
			i += end + 2
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			break
		}
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

func TestTemplatesDeclareOnlyNamesTheGeneratorMinted(t *testing.T) {
	paths, err := fs.Glob(templateFS, "templates/*.go.tmpl")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no templates found: %v", err)
	}
	sort.Strings(paths)
	seen := 0
	used := map[string]bool{}
	for _, path := range paths {
		src, err := fs.ReadFile(templateFS, path)
		if err != nil {
			t.Fatal(err)
		}
		inBlock := false
		for n, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(line, "var (") || strings.HasPrefix(line, "const (") {
				inBlock = true
				continue
			}
			if inBlock && strings.HasPrefix(line, ")") {
				inBlock = false
				continue
			}
			token, ok := declaredNameToken(line, inBlock)
			if !ok || token == "" {
				continue
			}
			seen++
			if !strings.Contains(token, "{{") {
				// Fixed text: a helper the registry reserves, or a name local
				// to a template's own scaffolding. The reserved-names test holds
				// the helpers to the registry.
				continue
			}
			if _, ok := declaredNameActions[token]; ok {
				used[token] = true
				continue
			}
			t.Errorf("%s:%d declares %q: a declared name is one action naming what the generator's registry minted, "+
				"never text composed around one -- the registry never sees the composition, so nothing stops it from "+
				"landing on a name another declaration holds. Carry the whole name in the IR", path, n+1, token)
		}
	}
	if seen < 50 {
		t.Fatalf("only %d declarations found across the templates; the scan has stopped seeing them", seen)
	}
	for action := range declaredNameActions {
		if !used[action] {
			t.Errorf("declaredNameActions lists %s, which no template declares a name with any more; drop the stale entry", action)
		}
	}
}
