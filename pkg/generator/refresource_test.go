package generator

import (
	"encoding/json"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// Two rules that the root-document anchor index used to supply by accident, and
// that resolving every reference in its own resource has to state instead.

// Through draft 7 an $id beside a $ref is one of the siblings the $ref
// replaces, so it starts no resource and moves no base URI: the suite's "$ref
// prevents a sibling $id from changing the base uri". The generator reads a
// document it was not told the dialect of under Config.Draft, so the index it
// resolves through has to read the $id that way too -- whether the dialect comes
// from $schema or from Config.Draft.
func TestSiblingIDBesideRefIsIgnoredThroughDraft7(t *testing.T) {
	const body = `
		"$id": "http://localhost:1234/sibling_id/base/",
		"definitions": {
			"foo": {"$id": "http://localhost:1234/sibling_id/foo.json", "type": "string", "title": "wrong"},
			"base_foo": {"$id": "foo.json", "type": "number", "title": "right"}
		},
		"allOf": [{"$id": "http://localhost:1234/sibling_id/", "$ref": "foo.json"}]`
	cases := []struct {
		name  string
		src   string
		draft schema.Draft
		want  string
	}{
		{"stated draft-07", `{"$schema": "http://json-schema.org/draft-07/schema#",` + body + `}`, schema.DraftUnknown, "right"},
		{"Config.Draft 7", `{` + body + `}`, schema.Draft07, "right"},
		{"Config.Draft 4", `{` + body + `}`, schema.Draft04, "right"},
		// From 2019-09 the $id applies and moves the base.
		{"stated 2020-12", `{"$schema": "https://json-schema.org/draft/2020-12/schema",` + body + `}`, schema.DraftUnknown, "wrong"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s schema.Schema
			if err := json.Unmarshal([]byte(tc.src), &s); err != nil {
				t.Fatal(err)
			}
			s.Normalize()
			g := New(Config{PackageName: "testpkg", Draft: tc.draft})
			if _, err := g.Generate(&s); err != nil {
				t.Fatalf("generate: %v", err)
			}
			branch := s.AllOf[0]
			got := g.resolveRefInContextUncounted(branch.Ref, branch)
			if got == nil || got.Title != tc.want {
				t.Errorf("foo.json beside a sibling $id reached %v, want the definition titled %q", got, tc.want)
			}
		})
	}
}

// In v1 a $dynamicRef names a dynamic anchor rather than a URI: "#items" in a
// resource that declares none is decided by the dynamic scope, whose outermost
// frame is always the document's root resource (the suite's "A $dynamicRef
// resolves to the first $dynamicAnchor still in scope ..."). In 2020-12 the same
// reference names nothing its resource declares and is refused.
func TestV1DynamicRefWithoutALocalAnchorIsDecidedByTheRootResource(t *testing.T) {
	doc := func(dialect string) string {
		return `{
			"$schema": "` + dialect + `",
			"$id": "https://test.json-schema.org/typical-dynamic-resolution/root",
			"$ref": "list",
			"$defs": {
				"foo": {"$dynamicAnchor": "items", "type": "string"},
				"list": {"$id": "list", "type": "array", "items": {"$dynamicRef": "#items"}}
			}
		}`
	}
	var s schema.Schema
	if err := json.Unmarshal([]byte(doc("https://json-schema.org/v1")), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	g := New(Config{PackageName: "testpkg"})
	if _, err := g.Generate(&s); err != nil {
		t.Fatalf("generate: %v", err)
	}
	items := s.Defs["list"].Items.Schema
	target, anchor := g.dynamicRefInitialTarget(items.DynamicRef, items, g.resolveRefInContextUncounted)
	if target != s.Defs["foo"] || anchor != "items" {
		t.Errorf("initial target = %v (anchor %q), want the root resource's $dynamicAnchor items", target, anchor)
	}

	var old schema.Schema
	if err := json.Unmarshal([]byte(doc("https://json-schema.org/draft/2020-12/schema")), &old); err != nil {
		t.Fatal(err)
	}
	old.Normalize()
	if _, err := New(Config{PackageName: "testpkg"}).Generate(&old); err == nil {
		t.Error(`2020-12: "#items" names no anchor the list resource declares, and must be refused`)
	}
}
