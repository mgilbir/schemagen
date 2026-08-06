package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// subschemaDepthFixtures are the two keywords whose argument is a sub-schema the
// static extractor read only in part -- `propertyNames` (issue #180) and
// `dependentSchemas` (issue #181) -- together with the controls that tell a fix
// from an amputation.
//
// Both defects have the same shape and the same symptom: the extractor reads a
// hand-picked list of scalar fields off the sub-schema and had no gate asking
// whether it stated anything else, so a sub-schema factored into `$defs` and
// reached by `$ref` was read as one stating nothing, and the keyword asserted
// nothing at all. Every schema below is written twice where it can be -- once
// through a reference and once inline -- because the inline spelling worked
// throughout and is what makes the reference spelling's silence visible.
//
// They are run compiled rather than compared against a golden for the reason the
// `not` fixtures are: the symptom is a Validate that accepts, which reads exactly
// like a schema with nothing to check. The IR shows nothing either -- the missing
// constraint is a nil field.
func subschemaDepthFixtures() []notFixture {
	return []notFixture{
		{
			// Issue #180 in the shape the issue reports it: propertyNames whose
			// sub-schema is a bare $ref. The extractor read `maxLength` off the
			// referring schema, which does not state it, and produced no
			// constraint at all.
			Name:       "propertynames_via_ref",
			SchemaPath: "testdata/schemas/regression/propertynames_via_ref.json",
			Instances: []notInstance{
				{Name: "long name", Doc: `{"toolong":1}`, Valid: false,
					Why: "the referenced sub-schema caps a name at three characters; accepting this is issue #180"},
				{Name: "short name", Doc: `{"ok":1}`, Valid: true,
					Why: "the constraint must not swallow the names the schema permits"},
				{Name: "no names", Doc: `{}`, Valid: true,
					Why: "propertyNames says nothing about an object with no keys"},
			},
		},
		{
			// Issue #181 in the shape the issue reports it.
			Name:       "dependentschemas_via_ref",
			SchemaPath: "testdata/schemas/regression/dependentschemas_via_ref.json",
			Instances: []notInstance{
				{Name: "trigger without dependency", Doc: `{"card":"x"}`, Valid: false,
					Why: "the referenced branch requires \"cvv\" once \"card\" is present; accepting this is issue #181"},
				{Name: "trigger with dependency", Doc: `{"card":"x","cvv":"123"}`, Valid: true,
					Why: "the branch is satisfied; a fix that rejects this is an amputation"},
				{Name: "no trigger", Doc: `{}`, Valid: true,
					Why: "a dependent branch applies only when its trigger key is present"},
			},
		},
		{
			// The neighbouring spellings of a propertyNames sub-schema, each at
			// the same position so that one arm and not two decides each case.
			Name:       "propertynames_subschema_shapes",
			SchemaPath: "testdata/schemas/regression/propertynames_subschema_shapes.json",
			Instances: []notInstance{
				{Name: "ref rejects", Doc: `{"viaRef":{"toolong":1}}`, Valid: false,
					Why: "a $ref to a $defs entry, which is how the sub-schema is normally written"},
				{Name: "ref accepts", Doc: `{"viaRef":{"ok":1}}`, Valid: true, Why: "control for the above"},

				{Name: "ref chain rejects", Doc: `{"viaRefChain":{"toolong":1}}`, Valid: false,
					Why: "a $ref to a $defs entry that is itself a $ref"},
				{Name: "ref chain accepts", Doc: `{"viaRefChain":{"ok":1}}`, Valid: true, Why: "control for the above"},

				{Name: "inline rejects", Doc: `{"inline":{"toolong":1}}`, Valid: false,
					Why: "control: the spelling that already worked must go on working"},
				{Name: "inline accepts", Doc: `{"inline":{"ok":1}}`, Valid: true, Why: "control for the above"},

				{Name: "allOf rejects on length", Doc: `{"composed":{"abcd":1}}`, Valid: false,
					Why: "both branches of the allOf bind; this one is too long"},
				{Name: "allOf rejects on pattern", Doc: `{"composed":{"bc":1}}`, Valid: false,
					Why: "short enough, but the second branch demands a leading \"a\""},
				{Name: "allOf accepts", Doc: `{"composed":{"ab":1}}`, Valid: true, Why: "control for the two above"},

				{Name: "anyOf rejects", Doc: `{"alternated":{"gamma":1}}`, Valid: false,
					Why: "no branch of the anyOf names this key"},
				{Name: "anyOf accepts", Doc: `{"alternated":{"beta":1}}`, Valid: true, Why: "control for the above"},

				{Name: "oneOf rejects on both", Doc: `{"exclusive":{"zz":1}}`, Valid: false,
					Why: "short and leading \"z\": two branches match, so \"exactly one\" fails"},
				{Name: "oneOf rejects on neither", Doc: `{"exclusive":{"abcd":1}}`, Valid: false,
					Why: "too long and no leading \"z\": no branch matches"},
				{Name: "oneOf accepts first", Doc: `{"exclusive":{"ab":1}}`, Valid: true, Why: "control: the first branch alone"},
				{Name: "oneOf accepts second", Doc: `{"exclusive":{"zzzz":1}}`, Valid: true, Why: "control: the second branch alone"},

				{Name: "not rejects", Doc: `{"negated":{"forbidden":1}}`, Valid: false,
					Why: "the negation names the one key the schema forbids"},
				{Name: "not accepts", Doc: `{"negated":{"fine":1}}`, Valid: true, Why: "control for the above"},

				{Name: "enum rejects", Doc: `{"enumerated":{"c":1}}`, Valid: false,
					Why: "control: a string enum was already read, and must go on being read"},
				{Name: "enum accepts", Doc: `{"enumerated":{"a":1}}`, Valid: true, Why: "control for the above"},

				{Name: "const rejects", Doc: `{"constNamed":{"other":1}}`, Valid: false,
					Why: "control: a string const was already read"},
				{Name: "const accepts", Doc: `{"constNamed":{"only":1}}`, Valid: true, Why: "control for the above"},

				{Name: "pattern rejects", Doc: `{"patterned":{"qx":1}}`, Valid: false,
					Why: "control: a pattern was already read"},
				{Name: "pattern accepts", Doc: `{"patterned":{"px":1}}`, Valid: true, Why: "control for the above"},

				{Name: "non-string enum rejects any key", Doc: `{"nonStringEnum":{"a":1}}`, Valid: false,
					Why: "the enum holds two numbers and a property name is a string, so no key qualifies; the extractor kept the string members only and this enum has none, which left it enforcing nothing"},
				{Name: "non-string enum accepts the empty object", Doc: `{"nonStringEnum":{}}`, Valid: true,
					Why: "control: the schema admits an object with no keys and nothing else"},

				{Name: "type rejects any key", Doc: `{"typedName":{"a":1}}`, Valid: false,
					Why: "the sub-schema demands an integer and a property name is a string; `type` was read by nothing"},
				{Name: "type accepts the empty object", Doc: `{"typedName":{}}`, Valid: true, Why: "control for the above"},

				{Name: "non-string const rejects any key", Doc: `{"constNonString":{"a":1}}`, Valid: false,
					Why: "the const is a number and a property name is a string, so no key qualifies; the extractor promotes a const to a one-member enum and then keeps the string members only, which left this enforcing nothing"},
				{Name: "non-string const accepts the empty object", Doc: `{"constNonString":{}}`, Valid: true, Why: "control for the above"},

				{Name: "const beside enum narrows the enum", Doc: `{"constBesideEnum":{"b":1}}`, Valid: false,
					Why: "the const and the enum are conjuncts, so only \"a\" satisfies both; the extractor reads the const only when no enum stands beside it, and this key passed the enum alone"},
				{Name: "const beside enum accepts the name both admit", Doc: `{"constBesideEnum":{"a":1}}`, Valid: true, Why: "control for the above"},

				{Name: "ref beside a read keyword rejects on the ref", Doc: `{"lengthBeside":{"toolong":1}}`, Valid: false,
					Why: "the $ref caps the length; the minLength written beside it does not, and reading only that left the cap unenforced"},
				{Name: "ref beside a read keyword rejects on the sibling", Doc: `{"lengthBeside":{"a":1}}`, Valid: false,
					Why: "the sibling minLength must survive the sub-schema being taken over"},
				{Name: "ref beside a read keyword accepts", Doc: `{"lengthBeside":{"ok":1}}`, Valid: true, Why: "control for the two above"},

				{Name: "format annotates rather than asserts here", Doc: `{"formatAnnotation":{"not an address":1}}`, Valid: true,
					Why: "2020-12 puts `format` in the annotation vocabulary, so this key is permitted; the draft-07 fixture is where it asserts"},

				{Name: "unconstrained object", Doc: `{}`, Valid: true,
					Why: "every property is optional and none is present"},
			},
		},
		{
			// The `format` half of #180, which needs a dialect that asserts
			// `format` to be a defect at all. A property name is a string in
			// every draft, so the keyword's subject needs no inference here --
			// and it was read by nothing.
			//
			// It is checked statically rather than through the evaluator, which
			// deliberately models no `format` at all (see validatorKeywords). That
			// is why the gate names the keyword as read: sending it to the
			// evaluator would have the evaluator decline the whole sub-schema over
			// it and leave the keyword exactly where it was.
			Name:       "propertynames_format_draft7",
			SchemaPath: "testdata/schemas/regression/propertynames_format_draft7.json",
			Instances: []notInstance{
				{Name: "format rejects", Doc: `{"emailNames":{"not an address":1}}`, Valid: false,
					Why: "draft-07 asserts `format`, so a key that is not an e-mail address is refused"},
				{Name: "format accepts", Doc: `{"emailNames":{"a@b.com":1}}`, Valid: true, Why: "control for the above"},
				{Name: "format says nothing about no keys", Doc: `{"emailNames":{}}`, Valid: true,
					Why: "control: a constraint on names says nothing about an object with none"},

				{Name: "format beside a length rejects on the format", Doc: `{"boundedEmail":{"not an addr":1}}`, Valid: false,
					Why: "eleven characters, so the length passes and only the format can refuse it"},
				{Name: "format beside a length rejects on the length", Doc: `{"boundedEmail":{"someone@example.com":1}}`, Valid: false,
					Why: "a valid address of nineteen characters; the length must survive the format being read"},
				{Name: "format beside a length accepts", Doc: `{"boundedEmail":{"a@b.com":1}}`, Valid: true, Why: "control for the two above"},
			},
		},
		{
			// The neighbouring spellings of a dependentSchemas branch. Each
			// trigger is a separate branch of the same keyword on the same
			// schema object, so a document naming one trigger exercises that
			// branch and no other.
			Name:       "dependentschemas_subschema_shapes",
			SchemaPath: "testdata/schemas/regression/dependentschemas_subschema_shapes.json",
			Instances: []notInstance{
				{Name: "ref rejects", Doc: `{"card":"x"}`, Valid: false,
					Why: "a $ref to a $defs entry, which is how the branch is normally written"},
				{Name: "ref accepts", Doc: `{"card":"x","cvv":"1"}`, Valid: true, Why: "control for the above"},

				{Name: "ref chain rejects", Doc: `{"chain":1}`, Valid: false,
					Why: "a $ref to a $defs entry that is itself a $ref"},
				{Name: "ref chain accepts", Doc: `{"chain":1,"cvv":"1"}`, Valid: true, Why: "control for the above"},

				{Name: "inline required rejects", Doc: `{"inlineReq":1}`, Valid: false,
					Why: "control: the spelling that already worked must go on working"},
				{Name: "inline required accepts", Doc: `{"inlineReq":1,"cvv":"1"}`, Valid: true, Why: "control for the above"},

				{Name: "allOf rejects", Doc: `{"composed":1,"a":2}`, Valid: false,
					Why: "both branches bind and the second demands \"b\""},
				{Name: "allOf accepts", Doc: `{"composed":1,"a":2,"b":3}`, Valid: true, Why: "control for the above"},

				{Name: "anyOf rejects", Doc: `{"alternated":1}`, Valid: false,
					Why: "neither branch is satisfied"},
				{Name: "anyOf accepts", Doc: `{"alternated":1,"q":2}`, Valid: true, Why: "control for the above"},

				{Name: "not rejects", Doc: `{"negated":1,"banned":2}`, Valid: false,
					Why: "the branch forbids the document that carries \"banned\""},
				{Name: "not accepts", Doc: `{"negated":1}`, Valid: true, Why: "control for the above"},

				{Name: "if/then rejects", Doc: `{"conditional":1,"x":2}`, Valid: false,
					Why: "the condition holds, so the consequence demands \"y\""},
				{Name: "if/then accepts on the consequence", Doc: `{"conditional":1,"x":2,"y":3}`, Valid: true, Why: "control for the above"},
				{Name: "if/then accepts on the condition", Doc: `{"conditional":1}`, Valid: true,
					Why: "control: the condition fails, so the consequence never applies"},

				{Name: "nested dependentSchemas rejects", Doc: `{"nested":1,"inner":2}`, Valid: false,
					Why: "the branch is itself a dependentSchemas, triggered by \"inner\""},
				{Name: "nested dependentSchemas accepts", Doc: `{"nested":1,"inner":2,"deep":3}`, Valid: true, Why: "control for the above"},
				{Name: "nested dependentSchemas untriggered", Doc: `{"nested":1}`, Valid: true,
					Why: "control: the inner trigger is absent"},

				{Name: "maxProperties rejects", Doc: `{"bounded":1,"x":2,"y":3}`, Valid: false,
					Why: "control: a property count was already read, and must go on being read"},
				{Name: "maxProperties accepts", Doc: `{"bounded":1}`, Valid: true, Why: "control for the above"},

				{Name: "properties rejects", Doc: `{"shaped":1,"amount":"not a number"}`, Valid: false,
					Why: "control: a branch naming a property's type was already read"},
				{Name: "properties accepts", Doc: `{"shaped":1,"amount":2}`, Valid: true, Why: "control for the above"},

				{Name: "closed branch permits a pattern key", Doc: `{"keyed":1,"p_x":2}`, Valid: true,
					Why: "the branch's additionalProperties:false is relative to its patternProperties as well as its properties; the allowed-key list was built from `properties` alone, so this was refused -- a rejection of a document the schema permits"},
				{Name: "closed branch permits its own property", Doc: `{"keyed":1}`, Valid: true, Why: "control for the above"},
				{Name: "closed branch refuses an unnamed key", Doc: `{"keyed":1,"zzz":2}`, Valid: false,
					Why: "control: no property and no pattern names this key, so the branch must still refuse it"},

				{Name: "branch property states items rejects", Doc: `{"listed":1,"arr":["x"]}`, Valid: false,
					Why: "the branch says arr holds integers; the per-property reading drops `items`, so the branch had to go to the evaluator whole"},
				{Name: "branch property states items accepts", Doc: `{"listed":1,"arr":[2]}`, Valid: true, Why: "control for the above"},

				{Name: "branch property states two types rejects", Doc: `{"multi":1,"val":true}`, Valid: false,
					Why: "a type union is a keyword the per-property check names and cannot carry -- it emits one type or none, so the whole assertion was dropped"},
				{Name: "branch property states two types accepts a string", Doc: `{"multi":1,"val":"s"}`, Valid: true, Why: "control for the above"},
				{Name: "branch property states two types accepts an integer", Doc: `{"multi":1,"val":2}`, Valid: true, Why: "control for the above"},

				{Name: "schema-valued additionalProperties rejects", Doc: `{"valued":1,"x":"s"}`, Valid: false,
					Why: "the branch types every key it does not name; only the boolean false spelling was read, so a schema-valued one asserted nothing"},
				{Name: "schema-valued additionalProperties accepts", Doc: `{"valued":1,"x":2}`, Valid: true, Why: "control for the above"},

				{Name: "no trigger", Doc: `{}`, Valid: true,
					Why: "no branch applies to a document naming none of the triggers"},
			},
		},
		{
			// The draft where a $ref replaces what stands beside it. The branch
			// means its target and nothing else, so the target's constraint has
			// to bind and the siblings must not -- and the static reading did the
			// opposite of both, keeping the branch's own `required` while the
			// target went unread. That is a false accept and a false reject in
			// the same branch.
			Name:       "dependentschemas_branch_ref_draft7",
			SchemaPath: "testdata/schemas/regression/dependentschemas_branch_ref_draft7.json",
			Instances: []notInstance{
				{Name: "sibling required does not bind", Doc: `{"alpha":"s"}`, Valid: true,
					Why: "under draft-07 the $ref replaces the branch, so the `required` written beside it says nothing; enforcing it refused a document the schema permits"},
				{Name: "sibling properties does not bind", Doc: `{"alpha":"s","bravo":1}`, Valid: true,
					Why: "the same for the branch's own `properties`: the minimum of 5 is not part of what the branch says"},
				{Name: "the target binds", Doc: `{"alpha":"s","x":1,"y":2}`, Valid: false,
					Why: "the target caps the object at two properties, and that is the whole of what the branch says"},
				{Name: "no trigger", Doc: `{"x":1,"y":2,"z":3}`, Valid: true,
					Why: "control: the cap is the branch's, and the branch needs its trigger"},
			},
		},
	}
}

// TestSubschemaKeywordsAreReadWhole compiles each fixture and puts every document
// to the generated type.
func TestSubschemaKeywordsAreReadWhole(t *testing.T) {
	for _, fx := range subschemaDepthFixtures() {
		t.Run(fx.Name, func(t *testing.T) {
			generated := generateFromSchemaWithConfig(t, fx.SchemaPath, generator.Config{
				PackageName:  "testpkg",
				OmitEmpty:    true,
				RootTypeName: "Root",
			})
			const rootType = "Root"

			tmpDir := t.TempDir()
			generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
			if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
				t.Fatalf("writing types.go: %v", err)
			}
			writeSharedHelpers(t, tmpDir, generatedMain)

			mainGo, err := notInstanceMain(rootType, fx.Instances)
			if err != nil {
				t.Fatalf("building main.go: %v", err)
			}
			if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
				t.Fatalf("writing main.go: %v", err)
			}
			if err := writeTestGoMod(tmpDir, "subschema_depth_test"); err != nil {
				t.Fatalf("writing go.mod: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "run", ".")
			cmd.Dir = tmpDir
			out, runErr := cmd.CombinedOutput()
			text := programOutput(out)
			if runErr != nil || text != "PASS" {
				t.Fatalf("%s:\n%s", fx.SchemaPath, text)
			}
		})
	}
}
