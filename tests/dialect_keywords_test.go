package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// Issue #203: the dialect was not consulted for keyword availability, in either
// direction. A keyword was enforced in dialects that predate it -- draft 4 has
// no `const`, draft 7 no `dependentRequired` -- and a spelling a later draft
// removed went on being honoured, so a draft-6 document's `divisibleBy` still
// bound. Six keywords were gated, each by a predicate of its own, and
// twenty-nine were not.
//
// These fixtures are run from the schema *body* rather than from a file per
// dialect, and that is the point of the shape. Every case states its verdict
// under both dialects side by side, off one body, so the two cannot drift apart:
// the arm that must ignore the keyword and the arm that must enforce it are
// literally the same schema with one URI changed. A fix that stopped enforcing
// the keyword everywhere passes the first column and fails the second, which is
// the amputation these have to tell from a fix.
//
// They compile and run, because none of this is visible in the IR or the emitted
// source: a check that should not have been emitted and a check for a keyword
// the schema never stated produce source that reads perfectly well either way.

type dialectArm struct {
	// Name is the arm's label in the test output.
	Name string
	// URI is the $schema the body is given for this arm.
	URI string
}

type dialectInstance struct {
	Doc string
	// Without is the verdict under the dialect that does not define the
	// keywords; With, under the one that does.
	Without, With bool
	Why           string
}

type dialectFixture struct {
	Name string
	// Body is the schema with no $schema member; each arm supplies one.
	Body string
	// Without is the dialect that has never heard of the keywords the body
	// states; With is the one that defines them.
	Without, With dialectArm
	Instances     []dialectInstance
}

const (
	uriDraft03 = "http://json-schema.org/draft-03/schema#"
	uriDraft04 = "http://json-schema.org/draft-04/schema#"
	uriDraft06 = "http://json-schema.org/draft-06/schema#"
	uriDraft07 = "http://json-schema.org/draft-07/schema#"
	uriDraft19 = "https://json-schema.org/draft/2019-09/schema"
)

func dialectKeywordFixtures() []dialectFixture {
	return []dialectFixture{
		{
			// The case the issue reports, and its mirror, and both controls, in
			// one body. exclusiveMinimum/exclusiveMaximum kept their name and
			// changed their type: a boolean modifying the sibling bound in drafts
			// 3 and 4, the bound itself from draft 6. Each dialect knows one
			// spelling and has to read the other as an unknown value.
			//
			// The pairing is what makes this the sharpest instance. `num` and
			// `numMax` state the modern spelling and `bool` and `boolMax` the
			// legacy one, so each arm has two properties it must enforce and two
			// it must ignore -- and a generator that simply stopped emitting the
			// keyword would fail the two it must enforce.
			Name: "exclusive bounds change spelling at draft 6",
			Body: `{
				"type": "object",
				"properties": {
					"num":     {"type": "number", "minimum": 3, "exclusiveMinimum": 5},
					"bool":    {"type": "number", "minimum": 5, "exclusiveMinimum": true},
					"numMax":  {"type": "number", "maximum": 7, "exclusiveMaximum": 5},
					"boolMax": {"type": "number", "maximum": 5, "exclusiveMaximum": true}
				}
			}`,
			Without: dialectArm{"draft 4", uriDraft04},
			With:    dialectArm{"draft 6", uriDraft06},
			Instances: []dialectInstance{
				{Doc: `{"num":4}`, Without: true, With: false,
					Why: "the reported document: draft 4 defines exclusiveMinimum as a boolean, so the number is an " +
						"unknown value and only minimum:3 binds. Refusing 4 applies draft-6 semantics to a draft-4 document"},
				{Doc: `{"num":2}`, Without: false, With: false,
					Why: "the sibling minimum:3 must survive the dropped keyword; discarding it too would accept 2"},
				{Doc: `{"num":6}`, Without: true, With: true,
					Why: "above both readings, so it says nothing about which one is in force"},
				{Doc: `{"bool":5}`, Without: false, With: true,
					Why: "the mirror: draft 4's boolean makes minimum:5 exclusive, and draft 6 has no reading of a " +
						"boolean there, so minimum:5 binds inclusively and 5 is valid"},
				{Doc: `{"bool":4}`, Without: false, With: false, Why: "below minimum:5 under either reading"},
				{Doc: `{"bool":6}`, Without: true, With: true, Why: "above minimum:5 under either reading"},
				{Doc: `{"numMax":6}`, Without: true, With: false,
					Why: "exclusiveMaximum, the same way round: draft 4 ignores the number and only maximum:7 binds"},
				{Doc: `{"numMax":8}`, Without: false, With: false, Why: "the sibling maximum:7 survives in both"},
				{Doc: `{"boolMax":5}`, Without: false, With: true,
					Why: "draft 4's boolean makes maximum:5 exclusive; draft 6 reads maximum:5 inclusively"},
				{Doc: `{"boolMax":6}`, Without: false, With: false, Why: "above maximum:5 under either reading"},
			},
		},
		{
			// Draft 3 has none of the four composition keywords, no multipleOf
			// (it spells that divisibleBy), no min/maxProperties, and spells
			// `required` on the property as a boolean rather than on the parent
			// as an array.
			Name: "draft 4's keyword set written in draft 3",
			Body: `{
				"type": "object",
				"properties": {
					"mult":  {"type": "integer", "multipleOf": 2},
					"comp":  {"type": "integer", "allOf": [{"minimum": 5}]},
					"any":   {"anyOf": [{"type": "string"}, {"type": "boolean"}]},
					"one":   {"type": "integer", "oneOf": [{"minimum": 5}]},
					"neg":   {"type": "integer", "not": {"minimum": 5}},
					"sized": {"type": "object", "minProperties": 1, "maxProperties": 1,
					          "properties": {"x": {"type": "integer"}, "y": {"type": "integer"}}},
					"req":   {"type": "object", "properties": {"x": {"type": "integer"}}, "required": ["x"]}
				}
			}`,
			Without: dialectArm{"draft 3", uriDraft03},
			With:    dialectArm{"draft 4", uriDraft04},
			Instances: []dialectInstance{
				{Doc: `{"mult":3}`, Without: true, With: false, Why: "draft 3 spells this divisibleBy and has no multipleOf"},
				{Doc: `{"comp":1}`, Without: true, With: false, Why: "draft 3 has no allOf; it spells the intersection `extends`"},
				{Doc: `{"any":1}`, Without: true, With: false,
					Why: "draft 3 has no anyOf. The branches are two types rather than a bound, because a " +
						"single-branch anyOf that only narrows a typed sibling is not enforced on any dialect -- " +
						"a case that cannot tell the dialects apart proves nothing about the gate"},
				{Doc: `{"one":1}`, Without: true, With: false, Why: "draft 3 has no oneOf"},
				{Doc: `{"neg":7}`, Without: true, With: false, Why: "draft 3 has no not; it spells the complement `disallow`"},
				{Doc: `{"sized":{}}`, Without: true, With: false, Why: "minProperties arrived in draft 4"},
				{Doc: `{"sized":{"x":1,"y":2}}`, Without: true, With: false, Why: "maxProperties arrived in draft 4"},
				{Doc: `{"req":{}}`, Without: true, With: false, Why: "the required array is draft 4's spelling"},
				{Doc: `{"mult":4,"comp":9,"any":"s","one":9,"neg":1,"sized":{"x":1},"req":{"x":1}}`,
					Without: true, With: true,
					Why: "the control: every keyword satisfied, so both dialects accept and a fix that " +
						"disabled the keywords outright cannot hide here"},
			},
		},
		{
			// const, contains and propertyNames arrived in draft 6; if/then/else
			// in draft 7. Draft 4 has none of them.
			Name: "draft 6 and 7 keywords written in draft 4",
			Body: `{
				"type": "object",
				"properties": {
					"c":     {"const": "a"},
					"arr":   {"type": "array", "items": {"type": ["string", "integer"]},
					          "contains": {"type": "integer"}},
					"names": {"type": "object", "propertyNames": {"maxLength": 2}},
					"cond":  {"type": "object",
					          "properties": {"a": {"type": "string"}, "b": {"type": "string"}},
					          "if": {"required": ["a"]}, "then": {"required": ["b"]}}
				}
			}`,
			Without: dialectArm{"draft 4", uriDraft04},
			With:    dialectArm{"draft 7", uriDraft07},
			Instances: []dialectInstance{
				{Doc: `{"c":"b"}`, Without: true, With: false, Why: "const arrived in draft 6"},
				{Doc: `{"arr":["x"]}`, Without: true, With: false, Why: "contains arrived in draft 6"},
				{Doc: `{"names":{"abc":1}}`, Without: true, With: false, Why: "propertyNames arrived in draft 6"},
				{Doc: `{"cond":{"a":"x"}}`, Without: true, With: false, Why: "if/then arrived in draft 7"},
				{Doc: `{"c":"a","arr":[1],"names":{"ab":1},"cond":{"a":"x","b":"y"}}`,
					Without: true, With: true, Why: "the control: every keyword satisfied"},
			},
		},
		{
			// The 2019-09 additions, written in draft 7. dependentRequired and
			// dependentSchemas are the split of draft 7's own `dependencies`, and
			// the split spelling is not draft 7's.
			//
			// Those two are the fixture's exceptions and they run opposite ways --
			// `dr` is gated and `ds` is not -- so this body holds both halves of
			// issue #197's decision, side by side off one schema. Neither can be
			// falsified by `make test-external`: the suite ships no file stating
			// either keyword under a dialect that predates it.
			Name: "2019-09 keywords written in draft 7",
			Body: `{
				"type": "object",
				"properties": {
					"dr": {"type": "object",
					       "properties": {"a": {"type": "integer"}, "b": {"type": "integer"}},
					       "dependentRequired": {"a": ["b"]}},
					"dep": {"type": "object",
					        "properties": {"a": {"type": "integer"}, "b": {"type": "integer"}},
					        "dependencies": {"a": ["b"]}},
					"ds": {"type": "object",
					       "properties": {"a": {"type": "integer"}, "b": {"type": "integer"}},
					       "dependentSchemas": {"a": {"required": ["b"]}}},
					"mc": {"type": "array", "items": {"type": "integer"},
					       "contains": {"type": "integer"}, "minContains": 2},
					"up": {"type": "object", "properties": {"a": {"type": "integer"}},
					       "unevaluatedProperties": false},
					"ui": {"type": "array", "allOf": [{"items": [{"type": "integer"}]}],
					       "unevaluatedItems": false}
				}
			}`,
			Without: dialectArm{"draft 7", uriDraft07},
			With:    dialectArm{"2019-09", uriDraft19},
			Instances: []dialectInstance{
				{Doc: `{"dr":{"a":1}}`, Without: true, With: false, Why: "dependentRequired arrived in 2019-09"},
				{Doc: `{"dep":{"a":1}}`, Without: false, With: false,
					Why: "the exception, and the only row in these fixtures where both arms enforce. 2019-09 " +
						"removed `dependencies` in favour of the pair above, so a reading of the specification " +
						"alone gates it -- and that reading was measured against the pinned suite and failed 25 " +
						"groups: upstream ships optional/dependencies-compatibility.json for 2019-09, 2020-12 " +
						"and v1 and marks the keyword binding in all three. This is what holds that decision " +
						"without waiting for `make test-external`; see the `dependencies` row in " +
						"pkg/schema/keyworddialects.go"},
				{Doc: `{"ds":{"a":1}}`, Without: false, With: false,
					Why: "the second exception, and the deliberate deviation issue #197 decided. " +
						"dependentSchemas arrived in 2019-09, so a plain reading gates it under draft 7 -- and " +
						"unlike `dependencies` above there is no measurement to appeal to, because upstream " +
						"ships no dependentSchemas compatibility file for any dialect. The call is that a " +
						"document writing the keyword in full meant it, and that dropping it is the failure " +
						"nobody can see. Note this row runs opposite to `dr` two lines up, which is the " +
						"asymmetry the keywordDialects comment argues for: draft 7 can express " +
						"dependentRequired as `dependencies` with an array and has no need of the later " +
						"spelling. Re-gating the row flips this line and nothing else; see the " +
						"`dependentSchemas` row in pkg/schema/keyworddialects.go"},
				{Doc: `{"mc":[1]}`, Without: true, With: false, Why: "minContains arrived in 2019-09"},
				{Doc: `{"up":{"b":1}}`, Without: true, With: false, Why: "unevaluatedProperties arrived in 2019-09"},
				{Doc: `{"ui":[1,2]}`, Without: true, With: false, Why: "unevaluatedItems arrived in 2019-09"},
				{Doc: `{"dr":{"a":1,"b":2},"ds":{"a":1,"b":2},"dep":{"a":1,"b":2},"mc":[1,2],"up":{"a":1},"ui":[1]}`,
					Without: true, With: true, Why: "the control: every keyword satisfied"},
			},
		},
		{
			// The backward direction. Each of these is draft 3's alone, and each
			// went on being honoured in every later dialect including v1 --
			// which is over-enforcement, the direction that refuses documents the
			// dialect permits.
			Name: "draft 3's own spellings written in draft 6",
			Body: `{
				"type": "object",
				"properties": {
					"div": {"type": "integer", "divisibleBy": 2},
					"ext": {"type": "integer", "extends": {"minimum": 5}},
					"dis": {"disallow": "string"},
					"req": {"type": "object", "properties": {"x": {"type": "integer", "required": true}}}
				}
			}`,
			Without: dialectArm{"draft 6", uriDraft06},
			With:    dialectArm{"draft 3", uriDraft03},
			Instances: []dialectInstance{
				{Doc: `{"div":3}`, Without: true, With: false, Why: "divisibleBy is draft 3's; draft 6 spells it multipleOf"},
				{Doc: `{"ext":1}`, Without: true, With: false, Why: "extends is draft 3's; draft 6 spells it allOf"},
				{Doc: `{"dis":"a"}`, Without: true, With: false, Why: "disallow is draft 3's; draft 6 spells it not"},
				{Doc: `{"req":{}}`, Without: true, With: false,
					Why: "the per-property boolean required is draft 3's; draft 6 takes an array on the parent"},
				{Doc: `{"div":4,"ext":9,"dis":1,"req":{"x":1}}`, Without: true, With: true,
					Why: "the control: every draft-3 keyword satisfied, so draft 3 accepts too"},
			},
		},
	}
}

// TestKeywordAvailabilityFollowsTheDialect compiles each body under both
// dialects and puts every document to the generated type.
func TestKeywordAvailabilityFollowsTheDialect(t *testing.T) {
	for _, fx := range dialectKeywordFixtures() {
		t.Run(fx.Name, func(t *testing.T) {
			for _, arm := range []struct {
				dialectArm
				want func(dialectInstance) bool
			}{
				{fx.Without, func(in dialectInstance) bool { return in.Without }},
				{fx.With, func(in dialectInstance) bool { return in.With }},
			} {
				t.Run(arm.Name, func(t *testing.T) {
					instances := make([]notInstance, 0, len(fx.Instances))
					for _, in := range fx.Instances {
						instances = append(instances, notInstance{
							Name: in.Doc, Doc: in.Doc, Valid: arm.want(in), Why: in.Why,
						})
					}
					runDialectArm(t, fx.Body, arm.URI, instances)
				})
			}
		})
	}
}

// runDialectArm generates from body with the given $schema, compiles it and runs
// every document against the result.
func runDialectArm(t *testing.T, body, uri string, instances []notInstance) {
	t.Helper()
	mainGo, err := notInstanceMain("Root", instances)
	if err != nil {
		t.Fatalf("building main.go: %v", err)
	}
	runDialectProgram(t, body, uri, generator.Config{
		PackageName: "testpkg", OmitEmpty: true, RootTypeName: "Root",
	}, mainGo)
}

// runDialectProgram generates from body with the given $schema under cfg,
// compiles it beside mainGo and runs the result, which must print PASS.
//
// The body is the unit rather than a fixture file because these tests vary one
// thing: two arms of the same schema under two dialects. A file per arm is two
// files that have to be kept identical by hand, and the assertion is only worth
// anything while they are.
func runDialectProgram(t *testing.T, body, uri string, cfg generator.Config, mainGo string) {
	t.Helper()

	src := withSchemaKeyword(t, body, uri)
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

	tmpDir := t.TempDir()
	generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}
	writeSharedHelpers(t, tmpDir, generatedMain)
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}
	if err := writeTestGoMod(tmpDir, "dialect_keywords_test"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	out, runErr := cmd.CombinedOutput()
	text := programOutput(out)
	if runErr != nil || text != "PASS" {
		t.Fatalf("$schema %s:\n%s", uri, text)
	}
}

// withSchemaKeyword inserts a $schema member into a body that states none.
//
// It goes through the JSON rather than through string surgery so that a body
// that already declares one is caught here, rather than silently taking the
// dialect the fixture was meant to vary.
func withSchemaKeyword(t *testing.T, body, uri string) string {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		t.Fatalf("fixture body is not a JSON object: %v\n%s", err, body)
	}
	if _, ok := obj["$schema"]; ok {
		t.Fatalf("fixture body declares its own $schema; the dialect is what these fixtures vary")
	}
	obj["$schema"] = json.RawMessage(`"` + uri + `"`)
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("re-marshalling fixture body: %v", err)
	}
	return string(out)
}

// TestAnnotationVocabularyStrictModeFollowsTheDialect is the running-program
// half of the annotation-keyword spans, and the reason it exists is that
// `make test-external` cannot supply one.
//
// readOnly, writeOnly and deprecated change no validation verdict in any draft
// that defines them, so the official suite has no case for any of the three --
// an audit of the pinned checkout found them stated by no suite schema at all,
// in any draft directory. The external run therefore passes whatever span the
// table gives them, in either direction. A gate nothing can falsify is a gate
// that proves nothing, so the falsification is written here.
//
// --strict-read-write is what makes two of them observable from outside: it
// turns readOnly into a decode that refuses the property and writeOnly into an
// encode that omits it. Both keywords arrived in draft 7, so a draft-6 document
// writing them states two words its dialect has never heard of, and the
// generated type must behave as though neither were there. deprecated reaches
// only a doc comment and is held on the IR instead, by
// TestAnnotationVocabularyFollowsTheDialect.
func TestAnnotationVocabularyStrictModeFollowsTheDialect(t *testing.T) {
	const body = `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"serverID": {"type": "string", "readOnly": true},
			"secret":   {"type": "string", "writeOnly": true},
			"plain":    {"type": "string"}
		}
	}`

	// The program is one text with the expected verdict passed in, so the two
	// arms cannot drift into asserting different things about the same schema.
	program := func(keywordsBind bool) string {
		return `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const bind = ` + fmt.Sprintf("%t", keywordsBind) + `

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func main() {
	// readOnly: under a dialect that defines it, --strict-read-write refuses a
	// document that sets the property. Under one that does not, the keyword is
	// not there to refuse anything.
	var v Doc
	err := json.Unmarshal([]byte(` + "`" + `{"serverID":"srv-7"}` + "`" + `), &v)
	switch {
	case bind && err == nil:
		fail("a readOnly property was accepted although the dialect defines readOnly")
	case bind && !strings.Contains(err.Error(), "read-only property may not be set"):
		fail("decode failed for the wrong reason: %v", err)
	case !bind && err != nil:
		fail("a dialect with no readOnly keyword refused the property anyway: %v", err)
	}

	// A property carrying no annotation decodes under both, so the arm above
	// cannot be satisfied by a type that refuses everything.
	var control Doc
	if err := json.Unmarshal([]byte(` + "`" + `{"plain":"p"}` + "`" + `), &control); err != nil {
		fail("control document refused: %v", err)
	}

	// writeOnly: under a dialect that defines it, MarshalJSON omits the property.
	var w Doc
	if err := json.Unmarshal([]byte(` + "`" + `{"secret":"hunter2","plain":"p"}` + "`" + `), &w); err != nil {
		fail("decoding a writeOnly property failed: %v", err)
	}
	out, err := json.Marshal(w)
	if err != nil {
		fail("marshal: %v", err)
	}
	emitted := strings.Contains(string(out), "hunter2")
	if bind && emitted {
		fail("writeOnly property was emitted although the dialect defines writeOnly: %s", out)
	}
	if !bind && !emitted {
		fail("a dialect with no writeOnly keyword omitted the property anyway: %s", out)
	}
	// The sibling must survive either way, so "omitted" is not "emitted nothing".
	if !strings.Contains(string(out), "\"plain\"") {
		fail("the unannotated property went missing too: %s", out)
	}

	fmt.Println("PASS")
}
`
	}

	cfg := generator.Config{PackageName: "testpkg", OmitEmpty: true, StrictReadWrite: true}
	for _, tt := range []struct {
		name string
		uri  string
		bind bool
	}{
		{"draft 6 has neither keyword", uriDraft06, false},
		{"draft 7 introduced both", uriDraft07, true},
		{"2019-09 keeps both", uriDraft19, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runDialectProgram(t, body, tt.uri, cfg, program(tt.bind))
		})
	}
}
