package refs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
)

// An optional property reached through a chain of $refs is the same property
// however long the chain is, and wherever it goes.
//
// A $ref chain is a chain of Go names -- {"$ref":"#/$defs/A"} over an A that is
// itself {"$ref":"#/$defs/B"} declares `type A B` -- and whether an optional
// field needs a pointer, an omitempty or an omitzero is a question about the
// value at the end of the chain. It used to be answered one name deep. So a
// property whose chain was two long and ended at an object came out as a value
// field that omitempty never omits, and {"name":"x"} was written back out as
// {"name":"x","sig":{"q":""}}: a property the document never had, holding a
// value that satisfies the definition's own `required`, so nothing downstream
// could tell it was invented. Where the object was a oneOf, the invented value
// was a null the same type refused to read back. Every CycloneDX 1.6 BOM has
// one (definitions.signature is a $ref to jsf's signature definition).
//
// The grid below is the whole of that question: every kind of value a
// definition can generate, reached inline, through one, two and three $refs in
// the same document, and through chains that cross into a second document at
// the first hop, the second hop or both -- under the default configuration,
// under --omit-empty=false, with both documents in one package
// (--shared-types), and with each document in a package of its own
// (--schema-package), where the far end of the chain is a type only the other
// package's published record describes.
//
// What each configuration must do:
//
//   - A document that leaves the property out marshals without it. That is the
//     defect itself, and it holds wherever optional fields are omitted at all:
//     under --omit-empty=false every optional field is written by design, and a
//     zero the schema permits is what that flag asks for.
//   - A document that carries the property marshals it back as the value it
//     was, and a value the schema refuses is refused, through every chain.
//   - Whatever is marshalled decodes again with the same type. That is what the
//     oneOf spelling broke, and it holds under every configuration.

// refChainKind is one kind of value a definition can generate.
type refChainKind struct {
	name    string
	schema  string
	present string // a value the definition accepts, and that is its type's zero where one exists
	refused string // a value the definition refuses; "" where nothing is refused
}

var refChainKinds = []refChainKind{
	{"struct", `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`, `{"q":"v"}`, `{}`},
	{"objunion", `{"type":"object","oneOf":[{"properties":{"q":{"type":"string"}},"required":["q"]},{"properties":{"r":{"type":"integer"}},"required":["r"]}]}`, `{"r":1}`, `{"r":1,"q":"v"}`},
	{"union", `{"oneOf":[{"type":"string"},{"type":"integer"}]}`, `5`, `true`},
	{"enum", `{"enum":["a","b"]}`, `"a"`, `"z"`},
	{"rawenum", `{"enum":[{"a":1},"x"]}`, `{"a":1}`, `{"a":2}`},
	{"alias", `{"type":"string","maxLength":3}`, `""`, `"long"`},
	{"array", `{"type":"array","items":{"type":"string"},"maxItems":1}`, `[]`, `["a","b"]`},
	{"map", `{"type":"object","additionalProperties":{"type":"string"}}`, `{}`, `{"a":1}`},
	{"int", `{"type":"integer","maximum":5}`, `0`, `6`},
	{"bool", `{"type":"boolean","const":false}`, `false`, `true`},
	{"num", `{"type":"number","maximum":5}`, `0`, `5.5`},
	{"datetime", `{"type":"string","format":"date-time"}`, `"2020-01-01T00:00:00Z"`, ``},
	{"inferred", `{"maxLength":3}`, `""`, `"long"`},
	{"typeonly", `{"type":["string","integer"]}`, `""`, `true`},
	{"not", `{"not":{"type":"string"}}`, `0`, `"s"`},
	{"dynamic", `{"oneOf":[{"minimum":1},{"maximum":0}]}`, `0`, ``},
	{"nullable", `{"type":["string","null"]}`, `null`, `1`},
	{"any", `{}`, `0`, ``},
}

// refChainRoutes are the ways a property reaches a kind's definition. local
// routes stay in schema.json; the others reach other.json, whose own chain of
// OH names ends at the same definitions.
var refChainRoutes = []struct {
	suffix string
	ref    func(kind string) string // "" for the inline definition
}{
	{"0", func(string) string { return "" }},
	{"1", func(k string) string { return "#/$defs/B_" + k }},
	{"2", func(k string) string { return "#/$defs/H1_" + k }},
	{"3", func(k string) string { return "#/$defs/H2_" + k }},
	{"x1", func(k string) string { return "other.json#/$defs/B_" + k }},
	{"x2", func(k string) string { return "other.json#/$defs/OH1_" + k }},
	{"x2b", func(k string) string { return "#/$defs/LX_" + k }},
	{"x3", func(k string) string { return "#/$defs/LX2_" + k }},
	{"x4", func(k string) string { return "other.json#/$defs/OH2_" + k }},
}

// refChainDocuments builds the two documents and the list of cases, one per
// property.
func refChainDocuments(t *testing.T) (schemaDoc, otherDoc []byte, cases []refChainCase) {
	t.Helper()
	props := map[string]json.RawMessage{}
	defs := map[string]json.RawMessage{}
	odefs := map[string]json.RawMessage{}
	ref := func(to string) json.RawMessage {
		b, _ := json.Marshal(map[string]string{"$ref": to})
		return b
	}
	for _, k := range refChainKinds {
		if !json.Valid([]byte(k.schema)) {
			t.Fatalf("kind %s: schema is not JSON", k.name)
		}
		defs["B_"+k.name] = json.RawMessage(k.schema)
		defs["H1_"+k.name] = ref("#/$defs/B_" + k.name)
		defs["H2_"+k.name] = ref("#/$defs/H1_" + k.name)
		defs["LX_"+k.name] = ref("other.json#/$defs/B_" + k.name)
		defs["LX2_"+k.name] = ref("other.json#/$defs/OH1_" + k.name)
		odefs["B_"+k.name] = json.RawMessage(k.schema)
		odefs["OH1_"+k.name] = ref("#/$defs/B_" + k.name)
		odefs["OH2_"+k.name] = ref("#/$defs/OH1_" + k.name)
		for _, r := range refChainRoutes {
			name := "p_" + k.name + "_" + r.suffix
			if to := r.ref(k.name); to != "" {
				props[name] = ref(to)
			} else {
				props[name] = json.RawMessage(k.schema)
			}
			cases = append(cases, refChainCase{Name: name, Present: k.present, Refused: k.refused})
		}
	}
	var err error
	schemaDoc, err = json.Marshal(map[string]any{
		"$schema":    "https://json-schema.org/draft/2020-12/schema",
		"$id":        "https://ex.test/schema.json",
		"type":       "object",
		"properties": props,
		"$defs":      defs,
	})
	if err != nil {
		t.Fatal(err)
	}
	otherDoc, err = json.Marshal(map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     "https://ex.test/other.json",
		"type":    "object",
		"$defs":   odefs,
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Name < cases[j].Name })
	return schemaDoc, otherDoc, cases
}

type refChainCase struct {
	Name    string `json:"name"`
	Present string `json:"present"`
	Refused string `json:"refused"`
}

func TestOptionalPropertyThroughARefChainRoundTrips(t *testing.T) {
	if testing.Short() {
		t.Skip("generates and compiles five packages")
	}
	bin := schemagenBinary(t)
	schemaDoc, otherDoc, cases := refChainDocuments(t)
	src := t.TempDir()
	schemaPath := filepath.Join(src, "schema.json")
	otherPath := filepath.Join(src, "other.json")
	writeCrossFile(t, schemaPath, string(schemaDoc))
	writeCrossFile(t, otherPath, string(otherDoc))

	configs := []struct {
		name        string
		omitsAbsent bool
		args        func(out string) []string
	}{
		{"default", true, func(out string) []string {
			return []string{"generate", schemaPath, "-o", filepath.Join(out, "gen"), "-p", "gen", "--root-name", "schema.json=Root"}
		}},
		{"omit-empty=false", false, func(out string) []string {
			return []string{"generate", schemaPath, "-o", filepath.Join(out, "gen"), "-p", "gen", "--root-name", "schema.json=Root", "--omit-empty=false"}
		}},
		{"shared-types", true, func(out string) []string {
			return []string{"generate", schemaPath, otherPath, "-o", filepath.Join(out, "gen"), "-p", "gen", "--shared-types",
				"--root-name", "schema.json=Root", "--root-name", "other.json=Other"}
		}},
		{"schema-package", true, func(out string) []string {
			return []string{"generate", schemaPath, otherPath, "-o", out,
				"--schema-package", "https://ex.test/schema.json=ex.test/rc/gen",
				"--schema-package", "https://ex.test/other.json=ex.test/rc/other",
				"--root-name", "schema.json=Root", "--root-name", "other.json=Other"}
		}},
		{"schema-package,omit-empty=false", false, func(out string) []string {
			return []string{"generate", schemaPath, otherPath, "-o", out, "--omit-empty=false",
				"--schema-package", "https://ex.test/schema.json=ex.test/rc/gen",
				"--schema-package", "https://ex.test/other.json=ex.test/rc/other",
				"--root-name", "schema.json=Root", "--root-name", "other.json=Other"}
		}},
	}
	casesJSON, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range configs {
		t.Run(cfg.name, func(t *testing.T) {
			t.Parallel()
			out := t.TempDir()
			runSchemagen(t, bin, cfg.args(out)...)
			if err := writeTestGoMod(out, "ex.test/rc"); err != nil {
				t.Fatal(err)
			}
			drv := filepath.Join(out, "driver")
			if err := os.MkdirAll(drv, 0o755); err != nil {
				t.Fatal(err)
			}
			main := strings.NewReplacer(
				"@OMITS@", fmt.Sprint(cfg.omitsAbsent),
			).Replace(refChainDriver)
			if err := os.WriteFile(filepath.Join(drv, "main.go"), []byte(main), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(drv, "cases.json"), casesJSON, 0o644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			cmd := testgo.Command(ctx, out, "run", "-mod=mod", "./driver", filepath.Join(drv, "cases.json"))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("driver: %v\n%s", err, output)
			}
			if got := strings.TrimSpace(string(output)); got != fmt.Sprintf("PASS %d", len(cases)) {
				t.Errorf("%d properties, each reached by a different route; the ones that did not round-trip:\n%s",
					len(cases), got)
			}
		})
	}
}

// refChainDriver checks one property at a time, and reads back only that
// property's own member of whatever was marshalled, so a failure names the route
// that failed rather than the first failing member of the whole object.
const refChainDriver = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"

	gen "ex.test/rc/gen"
)

type testCase struct {
	Name    string ` + "`json:\"name\"`" + `
	Present string ` + "`json:\"present\"`" + `
	Refused string ` + "`json:\"refused\"`" + `
}

const omitsAbsent = @OMITS@

func canonical(b []byte) any {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return "unparseable: " + string(b)
	}
	return v
}

// member marshals v and returns name's member of the result.
func member(v gen.Root, name string) (json.RawMessage, bool, error) {
	out, err := json.Marshal(v)
	if err != nil {
		return nil, false, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		return nil, false, fmt.Errorf("marshalled %s, which is not an object: %v", out, err)
	}
	m, ok := obj[name]
	return m, ok, nil
}

// readsBack decodes {name: m} with the same type and marshals it again:
// whatever the type wrote, it has to be able to read. validate also holds what
// was written to the schema, which a value the document carried has to satisfy.
//
// A zero --omit-empty=false invents is held only to the first half. The flag
// writes a zero wherever the schema is not seen to forbid it, and the README
// names the shapes it sees -- a null for a typed property, a zero a const, an
// enum, a minLength, a pattern or a numeric bound excludes -- so an invented
// null that a oneOf refuses because two of its branches accept it is that
// flag's documented reach, and not a question about the chain the property is
// reached through.
func readsBack(name string, m json.RawMessage, validate bool) error {
	doc := fmt.Sprintf("{%q:%s}", name, m)
	var again gen.Root
	if err := json.Unmarshal([]byte(doc), &again); err != nil {
		return fmt.Errorf("wrote %s, which the same type refuses to read: %v", doc, err)
	}
	if err := again.Validate(); validate && err != nil {
		return fmt.Errorf("wrote %s, which the same type reads and then refuses: %v", doc, err)
	}
	m2, _, err := member(again, name)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(canonical(m), canonical(m2)) {
		return fmt.Errorf("marshalling is not idempotent: %s, then %s", m, m2)
	}
	return nil
}

func main() {
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	var cases []testCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		panic(err)
	}
	failed := 0
	fail := func(format string, args ...any) {
		failed++
		fmt.Printf(format+"\n", args...)
	}
	for _, c := range cases {
		// Absent.
		var absent gen.Root
		if err := json.Unmarshal([]byte("{}"), &absent); err != nil {
			fail("%s: {} does not decode: %v", c.Name, err)
			continue
		}
		m, ok, err := member(absent, c.Name)
		switch {
		case err != nil:
			fail("%s: absent: %v", c.Name, err)
		case ok && omitsAbsent:
			fail("%s: absent, and written back as %s", c.Name, m)
		case ok:
			if err := readsBack(c.Name, m, false); err != nil {
				fail("%s: absent: %v", c.Name, err)
			}
		}

		// Present.
		doc := fmt.Sprintf("{%q:%s}", c.Name, c.Present)
		var present gen.Root
		if err := json.Unmarshal([]byte(doc), &present); err != nil {
			fail("%s: %s does not decode: %v", c.Name, doc, err)
			continue
		}
		if err := present.Validate(); err != nil {
			fail("%s: %s is refused: %v", c.Name, doc, err)
		}
		m, ok, err = member(present, c.Name)
		switch {
		case err != nil:
			fail("%s: present: %v", c.Name, err)
		case !ok:
			fail("%s: %s written back without the property", c.Name, doc)
		case !reflect.DeepEqual(canonical(m), canonical([]byte(c.Present))):
			fail("%s: %s written back as %s", c.Name, doc, m)
		default:
			if err := readsBack(c.Name, m, true); err != nil {
				fail("%s: present: %v", c.Name, err)
			}
		}

		// Refused.
		if c.Refused != "" {
			doc := fmt.Sprintf("{%q:%s}", c.Name, c.Refused)
			var refused gen.Root
			if err := json.Unmarshal([]byte(doc), &refused); err == nil {
				if err := refused.Validate(); err == nil {
					fail("%s: %s is accepted", c.Name, doc)
				}
			}
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
	fmt.Printf("PASS %d\n", len(cases))
}
`
