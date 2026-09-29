package emitter

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// The generator's name registry holds, before any name a schema derives, every
// identifier generated code spells as fixed text: the helper file's
// declarations, the validation-capability block's, and the names the files'
// imports are spelled under (generator/reserved.go). Those tables are a second
// statement of what these templates declare, so they are held to the templates
// here, by rendering them and reading back what the rendering declares -- in
// both directions. A helper added without a table entry is a name a schema can
// still take; an entry nothing declares any more is a name the registry keeps
// out of reach for nothing.

// renderEveryFixedDeclaration renders the helper file with every helper on, and
// a schema file carrying the validation-capability block, and returns the two
// sources.
func renderEveryFixedDeclaration(t *testing.T) [][]byte {
	t.Helper()
	e := mustNew(t)
	var h generator.HelperSet
	v := reflect.ValueOf(&h).Elem()
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).Kind() == reflect.Bool {
			v.Field(i).SetBool(true)
		}
	}
	h.AnnotationsFormats = []string{"date-time", "email", "hostname", "regex", "uri"}
	helpers, ok, err := e.EmitHelpers("model", h)
	if err != nil || !ok {
		t.Fatalf("EmitHelpers: ok=%v err=%v", ok, err)
	}
	file, err := e.Emit(&generator.File{
		PackageName: "model",
		Imports:     []generator.Import{generator.GeneratedImport("github.com/mgilbir/schemagen/pkg/validationruntime")},
		ValidationCapability: generator.ValidationCapability{
			Mode:            generator.ValidationModeHybrid,
			RequiresRuntime: true,
			RuntimeFeatures: []generator.ValidationFeature{generator.ValidationFeatureDynamicRef},
		},
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	return [][]byte{helpers, file}
}

// fixedDeclarations reads the package-level names and the import names out of
// rendered sources.
func fixedDeclarations(t *testing.T, sources [][]byte) (decls map[string]bool, imports map[string]string) {
	t.Helper()
	decls = map[string]bool{}
	imports = map[string]string{}
	for _, src := range sources {
		f, err := parser.ParseFile(token.NewFileSet(), "fixed.go", src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing rendered source: %v", err)
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("import path %s: %v", imp.Path.Value, err)
			}
			name := path.Base(p)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			imports[p] = name
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						decls[s.Name.Name] = true
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.Name != "_" {
								decls[n.Name] = true
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.Name != "init" {
					decls[d.Name.Name] = true
				}
			}
		}
	}
	return decls, imports
}

func TestReservedHelperIdentifiersMatchTheTemplates(t *testing.T) {
	decls, _ := fixedDeclarations(t, renderEveryFixedDeclaration(t))
	if len(decls) == 0 {
		t.Fatal("the renderings declare nothing; the scan is broken, not the tables")
	}
	listed := map[string]bool{}
	for _, name := range generator.HelperIdentifiers() {
		listed[name] = true
	}
	for _, name := range sortedSet(decls) {
		if !listed[name] {
			t.Errorf("the templates declare %s at package level and generator/reserved.go does not reserve it: "+
				"a type, constant or import alias the generator derives can take the name and the package "+
				"stops compiling", name)
		}
	}
	for _, name := range sortedSet(listed) {
		if !decls[name] {
			t.Errorf("generator/reserved.go reserves %s, which no helper rendering declares; drop the stale entry", name)
		}
	}
}

func TestReservedImportNamesMatchTheTemplates(t *testing.T) {
	_, imports := fixedDeclarations(t, renderEveryFixedDeclaration(t))
	table := generator.GeneratedImportNames()
	for _, p := range sortedKeysOf(imports) {
		want, ok := table[p]
		if !ok {
			t.Errorf("a generated file imports %q and generator/reserved.go does not list it", p)
			continue
		}
		if imports[p] != want {
			t.Errorf("a generated file spells %q as %s, and the registry reserves %s", p, imports[p], want)
		}
	}
	for _, p := range sortedKeysOf(table) {
		if _, ok := imports[p]; !ok {
			t.Errorf("generator/reserved.go lists the import %q, which no generated file carries; drop the stale entry", p)
		}
	}
}

// TestReservedPredeclaredIdentifiersCoverTheUniverse holds the predeclared list
// to the toolchain running the test. Declaring one of these at package scope,
// or importing a package under one, shadows it for generated code that uses it.
// The list may hold more than one toolchain declares (it is the union over the
// releases generated code supports), never less.
func TestReservedPredeclaredIdentifiersCoverTheUniverse(t *testing.T) {
	listed := map[string]bool{}
	for _, name := range generator.PredeclaredIdentifiers() {
		listed[name] = true
	}
	for _, name := range types.Universe.Names() {
		if !listed[name] {
			t.Errorf("Go's universe scope declares %s, which the name registry does not reserve", name)
		}
	}
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
