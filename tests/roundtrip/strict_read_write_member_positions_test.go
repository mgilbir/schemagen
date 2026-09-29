package roundtrip

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// accessPosition is one place "readOnly" or "writeOnly" can be written, with
// the documents that tell --strict-read-write's two halves apart there.
//
// schema is the property's sub-schema with KW where the keyword goes; the
// matrix writes it once with `"readOnly":true` and once with
// `"writeOnly":true`. refuse and accept are documents for the readOnly
// spelling: under the flag the first must fail to decode with the refusal and
// the second must decode. woIn is a document for the writeOnly spelling and
// woOut what the flag writes back out for it. Every document is valid against
// the schema, so without the flag every one decodes and writes back unchanged,
// and under either setting Validate accepts every document that decodes.
type accessPosition struct {
	name   string
	schema string
	refuse []string
	accept []string
	woIn   string
	woOut  string
}

// accessPositions is the rule accessRulesFor states, position by position.
//
// A keyword binds a location exactly where the route to it is fixed by keys and
// indexes -- properties, patternProperties, additionalProperties, prefixItems,
// items -- and by allOf and $ref. There readOnly refuses and writeOnly strips.
// Everywhere else the route is conditional -- a branch, a contains, the part of
// an unevaluated keyword's reach a branch might evaluate -- and there readOnly
// binds nothing while writeOnly strips everything the route could reach. An
// array element is not a member and neither keyword acts on it.
//
// Each accept list holds a document that a rule reaching too far would refuse,
// which is what makes it evidence: for the exact positions it is a member the
// keyword does not reach, and for the conditional ones it is the document on
// which the branch is *not* selected, so a refusal there is a refusal of a
// document the schema permits and marked nothing in.
var accessPositions = []accessPosition{
	{
		name:   "property",
		schema: `{"type":"object","properties":{"m":{"type":"integer",KW},"c":{"type":"integer"}}}`,
		refuse: []string{`{"m":1}`, `{"c":1,"m":1}`},
		accept: []string{`{"c":1}`, `{}`},
		woIn:   `{"m":1,"c":2}`, woOut: `{"c":2}`,
	},
	{
		name:   "propertyViaRef",
		schema: `{"type":"object","properties":{"m":{"$ref":"#/$defs/KWREF"},"c":{"type":"integer"}}}`,
		refuse: []string{`{"m":1}`},
		accept: []string{`{"c":1}`},
		woIn:   `{"m":1,"c":2}`, woOut: `{"c":2}`,
	},
	{
		name:   "propertyViaAllOf",
		schema: `{"type":"object","allOf":[{"properties":{"m":{"type":"integer",KW}}}],"properties":{"c":{"type":"integer"}}}`,
		refuse: []string{`{"m":1}`},
		accept: []string{`{"c":1}`},
		woIn:   `{"m":1,"c":2}`, woOut: `{"c":2}`,
	},
	{
		// The finding's second half: a keyword written directly on a pattern
		// value was read by nothing, so a writeOnly secret was written back out.
		name:   "patternValue",
		schema: `{"type":"object","properties":{"c":{"type":"integer"}},"patternProperties":{"^m":{"type":"integer",KW}}}`,
		refuse: []string{`{"m1":1}`, `{"c":1,"mm":2}`},
		accept: []string{`{"c":1,"x":1}`},
		woIn:   `{"m1":1,"m2":2,"c":3,"x":4}`, woOut: `{"c":3,"x":4}`,
	},
	{
		name:   "patternValueViaRef",
		schema: `{"type":"object","patternProperties":{"^m":{"$ref":"#/$defs/KWREF"}}}`,
		refuse: []string{`{"m1":1}`},
		accept: []string{`{"x":1}`},
		woIn:   `{"m1":1,"x":2}`, woOut: `{"x":2}`,
	},
	{
		// A pattern matches declared properties too: "m1" is both.
		name:   "patternOverAProperty",
		schema: `{"type":"object","properties":{"m1":{"type":"integer"},"c":{"type":"integer"}},"patternProperties":{"^m":{"type":"integer",KW}}}`,
		refuse: []string{`{"m1":1}`},
		accept: []string{`{"c":1}`},
		woIn:   `{"m1":1,"c":2}`, woOut: `{"c":2}`,
	},
	{
		name:   "additionalValue",
		schema: `{"type":"object","properties":{"c":{"type":"integer"}},"patternProperties":{"^p":{"type":"integer"}},"additionalProperties":{"type":"integer",KW}}`,
		refuse: []string{`{"x":1}`, `{"c":1,"p1":2,"x":3}`},
		accept: []string{`{"c":1,"p1":2}`},
		woIn:   `{"c":1,"p1":2,"x":3}`, woOut: `{"c":1,"p1":2}`,
	},
	{
		// additionalProperties steps past its own object's names and no
		// other's: "a" is named only by an allOf branch, so it is one of the
		// outer object's leftovers (2020-12 §10.3.2.3) and is bound.
		name:   "additionalValueBesideAnAllOf",
		schema: `{"type":"object","allOf":[{"properties":{"a":{"type":"integer"}}}],"properties":{"c":{"type":"integer"}},"additionalProperties":{"type":"integer",KW}}`,
		refuse: []string{`{"a":1}`},
		accept: []string{`{"c":1}`},
		woIn:   `{"a":1,"c":2}`, woOut: `{"c":2}`,
	},
	{
		// Nothing but "c" is ever evaluated, so every other member is
		// unevaluated on every document: exact.
		name:   "unevaluatedValue",
		schema: `{"type":"object","properties":{"c":{"type":"integer"}},"unevaluatedProperties":{"type":"integer",KW}}`,
		refuse: []string{`{"x":1}`},
		accept: []string{`{"c":1}`},
		woIn:   `{"c":1,"x":2}`, woOut: `{"c":1}`,
	},
	{
		// "b" is evaluated when the first branch passes -- {"b":1} -- so it is
		// the unevaluated keyword's only on some documents: readOnly leaves it
		// alone and writeOnly strips it. "x" is named by nothing, so it is
		// exact.
		name:   "unevaluatedValueBesideABranch",
		schema: `{"type":"object","anyOf":[{"properties":{"b":{"type":"integer"}}},{"required":["z"]}],"unevaluatedProperties":{KW}}`,
		refuse: []string{`{"x":1}`},
		accept: []string{`{"b":1}`},
		woIn:   `{"b":1,"x":2}`, woOut: `{}`,
	},
	{
		name:   "memberOfAPatternValue",
		schema: `{"type":"object","patternProperties":{"^o":{"type":"object","properties":{"m":{"type":"integer",KW},"c":{"type":"integer"}}}}}`,
		refuse: []string{`{"o1":{"m":1}}`},
		accept: []string{`{"o1":{"c":1},"z":{"m":1}}`},
		woIn:   `{"o1":{"m":1,"c":2},"z":{"m":3}}`, woOut: `{"o1":{"c":2},"z":{"m":3}}`,
	},
	{
		name:   "memberOfAnAdditionalValue",
		schema: `{"type":"object","properties":{"c":{"type":"object"}},"additionalProperties":{"type":"object","properties":{"m":{"type":"integer",KW}}}}`,
		refuse: []string{`{"x":{"m":1}}`},
		accept: []string{`{"c":{"m":1}}`},
		woIn:   `{"c":{"m":1},"x":{"m":2,"k":3}}`, woOut: `{"c":{"m":1},"x":{"k":3}}`,
	},
	{
		name:   "memberOfATupleSlot",
		schema: `{"type":"array","prefixItems":[{"type":"object","properties":{"m":{"type":"integer",KW}}}]}`,
		refuse: []string{`[{"m":1}]`},
		accept: []string{`[{},{"m":1}]`},
		woIn:   `[{"m":1,"c":2},{"m":3}]`, woOut: `[{"c":2},{"m":3}]`,
	},
	{
		name:   "memberOfAnItem",
		schema: `{"type":"array","items":{"type":"object","properties":{"m":{"type":"integer",KW}}}}`,
		refuse: []string{`[{"c":1},{"m":1}]`},
		accept: []string{`[{"c":1}]`},
		woIn:   `[{"m":1,"c":2}]`, woOut: `[{"c":2}]`,
	},
	{
		name:   "memberOfAnItemAfterATuple",
		schema: `{"type":"array","prefixItems":[{"type":"object"}],"items":{"type":"object","properties":{"m":{"type":"integer",KW}}}}`,
		refuse: []string{`[{},{"m":1}]`},
		accept: []string{`[{"m":1}]`},
		woIn:   `[{"m":1},{"m":2,"c":3}]`, woOut: `[{"m":1},{"c":3}]`,
	},
	{
		name:   "memberOfAnUnevaluatedItem",
		schema: `{"type":"array","prefixItems":[{"type":"object"}],"unevaluatedItems":{"type":"object","properties":{"m":{"type":"integer",KW}}}}`,
		refuse: []string{`[{},{"m":1}]`},
		accept: []string{`[{"m":1}]`},
		woIn:   `[{"m":1},{"m":2,"c":3}]`, woOut: `[{"m":1},{"c":3}]`,
	},
	{
		// Element 1 is evaluated by the anyOf branch's tuple when it carries
		// "k", so it is unevaluated only on some documents; element 2 is past
		// every tuple and is exact.
		name:   "memberOfAnUnevaluatedItemBesideABranch",
		schema: `{"type":"array","prefixItems":[{"type":"object"}],"anyOf":[{"prefixItems":[{},{"type":"object","required":["k"]}]},{"maxItems":100}],"unevaluatedItems":{"type":"object","properties":{"m":{"type":"integer",KW}}}}`,
		refuse: []string{`[{},{},{"m":1}]`},
		accept: []string{`[{},{"k":1,"m":1}]`},
		woIn:   `[{"m":1},{"k":1,"m":2},{"m":3,"c":4}]`, woOut: `[{"m":1},{"k":1},{"c":4}]`,
	},
	{
		// The finding's first half, verbatim: element 1 does not match
		// contains, and was refused as though it did.
		name:   "memberOfAContainsElement",
		schema: `{"type":"array","contains":{"type":"object","required":["kind"],"properties":{"m":{"type":"integer",KW}}}}`,
		accept: []string{`[{"kind":1},{"m":2}]`},
		woIn:   `[{"kind":1,"m":2},{"m":3}]`, woOut: `[{"kind":1},{}]`,
	},
	{
		name:   "memberUnderAnyOf",
		schema: `{"anyOf":[{"type":"object","required":["t"],"properties":{"m":{"type":"integer",KW}}},{"type":"object"}]}`,
		accept: []string{`{"m":1}`},
		woIn:   `{"m":1,"c":2}`, woOut: `{"c":2}`,
	},
	{
		name:   "memberUnderOneOf",
		schema: `{"oneOf":[{"type":"object","required":["t"],"properties":{"m":{"type":"integer",KW}}},{"type":"object","required":["u"]}]}`,
		accept: []string{`{"u":1,"m":1}`},
		woIn:   `{"u":1,"m":1}`, woOut: `{"u":1}`,
	},
	{
		name:   "memberUnderThen",
		schema: `{"type":"object","if":{"required":["t"]},"then":{"properties":{"m":{"type":"integer",KW}}}}`,
		accept: []string{`{"m":1}`},
		woIn:   `{"m":1}`, woOut: `{}`,
	},
	{
		name:   "memberUnderElse",
		schema: `{"type":"object","if":{"required":["t"]},"else":{"properties":{"m":{"type":"integer",KW}}}}`,
		accept: []string{`{"t":1,"m":1}`},
		woIn:   `{"t":1,"m":1}`, woOut: `{"t":1}`,
	},
	{
		name:   "memberUnderDependentSchemas",
		schema: `{"type":"object","dependentSchemas":{"t":{"properties":{"m":{"type":"integer",KW}}}}}`,
		accept: []string{`{"m":1}`},
		woIn:   `{"m":1}`, woOut: `{}`,
	},
	{
		name:   "memberUnderNot",
		schema: `{"type":"object","not":{"required":["zz"],"properties":{"m":{"type":"integer",KW}}}}`,
		accept: []string{`{"m":1}`},
		woIn:   `{"m":1}`, woOut: `{}`,
	},
	{
		// An element is not a member: it cannot be left out of an array
		// without changing the array's length. Documentation only.
		name:   "anItem",
		schema: `{"type":"array","items":{"type":"integer",KW}}`,
		accept: []string{`[1,2]`},
		woIn:   `[1,2]`, woOut: `[1,2]`,
	},
	{
		name:   "aTupleSlot",
		schema: `{"type":"array","prefixItems":[{"type":"integer",KW}]}`,
		accept: []string{`[1]`},
		woIn:   `[1]`, woOut: `[1]`,
	},
}

// accessPositionsBefore2020 are the positions only the dialects before 2020-12
// spell: additionalItems after an items array, an empty one included, which
// leaves every element to additionalItems.
var accessPositionsBefore2020 = []accessPosition{
	{
		name:   "memberOfAnAdditionalItem",
		schema: `{"type":"array","items":[{"type":"object"}],"additionalItems":{"type":"object","properties":{"m":{"type":"integer",KW}}}}`,
		refuse: []string{`[{},{"m":1}]`},
		accept: []string{`[{"m":1}]`},
		woIn:   `[{"m":1},{"m":2,"c":3}]`, woOut: `[{"m":1},{"c":3}]`,
	},
	{
		name:   "memberOfAnAdditionalItemAfterAnEmptyTuple",
		schema: `{"type":"array","items":[],"additionalItems":{"type":"object","properties":{"m":{"type":"integer",KW}}}}`,
		refuse: []string{`[{"m":1}]`},
		accept: []string{`[{"c":1}]`},
		woIn:   `[{"m":1,"c":2}]`, woOut: `[{"c":2}]`,
	},
}

// accessPositionsSchema writes the matrix as one document of the dialect
// named: two properties per position, "<name>RO" and "<name>WO".
func accessPositionsSchema(dialect string, positions []accessPosition) string {
	var props []string
	for _, p := range positions {
		for _, kw := range []struct{ suffix, keyword, ref string }{
			{"RO", `"readOnly":true`, `"#/$defs/RO"`},
			{"WO", `"writeOnly":true`, `"#/$defs/WO"`},
		} {
			sub := strings.ReplaceAll(p.schema, `"#/$defs/KWREF"`, kw.ref)
			sub = strings.ReplaceAll(sub, "KW", kw.keyword)
			props = append(props, strconv.Quote(p.name+kw.suffix)+":"+sub)
		}
	}
	return `{"$schema":` + strconv.Quote(dialect) + `,"type":"object","properties":{` +
		strings.Join(props, ",") +
		`},"$defs":{"RO":{"type":"integer","readOnly":true},"WO":{"type":"integer","writeOnly":true}}}`
}

// accessPositionsMain is the program the matrix runs: every document of every
// position, decoded, validated and written back, against what the setting says
// each must do.
func accessPositionsMain(strict bool, positions []accessPosition) string {
	type docCase struct {
		Prop   string `json:"prop"`
		Doc    string `json:"doc"`
		Refuse bool   `json:"refuse"`
		Out    string `json:"out"`
	}
	var cases []docCase
	for _, p := range positions {
		for _, d := range p.refuse {
			cases = append(cases, docCase{Prop: p.name + "RO", Doc: d, Refuse: strict, Out: d})
		}
		for _, d := range p.accept {
			cases = append(cases, docCase{Prop: p.name + "RO", Doc: d, Out: d})
		}
		out := p.woIn
		if strict {
			out = p.woOut
		}
		cases = append(cases, docCase{Prop: p.name + "WO", Doc: p.woIn, Out: out})
	}
	table, err := json.Marshal(cases)
	if err != nil {
		panic(err)
	}
	return `package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"enginetest/gen"
)

type docCase struct {
	Prop   string ` + "`json:\"prop\"`" + `
	Doc    string ` + "`json:\"doc\"`" + `
	Refuse bool   ` + "`json:\"refuse\"`" + `
	Out    string ` + "`json:\"out\"`" + `
}

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func main() {
	var cases []docCase
	if err := json.Unmarshal([]byte(` + strconv.Quote(string(table)) + `), &cases); err != nil {
		panic(err)
	}
	failed := false
	for _, c := range cases {
		doc := "{" + fmt.Sprintf("%q", c.Prop) + ":" + c.Doc + "}"
		var v gen.Root
		err := json.Unmarshal([]byte(doc), &v)
		if c.Refuse {
			if err == nil || !strings.Contains(err.Error(), "read-only property may not be set") {
				fmt.Printf("%s: want the readOnly refusal, got %v\n", doc, err)
				failed = true
			}
			continue
		}
		if err != nil {
			fmt.Printf("%s: refused a document nothing marks here: %v\n", doc, err)
			failed = true
			continue
		}
		if err := v.Validate(); err != nil {
			fmt.Printf("%s: Validate rejected a valid document: %v\n", doc, err)
			failed = true
		}
		out, err := json.Marshal(&v)
		if err != nil {
			fmt.Printf("%s: marshal: %v\n", doc, err)
			failed = true
			continue
		}
		want := "{" + fmt.Sprintf("%q", c.Prop) + ":" + c.Out + "}"
		if !sameJSON(out, []byte(want)) {
			fmt.Printf("%s: wrote %s, want %s\n", doc, out, want)
			failed = true
		}
	}
	if !failed {
		fmt.Println("PASS")
	}
}
`
}

// TestStrictReadWriteBindsEveryMemberExactlyWhereItsRouteIsFixed is the whole
// position matrix, under both settings of the flag.
//
// Two findings are what it pins. readOnly inside `contains` refused
// {"list":[{"kind":1},{"secret":2}]} under a contains requiring "kind" --
// element 1 does not match it, so it is not an element contains describes, and
// the refusal rejected a document the schema permits. And a keyword written
// directly on a patternProperties or additionalProperties value was read by
// nothing, on the ground that "a map value has no property name to key on":
// true of an array element, and false of an object member, which has a key and
// can be refused or left out by it. The rest of the matrix is the rule those two
// were exceptions to, at every position it reaches, so that a third exception
// names itself.
func TestStrictReadWriteBindsEveryMemberExactlyWhereItsRouteIsFixed(t *testing.T) {
	for _, dialect := range []struct {
		uri       string
		positions []accessPosition
	}{
		{"https://json-schema.org/draft/2020-12/schema", accessPositions},
		{"https://json-schema.org/draft/2019-09/schema", accessPositionsBefore2020},
	} {
		schemaJSON := accessPositionsSchema(dialect.uri, dialect.positions)
		for _, strict := range []bool{true, false} {
			t.Run(dialect.uri+"/strict="+strconv.FormatBool(strict), func(t *testing.T) {
				cfg := generator.DefaultConfig()
				cfg.StrictReadWrite = strict
				if out := runEngineProgram(t, schemaJSON, cfg, accessPositionsMain(strict, dialect.positions)); out != "PASS" {
					t.Fatalf("the matrix disagreed with the rule:\n%s", out)
				}
			})
		}
	}
}
