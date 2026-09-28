package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"testing"
)

// parseDocument parses a document with encoding/json alone. UseNumber: a
// document may hold a number no float64 can (1e400).
func parseDocument(body []byte) (any, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	return v, dec.Decode(&v)
}

// walkDocument follows reference tokens through a document parseDocument read.
func walkDocument(cur any, tokens []string) (any, error) {
	for _, tok := range tokens {
		switch n := cur.(type) {
		case map[string]any:
			next, ok := n[tok]
			if !ok {
				return nil, fmt.Errorf("no member %q", tok)
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(n) {
				return nil, fmt.Errorf("no entry %q", tok)
			}
			cur = n[i]
		default:
			return nil, fmt.Errorf("a scalar has no member %q", tok)
		}
	}
	return cur, nil
}

// TestEveryNodeIsLocatedWhereTheDocumentWroteIt holds the location every node
// carries to the document it was read from, over the whole corpus under both
// its own dialect and none: every node Normalize leaves reachable has a
// location, the location is in the document, and it holds a schema -- an
// object or a boolean -- except for the nodes that stand for something the
// document wrote that is not one: draft 3's type names under "disallow", and
// the nodes a rewrite synthesizes, which are located at the keyword that
// produced them.
func TestEveryNodeIsLocatedWhereTheDocumentWroteIt(t *testing.T) {
	docs := corpusSchemas(t)
	if len(docs) < 500 {
		t.Fatalf("only %d schemas found; the corpus walk has stopped reaching them", len(docs))
	}
	nodes := 0
	for _, d := range docs {
		raw, err := parseDocument(d.body)
		if err != nil {
			continue
		}
		for _, draft := range []Draft{DraftUnknown, d.draft} {
			var s Schema
			if json.Unmarshal(d.body, &s) != nil {
				continue
			}
			s.NormalizeForDraft(draft)
			seen := map[*Schema]bool{}
			var visit func(n *Schema)
			visit = func(n *Schema) {
				if n == nil || seen[n] {
					return
				}
				seen[n] = true
				nodes++
				doc, tokens, ok := n.SourceLocation()
				if !ok {
					t.Errorf("%s under %v: a node has no location", d.name, draft)
					return
				}
				if doc != &s {
					t.Errorf("%s under %v: a node is located in another document", d.name, draft)
				}
				v, err := walkDocument(raw, tokens)
				if err != nil {
					t.Errorf("%s under %v: a node is located at %s, which is not in the document: %v", d.name, draft, PointerFragment(tokens...), err)
					return
				}
				switch v.(type) {
				case map[string]any, bool:
				default:
					if !locatedAtRewrittenValue(tokens) {
						t.Errorf("%s under %v: a node is located at %s, which holds %v, not a schema", d.name, draft, PointerFragment(tokens...), v)
					}
				}
				n.eachChild(visit)
				if n.AdditionalProperties != nil {
					visit(n.AdditionalProperties.AsSchema())
				}
				if n.AdditionalItems != nil {
					visit(n.AdditionalItems.AsSchema())
				}
			}
			visit(&s)
		}
	}
	t.Logf("%d nodes located over %d schemas", nodes, len(docs))
}

// locatedAtRewrittenValue reports whether tokens end at a value a node stands
// for that is not itself a schema: a draft 3 "disallow" entry that is a type
// name, or the "disallow", "divisibleBy" or "dependencies" member a rewrite
// synthesized a node for.
func locatedAtRewrittenValue(tokens []string) bool {
	for i := len(tokens) - 1; i >= 0 && i >= len(tokens)-2; i-- {
		switch tokens[i] {
		case "disallow", "divisibleBy", "dependencies":
			return true
		}
	}
	return false
}

func TestRewritesLocateWhatTheySynthesize(t *testing.T) {
	var s Schema
	doc := `{"divisibleBy":2,"multipleOf":3,
		"disallow":["string",{"type":"integer"}],
		"not":{"type":"boolean"},
		"dependencies":{"a":{"minProperties":1}},
		"dependentSchemas":{"a":{"maxProperties":3}},
		"additionalProperties":false}`
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	loc := func(n *Schema) string {
		_, tokens, ok := n.SourceLocation()
		if !ok {
			return "<none>"
		}
		return PointerFragment(tokens...)
	}
	// allOf: the divisibleBy that differs from multipleOf, then the disallow
	// that could not be "not" because "not" was taken.
	if len(s.AllOf) != 2 {
		t.Fatalf("allOf = %d entries, want 2", len(s.AllOf))
	}
	for _, c := range []struct {
		what string
		n    *Schema
		want string
	}{
		{"the divisibleBy node", s.AllOf[0], "#/divisibleBy"},
		{"the disallow node", s.AllOf[1], "#/disallow"},
		{"the union of the disallow entries", s.AllOf[1].Not, "#/disallow"},
		{"a disallow type name", s.AllOf[1].Not.AnyOf[0], "#/disallow/0"},
		{"a disallow schema", s.AllOf[1].Not.AnyOf[1], "#/disallow/1"},
		{"the document's own not", s.Not, "#/not"},
		{"the join of both dependency spellings", s.DependentSchemas["a"], "#/dependencies/a"},
		{"the dependentSchemas half", s.DependentSchemas["a"].AllOf[0], "#/dependentSchemas/a"},
		{"the dependencies half", s.DependentSchemas["a"].AllOf[1], "#/dependencies/a"},
		{"a boolean additionalProperties", s.AdditionalProperties.AsSchema(), "#/additionalProperties"},
	} {
		if got := loc(c.n); got != c.want {
			t.Errorf("%s is located at %s, want %s", c.what, got, c.want)
		}
	}

	// The type name is located, but no schema is there for a pointer to reach.
	if _, err := NewLocalResolver(&s).Resolve("#/disallow/0"); err == nil {
		t.Error(`"#/disallow/0" resolved; the document holds the string "string" there, not a schema`)
	}
	if got, err := NewLocalResolver(&s).Resolve("#/disallow/1"); err != nil || got != s.AllOf[1].Not.AnyOf[1] {
		t.Errorf(`"#/disallow/1" = %v, %v; want the schema the document wrote there`, got, err)
	}
}

func TestAVendorKeywordsSchemaIsLocatedInsideIt(t *testing.T) {
	var s Schema
	if err := json.Unmarshal([]byte(`{"x-v":[{"properties":{"p":{"type":"string"}}}]}`), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	sub, err := NewLocalResolver(&s).Resolve("#/x-v/0")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		n    *Schema
		want []string
	}{
		{sub, []string{"x-v", "0"}},
		{sub.Properties["p"], []string{"x-v", "0", "properties", "p"}},
	} {
		doc, tokens, ok := c.n.SourceLocation()
		if !ok || doc != &s || !reflect.DeepEqual(tokens, c.want) {
			t.Errorf("located at %v in %p (ok=%v), want %v in the document %p", tokens, doc, ok, c.want, &s)
		}
	}

	// Under a node with no location -- a tree nobody normalized -- the value is
	// not taken for the root of a document of its own.
	var bare Schema
	if err := json.Unmarshal([]byte(`{"x-v":{"properties":{"p":{}}}}`), &bare); err != nil {
		t.Fatal(err)
	}
	orphan, err := bare.extensionSchema("x-v", nil, bare.Extensions["x-v"])
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := orphan.SourceLocation(); ok {
		t.Error("a vendor keyword's schema under an unlocated node was given a location")
	}
	if _, _, ok := orphan.Properties["p"].SourceLocation(); ok {
		t.Error("a node below it was given a location")
	}
}

func TestMalformedEntriesAreRecordedWhereTheDocumentWroteThem(t *testing.T) {
	for _, c := range []struct {
		doc     string
		keyword string
		path    []string
	}{
		{`{"allOf":[{},null]}`, "allOf", []string{"1"}},
		{`{"properties":{"a":{},"b":5}}`, "properties", []string{"b"}},
		{`{"items":[{},true,"x"]}`, "items", []string{"2"}},
		{`{"extends":[{},null]}`, "extends", []string{"1"}},
		{`{"disallow":["string",7]}`, "disallow", []string{"1"}},
		{`{"dependencies":{"a":null}}`, "dependencies", []string{"a"}},
		{`{"type":["string",null]}`, "type", []string{"1"}},
		{`{"minLength":null}`, "minLength", nil},
	} {
		var s Schema
		if err := json.Unmarshal([]byte(c.doc), &s); err != nil {
			t.Fatal(err)
		}
		bad := s.MalformedKeywords()
		if len(bad) != 1 || bad[0].Keyword != c.keyword || !reflect.DeepEqual(bad[0].Path, c.path) {
			t.Errorf("%s: malformed = %v, want %s at %v", c.doc, bad, c.keyword, c.path)
		}
	}
}

// TestWrittenFindsTheOriginalOfACopy: Written answers, for a value copy of a
// node, the node the document wrote there, from the location the copy keeps
// and without a walk of the document; for a node of the document, the node
// itself; for a document's root and a copy of it, the root; and for a node
// no document wrote, the node, with found false.
func TestWrittenFindsTheOriginalOfACopy(t *testing.T) {
	var doc Schema
	if err := json.Unmarshal([]byte(`{"properties":{"a":{"type":"string"},"b":{"items":{"minimum":1}}},"$defs":{"d":{}}}`), &doc); err != nil {
		t.Fatal(err)
	}
	doc.Normalize()
	for _, orig := range []*Schema{&doc, doc.Properties["a"], doc.Properties["b"].Items.Schema, doc.Defs["d"]} {
		if got, found := orig.Written(); !found || got != orig {
			t.Errorf("Written of a node of the document = %p, %v; want the node itself", got, found)
		}
		cp := *orig
		if got, found := cp.Written(); !found || got != orig {
			t.Errorf("Written of a copy = %p, %v; want its original %p", got, found, orig)
		}
	}
	built := &Schema{Type: TypeList{"string"}}
	if got, found := built.Written(); found || got != built {
		t.Errorf("Written of a node no document wrote = %p, %v; want itself, not found", got, found)
	}
}
