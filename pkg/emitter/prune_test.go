package emitter

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// helperMinimalityCases are schemas whose generated code reaches into the
// helper blocks by every route there is: none at all, the in-place decoder for
// a flat struct, containers and unions of this package's types, the runtime
// evaluator with and without the comparisons, the identity walker and each
// entry into it, raw JSON held whole, --strict-read-write's walker, and the
// number shadows.
var helperMinimalityCases = []struct {
	name string
	doc  string
	cfg  func(*generator.Config)
}{
	{"a flat struct", `{"type":"object","properties":{"s":{"type":"string"},"n":{"type":"integer"},"b":{"type":"boolean"}}}`, nil},
	{"a struct of structs", `{"type":"object","properties":{"a":{"type":"object","properties":{"x":{"type":"string"}}},"l":{"type":"array","items":{"type":"object","properties":{"y":{"type":"number"}}}},"m":{"type":"object","additionalProperties":{"type":"object","properties":{"z":{"type":"string"}}}}}}`, nil},
	{"a oneOf of structs", `{"oneOf":[{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]},{"type":"object","properties":{"b":{"type":"integer"}},"required":["b"]}]}`, nil},
	{"a tuple", `{"type":"object","properties":{"t":{"type":"array","prefixItems":[{"type":"object","properties":{"a":{"type":"integer","minimum":1}}},{"type":"string"}]}}}`, nil},
	{"the evaluator judging types only", `{"$schema":"https://json-schema.org/draft/2020-12/schema","anyOf":[{"prefixItems":[{"type":"integer"}]},{"prefixItems":[{"type":"string"}]}],"unevaluatedItems":false}`, nil},
	{"the evaluator judging an enum and uniqueItems", `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","unevaluatedProperties":{"enum":[1,"a"],"uniqueItems":true,"maxLength":2}}`, nil},
	{"an object-level conditional's const", `{"type":"object","properties":{"k":{}},"if":{"properties":{"k":{"const":1}}},"then":{"required":["x"]}}`, nil},
	{"an enum held as raw JSON", `{"enum":[{"a":1},"x"]}`, nil},
	{"uniqueItems over structs", `{"type":"object","properties":{"a":{"type":"array","uniqueItems":true,"items":{"type":"object","properties":{"x":{"type":"string"}}}}}}`, nil},
	{"a contains stating a type", `{"type":"object","properties":{"a":{"type":"array","contains":{"type":"integer"}}}}`, nil},
	{"a schema held whole", `{"$schema":"https://json-schema.org/draft/2020-12/schema","$dynamicAnchor":"n","not":{"type":"number"},"properties":{"c":{"$dynamicRef":"#n"},"n":{"enum":["a","b"]}}}`, nil},
	{"patterns and formats", `{"type":"object","properties":{"p":{"type":"string","pattern":"^a+$"},"d":{"type":"string","format":"date-time"},"h":{"type":"string","format":"hostname"}},"patternProperties":{"^x":{"type":"integer"}}}`,
		func(c *generator.Config) { c.FormatAssertion = true }},
	{"--strict-read-write", `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"t":{"type":"array","prefixItems":[{"$ref":"#/$defs/A"}]},"u":{"type":"array","uniqueItems":true,"items":{"$ref":"#/$defs/A"}}},"$defs":{"A":{"type":"object","properties":{"ro":{"type":"integer","readOnly":true},"wo":{"type":"integer","writeOnly":true}}}}}`,
		func(c *generator.Config) { c.StrictReadWrite = true }},
	{"--big-int and --exact-numbers", `{"type":"object","properties":{"i":{"type":"integer","maximum":9007199254740993},"f":{"type":"number","multipleOf":0.1}}}`,
		func(c *generator.Config) { c.BigIntSupport = true; c.ExactNumbers = true }},
}

// TestHelperDeclarationsAreAllNeeded holds the pruned helper file (see
// pruneHelpers) to the type checker, in both directions, for each of
// helperMinimalityCases: the generated package type-checks, so nothing it
// reaches was pruned; and every declaration left in the helper file is reached
// from the generated file through what go/types resolves each identifier to --
// a use of the declared object itself, not a name that happens to match -- so
// nothing was kept that the package does not reach. The only declarations
// exempt are methods an interface may call without naming them: exported ones,
// which encoding/json, fmt and errors call, and ones an interface lists, which
// a type assertion the checker cannot follow may call. They are reached with
// their type.
//
// The check is the type checker's, not the pruning's own reading of the
// source: a pass that misread what a declaration mentions would pass a test
// written with the same reading.
func TestHelperDeclarationsAreAllNeeded(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	imp := importer.ForCompiler(token.NewFileSet(), "source", nil)
	for _, c := range helperMinimalityCases {
		t.Run(c.name, func(t *testing.T) {
			var s schema.Schema
			if err := json.Unmarshal([]byte(c.doc), &s); err != nil {
				t.Fatal(err)
			}
			s.Normalize()
			cfg := generator.Config{PackageName: "p", OmitEmpty: true, RootTypeName: "Root"}
			if c.cfg != nil {
				c.cfg(&cfg)
			}
			ir, err := generator.New(cfg).Generate(&s)
			if err != nil {
				t.Fatal(err)
			}
			src, err := e.Emit(ir)
			if err != nil {
				t.Fatal(err)
			}
			helpers, ok, err := e.EmitHelpers("p", generator.HelpersReferencedBy(string(src)))
			if err != nil {
				t.Fatal(err)
			}
			fset := token.NewFileSet()
			tf, err := parser.ParseFile(fset, "types.go", src, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			files := []*ast.File{tf}
			var hf *ast.File
			if ok {
				if hf, err = parser.ParseFile(fset, "helpers.go", helpers, parser.SkipObjectResolution); err != nil {
					t.Fatal(err)
				}
				files = append(files, hf)
			}
			info := &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}}
			conf := types.Config{Importer: imp}
			if _, err := conf.Check("p", fset, files, info); err != nil {
				t.Fatalf("the package does not type-check: something it reaches was pruned: %v", err)
			}
			if hf == nil {
				return
			}

			// Each helper declaration, by the objects it declares.
			declOf := map[types.Object]int{}
			for i, d := range hf.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					declOf[info.Defs[d.Name]] = i
				case *ast.GenDecl:
					for _, sp := range d.Specs {
						switch sp := sp.(type) {
						case *ast.TypeSpec:
							declOf[info.Defs[sp.Name]] = i
						case *ast.ValueSpec:
							for _, n := range sp.Names {
								if o := info.Defs[n]; o != nil {
									declOf[o] = i
								}
							}
						}
					}
				}
			}
			// The helper declarations a node uses, as the checker resolved them.
			usesIn := func(n ast.Node) []int {
				var out []int
				ast.Inspect(n, func(x ast.Node) bool {
					if id, ok := x.(*ast.Ident); ok {
						if o := info.Uses[id]; o != nil {
							if o, ok := o.(*types.Func); ok && o.Origin() != nil {
								if i, ok := declOf[o.Origin()]; ok {
									out = append(out, i)
									return true
								}
							}
							if i, ok := declOf[o]; ok {
								out = append(out, i)
							}
						}
					}
					return true
				})
				return out
			}

			// Methods an interface may call unnamed, reached with their type.
			listed := map[string]bool{}
			for _, f := range files {
				ast.Inspect(f, func(n ast.Node) bool {
					if it, ok := n.(*ast.InterfaceType); ok {
						for _, m := range it.Methods.List {
							for _, id := range m.Names {
								listed[id.Name] = true
							}
						}
					}
					return true
				})
			}
			exempt := map[int]bool{}
			methodsOf := map[string][]int{}
			for i, d := range hf.Decls {
				if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv != nil && len(fd.Recv.List) > 0 {
					if ast.IsExported(fd.Name.Name) || listed[fd.Name.Name] {
						exempt[i] = true
						recv := receiverTypeName(fd.Recv.List[0].Type)
						methodsOf[recv] = append(methodsOf[recv], i)
					}
				}
			}

			reached := map[int]bool{}
			var work []int
			reach := func(i int) {
				if !reached[i] {
					reached[i] = true
					work = append(work, i)
				}
			}
			for _, i := range usesIn(tf) {
				reach(i)
			}
			for i, d := range hf.Decls {
				if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "init" {
					reach(i)
				}
				if gd, ok := d.(*ast.GenDecl); ok {
					for _, sp := range gd.Specs {
						if vs, ok := sp.(*ast.ValueSpec); ok {
							for _, n := range vs.Names {
								if n.Name == "_" {
									reach(i)
								}
							}
						}
					}
				}
			}
			for len(work) > 0 {
				i := work[len(work)-1]
				work = work[:len(work)-1]
				for _, j := range usesIn(hf.Decls[i]) {
					reach(j)
				}
				if gd, ok := hf.Decls[i].(*ast.GenDecl); ok && gd.Tok == token.TYPE {
					for _, sp := range gd.Specs {
						for _, j := range methodsOf[sp.(*ast.TypeSpec).Name.Name] {
							reach(j)
						}
					}
				}
			}
			checked := 0
			for i, d := range hf.Decls {
				name := declName(d)
				if name == "" || exempt[i] {
					continue
				}
				checked++
				if !reached[i] {
					t.Errorf("the helper file keeps %s, which nothing the package reaches needs", name)
				}
			}
			if checked < 3 {
				t.Fatalf("only %d declarations were checked; the case reaches almost nothing", checked)
			}
		})
	}
}

// declName names a top-level declaration for the minimality check, or "" for
// one it exempts: the imports, and an exported method.
func declName(d ast.Decl) string {
	switch d := d.(type) {
	case *ast.GenDecl:
		if d.Tok == token.IMPORT {
			return ""
		}
		var names []string
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
		return d.Tok.String() + " " + strings.Join(names, ", ")
	case *ast.FuncDecl:
		if d.Recv != nil && len(d.Recv.List) > 0 {
			if ast.IsExported(d.Name.Name) {
				return ""
			}
			return "method " + receiverTypeName(d.Recv.List[0].Type) + "." + d.Name.Name
		}
		return "func " + d.Name.Name
	}
	return ""
}

// TestPruningKeepsAHandBuiltSetWhole: a HelperSet written by hand, with no
// roots read from source, keeps every declaration of every block it renders,
// which is what a caller that composes its own set is asking for.
func TestPruningKeepsAHandBuiltSetWhole(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	whole, _, err := e.EmitHelpers("p", generator.HelperSet{Decode: true})
	if err != nil {
		t.Fatal(err)
	}
	pruned, _, err := e.EmitHelpers("p", generator.HelperSet{Decode: true, Roots: []string{"jsonOpenDoc"}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(whole, []byte("func jsonDecodeMap[")) || bytes.Contains(pruned, []byte("func jsonDecodeMap[")) {
		t.Errorf("a set with no roots must keep the block whole, and one with roots only what they reach")
	}
	if !bytes.Contains(pruned, []byte("func jsonOpenDoc(")) {
		t.Errorf("the root itself was pruned")
	}
}
