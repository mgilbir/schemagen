package testsupport

import (
	"testing"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// GenerateFromSchema runs the full pipeline: load → normalize → generate → emit.
// schemaPath is relative to the repository root.
func GenerateFromSchema(t *testing.T, schemaPath string) []byte {
	t.Helper()
	return GenerateFromSchemaWithConfig(t, schemaPath, generator.Config{
		PackageName: "testpkg",
		OmitEmpty:   true,
	})
}

// GenerateFromSchemaWithConfig runs the pipeline with a custom generator config.
func GenerateFromSchemaWithConfig(t *testing.T, schemaPath string, cfg generator.Config) []byte {
	t.Helper()

	fullPath := RepoPath(schemaPath)
	s, err := schema.LoadFromFile(fullPath)
	if err != nil {
		t.Fatalf("loading schema %s: %v", schemaPath, err)
	}

	// The draft the config names is normalized under too, exactly as cmd/schemagen
	// does it: normalization is where a keyword the dialect does not define is
	// dropped, and a draft answered from two sources is issue #203 in miniature.
	// DraftUnknown means "read it from the document", which is what every fixture
	// that names no draft gets.
	s.NormalizeForDraft(cfg.Draft)

	gen := generator.New(cfg)
	ir, err := gen.Generate(s)
	if err != nil {
		t.Fatalf("generating IR for %s: %v", schemaPath, err)
	}

	em, err := emitter.New()
	if err != nil {
		t.Fatalf("creating emitter: %v", err)
	}

	src, err := em.Emit(ir)
	if err != nil {
		t.Fatalf("emitting code for %s: %v", schemaPath, err)
	}

	return src
}

// GenerateWithRootName runs the pipeline with the root type name the CLI's
// --root-name flag supplies, which overrides the title.
func GenerateWithRootName(t *testing.T, schemaPath, rootName string) string {
	t.Helper()

	s, err := schema.LoadFromFile(RepoPath(schemaPath))
	if err != nil {
		t.Fatalf("loading schema %s: %v", schemaPath, err)
	}
	s.NormalizeForDraft(schema.DraftUnknown)

	gen := generator.New(generator.Config{PackageName: "testpkg", OmitEmpty: true})
	ir, err := gen.Generate(s, generator.WithRootTypeName(rootName))
	if err != nil {
		t.Fatalf("generating IR for %s: %v", schemaPath, err)
	}
	em, err := emitter.New()
	if err != nil {
		t.Fatalf("creating emitter: %v", err)
	}
	src, err := em.Emit(ir)
	if err != nil {
		t.Fatalf("emitting code for %s: %v", schemaPath, err)
	}
	return string(src)
}
