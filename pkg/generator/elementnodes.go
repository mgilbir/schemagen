package generator

import (
	"strconv"
	"strings"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// An element held as decoded JSON -- a tuple position of a []any, an element a
// contains counts, an inferred array's items -- whose sub-schema has a
// generated type of its own was judged by marshalling the element and decoding
// the text into that type, whose Validate then judged it. The element is an any:
// decoded whole by encoding/json, or built in Go, and in either case there is
// no document span to decode the type from that could not go stale.
//
// So the element is judged as it is held instead: the type's schema is compiled
// for the runtime evaluator, which reads a decoded any -- and a value of this
// package's types or any other Go value, read as a tree (see jsonTreeView) --
// and judges it by the same rules it judges every schema it is given. Nothing
// is written out. Where the evaluator declines the schema, the position keeps
// the typed judgement it had.

// elementNode is the schema of the type typeName, which s describes, compiled
// for the evaluator and declared in this file; nil where the evaluator declines
// it, or where no type validates at all.
func (g *Generator) elementNode(typeName string, s *schema.Schema) *ElementNode {
	if s == nil || typeName == "" || !g.validationKeywordsEnabled() {
		return nil
	}
	if n, seen := g.elementNodes[typeName]; seen {
		return n
	}
	if g.elementNodes == nil {
		g.elementNodes = make(map[string]*ElementNode)
	}
	base := strings.ReplaceAll(typeName, ".", "_")
	b := &nodeBuilder{
		g:          g,
		allowed:    validatorKeywords,
		inlineRefs: true,
		stack:      map[*schema.Schema]int{},
		// Named after the type, which is unique in its package, as an
		// AnnotationSchemaDef's hoisted nodes are; see claimCarriedIdents.
		hoistPrefix: "_et" + base + "Node",
	}
	lit, nodes, ok := b.build(s)
	if !ok {
		g.elementNodes[typeName] = nil
		return nil
	}
	n := &ElementNode{
		TypeName: typeName,
		Var: g.names.claim("_et"+base, memberHolder(typeName, "element-node",
			"the schema of "+typeName+", compiled for judging an element held as decoded JSON")),
		Literal: lit,
		Nodes:   nodes,
	}
	for i, node := range nodes {
		if !g.names.claimExactly(node.Name, memberHolder(typeName, "element-node/"+strconv.Itoa(i), "a compiled node of the element schema of "+typeName)) {
			held, _ := g.names.holderOf(node.Name)
			g.noteNamingDefect(&NamingDefectError{Name: node.Name, Holder: held.what, Detail: "a compiled element schema node of " + typeName + " was named"})
		}
	}
	g.elementNodes[typeName] = n
	g.output.ElementNodes = append(g.output.ElementNodes, n)
	return n
}

// inferredArrayNodes compiles the item schemas an inferred array's items and
// tail types were generated from; see inferredArrayItems, which chose them.
func (g *Generator) inferredArrayNodes(s *schema.Schema, itemsTypeName, additionalItemsTypeName string) (items, additional *ElementNode) {
	if s == nil || s.Items == nil && s.AdditionalItems == nil {
		return nil, nil
	}
	if itemsTypeName != "" && s.Items != nil && s.Items.Schema != nil {
		items = g.elementNode(itemsTypeName, s.Items.Schema)
	}
	if additionalItemsTypeName != "" {
		switch {
		case len(s.PrefixItems) > 0 && g.supportsPrefixItems(s) && s.Items != nil && s.Items.Schema != nil:
			additional = g.elementNode(additionalItemsTypeName, s.Items.Schema)
		case s.AdditionalItems != nil && s.AdditionalItems.Schema != nil:
			additional = g.elementNode(additionalItemsTypeName, s.AdditionalItems.Schema)
		}
	}
	return items, additional
}
