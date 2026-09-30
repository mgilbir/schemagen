package generator

import (
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// The keyword ledger costs in proportion to the schema and the IR it reads,
// however many of the schema's declarations reach the same nodes.
//
// A schema that describes itself -- a metaschema, a tree of rules -- has n
// definitions that each reach the whole graph. The ledger used to answer, for
// each declaration on its own, which of those nodes the declaration's IR
// carried whole and which it had reached, walking everything below each such
// node again for each declaration: O(declarations x reach), 40% of Generate at
// n = 128. What an evaluator literal carries, what has been reached, and what a
// declaration that claims nothing finds, are facts about the node, worked out
// once per run and shared by every declaration that meets it.
//
// Each family below is measured at n = 8 ... 128 on the ledger alone -- the
// run over a Generate call's finished IR, not the generation before it --
// against the size of what it reads: the document's values and the IR's
// elements. That is n for most families, and n squared for the allOf chain,
// whose every link's struct carries the properties of every link after it.
// The allocations, which are deterministic, may grow 1.15 times faster than
// that size -- 2.3 times for each doubling of a linear one, where a quadratic
// cost doubles to 4. The CPU time, the process's own user and system time and
// the least of five interleaved rounds, is held to the same over the whole
// range and to a looser factor for each step, since it is a measurement.

type ledgerFamily struct {
	name   string
	schema func(n int) string
}

func ledgerFamilies() []ledgerFamily {
	object := func(v map[string]any) string {
		b, err := json.Marshal(v)
		if err != nil {
			panic(err)
		}
		return string(b)
	}
	ref := func(to string) map[string]any { return map[string]any{"$ref": to} }
	name := func(prefix string, i int) string { return fmt.Sprintf("%s%d", prefix, i) }
	return []ledgerFamily{
		{
			// The metaschema's shape: a property for each keyword, each one
			// of several shapes that lead back to the root.
			name: "metaschema fan-out",
			schema: func(n int) string {
				props := map[string]any{}
				defs := map[string]any{}
				for i := 0; i < n; i++ {
					props[name("kw", i)] = ref("#/definitions/" + name("d", i))
					defs[name("d", i)] = map[string]any{"anyOf": []any{
						ref("#"),
						map[string]any{"type": "array", "items": ref("#")},
						map[string]any{"type": "object", "additionalProperties": ref("#")},
					}}
				}
				return object(map[string]any{
					"$schema": "http://json-schema.org/draft-07/schema#", "$id": "https://schemagen.test/keywords",
					"type": []any{"object", "boolean"}, "properties": props, "definitions": defs,
				})
			},
		},
		{
			// A conditional per keyword whose branches lead back to the root
			// and to one shared definition.
			name: "conditional fan-out",
			schema: func(n int) string {
				props := map[string]any{}
				defs := map[string]any{"common": map[string]any{
					"type": "object", "additionalProperties": ref("#"),
				}}
				for i := 0; i < n; i++ {
					props[name("kw", i)] = ref("#/$defs/" + name("k", i))
					defs[name("k", i)] = map[string]any{
						"if":   ref("#"),
						"then": map[string]any{"anyOf": []any{ref("#"), ref("#/$defs/common")}},
						"else": map[string]any{"not": ref("#/$defs/common")},
					}
				}
				return object(map[string]any{
					"$schema": "https://json-schema.org/draft/2019-09/schema", "$id": "https://schemagen.test/conditionals",
					"type": "object", "properties": props, "$defs": defs,
				})
			},
		},
		{
			// A long allOf chain: each definition is the next one and a
			// property of its own.
			name: "allOf chain",
			schema: func(n int) string {
				defs := map[string]any{}
				for i := 0; i < n; i++ {
					d := map[string]any{
						"type":       "object",
						"properties": map[string]any{name("p", i): map[string]any{"type": "string", "maxLength": i + 1}},
					}
					if i+1 < n {
						d["allOf"] = []any{ref("#/$defs/" + name("c", i+1))}
					}
					defs[name("c", i)] = d
				}
				return object(map[string]any{
					"$schema": "https://json-schema.org/draft/2020-12/schema",
					"$ref":    "#/$defs/c0", "$defs": defs,
				})
			},
		},
		{
			// A root anyOf of n definitions, each leading back to it.
			name: "root anyOf of definitions",
			schema: func(n int) string {
				alts := make([]any, 0, n)
				defs := map[string]any{}
				for i := 0; i < n; i++ {
					alts = append(alts, ref("#/definitions/"+name("b", i)))
					defs[name("b", i)] = map[string]any{"anyOf": []any{
						ref("#"), map[string]any{"type": "string", "minLength": i},
					}}
				}
				return object(map[string]any{
					"$schema": "http://json-schema.org/draft-07/schema#", "$id": "https://schemagen.test/branches",
					"anyOf": alts, "definitions": defs,
				})
			},
		},
	}
}

// ledgerCost is what one run of the ledger costs, and the size of what it
// read.
type ledgerCost struct {
	allocs uint64
	cpu    time.Duration
	defs   int
	// size is the schema's nodes and the IR's elements: what the ledger is
	// linear in.
	size int
}

// documentSize counts the values of the JSON document src: an upper bound on
// its schema nodes, and linear in them.
func documentSize(src string) int {
	var v any
	if err := json.Unmarshal([]byte(src), &v); err != nil {
		panic(err)
	}
	n := 0
	var walk func(any)
	walk = func(x any) {
		n++
		switch x := x.(type) {
		case map[string]any:
			// maporder: a count; the order members are counted in is not read.
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
	return n
}

// irSize counts the elements of the IR: every struct, pointer, slice element
// and map entry reachable from the type definitions, each counted once. A
// schema node is the document's, counted by documentSize, and not descended
// into; an evaluator's set of compiled nodes is one element, since the ledger
// asks it about a node and never reads it through.
func irSize(file *File) int {
	n := 0
	seen := map[uintptr]bool{}
	schemaType := reflect.TypeOf((*schema.Schema)(nil))
	evalType := reflect.TypeOf((*EvaluatorNodes)(nil))
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || v.Type() == schemaType {
				return
			}
			if seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			n++
			if v.Type() == evalType {
				return
			}
			walk(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Struct:
			n++
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				n++
				walk(v.Index(i))
			}
		case reflect.Map:
			iter := v.MapRange()
			for iter.Next() {
				n++
				walk(iter.Value())
			}
		}
	}
	for _, td := range file.TypeDefs {
		walk(reflect.ValueOf(td))
	}
	return n
}

// ledgerSubject is one generated schema whose ledger is measured.
type ledgerSubject struct {
	g    *Generator
	root *schema.Schema
	reps int
	cost ledgerCost
}

// prepareLedger generates src and counts what one run of the ledger alone
// allocates over the finished IR.
func prepareLedger(t *testing.T, src string, reps int) *ledgerSubject {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(src), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	g := New(Config{PackageName: "p"})
	file, err := g.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	sub := &ledgerSubject{g: g, root: &s, reps: reps}
	sub.cost = ledgerCost{defs: len(file.TypeDefs), size: documentSize(src) + irSize(file)}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	g.runLedger(&s)
	runtime.ReadMemStats(&after)
	sub.cost.allocs = after.Mallocs - before.Mallocs
	return sub
}

// timeLedgers sets each subject's CPU time to the least, over five rounds, of
// the mean CPU time of a run in a batch. The subjects take turns within each
// round, so a stretch of a busy machine slows every size alike rather than
// the one measured while it lasted.
//
// The collector is off while a batch runs: what a collection costs is the live
// heap -- the generator's, the IR's, the test binary's -- and not the ledger's
// work, and it grows with the schema on its own account. What the ledger
// allocates is counted by prepareLedger. A batch allocates a few megabytes at
// most.
func timeLedgers(subjects []*ledgerSubject) {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	for round := 0; round < 5; round++ {
		for _, sub := range subjects {
			runtime.GC()
			start := ledgerCPUTime()
			for i := 0; i < sub.reps; i++ {
				sub.g.runLedger(sub.root)
			}
			if took := (ledgerCPUTime() - start) / time.Duration(sub.reps); round == 0 || took < sub.cost.cpu {
				sub.cost.cpu = took
			}
		}
	}
}

// ledgerCPUTime is the CPU time the process has used so far, user and system:
// what other processes on a busy machine take is not counted.
func ledgerCPUTime() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		panic(err)
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func TestLedgerCostIsLinearInTheSchema(t *testing.T) {
	const (
		// slack is how much faster than what it read the ledger's cost may
		// grow: 2.3 for each doubling of a linear input, and 2 is linear.
		slack = 1.15
		// slackCPU is the same for one step of the CPU time, a measurement of
		// a millisecond or so; the whole range is held to slack.
		slackCPU = 1.75
	)
	sizes := []int{8, 16, 32, 64, 128}
	timeLedgers([]*ledgerSubject{prepareLedger(t, ledgerFamilies()[0].schema(4), 1)})
	for _, family := range ledgerFamilies() {
		t.Run(family.name, func(t *testing.T) {
			subjects := make([]*ledgerSubject, len(sizes))
			for i, n := range sizes {
				subjects[i] = prepareLedger(t, family.schema(n), max(4, 4096/n))
			}
			timeLedgers(subjects)
			costs := make([]ledgerCost, len(sizes))
			for i, sub := range subjects {
				costs[i] = sub.cost
			}
			var rows strings.Builder
			for i, n := range sizes {
				fmt.Fprintf(&rows, "\n  n=%-4d size=%-7d allocs=%-9d cpu=%-10v types=%d", n, costs[i].size, costs[i].allocs, costs[i].cpu, costs[i].defs)
			}
			if last := costs[len(costs)-1]; last.defs < sizes[len(sizes)-1] {
				t.Fatalf("the family no longer generates a type per definition (%d types at n=%d):%s", last.defs, sizes[len(sizes)-1], rows.String())
			}
			for i := 1; i < len(sizes); i++ {
				prev, cur := costs[i-1], costs[i]
				grew := float64(cur.size) / float64(prev.size)
				if r := float64(cur.allocs) / float64(prev.allocs); r > slack*grew {
					t.Errorf("allocations grew %.2fx from n=%d to n=%d, for %.2fx the schema and IR; want at most %.2fx:%s",
						r, sizes[i-1], sizes[i], grew, slack*grew, rows.String())
				}
				if r := float64(cur.cpu) / float64(prev.cpu); r > slackCPU*grew {
					t.Errorf("CPU time grew %.2fx from n=%d to n=%d, for %.2fx the schema and IR; want at most %.2fx:%s",
						r, sizes[i-1], sizes[i], grew, slackCPU*grew, rows.String())
				}
			}
			first, last := costs[0], costs[len(costs)-1]
			bound := float64(last.size) / float64(first.size)
			for i := 1; i < len(sizes); i++ {
				bound *= slack
			}
			if whole := float64(last.cpu) / float64(first.cpu); whole > bound {
				t.Errorf("CPU time grew %.1fx from n=%d to n=%d, want at most %.1fx:%s", whole, sizes[0], sizes[len(sizes)-1], bound, rows.String())
			}
			t.Log(rows.String())
		})
	}
}
