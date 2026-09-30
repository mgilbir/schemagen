package numbers

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
	"github.com/mgilbir/schemagen/tests/internal/testsupport"
)

// This file is the number verdict grid: every numeric keyword, at every
// position the generator writes a check in, under every configuration that
// changes how a number is held, put to the instances that tell an exact
// reading from an approximate one -- and every verdict held to an independent
// oracle.
//
// JSON Schema defines its numeric keywords over numbers as mathematical
// values. multipleOf is "the instance divided by the divisor is an integer",
// minimum is an ordering on the reals, "integer" is a zero fractional part
// (from draft 6; draft 4 reads the token), and const, enum and uniqueItems
// compare numbers by value. The oracle is an implementation that does exactly
// that: santhosh-tekuri/jsonschema v6.0.2, which decodes with UseNumber and
// does its arithmetic in big.Rat, run out of tree (it is not in go.mod) and
// cross-checked through Bowtie against python-jsonschema and ajv, both of
// which work in binary floating point and so disagree with it -- and with the
// specification -- on exactly the instances below that are about precision.
// The verdicts are frozen in testdata/number_oracle/verdicts.json, so this test
// needs neither Docker nor the network. See scripts/number-oracle/ for how the
// file is made.
//
// What each cell expects depends on how the position holds its number under
// the configuration, and that is spelled out rather than inferred: see
// gridHolding. A position that holds a "number" as float64 holds the float64 a
// document's literal rounds to -- decided at decode, before any keyword runs --
// and the verdict there is the oracle's verdict on that rounded document. That
// is the representation --exact-numbers exists to change, and the grid checks
// that the flag changes it and changes nothing else. Every other cell expects
// the oracle's verdict on the document as written, in every configuration.

// gridKeyword is one numeric schema put at every position.
type gridKeyword struct {
	Name   string
	Draft  string // "2020-12" or "4"
	Schema string // the keyword's schema, as JSON
	// Kind is the JSON type the schema declares -- "integer", "number" -- or
	// "untyped" where it declares none, "union" for a type list, and "array"
	// for the uniqueItems schemas, which take array instances.
	Kind string
}

// gridPosition wraps a keyword schema into the document shape that puts it at
// one position.
type gridPosition struct {
	Name string
	// Wrap is the position schema with the keyword inside it; ok is false for
	// a keyword the position does not take.
	Wrap func(k gridKeyword) (string, bool)
	// Doc is the instance the position schema judges, with x at the keyword's
	// place.
	Doc func(x string) string
	// Drafts lists the drafts the position exists in.
	Drafts []string
}

var gridKeywords = []gridKeyword{
	{"int_max", "2020-12", `{"type":"integer","maximum":9007199254740992}`, "integer"},
	{"int_min", "2020-12", `{"type":"integer","minimum":-9007199254740992}`, "integer"},
	{"int_xmax", "2020-12", `{"type":"integer","exclusiveMaximum":9007199254740993}`, "integer"},
	{"int_frac_min", "2020-12", `{"type":"integer","minimum":1.5}`, "integer"},
	{"int_mult7", "2020-12", `{"type":"integer","multipleOf":7}`, "integer"},
	{"int_mult2_5", "2020-12", `{"type":"integer","multipleOf":2.5}`, "integer"},
	{"int_bigmax", "2020-12", `{"type":"integer","maximum":1e99}`, "integer"},
	{"int", "2020-12", `{"type":"integer"}`, "integer"},
	{"int_const", "2020-12", `{"type":"integer","const":9007199254740993}`, "integer"},
	{"num_max", "2020-12", `{"type":"number","maximum":9007199254740992}`, "number"},
	{"num_xmin", "2020-12", `{"type":"number","exclusiveMinimum":0.1}`, "number"},
	{"num_mult1", "2020-12", `{"type":"number","multipleOf":1}`, "number"},
	{"num_mult01", "2020-12", `{"type":"number","multipleOf":0.1}`, "number"},
	{"num_mult001", "2020-12", `{"type":"number","multipleOf":0.01}`, "number"},
	{"num_const", "2020-12", `{"type":"number","const":1}`, "number"},
	{"num_enum", "2020-12", `{"type":"number","enum":[1,0.3,9007199254740993]}`, "number"},
	{"num_bigmax", "2020-12", `{"type":"number","maximum":1e99}`, "number"},
	{"untyped_max", "2020-12", `{"maximum":9007199254740992}`, "untyped"},
	{"untyped_mult", "2020-12", `{"multipleOf":0.1}`, "untyped"},
	{"untyped_const", "2020-12", `{"const":1}`, "untyped"},
	{"union_int", "2020-12", `{"type":["integer","string"]}`, "union"},
	{"uniq_num", "2020-12", `{"type":"array","items":{"type":"number"},"uniqueItems":true}`, "array"},
	{"uniq_int", "2020-12", `{"type":"array","items":{"type":"integer"},"uniqueItems":true}`, "array"},
	{"uniq_untyped", "2020-12", `{"type":"array","items":{},"uniqueItems":true}`, "array"},
	{"d4_int", "4", `{"type":"integer"}`, "integer"},
	{"d4_xmax", "4", `{"type":"number","maximum":10,"exclusiveMaximum":true}`, "number"},
	{"d4_xmin", "4", `{"type":"integer","minimum":5,"exclusiveMinimum":true}`, "integer"},
	{"d4_mult01", "4", `{"type":"number","multipleOf":0.1}`, "number"},
}

// gridScalars are the number instances: the float64 boundary, the spellings of
// one integer, the decimals binary floating point cannot hold, the bounds past
// int64 and past float64, the two zeros, and a string, which every numeric
// keyword is satisfied by.
var gridScalars = []string{
	"9007199254740992", "9007199254740993", "-9007199254740993",
	"1", "1.0", "1e0", "0.3", "0.30", "1.0000000001", "1e-10",
	"0", "-0", "-0.0", "0.1", "0.10000000000000001", "7", "8", "5", "6",
	"10", "10.000000000000001", "2.5", "7e98", "1e99",
	"1000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001",
	"1e100", "12345678901234567891", "12345678901234567891.5", "9223372036854775808",
	"1e400", "-1e400", "1e1000", "1e-1000", `"7"`,
}

// gridArrays are the uniqueItems instances.
var gridArrays = []string{
	"[1,1.0]", "[100,1e2]", "[0,-0]", "[1,2]", "[0.3,0.30]",
	"[9007199254740992,9007199254740993]", "[1e400,1e400]", `[1,"1"]`,
}

// withoutType is the keyword schema with its "type" removed, for the union
// branches that sit inside a schema declaring the type themselves.
func withoutType(k gridKeyword) (string, string) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(k.Schema), &m); err != nil {
		panic(err)
	}
	typ := ""
	if raw, ok := m["type"]; ok {
		typ = string(raw)
		delete(m, "type")
	}
	b, _ := json.Marshal(m)
	return string(b), typ
}

var gridPositions = []gridPosition{
	{Name: "root", Drafts: []string{"2020-12", "4"},
		Wrap: func(k gridKeyword) (string, bool) { return k.Schema, true },
		Doc:  func(x string) string { return x }},
	{Name: "prop", Drafts: []string{"2020-12", "4"},
		Wrap: func(k gridKeyword) (string, bool) {
			return `{"type":"object","properties":{"v":` + k.Schema + `}}`, true
		},
		Doc: func(x string) string { return `{"v":` + x + `}` }},
	{Name: "ref", Drafts: []string{"2020-12", "4"},
		Wrap: func(k gridKeyword) (string, bool) {
			return `{"type":"object","properties":{"v":{"$ref":"#/definitions/d"}},"definitions":{"d":` + k.Schema + `}}`, true
		},
		Doc: func(x string) string { return `{"v":` + x + `}` }},
	{Name: "items", Drafts: []string{"2020-12", "4"},
		Wrap: func(k gridKeyword) (string, bool) {
			return `{"type":"array","items":` + k.Schema + `}`, true
		},
		Doc: func(x string) string { return `[` + x + `]` }},
	// The anyOf/oneOf branches of a scalar alias. A branch carrying "const" or
	// "enum" is not taken: aliasVariantKeywords does not list either, so such a
	// group is not compiled at all and the alias accepts every value -- a check
	// that is dropped, not one that reads a number wrongly, and one the work that
	// routes unexpressed keywords to the runtime evaluator is to pick up. Put
	// here, it would measure that and nothing about numbers.
	{Name: "anyof", Drafts: []string{"2020-12", "4"},
		Wrap: func(k gridKeyword) (string, bool) {
			rest, typ := withoutType(k)
			if typ == "" || k.Kind == "array" || k.Kind == "union" || gridStatesConstOrEnum(k) {
				return "", false
			}
			return `{"type":` + typ + `,"anyOf":[` + rest + `,{"type":"string"}]}`, true
		},
		Doc: func(x string) string { return x }},
	{Name: "oneof", Drafts: []string{"2020-12", "4"},
		Wrap: func(k gridKeyword) (string, bool) {
			rest, typ := withoutType(k)
			if typ == "" || k.Kind == "array" || k.Kind == "union" || gridStatesConstOrEnum(k) {
				return "", false
			}
			return `{"type":` + typ + `,"oneOf":[` + rest + `,{"type":"string"}]}`, true
		},
		Doc: func(x string) string { return x }},
	{Name: "contains", Drafts: []string{"2020-12"},
		Wrap: func(k gridKeyword) (string, bool) {
			return `{"type":"array","items":{},"contains":` + k.Schema + `}`, true
		},
		Doc: func(x string) string { return `[` + x + `]` }},
	{Name: "pattern", Drafts: []string{"2020-12", "4"},
		Wrap: func(k gridKeyword) (string, bool) {
			return `{"type":"object","patternProperties":{"^v$":` + k.Schema + `}}`, true
		},
		Doc: func(x string) string { return `{"v":` + x + `}` }},
	{Name: "ifthen", Drafts: []string{"2020-12"},
		Wrap: func(k gridKeyword) (string, bool) {
			return `{"type":"object","properties":{"v":{"if":{"type":["number","string","array"]},"then":` + k.Schema + `}}}`, true
		},
		Doc: func(x string) string { return `{"v":` + x + `}` }},
	{Name: "uneval", Drafts: []string{"2020-12"},
		Wrap: func(k gridKeyword) (string, bool) {
			return `{"type":"object","properties":{"a":{}},"unevaluatedProperties":` + k.Schema + `}`, true
		},
		Doc: func(x string) string { return `{"v":` + x + `}` }},
	{Name: "nonobject", Drafts: []string{"2020-12", "4"},
		Wrap: func(k gridKeyword) (string, bool) {
			rest, _ := withoutType(k)
			if k.Kind == "array" || k.Kind == "union" || rest == "{}" {
				return "", false
			}
			return `{"properties":{"a":{"type":"string"}},` + strings.TrimPrefix(rest, "{"), true
		},
		Doc: func(x string) string { return x }},
	{Name: "notnot", Drafts: []string{"2020-12", "4"},
		Wrap: func(k gridKeyword) (string, bool) {
			return `{"not":{"not":` + k.Schema + `}}`, true
		},
		Doc: func(x string) string { return x }},
	// An array whose unevaluatedItems depends on which anyOf branch matched,
	// which is decided by the runtime evaluator: the keyword is a node literal
	// judged against a value decoded keeping its literals. notnot above is the
	// evaluator too, for a scalar.
	//
	// The object-shaped counterpart -- {"properties":{"v":true},"anyOf":
	// [{"properties":{"v":K}}],"unevaluatedProperties":false} -- is left out on
	// purpose: the static object-level anyOf reduction reads only a branch's
	// required keys, types and enum/const values, so every numeric keyword
	// inside it is dropped and the grid would measure that rather than how a
	// number is read. That is a dropped check, and it belongs to the work that
	// routes such shapes to the evaluator.
	{Name: "unevalitems", Drafts: []string{"2020-12"},
		Wrap: func(k gridKeyword) (string, bool) {
			return `{"type":"array","anyOf":[{"items":` + k.Schema + `}],"unevaluatedItems":false}`, true
		},
		Doc: func(x string) string { return `[` + x + `]` }},
}

// gridStatesConstOrEnum reports a keyword schema stating "const" or "enum".
func gridStatesConstOrEnum(k gridKeyword) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(k.Schema), &m); err != nil {
		panic(err)
	}
	_, c := m["const"]
	_, e := m["enum"]
	return c || e
}

// gridConfig is one generator configuration the grid runs under.
type gridConfig struct {
	Name string
	Cfg  func() generator.Config
}

var gridConfigs = []gridConfig{
	{"default", func() generator.Config { return generator.Config{} }},
	{"exact", func() generator.Config { return generator.Config{ExactNumbers: true} }},
	{"bigint", func() generator.Config { return generator.Config{BigIntSupport: true} }},
	{"raw", func() generator.Config { return generator.Config{RawUntyped: true} }},
	{"runtime", func() generator.Config { return generator.Config{Validation: generator.ValidationModeRuntime} }},
	{"all", func() generator.Config {
		return generator.Config{ExactNumbers: true, BigIntSupport: true, RawUntyped: true}
	}},
}

func draftURI(d string) string {
	if d == "4" {
		return "http://json-schema.org/draft-04/schema#"
	}
	return "https://json-schema.org/draft/2020-12/schema"
}

// gridCell is one schema at one position, and the instances it is put to.
type gridCell struct {
	Keyword  gridKeyword
	Position gridPosition
	Schema   string // standalone, with $schema
	Docs     []string
	Values   []string // the instance at the keyword's place, for each doc
}

func gridCells() []gridCell {
	var cells []gridCell
	for _, k := range gridKeywords {
		for _, p := range gridPositions {
			if !contains(p.Drafts, k.Draft) {
				continue
			}
			wrapped, ok := p.Wrap(k)
			if !ok {
				continue
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal([]byte(wrapped), &m); err != nil {
				panic(fmt.Sprintf("%s/%s: %v: %s", k.Name, p.Name, err, wrapped))
			}
			m["$schema"], _ = json.Marshal(draftURI(k.Draft))
			standalone, _ := json.Marshal(m)
			cell := gridCell{Keyword: k, Position: p, Schema: string(standalone)}
			values := gridScalars
			if k.Kind == "array" {
				values = gridArrays
			}
			for _, x := range values {
				cell.Docs = append(cell.Docs, p.Doc(x))
				cell.Values = append(cell.Values, x)
			}
			cells = append(cells, cell)
		}
	}
	return cells
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// roundedTo64 is the document a float64 position holds: every number in it
// replaced by the shortest spelling of the float64 it rounds to. ok is false
// when a number is past float64's range, which such a position refuses at
// decode.
func roundedTo64(doc string) (string, bool) {
	var v any
	dec := json.NewDecoder(strings.NewReader(doc))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		panic(err)
	}
	ok := true
	var walk func(any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case json.Number:
			f, err := strconv.ParseFloat(string(t), 64)
			if err != nil {
				ok = false
				return t
			}
			return json.Number(strconv.FormatFloat(f, 'g', -1, 64))
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
		case map[string]any:
			// maporder: rewrites each member in place under its own key.
			for k := range t {
				t[k] = walk(t[k])
			}
		}
		return v
	}
	v = walk(v)
	b, _ := json.Marshal(v)
	return string(b), ok
}

// gridHolding is how a position holds the keyword's number under a
// configuration: "exact" where the literal the document wrote is what the
// check reads, "float" where the position is a float64 and holds what the
// literal rounds to, and "int64" where it is an int64 and refuses at decode an
// integer int64 does not hold.
//
// It is written out rather than read from the generated code, because the
// point of the grid is to hold the generated code to it. Each "float" and
// "int64" entry is a Go type the documentation says the position has; nothing
// here excuses a check. Where the literal survives to the check -- a raw
// member, a value decoded with UseNumber, a json.Number, a big-int wrapper, an
// inferred wrapper that keeps its bytes -- the answer is "exact" whatever the
// flag.
func gridHolding(k gridKeyword, pos string, cfg string) string {
	raw := cfg == "raw" || cfg == "all"
	switch pos {
	case "contains":
		// The element is untyped -- "items":{} -- so it is held as `any`, a
		// float64, unless --raw-untyped keeps its bytes. Every contains
		// schema here reads that element as it is held: one with numeric
		// keywords through the element's kind and text, and one with a type
		// of its own -- the array schemas -- through the evaluator node
		// compiled for it (ContainsDef.Node), over a tree view of the element.
		// It is not decoded into the sub-schema's Go type, so that type's
		// holding does not enter.
		if raw {
			return "exact"
		}
		return "float"
	case "root", "prop", "ref", "items", "anyof", "oneof":
		return gridTypedHolding(k, cfg)
	case "pattern":
		// A patternProperties member with only scalar keywords is judged on
		// its raw bytes; one whose sub-schema has a type of its own is decoded
		// into that type.
		if k.Kind == "array" {
			return gridTypedHolding(k, cfg)
		}
	}
	// Everything else reads the literal: a non-object document and an
	// unevaluated property are judged on their raw bytes; an if/then branch, a
	// not, and the runtime evaluator on a value decoded keeping every number as
	// its json.Number; and a position the schema gives no type is an inferred
	// wrapper that keeps its bytes.
	return "exact"
}

// gridTypedHolding is how the Go type a keyword's schema is given holds its
// number under a configuration.
func gridTypedHolding(k gridKeyword, cfg string) string {
	exact := cfg == "exact" || cfg == "all"
	bigint := cfg == "bigint" || cfg == "all"
	raw := cfg == "raw" || cfg == "all"
	switch k.Kind {
	case "number":
		// An enum whose members float64 cannot hold apart holds its literal
		// under every configuration; see the enum base in generateEnumDef.
		if exact || k.Name == "num_enum" {
			return "exact"
		}
		return "float"
	case "integer":
		// An int64, or under --big-int the wrapper that carries the rest.
		if bigint {
			return "exact"
		}
		return "int64"
	case "array":
		switch k.Name {
		case "uniq_num":
			if !exact {
				return "float"
			}
		case "uniq_int":
			if !bigint {
				return "int64"
			}
		case "uniq_untyped":
			if !raw {
				return "float"
			}
		}
	}
	return "exact"
}

// gridOracleEntry is one frozen verdict.
type gridOracleEntry struct {
	Schema string          `json:"schema"`
	Doc    string          `json:"instance"`
	Valid  bool            `json:"valid"`
	Bowtie map[string]bool `json:"bowtie,omitempty"`
}

// gridOraclePath is the frozen verdict file, named from the repository root so
// that the test does not depend on how deep its package sits under tests/.
var gridOraclePath = testsupport.RepoPath("testdata", "number_oracle", "verdicts.json")

// gridOraclePairs lists every (schema, instance) the grid needs a verdict for:
// each document as written, and as a float64 position holds it.
func gridOraclePairs(cells []gridCell) []gridOracleEntry {
	seen := map[string]bool{}
	var out []gridOracleEntry
	add := func(s, d string) {
		key := s + "\x00" + d
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, gridOracleEntry{Schema: s, Doc: d})
	}
	for _, c := range cells {
		for _, d := range c.Docs {
			add(c.Schema, d)
			if r, ok := roundedTo64(d); ok {
				add(c.Schema, r)
			}
		}
	}
	return out
}

// gridSampleEnv names the variable that runs the whole grid. `make
// grid-numbers` sets it, and the nightly external workflow runs that target.
const gridSampleEnv = "SCHEMAGEN_NUMBER_GRID_FULL"

// gridSamplePercent is the share of cells a plain `go test` runs, before the
// top-up that gives every keyword and every position at least one.
const gridSamplePercent = 8

// gridSample picks the cells a run compiles. The whole grid is a generated
// package per cell per configuration -- some 1800 of them -- and the cost of a
// run is in compiling those, not in the instances each one judges. So the
// unit sampled is the cell, and a sampled cell is run under every
// configuration with every instance: the invariant the grid exists for,
// configurations agreeing wherever they hold the literal, is checked on each
// cell it runs.
//
// The sample is fixed, not random: a cell is in when an FNV-1a hash of its
// name falls under gridSamplePercent, and then, for any keyword or position no
// such cell reached, the cell of it with the smallest hash is added. Every
// keyword, every position and every configuration is therefore exercised on
// every run, and a given cell is either always in the sample or never -- the
// rest are the nightly full run's.
func gridSample(cells []gridCell) []gridCell {
	if os.Getenv(gridSampleEnv) != "" {
		return cells
	}
	hash := func(c gridCell) uint32 {
		h := fnv.New32a()
		h.Write([]byte(c.Keyword.Name + "/" + c.Position.Name))
		return h.Sum32()
	}
	in := make([]bool, len(cells))
	byKeyword, byPosition := map[string]bool{}, map[string]bool{}
	for i, c := range cells {
		if hash(c)%100 < gridSamplePercent {
			in[i] = true
			byKeyword[c.Keyword.Name] = true
			byPosition[c.Position.Name] = true
		}
	}
	topUp := func(covered map[string]bool, name func(gridCell) string) {
		best := map[string]int{}
		for i, c := range cells {
			n := name(c)
			if covered[n] {
				continue
			}
			if j, ok := best[n]; !ok || hash(c) < hash(cells[j]) {
				best[n] = i
			}
		}
		for n, i := range best {
			in[i] = true
			covered[n] = true
			byKeyword[cells[i].Keyword.Name] = true
			byPosition[cells[i].Position.Name] = true
		}
	}
	topUp(byKeyword, func(c gridCell) string { return c.Keyword.Name })
	topUp(byPosition, func(c gridCell) string { return c.Position.Name })
	var out []gridCell
	for i, c := range cells {
		if in[i] {
			out = append(out, c)
		}
	}
	return out
}

// TestNumberVerdictGrid runs the grid: a fixed sample of it under `go test`,
// all of it with SCHEMAGEN_NUMBER_GRID_FULL set (`make grid-numbers`, run
// nightly). Set SCHEMAGEN_NUMBER_GRID_DUMP to a path to write the (schema,
// instance) pairs the oracle has to answer, which is the input
// scripts/number-oracle/ freezes into verdicts.json.
func TestNumberVerdictGrid(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles generated packages under six configurations")
	}
	all := gridCells()
	// Every pair of the whole grid has a frozen verdict, sampled or not: the
	// check is a map lookup, and a grid edited without rerunning the oracle
	// should fail every run rather than only the nightly one.
	pairs := gridOraclePairs(all)
	if dump := os.Getenv("SCHEMAGEN_NUMBER_GRID_DUMP"); dump != "" {
		b, err := json.MarshalIndent(pairs, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dump, b, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Skipf("wrote %d oracle pairs to %s", len(pairs), dump)
	}
	oracle := loadGridOracle(t)
	missing := 0
	for _, p := range pairs {
		if _, ok := oracle[p.Schema+"\x00"+p.Doc]; !ok {
			missing++
			if missing <= 5 {
				t.Errorf("no frozen verdict for %s against %s", p.Doc, p.Schema)
			}
		}
	}
	if missing > 0 {
		t.Fatalf("%d grid pairs have no frozen verdict; the grid changed without the oracle being rerun (see scripts/number-oracle)", missing)
	}

	cells := gridSample(all)
	sampledK, sampledP := map[string]bool{}, map[string]bool{}
	for _, c := range cells {
		sampledK[c.Keyword.Name] = true
		sampledP[c.Position.Name] = true
	}
	for _, c := range all {
		if !sampledK[c.Keyword.Name] || !sampledP[c.Position.Name] {
			t.Fatalf("the sample leaves out %s/%s's keyword or position entirely", c.Keyword.Name, c.Position.Name)
		}
	}
	t.Logf("running %d of %d cells under %d configurations (%s=1 runs them all)", len(cells), len(all), len(gridConfigs), gridSampleEnv)

	type verdictKey struct{ cfg, cell, doc string }
	results := map[verdictKey]string{}
	ran := map[string]bool{}
	var mu sync.Mutex
	// One module per configuration, each in its own temporary directory, so the
	// six builds are independent and run side by side.
	t.Run("configs", func(t *testing.T) {
		for _, cfg := range gridConfigs {
			cfg := cfg
			t.Run(cfg.Name, func(t *testing.T) {
				t.Parallel()
				out := runGridConfig(t, cfg, cells)
				mu.Lock()
				defer mu.Unlock()
				ran[cfg.Name] = out != nil
				for k, v := range out {
					parts := strings.SplitN(k, "\x00", 2)
					results[verdictKey{cfg.Name, parts[0], parts[1]}] = v
				}
			})
		}
	})

	checked, failures := 0, 0
	failedCells := map[string][]string{}
	byCellAndDoc := map[string]map[string][]string{}
	for _, c := range cells {
		cellID := c.Keyword.Name + "/" + c.Position.Name
		for i, d := range c.Docs {
			for _, cfg := range gridConfigs {
				if !ran[cfg.Name] {
					continue // already reported: the configuration did not run
				}
				got, ok := results[verdictKey{cfg.Name, cellID, d}]
				if !ok {
					t.Errorf("%s %s %s: no verdict came back", cfg.Name, cellID, d)
					failures++
					continue
				}
				want := gridExpected(c, i, cfg.Name, oracle)
				checked++
				if !gridAgrees(got, want) {
					failures++
					failedCells[cellID] = append(failedCells[cellID], fmt.Sprintf("%s:%s=%s/%s", cfg.Name, c.Values[i], got, want))
					if failures <= 60 {
						t.Errorf("%s: %s at %s, instance %s: got %s, want %s (%s holding)",
							cfg.Name, c.Keyword.Name, c.Position.Name, c.Values[i], got, want, gridHolding(c.Keyword, c.Position.Name, cfg.Name))
					}
				}
				if byCellAndDoc[cellID] == nil {
					byCellAndDoc[cellID] = map[string][]string{}
				}
				if gridHolding(c.Keyword, c.Position.Name, cfg.Name) == "exact" {
					byCellAndDoc[cellID][d] = append(byCellAndDoc[cellID][d], cfg.Name+"="+gridVerdictOnly(got))
				}
			}
		}
	}
	if failures > 60 {
		t.Errorf("... and %d more", failures-60)
	}
	if failures > 0 {
		// Every failing cell, once, with its failures in one line: the shape
		// of a defect is usually a whole position or a whole configuration.
		failing := make([]string, 0, len(failedCells))
		for id := range failedCells {
			failing = append(failing, id)
		}
		sort.Strings(failing)
		for _, id := range failing {
			t.Logf("failing cell %s (%d): %s", id, len(failedCells[id]), strings.Join(failedCells[id], "  "))
		}
	}
	// The invariant the grid exists for: wherever a configuration holds the
	// literal, it gives the verdict every other such configuration gives. A
	// flag about representation does not change a verdict.
	disagreements := 0
	ids := make([]string, 0, len(byCellAndDoc))
	for id := range byCellAndDoc {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for d, vs := range byCellAndDoc[id] {
			first := ""
			for _, v := range vs {
				verdict := v[strings.Index(v, "=")+1:]
				if first == "" {
					first = verdict
				} else if verdict != first {
					disagreements++
					if disagreements <= 20 {
						t.Errorf("%s, instance %s: configurations that hold the literal disagree: %v", id, d, vs)
					}
					break
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no cell was checked: the grid is watching nothing")
	}
	t.Logf("%d cells x configurations checked against the oracle; %d failed; %d disagreements between exact configurations", checked, failures, disagreements)
}

func gridVerdictOnly(got string) string {
	if strings.HasPrefix(got, "invalid") {
		return "invalid"
	}
	return got
}

// gridExpected is the verdict a cell must give: the oracle's on the document
// as the position holds it.
func gridExpected(c gridCell, i int, cfg string, oracle map[string]gridOracleEntry) string {
	doc := c.Docs[i]
	holding := gridHolding(c.Keyword, c.Position.Name, cfg)
	value := c.Values[i]
	if strings.Contains(holding, "float") {
		r, ok := roundedTo64(doc)
		if !ok {
			return "invalid:decode"
		}
		doc = r
		value, _ = roundedTo64(value)
	}
	if strings.Contains(holding, "int64") && gridNamesIntegerPastInt64(value) {
		return "invalid:decode"
	}
	if oracle[c.Schema+"\x00"+doc].Valid {
		return "valid"
	}
	return "invalid"
}

// gridNamesIntegerPastInt64 reports an instance that names an integer no
// int64 holds -- anywhere in it, for an array instance -- which an int64
// position refuses at decode.
func gridNamesIntegerPastInt64(x string) bool {
	var v any
	dec := json.NewDecoder(strings.NewReader(x))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return false
	}
	past := false
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case json.Number:
			// big.Rat rather than anything of this repository's: the grid's
			// own reasoning stays independent of the code it checks.
			if r, ok := new(big.Rat).SetString(string(t)); ok && r.IsInt() && !r.Num().IsInt64() {
				past = true
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return past
}

// gridAgrees compares a verdict with the expected one. A refusal is a refusal
// whether the decode or Validate raised it: a patternProperties member or a
// contains element is decoded into its own type inside Validate, so a value
// that type cannot hold is refused there rather than by the document's decode.
func gridAgrees(got, want string) bool {
	if strings.HasPrefix(want, "invalid") {
		return strings.HasPrefix(got, "invalid")
	}
	return got == want
}

func loadGridOracle(t *testing.T) map[string]gridOracleEntry {
	t.Helper()
	b, err := os.ReadFile(gridOraclePath)
	if err != nil {
		t.Fatalf("reading the frozen oracle: %v", err)
	}
	var entries []gridOracleEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		t.Fatalf("parsing the frozen oracle: %v", err)
	}
	out := make(map[string]gridOracleEntry, len(entries))
	for _, e := range entries {
		out[e.Schema+"\x00"+e.Doc] = e
	}
	return out
}

// runGridConfig generates every cell under one configuration, one package per
// cell in one module, and runs every instance through its root type. The
// answer for each is "valid", "invalid:decode" or "invalid:validate".
func runGridConfig(t *testing.T, cfg gridConfig, cells []gridCell) map[string]string {
	t.Helper()
	dir := t.TempDir()
	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}
	var imports, runners strings.Builder
	type job struct {
		Pkg  string
		Cell string
		Docs []string
	}
	var jobs []job
	for _, c := range cells {
		// Named by the cell rather than its place in the run, so a cell is the
		// same source -- and the same build-cache entry -- in the sample and in
		// the full grid.
		h := fnv.New32a()
		h.Write([]byte(c.Keyword.Name + "/" + c.Position.Name))
		pkg := fmt.Sprintf("c%08x", h.Sum32())
		var s schema.Schema
		if err := json.Unmarshal([]byte(c.Schema), &s); err != nil {
			t.Fatalf("%s/%s: %v", c.Keyword.Name, c.Position.Name, err)
		}
		s.Normalize()
		gc := cfg.Cfg()
		gc.PackageName = pkg
		gc.OmitEmpty = true
		gc.RootTypeName = "Root"
		ir, err := generator.New(gc).Generate(&s)
		if err != nil {
			t.Fatalf("%s: generating %s/%s: %v", cfg.Name, c.Keyword.Name, c.Position.Name, err)
		}
		src, err := em.Emit(ir)
		if err != nil {
			t.Fatalf("%s: emitting %s/%s: %v", cfg.Name, c.Keyword.Name, c.Position.Name, err)
		}
		pdir := filepath.Join(dir, pkg)
		if err := os.MkdirAll(pdir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pdir, "types.go"), src, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := writeSharedHelpersErr(pdir, string(src)); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&imports, "\t%s \"cogen_test/%s\"\n", pkg, pkg)
		fmt.Fprintf(&runners, "\t%q: run[%s.Root],\n", pkg, pkg)
		jobs = append(jobs, job{Pkg: pkg, Cell: c.Keyword.Name + "/" + c.Position.Name, Docs: c.Docs})
	}
	jobsJSON, err := json.Marshal(jobs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "jobs.json"), jobsJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	mainGo := "package main\n\nimport (\n\t\"encoding/json\"\n\t\"fmt\"\n\t\"os\"\n\n" + imports.String() + ")\n\n" +
		"var runners = map[string]func([]byte) string{\n" + runners.String() + "}\n" + gridMainBody
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatal(err)
	}
	// The cogen module layout: the runtime module every generated package
	// imports, replaced onto this checkout.
	if err := writeCogenGoMod(dir); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	out, runErr := testgo.Command(ctx, dir, "run", ".").CombinedOutput()
	if runErr != nil {
		// Reported, and the other configurations still run: a package that
		// does not build under one flag is a failure of that flag, not a
		// reason to stop looking at the rest.
		t.Errorf("%s: running the grid: %v\n%.4000s", cfg.Name, runErr, out)
		return nil
	}
	var answers map[string]string
	if err := json.Unmarshal([]byte(programOutput(out)), &answers); err != nil {
		t.Errorf("%s: reading the grid's answers: %v\n%.4000s", cfg.Name, err, out)
		return nil
	}
	return answers
}

const gridMainBody = `
type job struct {
	Pkg  string
	Cell string
	Docs []string
}

func run[T any](doc []byte) (verdict string) {
	defer func() {
		if r := recover(); r != nil {
			verdict = fmt.Sprintf("panic: %v", r)
		}
	}()
	var v T
	if err := json.Unmarshal(doc, &v); err != nil {
		return "invalid:decode"
	}
	if x, ok := any(&v).(interface{ Validate() error }); ok {
		if err := x.Validate(); err != nil {
			return "invalid:validate"
		}
	}
	return "valid"
}

func main() {
	raw, err := os.ReadFile("jobs.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var jobs []job
	if err := json.Unmarshal(raw, &jobs); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	answers := map[string]string{}
	for _, j := range jobs {
		for _, d := range j.Docs {
			answers[j.Cell+"\x00"+d] = runners[j.Pkg]([]byte(d))
		}
	}
	enc, err := json.Marshal(answers)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(enc))
}
`
