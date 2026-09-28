package generator

import (
	"encoding/json"
	"strings"
	"testing"

	ecma262 "github.com/mgilbir/goecma262"
	ecmaflags "github.com/mgilbir/goecma262/flags"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// TestPatternEngineSourceIsThePatternWhenItCompiles holds the rule that the
// engine is handed a schema's pattern byte for byte. Every pattern here is a
// valid ECMA-262 regular expression under the "u" flag, several of them ones a
// rewrite keyed on punctuation would be tempted by -- an escaped hyphen in a
// class, an escaped slash, an escaped backslash before a colon.
func TestPatternEngineSourceIsThePatternWhenItCompiles(t *testing.T) {
	for _, p := range []string{
		`^[a-z]+$`,
		`^[\-a]$`,
		`^\/$`,
		`^\\:$`,
		`^[^]$`,
		`(?<=a)b`,
		`^(?<x>a)\k<x>$`,
		`^\p{L}+$`,
		`^\p{White_Space}\p{space}$`,
		`^\u{1F600}$`,
		`^\d{3}-\d{4}$`,
		// ES2025 modifier groups.
		`^(?i:ab)c$`,
		`^(?i:a(?-i:b))$`,
		`^(?ims:a.)$`,
		`^(?-s:.)$`,
		``,
	} {
		got, err := PatternEngineSource(p)
		if err != nil {
			t.Errorf("PatternEngineSource(%q): %v", p, err)
			continue
		}
		if got != p {
			t.Errorf("PatternEngineSource(%q) = %q; a pattern that compiles must reach the engine unchanged", p, got)
		}
	}
}

// TestPatternEngineSourceRewritesOnlyPunctuationEscapes covers the one
// rewrite: a backslash before ASCII punctuation the "u" flag does not let a
// pattern escape. The rewritten pattern must mean what the pattern means
// wherever it has a meaning -- without the flag, where the escape is the
// character itself -- so each is checked against the pattern compiled without
// the flag, over inputs that exercise the escaped characters.
func TestPatternEngineSourceRewritesOnlyPunctuationEscapes(t *testing.T) {
	for _, tt := range []struct {
		pattern, want string
		inputs        []string
	}{
		{`^[A-Za-z0-9_\-\.\:]+$`, `^[A-Za-z0-9_\x2d\.\x3a]+$`, []string{"wake_up-time:1.0", "a b", "-", ":", "x;y"}},
		{`^\:$`, `^\x3a$`, []string{":", "\\:", "a"}},
		{`^[\@\#]+\!$`, `^[\x40\x23]+\x21$`, []string{"@#!", "@!", "#", "a!"}},
		// An escaped backslash consumes the next backslash, and the colon after
		// it is a plain colon: only the escaped colon is rewritten.
		{`^\\\:\\:$`, `^\\\x3a\\:$`, []string{`\:\:`, `\\:`}},
	} {
		got, err := PatternEngineSource(tt.pattern)
		if err != nil {
			t.Errorf("PatternEngineSource(%q): %v", tt.pattern, err)
			continue
		}
		if got != tt.want {
			t.Errorf("PatternEngineSource(%q) = %q, want %q", tt.pattern, got, tt.want)
			continue
		}
		plain := ecma262.MustCompile(tt.pattern, 0)
		rewritten := ecma262.MustCompile(got, ecmaflags.Unicode)
		for _, in := range tt.inputs {
			if a, b := plain.MatchString(in), rewritten.MatchString(in); a != b {
				t.Errorf("%q on %q: %v as written (no u flag), %v rewritten (u flag); the rewrite changed the meaning", tt.pattern, in, a, b)
			}
		}
	}
}

// TestPatternEngineSourceRefusesWhatHasNoOneMeaning is the other side of the
// rewrite: a letter escape the "u" flag refuses is not read as its letter,
// because dialects disagree about it -- \e is ESC in PCRE, \a is BEL, \z an
// anchor -- and a pattern that is not a regular expression in any reading
// stays refused.
//
// A modifier group is a regular expression, and compiles; what ECMA-262 makes
// an early error of is not: a flag named twice or on both sides of the "-", a
// group that names no flag at all, a flag other than i, m or s, and the
// Perl-style unscoped "(?i)". \p takes the property names the specification
// lists and no other alias Unicode has for them, so \p{WSpace} is refused
// where \p{White_Space} and \p{space} compile.
func TestPatternEngineSourceRefusesWhatHasNoOneMeaning(t *testing.T) {
	for _, p := range []string{
		`[\e]`, `\a`, `^\z`, `(`, `a{2,1}`, `[z-a]`, `\p{NotAProperty}`,
		`(?ii:a)`, `(?i-i:a)`, `(?-:a)`, `(?x:a)`, `(?i)a`, `(?g:a)`,
		`\p{WSpace}`,
	} {
		if got, err := PatternEngineSource(p); err == nil {
			t.Errorf("PatternEngineSource(%q) = %q, want an error", p, got)
		}
	}
}

// TestUncompilablePatternIsASchemaError puts a pattern that is not an
// ECMA-262 regular expression into every position a pattern occupies, and
// requires generation to refuse the schema, naming the JSON Pointer of the
// keyword. None of these may generate: a pattern that reached generated code
// used to panic at the first Validate (contains), be dropped in silence (a
// declared property matched by patternProperties), or match nothing.
func TestUncompilablePatternIsASchemaError(t *testing.T) {
	const bad = `"(?<"`
	for _, tt := range []struct {
		schema, pointer string
	}{
		{`{"type":"string","pattern":` + bad + `}`, `#/pattern`},
		{`{"type":"object","properties":{"s":{"type":"string","pattern":` + bad + `}}}`, `#/properties/s/pattern`},
		// Pointers are written as URI fragments, as every refusal on the walk
		// writes them: "<" is %3C and "^" is %5E.
		{`{"type":"object","patternProperties":{` + bad + `:{"type":"integer"}}}`, `#/patternProperties/(?%3C`},
		{`{"type":"object","properties":{"a/b":{}},"patternProperties":{"^a":{"pattern":` + bad + `}}}`, `#/patternProperties/%5Ea/pattern`},
		// Keywords Normalize rewrites are reported as the document spelled
		// them, not as the keyword the pattern was moved under.
		{`{"$schema":"http://json-schema.org/draft-03/schema#","extends":[{"pattern":` + bad + `}]}`, `#/extends/0/pattern`},
		{`{"$schema":"http://json-schema.org/draft-03/schema#","extends":{"pattern":` + bad + `}}`, `#/extends/pattern`},
		{`{"$schema":"http://json-schema.org/draft-03/schema#","disallow":[{"pattern":` + bad + `}]}`, `#/disallow/0/pattern`},
		{`{"$schema":"http://json-schema.org/draft-07/schema#","definitions":{"d":{"pattern":` + bad + `}}}`, `#/definitions/d/pattern`},
		{`{"$schema":"http://json-schema.org/draft-07/schema#","dependencies":{"a":{"pattern":` + bad + `}}}`, `#/dependencies/a/pattern`},
		{`{"type":"object","propertyNames":{"pattern":` + bad + `}}`, `#/propertyNames/pattern`},
		{`{"type":"array","items":{"type":"string","pattern":` + bad + `}}`, `#/items/pattern`},
		{`{"type":"array","contains":{"type":"string","pattern":` + bad + `}}`, `#/contains/pattern`},
		{`{"type":"array","prefixItems":[{"pattern":` + bad + `}]}`, `#/prefixItems/0/pattern`},
		{`{"anyOf":[{"pattern":` + bad + `},{"type":"integer"}]}`, `#/anyOf/0/pattern`},
		{`{"not":{"pattern":` + bad + `}}`, `#/not/pattern`},
		{`{"$defs":{"unused":{"pattern":` + bad + `}},"type":"string"}`, `#/$defs/unused/pattern`},
		{`{"type":"object","properties":{"s~/":{"pattern":` + bad + `}}}`, `#/properties/s~0~1/pattern`},
	} {
		var s schema.Schema
		if err := json.Unmarshal([]byte(tt.schema), &s); err != nil {
			t.Fatalf("%s: %v", tt.schema, err)
		}
		s.Normalize()
		_, err := New(DefaultConfig()).Generate(&s)
		if err == nil {
			t.Errorf("%s: generated; a pattern that is not a regular expression must be refused", tt.schema)
			continue
		}
		if !strings.Contains(err.Error(), tt.pointer+": ") || !strings.Contains(err.Error(), "is not an ECMA-262 regular expression") {
			t.Errorf("%s: error %q does not name %s", tt.schema, err, tt.pointer)
		}
	}
}

// TestPatternVarNameIsOnePerPattern: the same pattern gets the same variable
// wherever it is used, and two patterns never share one; the registry answers
// for every name it handed out, which is what the helper file is built from.
func TestPatternVarNameIsOnePerPattern(t *testing.T) {
	a1, err := PatternVarName(`^a$`)
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := PatternVarName(`^a$`)
	b, _ := PatternVarName(`^b$`)
	if a1 != a2 || a1 == b {
		t.Fatalf("names %q %q %q: one pattern must have one name, and two patterns two", a1, a2, b)
	}
	if len(a1) != patternVarNameLen || !strings.HasPrefix(a1, patternVarPrefix) {
		t.Fatalf("name %q is not a prefix and sixteen hex digits", a1)
	}
	got := HelpersReferencedBy("x := " + b + ".matches(s); y := " + a1 + "; z := " + a2).Patterns
	if len(got) != 2 || got[0] != `^a$` || got[1] != `^b$` {
		t.Fatalf("HelpersReferencedBy found %q, want [^a$ ^b$]", got)
	}
	if _, err := PatternVarName(`(`); err == nil {
		t.Fatal("PatternVarName accepted a pattern that does not compile")
	}
}
