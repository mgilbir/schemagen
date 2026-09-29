package roundtrip

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// The four configurations that change what a default lands in: the plain
// types, --big-int's integer wrapper, --exact-numbers' json.Number, and
// --raw-untyped's json.RawMessage for an untyped position.
var defaultPolicyConfigs = []struct {
	name string
	cfg  func(*generator.Config)
}{
	{"default", func(*generator.Config) {}},
	{"big-int", func(c *generator.Config) { c.BigIntSupport = true }},
	{"exact-numbers", func(c *generator.Config) { c.ExactNumbers = true }},
	{"raw-untyped", func(c *generator.Config) { c.RawUntyped = true }},
	// And --strict-read-write, whose decoder refuses a readOnly member: a
	// default is the authority's own value, not a client's, so a decoded one
	// is decoded past that refusal.
	{"strict-read-write", func(c *generator.Config) { c.StrictReadWrite = true }},
}

// skip marks a default SetDefaults must not plant, and the generator must
// report. The empty string marks one it must not plant and need not report: a
// null default, which leaves the field as a null in the document does.
const skip = "SKIP"

// defaultPolicyCase is one property with a default, and what each
// configuration does with it: the JSON SetDefaults writes, skip, or "".
type defaultPolicyCase struct {
	name   string
	schema string
	want   map[string]string
}

func everywhere(v string) map[string]string {
	return map[string]string{"default": v, "big-int": v, "exact-numbers": v, "raw-untyped": v, "strict-read-write": v}
}

func except(v string, config, there string) map[string]string {
	m := everywhere(v)
	m[config] = there
	return m
}

// defaultPolicyCases is the policy, value by value: SetDefaults plants a
// default exactly when the field's Go type holds it -- writes it back out as
// the same JSON -- and it is valid against the schemas that describe the
// property on every document. Every other default is skipped and reported,
// and none fails generation.
var defaultPolicyCases = []defaultPolicyCase{
	{"intOk", `{"type":"integer","default":7}`, everywhere("7")},
	{"intFloatSpelling", `{"type":"integer","default":4.0}`, everywhere("4")},
	// The audit's three: a fraction, a string and an array on an integer --
	// the first used to refuse generation and the other two to vanish.
	{"intFraction", `{"type":"integer","default":4.5}`, everywhere(skip)},
	{"intString", `{"type":"integer","default":"x"}`, everywhere(skip)},
	{"intArray", `{"type":"integer","default":[]}`, everywhere(skip)},
	// A valid integer no int64 holds: refused generation; --big-int holds it.
	{"intHuge", `{"type":"integer","default":1e30}`, except(skip, "big-int", "1000000000000000000000000000000")},
	{"intBelowMinimum", `{"type":"integer","minimum":10,"default":5}`, everywhere(skip)},
	{"numOk", `{"type":"number","default":1.25}`, everywhere("1.25")},
	// A valid number no float64 holds: refused generation; --exact-numbers
	// holds it.
	{"numHuge", `{"type":"number","default":1e400}`, except(skip, "exact-numbers", "1e400")},
	{"numPrecise", `{"type":"number","default":1.2345678901234567890}`, except(skip, "exact-numbers", "1.2345678901234567890")},
	{"numNotMultiple", `{"type":"number","multipleOf":0.5,"default":0.75}`, everywhere(skip)},
	{"strOk", `{"type":"string","default":"s"}`, everywhere(`"s"`)},
	{"strNumber", `{"type":"string","default":5}`, everywhere(skip)},
	{"strPattern", `{"type":"string","pattern":"^a","default":"bc"}`, everywhere(skip)},
	{"strTooLong", `{"type":"string","maxLength":2,"default":"abc"}`, everywhere(skip)},
	// The audit's fourth: planted, and made {} invalid.
	{"enumOut", `{"type":"string","enum":["a","b"],"default":"zzz"}`, everywhere(skip)},
	{"enumIn", `{"type":"string","enum":["a","b"],"default":"b"}`, everywhere(`"b"`)},
	{"constOut", `{"type":"string","const":"k","default":"j"}`, everywhere(skip)},
	{"boolOk", `{"type":"boolean","default":true}`, everywhere("true")},
	{"boolString", `{"type":"boolean","default":"true"}`, everywhere(skip)},
	{"nullableNull", `{"type":["string","null"],"default":null}`, everywhere("")},
	{"nullableStr", `{"type":["string","null"],"default":"n"}`, everywhere(`"n"`)},
	{"notViolated", `{"type":"string","not":{"enum":["x"]},"default":"x"}`, everywhere(skip)},
	{"anyOfNone", `{"anyOf":[{"type":"string","maxLength":1},{"type":"integer"}],"default":"long"}`, everywhere(skip)},
	{"arrOk", `{"type":"array","items":{"type":"integer"},"default":[1,2]}`, everywhere("[1,2]")},
	{"arrBadItem", `{"type":"array","items":{"type":"integer"},"default":[1,"x"]}`, everywhere(skip)},
	{"arrTooShort", `{"type":"array","items":{"type":"integer"},"minItems":3,"default":[1]}`, everywhere(skip)},
	{"arrNotUnique", `{"type":"array","uniqueItems":true,"items":{"type":"integer"},"default":[1,1]}`, everywhere(skip)},
	{"arrTupleBad", `{"type":"array","prefixItems":[{"type":"string"}],"default":[5]}`, everywhere(skip)},
	{"mapOk", `{"type":"object","additionalProperties":{"type":"integer"},"default":{"x":1}}`, everywhere(`{"x":1}`)},
	{"mapBad", `{"type":"object","additionalProperties":{"type":"integer"},"default":{"x":"y"}}`, everywhere(skip)},
	{"mapHuge", `{"type":"object","additionalProperties":{"type":"integer"},"default":{"x":1e30}}`,
		except(skip, "big-int", `{"x":1000000000000000000000000000000}`)},
	// A value no literal spells is planted by decoding it, which leaves the
	// state a document carrying it would -- the struct's own key set and
	// overflow members included.
	{"objStruct", `{"type":"object","properties":{"n":{"type":"string"}},"default":{"n":"x"}}`, everywhere(`{"n":"x"}`)},
	{"objStructBad", `{"type":"object","properties":{"n":{"type":"string"}},"default":{"n":5}}`, everywhere(skip)},
	{"objNested", `{"type":"object","properties":{"inner":{"type":"object","properties":{"v":{"type":"integer"}},"required":["v"]},"s":{"type":"string"}},"default":{"inner":{"v":2},"s":"a"}}`,
		everywhere(`{"inner":{"v":2},"s":"a"}`)},
	{"objNestedBad", `{"type":"object","properties":{"inner":{"type":"object","properties":{"v":{"type":"integer"}},"required":["v"]}},"default":{"inner":{}}}`, everywhere(skip)},
	{"objOverflow", `{"type":"object","properties":{"n":{"type":"string"}},"default":{"n":"x","extra":[1,{"z":true}]}}`,
		everywhere(`{"n":"x","extra":[1,{"z":true}]}`)},
	// The decode holds a nested number exactly where the field does.
	{"objPreciseMember", `{"type":"object","properties":{"f":{"type":"number"}},"default":{"f":1.2345678901234567890}}`,
		except(skip, "exact-numbers", `{"f":1.2345678901234567890}`)},
	{"arrOfStruct", `{"type":"array","items":{"type":"object","properties":{"a":{"type":"integer"}},"required":["a"]},"default":[{"a":1},{"a":2}]}`,
		everywhere(`[{"a":1},{"a":2}]`)},
	{"arrOfStructBad", `{"type":"array","items":{"type":"object","properties":{"a":{"type":"integer"}},"required":["a"]},"default":[{"a":1},{}]}`, everywhere(skip)},
	{"mapOfStruct", `{"type":"object","additionalProperties":{"type":"object","properties":{"a":{"type":"integer"}}},"default":{"k":{"a":1}}}`,
		everywhere(`{"k":{"a":1}}`)},
	// A union: which variant the default selects is the decode's decision,
	// and a default matching no branch, or two, is not valid there.
	{"union", unionSchema(`{"b":3}`), everywhere(`{"b":3}`)},
	{"unionNone", unionSchema(`{"c":1}`), everywhere(skip)},
	{"unionBoth", unionSchema(`{"a":"x","b":3}`), everywhere(skip)},
	{"objReadOnlyMember", `{"type":"object","properties":{"id":{"type":"string","readOnly":true}},"default":{"id":"srv"}}`, everywhere(`{"id":"srv"}`)},
	// Required, so the field is bare and asked whether it is still its zero.
	{"reqObj", `{"type":"object","properties":{"k":{"type":"string"}},"default":{"k":"v"}}`, everywhere(`{"k":"v"}`)},
	// An untyped position holds any JSON: as encoding/json decodes it, or,
	// under --raw-untyped, as its bytes.
	{"untyped", `{"default":{"a":[1,2.5,"s",null,true]}}`, everywhere(`{"a":[1,2.5,"s",null,true]}`)},
	{"untypedHugeNumber", `{"default":12345678901234567890}`, except(skip, "raw-untyped", "12345678901234567890")},
	{"viaRefOk", `{"$ref":"#/$defs/Lim","default":3}`, everywhere("3")},
	{"viaRefBad", `{"$ref":"#/$defs/Lim","default":9}`, everywhere(skip)},
	{"defaultBehindRef", `{"$ref":"#/$defs/LimBadDefault"}`, everywhere(skip)},
	{"viaAllOfBad", `{"allOf":[{"type":"integer","maximum":5}],"default":9}`, everywhere(skip)},
	{"required", `{"type":"integer","default":3}`, everywhere("3")},
}

// unionSchema is a oneOf the generator builds as a group of two object
// variants, with the given default.
func unionSchema(dflt string) string {
	return `{"oneOf":[{"type":"object","required":["a"],"properties":{"a":{"type":"string"}}},` +
		`{"type":"object","required":["b"],"properties":{"b":{"type":"integer"}}}],"default":` + dflt + `}`
}

const defaultPolicyDefs = `"$defs":{` +
	`"Lim":{"type":"integer","maximum":5},` +
	`"LimBadDefault":{"type":"integer","maximum":5,"default":9}}`

func defaultPolicySchema() string {
	props := make([]string, len(defaultPolicyCases))
	for i, c := range defaultPolicyCases {
		props[i] = strconv.Quote(c.name) + ":" + c.schema
	}
	return `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["required","reqObj"],"properties":{` +
		strings.Join(props, ",") + `},` + defaultPolicyDefs + `}`
}

// defaultPolicyMain decodes {} (and, for a required property, the least
// document that has it), runs SetDefaults, and holds what it writes to want:
// each planted property is the JSON the case names, compared by value, and
// nothing else is written. What it writes is then decoded afresh and must be
// valid -- which runs every check on every planted value, not only the ones a
// defaulted field's Validate reads.
func defaultPolicyMain(want map[string]string, start string, strictReadWrite bool) string {
	table, err := json.Marshal(want)
	if err != nil {
		panic(err)
	}
	return `package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"

	"enginetest/gen"
)

// sameJSON is JSON equality with numbers compared by value, exactly.
func sameJSON(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		rx, okx := new(big.Rat).SetString(string(x))
		ry, oky := new(big.Rat).SetString(string(y))
		return okx && oky && rx.Cmp(ry) == 0
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !sameJSON(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k := range x {
			if !sameJSON(x[k], y[k]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

func decode(text []byte) any {
	d := json.NewDecoder(bytes.NewReader(text))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		panic(fmt.Sprintf("%s: %v", text, err))
	}
	return v
}

func main() {
	var want map[string]string
	if err := json.Unmarshal([]byte(` + strconv.Quote(string(table)) + `), &want); err != nil {
		panic(err)
	}
	var v gen.Root
	if err := json.Unmarshal([]byte(` + strconv.Quote(start) + `), &v); err != nil {
		fmt.Println("decoding the start document:", err)
		return
	}
	v.SetDefaults()
	out, err := json.Marshal(&v)
	if err != nil {
		fmt.Println("marshal:", err)
		return
	}
	got := decode(out).(map[string]any)
	failed := false
	start := decode([]byte(` + strconv.Quote(start) + `)).(map[string]any)
	for name, w := range want {
		g, present := got[name]
		_, carried := start[name]
		switch {
		case carried:
		case w == "" || w == "SKIP":
			if present {
				fmt.Printf("%s: SetDefaults wrote %v, want nothing\n", name, g)
				failed = true
			}
		case !present:
			fmt.Printf("%s: SetDefaults wrote nothing, want %s\n", name, w)
			failed = true
		case !sameJSON(g, decode([]byte(w))):
			fmt.Printf("%s: SetDefaults wrote %v, want %s\n", name, g, w)
			failed = true
		}
	}
	// A bare field planted by decoding is asked whether it is still its zero,
	// beside the key set: a value built in Go has no key set, and what its
	// caller assigned must survive SetDefaults.
	if field := reflect.ValueOf(&v).Elem().FieldByName("ReqObj"); field.IsValid() {
		var src gen.Root
		if err := json.Unmarshal([]byte(` + "`" + `{"reqObj":{"k":"mine"}}` + "`" + `), &src); err != nil {
			panic(err)
		}
		var built gen.Root
		reflect.ValueOf(&built).Elem().FieldByName("ReqObj").Set(reflect.ValueOf(&src).Elem().FieldByName("ReqObj"))
		built.SetDefaults()
		kept, err := json.Marshal(reflect.ValueOf(&built).Elem().FieldByName("ReqObj").Interface())
		if err != nil || !sameJSON(decode(kept), decode([]byte(` + "`" + `{"k":"mine"}` + "`" + `))) {
			fmt.Printf("reqObj: SetDefaults overwrote a value built in Go: %s %v\n", kept, err)
			failed = true
		}
	}
	var again gen.Root
	// Under --strict-read-write the decoder refuses what a client may not set
	// -- a readOnly member the default planted -- and goes on decoding, so
	// the refusal is not a verdict on the value.
	if err := json.Unmarshal(out, &again); err != nil && !(` + strconv.FormatBool(strictReadWrite) + ` && strings.Contains(err.Error(), "read-only property may not be set")) {
		fmt.Printf("what SetDefaults wrote does not decode: %s: %v\n", out, err)
		failed = true
	} else if err := again.Validate(); err != nil {
		fmt.Printf("what SetDefaults wrote is not valid: %s: %v\n", out, err)
		failed = true
	}
	if !failed {
		fmt.Println("PASS")
	}
}
`
}

// skippedDefaultsOf generates schemaJSON under cfg and returns the properties
// the generator reported a default for, with each report.
func skippedDefaultsOf(t *testing.T, schemaJSON string, cfg generator.Config) map[string]generator.SkippedDefault {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(schemaJSON), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	cfg.PackageName = "gen"
	g := generator.New(cfg)
	if _, err := g.Generate(&s, generator.WithRootTypeName("Root")); err != nil {
		t.Fatalf("generation failed; no default may fail it: %v", err)
	}
	out := map[string]generator.SkippedDefault{}
	for _, sd := range g.SkippedDefaults() {
		out[sd.Property] = sd
	}
	return out
}

// checkSkipped holds the generator's reports to want: exactly the skip cases,
// each located at a default the document wrote.
func checkSkipped(t *testing.T, got map[string]generator.SkippedDefault, want map[string]string) {
	t.Helper()
	var names []string
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sd, reported := got[name]
		switch {
		case want[name] == skip && !reported:
			t.Errorf("%s: skipped without a report", name)
		case want[name] != skip && reported:
			t.Errorf("%s: reported as skipped (%s), want %q", name, sd.Reason, want[name])
		case reported && !strings.HasSuffix(sd.Location, "/default"):
			t.Errorf("%s: the report is not located at a default: %+v", name, sd)
		}
	}
}

// TestDefaultPolicyAcrossConfigurations is the default policy as a matrix:
// every case above under each configuration that changes what a default lands
// in, generated, reported and run.
func TestDefaultPolicyAcrossConfigurations(t *testing.T) {
	schemaJSON := defaultPolicySchema()
	for _, c := range defaultPolicyConfigs {
		t.Run(c.name, func(t *testing.T) {
			cfg := generator.DefaultConfig()
			c.cfg(&cfg)
			want := map[string]string{}
			for _, dc := range defaultPolicyCases {
				want[dc.name] = dc.want[c.name]
			}
			checkSkipped(t, skippedDefaultsOf(t, schemaJSON, cfg), want)
			// {} does not carry "required", which is required; SetDefaults
			// plants it, so what it writes is valid where {} was not.
			if out := runEngineProgram(t, schemaJSON, cfg, defaultPolicyMain(want, `{}`, cfg.StrictReadWrite)); out != "PASS" {
				t.Fatalf("SetDefaults disagreed with the policy:\n%s", out)
			}
		})
	}
}

// TestDefaultPolicyJudgesTheWholeLocation is the part of the judgement a
// property's own schema does not show: the patternProperties that govern its
// key, the additionalProperties of an allOf branch that does not name it, and
// the propertyNames its key has to satisfy. Each applies to the property on
// every document, so a default they refuse is refused.
func TestDefaultPolicyJudgesTheWholeLocation(t *testing.T) {
	schemaJSON := `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object",` +
		`"propertyNames":{"maxLength":5},` +
		`"patternProperties":{"^pg":{"type":"integer"}},` +
		`"allOf":[{"properties":{"pgStr":true,"pgInt":true,"pnTooLong":true},"additionalProperties":{"type":"integer"}}],` +
		`"properties":{` +
		`"pgStr":{"default":"x"},"pgInt":{"default":3},` +
		`"agStr":{"default":"x"},"agInt":{"default":4},` +
		`"pnTooLong":{"default":1}}}`
	want := map[string]string{"pgStr": skip, "pgInt": "3", "agStr": skip, "agInt": "4", "pnTooLong": skip}
	cfg := generator.DefaultConfig()
	checkSkipped(t, skippedDefaultsOf(t, schemaJSON, cfg), want)
	if out := runEngineProgram(t, schemaJSON, cfg, defaultPolicyMain(want, `{}`, false)); out != "PASS" {
		t.Fatalf("SetDefaults disagreed with the policy:\n%s", out)
	}
}

// TestDefaultPolicyDefersWhatGenerationCannotDecide is the runtime half. A
// format the generated code asserts is judged by a helper the generator does
// not run, so a default under one is compiled for the runtime evaluator and
// planted only if it accepts: "a@b.co" is, "nope" is not. "either" is the
// control that the deferral is not wholesale: its second anyOf branch accepts
// "zz" outright, so it is decided at generation time and planted as it stands.
func TestDefaultPolicyDefersWhatGenerationCannotDecide(t *testing.T) {
	schemaJSON := `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{` +
		`"good":{"type":"string","format":"email","default":"a@b.co"},` +
		`"bad":{"type":"string","format":"email","default":"nope"},` +
		`"either":{"anyOf":[{"type":"string","format":"email"},{"type":"string","maxLength":2}],"default":"zz"},` +
		// An asserted date-time or ip is held as a time.Time or a netip.Addr,
		// planted by decoding -- and only where it writes the same bytes
		// back: both types respell what they are given.
		`"when":{"type":"string","format":"date-time","default":"2020-01-02T03:04:05Z"},` +
		`"whenRespelled":{"type":"string","format":"date-time","default":"2020-01-02T03:04:05.000Z"},` +
		`"addr":{"type":"string","format":"ipv6","default":"::1"},` +
		`"addrRespelled":{"type":"string","format":"ipv6","default":"::0001"}}}`
	cfg := generator.DefaultConfig()
	cfg.FormatAssertion = true
	want := map[string]string{
		"good": `"a@b.co"`, "bad": "", "either": `"zz"`,
		"when": `"2020-01-02T03:04:05Z"`, "whenRespelled": skip,
		"addr": `"::1"`, "addrRespelled": skip,
	}
	// The two deferred defaults are decided when SetDefaults runs, and are
	// not reported; the respelled ones are.
	checkSkipped(t, skippedDefaultsOf(t, schemaJSON, cfg), want)
	src := string(generateInlineSource(t, schemaJSON, cfg))
	for _, field := range []string{"Good", "Bad"} {
		if !strings.Contains(src, "rt.EvalNode(&_djRoot"+field+",") {
			t.Errorf("the default of %s is not deferred to the runtime evaluator:\n%s", field, src)
		}
	}
	if strings.Contains(src, "rt.EvalNode(&_djRootEither") {
		t.Errorf("the default of Either was deferred though generation decided it")
	}
	if out := runEngineProgram(t, schemaJSON, cfg, defaultPolicyMain(want, `{}`, false)); out != "PASS" {
		t.Fatalf("SetDefaults disagreed with the policy:\n%s", out)
	}
}

// generateInlineSource generates schemaJSON under cfg, with Root as its root
// type, and returns the source.
func generateInlineSource(t *testing.T, schemaJSON string, cfg generator.Config) []byte {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(schemaJSON), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	cfg.PackageName = "gen"
	ir, err := generator.New(cfg).Generate(&s, generator.WithRootTypeName("Root"))
	if err != nil {
		t.Fatal(err)
	}
	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}
	src, err := em.Emit(ir)
	if err != nil {
		t.Fatal(err)
	}
	return src
}
