package generator

import "strings"

// How a generated type decodes the value at one position.
//
// Every struct, every raw-JSON wrapper, and every alias over a slice, a map or a
// struct decodes in place: it has a decodeJSONAt(*jsonDoc, jsonSpan) method that
// reads the value at a span of one indexed document, and hands each member's
// span to the member's own type in turn (see the jsonDoc helpers). A container
// of such a type holds nothing encoding/json can decode for it without calling
// back into the type -- and calling back through encoding/json scans the member
// again, at every level, which is what made decoding a recursive document
// quadratic in its depth. So the decode of each position is composed here, out
// of the generic helpers and the in-place methods, as a Go expression of type
// jsonAt[T] over the position's own type:
//
//	*Item            func(_p **Item, _d *jsonDoc, _s jsonSpan) error { return jsonDecodePtr(_p, _d, _s, (*Item).decodeJSONAt) }
//	[]Item           func(_p *[]Item, _d *jsonDoc, _s jsonSpan) error { return jsonDecodeSlice(_p, _d, _s, (*Item).decodeJSONAt) }
//	map[string]int64 jsonAtJSON[map[string]int64]
//
// A position holding nothing that decodes in place -- a scalar, a container of
// scalars, an enum, another package's type -- is left to encoding/json whole,
// through jsonAtJSON: nothing under it calls back, so the one decode is linear.
//
// Two readings are composed, because the generated code reports a refusal in two
// ways and both are pinned. The json reading is encoding/json's own: a refusal
// comes back exactly as json.Unmarshal into the same type would have returned
// it. The member reading is what a struct's declared member takes, and names the
// element or key at fault inside the member (see decodepath_helpers); it opens
// every slice and map it meets, as decodeMemberExpr always has, and reads
// everything else the json way with jsonDecodeRefusal in front.

// resolveDecodeAt settles which types of this file decode in place.
//
// Every struct does, and so carries an UnmarshalJSON: one decoded by
// encoding/json instead would match its keys case-insensitively (issue #245) and
// merge into whatever the value held before. Every struct the generator builds
// already had one -- each carries an overflow map for it to fill -- so this
// states what was true rather than changing it.
//
// Run before populateAliasDelegates, because an alias that decodes in place has
// an UnmarshalJSON of its own, which an alias over it has to borrow.
func (g *Generator) resolveDecodeAt() {
	for _, td := range g.output.TypeDefs {
		switch d := td.(type) {
		case *StructDef:
			d.NeedsUnmarshal = true
		case *AliasDef:
			d.DecodeAt = g.aliasDecodesInPlace(d)
		}
	}
}

// aliasDecodesInPlace reports whether an alias is one of those that decode in
// place: one that can carry methods, over a chain of names that ends at a
// slice, a map, a struct or a raw-JSON wrapper.
//
// A slice or a map is here even when nothing inside it decodes in place,
// because the alias needs an UnmarshalJSON of its own either way. Decoded by
// encoding/json into an alias the caller already holds, a map is merged into --
// the members of an earlier document stay -- and a slice is written over in the
// array it already had, which a copy of the value made before the decode shares.
// The alias's own decode replaces the value instead; see the unmarshal template.
func (g *Generator) aliasDecodesInPlace(ad *AliasDef) bool {
	if ad == nil || !ad.CanHaveMethods() {
		return false
	}
	end := g.aliasEndOf(ad.Underlying)
	switch {
	case end.Go != nil:
		switch end.Go.(type) {
		case *ArrayType, *MapType:
			return true
		}
	case end.Def != nil:
		return defDecodesInPlace(end.Def)
	}
	return false
}

// defDecodesInPlace reports whether a declaration has a decodeJSONAt method.
func defDecodesInPlace(td TypeDef) bool {
	switch d := td.(type) {
	case *StructDef, *TypeOnlySchemaDef, *NotSchemaDef, *DynamicSchemaDef, *AnnotationSchemaDef:
		return true
	case *AliasDef:
		return d.DecodeAt
	}
	return false
}

// resolveDecodePlans writes the decode of every position this file's types
// decode: each struct field, each union variant, each overflow value, and each
// alias's own value. Run once every declaration and every leaf decode exists,
// since a position's decode is composed from what the types under it do.
func (g *Generator) resolveDecodePlans() {
	for _, td := range g.output.TypeDefs {
		switch d := td.(type) {
		case *StructDef:
			for i := range d.Fields {
				f := &d.Fields[i]
				t := f.Type
				if f.LeafDecode != nil {
					t = f.LeafDecode.ShadowType
				}
				f.MemberDecoder = g.memberDecoder(t)
				f.ValueDecoder = g.jsonDecoder(t)
			}
			for i := range d.OneOfs {
				for j := range d.OneOfs[i].Variants {
					v := &d.OneOfs[i].Variants[j]
					t := v.Type
					if v.LeafDecode != nil {
						t = v.LeafDecode.ShadowType
					}
					v.Decoder = g.jsonDecoder(t)
				}
			}
			if ap := d.AdditionalProperties; ap != nil {
				t := ap.ValueType
				if ap.LeafDecode != nil {
					t = ap.LeafDecode.ShadowType
				}
				ap.ValueDecoder = g.jsonDecoder(t)
			}
			for i := range d.PatternProperties {
				d.PatternProperties[i].Decoder = g.heldDecoder(d.PatternProperties[i].TypeName)
			}
			for i := range d.TupleValidations {
				ft := &d.TupleValidations[i]
				g.resolveTupleDecoders(ft.Items, ft.Tail)
				if !ft.HasHeldPositions() || ft.IsPointer {
					continue
				}
				for j := range d.Fields {
					if f := &d.Fields[j]; f.Name == ft.FieldName {
						f.MemberDecoder = lazyItemsDecoder(f.Type, f.MemberDecoder)
						f.ValueDecoder = lazyItemsDecoder(f.Type, f.ValueDecoder)
					}
				}
			}
			g.resolveItemTupleDecoders(d.ItemValidations)
			for i := range d.BranchOverflowChecks {
				d.BranchOverflowChecks[i].Decoder = g.heldDecoder(d.BranchOverflowChecks[i].TypeName)
			}
			if u := d.UnevaluatedProperties; u != nil {
				u.ValueDecoder = g.heldDecoder(u.ValueType)
			}
		case *TypeOnlySchemaDef:
			for i := range d.TypeBranches {
				d.TypeBranches[i].Decoder = g.heldDecoder(d.TypeBranches[i].TypeName)
			}
		case *AliasDef:
			g.resolveTupleDecoders(d.TupleItems, d.TupleTail)
			g.resolveItemTupleDecoders(d.ItemValidations)
			if !d.DecodeAt {
				continue
			}
			d.UnderlyingDecoder = g.structuralDecoder(&NamedType{Name: d.Name}, d.Underlying)
			if d.UnmarshalAs != "" {
				d.UnmarshalAsDecoder = g.namedDecoder(d.UnmarshalAs)
			}
		}
	}
}

// resolveTupleDecoders sets each typed tuple position's in-place decode.
func (g *Generator) resolveTupleDecoders(items []TupleItemDef, tail *TupleItemDef) {
	for i := range items {
		items[i].Decoder = g.heldDecoder(items[i].TypeName)
	}
	if tail != nil {
		tail.Decoder = g.heldDecoder(tail.TypeName)
	}
}

// resolveItemTupleDecoders is resolveTupleDecoders for the tuples that are
// elements of an array.
func (g *Generator) resolveItemTupleDecoders(defs []ItemValidationDef) {
	for i := range defs {
		for j := range defs[i].Levels {
			lv := &defs[i].Levels[j]
			g.resolveTupleDecoders(lv.TupleItems, lv.TupleTail)
		}
	}
}

// lazyItemsDecoder wraps the decode of a tuple field so that, decoded where
// Validate decodes a held value, its elements are read lazily (see
// jsonLazyItemsOr). Anywhere else the decode is dec, unchanged.
func lazyItemsDecoder(t GoType, dec string) string {
	if !isAnySlice(t) || dec == "" {
		return dec
	}
	return jsonAtLiteral(t, "jsonLazyItemsOr["+t.GoTypeName()+"](_p, _d, _s, "+dec+")")
}

// isAnySlice reports whether t is []any, which is what a tuple is held as.
func isAnySlice(t GoType) bool {
	a, ok := t.(*ArrayType)
	if !ok {
		return false
	}
	p, ok := a.ItemType.(*PrimitiveType)
	return ok && p.Name == "any"
}

// heldDecoder is the in-place decode of a type Validate decodes a held raw value
// into, named as generated source names it, wherever decoding it reaches a type
// of this package that decodes in place; "" otherwise -- a value encoding/json
// decodes whole holds nothing that decodes again, so the one decode it gets is
// already the whole cost. See jsonDecodeHeld.
func (g *Generator) heldDecoder(name string) string {
	if name == "" || strings.ContainsAny(name, ".*[]") {
		return ""
	}
	t := &NamedType{Name: name}
	if !g.reachesInPlace(t) {
		return ""
	}
	return g.jsonDecoder(t)
}

// namedDecoder is the json reading of a type named in generated source, as
// UnmarshalAs names one: this package's in-place method where the name is one
// of this package's types that decodes in place, and encoding/json otherwise.
func (g *Generator) namedDecoder(name string) string {
	if !strings.Contains(name, ".") {
		if td := g.typeDefInScope(name); td != nil && defDecodesInPlace(td) {
			return "(*" + name + ").decodeJSONAt"
		}
	}
	return "jsonAtJSON[" + name + "]"
}

// reachesInPlace reports whether decoding a value of t reaches a type of this
// package that decodes in place. Where it does not, encoding/json can decode
// the whole value without calling back into this package at all.
func (g *Generator) reachesInPlace(t GoType) bool {
	return g.reachesInPlaceSeen(t, nil)
}

func (g *Generator) reachesInPlaceSeen(t GoType, seen map[string]bool) bool {
	switch v := t.(type) {
	case *PointerType:
		return g.reachesInPlaceSeen(v.Inner, seen)
	case *ArrayType:
		return g.reachesInPlaceSeen(v.ItemType, seen)
	case *MapType:
		return g.reachesInPlaceSeen(v.ValueType, seen)
	case *NamedType:
		if v.PkgAlias != "" {
			return false
		}
		td := g.typeDefInScope(v.Name)
		if td == nil {
			return false
		}
		if defDecodesInPlace(td) {
			return true
		}
		// An alias that cannot carry methods is decoded through its
		// underlying type, so what that holds is what it reaches.
		if ad, ok := td.(*AliasDef); ok && !ad.CanHaveMethods() {
			if seen[v.Name] {
				return false
			}
			if seen == nil {
				seen = make(map[string]bool, 2)
			}
			seen[v.Name] = true
			return g.reachesInPlaceSeen(ad.Underlying, seen)
		}
	}
	return false
}

// jsonDecoder is the json reading of a value of t.
func (g *Generator) jsonDecoder(t GoType) string {
	if !g.reachesInPlace(t) {
		return "jsonAtJSON[" + t.GoTypeName() + "]"
	}
	switch v := t.(type) {
	case *NamedType:
		if v.Pointer {
			inner := &NamedType{Name: v.Name, PkgAlias: v.PkgAlias, foreign: v.foreign}
			return g.pointerDecoder(t, inner)
		}
		td := g.typeDefInScope(v.Name)
		if td != nil && defDecodesInPlace(td) {
			return "(*" + v.Name + ").decodeJSONAt"
		}
		// An alias that cannot carry methods: its underlying type's decode,
		// spelled over the alias, which the generic helpers accept because they
		// are written over the underlying shape.
		if ad, ok := td.(*AliasDef); ok {
			if dec := g.structuralDecoder(v, ad.Underlying); dec != "" {
				return dec
			}
		}
	case *PointerType:
		return g.pointerDecoder(t, v.Inner)
	case *ArrayType:
		return g.sliceDecoder(t, v.ItemType)
	case *MapType:
		return g.mapDecoder(t, v.ValueType)
	}
	return "jsonAtJSON[" + t.GoTypeName() + "]"
}

// The three generic container helpers, instantiated over a position's own type
// -- which may be a name over the shape rather than the shape itself -- and the
// type of what it holds.
//
// The type arguments are written out rather than left to inference. Inference
// unifies the helper's parameters with the arguments' types level by level,
// and go/types gives up with an internal compiler error past fifty levels of
// nesting: the decode of a type nested two hundred slices deep, which a schema
// writes in two hundred "items", did not compile. Spelled out, the arguments
// are only checked for identity, which has no such limit.

func (g *Generator) pointerDecoder(self, inner GoType) string {
	dec := g.jsonDecoder(inner)
	if own := "(*" + inner.GoTypeName() + ").decodeJSONAt"; dec == own {
		// A pointer to one of this package's types that decodes in place,
		// written out rather than through jsonDecodePtr: the helper is
		// instantiated over what the pointer points to, and a struct is a shape
		// of its own, so it was compiled once per struct type of the package.
		// The same steps: a null leaves the pointer nil, and anything else is
		// decoded into a newly allocated value.
		return "func(_p *" + self.GoTypeName() + ", _d *jsonDoc, _s jsonSpan) error {\n" +
			"if _d.isNull(_s) {\n*_p = nil\nreturn nil\n}\n" +
			"_v := new(" + inner.GoTypeName() + ")\n*_p = _v\nreturn _v.decodeJSONAt(_d, _s)\n}"
	}
	return jsonAtLiteral(self, "jsonDecodePtr["+self.GoTypeName()+", "+inner.GoTypeName()+"](_p, _d, _s, "+dec+")")
}

func (g *Generator) sliceDecoder(self, elem GoType) string {
	return jsonAtLiteral(self, "jsonDecodeSlice["+self.GoTypeName()+", "+elem.GoTypeName()+"](_p, _d, _s, "+g.jsonDecoder(elem)+")")
}

func (g *Generator) mapDecoder(self, value GoType) string {
	return jsonAtLiteral(self, "jsonDecodeMap["+self.GoTypeName()+", "+value.GoTypeName()+"](_p, _d, _s, "+g.jsonDecoder(value)+")")
}

// structuralDecoder is the json reading of a value of the named type self whose
// underlying type is underlying: the decode of the shape underlying ends at,
// written over self. A chain of names is followed to its end, since that end is
// the shape the generic helpers are instantiated over.
//
// It answers "" where nothing under the shape decodes in place, which the alias
// template reads as "decode the value whole through encoding/json", into a type
// declared over self: the one decode is the fast route, and a slice decoded
// element by element through the helpers would be the slow one.
func (g *Generator) structuralDecoder(self *NamedType, underlying GoType) string {
	end := g.aliasEndOf(underlying)
	switch {
	case end.Go != nil:
		if !g.reachesInPlace(end.Go) {
			return ""
		}
		switch v := end.Go.(type) {
		case *PointerType:
			return g.pointerDecoder(self, v.Inner)
		case *NamedType:
			if v.Pointer {
				return g.pointerDecoder(self, &NamedType{Name: v.Name, PkgAlias: v.PkgAlias, foreign: v.foreign})
			}
		case *ArrayType:
			return g.sliceDecoder(self, v.ItemType)
		case *MapType:
			return g.mapDecoder(self, v.ValueType)
		}
	case end.Def != nil && defDecodesInPlace(end.Def):
		// A struct or a wrapper at the end of the chain: its own method, over a
		// pointer converted to it -- legal, since the two types share one
		// underlying type.
		return jsonAtLiteral(self, "(*"+end.Def.TypeName()+")(_p).decodeJSONAt(_d, _s)")
	}
	return ""
}

// memberDecoder is the member reading of a value of t: a struct's declared
// member takes it, so that a refusal names the element or key at fault inside
// the member. It opens every slice and map it meets, as decodeMemberExpr does,
// and reads everything else the json way with jsonDecodeRefusal in front.
//
// A member holding nothing that decodes in place is decoded whole, and opened up
// only once that decode has failed -- the one decode is the fast route, and the
// probe the refusal it is traced by. See jsonProbeLeaf.
func (g *Generator) memberDecoder(t GoType) string {
	if !g.reachesInPlace(t) {
		return jsonAtLiteral(t, "jsonProbeLeaf["+t.GoTypeName()+"](_p, _d, _s, "+decodeMemberExpr(t)+")")
	}
	switch v := t.(type) {
	case *ArrayType:
		return jsonAtLiteral(t, "jsonProbeSlice["+v.ItemType.GoTypeName()+"](_p, _d, _s, "+g.memberDecoder(v.ItemType)+")")
	case *MapType:
		return jsonAtLiteral(t, "jsonProbeMap["+v.ValueType.GoTypeName()+"](_p, _d, _s, "+g.memberDecoder(v.ValueType)+")")
	}
	return jsonAtLiteral(t, "jsonDecodeRefusal("+g.jsonDecoder(t)+"(_p, _d, _s))")
}

// jsonAtLiteral writes a jsonAt[T] over t as a function literal returning body,
// which reads its arguments as _p, _d and _s. The literal captures nothing, so
// it costs no allocation however often the decode runs.
func jsonAtLiteral(t GoType, body string) string {
	return "func(_p *" + t.GoTypeName() + ", _d *jsonDoc, _s jsonSpan) error { return " + body + " }"
}

// decodeMemberExpr is the decode of one position, written over the emitted
// helpers, which a member holding nothing that decodes in place is put to once
// its decode has failed (see jsonProbeLeaf).
//
// A slice and a map are opened up rather than decoded whole, because the whole
// is where the index and the key go missing: decoding into a []T reports the
// element's refusal with nothing saying which element it was. Everything else --
// a scalar, a named type, a pointer to either -- is decoded as itself.
//
// A pointer is deliberately not descended through. encoding/json fills a
// settable pointer with nil for a JSON null without consulting the type's own
// UnmarshalJSON at all, so a probe that dropped the pointer would refuse a null
// the real decode accepts, and name a member that is not at fault.
func decodeMemberExpr(t GoType) string {
	switch v := t.(type) {
	case *ArrayType:
		return "jsonDecodeItems(" + decodeMemberExpr(v.ItemType) + ")"
	case *MapType:
		return "jsonDecodeValues(" + decodeMemberExpr(v.ValueType) + ")"
	}
	return "jsonDecodeValue[" + t.GoTypeName() + "]"
}
