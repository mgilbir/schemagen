package schemagen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// The name-collision diagnostics name each definition by where its document
// wrote it, and these hold that to an oracle rather than to expected strings:
// every location a diagnostic prints is decoded with the one pointer decoder
// (schema.FragmentPointer) and walked through the bytes of the document or
// resource the line names, parsed here with encoding/json alone, and what it
// reaches has to be that definition as written.
//
// The claims are collected from the normalized documents, where a draft-07
// document's "definitions" is mirrored as "$defs" and the mirror is read first,
// and where an embedded resource's definitions sit below the file that embeds
// it. Naming the keyword the collection found a claim under told the author
// about a "$defs" their document does not contain; naming a location from the
// file's root after a resource's $id named a location in no document.

// claimLine is one line of a collision diagnostic that locates a definition:
// "  <document> [(reached by $ref)] <location> becomes|keeps <Name>".
var claimLine = regexp.MustCompile(`(?m)^  (\S+)(?: \(reached by \$ref\))? (#\S*) (?:becomes|keeps) (\S+)$`)

func TestCollisionDiagnosticsNameTheLocationTheDocumentWrote(t *testing.T) {
	const draft07 = `"$schema": "http://json-schema.org/draft-07/schema#"`
	for _, tc := range []struct {
		name  string
		files []string // name, body pairs
		args  []string
		// want maps a Go name the diagnostic reports to the JSON of the
		// definition its location has to reach.
		want map[string]string
		// lines is how many of the diagnostic's lines locate a definition.
		lines int
	}{
		{
			name: "a draft-07 definition named after its own root type",
			files: []string{"x.json", `{` + draft07 + `,
				"title": "X", "type": "object",
				"properties": {"p": {"$ref": "#/definitions/X"}},
				"definitions": {"X": {"type": "string"}}}`},
			want:  map[string]string{"DefsX": `{"type": "string"}`, "DefinitionsX": `{"type": "string"}`},
			lines: 1,
		},
		{
			name: "two documents' draft-07 definitions",
			files: []string{
				"alpha.json", `{` + draft07 + `, "title": "Alpha", "type": "object",
					"properties": {"t": {"$ref": "#/definitions/Thing"}},
					"definitions": {"Thing": {"type": "object", "properties": {"k": {"type": "string"}}}}}`,
				"beta.json", `{` + draft07 + `, "title": "Beta", "type": "object",
					"properties": {"t": {"$ref": "#/definitions/Thing"}},
					"definitions": {"Thing": {"type": "integer"}}}`,
			},
			args: []string{"--shared-types"},
			want: map[string]string{
				"AlphaThing": `{"type": "object", "properties": {"k": {"type": "string"}}}`,
				"BetaThing":  `{"type": "integer"}`,
			},
			lines: 2,
		},
		{
			name:  "embedded resources, each named by its $id",
			files: []string{"single.json", embeddedTwoResources},
			want: map[string]string{
				"AX": `{"type": "string", "minLength": 3}`,
				"BX": `{"type": "integer", "minimum": 10}`,
			},
			lines: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, paths := writeSchemas(t, tc.files...)
			args := append(append(append([]string{}, paths...), "-o", filepath.Join(dir, "gen"), "-p", "gen"), tc.args...)
			stderr, err := runGenerateCapturing(t, args...)
			if err != nil {
				t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
			}
			lines := claimLine.FindAllStringSubmatch(stderr, -1)
			checked := 0
			for _, m := range lines {
				where, loc, name := m[1], m[2], m[3]
				want, ok := tc.want[name]
				if !ok {
					continue
				}
				assertDefinitionAt(t, paths, where, loc, want, stderr)
				checked++
			}
			if checked != tc.lines {
				t.Fatalf("%d lines of the diagnostic located a definition, want %d:\n%s", checked, tc.lines, stderr)
			}
		})
	}
}

// TestNumberedQualifiedNameNamesTheLocationTheDocumentWrote is the same oracle
// for a definition whose qualified name was itself taken. That used to refuse
// the run, naming the definition schemagen was moving; the name registry
// numbers it instead (ADocThing2, ADocThing being the definition keyed so), and
// the line reporting it has to locate the definition the same way.
func TestNumberedQualifiedNameNamesTheLocationTheDocumentWrote(t *testing.T) {
	dir, paths := writeSchemas(t,
		"a.json", `{"$schema": "http://json-schema.org/draft-07/schema#",
			"title": "ADoc",
			"properties": {"t": {"$ref": "#/definitions/Thing"}, "q": {"$ref": "#/definitions/ADocThing"}},
			"definitions": {
				"Thing": {"type": "object", "properties": {"k": {"type": "string"}}},
				"ADocThing": {"type": "object", "properties": {"z": {"type": "boolean"}}}
			}}`,
		"b.json", `{"$schema": "http://json-schema.org/draft-07/schema#",
			"title": "BDoc", "properties": {"t": {"$ref": "#/definitions/Thing"}},
			"definitions": {"Thing": {"type": "integer"}}}`)
	stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...),
		"-o", filepath.Join(dir, "gen"), "-p", "gen", "--shared-types")...)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	located := 0
	for _, m := range claimLine.FindAllStringSubmatch(stderr, -1) {
		if m[3] == "ADocThing2" {
			assertDefinitionAt(t, paths, m[1], m[2], `{"type": "object", "properties": {"k": {"type": "string"}}}`, stderr)
			located++
		}
	}
	if located != 1 {
		t.Fatalf("%d lines locate the definition numbered ADocThing2, want 1:\n%s", located, stderr)
	}
}

// assertDefinitionAt resolves loc -- a fragment -- in the document or embedded
// resource where names: one of the files written, or the $id of a resource one
// of them embeds. The value there has to be want.
func assertDefinitionAt(t *testing.T, paths []string, where, loc, want, diagnostic string) {
	t.Helper()
	var doc any
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var parsed any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatal(err)
		}
		if p == where {
			doc = parsed
			break
		}
		if res := findResource(parsed, where); res != nil {
			doc = res
			break
		}
	}
	if doc == nil {
		t.Fatalf("the diagnostic names %q, which is no document or resource of the run:\n%s", where, diagnostic)
	}
	tokens, isPointer, err := schema.FragmentPointer(strings.TrimPrefix(loc, "#"))
	if err != nil || !isPointer {
		t.Fatalf("the diagnostic names %q in %s, which is not a JSON Pointer fragment (%v):\n%s", loc, where, err, diagnostic)
	}
	got, err := walkRaw(doc, tokens)
	if err != nil {
		t.Fatalf("the diagnostic names %q in %s, which is not a location there (%v):\n%s", loc, where, err, diagnostic)
	}
	var wantV any
	if err := json.Unmarshal([]byte(want), &wantV); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, wantV) {
		gotJSON, _ := json.Marshal(got)
		t.Fatalf("the diagnostic names %q in %s, which holds %s, not the definition %s:\n%s", loc, where, gotJSON, want, diagnostic)
	}
}

// findResource returns the object in v whose "$id" is id.
func findResource(v any, id string) any {
	switch n := v.(type) {
	case map[string]any:
		if got, _ := n["$id"].(string); got == id {
			return n
		}
		for _, k := range sortedAnyKeys(n) {
			if r := findResource(n[k], id); r != nil {
				return r
			}
		}
	case []any:
		for _, e := range n {
			if r := findResource(e, id); r != nil {
				return r
			}
		}
	}
	return nil
}

func sortedAnyKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// walkRaw follows reference tokens through a value encoding/json decoded.
func walkRaw(v any, tokens []string) (any, error) {
	for _, tok := range tokens {
		switch n := v.(type) {
		case map[string]any:
			next, ok := n[tok]
			if !ok {
				return nil, fmt.Errorf("no member %q", tok)
			}
			v = next
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(n) {
				return nil, fmt.Errorf("no entry %q", tok)
			}
			v = n[i]
		default:
			return nil, fmt.Errorf("a scalar has no member %q", tok)
		}
	}
	return v, nil
}

// moveLine is a name-move warning's location: the definition's own location,
// or the one written beside another claimant's description.
var moveLine = regexp.MustCompile(`(?m)^warning: (\S+): (?:(#\S*)|.* \((#\S*)\)) is (\S+), not `)

// TestNameMovesNameTheLocationTheDocumentWrote holds the name-move warnings to
// the same oracle: a draft-07 definition keyed like a generated helper, and a
// property whose field is numbered off a generated method, each located where
// the document wrote it -- "definitions", not the "$defs" mirror.
func TestNameMovesNameTheLocationTheDocumentWrote(t *testing.T) {
	dir, paths := writeSchemas(t, "a.json", `{"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "object",
		"properties": {"validate": {"type": "integer", "minimum": 4}, "m": {"$ref": "#/definitions/SchemagenValidationMode"}},
		"definitions": {"SchemagenValidationMode": {"type": "string", "minLength": 2}}}`)
	stderr, err := runGenerateCapturing(t, paths[0], "-o", filepath.Join(dir, "gen"), "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	want := map[string]string{
		"SchemagenValidationMode2": `{"type": "string", "minLength": 2}`,
		"Validate1":                `{"type": "integer", "minimum": 4}`,
	}
	located := 0
	for _, m := range moveLine.FindAllStringSubmatch(stderr, -1) {
		body, ok := want[m[4]]
		if !ok {
			continue
		}
		assertDefinitionAt(t, paths, m[1], m[2]+m[3], body, stderr)
		located++
	}
	if located != len(want) {
		t.Fatalf("%d move warnings were located, want %d:\n%s", located, len(want), stderr)
	}
}
