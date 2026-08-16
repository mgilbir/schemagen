package tests

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// The four documents issue #332 is about, and the verdicts every implementation
// gives them. One resource, r, is entered from two different anchored callers,
// and its $recursiveRef -- $dynamicRef under 2020-12 -- means a when r is
// reached through a and b when it is reached through b.
//
// The middle two are the pair that matters, and they are a pair on purpose:
// they differ only in what "v" holds, and they must come out on opposite sides.
// A generated type that binds the reference to one resource and calls it the
// answer gets one of them wrong whichever it binds -- and, when the binding is r
// itself, gets three of the four wrong, in both directions at once.
//
// Not this project's reasoning: python-jsonschema 4.26.0, go-jsonschema
// (santhosh-tekuri) v6.0.2, js-ajv 8.20.0 and rust-boon were asked through
// Bowtie, with a, b and r supplied as a registry and main.json as the schema,
// under both dialects. All four agree on all four documents, in both, with
// nothing errored or skipped, so there is no split to record.
var (
	twoCallersValid = []string{
		// v is reached through a, so it is judged by a: "x" is what it needs.
		`{"a":{"x":1,"r":{"c":1,"v":{"x":9}}}}`,
		// And through b it is judged by b, which asks for "y" instead.
		`{"b":{"y":1,"r":{"c":1,"v":{"y":9}}}}`,
	}
	twoCallersInvalid = []string{
		// a's answer, down b's path. Accepting this is the false acceptance a
		// binding to a produces.
		`{"b":{"y":1,"r":{"c":1,"v":{"x":9}}}}`,
		// r's own answer, down b's path. Accepting this is the false acceptance
		// a binding to r produces -- which is what the generator did before
		// #332, since a fragment type seeds its scope at itself (#293).
		`{"b":{"y":1,"r":{"c":1,"v":{"c":9}}}}`,
	}
)

// TestTwoCallersResolvesPerCaller is issue #332's four verdicts, through
// generate-compile-run, on the shape the runtime evaluator can take.
//
// It is the positive half of the pair. Every keyword in these fixtures is one
// the evaluator models, so dynamicScopeDecidesTheTarget routes the document
// there and the reference is resolved against the resources the *value* entered
// -- which is the only mechanism in the generator that can give two callers two
// answers. The verdicts below are then simply right, and they are the ones the
// four implementations give.
//
// The negative half is TestTwoCallersRefusedWhenTheEvaluatorCannotTakeIt, on the
// same shape carrying one keyword the evaluator does not model. Between them
// they say the whole rule: where the answer can be expressed it is, and where it
// cannot the schema is refused rather than bound to a guess.
//
// Root, not a fragment type, is what the cases are run against, and that is the
// point. The disagreement is about a value's *path* through the document, so it
// only exists in a document: a caller holding an RJSON has entered r and nothing
// else, and r's own answer is the right one for it.
func TestTwoCallersResolvesPerCaller(t *testing.T) {
	for _, dir := range []string{
		"testdata/schemas/regression/two_callers_2019_modelled",
		"testdata/schemas/regression/two_callers_2020_modelled",
	} {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			runValidationCasesOn(t, dir+"/main.json", "Root", generator.Config{
				PackageName: "testpkg",
				OmitEmpty:   true,
				// a.json, b.json and r.json are siblings of main.json, and
				// without a resolver rooted beside it the document is three
				// unresolved references rather than the shape being tested.
				Resolver: schema.NewFileResolver(filepath.Join("..", dir)),
			}, twoCallersValid, twoCallersInvalid)
		})
	}
}

// TestTwoCallersRefusedWhenTheEvaluatorCannotTakeIt is issue #332 itself: the
// same four resources, with one keyword the runtime evaluator does not model,
// and therefore no path left that can express what the reference means.
//
// What used to happen here is the defect. dynamicScopeDecidesTheTarget fired --
// it always did; the reach holds three anchored resources and the count is what
// it is -- and runtimeSchemaDef then declined over the unmodelled keyword, and
// the nil it returned dropped the schema onto the static arms, which resolved
// the reference once and emitted a struct. Against the four documents above that
// struct answered invalid, invalid, invalid, valid: three wrong, and wrong in
// both directions, with nothing in the generated source saying so.
//
// So the assertion is that generation is refused, and that the message says
// enough to act on: which keyword has no settled answer, how many declarations
// of its anchor are in reach, and what stopped the evaluator -- which is the one
// part a schema author can remove. Removing it is what the _modelled fixtures
// beside these are, and TestTwoCallersResolvesPerCaller is what they then get.
//
// Both dialects, because $recursiveAnchor/$recursiveRef and
// $dynamicAnchor/$dynamicRef reach the refusal by different arms of
// dynamicRefTarget and only one of them names an anchor.
func TestTwoCallersRefusedWhenTheEvaluatorCannotTakeIt(t *testing.T) {
	for _, tt := range []struct {
		dir  string
		want []string
	}{
		{
			dir: "testdata/schemas/regression/two_callers_2019",
			want: []string{
				"a $recursiveRef under this schema",
				"the $recursiveAnchor of the outermost resource in scope",
				"3 of the resources it reaches declare that anchor",
				`the runtime evaluator declined to compile it: a schema under it states "x-unmodelled"`,
			},
		},
		{
			dir: "testdata/schemas/regression/two_callers_2020",
			want: []string{
				"a $dynamicRef under this schema",
				`$dynamicAnchor "node"`,
				"3 of the resources it reaches declare that anchor",
				`the runtime evaluator declined to compile it: a schema under it states "x-unmodelled"`,
			},
		},
	} {
		t.Run(filepath.Base(tt.dir), func(t *testing.T) {
			path := filepath.Join("..", tt.dir, "main.json")
			s, err := schema.LoadFromFile(path)
			if err != nil {
				t.Fatalf("loading %s: %v", path, err)
			}
			s.NormalizeForDraft(schema.DraftUnknown)
			gen := generator.New(generator.Config{
				PackageName: "testpkg",
				OmitEmpty:   true,
				Resolver:    schema.NewFileResolver(filepath.Join("..", tt.dir)),
			})
			ir, err := gen.Generate(s)
			if err == nil {
				// Not a bare "want error". A type produced here is the defect,
				// so the report says what it would have done with the documents
				// above rather than only that it existed.
				em, emErr := emitter.New()
				if emErr != nil {
					t.Fatalf("creating emitter: %v", emErr)
				}
				src, emitErr := em.Emit(ir)
				if emitErr != nil {
					t.Fatalf("emitting: %v", emitErr)
				}
				t.Fatalf("generation succeeded, so the $recursiveRef or $dynamicRef here was bound to one resource "+
					"and the type answers for callers that reach r another way. That is issue #332: %s is accepted "+
					"or %s rejected depending which resource won. Generated:\n%s",
					twoCallersInvalid[1], twoCallersValid[1], string(src))
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q, so a caller cannot tell what could not be decided or why.\nGot: %v", want, err)
				}
			}
		})
	}
}
