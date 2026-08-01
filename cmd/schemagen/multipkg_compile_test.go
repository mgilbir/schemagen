package schemagen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildGenerated writes a go.mod for the generated tree and compiles it. The
// generator can emit plausible-looking Go that does not build (a foreign package
// aliased against a stdlib import, a validation guard against the wrong zero
// literal), so multi-package output is checked by compiling it rather than by
// string matching alone.
func buildGenerated(t *testing.T, dir, modulePath string) (string, error) {
	t.Helper()
	gomod := "module " + modulePath + "\n\ngo 1.23\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A foreign package whose last path segment collides with a stdlib import the
// generated file also needs must be aliased, or the file declares the same name
// twice and does not compile.
func TestMultiPackageForeignPackageNameCollidesWithStdlib(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/a.json",
		"title": "ADoc",
		"type": "object",
		"definitions": {
			"widget": {"type": "object", "properties": {"size": {"type": "integer"}}, "required": ["size"]}
		}
	}`)
	// b.json needs encoding/json and time of its own, and refs a.json — whose
	// assigned import path ends in "json".
	writeFile(t, filepath.Join(src, "b.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/b.json",
		"title": "BDoc",
		"type": "object",
		"properties": {
			"w": {"$ref": "https://ex.test/a.json#/definitions/widget"},
			"t": {"type": "string", "format": "date-time"}
		},
		"required": ["w"]
	}`)

	out := t.TempDir()
	if err := runGenerateArgs(t, filepath.Join(src, "a.json"), filepath.Join(src, "b.json"),
		"-o", out,
		"--schema-package", "https://ex.test/a.json=example.com/m/json",
		"--schema-package", "https://ex.test/b.json=example.com/m/bpkg",
		"--root-name", "a.json=ADoc", "--root-name", "b.json=BDoc",
	); err != nil {
		t.Fatalf("generate: %v", err)
	}

	if buildOut, err := buildGenerated(t, out, "example.com/m"); err != nil {
		t.Errorf("generated multi-package output does not compile: %v\n%s", err, buildOut)
	}
}

// The happy path must compile too, including the cross-package import and the
// validation guards the referencing package emits for a foreign type.
func TestMultiPackageHappyPathCompiles(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/a.json",
		"title": "ADoc",
		"type": "object",
		"definitions": {
			"widget": {
				"type": "object",
				"properties": {"size": {"type": "integer"}, "tags": {"type": "array", "items": {"type": "string"}}},
				"required": ["size"]
			}
		}
	}`)
	writeFile(t, filepath.Join(src, "b.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/b.json",
		"title": "BDoc",
		"type": "object",
		"properties": {"w": {"$ref": "https://ex.test/a.json#/definitions/widget"}},
		"required": ["w"]
	}`)

	out := t.TempDir()
	// Dependent schema listed first: ordering is derived, not trusted.
	if err := runGenerateArgs(t, filepath.Join(src, "b.json"), filepath.Join(src, "a.json"),
		"-o", out,
		"--schema-package", "https://ex.test/a.json=example.com/m/apkg",
		"--schema-package", "https://ex.test/b.json=example.com/m/bpkg",
		"--root-name", "a.json=ADoc", "--root-name", "b.json=BDoc",
	); err != nil {
		t.Fatalf("generate: %v", err)
	}

	bOut, err := os.ReadFile(filepath.Join(out, "bpkg", "b.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bOut), "example.com/m/apkg") {
		t.Errorf("b should import apkg rather than copy the type:\n%s", bOut)
	}
	if buildOut, err := buildGenerated(t, out, "example.com/m"); err != nil {
		t.Errorf("generated multi-package output does not compile: %v\n%s", err, buildOut)
	}
}

// Shared helpers are package-level functions, so every generated package that
// needs one must get its own copy. Emitting them once for the whole run would
// leave every package but the first referencing undefined functions.
func TestMultiPackageEmitsHelpersPerPackage(t *testing.T) {
	src := t.TempDir()
	// Two independent documents, each with a discriminated oneOf, so both
	// packages need the oneOf and discriminator helpers.
	for _, doc := range []struct{ name, id, title, prop, k1, k2 string }{
		{"one.json", "https://ex.test/one.json", "One", "v", "a", "b"},
		{"two.json", "https://ex.test/two.json", "Two", "w", "c", "d"},
	} {
		writeFile(t, filepath.Join(src, doc.name), fmt.Sprintf(`{
			"$schema": "http://json-schema.org/draft-07/schema#",
			"$id": %q,
			"title": %q,
			"type": "object",
			"properties": {%q: {"oneOf": [
				{"$ref": "#/definitions/%s"},
				{"$ref": "#/definitions/%s"}
			]}},
			"definitions": {
				%q: {"type":"object","properties":{"k":{"const":%q},"x":{"type":"string"}},"required":["k","x"]},
				%q: {"type":"object","properties":{"k":{"const":%q},"y":{"type":"string"}},"required":["k","y"]}
			}
		}`, doc.id, doc.title, doc.prop, doc.k1, doc.k2, doc.k1, doc.k1, doc.k2, doc.k2))
	}

	out := t.TempDir()
	if err := runGenerateArgs(t, filepath.Join(src, "one.json"), filepath.Join(src, "two.json"),
		"-o", out,
		"--schema-package", "https://ex.test/one.json=example.com/m/onepkg",
		"--schema-package", "https://ex.test/two.json=example.com/m/twopkg",
	); err != nil {
		t.Fatalf("generate: %v", err)
	}

	for pkgDir, pkgName := range map[string]string{"onepkg": "onepkg", "twopkg": "twopkg"} {
		body, err := os.ReadFile(filepath.Join(out, pkgDir, helperFileName))
		if err != nil {
			t.Fatalf("package %s has no helper file: %v", pkgDir, err)
		}
		if !strings.Contains(string(body), "package "+pkgName) {
			t.Errorf("helper file in %s has the wrong package clause:\n%s", pkgDir, body)
		}
	}

	if buildOut, err := buildGenerated(t, out, "example.com/m"); err != nil {
		t.Errorf("generated multi-package output does not compile: %v\n%s", err, buildOut)
	}
}

// A $ref targeting a subschema that carries its own $id must still be
// recognized as belonging to the package that owns the containing document.
func TestMultiPackageNestedIDResourceIsImported(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/a.json",
		"title": "ADoc",
		"type": "object",
		"definitions": {
			"widget": {
				"$id": "https://ex.test/widget-scope",
				"type": "object",
				"properties": {"size": {"type": "integer"}},
				"required": ["size"]
			}
		}
	}`)
	writeFile(t, filepath.Join(src, "b.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/b.json",
		"title": "BDoc",
		"type": "object",
		"properties": {"w": {"$ref": "https://ex.test/a.json#/definitions/widget"}},
		"required": ["w"]
	}`)

	out := t.TempDir()
	if err := runGenerateArgs(t, filepath.Join(src, "a.json"), filepath.Join(src, "b.json"),
		"-o", out,
		"--schema-package", "https://ex.test/a.json=example.com/m/apkg",
		"--schema-package", "https://ex.test/b.json=example.com/m/bpkg",
		"--root-name", "a.json=ADoc", "--root-name", "b.json=BDoc",
	); err != nil {
		t.Fatalf("generate: %v", err)
	}

	bOut, err := os.ReadFile(filepath.Join(out, "bpkg", "b.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(bOut)
	if !strings.Contains(got, "example.com/m/apkg") {
		t.Errorf("nested-$id target should be imported from its owning package:\n%s", got)
	}
	if strings.Contains(got, "type WidgetScope struct") {
		t.Errorf("nested-$id target was duplicated locally instead of imported:\n%s", got)
	}
	if buildOut, err := buildGenerated(t, out, "example.com/m"); err != nil {
		t.Errorf("generated multi-package output does not compile: %v\n%s", err, buildOut)
	}
}

// A $ref whose resolved URI does not match the target's $id (the file sits
// somewhere other than where its $id says) must still reach the instance this
// run loaded, or a second copy is parsed and the type is duplicated instead of
// imported.
func TestMultiPackageRefResolvingAwayFromDeclaredID(t *testing.T) {
	src := t.TempDir()
	// a.json declares an $id under /other/, but lives beside b.json.
	writeFile(t, filepath.Join(src, "a.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/other/a.json",
		"title": "ADoc",
		"type": "object",
		"definitions": {
			"widget": {"type": "object", "properties": {"size": {"type": "integer"}}, "required": ["size"]}
		}
	}`)
	// The sibling-relative ref resolves to https://ex.test/a.json, which is not
	// a.json's $id.
	writeFile(t, filepath.Join(src, "b.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/b.json",
		"title": "BDoc",
		"type": "object",
		"properties": {"w": {"$ref": "a.json#/definitions/widget"}},
		"required": ["w"]
	}`)

	out := t.TempDir()
	if err := runGenerateArgs(t, filepath.Join(src, "a.json"), filepath.Join(src, "b.json"),
		"-o", out,
		"--schema-package", "https://ex.test/other/a.json=example.com/m/apkg",
		"--schema-package", "https://ex.test/b.json=example.com/m/bpkg",
		"--root-name", "a.json=ADoc", "--root-name", "b.json=BDoc",
	); err != nil {
		t.Fatalf("generate: %v", err)
	}
	bOut, err := os.ReadFile(filepath.Join(out, "bpkg", "b.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(bOut)
	if !strings.Contains(got, "example.com/m/apkg") {
		t.Errorf("b should import apkg even though the ref resolved away from a.json's $id:\n%s", got)
	}
	if strings.Contains(got, "type Widget struct") {
		t.Errorf("a second copy of a.json was parsed and the type duplicated:\n%s", got)
	}
	if buildOut, err := buildGenerated(t, out, "example.com/m"); err != nil {
		t.Errorf("generated output does not compile: %v\n%s", err, buildOut)
	}
}

// A sibling-relative $ref inside an input other than the first must resolve:
// the file resolver has to be rooted at every input's directory, not only the
// first argument's.
func TestMultiPackageSiblingRefInNonFirstInput(t *testing.T) {
	src := t.TempDir()
	one := filepath.Join(src, "one")
	two := filepath.Join(src, "two")
	for _, d := range []string{one, two} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(one, "a.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/one/a.json",
		"title": "ADoc", "type": "object",
		"properties": {"n": {"type": "integer"}}
	}`)
	writeFile(t, filepath.Join(two, "helper.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/two/helper.json",
		"title": "HDoc", "type": "object",
		"definitions": {"widget": {"type": "object", "properties": {"size": {"type": "integer"}}, "required": ["size"]}}
	}`)
	writeFile(t, filepath.Join(two, "b.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/two/b.json",
		"title": "BDoc", "type": "object",
		"properties": {"w": {"$ref": "helper.json#/definitions/widget"}},
		"required": ["w"]
	}`)

	out := t.TempDir()
	if err := runGenerateArgs(t, filepath.Join(one, "a.json"), filepath.Join(two, "b.json"),
		"-o", out,
		"--schema-package", "https://ex.test/one/a.json=example.com/m/apkg",
		"--schema-package", "https://ex.test/two/b.json=example.com/m/bpkg",
		"--root-name", "a.json=ADoc", "--root-name", "b.json=BDoc",
	); err != nil {
		t.Fatalf("a sibling-relative ref in a non-first input should resolve: %v", err)
	}
	if buildOut, err := buildGenerated(t, out, "example.com/m"); err != nil {
		t.Errorf("generated output does not compile: %v\n%s", err, buildOut)
	}
}

// Distinct import paths ending in the same segment default to the same output
// directory, and a directory holds one Go package.
func TestMultiPackageRejectsSharedOutputDirectory(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/a.json", "title": "ADoc", "type": "object",
		"properties": {"n": {"type": "integer"}}
	}`)
	writeFile(t, filepath.Join(src, "b.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/b.json", "title": "BDoc", "type": "object",
		"properties": {"m": {"type": "integer"}}
	}`)

	err := runGenerateArgs(t, filepath.Join(src, "a.json"), filepath.Join(src, "b.json"),
		"-o", t.TempDir(),
		"--schema-package", "https://ex.test/a.json=example.com/m/one/shared",
		"--schema-package", "https://ex.test/b.json=example.com/m/two/shared",
		"--root-name", "a.json=ADoc", "--root-name", "b.json=BDoc",
	)
	if err == nil {
		t.Fatal("expected an error: both packages default to the same output directory")
	}
	if !strings.Contains(err.Error(), "one Go package") {
		t.Errorf("error should explain the one-package-per-directory rule, got: %v", err)
	}
}

// --lenient-refs must reach the per-package generators, and the strict failure
// should point at the escape hatches.
func TestMultiPackageLenientRefsIsHonoured(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "c.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/c.json", "title": "CDoc", "type": "object",
		"properties": {"x": {"$ref": "https://ex.test/missing.json#/definitions/nope"}}
	}`)
	args := []string{
		filepath.Join(src, "c.json"), "-o", t.TempDir(),
		"--schema-package", "https://ex.test/c.json=example.com/m/cpkg",
		"--root-name", "c.json=CDoc",
	}
	err := runGenerateArgs(t, args...)
	if err == nil {
		t.Fatal("an unresolvable $ref should fail multi-package generation by default")
	}
	if !strings.Contains(err.Error(), "--lenient-refs") {
		t.Errorf("the error should mention the escape hatch, got: %v", err)
	}
	if err := runGenerateArgs(t, append(args, "--lenient-refs")...); err != nil {
		t.Errorf("--lenient-refs should be honoured in multi-package mode: %v", err)
	}
}

// --package names one package; in multi-package mode each package is named from
// its import path, so combining them is a mistake worth reporting.
func TestMultiPackageRejectsPackageFlag(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/a.json", "title": "ADoc", "type": "object",
		"properties": {"n": {"type": "integer"}}
	}`)
	err := runGenerateArgs(t, filepath.Join(src, "a.json"), "-o", t.TempDir(), "-p", "mypkg",
		"--schema-package", "https://ex.test/a.json=example.com/m/apkg",
		"--root-name", "a.json=ADoc",
	)
	if err == nil || !strings.Contains(err.Error(), "--package cannot be combined") {
		t.Errorf("expected --package to be rejected in multi-package mode, got: %v", err)
	}
}

// A --field-map entry that was applied must not be reported as unused just
// because generation went through the multi-package path.
func TestMultiPackageFieldMapDoesNotWarnSpuriously(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/a.json", "title": "ADoc", "type": "object",
		"properties": {"my_field": {"type": "string"}}
	}`)
	writeFile(t, filepath.Join(src, "fm.json"), `{"a.json": {"ADoc": {"my_field": "MyRenamedField"}}}`)

	out := t.TempDir()
	cmd := NewRootCmd()
	var stdout, stderr strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"generate", filepath.Join(src, "a.json"), "-o", out,
		"--schema-package", "https://ex.test/a.json=example.com/m/apkg",
		"--root-name", "a.json=ADoc",
		"--field-map", filepath.Join(src, "fm.json"),
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if strings.Contains(stderr.String(), "does not match") {
		t.Errorf("applied field-map entry should not be reported unused:\n%s", stderr.String())
	}
	gen, err := os.ReadFile(filepath.Join(out, "apkg", "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gen), "MyRenamedField") {
		t.Errorf("field-map rename should have been applied:\n%s", gen)
	}
}

// The multi-package error paths: each one is a plain user mistake, and each one
// previously either produced no message or an unhelpful one.
func TestMultiPackageErrorPaths(t *testing.T) {
	src := t.TempDir()
	withID := filepath.Join(src, "a.json")
	writeFile(t, withID, `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/a.json", "title": "ADoc", "type": "object",
		"properties": {"n": {"type": "integer"}}
	}`)
	noID := filepath.Join(src, "noid.json")
	writeFile(t, noID, `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"title": "NoID", "type": "object", "properties": {"n": {"type": "integer"}}
	}`)
	dupA := filepath.Join(src, "dup.json")
	writeFile(t, dupA, `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/a.json", "title": "DupDoc", "type": "object",
		"properties": {"n": {"type": "integer"}}
	}`)

	mapA := "https://ex.test/a.json=example.com/m/apkg"

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "input without $id",
			args: []string{noID, "--schema-package", mapA},
			want: "requires every schema to declare $id",
		},
		{
			name: "input with no package mapping",
			args: []string{withID, "--schema-package", "https://ex.test/other.json=example.com/m/other"},
			want: "no --schema-package mapping",
		},
		{
			name: "two inputs sharing an $id",
			args: []string{withID, dupA, "--schema-package", mapA, "--root-name", "a.json=ADoc", "--root-name", "dup.json=DupDoc"},
			want: "duplicate $id",
		},
		{
			name: "--schema-output without --schema-package",
			args: []string{withID, "--schema-output", "https://ex.test/a.json=out.go"},
			want: "--schema-output requires --schema-package",
		},
		{
			name: "non-static validation",
			args: []string{withID, "--schema-package", mapA, "--validation", "runtime"},
			want: "require --validation static",
		},
		{
			name: "malformed --schema-package",
			args: []string{withID, "--schema-package", "no-equals-sign"},
			want: "expected <document $id>=<Go import path>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runGenerateArgs(t, append(tc.args, "-o", t.TempDir())...)
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should mention %q, got: %v", tc.want, err)
			}
		})
	}
}

// A document that refers to itself and is reached through a $ref -- the shape of
// every JSON Schema meta-schema. Generation used to recurse without bound and
// exhaust memory; once that terminated, the output still failed to build with
// "invalid recursive type", because the self-reference was emitted by value.
// Only compiling it catches the second half.
func TestSelfReferentialDocumentCompiles(t *testing.T) {
	src := t.TempDir()
	// Reduced from the draft-04 meta-schema: a property whose anyOf refs the
	// document root, which in turn declares that property.
	writeFile(t, filepath.Join(src, "meta.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/meta.json",
		"type": "object",
		"properties": {
			"additionalItems": {"anyOf": [{"type": "boolean"}, {"$ref": "#"}]},
			"items": {"anyOf": [{"$ref": "#"}, {"type": "array", "items": {"$ref": "#"}}]},
			"title": {"type": "string"}
		}
	}`)
	writeFile(t, filepath.Join(src, "root.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/root.json",
		"title": "Doc",
		"$ref": "meta.json"
	}`)

	out := t.TempDir()
	if err := runGenerateArgs(t, filepath.Join(src, "root.json"), "-o", out, "-p", "gen"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if buildOut, err := buildGenerated(t, out, "example.com/selfref"); err != nil {
		t.Errorf("generated output does not compile: %v\n%s", err, buildOut)
	}
}

// An array definition whose items refer back to the document that declares the
// property pointing at that definition. Resolving the item type re-enters
// generation for the array under the same name, and the "already generated"
// flag used to be set only after that descent returned -- so the array was
// declared twice and the output did not build. Reaching the document through
// allOf is what makes the second entry a fresh one rather than a cycle the
// in-progress guard already covers.
func TestArrayDefinitionReachedThroughItsOwnItemsCompiles(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "meta.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/meta.json",
		"type": "object",
		"properties": {
			"allOf": {"$ref": "#/definitions/schemaArray"},
			"title": {"type": "string"}
		},
		"definitions": {
			"schemaArray": {"type": "array", "items": {"$ref": "#"}}
		}
	}`)
	writeFile(t, filepath.Join(src, "root.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/root.json",
		"title": "Doc",
		"allOf": [{"$ref": "meta.json"}]
	}`)

	out := t.TempDir()
	if err := runGenerateArgs(t, filepath.Join(src, "root.json"), "-o", out, "-p", "gen"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if buildOut, err := buildGenerated(t, out, "example.com/arraydup"); err != nil {
		t.Errorf("generated output does not compile: %v\n%s", err, buildOut)
	}
}
