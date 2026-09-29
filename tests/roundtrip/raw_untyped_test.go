package roundtrip

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/generator"
)

// untypedPositionsSchema is the position matrix TestGoldenRawUntyped pins as
// text. The tests here run the same document, because the golden can say what
// type each position has and cannot say what a value does on the way through
// it -- which is the whole question this flag exists to answer.
const untypedPositionsSchema = "testdata/schemas/regression/untyped_positions.json"

// rawUntypedConfig is the default configuration with Config.RawUntyped on.
func rawUntypedConfig() generator.Config {
	return generator.Config{PackageName: "testpkg", OmitEmpty: true, RawUntyped: true}
}

// untypedRoundTripMain is the program both round-trip tests below compile. It
// decodes one document into the generated root, marshals it back, and reports
// every untyped member whose bytes came back other than as given.
//
// It compares member by member rather than document to document because the
// generated MarshalJSON writes the *object's* members in its own order, and that
// order is not what is under test: JSON gives it no meaning and the struct's
// layout decides it. What is under test is what happens *inside* each untyped
// member, so each is compared against the compacted form of what was sent --
// compaction being the one thing encoding/json does to every RawMessage it
// writes, and the one thing JSON says carries no information.
//
// The three map-typed positions -- values, freeObject, nullableFreeObject --
// are sent with their members already in sorted order, because a Go map is what
// holds them and encoding/json writes a map's keys sorted whatever order they
// arrived in. Each *value* in them is a RawMessage and is compared as strictly
// as every other; it is the map's own member order that the flag does not and
// cannot promise, and the doc comment says so.
//
// It also decodes the same document a second time through a json.Decoder with
// UseNumber set, and checks that the two decodes marshal to the same bytes. That
// is the trap the flag's doc comment describes: the decoder setting a caller
// would reach for never reaches a field of a generated struct, because the
// struct's own UnmarshalJSON decodes its members through json.Unmarshal. The
// test holds that claim under both configurations -- under the default it is why
// UseNumber does not rescue the value, under the flag it is why nothing needs
// rescuing.
const untypedRoundTripMain = `package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	input := []byte(` + "`" + `{
		"required": {"z": 9007199254740993, "a": 1.10, "m": [1.0, 2.50], "big": 123456789012345678901234567890},
		"scalar": {"z": 9007199254740993, "a": 1.10, "m": [1.0, 2.50], "big": 123456789012345678901234567890},
		"empty": [1.0, 1e2, -0.0, "s", null, true],
		"anything": {"k": [1.0]},
		"elements": [1.0, 9007199254740993, {"b": 2, "a": 1}],
		"values": {"x": 1e2, "y": 1.10},
		"aliased": [1.0, {"b": 2, "a": 1}],
		"aliasedList": [1.0, 123456789012345678901234567890],
		"freeObject": {"a": 9007199254740993, "z": 1.0},
		"nullableFreeObject": {"a": 9007199254740993, "z": 1.0},
		"cycle": {"z": 1.0}
	}` + "`" + `)

	var members map[string]json.RawMessage
	if err := json.Unmarshal(input, &members); err != nil {
		fmt.Println("reading the input:", err)
		os.Exit(1)
	}

	var root UntypedPositions
	if err := json.Unmarshal(input, &root); err != nil {
		fmt.Println("decode:", err)
		os.Exit(1)
	}
	if err := root.Validate(); err != nil {
		fmt.Println("validate:", err)
		os.Exit(1)
	}
	out, err := json.Marshal(root)
	if err != nil {
		fmt.Println("encode:", err)
		os.Exit(1)
	}

	var viaUseNumber UntypedPositions
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.UseNumber()
	if err := dec.Decode(&viaUseNumber); err != nil {
		fmt.Println("decode through UseNumber:", err)
		os.Exit(1)
	}
	outViaUseNumber, err := json.Marshal(viaUseNumber)
	if err != nil {
		fmt.Println("encode after UseNumber:", err)
		os.Exit(1)
	}
	if !bytes.Equal(out, outViaUseNumber) {
		fmt.Printf("an outer decoder's UseNumber reached the generated type:\n  plain:     %s\n  UseNumber: %s\n", out, outViaUseNumber)
		os.Exit(1)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		fmt.Println("reading the output:", err)
		os.Exit(1)
	}
	var failures []string
	for name, sent := range members {
		var want bytes.Buffer
		if err := json.Compact(&want, sent); err != nil {
			fmt.Println("compacting", name, err)
			os.Exit(1)
		}
		if string(got[name]) != want.String() {
			failures = append(failures, fmt.Sprintf("  %s:\n    sent: %s\n    got:  %s", name, want.String(), got[name]))
		}
	}
	if len(failures) > 0 {
		fmt.Println("members that did not come back as sent:")
		for _, f := range failures {
			fmt.Println(f)
		}
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`

// TestRawUntypedRoundTripsUntypedPositionsVerbatim is the behavioural half of
// the golden pair: under Config.RawUntyped, a document put through the
// generated type comes back byte for byte in every position the schema left
// untyped.
//
// Each of the four ways the default loses the value is in the document, in
// every position the flag reaches. 9007199254740993 is one past 2^53 and has
// no float64 of its own; 1.10 and 2.50 carry a trailing zero a float64 cannot
// remember; 123456789012345678901234567890 comes back in exponent notation;
// and the members are written in an order a map[string]any forgets. -0.0, 1e2
// and a null are there for the spellings a decode-and-re-encode also rewrites.
func TestRawUntypedRoundTripsUntypedPositionsVerbatim(t *testing.T) {
	runGeneratedMainProgramWithConfig(t, untypedPositionsSchema, "rawuntyped_roundtrip", untypedRoundTripMain, rawUntypedConfig())
}

// TestUntypedPositionsRoundTripThroughFloat64ByDefault pins what the default
// does with the same document, so that the flag's reason for existing is a
// measurement in the suite rather than a claim in a comment.
//
// The program expects the value back as sent and this test expects the program
// to fail, naming the position and both spellings. It is written as a positive
// assertion on the exact text rather than as "the default differs" so that a
// change in *how* the default loses the value -- encoding/json learning to keep
// a trailing zero, say -- shows up as a failure someone reads, rather than
// passing because it was still wrong somehow.
func TestUntypedPositionsRoundTripThroughFloat64ByDefault(t *testing.T) {
	out := runGeneratedMainProgramOutput(t, untypedPositionsSchema, "rawuntyped_default", untypedRoundTripMain, generator.Config{
		PackageName: "testpkg",
		OmitEmpty:   true,
	})
	for _, want := range []string{
		"members that did not come back as sent:",
		// The integer past 2^53, the trailing zeros, the big integer and the
		// member order, all lost in one position.
		`    sent: {"z":9007199254740993,"a":1.10,"m":[1.0,2.50],"big":123456789012345678901234567890}`,
		`    got:  {"a":1.1,"big":1.2345678901234568e+29,"m":[1,2.5],"z":9007199254740992}`,
		// -0.0 and 1e2 rewritten in a position whose schema is {}.
		`    sent: [1.0,1e2,-0.0,"s",null,true]`,
		`    got:  [1,100,-0,"s",null,true]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("default configuration output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "an outer decoder's UseNumber reached the generated type") {
		t.Errorf("an outer decoder's UseNumber reached an untyped field, which the doc comment on Config.RawUntyped says it cannot; the comment is now wrong:\n%s", out)
	}
}

// runGeneratedMainProgramOutput compiles the generated types for schemaPath
// together with the supplied main() and returns whatever the program printed,
// whether or not it exited cleanly. It is runGeneratedMainProgramWithConfig for
// a test whose subject is the program's report of a failure rather than its
// PASS -- so the program's exit code is the test's evidence, not its verdict.
// A program that did not build is still a test failure, since there is then
// nothing to read.
func runGeneratedMainProgramOutput(t *testing.T, schemaPath, moduleName, mainGo string, cfg generator.Config) string {
	t.Helper()
	generated := generateFromSchemaWithConfig(t, schemaPath, cfg)
	tmpDir := t.TempDir()

	generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}
	writeSharedHelpers(t, tmpDir, generatedMain)
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}
	if err := writeTestGoMod(tmpDir, moduleName); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	build := testgo.Command(ctx, tmpDir, "build", "-o", filepath.Join(tmpDir, "prog"), ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%s did not build:\n%s\nerror: %v", moduleName, string(out), err)
	}
	run := exec.CommandContext(ctx, filepath.Join(tmpDir, "prog"))
	run.Dir = tmpDir
	out, _ := run.CombinedOutput()
	return string(out)
}

// TestRawUntypedComparesRawElementsAsJSONValues holds the checks that read a
// raw element from beside an untyped schema to JSON equality.
//
// json.Marshal writes a RawMessage back as it stands, so a check keyed on those
// bytes would call 1 and 1.0 two elements and {"a":1,"b":2} and {"b":2,"a":1}
// two more, which is not what uniqueItems means, and would miss a contains
// const of 1 in an array holding 1.0, which is not what const means. The default
// gets the reduction from the decode into `any`; here the emitted check asks
// _jsonCanonical for it, and this is the assertion that it does so at each of
// the shapes the rule is built for: a property, a $defs alias, and an element
// that is itself an array.
func TestRawUntypedComparesRawElementsAsJSONValues(t *testing.T) {
	runValidationCasesWithConfig(t, untypedPositionsSchema, rawUntypedConfig(),
		[]string{
			`{"required":1,"unique":[1,2,"1",true,null,[1],{"a":1}]}`,
			`{"required":1,"unique":[{"a":1,"b":2},{"a":1,"b":3}]}`,
			`{"required":1,"nestedUnique":[[1,2],[1,2]]}`,
			`{"required":1,"aliasedList":[1.0,2]}`,
			`{"required":1,"aliasedList":[1e0]}`,
			`{"required":1,"containsConst":[2,1.0]}`,
			`{"required":1,"containsConst":[1e0]}`,
			`{"required":1,"containsEnum":[1.50]}`,
			`{"required":1,"containsEnum":[{"k":1.0}]}`,
		},
		[]string{
			`{"required":1,"unique":[1,1.0]}`,                            // one number, two spellings
			`{"required":1,"unique":[1,1e0]}`,                            // the exponent spelling of the same
			`{"required":1,"unique":[{"a":1,"b":2},{"b":2,"a":1}]}`,      // one object, two member orders
			`{"required":1,"unique":[[1.0],[1]]}`,                        // the reduction reaches inside an array
			`{"required":1,"nestedUnique":[[1,1.0]]}`,                    // the same on an element that is itself an array
			`{"required":1,"aliasedList":[1,1.0]}`,                       // and on a $defs alias
			`{"required":1,"aliasedList":[2]}`,                           // the alias's contains: no 1.0 here
			`{"required":1,"containsConst":[2,"1"]}`,                     // a string is not the number 1
			`{"required":1,"containsEnum":[1.5000000000000001,{"k":2}]}`, // neither member
		},
	)
}
