package tests

import (
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// TestStrictWriteOnlyFollowsAConditionalAndReadOnlyDoesNot is the asymmetry, at
// every position --strict-read-write reaches a keyword only through a
// conditional applicator: anyOf, if/then/else, dependentSchemas, and a doubled
// not.
//
// Both keywords used to do nothing at all of them. readOnly still does, and that
// is correct: 2020-12 §7.7.1 says a subschema that was not selected, or that
// failed, contributes no annotation, so a refusal keyed on a branch this document
// did not match would reject a document the schema accepts -- and `not` is the
// sharp case, since a `not` that succeeds is a subschema that failed. The
// roViaAnyOf control in TestStrictReadWriteBindsWhereverThePropertyIs says the
// same thing about the same reach and is untouched; roOnAnyOf below is that
// control again, in this document, so that a readOnly which acquired the descent
// fails here too rather than only there.
//
// writeOnly now binds, and the reason it may is that the two keywords fail in
// opposite directions. Over-stripping loses a field: the value is still in hand,
// the omission is visible in the payload, and the caller can turn the flag off.
// Under-stripping writes out a property whose whole meaning is "never present
// when the instance is retrieved" (§9.4) -- the shape a password, a token or a
// private key has -- with no diagnostic anywhere. --strict-read-write is a policy
// the caller chose and not spec validation, so it is allowed to be stricter than
// the annotation rules in the direction that fails safe. See conditionalReachAt.
//
// Two mechanisms answer this question and both are exercised, because a fix to
// one would leave the other silently untouched. The `via*` properties put the
// marked member *inside* the branch, where the generated code holds the value as
// raw JSON and the rules are a path table. The `on*` properties write the keyword
// on the branch that describes the property itself, where the rule is a key in
// the root struct's own list. Every arm of both walks -- anyOf, oneOf, if, then,
// else, not, dependentSchemas -- has a property here, so removing one fails
// something.
//
// The Validate half is the constraint on all of it, and it is asserted at every
// position rather than argued: #170 established that neither keyword may reach a
// verdict, #234 got that to 0 positions violating it, and this must keep it
// there. "lim" is a real constraint rather than an annotation, so its verdict is
// the schema's business -- valid where the document conforms and invalid where it
// does not, identically at every position and identically to what the same
// document got before any of this.
//
// Running rather than reading the generated source is the point, as it is for
// #172's matrix and #219's: what these keywords are worth is what the decoder and
// the encoder do.
func TestStrictWriteOnlyFollowsAConditionalAndReadOnlyDoesNot(t *testing.T) {
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

// branched names the positions the sub-schema is reached through a conditional
// applicator, including one nested inside another. unconditional names the two
// where it is reached on every valid document.
//
// viaOneOf is in neither, and that is not an oversight: a oneOf of two object
// shapes compiles to a variant type per branch, each carrying the check in its
// own decoder, so readOnly binds there and always has (issue #175). It is
// asserted on its own below.
var branched = []string{"viaAnyOf", "viaThen", "viaElse", "viaDependent", "viaNotNot", "viaThenAnyOf", "viaThenOneOf", "viaIf"}
var unconditional = []string{"viaAllOf", "plain"}

// onBranch names the properties whose own schema puts the keyword on a branch.
// The whole property is what is marked, so the whole property is what goes.
var onBranch = []struct{ name, value string }{
	{"onAnyOf", ` + "`" + `"s"` + "`" + `},
	{"onOneOf", ` + "`" + `"s"` + "`" + `},
	{"onIf", ` + "`" + `"s"` + "`" + `},
	{"onThen", ` + "`" + `"s"` + "`" + `},
	{"onElse", ` + "`" + `"s"` + "`" + `},
	{"onNotNot", ` + "`" + `"s"` + "`" + `},
	{"onDependent", ` + "`" + `{"k":1}` + "`" + `},
}

func decode(doc string) (StrictConditionalReadWrite, error) {
	var v StrictConditionalReadWrite
	err := json.Unmarshal([]byte(doc), &v)
	return v, err
}

func every() []string {
	return append(append(append([]string{}, branched...), unconditional...), "viaOneOf")
}

func main() {
	// writeOnly is stripped at every position, through a branch and through an
	// in-place applicator alike. The other two members of the same sub-schema are
	// the control: a strip that deleted the property, or reached past the member
	// it names, would satisfy "wo is gone" and be wrong about everything else.
	for _, p := range every() {
		in := fmt.Sprintf("{%q:{\"wo\":2,\"ok\":1,\"lim\":3}}", p)
		v, err := decode(in)
		if err != nil {
			fail("decoding the writeOnly document at %s: %v", p, err)
		}
		out, err := json.Marshal(v)
		if err != nil {
			fail("marshaling at %s: %v", p, err)
		}
		var got map[string]map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			fail("re-reading the output at %s: %v", p, err)
		}
		held, present := got[p]
		if !present {
			fail("strict mode dropped the whole of %q, which nothing marks writeOnly: %s", p, out)
		}
		if _, leaked := held["wo"]; leaked {
			fail("strict mode wrote the writeOnly member at %s: %s", p, out)
		}
		for _, kept := range []string{"ok", "lim"} {
			if _, ok := held[kept]; !ok {
				fail("strict mode dropped %q at %s, which carries no annotation: %s", kept, p, out)
			}
		}
	}

	// The same question asked of the other mechanism: here the keyword is on the
	// branch that describes the property itself, so the rule is a key in this
	// struct's own list rather than a path into a value it holds.
	for _, c := range onBranch {
		in := fmt.Sprintf("{%q:%s,\"untouched\":\"u\"}", c.name, c.value)
		v, err := decode(in)
		if err != nil {
			fail("decoding the writeOnly document at %s: %v", c.name, err)
		}
		out, err := json.Marshal(v)
		if err != nil {
			fail("marshaling at %s: %v", c.name, err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			fail("re-reading the output at %s: %v", c.name, err)
		}
		if _, leaked := got[c.name]; leaked {
			fail("strict mode wrote %q, which a branch of its own schema marks writeOnly: %s", c.name, out)
		}
		if _, ok := got["untouched"]; !ok {
			fail("strict mode dropped \"untouched\" alongside %s: %s", c.name, out)
		}
	}

	// A dependentSchemas branch is stripped whether or not its trigger key is
	// present, and that is the deliberate direction rather than an oversight: the
	// rules are a static table of locations and cannot evaluate "when ok is also
	// there". Over-stripping loses a field the caller can see is missing;
	// under-stripping emits the secret. Written down so that changing it is a
	// choice.
	v, err := decode(` + "`" + `{"viaDependent":{"wo":2}}` + "`" + `)
	if err != nil {
		fail("decoding the untriggered dependentSchemas document: %v", err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		fail("marshaling the untriggered dependentSchemas document: %v", err)
	}
	if strings.Contains(string(out), "\"wo\"") {
		fail("strict mode wrote a writeOnly member a dependentSchemas branch marks, "+
			"on the ground that the branch's trigger was absent: %s", out)
	}

	// readOnly does not bind through a branch. Each position is asserted on its
	// own so that one which acquires the refusal names itself.
	for _, p := range branched {
		doc := fmt.Sprintf("{%q:{\"ro\":1,\"ok\":1}}", p)
		if _, err := decode(doc); err != nil {
			fail("strict mode refused a document the schema accepts at %s: "+
				"readOnly inside a conditional binds nothing (2020-12 section 7.7.1): %v", p, err)
		}
	}

	// The same for the property-level spelling, which is the control the whole
	// change turns on: onAnyOf is stripped and roOnAnyOf, the same shape with the
	// other keyword, is neither refused on the way in nor dropped on the way out.
	w, err := decode(` + "`" + `{"roOnAnyOf":"s","untouched":"u"}` + "`" + `)
	if err != nil {
		fail("strict mode refused a document whose anyOf branch says readOnly, "+
			"which binds nothing: %v", err)
	}
	roOut, err := json.Marshal(w)
	if err != nil {
		fail("marshaling the readOnly document: %v", err)
	}
	if !strings.Contains(string(roOut), "roOnAnyOf") {
		fail("strict mode stripped a property whose anyOf branch says readOnly, not writeOnly: %s", roOut)
	}

	// And readOnly does bind where the sub-schema is reached on every valid
	// document, which is what tells "readOnly declines a branch" apart from
	// "readOnly is off".
	for _, p := range unconditional {
		doc := fmt.Sprintf("{%q:{\"ro\":1,\"ok\":1}}", p)
		_, err := decode(doc)
		if err == nil {
			fail("strict mode decoded a document setting a readOnly property at %s", p)
		}
		if !strings.Contains(err.Error(), "read-only property may not be set") {
			fail("decoding at %s failed for the wrong reason: %v", p, err)
		}
	}

	// The sharp case, and the one a location reached twice turns on: bothWays is
	// a tuple slot described by an allOf and again by an anyOf, so the path table
	// reaches "bothWays[0].ro" once with readOnly and once through a branch that
	// says nothing about it. The union is what has to survive -- a branch cannot
	// take away what an in-place applicator said -- and the writeOnly member of
	// the same slot still goes.
	if _, err := decode(` + "`" + `{"bothWays":[{"ro":1,"ok":1}]}` + "`" + `); err == nil {
		fail("strict mode decoded a document setting a readOnly member of bothWays[0]: " +
			"an anyOf describing the same location does not withdraw what the allOf marked")
	} else if !strings.Contains(err.Error(), "read-only property may not be set") {
		fail("decoding at bothWays failed for the wrong reason: %v", err)
	}
	both, err := decode(` + "`" + `{"bothWays":[{"wo":2,"ok":1}]}` + "`" + `)
	if err != nil {
		fail("decoding the bothWays writeOnly document: %v", err)
	}
	// And the other half of the union: the anyOf marks that same "ro" member
	// writeOnly, so it is stripped as well. Planted after the decode because the
	// decoder refuses a document that carries it -- which is the readOnly rule
	// the line above just asserted.
	both.BothWays = []any{map[string]any{"ro": 1, "wo": 2, "ok": 1}}
	bothOut, err := json.Marshal(both)
	if err != nil {
		fail("marshaling the bothWays writeOnly document: %v", err)
	}
	for _, gone := range []string{"\"wo\"", "\"ro\""} {
		if strings.Contains(string(bothOut), gone) {
			fail("strict mode wrote %s in bothWays[0], which a branch marks writeOnly: %s", gone, bothOut)
		}
	}
	if !strings.Contains(string(bothOut), "\"ok\"") {
		fail("strict mode dropped the unmarked member of bothWays[0]: %s", bothOut)
	}

	// viaOneOf is refused too, and for a different reason: each branch compiles
	// to its own variant type carrying the check in its own decoder (issue #175).
	// The refusal reaches the caller as the union's "no matching oneOf variant"
	// rather than as its own message, because the variant that refused is one of
	// several the union tried. That is pre-existing and unchanged here; what this
	// asserts is that the document is still refused.
	if _, err := decode(` + "`" + `{"viaOneOf":{"ro":1,"ok":1}}` + "`" + `); err == nil {
		fail("strict mode decoded a document setting a readOnly property at viaOneOf, " +
			"whose oneOf branch compiles to a variant type of its own")
	}

	// No verdict moves anywhere. Both keywords are annotations and Validate has
	// never consulted either; "lim" is the constraint beside them, and its answer
	// has to be the schema's at every position under either setting of the flag.
	for _, p := range every() {
		conforming := fmt.Sprintf("{%q:{\"ok\":1,\"lim\":3}}", p)
		c, err := decode(conforming)
		if err != nil {
			fail("decoding the conforming document at %s: %v", p, err)
		}
		if err := c.Validate(); err != nil {
			fail("Validate rejected %s, which the schema permits: %v", conforming, err)
		}

		nonconforming := fmt.Sprintf("{%q:{\"ok\":1,\"lim\":99}}", p)
		b, err := decode(nonconforming)
		if err != nil {
			fail("decoding the non-conforming document at %s: %v", p, err)
		}
		verdict := b.Validate()
		// viaIf is the exception and it is the schema's own answer, not a gap:
		// the sub-schema is the *condition*, so a value that fails it leaves the
		// consequence inapplicable and nothing is violated. Pinned rather than
		// skipped, because "the verdict did not move" is the claim being made.
		if p == "viaIf" {
			if verdict != nil {
				fail("Validate rejected %s: a value failing an \"if\" violates nothing, "+
					"since the consequence simply does not apply: %v", nonconforming, verdict)
			}
			continue
		}
		if verdict == nil {
			fail("Validate accepted %s, which exceeds the maximum the schema states", nonconforming)
		}
		if strings.Contains(verdict.Error(), "read-only") || strings.Contains(verdict.Error(), "write-only") {
			fail("Validate at %s answered with an annotation, which constrains no document: %v", p, verdict)
		}
	}

	// The same for a document carrying the two annotated members, at every
	// position where nothing refuses them: it decodes, and it validates.
	for _, p := range branched {
		doc := fmt.Sprintf("{%q:{\"ro\":1,\"wo\":2,\"ok\":1,\"lim\":5}}", p)
		x, err := decode(doc)
		if err != nil {
			fail("decoding %s: %v", doc, err)
		}
		if err := x.Validate(); err != nil {
			fail("Validate rejected %s, which the schema permits: %v", doc, err)
		}
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgramWithConfig(t,
		"testdata/schemas/regression/strict_conditional_read_write.json",
		"strict_conditional_read_write_test",
		mainGo,
		generator.Config{PackageName: "testpkg", OmitEmpty: true, StrictReadWrite: true},
	)
}

// TestConditionalReadWriteIsDocumentationByDefault is the other setting of the
// matrix above, and it is what says the asymmetry is the flag's and not the
// generator's.
//
// Under the default configuration none of it may reach the decoder or the
// encoder: no table, no walker call, no refusal type, and no writeOnly key list.
// The document round-trips instead, which the round-trip fixtures assert; this
// asserts that the generated source has nowhere for a strip to come from.
func TestConditionalReadWriteIsDocumentationByDefault(t *testing.T) {
	src := string(generateFromSchema(t, "testdata/schemas/regression/strict_conditional_read_write.json"))
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
	// The keywords still reach the reader on the type that carries them, which is
	// the whole of what they do by default.
	if !strings.Contains(src, `Write-only: the schema says "writeOnly"`) {
		t.Errorf("the default configuration dropped the writeOnly doc comment entirely:\n%s", src)
	}
}
