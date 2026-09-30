package generator

import (
	"fmt"
	"slices"
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

// AccessStep is the step an AccessMove takes: which members or elements of the
// value in hand it reaches.
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

// AccessRules is what --strict-read-write has to say about the locations below
// the value a generated type holds: where, in its file's access machine, the
// walk of that value starts.
//
// The flat ReadOnlyKeys/WriteOnlyKeys lists on StructDef say the same thing for
// a member of the struct itself, which is the case a Go field covers and the
// only case they ever covered. The machine says it for the members below a
// value the generated code keeps as raw JSON -- a prefixItems slot, a contains
// element, a patternProperties value, anything under a type whose whole schema
// is held as data -- where there is no field, no nested type ever decodes, and
// until issue #219 the flag was therefore a silent no-op.
type AccessRules struct {
	// Machine is the file's machine variable (File.AccessMachineVar), and
	// Start the state the walk starts in.
	Machine string
	Start   int
	// root is the access graph node the rules were built from, which is what
	// a parent's rules are compared against to tell whether this type strips
	// what the parent would (see stripRulesFor).
	root  accessKey
	graph *accessGraph
}

// AccessState is one state of the access machine: the moves a walk in it makes
// from the value in hand to the members or elements inside it.
type AccessState struct {
	Moves []AccessMove
}

// AccessMove is one move out of a state. Step says which members or elements
// it reaches. ReadOnly and WriteOnly say the schema marks what it reaches: the
// decoder refuses a document that sets it, the encoder leaves it out. Next is
// the state what it reaches is walked in, -1 for none, and SeekReadOnly and
// SeekWriteOnly say whether that walk can find a readOnly or a writeOnly
// member, so that each half of the walker follows only the moves that can
// matter to it.
type AccessMove struct {
	Step                        AccessStep
	Next                        int
	ReadOnly, WriteOnly         bool
	SeekReadOnly, SeekWriteOnly bool
}

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
func (g *Generator) accessRulesFor(s *schema.Schema, minDepth int) *AccessRules {
	if s == nil || !g.config.StrictReadWrite {
		return nil
	}
	a := g.accessGraphFor(s)
	root := a.build(s, false)
	a.settle()
	if !a.liveRO[root] && !a.liveWO[root] {
		return nil
	}
	var start int
	if minDepth <= 1 {
		start = g.accessState(a, root)
	} else {
		// A struct's own properties are its flat key lists' to refuse and
		// strip: its start is the root's state without their marks. What lies
		// below them is still walked.
		var moves []AccessMove
		for _, m := range g.accessMoves(a, root) {
			if m.Next < 0 && m.Step.Kind == AccessProperty {
				continue
			}
			moves = append(moves, m)
		}
		if len(moves) == 0 {
			return nil
		}
		start = g.addAccessState(moves)
	}
	return &AccessRules{Machine: g.accessMachineVar(), Start: start, root: root, graph: a}
}

// # A machine rather than a table of paths, in time proportionate to the schema
//
// The rules used to be a table of paths, found by walking every path from the
// value down to a depth bound of 24 steps. A schema that refers to itself
// through several value positions -- a metaschema is the sharp case, a dozen
// keywords each leading back to the root -- has a number of such paths
// exponential in the bound: the JSON Schema Test Suite's "validate definition
// against metaschema" and "remote ref, containing refs itself" groups did not
// finish generating in 45 seconds under --strict-read-write, though a
// metaschema marks nothing at all. And where such a schema does mark
// something, the table the paths make is exponential too. The bound also left a
// hole: a writeOnly member deeper than it, inside a recursive value held as raw
// JSON, was written back out, and a readOnly one accepted.
//
// So the rules are a machine, and the paths are its runs. build records, once
// per (schema node, whether the route to it is conditional), what the node says
// -- the member steps it marks, and the steps and in-place moves to the nodes
// below it -- which is a graph the size of the schema, shared by every
// accessRulesFor call of the Generate (one per dynamic scope where a dynamic
// reference is reachable; see accessGraphFor). A schema that refers to itself
// is a cycle in the graph, and so in the machine: nothing bounds the depth a
// rule reaches. settle marks the nodes from which a
// readOnly or a writeOnly mark is reachable. accessState turns each such node
// into a state of the file's machine: its moves are those of the node and of
// every node an in-place applicator reaches from it, and a move leads to a state
// only where something can still be found. The generated walker runs the
// machine over the document, carrying the set of states each value is walked
// in -- never more than the machine has -- so every value is read once and its
// work is the document's size times the states in play. A node from which no
// mark is reachable at all (accessMarksReachable) is not built.

// accessKey is one node of the access graph: a schema node, reached by an exact
// route or a conditional one.
type accessKey struct {
	node     *schema.Schema
	branched bool
}

// accessEdge is one move out of an access graph node: a member step marked by
// the schema at value (mark), or a move to another graph node, taking a step
// or -- for an in-place applicator -- none.
type accessEdge struct {
	step   *AccessStep
	to     accessKey
	mark   bool
	ro, wo bool
}

type accessGraph struct {
	g      *Generator
	edges  map[accessKey][]accessEdge
	liveRO map[accessKey]bool
	liveWO map[accessKey]bool
	reach  map[*schema.Schema][]*schema.Schema
	rw     map[*schema.Schema][2]bool
	order  []accessKey
	stable bool
	// reverse and settled are settle's: the moves into each node, and how much
	// of order it has taken in.
	reverse map[accessKey][]accessKey
	settled int
	// stepKeys caches accessStepKey by step, closures caches closure, and
	// covers holds the pairs of graph nodes memberCovers has decided, for
	// writeOnly ([0]) and readOnly ([1]). See memberCovers.
	stepKeys map[*AccessStep]string
	closures map[accessKey][]accessEdge
	covers   [2]map[accessPair]bool
}

// accessPair is a pair of graph nodes memberCovers relates: a state of the
// parent's walk, and one of the member's.
type accessPair struct{ p, c accessKey }

// stepKey is accessStepKey of a step of the graph, computed once per step.
func (a *accessGraph) stepKey(st *AccessStep) string {
	if k, ok := a.stepKeys[st]; ok {
		return k
	}
	if a.stepKeys == nil {
		a.stepKeys = map[*AccessStep]string{}
	}
	k := accessStepKey(*st)
	a.stepKeys[st] = k
	return k
}

// settledClosure is closure(k) for a node built and settled, which no later
// build changes: a node is built with everything below it, and its liveness is
// that of what lies below it. It is computed once.
func (a *accessGraph) settledClosure(k accessKey) []accessEdge {
	a.settle()
	if c, ok := a.closures[k]; ok {
		return c
	}
	if a.closures == nil {
		a.closures = map[accessKey][]accessEdge{}
	}
	c := a.closure(k)
	a.closures[k] = c
	return c
}

// accessGraphFor is the access graph for the dynamic scope in force, built as
// accessRulesFor asks for more of it.
//
// A $dynamicRef or a $recursiveRef resolves through the dynamic scope the
// generator is walking in, so what a node reaches -- and so the graph built
// from it -- is an answer under one scope. One graph is kept per scope, keyed by
// the resources in it, and within a scope every accessRulesFor call shares it:
// the nodes of a schema are built once for the whole Generate, not once per type
// that reaches them.
//
// A schema that reaches no dynamic reference (accessScopeFree) resolves the same
// way under every scope, and every such schema shares one graph whatever scope
// its type was generated under. That is what lets a struct's rules be compared
// with those of a member's type generated under another scope -- a $defs type
// and the document root are generated under different ones -- which is what
// the cuts in stripRulesFor and pruneDecodeRules ask.
func (g *Generator) accessGraphFor(s *schema.Schema) *accessGraph {
	key := "scope-free"
	if !g.accessScopeFree(s) {
		var scope strings.Builder
		for _, resource := range g.dynamicScope {
			fmt.Fprintf(&scope, "%p;", resource)
		}
		key = "scope:" + scope.String()
	}
	if a, ok := g.accessGraphs[key]; ok {
		return a
	}
	if g.accessGraphs == nil {
		g.accessGraphs = map[string]*accessGraph{}
	}
	a := &accessGraph{
		g:      g,
		edges:  map[accessKey][]accessEdge{},
		liveRO: map[accessKey]bool{},
		liveWO: map[accessKey]bool{},
		reach:  map[*schema.Schema][]*schema.Schema{},
		rw:     map[*schema.Schema][2]bool{},
	}
	g.accessGraphs[key] = a
	return a
}

func (a *accessGraph) unconditionalReach(n *schema.Schema) []*schema.Schema {
	if r, ok := a.reach[n]; ok {
		return r
	}
	r := a.g.unconditionalReachAt(n, true)
	a.reach[n] = r
	return r
}

func (a *accessGraph) readWrite(n *schema.Schema) (ro, wo bool) {
	if v, ok := a.rw[n]; ok {
		return v[0], v[1]
	}
	ro, wo = a.g.readWriteAtLocation(n)
	a.rw[n] = [2]bool{ro, wo}
	return ro, wo
}

// build records the graph node for (node, branched) and everything below it,
// and returns its key. The traversal is the rule accessRulesFor states, keyword
// by keyword. A node is recorded before what lies below it is, so a schema that
// refers back to it closes a cycle rather than recursing.
func (a *accessGraph) build(node *schema.Schema, branched bool) accessKey {
	k := accessKey{node: node, branched: branched}
	if _, done := a.edges[k]; done {
		return k
	}
	a.edges[k] = nil
	a.order = append(a.order, k)
	a.stable = false
	if node == nil || !a.g.accessMarksReachable(node) {
		return k
	}
	var out []accessEdge
	// mark records what the schema at a member step says about the member
	// itself. A struct's own flat key lists answer for its properties and for
	// nothing else, so a member chosen by pattern or as a leftover is a rule
	// even at the depth minDepth leaves to them (see accessRulesFor).
	mark := func(st AccessStep, value *schema.Schema, branched bool) {
		ro, wo := a.readWrite(value)
		if branched {
			ro = false
		}
		if !ro && !wo {
			return
		}
		out = append(out, accessEdge{step: &st, mark: true, ro: ro, wo: wo})
	}
	// walk records a move to the node below: one step down, or in place.
	walk := func(st *AccessStep, child *schema.Schema, branched bool) {
		if child == nil || !a.g.accessMarksReachable(child) {
			return
		}
		out = append(out, accessEdge{step: st, to: a.build(child, branched)})
	}
	stepOf := func(k AccessStepKind, name string, index int) *AccessStep {
		return &AccessStep{Kind: k, Name: name, Index: index}
	}
	other := func(except, exceptPatterns []string) AccessStep {
		return AccessStep{Kind: AccessOther, Except: except, ExceptPatterns: exceptPatterns}
	}

	for _, r := range a.unconditionalReach(node) {
		for _, name := range sortedKeys(r.Properties) {
			ps := r.Properties[name]
			st := AccessStep{Kind: AccessProperty, Name: name}
			mark(st, ps, branched)
			walk(&st, ps, branched)
		}
		// A member a pattern matches is that pattern's on every document that
		// has the member: the key decides it and nothing else does.
		for _, pat := range sortedKeys(r.PatternProperties) {
			value := r.PatternProperties[pat]
			st := AccessStep{Kind: AccessPattern, Name: pat}
			mark(st, value, branched)
			walk(&st, value, branched)
		}
		// additionalProperties is exact in the same way. What it steps past is
		// its own schema object's `properties` and `patternProperties` and
		// nothing else -- not an allOf branch's, which 2020-12 §10.3.2.3 leaves
		// to unevaluatedProperties -- so a member only a sibling allOf branch
		// names is still one of its leftovers.
		if value := additionalPropertiesSchema(r); value != nil {
			st := other(sortedKeys(r.Properties), sortedKeys(r.PatternProperties))
			mark(st, value, branched)
			walk(&st, value, branched)
		}
		// unevaluatedProperties reaches two sets. The members nothing beside it
		// could ever evaluate are its on every valid document, an exact
		// location; the members only a branch might evaluate are its on some
		// documents, a conditional one. They are the same set wherever no
		// branch names a member, and then one rule.
		if value := r.UnevaluatedProperties; value != nil {
			exact, exactPatterns, someExact := a.g.unevaluatedMembers(r, true)
			if someExact {
				st := other(exact, exactPatterns)
				mark(st, value, branched)
				walk(&st, value, branched)
			}
			wide, widePatterns, someWide := a.g.unevaluatedMembers(r, false)
			if someWide && (!someExact || !slices.Equal(wide, exact) || !slices.Equal(widePatterns, exactPatterns)) {
				st := other(wide, widePatterns)
				mark(st, value, true)
				walk(&st, value, true)
			}
		}
		tuple := a.g.accessTupleOf(r)
		for i, slot := range tuple {
			walk(stepOf(AccessTuple, "", i), slot, branched)
		}
		if r.Items != nil && r.Items.Schema != nil {
			walk(stepOf(AccessItems, "", len(tuple)), r.Items.Schema, branched)
		}
		if a.g.additionalItemsApplies(r) {
			walk(stepOf(AccessItems, "", len(tuple)), r.AdditionalItems.AsSchema(), branched)
		}
		// unevaluatedItems likewise: from the index nothing beside it could
		// evaluate it is exact, and from the index the unconditional tuples end
		// at it is conditional.
		if value := r.UnevaluatedItems; value != nil {
			exactFrom, someExact := a.g.unevaluatedItems(r, true)
			if someExact {
				walk(stepOf(AccessItems, "", exactFrom), value, branched)
			}
			if wideFrom, someWide := a.g.unevaluatedItems(r, false); someWide && (!someExact || wideFrom != exactFrom) {
				walk(stepOf(AccessItems, "", wideFrom), value, true)
			}
		}
		// `contains` describes the elements it matches, and which those are is
		// the document's business: {"contains":{"required":["kind"]}} says
		// nothing about an element with no "kind". So it is conditional, and
		// walked over every element because any of them may match.
		if r.Contains != nil {
			walk(stepOf(AccessItems, "", 0), r.Contains, true)
		}
		// The conditional applicators, in place: each describes the value in
		// hand rather than a value inside it, exactly as allOf and $ref do, and
		// differs from them only in applying to some documents instead of all.
		// Everything found below here is marked `branched`, which is what holds
		// readOnly to the unconditional reach while letting writeOnly follow the
		// branch. See conditionalReachAt.
		for _, branch := range r.AnyOf {
			walk(nil, branch, true)
		}
		for _, branch := range r.OneOf {
			walk(nil, branch, true)
		}
		for _, branch := range []*schema.Schema{r.If, r.Then, r.Else, r.Not} {
			walk(nil, branch, true)
		}
		for _, key := range sortedKeys(r.DependentSchemas) {
			walk(nil, r.DependentSchemas[key], true)
		}
	}
	a.edges[k] = out
	return k
}

// settle marks the graph nodes from which a readOnly mark (liveRO) and a
// writeOnly mark (liveWO) is reachable: those with such a marked member step,
// and every node with a move to one.
//
// The graph grows as later types ask for more of it, and settle takes in only
// the nodes built since it last ran: a node's moves never change once built and
// liveness only ever grows, so what was settled stays right. A new node is live
// when it has a mark or a move to a node already live -- the second is what
// ties a new type's root to the part of the graph an earlier type built -- and
// liveness then spreads back over the reverse moves, the old ones included.
// Each node and move is taken in once over the whole Generate: linear in the
// graph, however many types share it.
func (a *accessGraph) settle() {
	if a.stable {
		return
	}
	if a.reverse == nil {
		a.reverse = map[accessKey][]accessKey{}
	}
	var queueRO, queueWO []accessKey
	fresh := a.order[a.settled:]
	for _, k := range fresh {
		for _, e := range a.edges[k] {
			if !e.mark {
				a.reverse[e.to] = append(a.reverse[e.to], k)
			}
		}
	}
	for _, k := range fresh {
		for _, e := range a.edges[k] {
			ro, wo := e.ro, e.wo
			if !e.mark {
				ro, wo = a.liveRO[e.to], a.liveWO[e.to]
			}
			if ro && !a.liveRO[k] {
				a.liveRO[k] = true
				queueRO = append(queueRO, k)
			}
			if wo && !a.liveWO[k] {
				a.liveWO[k] = true
				queueWO = append(queueWO, k)
			}
		}
	}
	spread := func(live map[accessKey]bool, queue []accessKey) {
		for len(queue) > 0 {
			k := queue[0]
			queue = queue[1:]
			for _, p := range a.reverse[k] {
				if !live[p] {
					live[p] = true
					queue = append(queue, p)
				}
			}
		}
	}
	spread(a.liveRO, queueRO)
	spread(a.liveWO, queueWO)
	a.settled = len(a.order)
	a.stable = true
}

// closure is every move a walk in graph node k makes that takes a step: k's
// own, and those of every node an in-place applicator reaches from k, since
// those describe the same value. A move two routes reach alike is one move.
func (a *accessGraph) closure(k accessKey) []accessEdge {
	type edgeID struct {
		step         string
		mark, ro, wo bool
		to           accessKey
	}
	var out []accessEdge
	seenNode := map[accessKey]bool{}
	seenEdge := map[edgeID]bool{}
	var visit func(k accessKey)
	visit = func(k accessKey) {
		if seenNode[k] || (!a.liveRO[k] && !a.liveWO[k]) {
			return
		}
		seenNode[k] = true
		for _, e := range a.edges[k] {
			if e.step == nil {
				visit(e.to)
				continue
			}
			id := edgeID{step: a.stepKey(e.step), mark: e.mark, ro: e.ro, wo: e.wo, to: e.to}
			if seenEdge[id] {
				continue
			}
			seenEdge[id] = true
			out = append(out, e)
		}
	}
	visit(k)
	return out
}

// accessRootOf is the graph node a type built from s starts its rules in.
func (g *Generator) accessRootOf(s *schema.Schema) *accessStateID {
	a := g.accessGraphFor(s)
	k := a.build(s, false)
	a.settle()
	return &accessStateID{graph: a, key: k}
}

// accessStateID names a graph node of one access graph, which is what a state
// of the file's machine stands for.
type accessStateID struct {
	graph *accessGraph
	key   accessKey
}

// accessState is the machine state for graph node k, added to the file's
// machine the first time it is asked for.
func (g *Generator) accessState(a *accessGraph, k accessKey) int {
	id := accessStateID{graph: a, key: k}
	if i, ok := g.accessStates[id]; ok {
		return i
	}
	if g.accessStates == nil {
		g.accessStates = map[accessStateID]int{}
	}
	i := g.addAccessState(nil)
	g.accessStates[id] = i
	g.accessStateKeys[i] = id
	g.output.AccessMachine[i].Moves = g.accessMoves(a, k)
	return i
}

// addAccessState appends a state to the file's machine.
func (g *Generator) addAccessState(moves []AccessMove) int {
	g.output.AccessMachine = append(g.output.AccessMachine, AccessState{Moves: moves})
	if g.accessStateKeys == nil {
		g.accessStateKeys = map[int]accessStateID{}
	}
	return len(g.output.AccessMachine) - 1
}

// accessMoves is the moves of the state for graph node k: its marks, and a move
// to the state below for every step something can still be found through.
func (g *Generator) accessMoves(a *accessGraph, k accessKey) []AccessMove {
	var out []AccessMove
	for _, e := range a.closure(k) {
		if e.mark {
			out = append(out, AccessMove{Step: *e.step, Next: -1, ReadOnly: e.ro, WriteOnly: e.wo})
			continue
		}
		ro, wo := a.liveRO[e.to], a.liveWO[e.to]
		if !ro && !wo {
			continue
		}
		out = append(out, AccessMove{Step: *e.step, Next: g.accessState(a, e.to), SeekReadOnly: ro, SeekWriteOnly: wo})
	}
	return out
}

// accessMachineVar is the package variable the file's machine is declared
// as, claimed the first time a type needs it.
func (g *Generator) accessMachineVar() string {
	if g.output.AccessMachineVar == "" {
		g.output.AccessMachineVar = g.names.claim("_accessStates",
			memberHolder(g.rootTypeName, "access-machine", "the --strict-read-write machine of this file"))
	}
	return g.output.AccessMachineVar
}

// accessStepKey identifies a step: its kind, name and index, and its Except
// lists, which say which members an AccessOther step reaches -- two such steps
// out of one node need not agree: an additionalProperties steps past its own
// object's names only, so one on the node and one on its allOf branch reach
// different leftovers, and an unevaluatedProperties beside them a third set.
func accessStepKey(s AccessStep) string {
	return fmt.Sprintf("%d\x00%q\x00%d\x00%q\x00%q", s.Kind, s.Name, s.Index, s.Except, s.ExceptPatterns)
}

// accessMarksReachable reports whether a "readOnly": true or a "writeOnly":
// true is reachable from s along any edge accessRulesFor's walk or
// readWriteAtLocation's reaches could take: the value-position keywords, the
// in-place applicators, and every reference -- a dynamic one to its static
// target and to every declaration of its anchor, so the answer does not turn on
// the dynamic scope in force when it is asked, and can be kept for the whole
// Generate call.
//
// It lets the access graph skip, whole, a subtree that can contribute no rule
// -- a metaschema, whose every keyword leads back to the root and which marks
// nothing, builds no graph at all. The question is answered for every node at
// once, in time linear in the nodes and edges reachable from where it was first
// asked.
//
// Over-approximating is the safe direction: a subtree answered "reachable" is
// walked as before, and one answered "not reachable" has no node that could
// mark anything, whichever branch or scope a document takes.
func (g *Generator) accessMarksReachable(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	if known, ok := g.accessMarks[s]; ok {
		return known
	}
	if g.accessMarks == nil {
		g.accessMarks = map[*schema.Schema]bool{}
	}
	// Collect every node reachable from s that is not already answered, with
	// its edges, then propagate marks backwards along the edges.
	var order []*schema.Schema
	edges := map[*schema.Schema][]*schema.Schema{}
	marked := map[*schema.Schema]bool{}
	dynamic := map[*schema.Schema]bool{}
	var stack []*schema.Schema
	push := func(n *schema.Schema) {
		if n == nil {
			return
		}
		if _, answered := g.accessMarks[n]; answered {
			return
		}
		if _, seen := edges[n]; seen {
			return
		}
		edges[n] = nil
		order = append(order, n)
		stack = append(stack, n)
	}
	push(s)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		succ := g.accessSuccessors(n)
		edges[n] = succ
		if n.IsReadOnly() || n.IsWriteOnly() {
			marked[n] = true
		}
		if n.DynamicRef != "" || n.RecursiveRef != "" {
			dynamic[n] = true
		}
		for _, m := range succ {
			if g.accessMarks[m] {
				marked[n] = true
			}
			if g.accessDynamic[m] {
				dynamic[n] = true
			}
			push(m)
		}
	}
	// Reverse edges among the new nodes, and a breadth-first spread of "a mark
	// is reachable" from every node known to have one.
	reverse := map[*schema.Schema][]*schema.Schema{}
	for _, n := range order {
		for _, m := range edges[n] {
			if _, isNew := edges[m]; isNew {
				reverse[m] = append(reverse[m], n)
			}
		}
	}
	spread := func(set map[*schema.Schema]bool) {
		var queue []*schema.Schema
		for _, n := range order {
			if set[n] {
				queue = append(queue, n)
			}
		}
		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]
			for _, p := range reverse[n] {
				if !set[p] {
					set[p] = true
					queue = append(queue, p)
				}
			}
		}
	}
	spread(marked)
	spread(dynamic)
	if g.accessDynamic == nil {
		g.accessDynamic = map[*schema.Schema]bool{}
	}
	for _, n := range order {
		g.accessMarks[n] = marked[n]
		g.accessDynamic[n] = dynamic[n]
	}
	return g.accessMarks[s]
}

// accessScopeFree reports whether nothing reachable from s resolves through
// the dynamic scope -- no $dynamicRef and no $recursiveRef -- so that what s
// reaches is the same under every scope. Computed by accessMarksReachable's
// pass.
func (g *Generator) accessScopeFree(s *schema.Schema) bool {
	g.accessMarksReachable(s)
	return !g.accessDynamic[s]
}

// accessSuccessors is every schema accessMarksReachable steps to from n.
func (g *Generator) accessSuccessors(n *schema.Schema) []*schema.Schema {
	out := g.inPlaceSuccessors(n)
	if _, target := g.referenceTargetUncounted(n); target != nil {
		out = append(out, target)
	}
	for _, key := range sortedKeys(n.Properties) {
		out = append(out, n.Properties[key])
	}
	for _, key := range sortedKeys(n.PatternProperties) {
		out = append(out, n.PatternProperties[key])
	}
	out = append(out, additionalPropertiesSchema(n), n.UnevaluatedProperties, n.Contains, n.UnevaluatedItems)
	out = append(out, n.PrefixItems...)
	if n.Items != nil {
		out = append(out, n.Items.Schema)
		out = append(out, n.Items.Schemas...)
	}
	if n.AdditionalItems != nil {
		out = append(out, n.AdditionalItems.Schema)
	}
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
