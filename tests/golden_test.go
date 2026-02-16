package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// goldenTestCase defines a test case: schema input → expected golden output.
type goldenTestCase struct {
	Name       string
	SchemaPath string
	GoldenPath string
}

func allGoldenTests() []goldenTestCase {
	return []goldenTestCase{
		{"basic/simple_object", "testdata/schemas/basic/simple_object.json", "testdata/golden/basic/simple_object.go"},
		{"basic/nested_object", "testdata/schemas/basic/nested_object.json", "testdata/golden/basic/nested_object.go"},
		{"basic/primitive_types", "testdata/schemas/basic/primitive_types.json", "testdata/golden/basic/primitive_types.go"},
		{"basic/array_types", "testdata/schemas/basic/array_types.json", "testdata/golden/basic/array_types.go"},
		{"basic/additional_properties", "testdata/schemas/basic/additional_properties.json", "testdata/golden/basic/additional_properties.go"},
		{"basic/additional_properties_bool", "testdata/schemas/basic/additional_properties_bool.json", "testdata/golden/basic/additional_properties_bool.go"},
		{"refs/defs_ref", "testdata/schemas/refs/defs_ref.json", "testdata/golden/refs/defs_ref.go"},
		{"refs/definitions_ref", "testdata/schemas/refs/definitions_ref.json", "testdata/golden/refs/definitions_ref.go"},
		{"enum/string_enum", "testdata/schemas/enum/string_enum.json", "testdata/golden/enum/string_enum.go"},
		{"composition/allof_simple", "testdata/schemas/composition/allof_simple.json", "testdata/golden/composition/allof_simple.go"},
		{"composition/oneof_simple", "testdata/schemas/composition/oneof_simple.json", "testdata/golden/composition/oneof_simple.go"},
		{"composition/oneof_complex", "testdata/schemas/composition/oneof_complex.json", "testdata/golden/composition/oneof_complex.go"},
		{"composition/oneof_with_null", "testdata/schemas/composition/oneof_with_null.json", "testdata/golden/composition/oneof_with_null.go"},
		{"validation/string_constraints", "testdata/schemas/validation/string_constraints.json", "testdata/golden/validation/string_constraints.go"},
		{"formats/datetime", "testdata/schemas/formats/datetime.json", "testdata/golden/formats/datetime.go"},
	}
}

func TestGoldenFiles(t *testing.T) {
	for _, tc := range allGoldenTests() {
		t.Run(tc.Name, func(t *testing.T) {
			got := generateFromSchema(t, tc.SchemaPath)

			goldenPath := filepath.Join("..", tc.GoldenPath)
			if os.Getenv("UPDATE_GOLDEN") == "true" {
				dir := filepath.Dir(goldenPath)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("creating golden dir: %v", err)
				}
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatalf("updating golden file: %v", err)
				}
				t.Logf("Updated golden file: %s", goldenPath)
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("reading golden file %s: %v\nRun with UPDATE_GOLDEN=true to create it", goldenPath, err)
			}

			if string(got) != string(want) {
				t.Errorf("generated output differs from golden file %s", tc.GoldenPath)
				// Show a simple diff
				gotLines := strings.Split(string(got), "\n")
				wantLines := strings.Split(string(want), "\n")
				maxLines := len(gotLines)
				if len(wantLines) > maxLines {
					maxLines = len(wantLines)
				}
				for i := 0; i < maxLines; i++ {
					var gotLine, wantLine string
					if i < len(gotLines) {
						gotLine = gotLines[i]
					}
					if i < len(wantLines) {
						wantLine = wantLines[i]
					}
					if gotLine != wantLine {
						t.Errorf("  line %d:\n    got:  %q\n    want: %q", i+1, gotLine, wantLine)
					}
				}
			}
		})
	}
}

// generateFromSchema runs the full pipeline: load → normalize → generate → emit.
func generateFromSchema(t *testing.T, schemaPath string) []byte {
	t.Helper()

	fullPath := filepath.Join("..", schemaPath)
	s, err := schema.LoadFromFile(fullPath)
	if err != nil {
		t.Fatalf("loading schema %s: %v", schemaPath, err)
	}

	s.Normalize()

	cfg := generator.Config{
		PackageName: "testpkg",
		OmitEmpty:   true,
	}
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
