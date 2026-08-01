package tests

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
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
//	root            always {"type":"object"} with 1..4 properties
//	object          type, properties (1..4), required (any subset)
//	array           type, items (a single schema), minItems, maxItems
//	string          type, and either {minLength, maxLength} or pattern
//	integer         type, minimum | exclusiveMinimum, maximum |
//	                exclusiveMaximum, multipleOf
//	number          as integer, on a fractional lattice (0.5 / 2.5)
//	boolean         type
//	null            type
//	enum            2..4 members, all strings or all integers
//	const           one string, integer or boolean member
//	$defs + $ref    0..3 definitions; a definition body is an object, a
//	                bounded string/integer/number, or an enum. Definition i
//	                may $ref definitions < i, so the graph is acyclic.
//	allOf           on an object node: its properties, and the `required`
//	                entries that go with them, are partitioned across 1..2
//	                allOf branches instead of being declared inline
//	anyOf           two forms. On an object node, 2..3 branches each keyed by
//	                a discriminator property that is `required` in that
//	                branch and typed there. On a property, two typed scalar
//	                alternatives ({"type":"string","minLength":n} and
//	                {"type":"integer","minimum":m}).
//	oneOf           two forms. On the *root* object, the same discriminator
//	                branches as anyOf (only the root: see
//	                coGapNestedOneOfDropsSiblings). As the whole document, two
//	                overlapping integer windows, so that a value matching
//	                *both* branches is reachable and can be made a mutant.
//	if/then/else    as the whole document, over an integer pivot: `if`
//	                {minimum: P}, `then` {maximum: P+K}, `else` {minimum:
//	                P-K}. Both outcomes of `if` are generated.
//	not             as the whole document, {"not":{"type":T}} over a value of
//	                some other type.
//
// Where composition sits is not a matter of taste. schemagen enforces the same
// keyword in some positions and not others (see coKnownGaps), and the grammar
// is placed to land on the positions that are enforced: object-level allOf /
// anyOf / oneOf get flattened into the struct and checked against the raw JSON
// keys, an inline anyOf of typed scalars becomes an alternatives wrapper whose
// Validate the parent calls, and a document *rooted* at oneOf / if / not
// becomes the root type, whose Validate the harness calls directly.
//
// Deliberately not emitted, because co-generating a *conforming* instance for
// them is its own project and a wrong instance produces false failures that
// burn the whole budget in triage: patternProperties, additionalProperties,
// propertyNames, minProperties / maxProperties, dependentRequired /
// dependentSchemas, unevaluated*, contains, prefixItems and tuple-form items,
// uniqueItems, format, multi-valued type, boolean schemas, recursive or remote
// $refs, $id / $anchor.
//
// Also not emitted, but for a different reason — see coKnownGaps below.

// ---------------------------------------------------------------------------
// Known gaps
// ---------------------------------------------------------------------------
//
// coKnownGaps records constructs the grammar avoids because schemagen is
// already known to handle them incorrectly. They are excluded so the harness
// reports regressions rather than re-reporting the same defects on every
// iteration, and each is reachable again by setting
// SCHEMAGEN_COGEN_INCLUDE_KNOWN_GAPS=1 so the exclusions stay verifiable
// instead of being claims in a comment. The minimal reproducer for each is in
// the doc comment of its toggle.
var coIncludeKnownGaps = os.Getenv("SCHEMAGEN_COGEN_INCLUDE_KNOWN_GAPS") == "1"

// coGapWrapperValidateNotCalled: a property whose type is one of the "raw JSON
// wrapper" types -- the one generated for a `not` schema (NotSchemaDef) and the
// one generated for a schema whose only keywords are oneOf / anyOf / if / then
// / else (DynamicSchemaDef) -- never has its Validate() called by the enclosing
// struct. Both types carry a correct Validate of their own;
// populateValidatableFields simply does not count them as validatable, so
// nothing invokes it and the constraint is dead at every position except the
// document root.
//
//	schema   {"$defs":{"NotInt":{"not":{"type":"integer"}}},
//	          "type":"object","properties":{"a":{"$ref":"#/$defs/NotInt"}},
//	          "required":["a"]}
//	instance {"a":7}                 accepted; NotInt.Validate() would reject it
//
// The same schema written with the `not` at the document root is rejected
// correctly, which is why the grammar puts `not`, root-level oneOf and
// if/then/else there. This toggle instead hangs them off a property through
// $defs, which is where they die.
func coGapWrapperValidateNotCalled() bool { return coIncludeKnownGaps }

// coGapInlineConditionalDropped: `not` and `if`/`then`/`else` written inline as
// a property's own schema are dropped before any type is chosen -- the property
// becomes a bare `any` with no Validate at all. This is a different defect from
// coGapWrapperValidateNotCalled: there a correct wrapper type exists and is
// never consulted, here no wrapper is generated in the first place.
//
//	schema   {"type":"object","properties":{"a":{"not":{"type":"integer"}}},
//	          "required":["a"]}
//	instance {"a":7}                 accepted; the field is `any`
//
//	schema   {"type":"object",
//	          "properties":{"a":{"if":{"minimum":10},"then":{"maximum":20}}},
//	          "required":["a"]}
//	instance {"a":99}                accepted; the field is `any`
//
// The object-level spelling is dropped too: an `if`/`then`/`else` beside an
// object's `properties` produces no check anywhere in the generated Validate.
//
//	schema   {"type":"object","properties":{"kind":{"type":"string"},
//	          "a":{"type":"string"}},"required":["kind","a"],
//	          "if":{"properties":{"kind":{"const":"x"}},"required":["kind"]},
//	          "then":{"properties":{"a":{"minLength":5}}}}
//	instance {"kind":"x","a":"ab"}   accepted
func coGapInlineConditionalDropped() bool { return coIncludeKnownGaps }

// coGapScalarAllOfDropped: an allOf whose branches constrain a *scalar*
// property is dropped entirely. The branch keywords never reach the field's
// validation, so the property is emitted as a plain Go string/int64 with no
// check. The object-level spelling -- branches that carry `properties` and
// `required` -- is flattened correctly, which is the form the grammar emits.
//
//	schema   {"type":"object",
//	          "properties":{"a":{"type":"string",
//	                             "allOf":[{"minLength":3},{"maxLength":10}]}},
//	          "required":["a"]}
//	instance {"a":"z"}               accepted
func coGapScalarAllOfDropped() bool { return coIncludeKnownGaps }

// coGapOneOfVariantConstraints: an inline oneOf of typed scalars on a property
// becomes a sealed-interface union whose variant selection happens in
// UnmarshalJSON and considers only whether the raw JSON decodes into the
// variant's Go type. A branch's own constraints are not consulted, so a value
// that decodes into one variant but violates that variant's constraints matches
// it anyway, and nothing downstream rechecks. The grammar reaches oneOf over
// scalars through the document root instead, where the dynamic evaluator does
// apply the branch constraints.
//
//	schema   {"type":"object",
//	          "properties":{"a":{"oneOf":[{"type":"string","minLength":3},
//	                                      {"type":"integer","minimum":5}]}},
//	          "required":["a"]}
//	instance {"a":"z"}               accepted
func coGapOneOfVariantConstraints() bool { return coIncludeKnownGaps }

// coGapNestedOneOfDropsSiblings: a *property* whose schema is an object with
// both its own `properties`/`required` and a `oneOf` is generated as a sealed
// interface over the oneOf branches alone. The object's own properties never
// appear in any generated type, so every constraint they carried is gone --
// including `required`. The same schema at the document root is flattened
// correctly and keeps both, which is where the grammar puts it.
//
//	schema   {"type":"object","properties":{
//	           "f":{"type":"object","properties":{"h":{"type":"boolean"}},
//	                "required":["h"],
//	                "oneOf":[{"properties":{"tagOne":{"type":"integer"}},
//	                          "required":["tagOne"]},
//	                         {"properties":{"tagTwo":{"type":"string"}},
//	                          "required":["tagTwo"]}]}}}
//	instance {"f":{"tagOne":41}}     accepted, though "h" is required
//
// The generated type for "f" is an interface over RootFOption0{TagOne} and
// RootFOption1{TagTwo}; neither mentions "h". anyOf in the same position is
// flattened correctly, so this is specific to oneOf.
func coGapNestedOneOfDropsSiblings() bool { return coIncludeKnownGaps }

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

	// Composition leaves. Each is a whole schema whose only keywords are
	// applicators, so each is a value the grammar picks first and a schema it
	// derives from that value.
	coAltAnyOf // anyOf (or, under a known-gap toggle, oneOf) over two typed scalars
	coOneOfWin // oneOf over two overlapping integer windows
	coIfElse   // if/then/else over an integer pivot
	coNot      // not: {"type": T}
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

	// array
	elem     *coNode
	numItems int
	minItems *int
	maxItems *int

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
	// "anyOf"; it is only reachable under coGapOneOfVariantConstraints.
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
	winLo0, winHi0 int64
	winLo1, winHi1 int64
	winValue       int64

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

	// scalarAllOf moves a string node's length keywords into an allOf. Only
	// reachable under coGapScalarAllOfDropped.
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
	// One document in four is rooted at a composition leaf rather than at an
	// object. That is the only position where schemagen enforces `not`,
	// if/then/else and a oneOf over scalars at all (see
	// coGapWrapperValidateNotCalled), so it is the only position from which a
	// mutant of those keywords can be expected to be rejected.
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
	// A composition wrapper reached through a $ref is a known gap: the wrapper
	// type carries a correct Validate that nothing calls.
	if coGapWrapperValidateNotCalled() && b.chance(3) {
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
		p.present = p.required || !b.chance(4)
		n.props = append(n.props, p)
	}
	b.applyComposition(n, depth)
	return n
}

// applyComposition decides whether an object routes its properties through an
// allOf, or grows a discriminated anyOf / oneOf beside them.
//
// The instance is not touched by the allOf case at all: moving a property's
// declaration into a branch does not change which values satisfy the document,
// because allOf is a conjunction and the partition is a partition. The anyOf
// and oneOf cases add exactly one property to the instance -- the discriminator
// of the branch the document is meant to match.
func (b *coBuilder) applyComposition(n *coNode, depth int) {
	// A discriminated oneOf only survives generation at the document root; see
	// coGapNestedOneOfDropsSiblings.
	rootObject := depth == 0
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
		if !rootObject && !coGapNestedOneOfDropsSiblings() {
			n.comp = coCompAnyOf
			b.addDiscriminators(n)
			return
		}
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
		return b.buildOneOfWin()
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
func (b *coBuilder) buildOneOfWin() *coNode {
	n := &coNode{kind: coOneOfWin}
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

// buildValue picks the schema for an object property.
func (b *coBuilder) buildValue(depth, visible int) *coNode {
	kinds := []coKind{coString, coInteger, coNumber, coBoolean, coNull, coEnum, coConst, coAltAnyOf}
	if depth < coMaxDepth {
		kinds = append(kinds, coObject, coArray)
	}
	if visible > 0 {
		kinds = append(kinds, coRef)
	}
	// Composition leaves that only work at the document root, reachable here
	// only to keep their exclusion executable.
	if coGapInlineConditionalDropped() {
		kinds = append(kinds, coIfElse, coNot)
	}
	switch kinds[b.rng.IntN(len(kinds))] {
	case coObject:
		return b.buildObject(depth, visible)
	case coArray:
		return b.buildArray(depth, visible)
	case coAltAnyOf:
		return b.buildAltAnyOf(coGapOneOfVariantConstraints() && b.chance(2))
	case coIfElse:
		return b.buildIfElse()
	case coNot:
		return b.buildNot()
	case coString:
		return b.buildString()
	case coInteger:
		return b.buildNumeric(coInteger)
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
		kinds = append(kinds, coObject)
	}
	if visible > 0 {
		kinds = append(kinds, coRef)
	}
	switch kinds[b.rng.IntN(len(kinds))] {
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
	n.scalarAllOf = coGapScalarAllOfDropped() && b.chance(2)
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
		if props, req := coDeclare(groups[0]); len(props) > 0 {
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
	case coArray:
		m["type"] = "array"
		m["items"] = n.elem.fragment()
		if n.minItems != nil {
			m["minItems"] = *n.minItems
		}
		if n.maxItems != nil {
			m["maxItems"] = *n.maxItems
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
		m["oneOf"] = []any{
			map[string]any{"type": "integer", "minimum": n.winLo0, "maximum": n.winHi0},
			map[string]any{"type": "integer", "minimum": n.winLo1, "maximum": n.winHi1},
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
		return m
	case coArray:
		return d.arrayValue(n, n.numItems)
	case coString:
		return n.strValue
	case coInteger:
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
		out = append(out, d.value(n.elem))
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
		if n.numItems > 0 {
			d.collect(n.elem, coPath(path, 0), prop, out)
		}
		if len(path) > 0 {
			*out = append(*out, coMutation{
				Keyword: "type", Path: path, Prop: prop, Value: 7, Loose: true,
			})
		}

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
		// The rejection is reported as a type failure ("string is not
		// allowed"), because the wrapper's last act, once no alternative has
		// matched, is to name the JSON type it was handed. That is the message
		// to assert on; asserting on the word "anyOf" would be asserting on a
		// message the generator never emits here.
		what := "anyOf"
		if n.altOneOf {
			what = "oneOf"
		}
		*out = append(*out, coMutation{
			Keyword: what + "AllBranchesString", Path: path, Prop: prop,
			Value: coFillString(n.altStrMin - 1),
			Want:  []string{prop, "is not allowed"},
		})
		*out = append(*out, coMutation{
			Keyword: what + "AllBranchesNumber", Path: path, Prop: prop,
			Value: n.altIntMin - 1,
			Want:  []string{prop, "is not allowed"},
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
				case coObject, coArray, coRef, coAltAnyOf, coOneOfWin, coIfElse, coNot:
					c := d.clone()
					coNodesOf(c)[i].props[j].node = &coNode{kind: coBoolean}
					out = append(out, c)
				}
			}

		case coArray:
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
			if n.numItems > 0 && (n.minItems == nil || n.numItems > *n.minItems) {
				c := d.clone()
				coNodesOf(c)[i].numItems--
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
