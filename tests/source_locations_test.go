package tests

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// A refusal names where the document wrote the value it refuses, and this file
// holds that to an oracle rather than to a list of expected strings: the
// location a diagnostic prints is decoded with the one pointer decoder
// (schema.FragmentPointer) and walked through the document's own bytes, parsed
// here independently of pkg/schema, and what it reaches has to be the offending
// value itself. A location in this package's rewritten spelling of the document
// -- "#/allOf/1" for the second "extends" entry, "#/$defs/a" for a draft-07
// "definitions" member -- reaches nothing, or the wrong value, and fails.
//
// The matrix is every rewrite Normalize performs, crossed with every kind of
// refusal that prints a location, at the rewritten position itself (depth 1)
// and one schema below it (depth 2):
//
//   - the rewrites: draft 3's "extends" (one schema, and an array entry),
//     "disallow" (one schema, and an array entry beside a type name), draft 3's
//     schema-valued "type" entries, "dependencies" (under draft 3 and draft 7),
//     "definitions" and "$defs" (each mirrored into the other where one is
//     empty), and "divisibleBy"; plus "properties", which nothing rewrites, as
//     the control, and "extends" under no dialect at all;
//   - the refusals: a null where a schema belongs, a value that is not a schema,
//     a keyword whose value is malformed (null, and the wrong type), and a
//     keyword written in the form another dialect gives it.
//
// Then the same across the two ways a node is reached after Generate checked
// its argument: through a $ref into another document, and into a vendor
// keyword's value.

const uriDraft201909 = "https://json-schema.org/draft/2019-09/schema"

// locationPayload is what a rewritten position holds, and the value the
// refusal must name.
type locationPayload struct {
	name string
	// body is the JSON written at the rewritten position.
	body string
	// offending is the JSON of the value the refusal is about, which the printed
	// location must reach in the document.
	offending string
}

func draft3Payloads() []locationPayload {
	return []locationPayload{
		{"null", `null`, `null`},
		{"not a schema", `5`, `5`},
		{"null keyword", `{"minLength":null}`, `null`},
		{"depth 2: malformed keyword", `{"properties":{"p":{"minLength":"x"}}}`, `"x"`},
		{"depth 2: null member", `{"properties":{"p":null}}`, `null`},
		{"depth 2: null member of a rewritten keyword", `{"extends":[{},null]}`, `null`},
		{"form: the array required", `{"properties":{"q":{"required":["a"]}}}`, `["a"]`},
		{"divisibleBy malformed", `{"divisibleBy":"x"}`, `"x"`},
	}
}

func laterPayloads() []locationPayload {
	return []locationPayload{
		{"null", `null`, `null`},
		{"not a schema", `5`, `5`},
		{"null keyword", `{"minLength":null}`, `null`},
		{"depth 2: malformed keyword", `{"properties":{"p":{"minLength":"x"}}}`, `"x"`},
		{"depth 2: null member", `{"properties":{"p":null}}`, `null`},
		{"depth 2: null member of a rewritten keyword", `{"dependencies":{"b":null}}`, `null`},
		{"form: the boolean required", `{"properties":{"q":{"required":true}}}`, `true`},
	}
}

func TestRefusalsNameTheLocationTheDocumentWrote(t *testing.T) {
	type rewrite struct {
		name     string
		dialect  string // "" for no $schema
		wrap     func(x string) string
		payloads []locationPayload
	}
	// Under no dialect every form binds, so no form is refused there.
	noForms := func(ps []locationPayload) []locationPayload {
		var out []locationPayload
		for _, p := range ps {
			if !strings.HasPrefix(p.name, "form:") {
				out = append(out, p)
			}
		}
		return out
	}
	rewrites := []rewrite{
		{"extends, one schema", uriDraft03, func(x string) string { return `{"extends":` + x + `}` }, draft3Payloads()},
		{"extends, an array entry", uriDraft03, func(x string) string { return `{"extends":[{},` + x + `]}` }, draft3Payloads()},
		{"disallow, one schema", uriDraft03, func(x string) string { return `{"disallow":` + x + `}` }, draft3Payloads()},
		{"disallow, an entry beside a type name", uriDraft03, func(x string) string { return `{"disallow":["string",` + x + `]}` }, draft3Payloads()},
		{"type, a schema entry", uriDraft03, func(x string) string { return `{"type":["string",` + x + `]}` }, draft3Payloads()},
		{"dependencies, draft 3", uriDraft03, func(x string) string { return `{"dependencies":{"a":` + x + `}}` }, draft3Payloads()},
		{"dependencies, draft 7", uriDraft07, func(x string) string { return `{"dependencies":{"a":` + x + `}}` }, laterPayloads()},
		{"definitions, mirrored as $defs", uriDraft07, func(x string) string { return `{"definitions":{"a":` + x + `}}` }, laterPayloads()},
		{"definitions, draft 4", uriDraft04, func(x string) string { return `{"definitions":{"a":` + x + `}}` }, laterPayloads()},
		{"$defs, mirrored as definitions", uriDraft201909, func(x string) string { return `{"$defs":{"a":` + x + `}}` }, laterPayloads()},
		{"divisibleBy beside multipleOf", uriDraft03, func(x string) string { return `{"divisibleBy":2,"multipleOf":3,"extends":` + x + `}` }, draft3Payloads()},
		{"properties, no rewrite", uriDraft07, func(x string) string { return `{"properties":{"a":` + x + `}}` }, laterPayloads()},
		// Keys a URI fragment cannot hold as they stand -- "%", "^", a space --
		// and the two RFC 6901 escapes, under a rewrite: the location has to
		// be one the decoder reads back to these keys.
		{"hostile keys under dependencies", uriDraft07, func(x string) string {
			return `{"dependencies":{"a%25b ^c":{"properties":{"d/e~f":` + x + `}}}}`
		}, laterPayloads()},
		{"extends under no dialect", "", func(x string) string { return `{"extends":[{},` + x + `]}` }, noForms(draft3Payloads())},
		{"dependencies under no dialect", "", func(x string) string { return `{"dependencies":{"a":` + x + `}}` }, noForms(laterPayloads())},
	}

	// Counted from the matrix rather than from the runs, so that running a
	// subset with -run does not trip it.
	cases := 0
	for _, rw := range rewrites {
		cases += len(rw.payloads)
	}
	if cases < 100 {
		t.Fatalf("the matrix holds %d cases; it has stopped generating them", cases)
	}
	for _, rw := range rewrites {
		for _, p := range rw.payloads {
			t.Run(rw.name+"/"+p.name, func(t *testing.T) {
				doc := rw.wrap(p.body)
				if rw.dialect != "" {
					doc = withSchemaKeyword(t, doc, rw.dialect)
				}
				err := generateForLocation(t, doc, nil)
				if err == nil {
					t.Fatalf("%s generated; it holds %s, which no dialect defining the keyword accepts", doc, p.offending)
				}
				assertLocationReaches(t, err.Error(), map[string]string{"": doc}, p.offending)
			})
		}
	}
}

// TestRefusalsAcrossReferencesNameTheLocationTheDocumentWrote covers the two
// ways a node is first reached after Generate has checked its argument, which
// the null check runs again from: a $ref into another document, and a $ref
// into a vendor keyword's value. Each is crossed with a rewrite on the way to
// the offending value.
func TestRefusalsAcrossReferencesNameTheLocationTheDocumentWrote(t *testing.T) {
	const otherURI = "https://ex.test/other.json"
	for _, tc := range []struct {
		name      string
		root      string
		other     string
		offending string
	}{
		{
			name:      "into another document, under its dependencies",
			root:      `{"$schema":"` + uriDraft07 + `","type":"object","properties":{"x":{"$ref":"` + otherURI + `#/definitions/a"}}}`,
			other:     `{"$schema":"` + uriDraft07 + `","$id":"` + otherURI + `","definitions":{"a":{"dependencies":{"b":{"minLength":null}}}}}`,
			offending: `null`,
		},
		{
			name:      "into another document, under its extends",
			root:      `{"$schema":"` + uriDraft07 + `","type":"object","properties":{"x":{"$ref":"` + otherURI + `#/definitions/a"}}}`,
			other:     `{"$schema":"` + uriDraft03 + `","$id":"` + otherURI + `","definitions":{"a":{"extends":[{},{"properties":{"p":null}}]}}}`,
			offending: `null`,
		},
		{
			name:      "into another document, at a rewritten location",
			root:      `{"$schema":"` + uriDraft07 + `","type":"object","properties":{"x":{"$ref":"` + otherURI + `#/extends/1"}}}`,
			other:     `{"$schema":"` + uriDraft03 + `","$id":"` + otherURI + `","extends":[{},{"minLength":"x"}]}`,
			offending: `"x"`,
		},
		{
			name:      "into a vendor keyword, under its extends",
			root:      `{"$schema":"` + uriDraft03 + `","x-vendor":[{"extends":{"minLength":null}}],"type":"object","properties":{"x":{"$ref":"#/x-vendor/0"}}}`,
			offending: `null`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := map[string]string{"": tc.root}
			var resolver schema.SchemaResolver
			if tc.other != "" {
				var other schema.Schema
				if err := json.Unmarshal([]byte(tc.other), &other); err != nil {
					t.Fatal(err)
				}
				other.Normalize()
				resolver = schema.NewMappingResolver(map[string]*schema.Schema{otherURI: &other})
				docs[otherURI] = tc.other
			}
			err := generateForLocation(t, tc.root, resolver)
			if err == nil {
				t.Fatalf("%s generated; the document it reaches holds %s where no schema may", tc.root, tc.offending)
			}
			assertLocationReaches(t, err.Error(), docs, tc.offending)
		})
	}
}

func generateForLocation(t *testing.T, doc string, resolver schema.SchemaResolver) error {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("parsing %s: %v", doc, err)
	}
	s.Normalize()
	_, err := generator.New(generator.Config{PackageName: "testpkg", Resolver: resolver}).Generate(&s)
	return err
}

// assertLocationReaches finds the location msg reports, reads it with the one
// pointer decoder, walks the document it names -- docs maps a document's URI to
// its bytes, "" to the document Generate was handed -- and requires the value
// there to be offending.
func assertLocationReaches(t *testing.T, msg string, docs map[string]string, offending string) {
	t.Helper()
	loc, ok := printedLocation(msg)
	if !ok {
		t.Fatalf("the refusal names no location:\n%s", msg)
	}
	docPart, fragment, _ := strings.Cut(loc, "#")
	raw, ok := docs[docPart]
	if !ok {
		t.Fatalf("the refusal names %q, a document the run does not hold:\n%s", docPart, msg)
	}
	tokens, isPointer, err := schema.FragmentPointer(fragment)
	if err != nil || !isPointer {
		t.Fatalf("the refusal names %q, which is not a JSON Pointer fragment (%v):\n%s", loc, err, msg)
	}
	got, err := walkJSON(raw, tokens)
	if err != nil {
		t.Fatalf("the refusal names %q, which is not a location in the document (%v):\n%s\ndocument: %s", loc, err, msg, raw)
	}
	var want any
	if err := json.Unmarshal([]byte(offending), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.Marshal(got)
		t.Fatalf("the refusal names %q, which holds %s in the document, not the offending %s:\n%s\ndocument: %s", loc, gotJSON, offending, msg, raw)
	}
}

// printedLocation returns the location a refusal starts its complaint with: the
// URI reference ending at the first ": " after a "#".
func printedLocation(msg string) (string, bool) {
	hash := strings.Index(msg, "#")
	if hash < 0 {
		return "", false
	}
	start := strings.LastIndexAny(msg[:hash], " \n\t") + 1
	end := strings.Index(msg[hash:], ": ")
	if end < 0 {
		return "", false
	}
	return msg[start : hash+end], true
}

// walkJSON follows reference tokens through a JSON document parsed with
// encoding/json alone, so nothing pkg/schema reads or rewrites stands between
// the location and the bytes.
func walkJSON(doc string, tokens []string) (any, error) {
	var cur any
	if err := json.Unmarshal([]byte(doc), &cur); err != nil {
		return nil, err
	}
	for _, tok := range tokens {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[tok]
			if !ok {
				return nil, &json.UnsupportedValueError{Str: "no member " + strconv.Quote(tok)}
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(node) {
				return nil, &json.UnsupportedValueError{Str: "no entry " + strconv.Quote(tok)}
			}
			cur = node[i]
		default:
			return nil, &json.UnsupportedValueError{Str: "a scalar has no member " + strconv.Quote(tok)}
		}
	}
	return cur, nil
}
