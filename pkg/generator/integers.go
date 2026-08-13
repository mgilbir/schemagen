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

// LeafDecodeDef says how one decode position recovers its declared value from
// JSON that encoding/json would otherwise read into the wrong thing.
//
// Two leaves need it, for the same structural reason and with the same remedy.
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

	// Integers and Numbers say which leaves this decode replaced. Both may be
	// true -- {"type":"array","items":{"type":["integer","number"]}} does not
	// produce one, but a struct holding an integer property and a number one
	// produces a decode of each, and the emitted commentary has to say which
	// question it is answering. Nothing else reads them: the shadow and the
	// conversion above are what the decode is made of.
	Integers bool
	Numbers  bool
}

// leafShadowVar is the variable the Convert expression reads. Every site that
// emits a LeafDecodeDef binds a value of ShadowType to this name first, so one
// expression serves all of them.
const leafShadowVar = "_iv"

// shadowLeaves says which leaves a shadow walk is allowed to replace.
//
// The two are decided separately because they are refused separately. A draft
// that requires an integer *token* -- draft 3 and draft 4, where 1.0 is not an
// integer -- wants the plain int64 decode and no shadow, while a json.Number in
// the same type still has to be kept from taking a JSON string. Reading them as
// one flag made a draft-4 schema with both kinds of leaf give up the number's
// guard to keep the integer's.
type shadowLeaves struct {
	integers bool
	numbers  bool
}

func (l shadowLeaves) none() bool { return !l.integers && !l.numbers }

// leafShadowType replaces every leaf the walk is asked for with its shadow
// type, and reports whether the type held any.
//
// The walk descends only through the containers encoding/json decodes
// structurally -- a pointer, a slice, a map -- because those are the shapes
// where the leaf is reached by the *outer* decode and so has no chance to speak
// for itself. It deliberately stops at a NamedType: a named integer, a named
// number, a named enum and a named struct each carry their own UnmarshalJSON,
// and substituting one would both fail to compile and throw away the behaviour
// that type was generated to have.
func leafShadowType(t GoType, want shadowLeaves) (GoType, bool) {
	switch v := t.(type) {
	case *PrimitiveType:
		if want.integers && v.Name == "int64" {
			return &PrimitiveType{Name: integerShadowName}, true
		}
		if want.numbers && v.Name == GoNumberTypeName {
			return &PrimitiveType{Name: numberShadowName}, true
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
// shadow, and a json.Number in the same type still gets its own.
func (g *Generator) leafDecodeFor(t GoType, s *schema.Schema) *LeafDecodeDef {
	if t == nil {
		return nil
	}
	want := shadowLeaves{
		integers: !g.requiresStrictIntegerToken(s),
		numbers:  g.config.ExactNumbers,
	}
	if want.none() {
		return nil
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
	}
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
// container of integers or of exact numbers, and the overflow map of an object
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
// type over json.Number does not inherit.
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
