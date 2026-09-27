package generator

import (
	"fmt"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// This file is the one place a Go identifier the generated package declares is
// handed out.
//
// A generated package has one name space at package scope -- every type, every
// enum constant, every package-level variable and helper function, and, in each
// file, every import name -- and one member name space per type, where its
// fields and its methods live together. Names used to be minted per arm: the
// property arm asked whether a name was free, the enum arm and the object arm
// did not, the nullable-oneOf arm read the name straight off the reference
// string, a titled oneOf variant reused whatever type already stood under its
// title, the variant dedupe appended a number without asking whether that number
// was taken, and enum constants, union getters, the derived package variables
// and the import aliases each kept a private idea of what was taken. So two
// schema nodes could end up under one name. Where the second arm declared its
// own type the package did not compile ("declares [AB] twice ... a defect in
// schemagen"); where it did not, the position silently carried the other node's
// type -- validated against a schema written somewhere else, refusing what its
// own schema allows and accepting what it forbids.
//
// So every identifier is claimed here, through claim, under one policy.
//
// The policy:
//
//  1. A name has exactly one holder. A claim never takes a name another holder
//     has, and never gives a holder a name it did not ask for without recording
//     that it did (see NameMove).
//
//  2. Precedence is the order claims are made in, and that order is fixed:
//
//     a. Generator-owned identifiers, held from New(): Go's predeclared
//     identifiers, every identifier the helper file declares, and every
//     import name a generated file may use. A schema cannot move them, because
//     the code that refers to them is fixed text in a template.
//
//     b. The names the caller chose, also held from New(): every
//     Config.DefinitionTypeNames pin. Then, at the start of each Generate, the
//     root type name.
//
//     c. The document's own definitions ($defs, then definitions), before any
//     type is generated -- in the order definitionClaimOrder gives: a key
//     already spelled as the Go name it derives first, then by keyword, then by
//     key. So a definition keeps the name its key spells whatever the document
//     happens to generate first, and a name the generator mints for a position
//     never displaces one.
//
//     d. Everything else, in generation order, which is deterministic: types
//     minted for positions and for nodes a $ref reaches, union wrapper and
//     interface types, enum constants, the package variables a declaration
//     carries, cross-package import aliases, and the placeholder an unresolved
//     reference leaves under --lenient-refs.
//
//  3. A claim whose wanted spelling is held by another holder takes the first of
//     NumberedName(want, 2), NumberedName(want, 3), ... that nothing holds:
//     Want2, Want3, or Want_2, Want_3 where the wanted name already ends in a
//     digit, so the number added cannot be read as part of the one already
//     there (String2 numbered is String2_2, never String22).
//
//  4. The same holder asking again for the same name gets the same answer, so a
//     node reached twice is declared once. Two nodes Config.DefinitionTypeNames
//     pins to one name are one holder (the caller has said they are one type),
//     and a node an allOf or anyOf merge synthesized is the node it was merged
//     from (Generator.holderFor).
//
// A type's name is held from the moment its generation starts -- so a position
// that derives the same spelling while it is in flight is numbered off it --
// and given back if no arm declared anything under it. A position's name is
// only looked up where it is derived (unclaimedTypeName) and claimed where a
// declaration is made (generateTypeDef, declareFor), so the many arms that
// declare nothing hold nothing. A declaration reaching a name held for another
// node is a NamingDefectError, never a quiet reuse of that node's type.
//
// A member name space works the same way, scoped to one type: the members the
// generator adds (Validate, MarshalJSON, the overflow maps, ...) are held first,
// then the fields the schema's properties become (a --field-map override ahead
// of a derived name), then the union getters.
//
// This is a registry claimed in one pass that runs ahead of generation for the
// names fixed before it (a, b and c above) and alongside it for the rest (d). A
// separate pre-walk predicting every name generation would mint was considered
// and not built: which positions get a declaration is decided by a few hundred
// arms of the generator, and predicting it would be a second derivation of those
// decisions -- the very drift this file exists to remove. What it buys is
// obtained instead by the precedence: nothing minted during generation can take
// a name fixed before it, and among the rest, first-come in a deterministic
// order.
//
// Diagnostics are read from here and nowhere else. NameMoves lists the claims
// that did not get the spelling they asked for, with what holds it, and only for
// names that were declared -- so a warning can never describe a type the package
// does not have.

// holderKind is what kind of declaration holds a package-level name.
type holderKind uint8

const (
	// holderReserved: an identifier generated code refers to as fixed text --
	// a helper, an import name, a predeclared identifier.
	holderReserved holderKind = iota + 1
	// holderType: a type declared for a schema node.
	holderType
	// holderMember: a package-level identifier that belongs to a declared type
	// rather than to a schema node of its own -- an enum constant, a union's
	// wrapper and interface types, a package variable a declaration carries.
	holderMember
	// holderImport: the name a cross-package import is spelled under.
	holderImport
	// holderUnresolved: the name an unresolved reference leaves spelled under
	// --lenient-refs, which nothing declares.
	holderUnresolved
)

// nameHolder is who holds a name.
type nameHolder struct {
	kind holderKind
	// node is the schema node a holderType name was claimed for.
	node *schema.Schema
	// key distinguishes holders that have no node: the owning type and the role
	// for holderMember ("Root/const/2"), the import path for holderImport, the
	// reference for holderUnresolved, and what reserves it for holderReserved.
	key string
	// what describes the holder in the terms a diagnostic uses.
	what string
	// role is what the claim is, for NameMove.Role: "root", "definition", or
	// empty for everything else.
	role string
}

// NameMove is one claim that did not get the spelling it asked for.
type NameMove struct {
	// Role is "root" for a document's root type, "definition" for a $defs or
	// definitions entry, and empty for a name the generator minted -- for a
	// position, a reference's target, an enum constant, a union member, an
	// import alias. The first two are names the document chose, which is why a
	// caller may want to say so when one moves.
	Role string
	// Wanted is the name the claim asked for.
	Wanted string
	// Got is the name it was given.
	Got string
	// Claimant describes what asked: "the root type", "$defs/a_b", "a type for
	// a position inside Root", "an enum constant of A", ...
	Claimant string
	// Holder describes what already held Wanted.
	Holder string
}

// nameRegistry holds every package-level identifier of one generated package,
// and the member names of each of its types.
type nameRegistry struct {
	held map[string]nameHolder
	// declared is the set of type names whose declaration has been committed.
	// A type name is held from the moment its generation starts -- so a
	// recursive reach of the same name is recognised as the same holder -- and
	// released again if nothing was declared under it.
	declared map[string]bool
	// pinned maps a node to the name Config.DefinitionTypeNames gives it. Two
	// nodes pinned to one name are one holder: the caller has said they are one
	// type (a definition spelled under both $defs and definitions, or identical
	// definitions of several documents sharing a type).
	pinned map[*schema.Schema]string
	// members holds each type's member names.
	members map[string]*memberScope
	// unresolved maps an unresolved reference to the undeclared name it left
	// spelled; see Generator.unresolvedRefTypeName.
	unresolved map[string]string
	moves      []NameMove
	// pending holds the moves firstAvailable found for spellings not yet
	// claimed; claimExactly records one when its spelling is taken.
	pending map[pendingKey]NameMove
	// byNode lists, per schema node, the type names claimed for it, in the
	// order they were claimed; declaredNameOf reads it.
	byNode map[*schema.Schema][]string
}

func newNameRegistry(pinned map[*schema.Schema]string) *nameRegistry {
	r := &nameRegistry{
		held:       make(map[string]nameHolder),
		declared:   make(map[string]bool),
		pinned:     pinned,
		members:    make(map[string]*memberScope),
		unresolved: make(map[string]string),
		pending:    make(map[pendingKey]NameMove),
		byNode:     make(map[*schema.Schema][]string),
	}
	r.reserveGenerated()
	// The pins, in two passes. First every pin generated code does not already
	// spell is held as given: two nodes pinned to one name are one holder
	// (sameHolder), and pins of different names cannot contest one, so the order
	// is immaterial. Then a pin that lands on a generated identifier -- a caller
	// can pin a definition keyed "SchemagenValidationMode" to that very name --
	// is numbered off it like any other claim, in name order, after every pin
	// that could be kept has been kept.
	var displaced []string
	displacedNodes := make(map[string][]*schema.Schema)
	// maporder: holds each pin that is free as given, and collects the rest;
	// the collected names are sorted before they are claimed.
	for node, name := range pinned {
		h := nameHolder{kind: holderType, node: node, role: "definition", what: "the definition pinned to " + name}
		if held, ok := r.held[name]; ok && held.kind == holderReserved {
			if len(displacedNodes[name]) == 0 {
				displaced = append(displaced, name)
			}
			displacedNodes[name] = append(displacedNodes[name], node)
			continue
		}
		r.claimExactly(name, h)
	}
	sort.Strings(displaced)
	for _, name := range displaced {
		for _, node := range displacedNodes[name] {
			// Every node pinned to one name is one holder, so each gets the
			// numbered spelling the first was given.
			r.claim(name, nameHolder{kind: holderType, node: node, role: "definition", what: "the definition pinned to " + name})
		}
	}
	return r
}

// reserveGenerated holds every identifier generated code spells as fixed text.
func (r *nameRegistry) reserveGenerated() {
	for _, name := range predeclaredIdentifiers {
		r.held[name] = nameHolder{kind: holderReserved, key: name, what: "Go's predeclared " + name}
	}
	for _, name := range helperIdentifiers {
		r.held[name] = nameHolder{kind: holderReserved, key: name, what: "the generated helper " + name}
	}
	for _, imp := range generatedImports {
		r.held[imp.Name] = nameHolder{kind: holderReserved, key: imp.Name, what: "the import name of " + strconv.Quote(imp.Path)}
	}
}

// sameHolder reports whether two holders are one.
func (r *nameRegistry) sameHolder(a, b nameHolder) bool {
	if a.kind != b.kind {
		return false
	}
	if a.kind == holderType {
		if a.node == b.node {
			return true
		}
		pa, aok := r.pinned[a.node]
		pb, bok := r.pinned[b.node]
		return aok && bok && pa == pb
	}
	return a.key == b.key
}

// availableTo reports whether name is free, or already held by h.
func (r *nameRegistry) availableTo(name string, h nameHolder) bool {
	held, ok := r.held[name]
	return !ok || r.sameHolder(held, h)
}

// claim gives h the first spelling of want, in the order NumberedName lists
// them, that nothing but h holds, holds it for h, and returns it.
func (r *nameRegistry) claim(want string, h nameHolder) string {
	got := r.firstAvailable(want, h)
	r.claimExactly(got, h)
	return got
}

// firstAvailable is the spelling claim would give h, without holding it. For a
// name that is held only once something is declared under it. A move, if there
// is one, is kept pending, since it is the want that is known here: it is
// recorded when claimExactly takes the spelling, and NameMoves reports it only
// if the spelling is declared in the end.
func (r *nameRegistry) firstAvailable(want string, h nameHolder) string {
	got := want
	for n := 2; !r.availableTo(got, h); n++ {
		got = NumberedName(want, n)
	}
	if got != want {
		r.pending[pendingKey{name: got, node: h.node, key: h.key}] = NameMove{
			Role: h.role, Wanted: want, Got: got, Claimant: h.what, Holder: r.held[want].what,
		}
	}
	return got
}

// pendingKey identifies a move firstAvailable found and nothing has claimed yet.
type pendingKey struct {
	name string
	node *schema.Schema
	key  string
}

// claimExactly holds name for h if it is available to h, and reports whether
// it did. For identifiers whose spelling is fixed by construction elsewhere --
// where numbering would have to rewrite text that already refers to the name --
// and for a spelling firstAvailable settled on, whose move is recorded now that
// it is taken.
func (r *nameRegistry) claimExactly(name string, h nameHolder) bool {
	if !r.availableTo(name, h) {
		return false
	}
	if _, ok := r.held[name]; !ok {
		r.held[name] = h
		if h.kind == holderType && h.node != nil {
			r.byNode[h.node] = append(r.byNode[h.node], name)
		}
		k := pendingKey{name: name, node: h.node, key: h.key}
		if m, ok := r.pending[k]; ok {
			r.moves = append(r.moves, m)
			delete(r.pending, k)
		}
	}
	return true
}

// pinnedNameOf returns the name a pinned node holds -- its pin, or the
// numbered spelling of it newNameRegistry gave it where generated code already
// spells the pin -- and whether node is pinned at all.
func (r *nameRegistry) pinnedNameOf(node *schema.Schema) (string, bool) {
	pin, ok := r.pinned[node]
	if !ok {
		return "", false
	}
	if names := r.byNode[node]; len(names) > 0 {
		return names[0], true
	}
	// An equivalent node holds it (sameHolder): pinned to the same name.
	if held, ok := r.held[pin]; ok && r.sameHolder(held, nameHolder{kind: holderType, node: node}) {
		return pin, true
	}
	for n := 2; ; n++ {
		name := NumberedName(pin, n)
		held, ok := r.held[name]
		if !ok {
			return pin, true
		}
		if r.sameHolder(held, nameHolder{kind: holderType, node: node}) {
			return name, true
		}
	}
}

// declaredNameOf returns the first name a declaration for node stands under.
func (r *nameRegistry) declaredNameOf(node *schema.Schema) (string, bool) {
	for _, name := range r.byNode[node] {
		held, ok := r.held[name]
		if ok && held.kind == holderType && r.declared[name] && r.sameHolder(held, nameHolder{kind: holderType, node: node}) {
			return name, true
		}
	}
	if name, ok := r.pinned[node]; ok && r.declared[name] {
		return name, true
	}
	return "", false
}

// release gives name back if h holds it and no declaration was committed under
// it: a name asked for and not used is held by nobody.
func (r *nameRegistry) release(name string, h nameHolder) {
	held, ok := r.held[name]
	if !ok || r.declared[name] || !r.sameHolder(held, h) {
		return
	}
	delete(r.held, name)
}

// withdraw gives back a declared type's name and everything it carried: the
// package-level identifiers held in its name (memberHolder) and its member
// scope. For a def taken out of the file after it was declared.
func (r *nameRegistry) withdraw(typeName string) {
	delete(r.declared, typeName)
	delete(r.held, typeName)
	delete(r.members, typeName)
	prefix := typeName + "\x00"
	// maporder: deletes every entry the predicate selects; which is visited
	// first cannot change what is left.
	for name, h := range r.held {
		if h.kind == holderMember && len(h.key) > len(prefix) && h.key[:len(prefix)] == prefix {
			delete(r.held, name)
		}
	}
}

// holderOf returns the holder of name, if any.
func (r *nameRegistry) holderOf(name string) (nameHolder, bool) {
	h, ok := r.held[name]
	return h, ok
}

// declaredMoves returns the recorded moves whose outcome is a name the package
// declares, in the order they were made, each once.
func (r *nameRegistry) declaredMoves(isDeclared func(string) bool) []NameMove {
	seen := make(map[NameMove]bool, len(r.moves))
	var out []NameMove
	for _, m := range r.moves {
		if seen[m] || !isDeclared(m.Got) {
			continue
		}
		if held, ok := r.held[m.Wanted]; !ok || held.kind == holderUnresolved {
			// What held the name let go of it, or never declared it: a
			// diagnostic naming it would describe something the package does
			// not have.
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// NumberedName is the n-th spelling (n >= 2) the collision policy gives a name
// that is already held: want followed by n, or by an underscore and n when want
// already ends in a digit, so that the added number cannot run into the one
// already there. Exported so that every caller that separates names -- the CLI
// resolving several documents' definitions among them -- numbers the way the
// generator does.
func NumberedName(want string, n int) string {
	if r, _ := utf8.DecodeLastRuneInString(want); r >= '0' && r <= '9' {
		return want + "_" + strconv.Itoa(n)
	}
	return want + strconv.Itoa(n)
}

// memberScope holds the member names of one type: its fields and its methods,
// which Go keeps in one name space ("field and method with the same name").
type memberScope struct {
	held map[string]string // name -> what holds it
}

// memberScopeFor returns the member scope of typeName, creating it with the
// members every generated type may carry already held.
func (r *nameRegistry) memberScopeFor(typeName string) *memberScope {
	if m, ok := r.members[typeName]; ok {
		return m
	}
	m := &memberScope{held: make(map[string]string)}
	for _, name := range generatedMemberNames {
		m.held[name] = "the generated member " + name
	}
	r.members[typeName] = m
	return m
}

// claim gives what the first spelling of want, in NumberedName's order, that
// the scope does not hold.
func (m *memberScope) claim(want, what string) string {
	got := want
	for n := 2; ; n++ {
		if holder, ok := m.held[got]; !ok || holder == what {
			break
		}
		got = NumberedName(want, n)
	}
	m.held[got] = what
	return got
}

// holds reports whether the scope holds name for anything but what.
func (m *memberScope) heldByOther(name, what string) (string, bool) {
	holder, ok := m.held[name]
	if !ok || holder == what {
		return "", false
	}
	return holder, true
}

// definitionClaim is one definition of the document being generated, as the
// registry claims it.
type definitionClaim struct {
	keyword string // "$defs" or "definitions"
	key     string
	node    *schema.Schema
	want    string
}

// definitionClaimOrder sorts the definitions of one document into the order
// they claim their names in: a key already spelled as the Go name it derives
// first ($defs/X keeps X ahead of $defs/!, which derives X too), then by
// keyword ($defs ahead of definitions), then by key. It is the order the CLI's
// splitFoldedClaims gives the same claims, so a library caller and the CLI name
// one document alike.
func definitionClaimOrder(claims []definitionClaim) {
	sort.SliceStable(claims, func(i, j int) bool {
		a, b := claims[i], claims[j]
		ra, rb := a.want != a.key, b.want != b.key
		if ra != rb {
			return !ra
		}
		if a.keyword != b.keyword {
			return a.keyword == "$defs"
		}
		return a.key < b.key
	})
}

// typeHolder is the holder a type declared for s claims its name as.
func typeHolder(s *schema.Schema, what string) nameHolder {
	return nameHolder{kind: holderType, node: s, what: what}
}

// memberHolder is the holder of a package-level identifier that belongs to the
// type owner, in the given role.
func memberHolder(owner, role, what string) nameHolder {
	return nameHolder{kind: holderMember, key: owner + "\x00" + role, what: what}
}

// NamingDefectError reports that generation reached a declaration of a name
// the registry holds for something else. No schema can ask for that -- every
// name is claimed before it is declared -- so this is schemagen's defect, and
// it is reported instead of handing back a package whose positions carry
// another node's type.
type NamingDefectError struct {
	Name   string
	Holder string
	Detail string
}

func (e *NamingDefectError) Error() string {
	return fmt.Sprintf("internal naming defect: %s, but %s already holds the name %s; this is a defect in schemagen rather than in the schema -- please report it, with the schema that produced it", e.Detail, e.Holder, e.Name)
}

// DeclaredTypeName reports the name the package declares a type for s under,
// read from the name registry -- which is what the declaration was made under,
// not a prediction of it. False when nothing was declared for s. It spans every
// Generate call that shares this generator's package (see Config.SharedTypes).
func (g *Generator) DeclaredTypeName(s *schema.Schema) (string, bool) {
	if g.names == nil || s == nil {
		return "", false
	}
	return g.names.declaredNameOf(s)
}

// NameMoves lists the names that did not get the spelling they asked for
// because something else held it, in the order they were claimed: a definition
// numbered off a generated helper, a type for a position numbered off a
// definition, an enum constant numbered off a type. Only moves whose outcome the
// package declares are listed, and only where what held the wanted spelling
// still holds it, so every name a report built from this mentions is one the
// generated code has.
func (g *Generator) NameMoves() []NameMove {
	if g.names == nil {
		return nil
	}
	isDeclared := func(name string) bool {
		if g.names.declared[name] {
			return true
		}
		// A package-level identifier that is not a type -- a constant, a
		// variable, a union's wrapper type, an import -- is declared when its
		// holder is; only types go through declare.
		h, ok := g.names.holderOf(name)
		return ok && h.kind != holderType && h.kind != holderUnresolved
	}
	var out []NameMove
	for _, m := range g.names.declaredMoves(isDeclared) {
		if isDeclared(m.Wanted) {
			out = append(out, m)
		}
	}
	return out
}
