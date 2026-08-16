package schema

import (
	"encoding/json"
	"net/url"
	"reflect"
	"testing"
)

// Which keywords declare a plain-name fragment was answered three times over --
// by the resolver that searches a document another document $refs into, by the
// resource graph, and by the generator's anchor index -- and the three answers
// differed. "$dynamicAnchor" was in two of them and not in the resolver's, so a
// $ref naming one resolved within a document and was refused across documents
// (issue #307).
//
// These tests hold the answer to one place. AnchorNames states it; the two
// consumers in this package are checked to agree with it on the same input, so
// that a keyword added to one of them and not the other is a failure here
// rather than a report from a sweep months later.

func parseSchemaForAnchors(t *testing.T, src string) *Schema {
	t.Helper()
	var s Schema
	if err := json.Unmarshal([]byte(src), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	return &s
}

func TestAnchorNamesListsEverySpellingOfAPlainNameFragment(t *testing.T) {
	cases := []struct {
		name string
		node string
		want []string
	}{
		{"anchor", `{"$anchor": "node"}`, []string{"node"}},
		// 2020-12 §8.2.2: $dynamicAnchor "behaves like $anchor ... in that it
		// creates a plain name fragment".
		{"dynamicAnchor", `{"$dynamicAnchor": "node"}`, []string{"node"}},
		// One node, two names, both of them live.
		{"both", `{"$anchor": "a", "$dynamicAnchor": "b"}`, []string{"a", "b"}},
		// The same name written twice is one name.
		{"sameName", `{"$anchor": "node", "$dynamicAnchor": "node"}`, []string{"node"}},
		// The pre-2019-09 spelling: names the node without changing the base URI.
		{"legacyIDFragment", `{"$id": "#node"}`, []string{"node"}},
		// A scope-changing id names no fragment.
		{"absoluteID", `{"$id": "https://ex.test/t.json"}`, nil},
		// "#" is the document root and "#/..." is a JSON Pointer; neither is a
		// plain-name fragment.
		{"rootFragment", `{"$id": "#"}`, nil},
		{"pointerFragment", `{"$id": "#/$defs/X"}`, nil},
		// $recursiveAnchor takes a boolean, so it declares no name at all. It is
		// the resource's *unnamed* dynamic anchor, reachable only by
		// "$recursiveRef": "#", and no "#name" can spell it.
		{"recursiveAnchor", `{"$recursiveAnchor": true}`, nil},
		{"nothing", `{"type": "string"}`, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AnchorNames(parseSchemaForAnchors(t, tc.node))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("AnchorNames = %v, want %v", got, tc.want)
			}
		})
	}
}

// Every name AnchorNames reports must be findable by both consumers in this
// package, and a name it does not report must be findable by neither.
func TestResolverAndResourceGraphAgreeWithAnchorNames(t *testing.T) {
	doc := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/t.json",
		"$defs": {
			"byAnchor":        {"$anchor": "a", "type": "string"},
			"byDynamicAnchor": {"$dynamicAnchor": "d", "type": "string"},
			"byBoth":          {"$anchor": "b1", "$dynamicAnchor": "b2", "type": "string"},
			"byLegacyID":      {"$id": "#l", "type": "string"}
		}
	}`
	root := parseSchemaForAnchors(t, doc)
	base, err := url.Parse("https://ex.test/t.json")
	if err != nil {
		t.Fatal(err)
	}
	graph := BuildResourceGraph(root, base, Draft202012)
	res := graph.Resources["https://ex.test/t.json"]
	if res == nil {
		t.Fatalf("no resource for the document; have %v", graph.SortedResourceURIs())
	}
	resolver := NewLocalResolver(root)

	for defName, node := range root.Defs {
		names := AnchorNames(node)
		if len(names) == 0 {
			t.Fatalf("%s declares no anchor name; the fixture no longer covers what it claims to", defName)
		}
		for _, name := range names {
			found, err := resolver.Resolve("#" + name)
			if err != nil {
				t.Errorf("resolver: #%s (declared by %s): %v", name, defName, err)
			} else if found != node {
				t.Errorf("resolver: #%s resolved to the wrong node", name)
			}
			if res.Anchors[name] != node {
				t.Errorf("resource graph: Anchors[%q] (declared by %s) = %v, want the declaring node", name, defName, res.Anchors[name])
			}
		}
	}

	// A name nothing declares must be found by neither.
	if _, err := resolver.Resolve("#absent"); err == nil {
		t.Error("resolver found an anchor nothing declares")
	}
	if res.Anchors["absent"] != nil {
		t.Error("resource graph indexed an anchor nothing declares")
	}
}

// $recursiveAnchor is not a named anchor and must not become one, in either
// consumer -- and must still be recorded as the resource's unnamed dynamic
// anchor, which is what "$recursiveRef": "#" walks to.
func TestRecursiveAnchorIsUnnamedInBothConsumers(t *testing.T) {
	root := parseSchemaForAnchors(t, `{
		"$schema": "https://json-schema.org/draft/2019-09/schema",
		"$id": "https://ex.test/t.json",
		"$recursiveAnchor": true,
		"type": "object",
		"properties": {"child": {"$recursiveRef": "#"}}
	}`)
	base, err := url.Parse("https://ex.test/t.json")
	if err != nil {
		t.Fatal(err)
	}
	graph := BuildResourceGraph(root, base, Draft201909)
	res := graph.Resources["https://ex.test/t.json"]
	if res == nil {
		t.Fatalf("no resource for the document; have %v", graph.SortedResourceURIs())
	}
	if names := AnchorNames(root); names != nil {
		t.Errorf("AnchorNames = %v, want none: $recursiveAnchor takes a boolean", names)
	}
	if len(res.Anchors) != 0 {
		t.Errorf("resource graph named %v; $recursiveAnchor declares no name", res.Anchors)
	}
	if res.DynamicAnchors[""] != root {
		t.Error("resource graph did not record the resource's unnamed dynamic anchor")
	}
	resolver := NewLocalResolver(root)
	for _, frag := range []string{"#true", "#recursiveAnchor", "#node"} {
		if _, err := resolver.Resolve(frag); err == nil {
			t.Errorf("resolver resolved %q against a $recursiveAnchor", frag)
		}
	}
}
