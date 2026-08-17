package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// The same position, written three ways, has to get the same verdicts.
//
// Three keywords put a reference on a schema object: $ref, $recursiveRef and
// $dynamicRef. Where the reference has one possible target -- which is the
// ordinary case, and the case every document below is -- the three say the same
// thing, so a keyword that reaches its sub-schema through any one of them must
// enforce exactly what it enforces through the others.
//
// That was not true. {"contains":{"$dynamicRef":"#it"}} generated
//
//	_containsCount = len(c.Arr)
//
// which counts every element as matching, so `contains` asserted only that the
// array was non-empty and the anchor's own minLength was applied to nothing --
// while the identical document written {"$ref":"#/$defs/item"} generated the
// real per-element check. Issue #337. The cause was a reading of the schema that
// could not see the keyword at all: isAlwaysTrueSchema decided a sub-schema
// stated nothing by walking a hand-written list of struct fields that ended
// `s.EffectiveRef() == ""`, and EffectiveRef is $ref-or-$recursiveRef and never
// $dynamicRef.
//
// pkg/generator/refsites_test.go is the structural half: every function that
// reads a reference off a schema, classified, with the set of keywords it names
// pinned against the source. This is the half that fails under a defect rather
// than under a refactor. It generates, compiles and runs each document and holds
// it to the verdicts the schema states, so a check that has gone missing is
// caught by the document it stops refusing rather than by an IR field being
// empty.
//
// #339 is why the corpus is arranged by keyword and not only by position. #313's
// gate enumerated every position a type name can be written into and was blind
// to reference kinds, because its probe set contained no dynamic reference at
// all -- so a whole keyword could be wrong at every position it enumerated and
// the gate stayed green. TestEveryPositionIsDrivenByEveryReferenceKind is what
// says, from the run itself, which keywords actually reached which positions.
//
// What this corpus reaches, measured rather than claimed:
//
//	go test ./tests/ -run TestReferenceKindsAgree \
//	  -coverpkg=./pkg/generator/...,./pkg/schema/... -coverprofile=cov.out
//
// executes 44 of the 64 functions refReadingSites classifies. The twenty it does
// not are these, and none of them is a position a schema author writes:
//
//   - ten live in cmd/schemagen, which this in-process run does not link.
//   - dynamicScopeRefusal and inPlaceSuccessors are reached only where the
//     dynamic scope *decides* the target, which is two declarations of one
//     anchor -- deliberately outside this corpus, since such a document is
//     refused or routed to the runtime evaluator instead (#332). tests/two_callers_test.go
//     is the gate for that.
//   - firstAllOfArrayAliasName needs an array-typed target, variantMatchesMapping
//     a discriminator mapping, hasNonTypeScopedConstraints a draft-3
//     schema-valued `type`, and neither family carries one.
//   - schemaCarriesRef, collectEvaluatedItems and countEvaluatedItemsOnPath are
//     reached on paths these documents do not take.
//   - crossPackageMiss.describe and ResolveError.Error are error text, and are
//     classified refNotASchemaReference.
//
// The figure belongs in a comment rather than in an assertion because it is a
// measurement of this corpus and would have to be re-taken, not defended, the
// next time a position is added. What is asserted is the part that can go
// silently wrong: that every position here is driven by every keyword that can
// spell it, which is the check below.

// The two document families.
//
// A reference's spelling is not free: $recursiveRef takes no target but "#", so
// the only schema it can name is the resource it is written in, and $dynamicRef
// exists only from 2020-12 while $recursiveRef exists only in 2019-09. One
// family of documents therefore cannot carry all three, and two are needed.
//
//   - refFamilyAnchor is 2020-12. Its target is a definition carrying a
//     $dynamicAnchor, reached either as "#/$defs/obj" by $ref or as "#o" by a
//     bookended $dynamicRef. Exactly one schema in each document declares that
//     anchor, so the dynamic scope has nothing to decide and the two spellings
//     are the same statement.
//
//   - refFamilyRoot is 2019-09. Its target is the document root, reached either
//     as "#" by $ref or as "#" by $recursiveRef. No resource declares
//     $recursiveAnchor, so the reference is not bookended and again the two
//     spellings are the same statement.
//
// Both targets are objects that accept {"x":"abcd"} and refuse {"x":"ab"}, so
// one pair of instances judges a position in either family.
type refFamily struct {
	name string
	// spellings maps a label to the reference object written at the position.
	spellings map[string]string
	// stringSpellings is the same for a position whose reference has to name a
	// string rather than an object. Empty where the family has no such target;
	// see refPositionsAnchorOnly.
	stringSpellings map[string]string
	// document renders the whole schema around a position's fragment.
	document func(pos refPosition, ref string) string
	// instance renders one of the position's instance templates.
	instance func(tmpl, value string) string
}

const (
	refTargetValid   = `{"x":"abcd"}`
	refTargetInvalid = `{"x":"ab"}`
)

var refFamilies = []refFamily{
	{
		name: "anchor_2020",
		spellings: map[string]string{
			"$ref":        `{"$ref": "#/$defs/obj"}`,
			"$dynamicRef": `{"$dynamicRef": "#o"}`,
		},
		stringSpellings: map[string]string{
			"$ref":        `{"$ref": "#/$defs/str"}`,
			"$dynamicRef": `{"$dynamicRef": "#s"}`,
		},
		document: func(pos refPosition, ref string) string {
			body := strings.ReplaceAll(pos.fragment, "REF", ref)
			if pos.objectLevel {
				body = `"type":"object",` + body
			} else {
				body = `"type":"object","properties":{` + body + `}`
			}
			return `{"$schema":"https://json-schema.org/draft/2020-12/schema",` +
				`"$id":"https://ex.test/anchor.json","title":"Root",` + body +
				`,"$defs":{"obj":{"$dynamicAnchor":"o","type":"object",` +
				`"properties":{"x":{"type":"string","minLength":4}}}}}`
		},
		instance: func(tmpl, value string) string {
			return strings.ReplaceAll(tmpl, "V", value)
		},
	},
	{
		name: "root_2019",
		spellings: map[string]string{
			"$ref":          `{"$ref": "#"}`,
			"$recursiveRef": `{"$recursiveRef": "#"}`,
		},
		document: func(pos refPosition, ref string) string {
			frag := strings.ReplaceAll(pos.fragment, "REF", ref)
			props := `"x":{"type":"string","minLength":4}`
			body := ""
			if pos.objectLevel {
				body = `"type":"object","properties":{` + props + `},` + frag
			} else {
				body = `"type":"object","properties":{` + props + `,` + frag + `}`
			}
			return `{"$schema":"https://json-schema.org/draft/2019-09/schema",` +
				`"$id":"https://ex.test/root.json","title":"Root",` + body + `}`
		},
		// The root is the target here, so every instance has to satisfy it too:
		// the document's own "x" is set to a value the target admits, and the
		// position's value is what the case is actually about.
		instance: func(tmpl, value string) string {
			out := strings.ReplaceAll(tmpl, "V", value)
			return `{"x":"abcd",` + strings.TrimPrefix(out, "{")
		},
	},
}

// refPosition is one place a schema can put a reference, with a document that
// the target admits and one it refuses.
//
// fragment carries REF where the reference object goes. A property position's
// fragment is a member of "properties"; an objectLevel one is a member of the
// schema object itself.
//
// valid and invalid carry V where the position's value goes: refTargetValid in
// the first and refTargetInvalid in the second, so one template says both cases
// and they cannot drift apart.
type refPosition struct {
	name        string
	fragment    string
	valid       string
	invalid     string
	objectLevel bool
	// stringTarget marks a position whose reference must name a string rather
	// than an object -- propertyNames judges a key. Such a position carries its
	// own target and runs in the anchor family alone.
	stringTarget bool
	// wholeInstances marks a position whose valid and invalid documents are
	// written out rather than built round the target's own pair, because what
	// the position is about is not the value at a property: an in-place
	// applicator judges the object itself.
	wholeInstances bool
}

var refPositions = []refPosition{
	{name: "property_value", fragment: `"p":REF`, valid: `{"p":V}`, invalid: `{"p":V}`},
	// The target is an object, so a null at the position is a value it forbids.
	// A position that reads the reference only to ask what JSON kinds the target
	// admits is a reader like any other: schemaForbidsKindAt is where the
	// question is put, and before #337 it could not see a $dynamicRef, so
	// {"p":null} was accepted where the same document written with a $ref
	// refused it.
	{name: "property_value_refuses_null", fragment: `"p":REF`, wholeInstances: true,
		valid: `{"p":{"x":"abcd"}}`, invalid: `{"p":null}`},
	{name: "array_items", fragment: `"p":{"type":"array","items":REF}`, valid: `{"p":[V]}`, invalid: `{"p":[V]}`},
	{name: "nested_array_items", fragment: `"p":{"type":"array","items":{"type":"array","items":REF}}`,
		valid: `{"p":[[V]]}`, invalid: `{"p":[[V]]}`},
	{name: "contains", fragment: `"p":{"type":"array","contains":REF}`, valid: `{"p":[V]}`, invalid: `{"p":[V]}`},
	{name: "contains_min_contains", fragment: `"p":{"type":"array","contains":REF,"minContains":2}`,
		valid: `{"p":[V,{"x":"efgh"}]}`, invalid: `{"p":[V,{"x":"efgh"}]}`},
	{name: "contains_of_a_map_value", fragment: `"p":{"type":"object","additionalProperties":{"type":"array","contains":REF}}`,
		valid: `{"p":{"w":[V]}}`, invalid: `{"p":{"w":[V]}}`},
	{name: "any_of_branch", fragment: `"p":{"anyOf":[REF,{"type":"integer","minimum":100}]}`,
		valid: `{"p":V}`, invalid: `{"p":V}`},
	{name: "one_of_branch", fragment: `"p":{"oneOf":[REF,{"type":"integer","minimum":100}]}`,
		valid: `{"p":V}`, invalid: `{"p":V}`},
	{name: "all_of_branch", fragment: `"p":{"allOf":[REF]}`, valid: `{"p":V}`, invalid: `{"p":V}`},
	{name: "all_of_branch_with_a_sibling", fragment: `"p":{"allOf":[REF],"maxProperties":3}`,
		valid: `{"p":V}`, invalid: `{"p":V}`},
	// The negation: the value the target admits is the one this position must
	// refuse, so the two templates are handed the opposite documents. See
	// refCase, which is where the swap is made.
	{name: "not_branch", fragment: `"p":{"not":REF}`, valid: `{"p":V}`, invalid: `{"p":V}`},
	{name: "then_branch", fragment: `"p":{"if":{"type":"object"},"then":REF}`, valid: `{"p":V}`, invalid: `{"p":V}`},
	{name: "else_branch", fragment: `"p":{"if":{"type":"integer"},"then":{"type":"integer"},"else":REF}`,
		valid: `{"p":V}`, invalid: `{"p":V}`},
	{name: "inferred_array_items", fragment: `"p":{"$ref":"#/$defs/bag"}`,
		valid: `{"p":[V]}`, invalid: `{"p":[V]}`},
	{name: "inferred_array_contains", fragment: `"p":{"$ref":"#/$defs/bag"}`,
		valid: `{"p":[V]}`, invalid: `{"p":[V]}`},

	{name: "additional_properties", fragment: `"additionalProperties":REF`, objectLevel: true,
		valid: `{"w":V}`, invalid: `{"w":V}`},
	{name: "pattern_properties", fragment: `"patternProperties":{"^k":REF},"additionalProperties":true`, objectLevel: true,
		valid: `{"k1":V}`, invalid: `{"k1":V}`},
	{name: "unevaluated_properties", fragment: `"unevaluatedProperties":REF`, objectLevel: true,
		valid: `{"w":V}`, invalid: `{"w":V}`},
	// The branch declares "x" as its own so that the root family's instances,
	// which have to carry one for the target, are not judged by the branch's
	// overflow keyword as well.
	{name: "branch_overflow", fragment: `"allOf":[{"properties":{"x":true},"additionalProperties":REF}]`,
		objectLevel: true, valid: `{"w":V}`, invalid: `{"w":V}`},
	// The same branch with the unevaluated keyword instead, which is a different
	// producer: it collects what the branch and everything it references have
	// already evaluated before judging what is left.
	{name: "branch_unevaluated", fragment: `"allOf":[{"properties":{"x":true},"unevaluatedProperties":REF}]`,
		objectLevel: true, valid: `{"w":V}`, invalid: `{"w":V}`},
	// An array whose element schema is the reference and whose unevaluatedItems
	// is false: the element positions a reference evaluates are counted by a
	// producer of their own.
	{name: "unevaluated_items_after_a_referenced_element", fragment: `"p":{"type":"array","items":REF,"unevaluatedItems":false}`,
		valid: `{"p":[V]}`, invalid: `{"p":[V]}`},
}

// refPositionsAnchorOnly names the positions the root family cannot carry, and
// why.
//
// An entry withdraws the coverage claim for one family only. The position is
// still driven by both spellings of the other, so it is judged; what is missing
// is a second dialect's confirmation, which is worth saying rather than
// assuming.
var refPositionsAnchorOnly = map[string]string{
	"unevaluated_items": "the prefix it leaves unevaluated is prefixItems, which 2019-09 does not define, so " +
		"the root family's document would apply the keyword to the whole array and judge a different schema",
	"dependent_schema": "the branch is an in-place applicator: it judges the object the trigger was found on. " +
		"In the root family that object is the target, so the case would ask whether the root satisfies itself " +
		"and could not fail",
	"unevaluated_properties_beside_allof": "the branch would be a reference to the very schema it is a branch " +
		"of, which is an allOf over the whole document and not the position under test",
	"prefix_items": "2020-12 spells the tuple prefixItems and 2019-09 spells it an array-valued items. Written " +
		"the second way it is a different keyword through a different producer, so the same case in the root " +
		"family would not be the same position",
	"tail_past_prefix": "as prefix_items: the tail is items-beside-prefixItems in 2020-12 and additionalItems " +
		"in 2019-09",
	"property_names": "propertyNames judges a property key, which is a string, and the root family's only " +
		"reachable target is the document root, which is an object. A $recursiveRef cannot name anything else -- " +
		"\"#\" is the only value the draft gives it",
}

// The three positions above, in the anchor family only.
var refAnchorOnlyPositions = []refPosition{
	{name: "prefix_items", fragment: `"p":{"type":"array","prefixItems":[REF]}`,
		valid: `{"p":[V]}`, invalid: `{"p":[V]}`},
	{name: "tail_past_prefix", fragment: `"p":{"type":"array","prefixItems":[{"type":"integer"}],"items":REF}`,
		valid: `{"p":[1,V]}`, invalid: `{"p":[1,V]}`},
	{name: "unevaluated_items", fragment: `"p":{"type":"array","prefixItems":[{"type":"integer"}],"unevaluatedItems":REF}`,
		valid: `{"p":[1,V]}`, invalid: `{"p":[1,V]}`},
	{name: "property_names", fragment: `"propertyNames":REF,"additionalProperties":{"type":"integer"}`,
		objectLevel: true, stringTarget: true, valid: `{"abcd":1}`, invalid: `{"ab":1}`},
	{name: "dependent_schema", fragment: `"dependentSchemas":{"t":REF},"additionalProperties":true`,
		objectLevel: true, wholeInstances: true, valid: `{"t":1,"x":"abcd"}`, invalid: `{"t":1,"x":"ab"}`},
	{name: "unevaluated_properties_beside_allof", fragment: `"allOf":[REF],"unevaluatedProperties":false`,
		objectLevel: true, wholeInstances: true, valid: `{"x":"abcd"}`, invalid: `{"x":"abcd","z":1}`},
}

// TestReferenceKindsAgree is the behavioural gate: every position, written with
// every reference keyword that can spell it, judged through generate-compile-run.
//
// The assertion is the verdict and not the shape of the generated source. A
// check that has gone missing is exactly the thing a source comparison cannot
// see -- the file still compiles, still declares the type, and still has a
// Validate that returns nil -- which is why issue #337 survived a corpus of
// goldens, a compile gate and a cross-package qualification gate.
func TestReferenceKindsAgree(t *testing.T) {
	for _, c := range refCases() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			runRefKindCase(t, c)
		})
	}
}

// refCase is one document: a position, a family and a spelling.
type refCase struct {
	name     string
	position string
	keyword  string
	document string
	valid    []string
	invalid  []string
}

func refCases() []refCase {
	var out []refCase
	for _, fam := range refFamilies {
		positions := refPositions
		if fam.name == "anchor_2020" {
			positions = append(append([]refPosition(nil), refPositions...), refAnchorOnlyPositions...)
		}
		for _, pos := range positions {
			spellings := fam.spellings
			if pos.stringTarget {
				spellings = fam.stringSpellings
			}
			for _, keyword := range sortedRefSpellings(spellings) {
				ref := spellings[keyword]
				valid, invalid := refTargetValid, refTargetInvalid
				if pos.name == "not_branch" {
					// A negation inverts which document the position admits.
					valid, invalid = refTargetInvalid, refTargetValid
				}
				validDoc := fam.instance(pos.valid, valid)
				invalidDoc := fam.instance(pos.invalid, invalid)
				if pos.stringTarget || pos.wholeInstances {
					validDoc, invalidDoc = fam.instance(pos.valid, ""), fam.instance(pos.invalid, "")
				}
				out = append(out, refCase{
					name:     fam.name + "/" + pos.name + "/" + refKeywordLabel(keyword),
					position: pos.name,
					keyword:  keyword,
					document: refDocumentFor(fam, pos, ref),
					valid:    []string{validDoc},
					invalid:  []string{invalidDoc},
				})
			}
		}
	}
	return out
}

// refDocumentFor renders the schema, adding the extra definitions two positions
// need.
func refDocumentFor(fam refFamily, pos refPosition, ref string) string {
	doc := fam.document(pos, ref)
	switch pos.name {
	case "inferred_array_items":
		doc = addRefDef(doc, `"bag":{"type":"array","items":`+ref+`,"minItems":1}`)
	case "inferred_array_contains":
		doc = addRefDef(doc, `"bag":{"type":"array","contains":`+ref+`,"minItems":1}`)
	case "property_names":
		doc = addRefDef(doc, `"str":{"$dynamicAnchor":"s","type":"string","minLength":4}`)
	}
	return doc
}

// addRefDef splices one more definition into the document's $defs, creating the
// keyword where the family did not write one.
func addRefDef(doc, def string) string {
	const defs = `"$defs":{`
	if i := strings.Index(doc, defs); i >= 0 {
		return doc[:i+len(defs)] + def + "," + doc[i+len(defs):]
	}
	return strings.TrimSuffix(doc, "}") + `,"$defs":{` + def + `}}`
}

// refKeywordLabel makes a subtest name out of a keyword.
func refKeywordLabel(keyword string) string {
	return strings.TrimPrefix(keyword, "$")
}

func sortedRefSpellings(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// runRefKindCase generates the document, compiles it, and runs the instances
// through the generated Validate.
//
// It does not go through runValidationCasesOn because that helper takes a path
// under the repository and these documents are built in memory: writing 100
// fixtures to testdata for a table that is already exhaustive would put the
// interesting part -- which spelling stands where -- in file names.
func runRefKindCase(t *testing.T, c refCase) {
	t.Helper()

	var s schema.Schema
	if err := json.Unmarshal([]byte(c.document), &s); err != nil {
		t.Fatalf("the document this case builds is not JSON: %v\n%s", err, c.document)
	}
	s.NormalizeForDraft(schema.DraftUnknown)
	s.ComputeBaseURIs(nil, &s)

	gen := generator.New(generator.Config{PackageName: "main", OmitEmpty: true})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generating %s: %v\n%s", c.name, err, c.document)
	}
	em, err := emitter.New()
	if err != nil {
		t.Fatalf("creating emitter: %v", err)
	}
	src, err := em.Emit(ir)
	if err != nil {
		t.Fatalf("emitting %s: %v", c.name, err)
	}
	if !strings.Contains(string(src), "type Root ") && !strings.Contains(string(src), "type Root struct") {
		t.Fatalf("%s generated no type Root, so the cases below would judge something else:\n%s", c.name, src)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "types.go"), src, 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}
	writeSharedHelpers(t, dir, string(src))
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(refMainProgram(c)), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}
	if err := writeTestGoMod(dir, "reference_kind_agreement"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil || programOutput(out) != "PASS" {
		t.Fatalf("%s does not enforce what the same position enforces through the other spellings.\n"+
			"A reference with one possible target says the same thing however it is written, so a keyword that "+
			"reaches its sub-schema through %s has to check what it checks through a $ref. Issue #337 is this "+
			"failing for `contains` under a $dynamicRef, where the generated count was len(array) and every "+
			"element matched.\n%s\nschema:\n%s",
			c.name, c.keyword, programOutput(out), c.document)
	}
}

func refMainProgram(c refCase) string {
	quote := func(ss []string) string {
		out := make([]string, len(ss))
		for i, s := range ss {
			out[i] = fmt.Sprintf("%q", s)
		}
		return strings.Join(out, ", ")
	}
	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	valid := []string{%s}
	invalid := []string{%s}
	bad := false
	for _, in := range valid {
		var o Root
		if err := json.Unmarshal([]byte(in), &o); err != nil {
			fmt.Fprintf(os.Stderr, "the schema admits %%s and the generated code refused it at decode: %%v\n", in, err)
			bad = true
			continue
		}
		if err := o.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "the schema admits %%s and Validate refused it: %%v\n", in, err)
			bad = true
		}
	}
	for _, in := range invalid {
		var o Root
		if err := json.Unmarshal([]byte(in), &o); err != nil {
			continue // a decode-time refusal is a refusal
		}
		if err := o.Validate(); err == nil {
			fmt.Fprintf(os.Stderr, "the schema forbids %%s and the generated code accepted it\n", in)
			bad = true
		}
	}
	if bad {
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`, quote(c.valid), quote(c.invalid))
}

// TestEveryPositionIsDrivenByEveryReferenceKind reads the corpus back and says
// which reference keywords actually reached which positions.
//
// It is the answer to the hole issue #339 found. #313's enumeration was total
// over the positions a type name can be written into, and every one of them was
// probed -- with $ref, and only ever with $ref, so a defect that was a property
// of the *keyword* rather than of the position was invisible at all 45 of them.
// A coverage claim over positions is worth only as much as the keywords behind
// it, so the keywords are counted here rather than assumed, and a position no
// third keyword reaches has to say why in refPositionsAnchorOnly.
func TestEveryPositionIsDrivenByEveryReferenceKind(t *testing.T) {
	byPosition := map[string]map[string]bool{}
	keywords := map[string]bool{}
	for _, c := range refCases() {
		if byPosition[c.position] == nil {
			byPosition[c.position] = map[string]bool{}
		}
		byPosition[c.position][c.keyword] = true
		keywords[c.keyword] = true
	}

	for _, want := range []string{"$ref", "$recursiveRef", "$dynamicRef"} {
		if !keywords[want] {
			t.Errorf("no case in this file writes %s, so the corpus cannot say anything about it. "+
				"That is the shape of the hole issue #339 found: a gate total over positions and empty of a "+
				"whole keyword.", want)
		}
	}

	for _, pos := range sortedRefPositionNames(byPosition) {
		reached := byPosition[pos]
		if !reached["$ref"] {
			t.Errorf("%s is driven by no $ref, so there is no reading of the position to compare the others "+
				"against", pos)
		}
		if !reached["$dynamicRef"] {
			t.Errorf("%s is driven by no $dynamicRef, which is the keyword issue #337 was about", pos)
		}
		if reached["$recursiveRef"] {
			continue
		}
		reason, declared := refPositionsAnchorOnly[pos]
		if !declared {
			t.Errorf("%s is driven by no $recursiveRef and is not recorded in refPositionsAnchorOnly. "+
				"Either add it to the root family or say why it cannot be there -- an undeclared gap is a "+
				"coverage claim nobody made and nobody can check.", pos)
			continue
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s is recorded as anchor-family-only with no reason given", pos)
		}
	}

	for pos := range refPositionsAnchorOnly {
		if _, ok := byPosition[pos]; !ok {
			t.Errorf("refPositionsAnchorOnly names %q, which this file no longer drives at all", pos)
		}
	}

	// A floor, so that a corpus that quietly emptied out cannot pass by
	// vacuously satisfying every rule above.
	if len(byPosition) < 20 {
		t.Errorf("only %d positions in the corpus; it was written with more, and a gate measuring almost "+
			"nothing passes for the wrong reason", len(byPosition))
	}
}

func sortedRefPositionNames(m map[string]map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
