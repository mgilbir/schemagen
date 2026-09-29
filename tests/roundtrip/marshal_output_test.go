package roundtrip

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
)

// TestMarshalWritesWhatEncodingJSONWrote pins what MarshalJSON writes for a
// value built in Go, now that every generated type writes itself (see
// generator/encodeplan.go) rather than handing its members to encoding/json.
//
// The expected bytes and the expected error are what the MarshalJSON before
// that change produced for the same value, and every piece of them is
// encoding/json's own doing: a member written by hand and one a struct tag
// names, sorted into one object because the struct has an overflow map; a
// patternProperties and an additionalProperties value held as raw JSON with
// white space in it, compacted; "<", "&", ">" and U+2028 escaped in keys and in
// values; a nil pointer in a slice and a nil pointer in a map written as null,
// the map's keys in order; a union member. A failure three levels down is
// reported through the three MarshalJSONs encoding/json called on the way, as
// a *json.MarshalerError errors.As can find -- every level is reached through a
// pointer, which both encodings/json name the same way.
func TestMarshalWritesWhatEncodingJSONWrote(t *testing.T) {
	if testing.Short() {
		t.Skip("generates and compiles a package")
	}
	t.Parallel()
	bin := schemagenBinary(t)
	out := t.TempDir()
	schemaPath := filepath.Join(out, "s.json")
	writeCrossFile(t, schemaPath, `{"type":"object","properties":{
	  "c":{"$ref":"#"},
	  "f":{"type":"number"},
	  "kids":{"type":"array","items":{"$ref":"#"}},
	  "m":{"type":"object","additionalProperties":{"$ref":"#"}},
	  "a\"b":{"type":"string"},
	  "s":{"type":"string"},
	  "u":{"oneOf":[{"type":"string"},{"$ref":"#"}]}},
	  "patternProperties":{"^p":{"type":"integer"}}}`)
	runSchemagen(t, bin, "generate", schemaPath, "-o", filepath.Join(out, "m"), "-p", "m", "--root-name", "s.json=Root")
	if err := writeTestGoMod(out, "ex.test/mo"); err != nil {
		t.Fatal(err)
	}
	drv := filepath.Join(out, "driver")
	if err := os.MkdirAll(drv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(drv, "main.go"), []byte(marshalOutputDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	output, err := testgo.Command(ctx, out, "run", "-mod=mod", "./driver").CombinedOutput()
	if err != nil {
		t.Fatalf("driver: %v\n%s", err, output)
	}
	got := strings.Split(programOutput(output), "\n")
	want := []string{
		`{"a\"b":"x\u003cy","c":{"kids":[{"f":1.5},null],"m":{"a":null,"b":{"s":"b"}},"s":"n"},"p1":1,"p\u003c":2,"s":"\u003c\u0026\u003e\u2028","u":"u\u003c","zz":{"h":"\u003cb\u003e"}}`,
		`json: error calling MarshalJSON for type *m.Root: json: error calling MarshalJSON for type *m.Root: json: error calling MarshalJSON for type *m.Root: json: unsupported value: NaN`,
		`as: *m.Root`,
	}
	// json.Marshal's bytes, and then MarshalJSON's own, which are the same.
	want = append([]string{want[0]}, want...)
	if len(got) != len(want) {
		t.Fatalf("driver printed %d lines, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got: %s\nwant: %s", i, got[i], want[i])
		}
	}
}

const marshalOutputDriver = `package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"ex.test/mo/m"
)

func sp(s string) *string   { return &s }
func fp(f float64) *float64 { return &f }

func main() {
	r := &m.Root{S: sp("<&>\u2028"), AB: sp("x<y"),
		PatternProperties:    map[string]json.RawMessage{"p1": json.RawMessage(" 1 "), "p<": json.RawMessage("2")},
		AdditionalProperties: map[string]json.RawMessage{"zz": json.RawMessage(" {\"h\" : \"<b>\"} ")},
		C:                    &m.Root{S: sp("n"), Kids: []*m.Root{{F: fp(1.5)}, nil}, M: map[string]*m.Root{"b": {S: sp("b")}, "a": nil}},
		U:                    &m.Root_String{String: "u<"},
	}
	b, err := json.Marshal(r)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))
	// The type's own MarshalJSON too: json.Marshal compacts what it returns,
	// which would hide a MarshalJSON that wrote something else.
	b, err = r.MarshalJSON()
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))
	r.C.C = &m.Root{F: fp(math.NaN())}
	_, err = json.Marshal(r)
	fmt.Println(err)
	var me *json.MarshalerError
	if errors.As(err, &me) {
		fmt.Println("as:", me.Type)
	}
}
`
