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

// anchorNameOf returns the name under which s can be reached as a plain "#name"
// anchor. Normalize copies a legacy "id" into ID, so both are consulted.
func anchorNameOf(s *Schema) string {
	if s.Anchor != "" {
		return s.Anchor
	}
	if name := plainNameFragment(s.ID); name != "" {
		return name
	}
	return plainNameFragment(s.LegacyID)
}

// changesScope reports whether a subschema's id starts a new document scope.
// Only a scope-changing id hides the subtree from the parent's anchor search —
// a plain-name fragment id names a node inside the *current* scope, so the
// subtree must still be searched.
func changesScope(s *Schema) bool {
	return s.ID != "" && plainNameFragment(s.ID) == ""
}

// findAnchor searches the schema tree for a $anchor matching the given name,
// or for the pre-2019-09 spelling of the same thing ({"id": "#name"}).
//
// The legacy form matters most for documents reached through a SchemaResolver:
// the generator's own anchor index covers the root document and does understand
// "id", but a resolver-fetched document is not in that index and is searched
// here instead.
func (r *LocalResolver) findAnchor(s *Schema, anchor string) (*Schema, error) {
	if s == nil {
		return nil, fmt.Errorf("anchor %q not found", anchor)
	}
	if anchorNameOf(s) == anchor {
		return s, nil
	}
	// Search in all sub-schema locations, but skip sub-schemas that start their
	// own document scope — their anchors belong to that scope, not the parent's.
	for _, sub := range r.allSubSchemas(s) {
		if changesScope(sub) {
			continue
		}
		if found, err := r.findAnchor(sub, anchor); err == nil {
			return found, nil
		}
	}
	return nil, fmt.Errorf("anchor %q not found", anchor)
}

// allSubSchemas returns all immediate sub-schemas of a schema for tree traversal.
// Map-valued fields (Properties, Defs, etc.) are iterated in sorted key order
// to ensure deterministic anchor resolution regardless of Go map iteration order.
func (r *LocalResolver) allSubSchemas(s *Schema) []*Schema {
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
	s.Normalize()

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
		return nil, fmt.Errorf("HTTPResolver: fetching %q: %w", docKey, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTPResolver: fetching %q: HTTP %d", docKey, resp.StatusCode)
	}

	// An HTML error page parses as neither schema nor useful error, so reject a
	// clearly non-JSON body up front. An absent Content-Type is tolerated: some
	// schema hosts omit it, and json.Unmarshal is the real check either way.
	if ct := resp.Header.Get("Content-Type"); ct != "" && !isJSONContentType(ct) {
		return nil, fmt.Errorf("HTTPResolver: %q returned Content-Type %q, want JSON", docKey, ct)
	}

	body, err := h.readCapped(resp.Body, docKey)
	if err != nil {
		return nil, err
	}

	var s Schema
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("HTTPResolver: parsing schema from %q: %w", docKey, err)
	}
	s.Normalize()

	h.cache[docKey] = &s

	if fragment != "" {
		local := NewLocalResolver(&s)
		return local.ResolveLocal("#" + fragment)
	}

	return &s, nil
}

// readCapped reads at most maxResponseBytes from body, erroring rather than
// truncating when the document is larger — a silently truncated schema would
// surface as a confusing parse error.
func (h *HTTPResolver) readCapped(body io.Reader, docKey string) ([]byte, error) {
	if h.maxResponseBytes <= 0 {
		data, err := io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("HTTPResolver: reading response from %q: %w", docKey, err)
		}
		return data, nil
	}
	// Read one byte past the cap so exceeding it is distinguishable from
	// exactly reaching it.
	data, err := io.ReadAll(io.LimitReader(body, h.maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("HTTPResolver: reading response from %q: %w", docKey, err)
	}
	if int64(len(data)) > h.maxResponseBytes {
		return nil, fmt.Errorf("HTTPResolver: %q exceeds the %d byte response limit", docKey, h.maxResponseBytes)
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
		return nil, fmt.Errorf("resolving %q: %w", ref, errors.Join(errs...))
	}
	return nil, fmt.Errorf("no resolvers configured")
}
