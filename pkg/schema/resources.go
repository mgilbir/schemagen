package schema

import (
	"net/url"
	"sort"
	"strings"
)

// Resource describes one JSON Schema resource in a schema graph. A resource is
// rooted at a schema node that establishes its own base URI/document scope.
type Resource struct {
	CanonicalURI string
	Draft        Draft
	Root         *Schema

	// Anchors indexes every node this resource declares a plain-name fragment
	// on -- every name under which "#name" reaches it from inside the resource
	// -- by that name. Which keywords declare one is AnchorNames and nothing
	// else; a node carrying two names appears under both.
	//
	// DynamicAnchors indexes the same resource's "$dynamicAnchor" declarations,
	// which is a different question: $dynamicAnchor also names a plain-name
	// fragment, so it appears in Anchors too, but what a $dynamicRef does with
	// it is a walk of the dynamic scope rather than a lookup. The empty key is
	// the resource's *unnamed* dynamic anchor -- "$recursiveAnchor": true, which
	// takes a boolean and so names nothing that could be in Anchors, and which
	// is what "$recursiveRef": "#" walks to.
	//
	// Both are scoped to this resource: a nested "$id" starts a resource of its
	// own, and an anchor written inside that one belongs to it and not here.
	// That subtree gets its own Resource in the graph.
	//
	// Anchors is what every "#name" reference is answered from: ResourceIndex
	// resolves a plain-name fragment by looking it up here, in the resource the
	// reference names, and nowhere else. The generator used to keep an index of
	// its own, of the *root* document's anchors, and consulted it before the
	// resource a reference was written in -- so "#name" in another document
	// meant the root's node whenever the root declared that name. There is no
	// second index now. DynamicAnchors is the same resource's declarations for
	// the dynamic-scope walk, which pkg/generator's resourceDynamicAnchor
	// performs by the same scoping rule.
	//
	// The contract is pinned from outside the traversal that builds it:
	// TestResourceIndexReachesEveryAnchorPosition finds the anchors in a fixture
	// by walking its raw JSON and requires these maps to hold exactly those.
	Anchors        map[string]*Schema
	DynamicAnchors map[string]*Schema

	// ambiguous holds the plain-name fragments more than one node of this
	// resource declares. Anchors keeps the first of them, in subSchemas order,
	// so the map is still a function of the document; a reference that names
	// one is refused (see AmbiguousAnchorError) rather than answered with it.
	ambiguous map[string]bool

	// document is the root of the document this resource was registered
	// with, set by ResourceIndex.
	document *Schema
}

// ResourceGraph indexes schema resources, anchors, and dynamic anchors by their
// canonical URI. It gives code generation and validation planning a document-aware
// view of a schema instead of only a tree of Schema nodes.
type ResourceGraph struct {
	Root      *Schema
	Resources map[string]*Resource
}

// BuildResourceGraph computes base/document scopes and indexes every resource in
// the schema tree. defaultDraft is used when a resource does not declare $schema.
//
// It is ResourceIndex.Graph over an index holding only root, retrieved from
// baseURI: one walk decides what a resource is and what it declares, for the
// graph and for every reference resolved, so the two cannot disagree. A graph
// has nowhere to report a document that claims one URI twice; it keeps the
// first such resource, in subSchemas order, and a ResourceIndex refuses the
// document (see DuplicateIdentifierError).
func BuildResourceGraph(root *Schema, baseURI *url.URL, defaultDraft Draft) *ResourceGraph {
	if root == nil {
		return &ResourceGraph{Resources: map[string]*Resource{}}
	}
	// No dialect is chosen from outside: defaultDraft is what a resource that
	// declares none is described as, not an override of one that does.
	x := NewResourceIndex(nil)
	// A refused document still has its base URIs computed, which is all Graph
	// reads; see above.
	_ = x.AddDocument(root, baseURI)
	return x.Graph(root, defaultDraft)
}

// SortedResourceURIs returns resource URIs in deterministic order.
func (g *ResourceGraph) SortedResourceURIs() []string {
	if g == nil || len(g.Resources) == 0 {
		return nil
	}
	keys := make([]string, 0, len(g.Resources))
	for k := range g.Resources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func canonicalResourceURI(s *Schema) string {
	if s != nil && s.BaseURI != nil {
		return strings.TrimSuffix(s.BaseURI.String(), "#")
	}
	return "#"
}

func resourceDraft(s *Schema, fallback Draft) Draft {
	if d := DetectDraft(s); d != DraftUnknown {
		return d
	}
	if fallback != DraftUnknown {
		return fallback
	}
	return DraftUnknown
}

func collectResourceAnchors(s *Schema, res *Resource, isRoot bool, dialect func(*Schema) Draft) {
	if s == nil || s.IsBooleanSchema() {
		return
	}
	if !isRoot && s.DocumentRoot == s {
		return
	}
	// AnchorNames is the one statement of which keywords declare a plain-name
	// fragment; see its doc comment for why this must not be a list kept here.
	//
	// The first declaration of a name, in subSchemas order, is the one kept, and
	// a second node declaring it marks the name ambiguous; see Resource.ambiguous.
	// The same node reached twice -- "definitions" and "$defs" are mirrored into
	// each other by Normalize -- is one declaration, not two.
	for _, name := range anchorNamesIn(s, dialect) {
		if have, ok := res.Anchors[name]; ok {
			if have != s {
				if res.ambiguous == nil {
					res.ambiguous = make(map[string]bool)
				}
				res.ambiguous[name] = true
			}
			continue
		}
		res.Anchors[name] = s
	}
	if _, ok := res.DynamicAnchors[s.DynamicAnchor]; s.DynamicAnchor != "" && !ok {
		res.DynamicAnchors[s.DynamicAnchor] = s
	}
	// "$recursiveAnchor" names nothing, so it is not in Anchors. It declares the
	// resource's unnamed dynamic anchor, which is what "$recursiveRef": "#"
	// walks to.
	if _, ok := res.DynamicAnchors[""]; s.RecursiveAnchor != nil && *s.RecursiveAnchor && !ok {
		res.DynamicAnchors[""] = s
	}
	for _, sub := range subSchemas(s) {
		collectResourceAnchors(sub, res, false, dialect)
	}
}
