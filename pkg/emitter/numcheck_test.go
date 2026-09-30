package emitter

import (
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// TestNumViolatedWritesTheFastFormWhereItIsExact pins which form each numeric
// check is written in.
//
// Every form is exact -- the number is always judged as a mathematical value --
// so a check sent down the slow form is not a wrong answer, and nothing that
// compares verdicts can see it. It is a regression all the same: the forms
// that compare in int64 or float64 are what keep the common check as cheap as
// it was before the numbers were made exact, and the helper each falls back
// to formats and parses on every call. The literal was once handed to the
// generator as a plain string, which it does not read as a number, and every
// check in the corpus took the slow form without a single verdict moving.
func TestNumViolatedWritesTheFastFormWhereItIsExact(t *testing.T) {
	rule := func(kind string, value string, operand generator.NumOperandKind) generator.ValidationRule {
		r := generator.ValidationRule{RuleType: kind, Value: schema.Number(value), NumOperand: operand}
		if kind == "const" {
			r.Value, r.ExactValue = value, value
		}
		return r
	}
	for _, tc := range []struct {
		name string
		rule generator.ValidationRule
		want string
	}{
		{"float64 against a bound float64 spells", rule("minimum", "0", generator.NumOperandFloat64), "float64(x) < 0"},
		{"float64 against a decimal float64 spells", rule("maximum", "0.1", generator.NumOperandFloat64), "float64(x) > 0.1"},
		{"float64 against a bound it cannot spell", rule("maximum", "9007199254740993", generator.NumOperandFloat64), `rt.NumberAbove(float64(x), "9007199254740993")`},
		{"float64 exclusive bound", rule("exclusiveMinimum", "1e2", generator.NumOperandFloat64), "float64(x) <= 100"},
		{"float64 multipleOf with digits and places", rule("multipleOf", "0.05", generator.NumOperandFloat64), `!rt.FloatIsMultipleOf(float64(x), "0.05", 5, 2)`},
		{"float64 multipleOf past the fast path", rule("multipleOf", "1e-20", generator.NumOperandFloat64), `!rt.FloatIsMultipleOf(float64(x), "1e-20", 0, 0)`},
		{"float64 const it spells", rule("const", "2.5", generator.NumOperandFloat64), "float64(x) != 2.5"},
		{"float64 const it cannot", rule("const", "0.1000000000000000000001", generator.NumOperandFloat64), `!rt.NumberEqual(float64(x), "0.1000000000000000000001")`},
		{"int64 against an int64 bound", rule("minimum", "1", generator.NumOperandInt64), "x < 1"},
		{"int64 against the largest int64", rule("maximum", "9223372036854775807", generator.NumOperandInt64), "x > 9223372036854775807"},
		{"int64 against a fractional bound", rule("minimum", "1.5", generator.NumOperandInt64), `rt.NumberBelow(int64(x), "1.5")`},
		{"int64 against a bound past int64", rule("maximum", "1e19", generator.NumOperandInt64), `rt.NumberAbove(int64(x), "1e19")`},
		{"int64 multipleOf an integer", rule("multipleOf", "7", generator.NumOperandInt64), "x%7 != 0"},
		{"int64 multipleOf a fraction", rule("multipleOf", "2.5", generator.NumOperandInt64), `rt.NumberNotMultipleOf(int64(x), "2.5")`},
		{"int64 const", rule("const", "9007199254740993", generator.NumOperandInt64), "x != 9007199254740993"},
		{"json.Number", rule("minimum", "0.1", generator.NumOperandJSONNumber), `rt.NumberCmp(json.Number(x), "0.1") < 0`},
		{"json.Number multipleOf", rule("multipleOf", "0.1", generator.NumOperandJSONNumber), `!rt.NumberIsMultipleOf(json.Number(x), "0.1")`},
		{"any other type", rule("exclusiveMaximum", "3", generator.NumOperandAny), `rt.NumberAtLeast(x, "3")`},
		{"a patternProperties rule", rule("ppMultipleOf", "0.1", generator.NumOperandAny), `rt.NumberNotMultipleOf(x, "0.1")`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := numViolatedFunc(tc.rule, "x")
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}
