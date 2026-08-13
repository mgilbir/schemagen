package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// errorPathCase is one document put to a compiled type, together with the whole
// of the message the type is expected to answer with.
//
// Want is compared for equality and never for containment. The sweep that found
// issues #279 and #280 caught its own harness matching "p" inside "PLANTEDp" and
// reporting nothing had changed; a path assertion decided by strings.Contains
// passes under exactly the defect it is written to catch, because a wrong path
// is a right path with something extra glued to it. An empty Want means the
// document is legal and must be accepted -- which is what holds the verdicts
// still while the messages move.
type errorPathCase struct {
	Name   string
	Doc    string
	Want   string
	Reason string
}

type errorPathFixture struct {
	Name   string
	Schema string
	Cases  []errorPathCase
}

// TestErrorPathsNameTheDocument holds every validation message to a path a
// caller can follow in their own document, which is what issues #279 and #280
// were each one half of.
//
// Both were one defect wearing two faces: a placeholder segment written where
// the generator had no name to write. A materialized $defs type reported its own
// root under "value", so a $ref to a scalar came out as `p.value: ...` -- a
// member that does not exist, and one that can name a real sibling. A root array
// or map reported under the keyword that governed it, so `[1]` came out as
// `items[1]` and `kk` as `additionalProperties["kk"]` -- byte for byte what a
// document with a member of that name would print. The same site glued a "."
// in front of a message that was prose rather than a path, which is how
// `too many properties: ...` became `cfg.too many properties: ...`.
//
// The fix is that a message carries what has to go in front of it (see
// jsonPathError in the emitted helpers), so the three cases are told apart at
// the join rather than papered over with an invented segment. These fixtures are
// therefore run compiled: the answer is a string a document produces at runtime,
// and no golden comparison can tell a path that reads plausibly from one that is
// true.
func TestErrorPathsNameTheDocument(t *testing.T) {
	runErrorPathFixtures(t, "error_path_test", errorPathFixtures())
}

func errorPathFixtures() []errorPathFixture {
	return []errorPathFixture{
		// ---- issue #279: the phantom ".value" segment ----
		{
			// The issue's own reproduction. The document's real "value" property
			// is legal, and the message sent the caller to it.
			Name: "ref_to_scalar_beside_a_real_value_property",
			Schema: `{"type":"object",
			  "properties":{"p":{"$ref":"#/$defs/T"},"value":{"type":"integer","minimum":100}},
			  "$defs":{"T":{"type":"string","minLength":3}}}`,
			Cases: []errorPathCase{
				{Name: "the ref is at fault", Doc: `{"p":"ab","value":200}`,
					Want:   `p: length 2 is less than minimum 3`,
					Reason: "the faulty member is p; there is no p.value, and naming one points at a sibling that is legal"},
				{Name: "the real value property is at fault", Doc: `{"p":"abc","value":1}`,
					Want:   `value: value 1 is less than minimum 100`,
					Reason: "control: a member actually named value still reports under its own name"},
				{Name: "both legal", Doc: `{"p":"abc","value":200}`,
					Reason: "control: no verdict may change"},
			},
		},
		{
			Name:   "ref_to_number",
			Schema: `{"type":"object","properties":{"num":{"$ref":"#/$defs/N"}},"$defs":{"N":{"type":"integer","minimum":5}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"num":1}`, Want: `num: 1 is less than minimum 5`,
					Reason: "was num.value"},
				{Name: "accepts", Doc: `{"num":9}`, Reason: "control"},
			},
		},
		{
			Name:   "ref_to_array",
			Schema: `{"type":"object","properties":{"arr":{"$ref":"#/$defs/A"}},"$defs":{"A":{"type":"array","minItems":2,"items":{"type":"string"}}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"arr":["x"]}`, Want: `arr: has 1 items, minimum is 2`,
					Reason: "was arr.value; the array itself is at fault, so there is no member to name"},
				{Name: "accepts", Doc: `{"arr":["x","y"]}`, Reason: "control"},
			},
		},
		{
			Name:   "ref_to_object_is_the_control",
			Schema: `{"type":"object","properties":{"obj":{"$ref":"#/$defs/O"}},"$defs":{"O":{"type":"object","properties":{"r":{"type":"string"}},"required":["r"]}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"obj":{}}`, Want: `obj.r: required property is missing`,
					Reason: "control: a $ref to an object was always right and must stay so"},
				{Name: "accepts", Doc: `{"obj":{"r":"x"}}`, Reason: "control"},
			},
		},
		{
			// The nested positions the phantom segment could reappear at.
			Name:   "ref_to_scalar_inside_an_array",
			Schema: `{"type":"object","properties":{"list":{"type":"array","items":{"$ref":"#/$defs/T"}}},"$defs":{"T":{"type":"string","minLength":3}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"list":["abc","b"]}`, Want: `list[1]: length 1 is less than minimum 3`,
					Reason: "was list[1].value"},
				{Name: "accepts", Doc: `{"list":["abc","bcd"]}`, Reason: "control"},
			},
		},
		{
			Name:   "ref_to_scalar_inside_a_map",
			Schema: `{"type":"object","properties":{"m":{"type":"object","additionalProperties":{"$ref":"#/$defs/T"}}},"$defs":{"T":{"type":"string","minLength":3}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"m":{"kk":"b"}}`, Want: `m["kk"]: length 1 is less than minimum 3`,
					Reason: `was m["kk"].value`},
				{Name: "accepts", Doc: `{"m":{"kk":"bbb"}}`, Reason: "control"},
			},
		},
		{
			Name: "ref_to_scalar_two_levels_deep",
			Schema: `{"type":"object","properties":{"a":{"type":"object","properties":{"b":{"type":"object","properties":{"p":{"$ref":"#/$defs/T"}}}}}},
			  "$defs":{"T":{"type":"string","minLength":3}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"a":{"b":{"p":"ab"}}}`, Want: `a.b.p: length 2 is less than minimum 3`,
					Reason: "was a.b.p.value"},
				{Name: "accepts", Doc: `{"a":{"b":{"p":"abc"}}}`, Reason: "control"},
			},
		},
		{
			Name:   "ref_to_scalar_inside_a_map_of_arrays",
			Schema: `{"type":"object","properties":{"m":{"type":"object","additionalProperties":{"type":"array","items":{"$ref":"#/$defs/T"}}}},"$defs":{"T":{"type":"string","minLength":3}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"m":{"kk":["abc","b"]}}`, Want: `m["kk"][1]: length 1 is less than minimum 3`,
					Reason: `was m["kk"][1].value`},
				{Name: "accepts", Doc: `{"m":{"kk":["abc"]}}`, Reason: "control"},
			},
		},
		{
			// A $ref to an array of objects: the whole path below the property is
			// the document's, and the alias contributed a keyword segment to it.
			Name: "ref_to_an_array_of_objects",
			Schema: `{"type":"object","properties":{"a":{"$ref":"#/$defs/L"}},
			  "$defs":{"L":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string","minLength":3}}}}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"a":[{"name":"aaa"},{"name":"aaa"},{"name":"b"}]}`,
					Want:   `a[2].name: length 1 is less than minimum 3`,
					Reason: "was a.items[2].name; the document has no member a.items"},
				{Name: "accepts", Doc: `{"a":[{"name":"aaa"}]}`, Reason: "control"},
			},
		},

		// ---- issue #279, second symptom: a "." in front of prose ----
		{
			Name:   "prose_about_a_nested_object",
			Schema: `{"type":"object","properties":{"cfg":{"type":"object","maxProperties":1}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"cfg":{"a":1,"b":2}}`,
					Want:   `cfg: too many properties: 2 exceeds maximum 1`,
					Reason: `was cfg.too many properties, which reads as a member named "too many properties"`},
				{Name: "accepts", Doc: `{"cfg":{"a":1}}`, Reason: "control"},
			},
		},
		{
			Name:   "prose_about_an_additional_property",
			Schema: `{"type":"object","properties":{"box":{"type":"object","properties":{"a":{"type":"integer"}},"additionalProperties":false}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"box":{"a":1,"zz":2}}`,
					Want:   `box: additional property "zz" is not allowed`,
					Reason: "was box.additional property ..."},
				{Name: "accepts", Doc: `{"box":{"a":1}}`, Reason: "control"},
			},
		},
		{
			Name:   "prose_three_levels_down",
			Schema: `{"type":"object","properties":{"a":{"type":"object","properties":{"b":{"type":"object","properties":{"c":{"type":"object","minProperties":2}}}}}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"a":{"b":{"c":{"x":1}}}}`,
					Want:   `a.b.c: too few properties: 1 is less than minimum 2`,
					Reason: "the join is wrong at every depth, not only the first"},
				{Name: "accepts", Doc: `{"a":{"b":{"c":{"x":1,"y":2}}}}`, Reason: "control"},
			},
		},
		{
			Name:   "prose_under_an_array_element",
			Schema: `{"type":"object","properties":{"arr":{"type":"array","items":{"type":"object","properties":{"a":{"type":"integer"}},"additionalProperties":false}}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"arr":[{"a":1},{"a":1,"zz":2}]}`,
					Want:   `arr[1]: additional property "zz" is not allowed`,
					Reason: "was arr[1].additional property ..."},
				{Name: "accepts", Doc: `{"arr":[{"a":1}]}`, Reason: "control"},
			},
		},
		{
			Name:   "prose_from_unevaluated_properties",
			Schema: `{"type":"object","properties":{"arr":{"type":"array","items":{"allOf":[{"type":"object","properties":{"a":{"type":"integer"}}}],"unevaluatedProperties":false}}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"arr":[{"a":1},{"a":1,"zz":2}]}`,
					Want:   `arr[1]: unevaluated property "zz" is not allowed`,
					Reason: "was arr[1].unevaluated property ..."},
				{Name: "accepts", Doc: `{"arr":[{"a":1}]}`, Reason: "control"},
			},
		},
		{
			Name:   "prose_from_an_enum",
			Schema: `{"type":"object","properties":{"e":{"enum":["a","b"]}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"e":"z"}`, Want: `e: invalid RootE value: z`,
					Reason: "was e.invalid RootE value: z"},
				{Name: "accepts", Doc: `{"e":"a"}`, Reason: "control"},
			},
		},

		// ---- issue #280: a root array or map naming its keyword ----
		{
			Name:   "root_array_of_scalars",
			Schema: `{"type":"array","items":{"type":"string","minLength":3}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `["aaa","b"]`, Want: `[1]: length 1 is less than minimum 3`,
					Reason: `was items[1], which is what {"properties":{"items":...}} prints`},
				{Name: "accepts", Doc: `["aaa","bbb"]`, Reason: "control"},
			},
		},
		{
			Name:   "root_array_of_objects",
			Schema: `{"type":"array","items":{"type":"object","properties":{"n":{"type":"integer","minimum":5}}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `[{"n":9},{"n":1}]`, Want: `[1].n: value 1 is less than minimum 5`,
					Reason: "was items[1].n"},
				{Name: "accepts", Doc: `[{"n":9}]`, Reason: "control"},
			},
		},
		{
			Name:   "root_array_of_arrays",
			Schema: `{"type":"array","items":{"type":"array","items":{"type":"string","minLength":3}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `[["aaa"],["aaa","b"]]`, Want: `[1][1]: length 1 is less than minimum 3`,
					Reason: "was items[1][1]"},
				{Name: "accepts", Doc: `[["aaa"]]`, Reason: "control"},
			},
		},
		{
			Name:   "root_map",
			Schema: `{"type":"object","additionalProperties":{"type":"string","minLength":3}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"kk":"b"}`, Want: `["kk"]: length 1 is less than minimum 3`,
					Reason: `was additionalProperties["kk"]`},
				{Name: "accepts", Doc: `{"kk":"bbb"}`, Reason: "control"},
			},
		},
		{
			Name:   "root_map_of_objects",
			Schema: `{"type":"object","additionalProperties":{"type":"object","properties":{"n":{"type":"integer","minimum":5}}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"kk":{"n":1}}`, Want: `["kk"].n: value 1 is less than minimum 5`,
					Reason: `was additionalProperties["kk"].n`},
				{Name: "accepts", Doc: `{"kk":{"n":9}}`, Reason: "control"},
			},
		},
		{
			// Why a root map key keeps its brackets and quotes rather than being
			// written bare: a key may contain the separator, and `a.b: ...` would
			// read as two steps into a document that has one.
			Name:   "root_map_key_containing_a_dot",
			Schema: `{"type":"object","additionalProperties":{"type":"string","minLength":3}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"a.b":"x"}`, Want: `["a.b"]: length 1 is less than minimum 3`,
					Reason: "a bare key would print a.b, which names a nested member the document has not got"},
				{Name: "accepts", Doc: `{"a.b":"xyz"}`, Reason: "control"},
			},
		},
		{
			// The overflow map beside declared properties: the same keyword, the
			// same substitution, one level in.
			Name:   "overflow_map_beside_declared_properties",
			Schema: `{"type":"object","properties":{"cfg":{"type":"object","properties":{"alpha":{"type":"string"}},"additionalProperties":{"type":"integer","minimum":5}}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"cfg":{"alpha":"a","zz":1}}`,
					Want:   `cfg["zz"]: value 1 is less than minimum 5`,
					Reason: `was cfg.additionalProperties["zz"]; "zz" is a key the document really carries`},
				{Name: "accepts", Doc: `{"cfg":{"alpha":"a","zz":9}}`, Reason: "control"},
			},
		},

		{
			// The decode carries the same paths as the checks, and carried the
			// same invented segment. These two are raised by UnmarshalJSON rather
			// than by Validate.
			Name:   "overflow_null_names_the_key",
			Schema: `{"type":"object","properties":{"alpha":{"type":"string"}},"additionalProperties":{"type":"integer"}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"alpha":"a","zz":null}`, Want: `["zz"]: null is not allowed`,
					Reason: `was additionalProperties["zz"]`},
				{Name: "accepts", Doc: `{"alpha":"a","zz":1}`, Reason: "control"},
			},
		},
		{
			Name:   "root_array_alias_null_names_the_index",
			Schema: `{"$ref":"#/$defs/L","$defs":{"L":{"type":"array","items":{"type":"string"}}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `["x",null]`, Want: `[1]: null is not allowed`,
					Reason: "was value[1]"},
				{Name: "accepts", Doc: `["x"]`, Reason: "control"},
			},
		},

		// ---- the positions that were already right ----
		{
			Name:   "control_declared_array_property",
			Schema: `{"type":"object","properties":{"arr":{"type":"array","items":{"type":"string","minLength":3}}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"arr":["aaa","b"]}`, Want: `arr[1]: length 1 is less than minimum 3`,
					Reason: "control: a named array keeps its name"},
				{Name: "accepts", Doc: `{"arr":["aaa"]}`, Reason: "control"},
			},
		},
		{
			Name:   "control_two_nested_arrays",
			Schema: `{"type":"object","properties":{"rows":{"type":"array","items":{"type":"object","properties":{"cells":{"type":"array","items":{"type":"string","minLength":3}}}}}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"rows":[{"cells":["aaa"]},{"cells":["aaa","b"]}]}`,
					Want: `rows[1].cells[1]: length 1 is less than minimum 3`, Reason: "control"},
				{Name: "accepts", Doc: `{"rows":[{"cells":["aaa"]}]}`, Reason: "control"},
			},
		},
		{
			Name:   "control_declared_map_property",
			Schema: `{"type":"object","properties":{"m":{"type":"object","additionalProperties":{"type":"string","minLength":3}}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"m":{"kk":"b"}}`, Want: `m["kk"]: length 1 is less than minimum 3`,
					Reason: "control: a named map keeps its name"},
				{Name: "accepts", Doc: `{"m":{"kk":"bbb"}}`, Reason: "control"},
			},
		},
		{
			Name:   "control_tuple_position",
			Schema: `{"type":"object","properties":{"tup":{"type":"array","prefixItems":[{"type":"string"},{"type":"integer","minimum":5}]}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"tup":["a",1]}`, Want: `tup: items[1]: 1 is less than minimum 5`,
					Reason: "control: a tuple position reports under the keyword that gave it one, and keeps doing so"},
				{Name: "accepts", Doc: `{"tup":["a",9]}`, Reason: "control"},
			},
		},
		{
			Name:   "control_required_nested",
			Schema: `{"type":"object","properties":{"shape":{"type":"object","properties":{"r":{"type":"string"}},"required":["r"]}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"shape":{}}`, Want: `shape.r: required property is missing`, Reason: "control"},
				{Name: "accepts", Doc: `{"shape":{"r":"x"}}`, Reason: "control"},
			},
		},
		{
			Name:   "control_required_two_deep",
			Schema: `{"type":"object","properties":{"a":{"type":"object","properties":{"b":{"type":"object","properties":{"r":{"type":"string"}},"required":["r"]}}}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"a":{"b":{}}}`, Want: `a.b.r: required property is missing`, Reason: "control"},
				{Name: "accepts", Doc: `{"a":{"b":{"r":"x"}}}`, Reason: "control"},
			},
		},
		{
			Name:   "control_keyword_segments",
			Schema: `{"type":"object","properties":{"n":{"not":{"type":"string"}},"pn":{"type":"object","propertyNames":{"pattern":"^z"}},"ds":{"type":"object","dependentSchemas":{"x":{"required":["y"]}}},"arr":{"type":"array","contains":{"type":"integer","minimum":5}}}}`,
			Cases: []errorPathCase{
				{Name: "not", Doc: `{"n":"s"}`, Want: `n.not: value must not be string`,
					Reason: "control: a schema keyword is a location a caller can follow, and stays joined with a dot"},
				{Name: "propertyNames", Doc: `{"pn":{"a":1}}`,
					Want: `pn.propertyNames: property name "a" does not match pattern "^z"`, Reason: "control"},
				{Name: "dependentSchemas", Doc: `{"ds":{"x":1}}`,
					Want: `ds.dependentSchema "x": property "y" is required`, Reason: "control"},
				{Name: "contains", Doc: `{"arr":[1,2]}`,
					Want: `arr: contains: no element matches the contains schema`, Reason: "control"},
				{Name: "all legal", Doc: `{"n":5,"pn":{"za":1},"ds":{"x":1,"y":2},"arr":[9]}`, Reason: "control"},
			},
		},
		{
			// The accessor test that decides how a message is joined is written
			// against the format verb rather than the bracket, so that a document
			// whose own property is spelled like an accessor cannot meet it. The
			// nested case is the one that can tell the two apart: at the root
			// nothing joins the message, so both spellings print the same thing.
			Name:   "control_property_named_like_an_accessor",
			Schema: `{"type":"object","properties":{"outer":{"type":"object","properties":{"[x]":{"type":"array","items":{"type":"string","minLength":3}}}},"[y]":{"type":"array","items":{"type":"string","minLength":3}}}}`,
			Cases: []errorPathCase{
				{Name: "nested", Doc: `{"outer":{"[x]":["b"]}}`,
					Want:   `outer.[x][0]: length 1 is less than minimum 3`,
					Reason: "control: a property really named [x] is a name, joined with a dot, and is not an accessor"},
				{Name: "at the root", Doc: `{"[y]":["b"]}`, Want: `[y][0]: length 1 is less than minimum 3`,
					Reason: "control"},
				{Name: "accepts", Doc: `{"outer":{"[x]":["bbb"]},"[y]":["bbb"]}`, Reason: "control"},
			},
		},
		{
			// An alias whose whole definition is another named type delegates to
			// that type's Validate. The two answer for the same value, so there is
			// no step of path between them -- and the delegated message may be of
			// any of the three kinds, so re-marking it as prose is wrong for two
			// of them.
			Name: "delegating_alias_keeps_the_message_it_is_given",
			Schema: `{"type":"object","properties":{"w":{"$ref":"#/$defs/wrap"},"v":{"$ref":"#/$defs/wrapList"}},
			  "$defs":{"wrap":{"$ref":"#/$defs/target"},
			           "target":{"type":"object","properties":{"r":{"type":"string"}},"required":["r"],"additionalProperties":false},
			           "wrapList":{"$ref":"#/$defs/list"},
			           "list":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string","minLength":3}}}}}}`,
			Cases: []errorPathCase{
				{Name: "delegated message names a member", Doc: `{"w":{}}`,
					Want:   `w.r: required property is missing`,
					Reason: "was w.value: r: ...; a delegation is not a step of path, and the member name still takes a dot"},
				{Name: "delegated message opens with an accessor", Doc: `{"v":[{"name":"aaa"},{"name":"b"}]}`,
					Want:   `v[1].name: length 1 is less than minimum 3`,
					Reason: "was w.value: items[1].name; the accessor is glued on with nothing between"},
				{Name: "accepts", Doc: `{"w":{"r":"x"},"v":[{"name":"aaa"}]}`, Reason: "control"},
			},
		},
		{
			// A percent in a property name survives being carried through a
			// format string on its way into the path.
			Name:   "control_property_name_with_a_percent",
			Schema: `{"type":"object","properties":{"a%d":{"$ref":"#/$defs/T"}},"$defs":{"T":{"type":"string","minLength":3}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"a%d":"b"}`, Want: `a%d: length 1 is less than minimum 3`,
					Reason: "control: the name reaches the message intact"},
				{Name: "accepts", Doc: `{"a%d":"bbb"}`, Reason: "control"},
			},
		},
		{
			// A root scalar has no path at all, and says so by saying nothing
			// rather than by inventing a member.
			Name:   "root_scalar_has_no_path",
			Schema: `{"type":"string","minLength":3}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `"ab"`, Want: `length 2 is less than minimum 3`,
					Reason: "was value: length 2 ...; there is no member of a string"},
				{Name: "accepts", Doc: `"abc"`, Reason: "control"},
			},
		},
		{
			Name:   "root_ref_to_scalar_has_no_path",
			Schema: `{"$ref":"#/$defs/T","$defs":{"T":{"type":"string","minLength":3}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `"ab"`, Want: `length 2 is less than minimum 3`,
					Reason: "was value: value: length 2 ... -- the placeholder twice over, once per delegation"},
				{Name: "accepts", Doc: `"abc"`, Reason: "control"},
			},
		},
	}
}

// runErrorPathFixtures generates each schema, compiles it, and puts every
// document to the compiled type, comparing the whole message.
func runErrorPathFixtures(t *testing.T, module string, fixtures []errorPathFixture) {
	t.Helper()
	em, err := emitter.New()
	if err != nil {
		t.Fatalf("creating emitter: %v", err)
	}
	for _, fx := range fixtures {
		t.Run(fx.Name, func(t *testing.T) {
			if !json.Valid([]byte(fx.Schema)) {
				t.Fatalf("fixture schema is not valid JSON:\n%s", fx.Schema)
			}
			tmpDir := t.TempDir()
			schemaPath := filepath.Join(tmpDir, "schema.json")
			if err := os.WriteFile(schemaPath, []byte(fx.Schema), 0o644); err != nil {
				t.Fatalf("writing schema: %v", err)
			}
			s, err := schema.LoadFromFile(schemaPath)
			if err != nil {
				t.Fatalf("loading schema: %v", err)
			}
			s.Normalize()
			ir, err := generator.New(generator.Config{
				PackageName:  "testpkg",
				OmitEmpty:    true,
				RootTypeName: "Root",
			}).Generate(s)
			if err != nil {
				t.Fatalf("generating IR: %v", err)
			}
			src, err := em.Emit(ir)
			if err != nil {
				t.Fatalf("emitting: %v", err)
			}

			generatedMain := strings.Replace(string(src), "package testpkg", "package main", 1)
			if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
				t.Fatalf("writing types.go: %v", err)
			}
			writeSharedHelpers(t, tmpDir, generatedMain)
			mainGo, err := errorPathMain(fx.Cases)
			if err != nil {
				t.Fatalf("building main.go: %v", err)
			}
			if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
				t.Fatalf("writing main.go: %v", err)
			}
			if err := writeTestGoMod(tmpDir, module); err != nil {
				t.Fatalf("writing go.mod: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "run", ".")
			cmd.Dir = tmpDir
			out, runErr := cmd.CombinedOutput()
			text := programOutput(out)
			if runErr != nil || text != "PASS" {
				t.Fatalf("%s:\n%s", fx.Name, text)
			}
		})
	}
}

// errorPathMain writes the program that puts each document to the type and
// compares the whole of the message.
func errorPathMain(cases []errorPathCase) (string, error) {
	var b strings.Builder
	b.WriteString(`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type errorPathCase struct {
	name   string
	doc    string
	want   string
	reason string
}

func main() {
	cases := []errorPathCase{
`)
	for _, c := range cases {
		if !json.Valid([]byte(c.Doc)) {
			return "", fmt.Errorf("case %q: %s is not valid JSON", c.Name, c.Doc)
		}
		fmt.Fprintf(&b, "\t\t{name: %s, doc: %s, want: %s, reason: %s},\n",
			goQuote(c.Name), goQuote(c.Doc), goQuote(c.Want), goQuote(c.Reason))
	}
	b.WriteString(`	}

	var errs []string
	for _, c := range cases {
		var v Root
		err := json.Unmarshal([]byte(c.doc), &v)
		if err == nil {
			if val, ok := any(v).(interface{ Validate() error }); ok {
				err = val.Validate()
			}
		}
		got := ""
		if err != nil {
			got = err.Error()
		}
		// Equality, not containment: a wrong path is a right path with an extra
		// segment glued to it, so a substring match passes under the defect.
		if got != c.want {
			errs = append(errs, fmt.Sprintf("%s: %s\n     got:  %q\n     want: %q\n     (%s)",
				c.name, c.doc, got, c.want, c.reason))
		}
	}
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "FAIL: %s\n", e)
		}
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`)
	return b.String(), nil
}
