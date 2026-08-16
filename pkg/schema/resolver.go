package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SchemaResolver resolves $ref references to schema objects.
// Implementations handle different URI schemes (local fragments, file paths, HTTP, etc.).
type SchemaResolver interface {
	// ResolveSchema resolves a $ref string to a Schema.
	// The baseURI provides the context for resolving relative references.
	// For fragment-only refs like "#/$defs/Foo", baseURI is the document's own URI.
	ResolveSchema(ref string, baseURI *url.URL) (*Schema, error)
}

// ---------- LocalResolver (JSON Pointer within a single document) ----------

// LocalResolver resolves fragment-only $ref references (#, #/$defs/Foo, #/properties/bar, etc.)
// within a single root JSON Schema document using full JSON Pointer traversal.
type LocalResolver struct {
	root  *Schema
	cache map[string]*Schema
}

// NewLocalResolver creates a LocalResolver rooted at the given schema.
func NewLocalResolver(root *Schema) *LocalResolver {
	return &LocalResolver{
		root:  root,
		cache: make(map[string]*Schema),
	}
}

// NewResolver is a backward-compatible alias for NewLocalResolver.
func NewResolver(root *Schema) *LocalResolver {
	return NewLocalResolver(root)
}

// ResolveSchema implements SchemaResolver for fragment-only refs.
// The baseURI parameter is ignored; resolution is always within the root document.
func (r *LocalResolver) ResolveSchema(ref string, baseURI *url.URL) (*Schema, error) {
	// Only handle fragment-only refs.
	if !strings.HasPrefix(ref, "#") {
		return nil, fmt.Errorf("LocalResolver only handles fragment refs (got %q)", ref)
	}
	return r.Resolve(ref)
}

// Resolve resolves a fragment-only $ref within the root document.
// This is the backward-compatible single-arg method.
func (r *LocalResolver) Resolve(ref string) (*Schema, error) {
	return r.ResolveLocal(ref)
}

// ResolveLocal resolves a fragment-only $ref within the root document.
// This is the direct-call method (without baseURI) for backward compatibility.
func (r *LocalResolver) ResolveLocal(ref string) (*Schema, error) {
	if cached, ok := r.cache[ref]; ok {
		return cached, nil
	}

	resolved, err := r.resolve(ref)
	if err != nil {
		return nil, err
	}

	r.cache[ref] = resolved
	return resolved, nil
}

func (r *LocalResolver) resolve(ref string) (*Schema, error) {
	if !strings.HasPrefix(ref, "#") {
		return nil, fmt.Errorf("unsupported ref format (only local refs starting with '#' are supported): %s", ref)
	}

	// "#" refers to the root.
	if ref == "#" {
		return r.root, nil
	}

	// Plain-name anchor: "#foo" (no slash after #)
	if !strings.HasPrefix(ref, "#/") {
		anchor := ref[1:] // strip leading "#"
		return r.findAnchor(r.root, anchor)
	}

	// JSON Pointer: "#/path/to/thing"
	path := strings.TrimPrefix(ref, "#/")
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = UnescapePointerToken(p)
	}

	return r.walkPath(r.root, parts, ref)
}

// plainNameFragment returns the anchor name an id declares when it is a
// plain-name fragment such as "#foo", or "" for anything else.
//
// Drafts before 2019-09 had no "$anchor": a location-independent identifier was
// written as {"id": "#foo"}, which names the subschema *without* changing the
// base URI. "#" is the document root and "#/..." is a JSON Pointer, so neither
// is an anchor; a non-fragment id like "http://example.com/x.json" changes
// scope rather than naming a node.
func plainNameFragment(id string) string {
	if !strings.HasPrefix(id, "#") {
		return ""
	}
	name := id[1:]
	if name == "" || strings.HasPrefix(name, "/") {
		return ""
	}
	return name
}

// AnchorNames returns every plain-name fragment under which s can be reached as
// "#name" from within its own resource, most specific spelling first.
//
// This is the single statement of which keywords declare such a name, and every
// index of anchors in this repository is required to consult it rather than
// keep a list of its own. Three separate indexes had grown three lists: the
// resolver's (here, for a document another document $refs into), the resource
// graph's in resources.go, and the generator's indexAnchors. They disagreed
// about "$dynamicAnchor", so one and the same schema set resolved when spelled
// as an embedded resource and was refused when spelled as a second input
// document (issue #307).
//
// The spellings:
//
//   - "$anchor", 2019-09 and later.
//   - "$dynamicAnchor", 2020-12 and later. §8.2.2 says it "behaves like $anchor
//     ... in that it creates a plain name fragment", so a *plain* $ref naming
//     one resolves by ordinary lookup. That is a separate question from what
//     $dynamicRef does with it, which is a dynamic-scope walk over the same
//     name and stays in pkg/generator.
//   - The pre-2019-09 spelling {"id": "#name"}, which names a subschema without
//     changing the base URI. Normalize copies a legacy "id" into ID, so both
//     fields are consulted.
//
// "$recursiveAnchor" is deliberately not here. 2019-09 gives it a boolean, not
// a name -- {"$recursiveAnchor": true} -- so it declares no plain-name fragment
// that any "#name" could spell, and there is nothing for a plain $ref to name.
// What it declares is the *unnamed* dynamic anchor "$recursiveRef": "#" walks
// to, which resources.go records under the empty key and which never reaches
// this function.
//
// Multiple names on one node are possible and all of them count: a node may
// carry "$anchor": "a" and "$dynamicAnchor": "b" and answer to both.
func AnchorNames(s *Schema) []string {
	if s == nil {
		return nil
	}
	var names []string
	add := func(name string) {
		if name == "" {
			return
		}
		for _, have := range names {
			if have == name {
				return
			}
		}
		names = append(names, name)
	}
	add(s.Anchor)
	add(s.DynamicAnchor)
	add(plainNameFragment(s.ID))
	add(plainNameFragment(s.LegacyID))
	return names
}

// hasAnchorName reports whether s answers to the plain-name fragment anchor.
func hasAnchorName(s *Schema, anchor string) bool {
	for _, name := range AnchorNames(s) {
		if name == anchor {
			return true
		}
	}
	return false
}

// changesScope reports whether a subschema's id starts a new document scope.
// Only a scope-changing id hides the subtree from the parent's anchor search —
// a plain-name fragment id names a node inside the *current* scope, so the
// subtree must still be searched.
func changesScope(s *Schema) bool {
	return s.ID != "" && plainNameFragment(s.ID) == ""
}

// findAnchor searches the schema tree for a node answering to the given
// plain-name fragment, under every spelling AnchorNames recognises.
//
// Which spellings those are matters most for documents reached through a
// SchemaResolver: the generator's own anchor index covers the root document,
// but a resolver-fetched document is not in that index and is searched here
// instead, so anything this search does not know about is unreachable across
// documents while remaining reachable within one (issue #307).
func (r *LocalResolver) findAnchor(s *Schema, anchor string) (*Schema, error) {
	if s == nil {
		return nil, fmt.Errorf("anchor %q not found", anchor)
	}
	if hasAnchorName(s, anchor) {
		return s, nil
	}
	// Search in all sub-schema locations, but skip sub-schemas that start their
	// own document scope — their anchors belong to that scope, not the parent's.
	for _, sub := range subSchemas(s) {
		if changesScope(sub) {
			continue
		}
		if found, err := r.findAnchor(sub, anchor); err == nil {
			return found, nil
		}
	}
	return nil, fmt.Errorf("anchor %q not found", anchor)
}

// subSchemas returns every immediate subschema of s: every position in which
// this package will look for an anchor, a nested resource, or anything else
// that is a schema in its own right.
//
// This is the single statement of that list, for the same reason AnchorNames is
// the single statement of which keywords declare a plain-name fragment, and it
// is the other half of the same defect. Which *keywords* name a node was
// answered in three places and the three disagreed (issue #307, settled in
// #326); which *positions* hold a node was answered in two -- here, for the
// resolver that searches a document another document $refs into, and again in
// resources.go for the resource graph -- and nothing checked that those two
// agreed either. They enumerated the same twenty-three positions in a different
// order, which is a coincidence rather than a guarantee: a keyword added to one
// copy would have left the other blind to every anchor written under it, and the
// resource graph would have gone on reporting a complete index that quietly was
// not one. TestResourceIndexReachesEveryAnchorPosition names every position and
// fails when one leaves this function.
//
// Map-valued fields (Properties, Defs, etc.) are iterated in sorted key order so
// that anchor resolution is deterministic regardless of Go map iteration order.
//
// Deliberately *not* unified with pkg/generator's allSubSchemas, which answers a
// different question -- which subschemas produce a Go type -- and leaves out
// propertyNames and contentSchema on purpose.
func subSchemas(s *Schema) []*Schema {
	if s == nil {
		return nil
	}
	var subs []*Schema
	for _, k := range sortedKeys(s.Properties) {
		subs = append(subs, s.Properties[k])
	}
	subs = append(subs, s.TypeSchemas...)
	for _, k := range sortedKeys(s.Defs) {
		subs = append(subs, s.Defs[k])
	}
	for _, k := range sortedKeys(s.Definitions) {
		subs = append(subs, s.Definitions[k])
	}
	subs = append(subs, s.AllOf...)
	subs = append(subs, s.AnyOf...)
	subs = append(subs, s.OneOf...)
	if s.Not != nil {
		subs = append(subs, s.Not)
	}
	if s.If != nil {
		subs = append(subs, s.If)
	}
	if s.Then != nil {
		subs = append(subs, s.Then)
	}
	if s.Else != nil {
		subs = append(subs, s.Else)
	}
	if s.Items != nil {
		if s.Items.Schema != nil {
			subs = append(subs, s.Items.Schema)
		}
		subs = append(subs, s.Items.Schemas...)
	}
	subs = append(subs, s.PrefixItems...)
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		subs = append(subs, s.AdditionalProperties.Schema)
	}
	if s.AdditionalItems != nil && s.AdditionalItems.Schema != nil {
		subs = append(subs, s.AdditionalItems.Schema)
	}
	if s.Contains != nil {
		subs = append(subs, s.Contains)
	}
	for _, k := range sortedKeys(s.PatternProperties) {
		subs = append(subs, s.PatternProperties[k])
	}
	for _, k := range sortedKeys(s.DependentSchemas) {
		subs = append(subs, s.DependentSchemas[k])
	}
	if s.PropertyNames != nil {
		subs = append(subs, s.PropertyNames)
	}
	if s.UnevaluatedItems != nil {
		subs = append(subs, s.UnevaluatedItems)
	}
	if s.UnevaluatedProperties != nil {
		subs = append(subs, s.UnevaluatedProperties)
	}
	if s.ContentSchema != nil {
		subs = append(subs, s.ContentSchema)
	}
	return subs
}

// sortedKeys returns the keys of a map[string]*Schema in sorted order.
func sortedKeys(m map[string]*Schema) []string {
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

func (r *LocalResolver) walkPath(current *Schema, parts []string, originalRef string) (*Schema, error) {
	if len(parts) == 0 {
		return current, nil
	}
	if current == nil {
		return nil, fmt.Errorf("cannot traverse nil schema at %q in: %s", parts[0], originalRef)
	}

	key := parts[0]
	rest := parts[1:]

	switch key {
	case "$defs":
		if len(rest) == 0 {
			return nil, fmt.Errorf("incomplete ref, expected name after $defs: %s", originalRef)
		}
		name := rest[0]
		if current.Defs == nil {
			return nil, fmt.Errorf("schema has no $defs, cannot resolve: %s", originalRef)
		}
		s, ok := current.Defs[name]
		if !ok {
			return nil, fmt.Errorf("$defs does not contain %q: %s", name, originalRef)
		}
		return r.walkPath(s, rest[1:], originalRef)

	case "definitions":
		if len(rest) == 0 {
			return nil, fmt.Errorf("incomplete ref, expected name after definitions: %s", originalRef)
		}
		name := rest[0]
		if current.Definitions == nil {
			return nil, fmt.Errorf("schema has no definitions, cannot resolve: %s", originalRef)
		}
		s, ok := current.Definitions[name]
		if !ok {
			return nil, fmt.Errorf("definitions does not contain %q: %s", name, originalRef)
		}
		return r.walkPath(s, rest[1:], originalRef)

	case "properties":
		if len(rest) == 0 {
			return nil, fmt.Errorf("incomplete ref, expected name after properties: %s", originalRef)
		}
		name := rest[0]
		if current.Properties == nil {
			return nil, fmt.Errorf("schema has no properties, cannot resolve: %s", originalRef)
		}
		s, ok := current.Properties[name]
		if !ok {
			return nil, fmt.Errorf("properties does not contain %q: %s", name, originalRef)
		}
		return r.walkPath(s, rest[1:], originalRef)

	case "items":
		if current.Items == nil {
			return nil, fmt.Errorf("schema has no items: %s", originalRef)
		}
		if current.Items.Schema != nil {
			return r.walkPath(current.Items.Schema, rest, originalRef)
		}
		// Array form: items/0, items/1, ...
		if len(rest) == 0 {
			return nil, fmt.Errorf("incomplete ref, expected index after items: %s", originalRef)
		}
		idx, err := parseIndex(rest[0])
		if err != nil {
			return nil, fmt.Errorf("invalid items index %q: %s", rest[0], originalRef)
		}
		if idx >= len(current.Items.Schemas) {
			return nil, fmt.Errorf("items index %d out of range: %s", idx, originalRef)
		}
		return r.walkPath(current.Items.Schemas[idx], rest[1:], originalRef)

	case "prefixItems":
		if len(rest) == 0 {
			return nil, fmt.Errorf("incomplete ref, expected index after prefixItems: %s", originalRef)
		}
		idx, err := parseIndex(rest[0])
		if err != nil {
			return nil, fmt.Errorf("invalid prefixItems index %q: %s", rest[0], originalRef)
		}
		if idx >= len(current.PrefixItems) {
			return nil, fmt.Errorf("prefixItems index %d out of range: %s", idx, originalRef)
		}
		return r.walkPath(current.PrefixItems[idx], rest[1:], originalRef)

	case "allOf", "anyOf", "oneOf":
		if len(rest) == 0 {
			return nil, fmt.Errorf("incomplete ref, expected index after %s: %s", key, originalRef)
		}
		idx, err := parseIndex(rest[0])
		if err != nil {
			return nil, fmt.Errorf("invalid %s index %q: %s", key, rest[0], originalRef)
		}
		var arr []*Schema
		switch key {
		case "allOf":
			arr = current.AllOf
		case "anyOf":
			arr = current.AnyOf
		case "oneOf":
			arr = current.OneOf
		}
		if idx >= len(arr) {
			return nil, fmt.Errorf("%s index %d out of range: %s", key, idx, originalRef)
		}
		return r.walkPath(arr[idx], rest[1:], originalRef)

	case "not":
		if current.Not == nil {
			return nil, fmt.Errorf("schema has no not: %s", originalRef)
		}
		return r.walkPath(current.Not, rest, originalRef)

	case "if":
		if current.If == nil {
			return nil, fmt.Errorf("schema has no if: %s", originalRef)
		}
		return r.walkPath(current.If, rest, originalRef)

	case "then":
		if current.Then == nil {
			return nil, fmt.Errorf("schema has no then: %s", originalRef)
		}
		return r.walkPath(current.Then, rest, originalRef)

	case "else":
		if current.Else == nil {
			return nil, fmt.Errorf("schema has no else: %s", originalRef)
		}
		return r.walkPath(current.Else, rest, originalRef)

	case "additionalProperties":
		target := current.AdditionalProperties.AsSchema()
		if target == nil {
			return nil, fmt.Errorf("schema has no additionalProperties schema: %s", originalRef)
		}
		return r.walkPath(target, rest, originalRef)

	case "additionalItems":
		target := current.AdditionalItems.AsSchema()
		if target == nil {
			return nil, fmt.Errorf("schema has no additionalItems schema: %s", originalRef)
		}
		return r.walkPath(target, rest, originalRef)

	case "patternProperties":
		if len(rest) == 0 {
			return nil, fmt.Errorf("incomplete ref, expected pattern after patternProperties: %s", originalRef)
		}
		name := rest[0]
		if current.PatternProperties == nil {
			return nil, fmt.Errorf("schema has no patternProperties: %s", originalRef)
		}
		s, ok := current.PatternProperties[name]
		if !ok {
			return nil, fmt.Errorf("patternProperties does not contain %q: %s", name, originalRef)
		}
		return r.walkPath(s, rest[1:], originalRef)

	case "dependentSchemas":
		if len(rest) == 0 {
			return nil, fmt.Errorf("incomplete ref, expected name after dependentSchemas: %s", originalRef)
		}
		name := rest[0]
		if current.DependentSchemas == nil {
			return nil, fmt.Errorf("schema has no dependentSchemas: %s", originalRef)
		}
		s, ok := current.DependentSchemas[name]
		if !ok {
			return nil, fmt.Errorf("dependentSchemas does not contain %q: %s", name, originalRef)
		}
		return r.walkPath(s, rest[1:], originalRef)

	case "contains":
		if current.Contains == nil {
			return nil, fmt.Errorf("schema has no contains: %s", originalRef)
		}
		return r.walkPath(current.Contains, rest, originalRef)

	case "propertyNames":
		if current.PropertyNames == nil {
			return nil, fmt.Errorf("schema has no propertyNames: %s", originalRef)
		}
		return r.walkPath(current.PropertyNames, rest, originalRef)

	case "unevaluatedProperties":
		if current.UnevaluatedProperties == nil {
			return nil, fmt.Errorf("schema has no unevaluatedProperties: %s", originalRef)
		}
		return r.walkPath(current.UnevaluatedProperties, rest, originalRef)

	case "unevaluatedItems":
		if current.UnevaluatedItems == nil {
			return nil, fmt.Errorf("schema has no unevaluatedItems: %s", originalRef)
		}
		return r.walkPath(current.UnevaluatedItems, rest, originalRef)

	case "contentSchema":
		if current.ContentSchema == nil {
			return nil, fmt.Errorf("schema has no contentSchema: %s", originalRef)
		}
		return r.walkPath(current.ContentSchema, rest, originalRef)

	default:
		// Check Extensions for unknown keywords (e.g., vendor extensions,
		// arbitrary keywords referenced via JSON Pointer $ref).
		if current.Extensions != nil {
			if raw, ok := current.Extensions[key]; ok {
				// Try the whole value as a schema first, then walk any remaining
				// pointer inside it. That is the right order for a keyword whose
				// value *is* a schema (a vendor keyword holding "properties",
				// say), where the remaining tokens name schema fields.
				if sub, err := current.extensionSchema(key, nil, raw); err == nil {
					if len(rest) == 0 {
						return sub, nil
					}
					if target, err := r.walkPath(sub, rest, originalRef); err == nil {
						return target, nil
					}
				}
				// Otherwise the keyword holds a collection and the *element* is
				// the schema: "examples" is an array, so "#/examples/0" must
				// index it before parsing. This also covers a keyword whose
				// value is a plain object of schemas.
				sub, err := current.extensionSchema(key, rest, raw)
				if err != nil {
					return nil, fmt.Errorf("cannot parse extension %q as schema in: %s: %w", key, originalRef, err)
				}
				return sub, nil
			}
		}
		return nil, fmt.Errorf("unsupported ref path segment %q in: %s", key, originalRef)
	}
}

// parseIndex parses a string as a non-negative integer index.
//
// A run of digits long enough to overflow an int is refused rather than allowed
// to wrap. Every caller bounds the result with "idx >= len(...)", which a
// wrapped-negative index passes; the subscript that follows then panics with
// "index out of range [-9219744073709551616]". A ref of
// "#/items/9227000000000000000" is enough to produce one, and no slice could
// have that element in any case, so refusing it is the same answer the bound
// check was going to give.
func parseIndex(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, fmt.Errorf("empty index")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-numeric index: %s", s)
		}
		d := int(c - '0')
		if n > (maxInt-d)/10 {
			return 0, fmt.Errorf("index out of range: %s", s)
		}
		n = n*10 + d
	}
	return n, nil
}

// maxInt is the largest value an int can hold on this platform.
const maxInt = int(^uint(0) >> 1)

// UnescapePointerToken decodes one reference token of a JSON Pointer that
// arrived as a URI fragment, which is the only way a $ref ever carries one.
//
// Per RFC 6901 §6 the two decodings happen in one order and not the other:
// percent-decoding first (RFC 3986 §3.5), then the JSON Pointer escapes. The
// fragment is a URI component, so its percent-escapes belong to the outer
// encoding layer and have to come off before anything reads the pointer syntax
// underneath. The orders are not interchangeable -- "%7E1" percent-decodes to
// "~1" and then unescapes to "/", while unescaping first finds no literal "~1"
// and leaves the token naming "~1" -- so a caller that picks the wrong one
// names a different member of the document than the pointer does.
//
// It is exported because that answer has three consumers and must be one
// function: the resolver below, which decides what a $ref reaches; the
// generator's ref-to-name derivation, which names what it reached; and
// cmd/schemagen's shared-definition bookkeeping, which has to agree with both
// about which definition a pointer names. Two of the three had their own copy
// and one of those copies was wrong -- it never percent-decoded at all, though
// its comment said it did (issue #305). The same collapse as #178 and #203/#211:
// one rule, one implementation.
func UnescapePointerToken(token string) string {
	if decoded, err := url.PathUnescape(token); err == nil {
		token = decoded
	}
	return unescapeJSONPointer(token)
}

// unescapeJSONPointer decodes JSON Pointer escaping (RFC 6901):
// ~1 → / and ~0 → ~
func unescapeJSONPointer(token string) string {
	// Order matters: ~1 first, then ~0
	token = strings.ReplaceAll(token, "~1", "/")
	token = strings.ReplaceAll(token, "~0", "~")
	return token
}

// ---------- MappingResolver (static URI → Schema map) ----------

// MappingResolver resolves $ref URIs using a static map of URI → Schema.
// This is used for test suites (mapping localhost URLs to local schemas)
// and for $id-indexed schemas within a document.
type MappingResolver struct {
	schemas map[string]*Schema // full URI → Schema
}

// NewMappingResolver creates a MappingResolver from a map of URI strings to schemas.
func NewMappingResolver(schemas map[string]*Schema) *MappingResolver {
	return &MappingResolver{schemas: schemas}
}

// ResolveSchema implements SchemaResolver. It resolves the ref by combining it with
// the baseURI (for relative refs) or using it directly (for absolute refs).
// If the resolved URI has a fragment, it delegates to a LocalResolver on the
// matched schema.
func (m *MappingResolver) ResolveSchema(ref string, baseURI *url.URL) (*Schema, error) {
	// Parse the ref as a URI.
	refURL, err := url.Parse(ref)
	if err != nil {
		return nil, fmt.Errorf("invalid ref URI %q: %w", ref, err)
	}

	// Resolve against base URI if relative.
	resolved := refURL
	if baseURI != nil && !refURL.IsAbs() && !strings.HasPrefix(ref, "#") {
		resolved = baseURI.ResolveReference(refURL)
	}

	// Split into document URI (without fragment) and fragment.
	fragment := resolved.Fragment
	docURI := *resolved
	docURI.Fragment = ""
	docKey := docURI.String()

	// Look up the document schema.
	s, ok := m.schemas[docKey]
	if !ok {
		// Try without trailing slash.
		if strings.HasSuffix(docKey, "/") {
			s, ok = m.schemas[strings.TrimSuffix(docKey, "/")]
		}
		if !ok {
			return nil, fmt.Errorf("MappingResolver: no schema for URI %q", docKey)
		}
	}

	// If there's a fragment, resolve it within the found schema.
	if fragment != "" {
		local := NewLocalResolver(s)
		return local.ResolveLocal("#" + fragment)
	}

	return s, nil
}

// normalizeLoadedDocument normalizes a whole document a resolver has just
// loaded, under the dialect the run was told to read its documents in.
//
// A document a $ref pulls in is a document of the run exactly as a listed input
// is, and --draft is the caller's statement about the run: the CLI normalizes
// every input with NormalizeForDraft(draft), while both resolvers called plain
// Normalize() and so answered "which dialect" from a second source. Normalize is
// NormalizeForDraft(DraftUnknown), and DraftUnknown means "read the dialect from
// the document" -- which for a document stating no $schema is no dialect at all,
// and the keyword gate answers yes to every keyword there. One command line then
// read one schema set under two dialects: under --draft 3 the `const` dropped
// from the document the caller listed survived in the one reached by $ref beside
// it, so the same JSON was accepted by one generated type and refused by the
// other (issue #314).
//
// It is supplied only where the document states no dialect of its own, which is
// the same rule the generator's draftForSchema already applies to that document
// when it decides what a keyword means: a resource reached by $ref that declares
// its own $schema keeps it, so cross-draft $ref semantics are preserved, and the
// README states that as the one exception to --draft. Forcing the override here
// regardless would leave normalization and generation reading one node under two
// dialects, which is the shape of #203 rather than a fix for #314.
//
// What NormalizeForDraft supplies is in any case the *root's* dialect and never
// an embedded resource's, so a nested $id/$schema resource keeps its own for the
// subtree below it. That rule is inherited here rather than restated.
//
// draft is DraftUnknown for a run that passed no --draft, and this is then the
// exact Normalize() call it replaces.
func normalizeLoadedDocument(s *Schema, draft Draft) {
	if DetectDraft(s) != DraftUnknown {
		draft = DraftUnknown
	}
	s.NormalizeForDraft(draft)
}

// ---------- FileResolver (local filesystem) ----------

// FileResolver resolves $ref URIs by loading JSON Schema files from the filesystem.
// It resolves relative paths against a base directory.
type FileResolver struct {
	baseDir string
	cache   map[string]*Schema
	draft   Draft
}

// FileResolverOption configures a FileResolver.
type FileResolverOption func(*FileResolver)

// WithFileResolverDraft sets the dialect the root of a document this resolver
// loads is read under. See normalizeLoadedDocument.
func WithFileResolverDraft(d Draft) FileResolverOption {
	return func(r *FileResolver) { r.draft = d }
}

// NewFileResolver creates a FileResolver that loads schemas relative to baseDir.
func NewFileResolver(baseDir string, opts ...FileResolverOption) *FileResolver {
	r := &FileResolver{
		baseDir: baseDir,
		cache:   make(map[string]*Schema),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// withinBase reports whether target resolves to a path inside the resolver's
// base directory subtree. Both are made absolute and cleaned so that "../"
// traversal cannot escape the root, and both have their symlinks resolved so
// that a link *inside* the base directory cannot point outside it — a lexical
// prefix check alone would accept base/link.json → /etc/passwd. An empty
// baseDir (no confinement configured) permits any path.
func (f *FileResolver) withinBase(target string) bool {
	if f.baseDir == "" {
		return true
	}
	absBase, err := filepath.Abs(f.baseDir)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	absBase = resolveSymlinks(filepath.Clean(absBase))
	absTarget = resolveSymlinks(filepath.Clean(absTarget))
	if absTarget == absBase {
		return true
	}
	return strings.HasPrefix(absTarget, absBase+string(filepath.Separator))
}

// resolveSymlinks returns path with every symlink in it resolved. The path need
// not exist: the deepest existing ancestor is resolved and the remaining
// (non-existent) segments are appended unchanged, which is enough to confine
// the read — a missing file fails on open regardless. Falls back to the input
// when nothing along the path can be resolved.
func resolveSymlinks(path string) string {
	rest := ""
	for cur := path; ; {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return resolved
			}
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Reached the root without resolving anything.
			return path
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// ResolveSchema implements SchemaResolver. It handles file:// URLs and relative file paths.
func (f *FileResolver) ResolveSchema(ref string, baseURI *url.URL) (*Schema, error) {
	// Parse ref.
	refURL, err := url.Parse(ref)
	if err != nil {
		return nil, fmt.Errorf("invalid ref URI %q: %w", ref, err)
	}

	// Fragment-only refs are not our responsibility.
	if refURL.Scheme == "" && refURL.Host == "" && refURL.Path == "" {
		return nil, fmt.Errorf("FileResolver: fragment-only ref %q not handled", ref)
	}

	// Only handle file:// scheme or scheme-less (relative paths).
	if refURL.Scheme != "" && refURL.Scheme != "file" {
		return nil, fmt.Errorf("FileResolver: unsupported scheme %q in %q", refURL.Scheme, ref)
	}

	// Determine the file path.
	var filePath string
	if refURL.Scheme == "file" {
		filePath = refURL.Path
	} else {
		// Relative path: resolve against baseDir or baseURI.
		relPath := refURL.Path
		if baseURI != nil && baseURI.Scheme == "file" {
			// Resolve relative to the base file's directory.
			baseDir := filepath.Dir(baseURI.Path)
			filePath = filepath.Join(baseDir, relPath)
		} else {
			filePath = filepath.Join(f.baseDir, relPath)
		}
	}

	// Confine reads to the resolver's base directory subtree. A $ref path that
	// escapes it (via "../" sequences or an absolute file:// path) would let an
	// untrusted schema read arbitrary files during generation, so it is rejected.
	if !f.withinBase(filePath) {
		return nil, fmt.Errorf("FileResolver: refusing to read %q outside base directory %q", filePath, f.baseDir)
	}

	// Check cache.
	fragment := refURL.Fragment
	if cached, ok := f.cache[filePath]; ok {
		if fragment != "" {
			local := NewLocalResolver(cached)
			return local.ResolveLocal("#" + fragment)
		}
		return cached, nil
	}

	// Load the file.
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("FileResolver: reading %q: %w", filePath, err)
	}

	var s Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("FileResolver: parsing %q: %w", filePath, err)
	}
	normalizeLoadedDocument(&s, f.draft)

	f.cache[filePath] = &s

	if fragment != "" {
		local := NewLocalResolver(&s)
		return local.ResolveLocal("#" + fragment)
	}

	return &s, nil
}

// ---------- HTTPResolver (remote HTTP/HTTPS) ----------

// DefaultMaxResponseBytes caps how much of a remote schema document is read.
// Remote resolution is opt-in (--allow-remote-refs) and points at URLs taken
// from the input schema, so an unbounded read lets a hostile or broken endpoint
// exhaust memory during generation.
const DefaultMaxResponseBytes int64 = 10 << 20 // 10 MiB

// maxRedirects bounds the redirect chain for a single fetch. Each hop can send
// the request to a host the input schema never named, so the chain is kept
// short and is not allowed to downgrade https to http.
const maxRedirects = 5

// HTTPResolver resolves $ref URIs by fetching JSON Schema documents over HTTP/HTTPS.
// It caches fetched schemas in memory to avoid redundant network requests.
type HTTPResolver struct {
	client           *http.Client
	cache            map[string]*Schema
	maxResponseBytes int64
	draft            Draft
}

// HTTPResolverOption configures an HTTPResolver.
type HTTPResolverOption func(*HTTPResolver)

// WithHTTPClient sets a custom HTTP client for the resolver.
func WithHTTPClient(client *http.Client) HTTPResolverOption {
	return func(r *HTTPResolver) {
		r.client = client
	}
}

// WithMaxResponseBytes caps the size of a fetched schema document. A value <= 0
// removes the cap.
func WithMaxResponseBytes(n int64) HTTPResolverOption {
	return func(r *HTTPResolver) {
		r.maxResponseBytes = n
	}
}

// WithHTTPResolverDraft sets the dialect the root of a document this resolver
// fetches is read under. See normalizeLoadedDocument.
func WithHTTPResolverDraft(d Draft) HTTPResolverOption {
	return func(r *HTTPResolver) {
		r.draft = d
	}
}

// NewHTTPResolver creates an HTTPResolver that fetches schemas over HTTP/HTTPS.
// By default it uses a client with a 30-second timeout, a bounded redirect
// chain that refuses https→http downgrades, and a 10 MiB response cap.
func NewHTTPResolver(opts ...HTTPResolverOption) *HTTPResolver {
	r := &HTTPResolver{
		client:           &http.Client{Timeout: 30 * time.Second},
		cache:            make(map[string]*Schema),
		maxResponseBytes: DefaultMaxResponseBytes,
	}
	for _, opt := range opts {
		opt(r)
	}
	// Apply the redirect policy to whichever client we ended up with, but never
	// override one the caller configured deliberately.
	if r.client != nil && r.client.CheckRedirect == nil {
		r.client.CheckRedirect = checkRedirect
	}
	return r
}

// checkRedirect bounds the redirect chain and refuses a downgrade from https to
// http, which would silently move a fetch onto an unprotected connection.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf("refusing redirect from https to %q (%s)", req.URL.Scheme, req.URL.Redacted())
	}
	return nil
}

// ResolveSchema implements SchemaResolver. It handles http:// and https:// URLs.
func (h *HTTPResolver) ResolveSchema(ref string, baseURI *url.URL) (*Schema, error) {
	// Parse ref.
	refURL, err := url.Parse(ref)
	if err != nil {
		return nil, fmt.Errorf("invalid ref URI %q: %w", ref, err)
	}

	// Resolve against base URI if relative.
	resolved := refURL
	if baseURI != nil && !refURL.IsAbs() && !strings.HasPrefix(ref, "#") {
		resolved = baseURI.ResolveReference(refURL)
	}

	// Only handle http:// and https:// schemes.
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return nil, fmt.Errorf("HTTPResolver: unsupported scheme %q in %q", resolved.Scheme, resolved.String())
	}

	// Fragment-only refs after resolution are not our responsibility.
	if resolved.Host == "" && resolved.Path == "" {
		return nil, fmt.Errorf("HTTPResolver: fragment-only ref %q not handled", ref)
	}

	// Split into document URI (without fragment) and fragment.
	fragment := resolved.Fragment
	docURI := *resolved
	docURI.Fragment = ""
	docKey := docURI.String()

	// Check cache.
	if cached, ok := h.cache[docKey]; ok {
		if fragment != "" {
			local := NewLocalResolver(cached)
			return local.ResolveLocal("#" + fragment)
		}
		return cached, nil
	}

	// Fetch the schema.
	resp, err := h.client.Get(docKey)
	if err != nil {
		return nil, &RemoteFetchError{URL: docKey, Reason: err}
	}
	defer resp.Body.Close()

	// The URI this document was actually retrieved from, which is the requested
	// one only when nothing redirected. It is what the base URI of a document
	// that declares no $id is, per RFC 3986 §5.1.3 -- deferred to by 2020-12
	// §9.1.1 -- so a relative $ref inside the answer is read against the
	// directory that answered and not the one that was asked. Keyed by the
	// requested URL alone, /redirect.json's `{"$ref":"sub.json"}` fetched the
	// /sub.json beside the redirect instead of the one beside the document, and
	// enforced it in silence (issue #315).
	//
	// Also the second cache key. Reaching one document under both URLs otherwise
	// parsed it twice, and two instances of one document are two Go types.
	retrievalKey := docKey
	if resp.Request != nil && resp.Request.URL != nil {
		final := *resp.Request.URL
		final.Fragment = ""
		retrievalKey = final.String()
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &RemoteFetchError{URL: docKey, Reason: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}

	// An HTML error page parses as neither schema nor useful error, so reject a
	// clearly non-JSON body up front. An absent Content-Type is tolerated: some
	// schema hosts omit it, and json.Unmarshal is the real check either way.
	if ct := resp.Header.Get("Content-Type"); ct != "" && !isJSONContentType(ct) {
		return nil, &RemoteFetchError{URL: docKey, Reason: fmt.Errorf("Content-Type %q, want JSON", ct)}
	}

	body, err := h.readCapped(resp.Body, docKey)
	if err != nil {
		return nil, err
	}

	var s Schema
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, &RemoteFetchError{URL: docKey, Reason: fmt.Errorf("parsing schema: %w", err)}
	}
	normalizeLoadedDocument(&s, h.draft)
	if retrievalURL, err := url.Parse(retrievalKey); err == nil {
		s.RetrievalURI = retrievalURL
	}

	h.cache[docKey] = &s
	if retrievalKey != docKey {
		h.cache[retrievalKey] = &s
	}

	if fragment != "" {
		local := NewLocalResolver(&s)
		return local.ResolveLocal("#" + fragment)
	}

	return &s, nil
}

// RemoteFetchError reports a remote $ref the HTTP resolver was permitted to
// fetch and could not turn into a schema.
//
// Typed because the ways a remote $ref fails need different advice and only this
// one says the network was reached: a run that passed --allow-remote-refs and
// got a 404, a refused connection or an HTML body was told to pass
// --allow-remote-refs, which sent the caller looking for a second cause that was
// not there (issue #317). Every failure from the request onwards takes this
// shape, so "was a fetch attempted" is one errors.As away.
type RemoteFetchError struct {
	// URL is the document URI the request was made for.
	URL string
	// Reason is what went wrong once the request had been made: the transport
	// error, a status other than 200, a body that is not JSON, or one over the
	// response cap.
	Reason error
}

func (e *RemoteFetchError) Error() string {
	return fmt.Sprintf("HTTPResolver: fetching %q: %v", e.URL, e.Reason)
}

func (e *RemoteFetchError) Unwrap() error { return e.Reason }

// readCapped reads at most maxResponseBytes from body, erroring rather than
// truncating when the document is larger — a silently truncated schema would
// surface as a confusing parse error.
func (h *HTTPResolver) readCapped(body io.Reader, docKey string) ([]byte, error) {
	if h.maxResponseBytes <= 0 {
		data, err := io.ReadAll(body)
		if err != nil {
			return nil, &RemoteFetchError{URL: docKey, Reason: fmt.Errorf("reading response: %w", err)}
		}
		return data, nil
	}
	// Read one byte past the cap so exceeding it is distinguishable from
	// exactly reaching it.
	data, err := io.ReadAll(io.LimitReader(body, h.maxResponseBytes+1))
	if err != nil {
		return nil, &RemoteFetchError{URL: docKey, Reason: fmt.Errorf("reading response: %w", err)}
	}
	if int64(len(data)) > h.maxResponseBytes {
		return nil, &RemoteFetchError{URL: docKey, Reason: fmt.Errorf("exceeds the %d byte response limit", h.maxResponseBytes)}
	}
	return data, nil
}

// isJSONContentType reports whether a Content-Type header names a JSON media
// type: application/json, application/schema+json, and any other "+json"
// structured suffix. Parameters (charset, profile) are ignored.
func isJSONContentType(ct string) bool {
	mediaType := ct
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	return mediaType == "application/json" ||
		mediaType == "text/json" ||
		strings.HasSuffix(mediaType, "+json")
}

// ---------- CompositeResolver (chain of resolvers) ----------

// CompositeResolver tries multiple SchemaResolvers in order, returning the result
// from the first one that succeeds.
type CompositeResolver struct {
	resolvers []SchemaResolver
}

// NewCompositeResolver creates a CompositeResolver that tries resolvers in order.
func NewCompositeResolver(resolvers ...SchemaResolver) *CompositeResolver {
	return &CompositeResolver{resolvers: resolvers}
}

// ResolveSchema implements SchemaResolver by trying each resolver in order and
// returning the first success. On failure it joins every resolver's error so
// the caller sees the informative one (e.g. the file resolver's "no such file")
// instead of only the last resolver's (often a generic "unsupported scheme").
func (c *CompositeResolver) ResolveSchema(ref string, baseURI *url.URL) (*Schema, error) {
	var errs []error
	for _, r := range c.resolvers {
		s, err := r.ResolveSchema(ref, baseURI)
		if err == nil {
			return s, nil
		}
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, &ResolveError{Ref: ref, Errs: errs}
	}
	return nil, fmt.Errorf("no resolvers configured")
}

// ResolveError reports that no resolver of a chain could serve a $ref, and keeps
// what each of them said about it.
//
// Typed so a caller can reach the individual answers rather than a joined
// string: the four ways a remote $ref fails are told apart by which resolver
// spoke and how, and the generator that reports the ref needs that to say
// anything true about it (issue #317). The joined rendering is unchanged, so a
// caller that only prints the error sees what it saw before.
type ResolveError struct {
	// Ref is the reference as the schema wrote it.
	Ref string
	// Errs holds one entry per resolver in the chain, in the order they were
	// tried.
	Errs []error
}

func (e *ResolveError) Error() string {
	return fmt.Sprintf("resolving %q: %v", e.Ref, errors.Join(e.Errs...))
}

func (e *ResolveError) Unwrap() []error { return e.Errs }
