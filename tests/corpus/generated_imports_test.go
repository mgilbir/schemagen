package corpus

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// TestGeneratedCodeNeverImportsTheMainModule holds the property the runtime
// module exists for: a package schemagen generates depends on the standard
// library and on github.com/mgilbir/schemagen/runtime, and on nothing else of
// this repository.
//
// The generated code used to import pkg/validationruntime, and its build then
// needed the whole schemagen module -- cobra, the generator, everything -- in the
// requirements of whoever held it. The runtime module carries the capability
// metadata now, and a user's module graph is the runtime's, three modules. A
// template or an import table that reaches back for a package of the main module
// would put the generator back in every user's build, and would compile in this
// repository, where the main module is right there to be found -- so the
// compiler cannot be what says so.
//
// It reads the import declarations of the emitted source, of the package's
// pattern file, over every corpus schema, under the configurations that change
// what is imported: the dialect's own, forced format assertion, and both
// validation modes that record capability metadata.
func TestGeneratedCodeNeverImportsTheMainModule(t *testing.T) {
	const runtimePath = "github.com/mgilbir/schemagen/runtime"
	em, err := emitter.New()
	if err != nil {
		t.Fatalf("creating emitter: %v", err)
	}
	cfgs := []struct {
		name string
		cfg  generator.Config
	}{
		{"dialect", generator.Config{}},
		{"format-assertion", generator.Config{FormatAssertion: true}},
		{"hybrid", generator.Config{Validation: generator.ValidationModeHybrid}},
		{"runtime", generator.Config{Validation: generator.ValidationModeRuntime}},
	}
	files, imports, runtimeUsers := 0, 0, 0
	for _, path := range allRegressionSchemas(t) {
		for _, c := range cfgs {
			s, err := schema.LoadFromFile(path)
			if err != nil {
				continue
			}
			s.Normalize()
			s.ComputeBaseURIs(nil, s)
			abs, err := filepath.Abs(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg := c.cfg
			cfg.PackageName = "testpkg"
			cfg.OmitEmpty = true
			cfg.Resolver = schema.NewCompositeResolver(schema.NewFileResolver(filepath.Dir(abs)))
			ir, err := generator.New(cfg).Generate(s)
			if err != nil {
				continue // a refusal is the refusal sweeps' business
			}
			src, err := em.Emit(ir)
			if err != nil {
				continue
			}
			helperSrc, _, err := em.EmitHelpers("testpkg", generator.HelpersReferencedBy(string(src)))
			if err != nil {
				t.Fatalf("%s/%s: emitting helpers: %v", filepath.Base(path), c.name, err)
			}
			for _, text := range [][]byte{src, helperSrc} {
				if len(text) == 0 {
					continue
				}
				f, err := parser.ParseFile(token.NewFileSet(), "", text, parser.ImportsOnly)
				if err != nil {
					t.Fatalf("%s/%s: generated source does not parse: %v", filepath.Base(path), c.name, err)
				}
				files++
				for _, imp := range f.Imports {
					p, _ := strconv.Unquote(imp.Path.Value)
					imports++
					switch {
					case p == runtimePath:
						runtimeUsers++
					case strings.HasPrefix(p, "github.com/mgilbir/schemagen"):
						t.Errorf("%s/%s: generated code imports %s: it may import the runtime module and nothing else of schemagen", filepath.Base(path), c.name, p)
					}
				}
			}
		}
	}
	if files < 500 || imports == 0 || runtimeUsers == 0 {
		t.Fatalf("looked at %d files, %d imports, %d of the runtime: the sweep is not seeing the corpus", files, imports, runtimeUsers)
	}
}
