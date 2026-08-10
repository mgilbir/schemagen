package tests

import (
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// TestStrictReadWriteReachesTheUntypedPositions is issue #219.
//
// --strict-read-write keys on property names, and the names it knew were the
// ones a Go field could carry. Five positions in the same document have no
// field: a prefixItems slot and a contains element are held in a []any, a
// patternProperties value in a map of raw JSON, and a schema whose whole shape
// is one of the unevaluated keywords becomes a type with no fields at all. At
// every one of them the flag did nothing, and a writeOnly secret was written
// straight back out.
//
// Three of them were worse than nothing. The type the generator built for the
// sub-schema was decoded into by a *Validate* check -- that is how a tuple slot
// and a patternProperties value are judged -- so the flag's readOnly refusal
// came back as a validation verdict:
//
//	patternProperties ^k: key "k1": ro: read-only property may not be set
//
// readOnly is an annotation under every draft that defines it and constrains no
// document, so that is a verdict about a question the schema did not ask. It is
// the distinction #170 was built around, and the middle group of #219 is it
// being lost. The fix is not to silence the verdict: the refusal moves to the
// decoder, where the flag says it lives, and the check goes on decoding.
//
// Running rather than reading the generated source is the point, as it is for
// #172's matrix: what these keywords are worth is what the decoder and the
// encoder do.
func TestStrictReadWriteReachesTheUntypedPositions(t *testing.T) {
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

func main() {
	// Every position refuses a document that sets the readOnly member, whether
	// or not anything ever decodes the value into the type built for it.
	for _, doc := range []string{
		` + "`" + `{"tuple":[{"ro":1}]}` + "`" + `,
		` + "`" + `{"patterned":{"k1":{"ro":1}}}` + "`" + `,
		` + "`" + `{"holds":[{"ro":1}]}` + "`" + `,
		` + "`" + `{"leftoverProps":{"k":{"ro":1}}}` + "`" + `,
		` + "`" + `{"leftoverItems":[{"ro":1}]}` + "`" + `,
		` + "`" + `{"mapped":{"other":{"ro":1}}}` + "`" + `,
		// The control: a uniform items sub-schema becomes the element type,
		// which carries the check in its own decoder and always did.
		` + "`" + `{"typedItems":[{"ro":1}]}` + "`" + `,
		// And one level further in, to show the paths are paths and not a
		// special case for the first step.
		` + "`" + `{"tuple":[{"ro":1,"ok":2}],"plain":"p"}` + "`" + `,
	} {
		var v ReadWriteUntypedPositions
		if err := json.Unmarshal([]byte(doc), &v); err == nil {
			fail("strict mode decoded a document setting a readOnly property: %s", doc)
		} else if !strings.Contains(err.Error(), "read-only property may not be set") {
			fail("decoding %s failed for the wrong reason: %v", doc, err)
		}
	}

	// Nothing else is refused. Each position accepts the same sub-schema's
	// other two members, which is what says the rules name locations rather
	// than a shape.
	for _, doc := range []string{
		` + "`" + `{}` + "`" + `,
		` + "`" + `{"tuple":[{"ok":1,"wo":2}]}` + "`" + `,
		` + "`" + `{"patterned":{"k1":{"ok":1,"wo":2},"other":{"ro":9}}}` + "`" + `,
		` + "`" + `{"holds":[{"ok":1,"wo":2}]}` + "`" + `,
		` + "`" + `{"leftoverProps":{"k":{"ok":1,"wo":2}}}` + "`" + `,
		` + "`" + `{"leftoverItems":[{"ok":1,"wo":2}]}` + "`" + `,
		` + "`" + `{"typedItems":[{"ok":1,"wo":2}]}` + "`" + `,
		` + "`" + `{"plain":"p"}` + "`" + `,
		// The members additionalProperties does not govern. "decl" is claimed by
		// name and "p1" by pattern, and each declares a plain "ro" that no
		// keyword marks -- so a walker that did not step past the two claims
		// would refuse a document the schema never marked.
		` + "`" + `{"mapped":{"decl":{"ro":1}}}` + "`" + `,
		` + "`" + `{"mapped":{"p1":{"ro":1}}}` + "`" + `,
		// A key the pattern does not match is not the position at all, so the
		// readOnly written for the matched values says nothing about it. This
		// is the control that catches a walker matching every member.
		` + "`" + `{"patterned":{"zz":{"ro":1}}}` + "`" + `,
		// The other boundary, and it is a decision rather than a gap: a
		// subschema that was not selected contributes no annotation (2020-12
		// section 7.7.1), so readOnly does not bind through either of these.
		// readWriteAtLocation draws the same line for a struct's own members,
		// and TestStrictReadWriteBindsWhereverThePropertyIs is the control
		// there. writeOnly is the half that does bind through a conditional --
		// the two fail in opposite directions and only one of them can leak --
		// and TestStrictWriteOnlyFollowsAConditionalAndReadOnlyDoesNot is where
		// that whole matrix is asserted, position by position.
		` + "`" + `{"viaThen":{"ro":1}}` + "`" + `,
		` + "`" + `{"viaAnyOf":{"ro":1}}` + "`" + `,
	} {
		var v ReadWriteUntypedPositions
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			fail("strict mode refused a document it has nothing to say about: %s: %v", doc, err)
		}
	}

	// writeOnly, the same positions the other way round: in and not out.
	var v ReadWriteUntypedPositions
	in := ` + "`" + `{"tuple":[{"ok":1,"wo":2}],"patterned":{"k1":{"ok":1,"wo":2}},` +
		`"holds":[{"ok":1,"wo":2}],"leftoverProps":{"k":{"ok":1,"wo":2}},` +
		`"leftoverItems":[{"ok":1,"wo":2}],"typedItems":[{"ok":1,"wo":2}],"plain":"p"}` + "`" + `
	if err := json.Unmarshal([]byte(in), &v); err != nil {
		fail("decoding the writeOnly document: %v", err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		fail("marshaling: %v", err)
	}
	if strings.Contains(string(out), "\"wo\"") {
		fail("strict mode wrote a writeOnly property: %s", out)
	}
	// And took nothing with it. A delete that reached past the locations it
	// names would satisfy the line above and be wrong about everything else.
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		fail("re-reading the output: %v", err)
	}
	for _, kept := range []string{"tuple", "patterned", "holds", "leftoverProps", "leftoverItems", "typedItems", "plain"} {
		if _, present := got[kept]; !present {
			fail("strict mode dropped %q, which carries no writeOnly: %s", kept, out)
		}
	}
	if !strings.Contains(string(out), "\"ok\":1") {
		fail("strict mode dropped the member that carries no annotation: %s", out)
	}

	// The half that is not the flag's business. readOnly must not reach a
	// validation verdict at any of these positions, which is what it did at
	// three of them: the value is held as raw JSON and a Validate check decodes
	// it into the type built for the sub-schema, whose decoder refuses.
	//
	// The readOnly members are planted after a decode rather than written in
	// the document, because the decoder now refuses the document -- which is the
	// point. The decode is what fills the presence map the contains check is
	// gated on, so a value built from nothing would exercise nothing.
	var w ReadWriteUntypedPositions
	if err := json.Unmarshal([]byte(` + "`" + `{"holds":[{"ok":3}],"tuple":[{"ok":3}],"patterned":{"k1":{"ok":3}}}` + "`" + `), &w); err != nil {
		fail("decoding the verdict fixture: %v", err)
	}
	w.Holds = []any{map[string]any{"ro": 1, "ok": 3}}
	w.Tuple = []any{map[string]any{"ro": 1, "ok": 3}}
	w.Patterned.PatternProperties["k1"] = json.RawMessage(` + "`" + `{"ro":1,"ok":3}` + "`" + `)
	if err := w.Validate(); err != nil {
		fail("Validate consulted readOnly, which constrains no document: %v", err)
	}

	// And the check the Validate path is there to make still gets made. The
	// sub-schema seals itself with additionalProperties:false, which the
	// generated decoder judges from the overflow map it fills *after* the
	// readOnly scan -- so a refusal returned from the middle of that decode
	// would leave the map empty and this document would be accepted. The
	// refusal is recorded and the decode runs on for exactly this reason.
	w.Tuple = []any{map[string]any{"ro": 1, "extra": 2}}
	err = w.Validate()
	if err == nil {
		fail("the planted tuple element was validated against a half-decoded value: " +
			"an undeclared key its sub-schema forbids was accepted")
	}
	if strings.Contains(err.Error(), "read-only") {
		fail("Validate consulted readOnly, which constrains no document: %v", err)
	}

	// The same argument one level down, where the refusal arrives from a nested
	// type rather than from the element's own key list. encoding/json records an
	// Unmarshaler's error and goes on filling the rest of the value, so holding
	// it is what leaves this element whole enough to be judged; returning at once
	// would leave the overflow map empty and accept the forbidden key again.
	w.Tuple = []any{map[string]any{"child": map[string]any{"cro": 1}, "extra": 2}}
	err = w.Validate()
	if err == nil {
		fail("a nested readOnly refusal cut the element's decode short: " +
			"an undeclared key its sub-schema forbids was accepted")
	}
	if strings.Contains(err.Error(), "read-only") {
		fail("Validate consulted readOnly, which constrains no document: %v", err)
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgramWithConfig(t,
		"testdata/schemas/regression/read_write_untyped_positions.json",
		"strict_read_write_untyped_test",
		mainGo,
		generator.Config{PackageName: "testpkg", OmitEmpty: true, StrictReadWrite: true},
	)
}

// TestReadWriteUntypedPositionsAreDocumentationByDefault is the other setting of
// the matrix above, and it is what says the flag is the only thing that makes
// any of it happen.
//
// The paths are a table in the generated source, so this reads the source: under
// the default configuration there must be no table, no walker call, and no
// refusal type, and the file must be what it would have been if the two keywords
// had never been parsed.
func TestReadWriteUntypedPositionsAreDocumentationByDefault(t *testing.T) {
	src := string(generateFromSchema(t, "testdata/schemas/regression/read_write_untyped_positions.json"))
	for _, unwanted := range []string{
		"AccessRules",
		"_accessRefuseReadOnly",
		"_accessStripWriteOnly",
		"_decodeIgnoringReadOnly",
		"_readOnlyRefusal",
		"read-only property may not be set",
	} {
		if strings.Contains(src, unwanted) {
			t.Errorf("the default configuration emitted %q; readOnly/writeOnly behaviour is --strict-read-write only", unwanted)
		}
	}
	// The keywords still reach the reader. That is the whole of what they do by
	// default, and it is what makes the absence above a configuration decision
	// rather than the schema having been dropped on the floor.
	if !strings.Contains(src, `Read-only: the schema says "readOnly"`) {
		t.Errorf("the default configuration dropped the readOnly doc comment entirely:\n%s", src)
	}
}
