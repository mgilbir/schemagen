package generator

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"
	"testing"
	"time"
)

// The layout model in layout.go is a transcription: of cmd/compile's rules for
// where a field goes, and of four standard library declarations it cannot see.
// A transcription is worth exactly what checks it, so the tests here measure the
// same types with the running Go and compare.
//
// runtimeLayout is deliberately not written in terms of anything in layout.go.
// It walks reflect.Type, which reports what the compiler actually did, so the
// two arrive at their answers by different routes and agreeing means something.

// runtimePtrdata is gc's ptrdata over a real type: the offset just past the last
// byte of the value that can hold a pointer.
func runtimePtrdata(t reflect.Type) int64 {
	switch t.Kind() {
	case reflect.String, reflect.UnsafePointer,
		reflect.Chan, reflect.Map, reflect.Pointer, reflect.Func, reflect.Slice:
		return int64(goArchWordSize)
	case reflect.Interface:
		return 2 * int64(goArchWordSize)
	case reflect.Array:
		n := int64(t.Len())
		if n == 0 {
			return 0
		}
		elem := runtimePtrdata(t.Elem())
		if elem == 0 {
			return 0
		}
		return (n-1)*int64(t.Elem().Size()) + elem
	case reflect.Struct:
		var last int64
		for i := range t.NumField() {
			f := t.Field(i)
			if p := runtimePtrdata(f.Type); p != 0 {
				last = int64(f.Offset) + p
			}
		}
		return last
	}
	return 0
}

func runtimeLayout(t reflect.Type) goLayout {
	return goLayout{
		Size:    int64(t.Size()),
		Align:   int64(t.Align()),
		Ptrdata: runtimePtrdata(t),
	}
}

// goArchWordSize is the word size of the architecture the test is running on,
// worked out from a pointer rather than assumed.
var goArchWordSize = reflect.TypeOf((*int)(nil)).Size()

// skipOff64Bit skips a test that can only speak for the layout layout.go models,
// which is the 64-bit one. Nothing in this repository is built for a 32-bit
// target today; the guard is here so that if something ever is, these read as
// "not measured here" instead of as a wall of failures about a model that was
// never claiming to describe that architecture.
func skipOff64Bit(t *testing.T) {
	t.Helper()
	if goArchWordSize != layoutWordSize {
		t.Skipf("layout.go models a %d-byte word; this is a %d-byte one", layoutWordSize, goArchWordSize)
	}
}

// TestPrimitiveLayoutsMatchTheRuntime holds the table of primitive layouts
// against the types it claims to describe.
//
// Four of its entries are facts about another package's source rather than about
// the Go language: json.RawMessage, json.Number, time.Time and netip.Addr. Those
// are the ones this test is really for -- netip.Addr's second word was an
// interned-value pointer and is now a unique.Handle, and a version of Go that
// rearranged one of them would otherwise leave every generated struct holding it
// ordered by a size the compiler does not agree with, silently and with no
// symptom short of running the analyzer.
func TestPrimitiveLayoutsMatchTheRuntime(t *testing.T) {
	skipOff64Bit(t)

	real := map[string]reflect.Type{
		"bool":            reflect.TypeOf(false),
		"int64":           reflect.TypeOf(int64(0)),
		"float64":         reflect.TypeOf(float64(0)),
		"string":          reflect.TypeOf(""),
		"any":             reflect.TypeOf((*any)(nil)).Elem(),
		GoNumberTypeName:  reflect.TypeOf(json.Number("")),
		"json.RawMessage": reflect.TypeOf(json.RawMessage(nil)),
		"time.Time":       reflect.TypeOf(time.Time{}),
		"netip.Addr":      reflect.TypeOf(netip.Addr{}),
	}

	for name, modelled := range primitiveLayouts {
		rt, ok := real[name]
		if !ok {
			t.Errorf("primitiveLayouts describes %q, which this test does not measure. A modelled size nothing "+
				"compares against the real type is a number nobody can see go stale", name)
			continue
		}
		if got := runtimeLayout(rt); got != modelled {
			t.Errorf("%s: modelled %+v, runtime says %+v", name, modelled, got)
		}
	}
	for name := range real {
		if _, ok := primitiveLayouts[name]; !ok {
			t.Errorf("this test measures %q, which primitiveLayouts no longer names", name)
		}
	}
}

// TestCompositeLayoutsMatchTheRuntime does the same for the shapes that are not
// named in the table: a pointer, a slice, a map and an interface, which every
// generated container reduces to.
func TestCompositeLayoutsMatchTheRuntime(t *testing.T) {
	skipOff64Bit(t)

	for _, c := range []struct {
		what     string
		modelled goLayout
		rt       reflect.Type
	}{
		{"pointer", layoutPointer, reflect.TypeOf((*string)(nil))},
		{"slice", layoutSlice, reflect.TypeOf([]string(nil))},
		{"map", layoutMap, reflect.TypeOf(map[string]any(nil))},
		{"string", layoutString, reflect.TypeOf("")},
		{"interface", layoutInterface, reflect.TypeOf((*any)(nil)).Elem()},
	} {
		if got := runtimeLayout(c.rt); got != c.modelled {
			t.Errorf("%s: modelled %+v, runtime says %+v", c.what, c.modelled, got)
		}
	}
}

// TestStructLayoutMatchesTheRuntime checks the assembly rule -- the padding
// between one member and the next, the round-up at the end, and where the
// pointer extent lands -- against real structs of the same shapes.
//
// The cases are the ones a generated struct is made of: a required scalar and an
// optional pointer to one, an overflow map, the raw bytes a non-object hatch
// keeps, and the bookkeeping flags. The badly-ordered ones are included on
// purpose: the model has to be right about the layout a *bad* order produces,
// since that is the thing the ordering is chosen to improve on.
func TestStructLayoutMatchesTheRuntime(t *testing.T) {
	skipOff64Bit(t)

	for _, c := range []struct {
		what     string
		members  []goLayout
		rt       reflect.Type
		wantSame bool
	}{
		{
			what:    "empty",
			members: nil,
			rt:      reflect.TypeOf(struct{}{}),
		},
		{
			what:    "flag between two pointers",
			members: []goLayout{layoutPointer, {Size: 1, Align: 1}, layoutPointer},
			rt: reflect.TypeOf(struct {
				a *int64
				b bool
				c map[string]bool
			}{}),
		},
		{
			what:    "the same three in the cheap order",
			members: []goLayout{layoutPointer, layoutPointer, {Size: 1, Align: 1}},
			rt: reflect.TypeOf(struct {
				a *int64
				c map[string]bool
				b bool
			}{}),
		},
		{
			what:    "strings, raw bytes and flags",
			members: []goLayout{layoutString, layoutSlice, {Size: 1, Align: 1}, {Size: 1, Align: 1}},
			rt: reflect.TypeOf(struct {
				s  string
				r  json.RawMessage
				f1 bool
				f2 bool
			}{}),
		},
		{
			what:    "no pointer anywhere",
			members: []goLayout{{Size: 8, Align: 8}, {Size: 1, Align: 1}},
			rt: reflect.TypeOf(struct {
				n int64
				f bool
			}{}),
		},
		{
			what:    "a time and an interface",
			members: []goLayout{primitiveLayouts["time.Time"], layoutInterface, {Size: 1, Align: 1}},
			rt: reflect.TypeOf(struct {
				t time.Time
				v any
				f bool
			}{}),
		},
	} {
		got := layoutOfStruct(c.members)
		if want := runtimeLayout(c.rt); got != want {
			t.Errorf("%s: modelled %+v, runtime says %+v", c.what, got, want)
		}
	}
}

// TestLayoutOrderIsTheCheapestOrder checks the comparator against the thing it
// is for, rather than against itself: for every permutation of a handful of
// members, no permutation lays out smaller or with a shorter pointer extent than
// the one layoutOrder picks.
//
// Brute force is affordable at these sizes and is the only check that does not
// simply restate the sort. The two quantities are ranked the way fieldalignment
// ranks them -- size first, and pointer extent only where the sizes tie -- so
// that "cheapest" here means what it means there.
func TestLayoutOrderIsTheCheapestOrder(t *testing.T) {
	sets := [][]goLayout{
		{layoutPointer, {Size: 1, Align: 1}, layoutPointer},
		{layoutString, {Size: 1, Align: 1}, layoutSlice, {Size: 8, Align: 8}},
		{{Size: 8, Align: 8}, layoutSlice, layoutMap, {Size: 1, Align: 1}, layoutString},
		{layoutInterface, primitiveLayouts["time.Time"], {Size: 1, Align: 1}, layoutPointer},
		{{Size: 1, Align: 1}, {Size: 1, Align: 1}, {Size: 1, Align: 1}},
		{layoutSlice},
		{},
	}

	for _, members := range sets {
		chosen := permute(members, layoutOrder(members))
		got := layoutOfStruct(chosen)
		for _, order := range permutations(len(members)) {
			alt := layoutOfStruct(permute(members, order))
			if alt.Size < got.Size || (alt.Size == got.Size && alt.Ptrdata < got.Ptrdata) {
				t.Errorf("layoutOrder chose %+v for %s, but the order %v gives %+v, which is cheaper",
					got, describe(members), order, alt)
				break
			}
		}
	}
}

// TestLayoutOrderIsStable pins the tie-break, which is what makes the emitted
// source the same from one run to the next: members the comparison cannot tell
// apart keep the order they arrived in, and for the properties of a struct that
// is their JSON name order.
func TestLayoutOrderIsStable(t *testing.T) {
	members := []goLayout{layoutPointer, layoutPointer, layoutString, layoutPointer, layoutString}
	order := layoutOrder(members)
	want := []int{0, 1, 3, 2, 4} // the three pointers, in order; then the two strings, in order
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func permute[T any](in []T, order []int) []T {
	out := make([]T, len(order))
	for i, index := range order {
		out[i] = in[index]
	}
	return out
}

func describe(members []goLayout) string {
	return fmt.Sprintf("%d members %+v", len(members), members)
}

// permutations lists every ordering of n items. n stays small here by
// construction; the sets above are hand-written.
func permutations(n int) [][]int {
	if n == 0 {
		return [][]int{{}}
	}
	var out [][]int
	for _, rest := range permutations(n - 1) {
		for at := 0; at <= len(rest); at++ {
			one := make([]int, 0, n)
			one = append(one, rest[:at]...)
			one = append(one, n-1)
			one = append(one, rest[at:]...)
			out = append(out, one)
		}
	}
	return out
}
