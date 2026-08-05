package generator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// dynamicScopeFixture parses a schema and runs it through a Generator, which is
// what computes the base URIs and document roots every function below reads. A
// test that skipped Generate would be asking about a tree in which no node knows
// which resource it belongs to, and every answer would be the same wrong one.
func dynamicScopeFixture(t *testing.T, src string) (*Generator, *schema.Schema) {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(src), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	g := New(Config{PackageName: "testpkg", Validation: ValidationModeHybrid})
	if _, err := g.Generate(&s); err != nil {
		t.Fatalf("generate: %v", err)
	}
	return g, &s
}

// TestResourceDynamicAnchorStopsAtANestedResource is the difference between
// resourceDynamicAnchor and findDynamicAnchor, and the reason there are two.
//
// findDynamicAnchor stops descending at an $id but still reads the boundary node
// itself, which is right for "what can this document reach" and wrong for "what
// does this resource put on the dynamic scope". The fixture is the suite's
// "after leaving a dynamic scope" shape: the document root holds $defs/thingy,
// whose own $id makes it the resource inner_scope. Credited to the document
// root, thingy would be on the outermost frame of every evaluation and the
// $dynamicRef would resolve to it every time -- the one answer that schema
// exists to call wrong.
func TestResourceDynamicAnchorStopsAtANestedResource(t *testing.T) {
	_, root := dynamicScopeFixture(t, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://schemagen.test/scope/main",
		"then": {
			"$id": "second_scope",
			"$defs": {"thingy": {"$dynamicAnchor": "thingy", "type": "null"}}
		},
		"$defs": {
			"thingy": {"$id": "inner_scope", "$dynamicAnchor": "thingy", "type": "string"}
		}
	}`)

	if got := resourceDynamicAnchor(root, "thingy"); got != nil {
		t.Errorf("the document root declares no anchor of its own, but resourceDynamicAnchor found one (type %v) — "+
			"an anchor on a nested $id belongs to that resource, not to this one", got.Type)
	}
	// The control: the resource that really does declare it must still answer,
	// or the check above would pass for a function that never finds anything.
	inner := root.Defs["thingy"]
	if got := resourceDynamicAnchor(inner, "thingy"); got != inner {
		t.Errorf("inner_scope declares thingy on its own root; got %v", got)
	}
	second := root.Then
	if got := resourceDynamicAnchor(second, "thingy"); got == nil || got.Type[0] != "null" {
		t.Errorf("second_scope declares thingy in its own $defs; got %v", got)
	}
	// findDynamicAnchor is the reading that must not be used here, and this
	// pins the disagreement rather than assuming it.
	if findDynamicAnchor(root, "thingy") == nil {
		t.Error("findDynamicAnchor is expected to reach across the $id boundary; " +
			"if it no longer does, resourceDynamicAnchor has stopped being a distinct reading")
	}
}

// TestDynamicRefTargetReadsBookending covers the decision that settles most of
// these keywords: a reference whose static target does not carry the anchor it
// names is a $ref with a longer name, and no dynamic scope is consulted for it.
func TestDynamicRefTargetReadsBookending(t *testing.T) {
	t.Run("$recursiveRef without an anchored target", func(t *testing.T) {
		g, root := dynamicScopeFixture(t, `{
			"$schema": "https://json-schema.org/draft/2019-09/schema",
			"$id": "https://schemagen.test/plain/schema.json",
			"$defs": {
				"myobject": {
					"$id": "myobject.json",
					"$recursiveAnchor": false,
					"additionalProperties": {"$recursiveRef": "#"}
				}
			}
		}`)
		myobject := root.Defs["myobject"]
		ref := myobject.AdditionalProperties.Schema
		target, anchor, dynamic, ok := g.dynamicRefTarget(ref)
		if !ok || target != myobject {
			t.Fatalf("target = %v (ok=%v), want the resource the keyword is written in", target, ok)
		}
		if anchor != "" || dynamic {
			t.Errorf("dynamic = %v, anchor = %q; $recursiveAnchor is false, so nothing is searched", dynamic, anchor)
		}
	})

	t.Run("$recursiveRef with an anchored target", func(t *testing.T) {
		g, root := dynamicScopeFixture(t, `{
			"$schema": "https://json-schema.org/draft/2019-09/schema",
			"$id": "https://schemagen.test/anchored/schema.json",
			"$defs": {
				"myobject": {
					"$id": "myobject.json",
					"$recursiveAnchor": true,
					"additionalProperties": {"$recursiveRef": "#"}
				}
			}
		}`)
		myobject := root.Defs["myobject"]
		ref := myobject.AdditionalProperties.Schema
		target, anchor, dynamic, ok := g.dynamicRefTarget(ref)
		if !ok || target != myobject || anchor != "" || !dynamic {
			t.Fatalf("got target=%v anchor=%q dynamic=%v ok=%v, want the resource, the empty anchor, and a dynamic search",
				target, anchor, dynamic, ok)
		}
	})

	t.Run("$dynamicRef without a matching $dynamicAnchor", func(t *testing.T) {
		g, root := dynamicScopeFixture(t, `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://schemagen.test/unbookended/main",
			"$defs": {
				"plain": {"$anchor": "thingy", "type": "string"},
				"user": {"$dynamicRef": "#thingy"}
			}
		}`)
		target, _, dynamic, ok := g.dynamicRefTarget(root.Defs["user"])
		if !ok || target != root.Defs["plain"] {
			t.Fatalf("target = %v (ok=%v), want the plain $anchor", target, ok)
		}
		if dynamic {
			t.Error("a plain $anchor is not a bookend, so no dynamic search happens")
		}
	})
}

// TestDynamicRefCanLoopRefusesAReferenceThatNeverDescends is the check the node
// builder's own cycle detection cannot make.
//
// That one refuses a cycle in the schema *tree*. A dynamic reference closes its
// cycle through a target chosen per document, so there is no such edge to find:
// {"$id":"a","$recursiveAnchor":true,"$recursiveRef":"#"} is three keywords, no
// tree cycle at all, and a generated program that never returns.
//
// The accept case is the one that matters as much. Every recursive schema in the
// test suite gets back to its own reference through properties or
// additionalProperties, so a check that refused all recursion would refuse the
// schemas this exists to compile.
func TestDynamicRefCanLoopRefusesAReferenceThatNeverDescends(t *testing.T) {
	t.Run("in place", func(t *testing.T) {
		g, root := dynamicScopeFixture(t, `{
			"$schema": "https://json-schema.org/draft/2019-09/schema",
			"$id": "https://schemagen.test/loop/schema.json",
			"$defs": {
				"a": {"$id": "a.json", "$recursiveAnchor": true, "$recursiveRef": "#"}
			}
		}`)
		a := root.Defs["a"]
		target, anchor, dynamic, ok := g.dynamicRefTarget(a)
		if !ok || !dynamic {
			t.Fatalf("fixture is not a dynamic reference: ok=%v dynamic=%v", ok, dynamic)
		}
		if !g.dynamicRefCanLoop(a, target, anchor) {
			t.Error("a $recursiveRef on the root of the resource it anchors reaches itself with the same value; " +
				"compiling it produces a program that does not finish")
		}
	})

	t.Run("descending", func(t *testing.T) {
		g, root := dynamicScopeFixture(t, `{
			"$schema": "https://json-schema.org/draft/2019-09/schema",
			"$id": "https://schemagen.test/descend/schema.json",
			"$defs": {
				"a": {
					"$id": "a.json",
					"$recursiveAnchor": true,
					"additionalProperties": {"$recursiveRef": "#"}
				}
			}
		}`)
		ref := root.Defs["a"].AdditionalProperties.Schema
		target, anchor, _, ok := g.dynamicRefTarget(ref)
		if !ok {
			t.Fatal("fixture did not resolve")
		}
		if g.dynamicRefCanLoop(ref, target, anchor) {
			t.Error("the value gets smaller each time round, so a finite document ends the recursion; " +
				"refusing this refuses every recursive schema the suite has")
		}
	})
}

// TestRuntimeSchemaDefCarriesRecursionAndRefusesRegress is the same distinction
// one level up, at the thing that emits code.
//
// A cycle that descends becomes a node variable the schema points back at; a
// cycle that applies to the same value for ever is refused, and the caller gets
// what it got before hoisting existed. Both directions are asserted because the
// refusal alone is satisfied by a builder that hoists nothing.
func TestRuntimeSchemaDefCarriesRecursionAndRefusesRegress(t *testing.T) {
	t.Run("descending cycle", func(t *testing.T) {
		g, root := dynamicScopeFixture(t, `{
			"$schema": "https://json-schema.org/draft/2019-09/schema",
			"$id": "https://schemagen.test/rec/schema.json",
			"anyOf": [
				{"type": "string"},
				{"type": "object", "additionalProperties": {"$ref": "#"}}
			]
		}`)
		def := g.runtimeSchemaDef("Root", root)
		if def == nil {
			t.Fatal("a schema whose additionalProperties are schemas of its own shape must compile")
		}
		if len(def.Nodes) != 1 {
			t.Fatalf("Nodes = %d, want the one node the cycle points back at", len(def.Nodes))
		}
		if !strings.Contains(def.Nodes[0].Literal, "Ref: &"+def.Nodes[0].Name) {
			t.Errorf("the hoisted node does not refer back to itself:\n%s", def.Nodes[0].Literal)
		}
	})

	t.Run("in-place cycle", func(t *testing.T) {
		g, root := dynamicScopeFixture(t, `{
			"$schema": "https://json-schema.org/draft/2019-09/schema",
			"$id": "https://schemagen.test/regress/schema.json",
			"anyOf": [
				{"type": "string"},
				{"allOf": [{"$ref": "#"}]}
			]
		}`)
		if def := g.runtimeSchemaDef("Root", root); def != nil {
			t.Errorf("an allOf that leads back to the same value is an infinite regress, "+
				"and compiling it produces a program that does not finish; got:\n%s", def.NodeLiteral)
		}
	})
}

// TestRuntimeSchemaDefNamesHoistedNodesAfterTheirType checks the one thing that
// makes two compiled schemas able to share a package: the variables are named
// after the type they belong to, which is unique in its package, so no two
// schemas can claim the same name.
func TestRuntimeSchemaDefNamesHoistedNodesAfterTheirType(t *testing.T) {
	g, root := dynamicScopeFixture(t, `{
		"$schema": "https://json-schema.org/draft/2019-09/schema",
		"$id": "https://schemagen.test/names/schema.json",
		"anyOf": [
			{"type": "string"},
			{"type": "object", "additionalProperties": {"$ref": "#"}}
		]
	}`)
	first := g.runtimeSchemaDef("Alpha", root)
	second := g.runtimeSchemaDef("Beta", root)
	if first == nil || second == nil {
		t.Fatal("both compilations must succeed")
	}
	if first.Nodes[0].Name == second.Nodes[0].Name {
		t.Fatalf("two types in one package would declare %s twice", first.Nodes[0].Name)
	}
	for _, def := range []*AnnotationSchemaDef{first, second} {
		if !strings.HasPrefix(def.Nodes[0].Name, "_rt"+def.Name) {
			t.Errorf("%s: node named %q does not name its type", def.Name, def.Nodes[0].Name)
		}
	}
}

// TestHelpersReferencedByReadsTheDynamicArms covers the flag that decides
// whether the helper file declares _schemaAnchor, _dynamicRef, _schemaFrame and
// the scope-carrying evaluator at all.
//
// All three spellings are checked separately because each can appear without the
// others -- a recursive schema with no dynamic reference in it names only the
// first -- and a missed one is a file naming a type the helpers do not declare,
// which does not compile.
func TestHelpersReferencedByReadsTheDynamicArms(t *testing.T) {
	for _, tt := range []struct {
		name string
		src  string
		want bool
	}{
		{"node reference", `var X = _schemaNode{Ref: &_rtXNode1}`, true},
		{"dynamic reference", `var X = _schemaNode{DynamicRef: &_dynamicRef{Anchor: "a"}}`, true},
		{"resource frame", `var X = _schemaNode{DynamicAnchors: []_schemaAnchor{{Name: "a"}}}`, true},
		{"neither", `var X = _schemaNode{Type: []string{"string"}}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := HelpersReferencedBy(tt.src).AnnotationsDynamic; got != tt.want {
				t.Errorf("AnnotationsDynamic = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestReferenceKeywordsFollowTheirDialect is issue #161 at the level the fix is
// written: the normalization pass, over every draft this generator identifies.
//
// The compiled-and-run half is TestReferenceKeywordsAreIgnoredByDraftsWithoutThem,
// which covers draft 7, 2019-09, 2020-12 and v1. Drafts 3, 4 and 6 have no
// fixture of their own -- neither keyword can appear in a suite file for them and
// a hand-written one would say what draft 7's already says -- so this is what
// holds them, and it is a table rather than three more fixtures because the
// question is the same question seven times.
//
// The two keywords are asked separately of every draft, and 2019-09 is why: it
// defines one of them and not the other, so a gate written per draft rather than
// per keyword passes every other row here and fails that one.
func TestReferenceKeywordsFollowTheirDialect(t *testing.T) {
	for _, tc := range []struct {
		draft    string
		uri      string
		defsKey  string
		keepsRec bool
		keepsDyn bool
	}{
		{draft: "3", uri: "http://json-schema.org/draft-03/schema#", defsKey: "definitions"},
		{draft: "4", uri: "http://json-schema.org/draft-04/schema#", defsKey: "definitions"},
		{draft: "6", uri: "http://json-schema.org/draft-06/schema#", defsKey: "definitions"},
		{draft: "7", uri: "http://json-schema.org/draft-07/schema#", defsKey: "definitions"},
		{draft: "2019-09", uri: "https://json-schema.org/draft/2019-09/schema", defsKey: "$defs", keepsRec: true},
		{draft: "2020-12", uri: "https://json-schema.org/draft/2020-12/schema", defsKey: "$defs", keepsRec: true, keepsDyn: true},
		{draft: "v1", uri: "https://json-schema.org/v1", defsKey: "$defs", keepsRec: true, keepsDyn: true},
	} {
		t.Run(tc.draft, func(t *testing.T) {
			src := `{
				"$schema": "` + tc.uri + `",
				"$id": "https://schemagen.test/ref-keywords/` + tc.draft + `",
				"properties": {
					"rec": {"$recursiveRef": "#/` + tc.defsKey + `/narrow"},
					"dyn": {"$dynamicRef": "#/` + tc.defsKey + `/narrow"}
				},
				"` + tc.defsKey + `": {"narrow": {"type": "integer"}}
			}`
			_, root := dynamicScopeFixture(t, src)
			if got := root.Properties["rec"].RecursiveRef != ""; got != tc.keepsRec {
				t.Errorf("draft %s keeps $recursiveRef = %v, want %v", tc.draft, got, tc.keepsRec)
			}
			if got := root.Properties["dyn"].DynamicRef != ""; got != tc.keepsDyn {
				t.Errorf("draft %s keeps $dynamicRef = %v, want %v", tc.draft, got, tc.keepsDyn)
			}
			// EffectiveRef is the function issue #161 names, and the one some
			// forty call sites read. Clearing the field is what makes it agree,
			// so it is asserted here rather than assumed to follow.
			if got := root.Properties["rec"].EffectiveRef() != ""; got != tc.keepsRec {
				t.Errorf("draft %s: EffectiveRef reports a reference = %v, want %v", tc.draft, got, tc.keepsRec)
			}
		})
	}
}

// TestReferenceKeywordsFollowTheNodesOwnDialect is the half of the pass that a
// document-wide gate would get wrong.
//
// $schema is declared per resource, so a 2019-09 resource embedded in a draft-7
// document is read under 2019-09 and keeps its $recursiveRef, while the draft-7
// nodes around it lose theirs. The same rule normalizeDialectFormats applies to
// draft 3's format spellings, and the same reason refOverridesSiblingsForSchema
// exists beside refOverridesSiblings.
func TestReferenceKeywordsFollowTheNodesOwnDialect(t *testing.T) {
	_, root := dynamicScopeFixture(t, `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://schemagen.test/mixed/main",
		"properties": {"host": {"$recursiveRef": "#/definitions/narrow"}},
		"definitions": {
			"narrow": {"type": "integer"},
			"embedded": {
				"$id": "https://schemagen.test/mixed/embedded",
				"$schema": "https://json-schema.org/draft/2019-09/schema",
				"properties": {"guest": {"$recursiveRef": "#/$defs/narrow"}},
				"$defs": {"narrow": {"type": "integer"}}
			}
		}
	}`)
	if got := root.Properties["host"].RecursiveRef; got != "" {
		t.Errorf("the draft-7 host kept $recursiveRef = %q", got)
	}
	embedded := root.Definitions["embedded"]
	if got := embedded.Properties["guest"].RecursiveRef; got == "" {
		t.Error("the embedded 2019-09 resource lost its $recursiveRef; the gate is reading the document rather than the node")
	}
}
