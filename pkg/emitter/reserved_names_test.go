package emitter

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path"
	"path/filepath"
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

// renderEveryFixedDeclaration renders the helper file with a pattern in it, and
// a schema file carrying the validation-capability block, and returns the two
// sources.
func renderEveryFixedDeclaration(t *testing.T) [][]byte {
	t.Helper()
	e := mustNew(t)
	if _, err := generator.PatternVarName("^a$"); err != nil {
		t.Fatal(err)
	}
	helpers, ok, err := e.EmitHelpers("model", generator.HelperSet{Patterns: []string{"^a$"}})
	if err != nil || !ok {
		t.Fatalf("EmitHelpers: ok=%v err=%v", ok, err)
	}
	file, err := e.Emit(&generator.File{
		PackageName: "model",
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
	// The pattern variables are named by PatternVarName: an underscore, a fixed
	// prefix and sixteen hex digits. No name the generator derives from a
	// schema begins with an underscore, so none can land on one, and a table
	// entry per pattern is not something the registry could hold.
	patternVar, err := generator.PatternVarName("^a$")
	if err != nil {
		t.Fatal(err)
	}
	if !decls[patternVar] {
		t.Errorf("the helper file declares no variable for its pattern (%s): %v", patternVar, sortedSet(decls))
	}
	delete(decls, patternVar)
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

// TestReservedImportNamesMatchTheGenerator holds the table of reserved import
// names to the imports the generator claims for a file (its GeneratedImport
// calls, read from source) and to the import the helper file carries, in both
// directions. Since the helpers moved into the runtime module, the helper file
// imports the runtime package and nothing else, so the packages a schema file can
// name are the ones the generator's model of a file's imports asks for.
func TestReservedImportNamesMatchTheGenerator(t *testing.T) {
	claimed := generatorClaimedImports(t)
	table := generator.GeneratedImportNames()
	for _, p := range sortedSet(claimed) {
		if _, ok := table[p]; !ok {
			t.Errorf("the generator claims the import %q and generator/reserved.go does not list it", p)
		}
	}
	for _, p := range sortedKeysOf(table) {
		if !claimed[p] {
			t.Errorf("generator/reserved.go lists the import %q, which the generator never claims; drop the stale entry", p)
		}
	}
	_, imports := fixedDeclarations(t, renderEveryFixedDeclaration(t))
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
	if imports[generator.RuntimeImportPath] != generator.RuntimeAlias {
		t.Errorf("the helper file does not import the runtime module as %s: %v", generator.RuntimeAlias, imports)
	}
}

// generatorClaimedImports is every path the generator passes to GeneratedImport,
// read from its source: a string literal, or the runtime module's constant.
func generatorClaimedImports(t *testing.T) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "generator", "generator.go"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), "generator.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	claimed := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "GeneratedImport" {
			return true
		}
		switch a := call.Args[0].(type) {
		case *ast.BasicLit:
			if p, err := strconv.Unquote(a.Value); err == nil {
				claimed[p] = true
			}
		case *ast.Ident:
			if a.Name == "RuntimeImportPath" {
				claimed[generator.RuntimeImportPath] = true
			}
		}
		return true
	})
	if len(claimed) < 8 {
		t.Fatalf("read only %d claimed imports from generator.go; the scan is not reading it", len(claimed))
	}
	return claimed
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
