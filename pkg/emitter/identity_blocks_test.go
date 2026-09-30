package emitter

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// topLevelNames is every name a Go file declares at the top level of its
// package: its functions (not methods), types, variables and constants.
func topLevelNames(f *ast.File) []string {
	var names []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				names = append(names, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					names = append(names, s.Name.Name)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						names = append(names, n.Name)
					}
				}
			}
		}
	}
	sort.Strings(names)
	return names
}

// TestIdentityBlockTableMatchesTheTemplates holds the generator's table of the
// identity blocks -- which a generated file's names are matched against to pick
// the blocks it takes -- to the names each template block declares, rendered
// with and without the in-place decode its lazy arms are written for. A name
// added to a block and not to the table would never pull its block in; one
// moved between blocks would pull in the wrong one.
func TestIdentityBlockTableMatchesTheTemplates(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	table := generator.IdentityBlockDecls()
	if len(table) != 6 {
		t.Fatalf("the generator's table lists %d identity blocks; there are six", len(table))
	}
	for name, want := range table {
		want = append([]string(nil), want...)
		sort.Strings(want)
		for _, decode := range []bool{false, true} {
			var buf bytes.Buffer
			if err := e.tmpl.ExecuteTemplate(&buf, name, generator.HelperSet{Decode: decode}); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			f, err := parser.ParseFile(token.NewFileSet(), name, "package p\n"+buf.String(), parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("%s does not parse as Go on its own: %v", name, err)
			}
			if got := topLevelNames(f); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("%s (decode %v) declares %v; the generator's table lists %v", name, decode, got, want)
			}
		}
	}
}
