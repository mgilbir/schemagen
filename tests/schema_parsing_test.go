package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// The behavioural half of the parsing fixes in pkg/schema: each test here is a
// schema as a user writes it, generated, compiled and run against documents,
// because a parse-level assertion says what the tree holds and not what the
// generated type then does with it.

// runInlineSchema generates src under cfg (normalizing under cfg.Draft, as the
// CLI does), compiles it and runs every instance against the type named Root.
func runInlineSchema(t *testing.T, src string, cfg generator.Config, instances []notInstance) {
	t.Helper()
	if cfg.PackageName == "" {
		cfg.PackageName = "testpkg"
	}
	cfg.OmitEmpty = true
	cfg.RootTypeName = "Root"

	var s schema.Schema
	if err := json.Unmarshal([]byte(src), &s); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, src)
	}
	s.NormalizeForDraft(cfg.Draft)
	ir, err := generator.New(cfg).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v\n%s", err, src)
	}
	em, err := emitter.New()
	if err != nil {
		t.Fatalf("emitter: %v", err)
	}
	generated, err := em.Emit(ir)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	mainGo, err := notInstanceMain("Root", instances)
	if err != nil {
		t.Fatalf("building main.go: %v", err)
	}

	dir := t.TempDir()
	code := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSharedHelpers(t, dir, code)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeCogenGoMod(dir, strings.Contains(code, "pkg/validationruntime")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, runErr := testgo.Command(ctx, dir, "run", ".").CombinedOutput()
	if text := programOutput(out); runErr != nil || text != "PASS" {
		t.Fatalf("%s:\n%s", src, text)
	}
}

// generateInline runs the same pipeline as runInlineSchema up to generation and
// returns its error.
func generateInline(t *testing.T, src string, cfg generator.Config) error {
	t.Helper()
	cfg.PackageName = "testpkg"
	var s schema.Schema
	if err := json.Unmarshal([]byte(src), &s); err != nil {
		return err
	}
	s.NormalizeForDraft(cfg.Draft)
	_, err := generator.New(cfg).Generate(&s)
	return err
}

// TestDraft3BooleanRequiredNeverRequiresAName: draft 3's `"required": true`
// used to be stored as the magic property name "\x00__draft3_required_true__"
// in the Required list and promoted only under "properties". Anywhere else it
// reached the generator as a required property of that name, and the type
// refused every object. And a 2020-12 document that really requires a property
// of that name had the requirement read as draft 3's boolean and gated away.
func TestDraft3BooleanRequiredNeverRequiresAName(t *testing.T) {
	const draft3 = `"$schema":"http://json-schema.org/draft-03/schema#"`
	for _, tc := range []struct {
		name, src string
		instances []notInstance
	}{
		{"at a draft-3 root", `{` + draft3 + `,"type":"object","required":true}`, []notInstance{
			{Name: "empty object", Doc: `{}`, Valid: true,
				Why: "the root is always present, so its own required:true asks nothing -- it used to require the marker"},
			{Name: "a string", Doc: `"x"`, Valid: false, Why: "control: the type still binds"},
		}},
		{"under additionalProperties with no $schema", `{"additionalProperties":{"type":"object","required":true}}`, []notInstance{
			{Name: "an extra member", Doc: `{"k":{}}`, Valid: true,
				Why: "a present member's own required:true asks nothing -- it used to require the marker of the member"},
			{Name: "a non-object member", Doc: `{"k":1}`, Valid: false, Why: "control: the member's type still binds"},
		}},
		{"on a draft-3 property", `{` + draft3 + `,"type":"object","properties":{"a":{"type":"integer","required":true},"b":{"type":"integer"}}}`, []notInstance{
			{Name: "the property present", Doc: `{"a":1}`, Valid: true, Why: "control"},
			{Name: "the property absent", Doc: `{"b":1}`, Valid: false,
				Why: "draft 3's boolean on a property is the parent's requirement"},
		}},
		{"a real property of the marker's name", `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["\u0000__draft3_required_true__"]}`, []notInstance{
			{Name: "the property absent", Doc: `{}`, Valid: false,
				Why: "the document requires a property of this name; it used to be taken for draft 3's boolean and gated away"},
			{Name: "the property present", Doc: `{"\u0000__draft3_required_true__":1}`, Valid: true, Why: "control"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runInlineSchema(t, tc.src, generator.Config{}, tc.instances)
		})
	}
}

// TestLegacyKeywordSubschemasFollowTheirDialect is the behavioural half of the
// dialect pass reaching every subschema: a draft-4 document's
// dependencies.a.properties.b.const was enforced, although draft 4 has no
// const, because the dependencies subschemas were only parsed after the pass
// had run.
func TestLegacyKeywordSubschemasFollowTheirDialect(t *testing.T) {
	const body = `"type":"object","dependencies":{"a":{"properties":{"b":{"const":1}}}}`
	for _, tc := range []struct {
		uri      string
		enforced bool
	}{
		{"http://json-schema.org/draft-04/schema#", false},
		{"http://json-schema.org/draft-03/schema#", false},
		{"http://json-schema.org/draft-06/schema#", true},
		{"http://json-schema.org/draft-07/schema#", true},
		{"https://json-schema.org/draft/2020-12/schema", true},
	} {
		t.Run(tc.uri, func(t *testing.T) {
			runInlineSchema(t, `{"$schema":"`+tc.uri+`",`+body+`}`, generator.Config{}, []notInstance{
				{Name: "b is not the const", Doc: `{"a":1,"b":2}`, Valid: !tc.enforced,
					Why: "const binds exactly where the dialect defines it"},
				{Name: "b is the const", Doc: `{"a":1,"b":1}`, Valid: true, Why: "control"},
				{Name: "no a", Doc: `{"b":2}`, Valid: true, Why: "control: the dependency does not apply"},
			})
		})
	}
	// Draft 3's own keywords, holding a keyword draft 3 does not have.
	runInlineSchema(t, `{"$schema":"http://json-schema.org/draft-03/schema#","extends":{"allOf":[{"type":"string"}]},"disallow":[{"anyOf":[{"type":"integer"}]}]}`,
		generator.Config{}, []notInstance{
			{Name: "an integer", Doc: `5`, Valid: true, Why: "draft 3 has neither allOf nor anyOf, inside extends or disallow alike"},
			{Name: "a string", Doc: `"x"`, Valid: true, Why: "control"},
		})
}

// TestAForcedDialectReadsEveryKeywordFormAsThatDialect is --draft on a
// document written for another dialect: a keyword form the forced dialect does
// not define is refused, naming the flag. A draft-07 tuple forced to 2020-12
// used to keep "items" as a tuple and lose "additionalItems", so ["x",1,2]
// passed under neither dialect's reading; ignoring both would have been no
// better, dropping the tuple the author wrote in silence.
func TestAForcedDialectReadsEveryKeywordFormAsThatDialect(t *testing.T) {
	const src = `{"$schema":"http://json-schema.org/draft-07/schema#","type":"array","items":[{"type":"string"}],"additionalItems":false}`
	for _, d := range []schema.Draft{schema.Draft202012, schema.DraftV1} {
		err := generateInline(t, src, generator.Config{Draft: d})
		for _, want := range []string{`#/items: "items" is written as an array of schemas`, "prefixItems",
			d.String() + " does not define that form", "the document's own $schema declares Draft-07"} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("--draft %v: err = %v, want it to say %q", d, err, want)
			}
		}
	}
	for _, tc := range []struct {
		draft     schema.Draft
		instances []notInstance
	}{
		{schema.Draft07, []notInstance{
			{Name: "past the tuple", Doc: `["x",1,2]`, Valid: false, Why: "draft 7: additionalItems:false closes the tuple"},
			{Name: "a wrong first element", Doc: `[1]`, Valid: false, Why: "draft 7: the tuple types position 0"},
			{Name: "the tuple", Doc: `["x"]`, Valid: true, Why: "control"},
		}},
		{schema.Draft201909, []notInstance{
			{Name: "past the tuple", Doc: `["x",1,2]`, Valid: false, Why: "2019-09 still has the tuple form"},
			{Name: "a wrong first element", Doc: `[1]`, Valid: false, Why: "2019-09 still has the tuple form"},
		}},
	} {
		t.Run(tc.draft.String(), func(t *testing.T) {
			runInlineSchema(t, src, generator.Config{Draft: tc.draft}, tc.instances)
		})
	}
}

// TestMalformedKeywordValuesAreRefusedWithAPointer: a value no dialect gives a
// keyword is refused, naming where it is, wherever the keyword is defined --
// the policy a null subschema already had. {"type":[1,2]} used to generate a
// type with no type at all, and {"dependencies":{"a":null}} one whose
// dependency every object satisfied.
func TestMalformedKeywordValuesAreRefusedWithAPointer(t *testing.T) {
	for _, tc := range []struct {
		src, want string
	}{
		{`{"type":[1,2]}`, "#/type/0: must be a type name or a schema"},
		{`{"$schema":"http://json-schema.org/draft-07/schema#","dependencies":{"a":null}}`, "#/dependencies/a: schema is null"},
		{`{"$schema":"http://json-schema.org/draft-07/schema#","properties":{"p":{"minLength":-1}}}`, "#/properties/p/minLength"},
		{`{"$schema":"http://json-schema.org/draft-07/schema#","properties":{"p":{"maxLength":"3"}}}`, "#/properties/p/maxLength"},
		{`{"$schema":"https://json-schema.org/draft/2020-12/schema","required":["a",null]}`, "#/required: contains null"},
		{`{"$schema":"https://json-schema.org/draft/2020-12/schema","not":null}`, "#/not: schema is null"},
		{`{"$schema":"https://json-schema.org/draft/2020-12/schema","$defs":{"d":{"$id":"http://[::1"}}}`, "#/$defs/d/$id: not a URI-reference"},
	} {
		err := generateInline(t, tc.src, generator.Config{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one containing %q", tc.src, err, tc.want)
		}
	}
	// The same values under a dialect that does not define the keyword say
	// nothing, and the document generates.
	for _, src := range []string{
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","divisibleBy":"x","extends":5,"disallow":[null]}`,
		`{"$schema":"http://json-schema.org/draft-04/schema#","const":null,"contains":5,"propertyNames":null,"$id":5}`,
		`{"$schema":"http://json-schema.org/draft-06/schema#","if":7,"contentMediaType":1,"dependentRequired":{"a":[null]}}`,
		`{"$schema":"http://json-schema.org/draft-03/schema#","maxLength":-1}`,
	} {
		if err := generateInline(t, src, generator.Config{}); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
}

// TestBothSpellingsBindUnderNoDialect: a document naming no dialect this
// package recognises is read as the union of them (keywordDialect.definedIn),
// so draft 3's spelling of a keyword and the one that replaced it can both
// stand -- and then both bind. Each legacy rewrite assumed it was alone: an
// existing "not" made "disallow" a no-op, an existing "multipleOf" did the same
// to "divisibleBy", and "dependencies" overwrote a "dependentSchemas" or
// "dependentRequired" entry for the same property.
func TestBothSpellingsBindUnderNoDialect(t *testing.T) {
	const src = `{"type":"object","properties":{
		"n": {"not":{"type":"string"},"disallow":"integer"},
		"m": {"multipleOf":2,"divisibleBy":3},
		"d": {"type":"object","dependentSchemas":{"a":{"required":["x"]}},"dependencies":{"a":{"required":["y"]}}},
		"r": {"type":"object","dependentRequired":{"a":["x"]},"dependencies":{"a":["y"]}}
	}}`
	runInlineSchema(t, src, generator.Config{}, []notInstance{
		{Name: "not's type", Doc: `{"n":"s"}`, Valid: false, Why: "control: not binds"},
		{Name: "disallow's type", Doc: `{"n":1}`, Valid: false, Why: "disallow binds beside not; it was dropped when not was present"},
		{Name: "neither", Doc: `{"n":true}`, Valid: true, Why: "control"},
		{Name: "a multiple of both", Doc: `{"m":6}`, Valid: true, Why: "control"},
		{Name: "a multiple of 2 only", Doc: `{"m":4}`, Valid: false, Why: "divisibleBy binds beside multipleOf; it was dropped"},
		{Name: "a multiple of 3 only", Doc: `{"m":9}`, Valid: false, Why: "control: multipleOf binds"},
		{Name: "both dependent schemas met", Doc: `{"d":{"a":1,"x":1,"y":1}}`, Valid: true, Why: "control"},
		{Name: "only dependentSchemas met", Doc: `{"d":{"a":1,"x":1}}`, Valid: false, Why: "control: dependencies binds"},
		{Name: "only dependencies met", Doc: `{"d":{"a":1,"y":1}}`, Valid: false, Why: "dependencies used to overwrite the dependentSchemas entry"},
		{Name: "both required lists met", Doc: `{"r":{"a":1,"x":1,"y":1}}`, Valid: true, Why: "control"},
		{Name: "only dependencies' list met", Doc: `{"r":{"a":1,"y":1}}`, Valid: false, Why: "dependencies used to overwrite the dependentRequired entry"},
		{Name: "only dependentRequired's list met", Doc: `{"r":{"a":1,"x":1}}`, Valid: false, Why: "control: dependencies binds"},
	})
}
