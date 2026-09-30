package generator

import (
	"github.com/mgilbir/schemagen/pkg/schema"
)

// Provenance says which keyword of which schema node an element of the IR
// enforces. It is what the keyword ledger reads (see ledger.go): a claim that a
// keyword is carried is taken from the element that carries it, in the IR that
// is finally emitted, and never from the arm that meant to build one.
//
// The distinction is the whole point. Several passes run after an arm has
// built a check -- the field-rule filter that drops what a Go type cannot
// compile, withoutFormatRules and withoutContentRules, resolveItemValidations,
// resolvePatternPropertyTypes -- and each of them may remove an element the arm
// built. An arm that recorded "I enforced minLength" as it went would go on
// saying so after the element was gone. An element that carries its own
// provenance is gone with it, so the ledger cannot count a check that is not
// in the generated code.
//
// Source is the node the element was built from. It is often a node the
// generator synthesized rather than one the document wrote -- the merge target
// generateAllOfDef builds from a schema and its allOf branches, a value copy
// with a keyword rewritten, the allOf node conjoinAllOfProperty makes of two
// contributions to one property. The ledger maps such a node back to the nodes
// the document wrote, and credits one of those with the keyword only where the
// synthesized value implies the value that node wrote; see ledgerAuthors and
// impliesKeyword. That is how a merge that kept the first of two `pattern`s is
// seen to have carried one and dropped the other.
//
// Keyword is the JSON Schema keyword, spelled as the document's dialect spells
// it after normalization ("dependentSchemas", not "dependencies").
//
// Evaluated is set on an element that carries a runtime-evaluator literal: the
// sets of nodes the literals it runs compile whole. The evaluator refuses a
// schema carrying a keyword it does not model (nodeBuilder.keywordsOnly), so a
// literal that was built at all enforces every keyword each of these nodes
// states.
//
// Nothing in the emitter reads any of this; it is provenance, not code.
type Provenance struct {
	Source    *schema.Schema
	Keyword   string
	Evaluated []*EvaluatorNodes
}

// EvaluatorNodes is the set of schema nodes one evaluator literal compiles
// whole, in the order the build first met them.
//
// It is a value of its own, referred to by pointer, so that one compiled
// literal is one set however many elements run it: an element names the sets
// it runs, and the ledger credits each set's nodes by asking the set, once per
// node it meets, rather than copying the set into every declaration that
// reaches it. A literal shared by several types is one set shared by them.
type EvaluatorNodes struct {
	Nodes []*schema.Schema
	has   map[*schema.Schema]bool
}

// NewEvaluatorNodes is the set of nodes, in the order given.
func NewEvaluatorNodes(nodes []*schema.Schema) *EvaluatorNodes {
	has := make(map[*schema.Schema]bool, len(nodes))
	for _, n := range nodes {
		has[n] = true
	}
	return &EvaluatorNodes{Nodes: nodes, has: has}
}

// Has reports whether the literal compiles n whole.
func (e *EvaluatorNodes) Has(n *schema.Schema) bool {
	return e != nil && e.has[n]
}

// claimOf is the provenance of an element enforcing one keyword of src.
func claimOf(src *schema.Schema, keyword string) Provenance {
	return Provenance{Source: src, Keyword: keyword}
}

// ruleKeywords maps a ValidationRule's RuleType to the keyword it enforces,
// for the rule types whose name is not already the keyword.
//
// The "pp" rule types are the patternProperties fallback's spelling of the
// ordinary ones (extractPatternPropertyValidationRules); "content" carries
// contentEncoding and contentMediaType together and is resolved by the ledger
// against what its ContentCheck holds. "forbidden", "never" and the synthesized
// "maxItems" of a closed tuple are set at their construction sites, which know
// which keyword produced them.
var ruleKeywords = map[string]string{
	"ppType":             "type",
	"ppMinimum":          "minimum",
	"ppMaximum":          "maximum",
	"ppExclusiveMinimum": "exclusiveMinimum",
	"ppExclusiveMaximum": "exclusiveMaximum",
	"ppMultipleOf":       "multipleOf",
	"ppMinLength":        "minLength",
	"ppMaxLength":        "maxLength",
	"ppPattern":          "pattern",
	"ppMinItems":         "minItems",
	"ppMaxItems":         "maxItems",
}

// ruleKeyword is the keyword a rule of this type enforces when nothing more
// specific was recorded at its construction site.
func ruleKeyword(ruleType string) string {
	if kw, ok := ruleKeywords[ruleType]; ok {
		return kw
	}
	return ruleType
}

// claimRules stamps every rule built from src that does not carry a
// provenance yet with src and the keyword its rule type names. It returns the
// slice for chaining.
//
// A rule that already carries one keeps it: the construction site knew better
// (a "maxItems" synthesized from a closed tuple is the tuple's keyword, not a
// maxItems the document wrote).
func claimRules(rules []ValidationRule, src *schema.Schema) []ValidationRule {
	for i := range rules {
		if rules[i].Claim.Source == nil {
			rules[i].Claim = claimOf(src, ruleKeyword(rules[i].RuleType))
		}
	}
	return rules
}

// claimContainsChecks stamps element checks built from src; CheckType is the
// keyword itself.
func claimContainsChecks(checks []ContainsCheck, src *schema.Schema) []ContainsCheck {
	for i := range checks {
		if checks[i].Claim.Source == nil {
			checks[i].Claim = claimOf(src, checks[i].CheckType)
		}
	}
	return checks
}

// claimDynamicChecks stamps dynamic checks built from src; Kind is the keyword
// itself.
func claimDynamicChecks(checks []DynamicCheck, src *schema.Schema) []DynamicCheck {
	for i := range checks {
		if checks[i].Claim.Source == nil {
			checks[i].Claim = claimOf(src, checks[i].Kind)
		}
	}
	return checks
}

// closedTupleKeyword names the keyword that closes a tuple closedTupleMaxItems
// found closed: additionalItems:false beside array-form items, or 2020-12's
// items:false beside prefixItems.
func closedTupleKeyword(s *schema.Schema) string {
	if s.AdditionalItems != nil && s.AdditionalItems.Bool != nil && !*s.AdditionalItems.Bool &&
		s.Items != nil && len(s.Items.Schemas) > 0 {
		return "additionalItems"
	}
	return "items"
}

// evaluatorClaim is the provenance of an element carrying a runtime-evaluator
// literal compiled by b for keyword of owner.
func evaluatorClaim(owner *schema.Schema, keyword string, b *nodeBuilder) Provenance {
	return Provenance{Source: owner, Keyword: keyword, Evaluated: []*EvaluatorNodes{b.takeCompiled()}}
}
