package generator

import (
	"encoding/json"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// dynamicSupportedKeywords are the JSON Schema keywords the dynamic evaluator
// can express against a value whose Go type is not known statically.
//
// "$schema", "$id", "title", "description", "$comment", "default", "examples",
// "deprecated", "readOnly" and "writeOnly" constrain nothing, so a branch
// carrying only those is still representable.
var dynamicSupportedKeywords = map[string]bool{
	"type": true, "minimum": true, "maximum": true,
	"exclusiveMinimum": true, "exclusiveMaximum": true, "multipleOf": true,
	"minLength": true, "maxLength": true, "pattern": true,

	"$schema": false, "$id": false, "title": false, "description": false,
	"$comment": false, "default": false, "examples": false,
	"deprecated": false, "readOnly": false, "writeOnly": false,
}

// dynamicBranchChecks converts a sub-schema into checks evaluated against an
// untyped value.
//
// The second return value is false when the sub-schema uses any keyword the
// evaluator cannot express. That gate is the point of the function: emitting a
// branch that ignores one of its keywords would change which values match, so
// the caller must fall back to generating no validation rather than wrong
// validation. Representability is decided from the keywords actually present in
// the re-marshaled schema, not from a hand-maintained list of struct fields --
// a new keyword in the parser then fails closed instead of being silently
// dropped.
func dynamicBranchChecks(s *schema.Schema) ([]DynamicCheck, bool) {
	if s == nil || s.IsBooleanSchema() {
		return nil, false
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, false
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
		return nil, false
	}
	for key := range present {
		supported, known := dynamicSupportedKeywords[key]
		if !known {
			return nil, false
		}
		_ = supported
	}
	if len(s.Extensions) > 0 {
		return nil, false
	}

	var checks []DynamicCheck
	if len(s.Type) == 1 {
		checks = append(checks, DynamicCheck{Kind: "type", Value: s.Type[0]})
	} else if len(s.Type) > 1 {
		// A type union in a branch would need alternation the evaluator does
		// not express yet.
		return nil, false
	}
	if s.Minimum != nil {
		checks = append(checks, DynamicCheck{Kind: "minimum", Value: *s.Minimum})
	}
	if s.Maximum != nil {
		checks = append(checks, DynamicCheck{Kind: "maximum", Value: *s.Maximum})
	}
	if s.ExclusiveMinimum != nil {
		if s.ExclusiveMinimum.Number == nil {
			return nil, false // draft-4 boolean form modifies minimum; not expressed here
		}
		checks = append(checks, DynamicCheck{Kind: "exclusiveMinimum", Value: *s.ExclusiveMinimum.Number})
	}
	if s.ExclusiveMaximum != nil {
		if s.ExclusiveMaximum.Number == nil {
			return nil, false
		}
		checks = append(checks, DynamicCheck{Kind: "exclusiveMaximum", Value: *s.ExclusiveMaximum.Number})
	}
	if s.MultipleOf != nil {
		checks = append(checks, DynamicCheck{Kind: "multipleOf", Value: *s.MultipleOf})
	}
	if s.MinLength != nil {
		checks = append(checks, DynamicCheck{Kind: "minLength", Value: s.MinLength.Int()})
	}
	if s.MaxLength != nil {
		checks = append(checks, DynamicCheck{Kind: "maxLength", Value: s.MaxLength.Int()})
	}
	if s.Pattern != nil {
		checks = append(checks, DynamicCheck{Kind: "pattern", Value: *s.Pattern})
	}
	// A branch with no checks matches everything; that is meaningful for oneOf
	// counting, so an empty slice is a valid result.
	return checks, true
}

// dynamicBranches converts a list of sub-schemas, failing closed if any one of
// them is not representable.
func dynamicBranches(subs []*schema.Schema) ([][]DynamicCheck, bool) {
	if len(subs) == 0 {
		return nil, false
	}
	out := make([][]DynamicCheck, 0, len(subs))
	for _, sub := range subs {
		checks, ok := dynamicBranchChecks(sub)
		if !ok {
			return nil, false
		}
		out = append(out, checks)
	}
	return out, true
}

// dynamicSchemaDef builds a DynamicSchemaDef for a root schema whose only
// constraints come from applicators, or returns nil when those constraints
// cannot be expressed against an untyped value. Returning nil is the signal to
// fall back to the historical `type X any` with no validation: partial
// validation would reject values the schema allows.
func (g *Generator) dynamicSchemaDef(name string, s *schema.Schema) *DynamicSchemaDef {
	if !g.validationKeywordsEnabled() {
		return nil
	}
	// Only take over a schema whose constraints are entirely the applicators
	// handled here. Anything else -- a $ref, allOf, not, properties -- is
	// already handled by a dedicated path, and hijacking it would drop that
	// handling: {"$ref":"...","if":{...}} takes its constraint from the $ref,
	// and answering it with an if/then evaluator silently accepts everything.
	if !dynamicRootKeywordsOnly(s) {
		return nil
	}

	def := &DynamicSchemaDef{Name: name, Description: s.Description}

	if len(s.OneOf) > 0 {
		branches, ok := dynamicBranches(s.OneOf)
		if !ok {
			return nil
		}
		def.OneOf = branches
	}
	if len(s.AnyOf) > 0 {
		branches, ok := dynamicBranches(s.AnyOf)
		if !ok {
			return nil
		}
		def.AnyOf = branches
	}
	// An "if" with neither "then" nor "else" constrains nothing, so there is no
	// validation to generate for it.
	if s.If != nil && (s.Then != nil || s.Else != nil) {
		ifChecks, ok := dynamicBranchChecks(s.If)
		if !ok {
			return nil
		}
		def.HasIfThenElse = true
		def.If = ifChecks
		if s.Then != nil {
			thenChecks, ok := dynamicBranchChecks(s.Then)
			if !ok {
				return nil
			}
			def.Then, def.HasThen = thenChecks, true
		}
		if s.Else != nil {
			elseChecks, ok := dynamicBranchChecks(s.Else)
			if !ok {
				return nil
			}
			def.Else, def.HasElse = elseChecks, true
		}
	}

	if len(def.OneOf) == 0 && len(def.AnyOf) == 0 && !def.HasIfThenElse {
		return nil
	}
	return def
}

// dynamicRootKeywordsRoot lists the keywords a root schema may carry for the
// dynamic evaluator to own it. Applicators it implements, plus annotations that
// constrain nothing.
var dynamicRootKeywords = map[string]bool{
	"oneOf": true, "anyOf": true, "if": true, "then": true, "else": true,

	"$schema": true, "$id": true, "title": true, "description": true,
	"$comment": true, "default": true, "examples": true,
	"deprecated": true, "readOnly": true, "writeOnly": true,
}

// dynamicRootKeywordsOnly reports whether every keyword on s is one the dynamic
// evaluator owns. Decided from the re-marshaled schema so a keyword the
// generator learns later fails closed here too.
func dynamicRootKeywordsOnly(s *schema.Schema) bool {
	if s == nil || len(s.Extensions) > 0 {
		return false
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return false
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
		return false
	}
	for key := range present {
		if !dynamicRootKeywords[key] {
			return false
		}
	}
	return true
}
