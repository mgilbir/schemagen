package generator

import (
	"bytes"
	"encoding/json"
	"math/big"
	"slices"
	"sort"
	"strconv"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// This file runs the keyword ledger over one Generate call. ledger.go says what
// the ledger is for and how its result is reported; this is how the two
// invariants are checked.
//
// I1, per generated type: every assertion a node in the type's scope states is
// claimed -- carried by an element of the type's IR, vacuous for every instance
// the type can hold, or handed to another generated type the IR dispatches to.
// I2, per document: every node an evaluation of the document reaches lies in
// the scope of some generated type.
//
// A type's scope is the node it was generated for, the nodes that node reaches
// in place (allOf branches and reference targets, which bind on the same
// instance; the anyOf, oneOf, not, if/then/else and dependentSchemas branches,
// which bind on it selectively), and every subschema below them that the type
// does not hand to another type. What it hands off is decided by the IR alone:
// a child is handed off where the child is the node another type was generated
// for and the type's IR validates a value through that type at that position.
// Anything else stays in scope and has to be claimed by the type's own IR.
//
// Claims are read off the IR (see Provenance). An element built from a node
// the generator synthesized is credited to the nodes the document wrote only
// where its value implies theirs -- impliesKeyword -- which is what makes a
// merge that kept one of two `pattern`s visible: the element carries the first,
// implies the first, and does not imply the second.

// claimHow says how a keyword is claimed. The ledger reports only what is
// unclaimed, but tests and diagnostics read the distinction.
type claimHow uint8

const (
	claimStatic claimHow = iota + 1
	claimEvaluator
	claimVacuous
	claimDelegated
)

// ledgerRun is one run of the ledger.
type ledgerRun struct {
	g       *Generator
	locator docLocator

	defs   []*ledgerDefInfo
	byName map[string]*ledgerDefInfo
	byRoot map[*schema.Schema][]*ledgerDefInfo

	locIndex    map[ledgerLoc]*schema.Schema
	indexedDocs map[*schema.Schema]bool

	// nodes holds what the run has worked out about each node it has looked
	// at; see ledgerNode.
	nodes map[*schema.Schema]*ledgerNode
	slab  []ledgerNode

	reachBuf   []*schema.Schema
	reachArena []*schema.Schema

	// walks counts the scope walks begun, to give each an id.
	walks int32

	findings []LedgerEntry
	// reported maps each finding made to its index in findings.
	reported map[ledgerFindingKey]int
	// folded counts, per finding, the findings below it folded into it; see
	// scopeWalk.report. foldedKeys keeps a finding from being counted twice.
	folded     map[int]int
	foldedKeys map[ledgerFindingKey]bool

	// declinedAbove memoises inheritedDecline.
	declinedAbove map[*schema.Schema]ledgerDecline
}

// ledgerDecline is a decline of the runtime evaluator that covers a node from
// above: the keyword of an ancestor the node is written inside, and the reason
// the evaluator gave. The zero value is no decline.
type ledgerDecline struct {
	at     ledgerKey
	reason string
}

// ledgerNode is what one run of the ledger has worked out about one node. It
// is one record rather than a map per question because the run asks most of
// the questions of most of the nodes, and one lookup that answers them all
// costs less than a lookup, and a map that grows, for each.
type ledgerNode struct {
	has ledgerNodeFacts

	canon   *schema.Schema // canonical
	ref     *schema.Schema // refTarget
	dynamic *schema.Schema // dynamicBesideRef
	authors []*schema.Schema
	reach   []*schema.Schema // inPlaceReach
	kids    ledgerChildList  // children

	// parent is the node whose keyword holds this one in the documents, and
	// that keyword.
	parent ledgerParent

	assertions []string // assertions
	form       string   // contentHash

	// reached is set once some scope walk visited the node, for I2.
	reached bool
	// visitedBy is the id of the last scope walk that visited the node.
	visitedBy int32
}

// ledgerNodeFacts says which of a ledgerNode's answers have been worked out.
type ledgerNodeFacts uint16

const (
	hasCanon ledgerNodeFacts = 1 << iota
	hasRef
	hasAuthors
	hasReach
	hasKids
	hasParent
	hasAssertions
	hasForm
	hasIndexed
	hasDynamic
)

// node is the run's record of n, made on first use. Records are carved out of
// a block of them, which is replaced rather than grown when it fills, so a
// record never moves. The blocks start small, for the many documents of a
// handful of nodes, and double up to a cap.
func (l *ledgerRun) node(n *schema.Schema) *ledgerNode {
	if r := l.nodes[n]; r != nil {
		return r
	}
	if len(l.slab) == cap(l.slab) {
		l.slab = make([]ledgerNode, 0, min(max(2*cap(l.slab), 16), 512))
	}
	l.slab = append(l.slab, ledgerNode{})
	r := &l.slab[len(l.slab)-1]
	l.nodes[n] = r
	return r
}

// reachedNode reports whether some scope walk visited n.
func (l *ledgerRun) reachedNode(n *schema.Schema) bool {
	r := l.nodes[n]
	return r != nil && r.reached
}

// parentOf is the node whose keyword holds n in the documents, and that
// keyword; false for a document's root and a node no document holds.
//
// The node the document wrote n inside is asked first: n is among its
// subschemas unless a rewrite moved it. Only then is the whole document
// indexed, once.
func (l *ledgerRun) parentOf(n *schema.Schema) (ledgerParent, bool) {
	if r := l.nodes[n]; r != nil && r.has&hasParent != 0 {
		return r.parent, true
	}
	holder, rel, ok := n.SourceLink()
	if !ok || len(rel) == 0 {
		// A document's root, or a node no document wrote: nothing holds it.
		return ledgerParent{}, false
	}
	for _, c := range l.children(holder, true) {
		if c.node == n {
			r := l.node(n)
			r.has |= hasParent
			r.parent = ledgerParent{node: holder, keyword: c.keyword}
			return r.parent, true
		}
	}
	if orig, found := n.Written(); found && orig != n {
		// A copy the generator made: the document holds its original.
		return ledgerParent{}, false
	}
	l.indexDocument(n)
	if r := l.nodes[n]; r != nil && r.has&hasParent != 0 {
		return r.parent, true
	}
	return ledgerParent{}, false
}

// ledgerLoc is where a document wrote a node, as the last link of its location
// (schema.SourceLink): the node holding it and the reference tokens from there.
// A value copy keeps its original's link -- the same holder, and the very same
// token slice, which the copy shares rather than repeats -- so the two share a
// ledgerLoc, and the key is two pointers and a length rather than the tokens
// spelled out, which would cost every node of every document a string to
// build and to hash.
type ledgerLoc struct {
	holder *schema.Schema
	rel    *string
	n      int
}

// ledgerLocOf is n's ledgerLoc, or false for a node no document wrote.
func ledgerLocOf(n *schema.Schema) (ledgerLoc, bool) {
	holder, rel, ok := n.SourceLink()
	if !ok {
		return ledgerLoc{}, false
	}
	k := ledgerLoc{holder: holder, n: len(rel)}
	if len(rel) > 0 {
		k.rel = &rel[0]
	}
	return k, true
}

// writtenIn follows n's location links to the top: the root of the document
// that wrote n, with located true, or, for a node SourceLocation does not
// locate, the top the links reach (n itself for a node never located) with
// located false. It is SourceLocation without spelling out the path.
func writtenIn(n *schema.Schema) (top *schema.Schema, located bool) {
	top = n
	for {
		holder, rel, ok := top.SourceLink()
		if !ok {
			return top, false
		}
		if len(rel) == 0 {
			return holder, true
		}
		top = holder
	}
}

// located reports whether a document wrote n: SourceLocation's ok.
func located(n *schema.Schema) bool {
	_, ok := writtenIn(n)
	return ok
}

// ledgerParent is the node whose keyword holds a node, and that keyword.
type ledgerParent struct {
	node    *schema.Schema
	keyword string
}

type ledgerFindingKey struct {
	node    *schema.Schema
	keyword string
	def     string
}

// ledgerDefInfo is one generated declaration as the ledger sees it.
type ledgerDefInfo struct {
	name string
	td   TypeDef
	// root is the node the declaration was generated for, as the generator
	// held it; canonRoot is that node as the document wrote it.
	root      *schema.Schema
	canonRoot *schema.Schema

	// The maps below are made when the first entry is written: most
	// declarations write to only some of them, and a run has thousands.
	pool  map[ledgerKey]claimHow
	whole map[*schema.Schema]claimHow

	// dispatchAll names every type this declaration's Validate reaches a value
	// through; dispatchProp narrows that to the types reached through one
	// property, for a struct, where the IR says which property it is.
	dispatchAll  map[string]bool
	dispatchProp map[string][]string
	// dispatchSkips records, for a type the declaration hands only some kinds
	// of value to, the kinds it never hands it.
	dispatchSkips map[string]jsonKinds

	// typeRefs names every generated type the declaration holds a value as,
	// whether or not it validates through it: the decoder enforces what such
	// a type's Go shape says either way.
	typeRefs map[string]bool

	// forbidsAll is set for a declaration that accepts no instance at all.
	forbidsAll bool
	// dependentRequired collects the declaration's dependentRequired checks,
	// which are judged together against each node stating the keyword.
	dependentRequired []DependentRequiredDef
}

// runLedger checks the two invariants over the declarations this Generate call
// produced, and records what it found on g.ledger.
func (g *Generator) runLedger(root *schema.Schema) {
	l := &ledgerRun{
		g:           g,
		locator:     docLocator{home: g.homeDoc},
		byName:      make(map[string]*ledgerDefInfo),
		byRoot:      make(map[*schema.Schema][]*ledgerDefInfo),
		locIndex:    make(map[ledgerLoc]*schema.Schema),
		indexedDocs: make(map[*schema.Schema]bool),
		nodes:       make(map[*schema.Schema]*ledgerNode),
		reported:    make(map[ledgerFindingKey]int),
	}
	l.collectDefs()
	l.inferRoots()
	for _, d := range l.defs {
		l.claimsOf(d)
	}
	for _, d := range l.defs {
		l.checkScope(d)
	}
	l.checkReach(root)
	l.noteFolded()
	sort.SliceStable(l.findings, func(i, j int) bool {
		a, b := l.findings[i], l.findings[j]
		if a.Location != b.Location {
			return a.Location < b.Location
		}
		if a.Keyword != b.Keyword {
			return a.Keyword < b.Keyword
		}
		return a.Def < b.Def
	})
	g.ledger.unclaimed = l.findings
}

// ---------------------------------------------------------------------------
// The documents: where each node was written, and what holds it.
// ---------------------------------------------------------------------------

// indexDocument records every node of the document holding s by where the
// document wrote it, and the parent of each.
func (l *ledgerRun) indexDocument(s *schema.Schema) {
	if s == nil {
		return
	}
	doc, _ := writtenIn(s)
	if l.indexedDocs[doc] {
		return
	}
	l.indexedDocs[doc] = true
	l.indexNode(doc)
}

// indexNode records n and the nodes below it that no earlier walk recorded,
// and gives each the parent the walk first meets it under where parentOf has
// not already given it one.
func (l *ledgerRun) indexNode(n *schema.Schema) {
	rn := l.node(n)
	if rn.has&hasIndexed != 0 {
		return
	}
	rn.has |= hasIndexed
	if key, ok := ledgerLocOf(n); ok {
		if _, taken := l.locIndex[key]; !taken {
			l.locIndex[key] = n
		}
	}
	for _, c := range l.children(n, true) {
		if c.node == nil {
			continue
		}
		if r := l.node(c.node); r.has&hasParent == 0 {
			r.has |= hasParent
			r.parent = ledgerParent{node: n, keyword: c.keyword}
		}
		l.indexNode(c.node)
	}
}

// canonical is the node the document wrote at the place n says it was
// written: n itself for a node of the document, and the original for a value
// copy the generator made of one (a copy keeps its original's location).
func (l *ledgerRun) canonical(n *schema.Schema) *schema.Schema {
	if n == nil {
		return nil
	}
	if r := l.nodes[n]; r != nil && r.has&hasCanon != 0 {
		return r.canon
	}
	// The holder of the location says which node it read there, which answers
	// almost every node without a walk. A node a rewrite placed where its
	// holder read nothing is looked up in the index of the whole document,
	// built the first time one is met.
	c, found := n.Written()
	if !found {
		c = n
		if key, ok := ledgerLocOf(n); ok {
			l.indexDocument(n)
			if orig, ok := l.locIndex[key]; ok {
				c = orig
			}
		}
	}
	r := l.node(n)
	r.has |= hasCanon
	r.canon = c
	return c
}

// authors lists the nodes the document wrote that an element built from src
// may be speaking for: src's original when src is a copy, and every node the
// merge src is reached in place from when src is a merge target -- or the
// allOf node conjoinAllOfProperty makes of two contributions. src itself is
// not listed.
func (l *ledgerRun) authors(src *schema.Schema) []*schema.Schema {
	if src == nil {
		return nil
	}
	rec := l.node(src)
	if rec.has&hasAuthors != 0 {
		return rec.authors
	}
	rec.has |= hasAuthors // a cycle answers "none" rather than recursing
	var out []*schema.Schema
	add := func(n *schema.Schema) {
		if n == nil || n == src {
			return
		}
		for _, have := range out {
			if have == n {
				return
			}
		}
		out = append(out, n)
	}
	from, merged := l.g.mergeDocSources[src]
	if !merged {
		from, merged = l.g.ledger.synthesizedFrom[src]
	}
	switch {
	case merged:
		for _, n := range l.inPlaceReach(l.canonical(from)) {
			add(n)
		}
	default:
		if c := l.canonical(src); c != src {
			add(c)
		} else if len(src.AllOf) > 0 && !located(src) {
			// conjoinAllOfProperty's node: the conjunction of the two.
			for _, branch := range src.AllOf {
				add(l.canonical(branch))
				for _, n := range l.authors(branch) {
					add(n)
				}
				for _, n := range l.inPlaceReach(l.canonical(branch)) {
					add(n)
				}
			}
		}
	}
	rec.authors = out
	return out
}

// inPlaceReach is n and every node n reaches through allOf and through a
// reference that applies in place, recursively: the nodes whose assertions
// all bind on the instance n binds on, unconditionally.
//
// The answer is remembered, and shared: its capacity is its length, so a
// caller that appends to it gets a copy rather than writing into the memo.
func (l *ledgerRun) inPlaceReach(n *schema.Schema) []*schema.Schema {
	if n == nil {
		return nil
	}
	if r := l.nodes[n]; r != nil && r.has&hasReach != 0 {
		return r.reach
	}
	st := reachWalk{out: l.reachBuf[:0]}
	l.walkReach(n, &st)
	l.reachBuf = st.out[:0]
	// The answers are carved out of one shared block rather than allocated
	// one by one: there is one per node the ledger looks at, and most are a
	// single node.
	if cap(l.reachArena)-len(l.reachArena) < len(st.out) {
		l.reachArena = make([]*schema.Schema, 0, max(256, len(st.out)))
	}
	start := len(l.reachArena)
	l.reachArena = append(l.reachArena, st.out...)
	out := l.reachArena[start:len(l.reachArena):len(l.reachArena)]
	r := l.node(n)
	r.has |= hasReach
	r.reach = out
	return out
}

// reachWalk is the state of one inPlaceReach walk. Almost every reach is a
// node or two, which a scan of out answers without a set; seen takes over once
// out is long enough for the scan to cost more than a set does.
type reachWalk struct {
	out  []*schema.Schema
	seen map[*schema.Schema]bool
}

func (l *ledgerRun) walkReach(m *schema.Schema, st *reachWalk) {
	if m == nil {
		return
	}
	if st.seen != nil {
		if st.seen[m] {
			return
		}
		st.seen[m] = true
	} else if slices.Contains(st.out, m) {
		return
	} else if len(st.out) == 16 {
		st.seen = make(map[*schema.Schema]bool, 32)
		for _, o := range st.out {
			st.seen[o] = true
		}
		st.seen[m] = true
	}
	st.out = append(st.out, m)
	for _, b := range m.AllOf {
		l.walkReach(b, st)
	}
	if t := l.refTarget(m); t != nil {
		l.walkReach(t, st)
	}
	if t := l.dynamicBesideRef(m); t != nil {
		l.walkReach(t, st)
	}
}

// dynamicBesideRef is where n's $dynamicRef leads when n also carries a $ref
// or $recursiveRef, or nil. Both apply to the instance, and refTarget, which
// follows the generator's own precedence, answers with the other one: the
// $dynamicRef's target is a node the document evaluates all the same, which
// the ledger has to walk to find what nothing carries of it.
func (l *ledgerRun) dynamicBesideRef(n *schema.Schema) *schema.Schema {
	if n == nil || n.IsBooleanSchema() || n.DynamicRef == "" || n.EffectiveRef() == "" {
		return nil
	}
	if r := l.nodes[n]; r != nil && r.has&hasDynamic != 0 {
		return r.dynamic
	}
	t := l.g.resolveDynamicRefUncounted(n.DynamicRef, n)
	r := l.node(n)
	r.has |= hasDynamic
	r.dynamic = t
	return t
}

// refTarget is where n's $ref (or $recursiveRef, or $dynamicRef resolved
// against the document) leads, or nil.
func (l *ledgerRun) refTarget(n *schema.Schema) *schema.Schema {
	if n == nil || n.IsBooleanSchema() || referenceOn(n) == "" {
		return nil
	}
	if r := l.nodes[n]; r != nil && r.has&hasRef != 0 {
		return r.ref
	}
	_, t := l.g.referenceTargetUncounted(n)
	r := l.node(n)
	r.has |= hasRef
	r.ref = t
	return t
}

// ledgerChild is one subschema a node holds, with the keyword (and the key
// within it) that holds it, and whether it applies to the same instance as
// its parent.
type ledgerChild struct {
	keyword string
	key     string
	node    *schema.Schema
	inPlace bool
}

// ledgerChildren lists the subschemas n holds, in a fixed order. withDefs
// includes the definition containers, which a location index needs and a
// scope does not: a definition constrains nothing until something refers to
// it.
func ledgerChildren(n *schema.Schema, withDefs bool) []ledgerChild {
	if n == nil || n.IsBooleanSchema() {
		return nil
	}
	// Sized up front: every walk of the ledger lists the children of every
	// node it visits, and growing the slice child by child was most of what
	// listing them cost.
	// Only the keywords holding several schemas are counted: the ones holding
	// one grow the slice by append, which costs little because few nodes
	// state more than one or two of them.
	size := len(n.AllOf) + len(n.AnyOf) + len(n.OneOf) + len(n.TypeSchemas) + len(n.PrefixItems) +
		len(n.DependentSchemas) + len(n.Properties) + len(n.PatternProperties)
	if n.Items != nil {
		size += len(n.Items.Schemas)
	}
	if withDefs {
		size += len(n.Defs) + len(n.Definitions)
	}
	var out []ledgerChild
	if size > 0 {
		out = make([]ledgerChild, 0, size+2)
	}
	forEachLedgerChild(n, withDefs, true, func(c ledgerChild) { out = append(out, c) })
	return out
}

// eachSubschema calls fn on every subschema ledgerChildren lists, in no
// particular order and without building the list: for a walk that visits
// every node once and orders what it keeps by itself.
func eachSubschema(n *schema.Schema, withDefs bool, fn func(*schema.Schema)) {
	forEachLedgerChild(n, withDefs, false, func(c ledgerChild) {
		if c.node != nil {
			fn(c.node)
		}
	})
}

// forEachLedgerChild is the one statement of which subschemas a node holds,
// for ledgerChildren and eachSubschema: every keyword's members in a fixed
// order of keywords, and a map's members in the order of their keys where
// sorted is set.
func forEachLedgerChild(n *schema.Schema, withDefs, sorted bool, fn func(ledgerChild)) {
	if n == nil || n.IsBooleanSchema() {
		return
	}
	list := func(kw string, subs []*schema.Schema, inPlace bool) {
		for i, s := range subs {
			fn(ledgerChild{keyword: kw, key: strconv.Itoa(i), node: s, inPlace: inPlace})
		}
	}
	one := func(kw string, s *schema.Schema, inPlace bool) {
		if s != nil {
			fn(ledgerChild{keyword: kw, node: s, inPlace: inPlace})
		}
	}
	byKey := func(kw string, m map[string]*schema.Schema, inPlace bool) {
		if sorted {
			for _, k := range sortedKeys(m) {
				fn(ledgerChild{keyword: kw, key: k, node: m[k], inPlace: inPlace})
			}
			return
		}
		// maporder: sorted is false only for a caller that orders what it keeps.
		for k, s := range m {
			fn(ledgerChild{keyword: kw, key: k, node: s, inPlace: inPlace})
		}
	}
	list("allOf", n.AllOf, true)
	list("anyOf", n.AnyOf, true)
	list("oneOf", n.OneOf, true)
	list("type", n.TypeSchemas, true)
	one("not", n.Not, true)
	one("if", n.If, true)
	one("then", n.Then, true)
	one("else", n.Else, true)
	byKey("dependentSchemas", n.DependentSchemas, true)
	byKey("properties", n.Properties, false)
	byKey("patternProperties", n.PatternProperties, false)
	if n.AdditionalProperties != nil {
		one("additionalProperties", n.AdditionalProperties.Schema, false)
	}
	one("propertyNames", n.PropertyNames, false)
	one("unevaluatedProperties", n.UnevaluatedProperties, false)
	if n.Items != nil {
		one("items", n.Items.Schema, false)
		list("items", n.Items.Schemas, false)
	}
	list("prefixItems", n.PrefixItems, false)
	if n.AdditionalItems != nil {
		one("additionalItems", n.AdditionalItems.Schema, false)
	}
	one("contains", n.Contains, false)
	one("unevaluatedItems", n.UnevaluatedItems, false)
	one("contentSchema", n.ContentSchema, false)
	if withDefs {
		byKey("$defs", n.Defs, false)
		byKey("definitions", n.Definitions, false)
	}
}

// ledgerChildList is one node's ledgerChildren with the definition
// containers, and how many of them come before those containers.
type ledgerChildList struct {
	all []ledgerChild
	own int
}

// children is ledgerChildren, remembered per node: the ledger lists the
// children of most nodes several times -- indexing the document, walking a
// scope, walking the document for I2 -- and listing them sorts every property
// map. Nothing changes a node once the ledger runs. The list is shared and is
// read, not modified; its capacity is its length, so an append copies.
func (l *ledgerRun) children(n *schema.Schema, withDefs bool) []ledgerChild {
	if n == nil {
		return nil
	}
	return l.childrenOf(l.node(n), n, withDefs)
}

// childrenOf is children for a node whose record the caller holds.
func (l *ledgerRun) childrenOf(r *ledgerNode, n *schema.Schema, withDefs bool) []ledgerChild {
	if r.has&hasKids == 0 {
		all := ledgerChildren(n, true)
		own := len(all)
		// The containers come last (ledgerChildren).
		for own > 0 && (all[own-1].keyword == "$defs" || all[own-1].keyword == "definitions") {
			own--
		}
		r.has |= hasKids
		r.kids = ledgerChildList{all: all[:len(all):len(all)], own: own}
	}
	c := r.kids
	if withDefs {
		return c.all
	}
	return c.all[:c.own:c.own]
}

// ---------------------------------------------------------------------------
// The declarations.
// ---------------------------------------------------------------------------

func (l *ledgerRun) collectDefs() {
	// The last registration of a name wins. A name is registered twice only
	// when generateTypeDef, generating it for one node, generates the same name
	// again for the node that node leads to -- a $ref whose target's
	// declaration is re-read for the referrer -- and the inner call returns
	// first. The outer node is the one the declaration stands for; it reaches
	// the inner one through its reference.
	registered := make(map[string]*schema.Schema, len(l.g.ledger.defs))
	for _, d := range l.g.ledger.defs {
		registered[d.name] = d.node
	}
	for _, td := range l.g.output.TypeDefs {
		name := td.TypeName()
		if _, dup := l.byName[name]; dup {
			continue
		}
		root := registered[name]
		if root == nil {
			// The node the name registry holds the name for.
			if h, ok := l.g.names.holderOf(name); ok && h.kind == holderType {
				root = h.node
			}
		}
		if root == nil {
			root = l.g.typeSchemas[name]
		}
		d := &ledgerDefInfo{name: name, td: td, root: root}
		if root != nil {
			l.setRoot(d, root)
		}
		l.defs = append(l.defs, d)
		l.byName[name] = d
	}
}

// setRoot records the node a declaration was generated for.
func (l *ledgerRun) setRoot(d *ledgerDefInfo, root *schema.Schema) {
	d.root = root
	d.canonRoot = l.canonical(root)
	// A declaration answers for the node it was generated for and, when that
	// node is one the generator built -- the allOf of two contributions to one
	// property, a merge target -- for the nodes it was built from: those are
	// the children a parent's scope meets.
	l.byRoot[d.canonRoot] = append(l.byRoot[d.canonRoot], d)
	for _, a := range l.authors(root) {
		if a != d.canonRoot {
			l.byRoot[a] = append(l.byRoot[a], d)
		}
	}
}

// inferRoots finds the node for each declaration the generator recorded none
// for, from the IR that refers to it: a position holding a value of the type
// is a position whose schema the type was generated from. Several arms declare
// a type -- an enum for an array element, a wrapper for a tuple slot -- without
// passing through generateTypeDef or resolvePropertyType, which are where the
// generator records an owner; the IR is where every one of them is referred to.
func (l *ledgerRun) inferRoots() {
	for round := 0; round < 4; round++ {
		changed := false
		note := func(node *schema.Schema, name string) {
			if node == nil || name == "" {
				return
			}
			if t := l.byName[name]; t != nil && t.root == nil {
				l.setRoot(t, node)
				changed = true
			}
		}
		for _, d := range l.defs {
			if d.root == nil {
				continue
			}
			l.eachTypedPosition(d, note)
		}
		if !changed {
			return
		}
	}
}

// eachTypedPosition calls visit with every schema position d's IR holds a
// value of a named type at, and the type's name.
func (l *ledgerRun) eachTypedPosition(d *ledgerDefInfo, visit func(*schema.Schema, string)) {
	group := l.group(d)
	byGoType := func(nodes []*schema.Schema, t GoType) {
		for depth := 0; t != nil && depth <= maxItemLevels; depth++ {
			if pt, ok := t.(*PointerType); ok {
				t = pt.Inner
			}
			for _, n := range nodes {
				visit(n, namedTypeName(t))
			}
			var next []*schema.Schema
			switch ct := t.(type) {
			case *ArrayType:
				for _, n := range nodes {
					if n != nil && !n.IsBooleanSchema() && n.Items != nil && n.Items.Schema != nil {
						next = append(next, n.Items.Schema)
					}
				}
				t = ct.ItemType
			case *MapType:
				for _, n := range nodes {
					if n != nil && !n.IsBooleanSchema() && n.AdditionalProperties != nil && n.AdditionalProperties.Schema != nil {
						next = append(next, n.AdditionalProperties.Schema)
					}
				}
				t = ct.ValueType
			default:
				return
			}
			nodes = next
		}
	}
	levels := func(ivs []ItemValidationDef) {
		for i := range ivs {
			for j := range ivs[i].Levels {
				lv := &ivs[i].Levels[j]
				visit(lv.Claim.Source, lv.ElemTypeName)
				for k := range lv.TupleItems {
					visit(lv.TupleItems[k].Claim.Source, lv.TupleItems[k].TypeName)
				}
				if lv.TupleTail != nil {
					visit(lv.TupleTail.Claim.Source, lv.TupleTail.TypeName)
				}
				if lv.Contains != nil {
					visit(lv.Contains.Claim.Source, lv.Contains.TypeName)
				}
			}
		}
	}
	tuple := func(items []TupleItemDef, tail *TupleItemDef) {
		for i := range items {
			visit(items[i].Claim.Source, items[i].TypeName)
		}
		if tail != nil {
			visit(tail.Claim.Source, tail.TypeName)
		}
	}
	switch td := d.td.(type) {
	case *StructDef:
		var buf []*schema.Schema
		for _, f := range td.Fields {
			buf = propertyNodes(buf, group, f.JSONName)
			byGoType(buf, f.Type)
		}
		for i := range td.OneOfs {
			for _, v := range td.OneOfs[i].Variants {
				visit(v.Claim.Source, namedTypeName(v.Type))
			}
		}
		levels(td.ItemValidations)
		for i := range td.ContainsValidations {
			if c := td.ContainsValidations[i].Contains; c != nil {
				visit(c.Claim.Source, c.TypeName)
			}
		}
		for i := range td.TupleValidations {
			tuple(td.TupleValidations[i].Items, td.TupleValidations[i].Tail)
		}
		for i := range td.PatternProperties {
			pp := &td.PatternProperties[i]
			if pp.Claim.Source != nil {
				visit(pp.Claim.Source.PatternProperties[pp.Pattern], pp.TypeName)
			}
		}
		if ap := td.AdditionalProperties; ap != nil && ap.Claim.Source != nil &&
			ap.Claim.Source.AdditionalProperties != nil && ap.Claim.Source.AdditionalProperties.Schema != nil {
			byGoType([]*schema.Schema{ap.Claim.Source.AdditionalProperties.Schema}, ap.ValueType)
		}
		for i := range td.BranchOverflowChecks {
			bc := &td.BranchOverflowChecks[i]
			if b := bc.Claim.Source; b != nil {
				switch {
				case bc.Keyword == "additionalProperties" && b.AdditionalProperties != nil:
					visit(b.AdditionalProperties.Schema, bc.TypeName)
				case bc.Keyword == "unevaluatedProperties":
					visit(b.UnevaluatedProperties, bc.TypeName)
				}
			}
		}
	case *AliasDef:
		byGoType(group, ad(td))
		levels(td.ItemValidations)
		tuple(td.TupleItems, td.TupleTail)
		if td.Contains != nil {
			visit(td.Contains.Claim.Source, td.Contains.TypeName)
		}
	case *InferredAliasDef:
		for i := range td.TupleItems {
			visit(td.TupleItems[i].Claim.Source, td.TupleItems[i].TypeName)
		}
		for _, n := range group {
			if n.IsBooleanSchema() {
				continue
			}
			if n.Items != nil && n.Items.Schema != nil {
				visit(n.Items.Schema, td.ItemsTypeName)
			}
			if n.AdditionalItems != nil {
				visit(n.AdditionalItems.Schema, td.AdditionalItemsTypeName)
			} else if len(n.PrefixItems) > 0 && n.Items != nil {
				visit(n.Items.Schema, td.AdditionalItemsTypeName)
			}
		}
		if td.Contains != nil {
			visit(td.Contains.Claim.Source, td.Contains.TypeName)
		}
	case *TypeOnlySchemaDef:
		for i := range td.TypeBranches {
			visit(td.TypeBranches[i].Claim.Source, td.TypeBranches[i].TypeName)
		}
	}
}

// ad is an alias's Go type with the named type it delegates to, when that is
// not spelled by its underlying type.
func ad(a *AliasDef) GoType {
	if a.ValidateAs != "" && namedTypeName(a.Underlying) == "" {
		return &NamedType{Name: a.ValidateAs}
	}
	return a.Underlying
}

// ---------------------------------------------------------------------------
// Recording a claim.
// ---------------------------------------------------------------------------

// claim credits keyword of src, and of every author of src whose value src's
// implies, to d.
func (l *ledgerRun) claim(d *ledgerDefInfo, src *schema.Schema, keyword string, how claimHow) {
	if src == nil || keyword == "" {
		return
	}
	d.setPool(ledgerKey{src, keyword}, how)
	for _, a := range l.authors(src) {
		if impliesKeyword(src, a, keyword, l) {
			if _, ok := d.pool[ledgerKey{a, keyword}]; !ok {
				d.setPool(ledgerKey{a, keyword}, how)
			}
		}
	}
}

// setPool records a claim on one keyword of one node.
func (d *ledgerDefInfo) setPool(k ledgerKey, how claimHow) {
	if d.pool == nil {
		d.pool = make(map[ledgerKey]claimHow)
	}
	d.pool[k] = how
}

// setWhole records a claim on every keyword of n.
func (d *ledgerDefInfo) setWhole(n *schema.Schema, how claimHow) {
	if d.whole == nil {
		d.whole = make(map[*schema.Schema]claimHow)
	}
	d.whole[n] = how
}

// claimWhole credits every keyword n states to d: n was compiled whole into an
// evaluator literal, or the IR refuses every instance n could judge.
func (l *ledgerRun) claimWhole(d *ledgerDefInfo, n *schema.Schema, how claimHow) {
	if n == nil {
		return
	}
	d.setWhole(n, how)
	if c := l.canonical(n); c != n {
		if _, ok := d.whole[c]; !ok {
			d.setWhole(c, how)
		}
	}
}

// claimed reports whether d holds a claim on keyword of n.
func (d *ledgerDefInfo) claimed(n *schema.Schema, keyword string) bool {
	if _, ok := d.whole[n]; ok {
		return true
	}
	_, ok := d.pool[ledgerKey{n, keyword}]
	return ok
}

// impliesKeyword reports whether the value src holds for keyword says at
// least what a's does: that an instance src accepts, a accepts, as far as that
// keyword goes.
//
// The per-keyword readings are the lattice each keyword's conjunction lives
// in: a larger lower bound, a smaller upper bound, a multiple of a multipleOf,
// a subset of an enum or of a set of types, a superset of required names.
// Where a keyword has no such order -- pattern, format, the content pair,
// const -- only the same value implies it. That is exactly the case a merge
// keeping one of two values loses: the value kept implies itself and not the
// other.
func impliesKeyword(src, a *schema.Schema, keyword string, l *ledgerRun) bool {
	if src == a {
		return true
	}
	if src == nil || a == nil {
		return false
	}
	if a.IsFalseSchema() {
		return keyword == "false" && src.IsFalseSchema()
	}
	switch keyword {
	case "minLength":
		return flexGE(src.MinLength, a.MinLength)
	case "maxLength":
		return flexGE(a.MaxLength, src.MaxLength)
	case "minItems":
		return flexGE(src.MinItems, a.MinItems)
	case "maxItems":
		return flexGE(a.MaxItems, src.MaxItems)
	case "minProperties":
		return flexGE(src.MinProperties, a.MinProperties)
	case "maxProperties":
		return flexGE(a.MaxProperties, src.MaxProperties)
	case "minContains":
		return flexGE(src.MinContains, a.MinContains)
	case "maxContains":
		return flexGE(a.MaxContains, src.MaxContains)
	case "minimum":
		return numberGE(src.Minimum, a.Minimum)
	case "maximum":
		return numberGE(a.Maximum, src.Maximum)
	case "exclusiveMinimum":
		return exclusiveImplies(src.ExclusiveMinimum, a.ExclusiveMinimum, src.Minimum, a.Minimum, true)
	case "exclusiveMaximum":
		return exclusiveImplies(src.ExclusiveMaximum, a.ExclusiveMaximum, src.Maximum, a.Maximum, false)
	case "multipleOf":
		return multipleImplies(src.MultipleOf, a.MultipleOf)
	case "pattern":
		return src.Pattern != nil && a.Pattern != nil && *src.Pattern == *a.Pattern
	case "format":
		return src.Format != nil && a.Format != nil && *src.Format == *a.Format
	case "contentEncoding":
		return src.ContentEncoding == a.ContentEncoding
	case "contentMediaType":
		return src.ContentMediaType == a.ContentMediaType
	case "uniqueItems":
		return src.UniqueItems != nil && *src.UniqueItems && a.UniqueItems != nil
	case "type":
		return len(src.Type) > 0 && kindsOfTypeList(src.Type)&^kindsOfTypeList(a.Type) == 0
	case "required":
		return stringSubset(a.Required, src.Required)
	case "enum":
		return valuesSubset(enumLikeValues(src), a.Enum)
	case "const":
		return (a.Const != nil || a.ConstIsNull) && valuesSubset(enumLikeValues(src), enumLikeValues(a)) &&
			len(enumLikeValues(src)) > 0
	case "dependentRequired":
		// maporder: a predicate; it answers the same whichever trigger it stops at.
		for trigger, need := range a.DependentRequired {
			if !stringSubset(need, src.DependentRequired[trigger]) {
				return false
			}
		}
		return true
	case "$ref":
		return src.Ref == a.Ref && l.refTarget(src) == l.refTarget(a)
	}
	// A keyword holding subschemas: implied where the subschema src holds is
	// the one a holds, or a conjunction that includes it.
	return sameChildren(src, a, keyword, l)
}

// sameChildren reports whether the subschemas src holds under keyword are a's,
// one for one, or conjunctions including them.
func sameChildren(src, a *schema.Schema, keyword string, l *ledgerRun) bool {
	var sc, ac []*schema.Schema
	for _, c := range l.children(src, false) {
		if c.keyword == keyword {
			sc = append(sc, c.node)
		}
	}
	for _, c := range l.children(a, false) {
		if c.keyword == keyword {
			ac = append(ac, c.node)
		}
	}
	if len(ac) == 0 {
		return keywordBoolFalse(a, keyword) && keywordBoolFalse(src, keyword)
	}
	if len(sc) != len(ac) {
		return false
	}
	for i := range ac {
		if sc[i] == ac[i] || l.canonical(sc[i]) == ac[i] {
			continue
		}
		found := false
		for _, x := range l.authors(sc[i]) {
			if x == ac[i] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// keywordBoolFalse reports whether n states keyword as the boolean false,
// which the two SchemaOrBool keywords hold without a node.
func keywordBoolFalse(n *schema.Schema, keyword string) bool {
	switch keyword {
	case "additionalProperties":
		return n.AdditionalProperties != nil && n.AdditionalProperties.Bool != nil && !*n.AdditionalProperties.Bool
	case "additionalItems":
		return n.AdditionalItems != nil && n.AdditionalItems.Bool != nil && !*n.AdditionalItems.Bool
	}
	return false
}

func flexGE(a, b *schema.FlexInt) bool {
	return a != nil && b != nil && a.Int() >= b.Int()
}

func numberGE(a, b *schema.Number) bool {
	if a == nil || b == nil {
		return false
	}
	ra, ok1 := a.Rat()
	rb, ok2 := b.Rat()
	return ok1 && ok2 && ra.Cmp(rb) >= 0
}

// exclusiveImplies reads the two spellings of an exclusive bound: draft 4's
// boolean beside the plain bound, and the number every later draft writes.
func exclusiveImplies(src, a *schema.SchemaOrFloat, srcPlain, aPlain *schema.Number, lower bool) bool {
	if a == nil {
		return false
	}
	bound := func(e *schema.SchemaOrFloat, plain *schema.Number) *big.Rat {
		if e == nil {
			return nil
		}
		if e.Number != nil {
			if r, ok := e.Number.Rat(); ok {
				return r
			}
			return nil
		}
		if e.Bool != nil && *e.Bool && plain != nil {
			if r, ok := plain.Rat(); ok {
				return r
			}
		}
		return nil
	}
	rs, ra := bound(src, srcPlain), bound(a, aPlain)
	if rs == nil || ra == nil {
		return false
	}
	if lower {
		return rs.Cmp(ra) >= 0
	}
	return rs.Cmp(ra) <= 0
}

// multipleImplies reports whether every multiple of src is a multiple of a:
// whether src is itself an integral multiple of a.
func multipleImplies(src, a *schema.Number) bool {
	if src == nil || a == nil {
		return false
	}
	rs, ok1 := src.Rat()
	ra, ok2 := a.Rat()
	if !ok1 || !ok2 || ra.Sign() == 0 || rs.Sign() <= 0 {
		return false
	}
	q := new(big.Rat).Quo(rs, ra)
	return q.IsInt()
}

func stringSubset(sub, super []string) bool {
	have := make(map[string]bool, len(super))
	for _, s := range super {
		have[s] = true
	}
	for _, s := range sub {
		if !have[s] {
			return false
		}
	}
	return true
}

// valuesSubset reports whether every value of sub is one of super, by JSON
// equality.
func valuesSubset(sub, super []any) bool {
	// A short list is scanned, comparing strings, booleans and null directly
	// -- nearly every enumeration is those -- and anything else by its key.
	if len(super) <= 16 {
		for _, v := range sub {
			if !slices.ContainsFunc(super, func(w any) bool { return sameJSONValue(v, w) }) {
				return false
			}
		}
		return true
	}
	// A long one is put in a set: its strings as themselves, everything else
	// by its key, which never equals a string's.
	strs := make(map[string]bool, len(super))
	var others map[string]bool
	for _, w := range super {
		if s, ok := w.(string); ok {
			strs[s] = true
			continue
		}
		if others == nil {
			others = make(map[string]bool)
		}
		others[exactEnumValueKey(w)] = true
	}
	for _, v := range sub {
		if s, ok := v.(string); ok {
			if !strs[s] {
				return false
			}
		} else if !others[exactEnumValueKey(v)] {
			return false
		}
	}
	return true
}

// sameJSONValue reports whether a and b are one JSON value, as
// exactEnumValueKey tells values apart.
func sameJSONValue(a, b any) bool {
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case nil:
		return b == nil
	}
	switch b.(type) {
	case string, bool, nil:
		return false
	}
	return exactEnumValueKey(a) == exactEnumValueKey(b)
}

// jsonUnmarshalNumber decodes JSON text holding numbers as json.Number, so a
// value compares as the literal the schema wrote.
func jsonUnmarshalNumber(data []byte, v *any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(v)
}
