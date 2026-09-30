package generator

import "github.com/mgilbir/schemagen/pkg/schema"

// What a Go type is, underneath every name it is reached through.
//
// A $ref chain is a chain of Go names. {"$ref":"#/$defs/A"} over an A that is
// itself {"$ref":"#/$defs/B"} declares `type A B`, and a third hop declares a
// third name over the second; the chain may leave the document, and under
// --schema-package it may leave the Go package too, where the next name is
// another package's and only that package's published record describes it.
// Whatever the chain's length and wherever it goes, the value at the end of it
// is one Go type -- a struct, a string, a slice -- and every question the
// generator asks about "what kind of value is a field of this type" is a
// question about that end, not about the first name.
//
// The questions used to be answered one hop deep, each by a predicate of its
// own, and each hop-counting differently: zeroLossyTypeName followed the chain,
// isStructType did not look past the first name, isCollectionType and
// isInterfaceType looked one alias deep, isObjectProperty resolved one $ref of
// the schema. So {"$ref":"#/$defs/A"} with A a $ref to an object came out as a
// value field `Sig A` over `type A B`: not a struct by the one predicate that
// decides the pointer, never omitted by omitempty, and an absent property was
// written back out as {"q":""} -- a value the document never had, satisfying
// the schema's own `required`, so nothing downstream could tell it was
// invented. Where B was an object-level oneOf, the invented value was a null
// the same type then refused to read back. Every CycloneDX 1.6 BOM carries one
// of these (definitions.signature is a $ref to jsf's signature definition), so
// every component, service and metadata block round-tripped with a "signature"
// it did not have.
//
// aliasEndOf is the one walk, and the predicates below are answered from what
// it finds. A type another package owns ends the walk: that package ran this
// same walk over its own declarations before it published the record the
// reference carries, so the record already describes the end of its chain.

// aliasEnd is where a chain of Go names comes out. At most one of the three
// fields is set; none is set when the chain reaches a name nothing has
// declared yet -- a type still being generated, which is how a recursive
// $ref looks from inside its own definition -- or goes round in a circle.
type aliasEnd struct {
	// Go is the anonymous type the chain ends at: a primitive, a slice, a map,
	// or a pointer -- including a NamedType spelled with its own pointer, which
	// is a pointer whatever it points at.
	Go GoType
	// Def is the declaration the chain ends at, when that is a type this
	// package (or an earlier file of a shared-types run) declares as anything
	// but an alias.
	Def TypeDef
	// Foreign is the type the chain ends at when it reaches one another package
	// owns. Its record answers for the rest of the chain.
	Foreign *NamedType
}

// known reports whether the walk reached an end it can describe.
func (e aliasEnd) known() bool {
	return e.Go != nil || e.Def != nil || e.Foreign != nil
}

// maxAliasChain bounds the walk. A $ref chain long enough to reach it is not
// one a schema writes; the bound exists so that a malformed cycle the visited
// set somehow missed still ends.
const maxAliasChain = 64

// aliasEndOf follows t through every AliasDef between it and the declaration
// it stands for.
func (g *Generator) aliasEndOf(t GoType) aliasEnd {
	var seen map[string]bool
	for range maxAliasChain {
		nt, ok := t.(*NamedType)
		if !ok || nt.Pointer {
			return aliasEnd{Go: t}
		}
		if nt.PkgAlias != "" {
			return aliasEnd{Foreign: nt}
		}
		if seen[nt.Name] {
			return aliasEnd{}
		}
		if seen == nil {
			seen = make(map[string]bool, 4)
		}
		seen[nt.Name] = true
		td := g.typeDefInScope(nt.Name)
		if td == nil {
			return aliasEnd{}
		}
		ad, isAlias := td.(*AliasDef)
		if !isAlias {
			return aliasEnd{Def: td}
		}
		if ad.Underlying == nil {
			return aliasEnd{}
		}
		t = ad.Underlying
	}
	return aliasEnd{}
}

// typeDefInScope is the declaration of name among the types this call emitted
// and, in a shared-types run, the ones earlier calls did -- the same first
// match typeDefsInScope's order gives, without building the joined slice.
func (g *Generator) typeDefInScope(name string) TypeDef {
	if g.output != nil {
		for _, td := range g.output.TypeDefs {
			if td.TypeName() == name {
				return td
			}
		}
	}
	for _, td := range g.priorTypeDefs {
		if td.TypeName() == name {
			return td
		}
	}
	return nil
}

// hasNilState reports whether a value of this Go type can be nil, so that
// `x != nil` both compiles against it and says something.
//
// Four shapes have that state and no others: a pointer, a slice or map, an
// interface, and raw JSON bytes (json.RawMessage, and the enum over it a
// heterogeneous `enum` becomes). Everything else a property can be typed as --
// a string, an int64, a time.Time, a generated struct, one of the wrappers --
// has a zero and no absence, and comparing one to nil is a compile error
// rather than a check that answers badly.
//
// The question is asked wherever emitted code stands in a nil for "the caller
// never set this": what an optional field is omitted by, whether it needs a
// pointer to be omitted at all, and whether a forbidden property can be caught
// in a value that was built in Go rather than decoded. Which fields have it is a
// fact about the configuration as much as about the schema -- under
// --omit-empty=false an optional scalar is no longer pointer-wrapped -- so it
// has to be asked of the resolved type and not assumed.
func (g *Generator) hasNilState(t GoType) bool {
	if t == nil {
		return false
	}
	if t.IsPointer() {
		return true
	}
	end := g.aliasEndOf(t)
	switch {
	case end.Foreign != nil:
		return end.Foreign.foreign.NilState
	case end.Go != nil:
		switch v := end.Go.(type) {
		case *PointerType, *ArrayType, *MapType:
			return true
		case *NamedType:
			return v.Pointer
		case *PrimitiveType:
			return v.Name == "any" || v.Name == GoRawTypeName
		}
	case end.Def != nil:
		if d, ok := end.Def.(*EnumDef); ok {
			return d.IsRaw
		}
	}
	return false
}

// canHaveMethods reports whether a type declared as `type X t` may carry
// methods: Go permits none on a type whose underlying type is a pointer or an
// interface, and the underlying type of a name over a name is the underlying
// type of the last one.
//
// A chain that reaches another package's type is answered by what that package
// published. Its declaration is not in this package's table, and a local type
// of the same name is a different type -- following that one would decide
// whether `type X other.T` may carry methods from a namesake. A chain that
// reaches a name nothing has declared yet, or that goes round in a circle,
// answers yes, which is what an ordinary declaration is.
func (g *Generator) canHaveMethods(t GoType) bool {
	if t == nil {
		return true
	}
	end := g.aliasEndOf(t)
	switch {
	case end.Foreign != nil:
		return !end.Foreign.foreign.NoMethods
	case end.Go != nil:
		if end.Go.IsPointer() {
			return false
		}
		if pt, ok := end.Go.(*PrimitiveType); ok && pt.Name == "any" {
			return false
		}
	}
	return true
}

// zeroJSONKindName is zeroJSONKind's answer as the one string a published
// record carries: the kind, or "" where no single JSON value names the zero.
func (g *Generator) zeroJSONKindName(t GoType) string {
	kind, ok := g.zeroJSONKind(t, 0)
	if !ok {
		return ""
	}
	return kind
}

// isCollectionType reports whether a Go type is a slice or a map, directly or
// through any chain of names. Such optional fields use ",omitzero" so a
// present-but-empty collection is preserved on marshal.
func (g *Generator) isCollectionType(t GoType) bool {
	end := g.aliasEndOf(t)
	switch {
	case end.Foreign != nil:
		return end.Foreign.foreign.Collection
	case end.Go != nil:
		switch end.Go.(type) {
		case *ArrayType, *MapType:
			return true
		}
	}
	return false
}

// isInterfaceType reports whether a Go type is the empty interface, directly or
// through any chain of names. Like a pointer or a collection, its nil is what
// unmarshal leaves when the property was absent, and it marshals to null; and
// no type whose chain ends at one may carry a method.
func (g *Generator) isInterfaceType(t GoType) bool {
	end := g.aliasEndOf(t)
	switch {
	case end.Foreign != nil:
		return end.Foreign.foreign.Interface
	case end.Go != nil:
		pt, ok := end.Go.(*PrimitiveType)
		return ok && pt.Name == "any"
	}
	return false
}

// isStructTypeNamed reports whether a named type is a Go struct underneath:
// declared as one here, reached through any chain of aliases that ends at one,
// or published as one by the package that owns it.
//
// The answer decides whether an optional property is pointer-wrapped, since
// omitempty never omits a struct. Getting it from the first name alone wrote
// `type A B` over a struct B as an always-present value; getting it for a
// foreign name from a namesake -- or from nothing at all -- did the same to a
// foreign struct (issue #296).
func (g *Generator) isStructTypeNamed(nt *NamedType) bool {
	if nt == nil || nt.Pointer {
		return false
	}
	end := g.aliasEndOf(nt)
	switch {
	case end.Foreign != nil:
		return end.Foreign.foreign.Struct
	case end.Def != nil:
		_, isStruct := end.Def.(*StructDef)
		return isStruct
	}
	return false
}

// isRawValueWrapperType reports whether t names a generated type that keeps the
// value as raw JSON and validates it after the fact: the wrappers built for a
// draft-3 schema-valued "type", a multi-type union, and an anyOf across
// unrelated representations (TypeOnlySchemaDef), for a bare "not"
// (NotSchemaDef), for a schema constrained only by oneOf / anyOf /
// if-then-else (DynamicSchemaDef), and for a schema held as data
// (AnnotationSchemaDef). Such a type is a struct with a custom MarshalJSON, so
// it is never omitted by omitempty and needs omitzero instead -- without which
// an absent optional property of that type marshals as null and the document no
// longer round-trips.
//
// Only the wrapper itself answers yes. An alias over one would inherit neither
// its IsZero nor its MarshalJSON, which is why aliasDropsMethods has a $ref to
// one generated from the schema again rather than aliased; a chain that did
// reach one through an alias would reach a type that omitzero cannot drop.
func (g *Generator) isRawValueWrapperType(t GoType) bool {
	nt, ok := t.(*NamedType)
	if !ok || nt.Pointer {
		return false
	}
	if nt.PkgAlias != "" {
		return nt.foreign.RawWrapper
	}
	switch g.typeDefInScope(nt.Name).(type) {
	case *TypeOnlySchemaDef, *NotSchemaDef, *DynamicSchemaDef, *AnnotationSchemaDef:
		return true
	}
	return false
}

// isZeroLossyPrimitive returns true if the Go type is a primitive whose zero value
// would be lost with omitempty ("", false, int64=0, float64=0.0).
//
// time.Time and netip.Addr are worse off than the scalars, not better, and for
// the reason the whole rule exists. omitempty never omits a struct, so an
// optional property the document did not carry is not merely invisible -- it is
// *invented into the output*: an absent `format: date-time` marshals as
// "0001-01-01T00:00:00Z" through time.Time's own MarshalJSON, and an absent
// `format: ipv4` as "" through netip.Addr's MarshalText. Both are values the
// document never held and the schema never saw.
//
// ",omitzero" would omit them, since time.Time has IsZero and netip.Addr is
// comparable, but it omits by *value*: a document that genuinely carries
// "0001-01-01T00:00:00Z" would come back without the property. That trades
// inventing a value for dropping one, which is the same round-trip break in the
// other direction. The pointer distinguishes the two exactly -- nil is absent,
// non-nil is present whatever the instant -- which is the contract every other
// zero-lossy type here already has.
func isZeroLossyPrimitive(goType GoType) bool {
	pt, ok := goType.(*PrimitiveType)
	if !ok {
		return false
	}
	switch pt.Name {
	case "string", "bool", "int64", "float64", GoNumberTypeName, "time.Time", "netip.Addr":
		return true
	}
	return false
}

// isZeroLossyNamedType reports whether t names a generated type that has no
// representation for "absent" of its own and is not a struct -- a name over a
// zero-lossy primitive (a $ref to a "type":"string" definition, an inline enum,
// a const promoted to a single-value enum), or one of the wrapper structs over
// a scalar. The name does not give the value a nil to be absent in, so such a
// field loses a legitimate "", 0 or false to omitempty exactly as a bare
// primitive would.
//
// The wrappers are the InferredAliasDef built for a definition that carries
// constraints but no "type", and the BigIntAliasDef that BigIntSupport puts over
// a named integer. They are worse off than a named primitive, not better --
// omitempty never omits a struct, so an absent optional property is fabricated
// into the output as the wrapper's zero and then measured against the
// definition's constraints. And unlike the raw-value wrappers they carry no
// IsZero to hand ",omitzero": their zero is exactly what a present 0 or ""
// decodes to, so omitzero would drop a legitimate value. The pointer is the
// only representation of absence left.
//
// The answer comes from the generated type rather than from the property's
// schema because a $ref says nothing about the shape of its target. Both are
// available by the time this is asked: resolvePropertyType generates a ref
// target, and generateEnumDef an inline enum, before returning a name for it.
func (g *Generator) isZeroLossyNamedType(t GoType) bool {
	nt, ok := t.(*NamedType)
	if !ok || nt.Pointer {
		return false
	}
	end := g.aliasEndOf(nt)
	switch {
	case end.Foreign != nil:
		// A type owned by another package of a cross-package run. The owning
		// generator ran this same predicate over it and published the answer;
		// a type whose owner has not been generated yet published nothing and
		// answers no.
		return end.Foreign.foreign.ZeroLossy
	case end.Go != nil:
		return isZeroLossyPrimitive(end.Go)
	case end.Def != nil:
		switch d := end.Def.(type) {
		case *EnumDef:
			// A heterogeneous enum is backed by json.RawMessage, whose zero is
			// nil -- absent already has a representation there.
			return isZeroLossyPrimitive(d.BaseType)
		case *InferredAliasDef, *BigIntAliasDef:
			return true
		}
	}
	return false
}

// absenceNeedsPointer reports whether an optional property of this Go type can
// be told absent from present only by holding it behind a pointer.
//
// That is every type with no absent state of its own: a struct, which
// omitempty never omits; and a scalar or a wrapper over one, whose zero is a
// value a document can carry. A pointer, a collection, an interface and raw
// bytes are nil exactly when the property was absent, and a raw-value wrapper
// holds no bytes then and reports it through IsZero, so none of those needs
// one.
//
// The type is followed to the end of its chain of names, which is what decides
// the answer: the same struct reached through one $ref or through three is the
// same value. Where the chain reaches a name nothing has declared yet -- the
// type being generated right now, reached back through a recursive $ref -- the
// schema is asked instead, followed through every $ref it chains through to the
// schema that will be declared there.
func (g *Generator) absenceNeedsPointer(t GoType, s *schema.Schema) bool {
	if t == nil || t.IsPointer() || g.hasNilState(t) || g.isRawValueWrapperType(t) {
		return false
	}
	if isZeroLossyPrimitive(t) {
		return true
	}
	nt, ok := t.(*NamedType)
	if !ok {
		return false
	}
	if g.isStructTypeNamed(nt) || g.isZeroLossyNamedType(nt) {
		return true
	}
	if g.aliasEndOf(nt).known() {
		return false
	}
	return g.schemaDeclaresStruct(s)
}

// schemaDeclaresStruct is the schema's side of absenceNeedsPointer, for a type
// that is still being generated: whether the schema at the end of s's $ref
// chain is an object with properties, which is what a struct is declared from.
func (g *Generator) schemaDeclaresStruct(s *schema.Schema) bool {
	for range maxAliasChain {
		if s == nil {
			return false
		}
		if primarySchemaType(s) == "object" && hasProperties(s) {
			return true
		}
		_, next := g.referenceTargetUncounted(s)
		if next == nil || next == s {
			return false
		}
		s = next
	}
	return false
}
