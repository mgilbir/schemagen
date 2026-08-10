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
	Cases  []numCase
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
	return append(fx, bigFx...)
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
		OmitEmpty:     true,
		RootTypeName:  "Root",
		BigIntSupport: fx.BigInt,
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
		t.Fatalf("%s (bigInt=%v):\n%s", fx.Name, fx.BigInt, text)
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
`, rootType)
	return b.String(), nil
}
