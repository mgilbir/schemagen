package schema

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustDoc(t *testing.T, src string) *Schema {
	t.Helper()
	var s Schema
	if err := json.Unmarshal([]byte(src), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	return &s
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// JSON Schema 2020-12 Appendix A, "Schema identification examples": every URI
// the table lists for each node of the example document resolves to that node,
// and to no other. It is the specification's own statement of what the index
// is keyed by -- absolute resource URI and fragment -- including a plain-name
// fragment declared in two resources ("bar"), which each resource answers for
// itself.
func TestIndexAnswersTheSpecificationsIdentificationExamples(t *testing.T) {
	root := mustDoc(t, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://example.com/root.json",
		"$defs": {
			"A": { "$anchor": "foo" },
			"B": {
				"$id": "other.json",
				"$defs": {
					"X": { "$anchor": "bar" },
					"Y": { "$id": "t/inner.json", "$anchor": "bar" }
				}
			},
			"C": { "$id": "urn:uuid:ee564b8a-7a87-4125-8c96-e9f123d6766f" }
		}
	}`)
	x := NewResourceIndex(nil)
	if err := x.AddDocument(root, nil); err != nil {
		t.Fatal(err)
	}
	b := root.Defs["B"]
	table := map[*Schema][]string{
		root:           {"https://example.com/root.json", "https://example.com/root.json#"},
		root.Defs["A"]: {"https://example.com/root.json#foo", "https://example.com/root.json#/$defs/A"},
		b:              {"https://example.com/other.json", "https://example.com/other.json#", "https://example.com/root.json#/$defs/B"},
		b.Defs["X"]: {"https://example.com/other.json#bar", "https://example.com/other.json#/$defs/X",
			"https://example.com/root.json#/$defs/B/$defs/X"},
		b.Defs["Y"]: {"https://example.com/t/inner.json", "https://example.com/t/inner.json#bar",
			"https://example.com/other.json#/$defs/Y", "https://example.com/root.json#/$defs/B/$defs/Y"},
		root.Defs["C"]: {"urn:uuid:ee564b8a-7a87-4125-8c96-e9f123d6766f", "urn:uuid:ee564b8a-7a87-4125-8c96-e9f123d6766f#",
			"https://example.com/root.json#/$defs/C"},
	}
	for want, uris := range table {
		for _, uri := range uris {
			got, err := x.ResolveSchema(uri, nil)
			if err != nil {
				t.Errorf("%s: %v", uri, err)
				continue
			}
			if got != want {
				t.Errorf("%s resolved to the wrong node", uri)
			}
		}
	}
	// And "#bar" written in each resource is that resource's own.
	if got, _ := x.Resolve("#bar", b); got != b.Defs["X"] {
		t.Error(`"#bar" written in other.json should be other.json's X`)
	}
	if got, _ := x.Resolve("#bar", b.Defs["Y"]); got != b.Defs["Y"] {
		t.Error(`"#bar" written in t/inner.json should be t/inner.json's own root`)
	}
	if _, err := x.Resolve("#bar", root); err == nil {
		t.Error(`"#bar" written in root.json names nothing root.json declares, and must not resolve`)
	}
}

// The bundled compound document the specification describes (2020-12 §9.3):
// an embedded resource with its own $id and its own dialect, whose "#/..."
// pointers are relative to itself, beside a root that has a same-named
// definition. Each pointer reaches its own resource's node.
func TestIndexResolvesABundledDocumentPerResource(t *testing.T) {
	root := mustDoc(t, `{
		"$id": "https://example.com/schemas/customer",
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"properties": {
			"shipping_address": { "$ref": "/schemas/address" },
			"state": { "$ref": "#/$defs/state" }
		},
		"$defs": {
			"state": { "title": "customer state" },
			"address": {
				"$id": "/schemas/address",
				"$schema": "http://json-schema.org/draft-07/schema#",
				"type": "object",
				"properties": { "state": { "$ref": "#/definitions/state" }, "st": { "$ref": "#/$defs/state" } },
				"definitions": { "state": { "title": "address state", "enum": ["CA", "NY"] } },
				"$defs": { "state": { "title": "address $defs state" } }
			}
		}
	}`)
	x := NewResourceIndex(nil)
	if err := x.AddDocument(root, nil); err != nil {
		t.Fatal(err)
	}
	address := root.Defs["address"]
	resolveTitle := func(ref string, ctx *Schema) string {
		t.Helper()
		got, err := x.Resolve(ref, ctx)
		if err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		return got.Title
	}
	if n, err := x.Resolve(root.Properties["shipping_address"].Ref, root.Properties["shipping_address"]); err != nil || n != address {
		t.Errorf("/schemas/address should be the embedded resource: %v", err)
	}
	if got := resolveTitle("#/$defs/state", root.Properties["state"]); got != "customer state" {
		t.Errorf("#/$defs/state in the root = %q", got)
	}
	if got := resolveTitle("#/definitions/state", address.Properties["state"]); got != "address state" {
		t.Errorf("#/definitions/state in the address resource = %q", got)
	}
	if got := resolveTitle("#/$defs/state", address.Properties["st"]); got != "address $defs state" {
		t.Errorf("#/$defs/state in the address resource = %q, want the address resource's own, not the root's", got)
	}
}

// A document reached by several URIs is one registered document and one
// instance: its retrieval URI, its $id, and a relative path from another
// document all name it.
func TestIndexServesOneDocumentUnderEveryURIItIsKnownBy(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	aPath := filepath.Join(dir, "a.json")
	if err := os.WriteFile(aPath, []byte(`{"$id": "https://ex.test/a.json", "title": "A"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	bPath := filepath.Join(dir, "b", "b.json")
	if err := os.WriteFile(bPath, []byte(`{"properties": {"byPath": {"$ref": "../a.json"}, "byID": {"$ref": "https://EX.test/a.json#"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	loader := NewFileResolver(dir)
	x := NewResourceIndex(loader)
	a, err := LoadFromFile(aPath)
	if err != nil {
		t.Fatal(err)
	}
	a.Normalize()
	if err := x.AddDocument(a, nil); err != nil {
		t.Fatal(err)
	}
	b, err := LoadFromFile(bPath)
	if err != nil {
		t.Fatal(err)
	}
	b.Normalize()
	if err := x.AddDocument(b, nil); err != nil {
		t.Fatal(err)
	}
	for _, prop := range []string{"byPath", "byID"} {
		got, err := x.Resolve(b.Properties[prop].Ref, b.Properties[prop])
		if err != nil {
			t.Fatalf("%s: %v", prop, err)
		}
		if got != a {
			t.Errorf("%s reached a second instance of a.json rather than the registered one", prop)
		}
	}
	// Registering it again under its own URI is the same document.
	if err := x.AddDocument(a, mustURL(t, "https://ex.test/a.json")); err != nil {
		t.Errorf("re-registering a document under a URI it already has: %v", err)
	}
}

// Two schemas identifying as one URI are refused -- within a document, across
// documents, and between an $id and another document's retrieval URI. 2020-12
// §9.1.2: "When multiple schemas try to identify as the same URI, validators
// SHOULD raise an error condition." A refused document leaves the index as it
// was.
func TestIndexRefusesTwoSchemasIdentifyingAsOneURI(t *testing.T) {
	t.Run("in one document", func(t *testing.T) {
		doc := mustDoc(t, `{"$id": "https://ex.test/r.json", "$defs": {
			"a": {"$id": "dup.json", "type": "string"},
			"b": {"$id": "dup.json", "type": "integer"}}}`)
		x := NewResourceIndex(nil)
		err := x.AddDocument(doc, nil)
		var dup *DuplicateIdentifierError
		if !errors.As(err, &dup) || dup.URI != "https://ex.test/dup.json" {
			t.Fatalf("err = %v, want a duplicate identifier error for https://ex.test/dup.json", err)
		}
		if _, err := x.ResolveSchema("https://ex.test/r.json", nil); err == nil {
			t.Error("a refused document must not be registered in part")
		}
	})
	t.Run("across documents", func(t *testing.T) {
		x := NewResourceIndex(nil)
		if err := x.AddDocument(mustDoc(t, `{"$id": "https://ex.test/one.json"}`), nil); err != nil {
			t.Fatal(err)
		}
		err := x.AddDocument(mustDoc(t, `{"$id": "https://EX.TEST/one.json#"}`), nil)
		var dup *DuplicateIdentifierError
		if !errors.As(err, &dup) {
			t.Fatalf("err = %v, want a duplicate identifier error: scheme and host are case-insensitive and an empty fragment is none", err)
		}
		if !strings.Contains(err.Error(), "duplicate $id") {
			t.Errorf("message %q should say what it is", err)
		}
	})
	t.Run("an $id naming another document's file", func(t *testing.T) {
		x := NewResourceIndex(nil)
		if err := x.AddDocument(mustDoc(t, `{}`), mustURL(t, "file:///s/a.json")); err != nil {
			t.Fatal(err)
		}
		var dup *DuplicateIdentifierError
		if err := x.AddDocument(mustDoc(t, `{"$id": "file:///s/a.json"}`), mustURL(t, "file:///s/b.json")); !errors.As(err, &dup) {
			t.Fatalf("err = %v, want a duplicate identifier error", err)
		}
	})
	t.Run("the graph and the index agree", func(t *testing.T) {
		// One walk decides what a resource is for both, so the graph cannot
		// hold a resource the index does not, or the other way about.
		doc := mustDoc(t, `{"$id": "https://ex.test/g.json", "$defs": {
			"a": {"$id": "a.json", "$anchor": "x"},
			"b": {"$id": "b.json", "$defs": {"c": {"$id": "c.json"}}}}}`)
		x := NewResourceIndex(nil)
		if err := x.AddDocument(doc, nil); err != nil {
			t.Fatal(err)
		}
		graph := x.Graph(doc, Draft202012)
		for uri, res := range graph.Resources {
			if got := x.Resource(uri); got == nil || got.Root != res.Root {
				t.Errorf("graph resource %s is not the index's", uri)
			}
		}
		if len(graph.Resources) != 4 {
			t.Errorf("graph has %d resources, want 4", len(graph.Resources))
		}
	})
}

// A plain-name fragment two nodes of one resource declare is refused when a
// reference names it (2020-12 §8.2.2: undefined, and an implementation MAY
// raise an error), rather than answered with whichever the walk met first. The
// same name in two resources is two names.
func TestIndexRefusesAnAmbiguousAnchorWhenItIsNamed(t *testing.T) {
	doc := mustDoc(t, `{"$id": "https://ex.test/r.json", "$defs": {
		"a": {"$anchor": "twice"},
		"b": {"$dynamicAnchor": "twice"},
		"c": {"$id": "c.json", "$anchor": "twice"},
		"d": {"$anchor": "once"}}}`)
	x := NewResourceIndex(nil)
	if err := x.AddDocument(doc, nil); err != nil {
		t.Fatalf("an ambiguous anchor nothing names is not an error: %v", err)
	}
	var amb *AmbiguousAnchorError
	if _, err := x.Resolve("#twice", doc); !errors.As(err, &amb) {
		t.Errorf("err = %v, want an ambiguous anchor error", err)
	}
	if got, err := x.Resolve("#twice", doc.Defs["c"]); err != nil || got != doc.Defs["c"] {
		t.Errorf("c.json declares twice once, and answers for itself: %v", err)
	}
	if got, err := x.Resolve("#once", doc); err != nil || got != doc.Defs["d"] {
		t.Errorf("#once: %v", err)
	}
	// The LocalResolver a caller may use directly says the same.
	if _, err := NewLocalResolver(doc).Resolve("#twice"); !errors.As(err, &amb) {
		t.Errorf("LocalResolver: err = %v, want an ambiguous anchor error", err)
	}
}

// Relative references resolve by RFC 3986 against the base URI of the resource
// they are written in, and nothing else: "../" climbs from that base, and a
// document from nowhere nameable has a base of its own that nothing is fetched
// under.
func TestIndexResolvesRelativeReferencesAgainstTheirOwnBase(t *testing.T) {
	sub := mustDoc(t, `{"title": "sub"}`)
	top := mustDoc(t, `{"title": "top"}`)
	mapping := NewMappingResolver(map[string]*Schema{
		"https://ex.test/dir/sub.json": sub,
		"https://ex.test/top.json":     top,
	})
	doc := mustDoc(t, `{"$id": "https://ex.test/dir/doc.json", "properties": {
		"down": {"$ref": "sub.json"},
		"up": {"$ref": "../top.json"},
		"nested": {"$id": "inner/", "properties": {"rel": {"$ref": "../sub.json"}}}}}`)
	x := NewResourceIndex(mapping)
	if err := x.AddDocument(doc, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := x.Resolve("sub.json", doc.Properties["down"]); err != nil || got != sub {
		t.Errorf("sub.json: %v", err)
	}
	if got, err := x.Resolve("../top.json", doc.Properties["up"]); err != nil || got != top {
		t.Errorf("../top.json: %v", err)
	}
	// Written inside a resource whose $id moved the base to dir/inner/, "../"
	// climbs from there -- to dir/, not to the document's own parent.
	rel := doc.Properties["nested"].Properties["rel"]
	if got, err := x.Resolve("../sub.json", rel); err != nil || got != sub {
		t.Errorf("../sub.json inside dir/inner/: %v", err)
	}

	// No retrieval URI: a base of its own, under which nothing is asked of the
	// loader, and which still makes its embedded relative $ids absolute keys.
	anon := mustDoc(t, `{"$defs": {"e": {"$id": "e.json", "title": "e"}}, "properties": {"p": {"$ref": "e.json"}}}`)
	y := NewResourceIndex(nil)
	if err := y.AddDocument(anon, nil); err != nil {
		t.Fatal(err)
	}
	if anon.BaseURI == nil || anon.BaseURI.Scheme != AnonymousDocumentScheme {
		t.Fatalf("base = %v, want one under %s", anon.BaseURI, AnonymousDocumentScheme)
	}
	if got, err := y.Resolve("e.json", anon.Properties["p"]); err != nil || got != anon.Defs["e"] {
		t.Errorf("e.json in a document from nowhere: %v", err)
	}
}

// A file resolver confined to several directories judges a read by the ones
// that hold the file the reference is written in: two sibling input directories
// do not open either to the other, while a directory holding the referrer
// admits everything under it.
func TestFileResolverConfinesEachReadToTheReferrersDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"a/x.json", "a/other.json", "b/y.json", "b/z.json"} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fr := NewFileResolver("", WithFileResolverRoots(filepath.Join(dir, "a"), filepath.Join(dir, "b")))
	fileURI := func(rel string) string {
		u, err := FileURI(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return u.String()
	}
	referrer := mustURL(t, fileURI("b/y.json"))
	if _, err := fr.ResolveSchema(fileURI("b/z.json"), referrer); err != nil {
		t.Errorf("b/y.json reading b/z.json: %v", err)
	}
	if _, err := fr.ResolveSchema(fileURI("a/other.json"), referrer); err == nil || !strings.Contains(err.Error(), "refusing to read") {
		t.Errorf("b/y.json reading a/other.json: err = %v, want a refusal", err)
	}
	if _, err := fr.ResolveSchema(fileURI("a/other.json"), mustURL(t, fileURI("a/x.json"))); err != nil {
		t.Errorf("a/x.json reading a/other.json: %v", err)
	}
	// With several roots and no base file, a relative path has nothing to be
	// read beside; it is refused rather than joined onto one of them.
	if _, err := fr.ResolveSchema("z.json", nil); err == nil {
		t.Error("a relative path with no referring file was read from one of the roots")
	}
	// A loaded document knows the file it came from.
	s, err := fr.ResolveSchema(fileURI("b/z.json"), referrer)
	if err != nil {
		t.Fatal(err)
	}
	if s.RetrievalURI == nil || s.RetrievalURI.String() != fileURI("b/z.json") {
		t.Errorf("RetrievalURI = %v", s.RetrievalURI)
	}
}
