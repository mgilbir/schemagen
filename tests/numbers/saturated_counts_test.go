package numbers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
	"github.com/mgilbir/schemagen/tests/internal/testsupport"
)

// A count keyword whose bound is past int64 is held saturated at MaxInt (see
// schema.FlexInt): {"maxLength":2^63} used to wrap to MinInt64 and refuse "",
// and {"minLength":1e19} to accept "x". The tests here hold the three things
// the saturated bound must do, over every count keyword in every position the
// generator emits a count check at, under every validation mode:
//
//   - keep the verdict of the number the schema wrote -- a maximum that large
//     admits everything, a minimum that large nothing;
//   - state that number, not MaxInt, in every message that states a bound: a
//     message saying "minimum 9223372036854775807" names a bound nobody wrote;
//   - compile on a 32-bit target, where MaxInt64 overflows int.
//
// testdata/schemas/regression/saturated_counts.json gives each count keyword
// and position its own distinct literal (1e19, 1e20, ... 1e48), so a message
// quoting a literal quotes the one that bound it and not a neighbour's.

type saturatedCase struct {
	name  string
	doc   string
	valid bool
	// says is the literal an error message must state, for a case whose
	// refusal names a bound. Empty where the refusal names none (a contains
	// or not/oneOf branch, which reports the branch, not the bound).
	says string
}

var saturatedCases = []saturatedCase{
	{"a maximum past int64, empty", `{"sMax":""}`, true, ""},
	{"a maximum past int64 admits", `{"sMax":"abcdefghij"}`, true, ""},
	{"a minimum past int64", `{"sMin":"x"}`, false, "1e19"},
	{"MaxInt64 exactly admits", `{"sExact":"abc"}`, true, ""},
	{"past int32 admits", `{"sWide":"abc"}`, true, ""},
	{"a required minimum", `{"req":{"rMin":"x"}}`, false, "1e20"},
	{"maxItems admits", `{"aMax":[1,2,3]}`, true, ""},
	{"minItems past uint64", `{"aMin":[1]}`, false, "18446744073709551616"},
	{"a required minItems", `{"reqArr":{"raMin":[1]}}`, false, "1e21"},
	{"maxProperties 1e400 admits", `{"oMax":{"a":1,"b":2}}`, true, ""},
	{"minProperties", `{"oMin":{"a":1}}`, false, "1e22"},
	{"maxContains admits", `{"cMax":[1,2,3]}`, true, ""},
	{"minContains", `{"cMin":[1,2,3]}`, false, "1e24"},
	{"a contains sub-schema's minLength", `{"cStr":["abc"]}`, false, ""},
	{"propertyNames maxLength admits", `{"names":{"abc":1}}`, true, ""},
	{"propertyNames minLength", `{"namesMin":{"abc":1}}`, false, "1e27"},
	{"propertyNames minLength, no names", `{"namesMin":{}}`, true, ""},
	{"an element's minLength", `{"elems":["abc"]}`, false, "1e28"},
	{"an element's minItems", `{"elemArrs":[[1]]}`, false, "1e29"},
	{"a pattern property's maxItems admits", `{"pattern":{"p1":[1,2]}}`, true, ""},
	{"a pattern property's minLength", `{"patternMin":{"p1":"abc"}}`, false, "1e32"},
	{"a pattern property's minItems", `{"patternArrMin":{"p1":[1]}}`, false, "1e33"},
	{"a dependent schema's minProperties", `{"dep":{"a":1}}`, false, "1e34"},
	{"a dependent schema's maxProperties admits", `{"depMax":{"a":1,"b":2}}`, true, ""},
	{"not of an unsatisfiable minLength admits", `{"notMin":"x"}`, true, ""},
	{"not of an unbounded maxLength refuses", `{"notMax":"x"}`, false, ""},
	{"not of an unsatisfiable minItems admits", `{"notArr":[1]}`, true, ""},
	{"oneOf: only the unbounded branch matches", `{"oneStr":"x"}`, true, ""},
	{"oneOf over arrays: only the unbounded branch matches", `{"oneArr":[1]}`, true, ""},
	{"a string alias's minLength", `{"alias":"x"}`, false, "1e46"},
	{"an array alias's minItems", `{"aliasArr":[1]}`, false, "1e47"},
	{"an untyped alias's minLength", `{"inferred":"x"}`, false, "1e48"},
	{"unevaluatedItems' minLength", `{"ueItems":["a","b"]}`, false, "1e43"},
	{"unevaluatedItems, nothing unevaluated", `{"ueItems":["a"]}`, true, ""},
	{"unevaluatedProperties' minLength", `{"ueProps":{"s":"a","t":"b"}}`, false, "1e44"},
	{"merged with a tighter bound", `{"merged":"abcd"}`, false, ""},
	{"merged, within the tighter bound", `{"merged":"abc"}`, true, ""},
	{"a required maximum admits", `{"reqMax":{"rMax":"abc"}}`, true, ""},
	{"a nullable array's minItems", `{"nullArr":[1]}`, false, "1e51"},
	{"a nullable array's minItems, null", `{"nullArr":null}`, true, ""},
	{"a nullable array's maxItems admits", `{"nullArrMax":[1,2]}`, true, ""},
	{"an element's maxLength admits", `{"elemsMax":["abc"]}`, true, ""},
	{"an element's maxItems admits", `{"elemArrsMax":[[1,2]]}`, true, ""},
	{"unevaluatedProperties' maxLength admits", `{"uePropsMax":{"s":"a","t":"b"}}`, true, ""},
	{"unevaluatedItems' maxLength admits", `{"ueItemsMax":["a","b"]}`, true, ""},
	{"a contains sub-schema's maxLength admits", `{"cStrMax":["abc"]}`, true, ""},
	{"a pattern property's maxLength admits", `{"patternMax":{"p1":"abc"}}`, true, ""},
	{"a string alias's maxLength admits", `{"aliasMax":"abc"}`, true, ""},
	{"an array alias's maxItems admits", `{"aliasArrMax":[1]}`, true, ""},
	{"an alias's anyOf: the unbounded branch", `{"aliasAny":"x"}`, true, ""},
	{"an alias's oneOf: only the unbounded branch", `{"aliasOne":"x"}`, true, ""},
	{"an array alias's anyOf", `{"aliasArrAny":[1]}`, true, ""},
	{"an array alias's oneOf", `{"aliasArrOne":[1]}`, true, ""},
	{"an untyped alias's maxLength admits", `{"inferredMax":"abc"}`, true, ""},
	{"an untyped array alias's maxItems admits", `{"inferredArrMax":[1,2]}`, true, ""},
	{"an untyped array alias's minItems", `{"inferredArrMin":[1]}`, false, "1e91"},
	{"an untyped alias's anyOf", `{"inferredAny":"x"}`, true, ""},
	{"an untyped alias's oneOf", `{"inferredOne":"x"}`, true, ""},
	{"an untyped array alias's anyOf", `{"inferredArrAny":[1]}`, true, ""},
	{"an untyped array alias's oneOf", `{"inferredArrOne":[1]}`, true, ""},
	{"not of unsatisfiable minimums admits", `{"notBranch":"x"}`, true, ""},
	{"not of unsatisfiable minimums admits an array", `{"notBranch":[1]}`, true, ""},
	{"not of unbounded maximums refuses", `{"notBranchMax":"x"}`, false, ""},
	{"nothing present", `{}`, true, ""},
}

// saturatedMain decodes and validates every case against Root and prints one
// JSON line per case: whether it was accepted, and the error if it was not.
func saturatedMain(cases []saturatedCase) string {
	var b strings.Builder
	b.WriteString("package main\n\nimport (\n\t\"encoding/json\"\n\t\"fmt\"\n)\n\nfunc main() {\n\tdocs := []string{\n")
	for _, c := range cases {
		fmt.Fprintf(&b, "\t\t%s,\n", goQuote(c.doc))
	}
	b.WriteString(`	}
	for _, d := range docs {
		var v Root
		err := json.Unmarshal([]byte(d), &v)
		if err == nil {
			if val, ok := any(&v).(interface{ Validate() error }); ok {
				err = val.Validate()
			}
		}
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		line, _ := json.Marshal(map[string]any{"ok": err == nil, "err": msg})
		fmt.Println(string(line))
	}
}
`)
	return b.String()
}

// generateSaturatedPackage writes the fixture's generated package, as package
// main beside mainGo, into a fresh directory with its go.mod, and returns it.
func generateSaturatedPackage(t *testing.T, fixture string, cfg generator.Config, mainGo string) string {
	t.Helper()
	cfg.PackageName, cfg.OmitEmpty, cfg.RootTypeName = "testpkg", true, "Root"
	s, err := schema.LoadFromFile(testsupport.RepoPath("testdata", "schemas", "regression", fixture))
	if err != nil {
		t.Fatal(err)
	}
	s.NormalizeForDraft(cfg.Draft)
	ir, err := generator.New(cfg).Generate(s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}
	src, err := em.Emit(ir)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir := t.TempDir()
	code := strings.Replace(string(src), "package testpkg", "package main", 1)
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
	return dir
}

// saturatedStandIn matches the saturated values themselves: a message quoting
// either states a bound the schema did not write.
var saturatedStandIn = regexp.MustCompile(`-?922337203685477580[78]`)

// saturatedFixtures pairs each count-heavy fixture with its cases. Two
// positions need a root of their own: a root that constrains through
// applicators and declares no type is a dynamic schema with its own evaluator,
// and a root with properties and no type accepts a non-object and checks it
// against the root's own string and array keywords.
var saturatedFixtures = []struct {
	fixture string
	cases   []saturatedCase
}{
	{"saturated_counts.json", saturatedCases},
	{"saturated_counts_dynamic.json", []saturatedCase{
		{"matches the unbounded branch", `"yes"`, true, ""},
		{"matches neither", `"no"`, false, ""},
	}},
	{"saturated_counts_nonobject.json", []saturatedCase{
		{"a string below the minimum", `"x"`, false, "1e19"},
		{"an array below the minimum", `[1]`, false, "1e21"},
		{"an object", `{"a":1}`, true, ""},
	}},
}

func TestSaturatedCountsKeepTheirVerdictAndTheirLiteral(t *testing.T) {
	modes := []generator.ValidationMode{generator.ValidationModeStatic, generator.ValidationModeHybrid, generator.ValidationModeRuntime}
	for _, fx := range saturatedFixtures {
		for _, mode := range modes {
			t.Run(fx.fixture+"/"+string(mode), func(t *testing.T) {
				runSaturatedFixture(t, fx.fixture, mode, fx.cases)
			})
		}
	}
}

func runSaturatedFixture(t *testing.T, fixture string, mode generator.ValidationMode, saturatedCases []saturatedCase) {
	t.Helper()
	dir := generateSaturatedPackage(t, fixture, generator.Config{Validation: mode}, saturatedMain(saturatedCases))
	out, err := goCmd(t, dir, nil, "run", ".")
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(programOutput(out)), "\n")
	if len(lines) != len(saturatedCases) {
		t.Fatalf("got %d results for %d cases:\n%s", len(lines), len(saturatedCases), out)
	}
	for i, c := range saturatedCases {
		var r struct {
			OK  bool   `json:"ok"`
			Err string `json:"err"`
		}
		if err := json.Unmarshal([]byte(lines[i]), &r); err != nil {
			t.Fatalf("%s: %q: %v", c.name, lines[i], err)
		}
		if r.OK != c.valid {
			t.Errorf("%s: %s accepted=%v, want %v (err: %s)", c.name, c.doc, r.OK, c.valid, r.Err)
			continue
		}
		if m := saturatedStandIn.FindString(r.Err); m != "" {
			t.Errorf("%s: the message states %s, a bound the schema never wrote: %s", c.name, m, r.Err)
		}
		if c.says != "" && !strings.Contains(r.Err, c.says) {
			t.Errorf("%s: the message does not state the schema's bound %s: %s", c.name, c.says, r.Err)
		}
	}
}

// TestCountBoundsCompileOnA32BitTarget: the generator's int is 64 bits and a
// target's may be 32, so a bound written as a literal that is an int here --
// MaxInt64 from saturation, or 3000000000 -- is a constant the 32-bit compiler
// refuses. Every count-heavy fixture, in every mode, must type-check and build
// under GOARCH=386 as well as natively.
func TestCountBoundsCompileOnA32BitTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-compiles generated packages")
	}
	const noMain = "package main\n\nfunc main() {}\n"
	for _, fx := range saturatedFixtures {
		fixture := fx.fixture
		for _, mode := range []generator.ValidationMode{generator.ValidationModeStatic, generator.ValidationModeHybrid, generator.ValidationModeRuntime} {
			t.Run(fixture+"/"+string(mode), func(t *testing.T) {
				dir := generateSaturatedPackage(t, fixture, generator.Config{Validation: mode}, noMain)
				for _, env := range [][]string{nil, {"GOARCH=386"}} {
					if out, err := goCmd(t, dir, env, "vet", "."); err != nil {
						t.Fatalf("go vet %v: %v\n%s", env, err, out)
					}
					if out, err := goCmd(t, dir, env, "build", "-o", os.DevNull, "."); err != nil {
						t.Fatalf("go build %v: %v\n%s", env, err, out)
					}
				}
			})
		}
	}
}
