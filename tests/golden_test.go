package tests

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		{"composition/anyof_simple", "testdata/schemas/composition/anyof_simple.json", "testdata/golden/composition/anyof_simple.go"},
		{"composition/oneof_with_null", "testdata/schemas/composition/oneof_with_null.json", "testdata/golden/composition/oneof_with_null.go"},
		{"validation/string_constraints", "testdata/schemas/validation/string_constraints.json", "testdata/golden/validation/string_constraints.go"},
		{"validation/numeric_constraints", "testdata/schemas/validation/numeric_constraints.json", "testdata/golden/validation/numeric_constraints.go"},
		{"formats/datetime", "testdata/schemas/formats/datetime.json", "testdata/golden/formats/datetime.go"},
		{"formats/all_formats", "testdata/schemas/formats/all_formats.json", "testdata/golden/formats/all_formats.go"},
		{"defaults/server_config", "testdata/schemas/defaults/server_config.json", "testdata/golden/defaults/server_config.go"},
		{"composition/oneof_discriminator", "testdata/schemas/composition/oneof_discriminator.json", "testdata/golden/composition/oneof_discriminator.go"},
		{"composition/oneof_discriminator_heuristic", "testdata/schemas/composition/oneof_discriminator_heuristic.json", "testdata/golden/composition/oneof_discriminator_heuristic.go"},
		{"validation/unevaluated_items", "testdata/schemas/validation/unevaluated_items.json", "testdata/golden/validation/unevaluated_items.go"},
		{"advanced/recursive_tree", "testdata/schemas/advanced/recursive_tree.json", "testdata/golden/advanced/recursive_tree.go"},
		{"advanced/pattern_properties", "testdata/schemas/advanced/pattern_properties.json", "testdata/golden/advanced/pattern_properties.go"},
		{"advanced/nullable_const", "testdata/schemas/advanced/nullable_const.json", "testdata/golden/advanced/nullable_const.go"},
		{"advanced/tuple_array", "testdata/schemas/advanced/tuple_array.json", "testdata/golden/advanced/tuple_array.go"},
		{"advanced/cross_refs", "testdata/schemas/advanced/cross_refs.json", "testdata/golden/advanced/cross_refs.go"},
		{"advanced/complex_tuple", "testdata/schemas/advanced/complex_tuple.json", "testdata/golden/advanced/complex_tuple.go"},
		{"validation/nested_errors", "testdata/schemas/validation/nested_errors.json", "testdata/golden/validation/nested_errors.go"},
		{"regression/allof_oneof_variants", "testdata/schemas/regression/allof_oneof_variants.json", "testdata/golden/regression/allof_oneof_variants.go"},
		{"regression/allof_oneof_crossed_types", "testdata/schemas/regression/allof_oneof_crossed_types.json", "testdata/golden/regression/allof_oneof_crossed_types.go"},
		{"regression/allof_if_then_branches", "testdata/schemas/regression/allof_if_then_branches.json", "testdata/golden/regression/allof_if_then_branches.go"},
		{"regression/nullable_array_items", "testdata/schemas/regression/nullable_array_items.json", "testdata/golden/regression/nullable_array_items.go"},
		{"regression/draft3_type_union", "testdata/schemas/regression/draft3_type_union.json", "testdata/golden/regression/draft3_type_union.go"},
		{"regression/draft3_type_multi", "testdata/schemas/regression/draft3_type_multi.json", "testdata/golden/regression/draft3_type_multi.go"},
		{"regression/property_count", "testdata/schemas/regression/property_count.json", "testdata/golden/regression/property_count.go"},
		{"regression/allof_tightest_constraints", "testdata/schemas/regression/allof_tightest_constraints.json", "testdata/golden/regression/allof_tightest_constraints.go"},
		{"regression/anyof_required_branches", "testdata/schemas/regression/anyof_required_branches.json", "testdata/golden/regression/anyof_required_branches.go"},
		{"regression/anyof_required_only", "testdata/schemas/regression/anyof_required_only.json", "testdata/golden/regression/anyof_required_only.go"},
		{"regression/validatable_field_fmt", "testdata/schemas/regression/validatable_field_fmt.json", "testdata/golden/regression/validatable_field_fmt.go"},
		{"regression/quoted_property_name", "testdata/schemas/regression/quoted_property_name.json", "testdata/golden/regression/quoted_property_name.go"},
		{"regression/oneof_optional_const", "testdata/schemas/regression/oneof_optional_const.json", "testdata/golden/regression/oneof_optional_const.go"},
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
	return generateFromSchemaWithConfig(t, schemaPath, generator.Config{
		PackageName: "testpkg",
		OmitEmpty:   true,
	})
}

// generateFromSchemaWithConfig runs the pipeline with a custom generator config.
func generateFromSchemaWithConfig(t *testing.T, schemaPath string, cfg generator.Config) []byte {
	t.Helper()

	fullPath := filepath.Join("..", schemaPath)
	s, err := schema.LoadFromFile(fullPath)
	if err != nil {
		t.Fatalf("loading schema %s: %v", schemaPath, err)
	}

	s.Normalize()

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

// TestGoldenBigInt tests golden output with --big-int enabled.
func TestGoldenBigInt(t *testing.T) {
	tests := []goldenTestCase{
		{"bigint/integer_constraints", "testdata/schemas/bigint/integer_constraints.json", "testdata/golden/bigint/integer_constraints.go"},
	}
	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			got := generateFromSchemaWithConfig(t, tc.SchemaPath, generator.Config{
				PackageName:   "testpkg",
				OmitEmpty:     true,
				BigIntSupport: true,
			})

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
				gotLines := strings.Split(string(got), "\n")
				wantLines := strings.Split(string(want), "\n")
				for i := range gotLines {
					if i >= len(wantLines) {
						t.Logf("  line %d:\n\tgot:  %q\n\twant: %q", i+1, gotLines[i], "")
						continue
					}
					if gotLines[i] != wantLines[i] {
						t.Logf("  line %d:\n\tgot:  %q\n\twant: %q", i+1, gotLines[i], wantLines[i])
					}
				}
			}
		})
	}
}

// TestBigIntRoundTrip tests marshal/unmarshal round-trip for BigInt types
// using various values: small int, large int (overflow int64), boundary values.
func TestBigIntRoundTrip(t *testing.T) {
	schemaPath := "testdata/schemas/bigint/integer_constraints.json"
	generated := generateFromSchemaWithConfig(t, schemaPath, generator.Config{
		PackageName:   "testpkg",
		OmitEmpty:     true,
		BigIntSupport: true,
	})

	tmpDir := t.TempDir()

	generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}

	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	testCases := []struct {
		name  string
		input string
	}{
		{"zero", "0"},
		{"small_int", "42"},
		{"max_int64", "9223372036854775807"},
		{"overflow_int64", "9223372036854775808"},
		{"large_bigint", "123456789012345678901234567890"},
		{"just_under_max", "999999999999999999999999999999"},
	}

	var errs []string
	for _, tc := range testCases {
		var c Counter
		if err := json.Unmarshal([]byte(tc.input), &c); err != nil {
			errs = append(errs, fmt.Sprintf("%s: unmarshal failed: %v", tc.name, err))
			continue
		}

		// Validate
		if err := c.Validate(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: validate failed: %v", tc.name, err))
			continue
		}

		// Marshal back
		out, err := json.Marshal(c)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: marshal failed: %v", tc.name, err))
			continue
		}

		// Compare: the marshaled value should equal the input
		if string(out) != tc.input {
			errs = append(errs, fmt.Sprintf("%s: round-trip mismatch: got %s, want %s", tc.name, string(out), tc.input))
		}
	}

	// Test validation failures (exclusiveMaximum = 1e30, minimum = 0)
	invalidCases := []struct {
		name  string
		input string
	}{
		{"negative", "-1"},
		{"at_exclusive_max", "1000000000000000000000000000000"},
		{"over_max", "1000000000000000000000000000001"},
	}

	for _, tc := range invalidCases {
		var c Counter
		if err := json.Unmarshal([]byte(tc.input), &c); err != nil {
			errs = append(errs, fmt.Sprintf("%s: unmarshal should succeed: %v", tc.name, err))
			continue
		}
		if err := c.Validate(); err == nil {
			errs = append(errs, fmt.Sprintf("%s: expected validation error but got nil", tc.name))
		}
	}

	// Test invalid types
	invalidTypes := []struct {
		name  string
		input string
	}{
		{"null", "null"},
		{"string", "\"42\""},
		{"float", "3.14"},
	}

	for _, tc := range invalidTypes {
		var c Counter
		if err := json.Unmarshal([]byte(tc.input), &c); err == nil {
			// For float, check if it was accepted (it shouldn't be since 3.14 has fractional part)
			if tc.name == "float" {
				// 3.14 should fail because it's not an integer
				errs = append(errs, fmt.Sprintf("%s: expected unmarshal error for non-integer float", tc.name))
			}
		}
	}

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "FAIL: %s\n", e)
		}
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}

	if err := writeTestGoMod(tmpDir, "bigint_roundtrip_test"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("BigInt round-trip test failed:\n%s\nerror: %v", string(output), err)
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr != "PASS" {
		t.Fatalf("BigInt round-trip test output:\n%s", outputStr)
	}
}

// TestValidationErrorPaths verifies that nested validation errors include the full JSON path.
func TestValidationErrorPaths(t *testing.T) {
	schemaPath := "testdata/schemas/validation/nested_errors.json"
	generated := generateFromSchema(t, schemaPath)

	tmpDir := t.TempDir()

	generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}

	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	testCases := []struct {
		name        string
		input       string
		wantErr     string
	}{
		{
			name:    "nested_object_field",
			input:   ` + "`" + `{"name":"Acme","address":{"street":"Main","city":"A"}}` + "`" + `,
			wantErr: "address.city:",
		},
		{
			name:    "nested_object_pattern",
			input:   ` + "`" + `{"name":"Acme","address":{"street":"Main","city":"NY","zip":"bad"}}` + "`" + `,
			wantErr: "address.zip:",
		},
		{
			name:    "array_element_field",
			input:   ` + "`" + `{"name":"Acme","address":{"street":"Main","city":"NY"},"employees":[{"name":"Alice","age":200}]}` + "`" + `,
			wantErr: "employees[0].age:",
		},
		{
			name:    "array_second_element",
			input:   ` + "`" + `{"name":"Acme","address":{"street":"Main","city":"NY"},"employees":[{"name":"Alice","age":25},{"name":"","age":30}]}` + "`" + `,
			wantErr: "employees[1].name:",
		},
		{
			name:    "root_level_field",
			input:   ` + "`" + `{"name":"","address":{"street":"Main","city":"NY"}}` + "`" + `,
			wantErr: "name:",
		},
		{
			// A required property missing from a nested object must be reported
			// with the parent's path, as a validation error (not a parse error).
			name:    "nested_missing_required",
			input:   ` + "`" + `{"name":"Acme","address":{"city":"NY"}}` + "`" + `,
			wantErr: "address.street: required property is missing",
		},
		{
			// A required property missing from an array element carries the index.
			name:    "array_element_missing_required",
			input:   ` + "`" + `{"name":"Acme","address":{"street":"Main","city":"NY"},"employees":[{"age":25}]}` + "`" + `,
			wantErr: "employees[0].name: required property is missing",
		},
		{
			// A required property missing at the root is reported without a prefix.
			name:    "root_missing_required",
			input:   ` + "`" + `{"name":"Acme"}` + "`" + `,
			wantErr: "address: required property is missing",
		},
	}

	var errs []string
	for _, tc := range testCases {
		var c Company
		if err := json.Unmarshal([]byte(tc.input), &c); err != nil {
			errs = append(errs, fmt.Sprintf("%s: unmarshal failed: %v", tc.name, err))
			continue
		}
		err := c.Validate()
		if err == nil {
			errs = append(errs, fmt.Sprintf("%s: expected validation error but got nil", tc.name))
			continue
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			errs = append(errs, fmt.Sprintf("%s: error %q does not contain %q", tc.name, err.Error(), tc.wantErr))
		}
	}

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "FAIL: %s\n", e)
		}
		os.Exit(1)
	}
	fmt.Println("PASS")
}

`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}

	if err := writeTestGoMod(tmpDir, "error_path_test"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("validation error path test failed:\n%s\nerror: %v", string(output), err)
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr != "PASS" {
		t.Fatalf("validation error path test output:\n%s", outputStr)
	}
}

func TestNestedRemoteItemsValidation(t *testing.T) {
	input := `{
		"id": "http://localhost:1234/",
		"items": {
			"id": "baseUriChange/",
			"items": {"$ref": "folderInteger.json"}
		}
	}`
	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	s.Normalize()
	base, err := url.Parse("http://localhost:1234/")
	if err != nil {
		t.Fatalf("parse base uri: %v", err)
	}
	s.ComputeBaseURIs(base, &s)
	remote := &schema.Schema{Type: schema.TypeList{"integer"}}
	gen := generator.New(generator.Config{
		PackageName: "testpkg",
		OmitEmpty:   true,
		Draft:       schema.Draft03,
		Resolver: schema.NewMappingResolver(map[string]*schema.Schema{
			"http://localhost:1234/baseUriChange/folderInteger.json": remote,
		}),
	})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, td := range ir.TypeDefs {
		if alias, ok := td.(*generator.InferredAliasDef); ok && alias.Name == "Root" {
			if alias.ItemsNested == nil {
				t.Fatalf("root IR missing nested item validation: %#v", alias)
			}
		}
	}
	em, err := emitter.New()
	if err != nil {
		t.Fatalf("emitter: %v", err)
	}
	generated, err := em.Emit(ir)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(string(generated), "items[%d][%d]") {
		t.Fatalf("generated code missing nested item validation:\n%s", string(generated))
	}

	tmpDir := t.TempDir()
	generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}

	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	valid := []byte(` + "`" + `[[1]]` + "`" + `)
	var validObj Root
	if err := json.Unmarshal(valid, &validObj); err != nil {
		fmt.Fprintf(os.Stderr, "valid unmarshal: %v\n", err)
		os.Exit(1)
	}
	if err := validObj.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "valid validate: %v\n", err)
		os.Exit(1)
	}

	invalid := []byte(` + "`" + `[["a"]]` + "`" + `)
	var invalidObj Root
	if err := json.Unmarshal(invalid, &invalidObj); err != nil {
		fmt.Fprintf(os.Stderr, "invalid unmarshal should succeed: %v\n", err)
		os.Exit(1)
	}
	if err := invalidObj.Validate(); err == nil {
		fmt.Fprintf(os.Stderr, "invalid validate: expected error\n")
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}
	if err := writeTestGoMod(tmpDir, "nested_remote_items_test"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("nested remote items test failed:\n%s\nerror: %v", string(output), err)
	}
	if strings.TrimSpace(string(output)) != "PASS" {
		t.Fatalf("nested remote items output:\n%s", string(output))
	}
}
