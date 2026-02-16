package schema

import (
	"fmt"
	"strings"
)

// Resolver resolves $ref references within a JSON Schema document.
type Resolver struct {
	root  *Schema
	cache map[string]*Schema
}

// NewResolver creates a new Resolver rooted at the given schema.
func NewResolver(root *Schema) *Resolver {
	return &Resolver{
		root:  root,
		cache: make(map[string]*Schema),
	}
}

// Resolve resolves a $ref string to the target Schema.
// Supported reference formats:
//   - "#" — the root schema
//   - "#/$defs/TypeName" — lookup in $defs
//   - "#/definitions/TypeName" — lookup in definitions
//   - "#/properties/propName" — lookup in properties
func (r *Resolver) Resolve(ref string) (*Schema, error) {
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

func (r *Resolver) resolve(ref string) (*Schema, error) {
	if !strings.HasPrefix(ref, "#") {
		return nil, fmt.Errorf("unsupported ref format (only local refs starting with '#' are supported): %s", ref)
	}

	// "#" refers to the root.
	if ref == "#" {
		return r.root, nil
	}

	// Strip leading "#/"
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("invalid ref format: %s", ref)
	}

	path := strings.TrimPrefix(ref, "#/")
	parts := strings.Split(path, "/")

	return r.walkPath(r.root, parts, ref)
}

func (r *Resolver) walkPath(current *Schema, parts []string, originalRef string) (*Schema, error) {
	if len(parts) == 0 {
		return current, nil
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

	default:
		return nil, fmt.Errorf("unsupported ref path segment %q in: %s", key, originalRef)
	}
}
