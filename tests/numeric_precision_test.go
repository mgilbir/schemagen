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

// numCase is one JSON document put to a generated type, and everything the type
// is supposed to answer about it: whether the decode holds the value at all,
// whether Validate accepts it, and what the value marshals back to.
//
// The third is the one a verdict test on its own misses. A decode that silently
// turns 9223372036854775808 into -9223372036854775808 accepts the document and
// validates clean; only re-encoding shows that the value in hand is not the
// value that arrived. That is issue #215, and it is why Want is a JSON document
// rather than a boolean.
type numCase struct {
	Name string
	Doc  string
	// Want is the JSON the value must marshal back to when the document is
	// accepted, "" when Validate must reject it, and decodeErr when the value is
	// outside what the position's Go type can hold.
	Want string
	Why  string
}

// decodeErr is the sentinel Want for a document no value of the type can hold.
// It is not a JSON document, so no case can name it by accident.
const decodeErr = "<decode error>"

// numFixture is one schema run against a matrix of documents, under one config.
type numFixture struct {
	Name   string
	Schema string // the schema document, inline: these differ by one number
	BigInt bool
	// Exact is Config.ExactNumbers: "number" held as the literal the document
	// wrote rather than as the float64 it rounds to.
	Exact bool
	// OmitEmptyFalse turns Config.OmitEmpty off, which is the other setting a
	// number field's tag is decided by and so the other one a round trip has to
	// hold under.
	OmitEmptyFalse bool
	Cases          []numCase
}

const numDialect = `"$schema":"https://json-schema.org/draft/2020-12/schema",`

// The boundaries every numeric path has to be right at. MaxInt64 and its
// neighbours are where int64 ends; 2^63 is one past it and is the float64 both
// of them round to; 2^53 is where consecutive integers stop having distinct
// float64s at all.
const (
	maxInt64      = "9223372036854775807"
	maxInt64Minus = "9223372036854775806"
	twoPow63      = "9223372036854775808"
	twoPow63Plus  = "9223372036854775809"
	minInt64      = "-9223372036854775808"
	twoPow53      = "9007199254740992"
	twoPow53Plus  = "9007199254740993"
	twoPow62      = "4611686018427387904"
	twoPow62Plus  = "4611686018427387905"
)

// numericPrecisionFixtures are issues #215, #216 and #220 and the boundaries
// around each, run compiled because none of the three shows in the IR.
//
// #215 emitted a decode that accepted a value it could not hold and returned it
// sign-flipped, with no error anywhere: a verdict test passes on it. #216
// emitted a float literal into an int64 constant declaration, which does not
// compile -- and just below the threshold where it fails to build it compiles
// and gives the wrong answer instead. #220 emitted a bound as its shortest
// decimal and had big.Float read that decimal literally, so a value equal to
// its own minimum was refused by 96.
func numericPrecisionFixtures() []numFixture {
	// intSchema is {"type":"integer"} with one keyword added.
	intSchema := func(extra string) string {
		if extra == "" {
			return "{" + numDialect + `"type":"integer"}`
		}
		return "{" + numDialect + `"type":"integer",` + extra + "}"
	}

	fx := []numFixture{
		{
			// Issue #215. Nothing here is about validation: the schema states no
			// keyword beyond the type, and every case is about whether the
			// decode kept the number.
			Name:   "plain_integer_decode",
			Schema: intSchema(""),
			Cases: []numCase{
				{"MaxInt64", maxInt64, maxInt64, "the largest int64; it must survive unchanged"},
				{"MaxInt64-1", maxInt64Minus, maxInt64Minus, "control for the above"},
				{"2^63", twoPow63, decodeErr, "one past int64. Issue #215: this was accepted and returned as -2^63"},
				{"2^63+1", twoPow63Plus, decodeErr, "also past int64, and the same float64 as 2^63"},
				{"MinInt64", minInt64, minInt64, "the smallest int64; the lower guard must not refuse it"},
				{"-2^63-1", "-9223372036854775809", decodeErr, "one below int64"},
				{"2^53", twoPow53, twoPow53, "the last integer with a float64 of its own"},
				{"2^53+1", twoPow53Plus, twoPow53Plus, "the first that has not; through float64 it comes back as 2^53"},
				{"-2^53-1", "-9007199254740993", "-9007199254740993", "the same one below zero"},
				{"MaxInt64 in float notation", maxInt64 + ".0", maxInt64,
					"an integer however it is written, from draft 6 on; the float64 it parses to is 2^63, which is not this number"},
				{"2^53+1 in float notation", twoPow53Plus + ".0", twoPow53Plus,
					"float notation must not round: this rounds to 2^53 and the round trip then hides it"},
				{"2^63 in float notation", twoPow63 + ".0", decodeErr, "no int64 holds it, whatever notation it arrives in"},
				{"one written 1.0", "1.0", "1", "control: zero fractional part is still an integer"},
				{"one written 1e2", "1e2", "100", "control: an exponent is still an integer"},
				{"1.5", "1.5", decodeErr, "control: a fraction is not an integer"},
			},
		},
		{
			// Issue #216. The const fits int64 exactly and does not fit float64
			// at all; the emitted constant declaration is where that showed.
			Name:   "const_maxint64",
			Schema: intSchema(`"const":` + maxInt64),
			Cases: []numCase{
				{"the constant itself", maxInt64, maxInt64, "issue #216: this declaration did not compile, and below the threshold it rejected its own value"},
				{"one below", maxInt64Minus, "", "the only permitted value is the constant"},
			},
		},
		{
			// The wrong-verdict half of #216, below the build-failure threshold.
			Name:   "const_2pow53_plus_1",
			Schema: intSchema(`"const":` + twoPow53Plus),
			Cases: []numCase{
				{"the constant itself", twoPow53Plus, twoPow53Plus, "issue #216: rounded to 2^53, this was rejected"},
				{"the value it rounded to", twoPow53, "", "issue #216: 2^53 was accepted in its place"},
			},
		},
		{
			Name:   "enum_maxint64",
			Schema: intSchema(`"enum":[` + maxInt64 + `,1]`),
			Cases: []numCase{
				{"the large member", maxInt64, maxInt64, "issue #216: `enum` failed to compile at eleven positions for this value"},
				{"the small member", "1", "1", "control: the other member still works"},
				{"one below the large member", maxInt64Minus, "", "not a member"},
			},
		},
		{
			Name:   "minimum_maxint64",
			Schema: intSchema(`"minimum":` + maxInt64),
			Cases: []numCase{
				{"the bound itself", maxInt64, maxInt64, "a value equal to its minimum satisfies it"},
				{"one below", maxInt64Minus, "", "compared as float64 both operands are 2^63 and this was accepted"},
			},
		},
		{
			Name:   "exclusive_maximum_2pow62",
			Schema: intSchema(`"exclusiveMaximum":` + twoPow62),
			Cases: []numCase{
				{"one below the bound", "4611686018427387903", "4611686018427387903", "strictly less, so it is admitted"},
				{"the bound itself", twoPow62, "", "exclusive: the bound is not admitted"},
			},
		},
		{
			Name:   "multiple_of_2pow62",
			Schema: intSchema(`"multipleOf":` + twoPow62),
			Cases: []numCase{
				{"the divisor", twoPow62, twoPow62, "every number divides itself"},
				{"one past it", twoPow62Plus, "", "a float64 quotient compared against a tolerance called this a multiple"},
				{"zero", "0", "0", "control: zero is a multiple of everything"},
			},
		},
		{
			// Genuine floating point. Every case here has to answer exactly as
			// it did before any of this: a fix that turns numbers into integers
			// is not a fix.
			Name:   "number_positions_unchanged",
			Schema: "{" + numDialect + `"type":"number","minimum":1.5,"multipleOf":0.5}`,
			Cases: []numCase{
				{"the bound itself", "1.5", "1.5", "a fractional minimum still admits its own value"},
				{"below the bound", "1.4", "", "1.4 < 1.5"},
				{"a multiple", "2.5", "2.5", "2.5 is 5 halves"},
				{"not a multiple", "1.6", "", "1.6 is not a whole number of halves"},
			},
		},
		{
			Name:   "negative_zero_and_fractions",
			Schema: "{" + numDialect + `"type":"number"}`,
			Cases: []numCase{
				{"negative zero", "-0.0", "-0", "a float64 keeps the sign of zero, and the schema says nothing against it"},
				{"a fraction", "1.5", "1.5", "control"},
				{"out of float64 range", "1e400", decodeErr, "no float64 holds it"},
			},
		},
	}

	// Issue #252: "number" round-tripped through float64 and lost precision, so
	// a read-modify-write rewrote a field the caller never touched. Run under
	// --exact-numbers, which is the flag that exists for exactly this, with the
	// default-configuration control beside each so that what the flag changes is
	// visible rather than asserted.
	//
	// The bounds are the point of the keyword fixtures below. Each is a number
	// float64 cannot tell from its neighbour -- 0.1 and the decimal float64
	// actually holds for it -- so a check made through float64 has both operands
	// as one value and nothing left to compare. Only an exact comparison can
	// answer them, which is what makes them worth writing.
	const (
		// The exact value of the float64 nearest 0.1, truncated. It is a larger
		// number than 0.1 and the same float64 as 0.1.
		float64Of01 = "0.1000000000000000055511151231257827"
		// The issue's own two documents.
		longDecimal = "1.2345678901234567890"
		hugeNumber  = "123456789012345678901234567890"
		// Forty digits, which is past every binary format in the standard
		// library rather than only past float64.
		fortyDigits = "1.234567890123456789012345678901234567890"
	)
	numSchema := func(extra string) string {
		if extra == "" {
			return "{" + numDialect + `"type":"object","properties":{"n":{"type":"number"}}}`
		}
		return "{" + numDialect + `"type":"object","properties":{"n":{"type":"number",` + extra + "}}}"
	}

	exactFx := []numFixture{
		{
			// The issue, verbatim, in the position it was reported in.
			Name:   "exact_issue_252_scalars",
			Exact:  true,
			Schema: "{" + numDialect + `"type":"object","properties":{"f":{"type":"number"},"g":{"type":"number"}}}`,
			Cases: []numCase{
				{"the reported document", `{"f":` + longDecimal + `,"g":` + hugeNumber + `}`,
					`{"f":` + longDecimal + `,"g":` + hugeNumber + `}`,
					"issue #252: came back as {\"f\":1.2345678901234567,\"g\":1.2345678901234568e+29}"},
				{"a string where a number belongs", `{"f":"1.5"}`, decodeErr,
					"json.Number is a string underneath and would take this; the float64 it replaces refused it, and so must this"},
				{"an ordinary number", `{"f":1.5}`, `{"f":1.5}`, "control: nothing exotic changes"},
			},
		},
		{
			// The same document under the default configuration. If this ever
			// passes with the exact answers, the flag has stopped being a flag.
			Name:   "default_issue_252_scalars",
			Schema: "{" + numDialect + `"type":"object","properties":{"f":{"type":"number"},"g":{"type":"number"}}}`,
			Cases: []numCase{
				{"the reported document", `{"f":` + longDecimal + `,"g":` + hugeNumber + `}`,
					`{"f":1.2345678901234567,"g":1.2345678901234568e+29}`,
					"the default is float64 and this is what float64 makes of it; issue #252 is that it is silent"},
				{"a string where a number belongs", `{"f":"1.5"}`, decodeErr, "float64 refuses it"},
			},
		},
		{
			Name:  "exact_positions",
			Exact: true,
			Schema: "{" + numDialect + `"type":"object","properties":{` +
				`"scalar":{"type":"number"},` +
				`"arr":{"type":"array","items":{"type":"number"}},` +
				`"m":{"type":"object","additionalProperties":{"type":"number"}},` +
				`"aliased":{"$ref":"#/$defs/Temp"},` +
				`"nullable":{"type":["number","null"]}},` +
				`"$defs":{"Temp":{"type":"number"}}}`,
			Cases: []numCase{
				{"every position at once",
					`{"aliased":` + longDecimal + `,"arr":[` + longDecimal + `,` + hugeNumber + `],"m":{"a":` + longDecimal + `},"nullable":` + hugeNumber + `,"scalar":` + longDecimal + `}`,
					`{"aliased":` + longDecimal + `,"arr":[` + longDecimal + `,` + hugeNumber + `],"m":{"a":` + longDecimal + `},"nullable":` + hugeNumber + `,"scalar":` + longDecimal + `}`,
					"a scalar, an array element, a map value, a $defs alias and a nullable: the flag reaches every position the schema types"},
				{"a null where the schema permits one", `{"nullable":null}`, `{"nullable":null}`,
					"control: the null bookkeeping is unaffected by how the number is held"},
				{"a string inside an array", `{"arr":["1.5"]}`, decodeErr, "the element is a number position too"},
				{"a string inside a map", `{"m":{"a":"1.5"}}`, decodeErr, "and so is a map value"},
				{"a string at the alias", `{"aliased":"1.5"}`, decodeErr, "and so is the alias"},
			},
		},
		{
			// The precision boundaries, each decoded, validated, marshalled and
			// read back by the harness.
			Name:   "exact_boundaries",
			Exact:  true,
			Schema: numSchema(""),
			Cases: []numCase{
				{"one tenth", `{"n":0.1}`, `{"n":0.1}`, "the number no binary float holds; here it is held as itself"},
				{"subnormal", `{"n":1e-320}`, `{"n":1e-320}`, "below float64's normal range, where its precision collapses"},
				{"past float64 entirely", `{"n":1e400}`, `{"n":1e400}`,
					"no float64 holds it, so the default cannot even decode it; the literal has no such limit"},
				{"forty digits", `{"n":` + fortyDigits + `}`, `{"n":` + fortyDigits + `}`, "every digit kept"},
				{"negative zero", `{"n":-0.0}`, `{"n":-0.0}`,
					"the literal, sign and trailing zero and all; float64 writes it back as -0"},
				{"exactly representable", `{"n":0.5}`, `{"n":0.5}`, "a value float64 does hold exactly still comes back as written"},
				{"an integer written as one", `{"n":1}`, `{"n":1}`, "control: a whole number does not grow a point"},
			},
		},
		{
			Name:   "default_boundaries",
			Schema: numSchema(""),
			Cases: []numCase{
				{"one tenth", `{"n":0.1}`, `{"n":0.1}`, "float64's shortest round-tripping decimal is the same text here"},
				{"past float64 entirely", `{"n":1e400}`, decodeErr, "no float64 holds it: the default refuses the document outright"},
				{"forty digits", `{"n":` + fortyDigits + `}`, `{"n":1.2345678901234567}`, "what the default does with them, and why #252 was filed"},
				{"negative zero", `{"n":-0.0}`, `{"n":-0}`, "float64 keeps the sign and loses the spelling"},
			},
		},
		{
			Name:   "exact_minimum",
			Exact:  true,
			Schema: numSchema(`"minimum":` + float64Of01),
			Cases: []numCase{
				{"the bound itself", `{"n":` + float64Of01 + `}`, `{"n":` + float64Of01 + `}`, "a value equal to its minimum satisfies it"},
				{"one tenth", `{"n":0.1}`, "", "0.1 is below this bound and is the same float64 as it: only an exact comparison can refuse it"},
				{"clearly above", `{"n":0.2}`, `{"n":0.2}`, "control"},
			},
		},
		{
			Name:  "exact_maximum",
			Exact: true,
			// The bound is written with an exponent and the values without one,
			// so the two are the same numbers spelled with different digit runs.
			Schema: numSchema(`"maximum":1e-1`),
			Cases: []numCase{
				{"one tenth", `{"n":0.1}`, `{"n":0.1}`, "a value equal to its maximum satisfies it"},
				{"the float64 of one tenth", `{"n":` + float64Of01 + `}`, "", "larger than 0.1, and indistinguishable from it in float64"},
				{"a different spelling of the bound", `{"n":1e-1}`, `{"n":1e-1}`, "1e-1 is 0.1; the comparison is on the number, not the text"},
				{"the leading zero of the value", `{"n":0.1}`, `{"n":0.1}`,
					"0.1 and 1e-1 are one number written with a different number of digits, and lining the two up is what the leading-zero rule in jsonNumberParts is for"},
			},
		},
		{
			Name:   "exact_exclusive_minimum",
			Exact:  true,
			Schema: numSchema(`"exclusiveMinimum":0.1`),
			Cases: []numCase{
				{"the bound itself", `{"n":0.1}`, "", "exclusive: the bound is not admitted"},
				{"the float64 of one tenth", `{"n":` + float64Of01 + `}`, `{"n":` + float64Of01 + `}`,
					"strictly greater than 0.1, and float64 says equal -- so through float64 this is refused, and the schema admits it"},
				{"a trailing zero on the bound", `{"n":0.10}`, "", "0.10 is 0.1 however it is spelled"},
			},
		},
		{
			Name:   "exact_exclusive_maximum",
			Exact:  true,
			Schema: numSchema(`"exclusiveMaximum":` + float64Of01),
			Cases: []numCase{
				{"one tenth", `{"n":0.1}`, `{"n":0.1}`, "strictly below the bound; float64 calls the two equal and refuses it"},
				{"the bound itself", `{"n":` + float64Of01 + `}`, "", "exclusive"},
			},
		},
		{
			Name:   "exact_multiple_of",
			Exact:  true,
			Schema: numSchema(`"multipleOf":1`),
			Cases: []numCase{
				{"a whole number", `{"n":4}`, `{"n":4}`, "control"},
				{"a whole number with a tenth of a nanosecond on it", `{"n":1000000000.0000000001}`, "",
					"float64 cannot hold the last digit at this magnitude, so the quotient it computes is exactly 1e9 and the tolerance test passes"},
				{"a fraction", `{"n":0.5}`, "", "control"},
			},
		},
		{
			Name:   "exact_multiple_of_decimal",
			Exact:  true,
			Schema: numSchema(`"multipleOf":0.1`),
			Cases: []numCase{
				{"three tenths", `{"n":0.3}`, `{"n":0.3}`, "three of them exactly, which in float64 is 2.9999999999999996 of them"},
				{"one and a half tenths", `{"n":0.15}`, "", "not a whole number of tenths"},
				{"zero", `{"n":0}`, `{"n":0}`, "zero is a multiple of everything"},
			},
		},
		{
			// The scales the divisibility test folds rather than expands.
			Name:   "exact_multiple_of_scales",
			Exact:  true,
			Schema: numSchema(`"multipleOf":3`),
			Cases: []numCase{
				{"a googol", `{"n":1e100}`, "",
					"10^100 is not divisible by 3, and the divisor is folded into the exponent rather than the exponent expanded"},
				{"three googol", `{"n":3e100}`, `{"n":3e100}`, "3 x 10^100 is three times 10^100"},
				{"a tenth", `{"n":0.1}`, "", "a divisor larger than the value divides nothing but zero"},
			},
		},
		{
			Name:   "exact_const",
			Exact:  true,
			Schema: numSchema(`"const":1.0000000000000000000000000000001`),
			Cases: []numCase{
				{"the constant itself", `{"n":1.0000000000000000000000000000001}`, `{"n":1.0000000000000000000000000000001}`, "its own value satisfies it"},
				{"one", `{"n":1}`, "", "1 is the float64 this constant rounds to, and is not this constant"},
				{"the constant with a trailing zero", `{"n":1.00000000000000000000000000000010}`, `{"n":1.00000000000000000000000000000010}`,
					"the same number written differently: a comparison of the JSON text would refuse it"},
			},
		},
		{
			Name:   "exact_enum",
			Exact:  true,
			Schema: "{" + numDialect + `"type":"number","enum":[1.5,2.5000000000000000001]}`,
			Cases: []numCase{
				{"a member", "1.5", "1.5", "control"},
				{"a member with a trailing zero", "1.50", "1.50",
					"the same member written differently -- a switch on the constant would refuse it -- and the literal is what comes back"},
				{"the float64 of the other member", "2.5", "", "2.5 is what the second member rounds to and is not that member"},
				{"the other member", "2.5000000000000000001", "2.5000000000000000001", "exactly, or not at all"},
				{"a non-member", "3", "", "control"},
			},
		},
		{
			// Both numeric types held exactly at once. The integer half is #230's
			// and must be untouched by any of this.
			Name:   "exact_with_big_int",
			Exact:  true,
			BigInt: true,
			Schema: "{" + numDialect + `"type":"object","properties":{"i":{"type":"integer"},"n":{"type":"number"}}}`,
			Cases: []numCase{
				{"both past what their default holds", `{"i":` + twoPow63 + `,"n":` + hugeNumber + `}`,
					`{"i":` + twoPow63 + `,"n":` + hugeNumber + `}`,
					"--big-int carries the integer past int64 and --exact-numbers the number past float64; neither disturbs the other"},
				{"MaxInt64 and one tenth", `{"i":` + maxInt64 + `,"n":0.1}`, `{"i":` + maxInt64 + `,"n":0.1}`, "control"},
				{"an integer written in float notation", `{"i":1.0,"n":1.0}`, `{"i":1,"n":1.0}`,
					"the integer is 1 from draft 6 on and is written back as one; the number is the literal it arrived as"},
			},
		},
		{
			// contains counts the elements a sub-schema matches, and it counts
			// them by re-marshalling each element and reading it back -- which
			// is a second place the reading has to be exact.
			Name:   "exact_contains",
			Exact:  true,
			Schema: "{" + numDialect + `"type":"object","properties":{"xs":{"type":"array","items":{"type":"number"},"contains":{"exclusiveMinimum":0.1}}}}`,
			Cases: []numCase{
				{"an element just past the bound", `{"xs":[` + float64Of01 + `]}`, `{"xs":[` + float64Of01 + `]}`,
					"strictly greater than 0.1 and the same float64 as it: read back through float64 this element matches nothing and the array is rejected"},
				{"an element at the bound", `{"xs":[0.1]}`, "", "not strictly greater, so nothing matches"},
				{"an element past float64 entirely", `{"xs":[1e400]}`, `{"xs":[1e400]}`,
					"read back through float64 this is not a number at all, and the element that satisfies the schema would not be counted"},
			},
		},
		{
			// The exponents no int64 holds. A literal may carry any number of
			// exponent digits the JSON grammar allows, and reading one into an
			// integer that wraps answers the opposite of the truth.
			Name:   "exact_extreme_exponents",
			Exact:  true,
			Schema: numSchema(`"maximum":5`),
			Cases: []numCase{
				{"an exponent past int64", `{"n":1e99999999999999999999}`, "",
					"a number this large exceeds every bound; read as anything smaller it would be accepted"},
				{"the same exponent downwards", `{"n":1e-99999999999999999999}`, `{"n":1e-99999999999999999999}`,
					"and this one is near zero, which the bound admits"},
				{"a zero mantissa at a vast exponent", `{"n":0e99999999999999999999}`, `{"n":0e99999999999999999999}`,
					"zero however it is spelled, and zero is under the bound"},
			},
		},
		{
			Name:           "exact_omit_empty_false",
			Exact:          true,
			OmitEmptyFalse: true,
			Schema:         numSchema(""),
			Cases: []numCase{
				{"a value", `{"n":` + longDecimal + `}`, `{"n":` + longDecimal + `}`, "the tag setting does not touch what the number is held as"},
				{"nothing", `{}`, `{"n":0}`,
					"without omitempty an optional scalar is not pointer-wrapped, so an absent property is the zero json.Number -- which encoding/json writes as 0, exactly as the float64 zero it replaces was written"},
			},
		},
	}
	// Issue #220: the bound is kept exactly and the value is a big.Int, so both
	// sides of the comparison are arbitrary-precision. Run under --big-int only,
	// which is the flag that exists for exactly this.
	bigFx := []numFixture{
		{
			Name:   "bigint_minimum_2pow62",
			Schema: intSchema(`"minimum":` + twoPow62),
			BigInt: true,
			Cases: []numCase{
				{"the bound itself", twoPow62, twoPow62,
					"issue #220: the bound was re-emitted as 4.611686018427388e+18 and big.Float read that decimal literally, 96 above the bound the schema stated"},
				{"one below", "4611686018427387903", "", "control: the bound still refuses what is under it"},
				{"far above", twoPow63, twoPow63, "a value past int64 is what the flag is for; it must round-trip"},
			},
		},
		{
			Name:   "bigint_exclusive_maximum_maxint64",
			Schema: intSchema(`"exclusiveMaximum":` + maxInt64),
			BigInt: true,
			Cases: []numCase{
				{"the bound itself", maxInt64, "", "issue #220 in the other direction: this was accepted"},
				{"one below", maxInt64Minus, maxInt64Minus, "control"},
			},
		},
		{
			Name:   "bigint_multiple_of_2pow62",
			Schema: intSchema(`"multipleOf":` + twoPow62),
			BigInt: true,
			Cases: []numCase{
				{"twice the divisor", twoPow63, twoPow63, "2^63 is two times 2^62; the rounded divisor said otherwise"},
				{"one past the divisor", twoPow62Plus, "", "control"},
			},
		},
		{
			Name:   "bigint_plain_decode",
			Schema: intSchema(""),
			BigInt: true,
			Cases: []numCase{
				{"past int64", twoPow63, twoPow63, "held as a big.Int and round-tripped exactly"},
				{"2^53+1 in float notation", twoPow53Plus + ".0", twoPow53Plus,
					"the int64 fast path read this through float64 and the round-trip check it made could not see the loss"},
				{"inside int64", maxInt64, maxInt64, "control: the int64 arm is still exact"},
			},
		},
	}
	return append(append(fx, exactFx...), bigFx...)
}

func TestNumericPrecision(t *testing.T) {
	for _, fx := range numericPrecisionFixtures() {
		t.Run(fx.Name, func(t *testing.T) {
			t.Parallel()
			runNumFixture(t, fx)
		})
	}
}

func runNumFixture(t *testing.T, fx numFixture) {
	t.Helper()

	tmpDir := t.TempDir()
	schemaPath := filepath.Join(tmpDir, "schema.json")
	if !json.Valid([]byte(fx.Schema)) {
		t.Fatalf("fixture %s: schema is not valid JSON: %s", fx.Name, fx.Schema)
	}
	if err := os.WriteFile(schemaPath, []byte(fx.Schema), 0o644); err != nil {
		t.Fatalf("writing schema: %v", err)
	}

	// The pipeline is run here rather than through generateFromSchemaWithConfig,
	// which resolves its argument against the repository root: these schemas are
	// written out per-case -- they differ by one number and there are thirty of
	// them -- so there is no fixture path to resolve.
	s, err := schema.LoadFromFile(schemaPath)
	if err != nil {
		t.Fatalf("loading schema: %v", err)
	}
	s.NormalizeForDraft(schema.DraftUnknown)
	gen := generator.New(generator.Config{
		PackageName:   "testpkg",
		OmitEmpty:     !fx.OmitEmptyFalse,
		RootTypeName:  "Root",
		BigIntSupport: fx.BigInt,
		ExactNumbers:  fx.Exact,
	})
	ir, err := gen.Generate(s)
	if err != nil {
		t.Fatalf("generating IR: %v", err)
	}
	em, err := emitter.New()
	if err != nil {
		t.Fatalf("creating emitter: %v", err)
	}
	generated, err := em.Emit(ir)
	if err != nil {
		t.Fatalf("emitting: %v", err)
	}

	genMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(genMain), 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}
	writeSharedHelpers(t, tmpDir, genMain)

	mainGo, err := numCaseMain("Root", fx.Cases)
	if err != nil {
		t.Fatalf("building main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}
	if err := writeTestGoMod(tmpDir, "numprecision"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	out, runErr := cmd.CombinedOutput()
	text := programOutput(out)
	if runErr != nil || text != "PASS" {
		t.Fatalf("%s (bigInt=%v exact=%v):\n%s", fx.Name, fx.BigInt, fx.Exact, text)
	}
}

// numCaseMain writes the program that puts each document to the type and reports
// all three answers: whether it decoded, whether Validate accepted it, and what
// it marshals back to.
func numCaseMain(rootType string, cases []numCase) (string, error) {
	var b strings.Builder
	b.WriteString(`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

const decodeErr = "<decode error>"

type numCase struct {
	name string
	doc  string
	want string
	why  string
}

func main() {
	cases := []numCase{
`)
	for _, c := range cases {
		if !json.Valid([]byte(c.Doc)) {
			return "", fmt.Errorf("case %q: %s is not valid JSON", c.Name, c.Doc)
		}
		fmt.Fprintf(&b, "\t\t{name: %s, doc: %s, want: %s, why: %s},\n",
			goQuote(c.Name), goQuote(c.Doc), goQuote(c.Want), goQuote(c.Why))
	}
	fmt.Fprintf(&b, `	}

	var errs []string
	for _, c := range cases {
		var v %s
		if err := json.Unmarshal([]byte(c.doc), &v); err != nil {
			if c.want != decodeErr {
				errs = append(errs, fmt.Sprintf("%%s: %%s did not decode: %%v (%%s)", c.name, c.doc, err, c.why))
			}
			continue
		}
		if c.want == decodeErr {
			out, _ := json.Marshal(v)
			errs = append(errs, fmt.Sprintf("%%s: %%s decoded as %%s, and no value of this type holds it (%%s)",
				c.name, c.doc, out, c.why))
			continue
		}
		var vErr error
		if val, ok := any(v).(interface{ Validate() error }); ok {
			vErr = val.Validate()
		}
		if c.want == "" {
			if vErr == nil {
				errs = append(errs, fmt.Sprintf("%%s: %%s was accepted, want rejected (%%s)", c.name, c.doc, c.why))
			}
			continue
		}
		if vErr != nil {
			errs = append(errs, fmt.Sprintf("%%s: %%s was rejected: %%v (%%s)", c.name, c.doc, vErr, c.why))
			continue
		}
		out, err := json.Marshal(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%%s: %%s did not re-encode: %%v (%%s)", c.name, c.doc, err, c.why))
			continue
		}
		if string(out) != c.want {
			errs = append(errs, fmt.Sprintf("%%s: %%s came back as %%s, want %%s (%%s)", c.name, c.doc, out, c.want, c.why))
			continue
		}
		// And once more from what it wrote. A value that encodes correctly and
		// then cannot be read back, or reads back as something else, has not
		// round-tripped: the second pass is where a representation that only
		// looks exact on the way out shows itself.
		var again %s
		if err := json.Unmarshal(out, &again); err != nil {
			errs = append(errs, fmt.Sprintf("%%s: %%s re-encoded as %%s, which does not decode: %%v (%%s)", c.name, c.doc, out, err, c.why))
			continue
		}
		if val, ok := any(again).(interface{ Validate() error }); ok {
			if err := val.Validate(); err != nil {
				errs = append(errs, fmt.Sprintf("%%s: %%s re-encoded as %%s, which its own Validate rejects: %%v (%%s)", c.name, c.doc, out, err, c.why))
				continue
			}
		}
		out2, err := json.Marshal(again)
		if err != nil || string(out2) != string(out) {
			errs = append(errs, fmt.Sprintf("%%s: %%s is not stable: %%s then %%s (%%s)", c.name, c.doc, out, out2, c.why))
		}
	}
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "FAIL: %%s\n", e)
		}
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`, rootType, rootType)
	return b.String(), nil
}
