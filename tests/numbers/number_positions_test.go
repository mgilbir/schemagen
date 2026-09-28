package numbers

import (
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/tests/internal/testsupport"
)

// The positions below are ones the number verdict grid does not reach in the
// shape that went wrong, each a check that read a number from a value of a type
// it did not expect -- and so did not read it at all. They were found by
// rewriting every numeric check onto the exact core, which asks what a number
// is in whatever Go type it is held rather than asserting one type.

// numberPositionConfig is the configuration every case here is generated
// under unless it says otherwise: the ordinary one, with the root named so the
// runner can put each document to it whatever shape the root has.
func numberPositionConfig() generator.Config {
	return generator.Config{PackageName: "testpkg", OmitEmpty: true, RootTypeName: "Root"}
}

// runNumberPositionCases puts every document in valid and invalid to the
// schema's root and requires each to be admitted or refused as listed. A
// refusal is either stage's: an unmarshal error or a Validate error.
func runNumberPositionCases(t *testing.T, schemaPath string, cfg generator.Config, valid, invalid []string) {
	t.Helper()
	var instances []testsupport.NotInstance
	for _, doc := range valid {
		instances = append(instances, testsupport.NotInstance{Name: "admits " + doc, Doc: doc, Valid: true, Why: "the schema admits it"})
	}
	for _, doc := range invalid {
		instances = append(instances, testsupport.NotInstance{Name: "refuses " + doc, Doc: doc, Valid: false, Why: "the schema refuses it"})
	}
	testsupport.RunInstanceFixturesWithConfig(t, "number_positions_test", []testsupport.NotFixture{
		{Name: "cases", SchemaPath: schemaPath, Instances: instances},
	}, cfg)
}

// TestUnevaluatedIntegerReadsTheLiteral: an unevaluated property's value was
// decoded into the int64 its "integer" maps to, which refuses 1.0 -- an
// integer from draft 6 on -- and the bound was compared on that int64.
func TestUnevaluatedIntegerReadsTheLiteral(t *testing.T) {
	t.Parallel()
	runNumberPositionCases(t, "testdata/schemas/regression/numbers_unevaluated_integer.json", numberPositionConfig(),
		[]string{`{"b":1.0}`, `{"b":1e2}`, `{"b":9007199254740992}`, `{"a":"x","b":-5}`},
		[]string{`{"b":1.5}`, `{"b":"1"}`, `{"b":9007199254740993}`, `{"b":1e400}`})
}

// TestUntypedBoundJudgesANumberFloat64CannotHold: a schema stating a bound
// and no type is held by a wrapper that decodes a number into a float64 and
// keeps anything else as bytes it does not judge. 1e400 is a number no
// float64 holds, so it was kept as bytes and passed over as though it were a
// string.
func TestUntypedBoundJudgesANumberFloat64CannotHold(t *testing.T) {
	t.Parallel()
	runNumberPositionCases(t, "testdata/schemas/regression/numbers_untyped_bound.json", numberPositionConfig(),
		[]string{`{"v":"x"}`, `{"v":-1e400}`, `{"v":5}`, `{"v":4.99999999999999999999}`},
		[]string{`{"v":1e400}`, `{"v":5.00000000000000000001}`})
}

// TestNotBranchIsSatisfiedByAValueItsKeywordDoesNotJudge: a `not` over a
// disjunction counted a value a branch's numeric keyword does not apply to as
// not matching that branch, so {"type":"string","minimum":5} -- which every
// string satisfies -- did not match a string, and the `not` admitted it.
func TestNotBranchIsSatisfiedByAValueItsKeywordDoesNotJudge(t *testing.T) {
	t.Parallel()
	runNumberPositionCases(t, "testdata/schemas/regression/numbers_not_branch_vacuous.json", numberPositionConfig(),
		[]string{`3`, `null`, `[]`},
		[]string{`"abc"`, `""`, `true`})
}

// TestContainsEnumEvaluatesItems: an item a `contains` matches is an item it
// evaluated, and the unevaluatedItems check of an untyped array asked that of
// a const contains and never of an enum one -- so every item stayed
// unevaluated and a document the schema admits was refused. The members are
// compared by value.
func TestContainsEnumEvaluatesItems(t *testing.T) {
	t.Parallel()
	runNumberPositionCases(t, "testdata/schemas/regression/numbers_contains_enum_evaluates.json", numberPositionConfig(),
		[]string{`[1,2,1.0]`, `[2e0]`},
		[]string{`[1,3]`, `[]`, `["1"]`})
}

// TestContainsBoundsEvaluateItems: the same check read `minimum` and
// `maximum` of a contains and nothing else numeric, so an item an exclusive
// bound or a multipleOf refused was still counted as evaluated.
func TestContainsBoundsEvaluateItems(t *testing.T) {
	t.Parallel()
	runNumberPositionCases(t, "testdata/schemas/regression/numbers_contains_bounds_evaluate.json", numberPositionConfig(),
		[]string{`[6,7.5]`, `[5.5]`},
		[]string{`[6,5]`, `[6,6.25]`, `[5]`})
}

// TestEvaluatorNodeFieldsAreReadUnderAlignment: the runtime evaluator declares
// a node field -- format, a bound, multipleOf -- only when a node literal sets
// it, and the literal was found by matching `Key: value` as text. go/format
// pads a key to the longest one beside it, so `Format:    _strPtr("date")`
// next to MinLength was not found, the field was not declared, and under
// --format-assertion this schema generated a package that did not compile
// ("unknown field Format in struct literal of type _schemaNode"). The bounds
// and multipleOf beside each other are the same shape for the number arms.
func TestEvaluatorNodeFieldsAreReadUnderAlignment(t *testing.T) {
	t.Parallel()
	cfg := numberPositionConfig()
	cfg.FormatAssertion = true
	runNumberPositionCases(t, "testdata/schemas/regression/evaluator_node_aligned_fields.json", cfg,
		[]string{`["2020-01-01"]`, `[5]`, `[99]`, `[500]`, `[true]`},
		[]string{`["x"]`, `["2020-13-45"]`, `["2020-01-01",1]`})
}
