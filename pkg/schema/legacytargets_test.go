package schema

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// legacyDoc is a draft-3 document with a subschema at every location Normalize
// rewrites away, each titled with where the document wrote it.
const legacyDoc = `{
	"$schema": "http://json-schema.org/draft-03/schema#",
	"extends": [{"title": "extends/0", "properties": {"q": {"title": "extends/0/properties/q"}}}, {"title": "extends/1"}],
	"disallow": ["string", {"title": "disallow/1"}],
	"type": ["string", {"title": "type/1"}],
	"dependencies": {"a": {"title": "dependencies/a"}, "b": ["x"], "c": "x", "a/b": {"title": "dependencies/a~1b"}},
	"properties": {"p": {"extends": {"title": "properties/p/extends"}, "disallow": {"title": "properties/p/disallow"}}}
}`

var legacyTargetCases = []struct {
	fragment, title string // title "" means no schema is there
}{
	{"/extends/0", "extends/0"},
	{"/extends/1", "extends/1"},
	{"/extends/0/properties/q", "extends/0/properties/q"},
	{"/disallow/1", "disallow/1"},
	{"/type/1", "type/1"},
	{"/dependencies/a", "dependencies/a"},
	{"/dependencies/a~1b", "dependencies/a~1b"},
	{"/dependencies/a%7E1b", "dependencies/a~1b"},
	{"/properties/p/extends", "properties/p/extends"},
	{"/properties/p/disallow", "properties/p/disallow"},
	// Locations that hold no schema in the document: a type name, the names a
	// dependency requires, draft 3's bare-string dependency.
	{"/disallow/0", ""},
	{"/type/0", ""},
	{"/dependencies/b", ""},
	{"/dependencies/c", ""},
}

// TestAPointerIntoARewrittenKeywordStillResolves: Normalize moves the
// subschemas of "extends", "disallow", "dependencies" and draft 3's
// schema-valued "type" entries to the keywords that replaced them, and a $ref
// written against the document -- "#/dependencies/a" -- named a location the
// normalized tree no longer had, so it failed to resolve in every resolver.
// Each is answered from where the document wrote it, through every resolver.
func TestAPointerIntoARewrittenKeywordStillResolves(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.json"), []byte(legacyDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/schema+json")
		_, _ = w.Write([]byte(legacyDoc))
	}))
	defer server.Close()
	var mapped Schema
	if err := json.Unmarshal([]byte(legacyDoc), &mapped); err != nil {
		t.Fatal(err)
	}
	mapped.Normalize()
	if bad := mapped.MalformedKeywords(); len(bad) > 0 {
		t.Fatalf("the fixture is malformed: %v", bad)
	}

	resolvers := map[string]func(fragment string) (*Schema, error){
		"LocalResolver": func(fragment string) (*Schema, error) {
			return NewLocalResolver(&mapped).Resolve("#" + fragment)
		},
		"MappingResolver": func(fragment string) (*Schema, error) {
			return NewMappingResolver(map[string]*Schema{"https://example.test/doc.json": &mapped}).
				ResolveSchema("https://example.test/doc.json#"+fragment, nil)
		},
		"FileResolver": func(fragment string) (*Schema, error) {
			return NewFileResolver(dir).ResolveSchema("doc.json#"+fragment, nil)
		},
		"HTTPResolver": func(fragment string) (*Schema, error) {
			return NewHTTPResolver(WithHTTPClient(server.Client())).ResolveSchema(server.URL+"/doc.json#"+fragment, nil)
		},
	}
	for name, resolve := range resolvers {
		for _, tc := range legacyTargetCases {
			got, err := resolve(tc.fragment)
			switch {
			case tc.title == "" && err == nil:
				t.Errorf("%s: #%s reached %q; the document holds no schema there", name, tc.fragment, got.Title)
			case tc.title != "" && err != nil:
				t.Errorf("%s: #%s: %v", name, tc.fragment, err)
			case tc.title != "" && got.Title != tc.title:
				t.Errorf("%s: #%s reached %q, want %q", name, tc.fragment, got.Title, tc.title)
			}
		}
	}

	// The node a pointer reaches is the node in the normalized tree, not a
	// copy: cycle detection and type naming compare nodes by identity.
	r := NewLocalResolver(&mapped)
	if got, _ := r.Resolve("#/dependencies/a"); got != mapped.DependentSchemas["a"] {
		t.Error("#/dependencies/a is not the node dependentSchemas holds")
	}
	if got, _ := r.Resolve("#/extends/1"); got != mapped.AllOf[1] {
		t.Error("#/extends/1 is not the node allOf holds")
	}
}

// TestAPointerIntoADroppedKeywordResolvesUnderItsDialect: under a dialect
// that does not define "extends" the keyword is unknown, and a pointer into an
// unknown keyword still reaches its value, as it does through Extensions; the
// value is then read under the node's dialect, which here drops a "const"
// draft 4 does not have.
func TestAPointerIntoADroppedKeywordResolvesUnderItsDialect(t *testing.T) {
	var s Schema
	doc := `{"$schema":"http://json-schema.org/draft-04/schema#","extends":[{"const":1,"minimum":2}]}`
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	if len(s.AllOf) != 0 {
		t.Fatalf("draft 4 has no extends, and it was rewritten into allOf %v", s.AllOf)
	}
	got, err := NewLocalResolver(&s).Resolve("#/extends/0")
	if err != nil {
		t.Fatal(err)
	}
	if got.Minimum == nil || got.Const != nil {
		t.Errorf("the target was not read under draft 4: minimum=%v const=%v", got.Minimum, got.Const)
	}
}

// TestAPointerIntoAKeywordTheDialectDoesNotDefineResolves: a keyword a
// dialect does not define is an unknown keyword there, and a pointer reaches
// an unknown keyword's value (the official suite's refOfUnknownKeyword); the
// dialect pass clearing the field used to make one this package *has* a field
// for unreachable -- draft 3's "#/not" -- while any keyword it has no field for
// stayed reachable through Extensions. The value is read under the node's
// dialect, as an unknown keyword's is.
func TestAPointerIntoAKeywordTheDialectDoesNotDefineResolves(t *testing.T) {
	for _, tc := range []struct {
		d       Draft
		members string
		ref     string
	}{
		{Draft03, `"not":{"title":"t","const":1}`, "#/not"},
		{Draft03, `"allOf":[{"title":"t","const":1}]`, "#/allOf/0"},
		{Draft04, `"if":{"title":"t","const":1}`, "#/if"},
		{Draft06, `"unevaluatedProperties":{"title":"t","const":1}`, "#/unevaluatedProperties"},
		{Draft202012, `"additionalItems":{"title":"t","properties":{"p":{"title":"deeper"}}}`, "#/additionalItems/properties/p"},
		{Draft202012, `"dependentSchemas":{},"x":1,"additionalItems":{"title":"t"}`, "#/additionalItems"},
	} {
		var s Schema
		doc := documentIn(tc.d, tc.members)
		if err := json.Unmarshal([]byte(doc), &s); err != nil {
			t.Fatal(err)
		}
		s.Normalize()
		got, err := NewLocalResolver(&s).Resolve(tc.ref)
		if err != nil {
			t.Errorf("%s: %s: %v", doc, tc.ref, err)
			continue
		}
		if got.Title != "t" && got.Title != "deeper" {
			t.Errorf("%s: %s reached %q", doc, tc.ref, got.Title)
		}
		if !KeywordDefinedIn("const", tc.d) && got.Const != nil {
			t.Errorf("%s: %s was not read under %v: its const survived", doc, tc.ref, tc.d)
		}
		// Twice: the value is memoized, so both refs are one node.
		again, _ := NewLocalResolver(&s).Resolve(tc.ref)
		if again != got {
			t.Errorf("%s: %s resolved to two nodes", doc, tc.ref)
		}
	}
}
