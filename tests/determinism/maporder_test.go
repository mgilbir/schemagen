package determinism

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/tests/internal/testsupport"
)

// The static half of the determinism gate: nothing the generator does may
// depend on the order a Go map is ranged in, unless the site says why it
// cannot.
//
// Go randomises map iteration on purpose, per range statement, so a loop over a
// map whose order reaches anything observable -- the order of the generated
// source, of the IR, of a warning list, which of several errors is returned --
// makes the tool's output a function of the run and not of the input. That is
// how a schema with five dependentSchemas beside unevaluatedProperties came to
// generate four different files in eight runs, and a generated Validate refused
// a document with four unknown keys for a different key from run to run.
//
// Every such loop was, individually, a convention someone had to remember:
// "sorted for determinism" is written above two dozen of them, and was missing
// from the ones that broke. The dynamic gate (TestGenerationIsDeterministic)
// catches a site only when a corpus schema drives it with enough members for
// the orders to differ -- Go's small maps yield only as many orders as they
// have entries, so a two-member map repeats its order most of the time -- and
// only for the schemas the corpus has. This one catches the site itself.
//
// What it accepts, for a `range` over a map-typed expression, a call to
// maps.Keys, maps.Values or maps.All, or reflect's MapRange and MapKeys:
//
//   - the collect-and-sort idiom, recognised structurally: a loop whose whole
//     body appends to one slice, where the next statement of the enclosing
//     block that mentions the slice sorts it by the element's own total order
//     (sort.Strings, sort.Ints, sort.Float64s, slices.Sort). A sort.Slice or a
//     SortFunc is not recognised, because a comparator that ties leaves the
//     tied members in the map's order;
//   - a maps.Keys or maps.Values that is the direct argument of slices.Sorted;
//   - anything annotated `maporder: <reason>` in a comment ending on the line
//     above it, or on its own line. The reason is required, and is where the
//     argument that the order cannot matter is written down -- a set being
//     filled, a copy under distinct keys, a predicate that returns the same
//     answer whichever member it stops at.
//
// Test files are exempt: what they print on failure is not the tool's output.

// mapOrderGuardedPackages are the directories whose non-test Go files make up
// what the schemagen binary and library run, and the non-test files of the
// harness the tests share. Listed rather than walked so that a package added to
// the module has to be added here by a person who decided it belongs;
// TestMapOrderGuardCoversEveryPackage refuses one that is missing.
var mapOrderGuardedPackages = []string{
	".",
	"cmd/schemagen",
	"internal/tagoracle",
	"internal/tagoracle/probe",
	"internal/testgo",
	"pkg/schema",
	"pkg/generator",
	"pkg/generator/internal/unicodepin",
	"pkg/generator/internal/unicodepin/gen",
	"pkg/emitter",
	"pkg/emitter/internal/gocontext",
	"pkg/emitter/internal/gocontext/guardgen",
	"pkg/validationruntime",
	"tests/external",
	"tests/internal/testsupport",
}

// mapOrderSite is one place where a map's iteration order is read.
type mapOrderSite struct {
	pos  token.Position
	what string // "range over map[string]bool", "maps.Keys", ...
	line string // the source line, trimmed
	// annotated is the reason given in a `maporder:` comment, when there is one.
	annotated string
	// idiom is true for the collect-and-sort idiom and slices.Sorted(maps.Keys).
	idiom bool
}

var mapOrderAnnotation = regexp.MustCompile(`maporder:\s*(\S.*)`)

// findMapOrderSites reports every map-order read in files. info must hold the
// types of every expression (types.Info.Types).
func findMapOrderSites(fset *token.FileSet, files []*ast.File, info *types.Info, src map[string][]string) []mapOrderSite {
	var sites []mapOrderSite
	for _, f := range files {
		filename := fset.Position(f.Pos()).Filename
		lines := src[filename]
		// Comment text by the line it ends on, for the annotation lookup.
		commentEndingOn := map[int]string{}
		commentOn := map[int]string{}
		for _, cg := range f.Comments {
			end := fset.Position(cg.End()).Line
			commentEndingOn[end] += cg.Text()
			for _, c := range cg.List {
				l := fset.Position(c.Pos()).Line
				commentOn[l] += c.Text
			}
		}
		annotationAt := func(line int) string {
			for _, text := range []string{commentOn[line], commentEndingOn[line-1]} {
				if m := mapOrderAnnotation.FindStringSubmatch(text); m != nil {
					return strings.TrimSpace(m[1])
				}
			}
			return ""
		}
		record := func(n ast.Node, what string, idiom bool) {
			p := fset.Position(n.Pos())
			line := ""
			if p.Line-1 < len(lines) {
				line = strings.TrimSpace(lines[p.Line-1])
			}
			sites = append(sites, mapOrderSite{pos: p, what: what, line: line, annotated: annotationAt(p.Line), idiom: idiom})
		}

		// sortedArgs are the maps.Keys/maps.Values calls that are the direct
		// argument of slices.Sorted, found before the walk that reports them.
		sortedArgs := map[ast.Node]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			if pkg, name := calleeName(info, call); pkg == "slices" && name == "Sorted" {
				if inner, ok := call.Args[0].(*ast.CallExpr); ok {
					if ipkg, iname := calleeName(info, inner); ipkg == "maps" && (iname == "Keys" || iname == "Values") {
						sortedArgs[inner] = true
					}
				}
			}
			return true
		})

		var visitList func(list []ast.Stmt)
		var visit func(n ast.Node)
		visitList = func(list []ast.Stmt) {
			for i, st := range list {
				inner := st
				for {
					l, ok := inner.(*ast.LabeledStmt)
					if !ok {
						break
					}
					inner = l.Stmt
				}
				if rs, ok := inner.(*ast.RangeStmt); ok {
					if tv, ok := info.Types[rs.X]; ok && tv.Type != nil {
						if _, isMap := tv.Type.Underlying().(*types.Map); isMap {
							record(rs, "range over "+tv.Type.String(), isCollectAndSort(rs, list[i+1:]))
						}
					}
				}
				visit(st)
			}
		}
		visit = func(n ast.Node) {
			ast.Inspect(n, func(c ast.Node) bool {
				switch x := c.(type) {
				case *ast.BlockStmt:
					visitList(x.List)
					return false
				case *ast.CaseClause:
					for _, e := range x.List {
						visit(e)
					}
					visitList(x.Body)
					return false
				case *ast.CommClause:
					if x.Comm != nil {
						visit(x.Comm)
					}
					visitList(x.Body)
					return false
				case *ast.CallExpr:
					pkg, name := calleeName(info, x)
					switch {
					case pkg == "maps" && (name == "Keys" || name == "Values" || name == "All"):
						record(x, "maps."+name, sortedArgs[x])
					case pkg == "reflect" && (name == "MapRange" || name == "MapKeys"):
						record(x, "reflect.Value."+name, false)
					}
				}
				return true
			})
		}
		for _, decl := range f.Decls {
			visit(decl)
		}
	}
	return sites
}

// calleeName names the package-level function or method a call reaches, as
// (package name, function name); ("", "") for anything else.
func calleeName(info *types.Info, call *ast.CallExpr) (string, string) {
	var id *ast.Ident
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		id = fun.Sel
	case *ast.IndexExpr: // slices.Sorted[...] with explicit instantiation
		if sel, ok := fun.X.(*ast.SelectorExpr); ok {
			id = sel.Sel
		}
	case *ast.Ident:
		id = fun
	}
	if id == nil {
		return "", ""
	}
	obj := info.Uses[id]
	if obj == nil || obj.Pkg() == nil {
		return "", ""
	}
	if fn, ok := obj.(*types.Func); ok {
		return fn.Pkg().Path(), fn.Name()
	}
	return "", ""
}

// isCollectAndSort recognises
//
//	for k := range m {
//		keys = append(keys, k)
//	}
//	...
//	sort.Strings(keys)
//
// where every statement between the loop and the sort leaves keys alone. rest
// is the enclosing block's statements after the loop.
func isCollectAndSort(rs *ast.RangeStmt, rest []ast.Stmt) bool {
	if rs.Body == nil || len(rs.Body.List) != 1 {
		return false
	}
	as, ok := rs.Body.List[0].(*ast.AssignStmt)
	if !ok || as.Tok != token.ASSIGN || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
		return false
	}
	dst, ok := as.Lhs[0].(*ast.Ident)
	if !ok {
		return false
	}
	call, ok := as.Rhs[0].(*ast.CallExpr)
	if !ok || len(call.Args) < 2 {
		return false
	}
	if fn, ok := call.Fun.(*ast.Ident); !ok || fn.Name != "append" {
		return false
	}
	if first, ok := call.Args[0].(*ast.Ident); !ok || first.Name != dst.Name {
		return false
	}
	for _, st := range rest {
		if !mentions(st, dst.Name) {
			continue
		}
		es, ok := st.(*ast.ExprStmt)
		if !ok {
			return false
		}
		sc, ok := es.X.(*ast.CallExpr)
		if !ok || len(sc.Args) != 1 {
			return false
		}
		sel, ok := sc.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return false
		}
		switch pkg.Name + "." + sel.Sel.Name {
		case "sort.Strings", "sort.Ints", "sort.Float64s", "slices.Sort":
		default:
			return false
		}
		arg, ok := sc.Args[0].(*ast.Ident)
		return ok && arg.Name == dst.Name
	}
	return false
}

func mentions(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(c ast.Node) bool {
		if id, ok := c.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

// useTestgoEnvForImports gives this test the go environment testgo.Env gives
// every go command a test runs. The source importer these guards type-check
// with resolves an import path by running `go list` itself, in this process's
// environment, so the three variables testgo.Env replaces -- GOCACHE, GOFLAGS,
// GOWORK -- are set to the same values for the length of the test; a
// developer's -mod=vendor or go.work then cannot change what the guard sees.
func useTestgoEnvForImports(t *testing.T) {
	t.Helper()
	for _, e := range testgo.Env() {
		k, v, _ := strings.Cut(e, "=")
		switch k {
		case "GOCACHE", "GOFLAGS", "GOWORK":
			t.Setenv(k, v)
		}
	}
}

// typeCheckDir parses the non-test Go files of dir and type-checks them.
func typeCheckDir(fset *token.FileSet, imp types.ImporterFrom, dir string) ([]*ast.File, *types.Info, map[string][]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, nil, nil, err
	}
	sort.Strings(matches)
	var files []*ast.File
	src := map[string][]string{}
	for _, m := range matches {
		if strings.HasSuffix(m, "_test.go") {
			continue
		}
		// Only the files this build compiles: a package can hold two
		// declarations of one name behind opposite build constraints.
		if ok, err := build.Default.MatchFile(filepath.Dir(m), filepath.Base(m)); err != nil || !ok {
			if err != nil {
				return nil, nil, nil, err
			}
			continue
		}
		data, err := os.ReadFile(m)
		if err != nil {
			return nil, nil, nil, err
		}
		f, err := parser.ParseFile(fset, m, data, parser.ParseComments)
		if err != nil {
			return nil, nil, nil, err
		}
		files = append(files, f)
		src[m] = strings.Split(string(data), "\n")
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}}
	conf := types.Config{Importer: imp}
	if _, err := conf.Check(dir, fset, files, info); err != nil {
		return nil, nil, nil, err
	}
	return files, info, src, nil
}

func TestNoMapOrderReachesTheGenerator(t *testing.T) {
	useTestgoEnvForImports(t)
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "source", nil).(types.ImporterFrom)

	total, idioms, annotated := 0, 0, 0
	var bad []string
	for _, dir := range mapOrderGuardedPackages {
		files, info, src, err := typeCheckDir(fset, imp, testsupport.RepoPath(dir))
		if err != nil {
			t.Fatalf("type-checking %s: %v", dir, err)
		}
		for _, s := range findMapOrderSites(fset, files, info, src) {
			total++
			switch {
			case s.idiom:
				idioms++
			case s.annotated != "":
				annotated++
			default:
				rel, _ := filepath.Rel(testsupport.Root, s.pos.Filename)
				bad = append(bad, fmt.Sprintf("%s:%d: %s: %s", rel, s.pos.Line, s.what, s.line))
			}
		}
	}
	// A guard that found nothing to judge is measuring nothing: the tree has
	// well over a hundred of these, and a count near zero means the type check
	// stopped seeing them rather than that they went away.
	if total < 100 {
		t.Fatalf("found only %d map-order sites; the analysis is not seeing the code", total)
	}
	t.Logf("%d map-order sites: %d collect-and-sort, %d annotated", total, idioms, annotated)
	if len(bad) > 0 {
		t.Errorf("%d loops read a map's iteration order without saying why that order cannot reach the output.\n"+
			"Range over sorted keys instead, or, where the order provably cannot matter, say why in a\n"+
			"`// maporder: <reason>` comment on the line above:\n\t%s", len(bad), strings.Join(bad, "\n\t"))
	}
}

// TestMapOrderGuardCoversEveryPackage keeps mapOrderGuardedPackages honest: a
// package that exists and is not listed is a package the guard never read.
func TestMapOrderGuardCoversEveryPackage(t *testing.T) {
	listed := map[string]bool{}
	for _, d := range mapOrderGuardedPackages {
		listed[d] = true
	}
	var missing []string
	err := filepath.Walk(testsupport.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(testsupport.Root, path)
		if info.IsDir() {
			switch {
			case rel == ".":
				return nil
			case strings.HasPrefix(info.Name(), "."), info.Name() == "testdata", rel == "scripts", rel == "docs":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			dir := filepath.Dir(rel)
			if !listed[dir] {
				missing = append(missing, dir)
				listed[dir] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Errorf("packages with non-test Go files that the map-order guard does not read: %v; add them to mapOrderGuardedPackages", missing)
	}
}
