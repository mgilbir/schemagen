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
)

// The name-collision diagnostics name each definition by where its document
// wrote it, and these hold that to an oracle rather than to expected strings:
// every location a diagnostic prints is read as a JSON Pointer
// (readLocationFragment) and walked through the bytes of the document or
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

// renamedLine is the pinned-name refusal's: "  <location> in <document> was renamed to <Name>".
var renamedLine = regexp.MustCompile(`(?m)^  (#\S*) in (\S+) was renamed to (\S+),`)

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

// TestPinnedNameRefusalNamesTheLocationTheDocumentWrote is the same oracle for
// the refusal that names the definition schemagen was moving when the name it
// chose was taken.
func TestPinnedNameRefusalNamesTheLocationTheDocumentWrote(t *testing.T) {
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
	_, err := runGenerateCapturing(t, append(append([]string{}, paths...),
		"-o", filepath.Join(dir, "gen"), "-p", "gen", "--shared-types")...)
	if err == nil {
		t.Fatal("expected the run to be refused")
	}
	m := renamedLine.FindStringSubmatch(err.Error())
	if m == nil {
		t.Fatalf("the refusal locates no definition:\n%v", err)
	}
	assertDefinitionAt(t, paths, m[2], m[1], `{"type": "object", "properties": {"k": {"type": "string"}}}`, err.Error())
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
	tokens, isPointer, err := readLocationFragment(strings.TrimPrefix(loc, "#"))
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

// readLocationFragment reads a fragment as a JSON Pointer, per RFC 6901.
func readLocationFragment(fragment string) ([]string, bool, error) {
	if fragment == "" {
		return nil, true, nil
	}
	if !strings.HasPrefix(fragment, "/") {
		return nil, false, nil
	}
	tokens := strings.Split(fragment[1:], "/")
	for i, t := range tokens {
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(t, "~1", "/"), "~0", "~")
	}
	return tokens, true, nil
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
