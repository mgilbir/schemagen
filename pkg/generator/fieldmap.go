package generator

import (
	"encoding/json"
	"fmt"
	"os"
)

// reservedFieldNames are identifiers the emitter generates on a struct as
// methods or synthesized fields. A field-map override that targets one of these
// would produce uncompilable Go (a field redeclaring a method, or colliding
// with the overflow field), so overrides to these names are rejected up front.
// Keyed by name → human-readable description of what it collides with.
var reservedFieldNames = map[string]string{
	"Validate":             "the generated Validate method",
	"MarshalJSON":          "the generated MarshalJSON method",
	"UnmarshalJSON":        "the generated UnmarshalJSON method",
	"SetDefaults":          "the generated SetDefaults method",
	"SchemagenJSONTree":    "the generated SchemagenJSONTree method",
	"AdditionalProperties": "the generated additional-properties overflow field",
	"PatternProperties":    "the generated pattern-properties overflow field",
}

// generatedMemberNames lists the exported members (methods and synthesized
// overflow fields) the emitter may place on any generated struct. The
// derived-name dedup pass in generateStructDef treats these as pre-occupied so
// that a derived property field name colliding with one is auto-renamed via the
// numeric-suffix mechanism (unlike field-map overrides, which are user-pinned
// and instead error via reservedFieldNames). Reserved unconditionally; see the
// comment at the dedup pass for the rationale.
var generatedMemberNames = []string{
	"Validate",
	"MarshalJSON",
	"UnmarshalJSON",
	"SetDefaults",
	"SchemagenJSONTree",
	"AdditionalProperties",
	"PatternProperties",
}

// FieldNameMap maps a Go type name to a set of property overrides, where each
// override pins a JSON property name to a specific Go struct field name.
//
//	FieldNameMap{
//	    "Person":  {"first_name": "GivenName"},
//	    "Address": {"zip": "PostalCode"},
//	}
//
// Overrides let generated structs keep field names compatible with an existing
// codebase that is migrating to schema-generated types.
type FieldNameMap map[string]map[string]string

// Override returns the configured Go field name for the given type/property
// pair, if one is present.
func (m FieldNameMap) Override(typeName, jsonProp string) (string, bool) {
	if m == nil {
		return "", false
	}
	props, ok := m[typeName]
	if !ok {
		return "", false
	}
	name, ok := props[jsonProp]
	return name, ok
}

// FieldMapFile is the on-disk shape of a --field-map config: schema-file base
// name → type name → JSON property → Go field name.
type FieldMapFile map[string]FieldNameMap

// LoadFieldMapFile reads and validates a field-map JSON config from disk. Every
// override value must be a valid exported Go identifier, since generated struct
// fields must be exported to participate in JSON (un)marshaling.
func LoadFieldMapFile(path string) (FieldMapFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading field map: %w", err)
	}

	var fm FieldMapFile
	if err := json.Unmarshal(data, &fm); err != nil {
		return nil, fmt.Errorf("parsing field map %s: %w", path, err)
	}

	// In key order at every level, so that a file with several bad entries is
	// refused for the same one on every run rather than for whichever the map
	// happened to yield first.
	for _, file := range sortedKeys(fm) {
		types := fm[file]
		for _, typeName := range sortedKeys(types) {
			props := types[typeName]
			for _, prop := range sortedKeys(props) {
				goName := props[prop]
				if !isExportedGoIdentifier(goName) {
					return nil, fmt.Errorf(
						"field map %s: %q -> %s.%s maps to %q, which is not a valid exported Go identifier",
						path, file, typeName, prop, goName)
				}
			}
		}
	}

	return fm, nil
}

// isExportedGoIdentifier reports whether s is a valid Go identifier that begins
// with an upper-case letter (i.e. exported), under every Go the generated code
// supports. See IsExportedIdentifier and unicodeident.go.
func isExportedGoIdentifier(s string) bool {
	return IsExportedIdentifier(s)
}
