package schema

import (
	"net/url"
	"sort"
	"strings"
)

// Resource describes one JSON Schema resource in a schema graph. A resource is
// rooted at a schema node that establishes its own base URI/document scope.
type Resource struct {
	CanonicalURI   string
	Draft          Draft
	Root           *Schema
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

	for _, sub := range schemaChildren(s) {
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
	if s.Anchor != "" {
		res.Anchors[s.Anchor] = s
	}
	if s.DynamicAnchor != "" {
		res.DynamicAnchors[s.DynamicAnchor] = s
		res.Anchors[s.DynamicAnchor] = s
	}
	if s.RecursiveAnchor != nil && *s.RecursiveAnchor {
		res.DynamicAnchors[""] = s
	}
	for _, sub := range schemaChildren(s) {
		collectResourceAnchors(sub, res, false)
	}
}

func schemaChildren(s *Schema) []*Schema {
	if s == nil {
		return nil
	}
	var out []*Schema
	for _, key := range sortedSchemaKeys(s.Properties) {
		out = append(out, s.Properties[key])
	}
	out = append(out, s.TypeSchemas...)
	for _, key := range sortedSchemaKeys(s.PatternProperties) {
		out = append(out, s.PatternProperties[key])
	}
	for _, key := range sortedSchemaKeys(s.Defs) {
		out = append(out, s.Defs[key])
	}
	for _, key := range sortedSchemaKeys(s.Definitions) {
		out = append(out, s.Definitions[key])
	}
	out = append(out, s.AllOf...)
	out = append(out, s.AnyOf...)
	out = append(out, s.OneOf...)
	if s.Not != nil {
		out = append(out, s.Not)
	}
	if s.If != nil {
		out = append(out, s.If)
	}
	if s.Then != nil {
		out = append(out, s.Then)
	}
	if s.Else != nil {
		out = append(out, s.Else)
	}
	if s.Items != nil {
		if s.Items.Schema != nil {
			out = append(out, s.Items.Schema)
		}
		out = append(out, s.Items.Schemas...)
	}
	out = append(out, s.PrefixItems...)
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		out = append(out, s.AdditionalProperties.Schema)
	}
	if s.AdditionalItems != nil && s.AdditionalItems.Schema != nil {
		out = append(out, s.AdditionalItems.Schema)
	}
	if s.Contains != nil {
		out = append(out, s.Contains)
	}
	for _, key := range sortedSchemaKeys(s.DependentSchemas) {
		out = append(out, s.DependentSchemas[key])
	}
	if s.PropertyNames != nil {
		out = append(out, s.PropertyNames)
	}
	if s.UnevaluatedItems != nil {
		out = append(out, s.UnevaluatedItems)
	}
	if s.UnevaluatedProperties != nil {
		out = append(out, s.UnevaluatedProperties)
	}
	if s.ContentSchema != nil {
		out = append(out, s.ContentSchema)
	}
	return out
}

func sortedSchemaKeys(m map[string]*Schema) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
