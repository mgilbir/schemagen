package testsupport

import (
	"os"
	"path/filepath"
	"testing"
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
	return out, boms
}
