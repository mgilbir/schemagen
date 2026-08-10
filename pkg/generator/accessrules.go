package generator

import (
	"sort"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// AccessStepKind names one move from a value to a value inside it.
//
// The steps below are the applicators that describe a *different* value than
// the schema object they are written in. The in-place ones -- allOf and $ref --
// are not steps at all: unconditionalReachAt folds them into the node being
// walked, because what they say is said about the same location. anyOf, oneOf,
// if/then/else, dependentSchemas and not are not steps either, and that is a
// decision rather than an omission: see accessRulesFor.
type AccessStepKind int

const (
	// AccessProperty is one named member of an object. Every rule ends in one:
	// readOnly means "do not accept this member" and writeOnly means "do not
	// write this member", and neither has an action at a location with no member
	// name -- an array element cannot be left out without changing the array's
	// length, which minItems can see.
	AccessProperty AccessStepKind = iota
	// AccessPattern is every member whose key matches an ECMA-262 pattern.
	AccessPattern
	// AccessOther is every member no `properties` names and no
	// `patternProperties` pattern matches: the value side of additionalProperties
	// and of unevaluatedProperties.
	AccessOther
	// AccessItems is every array element from Index onwards.
	AccessItems
	// AccessTuple is one array position.
	AccessTuple
)

// AccessStep is one move in an AccessRule's path.
type AccessStep struct {
	Kind AccessStepKind
	// Name is the member name for AccessProperty and the pattern for
	// AccessPattern.
	Name string
	// Index is the position for AccessTuple, and the first position reached for
	// AccessItems -- which is how unevaluatedItems is written, since it applies
	// to what a prefixItems tuple left over.
	Index int
	// Except and ExceptPatterns are what AccessOther steps past: the names the
	// same schema object declares and the patterns it matches.
	Except         []string
	ExceptPatterns []string
}

// AccessRule is one location --strict-read-write has something to say about,
// written as the path from the value a generated type holds down to the object
// member the keyword marks.
//
// The flat ReadOnlyKeys/WriteOnlyKeys lists on StructDef say the same thing for
// a member of the struct itself, which is the case a Go field covers and the
// only case they ever covered. These say it for the members below a value the
// generated code keeps as raw JSON -- a prefixItems slot, a contains element, a
// patternProperties value, anything under a type whose whole schema is held as
// data -- where there is no field, no nested type ever decodes, and until issue
// #219 the flag was therefore a silent no-op.
type AccessRule struct {
	Path      []AccessStep
	ReadOnly  bool
	WriteOnly bool
}

// maxAccessDepth and maxAccessRules bound the walk. A schema that refers to
// itself is stopped by the on-path visited set long before either, so these are
// for breadth rather than for recursion: a very wide schema should not turn a
// flag into a megabyte of tables. What is dropped is not enforcement lost
// outright -- a location deep enough to hit these is reached through some named
// type whose own rules cover it -- but the caps are deliberately generous.
const (
	maxAccessDepth = 24
	maxAccessRules = 4096
)

// accessRulesFor returns the readOnly/writeOnly locations beneath s, as paths
// from the value the generated type holds.
//
// minDepth is 2 for a struct, whose own members are already covered by the flat
// key lists the decoder and encoder carry, and 1 for a type that holds raw JSON
// and has no fields for a list to name.
//
// The walk descends through the applicators that name a value inside the one in
// hand, and through allOf and $ref, which name the same one. It does not descend
// through anyOf, oneOf, if/then/else, dependentSchemas or not, and that is the
// line 2020-12 §7.7.1 draws rather than a gap: a subschema that was not selected,
// or that failed, contributes no annotations, so a rule keyed on one would refuse
// a document the schema never marked, and drop a value the schema never said to
// drop. readWriteAtLocation and unconditionalReachAt draw the same line for the
// flat lists, and TestStrictReadWriteBindsWhereverThePropertyIs is the control
// that says so. A `not` is the sharper case of the same rule: a `not` that
// succeeds is a subschema that failed, and a failed subschema contributes
// nothing at all.
func (g *Generator) accessRulesFor(s *schema.Schema, minDepth int) []AccessRule {
	if s == nil || !g.config.StrictReadWrite {
		return nil
	}
	var out []AccessRule
	onPath := map[*schema.Schema]bool{}
	var walk func(node *schema.Schema, path []AccessStep)
	walk = func(node *schema.Schema, path []AccessStep) {
		if node == nil || len(path) >= maxAccessDepth || len(out) >= maxAccessRules {
			return
		}
		if onPath[node] {
			return
		}
		onPath[node] = true
		defer delete(onPath, node)

		reach := g.unconditionalReachAt(node, true)
		// The declared names and patterns of the whole reach, which is what an
		// AccessOther step has to walk past: additionalProperties skips what
		// `properties` and `patternProperties` claimed, and an allOf branch's
		// claims count as much as the node's own once the merge has folded them
		// together.
		var declared, patterns []string
		seenName := map[string]bool{}
		seenPattern := map[string]bool{}
		for _, r := range reach {
			for name := range r.Properties {
				if !seenName[name] {
					seenName[name] = true
					declared = append(declared, name)
				}
			}
			for pat := range r.PatternProperties {
				if !seenPattern[pat] {
					seenPattern[pat] = true
					patterns = append(patterns, pat)
				}
			}
		}
		sort.Strings(declared)
		sort.Strings(patterns)

		step := func(k AccessStepKind, name string, index int) []AccessStep {
			next := make([]AccessStep, len(path), len(path)+1)
			copy(next, path)
			st := AccessStep{Kind: k, Name: name, Index: index}
			if k == AccessOther {
				st.Except = declared
				st.ExceptPatterns = patterns
			}
			return append(next, st)
		}

		for _, r := range reach {
			for _, name := range sortedKeys(r.Properties) {
				ps := r.Properties[name]
				next := step(AccessProperty, name, 0)
				if len(next) >= minDepth {
					ro, wo := g.readWriteAtLocation(ps)
					if ro || wo {
						out = append(out, AccessRule{Path: next, ReadOnly: ro, WriteOnly: wo})
					}
				}
				walk(ps, next)
			}
			for _, pat := range sortedKeys(r.PatternProperties) {
				walk(r.PatternProperties[pat], step(AccessPattern, pat, 0))
			}
			// additionalProperties and unevaluatedProperties reach the same set
			// of members here. They are not the same keyword -- unevaluated also
			// counts what an in-place applicator evaluated -- but this walk does
			// not descend into those applicators at all, so at every location it
			// does reach, the two名 the same leftovers.
			for _, value := range []*schema.Schema{additionalPropertiesSchema(r), r.UnevaluatedProperties} {
				if value != nil {
					walk(value, step(AccessOther, "", 0))
				}
			}
			tuple := r.PrefixItems
			if !g.supportsPrefixItems(r) {
				tuple = nil
			}
			var itemsSchema *schema.Schema
			if r.Items != nil {
				if len(r.Items.Schemas) > 0 && len(tuple) == 0 {
					tuple = r.Items.Schemas
				} else if r.Items.Schema != nil {
					itemsSchema = r.Items.Schema
				}
			}
			for i, slot := range tuple {
				walk(slot, step(AccessTuple, "", i))
			}
			if itemsSchema != nil {
				walk(itemsSchema, step(AccessItems, "", len(tuple)))
			}
			if r.AdditionalItems != nil && len(tuple) > 0 && itemsSchema == nil {
				walk(r.AdditionalItems.AsSchema(), step(AccessItems, "", len(tuple)))
			}
			if r.UnevaluatedItems != nil {
				walk(r.UnevaluatedItems, step(AccessItems, "", len(tuple)))
			}
			// `contains` describes the elements it matches. Which those are is
			// the document's business, so this is the one descent here that is
			// not exact -- an element the sub-schema does not describe simply
			// carries no member the rule names, and the walk finds nothing at
			// it. It is included because the alternative is the position the
			// issue reports: the whole sub-schema compiled to a type nothing
			// decodes into, so `writeOnly` inside a `contains` was emitted
			// straight back out.
			if r.Contains != nil {
				walk(r.Contains, step(AccessItems, "", 0))
			}
		}
	}
	walk(s, nil)
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return accessRuleLess(out[i], out[j]) })
	return out
}

// additionalPropertiesSchema is the sub-schema an additionalProperties names, or
// nil where it is a boolean -- which forbids or permits members but describes
// none, so there is nothing below it to mark.
func additionalPropertiesSchema(s *schema.Schema) *schema.Schema {
	if s == nil || s.AdditionalProperties == nil || s.AdditionalProperties.Schema == nil {
		return nil
	}
	return s.AdditionalProperties.Schema
}

// accessRuleLess orders the emitted table. The rules refuse and delete the same
// things in any order, but generated source that changed between runs of one
// input would be unusable, and a reader diffing it needs a stable list.
func accessRuleLess(a, b AccessRule) bool {
	for i := 0; i < len(a.Path) && i < len(b.Path); i++ {
		x, y := a.Path[i], b.Path[i]
		if x.Kind != y.Kind {
			return x.Kind < y.Kind
		}
		if x.Name != y.Name {
			return x.Name < y.Name
		}
		if x.Index != y.Index {
			return x.Index < y.Index
		}
	}
	return len(a.Path) < len(b.Path)
}

// AccessRulesUsePatterns reports whether any rule matches a key by ECMA-262
// pattern, which is the one arm of the generated walker that needs the regexp
// engine. It is asked so that a package whose rules name no pattern does not
// acquire the dependency; the evaluator's AnnotationsPattern is the same
// decision for the same reason.
func AccessRulesUsePatterns(rules []AccessRule) bool {
	for _, r := range rules {
		for _, s := range r.Path {
			if s.Kind == AccessPattern {
				return true
			}
		}
	}
	return false
}
