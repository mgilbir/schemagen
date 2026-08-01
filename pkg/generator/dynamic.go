package generator

import (
	"encoding/json"
	"sort"

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

// objectConditionalKeywords lists the keywords a branch of an object-level
// if/then/else may carry. Only object shape is modelled here: which properties
// must be present, and what each named property must look like. A branch that
// says anything else is not expressible, and the whole group is dropped rather
// than checked with a keyword ignored.
var objectConditionalKeywords = map[string]bool{
	"properties": true, "required": true,

	"$schema": true, "$id": true, "title": true, "description": true,
	"$comment": true, "default": true, "examples": true,
	"deprecated": true, "readOnly": true, "writeOnly": true,
}

// objectPropertyChecks converts a property sub-schema of a conditional branch
// into checks against that property's decoded JSON value.
//
// It is dynamicBranchChecks plus "const", which the conditional needs and the
// applicator evaluator has no use for: the shape that motivates this is a
// discriminator, {"if":{"properties":{"kind":{"const":"x"}}}}. The constant is
// marshaled here and compared against the marshaled instance value, so the two
// encodings are produced by the same code and differ only when the values do.
//
// The second return value is false when the sub-schema uses anything else, and
// the caller must then drop the whole group.
func objectPropertyChecks(s *schema.Schema) ([]DynamicCheck, bool) {
	if s == nil || s.IsBooleanSchema() {
		return nil, false
	}
	if s.Const == nil && !s.ConstIsNull {
		return dynamicBranchChecks(s)
	}
	// Strip const and let dynamicBranchChecks pass judgement on what is left,
	// so a keyword neither of us models still fails closed there.
	rest := *s
	rest.Const = nil
	rest.ConstIsNull = false
	checks, ok := dynamicBranchChecks(&rest)
	if !ok {
		return nil, false
	}
	var value any
	if s.Const != nil {
		value = *s.Const
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	return append(checks, DynamicCheck{Kind: "const", Value: string(encoded)}), true
}

// objectConditionalBranch converts one side of an object-level if/then/else.
//
// Everything about the branch has to be expressible for it to be used at all.
// The `if` decides which of `then` and `else` applies, so a condition evaluated
// with one of its keywords ignored picks the wrong branch and turns a document
// the schema allows into a rejection. `then` and `else` are held to the same
// bar for a simpler reason: a branch checked in part is a check whose meaning
// nobody can state.
func objectConditionalBranch(keyword string, s *schema.Schema) (*ObjectConditionalBranch, bool) {
	if s == nil || s.IsBooleanSchema() || len(s.Extensions) > 0 {
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
		if !objectConditionalKeywords[key] {
			return nil, false
		}
	}

	branch := &ObjectConditionalBranch{Keyword: keyword}
	branch.RequiredKeys = append(branch.RequiredKeys, s.Required...)
	sort.Strings(branch.RequiredKeys)
	for _, name := range sortedKeys(s.Properties) {
		checks, ok := objectPropertyChecks(s.Properties[name])
		if !ok {
			return nil, false
		}
		if len(checks) == 0 {
			// A property schema that constrains nothing is satisfied by every
			// value; emitting it would generate `if !(true)`.
			continue
		}
		branch.Properties = append(branch.Properties, ObjectPropertyConstraint{
			JSONName: name,
			Checks:   checks,
		})
	}
	return branch, true
}

// objectConditionalDef builds the check for an object-level if/then/else, or
// returns nil when any part of it is outside what the branches above express.
func objectConditionalDef(s *schema.Schema) *ObjectConditionalDef {
	if s == nil || s.If == nil || (s.Then == nil && s.Else == nil) {
		return nil
	}
	ifBranch, ok := objectConditionalBranch("if", s.If)
	if !ok || ifBranch.Empty() {
		// A condition that constrains nothing is matched by every object, so
		// `else` is unreachable and `then` is unconditional. Neither is this
		// check's business: the unconditional reading belongs to whatever
		// flattens `then` into the struct.
		return nil
	}
	def := &ObjectConditionalDef{If: *ifBranch}
	if s.Then != nil {
		then, ok := objectConditionalBranch("then", s.Then)
		if !ok {
			return nil
		}
		if !then.Empty() {
			def.Then = then
		}
	}
	if s.Else != nil {
		elseBranch, ok := objectConditionalBranch("else", s.Else)
		if !ok {
			return nil
		}
		if !elseBranch.Empty() {
			def.Else = elseBranch
		}
	}
	if def.Then == nil && def.Else == nil {
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
