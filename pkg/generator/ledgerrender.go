package generator

import (
	"maps"
	"sort"

	"github.com/mgilbir/schemagen/internal/gentest"
)

// This file is what the keyword ledger knows about the emitter: which kinds of
// validation rule each list of rules in the IR is rendered for.
//
// The ledger reads its claims off the IR, on the understanding that every
// element of it becomes code. For a list of ValidationRule that is not so: each
// template walks its list with an `if eq .RuleType "..."` per kind of rule it
// knows, and a rule of any other kind is passed over without a word. An alias
// wrapper's template has no arm for "forbidden", so {"enum":[]} beside a
// format that made the position such a wrapper put a rule in the IR that no
// code checks -- and a ledger crediting it would have said the empty enum was
// enforced while {"p":1} was accepted. So a rule is credited only where the
// template of its list renders its kind.
//
// The table is written out rather than derived, because this package cannot
// see the templates; TestLedgerKnowsWhatTheTemplatesRender in pkg/emitter reads
// the templates' parse trees and fails when the two differ.

// ruleList names one list of ValidationRule in the IR by the template range
// that renders it.
type ruleList string

const (
	rulesStruct           ruleList = "struct"            // StructDef.Validations
	rulesStructNonObject  ruleList = "struct-non-object" // StructDef.NonObjectValidations
	rulesPatternProperty  ruleList = "pattern-property"  // PatternPropertyDef.Validations
	rulesAlias            ruleList = "alias"             // AliasDef.Validations
	rulesAliasVariant     ruleList = "alias-variant"     // AliasDef.AnyOfVariants / OneOfVariants
	rulesInferred         ruleList = "inferred"          // InferredAliasDef.Validations
	rulesInferredVariant  ruleList = "inferred-variant"  // InferredAliasDef.AnyOfVariants / OneOfVariants
	rulesBigInt           ruleList = "bigint"            // BigIntAliasDef.Validations
	rulesBigIntVariant    ruleList = "bigint-variant"    // BigIntAliasDef.AnyOfVariants / OneOfVariants
	rulesItemLevel        ruleList = "item-level"        // ItemLevel.Rules
	rulesNotBranch        ruleList = "not-branch"        // NotSchemaBranch.Validations
	rulesOneOfVariant     ruleList = "oneof-variant"     // OneOfVariant.Checks
	rulesUnevalProperties ruleList = "uneval-properties" // UnevaluatedPropertiesDef.Validations

	// The lists of ContainsCheck, judged by CheckType the same way.
	checksContains             ruleList = "contains"                // ContainsDef.Checks
	checksUnevalItems          ruleList = "uneval-items"            // UnevaluatedItemsDef.Checks
	checksInferredItems        ruleList = "inferred-items"          // InferredAliasDef.ItemsChecks
	checksInferredContainsEval ruleList = "inferred-contains-evals" // InferredAliasDef.Contains.Checks, read to mark items contains evaluates
)

func ruleTypes(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// renderedRuleTypes is, for each rule list, the rule kinds its template
// renders a check for.
var renderedRuleTypes = map[ruleList]map[string]bool{
	rulesStruct: ruleTypes("const", "content", "exclusiveMaximum", "exclusiveMinimum", "forbidden", "format",
		"maxItems", "maxLength", "maxProperties", "maximum", "minItems", "minLength", "minProperties", "minimum",
		"multipleOf", "pattern", "uniqueItems"),
	rulesStructNonObject: ruleTypes("ppExclusiveMaximum", "ppExclusiveMinimum", "ppMaxItems", "ppMaxLength",
		"ppMaximum", "ppMinItems", "ppMinLength", "ppMinimum", "ppMultipleOf", "ppPattern", "ppType"),
	rulesPatternProperty: ruleTypes("ppExclusiveMaximum", "ppExclusiveMinimum", "ppMaxItems", "ppMaxLength",
		"ppMaximum", "ppMinItems", "ppMinLength", "ppMinimum", "ppMultipleOf", "ppPattern", "ppType"),
	rulesAlias: ruleTypes("content", "exclusiveMaximum", "exclusiveMinimum", "format", "maxItems", "maxLength",
		"maximum", "minItems", "minLength", "minimum", "multipleOf", "pattern", "uniqueItems"),
	rulesAliasVariant: ruleTypes("exclusiveMaximum", "exclusiveMinimum", "maxItems", "maxLength", "maximum",
		"minItems", "minLength", "minimum", "multipleOf", "never", "pattern"),
	rulesInferred: ruleTypes("content", "exclusiveMaximum", "exclusiveMinimum", "format", "maxItems", "maxLength",
		"maximum", "minItems", "minLength", "minimum", "multipleOf", "pattern", "uniqueItems"),
	rulesInferredVariant: ruleTypes("exclusiveMaximum", "exclusiveMinimum", "maxItems", "maxLength", "maximum",
		"minItems", "minLength", "minimum", "multipleOf", "never", "pattern"),
	rulesBigInt:        ruleTypes("exclusiveMaximum", "exclusiveMinimum", "maximum", "minimum", "multipleOf"),
	rulesBigIntVariant: ruleTypes("exclusiveMaximum", "exclusiveMinimum", "maximum", "minimum", "multipleOf", "never"),
	rulesItemLevel: ruleTypes("const", "content", "exclusiveMaximum", "exclusiveMinimum", "format", "maxItems",
		"maxLength", "maximum", "minItems", "minLength", "minimum", "multipleOf", "pattern", "uniqueItems"),
	rulesNotBranch: ruleTypes("exclusiveMaximum", "exclusiveMinimum", "maxItems", "maxLength", "maximum",
		"minItems", "minLength", "minimum", "multipleOf", "pattern"),
	rulesOneOfVariant: ruleTypes("exclusiveMaximum", "exclusiveMinimum", "maxItems", "maxLength", "maximum",
		"minItems", "minLength", "minimum", "multipleOf", "pattern"),
	rulesUnevalProperties: ruleTypes("const", "exclusiveMaximum", "exclusiveMinimum", "maxLength", "maximum",
		"minLength", "minimum", "multipleOf", "pattern"),

	checksContains: ruleTypes("exclusiveMaximum", "exclusiveMinimum", "maxLength", "maximum", "minLength",
		"minimum", "multipleOf", "pattern", "type"),
	checksUnevalItems: ruleTypes("exclusiveMaximum", "exclusiveMinimum", "maxLength", "maximum", "minLength",
		"minimum", "multipleOf", "pattern"),
	checksInferredItems: ruleTypes("exclusiveMaximum", "exclusiveMinimum", "maximum", "minimum", "multipleOf",
		"type"),
	checksInferredContainsEval: ruleTypes("exclusiveMaximum", "exclusiveMinimum", "maximum", "minimum",
		"multipleOf", "pattern", "type"),
}

// typedFormatDecodeEnforces says, for each format a field is decoded into a Go
// type for rather than checked as a string (formatGoType), whether that
// decode, with the check a "format" rule makes of the decoded value (the
// address family; a rule that is not StringBacked), refuses every string the
// format excludes. Where it does not, neither the field's Go type nor that
// rule carries `format`, and nothing else in the IR does.
//
// It is written down, not assumed, and TestTypedFormatDecodes in tests holds
// it to the generated code: it decodes and validates strings each format
// excludes, and strings it admits, through a generated field of that type, and
// fails when an entry says the decode enforces the format and an excluded
// string gets through, or says it does not and none does. Today none does: time.Time
// takes a -24:00 offset, and netip.Addr takes "" for ipv4 and ipv6 and a
// zoned address for ipv6.
var typedFormatDecodeEnforces = map[string]bool{
	"date-time": false,
	"ipv4":      false,
	"ipv6":      false,
}

// renderedChecks keeps the checks of list whose CheckType its template
// renders.
func renderedChecks(checks []ContainsCheck, list ruleList) []ContainsCheck {
	kinds := renderedRuleTypes[list]
	var out []ContainsCheck
	for _, c := range checks {
		if kinds[c.CheckType] {
			out = append(out, c)
		}
	}
	return out
}

// rendered keeps the rules of list whose kind its template renders.
func rendered(rules []ValidationRule, list ruleList) []ValidationRule {
	kinds := renderedRuleTypes[list]
	for i, r := range rules {
		if kinds[r.RuleType] {
			continue
		}
		// One the template passes over: copy the rest without it.
		out := append([]ValidationRule(nil), rules[:i]...)
		for _, r := range rules[i+1:] {
			if kinds[r.RuleType] {
				out = append(out, r)
			}
		}
		return out
	}
	return rules
}

func init() {
	gentest.LedgerTypedFormatDecodes = func() map[string]bool {
		return maps.Clone(typedFormatDecodeEnforces)
	}
	gentest.LedgerRenderedRuleTypes = func() map[string][]string {
		out := make(map[string][]string, len(renderedRuleTypes))
		// maporder: each list is sorted, and the result is a map.
		for list, kinds := range renderedRuleTypes {
			var names []string
			// maporder: sorted below.
			for k := range kinds {
				names = append(names, k)
			}
			sort.Strings(names)
			out[string(list)] = names
		}
		return out
	}
}
