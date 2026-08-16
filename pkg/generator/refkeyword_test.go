package generator

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// Three keywords carry a reference this generator resolves, and until #307 the
// refusal called all three "$ref". The value is recorded at one place --
// resolveRefInContextAs -- which is reached through a callback from
// dynamicRefInitialTarget and cannot see the keyword unless the caller states
// it, so what these tests hold is the threading rather than the wording.
//
// The fixtures each name an anchor or a pointer that no document declares, which
// is the failure shape a $dynamicRef and a $recursiveRef actually have: their
// value is a bare fragment, so it is the document's own contents that are
// missing and not another document.
func generateForRefKeyword(t *testing.T, src string) error {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(src), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	g := New(Config{PackageName: "testpkg", Validation: ValidationModeHybrid})
	_, err := g.Generate(&s)
	if err == nil {
		t.Fatal("generation succeeded on a reference nothing can serve")
	}
	return err
}

func TestUnresolvedRefErrorNamesTheKeywordThatWasWritten(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		ref     string
		keyword string
	}{
		{
			name: "$ref",
			src: `{
				"$schema": "https://json-schema.org/draft/2020-12/schema",
				"title": "Doc", "type": "object",
				"properties": {"p": {"$ref": "#/$defs/nope"}}
			}`,
			ref:     "#/$defs/nope",
			keyword: "$ref",
		},
		{
			// 2020-12 §8.2.3.2. The fragment names a $dynamicAnchor, and no
			// document in this run declares one called "nope".
			name: "$dynamicRef",
			src: `{
				"$schema": "https://json-schema.org/draft/2020-12/schema",
				"$id": "https://ex.test/dyn.json",
				"title": "Doc", "type": "object",
				"properties": {"p": {"$dynamicRef": "#nope"}}
			}`,
			ref:     "#nope",
			keyword: "$dynamicRef",
		},
		{
			// 2019-09's spelling. "#" always resolves -- it is the resource in
			// scope -- so the fixture writes the one other thing the keyword can
			// say: a pointer, read as a plain reference, into a $defs that has no
			// such entry.
			name: "$recursiveRef",
			src: `{
				"$schema": "https://json-schema.org/draft/2019-09/schema",
				"$id": "https://ex.test/rec.json",
				"$recursiveAnchor": true,
				"title": "Doc", "type": "object",
				"properties": {"p": {"$recursiveRef": "#/$defs/nope"}}
			}`,
			ref:     "#/$defs/nope",
			keyword: "$recursiveRef",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := generateForRefKeyword(t, tc.src)

			var unresolved *UnresolvedRefsError
			if !errors.As(err, &unresolved) {
				t.Fatalf("want an UnresolvedRefsError, got %T: %v", err, err)
			}
			got := unresolved.Keywords[tc.ref]
			if len(got) != 1 || got[0] != tc.keyword {
				t.Errorf("Keywords[%q] = %v, want [%s]", tc.ref, got, tc.keyword)
			}
			want := "cannot resolve " + tc.keyword + " " + `"` + tc.ref + `"`
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the message should say %q, got:\n%v", want, err)
			}
			// The whole defect in one line: naming the keyword is worth nothing
			// if the old spelling is still in the sentence beside it.
			for _, other := range []string{"$ref", "$dynamicRef", "$recursiveRef"} {
				if other == tc.keyword {
					continue
				}
				if strings.Contains(err.Error(), "cannot resolve "+other+" ") {
					t.Errorf("the message also names %s, which this document does not write:\n%v", other, err)
				}
			}
		})
	}
}

// A run whose references fail under two different keywords must name both, each
// against its own refs. One heading over a merged list is what the old message
// was, and it is wrong for whichever half it does not describe.
func TestUnresolvedRefErrorGroupsTheKeywordsSeparately(t *testing.T) {
	err := generateForRefKeyword(t, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/both.json",
		"title": "Doc", "type": "object",
		"properties": {
			"p": {"$ref": "#/$defs/nope"},
			"q": {"$dynamicRef": "#alsoNope"}
		}
	}`)

	msg := err.Error()
	for _, want := range []string{
		`cannot resolve $ref "#/$defs/nope"`,
		`cannot resolve $dynamicRef "#alsoNope"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message should say %q, got:\n%s", want, msg)
		}
	}
	// Deterministic: two runs of the same input produce the same sentence, or
	// nobody can diff a build log against another.
	if again := generateForRefKeyword(t, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/both.json",
		"title": "Doc", "type": "object",
		"properties": {
			"p": {"$ref": "#/$defs/nope"},
			"q": {"$dynamicRef": "#alsoNope"}
		}
	}`); again.Error() != msg {
		t.Errorf("the message is not stable between identical runs:\n%s\n%s", msg, again)
	}
}

// One node, two reference keywords, the same value on both. This is the case
// the keyword cannot be read back off the node for -- refKeywordOf sees $ref
// first and would answer $ref for the $recursiveRef too -- and it is why
// resolveRecursiveRef and the callback resolveDynamicRef hands to
// dynamicRefInitialTarget state their keyword instead of leaving it inferred.
// Both are written, so both are reported: a message naming one of them is a
// message that is silently wrong about the other.
func TestBothReferenceKeywordsOnOneNodeAreBothNamed(t *testing.T) {
	err := generateForRefKeyword(t, `{
		"$schema": "https://json-schema.org/draft/2019-09/schema",
		"$id": "https://ex.test/both-keywords.json",
		"$recursiveAnchor": true,
		"title": "Doc", "type": "object",
		"properties": {"p": {"$ref": "#/$defs/nope", "$recursiveRef": "#/$defs/nope"}}
	}`)

	var unresolved *UnresolvedRefsError
	if !errors.As(err, &unresolved) {
		t.Fatalf("want an UnresolvedRefsError, got %T: %v", err, err)
	}
	got := unresolved.Keywords["#/$defs/nope"]
	if len(got) != 2 || got[0] != "$recursiveRef" || got[1] != "$ref" {
		t.Errorf("Keywords = %v, want both [$recursiveRef $ref]", got)
	}
	for _, want := range []string{
		`cannot resolve $ref "#/$defs/nope"`,
		`cannot resolve $recursiveRef "#/$defs/nope"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message should say %q, got:\n%v", want, err)
		}
	}
}

// The two predicates the advice is composed from, held directly. Their whole
// purpose is that each clause of the parenthetical appears only where it is
// true, and a predicate is the smallest thing that can be wrong about that.
func TestUnresolvedRefsErrorSeparatesSameAndOtherDocumentRefs(t *testing.T) {
	cases := []struct {
		name          string
		refs          []string
		wantSame      bool
		wantOtherDoc  bool
		wantKeywordOf map[string][]string
	}{
		{name: "pointer", refs: []string{"#/$defs/x"}, wantSame: true},
		{name: "anchor", refs: []string{"#name"}, wantSame: true},
		{name: "absolute URI", refs: []string{"https://ex.test/a.json"}, wantOtherDoc: true},
		{name: "relative path", refs: []string{"other.json#/$defs/x"}, wantOtherDoc: true},
		{name: "both", refs: []string{"#name", "other.json"}, wantSame: true, wantOtherDoc: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &UnresolvedRefsError{Refs: tc.refs}
			if got := e.AnySameDocument(); got != tc.wantSame {
				t.Errorf("AnySameDocument() = %v, want %v", got, tc.wantSame)
			}
			if got := e.AnyOtherDocument(); got != tc.wantOtherDoc {
				t.Errorf("AnyOtherDocument() = %v, want %v", got, tc.wantOtherDoc)
			}
		})
	}
}

// An error carrying no Keywords -- one a caller outside this package built --
// still renders, as "$ref". Silence, or a message with an empty keyword in it,
// would be worse than the defect being fixed.
func TestUnresolvedRefsErrorWithoutKeywordsReadsAsPlainRef(t *testing.T) {
	e := &UnresolvedRefsError{Refs: []string{"a.json"}}
	if want := `cannot resolve $ref "a.json"`; e.Error() != want {
		t.Errorf("Error() = %q, want %q", e.Error(), want)
	}
}
