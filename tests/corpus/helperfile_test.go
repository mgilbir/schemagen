package corpus

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
	"github.com/mgilbir/schemagen/tests/internal/testsupport"
)

// TestGeneratedCodeNamesOnlyWhatTheRuntimeExports holds the claim the runtime
// module makes possible: everything the generated code reaches for in the
// runtime package, the runtime exports.
//
// The call and the declaration are in different modules, and the compiler only
// joins them when somebody compiles the schema. A template that spells a name the
// runtime does not have -- a rename on one side, a helper moved and its callers
// left -- fails to build for whichever schema first reaches it, which need not be
// one any compile test generates. This walks every fixture in the tree, in both
// format postures, and asks the question of the emitted source directly: every
// `rt.X` it contains, in the types file and in the package's pattern file, is an
// exported top-level name of the runtime.
//
// It reads the emitted source rather than a golden, so a fixture that is not
// pinned as a golden is covered too. Which checks are emitted depends on the
// draft, so a fixture that annotates on its own dialect is generated a second
// time with assertion forced.
func TestGeneratedCodeNamesOnlyWhatTheRuntimeExports(t *testing.T) {
	schemaFiles := allRegressionSchemas(t)
	if len(schemaFiles) == 0 {
		t.Fatal("no schemas found to check")
	}
	exports := runtimeExports(t)

	em, err := emitter.New()
	if err != nil {
		t.Fatalf("creating emitter: %v", err)
	}

	// A schema the generator refuses under a configuration is skipped for that
	// configuration, and the skips are pinned per configuration: see
	// pinnedRefusals.
	type helperCfg struct {
		name    string
		asserts bool
	}
	cfgs := []helperCfg{{"dialect", false}, {"format-assertion", true}}
	refusals := map[string]*refusalLedger{}
	for _, cfg := range cfgs {
		refusals[cfg.name] = newRefusalLedger("helper-file/" + cfg.name)
	}
	references := 0
	for _, path := range schemaFiles {
		for _, cfg := range cfgs {
			ledger := refusals[cfg.name]
			t.Run(filepath.Base(path)+"/"+cfg.name, func(t *testing.T) {
				s, err := schema.LoadFromFile(path)
				if err != nil {
					ledger.refuse(path, fmt.Errorf("load: %w", err))
					t.Skipf("not loadable: %v", err)
				}
				s.Normalize()
				// Resolved as the CLI resolves it, from the schema's own
				// directory: without it a schema that $refs a sibling file is
				// refused and never checked.
				s.ComputeBaseURIs(nil, s)
				abs, err := filepath.Abs(path)
				if err != nil {
					t.Fatal(err)
				}
				gen := generator.New(generator.Config{
					PackageName:     "testpkg",
					OmitEmpty:       true,
					FormatAssertion: cfg.asserts,
					Resolver:        schema.NewCompositeResolver(schema.NewFileResolver(filepath.Dir(abs))),
				})
				ir, err := gen.Generate(s)
				if err != nil {
					ledger.refuse(path, fmt.Errorf("generate: %w", err))
					t.Skipf("not generatable: %v", err)
				}
				src, err := em.Emit(ir)
				if err != nil {
					ledger.refuse(path, fmt.Errorf("emit: %w", err))
					t.Skipf("not emittable: %v", err)
				}
				ledger.generated()

				helperSrc, _, err := em.EmitHelpers("testpkg", generator.HelpersReferencedBy(string(src)))
				if err != nil {
					t.Fatalf("emitting helpers: %v", err)
				}
				for _, text := range [][]byte{src, helperSrc} {
					references += checkRuntimeReferences(t, filepath.Base(path), text, exports)
				}
			})
		}
	}
	for _, cfg := range cfgs {
		refusals[cfg.name].check(t)
	}
	if references == 0 {
		t.Fatal("no reference to the runtime package in any generated file: this test is watching nothing")
	}
}

// TestGeneratedCodeNamesOnlyWhatTheRuntimeExportsForSmallSchemas is the same
// claim for the smallest schemas that reach one construct each, which are inline
// rather than corpus fixtures because the construct is the property being
// pinned: a corpus schema outside the adversarial set happens to reach most
// of them for some other reason, and would pass with one of them broken.
func TestGeneratedCodeNamesOnlyWhatTheRuntimeExportsForSmallSchemas(t *testing.T) {
	em, err := emitter.New()
	if err != nil {
		t.Fatalf("creating emitter: %v", err)
	}
	exports := runtimeExports(t)
	for _, tc := range []struct {
		name   string
		schema string
	}{
		{"one untyped property", `{"properties":{"b":{}}}`},
		{"a nullable property", `{"properties":{"b":{"type":["string","null"]}}}`},
		{"a container alias", `{"type":"array","items":{"type":"string"}}`},
		{"an integer property", `{"properties":{"n":{"type":"integer"}}}`},
		{"a date-time property", `{"properties":{"d":{"type":"string","format":"date-time"}}}`},
		{"an ipv4 property", `{"properties":{"a":{"type":"string","format":"ipv4"}}}`},
		{"a pattern", `{"properties":{"p":{"type":"string","pattern":"^a+$"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s schema.Schema
			if err := json.Unmarshal([]byte(tc.schema), &s); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			s.Normalize()
			ir, err := generator.New(generator.Config{
				PackageName:     "testpkg",
				OmitEmpty:       true,
				FormatAssertion: true,
			}).Generate(&s)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			src, err := em.Emit(ir)
			if err != nil {
				t.Fatalf("emit: %v", err)
			}
			helperSrc, _, err := em.EmitHelpers("testpkg", generator.HelpersReferencedBy(string(src)))
			if err != nil {
				t.Fatalf("emitting helpers: %v", err)
			}
			n := 0
			for _, text := range [][]byte{src, helperSrc} {
				n += checkRuntimeReferences(t, tc.name, text, exports)
			}
			if n == 0 {
				t.Errorf("%s: the generated files name nothing in the runtime, so nothing was checked", tc.name)
			}
		})
	}
}

// checkRuntimeReferences reports every `rt.X` in one generated file that is not
// an exported name of the runtime, and returns how many references it looked at.
// An empty file (no pattern file was written) has none.
func checkRuntimeReferences(t *testing.T, label string, src []byte, exports map[string]bool) int {
	t.Helper()
	if len(src) == 0 {
		return 0
	}
	f, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		t.Errorf("%s: generated source does not parse: %v", label, err)
		return 0
	}
	n := 0
	ast.Inspect(f, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == generator.RuntimeAlias && id.Obj == nil {
			n++
			if !exports[sel.Sel.Name] {
				t.Errorf("%s: the generated code uses %s.%s, which the runtime module does not export", label, id.Name, sel.Sel.Name)
			}
		}
		return true
	})
	return n
}

// runtimeExports is every exported top-level name the runtime module declares --
// functions, types, variables and constants -- read from its source.
func runtimeExports(t *testing.T) map[string]bool {
	t.Helper()
	dir := testsupport.RepoPath("runtime")
	pkgs, err := parser.ParseDir(token.NewFileSet(), dir, func(fi fs.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatalf("parsing the runtime module: %v", err)
	}
	out := map[string]bool{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil && d.Name.IsExported() {
						out[d.Name.Name] = true
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch spec := spec.(type) {
						case *ast.TypeSpec:
							if spec.Name.IsExported() {
								out[spec.Name.Name] = true
							}
						case *ast.ValueSpec:
							for _, name := range spec.Names {
								if name.IsExported() {
									out[name.Name] = true
								}
							}
						}
					}
				}
			}
		}
	}
	if len(out) < 100 {
		t.Fatalf("found only %d exported names in %s; the scan is not reading the runtime module", len(out), dir)
	}
	return out
}

// allRegressionSchemas lists every schema fixture in the tree, which is the
// population this guard has to cover: a fixture that is not a golden is still a
// schema someone can hand the CLI.
func allRegressionSchemas(t *testing.T) []string {
	t.Helper()
	var out []string
	root := testsupport.RepoPath("testdata", "schemas")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}
		// The adversarial corpus is deliberately malformed; whether it generates
		// at all is FuzzGenerate's business, not this test's.
		if strings.Contains(filepath.ToSlash(path), "/adversarial/") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking schemas: %v", err)
	}
	return out
}
