package determinism

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"path/filepath"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
)

// listImporter resolves and type-checks imports from source, the way
// go/importer's "source" importer does, but by asking `go list` in a directory
// it is given and in the environment testgo.Env gives every go command a test
// runs. It reads and writes no process-wide state.
//
// The standard source importer cannot do that. It asks `go list` where a path
// is from go/build's process-wide default context, whose directory is the
// process's working directory, and whose environment is the process's. Generated
// code now imports the runtime module, which the main module does not require
// (so that `go install ...@latest` needs no tag of it), and only the throwaway
// module the packages are written into -- or the runtime's own directory -- can
// resolve it. Pointing the default context there for the length of a test is a
// mutation every other test in the process would see; and setting GOFLAGS or
// GOWORK in the environment for the same reason, which the old helper did, is
// the same kind of thing. A developer's -mod=vendor or go.work cannot change what
// this sees either: testgo.Env replaces them.
//
// One importer belongs to one goroutine. It type-checks dependencies without
// function bodies, which is all an importer is asked for.
type listImporter struct {
	fset *token.FileSet
	dir  string
	pkgs map[string]*types.Package
	info map[string]*listedPackage
}

// listedPackage is the part of `go list -json` this reads.
type listedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	ImportMap  map[string]string
	Error      *struct{ Err string }
}

func newListImporter(fset *token.FileSet, dir string) *listImporter {
	return &listImporter{fset: fset, dir: dir, pkgs: map[string]*types.Package{}, info: map[string]*listedPackage{}}
}

func (l *listImporter) Import(path string) (*types.Package, error) {
	return l.ImportFrom(path, l.dir, 0)
}

// ImportFrom resolves path in the module of the directory the importer was made
// for. srcDir is ignored: every package one importer is asked about is in that
// module.
func (l *listImporter) ImportFrom(path, srcDir string, mode types.ImportMode) (*types.Package, error) {
	if path == "unsafe" {
		return types.Unsafe, nil
	}
	if p, ok := l.pkgs[path]; ok {
		return p, nil
	}
	if _, ok := l.info[path]; !ok {
		if err := l.list(path); err != nil {
			return nil, err
		}
	}
	return l.check(path)
}

// list runs `go list -deps` for path and remembers every package it reports.
// cgo is off, as it is for the compile the generated code is built with here, so
// that a package with a cgo and a pure Go implementation is read as the pure one.
func (l *listImporter) list(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := testgo.Command(ctx, l.dir, "list", "-e", "-json", "-deps", "--", path)
	cmd.Env = append(cmd.Env, "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("go list %s in %s: %v\n%s", path, l.dir, err, stderr.String())
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listedPackage
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if _, seen := l.info[p.ImportPath]; !seen {
			pp := p
			l.info[p.ImportPath] = &pp
		}
	}
	if p, ok := l.info[path]; !ok || p.Error != nil {
		msg := "not listed"
		if ok {
			msg = p.Error.Err
		}
		return fmt.Errorf("cannot resolve %s in %s: %s", path, l.dir, msg)
	}
	return nil
}

func (l *listImporter) check(path string) (*types.Package, error) {
	if p, ok := l.pkgs[path]; ok {
		return p, nil
	}
	lp := l.info[path]
	var files []*ast.File
	for _, name := range lp.GoFiles {
		f, err := parser.ParseFile(l.fset, filepath.Join(lp.Dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	conf := types.Config{
		Importer:         listedImports{l, lp},
		IgnoreFuncBodies: true,
		FakeImportC:      true,
		Error:            func(error) {}, // a dependency's own errors are its build's to report
	}
	pkg, _ := conf.Check(lp.ImportPath, l.fset, files, nil)
	if pkg == nil {
		return nil, fmt.Errorf("type-checking %s failed", path)
	}
	l.pkgs[path] = pkg
	return pkg, nil
}

// listedImports is the importer one dependency is checked with: a path written
// in its source is mapped through the package's own ImportMap first, which is how
// the standard library's vendored packages are named.
type listedImports struct {
	l  *listImporter
	lp *listedPackage
}

func (li listedImports) Import(path string) (*types.Package, error) {
	return li.ImportFrom(path, "", 0)
}

func (li listedImports) ImportFrom(path, _ string, _ types.ImportMode) (*types.Package, error) {
	if mapped, ok := li.lp.ImportMap[path]; ok {
		path = mapped
	}
	if path == "unsafe" {
		return types.Unsafe, nil
	}
	if p, ok := li.l.pkgs[path]; ok {
		return p, nil
	}
	if _, ok := li.l.info[path]; !ok {
		return nil, fmt.Errorf("%s imports %s, which go list did not report", li.lp.ImportPath, path)
	}
	return li.l.check(path)
}
