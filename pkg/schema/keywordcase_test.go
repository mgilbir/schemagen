package schema

import (
	"encoding/json"
	"fmt"
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
// keyword it resembles. See parse.go for the four ways that came out
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

// TestACaseVariantOfAKeywordIsNotFoldedByAnASCIIRule is why the decoder looks a
// keyword up by its exact name rather than asking encoding/json to match it.
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

// TestADuplicateKeyMeansItsLastValueWhateverElseTheObjectHolds pins the one
// duplicate-key policy: the last value of a key is its value, for every keyword
// type and independently of every other key.
//
// Two things made it otherwise. encoding/json decodes a repeated key into a
// struct field holding a map by filling the map the first value already
// filled, so {"properties":{"a":{}},"properties":{"b":{}}} read as properties a
// and b -- a merge no reading of the document asks for. And the case-folding
// guard that preceded this decoder rebuilt the object from its key set whenever
// some key folded onto a keyword, and the key set holds only the last value; so
// adding an unrelated {"Title":"x"} flipped the same document to properties b
// alone. Each case below is stated bare and beside such a key, and the two must
// agree.
func TestADuplicateKeyMeansItsLastValueWhateverElseTheObjectHolds(t *testing.T) {
	cases := []struct {
		name string
		body string // the object's members, without braces
		want func(*Schema) string
		is   string
	}{
		{"a map-valued keyword", `"properties":{"a":{}},"properties":{"b":{}}`,
			func(s *Schema) string { return strings.Join(slices.Sorted(maps.Keys(s.Properties)), ",") }, "b"},
		{"a scalar keyword", `"minLength":1,"minLength":2`,
			func(s *Schema) string { return fmt.Sprint(s.MinLength.Int()) }, "2"},
		{"an array keyword", `"required":["a"],"required":["b"]`,
			func(s *Schema) string { return strings.Join(s.Required, ",") }, "b"},
		{"an unrecognised keyword", `"x-vendor":1,"x-vendor":2`,
			func(s *Schema) string { return string(s.Extensions["x-vendor"]) }, "2"},
		{"a key inside a map-valued keyword", `"properties":{"a":{"type":"string"},"a":{"type":"integer"}}`,
			func(s *Schema) string { return strings.Join(s.Properties["a"].Type, ",") }, "integer"},
		{"a legacy keyword read as schemas", `"dependencies":{"a":["x"]},"dependencies":{"b":["y"]}`,
			func(s *Schema) string { return strings.Join(slices.Sorted(maps.Keys(s.DependencyRequired)), ",") }, "b"},
	}
	for _, c := range cases {
		for _, beside := range []string{"", `,"Title":"x"`} {
			doc := "{" + c.body + beside + "}"
			t.Run(c.name+beside, func(t *testing.T) {
				var s Schema
				if err := json.Unmarshal([]byte(doc), &s); err != nil {
					t.Fatalf("%s was refused: %v", doc, err)
				}
				if bad := s.MalformedKeywords(); len(bad) > 0 {
					t.Fatalf("%s reported malformed keywords: %v", doc, bad)
				}
				if got := c.want(&s); got != c.is {
					t.Errorf("%s read as %q, want %q (the last value)", doc, got, c.is)
				}
			})
		}
	}
}

// jsonQuote writes a JSON string literal for a keyword name. The names are
// the package's own struct tags, so this is quoting text that cannot fail to
// encode.
func jsonQuote(s string) string {
	q, _ := json.Marshal(s)
	return string(q)
}
