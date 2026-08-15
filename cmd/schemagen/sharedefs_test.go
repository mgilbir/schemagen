package schemagen

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The tests in this file are about two documents of one Go package that each
// define a $defs entry of the same name. Nothing single-document can see any of
// it: the name registry is per package, and with one document there is nothing
// for a name to collide with. Issue #249.
//
// The behavioural ones generate through the CLI, compile the result and *run*
// it. Reading the emitted text is not enough here -- the defect emitted a
// package that compiled perfectly and typed one document's property with
// another document's schema, which only shows up when a document is put through
// it.

// rootInstance is a document, the root type it is to be decoded into, and the
// verdict that root's schema gives it. remarshal, when set, is what the decoded
// value must marshal back to: a type built from the wrong schema invents the
// properties that schema declares, and the invention is visible nowhere else.
type rootInstance struct {
	rootType  string
	doc       string
	valid     bool
	remarshal string
}

// writeSchemas writes the given name→body pairs into a fresh directory and
// returns the directory and the paths, in the order named.
func writeSchemas(t *testing.T, namedBodies ...string) (string, []string) {
	t.Helper()
	if len(namedBodies)%2 != 0 {
		t.Fatal("writeSchemas takes name, body pairs")
	}
	dir := t.TempDir()
	var paths []string
	for i := 0; i < len(namedBodies); i += 2 {
		path := filepath.Join(dir, namedBodies[i])
		writeFile(t, path, namedBodies[i+1])
		paths = append(paths, path)
	}
	return dir, paths
}

// generateCompileRunRoots is generateCompileRun for a set of instances that name
// their own root type, which is what a two-document fixture needs: the whole
// question is whether each document's property carries its own document's
// schema, and that cannot be asked through one root.
func generateCompileRunRoots(t *testing.T, argsFor func(modRoot string) []string, importPath string, instances []rootInstance) string {
	t.Helper()

	dir := t.TempDir()
	stderr, err := runGenerateCapturing(t, argsFor(dir)...)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}

	var body strings.Builder
	for i, in := range instances {
		fmt.Fprintf(&body, `
	{
		var v%d root.%s
		check(%q, %q, %t, %q, json.Unmarshal([]byte(%q), &v%d), func() ([]byte, error) { return json.Marshal(v%d) }, func() error { return v%d.Validate() })
	}`, i, in.rootType, in.rootType, in.doc, in.valid, in.remarshal, in.doc, i, i, i)
	}

	driver := fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"

	root %q
)

var failed int

func check(rootType, doc string, valid bool, remarshal string, decodeErr error, marshal func() ([]byte, error), validate func() error) {
	err := decodeErr
	if err == nil {
		err = validate()
	}
	if got := err == nil; got != valid {
		failed++
		fmt.Printf("FAIL %%s %%s: want valid=%%t, got valid=%%t (err=%%v)\n", rootType, doc, valid, got, err)
		return
	}
	if err != nil || remarshal == "" {
		return
	}
	back, mErr := marshal()
	if mErr != nil {
		failed++
		fmt.Printf("FAIL %%s %%s: marshal: %%v\n", rootType, doc, mErr)
		return
	}
	if string(back) != remarshal {
		failed++
		fmt.Printf("FAIL %%s %%s: want remarshal %%s, got %%s\n", rootType, doc, remarshal, back)
	}
}

func main() {
%s
	if failed > 0 {
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`, importPath, body.String())

	writeFile(t, filepath.Join(dir, "main.go"), driver)
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n\ngo 1.23\n")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "PASS") {
		t.Fatalf("generated package did not carry each document's own definition: %v\n%s", err, out)
	}
	return stderr
}

// declaredTypeNames lists the `type X ...` names of every .go file under dir,
// sorted. Shared helper declarations are left out: they are the same in every
// run and say nothing about which document's definition landed where.
func declaredTypeNames(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, helperFileName) {
			return err
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, line := range strings.Split(string(src), "\n") {
			if rest, ok := strings.CutPrefix(line, "type "); ok {
				names = append(names, strings.Fields(rest)[0])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	return names
}

// ---------- the reproducer ----------

const alphaThingDoc = `{
	"title": "Alpha",
	"properties": {"thing": {"$ref": "#/$defs/Thing"}},
	"$defs": {"Thing": {
		"type": "object",
		"properties": {"k": {"type": "string"}, "v": {"type": "integer"}},
		"required": ["k"]
	}}
}`

const betaThingDoc = `{
	"title": "Beta",
	"properties": {"thing": {"$ref": "#/$defs/Thing"}},
	"$defs": {"Thing": {"type": "object", "properties": {"note": {"type": "string"}}}}
}`

// Each document's property must carry its own document's definition. Before the
// fix both carried whichever was generated first: Beta rejected
// {"thing":{"note":"hello"}} for a missing "k" that is not a Beta property at
// all, and marshalled it back as {"thing":{"k":"","note":"hello"}} -- a key the
// document never had.
func TestSharedTypesKeepsEachDocumentsOwnDefinition(t *testing.T) {
	_, paths := writeSchemas(t, "alpha.json", alphaThingDoc, "beta.json", betaThingDoc)

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types")
		},
		"example.com/m/gen",
		[]rootInstance{
			{"Beta", `{"thing":{"note":"hello"}}`, true, `{"thing":{"note":"hello"}}`},
			{"Beta", `{}`, true, `{}`},
			// Alpha keeps its own shape: "k" is required and "v" is an integer.
			{"Alpha", `{"thing":{"note":"hello"}}`, false, ""},
			{"Alpha", `{"thing":{"k":"x","v":1}}`, true, `{"thing":{"k":"x","v":1}}`},
			{"Alpha", `{"thing":{"k":"x","v":"no"}}`, false, ""},
		})
}

// The worse half of the same defect: when the two definitions are of different
// JSON types, a document valid per its own schema did not decode at all --
// "json: cannot unmarshal string into Go struct field".
func TestSharedTypesKeepsDefinitionsOfDifferentJSONTypesApart(t *testing.T) {
	_, paths := writeSchemas(t,
		"alpha.json", alphaThingDoc,
		"gamma.json", `{
			"title": "Gamma",
			"properties": {"thing": {"$ref": "#/$defs/Thing"}},
			"$defs": {"Thing": {"type": "string", "minLength": 2}}
		}`)

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types")
		},
		"example.com/m/gen",
		[]rootInstance{
			{"Gamma", `{"thing":"abc"}`, true, `{"thing":"abc"}`},
			{"Gamma", `{"thing":"a"}`, false, ""},
			{"Alpha", `{"thing":{"k":"x"}}`, true, `{"thing":{"k":"x"}}`},
		})
}

// The control that matters most, since the obvious fix renames everything: two
// documents whose definitions agree keep sharing one type. They are spelled
// differently on purpose -- different key order, different whitespace -- because
// the comparison has to be about what the definitions say and not about how they
// were typed.
func TestSharedTypesStillSharesIdenticalDefinitions(t *testing.T) {
	dir, paths := writeSchemas(t,
		"alpha.json", `{
			"title": "Alpha",
			"properties": {"thing": {"$ref": "#/$defs/Thing"}},
			"$defs": {"Thing": {
				"type": "object",
				"properties": {"k": {"type": "string"}, "v": {"type": "integer"}},
				"required": ["k"]
			}}
		}`,
		"beta.json", "{\"title\":\"Beta\",\n\"$defs\":{\"Thing\":{\"required\":[\"k\"],\n\t\"properties\":{\"v\":{\"type\":\"integer\"},\"k\":{\"type\":\"string\"}},\"type\":\"object\"}},\n  \"properties\":{\"thing\":{\"$ref\":\"#/$defs/Thing\"}}}")

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out, "-p", "gen", "--shared-types")...)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stderr, "do not describe the same type") {
		t.Errorf("identical definitions must not be split:\n%s", stderr)
	}
	got := declaredTypeNames(t, out)
	want := []string{"Alpha", "Beta", "Thing"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("declared types = %v, want %v (one shared Thing)", got, want)
	}
}

// A keyword the document's own dialect does not define is dropped before the
// comparison, so two documents that differ only in one still share. Draft 7 has
// no "$defs" and no "unevaluatedProperties"; the second document states the
// latter and the first does not.
func TestSharedTypesSharesDefinitionsThatDifferOnlyOutsideTheirDialect(t *testing.T) {
	dir, paths := writeSchemas(t,
		"alpha.json", `{
			"$schema": "http://json-schema.org/draft-07/schema#",
			"title": "Alpha",
			"properties": {"thing": {"$ref": "#/definitions/Thing"}},
			"definitions": {"Thing": {"type": "object", "properties": {"k": {"type": "string"}}}}
		}`,
		"beta.json", `{
			"$schema": "http://json-schema.org/draft-07/schema#",
			"title": "Beta",
			"properties": {"thing": {"$ref": "#/definitions/Thing"}},
			"definitions": {"Thing": {"type": "object", "properties": {"k": {"type": "string"}}, "unevaluatedProperties": false}}
		}`)

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out, "-p", "gen", "--shared-types")...)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	got := declaredTypeNames(t, out)
	want := []string{"Alpha", "Beta", "Thing"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("declared types = %v, want %v; stderr:\n%s", got, want, stderr)
	}
}

// Both claims are qualified, never only the later one, so the names the package
// exports are the same whichever order the inputs were listed in. Renaming only
// the loser is the trap issue #228 came out of.
func TestSharedTypesDefinitionNamesDoNotDependOnInputOrder(t *testing.T) {
	dir, paths := writeSchemas(t, "alpha.json", alphaThingDoc, "beta.json", betaThingDoc)

	forward := filepath.Join(dir, "forward")
	reverse := filepath.Join(dir, "reverse")
	if _, err := runGenerateCapturing(t, paths[0], paths[1], "-o", forward, "-p", "gen", "--shared-types"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := runGenerateCapturing(t, paths[1], paths[0], "-o", reverse, "-p", "gen", "--shared-types"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	forwardNames := strings.Join(declaredTypeNames(t, forward), ",")
	reverseNames := strings.Join(declaredTypeNames(t, reverse), ",")
	if forwardNames != reverseNames {
		t.Fatalf("type names depend on input order:\n  alpha beta: %s\n  beta alpha: %s", forwardNames, reverseNames)
	}
	if want := "Alpha,AlphaThing,Beta,BetaThing"; forwardNames != want {
		t.Errorf("declared types = %s, want %s", forwardNames, want)
	}
}

// Three documents, two of which agree. The name is settled for the whole group
// rather than per pair: splitting the odd one out and letting the other two keep
// the bare name would make the answer depend on which group happens to be the
// larger, and with two groups of two it would have no answer at all.
//
// The definition all three agree on stays one type, which is the same test as
// the control above and the reason the split is decided per name.
func TestSharedTypesSplitsAllClaimsOnAContestedName(t *testing.T) {
	body := func(title, thing string) string {
		return fmt.Sprintf(`{
			"title": %q,
			"properties": {"s": {"$ref": "#/$defs/Shared"}, "t": {"$ref": "#/$defs/Thing"}},
			"$defs": {
				"Shared": {"type": "object", "properties": {"id": {"type": "string"}}},
				"Thing": %s
			}
		}`, title, thing)
	}
	dir, paths := writeSchemas(t,
		"a.json", body("ADoc", `{"type": "object", "properties": {"k": {"type": "string"}}}`),
		"b.json", body("BDoc", `{"type": "object", "properties": {"k": {"type": "string"}}}`),
		"c.json", body("CDoc", `{"type": "integer"}`))

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out, "-p", "gen", "--shared-types")...)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	got := strings.Join(declaredTypeNames(t, out), ",")
	want := "ADoc,ADocThing,BDoc,BDocThing,CDoc,CDocThing,Shared"
	if got != want {
		t.Errorf("declared types = %s, want %s\nstderr:\n%s", got, want, stderr)
	}
}

// Two definitions can be identical and still describe different types, when what
// they reference differs. The agreement has to be transitive or the shared type
// is whichever document's target was generated first -- the same defect one
// level down.
func TestSharedTypesFollowsWhatADefinitionReferences(t *testing.T) {
	body := func(title, other string) string {
		return fmt.Sprintf(`{
			"title": %q,
			"properties": {"t": {"$ref": "#/$defs/Thing"}},
			"$defs": {
				"Thing": {"type": "object", "properties": {"o": {"$ref": "#/$defs/Other"}}},
				"Other": %s
			}
		}`, title, other)
	}
	dir, paths := writeSchemas(t,
		"a.json", body("ADoc", `{"type": "object", "properties": {"x": {"type": "string"}}, "required": ["x"]}`),
		"b.json", body("BDoc", `{"type": "object", "properties": {"y": {"type": "integer"}}}`))

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out, "-p", "gen", "--shared-types")...)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	got := strings.Join(declaredTypeNames(t, out), ",")
	want := "ADoc,ADocOther,ADocThing,BDoc,BDocOther,BDocThing"
	if got != want {
		t.Errorf("declared types = %s, want %s\nstderr:\n%s", got, want, stderr)
	}
}

// Definitions that say the same thing about themselves and reach outside their
// own $defs are not known to describe the same type: what a "#/properties/k"
// pointer reaches is not a claim this resolver tracks, so nothing establishes
// that the two documents' versions of it agree -- and here they do not.
func TestSharedTypesSplitsDefinitionsThatReachOutsideTheirOwnDefs(t *testing.T) {
	body := func(title, k string) string {
		return fmt.Sprintf(`{
			"title": %q,
			"properties": {"t": {"$ref": "#/$defs/Thing"}, "k": %s},
			"$defs": {"Thing": {"$ref": "#/properties/k"}}
		}`, title, k)
	}
	dir, paths := writeSchemas(t,
		"c.json", body("CDoc", `{"type": "string", "minLength": 3}`),
		"d.json", body("DDoc", `{"type": "integer", "minimum": 9}`))

	out := filepath.Join(dir, "gen")
	if _, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out, "-p", "gen", "--shared-types")...); err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := strings.Join(declaredTypeNames(t, out), ",")
	if want := "CDoc,CDocThing,DDoc,DDocThing,K"; got != want {
		t.Errorf("declared types = %s, want %s", got, want)
	}
}

// Three keywords a definition can differ by that Schema.MarshalJSON does not
// write, and that the generator does read. Each of them, on its own, changes the
// type that comes out, so a comparison built on the marshalled form alone would
// share two definitions that describe different things.
func TestSharedTypesSeesDefinitionsThatDifferOutsideTheMarshalledForm(t *testing.T) {
	for _, tc := range []struct{ name, dialect, thingA, thingB string }{
		{
			// Const is `any`, so a JSON null there is indistinguishable from an
			// absent "const" once marshalled.
			name:   "const null",
			thingA: `{"const": null}`,
			thingB: `{}`,
		},
		{
			// Extensions hold every keyword the dialect does not define, and a
			// oneOf branch carrying one is not compiled into a variant.
			name:   "a vendor keyword",
			thingA: `{"oneOf": [{"type": "string", "x-vendor": 1}, {"type": "integer"}]}`,
			thingB: `{"oneOf": [{"type": "string"}, {"type": "integer"}]}`,
		},
		{
			// Draft 3 allows a schema among the entries of "type"; those are
			// kept beside the string entries and not in the marshalled form.
			// Both definitions here state *only* schema entries, so what the
			// marshalled form does carry -- the string entries -- is the same
			// for the two of them and says nothing about the difference.
			name:    "a schema-valued type entry",
			dialect: "http://json-schema.org/draft-03/schema#",
			thingA:  `{"type": [{"type": "string", "maxLength": 2}, {"type": "number"}]}`,
			thingB:  `{"type": [{"type": "integer"}, {"type": "number"}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := func(title, thing string) string {
				dialect := tc.dialect
				if dialect == "" {
					dialect = "https://json-schema.org/draft/2020-12/schema"
				}
				return fmt.Sprintf(`{"$schema": %q, "title": %q,
					"properties": {"t": {"$ref": "#/$defs/Thing"}},
					"$defs": {"Thing": %s}}`, dialect, title, thing)
			}
			dir, paths := writeSchemas(t, "a.json", body("ADoc", tc.thingA), "b.json", body("BDoc", tc.thingB))

			out := filepath.Join(dir, "gen")
			if _, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out, "-p", "gen", "--shared-types")...); err != nil {
				t.Fatalf("generate: %v", err)
			}
			got := strings.Join(declaredTypeNames(t, out), ",")
			for _, want := range []string{"ADocThing", "BDocThing"} {
				if !strings.Contains(got, want) {
					t.Errorf("declared types = %s, want a %s (the two definitions differ)", got, want)
				}
			}
		})
	}
}

// A document may carry the same definitions under both "definitions" and "$defs"
// -- Normalize only merges the two keywords when one of them is empty -- and that
// is two nodes with one meaning, not a name the document claims twice. It must
// still be qualified against the other document, and both nodes must land on the
// same qualified name.
func TestSharedTypesQualifiesADefinitionSpelledBothWays(t *testing.T) {
	_, paths := writeSchemas(t,
		"a.json", `{
			"$schema": "http://json-schema.org/draft-07/schema#",
			"title": "ADoc",
			"properties": {"t": {"$ref": "#/definitions/Thing"}, "u": {"$ref": "#/$defs/Thing"}},
			"definitions": {"Thing": {"type": "object", "properties": {"k": {"type": "string"}}, "required": ["k"]}},
			"$defs": {"Thing": {"type": "object", "properties": {"k": {"type": "string"}}, "required": ["k"]}}
		}`,
		"b.json", `{
			"$schema": "http://json-schema.org/draft-07/schema#",
			"title": "BDoc",
			"properties": {"t": {"$ref": "#/definitions/Thing"}},
			"definitions": {"Thing": {"type": "object", "properties": {"note": {"type": "string"}}}}
		}`)

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types")
		},
		"example.com/m/gen",
		[]rootInstance{
			{"BDoc", `{"t":{"note":"hello"}}`, true, `{"t":{"note":"hello"}}`},
			{"ADoc", `{"t":{"k":"x"},"u":{"k":"y"}}`, true, `{"t":{"k":"x"},"u":{"k":"y"}}`},
			{"ADoc", `{"t":{"note":"hello"}}`, false, ""},
		})
}

// A definition whose name is another document's *root* type name is the same
// collision, and it used to be answered two different ways depending on the
// order: listed first, the root won and the definition silently became it;
// listed second, the run failed with "give each schema a distinct root name",
// which describes a different problem. The root keeps its name -- it is the
// caller's, and it is what every other name here is qualified with.
func TestSharedTypesQualifiesADefinitionThatCollidesWithARootName(t *testing.T) {
	_, paths := writeSchemas(t,
		"alpha.json", `{"title": "Alpha", "type": "object",
			"properties": {"n": {"type": "integer"}}, "required": ["n"]}`,
		"beta.json", `{"title": "Beta",
			"properties": {"a": {"$ref": "#/$defs/Alpha"}},
			"$defs": {"Alpha": {"type": "object", "properties": {"z": {"type": "string"}}, "required": ["z"]}}}`)

	for _, order := range [][]string{{paths[0], paths[1]}, {paths[1], paths[0]}} {
		generateCompileRunRoots(t,
			func(modRoot string) []string {
				return append(append([]string{}, order...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types")
			},
			"example.com/m/gen",
			[]rootInstance{
				// Beta.a is beta's own $defs/Alpha: "z" is required and "n" is not
				// a property of it at all.
				{"Beta", `{"a":{"z":"x"}}`, true, `{"a":{"z":"x"}}`},
				{"Beta", `{"a":{"n":1}}`, false, ""},
				// Alpha is still alpha's root.
				{"Alpha", `{"n":1}`, true, `{"n":1}`},
				{"Alpha", `{}`, false, ""},
			})
	}
}

// A definition reached by a $ref from the other document as well as from its own.
// The name is pinned on the definition's node, so the reference resolves to the
// same answer as the definition itself however it arrives -- which is what makes
// the two properties below carry two different types.
func TestSharedTypesQualifiesADefinitionReachedFromBothDocuments(t *testing.T) {
	_, paths := writeSchemas(t,
		"alpha.json", `{
			"$id": "https://ex.test/alpha.json", "title": "Alpha",
			"properties": {
				"mine": {"$ref": "#/$defs/Thing"},
				"theirs": {"$ref": "https://ex.test/beta.json#/$defs/Thing"}
			},
			"$defs": {"Thing": {"type": "object", "properties": {"k": {"type": "string"}}, "required": ["k"]}}
		}`,
		"beta.json", `{
			"$id": "https://ex.test/beta.json", "title": "Beta",
			"properties": {"thing": {"$ref": "#/$defs/Thing"}},
			"$defs": {"Thing": {"type": "object", "properties": {"note": {"type": "string"}}, "required": ["note"]}}
		}`)

	for _, order := range [][]string{{paths[0], paths[1]}, {paths[1], paths[0]}} {
		dir := t.TempDir()
		out := filepath.Join(dir, "gen")
		if _, err := runGenerateCapturing(t, append(append([]string{}, order...), "-o", out, "-p", "gen", "--shared-types")...); err != nil {
			t.Fatalf("generate: %v", err)
		}
		// The names, not only the shapes: a reference that missed the pin
		// materializes the *same* definition a second time under the name its
		// $defs key derives, which behaves correctly and leaves the package with
		// two types for one definition and a field type that depends on the
		// order the inputs were listed.
		got := strings.Join(declaredTypeNames(t, out), ",")
		if want := "Alpha,AlphaThing,Beta,BetaThing"; got != want {
			t.Errorf("declared types = %s, want %s (order %v)", got, want, order)
		}

		generateCompileRunRoots(t,
			func(modRoot string) []string {
				return append(append([]string{}, order...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types")
			},
			"example.com/m/gen",
			[]rootInstance{
				{"Alpha", `{"mine":{"k":"x"},"theirs":{"note":"y"}}`, true, `{"mine":{"k":"x"},"theirs":{"note":"y"}}`},
				// Each position holds the other's shape: both must be refused.
				{"Alpha", `{"mine":{"note":"y"}}`, false, ""},
				{"Alpha", `{"theirs":{"k":"x"}}`, false, ""},
				{"Beta", `{"thing":{"note":"y"}}`, true, `{"thing":{"note":"y"}}`},
			})
	}
}

// The diagnostic, verbatim. A caller who expected one shared type has to be told
// they did not get one, and a message nobody has watched arrive is not a message.
func TestSharedTypesReportsTheDefinitionsItSplit(t *testing.T) {
	dir, paths := writeSchemas(t, "alpha.json", alphaThingDoc, "beta.json", betaThingDoc)

	stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", filepath.Join(dir, "gen"), "-p", "gen", "--shared-types")...)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	want := "warning: 2 documents claim the Go type name Thing, and those claims do not describe the same type, so they cannot be one:\n" +
		"  " + paths[0] + " $defs/Thing becomes AlphaThing\n" +
		"  " + paths[1] + " $defs/Thing becomes BetaThing\n" +
		"one package holds one type per name, so sharing it would have given every document whichever schema was generated first and discarded the rest. " +
		"Each definition is qualified with its own document's root type name -- all of them, not only the later ones, so the generated names do not depend on the order the inputs were listed. " +
		"A listed document's own root type keeps the name it was given; --root-name sets both. " +
		"Make the definitions identical if they were meant to be one type, or rename one of them in the schema to choose the Go names yourself.\n"
	if stderr != want {
		t.Errorf("stderr =\n%q\nwant\n%q", stderr, want)
	}
}

// The prefix is the document's root type name, so --root-name controls it. This
// is also the escape hatch the refusal below names.
func TestSharedTypesQualifiedNamesFollowRootName(t *testing.T) {
	dir, paths := writeSchemas(t, "alpha.json", alphaThingDoc, "beta.json", betaThingDoc)

	out := filepath.Join(dir, "gen")
	if _, err := runGenerateCapturing(t, append(append([]string{}, paths...),
		"-o", out, "-p", "gen", "--shared-types",
		"--root-name=file:"+paths[0]+"=Aye", "--root-name=file:"+paths[1]+"=Bee")...); err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := strings.Join(declaredTypeNames(t, out), ",")
	if want := "Aye,AyeThing,Bee,BeeThing"; got != want {
		t.Errorf("declared types = %s, want %s", got, want)
	}
}

// The qualified name is one schemagen invented, so it was never written in any
// document and cannot be checked against one. When it lands on a name the
// package already declares it has separated nothing, and the run is refused
// rather than silently merging under the new name -- which would be the same
// defect wearing a longer identifier.
func TestSharedTypesRefusesAQualifiedNameThatIsAlreadyTaken(t *testing.T) {
	// Two shapes reach it. In the first the name is taken by another definition,
	// which the caller can see; in the second by a type generated for a position
	// *inside* a document (root name + property name), which the caller cannot.
	//
	// The remedy differs with the shape, and the message says so: --root-name
	// separates the definition from another definition, and does not separate it
	// from an inline position, because that position is named from the root name
	// too and moves with it. Each case checks the remedy that applies.
	for _, tc := range []struct {
		name    string
		alpha   string
		fixed   string
		fixArgs []string
		want    string
	}{
		{
			name: "another definition",
			alpha: `{
				"title": "ADoc",
				"properties": {"t": {"$ref": "#/$defs/Thing"}, "q": {"$ref": "#/$defs/ADocThing"}},
				"$defs": {
					"Thing": {"type": "object", "properties": {"k": {"type": "string"}}},
					"ADocThing": {"type": "object", "properties": {"z": {"type": "boolean"}}}
				}
			}`,
			fixArgs: []string{"--root-name=file:%[1]s=Aye"},
			want:    "AyeThing",
		},
		{
			name: "an inline position",
			alpha: `{
				"title": "ADoc",
				"properties": {
					"t": {"$ref": "#/$defs/Thing"},
					"thing": {"type": "object", "properties": {"z": {"type": "boolean"}}, "required": ["z"]}
				},
				"$defs": {"Thing": {"type": "object", "properties": {"k": {"type": "string"}}}}
			}`,
			// The root name moves both, so the definition is what has to change.
			fixed: `{
				"title": "ADoc",
				"properties": {
					"t": {"$ref": "#/$defs/Widget"},
					"thing": {"type": "object", "properties": {"z": {"type": "boolean"}}, "required": ["z"]}
				},
				"$defs": {"Widget": {"type": "object", "properties": {"k": {"type": "string"}}}}
			}`,
			want: "ADocThing",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, paths := writeSchemas(t, "a.json", tc.alpha,
				"b.json", `{"title": "BDoc", "properties": {"t": {"$ref": "#/$defs/Thing"}},
					"$defs": {"Thing": {"type": "integer"}}}`)

			_, err := runGenerateCapturing(t, append(append([]string{}, paths...),
				"-o", filepath.Join(dir, "gen"), "-p", "gen", "--shared-types")...)
			if err == nil {
				t.Fatal("expected the run to be refused")
			}
			msg := err.Error()
			for _, want := range []string{
				"$defs/Thing in " + paths[0] + " was renamed to ADocThing, which another schema in this package already declares",
				"a property \"thing\" under a root named Alpha is also AlphaThing, and --root-name moves both of them together",
				"Rename the definition, or whatever else holds that name, in the schema",
				"--schema-package",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal should say %q, got:\n%s", want, msg)
				}
			}

			// And the remedy the message names has to work.
			if tc.fixed != "" {
				writeFile(t, paths[0], tc.fixed)
			}
			args := append(append([]string{}, paths...), "-o", filepath.Join(dir, "gen2"), "-p", "gen", "--shared-types")
			for _, a := range tc.fixArgs {
				args = append(args, fmt.Sprintf(a, paths[0]))
			}
			if _, err := runGenerateCapturing(t, args...); err != nil {
				t.Fatalf("the remedy should resolve it: %v", err)
			}
			if names := strings.Join(declaredTypeNames(t, filepath.Join(dir, "gen2")), ","); !strings.Contains(names, tc.want) {
				t.Errorf("declared types = %s, want a %s", names, tc.want)
			}
		})
	}
}

// --schema-package shares a name space per package, so two documents assigned to
// one package have the same collision and get the same answer. Documents in
// *different* packages do not: qualifying there would rename types nothing was
// about to merge.
func TestSchemaPackageQualifiesWithinAPackageAndNotAcrossOne(t *testing.T) {
	alpha := `{
		"$id": "https://ex.test/alpha.json", "title": "Alpha",
		"properties": {"thing": {"$ref": "#/$defs/Thing"}},
		"$defs": {"Thing": {"type": "object", "properties": {"k": {"type": "string"}}, "required": ["k"]}}
	}`
	beta := `{
		"$id": "https://ex.test/beta.json", "title": "Beta",
		"properties": {"thing": {"$ref": "#/$defs/Thing"}},
		"$defs": {"Thing": {"type": "object", "properties": {"note": {"type": "string"}}}}
	}`

	t.Run("one package", func(t *testing.T) {
		dir, paths := writeSchemas(t, "alpha.json", alpha, "beta.json", beta)
		out := filepath.Join(dir, "gen")
		if _, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out,
			"--schema-package", "https://ex.test/alpha.json=example.com/m/one",
			"--schema-package", "https://ex.test/beta.json=example.com/m/one")...); err != nil {
			t.Fatalf("generate: %v", err)
		}
		got := strings.Join(declaredTypeNames(t, out), ",")
		if want := "Alpha,AlphaThing,Beta,BetaThing"; got != want {
			t.Errorf("declared types = %s, want %s", got, want)
		}
	})

	t.Run("two packages", func(t *testing.T) {
		dir, paths := writeSchemas(t, "alpha.json", alpha, "beta.json", beta)
		out := filepath.Join(dir, "gen")
		stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out,
			"--schema-package", "https://ex.test/alpha.json=example.com/m/one",
			"--schema-package", "https://ex.test/beta.json=example.com/m/two")...)
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if strings.Contains(stderr, "do not describe the same type") {
			t.Errorf("separate packages are separate name spaces and must not be qualified:\n%s", stderr)
		}
		got := strings.Join(declaredTypeNames(t, out), ",")
		if want := "Alpha,Beta,Thing,Thing"; got != want {
			t.Errorf("declared types = %s, want %s (one Thing per package)", got, want)
		}
	})
}

// The default configuration gives every input its own generator, so nothing is
// shared and nothing is qualified. Two files of the package then declare the
// same name, which is the collision packageDecls already refuses -- and it must
// keep refusing it rather than being quietly repaired by machinery meant for the
// modes that do share.
func TestDefaultConfigStillRefusesTwoFilesDeclaringOneName(t *testing.T) {
	dir, paths := writeSchemas(t, "alpha.json", alphaThingDoc, "beta.json", betaThingDoc)

	_, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", filepath.Join(dir, "gen"), "-p", "gen")...)
	if err == nil {
		t.Fatal("expected the package-declaration collision to be refused")
	}
	if !strings.Contains(err.Error(), "both declare") || !strings.Contains(err.Error(), "--shared-types") {
		t.Errorf("unexpected error: %v", err)
	}
}
