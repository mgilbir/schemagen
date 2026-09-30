package generator

import (
	"slices"
	"strings"
)

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
			g.pruneDecodeRules(d)
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
				d.HasPatternProperties() || d.NeedsJSONNulls() || len(d.WriteOnlyKeys) > 0 || d.AccessRules != nil ||
				hasManualField(d.Fields))
			// The package variables the encode declares beside the type,
			// named by the registry as every package-level name is.
			if !d.HasTopLevelOneOf() && !d.EncodeObject && len(d.EncodeMembers) > 0 {
				d.EncodeKeysVar = g.names.claim("_encKeys"+d.Name,
					memberHolder(d.Name, "encode-keys", "the member names "+d.Name+" is written with"))
			}
			if d.EncodeObject && d.StripRules != nil {
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

// stripRulesFor is where a struct's MarshalJSON starts stripping writeOnly
// members from what it writes: its AccessRules' start, less the moves that
// strip nothing, and less the walk into a member the struct holds as a type of
// its own that strips everything the walk would -- that member has already been
// written without it, and walking it again reads the member's whole subtree for
// nothing: a type that holds itself, stripping one level down at every level,
// read its whole document at every level. The decoder keeps AccessRules
// unchanged.
func (g *Generator) stripRulesFor(d *StructDef) *AccessRules {
	if d.AccessRules == nil {
		return nil
	}
	var moves []AccessMove
	changed := false
	for _, m := range g.output.AccessMachine[d.AccessRules.Start].Moves {
		if m.Next >= 0 && (!m.SeekWriteOnly || (m.Step.Kind == AccessProperty && g.memberStripsItself(d, m))) {
			m.Next, m.SeekReadOnly, m.SeekWriteOnly = -1, false, false
			changed = true
		}
		if !m.WriteOnly && m.Next < 0 {
			changed = true
			continue
		}
		moves = append(moves, m)
	}
	if len(moves) == 0 {
		return nil
	}
	if !changed {
		return d.AccessRules
	}
	return &AccessRules{Machine: d.AccessRules.Machine, Start: g.addAccessState(moves)}
}

// pruneDecodeRules is stripRulesFor's counterpart for the decoder: a struct's
// start state stops walking into a member it holds as a type of its own whose
// decoder refuses everything the walk would -- decoding the member refuses it
// already, and walking it again from here read the member's subtree once more
// at every level of a value that holds itself. It runs once every type is
// declared, after stripRulesFor has read the start as it was built. The start
// state is the struct's own (accessRulesFor builds a fresh one for a struct),
// so nothing else is changed by it.
func (g *Generator) pruneDecodeRules(d *StructDef) {
	if d.AccessRules == nil {
		return
	}
	moves := g.output.AccessMachine[d.AccessRules.Start].Moves
	for i := range moves {
		m := &moves[i]
		if m.Next >= 0 && m.SeekReadOnly && m.Step.Kind == AccessProperty && g.memberCovers(d, *m, true) {
			m.SeekReadOnly = false
		}
	}
}

// memberStripsItself reports whether the member a property move of d walks
// into is written by a struct whose own MarshalJSON strips everything the walk
// in the move's state would. See memberCovers.
func (g *Generator) memberStripsItself(d *StructDef, m AccessMove) bool {
	return g.memberCovers(d, m, false)
}

// memberCovers reports whether the member a property move of d walks into is
// held by a struct whose own decoder (readOnly) or encoder (!readOnly) does
// everything the walk in the move's state would.
//
// The machine has cycles -- a schema that refers to itself -- so "everything the
// walk would" is not a finite list to compare. It is decided as a simulation:
// the greatest relation between the parent's states and the member struct's in
// which every readOnly (or writeOnly) move of a parent state is matched by a
// move of the related member state on the same step -- marking the same, or
// leading to states that are related in turn -- and, at the member's own start,
// a marked property may instead be one the member's own key list names, since
// its decoder and encoder carry those there. A simulation is a property of the
// states, so it holds of every run, at every depth: whatever the parent's walk
// would refuse or strip below the member, the member's own walk does, in a
// document of any depth, by induction on the document.
//
// The relation is found by starting from every pair the steps can reach and
// removing a pair that fails until none does, rechecking only the pairs that
// lean on one removed. A pair of the member's start is decided for this member
// alone, since its key lists answer there; every other pair is a property of
// the graph, and its answer is kept for the whole Generate: the pairs explored
// are closed under the steps, save pairs already decided, which stand at their
// answer, so what is left is the greatest relation on them. Each pair of the
// graph is so decided once however many members ask, and a pair of a node with
// itself -- the member's type built from the very schema the parent's walk
// stands in, the usual case -- is related without looking: the identity is a
// simulation.
func (g *Generator) memberCovers(d *StructDef, m AccessMove, readOnly bool) bool {
	child := g.memberStruct(d, m.Step.Name)
	if child == nil || child.accessRoot == nil {
		return false
	}
	parent, ok := g.accessStateKeys[m.Next]
	if !ok || parent.graph != child.accessRoot.graph {
		return false
	}
	if parent.key == child.accessRoot.key {
		return true
	}
	a := parent.graph
	a.settle()
	which, live, keys := 0, a.liveWO, child.WriteOnlyKeys
	marks := func(e accessEdge) bool { return e.wo }
	if readOnly {
		which, live, keys = 1, a.liveRO, child.ReadOnlyKeys
		marks = func(e accessEdge) bool { return e.ro }
	}
	if a.covers[which] == nil {
		a.covers[which] = map[accessPair]bool{}
	}
	decided := a.covers[which]
	relevant := func(e accessEdge) bool {
		return e.mark && marks(e) || !e.mark && live[e.to]
	}
	// bySteps is a node's step moves by step, so that matching a move looks at
	// the moves on its step alone.
	bySteps := map[accessKey]map[string][]accessEdge{}
	byStep := func(k accessKey) map[string][]accessEdge {
		idx, ok := bySteps[k]
		if !ok {
			idx = map[string][]accessEdge{}
			for _, f := range a.settledClosure(k) {
				key := a.stepKey(f.step)
				idx[key] = append(idx[key], f)
			}
			bySteps[k] = idx
		}
		return idx
	}
	type pair struct {
		accessPair
		top bool // the member struct's own start, where its key lists answer for its properties
	}
	// known is the answer for a pair that needs no deciding here.
	known := func(pr pair) (answer, ok bool) {
		if pr.top {
			return false, false
		}
		if pr.p == pr.c {
			return true, true
		}
		answer, ok = decided[pr.accessPair]
		return answer, ok
	}
	start := pair{accessPair: accessPair{p: parent.key, c: child.accessRoot.key}, top: true}
	related := map[pair]bool{start: true}
	leanOn := map[pair][]pair{}
	explored := []pair{start}
	for i := 0; i < len(explored); i++ {
		pr := explored[i]
		for _, e := range a.settledClosure(pr.p) {
			if e.mark || !relevant(e) {
				continue
			}
			for _, f := range byStep(pr.c)[a.stepKey(e.step)] {
				if f.mark {
					continue
				}
				next := pair{accessPair: accessPair{p: e.to, c: f.to}}
				if _, ok := known(next); ok {
					continue
				}
				leanOn[next] = append(leanOn[next], pr)
				if !related[next] {
					related[next] = true
					explored = append(explored, next)
				}
			}
		}
	}
	holds := func(pr pair) bool {
		for _, e := range a.settledClosure(pr.p) {
			if !relevant(e) {
				continue
			}
			if e.mark && pr.top && e.step.Kind == AccessProperty && slices.Contains(keys, e.step.Name) {
				continue
			}
			matched := false
			for _, f := range byStep(pr.c)[a.stepKey(e.step)] {
				if f.mark != e.mark {
					continue
				}
				if e.mark {
					matched = marks(f)
				} else {
					next := pair{accessPair: accessPair{p: e.to, c: f.to}}
					answer, ok := known(next)
					matched = ok && answer || !ok && related[next]
				}
				if matched {
					break
				}
			}
			if !matched {
				return false
			}
		}
		return true
	}
	work := slices.Clone(explored)
	for len(work) > 0 {
		pr := work[len(work)-1]
		work = work[:len(work)-1]
		if related[pr] && !holds(pr) {
			delete(related, pr)
			work = append(work, leanOn[pr]...)
		}
	}
	for _, pr := range explored[1:] {
		decided[pr.accessPair] = related[pr]
	}
	return related[start]
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
