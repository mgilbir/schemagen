package schema

import (
	"encoding/json"
	"strings"
)

// Draft represents a JSON Schema draft version.
type Draft int

const (
	DraftUnknown Draft = iota
	Draft03
	Draft04
	Draft06
	Draft07
	Draft201909
	Draft202012
)

// String returns a human-readable name for the draft.
func (d Draft) String() string {
	switch d {
	case Draft03:
		return "Draft-03"
	case Draft04:
		return "Draft-04"
	case Draft06:
		return "Draft-06"
	case Draft07:
		return "Draft-07"
	case Draft201909:
		return "Draft 2019-09"
	case Draft202012:
		return "Draft 2020-12"
	default:
		return "Unknown"
	}
}

// DetectDraft inspects the $schema URI to determine which draft version is used.
func DetectDraft(s *Schema) Draft {
	uri := s.Schema

	switch {
	case strings.Contains(uri, "draft-03"):
		return Draft03
	case strings.Contains(uri, "draft-04"):
		return Draft04
	case strings.Contains(uri, "draft-06"):
		return Draft06
	case strings.Contains(uri, "draft-07"):
		return Draft07
	case strings.Contains(uri, "draft/2019-09"):
		return Draft201909
	case strings.Contains(uri, "draft/2020-12"):
		return Draft202012
	default:
		return DraftUnknown
	}
}

// Normalize ensures the schema is consistent regardless of which draft it was
// authored in. It performs the following normalizations:
//   - Copies definitions <-> $defs bidirectionally
//   - Copies Draft 3/4 "id" to "$id"
//   - Converts Draft 3 "extends" to allOf
//   - Converts Draft 3 "divisibleBy" to multipleOf
//   - Converts Draft 4-7 "dependencies" to dependentSchemas/dependentRequired
//   - Recursively normalizes all nested schemas
func (s *Schema) Normalize() {
	if s == nil || s.IsBooleanSchema() {
		return
	}

	// Copy Draft 3/4 "id" to "$id" if $id is empty.
	if s.ID == "" && s.LegacyID != "" {
		s.ID = s.LegacyID
	}

	// Copy definitions → $defs if $defs is empty.
	if len(s.Defs) == 0 && len(s.Definitions) > 0 {
		s.Defs = make(map[string]*Schema, len(s.Definitions))
		for k, v := range s.Definitions {
			s.Defs[k] = v
		}
	}

	// Copy $defs → definitions if definitions is empty.
	if len(s.Definitions) == 0 && len(s.Defs) > 0 {
		s.Definitions = make(map[string]*Schema, len(s.Defs))
		for k, v := range s.Defs {
			s.Definitions[k] = v
		}
	}

	// Draft 3: convert "extends" to allOf.
	if len(s.Extends) > 0 {
		s.normalizeExtends()
	}

	// Draft 3: convert per-property "required": true to parent Required array.
	s.normalizeDraft3Required()

	// Draft 3: convert "divisibleBy" to "multipleOf".
	if s.DivisibleBy != nil && s.MultipleOf == nil {
		s.MultipleOf = s.DivisibleBy
	}

	// Draft 3: convert "disallow" to "not".
	// "disallow" is the draft 3 equivalent of "not" with type constraints.
	// It can be a single type string or an array of type strings.
	if len(s.Disallow) > 0 && s.Not == nil {
		s.normalizeDisallow()
	}

	// Draft 4-7: convert "dependencies" to dependentSchemas/dependentRequired.
	if len(s.Dependencies) > 0 {
		s.normalizeDependencies()
	}

	// Recursively normalize nested schemas.
	s.normalizeChildren()
}

// normalizeDisallow converts Draft 3's "disallow" to an equivalent "not" schema.
// A single type becomes not:{type:T}. An array becomes not:{anyOf:[...]},
// preserving inline schema objects instead of dropping them.
func (s *Schema) normalizeDisallow() {
	trimmed := trimJSONWhitespace(s.Disallow)
	if len(trimmed) == 0 {
		return
	}

	if trimmed[0] == '"' {
		// Single type string: "disallow": "integer"
		var t string
		if json.Unmarshal(s.Disallow, &t) == nil {
			s.Not = &Schema{Type: TypeList{t}}
		}
		return
	} else if trimmed[0] == '[' {
		// Array of strings or schemas: "disallow": ["integer", "boolean"]
		var raw []json.RawMessage
		if json.Unmarshal(s.Disallow, &raw) == nil {
			var branches []*Schema
			for _, elem := range raw {
				elemTrimmed := trimJSONWhitespace(elem)
				if len(elemTrimmed) > 0 && elemTrimmed[0] == '"' {
					var t string
					if json.Unmarshal(elem, &t) == nil {
						branches = append(branches, &Schema{Type: TypeList{t}})
					}
					continue
				}
				var branch Schema
				if json.Unmarshal(elem, &branch) == nil {
					branches = append(branches, &branch)
				}
			}
			if len(branches) == 1 {
				s.Not = branches[0]
			} else if len(branches) > 1 {
				s.Not = &Schema{AnyOf: branches}
			}
		}
	}
}

// normalizeDraft3Required converts Draft 3's per-property "required": true
// to the parent schema's Required array (Draft 4+ format).
func (s *Schema) normalizeDraft3Required() {
	for name, prop := range s.Properties {
		if prop != nil && prop.Required.IsDraft3Required() {
			s.Required = append(s.Required, name)
			prop.Required = nil // clear the sentinel
		}
	}
}

// normalizeExtends converts Draft 3's "extends" to allOf.
func (s *Schema) normalizeExtends() {
	// "extends" can be a single schema or array of schemas.
	trimmed := trimJSONWhitespace(s.Extends)
	if len(trimmed) == 0 {
		return
	}

	if trimmed[0] == '[' {
		var schemas []*Schema
		if json.Unmarshal(s.Extends, &schemas) == nil {
			s.AllOf = append(s.AllOf, schemas...)
		}
	} else {
		var sc Schema
		if json.Unmarshal(s.Extends, &sc) == nil {
			s.AllOf = append(s.AllOf, &sc)
		}
	}
	s.Extends = nil
}

// normalizeDependencies converts Draft 4-7's "dependencies" to
// dependentSchemas and dependentRequired (Draft 2019-09+ split).
func (s *Schema) normalizeDependencies() {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(s.Dependencies, &raw); err != nil {
		return
	}

	for key, val := range raw {
		trimmed := trimJSONWhitespace(val)
		if len(trimmed) == 0 {
			continue
		}

		// Try as array of strings (dependentRequired).
		if trimmed[0] == '[' {
			var arr []string
			if json.Unmarshal(val, &arr) == nil {
				if s.DependentRequired == nil {
					s.DependentRequired = make(map[string][]string)
				}
				s.DependentRequired[key] = arr
				continue
			}
		}

		// Try as schema (dependentSchemas).
		var sc Schema
		if json.Unmarshal(val, &sc) == nil {
			if s.DependentSchemas == nil {
				s.DependentSchemas = make(map[string]*Schema)
			}
			s.DependentSchemas[key] = &sc
		}
	}
	s.Dependencies = nil
}

// normalizeChildren recursively normalizes all nested sub-schemas.
func (s *Schema) normalizeChildren() {
	for _, sub := range s.Properties {
		if sub != nil {
			sub.Normalize()
		}
	}
	for _, sub := range s.PatternProperties {
		if sub != nil {
			sub.Normalize()
		}
	}
	for _, sub := range s.Defs {
		if sub != nil {
			sub.Normalize()
		}
	}
	for _, sub := range s.Definitions {
		if sub != nil {
			sub.Normalize()
		}
	}
	for _, sub := range s.AllOf {
		if sub != nil {
			sub.Normalize()
		}
	}
	for _, sub := range s.AnyOf {
		if sub != nil {
			sub.Normalize()
		}
	}
	for _, sub := range s.OneOf {
		if sub != nil {
			sub.Normalize()
		}
	}
	for _, sub := range s.PrefixItems {
		if sub != nil {
			sub.Normalize()
		}
	}
	if s.Not != nil {
		s.Not.Normalize()
	}
	if s.Items != nil {
		if s.Items.Schema != nil {
			s.Items.Schema.Normalize()
		}
		for _, sub := range s.Items.Schemas {
			if sub != nil {
				sub.Normalize()
			}
		}
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		s.AdditionalProperties.Schema.Normalize()
	}
	if s.AdditionalItems != nil && s.AdditionalItems.Schema != nil {
		s.AdditionalItems.Schema.Normalize()
	}
	if s.If != nil {
		s.If.Normalize()
	}
	if s.Then != nil {
		s.Then.Normalize()
	}
	if s.Else != nil {
		s.Else.Normalize()
	}
	if s.Contains != nil {
		s.Contains.Normalize()
	}
	if s.PropertyNames != nil {
		s.PropertyNames.Normalize()
	}
	if s.ContentSchema != nil {
		s.ContentSchema.Normalize()
	}
	if s.UnevaluatedItems != nil {
		s.UnevaluatedItems.Normalize()
	}
	if s.UnevaluatedProperties != nil {
		s.UnevaluatedProperties.Normalize()
	}
	for _, sub := range s.DependentSchemas {
		if sub != nil {
			sub.Normalize()
		}
	}
}
