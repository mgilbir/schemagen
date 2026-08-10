package generator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// AccessStepKind names one move from a value to a value inside it.
//
// The steps below are the applicators that describe a *different* value than
// the schema object they are written in. The in-place ones -- allOf and $ref --
// are not steps at all: unconditionalReachAt folds them into the node being
// walked, because what they say is said about the same location. anyOf, oneOf,
// if/then/else, dependentSchemas and not are not steps either, for the same
// reason: they describe the value in hand, and differ from the two above only in
// describing it on some documents rather than all. accessRulesFor walks them at
// the same path, and what that difference costs is which keyword may be read
// below one.
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
// hand, and through allOf and $ref, which name the same one.
//
// It descends through the conditional applicators too -- anyOf, oneOf,
// if/then/else, dependentSchemas, not -- but a rule found only below one carries
// WriteOnly and never ReadOnly, which is the `branched` flag below. That is not a
// softening of 2020-12 §7.7.1: readOnly is still read only where the schema says
// it applies to every valid instance, because a refusal keyed on a branch the
// document never matched refuses a document the schema permits, and a `not` that
// succeeds is a subschema that failed. writeOnly is a policy in the other
// direction -- over-stripping loses a field visibly, under-stripping emits a
// secret silently -- and conditionalReachAt is where that is argued in full.
// TestStrictReadWriteBindsWhereverThePropertyIs' roViaAnyOf is the control on the
// readOnly half and it still holds.
//
// The `Except` lists an AccessOther step carries are computed from the
// unconditional reach only, at both settings of the flag. So a branch's
// `properties` never narrows what an additionalProperties rule covers: narrowing
// it would take a location *out* of the readOnly walker's reach on the strength
// of a branch, which is the direction that under-enforces.
func (g *Generator) accessRulesFor(s *schema.Schema, minDepth int) []AccessRule {
	if s == nil || !g.config.StrictReadWrite {
		return nil
	}
	var out []AccessRule
	// A location the walk reaches twice -- unconditionally and again through a
	// branch -- is one rule, not two: the flags are OR-ed onto the entry already
	// emitted, so a readOnly the unconditional pass found is never overwritten by
	// a branch that says nothing about it.
	at := map[string]int{}
	emit := func(path []AccessStep, ro, wo bool) {
		if !ro && !wo {
			return
		}
		key := accessRuleKey(path)
		if i, seen := at[key]; seen {
			out[i].ReadOnly = out[i].ReadOnly || ro
			out[i].WriteOnly = out[i].WriteOnly || wo
			return
		}
		at[key] = len(out)
		out = append(out, AccessRule{Path: path, ReadOnly: ro, WriteOnly: wo})
	}
	onPath := map[*schema.Schema]bool{}
	var walk func(node *schema.Schema, path []AccessStep, branched bool)
	walk = func(node *schema.Schema, path []AccessStep, branched bool) {
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
					if branched {
						ro = false
					}
					emit(next, ro, wo)
				}
				walk(ps, next, branched)
			}
			for _, pat := range sortedKeys(r.PatternProperties) {
				walk(r.PatternProperties[pat], step(AccessPattern, pat, 0), branched)
			}
			// additionalProperties and unevaluatedProperties reach the same set
			// of members here. They are not the same keyword -- unevaluated also
			// counts what an in-place applicator evaluated -- but this walk does
			// not descend into those applicators at all, so at every location it
			// does reach, the two名 the same leftovers.
			for _, value := range []*schema.Schema{additionalPropertiesSchema(r), r.UnevaluatedProperties} {
				if value != nil {
					walk(value, step(AccessOther, "", 0), branched)
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
				walk(slot, step(AccessTuple, "", i), branched)
			}
			if itemsSchema != nil {
				walk(itemsSchema, step(AccessItems, "", len(tuple)), branched)
			}
			if r.AdditionalItems != nil && len(tuple) > 0 && itemsSchema == nil {
				walk(r.AdditionalItems.AsSchema(), step(AccessItems, "", len(tuple)), branched)
			}
			if r.UnevaluatedItems != nil {
				walk(r.UnevaluatedItems, step(AccessItems, "", len(tuple)), branched)
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
				walk(r.Contains, step(AccessItems, "", 0), branched)
			}
			// The conditional applicators, at the same path: each describes the
			// value in hand rather than a value inside it, exactly as allOf and
			// $ref do, and differs from them only in applying to some documents
			// instead of all. Everything found below here is marked `branched`,
			// which is what holds readOnly to the unconditional reach while
			// letting writeOnly follow the branch. See conditionalReachAt.
			for _, branch := range r.AnyOf {
				walk(branch, path, true)
			}
			for _, branch := range r.OneOf {
				walk(branch, path, true)
			}
			for _, branch := range []*schema.Schema{r.If, r.Then, r.Else, r.Not} {
				walk(branch, path, true)
			}
			for _, key := range sortedKeys(r.DependentSchemas) {
				walk(r.DependentSchemas[key], path, true)
			}
		}
	}
	walk(s, nil, false)
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

// accessRuleKey identifies a location, so that a path the walk reaches twice --
// once outright and once through a branch -- is one entry in the table whose
// flags are the union, rather than two entries the generated walker would apply
// one after the other.
//
// The Except lists are not in the key, and cannot be: they are a property of the
// schema object the step was taken from, so two AccessOther steps at the same
// path have the same ones by construction. Only Kind, Name and Index say where a
// step goes.
func accessRuleKey(path []AccessStep) string {
	var b strings.Builder
	for _, s := range path {
		fmt.Fprintf(&b, "%d\x00%s\x00%d\x00", s.Kind, s.Name, s.Index)
	}
	return b.String()
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
