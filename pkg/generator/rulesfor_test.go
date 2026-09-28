package generator

import (
	"fmt"
	"testing"
)

// TestRulesAreBuiltByTheGeneratorThatIsGenerating is the audit's C4 and its
// class. The rule extractor was a free function, and the one question it asks
// of a generator -- whether unevaluatedItems:false closes a tuple, which
// resolves references -- it asked of a zero Generator built on the spot: no
// resource index, no definitions, no draft override. A reference under an
// unevaluatedItems subschema was resolved through nil maps and the generator
// panicked on a legal schema.
//
// The extractor is reached from every position a value can be checked at, so
// the reference is put under unevaluatedItems at each of them, and under the
// three spellings of a reference. Every one must generate.
func TestRulesAreBuiltByTheGeneratorThatIsGenerating(t *testing.T) {
	refs := []string{
		`{"$ref":"#/$defs/s"}`,
		`{"anyOf":[{"$ref":"#/$defs/s"}]}`,
		`{"allOf":[{"$ref":"#/$defs/s"}],"prefixItems":[{}]}`,
	}
	positions := []struct {
		name string
		wrap func(string) string
	}{
		{"root", func(s string) string { return s }},
		{"property", func(s string) string { return `{"properties":{"a":` + s + `}}` }},
		{"items", func(s string) string { return `{"type":"array","items":` + s + `}` }},
		{"prefixItems", func(s string) string { return `{"type":"array","prefixItems":[` + s + `]}` }},
		{"additionalProperties", func(s string) string { return `{"type":"object","additionalProperties":` + s + `}` }},
		{"oneOf", func(s string) string { return `{"oneOf":[` + s + `,{"type":"null"}]}` }},
	}
	for ri, ref := range refs {
		for _, p := range positions {
			sub := `{"type":"array","prefixItems":[{}],"unevaluatedItems":` + ref + `}`
			doc := `{"$defs":{"s":{}},` + p.wrap(sub)[1:]
			if p.wrap(sub)[0] != '{' {
				t.Fatalf("position %s wraps into a non-object", p.name)
			}
			t.Run(fmt.Sprintf("%s/ref%d", p.name, ri), func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("generating %s panicked: %v", doc, r)
					}
				}()
				if _, err := New(Config{PackageName: "testpkg"}).Generate(parseNormalized(t, doc)); err != nil {
					t.Fatalf("generating %s: %v", doc, err)
				}
			})
		}
	}

	// The audit's own reproduction.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("the audit's C4 schema panicked the generator: %v", r)
		}
	}()
	const c4 = `{"properties":{"a":{"unevaluatedItems":{"anyOf":[{"$ref":"#/$defs/s"}]}}},"$defs":{"s":{}}}`
	if _, err := New(Config{PackageName: "testpkg"}).Generate(parseNormalized(t, c4)); err != nil {
		t.Fatalf("generating %s: %v", c4, err)
	}
}
