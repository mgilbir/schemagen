package generator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// This file walks each declaration's scope and holds it to I1, and walks the
// document to hold the run to I2. See ledgerrun.go for the two invariants.

// ledgerHierarchical are the keywords whose whole meaning is their
// subschemas: an allOf, a property map, an item schema, a reference. They are
// claimed by their subschemas being claimed, so the scope walk judges the
// subschemas and not the keyword. The keywords holding subschemas that are not
// in this set -- contains, anyOf, oneOf, not, if, dependentSchemas and the two
// unevaluated keywords -- also assert something of their own (that a match
// exists, how many branches match, which branch applies, what is left over),
// and that is claimed or not like any leaf keyword.
var ledgerHierarchical = map[string]bool{
	"allOf": true, "properties": true, "patternProperties": true,
	"items": true, "prefixItems": true, "propertyNames": true,
	"$ref": true, "$dynamicRef": true, "$recursiveRef": true,
	"then": true, "else": true,
}

// scopeWalk is one declaration's walk.
type scopeWalk struct {
	l *ledgerRun
	d *ledgerDefInfo
	// id marks the nodes this walk has visited (ledgerNode.visitedBy).
	id int32
	// shared is set for the walk of a declaration that claims nothing, which
	// shares what it has visited with every other such walk; see visit.
	shared bool
}

// scopeFrame is what a node's position in the walk says about it.
type scopeFrame struct {
	// prop is the struct property the position lies under, for a struct
	// declaration, which narrows what counts as validated through; "" where no
	// property is known.
	prop string
	// kinds are the instance kinds the position can hold, as the nodes above
	// it in the same in-place group state.
	kinds jsonKinds
	// inRootGroup is set while the walk is still in the declaration's own
	// in-place group, whose properties are the declaration's properties.
	inRootGroup bool
	// parentClaimed is set for a boolean child whose parent keyword is itself
	// claimed -- additionalProperties: false forbidden by the struct, a closed
	// tuple's false tail -- which is what claims the `false`.
	parentClaimed bool
	// undispatched names a type the node has of its own that this position
	// does not validate through, for the message.
	undispatched string
	// values, when hasValues is set, are the only values the instance at this
	// position can take: an enum or const the declaration enforces restricts
	// it to them. See ledgervalues.go.
	values    []any
	hasValues bool
}

// checkScope holds d to I1.
func (l *ledgerRun) checkScope(d *ledgerDefInfo) {
	root := l.scopeRoot(d)
	if root == nil {
		return
	}
	l.walks++
	w := &scopeWalk{l: l, d: d, id: l.walks, shared: d.claimsNothing()}
	// The declaration's own Go type decodes only some kinds, and every
	// instance it holds is one of them.
	kinds := l.goKinds(&NamedType{Name: d.name}, 0) | kindNull
	w.visit(root, scopeFrame{kinds: kinds, inRootGroup: true})
}

// ledgerSharedVisit is a node and the kinds of instance a walk met it with.
type ledgerSharedVisit struct {
	node  *schema.Schema
	kinds jsonKinds
}

func (w *scopeWalk) visit(n *schema.Schema, f scopeFrame) {
	if n == nil {
		return
	}
	l, d := w.l, w.d
	rn := l.node(n)
	if rn.visitedBy == w.id {
		return
	}
	rn.visitedBy = w.id
	if w.shared {
		// A declaration that claims nothing and hands nothing on meets every
		// node the way every other such declaration does: what a visit finds
		// depends on the node and the kinds the instance can be, and on no
		// claim. Once one of them has visited a node with those kinds, what
		// the visit could report is reported, and what lies below it is
		// reached, so the rest stop there. Without this, n such declarations
		// reaching one graph -- a recursive anyOf the evaluator declined,
		// each branch a type of its own -- walked it n times.
		k := ledgerSharedVisit{node: n, kinds: f.kinds}
		if l.sharedVisits[k] {
			return
		}
		if l.sharedVisits == nil {
			l.sharedVisits = make(map[ledgerSharedVisit]bool)
		}
		l.sharedVisits[k] = true
	}
	c := l.canonical(n)
	rn.reached = true
	if c != n {
		l.node(c).reached = true
	}
	if d.forbidsAll || d.carriesWhole(n) || d.carriesWhole(c) {
		// Compiled whole into an evaluator literal, or refused whenever it is
		// present at all: nothing under it is left to claim.
		l.markReached(n)
		return
	}
	if n.IsBooleanSchema() {
		if n.IsFalseSchema() && !f.parentClaimed && !d.claimed(n, "false") && !d.claimed(c, "false") {
			w.report(n, "false", f)
		}
		return
	}
	kinds := f.kinds
	assertions := l.assertionsOf(rn, n)
	values, hasValues := f.values, f.hasValues
	for _, k := range assertions {
		switch k {
		case "type":
			kinds &= kindsOfTypeList(n.Type) | kindNull
		case "enum", "const":
			if w.claimed(n, c, k) {
				restricted := enumLikeValues(n)
				if k == "enum" {
					restricted = n.Enum
				}
				if hasValues {
					restricted = intersectValues(values, restricted)
				}
				values, hasValues = restricted, true
			}
		}
	}
	// Two applicators can assert nothing whatever their subschemas say: an
	// anyOf one of whose branches every instance satisfies, and an if whose
	// then and else demand nothing. Their subschemas are then not demands at
	// all, and a declaration that emitted nothing for them is right.
	anyOfVacuous := len(n.AnyOf) > 0 && l.anyBranchAlwaysHolds(w, n.AnyOf, kinds)
	ifInert := n.If != nil && !l.subtreeAsserts(n.Then, make(map[*schema.Schema]bool)) &&
		!l.subtreeAsserts(n.Else, make(map[*schema.Schema]bool))
	// And a not over a schema no instance satisfies, which every instance
	// satisfies.
	notInert := n.Not != nil && l.g.schemaForbidsEveryValue(n.Not)
	for _, k := range assertions {
		if w.claimed(n, c, k) {
			continue
		}
		if kk, scoped := keywordKinds[k]; scoped && kinds&kk == 0 {
			continue
		}
		if k == "type" && (f.kinds&^kindNull)&^kindsOfTypeList(n.Type) == 0 {
			// Every kind the position can hold is one the type admits.
			continue
		}
		if ledgerHierarchical[k] {
			continue
		}
		if hasValues && allValuesSatisfy(n, k, values) {
			continue
		}
		switch k {
		case "additionalProperties":
			// Schema-valued, its subschema is judged; `true`, it asserts
			// nothing. Only `false` is a demand of the keyword's own.
			if !keywordBoolFalse(n, k) {
				continue
			}
		case "additionalItems":
			if !keywordBoolFalse(n, k) {
				continue
			}
			if closedByMaxItems(w, n, c) {
				continue
			}
		case "unevaluatedItems":
			if l.unevaluatedItemsVacuous(n) {
				continue
			}
		case "unevaluatedProperties":
			if n.UnevaluatedProperties != nil && n.UnevaluatedProperties.IsTrueSchema() {
				continue
			}
		case "anyOf":
			if anyOfVacuous {
				continue
			}
		case "if":
			if ifInert {
				continue
			}
		case "not":
			if notInert {
				continue
			}
		case "dependentRequired":
			if dependentRequiredInert(n) {
				continue
			}
		}
		w.report(n, k, f)
	}

	binds := func(k string) bool { return stringsContain(assertions, k) }
	for _, ch := range l.childrenOf(rn, n, false) {
		if !binds(ch.keyword) {
			// then/else without an if, a $ref's siblings where the dialect says
			// it replaces them, contentSchema, an annotation-only keyword.
			continue
		}
		if (anyOfVacuous && ch.keyword == "anyOf") ||
			(ifInert && (ch.keyword == "if" || ch.keyword == "then" || ch.keyword == "else")) ||
			(notInert && ch.keyword == "not") {
			l.markReached(ch.node)
			continue
		}
		// A keyword about a kind the instance cannot be says nothing, and
		// neither does anything under it.
		if kk, scoped := keywordKinds[ch.keyword]; scoped && kinds&kk == 0 {
			l.markReached(ch.node)
			continue
		}
		next := scopeFrame{prop: f.prop, kinds: kindAll, undispatched: f.undispatched}
		if ch.inPlace {
			next.kinds = kinds
			next.inRootGroup = f.inRootGroup
			next.values, next.hasValues = values, hasValues
		} else {
			if f.inRootGroup && ch.keyword == "properties" {
				next.prop = ch.key
			}
			if hasValues {
				next.values, next.hasValues = projectValues(values, n, ch.keyword, ch.key)
			}
		}
		if w.delegated(ch.node, next.prop) {
			l.markReachedShallow(ch.node)
			continue
		}
		if skips := w.partlyDelegated(ch.node, next.prop); skips != 0 {
			// The type takes only some kinds; the rest are this
			// declaration's, and only they are walked.
			if next.kinds &= skips; next.kinds == 0 {
				l.markReachedShallow(ch.node)
				continue
			}
		}
		if owner := w.ownerName(ch.node); owner != "" {
			next.undispatched = owner
		}
		if ch.node.IsFalseSchema() {
			next.parentClaimed = w.claimed(n, c, ch.keyword) ||
				(ch.keyword == "items" && len(n.PrefixItems) > 0 && closedByMaxItems(w, n, c))
		}
		w.visit(ch.node, next)
	}
	if bindsReference(assertions) {
		for _, t := range [2]*schema.Schema{l.refTarget(n), l.dynamicBesideRef(n)} {
			if t == nil {
				continue
			}
			next := scopeFrame{prop: f.prop, kinds: kinds, inRootGroup: f.inRootGroup, undispatched: f.undispatched,
				values: values, hasValues: hasValues}
			if w.delegated(t, f.prop) {
				l.markReachedShallow(t)
				continue
			}
			if skips := w.partlyDelegated(t, f.prop); skips != 0 {
				if next.kinds &= skips; next.kinds == 0 {
					l.markReachedShallow(t)
					continue
				}
			}
			if owner := w.ownerName(t); owner != "" {
				next.undispatched = owner
			}
			w.visit(t, next)
		}
	}
}

// dependentRequiredInert reports whether n's dependentRequired names no
// property for any trigger.
func dependentRequiredInert(n *schema.Schema) bool {
	// maporder: a predicate; it answers the same whichever entry it stops at.
	for _, need := range n.DependentRequired {
		if len(need) > 0 {
			return false
		}
	}
	return true
}

// closedByMaxItems reports whether n's closed tuple tail -- items:false beside
// prefixItems, additionalItems:false beside array-form items -- is already
// said by a claimed maxItems no larger than the tuple: then no array long
// enough to reach the tail gets past the bound.
func closedByMaxItems(w *scopeWalk, n, c *schema.Schema) bool {
	if n.MaxItems == nil || !w.claimed(n, c, "maxItems") {
		return false
	}
	tuple := len(n.PrefixItems)
	if tuple == 0 && n.Items != nil {
		tuple = len(n.Items.Schemas)
	}
	return tuple > 0 && n.MaxItems.Int() <= tuple
}

// claimed reports whether keyword of n (or of c, the node n copies) is claimed
// for this declaration.
func (w *scopeWalk) claimed(n, c *schema.Schema, keyword string) bool {
	return w.d.claimed(n, keyword) || (c != n && w.d.claimed(c, keyword))
}

// delegated reports whether the declaration hands node to another generated
// type at this position: node is the node that type was generated for, and the
// declaration's IR validates through it here.
func (w *scopeWalk) delegated(node *schema.Schema, prop string) bool {
	name := w.delegate(node, prop)
	return name != "" && w.d.dispatchSkips[name] == 0
}

// partlyDelegated reports the kinds of value the type node is delegated to
// never sees, where node is delegated to one that validates only some: a
// wrapper that holds a value of another kind raw and hands only the kind it
// decodes to that type. The node's keywords about those kinds are this
// declaration's to carry. 0 when node is delegated whole or not at all.
func (w *scopeWalk) partlyDelegated(node *schema.Schema, prop string) jsonKinds {
	if name := w.delegate(node, prop); name != "" {
		return w.d.dispatchSkips[name]
	}
	return 0
}

// delegate names the generated type the declaration hands node to, or "".
func (w *scopeWalk) delegate(node *schema.Schema, prop string) string {
	if node == nil {
		return ""
	}
	cn := w.l.canonical(node)
	for _, t := range w.l.byRoot[cn] {
		if t == w.d {
			continue
		}
		if w.d.dispatches(prop, t.name) {
			return t.name
		}
		// A type with no Validate of its own decodes the value and has nothing
		// more to say; the Go type the position holds is that type, and what it
		// enforces is its own declaration's business.
		if !localTypeIsValidatable(t.td) && w.d.typeRefs[t.name] {
			return t.name
		}
	}
	// A type an earlier Generate call of a shared-types run declared, or
	// another package's: its own run answers for it.
	if name, ok := w.l.g.nodeTypeNames[cn]; ok && w.l.byName[name] == nil && w.d.dispatches(prop, name) {
		return name
	}
	// A type generated for another node that says exactly what this one says:
	// two $defs keys spelling one definition are one type on purpose, and so
	// are two positions whose schemas differ only in their prose. The type
	// answers for this node where the position validates through it -- and not
	// where two different schemas merely derived one Go name, which leaves this
	// node's keywords checked by nothing.
	answers := func(name string) bool {
		t := w.l.byName[name]
		return t != nil && t != w.d && t.canonRoot != nil && w.l.sameContent(cn, t.canonRoot)
	}
	if prop != "" {
		if i := slices.IndexFunc(w.d.dispatchProp[prop], answers); i >= 0 {
			return w.d.dispatchProp[prop][i]
		}
		return ""
	}
	var found []string
	// maporder: every answering name is collected and the least returned.
	for name := range w.d.dispatchAll {
		if answers(name) {
			found = append(found, name)
		}
	}
	if len(found) == 0 {
		return ""
	}
	return slices.Min(found)
}

// sameContent reports whether two nodes of one document state exactly the
// same thing: the same keywords with the same values, including the ones the
// marshaled form leaves out.
func (l *ledgerRun) sameContent(a, b *schema.Schema) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil || a.IsBooleanSchema() != b.IsBooleanSchema() {
		return false
	}
	da, okA := writtenIn(a)
	db, okB := writtenIn(b)
	if !okA || !okB || da != db {
		// A relative reference inside means the same thing only within one
		// document.
		return false
	}
	if a.ConstIsNull != b.ConstIsNull || len(a.TypeSchemas) > 0 || len(b.TypeSchemas) > 0 {
		return false
	}
	// The shape first, which rules out almost every pair without digesting
	// either subtree. Each test is one two equal nodes pass; the digest below
	// is what decides.
	if len(a.Properties) != len(b.Properties) || len(a.Required) != len(b.Required) ||
		len(a.AllOf) != len(b.AllOf) || len(a.AnyOf) != len(b.AnyOf) || len(a.OneOf) != len(b.OneOf) ||
		len(a.Enum) != len(b.Enum) || len(a.Type) != len(b.Type) || referenceOn(a) != referenceOn(b) ||
		(a.Items == nil) != (b.Items == nil) || (a.AdditionalProperties == nil) != (b.AdditionalProperties == nil) ||
		len(a.PatternProperties) != len(b.PatternProperties) || len(a.PrefixItems) != len(b.PrefixItems) {
		return false
	}
	for i := range a.Type {
		if a.Type[i] != b.Type[i] {
			return false
		}
	}
	// maporder: a predicate; it answers the same whichever key it stops at.
	for k := range a.Properties {
		if _, ok := b.Properties[k]; !ok {
			return false
		}
	}
	ha := l.contentHash(a)
	return ha != "" && ha == l.contentHash(b)
}

// contentHash is a digest of what a node asserts, which two nodes can be
// compared by: its own keywords, annotations left out -- a title or a
// description says nothing about a document, and two definitions that differ
// only in their prose are one type on purpose -- and, for each subschema it
// holds, the keyword and key that hold it and the subschema's own digest. Each
// node is digested once, so comparing any two costs a lookup. "" when a node's
// keywords cannot be read.
func (l *ledgerRun) contentHash(n *schema.Schema) string {
	if n == nil {
		return "nil"
	}
	rec := l.node(n)
	if rec.has&hasForm != 0 {
		return rec.form
	}
	rec.has |= hasForm // a node reached again while it is being digested reads ""
	own, ok := ownAssertionsJSON(n)
	if !ok {
		return ""
	}
	h := sha256.New()
	h.Write(own)
	for _, k := range sortedKeys(n.Extensions) {
		if inertKeywords[k] {
			continue
		}
		h.Write([]byte("\x1f" + k + "\x1e"))
		h.Write(n.Extensions[k])
	}
	for _, c := range l.children(n, true) {
		child := l.contentHash(c.node)
		if child == "" {
			return ""
		}
		h.Write([]byte("\x1d" + c.keyword + "\x1e" + c.key + "\x1e" + child))
	}
	sum := hex.EncodeToString(h.Sum(nil))
	rec.form = sum
	return sum
}

// ownAssertionsJSON is a node's marshaled keywords without its subschemas and
// without the keywords that constrain nothing.
func ownAssertionsJSON(n *schema.Schema) ([]byte, bool) {
	if n.IsBooleanSchema() {
		if n.IsTrueSchema() {
			return []byte("true"), true
		}
		return []byte("false"), true
	}
	c := *n
	c.Properties, c.PatternProperties, c.Defs, c.Definitions, c.DependentSchemas = nil, nil, nil, nil, nil
	c.AllOf, c.AnyOf, c.OneOf, c.PrefixItems, c.TypeSchemas = nil, nil, nil, nil, nil
	c.Not, c.If, c.Then, c.Else, c.Contains, c.PropertyNames = nil, nil, nil, nil, nil, nil
	c.UnevaluatedItems, c.UnevaluatedProperties, c.ContentSchema, c.Items = nil, nil, nil, nil
	if n.AdditionalProperties != nil {
		c.AdditionalProperties = &schema.SchemaOrBool{Bool: n.AdditionalProperties.Bool}
	}
	if n.AdditionalItems != nil {
		c.AdditionalItems = &schema.SchemaOrBool{Bool: n.AdditionalItems.Bool}
	}
	body, err := json.Marshal(c)
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, false
	}
	// maporder: deletes per key; no order is observable.
	for k := range m {
		if nonConstrainingKeywords[k] && k != "$id" && k != "id" {
			delete(m, k)
		}
	}
	out, err := json.Marshal(m)
	return out, err == nil
}

// ownerName names the type node was generated for, where it has one other
// than the declaration being walked.
func (w *scopeWalk) ownerName(node *schema.Schema) string {
	for _, t := range w.l.byRoot[w.l.canonical(node)] {
		if t != w.d {
			return t.name
		}
	}
	return ""
}

// report records an unclaimed keyword of n.
//
// A keyword written inside another the runtime evaluator declined -- a node
// below a `not` too large for it, say -- is unchecked because that keyword is,
// and where the ledger has already reported the keyword above, the finding is
// folded into that one instead of standing beside it: the one
// above says what went wrong and, in its count, how much it takes with it. A
// chain of declined keywords otherwise reports every link of the chain at its
// own location, and locations grow with depth, so a document n levels deep
// read as n findings of up to n tokens each -- quadratic in the input, in time
// and in the length of the report.
//
// Whichever declaration's walk reported the keyword above, it covers the one
// below: a finding is a keyword at a location, reported once whatever reaches
// it, and the keyword below is unchecked because the one above is. Were the
// cover asked of the same declaration only, every other declaration reaching
// the node would have to walk to it to report it again, and in a graph every
// declaration reaches that is a walk of the whole graph per declaration.
func (w *scopeWalk) report(n *schema.Schema, keyword string, f scopeFrame) {
	l := w.l
	c := l.canonical(n)
	key := ledgerFindingKey{node: c, keyword: keyword}
	if _, ok := l.reported[key]; ok {
		return
	}
	reason, above := l.declineReason(n, keyword)
	if above.node != nil {
		if at, ok := l.reported[ledgerFindingKey{node: l.canonical(above.node), keyword: above.keyword}]; ok {
			fk := ledgerFindingKey{node: c, keyword: keyword}
			if !l.foldedKeys[fk] {
				if l.folded == nil {
					l.folded = make(map[int]int)
					l.foldedKeys = make(map[ledgerFindingKey]bool)
				}
				l.foldedKeys[fk] = true
				l.folded[at]++
			}
			return
		}
	}
	if reason == "" {
		switch {
		case f.undispatched != "":
			reason = "it has a type of its own, " + f.undispatched + ", which this position does not validate through, and no check here carries it"
		default:
			reason = "no check in the generated code carries it"
		}
	}
	l.reported[key] = len(l.findings)
	l.findings = append(l.findings, LedgerEntry{
		Location: l.locationOf(n),
		Keyword:  keyword,
		Def:      w.d.name,
		Reason:   reason,
	})
}

// noteFolded says, on each finding others were folded into, how many.
func (l *ledgerRun) noteFolded() {
	// maporder: each entry edits its own finding; no order is observable.
	for at, count := range l.folded {
		noun := "keywords"
		if count == 1 {
			noun = "keyword"
		}
		l.findings[at].Reason += "; the " + strconv.Itoa(count) + " " + noun + " written inside it go unchecked with it"
	}
}

// declineReason is why the runtime evaluator declined keyword of n or a node
// above it, where a collector offered it one, and -- where it was a node
// above -- the keyword of that node n is written inside.
func (l *ledgerRun) declineReason(n *schema.Schema, keyword string) (string, ledgerKey) {
	decl := l.g.ledger.declines
	if len(decl) == 0 {
		return "", ledgerKey{}
	}
	c := l.canonical(n)
	for _, m := range []*schema.Schema{n, c} {
		if r, ok := decl[ledgerKey{m, keyword}]; ok {
			return r, ledgerKey{}
		}
		if r, ok := decl[ledgerKey{m, "*"}]; ok {
			return r, ledgerKey{}
		}
	}
	d := l.inheritedDecline(c)
	return d.reason, d.at
}

// inheritedDecline is the nearest decline above n in the documents: the first
// ancestor, going up, that holds n's branch under a keyword the evaluator
// declined, or that the evaluator declined whole.
//
// Every node on the way up shares the answer of the node it stops at, so the
// answer is recorded for all of them and each is looked up once: a chain n
// deep costs n steps in all, not n per node. A node is recorded before its
// parent is asked about, as no decline, so a holder chain that came back on
// itself would stop there rather than go round.
func (l *ledgerRun) inheritedDecline(n *schema.Schema) ledgerDecline {
	decl := l.g.ledger.declines
	if l.declinedAbove == nil {
		l.declinedAbove = make(map[*schema.Schema]ledgerDecline)
	}
	var chain []*schema.Schema
	var found ledgerDecline
	for m := n; m != nil; {
		if d, ok := l.declinedAbove[m]; ok {
			found = d
			break
		}
		l.declinedAbove[m] = ledgerDecline{}
		chain = append(chain, m)
		p, ok := l.parentOf(m)
		if !ok {
			break
		}
		if r, ok := decl[ledgerKey{p.node, p.keyword}]; ok {
			found = ledgerDecline{at: ledgerKey{p.node, p.keyword}, reason: r}
			break
		}
		if r, ok := decl[ledgerKey{p.node, "*"}]; ok {
			found = ledgerDecline{at: ledgerKey{p.node, p.keyword}, reason: r}
			break
		}
		m = p.node
	}
	for _, m := range chain {
		l.declinedAbove[m] = found
	}
	return found
}

// locationOf is where the document wrote n, as every located diagnostic of
// this package spells it.
func (l *ledgerRun) locationOf(n *schema.Schema) string {
	if loc, ok := l.locator.name(l.canonical(n)); ok {
		return loc
	}
	if loc, ok := l.locator.name(n); ok {
		return loc
	}
	return "(a schema no document wrote)"
}

// assertions memoises Generator.statedAssertions.
func (l *ledgerRun) assertions(n *schema.Schema) []string {
	return l.assertionsOf(l.node(n), n)
}

// assertionsOf is assertions for a node whose record the caller holds.
func (l *ledgerRun) assertionsOf(r *ledgerNode, n *schema.Schema) []string {
	if r.has&hasAssertions == 0 {
		r.has |= hasAssertions
		r.assertions = l.g.statedAssertions(n)
	}
	return r.assertions
}

// markReached marks n and everything below it reached.
//
// Every declaration that carries a node whole marks it, and in a schema that
// describes itself every one of them reaches the whole graph. A node marked
// here has everything below it marked by the same call -- the mark is set on
// the way in, and a call returns only once the walk below it is done -- so a
// later call stops at it: each node is walked once per run, not once per
// declaration.
func (l *ledgerRun) markReached(n *schema.Schema) {
	var walk func(m *schema.Schema)
	walk = func(m *schema.Schema) {
		if m == nil {
			return
		}
		rm := l.node(m)
		if rm.has&hasSubtreeReached != 0 {
			return
		}
		rm.has |= hasSubtreeReached
		rm.reached = true
		l.node(l.canonical(m)).reached = true
		for _, ch := range l.children(m, false) {
			walk(ch.node)
		}
		walk(l.refTarget(m))
		walk(l.dynamicBesideRef(m))
	}
	walk(n)
}

// markReachedShallow marks a node another declaration's walk answers for.
func (l *ledgerRun) markReachedShallow(n *schema.Schema) {
	l.node(n).reached = true
	l.node(l.canonical(n)).reached = true
}

// unevaluatedItemsVacuous reports whether n's unevaluatedItems can never see
// an item: the generator's own evaluated-set reading says every position is
// evaluated by something beside it.
func (l *ledgerRun) unevaluatedItemsVacuous(n *schema.Schema) bool {
	if n.UnevaluatedItems == nil {
		return true
	}
	if n.UnevaluatedItems.IsTrueSchema() {
		return true
	}
	probe := &UnevaluatedItemsDef{}
	l.g.collectEvaluatedItems(n, probe)
	return probe.AllEvaluated
}

// anyBranchAlwaysHolds reports whether some anyOf branch is satisfied by every
// instance the position can hold: every assertion it states is about a kind
// the instance cannot be, or is its type and admits every kind the instance
// can be. The anyOf then asserts nothing, and a declaration that emitted no
// check for it is right not to.
func (l *ledgerRun) anyBranchAlwaysHolds(w *scopeWalk, branches []*schema.Schema, kinds jsonKinds) bool {
	for _, b := range branches {
		if b == nil {
			continue
		}
		if b.IsTrueSchema() {
			return true
		}
		if b.IsBooleanSchema() {
			continue
		}
		holds := true
		for _, k := range l.assertions(b) {
			if k == "type" {
				if kinds&^kindsOfTypeList(b.Type)&^kindNull != 0 {
					holds = false
				}
				continue
			}
			if kk, scoped := keywordKinds[k]; scoped && kinds&kk == 0 {
				continue
			}
			holds = false
			break
		}
		if holds {
			return true
		}
	}
	return false
}

// checkReach holds the run to I2: every node an evaluation of the document
// reaches lies in some declaration's scope. A node none reached is reported
// once, at the top of the unreached subtree, and only if something in that
// subtree asserts anything.
func (l *ledgerRun) checkReach(root *schema.Schema) {
	seen := make(map[*schema.Schema]bool)
	var walk func(n *schema.Schema)
	walk = func(n *schema.Schema) {
		if n == nil || seen[n] {
			return
		}
		seen[n] = true
		if !l.reachedNode(n) && !l.reachedNode(l.canonical(n)) {
			if l.subtreeAsserts(n, make(map[*schema.Schema]bool)) {
				key := ledgerFindingKey{node: l.canonical(n), keyword: "*"}
				if _, ok := l.reported[key]; !ok {
					l.reported[key] = len(l.findings)
					l.findings = append(l.findings, LedgerEntry{
						Location: l.locationOf(n),
						Keyword:  "*",
						Reason:   "no generated type is responsible for this schema, so nothing it states is checked",
					})
				}
			}
			return
		}
		a := l.assertions(n)
		for _, ch := range l.children(n, false) {
			if stringsContain(a, ch.keyword) {
				walk(ch.node)
			}
		}
		if bindsReference(a) {
			walk(l.refTarget(n))
			walk(l.dynamicBesideRef(n))
		}
	}
	walk(root)
}

// stringsContain reports whether list holds s. The lists it is asked of are a
// node's assertions, a handful of keywords, where a scan is cheaper than the
// set it would take to avoid one.
func stringsContain(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// bindsReference reports whether a node's assertions include a reference.
func bindsReference(assertions []string) bool {
	return stringsContain(assertions, "$ref") || stringsContain(assertions, "$dynamicRef") ||
		stringsContain(assertions, "$recursiveRef")
}

// subtreeAsserts reports whether n or anything it binds states an assertion.
func (l *ledgerRun) subtreeAsserts(n *schema.Schema, seen map[*schema.Schema]bool) bool {
	if n == nil || seen[n] {
		return false
	}
	seen[n] = true
	a := l.assertions(n)
	for _, k := range a {
		if !ledgerHierarchical[k] {
			return true
		}
	}
	for _, ch := range l.children(n, false) {
		if stringsContain(a, ch.keyword) && l.subtreeAsserts(ch.node, seen) {
			return true
		}
	}
	if bindsReference(a) {
		return l.subtreeAsserts(l.refTarget(n), seen) || l.subtreeAsserts(l.dynamicBesideRef(n), seen)
	}
	return false
}
