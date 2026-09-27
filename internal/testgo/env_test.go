package testgo

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// moduleRoot is the repository root, two levels above this package.
const moduleRoot = "../.."

// TestEnvIsolatesAThrowawayBuildFromTheCallersGoSettings watches the three
// replacements Env makes actually reach a build, rather than reading them back
// out of a slice.
//
// GOFLAGS is the one with teeth. A developer who runs the suite from a shell
// exporting GOFLAGS=-mod=vendor, -race or a build tag used to have it applied
// to every throwaway module the tests compiled as well: at best a slower run,
// at worst every generated-code test failing for a reason that has nothing to
// do with the generator. The fixture makes that observable: its only file
// fails to compile under a build tag, and the caller's GOFLAGS carries the tag.
// Were GOFLAGS inherited, the build would fail.
func TestEnvIsolatesAThrowawayBuildFromTheCallersGoSettings(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module goflagsfixture\n\ngo 1.23.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	poisoned := "//go:build schemagen_goflags_leak\n\npackage main\n\nvar _ = this_does_not_compile\n"
	if err := os.WriteFile(filepath.Join(src, "leak.go"), []byte(poisoned), 0o644); err != nil {
		t.Fatal(err)
	}

	elsewhere := t.TempDir()
	t.Setenv("GOFLAGS", "-tags=schemagen_goflags_leak")
	t.Setenv("GOCACHE", filepath.Join(elsewhere, "developer-cache"))
	t.Setenv("GOWORK", filepath.Join(elsewhere, "go.work"))

	cmd := Command(context.Background(), src, "build", "-o", filepath.Join(src, "bin"), ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("a throwaway build failed under the caller's GOFLAGS/GOWORK, so those reached it: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "developer-cache")); err == nil {
		t.Errorf("the build wrote into the caller's GOCACHE; generated code has no business in the developer's cache")
	}

	// And the environment says exactly one thing for each, the thing asked for.
	counts := map[string][]string{}
	for _, e := range cmd.Env {
		k, v, _ := strings.Cut(e, "=")
		counts[k] = append(counts[k], v)
	}
	want := map[string]string{"GOCACHE": sharedCacheDir, "GOFLAGS": "-trimpath", "GOWORK": "off"}
	for k, v := range want {
		if got := counts[k]; len(got) != 1 || got[0] != v {
			t.Errorf("Env sets %s to %q; want exactly %q", k, got, v)
		}
	}
}

// TestCommandRefusesABinaryThatNeverReleasesTheCache pins the guard that makes
// "every test binary that builds calls Main" something other than a convention.
func TestCommandRefusesABinaryThatNeverReleasesTheCache(t *testing.T) {
	mainInstalled = false
	defer func() { mainInstalled = true }()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("Command handed out the shared cache in a binary whose TestMain does not call Main; " +
				"that binary would leave the cache for the age-based sweep")
		}
		if !strings.Contains(r.(string), "testgo.Main") {
			t.Errorf("the refusal does not say what to do: %v", r)
		}
	}()
	Command(context.Background(), t.TempDir(), "version")
}

// TestNoTestRunsTheGoToolDirectly holds every Go file in the module to reaching
// the go tool through this package, which is the only way the rules above
// apply to it.
//
// It is a static check on purpose. The defect it replaces was not one wrong
// call site but forty: every test that compiled generated code had written its
// own exec.Command("go", ...), a handful with the shared cache, most without
// it, and none without the caller's GOFLAGS. A behavioural check can only watch
// the call sites it happens to run; this reads all of them, including the ones
// behind an environment variable nobody sets.
//
// What it refuses: exec.Command or exec.CommandContext naming "go" or "make"
// (whose recipes run go) as a literal, anywhere but this package's own
// Command; and a literal "GOCACHE=" outside this package, which is what a call
// site rolling its own environment looks like.
func TestNoTestRunsTheGoToolDirectly(t *testing.T) {
	var offences []string
	forEachGoFile(t, func(path string, f *ast.File, fset *token.FileSet) {
		inThisPackage := filepath.Dir(path) == filepath.Join(moduleRoot, "internal", "testgo")
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				name, ok := execCommandName(n)
				if !ok {
					return true
				}
				if (name == "go" || name == "make") && !inThisPackage {
					offences = append(offences, fset.Position(n.Pos()).String()+": exec runs "+strconv.Quote(name)+" directly; use testgo.Command (or testgo.MakeCommand)")
				}
			case *ast.BasicLit:
				if n.Kind == token.STRING && strings.Contains(n.Value, "GOCACHE=") && !inThisPackage {
					offences = append(offences, fset.Position(n.Pos()).String()+": sets GOCACHE by hand; use testgo.Env")
				}
			}
			return true
		})
	})
	for _, o := range offences {
		t.Error(o)
	}
}

// TestEveryTempDirectoryATestMakesIsOneTheSweepKnows is the other half of the
// leak: a directory made with os.MkdirTemp in the shared temp directory under a
// name no prefix covers is never reclaimed by anything when its test binary is
// killed. main_test.go and tests/crosspackage_agreement_test.go each built the
// CLI into one of those and never removed it even on success; 252 of them, 863
// MB, were on the audit machine.
//
// t.TempDir() is always fine -- the testing package removes it -- and so is a
// MkdirTemp inside a directory the test already owns. What is checked is a
// MkdirTemp whose parent is the temp directory itself: "" or os.TempDir().
func TestEveryTempDirectoryATestMakesIsOneTheSweepKnows(t *testing.T) {
	forEachGoFile(t, func(path string, f *ast.File, fset *token.FileSet) {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isSelector(call.Fun, "os", "MkdirTemp") || len(call.Args) != 2 {
				return true
			}
			if !isSharedTempDir(call.Args[0]) {
				return true
			}
			pos := fset.Position(call.Pos()).String()
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok {
				// The one non-literal allowed is this package's own, which checks
				// its prefix at run time.
				if filepath.Dir(path) != filepath.Join(moduleRoot, "internal", "testgo") {
					t.Errorf("%s: os.MkdirTemp in the temp directory with a computed name; the sweep cannot be shown to know it -- use testgo.MkdirWorkTemp or testgo.MkdirProcessTemp", pos)
				}
				return true
			}
			pattern, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Errorf("%s: %v", pos, err)
				return true
			}
			if !isNewDirPrefix(pattern) {
				t.Errorf("%s: os.MkdirTemp(%q) makes a directory in the temp directory under a name the sweep does not know, "+
					"so a killed run leaks it for good; use t.TempDir, testgo.MkdirWorkTemp or testgo.MkdirProcessTemp", pos, pattern)
			}
			return true
		})
	})
}

// forEachGoFile parses every .go file in the module, skipping testdata
// (generated goldens and fixtures, which are not code the tests run) and the
// external checkouts.
func forEachGoFile(t *testing.T, visit func(path string, f *ast.File, fset *token.FileSet)) {
	t.Helper()
	fset := token.NewFileSet()
	seen := 0
	err := filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", ".git", "vendor", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		seen++
		visit(path, f, fset)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}
	// A walk that found nothing proves nothing; the module has well over a
	// hundred Go files.
	if seen < 100 {
		t.Fatalf("walked only %d Go files from %s; the check is not looking at the module", seen, moduleRoot)
	}
}

// execCommandName returns the program an exec.Command or exec.CommandContext
// call names, when it is a string literal.
func execCommandName(call *ast.CallExpr) (string, bool) {
	var arg ast.Expr
	switch {
	case isSelector(call.Fun, "exec", "Command") && len(call.Args) >= 1:
		arg = call.Args[0]
	case isSelector(call.Fun, "exec", "CommandContext") && len(call.Args) >= 2:
		arg = call.Args[1]
	default:
		return "", false
	}
	lit, ok := arg.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// isSharedTempDir reports whether a MkdirTemp parent argument is the shared
// temp directory: the empty string, or os.TempDir().
func isSharedTempDir(e ast.Expr) bool {
	if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		s, err := strconv.Unquote(lit.Value)
		return err == nil && s == ""
	}
	if call, ok := e.(*ast.CallExpr); ok {
		return isSelector(call.Fun, "os", "TempDir")
	}
	return false
}

// isNewDirPrefix reports whether a MkdirTemp pattern starts with a prefix a new
// directory may be made under: a work-directory prefix or the process-directory
// one. The legacy prefixes are swept but closed to new directories.
func isNewDirPrefix(pattern string) bool {
	for _, p := range append(append([]string{}, WorkDirPrefixes...), ProcessDirPrefix) {
		if strings.HasPrefix(pattern, p) {
			return true
		}
	}
	return false
}
