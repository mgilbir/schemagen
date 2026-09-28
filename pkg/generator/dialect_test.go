package generator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// dialectFixture is a draft-07 document with one node whose meaning depends on
// the dialect twice over: under draft 7 its $ref replaces its siblings, so the
// $id beside it starts no resource and "#/definitions/b" is looked up in the
// document root, which has no "b"; from 2019-09 the $id starts a resource of
// its own and the same fragment names the "b" the node itself holds.
const dialectFixture = `{
	"$schema": "http://json-schema.org/draft-07/schema#",
	"type": "object",
	"properties": {
		"p": {
			"$id": "http://example.com/other.json",
			"$ref": "#/definitions/b",
			"definitions": {"b": {"type": "integer"}}
		}
	}
}`

// TestConfigDraftIsOneAnswerForTheIndexAndTheGenerator is follow-up (p).
//
// A caller decodes a draft-07 document, normalizes it the ordinary way -- under
// the draft it states -- and generates it under Config.Draft 2020-12. The
// resource index read the dialect normalization had settled, so the $id beside
// the $ref named no resource and the reference was looked for in the document
// root; the generator read Config.Draft and treated the node as a 2020-12
// applicator. One document, two dialects, and a reference that resolved under
// neither reading: generation failed with "cannot resolve $ref".
//
// With both reading pkg/schema's one rule, Config.Draft wins for the document
// the caller handed over, the $id starts a resource, and the reference reaches
// the integer beside it.
func TestConfigDraftIsOneAnswerForTheIndexAndTheGenerator(t *testing.T) {
	var s schema.Schema
	if err := json.Unmarshal([]byte(dialectFixture), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	g := New(Config{PackageName: "testpkg", Draft: schema.Draft202012})
	ir, err := g.Generate(&s)
	if err != nil {
		t.Fatalf("generate under Config.Draft 2020-12: %v -- the index and the generator read the document under two dialects", err)
	}
	p := s.Properties["p"]
	if got := g.index.DialectOf(p); got != schema.Draft202012 {
		t.Errorf("index reads p under %v, want 2020-12: Config.Draft wins for the document the caller handed over", got)
	}
	if got := g.draftForSchema(p); got != schema.Draft202012 {
		t.Errorf("generator reads p under %v, want 2020-12", got)
	}
	var fieldType, underlying string
	for _, td := range ir.TypeDefs {
		switch d := td.(type) {
		case *StructDef:
			for _, f := range d.Fields {
				if d.Name == "Root" && f.JSONName == "p" {
					fieldType = namedTypeName(f.Type)
				}
			}
		case *AliasDef:
			if d.Name == "B" {
				underlying = d.Underlying.GoTypeName()
			}
		}
	}
	if fieldType != "B" || underlying != "int64" {
		t.Errorf("p is %q over %q, want B over int64: under 2020-12 its $ref reaches the integer its own $id scopes", fieldType, underlying)
	}

	// And the $schema the override contradicted is reported where it is
	// written, rather than overridden in silence.
	over := g.DialectOverrides()
	if len(over) != 1 || over[0].Location != "#" || over[0].Stated != schema.Draft07 || over[0].Applied != schema.Draft202012 {
		t.Errorf("DialectOverrides = %+v, want the root's draft-07 $schema reported as read under 2020-12", over)
	}
}

// TestDialectRuleKeepsWhatAnOverrideDoesNotSpeakFor holds the two exceptions:
// an embedded resource declaring its own $id beside its own $schema, and a
// document no caller listed. A $schema written on a node that declares no
// resource is the document's, and the override covers it.
func TestDialectRuleKeepsWhatAnOverrideDoesNotSpeakFor(t *testing.T) {
	var s schema.Schema
	doc := `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$defs": {
			"embedded": {"$id": "https://ex.test/e.json", "$schema": "https://json-schema.org/draft/2019-09/schema",
				"properties": {"x": {"type": "string"}}},
			"bare": {"$schema": "https://json-schema.org/draft/2019-09/schema", "properties": {"y": {"type": "string"}}}
		}
	}`
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.NormalizeForDraft(schema.Draft202012)
	x := schema.NewResourceIndex(nil, schema.WithIndexDraft(schema.Draft202012))
	if err := x.AddDocument(&s, nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	for _, tc := range []struct {
		node *schema.Schema
		want schema.Draft
		why  string
	}{
		{&s, schema.Draft202012, "the root of the document the caller handed over"},
		{s.Defs["embedded"], schema.Draft201909, "an embedded resource declaring its own $id and $schema"},
		{s.Defs["embedded"].Properties["x"], schema.Draft201909, "a node inside that resource"},
		{s.Defs["bare"], schema.Draft202012, "a $schema on a node that declares no resource of its own"},
		{s.Defs["bare"].Properties["y"], schema.Draft202012, "a node below it"},
	} {
		if got := x.DialectOf(tc.node); got != tc.want {
			t.Errorf("%s: index reads %v, want %v", tc.why, got, tc.want)
		}
		if got := tc.node.DetectedDraft; got != tc.want {
			t.Errorf("%s: normalization settled %v, want %v -- normalization and the index must give one answer", tc.why, got, tc.want)
		}
	}
}

// TestAnIndexBuiltForAnotherDraftIsRefused: an index a caller built and passed
// as Config.Resolver is used as it is, so one built for a different draft than
// Config.Draft would compute the document's resources under one dialect while
// the generator read its keywords under the other -- the disagreement the one
// rule exists to end. That is an error naming both; an index built for the
// same draft generates.
func TestAnIndexBuiltForAnotherDraftIsRefused(t *testing.T) {
	gen := func(indexDraft schema.Draft) error {
		var s schema.Schema
		if err := json.Unmarshal([]byte(dialectFixture), &s); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		s.NormalizeForDraft(schema.Draft202012)
		x := schema.NewResourceIndex(nil, schema.WithIndexDraft(indexDraft))
		_, err := New(Config{PackageName: "testpkg", Draft: schema.Draft202012, Resolver: x}).Generate(&s)
		return err
	}
	err := gen(schema.Draft07)
	if err == nil {
		t.Fatal("an index built for draft-07 generated under Config.Draft 2020-12")
	}
	for _, want := range []string{"Config.Draft is Draft 2020-12", "under Draft-07", "WithIndexDraft"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if err := gen(schema.Draft202012); err != nil {
		t.Errorf("an index built for Config.Draft's own draft was refused: %v", err)
	}
}
