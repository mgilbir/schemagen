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

// IntegerDecodeDef says how one decode position recovers a value whose Go type
// holds int64 from JSON that may write those integers in float notation.
//
// From draft 6 onwards a number with a zero fractional part *is* an integer:
// {"n":1.0} satisfies {"type":"integer"} and every independent implementation
// agrees. encoding/json disagrees -- it refuses 1.0 for an int64 destination --
// so the generated code has to decode through a type that answers for itself.
// A named integer type already did (see the alias template's IsIntegerType
// arm), which is why the same schema used to be accepted at a document root
// and rejected one level down under a property: issue #90.
//
// ShadowType is the position's Go type with every int64 in it replaced by
// jsonInteger, so encoding/json performs the whole decode -- nesting, nils,
// nulls, absence -- and only the leaves behave differently. Convert is the
// expression that turns the decoded shadow value, named by integerShadowVar,
// back into the declared type. The two are always built together and always
// from the same GoType, so they cannot describe different shapes.
type IntegerDecodeDef struct {
	ShadowType GoType // the position's type, with int64 replaced by jsonInteger
	Convert    string // expression over integerShadowVar producing the declared type
}

// integerShadowVar is the variable the Convert expression reads. Every site
// that emits an IntegerDecodeDef binds a value of ShadowType to this name
// first, so one expression serves all of them.
const integerShadowVar = "_iv"

// integerShadowType replaces every int64 the type holds with the shadow type,
// and reports whether it held any.
//
// The walk descends only through the containers encoding/json decodes
// structurally -- a pointer, a slice, a map -- because those are the shapes
// where the leaf int64 is reached by the *outer* decode and so has no chance to
// speak for itself. It deliberately stops at a NamedType: a named integer, a
// named enum and a named struct each carry their own UnmarshalJSON, and
// substituting one would both fail to compile and throw away the behaviour that
// type was generated to have.
func integerShadowType(t GoType) (GoType, bool) {
	switch v := t.(type) {
	case *PrimitiveType:
		if v.Name == "int64" {
			return &PrimitiveType{Name: integerShadowName}, true
		}
	case *PointerType:
		if inner, ok := integerShadowType(v.Inner); ok {
			return &PointerType{Inner: inner}, true
		}
	case *ArrayType:
		if item, ok := integerShadowType(v.ItemType); ok {
			return &ArrayType{ItemType: item}, true
		}
	case *MapType:
		if val, ok := integerShadowType(v.ValueType); ok {
			return &MapType{KeyType: v.KeyType, ValueType: val}, true
		}
	}
	return nil, false
}

// integerConvert builds the expression converting a shadow value back to the
// declared type. expr names the shadow value; depth keeps the closure
// parameters of nested levels from shadowing one another.
//
// A slice of jsonInteger is not convertible to a slice of int64 -- Go requires
// identical element types -- so each container level is rebuilt by a helper
// that preserves nil, which is what keeps an absent, a null and an empty
// collection as distinguishable after the change as they were before it.
func integerConvert(t GoType, expr string, depth int) string {
	switch v := t.(type) {
	case *PrimitiveType:
		// The leaf. Its shadow is jsonInteger, whose underlying type is int64.
		return "int64(" + expr + ")"
	case *PointerType:
		return integerConvertCall("jsonIntegerPtr", v.Inner, expr, depth)
	case *ArrayType:
		return integerConvertCall("jsonIntegerSlice", v.ItemType, expr, depth)
	case *MapType:
		return integerConvertCall("jsonIntegerMap", v.ValueType, expr, depth)
	}
	return expr
}

func integerConvertCall(helper string, elem GoType, expr string, depth int) string {
	shadow, _ := integerShadowType(elem)
	param := fmt.Sprintf("_ix%d", depth)
	return fmt.Sprintf("%s(%s, func(%s %s) %s { return %s })",
		helper, expr, param, shadow.GoTypeName(), elem.GoTypeName(),
		integerConvert(elem, param, depth+1))
}

// integerDecodeFor builds the decode description for a position whose declared
// Go type is t and whose schema is s, or nil when the position needs nothing.
//
// Nothing is needed in two cases, and both are load-bearing. A type holding no
// int64 has no leaf to reach. And a draft that requires an integer *token*
// -- draft 3 and draft 4, where 1.0 is not an integer and the official suite's
// zeroTerminatedFloats says so -- is already served exactly right by the plain
// int64 decode, which refuses the float notation. Emitting the tolerant path
// there would not fix a defect, it would introduce one in the other direction.
func (g *Generator) integerDecodeFor(t GoType, s *schema.Schema) *IntegerDecodeDef {
	if t == nil || g.requiresStrictIntegerToken(s) {
		return nil
	}
	shadow, ok := integerShadowType(t)
	if !ok {
		return nil
	}
	return &IntegerDecodeDef{
		ShadowType: shadow,
		Convert:    integerConvert(t, integerShadowVar, 0),
	}
}

// needsUnmarshalForIntegers reports whether a field's integer decode obliges its
// struct to carry an UnmarshalJSON, because the shadow and the conversion are
// emitted inside that method and nowhere else.
//
// No route reaches this today: every object struct already sets the flag on
// other grounds -- it rejects null, or it accepts non-object data, or it has an
// overflow map -- and neutering this function changes no golden, no regression
// and no co-generated iteration. It is kept so that the two are decided
// together rather than by coincidence: a struct that lost the flag for some
// other reason would otherwise keep the field's declared int64 and silently
// drop the decode that was built for it.
func needsUnmarshalForIntegers(f FieldDef) bool {
	return f.IntegerDecode != nil
}

// resolveIntegerDecodes settles the positions that are decided from a whole type
// definition rather than from one property: a named type whose underlying is a
// container of integers, an integer enum, and the overflow map of an object
// whose values one sub-schema types.
//
// Both are the same defect as a struct field's -- a schema integer reached by a
// decode that answers from the Go type -- and both are held here rather than at
// the many construction sites because the type's own schema, which is what
// names its draft, is on hand under g.typeSchemas.
//
// A named type whose underlying is a bare int64 is deliberately left alone: it
// already decodes through the alias template's IsIntegerType arm, which is
// draft-aware through StrictInteger and is the behaviour issue #90 measures
// every other position against.
func (g *Generator) resolveIntegerDecodes() {
	for _, td := range g.output.TypeDefs {
		switch d := td.(type) {
		case *AliasDef:
			if !d.CanHaveMethods() || d.IsIntegerType() || d.UnmarshalAs != "" {
				continue
			}
			d.IntegerDecode = g.integerDecodeFor(d.Underlying, g.typeSchemas[d.Name])
		case *EnumDef:
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
		case *StructDef:
			// The overflow map of an object whose values one sub-schema types.
			// Settled here rather than beside each of the several places that
			// build an AdditionalPropertiesDef, so no route can acquire the map
			// without the decode that goes with its value type.
			if d.AdditionalProperties == nil {
				continue
			}
			d.AdditionalProperties.IntegerDecode =
				g.integerDecodeFor(d.AdditionalProperties.ValueType, g.typeSchemas[d.Name])
		}
	}
}
