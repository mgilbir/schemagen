package tests

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// TestGeneratedCorpusCompiles generates every schema under testdata/schemas and
// compiles the lot.
//
// It exists because nothing did. The pipeline had a fuzz target whose only
// property is "does not panic", a golden corpus that compares text, and a
// round-trip suite over a couple of dozen hand-picked documents -- so a schema
// that generated gofmt-clean Go which the compiler rejects passed every gate
// there was, behind a zero exit code. Three schemas already in this repository's
// own corpus were doing exactly that when issue #269 was filed: a duplicate enum
// member became two `case` labels of one value, and 1e308 became a
// three-hundred-and-nine-digit integer constant that gc refuses. Both had been
// sitting in testdata/schemas for as long as the files had existed.
//
// The whole corpus goes into one module as one package per schema and is built
// with a single `go build ./...`, which is what makes the gate affordable: the
// build is parallel and shares its dependency compilations, and the corpus
// measured a few seconds against several minutes for a build per schema.
//
// A schema this generator refuses is not a failure here. Refusing is a
// legitimate answer -- half the adversarial corpus is malformed on purpose --
// and the property under test is narrower and absolute: whatever is emitted
// must compile.
//
// The configuration is the CLI's default, and deliberately only that. Every
// other configuration multiplies the corpus by another full build, and the
// default is the one every schema here is written against; a defect that needs
// --big-int or --exact-numbers to appear is for the cogen harness, which
// already deals the flag matrix.
func TestGeneratedCorpusCompiles(t *testing.T) {
	// Sorted so that the package a schema is generated into is the same one
	// from run to run, which is what makes a failure reproducible by name.
	schemas := corpusSchemaPaths(t)
	sort.Strings(schemas)
	if len(schemas) == 0 {
		// A gate measuring nothing passes for the wrong reason.
		t.Fatalf("no schemas found under %s", fuzzSchemaDir)
	}

	em, err := emitter.New()
	if err != nil {
		t.Fatalf("emitter.New: %v", err)
	}

	dir := t.TempDir()
	if err := writeTestGoMod(dir, "corpus_compile_test"); err != nil {
		t.Fatal(err)
	}

	// pkgDir maps the Go package directory back to the schema that produced it,
	// so a compiler error naming a directory can be reported against a file
	// someone can open.
	pkgDir := make(map[string]string, len(schemas))
	emitted, refused := 0, 0
	for i, path := range schemas {
		src, helpers, ok := generateForCompile(t, em, path)
		if !ok {
			refused++
			continue
		}
		name := fmt.Sprintf("p%04d", i)
		sub := filepath.Join(dir, name)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "types.go"), src, 0o644); err != nil {
			t.Fatal(err)
		}
		if len(helpers) > 0 {
			if err := os.WriteFile(filepath.Join(sub, "helpers.go"), helpers, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		pkgDir[name] = path
		emitted++
	}

	t.Logf("compiling %d packages generated from %d corpus schemas (%d refused by the generator)",
		emitted, len(schemas), refused)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err == nil {
		return
	}

	// go build reports each broken package under a "# corpus_compile_test/pNNNN"
	// header. Rewriting those headers to the schema path is the difference
	// between a report someone can act on and four hundred lines naming
	// directories in a temp tree that no longer exists.
	report := string(output)
	for name, path := range pkgDir {
		report = strings.ReplaceAll(report, "corpus_compile_test/"+name, path)
		report = strings.ReplaceAll(report, name+"/types.go", path+" -> types.go")
		report = strings.ReplaceAll(report, name+"/helpers.go", path+" -> helpers.go")
	}
	t.Errorf("generated code does not compile (%v):\n%s", err, report)
}

// generateForCompile runs one schema through the pipeline exactly as the CLI's
// default does, and reports ok=false for a schema the generator declines.
//
// The file resolver rooted at the schema's own directory is part of "as the CLI
// does": a corpus schema that $refs a sibling file resolves there and nowhere
// else, and without it every such schema would be counted as refused and
// quietly leave the gate.
func generateForCompile(t *testing.T, em *emitter.Emitter, path string) (src, helpers []byte, ok bool) {
	t.Helper()

	s, err := schema.LoadFromFile(path)
	if err != nil {
		return nil, nil, false
	}
	s.NormalizeForDraft(schema.DraftUnknown)
	s.ComputeBaseURIs(nil, s)

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, false
	}
	cfg := generator.Config{
		PackageName: "gen",
		OutputDir:   ".",
		OmitEmpty:   true,
		Validation:  generator.ValidationModeStatic,
		Resolver:    schema.NewCompositeResolver(schema.NewFileResolver(filepath.Dir(abs))),
	}
	ir, err := generator.New(cfg).Generate(s)
	if err != nil || ir == nil {
		return nil, nil, false
	}
	src, err = em.Emit(ir)
	if err != nil {
		return nil, nil, false
	}
	helpers, hasHelpers, err := em.EmitHelpers(cfg.PackageName, generator.HelpersReferencedBy(string(src)))
	if err != nil {
		return nil, nil, false
	}
	if !hasHelpers {
		helpers = nil
	}
	return src, helpers, true
}
