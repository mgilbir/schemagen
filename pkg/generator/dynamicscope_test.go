package generator

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
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

// TestDynamicScopeDecidesTheTarget is the narrowing that keeps issue #160's fix
// from claiming every schema that mentions these two keywords.
//
// Compiling a schema to the runtime evaluator is what makes a bookended
// reference resolve per document, and it costs the caller the struct or named
// type the static path would have produced. That price is worth paying only
// where the schema really does leave the target open, so the predicate asks
// whether a second declaration of the anchor is in reach -- and the two false
// cases below are what say it asks that rather than "does a dynamic reference
// appear anywhere".
//
// The recursive tree is the shape every $recursiveRef in the test suite is
// written as, and the shape most real 2019-09 schemas use. It anchors one
// resource and refers back to it, so the answer is the same down every path and
// the type it generates today is the right one. Answering true for it would turn
// each of those into a raw-JSON wrapper for no gain.
func TestDynamicScopeDecidesTheTarget(t *testing.T) {
	t.Run("two resources supply the same anchor", func(t *testing.T) {
		g, root := dynamicScopeFixture(t, `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://schemagen.test/two-paths/main",
			"type": "object",
			"properties": {
				"numbers": {"$ref": "numberList"},
				"strings": {"$ref": "stringList"}
			},
			"$defs": {
				"genericList": {
					"$id": "genericList",
					"properties": {"list": {"items": {"$dynamicRef": "#itemType"}}},
					"$defs": {"defaultItemType": {"$dynamicAnchor": "itemType"}}
				},
				"numberList": {
					"$id": "numberList",
					"$defs": {"itemType": {"$dynamicAnchor": "itemType", "type": "number"}},
					"$ref": "genericList"
				},
				"stringList": {
					"$id": "stringList",
					"$defs": {"itemType": {"$dynamicAnchor": "itemType", "type": "string"}},
					"$ref": "genericList"
				}
			}
		}`)
		if !g.dynamicScopeDecidesTheTarget(root) {
			t.Error("numberList and stringList both declare itemType and both reach the same reference, " +
				"so which one it means is a fact about the document and no single Go type can state it")
		}
		// genericList on its own reaches only its own declaration, and a caller
		// validating against that type starts the dynamic scope there -- so the
		// static answer is the whole answer and the type it has stays.
		if g.dynamicScopeDecidesTheTarget(root.Defs["genericList"]) {
			t.Error("genericList alone reaches one declaration of itemType, so its reference has one possible target")
		}
		// numberList is what makes following references load-bearing. It holds
		// one declaration of itemType and no dynamic reference at all; the
		// reference, and the second declaration that competes with its own, are
		// both inside genericList, which it can reach only through its $ref. A
		// walk of the subtree alone finds neither and answers false.
		if !g.dynamicScopeDecidesTheTarget(root.Defs["numberList"]) {
			t.Error("numberList reaches genericList only through its $ref, and that is where both the reference " +
				"and the competing declaration of itemType are; a reach that does not follow references misses them")
		}
	})

	t.Run("one resource supplies the anchor", func(t *testing.T) {
		// The second $recursiveAnchor, on `inner`, is what makes the count a
		// count of *resources* rather than of nodes. A $recursiveAnchor anchors
		// the root of the resource that writes it and nothing else -- the rule
		// pkg/schema's resource graph applies and findDynamicAnchorDeclarations
		// with it -- so this one puts nothing on any dynamic scope and there is
		// still exactly one place the reference can land. Counted as a
		// declaration it would make this schema look path-dependent and cost it
		// its generated type, which is the shape every recursive 2019-09 schema
		// in the suite has.
		g, root := dynamicScopeFixture(t, `{
			"$schema": "https://json-schema.org/draft/2019-09/schema",
			"$id": "https://schemagen.test/one-anchor/main.json",
			"$defs": {
				"node": {
					"$id": "node.json",
					"$recursiveAnchor": true,
					"type": "object",
					"properties": {"inner": {"$recursiveAnchor": true, "type": "object"}},
					"additionalProperties": {"$recursiveRef": "#"}
				}
			},
			"properties": {"tree": {"$ref": "node.json"}}
		}`)
		if g.dynamicScopeDecidesTheTarget(root) {
			t.Error("one resource declares the anchor, so the reference means the same thing down every path; " +
				"routing this to the evaluator would cost every recursive 2019-09 schema its generated type")
		}
	})

	t.Run("no bookend", func(t *testing.T) {
		// Two anchored resources are in reach on purpose, so the count alone
		// answers yes and the bookend test is the only thing left saying no.
		// Without them this subtest would pass for a predicate that had stopped
		// reading bookending at all.
		//
		// "#" is the only value 2019-09 gives $recursiveRef, and a $recursiveRef
		// spelled as anything else is read as the plain reference it looks like
		// -- dynamicRefTarget's rule, and the reason "$recursiveRef with no
		// $recursiveAnchor works like $ref" is a test with its answer in the
		// title. So the two anchors below are not in play for this reference,
		// however many of them there are.
		g, root := dynamicScopeFixture(t, `{
			"$schema": "https://json-schema.org/draft/2019-09/schema",
			"$id": "https://schemagen.test/unbookended/main.json",
			"properties": {"a": {"$ref": "#/$defs/user"}},
			"$defs": {
				"user": {"$recursiveRef": "#/$defs/narrow"},
				"narrow": {"type": "integer"},
				"one": {"$id": "one.json", "$recursiveAnchor": true, "type": "number"},
				"two": {"$id": "two.json", "$recursiveAnchor": true, "type": "boolean"}
			}
		}`)
		if g.dynamicScopeDecidesTheTarget(root) {
			t.Error("a $recursiveRef whose value is not \"#\" is a plain reference, so neither $recursiveAnchor " +
				"in the document is ever searched and the target is the one the pointer names")
		}
	})
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

// TestStaticDynamicRefReadsTheScopeByResource is issues #163 and #164 at the
// function the fix is written in.
//
// The compiled-and-run half is TestDynamicAnchorOnANestedIdBelongsToThatResource,
// which is what proves the binding reaches a document; this is what says
// resolveDynamicRef asks of a scope frame the same question the generated
// evaluator asks, rather than the one findDynamicAnchor answers. The two
// disagree only where an anchor sits on a node carrying its own $id, and no
// corpus file has that shape -- so without this and the fixture beside it the
// rule is held by nothing.
func TestStaticDynamicRefReadsTheScopeByResource(t *testing.T) {
	// A stray resource: a $defs entry with its own $id and a $dynamicAnchor,
	// which nothing refers to. Read by resource it is on no scope any evaluation
	// builds; read by findDynamicAnchor the document root publishes it, and it
	// then outranks the bookend for every reference in the document.
	t.Run("a boundary node publishes nothing", func(t *testing.T) {
		g, root := dynamicScopeFixture(t, `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://schemagen.test/boundary/main",
			"type": "object",
			"properties": {"box": {"$ref": "genericBox"}},
			"$defs": {
				"genericBox": {
					"$id": "genericBox",
					"properties": {"value": {"$dynamicRef": "#itemType"}},
					"$defs": {"defaultItemType": {"$dynamicAnchor": "itemType"}}
				},
				"stray": {"$id": "strayItemType", "$dynamicAnchor": "itemType", "type": "number"}
			}
		}`)
		box := root.Defs["genericBox"]
		ref := box.Properties["value"]
		bookend := box.Defs["defaultItemType"]

		g.dynamicScope = []*schema.Schema{root, box}
		got := g.resolveDynamicRef(ref.DynamicRef, ref)
		if got != bookend {
			t.Errorf("resolveDynamicRef = %s, want the bookend: strayItemType is a resource of its own, "+
				"so nothing that enters the document root puts its anchor on the scope", describeAnchorTarget(got))
		}
		// The control on the fixture rather than on the fix: if the stray anchor
		// were not reachable by the old reading at all, the case above would
		// pass for any implementation whatsoever.
		if findDynamicAnchor(root, "itemType") != root.Defs["stray"] {
			t.Error("the fixture no longer reproduces the divergence: findDynamicAnchor must still credit " +
				"the stray anchor to the document root, or nothing here is being tested")
		}
	})

	// The other direction, and the reason the fix is not "take the bookend". A
	// resource that declares the anchor in its own $defs still answers, and it
	// answers even though an outer frame was asked first and said nothing.
	t.Run("an entered resource still publishes its own", func(t *testing.T) {
		g, root := dynamicScopeFixture(t, `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://schemagen.test/inner/main",
			"type": "object",
			"properties": {"box": {"$ref": "holder"}},
			"$defs": {
				"aStray": {"$id": "strayRes", "$dynamicAnchor": "itemType"},
				"genericBox": {
					"$id": "genericBox",
					"properties": {"value": {"$dynamicRef": "#itemType"}},
					"$defs": {"defaultItemType": {"$dynamicAnchor": "itemType"}}
				},
				"holder": {
					"$id": "holder",
					"$ref": "genericBox",
					"$defs": {"itemType": {"$dynamicAnchor": "itemType", "type": "string"}}
				}
			}
		}`)
		box := root.Defs["genericBox"]
		ref := box.Properties["value"]
		want := root.Defs["holder"].Defs["itemType"]

		g.dynamicScope = []*schema.Schema{root, root.Defs["holder"], box}
		if got := g.resolveDynamicRef(ref.DynamicRef, ref); got != want {
			t.Errorf("resolveDynamicRef = %s, want holder's own itemType: the document root declares none, "+
				"so the walk has to go on to the resource that does", describeAnchorTarget(got))
		}
	})

	// The frame that declares the anchor on its own root rather than below it.
	// resourceDynamicAnchor answers this one from its isRoot arm, which the two
	// cases above never reach, and a reading that skipped the frame node itself
	// would fall through to the bookend here and nowhere else.
	t.Run("a resource anchored on its own root", func(t *testing.T) {
		g, root := dynamicScopeFixture(t, `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://schemagen.test/self/main",
			"type": "object",
			"properties": {"box": {"$ref": "holder"}},
			"$defs": {
				"genericBox": {
					"$id": "genericBox",
					"properties": {"value": {"$dynamicRef": "#itemType"}},
					"$defs": {"defaultItemType": {"$dynamicAnchor": "itemType"}}
				},
				"holder": {
					"$id": "holder",
					"$dynamicAnchor": "itemType",
					"type": ["object", "string"]
				}
			}
		}`)
		box := root.Defs["genericBox"]
		ref := box.Properties["value"]
		holder := root.Defs["holder"]

		g.dynamicScope = []*schema.Schema{root, holder, box}
		if got := g.resolveDynamicRef(ref.DynamicRef, ref); got != holder {
			t.Errorf("resolveDynamicRef = %s, want holder itself: an anchor on a resource root is the "+
				"resource's own, and the frame that entered it publishes it", describeAnchorTarget(got))
		}
	})
}

// describeAnchorTarget names a resolved target by what distinguishes one
// candidate from another here, since every one of them is an anonymous
// subschema and %v over a Schema prints a wall of zero fields.
func describeAnchorTarget(s *schema.Schema) string {
	if s == nil {
		return "nil"
	}
	return "{$id: " + s.ID + ", type: " + strings.Join(s.Type, "|") + "}"
}

// TestReferenceKeywordsFollowAnExplicitDraft is what normalizeDialectRefKeywords
// still does that schema.Normalize's dialect gate does not.
//
// Normalize reads the dialect the document declares. Config.Draft is the
// caller's statement that the document is to be read as some other dialect, and
// it reaches the generator's own per-node answer -- draftForSchema -- which
// supportsPrefixItems and supportsDependentRequired read too. A caller who
// supplies it to Generate and not to normalization gets it applied here, and
// dropping this pass would leave --draft reaching those two keywords and not
// these.
func TestReferenceKeywordsFollowAnExplicitDraft(t *testing.T) {
	src := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://schemagen.test/explicit-draft",
		"properties": {
			"rec": {"$recursiveRef": "#/$defs/narrow"},
			"dyn": {"$dynamicRef": "#/$defs/narrow"}
		},
		"$defs": {"narrow": {"type": "integer"}}
	}`

	// Normalized under the document's own dialect, which defines both, and then
	// generated as draft 7, which defines neither.
	var s schema.Schema
	if err := json.Unmarshal([]byte(src), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	if s.Properties["rec"].RecursiveRef == "" || s.Properties["dyn"].DynamicRef == "" {
		t.Fatalf("2020-12 lost a reference keyword in normalization; the control this test needs is gone")
	}
	if _, err := New(Config{PackageName: "testpkg", Draft: schema.Draft07}).Generate(&s); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got := s.Properties["rec"].RecursiveRef; got != "" {
		t.Errorf("$recursiveRef = %q under --draft 7, want it dropped", got)
	}
	if got := s.Properties["dyn"].DynamicRef; got != "" {
		t.Errorf("$dynamicRef = %q under --draft 7, want it dropped", got)
	}
}

// TestDynamicScopeStaysAtTheTypeItStartedIn holds the invariant
// resolveRecursiveRef's direction argument rests on: when a bookended reference
// is resolved, the dynamic scope stands at exactly the depth the type being
// generated started at, and no frame pushed on the way there is still on it.
//
// That is what makes the walk's *order* inert under a scope seeded at the type
// rather than at the document (#293). Outermost-first and innermost-first are
// the same walk on one frame, so the two candidate scope constructions differ
// only in what that one frame is -- the seed -- and not in how it is searched.
//
// This is a test and not a comment because the same function has already carried
// a wrong measurement in a comment for months. #167 recorded that the walk "is
// called three times, always at len(dynamicScope) == 1", instrumented it once,
// threw the instrumentation away, and left a fixture that did not reach the code
// at all; the claim was false and nothing in the tree could say so. So the
// counters live on the Generator and are read here.
//
// Two assertions, because the interesting failure is the quiet one. The depth is
// the invariant. The consultation count is what stops a zero depth from being
// vacuous: were the corpus to lose every bookended reference, or a refactor to
// stop routing through these two functions, the depth would read zero for the
// uninteresting reason and this test would go on passing. It has been watched
// failing in both directions -- planting a resolution into mergeAllOfBranches'
// pushed window makes the depth read 2, and removing the corpus's dynamic
// references makes the count read 0.
func TestDynamicScopeStaysAtTheTypeItStartedIn(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "schemas")

	var consultations, generated int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return err
		}
		s, loadErr := schema.LoadFromFile(path)
		if loadErr != nil {
			return nil
		}
		s.NormalizeForDraft(schema.DraftUnknown)
		s.ComputeBaseURIs(nil, s)
		abs, absErr := filepath.Abs(path)
		if absErr != nil {
			return nil
		}
		g := New(Config{
			PackageName: "testpkg",
			OmitEmpty:   true,
			Validation:  ValidationModeStatic,
			// The corpus has schemas whose $ref names a sibling file, and without
			// a resolver rooted beside them those references are never followed --
			// which is exactly the path that pushes a frame, so the run would be
			// measuring the shapes this test is not about.
			Resolver: schema.NewCompositeResolver(schema.NewFileResolver(filepath.Dir(abs))),
		})
		if _, genErr := g.Generate(s); genErr != nil {
			return nil
		}
		generated++
		consultations += g.dynamicScopeConsultations
		if g.framesAboveTypeScope != 0 {
			t.Errorf("%s: the dynamic scope was consulted %d frames above the depth the type being generated started at, want 0.\n"+
				"A frame pushed inside one generateTypeDefBody is still on the scope when a bookended reference is resolved, so "+
				"resolveRecursiveRef's walk now has more than one frame to choose between and its direction decides a verdict. "+
				"See the direction argument on resolveRecursiveRef, which this breaks.",
				path, g.framesAboveTypeScope)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if generated == 0 {
		t.Fatalf("no schema under %s generated, so nothing was measured", root)
	}
	if consultations == 0 {
		t.Fatalf("the dynamic scope was never consulted across %d generated schemas, so the depth assertion above is vacuous. "+
			"Either the corpus lost every bookended $recursiveRef and $dynamicRef, or resolution stopped going through "+
			"resolveRecursiveRef and resolveDynamicRef -- and in the second case the invariant is no longer being measured at all",
			generated)
	}
	t.Logf("%d schemas generated, dynamic scope consulted %d times", generated, consultations)
}
