package runtime

import (
	"bytes"
	"flag"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var updateAPI = flag.Bool("update-api", false, "rewrite testdata/api.txt from the exported API")

// apiFile is the exported API as of the last time a person looked at it.
const apiFile = "testdata/api.txt"

// exportedAPI renders every exported top-level declaration of this package, one
// per entry, without bodies, comments or unexported fields, sorted. It is the
// surface generated code compiles against, so it is what has to stay put.
func exportedAPI(t *testing.T) (entries []string, undocumented []string) {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	pkg := pkgs["runtime"]
	if pkg == nil {
		t.Fatal("package runtime not found in the working directory")
	}
	for _, f := range pkg.Files {
		// Drops the unexported declarations, and the unexported fields and
		// methods of the exported ones.
		if !ast.FileExports(f) {
			continue
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				// A method of an unexported type is not part of the surface:
				// nothing outside the package can name the type, only reach
				// it through an interface it satisfies.
				if d.Recv != nil && len(d.Recv.List) == 1 && !ast.IsExported(receiverBase(d.Recv.List[0].Type)) {
					continue
				}
				if d.Doc == nil {
					label := "func " + d.Name.Name
					if d.Recv != nil && len(d.Recv.List) == 1 {
						label = "method (" + render(t, fset, d.Recv.List[0].Type) + ") " + d.Name.Name
					}
					undocumented = append(undocumented, label)
				}
				d.Body, d.Doc = nil, nil
				entries = append(entries, render(t, fset, d))
			case *ast.GenDecl:
				if d.Tok == token.IMPORT {
					continue
				}
				for _, spec := range d.Specs {
					documented := d.Doc != nil
					switch s := spec.(type) {
					case *ast.TypeSpec:
						documented = documented || s.Doc != nil
						s.Doc, s.Comment = nil, nil
						if !documented {
							undocumented = append(undocumented, "type "+s.Name.Name)
						}
					case *ast.ValueSpec:
						documented = documented || s.Doc != nil
						s.Doc, s.Comment = nil, nil
						if !documented {
							undocumented = append(undocumented, d.Tok.String()+" "+s.Names[0].Name)
						}
					}
					single := &ast.GenDecl{Tok: d.Tok, Specs: []ast.Spec{spec}}
					entries = append(entries, render(t, fset, single))
				}
			}
		}
	}
	sort.Strings(entries)
	sort.Strings(undocumented)
	return entries, undocumented
}

// receiverBase is the name of the type a method is declared on.
func receiverBase(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		case *ast.Ident:
			return x.Name
		default:
			return ""
		}
	}
}

func render(t *testing.T, fset *token.FileSet, n ast.Node) string {
	t.Helper()
	var buf bytes.Buffer
	if err := (&printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}).Fprint(&buf, fset, n); err != nil {
		t.Fatal(err)
	}
	// One entry is one line, so a diff names the declaration that moved.
	return strings.Join(strings.Fields(buf.String()), " ")
}

// TestTheExportedAPIIsTheOneThatWasReviewed holds the runtime's exported surface
// to testdata/api.txt.
//
// Generated code is compiled against this package by people who did not
// generate it with the runtime they build with, so what it exports is a
// compatibility promise: a name, a signature or a type that changes turns a
// build that worked into one that does not. The file is the list a reviewer
// reads. A removed or changed line is a break; an added one is an addition, and
// is fine before a release and belongs to the next API level after one (see the
// package documentation). Either way the change has to be made on purpose:
//
//	go test ./runtime -run TestTheExportedAPIIsTheOneThatWasReviewed -update-api
func TestTheExportedAPIIsTheOneThatWasReviewed(t *testing.T) {
	entries, _ := exportedAPI(t)
	if len(entries) < 100 {
		t.Fatalf("found only %d exported declarations; the scan is not reading the package", len(entries))
	}
	got := strings.Join(entries, "\n") + "\n"
	if *updateAPI {
		if err := os.MkdirAll(filepath.Dir(apiFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(apiFile, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	wantBytes, err := os.ReadFile(apiFile)
	if err != nil {
		t.Fatalf("reading %s: %v (create it with -update-api)", apiFile, err)
	}
	want := string(wantBytes)
	if got == want {
		return
	}
	wantSet, gotSet := map[string]bool{}, map[string]bool{}
	for _, l := range strings.Split(strings.TrimSpace(want), "\n") {
		wantSet[l] = true
	}
	for _, l := range strings.Split(strings.TrimSpace(got), "\n") {
		gotSet[l] = true
	}
	var removed, added []string
	for l := range wantSet {
		if !gotSet[l] {
			removed = append(removed, l)
		}
	}
	for l := range gotSet {
		if !wantSet[l] {
			added = append(added, l)
		}
	}
	sort.Strings(removed)
	sort.Strings(added)
	t.Errorf("the exported API is not the one in %s.\n"+
		"Removed or changed (a compatibility break for generated code):\n\t%s\n"+
		"Added (an addition to the API):\n\t%s\n"+
		"If this is meant, review it and run this test with -update-api.",
		apiFile, strings.Join(removed, "\n\t"), strings.Join(added, "\n\t"))
}

// TestEveryExportedNameIsDocumented holds the exported surface to being
// documented: it is a compatibility promise, and a promise nobody can read is
// not one.
func TestEveryExportedNameIsDocumented(t *testing.T) {
	_, undocumented := exportedAPI(t)
	if len(undocumented) > 0 {
		t.Errorf("%d exported declarations have no doc comment:\n\t%s", len(undocumented), strings.Join(undocumented, "\n\t"))
	}
}

// TestTheRuntimeImportsNothingOfSchemagen holds the module to what it says it
// depends on. Generated code imports this module and nothing else of this
// repository, so a runtime that imported the main module would drag the
// generator, cobra and everything else into every user's build -- and a cycle
// besides, since the main module would then have to require a runtime that
// requires it.
//
// It reads the import declarations of every Go file in the module, tests
// included, and the module file's requirements.
func TestTheRuntimeImportsNothingOfSchemagen(t *testing.T) {
	const self = "github.com/mgilbir/schemagen/runtime"
	checked := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(p, "github.com/mgilbir/schemagen") && p != self && !strings.HasPrefix(p, self+"/") {
				t.Errorf("%s imports %s: the runtime must not import the main module", path, p)
			}
			checked++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no import declaration read; this test is watching nothing")
	}
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(mod), "\n") {
		f := strings.Fields(line)
		if len(f) >= 1 && f[0] == "require" {
			f = f[1:]
		}
		if len(f) >= 2 && f[0] == "github.com/mgilbir/schemagen" {
			t.Errorf("go.mod requires %s: the runtime must not depend on the main module", f[0])
		}
	}
	if !strings.Contains(string(mod), "module "+self+"\n") {
		t.Errorf("go.mod does not declare module %s", self)
	}
}
