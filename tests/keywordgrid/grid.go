// Package keywordgrid is the keyword grid: every assertion keyword JSON Schema
// 2020-12 defines, written at every position a subschema can stand in, under
// every composition a real schema puts beside it, in every dialect from draft 4
// on that has the keyword, each with a document that satisfies it, one that
// violates it and one of another kind.
//
// It exists because the question "does schemagen enforce keyword K" has no
// single answer: the audit behind the keyword ledger found keywords enforced at
// a property and dropped one position over at `items`, enforced alone and
// dropped beside a `type` that made the Go type nullable, enforced in one allOf
// branch and dropped when a second branch said the same thing again. A test
// written for one of those shapes proves nothing about the next one. The grid
// writes them all mechanically, so a gap is a failing cell rather than a
// finding someone has to think of.
//
// The grid is data. What a cell's documents should get is decided three ways:
// by construction (Cell.Constructed, where the position and composition
// preserve the row's own verdict), by Bowtie -- independent implementations,
// frozen into testdata/grid/verdicts.json by `make grid-oracle` -- and by the
// runtime evaluator schemagen ships, as a second opinion inside the project.
// See grid_test.go beside this file.
package keywordgrid

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

// Dialect is one value of the dialect axis: the $schema a cell states. The
// rows are written in 2020-12; a cell of an earlier dialect is the same schema
// in that dialect's spelling (see translate), and a cell that uses a keyword
// the dialect does not have is not built. Keywords that mean something
// different there -- a $ref's siblings up to draft 7, a boolean exclusiveMinimum
// in draft 4 -- are the point of the axis: the same row judged under the
// dialect's own rules, by an oracle that knows them.
type Dialect struct {
	Name, URI string
	level     int // 4, 6, 7, 2019 or 2020
}

// Dialects is the dialect axis.
var Dialects = []Dialect{
	{Name: "2020-12", URI: "https://json-schema.org/draft/2020-12/schema", level: 2020},
	{Name: "2019-09", URI: "https://json-schema.org/draft/2019-09/schema", level: 2019},
	{Name: "draft7", URI: "http://json-schema.org/draft-07/schema#", level: 7},
	{Name: "draft6", URI: "http://json-schema.org/draft-06/schema#", level: 6},
	{Name: "draft4", URI: "http://json-schema.org/draft-04/schema#", level: 4},
}

// formatAsserts reports whether format is an assertion under d, as this
// generator reads the dialects (README, "Format: assertion or annotation"):
// drafts 4 to 7 assert it, 2019-09 and 2020-12 annotate. Where the
// specification leaves it to the implementation the oracle's implementations
// disagree, and the construction -- which is the generator's documented
// reading -- is what decides.
func (d Dialect) formatAsserts() bool { return d.level <= 7 }

// Row is one keyword: the subschema asserting it, and three documents.
type Row struct {
	Name string
	// Schema is the subschema, as a JSON object.
	Schema string
	// Kind is the JSON kind the keyword says something about, "" for one that
	// applies to every kind.
	Kind string
	// Valid satisfies Schema, Invalid violates it, Other is a document of
	// another kind -- which a kind-scoped keyword admits vacuously and a
	// `type` refuses.
	Valid, Invalid, Other string
	// Second is a second subschema stating the same keyword, for the
	// composition that writes the keyword twice; "" where the row has none.
	Second string
	// Format marks a row whose keyword is format: an annotation where the
	// dialect makes it one, so its "invalid" document is one only an assertion
	// would refuse and the construction decides nothing there, and an
	// assertion elsewhere (Dialect.formatAsserts).
	Format bool
}

// Rows is the keyword axis.
var Rows = []Row{
	{Name: "type-string", Schema: `{"type":"string"}`, Kind: "string", Valid: `"a"`, Invalid: `1`, Other: `null`},
	{Name: "type-integer", Schema: `{"type":"integer"}`, Kind: "integer", Valid: `1`, Invalid: `1.5`, Other: `"a"`},
	{Name: "type-number", Schema: `{"type":"number"}`, Kind: "number", Valid: `1.5`, Invalid: `"a"`, Other: `null`},
	{Name: "type-boolean", Schema: `{"type":"boolean"}`, Kind: "boolean", Valid: `true`, Invalid: `"true"`, Other: `0`},
	{Name: "type-null", Schema: `{"type":"null"}`, Kind: "null", Valid: `null`, Invalid: `0`, Other: `""`},
	{Name: "type-array", Schema: `{"type":"array"}`, Kind: "array", Valid: `[]`, Invalid: `{}`, Other: `"[]"`},
	{Name: "type-object", Schema: `{"type":"object"}`, Kind: "object", Valid: `{}`, Invalid: `[]`, Other: `"{}"`},
	{Name: "type-union", Schema: `{"type":["string","integer"]}`, Kind: "", Valid: `"a"`, Invalid: `1.5`, Other: `null`},
	{Name: "enum", Schema: `{"enum":["a",1]}`, Kind: "", Valid: `1`, Invalid: `"b"`, Other: `null`, Second: `{"enum":[1,2]}`},
	{Name: "enum-object", Schema: `{"enum":[{"k":1}]}`, Kind: "", Valid: `{"k":1}`, Invalid: `{"k":2}`, Other: `1`},
	{Name: "const", Schema: `{"const":"a"}`, Kind: "", Valid: `"a"`, Invalid: `"b"`, Other: `1`, Second: `{"const":"a"}`},
	{Name: "const-number", Schema: `{"const":1}`, Kind: "", Valid: `1.0`, Invalid: `2`, Other: `"1"`},
	{Name: "minLength", Schema: `{"minLength":2}`, Kind: "string", Valid: `"ab"`, Invalid: `"a"`, Other: `1`, Second: `{"minLength":3}`},
	{Name: "maxLength", Schema: `{"maxLength":2}`, Kind: "string", Valid: `"ab"`, Invalid: `"abc"`, Other: `123`, Second: `{"maxLength":1}`},
	{Name: "pattern", Schema: `{"pattern":"^a"}`, Kind: "string", Valid: `"ab"`, Invalid: `"ba"`, Other: `1`, Second: `{"pattern":"b$"}`},
	{Name: "format", Schema: `{"format":"date"}`, Kind: "string", Valid: `"2020-01-01"`, Invalid: `"x"`, Other: `1`, Format: true},
	{Name: "minimum", Schema: `{"minimum":2}`, Kind: "number", Valid: `2`, Invalid: `1`, Other: `"a"`, Second: `{"minimum":3}`},
	{Name: "maximum", Schema: `{"maximum":2}`, Kind: "number", Valid: `2`, Invalid: `3`, Other: `"a"`, Second: `{"maximum":1}`},
	{Name: "exclusiveMinimum", Schema: `{"exclusiveMinimum":2}`, Kind: "number", Valid: `3`, Invalid: `2`, Other: `"a"`},
	{Name: "exclusiveMaximum", Schema: `{"exclusiveMaximum":2}`, Kind: "number", Valid: `1`, Invalid: `2`, Other: `"a"`},
	{Name: "multipleOf", Schema: `{"multipleOf":2}`, Kind: "number", Valid: `4`, Invalid: `3`, Other: `"a"`, Second: `{"multipleOf":3}`},
	{Name: "multipleOf-fraction", Schema: `{"multipleOf":0.5}`, Kind: "number", Valid: `1.5`, Invalid: `1.25`, Other: `"a"`, Second: `{"multipleOf":0.75}`},
	{Name: "minItems", Schema: `{"minItems":2}`, Kind: "array", Valid: `[1,2]`, Invalid: `[1]`, Other: `"ab"`, Second: `{"minItems":3}`},
	{Name: "maxItems", Schema: `{"maxItems":1}`, Kind: "array", Valid: `[1]`, Invalid: `[1,2]`, Other: `"ab"`},
	{Name: "uniqueItems", Schema: `{"uniqueItems":true}`, Kind: "array", Valid: `[1,2]`, Invalid: `[1,1]`, Other: `"aa"`},
	{Name: "items", Schema: `{"items":{"type":"integer"}}`, Kind: "array", Valid: `[1]`, Invalid: `["a"]`, Other: `{"a":"a"}`, Second: `{"items":{"maximum":3}}`},
	{Name: "items-bounds", Schema: `{"items":{"maximum":3}}`, Kind: "array", Valid: `[1]`, Invalid: `[5]`, Other: `5`},
	{Name: "prefixItems", Schema: `{"prefixItems":[{"type":"integer"}]}`, Kind: "array", Valid: `[1,"x"]`, Invalid: `["a"]`, Other: `"a"`, Second: `{"prefixItems":[{"maximum":3}]}`},
	{Name: "prefixItems-closed", Schema: `{"prefixItems":[{"type":"integer"}],"items":false}`, Kind: "array", Valid: `[1]`, Invalid: `[1,2]`, Other: `"a"`},
	{Name: "contains", Schema: `{"contains":{"type":"integer"}}`, Kind: "array", Valid: `["a",1]`, Invalid: `["a"]`, Other: `"a"`, Second: `{"contains":{"const":1}}`},
	{Name: "contains-bounds", Schema: `{"contains":{"minimum":3}}`, Kind: "array", Valid: `[1,5]`, Invalid: `[1]`, Other: `1`},
	{Name: "contains-enum-sibling", Schema: `{"contains":{"enum":[1,5],"minimum":3}}`, Kind: "array", Valid: `[5]`, Invalid: `[1]`, Other: `1`},
	{Name: "minContains", Schema: `{"contains":{"type":"integer"},"minContains":2}`, Kind: "array", Valid: `[1,2]`, Invalid: `[1,"a"]`, Other: `"a"`},
	{Name: "maxContains", Schema: `{"contains":{"type":"integer"},"maxContains":1}`, Kind: "array", Valid: `[1,"a"]`, Invalid: `[1,2]`, Other: `"a"`},
	{Name: "unevaluatedItems", Schema: `{"prefixItems":[{"type":"integer"}],"unevaluatedItems":false}`, Kind: "array", Valid: `[1]`, Invalid: `[1,2]`, Other: `"a"`},
	{Name: "unevaluatedItems-contains", Schema: `{"contains":{"type":"string"},"unevaluatedItems":false}`, Kind: "array", Valid: `["x"]`, Invalid: `["x",1]`, Other: `1`},
	{Name: "properties", Schema: `{"properties":{"a":{"type":"integer"}}}`, Kind: "object", Valid: `{"a":1}`, Invalid: `{"a":"x"}`, Other: `[1]`, Second: `{"properties":{"a":{"maximum":3}}}`},
	{Name: "required", Schema: `{"required":["a"]}`, Kind: "object", Valid: `{"a":1}`, Invalid: `{"b":1}`, Other: `"a"`, Second: `{"required":["b"]}`},
	{Name: "additionalProperties-false", Schema: `{"properties":{"a":{}},"additionalProperties":false}`, Kind: "object", Valid: `{"a":1}`, Invalid: `{"b":1}`, Other: `1`},
	{Name: "additionalProperties-schema", Schema: `{"additionalProperties":{"type":"integer"}}`, Kind: "object", Valid: `{"b":1}`, Invalid: `{"b":"x"}`, Other: `"x"`},
	{Name: "patternProperties", Schema: `{"patternProperties":{"^x":{"type":"integer"}}}`, Kind: "object", Valid: `{"x1":1}`, Invalid: `{"x1":"a"}`, Other: `1`, Second: `{"patternProperties":{"^x":{"maximum":3}}}`},
	{Name: "propertyNames", Schema: `{"propertyNames":{"maxLength":2}}`, Kind: "object", Valid: `{"ab":1}`, Invalid: `{"abc":1}`, Other: `"abc"`},
	{Name: "minProperties", Schema: `{"minProperties":1}`, Kind: "object", Valid: `{"a":1}`, Invalid: `{}`, Other: `[]`},
	{Name: "maxProperties", Schema: `{"maxProperties":1}`, Kind: "object", Valid: `{"a":1}`, Invalid: `{"a":1,"b":2}`, Other: `[1,2]`},
	{Name: "dependentRequired", Schema: `{"dependentRequired":{"a":["b"]}}`, Kind: "object", Valid: `{"a":1,"b":2}`, Invalid: `{"a":1}`, Other: `"a"`, Second: `{"dependentRequired":{"a":["c"]}}`},
	{Name: "dependentSchemas", Schema: `{"dependentSchemas":{"a":{"required":["b"]}}}`, Kind: "object", Valid: `{"a":1,"b":2}`, Invalid: `{"a":1}`, Other: `"a"`, Second: `{"dependentSchemas":{"a":{"required":["c"]}}}`},
	{Name: "unevaluatedProperties", Schema: `{"properties":{"a":{}},"unevaluatedProperties":false}`, Kind: "object", Valid: `{"a":1}`, Invalid: `{"a":1,"b":2}`, Other: `1`},
	{Name: "allOf", Schema: `{"allOf":[{"minLength":2},{"maxLength":3}]}`, Kind: "string", Valid: `"ab"`, Invalid: `"a"`, Other: `1`},
	{Name: "anyOf", Schema: `{"anyOf":[{"type":"string"},{"type":"integer"}]}`, Kind: "", Valid: `1`, Invalid: `true`, Other: `null`},
	{Name: "anyOf-bounds", Schema: `{"anyOf":[{"minLength":3},{"maxLength":0}]}`, Kind: "string", Valid: `"abc"`, Invalid: `"x"`, Other: `1`},
	{Name: "oneOf", Schema: `{"oneOf":[{"minimum":2},{"multipleOf":2}]}`, Kind: "number", Valid: `3`, Invalid: `4`, Other: `"a"`},
	{Name: "oneOf-types", Schema: `{"oneOf":[{"type":"string"},{"type":"integer"}]}`, Kind: "", Valid: `"a"`, Invalid: `1.5`, Other: `null`},
	{Name: "not", Schema: `{"not":{"type":"string"}}`, Kind: "", Valid: `1`, Invalid: `"a"`, Other: `null`},
	{Name: "not-const", Schema: `{"not":{"const":"x"}}`, Kind: "", Valid: `"y"`, Invalid: `"x"`, Other: `1`},
	{Name: "if-then", Schema: `{"if":{"minimum":5},"then":{"multipleOf":2}}`, Kind: "number", Valid: `6`, Invalid: `7`, Other: `"a"`},
	{Name: "if-else", Schema: `{"if":{"minimum":5},"else":{"multipleOf":2}}`, Kind: "number", Valid: `2`, Invalid: `3`, Other: `"a"`},
	{Name: "if-object", Schema: `{"if":{"required":["a"]},"then":{"required":["b"]}}`, Kind: "object", Valid: `{"a":1,"b":1}`, Invalid: `{"a":1}`, Other: `1`},
	{Name: "ref", Schema: `{"$ref":"#/$defs/%sR"}`, Kind: "string", Valid: `"ab"`, Invalid: `"a"`, Other: `1`},
	// A $dynamicRef to a $dynamicAnchor nothing in the dynamic scope
	// overrides, which resolves as a $ref to the anchor does.
	{Name: "dynamicRef", Schema: `{"$dynamicRef":"#%sD"}`, Kind: "string", Valid: `"ab"`, Invalid: `"a"`, Other: `1`},
	// An exclusive bound beside a plain one in another branch: draft 4 spells
	// it as a flag on minimum, and a merge that moved the flag onto the other
	// branch's minimum refused a value both allow.
	{Name: "exclusiveMinimum-minimum", Schema: `{"exclusiveMinimum":5}`, Kind: "number", Valid: `6`, Invalid: `5`, Other: `"a"`, Second: `{"minimum":6}`},
	// A format a Go type decodes: whether the decode enforces all of it.
	{Name: "format-ipv4", Schema: `{"format":"ipv4"}`, Kind: "string", Valid: `"1.2.3.4"`, Invalid: `""`, Other: `1`, Format: true},
	// Shapes the audit found keywords lost in, each written once here so the
	// grid, not a person, is what notices the next one like it.
	//
	// A string keyword beside the keywords that make the value a struct.
	{Name: "string-keyword-on-object", Schema: `{"properties":{"a":{}},"minLength":3}`, Kind: "string", Valid: `"abc"`, Invalid: `"ab"`, Other: `{"a":1}`},
	// A const in an if that the instance does not state, deciding which
	// properties unevaluatedProperties sees evaluated.
	{Name: "unevaluatedProperties-if-const", Schema: `{"if":{"properties":{"k":{"const":"x"}}},"then":{"properties":{"b":{}}},"unevaluatedProperties":false}`, Kind: "object", Valid: `{"b":1}`, Invalid: `{"c":1}`, Other: `1`},
	// Object keywords beside a type that admits no object.
	{Name: "null-with-properties", Schema: `{"type":"null","properties":{"a":{"oneOf":[{"type":"string"},{"type":"integer"}]}}}`, Kind: "null", Valid: `null`, Invalid: `{}`, Other: `0`},
	// A contains schema that is always true but for the keyword beside it.
	{Name: "contains-if-false", Schema: `{"contains":{"if":false,"else":true,"type":"string"}}`, Kind: "array", Valid: `["x"]`, Invalid: `[1]`, Other: `1`},
	// A property type stated in only one anyOf branch.
	{Name: "anyOf-property-type", Schema: `{"anyOf":[{"required":["a"],"properties":{"a":{"type":"string"}}},{"required":["b"]}]}`, Kind: "object", Valid: `{"a":1,"b":1}`, Invalid: `{"a":1}`, Other: `1`},
	{Name: "false", Schema: `false`, Kind: "", Valid: ``, Invalid: `1`, Other: `"a"`},
	{Name: "empty-enum", Schema: `{"enum":[]}`, Kind: "", Valid: ``, Invalid: `1`, Other: `null`},
}

// refTargets are the $defs a row's schema names, by the name under $defs,
// with %s standing for the cell's unique prefix.
var refTargets = map[string]map[string]string{
	"ref":        {"%sR": `{"minLength":2}`},
	"dynamicRef": {"%sD": `{"$dynamicAnchor":"%sD","minLength":2}`},
}

// Position is where the keyword's subschema stands in the cell's schema.
type Position struct {
	Name string
	// Wrap places the subschema s in a document root.
	Wrap func(s any) map[string]any
	// Instance maps a keyword-level document, as JSON text, to the whole
	// document, or reports that the position cannot hold it. It works on the
	// text so that a number spelling the row chose on purpose -- 1.0, which
	// asks whether a fractionless number is an integer -- reaches the
	// document as written.
	Instance func(v string) (string, bool)
	// Preserves says whether the whole document's verdict is the
	// keyword-level document's (true), its negation (false with Inverts), or
	// neither decided by construction.
	Preserves, Inverts bool
}

func obj(kv ...any) map[string]any {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func same(v string) (string, bool) { return v, true }

// Positions is the position axis.
var Positions = []Position{
	{Name: "root", Wrap: func(s any) map[string]any { return asObject(s) }, Instance: same, Preserves: true},
	{Name: "property", Wrap: func(s any) map[string]any {
		return obj("type", "object", "properties", obj("p", s))
	}, Instance: func(v string) (string, bool) { return `{"p":` + v + `}`, true }, Preserves: true},
	{Name: "items", Wrap: func(s any) map[string]any {
		return obj("type", "array", "items", s)
	}, Instance: func(v string) (string, bool) { return `[` + v + `]`, true }, Preserves: true},
	{Name: "prefixItems", Wrap: func(s any) map[string]any {
		return obj("type", "array", "prefixItems", []any{s})
	}, Instance: func(v string) (string, bool) { return `[` + v + `]`, true }, Preserves: true},
	{Name: "contains", Wrap: func(s any) map[string]any {
		return obj("type", "array", "contains", s)
	}, Instance: func(v string) (string, bool) { return `[` + v + `]`, true }, Preserves: true},
	{Name: "additionalProperties", Wrap: func(s any) map[string]any {
		return obj("type", "object", "additionalProperties", s)
	}, Instance: func(v string) (string, bool) { return `{"k":` + v + `}`, true }, Preserves: true},
	{Name: "patternProperties", Wrap: func(s any) map[string]any {
		return obj("type", "object", "patternProperties", obj("^k", s))
	}, Instance: func(v string) (string, bool) { return `{"k":` + v + `}`, true }, Preserves: true},
	{Name: "propertyNames", Wrap: func(s any) map[string]any {
		return obj("type", "object", "propertyNames", s)
	}, Instance: func(v string) (string, bool) {
		// A property name is a string, so only a string document has a
		// place here.
		if !strings.HasPrefix(v, `"`) {
			return "", false
		}
		return `{` + v + `:1}`, true
	}, Preserves: true},
	{Name: "dependentSchemas", Wrap: func(s any) map[string]any {
		return obj("type", "object", "dependentSchemas", obj("t", s))
	}, Instance: func(v string) (string, bool) {
		// The subschema applies to the object that has the trigger, so only an
		// object document has a place here, and it gains the trigger -- which
		// is why the position decides no verdict by construction.
		if !strings.HasPrefix(v, "{") {
			return "", false
		}
		rest := strings.TrimSpace(strings.TrimPrefix(v, "{"))
		if rest == "}" {
			return `{"t":0}`, true
		}
		return `{"t":0,` + rest, true
	}, Preserves: false},
	{Name: "if-then", Wrap: func(s any) map[string]any { return obj("if", true, "then", s) }, Instance: same, Preserves: true},
	{Name: "if-else", Wrap: func(s any) map[string]any { return obj("if", false, "else", s) }, Instance: same, Preserves: true},
	{Name: "if-then-false", Wrap: func(s any) map[string]any { return obj("if", s, "then", false) }, Instance: same, Inverts: true},
	{Name: "oneOf-false", Wrap: func(s any) map[string]any { return obj("oneOf", []any{s, false}) }, Instance: same, Preserves: true},
	{Name: "anyOf-false", Wrap: func(s any) map[string]any { return obj("anyOf", []any{s, false}) }, Instance: same, Preserves: true},
	{Name: "allOf", Wrap: func(s any) map[string]any { return obj("allOf", []any{s}) }, Instance: same, Preserves: true},
	{Name: "ref", Wrap: func(s any) map[string]any {
		return obj("$ref", "#/$defs/%sP", "$defs", obj("%sP", s))
	}, Instance: same, Preserves: true},
	{Name: "nested-allOf", Wrap: func(s any) map[string]any {
		return obj("allOf", []any{obj("allOf", []any{s})})
	}, Instance: same, Preserves: true},
	{Name: "not-not", Wrap: func(s any) map[string]any { return obj("not", obj("not", s)) }, Instance: same, Preserves: true},
}

// Composition is what a real schema writes beside the keyword.
type Composition struct {
	Name string
	// Apply returns the subschema with the composition applied, and false
	// where it does not apply to the row. The defs it returns are placed in
	// the cell root's $defs.
	Apply func(r Row, s map[string]any) (any, map[string]any, bool)
	// Preserves says whether the composition leaves the row's verdicts as
	// they are, so the construction still decides them.
	Preserves bool
	// preserves, where set, narrows Preserves by dialect.
	preserves func(d Dialect) bool
	// ofRowKind narrows Preserves to the documents of the row's own kind: a
	// `type` stating that kind beside the keyword leaves their verdicts as the
	// keyword gives them, and refuses a document of any other kind whatever
	// the keyword says.
	ofRowKind bool
}

func (c Composition) preservesUnder(d Dialect) bool {
	return c.Preserves && (c.preserves == nil || c.preserves(d))
}

// documentOfKind reports whether the JSON document doc is of kind in every
// dialect: an integer is written without a fraction or an exponent, because
// draft 4 does not count 1.0 as one.
func documentOfKind(doc, kind string) bool {
	var v any
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		panic(err)
	}
	switch x := v.(type) {
	case nil:
		return kind == "null"
	case bool:
		return kind == "boolean"
	case string:
		return kind == "string"
	case []any:
		return kind == "array"
	case map[string]any:
		return kind == "object"
	case float64:
		if kind == "number" {
			return true
		}
		return kind == "integer" && !strings.ContainsAny(strings.TrimSpace(doc), ".eE") && x == float64(int64(x))
	}
	return false
}

func otherKind(kind string) string {
	switch kind {
	case "string":
		return "number"
	case "number", "integer":
		return "string"
	case "array":
		return "object"
	case "object":
		return "array"
	case "boolean":
		return "string"
	case "null":
		return "string"
	}
	return ""
}

func with(s map[string]any, kv ...any) map[string]any {
	out := make(map[string]any, len(s)+len(kv)/2)
	// maporder: copies members under their own keys, which are distinct.
	for k, v := range s {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

// Compositions is the composition axis.
var Compositions = []Composition{
	{Name: "plain", Apply: func(r Row, s map[string]any) (any, map[string]any, bool) { return s, nil, true }, Preserves: true},
	{Name: "typed", Apply: func(r Row, s map[string]any) (any, map[string]any, bool) {
		if r.Kind == "" || s["type"] != nil {
			return nil, nil, false
		}
		return with(s, "type", r.Kind), nil, true
	}, Preserves: true, ofRowKind: true},
	{Name: "nullable", Apply: func(r Row, s map[string]any) (any, map[string]any, bool) {
		if r.Kind == "" || r.Kind == "null" || s["type"] != nil {
			return nil, nil, false
		}
		return with(s, "type", []any{r.Kind, "null"}), nil, true
	}},
	{Name: "multitype", Apply: func(r Row, s map[string]any) (any, map[string]any, bool) {
		if r.Kind == "" || s["type"] != nil {
			return nil, nil, false
		}
		return with(s, "type", []any{r.Kind, otherKind(r.Kind)}), nil, true
	}},
	{Name: "allOf-twice", Apply: func(r Row, s map[string]any) (any, map[string]any, bool) {
		if r.Second == "" {
			return nil, nil, false
		}
		var second any
		if err := json.Unmarshal([]byte(r.Second), &second); err != nil {
			panic(err)
		}
		return obj("allOf", []any{s, second}), nil, true
	}},
	{Name: "ref-sibling", Apply: func(r Row, s map[string]any) (any, map[string]any, bool) {
		if s["$ref"] != nil {
			return nil, nil, false
		}
		target := map[string]any{}
		if r.Kind != "" {
			target = obj("type", r.Kind)
		}
		return with(s, "$ref", "#/$defs/%sT"), obj("%sT", target), true
	}, Preserves: true,
		// Up to draft 7 a $ref replaces its siblings, so the row's keyword
		// beside it says nothing.
		preserves: func(d Dialect) bool { return d.level >= 2019 }},
	{Name: "enum-sibling", Apply: func(r Row, s map[string]any) (any, map[string]any, bool) {
		if s["enum"] != nil || s["const"] != nil || r.Valid == "" {
			return nil, nil, false
		}
		var members []any
		for _, doc := range []string{r.Valid, r.Invalid, r.Other} {
			var v any
			if err := json.Unmarshal([]byte(doc), &v); err != nil {
				panic(err)
			}
			members = append(members, v)
		}
		return with(s, "enum", members), nil, true
	}, Preserves: true},
	{Name: "format-sibling", Apply: func(r Row, s map[string]any) (any, map[string]any, bool) {
		if s["format"] != nil {
			return nil, nil, false
		}
		return with(s, "format", "date"), nil, true
	}, Preserves: true,
		// Where format asserts, the row's documents are not dates.
		preserves: func(d Dialect) bool { return !d.formatAsserts() }},
	{Name: "unevaluated", Apply: func(r Row, s map[string]any) (any, map[string]any, bool) {
		switch r.Kind {
		case "object":
			if s["unevaluatedProperties"] != nil {
				return nil, nil, false
			}
			return with(s, "unevaluatedProperties", false), nil, true
		case "array":
			if s["unevaluatedItems"] != nil {
				return nil, nil, false
			}
			return with(s, "unevaluatedItems", false), nil, true
		}
		return nil, nil, false
	}},
	{Name: "oneOf-null", Apply: func(r Row, s map[string]any) (any, map[string]any, bool) {
		return obj("oneOf", []any{s, obj("type", "null")}), nil, true
	}},
	{Name: "alias", Apply: func(r Row, s map[string]any) (any, map[string]any, bool) {
		if r.Kind == "" || r.Kind == "object" || r.Kind == "array" || s["type"] != nil {
			return nil, nil, false
		}
		return obj("$ref", "#/$defs/%sA"), obj("%sA", with(s, "type", r.Kind)), true
	}},
}

// Cell is one grid cell: a whole schema, the documents it is judged on, and
// what the construction says each should get where it says anything.
type Cell struct {
	Dialect, Row, Position, Composition string
	// Key names the cell uniquely and stably: dialect/row/position/composition.
	Key string
	// Prefix is the Go-identifier-safe prefix the cell's $defs keys carry, so
	// cells generated into one package do not collide. It is drawn from a hash
	// of the key, so no other cell's place in the axes changes it.
	Prefix string
	Schema json.RawMessage
	// Content is Schema with a prefix that names no cell: what the schema
	// says, and nothing about where in the grid it stands. A verdict is
	// recorded against it (VerdictKey), so a row inserted, moved or removed
	// changes the key of no other cell's document.
	Content   json.RawMessage
	Instances []json.RawMessage
	// Constructed is the verdict the construction gives each instance, or nil
	// where it gives none. Only the valid and invalid documents of a row are
	// constructed; the other-kind document's verdict depends on the keyword.
	Constructed []*bool
}

// VerdictKey is the key a verdict for one document against one schema is
// recorded under: a hash of the two as the grid writes them. schema is a
// cell's Content.
func VerdictKey(schema, instance json.RawMessage) string {
	h := sha256.New()
	h.Write(schema)
	h.Write([]byte{0})
	h.Write(instance)
	return hex.EncodeToString(h.Sum(nil))[:24]
}

func asObject(s any) map[string]any {
	if m, ok := s.(map[string]any); ok {
		return m
	}
	// A boolean schema at the root: allOf holds it without changing its
	// meaning, and the root still carries $schema.
	return obj("allOf", []any{s})
}

// substitute replaces the %s placeholder in every string of v with prefix.
func substitute(v any, prefix string) any {
	switch x := v.(type) {
	case string:
		return strings.ReplaceAll(x, "%s", prefix)
	case map[string]any:
		out := make(map[string]any, len(x))
		// In key order: two keys could spell the same key once substituted,
		// and which one is kept must not depend on the map's order.
		for _, k := range slices.Sorted(maps.Keys(x)) {
			e := x[k]
			out[strings.ReplaceAll(k, "%s", prefix)] = substitute(e, prefix)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = substitute(e, prefix)
		}
		return out
	}
	return v
}

// Cells builds every cell of the grid, in a fixed order.
func Cells() []Cell {
	var cells []Cell
	prefixes := map[string]string{}
	for _, dl := range Dialects {
		for _, r := range Rows {
			cells = appendRowCells(cells, dl, r, prefixes)
		}
	}
	sort.SliceStable(cells, func(i, j int) bool { return cells[i].Key < cells[j].Key })
	return cells
}

func appendRowCells(cells []Cell, dl Dialect, r Row, prefixes map[string]string) []Cell {
	var kw any
	if err := json.Unmarshal([]byte(r.Schema), &kw); err != nil {
		panic(fmt.Sprintf("row %s: %v", r.Name, err))
	}
	for _, p := range Positions {
		for _, c := range Compositions {
			var s any = kw
			var defs map[string]any
			if m, ok := kw.(map[string]any); ok {
				applied, d, ok := c.Apply(r, m)
				if !ok {
					continue
				}
				s, defs = applied, d
			} else if c.Name != "plain" {
				continue
			}
			// A copy: the root position hands back the row's own subschema,
			// which every other cell of the row shares, and the $defs written
			// below would otherwise land in all of them.
			root := with(p.Wrap(s))
			allDefs := map[string]any{}
			if d, ok := root["$defs"].(map[string]any); ok {
				// maporder: copies members under their own keys, which are distinct.
				for k, v := range d {
					allDefs[k] = v
				}
			}
			// maporder: copies members under their own keys, which are distinct;
			// a composition's def replacing the position's is decided by the
			// order of the two loops, not by either map's.
			for k, v := range defs {
				allDefs[k] = v
			}
			// maporder: copies members under their own keys, which are distinct.
			for name, t := range refTargets[r.Name] {
				var target any
				if err := json.Unmarshal([]byte(t), &target); err != nil {
					panic(err)
				}
				allDefs[name] = target
			}
			if len(allDefs) > 0 {
				root["$defs"] = allDefs
			}
			translated, ok := translate(root, dl.level)
			if !ok {
				continue
			}
			root = with(asObject(translated), "$schema", dl.URI)
			key := dl.Name + "/" + r.Name + "/" + p.Name + "/" + c.Name
			sum := sha256.Sum256([]byte(key))
			prefix := "G" + hex.EncodeToString(sum[:5])
			if other, taken := prefixes[prefix]; taken {
				panic(fmt.Sprintf("cells %s and %s draw one prefix %s", other, key, prefix))
			}
			prefixes[prefix] = key
			schemaJSON, err := json.Marshal(substitute(root, prefix))
			if err != nil {
				panic(err)
			}
			content, err := json.Marshal(substitute(root, "G"))
			if err != nil {
				panic(err)
			}
			cell := Cell{
				Dialect: dl.Name, Row: r.Name, Position: p.Name, Composition: c.Name,
				Key:     key,
				Prefix:  prefix,
				Schema:  schemaJSON,
				Content: content,
			}
			for i, doc := range []string{r.Valid, r.Invalid, r.Other} {
				if doc == "" {
					continue
				}
				if !json.Valid([]byte(doc)) {
					panic(fmt.Sprintf("row %s document %q is not JSON", r.Name, doc))
				}
				whole, ok := p.Instance(doc)
				if !ok {
					continue
				}
				if !json.Valid([]byte(whole)) {
					panic(fmt.Sprintf("cell %s document %q is not JSON", cell.Key, whole))
				}
				cell.Instances = append(cell.Instances, json.RawMessage(whole))
				var constructed *bool
				annotation := r.Format && !dl.formatAsserts()
				if i < 2 && !annotation && c.preservesUnder(dl) && (p.Preserves || p.Inverts) &&
					(!c.ofRowKind || documentOfKind(doc, r.Kind)) {
					verdict := i == 0
					if p.Inverts {
						verdict = !verdict
					}
					constructed = &verdict
				}
				cell.Constructed = append(cell.Constructed, constructed)
			}
			if len(cell.Instances) > 0 {
				cells = append(cells, cell)
			}
		}
	}
	return cells
}

// translate writes a 2020-12 schema in the spelling of the dialect at level,
// and reports false where it uses a keyword the dialect does not have. It
// knows which keywords hold subschemas, so instance data -- an enum's members,
// a const, a property's name -- is copied as it is.
func translate(v any, level int) (any, bool) {
	if level >= 2020 {
		return v, true
	}
	switch x := v.(type) {
	case bool:
		if level <= 4 {
			// Draft 4 has no boolean schemas; these two say the same.
			if x {
				return map[string]any{}, true
			}
			return obj("not", map[string]any{}), true
		}
		return x, true
	case map[string]any:
		return translateObject(x, level)
	}
	return v, true
}

func translateObject(s map[string]any, level int) (any, bool) {
	out := make(map[string]any, len(s))
	sub := func(v any) (any, bool) { return translate(v, level) }
	subs := func(v any) (any, bool) {
		list, _ := v.([]any)
		res := make([]any, len(list))
		for i, e := range list {
			t, ok := sub(e)
			if !ok {
				return nil, false
			}
			res[i] = t
		}
		return res, true
	}
	subMap := func(v any) (map[string]any, bool) {
		m, _ := v.(map[string]any)
		res := make(map[string]any, len(m))
		// maporder: translates members under their own keys, which are
		// distinct, and any member that fails fails the whole map the same way.
		for k, e := range m {
			t, ok := sub(e)
			if !ok {
				return nil, false
			}
			res[k] = t
		}
		return res, true
	}
	set := func(k string, v any) bool {
		if _, taken := out[k]; taken {
			return false
		}
		out[k] = v
		return true
	}
	defsKey := "$defs"
	if level <= 7 {
		defsKey = "definitions"
	}
	// maporder: each member is written under its own key, and a collision
	// refuses the cell whichever member meets it first.
	for k, v := range s {
		var ok bool
		switch k {
		case "$defs":
			var m map[string]any
			if m, ok = subMap(v); ok {
				ok = set(defsKey, m)
			}
		case "$ref":
			ref, _ := v.(string)
			if level <= 7 {
				ref = strings.Replace(ref, "#/$defs/", "#/definitions/", 1)
			}
			ok = set("$ref", ref)
		case "$dynamicRef", "$dynamicAnchor":
			ok = false
		case "prefixItems":
			var list any
			if list, ok = subs(v); ok {
				ok = set("items", list)
			}
		case "items":
			var t any
			if t, ok = sub(v); ok {
				if _, tuple := s["prefixItems"]; tuple {
					ok = set("additionalItems", t)
				} else {
					ok = set("items", t)
				}
			}
		case "unevaluatedItems", "unevaluatedProperties", "minContains", "maxContains":
			ok = level >= 2019
			if ok {
				var t any
				if t, ok = sub(v); ok {
					ok = set(k, t)
				}
			}
		case "dependentRequired", "dependentSchemas":
			if level >= 2019 {
				var m any = v
				if k == "dependentSchemas" {
					m, ok = subMap(v)
				} else {
					ok = true
				}
				ok = ok && set(k, m)
				break
			}
			deps, _ := out["dependencies"].(map[string]any)
			if deps == nil {
				deps = map[string]any{}
				out["dependencies"] = deps
			}
			ok = true
			// maporder: each trigger is written under its own name.
			for trigger, e := range v.(map[string]any) {
				if _, taken := deps[trigger]; taken {
					ok = false
					break
				}
				if k == "dependentSchemas" {
					var t any
					if t, ok = sub(e); !ok {
						break
					}
					e = t
				}
				deps[trigger] = e
			}
		case "contains", "propertyNames":
			ok = level >= 6
			if ok {
				var t any
				if t, ok = sub(v); ok {
					ok = set(k, t)
				}
			}
		case "const":
			ok = level >= 6 && set(k, v)
		case "if", "then", "else":
			ok = level >= 7
			if ok {
				var t any
				if t, ok = sub(v); ok {
					ok = set(k, t)
				}
			}
		case "exclusiveMinimum", "exclusiveMaximum":
			if level <= 4 {
				bound := "minimum"
				if k == "exclusiveMaximum" {
					bound = "maximum"
				}
				if _, both := s[bound]; both {
					ok = false
					break
				}
				ok = set(bound, v) && set(k, true)
				break
			}
			ok = set(k, v)
		case "not", "additionalProperties", "additionalItems":
			var t any
			if t, ok = sub(v); ok {
				ok = set(k, t)
			}
		case "allOf", "anyOf", "oneOf":
			var list any
			if list, ok = subs(v); ok {
				ok = set(k, list)
			}
		case "properties", "patternProperties":
			var m map[string]any
			if m, ok = subMap(v); ok {
				ok = set(k, m)
			}
		default:
			ok = set(k, v)
		}
		if !ok {
			return nil, false
		}
	}
	return out, true
}
