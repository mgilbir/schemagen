package generator

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
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
// must hold a schema, or the first keyword whose value is malformed (see
// schema.Schema.MalformedKeywords), identified by a JSON Pointer into the
// document.
//
// A null the document wrote is recorded by the decode, at the entry, as a
// malformed value of the keyword that holds it (schema.KeywordError.Path), so
// this reports it where the document wrote it. A nil *element* of a []*Schema
// or a map[string]*Schema is what a null is in a tree built through the Go API,
// and only the container can tell it apart from an entry that was never there,
// so the walk checks for those too. Nil pointer *fields* ("not", "if",
// "contains", ...) are deliberately not checked: for those, absent and null
// both arrive as a nil pointer, so rejecting nil would reject every schema that
// simply omits the keyword.
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
// The location reported is where the document wrote the value, which is not
// the path this walk took to it: the walk sees the tree Normalize rewrote, where
// draft 3's "extends" is "allOf", "disallow" is "not", "dependencies" is
// "dependentSchemas" and "definitions" is mirrored as "$defs", and a path
// through that names a location in no document. So every node that has a
// location of its own (schema.Schema.SourceLocation, recorded when the
// document was read) is reported at it, and the path the walk took is used only
// for a node that has none -- one built through the Go API. See docLocator.
//
// rootPtr is the fallback for s itself: "#" for the document Generate was
// handed, and the $ref string for a node reached through one.
//
// verified carries across calls for the lifetime of one Generate, so a node is
// walked once no matter how many refs land on it. A node joins it only once its
// whole subtree is clear: marking on entry would let a walk that gave up
// part-way still record the nodes it had touched as checked, and a later ref
// onto one of those would then be waved through with the null still under it.
func checkNullSubschemas(s *schema.Schema, rootPtr string, home *schema.Schema, verified map[*schema.Schema]bool) error {
	w := nullWalk{locator: docLocator{home: home}, verified: verified, onPath: make(map[*schema.Schema]bool)}
	return w.check(s, strings.TrimSuffix(rootPtr, "/"))
}

// docLocator names where a document wrote a node.
type docLocator struct {
	// home is the root of the document Generate was handed, whose locations
	// are written as a bare fragment ("#/extends/1"), as the reports about the
	// input document always have been. A node in any other document is
	// written with that document's URI in front.
	home *schema.Schema
}

// name returns the location of n as a URI reference -- a fragment for the home
// document -- and whether n has one. The fragment is schema.PointerFragment,
// which schema.FragmentPointer reads back to the same tokens.
func (l docLocator) name(n *schema.Schema) (string, bool) {
	doc, tokens, ok := n.SourceLocation()
	if !ok {
		return "", false
	}
	fragment := schema.PointerFragment(tokens...)
	if doc == l.home {
		return fragment, true
	}
	if doc.BaseURI == nil {
		return "", false
	}
	u := *doc.BaseURI
	u.Fragment, u.RawFragment = "", ""
	return u.String() + fragment, true
}

// below extends a location by reference tokens.
func below(ptr string, tokens ...string) string {
	return ptr + strings.TrimPrefix(schema.PointerFragment(tokens...), "#")
}

type nullWalk struct {
	locator  docLocator
	verified map[*schema.Schema]bool
	// onPath keeps the walk finite. A tree parsed from JSON cannot contain a
	// cycle, but a Schema built through the Go API can, and "verified" is no
	// help there because it is only written on the way out.
	onPath map[*schema.Schema]bool
}

// at is where the document wrote s, which the walk reached at ptr: worked out
// only when there is something to report, since most walks find nothing, and
// ptr, the path the walk took, when s is a node no document wrote.
func (w nullWalk) at(s *schema.Schema, ptr string) string {
	if loc, ok := w.locator.name(s); ok {
		return loc
	}
	return ptr
}

// check walks s, which the walk reached at ptr.
func (w nullWalk) check(s *schema.Schema, ptr string) error {
	if s == nil || w.verified[s] || w.onPath[s] {
		return nil
	}
	w.onPath[s] = true
	defer delete(w.onPath, s)

	// A keyword whose value is malformed is the same mistake one level up: the
	// document wrote something the keyword cannot hold, and generating as though
	// it had written nothing would certify data against a document nobody
	// wrote. schema.Schema records these while decoding and the dialect pass
	// drops the records for keywords the node's dialect does not define, so
	// what is left here is exactly the set to refuse. See
	// schema.Schema.MalformedKeywords. The record says where below the keyword
	// the value is, so a null entry is reported at the entry.
	if bad := s.MalformedKeywords(); len(bad) > 0 {
		return fmt.Errorf("%s: %w", below(w.at(s, ptr), append([]string{bad[0].Keyword}, bad[0].Path...)...), bad[0].Err)
	}

	// A nil entry of a container is what a null becomes in a tree built
	// through the Go API; the decode records a null the document wrote as a
	// malformed value, above. Reported at the path the walk took, which is all
	// such a tree has.
	list := func(keyword string, subs []*schema.Schema) error {
		for i, sub := range subs {
			if sub == nil {
				return nullSubschemaError(below(w.at(s, ptr), keyword, strconv.Itoa(i)))
			}
			if err := w.check(sub, below(ptr, keyword, strconv.Itoa(i))); err != nil {
				return err
			}
		}
		return nil
	}
	byKey := func(keyword string, m map[string]*schema.Schema) error {
		for _, k := range sortedKeys(m) {
			if m[k] == nil {
				return nullSubschemaError(below(w.at(s, ptr), keyword, k))
			}
			if err := w.check(m[k], below(ptr, keyword, k)); err != nil {
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
		if err := w.check(f.sub, below(ptr, f.keyword)); err != nil {
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
		if err := w.check(s.AdditionalProperties.Schema, below(ptr, "additionalProperties")); err != nil {
			return err
		}
	}
	if s.AdditionalItems != nil {
		if err := w.check(s.AdditionalItems.Schema, below(ptr, "additionalItems")); err != nil {
			return err
		}
	}
	if s.Items != nil {
		if err := w.check(s.Items.Schema, below(ptr, "items")); err != nil {
			return err
		}
		if err := list("items", s.Items.Schemas); err != nil {
			return err
		}
	}
	w.verified[s] = true
	return nil
}

func nullSubschemaError(ptr string) error {
	return fmt.Errorf("%s: schema is null (a schema must be an object or boolean)", ptr)
}
