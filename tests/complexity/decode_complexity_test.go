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

// Decoding a document costs in proportion to the document, however deeply the
// generated types nest.
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
// The test holds every recursive shape the generator decodes through -- a
// member, an element, a map value, a union branch, a chain of named types, an
// alias over a slice, a conditional that keeps members for Validate, a wrapper
// that keeps raw bytes -- to growing linearly, both for a document that decodes
// and for one refused at its deepest point. Allocations are the signal: they are
// deterministic, so the bounds can be tight enough to tell eight times from
// sixty-four. Wall time is checked too, loosely enough to survive a loaded
// machine. The live heap a decoded value holds is held to a multiple of the
// document's own size, which is what copying each level's raw members per level
// broke: a 24 KB document left 70 MB of live heap behind.

// decodeShape is one recursive schema and the documents it is measured on.
type decodeShape struct {
	name   string
	schema string
	flags  []string
	// unit is the JSON nesting one level of the shape adds: encoding/json
	// refuses a document nested more than 10,000 deep, so a shape nesting two
	// containers per level is measured at half the depth.
	unit int
	// open and close build a document of depth levels: open repeated, then
	// the leaf, then close repeated. good is the leaf of a document that
	// decodes, bad the leaf of one refused at the bottom.
	open, close, good, bad string
}

var decodeShapes = []decodeShape{
	{
		name:   "member",
		schema: `{"type":"object","properties":{"c":{"$ref":"#"},"n":{"type":"string"}}}`,
		unit:   1, open: `{"n":"x","c":`, close: `}`, good: `{"n":"y"}`, bad: `{"n":1}`,
	},
	{
		// An if/then beside the members keeps them as raw JSON for Validate
		// (_jsonRawProps); each level used to keep a copy of its whole subtree.
		name: "conditional",
		schema: `{"type":"object","properties":{"c":{"$ref":"#"},"n":{"type":"string"}},
		  "if":{"properties":{"n":{"const":"a"}},"required":["n"]},"then":{"required":["c"]}}`,
		unit: 1, open: `{"n":"a","c":`, close: `}`, good: `{"n":"y"}`, bad: `{"n":1}`,
	},
	{
		name:   "element",
		schema: `{"type":"object","properties":{"kids":{"type":"array","items":{"$ref":"#"}},"n":{"type":"string"}}}`,
		unit:   2, open: `{"n":"x","kids":[`, close: `]}`, good: `{"n":"y"}`, bad: `{"n":1}`,
	},
	{
		name:   "map value",
		schema: `{"type":"object","additionalProperties":{"$ref":"#"}}`,
		unit:   1, open: `{"k":`, close: `}`, good: `{}`, bad: `1`,
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
		schema: `{"$ref":"#/$defs/U","$defs":{"U":{"oneOf":[{"$ref":"#/$defs/O"},{"type":"object","properties":{"z":{"type":"string"}}}]},
		  "O":{"type":"object","properties":{"c":{"$ref":"#/$defs/U"}},"required":["c"]}}}`,
		// No document of this shape is refused at the bottom: wherever the
		// branch that recurses fails, the one that holds the member as raw
		// JSON takes the level instead.
		unit: 1, open: `{"c":`, close: `}`, good: `{"c":"x"}`, bad: ``,
	},
	{
		// The member reaches the next level through two more names.
		name: "ref chain",
		schema: `{"$ref":"#/$defs/N","$defs":{"N":{"type":"object","properties":{"c":{"$ref":"#/$defs/A"},"n":{"type":"string"}}},
		  "A":{"$ref":"#/$defs/B"},"B":{"$ref":"#/$defs/N"}}}`,
		unit: 1, open: `{"n":"x","c":`, close: `}`, good: `{"n":"y"}`, bad: `{"n":1}`,
	},
	{
		// An alias over a slice of the type that holds it.
		name: "slice alias",
		schema: `{"$ref":"#/$defs/N","$defs":{"N":{"type":"object","properties":{"k":{"$ref":"#/$defs/L"},"n":{"type":"string"}}},
		  "L":{"type":"array","items":{"$ref":"#/$defs/N"}}}}`,
		unit: 2, open: `{"n":"x","k":[`, close: `]}`, good: `{"n":"y"}`, bad: `{"n":1}`,
	},
	{
		// A member held as raw JSON at every level, beside the one that recurses.
		name:   "raw wrapper",
		schema: `{"type":"object","properties":{"c":{"$ref":"#"},"d":{"oneOf":[{"minimum":1},{"maximum":0}]},"n":{"type":"string"}}}`,
		unit:   1, open: `{"d":0,"n":"x","c":`, close: `}`, good: `{"n":"y"}`, bad: `{"n":1}`,
	},
	{
		// --strict-read-write walks the rules below a struct's own members over
		// the document at every level.
		name: "strict read-write",
		schema: `{"type":"object","properties":{"c":{"$ref":"#"},"n":{"type":"string"},
		  "t":{"type":"array","prefixItems":[{"type":"object","properties":{"ro":{"type":"string","readOnly":true}}}]}}}`,
		flags: []string{"--strict-read-write"},
		unit:  1, open: `{"n":"x","t":[{}],"c":`, close: `}`, good: `{"n":"y"}`, bad: `{"n":1}`,
	},
}

func TestDecodeCostIsLinearInTheDocument(t *testing.T) {
	if testing.Short() {
		t.Skip("generates, compiles and measures nine packages")
	}
	bin := schemagenBinary(t)
	out := t.TempDir()
	var imports, entries strings.Builder
	for i, shape := range decodeShapes {
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
	main := strings.NewReplacer("@IMPORTS@", imports.String(), "@ENTRIES@", entries.String()).Replace(decodeComplexityDriver)
	if err := os.WriteFile(filepath.Join(drv, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	output, err := testgo.Command(ctx, out, "run", "-mod=mod", "./driver").CombinedOutput()
	if err != nil {
		t.Fatalf("driver: %v\n%s", err, output)
	}

	var results []decodeMeasurement
	for _, line := range strings.Split(programOutput(output), "\n") {
		var m decodeMeasurement
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("driver printed %q: %v", line, err)
		}
		results = append(results, m)
	}
	want := 0
	for _, shape := range decodeShapes {
		want++
		if shape.bad != "" {
			want++
		}
	}
	if len(results) != want {
		t.Fatalf("%d measurements for %d shapes, want %d", len(results), len(decodeShapes), want)
	}
	for _, m := range results {
		t.Logf("%-18s %-8s depths %v: allocated %v B, best of five %v ns, live %v B, document %v B",
			m.Shape, m.Kind, m.Depths, m.Bytes, m.Nanos, m.Live, m.Sizes)
		checkDecodeGrowth(t, m)
	}
}

// decodeMeasurement is what the driver reports for one shape and one kind of
// document, at each depth it was built at.
type decodeMeasurement struct {
	Shape  string   `json:"shape"`
	Kind   string   `json:"kind"`
	Depths []int    `json:"depths"`
	Sizes  []int    `json:"sizes"`
	Bytes  []uint64 `json:"bytes"`
	Nanos  []int64  `json:"nanos"`
	Live   []int64  `json:"live"`
	Errors []string `json:"errors"`
}

type decodeDriverSpec struct {
	Name  string `json:"name"`
	Unit  int    `json:"unit"`
	Open  string `json:"open"`
	Close string `json:"close"`
	Good  string `json:"good"`
	Bad   string `json:"bad"`
}

func (s decodeShape) driverSpec() decodeDriverSpec {
	return decodeDriverSpec{Name: s.name, Unit: s.unit, Open: s.open, Close: s.close, Good: s.good, Bad: s.bad}
}

// checkDecodeGrowth holds one measurement to linear growth. The depths double
// three times, so linear growth is a factor of eight from the first to the
// last and quadratic growth a factor of sixty-four; the bounds sit well between.
func checkDecodeGrowth(t *testing.T, m decodeMeasurement) {
	t.Helper()
	label := m.Shape + ", " + m.Kind
	last := len(m.Depths) - 1
	for i, e := range m.Errors {
		wantErr := m.Kind == "refused"
		if (e != "") != wantErr {
			t.Errorf("%s at depth %d: decode error %q, want one: %v", label, m.Depths[i], e, wantErr)
		}
	}
	growth := float64(m.Depths[last]) / float64(m.Depths[0])
	byteRatio := float64(m.Bytes[last]) / float64(m.Bytes[0])
	if byteRatio > 1.5*growth {
		t.Errorf("%s: allocation grew %.1fx for %.0fx the depth (%v bytes at depths %v) -- linear is %.0fx, quadratic %.0fx",
			label, byteRatio, growth, m.Bytes, m.Depths, growth, growth*growth)
	}
	// Wall time is measured as the best of several runs, and still allowed five
	// times the linear factor: it is the noisy signal, and exists to catch the
	// scan that allocates nothing.
	timeRatio := float64(m.Nanos[last]) / float64(max(m.Nanos[0], 1))
	if timeRatio > 5*growth && m.Nanos[last] > int64(50*time.Millisecond) {
		t.Errorf("%s: decode time grew %.1fx for %.0fx the depth (%v ns at depths %v)", label, timeRatio, growth, m.Nanos, m.Depths)
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
		// document spells in a few bytes -- a map per level, where the
		// document writes `{"k":` -- so it is larger than the document; what it
		// may not be is a copy of the document per level, which is thousands
		// of times the document at these depths.
		if limit := int64(128 * m.Sizes[last]); m.Live[last] > limit {
			t.Errorf("%s: a %d-byte document left %d bytes of live heap, more than 128 times its size", label, m.Sizes[last], m.Live[last])
		}
	}
}

// decodeComplexityDriver builds each shape's documents, decodes them, and
// prints one JSON line per shape and kind.
const decodeComplexityDriver = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

@IMPORTS@
)

type spec struct {
	Name  string ` + "`json:\"name\"`" + `
	Unit  int    ` + "`json:\"unit\"`" + `
	Open  string ` + "`json:\"open\"`" + `
	Close string ` + "`json:\"close\"`" + `
	Good  string ` + "`json:\"good\"`" + `
	Bad   string ` + "`json:\"bad\"`" + `
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

// measure decodes doc into a fresh value: the bytes allocated, the best time
// of several runs, and the live heap the value holds once decoded.
func measure(mk func() any, doc []byte) (alloc uint64, nanos int64, live int64, errText string) {
	var ms runtime.MemStats
	// Twice: an object a sync.Pool held survives one collection in the pool's
	// victim cache, and would be counted as live here and freed below.
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&ms)
	base := ms.HeapAlloc
	before := ms.TotalAlloc
	v := mk()
	// The type's own UnmarshalJSON, rather than json.Unmarshal: the latter
	// checks the whole document before it calls the type, and that check is
	// encoding/json's -- on the encoding/json Go 1.27 ships it grows faster than
	// the document with the depth of the nesting, which would be measured here
	// as this decode's cost.
	err := v.(json.Unmarshaler).UnmarshalJSON(doc)
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
		w := mk().(json.Unmarshaler)
		t0 := time.Now()
		_ = w.UnmarshalJSON(doc)
		if d := time.Since(t0).Nanoseconds(); d < nanos {
			nanos = d
		}
	}
	return
}

func main() {
	for _, e := range entries {
		var s spec
		if err := json.Unmarshal([]byte(e.spec), &s); err != nil {
			panic(err)
		}
		for _, kind := range []string{"decoded", "refused"} {
			leaf := s.Good
			if kind == "refused" {
				if s.Bad == "" {
					continue
				}
				leaf = s.Bad
			}
			r := result{Shape: s.Name, Kind: kind}
			for _, depth := range []int{1000, 2000, 4000, 8000} {
				depth /= s.Unit
				doc := []byte(strings.Repeat(s.Open, depth) + leaf + strings.Repeat(s.Close, depth))
				if !json.Valid(doc) {
					fmt.Fprintf(os.Stderr, "%s: built an invalid document\n", s.Name)
					os.Exit(1)
				}
				alloc, nanos, live, errText := measure(e.mk, doc)
				r.Depths = append(r.Depths, depth)
				r.Sizes = append(r.Sizes, len(doc))
				r.Bytes = append(r.Bytes, alloc)
				r.Nanos = append(r.Nanos, nanos)
				r.Live = append(r.Live, live)
				r.Errors = append(r.Errors, errText)
			}
			out, _ := json.Marshal(r)
			fmt.Println(string(out))
		}
	}
}
`
