package determinism

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

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
)

// TestGeneratedCodeRefusesTheLeastBadMember decodes and validates, many times
// over in one process, documents that break one rule at a dozen members, and
// requires the generated code to refuse every time with the same message --
// the one naming the least of the offending keys.
//
// Each loop in the generated code that walks a map and returns at the first
// member it refuses used to name whichever member Go's randomised map order
// reached first: {"type":"object","properties":{"a":{}},"additionalProperties":
// false} refused {"a":1,"w":1,"x":2,"y":3,"z":4} for "w", "x" or "z" depending
// on the run. They are now emitted through the least_key_open template, whose
// loop checks every member and keeps the least failing key -- the member the runtime
// evaluator's sorted walk names for the same document. A case per loop that
// can refuse a member; each document offends with twelve members, so a loop
// still ranging in map order fails this with near certainty on the first few
// of its forty attempts.
func TestGeneratedCodeRefusesTheLeastBadMember(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a program")
	}
	offenders := strings.Split("bcdefghijklm", "")
	obj := func(format string) string {
		parts := make([]string, len(offenders))
		for i, k := range offenders {
			parts[i] = fmt.Sprintf(format, k)
		}
		return strings.Join(parts, ",")
	}
	cases := []struct {
		name, schema, doc string
		// want is a substring the one message has to carry: the least
		// offending key, quoted the way the message quotes it.
		want string
	}{
		{
			name:   "additionalProperties false",
			schema: `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":false}`,
			doc:    `{"a":"x",` + obj(`"%s":1`) + `}`,
			want:   `"b"`,
		},
		{
			name:   "patternProperties value",
			schema: `{"type":"object","properties":{"a":{"type":"string"}},"patternProperties":{"^[b-m]$":{"type":"string","minLength":3}}}`,
			doc:    `{"a":"x",` + obj(`"%s":"z"`) + `}`,
			want:   `"b"`,
		},
		{
			name:   "map property whose values validate themselves",
			schema: `{"type":"object","properties":{"m":{"type":"object","additionalProperties":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}}}}`,
			doc:    `{"m":{` + obj(`"%s":{}`) + `}}`,
			want:   `"b"`,
		},
		{
			name:   "map property whose values are checked in place",
			schema: `{"type":"object","properties":{"m":{"type":"object","additionalProperties":{"type":"string","minLength":3}}}}`,
			doc:    `{"m":{` + obj(`"%s":"z"`) + `}}`,
			want:   `"b"`,
		},
		{
			name:   "map of maps, both levels offending",
			schema: `{"type":"object","properties":{"n":{"type":"object","additionalProperties":{"type":"object","additionalProperties":{"type":"string","minLength":3}}}}}`,
			doc:    `{"n":{` + obj(`"%s":{`+obj(`"%s":"z"`)+`}`) + `}}`,
			want:   `n["b"]["b"]`,
		},
		{
			name:   "dependentSchemas closing the object",
			schema: `{"type":"object","properties":{"a":{"type":"string"}},"dependentSchemas":{"a":{"properties":{"a":{}},"additionalProperties":false}}}`,
			doc:    `{"a":"x",` + obj(`"%s":1`) + `}`,
			want:   `"b"`,
		},
		{
			name:   "propertyNames",
			schema: `{"type":"object","propertyNames":{"minLength":2}}`,
			doc:    `{"aa":1,` + obj(`"%s":1`) + `}`,
			want:   `"b"`,
		},
		{
			name:   "an allOf branch's own additionalProperties",
			schema: `{"type":"object","properties":{"a":{"type":"string"}},"allOf":[{"properties":{"a":{}},"additionalProperties":false}]}`,
			doc:    `{"a":"x",` + obj(`"%s":1`) + `}`,
			want:   `"b"`,
		},
		{
			name:   "unevaluatedProperties false",
			schema: `{"type":"object","properties":{"a":{"type":"string"}},"unevaluatedProperties":false}`,
			doc:    `{"a":"x",` + obj(`"%s":1`) + `}`,
			want:   `"b"`,
		},
		{
			name:   "unevaluatedProperties with a schema",
			schema: `{"type":"object","properties":{"a":{"type":"string"}},"unevaluatedProperties":{"type":"string","minLength":3}}`,
			doc:    `{"a":"x",` + obj(`"%s":"z"`) + `}`,
			want:   `"b"`,
		},
		{
			name:   "additionalProperties values refused while decoding",
			schema: `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":{"type":"integer"}}`,
			doc:    `{"a":"x",` + obj(`"%s":"not a number"`) + `}`,
			want:   `"b"`,
		},
		{
			name:   "additionalProperties nulls refused while decoding",
			schema: `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":{"type":"string"}}`,
			doc:    `{"a":"x",` + obj(`"%s":null`) + `}`,
			want:   `"b"`,
		},
	}

	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := writeTestGoMod(dir, "leastkey"); err != nil {
		t.Fatal(err)
	}
	var imports, calls strings.Builder
	for i, c := range cases {
		pkg := fmt.Sprintf("c%02d", i)
		sub := filepath.Join(dir, pkg)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		schemaPath := filepath.Join(sub, "schema.json")
		if err := os.WriteFile(schemaPath, []byte(c.schema), 0o644); err != nil {
			t.Fatal(err)
		}
		tr := generateTranscript(em, schemaPath, generator.Config{
			PackageName: pkg,
			OutputDir:   ".",
			OmitEmpty:   true,
			Validation:  generator.ValidationModeStatic,
		})
		if tr.src == nil {
			t.Fatalf("%s: schema did not generate:\n%s", c.name, tr.report)
		}
		// The case has to reach the helper, or it is testing something else.
		if !strings.Contains(string(tr.src), "// refused for the least failing key") {
			t.Fatalf("%s: the generated code has no loop emitted by least_key_open", c.name)
		}
		if err := os.WriteFile(filepath.Join(sub, "types.go"), tr.src, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "helpers.go"), tr.helpers, 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&imports, "\t%s \"leastkey/%s\"\n", pkg, pkg)
		fmt.Fprintf(&calls, "\trun(%q, %q, func(b []byte) error {\n\t\tvar v %s.Root\n\t\tif err := json.Unmarshal(b, &v); err != nil {\n\t\t\treturn err\n\t\t}\n\t\treturn v.Validate()\n\t})\n", c.name, c.doc, pkg)
	}
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
` + imports.String() + `)

var out = map[string][]string{}

// run decodes and validates doc forty times and records every distinct outcome.
// Every call walks fresh maps from a fresh random starting point, so a loop
// that returns at the first member it meets reports different members here.
func run(name, doc string, check func([]byte) error) {
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		seen[fmt.Sprint(check([]byte(doc)))] = true
	}
	var msgs []string
	for m := range seen {
		msgs = append(msgs, m)
	}
	sort.Strings(msgs)
	out[name] = msgs
}

func main() {
` + calls.String() + `	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		panic(err)
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := testgo.Command(ctx, dir, "run", ".")
	output, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("running the program: %v\n%s", err, stderr)
	}
	var got map[string][]string
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("reading the program's output: %v\n%s", err, output)
	}
	for _, c := range cases {
		msgs := got[c.name]
		switch {
		case len(msgs) != 1:
			t.Errorf("%s: %d different refusals of one document:\n\t%s", c.name, len(msgs), strings.Join(msgs, "\n\t"))
		case msgs[0] == "<nil>":
			t.Errorf("%s: the document was accepted; the case is not reaching the rule it is written for", c.name)
		case !strings.Contains(msgs[0], c.want):
			t.Errorf("%s: refused for %q, want the least offending key %s", c.name, msgs[0], c.want)
		}
	}
}

// TestRefusedDecodeLeavesOneValue decodes, many times over in one process, a
// document the decoder refuses at one member, and requires every decode to
// leave the same value behind.
//
// The least-key loop that files a struct's additional and pattern members
// ranges over them in Go's randomised map order, and it filed each member into
// the receiver as it met it. A refusal at the least key ends the loop with the
// members met before it filed and the rest not, so one refused document left a
// different value from run to run -- and a value decoded into twice was not the
// value the second document alone leaves, which the corpus-wide ownership test
// in tests/corpus caught only when the order happened to go that way. The
// members are now filed into locals the receiver is given only when no member
// was refused. Each document here refuses at its least key and carries a dozen
// members that decode, so a loop that still files as it goes leaves a
// different value within the first few of its forty decodes: all forty
// differed from the first, for each case, when this was written.
func TestRefusedDecodeLeavesOneValue(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a program")
	}
	members := func(format string) string {
		keys := strings.Split("cdefghijklmn", "")
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = fmt.Sprintf(format, k)
		}
		return strings.Join(parts, ",")
	}
	cases := []struct{ name, schema, doc string }{
		{
			name:   "additionalProperties value refused",
			schema: `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":{"type":"integer"}}`,
			doc:    `{"a":"x","b":"not a number",` + members(`"%s":1`) + `}`,
		},
		{
			name:   "additionalProperties null refused",
			schema: `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":{"type":"string"}}`,
			doc:    `{"a":"x","b":null,` + members(`"%s":"v"`) + `}`,
		},
		{
			name:   "pattern members filed before an additional member is refused",
			schema: `{"type":"object","patternProperties":{"^p":{}},"additionalProperties":{"type":"integer"}}`,
			doc:    `{"b":"not a number",` + members(`"p%s":{}`) + `}`,
		},
	}

	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := writeTestGoMod(dir, "refusedvalue"); err != nil {
		t.Fatal(err)
	}
	var imports, calls strings.Builder
	for i, c := range cases {
		pkg := fmt.Sprintf("c%02d", i)
		sub := filepath.Join(dir, pkg)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		schemaPath := filepath.Join(sub, "schema.json")
		if err := os.WriteFile(schemaPath, []byte(c.schema), 0o644); err != nil {
			t.Fatal(err)
		}
		tr := generateTranscript(em, schemaPath, generator.Config{
			PackageName: pkg,
			OutputDir:   ".",
			OmitEmpty:   true,
			Validation:  generator.ValidationModeStatic,
		})
		if tr.src == nil {
			t.Fatalf("%s: schema did not generate:\n%s", c.name, tr.report)
		}
		if !strings.Contains(string(tr.src), "// refused for the least failing key") {
			t.Fatalf("%s: the generated code has no loop emitted by least_key_open", c.name)
		}
		if err := os.WriteFile(filepath.Join(sub, "types.go"), tr.src, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "helpers.go"), tr.helpers, 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&imports, "\t%s \"refusedvalue/%s\"\n", pkg, pkg)
		fmt.Fprintf(&calls, "\trun(%q, %q, func() any { return new(%s.Root) })\n", c.name, c.doc, pkg)
	}
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
` + imports.String() + `)

type outcome struct {
	Err       string
	Differing int
}

var out = map[string]outcome{}

// run decodes doc into a fresh value, then forty more times, and counts the
// later decodes whose value differs from the first one's.
func run(name, doc string, mk func() any) {
	first := mk()
	err := json.Unmarshal([]byte(doc), first)
	o := outcome{Err: fmt.Sprint(err)}
	for i := 0; i < 40; i++ {
		v := mk()
		_ = json.Unmarshal([]byte(doc), v)
		if !reflect.DeepEqual(v, first) {
			o.Differing++
		}
	}
	out[name] = o
}

func main() {
` + calls.String() + `	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		panic(err)
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	output, err := testgo.Command(ctx, dir, "run", ".").Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("running the program: %v\n%s", err, stderr)
	}
	var got map[string]struct {
		Err       string
		Differing int
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("reading the program's output: %v\n%s", err, output)
	}
	for _, c := range cases {
		o, ok := got[c.name]
		switch {
		case !ok:
			t.Errorf("%s: the program reported nothing", c.name)
		case o.Err == "<nil>":
			t.Errorf("%s: the document was accepted; the case is not reaching the refusal it is written for", c.name)
		case o.Differing != 0:
			t.Errorf("%s: %d of forty decodes of one refused document left a value different from the first decode's", c.name, o.Differing)
		}
	}
}
