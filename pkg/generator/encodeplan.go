package generator

import "strings"

// This file plans how every generated value is written back out: the encoding
// half of decodeplan.go.
//
// A value used to be written by encoding/json, member by member, calling each
// member type's MarshalJSON and then checking and compacting what it returned --
// the member's whole subtree, at every level of the value -- and a struct with
// members to write by hand parsed its own output back into a map and marshalled
// that again. A document nested d levels deep cost d times its size to write:
// 8,000 levels took four and a half seconds. Now every struct, and every alias
// over one, has an appendJSON that writes the value into one buffer, and calls
// the appendJSON of what it holds directly; encoding/json writes only the leaves
// -- scalars and the types this package does not write itself -- over their own
// values. The bytes are the ones encoding/json wrote, because every piece of
// them is still encoding/json's: see the emitted jsonAppendLeaf.

// EncodeMember is one member of the object encoding/json wrote for a struct:
// a tagged field, or one of the raw members MarshalJSON added for a union.
type EncodeMember struct {
	// Index is the member's place in EncodeMembers, which is the number the
	// struct's appendMemberJSON writes it under.
	Index    int
	JSONName string
	Field    *FieldDef
	OneOf    *OneOfDef
	// Omit is "empty" or "zero" for a field tagged ",omitempty" or
	// ",omitzero", and "" for one written unconditionally.
	Omit string
}

// encodeAdditionalMember is the index an additionalProperties value is
// deferred under: every one shares it, and appendMemberJSON reads which one
// from the key. It is past any index EncodeMembers can hold.
const encodeAdditionalMember = 1 << 30

// resolveEncodePlans writes the encode of every position this file's types
// write, once every declaration exists and every alias has its delegates.
func (g *Generator) resolveEncodePlans() {
	// Which aliases write themselves depends on what they hold, and an alias
	// may hold another; a chain is settled from its far end, one link a pass.
	for changed := true; changed; {
		changed = false
		for _, td := range g.output.TypeDefs {
			if ad, ok := td.(*AliasDef); ok && !ad.EncodeTo && g.aliasEncodes(ad) {
				ad.EncodeTo = true
				changed = true
			}
		}
	}
	for _, td := range g.output.TypeDefs {
		switch d := td.(type) {
		case *StructDef:
			for i := range d.Fields {
				f := &d.Fields[i]
				f.EncodeLeaf = !g.reachesEncoder(f.Type)
				f.Encoder = g.jsonEncoder(f.Type)
			}
			for i := range d.OneOfs {
				for j := range d.OneOfs[i].Variants {
					v := &d.OneOfs[i].Variants[j]
					v.Encoder = g.jsonEncoder(v.Type)
				}
			}
			if ap := d.AdditionalProperties; ap != nil {
				ap.ValueEncodeLeaf = !g.reachesEncoder(ap.ValueType)
				ap.ValueEncoder = g.jsonEncoder(ap.ValueType)
			}
			d.EncodeMembers = encodeMembers(d)
			d.StripRules = g.stripRulesFor(d)
			d.EncodeManual = nil
			next := len(d.EncodeMembers)
			for i := range d.OneOfs {
				if o := &d.OneOfs[i]; o.IsProperty() && o.ManualJSON {
					d.EncodeManual = append(d.EncodeManual, EncodeMember{Index: next, JSONName: o.JSONName, OneOf: o})
					next++
				}
			}
			for i := range d.Fields {
				if f := &d.Fields[i]; f.ManualJSON {
					d.EncodeManual = append(d.EncodeManual, EncodeMember{Index: next, JSONName: f.JSONName, Field: f})
					next++
				}
			}
			d.EncodeObject = d.NeedsMarshal && (d.HasManualJSONOneOf() || d.AdditionalProperties != nil ||
				d.HasPatternProperties() || d.NeedsJSONNulls() || len(d.WriteOnlyKeys) > 0 || len(d.AccessRules) > 0 ||
				hasManualField(d.Fields))
			// The package variables the encode declares beside the type,
			// named by the registry as every package-level name is.
			if !d.HasTopLevelOneOf() && !d.EncodeObject && len(d.EncodeMembers) > 0 {
				d.EncodeKeysVar = g.names.claim("_encKeys"+d.Name,
					memberHolder(d.Name, "encode-keys", "the member names "+d.Name+" is written with"))
			}
			if d.EncodeObject && len(d.StripRules) > 0 {
				d.StripRulesVar = g.names.claim("_stripRules"+d.Name,
					memberHolder(d.Name, "strip-rules", "the writeOnly rules "+d.Name+" strips as it is written"))
			}
		case *AliasDef:
			if !d.EncodeTo {
				continue
			}
			if d.MarshalAs != "" {
				d.ValueEncoder = g.jsonEncoder(&NamedType{Name: d.MarshalAs})
			} else {
				d.ValueEncoder = g.structuralEncoder(&NamedType{Name: d.Name}, d.Underlying)
			}
		}
	}
}

// stripRulesFor is the writeOnly rules a struct's MarshalJSON strips from what
// it writes.
//
// A rule is a path from the struct down to a member the schema marks writeOnly.
// Where the path's first step is a member the struct holds as a type of its
// own, and that type strips the rest of the path itself -- the rest is one of
// its own rules, or the member its own writeOnly list names -- the member has
// already been written without it, and applying the rule again reads the
// member's whole subtree for nothing: a type that holds itself, stripping one
// level down at every level, read its whole document at every level. Every
// other rule is kept, readOnly ones included in AccessRules, which the decoder
// applies unchanged.
func (g *Generator) stripRulesFor(d *StructDef) []AccessRule {
	var out []AccessRule
	for _, r := range d.AccessRules {
		if !r.WriteOnly {
			continue
		}
		if len(r.Path) > 1 && r.Path[0].Kind == AccessProperty {
			if child := g.memberStruct(d, r.Path[0].Name); child != nil && stripsItself(child, r.Path[1:]) {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// memberStruct is the struct a declared member of d is written by, through a
// pointer and a chain of names; nil where the member is anything else.
func (g *Generator) memberStruct(d *StructDef, jsonName string) *StructDef {
	for i := range d.Fields {
		f := &d.Fields[i]
		if f.JSONName != jsonName {
			continue
		}
		t := f.Type
		if p, ok := t.(*PointerType); ok {
			t = p.Inner
		}
		if nt, ok := t.(*NamedType); ok && nt.Pointer {
			t = &NamedType{Name: nt.Name, PkgAlias: nt.PkgAlias, foreign: nt.foreign}
		}
		if sd, ok := g.aliasEndOf(t).Def.(*StructDef); ok {
			return sd
		}
		return nil
	}
	return nil
}

// stripsItself reports whether a struct's own MarshalJSON strips the member at
// path from what it writes.
func stripsItself(sd *StructDef, path []AccessStep) bool {
	if len(path) == 1 && path[0].Kind == AccessProperty {
		for _, k := range sd.WriteOnlyKeys {
			if k == path[0].Name {
				return true
			}
		}
	}
	for _, r := range sd.AccessRules {
		if r.WriteOnly && accessPathsEqual(r.Path, path) {
			return true
		}
	}
	return false
}

func accessPathsEqual(a, b []AccessStep) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Kind != y.Kind || x.Name != y.Name || x.Index != y.Index ||
			strings.Join(x.Except, "\x00") != strings.Join(y.Except, "\x00") ||
			strings.Join(x.ExceptPatterns, "\x00") != strings.Join(y.ExceptPatterns, "\x00") {
			return false
		}
	}
	return true
}

// HasTopLevelOneOf reports whether the struct stands for a whole-value union,
// which MarshalJSON writes as the selected variant alone.
func (d *StructDef) HasTopLevelOneOf() bool {
	for i := range d.OneOfs {
		if !d.OneOfs[i].IsProperty() {
			return true
		}
	}
	return false
}

// EncodeAdditionalMember is encodeAdditionalMember, for the template.
func (d *StructDef) EncodeAdditionalMember() int { return encodeAdditionalMember }

func hasManualField(fields []FieldDef) bool {
	for i := range fields {
		if fields[i].ManualJSON {
			return true
		}
	}
	return false
}

// encodeMembers lists what encoding/json wrote for the struct, in its order.
//
// A struct without a MarshalJSON of its own was written by encoding/json
// directly: its tagged fields, in declaration order. One with a MarshalJSON
// wrote an auxiliary struct instead, which embeds the struct's fields and
// declares one json.RawMessage per union beside them, before or after the
// embedded fields as MarshalAuxRawFirst says -- and encoding/json's rules for
// such a struct decide what it writes: a member of the auxiliary struct itself
// hides an embedded field of the same name, and two fields of the same name at
// the same depth are both left out.
func encodeMembers(d *StructDef) []EncodeMember {
	var fields []EncodeMember
	for _, m := range d.Members() {
		if m.Field == nil || m.Field.ManualJSON {
			continue
		}
		f := m.Field
		omit := ""
		switch {
		case f.OmitZero:
			omit = "zero"
		case f.OmitEmpty:
			omit = "empty"
		}
		fields = append(fields, EncodeMember{JSONName: f.JSONName, Field: f, Omit: omit})
	}
	fields = dropDuplicateNames(fields)
	if !d.NeedsMarshal {
		return numberMembers(fields)
	}
	var unions []EncodeMember
	for i := range d.OneOfs {
		o := &d.OneOfs[i]
		if !o.IsProperty() || o.ManualJSON {
			continue
		}
		unions = append(unions, EncodeMember{JSONName: o.JSONName, OneOf: o, Omit: "empty"})
	}
	unions = dropDuplicateNames(unions)
	hidden := make(map[string]bool, len(unions))
	for _, u := range unions {
		hidden[u.JSONName] = true
	}
	var shown []EncodeMember
	for _, f := range fields {
		if !hidden[f.JSONName] {
			shown = append(shown, f)
		}
	}
	if d.MarshalAuxRawFirst() {
		return numberMembers(append(unions, shown...))
	}
	return numberMembers(append(shown, unions...))
}

// dropDuplicateNames leaves out every member whose name another member at the
// same depth also takes, as encoding/json does with two tagged fields.
func dropDuplicateNames(ms []EncodeMember) []EncodeMember {
	count := make(map[string]int, len(ms))
	for _, m := range ms {
		count[m.JSONName]++
	}
	out := ms[:0:0]
	for _, m := range ms {
		if count[m.JSONName] == 1 {
			out = append(out, m)
		}
	}
	return out
}

func numberMembers(ms []EncodeMember) []EncodeMember {
	for i := range ms {
		ms[i].Index = i
	}
	return ms
}

// defEncodes reports whether a declaration has an appendJSON method.
func defEncodes(td TypeDef) bool {
	switch d := td.(type) {
	case *StructDef:
		return true
	case *AliasDef:
		return d.EncodeTo
	}
	return false
}

// defHasMarshalJSON reports whether encoding/json called a MarshalJSON of the
// declaration's own when it wrote one of its values, which is what a failure
// inside the value was reported through.
func defHasMarshalJSON(td TypeDef) bool {
	switch d := td.(type) {
	case *StructDef:
		return d.NeedsMarshal
	case *AliasDef:
		return d.CanHaveMethods() && d.MarshalAs != ""
	}
	return false
}

// aliasEncodes reports whether an alias writes itself: one that can carry
// methods, over a value that holds a type of this package that does -- through
// MarshalAs where its MarshalJSON delegates, and through the underlying type
// where it has none.
func (g *Generator) aliasEncodes(ad *AliasDef) bool {
	if ad == nil || !ad.CanHaveMethods() {
		return false
	}
	if ad.MarshalAs != "" {
		return g.reachesEncoder(&NamedType{Name: ad.MarshalAs})
	}
	return g.reachesEncoder(ad.Underlying)
}

// reachesEncoder reports whether writing a value of t reaches a type of this
// package that writes itself. Where it does not, encoding/json can write the
// whole value without calling back into this package at all.
func (g *Generator) reachesEncoder(t GoType) bool {
	return g.reachesEncoderSeen(t, nil)
}

func (g *Generator) reachesEncoderSeen(t GoType, seen map[string]bool) bool {
	switch v := t.(type) {
	case *PointerType:
		return g.reachesEncoderSeen(v.Inner, seen)
	case *ArrayType:
		return g.reachesEncoderSeen(v.ItemType, seen)
	case *MapType:
		if !isStringKey(v.KeyType) {
			return false
		}
		return g.reachesEncoderSeen(v.ValueType, seen)
	case *NamedType:
		if v.PkgAlias != "" || strings.Contains(v.Name, ".") {
			return false
		}
		td := g.typeDefInScope(v.Name)
		if td == nil {
			return false
		}
		if defEncodes(td) {
			return true
		}
		// An alias that cannot carry methods is written through its
		// underlying type, so what that holds is what it reaches.
		if ad, ok := td.(*AliasDef); ok && !ad.CanHaveMethods() {
			if seen[v.Name] {
				return false
			}
			if seen == nil {
				seen = make(map[string]bool, 2)
			}
			seen[v.Name] = true
			return g.reachesEncoderSeen(ad.Underlying, seen)
		}
	}
	return false
}

func isStringKey(t GoType) bool {
	p, ok := t.(*PrimitiveType)
	return ok && p.Name == "string"
}

// jsonEncoder is the writing of a value of t, as a jsonEnc[T] expression.
func (g *Generator) jsonEncoder(t GoType) string {
	return g.jsonEncoderVia(t, false)
}

// jsonEncoderVia is jsonEncoder for a value reached through a pointer or not,
// which a failure is named by (see the emitted jsonEncMarshaler).
func (g *Generator) jsonEncoderVia(t GoType, viaPointer bool) string {
	if !g.reachesEncoder(t) {
		return "rt.AppendLeaf[" + t.GoTypeName() + "]"
	}
	switch v := t.(type) {
	case *NamedType:
		if v.Pointer {
			inner := &NamedType{Name: v.Name, PkgAlias: v.PkgAlias, foreign: v.foreign}
			return g.pointerEncoder(t, inner)
		}
		td := g.typeDefInScope(v.Name)
		if td != nil && defEncodes(td) {
			return ownEncoder(v.Name, td, viaPointer)
		}
		if ad, ok := td.(*AliasDef); ok {
			if enc := g.structuralEncoder(v, ad.Underlying); enc != "" {
				return enc
			}
		}
	case *PointerType:
		return g.pointerEncoder(t, v.Inner)
	case *ArrayType:
		return g.sliceEncoder(t, v.ItemType)
	case *MapType:
		return g.mapEncoder(t, v.ValueType)
	}
	return "rt.AppendLeaf[" + t.GoTypeName() + "]"
}

// ownEncoder is a type's own appendJSON, reporting a failure the way
// encoding/json did when the type has a MarshalJSON it called.
func ownEncoder(name string, td TypeDef, viaPointer bool) string {
	if !defHasMarshalJSON(td) {
		return name + ".appendJSON"
	}
	// Written out rather than through jsonEncMarshaler, which is instantiated
	// over the type and so compiled once per struct type of the package; the
	// refusal is built only on the failure.
	return "func(_v " + name + ", _b []byte) ([]byte, error) {\n" + ownEncodeBody("_v", name, viaPointer) + "}"
}

// ownEncodeBody writes the value v, of this package's type name that has a
// MarshalJSON, into _b, as statements ending in a return: jsonEncMarshaler's
// steps, spelled out.
func ownEncodeBody(v, name string, viaPointer bool) string {
	via := "false"
	if viaPointer {
		via = "true"
	}
	return "_out, _err := " + v + ".appendJSON(_b)\n" +
		"if _err != nil {\nreturn _b, rt.MarshalerErrFor(_err, (*" + name + ")(nil), " + via + ")\n}\n" +
		"return _out, nil\n"
}

// The three container helpers, instantiated over a position's own type and
// the type of what it holds, with their type arguments written out for the
// reason the decode helpers' are (see decodeplan.go).

func (g *Generator) pointerEncoder(self, inner GoType) string {
	if n, ok := inner.(*NamedType); ok && !n.Pointer && n.PkgAlias == "" {
		if td := g.typeDefInScope(n.Name); td != nil && defEncodes(td) && g.reachesEncoder(inner) {
			// A pointer to one of this package's types, written out rather than
			// through jsonEncPtr, which is instantiated over what the pointer
			// points to and so compiled once per struct type: null for a nil
			// one, and the value's own writing otherwise.
			body := "return (*_v).appendJSON(_b)\n"
			if defHasMarshalJSON(td) {
				body = ownEncodeBody("(*_v)", n.Name, true)
			}
			return "func(_v " + self.GoTypeName() + ", _b []byte) ([]byte, error) {\n" +
				"if _v == nil {\nreturn append(_b, \"null\"...), nil\n}\n" + body + "}"
		}
	}
	return jsonEncLiteral(self, "rt.EncPtr["+self.GoTypeName()+", "+inner.GoTypeName()+"](_v, _b, "+g.jsonEncoderVia(inner, true)+")")
}

func (g *Generator) sliceEncoder(self, elem GoType) string {
	return jsonEncLiteral(self, "rt.EncSlice["+self.GoTypeName()+", "+elem.GoTypeName()+"](_v, _b, "+g.jsonEncoder(elem)+")")
}

func (g *Generator) mapEncoder(self, value GoType) string {
	return jsonEncLiteral(self, "rt.EncMap["+self.GoTypeName()+", "+value.GoTypeName()+"](_v, _b, "+g.jsonEncoder(value)+")")
}

// structuralEncoder is the writing of a value of the named type self whose
// underlying type is underlying: the writing of the shape underlying ends at,
// over self. It answers "" where nothing under the shape writes itself.
func (g *Generator) structuralEncoder(self *NamedType, underlying GoType) string {
	end := g.aliasEndOf(underlying)
	switch {
	case end.Go != nil:
		if !g.reachesEncoder(end.Go) {
			return ""
		}
		switch v := end.Go.(type) {
		case *PointerType:
			return g.pointerEncoder(self, v.Inner)
		case *NamedType:
			if v.Pointer {
				return g.pointerEncoder(self, &NamedType{Name: v.Name, PkgAlias: v.PkgAlias, foreign: v.foreign})
			}
		case *ArrayType:
			return g.sliceEncoder(self, v.ItemType)
		case *MapType:
			return g.mapEncoder(self, v.ValueType)
		}
	case end.Def != nil && defEncodes(end.Def):
		// A struct at the end of the chain: its own method, over the value
		// converted to it -- legal, since the two share one underlying type.
		name := end.Def.TypeName()
		return jsonEncLiteral(self, name+"(_v).appendJSON(_b)")
	}
	return ""
}

// jsonEncLiteral writes a jsonEnc[T] over t as a function literal returning
// body, which reads its arguments as _v and _b.
func jsonEncLiteral(t GoType, body string) string {
	return "func(_v " + t.GoTypeName() + ", _b []byte) ([]byte, error) { return " + body + " }"
}
