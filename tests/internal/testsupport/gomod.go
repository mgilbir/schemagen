package testsupport

import (
	"fmt"
	"os"
	"path/filepath"
)

// Module metadata for the temp go.mod files the harnesses write.
//
// These are the modules *generated code* imports: the ECMA-262 engine for
// `pattern` and `format: regex`, and x/net/idna for the two hostname formats
// (which pulls x/text). They are pinned here as well as in the repository's own
// go.mod because the harness builds a throwaway module per test group, and a
// module with no go.sum entry cannot build offline.
const (
	Goecma262Version = "v0.2.0"
	Goecma262H1      = "h1:Ycgd4Gt6mjxAtNd5H1+K5kRm3bcq4uMM+vqZz2Fh3X4="
	Goecma262GoMod   = "h1:wQvOAFchLrhVSiF4JsSzH+yE6eLpc8gOBrvpuahNucI="

	// Pinned to the newest pair whose own go directive is 1.23, which is what
	// the throwaway modules below declare and what generated code should not
	// need more than. A later x/net raises the floor to Go 1.25 and would make
	// that the requirement for anyone whose schema names a hostname; the idna
	// behaviour is identical across the range, measured against this corpus.
	XnetVersion = "v0.38.0"
	xnetH1      = "h1:vRMAPTMaeGqVhG5QyLJHqNDwecKTomGeqbnfZyKlBI8="
	xnetGoMod   = "h1:ivrbrMbzFq5J41QOQh0siUuly180yBYtLp+CKbEaFx8="

	XtextVersion = "v0.24.0"
	xtextH1      = "h1:dd5Bzh4yt5KYA8f9CJHCP4FB4D51c2c6JvN37xJJkJ0="
	xtextGoMod   = "h1:L8rBsPeo2pSS+xqN0d5u2ikmjtmoJbDBT1b7nHvFCdU="
)

// WriteTestGoMod writes a go.mod and go.sum in dir naming every module the
// generated code may import. moduleName is the module name for the temp project
// (e.g. "compile_test", "roundtrip_test").
//
// All three are required unconditionally rather than by inspecting the emitted
// source: an unused require is harmless, a missing one is a build failure in a
// throwaway module nobody will read the go.mod of.
func WriteTestGoMod(dir, moduleName string) error {
	// x/text is indirect: nothing generated imports it, but x/net/idna does, and
	// a module that names only its direct requirements does not build offline.
	goMod := fmt.Sprintf("module %s\n\ngo 1.23.0\n\nrequire (\n\tgithub.com/mgilbir/goecma262 %s\n\tgolang.org/x/net %s\n)\n\nrequire golang.org/x/text %s // indirect\n",
		moduleName, Goecma262Version, XnetVersion, XtextVersion)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return fmt.Errorf("write go.mod: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(TestGoSum()), 0o644); err != nil {
		return fmt.Errorf("write go.sum: %w", err)
	}
	return nil
}

// TestGoSum is the go.sum body naming every module WriteTestGoMod requires.
func TestGoSum() string {
	return fmt.Sprintf(
		"github.com/mgilbir/goecma262 %s %s\ngithub.com/mgilbir/goecma262 %s/go.mod %s\n"+
			"golang.org/x/net %s %s\ngolang.org/x/net %s/go.mod %s\n"+
			"golang.org/x/text %s %s\ngolang.org/x/text %s/go.mod %s\n",
		Goecma262Version, Goecma262H1, Goecma262Version, Goecma262GoMod,
		XnetVersion, xnetH1, XnetVersion, xnetGoMod,
		XtextVersion, xtextH1, XtextVersion, xtextGoMod)
}

// WriteCogenGoMod writes the throwaway module's go.mod. It is WriteTestGoMod
// plus one thing: when the emitted code imports pkg/validationruntime -- which
// hybrid and runtime do as soon as the schema uses a keyword requiring runtime
// evaluation, unevaluatedProperties being the first the grammar reaches -- the
// import has to resolve to something.
//
// It resolves to a stub module rather than to this checkout. Replacing onto the
// repository would drag schemagen's own go.mod into the generated module's
// graph, so the throwaway module would need go.sum entries for cobra and
// everything else, and `go run` would refuse until they were written. The
// package being vendored is 41 lines and imports nothing but fmt, so a stub
// carrying just that file, declaring the same module path and requiring
// nothing, is both smaller and hermetic.
func WriteCogenGoMod(dir string, needsRuntime bool) error {
	if !needsRuntime {
		return WriteTestGoMod(dir, "cogen_test")
	}

	src, err := os.ReadFile(RepoPath("pkg", "validationruntime", "runtime.go"))
	if err != nil {
		return fmt.Errorf("read validationruntime source: %w", err)
	}
	stub := filepath.Join(dir, "schemagenstub")
	pkgDir := filepath.Join(stub, "pkg", "validationruntime")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return fmt.Errorf("stub dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "runtime.go"), src, 0o644); err != nil {
		return fmt.Errorf("write stub package: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stub, "go.mod"),
		[]byte("module github.com/mgilbir/schemagen\n\ngo 1.23\n"), 0o644); err != nil {
		return fmt.Errorf("write stub go.mod: %w", err)
	}

	goMod := fmt.Sprintf("module cogen_test\n\ngo 1.23.0\n\nrequire (\n\tgithub.com/mgilbir/goecma262 %s\n\tgithub.com/mgilbir/schemagen v0.0.0\n\tgolang.org/x/net %s\n)\n\nrequire golang.org/x/text %s // indirect\n\nreplace github.com/mgilbir/schemagen => ./schemagenstub\n",
		Goecma262Version, XnetVersion, XtextVersion)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return fmt.Errorf("write go.mod: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(TestGoSum()), 0o644); err != nil {
		return fmt.Errorf("write go.sum: %w", err)
	}
	return nil
}
