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
//	const           one string or integer member
//	$defs + $ref    0..3 definitions; a definition body is an object, a
//	                bounded string/integer/number, or an enum. Definition i
//	                may $ref definitions < i, so the graph is acyclic.
//
// Deliberately not emitted, because co-generating a *conforming* instance for
// them is its own project and a wrong instance produces false failures that
// burn the whole budget in triage: allOf / anyOf / oneOf, if/then/else, not,
// patternProperties, additionalProperties, propertyNames, minProperties /
// maxProperties, dependentRequired / dependentSchemas, unevaluated*, contains,
// prefixItems and tuple-form items, uniqueItems, format, multi-valued type,
// boolean schemas, recursive or remote $refs, $id / $anchor.
//
// Also not emitted, but for a different reason — see coKnownGaps below.

// coKnownGaps records constructs the grammar avoids because schemagen is
// already known to handle them incorrectly. They are excluded so the harness
// reports regressions rather than re-reporting the same four defects on every
// iteration, and each is reachable again by setting
// SCHEMAGEN_COGEN_INCLUDE_KNOWN_GAPS=1 so the exclusions stay verifiable
// instead of being claims in a comment. Every one of them was found by this
// harness during development; the minimal reproducers are in the doc comment
// of each toggle below.
var coIncludeKnownGaps = os.Getenv("SCHEMAGEN_COGEN_INCLUDE_KNOWN_GAPS") == "1"

// coGapItemConstraints: constraints on array items of *primitive* type are
// dropped. {"type":"array","items":{"type":"string","minLength":2}} emits
// []string, and nothing checks minLength — the generator only validates
// elements whose Go type is a named type carrying a Validate method (objects,
// enums, $ref-ed primitives). So array element nodes are restricted to kinds
// that produce a named type, or to primitives with no constraints to lose.
func coGapItemConstraints() bool { return coIncludeKnownGaps }

// coGapConstItems: a const in item position is dropped entirely.
// {"type":"array","items":{"const":5}} emits []any and a Validate that is
// nothing but `return nil` — the same schema written as
// {"type":"array","items":{"enum":[5]}} emits a named element type with a
// working Validate, so this is specific to const, not to items. Array element
// nodes therefore never use const.
//
//	schema   {"type":"object","properties":{"a":{"type":"array","items":{"const":5}}}}
//	instance {"a":[9998]}   accepted
func coGapConstItems() bool { return coIncludeKnownGaps }

// coGapAbsentNull: an optional {"type":"null"} property emits *any with a
// plain `json:"nothing"` tag — no omitempty — so a property the input omitted
// comes back as an explicit null and the round-trip fails. The grammar
// therefore always puts null-typed properties in the instance.
//
//	schema   {"type":"object","properties":{"n":{"type":"null"}}}
//	instance {}
//	marshals {"n":null}
func coGapAbsentNull() bool { return coIncludeKnownGaps }

// coGapNullType: {"type":"null"} is not enforced at all. The field is *any,
// which accepts any JSON value, and no validation rule is emitted, so no
// mutation of a null-typed value can be rejected.
//
//	schema   {"type":"object","properties":{"n":{"type":"null"}},"required":["n"]}
//	instance {"n":123}   accepted by both UnmarshalJSON and Validate
func coGapNullType() bool { return coIncludeKnownGaps }

// coGapZeroNamedPrimitive: an optional property whose Go type is a *named*
// primitive (a $ref to a string/integer/number definition, or an inline
// enum/const) is emitted as a non-pointer with omitempty, and its Validate
// call is guarded by a `!= <zero>` test. A Go zero value therefore both
// disappears from the marshalled output and escapes validation. The grammar
// keeps such nodes away from the zero value: primitive definitions use
// minLength >= 2 or a range whose low end is at least 3 steps above zero, and
// enum/const members are never "", 0 or false.
//
//	schema   {"$defs":{"C":{"type":"integer","minimum":0}},
//	          "type":"object","properties":{"c":{"$ref":"#/$defs/C"}}}
//	instance {"c":0}
//	marshals {}                      // round-trip loses the property
//	          and Validate skips the minimum check for the same reason
//
// Unlike the other three, the toggle only makes this *reachable*, not
// frequent: it needs a primitive definition, referenced from an optional
// property, whose value lands exactly on the Go zero, and 400 iterations of
// the widened grammar did not produce one. The reproducer above is the
// demonstration; the toggle is not.
func coGapZeroNamedPrimitive() bool { return coIncludeKnownGaps }

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
	step             float64
	lo, hi           float64
	minStyle         coBoundStyle
	maxStyle         coBoundStyle
	emitMultipleOf   bool
	numValue         float64
	positiveRequired bool

	// boolean
	boolValue bool

	// enum / const
	choices  []any
	choiceIx int
	offValue any

	// ref
	refName string
}

type coProp struct {
	name     string
	required bool
	present  bool
	node     *coNode
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

var coEnumWords = []string{"red", "green", "blue", "amber", "cyan", "teal"}

// coEnumInts are distinct and positive: a zero member would be the Go zero of
// the generated named type, which is the trap coGapZeroNamedPrimitive
// describes.
var coEnumInts = []int64{3, 5, 7, 11, 13, 17}

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

// buildDefBody builds the body of a $defs entry. Primitive bodies become named
// Go types whose optional uses carry omitempty and a zero-value guard, so they
// are built to stay clear of the Go zero — see coGapZeroNamedPrimitive.
func (b *coBuilder) buildDefBody(depth, visible int) *coNode {
	switch b.rng.IntN(5) {
	case 0, 1:
		return b.buildObject(depth, visible)
	case 2:
		return b.buildString(!coGapZeroNamedPrimitive())
	case 3:
		if b.chance(2) {
			return b.buildNumeric(coInteger, !coGapZeroNamedPrimitive())
		}
		return b.buildNumeric(coNumber, !coGapZeroNamedPrimitive())
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
		// An absent null-typed property comes back as an explicit null and
		// breaks the round-trip; see coGapAbsentNull.
		if child.kind == coNull && !coGapAbsentNull() {
			p.present = true
		}
		n.props = append(n.props, p)
	}
	return n
}

// buildValue picks the schema for an object property.
func (b *coBuilder) buildValue(depth, visible int) *coNode {
	kinds := []coKind{coString, coInteger, coNumber, coBoolean, coNull, coEnum, coConst}
	if depth < coMaxDepth {
		kinds = append(kinds, coObject, coArray)
	}
	if visible > 0 {
		kinds = append(kinds, coRef)
	}
	switch kinds[b.rng.IntN(len(kinds))] {
	case coObject:
		return b.buildObject(depth, visible)
	case coArray:
		return b.buildArray(depth, visible)
	case coString:
		return b.buildString(false)
	case coInteger:
		return b.buildNumeric(coInteger, false)
	case coNumber:
		return b.buildNumeric(coNumber, false)
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

// buildElem picks the schema for an array element. Constrained primitives are
// excluded: schemagen emits []string for them and validates nothing, so a
// mutant that violates the element constraint would be accepted through no
// fault of the harness. See coGapItemConstraints.
func (b *coBuilder) buildElem(depth, visible int) *coNode {
	kinds := []coKind{coString, coInteger, coNumber, coBoolean, coEnum}
	if coGapConstItems() {
		kinds = append(kinds, coConst)
	}
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
	case coInteger, coNumber, coString:
		if coGapItemConstraints() {
			switch {
			case b.chance(3):
				return b.buildString(false)
			case b.chance(2):
				return b.buildNumeric(coInteger, false)
			default:
				return b.buildNumeric(coNumber, false)
			}
		}
		// An unconstrained primitive has nothing for the missing per-element
		// validation to lose, so it stays in the grammar.
		switch b.rng.IntN(3) {
		case 0:
			return &coNode{kind: coString, patIdx: -1, lenHi: 6, strValue: coFillString(4)}
		case 1:
			return &coNode{kind: coInteger, step: 1, lo: 1, hi: 9, numValue: 4}
		default:
			return &coNode{kind: coNumber, step: 1, lo: 1, hi: 9, numValue: 4}
		}
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
func (b *coBuilder) buildString(avoidZero bool) *coNode {
	n := &coNode{kind: coString, patIdx: -1}
	if b.chance(3) {
		n.patIdx = b.rng.IntN(len(coPatterns))
		p := coPatterns[n.patIdx]
		n.strValue = p.good[b.rng.IntN(len(p.good))]
		return n
	}
	n.lenLo = b.rng.IntN(5)
	if avoidZero && n.lenLo < 2 {
		// A shorter window would let the minLength mutant land on "", the Go
		// zero of the named type; see coGapZeroNamedPrimitive.
		n.lenLo = 2
	}
	n.lenHi = n.lenLo + b.rng.IntN(7)
	n.emitMin = n.lenLo > 0 || b.chance(2)
	n.emitMax = b.chance(2)
	if avoidZero {
		n.emitMin = true
	}
	if !n.emitMin && !n.emitMax {
		n.emitMax = true
	}
	n.strValue = coFillString(n.lenLo + b.rng.IntN(n.lenHi-n.lenLo+1))
	return n
}

// buildNumeric lays the node out on a lattice so that every mutant violates
// exactly one keyword: the minimum mutant is a lattice point one step below
// the bound (so multipleOf still holds), and the multipleOf mutant sits inside
// the bounds but off the lattice.
func (b *coBuilder) buildNumeric(kind coKind, positive bool) *coNode {
	n := &coNode{kind: kind, positiveRequired: positive}
	if kind == coInteger {
		n.step = []float64{1, 1, 2, 3, 5}[b.rng.IntN(5)]
	} else {
		n.step = []float64{0.5, 1, 1, 2.5}[b.rng.IntN(4)]
	}
	// The span is at least four steps so that both bounds can be exclusive and
	// still leave lattice points, and so the off-lattice multipleOf mutant fits.
	span := float64(4+b.rng.IntN(9)) * n.step
	if positive {
		// Three steps of clearance keeps every mutant — including
		// minimum-minus-one-step — strictly above zero.
		n.lo = float64(3+b.rng.IntN(18)) * n.step
	} else {
		n.lo = float64(b.rng.IntN(41)-20) * n.step
	}
	n.hi = n.lo + span

	n.minStyle = []coBoundStyle{coBoundNone, coBoundInclusive, coBoundInclusive, coBoundExclusive}[b.rng.IntN(4)]
	n.maxStyle = []coBoundStyle{coBoundNone, coBoundInclusive, coBoundInclusive, coBoundExclusive}[b.rng.IntN(4)]
	if positive && n.minStyle == coBoundNone {
		n.minStyle = coBoundInclusive
	}
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

// buildConst never picks a boolean. A const of true emits a named bool type
// whose only non-conforming value is false, which is the Go zero — the
// optional-property guard would skip the check. See coGapZeroNamedPrimitive.
func (b *coBuilder) buildConst() *coNode {
	n := &coNode{kind: coConst}
	if b.chance(2) {
		n.choices = []any{coEnumWords[b.rng.IntN(len(coEnumWords))]}
		n.offValue = coOffString
	} else {
		n.choices = []any{coEnumInts[b.rng.IntN(len(coEnumInts))]}
		n.offValue = coOffInt
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
		props := map[string]any{}
		var req []string
		for _, p := range n.props {
			props[p.name] = p.node.fragment()
			if p.required {
				req = append(req, p.name)
			}
		}
		m["properties"] = props
		if len(req) > 0 {
			sort.Strings(req)
			m["required"] = req
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
	}
	return m
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
}

func (m coMutation) key() string {
	var b strings.Builder
	b.WriteString(m.Keyword)
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
		}
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
		// {"type":"null"} emits *any and no validation rule, so there is no
		// edit a conforming generator could make that Validate would reject.
		// See coGapNullType.
		if coGapNullType() {
			*out = append(*out, coMutation{
				Keyword: "type", Path: path, Prop: prop, Value: 123,
				Want: []string{prop},
			})
		}

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
	}
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
					if n.props[j].node.kind != coNull || coGapAbsentNull() {
						c := d.clone()
						coNodesOf(c)[i].props[j].present = false
						out = append(out, c)
					}
				}
				// Collapse a composite property to a scalar leaf.
				switch n.props[j].node.kind {
				case coObject, coArray, coRef:
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
