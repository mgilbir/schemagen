package generator

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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

// checkNullSubschemas reports the first JSON null sitting in a position that
// must hold a schema, identified by a JSON Pointer into the document.
//
// json.Unmarshal cannot flag these for us. A null inside a []*Schema or a
// map[string]*Schema lands as a nil *element*, and only the container can tell
// it apart from an entry that was never there -- which is why the check has to
// happen over the parsed tree rather than in an UnmarshalJSON hook. Nil pointer
// *fields* ("not", "if", "contains", ...) are deliberately not checked: for
// those, absent and null both arrive as a nil pointer, so rejecting nil would
// reject every schema that simply omits the keyword.
//
// The nulls are rejected, not dropped. {"allOf":[null]} is not a schema, and
// silently generating for {"allOf":[]} instead would hand back a type that
// certifies data against a document nobody wrote.
//
// Extensions are not descended into: a vendor keyword's value is arbitrary
// JSON, so a null there says nothing about schema validity. {"examples":[null]}
// is a perfectly ordinary document. That is also why the check has to be
// repeated when a $ref *resolves into* an extension: the keyword's raw JSON is
// only parsed as a schema at that point, which is after Generate checked the
// tree. Same for a document the resolver fetched -- it was never in the tree at
// all. See the call in resolveRefInContext.
//
// rootPtr is what the reported pointers are relative to: "#" for the document
// Generate was handed, and the $ref string for a document reached through one,
// so "#/examples/0" plus "/allOf/0" reads as the path a caller can follow.
//
// The pointer names the *normalized* document, which is what the generator
// works on. That only shows for "definitions": Normalize aliases it to "$defs",
// so a draft-07 document's null definition is reported under "$defs" -- the
// definition name, which is the part that locates the problem, is unchanged.
//
// verified carries across calls for the lifetime of one Generate, so a node is
// walked once no matter how many refs land on it. A node joins it only once its
// whole subtree is clear: marking on entry would let a walk that gave up
// part-way still record the nodes it had touched as checked, and a later ref
// onto one of those would then be waved through with the null still under it.
func checkNullSubschemas(s *schema.Schema, rootPtr string, verified map[*schema.Schema]bool) error {
	return checkNullSubschemasIn(s, strings.TrimSuffix(rootPtr, "/"), verified, make(map[*schema.Schema]bool))
}

func checkNullSubschemasIn(s *schema.Schema, ptr string, verified, onPath map[*schema.Schema]bool) error {
	// onPath keeps the walk finite. A tree parsed from JSON cannot contain a
	// cycle, but a Schema built through the Go API can, and "verified" is no
	// help there because it is only written on the way out.
	if s == nil || verified[s] || onPath[s] {
		return nil
	}
	onPath[s] = true
	defer delete(onPath, s)

	list := func(keyword string, subs []*schema.Schema) error {
		for i, sub := range subs {
			at := fmt.Sprintf("%s/%s/%d", ptr, keyword, i)
			if sub == nil {
				return nullSubschemaError(at)
			}
			if err := checkNullSubschemasIn(sub, at, verified, onPath); err != nil {
				return err
			}
		}
		return nil
	}
	byKey := func(keyword string, m map[string]*schema.Schema) error {
		for _, k := range sortedKeys(m) {
			at := ptr + "/" + keyword + "/" + escapeJSONPointerToken(k)
			if m[k] == nil {
				return nullSubschemaError(at)
			}
			if err := checkNullSubschemasIn(m[k], at, verified, onPath); err != nil {
				return err
			}
		}
		return nil
	}

	// Keyword order follows WalkSchema's so the two traversals stay comparable.
	for _, l := range []struct {
		keyword string
		subs    []*schema.Schema
	}{
		{"allOf", s.AllOf},
		{"anyOf", s.AnyOf},
		{"oneOf", s.OneOf},
		{"prefixItems", s.PrefixItems},
		{"type", s.TypeSchemas},
	} {
		if err := list(l.keyword, l.subs); err != nil {
			return err
		}
	}

	for _, f := range []struct {
		keyword string
		sub     *schema.Schema
	}{
		{"not", s.Not}, {"contains", s.Contains}, {"if", s.If},
		{"then", s.Then}, {"else", s.Else}, {"contentSchema", s.ContentSchema},
		{"propertyNames", s.PropertyNames},
		{"unevaluatedItems", s.UnevaluatedItems},
		{"unevaluatedProperties", s.UnevaluatedProperties},
	} {
		if err := checkNullSubschemasIn(f.sub, ptr+"/"+f.keyword, verified, onPath); err != nil {
			return err
		}
	}

	for _, m := range []struct {
		keyword string
		subs    map[string]*schema.Schema
	}{
		{"properties", s.Properties},
		{"patternProperties", s.PatternProperties},
		{"$defs", s.Defs},
		{"definitions", s.Definitions},
		{"dependentSchemas", s.DependentSchemas},
	} {
		if err := byKey(m.keyword, m.subs); err != nil {
			return err
		}
	}

	if s.AdditionalProperties != nil {
		if err := checkNullSubschemasIn(s.AdditionalProperties.Schema, ptr+"/additionalProperties", verified, onPath); err != nil {
			return err
		}
	}
	if s.AdditionalItems != nil {
		if err := checkNullSubschemasIn(s.AdditionalItems.Schema, ptr+"/additionalItems", verified, onPath); err != nil {
			return err
		}
	}
	if s.Items != nil {
		if err := checkNullSubschemasIn(s.Items.Schema, ptr+"/items", verified, onPath); err != nil {
			return err
		}
		if err := list("items", s.Items.Schemas); err != nil {
			return err
		}
	}
	verified[s] = true
	return nil
}

func nullSubschemaError(ptr string) error {
	return fmt.Errorf("%s: schema is null (a schema must be an object or boolean)", ptr)
}

// escapeJSONPointerToken applies RFC 6901 escaping so a property name
// containing "/" or "~" still yields a pointer that names it unambiguously.
func escapeJSONPointerToken(token string) string {
	token = strings.ReplaceAll(token, "~", "~0")
	return strings.ReplaceAll(token, "/", "~1")
}
