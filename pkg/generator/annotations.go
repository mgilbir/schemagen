package generator

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// annotationKeywords are the keywords the runtime annotation evaluator models.
// Anything outside this set makes a schema ineligible, so it keeps today's
// static checks instead of being evaluated with a keyword silently ignored.
var annotationKeywords = map[string]bool{
	"type": true, "const": true, "multipleOf": true, "minimum": true, "maximum": true,
	"prefixItems": true, "items": true, "additionalItems": true,
	"contains": true, "minContains": true, "maxContains": true,
	"allOf": true, "anyOf": true, "oneOf": true,
	"if": true, "then": true, "else": true,
	"unevaluatedItems": true,

	"$schema": true, "$id": true, "title": true, "description": true,
	"$comment": true, "default": true, "examples": true,
	"deprecated": true, "readOnly": true, "writeOnly": true,
}

// needsRuntimeAnnotations reports whether a schema's unevaluatedItems depends on
// which items sibling applicators evaluated for the actual value.
//
// Static analysis handles unevaluatedItems next to a fixed tuple. It cannot
// handle it next to in-place applicators, because the evaluated set then
// depends on which branches match the value in hand. Only that case is routed
// to the evaluator, so schemas that already work keep their generated shape.
func needsRuntimeAnnotations(s *schema.Schema) bool {
	if s == nil || s.UnevaluatedItems == nil {
		return false
	}
	if len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 || s.If != nil {
		return true
	}
	return false
}

// hasCousinUnevaluatedItems reports whether any in-place applicator subschema
// carries its own unevaluatedItems. Those are cousins of their siblings and see
// no annotations from them, which static merging gets wrong.
func hasCousinUnevaluatedItems(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	groups := [][]*schema.Schema{s.AllOf, s.AnyOf, s.OneOf}
	for _, g := range groups {
		for _, sub := range g {
			if sub != nil && sub.UnevaluatedItems != nil {
				return true
			}
		}
	}
	return false
}

// annotationNodeLiteral renders a schema as a Go _schemaNode composite literal,
// or reports ok=false when any part of the subtree is outside the model.
func annotationNodeLiteral(s *schema.Schema, indent int) (string, bool) {
	if s == nil {
		return "", false
	}
	pad := strings.Repeat("\t", indent)
	inner := strings.Repeat("\t", indent+1)

	if s.IsBooleanSchema() {
		return fmt.Sprintf("_schemaNode{Boolean: _boolPtr(%t)}", s.IsTrueSchema()), true
	}
	if !annotationKeywordsOnly(s) {
		return "", false
	}

	var fields []string
	add := func(f string) { fields = append(fields, inner+f) }

	if len(s.Type) == 1 {
		add(fmt.Sprintf("Type: %q,", s.Type[0]))
	} else if len(s.Type) > 1 {
		return "", false
	}
	if s.Const != nil {
		raw, err := json.Marshal(*s.Const)
		if err != nil {
			return "", false
		}
		add(fmt.Sprintf("Const: _strPtr(%q),", string(raw)))
	} else if s.ConstIsNull {
		add(`Const: _strPtr("null"),`)
	}
	if s.MultipleOf != nil {
		add(fmt.Sprintf("MultipleOf: _floatPtr(%s),", formatFloatLiteral(*s.MultipleOf)))
	}
	if s.Minimum != nil {
		add(fmt.Sprintf("Minimum: _floatPtr(%s),", formatFloatLiteral(*s.Minimum)))
	}
	if s.Maximum != nil {
		add(fmt.Sprintf("Maximum: _floatPtr(%s),", formatFloatLiteral(*s.Maximum)))
	}

	// prefixItems, or the pre-2020 tuple form of items.
	tuple := s.PrefixItems
	var itemsSchema *schema.Schema
	if s.Items != nil {
		if len(s.Items.Schemas) > 0 {
			if len(tuple) > 0 {
				return "", false // both forms at once is not modeled
			}
			tuple = s.Items.Schemas
		} else if s.Items.Schema != nil {
			itemsSchema = s.Items.Schema
		}
	}
	if len(tuple) > 0 {
		list, ok := annotationNodeList(tuple, indent+2)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("PrefixItems: %s,", list))
	}
	if itemsSchema != nil {
		lit, ok := annotationNodeLiteral(itemsSchema, indent+1)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("Items: _node(%s),", lit))
	}
	// additionalItems applies only alongside a tuple. On its own it is ignored:
	// it evaluates nothing and constrains nothing, so mapping it onto items
	// would both validate elements it must not and mark them evaluated.
	if s.AdditionalItems != nil && len(tuple) > 0 {
		if itemsSchema != nil {
			return "", false // items already claims the positions past the tuple
		}
		additional := s.AdditionalItems.AsSchema()
		if additional == nil {
			return "", false
		}
		lit, ok := annotationNodeLiteral(additional, indent+1)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("Items: _node(%s),", lit))
	}

	if s.Contains != nil {
		lit, ok := annotationNodeLiteral(s.Contains, indent+1)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("Contains: _node(%s),", lit))
		if s.MinContains != nil {
			add(fmt.Sprintf("MinContains: _intPtr(%d),", s.MinContains.Int()))
		}
		if s.MaxContains != nil {
			add(fmt.Sprintf("MaxContains: _intPtr(%d),", s.MaxContains.Int()))
		}
	}

	for _, group := range []struct {
		name string
		subs []*schema.Schema
	}{{"AllOf", s.AllOf}, {"AnyOf", s.AnyOf}, {"OneOf", s.OneOf}} {
		if len(group.subs) == 0 {
			continue
		}
		list, ok := annotationNodeList(group.subs, indent+2)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("%s: %s,", group.name, list))
	}

	for _, branch := range []struct {
		name string
		sub  *schema.Schema
	}{{"If", s.If}, {"Then", s.Then}, {"Else", s.Else}, {"UnevaluatedItems", s.UnevaluatedItems}} {
		if branch.sub == nil {
			continue
		}
		lit, ok := annotationNodeLiteral(branch.sub, indent+1)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("%s: _node(%s),", branch.name, lit))
	}

	if len(fields) == 0 {
		return "_schemaNode{}", true
	}
	sort.Strings(fields)
	return "_schemaNode{\n" + strings.Join(fields, "\n") + "\n" + pad + "}", true
}

func annotationNodeList(subs []*schema.Schema, indent int) (string, bool) {
	pad := strings.Repeat("\t", indent)
	closePad := strings.Repeat("\t", indent-1)
	parts := make([]string, 0, len(subs))
	for _, sub := range subs {
		lit, ok := annotationNodeLiteral(sub, indent)
		if !ok {
			return "", false
		}
		parts = append(parts, pad+lit+",")
	}
	return "[]_schemaNode{\n" + strings.Join(parts, "\n") + "\n" + closePad + "}", true
}

// annotationKeywordsOnly gates on the keywords actually present, read from the
// re-marshaled schema, so a keyword the parser learns later fails closed.
func annotationKeywordsOnly(s *schema.Schema) bool {
	if len(s.Extensions) > 0 {
		return false
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return false
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
		return false
	}
	for key := range present {
		if !annotationKeywords[key] {
			return false
		}
	}
	return true
}

func formatFloatLiteral(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// annotationSchemaDef builds an AnnotationSchemaDef when a schema's
// unevaluatedItems depends on runtime annotations and the whole subtree fits the
// evaluator's model. Returns nil to keep the existing static handling.
func (g *Generator) annotationSchemaDef(name string, s *schema.Schema) *AnnotationSchemaDef {
	if !g.validationKeywordsEnabled() {
		return nil
	}
	if !needsRuntimeAnnotations(s) && !hasCousinUnevaluatedItems(s) {
		return nil
	}
	lit, ok := annotationNodeLiteral(s, 0)
	if !ok {
		return nil
	}
	return &AnnotationSchemaDef{Name: name, Description: s.Description, NodeLiteral: lit}
}
