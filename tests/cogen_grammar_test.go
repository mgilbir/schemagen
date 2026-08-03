package tests

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
)

// This file holds the grammar half of the co-generation harness: a recursive
// builder that produces a JSON Schema *and* a conforming instance in the same
// pass, plus the mutation catalogue derived from it. The driver that compiles
// and runs the result lives in cogen_test.go.
//
// The single design rule is that the instance is never inferred from the
// schema. One recursive function decides "this node is a string with
// minLength 3", and at that moment it records both the schema fragment and a
// conforming value. Reading an arbitrary schema back and synthesising a
// conforming document would be a second JSON Schema implementation — about as
// hard as the validator under test — and its bugs would surface as false
// failures. Deciding once and emitting both makes the instance correct by
// construction, and it makes the negative cases exact: because the builder
// knows which keyword it just emitted, it knows precisely how to violate that
// one keyword and what the resulting error should say.

// coDialect is the only dialect the grammar emits. Sticking to one draft keeps
// the "conforming by construction" claim checkable by eye; the external suite
// is where draft coverage is exercised.
const coDialect = "https://json-schema.org/draft/2020-12/schema"

// coMaxDepth bounds nesting. Deeper trees mostly re-test the same code paths
// while making every compilation and every shrink step more expensive.
const coMaxDepth = 3

// ---------------------------------------------------------------------------
// Covered subset
// ---------------------------------------------------------------------------
//
// "Generation must not fail" is only meaningful relative to what the grammar
// can produce, so the subset is written down rather than left implicit.
//
// Emitted:
//
//	root            {"type":"object"} with 1..4 properties, or a composition
//	                leaf as the whole document (see below)
//	object          type, properties (1..4), required (any subset)
//	map             an object whose *whole* shape is additionalProperties: no
//	                declared property names, no patternProperties, one value
//	                sub-schema drawn from the same set an array element takes,
//	                and 1..3 keys in the instance. This is the shape schemagen
//	                types as a Go map; the mutation violates the value schema
//	                under the last key.
//	array           type, items (a single schema), minItems, maxItems, or
//	                contains (see "Array keywords" below)
//	string          type, and either {minLength, maxLength} or pattern
//	integer         type, minimum | exclusiveMinimum, maximum |
//	                exclusiveMaximum, multipleOf
//	number          as integer, on a fractional lattice (0.5 / 2.5)
//	boolean         type
//	null            type
//	enum            2..4 members, all strings or all integers
//	const           one string, integer or boolean member
//	$defs + $ref    0..3 definitions; a definition body is an object, a
//	                bounded string/integer/number, an enum, or a composition
//	                leaf (oneOf / if/then/else / not). Definition i may $ref
//	                definitions < i, so the graph is acyclic.
//	allOf           on an object node: its properties, and the `required`
//	                entries that go with them, are partitioned across 1..2
//	                allOf branches instead of being declared inline
//	anyOf           two forms. On an object node, 2..3 branches each keyed by
//	                a discriminator property that is `required` in that
//	                branch and typed there. On a property, two typed scalar
//	                alternatives ({"type":"string","minLength":n} and
//	                {"type":"integer","minimum":m}).
//	oneOf           four forms. On any object node, root or nested, the same
//	                discriminator branches as anyOf. On a property, the same
//	                two typed scalar alternatives as anyOf. As a composition
//	                leaf, two overlapping integer windows, so that a value
//	                matching *both* branches is reachable and can be made a
//	                mutant. Also on a property, two *object* branches, each
//	                requiring a key the other does not mention and constraining
//	                that key's value -- the one form whose branch constraints
//	                sit a level below the branch, and so the one form that can
//	                tell whether the union's owner validates the variant
//	                selection chose.
//	if/then/else    over an integer pivot: `if` {minimum: P}, `then` {maximum:
//	                P+K}, `else` {minimum: P-K}. Both outcomes of `if` are
//	                generated. Emitted as a composition leaf and inline as a
//	                property's own schema.
//	not             {"not":{"type":T}} over a value of some other type. Emitted
//	                as a composition leaf and inline as a property's own schema.
//
// Object keywords that speak about keys rather than about one named property.
// All of them are decided by the builder and never inferred, which for the
// evaluation-sensitive ones means the builder tracks evaluation itself:
//
//	patternProperties   one regexp, ^p_[a-z]+$, with 1..2 keys matching it in
//	                    the instance. The value schema is a bounded string or
//	                    integer, so the mutant violates the branch the key was
//	                    matched into rather than the property's own schema.
//	additionalProperties: false
//	                    only where the object declares every key it carries
//	                    inline: no allOf partition, no discriminator branches
//	                    and no if/then, because a key a *sibling* applicator
//	                    declares is still "additional" to this schema object
//	                    and the conforming instance would be rejected.
//	unevaluatedProperties
//	                    both spellings. false forbids any key no applicator
//	                    evaluated; a schema value constrains those keys, and
//	                    the instance then carries one deliberately unevaluated
//	                    key for the mutation to work on. Unlike
//	                    additionalProperties this composes with everything the
//	                    grammar emits, because a sibling applicator that
//	                    *succeeds* does evaluate what it declares -- so the
//	                    grammar puts it beside allOf partitions, discriminator
//	                    branches, patternProperties and if/then at once.
//	if/then             object-level, over a dedicated `condKey` property whose
//	                    const decides whether `then` applies, and a `thenKey`
//	                    property `then` declares and requires. The builder
//	                    picks which side of `if` the instance is on, so which
//	                    keys are evaluated is a decision rather than a
//	                    deduction. This is the evaluation source that makes
//	                    unevaluatedProperties interesting: flipping condKey
//	                    unevaluates thenKey without touching anything else.
//	dependentRequired   one trigger property, present, and 1..2 dependents that
//	dependentSchemas    are present and *not* `required`, so removing one
//	                    violates the dependency and nothing else.
//	                    A dependentSchemas branch also states a `minimum` on
//	                    depShapeKey, a dedicated integer property the node
//	                    declares inline with no bound of its own. That is the
//	                    branch's shape half, and its mutant -- the value moved
//	                    one below the minimum -- is refused by nothing else in
//	                    the document, so it fails on a dependency that fires on
//	                    presence and never on shape.
//	minProperties       emitted only where no other mutation of the same node
//	maxProperties       deletes or adds a key, so each count mutant is
//	                    unambiguous. The bound is the instance's own key count,
//	                    computed from the same decisions that build it.
//	propertyNames       {"pattern": "^[A-Za-z_][A-Za-z0-9_]*$"}, which every
//	                    name the grammar emits satisfies; the mutant adds one
//	                    that does not. Declared beside the node's allOf or
//	                    inside one of its branches, half each: only the second
//	                    position asks whether a *branch's* propertyNames is read
//	                    at all, since one stated beside the allOf would be
//	                    enforced either way.
//
// Array keywords:
//
//	uniqueItems         on an array whose elements are a plain integer and
//	                    whose i-th element is i, so the array is unique by
//	                    construction at every length the length mutants need.
//	contains +          on the same numbered-integer array, with the sub-schema
//	minContains +       {"type":"integer","minimum":k}: element i matches iff
//	maxContains         i >= k, so the number of matching elements is decided
//	                    rather than read back. minContains and maxContains are
//	                    pinned to that number, which is what makes "one element
//	                    stops matching" and "one more element matches" each a
//	                    violation of exactly one bound. minContains is always
//	                    stated when more than one element matches: under the
//	                    default of 1, dropping one match would leave a
//	                    conforming document and there would be no mutant.
//	                    Exclusive with uniqueItems, minItems and maxItems --
//	                    every one of their mutants would move the match count
//	                    too.
//	prefixItems +       tuple form: 1..3 prefix entries typed string / integer
//	unevaluatedItems    / boolean, in one of three arrangements drawn per node
//	                    (see coTupleMode). Each entry also states a bound its
//	                    conforming value sits exactly on -- minLength 3 under
//	                    "tup", minimum 6 under 6 -- so a position can be
//	                    violated without moving its type, which is what asks
//	                    whether the positional sub-schema is read rather than
//	                    only the length the prefix implies. A boolean position
//	                    states no bound: no keyword narrows a boolean without
//	                    pinning it, so that position is asked about by the
//	                    wrong-type mutant alone.
//
//	                    Where unevaluatedItems is false the mutant appends one
//	                    element, which no prefix entry evaluates. Where the
//	                    prefix sits behind an allOf the same mutant asks whether
//	                    evaluation was seen through the applicator -- the branch
//	                    must match, so what it evaluates is decided and not a
//	                    runtime choice. Where unevaluatedItems is a sub-schema
//	                    the instance already carries one position past the
//	                    prefix, and the mutant rewrites it to a value of a type
//	                    that sub-schema excludes; the length does not move, so
//	                    no length bound can be what rejects it.
//
// A composition leaf sits either as the whole document -- becoming the root
// type -- or as a $defs entry a property $refs, becoming a wrapper type the
// enclosing struct's Validate calls. Both positions are generated.
//
// Where composition sits is not a matter of taste. schemagen enforces the same
// keyword in some positions and not others (see coKnownGaps), and the grammar
// is placed to land on the positions that are enforced: object-level allOf /
// anyOf / oneOf get flattened into the struct and checked against the raw JSON
// keys, an inline anyOf of typed scalars becomes an alternatives wrapper whose
// Validate the parent calls, an inline oneOf of typed scalars becomes a
// sealed-interface union whose selection applies the branch constraints, an
// inline if / not becomes a raw-JSON wrapper whose Validate the parent calls,
// and oneOf / if / not become a wrapper type -- either the root type, whose
// Validate the harness calls directly, or a $defs entry whose Validate the
// referencing struct calls. An inline oneOf whose branches state bounds and no
// type is the one shape the union cannot select on, so it leaves the union path
// and becomes a property type carrying the branches as constraints; that is the
// hoisted spelling of coOneOfWin, and it is emitted inline for exactly that
// reason.
//
// An inline oneOf of *object* branches -- coOneOfObj -- is the same union with
// the branch constraints moved one level down, into the variant types, where
// only the owner's Validate can reach them. It is emitted because the scalar
// spelling above cannot substitute for it: a scalar variant's constraints are
// applied by selection during UnmarshalJSON, so they hold whether or not the
// owner descends, and a union that never descends looks identical from there.
// That is why issue #61 stood while the harness was green.
//
// Deliberately not emitted, because co-generating a *conforming* instance for
// them is its own project and a wrong instance produces false failures that
// burn the whole budget in triage: format, multi-valued type, boolean schemas,
// recursive or remote $refs, $id / $anchor, and `items` as a tuple (the
// pre-2020-12 spelling of prefixItems).
//
// Also not emitted, but for a different reason — see "Not emitted at all"
// below.

// ---------------------------------------------------------------------------
// Known gaps
// ---------------------------------------------------------------------------
//
// This section records constructs the grammar avoids because schemagen is
// already known to handle them incorrectly, so the harness reports regressions
// rather than re-reporting the same defects on every iteration. Each is gated
// by a coGap predicate the grammar consults, reachable again by setting
// SCHEMAGEN_COGEN_INCLUDE_KNOWN_GAPS=1, so the exclusion stays verifiable
// instead of being a claim in a comment. The minimal reproducer for each goes
// in the doc comment of its toggle.
//
// A toggle is a gate the grammar consults, not a label. Where a defect could
// only be reached by a shape the grammar does not build at all, it is written
// down under "Not emitted at all" below rather than given a toggle that would
// promise more than it delivers.
//
// There are none at present. The last of them was
// coGapAdditionalPropertiesSchema -- a schema-valued additionalProperties
// beside declared properties, whose subschema constraints went unchecked --
// and it is gone with the fix for issue #92: coExtraAddlSchema is now emitted
// unconditionally and mutated like any other keyword.

// ---------------------------------------------------------------------------
// Not emitted at all
// ---------------------------------------------------------------------------
//
// This section records defects that no shape the grammar builds could reach,
// which is why none of them carries a coGap toggle: a predicate nothing
// consults would promise a reachability it does not have. They are written down
// because a defect nobody wrote down is a defect that gets rediscovered.
//
// There are none at present. All three that stood here are fixed, and the
// grammar now draws and mutates the shapes that reach them:
//
//   - dependentSchemas branch keywords other than `required`. A branch was
//     reduced to its `required` list, so the dependency fired on presence and
//     never on shape. Gone with issue #93; a branch now carries keywords beside
//     `required`, drawn as coDepShapeKey and mutated like any other.
//   - prefixItems positional subschemas, and unevaluatedItems reached through an
//     applicator or written as a sub-schema. Gone with issues #94 and #95; see
//     coTupleMode and the prefixItemsBound / prefixItemsType /
//     unevaluatedItemsSchema mutants. A tuple written as an array's element or a
//     map's value went with them: it is drawn by buildElem, so the same mutants
//     land there too.

// ---------------------------------------------------------------------------
// Node model
// ---------------------------------------------------------------------------

type coKind int

const (
	coObject coKind = iota
	coArray
	coString
	coInteger
	coNumber
	coBoolean
	coNull
	coEnum
	coConst
	coRef
	// coTuple is an array in tuple form: prefixItems types the positions it
	// has, and unevaluatedItems: false forbids the ones it does not.
	coTuple
	// coMap is an object whose *whole* shape is additionalProperties: it
	// declares no property names and no patternProperties, so one sub-schema
	// governs everything it holds and the generated Go type is a map. The
	// value sub-schema is the same set of leaves an array element draws from,
	// and the mutation violates it under one key.
	coMap

	// Composition leaves. Each is a whole schema whose only keywords are
	// applicators, so each is a value the grammar picks first and a schema it
	// derives from that value.
	coAltAnyOf // anyOf (or, under a known-gap toggle, oneOf) over two typed scalars
	coOneOfWin // oneOf over two overlapping integer windows, typed per branch or once above them
	coIfElse   // if/then/else over an integer pivot
	coNot      // not: {"type": T}
	// coOneOfObj is a oneOf over two *object* branches, each requiring a key
	// the other does not mention and constraining that key's value. It is the
	// only shape in the grammar whose branch constraints live one level below
	// the branch itself, which is what makes it the shape that tests whether
	// the union's owner descends into the variant it selected.
	//
	// Half its instances carry both branches' required keys (objBoth), with the
	// branch that is not taken failing on its nested constraint rather than on
	// its `required`. That is the arrangement in which "exactly one branch"
	// cannot be read off the key set, so it is the one that tests whether
	// selection consults the branch at all rather than only its required keys.
	coOneOfObj
)

// coComp says which applicator, if any, an object node routes its own
// properties through.
type coComp int

const (
	coCompNone coComp = iota
	// coCompAllOf partitions the object's properties across allOf branches.
	// The instance does not change: a branch only ever carries constraints the
	// value already satisfies, which is what makes "conforming by
	// construction" survive the move.
	coCompAllOf
	// coCompAnyOf and coCompOneOf add discriminator branches beside the
	// object's own properties. Each branch requires one property that no other
	// branch mentions, so which branches match is decided by which
	// discriminators are present -- and that is decidable by inspection rather
	// than by evaluating the schema.
	coCompAnyOf
	coCompOneOf
)

// coExtra is the object keyword that governs the keys the object does not
// declare by name. At most one is chosen per node, because every one of them is
// violated by the same edit -- putting a key in the document that the schema
// did not account for -- so two of them at one node would make each other's
// mutant ambiguous.
type coExtra int

const (
	coExtraNone coExtra = iota
	// coExtraAddlFalse is additionalProperties: false. It is only ever chosen
	// for an object that declares every key it carries in its own
	// `properties`: `additionalProperties` reads the `properties` and
	// `patternProperties` of the schema object it sits in and nothing else, so
	// a key declared by an allOf branch, by a discriminator branch or by
	// `then` is additional to *this* object and the conforming instance would
	// be rejected.
	coExtraAddlFalse
	// coExtraAddlSchema is additionalProperties: {integer with a minimum}. The
	// instance then carries coExtraKey, which no `properties` or
	// `patternProperties` claims, so the subschema governs it and there is
	// something for it to be violated on. It shares coExtraAddlFalse's
	// restriction to an object that declares every key it carries, for the same
	// reason: additionalProperties reads this schema object's `properties` and
	// `patternProperties` and nothing else, so a key an allOf branch, a
	// discriminator branch or `then` declares would be judged by the subschema
	// too and the conforming instance would be rejected.
	coExtraAddlSchema
	// coExtraUnevalFalse is unevaluatedProperties: false. Unlike
	// additionalProperties it does see what sibling applicators evaluated, so
	// it composes with every other object shape the grammar emits.
	coExtraUnevalFalse
	// coExtraUnevalSchema is unevaluatedProperties: {integer with a minimum}.
	// The instance then carries coUnevalKey, a key no applicator evaluates, so
	// there is something for the subschema to be violated on.
	coExtraUnevalSchema
	coExtraMaxProps
	coExtraPropNames
)

// coDepKind says which spelling of "this key requires those keys" the object
// uses, or that it uses neither.
type coDepKind int

const (
	coDepNone coDepKind = iota
	coDepRequired
	coDepSchemas
)

// coCond is an object-level if/then over a dedicated property. `if` matches
// when condKey holds coCondYes; `then` declares thenKey and requires it.
//
// It is written this way so the builder decides evaluation rather than deducing
// it: yes says which side of `if` the instance is on, and thenKey is in the
// instance exactly when it is on the `then` side. That makes the pair of facts
// unevaluatedProperties needs -- "thenKey is present" and "thenKey was
// evaluated" -- two records of one decision instead of a judgement about the
// schema.
type coCond struct {
	yes bool  // the instance sets condKey to coCondYes, so `then` applies
	min int64 // then's thenKey minimum
	val int64 // the thenKey value, >= min, present only when yes
}

// coBoundStyle says how a numeric bound is expressed, or that it is absent.
type coBoundStyle int

const (
	coBoundNone coBoundStyle = iota
	coBoundInclusive
	coBoundExclusive
)

// coNode is one node of a co-generated (schema, instance) pair. Every field
// that shapes the schema also shapes the value, which is what keeps the two in
// step: there is no second pass that reads the schema back.
type coNode struct {
	kind coKind

	// object
	props []*coProp

	// object keywords about keys rather than about a named property. Each is
	// governed by an "active" predicate rather than by the field alone, so a
	// shrink step that removes what a keyword depended on removes the keyword
	// from the schema and its mutation from the catalogue together.
	extra     coExtra
	unevalMin int64 // coExtraUnevalSchema: the minimum the subschema carries
	// propNamesBranch declares coExtraPropNames inside an allOf branch rather
	// than beside it. See propNamesInBranch.
	propNamesBranch bool
	patKeys         []string
	patStr          bool // the patternProperties value schema is a string, not an integer
	patBound        int  // its minLength, or its minimum
	dep             coDepKind
	depTrig         string   // the property whose presence triggers the dependency
	depOn           []string // the properties it then requires
	// depShapeMin is the minimum a coDepSchemas branch states on coDepShapeKey.
	// It is the branch's shape half, and a dependency that fires on presence
	// alone never reads it -- which is what the mutant derived from it says.
	depShapeMin int64
	minProps    bool // emit minProperties at the instance's own key count
	cond        *coCond

	// array
	elem     *coNode
	numItems int
	minItems *int
	maxItems *int
	// unique makes the array's elements a plain integer whose i-th value is i.
	// uniqueItems needs distinct elements at every length the length mutants
	// reach, and identical elements are what the array shape otherwise relies
	// on, so the two cannot share an element schema.
	unique bool

	// contains takes the same numbered-integer element shape as unique, and
	// states {"type":"integer","minimum":containsMin}: element i matches exactly
	// when i >= containsMin, so numItems-containsMin elements match and the
	// count is a decision rather than something read back off the instance.
	// minContains and maxContains, when emitted, are pinned to that count --
	// which is what makes changing one element a violation of exactly one bound.
	contains        bool
	containsMin     int
	emitMinContains bool
	emitMaxContains bool

	// coTuple: one JSON type name per prefixItems position, and which of the
	// three arrangements of prefixItems and unevaluatedItems this node is.
	tupleTypes []string
	tupleMode  coTupleMode
	// coTupleUnevalSchema: the JSON type the schema-valued unevaluatedItems
	// states. Positions past the prefix hold a value of that type; the mutant
	// appends one of coTupleUnevalOther, which is of a type it can never be.
	unevalItemType string

	// coMap: the keys the instance carries. The value schema is `elem`, shared
	// with coArray, so the node walk and the clone reach it without a second
	// field to keep in step. Every key holds a value drawn from that one
	// sub-schema, so a mutation under one key violates it there and nowhere
	// else.
	mapKeys []string
	// mapNullable spells the map's type as ["object","null"] rather than
	// "object". That is the nullable form of the same node (issue #91), and it
	// used to take a different route through the generator: no map branch in the
	// nullable arm, so the property came out *map[string]any with the value
	// schema and its keywords dropped. The instance stays a populated object --
	// a null one has no value for the mutation to violate -- so what the
	// nullable spelling adds here is the schema, not a second instance shape.
	mapNullable bool

	// coInteger: write the instance value in float notation ("1.0" rather than
	// "1"). Draft 6 onwards calls that the same integer; the generated code did
	// not, at every position but a document root. See buildValue.
	intFloatToken bool

	// string. lenLo/lenHi always hold the effective window even when the
	// corresponding keyword is not emitted, so value generation has a range to
	// draw from either way. patIdx indexes coPatterns, or -1.
	lenLo, lenHi     int
	emitMin, emitMax bool
	patIdx           int
	strValue         string

	// integer / number. Values live on a lattice of `step`; lo and hi are
	// lattice points, and the emitted bounds are derived from them.
	step           float64
	lo, hi         float64
	minStyle       coBoundStyle
	maxStyle       coBoundStyle
	emitMultipleOf bool
	numValue       float64

	// boolean
	boolValue bool

	// enum / const
	choices  []any
	choiceIx int
	offValue any

	// ref
	refName string

	// object composition. comp says which applicator the object routes through.
	// For coCompAllOf, groups is the number of allOf branches and each property
	// carries the index of the branch it went to (0 meaning "stayed inline").
	// For coCompAnyOf / coCompOneOf, branches are the discriminator branches and
	// branchIx is the one the instance satisfies.
	comp     coComp
	groups   int
	branches []*coDisc
	branchIx int

	// coAltAnyOf: two typed scalar alternatives, a string one carrying
	// minLength and an integer one carrying minimum. altUseStr says which one
	// the instance takes. altOneOf spells the applicator "oneOf" instead of
	// "anyOf". The two alternatives are disjoint by type, so the branch the
	// instance satisfies is the only one either applicator can match, and the
	// same instance conforms under both spellings.
	altStrMin int
	altIntMin int64
	altUseStr bool
	altOneOf  bool
	altStr    string
	altInt    int64

	// coOneOfWin: two integer windows [winLo0,winHi0] and [winLo1,winHi1] with
	// winLo0 < winLo1 <= winHi0 < winHi1, so each of "matches only the first",
	// "matches only the second", "matches both" and "matches neither" is a
	// non-empty set of integers.
	//
	// winHoist writes the same construct with the type stated once beside the
	// oneOf and each branch reduced to its bounds -- {"type":"integer",
	// "oneOf":[{"minimum":..,"maximum":..},{..}]} rather than repeating
	// "type":"integer" inside both branches. The two spellings accept exactly
	// the same integers, so one instance conforms under either.
	winLo0, winHi0 int64
	winLo1, winHi1 int64
	winValue       int64
	winHoist       bool

	// coOneOfObj: two object branches. Branch 0 declares and requires
	// coOneOfObjKeys[0], typed string with minLength objStrMin; branch 1
	// declares and requires coOneOfObjKeys[1], typed integer with minimum
	// objIntMin. The two required keys are different names, so a document
	// carrying one of them satisfies that branch's `required` and fails the
	// other's -- which is what makes "exactly one branch" a fact about which
	// key is present rather than a claim that has to be evaluated.
	//
	// objUseStr says which branch the instance takes; objStr and objInt are the
	// conforming values, chosen at or above their bound.
	//
	// objBoth widens that: the instance carries *both* branches' required keys,
	// the taken branch's key at a conforming value and the other branch's key at
	// a value that branch forbids. "Exactly one branch" is then a fact about the
	// branches' nested constraints rather than about which key is present, which
	// is the only shape that can tell a selection reading the whole branch from
	// one gating on required-key presence alone (issue #81). Both reference
	// implementations were asked: with varStr below its minLength and varInt at
	// or above its minimum, {"varStr":..,"varInt":..} is valid.
	objStrMin int
	objIntMin int64
	objUseStr bool
	objBoth   bool
	objStr    string
	objInt    int64

	// coIfElse: if {minimum: pivot}, then {maximum: pivot+span},
	// else {minimum: pivot-span}. iteIf records which side the instance is on.
	pivot, span int64
	iteIf       bool
	iteValue    int64

	// coNot: the forbidden JSON type, a value that is not of that type, and one
	// that is.
	notType string
	notOK   any
	notBad  any

	// scalarAllOf moves a string node's length keywords into an allOf, so the
	// same bounds are reached through a branch rather than stated directly.
	scalarAllOf bool
}

// coDisc is one discriminator branch of an object-level anyOf or oneOf: a
// property that the branch declares, types, and lists in its own `required`.
// No two branches of a node share a name, so a document that carries exactly
// one of them matches exactly one branch.
type coDisc struct {
	name  string
	jtype string // "string", "integer" or "boolean"
	value any
}

type coProp struct {
	name     string
	required bool
	present  bool
	node     *coNode
	// group is the allOf branch this property was routed into, or 0 when it
	// stayed among the object's own `properties`. Only meaningful when the
	// owning node has comp == coCompAllOf.
	group int
}

// coDoc is a whole co-generated document: the $defs, in dependency order, and
// the root object.
type coDoc struct {
	defOrder []string
	defs     map[string]*coNode
	root     *coNode
}

func (d *coDoc) deref(n *coNode) *coNode {
	for n.kind == coRef {
		n = d.defs[n.refName]
	}
	return n
}

// ---------------------------------------------------------------------------
// Vocabularies
// ---------------------------------------------------------------------------

var coPropNames = []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel"}

var coDefNames = []string{"DefA", "DefB", "DefC"}

// coDiscNames name the discriminator properties of an object-level anyOf or
// oneOf. They are drawn from a vocabulary disjoint from coPropNames so a
// discriminator can never collide with a property the object declares itself,
// which would put two schemas on one name and break the "exactly one branch"
// argument.
var coDiscNames = []string{"tagOne", "tagTwo", "tagThree"}

// coOneOfObjKeys name the properties the two object branches of a coOneOfObj
// declare and require, one each. The whole construct rests on the two names
// being different: the branch the instance does not take is unsatisfied because
// its required key is absent, and a mutant that violates one branch's nested
// constraint cannot accidentally satisfy the other. The names live in their own
// vocabulary rather than borrowing coPropNames so that reading a failing
// document says which construct a key belongs to.
//
// They need not be disjoint from the keys of any enclosing object: these are
// keys of the union's own value, a different JSON object from the one that
// holds it.
var coOneOfObjKeys = [2]string{"varStr", "varInt"}

// The key vocabularies below are pairwise disjoint, and disjoint from
// coPropNames and coDiscNames. That is what lets every argument about them be
// made by name: a key matching coPatternExpr is one patternProperties covers
// and nothing else covers, coUnevalKey is a key no applicator evaluates,
// coExtraKey is a key nothing in the schema mentions at all. Reusing a name
// across two roles would put two schemas on it and every such argument would
// have to be re-made.
const (
	// coPatternExpr matches coPatternKeys and nothing else the grammar emits:
	// no coPropName, coDiscName or synthetic key starts with "p_".
	coPatternExpr = "^p_[a-z]+$"

	// coCondKey drives an object-level if/then, and coThenKey is what `then`
	// declares. coCondKey is declared in the object's own `properties` as well,
	// so that it is evaluated whichever side of `if` the instance is on --
	// otherwise an instance on the `else` side would leave coCondKey
	// unevaluated and fail its own unevaluatedProperties.
	coCondKey = "condKey"
	coThenKey = "thenKey"
	coCondYes = "yes"
	coCondNo  = "no"

	// coUnevalKey is the key a schema-valued unevaluatedProperties is violated
	// on. It is in the instance and nothing evaluates it, which is the point.
	coUnevalKey = "xtraKey"

	// coExtraKey is the key an "extra key" mutant adds. It matches
	// coPropNamePattern, so it violates whichever of additionalProperties,
	// unevaluatedProperties and maxProperties the node carries and never
	// propertyNames.
	coExtraKey = "zzExtra"

	// coDepShapeKey is the key a dependentSchemas branch constrains by *shape*
	// rather than by presence. The node declares it in its own `properties` as a
	// bare integer -- so it is evaluated, permitted and counted like any other
	// declared key -- and the branch states a `minimum` on it. The instance
	// carries a value above that minimum, and the mutant one below it, so the
	// only thing in the document that can refuse the mutant is the branch
	// keyword the dependency fires.
	coDepShapeKey = "depShapeKey"

	// coPropNamePattern accepts every key the grammar emits; coBadPropName is
	// the one propertyNames has to reject, and it is rejected on its first
	// character so no prefix of it could pass.
	coPropNamePattern = "^[A-Za-z_][A-Za-z0-9_]*$"
	coBadPropName     = "9-bad"
)

var coPatternKeys = []string{"p_one", "p_two", "p_three"}

// coTupleMode is which arrangement of prefixItems and unevaluatedItems a tuple
// node carries. All three state the same positions; they differ in where the
// prefix is written and in what the positions past it must satisfy, which is
// what makes each one a different question to ask of the generated code.
type coTupleMode int

const (
	// coTupleUnevalFalse writes prefixItems and unevaluatedItems: false as
	// siblings. This is the arrangement the grammar has always emitted, and the
	// only one whose positions carry a bound beside their type: it is the one
	// the static path handles end to end, so a position's whole sub-schema is
	// reachable there.
	coTupleUnevalFalse coTupleMode = iota
	// coTupleUnevalAllOf puts prefixItems inside an allOf branch, leaving
	// unevaluatedItems: false beside the allOf rather than beside the prefix.
	// The positions it evaluates are the same ones -- an allOf branch must
	// match, so what it evaluates is evaluated, and no runtime choice enters --
	// which is what lets the builder state the evaluated count outright rather
	// than infer it from the instance.
	coTupleUnevalAllOf
	// coTupleUnevalSchema states unevaluatedItems as a sub-schema rather than
	// false, so the positions past the prefix are typed rather than forbidden.
	coTupleUnevalSchema
)

// coTupleTypes pairs a JSON type name with a value of that type, for the
// prefixItems positions of a tuple. As with coNotCases, every half is written
// down rather than derived.
//
// bound is the keyword the position states beside its type, and under a value
// of the position's own JSON type that violates it -- which is what makes the
// positional mutant a violation of the sub-schema rather than of the type. A
// boolean position has no bound: JSON Schema has no keyword that narrows a
// boolean without pinning it to one of its two values, so that position is
// asked about by the wrong-type mutant alone.
//
// other is a value whose JSON type the position can never accept, for the
// mutant that asks whether the position's `type` is read at all.
var coTupleTypes = []struct {
	name  string
	value any
	bound map[string]any
	under any
	other any
}{
	{"string", "tup", map[string]any{"minLength": 3}, "", int64(7)},
	{"integer", int64(6), map[string]any{"minimum": int64(6)}, int64(5), "notAnInt"},
	{"boolean", true, nil, nil, "notABool"},
}

func coTupleEntry(name string) (value, under, other any, bound map[string]any) {
	for _, t := range coTupleTypes {
		if t.name == name {
			return t.value, t.under, t.other, t.bound
		}
	}
	return nil, nil, nil, nil
}

// coTupleUnevalOther is the value a schema-valued unevaluatedItems mutant
// appends: whichever of these the sub-schema's own type is not. Both are drawn
// from types the grammar already emits, so the value is one the pipeline is
// known to be able to carry.
func coTupleUnevalOther(unevalType string) any {
	if unevalType == "string" {
		return int64(7)
	}
	return "notTheType"
}

// coTupleUnevalConforming is a value of the type a schema-valued
// unevaluatedItems states, for the positions past the prefix that the
// conforming instance carries.
func coTupleUnevalConforming(unevalType string) any {
	if unevalType == "string" {
		return "uneval"
	}
	return int64(11)
}

// coDiscTypes pairs the JSON type a discriminator branch declares with a value
// of that type. The values are deliberately away from their Go zero: a
// discriminator that marshalled away would silently unmatch its own branch.
var coDiscTypes = []struct {
	jtype string
	value any
}{
	{"string", "marker"},
	{"integer", int64(41)},
	{"boolean", true},
}

// coNotCases pairs a JSON type to forbid with a value that is not of that type
// and one that is. Both halves are written by hand for the same reason
// coPatterns are: deriving "a value of some other type" from a type name is
// inference, and inference is what this harness refuses to do.
var coNotCases = []struct {
	typeName string
	ok       any
	bad      any
}{
	{"integer", "abc", int64(7)},
	{"string", int64(7), "abc"},
	{"boolean", "abc", true},
	{"object", int64(7), map[string]any{}},
	{"array", "abc", []any{}},
	{"null", int64(7), nil},
}

// coEnumWords includes the empty string for the same reason coEnumInts includes
// zero: it is the Go zero of the named string type an enum or const generates,
// and an optional property holding it is the case a value field with omitempty
// used to lose.
var coEnumWords = []string{"", "red", "green", "blue", "amber", "cyan", "teal"}

// coEnumInts are distinct. Zero is among them on purpose: it is the Go zero of
// the generated named type, so an optional property carrying it is the case
// where omitempty on a value field would drop it from the output and skip the
// enum's own Validate.
var coEnumInts = []int64{0, 3, 5, 7, 11, 13, 17}

const coOffString = "zzz_not_a_member"

const coOffInt int64 = 9998

// coPatterns pairs a regexp with strings that match it and one that does not.
// Both halves are written by hand: deriving a conforming string from a regexp
// is exactly the kind of inference this harness refuses to do.
var coPatterns = []struct {
	expr string
	good []string
	bad  string
}{
	{`^[a-z]+$`, []string{"abc", "zz", "lorem"}, "A1"},
	{`^x[0-9][0-9]$`, []string{"x00", "x42", "x99"}, "x4z"},
	{`^[A-Z][a-z][a-z]$`, []string{"Abc", "Zyx"}, "abc"},
	{`^v[0-9]+\.[0-9]+$`, []string{"v1.0", "v12.34"}, "v1"},
}

// coFillString returns a string of exactly n runes, drawn from an alphabet the
// length keywords are the only thing constraining.
func coFillString(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(alphabet[i%len(alphabet)])
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Builder
// ---------------------------------------------------------------------------

type coBuilder struct {
	rng *rand.Rand
	doc *coDoc
}

// coBuild produces a document from a seed. It is a pure function of the seed:
// the same seed always yields the same schema, the same instance and the same
// mutants, which is what makes a reported failure replayable.
func coBuild(seed uint64) *coDoc {
	b := &coBuilder{
		rng: rand.New(rand.NewPCG(seed, 0x5EEDC0DE)),
		doc: &coDoc{defs: map[string]*coNode{}},
	}
	// One document in four is a tuple at the document root. An array root
	// reaches a different emitter path from the same array behind a property --
	// that is where a missing math import for a prefixItems position typed
	// "integer" went unnoticed -- so the root position is worth generating in
	// its own right.
	if b.chance(4) {
		b.doc.root = b.buildTuple()
		return b.doc
	}
	// One document in four of what remains is rooted at a composition leaf
	// rather than at an object, which exercises `not`, if/then/else and a oneOf
	// over scalars as the root type itself. The other position those keywords
	// reach is a $defs entry behind a property's $ref, which buildDefBody
	// produces.
	//
	// Such a document carries no $defs: the dynamic evaluator only takes over a
	// schema whose keywords are entirely applicators, and it decides that from
	// the marshalled document, so a "$defs" beside the "oneOf" would make the
	// whole construct fall back to an unvalidated `any`.
	if b.chance(4) {
		b.doc.root = b.buildRootComposition()
		return b.doc
	}
	for i := 0; i < b.rng.IntN(len(coDefNames)+1); i++ {
		name := coDefNames[i]
		b.doc.defOrder = append(b.doc.defOrder, name)
		// Definition i may only reference definitions built before it, which
		// is what keeps the reference graph acyclic without a cycle check.
		b.doc.defs[name] = b.buildDefBody(1, i)
	}
	b.doc.root = b.buildObject(0, len(b.doc.defOrder))
	return b.doc
}

func (b *coBuilder) chance(n int) bool { return b.rng.IntN(n) == 0 }

// buildDefBody builds the body of a $defs entry. A primitive body becomes a
// named Go type, and an optional property referencing one is the case where a
// value landing on the Go zero used to vanish from the output; nothing here
// steers away from that value any more.
func (b *coBuilder) buildDefBody(depth, visible int) *coNode {
	// A composition leaf in a $defs entry becomes a wrapper type, and a property
	// referencing it is where that wrapper's Validate has to be reached from --
	// the position at which the constraint used to be dead.
	if b.chance(3) {
		return b.buildRootComposition()
	}
	switch b.rng.IntN(5) {
	case 0, 1:
		return b.buildObject(depth, visible)
	case 2:
		return b.buildString()
	case 3:
		if b.chance(2) {
			return b.buildNumeric(coInteger)
		}
		return b.buildNumeric(coNumber)
	default:
		return b.buildEnum()
	}
}

func (b *coBuilder) buildObject(depth, visible int) *coNode {
	n := &coNode{kind: coObject}
	names := append([]string(nil), coPropNames...)
	b.rng.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
	count := 1 + b.rng.IntN(4)
	for i := 0; i < count; i++ {
		child := b.buildValue(depth+1, visible)
		p := &coProp{name: names[i], node: child, required: b.chance(2)}
		// A nullable property is always required here, and that is a statement
		// about the harness rather than about the schema. Every property whose
		// type admits null has its omitempty suppressed -- dropping a nil would
		// erase an explicit null -- so an *absent* one marshals back as
		// "prop":null, and the round-trip is compared byte for byte. That is a
		// deliberate, pre-existing property of every nullable field in the
		// generator and has nothing to do with the value typing this node is
		// here to exercise, so the grammar keeps the key present rather than
		// reporting it. `required` rather than `present` because the shrinker
		// may clear `present` on an optional property but never on a required
		// one.
		if child.kind == coMap && child.mapNullable {
			p.required = true
		}
		p.present = p.required || !b.chance(4)
		n.props = append(n.props, p)
	}
	b.applyComposition(n, depth)
	b.applyObjectExtras(n)
	return n
}

// applyObjectExtras adds the object keywords that speak about keys rather than
// about one named property. It runs after applyComposition because several of
// them are only sound in the absence of a sibling applicator, and that is a
// decision applyComposition has just made.
func (b *coBuilder) applyObjectExtras(n *coNode) {
	// An object-level if/then. The keys it adds are its own, so it composes
	// with everything except additionalProperties -- which does not look inside
	// `then` and would call what `then` declares an additional property.
	if b.chance(4) {
		c := &coCond{yes: b.chance(2), min: int64(b.rng.IntN(21) - 10)}
		c.val = c.min + int64(b.rng.IntN(5))
		n.cond = c
	}

	// patternProperties. Its keys are matched by the pattern and by nothing
	// else, so they are evaluated for unevaluatedProperties, permitted by
	// additionalProperties, and counted by min/maxProperties like any other
	// key. That makes it the one member of this group that composes freely.
	if b.chance(3) {
		keys := append([]string(nil), coPatternKeys...)
		b.rng.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
		n.patKeys = append(n.patKeys, keys[:1+b.rng.IntN(2)]...)
		n.patStr = b.chance(2)
		if n.patStr {
			// At least 1, so a string one rune shorter exists to be the mutant.
			n.patBound = 1 + b.rng.IntN(4)
		} else {
			n.patBound = b.rng.IntN(16) - 5
		}
	}

	// At most one keyword about keys the object does not name: each is violated
	// by the same edit, so two at one node would make each other's mutant
	// ambiguous.
	switch b.rng.IntN(8) {
	case 0:
		// Only where every key the instance carries is declared by this schema
		// object itself -- see coExtraAddlFalse.
		if n.comp == coCompNone && n.cond == nil {
			n.extra = coExtraAddlFalse
			if b.chance(2) {
				n.extra = coExtraAddlSchema
				n.unevalMin = int64(b.rng.IntN(21) - 10)
			}
		}
	case 1, 2:
		n.extra = coExtraUnevalFalse
	case 3:
		n.extra = coExtraUnevalSchema
		n.unevalMin = int64(b.rng.IntN(21) - 10)
	case 4:
		// A oneOf node's "matched two variants" mutant adds a second
		// discriminator, which would push the key count past the bound too.
		if n.comp != coCompOneOf {
			n.extra = coExtraMaxProps
		}
	case 5:
		n.extra = coExtraPropNames
		// Half of them declare the keyword inside an allOf branch instead of
		// beside it. Only that position can tell whether a branch's
		// propertyNames is read at all -- the parent's has been carried through
		// an allOf since #68, and a keyword the parent also states would be
		// enforced whether or not the branch was ever looked at.
		n.propNamesBranch = b.chance(2)
	}

	if b.chance(3) {
		b.addDependency(n)
	}
	n.minProps = b.chance(4)
}

// addDependency picks a trigger property and the properties its presence
// requires. The dependents are drawn only from properties that are present and
// *not* `required`: a required dependent would make the mutation that removes
// it a `required` violation as well, and the mutant would stop saying anything
// about the dependency.
func (b *coBuilder) addDependency(n *coNode) {
	var trig string
	var deps []string
	for _, p := range n.props {
		if !p.present {
			continue
		}
		if trig == "" {
			trig = p.name
			continue
		}
		if !p.required && len(deps) < 2 {
			deps = append(deps, p.name)
		}
	}
	if trig == "" || len(deps) == 0 {
		return
	}
	n.depTrig, n.depOn = trig, deps
	if b.chance(2) {
		n.dep = coDepRequired
	} else {
		n.dep = coDepSchemas
		// The branch's shape half. dependentRequired has no place to put one:
		// it is a list of names and nothing else, which is exactly what
		// distinguishes the two spellings.
		n.depShapeMin = int64(b.rng.IntN(21) - 10)
	}
}

// buildTuple builds an array in tuple form: prefixItems types the positions it
// has, and unevaluatedItems says what the positions it does not have may be.
//
// One of the three modes is drawn each time, so all three are emitted by
// default. In coTupleUnevalFalse the prefix entries also carry a bound beside
// their type, which is what makes a positional sub-schema -- rather than only
// the length the prefix implies -- something the mutants ask about. The other
// two modes keep their entries type-only: their subject is which positions
// count as evaluated, and a keyword the runtime annotation evaluator does not
// model would take the schema off that path and change what is being tested.
func (b *coBuilder) buildTuple() *coNode {
	n := &coNode{kind: coTuple}
	types := []int{0, 1, 2}
	b.rng.Shuffle(len(types), func(i, j int) { types[i], types[j] = types[j], types[i] })
	for _, ix := range types[:1+b.rng.IntN(3)] {
		n.tupleTypes = append(n.tupleTypes, coTupleTypes[ix].name)
	}
	switch b.rng.IntN(3) {
	case 1:
		n.tupleMode = coTupleUnevalAllOf
	case 2:
		n.tupleMode = coTupleUnevalSchema
		if b.chance(2) {
			n.unevalItemType = "string"
		} else {
			n.unevalItemType = "integer"
		}
	default:
		n.tupleMode = coTupleUnevalFalse
	}
	return n
}

func coTupleValue(name string) any {
	v, _, _, _ := coTupleEntry(name)
	return v
}

// tupleTailCount is how many positions past the prefix the conforming instance
// carries. Only a schema-valued unevaluatedItems permits any: the other two
// modes forbid every position the prefix does not name.
func (n *coNode) tupleTailCount() int {
	if n.tupleMode == coTupleUnevalSchema {
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// Which object keywords are live
// ---------------------------------------------------------------------------
//
// Each keyword below is emitted only while the thing it was built against is
// still there. The predicate is consulted by fragment() and by collect() alike,
// so a shrink step that drops the property a dependency named, or the branch a
// count was reckoned against, removes the keyword from the schema and its
// mutation from the catalogue in one move -- rather than leaving a schema the
// instance no longer satisfies.

func (n *coNode) presentProp(name string) *coProp {
	for _, p := range n.props {
		if p.name == name && p.present {
			return p
		}
	}
	return nil
}

// depActive reports whether the dependency still names present, optional
// properties. A dependent that has become required, or absent, would make the
// mutation that removes it mean something else.
func (n *coNode) depActive() bool {
	if n.dep == coDepNone || len(n.depOn) == 0 || n.presentProp(n.depTrig) == nil {
		return false
	}
	for _, name := range n.depOn {
		p := n.presentProp(name)
		if p == nil || p.required {
			return false
		}
	}
	return true
}

// depShapeActive reports whether the dependency is the dependentSchemas
// spelling and still fires, which is what puts coDepShapeKey in the instance
// and the branch's `minimum` in the schema. dependentRequired has no branch to
// carry a shape constraint in.
func (n *coNode) depShapeActive() bool {
	return n.dep == coDepSchemas && n.depActive()
}

// addlFalseActive holds only while every key the instance carries is declared
// by this schema object: additionalProperties does not read allOf branches,
// discriminator branches or `then`.
func (n *coNode) addlFalseActive() bool {
	return n.extra == coExtraAddlFalse && n.declaresEveryKey()
}

// addlSchemaActive is the same test for the schema-valued spelling, which needs
// it for the same reason: a key this schema object does not declare is governed
// by the subschema, and one an allOf or discriminator branch declares would be
// judged by it too.
func (n *coNode) addlSchemaActive() bool {
	return n.extra == coExtraAddlSchema && n.declaresEveryKey()
}

func (n *coNode) declaresEveryKey() bool {
	return n.comp == coCompNone && len(n.branches) == 0 && n.cond == nil
}

// maxPropsActive drops the bound where the node also carries a mutation that
// adds a key for a different reason.
func (n *coNode) maxPropsActive() bool {
	return n.extra == coExtraMaxProps &&
		!(n.comp == coCompOneOf && len(n.branches) > 0)
}

// propNamesActive holds whenever the node chose the keyword: propertyNames
// constrains every key the instance carries, wherever the property was
// declared.
func (n *coNode) propNamesActive() bool {
	return n.extra == coExtraPropNames
}

// propNamesInBranch reports whether the node's propertyNames is declared inside
// one of its allOf branches rather than beside them. Both positions bind the
// same object -- allOf branches all apply to the instance the parent describes
// -- so the instance and the mutant are unchanged by the move; what changes is
// that the constraint is now only reachable by reading the branch.
//
// It needs a branch to sit in, and the shrinker can empty the last one by
// dropping its properties, so this is a predicate over the props rather than a
// flag on its own.
func (n *coNode) propNamesInBranch() bool {
	if !n.propNamesBranch || n.comp != coCompAllOf || !n.propNamesActive() {
		return false
	}
	for _, p := range n.props {
		if p.group > 0 {
			return true
		}
	}
	return false
}

// minPropsActive holds only where nothing else at this node removes a key.
// Every other delete mutation here -- a required property, a discriminator, the
// property a dependency requires -- would drop the count below the bound as
// well, and the mutant would be rejected for the wrong keyword.
func (n *coNode) minPropsActive() bool {
	if !n.minProps || len(n.branches) > 0 || n.cond != nil || n.depActive() {
		return false
	}
	droppable := false
	for _, p := range n.props {
		if !p.present {
			continue
		}
		if p.required {
			return false
		}
		droppable = true
	}
	return droppable
}

// instanceKeyCount is the number of keys value() will put in this object. It is
// the same walk value() makes, which is what lets minProperties and
// maxProperties be stated as the instance's own count rather than as a number
// that has to be kept in step with it by hand.
func (n *coNode) instanceKeyCount() int {
	count := 0
	for _, p := range n.props {
		if p.present {
			count++
		}
	}
	if len(n.branches) > 0 {
		count++
	}
	count += len(n.patKeys)
	if n.cond != nil {
		count++ // condKey
		if n.cond.yes {
			count++ // thenKey
		}
	}
	if n.extra == coExtraUnevalSchema {
		count++ // coUnevalKey
	}
	if n.addlSchemaActive() {
		count++ // coExtraKey
	}
	if n.depShapeActive() {
		count++ // coDepShapeKey
	}
	return count
}

// patValue is a value the patternProperties subschema accepts, patBadValue one
// it rejects, and they differ in the one keyword that subschema carries.
func (n *coNode) patValue() any {
	if n.patStr {
		return coFillString(n.patBound + 1)
	}
	return int64(n.patBound + 2)
}

func (n *coNode) patBadValue() any {
	if n.patStr {
		return coFillString(n.patBound - 1)
	}
	return int64(n.patBound - 1)
}

// unevalValue and unevalBadValue straddle the minimum a schema-valued
// unevaluatedProperties carries.
func (n *coNode) unevalValue() int64    { return n.unevalMin + 3 }
func (n *coNode) unevalBadValue() int64 { return n.unevalMin - 1 }

// depShapeValue and depShapeBadValue straddle the minimum a dependentSchemas
// branch puts on coDepShapeKey. The key's own declaration is a bare integer, so
// both are values it accepts and only the branch separates them.
func (n *coNode) depShapeValue() int64    { return n.depShapeMin + 3 }
func (n *coNode) depShapeBadValue() int64 { return n.depShapeMin - 1 }

// applyComposition decides whether an object routes its properties through an
// allOf, or grows a discriminated anyOf / oneOf beside them.
//
// The instance is not touched by the allOf case at all: moving a property's
// declaration into a branch does not change which values satisfy the document,
// because allOf is a conjunction and the partition is a partition. The anyOf
// and oneOf cases add exactly one property to the instance -- the discriminator
// of the branch the document is meant to match.
func (b *coBuilder) applyComposition(n *coNode, depth int) {
	switch b.rng.IntN(6) {
	case 0, 1:
		// Partition the properties across 1..2 allOf branches. The first
		// branches are seeded round-robin so none of them comes out empty: an
		// empty branch is `{}`, which constrains nothing and would make the
		// applicator invisible.
		n.comp = coCompAllOf
		n.groups = 1 + b.rng.IntN(2)
		if n.groups > len(n.props) {
			n.groups = len(n.props)
		}
		for i, p := range n.props {
			if i < n.groups {
				p.group = i + 1
				continue
			}
			p.group = b.rng.IntN(n.groups + 1)
		}
	case 2:
		n.comp = coCompAnyOf
		b.addDiscriminators(n)
	case 3:
		n.comp = coCompOneOf
		b.addDiscriminators(n)
	default:
		n.comp = coCompNone
	}
}

// addDiscriminators gives an object 2..3 branches, each keyed on a property
// name no other branch mentions, and records which one the instance satisfies.
func (b *coBuilder) addDiscriminators(n *coNode) {
	count := 2 + b.rng.IntN(2)
	types := append([]int(nil), []int{0, 1, 2}...)
	b.rng.Shuffle(len(types), func(i, j int) { types[i], types[j] = types[j], types[i] })
	for i := 0; i < count; i++ {
		t := coDiscTypes[types[i]]
		n.branches = append(n.branches, &coDisc{name: coDiscNames[i], jtype: t.jtype, value: t.value})
	}
	n.branchIx = b.rng.IntN(count)
}

// buildRootComposition builds a composition leaf: a schema whose only keywords
// are applicators, and whose instance is a single scalar.
func (b *coBuilder) buildRootComposition() *coNode {
	switch b.rng.IntN(3) {
	case 0:
		return b.buildOneOfWin(b.chance(2))
	case 1:
		return b.buildIfElse()
	default:
		return b.buildNot()
	}
}

// buildOneOfWin lays two integer windows out so that they overlap without
// either containing the other:
//
//	B0 = [lo0, hi0]   B1 = [lo1, hi1]   lo0 < lo1 <= hi0 < hi1
//
// [lo0, lo1-1] then matches B0 alone, [hi0+1, hi1] matches B1 alone, [lo1, hi0]
// matches both -- which is what makes a "matched two variants" mutant reachable
// at all -- and anything below lo0 matches neither.
//
// hoist picks the spelling: see winHoist. Both windows describe integers either
// way, so the instance and every mutant are the same numbers under both.
func (b *coBuilder) buildOneOfWin(hoist bool) *coNode {
	n := &coNode{kind: coOneOfWin, winHoist: hoist}
	n.winLo0 = int64(b.rng.IntN(21) - 10)
	only0 := int64(1 + b.rng.IntN(3)) // width of the "B0 alone" band
	n.winLo1 = n.winLo0 + only0
	n.winHi0 = n.winLo1 + int64(b.rng.IntN(3))
	only1 := int64(1 + b.rng.IntN(3)) // width of the "B1 alone" band
	n.winHi1 = n.winHi0 + only1
	if b.chance(2) {
		n.winValue = n.winLo0 + int64(b.rng.IntN(int(only0)))
	} else {
		n.winValue = n.winHi0 + 1 + int64(b.rng.IntN(int(only1)))
	}
	return n
}

// buildIfElse pins the whole construct to one integer pivot, so that which side
// of `if` the instance falls on is a decision the builder makes rather than one
// anything has to work out from the schema.
func (b *coBuilder) buildIfElse() *coNode {
	n := &coNode{kind: coIfElse}
	n.pivot = int64(b.rng.IntN(21) - 10)
	n.span = int64(1 + b.rng.IntN(4))
	n.iteIf = b.chance(2)
	if n.iteIf {
		n.iteValue = n.pivot + int64(b.rng.IntN(int(n.span)+1))
	} else {
		n.iteValue = n.pivot - int64(1+b.rng.IntN(int(n.span)))
	}
	return n
}

func (b *coBuilder) buildNot() *coNode {
	c := coNotCases[b.rng.IntN(len(coNotCases))]
	return &coNode{kind: coNot, notType: c.typeName, notOK: c.ok, notBad: c.bad}
}

// buildAltAnyOf builds two typed scalar alternatives. minLength is at least 1
// so that a string one rune shorter than the bound exists, which is the mutant
// that violates the string branch by its constraint and the integer branch by
// its type.
func (b *coBuilder) buildAltAnyOf(oneOf bool) *coNode {
	n := &coNode{kind: coAltAnyOf, altOneOf: oneOf}
	n.altStrMin = 1 + b.rng.IntN(4)
	n.altIntMin = int64(b.rng.IntN(21) - 10)
	n.altUseStr = b.chance(2)
	if n.altUseStr {
		n.altStr = coFillString(n.altStrMin + b.rng.IntN(3))
	} else {
		n.altInt = n.altIntMin + int64(b.rng.IntN(5))
	}
	return n
}

// buildOneOfObj builds two object branches keyed by different required
// properties, each constraining its own property's value.
//
// minLength is at least 1 so a string one rune shorter than the bound exists;
// that string, under the branch's key, is a document that satisfies the
// branch's `required` and violates its nested constraint -- the mutant no
// scalar-branch shape in this grammar can express, because a scalar branch has
// no interior to violate.
//
// That same string is what objBoth puts beside a conforming varInt, and the
// integer one below objIntMin is what it puts beside a conforming varStr: a
// document carrying both required keys where only one branch is satisfied. Both
// are conforming instances, and the mutants below turn each into an invalid one
// by moving the violating key to a value its branch accepts (two branches
// matched) or the conforming key to one its branch rejects (none matched).
func (b *coBuilder) buildOneOfObj() *coNode {
	n := &coNode{kind: coOneOfObj}
	n.objStrMin = 1 + b.rng.IntN(4)
	n.objIntMin = int64(b.rng.IntN(21) - 10)
	n.objUseStr = b.chance(2)
	n.objBoth = b.chance(2)
	if n.objUseStr {
		n.objStr = coFillString(n.objStrMin + b.rng.IntN(3))
		if n.objBoth {
			n.objInt = n.objIntMin - 1
		}
	} else {
		n.objInt = n.objIntMin + int64(b.rng.IntN(5))
		if n.objBoth {
			n.objStr = coFillString(n.objStrMin - 1)
		}
	}
	return n
}

// buildValue picks the schema for an object property.
func (b *coBuilder) buildValue(depth, visible int) *coNode {
	kinds := []coKind{coString, coInteger, coNumber, coBoolean, coNull, coEnum, coConst, coAltAnyOf}
	if depth < coMaxDepth {
		kinds = append(kinds, coObject, coArray, coTuple, coMap)
	}
	if visible > 0 {
		kinds = append(kinds, coRef)
	}
	// Composition leaves. Written inline as a property's own schema these have
	// no Go type of their own, so each becomes a raw-JSON wrapper whose Validate
	// the enclosing struct calls.
	//
	// coOneOfWin is admitted here only in its hoisted spelling (see winHoist).
	// That is the one branch shape a sealed-interface union cannot select on --
	// a branch stating bounds and no type resolves to `any`, and an `any`
	// variant carries no checks -- so it is the position where the property has
	// to leave the union path and pick up the branches as constraints on the
	// declared type. The unhoisted spelling inline is a union of two same-typed
	// variants, a different construct that coAltAnyOf's oneOf form already
	// covers at this position.
	//
	// coOneOfObj belongs here and nowhere else. It is a union whose variants
	// are named object types, so the branch constraints sit inside those types
	// rather than on the union, and only the owner's Validate can reach them --
	// which is exactly the position where a missing descent goes unnoticed. The
	// scalar-branch spellings above cannot stand in for it: their constraints
	// are applied by variant selection during UnmarshalJSON, so they are
	// enforced whether or not anything descends.
	kinds = append(kinds, coIfElse, coNot, coOneOfWin, coOneOfObj)
	switch kinds[b.rng.IntN(len(kinds))] {
	case coOneOfObj:
		return b.buildOneOfObj()
	case coObject:
		return b.buildObject(depth, visible)
	case coArray:
		return b.buildArray(depth, visible)
	case coMap:
		return b.buildMap(depth, visible)
	case coTuple:
		return b.buildTuple()
	case coAltAnyOf:
		return b.buildAltAnyOf(b.chance(2))
	case coOneOfWin:
		return b.buildOneOfWin(true)
	case coIfElse:
		return b.buildIfElse()
	case coNot:
		return b.buildNot()
	case coString:
		return b.buildString()
	case coInteger:
		n := b.buildNumeric(coInteger)
		// Half the integer properties write their value in float notation. From
		// draft 6 on -- and coDialect is 2020-12 -- a number with a zero
		// fractional part is an integer, so {"n":1.0} conforms to
		// {"type":"integer"} and both reference implementations say so. It did
		// not conform to the *generated* code: a struct field was an int64
		// handed to encoding/json, which refuses the notation, while a document
		// root typed integer accepted it (issue #90).
		//
		// Confined to a property. An array element or a map value would put two
		// spellings of one number in a single container, and uniqueItems
		// compares elements after decoding, so a pair JSON Schema calls equal
		// could be read as distinct -- a soundness question about the harness
		// rather than about the position, and one the behavioural regressions
		// cover instead.
		n.intFloatToken = b.chance(2)
		return n
	case coNumber:
		return b.buildNumeric(coNumber)
	case coBoolean:
		return &coNode{kind: coBoolean, boolValue: b.chance(2)}
	case coNull:
		return &coNode{kind: coNull}
	case coEnum:
		return b.buildEnum()
	case coConst:
		return b.buildConst()
	default:
		return &coNode{kind: coRef, refName: b.doc.defOrder[b.rng.IntN(visible)]}
	}
}

// buildElem picks the schema for an array element. It draws from the same kinds
// an object property does, minus null (whose gaps are recorded above, and which
// has no property name of its own to report against here) and minus array,
// since an array of arrays only re-tests the dimension buildArray already
// covers at a multiple of the compile cost.
func (b *coBuilder) buildElem(depth, visible int) *coNode {
	kinds := []coKind{coString, coInteger, coNumber, coBoolean, coEnum, coConst}
	if depth < coMaxDepth {
		// A tuple written as an array's element, or a map's value, is a
		// position of its own: the Go type there is a []any inside a [][]any or
		// a map[string][]any, so it is neither a field the struct-level checks
		// can name nor a type with a Validate to dispatch to. Its positions were
		// checked nowhere until issue #94 was fixed, and only the length its
		// prefix implies was, so the shape is emitted here rather than left to
		// the property position alone.
		kinds = append(kinds, coObject, coTuple)
	}
	if visible > 0 {
		kinds = append(kinds, coRef)
	}
	switch kinds[b.rng.IntN(len(kinds))] {
	case coTuple:
		return b.buildTuple()
	case coObject:
		return b.buildObject(depth, visible)
	case coEnum:
		return b.buildEnum()
	case coConst:
		return b.buildConst()
	case coRef:
		return &coNode{kind: coRef, refName: b.doc.defOrder[b.rng.IntN(visible)]}
	case coString:
		return b.buildString()
	case coInteger:
		return b.buildNumeric(coInteger)
	case coNumber:
		return b.buildNumeric(coNumber)
	default:
		return &coNode{kind: coBoolean, boolValue: b.chance(2)}
	}
}

func (b *coBuilder) buildArray(depth, visible int) *coNode {
	n := &coNode{kind: coArray, elem: b.buildElem(depth+1, visible)}
	lo := b.rng.IntN(3)
	hi := lo + b.rng.IntN(4)
	n.numItems = lo + b.rng.IntN(hi-lo+1)
	if b.chance(2) {
		v := lo
		n.minItems = &v
	}
	if b.chance(2) {
		v := hi
		n.maxItems = &v
	}
	// uniqueItems needs elements that differ at every length the length mutants
	// reach, and the shared element shape the rest of the array machinery rests
	// on produces identical ones. So a unique array takes an unconstrained
	// integer element instead, and arrayValue numbers the positions: the
	// minItems and maxItems mutants stay unique, and the only way to make a
	// duplicate is the mutation that means to.
	//
	// contains needs the same numbered shape, for a different reason -- it
	// counts, so its elements have to be tellable apart. The two are
	// alternatives rather than options that compose: every contains mutant
	// writes a value the array already holds, which uniqueItems would reject
	// for a second reason and so leave the contains bound unproven.
	switch b.rng.IntN(3) {
	case 0:
		n.unique = true
		n.elem = &coNode{kind: coInteger}
	case 1:
		// minItems and maxItems come off for the same reason uniqueItems is
		// excluded: their mutants change the array's length, and with numbered
		// elements a shorter array holds fewer matches and a longer one more,
		// so each would violate a contains bound as well as its own.
		n.contains = true
		n.elem = &coNode{kind: coInteger}
		n.minItems, n.maxItems = nil, nil
		n.numItems = 2 + b.rng.IntN(3)
		// At least one match, and at least one element below the threshold for
		// the maxContains mutant to raise.
		matches := 1 + b.rng.IntN(n.numItems-1)
		n.containsMin = n.numItems - matches
		// minContains has to be stated whenever more than one element matches:
		// under the default of 1, dropping a single match still leaves one, and
		// the mutant would be a conforming document. Where exactly one matches
		// the default says the same thing, so both spellings are generated.
		n.emitMinContains = matches > 1 || b.chance(2)
		n.emitMaxContains = b.chance(2)
	}
	return n
}

// containsMatches is how many elements satisfy the contains sub-schema: the
// positions numbered at or above its minimum. It is derived from numItems
// rather than stored, so a shrink step that shortens the array moves the
// emitted minContains and maxContains with it.
func (n *coNode) containsMatches() int { return n.numItems - n.containsMin }

// buildMap builds an object whose whole shape is additionalProperties: no
// declared property names, no patternProperties, one sub-schema for every value.
// That is the shape schemagen types as a Go map, and the position where the
// value sub-schema used to be thrown away (issue #84): the field came out
// map[string]any and the keywords under additionalProperties were enforced
// nowhere.
//
// The value schema is drawn from buildElem, the same set an array element takes.
// That is deliberate rather than convenient: an array element and a map value
// reach their checks through one mechanism, so the same leaves are the ones
// whose enforcement is already established at the sibling position, and a
// divergence between the two shows up as a map failure on a leaf arrays pass.
//
// At least one key, so there is always a value for the mutation to work on.
func (b *coBuilder) buildMap(depth, visible int) *coNode {
	n := &coNode{kind: coMap, elem: b.buildElem(depth+1, visible)}
	for i := 0; i < 1+b.rng.IntN(3); i++ {
		n.mapKeys = append(n.mapKeys, fmt.Sprintf("k%d", i))
	}
	n.mapNullable = b.chance(2)
	return n
}

// buildString chooses either a length window or a pattern, never both: a
// mutant has to violate exactly one keyword, and shortening a string to break
// minLength would usually break the pattern at the same time.
func (b *coBuilder) buildString() *coNode {
	n := &coNode{kind: coString, patIdx: -1}
	if b.chance(3) {
		n.patIdx = b.rng.IntN(len(coPatterns))
		p := coPatterns[n.patIdx]
		n.strValue = p.good[b.rng.IntN(len(p.good))]
		return n
	}
	n.lenLo = b.rng.IntN(5)
	n.lenHi = n.lenLo + b.rng.IntN(7)
	n.emitMin = n.lenLo > 0 || b.chance(2)
	n.emitMax = b.chance(2)
	if !n.emitMin && !n.emitMax {
		n.emitMax = true
	}
	n.scalarAllOf = b.chance(2)
	n.strValue = coFillString(n.lenLo + b.rng.IntN(n.lenHi-n.lenLo+1))
	return n
}

// buildNumeric lays the node out on a lattice so that every mutant violates
// exactly one keyword: the minimum mutant is a lattice point one step below
// the bound (so multipleOf still holds), and the multipleOf mutant sits inside
// the bounds but off the lattice.
func (b *coBuilder) buildNumeric(kind coKind) *coNode {
	n := &coNode{kind: kind}
	if kind == coInteger {
		n.step = []float64{1, 1, 2, 3, 5}[b.rng.IntN(5)]
	} else {
		n.step = []float64{0.5, 1, 1, 2.5}[b.rng.IntN(4)]
	}
	// The span is at least four steps so that both bounds can be exclusive and
	// still leave lattice points, and so the off-lattice multipleOf mutant fits.
	span := float64(4+b.rng.IntN(9)) * n.step
	// The window straddles zero, so both the instance value and the mutants may
	// land on it. That is deliberate: zero is the Go zero of a named numeric
	// type and the value an optional property used to lose.
	n.lo = float64(b.rng.IntN(41)-20) * n.step
	n.hi = n.lo + span

	n.minStyle = []coBoundStyle{coBoundNone, coBoundInclusive, coBoundInclusive, coBoundExclusive}[b.rng.IntN(4)]
	n.maxStyle = []coBoundStyle{coBoundNone, coBoundInclusive, coBoundInclusive, coBoundExclusive}[b.rng.IntN(4)]
	n.emitMultipleOf = n.step != 1 && !b.chance(4)

	lo, hi := n.allowedRange()
	steps := int((hi-lo)/n.step + 0.5)
	n.numValue = lo + float64(b.rng.IntN(steps+1))*n.step
	return n
}

// allowedRange returns the closed range of lattice points the instance value
// may take, narrowed by one step on a side whose bound is exclusive.
func (n *coNode) allowedRange() (float64, float64) {
	lo, hi := n.lo, n.hi
	if n.minStyle == coBoundExclusive {
		lo += n.step
	}
	if n.maxStyle == coBoundExclusive {
		hi -= n.step
	}
	return lo, hi
}

// offLatticeOffset is the amount added to the low end of the allowed range to
// build a multipleOf mutant. For integers it must itself be an integer, or the
// mutant would violate "type" as well.
func (n *coNode) offLatticeOffset() float64 {
	if n.kind == coInteger {
		return 1
	}
	return n.step / 2
}

func (b *coBuilder) buildEnum() *coNode {
	n := &coNode{kind: coEnum}
	count := 2 + b.rng.IntN(3)
	if b.chance(2) {
		words := append([]string(nil), coEnumWords...)
		b.rng.Shuffle(len(words), func(i, j int) { words[i], words[j] = words[j], words[i] })
		for i := 0; i < count; i++ {
			n.choices = append(n.choices, words[i])
		}
		n.offValue = coOffString
	} else {
		ints := append([]int64(nil), coEnumInts...)
		b.rng.Shuffle(len(ints), func(i, j int) { ints[i], ints[j] = ints[j], ints[i] })
		for i := 0; i < count; i++ {
			n.choices = append(n.choices, ints[i])
		}
		n.offValue = coOffInt
	}
	n.choiceIx = b.rng.IntN(count)
	return n
}

// buildConst may pick a boolean. A const of true emits a named bool type whose
// only non-conforming value is false, and a const of false the mirror image, so
// either way the mutant is the Go zero or the value is — both of which an
// optional property has to carry through the round trip and past Validate.
func (b *coBuilder) buildConst() *coNode {
	n := &coNode{kind: coConst}
	switch b.rng.IntN(5) {
	case 0, 1:
		n.choices = []any{coEnumWords[b.rng.IntN(len(coEnumWords))]}
		n.offValue = coOffString
	case 2, 3:
		n.choices = []any{coEnumInts[b.rng.IntN(len(coEnumInts))]}
		n.offValue = coOffInt
	default:
		v := b.chance(2)
		n.choices = []any{v}
		n.offValue = !v
	}
	return n
}

// ---------------------------------------------------------------------------
// Schema emission
// ---------------------------------------------------------------------------

func (d *coDoc) schema() map[string]any {
	s := d.root.fragment()
	s["$schema"] = coDialect
	if len(d.defOrder) > 0 {
		defs := map[string]any{}
		for _, name := range d.defOrder {
			defs[name] = d.defs[name].fragment()
		}
		s["$defs"] = defs
	}
	return s
}

func (n *coNode) fragment() map[string]any {
	m := map[string]any{}
	switch n.kind {
	case coObject:
		m["type"] = "object"
		// Properties routed into an allOf branch are declared there and only
		// there, so the branch is the sole source of both the property's schema
		// and its `required` entry. Anything else would leave the constraint
		// enforceable without ever reading the branch, and the mutant would
		// stop saying anything about allOf.
		groups := make([][]*coProp, n.groups+1)
		for _, p := range n.props {
			g := 0
			if n.comp == coCompAllOf {
				g = p.group
			}
			groups[g] = append(groups[g], p)
		}
		props, req := coDeclare(groups[0])
		if n.cond != nil {
			// condKey is declared here as well as inside `if`, so that it is
			// evaluated whichever side of `if` the instance took. Left to `if`
			// alone it would be evaluated only when `if` succeeded, and an
			// instance on the other side would fail its own
			// unevaluatedProperties on the very key that put it there.
			props[coCondKey] = map[string]any{"type": "string"}
		}
		if n.depShapeActive() {
			// Declared inline, and with no bound of its own: the branch's
			// `minimum` is then the only thing in the document that can refuse
			// the key, and declaring it here keeps it evaluated, permitted by
			// additionalProperties: false, and accepted by propertyNames.
			props[coDepShapeKey] = map[string]any{"type": "integer"}
		}
		if len(props) > 0 {
			m["properties"] = props
			if len(req) > 0 {
				m["required"] = req
			}
		}
		var branches []any
		for _, group := range groups[1:] {
			// A branch the shrinker emptied by dropping its last property is
			// left out rather than emitted as `{}`: an empty branch constrains
			// nothing, so keeping it would put an applicator in the schema that
			// no mutation can speak for.
			if len(group) == 0 {
				continue
			}
			props, req := coDeclare(group)
			branch := map[string]any{"properties": props}
			if len(req) > 0 {
				branch["required"] = req
			}
			branches = append(branches, branch)
		}
		if len(branches) > 0 {
			// A branch's propertyNames binds the same object the parent's does,
			// and putting it here is the only way to ask whether the branch was
			// read for it: stated beside the allOf it would be enforced either
			// way. See propNamesInBranch.
			if n.propNamesInBranch() {
				branches[0].(map[string]any)["propertyNames"] = map[string]any{"pattern": coPropNamePattern}
			}
			m["allOf"] = branches
		}
		if len(n.branches) > 0 {
			var alts []any
			for _, d := range n.branches {
				alts = append(alts, map[string]any{
					"properties": map[string]any{d.name: map[string]any{"type": d.jtype}},
					"required":   []string{d.name},
				})
			}
			if n.comp == coCompOneOf {
				m["oneOf"] = alts
			} else {
				m["anyOf"] = alts
			}
		}
		n.emitKeyKeywords(m)

	case coTuple:
		m["type"] = "array"
		prefix := make([]any, 0, len(n.tupleTypes))
		for _, t := range n.tupleTypes {
			entry := map[string]any{"type": t}
			// Only the static arrangement states a bound beside the type; see
			// buildTuple.
			if n.tupleMode == coTupleUnevalFalse {
				_, _, _, bound := coTupleEntry(t)
				for k, v := range bound {
					entry[k] = v
				}
			}
			prefix = append(prefix, entry)
		}
		switch n.tupleMode {
		case coTupleUnevalAllOf:
			// The prefix sits behind an applicator, so nothing beside
			// unevaluatedItems names a position. What the branch evaluates is
			// still exactly the prefix, since an allOf branch has to match.
			m["allOf"] = []any{map[string]any{"prefixItems": prefix}}
			m["unevaluatedItems"] = false
		case coTupleUnevalSchema:
			m["prefixItems"] = prefix
			m["unevaluatedItems"] = map[string]any{"type": n.unevalItemType}
		default:
			m["prefixItems"] = prefix
			m["unevaluatedItems"] = false
		}

	case coMap:
		// No `properties` and no `patternProperties`: additionalProperties
		// governs the whole object, which is what makes it a map rather than a
		// struct with an overflow.
		if n.mapNullable {
			m["type"] = []any{"object", "null"}
		} else {
			m["type"] = "object"
		}
		m["additionalProperties"] = n.elem.fragment()

	case coArray:
		m["type"] = "array"
		m["items"] = n.elem.fragment()
		if n.minItems != nil {
			m["minItems"] = *n.minItems
		}
		if n.maxItems != nil {
			m["maxItems"] = *n.maxItems
		}
		if n.unique {
			m["uniqueItems"] = true
		}
		if n.contains {
			m["contains"] = map[string]any{"type": "integer", "minimum": n.containsMin}
			if n.emitMinContains {
				m["minContains"] = n.containsMatches()
			}
			if n.emitMaxContains {
				m["maxContains"] = n.containsMatches()
			}
		}
	case coString:
		m["type"] = "string"
		if n.patIdx >= 0 {
			m["pattern"] = coPatterns[n.patIdx].expr
			break
		}
		if n.scalarAllOf {
			var branches []any
			if n.emitMin {
				branches = append(branches, map[string]any{"minLength": n.lenLo})
			}
			if n.emitMax {
				branches = append(branches, map[string]any{"maxLength": n.lenHi})
			}
			m["allOf"] = branches
			break
		}
		if n.emitMin {
			m["minLength"] = n.lenLo
		}
		if n.emitMax {
			m["maxLength"] = n.lenHi
		}
	case coInteger, coNumber:
		if n.kind == coInteger {
			m["type"] = "integer"
		} else {
			m["type"] = "number"
		}
		switch n.minStyle {
		case coBoundInclusive:
			m["minimum"] = n.lo
		case coBoundExclusive:
			m["exclusiveMinimum"] = n.lo
		}
		switch n.maxStyle {
		case coBoundInclusive:
			m["maximum"] = n.hi
		case coBoundExclusive:
			m["exclusiveMaximum"] = n.hi
		}
		if n.emitMultipleOf {
			m["multipleOf"] = n.step
		}
	case coBoolean:
		m["type"] = "boolean"
	case coNull:
		m["type"] = "null"
	case coEnum:
		m["enum"] = n.choices
	case coConst:
		m["const"] = n.choices[0]
	case coRef:
		m["$ref"] = "#/$defs/" + n.refName

	case coAltAnyOf:
		alts := []any{
			map[string]any{"type": "string", "minLength": n.altStrMin},
			map[string]any{"type": "integer", "minimum": n.altIntMin},
		}
		if n.altOneOf {
			m["oneOf"] = alts
		} else {
			m["anyOf"] = alts
		}

	case coOneOfWin:
		if n.winHoist {
			m["type"] = "integer"
			m["oneOf"] = []any{
				map[string]any{"minimum": n.winLo0, "maximum": n.winHi0},
				map[string]any{"minimum": n.winLo1, "maximum": n.winHi1},
			}
			break
		}
		m["oneOf"] = []any{
			map[string]any{"type": "integer", "minimum": n.winLo0, "maximum": n.winHi0},
			map[string]any{"type": "integer", "minimum": n.winLo1, "maximum": n.winHi1},
		}

	case coOneOfObj:
		m["oneOf"] = []any{
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					coOneOfObjKeys[0]: map[string]any{"type": "string", "minLength": n.objStrMin},
				},
				"required": []string{coOneOfObjKeys[0]},
			},
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					coOneOfObjKeys[1]: map[string]any{"type": "integer", "minimum": n.objIntMin},
				},
				"required": []string{coOneOfObjKeys[1]},
			},
		}

	case coIfElse:
		m["if"] = map[string]any{"type": "integer", "minimum": n.pivot}
		m["then"] = map[string]any{"type": "integer", "maximum": n.pivot + n.span}
		m["else"] = map[string]any{"type": "integer", "minimum": n.pivot - n.span}

	case coNot:
		m["not"] = map[string]any{"type": n.notType}
	}
	return m
}

// emitKeyKeywords writes the object keywords that speak about keys rather than
// about one named property. Every one of them is guarded by the predicate that
// decides whether its mutation is still unambiguous, so the schema and the
// mutation catalogue can never disagree about which of them are live.
func (n *coNode) emitKeyKeywords(m map[string]any) {
	if len(n.patKeys) > 0 {
		value := map[string]any{}
		if n.patStr {
			value["type"] = "string"
			value["minLength"] = n.patBound
		} else {
			value["type"] = "integer"
			value["minimum"] = n.patBound
		}
		m["patternProperties"] = map[string]any{coPatternExpr: value}
	}

	if n.cond != nil {
		m["if"] = map[string]any{
			"properties": map[string]any{coCondKey: map[string]any{"const": coCondYes}},
			"required":   []string{coCondKey},
		}
		// `then` requires what it declares, so both sides of `if` have a mutant:
		// on the `then` side, removing thenKey; on the other, turning condKey
		// into the const so that `then` starts applying to a document that does
		// not carry it.
		m["then"] = map[string]any{
			"properties": map[string]any{coThenKey: map[string]any{"type": "integer", "minimum": n.cond.min}},
			"required":   []string{coThenKey},
		}
	}

	switch {
	case n.addlFalseActive():
		m["additionalProperties"] = false
	case n.addlSchemaActive():
		m["additionalProperties"] = map[string]any{"type": "integer", "minimum": n.unevalMin}
	case n.extra == coExtraUnevalFalse:
		m["unevaluatedProperties"] = false
	case n.extra == coExtraUnevalSchema:
		m["unevaluatedProperties"] = map[string]any{"type": "integer", "minimum": n.unevalMin}
	case n.maxPropsActive():
		m["maxProperties"] = n.instanceKeyCount()
	case n.propNamesActive() && !n.propNamesInBranch():
		m["propertyNames"] = map[string]any{"pattern": coPropNamePattern}
	}

	if n.minPropsActive() {
		m["minProperties"] = n.instanceKeyCount()
	}

	if n.depActive() {
		deps := append([]string(nil), n.depOn...)
		sort.Strings(deps)
		if n.dep == coDepRequired {
			m["dependentRequired"] = map[string]any{n.depTrig: deps}
		} else {
			// The branch states both halves of what a subschema can say: which
			// keys must be there, and what one of them has to look like. The
			// two are mutated separately -- removing a dependent, and moving
			// coDepShapeKey off the branch's minimum -- so a dependency that
			// fired on presence and never on shape would be caught by the
			// second even while the first passed.
			m["dependentSchemas"] = map[string]any{n.depTrig: map[string]any{
				"required": deps,
				"properties": map[string]any{
					coDepShapeKey: map[string]any{"minimum": n.depShapeMin},
				},
			}}
		}
	}
}

// coDeclare renders one group of properties as the `properties` and `required`
// of whichever schema object is declaring them -- the node itself, or one of
// its allOf branches.
func coDeclare(group []*coProp) (map[string]any, []string) {
	props := map[string]any{}
	var req []string
	for _, p := range group {
		props[p.name] = p.node.fragment()
		if p.required {
			req = append(req, p.name)
		}
	}
	sort.Strings(req)
	return props, req
}

// ---------------------------------------------------------------------------
// Instance emission
// ---------------------------------------------------------------------------

func (d *coDoc) instance() any { return d.value(d.root) }

func (d *coDoc) value(n *coNode) any {
	switch n.kind {
	case coObject:
		m := map[string]any{}
		for _, p := range n.props {
			if p.present {
				m[p.name] = d.value(p.node)
			}
		}
		// Exactly one discriminator goes in. The others stay out, which is what
		// makes "branch i is not satisfied" true for every other i: a branch
		// lists its own discriminator in `required`, and the document does not
		// have it.
		if len(n.branches) > 0 {
			disc := n.branches[n.branchIx]
			m[disc.name] = disc.value
		}
		for _, k := range n.patKeys {
			m[k] = n.patValue()
		}
		if n.cond != nil {
			// thenKey goes in exactly when `if` matches, which is what makes
			// "present" and "evaluated" the same decision rather than two.
			if n.cond.yes {
				m[coCondKey] = coCondYes
				m[coThenKey] = n.cond.val
			} else {
				m[coCondKey] = coCondNo
			}
		}
		if n.extra == coExtraUnevalSchema {
			m[coUnevalKey] = n.unevalValue()
		}
		if n.addlSchemaActive() {
			m[coExtraKey] = n.unevalValue()
		}
		if n.depShapeActive() {
			m[coDepShapeKey] = n.depShapeValue()
		}
		return m
	case coTuple:
		return d.tupleValue(n, len(n.tupleTypes)+n.tupleTailCount())
	case coArray:
		return d.arrayValue(n, n.numItems)
	case coMap:
		m := map[string]any{}
		for _, k := range n.mapKeys {
			m[k] = d.value(n.elem)
		}
		return m
	case coString:
		return n.strValue
	case coInteger:
		if n.intFloatToken {
			// json.RawMessage is emitted verbatim by json.Marshal, which is the
			// only way to keep the "1.0" spelling: an int64 or a float64 both
			// marshal as "1". The round-trip comparison reparses both sides
			// into `any`, where 1.0 and 1 are the same float64, so a value the
			// generated code reads back in the short spelling still matches.
			return json.RawMessage(fmt.Sprintf("%d.0", int64(n.numValue)))
		}
		return int64(n.numValue)
	case coNumber:
		return n.numValue
	case coBoolean:
		return n.boolValue
	case coNull:
		return nil
	case coEnum, coConst:
		return n.choices[n.choiceIx]
	case coRef:
		return d.value(d.defs[n.refName])
	case coAltAnyOf:
		if n.altUseStr {
			return n.altStr
		}
		return n.altInt
	case coOneOfWin:
		return n.winValue
	case coOneOfObj:
		// Under objBoth both required keys go in and the branch that is not
		// taken is unsatisfied by its own nested constraint instead -- the one
		// arrangement where "exactly one branch" cannot be read off the key set.
		if n.objBoth {
			return map[string]any{coOneOfObjKeys[0]: n.objStr, coOneOfObjKeys[1]: n.objInt}
		}
		// Otherwise exactly one branch's required key goes in, so the other
		// branch is unsatisfied by construction and "exactly one" holds without
		// anything having to evaluate the branches.
		if n.objUseStr {
			return map[string]any{coOneOfObjKeys[0]: n.objStr}
		}
		return map[string]any{coOneOfObjKeys[1]: n.objInt}
	case coIfElse:
		return n.iteValue
	case coNot:
		return n.notOK
	}
	return nil
}

// arrayValue builds an array of count structurally identical elements. Sharing
// one element shape is what makes element mutations addressable: a mutation
// derived from the element node always applies at index 0, whatever the
// length, and lengthening the array for a maxItems mutant needs no new
// decisions.
func (d *coDoc) arrayValue(n *coNode, count int) []any {
	out := make([]any, 0, count)
	for i := 0; i < count; i++ {
		if n.unique || n.contains {
			// Numbering the positions makes the array unique at every length,
			// so the length mutants stay violations of exactly minItems or
			// maxItems and the only duplicate is the one uniqueItems' own
			// mutant plants. It is also what gives `contains` a decided number
			// of matching elements: position i matches iff i >= containsMin.
			out = append(out, int64(i))
			continue
		}
		out = append(out, d.value(n.elem))
	}
	return out
}

// tupleValue builds count positions of a tuple, taking the type of position i
// from prefixItems when there is one. count beyond the prefix is how the
// unevaluatedItems mutant is built: that position is one no prefix entry
// evaluates.
//
// What a position past the prefix holds is decided by the mode. Where
// unevaluatedItems is a sub-schema the position must satisfy it, so it holds a
// value of that type and the array stays conforming however many of them there
// are; the mutant is a value of a type the sub-schema excludes, not an extra
// element. Where unevaluatedItems is false no such position may exist at all,
// so the value written there is immaterial and the extra element is itself the
// violation.
func (d *coDoc) tupleValue(n *coNode, count int) []any {
	out := make([]any, 0, count)
	for i := 0; i < count; i++ {
		if i < len(n.tupleTypes) {
			out = append(out, coTupleValue(n.tupleTypes[i]))
			continue
		}
		if n.tupleMode == coTupleUnevalSchema {
			out = append(out, coTupleUnevalConforming(n.unevalItemType))
			continue
		}
		out = append(out, coTupleValue(coTupleTypes[i%len(coTupleTypes)].name))
	}
	return out
}

// ---------------------------------------------------------------------------
// Mutations
// ---------------------------------------------------------------------------

// coMutation is an instance edit that violates exactly one keyword at exactly
// one location. Everything else about the instance stays conforming, so a
// Validate() that accepts the result has demonstrably failed to enforce that
// one keyword.
type coMutation struct {
	Keyword string // the keyword the edit violates
	Path    []any  // navigation from the root of the instance; string or int steps
	Prop    string // the JSON property name the generated error should mention
	Delete  bool   // remove Path's last step instead of replacing it
	Value   any    // replacement, when Delete is false
	Want    []string
	// Loose marks a mutation whose rejection may legitimately come from
	// json.Unmarshal rather than Validate: a value of the wrong JSON type
	// usually cannot even be decoded into the generated Go field.
	Loose bool
	// Via names the applicator the violated keyword was reached through, when
	// it was reached through one. It changes nothing about the mutation; it is
	// there so a report says which of two identical-looking cases -- a
	// minLength declared inline and the same minLength declared inside an allOf
	// branch -- actually failed.
	Via string
}

func (m coMutation) key() string {
	var b strings.Builder
	b.WriteString(m.Keyword)
	if m.Via != "" {
		fmt.Fprintf(&b, "[%s]", m.Via)
	}
	b.WriteByte('@')
	for _, step := range m.Path {
		fmt.Fprintf(&b, "/%v", step)
	}
	return b.String()
}

// apply returns a copy of doc with the mutation applied. The instance is
// round-tripped through JSON first so the edit cannot alias the original.
func (m coMutation) apply(instance any) (any, error) {
	raw, err := json.Marshal(instance)
	if err != nil {
		return nil, err
	}
	var copied any
	if err := json.Unmarshal(raw, &copied); err != nil {
		return nil, err
	}
	if len(m.Path) == 0 {
		return m.Value, nil
	}
	parent := copied
	for _, step := range m.Path[:len(m.Path)-1] {
		switch s := step.(type) {
		case string:
			obj, ok := parent.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("path step %q: parent is %T, not an object", s, parent)
			}
			parent = obj[s]
		case int:
			arr, ok := parent.([]any)
			if !ok || s >= len(arr) {
				return nil, fmt.Errorf("path step %d: parent is %T", s, parent)
			}
			parent = arr[s]
		}
	}
	switch last := m.Path[len(m.Path)-1].(type) {
	case string:
		obj, ok := parent.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("final step %q: parent is %T, not an object", last, parent)
		}
		if m.Delete {
			delete(obj, last)
		} else {
			obj[last] = m.Value
		}
	case int:
		arr, ok := parent.([]any)
		if !ok || last >= len(arr) {
			return nil, fmt.Errorf("final step %d: parent is %T", last, parent)
		}
		arr[last] = m.Value
	}
	return copied, nil
}

// mutations walks the tree beside the instance and derives one mutation per
// emitted keyword. It never inspects the schema JSON: each entry is written at
// the point the builder's decision is still in hand.
func (d *coDoc) mutations() []coMutation {
	var out []coMutation
	d.collect(d.root, nil, "", &out)
	return out
}

func coPath(base []any, step any) []any {
	next := make([]any, len(base), len(base)+1)
	copy(next, base)
	return append(next, step)
}

func (d *coDoc) collect(n *coNode, path []any, prop string, out *[]coMutation) {
	switch n.kind {
	case coRef:
		d.collect(d.defs[n.refName], path, prop, out)

	case coObject:
		for _, p := range n.props {
			before := len(*out)
			if p.required {
				*out = append(*out, coMutation{
					Keyword: "required",
					Path:    coPath(path, p.name),
					Prop:    p.name,
					Delete:  true,
					Want:    []string{p.name, "required property is missing"},
				})
			}
			if p.present {
				d.collect(p.node, coPath(path, p.name), p.name, out)
			}
			// Every constraint on a property the object pushed into an allOf
			// branch is only reachable by reading that branch, so each of these
			// mutants is also a test that the branch was read at all.
			if n.comp == coCompAllOf && p.group > 0 {
				for i := before; i < len(*out); i++ {
					(*out)[i].Via = "allOf"
				}
			}
		}
		coCollectDiscriminators(n, path, prop, out)
		d.collectKeyKeywords(n, path, prop, out)
		if len(path) > 0 {
			*out = append(*out, coMutation{
				Keyword: "type", Path: path, Prop: prop, Value: 7, Loose: true,
			})
		}

	case coArray:
		if n.minItems != nil && *n.minItems > 0 {
			*out = append(*out, coMutation{
				Keyword: "minItems", Path: path, Prop: prop,
				Value: d.arrayValue(n, *n.minItems-1),
				Want:  []string{prop, "minimum is"},
			})
		}
		if n.maxItems != nil {
			*out = append(*out, coMutation{
				Keyword: "maxItems", Path: path, Prop: prop,
				Value: d.arrayValue(n, *n.maxItems+1),
				Want:  []string{prop, "maximum is"},
			})
		}
		if n.unique && n.numItems > 1 {
			// Element 0 is 0 and element 1 is 1, so writing 0 over element 1
			// makes the one duplicate the array did not have. The length is
			// untouched, so minItems and maxItems still hold.
			*out = append(*out, coMutation{
				Keyword: "uniqueItems", Path: coPath(path, 1), Prop: prop,
				Value: int64(0), Want: []string{prop, "not unique"},
			})
		}
		if n.contains {
			// The lowest matching element is at index containsMin and holds
			// that same value. Writing 0 over it makes it stop matching and
			// changes nothing else: the length is untouched, it is still an
			// integer, and uniqueItems is not on a contains array -- so the
			// count is one short of the bound and nothing else is violated.
			want := []string{prop, "no element matches"}
			if n.emitMinContains {
				want = []string{prop, "minimum is"}
			}
			*out = append(*out, coMutation{
				Keyword: "contains", Path: coPath(path, n.containsMin), Prop: prop,
				Value: int64(0), Want: want,
			})
			if n.emitMaxContains {
				// The mirror image: the highest element below the threshold is
				// at index containsMin-1, and raising it to the threshold adds
				// a match the upper bound does not allow.
				*out = append(*out, coMutation{
					Keyword: "maxContains", Path: coPath(path, n.containsMin-1), Prop: prop,
					Value: int64(n.containsMin), Want: []string{prop, "maximum is"},
				})
			}
		}
		if n.numItems > 0 {
			d.collect(n.elem, coPath(path, 0), prop, out)
		}
		if len(path) > 0 {
			*out = append(*out, coMutation{
				Keyword: "type", Path: path, Prop: prop, Value: 7, Loose: true,
			})
		}

	case coMap:
		// The value sub-schema's own mutants, under the last key rather than the
		// first. Every key holds the same value, so any one of them would do;
		// taking the last one means a map carrying more than one key is only
		// accepted if the check reached past the first, which a loop that broke
		// early would not.
		if len(n.mapKeys) > 0 {
			last := n.mapKeys[len(n.mapKeys)-1]
			d.collect(n.elem, coPath(path, last), prop, out)
		}
		if len(path) > 0 {
			*out = append(*out, coMutation{
				Keyword: "type", Path: path, Prop: prop, Value: 7, Loose: true,
			})
		}

	case coTuple:
		// What unevaluatedItems forbids, and how the violation is built, is
		// decided by which arrangement the node carries -- not read back off the
		// instance. In every mode the prefix evaluates positions 0..k-1 and
		// nothing else evaluates anything: coTupleUnevalFalse states the prefix
		// as a sibling, coTupleUnevalAllOf behind an allOf branch that must
		// match, and neither introduces a runtime choice about which positions
		// were reached.
		switch n.tupleMode {
		case coTupleUnevalSchema:
			// The positions past the prefix are typed rather than forbidden, so
			// the violation is a value of a type the sub-schema excludes, at the
			// one such position the conforming instance already carries. The
			// length does not move, so no length bound can be what rejects it.
			*out = append(*out, coMutation{
				Keyword: "unevaluatedItemsSchema", Path: coPath(path, len(n.tupleTypes)), Prop: prop,
				Value: coTupleUnevalOther(n.unevalItemType),
				Want:  []string{prop, "unevaluatedItems"},
			})
		case coTupleUnevalAllOf:
			// One element past the prefix, which no branch evaluates. The prefix
			// is behind an applicator, so the length it implies is not a bound
			// the generated code may state: the rejection has to come from
			// unevaluatedItems having seen through the allOf.
			*out = append(*out, coMutation{
				Keyword: "unevaluatedItems", Path: path, Prop: prop,
				Via:   "allOf",
				Value: d.tupleValue(n, len(n.tupleTypes)+1),
				Want:  []string{prop},
			})
		default:
			// One element past the prefix. unevaluatedItems: false forbids it
			// whatever the value is, so the element the mutant appends needs no
			// thought beyond being valid JSON.
			*out = append(*out, coMutation{
				Keyword: "unevaluatedItems", Path: path, Prop: prop,
				Value: d.tupleValue(n, len(n.tupleTypes)+1),
				Want:  []string{prop, "maximum is"},
			})
		}
		// The positional sub-schemas. Only the static arrangement states a bound
		// beside a position's type (see buildTuple), so only there is there a
		// sub-schema to violate without also changing the type; every mode can
		// be asked whether the type itself is read.
		for i, t := range n.tupleTypes {
			_, under, other, bound := coTupleEntry(t)
			if n.tupleMode == coTupleUnevalFalse && len(bound) > 0 {
				*out = append(*out, coMutation{
					Keyword: "prefixItemsBound", Path: coPath(path, i), Prop: prop,
					Value: under, Want: []string{prop},
				})
			}
			*out = append(*out, coMutation{
				Keyword: "prefixItemsType", Path: coPath(path, i), Prop: prop,
				Value: other, Want: []string{prop},
			})
		}
		*out = append(*out, coMutation{
			Keyword: "type", Path: path, Prop: prop, Value: 7, Loose: true,
		})

	case coString:
		if n.patIdx >= 0 {
			*out = append(*out, coMutation{
				Keyword: "pattern", Path: path, Prop: prop,
				Value: coPatterns[n.patIdx].bad,
				Want:  []string{prop, "does not match pattern"},
			})
		} else {
			if n.emitMin && n.lenLo > 0 {
				*out = append(*out, coMutation{
					Keyword: "minLength", Path: path, Prop: prop,
					Value: coFillString(n.lenLo - 1),
					Want:  []string{prop, "is less than minimum"},
				})
			}
			if n.emitMax {
				*out = append(*out, coMutation{
					Keyword: "maxLength", Path: path, Prop: prop,
					Value: coFillString(n.lenHi + 1),
					Want:  []string{prop, "exceeds maximum"},
				})
			}
		}
		*out = append(*out, coMutation{
			Keyword: "type", Path: path, Prop: prop, Value: 7, Loose: true,
		})

	case coInteger, coNumber:
		num := func(v float64) any {
			if n.kind == coInteger {
				return int64(v)
			}
			return v
		}
		switch n.minStyle {
		case coBoundInclusive:
			*out = append(*out, coMutation{
				Keyword: "minimum", Path: path, Prop: prop, Value: num(n.lo - n.step),
				Want: []string{prop, "is less than minimum"},
			})
		case coBoundExclusive:
			*out = append(*out, coMutation{
				Keyword: "exclusiveMinimum", Path: path, Prop: prop, Value: num(n.lo),
				Want: []string{prop, "must be greater than"},
			})
		}
		switch n.maxStyle {
		case coBoundInclusive:
			*out = append(*out, coMutation{
				Keyword: "maximum", Path: path, Prop: prop, Value: num(n.hi + n.step),
				Want: []string{prop, "exceeds maximum"},
			})
		case coBoundExclusive:
			*out = append(*out, coMutation{
				Keyword: "exclusiveMaximum", Path: path, Prop: prop, Value: num(n.hi),
				Want: []string{prop, "must be less than"},
			})
		}
		if n.emitMultipleOf {
			lo, _ := n.allowedRange()
			*out = append(*out, coMutation{
				Keyword: "multipleOf", Path: path, Prop: prop, Value: num(lo + n.offLatticeOffset()),
				Want: []string{prop, "is not a multiple of"},
			})
		}
		*out = append(*out, coMutation{
			Keyword: "type", Path: path, Prop: prop, Value: "not-a-number", Loose: true,
		})

	case coBoolean:
		*out = append(*out, coMutation{
			Keyword: "type", Path: path, Prop: prop, Value: "not-a-boolean", Loose: true,
		})

	case coNull:
		*out = append(*out, coMutation{
			Keyword: "type", Path: path, Prop: prop, Value: 123,
			Want: []string{prop},
		})

	case coEnum:
		*out = append(*out, coMutation{
			Keyword: "enum", Path: path, Prop: prop, Value: n.offValue,
			Want: []string{prop, "invalid "},
		})

	case coConst:
		*out = append(*out, coMutation{
			Keyword: "const", Path: path, Prop: prop, Value: n.offValue,
			Want: []string{prop, "invalid "},
		})

	case coAltAnyOf:
		// Both mutants are emitted whichever alternative the instance took,
		// because a mutant only has to be invalid -- it does not have to be a
		// near miss of the branch the instance happens to be on. Each violates
		// one branch by its constraint and the other by its type, so no branch
		// is satisfied and the document is invalid under anyOf and under oneOf
		// alike.
		//
		// The two spellings reject in different places, because they generate
		// different things.
		//
		// anyOf becomes an alternatives wrapper that keeps the raw JSON, so the
		// rejection is a Validate() failure reported as a type failure ("string
		// is not allowed"): the wrapper's last act, once no alternative has
		// matched, is to name the JSON type it was handed. That is the message
		// to assert on; asserting on the word "anyOf" would be asserting on a
		// message the generator never emits here.
		//
		// oneOf becomes a sealed-interface union, and there the decision of
		// which branch a value belongs to *is* the decode: the field holds one
		// typed variant wrapper, so a value matching no branch has nothing to be
		// stored as and UnmarshalJSON is the only place that can say so. The
		// mutants are marked Loose for that reason and not because the check is
		// weaker -- an accepted mutant is still a failure, which is the property
		// under test. Want records the message the union does emit, for the day
		// the site changes.
		//
		// This position says nothing about whether Validate descends into the
		// union, and cannot: a scalar variant is a plain Go string or int64 with
		// no Validate to descend to. coOneOfObj is the position that settles it.
		what := "anyOf"
		want := []string{prop, "is not allowed"}
		loose := false
		if n.altOneOf {
			what = "oneOf"
			want = []string{"oneOf"}
			loose = true
		}
		*out = append(*out, coMutation{
			Keyword: what + "AllBranchesString", Path: path, Prop: prop,
			Value: coFillString(n.altStrMin - 1),
			Want:  want, Loose: loose,
		})
		*out = append(*out, coMutation{
			Keyword: what + "AllBranchesNumber", Path: path, Prop: prop,
			Value: n.altIntMin - 1,
			Want:  want, Loose: loose,
		})

	case coOneOfWin:
		// [winLo1, winHi0] is inside both windows, so this mutant satisfies two
		// branches. It is the mutant that separates oneOf from anyOf: an
		// implementation that stops counting at the first match accepts it.
		*out = append(*out, coMutation{
			Keyword: "oneOfMatchesTwo", Path: path, Prop: prop, Value: n.winLo1,
			Want: []string{prop, "oneOf"},
		})
		// Below the lower window, so it satisfies neither branch.
		*out = append(*out, coMutation{
			Keyword: "oneOfMatchesNone", Path: path, Prop: prop, Value: n.winLo0 - 1,
			Want: []string{prop, "oneOf"},
		})

	case coOneOfObj:
		// Both mutants are emitted whichever branch the instance took, on the
		// same reasoning as coAltAnyOf: a mutant has to be invalid, not a near
		// miss of the branch in play. Each replaces the whole union value with
		// an object carrying one branch's required key and a value that branch
		// forbids. That branch is then selected -- its required key is the only
		// one present, so the other branch's `required` fails and selection is
		// unambiguous -- and it is violated, so no branch is satisfied and the
		// document is invalid.
		//
		// Neither is Loose, and that is the whole point of this node. The value
		// is of the right JSON type for the field it lands in, so it decodes;
		// the only place left that can reject it is the owner's Validate
		// descending into the variant selection chose. A generator that skips
		// that descent accepts both, which is what issue #61 was.
		*out = append(*out, coMutation{
			Keyword: "oneOfObjBranchString", Path: path, Prop: prop,
			Value: map[string]any{coOneOfObjKeys[0]: coFillString(n.objStrMin - 1)},
			Want:  []string{prop, coOneOfObjKeys[0], "is less than minimum"},
		})
		*out = append(*out, coMutation{
			Keyword: "oneOfObjBranchNumber", Path: path, Prop: prop,
			Value: map[string]any{coOneOfObjKeys[1]: n.objIntMin - 1},
			Want:  []string{prop, coOneOfObjKeys[1], "is less than minimum"},
		})
		// An object carrying neither required key satisfies neither branch.
		// Selection is the only thing that can say so -- there is no variant to
		// store the value as -- so this one is expected in the decoder.
		*out = append(*out, coMutation{
			Keyword: "oneOfObjMatchesNone", Path: path, Prop: prop,
			Value: map[string]any{}, Want: []string{"oneOf"}, Loose: true,
		})

		if !n.objBoth {
			break
		}
		// Both required keys are present, so neither of the two mutants below
		// changes which branches selection *considers* -- only whether each one
		// is satisfied. They are the pair that separates a selection reading the
		// whole branch from one that stops at the required keys: under the
		// latter every one of these documents, and the conforming instance they
		// are derived from, counts two matches and is rejected alike.
		//
		// Both are Loose. Once the branches are read, a value satisfying two of
		// them and a value satisfying none both leave the union with no single
		// variant to store the value as, which is the decoder's to say.
		if n.objUseStr {
			// varInt sits one below its minimum, so branch 1 is unsatisfied.
			// Raise it to the bound and both branches hold.
			*out = append(*out, coMutation{
				Keyword: "oneOfObjBothMatchesTwo", Path: coPath(path, coOneOfObjKeys[1]), Prop: prop,
				Value: n.objIntMin, Want: []string{"oneOf"}, Loose: true,
			})
			// Shorten varStr below its minLength and branch 0 stops holding
			// too, leaving no branch satisfied.
			*out = append(*out, coMutation{
				Keyword: "oneOfObjBothMatchesNone", Path: coPath(path, coOneOfObjKeys[0]), Prop: prop,
				Value: coFillString(n.objStrMin - 1), Want: []string{"oneOf"}, Loose: true,
			})
		} else {
			*out = append(*out, coMutation{
				Keyword: "oneOfObjBothMatchesTwo", Path: coPath(path, coOneOfObjKeys[0]), Prop: prop,
				Value: coFillString(n.objStrMin), Want: []string{"oneOf"}, Loose: true,
			})
			*out = append(*out, coMutation{
				Keyword: "oneOfObjBothMatchesNone", Path: coPath(path, coOneOfObjKeys[1]), Prop: prop,
				Value: n.objIntMin - 1, Want: []string{"oneOf"}, Loose: true,
			})
		}

	case coIfElse:
		// Above the pivot, so `if` holds and `then` applies -- and above
		// pivot+span, so `then` is violated.
		*out = append(*out, coMutation{
			Keyword: "then", Path: path, Prop: prop, Value: n.pivot + n.span + 1,
			Want: []string{prop, "then"},
		})
		// Below the pivot, so `if` fails and `else` applies -- and below
		// pivot-span, so `else` is violated.
		*out = append(*out, coMutation{
			Keyword: "else", Path: path, Prop: prop, Value: n.pivot - n.span - 1,
			Want: []string{prop, "else"},
		})

	case coNot:
		*out = append(*out, coMutation{
			Keyword: "not", Path: path, Prop: prop, Value: n.notBad,
			Want: []string{prop, "must not be " + n.notType},
		})
	}
}

// collectDiscriminators derives the mutations of an object-level anyOf or
// oneOf. Both rest on the same fact: the instance carries exactly one
// discriminator, so exactly one branch is satisfied, and each branch's identity
// is decided by a property no other branch mentions.
func coCollectDiscriminators(n *coNode, path []any, prop string, out *[]coMutation) {
	if len(n.branches) == 0 {
		return
	}
	here := n.branches[n.branchIx]

	// Removing the only discriminator present leaves every branch short of its
	// required property, so the count drops to zero. That is invalid under
	// anyOf ("at least one") and under oneOf ("exactly one") alike.
	*out = append(*out, coMutation{
		Keyword: coCompName(n.comp) + "MatchesNone",
		Path:    coPath(path, here.name),
		Prop:    prop,
		Delete:  true,
		Want:    []string{prop, coCompName(n.comp)},
	})

	// Adding a second branch's discriminator, with a value that branch accepts,
	// takes the count to two. Under oneOf that is a violation; under anyOf it
	// is not, and no such mutation is emitted there -- which is the whole
	// asymmetry between the two keywords.
	if n.comp == coCompOneOf {
		other := n.branches[(n.branchIx+1)%len(n.branches)]
		*out = append(*out, coMutation{
			Keyword: "oneOfMatchesTwo",
			Path:    coPath(path, other.name),
			Prop:    prop,
			Value:   other.value,
			Want:    []string{prop, "oneOf"},
		})
	}
}

// collectKeyKeywords derives the mutations of the object keywords that speak
// about keys. Each rests on the builder having decided which keys the instance
// carries and which applicator accounts for each of them, so "this edit
// violates exactly this keyword" is an argument about a decision rather than
// about the schema.
//
// The guards here are the same predicates the schema emission consults, so a
// keyword that is not in the schema contributes no mutation and a keyword that
// is contributes exactly one.
func (d *coDoc) collectKeyKeywords(n *coNode, path []any, prop string, out *[]coMutation) {
	// patternProperties. The key stays and only its value changes, so which
	// keys exist is untouched and nothing but the pattern's own subschema can
	// be the reason for the rejection.
	if len(n.patKeys) > 0 {
		key := n.patKeys[0]
		want := []string{"patternProperties", key, "is less than minimum"}
		if n.patStr {
			want = []string{"patternProperties", key, "minLength"}
		}
		*out = append(*out, coMutation{
			Keyword: "patternProperties", Path: coPath(path, key), Prop: prop,
			Value: n.patBadValue(), Want: want,
		})
	}

	if n.cond != nil {
		if n.cond.yes {
			// `if` matches, so `then` applies and requires thenKey.
			*out = append(*out, coMutation{
				Keyword: "thenRequired", Path: coPath(path, coThenKey), Prop: prop,
				Delete: true, Want: []string{"then", coThenKey},
			})
			*out = append(*out, coMutation{
				Keyword: "thenSchema", Path: coPath(path, coThenKey), Prop: prop,
				Value: n.cond.min - 1, Want: []string{"then", coThenKey},
			})
			if n.extra == coExtraUnevalFalse {
				// This is the mutant unevaluatedProperties exists for. Moving
				// condKey off the const makes `if` fail, so `then` no longer
				// applies and no longer evaluates thenKey. There is no `else`,
				// and condKey is typed by the object's own `properties`, so
				// nothing else about the document changes: its single fault is
				// a key that no applicator accounted for.
				*out = append(*out, coMutation{
					Keyword: "unevaluatedPropertiesConditional", Path: coPath(path, coCondKey), Prop: prop,
					Value: coCondNo, Want: []string{"unevaluated property", coThenKey},
				})
			}
		} else {
			// `if` fails, so thenKey is legitimately absent. Moving condKey
			// onto the const makes `then` start applying to a document that
			// does not carry what `then` requires.
			*out = append(*out, coMutation{
				Keyword: "thenTriggered", Path: coPath(path, coCondKey), Prop: prop,
				Value: coCondYes, Want: []string{"then", coThenKey},
			})
		}
	}

	// The keyword about keys the object does not name. At most one is live, so
	// the added key has exactly one thing to violate.
	switch {
	case n.addlFalseActive():
		*out = append(*out, coMutation{
			Keyword: "additionalProperties", Path: coPath(path, coExtraKey), Prop: prop,
			Value: int64(1), Want: []string{"additional property", coExtraKey},
		})
	case n.addlSchemaActive():
		// coExtraKey is already in the instance and already covered by
		// additionalProperties; the mutation only moves its value off the
		// subschema's minimum.
		*out = append(*out, coMutation{
			Keyword: "additionalPropertiesSchema", Path: coPath(path, coExtraKey), Prop: prop,
			Value: n.unevalBadValue(),
			Want:  []string{"additionalProperties", coExtraKey, "is less than minimum"},
		})
	case n.extra == coExtraUnevalFalse:
		*out = append(*out, coMutation{
			Keyword: "unevaluatedProperties", Path: coPath(path, coExtraKey), Prop: prop,
			Value: int64(1), Want: []string{"unevaluated property", coExtraKey},
		})
	case n.extra == coExtraUnevalSchema:
		// coUnevalKey is already in the instance and already unevaluated; the
		// mutation only moves its value off the subschema's minimum.
		*out = append(*out, coMutation{
			Keyword: "unevaluatedPropertiesSchema", Path: coPath(path, coUnevalKey), Prop: prop,
			Value: n.unevalBadValue(),
			Want:  []string{"unevaluated property", coUnevalKey, "is less than minimum"},
		})
	case n.maxPropsActive():
		*out = append(*out, coMutation{
			Keyword: "maxProperties", Path: coPath(path, coExtraKey), Prop: prop,
			Value: int64(1), Want: []string{"too many properties", "exceeds maximum"},
		})
	case n.propNamesActive():
		via := ""
		if n.propNamesInBranch() {
			via = "allOf"
		}
		*out = append(*out, coMutation{
			Keyword: "propertyNames", Path: coPath(path, coBadPropName), Prop: prop,
			Value: int64(1), Want: []string{"propertyNames", coBadPropName}, Via: via,
		})
	}

	// minProperties. minPropsActive has already established that the node has
	// a present property, that none of its present properties are required,
	// and that nothing else here removes a key -- so dropping the first present
	// property takes the count below the bound and does nothing else.
	if n.minPropsActive() {
		for _, p := range n.props {
			if !p.present {
				continue
			}
			*out = append(*out, coMutation{
				Keyword: "minProperties", Path: coPath(path, p.name), Prop: prop,
				Delete: true, Want: []string{"too few properties", "is less than minimum"},
			})
			break
		}
	}

	// The dependency. Its dependents are present and optional, so removing one
	// leaves `required` satisfied and the trigger in place: the only thing the
	// document now fails is the dependency itself.
	if n.depActive() {
		keyword, reported := "dependentRequired", "dependentRequired"
		if n.dep == coDepSchemas {
			keyword, reported = "dependentSchemas", "dependentSchema"
		}
		*out = append(*out, coMutation{
			Keyword: keyword, Path: coPath(path, n.depOn[0]), Prop: prop,
			Delete: true, Want: []string{reported, n.depOn[0]},
		})
	}

	// The other half of a dependentSchemas branch: what it demands of a key's
	// *value*. coDepShapeKey is already in the instance and already declared, so
	// the mutation only moves its value off the branch's minimum -- which the
	// branch is the only part of the document to state.
	if n.depShapeActive() {
		*out = append(*out, coMutation{
			Keyword: "dependentSchemasShape", Path: coPath(path, coDepShapeKey), Prop: prop,
			Value: n.depShapeBadValue(),
			Want:  []string{"dependentSchema", coDepShapeKey},
		})
	}
}

func coCompName(c coComp) string {
	if c == coCompOneOf {
		return "oneOf"
	}
	return "anyOf"
}

// ---------------------------------------------------------------------------
// Cloning and shrinking
// ---------------------------------------------------------------------------

func (n *coNode) clone() *coNode {
	if n == nil {
		return nil
	}
	c := *n
	c.props = nil
	for _, p := range n.props {
		q := *p
		q.node = p.node.clone()
		c.props = append(c.props, &q)
	}
	c.elem = n.elem.clone()
	c.minItems = coCopyInt(n.minItems)
	c.maxItems = coCopyInt(n.maxItems)
	c.choices = append([]any(nil), n.choices...)
	c.branches = nil
	for _, b := range n.branches {
		d := *b
		c.branches = append(c.branches, &d)
	}
	c.patKeys = append([]string(nil), n.patKeys...)
	c.depOn = append([]string(nil), n.depOn...)
	c.tupleTypes = append([]string(nil), n.tupleTypes...)
	c.mapKeys = append([]string(nil), n.mapKeys...)
	if n.cond != nil {
		cond := *n.cond
		c.cond = &cond
	}
	return &c
}

func coCopyInt(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func (d *coDoc) clone() *coDoc {
	c := &coDoc{defOrder: append([]string(nil), d.defOrder...), defs: map[string]*coNode{}}
	for k, v := range d.defs {
		c.defs[k] = v.clone()
	}
	c.root = d.root.clone()
	return c
}

// coNodesOf lists every node of a document in a deterministic order, so a
// shrink step can address "the i-th node" of a clone and get the same node it
// addressed in the original.
func coNodesOf(d *coDoc) []*coNode {
	var out []*coNode
	var walk func(*coNode)
	walk = func(n *coNode) {
		if n == nil {
			return
		}
		out = append(out, n)
		for _, p := range n.props {
			walk(p.node)
		}
		walk(n.elem)
	}
	for _, name := range d.defOrder {
		walk(d.defs[name])
	}
	walk(d.root)
	return out
}

// coRefCount reports how many $ref nodes point at a definition.
func coRefCount(d *coDoc, name string) int {
	count := 0
	for _, n := range coNodesOf(d) {
		if n.kind == coRef && n.refName == name {
			count++
		}
	}
	return count
}

// coReduce yields simplified variants of a document. The shrinker tries each
// in turn and keeps any that still reproduces the failure, so the reported
// case is the smallest one reachable by these moves rather than whatever the
// seed happened to produce.
func coReduce(d *coDoc) []*coDoc {
	var out []*coDoc

	// Drop a definition nothing refers to any more.
	for i, name := range d.defOrder {
		if coRefCount(d, name) > 0 {
			continue
		}
		c := d.clone()
		c.defOrder = append(append([]string(nil), c.defOrder[:i]...), c.defOrder[i+1:]...)
		delete(c.defs, name)
		out = append(out, c)
	}

	nodes := coNodesOf(d)
	for i, n := range nodes {
		switch n.kind {
		case coObject:
			// Drop each keyword about keys on its own. These are the reductions
			// that tell a failure in one of them apart from a failure in the
			// object it was attached to.
			if n.extra != coExtraNone {
				c := d.clone()
				coNodesOf(c)[i].extra = coExtraNone
				out = append(out, c)
			}
			if len(n.patKeys) > 0 {
				c := d.clone()
				coNodesOf(c)[i].patKeys = nil
				out = append(out, c)
			}
			if n.cond != nil {
				c := d.clone()
				coNodesOf(c)[i].cond = nil
				out = append(out, c)
			}
			if n.dep != coDepNone {
				c := d.clone()
				t := coNodesOf(c)[i]
				t.dep, t.depTrig, t.depOn = coDepNone, "", nil
				out = append(out, c)
			}
			if n.minProps {
				c := d.clone()
				coNodesOf(c)[i].minProps = false
				out = append(out, c)
			}
			// Drop the applicator and declare everything inline again. This is
			// the reduction that tells the two apart: if the failure survives
			// it, composition was not what broke.
			if n.comp != coCompNone {
				c := d.clone()
				t := coNodesOf(c)[i]
				t.comp = coCompNone
				t.groups = 0
				t.branches = nil
				t.branchIx = 0
				for _, p := range t.props {
					p.group = 0
				}
				out = append(out, c)
			}
			// Narrow a discriminated anyOf/oneOf to two branches, keeping the
			// one the instance matches.
			if len(n.branches) > 2 {
				c := d.clone()
				t := coNodesOf(c)[i]
				keep := t.branches[t.branchIx]
				drop := t.branches[(t.branchIx+1)%len(t.branches)]
				t.branches = []*coDisc{keep, drop}
				t.branchIx = 0
				out = append(out, c)
			}
			for j := range n.props {
				if len(n.props) > 1 {
					c := d.clone()
					t := coNodesOf(c)[i]
					t.props = append(append([]*coProp(nil), t.props[:j]...), t.props[j+1:]...)
					out = append(out, c)
				}
				// Make a required property optional, then absent.
				if n.props[j].required {
					c := d.clone()
					coNodesOf(c)[i].props[j].required = false
					out = append(out, c)
				}
				if !n.props[j].required && n.props[j].present {
					c := d.clone()
					coNodesOf(c)[i].props[j].present = false
					out = append(out, c)
				}
				// Collapse a composite property to a scalar leaf.
				switch n.props[j].node.kind {
				case coObject, coArray, coTuple, coMap, coRef, coAltAnyOf, coOneOfWin, coIfElse, coNot, coOneOfObj:
					c := d.clone()
					coNodesOf(c)[i].props[j].node = &coNode{kind: coBoolean}
					out = append(out, c)
				}
			}

		case coArray:
			if n.unique {
				c := d.clone()
				coNodesOf(c)[i].unique = false
				out = append(out, c)
			}
			if n.contains {
				c := d.clone()
				coNodesOf(c)[i].contains = false
				out = append(out, c)
			}
			if n.minItems != nil {
				c := d.clone()
				coNodesOf(c)[i].minItems = nil
				out = append(out, c)
			}
			if n.maxItems != nil {
				c := d.clone()
				coNodesOf(c)[i].maxItems = nil
				out = append(out, c)
			}
			// A contains array stops conforming once fewer elements are
			// numbered at or above the threshold than its own minContains, and
			// its mutants address indices either side of that threshold, so it
			// shrinks no further than one matching element.
			shorter := n.numItems > 0 && (n.minItems == nil || n.numItems > *n.minItems)
			if n.contains && n.numItems <= n.containsMin+1 {
				shorter = false
			}
			if shorter {
				c := d.clone()
				coNodesOf(c)[i].numItems--
				out = append(out, c)
			}

		case coMap:
			// Fewer keys is the same map with the same value schema behind it.
			// The mutation follows the last key, so dropping keys from the front
			// keeps the mutated one in place.
			if len(n.mapKeys) > 1 {
				c := d.clone()
				t := coNodesOf(c)[i]
				t.mapKeys = t.mapKeys[1:]
				out = append(out, c)
			}
			// The non-nullable spelling is the simpler of the two and the one
			// whose typing was already fixed, so a failure that survives here is
			// a failure of the map itself rather than of the null beside it.
			if n.mapNullable {
				c := d.clone()
				coNodesOf(c)[i].mapNullable = false
				out = append(out, c)
			}

		case coTuple:
			// A shorter prefix is a smaller tuple with the same argument
			// behind it, so the reported case names as few positions as the
			// failure needs.
			if len(n.tupleTypes) > 1 {
				c := d.clone()
				t := coNodesOf(c)[i]
				t.tupleTypes = t.tupleTypes[:len(t.tupleTypes)-1]
				out = append(out, c)
			}

		case coString:
			if n.patIdx >= 0 {
				break
			}
			if n.emitMin {
				c := d.clone()
				t := coNodesOf(c)[i]
				t.emitMin = false
				t.emitMax = true
				out = append(out, c)
			}
			if n.emitMax {
				c := d.clone()
				t := coNodesOf(c)[i]
				t.emitMax = false
				t.emitMin = true
				out = append(out, c)
			}

		case coInteger, coNumber:
			// The short spelling is the one every draft admits, so a failure
			// that survives dropping the float notation is about the keyword
			// rather than about how the number was written.
			if n.intFloatToken {
				c := d.clone()
				coNodesOf(c)[i].intFloatToken = false
				out = append(out, c)
			}
			if n.minStyle != coBoundNone {
				c := d.clone()
				coNodesOf(c)[i].minStyle = coBoundNone
				out = append(out, c)
			}
			if n.maxStyle != coBoundNone {
				c := d.clone()
				coNodesOf(c)[i].maxStyle = coBoundNone
				out = append(out, c)
			}
			if n.emitMultipleOf {
				c := d.clone()
				coNodesOf(c)[i].emitMultipleOf = false
				out = append(out, c)
			}

		case coEnum:
			if len(n.choices) > 1 {
				c := d.clone()
				t := coNodesOf(c)[i]
				keep := t.choices[t.choiceIx]
				t.choices = []any{keep}
				t.choiceIx = 0
				out = append(out, c)
			}
		}
	}
	return out
}
