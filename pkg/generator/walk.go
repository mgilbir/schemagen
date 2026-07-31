package generator

import (
	"encoding/json"
	"sort"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// WalkSchema calls visit for s and every subschema reachable from it, once per
// distinct node. Maps are traversed in sorted key order so callers that collect
// results get a deterministic sequence.
//
// Unknown/vendor keywords kept in Extensions as raw JSON are parsed and walked
// too, because a JSON Pointer $ref can resolve into them (see the resolver's
// handling of unrecognized path segments). A raw value that does not parse as a
// schema is skipped rather than reported: it may legitimately be arbitrary JSON.
func WalkSchema(s *schema.Schema, visit func(*schema.Schema)) {
	seen := make(map[*schema.Schema]bool)
	walkSchemaNode(s, visit, seen)
}

func walkSchemaNode(s *schema.Schema, visit func(*schema.Schema), seen map[*schema.Schema]bool) {
	if s == nil || seen[s] {
		return
	}
	seen[s] = true
	visit(s)

	list := func(subs []*schema.Schema) {
		for _, sub := range subs {
			walkSchemaNode(sub, visit, seen)
		}
	}
	byKey := func(m map[string]*schema.Schema) {
		if len(m) == 0 {
			return
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walkSchemaNode(m[k], visit, seen)
		}
	}

	list(s.AllOf)
	list(s.AnyOf)
	list(s.OneOf)
	list(s.PrefixItems)
	list(s.TypeSchemas)

	for _, sub := range []*schema.Schema{
		s.Not, s.Contains, s.If, s.Then, s.Else,
		s.ContentSchema, s.PropertyNames,
		s.UnevaluatedItems, s.UnevaluatedProperties,
	} {
		walkSchemaNode(sub, visit, seen)
	}

	byKey(s.Properties)
	byKey(s.PatternProperties)
	byKey(s.Definitions)
	byKey(s.Defs)
	byKey(s.DependentSchemas)

	if s.AdditionalProperties != nil {
		walkSchemaNode(s.AdditionalProperties.Schema, visit, seen)
	}
	if s.AdditionalItems != nil {
		walkSchemaNode(s.AdditionalItems.Schema, visit, seen)
	}
	if s.Items != nil {
		walkSchemaNode(s.Items.Schema, visit, seen)
		list(s.Items.Schemas)
	}

	// Vendor/unknown keywords: parse each raw value as a schema and walk it if
	// it plausibly is one. Sorted for determinism.
	if len(s.Extensions) > 0 {
		keys := make([]string, 0, len(s.Extensions))
		for k := range s.Extensions {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			var sub schema.Schema
			if err := json.Unmarshal(s.Extensions[k], &sub); err != nil {
				continue
			}
			walkSchemaNode(&sub, visit, seen)
		}
	}
}
