package generator

import (
	"fmt"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// integerShadowName is the helper type a schema integer is decoded through.
// It is an int64 with an UnmarshalJSON of its own, which is the only way to
// reach encoding/json's decision about a number token: that decision is made
// from the destination Go type, and a bare int64 refuses 1.0 outright.
const integerShadowName = "jsonInteger"

// numberShadowName is the helper type a schema number is decoded through under
// Config.ExactNumbers, and it is here for the mirror image of the reason above.
// The declared type is json.Number, which encoding/json fills from a JSON
// *string* as readily as from a number -- {"n":"1.5"} would satisfy
// {"type":"number"} -- and a type of our own is the only place that decision
// can be intercepted.
const numberShadowName = "jsonNumber"

// dateTimeShadowName is the helper type a `format: date-time` is decoded
// through, and it is here because time.Time's decoder is stricter than the
// format it names. RFC 3339 section 5.6 admits a lower case "t" between the
// date and the time and a lower case "z" for the UTC offset -- its ABNF says so
// in a NOTE -- and time.Time is read from a parse layout, which matches both
// characters literally. {"a":"2020-01-02t03:04:05z"} came back as `cannot parse
// "t03:04:05z" as "T"`: a document the format permits, refused at decode. That
// verdict is made from the destination Go type and can be intercepted nowhere
// else. See issue #264.
const dateTimeShadowName = "jsonDateTime"

// dateTimeGoTypeName is the Go type a `format: date-time` maps to, and so the
// leaf the walk below looks for. Written once here rather than quoted at each
// of the three places that ask, which is how the shadow and the conversion
// stay descriptions of the same leaf.
const dateTimeGoTypeName = "time.Time"

// ipv4ShadowName and ipv6ShadowName are the types an asserted `format: ipv4` or
// `format: ipv6` is decoded through, and they are here for the reason
// jsonDateTime is: the Go type the format maps to refuses a value in words of
// its own, and those words are the parser's rather than the schema's.
//
// netip.Addr is filled through encoding.TextUnmarshaler, whose error
// encoding/json passes back untouched -- so `{"a":"nope"}` came back as
// `ParseAddr("nope"): unable to parse IP`: no path, no keyword, and no mention
// of the format the document broke. The same format one sibling string keyword
// away is held as a string and answered with `"nope" is not a valid IPv4
// address`, which the generator already knows how to write. See issue #282.
//
// Two shadows rather than one because the two messages differ, and the message
// is the whole point: which of them a position takes is decided from the format
// at that position (see leafIPFormat). Neither shadow judges the address family
// -- ParseAddr accepts "::1" for both -- because that verdict is Validate's, and
// moving it here would change what the decode refuses.
const (
	ipv4ShadowName = "jsonIPv4Addr"
	ipv6ShadowName = "jsonIPv6Addr"
)

// ipAddrGoTypeName is the Go type an asserted ipv4 or ipv6 format maps to. See
// formatGoType, which is where the mapping is made.
const ipAddrGoTypeName = "netip.Addr"

// ipShadowName is the shadow one of the two ip formats is decoded through.
func ipShadowName(format string) string {
	if format == "ipv6" {
		return ipv6ShadowName
	}
	return ipv4ShadowName
}

// LeafDecodeDef says how one decode position recovers its declared value from
// JSON that encoding/json would otherwise read into the wrong thing.
//
// Three leaves need it, for the same structural reason and with the same remedy.
//
// An int64: from draft 6 onwards a number with a zero fractional part *is* an
// integer, so {"n":1.0} satisfies {"type":"integer"} and every independent
// implementation agrees. encoding/json disagrees -- it refuses 1.0 for an int64
// destination -- so the generated code has to decode through a type that
// answers for itself. A named integer type already did (see the alias
// template's IsIntegerType arm), which is why the same schema used to be
// accepted at a document root and rejected one level down under a property:
// issue #90.
//
// A json.Number, which is what Config.ExactNumbers holds a "number" as: it is a
// string underneath, and encoding/json will fill one from a JSON string. Left
// alone, {"n":"1.5"} would satisfy {"type":"number"} -- a document the schema
// forbids, accepted and then written back out unquoted as a number nobody sent.
//
// A time.Time, which is what an asserted `format: date-time` maps to: its
// decoder refuses the lower case "t" and "z" spellings RFC 3339 permits, so a
// document the format allows was refused. Same shape of defect, same only
// possible cure. See dateTimeShadowName and issue #264.
//
// ShadowType is the position's Go type with every such leaf replaced by the
// helper type that answers for itself, so encoding/json performs the whole
// decode -- nesting, nils, nulls, absence -- and only the leaves behave
// differently. Convert is the expression that turns the decoded shadow value,
// named by leafShadowVar, back into the declared type. The two are always built
// together and always from the same GoType, so they cannot describe different
// shapes.
type LeafDecodeDef struct {
	ShadowType GoType // the position's type, with its leaves replaced by shadows
	Convert    string // expression over leafShadowVar producing the declared type

	// Integers, Numbers and DateTimes say which leaves this decode replaced.
	// More than one may be true -- {"type":"array","items":{"type":["integer",
	// "number"]}} does not produce one, but a struct holding an integer
	// property, a number one and a date-time one produces a decode of each, and
	// the emitted commentary has to say which question it is answering. Nothing
	// else reads them: the shadow and the conversion above are what the decode
	// is made of.
	Integers  bool
	Numbers   bool
	DateTimes bool
	// IPAddrs says this decode replaced a netip.Addr leaf. Unlike the three
	// above it does not select a sentence of commentary -- the shadow changes
	// only the words a bad address is refused in, and the decode it performs is
	// the one that always ran -- but it is recorded beside them so that a
	// position's leaves are described in one place.
	IPAddrs bool
}

// leafShadowVar is the variable the Convert expression reads. Every site that
// emits a LeafDecodeDef binds a value of ShadowType to this name first, so one
// expression serves all of them.
const leafShadowVar = "_iv"

// shadowLeaves says which leaves a shadow walk is allowed to replace.
//
// The three are decided separately because they are refused separately, and no
// two of the conditions are the same one. A draft that requires an integer
// *token* -- draft 3 and draft 4, where 1.0 is not an integer -- wants the plain
// int64 decode and no shadow, while a json.Number in the same type still has to
// be kept from taking a JSON string. Reading those two as one flag made a
// draft-4 schema with both kinds of leaf give up the number's guard to keep the
// integer's.
//
// The date-time leaf is neither draft-conditional nor flag-conditional: no draft
// has ever meant a different RFC 3339 by `format: date-time`, and the mapping to
// time.Time is what a document is refused by, so wherever the mapping is taken
// the leaf is wanted. It matters that this is not the integer's condition --
// draft 3 and draft 4 are two of the five dialects that assert `format` by
// default, so a date-time leaf sharing the integer's gate would be missing on
// exactly the drafts the defect was worst on.
type shadowLeaves struct {
	integers  bool
	numbers   bool
	dateTimes bool
	// ipAddr is the ip format governing this position's netip.Addr leaf, and is
	// empty where the position has none. It is a format rather than a flag
	// because the two formats are refused in different words and so are decoded
	// through different shadows; the walk descends only containers, so one
	// position reaches at most one such leaf and one answer settles it. See
	// leafIPFormat.
	ipAddr string
}

// leafShadowType replaces every leaf the walk is asked for with its shadow
// type, and reports whether the type held any.
//
// The walk descends only through the containers encoding/json decodes
// structurally -- a pointer, a slice, a map -- because those are the shapes
// where the leaf is reached by the *outer* decode and so has no chance to speak
// for itself. It deliberately stops at a NamedType: a named integer, a named
// number, a named enum, a named struct and an alias over time.Time each carry
// their own UnmarshalJSON, and substituting one would both fail to compile and
// throw away the behaviour that type was generated to have.
func leafShadowType(t GoType, want shadowLeaves) (GoType, bool) {
	switch v := t.(type) {
	case *PrimitiveType:
		if want.integers && v.Name == "int64" {
			return &PrimitiveType{Name: integerShadowName}, true
		}
		if want.numbers && v.Name == GoNumberTypeName {
			return &PrimitiveType{Name: numberShadowName}, true
		}
		if want.dateTimes && v.Name == dateTimeGoTypeName {
			return &PrimitiveType{Name: dateTimeShadowName}, true
		}
		if want.ipAddr != "" && v.Name == ipAddrGoTypeName {
			return &PrimitiveType{Name: ipShadowName(want.ipAddr)}, true
		}
	case *PointerType:
		if inner, ok := leafShadowType(v.Inner, want); ok {
			return &PointerType{Inner: inner}, true
		}
	case *ArrayType:
		if item, ok := leafShadowType(v.ItemType, want); ok {
			return &ArrayType{ItemType: item}, true
		}
	case *MapType:
		if val, ok := leafShadowType(v.ValueType, want); ok {
			return &MapType{KeyType: v.KeyType, ValueType: val}, true
		}
	}
	return nil, false
}

// leafConvert builds the expression converting a shadow value back to the
// declared type. expr names the shadow value; depth keeps the closure
// parameters of nested levels from shadowing one another.
//
// A slice of jsonInteger is not convertible to a slice of int64 -- Go requires
// identical element types -- so each container level is rebuilt by a helper
// that preserves nil, which is what keeps an absent, a null and an empty
// collection as distinguishable after the change as they were before it. The
// same helpers rebuild a container of jsonNumber: they are written over two
// type parameters and care about the shape rather than the leaf.
func leafConvert(t GoType, expr string, depth int, want shadowLeaves) string {
	switch v := t.(type) {
	case *PrimitiveType:
		// The leaf. Its shadow's underlying type is the declared one, so the
		// conversion is the declared type's own name.
		if want.numbers && v.Name == GoNumberTypeName {
			return GoNumberTypeName + "(" + expr + ")"
		}
		if want.dateTimes && v.Name == dateTimeGoTypeName {
			return dateTimeGoTypeName + "(" + expr + ")"
		}
		if want.ipAddr != "" && v.Name == ipAddrGoTypeName {
			return ipAddrGoTypeName + "(" + expr + ")"
		}
		return "int64(" + expr + ")"
	case *PointerType:
		return leafConvertCall("jsonIntegerPtr", v.Inner, expr, depth, want)
	case *ArrayType:
		return leafConvertCall("jsonIntegerSlice", v.ItemType, expr, depth, want)
	case *MapType:
		return leafConvertCall("jsonIntegerMap", v.ValueType, expr, depth, want)
	}
	return expr
}

func leafConvertCall(helper string, elem GoType, expr string, depth int, want shadowLeaves) string {
	shadow, _ := leafShadowType(elem, want)
	param := fmt.Sprintf("_ix%d", depth)
	return fmt.Sprintf("%s(%s, func(%s %s) %s { return %s })",
		helper, expr, param, shadow.GoTypeName(), elem.GoTypeName(),
		leafConvert(elem, param, depth+1, want))
}

// leafDecodeFor builds the decode description for a position whose declared Go
// type is t and whose schema is s, or nil when the position needs nothing.
//
// Nothing is needed when the type holds no leaf the walk would replace. A draft
// that requires an integer *token* -- draft 3 and draft 4, where 1.0 is not an
// integer and the official suite's zeroTerminatedFloats says so -- is already
// served exactly right by the plain int64 decode, which refuses the float
// notation. Emitting the tolerant path there would not fix a defect, it would
// introduce one in the other direction; so that draft asks for no integer
// shadow, and a json.Number or a time.Time in the same type still gets its own.
//
// There is no "no leaf was asked for" exit here. There was one while the two
// conditional leaves were the only ones, and the date-time leaf is asked for
// unconditionally, so it could no longer be reached; what decides now is the
// walk's own answer, which is the question that was really being asked.
func (g *Generator) leafDecodeFor(t GoType, s *schema.Schema) *LeafDecodeDef {
	if t == nil {
		return nil
	}
	want := shadowLeaves{
		integers:  !g.requiresStrictIntegerToken(s),
		numbers:   g.config.ExactNumbers,
		dateTimes: true,
		ipAddr:    leafIPFormat(t, s),
	}
	shadow, ok := leafShadowType(t, want)
	if !ok {
		return nil
	}
	return &LeafDecodeDef{
		ShadowType: shadow,
		Convert:    leafConvert(t, leafShadowVar, 0, want),
		Integers:   want.integers && typeHoldsLeaf(t, "int64"),
		Numbers:    want.numbers && typeHoldsLeaf(t, GoNumberTypeName),
		DateTimes:  want.dateTimes && typeHoldsLeaf(t, dateTimeGoTypeName),
		IPAddrs:    want.ipAddr != "",
	}
}

// leafIPFormat is the ip format governing this position's netip.Addr leaf, or ""
// where the position holds none.
//
// The schema is descended in step with the Go type, through the same containers
// the shadow walk descends and no others, so what is found is the format of the
// leaf that will be replaced. Reading the format off the outermost schema would
// answer for the wrong position -- an array of ipv4 states the format on its
// items -- and searching the schema on its own would run into a $ref that
// contains itself. The Go type is finite by construction, so this walk ends.
//
// A nil schema, or a container whose element schema is absent, answers "": the
// position keeps the decode it had, which is the behaviour every position had
// before the shadow existed.
func leafIPFormat(t GoType, s *schema.Schema) string {
	if s == nil {
		return ""
	}
	switch v := t.(type) {
	case *PrimitiveType:
		if v.Name != ipAddrGoTypeName || s.Format == nil {
			return ""
		}
		if *s.Format == "ipv4" || *s.Format == "ipv6" {
			return *s.Format
		}
	case *PointerType:
		return leafIPFormat(v.Inner, s)
	case *ArrayType:
		if s.Items != nil && s.Items.Schema != nil {
			return leafIPFormat(v.ItemType, s.Items.Schema)
		}
	case *MapType:
		if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
			return leafIPFormat(v.ValueType, s.AdditionalProperties.Schema)
		}
	}
	return ""
}

// typeHoldsLeaf reports whether the type reaches a primitive of this name
// through the containers a shadow walk descends. It answers what the decode is
// *about*, which is what the emitted commentary needs and the shadow type on
// its own no longer says: a shadow built for two leaves names neither.
func typeHoldsLeaf(t GoType, name string) bool {
	switch v := t.(type) {
	case *PrimitiveType:
		return v.Name == name
	case *PointerType:
		return typeHoldsLeaf(v.Inner, name)
	case *ArrayType:
		return typeHoldsLeaf(v.ItemType, name)
	case *MapType:
		return typeHoldsLeaf(v.ValueType, name)
	}
	return false
}

// needsUnmarshalForLeafDecode reports whether a field's decode obliges its
// struct to carry an UnmarshalJSON, because the shadow and the conversion are
// emitted inside that method and nowhere else.
//
// No route reaches this today: every object struct already sets the flag on
// other grounds -- it rejects null, or it accepts non-object data, or it has an
// overflow map -- and neutering this function changes no golden, no regression
// and no co-generated iteration. It is kept so that the two are decided
// together rather than by coincidence: a struct that lost the flag for some
// other reason would otherwise keep the field's declared type and silently drop
// the decode that was built for it.
func needsUnmarshalForLeafDecode(f FieldDef) bool {
	return f.LeafDecode != nil
}

// resolveEnumIntegerTokens decides, for each const-form enum over int64, whether
// its draft admits a number written 1.0 for the member spelled 1 -- in which
// case the enum needs an UnmarshalJSON of its own, because a named type is just
// an int64 to encoding/json and an int64 refuses that spelling.
//
// It is the same question resolveLeafDecodes answers for the other type
// definitions, and asked from the same place (g.typeSchemas, which is what names
// a type's draft). It runs as a pass of its own only because the answer is a
// fact *about the enum's methods*, which populateAliasDelegates has to have
// before it can decide whether an alias over that enum must borrow them --
// and populateAliasDelegates in turn has to run before resolveLeafDecodes,
// which reads the UnmarshalAs it sets.
func (g *Generator) resolveEnumIntegerTokens() {
	for _, td := range g.output.TypeDefs {
		d, ok := td.(*EnumDef)
		if !ok {
			continue
		}
		// A const-form enum is a named type over its base type and carries no
		// UnmarshalJSON, so `type E int64` refuses 1.0 exactly as a bare
		// int64 field did. The raw form keeps the bytes and needs nothing.
		if d.IsRaw {
			continue
		}
		if pt, ok := d.BaseType.(*PrimitiveType); !ok || pt.Name != "int64" {
			continue
		}
		d.IntegerToken = !g.requiresStrictIntegerToken(g.typeSchemas[d.Name])
	}
}

// resolveLeafDecodes settles the positions that are decided from a whole type
// definition rather than from one property: a named type whose underlying is a
// container of integers, of exact numbers or of date-times, and the overflow map of an object
// whose values one sub-schema types. The integer enum is settled by
// resolveEnumIntegerTokens, which has to run earlier; see its comment.
//
// Both are the same defect as a struct field's -- a schema leaf reached by a
// decode that answers from the Go type -- and both are held here rather than at
// the many construction sites because the type's own schema, which is what
// names its draft, is on hand under g.typeSchemas.
//
// A named type whose underlying is a bare int64 is deliberately left alone: it
// already decodes through the alias template's IsIntegerType arm, which is
// draft-aware through StrictInteger and is the behaviour issue #90 measures
// every other position against. A named type over a bare json.Number is left
// alone for the mirror-image reason: the alias template's IsNumberType arm
// decodes it, and that arm is also what gives the type the MarshalJSON a named
// type over json.Number does not inherit. A named type over a bare time.Time is
// left alone here too, and reaches the shadow by a third route:
// populateAliasDelegates has already pointed its UnmarshalAs at jsonDateTime,
// and the UnmarshalAs arm runs before this one.
func (g *Generator) resolveLeafDecodes() {
	for _, td := range g.output.TypeDefs {
		switch d := td.(type) {
		case *AliasDef:
			if !d.CanHaveMethods() || d.IsIntegerType() || d.IsNumberType() || d.UnmarshalAs != "" {
				continue
			}
			d.LeafDecode = g.leafDecodeFor(d.Underlying, g.typeSchemas[d.Name])
		case *StructDef:
			// The overflow map of an object whose values one sub-schema types.
			// Settled here rather than beside each of the several places that
			// build an AdditionalPropertiesDef, so no route can acquire the map
			// without the decode that goes with its value type.
			if d.AdditionalProperties == nil {
				continue
			}
			d.AdditionalProperties.LeafDecode =
				g.leafDecodeFor(d.AdditionalProperties.ValueType, g.typeSchemas[d.Name])
		}
	}
}
