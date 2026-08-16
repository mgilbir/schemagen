package schema

import (
	"encoding/json"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Resource.Anchors and Resource.DynamicAnchors are read by nothing in this
// repository -- the generator keeps anchor indexes of its own, keyed by the ref
// path it needs to name a Go type -- so they are API for consumers of the
// exported type and nothing else. An index nobody in the tree reads is an index
// that can rot: every keyword position could quietly leave the traversal that
// fills it and no build, no golden and no compliance run would say a word.
//
// So the contract is pinned here, and pinned from *outside* the traversal. The
// expectation is computed by reading the fixture's raw JSON with a walk that
// recurses into every object and array it meets and knows nothing about which
// keywords hold a schema. A position dropped from subSchemas is then a name the
// JSON declares and the index does not have, which is a failure with the name in
// it rather than an index that has silently become a subset of itself.
//
// #326 settled which *keywords* declare a plain-name fragment (AnchorNames).
// What was left, and what these tests hold, is which *positions* get looked in.

// The fixture states no $schema on purpose: a node whose dialect is unknown
// keeps every keyword (see dropKeywordsOutsideDialect), which is what lets one
// document carry draft 3's schema-valued "type" entries, draft 7's array-form
// "items" and "additionalItems", and 2020-12's "prefixItems" and the
// "unevaluated" pair at once. Gated per dialect they could not share a document,
// and the point here is that every position is covered by one walk.
//
// Each anchored node carries a "title" equal to the name that reaches it, so the
// assertion can pin the node the index holds and not merely the key.
const resourceIndexFixture = `{
	"$id": "https://ex.test/root.json",
	"$recursiveAnchor": true,
	"title": "root",

	"type": ["object", {"$anchor": "atTypeSchemas", "title": "atTypeSchemas"}],
	"properties": {
		"p": {"$anchor": "atProperties", "title": "atProperties"},
		"arrSingle": {
			"items": {"$anchor": "atItemsSchema", "title": "atItemsSchema"}
		},
		"arrTuple": {
			"items": [{"$anchor": "atItemsArray", "title": "atItemsArray"}],
			"additionalItems": {"$anchor": "atAdditionalItems", "title": "atAdditionalItems"},
			"prefixItems": [{"$anchor": "atPrefixItems", "title": "atPrefixItems"}],
			"contains": {"$anchor": "atContains", "title": "atContains"},
			"unevaluatedItems": {"$anchor": "atUnevaluatedItems", "title": "atUnevaluatedItems"}
		}
	},
	"patternProperties": {"^x": {"$anchor": "atPatternProperties", "title": "atPatternProperties"}},
	"additionalProperties": {"$anchor": "atAdditionalProperties", "title": "atAdditionalProperties"},
	"unevaluatedProperties": {"$anchor": "atUnevaluatedProperties", "title": "atUnevaluatedProperties"},
	"propertyNames": {"$anchor": "atPropertyNames", "title": "atPropertyNames"},
	"dependentSchemas": {"k": {"$anchor": "atDependentSchemas", "title": "atDependentSchemas"}},
	"contentSchema": {"$anchor": "atContentSchema", "title": "atContentSchema"},

	"allOf": [{"$anchor": "atAllOf", "title": "atAllOf"}],
	"anyOf": [{"$anchor": "atAnyOf", "title": "atAnyOf"}],
	"oneOf": [{"$anchor": "atOneOf", "title": "atOneOf"}],
	"not": {"$anchor": "atNot", "title": "atNot"},
	"if": {"$anchor": "atIf", "title": "atIf"},
	"then": {"$anchor": "atThen", "title": "atThen"},
	"else": {"$anchor": "atElse", "title": "atElse"},

	"$defs": {
		"d": {"$anchor": "atDefs", "title": "atDefs"},
		"dyn": {"$dynamicAnchor": "atDynamicAnchor", "title": "atDynamicAnchor"},
		"both": {"$anchor": "atBothPlain", "$dynamicAnchor": "atBothDynamic", "title": "atBoth"},
		"legacy": {"$id": "#atLegacyIDFragment", "title": "atLegacyIDFragment"},
		"nested": {
			"$id": "nested.json",
			"$anchor": "atNestedResourceRoot",
			"title": "atNestedResourceRoot",
			"properties": {"q": {"$anchor": "atInsideNestedResource", "title": "atInsideNestedResource"}}
		}
	},
	"definitions": {"dd": {"$anchor": "atDefinitions", "title": "atDefinitions"}}
}`

const resourceIndexBase = "https://ex.test/root.json"

// Every plain name the fixture declares, written out so that a fixture edit that
// stops covering a position fails here -- with the name of the position -- and
// not by quietly agreeing with a traversal that no longer looks there. The
// twenty-three subSchemas positions each contribute one; the last four are the
// keyword spellings and the resource boundary.
var resourceIndexNames = []string{
	"atAdditionalItems",
	"atAdditionalProperties",
	"atAllOf",
	"atAnyOf",
	"atBothDynamic",
	"atBothPlain",
	"atContains",
	"atContentSchema",
	"atDefinitions",
	"atDefs",
	"atDependentSchemas",
	"atDynamicAnchor",
	"atElse",
	"atIf",
	"atInsideNestedResource",
	"atItemsArray",
	"atItemsSchema",
	"atLegacyIDFragment",
	"atNestedResourceRoot",
	"atNot",
	"atOneOf",
	"atPatternProperties",
	"atPrefixItems",
	"atProperties",
	"atPropertyNames",
	"atThen",
	"atTypeSchemas",
	"atUnevaluatedItems",
	"atUnevaluatedProperties",
}

// rawAnchor is one plain-name fragment, as read from the fixture's JSON rather
// than from anything that knows a schema keyword from a hole in the ground.
type rawAnchor struct {
	name     string
	title    string // the "title" of the declaring object, which pins the node
	resource string // canonical URI of the resource that must index it
	dynamic  bool   // declared by $dynamicAnchor, so DynamicAnchors must hold it too
}

// anchorsInRawJSON reads src as plain JSON and reports every plain-name fragment
// in it, together with the resource that owns it.
//
// It descends into every object and every array without consulting any list of
// keywords, which is the whole point: it cannot go blind in the same place the
// code under test goes blind. The fixture is written so that this over-broad
// reading is exact -- no anchor is written anywhere that is not a schema
// position, and no "enum", "const" or unknown keyword carries an object.
//
// The scope rule is restated here rather than borrowed: an "$id" that is not a
// plain-name fragment starts a resource, resolved against the base in force, and
// an anchor on that node belongs to the resource it starts rather than to the
// one it sits in.
func anchorsInRawJSON(t *testing.T, src, baseURI string) []rawAnchor {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	base, err := url.Parse(baseURI)
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}

	var out []rawAnchor
	var walk func(node any, base *url.URL)
	walk = func(node any, base *url.URL) {
		switch n := node.(type) {
		case map[string]any:
			scope := base
			id, _ := n["$id"].(string)
			if id != "" && !strings.HasPrefix(id, "#") {
				if u, err := url.Parse(id); err == nil {
					scope = base.ResolveReference(u)
				}
			}
			uri := strings.TrimSuffix(scope.String(), "#")
			title, _ := n["title"].(string)
			add := func(name string, dynamic bool) {
				if name != "" {
					out = append(out, rawAnchor{name: name, title: title, resource: uri, dynamic: dynamic})
				}
			}
			if a, ok := n["$anchor"].(string); ok {
				add(a, false)
			}
			if a, ok := n["$dynamicAnchor"].(string); ok {
				add(a, true)
			}
			if strings.HasPrefix(id, "#") && len(id) > 1 && !strings.HasPrefix(id, "#/") {
				add(id[1:], false)
			}
			keys := make([]string, 0, len(n))
			for k := range n {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(n[k], scope)
			}
		case []any:
			for _, elem := range n {
				walk(elem, base)
			}
		}
	}
	walk(doc, base)
	return out
}

func buildResourceIndexFixture(t *testing.T) *ResourceGraph {
	t.Helper()
	var s Schema
	if err := json.Unmarshal([]byte(resourceIndexFixture), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	base, err := url.Parse(resourceIndexBase)
	if err != nil {
		t.Fatal(err)
	}
	return BuildResourceGraph(&s, base, DraftUnknown)
}

// The index must hold exactly the anchors the document declares, in exactly the
// resource that owns each -- no fewer, which is a position the traversal stopped
// looking in, and no more, which is an anchor credited across a resource
// boundary it should not have crossed.
func TestResourceIndexReachesEveryAnchorPosition(t *testing.T) {
	found := anchorsInRawJSON(t, resourceIndexFixture, resourceIndexBase)

	// The fixture still covers what it claims to. Without this a position
	// deleted from the fixture would take the expectation with it and leave the
	// comparison below trivially true -- which is how a guard stops guarding.
	gotNames := make([]string, 0, len(found))
	for _, a := range found {
		gotNames = append(gotNames, a.name)
	}
	sort.Strings(gotNames)
	if !reflect.DeepEqual(gotNames, resourceIndexNames) {
		t.Fatalf("the fixture declares %v,\nwant %v -- a position it no longer covers is one this test no longer holds",
			gotNames, resourceIndexNames)
	}

	graph := buildResourceIndexFixture(t)

	wantPlain := map[string]map[string]string{}   // resource → name → title
	wantDynamic := map[string]map[string]string{} // resource → name → title
	for _, a := range found {
		if wantPlain[a.resource] == nil {
			wantPlain[a.resource] = map[string]string{}
			wantDynamic[a.resource] = map[string]string{}
		}
		wantPlain[a.resource][a.name] = a.title
		if a.dynamic {
			wantDynamic[a.resource][a.name] = a.title
		}
	}
	// The document root says "$recursiveAnchor": true, which names nothing and
	// so is not in Anchors; it is the resource's unnamed dynamic anchor.
	wantDynamic[resourceIndexBase][""] = "root"

	for uri := range wantPlain {
		res := graph.Resources[uri]
		if res == nil {
			t.Fatalf("no resource %q in the graph; have %v", uri, graph.SortedResourceURIs())
		}
		checkAnchorIndex(t, uri+" Anchors", res.Anchors, wantPlain[uri])
		checkAnchorIndex(t, uri+" DynamicAnchors", res.DynamicAnchors, wantDynamic[uri])
	}
	for _, uri := range graph.SortedResourceURIs() {
		if _, expected := wantPlain[uri]; !expected {
			t.Errorf("the graph holds a resource %q the document does not declare", uri)
		}
	}
}

func checkAnchorIndex(t *testing.T, what string, got map[string]*Schema, want map[string]string) {
	t.Helper()
	gotKeys := make([]string, 0, len(got))
	for k := range got {
		gotKeys = append(gotKeys, k)
	}
	wantKeys := make([]string, 0, len(want))
	for k := range want {
		wantKeys = append(wantKeys, k)
	}
	sort.Strings(gotKeys)
	sort.Strings(wantKeys)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Errorf("%s indexes %v, want %v", what, gotKeys, wantKeys)
		return
	}
	for name, title := range want {
		if got[name] == nil {
			t.Errorf("%s[%q] is nil", what, name)
			continue
		}
		if got[name].Title != title {
			t.Errorf("%s[%q] is the node titled %q, want the one titled %q",
				what, name, got[name].Title, title)
		}
	}
}

// The resolver searches a document another document $refs into, and it now
// shares subSchemas with the index above. Sharing is the fix; this is what makes
// it visible if the sharing is ever undone, because a name reachable by one and
// not the other is issue #307 in its other half -- the same schema answering
// within a document and refusing across one.
func TestResolverFindsEveryAnchorTheResourceIndexHolds(t *testing.T) {
	graph := buildResourceIndexFixture(t)
	res := graph.Resources[resourceIndexBase]
	if res == nil {
		t.Fatalf("no root resource; have %v", graph.SortedResourceURIs())
	}
	resolver := NewLocalResolver(graph.Root)
	for name, node := range res.Anchors {
		found, err := resolver.Resolve("#" + name)
		if err != nil {
			t.Errorf("resolver: #%s (%s): %v", name, node.Title, err)
			continue
		}
		if found != node {
			t.Errorf("resolver: #%s reached the node titled %q, the index holds the one titled %q",
				name, found.Title, node.Title)
		}
	}
	// The nested resource's anchors are its own. The resolver stops at the $id
	// boundary, so it must not reach them from the document root either.
	for _, name := range []string{"atNestedResourceRoot", "atInsideNestedResource"} {
		if _, err := resolver.Resolve("#" + name); err == nil {
			t.Errorf("the resolver reached %q across an $id boundary", name)
		}
	}
}

// eachChild is the third enumeration of the same positions -- Normalize's, which
// needs no order and pays no sort for one -- and it is allowed to stay separate
// for that reason alone. What it is not allowed to do is enumerate a different
// set: a keyword added to it and not to subSchemas leaves every anchor under
// that keyword out of the index, and a keyword added the other way round leaves
// it un-normalized. Nothing else in the tree compares the two.
func TestEachChildAndSubSchemasVisitTheSameNodes(t *testing.T) {
	var s Schema
	if err := json.Unmarshal([]byte(resourceIndexFixture), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	var check func(n *Schema, seen map[*Schema]bool)
	check = func(n *Schema, seen map[*Schema]bool) {
		if n == nil || n.IsBooleanSchema() || seen[n] {
			return
		}
		seen[n] = true

		viaSubSchemas := map[*Schema]int{}
		for _, sub := range subSchemas(n) {
			viaSubSchemas[sub]++
		}
		viaEachChild := map[*Schema]int{}
		n.eachChild(func(sub *Schema) { viaEachChild[sub]++ })

		for sub, count := range viaSubSchemas {
			if viaEachChild[sub] != count {
				t.Errorf("node %q: subSchemas visits a child %d time(s) that eachChild visits %d time(s) (child titled %q)",
					n.Title, count, viaEachChild[sub], sub.Title)
			}
		}
		for sub, count := range viaEachChild {
			if viaSubSchemas[sub] != count {
				t.Errorf("node %q: eachChild visits a child %d time(s) that subSchemas visits %d time(s) (child titled %q)",
					n.Title, count, viaSubSchemas[sub], sub.Title)
			}
		}

		for sub := range viaSubSchemas {
			check(sub, seen)
		}
		for sub := range viaEachChild {
			check(sub, seen)
		}
	}
	check(&s, map[*Schema]bool{})
}
