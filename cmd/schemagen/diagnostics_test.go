package schemagen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runGenerateCapturing runs "generate" with args and returns everything written
// to stderr along with the command's error. The warnings under test go to
// stderr, so a test that only inspected the returned error would pass on a
// command that emitted nothing at all -- which is exactly the defect these
// fixtures are about.
func runGenerateCapturing(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var stdout, stderr strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"generate"}, args...))
	err := cmd.Execute()
	return stderr.String(), err
}

// ---------- issue #223: a $ref by document $id ----------

// A $ref naming a document by its $id resolved under --shared-types and under
// --schema-package and failed under the default configuration, which is one
// input and two answers. It now resolves under all three -- and, since a ref
// that resolves while dropping what it referenced is only half an answer, the
// referenced constraint has to survive into the generated code.
func TestRefByDocumentIDResolvesAndEnforcesUnderDefaultConfig(t *testing.T) {
	src := t.TempDir()
	// Deliberately untitled: under the default configuration each input file
	// materializes its own copy of what it references, so a title here would
	// have both files declaring one name -- a separate, documented refusal that
	// has nothing to do with whether the ref resolved.
	writeFile(t, filepath.Join(src, "c.json"), `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/c.json",
		"type": "string", "minLength": 3
	}`)
	writeFile(t, filepath.Join(src, "main.json"), `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "MainDoc", "type": "object",
		"properties": {"c": {"$ref": "https://ex.test/c.json"}}
	}`)

	out := t.TempDir()
	stderr, err := runGenerateCapturing(t,
		filepath.Join(src, "main.json"), filepath.Join(src, "c.json"),
		"-o", out, "-p", "m")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}

	gen, readErr := os.ReadFile(filepath.Join(out, "main.go"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	// The whole point of the fixture: minLength came from the referenced
	// document, so a ref that resolved but dropped it would still be the bug.
	if !strings.Contains(string(gen), "less than minimum 3") {
		t.Errorf("the referenced minLength should be enforced:\n%s", gen)
	}
	if strings.Contains(string(gen), "C                    any") {
		t.Errorf("the property should carry the referenced type, not `any`:\n%s", gen)
	}
}

// The seven $ref spellings a sweep found correct are the controls on the fix
// above: consulting the run's own documents first must not take a ref away from
// the file resolver.
func TestFileRefFormsStillResolveAndEnforce(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "sub", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$defs": {
			"x": {"type": "string", "minLength": 7},
			"a/b": {"type": "string", "minLength": 9}
		},
		"type": "string", "minLength": 3,
		"$anchor": "root"
	}`
	writeFile(t, filepath.Join(src, "sibling.json"), body)
	writeFile(t, filepath.Join(src, "sub", "dir", "nested.json"), body)
	writeFile(t, filepath.Join(src, "withid.json"), `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://unrelated.test/elsewhere.json",
		"type": "string", "minLength": 5
	}`)

	cases := []struct {
		name    string
		ref     string
		minimum string
	}{
		{"sibling file", "sibling.json", "minimum 3"},
		{"./file", "./sibling.json", "minimum 3"},
		{"sub/dir/file", "sub/dir/nested.json", "minimum 3"},
		{"file#/$defs/x", "sibling.json#/$defs/x", "minimum 7"},
		{"file#anchor", "sibling.json#root", "minimum 3"},
		{"URL-encoded pointer", "sibling.json#/$defs/a~1b", "minimum 9"},
		{"sibling declaring an unrelated $id", "withid.json", "minimum 5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mainPath := filepath.Join(src, "main.json")
			writeFile(t, mainPath, `{
				"$schema": "https://json-schema.org/draft/2020-12/schema",
				"title": "MainDoc", "type": "object",
				"properties": {"p": {"$ref": `+quoteJSON(tc.ref)+`}}
			}`)
			out := t.TempDir()
			stderr, err := runGenerateCapturing(t, mainPath, "-o", out, "-p", "m")
			if err != nil {
				t.Fatalf("generate %s: %v\nstderr:\n%s", tc.ref, err, stderr)
			}
			gen, readErr := os.ReadFile(filepath.Join(out, "main.go"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !strings.Contains(string(gen), tc.minimum) {
				t.Errorf("ref %q should still enforce %q:\n%s", tc.ref, tc.minimum, gen)
			}
		})
	}
}

func quoteJSON(s string) string { return `"` + s + `"` }

// The advice attached to an unresolved-ref failure has to be true for the case
// that produced it. The old text said "place the referenced documents alongside
// the schema", which is no help for a $ref by absolute URI: the document can be
// sitting right next to the schema and still not be found.
func TestUnresolvedRefAdviceNamesBothResolutionRoutes(t *testing.T) {
	src := t.TempDir()
	mainPath := filepath.Join(src, "main.json")
	writeFile(t, mainPath, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "MainDoc", "type": "object",
		"properties": {"c": {"$ref": "https://ex.test/nowhere.json"}}
	}`)

	_, err := runGenerateCapturing(t, mainPath, "-o", t.TempDir(), "-p", "m")
	if err == nil {
		t.Fatal("expected generation to fail on an unresolvable $ref")
	}
	msg := err.Error()
	for _, want := range []string{
		`cannot resolve $ref "https://ex.test/nowhere.json"`,
		"matched against the $id of the documents given to this run",
		"pass the referenced document as an input too",
		"--allow-remote-refs",
		"--lenient-refs",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("failure message should contain %q, got:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "place the referenced documents alongside the schema") {
		t.Errorf("the advice that is false for this case should be gone:\n%s", msg)
	}
}

// ---------- issue #224: --lenient-refs must not degrade silently ----------

// The flag lets generation continue past a $ref nothing can serve, which is a
// reasonable thing to ask for. What it must not do is leave the caller believing
// the output checks what the schema says. Both statements the README promises
// are asserted, at three ref positions, and so is the type still behaving as
// `any`.
func TestLenientRefsWarnsAndSaysSoInTheSource(t *testing.T) {
	src := t.TempDir()
	mainPath := filepath.Join(src, "main.json")
	writeFile(t, mainPath, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "LenientDoc", "type": "object",
		"properties": {
			"p": {"$ref": "missing.json#/$defs/Nope"},
			"arr": {"type": "array", "items": {"$ref": "gone.json"}}
		},
		"$defs": {"D": {"$ref": "absent.json"}}
	}`)

	out := t.TempDir()
	stderr, err := runGenerateCapturing(t, mainPath, "-o", out, "-p", "m", "--lenient-refs")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}

	for _, ref := range []string{"absent.json", "gone.json", "missing.json#/$defs/Nope"} {
		want := "$ref \"" + ref + "\" could not be resolved; --lenient-refs generated the file anyway"
		if !strings.Contains(stderr, "warning: ") || !strings.Contains(stderr, want) {
			t.Errorf("stderr should carry a warning naming %q, got:\n%s", ref, stderr)
		}
	}

	gen, readErr := os.ReadFile(filepath.Join(out, "main.go"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	src2 := string(gen)
	if !strings.Contains(src2, "NOT VALIDATED: --lenient-refs generated this file with $refs that no resolver") {
		t.Errorf("the generated source should carry the banner:\n%s", src2)
	}
	for _, ref := range []string{"absent.json", "gone.json", "missing.json#/$defs/Nope"} {
		if !strings.Contains(src2, "//\t"+ref) {
			t.Errorf("the banner should name %q:\n%s", ref, src2)
		}
	}
	// Unchanged behaviour: the degraded position is still an untyped `any`.
	if !strings.Contains(src2, "P                    any") {
		t.Errorf("the degraded property should still be `any`:\n%s", src2)
	}
}

// A run with nothing unresolved must stay silent, and its source must not grow a
// banner. Without this the warning above could be emitted unconditionally and
// still look right.
func TestLenientRefsSaysNothingWhenEverythingResolves(t *testing.T) {
	src := t.TempDir()
	mainPath := filepath.Join(src, "main.json")
	writeFile(t, mainPath, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "FineDoc", "type": "object",
		"properties": {"p": {"$ref": "#/$defs/D"}},
		"$defs": {"D": {"type": "string"}}
	}`)

	out := t.TempDir()
	stderr, err := runGenerateCapturing(t, mainPath, "-o", out, "-p", "m", "--lenient-refs")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stderr, "could not be resolved") {
		t.Errorf("no ref failed, so nothing should be warned about:\n%s", stderr)
	}
	gen, readErr := os.ReadFile(filepath.Join(out, "main.go"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(gen), "NOT VALIDATED: --lenient-refs") {
		t.Errorf("no ref failed, so the source should carry no banner:\n%s", gen)
	}
}

// ---------- issue #176: required + readOnly under --strict-read-write ----------

// The combination produces a type no document satisfies: one that sets the
// property fails to decode, one that omits it fails Validate. The runtime
// behaviour is deliberately unchanged; the warning is the whole fix, so its text
// is what the test asserts.
func TestStrictReadWriteWarnsOnUnsatisfiableRequiredProperty(t *testing.T) {
	src := t.TempDir()
	mainPath := filepath.Join(src, "ro.json")
	writeFile(t, mainPath, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "RODoc", "type": "object", "required": ["id"],
		"properties": {
			"id": {"type": "string", "readOnly": true, "default": "auto"},
			"name": {"type": "string"}
		}
	}`)

	stderr, err := runGenerateCapturing(t, mainPath, "-o", t.TempDir(), "-p", "m", "--strict-read-write")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"warning: ",
		"RoDoc.id is both required and readOnly",
		"under --strict-read-write no document satisfies it",
		"fails to decode (read-only property may not be set)",
		"fails Validate (required property is missing)",
		"SetDefaults does not help",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr should contain %q, got:\n%s", want, stderr)
		}
	}
}

// The three shapes that must not be warned about. Each is a property of the same
// schema keywords in a configuration where no document is refused, so a warning
// keyed on anything coarser than "the decoder refuses this exact key and
// Validate demands it" fires here and is wrong.
func TestUnsatisfiableRequiredWarningDoesNotOverfire(t *testing.T) {
	cases := []struct {
		name  string
		flags []string
		body  string
	}{
		{
			// readOnly is an annotation without the flag: nothing is refused.
			name:  "required and readOnly without the flag",
			flags: nil,
			body: `{"$schema": "https://json-schema.org/draft/2020-12/schema",
				"title": "PlainDoc", "type": "object", "required": ["id"],
				"properties": {"id": {"type": "string", "readOnly": true}}}`,
		},
		{
			// The decoder refuses "id", but no document has to carry it.
			name:  "readOnly on an optional property",
			flags: []string{"--strict-read-write"},
			body: `{"$schema": "https://json-schema.org/draft/2020-12/schema",
				"title": "OptDoc", "type": "object", "required": ["name"],
				"properties": {"id": {"type": "string", "readOnly": true},
				               "name": {"type": "string"}}}`,
		},
		{
			// writeOnly is the other half of the flag and is satisfiable: the
			// document may set the property, it just is not written back out.
			name:  "required and writeOnly",
			flags: []string{"--strict-read-write"},
			body: `{"$schema": "https://json-schema.org/draft/2020-12/schema",
				"title": "WODoc", "type": "object", "required": ["secret"],
				"properties": {"secret": {"type": "string", "writeOnly": true}}}`,
		},
		{
			// The requirement binds only on the documents the `if` selects, so
			// the ones it does not select decode and validate.
			name:  "required only under a conditional",
			flags: []string{"--strict-read-write"},
			body: `{"$schema": "https://json-schema.org/draft/2020-12/schema",
				"title": "CondDoc", "type": "object",
				"properties": {"id": {"type": "string", "readOnly": true},
				               "k": {"type": "string"}},
				"if": {"properties": {"k": {"const": "x"}}, "required": ["k"]},
				"then": {"required": ["id"]}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := t.TempDir()
			mainPath := filepath.Join(src, "s.json")
			writeFile(t, mainPath, tc.body)
			args := append([]string{mainPath, "-o", t.TempDir(), "-p", "m"}, tc.flags...)
			stderr, err := runGenerateCapturing(t, args...)
			if err != nil {
				t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
			}
			if strings.Contains(stderr, "both required and readOnly") {
				t.Errorf("no document is refused here, so nothing should warn:\n%s", stderr)
			}
		})
	}
}

// The flag binds wherever the property's schema is, including through a $ref
// (issue #172), and the decoder's refusal follows it there. The warning is read
// off the same list, so it has to follow it too.
func TestUnsatisfiableRequiredWarningFollowsARef(t *testing.T) {
	src := t.TempDir()
	mainPath := filepath.Join(src, "refro.json")
	writeFile(t, mainPath, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "RefRODoc", "type": "object", "required": ["id"],
		"properties": {"id": {"$ref": "#/$defs/SID"}},
		"$defs": {"SID": {"type": "string", "readOnly": true}}
	}`)

	stderr, err := runGenerateCapturing(t, mainPath, "-o", t.TempDir(), "-p", "m", "--strict-read-write")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "RefRoDoc.id is both required and readOnly") {
		t.Errorf("a readOnly reached through a $ref should warn too:\n%s", stderr)
	}
}

// ---------- issue #228: a $ref cycle between input documents ----------

// --shared-types generates the inputs in one pass, so a cycle has no order that
// works. It used to fail by naming the root type names and telling the caller to
// make them distinct, which describes a different problem and cannot fix this
// one. Both spellings of the cycle are covered: by $id, and by relative path
// between documents that declare no $id at all.
func TestSharedTypesRefusesACycleBetweenInputDocuments(t *testing.T) {
	cases := []struct {
		name   string
		a, b   string
		refToA string
		refToB string
	}{
		{
			name: "by $id",
			a: `{"$schema": "https://json-schema.org/draft/2020-12/schema",
				"$id": "https://ex.test/a.json", "title": "ADoc", "type": "object",
				"properties": {"b": {"$ref": "https://ex.test/b.json"}}}`,
			b: `{"$schema": "https://json-schema.org/draft/2020-12/schema",
				"$id": "https://ex.test/b.json", "title": "BDoc", "type": "object",
				"properties": {"a": {"$ref": "https://ex.test/a.json"}}}`,
			refToA: `"https://ex.test/a.json"`,
			refToB: `"https://ex.test/b.json"`,
		},
		{
			name: "by relative path, no $id",
			a: `{"$schema": "https://json-schema.org/draft/2020-12/schema",
				"title": "ADoc", "type": "object",
				"properties": {"b": {"$ref": "b.json"}}}`,
			b: `{"$schema": "https://json-schema.org/draft/2020-12/schema",
				"title": "BDoc", "type": "object",
				"properties": {"a": {"$ref": "a.json"}}}`,
			refToA: `"a.json"`,
			refToB: `"b.json"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := t.TempDir()
			aPath := filepath.Join(src, "a.json")
			bPath := filepath.Join(src, "b.json")
			writeFile(t, aPath, tc.a)
			writeFile(t, bPath, tc.b)

			// Both orders, because neither one can work.
			for _, args := range [][]string{{aPath, bPath}, {bPath, aPath}} {
				out := t.TempDir()
				_, err := runGenerateCapturing(t,
					append(append([]string{}, args...), "-o", out, "-p", "m", "--shared-types")...)
				if err == nil {
					t.Fatalf("a cycle should be refused, order %v", args)
				}
				msg := err.Error()
				for _, want := range []string{
					"circular reference between input documents",
					aPath + " references " + bPath + " via " + tc.refToB,
					bPath + " references " + aPath + " via " + tc.refToA,
					"A cycle has no such order, so this set cannot be generated and nothing was written",
					"Merge the mutually-referencing documents into one document",
				} {
					if !strings.Contains(msg, want) {
						t.Errorf("refusal should contain %q, got:\n%s", want, msg)
					}
				}
				if strings.Contains(msg, "give each schema a distinct root name") {
					t.Errorf("the cycle must not be reported as a name collision:\n%s", msg)
				}
				if entries, readErr := os.ReadDir(out); readErr == nil && len(entries) > 0 {
					t.Errorf("nothing should have been written, found %d entries", len(entries))
				}
			}
		})
	}
}

// The two things the cycle check must leave alone: a correctly ordered
// non-cyclic run, and a genuine duplicate root name, which keeps its own
// (correct) message rather than being relabelled a cycle.
func TestSharedTypesCycleCheckLeavesTheOtherCasesAlone(t *testing.T) {
	t.Run("non-cyclic run in dependency order", func(t *testing.T) {
		src := t.TempDir()
		writeFile(t, filepath.Join(src, "a.json"), `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"title": "ADoc", "type": "object",
			"properties": {"b": {"$ref": "b.json"}}}`)
		writeFile(t, filepath.Join(src, "b.json"), `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"title": "BDoc", "type": "object",
			"properties": {"n": {"type": "integer"}}}`)

		out := t.TempDir()
		stderr, err := runGenerateCapturing(t,
			filepath.Join(src, "b.json"), filepath.Join(src, "a.json"),
			"-o", out, "-p", "m", "--shared-types")
		if err != nil {
			t.Fatalf("a non-cyclic set in dependency order should generate: %v\nstderr:\n%s", err, stderr)
		}
		for _, f := range []string{"a.go", "b.go"} {
			if _, statErr := os.Stat(filepath.Join(out, f)); statErr != nil {
				t.Errorf("expected %s to be written: %v", f, statErr)
			}
		}
	})

	t.Run("genuine duplicate root name", func(t *testing.T) {
		src := t.TempDir()
		body := `{"$schema": "https://json-schema.org/draft/2020-12/schema",
			"title": "Same", "type": "object", "properties": {%s}}`
		writeFile(t, filepath.Join(src, "x.json"), strings.Replace(body, "%s", `"a": {"type": "string"}`, 1))
		writeFile(t, filepath.Join(src, "y.json"), strings.Replace(body, "%s", `"b": {"type": "string"}`, 1))

		_, err := runGenerateCapturing(t,
			filepath.Join(src, "x.json"), filepath.Join(src, "y.json"),
			"-o", t.TempDir(), "-p", "m", "--shared-types")
		if err == nil {
			t.Fatal("two schemas claiming one root name should still fail")
		}
		if !strings.Contains(err.Error(), "give each schema a distinct root name") {
			t.Errorf("a name collision should keep its own message, got:\n%s", err)
		}
		if strings.Contains(err.Error(), "circular reference") {
			t.Errorf("a name collision is not a cycle:\n%s", err)
		}
	})

	t.Run("a document referencing itself is not a cycle", func(t *testing.T) {
		src := t.TempDir()
		mainPath := filepath.Join(src, "s.json")
		writeFile(t, mainPath, `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://ex.test/s.json", "title": "SDoc", "type": "object",
			"properties": {"child": {"$ref": "https://ex.test/s.json"}}}`)

		stderr, err := runGenerateCapturing(t, mainPath, "-o", t.TempDir(), "-p", "m", "--shared-types")
		if err != nil {
			t.Fatalf("a self-referential document should generate: %v\nstderr:\n%s", err, stderr)
		}
	})
}
