package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// caselessFixture is the schema both tests below are about.
const caselessFixture = "testdata/schemas/regression/caseless_property_names.json"

// caselessBagNames reads the property names the fixture declares inside one of
// its bags, so that the documents these tests build are derived from the fixture
// rather than restated beside it.
//
// The untaggable-names fixture keeps its list in Go and the schema in JSON, and
// the two can drift without a failure: a name present in the Go list and absent
// from the schema lands in AdditionalProperties, round-trips perfectly, and
// proves nothing about the field the test believes it is exercising. Reading the
// names out of the fixture removes that failure mode -- a name added to the
// schema is exercised at every position without anyone editing a test, and a
// name that is only in the test cannot exist.
func caselessBagNames(t *testing.T, bag string) []string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", caselessFixture))
	if err != nil {
		t.Fatalf("reading %s: %v", caselessFixture, err)
	}
	var doc struct {
		Properties map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing %s: %v", caselessFixture, err)
	}
	names := make([]string, 0, len(doc.Properties[bag].Properties))
	for n := range doc.Properties[bag].Properties {
		names = append(names, n)
	}
	if len(names) == 0 {
		t.Fatalf("%s declares no properties under %q", caselessFixture, bag)
	}
	sort.Strings(names)
	return names
}

// TestCaselessPropertyNamesRoundTripAtEveryPosition is issue #254: a property
// name in a script that has no upper case became an unexported Go field, and
// encoding/json ignores an unexported field however good its struct tag is.
//
//	{"日本語":"v","Ωμέγα":true}  →  {"Ωμέγα":true}
//
// Total silent loss, in both directions, with Validate passing either way -- the
// decoded value was never set, so nothing downstream could tell the property
// from an absent one. The Greek neighbour is in the same document because it is
// what makes the shape of the defect visible: Ω is upper case, so Greek survived
// and Japanese did not, and the difference between them is the writing system
// rather than anything either schema said.
//
// The assertion is the effect, not the field name. A golden pinning the Go names
// would have recorded the defect as intended behaviour just as happily as it
// records the fix, and the question this test exists to answer is whether a
// document survives the type, which only running one can say. Every name is
// carried at six positions -- required field, optional field, a nested object, a
// map value, an array element and a $defs type reached by $ref -- because this
// defect's family is "fixed in one arm, not its twin", and the derivation is
// shared by all six.
//
// The controls are the larger half of the fixture and matter more than the
// caseless names. Cyrillic (привет) and Greek (Ωμέγα) have case and were always
// carried correctly; café, ok, 1a and _x are the ordinary shapes; "", "🎉", "-"
// and "a,b" are the names #255 routes to the hand-written JSON path, which this
// change must not touch. A fix that renamed any of those would break every
// generated type that has ever used them, which is worse than the bug.
func TestCaselessPropertyNamesRoundTripAtEveryPosition(t *testing.T) {
	runGeneratedMainProgram(t, caselessFixture, "caseless_property_names_test", caselessNamesMain(t))
}

// caselessNamesMain builds the program body.
func caselessNamesMain(t *testing.T) string {
	t.Helper()

	names := caselessBagNames(t, "requiredBag")
	collide := caselessBagNames(t, "collide")

	bag := func(names []string) map[string]any {
		m := make(map[string]any, len(names))
		for i, n := range names {
			m[n] = fmt.Sprintf("v%d", i)
		}
		return m
	}
	full := bag(names)

	docs := []any{
		// Every name, at every position the fixture provides.
		map[string]any{
			"requiredBag":   full,
			"optionalBag":   full,
			"nested":        map[string]any{"inner": full},
			"mapValues":     map[string]any{"k": full},
			"arrayElements": []any{full, full},
			// The collision bag: 日本語 beside X日本語 beside 日-本-語, three
			// distinct properties whose Go names are minted from one stem. All
			// three values must come back, against their own keys.
			"collide": bag(collide),
			// A $defs type named 日本語型 -- the type name takes the same
			// derivation as the field names, and an unexported type is unusable
			// from the package a caller writes.
			"defRef": full,
		},
		// Only what the schema demands, so that no optional field invents a key.
		map[string]any{"requiredBag": full},
		// Present-but-empty containers, which must not be read as absent ones.
		map[string]any{
			"requiredBag":   full,
			"optionalBag":   map[string]any{},
			"nested":        map[string]any{"inner": map[string]any{}},
			"mapValues":     map[string]any{},
			"arrayElements": []any{},
			"collide":       map[string]any{},
			"defRef":        map[string]any{},
		},
	}
	// One name at a time in an otherwise empty optional bag. A document carrying
	// every name at once cannot tell "this key survived" from "some key with this
	// value survived" -- two fields that swapped tags would still return a
	// document with the right number of keys.
	for _, n := range names {
		docs = append(docs, map[string]any{
			"requiredBag": full,
			"optionalBag": map[string]any{n: "solo"},
		})
	}
	for _, n := range collide {
		docs = append(docs, map[string]any{
			"requiredBag": full,
			"collide":     map[string]any{n: "solo"},
		})
	}

	encoded := make([]string, len(docs))
	for i, d := range docs {
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatalf("encoding document %d: %v", i, err)
		}
		encoded[i] = string(raw)
	}

	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
)

func main() {
	docs := []string{%s}
	failed := false
	for _, in := range docs {
		var obj CaselessNames
		if err := json.Unmarshal([]byte(in), &obj); err != nil {
			fmt.Fprintf(os.Stderr, "unmarshal %%s: %%v\n", in, err)
			failed = true
			continue
		}
		// The input is a document its own schema accepts. If it is not, the
		// fixture and the documents have drifted apart and every verdict below
		// is measuring the wrong thing.
		if err := obj.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "INPUT REJECTED\n  in:  %%s\n  err: %%v\n", in, err)
			failed = true
			continue
		}
		out, err := json.Marshal(obj)
		if err != nil {
			fmt.Fprintf(os.Stderr, "marshal %%s: %%v\n", in, err)
			failed = true
			continue
		}
		var original, result any
		if err := json.Unmarshal([]byte(in), &original); err != nil {
			fmt.Fprintf(os.Stderr, "unmarshal original %%s: %%v\n", in, err)
			failed = true
			continue
		}
		if err := json.Unmarshal(out, &result); err != nil {
			fmt.Fprintf(os.Stderr, "output is not JSON %%s: %%v\n", string(out), err)
			failed = true
			continue
		}
		if !reflect.DeepEqual(original, result) {
			fmt.Fprintf(os.Stderr, "ROUND-TRIP MISMATCH\n  in:  %%s\n  out: %%s\n", in, string(out))
			failed = true
			continue
		}
		// And the output is still a document the schema accepts. Round-trip
		// equality implies it; it is asserted anyway because a dropped required
		// property is what the issue reported and is what a reader looks for.
		var again CaselessNames
		if err := json.Unmarshal(out, &again); err != nil {
			fmt.Fprintf(os.Stderr, "re-unmarshal %%s: %%v\n", string(out), err)
			failed = true
			continue
		}
		if err := again.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "OUTPUT FAILS ITS OWN SCHEMA\n  in:  %%s\n  out: %%s\n  err: %%v\n", in, string(out), err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`, goStringSliceElems(encoded))
}

// TestGeneratedCodeForCaselessNamesPassesGoVet is the diagnostic half of #254,
// and the reason it is a separate test is that it was already available and
// nothing was reading it. `go vet` reports
//
//	struct field 日本語 has json tag but is not exported
//
// on the generated file, so schemagen was emitting code its own toolchain
// flagged, under a zero exit code, for as long as the defect existed. Asserting
// it here turns that into a gate: the vet check is exactly the "a tag that
// cannot reach its field" rule, stated by the toolchain rather than by another
// hand-written model of encoding/json inside this repository.
//
// It is worth its own toolchain invocation for what it reads rather than for
// what it currently catches. The round-trip above visits every name the fixture
// declares, so today the two agree; but the round-trip can only speak about a
// field some document reaches, and vet speaks about every field of every type in
// the package. A name added to the fixture in a position the documents do not
// build, or a type the generator mints for its own reasons, is inside vet's
// answer and outside the round-trip's. It is also the check whose silence was
// the tell: this warning was there for the whole life of the defect, and nothing
// in the repository was reading it.
func TestGeneratedCodeForCaselessNamesPassesGoVet(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a generated package with the go toolchain")
	}

	generated := generateFromSchema(t, caselessFixture)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "types.go"), generated, 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}
	writeSharedHelpers(t, dir, string(generated))
	if err := writeTestGoMod(dir, "caseless_vet_test"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "vet", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go vet on the generated package for %s failed:\n%s", caselessFixture, strings.TrimSpace(string(out)))
	}
	if reported := programOutput(out); reported != "" {
		t.Fatalf("go vet on the generated package for %s reported:\n%s", caselessFixture, reported)
	}
}
