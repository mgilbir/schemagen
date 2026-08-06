package generator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

func annotationDefFor(t *testing.T, input string) *AnnotationSchemaDef {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	ir, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*AnnotationSchemaDef); ok {
			return d
		}
	}
	return nil
}

// unevaluatedItems next to in-place applicators needs the runtime evaluator:
// which items count as evaluated depends on which branches match the value.
func TestUnevaluatedItemsWithApplicatorsUsesRuntimeEvaluator(t *testing.T) {
	tests := []string{
		`{"prefixItems":[{"const":"foo"}],"anyOf":[{"prefixItems":[true,{"const":"bar"}]}],"unevaluatedItems":false}`,
		`{"unevaluatedItems":{"type":"boolean"},"anyOf":[{"items":{"type":"string"}},true]}`,
		`{"allOf":[{"prefixItems":[true]},{"unevaluatedItems":false}]}`,
		`{"allOf":[{"contains":{"multipleOf":2}},{"contains":{"multipleOf":3}}],"unevaluatedItems":{"multipleOf":5}}`,
	}
	for _, in := range tests {
		t.Run(in[:40], func(t *testing.T) {
			if annotationDefFor(t, in) == nil {
				t.Fatalf("expected the runtime evaluator for %s", in)
			}
		})
	}
}

// Schemas static analysis already handles keep their existing shape, so the
// change does not churn output for cases that were correct.
func TestStaticUnevaluatedItemsUnchanged(t *testing.T) {
	tests := []string{
		`{"type":"array","prefixItems":[{"type":"string"}],"unevaluatedItems":false}`,
		`{"type":"array","items":{"type":"string"}}`,
	}
	for _, in := range tests {
		t.Run(in[:30], func(t *testing.T) {
			if d := annotationDefFor(t, in); d != nil {
				t.Fatalf("static case was routed to the runtime evaluator: %s", in)
			}
		})
	}
}

// annotationPathDefFor is annotationDefFor for the narrow path alone.
//
// annotationDefFor reads the output, and two arms of generateTypeDef put an
// AnnotationSchemaDef there: the annotation path, whose allow-list is
// annotationKeywords, and the whole-schema evaluator, whose allow-list is
// validatorKeywords. A schema the first declines and the second claims is
// indistinguishable in the output, so a test about the first has to ask it.
//
// The generator is run first because annotationSchemaDef reads state Generate
// builds -- the $defs index a branch's $ref resolves through, and the resource
// graph under it.
func annotationPathDefFor(t *testing.T, input string) *AnnotationSchemaDef {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	g := New(Config{PackageName: "testpkg"})
	if _, err := g.Generate(&s); err != nil {
		t.Fatalf("generate: %v", err)
	}
	return g.annotationSchemaDef("Probe", &s)
}

// A keyword outside the *annotation* path's model must not be interpreted there
// with that keyword silently ignored.
//
// What changed since this was written is where such a schema goes instead. It
// used to fall to the static path, which does not enforce unevaluatedItems at
// all -- and for the properties case below did worse, giving an array-valued
// schema a struct type that refused []. It now goes to the whole-schema
// evaluator, whose allow-list does model uniqueItems, properties and $ref, so
// the keywords bind rather than being dropped. See
// unevaluatedNeedsRuntimeEvaluator.
//
// So the assertion is unchanged in what it is about and changed in how it asks:
// through annotationSchemaDef itself, which still declines all three, rather
// than through the output, where the second arm's answer now sits.
func TestAnnotationModelFailsClosed(t *testing.T) {
	tests := []string{
		`{"allOf":[{"uniqueItems":true}],"unevaluatedItems":false}`,
		`{"allOf":[{"properties":{"a":{"type":"string"}}}],"unevaluatedItems":false}`,
		`{"allOf":[{"$ref":"#/$defs/x"}],"unevaluatedItems":false,"$defs":{"x":{"type":"array"}}}`,
	}
	for _, in := range tests {
		t.Run(in[:36], func(t *testing.T) {
			if d := annotationPathDefFor(t, in); d != nil {
				t.Fatalf("unsupported keyword was routed to the annotation path: %s\n%s", in, d.NodeLiteral)
			}
			// And the other half of the same statement: the schema is not left
			// to the static path either, which is what let issue #189 accept
			// ["a",1]. Losing this is losing the fix, and the assertion above
			// cannot see it.
			if d := annotationDefFor(t, in); d == nil {
				t.Fatalf("declined by the annotation path and not taken by the whole-schema evaluator, so unevaluatedItems is left to the static path: %s", in)
			}
		})
	}
}

// additionalItems without a tuple is ignored by the spec: it neither validates
// nor evaluates. Modelling it as items made allOf reject values it must accept.
func TestIgnoredAdditionalItemsIsNotModelledAsItems(t *testing.T) {
	d := annotationDefFor(t, `{"allOf":[{"additionalItems":{"type":"number"}}],"unevaluatedItems":{"type":"string"}}`)
	if d == nil {
		t.Fatal("expected the runtime evaluator")
	}
	// The allOf branch must carry no Items: the additionalItems is inert.
	branch := d.NodeLiteral[strings.Index(d.NodeLiteral, "AllOf"):]
	if strings.Contains(branch[:strings.Index(branch, "UnevaluatedItems")], "Items:") {
		t.Fatalf("ignored additionalItems was modelled as items:\n%s", d.NodeLiteral)
	}
}

// TestKeywordAllowListsAgreeOnWhatConstrainsNothing is the guard on the drift
// that produced issue #178.
//
// annotationKeywords and validatorKeywords are allow-lists: a keyword outside
// one refuses the whole schema, so every entry missing from a list is a schema
// that silently loses the checks that list's path would have generated. Each of
// them used to carry its own hand-written copy of "the keywords that constrain
// nothing", and the copies disagreed -- annotationKeywords was short of $defs,
// definitions, id, $anchor, $dynamicAnchor and $recursiveAnchor, and both were
// short of $vocabulary. An unused $defs, which almost every real document
// carries, was enough to leave unevaluatedItems inside an allOf unenforced.
//
// allowing() removes that class by construction, and the first half below is
// what says so: it fails if the derivation is ever unpicked back into literals.
//
// The second half is for the difference that is real. The two lists genuinely
// model different keywords -- the annotation path takes over schemas whose
// static checks already work, so widening it changes code that is not broken --
// and a subset assertion alone would let that difference grow silently. So the
// difference is enumerated with a reason apiece: a keyword added to
// validatorKeywords and not to annotationKeywords fails here until somebody
// says why the narrower path cannot have it, and one added to annotationKeywords
// alone fails the subset check, because a keyword the narrower path models and
// the whole-schema path does not is a schema the evaluator would refuse in the
// position where refusing costs the most.
func TestKeywordAllowListsAgreeOnWhatConstrainsNothing(t *testing.T) {
	lists := []struct {
		name string
		set  map[string]bool
	}{
		{"annotationKeywords", annotationKeywords},
		{"validatorKeywords", validatorKeywords},
	}
	for _, source := range []struct {
		name string
		set  map[string]bool
	}{
		{"nonConstrainingKeywords", nonConstrainingKeywords},
		{"inertKeywords", inertKeywords},
	} {
		for keyword := range source.set {
			for _, list := range lists {
				if !list.set[keyword] {
					t.Errorf("%q is in %s but not in %s, so a schema stating it is refused over a keyword that constrains nothing",
						keyword, source.name, list.name)
				}
			}
		}
	}

	// Exactly the keywords validatorKeywords models and annotationKeywords does
	// not, and why. Anything else in the difference, either direction, is drift.
	const (
		staticAlready = "the static path already generates a working check for it, and the annotation path only takes schemas over to fix unevaluatedItems"
		objectShape   = "an object-shape keyword; a schema stating one gets a struct, which the annotation path must not replace"
		// A schema refused over one of these is not left to the static path: it
		// is offered to the whole-schema evaluator, which does inline references.
		// That is what settles #189 without widening this list.
		refInlining = "reference inlining is off on the annotation path (inlineRefs), so a schema stating it cannot be compiled there at all"
	)
	wantNarrower := map[string]string{
		"enum": staticAlready, "exclusiveMinimum": staticAlready, "exclusiveMaximum": staticAlready,
		"divisibleBy": staticAlready, "minLength": staticAlready, "maxLength": staticAlready,
		"pattern": staticAlready, "minItems": staticAlready, "maxItems": staticAlready,
		"uniqueItems": staticAlready, "not": staticAlready,

		"properties": objectShape, "patternProperties": objectShape,
		"additionalProperties": objectShape, "propertyNames": objectShape,
		"required": objectShape, "minProperties": objectShape, "maxProperties": objectShape,
		"dependentRequired": objectShape, "dependentSchemas": objectShape,
		"unevaluatedProperties": objectShape,

		"$ref": refInlining, "$dynamicRef": refInlining, "$recursiveRef": refInlining,
	}
	for keyword := range validatorKeywords {
		if annotationKeywords[keyword] {
			continue
		}
		if _, known := wantNarrower[keyword]; !known {
			t.Errorf("%q is in validatorKeywords but not in annotationKeywords, and nothing here says why; add it to annotationKeywords, or record the reason it cannot go there", keyword)
		}
	}
	for keyword, why := range wantNarrower {
		if !validatorKeywords[keyword] {
			t.Errorf("%q is recorded as a keyword only validatorKeywords has (%s), but validatorKeywords does not have it", keyword, why)
		}
		if annotationKeywords[keyword] {
			t.Errorf("%q is recorded as a keyword only validatorKeywords has (%s), but annotationKeywords has it too; drop the entry", keyword, why)
		}
	}
	for keyword := range annotationKeywords {
		if !validatorKeywords[keyword] {
			t.Errorf("%q is in annotationKeywords but not in validatorKeywords, so the whole-schema evaluator refuses a schema the narrower path compiles", keyword)
		}
	}
}

// TestUnusedDefinitionsDoNotDisableUnevaluatedItems is issue #178 as a document.
//
// Each of these states unevaluatedItems next to an in-place applicator, which is
// the shape only the runtime evaluator gets right, plus one keyword that carries
// no constraint where it sits. The keyword must not change the answer, and until
// #178 every one of them did: the schema was refused and fell back to static
// checks that accept ["a", 1].
func TestUnusedDefinitionsDoNotDisableUnevaluatedItems(t *testing.T) {
	for _, sibling := range []string{
		`"$defs":{"Unused":{}}`,
		`"definitions":{"Unused":{}}`,
		`"$anchor":"here"`,
		`"$dynamicAnchor":"here"`,
		`"$recursiveAnchor":true`,
		`"id":"http://example.com/root"`,
		// A $vocabulary that keeps the validation vocabulary, so the only thing
		// it changes is which keywords are *modelled*. One that leaves validation
		// out is a different matter entirely -- the keywords then do not bind and
		// no evaluator should be built. See hasValidationVocabulary.
		`"$vocabulary":{"https://json-schema.org/draft/2020-12/vocab/core":true,` +
			`"https://json-schema.org/draft/2020-12/vocab/applicator":true,` +
			`"https://json-schema.org/draft/2020-12/vocab/unevaluated":true,` +
			`"https://json-schema.org/draft/2020-12/vocab/validation":true}`,
		// The control from the issue: this one never disabled anything, and a
		// fix that stopped reading siblings altogether would still pass without
		// it.
		`"description":"says nothing about any value"`,
	} {
		doc := `{` + sibling + `,"allOf":[{"type":"array","prefixItems":[{"type":"string"}],"unevaluatedItems":false}]}`
		if d := annotationDefFor(t, doc); d == nil {
			t.Errorf("%s: no runtime evaluator, so unevaluatedItems is enforced by the static path, which accepts [\"a\",1]", doc)
		}
	}
}
