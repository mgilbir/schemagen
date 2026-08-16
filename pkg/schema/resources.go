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
	// These two are part of this package's API rather than of anything in this
	// repository: the generator maintains anchor indexes of its own, keyed by
	// the ref path it needs for naming a Go type, and pkg/generator's
	// resourceDynamicAnchor answers the DynamicAnchors question by walking the
	// resource on each call because it must also answer it for documents a
	// resolver fetched, which are not in this graph. Wiring that call site to
	// this index would change which node wins where a resource declares one name
	// twice -- undefined by the spec, but a change all the same -- and would go
	// silently blind on those fetched documents, so it is left alone
	// deliberately.
	//
	// An index nothing in the tree reads is an index that can rot, so the
	// contract is pinned from outside the traversal that builds it:
	// TestResourceIndexReachesEveryAnchorPosition finds the anchors in a fixture
	// by walking its raw JSON and requires these maps to hold exactly those.
	Anchors        map[string]*Schema
	DynamicAnchors map[string]*Schema
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
func BuildResourceGraph(root *Schema, baseURI *url.URL, defaultDraft Draft) *ResourceGraph {
	if root == nil {
		return &ResourceGraph{Resources: map[string]*Resource{}}
	}

	root.ComputeBaseURIs(baseURI, root)

	g := &ResourceGraph{
		Root:      root,
		Resources: make(map[string]*Resource),
	}
	g.collectResources(root, defaultDraft)
	return g
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

func (g *ResourceGraph) collectResources(s *Schema, defaultDraft Draft) {
	if s == nil || s.IsBooleanSchema() {
		return
	}

	if s.DocumentRoot == s || len(g.Resources) == 0 {
		uri := canonicalResourceURI(s)
		if _, exists := g.Resources[uri]; !exists {
			res := &Resource{
				CanonicalURI:   uri,
				Draft:          resourceDraft(s, defaultDraft),
				Root:           s,
				Anchors:        make(map[string]*Schema),
				DynamicAnchors: make(map[string]*Schema),
			}
			collectResourceAnchors(s, res, true)
			g.Resources[uri] = res
		}
	}

	for _, sub := range subSchemas(s) {
		g.collectResources(sub, defaultDraft)
	}
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

func collectResourceAnchors(s *Schema, res *Resource, isRoot bool) {
	if s == nil || s.IsBooleanSchema() {
		return
	}
	if !isRoot && s.DocumentRoot == s {
		return
	}
	// AnchorNames is the one statement of which keywords declare a plain-name
	// fragment; see its doc comment for why this must not be a list kept here.
	for _, name := range AnchorNames(s) {
		res.Anchors[name] = s
	}
	if s.DynamicAnchor != "" {
		res.DynamicAnchors[s.DynamicAnchor] = s
	}
	// "$recursiveAnchor" names nothing, so it is not in Anchors. It declares the
	// resource's unnamed dynamic anchor, which is what "$recursiveRef": "#"
	// walks to.
	if s.RecursiveAnchor != nil && *s.RecursiveAnchor {
		res.DynamicAnchors[""] = s
	}
	for _, sub := range subSchemas(s) {
		collectResourceAnchors(sub, res, false)
	}
}
