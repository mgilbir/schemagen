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
			Name:        "enum/string_enum",
			SchemaPath:  "testdata/schemas/enum/string_enum.json",
			FixturePath: "testdata/fixtures/enum/string_enum.json",
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
	}

	tmpDir := t.TempDir()

	// Write go.mod + go.sum (with goecma262 dependency for generated code)
	if err := writeTestGoMod(tmpDir, "compile_test"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	// Copy all golden files to the temp directory, adjusting for name conflicts
	// by prefixing with directory name
	fileIdx := 0
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

			data, err := os.ReadFile(filepath.Join(fullDir, entry.Name()))
			if err != nil {
				t.Fatalf("reading %s: %v", entry.Name(), err)
			}

			// Replace package name to be consistent
			content := strings.Replace(string(data), "package testpkg", "package main", 1)
			outName := fmt.Sprintf("gen_%d_%s", fileIdx, entry.Name())
			if err := os.WriteFile(filepath.Join(tmpDir, outName), []byte(content), 0o644); err != nil {
				t.Fatalf("writing %s: %v", outName, err)
			}
			fileIdx++
		}
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
func extractRootTypeName(t *testing.T, code string) string {
	t.Helper()

	lines := strings.Split(code, "\n")
	var lastType string
	var currentType string
	var hasJSONTag bool

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "type ") && strings.Contains(trimmed, " struct {") {
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
		// Fallback: just find the last struct
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "type ") && strings.Contains(trimmed, " struct {") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 2 {
					lastType = parts[1]
				}
			}
		}
	}

	if lastType == "" {
		// Final fallback: look for type aliases (e.g., "type Root = any" or "type Root string").
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "type ") {
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
