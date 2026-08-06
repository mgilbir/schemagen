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

	// DraftV1 is the undated stable release that succeeds the dated drafts,
	// dialect URI https://json-schema.org/v1.
	//
	// It is not an alias for Draft202012 even though the keyword set is nearly
	// the same, because the two disagree about the one keyword whose posture
	// this generator reads from the dialect. 2020-12 declares the
	// format-annotation vocabulary and the suite marks {"format":"email"}
	// satisfied by "2962"; v1 drops vocabularies and moves its format tests out
	// of optional/ into a required top-level format/ directory, where the same
	// document is marked invalid. Mapping v1 onto Draft202012 would silently
	// take the annotation reading and stop enforcing every format a v1 schema
	// names. See formatAssertsFor.
	DraftV1
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
	case DraftV1:
		return "v1"
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
	// v1 names no draft at all -- "https://json-schema.org/v1" -- so it is
	// matched on the host-and-path pair rather than on "v1" alone, which would
	// also fire on a "draft/v1" that means something else and on any unrelated
	// dialect whose URI happens to contain those two characters.
	case strings.Contains(uri, "json-schema.org/v1"):
		return DraftV1
	default:
		return DraftUnknown
	}
}

// Normalize ensures the schema is consistent regardless of which draft it was
// authored in. It performs the following normalizations:
//   - Drops every keyword the node's own dialect does not define
//   - Copies definitions <-> $defs bidirectionally
//   - Copies Draft 3/4 "id" to "$id"
//   - Converts Draft 3 "extends" to allOf
//   - Converts Draft 3 "divisibleBy" to multipleOf
//   - Converts Draft 4-7 "dependencies" to dependentSchemas/dependentRequired
//   - Recursively normalizes all nested schemas
//
// The dialect is the document's own $schema, inherited by every node that does
// not declare one of its own. NormalizeForDraft is the same walk with the root's
// dialect supplied from outside, which is what --draft does.
func (s *Schema) Normalize() {
	s.NormalizeForDraft(DraftUnknown)
}

// NormalizeForDraft normalizes under a dialect chosen from outside the document,
// which stands in for the root's own $schema exactly as the --draft flag does.
//
// Only the root's dialect is supplied: a nested node declaring its own $schema
// is an embedded resource and keeps it, for the subtree below it too. Passing
// DraftUnknown is the same as calling Normalize -- it means "read the dialect
// from the document" and not "this document has no dialect".
func (s *Schema) NormalizeForDraft(d Draft) {
	if s == nil || s.IsBooleanSchema() {
		return
	}
	if d == DraftUnknown {
		d = DetectDraft(s)
	}

	// The dialect gate is a pass of its own, run over the whole tree before the
	// first rewrite. Both halves of that sentence are load-bearing.
	//
	// *Before*, because five of the rewrites read a keyword one dialect alone
	// defines and write one that dialect does not have -- extends into allOf,
	// divisibleBy into multipleOf, disallow into not, the per-property boolean
	// required into the parent's array, dependencies into the 2019-09 pair.
	// Gating afterwards would delete what the rewrite had just legitimately
	// produced: a draft-3 document's allOf, arrived at from its own "extends",
	// dropped as a keyword draft 3 does not define. Gating first makes each
	// rewrite fire exactly where its source keyword survived the gate, which is
	// exactly the dialect that defines it.
	//
	// *A pass of its own*, because the rewrites also synthesize nodes -- draft
	// 3's {"disallow":["a","b"]} becomes a "not" holding an "anyOf", and draft 3
	// has no anyOf. A gate interleaved with the rewrites would reach that
	// synthesized node on the way down and clear the branch list it had just
	// built. The gate answers what the *document* states; the rewrites' output is
	// this package's internal spelling of what it states, and is not re-read.
	s.gateDialectKeywords(d)
	s.normalizeNode(d)
}

// gateDialectKeywords clears, over the whole tree, every keyword a node's own
// dialect does not define. A node declaring its own $schema takes that dialect,
// for itself and everything below it.
func (s *Schema) gateDialectKeywords(d Draft) {
	if s == nil || s.IsBooleanSchema() {
		return
	}
	s.dropKeywordsOutsideDialect(d)
	s.eachChild(func(sub *Schema) {
		child := d
		if own := DetectDraft(sub); own != DraftUnknown {
			child = own
		}
		sub.gateDialectKeywords(child)
	})
}

// normalizeInherited normalizes a nested node under the dialect it inherits,
// which its own $schema overrides for it and everything below it.
func (s *Schema) normalizeInherited(d Draft) {
	if s == nil || s.IsBooleanSchema() {
		return
	}
	if own := DetectDraft(s); own != DraftUnknown {
		d = own
	}
	s.normalizeNode(d)
}

func (s *Schema) normalizeNode(d Draft) {
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
	//
	// The gate is consulted here rather than left to the property's own pass
	// because the promotion happens on the parent: by the time the property is
	// normalized its boolean would already have become an entry in this schema's
	// required array, which no later dialect would recognise as draft 3's
	// spelling any more. The property's own pass still clears the leftover
	// sentinel where the promotion did not fire.
	if BooleanRequiredDefinedIn(d) {
		s.normalizeDraft3Required()
	}

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
	s.normalizeChildren(d)
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

// normalizeDependencies converts Draft 3-7's "dependencies" to
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

		// Draft 3 spells a single dependency as a bare property name --
		// {"dependencies":{"bar":"foo"}} -- where every later draft would write
		// the one-element array below. It is the same keyword and the same
		// meaning, so it normalizes to the same place.
		//
		// Without this arm the value fell through to the schema attempt, where
		// unmarshalling a JSON string into a Schema fails and the entry was
		// dropped in silence: the keyword was left enforcing nothing at all and
		// a schema stating only this one inferred no type either, so it came out
		// `type Root any` with no Validate. Recognising the shape here rather
		// than at inference is what keeps the three spellings of one keyword on
		// one path -- the array and object forms already worked, and only the
		// string form did not.
		if trimmed[0] == '"' {
			var dep string
			if json.Unmarshal(val, &dep) == nil {
				if s.DependentRequired == nil {
					s.DependentRequired = make(map[string][]string)
				}
				s.DependentRequired[key] = []string{dep}
				continue
			}
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

// normalizeChildren recursively normalizes all nested sub-schemas under the
// dialect they inherit from this one.
func (s *Schema) normalizeChildren(d Draft) {
	s.eachChild(func(sub *Schema) { sub.normalizeInherited(d) })
}

// eachChild calls fn for every sub-schema this node holds directly.
//
// It is the one traversal the dialect gate and the legacy rewrites share, so
// that a keyword position added to Schema cannot be reached by one and missed by
// the other -- the failure that let a $recursiveRef under propertyNames escape
// the pass that was meant to clear it.
func (s *Schema) eachChild(fn func(*Schema)) {
	visit := func(sub *Schema) {
		if sub != nil {
			fn(sub)
		}
	}
	for _, sub := range s.Properties {
		visit(sub)
	}
	for _, sub := range s.TypeSchemas {
		visit(sub)
	}
	for _, sub := range s.PatternProperties {
		visit(sub)
	}
	for _, sub := range s.Defs {
		visit(sub)
	}
	for _, sub := range s.Definitions {
		visit(sub)
	}
	for _, sub := range s.AllOf {
		visit(sub)
	}
	for _, sub := range s.AnyOf {
		visit(sub)
	}
	for _, sub := range s.OneOf {
		visit(sub)
	}
	for _, sub := range s.PrefixItems {
		visit(sub)
	}
	visit(s.Not)
	if s.Items != nil {
		visit(s.Items.Schema)
		for _, sub := range s.Items.Schemas {
			visit(sub)
		}
	}
	if s.AdditionalProperties != nil {
		visit(s.AdditionalProperties.Schema)
	}
	if s.AdditionalItems != nil {
		visit(s.AdditionalItems.Schema)
	}
	visit(s.If)
	visit(s.Then)
	visit(s.Else)
	visit(s.Contains)
	visit(s.PropertyNames)
	visit(s.ContentSchema)
	visit(s.UnevaluatedItems)
	visit(s.UnevaluatedProperties)
	for _, sub := range s.DependentSchemas {
		visit(sub)
	}
}
