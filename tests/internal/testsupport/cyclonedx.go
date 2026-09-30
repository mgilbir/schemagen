package testsupport

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"testing"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
)

// CycloneDXModule generates the CycloneDX 1.6 types into a module of their own,
// as package ex.test/cdx/cdx, with extra files written beside them -- paths
// relative to the module root -- and returns the module root and the BOMs.
func CycloneDXModule(t *testing.T, extra map[string]string) (string, []string) {
	t.Helper()
	dir, err := filepath.Abs(RepoPath("testdata", "cyclonedx-1.6"))
	if err != nil {
		t.Fatal(err)
	}
	boms, err := filepath.Glob(filepath.Join(dir, "boms", "valid-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(boms) < 40 {
		t.Fatalf("found %d BOMs under %s; the corpus is 45", len(boms), dir)
	}
	bin := SchemagenBinary(t)
	out := t.TempDir()
	RunSchemagen(t, bin, "generate", filepath.Join(dir, "schema", "bom-1.6.schema.json"),
		"-o", filepath.Join(out, "cdx"), "-p", "cdx", "--root-name", "bom-1.6.schema.json=Bom")
	if err := WriteTestGoMod(out, "ex.test/cdx"); err != nil {
		t.Fatal(err)
	}
	// maporder: writes each file to its own path, which no order of the loop changes.
	for rel, content := range extra {
		p := filepath.Join(out, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	RewriteHelperFile(t, filepath.Join(out, "cdx"))
	return out, boms
}

// compiledPatternLiteral is a compiled pattern's declaration in a helper file,
// its pattern the Go string literal it is compiled from.
var compiledPatternLiteral = regexp.MustCompile(`_schemagenPattern_[0-9a-f]+ += _schemagenCompilePattern\(("(?:[^"\\]|\\.)*")`)

// RewriteHelperFile writes the helper file of the generated package in dir
// again, for every file now in it: a file a test added to the package -- the
// identity check -- calls helpers the generated files do not, and the helper
// file holds only what the files it was written for reach (see the emitter's
// pruneHelpers). What it is written from is what the CLI writes it from.
func RewriteHelperFile(t *testing.T, dir string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var set generator.HelperSet
	pkg := ""
	for _, f := range files {
		if filepath.Base(f) == "schemagen_helpers.go" {
			// The compiled patterns are named in the generated files by a
			// variable the generating process registered; this process did not
			// generate them, so they are read back from the file itself.
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var patterns []string
			for _, m := range compiledPatternLiteral.FindAllStringSubmatch(string(b), -1) {
				p, err := strconv.Unquote(m[1])
				if err != nil {
					t.Fatal(err)
				}
				patterns = append(patterns, p)
			}
			sort.Strings(patterns)
			set.Merge(generator.HelperSet{Patterns: patterns})
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		set.Merge(generator.HelpersReferencedBy(string(b)))
		pkg = PackageNameOf(string(b))
	}
	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}
	src, needed, err := em.EmitHelpers(pkg, set)
	if err != nil {
		t.Fatal(err)
	}
	if needed {
		if err := os.WriteFile(filepath.Join(dir, "schemagen_helpers.go"), src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
