package schemagen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #319: two $refs into the interior of one document, each named after the
// last segment of its own pointer, collapsing onto a single Go type.
//
// The claim walk used to exempt a whole resource from judgement when the run
// generates it as an input, on the reasoning that collectNameClaims already
// speaks for it. It speaks for the root type and for the $defs and definitions
// entries, which is all the generator declares of a document nobody refers into
// -- and nothing at all for a position some $ref reached. So
// "#/$defs/A/properties/x" and "#/$defs/B/properties/x" both asked for X, the
// re-entrancy guard in generateTypeDef turned the second away, and every
// instance setting the second property was judged against the first's schema.
// Exit 0, no warning.
//
// The answer is #271's rather than #260's, and the distinction is the whole
// reason #308's resource keying does not reach this. #260 asks "two documents
// each own a namespace -- which one did this come from", and answers with a
// prefix; there is one document and one resource here, so both positions would
// take the same prefix and it separates nothing. #271 asks "two names this
// generator derived happen to coincide", and answers by numbering. These tests
// pin the numbering, and -- because a numbered name is worth nothing if the type
// under it is still the other position's -- they compile the output and run
// documents through it.

// The reproducer from the issue, verbatim: a string at one interior position and
// an integer at the other.
const twoInteriorPositions = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"title": "I",
	"type": "object",
	"properties": {
		"a": {"$ref": "#/$defs/A/properties/x"},
		"b": {"$ref": "#/$defs/B/properties/x"}
	},
	"$defs": {
		"A": {"type": "object", "properties": {"x": {"type": "string", "minLength": 3}}},
		"B": {"type": "object", "properties": {"x": {"type": "integer", "minimum": 7}}}
	}
}`

func TestTwoRefsIntoOneDocumentsInteriorKeepTheirOwnSchema(t *testing.T) {
	dir, paths := writeSchemas(t, "i.json", twoInteriorPositions)

	stderr, err := runGenerateCapturing(t, paths[0], "-o", filepath.Join(dir, "gen"), "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if got := strings.Join(declaredTypeNames(t, filepath.Join(dir, "gen")), ","); got != "A,B,I,X,X2" {
		t.Errorf("declared types = %s, want A,B,I,X,X2", got)
	}
	// The diagnostic names both positions the way the document writes them --
	// the JSON Pointer that reached each -- because the $defs key alone ("x" in
	// both) is precisely what does not tell them apart.
	for _, want := range []string{
		"declares the Go type name X in 2 places",
		"$defs/A/properties/x keeps X",
		"$defs/B/properties/x becomes X2",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return []string{paths[0], "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
		},
		"example.com/m/gen",
		[]rootInstance{
			// Valid per the schema, and refused at decode before the fix: "b"
			// carried the string.
			{"I", `{"a":"abc","b":7}`, true, `{"a":"abc","b":7}`},
			// Each position's own constraint has to survive, not only its Go type.
			{"I", `{"a":"ab","b":7}`, false, ""},
			{"I", `{"a":"abc","b":6}`, false, ""},
			// And each position must refuse the other's shape.
			{"I", `{"a":7,"b":"abc"}`, false, ""},
			{"I", `{}`, true, `{}`},
		})
}

// The other half of the same rule, and the one a fix must not break: two
// interior positions that describe the same schema stay one type. Agreement is
// what decides, exactly as it does for two $defs keys that fold onto one name --
// so this must generate a single X, and say nothing, because a warning about a
// document that is doing nothing wrong is its own defect.
func TestTwoRefsIntoPositionsThatDescribeOneSchemaStillShareOneType(t *testing.T) {
	dir, paths := writeSchemas(t, "s.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "S",
		"type": "object",
		"properties": {
			"a": {"$ref": "#/$defs/A/properties/x"},
			"b": {"$ref": "#/$defs/B/properties/x"}
		},
		"$defs": {
			"A": {"type": "object", "properties": {"x": {"type": "string", "minLength": 3}}},
			"B": {"type": "object", "properties": {"x": {"minLength": 3, "type": "string"}}}
		}
	}`)

	stderr, err := runGenerateCapturing(t, paths[0], "-o", filepath.Join(dir, "gen"), "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing: the two positions hold one schema", stderr)
	}
	if got := strings.Join(declaredTypeNames(t, filepath.Join(dir, "gen")), ","); got != "A,B,S,X" {
		t.Errorf("declared types = %s, want A,B,S,X", got)
	}
}

// A $ref into a position of a document that also declares a definition deriving
// the same name. The two are separated for the same reason, and the definition
// is the claim that keeps the name it asked for: a $defs key spelled as the Go
// name it derives outranks a position in claimSplitRank, so the schema author's
// own X stays X whichever way the document is written.
func TestAPositionContestingADefinitionNameIsSeparatedFromIt(t *testing.T) {
	dir, paths := writeSchemas(t, "m.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "M",
		"type": "object",
		"properties": {
			"a": {"$ref": "#/$defs/X"},
			"b": {"$ref": "#/$defs/A/properties/x"}
		},
		"$defs": {
			"X": {"type": "string", "minLength": 3},
			"A": {"type": "object", "properties": {"x": {"type": "integer", "minimum": 7}}}
		}
	}`)

	stderr, err := runGenerateCapturing(t, paths[0], "-o", filepath.Join(dir, "gen"), "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "$defs/X keeps X") {
		t.Errorf("the definition must keep the name its own key derives:\n%s", stderr)
	}

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return []string{paths[0], "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
		},
		"example.com/m/gen",
		[]rootInstance{
			{"M", `{"a":"abc","b":7}`, true, `{"a":"abc","b":7}`},
			{"M", `{"a":"abc","b":6}`, false, ""},
			{"M", `{"a":7,"b":7}`, false, ""},
		})
}

// A reference that reaches a position through something other than a $defs
// entry. The claim is filed wherever the pointer lands, so a property of a
// property collides with a definition of the same name just as an interior
// $defs position does -- and the type under each name is that position's own.
func TestARefIntoAPropertyOfAPropertyIsClaimedToo(t *testing.T) {
	dir, paths := writeSchemas(t, "p.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "P",
		"type": "object",
		"properties": {
			"outer": {"type": "object", "properties": {"x": {"type": "integer", "minimum": 7}}},
			"a": {"$ref": "#/$defs/X"},
			"b": {"$ref": "#/properties/outer/properties/x"}
		},
		"$defs": {"X": {"type": "string", "minLength": 3}}
	}`)

	stderr, err := runGenerateCapturing(t, paths[0], "-o", filepath.Join(dir, "gen"), "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "properties/outer/properties/x becomes X2") || !strings.Contains(stderr, "$defs/X keeps X") {
		t.Errorf("the position reached through properties must be claimed:\n%s", stderr)
	}

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return []string{paths[0], "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
		},
		"example.com/m/gen",
		[]rootInstance{
			{"P", `{"a":"abc","b":7}`, true, `{"a":"abc","b":7}`},
			{"P", `{"a":"abc","b":6}`, false, ""},
			{"P", `{"a":"ab","b":7}`, false, ""},
		})
}

// The same collapse one level out: the two references are written inside a
// document nobody listed, which a single $ref pulled into this package. That
// document's own declarations were said to be "counted once when the walk
// crossed into it", and crossing into it counts the one node the reference
// landed on -- so a.json's $defs/Outer reached its own two interior positions
// and they merged, with the integer discarded and nothing on stderr.
func TestTwoRefsInsideAReferencedDocumentsInteriorKeepTheirOwnSchema(t *testing.T) {
	dir, paths := writeSchemas(t,
		"a.json", `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$defs": {
				"Outer": {"type": "object", "properties": {
					"p": {"$ref": "#/$defs/A/properties/x"},
					"q": {"$ref": "#/$defs/B/properties/x"}}},
				"A": {"type": "object", "properties": {"x": {"type": "string", "minLength": 3}}},
				"B": {"type": "object", "properties": {"x": {"type": "integer", "minimum": 7}}}
			}}`,
		"main.json", `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"title": "Main", "type": "object",
			"properties": {"o": {"$ref": "a.json#/$defs/Outer"}}}`)
	main := paths[1]

	stderr, err := runGenerateCapturing(t, main, "-o", filepath.Join(dir, "gen"), "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"$defs/A/properties/x keeps X",
		"$defs/B/properties/x becomes X2",
		"(reached by $ref)",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return []string{main, "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
		},
		"example.com/m/gen",
		[]rootInstance{
			// Refused at decode before the fix: "q" carried the string.
			{"Main", `{"o":{"p":"abc","q":7}}`, true, `{"o":{"p":"abc","q":7}}`},
			{"Main", `{"o":{"p":"ab","q":7}}`, false, ""},
			{"Main", `{"o":{"p":"abc","q":6}}`, false, ""},
			{"Main", `{"o":{"p":7,"q":"abc"}}`, false, ""},
		})
}

// ---------- stability ----------

// The names must be a function of the document and of nothing else. Go
// randomizes map iteration on every run, so a walk that read $defs or properties
// out of a map without sorting would deal the numbered suffix to a different
// position each time -- and a generated API that changes between two runs of one
// command is worse than the collapse it replaced, because it breaks the caller's
// code rather than their data.
//
// Six colliding positions rather than the issue's two, and the count is what
// makes this a test rather than a coin flip. Go's iteration over a map small
// enough to sit in one group randomizes the slot it starts at, so a two-element
// map comes out in its stored order far more often than not: with the sorting
// deliberately removed and the numbering made to follow the walk, the
// two-position document above produced identical source in 19 runs out of 20,
// and twelve repeats of it noticed nothing at all. Six positions give six
// starting points instead of a heavily loaded two, and the same experiment then
// disagrees inside the first two runs, every time.
func TestRefPositionNamesAreIdenticalAcrossRuns(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "I", "type": "object", "properties": {`)
	for i, key := range refCollisionKeys {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `%q: {"$ref": "#/$defs/%s/properties/x"}`, strings.ToLower(key), key)
	}
	b.WriteString(`}, "$defs": {`)
	for i, key := range refCollisionKeys {
		if i > 0 {
			b.WriteString(",")
		}
		// Each position a different schema, so no two of them may share a type
		// and all six names are contested.
		fmt.Fprintf(&b, `%q: {"type":"object","properties":{"x":{"type":"integer","minimum":%d}}}`, key, i+1)
	}
	b.WriteString(`}}`)

	dir, paths := writeSchemas(t, "i.json", b.String())

	var first, firstStderr string
	for i := 0; i < 8; i++ {
		out := filepath.Join(dir, fmt.Sprintf("gen%d", i))
		stderr, err := runGenerateCapturing(t, paths[0], "-o", out, "-p", "gen")
		if err != nil {
			t.Fatalf("run %d: generate: %v\nstderr:\n%s", i, err, stderr)
		}
		src := generatedSource(t, out)
		if i == 0 {
			first, firstStderr = src, stderr
			// The document has to actually contest the name, or every run
			// agreeing says nothing at all.
			if !strings.Contains(stderr, "declares the Go type name X in 6 places") {
				t.Fatalf("the fixture does not produce the collision it is for:\n%s", stderr)
			}
			continue
		}
		if src != first {
			t.Fatalf("run %d generated different source from run 0", i)
		}
		if stderr != firstStderr {
			t.Fatalf("run %d reported differently from run 0:\n%s\nvs\n%s", i, stderr, firstStderr)
		}
	}
}

// The $defs keys the fixture above collides on. Deliberately not in sorted
// order in this list: the answer must come from the claims, not from the order
// anything was written down in.
var refCollisionKeys = []string{"D", "A", "F", "C", "E", "B"}

// The order the two references are encountered in must not decide which of them
// keeps the unnumbered name. Encounter order here is the order collectRefSites
// walks the properties, which is sorted, so swapping which property holds which
// pointer swaps the order the two positions are reached in -- and the claims
// still sort by the position they name, so $defs/A's keeps X in both spellings
// and $defs/B's is numbered in both.
//
// The load-bearing half is the second assertion: whichever name each position
// ends up with, the property must reach its own target's schema.
func TestRefPositionNamesDoNotDependOnTheOrderTheRefsAreEncountered(t *testing.T) {
	doc := func(first, second string) string {
		return fmt.Sprintf(`{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"title": "I",
			"type": "object",
			"properties": {"a": {"$ref": %q}, "b": {"$ref": %q}},
			"$defs": {
				"A": {"type": "object", "properties": {"x": {"type": "string", "minLength": 3}}},
				"B": {"type": "object", "properties": {"x": {"type": "integer", "minimum": 7}}}
			}
		}`, first, second)
	}
	const toA = "#/$defs/A/properties/x"
	const toB = "#/$defs/B/properties/x"

	for _, tc := range []struct {
		name string
		body string
		// The instance each spelling must accept: "a" holds whatever its own
		// pointer reached.
		valid string
	}{
		{"a reaches the string first", doc(toA, toB), `{"a":"abc","b":7}`},
		{"a reaches the integer first", doc(toB, toA), `{"a":7,"b":"abc"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, paths := writeSchemas(t, "i.json", tc.body)
			stderr, err := runGenerateCapturing(t, paths[0], "-o", filepath.Join(dir, "gen"), "-p", "gen")
			if err != nil {
				t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
			}
			// The name each position gets is decided by the position, not by
			// when it was reached: $defs/A's sorts first under
			// claimSplitOrderLess in both spellings.
			for _, want := range []string{
				"$defs/A/properties/x keeps X",
				"$defs/B/properties/x becomes X2",
			} {
				if !strings.Contains(stderr, want) {
					t.Errorf("missing %q in:\n%s", want, stderr)
				}
			}
			generateCompileRunRoots(t,
				func(modRoot string) []string {
					return []string{paths[0], "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
				},
				"example.com/m/gen",
				[]rootInstance{
					{"I", tc.valid, true, tc.valid},
					// Each property refuses the other's shape, whichever way
					// round the document wrote them.
					{"I", `{"a":true,"b":true}`, false, ""},
				})
		})
	}
}

// generatedSource concatenates every generated .go file under dir in a fixed
// order, so two runs can be compared byte for byte.
func generatedSource(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		src, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		b.WriteString(e.Name())
		b.WriteByte('\n')
		b.Write(src)
	}
	return b.String()
}
