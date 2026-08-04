package tests

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// roundTripTestCase defines a round-trip test: schema + fixture JSON.
type roundTripTestCase struct {
	Name        string
	SchemaPath  string
	FixturePath string
}

func allRoundTripTests() []roundTripTestCase {
	return []roundTripTestCase{
		{
			Name:        "basic/simple_object",
			SchemaPath:  "testdata/schemas/basic/simple_object.json",
			FixturePath: "testdata/fixtures/basic/simple_object.json",
		},
		{
			Name:        "basic/nested_object",
			SchemaPath:  "testdata/schemas/basic/nested_object.json",
			FixturePath: "testdata/fixtures/basic/nested_object.json",
		},
		{
			Name:        "basic/array_types",
			SchemaPath:  "testdata/schemas/basic/array_types.json",
			FixturePath: "testdata/fixtures/basic/array_types.json",
		},
		{
			Name:        "basic/additional_properties",
			SchemaPath:  "testdata/schemas/basic/additional_properties.json",
			FixturePath: "testdata/fixtures/basic/additional_properties.json",
		},
		{
			Name:        "basic/additional_properties_bool",
			SchemaPath:  "testdata/schemas/basic/additional_properties_bool.json",
			FixturePath: "testdata/fixtures/basic/additional_properties_bool.json",
		},
		{
			Name:        "composition/allof_simple",
			SchemaPath:  "testdata/schemas/composition/allof_simple.json",
			FixturePath: "testdata/fixtures/composition/allof_simple.json",
		},
		{
			Name:        "composition/oneof_simple_circle",
			SchemaPath:  "testdata/schemas/composition/oneof_simple.json",
			FixturePath: "testdata/fixtures/composition/oneof_simple.json",
		},
		{
			Name:        "composition/oneof_simple_rect",
			SchemaPath:  "testdata/schemas/composition/oneof_simple.json",
			FixturePath: "testdata/fixtures/composition/oneof_simple_rect.json",
		},
		{
			Name:        "composition/oneof_array_items",
			SchemaPath:  "testdata/schemas/composition/oneof_array_items.json",
			FixturePath: "testdata/fixtures/composition/oneof_array_items.json",
		},
		{
			// Regression: a oneOf variant that is itself a oneOf used to
			// disable discriminator detection, so the parent fell back to
			// trial unmarshalling. Because "const" is not enforced during
			// unmarshalling, sibling variants with identical required fields
			// both matched and the document failed to decode with
			// "multiple oneOf variants matched".
			//
			// The fixture deliberately selects "beta", the *second* value of
			// the nested union: that case is only emitted when the variant
			// carries the full DiscriminatorValues set, so an implementation
			// keeping only the singular first value fails here.
			Name:        "composition/oneof_nested_variants",
			SchemaPath:  "testdata/schemas/composition/oneof_nested_variants.json",
			FixturePath: "testdata/fixtures/composition/oneof_nested_variants.json",
		},
		{
			// Regression: an optional property whose type is a $ref to a
			// constrained array definition becomes a named slice type with its
			// own Validate() (e.g. `type TrackList []TrackListItem`). The
			// presence guard for that field must be `!= nil`, not `!= ""`
			// (a named slice's zero literal is nil) — otherwise the generated
			// code fails to compile.
			Name:        "composition/optional_ref_array",
			SchemaPath:  "testdata/schemas/composition/optional_ref_array.json",
			FixturePath: "testdata/fixtures/composition/optional_ref_array.json",
		},
		{
			Name:        "enum/string_enum",
			SchemaPath:  "testdata/schemas/enum/string_enum.json",
			FixturePath: "testdata/fixtures/enum/string_enum.json",
		},
		{
			// Regression: an optional property whose enum contains null
			// becomes a raw enum backed by json.RawMessage (a byte slice).
			// Its presence guard must be `!= nil`, not `!= ""` — otherwise
			// the generated code fails to compile.
			Name:        "enum/optional_nullable_enum",
			SchemaPath:  "testdata/schemas/enum/optional_nullable_enum.json",
			FixturePath: "testdata/fixtures/enum/optional_nullable_enum.json",
		},
		{
			Name:        "basic/primitive_types",
			SchemaPath:  "testdata/schemas/basic/primitive_types.json",
			FixturePath: "testdata/fixtures/basic/primitive_types.json",
		},
		{
			Name:        "refs/defs_ref",
			SchemaPath:  "testdata/schemas/refs/defs_ref.json",
			FixturePath: "testdata/fixtures/refs/defs_ref.json",
		},
		{
			Name:        "refs/definitions_ref",
			SchemaPath:  "testdata/schemas/refs/definitions_ref.json",
			FixturePath: "testdata/fixtures/refs/definitions_ref.json",
		},
		{
			Name:        "composition/anyof_simple",
			SchemaPath:  "testdata/schemas/composition/anyof_simple.json",
			FixturePath: "testdata/fixtures/composition/anyof_simple.json",
		},
		{
			Name:        "composition/oneof_complex",
			SchemaPath:  "testdata/schemas/composition/oneof_complex.json",
			FixturePath: "testdata/fixtures/composition/oneof_complex.json",
		},
		{
			Name:        "composition/oneof_with_null",
			SchemaPath:  "testdata/schemas/composition/oneof_with_null.json",
			FixturePath: "testdata/fixtures/composition/oneof_with_null.json",
		},
		{
			Name:        "composition/oneof_with_null_nil",
			SchemaPath:  "testdata/schemas/composition/oneof_with_null.json",
			FixturePath: "testdata/fixtures/composition/oneof_with_null_nil.json",
		},
		{
			Name:        "validation/string_constraints",
			SchemaPath:  "testdata/schemas/validation/string_constraints.json",
			FixturePath: "testdata/fixtures/validation/string_constraints.json",
		},
		{
			Name:        "validation/numeric_constraints",
			SchemaPath:  "testdata/schemas/validation/numeric_constraints.json",
			FixturePath: "testdata/fixtures/validation/numeric_constraints.json",
		},
		{
			Name:        "formats/datetime",
			SchemaPath:  "testdata/schemas/formats/datetime.json",
			FixturePath: "testdata/fixtures/formats/datetime.json",
		},
		{
			Name:        "composition/oneof_discriminator_click",
			SchemaPath:  "testdata/schemas/composition/oneof_discriminator.json",
			FixturePath: "testdata/fixtures/composition/oneof_discriminator_click.json",
		},
		{
			Name:        "composition/oneof_discriminator_keypress",
			SchemaPath:  "testdata/schemas/composition/oneof_discriminator.json",
			FixturePath: "testdata/fixtures/composition/oneof_discriminator_keypress.json",
		},
		{
			Name:        "composition/oneof_discriminator_heuristic",
			SchemaPath:  "testdata/schemas/composition/oneof_discriminator_heuristic.json",
			FixturePath: "testdata/fixtures/composition/oneof_discriminator_heuristic.json",
		},
		{
			Name:        "defaults/server_config",
			SchemaPath:  "testdata/schemas/defaults/server_config.json",
			FixturePath: "testdata/fixtures/defaults/server_config.json",
		},
		{
			Name:        "validation/unevaluated_items",
			SchemaPath:  "testdata/schemas/validation/unevaluated_items.json",
			FixturePath: "testdata/fixtures/validation/unevaluated_items.json",
		},
		{
			Name:        "advanced/recursive_tree",
			SchemaPath:  "testdata/schemas/advanced/recursive_tree.json",
			FixturePath: "testdata/fixtures/advanced/recursive_tree.json",
		},
		{
			// Nullable arrays ([]*T / []T, no omitempty) preserve null, a null
			// element, and an explicit empty [] across a round-trip.
			Name:        "regression/nullable_array_items",
			SchemaPath:  "testdata/schemas/regression/nullable_array_items.json",
			FixturePath: "testdata/fixtures/regression/nullable_array_items.json",
		},
		{
			// Optional (non-nullable) array uses ",omitzero": a present-but-empty
			// array (tags:[]) is preserved while an absent one (labels) is omitted.
			Name:        "regression/optional_empty_array",
			SchemaPath:  "testdata/schemas/regression/optional_empty_array.json",
			FixturePath: "testdata/fixtures/regression/optional_empty_array.json",
		},
		{
			// Mutually recursive $ref/$dynamicRef: a nested optional object field
			// must be pointer-wrapped even while its struct is mid-generation, so
			// an absent nested object is omitted rather than materialized as "{}".
			Name:        "regression/dynamicref_recursive",
			SchemaPath:  "testdata/schemas/regression/dynamicref_recursive.json",
			FixturePath: "testdata/fixtures/regression/dynamicref_recursive.json",
		},
		{
			Name:        "advanced/pattern_properties",
			SchemaPath:  "testdata/schemas/advanced/pattern_properties.json",
			FixturePath: "testdata/fixtures/advanced/pattern_properties.json",
		},
		{
			Name:        "advanced/nullable_const",
			SchemaPath:  "testdata/schemas/advanced/nullable_const.json",
			FixturePath: "testdata/fixtures/advanced/nullable_const.json",
		},
		{
			Name:        "advanced/tuple_array",
			SchemaPath:  "testdata/schemas/advanced/tuple_array.json",
			FixturePath: "testdata/fixtures/advanced/tuple_array.json",
		},
		{
			Name:        "advanced/cross_refs",
			SchemaPath:  "testdata/schemas/advanced/cross_refs.json",
			FixturePath: "testdata/fixtures/advanced/cross_refs.json",
		},
		{
			Name:        "regression/allof_oneof_variants",
			SchemaPath:  "testdata/schemas/regression/allof_oneof_variants.json",
			FixturePath: "testdata/fixtures/regression/allof_oneof_variants.json",
		},
		{
			Name:        "regression/allof_if_then_branches",
			SchemaPath:  "testdata/schemas/regression/allof_if_then_branches.json",
			FixturePath: "testdata/fixtures/regression/allof_if_then_branches.json",
		},
	}
}

// TestRoundTrip generates Go code from a schema, then creates a small Go program
// that unmarshals the fixture JSON into the generated type, marshals it back, and
// compares the result for semantic equality.
func TestRoundTrip(t *testing.T) {
	for _, tc := range allRoundTripTests() {
		t.Run(tc.Name, func(t *testing.T) {
			// 1. Generate Go code from the schema
			generated := generateFromSchema(t, tc.SchemaPath)

			// 2. Read fixture JSON
			fixturePath := filepath.Join("..", tc.FixturePath)
			fixtureData, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatalf("reading fixture: %v", err)
			}

			// 3. Determine the root type name from the generated code
			rootType := extractRootTypeName(t, string(generated))

			// 4. Create a temp directory with a Go module
			tmpDir := t.TempDir()

			// Write the generated code, replacing package name with "main"
			generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
			if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
				t.Fatalf("writing types.go: %v", err)
			}
			writeSharedHelpers(t, tmpDir, generatedMain)

			// Write the fixture JSON
			if err := os.WriteFile(filepath.Join(tmpDir, "fixture.json"), fixtureData, 0o644); err != nil {
				t.Fatalf("writing fixture.json: %v", err)
			}

			// Write a main.go that does the round-trip test
			mainGo := generateRoundTripMain(rootType)
			if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
				t.Fatalf("writing main.go: %v", err)
			}

			// Write go.mod + go.sum (with goecma262 dependency for generated code)
			if err := writeTestGoMod(tmpDir, "roundtrip_test"); err != nil {
				t.Fatalf("writing go.mod: %v", err)
			}

			// 5. Build and run the test program
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "run", ".")
			cmd.Dir = tmpDir
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("round-trip test failed:\n%s\nerror: %v", string(output), err)
			}

			outputStr := programOutput(output)
			if outputStr != "PASS" {
				t.Fatalf("round-trip test output:\n%s", outputStr)
			}
		})
	}
}

// TestCompile verifies that all generated golden files compile.
func TestCompile(t *testing.T) {
	// Collect all golden files
	goldenDirs := []string{
		"testdata/golden/basic",
		"testdata/golden/refs",
		"testdata/golden/enum",
		"testdata/golden/composition",
		"testdata/golden/validation",
		"testdata/golden/formats",
		"testdata/golden/defaults",
		"testdata/golden/advanced",
		"testdata/golden/bigint",
		"testdata/golden/regression",
	}

	// We can't compile all files together since they may have conflicting type names
	// (e.g., Address in nested_object.go and defs_ref.go). Instead, compile each separately.
	for _, dir := range goldenDirs {
		fullDir := filepath.Join("..", dir)
		entries, err := os.ReadDir(fullDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("reading %s: %v", dir, err)
		}

		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".go") {
				continue
			}

			t.Run(dir+"/"+entry.Name(), func(t *testing.T) {
				singleTmpDir := t.TempDir()

				if err := writeTestGoMod(singleTmpDir, "compile_test"); err != nil {
					t.Fatalf("writing go.mod: %v", err)
				}

				data, err := os.ReadFile(filepath.Join(fullDir, entry.Name()))
				if err != nil {
					t.Fatalf("reading golden file: %v", err)
				}

				content := strings.Replace(string(data), "package testpkg", "package compile_test", 1)
				if err := os.WriteFile(filepath.Join(singleTmpDir, entry.Name()), []byte(content), 0o644); err != nil {
					t.Fatalf("writing file: %v", err)
				}
				writeSharedHelpers(t, singleTmpDir, content)

				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "go", "build", ".")
				cmd.Dir = singleTmpDir
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("compilation failed:\n%s\nerror: %v", string(output), err)
				}
			})
		}
	}
}

// extractRootTypeName finds the root struct type in the generated code.
// It looks for the last struct type that has json-tagged fields (not wrapper structs
// which have no json tags). Wrapper structs for oneOf have fields without json tags.
// Only considers top-level type declarations (no leading whitespace) to avoid
// picking up type aliases inside function bodies (e.g. "type Alias X" in UnmarshalJSON).
func extractRootTypeName(t *testing.T, code string) string {
	t.Helper()

	lines := strings.Split(code, "\n")
	var lastType string
	var currentType string
	var hasJSONTag bool

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Only consider top-level type declarations (starts at column 0)
		if strings.HasPrefix(line, "type ") && strings.Contains(trimmed, " struct {") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 2 {
				currentType = parts[1]
				hasJSONTag = false
			}
		}
		if currentType != "" && strings.Contains(trimmed, "`json:\"") {
			hasJSONTag = true
		}
		if trimmed == "}" && currentType != "" {
			if hasJSONTag {
				lastType = currentType
			}
			currentType = ""
		}
	}

	if lastType == "" {
		// Fallback: just find the last top-level struct
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(line, "type ") && strings.Contains(trimmed, " struct {") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 2 {
					lastType = parts[1]
				}
			}
		}
	}

	if lastType == "" {
		// Final fallback: look for top-level type aliases (e.g., "type Root = any" or "type Root []any").
		// Only consider lines starting at column 0 to skip inner Alias declarations.
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(line, "type ") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 2 {
					lastType = parts[1]
					if parts[1] == "Root" {
						return "Root"
					}
				}
			}
		}
	}

	if lastType == "" {
		t.Fatal("could not find root type in generated code")
	}
	return lastType
}

// generateRoundTripMain creates a Go main() that:
// 1. Reads fixture.json
// 2. Unmarshals into the generated type
// 3. Marshals back to JSON
// 4. Compares original and round-tripped JSON for semantic equality
func generateRoundTripMain(rootType string) string {
	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
)

func main() {
	// Read fixture
	data, err := os.ReadFile("fixture.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading fixture: %%v\n", err)
		os.Exit(1)
	}

	// Unmarshal into typed struct
	var obj %s
	if err := json.Unmarshal(data, &obj); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal: %%v\n", err)
		os.Exit(1)
	}

	// Marshal back to JSON
	roundTripped, err := json.Marshal(obj)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %%v\n", err)
		os.Exit(1)
	}

	// Compare semantically: unmarshal both into any (handles objects, arrays, primitives)
	var original, result any
	if err := json.Unmarshal(data, &original); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal original: %%v\n", err)
		os.Exit(1)
	}
	if err := json.Unmarshal(roundTripped, &result); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal result: %%v\n", err)
		os.Exit(1)
	}

	if !reflect.DeepEqual(original, result) {
		fmt.Fprintf(os.Stderr, "ROUND-TRIP MISMATCH\n")
		fmt.Fprintf(os.Stderr, "Original:     %%s\n", string(data))
		fmt.Fprintf(os.Stderr, "Round-tripped: %%s\n", string(roundTripped))
		os.Exit(1)
	}

	fmt.Println("PASS")
}
`, rootType)
}

// TestDefaults generates Go code from the defaults schema, then creates a small Go program
// that unmarshals minimal JSON (only required fields), calls SetDefaults(), and verifies
// that default values were applied correctly.
func TestDefaults(t *testing.T) {
	schemaPath := "testdata/schemas/defaults/server_config.json"
	generated := generateFromSchema(t, schemaPath)

	rootType := extractRootTypeName(t, string(generated))

	tmpDir := t.TempDir()

	generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}
	writeSharedHelpers(t, tmpDir, generatedMain)

	// Minimal JSON: only the required field "name"
	minimalJSON := `{"name":"myserver"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "fixture.json"), []byte(minimalJSON), 0o644); err != nil {
		t.Fatalf("writing fixture.json: %v", err)
	}

	// Write a main.go that tests SetDefaults
	mainGo := generateDefaultsMain(rootType)
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}

	if err := writeTestGoMod(tmpDir, "defaults_test"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("defaults test failed:\n%s\nerror: %v", string(output), err)
	}

	outputStr := programOutput(output)
	if outputStr != "PASS" {
		t.Fatalf("defaults test output:\n%s", outputStr)
	}
}

// generateDefaultsMain creates a Go main() that:
// 1. Reads fixture.json (minimal — only required fields)
// 2. Unmarshals into the generated type
// 3. Calls SetDefaults()
// 4. Verifies that default values are applied correctly
func generateDefaultsMain(rootType string) string {
	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func intPtr(v int64) *int64       { return &v }
func floatPtr(v float64) *float64 { return &v }
func stringPtr(v string) *string  { return &v }

func main() {
	data, err := os.ReadFile("fixture.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading fixture: %%v\n", err)
		os.Exit(1)
	}

	var obj %s
	if err := json.Unmarshal(data, &obj); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal: %%v\n", err)
		os.Exit(1)
	}

	// Before SetDefaults: optional fields should be nil (pointer types) or zero
	if obj.Host != nil {
		fmt.Fprintf(os.Stderr, "before SetDefaults: Host should be nil, got %%q\n", *obj.Host)
		os.Exit(1)
	}
	if obj.Port != nil {
		fmt.Fprintf(os.Stderr, "before SetDefaults: Port should be nil, got %%v\n", *obj.Port)
		os.Exit(1)
	}

	// Call SetDefaults
	obj.SetDefaults()

	// After SetDefaults: default values should be applied
	var errs []string
	if obj.Name != "myserver" {
		errs = append(errs, fmt.Sprintf("Name: got %%q, want %%q", obj.Name, "myserver"))
	}
	if obj.Host == nil || *obj.Host != "localhost" {
		errs = append(errs, fmt.Sprintf("Host: got %%v, want localhost", obj.Host))
	}
	if obj.Port == nil || *obj.Port != 8080 {
		errs = append(errs, fmt.Sprintf("Port: got %%v, want 8080", obj.Port))
	}
	if obj.Timeout == nil || *obj.Timeout != 30.5 {
		errs = append(errs, fmt.Sprintf("Timeout: got %%v, want 30.5", obj.Timeout))
	}
	if obj.Debug == nil || *obj.Debug != true {
		errs = append(errs, fmt.Sprintf("Debug: got %%v, want true", obj.Debug))
	}
	if obj.LogLevel == nil || *obj.LogLevel != "info" {
		errs = append(errs, fmt.Sprintf("LogLevel: got %%v, want info", obj.LogLevel))
	}
	if obj.MaxRetries == nil || *obj.MaxRetries != 3 {
		errs = append(errs, fmt.Sprintf("MaxRetries: got %%v, want 3", obj.MaxRetries))
	}

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "FAIL: %%s\n", e)
		}
		os.Exit(1)
	}

	// Also verify that SetDefaults does NOT overwrite explicitly set values
	obj2 := %s{Name: "test", Host: stringPtr("custom.host"), Port: intPtr(9999)}
	obj2.SetDefaults()
	if obj2.Host == nil || *obj2.Host != "custom.host" {
		errs = append(errs, fmt.Sprintf("SetDefaults overwrote Host: got %%v, want custom.host", obj2.Host))
	}
	if obj2.Port == nil || *obj2.Port != 9999 {
		errs = append(errs, fmt.Sprintf("SetDefaults overwrote Port: got %%v, want 9999", obj2.Port))
	}

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "FAIL: %%s\n", e)
		}
		os.Exit(1)
	}

	fmt.Println("PASS")
}
`, rootType, rootType)
}

// TestUnevaluatedItemsValidation generates Go code from the unevaluated_items schema,
// then creates a small Go program that verifies Validate() correctly rejects arrays
// with too many items when unevaluatedItems: false.
func TestUnevaluatedItemsValidation(t *testing.T) {
	schemaPath := "testdata/schemas/validation/unevaluated_items.json"
	generated := generateFromSchema(t, schemaPath)

	rootType := extractRootTypeName(t, string(generated))

	tmpDir := t.TempDir()

	generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}
	writeSharedHelpers(t, tmpDir, generatedMain)

	mainGo := generateUnevaluatedItemsMain(rootType)
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}

	if err := writeTestGoMod(tmpDir, "unevalitems_test"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("unevaluatedItems validation test failed:\n%s\nerror: %v", string(output), err)
	}

	outputStr := programOutput(output)
	if outputStr != "PASS" {
		t.Fatalf("unevaluatedItems validation test output:\n%s", outputStr)
	}
}

func TestAllOfOneOfCrossedTypesValidation(t *testing.T) {
	schemaPath := "testdata/schemas/regression/allof_oneof_crossed_types.json"
	generated := generateFromSchema(t, schemaPath)
	rootType := extractRootTypeName(t, string(generated))
	tmpDir := t.TempDir()

	generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}
	writeSharedHelpers(t, tmpDir, generatedMain)
	mainGo := generateAllOfOneOfCrossedTypesMain(rootType)
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}
	if err := writeTestGoMod(tmpDir, "crossed_types_test"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("crossed-types validation test failed:\n%s\nerror: %v", string(output), err)
	}
	if outputStr := programOutput(output); outputStr != "PASS" {
		t.Fatalf("crossed-types validation output:\n%s", outputStr)
	}
}

func generateAllOfOneOfCrossedTypesMain(rootType string) string {
	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	valid := []string{
		`+"`"+`{"kind":"left","a":"x","b":1}`+"`"+`,
		`+"`"+`{"kind":"right","a":1,"b":"x"}`+"`"+`,
	}
	invalid := []string{
		`+"`"+`{"kind":"left","a":"x","b":"x"}`+"`"+`,
		`+"`"+`{"kind":"right","a":"x","b":"x"}`+"`"+`,
	}
	for _, input := range valid {
		var obj %s
		if err := json.Unmarshal([]byte(input), &obj); err != nil {
			fmt.Fprintf(os.Stderr, "valid unmarshal failed: %%v\n", err)
			os.Exit(1)
		}
		if err := obj.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "valid should pass: %%v\n", err)
			os.Exit(1)
		}
	}
	for _, input := range invalid {
		var obj %s
		if err := json.Unmarshal([]byte(input), &obj); err != nil {
			fmt.Fprintf(os.Stderr, "invalid should still unmarshal: %%v\n", err)
			os.Exit(1)
		}
		if err := obj.Validate(); err == nil {
			fmt.Fprintf(os.Stderr, "invalid should fail validation: %%s\n", input)
			os.Exit(1)
		}
	}
	fmt.Println("PASS")
}
`, rootType, rootType)
}

// generateUnevaluatedItemsMain creates a Go main() that tests unevaluatedItems validation:
// 1. A valid tuple (within prefixItems limit) should pass Validate()
// 2. A tuple exceeding prefixItems should fail Validate() when unevaluatedItems: false
func generateUnevaluatedItemsMain(rootType string) string {
	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	var errs []string

	// Test 1: Valid strict_tuple (exactly 2 items, matching prefixItems)
	{
		input := `+"`"+`{"strict_tuple": ["hello", 42]}`+"`"+`
		var obj %s
		if err := json.Unmarshal([]byte(input), &obj); err != nil {
			errs = append(errs, fmt.Sprintf("unmarshal valid strict_tuple: %%v", err))
		} else if err := obj.Validate(); err != nil {
			errs = append(errs, fmt.Sprintf("valid strict_tuple should pass: %%v", err))
		}
	}

	// Test 2: Invalid strict_tuple (3 items, exceeds prefixItems when unevaluatedItems: false)
	{
		input := `+"`"+`{"strict_tuple": ["hello", 42, "extra"]}`+"`"+`
		var obj %s
		if err := json.Unmarshal([]byte(input), &obj); err != nil {
			errs = append(errs, fmt.Sprintf("unmarshal invalid strict_tuple: %%v", err))
		} else if err := obj.Validate(); err == nil {
			errs = append(errs, "invalid strict_tuple (3 items) should fail validation")
		} else if !strings.Contains(err.Error(), "strict_tuple") {
			errs = append(errs, fmt.Sprintf("error should mention strict_tuple: %%v", err))
		}
	}

	// Test 3: Empty strict_tuple should pass
	{
		input := `+"`"+`{"strict_tuple": []}`+"`"+`
		var obj %s
		if err := json.Unmarshal([]byte(input), &obj); err != nil {
			errs = append(errs, fmt.Sprintf("unmarshal empty strict_tuple: %%v", err))
		} else if err := obj.Validate(); err != nil {
			errs = append(errs, fmt.Sprintf("empty strict_tuple should pass: %%v", err))
		}
	}

	// Test 4: strict_tuple with 1 item (within bounds) should pass
	{
		input := `+"`"+`{"strict_tuple": ["only"]}`+"`"+`
		var obj %s
		if err := json.Unmarshal([]byte(input), &obj); err != nil {
			errs = append(errs, fmt.Sprintf("unmarshal 1-item strict_tuple: %%v", err))
		} else if err := obj.Validate(); err != nil {
			errs = append(errs, fmt.Sprintf("1-item strict_tuple should pass: %%v", err))
		}
	}

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "FAIL: %%s\n", e)
		}
		os.Exit(1)
	}

	fmt.Println("PASS")
}
`, rootType, rootType, rootType, rootType)
}

// runValidationCases generates code for schemaPath, then compiles and runs a
// small program that asserts every input in valid passes Validate() and every
// input in invalid fails it. Used by focused validation regression tests.
func runValidationCases(t *testing.T, schemaPath string, valid, invalid []string) {
	t.Helper()
	generated := generateFromSchema(t, schemaPath)
	rootType := extractRootTypeName(t, string(generated))
	tmpDir := t.TempDir()

	generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}
	writeSharedHelpers(t, tmpDir, generatedMain)
	mainGo := fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	valid := []string{%s}
	invalid := []string{%s}
	for _, input := range valid {
		var obj %s
		if err := json.Unmarshal([]byte(input), &obj); err != nil {
			fmt.Fprintf(os.Stderr, "valid unmarshal failed for %%s: %%v\n", input, err)
			os.Exit(1)
		}
		if err := obj.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "valid should pass %%s: %%v\n", input, err)
			os.Exit(1)
		}
	}
	for _, input := range invalid {
		var obj %s
		if err := json.Unmarshal([]byte(input), &obj); err != nil {
			continue // unmarshal-time rejection is an acceptable failure mode
		}
		if err := obj.Validate(); err == nil {
			fmt.Fprintf(os.Stderr, "invalid should fail validation: %%s\n", input)
			os.Exit(1)
		}
	}
	fmt.Println("PASS")
}
`, goStringSliceElems(valid), goStringSliceElems(invalid), rootType, rootType)
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}
	if err := writeTestGoMod(tmpDir, "validation_cases_test"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("validation cases failed:\n%s\nerror: %v", string(output), err)
	}
	if outputStr := programOutput(output); outputStr != "PASS" {
		t.Fatalf("validation cases output:\n%s", outputStr)
	}
}

// goStringSliceElems renders items as comma-separated Go string literals for
// embedding inside a []string{...} composite literal.
func goStringSliceElems(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = strconv.Quote(s)
	}
	return strings.Join(quoted, ", ")
}

// TestPropertyCountValidation checks that minProperties/maxProperties on a
// struct with declared properties count the properties actually present in the
// JSON, not the number of declared fields.
func TestPropertyCountValidation(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/property_count.json",
		[]string{
			`{"a":"x"}`,
			`{"a":"x","b":"y"}`,
			`{"a":"x","extra":"y"}`,
		},
		[]string{
			`{}`,
			`{"a":"x","b":"y","c":"z"}`,
		},
	)
}

// TestAllOfTightestConstraints checks that overlapping numeric constraints from
// multiple allOf branches are combined to the tightest bound (and multipleOf to
// the least common multiple), rather than keeping only the first branch's value.
func TestAllOfTightestConstraints(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/allof_tightest_constraints.json",
		[]string{`12`, `18`, `24`},
		[]string{
			`7`,  // below the tighter minimum (10)
			`8`,  // >= 10 but only a multiple of 2, not lcm(2,3)=6
			`10`, // >= 10 but not a multiple of 6
			// The schema declares "type":"integer" beside its allOf, and the
			// merge only ever takes a type off a *branch* -- so the declared one
			// has to be read from the parent or it is lost. Lost, the type is
			// inferred from the bounds instead, which makes it a guess rather
			// than an assertion and hands the schema the wrapper that accepts
			// every instance type. All three of these were then accepted by a
			// schema that says "integer".
			`"abc"`,
			`[1,2]`,
			`true`,
		},
	)
}

// TestIntegerOneOfConstraints checks that a schema declaring an integer type
// alongside a constraint-only oneOf preserves both the declared type and the
// oneOf branches: a value matching zero branches is rejected, a value matching
// exactly one branch passes, and a non-integer is rejected at unmarshal.
// Regression for the dispatch arm that previously produced `type Root any`.
func TestIntegerOneOfConstraints(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/integer_oneof_constraints.json",
		[]string{
			`12`, // >= 10 only → exactly 1 branch
			`3`,  // <= 5 only → exactly 1 branch
		},
		[]string{
			`7`,   // matches neither branch (>5 and <10) → 0 branches
			`"x"`, // string is rejected at unmarshal for an integer type
		},
	)
}

// TestPatternPropertiesPatternECMA checks that a `pattern` constraint on a
// patternProperties value schema compiles (it previously emitted a std-regexp
// call without importing "regexp") and is evaluated with ECMA-262 semantics:
// the lookahead `(?=a)` matches under ecma262 where std RE2 would panic.
// Regression for audit finding C2.
func TestPatternPropertiesPatternECMA(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/pp_pattern_ecma.json",
		[]string{
			`{"v1":"aaa"}`,
		},
		[]string{
			`{"v1":"b"}`,
		},
	)
}

// TestUnevaluatedPropertiesPattern checks that a `pattern` constraint on a
// schema-valued unevaluatedProperties compiles and validates via ECMA-262.
// The pattern constrains the property value; `"xfoo"` matches `^x`, `"yfoo"`
// does not. Regression for audit finding C2.
func TestUnevaluatedPropertiesPattern(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/unevaluated_properties_pattern.json",
		[]string{
			`{"a":"s","extra":"xfoo"}`,
		},
		[]string{
			`{"a":"s","extra":"yfoo"}`,
		},
	)
}

// TestPatternPropertiesTypeList checks that a patternProperties value schema
// with a type list (`["string","null"]`) accepts a value whose JSON type
// matches any listed type, rather than only the first. Regression for audit
// finding C8, which previously rejected legal `null`.
func TestPatternPropertiesTypeList(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/pp_type_list.json",
		[]string{
			`{"v1":null}`,
			`{"v1":"s"}`,
		},
		[]string{
			`{"v1":5}`,
		},
	)
}

// TestFieldNameCollisions is a compile-and-run regression for audit finding C3:
// property names that derive to a generated member (Validate method, the
// AdditionalProperties overflow field) or to a Go keyword (type) must produce
// compilable code that round-trips losslessly and whose Validate() still works.
// The renamed Go fields keep their original JSON tags, so the wire format is
// unaffected — a value with all three colliding properties plus an unknown key
// (captured by the overflow field) survives an unmarshal→marshal cycle intact.
func TestFieldNameCollisions(t *testing.T) {
	schemaPath := "testdata/schemas/regression/field_name_collisions.json"
	generated := generateFromSchema(t, schemaPath)
	rootType := extractRootTypeName(t, string(generated))
	tmpDir := t.TempDir()

	generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}
	writeSharedHelpers(t, tmpDir, generatedMain)
	mainGo := generateFieldNameCollisionsMain(rootType)
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}
	if err := writeTestGoMod(tmpDir, "field_name_collisions_test"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("field-name-collision test failed:\n%s\nerror: %v", string(output), err)
	}
	if outputStr := programOutput(output); outputStr != "PASS" {
		t.Fatalf("field-name-collision output:\n%s", outputStr)
	}
}

func generateFieldNameCollisionsMain(rootType string) string {
	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
)

func main() {
	input := `+"`"+`{"validate":"x","additionalProperties":true,"type":"t","extra":1}`+"`"+`

	var obj %s
	if err := json.Unmarshal([]byte(input), &obj); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal: %%v\n", err)
		os.Exit(1)
	}

	// Validate() (the generated method, distinct from the "validate" property
	// field) must succeed for this valid document.
	if err := obj.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Validate should pass: %%v\n", err)
		os.Exit(1)
	}

	out, err := json.Marshal(obj)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %%v\n", err)
		os.Exit(1)
	}

	var original, result any
	if err := json.Unmarshal([]byte(input), &original); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal original: %%v\n", err)
		os.Exit(1)
	}
	if err := json.Unmarshal(out, &result); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal result: %%v\n", err)
		os.Exit(1)
	}
	if !reflect.DeepEqual(original, result) {
		fmt.Fprintf(os.Stderr, "ROUND-TRIP MISMATCH\nOriginal:      %%s\nRound-tripped: %%s\n", input, string(out))
		os.Exit(1)
	}

	fmt.Println("PASS")
}
`, rootType)
}

// TestAnyOfRequiredBranches checks that an anyOf whose variants are
// distinguished by required properties rejects an object matching no branch,
// rather than validating everything (the merged struct used to drop the
// per-branch constraints).
func TestAnyOfRequiredBranches(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/anyof_required_branches.json",
		[]string{
			`{"a":"x"}`,
			`{"b":"y"}`,
			`{"a":"x","b":"y"}`,
		},
		[]string{
			`{}`,
			`{"c":"z"}`,
		},
	)
}

// TestAnyOfRequiredOnly covers an anyOf whose branches are distinguished only
// by required properties (no type checks). The generated validator must not
// import "bytes" (which is only used by property checks), and must still reject
// an object matching neither branch.
func TestAnyOfRequiredOnly(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/anyof_required_only.json",
		[]string{`{"a":1}`, `{"b":2}`, `{"a":1,"b":2}`},
		[]string{`{}`, `{"c":3}`},
	)
}

// TestDraft3TypeMultiBranch covers a draft-3 schema-valued type union where one
// branch lists multiple JSON types ({"type":["array","null"]}). The types
// within a branch are alternatives (OR), so an array or null must validate
// while an integer must not.
func TestDraft3TypeMultiBranch(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/draft3_type_multi.json",
		[]string{`"foo"`, `[1,2,3]`, `null`, `[]`},
		[]string{`1`, `true`, `{}`},
	)
}

// TestAdditionalPropertiesTypedValues covers an object property whose whole
// shape is additionalProperties. Its values carry a schema, so the field is a
// map of that type and every value is validated; map[string]any would accept
// anything the schema forbids.
func TestAdditionalPropertiesTypedValues(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/additional_properties_typed_values.json",
		[]string{
			`{}`,
			`{"definitions":{}}`,
			`{"definitions":{"a":{"name":"xy"}}}`,
		},
		[]string{
			`{"definitions":{"a":{"name":"x"}}}`,
			`{"definitions":{"a":{}}}`,
		},
	)
}

// TestDraft3TypeUnionProperty covers a property whose draft-3 "type" array
// mixes a schema alternative with "array" — the shape the draft-3 meta-schema
// gives "items". The property must accept both an object matching the
// referenced schema and an array of them, and must enforce the referenced
// schema in either position: a single Go type can express neither alternative
// on its own, and picking one used to make the other fail at unmarshal.
func TestDraft3TypeUnionProperty(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/draft3_type_union_property.json",
		[]string{
			`{}`,
			`{"items":{"title":"x"}}`,
			`{"items":[{"title":"x"},{"title":"y"}]}`,
			`{"items":[]}`,
		},
		[]string{
			`{"items":{"title":1}}`,
			`{"items":[{"title":1}]}`,
			`{"items":"nope"}`,
			`{"items":5}`,
		},
	)
}

// TestAnyOfUnionAlternatives covers an anyOf whose alternatives share no Go
// type — a string enum or an array of them. The property used to fall back to
// `any`, which validated nothing at all; each alternative now has a generated
// type of its own that the value is checked against.
func TestAnyOfUnionAlternatives(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/anyof_union_alternatives.json",
		[]string{
			`{}`,
			`{"type":"string"}`,
			`{"type":["string","number"]}`,
		},
		[]string{
			`{"type":1}`,
			`{"type":"bogus"}`,
			`{"type":[]}`,
			`{"type":["string","string"]}`,
		},
	)
}

// TestMultiTypeWithSiblings covers a "type" union carrying siblings that apply
// to one alternative each. Collapsing the property to the single type those
// siblings hint at rejected every value of the other; and the branches must not
// inherit each other's constraints — uniqueItems is meaningless for a string,
// and applying it anyway rejects "integer" for using the letter e twice.
func TestMultiTypeWithSiblings(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/multitype_with_siblings.json",
		[]string{
			`{}`,
			`{"type":"integer"}`,
			`{"type":["a","b"]}`,
			`{"type":[]}`,
		},
		[]string{
			`{"type":"ab"}`,
			`{"type":["a","a"]}`,
			`{"type":1}`,
			`{"type":[1]}`,
		},
	)
}

// TestNullEntryInCollection covers a JSON null sitting in a slice or a map
// whose elements are pointers. encoding/json stores a nil without ever calling
// the element type's UnmarshalJSON, so the validation loop used to call a
// value-receiver Validate through that nil and panic. A null is now judged
// against the element schema: rejected where the schema names a type that
// excludes null, passed over where it admits one.
func TestNullEntryInCollection(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/null_entry_in_collection.json",
		[]string{
			`{}`,
			`{"kids":[]}`,
			`{"kids":[{"name":"a"}]}`,
			`{"byName":{"a":{"name":"b"}}}`,
			`{"loose":[null]}`,
		},
		[]string{
			`{"kids":[null]}`,
			`{"byName":{"a":null}}`,
		},
	)
}

// TestOneOfOptionalConstUnmarshal covers a oneOf whose variants share a const
// property that is NOT required. The heuristic discriminator must not fire on
// it (dispatching on a missing optional property would reject valid data);
// unmarshaling falls back to the try-each-variant path keyed on the distinct
// required fields.
func TestOneOfOptionalConstUnmarshal(t *testing.T) {
	schemaPath := "testdata/schemas/regression/oneof_optional_const.json"
	generated := generateFromSchema(t, schemaPath)
	if strings.Contains(string(generated), "oneofDiscriminatorValue") {
		t.Fatalf("optional const property must not become a discriminator")
	}
	rootType := extractRootTypeName(t, string(generated))
	tmpDir := t.TempDir()
	generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}
	writeSharedHelpers(t, tmpDir, generatedMain)
	mainGo := fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	// kind is omitted but each object matches exactly one variant via its
	// required field, so unmarshaling must succeed.
	for _, in := range []string{`+"`"+`{"p":{"x":"hi"}}`+"`"+`, `+"`"+`{"p":{"y":"yo"}}`+"`"+`, `+"`"+`{"p":{"kind":"a","x":"hi"}}`+"`"+`} {
		var v %s
		if err := json.Unmarshal([]byte(in), &v); err != nil {
			fmt.Fprintf(os.Stderr, "unmarshal %%s failed: %%v\n", in, err)
			os.Exit(1)
		}
	}
	fmt.Println("PASS")
}
`, rootType)
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}
	if err := writeTestGoMod(tmpDir, "oneof_optional_const_test"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("oneof optional-const test failed:\n%s\nerror: %v", string(output), err)
	}
	if programOutput(output) != "PASS" {
		t.Fatalf("unexpected output:\n%s", output)
	}
}

// TestOneOfRequiredOnlyObject covers a oneOf whose variants constrain only the
// object's required keys, with no properties anywhere in the schema. Such a
// oneOf is still an object union: the generated type must enforce
// exactly-one-branch rather than accepting everything.
func TestOneOfRequiredOnlyObject(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/oneof_required_only_object.json",
		[]string{
			`{"foo":1,"bar":2}`, // first branch only
			`{"foo":1,"baz":3}`, // second branch only
		},
		[]string{
			`{}`,                        // neither branch
			`{"foo":1}`,                 // neither branch
			`{"foo":1,"bar":2,"baz":3}`, // both branches → 2 matches
		},
	)
}

// TestOneOfObjectVariantConstraints is the end-to-end guard for issue #61: the
// owner's Validate never descended into a oneOf union field, so an object
// variant's *nested* constraints were dead. PR #58 closed the scalar case by
// applying each branch's rules during selection, but selection only decides
// which branch decodes -- {"a":{"x":"z"}} decodes cleanly into the first
// variant and nothing then checked its minLength.
//
// The two invalid documents that carry a branch's required key are the ones
// that matter, and neither can be caught anywhere but Validate: both decode,
// because the value is of the right JSON type for the field it lands in.
//
// The documents carrying *both* required keys are issue #81. Selection gated a
// branch on the presence of its required keys and consulted nothing else, so
// all three counted two matches and were rejected -- including the two the
// schema allows, where the other branch's own constraint fails. Both reference
// implementations were asked: {"x":"z","y":10} and {"x":"zzz","y":9} are valid,
// {"x":"z","y":9} and {"x":"zzz","y":10} are not.
func TestOneOfObjectVariantConstraints(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/oneof_object_variant_constraints.json",
		[]string{
			`{"a":{"x":"zzz"}}`,       // first branch, at its minLength
			`{"a":{"y":10}}`,          // second branch, at its minimum
			`{"a":{"y":42}}`,          // second branch, above it
			`{"a":{"x":"z","y":10}}`,  // both required keys, only the second branch satisfied
			`{"a":{"x":"zzz","y":9}}`, // both required keys, only the first branch satisfied
		},
		[]string{
			`{"a":{"x":"z"}}`,          // first branch selected, minLength 3 violated
			`{"a":{"y":9}}`,            // second branch selected, minimum 10 violated
			`{"a":{}}`,                 // neither branch's required key
			`{"a":{"x":"zzz","y":10}}`, // both branches → 2 matches
			`{"a":{"x":"z","y":9}}`,    // both required keys, neither branch satisfied
		},
	)
}

// TestHandBuiltOneOfVariantValidate is the other half of issue #61: a union
// value that was never unmarshalled escaped checking entirely, because
// selection was the only thing enforcing anything and selection only runs
// during UnmarshalJSON. It also pins the nil guards -- the dispatch must step
// over an empty wrapper rather than call a value-receiver Validate through a
// nil pointer.
func TestHandBuiltOneOfVariantValidate(t *testing.T) {
	mainGo := `package main

import (
	"fmt"
	"os"
)

func main() {
	// Never unmarshalled, so _jsonKeys is nil inside the variant and its
	// required-property check is skipped -- but minLength speaks about the
	// value that is there, and must still be applied.
	bad := OneOfObjectVariantConstraints{
		A: &OneOfObjectVariantConstraints_OneOfObjectVariantConstraintsAOption0{
			OneOfObjectVariantConstraintsAOption0: &OneOfObjectVariantConstraintsAOption0{X: "z"},
		},
	}
	if err := bad.Validate(); err == nil {
		fmt.Fprintln(os.Stderr, "hand-built variant violating minLength should fail Validate but passed")
		os.Exit(1)
	}

	good := OneOfObjectVariantConstraints{
		A: &OneOfObjectVariantConstraints_OneOfObjectVariantConstraintsAOption0{
			OneOfObjectVariantConstraintsAOption0: &OneOfObjectVariantConstraintsAOption0{X: "zzz"},
		},
	}
	if err := good.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "hand-built conforming variant should pass: %v\n", err)
		os.Exit(1)
	}

	// A wrapper holding nothing, and a union field holding nothing. Neither is
	// a value the schema has anything to say about, and neither may panic.
	empty := OneOfObjectVariantConstraints{
		A: &OneOfObjectVariantConstraints_OneOfObjectVariantConstraintsAOption0{},
	}
	if err := empty.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "empty wrapper should be stepped over: %v\n", err)
		os.Exit(1)
	}
	if err := (OneOfObjectVariantConstraints{}).Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "unset union field should be stepped over: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t, "testdata/schemas/regression/oneof_object_variant_constraints.json", "handbuilt_oneof_test", mainGo)
}

// TestOneOfStringLengthVariants covers a constraint-only oneOf attached to a
// declared string type. The branch checks use utf8.RuneCountInString, so the
// generated file must import "unicode/utf8" — it previously did not and failed
// to compile.
func TestOneOfStringLengthVariants(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/oneof_string_length_variants.json",
		[]string{
			`"a"`,      // maxLength branch only
			`"abcdef"`, // minLength branch only
		},
		[]string{
			`"abc"`, // both branches → 2 matches
			`3`,     // not a string
		},
	)
}

// runGeneratedMainProgram compiles the generated types for schemaPath together
// with the supplied main() body and asserts the program prints "PASS".
func runGeneratedMainProgram(t *testing.T, schemaPath, moduleName, mainGo string) {
	t.Helper()
	runGeneratedMainProgramWithConfig(t, schemaPath, moduleName, mainGo, generator.Config{
		PackageName: "testpkg",
		OmitEmpty:   true,
	})
}

// runGeneratedMainProgramWithConfig is the same, for a defect that only appears
// under a non-default generator configuration.
func runGeneratedMainProgramWithConfig(t *testing.T, schemaPath, moduleName, mainGo string, cfg generator.Config) {
	t.Helper()
	generated := generateFromSchemaWithConfig(t, schemaPath, cfg)
	tmpDir := t.TempDir()

	generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}
	writeSharedHelpers(t, tmpDir, generatedMain)
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}
	if err := writeTestGoMod(tmpDir, moduleName); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed:\n%s\nerror: %v", moduleName, string(output), err)
	}
	if outputStr := programOutput(output); outputStr != "PASS" {
		t.Fatalf("%s output:\n%s", moduleName, outputStr)
	}
}

// TestTypedAdditionalPropertiesValidatesItsValues is the behavioural half of
// issue #84. An object whose whole shape is `additionalProperties` came out
// map[string]any, so the value sub-schema was dropped and its keywords were
// enforced nowhere: every document below in `invalid` was accepted by the
// generated Validate.
//
// The type change is what makes the wrong-typed cases fail in the decoder; the
// per-value checks are what make the right-typed but out-of-bounds ones fail in
// Validate. Both are needed, and neither is visible from the IR alone.
func TestTypedAdditionalPropertiesValidatesItsValues(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/typed_additional_properties.json",
		[]string{
			`{"labels":{"a":"abc"}}`,
			`{"labels":{}}`,
			`{"labels":{"a":"abc","b":"defg"},"counts":{"x":5,"y":99}}`,
			`{"labels":{"a":"abc"},"groups":{"g":["ab","cd"]}}`,
		},
		[]string{
			`{"labels":{"a":"ab"}}`,                           // minLength 3 on a map value
			`{"labels":{"a":"abc","b":"x"}}`,                  // the second key is the one that violates
			`{"labels":{"a":"abc"},"counts":{"x":4}}`,         // minimum 5 on a map value
			`{"labels":{"a":"abc"},"groups":{"g":["abcde"]}}`, // maxLength 4 one level under the map
			`{"labels":{"a":1}}`,                              // wrong JSON type for a map value
		},
	)
}

// TestPatternPropertiesObjectValidatesInEveryPosition is the behavioural half of
// issue #96, and of the pattern half of #98. An object whose entire shape is
// `patternProperties` declares no property names, so resolveType's object arm --
// which asked hasProperties -- did not take it and the fallback answered
// map[string]any. The pattern was never matched, the value sub-schema was never
// checked and a sibling `additionalProperties` was dropped with the struct: the
// generated Validate returned nil for every document in `invalid` below.
//
// The schema states one shape and puts it in each position the generator reaches
// an object schema through, because this defect's family is "fixed in one arm,
// not its twin" -- #84, #91, #92 and #97 were four spellings of one keyword's
// gap. The root and $ref positions already worked; they are here so that a
// change which fixes an inline property by breaking them is not mistaken for a
// pass. Each invalid document isolates a single position, so a position that
// regresses names itself.
//
// The valid list carries the controls that matter more than the rejections: a
// key the pattern does not match is unconstrained, an absent property is not a
// violation, an explicit null satisfies ["object","null"], and the integer
// branch of the oneOf is still selectable. A fix that started rejecting these
// would be worse than the bug.
func TestPatternPropertiesObjectValidatesInEveryPosition(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/pattern_properties_positions.json",
		[]string{
			`{}`,
			`{"prop":{"aa":"abc"},"ref":{"aa":"abc"},"inItems":[{"aa":"abc"}],` +
				`"mapValue":{"k":{"aa":"abc"}},"nullable":{"aa":"abc"},"noType":{"aa":"abc"},` +
				`"inAllOf":{"aa":"abc"},"inTuple":[{"aa":"abc"}],"nested":{"inner":{"aa":"abc"}},` +
				`"inOneOf":{"aa":"abc"},"spare":{"aa":"abc"}}`,
			`{"nullable":null}`, // ["object","null"] still admits the null
			`{"inOneOf":5}`,     // the integer branch is still selectable
			`{"prop":{"zz":1}}`, // no pattern matches, so no sub-schema applies
			`{"prop":{}}`,       // an empty object matches nothing and violates nothing
			`{"inItems":[]}`,    // no element, no check
			`{"inTuple":[]}`,    // the tuple position is absent, not empty
		},
		[]string{
			`{"prop":{"aa":"ab"}}`,             // inline property
			`{"ref":{"aa":"ab"}}`,              // behind a $ref to a named definition
			`{"inItems":[{"aa":"ab"}]}`,        // array element
			`{"mapValue":{"k":{"aa":"ab"}}}`,   // map value
			`{"nullable":{"aa":"ab"}}`,         // the nullable spelling
			`{"noType":{"aa":"ab"}}`,           // patternProperties with no declared "type"
			`{"inAllOf":{"aa":"ab"}}`,          // inside an allOf branch
			`{"inTuple":[{"aa":"ab"}]}`,        // a prefixItems position
			`{"nested":{"inner":{"aa":"ab"}}}`, // a property of a nested object
			`{"inOneOf":{"aa":"ab"}}`,          // the object branch of a oneOf union
			`{"spare":{"aa":"ab"}}`,            // the parent's own overflow map
			`{"prop":{"aa":5}}`,                // matching key, wrong JSON type
		},
	)
}

// TestWholeObjectMapValidatesInEveryPosition is the behavioural half of issue
// #97 -- a named definition whose whole shape is `additionalProperties`, whose
// value type survived so a wrong JSON type died in the decoder while `minLength`
// was enforced nowhere -- carried across the same positions as the pattern shape
// above, since the two defects meet in the arms that decide whether an object
// gets a type of its own.
//
// The reproducer #97 states (the named-definition position) already passes on
// the branch this lands on, fixed by the overflow-value descent that went with
// #92. Positions it did not reach are the substance here: a lone allOf branch, a
// tuple slot, a oneOf branch, and an object that states no "type" at all -- that
// last one typed map[string]string correctly and then never checked it, because
// the value schema was looked up with the declared type rather than the inferred
// one the Go type had been chosen by.
func TestWholeObjectMapValidatesInEveryPosition(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/whole_object_map_positions.json",
		[]string{
			`{}`,
			`{"prop":{"k":"ab"},"ref":{"k":"ab"},"inItems":[{"k":"ab"}],` +
				`"mapValue":{"k":{"j":"ab"}},"nullable":{"k":"ab"},"noType":{"k":"ab"},` +
				`"inAllOf":{"k":"ab"},"inTuple":[{"k":"ab"}],"nested":{"inner":{"k":"ab"}},` +
				`"inOneOf":{"k":"ab"},"spare":{"k":"ab"}}`,
			`{"nullable":null}`,
			`{"inOneOf":5}`,
			`{"prop":{}}`,
			`{"inItems":[]}`,
			`{"inTuple":[]}`,
		},
		[]string{
			`{"prop":{"k":"a"}}`,
			`{"ref":{"k":"a"}}`,
			`{"inItems":[{"k":"a"}]}`,
			`{"mapValue":{"k":{"j":"a"}}}`,
			`{"nullable":{"k":"a"}}`,
			`{"noType":{"k":"a"}}`,
			`{"inAllOf":{"k":"a"}}`,
			`{"inTuple":[{"k":"a"}]}`,
			`{"nested":{"inner":{"k":"a"}}}`,
			`{"inOneOf":{"k":"a"}}`,
			`{"spare":{"k":"a"}}`,
		},
	)
}

// TestPatternValueSubschemaIsChecked covers a patternProperties sub-schema that
// says more than a scalar keyword.
//
// A pattern's keys are not known until a document arrives, so the values sit in
// a raw-JSON bucket with no field for the usual Validate dispatch to reach. The
// only thing that checked them was a hand-listed set of in-place rules -- type,
// the numeric bounds, multipleOf, the length bounds, pattern and the item-count
// bounds -- so every other keyword under a pattern was enforced nowhere. Each
// letter below is one of them, and each was accepted before the sub-schema was
// given a type of its own: {"^e":{"$ref":"#/$defs/D"}} generated D with a
// correct Validate and never called it.
//
// The valid list is the larger half on purpose. A key that matches no pattern is
// unconstrained, an empty object satisfies a sub-schema that only forbids
// things, and both branches of the oneOf and of the if/then are still
// selectable. Turning under-enforcement into over-enforcement would be the worse
// trade, so every bucket has an accepted document beside its rejected one.
//
// The last two letters are the scalar keywords the in-place rules still handle,
// pinned here so a change that routes everything through a materialized type is
// seen to leave them working rather than silently rewriting them.
func TestPatternValueSubschemaIsChecked(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/pattern_value_subschemas.json",
		[]string{
			`{}`,
			`{"zz":"anything","zq":{"whatever":true}}`, // no pattern matches
			`{"aa":"aaa"}`, `{"bb":"aaa"}`,
			`{"cc":{"x":1}}`, `{"cc":{"x":1,"y":2}}`,
			`{"dd":{"x":"abcde"}}`, `{"dd":{}}`, `{"dd":{"y":1}}`,
			`{"ee":7}`,
			`{"ff":["abcde"]}`, `{"ff":[]}`,
			`{"gg":[1,2]}`, `{"gg":[]}`,
			`{"hh":5}`, `{"hh":true}`,
			`{"ii":"abcde"}`,
			`{"jj":{"x":1}}`, `{"jj":{}}`,
			`{"kk":{"zz":"abcde"}}`, `{"kk":{"qq":1}}`,
			`{"ll":{"k":"abcde"}}`, `{"ll":{}}`,
			`{"mm":"x"}`, `{"mm":5}`,
			`{"nn":"abcde"}`, `{"nn":1}`,
			`{"oo":{"zx":1}}`, `{"oo":{}}`,
			`{"pp":{"x":1,"y":2}}`, `{"pp":{"y":2}}`, `{"pp":{}}`,
			`{"qq":{}}`,
			`{"rr":1}`, `{"rr":1.0}`, // draft 2020-12: a zero fractional part is an integer
			`{"ss":"abcde"}`,
			// The two sub-schemas whose materialized type Go forbids methods on:
			// a $ref to an empty schema, and a bare `format` with no type. Both
			// assert nothing, so both must accept anything -- and both are here
			// because the emitted dispatch would not compile if the pass that
			// notices a type carries no Validate stopped noticing.
			`{"tt":{"anything":1}}`, `{"tt":null}`,
			`{"uu":"not-an-ip"}`, `{"uu":5}`,
		},
		[]string{
			`{"aa":"ccc"}`,         // enum
			`{"bb":"bbb"}`,         // const
			`{"cc":{}}`,            // required
			`{"dd":{"x":"abc"}}`,   // a nested property's own constraint
			`{"ee":1}`,             // a $ref target's constraint
			`{"ff":["abc"]}`,       // an items sub-schema's constraint
			`{"gg":[1,1]}`,         // uniqueItems
			`{"hh":"x"}`,           // not
			`{"ii":"abc"}`,         // an allOf branch's constraint
			`{"jj":{"x":1,"y":2}}`, // maxProperties
			`{"kk":{"zz":"abc"}}`,  // a patternProperties nested under a pattern
			`{"ll":{"k":"abc"}}`,   // an additionalProperties nested under a pattern
			`{"mm":true}`,          // oneOf: no branch matches
			`{"nn":"abc"}`,         // if/then
			`{"oo":{"qq":1}}`,      // propertyNames
			`{"pp":{"x":1}}`,       // dependentRequired
			`{"qq":{"x":1}}`,       // additionalProperties:false under a pattern
			`{"rr":1.5}`,           // still not an integer in any draft
			`{"ss":"abc"}`,         // minLength, still on the in-place path
		},
	)
}

// TestPatternValueIntegerDraft4ReadsTheToken is the other half of the integer
// case above. Draft 4 decides `integer` on the token, so 1.0 is a number and not
// an integer; from draft 6 the value decides and 1.0 is one. The in-place check
// scanned the raw bytes for a '.' in every draft, which is right for 4 and wrong
// for the rest -- {"rr":1.0} was rejected under 2020-12 against a schema that
// permits it, a false rejection. Fixing that must not turn draft 4 lenient,
// which is what this pins.
func TestPatternValueIntegerDraft4ReadsTheToken(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/pattern_value_integer_draft4.json",
		[]string{`{"rr":1}`, `{"zz":1.0}`, `{}`},
		[]string{`{"rr":1.0}`, `{"rr":1.5}`, `{"rr":"x"}`},
	)
}

// TestPatternValueDescentComposes checks that the descent nests rather than
// working one level down. Each property puts a pattern bucket behind a different
// container -- an array element, a map value, a tuple slot, and a pattern whose
// value is an array of objects carrying a pattern of their own -- and the
// innermost constraint is a $ref or a required property, neither of which the
// in-place rules can express at any depth.
func TestPatternValueDescentComposes(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/pattern_value_nesting.json",
		[]string{
			`{}`,
			`{"rows":[{"bb":7}]}`, `{"rows":[]}`, `{"rows":[{"zz":1}]}`,
			`{"byKey":{"k":{"bb":{"x":"abcde"}}}}`, `{"byKey":{}}`, `{"byKey":{"k":{}}}`,
			`{"tuple":[{"bb":7}]}`, `{"tuple":[]}`,
			`{"deep":{"bb":[{"cc":7}]}}`, `{"deep":{"bb":[]}}`, `{"deep":{}}`,
		},
		[]string{
			`{"rows":[{"bb":1}]}`,
			`{"rows":[{"bb":7},{"bb":2}]}`, // the second element is the one that violates
			`{"byKey":{"k":{"bb":{"x":"abc"}}}}`,
			`{"byKey":{"k":{"bb":{}}}}`,
			`{"tuple":[{"bb":1}]}`,
			`{"deep":{"bb":[{"cc":1}]}}`,
		},
	)
}

// TestEmptyObjectForbidsEveryKey covers {"type":"object","additionalProperties":
// false} with no properties declared. It permits no key at all, so only {}
// satisfies it -- and a $defs entry of that shape has always rejected {"x":1}
// through the Forbidden overflow map, while every position that went through
// resolveType collapsed to map[string]any and accepted it. The answer depended
// on where the schema was written, which is the defect this pins closed in each
// position the object can occupy.
//
// `permissive` is the control the fix must not touch: `additionalProperties:
// true` permits every key and constrains none, so it stays a bare map and
// accepts what it always did.
func TestEmptyObjectForbidsEveryKey(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/empty_object_positions.json",
		[]string{
			`{}`,
			`{"prop":{},"ref":{},"inItems":[{}],"mapValue":{"k":{}},"nullable":{},` +
				`"noType":{},"inAllOf":{},"inTuple":[{}],"nested":{"inner":{}},` +
				`"inOneOf":{},"inPattern":{"bb":{}}}`,
			`{"nullable":null}`,
			`{"inOneOf":5}`,
			`{"inItems":[]}`, `{"inTuple":[]}`, `{"mapValue":{}}`,
			`{"inPattern":{"zz":{"anything":1}}}`, // no pattern matches, nothing applies
			`{"permissive":{"x":1,"y":2}}`,        // additionalProperties:true is untouched
		},
		[]string{
			`{"prop":{"x":1}}`,
			`{"ref":{"x":1}}`,
			`{"inItems":[{"x":1}]}`,
			`{"mapValue":{"k":{"x":1}}}`,
			`{"nullable":{"x":1}}`,
			`{"noType":{"x":1}}`,
			`{"inAllOf":{"x":1}}`,
			`{"inTuple":[{"x":1}]}`,
			`{"nested":{"inner":{"x":1}}}`,
			`{"inOneOf":{"x":1}}`,
			`{"inPattern":{"bb":{"x":1}}}`,
		},
	)
}

// TestBigIntNullableDefinitionAcceptsNull is the behavioural half of issue #85.
// A named ["integer","null"] reaches the big-integer wrapper, which held an
// int64 and a *big.Int and had no state for null: `{"n":null}` was rejected as
// "value  is not a valid integer" against a schema that permits it.
//
// This is a template-level fix, so nothing in the IR proves it. The program
// below asserts what the repair must hold together: null decodes, round-trips
// and validates; a literal 0 is still not a null in either direction; a numeric
// keyword beside the null is not applied to it, since under JSON Schema a
// keyword about numbers is satisfied vacuously by every other instance type; a
// plain integer is unaffected; and the arbitrary precision the flag was asked
// for still survives in the nullable position -- which is what the alternative
// fix, declining ["integer","null"] at the arm and resolving it to *int64, would
// have cost.
func TestBigIntNullableDefinitionAcceptsNull(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func decode(doc string) BigIntNullableDefinition {
	var v BigIntNullableDefinition
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		fail("decoding %s: %v", doc, err)
	}
	if err := v.Validate(); err != nil {
		fail("validating %s: %v", doc, err)
	}
	return v
}

func roundTrip(doc string) string {
	out, err := json.Marshal(decode(doc))
	if err != nil {
		fail("marshalling %s: %v", doc, err)
	}
	return string(out)
}

func main() {
	// The null the schema permits: accepted, distinguishable from a literal 0,
	// and written back as null rather than as the wrapper's zero. "b" carries a
	// minimum beside the null, which the null does not have to satisfy -- its
	// accessors answer 0, and applying the bound to that zero would reject a
	// value the schema allows.
	nullDoc := ` + "`" + `{"n":null,"b":null,"p":7}` + "`" + `
	if v := decode(nullDoc); !v.N.IsNull() || !v.B.IsNull() {
		fail("%s: N.IsNull()=%v B.IsNull()=%v", nullDoc, v.N.IsNull(), v.B.IsNull())
	}
	// The overflow-map marshal path re-serializes through a map, so the output
	// is key-sorted rather than in document order.
	if got := roundTrip(nullDoc); got != ` + "`" + `{"b":null,"n":null,"p":7}` + "`" + ` {
		fail("%s round-tripped to %s", nullDoc, got)
	}

	// The bound still bites on a value that is a number.
	var underMin BigIntNullableDefinition
	if err := json.Unmarshal([]byte(` + "`" + `{"n":1,"b":4,"p":7}` + "`" + `), &underMin); err != nil {
		fail("decoding an under-minimum b: %v", err)
	}
	if err := underMin.Validate(); err == nil {
		fail("b=4 passed a minimum of 5; the null branch must not skip the check for numbers")
	}

	// A literal zero is not a null, and must not be written back as one.
	zeroDoc := ` + "`" + `{"n":0,"b":5,"p":7}` + "`" + `
	if v := decode(zeroDoc); v.N.IsNull() {
		fail("%s: N.IsNull() = true; the int64 zero is not the null state", zeroDoc)
	}
	if got := roundTrip(zeroDoc); got != ` + "`" + `{"b":5,"n":0,"p":7}` + "`" + ` {
		fail("%s round-tripped to %s", zeroDoc, got)
	}

	// Arbitrary precision still holds in the nullable position. Resolving the
	// definition to *int64 instead would lose this value.
	bigDoc := ` + "`" + `{"n":123456789012345678901234567890,"b":null,"p":7}` + "`" + `
	v := decode(bigDoc)
	if !v.N.IsBigInt() || v.N.IsNull() {
		fail("%s: IsBigInt=%v IsNull=%v", bigDoc, v.N.IsBigInt(), v.N.IsNull())
	}
	if got := roundTrip(bigDoc); got != ` + "`" + `{"b":null,"n":123456789012345678901234567890,"p":7}` + "`" + ` {
		fail("%s round-tripped to %s", bigDoc, got)
	}

	// The non-nullable definition is untouched: a null is still not an integer.
	var rejected BigIntNullableDefinition
	if err := json.Unmarshal([]byte(` + "`" + `{"n":1,"b":null,"p":null}` + "`" + `), &rejected); err == nil {
		fail("p accepted a null; its schema lists only \"integer\"")
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgramWithConfig(t,
		"testdata/schemas/regression/bigint_nullable_definition.json",
		"bigint_nullable_test",
		mainGo,
		generator.Config{PackageName: "testpkg", OmitEmpty: true, BigIntSupport: true},
	)
}

// TestStructReuseResetsState is a regression guard for C5: reusing a value
// across json.Unmarshal calls must not resurrect stale synthesized state
// (overflow maps, the sticky non-object flag, or presence tracking) from a
// previous document.
func TestStructReuseResetsState(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	// Scenario 1: overflow (additionalProperties) map must not leak across
	// decodes. "stale" lands in the overflow map on the first decode; it must
	// be gone after the second decode of an object without it.
	var r StructReuse
	if err := json.Unmarshal([]byte(` + "`" + `{"a":1,"stale":true}` + "`" + `), &r); err != nil {
		fmt.Fprintf(os.Stderr, "first unmarshal: %v\n", err)
		os.Exit(1)
	}
	if err := json.Unmarshal([]byte(` + "`" + `{"a":2}` + "`" + `), &r); err != nil {
		fmt.Fprintf(os.Stderr, "second unmarshal: %v\n", err)
		os.Exit(1)
	}
	out, err := json.Marshal(r)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(1)
	}
	if string(out) != ` + "`" + `{"a":2}` + "`" + ` {
		fmt.Fprintf(os.Stderr, "overflow reuse: got %s, want {\"a\":2}\n", string(out))
		os.Exit(1)
	}

	// Scenario 2: the sticky non-object flag must not discard a subsequent
	// object document. Decode a bare number, then an object, into the same var.
	var n StructReuse
	if err := json.Unmarshal([]byte(` + "`" + `42` + "`" + `), &n); err != nil {
		fmt.Fprintf(os.Stderr, "nonobject unmarshal: %v\n", err)
		os.Exit(1)
	}
	if err := json.Unmarshal([]byte(` + "`" + `{"a":3}` + "`" + `), &n); err != nil {
		fmt.Fprintf(os.Stderr, "object-after-nonobject unmarshal: %v\n", err)
		os.Exit(1)
	}
	out2, err := json.Marshal(n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal2: %v\n", err)
		os.Exit(1)
	}
	if !strings.Contains(string(out2), ` + "`" + `"a":3` + "`" + `) || string(out2) == "42" {
		fmt.Fprintf(os.Stderr, "nonobject reuse: got %s, want an object containing \"a\":3\n", string(out2))
		os.Exit(1)
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t, "testdata/schemas/regression/struct_reuse.json", "struct_reuse_test", mainGo)
}

// TestHandBuiltAnyOfValidate is a regression guard for C6: object-level anyOf
// branch matching depends on JSON key presence, so a hand-constructed value
// (nil _jsonKeys) must skip the check, while a value built by json.Unmarshal
// must still enforce it.
func TestHandBuiltAnyOfValidate(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	// Hand-constructed value with a valid field set: presence is untracked
	// (_jsonKeys is nil), so the anyOf branch check is skipped and Validate
	// must pass.
	s := "x"
	built := AnyOfRequiredBranches{A: &s}
	if err := built.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "hand-built Validate should pass: %v\n", err)
		os.Exit(1)
	}

	// Value built from JSON that matches no anyOf branch: presence IS tracked,
	// so the check must still hard-fail (from-JSON path keeps enforcing).
	var fromJSON AnyOfRequiredBranches
	if err := json.Unmarshal([]byte(` + "`" + `{}` + "`" + `), &fromJSON); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal {}: %v\n", err)
		os.Exit(1)
	}
	if err := fromJSON.Validate(); err == nil {
		fmt.Fprintf(os.Stderr, "unmarshaled {} should fail Validate but passed\n")
		os.Exit(1)
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t, "testdata/schemas/regression/anyof_required_branches.json", "handbuilt_anyof_test", mainGo)
}

// TestUntypedOneOfBranches covers a schema that constrains values purely through
// oneOf while declaring no type of its own. It used to generate `type X any`,
// which Go forbids methods on, so every branch was silently dropped and any
// value was accepted. The expectations are the JSON Schema Test Suite's own for
// this schema (draft2020-12/oneOf.json, first group).
func TestUntypedOneOfBranches(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/untyped_oneof_branches.json",
		[]string{
			`1`,   // integer branch only
			`2.5`, // minimum branch only (>=2 but not an integer)
		},
		[]string{
			`3`,   // both branches match
			`1.5`, // neither branch matches
		},
	)
}

// TestUntypedIfThen covers the same class for if/then. Note the vacuous-success
// rule: a non-number satisfies both the if and the then, because numeric
// keywords do not constrain values of other types.
func TestUntypedIfThen(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/untyped_if_then.json",
		[]string{
			`-5`,      // if matches (<0) and then holds (>=-10)
			`3`,       // if does not match, no else to satisfy
			`"hello"`, // numeric keywords do not apply to strings
		},
		[]string{
			`-100`, // if matches (<0) but then fails (< -10)
		},
	)
}

// TestUnevaluatedItemsWithAnyOf covers unevaluatedItems whose evaluated set
// depends on which anyOf branches match the value being validated — the case
// static analysis cannot decide. Expectations are the JSON Schema Test Suite's
// own (draft2020-12/unevaluatedItems.json).
func TestUnevaluatedItemsWithAnyOf(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/unevaluated_items_anyof.json",
		[]string{
			`["foo","bar"]`,       // one branch matches, nothing unevaluated
			`["foo","bar","baz"]`, // both branches match, nothing unevaluated
		},
		[]string{
			`["foo","bar",42]`,       // index 2 unevaluated
			`["foo","bar","baz",42]`, // index 3 unevaluated
		},
	)
}

// TestUnevaluatedItemsCousins covers annotation scope: the unevaluatedItems in
// the second allOf branch is a cousin of the first and cannot see its
// annotations, so any non-empty array fails.
func TestUnevaluatedItemsCousins(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/unevaluated_items_cousins.json",
		[]string{`[]`},
		[]string{`[1]`, `["anything"]`},
	)
}

// TestIntegerFloatNotationAcceptedEverywhere is the behavioural half of issue
// #90. From draft 6 on, a number with a zero fractional part is an integer, so
// every document in the valid list is one the schema admits and one that
// python-jsonschema and js-ajv both accept.
//
// The list is a position sweep on purpose. A root alias already accepted 1.0
// while a struct field refused it, and the defect is that disagreement rather
// than any one position, so every place an integer can sit has to answer the
// same way: a required field, an optional one, a slice element at two depths, a
// map value, a nullable, the three named shapes, an enum member, a oneOf branch,
// a nested struct and a field carrying multipleOf.
//
// runValidationCases treats an unmarshal failure on a valid document as a
// failure of the test, which is exactly the defect's signature -- the document
// was refused before any constraint was consulted.
func TestIntegerFloatNotationAcceptedEverywhere(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/integer_float_notation.json",
		[]string{
			`{"req":1.0}`,
			`{"req":1e2}`,
			`{"req":-0.0}`,
			`{"req":1,"opt":2.0}`,
			`{"req":1,"arr":[1.0,2,3.0]}`,
			`{"req":1,"grid":[[1.0],[2.0,3]]}`,
			`{"req":1,"mp":{"k":1.0,"j":2}}`,
			`{"req":1,"nullint":4.0}`,
			`{"req":1,"nullint":null}`,
			`{"req":1,"named":5.0}`,
			`{"req":1,"namedarr":[6.0]}`,
			`{"req":1,"namedmap":{"k":7.0}}`,
			`{"req":1,"enm":2.0}`,
			`{"req":1,"un":12.0}`,
			`{"req":1,"nest":{"deep":8.0}}`,
			`{"req":1,"mult":4.0}`,
			// The plain spelling keeps working at each of them.
			`{"req":1,"arr":[1],"mp":{"k":2},"enm":3,"un":11,"nest":{"deep":4},"mult":6}`,
		},
		[]string{
			// A fraction that is not zero is not an integer in any draft.
			`{"req":1.5}`,
			`{"req":1,"arr":[1.5]}`,
			`{"req":1,"mp":{"k":2.5}}`,
			`{"req":1,"named":3.5}`,
			`{"req":1,"namedarr":[4.5]}`,
			`{"req":1,"namedmap":{"k":5.5}}`,
			`{"req":1,"nest":{"deep":6.5}}`,
			// A JSON string is not an integer however it reads.
			`{"req":"1"}`,
			`{"req":1,"arr":["2"]}`,
			// The constraints still apply to a value written in float notation.
			`{"req":1,"mult":3.0}`,
			`{"req":1,"enm":4.0}`,
			`{"req":1,"un":9.0}`,
		},
	)
}

// TestIntegerStrictTokenUnderDraft4 is the same sweep in the draft that reads
// "integer" the other way.
//
// Draft 4 defines an integer as a number with no fraction and no exponent, so
// 1.0 is not one -- the suite says so in draft4/optional/zeroTerminatedFloats.
// Everything in the invalid list must therefore stay refused, and this test is
// what stops the draft-6 repair from being applied where it is wrong. The valid
// list is the same documents written the one way draft 4 admits.
func TestIntegerStrictTokenUnderDraft4(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/integer_strict_token_draft4.json",
		[]string{
			`{"req":1}`,
			`{"req":1,"arr":[1,2]}`,
			`{"req":1,"mp":{"k":3}}`,
			`{"req":1,"named":4}`,
			`{"req":1,"enm":2}`,
		},
		[]string{
			`{"req":1.0}`,
			`{"req":1,"arr":[1.0]}`,
			`{"req":1,"mp":{"k":1.0}}`,
			`{"req":1,"named":1.0}`,
			`{"req":1,"enm":1.0}`,
			`{"req":1e2}`,
		},
	)
}

// TestNullableTypedAdditionalPropertiesValidatesItsValues is the behavioural
// half of issue #91: a ["object","null"] whose whole shape is
// additionalProperties came out *map[string]any, so neither the value type nor
// its keywords survived and every document in the invalid list was accepted.
//
// The nulls are here as well as the violations. The fix drops the pointer the
// property used to carry, on the precedent of the nullable array beside it, and
// a nil map has to keep meaning "null" in both directions for that to be sound.
func TestNullableTypedAdditionalPropertiesValidatesItsValues(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/nullable_typed_additional_properties.json",
		[]string{
			`{}`,
			`{"labels":null}`,
			`{"labels":{}}`,
			`{"labels":{"a":"abc"}}`,
			`{"labels":{"a":"abc","b":"defg"},"counts":{"x":5,"y":99}}`,
			`{"counts":null,"groups":null,"nested":null}`,
			`{"groups":{"g":["ab","cd"]}}`,
			`{"nested":{"outer":{"inner":9}}}`,
			`{"nested":{"outer":null}}`,
		},
		[]string{
			`{"labels":{"a":"ab"}}`,
			`{"labels":{"a":"abc","b":"x"}}`,
			`{"counts":{"x":4}}`,
			`{"groups":{"g":["abcde"]}}`,
			`{"nested":{"outer":{"inner":10}}}`,
			`{"labels":{"a":1}}`,
		},
	)
}

// TestFormatAliasDecodesItsOwnValue is issue #99. `format: date-time` behind a
// $ref becomes `type Stamp time.Time`, and a defined type inherits none of the
// methods of the type it is defined over: encoding/json fell through to
// time.Time's unexported fields, so an ordinary RFC 3339 string would not decode
// into Stamp at all, and a Stamp marshalled back out as `{}`. `format: ipv4`
// has the same shape over netip.Addr, whose representation is reached through
// encoding.TextUnmarshaler rather than json.Unmarshaler.
//
// This is deliberately not left to the golden file. The defect was pinned by
// one for as long as it existed: the golden records what the generator emits,
// agreed with the broken emission, and nothing anywhere compiled that alias and
// handed it a date. Only running the code can tell the two apart, so the
// program below decodes a real document through every position a named format
// alias reaches, checks the instants that came out rather than merely that
// something did, writes it back and compares byte for byte.
//
// The rejections at the end are the other half. A fix that made Stamp accept
// anything -- decoding through a string, say -- would pass every assertion
// above while accepting "not-a-timestamp" as a date-time.
func TestFormatAliasDecodesItsOwnValue(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"time"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// Every property is present, so the round trip is exact.
const doc = ` + "`" + `{"addr":"192.0.2.7","addr_list":["192.0.2.8"],"chained_stamp":"2020-01-02T03:04:05Z","optional_stamp":"2020-01-02T03:04:05Z","required_stamp":"2020-01-02T03:04:05Z","stamp_grid":[["2020-01-02T03:04:05Z"]],"stamp_list":["2020-01-02T03:04:05Z"],"stamp_map":{"k":"2020-01-02T03:04:05Z"},"tuple":["2020-01-02T03:04:05Z","192.0.2.9"]}` + "`" + `

func main() {
	want := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

	var v FormatAliasPositions
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		fail("decoding a plain RFC 3339 document: %v", err)
	}
	if err := v.Validate(); err != nil {
		fail("validating a plain RFC 3339 document: %v", err)
	}

	// The instant itself, position by position. A Stamp that decoded into
	// time.Time's zero would satisfy "no error" and nothing else.
	for _, c := range []struct {
		where string
		got   time.Time
	}{
		{"required_stamp", time.Time(v.RequiredStamp)},
		{"optional_stamp", time.Time(*v.OptionalStamp)},
		{"chained_stamp", time.Time(*v.ChainedStamp)},
		{"stamp_list[0]", time.Time(v.StampList[0])},
		{"stamp_map[k]", time.Time(v.StampMap["k"])},
		{"stamp_grid[0][0]", time.Time(v.StampGrid[0][0])},
	} {
		if !c.got.Equal(want) {
			fail("%s decoded to %v, want %v", c.where, c.got, want)
		}
	}
	if got := netip.Addr(*v.Addr).String(); got != "192.0.2.7" {
		fail("addr decoded to %q, want 192.0.2.7", got)
	}
	if got := netip.Addr(v.AddrList[0]).String(); got != "192.0.2.8" {
		fail("addr_list[0] decoded to %q, want 192.0.2.8", got)
	}

	// And back out again. Without a MarshalJSON of its own a Stamp writes
	// time.Time's unexported fields, which come out as {}.
	out, err := json.Marshal(v)
	if err != nil {
		fail("marshalling: %v", err)
	}
	if string(out) != doc {
		fail("round trip changed the document\n  in:  %s\n  out: %s", doc, string(out))
	}

	// A document carrying only what it must. Every optional property is absent,
	// and none of them may appear in the output: omitempty never omits a struct,
	// so before the pointer these came back as "0001-01-01T00:00:00Z" and "" --
	// values the document never held. The pointer is also what keeps the other
	// direction honest, which the zero-instant case below measures.
	minimal := ` + "`" + `{"required_stamp":"2020-01-02T03:04:05Z"}` + "`" + `
	var mv FormatAliasPositions
	if err := json.Unmarshal([]byte(minimal), &mv); err != nil {
		fail("decoding %s: %v", minimal, err)
	}
	if err := mv.Validate(); err != nil {
		fail("validating %s: %v", minimal, err)
	}
	mOut, err := json.Marshal(mv)
	if err != nil {
		fail("marshalling the minimal document: %v", err)
	}
	if string(mOut) != minimal {
		fail("an absent optional property was invented into the output\n  in:  %s\n  out: %s", minimal, string(mOut))
	}

	// The zero instant is a legitimate value, not an absence. ",omitzero" would
	// have omitted it -- that is the reason the pointer was chosen over it --
	// so a document that carries it must get it back.
	zeroInstant := ` + "`" + `{"optional_stamp":"0001-01-01T00:00:00Z","required_stamp":"2020-01-02T03:04:05Z"}` + "`" + `
	var zv FormatAliasPositions
	if err := json.Unmarshal([]byte(zeroInstant), &zv); err != nil {
		fail("decoding %s: %v", zeroInstant, err)
	}
	if zv.OptionalStamp == nil {
		fail("%s: optional_stamp is nil; a present zero instant is not an absence", zeroInstant)
	}
	zOut, err := json.Marshal(zv)
	if err != nil {
		fail("marshalling the zero-instant document: %v", err)
	}
	if string(zOut) != zeroInstant {
		fail("a present zero instant was dropped from the output\n  in:  %s\n  out: %s", zeroInstant, string(zOut))
	}

	// A malformed value must still be refused. The whole point is that the
	// alias carries the format's own decoder, not that it accepts more.
	for _, bad := range []string{
		` + "`" + `{"required_stamp":"not-a-timestamp"}` + "`" + `,
		` + "`" + `{"required_stamp":"2020-13-45T99:99:99Z"}` + "`" + `,
		` + "`" + `{"required_stamp":"2020-01-02"}` + "`" + `,
		` + "`" + `{"required_stamp":"2020-01-02T03:04:05Z","chained_stamp":"nope"}` + "`" + `,
		` + "`" + `{"required_stamp":"2020-01-02T03:04:05Z","stamp_list":["nope"]}` + "`" + `,
		` + "`" + `{"required_stamp":"2020-01-02T03:04:05Z","stamp_map":{"k":"nope"}}` + "`" + `,
		` + "`" + `{"required_stamp":"2020-01-02T03:04:05Z","stamp_grid":[["nope"]]}` + "`" + `,
		` + "`" + `{"required_stamp":"2020-01-02T03:04:05Z","addr":"999.999.999.999"}` + "`" + `,
		` + "`" + `{"required_stamp":"2020-01-02T03:04:05Z","addr_list":["not-an-address"]}` + "`" + `,
	} {
		var bv FormatAliasPositions
		if err := json.Unmarshal([]byte(bad), &bv); err == nil {
			fail("accepted a malformed value: %s", bad)
		}
	}

	// A tuple position is checked by re-decoding the element through the
	// position's type, so the same defect reached it from Validate.
	var tv FormatAliasPositions
	badTuple := ` + "`" + `{"required_stamp":"2020-01-02T03:04:05Z","tuple":["nope","192.0.2.9"]}` + "`" + `
	if err := json.Unmarshal([]byte(badTuple), &tv); err != nil {
		fail("decoding %s: %v", badTuple, err)
	}
	if err := tv.Validate(); err == nil {
		fail("tuple position 0 accepted %q as a date-time", "nope")
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t,
		"testdata/schemas/regression/format_alias_positions.json",
		"format_alias_positions_test",
		mainGo,
	)
}

// TestFormatAliasRootDecodesItsOwnValue is the root position of the same
// defect: a whole document that is a date-time makes the alias over time.Time
// the root type, where there is no enclosing struct whose decoder could have
// covered for it.
func TestFormatAliasRootDecodesItsOwnValue(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func main() {
	const doc = ` + "`" + `"2020-01-02T03:04:05Z"` + "`" + `
	var v FormatAliasRoot
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		fmt.Fprintf(os.Stderr, "decoding %s: %v\n", doc, err)
		os.Exit(1)
	}
	if got, want := time.Time(v), time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC); !got.Equal(want) {
		fmt.Fprintf(os.Stderr, "decoded to %v, want %v\n", got, want)
		os.Exit(1)
	}
	out, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshalling: %v\n", err)
		os.Exit(1)
	}
	if string(out) != doc {
		fmt.Fprintf(os.Stderr, "round trip: %s -> %s\n", doc, string(out))
		os.Exit(1)
	}
	var bad FormatAliasRoot
	if err := json.Unmarshal([]byte(` + "`" + `"not-a-timestamp"` + "`" + `), &bad); err == nil {
		fmt.Fprintln(os.Stderr, "accepted a malformed date-time")
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t,
		"testdata/schemas/regression/format_alias_root.json",
		"format_alias_root_test",
		mainGo,
	)
}

// TestFormatMapValuesDecode covers the one position where a format that maps to
// a distinct Go type is named nowhere but the overflow map. The import scan
// walked the declared fields only, so the file named time.Time without
// importing time and did not compile at all -- which running it is what catches,
// since a golden records source and never builds it.
func TestFormatMapValuesDecode(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func main() {
	const doc = ` + "`" + `{"a":"2020-01-02T03:04:05Z","b":"2021-06-07T08:09:10Z"}` + "`" + `
	var v FormatMapValues
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		fmt.Fprintf(os.Stderr, "decoding %s: %v\n", doc, err)
		os.Exit(1)
	}
	if got, want := v.AdditionalProperties["a"], time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC); !got.Equal(want) {
		fmt.Fprintf(os.Stderr, "a decoded to %v, want %v\n", got, want)
		os.Exit(1)
	}
	out, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshalling: %v\n", err)
		os.Exit(1)
	}
	if string(out) != doc {
		fmt.Fprintf(os.Stderr, "round trip: %s -> %s\n", doc, string(out))
		os.Exit(1)
	}
	var bad FormatMapValues
	if err := json.Unmarshal([]byte(` + "`" + `{"a":"not-a-timestamp"}` + "`" + `), &bad); err == nil {
		fmt.Fprintln(os.Stderr, "accepted a malformed date-time as a map value")
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t,
		"testdata/schemas/regression/format_map_values.json",
		"format_map_values_test",
		mainGo,
	)
}

// TestEnumAliasBorrowsEnumMethods is the same defect as #99 reached through a
// generated type rather than a stdlib one. Two enum shapes carry JSON methods
// of their own -- a heterogeneous enum is a json.RawMessage that keeps the bytes
// it was handed, and an int64 enum whose draft admits 1.0 reads the number
// through jsonInteger -- and `{"$ref": "#/$defs/TheEnum"}` makes an alias over
// the enum, which inherits neither.
//
// So `type RawAlias RawEnum` was a []byte to encoding/json: the member "a"
// arrived as base64 and was refused, and a value that did decode came back out
// base64-encoded. `type IntAlias IntEnum` refused the 1.0 spelling that the
// enum's own UnmarshalJSON exists to accept -- issue #90's defect, reappearing
// one $ref further along.
func TestEnumAliasBorrowsEnumMethods(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func main() {
	// Every member of the heterogeneous enum, in the alias position and as an
	// element of a slice of it. A string member is what shows the base64
	// decode; a number and a null are what show it is the bytes being kept and
	// not a string type standing in for them.
	//
	// The properties are written in key order because the overflow-map marshal
	// path re-serializes through a map, which sorts them.
	for _, doc := range []string{
		` + "`" + `{"num":1,"raw":"a","raw_list":["a",1,null]}` + "`" + `,
		` + "`" + `{"num":2,"raw":1,"raw_list":[]}` + "`" + `,
		` + "`" + `{"num":3,"raw":null}` + "`" + `,
	} {
		var v EnumAliasDelegation
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			fail("decoding %s: %v", doc, err)
		}
		if err := v.Validate(); err != nil {
			fail("validating %s: %v", doc, err)
		}
		out, err := json.Marshal(v)
		if err != nil {
			fail("marshalling %s: %v", doc, err)
		}
		if string(out) != doc {
			fail("round trip changed the document\n  in:  %s\n  out: %s", doc, string(out))
		}
	}

	// 1.0 is the integer 1 from draft 6 on, so it names the same member. The
	// enum's own decoder accepts it; the alias has to reach that decoder.
	oneFloat := ` + "`" + `{"raw":"a","num":1.0}` + "`" + `
	var fv EnumAliasDelegation
	if err := json.Unmarshal([]byte(oneFloat), &fv); err != nil {
		fail("decoding %s: %v", oneFloat, err)
	}
	if err := fv.Validate(); err != nil {
		fail("validating %s: %v", oneFloat, err)
	}
	if fv.Num != 1 {
		fail("%s decoded num to %v, want 1", oneFloat, fv.Num)
	}

	// Borrowing the decoder must not cost the enum check. A value outside the
	// enum decodes -- both forms accept any JSON of the right shape -- and is
	// refused by Validate.
	for _, bad := range []string{
		` + "`" + `{"raw":"zzz","num":1}` + "`" + `,
		` + "`" + `{"raw":"a","num":9}` + "`" + `,
		` + "`" + `{"raw":"a","num":1,"raw_list":["zzz"]}` + "`" + `,
	} {
		var bv EnumAliasDelegation
		if err := json.Unmarshal([]byte(bad), &bv); err != nil {
			continue // an unmarshal-time rejection is an acceptable failure mode
		}
		if err := bv.Validate(); err == nil {
			fail("accepted a value outside the enum: %s", bad)
		}
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t,
		"testdata/schemas/regression/enum_alias_delegation.json",
		"enum_alias_delegation_test",
		mainGo,
	)
}

// TestFormatAliasAssertsItsFormat covers a `format` that reached a named type.
// The rule was collected onto the alias and the alias template had no arm to
// emit it, so every format assertion behind a $ref was enforced nowhere: `type
// V4 netip.Addr` and `type Email string` both validated to `return nil` while
// the identical subschema written inline as a property was checked. An IPv6
// address satisfied an ipv4 definition, and "not-an-email" satisfied an email
// one.
//
// runValidationCases is the right harness here: the valid half must still pass
// (the risk in adding an assertion is rejecting what the schema allows) and the
// invalid half is what the missing check let through. The list covers the alias
// itself, an element of a slice of it and a value of a map of it, since the
// element positions reach the same Validate by a different route.
func TestFormatAliasAssertsItsFormat(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/format_alias_assertions.json",
		[]string{
			`{}`,
			`{"v4":"192.0.2.7"}`,
			`{"v6":"2001:db8::1"}`,
			`{"email":"a@b.test"}`,
			`{"uuid":"123e4567-e89b-12d3-a456-426614174000"}`,
			`{"day":"2020-01-02"}`,
			`{"site":"https://example.test/x"}`,
			`{"v4_list":["192.0.2.7","192.0.2.8"]}`,
			`{"email_map":{"k":"a@b.test"}}`,
		},
		[]string{
			`{"v4":"2001:db8::1"}`,              // an IPv6 address against an ipv4 definition
			`{"v6":"192.0.2.7"}`,                // and the reverse
			`{"email":"not-an-email"}`,          //
			`{"uuid":"123e4567-e89b-12d3"}`,     // too short to be a UUID
			`{"day":"2020-13-45"}`,              // not a date
			`{"day":"2020-01-02T03:04:05Z"}`,    // a date-time is not a date
			`{"site":"not a uri"}`,              // no scheme
			`{"v4_list":["2001:db8::1"]}`,       // through a slice element
			`{"email_map":{"k":"not-email"}}`,   // through a map value
			`{"v4":"192.0.2.7","v6":"1.2.3.4"}`, // the second property is the one that fails
		},
	)
}

// TestNullIPAddressDoesNotPanic guards the interaction between the two changes
// above. An optional ipv4/ipv6 property became a *netip.Addr so an absent one
// is nil rather than the zero Addr, which netip.Addr's MarshalText writes back
// out as "". The family assertion was written against a value and read
// `n.Field.IsValid()`; a JSON null leaves the pointer nil while _jsonKeys still
// records the key as present, so that line panicked on a document the schema
// permits nothing about.
//
// `{"name":"x","primary_ip":"1.2.3.4"}` is here as the control: without the
// required property present, Validate returns before it ever reaches the
// address arm, and the panic hides.
//
// The null document moved to the invalid half with issue #103. `gateway_ip` is
// {"type":"string","format":"ipv6"} and admits no null, so accepting one was
// itself the defect -- the case was written when an explicit null was silently
// erased everywhere, and it recorded that as the expected behaviour. What it
// guards is unchanged either way: a panic is not a rejection, and the harness
// reports one as a failure whichever list the document sits in.
func TestNullIPAddressDoesNotPanic(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/formats/all_formats.json",
		[]string{
			`{"name":"x","primary_ip":"1.2.3.4"}`,
			`{"name":"x","primary_ip":"1.2.3.4","gateway_ip":"2001:db8::1"}`,
		},
		[]string{
			`{"name":"x","primary_ip":"2001:db8::1"}`,
			`{"name":"x","primary_ip":"1.2.3.4","gateway_ip":"1.2.3.4"}`,
			`{"name":"x","primary_ip":"1.2.3.4","gateway_ip":null}`,
		},
	)
}

// TestAllOfKeepsBranchType is the third of the family: an allOf branch's
// contribution to the *type* was dropped where its contribution to the bounds
// was kept.
//
// `format` was not merged, so {"allOf":[{"$ref":"#/$defs/Stamp"}]} produced
// `type WrappedStamp string` while Stamp itself was time.Time -- two Go types
// for one schema, and the format assertion on one of them only. `enum` was not
// merged either, and it is the sharp case: nothing downstream can infer a type
// from an enum, so the merged schema fell through to `type X any`, which cannot
// carry a Validate at all. Every value outside the enum was accepted. `const`
// takes the same route through promoteConstToEnum, and under --big-int the
// integer arm lost the arbitrary-precision wrapper the flag exists to provide.
//
// The valid half matters as much as the invalid: a fix that merged the enum but
// got the type wrong would reject the members themselves.
func TestAllOfKeepsBranchType(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/allof_single_branch_type.json",
		[]string{
			`{"stamp":"2020-01-02T03:04:05Z","choice":"red","raw":"a"}`,
			`{"stamp":"2020-01-02T03:04:05Z","choice":"green","raw":1}`,
			`{"stamp":"2020-01-02T03:04:05Z","choice":"red","raw":null}`,
			`{"stamp":"2020-01-02T03:04:05Z","choice":"red","raw":"a","addr":"192.0.2.7","level":"high"}`,
		},
		[]string{
			`{"stamp":"not-a-timestamp","choice":"red","raw":"a"}`,                           // the format the branch carried
			`{"stamp":"2020-01-02T03:04:05Z","choice":"blue","raw":"a"}`,                     // outside the branch's enum
			`{"stamp":"2020-01-02T03:04:05Z","choice":"red","raw":"zzz"}`,                    // outside the heterogeneous enum
			`{"stamp":"2020-01-02T03:04:05Z","choice":"red","raw":2}`,                        // a number outside it
			`{"stamp":"2020-01-02T03:04:05Z","choice":"red","raw":"a","addr":"2001:db8::1"}`, // ipv4 branch, v6 value
			`{"stamp":"2020-01-02T03:04:05Z","choice":"red","raw":"a","level":"low"}`,        // outside the branch's const
		},
	)
}

// TestAllOfInlinePositionsKeepBranchType is the sibling half of
// TestAllOfKeepsBranchType. That one reaches the allOf through a $ref, so the
// merge runs under generateTypeDef and lands on a named type. Written inline,
// the same subschema is resolved rather than named, and resolveType has no arm
// that reads an allOf: it fell past every arm to `any`, which carries no
// Validate and is filtered out of the field's own rules. So both the Go type and
// every constraint the branch states were gone -- while the branch's own
// definition sat correctly generated in the same file.
//
// Six positions resolve rather than name, and each is listed below with a
// document that must be accepted beside one that must be refused. The tuple slot
// is here as the control: prefixItems materializes its positions through
// generateTypeDef, so it was already right, and a change that fixed the others
// by breaking the shared path would show up here.
func TestAllOfInlinePositionsKeepBranchType(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/allof_inline_positions.json",
		[]string{
			`{}`,
			`{"chain":"2020-01-02T03:04:05Z"}`,   // property
			`{"nested":"2020-01-02T03:04:05Z"}`,  // allOf inside an allOf
			`{"pick":"red"}`,                     // property, enum branch
			`{"lvl":"high"}`,                     // property, const branch
			`{"ip":"192.0.2.7"}`,                 // property, ipv4 branch
			`{"raw":"a"}`,                        // property, heterogeneous enum
			`{"raw":1}`,                          //
			`{"raw":null}`,                       //
			`{"list":["2020-01-02T03:04:05Z"]}`,  // array element
			`{"map":{"k":"green"}}`,              // map value
			`{"tuple":["2020-01-02T03:04:05Z"]}`, // tuple slot (control)
			`{"union":"red"}`,                    // oneOf branch
			`{"union":7}`,                        // its sibling branch
		},
		[]string{
			`{"chain":"nope"}`,     // not a date-time
			`{"nested":"nope"}`,    //
			`{"pick":"blue"}`,      // outside the enum
			`{"lvl":"low"}`,        // outside the const
			`{"ip":"2001:db8::1"}`, // an IPv6 address against an ipv4 branch
			`{"raw":"zzz"}`,        // outside the heterogeneous enum
			`{"raw":2}`,            //
			`{"list":["nope"]}`,    // through an array element
			`{"map":{"k":"blue"}}`, // through a map value
			`{"tuple":["nope"]}`,   // through the tuple slot
			`{"union":"blue"}`,     // matches neither oneOf branch
		},
	)
}

// TestAllOfBoundOnlyBranchIsEnforced is the last position of the allOf family:
// a branch stating only a bound. Nothing in it names a type outright, so the
// predicate that decides whether an inline allOf needs a name declined it and
// the value resolved to `any` -- with the bound enforced nowhere.
//
// The type comes from inferTypeFromConstraints, the same mapping the no-allOf
// path has always used: minLength/maxLength/pattern say "string", the numeric
// bounds say "number", minItems/maxItems say "array". An inferred type is a
// guess about what the schema is *about*, not an assertion that the instance
// must be one, so each of these is the InferredAliasDef wrapper: the bound binds
// a matching value and every other instance type passes untouched.
//
// That last part is why the accept list carries a number, an array, a boolean
// and a null for a minLength position. Under JSON Schema a keyword about strings
// is satisfied vacuously by everything that is not a string, and a fix that
// reached for a bare `type X string` instead would reject all four -- trading
// under-enforcement for a false rejection, which is the worse direction.
func TestAllOfBoundOnlyBranchIsEnforced(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/allof_bound_only.json",
		[]string{
			`{}`,
			`{"prop":"abc"}`,      // property, string long enough
			`{"prop":5}`,          // vacuous: minLength says nothing about a number
			`{"prop":[1,2]}`,      //
			`{"prop":true}`,       //
			`{"prop":null}`,       //
			`{"viaRef":"abc"}`,    // the branch is a $ref to a definition
			`{"viaRef":5}`,        //
			`{"num":7}`,           // numeric bound
			`{"num":"ab"}`,        // vacuous: minimum says nothing about a string
			`{"nested":"abc"}`,    // allOf inside an allOf
			`{"arr":[1,2]}`,       // minItems
			`{"arr":"ab"}`,        // vacuous for a string
			`{"list":["abc"]}`,    // array element
			`{"map":{"k":"abc"}}`, // map value
			`{"tuple":["abc"]}`,   // tuple slot
			`{"union":"abc"}`,     // oneOf branch: the bound branch alone matches
		},
		[]string{
			`{"prop":"xy"}`,      // too short
			`{"viaRef":"xy"}`,    // through the $ref
			`{"num":4}`,          // below the minimum
			`{"nested":"xy"}`,    // through the nested allOf
			`{"arr":[1]}`,        // too few items
			`{"list":["xy"]}`,    // through an array element
			`{"map":{"k":"xy"}}`, // through a map value
			`{"tuple":["xy"]}`,   // through the tuple slot
			`{"union":"xy"}`,     // matches neither branch
			// A boolean matches *both* branches: {"type":"boolean"} by name, and
			// the bound branch vacuously, since minLength says nothing about a
			// boolean. oneOf wants exactly one, so this is invalid -- and it is
			// only invalid if the bound branch really is vacuous for non-strings.
			// A fix that typed the branch `string` would have this match one
			// branch and pass.
			`{"union":true}`,
		},
	)
}

// TestAllOfKeepsBigIntWrapper is the --big-int half of the same defect: the
// allOf arm built a plain int64 alias where the no-allOf arm builds the
// arbitrary-precision wrapper, so a value too large for an int64 was silently
// truncated -- or refused -- under the flag whose whole purpose is to carry it.
func TestAllOfKeepsBigIntWrapper(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	const doc = ` + "`" + `{"n":123456789012345678901234567890}` + "`" + `
	var v AllOfBigInt
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		fmt.Fprintf(os.Stderr, "decoding %s: %v\n", doc, err)
		os.Exit(1)
	}
	if err := v.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "validating %s: %v\n", doc, err)
		os.Exit(1)
	}
	if !v.N.IsBigInt() {
		fmt.Fprintf(os.Stderr, "%s: N.IsBigInt() = false; the value does not fit an int64\n", doc)
		os.Exit(1)
	}
	out, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshalling: %v\n", err)
		os.Exit(1)
	}
	if string(out) != doc {
		fmt.Fprintf(os.Stderr, "round trip: %s -> %s\n", doc, string(out))
		os.Exit(1)
	}
	// The branch's own bound still binds.
	var under AllOfBigInt
	if err := json.Unmarshal([]byte(` + "`" + `{"n":3}` + "`" + `), &under); err != nil {
		fmt.Fprintf(os.Stderr, "decoding a small n: %v\n", err)
		os.Exit(1)
	}
	if err := under.Validate(); err == nil {
		fmt.Fprintln(os.Stderr, "n=3 passed a minimum of 5 carried by the allOf branch")
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`
	runGeneratedMainProgramWithConfig(t,
		"testdata/schemas/regression/allof_bigint.json",
		"allof_bigint_test",
		mainGo,
		generator.Config{PackageName: "testpkg", OmitEmpty: true, BigIntSupport: true},
	)
}

// TestExplicitNullIsRefusedWhereTheSchemaGivesAType is issue #103.
//
// An explicit null was accepted at every position whose schema states a type
// that does not include "null", and it was not merely unchecked but erased:
// {"s":null} against {"properties":{"s":{"type":"string"}}} came back {}.
// python-jsonschema and js-ajv both call that document invalid.
//
// The cause is that nothing downstream can see the null. encoding/json decodes
// one into a nil pointer, a nil slice or map, or a scalar left untouched at its
// zero -- all of them exactly the state an *absent* property leaves -- so by the
// time Validate runs "present and null" and "absent" are one state and the
// optional-field guard passes over both. The verdict is taken from the raw
// document instead, in UnmarshalJSON, which is why the invalid half here is
// refused at decode time rather than by Validate.
//
// The invalid half enumerates the positions, because the recurring failure in
// this generator is a fix that lands in one position and not its sibling: an
// inline scalar, a $ref to an alias and to a struct, an array element, both
// levels of a nested array, a map value, a map of arrays, an array of maps, a
// tuple slot, a oneOf branch, an allOf, an element of a named array alias, and
// an overflow value governed by a schema-valued additionalProperties -- each in
// its optional spelling and, where the shape allows, its required one.
//
// The valid half is the more important of the two. Refusing a null the schema
// *does* admit is a false rejection, worse than the acceptance it replaces, so
// every spelling of "may be null" is here: the type list on a property, on a
// definition behind a $ref, on an array's items and on a map's values, and the
// array that admits a null of its own while its elements do not. So is a
// property with no type at all and one carrying only a bound, where JSON Schema
// says nothing about null and this check must therefore say nothing either. The
// first entry is the control for the whole thing: an absent optional property is
// not a present null, and must stay accepted.
func TestExplicitNullIsRefusedWhereTheSchemaGivesAType(t *testing.T) {
	const required = `"reqScalar":"x","reqAlias":"ab","reqStruct":{},"reqArray":[]`
	with := func(extra string) string {
		if extra == "" {
			return "{" + required + "}"
		}
		return "{" + required + "," + extra + "}"
	}
	runValidationCases(t,
		"testdata/schemas/regression/explicit_null_positions.json",
		[]string{
			with(""),                                  // every optional property absent
			with(`"scalar":"x","count":1`),            // present and well typed
			with(`"alias":"ab","struct":{"k":"v"}`),   //
			with(`"array":["a"],"nested":[["a"]]`),    //
			with(`"mapOfString":{"a":"b"}`),           //
			with(`"tuple":["a",1]`),                   //
			with(`"union":{"tag":"t"}`),               //
			with(`"bounded":"ab","namedArray":["a"]`), //
			with(`"overflow":{"a":"b","zz":"c"}`),     //
			with(`"nullableScalar":null`),             // the type list admits null
			with(`"nullableAlias":null`),              // behind a $ref
			with(`"nullableItems":["a",null]`),        // in an array's items
			with(`"nullableValues":{"a":null}`),       // in a map's values
			with(`"nullableOuter":null`),              // the array itself may be null
			with(`"nullableOuter":["a"]`),             // and still holds strings
			with(`"untyped":null`),                    // no type keyword: null is a value like any other
			with(`"boundOnly":null`),                  // a bound alone says nothing about null
		},
		[]string{
			with(`"scalar":null`),            // inline scalar
			with(`"count":null`),             // inline integer
			with(`"alias":null`),             // $ref to a named alias
			with(`"struct":null`),            // $ref to a named struct
			with(`"inline":null`),            // inline object
			with(`"array":null`),             // the array itself
			with(`"array":["a",null]`),       // an array element
			with(`"nested":[["a"],null]`),    // a nested array's outer element
			with(`"nested":[["a",null]]`),    // and its inner one
			with(`"mapOfString":null`),       // the map itself
			with(`"mapOfString":{"a":null}`), // a map value
			with(`"mapOfArray":{"a":null}`),  // a map of arrays
			with(`"mapOfArray":{"a":["x",null]}`),
			with(`"arrayOfMap":[null]`), // an array of maps
			with(`"arrayOfMap":[{"a":null}]`),
			with(`"tuple":null`),               // the tuple itself
			with(`"tuple":[null,1]`),           // a tuple slot
			with(`"union":null`),               // a oneOf branch
			with(`"bounded":null`),             // inside an allOf
			with(`"namedArray":null`),          // a named array alias
			with(`"namedArray":["a",null]`),    // and its elements
			with(`"overflow":null`),            // a struct with an overflow map
			with(`"overflow":{"zz":null}`),     // and one of its overflow values
			with(`"inline":{"x":null}`),        // a property of an inline object
			with(`"struct":{"k":null}`),        // a property of a definition
			with(`"nullableOuter":["a",null]`), // the array admits null; its elements do not
			`{"reqScalar":null,"reqAlias":"ab","reqStruct":{},"reqArray":[]}`, // required scalar
			`{"reqScalar":"x","reqAlias":null,"reqStruct":{},"reqArray":[]}`,  // required alias
			`{"reqScalar":"x","reqAlias":"ab","reqStruct":null,"reqArray":[]}`,
			`{"reqScalar":"x","reqAlias":"ab","reqStruct":{},"reqArray":null}`,
			`{"reqScalar":"x","reqAlias":"ab","reqStruct":{},"reqArray":["a",null]}`,
			`null`, // the root object itself
		},
	)
}

// TestAllOfBranchOverflowIsEnforced covers the keyword an allOf merge cannot
// express by folding it into the parent: a branch's own additionalProperties.
//
// The keyword is scoped to the schema object stating it, so a branch declaring
// no property speaks about *every* key of the instance -- including the ones the
// parent declares and gives fields to. Folding it into the parent's overflow
// map, which holds only the keys the parent does not declare, would check it on
// a smaller set than the schema names; that is why it was dropped instead, and
// every unaccounted value went unchecked.
//
// Each verdict below was cross-checked against python-jsonschema and js-ajv
// through Bowtie; the two agree with each other on all of them.
//
// The last three positions are the controls, and they are what makes "nothing
// else moved" evidence rather than an absence of testing:
//
//   - ownAdditional states additionalProperties on the *parent*, where the
//     overflow map is the right scope and always was. A per-branch check that
//     claimed it too would report one violation twice, or check `a` -- which the
//     parent declares and its own additionalProperties therefore does not see.
//   - soleBranch is the narrow case the merge already handles exactly: nothing
//     anywhere names a property, so the branch's key set and the parent's
//     overflow map are provably the same set and the keyword is folded in. The
//     per-branch notion must subsume that arm, not duplicate it.
//   - plain has an allOf branch with no overflow keyword at all, so `z` is
//     unconstrained. A change that gave every branch a check would refuse it.
func TestAllOfBranchOverflowIsEnforced(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/allof_branch_overflow.json",
		[]string{
			`{}`,
			// The branch declares nothing, so its additionalProperties governs
			// every key -- the parent's `a` as much as an undeclared `b`.
			`{"bare":{}}`,
			`{"bare":{"a":7}}`,
			`{"bare":{"b":7}}`,
			`{"forbid":{}}`,
			// The branch's own properties and patterns are what it accounts for.
			`{"adjacent":{"b":1,"xy":2}}`,
			`{"adjacent":{}}`,
			// Through a $ref: the accounted set is the target's own properties.
			`{"viaRef":{"base":1}}`,
			`{"viaRef":{}}`,
			// Two branches each stating the keyword; both bind at once, which one
			// overflow map could never say.
			`{"twoBranches":{"a":7}}`,
			`{"nestedAllOf":{"a":7}}`,
			// A sub-schema past what an in-place scalar rule can express.
			`{"objectValue":{"a":{"n":7}}}`,
			// A schema-valued unevaluatedProperties in a branch: `b` is the only
			// key that branch evaluates, so nothing else is unevaluated here.
			// (Cousin isolation -- the branch cannot see the parent's `a`.)
			`{"branchUnevaluated":{"b":1}}`,
			`{"branchUnevaluatedFalse":{"b":1}}`,
			`{"branchUnevaluatedFalse":{}}`,
			// Controls: the parent's own keyword, the narrow merge, and a branch
			// with no overflow keyword at all.
			`{"ownAdditional":{"a":1,"z":7}}`,
			`{"soleBranch":{"k":"ab"}}`,
			`{"plain":{"a":1}}`,
			`{"plain":{"a":1,"z":"anything"}}`,
		},
		[]string{
			`{"bare":{"a":1}}`, // the parent's own property, judged by the branch
			`{"bare":{"b":1}}`,
			`{"forbid":{"a":1}}`, // additionalProperties: false forbids even `a`
			`{"forbid":{"z":1}}`,
			`{"adjacent":{"a":1}}`, // the branch does not declare `a`
			`{"adjacent":{"z":1}}`,
			`{"viaRef":{"other":1}}`,   // the $ref target does not declare it
			`{"twoBranches":{"a":1}}`,  // below the first branch's minimum
			`{"twoBranches":{"a":11}}`, // above the second branch's maximum
			`{"nestedAllOf":{"a":1}}`,  // through the nested allOf
			`{"objectValue":{"a":{}}}`, // the value sub-schema's `required`
			`{"objectValue":{"a":{"n":1}}}`,
			`{"branchUnevaluated":{"a":1}}`,      // unevaluated in the branch's scope
			`{"branchUnevaluatedFalse":{"a":1}}`, // the parent's own property
			`{"branchUnevaluatedFalse":{"c":1}}`,
			`{"ownAdditional":{"a":1,"z":1}}`,
			`{"soleBranch":{"k":"a"}}`,
			`{"plain":{}}`,
		},
	)
}

// TestAllOfObjectEnumIsEnforced covers an enum an allOf branch states over whole
// objects, beside properties the parent declares.
//
// The merge produces a struct, and the enum's members are whole documents, so
// there is no field for the check to hang off: it is a comparison of the
// document against each member. Dropped, every object satisfying `properties`
// was accepted rather than only the ones the enum permits.
//
// Both sides of the comparison go through one JSON encoder, which is what makes
// it sound: `reordered` pins that key order does not decide the answer and
// `nested` that a member is compared as a whole document rather than key by key.
// `plain` is the control -- an allOf branch stating no enum must leave the
// object unconstrained.
//
// Every verdict was cross-checked against python-jsonschema and js-ajv through
// Bowtie; the two agree with each other on all of them.
func TestAllOfObjectEnumIsEnforced(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/allof_object_enum.json",
		[]string{
			`{}`,
			`{"inline":{"k":1}}`,
			`{"inline":{"k":2}}`,
			`{"viaRef":{"k":1}}`,
			// The same document written with its keys in either order.
			`{"reordered":{"a":1,"b":2}}`,
			`{"reordered":{"b":2,"a":1}}`,
			// A const is a one-member enum, and the merge carries it in the Const
			// slot rather than the Enum one.
			`{"constMember":{"k":1}}`,
			`{"nested":{"k":{"n":[1,2]}}}`,
			// The same document with a number written another way. Both sides go
			// through one encoder, so 1.0 and 1 are the same member -- a
			// comparison against the bytes as they arrived would refuse this.
			`{"inline":{"k":1.0}}`,
			`{"nested":{"k":{"n":[1.0,2]}}}`,
			`{"standalone":{"k":1}}`,
			// Control: a branch with no enum leaves the object to `properties`.
			`{"plain":{"k":1}}`,
			`{"plain":{"k":99}}`,
		},
		[]string{
			`{"inline":{"k":3}}`,       // satisfies properties, outside the enum
			`{"inline":{}}`,            // no member is the empty object
			`{"inline":{"k":1,"z":9}}`, // a member plus an extra key is not a member
			`{"viaRef":{"k":3}}`,
			`{"reordered":{"a":1}}`,        // a subset of a member is not a member
			`{"constMember":{"k":2}}`,      // outside the const
			`{"nested":{"k":{"n":[2,1]}}}`, // array order does decide it
			`{"standalone":{"k":3}}`,
		},
	)
}

// TestHandBuiltObjectEnumValidate pins the one thing a whole-document enum
// cannot answer: a value that was never a document.
//
// The comparison is against the JSON the unmarshaler kept, because that is the
// instance the enum speaks about. A hand-constructed struct has none, and its Go
// zero values are not something the schema ever saw -- an optional field left
// nil is indistinguishable from an absent property, so marshalling the struct
// and comparing that would refuse values a caller built correctly. The check is
// skipped there instead, the same way required-property presence is, and the
// gate is what makes that a decision rather than an accident: without it the nil
// map encodes as `null` and every hand-built value is rejected.
func TestHandBuiltObjectEnumValidate(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	// Built by hand: no document, so the enum has nothing to compare against.
	k := int64(3)
	byHand := AllOfObjectEnumInline{K: &k}
	if err := byHand.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "hand-built value rejected: %v\n", err)
		os.Exit(1)
	}
	// The same value decoded from JSON does carry its document, and k=3 is not
	// a member. This is the accept-control's other half: the skip above is
	// about the absence of a document, not about the check being inert.
	var fromJSON AllOfObjectEnumInline
	if err := json.Unmarshal([]byte(` + "`" + `{"k":3}` + "`" + `), &fromJSON); err != nil {
		fmt.Fprintf(os.Stderr, "decoding: %v\n", err)
		os.Exit(1)
	}
	if err := fromJSON.Validate(); err == nil {
		fmt.Fprintln(os.Stderr, "a decoded {\"k\":3} passed an enum permitting only {\"k\":1} and {\"k\":2}")
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t,
		"testdata/schemas/regression/allof_object_enum.json",
		"allof_object_enum_handbuilt_test",
		mainGo,
	)
}

// TestRootCompositionBranchesValidation exercises a root anyOf whose every
// branch is one the static evaluator refuses: a $ref, a nested composition, an
// enum, and a boolean false. The whole schema used to come out as
// `type RootCompositionBranches any` -- a type Go forbids methods on, so there
// was no Validate and json.Unmarshal into it could not fail. Every one of the
// rejections below was an acceptance.
//
// The valid cases are the control that the branches are still *permissive*
// where the schema says so: one per surviving branch, so a check that rejected
// everything would fail here rather than look like a fix.
func TestRootCompositionBranchesValidation(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/root_composition_branches.json",
		[]string{
			`1`,    // the $ref branch: integer >= 1
			`42`,   // the same branch, further in
			`"ab"`, // the nested-composition branch: string, minLength 2
			`true`, // the enum branch
			`null`, // the enum branch's other member
		},
		[]string{
			`0`,     // integer, but below the $ref branch's minimum
			`"a"`,   // string, but shorter than the nested branch's minLength
			`false`, // not an enum member, and the `false` branch matches nothing
			`{}`,    // no branch admits an object
			`[1]`,   // nor an array
		},
	)
}

// TestRootNotObjectShapeValidation exercises a root "not" whose sub-schema
// states object structure, which extractNotSchemaDef does not handle: the
// schema used to come out as `type RootNotObjectShape any` and accept the one
// document it forbids.
//
// The valid list is the accept-control, and it is the whole point of a "not":
// everything that fails to match the negated sub-schema must still pass, which
// here is every value of the wrong type, every object missing the required key,
// and every object whose "foo" is not a string.
func TestRootNotObjectShapeValidation(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/root_not_object_shape.json",
		[]string{
			`{"foo":1}`,   // an object, but "foo" is not a string
			`{"bar":"x"}`, // an object without the required "foo"
			`{}`,          // required "foo" absent
			`"foo"`,       // not an object at all
			`[1,2]`,
			`null`,
			`7`,
		},
		[]string{
			`{"foo":"bar"}`,         // exactly the shape the "not" forbids
			`{"foo":"bar","baz":1}`, // extra keys do not rescue it
		},
	)
}

// TestRuntimeSchemaAbsentValueValidates pins that a runtime-evaluated wrapper
// holding no value validates rather than failing to decode one.
//
// The wrapper's Validate decodes its raw JSON, and a value that was never built
// from a document has none: an optional property the source JSON did not carry,
// or a value assembled in Go. Without the guard, json.Unmarshal is handed an
// empty slice and reports "unexpected end of JSON input", so a schema that
// forbids one shape rejected the absence of any shape at all.
func TestRuntimeSchemaAbsentValueValidates(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	// Never decoded from a document: the raw JSON is empty.
	var absent RootNotObjectShape
	if err := absent.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "a value that holds nothing was rejected: %v\n", err)
		os.Exit(1)
	}
	// The accept-control's other half: the skip above is about the absence of a
	// document, not about the check being inert. A value that does carry the
	// forbidden shape still has to fail.
	var present RootNotObjectShape
	if err := json.Unmarshal([]byte(` + "`" + `{"foo":"bar"}` + "`" + `), &present); err != nil {
		fmt.Fprintf(os.Stderr, "decoding: %v\n", err)
		os.Exit(1)
	}
	if err := present.Validate(); err == nil {
		fmt.Fprintln(os.Stderr, "the shape the schema forbids was accepted")
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t,
		"testdata/schemas/regression/root_not_object_shape.json",
		"root_not_absent_test",
		mainGo,
	)
}

// TestRefToRuntimeWrapperValidation pins that naming a definition which compiles
// to the runtime evaluator leaves it usable.
//
// A Go named type inherits none of its underlying type's methods, so
// "type RefToRuntimeWrapper Wrapped" over a struct whose only field is
// unexported decodes as a struct with no exported field: encoding/json then
// refuses "ab" outright, for a schema that says "ab" is exactly what it wants.
//
// The type is named here rather than left to the shared helper, which picks the
// last struct in the file -- that is Wrapped itself, and validating it directly
// tests the wrapper while stepping straight over the alias that was broken.
func TestRefToRuntimeWrapperValidation(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	// Valid, and not null: a null decodes into a struct with no exported field
	// without complaint, so it cannot tell a delegating alias from a broken one.
	for _, ok := range []string{` + "`" + `"ab"` + "`" + `, ` + "`" + `"abcd"` + "`" + `} {
		var v RefToRuntimeWrapper
		if err := json.Unmarshal([]byte(ok), &v); err != nil {
			fmt.Fprintf(os.Stderr, "valid document %s did not decode: %v\n", ok, err)
			os.Exit(1)
		}
		if err := v.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "valid document %s was rejected: %v\n", ok, err)
			os.Exit(1)
		}
		// Marshalling has to come back out through the wrapper too.
		if out, err := json.Marshal(v); err != nil || string(out) != ok {
			fmt.Fprintf(os.Stderr, "re-marshalling %s gave %s (%v)\n", ok, string(out), err)
			os.Exit(1)
		}
	}
	// The accept-control's other half: the schema still rejects what it forbids.
	for _, bad := range []string{` + "`" + `"a"` + "`" + `, ` + "`" + `null` + "`" + `, ` + "`" + `1` + "`" + `, ` + "`" + `{}` + "`" + `} {
		var v RefToRuntimeWrapper
		if err := json.Unmarshal([]byte(bad), &v); err != nil {
			continue // an unmarshal-time rejection is an acceptable failure mode
		}
		if err := v.Validate(); err == nil {
			fmt.Fprintf(os.Stderr, "invalid document %s was accepted\n", bad)
			os.Exit(1)
		}
	}
	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t,
		"testdata/schemas/regression/ref_to_runtime_wrapper.json",
		"ref_to_runtime_wrapper_test",
		mainGo,
	)
}
