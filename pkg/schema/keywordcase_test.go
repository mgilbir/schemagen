package schema

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The parse-level guards for issue #350: a JSON key that differs from a keyword
// only in case is not that keyword.
//
// JSON Schema keywords are case-sensitive, and a keyword an implementation does
// not recognise is to be ignored. encoding/json reads keys the other way round --
// a key matching no field exactly is matched a second time case-insensitively --
// so every keyword on Schema was accepted in every casing and enforced as the
// keyword it resembles. See exactKeywordObject for the four ways that came out
// wrong; tests/keyword_case_test.go is the same defect seen through the verdict a
// generated type gives.

// caseVariantValue is what every keyword in the drift guard below is given.
//
// It is a string on purpose, and a string no keyword's value could be mistaken
// for. Against a keyword typed as a string on Schema it decodes cleanly and the
// field then *holds* it, which is what makes the over-enforcement visible;
// against every other keyword it is the wrong shape and the decode fails, which
// is the other half of the same defect -- a legal document refused because the
// value of an unrecognised keyword was read as the value of the keyword it
// resembles. Either way the guard sees it.
const caseVariantValue = `"schemagen-issue-350"`

// TestEveryKeywordIsMatchedOnlyByItsExactSpelling is the drift guard: it asks the
// question of every keyword Schema declares rather than of the handful the issue
// was filed with.
//
// The keyword list comes from the struct's own json tags, exactly as
// knownSchemaKeys does, so a keyword this package learns later is covered by this
// test the day the field is added and without anyone remembering to extend a
// list. That is the property that matters here: the defect was uniform across
// every keyword, so a guard naming three of them would say nothing about the
// fourth.
func TestEveryKeywordIsMatchedOnlyByItsExactSpelling(t *testing.T) {
	var empty Schema
	if err := json.Unmarshal([]byte(`{}`), &empty); err != nil {
		t.Fatalf("decoding the empty schema: %v", err)
	}
	baseline, ok := empty.MarshaledKeywords()
	if !ok {
		t.Fatal("the empty schema has no keyword set")
	}

	for _, keyword := range knownSchemaKeyOrder {
		variant := strings.ToUpper(keyword)
		if variant == keyword {
			t.Fatalf("%q has no upper-case spelling to test with", keyword)
		}
		if knownSchemaKeys[variant] {
			t.Fatalf("%q is itself a keyword, so it is not a case variant of %q", variant, keyword)
		}

		t.Run(keyword, func(t *testing.T) {
			doc := `{` + jsonQuote(variant) + `:` + caseVariantValue + `}`
			var s Schema
			if err := json.Unmarshal([]byte(doc), &s); err != nil {
				t.Fatalf("%s was refused: %v\nan unrecognised keyword constrains nothing, including its own value", doc, err)
			}
			stated, ok := s.MarshaledKeywords()
			if !ok {
				t.Fatalf("%s decoded to something with no keyword set", doc)
			}
			if !maps.Equal(stated, baseline) {
				t.Errorf("%s stated %v, want %v\nthe key names no keyword, so the schema states nothing",
					doc, slices.Sorted(maps.Keys(stated)), slices.Sorted(maps.Keys(baseline)))
			}
			// The three fields the decode fills from the raw document rather
			// than from a tag, which MarshaledKeywords cannot see.
			if s.ConstIsNull || s.TypeSchemas != nil || s.BooleanSchema != nil {
				t.Errorf("%s filled a field the keyword set does not cover", doc)
			}
			// It is still an unrecognised keyword, so it is still reachable by
			// JSON Pointer -- that is what Extensions is for, and dropping the
			// key from the struct decode must not drop it from the document.
			if got, ok := s.Extensions[variant]; !ok {
				t.Errorf("%s did not preserve %q in Extensions", doc, variant)
			} else if string(got) != caseVariantValue {
				t.Errorf("%s preserved %q as %s, want %s", doc, variant, got, caseVariantValue)
			}
		})
	}
}

// TestTheExactSpellingWinsOverACaseVariantInEitherOrder covers the document that
// states a keyword and also carries a case variant of it.
//
// Both spellings filled the same field, so which value survived was decided by
// their order in the document: {"minLength":1,"MinLength":9} read as 9 and
// {"MinLength":9,"minLength":1} read as 1. The keyword is stated once and its
// value is 1 either way.
func TestTheExactSpellingWinsOverACaseVariantInEitherOrder(t *testing.T) {
	for _, doc := range []string{
		`{"minLength":1,"MinLength":9}`,
		`{"MinLength":9,"minLength":1}`,
	} {
		var s Schema
		if err := json.Unmarshal([]byte(doc), &s); err != nil {
			t.Fatalf("%s was refused: %v", doc, err)
		}
		if s.MinLength == nil {
			t.Errorf("%s dropped the keyword it states", doc)
			continue
		}
		if got := s.MinLength.Int(); got != 1 {
			t.Errorf("%s read minLength as %d, want 1", doc, got)
		}
	}
}

// TestACaseVariantOfAKeywordIsNotFoldedByAnASCIIRule is why exactKeywordObject
// asks strings.EqualFold rather than comparing lower-cased ASCII.
//
// U+017F LATIN SMALL LETTER LONG S folds to "s" under Unicode simple folding, so
// "$ſchema" is a key encoding/json matches to the $schema field -- and $schema
// chooses the dialect every keyword is then read under, which makes it the most
// consequential field on the struct to be able to fill by accident.
//
// The control is the point of the test rather than an aside. It shows the hazard
// is real by putting the same key to a struct that has not been protected from
// it: if a future encoding/json stopped folding this key, the control fails and
// says so, instead of the guard above quietly passing for a reason that has
// nothing to do with the fix.
func TestACaseVariantOfAKeywordIsNotFoldedByAnASCIIRule(t *testing.T) {
	const doc = `{"$ſchema":"https://json-schema.org/draft/2020-12/schema"}`

	var control struct {
		Schema string `json:"$schema"`
	}
	if err := json.Unmarshal([]byte(doc), &control); err != nil {
		t.Fatalf("decoding the control: %v", err)
	}
	if control.Schema == "" {
		t.Skip("encoding/json no longer folds U+017F onto \"s\", so this key is not a hazard")
	}

	var s Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("%s was refused: %v", doc, err)
	}
	if s.Schema != "" {
		t.Errorf("%s set the dialect to %q; the key names no keyword and sets nothing", doc, s.Schema)
	}
	if len(s.Extensions) != 1 {
		t.Errorf("the unrecognised keyword did not reach Extensions: %v", slices.Sorted(maps.Keys(s.Extensions)))
	}
}

// TestDiscriminatorFieldsAreMatchedByTheirExactNames is the same rule inside the
// one other struct a schema document is decoded into.
//
// The discriminator is a vendor keyword rather than a JSON Schema one, but a key
// it does not define is as much nothing to it as an unrecognised keyword is to a
// schema -- and this one is not inert: it names the property a generated oneOf
// dispatches on.
func TestDiscriminatorFieldsAreMatchedByTheirExactNames(t *testing.T) {
	var variant Schema
	const variantDoc = `{"discriminator":{"PropertyName":"kind","MAPPING":{"a":"#/$defs/A"}}}`
	if err := json.Unmarshal([]byte(variantDoc), &variant); err != nil {
		t.Fatalf("%s was refused: %v", variantDoc, err)
	}
	if variant.Discriminator == nil {
		t.Fatalf("%s dropped the discriminator itself", variantDoc)
	}
	if variant.Discriminator.PropertyName != "" {
		t.Errorf("%s dispatches on %q; neither key names a field of the discriminator",
			variantDoc, variant.Discriminator.PropertyName)
	}
	if variant.Discriminator.Mapping != nil {
		t.Errorf("%s built a mapping from %q", variantDoc, "MAPPING")
	}

	// The control: the exact spellings must still be read, or this is a fix that
	// switched the keyword off.
	var exact Schema
	const exactDoc = `{"discriminator":{"propertyName":"kind","mapping":{"a":"#/$defs/A"}}}`
	if err := json.Unmarshal([]byte(exactDoc), &exact); err != nil {
		t.Fatalf("%s was refused: %v", exactDoc, err)
	}
	if exact.Discriminator == nil || exact.Discriminator.PropertyName != "kind" {
		t.Fatalf("%s did not read the discriminator: %+v", exactDoc, exact.Discriminator)
	}
	if !reflect.DeepEqual(exact.Discriminator.Mapping, map[string]string{"a": "#/$defs/A"}) {
		t.Errorf("%s read the mapping as %v", exactDoc, exact.Discriminator.Mapping)
	}
}

// TestAnOrdinaryDocumentIsDecodedFromItsOwnBytes pins the fast path.
//
// The rebuild costs a copy of the node's subtree, and Schema.UnmarshalJSON runs
// once per node, so a rebuild taken unconditionally would be paid by every schema
// at every level. exactKeywordObject answering nil is what confines it to the
// documents that need it, and this is what says it still does.
func TestAnOrdinaryDocumentIsDecodedFromItsOwnBytes(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want bool // does the document need the rebuild?
	}{
		{name: "a plain schema", doc: `{"type":"string","minLength":1}`, want: false},
		{name: "an unrecognised keyword that folds onto nothing", doc: `{"x-vendor":1,"type":"string"}`, want: false},
		{name: "a keyword stated twice in one casing", doc: `{"minLength":1,"minLength":2}`, want: false},
		{name: "a property whose name happens to be a keyword", doc: `{"properties":{"MinLength":{"type":"string"}}}`, want: false},
		{name: "a case variant of a keyword", doc: `{"MinLength":5}`, want: true},
		{name: "a case variant beside the keyword itself", doc: `{"minLength":1,"MinLength":9}`, want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(c.doc), &raw); err != nil {
				t.Fatalf("decoding %s: %v", c.doc, err)
			}
			got := exactKeywordObject(raw, knownSchemaKeys, knownSchemaKeyOrder) != nil
			if got != c.want {
				t.Errorf("%s: rebuilt=%v, want %v", c.doc, got, c.want)
			}
		})
	}
}

// jsonQuote writes a JSON string literal for a keyword name. The names are
// the package's own struct tags, so this is quoting text that cannot fail to
// encode.
func jsonQuote(s string) string {
	q, _ := json.Marshal(s)
	return string(q)
}
