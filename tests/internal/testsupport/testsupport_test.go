package testsupport

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/internal/testgo"
)

// TestMain installs testgo.Main: this package runs the go tool, so its own test
// binary is held to the rule it asks of every importer.
func TestMain(m *testing.M) { testgo.Main(m) }

// testsDir is tests/, seen from this package's directory, tests/internal/testsupport.
const testsDir = "../.."

// TestEveryTestPackageSitsOneLevelBelowTests holds the layout Root is written
// for: every package under tests/ that holds tests is a direct child of tests/,
// so that Root, "../..", is the repository root from the directory go test runs
// it in. A package one level deeper would read every fixture, golden and corpus
// path from a directory that is not the repository, and the failures would say
// "no such file" about files that plainly exist.
//
// tests/internal is exempt: it is where the shared harness lives, not a set of
// tests, and this package's own tests do not read through Root.
func TestEveryTestPackageSitsOneLevelBelowTests(t *testing.T) {
	var packages, misplaced []string
	err := filepath.WalkDir(testsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(testsDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if d.Name() == "testdata" || rel == "internal" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		if dir == "." || strings.Contains(dir, "/") {
			misplaced = append(misplaced, rel)
			return nil
		}
		packages = append(packages, dir)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	packages = dedupe(packages)

	// A walk that found nothing is measuring nothing: tests/ has a dozen
	// packages.
	if len(packages) < 10 {
		t.Fatalf("found only %d test packages under tests/: %v; the walk is not looking at the tree", len(packages), packages)
	}
	for _, rel := range dedupe(misplaced) {
		t.Errorf("tests/%s is not in a directory directly below tests/, so testsupport.Root (%q) is not the "+
			"repository root where it runs; move it into one of %v or a new sibling of them", rel, Root, packages)
	}
	// And Root, from each of them, is this module.
	for _, p := range packages {
		mod, err := os.ReadFile(filepath.Join(testsDir, p, Root, "go.mod"))
		if err != nil {
			t.Errorf("tests/%s: %s/go.mod: %v", p, Root, err)
			continue
		}
		if !strings.HasPrefix(string(mod), "module github.com/mgilbir/schemagen\n") {
			t.Errorf("tests/%s: %s/go.mod is not this module's", p, Root)
		}
	}
}

// dedupe returns the distinct members of s, sorted.
func dedupe(s []string) []string {
	sorted := append([]string(nil), s...)
	sort.Strings(sorted)
	var out []string
	for i, v := range sorted {
		if i == 0 || v != sorted[i-1] {
			out = append(out, v)
		}
	}
	return out
}
