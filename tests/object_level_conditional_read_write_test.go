package tests

import (
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// TestStrictWriteOnlyBindsThroughAConditionalWrittenOnTheObject is the position
// the sibling fixture cannot express, and the one a user is most likely to write:
//
//	{"type":"object","properties":{"t":{"type":"integer"}},
//	 "if":{"required":["t"]},
//	 "then":{"properties":{"secret":{"type":"string","writeOnly":true}}}}
//
// strict_conditional_read_write.json puts each applicator on a *property's*
// schema, where the value is held as raw JSON and the path table answers. That is
// a real position and it passed -- and it says nothing at all about this one,
// because here the marked property is a member of the object the applicator is
// written on, and three separate readings all miss it:
//
//   - the field loop cannot, because "secret" is named by no schema that applies
//     on every valid instance and so becomes no field: it arrives in the overflow
//     map, where a Go field's key list has nothing to key on;
//   - mergedPropertyOrigins cannot, because that record is written by the allOf
//     and variant merges and a conditional written directly on the object goes
//     through neither;
//   - accessRulesFor reaches it and then drops it, because a struct asks for
//     minDepth 2 on the ground that its own members are covered by the key lists,
//     which is true of its fields and not of this.
//
// writeOnlyKeysAtLocation is what closes it, by naming every writeOnly property
// at this location in the writeOnly key list -- where the encoder deletes them
// after the overflow map has been merged in, so a property that never became a
// field goes just the same.
//
// readOnly gets no equivalent and every position here says so twice: the document
// setting the readOnly member decodes, and that member comes back out. Refusing
// it would be the §7.7.1 false rejection, and allOfObj and inlineObj are the
// controls that say the refusal still happens where the schema states the keyword
// unconditionally.
func TestStrictWriteOnlyBindsThroughAConditionalWrittenOnTheObject(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// branched names the object-level applicators that describe the members only
// sometimes. unconditional names the two that describe them always.
var branched = []string{"anyOfObj", "oneOfObj", "ifObj", "thenObj", "elseObj", "depObj", "notObj", "notNotObj", "declaredThenObj"}
var unconditional = []string{"allOfObj", "inlineObj"}

func decode(doc string) (ObjectLevelConditionalReadWrite, error) {
	var v ObjectLevelConditionalReadWrite
	err := json.Unmarshal([]byte(doc), &v)
	return v, err
}

// held re-reads one position out of a marshalled document.
func held(out []byte, p string) map[string]any {
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		fail("re-reading the output at %s: %v", p, err)
	}
	inner, ok := got[p].(map[string]any)
	if !ok {
		fail("strict mode dropped the whole of %q, which nothing marks writeOnly: %s", p, out)
	}
	return inner
}

func main() {
	// writeOnly is stripped at every object-level applicator. "seen" and "t" are
	// in the same document throughout: a strip that deleted the property, or
	// reached past the member it names, would satisfy "secret is gone" and be
	// wrong about everything else -- and "seen" in particular is the readOnly
	// member, which must survive the encoder.
	for _, p := range append(append([]string{}, branched...), unconditional...) {
		in := fmt.Sprintf("{%q:{\"t\":1,\"secret\":\"hunter2\",\"lim\":3}}", p)
		v, err := decode(in)
		if err != nil {
			fail("decoding the writeOnly document at %s: %v", p, err)
		}
		out, err := json.Marshal(v)
		if err != nil {
			fail("marshaling at %s: %v", p, err)
		}
		inner := held(out, p)
		if _, leaked := inner["secret"]; leaked {
			fail("strict mode wrote the writeOnly member at %s: %s", p, out)
		}
		for _, kept := range []string{"t", "lim"} {
			if _, ok := inner[kept]; !ok {
				fail("strict mode dropped %q at %s, which carries no annotation: %s", kept, p, out)
			}
		}
	}

	// The root object is the one that is not reached through a property, so it is
	// asserted on its own. Its own if/then marks rootSecret writeOnly, and the
	// property exists nowhere else in the schema.
	rv, err := decode(` + "`" + `{"rootTrigger":1,"rootSecret":"hunter2","rootSeen":"s"}` + "`" + `)
	if err != nil {
		fail("decoding the root-level writeOnly document: %v", err)
	}
	rootOut, err := json.Marshal(rv)
	if err != nil {
		fail("marshaling the root-level writeOnly document: %v", err)
	}
	if strings.Contains(string(rootOut), "rootSecret") {
		fail("strict mode wrote a property the root's own \"then\" marks writeOnly: %s", rootOut)
	}
	for _, kept := range []string{"rootTrigger", "rootSeen"} {
		if !strings.Contains(string(rootOut), kept) {
			fail("strict mode dropped %q from the root: %s", kept, rootOut)
		}
	}

	// readOnly binds at none of the branched positions, and it is asserted twice
	// at each: the document decodes, and the member comes back out. Each position
	// on its own, so one that acquires the refusal names itself.
	for _, p := range branched {
		doc := fmt.Sprintf("{%q:{\"t\":1,\"seen\":\"s\"}}", p)
		v, err := decode(doc)
		if err != nil {
			fail("strict mode refused a document the schema accepts at %s: "+
				"readOnly inside a conditional binds nothing (2020-12 section 7.7.1): %v", p, err)
		}
		out, err := json.Marshal(v)
		if err != nil {
			fail("marshaling the readOnly document at %s: %v", p, err)
		}
		if _, ok := held(out, p)["seen"]; !ok {
			fail("strict mode stripped %s.seen, which a branch marks readOnly and not writeOnly: %s", p, out)
		}
	}
	if _, err := decode(` + "`" + `{"rootTrigger":1,"rootSeen":"s"}` + "`" + `); err != nil {
		fail("strict mode refused a document whose root \"then\" marks a property readOnly: %v", err)
	}

	// And it does bind where the object states the keyword unconditionally, which
	// is what tells "readOnly declines a branch" apart from "readOnly is off".
	for _, p := range unconditional {
		doc := fmt.Sprintf("{%q:{\"t\":1,\"seen\":\"s\"}}", p)
		_, err := decode(doc)
		if err == nil {
			fail("strict mode decoded a document setting a readOnly property at %s", p)
		}
		if !strings.Contains(err.Error(), "read-only property may not be set") {
			fail("decoding at %s failed for the wrong reason: %v", p, err)
		}
	}

	// No verdict moves. Both keywords are annotations and Validate consults
	// neither; "lim" is the real constraint beside them, so a branch that applies
	// still enforces it and a document carrying the two marked members still
	// validates.
	for _, p := range append(append([]string{}, branched...), unconditional...) {
		annotated := fmt.Sprintf("{%q:{\"t\":1,\"secret\":\"hunter2\",\"seen\":\"s\",\"lim\":5}}", p)
		v, err := decode(annotated)
		if err != nil && !contains(unconditional, p) {
			fail("decoding %s: %v", annotated, err)
		}
		if err == nil {
			if verr := v.Validate(); verr != nil {
				fail("Validate rejected %s, which the schema permits: %v", annotated, verr)
			}
		}
	}
	// The branches that do apply to this document still enforce what they state,
	// which is what says the strip did not cost the schema its own reading.
	for _, p := range []string{"thenObj", "depObj", "notNotObj", "allOfObj", "inlineObj", "declaredThenObj"} {
		doc := fmt.Sprintf("{%q:{\"t\":1,\"lim\":99}}", p)
		v, err := decode(doc)
		if err != nil {
			fail("decoding %s: %v", doc, err)
		}
		verdict := v.Validate()
		if verdict == nil {
			fail("Validate accepted %s, which exceeds the maximum the branch states", doc)
		}
		if strings.Contains(verdict.Error(), "read-only") || strings.Contains(verdict.Error(), "write-only") {
			fail("Validate at %s answered with an annotation, which constrains no document: %v", p, verdict)
		}
	}

	fmt.Println("PASS")
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
`
	runGeneratedMainProgramWithConfig(t,
		"testdata/schemas/regression/object_level_conditional_read_write.json",
		"object_level_conditional_read_write_test",
		mainGo,
		generator.Config{PackageName: "testpkg", OmitEmpty: true, StrictReadWrite: true},
	)
}

// TestObjectLevelWriteOnlyKeyListNamesEachKeyOnce reads the generated source
// rather than running it, because what it is about cannot be run.
//
// Two readings now feed the writeOnly key list -- the field loop, and
// writeOnlyKeysAtLocation, whose reach starts at the object itself and so covers
// the same ground before it covers any branch. A property that is a field and is
// marked writeOnly outright is therefore found twice; inlineObj and allOfObj are
// both that shape. Deleting a key twice is idempotent, so no behaviour
// distinguishes the deduplicated list from the repeated one and no running
// program can watch it. What is left is the generated source, where a delete loop
// naming the same key twice reads as a defect and would be filed as one.
//
// Every list in the file is checked rather than one chosen by name, because which
// type has the overlap is a property of the fixture and not something a reader of
// this test should have to know. The sort is asserted beside it, on the same
// ground as TestStrictReadWriteKeyListsAreSorted: a list whose order came from
// which reading found the key first would change between runs of one input.
func TestObjectLevelWriteOnlyKeyListNamesEachKeyOnce(t *testing.T) {
	src := string(generateFromSchemaWithConfig(t,
		"testdata/schemas/regression/object_level_conditional_read_write.json",
		generator.Config{PackageName: "testpkg", OmitEmpty: true, StrictReadWrite: true},
	))
	const opener = "_woKey := range []string{"
	lines := strings.Split(src, "\n")
	lists := 0
	for i, line := range lines {
		if !strings.Contains(line, opener) {
			continue
		}
		lists++
		var keys []string
		for _, l := range lines[i+1:] {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "}") {
				break
			}
			keys = append(keys, strings.Trim(strings.TrimSuffix(l, ","), `"`))
		}
		seen := map[string]int{}
		for _, k := range keys {
			seen[k]++
		}
		for k, n := range seen {
			if n > 1 {
				t.Errorf("the writeOnly key %q is named %d times in one delete loop: %v", k, n, keys)
			}
		}
		if !sort.StringsAreSorted(keys) {
			t.Errorf("a writeOnly key list is not in sorted order: %v", keys)
		}
	}
	// The floor: a fixture that stopped emitting key lists at all would pass every
	// check above without asserting anything.
	if lists < 8 {
		t.Errorf("the strict generation emitted %d writeOnly key lists; the fixture has a position for each of "+
			"anyOf, oneOf, if, then, else, dependentSchemas, allOf and inline, so this is not the whole matrix", lists)
	}
}

// TestObjectLevelConditionalReadWriteIsDocumentationByDefault is the other
// setting: none of the above may reach the decoder or the encoder without the
// flag.
func TestObjectLevelConditionalReadWriteIsDocumentationByDefault(t *testing.T) {
	src := string(generateFromSchema(t, "testdata/schemas/regression/object_level_conditional_read_write.json"))
	for _, unwanted := range []string{
		"AccessRules",
		"_accessStripWriteOnly",
		"_accessRefuseReadOnly",
		"_woKey",
		"_roKey",
		"read-only property may not be set",
	} {
		if strings.Contains(src, unwanted) {
			t.Errorf("the default configuration emitted %q; readOnly/writeOnly behaviour is --strict-read-write only", unwanted)
		}
	}
}
