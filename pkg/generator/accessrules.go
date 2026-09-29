package generator

import (
	"fmt"
	"slices"
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
	// AccessProperty is one named member of an object.
	//
	// Every rule ends in a member step -- this one, AccessPattern or
	// AccessOther: readOnly means "do not accept this member" and writeOnly
	// means "do not write this member", and an object member has a key to
	// refuse or leave out whichever keyword chose it. Neither has an action at
	// an array element, which cannot be left out without changing the array's
	// length, which minItems can see.
	AccessProperty AccessStepKind = iota
	// AccessPattern is every member whose key matches an ECMA-262 pattern.
	AccessPattern
	// AccessOther is every member the step's Except names and ExceptPatterns do
	// not match: the value side of additionalProperties, whose lists are the
	// `properties` and `patternProperties` of the same schema object, and of
	// unevaluatedProperties, whose lists are what that object's in-place
	// applicators evaluate. See accessRulesFor and unevaluatedMembers.
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
	// Except and ExceptPatterns are what AccessOther steps past: for
	// additionalProperties the names its own schema object declares and the
	// patterns that object matches, and for unevaluatedProperties the ones that
	// object's in-place applicators evaluate (see unevaluatedMembers).
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
// minDepth is 2 for a struct, whose own named members are already covered by
// the flat key lists the decoder and encoder carry, and 1 for a type that holds
// raw JSON and has no fields for a list to name. The flat lists name properties
// and nothing else, so a struct's own members chosen by pattern or as leftovers
// -- a patternProperties or additionalProperties value marked readOnly or
// writeOnly -- are rules here at depth 1 whatever minDepth says.
//
// # Where a rule binds
//
// One rule decides it for every position, and it is 2020-12 §7.7.1's: a
// subschema contributes its annotations to exactly the instance locations it
// successfully evaluates. So a keyword binds a location *exactly* when the
// route to it names the location by something the document cannot change once
// the location exists:
//
//   - a member by its key: properties, patternProperties, and
//     additionalProperties -- whose leftovers are what the same schema
//     object's own `properties` and `patternProperties` leave, a fact about the
//     key alone;
//   - an element by its index: prefixItems (or draft 4-2019's items array),
//     items after them, additionalItems after an items array;
//   - the same location again: allOf and $ref.
//
// Every other route is *conditional*: whether it reaches a location depends on
// what the document holds there or elsewhere. anyOf, oneOf, if/then/else,
// dependentSchemas and not describe the value only when a branch is selected (a
// `not` that succeeds is a subschema that failed); contains describes only the
// elements that match it. unevaluatedProperties and unevaluatedItems are both:
// a member or an element that nothing beside them could ever evaluate is theirs
// on every valid document, and exact; one that only a branch or a contains
// might evaluate is theirs on some documents, and conditional.
//
// readOnly binds at exact locations and nowhere else, because a refusal is the
// program declining input, and one keyed on a branch the document did not take
// -- an element contains did not match, a member some anyOf branch evaluated --
// refuses a document the schema permits. writeOnly binds at both, over the
// widest set a conditional route could reach: over-stripping loses a field
// visibly, under-stripping emits a secret silently, and conditionalReachAt is
// where that is argued in full. That is the `branched` flag below. A route that
// crosses a conditional one anywhere is conditional from there down.
//
// For the two unevaluated keywords "the widest set" is what the schema object's
// *unconditional* in-place applicators do not evaluate: those evaluate on every
// valid document, so whatever they name is never unevaluated, while a branch's
// names may or may not be. An object whose unconditional reach already has an
// additionalProperties (or an items that takes every element) leaves nothing
// for its unevaluated keyword to reach, and gets no rule for it. See
// unevaluatedMembers and unevaluatedItems.
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

		step := func(k AccessStepKind, name string, index int) []AccessStep {
			next := make([]AccessStep, len(path), len(path)+1)
			copy(next, path)
			return append(next, AccessStep{Kind: k, Name: name, Index: index})
		}
		other := func(except, exceptPatterns []string) []AccessStep {
			next := step(AccessOther, "", 0)
			next[len(next)-1].Except = except
			next[len(next)-1].ExceptPatterns = exceptPatterns
			return next
		}
		// mark emits what the schema at a member step says about the member
		// itself. A struct's own flat key lists answer for its properties and
		// for nothing else, so a member chosen by pattern or as a leftover is a
		// rule even at the depth minDepth leaves to them.
		mark := func(next []AccessStep, value *schema.Schema, branched bool) {
			if len(next) < minDepth && next[len(next)-1].Kind == AccessProperty {
				return
			}
			ro, wo := g.readWriteAtLocation(value)
			if branched {
				ro = false
			}
			emit(next, ro, wo)
		}

		for _, r := range g.unconditionalReachAt(node, true) {
			for _, name := range sortedKeys(r.Properties) {
				ps := r.Properties[name]
				next := step(AccessProperty, name, 0)
				mark(next, ps, branched)
				walk(ps, next, branched)
			}
			// A member a pattern matches is that pattern's on every document
			// that has the member: the key decides it and nothing else does.
			for _, pat := range sortedKeys(r.PatternProperties) {
				value := r.PatternProperties[pat]
				next := step(AccessPattern, pat, 0)
				mark(next, value, branched)
				walk(value, next, branched)
			}
			// additionalProperties is exact in the same way. What it steps past
			// is its own schema object's `properties` and `patternProperties`
			// and nothing else -- not an allOf branch's, which 2020-12 §10.3.2.3
			// leaves to unevaluatedProperties -- so a member only a sibling
			// allOf branch names is still one of its leftovers.
			if value := additionalPropertiesSchema(r); value != nil {
				next := other(sortedKeys(r.Properties), sortedKeys(r.PatternProperties))
				mark(next, value, branched)
				walk(value, next, branched)
			}
			// unevaluatedProperties reaches two sets. The members nothing
			// beside it could ever evaluate are its on every valid document, an
			// exact location; the members only a branch might evaluate are its
			// on some documents, a conditional one. They are the same set
			// wherever no branch names a member, and then one rule.
			if value := r.UnevaluatedProperties; value != nil {
				exact, exactPatterns, someExact := g.unevaluatedMembers(r, true)
				if someExact {
					next := other(exact, exactPatterns)
					mark(next, value, branched)
					walk(value, next, branched)
				}
				wide, widePatterns, someWide := g.unevaluatedMembers(r, false)
				if someWide && (!someExact || !slices.Equal(wide, exact) || !slices.Equal(widePatterns, exactPatterns)) {
					next := other(wide, widePatterns)
					mark(next, value, true)
					walk(value, next, true)
				}
			}
			tuple := g.accessTupleOf(r)
			var itemsSchema *schema.Schema
			if r.Items != nil && r.Items.Schema != nil {
				itemsSchema = r.Items.Schema
			}
			for i, slot := range tuple {
				walk(slot, step(AccessTuple, "", i), branched)
			}
			if itemsSchema != nil {
				walk(itemsSchema, step(AccessItems, "", len(tuple)), branched)
			}
			if g.additionalItemsApplies(r) {
				walk(r.AdditionalItems.AsSchema(), step(AccessItems, "", len(tuple)), branched)
			}
			// unevaluatedItems likewise: from the index nothing beside it could
			// evaluate it is exact, and from the index the unconditional tuples
			// end at it is conditional.
			if value := r.UnevaluatedItems; value != nil {
				exactFrom, someExact := g.unevaluatedItems(r, true)
				if someExact {
					walk(value, step(AccessItems, "", exactFrom), branched)
				}
				if wideFrom, someWide := g.unevaluatedItems(r, false); someWide && (!someExact || wideFrom != exactFrom) {
					walk(value, step(AccessItems, "", wideFrom), true)
				}
			}
			// `contains` describes the elements it matches, and which those are
			// is the document's business: {"contains":{"required":["kind"]}}
			// says nothing about an element with no "kind". So it is conditional,
			// and walked over every element because any of them may match.
			if r.Contains != nil {
				walk(r.Contains, step(AccessItems, "", 0), true)
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

// accessTupleOf is the positional half of an array schema: prefixItems where
// the dialect has it, and draft 4 to 2019-09's array form of items where it
// does not.
func (g *Generator) accessTupleOf(r *schema.Schema) []*schema.Schema {
	var tuple []*schema.Schema
	if g.supportsPrefixItems(r) {
		tuple = r.PrefixItems
	}
	if len(tuple) == 0 && r.Items != nil && len(r.Items.Schemas) > 0 {
		tuple = r.Items.Schemas
	}
	return tuple
}

// additionalItemsApplies reports whether r's additionalItems describes any
// element: it does after a tuple -- an items array, an empty one included,
// which leaves every element to it -- and not beside an items sub-schema,
// which takes every element itself.
func (g *Generator) additionalItemsApplies(r *schema.Schema) bool {
	if r.AdditionalItems == nil || (r.Items != nil && r.Items.Schema != nil) {
		return false
	}
	return len(g.accessTupleOf(r)) > 0 || (r.Items != nil && r.Items.Schemas != nil)
}

// evaluatingReach is every schema whose evaluation of r's instance can count
// toward r's unevaluatedProperties or unevaluatedItems: the in-place
// applicators, transitively.
//
// With every set it is the ones that evaluate on every valid document -- r, its
// $ref chain and its allOf branches, unconditionalReachAt's reach. Without it,
// it is every one that evaluates on *some* document: the branches too -- anyOf,
// oneOf, if (whose annotations count when it passes), then, else,
// dependentSchemas. `not` is in neither: a `not` that succeeds is a subschema
// that failed, and it contributes nothing.
//
// opaque is set where the answer cannot be read off the schema: a dynamic
// reference evaluates whatever the document's path to it selects, and a
// reference that does not resolve here evaluates nothing anyone can name.
func (g *Generator) evaluatingReach(r *schema.Schema, every bool) (reach []*schema.Schema, opaque bool) {
	if every {
		return g.unconditionalReachAt(r, true), false
	}
	visited := map[*schema.Schema]bool{}
	var walk func(*schema.Schema)
	walk = func(n *schema.Schema) {
		if n == nil || visited[n] {
			return
		}
		visited[n] = true
		reach = append(reach, n)
		if n.DynamicRef != "" || n.RecursiveRef != "" {
			opaque = true
		}
		if referenceOn(n) != "" {
			_, target := g.referenceTargetUncounted(n)
			if target == nil {
				opaque = true
			}
			walk(target)
		}
		for _, branch := range n.AllOf {
			walk(branch)
		}
		for _, branch := range n.AnyOf {
			walk(branch)
		}
		for _, branch := range n.OneOf {
			walk(branch)
		}
		walk(n.If)
		walk(n.Then)
		walk(n.Else)
		for _, key := range sortedKeys(n.DependentSchemas) {
			walk(n.DependentSchemas[key])
		}
	}
	walk(r)
	return reach, opaque
}

// unevaluatedMembers is the set of members r's unevaluatedProperties reaches,
// as the lists an AccessOther step walks past: everything a schema in
// evaluatingReach names by `properties` or matches by `patternProperties`.
//
// every chooses which set. With it, the members it reaches on *every* valid
// document, which is the ones nothing in the whole evaluating reach -- branches
// included -- could ever evaluate: that is the exact location readOnly binds
// at. Without it, the members it reaches on *some* document, which is the ones
// the unconditional reach does not evaluate -- a branch's names may or may not
// be evaluated, so they stay in: that is the widest set writeOnly strips.
//
// some is false where there is no such member at all: an additionalProperties
// in the reach evaluates every member its own object does not name, a nested
// unevaluatedProperties every member its own reach does not, and an opaque
// reach any member.
func (g *Generator) unevaluatedMembers(r *schema.Schema, every bool) (except, exceptPatterns []string, some bool) {
	reach, opaque := g.evaluatingReach(r, !every)
	if opaque {
		return nil, nil, false
	}
	names := map[string]bool{}
	patterns := map[string]bool{}
	for _, n := range reach {
		if n.AdditionalProperties != nil || (n != r && n.UnevaluatedProperties != nil) {
			return nil, nil, false
		}
		// maporder: fills a set; the same members end up in it in any order.
		for name := range n.Properties {
			names[name] = true
		}
		// maporder: fills a set; the same members end up in it in any order.
		for pat := range n.PatternProperties {
			patterns[pat] = true
		}
	}
	return sortedKeys(names), sortedKeys(patterns), true
}

// unevaluatedItems is unevaluatedMembers for unevaluatedItems: the first index
// r's unevaluatedItems reaches, on every valid document or on some. It is the
// longest tuple in the evaluating reach, since each evaluates its own
// positions.
//
// some is false where no element is left: an items sub-schema, an
// additionalItems after an items array, or a nested unevaluatedItems in the
// reach takes every element past its own tuple. A contains takes whichever
// elements match it, so it leaves nothing that is unevaluated on every
// document, and is not counted at all for the widest set, where it may leave
// any of them.
func (g *Generator) unevaluatedItems(r *schema.Schema, every bool) (from int, some bool) {
	reach, opaque := g.evaluatingReach(r, !every)
	if opaque {
		return 0, false
	}
	for _, n := range reach {
		tuple := g.accessTupleOf(n)
		if (n.Items != nil && n.Items.Schema != nil) ||
			g.additionalItemsApplies(n) ||
			(n != r && n.UnevaluatedItems != nil) ||
			(every && n.Contains != nil) {
			return 0, false
		}
		from = max(from, len(tuple))
	}
	return from, true
}

// accessStepReachesKey reports whether a member step reaches the member named
// key, as the generated _accessKeyMatches decides it -- and false where a
// pattern has no answer, which the generated walker reports as an error rather
// than as a match.
func accessStepReachesKey(step AccessStep, key string) bool {
	switch step.Kind {
	case AccessProperty:
		return step.Name == key
	case AccessPattern:
		matched, err := PatternMatches(step.Name, key)
		return err == nil && matched
	case AccessOther:
		if slices.Contains(step.Except, key) {
			return false
		}
		for _, pat := range step.ExceptPatterns {
			if matched, err := PatternMatches(pat, key); err != nil || matched {
				return false
			}
		}
		return true
	}
	return false
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
// The Except lists are in the key. They say which members an AccessOther step
// reaches, and two such steps at one path need not agree: an additionalProperties
// steps past its own object's names only, so one on the node and one on its
// allOf branch reach different leftovers, and an unevaluatedProperties beside
// them reaches a third set. Folding them into one entry would apply one step's
// lists to the other's members.
func accessRuleKey(path []AccessStep) string {
	var b strings.Builder
	for _, s := range path {
		fmt.Fprintf(&b, "%d\x00%q\x00%d\x00%q\x00%q\x00", s.Kind, s.Name, s.Index, s.Except, s.ExceptPatterns)
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
		// Two AccessOther steps at one path can differ by their lists alone
		// (see accessRuleKey), and the table has to have one order for them.
		if c := slices.Compare(x.Except, y.Except); c != 0 {
			return c < 0
		}
		if c := slices.Compare(x.ExceptPatterns, y.ExceptPatterns); c != 0 {
			return c < 0
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
