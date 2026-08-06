package generator

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// findDef returns the generated definition for the named type, or nil.
func findDef(defs []TypeDef, name string) TypeDef {
	for _, td := range defs {
		if td.TypeName() == name {
			return td
		}
	}
	return nil
}

// A root composition whose branches the static evaluator refuses used to become
// `type Root any`. Go forbids methods on an interface-underlying type, so that
// type carries no Validate at all and json.Unmarshal into it cannot fail --
// which turned "I cannot read this schema" into "this schema permits
// everything". Each shape below is one of the blockers issue #113 counted.
func TestRefusedBranchesCompileToTheRuntimeEvaluator(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"boolean branch", `{"anyOf":[true,{"type":"string"}]}`},
		{"false branch", `{"anyOf":[false,{"type":"string"}]}`},
		{"$ref branch", `{"$defs":{"x":{"type":"integer"}},"anyOf":[{"$ref":"#/$defs/x"},{"type":"string"}]}`},
		{"nested composition", `{"anyOf":[{"anyOf":[{"type":"null"}]}]}`},
		{"nested oneOf", `{"oneOf":[{"oneOf":[{"type":"null"}]}]}`},
		{"const in a branch", `{"anyOf":[{"const":"x"},{"type":"integer"}]}`},
		{"enum in a branch", `{"anyOf":[{"enum":[1,2]},{"type":"string"}]}`},
		{"object keywords in a branch", `{"anyOf":[{"required":["a"]},{"minimum":2}]}`},
		{"$defs inside a branch", `{"anyOf":[{"$defs":{"y":{"type":"integer"}},"type":"string"},{"type":"integer"}]}`},
		{"$recursiveAnchor sibling", `{"$recursiveAnchor":true,"anyOf":[{"type":"boolean"},{"type":"integer"}]}`},
		{"if/then over object shape", `{"if":{"required":["a"]},"then":{"required":["b"]}}`},
		// Issue #114: a root "not" whose sub-schema states object structure.
		{"not over an object shape", `{"not":{"type":"object","properties":{"foo":{"type":"string"}}}}`},
		{"not over a required key", `{"not":{"required":["foo"]}}`},
		// Issue #111: unevaluatedProperties is modelled, so a schema whose only
		// content is that keyword no longer fails the evaluator closed.
		{"unevaluatedProperties in a branch", `{"not":{"anyOf":[true],"unevaluatedProperties":false}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defs := generateOne(t, tt.input)
			root := findDef(defs, "Root")
			if root == nil {
				t.Fatalf("no Root type generated for %s", tt.input)
			}
			switch root.(type) {
			case *AnnotationSchemaDef, *DynamicSchemaDef, *NotSchemaDef:
				// Any of the wrappers carries a Validate; which one is chosen is
				// the business of the more specific tests below.
			default:
				t.Fatalf("Root is %T for %s, expected a wrapper carrying a Validate", root, tt.input)
			}
		})
	}
}

// The runtime evaluator is the last thing tried, so it must not take a schema
// away from a path that already handles it better. These are the shapes that
// worked before it existed and have to keep the exact type they had.
func TestRuntimeEvaluatorDoesNotHijackWorkingShapes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string // %T of the Root definition
	}{
		{"expressible anyOf keeps the inline evaluator", `{"anyOf":[{"type":"string"},{"type":"number"}]}`, "*generator.DynamicSchemaDef"},
		{"expressible oneOf keeps the inline evaluator", `{"oneOf":[{"type":"integer"},{"minimum":2}]}`, "*generator.DynamicSchemaDef"},
		{"if/then over bounds keeps the inline evaluator", `{"if":{"exclusiveMaximum":0},"then":{"minimum":-10}}`, "*generator.DynamicSchemaDef"},
		{"not over a type keeps the not wrapper", `{"not":{"type":"string"}}`, "*generator.NotSchemaDef"},
		{"not over everything keeps the not wrapper", `{"not":{}}`, "*generator.NotSchemaDef"},
		{"an object stays a struct", `{"type":"object","properties":{"a":{"type":"string"}}}`, "*generator.StructDef"},
		{"a typed scalar stays an alias", `{"type":"string","minLength":2}`, "*generator.AliasDef"},
		{"an enum stays an enum", `{"enum":["a","b"]}`, "*generator.EnumDef"},
		{"an array stays an alias", `{"type":"array","items":{"type":"string"}}`, "*generator.AliasDef"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := findDef(generateOne(t, tt.input), "Root")
			if root == nil {
				t.Fatalf("no Root type generated for %s", tt.input)
			}
			if got := fmt.Sprintf("%T", root); got != tt.want {
				t.Fatalf("Root is %s for %s, want %s", got, tt.input, tt.want)
			}
		})
	}
}

// The constraint-only arm of issue #126 is the last thing every position tries,
// and this is what says so. Each schema below already had a Go type that carries
// its checks; taking the position over would replace that type with a raw-JSON
// wrapper, which is a worse type for the caller and no better a check.
//
// The last two are the ones a syntactic emptiness test gets wrong. A composition
// whose every branch is the `true` schema says exactly what {} says, but it is
// not spelled {} -- the compiled node has an AllOf field, so the literal is not
// one of the three unownedNodeLiterals and the wrapper looked worthwhile. See
// acceptsEveryValue.
func TestConstraintOnlyArmDoesNotHijackTypedPositions(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		jsonName string
		want     string
	}{
		{"typed element", `{"type":"object","properties":{"p":{"type":"array","items":{"type":"string"}}}}`, "p", "[]string"},
		{"typed map value", `{"type":"object","properties":{"p":{"type":"object","additionalProperties":{"type":"string"}}}}`, "p", "map[string]string"},
		{"constrained scalar", `{"type":"object","properties":{"p":{"type":"string","minLength":3}}}`, "p", "*string"},
		{"nullable scalar", `{"type":"object","properties":{"p":{"type":["string","null"]}}}`, "p", "*string"},
		{"allOf over true", `{"type":"object","properties":{"p":{"allOf":[{"$ref":"#/$defs/always"}]}},"$defs":{"always":true}}`, "p", "any"},
		{"allOf over empty", `{"type":"object","properties":{"p":{"allOf":[{},{}]}}}`, "p", "any"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := structNamed(t, generateForItemTest(t, addTitle(tt.input)), "Doc")
			if got := fieldNamedJSON(t, doc, tt.jsonName).Type.GoTypeName(); got != tt.want {
				t.Fatalf("%q: type = %q, want %q -- the constraint-only arm claimed a position that was already typed", tt.jsonName, got, tt.want)
			}
		})
	}
}

// addTitle names the root Doc so structNamed can find it.
func addTitle(input string) string {
	return `{"title":"Doc",` + input[1:]
}

// A schema that genuinely constrains nothing describes exactly what `any`
// describes. Turning those into a wrapper struct would break every caller's
// field type for no validation at all, so the evaluator has to hand them back.
func TestUnconstrainedSchemasKeepAny(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty schema", `{}`},
		{"annotations only", `{"description":"d","$comment":"c"}`},
		{"definitions only", `{"$defs":{"x":{"type":"integer"}}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := findDef(generateOne(t, tt.input), "Root")
			alias, ok := root.(*AliasDef)
			if !ok {
				t.Fatalf("Root is %T for %s, want an alias to any", root, tt.input)
			}
			if p, ok := alias.Underlying.(*PrimitiveType); !ok || p.Name != "any" {
				t.Fatalf("Root underlying is %v for %s, want any", alias.Underlying, tt.input)
			}
			if alias.Unenforced != "" {
				t.Fatalf("a schema that constrains nothing was reported as unenforced: %q", alias.Unenforced)
			}
		})
	}
}

// The evaluator must fail closed. A schema carrying something it cannot model
// keeps `any` rather than gaining a Validate that checks less than the schema
// says -- but it must now say so, because `any` has no Validate to be missing
// and cannot fail to unmarshal, so a caller has no other way to find out.
func TestUnmodelledKeywordsFailClosedAndAreReported(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		keyword string
	}{
		// The content keywords used to sit here. They are modelled now -- not by
		// the evaluator, which still has no arm for them, but by the string
		// wrapper issue #115 routes them to, which is why
		// TestContentOnlySchemaBecomesAStringWrapper is the test that owns them.
		//
		// unevaluatedProperties used to sit here too, and is modelled now as
		// well: issue #111 gave the evaluator an arm for it, so a schema whose
		// only content is that keyword no longer fails closed. Its case moved to
		// TestRefusedBranchesCompileToTheRuntimeEvaluator, in the other
		// direction.
		//
		// "format" stays out of the evaluator's model on purpose: schemagen
		// asserts a format only where the schema gives the position a string
		// type, and a node evaluator that quietly ignored it would enforce a
		// different schema here than the static path does two lines away.
		{"format in a branch", `{"not":{"anyOf":[true],"format":"email"}}`, "not"},
		{"an unknown keyword", `{"anyOf":[{"x-vendor":1},{"type":"string"}]}`, "anyOf"},
		{"a vendor keyword at the root", `{"x-vendor":{"a":1}}`, "x-vendor"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := findDef(generateOne(t, tt.input), "Root")
			alias, ok := root.(*AliasDef)
			if !ok {
				t.Fatalf("Root is %T for %s, want the any alias", root, tt.input)
			}
			if p, ok := alias.Underlying.(*PrimitiveType); !ok || p.Name != "any" {
				t.Fatalf("Root underlying is %v for %s, want any", alias.Underlying, tt.input)
			}
			if !strings.Contains(alias.Unenforced, tt.keyword) {
				t.Fatalf("Unenforced = %q for %s, want it to name %q", alias.Unenforced, tt.input, tt.keyword)
			}
		})
	}
}

// A $ref that leads back to a schema already being compiled cannot be inlined,
// and enforcing the part above the cycle would be a different schema. It has to
// be refused rather than looped on.
func TestRecursiveRefIsRefusedRatherThanInlined(t *testing.T) {
	defs := generateOne(t, `{"$defs":{"node":{"anyOf":[{"type":"null"},{"$ref":"#/$defs/node"}]}},"$ref":"#/$defs/node"}`)
	for _, td := range defs {
		if d, ok := td.(*AnnotationSchemaDef); ok {
			t.Fatalf("a cyclic schema was inlined into a runtime literal: %s", d.NodeLiteral)
		}
	}
}

// A pattern anywhere in the compiled literal is what pulls the regexp engine
// into the package's helper file. A literal without one must not, or every
// generated package would acquire a third-party dependency it never uses.
func TestPatternDrivesTheRegexpHelper(t *testing.T) {
	withPattern := findDef(generateOne(t, `{"anyOf":[{"anyOf":[{"pattern":"^a"}]},{"type":"integer"}]}`), "Root")
	def, ok := withPattern.(*AnnotationSchemaDef)
	if !ok {
		t.Fatalf("Root is %T, want an AnnotationSchemaDef", withPattern)
	}
	if !def.NeedsPattern {
		t.Fatal("a literal naming a pattern did not ask for the regexp helper")
	}

	withoutPattern := findDef(generateOne(t, `{"anyOf":[{"anyOf":[{"minLength":2}]},{"type":"integer"}]}`), "Root")
	plain, ok := withoutPattern.(*AnnotationSchemaDef)
	if !ok {
		t.Fatalf("Root is %T, want an AnnotationSchemaDef", withoutPattern)
	}
	if plain.NeedsPattern {
		t.Fatal("a literal with no pattern asked for the regexp helper anyway")
	}
}

// The report must name only types the reader can find. Some arms mint a type,
// discover it can carry no check, and leave it out of the file; a warning about
// one of those sends the reader looking for a declaration that is not there.
//
// The accept-control is the second half: a type that *is* declared and is
// genuinely unenforced still has to be reported, or the filter would be a way of
// silencing the diagnostic rather than sharpening it.
func TestUnenforcedReportNamesOnlyDeclaredTypes(t *testing.T) {
	// "^u" mints a type for the bucket, which carries nothing and is dropped
	// from the file; the root itself is a struct and has nothing to report.
	pruned := generatorFor(t, `{
		"title": "Doc",
		"type": "object",
		"patternProperties": {"^u": {"format": "ipv4"}}
	}`)
	if got := pruned.UnenforcedSchemas(); len(got) != 0 {
		t.Fatalf("reported %v, but none of those types is declared in the file", got)
	}

	declared := generatorFor(t, `{"title":"Doc","x-vendor-rule":{"a":1}}`)
	got := declared.UnenforcedSchemas()
	if len(got) != 1 || got[0].TypeName != "Doc" {
		t.Fatalf("UnenforcedSchemas() = %v, want one entry naming Doc", got)
	}
}

// generatorFor runs a schema through generation and hands back the generator, so
// a test can ask it what it reported as well as what it emitted.
func generatorFor(t *testing.T, input string) *Generator {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	g := New(Config{PackageName: "testpkg", LenientRefs: true})
	if _, err := g.Generate(&s); err != nil {
		t.Fatalf("generate: %v", err)
	}
	return g
}

// The two bare boolean schemas answer differently, and each answer is load-bearing.
//
// `true` accepts every value, which is exactly what `any` already says, so it
// stays an alias. Wrapping it gains no check and costs one: an alias over the
// wrapper inherits none of its methods and stopped decoding, a false rejection.
//
// `false` rejects every value, so it gets the forbidding wrapper -- the same one
// the document root has always produced, now reached in every position. The
// parent's old forbidden-property rule compared the field to nil and so could not
// see an explicit null; the wrapper's own Validate judges the value instead and
// refuses `{"b":null}` as well as `{"b":1}`.
func TestBareBooleanDefinitionsKeepTheirType(t *testing.T) {
	defs := generateOne(t, `{"$defs":{"yes":true,"no":false},"type":"object","properties":{"a":{"$ref":"#/$defs/yes"},"b":{"$ref":"#/$defs/no"}}}`)

	if def := findDef(defs, "Yes"); def != nil {
		alias, ok := def.(*AliasDef)
		if !ok {
			t.Fatalf("Yes is %T, want the alias a `true` schema has always produced", def)
		}
		if p, ok := alias.Underlying.(*PrimitiveType); !ok || p.Name != "any" {
			t.Fatalf("Yes underlying is %v, want any", alias.Underlying)
		}
	}

	if def := findDef(defs, "No"); def != nil {
		if _, ok := def.(*NotSchemaDef); !ok {
			t.Fatalf("No is %T, want the forbidding wrapper -- a `false` schema must reject every instance, including an explicit null", def)
		}
	}
}

// A definition that does compile to the runtime evaluator has to stay usable
// when something names it: a Go named type inherits none of its underlying
// type's methods, so the alias must be told to delegate them.
func TestAliasOverRuntimeWrapperDelegatesJSON(t *testing.T) {
	defs := generateOne(t, `{"$defs":{"wrapped":{"anyOf":[{"anyOf":[{"type":"null"}]}]}},"$ref":"#/$defs/wrapped"}`)
	root, ok := findDef(defs, "Root").(*AliasDef)
	if !ok {
		t.Fatalf("Root is %T, want an alias over the wrapper", findDef(defs, "Root"))
	}
	for _, got := range []struct {
		what string
		name string
	}{{"UnmarshalAs", root.UnmarshalAs}, {"MarshalAs", root.MarshalAs}, {"ValidateAs", root.ValidateAs}} {
		if got.name != "Wrapped" {
			t.Errorf("%s = %q, want %q: without it the alias decodes as a struct with no exported field", got.what, got.name, "Wrapped")
		}
	}
}

// TestContentOnlySchemaBecomesAStringWrapper covers issue #115. A schema whose
// only keyword is from the content vocabulary named no Go type, so it resolved
// to `any` -- and Go forbids methods on an interface, so the type had no
// Validate at all and json.Unmarshal into it could not fail.
//
// The narrowness is the point, and is why this is not "give it a string type":
// contentEncoding applies to strings and to nothing else, so a number satisfies
// it trivially. The wrapper's InferredGoType is a plain string and its Validate
// returns early for anything that did not decode into one, which is the same
// answer #106 gave a bare "format".
func TestContentOnlySchemaBecomesAStringWrapper(t *testing.T) {
	for _, tt := range []struct {
		name  string
		input string
	}{
		{"contentEncoding", `{"$schema":"http://json-schema.org/draft-07/schema#","contentEncoding":"base64"}`},
		{"contentMediaType", `{"$schema":"http://json-schema.org/draft-07/schema#","contentMediaType":"application/json"}`},
		{"both", `{"$schema":"http://json-schema.org/draft-07/schema#","contentEncoding":"base64","contentMediaType":"application/json"}`},
		{"contentSchema", `{"$schema":"https://json-schema.org/draft/2019-09/schema","contentMediaType":"application/json","contentSchema":{"type":"object"}}`},
		{"an encoding nothing decides", `{"$schema":"http://json-schema.org/draft-07/schema#","contentEncoding":"quoted-printable"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := findDef(generateOne(t, tt.input), "Root")
			def, ok := root.(*InferredAliasDef)
			if !ok {
				t.Fatalf("Root is %T for %s, want the string wrapper", root, tt.input)
			}
			if p, ok := def.InferredGoType.(*PrimitiveType); !ok || p.Name != "string" {
				t.Fatalf("Root holds %v, want a plain string: a content keyword says what a string must look like, not that the value is one", def.InferredGoType)
			}
		})
	}
}

// The dialect answers two separate questions about the content vocabulary, and
// this pins both.
//
// Does the dialect *have* the keywords? contentEncoding and contentMediaType
// arrived in draft 7, so drafts 3, 4 and 6 have never heard of them and a
// document writing one there is writing an unknown keyword -- which every draft
// says to ignore. Ignoring it means the schema states nothing at all, so the
// value is not a string either: {"contentEncoding":"base64"} declared as draft 4
// admits an object, an array and a number, and typing it as a string wrapper was
// a constraint invented out of a word the dialect has no meaning for. That half
// is settled in normalization, off schema.keywordDialects, before the generator
// looks (issue #203).
//
// Given that it has them, does the keyword assert or annotate? Only draft 7
// asserts. From 2019-09 the content vocabulary is annotation-only by definition
// -- the official suite marks {"contentEncoding":"base64"} satisfied by a string
// that is not base64 -- so a rule there would reject what the schema permits.
// That half is contentAssertsFor, and the 2019-09 and 2020-12 rows below are
// what tell the two questions apart: the keyword is read, the value is a string,
// and no check is emitted.
func TestContentAssertionFollowsTheDialect(t *testing.T) {
	for _, tt := range []struct {
		name    string
		schema  string
		defined bool
		asserts bool
	}{
		{"draft 7", `"http://json-schema.org/draft-07/schema#"`, true, true},
		{"2019-09", `"https://json-schema.org/draft/2019-09/schema"`, true, false},
		{"2020-12", `"https://json-schema.org/draft/2020-12/schema"`, true, false},
		{"draft 6", `"http://json-schema.org/draft-06/schema#"`, false, false},
		{"draft 4", `"http://json-schema.org/draft-04/schema#"`, false, false},
		{"draft 3", `"http://json-schema.org/draft-03/schema#"`, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := `{"$schema":` + tt.schema + `,"contentEncoding":"base64"}`
			root := findDef(generateOne(t, input), "Root")
			def, ok := root.(*InferredAliasDef)
			if ok != tt.defined {
				t.Fatalf("Root is %T (string wrapper = %v), want %v: a dialect that does not "+
					"define the keyword must not narrow the value to a string either", root, ok, tt.defined)
			}
			if !tt.defined {
				return
			}
			var hasContent bool
			for _, r := range def.Validations {
				if r.RuleType == "content" {
					hasContent = true
				}
			}
			if hasContent != tt.asserts {
				t.Fatalf("content rule emitted = %v, want %v: %s", hasContent, tt.asserts, input)
			}
		})
	}
}

// TestAllOfOverflowPositionGetsTheEvaluator covers issue #112. Two allOf
// branches each stating additionalProperties cannot be merged into one overflow
// value type -- satisfying both is an allOf of the two sub-schemas -- so the
// merge declines, and a position holding such a schema was typed map[string]any
// with no check anywhere.
//
// The evaluator rather than a widened merge: the typed overflow map reads
// {"additionalProperties":{"minimum":5}} as map[string]float64, and a string
// value then fails to decode though the schema admits it. The accept-controls
// are the shapes the merge does express, which must keep the types they had.
func TestAllOfOverflowPositionGetsTheEvaluator(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"two":  {"type":"object","allOf":[{"additionalProperties":{"minimum":5}},{"additionalProperties":{"maximum":9}}]},
			"sole": {"type":"object","allOf":[{"additionalProperties":{"minimum":5}}]},
			"keyed":{"type":"object","allOf":[{"properties":{"a":{"type":"string"}}},{"additionalProperties":{"minimum":5}},{"additionalProperties":{"maximum":9}}]}
		}
	}`)

	two := fieldNamedJSON(t, structNamed(t, ir, "Doc"), "two")
	name := two.Type.GoTypeName()
	if _, ok := findDef(ir.TypeDefs, name).(*AnnotationSchemaDef); !ok {
		t.Fatalf("two is %q, want the runtime-evaluator wrapper: map[string]any carries neither branch's bound", name)
	}

	// The merge expresses a lone branch exactly, and its typed overflow map is
	// a better Go type than a raw wrapper. Taking it over would be a loss.
	sole := fieldNamedJSON(t, structNamed(t, ir, "Doc"), "sole")
	soleDef, ok := findDef(ir.TypeDefs, namedTypeName(sole.Type)).(*StructDef)
	if !ok || soleDef.AdditionalProperties == nil {
		t.Fatalf("sole is %q, want the struct with a typed overflow map the merge already produced", sole.Type.GoTypeName())
	}

	// A branch naming a key produces a struct for some other reason, and the
	// per-branch overflow checks (#101) are what carry the keyword there.
	keyed := fieldNamedJSON(t, structNamed(t, ir, "Doc"), "keyed")
	keyedDef, ok := findDef(ir.TypeDefs, namedTypeName(keyed.Type)).(*StructDef)
	if !ok {
		t.Fatalf("keyed is %q, want the merged struct", keyed.Type.GoTypeName())
	}
	if len(keyedDef.BranchOverflowChecks) == 0 {
		t.Fatal("keyed lost its per-branch overflow checks")
	}
}
