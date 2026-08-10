package generator

import (
	"fmt"
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
// validation. Representability is decided from schemaKeywordSet, not from a
// hand-maintained list of struct fields -- a new keyword in the parser then
// fails closed instead of being silently dropped.
//
// That set is what refuses `"enum": []`, which admits nothing and so says what
// the boolean `false` schema on the first line says. A branch with no checks
// matches everything, so {"oneOf":[{"enum":[]},{"type":"string"}]} counted two
// matches for "x" and refused a document the schema permits -- the one failure
// this generator treats as worse than a missing check. It used to be caught by a
// local emptyEnumSchema test here, because `enum` is tagged omitempty and left no
// key behind; the same reading now covers `"const": null`, which nothing here
// caught and which made {"anyOf":[{"const":null}]} a branch matching every value.
// Refusing the branch hands the whole schema to the runtime evaluator, which is
// where a `false` branch is already answered.
func dynamicBranchChecks(s *schema.Schema) ([]DynamicCheck, bool) {
	if s == nil || s.IsBooleanSchema() {
		return nil, false
	}
	present, ok := schemaKeywordSet(s)
	if !ok {
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

	checks, whole := modelledChecks(s)
	if !whole {
		return nil, false
	}
	// A branch with no checks matches everything; that is meaningful for oneOf
	// counting, so an empty slice is a valid result.
	return checks, true
}

// modelledChecks converts the keywords the evaluator models into checks and
// says, through its second return value, whether those checks are the whole of
// what the keywords it looked at demand.
//
// It is the emission half of dynamicBranchChecks, split out so that a caller
// which may safely under-enforce can take the checks without the gate. It does
// not judge the keywords it does not model -- dynamicBranchChecks' own gate
// does that, from the re-marshaled key set, and stays the only place that
// decides representability.
//
// whole is false where a keyword it models is written in a form the checks
// cannot carry: a type union needs alternation the evaluator does not express,
// a draft 3 "type" whose entries are schemas rather than names widens the union
// past anything a name can say, and draft-4's boolean
// exclusiveMinimum/exclusiveMaximum modify their sibling `minimum`/`maximum`
// rather than standing on their own, so the check emitted for that sibling is
// weaker than the schema. All three are safe to keep only where weaker is
// acceptable.
//
// The schema-valued "type" is the one of the three the caller's gate cannot see
// for itself: TypeSchemas is tagged "-", so schemaKeywordSet reports the keyword
// (it is a "type" like any other) while the emission below finds an empty
// s.Type and would have said the branch demands nothing at all.
func modelledChecks(s *schema.Schema) ([]DynamicCheck, bool) {
	whole := true
	var checks []DynamicCheck
	if len(s.TypeSchemas) > 0 {
		whole = false
	}
	if len(s.Type) == 1 {
		checks = append(checks, DynamicCheck{Kind: "type", Value: s.Type[0]})
	} else if len(s.Type) > 1 {
		whole = false
	}
	if s.Minimum != nil {
		checks = append(checks, DynamicCheck{Kind: "minimum", Value: *s.Minimum})
	}
	if s.Maximum != nil {
		checks = append(checks, DynamicCheck{Kind: "maximum", Value: *s.Maximum})
	}
	if s.ExclusiveMinimum != nil {
		if s.ExclusiveMinimum.Number == nil {
			whole = false
		} else {
			checks = append(checks, DynamicCheck{Kind: "exclusiveMinimum", Value: *s.ExclusiveMinimum.Number})
		}
	}
	if s.ExclusiveMaximum != nil {
		if s.ExclusiveMaximum.Number == nil {
			whole = false
		} else {
			checks = append(checks, DynamicCheck{Kind: "exclusiveMaximum", Value: *s.ExclusiveMaximum.Number})
		}
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
	return checks, whole
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

	def := &DynamicSchemaDef{Name: name, Description: s.Description, Annotations: annotationsOf(s)}

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

// objectConditionalKeywords lists the keywords the *condition* of an
// object-level if/then/else may carry. Only object shape is modelled here:
// which properties must be present, and what each named property must look
// like. An `if` that says anything else is not expressible, and the whole group
// is dropped rather than the condition decided with a keyword ignored.
//
// `then` and `else` are not held to this list; see
// objectConditionalBranchLenient for why they need not be.
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
	encoded, err := constJSONValue(value)
	if err != nil {
		return nil, false
	}
	return append(checks, DynamicCheck{Kind: "const", Value: string(encoded)}), true
}

// objectConditionalBranch converts one side of an object-level if/then/else.
//
// Everything about the branch has to be expressible for it to be used at all.
// This is the rule for the `if`, which decides which of `then` and `else`
// applies: a condition evaluated with one of its keywords ignored picks the
// wrong branch and turns a document the schema allows into a rejection.
// `then` and `else` go through objectConditionalBranchLenient instead.
//
// schemaKeywordSet rather than the marshaled key set alone, so that a condition
// stating only `"enum": []` or `"const": null` is refused rather than read as a
// condition stating nothing -- which is a condition that always holds, so `then`
// would apply to every document. As at dynamicRootKeywordsOnly, no schema under
// testdata/schemas generates differently either way today: a group whose `if`
// says nothing about object shape is dropped before this is reached. The reading
// is shared because leaving one gate on the lossy form is what issue #154 is.
func objectConditionalBranch(keyword string, s *schema.Schema) (*ObjectConditionalBranch, bool) {
	if s == nil || s.IsBooleanSchema() || len(s.Extensions) > 0 {
		return nil, false
	}
	present, ok := schemaKeywordSet(s)
	if !ok {
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

// schemaCarriesRef reports whether s references another schema.
//
// A reference is the one keyword whose presence can make its siblings mean
// something other than what they say: before draft 2019-09 a `$ref` replaces
// the schema object it sits in, so a sibling `properties` beside one does not
// apply at all. Enforcing it anyway would reject documents the schema allows,
// which is precisely what the partial enforcement below must never do -- so a
// branch or property carrying a reference is left alone entirely rather than
// read in part.
func schemaCarriesRef(s *schema.Schema) bool {
	return s.EffectiveRef() != "" || s.DynamicRef != ""
}

// objectPropertyChecksLenient converts a property sub-schema of a `then` or
// `else` branch into the checks it can express, ignoring the keywords it
// cannot rather than refusing the property.
//
// It is objectPropertyChecks with the gate removed, and it is sound only in
// that position. A schema object's keywords are conjunctive, so a subset of
// them accepts a superset of the values: a property judged by part of its
// sub-schema can only let a wrong value through, never refuse a right one.
// The exceptions are handled rather than assumed -- a reference (see
// schemaCarriesRef), and draft 3's schema-valued `type` entries, which turn
// `type` into an alternation the surviving check would read as a demand.
//
// A sub-schema whose keywords are all unmodelled yields no checks and the
// caller drops the property, which is the same under-enforcement one step up.
func objectPropertyChecksLenient(s *schema.Schema) []DynamicCheck {
	if s == nil || s.IsBooleanSchema() || len(s.Extensions) > 0 || schemaCarriesRef(s) {
		return nil
	}
	checks, _ := modelledChecks(s)
	if len(s.TypeSchemas) > 0 {
		kept := checks[:0]
		for _, c := range checks {
			if c.Kind != "type" {
				kept = append(kept, c)
			}
		}
		checks = kept
	}
	if s.Const != nil || s.ConstIsNull {
		var value any
		if s.Const != nil {
			value = *s.Const
		}
		encoded, err := constJSONValue(value)
		if err != nil {
			return nil
		}
		checks = append(checks, DynamicCheck{Kind: "const", Value: string(encoded)})
	}
	return checks
}

// objectConditionalBranchLenient converts `then` or `else` into the part of
// itself the generated check can carry, dropping what it cannot express
// instead of dropping the group.
//
// This is the asymmetry between the condition and the consequence. `if` decides
// which of the two branches applies, so reading it with a keyword ignored picks
// the wrong branch and turns a document the schema allows into a rejection --
// it stays on objectConditionalBranch's exact-or-nothing rule. `then` and
// `else` only ever add demands to a branch already selected, and their keywords
// are conjunctive, so enforcing a subset of them can under-enforce and nothing
// worse. Held to the same bar as `if`, a single `items` inside a `then` cost
// the whole group its check, condition included.
//
// A branch carrying a reference is still refused whole (see schemaCarriesRef),
// and so is one carrying a keyword the parser did not recognize: an unmodelled
// standard keyword is known to be a conjunct that can be dropped, whereas
// nothing is known about a keyword schemagen has never seen. Both refusals cost
// only this branch; the other side and the condition survive.
func objectConditionalBranchLenient(keyword string, s *schema.Schema) *ObjectConditionalBranch {
	if s == nil || s.IsBooleanSchema() || len(s.Extensions) > 0 || schemaCarriesRef(s) {
		return nil
	}
	branch := &ObjectConditionalBranch{Keyword: keyword}
	branch.RequiredKeys = append(branch.RequiredKeys, s.Required...)
	sort.Strings(branch.RequiredKeys)
	for _, name := range sortedKeys(s.Properties) {
		checks := objectPropertyChecksLenient(s.Properties[name])
		if len(checks) == 0 {
			continue
		}
		branch.Properties = append(branch.Properties, ObjectPropertyConstraint{
			JSONName: name,
			Checks:   checks,
		})
	}
	return branch
}

// dependentSchemaKeyword names a dependentSchemas branch in an error message,
// the way "then" and "else" name the two sides of an object-level conditional.
// The trigger is quoted the way the other dependentSchema messages quote it;
// the emitter escapes the whole string before it reaches a format literal, so a
// trigger carrying a quote or a percent sign cannot break the generated source.
func dependentSchemaKeyword(trigger string) string {
	return fmt.Sprintf("dependentSchema %q", trigger)
}

// objectConditionalDef builds the check for an object-level if/then/else, or
// returns nil when the condition is outside what objectConditionalBranch
// expresses or neither consequence leaves anything to check.
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
		if then := objectConditionalBranchLenient("then", s.Then); !then.Empty() {
			def.Then = then
		}
	}
	if s.Else != nil {
		if elseBranch := objectConditionalBranchLenient("else", s.Else); !elseBranch.Empty() {
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
// evaluator owns. Decided from schemaKeywordSet so a keyword the generator
// learns later fails closed here too.
//
// Reading the hidden spellings changes no answer this gate gives today, and that
// was measured rather than assumed: every schema under testdata/schemas
// generates identically with the marshaled key set alone. Both spellings are
// claimed before a root reaches here -- generateTypeDef answers `"enum": []`
// with its forbidden arm and promotes `"const": null` to a single-member enum --
// so neither survives to be read. It uses the shared reading anyway because the
// alternative is one gate of five left on a form known to drop keywords, which
// is how the four defects issue #154 collects came to be four rather than one.
func dynamicRootKeywordsOnly(s *schema.Schema) bool {
	if s == nil || len(s.Extensions) > 0 {
		return false
	}
	present, ok := schemaKeywordSet(s)
	if !ok {
		return false
	}
	for key := range present {
		if !dynamicRootKeywords[key] {
			return false
		}
	}
	return true
}
