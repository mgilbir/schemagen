package testgo

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The throwaway modules generated code is compiled in.
//
// Generated code imports the runtime module (github.com/mgilbir/schemagen/
// runtime), which is the repository's ./runtime directory: a module of its own,
// with a go.mod that requires the ECMA-262 engine and x/net/idna and nothing
// else. A throwaway module reaches it by a replace onto this checkout, so a test
// always builds the generated code against the runtime that is being changed --
// and never against a release, which a test could not fetch offline anyway.
//
// The versions and hashes are pinned here as well as in go.mod files because the
// harness builds a throwaway module per test group, and a module with no go.sum
// entry cannot build offline.
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

	// RuntimeModulePath is the import path of the runtime module.
	RuntimeModulePath = "github.com/mgilbir/schemagen/runtime"

	// GoDirective is the go directive of the throwaway modules, and the oldest
	// Go a module holding generated code is promised to build with. The
	// runtime's own go.mod declares the same one: a runtime that required a
	// newer Go than the module importing it would break that module's build with
	// a toolchain message, and TestRuntimeGoDirectiveIsCoherent holds the two,
	// and the runtime's dependencies, to it.
	GoDirective = "1.23.0"
)

var (
	repoRootOnce sync.Once
	repoRootDir  string
	repoRootErr  error
)

// RepoRoot is the absolute path of the repository root: the nearest directory,
// walking up from the one the test runs in, whose go.mod declares this module.
func RepoRoot() (string, error) {
	repoRootOnce.Do(func() {
		dir, err := os.Getwd()
		if err != nil {
			repoRootErr = err
			return
		}
		for {
			if f, err := os.Open(filepath.Join(dir, "go.mod")); err == nil {
				first, _ := bufio.NewReader(f).ReadString('\n')
				f.Close()
				if strings.TrimSpace(first) == "module github.com/mgilbir/schemagen" {
					repoRootDir = dir
					return
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				repoRootErr = fmt.Errorf("testgo: no go.mod for module github.com/mgilbir/schemagen above the working directory")
				return
			}
			dir = parent
		}
	})
	return repoRootDir, repoRootErr
}

// RuntimeDir is the absolute path of the runtime module in this checkout.
func RuntimeDir() (string, error) {
	root, err := RepoRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "runtime"), nil
}

// GoMod is the go.mod of a throwaway module named moduleName that compiles
// generated code: the runtime module, replaced onto this checkout, and the
// modules the runtime imports.
//
// All are required unconditionally rather than by inspecting the emitted source:
// an unused require is harmless, a missing one is a build failure in a
// throwaway module nobody will read the go.mod of. x/text is indirect: nothing
// generated imports it, but x/net/idna does, and a module that names only its
// direct requirements does not build offline.
func GoMod(moduleName string) (string, error) {
	rt, err := RuntimeDir()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("module %s\n\ngo %s\n\nrequire (\n\tgithub.com/mgilbir/goecma262 %s\n\t%s v0.0.0\n\tgolang.org/x/net %s\n)\n\nrequire golang.org/x/text %s // indirect\n\nreplace %s => %s\n",
		moduleName, GoDirective, Goecma262Version, RuntimeModulePath, XnetVersion, XtextVersion, RuntimeModulePath, rt), nil
}

// GoSum is the go.sum body naming every module GoMod requires. The runtime module
// is replaced by a directory, which needs none.
func GoSum() string {
	return fmt.Sprintf(
		"github.com/mgilbir/goecma262 %s %s\ngithub.com/mgilbir/goecma262 %s/go.mod %s\n"+
			"golang.org/x/net %s %s\ngolang.org/x/net %s/go.mod %s\n"+
			"golang.org/x/text %s %s\ngolang.org/x/text %s/go.mod %s\n",
		Goecma262Version, Goecma262H1, Goecma262Version, Goecma262GoMod,
		XnetVersion, xnetH1, XnetVersion, xnetGoMod,
		XtextVersion, xtextH1, XtextVersion, xtextGoMod)
}

// WriteModule writes the go.mod and go.sum of a throwaway module named
// moduleName into dir. See GoMod.
func WriteModule(dir, moduleName string) error {
	mod, err := GoMod(moduleName)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		return fmt.Errorf("write go.mod: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(GoSum()), 0o644); err != nil {
		return fmt.Errorf("write go.sum: %w", err)
	}
	return nil
}
