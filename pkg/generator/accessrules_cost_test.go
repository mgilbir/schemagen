//go:build unix

package generator

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// Generating a schema under --strict-read-write costs in proportion to the
// schema, however its references loop back.
//
// The rules --strict-read-write emits for a value held as raw JSON are paths,
// and they used to be found by walking every path from the value down to the
// depth bound. A schema whose references lead back to itself through several
// value positions has a number of such paths exponential in the depth bound: a
// metaschema, whose every applicator keyword leads back to the root, did not
// finish generating in 45 seconds, and marks nothing at all -- ten groups of the
// JSON Schema Test Suite ("validate definition against metaschema", "remote
// ref, containing refs itself") under every draft from 4 to 2020-12.
//
// The shapes below grow a schema of that kind -- by the number of keywords
// leading back (fan-out) and by the length of the chain of definitions they
// lead back through (depth), through $ref and through $dynamicRef, marking
// nothing and marking a member -- and hold its generation to growing about
// linearly: allocations, which are deterministic, may not more than triple when
// the schema doubles, and the CPU time the process spent, read with getrusage
// so that other load on the machine does not count, stays under a bound a
// quadratic, let alone an exponential, blows through at the largest size.

// recursiveShape builds a schema of the given size.
type recursiveShape struct {
	name  string
	build func(size int) string
}

// metaLike is a metaschema's shape: fanout keywords, each leading back to the
// root through a chain of depth definitions, and, if mark is set, a member
// marked writeOnly and one marked readOnly beside them.
func metaLike(dialect, ref string, fanout, depth int, mark bool) string {
	var props, defs []string
	for d := 0; d < depth; d++ {
		next := ref
		if d+1 < depth {
			next = fmt.Sprintf(`{"$ref":"#/$defs/d%d"}`, d+1)
		}
		defs = append(defs, fmt.Sprintf(`"d%d":{"type":["object","boolean"],"properties":{"inner":%s},"additionalProperties":%s}`, d, next, next))
	}
	into := ref
	if depth > 0 {
		into = `{"$ref":"#/$defs/d0"}`
	}
	for f := 0; f < fanout; f++ {
		switch f % 4 {
		case 0:
			props = append(props, fmt.Sprintf(`"k%d":%s`, f, into))
		case 1:
			props = append(props, fmt.Sprintf(`"k%d":{"type":"object","additionalProperties":%s}`, f, into))
		case 2:
			props = append(props, fmt.Sprintf(`"k%d":{"type":"array","items":%s}`, f, into))
		case 3:
			props = append(props, fmt.Sprintf(`"k%d":{"anyOf":[%s,{"type":"array","items":%s}]}`, f, into, into))
		}
	}
	if mark {
		props = append(props, `"secret":{"type":"string","writeOnly":true}`, `"id":{"type":"string","readOnly":true}`)
	}
	return `{"$schema":"` + dialect + `","$id":"https://example.test/meta","$dynamicAnchor":"meta","type":["object","boolean"],` +
		`"properties":{` + strings.Join(props, ",") + `},"$defs":{` + strings.Join(defs, ",") + `}}`
}

var recursiveShapes = []recursiveShape{
	{"ref fan-out, no marks", func(n int) string {
		return metaLike("https://json-schema.org/draft/2020-12/schema", `{"$ref":"#"}`, n, 2, false)
	}},
	{"ref depth, no marks", func(n int) string {
		return metaLike("https://json-schema.org/draft/2020-12/schema", `{"$ref":"#"}`, 8, n, false)
	}},
	{"dynamicRef fan-out, no marks", func(n int) string {
		return metaLike("https://json-schema.org/draft/2020-12/schema", `{"$dynamicRef":"#meta"}`, n, 2, false)
	}},
	{"ref fan-out, marked", func(n int) string {
		return metaLike("https://json-schema.org/draft/2020-12/schema", `{"$ref":"#"}`, n, 2, true)
	}},
	{"ref depth, marked", func(n int) string {
		return metaLike("https://json-schema.org/draft/2020-12/schema", `{"$ref":"#"}`, 8, n, true)
	}},
	{"dynamicRef depth, marked", func(n int) string {
		return metaLike("https://json-schema.org/draft/2020-12/schema", `{"$dynamicRef":"#meta"}`, 8, n, true)
	}},
	{"draft-07 ref fan-out, marked", func(n int) string {
		return strings.Replace(metaLike("http://json-schema.org/draft-07/schema#", `{"$ref":"#"}`, n, 2, true), `"$dynamicAnchor":"meta",`, "", 1)
	}},
}

// cpuTime is the user and system time the process has spent.
func cpuTime(t *testing.T) time.Duration {
	t.Helper()
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		t.Fatalf("getrusage: %v", err)
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// generate generates doc, under --strict-read-write or not, and returns the
// allocations it made and the CPU time it took.
func generate(t *testing.T, doc string, strict bool) (allocs uint64, cpu time.Duration) {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := cpuTime(t)
	if _, err := New(Config{PackageName: "cost", StrictReadWrite: strict}).Generate(&s); err != nil {
		t.Fatalf("generate: %v", err)
	}
	cpu = cpuTime(t) - start
	runtime.ReadMemStats(&after)
	return after.Mallocs - before.Mallocs, cpu
}

// What is held to linear growth is what the flag costs: the allocations of a
// --strict-read-write run beyond those of the same run without it. The rest of
// generation is not this flag's, and is not linear in every one of these
// shapes: the runtime evaluator compiles a schema whole for each type it holds
// as data, so a metaschema with n keywords that each become such a type costs
// n compiles of n keywords under either setting.
func TestStrictReadWriteGenerationIsProportionateToTheSchema(t *testing.T) {
	sizes := []int{4, 8, 16, 32}
	for _, shape := range recursiveShapes {
		t.Run(shape.name, func(t *testing.T) {
			var allocs []uint64
			var cpus []time.Duration
			for _, n := range sizes {
				doc := shape.build(n)
				plain, _ := generate(t, doc, false)
				strict, c := generate(t, doc, true)
				// The flag's own cost, floored so that a size where it costs
				// next to nothing does not make the next one look steep.
				over := uint64(2000)
				if strict > plain+over {
					over = strict - plain
				}
				allocs = append(allocs, over)
				cpus = append(cpus, c)
			}
			t.Logf("sizes %v: allocations the flag adds %v, cpu %v", sizes, allocs, cpus)
			for i := 1; i < len(sizes); i++ {
				// Doubling the schema at most triples the work: linear is two,
				// quadratic four, and the path walk this replaced grew without
				// bound.
				if allocs[i] > 3*allocs[i-1] {
					t.Errorf("size %d -> %d: the allocations the flag adds grew %d -> %d, more than threefold",
						sizes[i-1], sizes[i], allocs[i-1], allocs[i])
				}
			}
			// The largest schema is a few hundred nodes; generating it is well
			// under a second of CPU on any machine this runs on.
			if limit := 5 * time.Second; cpus[len(cpus)-1] > limit {
				t.Errorf("size %d took %v of CPU, over %v", sizes[len(sizes)-1], cpus[len(cpus)-1], limit)
			}
		})
	}
}

// TestStrictReadWriteDoesNotWalkAMemberThatWalksItself holds the decoder and
// the encoder to one walk per level of a value that holds itself. A struct's
// machine reaches through a member decoded into a struct of its own, and that
// struct's decoder refuses, and its encoder strips, the same members again --
// so the parent walking into it re-read the member's subtree at every level.
// Where the member's type does everything the walk would, the parent's start
// stops at the member: here "c" is the struct itself.
func TestStrictReadWriteDoesNotWalkAMemberThatWalksItself(t *testing.T) {
	const doc = `{"title":"Node","type":"object","properties":{"c":{"$ref":"#"},` +
		`"t":{"type":"array","prefixItems":[{"type":"object","properties":{"ro":{"type":"string","readOnly":true},"wo":{"type":"string","writeOnly":true}}}]}}}`
	var s schema.Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	ir, err := New(Config{PackageName: "cost", StrictReadWrite: true}).Generate(&s)
	if err != nil {
		t.Fatal(err)
	}
	var node *StructDef
	for _, td := range ir.TypeDefs {
		if sd, ok := td.(*StructDef); ok && sd.Name == "Node" {
			node = sd
		}
	}
	if node == nil || node.AccessRules == nil || node.StripRules == nil {
		t.Fatalf("Node has no rules: %+v", node)
	}
	walked := func(rules *AccessRules, seek func(AccessMove) bool) (c, t bool) {
		for _, m := range ir.AccessMachine[rules.Start].Moves {
			if m.Next >= 0 && seek(m) {
				switch m.Step.Name {
				case "c":
					c = true
				case "t":
					t = true
				}
			}
		}
		return c, t
	}
	if c, tt := walked(node.AccessRules, func(m AccessMove) bool { return m.SeekReadOnly }); c || !tt {
		t.Errorf("the decoder walks into c: %v, into t: %v; want only t", c, tt)
	}
	if c, tt := walked(node.StripRules, func(m AccessMove) bool { return m.SeekWriteOnly }); c || !tt {
		t.Errorf("the encoder walks into c: %v, into t: %v; want only t", c, tt)
	}
}

// TestStrictReadWriteStillFindsTheRulesOfARecursiveSchema is the other half:
// a proportionate walk that found nothing would pass the test above. A marked
// metaschema-shaped schema still gets the root's own members' rules and the
// ones below its recursive positions.
func TestStrictReadWriteStillFindsTheRulesOfARecursiveSchema(t *testing.T) {
	var s schema.Schema
	doc := `{"type":"object","properties":{"k":{"type":"array","items":{"$ref":"#"}},` +
		`"secret":{"type":"string","writeOnly":true}},"additionalProperties":{"$ref":"#"}}`
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	g := New(Config{PackageName: "cost", StrictReadWrite: true})
	if _, err := g.Generate(&s); err != nil {
		t.Fatal(err)
	}
	rules := g.accessRulesFor(&s, 1)
	if rules == nil {
		t.Fatal("no rules")
	}
	// The machine's runs, spelled out to a depth: every path from the start
	// state to a writeOnly mark.
	machine := g.output.AccessMachine
	have := map[string]bool{}
	var run func(state int, path []string)
	run = func(state int, path []string) {
		if len(path) > 5 {
			return
		}
		for _, m := range machine[state].Moves {
			step := m.Step.Name
			switch m.Step.Kind {
			case AccessItems:
				step = "[]"
			case AccessOther:
				step = "*"
			}
			next := append(append([]string(nil), path...), step)
			if m.WriteOnly {
				have[strings.Join(next, "/")] = true
			}
			if m.Next >= 0 && m.SeekWriteOnly {
				run(m.Next, next)
			}
		}
	}
	run(rules.Start, nil)
	for _, want := range []string{"secret", "k/[]/secret", "*/secret", "k/[]/k/[]/secret", "*/k/[]/*/secret"} {
		if !have[want] {
			t.Errorf("no writeOnly rule %s among %d runs", want, len(have))
		}
	}
	// And the machine is the size of the schema, not of its runs.
	if n := len(machine); n > 8 {
		t.Errorf("the machine has %d states for a schema of four nodes", n)
	}
}
