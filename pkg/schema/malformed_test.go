package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// allDrafts is every dialect a document can be read under, DraftUnknown (no
// recognised $schema: the union of them all) included.
var allDrafts = []Draft{DraftUnknown, Draft03, Draft04, Draft06, Draft07, Draft201909, Draft202012, DraftV1}

// dialectURI is the $schema each draft is declared with.
var dialectURI = map[Draft]string{
	Draft03:     "http://json-schema.org/draft-03/schema#",
	Draft04:     "http://json-schema.org/draft-04/schema#",
	Draft06:     "http://json-schema.org/draft-06/schema#",
	Draft07:     "http://json-schema.org/draft-07/schema#",
	Draft201909: "https://json-schema.org/draft/2019-09/schema",
	Draft202012: "https://json-schema.org/draft/2020-12/schema",
	DraftV1:     "https://json-schema.org/v1",
}

// documentIn returns a schema object stating members under dialect d, as
// JSON text: members is the object's body without braces.
func documentIn(d Draft, members string) string {
	if d == DraftUnknown {
		return "{" + members + "}"
	}
	return `{"$schema":"` + dialectURI[d] + `",` + members + "}"
}

// malformedValueFor returns a value no dialect admits for the keyword a field
// of Schema carries, and whether there is one. The value is chosen from the
// field's type, so a keyword added later is covered the day its field is.
func malformedValueFor(f reflect.StructField) (string, bool) {
	t := f.Type
	switch {
	case holdsInstanceData(t):
		return "", false // const, enum's members, default: every value is legal
	case holdsSchemas(t):
		return `5`, true // a number is a schema in no draft
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case reflect.TypeOf(json.RawMessage{}): // extends, disallow, dependencies
		return `5`, true
	case reflect.TypeOf(TypeList{}), reflect.TypeOf(Discriminator{}):
		return `5`, true
	case reflect.TypeOf(Number("")):
		return `"5"`, true // a string underneath, and refusing a JSON string is why it is its own type
	}
	if t.Kind() == reflect.String {
		return `5`, true
	}
	return `"not this keyword's type"`, true
}

// TestAMalformedKeywordIsRefusedExactlyWhereItsDialectDefinesIt is the policy
// for a keyword whose value is not a legal value of it, over every keyword
// Schema has a field for and every dialect.
//
// A dialect that defines the keyword says the document is not a schema, and
// the value is reported; one that does not says the keyword is unknown and to
// be ignored, and it is -- whatever the value. Before, which of the two a
// malformed value got depended on the Go type of its field rather than on the
// spec: a typed field refused the document in every dialect ({"divisibleBy":"x"}
// in 2020-12, which has no divisibleBy), a raw one accepted it in every dialect
// ({"dependencies":{"a":null}}), and a lenient decoder read a meaning into it
// ({"type":[1,2]} as no type at all).
func TestAMalformedKeywordIsRefusedExactlyWhereItsDialectDefinesIt(t *testing.T) {
	typ := reflect.TypeOf(Schema{})
	covered := 0
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		keyword := jsonTagName(f)
		if keyword == "" || keyword == "$schema" {
			continue // $schema chooses the dialect this test varies
		}
		bad, ok := malformedValueFor(f)
		if !ok {
			continue
		}
		covered++
		for _, d := range allDrafts {
			doc := documentIn(d, jsonQuote(keyword)+":"+bad)
			var s Schema
			if err := json.Unmarshal([]byte(doc), &s); err != nil {
				t.Fatalf("%s: a schema object with a malformed keyword is still a schema object: %v", doc, err)
			}
			if !reflect.ValueOf(s).Field(i).IsZero() {
				t.Errorf("%s: the malformed value was read into %s", doc, f.Name)
			}
			s.Normalize()
			reported := false
			for _, ke := range s.MalformedKeywords() {
				if ke.Keyword == keyword {
					reported = true
				}
			}
			if want := KeywordDefinedIn(keyword, d); reported != want {
				t.Errorf("%s under %v: reported=%v, want %v (the dialect defines %q: %v)",
					doc, d, reported, want, keyword, want)
			}
		}
	}
	if covered < 40 {
		t.Fatalf("only %d keywords were given a malformed value; the field walk has stopped reaching them", covered)
	}
}

// TestAMalformedValueIsRefusedWhateverItsShape covers the spellings of a
// malformed value the type-driven matrix above does not generate: nulls, at
// the top and inside, and the entries of the keywords that hold a list.
func TestAMalformedValueIsRefusedWhateverItsShape(t *testing.T) {
	for _, tc := range []struct {
		doc, keyword string
	}{
		// Null where a schema is required. Every one of these used to read as
		// the keyword's absence, beside {"allOf":[null]}, which was refused.
		{`{"not":null}`, "not"},
		{`{"items":null}`, "items"},
		{`{"additionalProperties":null}`, "additionalProperties"},
		{`{"properties":null}`, "properties"},
		{`{"extends":null}`, "extends"},
		{`{"extends":[{},null]}`, "extends"},
		{`{"dependencies":{"a":null}}`, "dependencies"},
		// Null where a value of another type is required.
		{`{"minLength":null}`, "minLength"},
		{`{"type":null}`, "type"},
		{`{"title":null}`, "title"},
		{`{"$ref":null}`, "$ref"},
		// Null inside a value that holds neither schemas nor instance data.
		// encoding/json decodes each of these into a string as "".
		{`{"required":["a",null]}`, "required"},
		{`{"type":["string",null]}`, "type"},
		{`{"dependentRequired":{"a":["b",null]}}`, "dependentRequired"},
		{`{"dependencies":{"a":["b",null]}}`, "dependencies"},
		{`{"$vocabulary":{"https://example.test/v":null}}`, "$vocabulary"},
		{`{"discriminator":{"propertyName":"k","mapping":{"a":null}}}`, "discriminator"},
		// Entries no dialect gives a meaning.
		{`{"type":[1,2]}`, "type"},
		{`{"type":["string",true]}`, "type"},
		{`{"disallow":[null]}`, "disallow"},
		{`{"disallow":["string",7]}`, "disallow"},
		{`{"disallow":[true]}`, "disallow"},
		{`{"extends":true}`, "extends"},
		{`{"dependencies":{"a":5}}`, "dependencies"},
		{`{"dependencies":{"a":[1]}}`, "dependencies"},
		{`{"dependencies":5}`, "dependencies"},
		// Counts.
		{`{"minLength":-1}`, "minLength"},
		{`{"maxItems":-1}`, "maxItems"},
		{`{"minContains":1.5}`, "minContains"},
		{`{"maxProperties":"1"}`, "maxProperties"},
		// An identifier that is not a URI-reference.
		{`{"$id":"http://[::1"}`, "$id"},
		{`{"id":"%zz"}`, "id"},
	} {
		var s Schema
		if err := json.Unmarshal([]byte(tc.doc), &s); err != nil {
			t.Fatalf("%s: %v", tc.doc, err)
		}
		s.Normalize()
		bad := s.MalformedKeywords()
		if len(bad) != 1 || bad[0].Keyword != tc.keyword {
			t.Errorf("%s: malformed %v, want exactly %q", tc.doc, bad, tc.keyword)
		}
	}

	// And the controls: the legal spellings nearest to each of the above.
	for _, doc := range []string{
		`{"const":null}`, `{"default":null}`, `{"enum":[null,1]}`,
		`{"type":["string",{"minimum":1}]}`, `{"disallow":"string"}`, `{"disallow":["string",{}]}`,
		`{"extends":{}}`, `{"extends":[{}]}`, `{"dependencies":{"a":"b","c":["d"],"e":{},"f":true}}`,
		`{"required":[]}`, `{"minLength":0}`, `{"minLength":-0}`, `{"minLength":2.0}`, `{"minLength":1e1}`,
		`{"$id":"https://example.test/x"}`, `{"id":"#foo"}`, `{"examples":[null]}`, `{"x-vendor":null}`,
	} {
		var s Schema
		if err := json.Unmarshal([]byte(doc), &s); err != nil {
			t.Fatalf("%s: %v", doc, err)
		}
		s.Normalize()
		if bad := s.MalformedKeywords(); len(bad) > 0 {
			t.Errorf("%s: a legal document reported malformed %v", doc, bad)
		}
	}
}

// TestANegativeMaxLengthIsLegalOnlyInDraft3 holds the one dialect-dependent
// count: draft 3's meta-schema gives maxLength a plain "integer", and every
// other count, in every dialect, a non-negative one.
func TestANegativeMaxLengthIsLegalOnlyInDraft3(t *testing.T) {
	for _, d := range allDrafts {
		for _, keyword := range []string{"maxLength", "minLength", "maxItems", "minItems", "maxProperties", "minProperties", "maxContains", "minContains"} {
			var s Schema
			doc := documentIn(d, jsonQuote(keyword)+":-3")
			if err := json.Unmarshal([]byte(doc), &s); err != nil {
				t.Fatalf("%s: %v", doc, err)
			}
			s.Normalize()
			legal := keyword == "maxLength" && (d == Draft03 || d == DraftUnknown)
			reported := len(s.MalformedKeywords()) > 0
			if !KeywordDefinedIn(keyword, d) {
				if reported {
					t.Errorf("%s: reported under %v, which does not define %s", doc, d, keyword)
				}
				continue
			}
			if reported == legal {
				t.Errorf("%s under %v: reported=%v, want %v", doc, d, reported, !legal)
			}
			if legal && (s.MaxLength == nil || s.MaxLength.Int() != -3) {
				t.Errorf("%s: a legal negative maxLength was not kept: %v", doc, s.MaxLength)
			}
		}
	}
}

// TestIntegerCountsAreReadExactly holds FlexInt to the exact reading and the
// saturation FlexInt documents -- including that a saturated count still says
// the number the schema wrote.
func TestIntegerCountsAreReadExactly(t *testing.T) {
	const maxI, minI = math.MaxInt, math.MinInt
	for _, tc := range []struct {
		lit       string
		want      int
		saturated bool
		negative  bool
		err       bool
	}{
		{"0", 0, false, false, false},
		{"-0", 0, false, false, false},
		{"-0.0e5", 0, false, false, false},
		{"7", 7, false, false, false},
		{"2.0", 2, false, false, false},
		{"1e3", 1000, false, false, false},
		{"1.5e1", 15, false, false, false},
		{"150e-1", 15, false, false, false},
		{"9007199254740993", 9007199254740993, false, false, false}, // 2^53+1: float64 rounds it
		{"9007199254740993.0", 9007199254740993, false, false, false},
		{"9223372036854775807", maxI, false, false, false}, // MaxInt64 itself is not saturated
		{"9223372036854775806", 9223372036854775806, false, false, false},
		// Past int64: saturated, never wrapped.
		{"9223372036854775808", maxI, true, false, false},
		{"1e19", maxI, true, false, false},
		{"1e400", maxI, true, false, false},
		{"1e99999999999999999999999", maxI, true, false, false},
		{"123456789012345678901234567890", maxI, true, false, false},
		{"-9223372036854775808", minI, false, true, false},
		{"-9223372036854775809", minI, true, true, false},
		{"-1e30", minI, true, true, false},
		{"-1", -1, false, true, false},
		// Not integers.
		{"1.5", 0, false, false, true},
		{"1e-1", 0, false, false, true},
		{"1e-99999999999999999999999", 0, false, false, true},
		{`"5"`, 0, false, false, true},
		{"true", 0, false, false, true},
		{"01", 0, false, false, true},
		{"1.", 0, false, false, true},
		{".5", 0, false, false, true},
		{"1e", 0, false, false, true},
		{"+1", 0, false, false, true},
	} {
		got, saturated, negative, err := parseIntegerLiteral(tc.lit)
		if (err != nil) != tc.err {
			t.Errorf("%s: err = %v, want error %v", tc.lit, err, tc.err)
			continue
		}
		if tc.err {
			continue
		}
		if got != tc.want || saturated != tc.saturated || negative != tc.negative {
			t.Errorf("%s = %d (saturated %v, negative %v), want %d (%v, %v)",
				tc.lit, got, saturated, negative, tc.want, tc.saturated, tc.negative)
		}
		var f FlexInt
		_ = f.UnmarshalJSON([]byte(tc.lit))
		wantText := strconv.Itoa(tc.want)
		if tc.saturated {
			wantText = tc.lit
		}
		if f.String() != wantText || f.Saturated() != tc.saturated {
			t.Errorf("%s: String() = %q (saturated %v), want %q", tc.lit, f.String(), f.Saturated(), wantText)
		}
		if b, _ := f.MarshalJSON(); string(b) != wantText {
			t.Errorf("%s marshals as %s, want %s", tc.lit, b, wantText)
		}
	}
}

// TestDraft3BooleanRequiredIsNotAPropertyName is the in-band marker's
// replacement: draft 3's boolean "required" is its own field, promoted onto
// the parent where the parent's dialect reads it, and never a member of any
// Required list.
func TestDraft3BooleanRequiredIsNotAPropertyName(t *testing.T) {
	const marker = "\x00__draft3_required_true__"
	// normalized reads doc and normalizes it, and returns whether any node
	// refused a keyword along the way.
	normalized := func(t *testing.T, doc string) (*Schema, bool) {
		t.Helper()
		var s Schema
		if err := json.Unmarshal([]byte(doc), &s); err != nil {
			t.Fatalf("%s: %v", doc, err)
		}
		s.Normalize()
		refused := false
		walkAll(&s, func(n *Schema) {
			if len(n.MalformedKeywords()) > 0 {
				refused = true
			}
		})
		return &s, refused
	}
	var walk func(s *Schema, fn func(*Schema))
	walk = func(s *Schema, fn func(*Schema)) {
		if s == nil {
			return
		}
		fn(s)
		s.eachChild(func(sub *Schema) { walk(sub, fn) })
	}

	// Where draft 3 gives the boolean no reading, it must require nothing --
	// the audit's two scenarios -- in every dialect.
	for _, d := range allDrafts {
		for _, body := range []string{
			`"type":"object","required":true`,
			`"additionalProperties":{"type":"object","required":true}`,
			`"items":{"required":true}`,
			`"patternProperties":{"^a":{"required":true}}`,
		} {
			doc := documentIn(d, body)
			s, refused := normalized(t, doc)
			// Outside draft 3 the boolean is another dialect's spelling of a
			// keyword the dialect defines, and is refused.
			if wantRefused := d != Draft03 && d != DraftUnknown; refused != wantRefused {
				t.Errorf("%s: refused=%v, want %v", doc, refused, wantRefused)
			}
			walk(s, func(n *Schema) {
				if len(n.Required) > 0 {
					t.Errorf("%s: a node requires %q", doc, n.Required)
				}
				if n.Draft3Required != nil {
					t.Errorf("%s: Normalize left the boolean behind", doc)
				}
			})
		}
	}

	// Under properties it is promoted exactly where the parent's dialect has
	// the spelling, in name order.
	for _, d := range allDrafts {
		doc := documentIn(d, `"properties":{"b":{"required":true},"a":{"required":true},"c":{"required":false}}`)
		s, refused := normalized(t, doc)
		want := []string(nil)
		if d == Draft03 || d == DraftUnknown {
			want = []string{"a", "b"}
		}
		if refused != (want == nil) {
			t.Errorf("%s: refused=%v; the boolean is refused exactly where it is not promoted", doc, refused)
		}
		if !reflect.DeepEqual([]string(s.Required), want) {
			t.Errorf("%s: required = %q, want %q", doc, s.Required, want)
		}
	}

	// A real property of the marker's name is an ordinary property name, and
	// requiring it is an ordinary requirement.
	for _, d := range []Draft{DraftUnknown, Draft04, Draft07, Draft202012, DraftV1} {
		doc := documentIn(d, `"required":["\u0000__draft3_required_true__"]`)
		s, refused := normalized(t, doc)
		if refused {
			t.Errorf("%s: refused", doc)
		}
		if len(s.Required) != 1 || s.Required[0] != marker {
			t.Errorf("%s: required = %q, want the one name it states", doc, s.Required)
		}
	}
	// Under DraftUnknown both spellings can stand, and neither displaces the
	// other.
	s, _ := normalized(t, `{"required":["a"],"properties":{"a":{"required":true},"b":{"required":true}}}`)
	if !reflect.DeepEqual([]string(s.Required), []string{"a", "b"}) {
		t.Errorf("both spellings: required = %q, want [a b]", s.Required)
	}
}

// TestLegacyKeywordSubschemasAreGatedUnderTheirOwnDialect is issue C30's class:
// a subschema that only exists once a draft 3-7 keyword is read as schemas must
// be gated like any other subschema. Each row states, inside such a keyword, a
// keyword the node's dialect does not define, and it must be gone after
// Normalize wherever it ended up.
func TestLegacyKeywordSubschemasAreGatedUnderTheirOwnDialect(t *testing.T) {
	type row struct {
		d     Draft
		doc   string
		reach func(*Schema) *Schema
	}
	var rows []row
	for _, d := range []Draft{Draft03, Draft04, Draft06, Draft07} {
		// dependencies is read in every dialect; "const" arrived in draft 6
		// and "contentMediaType" in draft 7, and "dependentRequired" in 2019-09.
		for _, kw := range []string{`"const":1`, `"contentMediaType":"text/plain"`, `"dependentRequired":{"x":["y"]}`} {
			rows = append(rows, row{d, documentIn(d, `"dependencies":{"a":{"properties":{"b":{`+kw+`}}}}`),
				func(s *Schema) *Schema { return s.DependentSchemas["a"].Properties["b"] }})
		}
	}
	// extends and disallow are draft 3's alone, and draft 3 has none of these.
	for _, kw := range []string{`"const":1`, `"allOf":[{"type":"string"}]`, `"not":{"type":"string"}`, `"multipleOf":2`} {
		rows = append(rows,
			row{Draft03, documentIn(Draft03, `"extends":{"properties":{"b":{`+kw+`}}}`),
				func(s *Schema) *Schema { return s.AllOf[0].Properties["b"] }},
			row{Draft03, documentIn(Draft03, `"extends":[{},{"properties":{"b":{`+kw+`}}}]`),
				func(s *Schema) *Schema { return s.AllOf[1].Properties["b"] }},
			row{Draft03, documentIn(Draft03, `"disallow":[{"properties":{"b":{`+kw+`}}}]`),
				func(s *Schema) *Schema { return s.Not.Properties["b"] }},
		)
	}
	for _, r := range rows {
		var s Schema
		if err := json.Unmarshal([]byte(r.doc), &s); err != nil {
			t.Fatalf("%s: %v", r.doc, err)
		}
		s.Normalize()
		node := r.reach(&s)
		stated, _ := node.MarshaledKeywords()
		for kw := range stated {
			if !KeywordDefinedIn(kw, r.d) {
				t.Errorf("%s: %q survived Normalize under %v, which does not define it", r.doc, kw, r.d)
			}
		}
		if !KeywordDefinedIn("const", r.d) && (node.Const != nil || node.ConstIsNull) {
			t.Errorf("%s: const survived under %v", r.doc, r.d)
		}
	}

	// The same subschemas keep what their dialect does define: the gate is not
	// clearing the legacy keywords' subtrees wholesale.
	var s Schema
	doc := documentIn(Draft07, `"dependencies":{"a":{"properties":{"b":{"const":1}}}}`)
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	if s.DependentSchemas["a"].Properties["b"].Const == nil {
		t.Errorf("%s: draft 7's own const was cleared", doc)
	}
}

// TestAnExtensionSubschemaIsGatedUnderItsParentsDialect is the same class for
// the one subschema that exists only when a $ref reaches it: a vendor keyword's
// value, parsed as a schema on demand. It used to be normalized as a document
// of its own, under no dialect.
func TestAnExtensionSubschemaIsGatedUnderItsParentsDialect(t *testing.T) {
	for _, tc := range []struct {
		d         Draft
		wantConst bool
	}{{Draft04, false}, {Draft06, true}, {Draft202012, true}, {DraftUnknown, true}} {
		var s Schema
		doc := documentIn(tc.d, `"x-vendor":{"const":1},"$ref":"#/x-vendor"`)
		if err := json.Unmarshal([]byte(doc), &s); err != nil {
			t.Fatal(err)
		}
		s.Normalize()
		target, err := NewLocalResolver(&s).Resolve("#/x-vendor")
		if err != nil {
			t.Fatalf("%s: %v", doc, err)
		}
		if got := target.Const != nil; got != tc.wantConst {
			t.Errorf("%s: const kept = %v, want %v", doc, got, tc.wantConst)
		}
	}
}

// TestAKeywordFormTheDialectDoesNotDefineIsRefused is the rule for a keyword
// written in the spelling of another dialect -- stated or forced with --draft --
// over every keyword whose form changed between drafts: where the node's
// dialect defines the keyword and not the form, the value is refused, with a
// message naming whose spelling it is, what to write instead, and, under a
// dialect chosen from outside the document, the dialect the document declares.
// Where the dialect defines the form, the keyword binds; under no recognised
// dialect every form binds. The cases are the table's own: every form of every
// row split by form must have one, or the test fails.
//
// This used to be the opposite rule, ignore rather than refuse, which let
// --draft 2020-12 on a draft-07 tuple keep "items" as a tuple while dropping
// "additionalItems" -- and would have let it drop both in silence.
func TestAKeywordFormTheDialectDoesNotDefineIsRefused(t *testing.T) {
	type check struct {
		keyword string
		form    valueForm
		members string
		kept    func(*Schema) bool
	}
	checks := []check{
		{"items", formSchemaArray, `"items":[{"type":"string"}]`, func(s *Schema) bool { return s.Items != nil }},
		{"items", formSchema, `"items":{"type":"string"}`, func(s *Schema) bool { return s.Items != nil }},
		{"type", formTypeSchemas, `"type":["string",{"minimum":1}]`, func(s *Schema) bool { return s.Type != nil || s.TypeSchemas != nil }},
		{"type", formTypeNames, `"type":["string","null"]`, func(s *Schema) bool { return s.Type != nil }},
		{"exclusiveMinimum", formBoolean, `"minimum":1,"exclusiveMinimum":true`, func(s *Schema) bool { return s.ExclusiveMinimum != nil }},
		{"exclusiveMinimum", formNumber, `"exclusiveMinimum":1`, func(s *Schema) bool { return s.ExclusiveMinimum != nil }},
		{"exclusiveMaximum", formBoolean, `"maximum":1,"exclusiveMaximum":false`, func(s *Schema) bool { return s.ExclusiveMaximum != nil }},
		{"exclusiveMaximum", formNumber, `"exclusiveMaximum":1`, func(s *Schema) bool { return s.ExclusiveMaximum != nil }},
		{"required", formBoolean, `"properties":{"a":{"required":true}}`, func(s *Schema) bool { return len(s.Required) > 0 }},
		{"required", formStringArray, `"required":["a"]`, func(s *Schema) bool { return len(s.Required) > 0 }},
	}
	covered := map[string]bool{}
	for _, c := range checks {
		covered[fmt.Sprint(c.keyword, c.form)] = true
	}
	for keyword, kd := range keywordDialects {
		for _, f := range kd.Forms {
			if !covered[fmt.Sprint(keyword, f.Form)] {
				t.Errorf("keywordDialects[%q] has a form with no case here", keyword)
			}
		}
	}

	for _, c := range checks {
		kd := keywordDialects[c.keyword]
		var f keywordForm
		for _, cand := range kd.Forms {
			if cand.Form == c.form {
				f = cand
			}
		}
		for _, d := range allDrafts {
			for _, forced := range []bool{false, true} {
				if d == DraftUnknown && forced {
					continue // DraftUnknown passed to NormalizeForDraft means "read the document"
				}
				var doc string
				var s Schema
				declared := d
				if forced {
					// The document says draft 7 or draft 3, whichever does not
					// match; --draft says d. The forced dialect wins at the root.
					declared = Draft07
					if d == Draft07 {
						declared = Draft03
					}
					doc = documentIn(declared, c.members)
					if err := json.Unmarshal([]byte(doc), &s); err != nil {
						t.Fatal(err)
					}
					s.NormalizeForDraft(d)
				} else {
					doc = documentIn(d, c.members)
					if err := json.Unmarshal([]byte(doc), &s); err != nil {
						t.Fatal(err)
					}
					s.Normalize()
				}
				var refusal *KeywordError
				walkAll(&s, func(n *Schema) {
					for _, ke := range n.MalformedKeywords() {
						refusal = ke
					}
				})
				defined := kd.definedIn(d, c.form)
				if got := c.kept(&s); got != defined {
					t.Errorf("%s (forced=%v) under %v: kept=%v, want %v", doc, forced, d, got, defined)
				}
				if defined {
					if refusal != nil {
						t.Errorf("%s (forced=%v) under %v: refused a form the dialect defines: %v", doc, forced, d, refusal)
					}
					continue
				}
				if refusal == nil {
					t.Errorf("%s (forced=%v) under %v: another dialect's form was not refused", doc, forced, d)
					continue
				}
				msg := refusal.Error()
				for _, want := range []string{c.keyword, f.Dialects, d.String(), f.Instead} {
					if !strings.Contains(msg, want) {
						t.Errorf("%s under %v: the refusal does not say %q: %s", doc, d, want, msg)
					}
				}
				if mentions := strings.Contains(msg, "the document's own $schema declares "+declared.String()); mentions != forced {
					t.Errorf("%s (forced=%v) under %v: names the declared dialect=%v: %s", doc, forced, d, mentions, msg)
				}
			}
		}
	}
}

// TestEveryKeywordFormSaysWhatToWriteInstead holds the strings a form's
// refusal is written from.
func TestEveryKeywordFormSaysWhatToWriteInstead(t *testing.T) {
	for keyword, kd := range keywordDialects {
		for _, f := range kd.Forms {
			if f.Shape == "" || f.Dialects == "" || f.Instead == "" {
				t.Errorf("keywordDialects[%q]: a form is missing its Shape, Dialects or Instead: %+v", keyword, f)
			}
		}
	}
}

// TestDraftForURIMatchesTheWholeURI pins exact dialect detection.
func TestDraftForURIMatchesTheWholeURI(t *testing.T) {
	for _, tc := range []struct {
		uri  string
		want Draft
	}{
		{"http://json-schema.org/draft-03/schema#", Draft03},
		{"http://json-schema.org/draft-03/schema", Draft03},
		{"https://json-schema.org/draft-03/schema#", Draft03},
		{"http://json-schema.org/draft-04/schema#", Draft04},
		{"http://json-schema.org/draft-04/hyper-schema#", Draft04},
		{"http://json-schema.org/draft-06/schema#", Draft06},
		{"http://json-schema.org/draft-07/schema#", Draft07},
		{"http://json-schema.org/draft-07/schema", Draft07},
		{"https://json-schema.org/draft/2019-09/schema", Draft201909},
		{"https://json-schema.org/draft/2019-09/schema#", Draft201909},
		{"http://json-schema.org/draft/2019-09/schema", Draft201909},
		{"https://json-schema.org/draft/2020-12/schema", Draft202012},
		{"https://json-schema.org/draft/2020-12/hyper-schema", Draft202012},
		{"https://json-schema.org/v1", DraftV1},
		{"https://json-schema.org/v1#", DraftV1},
		// Near misses: each contains a draft's name and names another dialect.
		{"https://example.com/my-draft-07-extension/schema", DraftUnknown},
		{"http://json-schema.org/draft-07/schema#/definitions", DraftUnknown},
		{"http://json-schema.org/draft-07/schema##", DraftUnknown},
		{"http://json-schema.org/draft-07/schema.json", DraftUnknown},
		{"https://json-schema.org/draft/2020-12/meta/validation", DraftUnknown},
		{"http://localhost:1234/draft2020-12/metaschema-no-validation.json", DraftUnknown},
		{"https://example.test/json-schema.org/v1", DraftUnknown},
		{"https://json-schema.org/v1/schema", DraftUnknown},
		{"ftp://json-schema.org/draft-07/schema#", DraftUnknown},
		{"json-schema.org/draft-07/schema#", DraftUnknown},
		{"", DraftUnknown},
	} {
		if got := DraftForURI(tc.uri); got != tc.want {
			t.Errorf("DraftForURI(%q) = %v, want %v", tc.uri, got, tc.want)
		}
		if got := DetectDraft(&Schema{Schema: tc.uri}); got != DraftForURI(tc.uri) {
			t.Errorf("DetectDraft disagrees with DraftForURI on %q", tc.uri)
		}
	}
}

// TestNormalizeIsIdempotentOverTheCorpus is the property Normalize(Normalize(x))
// == Normalize(x), over every schema document in the repository's corpus and,
// when they have been downloaded, every schema of the JSON Schema Test Suite --
// its test groups, the remote documents its $refs fetch -- and the draft
// meta-schemas.
//
// The failure it guards against is specific and was real: the second pass read
// the first one's output as a document, found draft 3 nodes stating the
// array-form "required" the first had just written, and cleared it as a
// spelling draft 3 does not define.
func TestNormalizeIsIdempotentOverTheCorpus(t *testing.T) {
	docs := corpusSchemas(t)
	if len(docs) < 500 {
		t.Fatalf("only %d schemas found; the corpus walk has stopped reaching them", len(docs))
	}
	checked := 0
	for _, d := range docs {
		var once, twice Schema
		if json.Unmarshal(d.body, &once) != nil || json.Unmarshal(d.body, &twice) != nil {
			continue // not a schema: a fixture of refusals, or instance data
		}
		for _, draft := range []Draft{DraftUnknown, d.draft} {
			once, twice = Schema{}, Schema{}
			_ = json.Unmarshal(d.body, &once)
			_ = json.Unmarshal(d.body, &twice)
			once.NormalizeForDraft(draft)
			twice.NormalizeForDraft(draft)
			twice.NormalizeForDraft(draft)
			if !reflect.DeepEqual(once, twice) {
				a, _ := json.Marshal(once)
				b, _ := json.Marshal(twice)
				t.Errorf("%s under %v: a second Normalize changed the schema\nonce:  %s\ntwice: %s",
					d.name, draft, abbreviateJSON(string(a)), abbreviateJSON(string(b)))
			}
			checked++
		}
	}
	t.Logf("%d schemas, %d normalizations compared", len(docs), checked)
}

type corpusSchema struct {
	name  string
	body  []byte
	draft Draft
}

// corpusSchemas collects the repository's schema fixtures and, if present, the
// test suite's schemas with the dialect of the directory they sit in.
func corpusSchemas(t *testing.T) []corpusSchema {
	t.Helper()
	var out []corpusSchema
	for _, f := range globJSON(t, "../../testdata/schemas") {
		out = append(out, corpusSchema{name: f.name, body: f.body})
	}
	suiteDrafts := map[string]Draft{
		"draft3": Draft03, "draft4": Draft04, "draft6": Draft06, "draft7": Draft07,
		"draft2019-09": Draft201909, "draft2020-12": Draft202012, "v1": DraftV1,
	}
	for dir, d := range suiteDrafts {
		for _, f := range globJSON(t, "../../testdata/external/JSON-Schema-Test-Suite/tests/"+dir) {
			var groups []struct {
				Schema json.RawMessage `json:"schema"`
			}
			if json.Unmarshal(f.body, &groups) != nil {
				continue
			}
			for i, g := range groups {
				out = append(out, corpusSchema{name: fmt.Sprintf("%s[%d]", f.name, i), body: g.Schema, draft: d})
			}
		}
		// The documents the suite's $refs fetch, under the dialect of the
		// directory they are served from.
		for _, f := range globJSON(t, "../../testdata/external/JSON-Schema-Test-Suite/remotes/"+dir) {
			out = append(out, corpusSchema{name: f.name, body: f.body, draft: d})
		}
	}
	// The remotes outside a dialect's directory, and the meta-schemas, each of
	// which declares its own dialect.
	for _, root := range []string{"../../testdata/external/JSON-Schema-Test-Suite/remotes", "../../testdata/external/metaschemas"} {
		for _, f := range globJSON(t, root) {
			if rel, _ := filepath.Rel(root, f.name); !strings.Contains(rel, string(filepath.Separator)) || strings.HasSuffix(root, "metaschemas") {
				out = append(out, corpusSchema{name: f.name, body: f.body})
			}
		}
	}
	return out
}

type jsonFile struct {
	name string
	body []byte
}

// globJSON reads every .json file under dir, or none when dir is absent (the
// test suite is downloaded, not vendored).
func globJSON(t *testing.T, dir string) []jsonFile {
	t.Helper()
	var out []jsonFile
	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == dir {
				return filepath.SkipDir
			}
			return err
		}
		if e.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, jsonFile{name: path, body: body})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return out
}

// walkAll calls fn on s and every subschema below it.
func walkAll(s *Schema, fn func(*Schema)) {
	if s == nil {
		return
	}
	fn(s)
	s.eachChild(func(sub *Schema) { walkAll(sub, fn) })
}
