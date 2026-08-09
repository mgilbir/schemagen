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

	"github.com/mgilbir/schemagen/pkg/generator"
)

// notInstance is one document put to a generated type, and the verdict the
// schema gives it.
type notInstance struct {
	Name  string
	Doc   string
	Valid bool
	// Why says what the case is for, and is printed on failure. A verdict with
	// no reason attached is one the next reader has to re-derive from the schema.
	Why string
}

type notFixture struct {
	Name       string
	SchemaPath string
	Instances  []notInstance
}

// notRegressionFixtures are the two failure directions of the same weak handling
// of "not", one fixture group each, plus the controls that tell a fix from an
// amputation.
//
// Both groups are run the same way and for the same reason: the defects were
// invisible in the IR and in the emitted source, and only showed when a document
// was put to the compiled type. #177 emitted a Validate that read `return nil`,
// which looks like a schema with nothing to check; #185 emitted a check that
// looks right and rejects too much. Neither can be caught by comparing generated
// text to a golden, so these compile and run.
func notRegressionFixtures() []notFixture {
	return []notFixture{
		{
			// Issue #177, in the shape the issue reports it: a `not` written
			// beside a `type` at the document root. The type arm answered
			// `type RootTypedNot string`, the negation went nowhere, and
			// Validate was `return nil`.
			Name:       "not_beside_type_at_root",
			SchemaPath: "testdata/schemas/regression/not_beside_type_at_root.json",
			Instances: []notInstance{
				{Name: "forbidden", Doc: `"forbidden"`, Valid: false,
					Why: "the const the `not` names; accepting it is issue #177"},
				{Name: "permitted", Doc: `"allowed"`, Valid: true,
					Why: "the negation must not swallow the rest of the string type"},
				{Name: "wrong type", Doc: `42`, Valid: false,
					Why: "the `type` sibling still binds; a fix that keeps only the negation loses it"},
			},
		},
		{
			// The half of #177 that no evaluator is needed for: a `not` over
			// accept-all admits nothing, and a sibling cannot widen that. The
			// property spelling of this is already caught by forbiddingInlineType,
			// so the root is where the guard has to stand.
			Name:       "not_over_accept_all_beside_type",
			SchemaPath: "testdata/schemas/regression/not_over_accept_all_beside_type.json",
			Instances: []notInstance{
				{Name: "string", Doc: `"anything"`, Valid: false,
					Why: "the schema admits no instance at all; `type Root string` accepted every one"},
				{Name: "number", Doc: `1`, Valid: false, Why: "nor any other JSON kind"},
			},
		},
		{
			// The same defect in every position a sibling-bearing `not` can be
			// written. Each of these is a separate arm of the generator, and
			// each one typed the value from the sibling and dropped the
			// negation.
			Name:       "not_beside_sibling_keywords",
			SchemaPath: "testdata/schemas/regression/not_beside_sibling_keywords.json",
			Instances: []notInstance{
				{Name: "typed property rejects", Doc: `{"typed":"forbidden"}`, Valid: false,
					Why: "a property whose schema is a type beside a `not`"},
				{Name: "typed property accepts", Doc: `{"typed":"fine"}`, Valid: true, Why: "control for the above"},

				{Name: "object shape rejects", Doc: `{"objectShape":{"a":"x"}}`, Valid: false,
					Why: "the issue's second example: `not`:{required} beside `properties`"},
				{Name: "object shape accepts", Doc: `{"objectShape":{"b":1}}`, Valid: true, Why: "control for the above"},

				{Name: "ref rejects", Doc: `{"viaRef":"forbidden"}`, Valid: false,
					Why: "the same subschema behind a $ref, which was already right and must stay right"},
				{Name: "ref accepts", Doc: `{"viaRef":"fine"}`, Valid: true, Why: "control for the above"},

				{Name: "element rejects", Doc: `{"element":["forbidden"]}`, Valid: false,
					Why: "an array element written inline"},
				{Name: "element accepts", Doc: `{"element":["fine"]}`, Valid: true, Why: "control for the above"},

				{Name: "map value rejects", Doc: `{"mapValue":{"k":"forbidden"}}`, Valid: false,
					Why: "a map value, which reaches resolveType by its own route"},
				{Name: "map value accepts", Doc: `{"mapValue":{"k":"fine"}}`, Valid: true, Why: "control for the above"},

				{Name: "tuple slot rejects", Doc: `{"tupleSlot":["forbidden"]}`, Valid: false,
					Why: "a prefixItems position, whose check is written by tupleItemDefFor"},
				{Name: "tuple slot accepts", Doc: `{"tupleSlot":["fine"]}`, Valid: true, Why: "control for the above"},

				{Name: "enum sibling rejects", Doc: `{"enumSibling":"b"}`, Valid: false,
					Why: "a `not` beside an enum, which the enum arm claimed before any negation was read"},
				{Name: "enum sibling accepts", Doc: `{"enumSibling":"a"}`, Valid: true, Why: "control for the above"},
				{Name: "enum sibling still bounds", Doc: `{"enumSibling":"z"}`, Valid: false,
					Why: "the enum must survive the negation being carried"},

				{Name: "empty not forbids", Doc: `{"forbiddenByEmptyNot":"anything"}`, Valid: false,
					Why: "a `not` over accept-all admits nothing whatever stands beside it"},
				{Name: "empty not absent", Doc: `{}`, Valid: true,
					Why: "the property is optional; a constraint applies to a value that is there"},
			},
		},
		{
			// The element and map-value positions with the collection as the
			// whole document. They are not the same route as the properties of
			// the group above: nested one level, either of two arms answers and
			// disabling one alone changes nothing, so a guard written only in the
			// nested spelling passes with either arm broken. These two reach one
			// arm each.
			Name:       "not_beside_type_in_items",
			SchemaPath: "testdata/schemas/regression/not_beside_type_in_items.json",
			Instances: []notInstance{
				{Name: "forbidden element", Doc: `["forbidden"]`, Valid: false,
					Why: "the element schema's `not`, which []string had nowhere to carry"},
				{Name: "permitted element", Doc: `["fine"]`, Valid: true, Why: "control for the above"},
			},
		},
		{
			Name:       "not_beside_type_in_map_values",
			SchemaPath: "testdata/schemas/regression/not_beside_type_in_map_values.json",
			Instances: []notInstance{
				{Name: "forbidden value", Doc: `{"k":"forbidden"}`, Valid: false,
					Why: "the value schema's `not`, which map[string]string had nowhere to carry"},
				{Name: "permitted value", Doc: `{"k":"fine"}`, Valid: true, Why: "control for the above"},
			},
		},
		{
			// Issue #185: a keyword dropped from inside the `not` widens the
			// negation, so these are false rejections. The last two are the
			// controls -- an operand that really does state nothing but its type
			// must still forbid that type, or the fix is an amputation.
			Name:       "not_operand_vocabulary",
			SchemaPath: "testdata/schemas/regression/not_operand_vocabulary.json",
			Instances: []notInstance{
				{Name: "patternProperties permits", Doc: `{"patternProps":{"xa":"s"}}`, Valid: true,
					Why: "the operand demands an integer at ^x, this is a string, so the operand fails and the negation holds"},
				{Name: "patternProperties forbids", Doc: `{"patternProps":{"xa":1}}`, Valid: false,
					Why: "the operand matches, so the negation must reject"},
				{Name: "patternProperties permits non-object", Doc: `{"patternProps":"q"}`, Valid: true,
					Why: "the operand is object-typed and says nothing about a string"},

				{Name: "branch required permits", Doc: `{"branchRequired":{"a":1}}`, Valid: true,
					Why: "the branch requires \"b\", which is absent, so the branch fails and the negation holds"},
				{Name: "branch required forbids", Doc: `{"branchRequired":{"a":1,"b":2}}`, Valid: false,
					Why: "the branch matches, so the negation must reject"},

				{Name: "type-only operand still forbids", Doc: `{"typeOnly":"s"}`, Valid: false,
					Why: "control: {\"not\":{\"type\":\"string\"}} must go on refusing every string"},
				{Name: "type-only operand permits others", Doc: `{"typeOnly":1}`, Valid: true, Why: "control for the above"},

				{Name: "type-only branch still forbids", Doc: `{"typeOnlyBranch":true}`, Valid: false,
					Why: "control: a branch whose whole content is its type must go on negating"},
				{Name: "type-only branch permits others", Doc: `{"typeOnlyBranch":1}`, Valid: true, Why: "control for the above"},
			},
		},
		{
			// The `format` half of #185, which needs a dialect that asserts
			// `format` to be a defect at all -- under 2020-12 the keyword is an
			// annotation and {"not":{"type":"string","format":"email"}} really
			// does forbid every string.
			//
			// schemagen does not model `format` inside a negation, so the
			// negation is declined outright rather than negated on the `type`
			// alone: the property accepts everything, which under-enforces, and
			// the generated source says so. The rejection direction is not
			// asserted here because there is nothing to assert -- what this
			// pins is that the schema no longer refuses what it permits.
			Name:       "not_operand_format_draft7",
			SchemaPath: "testdata/schemas/regression/not_operand_format_draft7.json",
			Instances: []notInstance{
				{Name: "plain string permitted", Doc: `{"emailNot":"just a plain string"}`, Valid: true,
					Why: "the schema plainly permits it; refusing it is issue #185"},
				{Name: "number permitted", Doc: `{"emailNot":42}`, Valid: true,
					Why: "the operand is string-typed and says nothing about a number"},
				{Name: "control still forbids strings", Doc: `{"plainNot":"anything"}`, Valid: false,
					Why: "control: {\"not\":{\"type\":\"string\"}} beside it must go on refusing, or the fix is an amputation"},
				{Name: "control permits others", Doc: `{"plainNot":42}`, Valid: true, Why: "control for the above"},

				{Name: "branch format permits a long non-address", Doc: `{"branchFormat":"abcd"}`, Valid: true,
					Why: "the branch forbids strings that are three characters or more AND an e-mail address; read with the format dropped it forbade every string of three characters or more"},
				{Name: "branch format permits a short string", Doc: `{"branchFormat":"ab"}`, Valid: true,
					Why: "control: too short for the branch either way"},
			},
		},
	}
}

// TestNotIsEnforcedAndDoesNotOverNegate compiles each fixture and puts every
// document to the generated type.
//
// Reading the generated source is not enough for either defect and was how both
// survived: #177's symptom is a Validate that reads `return nil`, which is what
// a schema with nothing to check produces too, and #185's is a check that reads
// correctly and fires on more values than the schema names.
func TestNotIsEnforcedAndDoesNotOverNegate(t *testing.T) {
	runInstanceFixtures(t, "not_regression_test", notRegressionFixtures())
}

// runInstanceFixtures compiles each fixture's schema and puts every document in
// it to the generated root type, reporting the ones whose verdict disagrees.
//
// One runner for every group of fixtures written this way. The three that exist
// differed only in the module name written into the throwaway go.mod, and a
// fourth copy is how the next one starts drifting from the rest.
//
// module names the temporary module the compiled program lives in. It has no
// effect on the verdict and exists so that a failure names which group's
// throwaway directory it came from.
func runInstanceFixtures(t *testing.T, module string, fixtures []notFixture) {
	t.Helper()
	for _, fx := range fixtures {
		t.Run(fx.Name, func(t *testing.T) {
			// The root type is named rather than recovered from the emitted
			// source. extractRootTypeName looks for the last top-level struct,
			// and a root that is a slice or a map does not declare one -- it
			// answered with the *element* wrapper for the items fixture here,
			// which put every document to the wrong type and reported a control
			// case failing for a reason that had nothing to do with the schema.
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
			if err := writeTestGoMod(tmpDir, module); err != nil {
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

// notInstanceMain writes the program that puts each document to the type.
//
// The Validate call goes through a type assertion rather than a direct call
// because a schema schemagen cannot compile resolves to `type X any`, which Go
// forbids methods on. A direct call would not compile, and a fixture group would
// then fail to build rather than report which document it disagrees about --
// and the `format` group is exactly such a schema, deliberately.
func notInstanceMain(rootType string, instances []notInstance) (string, error) {
	var b strings.Builder
	b.WriteString(`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type notCase struct {
	name  string
	doc   string
	valid bool
	why   string
}

func main() {
	cases := []notCase{
`)
	for _, in := range instances {
		if !json.Valid([]byte(in.Doc)) {
			return "", fmt.Errorf("case %q: %s is not valid JSON", in.Name, in.Doc)
		}
		fmt.Fprintf(&b, "\t\t{name: %s, doc: %s, valid: %t, why: %s},\n",
			goQuote(in.Name), goQuote(in.Doc), in.Valid, goQuote(in.Why))
	}
	fmt.Fprintf(&b, `	}

	var errs []string
	for _, c := range cases {
		var v %s
		err := json.Unmarshal([]byte(c.doc), &v)
		if err == nil {
			if val, ok := any(v).(interface{ Validate() error }); ok {
				err = val.Validate()
			}
		}
		accepted := err == nil
		if accepted != c.valid {
			errs = append(errs, fmt.Sprintf("%%s: %%s accepted=%%v want=%%v err=%%v (%%s)",
				c.name, c.doc, accepted, c.valid, err, c.why))
		}
	}
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "FAIL: %%s\n", e)
		}
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`, rootType)
	return b.String(), nil
}

// goQuote renders s as a Go string literal. strconv.Quote would do, but the
// documents here are JSON and reading a doubly-escaped one in a failure message
// is what this avoids: a raw string literal keeps them legible, and the fallback
// is only reached by a document containing a backquote.
func goQuote(s string) string {
	if !strings.ContainsAny(s, "`\n\r") {
		return "`" + s + "`"
	}
	return fmt.Sprintf("%q", s)
}
