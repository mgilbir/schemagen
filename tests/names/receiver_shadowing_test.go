package names

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// receiverShadowingSchema reaches every construct whose generated method body
// declares a local of its own: a property decoded by hand because its name
// cannot go in a struct tag, a multipleOf on a required and on an optional
// number, uniqueItems on a property and on an alias, the patternProperties,
// additionalProperties and unevaluatedProperties walks, a branch's own
// additionalProperties, a oneOf at a property and one standing for a whole
// type, the checks on a value an untyped object schema accepts when it is not
// an object, and the closures an untyped schema's evaluator is built from.
//
// Every type is named with the letter the test is sweeping, so that every
// method receiver is that letter.
func receiverShadowingSchema(letter string) []byte {
	def := func(name string) string { return letter + name }
	ref := func(name string) map[string]any { return map[string]any{"$ref": "#/$defs/" + def(name)} }
	s := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"$defs": map[string]any{
			def("qty"):    map[string]any{"type": "number", "multipleOf": 2},
			def("list"):   map[string]any{"type": "array", "uniqueItems": true, "items": map[string]any{"type": "string"}},
			def("choice"): map[string]any{"oneOf": []any{map[string]any{"type": "object", "properties": map[string]any{"k": map[string]any{"type": "string"}}, "required": []any{"k"}}, map[string]any{"type": "object", "properties": map[string]any{"z": map[string]any{"type": "integer"}}, "required": []any{"z"}}}},
			def("loose"):  map[string]any{"properties": map[string]any{"a": map[string]any{}}, "minimum": 1, "multipleOf": 2, "minLength": 1, "pattern": "^a", "minItems": 1},
			def("dyn"):    map[string]any{"minLength": 1, "minimum": 1, "pattern": "^a"},
		},
		"properties": map[string]any{
			"a,b":   map[string]any{"type": "integer"},
			"c,d":   map[string]any{"type": "string"},
			"m":     map[string]any{"type": "number", "multipleOf": 0.5},
			"n":     map[string]any{"type": "number", "multipleOf": 0.5},
			"u":     map[string]any{"type": "array", "uniqueItems": true, "items": map[string]any{"type": "string"}},
			"pp":    map[string]any{"type": "object", "patternProperties": map[string]any{"^x": map[string]any{"type": "integer", "minimum": 1, "multipleOf": 2}, "^s": map[string]any{"type": "string", "minLength": 1, "pattern": "^a"}, "^t": map[string]any{"type": []any{"string", "null"}}}},
			"ap":    map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"ue":    map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{}}, "unevaluatedProperties": map[string]any{"type": "string", "minLength": 2}},
			"uep":   map[string]any{"type": "object", "patternProperties": map[string]any{"^p": map[string]any{}}, "unevaluatedProperties": map[string]any{"type": "number", "multipleOf": 2}},
			"af":    map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{}}, "additionalProperties": false},
			"bo":    map[string]any{"type": "object", "allOf": []any{map[string]any{"properties": map[string]any{"a": map[string]any{}}, "additionalProperties": false}}},
			"ow":    map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "integer"}}},
			"qty":   ref("qty"),
			"list":  ref("list"),
			"cho":   ref("choice"),
			"loose": ref("loose"),
			"dyn":   ref("dyn"),
		},
		"required": []any{"n"},
	}
	raw, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return raw
}

// TestMethodLocalsDoNotShadowReceivers generates receiverShadowingSchema under
// every letter and compiles the result.
//
// A method's receiver is named after the first letter of its type, and the
// templates declared single-letter locals of their own inside method bodies --
// v, k, q, s, b, c, i, f. Where the receiver was used after one of those in the
// same scope, the local won:
//
//	if v, ok := raw["a,b"]; ok {
//		if err := json.Unmarshal(v, &v.AB); err != nil {
//
// does not compile, and neither does a multipleOf on a property of a type
// named Q..., which divided into a local q and then reported q.Field. On an
// alias it did compile, and reported the quotient as the value that failed.
func TestMethodLocalsDoNotShadowReceivers(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles generated packages")
	}
	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := writeTestGoMod(dir, "receivers"); err != nil {
		t.Fatal(err)
	}
	for l := 'a'; l <= 'z'; l++ {
		letter := string(l)
		var s schema.Schema
		if err := json.Unmarshal(receiverShadowingSchema(letter), &s); err != nil {
			t.Fatal(err)
		}
		s.NormalizeForDraft(schema.DraftUnknown)
		s.ComputeBaseURIs(nil, &s)
		pkg := "p" + letter
		cfg := generator.Config{PackageName: pkg, OutputDir: ".", OmitEmpty: true, RootTypeName: strings.ToUpper(letter) + "root"}
		ir, err := generator.New(cfg).Generate(&s)
		if err != nil {
			t.Fatalf("%s: %v", letter, err)
		}
		src, err := em.Emit(ir)
		if err != nil {
			t.Fatalf("%s: %v", letter, err)
		}
		helpers, ok, err := em.EmitHelpers(pkg, generator.HelpersReferencedBy(string(src)))
		if err != nil {
			t.Fatalf("%s: %v", letter, err)
		}
		pdir := filepath.Join(dir, pkg)
		if err := os.MkdirAll(pdir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pdir, "types.go"), src, 0o644); err != nil {
			t.Fatal(err)
		}
		if ok {
			if err := os.WriteFile(filepath.Join(pdir, "schemagen_helpers.go"), helpers, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The alias whose check divided into q: the message has to name the value.
	main := `package main

import (
	"fmt"

	"receivers/pq"
)

func main() {
	fmt.Println(pq.Qqty(3).Validate())
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	vet := testgo.Command(ctx, dir, "vet", "./...")
	if out, err := vet.CombinedOutput(); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
	run := testgo.Command(ctx, dir, "run", ".")
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, out)
	}
	if got, want := programOutput(out), "3 is not a multiple of 2"; got != want {
		t.Errorf("Qqty(3).Validate() = %q, want %q", got, want)
	}
}
