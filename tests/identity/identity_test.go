package identity

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
)

// The checks that compare values -- uniqueItems, const, enum -- compare them by
// identity (the emitted jsonID), read off each value as it is held: a struct's
// jsonIdentity reads its members by the rules its appendJSON writes them by. They
// used to marshal the value and compare the text, which at every level of a
// document wrote out every subtree below it -- 0.8 to 3 ms of Validate per
// CycloneDX BOM. The two tests here hold the replacement to what it replaced.
//
// TestIdentityIsWhatMarshalJSONWrites is the correctness half: an identity read
// off a value is the identity of the text MarshalJSON writes for it, for every
// value of every type that reads its own, in every CycloneDX example BOM -- and
// again with the value changed the ways a document never changes it, which is
// where the rules a decoded value never exercises are: a member set twice by
// the overflow map, a string that is not UTF-8, the spellings of a number.
//
// TestValidateWritesNothing is the other half: Validate, run over every BOM,
// executes no statement that writes a value out.

func TestIdentityIsWhatMarshalJSONWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("generates and compiles the CycloneDX 1.6 types")
	}
	t.Parallel()
	root, boms := cycloneDXModule(t, map[string]string{
		"cdx/identity_check.go": identityCheckSource("cdx"),
		"idcheck/main.go":       identityCheckDriver,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := testgo.Command(ctx, root, append([]string{"run", "-mod=mod", "./idcheck"}, boms...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("driver: %v\n%s", err, output)
	}
	got := strings.TrimSpace(string(output))
	m := regexp.MustCompile(`^PASS (\d+) values, (\d+) changed$`).FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("identities that are not what MarshalJSON writes:\n%s", got)
	}
	// A floor, so that a walk that reached nothing -- no type reading its own
	// identity, or none of the values -- does not pass for having found no
	// difference.
	values, _ := strconv.Atoi(m[1])
	changed, _ := strconv.Atoi(m[2])
	if values < 500 || changed < 3000 {
		t.Fatalf("the walk compared %d values and %d changed ones; the BOMs hold far more", values, changed)
	}
	t.Logf("compared %d values and %d changed values across %d BOMs", values, changed, len(boms))
}

const identityCheckDriver = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"ex.test/cdx/cdx"
)

func main() {
	values, changed, bad := 0, 0, 0
	for _, path := range os.Args[1:] {
		in, err := os.ReadFile(path)
		if err != nil {
			panic(err)
		}
		var bom cdx.Bom
		if err := json.Unmarshal(in, &bom); err != nil {
			fmt.Printf("%s: does not decode: %v\n", filepath.Base(path), err)
			bad++
			continue
		}
		diffs, v, c := cdx.SchemagenIdentityDiffs(&bom)
		values += v
		changed += c
		for _, d := range diffs {
			fmt.Printf("%s: %s\n", filepath.Base(path), d)
			bad++
		}
	}
	if bad == 0 {
		fmt.Printf("PASS %d values, %d changed\n", values, changed)
	}
}
`

func TestValidateWritesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("generates, compiles and runs the CycloneDX 1.6 types with coverage")
	}
	t.Parallel()
	root, boms := cycloneDXModule(t, map[string]string{"covdrv/main.go": validateCoverageDriver})
	writes, executed := validateUnderCoverage(t, root, "cdx", boms, fmt.Sprintf("validated %d", len(boms)))
	// A floor, as in the identity test: counters that recorded nothing would
	// find nothing.
	if executed < 1000 {
		t.Fatalf("only %d blocks of the generated package ran during Validate; the counters are not reading it", executed)
	}
	if len(writes) > 0 {
		t.Errorf("Validate wrote values out, %d places, over the %d CycloneDX example BOMs:\n\t%s",
			len(writes), len(boms), strings.Join(writes, "\n\t"))
	}
}

// validateUnderCoverage builds root's ./covdrv with coverage over the whole
// module, runs it with args after the counters directory -- the driver clears
// the counters once it has decoded what it validates -- checks it printed want,
// and returns the statements of package dir that ran and write a value out,
// and how many of that package's blocks ran.
func validateUnderCoverage(t *testing.T, root, dir string, args []string, want string) ([]string, int) {
	t.Helper()
	// The module is named after dir and so is the package in it: a coverage
	// profile names the package's files by its import path.
	return profileWrites(t, runUnderCoverage(t, root, args, want), root, "ex.test/"+dir, []string{dir})
}

// runUnderCoverage builds root's ./covdrv with coverage over the whole module,
// runs it with args after the counters directory, checks it printed want, and
// returns the coverage profile it wrote.
func runUnderCoverage(t *testing.T, root string, args []string, want string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	bin := filepath.Join(root, "covdrv.bin")
	// The runtime module is covered too: the identity, the encoder and the
	// canonical reduction Validate would reach for are its code, not the
	// generated package's, and a write there is as much a write.
	build := testgo.Command(ctx, root, "build", "-mod=mod", "-cover", "-covermode=atomic", "-coverpkg=./...,"+testgo.RuntimeModulePath, "-o", bin, "./covdrv")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the driver with coverage: %v\n%s", err, out)
	}
	counters := filepath.Join(root, "counters")
	if err := os.MkdirAll(counters, 0o755); err != nil {
		t.Fatal(err)
	}
	run := exec.CommandContext(ctx, bin, append([]string{counters}, args...)...)
	run.Env = append(os.Environ(), "GOCOVERDIR="+counters)
	if out, err := run.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != want {
		t.Fatalf("driver: %v\n%s", err, out)
	}
	profile := filepath.Join(root, "profile.txt")
	textfmt := testgo.Command(ctx, root, "tool", "covdata", "textfmt", "-i="+counters, "-o="+profile)
	if out, err := textfmt.CombinedOutput(); err != nil {
		t.Fatalf("reading the counters: %v\n%s", err, out)
	}
	return profile
}

// profileWrites is executedWrites over each of pkgs, directories of the module
// at root, whose path is module.
func profileWrites(t *testing.T, profile, root, module string, pkgs []string) ([]string, int) {
	t.Helper()
	var writes []string
	executed := 0
	for _, pkg := range pkgs {
		w, n, err := executedWrites(profile, filepath.Join(root, pkg), module+"/"+pkg+"/")
		if err != nil {
			t.Fatal(err)
		}
		writes = append(writes, w...)
		executed += n
	}
	// And the runtime module the package calls into, whose blocks are not
	// counted in executed: that floor is about the generated package.
	runtimeDir, err := testgo.RuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	w, _, err := executedWrites(profile, runtimeDir, testgo.RuntimeModulePath+"/")
	if err != nil {
		t.Fatal(err)
	}
	return append(writes, w...), executed
}

// TestHeldElementsAreJudgedWithoutWriting is TestValidateWritesNothing for the
// elements held as decoded JSON whose sub-schema has a type of its own -- a
// tuple position, a contains, an inferred array's items, a tuple's tail. They
// were marshalled and decoded into the type to be judged; now they are judged
// as they are held, by the type's schema compiled for the evaluator (see
// ElementNode). The values are decoded ones and ones built in Go -- an element
// holding a value of the generated type itself, a pointer to one, an int64 --
// and each is held to the verdict the schema gives it.
func TestHeldElementsAreJudgedWithoutWriting(t *testing.T) {
	if testing.Short() {
		t.Skip("generates, compiles and runs a package with coverage")
	}
	t.Parallel()
	out := t.TempDir()
	schemaPath := filepath.Join(out, "held.json")
	if err := os.WriteFile(schemaPath, []byte(heldElementsSchema), 0o644); err != nil {
		t.Fatal(err)
	}
	runSchemagen(t, schemagenBinary(t), "generate", schemaPath, "-o", filepath.Join(out, "held"), "-p", "held", "--root-name", "held.json=Root")
	if err := writeTestGoMod(out, "ex.test/held"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(out, "covdrv"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "covdrv", "main.go"), []byte(heldElementsDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	writes, executed := validateUnderCoverage(t, out, "held", nil, "PASS")
	if executed < 50 {
		t.Fatalf("only %d blocks of the generated package ran during Validate; the counters are not reading it", executed)
	}
	if len(writes) > 0 {
		t.Errorf("Validate wrote values out to judge held elements, %d places:\n\t%s", len(writes), strings.Join(writes, "\n\t"))
	}
}

const heldElementsSchema = `{"$schema":"https://json-schema.org/draft/2020-12/schema",
 "$defs":{"P":{"type":"object","properties":{"a":{"type":"integer","minimum":1},"k":{"type":"array","items":{"$ref":"#/$defs/P"}}},"required":["a"]},
          "Loose":{"items":{"$ref":"#/$defs/P"},"minItems":1}},
 "type":"object",
 "properties":{
   "tup":{"type":"array","prefixItems":[{"$ref":"#/$defs/P"},{"type":"string"}],"items":{"$ref":"#/$defs/P"}},
   "has":{"type":"array","contains":{"$ref":"#/$defs/P"}},
   "loose":{"$ref":"#/$defs/Loose"},
   "tail":{"type":"array","prefixItems":[{"type":"string"}],"items":{"type":"object","required":["a"],"properties":{"a":{"const":2}}}}
 }}`

const heldElementsDriver = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/coverage"

	"ex.test/held/held"
)

func main() {
	docs := []struct {
		doc   string
		valid bool
	}{
		{` + "`" + `{"tup":[{"a":1,"k":[{"a":2}]},"x",{"a":3}],"has":[1,{"a":1}],"loose":[{"a":1}],"tail":["s",{"a":2}]}` + "`" + `, true},
		{` + "`" + `{"tup":[{"a":0},"x"]}` + "`" + `, false},
		{` + "`" + `{"tup":[{"a":1,"k":[{"a":-1}]}]}` + "`" + `, false},
		{` + "`" + `{"has":[1,"x",{"b":1}]}` + "`" + `, false},
		{` + "`" + `{"loose":[{"a":1},{}]}` + "`" + `, false},
		{` + "`" + `{"tail":["s",{"a":3}]}` + "`" + `, false},
		{` + "`" + `{"tup":[{"a":1},"x",{"a":1.5}]}` + "`" + `, false},
	}
	var decoded []held.Root
	for _, d := range docs {
		var r held.Root
		if err := json.Unmarshal([]byte(d.doc), &r); err != nil {
			fmt.Println("decode:", d.doc, err)
			os.Exit(1)
		}
		decoded = append(decoded, r)
	}
	built := []struct {
		r     held.Root
		valid bool
	}{
		{held.Root{Tup: []any{held.P{A: 1, K: []held.P{{A: 2}}}, "x", &held.P{A: 3}}, Has: []any{int64(1), held.P{A: 1}}, Tail: []any{"s", map[string]any{"a": int64(2)}}}, true},
		{held.Root{Tup: []any{held.P{A: 0}, "x"}}, false},
		{held.Root{Tup: []any{map[string]any{"a": int64(1), "k": []any{held.P{A: -1}}}}}, false},
		{held.Root{Tail: []any{"s", held.P{A: 2}}}, true},
		{held.Root{Tail: []any{"s", map[string]any{"a": 3}}}, false},
		{held.Root{Tup: []any{&held.P{A: 1}, "x", float32(1.5)}}, false},
	}
	if err := coverage.ClearCounters(); err != nil {
		panic(err)
	}
	for i, r := range decoded {
		if err := r.Validate(); (err == nil) != docs[i].valid {
			fmt.Println("decoded:", docs[i].doc, "valid:", docs[i].valid, "Validate:", err)
			os.Exit(1)
		}
	}
	for i, b := range built {
		if err := b.r.Validate(); (err == nil) != b.valid {
			fmt.Println("built:", i, "valid:", b.valid, "Validate:", err)
			os.Exit(1)
		}
	}
	if err := coverage.WriteCountersDir(os.Args[1]); err != nil {
		panic(err)
	}
	fmt.Println("PASS")
}
`

// TestStrippedAndNulledValuesAreReadWithoutWriting holds the three rulings
// appendJSON makes by what it writes -- the writeOnly locations
// --strict-read-write strips from below a struct's members and from a value
// held whole, the nulls a document wrote that are written back, and a
// hand-written member whose Go zero is left out -- to what they were when the
// identity was read off the text appendJSON wrote. Now they are made on trees:
// each identity, and each tree, is compared with what MarshalJSON writes, for
// decoded values and for the same values changed the ways a document never
// changes them; and then Validate, comparing the values by uniqueItems, runs
// with coverage and must write nothing out.
func TestStrippedAndNulledValuesAreReadWithoutWriting(t *testing.T) {
	if testing.Short() {
		t.Skip("generates, compiles and runs a package with coverage")
	}
	t.Parallel()
	for _, c := range []struct {
		name  string
		flags []string
	}{
		{"strict", []string{"--strict-read-write"}},
		// Named without a comma: covdata reads one in its input directory as
		// a list of two.
		{"strict-noomit", []string{"--strict-read-write", "--omit-empty=false"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out := t.TempDir()
			schemaPath := filepath.Join(out, "sn.json")
			if err := os.WriteFile(schemaPath, []byte(strippedNulledSchema), 0o644); err != nil {
				t.Fatal(err)
			}
			runSchemagen(t, schemagenBinary(t), append([]string{"generate", schemaPath, "-o", filepath.Join(out, "sn"), "-p", "sn", "--root-name", "sn.json=Root"}, c.flags...)...)
			if err := writeTestGoMod(out, "ex.test/sn"); err != nil {
				t.Fatal(err)
			}
			for rel, content := range map[string]string{
				"sn/identity_check.go": identityCheckSource("sn"),
				"covdrv/main.go":       strippedNulledDriver,
			} {
				p := filepath.Join(out, rel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			writes, executed := validateUnderCoverage(t, out, "sn", nil, "PASS")
			if executed < 50 {
				t.Fatalf("only %d blocks of the generated package ran during Validate; the counters are not reading it", executed)
			}
			if len(writes) > 0 {
				t.Errorf("Validate wrote values out to compare them, %d places:\n\t%s", len(writes), strings.Join(writes, "\n\t"))
			}
		})
	}
}

// strippedNulledSchema has a type of each kind: Holder strips writeOnly members
// from below a tuple slot and a patternProperties value, Leftover is held whole
// and strips from its bytes, Nulls writes back the nulls a document wrote, and
// Zero's members, named so no struct tag can carry them, are left out where they
// write what their Go zero writes.
const strippedNulledSchema = `{"$schema":"https://json-schema.org/draft/2020-12/schema",
 "$defs":{
  "Access":{"type":"object","properties":{"ro":{"type":"integer","readOnly":true},"wo":{"type":"integer","writeOnly":true},"ok":{"type":"integer"}}},
  "Holder":{"type":"object","properties":{
      "tuple":{"type":"array","prefixItems":[{"$ref":"#/$defs/Access"}]},
      "patterned":{"type":"object","patternProperties":{"^k":{"$ref":"#/$defs/Access"}}},
      "plain":{"type":"string"}}},
  "Nulls":{"type":"object","properties":{
      "ns":{"type":["string","null"]},
      "nn":{"type":["number","null"]},
      "nl":{"type":["array","null"],"items":{"type":"string"}},
      "any":{},
      "s":{"type":"string"}}},
  "Zero":{"type":"object","properties":{"c,d":{"const":"fixed"},"e,f":{"type":"integer","minimum":5},"g,h":{"minLength":2},"i,j":{"type":"number","minimum":5}}},
  "Leftover":{"type":"object","unevaluatedProperties":{"$ref":"#/$defs/Access"}}
 },
 "type":"object",
 "properties":{
   "holders":{"type":"array","uniqueItems":true,"items":{"$ref":"#/$defs/Holder"}},
   "nulls":{"type":"array","uniqueItems":true,"items":{"$ref":"#/$defs/Nulls"}},
   "zeros":{"type":"array","uniqueItems":true,"items":{"$ref":"#/$defs/Zero"}},
   "leftovers":{"type":"array","uniqueItems":true,"items":{"$ref":"#/$defs/Leftover"}}
 }}`

// strippedNulledDriver checks every decoded document's identities against what
// MarshalJSON writes (see identityCheckSource), then clears the counters and
// validates each, holding it to its verdict. The duplicates are values written
// the same: the same members in another order, a number spelled another way, a
// null written back where the document wrote one.
const strippedNulledDriver = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/coverage"

	"ex.test/sn/sn"
)

func main() {
	docs := []struct {
		doc   string
		valid bool
	}{
		{` + "`" + `{"holders":[{"tuple":[{"wo":1,"ok":2,"x":[1,{"a":1.50}]}],"patterned":{"k1":{"wo":3,"ok":4},"z":{"wo":5}},"plain":"p"},{"tuple":[{"ok":3}]}]}` + "`" + `, true},
		{` + "`" + `{"holders":[{"tuple":[{"ok":2}],"plain":"p"},{"plain":"p","tuple":[{"ok":2.0}]}]}` + "`" + `, false},
		{` + "`" + `{"nulls":[{"ns":null,"nn":null,"nl":null,"any":null,"s":"a"},{"ns":"x","nn":-0.0,"nl":["a"],"any":{"b":[1,2]},"s":"a"},{"s":"b"}]}` + "`" + `, true},
		{` + "`" + `{"nulls":[{"ns":null,"s":"a"},{"s":"a","ns":null}]}` + "`" + `, false},
		{` + "`" + `{"zeros":[{"c,d":"fixed","e,f":7,"g,h":"xy"},{"e,f":8},{}]}` + "`" + `, true},
		{` + "`" + `{"zeros":[{"e,f":7},{"e,f":7.0}]}` + "`" + `, false},
		{` + "`" + `{"leftovers":[{"a":{"wo":1,"ok":2}},{"a":{"ok":3}},{"b":{"ok":2}}]}` + "`" + `, true},
		{` + "`" + `{"leftovers":[{"a":{"ok":2e0,"wo":1}},{"a":{"wo":2,"ok":2}}]}` + "`" + `, false},
	}
	var decoded []sn.Root
	values, changed := 0, 0
	for _, d := range docs {
		var r sn.Root
		if err := json.Unmarshal([]byte(d.doc), &r); err != nil {
			fmt.Println("decode:", d.doc, err)
			os.Exit(1)
		}
		diffs, v, c := sn.SchemagenIdentityDiffs(&r)
		for _, diff := range diffs {
			fmt.Println(d.doc, diff)
		}
		if len(diffs) > 0 {
			os.Exit(1)
		}
		values += v
		changed += c
		decoded = append(decoded, r)
	}
	// A floor: a walk that reached nothing would find nothing.
	if values < 30 || changed < 50 {
		fmt.Println("the identity walk compared", values, "values and", changed, "changed ones")
		os.Exit(1)
	}
	if err := coverage.ClearCounters(); err != nil {
		panic(err)
	}
	for i, r := range decoded {
		if err := r.Validate(); (err == nil) != docs[i].valid {
			fmt.Println("decoded:", docs[i].doc, "valid:", docs[i].valid, "Validate:", err)
			os.Exit(1)
		}
	}
	if err := coverage.WriteCountersDir(os.Args[1]); err != nil {
		panic(err)
	}
	fmt.Println("PASS")
}
`

// TestAnotherPackagesValuesAreComparedWithoutWriting is TestValidateWritesNothing
// across packages. A package generated beside another in one run compares
// values of the other's types -- uniqueItems over them, and elements it judges
// as it holds them -- and it cannot call their jsonIdentity, which is
// unexported: it used to read them off what their MarshalJSON wrote. Each such
// type now declares SchemagenJSONTree, and they are read by that. Under
// --schema-package the two are separate packages; under --shared-types they are
// one, the control. Validate runs with coverage over both packages' code, over
// decoded documents and values built in Go, holds each to its verdict, and must
// write nothing out.
func TestAnotherPackagesValuesAreComparedWithoutWriting(t *testing.T) {
	if testing.Short() {
		t.Skip("generates, compiles and runs two packages with coverage")
	}
	t.Parallel()
	src := t.TempDir()
	schemaPath := filepath.Join(src, "schema.json")
	otherPath := filepath.Join(src, "other.json")
	writeCrossFile(t, schemaPath, crossCompareSchema)
	writeCrossFile(t, otherPath, crossCompareOther)
	bin := schemagenBinary(t)
	for _, c := range []struct {
		name  string
		args  func(out string) []string
		pkgs  []string
		other string
	}{
		{"schema-package", func(out string) []string {
			return []string{"generate", schemaPath, otherPath, "-o", out,
				"--schema-package", "https://ex.test/schema.json=ex.test/xp/gen",
				"--schema-package", "https://ex.test/other.json=ex.test/xp/other",
				"--root-name", "schema.json=Root", "--root-name", "other.json=Other"}
		}, []string{"gen", "other"}, "other"},
		{"shared-types", func(out string) []string {
			return []string{"generate", schemaPath, otherPath, "-o", filepath.Join(out, "gen"), "-p", "gen", "--shared-types",
				"--root-name", "schema.json=Root", "--root-name", "other.json=Other"}
		}, []string{"gen"}, "gen"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out := t.TempDir()
			runSchemagen(t, bin, c.args(out)...)
			if err := writeTestGoMod(out, "ex.test/xp"); err != nil {
				t.Fatal(err)
			}
			driver := strings.ReplaceAll(crossCompareDriver, "@OTHER@", c.other)
			if c.other == "gen" {
				driver = strings.Replace(driver, "\tother \"ex.test/xp/gen\"\n", "", 1)
				driver = strings.ReplaceAll(driver, "other.", "gen.")
			}
			if err := os.MkdirAll(filepath.Join(out, "covdrv"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, "covdrv", "main.go"), []byte(driver), 0o644); err != nil {
				t.Fatal(err)
			}
			writes, executed := profileWrites(t, runUnderCoverage(t, out, nil, "PASS"), out, "ex.test/xp", c.pkgs)
			if executed < 50 {
				t.Fatalf("only %d blocks of the generated packages ran during Validate; the counters are not reading them", executed)
			}
			if len(writes) > 0 {
				t.Errorf("Validate wrote values out to compare another package's, %d places:\n\t%s", len(writes), strings.Join(writes, "\n\t"))
			}
		})
	}
}

const crossCompareSchema = `{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/schema.json","type":"object",
 "properties":{"items":{"type":"array","uniqueItems":true,"items":{"$ref":"other.json#/$defs/Item"}},
               "tup":{"type":"array","prefixItems":[{"$ref":"other.json#/$defs/Item"}]},
               "one":{"$ref":"other.json#/$defs/Item"}}}`

const crossCompareOther = `{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/other.json","type":"object",
 "$defs":{"Item":{"type":"object","properties":{"a":{"type":"integer","minimum":1},"b":{"type":"array","items":{"type":"string"}},"n":{"type":["string","null"]}}}}}`

const crossCompareDriver = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/coverage"

	gen "ex.test/xp/gen"
	other "ex.test/xp/@OTHER@"
)

func main() {
	docs := []struct {
		doc   string
		valid bool
	}{
		{` + "`" + `{"items":[{"a":1,"b":["x"]},{"a":2},{"a":1,"n":null},{"b":["x"],"a":1,"n":"y"}],"tup":[{"a":3}]}` + "`" + `, true},
		{` + "`" + `{"items":[{"a":1,"b":["x"]},{"b":["x"],"a":1.0}]}` + "`" + `, false},
		{` + "`" + `{"items":[{"n":null},{"n":null}]}` + "`" + `, false},
		{` + "`" + `{"tup":[{"a":0}]}` + "`" + `, false},
	}
	var decoded []gen.Root
	for _, d := range docs {
		var r gen.Root
		if err := json.Unmarshal([]byte(d.doc), &r); err != nil {
			fmt.Println("decode:", d.doc, err)
			os.Exit(1)
		}
		decoded = append(decoded, r)
	}
	one, two := int64(1), int64(2)
	built := []struct {
		r     gen.Root
		valid bool
	}{
		{gen.Root{Items: []other.Item{{A: &one}, {A: &two}}, Tup: []any{other.Item{A: &two}}}, true},
		// An optional property's keywords are judged where the document wrote
		// it, which a value built in Go never says; so the duplicate is not
		// looked at, and its elements are read only by the tuple below.
		{gen.Root{Items: []other.Item{{A: &one, B: []string{"x"}}, {A: &one, B: []string{"x"}}}}, true},
		{gen.Root{Tup: []any{&other.Item{A: new(int64)}}}, false},
	}
	if err := coverage.ClearCounters(); err != nil {
		panic(err)
	}
	for i, r := range decoded {
		if err := r.Validate(); (err == nil) != docs[i].valid {
			fmt.Println("decoded:", docs[i].doc, "valid:", docs[i].valid, "Validate:", err)
			os.Exit(1)
		}
	}
	for i, b := range built {
		if err := b.r.Validate(); (err == nil) != b.valid {
			fmt.Println("built:", i, "valid:", b.valid, "Validate:", err)
			os.Exit(1)
		}
	}
	if err := coverage.WriteCountersDir(os.Args[1]); err != nil {
		panic(err)
	}
	fmt.Println("PASS")
}
`

// encodingCall is every way generated code writes a value out: encoding/json,
// a MarshalJSON, the generated encoder and the runtime's (AppendLeaf, the
// LeafOmit helpers), and the reduction to canonical text, which writes strings
// through encoding/json.
var encodingCall = regexp.MustCompile(`json\.Marshal\(|json\.MarshalIndent\(|json\.NewEncoder\(|\.MarshalJSON\(\)|\.appendJSON\(|AppendLeaf|LeafOmit|_jsonCanonical\(|\bCanonical\(`)

// executedWrites reads a coverage profile and reports every block of the
// package in dir that ran and writes a value out, and how many of its blocks
// ran at all.
func executedWrites(profile, dir, marker string) ([]string, int, error) {
	data, err := os.ReadFile(profile)
	if err != nil {
		return nil, 0, err
	}
	sources := map[string][]string{}
	found := map[string]bool{}
	executed := 0
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		// file:startLine.startCol,endLine.endCol statements count
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[2] == "0" {
			continue
		}
		file, span, ok := strings.Cut(fields[0], ":")
		if !ok || !strings.Contains(file, marker) {
			continue
		}
		executed++
		base := filepath.Base(file)
		src, ok := sources[base]
		if !ok {
			b, err := os.ReadFile(filepath.Join(dir, base))
			if err != nil {
				return nil, 0, err
			}
			src = strings.Split(string(b), "\n")
			sources[base] = src
		}
		from, to, _ := strings.Cut(span, ",")
		start, _ := strconv.Atoi(strings.SplitN(from, ".", 2)[0])
		end, _ := strconv.Atoi(strings.SplitN(to, ".", 2)[0])
		if start < 1 || end > len(src) || start > end {
			return nil, 0, fmt.Errorf("%s: block %s is outside the file", base, span)
		}
		for i := start; i <= end; i++ {
			if encodingCall.MatchString(src[i-1]) {
				found[fmt.Sprintf("%s:%d: %s", base, i, strings.TrimSpace(src[i-1]))] = true
			}
		}
	}
	var out []string
	for k := range found {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, executed, nil
}

// validateCoverageDriver decodes every BOM, clears the coverage counters, and
// validates every BOM, so that the counters it writes hold what Validate ran and
// nothing else.
const validateCoverageDriver = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/coverage"

	"ex.test/cdx/cdx"
)

func main() {
	var boms []cdx.Bom
	for _, path := range os.Args[2:] {
		in, err := os.ReadFile(path)
		if err != nil {
			panic(err)
		}
		var bom cdx.Bom
		if err := json.Unmarshal(in, &bom); err != nil {
			fmt.Printf("%s: does not decode: %v\n", path, err)
			os.Exit(1)
		}
		boms = append(boms, bom)
	}
	if err := coverage.ClearCounters(); err != nil {
		panic(err)
	}
	for i := range boms {
		if err := boms[i].Validate(); err != nil {
			fmt.Printf("%s: does not validate: %v\n", os.Args[2+i], err)
			os.Exit(1)
		}
	}
	if err := coverage.WriteCountersDir(os.Args[1]); err != nil {
		panic(err)
	}
	fmt.Printf("validated %d\n", len(boms))
}
`
