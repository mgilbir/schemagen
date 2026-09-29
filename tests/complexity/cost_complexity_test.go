package complexity

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
)

// Decoding a document, validating the value and marshalling it back each cost in
// proportion to the document, however deeply the generated types nest.
//
// Every generated UnmarshalJSON used to parse its object into raw members, cut
// it down and re-encode it, decode it again through an alias struct, and hand
// each member to encoding/json -- which scanned the member once more before
// calling the member type's UnmarshalJSON, which did the same to its own
// subtree. Each level scanned everything below it, so a document nested d
// levels deep cost d times its size: {"c":{"c":...}} against a schema whose
// "c" is a $ref to itself took six seconds at 8,000 levels and 48 KB, where
// encoding/json decodes it into an `any` in ten milliseconds. A refusal at the
// bottom was worse -- exponential in the depth, because it was traced by
// decoding each member again on its own, and each of those decodes traced its
// own refusal the same way: a 200-byte document hung the decoder.
//
// MarshalJSON had the same shape from the other side: each level marshalled its
// members through encoding/json, which checked and compacted the bytes every
// member type's own MarshalJSON returned -- the whole subtree, at every level
// -- and a type with members to write by hand parsed its own output back into a
// map and marshalled that again. Validate decoded what a value holds as raw
// JSON -- a patternProperties value, a member a branch's additionalProperties
// or an unevaluatedProperties judges, a draft 3 schema-valued type, a tuple
// position -- into its type at every level, and so decoded the rest of the
// document once per level; a keyword judged at run time decoded every member
// whole, the recursive one included.
//
// The test holds every recursive shape the generator decodes through -- a
// member, an element, a map value, a union branch, a chain of named types, an
// alias over a slice, a conditional that keeps members for Validate, a wrapper
// that keeps raw bytes, a patternProperties value, a branch's overflow, an
// unevaluatedProperties, a run-time keyword, a type schema, a tuple -- to
// growing linearly: the
// decode of a document that decodes and of one refused at its deepest point,
// the Validate of the decoded value and of one refused at its deepest point, and
// the MarshalJSON of the decoded value. Allocations are the signal: they are
// deterministic, so the bounds can be tight enough to tell eight times from
// sixty-four. Wall time is checked too, loosely enough to survive a loaded
// machine. The live heap a decoded value holds is held to a multiple of the
// document's own size, which is what copying each level's raw members per level
// broke: a 24 KB document left 70 MB of live heap behind.

// costShape is one recursive schema and the documents it is measured on.
type costShape struct {
	name   string
	schema string
	flags  []string
	// unit is the JSON nesting one level of the shape adds: encoding/json
	// refuses a document nested more than 10,000 deep, so a shape nesting two
	// containers per level is measured at half the depth.
	unit int
	// open and close build a document of depth levels: open repeated, then
	// the leaf, then close repeated. good is the leaf of a document that
	// decodes and validates, bad the leaf of one refused by the decode at the
	// bottom, and invalid the leaf of one that decodes and is refused by
	// Validate at the bottom. "" where no such document exists.
	open, close, good, bad, invalid string
	// nativeMarshal is set for a shape whose value no generated code writes:
	// encoding/json writes it alone, and its own cost is not this test's to
	// hold -- past 1,000 levels it starts tracking every container it enters
	// to detect a cycle, which moves its allocation between the first depth
	// measured and the rest.
	nativeMarshal bool
}

var costShapes = []costShape{
	{
		name:   "member",
		schema: `{"type":"object","properties":{"c":{"$ref":"#"},"n":{"type":"string","maxLength":3}}}`,
		unit:   1, open: `{"n":"x","c":`, close: `}`, good: `{"n":"y"}`, bad: `{"n":1}`, invalid: `{"n":"long"}`,
	},
	{
		// An if/then beside the members keeps them as raw JSON for Validate
		// (_jsonRawProps); each level used to keep a copy of its whole subtree.
		name: "conditional",
		schema: `{"type":"object","properties":{"c":{"$ref":"#"},"n":{"type":"string"}},
		  "if":{"properties":{"n":{"const":"a"}},"required":["n"]},"then":{"required":["c"]}}`,
		unit: 1, open: `{"n":"a","c":`, close: `}`, good: `{"n":"y"}`, bad: `{"n":1}`, invalid: `{"n":"a"}`,
	},
	{
		name:   "element",
		schema: `{"type":"object","properties":{"kids":{"type":"array","items":{"$ref":"#"}},"n":{"type":"string","maxLength":3}}}`,
		unit:   2, open: `{"n":"x","kids":[`, close: `]}`, good: `{"n":"y"}`, bad: `{"n":1}`, invalid: `{"n":"long"}`,
	},
	{
		name:   "map value",
		schema: `{"type":"object","additionalProperties":{"$ref":"#"},"maxProperties":1}`,
		unit:   1, open: `{"k":`, close: `}`, good: `{}`, bad: `1`, invalid: `{"a":{},"b":{}}`,
	},
	{
		// A union selected by its branch's required member, which is read off
		// the document at every level.
		name: "union",
		schema: `{"$ref":"#/$defs/U","$defs":{"U":{"oneOf":[{"$ref":"#/$defs/O"},{"type":"string"}]},
		  "O":{"type":"object","properties":{"c":{"$ref":"#/$defs/U"}},"required":["c"],"additionalProperties":false}}}`,
		unit: 1, open: `{"c":`, close: `}`, good: `"leaf"`, bad: `5`,
	},
	{
		// Two branches that both take an object, one of which declares none
		// of its members and holds them as raw JSON: every level's trial of it
		// used to copy the rest of the document.
		name: "overlapping union",
		schema: `{"$ref":"#/$defs/U","$defs":{"U":{"oneOf":[{"$ref":"#/$defs/O"},{"type":"object","properties":{"z":{"type":"string"}},"required":["z"]}]},
		  "O":{"type":"object","properties":{"c":{"$ref":"#/$defs/U"}},"required":["c"]}}}`,
		// No document of this shape is refused at the bottom: wherever the
		// branch that recurses fails, the one that holds the member as raw
		// JSON takes the level instead. The second branch requires "z", so
		// only the bottom level is its and the document validates.
		unit: 1, open: `{"c":`, close: `}`, good: `{"z":"x"}`,
	},
	{
		// The member reaches the next level through two more names.
		name: "ref chain",
		schema: `{"$ref":"#/$defs/N","$defs":{"N":{"type":"object","properties":{"c":{"$ref":"#/$defs/A"},"n":{"type":"string","maxLength":3}}},
		  "A":{"$ref":"#/$defs/B"},"B":{"$ref":"#/$defs/N"}}}`,
		unit: 1, open: `{"n":"x","c":`, close: `}`, good: `{"n":"y"}`, bad: `{"n":1}`, invalid: `{"n":"long"}`,
	},
	{
		// An alias over a slice of the type that holds it.
		name: "slice alias",
		schema: `{"$ref":"#/$defs/N","$defs":{"N":{"type":"object","properties":{"k":{"$ref":"#/$defs/L"},"n":{"type":"string","maxLength":3}}},
		  "L":{"type":"array","items":{"$ref":"#/$defs/N"}}}}`,
		unit: 2, open: `{"n":"x","k":[`, close: `]}`, good: `{"n":"y"}`, bad: `{"n":1}`, invalid: `{"n":"long"}`,
	},
	{
		// A member held as raw JSON at every level, beside the one that recurses.
		name:   "raw wrapper",
		schema: `{"type":"object","properties":{"c":{"$ref":"#"},"d":{"oneOf":[{"minimum":1},{"maximum":0}]},"n":{"type":"string","maxLength":3}}}`,
		unit:   1, open: `{"d":0,"n":"x","c":`, close: `}`, good: `{"n":"y"}`, bad: `{"n":1}`, invalid: `{"n":"long"}`,
	},
	{
		// --strict-read-write walks the rules below a struct's own members over
		// the document at every level, and strips the writeOnly ones on the way
		// out.
		name: "strict read-write",
		schema: `{"type":"object","properties":{"c":{"$ref":"#"},"n":{"type":"string","maxLength":3},
		  "t":{"type":"array","prefixItems":[{"type":"object","properties":{"ro":{"type":"string","readOnly":true},"wo":{"type":"string","writeOnly":true}}}]}}}`,
		flags: []string{"--strict-read-write"},
		unit:  1, open: `{"n":"x","t":[{"wo":"s"}],"c":`, close: `}`, good: `{"n":"y"}`, bad: `{"n":1}`, invalid: `{"n":"long"}`,
	},
	{
		// A patternProperties value is held as raw JSON; Validate decodes it
		// into its type. The decode never refuses at the bottom: nothing below
		// the first level is decoded until Validate asks.
		name:   "pattern properties",
		schema: `{"type":"object","properties":{"n":{"type":"string","maxLength":3}},"patternProperties":{"^c":{"$ref":"#"}}}`,
		unit:   1, open: `{"n":"x","c":`, close: `}`, good: `{"n":"y"}`, invalid: `{"n":"long"}`,
	},
	{
		// An allOf branch's additionalProperties, judged by decoding the
		// member the branch does not account for into the branch's type.
		name: "branch overflow",
		schema: `{"type":"object","properties":{"n":{"type":"string","maxLength":3}},
		  "allOf":[{"properties":{"n":{}},"additionalProperties":{"$ref":"#"}}]}`,
		unit: 1, open: `{"n":"x","c":`, close: `}`, good: `{"n":"y"}`, invalid: `{"n":"long"}`,
	},
	{
		// unevaluatedProperties with a schema, judged the same way.
		name:   "unevaluated",
		schema: `{"type":"object","properties":{"n":{"type":"string","maxLength":3}},"unevaluatedProperties":{"$ref":"#"}}`,
		unit:   1, open: `{"n":"x","c":`, close: `}`, good: `{"n":"y"}`, invalid: `{"n":"long"}`,
	},
	{
		// A keyword the static checks cannot state, evaluated at run time
		// against the object's raw members -- the recursive one among them.
		name: "runtime branch",
		schema: `{"type":"object","properties":{"c":{"$ref":"#"},"n":{"type":"string","maxLength":3}},
		  "anyOf":[{"properties":{"n":{}},"unevaluatedProperties":{"type":"object"}},{"required":["n"]}]}`,
		unit: 1, open: `{"n":"x","c":`, close: `}`, good: `{"n":"y"}`, invalid: `{"n":"long"}`,
	},
	{
		// A draft 3 schema-valued type, whose wrapper holds the value as raw
		// JSON and judges it by decoding it into the branch's type.
		name: "type schema",
		schema: `{"$ref":"#/$defs/T","$defs":{"T":{"type":["string",{"$ref":"#/$defs/O"}]},
		  "O":{"type":"object","properties":{"c":{"$ref":"#/$defs/T"},"n":{"type":"string","maxLength":3}}}}}`,
		unit: 1, open: `{"n":"x","c":`, close: `}`, good: `"y"`, invalid: `{"n":"long"}`,
	},
	{
		// The raw-JSON wrappers that judge what they hold by decoding it into an
		// any -- a bare "not", a multi-type "type", a schema held as data -- at
		// every level of a struct that recurses beside them, and then holding
		// the rest of the document themselves. Nothing below a wrapper is typed,
		// so nothing below one is judged again: the wrapper decodes what it holds
		// once.
		name: "not wrapper",
		schema: `{"type":"object","properties":{"c":{"$ref":"#"},"n":{"type":"string","maxLength":3},
		  "d":{"not":{"type":"number"}}}}`,
		unit: 2, open: `{"n":"x","d":[{"n":"x"}],"c":`, close: `}`, good: `{"n":"y"}`, bad: `{"n":1}`, invalid: `{"n":"long"}`,
	},
	{
		name: "type-only wrapper",
		schema: `{"type":"object","properties":{"c":{"$ref":"#"},"n":{"type":"string","maxLength":3},
		  "d":{"anyOf":[{"minItems":1},{"type":"string"}]}}}`,
		unit: 2, open: `{"n":"x","d":[{"n":"x"}],"c":`, close: `}`, good: `{"n":"y"}`, bad: `{"n":1}`, invalid: `{"n":"long"}`,
	},
	{
		// A schema held as data that recurses through itself: the whole
		// document is one wrapper's, judged by the evaluator, which reads the
		// document lazily -- a level at a time -- rather than decoding it whole
		// at the top and again at every level below.
		name: "dynamic root",
		schema: `{"$schema":"https://json-schema.org/draft/2020-12/schema","$dynamicAnchor":"n",
		  "not":{"type":"number"},"properties":{"c":{"$dynamicRef":"#n"},"n":{"type":"string","maxLength":3}}}`,
		unit: 1, open: `{"n":"x","c":`, close: `}`, good: `{"n":"y"}`, invalid: `{"n":"long"}`,
	},
	{
		// uniqueItems over the type that holds it, two elements a level: the
		// check at every level compares elements whose subtrees are the rest of
		// the document. Each element was marshalled to be compared, subtree and
		// all, at every level; now each element's identity is computed once, by
		// the check at the top, and read back below. See jsonValidation.
		name:   "unique elements",
		schema: `{"type":"object","properties":{"kids":{"type":"array","uniqueItems":true,"items":{"$ref":"#"}},"n":{"type":"string","maxLength":3}}}`,
		unit:   2, open: `{"n":"x","kids":[{"n":"z"},`, close: `]}`, good: `{"n":"y"}`, bad: `{"n":1}`, invalid: `{"n":"long"}`,
	},
	{
		// The same, judged by the runtime evaluator: unevaluatedItems beside an
		// anyOf is decided per document. The elements are read lazily from the
		// document, whose arrays' identities are kept on it. See
		// jsonLazy.jsonIdentity.
		name: "unique elements at run time",
		schema: `{"$ref":"#/$defs/N","$defs":{"N":{"type":"array","uniqueItems":true,
		  "anyOf":[{"items":{"$ref":"#/$defs/N"}}],"unevaluatedItems":false}}}`,
		unit: 1, open: `[[],`, close: `]`, good: `[[[]]]`, invalid: `[[],[]]`,
	},
	{
		// A tuple position: the array is held as decoded JSON, and Validate
		// decodes the position into its type. The array is a []any, which
		// encoding/json writes by itself.
		name:   "tuple",
		schema: `{"type":"array","prefixItems":[{"type":"string","maxLength":3},{"$ref":"#"}]}`,
		unit:   1, open: `["x",`, close: `]`, good: `["y"]`, invalid: `["long"]`,
		nativeMarshal: true,
	},
}

func TestDecodeValidateMarshalCostIsLinearInTheDocument(t *testing.T) {
	if testing.Short() {
		t.Skip("generates, compiles and measures a package per shape")
	}
	// Not parallel: it times what it measures. The time is the CPU time the
	// driver spends, not the wall clock, so the other test binaries go test runs
	// beside this one -- each package under tests/ is one -- slow it down
	// without moving the ratios it is judged on.
	bin := schemagenBinary(t)
	out := t.TempDir()
	var imports, entries strings.Builder
	for i, shape := range costShapes {
		pkg := fmt.Sprintf("s%d", i)
		schemaPath := filepath.Join(out, pkg+".json")
		writeCrossFile(t, schemaPath, shape.schema)
		args := append([]string{"generate", schemaPath, "-o", filepath.Join(out, pkg), "-p", pkg,
			"--root-name", pkg + ".json=Root"}, shape.flags...)
		runSchemagen(t, bin, args...)
		fmt.Fprintf(&imports, "\t%s \"ex.test/dc/%s\"\n", pkg, pkg)
		spec, err := json.Marshal(shape.driverSpec())
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&entries, "\t{spec: %s, mk: func() any { return new(%s.Root) }},\n", backquote(string(spec)), pkg)
	}
	if err := writeTestGoMod(out, "ex.test/dc"); err != nil {
		t.Fatal(err)
	}
	drv := filepath.Join(out, "driver")
	if err := os.MkdirAll(drv, 0o755); err != nil {
		t.Fatal(err)
	}
	main := strings.NewReplacer("@IMPORTS@", imports.String(), "@ENTRIES@", entries.String()).Replace(costComplexityDriver)
	if err := os.WriteFile(filepath.Join(drv, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	output, err := testgo.Command(ctx, out, "run", "-mod=mod", "./driver").CombinedOutput()
	if err != nil {
		t.Fatalf("driver: %v\n%s", err, output)
	}

	var results []costMeasurement
	for _, line := range strings.Split(programOutput(output), "\n") {
		var m costMeasurement
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("driver printed %q: %v", line, err)
		}
		results = append(results, m)
	}
	want := 0
	for _, shape := range costShapes {
		want += 3 // decoded, validated, marshalled
		if shape.bad != "" {
			want++
		}
		if shape.invalid != "" {
			want++
		}
	}
	if len(results) != want {
		t.Fatalf("%d measurements for %d shapes, want %d", len(results), len(costShapes), want)
	}
	native := map[string]bool{}
	for _, shape := range costShapes {
		native[shape.name] = shape.nativeMarshal
	}
	for _, m := range results {
		t.Logf("%-18s %-10s depths %v: allocated %v B, least CPU of five %v ns, live %v B, document %v B",
			m.Shape, m.Kind, m.Depths, m.Bytes, m.Nanos, m.Live, m.Sizes)
		if m.Kind == "marshalled" && native[m.Shape] {
			continue
		}
		checkCostGrowth(t, m)
	}
}

// costMeasurement is what the driver reports for one shape and one operation,
// at each depth it was built at.
type costMeasurement struct {
	Shape  string   `json:"shape"`
	Kind   string   `json:"kind"`
	Depths []int    `json:"depths"`
	Sizes  []int    `json:"sizes"`
	Bytes  []uint64 `json:"bytes"`
	Nanos  []int64  `json:"nanos"`
	Live   []int64  `json:"live"`
	Errors []string `json:"errors"`
}

type costDriverSpec struct {
	Name    string `json:"name"`
	Unit    int    `json:"unit"`
	Open    string `json:"open"`
	Close   string `json:"close"`
	Good    string `json:"good"`
	Bad     string `json:"bad"`
	Invalid string `json:"invalid"`
}

func (s costShape) driverSpec() costDriverSpec {
	return costDriverSpec{Name: s.name, Unit: s.unit, Open: s.open, Close: s.close, Good: s.good, Bad: s.bad, Invalid: s.invalid}
}

// checkCostGrowth holds one measurement to linear growth. The depths double
// three times, so linear growth is a factor of eight from the first to the
// last and quadratic growth a factor of sixty-four; the bounds sit well between.
func checkCostGrowth(t *testing.T, m costMeasurement) {
	t.Helper()
	label := m.Shape + ", " + m.Kind
	last := len(m.Depths) - 1
	for i, e := range m.Errors {
		wantErr := m.Kind == "refused" || m.Kind == "invalid"
		if (e != "") != wantErr {
			t.Errorf("%s at depth %d: error %q, want one: %v", label, m.Depths[i], e, wantErr)
		}
	}
	growth := float64(m.Depths[last]) / float64(m.Depths[0])
	byteRatio := float64(m.Bytes[last]) / float64(max(m.Bytes[0], 1))
	if byteRatio > 1.5*growth && m.Bytes[last] > 64<<10 {
		t.Errorf("%s: allocation grew %.1fx for %.0fx the depth (%v bytes at depths %v) -- linear is %.0fx, quadratic %.0fx",
			label, byteRatio, growth, m.Bytes, m.Depths, growth, growth*growth)
	}
	// Time is the least CPU time of several runs, and still allowed five times
	// the linear factor: it is the noisy signal, and exists to catch the scan
	// that allocates nothing.
	timeRatio := float64(m.Nanos[last]) / float64(max(m.Nanos[0], 1))
	if timeRatio > 5*growth && m.Nanos[last] > int64(50*time.Millisecond) {
		t.Errorf("%s: time grew %.1fx for %.0fx the depth (%v ns at depths %v)", label, timeRatio, growth, m.Nanos, m.Depths)
	}
	if m.Kind == "decoded" {
		// The live heap is read off the collector and moves by a few tens of
		// kilobytes whatever the program does, which at the shallowest depth
		// is most of the reading; the slack below is for that.
		if limit := int64(1.5*growth*float64(max(m.Live[0], 0))) + 256<<10; m.Live[last] > limit {
			t.Errorf("%s: live heap grew from %d to %d bytes for %.0fx the depth (%v bytes at depths %v)",
				label, m.Live[0], m.Live[last], growth, m.Live, m.Depths)
		}
		// A decoded value holds Go structs, maps and pointers for what the
		// document spells in a few bytes -- up to three maps per level, where
		// the overlapping union writes `{"c":` and each level is a union
		// recording its keys and raw members over an object recording its own
		// keys: some 170 times the document -- so it is larger than the
		// document; what it may not be is a copy of the document per level,
		// which is thousands of times the document at these depths.
		if limit := int64(256 * m.Sizes[last]); m.Live[last] > limit {
			t.Errorf("%s: a %d-byte document left %d bytes of live heap, more than 256 times its size", label, m.Sizes[last], m.Live[last])
		}
	}
}

// costComplexityDriver builds each shape's documents, decodes, validates and
// marshals them, and prints one JSON line per shape and operation.
const costComplexityDriver = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"

@IMPORTS@
)

type spec struct {
	Name    string ` + "`json:\"name\"`" + `
	Unit    int    ` + "`json:\"unit\"`" + `
	Open    string ` + "`json:\"open\"`" + `
	Close   string ` + "`json:\"close\"`" + `
	Good    string ` + "`json:\"good\"`" + `
	Bad     string ` + "`json:\"bad\"`" + `
	Invalid string ` + "`json:\"invalid\"`" + `
}

type entry struct {
	spec string
	mk   func() any
}

var entries = []entry{
@ENTRIES@
}

type result struct {
	Shape  string   ` + "`json:\"shape\"`" + `
	Kind   string   ` + "`json:\"kind\"`" + `
	Depths []int    ` + "`json:\"depths\"`" + `
	Sizes  []int    ` + "`json:\"sizes\"`" + `
	Bytes  []uint64 ` + "`json:\"bytes\"`" + `
	Nanos  []int64  ` + "`json:\"nanos\"`" + `
	Live   []int64  ` + "`json:\"live\"`" + `
	Errors []string ` + "`json:\"errors\"`" + `
}

// cpuNanos is the CPU time the process has used, user and system. Unlike the
// wall clock it does not count the time the process waited for a CPU, which is
// what a machine busy with other test binaries adds.
func cpuNanos() int64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		panic(err)
	}
	return ru.Utime.Nano() + ru.Stime.Nano()
}

// measure runs op once for the bytes it allocates and the live heap what it
// returns holds, and five more times for the least CPU time.
func measure(op func() (any, error)) (alloc uint64, nanos int64, live int64, errText string) {
	var ms runtime.MemStats
	// Twice: an object a sync.Pool held survives one collection in the pool's
	// victim cache, and would be counted as live here and freed below.
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&ms)
	base := ms.HeapAlloc
	before := ms.TotalAlloc
	v, err := op()
	runtime.ReadMemStats(&ms)
	alloc = ms.TotalAlloc - before
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&ms)
	live = int64(ms.HeapAlloc) - int64(base)
	runtime.KeepAlive(v)
	if err != nil {
		errText = err.Error()
	}
	nanos = int64(^uint64(0) >> 1)
	for i := 0; i < 5; i++ {
		t0 := cpuNanos()
		_, _ = op()
		if d := cpuNanos() - t0; d < nanos {
			nanos = d
		}
	}
	return
}

// decode is the type's own UnmarshalJSON, rather than json.Unmarshal: the
// latter checks the whole document before it calls the type, and that check is
// encoding/json's -- on the encoding/json Go 1.27 ships it grows faster than the
// document with the depth of the nesting, which would be measured here as this
// decode's cost. MarshalJSON is called directly for the same reason.
func decode(mk func() any, doc []byte) (any, error) {
	v := mk()
	return v, v.(json.Unmarshaler).UnmarshalJSON(doc)
}

func marshal(v any) ([]byte, error) {
	if m, ok := v.(json.Marshaler); ok {
		return m.MarshalJSON()
	}
	return json.Marshal(v)
}

func main() {
	for _, e := range entries {
		var s spec
		if err := json.Unmarshal([]byte(e.spec), &s); err != nil {
			panic(err)
		}
		rs := map[string]*result{}
		kinds := []string{"decoded", "validated", "marshalled"}
		if s.Bad != "" {
			kinds = append(kinds, "refused")
		}
		if s.Invalid != "" {
			kinds = append(kinds, "invalid")
		}
		for _, k := range kinds {
			rs[k] = &result{Shape: s.Name, Kind: k}
		}
		record := func(kind string, depth, size int, alloc uint64, nanos, live int64, errText string) {
			r := rs[kind]
			r.Depths = append(r.Depths, depth)
			r.Sizes = append(r.Sizes, size)
			r.Bytes = append(r.Bytes, alloc)
			r.Nanos = append(r.Nanos, nanos)
			r.Live = append(r.Live, live)
			r.Errors = append(r.Errors, errText)
		}
		build := func(depth int, leaf string) []byte {
			doc := []byte(strings.Repeat(s.Open, depth) + leaf + strings.Repeat(s.Close, depth))
			if !json.Valid(doc) {
				fmt.Fprintf(os.Stderr, "%s: built an invalid document\n", s.Name)
				os.Exit(1)
			}
			return doc
		}
		for _, depth := range []int{1000, 2000, 4000, 8000} {
			depth /= s.Unit
			doc := build(depth, s.Good)
			alloc, nanos, live, errText := measure(func() (any, error) { return decode(e.mk, doc) })
			record("decoded", depth, len(doc), alloc, nanos, live, errText)
			v, err := decode(e.mk, doc)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: the document that decodes does not: %v\n", s.Name, err)
				os.Exit(1)
			}
			alloc, nanos, _, errText = measure(func() (any, error) { return nil, v.(interface{ Validate() error }).Validate() })
			record("validated", depth, len(doc), alloc, nanos, 0, errText)
			alloc, nanos, _, errText = measure(func() (any, error) { return marshal(v) })
			record("marshalled", depth, len(doc), alloc, nanos, 0, errText)
			if s.Bad != "" {
				doc := build(depth, s.Bad)
				alloc, nanos, live, errText := measure(func() (any, error) { return decode(e.mk, doc) })
				record("refused", depth, len(doc), alloc, nanos, live, errText)
			}
			if s.Invalid != "" {
				doc := build(depth, s.Invalid)
				v, err := decode(e.mk, doc)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s: the document Validate should refuse does not decode: %v\n", s.Name, err)
					os.Exit(1)
				}
				alloc, nanos, _, errText := measure(func() (any, error) { return nil, v.(interface{ Validate() error }).Validate() })
				record("invalid", depth, len(doc), alloc, nanos, 0, errText)
			}
		}
		for _, k := range kinds {
			out, _ := json.Marshal(rs[k])
			fmt.Println(string(out))
		}
	}
}
`
