package tests

import (
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// defaultPresenceSchema is the fixture the tests below read. It carries the
// shapes testdata/fixtures/defaults/server_config.json structurally cannot: a
// property that is required (so never a pointer, in any configuration), a
// boolean, an array, a map, a named scalar, a named collection, a nested object
// reached directly and through both a map value and an array element -- and,
// beside them, the two targets that must still get nothing.
const defaultPresenceSchema = "testdata/schemas/regression/default_presence_positions.json"

// defaultPresenceProgram is the generated main() the three configuration tests
// below share, unchanged between them.
//
// The matrix is asserted over the marshalled document rather than over the Go
// fields, because the fields are not the same in every configuration --
// "optStr" is a *string under the default and a string under --omit-empty=false,
// and "reqInt" is an int64 under both and a wrapper struct under --big-int --
// while the JSON a caller sees is the same in all three. That is also the claim
// the two issues make: a default is what the document did not say, and what the
// document did say survives.
const defaultPresenceProgram = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// marshalledKeys reads a value back as the JSON text of each of its properties.
func marshalledKeys(v *DefaultPresencePositions) map[string]string {
	out, err := json.Marshal(v)
	if err != nil {
		fail("marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		fail("re-reading %s: %v", out, err)
	}
	got := map[string]string{}
	for k, v := range raw {
		got[k] = string(v)
	}
	return got
}

func check(label string, got, want map[string]string) {
	names := make([]string, 0, len(want))
	for k := range want {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		g, ok := got[k]
		if !ok {
			fail("%s: %q is not in the output at all", label, k)
		}
		if g != want[k] {
			fail("%s: %q is %s, want %s", label, k, g, want[k])
		}
	}
}

func decode(label, doc string) *DefaultPresencePositions {
	var v DefaultPresencePositions
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		fail("%s: decoding %s: %v", label, doc, err)
	}
	return &v
}

// The value of every defaulted property, as the schema states it.
var defaults = map[string]string{
	"reqStr":     "\"dflt\"",
	"reqBool":    "true",
	"reqInt":     "7",
	"reqNum":     "1.5",
	"reqArr":     "[\"z\"]",
	"reqMap":     "{\"k\":\"v\"}",
	"reqNamed":   "\"named\"",
	"optStr":     "\"dflt\"",
	"optBool":    "true",
	"optInt":     "7",
	"optNum":     "1.5",
	"optArr":     "[\"z\"]",
	"optMap":     "{\"k\":\"v\"}",
	"namedArr":   "[\"t\"]",
	"anyArr":     "[\"z\",1,true]",
	"anyMap":     "{\"a\":\"b\",\"k\":\"v\"}",
	"arrOfNamed": "[\"q\"]",
	"mapOfArr":   "{\"a\":[1,2]}",
}

// The same properties written at the Go zero of whatever holds them. Every one
// is a value the schema permits and a caller may mean.
const zeroDocument = "{" +
	"\"reqStr\":\"\",\"reqBool\":false,\"reqInt\":0,\"reqNum\":0," +
	"\"reqArr\":[],\"reqMap\":{},\"reqNamed\":\"\"," +
	"\"optStr\":\"\",\"optBool\":false,\"optInt\":0,\"optNum\":0," +
	"\"optArr\":[],\"optMap\":{}," +
	"\"namedArr\":[],\"anyArr\":[],\"anyMap\":{}," +
	"\"arrOfNamed\":[],\"mapOfArr\":{}}"

var zeros = map[string]string{
	"reqStr":     "\"\"",
	"reqBool":    "false",
	"reqInt":     "0",
	"reqNum":     "0",
	"reqArr":     "[]",
	"reqMap":     "{}",
	"reqNamed":   "\"\"",
	"optStr":     "\"\"",
	"optBool":    "false",
	"optInt":     "0",
	"optNum":     "0",
	"optArr":     "[]",
	"optMap":     "{}",
	"namedArr":   "[]",
	"anyArr":     "[]",
	"anyMap":     "{}",
	"arrOfNamed": "[]",
	"mapOfArr":   "{}",
}

// leafView is how each of the three nested positions is read back, so that the
// assertion does not depend on the order the generated MarshalJSON writes the
// two properties in. Both are pointers so that "written at its zero" and "not
// written at all" stay distinguishable here too.
type leafView struct {
	Ls *string ` + "`json:\"ls\"`" + `
	Lb *bool   ` + "`json:\"lb\"`" + `
}

func (l leafView) check(name string) {
	if l.Ls == nil {
		fail("%s: ls is missing from the output entirely", name)
	}
	if *l.Ls != "" {
		fail("%s: SetDefaults replaced an explicit empty string with %q", name, *l.Ls)
	}
	if l.Lb == nil || !*l.Lb {
		fail("%s: SetDefaults did not write the absent boolean's default", name)
	}
}

func main() {
	// Absent: every default is written. Under --omit-empty=false this is the
	// half a fix that merely stopped writing into non-pointer fields would
	// break, since nothing there is a pointer.
	absent := decode("absent", "{}")
	absent.SetDefaults()
	check("absent", marshalledKeys(absent), defaults)

	// Present at the zero value: nothing is written. This is issue #248 --
	// under the old writer every one of these came back as the default.
	present := decode("present-at-zero", zeroDocument)
	present.SetDefaults()
	check("present at the zero value", marshalledKeys(present), zeros)

	// The two properties nothing may be written for: a default whose target is
	// a struct has no literal, and neither has a string default on an integer
	// field. Under --omit-empty=false both fields are written out at their
	// zero, so it is the value and not the key that says so.
	out, err := json.Marshal(absent)
	if err != nil {
		fail("marshal: %v", err)
	}
	if strings.Contains(string(out), "\"ls\":\"x\"") {
		fail("structDflt: SetDefaults wrote a default no literal spells: %s", out)
	}
	if got, ok := marshalledKeys(absent)["mismatch"]; ok && got != "0" {
		fail("mismatch: SetDefaults wrote %s for a string default on an integer field", got)
	}
	// The same rule one level down: a default element the item type does not
	// admit takes the whole literal with it, because a composite that dropped
	// the element would be an array the schema never stated.
	if got, ok := marshalledKeys(absent)["arrMismatch"]; ok && got != "null" {
		fail("arrMismatch: SetDefaults wrote %s for an element the item type refuses", got)
	}

	// An explicit null is a value the document carried, and a collection is
	// exactly where it is indistinguishable from an absent property once
	// decoded: a JSON null and no key at all both leave a nil slice behind. The
	// key set is the only thing that tells them apart, and the marshalled form
	// cannot -- the null is written back from the record UnmarshalJSON keeps --
	// so this one is read off the field.
	explicitNull := decode("explicit null", "{\"nullArr\":null}")
	explicitNull.SetDefaults()
	if explicitNull.NullArr != nil {
		fail("nullArr: SetDefaults replaced an explicit null with %v", explicitNull.NullArr)
	}
	absentNull := decode("absent null", "{}")
	absentNull.SetDefaults()
	if len(absentNull.NullArr) != 1 || absentNull.NullArr[0] != "z" {
		fail("nullArr: an absent nullable array got %v, want [z]", absentNull.NullArr)
	}

	// A value that never came from JSON has no key set, so the Go zero is the
	// only signal there is: an empty one takes every default.
	var built DefaultPresencePositions
	built.SetDefaults()
	check("hand-built empty", marshalledKeys(&built), defaults)

	// ...and one constructed with values keeps them, which is why the zero test
	// stays as the fallback rather than being removed outright.
	kept := DefaultPresencePositions{}
	kept.ReqStr = "kept"
	kept.ReqNamed = Label("kept")
	kept.ReqArr = []string{"kept"}
	kept.SetDefaults()
	check("hand-built with values", marshalledKeys(&kept), map[string]string{
		"reqStr":   "\"kept\"",
		"reqNamed": "\"kept\"",
		"reqArr":   "[\"kept\"]",
	})

	// A nested object, a map value and an array element each decode through
	// their own UnmarshalJSON and so record their own key set. "ls" is written
	// at its zero and must survive; "lb" is absent and must be defaulted.
	nested := decode("nested",
		"{\"leaf\":{\"ls\":\"\"},\"leafMap\":{\"a\":{\"ls\":\"\"}},\"leafArr\":[{\"ls\":\"\"}]}")
	nested.Leaf.SetDefaults()
	fromMap := nested.LeafMap["a"]
	fromMap.SetDefaults()
	nested.LeafMap["a"] = fromMap
	nested.LeafArr[0].SetDefaults()

	nestedKeys := marshalledKeys(nested)
	var direct leafView
	if err := json.Unmarshal([]byte(nestedKeys["leaf"]), &direct); err != nil {
		fail("re-reading leaf: %v", err)
	}
	direct.check("nested object")
	var inMap map[string]leafView
	if err := json.Unmarshal([]byte(nestedKeys["leafMap"]), &inMap); err != nil {
		fail("re-reading leafMap: %v", err)
	}
	inMap["a"].check("map value")
	var inArr []leafView
	if err := json.Unmarshal([]byte(nestedKeys["leafArr"]), &inArr); err != nil {
		fail("re-reading leafArr: %v", err)
	}
	inArr[0].check("array element")

	fmt.Println("PASS")
}
`

// TestDefaultPresenceUnderDefaultConfig is issues #248 and #251 read under the
// configuration both were reported in.
//
// The distinction it draws is the whole of #248: a property the document did not
// carry gets its default, and a property the document carried at its zero keeps
// what the document said. A required property is where the two used to be the
// same test, because "required" is exactly what takes the pointer away.
//
// The array and map cells are #251's second half, which no configuration
// emitted at all.
func TestDefaultPresenceUnderDefaultConfig(t *testing.T) {
	runGeneratedMainProgramWithConfig(t,
		defaultPresenceSchema,
		"default_presence_test",
		defaultPresenceProgram,
		generator.Config{PackageName: "testpkg", OmitEmpty: true},
	)
}

// TestDefaultPresenceWithoutOmitEmpty is the sharper half of #248: with no
// omitempty nothing is pointer-wrapped, so every property of every type has a
// zero that an explicit value shares -- and false is a value nobody writes by
// accident.
//
// It is also the half that could be broken in the other direction, which is why
// the absent cells are asserted here too: a fix that simply stopped writing into
// non-pointer fields would satisfy #248 and disable defaults outright under this
// flag.
func TestDefaultPresenceWithoutOmitEmpty(t *testing.T) {
	runGeneratedMainProgramWithConfig(t,
		defaultPresenceSchema,
		"default_presence_noomit_test",
		defaultPresenceProgram,
		generator.Config{PackageName: "testpkg", OmitEmpty: false},
	)
}

// TestDefaultAloneAsksForTheKeySet is why "default" is one of the things
// StructDef.NeedsJSONKeys answers for.
//
// The fixture states nothing else: no required list, no dependent schema, no
// optional-property rule -- every other reason a struct records the document's
// keys. Under --omit-empty=false neither property is a pointer, so both need the
// key set and nothing else asks for it, and a generated file that referred to a
// _jsonKeys its own struct does not declare would not compile at all. That is
// what this run proves, beside the two verdicts.
func TestDefaultAloneAsksForTheKeySet(t *testing.T) {
	runGeneratedMainProgramWithConfig(t,
		"testdata/schemas/regression/default_sole_keyword.json",
		"default_sole_keyword_test",
		`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	var absent DefaultSoleKeyword
	if err := json.Unmarshal([]byte("{}"), &absent); err != nil {
		fail("decoding the empty document: %v", err)
	}
	absent.SetDefaults()
	if absent.S != "d" || !absent.B {
		fail("absent: got %q/%v, want \"d\"/true", absent.S, absent.B)
	}

	var present DefaultSoleKeyword
	if err := json.Unmarshal([]byte("{\"s\":\"\",\"b\":false}"), &present); err != nil {
		fail("decoding the zero document: %v", err)
	}
	present.SetDefaults()
	if present.S != "" || present.B {
		fail("present at the zero value: got %q/%v, want \"\"/false", present.S, present.B)
	}

	fmt.Println("PASS")
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
`,
		generator.Config{PackageName: "testpkg", OmitEmpty: false},
	)
}

// TestSetDefaultsStillCannotSatisfyARequiredReadOnlyProperty is issue #176 held
// in place while #248 changes what SetDefaults reads.
//
// The two rules refuse every document between them: --strict-read-write makes
// the decoder reject one that sets the readOnly property, and Validate rejects
// one whose keys lack a required property. SetDefaults now reads those same keys
// -- so a writer that filled the gap by recording the key it had just defaulted
// would turn a warned-about, unsatisfiable type into one that silently passes,
// on a property the document was never allowed to send.
//
// The property does still get its default, which is the other half: the field is
// assigned, and the required check is not about the field's value.
func TestSetDefaultsStillCannotSatisfyARequiredReadOnlyProperty(t *testing.T) {
	runGeneratedMainProgramWithConfig(t,
		"testdata/schemas/regression/readonly_required_default.json",
		"readonly_required_default_test",
		`package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	// A document that sets the readOnly property does not decode at all.
	var set ReadOnlyRequiredDefault
	if err := json.Unmarshal([]byte("{\"id\":\"x\"}"), &set); err == nil {
		fail("a document setting the readOnly property decoded")
	} else if !strings.Contains(err.Error(), "read-only") {
		fail("it was refused for the wrong reason: %v", err)
	}

	// One that omits it decodes, and Validate refuses it.
	var omitted ReadOnlyRequiredDefault
	if err := json.Unmarshal([]byte("{\"name\":\"n\"}"), &omitted); err != nil {
		fail("a document omitting the readOnly property did not decode: %v", err)
	}
	if err := omitted.Validate(); err == nil || !strings.Contains(err.Error(), "id") {
		fail("Validate accepted a document with no id: %v", err)
	}

	// SetDefaults writes the field and changes nothing about that.
	omitted.SetDefaults()
	if omitted.ID != "auto" {
		fail("SetDefaults did not write the default: %q", omitted.ID)
	}
	if err := omitted.Validate(); err == nil || !strings.Contains(err.Error(), "id") {
		fail("SetDefaults made an unsatisfiable required property pass Validate: %v", err)
	}

	fmt.Println("PASS")
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
`,
		generator.Config{PackageName: "testpkg", OmitEmpty: true, StrictReadWrite: true},
	)
}

// TestDefaultPresenceUnderBigInt is #251's first half. --big-int materializes
// every schema integer into a wrapper struct, which defaultToGoLiteral -- which
// answers from the Go type name and knows four scalars -- had nothing to say
// about, so the integer default was dropped with no diagnostic while the same
// schema under the default configuration emitted it.
func TestDefaultPresenceUnderBigInt(t *testing.T) {
	runGeneratedMainProgramWithConfig(t,
		defaultPresenceSchema,
		"default_presence_bigint_test",
		defaultPresenceProgram,
		generator.Config{PackageName: "testpkg", OmitEmpty: true, BigIntSupport: true},
	)
}
