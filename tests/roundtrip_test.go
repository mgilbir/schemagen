package tests

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

			outputStr := strings.TrimSpace(string(output))
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

	outputStr := strings.TrimSpace(string(output))
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

	outputStr := strings.TrimSpace(string(output))
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
	if outputStr := strings.TrimSpace(string(output)); outputStr != "PASS" {
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
