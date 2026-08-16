package schemagen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A directory holds one Go package, and the guard for that read only one way
// round: two packages writing into one directory was refused, one package
// writing into two was not. The comment on the helper-file emitter said "every
// input of a package writes into the same directory (enforced above)" and
// nothing enforced it. The result was a tree that does not compile behind a zero
// exit code -- the helper file in whichever directory the package's last input
// landed in, every other directory calling helpers nothing declares, and both
// directories declaring the same package clause under import paths nobody asked
// for. Issue #316.

const splitPkgDocA = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"$id": "https://ex.test/a.json",
	"title": "A", "type": "object",
	"properties": {"s": {"type": "string", "minLength": 3}},
	"required": ["s"]}`

const splitPkgDocB = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"$id": "https://ex.test/b.json",
	"title": "B", "type": "object",
	"properties": {"s": {"type": "string", "maxLength": 5}},
	"required": ["s"]}`

// b.json refs a.json, the case that also loses the reference the shared package
// assignment was for: A is declared in the other directory with no import.
const splitPkgDocBRefsA = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"$id": "https://ex.test/b.json",
	"title": "B", "type": "object",
	"properties": {"a": {"$ref": "https://ex.test/a.json"}},
	"required": ["a"]}`

// goFilesUnder lists every generated .go file in a tree, so a refusal can be
// held to leaving nothing behind rather than to the wording of its message.
func goFilesUnder(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".go") {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			found = append(found, rel)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return found
}

func TestSchemaOutputRefusesOnePackageInTwoDirectories(t *testing.T) {
	dir, paths := writeSchemas(t, "a.json", splitPkgDocA, "b.json", splitPkgDocB)
	out := filepath.Join(dir, "out")

	stderr, err := runGenerateCapturing(t, paths[0], paths[1], "-o", out,
		"--schema-package", "https://ex.test/a.json=ex.test/m/p",
		"--schema-package", "https://ex.test/b.json=ex.test/m/p",
		"--schema-output", "https://ex.test/a.json="+filepath.Join(out, "x", "a.go"),
		"--schema-output", "https://ex.test/b.json="+filepath.Join(out, "y", "b.go"))
	if err == nil {
		t.Fatalf("one package written into two directories must not succeed; stderr:\n%s", stderr)
	}
	msg := err.Error()
	if !strings.Contains(msg, `package "ex.test/m/p" writes into two directories`) {
		t.Errorf("the refusal should name the package and what it did, got: %v", msg)
	}
	// The refusal has to point at the setting to change, and here that is the flag.
	if !strings.Contains(msg, "--schema-output") {
		t.Errorf("the refusal should name where the paths came from, got: %v", msg)
	}
	// A refusal that had already written half the tree would be the same defect
	// in a different shape.
	if files := goFilesUnder(t, out); len(files) != 0 {
		t.Errorf("a refused run must write nothing, got: %v", files)
	}
}

// The same tree from the config file. #316 filed both spellings, and the
// config is the one built for multi-document runs, where the mistake is
// hardest to see.
func TestConfigOutputRefusesOnePackageInTwoDirectories(t *testing.T) {
	dir, paths := writeSchemas(t, "a.json", splitPkgDocA, "b.json", splitPkgDocB)
	out := filepath.Join(dir, "out")
	cfgPath := filepath.Join(dir, "cfg.json")
	writeFile(t, cfgPath, `{
		"outputDir": `+quoteJSON(out)+`,
		"documents": [
			{"id": "https://ex.test/a.json", "path": `+quoteJSON(paths[0])+`,
			 "package": "ex.test/m/p", "output": `+quoteJSON(filepath.Join(out, "x", "a.go"))+`},
			{"id": "https://ex.test/b.json", "path": `+quoteJSON(paths[1])+`,
			 "package": "ex.test/m/p", "output": `+quoteJSON(filepath.Join(out, "y", "b.go"))+`}
		]}`)

	stderr, err := runGenerateCapturing(t, "--config", cfgPath)
	if err == nil {
		t.Fatalf("the config spelling must be refused too; stderr:\n%s", stderr)
	}
	msg := err.Error()
	if !strings.Contains(msg, `package "ex.test/m/p" writes into two directories`) {
		t.Errorf("the refusal should name the package and what it did, got: %v", msg)
	}
	// "--schema-output" would send the reader to a command line that does not
	// contain it, the same reason warnUnmatchedDocumentKeys names the config.
	if !strings.Contains(msg, "config output") {
		t.Errorf("the refusal should name the config as the source, got: %v", msg)
	}
	if strings.Contains(msg, "--schema-output") {
		t.Errorf("no --schema-output was given on this run, got: %v", msg)
	}
	if files := goFilesUnder(t, out); len(files) != 0 {
		t.Errorf("a refused run must write nothing, got: %v", files)
	}
}

// One document routed elsewhere and the other left at the default layout splits
// the package just as surely, and names only one flag.
func TestOneExplicitOutputSplittingAPackageFromItsDefaultIsRefused(t *testing.T) {
	dir, paths := writeSchemas(t, "a.json", splitPkgDocA, "b.json", splitPkgDocB)
	out := filepath.Join(dir, "out")

	_, err := runGenerateCapturing(t, paths[0], paths[1], "-o", out,
		"--schema-package", "https://ex.test/a.json=ex.test/m/p",
		"--schema-package", "https://ex.test/b.json=ex.test/m/p",
		"--schema-output", "https://ex.test/a.json="+filepath.Join(out, "x", "a.go"))
	if err == nil {
		t.Fatal("a package half at its default path and half elsewhere must not succeed")
	}
	if !strings.Contains(err.Error(), "the default layout") {
		t.Errorf("the side that came from neither flag nor config should say so, got: %v", err)
	}
	if files := goFilesUnder(t, out); len(files) != 0 {
		t.Errorf("a refused run must write nothing, got: %v", files)
	}
}

// The cross-$ref case: the two documents share a package precisely so the ref
// stays one Go type, and splitting the directories is what loses it.
func TestSplitPackageWithACrossReferenceIsRefused(t *testing.T) {
	dir, paths := writeSchemas(t, "a.json", splitPkgDocA, "b.json", splitPkgDocBRefsA)
	out := filepath.Join(dir, "out")

	_, err := runGenerateCapturing(t, paths[0], paths[1], "-o", out,
		"--schema-package", "https://ex.test/a.json=ex.test/m/p",
		"--schema-package", "https://ex.test/b.json=ex.test/m/p",
		"--schema-output", "https://ex.test/a.json="+filepath.Join(out, "x", "a.go"),
		"--schema-output", "https://ex.test/b.json="+filepath.Join(out, "y", "b.go"))
	if err == nil {
		t.Fatal("a split package holding a cross-document $ref must not succeed")
	}
	if files := goFilesUnder(t, out); len(files) != 0 {
		t.Errorf("a refused run must write nothing, got: %v", files)
	}
}

// The refusal must not cost the case --schema-output exists for: two documents
// of one package routed by hand into one directory. It generates, the helper
// file lands in that directory, and the tree compiles.
func TestTwoDocumentsOfOnePackageInOneDirectoryStillCompile(t *testing.T) {
	dir, paths := writeSchemas(t, "a.json", splitPkgDocA, "b.json", splitPkgDocBRefsA)
	out := filepath.Join(dir, "out")
	pkgDir := filepath.Join(out, "shared")

	stderr, err := runGenerateCapturing(t, paths[0], paths[1], "-o", out,
		"--schema-package", "https://ex.test/a.json=ex.test/m/shared",
		"--schema-package", "https://ex.test/b.json=ex.test/m/shared",
		"--schema-output", "https://ex.test/a.json="+filepath.Join(pkgDir, "a.go"),
		"--schema-output", "https://ex.test/b.json="+filepath.Join(pkgDir, "b.go"))
	if err != nil {
		t.Fatalf("one package in one directory is the supported case: %v\nstderr:\n%s", err, stderr)
	}
	if _, statErr := os.Stat(filepath.Join(pkgDir, helperFileName)); statErr != nil {
		t.Errorf("the package's helper file should be in its one directory: %v", statErr)
	}
	if buildOut, buildErr := buildGenerated(t, out, "ex.test/m"); buildErr != nil {
		t.Errorf("generated output does not compile: %v\n%s", buildErr, buildOut)
	}
}

// The default layout puts every document of a package in one directory, so the
// new guard must not fire on a plain multi-package run either.
func TestDefaultLayoutMultiPackageRunIsUnaffected(t *testing.T) {
	dir, paths := writeSchemas(t, "a.json", splitPkgDocA, "b.json", splitPkgDocBRefsA)
	out := filepath.Join(dir, "out")

	if _, err := runGenerateCapturing(t, paths[0], paths[1], "-o", out,
		"--schema-package", "https://ex.test/a.json=ex.test/m/apkg",
		"--schema-package", "https://ex.test/b.json=ex.test/m/bpkg"); err != nil {
		t.Fatalf("default multi-package layout: %v", err)
	}
	if buildOut, err := buildGenerated(t, out, "ex.test/m"); err != nil {
		t.Errorf("generated output does not compile: %v\n%s", err, buildOut)
	}
}
