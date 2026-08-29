package generator

import "sort"

// This file works out how much room a generated type takes and where its
// pointers sit, so that the members of a generated struct can be declared in the
// order that wastes the least of both.
//
// Go lays a struct out in the order its fields are written: each field starts at
// the next offset its own type is allowed to begin at, and the compiler inserts
// padding to get there. `struct { flag bool; count int64 }` therefore spends
// seven bytes on nothing, where `struct { count int64; flag bool }` spends none.
// The garbage collector reads the same declaration a second way -- it scans a
// value from its start up to the last byte that could hold a pointer, so a
// pointerless field written between two pointers is scanned along with them.
// Both costs are paid by every value of the type, and neither is visible in the
// source.
//
// A generated struct has no reason to be written in any particular order: the
// order properties arrive in is the order their JSON names sort in, which says
// nothing about their types. So they are declared in the order that costs least,
// and the JSON name order is kept where it is read by people and by the decoder
// -- see StructDef.Fields, which this does not touch.
//
// The rules are cmd/compile's, transcribed from the gcSizes model that
// golang.org/x/tools' fieldalignment analyzer measures with, so that what is
// emitted is what that analyzer calls optimal. TestGeneratedCorpusIsFieldAligned
// checks that claim against the real Go runtime rather than against this model.

// The layout modelled here is the 64-bit one: an eight-byte word, and eight as
// the widest alignment any type asks for. That covers amd64, arm64 and every
// other 64-bit port, which is what generated code is built for in practice.
//
// A 32-bit build lays the same declaration out differently -- a word is four
// bytes there, and gc aligns int64 to four rather than to eight -- so an order
// chosen here is a good order there rather than a provably minimal one. Choosing
// per-architecture is not open to a generator: it emits one .go file that every
// GOARCH compiles, so the order has to be picked once, and picking it for the
// architecture the code will actually run on is the only useful choice.
const (
	layoutWordSize = 8
	layoutMaxAlign = 8
)

// goLayout is what the three questions a field's declaration position depends on
// answer to: how much room it takes, what offset it may start at, and how far
// into it the garbage collector has to scan.
//
// Ptrdata is the offset just past the last byte of the value that can hold a
// pointer -- 0 for a value with no pointer in it at all, 8 for a `string` (whose
// pointer is its first word and whose length is not scanned), 24 for a
// `time.Time` (whose *Location is its last word).
type goLayout struct {
	Size    int64
	Align   int64
	Ptrdata int64
}

// The layouts every Go type this generator emits reduces to. A named type takes
// its underlying type's, so these are the leaves of every answer below.
var (
	layoutPointer   = goLayout{Size: layoutWordSize, Align: layoutWordSize, Ptrdata: layoutWordSize}
	layoutMap       = layoutPointer
	layoutSlice     = goLayout{Size: 3 * layoutWordSize, Align: layoutWordSize, Ptrdata: layoutWordSize}
	layoutString    = goLayout{Size: 2 * layoutWordSize, Align: layoutWordSize, Ptrdata: layoutWordSize}
	layoutInterface = goLayout{Size: 2 * layoutWordSize, Align: layoutWordSize, Ptrdata: 2 * layoutWordSize}
)

// primitiveLayouts is every name a PrimitiveType can carry.
//
// The three that are not Go predeclared types are read from their own
// declarations: json.RawMessage is a []byte, json.Number a string, and time.Time
// and netip.Addr are structs whose last word is a pointer, which is why they are
// scanned to their end. Those four are facts about another package's source, so
// TestPrimitiveLayoutsMatchTheRuntime asks the running Go for each of them
// instead of trusting this table -- a standard library that rearranged one of
// them would otherwise make every struct holding it silently misordered.
var primitiveLayouts = map[string]goLayout{
	"bool":            {Size: 1, Align: 1},
	"int64":           {Size: 8, Align: 8},
	"float64":         {Size: 8, Align: 8},
	"string":          layoutString,
	"any":             layoutInterface,
	GoNumberTypeName:  layoutString,
	"json.RawMessage": layoutSlice,
	"time.Time":       {Size: 24, Align: 8, Ptrdata: 24},
	"netip.Addr":      {Size: 24, Align: 8, Ptrdata: 24},
}

// layoutAlign returns the smallest offset at or after x that a value aligned to
// a may begin at.
func layoutAlign(x, a int64) int64 {
	y := x + a - 1
	return y - y%a
}

// layoutOfStruct lays members out in the order given and reports what the struct
// they make costs.
//
// The zero-size trailing member gets a byte of its own, which is gc's rule for
// keeping a pointer to it from pointing past the end of the allocation. Nothing
// this generator emits has one; it is here so that the model is the compiler's
// model rather than the part of it these types happen to reach.
func layoutOfStruct(members []goLayout) goLayout {
	if len(members) == 0 {
		return goLayout{Align: 1}
	}
	var offset, ptrdata int64
	widest := int64(1)
	for i, m := range members {
		if m.Align > widest {
			widest = m.Align
		}
		start := layoutAlign(offset, m.Align)
		if m.Ptrdata != 0 {
			ptrdata = start + m.Ptrdata
		}
		size := m.Size
		if i == len(members)-1 && size == 0 && offset != 0 {
			size = 1
		}
		offset = start + size
	}
	return goLayout{Size: layoutAlign(offset, widest), Align: widest, Ptrdata: ptrdata}
}

// layoutOrder sorts members into the declaration order that costs least, and
// reports the permutation rather than reordering in place so that the caller can
// apply it to whatever it is holding.
//
// The comparison is fieldalignment's, keystroke for keystroke, because agreeing
// with that analyzer is the whole point: an order that is merely good still gets
// reported. In its terms:
//
//   - a zero-sized member first, where it costs nothing and can cost something
//     at the end (see layoutOfStruct);
//   - then the most tightly aligned, which is what removes the padding;
//   - then the members carrying pointers ahead of the ones that carry none, so
//     that the collector's scan stops as early as it can;
//   - among those, the ones with the least dead weight *after* their last
//     pointer first, so the member that drags the scan furthest sits at the end
//     of the pointerful run;
//   - and largest first to break what is left.
//
// The sort is stable where that comparison is indifferent, so members that are
// interchangeable keep the order they were declared in -- which for the
// properties of a struct is their JSON name order, and is what makes the emitted
// source the same from run to run.
func layoutOrder(members []goLayout) []int {
	order := make([]int, len(members))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		mi, mj := members[order[a]], members[order[b]]

		zeroi, zeroj := mi.Size == 0, mj.Size == 0
		if zeroi != zeroj {
			return zeroi
		}
		if mi.Align != mj.Align {
			return mi.Align > mj.Align
		}
		noptrsi, noptrsj := mi.Ptrdata == 0, mj.Ptrdata == 0
		if noptrsi != noptrsj {
			return noptrsj
		}
		if !noptrsi {
			if traili, trailj := mi.Size-mi.Ptrdata, mj.Size-mj.Ptrdata; traili != trailj {
				return traili < trailj
			}
		}
		return mi.Size > mj.Size
	})
	return order
}

// layoutTable answers what a generated type costs, over one generator's
// declarations -- and chooses the declaration order of every struct it measures
// on the way, because the two questions are one question.
//
// A struct's size and pointer extent depend on the order its members are written
// in, so "what does this struct cost" cannot be answered before "how will it be
// written", and a struct holding another by value cannot be ordered before the
// one it holds. Measuring is therefore what drives the ordering: reaching a
// struct measures its members, which orders and measures any struct among them
// first, and the order chosen is recorded on the declaration as it is passed.
//
// It memoises, because a type referenced by twenty properties would otherwise be
// laid out twenty times, and it refuses to answer twice about a type it is
// already in the middle of answering about. That guard is not reachable from Go
// that compiles -- a struct cannot contain itself except through a pointer, and
// a pointer is answered without looking at what it points to -- but a generator
// bug that produced such a cycle would otherwise recurse until the stack ran
// out, and reporting "not known" instead costs an unordered struct.
type layoutTable struct {
	defs     map[string]TypeDef
	memo     map[string]goLayout
	unknown  map[string]bool
	visiting map[string]bool
}

// orderMembersForLayout gives every struct this call declares the cheapest
// declaration order for its members, and reports the layout each declaration
// ended up with so that a package generated later in a cross-package run can
// place a member of one of them.
//
// It runs last, after every declaration exists and after the passes that settle
// what the members are: a struct gains and loses its unexported bookkeeping
// members from the answers those passes give, and ordering a list that is still
// being added to would order the wrong list.
func (g *Generator) orderMembersForLayout() map[string]goLayout {
	table := newLayoutTable(g.typeDefsInScope())
	layouts := make(map[string]goLayout, len(g.output.TypeDefs))
	for _, td := range g.output.TypeDefs {
		if l, ok := table.ofNamed(td.TypeName()); ok {
			layouts[td.TypeName()] = l
		}
	}
	return layouts
}

// newLayoutTable builds a table over every declaration in scope for this call,
// which in shared-types mode includes the ones earlier calls put in the package.
func newLayoutTable(defs []TypeDef) *layoutTable {
	t := &layoutTable{
		defs:     make(map[string]TypeDef, len(defs)),
		memo:     make(map[string]goLayout, len(defs)),
		unknown:  make(map[string]bool),
		visiting: make(map[string]bool),
	}
	for _, td := range defs {
		if _, seen := t.defs[td.TypeName()]; !seen {
			t.defs[td.TypeName()] = td
		}
	}
	return t
}

// of reports what a value of type gt costs, and whether that is known at all.
//
// "Not known" is a real answer rather than a failure. It is what a type whose
// declaration is not in this table gets -- a name from a package generated
// elsewhere that published no layout, or a name nothing declares -- and every
// caller responds to it the same way: leave the members in the order they were
// built in. An unordered struct is the output this generator produced before any
// of this existed, so the cost of not knowing is bounded at that.
func (t *layoutTable) of(gt GoType) (goLayout, bool) {
	switch v := gt.(type) {
	case nil:
		return goLayout{}, false
	case *PrimitiveType:
		l, ok := primitiveLayouts[v.Name]
		return l, ok
	case *PointerType:
		return layoutPointer, true
	case *ArrayType:
		return layoutSlice, true
	case *MapType:
		return layoutMap, true
	case *InterfaceType:
		return layoutInterface, true
	case *NamedType:
		if v.Pointer {
			return layoutPointer, true
		}
		if v.PkgAlias != "" {
			if !v.foreign.LayoutKnown {
				return goLayout{}, false
			}
			return v.foreign.Layout, true
		}
		return t.ofNamed(v.Name)
	}
	return goLayout{}, false
}

// ofNamed reports what the type declared under name costs.
func (t *layoutTable) ofNamed(name string) (goLayout, bool) {
	if l, ok := t.memo[name]; ok {
		return l, true
	}
	if t.unknown[name] || t.visiting[name] {
		return goLayout{}, false
	}
	td, ok := t.defs[name]
	if !ok {
		t.unknown[name] = true
		return goLayout{}, false
	}
	t.visiting[name] = true
	l, known := t.ofDef(td)
	delete(t.visiting, name)
	if !known {
		t.unknown[name] = true
		return goLayout{}, false
	}
	t.memo[name] = l
	return l, true
}

// ofDef reports what one declaration costs, by the shape its template emits.
//
// Every arm mirrors a template in pkg/emitter/templates, and the pairing is what
// keeps this honest: a template that changed the fields it writes without a
// change here would leave this reporting a size the compiler does not agree
// with. TestGeneratedCorpusIsFieldAligned is what would catch that, since it
// measures the compiled types rather than reading this.
func (t *layoutTable) ofDef(td TypeDef) (goLayout, bool) {
	switch d := td.(type) {
	case *StructDef:
		return t.ofStructDef(d)
	case *EnumDef:
		// enum.go.tmpl: `type N json.RawMessage` for the raw form, and
		// `type N <base>` for the const form.
		if d.IsRaw {
			return layoutSlice, true
		}
		return t.of(d.BaseType)
	case *AliasDef:
		// alias.go.tmpl: `type N <underlying>`.
		return t.of(d.Underlying)
	case *InferredAliasDef:
		order, layouts, ok := t.order(d.builtMembers())
		if !ok {
			return goLayout{}, false
		}
		d.memberOrder = order
		return layoutOfStruct(layouts), true
	case *BigIntAliasDef:
		// bigint_alias.go.tmpl writes these four in this order, which is
		// already the cheapest one for every configuration: the *big.Int is
		// the only member carrying a pointer, so it leads, and the two flags
		// pack into the tail. Nothing about it depends on the schema, so it is
		// written into the template rather than chosen here.
		members := []goLayout{layoutPointer, {Size: 8, Align: 8}, {Size: 1, Align: 1}}
		if d.AllowsNull {
			members = append(members, goLayout{Size: 1, Align: 1})
		}
		return layoutOfStruct(members), true
	case *AnnotationSchemaDef, *DynamicSchemaDef, *NotSchemaDef, *TypeOnlySchemaDef:
		// Each of these is `struct { _raw json.RawMessage }`.
		return layoutOfStruct([]goLayout{layoutSlice}), true
	}
	return goLayout{}, false
}

// ofStructDef chooses the declaration order of a generated struct and reports
// what it costs in that order.
func (t *layoutTable) ofStructDef(d *StructDef) (goLayout, bool) {
	order, layouts, ok := t.order(d.builtMembers())
	if !ok {
		return goLayout{}, false
	}
	d.memberOrder = order
	own := layoutOfStruct(layouts)
	// The throwaway struct MarshalJSON builds embeds this one and adds raw
	// bytes beside it; which goes first is decided by the same comparison, over
	// the two of them. See StructDef.MarshalAuxRawFirst.
	d.marshalAuxRawFirst = layoutOrder([]goLayout{own, layoutSlice})[0] == 1
	return own, true
}

// order measures each member, picks the declaration order they cost least in,
// and returns that order together with the members' layouts already permuted
// into it.
//
// A single member whose type this table cannot measure gives up on the whole
// struct: an order chosen around a hole in the middle of it is not an order
// anything can vouch for, and the built order is a perfectly good answer.
func (t *layoutTable) order(members []StructMember) ([]int, []goLayout, bool) {
	built := make([]goLayout, len(members))
	for i, m := range members {
		l, ok := t.of(m.Type)
		if !ok {
			return nil, nil, false
		}
		built[i] = l
	}
	order := layoutOrder(built)
	ordered := make([]goLayout, len(order))
	for i, index := range order {
		ordered[i] = built[index]
	}
	return order, ordered, true
}
