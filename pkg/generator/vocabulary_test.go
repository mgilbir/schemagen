package generator

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

func parseNormalized(t *testing.T, doc string) *schema.Schema {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	return &s
}

// TestUnknownKeywordsAreAnnotationsUnlessStrict holds the decided reading of a
// keyword the parser does not know: an annotation by default -- JSON Schema
// 2019-09 onward, and what every draft says to do with one -- and a located
// refusal under StrictKeywords, which is how a misspelled assertion
// ("minLenght") or a vendor keyword someone meant to be enforced gets seen.
func TestUnknownKeywordsAreAnnotationsUnlessStrict(t *testing.T) {
	const doc = `{
		"type": "object",
		"properties": {
			"a": {"type": "string", "minLenght": 3, "$comment": "known to constrain nothing"},
			"b": {"items": {"x-vendor": true}}
		},
		"$defs": {"unused": {"x-other": 1}}
	}`

	g := New(Config{PackageName: "testpkg"})
	if _, err := g.Generate(parseNormalized(t, doc)); err != nil {
		t.Fatalf("an unknown keyword refused the schema without StrictKeywords: %v", err)
	}
	for _, e := range g.Unclaimed() {
		if e.Keyword == "minLenght" || e.Keyword == "x-vendor" || e.Keyword == "x-other" {
			t.Errorf("the ledger lists the unknown keyword %q as an unenforced assertion; it is an annotation: %s", e.Keyword, e)
		}
	}

	_, err := New(Config{PackageName: "testpkg", StrictKeywords: true}).Generate(parseNormalized(t, doc))
	var unknown *UnknownKeywordsError
	if !errors.As(err, &unknown) {
		t.Fatalf("StrictKeywords: err = %v, want an UnknownKeywordsError", err)
	}
	want := []UnknownKeyword{
		{Location: "#/properties/a", Keyword: "minLenght"},
		{Location: "#/properties/b/items", Keyword: "x-vendor"},
		{Location: "#/$defs/unused", Keyword: "x-other"},
	}
	got := map[UnknownKeyword]bool{}
	for _, k := range unknown.Keywords {
		got[k] = true
	}
	if len(unknown.Keywords) != len(want) {
		t.Errorf("StrictKeywords named %v, want exactly %v ($comment is known to constrain nothing)", unknown.Keywords, want)
	}
	for _, k := range want {
		if !got[k] {
			t.Errorf("StrictKeywords did not name %s at %s; got %v", k.Keyword, k.Location, unknown.Keywords)
		}
	}
}

// TestUnknownRequiredVocabularyRefuses: a metaschema that declares a
// vocabulary this generator does not implement as required must refuse the
// schema (2020-12 §8.1.2), whether or not StrictKeywords is set; one it
// declares optional is ignored, which is what the suite's "ignore unrecognized
// optional vocabulary" asserts.
func TestUnknownRequiredVocabularyRefuses(t *testing.T) {
	meta := func(required bool) *schema.Schema {
		return parseNormalized(t, `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://ex.test/meta",
			"$vocabulary": {
				"https://json-schema.org/draft/2020-12/vocab/core": true,
				"https://json-schema.org/draft/2020-12/vocab/validation": true,
				"https://ex.test/vocab/custom": `+map[bool]string{true: "true", false: "false"}[required]+`
			}
		}`)
	}
	for _, tc := range []struct {
		required bool
		refuse   bool
	}{{true, true}, {false, false}} {
		resolver := schema.NewMappingResolver(map[string]*schema.Schema{"https://ex.test/meta": meta(tc.required)})
		doc := parseNormalized(t, `{"$schema": "https://ex.test/meta", "type": "string"}`)
		_, err := New(Config{PackageName: "testpkg", Resolver: resolver}).Generate(doc)
		var vocab *UnknownVocabularyError
		switch {
		case tc.refuse && !errors.As(err, &vocab):
			t.Errorf("required unknown vocabulary: err = %v, want an UnknownVocabularyError", err)
		case tc.refuse && (len(vocab.Vocabulary) != 1 || vocab.Vocabulary[0] != "https://ex.test/vocab/custom" || vocab.Location != "#"):
			t.Errorf("the refusal names %+v; want the custom vocabulary, at the document root", vocab)
		case !tc.refuse && err != nil:
			t.Errorf("optional unknown vocabulary refused the schema: %v", err)
		}
	}
}
