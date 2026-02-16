package schema

import "strings"

// Draft represents a JSON Schema draft version.
type Draft int

const (
	DraftUnknown Draft = iota
	Draft07
	Draft202012
)

// String returns a human-readable name for the draft.
func (d Draft) String() string {
	switch d {
	case Draft07:
		return "Draft-07"
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
	case strings.Contains(uri, "draft-07"):
		return Draft07
	case strings.Contains(uri, "draft/2020-12"):
		return Draft202012
	default:
		return DraftUnknown
	}
}

// Normalize ensures the schema is consistent regardless of which draft it was
// authored in. Specifically, it ensures both Definitions and Defs are populated
// so that downstream code can always use Defs.
func (s *Schema) Normalize() {
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

	// Recursively normalize nested schemas.
	for _, sub := range s.Properties {
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
	if s.If != nil {
		s.If.Normalize()
	}
	if s.Then != nil {
		s.Then.Normalize()
	}
	if s.Else != nil {
		s.Else.Normalize()
	}
}
