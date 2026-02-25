package schema

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	// Per RFC 6901 §6: when JSON Pointer is used as a URI fragment,
	// first percent-decode each segment (RFC 3986 §3.5), then apply
	// JSON Pointer unescaping (~1 → /, ~0 → ~).
	for i, p := range parts {
		if decoded, err := url.PathUnescape(p); err == nil {
			p = decoded
		}
		parts[i] = unescapeJSONPointer(p)
	}

	return r.walkPath(r.root, parts, ref)
}

// findAnchor searches the schema tree for a $anchor matching the given name.
func (r *LocalResolver) findAnchor(s *Schema, anchor string) (*Schema, error) {
	if s == nil {
		return nil, fmt.Errorf("anchor %q not found", anchor)
	}
	if s.Anchor == anchor {
		return s, nil
	}
	// Search in all sub-schema locations.
	for _, sub := range r.allSubSchemas(s) {
		if found, err := r.findAnchor(sub, anchor); err == nil {
			return found, nil
		}
	}
	return nil, fmt.Errorf("anchor %q not found", anchor)
}

// allSubSchemas returns all immediate sub-schemas of a schema for tree traversal.
func (r *LocalResolver) allSubSchemas(s *Schema) []*Schema {
	var subs []*Schema
	for _, v := range s.Properties {
		subs = append(subs, v)
	}
	for _, v := range s.Defs {
		subs = append(subs, v)
	}
	for _, v := range s.Definitions {
		subs = append(subs, v)
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
	for _, v := range s.PatternProperties {
		subs = append(subs, v)
	}
	for _, v := range s.DependentSchemas {
		subs = append(subs, v)
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
		if current.AdditionalProperties == nil || current.AdditionalProperties.Schema == nil {
			return nil, fmt.Errorf("schema has no additionalProperties schema: %s", originalRef)
		}
		return r.walkPath(current.AdditionalProperties.Schema, rest, originalRef)

	case "additionalItems":
		if current.AdditionalItems == nil || current.AdditionalItems.Schema == nil {
			return nil, fmt.Errorf("schema has no additionalItems schema: %s", originalRef)
		}
		return r.walkPath(current.AdditionalItems.Schema, rest, originalRef)

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
		return nil, fmt.Errorf("unsupported ref path segment %q in: %s", key, originalRef)
	}
}

// parseIndex parses a string as a non-negative integer index.
func parseIndex(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, fmt.Errorf("empty index")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-numeric index: %s", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
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

// ---------- FileResolver (local filesystem) ----------

// FileResolver resolves $ref URIs by loading JSON Schema files from the filesystem.
// It resolves relative paths against a base directory.
type FileResolver struct {
	baseDir string
	cache   map[string]*Schema
}

// NewFileResolver creates a FileResolver that loads schemas relative to baseDir.
func NewFileResolver(baseDir string) *FileResolver {
	return &FileResolver{
		baseDir: baseDir,
		cache:   make(map[string]*Schema),
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
	s.Normalize()

	f.cache[filePath] = &s

	if fragment != "" {
		local := NewLocalResolver(&s)
		return local.ResolveLocal("#" + fragment)
	}

	return &s, nil
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

// ResolveSchema implements SchemaResolver by trying each resolver in order.
func (c *CompositeResolver) ResolveSchema(ref string, baseURI *url.URL) (*Schema, error) {
	var lastErr error
	for _, r := range c.resolvers {
		s, err := r.ResolveSchema(ref, baseURI)
		if err == nil {
			return s, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no resolvers configured")
}
