package tests

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
		{
			// Issue #213's round-trip half. A property that only a `then` or an
			// `else` describes no longer refuses a null -- the branch typed it,
			// but only for the documents its condition selects -- and a null that
			// is no longer refused has to be recorded, or it comes back as an
			// absence. The document writes one in two such positions, and puts a
			// value the branch's enum would refuse in two more, which is the
			// state the type has to be able to hold at all.
			Name:        "regression/conditional_only_property_positions",
			SchemaPath:  "testdata/schemas/regression/conditional_only_property_positions.json",
			FixturePath: "testdata/fixtures/regression/conditional_only_property_positions.json",
		},
		{
			// Issue #110, and the reason it is here rather than only in the
			// goldens: this harness compares the document against itself after
			// a decode and a re-encode, which is exactly what the defect broke.
			// The fixture writes a null in most of the positions the schema
			// permits one and leaves "nullableScalar" out, so both directions
			// are live -- a dropped null and an absence written back as null
			// each fail the comparison. (The second is what the old convention
			// did: omitempty was suppressed for a nullable property, so its nil
			// was written as null whether the document had one or not.)
			Name:        "regression/present_null_positions",
			SchemaPath:  "testdata/schemas/regression/present_null_positions.json",
			FixturePath: "testdata/fixtures/regression/present_null_positions.json",
		},
		{
			// The accept-control for --strict-read-write, and it costs nothing to
			// have: under the default configuration "deprecated", "readOnly",
			// "writeOnly" and "examples" are documentation, so a document
			// carrying every one of them has to come back exactly as it went in.
			// The flag's own test asserts the two ways that stops being true;
			// this is what says the flag is the only thing that makes it stop.
			Name:        "regression/annotation_vocabulary",
			SchemaPath:  "testdata/schemas/regression/annotation_vocabulary.json",
			FixturePath: "testdata/fixtures/regression/annotation_vocabulary.json",
		},
		{
			// The same accept-control one level up: the annotation keywords now
			// reach every named-type kind's doc comment, and a comment is all
			// they are. A document that exercises all nine kinds at once has to
			// come back exactly as it went in.
			Name:        "regression/annotation_positions",
			SchemaPath:  "testdata/schemas/regression/annotation_positions.json",
			FixturePath: "testdata/fixtures/regression/annotation_positions.json",
		},
		{
			// The accept-control for issue #172's half. Every position
			// "readOnly" and "writeOnly" can be written appears here at once,
			// including the two the flag now binds that it did not before, and
			// under the default configuration the whole document has to come
			// back untouched. TestStrictReadWriteBindsWhereverThePropertyIs is
			// what says the flag is the only thing that changes it.
			Name:        "regression/read_write_positions",
			SchemaPath:  "testdata/schemas/regression/read_write_positions.json",
			FixturePath: "testdata/fixtures/regression/read_write_positions.json",
		},
		{
			// Issue #174's shape, under the setting that has nothing to say
			// about it. Every conditional branch here contributes a property to
			// the merged struct, so every one of them has to survive the trip:
			// the merge is what gives those values a field to live in, and a fix
			// that suppressed the branch instead of only its annotations would
			// drop them here.
			Name:        "regression/read_write_conditional_positions",
			SchemaPath:  "testdata/schemas/regression/read_write_conditional_positions.json",
			FixturePath: "testdata/fixtures/regression/read_write_conditional_positions.json",
		},
		{
			// The reach matrix, under the reading that changes nothing about a
			// document. Every conditional branch here contributes a property to
			// the merged struct and every unconditional one contributes an
			// annotation, so the trip is what says the narrowing suppresses the
			// keyword and not the field: a property that stopped becoming a
			// field would land in the overflow map and still round-trip, which
			// is why the doc-comment test looks fields up by their struct tags.
			Name:        "regression/annotation_reach_positions",
			SchemaPath:  "testdata/schemas/regression/annotation_reach_positions.json",
			FixturePath: "testdata/fixtures/regression/annotation_reach_positions.json",
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
	runValidationCasesWithConfig(t, schemaPath, generator.Config{
		PackageName: "testpkg",
		OmitEmpty:   true,
	}, valid, invalid)
}

// formatAssertingConfig is the configuration a test uses when its subject is
// the format assertion itself.
//
// Every fixture below declares draft 2020-12, whose default meta-schema makes
// `format` an annotation -- so under the ordinary configuration these documents
// are all valid and there is nothing for the test to see. The flag is what asks
// the question each of these tests is about. TestFormatPostureFollowsTheDialect
// is the one that asks whether the flag matters.
func formatAssertingConfig() generator.Config {
	return generator.Config{PackageName: "testpkg", OmitEmpty: true, FormatAssertion: true}
}

// formatAnnotatingConfig is formatAssertingConfig's mirror: the opt-out a caller
// reaches for on a dialect that asserts. Only TestFormatPostureFollowsTheDialect
// uses it, because it is the only test whose subject is the posture rather than
// the accuracy of a check.
func formatAnnotatingConfig() generator.Config {
	return generator.Config{PackageName: "testpkg", OmitEmpty: true, FormatAnnotation: true}
}

// runValidationCasesWithConfig is runValidationCases under a chosen generator
// configuration. It exists for the format posture: the same fixture has to be
// judged twice, once under the dialect's own answer and once under
// --format-assertion, since a test that only ever ran one of them could not tell
// a working flag from a flag that does nothing.
func runValidationCasesWithConfig(t *testing.T, schemaPath string, cfg generator.Config, valid, invalid []string) {
	t.Helper()
	runValidationCasesOn(t, schemaPath, "", cfg, valid, invalid)
}

// runValidationCasesForType is runValidationCases against a named type rather
// than the one extractRootTypeName picks.
//
// It exists because that function answers "the last top-level struct carrying
// JSON tags", which is the root for a schema whose root is an object and is not
// the root for a schema whose root is anything else. A root compiled to the
// runtime evaluator is a struct wrapping raw JSON with no tag on it, so a
// document schema that also defines a tagged struct anywhere hands these cases
// the wrong type -- and the wrong type has its own Validate, so the run passes
// or fails on a question nobody asked. Naming the type is the only way to be
// sure the documents are being judged by the schema the test is about.
func runValidationCasesForType(t *testing.T, schemaPath, typeName string, valid, invalid []string) {
	t.Helper()
	runValidationCasesOn(t, schemaPath, typeName, generator.Config{
		PackageName: "testpkg",
		OmitEmpty:   true,
	}, valid, invalid)
}

// refuseRoundTripBreakingConfig stops a configuration that deliberately breaks
// round-tripping from being measured by a helper whose whole assertion is that
// round-tripping holds.
//
// runValidationCasesOn decodes each document and, for the valid half, requires
// the decode to succeed; the round-trip harness above requires the bytes to come
// back as they went in. Config.StrictReadWrite makes both false on purpose: it
// refuses a document that sets a readOnly property, and drops every writeOnly
// one on the way out. Handed such a config, this helper would either report a
// fixture as broken or -- far worse -- grow an "unless strict mode is on"
// branch, and that branch would weaken the assertion for every other fixture in
// the tree. This repository has already paid for that once, when a golden pinned
// {"gateway_ip":null} as valid against a format:ipv6 string and a real defect
// survived as recorded behaviour.
//
// So the rule is a test failure rather than a convention. See
// TestStrictReadWriteChangesDecodeAndEncode, which asserts the asymmetry by
// name, and the regression/annotation_vocabulary round-trip case, which holds
// the default configuration to the ordinary contract.
//
// runGeneratedMainProgramWithConfig is deliberately not guarded: it runs a
// program that states its own contract, which is exactly where a caller who
// wants the flag should go.
// The decision is a pure function so it can be tested. A guard reached only by a
// caller nobody has written yet is a guard nobody has watched fire: planting a
// fault in the version that only called t.Fatalf left every test in the package
// passing, because no fixture hands these helpers a strict config today and the
// guard exists for the one somebody adds tomorrow.
// TestRoundTripHelpersRefuseAConfigThatBreaksRoundTripping is what watches it.
func roundTripBreakingReason(cfg generator.Config) string {
	if cfg.StrictReadWrite {
		return "generator.Config.StrictReadWrite deliberately breaks round-tripping: " +
			"it refuses a document setting a readOnly property and omits every writeOnly one. " +
			"Cover the flag in TestStrictReadWriteChangesDecodeAndEncode instead, " +
			"which states the asymmetry rather than discovering it as a diff."
	}
	return ""
}

func refuseRoundTripBreakingConfig(t *testing.T, cfg generator.Config) {
	t.Helper()
	if reason := roundTripBreakingReason(cfg); reason != "" {
		t.Fatalf("this helper asserts that a document comes back as it went in, and %s", reason)
	}
}

// TestRoundTripHelpersRefuseAConfigThatBreaksRoundTripping holds the rule that
// keeps --strict-read-write out of the round-trip fixtures.
//
// The hazard is not a fixture that fails. It is the repair somebody reaches for
// when one does: an "unless strict mode is on" branch inside
// runValidationCasesOn, which would weaken "a document comes back as it went in"
// for every fixture in the tree at once, to accommodate one caller. This
// repository has already paid for that kind of erosion, when a golden pinned
// {"gateway_ip":null} as valid against a format:ipv6 string and a real defect
// survived as recorded behaviour.
//
// The default configuration is the other half, and it is the half that would
// catch a guard written too wide: a refusal that fired on every config would
// take the entire package with it, which is a failure mode worth naming here
// rather than discovering as 200 red tests.
func TestRoundTripHelpersRefuseAConfigThatBreaksRoundTripping(t *testing.T) {
	strict := generator.Config{PackageName: "testpkg", OmitEmpty: true, StrictReadWrite: true}
	if roundTripBreakingReason(strict) == "" {
		t.Errorf("a config carrying StrictReadWrite was accepted by the round-trip helpers, " +
			"which assert that a document comes back as it went in -- and that flag is defined to break it")
	}
	for name, cfg := range map[string]generator.Config{
		"default":           {PackageName: "testpkg", OmitEmpty: true},
		"format asserting":  formatAssertingConfig(),
		"format annotating": formatAnnotatingConfig(),
		"strict properties": {PackageName: "testpkg", OmitEmpty: true, StrictProperties: true},
	} {
		if reason := roundTripBreakingReason(cfg); reason != "" {
			t.Errorf("%s config was refused, and it round-trips like any other: %s", name, reason)
		}
	}

	// And the decision is actually consulted. The two checks above hold what the
	// answer is; this holds that the helper asks. Deleting the call passed every
	// other test in this package -- there is no fixture handing a strict config
	// to runValidationCasesOn today, and there will not be one until somebody
	// makes exactly the mistake the guard is for, at which point the guard would
	// no longer be there. Reading the source is how this repository already
	// keeps that kind of rule from being a comment; see
	// TestEveryKnownFailureMapIsClassified.
	src, err := os.ReadFile("roundtrip_test.go")
	if err != nil {
		t.Fatalf("reading this file: %v", err)
	}
	body := string(src)
	// Both needles carry their leading newline and indentation, so they match a
	// declaration and a statement rather than the quoted copies of themselves
	// three lines below. Without that, the first draft of this check found its
	// own source line, measured the region between there and the end of this
	// function, found the string it was looking for inside its own assertion,
	// and passed under a planted deletion. A check that reads the file it is
	// written in has to be told the difference between code and a string
	// literal, and this is the cheapest way to say it.
	const decl = "\nfunc runValidationCasesOn("
	const call = "\n\trefuseRoundTripBreakingConfig(t, cfg)\n"
	start := strings.Index(body, decl)
	if start < 0 {
		t.Fatalf("roundtrip_test.go declares no runValidationCasesOn")
	}
	end := strings.Index(body[start+1:], "\n}\n")
	if end < 0 {
		t.Fatalf("cannot find the end of runValidationCasesOn")
	}
	if !strings.Contains(body[start:start+1+end], call) {
		t.Errorf("runValidationCasesOn does not call refuseRoundTripBreakingConfig, " +
			"so a config that breaks round-tripping reaches a harness whose whole assertion is that it holds")
	}
}

func runValidationCasesOn(t *testing.T, schemaPath, typeName string, cfg generator.Config, valid, invalid []string) {
	t.Helper()
	refuseRoundTripBreakingConfig(t, cfg)
	generated := generateFromSchemaWithConfig(t, schemaPath, cfg)
	rootType := typeName
	if rootType == "" {
		rootType = extractRootTypeName(t, string(generated))
	} else if !strings.Contains(string(generated), "type "+rootType+" ") {
		t.Fatalf("generated code declares no type %s; the fixture or the name is wrong", rootType)
	}
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

// runRoundTripCases generates code for schemaPath, then compiles and runs a
// program that unmarshals each document, marshals it back, and asserts the two
// are the same JSON value.
//
// runValidationCases is blind to this: a field written back as an invented null,
// or one dropped that the document carried, still passes Validate. The tag that
// decides it is chosen from the field's Go type and the property's schema
// together, and a golden pins the tag rather than its effect -- which is a
// guard that cannot fail for the reason it exists.
func runRoundTripCases(t *testing.T, schemaPath string, docs ...string) {
	t.Helper()
	cfg := generator.Config{PackageName: "testpkg", OmitEmpty: true}
	generated := generateFromSchemaWithConfig(t, schemaPath, cfg)
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
	"reflect"
)

func main() {
	docs := []string{%s}
	failed := false
	for _, input := range docs {
		var obj %s
		if err := json.Unmarshal([]byte(input), &obj); err != nil {
			fmt.Fprintf(os.Stderr, "unmarshal %%s: %%v\n", input, err)
			failed = true
			continue
		}
		out, err := json.Marshal(obj)
		if err != nil {
			fmt.Fprintf(os.Stderr, "marshal %%s: %%v\n", input, err)
			failed = true
			continue
		}
		var original, result any
		if err := json.Unmarshal([]byte(input), &original); err != nil {
			fmt.Fprintf(os.Stderr, "unmarshal original %%s: %%v\n", input, err)
			failed = true
			continue
		}
		if err := json.Unmarshal(out, &result); err != nil {
			fmt.Fprintf(os.Stderr, "unmarshal result %%s: %%v\n", string(out), err)
			failed = true
			continue
		}
		if !reflect.DeepEqual(original, result) {
			fmt.Fprintf(os.Stderr, "ROUND-TRIP MISMATCH\n  in:  %%s\n  out: %%s\n", input, string(out))
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`, goStringSliceElems(docs), rootType)
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}
	if err := writeTestGoMod(tmpDir, "round_trip_cases_test"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("round-trip cases failed:\n%s\nerror: %v", string(output), err)
	}
	if outputStr := programOutput(output); outputStr != "PASS" {
		t.Fatalf("round-trip cases output:\n%s", outputStr)
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

// TestAnnotationKeywordsReachTheDocComment covers the four annotation keywords
// the generator used to drop on the floor: "deprecated", "readOnly",
// "writeOnly" and "examples".
//
// pkg/schema.Schema held Title, Description and Default of the annotation
// vocabulary and nothing else, so the other four fell into Extensions and were
// never read. A property with "deprecated": true generated an ordinary field
// with no trace of the keyword, beside a sibling whose "description" became a
// doc comment correctly -- the annotation half of the vocabulary was half
// implemented, and which half you got depended on which keyword you wrote.
//
// A code generator is the consumer these were written for: they constrain
// nothing, so the only place their meaning can land is the generated source.
// "deprecated" is the one with an exact spelling rather than a chosen one --
// Go's convention is a paragraph beginning "Deprecated: ", which gopls,
// staticcheck and `go doc` all read -- so this checks the paragraph break too,
// not just the words.
//
// "plain" is the control, and it is the whole reason the keywords are held as
// pointers rather than bools: it writes false for all three, and an
// implementation that emitted on presence rather than on value would document a
// property as deprecated because the schema said it was not.
func TestAnnotationKeywordsReachTheDocComment(t *testing.T) {
	src := string(generateFromSchema(t, "testdata/schemas/regression/annotation_vocabulary.json"))

	for _, want := range []string{
		// The Go convention, and the blank comment line that makes it a
		// paragraph. Without the break these tools see no deprecation at all.
		"//\n\t// Deprecated: the schema marks this deprecated.\n\tLegacyID",
		// The description the field already had is still above it.
		"// The identifier this resource used to carry.",
		// examples, compacted, one per line, under the property that names them.
		"// Examples from the schema:\n\t//   \"abc-123\"\n\t//   \"def-456\"",
		"// Examples from the schema:\n\t//   1\n\t//   2\n\tCount",
		// readOnly and writeOnly say what the keyword means, since by default
		// the generated code does nothing else with them.
		`Read-only: the schema says "readOnly"`,
		`Write-only: the schema says "writeOnly"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated source does not contain %q:\n%s", want, src)
		}
	}

	// The controls. "plain" writes false three times and must read as a field
	// with nothing said about it; "untouched" writes nothing at all.
	for _, field := range []string{"Plain", "Untouched"} {
		idx := strings.Index(src, "\t"+field+" ")
		if idx < 0 {
			t.Fatalf("generated source declares no field %s:\n%s", field, src)
		}
		// Everything from the previous field's declaration to this one is this
		// field's comment block.
		start := strings.LastIndex(src[:idx], "`json:")
		if start < 0 {
			start = 0
		}
		block := src[start:idx]
		for _, unwanted := range []string{"Deprecated:", "Read-only:", "Write-only:", "Examples from the schema:"} {
			if strings.Contains(block, unwanted) {
				t.Errorf("field %s carries %q, and its schema asserts no annotation:\n%s", field, unwanted, block)
			}
		}
	}

	// And the default configuration changes nothing but comments. Neither
	// keyword may reach the decoder, the encoder or Validate unless the caller
	// asked for it.
	for _, unwanted := range []string{"read-only property may not be set", "_woKey"} {
		if strings.Contains(src, unwanted) {
			t.Errorf("the default configuration emitted %q; readOnly/writeOnly behaviour is --strict-read-write only:\n%s", unwanted, src)
		}
	}
}

// annotationKindCells is the position matrix issue #171 asked for: every kind of
// named type schemagen can generate, against the annotation vocabulary.
//
// It is a table rather than one assertion per keyword because the defect it
// pins is per-cell. Before #171 the two struct kinds carried all four keywords
// and the other seven carried none, while `description` reached all nine -- so
// the plumbing was there and the annotations were simply not wired into it, and
// a fix aimed at whichever kind was noticed first would have left the rest.
//
// Every kind is written twice in the schema: AnnX states all four keywords, and
// PlainX writes the three booleans as false. The control half is not decoration.
// The keywords are held as pointers precisely so that "absent" and "false" are
// distinguishable, and an implementation that emitted on presence rather than on
// value would mark a type deprecated because its schema said it was not.
type annotationKindCell struct {
	kind     string // the named-type kind, in the words the issue used
	typeName string // the type carrying the keywords
	control  string // the same kind, with the booleans written false
	// decl is the line the doc comment has to sit above. It differs between the
	// default and the big-int reading of the same $defs entry, so it is not
	// derived from typeName.
	decl string
	// bigInt marks the one kind that does not exist under the default
	// configuration: the same $defs entry is a plain int64 alias without it.
	bigInt bool
	// depOnlyDecl is the same kind again, from a $defs entry carrying a
	// description and "deprecated" and nothing else. It is where the paragraph
	// break is asserted, because it is the only place the break is this
	// template's doing: beside "examples" gofmt puts a blank comment line in on
	// its own account, to fence the indented block the example lines become, and
	// a template that passed the wrong precededByProse would still come out
	// looking right.
	depOnlyDecl string
}

var annotationKindCells = []annotationKindCell{
	{kind: "alias", typeName: "AnnAlias", control: "PlainAlias", decl: "type AnnAlias string",
		depOnlyDecl: "type DepAlias string"},
	{kind: "enum", typeName: "AnnEnum", control: "PlainEnum", decl: "type AnnEnum string",
		depOnlyDecl: "type DepEnum string"},
	{kind: "raw enum", typeName: "AnnRawEnum", decl: "type AnnRawEnum json.RawMessage",
		depOnlyDecl: "type DepRawEnum json.RawMessage"},
	{kind: "struct", typeName: "AnnStruct", control: "PlainStruct", decl: "type AnnStruct struct {",
		depOnlyDecl: "type DepStruct struct {"},
	{kind: "inferred alias", typeName: "AnnInferred", control: "PlainInferred", decl: "type AnnInferred struct {",
		depOnlyDecl: "type DepInferred struct {"},
	{kind: "type-only", typeName: "AnnTypeOnly", control: "PlainTypeOnly", decl: "type AnnTypeOnly struct {",
		depOnlyDecl: "type DepTypeOnly struct {"},
	{kind: "dynamic", typeName: "AnnDynamic", control: "PlainDynamic", decl: "type AnnDynamic struct {",
		depOnlyDecl: "type DepDynamic struct {"},
	{kind: "not", typeName: "AnnNot", control: "PlainNot", decl: "type AnnNot struct {",
		depOnlyDecl: "type DepNot struct {"},
	{kind: "annotation schema", typeName: "AnnRuntime", control: "PlainRuntime", decl: "type AnnRuntime struct {",
		depOnlyDecl: "type DepRuntime struct {"},
	{kind: "big-int alias", typeName: "AnnBigInt", control: "PlainBigInt", decl: "type AnnBigInt struct {",
		bigInt: true, depOnlyDecl: "type DepBigInt struct {"},
}

// TestAnnotationKeywordsReachEveryNamedTypeKind is issue #171's matrix, run.
//
// The big-int alias is the ninth kind and the only one that needs a
// configuration to exist, so it is read from the same document a second time
// under BigIntSupport -- where the $defs entry that was `type AnnBigInt int64`
// becomes the arbitrary-precision wrapper, a different template with a doc
// comment of its own to get wrong.
func TestAnnotationKeywordsReachEveryNamedTypeKind(t *testing.T) {
	const schemaPath = "testdata/schemas/regression/annotation_positions.json"
	defaultSrc := string(generateFromSchema(t, schemaPath))
	bigIntSrc := string(generateFromSchemaWithConfig(t, schemaPath, generator.Config{
		PackageName:   "testpkg",
		OmitEmpty:     true,
		BigIntSupport: true,
	}))

	for _, cell := range annotationKindCells {
		src := defaultSrc
		if cell.bigInt {
			src = bigIntSrc
		}
		t.Run(cell.kind, func(t *testing.T) {
			block := docCommentAbove(t, src, cell.decl)

			// One assertion per keyword, so a failure names the cell that moved
			// rather than "the annotation block changed".
			for keyword, want := range map[string]string{
				// The control for the whole matrix: description is what already
				// reached all nine kinds, and the contrast with it is what said
				// the plumbing existed and the annotations were not in it.
				"description": "// " + cell.typeName + " - ",
				"readOnly":    `Read-only: the schema says "readOnly"`,
				"writeOnly":   `Write-only: the schema says "writeOnly"`,
				"examples":    "Examples from the schema:",
				"deprecated":  "Deprecated: the schema marks this deprecated.",
			} {
				if !strings.Contains(block, want) {
					t.Errorf("%s: the %q comment above %s does not carry %s:\n%s",
						cell.kind, cell.typeName, cell.decl, keyword, block)
				}
			}

			// "Deprecated: " is the one with an exact spelling rather than a
			// chosen one, and being a paragraph of its own is half of it. gopls,
			// staticcheck and `go doc` all read the convention and all miss a
			// notice glued to the end of the paragraph above it.
			//
			// Asserted on the deprecated-only type rather than on this one. Where
			// "examples" is present, gofmt fences the indented block the example
			// lines become with blank comment lines of its own, and that fence
			// would satisfy this check whatever the template did.
			depBlock := docCommentAbove(t, src, cell.depOnlyDecl)
			if !strings.Contains(depBlock, "//\n// Deprecated: the schema marks this deprecated.") {
				t.Errorf("%s: the deprecation notice above %s is not its own paragraph, "+
					"so nothing that reads the Go convention will see it:\n%s",
					cell.kind, cell.depOnlyDecl, depBlock)
			}

			// And it is last, in both. A paragraph after it would be read as
			// part of the notice by `go doc`.
			for _, b := range []string{block, depBlock} {
				if got := strings.TrimSpace(b); !strings.HasSuffix(got, "// Deprecated: the schema marks this deprecated.") {
					t.Errorf("%s: the deprecation notice is not the last paragraph:\n%s", cell.kind, b)
				}
			}

			if cell.control == "" {
				return
			}
			controlDecl := strings.Replace(cell.decl, cell.typeName, cell.control, 1)
			controlBlock := docCommentAbove(t, src, controlDecl)
			for _, unwanted := range []string{"Deprecated:", "Read-only:", "Write-only:", "Examples from the schema:"} {
				if strings.Contains(controlBlock, unwanted) {
					t.Errorf("%s: %s carries %q, and its schema writes that keyword false:\n%s",
						cell.kind, cell.control, unwanted, controlBlock)
				}
			}
		})
	}

	// The whole point of the vocabulary is that it says nothing about any
	// document. Neither reading may have grown a check.
	for _, src := range []string{defaultSrc, bigIntSrc} {
		for _, unwanted := range []string{"read-only property may not be set", "_woKey"} {
			if strings.Contains(src, unwanted) {
				t.Errorf("the default configuration emitted %q; readOnly/writeOnly behaviour is --strict-read-write only", unwanted)
			}
		}
	}
}

// docCommentAbove returns the run of // lines immediately above decl.
//
// It walks back from the declaration rather than forward from the type name,
// because the name appears inside its own methods and inside every other type
// that mentions it, and a search that found one of those would assert against a
// comment belonging to something else.
func docCommentAbove(t *testing.T, src, decl string) string {
	t.Helper()
	idx := strings.Index(src, "\n"+decl)
	if idx < 0 {
		t.Fatalf("generated source declares no %q", decl)
	}
	lines := strings.Split(src[:idx+1], "\n")
	end := len(lines) - 1 // the empty element after the final newline
	start := end
	for start > 0 && strings.HasPrefix(lines[start-1], "//") {
		start--
	}
	return strings.Join(lines[start:end], "\n")
}

// TestStrictReadWriteChangesDecodeAndEncode states the contract of
// --strict-read-write, which is deliberately not a round-trip.
//
// It has its own program rather than a fixture because the two things it
// asserts are asymmetries: a document that goes in and does not come back, and
// a document that is refused outright. runValidationCases and the round-trip
// harness both mean "a document comes back as it went in", and every fixture in
// the tree leans on that -- teaching them about this flag would weaken the
// assertion everywhere to accommodate one caller. refuseRoundTripBreakingConfig
// is what makes that a test failure rather than a note.
//
// What the flag does *not* do is the other half of the contract, and it is
// asserted here too: Validate() gives the same answer under both settings.
// readOnly and writeOnly are the meta-data vocabulary in 2019-09 and 2020-12 and
// are annotations by definition, the official suite has no case for either, and
// a Validate that consulted one would be non-conformant with nothing in the
// corpus to say so.
func TestStrictReadWriteChangesDecodeAndEncode(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func main() {
	// A readOnly property present in the document is refused. JSON Schema
	// section 9.4 says an owning authority is expected to ignore or reject an
	// application's attempt to set one; this configuration rejects.
	for _, doc := range []string{
		` + "`" + `{"serverID":"srv-7"}` + "`" + `,
		` + "`" + `{"both":"b"}` + "`" + `,
		` + "`" + `{"untouched":"u","serverID":"srv-7"}` + "`" + `,
		// Even written as null: the property is present, which is what the
		// keyword is about, rather than what it was set to.
		` + "`" + `{"serverID":null}` + "`" + `,
	} {
		var v AnnotationVocabulary
		if err := json.Unmarshal([]byte(doc), &v); err == nil {
			fail("strict mode decoded a document setting a readOnly property: %s", doc)
		} else if !strings.Contains(err.Error(), "read-only property may not be set") {
			fail("decoding %s failed for the wrong reason: %v", doc, err)
		}
	}

	// Nothing else is refused. A property with readOnly:false, one with
	// writeOnly, and one carrying no annotation all decode as they always did --
	// the flag rejects one named set of keys and not a shape.
	for _, doc := range []string{
		` + "`" + `{}` + "`" + `,
		` + "`" + `{"plain":"p"}` + "`" + `,
		` + "`" + `{"secret":"hunter2"}` + "`" + `,
		` + "`" + `{"untouched":"u","legacyID":"abc-123","count":4}` + "`" + `,
	} {
		var v AnnotationVocabulary
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			fail("strict mode refused a document it has nothing to say about: %s: %v", doc, err)
		}
	}

	// A writeOnly property goes in and does not come out. That is the
	// asymmetry, stated rather than discovered: JSON Schema section 9.4 says
	// the value "is never present when the instance is retrieved from the
	// owning authority", and under this flag the generated type is that
	// authority's view.
	var v AnnotationVocabulary
	// "both" cannot arrive through the decoder here -- it is readOnly too, and
	// the loop above is what says so -- so it is set directly, which is the
	// position a server populating its own response is in.
	if err := json.Unmarshal([]byte(` + "`" + `{"secret":"hunter2","plain":"p","untouched":"u","count":4}` + "`" + `), &v); err != nil {
		fail("decoding the writeOnly document: %v", err)
	}
	both := "b"
	v.Both = &both

	out, err := json.Marshal(v)
	if err != nil {
		fail("marshaling: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		fail("re-reading the output: %v", err)
	}
	for _, gone := range []string{"secret", "both"} {
		if _, present := got[gone]; present {
			fail("strict mode wrote the writeOnly property %q: %s", gone, out)
		}
	}
	// And nothing else was taken with them. A delete that reached past the
	// keys it names would satisfy the two lines above and be wrong about
	// everything else in the document.
	for _, kept := range []string{"plain", "untouched", "count"} {
		if _, present := got[kept]; !present {
			fail("strict mode dropped %q, which carries no writeOnly: %s", kept, out)
		}
	}

	// Validate is not the flag's business, under either setting. Every one of
	// these documents satisfies the schema, and readOnly/writeOnly are
	// annotations that constrain none of them.
	for _, doc := range []string{
		` + "`" + `{}` + "`" + `,
		` + "`" + `{"secret":"hunter2"}` + "`" + `,
		` + "`" + `{"plain":"p","count":4}` + "`" + `,
	} {
		var w AnnotationVocabulary
		if err := json.Unmarshal([]byte(doc), &w); err != nil {
			fail("decoding %s: %v", doc, err)
		}
		if err := w.Validate(); err != nil {
			fail("Validate rejected %s, which the schema permits: %v", doc, err)
		}
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgramWithConfig(t,
		"testdata/schemas/regression/annotation_vocabulary.json",
		"strict_read_write_test",
		mainGo,
		generator.Config{PackageName: "testpkg", OmitEmpty: true, StrictReadWrite: true},
	)
}

// TestStrictReadWriteBindsWhereverThePropertyIs is issue #172's position matrix.
//
// --strict-read-write keys on a parent struct's property names, and until this
// was written it read the keyword off the property schema alone. So
// {"roInline":{"type":"string","readOnly":true}} bound and
// {"roViaRef":{"$ref":"#/$defs/ReadOnlyID"}} did not, though the two say the
// same thing about the same instance location and differ only in where the
// author wrote it. The chain and the object-level spellings are here for the
// same reason: each is one more way to say it that used to be dropped.
//
// The other half of the matrix is the boundary, and it is asserted rather than
// left implicit. Two things are on the far side of it.
//
// A readOnly array element and a readOnly map value generate no check,
// deliberately: the check keys on a property name and an element has none, and
// writeOnly has no coherent action there at all -- a property can be left out of
// an object, but an element cannot be left out of an array without changing its
// length, which is a thing minItems can see. Those positions are documentation,
// and the doc comment on the element's own type is where they are documented.
//
// And a keyword inside an anyOf branch generates none either. $ref and allOf
// bind whatever the document says, so what they state is stated of every
// instance; an anyOf branch applies only to the documents that match it, so a
// check keyed on one would refuse documents the schema never marked. See
// readWriteAtLocation.
//
// Running rather than reading the generated source is the point: what these
// keywords are worth is what the decoder and encoder do, and the test is a
// program that decodes and encodes.
func TestStrictReadWriteBindsWhereverThePropertyIs(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func main() {
	// Every spelling of "this property is readOnly" is refused, whether the
	// keyword sits on the property, on the schema it references, at the far end
	// of a chain of references, or on an object-typed definition's own level.
	for _, doc := range []string{
		` + "`" + `{"roInline":"a"}` + "`" + `,
		` + "`" + `{"roViaRef":"a"}` + "`" + `,
		` + "`" + `{"roViaChain":"a"}` + "`" + `,
		` + "`" + `{"roViaObject":{"n":"a"}}` + "`" + `,
		// An allOf branch binds unconditionally, exactly as a $ref does from
		// 2019-09 on, so it is read the same way.
		` + "`" + `{"roViaAllOf":"a"}` + "`" + `,
		// And the recursion that already worked: the property is on a nested
		// struct, which carries the check in its own UnmarshalJSON.
		` + "`" + `{"nested":{"sid":"s"}}` + "`" + `,
		` + "`" + `{"nestedList":[{"sid":"s"}]}` + "`" + `,
		// The oneOf group, issue #175. This property compiles to a sealed
		// interface rather than to a struct field, and the key lists are built
		// by walking the fields, so it was named in neither: the flag did
		// nothing at all here and said nothing about doing nothing.
		` + "`" + `{"roGroup":{"name":"n"}}` + "`" + `,
		` + "`" + `{"roGroup":{"id":3}}` + "`" + `,
	} {
		var v ReadWritePositions
		if err := json.Unmarshal([]byte(doc), &v); err == nil {
			fail("strict mode decoded a document setting a readOnly property: %s", doc)
		} else if !strings.Contains(err.Error(), "read-only property may not be set") {
			fail("decoding %s failed for the wrong reason: %v", doc, err)
		}
	}

	// The boundary. A readOnly array element and a readOnly map value are
	// documentation: there is no property name for the check to key on, so
	// nothing is refused, and that is stated here so a change to it is a test
	// failure rather than a surprise.
	//
	// The controls sit in the same list. A property writing readOnly:false, one
	// carrying no annotation at all, and a writeOnly property all decode, which
	// is what says the flag refuses a named set of keys and not a shape.
	for _, doc := range []string{
		` + "`" + `{}` + "`" + `,
		` + "`" + `{"roList":["a","b"]}` + "`" + `,
		` + "`" + `{"roMap":{"k":"a"}}` + "`" + `,
		` + "`" + `{"woList":["s"]}` + "`" + `,
		// An anyOf branch is the control for how far the applicator reach goes.
		// Which branch applies is the document's business, so a readOnly written
		// inside one binds nothing: a check keyed on it would refuse this
		// document, which the schema never marked.
		` + "`" + `{"roViaAnyOf":"a"}` + "`" + `,
		` + "`" + `{"roViaAnyOf":3}` + "`" + `,
		` + "`" + `{"nested":{"keep":"k"}}` + "`" + `,
		` + "`" + `{"plain":"p"}` + "`" + `,
		` + "`" + `{"untouched":"u"}` + "`" + `,
		` + "`" + `{"woInline":"s","woViaRef":"s"}` + "`" + `,
		// The group controls: a writeOnly group goes in, and a group whose
		// property states nothing is untouched in both directions.
		` + "`" + `{"woGroup":{"name":"n"}}` + "`" + `,
		` + "`" + `{"plainGroup":{"id":1}}` + "`" + `,
	} {
		var v ReadWritePositions
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			fail("strict mode refused a document it has nothing to say about: %s: %v", doc, err)
		}
	}

	// writeOnly, the same way round. Both spellings of the property go in and
	// do not come out; the array of writeOnly elements is untouched, for the
	// reason above.
	var v ReadWritePositions
	if err := json.Unmarshal([]byte(` + "`" + `{"woInline":"s1","woViaRef":"s2","woViaAllOf":"s4","woList":["s3"],"woGroup":{"name":"s5"},"plainGroup":{"id":9},"untouched":"u","plain":"p"}` + "`" + `), &v); err != nil {
		fail("decoding the writeOnly document: %v", err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		fail("marshaling: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		fail("re-reading the output: %v", err)
	}
	for _, gone := range []string{"woInline", "woViaRef", "woViaAllOf", "woGroup"} {
		if _, present := got[gone]; present {
			fail("strict mode wrote the writeOnly property %q: %s", gone, out)
		}
	}
	for _, kept := range []string{"woList", "untouched", "plain", "plainGroup"} {
		if _, present := got[kept]; !present {
			fail("strict mode dropped %q: %s", kept, out)
		}
	}

	// And none of it is a verdict. Every document here satisfies the schema
	// under either setting, because both keywords are annotations and Validate
	// does not consult them.
	for _, doc := range []string{
		` + "`" + `{}` + "`" + `,
		` + "`" + `{"roList":["a"],"roMap":{"k":"b"},"woList":["s"],"roViaAnyOf":"a"}` + "`" + `,
		` + "`" + `{"plain":"p","untouched":"u","woInline":"s"}` + "`" + `,
		` + "`" + `{"nested":{"keep":"k"}}` + "`" + `,
		` + "`" + `{"woGroup":{"id":1},"plainGroup":{"name":"n"}}` + "`" + `,
	} {
		var w ReadWritePositions
		if err := json.Unmarshal([]byte(doc), &w); err != nil {
			fail("decoding %s: %v", doc, err)
		}
		if err := w.Validate(); err != nil {
			fail("Validate rejected %s, which the schema permits: %v", doc, err)
		}
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgramWithConfig(t,
		"testdata/schemas/regression/read_write_positions.json",
		"strict_read_write_positions_test",
		mainGo,
		generator.Config{PackageName: "testpkg", OmitEmpty: true, StrictReadWrite: true},
	)
}

// TestReadWritePositionsAreDocumentationByDefault is the other setting of the
// same matrix, and it is what says the flag is the only thing that makes any of
// it happen.
//
// Under the default configuration none of the positions above may reach the
// decoder or the encoder -- not the property, not the reference, not the chain.
// The document round-trips instead, which the round-trip case asserts; this
// asserts that the generated source has nowhere for a rejection to come from.
func TestReadWritePositionsAreDocumentationByDefault(t *testing.T) {
	src := string(generateFromSchema(t, "testdata/schemas/regression/read_write_positions.json"))
	for _, unwanted := range []string{"read-only property may not be set", "_woKey", "_roKey"} {
		if strings.Contains(src, unwanted) {
			t.Errorf("the default configuration emitted %q; readOnly/writeOnly behaviour is --strict-read-write only:\n%s", unwanted, src)
		}
	}
	// The keywords still reach the reader, on the type that carries them. That
	// is the whole of what they do by default, and it is issue #171's half of
	// the same schema.
	if !strings.Contains(src, `Read-only: the schema says "readOnly"`) {
		t.Errorf("the default configuration dropped the readOnly doc comment entirely:\n%s", src)
	}
	// Including above the oneOf group, which carried no comment at all until
	// issue #175 -- OneOfDef held neither Description nor Annotations, so the
	// property's own prose was dropped along with the keywords. The interface
	// name is the marker because the field's tag is `json:"-"`: the group is
	// written by hand in MarshalJSON, so there is no property name in the line.
	for _, c := range []struct {
		marker string
		want   []string
		unwant []string
	}{
		{"isReadWritePositions_RoGroup", []string{"Chosen by the server.", `Read-only: the schema says "readOnly"`}, []string{"Write-only:"}},
		{"isReadWritePositions_WoGroup", []string{`Write-only: the schema says "writeOnly"`}, []string{"Read-only:"}},
		// The control: a group whose property says nothing gets nothing, which
		// is what makes the two above evidence rather than a comment the
		// template writes over every group.
		{"isReadWritePositions_PlainGroup", nil, []string{"Read-only:", "Write-only:", "Chosen by the server."}},
	} {
		block := docCommentAboveLine(t, src, c.marker, "`json:\"-\"`")
		for _, want := range c.want {
			if !strings.Contains(block, want) {
				t.Errorf("the comment above the %s field does not carry %q:\n%s", c.marker, want, block)
			}
		}
		for _, unwant := range c.unwant {
			if strings.Contains(block, unwant) {
				t.Errorf("the comment above the %s field carries %q, which its property does not state:\n%s", c.marker, unwant, block)
			}
		}
	}
}

// TestStrictReadWriteKeyListsAreSorted pins the order of the two key lists.
//
// It is a property of the generated source and not of what it does -- the
// decoder refuses the same documents whatever order the names are in -- but the
// lists are read by people diffing generated code, and the order stopped being
// automatic when the oneOf groups of issue #175 joined them: the fields are
// walked first and the groups after, so a struct carrying both came out sorted
// within each half and not across the two.
func TestStrictReadWriteKeyListsAreSorted(t *testing.T) {
	src := string(generateFromSchemaWithConfig(t, "testdata/schemas/regression/read_write_positions.json",
		generator.Config{PackageName: "testpkg", OmitEmpty: true, StrictReadWrite: true}))
	// Anchored on the root type's own methods. Every nested struct in this
	// document carries lists of its own, and the first in the file is
	// HasReadOnlyProperty's one-element one, which can show no ordering at all.
	for _, c := range []struct{ anchor, opener string }{
		{"func (r *ReadWritePositions) UnmarshalJSON", "_roKey := range []string{"},
		{"func (r ReadWritePositions) MarshalJSON", "_woKey := range []string{"},
	} {
		keys := stringListAfter(t, src, c.anchor, c.opener)
		if len(keys) < 2 {
			t.Fatalf("%s lists %d keys, which cannot show an ordering", c.opener, len(keys))
		}
		if !sort.StringsAreSorted(keys) {
			t.Errorf("%s is not in sorted order: %v", c.opener, keys)
		}
	}
}

// stringListAfter returns the quoted strings of the first Go slice literal
// opened by opener at or after anchor, which is expected to be a list of one
// element per line closed by a line whose first non-space character is "}".
func stringListAfter(t *testing.T, src, anchor, opener string) []string {
	t.Helper()
	from := strings.Index(src, anchor)
	if from < 0 {
		t.Fatalf("generated source has no %q", anchor)
	}
	rel := strings.Index(src[from:], opener)
	if rel < 0 {
		t.Fatalf("generated source has no %q after %q", opener, anchor)
	}
	idx := from + rel
	var keys []string
	for _, line := range strings.Split(src[idx+len(opener):], "\n")[1:] {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "}") {
			break
		}
		key, err := strconv.Unquote(strings.TrimSuffix(line, ","))
		if err != nil {
			t.Fatalf("the list after %q holds %q, which is not a Go string literal", opener, line)
		}
		keys = append(keys, key)
	}
	return keys
}

// TestStrictReadWriteDeclinesAConditionalReachedInPlace is issue #174, and the
// two keywords part company here.
//
// --strict-read-write must never refuse a document the schema accepts, and it
// did, for every way of reaching a conditional applicator through an in-place
// one. readWriteAtLocation declines to walk anyOf, oneOf and if/then/else on
// exactly that reasoning -- a branch contributes its annotations only to the
// documents that match it -- but the allOf merge had already folded the
// branch's property schemas into this struct's property map, so the key loop
// read `readOnly` off a `then` as though the property had been written that
// way and no walk was needed to get there.
//
// The distinction the merge erases is restored rather than the merge undone.
// Each branch property below still becomes a typed field -- the round-trip case
// on this schema is what says so -- and only the annotation is read through where
// the property came from.
//
// readOnly is read through it and writeOnly is not, and the whole readOnly half
// below is unchanged since #174: a refusal keyed on a branch the document did not
// select rejects a document the schema accepts, which is not recoverable inside
// the program. A writeOnly not stripped writes out the property whose whole
// meaning is "never present when the instance is retrieved" (§9.4) -- the shape a
// password or a token has -- with no diagnostic anywhere. Over-stripping loses a
// field visibly and the caller can turn the flag off; under-stripping leaks
// silently. conditionalReachAt is where that asymmetry is argued in full, and it
// is a policy this flag's caller chose rather than a reading of §7.7.1: Validate
// is unmoved by any of it, which the last block here asserts.
//
// The RoBinds*/WoBinds* half is not decoration. $ref and allOf bind on every
// valid instance and issue #172 established that they feed the key list; a fix
// that answered #174 by reading fewer of them would have traded a false
// rejection for a false acceptance, which is the worse of the two. roBindsBoth
// is the sharp case: the parent declares it readOnly and a `then` branch retypes
// it, so a rule that keyed on "a conditional touched this property" rather than
// on which schema said what would drop a check the schema states outright.
func TestStrictReadWriteDeclinesAConditionalReachedInPlace(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func main() {
	// The false rejections. Every one of these documents is accepted by the
	// schema and was refused by the generated decoder: the property is named
	// only inside a branch that this document does not select, or does not
	// select alone, so nothing marked it readOnly.
	for _, doc := range []string{
		` + "`" + `{"roCondThen":"x"}` + "`" + `,
		` + "`" + `{"roCondElse":"x"}` + "`" + `,
		` + "`" + `{"roCondAnyOf":"x"}` + "`" + `,
		` + "`" + `{"roCondOneOf":"x","pickB":1}` + "`" + `,
		// Through a $ref into the conditional rather than an inline one. The
		// hop is what makes the merge see a conditional at all, so both hops
		// are written out.
		` + "`" + `{"roCondThenViaRef":"x"}` + "`" + `,
		// The same defect one level down, where a property-level anyOf of two
		// object branches is merged into a struct of its own. That merge is a
		// different function from the allOf one and had the same hole.
		` + "`" + `{"condObject":{"b":1,"roCondNested":"x"}}` + "`" + `,
		// The two positions issue #174 reports as unaffected, asserted rather
		// than assumed: a dependentSchemas branch and a ` + "`" + `not` + "`" + ` both name a
		// property that no field is ever built for.
		` + "`" + `{"roCondDependent":"x"}` + "`" + `,
		` + "`" + `{"roCondNot":"x"}` + "`" + `,
		// And the document the branch *does* select. A static list of property
		// names cannot say "readOnly when mode is present", so the flag under-
		// enforces here rather than refusing on a condition it cannot evaluate.
		// That is the deliberate direction -- a missing check over a false
		// rejection -- and it is written down so that changing it is a choice.
		` + "`" + `{"mode":"m","roCondThen":"x"}` + "`" + `,
	} {
		var v ReadWriteConditionalPositions
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			fail("strict mode refused a document the schema accepts: %s: %v", doc, err)
		}
	}

	// The other side, and the reason the merge is read through rather than
	// ignored: an in-place applicator binds on every valid instance, so these
	// four are still refused.
	for _, doc := range []string{
		` + "`" + `{"roBindsInline":"a"}` + "`" + `,
		` + "`" + `{"roBindsViaRef":"a"}` + "`" + `,
		` + "`" + `{"roBindsViaAllOf":"a"}` + "`" + `,
		// Declared readOnly by the parent and retyped by a conditional branch.
		` + "`" + `{"roBindsBoth":"a"}` + "`" + `,
		// The same, with the unconditional half coming from an allOf branch
		// rather than from the parent. Those are two different places in the
		// merge and each has to record what it contributed, or the branch's
		// keyword is lost the moment a conditional mentions the property.
		` + "`" + `{"roBindsAllOfAndThen":"a"}` + "`" + `,
		// And the unconditional half behind a $ref, which is the one the
		// property schema itself does not state -- the doc comment does not
		// follow a reference (#172) but the key list must, so this is the only
		// case where the walk, and not the annotation read beside it, is what
		// keeps the check.
		` + "`" + `{"roBindsRefAndThen":"a"}` + "`" + `,
	} {
		var v ReadWriteConditionalPositions
		if err := json.Unmarshal([]byte(doc), &v); err == nil {
			fail("strict mode decoded a document setting a readOnly property: %s", doc)
		} else if !strings.Contains(err.Error(), "read-only property may not be set") {
			fail("decoding %s failed for the wrong reason: %v", doc, err)
		}
	}

	// writeOnly is the same reach asked the other way round, and it is answered
	// the other way round. A property a branch alone marks writeOnly is stripped,
	// exactly as one an allOf branch marks is; see conditionalReachAt for the
	// argument, which is that the two keywords fail in opposite directions and
	// only one of them can leak.
	//
	// The two readOnly properties in the same document are the control that says
	// this is keyed on the keyword and not on "a conditional touched this
	// property": both are contributed by a branch, neither is marked writeOnly,
	// and both come back out.
	var v ReadWriteConditionalPositions
	in := ` + "`" + `{"pickA":1,"woBindsViaAllOf":"a","woCondThen":"b","woCondAnyOf":"c",` +
		`"woCondGroup":{"id":4},"roCondThen":"keep","roCondAnyOf":"keep2"}` + "`" + `
	if err := json.Unmarshal([]byte(in), &v); err != nil {
		fail("decoding the writeOnly document: %v", err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		fail("marshaling: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		fail("re-reading the output: %v", err)
	}
	if _, present := got["woBindsViaAllOf"]; present {
		fail("strict mode wrote a property an allOf branch marks writeOnly: %s", out)
	}
	for _, gone := range []string{"woCondThen", "woCondAnyOf", "woCondGroup"} {
		if _, present := got[gone]; present {
			fail("strict mode wrote %q, which a conditional branch marks writeOnly: %s", gone, out)
		}
	}
	for _, kept := range []string{"pickA", "roCondThen", "roCondAnyOf"} {
		if _, present := got[kept]; !present {
			fail("strict mode dropped %q, which nothing marks writeOnly: %s", kept, out)
		}
	}

	// None of it is a verdict, under either setting: both keywords are
	// annotations and Validate does not consult them.
	for _, doc := range []string{
		` + "`" + `{"pickA":1}` + "`" + `,
		` + "`" + `{"pickB":2,"roCondOneOf":"x","roCondAnyOf":"y","woCondAnyOf":"z"}` + "`" + `,
		` + "`" + `{"mode":"m","pickA":1,"roCondThen":"x","woCondThen":"y","condObject":{"a":1,"roCondNested":"n"}}` + "`" + `,
	} {
		var w ReadWriteConditionalPositions
		if err := json.Unmarshal([]byte(doc), &w); err != nil {
			fail("decoding %s: %v", doc, err)
		}
		if err := w.Validate(); err != nil {
			fail("Validate rejected %s, which the schema permits: %v", doc, err)
		}
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgramWithConfig(t,
		"testdata/schemas/regression/read_write_conditional_positions.json",
		"strict_read_write_conditional_test",
		mainGo,
		generator.Config{PackageName: "testpkg", OmitEmpty: true, StrictReadWrite: true},
	)
}

// TestConditionalReadWriteIsNotDocumentedUnconditionally is the cosmetic half
// of issue #174, and it is the same reading asked under the default setting.
//
// A merged field took its doc comment from whatever the merge left in the
// property map, so a `then` branch's readOnly produced an unconditional
// "Read-only:" paragraph on a field the schema marks readOnly only sometimes.
// The comment and the key list are now taken from one reading of where the
// property came from, which is what stops the generated type from documenting
// one contract and enforcing another.
func TestConditionalReadWriteIsNotDocumentedUnconditionally(t *testing.T) {
	src := string(generateFromSchema(t, "testdata/schemas/regression/read_write_conditional_positions.json"))

	// Nothing behavioural under the default configuration, whatever the merge
	// produced. This is the same control the other matrix carries.
	for _, unwanted := range []string{"read-only property may not be set", "_woKey", "_roKey"} {
		if strings.Contains(src, unwanted) {
			t.Errorf("the default configuration emitted %q; readOnly/writeOnly behaviour is --strict-read-write only:\n%s", unwanted, src)
		}
	}

	for _, c := range []struct {
		jsonName string
		want     string
	}{
		// Contributed only by a branch that binds on some documents: the
		// keyword describes those documents, and the field's comment describes
		// all of them, so it may not be said here.
		{"roCondThen", ""},
		{"roCondElse", ""},
		{"roCondAnyOf", ""},
		{"roCondOneOf", ""},
		{"roCondThenViaRef", ""},
		{"woCondThen", ""},
		{"woCondAnyOf", ""},
		{"roCondNested", ""},
		// Not a conditional case: the doc comment deliberately stops at the
		// property schema, so a keyword behind a $ref is documented on the
		// referenced type instead. The check on it is still emitted, which the
		// strict test above is what says.
		{"roBindsRefAndThen", ""},
		// Written on the property itself or contributed by an allOf branch,
		// which binds on every valid instance. These are the control: without
		// them a fix that emitted the paragraph nowhere would pass.
		{"roBindsInline", `Read-only: the schema says "readOnly"`},
		{"roBindsViaAllOf", `Read-only: the schema says "readOnly"`},
		{"woBindsViaAllOf", `Write-only: the schema says "writeOnly"`},
		// Stated by the parent and merely retyped by a branch. The keyword is
		// the parent's and stays.
		{"roBindsBoth", `Read-only: the schema says "readOnly"`},
		{"roBindsAllOfAndThen", `Read-only: the schema says "readOnly"`},
	} {
		// Looking the field up by its struct tag is also what says the merge
		// still happened: a branch-contributed property that stopped becoming a
		// field would have no tag to find, and this fails rather than passing
		// vacuously. Without the merge the value would land in the overflow map,
		// where it still round-trips -- so the round-trip case alone cannot see
		// the difference.
		block := docCommentAboveLine(t, src, "`json:\""+c.jsonName+",omitempty\"`")
		switch {
		case c.want == "":
			for _, unwanted := range []string{"Read-only:", "Write-only:"} {
				if strings.Contains(block, unwanted) {
					t.Errorf("the comment above %q carries %q, which only a conditional branch states:\n%s",
						c.jsonName, unwanted, block)
				}
			}
		case !strings.Contains(block, c.want):
			t.Errorf("the comment above %q does not carry %q:\n%s", c.jsonName, c.want, block)
		}
	}

	// The two issues crossing: a property a conditional branch contributes,
	// carrying writeOnly beside a oneOf. It compiles to the sealed interface of
	// issue #175, which now carries the annotations it did not, and it is read
	// through the same provenance the fields are -- so the keyword the `then`
	// branch states is not documented of every document either.
	group := docCommentAboveLine(t, src,
		"isReadWriteConditionalPositions_WoCondGroup", "`json:\"-\"`")
	for _, unwanted := range []string{"Read-only:", "Write-only:"} {
		if strings.Contains(group, unwanted) {
			t.Errorf("the comment above the woCondGroup field carries %q, which only a conditional branch states:\n%s",
				unwanted, group)
		}
	}
}

// TestAnnotationReachThroughApplicators is the doc-comment half of issue #187,
// and the assertion that it answers the reach question the way
// --strict-read-write already did.
//
// A field's comment was read off the property node alone, so an annotation
// written one allOf branch below it -- which binds on every valid instance, and
// which the merge flattens away rather than leaving anywhere else to document --
// reached nothing: {"f":{"allOf":[{"description":"prose","readOnly":true}]}}
// produced a bare field beside a strict-mode check that enforced the same
// readOnly. The *Cond cells are the other direction and are equally the point: a
// branch that binds on some documents contributes its annotations to those
// documents, so a comment that carries them describes the wrong type.
func TestAnnotationReachThroughApplicators(t *testing.T) {
	src := string(generateFromSchema(t, "testdata/schemas/regression/annotation_reach_positions.json"))

	// Under the default configuration the whole vocabulary is a comment. This
	// is the control the sibling matrices carry.
	for _, unwanted := range []string{"read-only property may not be set", "_woKey", "_roKey"} {
		if strings.Contains(src, unwanted) {
			t.Errorf("the default configuration emitted %q; readOnly/writeOnly behaviour is --strict-read-write only:\n%s", unwanted, src)
		}
	}

	for _, c := range []struct {
		jsonName string
		want     []string
		unwanted []string
	}{
		// Behind an unconditional applicator on the property. The allOf is
		// flattened into the field's type, so this comment is the only place
		// left for what its branch says.
		{jsonName: "annViaAllOf", want: []string{
			"Prose written on an allOf branch of the property.",
			`Read-only: the schema says "readOnly"`,
			"Examples from the schema:",
			"Deprecated: the schema marks this deprecated.",
		}},
		// Two levels down, because everything below an unconditional applicator
		// is unconditional too.
		{jsonName: "annViaNestedAllOf", want: []string{
			"Prose two allOf levels below the property.",
			`Write-only: the schema says "writeOnly"`,
			"Deprecated: the schema marks this deprecated.",
		}},
		// Contributed by an allOf branch of the *object*, which the merge folds
		// into the property map. That route already worked and is here so a fix
		// that moved the reading could not lose it.
		{jsonName: "annViaMergedAllOf", want: []string{
			"Prose an allOf branch states about a merged property.",
			"Deprecated: the schema marks this deprecated.",
		}},
		// The one deliberate stop. A $ref survives into the generated source as
		// this field's type, and AnnotatedString carries the comment -- asserted
		// below, so this cell is a relocation and not a loss.
		{jsonName: "annViaRef", unwanted: []string{
			"Prose on a referenced type", "Read-only:", "Deprecated:",
		}},
		// Stated only by a branch that binds on the documents that match it.
		{jsonName: "annCondThen", unwanted: []string{
			"Prose only a then branch states.", "Deprecated:", "Examples from the schema:",
		}},
		{jsonName: "annCondAnyOf", unwanted: []string{
			"Prose only an anyOf branch states.", "Deprecated:",
		}},
		// Nothing said about it anywhere, which is what says the paragraphs
		// above are read off a schema rather than written over every field.
		{jsonName: "annPlain", unwanted: []string{
			"Read-only:", "Write-only:", "Deprecated:", "Examples from the schema:",
		}},
	} {
		// By struct tag, so a property that stopped becoming a field fails here
		// rather than passing vacuously: the round-trip case cannot see that
		// difference, since such a value still travels through the overflow map.
		block := docCommentAboveLine(t, src, "`json:\""+c.jsonName+",omitempty\"`")
		for _, want := range c.want {
			if !strings.Contains(block, want) {
				t.Errorf("the comment above %q does not carry %q:\n%s", c.jsonName, want, block)
			}
		}
		for _, unwanted := range c.unwanted {
			if strings.Contains(block, unwanted) {
				t.Errorf("the comment above %q carries %q, which no schema states of every instance:\n%s",
					c.jsonName, unwanted, block)
			}
		}
	}

	// A property that compiles to a sealed interface instead of a field carries
	// the same comment for the same reasons (#175), and has to answer the reach
	// question the same way -- prose and annotations together, since a group
	// showing a branch's prose while withholding its deprecation would document
	// two things at once.
	for _, c := range []struct {
		field    string
		want     []string
		unwanted []string
	}{
		{field: "isAnnotationReachPositions_AnnGroupPlain", want: []string{
			"Prose on a oneOf group written on the property.",
			"Deprecated: the schema marks this deprecated.",
		}},
		{field: "isAnnotationReachPositions_AnnCondGroup", unwanted: []string{
			"Prose only a then branch states about a group.", "Deprecated:",
		}},
	} {
		block := docCommentAboveLine(t, src, c.field, "`json:\"-\"`")
		for _, want := range c.want {
			if !strings.Contains(block, want) {
				t.Errorf("the comment above the %s field does not carry %q:\n%s", c.field, want, block)
			}
		}
		for _, unwanted := range c.unwanted {
			if strings.Contains(block, unwanted) {
				t.Errorf("the comment above the %s field carries %q, which only a conditional branch states:\n%s",
					c.field, unwanted, block)
			}
		}
	}

	annotationReachDocDetails(t, src)
}

// annotationReachDocDetails is the tail of TestAnnotationReachThroughApplicators:
// the two assertions that are about one declaration each rather than about a
// cell of the matrix.
func annotationReachDocDetails(t *testing.T, src string) {
	t.Helper()

	// The other half of the annViaRef cell: the keywords are not dropped, they
	// are written where the reference put them.
	refBlock := docCommentAbove(t, src, "type AnnotatedString string")
	for _, want := range []string{
		"Prose on a referenced type",
		`Read-only: the schema says "readOnly"`,
		"Deprecated: the schema marks this deprecated.",
	} {
		if !strings.Contains(refBlock, want) {
			t.Errorf("the comment above AnnotatedString does not carry %q, so the field's silence about it loses it:\n%s",
				want, refBlock)
		}
	}

	// "Deprecated: " has to be a paragraph of its own, or gopls, staticcheck and
	// `go doc` all miss it. Asserted on annViaNestedAllOf: it is the folded cell
	// with no "examples", and beside "examples" gofmt writes blank comment lines
	// of its own to fence the indented block, which would satisfy this check
	// whatever the annotation writer did.
	nested := docCommentAboveLine(t, src, "`json:\"annViaNestedAllOf,omitempty\"`")
	if !strings.Contains(nested, "//\n\t// Deprecated: the schema marks this deprecated.") {
		t.Errorf("the deprecation notice above annViaNestedAllOf is not its own paragraph:\n%s", nested)
	}
}

// TestStrictReadWriteAgreesWithTheDocCommentThroughAnAllOf is the crossing
// issue #187 reports: the flag reached through an allOf on the property and the
// comment did not, so a generated type enforced a readOnly whose existence its
// own documentation denied. One walk answers both now, so the two lists below
// are read off the same source and have to name the same properties.
func TestStrictReadWriteAgreesWithTheDocCommentThroughAnAllOf(t *testing.T) {
	const schemaPath = "testdata/schemas/regression/annotation_reach_positions.json"
	strictSrc := string(generateFromSchemaWithConfig(t, schemaPath, generator.Config{
		PackageName: "testpkg", OmitEmpty: true, StrictReadWrite: true,
	}))

	readOnlyKeys := strictKeyList(t, strictSrc, "_roKey")
	writeOnlyKeys := strictKeyList(t, strictSrc, "_woKey")

	for _, c := range []struct {
		jsonName string
		keys     map[string]bool
		comment  string
		bound    bool
		// documented is normally bound. It differs for exactly one position --
		// a $ref written on the property -- and that difference is the subject
		// of the cell, not an exemption from it.
		documented bool
	}{
		// The issue's own shape: an allOf on the property. The flag reached it,
		// the comment did not, and now both do.
		{jsonName: "annViaAllOf", keys: readOnlyKeys, comment: `Read-only: the schema says "readOnly"`,
			bound: true, documented: true},
		{jsonName: "annViaNestedAllOf", keys: writeOnlyKeys, comment: `Write-only: the schema says "writeOnly"`,
			bound: true, documented: true},
		// The conditional control, from the other issue this one has to agree
		// with: a `then` branch contributes its annotations to the documents it
		// matches, so neither the check nor the paragraph may be here (#174).
		{jsonName: "annCondThen", keys: readOnlyKeys, comment: `Read-only: the schema says "readOnly"`,
			bound: false, documented: false},
		// The one place the two readings are meant to differ, asserted rather
		// than left to be discovered. The check has nowhere but this struct to
		// live, so the flag follows the reference; the comment has somewhere
		// better, because the reference survives into the generated source as
		// this field's type and TestAnnotationReachThroughApplicators is what
		// says AnnotatedString carries the paragraph. Documenting it here as
		// well would print the same schema's comment twice, and printing it
		// *only* here would take the borrowed keyword without the borrowed
		// prose.
		{jsonName: "annViaRef", keys: readOnlyKeys, comment: `Read-only: the schema says "readOnly"`,
			bound: true, documented: false},
	} {
		block := docCommentAboveLine(t, strictSrc, "`json:\""+c.jsonName+",omitempty\"`")
		if documented := strings.Contains(block, c.comment); documented != c.documented {
			t.Errorf("%s: the doc comment carries %q = %v, want %v:\n%s",
				c.jsonName, c.comment, documented, c.documented, block)
		}
		if bound := c.keys[c.jsonName]; bound != c.bound {
			t.Errorf("%s: strict mode bound = %v, want %v (keys: readOnly %v, writeOnly %v)",
				c.jsonName, bound, c.bound, readOnlyKeys, writeOnlyKeys)
		}
	}
}

// strictKeyList reads the JSON property names out of one of the two lists
// --strict-read-write emits, by the loop variable that names it.
//
// Parsed rather than searched for as a substring: every one of these names is
// also a struct tag somewhere in the same file, so `strings.Contains` cannot
// tell a property the flag bound on from one it merely typed -- a check that
// would have passed for every cell here, in both directions.
func strictKeyList(t *testing.T, src, loopVar string) map[string]bool {
	t.Helper()
	marker := "for _, " + loopVar + " := range []string{"
	start := strings.Index(src, marker)
	if start < 0 {
		return map[string]bool{}
	}
	rest := src[start+len(marker):]
	end := strings.Index(rest, "}")
	if end < 0 {
		t.Fatalf("the %s list is not terminated:\n%s", loopVar, rest)
	}
	keys := map[string]bool{}
	for _, line := range strings.Split(rest[:end], "\n") {
		if name := strings.Trim(strings.TrimSpace(line), `",`); name != "" {
			keys[name] = true
		}
	}
	return keys
}

// TestDefaultsReachThroughApplicators is issue #186, run.
//
// "default" was read off the property node and nowhere else, so it survived
// being written inline and vanished behind either unconditional binder -- and a
// $ref almost always puts it behind one, since the reference becomes the field's
// own named type. Following the reference was necessary to find the keyword and
// not sufficient to write it: defaultToGoLiteral answers from the Go type name,
// which by then is `*DefaultedString` rather than `*string`.
//
// The *Cond fields are the same carve-out the annotations get, and here it is a
// behavioural claim rather than a cosmetic one: SetDefaults writing a value only
// a `then` branch states puts it on every document, including the ones the
// condition excludes.
func TestDefaultsReachThroughApplicators(t *testing.T) {
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
	var v AnnotationReachPositions
	if err := json.Unmarshal([]byte(` + "`" + `{}` + "`" + `), &v); err != nil {
		fail("decoding the empty document: %v", err)
	}
	v.SetDefaults()

	// Every unconditional route to the keyword. The first is the control that
	// already worked; the rest are the ones issue #186 reports, each named for
	// the applicator chain that carries it.
	for _, c := range []struct {
		name string
		got  *string
		want string
	}{
		{"dfltInline", v.DfltInline, "inline"},
		{"dfltViaMergedAllOf", v.DfltViaMergedAllOf, "merged"},
		// An allOf branch of the object states one and a ` + "`" + `then` + "`" + ` branch states
		// another. Only the branch that binds on every document may be read.
		{"dfltBindsBoth", v.DfltBindsBoth, "unconditional"},
		// Two unconditional statements about the same location, one on the
		// property and one an allOf branch below it. The nearest wins.
		{"dfltNearestWins", v.DfltNearestWins, "own"},
	} {
		if c.got == nil {
			fail("%s: SetDefaults wrote nothing, want %q", c.name, c.want)
		} else if *c.got != c.want {
			fail("%s: SetDefaults wrote %q, want %q", c.name, *c.got, c.want)
		}
	}

	// The same, where the applicator has left a named type between the struct
	// field and the scalar the default is written in.
	for _, c := range []struct {
		name string
		got  *string
		want string
	}{
		{"dfltViaRef", (*string)(v.DfltViaRef), "viaRef"},
		{"dfltViaRefChain", (*string)(v.DfltViaRefChain), "viaRef"},
		{"dfltViaAllOf", (*string)(v.DfltViaAllOf), "viaAllOf"},
		{"dfltViaAllOfRef", (*string)(v.DfltViaAllOfRef), "viaRef"},
		{"dfltViaNestedAllOf", (*string)(v.DfltViaNestedAllOf), "viaNested"},
		// A $defs entry whose allOf names itself. The walk that finds the
		// keyword has to reach the branch beside the cycle and stop at the
		// cycle -- without the visited set it has no iteration that ends,
		// and the generator does not return at all.
		{"dfltViaCycle", (*string)(v.DfltViaCycle), "cycle"},
	} {
		if c.got == nil {
			fail("%s: SetDefaults wrote nothing, want %q", c.name, c.want)
		} else if *c.got != c.want {
			fail("%s: SetDefaults wrote %q, want %q", c.name, *c.got, c.want)
		}
	}

	// A required property is not a pointer, so the zero value is the only
	// "unset" there is, and it takes a different arm of the writer.
	if string(v.DfltRequiredViaRef) != "viaRef" {
		fail("dfltRequiredViaRef: SetDefaults wrote %q, want %q", string(v.DfltRequiredViaRef), "viaRef")
	}

	// The other three scalars a default can be written in, each behind a $ref.
	if v.DfltIntViaRef == nil || int64(*v.DfltIntViaRef) != 7 {
		fail("dfltIntViaRef: %v, want 7", v.DfltIntViaRef)
	}
	if v.DfltNumberViaRef == nil || float64(*v.DfltNumberViaRef) != 1.5 {
		fail("dfltNumberViaRef: %v, want 1.5", v.DfltNumberViaRef)
	}
	if v.DfltBoolViaRef == nil || !bool(*v.DfltBoolViaRef) {
		fail("dfltBoolViaRef: %v, want true", v.DfltBoolViaRef)
	}

	// A default equal to the zero value still has to be written into a pointer
	// field: nil is what "absent" looks like there, so "" is a value and not an
	// absence. This is the only cell that can tell the pointer shape from the
	// bare one, since every other default here is non-zero.
	if v.DfltEmptyViaRef == nil {
		fail("dfltEmptyViaRef: SetDefaults wrote nothing; a nil pointer is distinguishable from a present empty string")
	} else if string(*v.DfltEmptyViaRef) != "" {
		fail("dfltEmptyViaRef: SetDefaults wrote %q, want the empty string", string(*v.DfltEmptyViaRef))
	}

	// Nothing states one about these unconditionally, so nothing may be
	// written. The four conditional ones are the carve-out; dfltObjectViaRef is
	// a default whose target is a struct, which no conversion of a JSON value
	// reaches; dfltMismatchViaRef states a string default for an integer type,
	// which has no literal either; and dfltNone is the plain control.
	for _, c := range []struct {
		name string
		set  bool
	}{
		{"dfltCondThen", v.DfltCondThen != nil},
		{"dfltCondElse", v.DfltCondElse != nil},
		{"dfltCondAnyOf", v.DfltCondAnyOf != nil},
		{"dfltCondOneOf", v.DfltCondOneOf != nil},
		{"dfltObjectViaRef", v.DfltObjectViaRef != nil},
		{"dfltMismatchViaRef", v.DfltMismatchViaRef != nil},
		// A $defs entry with no "type" compiles to an alias over the empty
		// interface. The conversion into it does compile, so nothing but the
		// scalar test stops SetDefaults from writing a boxed value into a field
		// whose zero test cannot tell it from any other.
		{"dfltAnyViaRef", v.DfltAnyViaRef != nil},
		// A multi-type $defs entry compiles to a wrapper struct, and its
		// default is a JSON string -- so a pass that answered "string" for a
		// declaration it could not read would produce a conversion of a string
		// literal into a struct, which is the shape that does not compile.
		{"dfltMultiTypeViaRef", !v.DfltMultiTypeViaRef.IsZero()},
		{"dfltNone", v.DfltNone != nil},
	} {
		if c.set {
			fail("%s: SetDefaults wrote a value no schema states of every instance", c.name)
		}
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t,
		"testdata/schemas/regression/annotation_reach_positions.json",
		"annotation_reach_defaults_test",
		mainGo,
	)
}

// docCommentAboveLine returns the run of comment lines immediately above the
// first line of src containing every one of markers.
//
// docCommentAbove keys on a whole declaration line, which a struct field cannot
// offer: gofmt aligns the fields of a struct with padding nobody writes, so the
// text of the line depends on the longest field name beside it, and a marker
// spanning two columns of it would stop matching when an unrelated field is
// renamed. Each marker is therefore one run of characters gofmt does not touch
// -- a struct tag, a type name -- and a line is identified by carrying all of
// them rather than by any one being unique in the file.
func docCommentAboveLine(t *testing.T, src string, markers ...string) string {
	t.Helper()
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		matched := true
		for _, marker := range markers {
			if !strings.Contains(line, marker) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		start := i
		for start > 0 && strings.HasPrefix(strings.TrimSpace(lines[start-1]), "//") {
			start--
		}
		return strings.Join(lines[start:i], "\n")
	}
	t.Fatalf("generated source has no line containing all of %q:\n%s", markers, src)
	return ""
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
			// A $ref to an empty schema: its materialized type is one Go forbids
			// methods on, so it asserts nothing and must accept anything. Here
			// because the emitted dispatch would not compile if the pass that
			// notices a type carries no Validate stopped noticing.
			`{"tt":{"anything":1}}`, `{"tt":null}`,
			// A bare `format` with no type. It has a type and a Validate now
			// (issue #106), but this fixture declares 2020-12, whose default
			// meta-schema makes `format` an annotation -- so the Validate
			// asserts nothing here and every instance satisfies the bucket,
			// malformed address included. The assertion side of the same
			// schema is TestUntypedFormatIsAssertedOnStringsOnly, which asks
			// for it; the draft gate itself is
			// TestFormatPostureFollowsTheDialect.
			`{"uu":"1.2.3.4"}`, `{"uu":"not-an-ip"}`,
			`{"uu":5}`, `{"uu":{"a":1}}`, `{"uu":[1]}`, `{"uu":true}`, `{"uu":null}`,
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
	runGeneratedMainProgramWithConfig(t,
		"testdata/schemas/regression/format_alias_positions.json",
		"format_alias_positions_test",
		mainGo,
		formatAssertingConfig(),
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
	runGeneratedMainProgramWithConfig(t,
		"testdata/schemas/regression/format_alias_root.json",
		"format_alias_root_test",
		mainGo,
		formatAssertingConfig(),
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
	runGeneratedMainProgramWithConfig(t,
		"testdata/schemas/regression/format_map_values.json",
		"format_map_values_test",
		mainGo,
		formatAssertingConfig(),
	)
}

// TestDateTimeAcceptsTheLowerCaseSpelling is issue #264.
//
// RFC 3339 section 5.6 gives date-time as full-date "T" full-time, and its ABNF
// carries a NOTE saying the "T" and the offset's "Z" "may alternatively be lower
// case 't' or 'z' respectively". time.Time is read from a layout, and a layout
// matches both literally, so {"req":"2020-01-02t03:04:05z"} came back as
//
//	cannot parse "t03:04:05z" as "T"
//
// -- a document the format permits, refused at decode.
//
// The schema is draft-07 and the configuration is the plain one, deliberately.
// This was never confined to --format-assertion: drafts 3, 4, 6, 7 and v1 assert
// `format` by default, and assertion is what restores the Go type mapping, so
// the *default* posture on five of the seven dialects rejected the document. A
// test written under formatAssertingConfig would have proved a smaller claim.
//
// It also cannot be left to the official suite. Its optional/format/date-time.json
// does carry the lower case case ("case-insensitive T and Z", marked valid), but
// its schema is a bare {"format":"date-time"} with no "type" -- which resolves to
// the string wrapper of issue #106 and never reaches time.Time at all. Every
// position below states the type, which is what takes the mapping, and none of
// the 2255 group schemas in the pinned suite does. The external gate is green on
// this defect and cannot be made to see it.
//
// Every position is here for the reason the integer ones are in issue #90:
// fixing the scalar field alone would leave an array element rejecting what the
// field accepts, which is the same defect one level down.
//
// The rejections at the end are the other half, and they are what a fix that
// merely loosened the parse would fail. Only the case of those two characters
// moves; a value that is not an RFC 3339 date-time for any other reason is
// refused exactly as before.
func TestDateTimeAcceptsTheLowerCaseSpelling(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// Every date-time is written in the lower case spelling, at every position that
// reaches a time.Time through encoding/json rather than through a method.
const lower = ` + "`" + `{"aliased":"2020-01-02t03:04:05z","bag":{"k":"2020-01-02t03:04:05z"},"chained":"2020-01-02t03:04:05z","grid":[["2020-01-02t03:04:05z"]],"list":["2020-01-02t03:04:05z"],"mp":{"k":"2020-01-02t03:04:05z"},"namedlist":["2020-01-02t03:04:05z"],"nest":{"inner":"2020-01-02t03:04:05z"},"opt":"2020-01-02t03:04:05z","plain":"2020-01-02t03:04:05z","req":"2020-01-02t03:04:05z","tuple":["2020-01-02t03:04:05z","x"],"un":"2020-01-02t03:04:05z"}` + "`" + `

func main() {
	want := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

	var v DateTimeCase
	if err := json.Unmarshal([]byte(lower), &v); err != nil {
		fail("decoding the lower case spelling: %v", err)
	}
	if err := v.Validate(); err != nil {
		fail("validating the lower case spelling: %v", err)
	}

	// The instant itself, position by position. A position that decoded into
	// time.Time's zero would satisfy "no error" and nothing else.
	for _, c := range []struct {
		where string
		got   time.Time
	}{
		{"req", v.Req},
		{"opt", *v.Opt},
		{"list[0]", v.List[0]},
		{"grid[0][0]", v.Grid[0][0]},
		{"mp[k]", v.Mp["k"]},
		{"nest.inner", *v.Nest.Inner},
		{"bag[k]", v.Bag.AdditionalProperties["k"]},
		{"aliased", time.Time(*v.Aliased)},
		{"chained", time.Time(Stamp(*v.Chained))},
		{"namedlist[0]", v.Namedlist[0]},
		{"un", time.Time(v.GetDateTimeCaseUnOption0())},
	} {
		if !c.got.Equal(want) {
			fail("%s decoded to %v, want %v", c.where, c.got, want)
		}
	}

	// A string the schema does not call a date-time is not respelled, whatever
	// it happens to hold. The rewrite is reached from the destination type, so
	// nothing that is not going to a time.Time can be touched by it -- and a
	// fix that scanned the document instead would corrupt this value.
	if *v.Plain != "2020-01-02t03:04:05z" {
		fail("plain was rewritten to %q; it is an ordinary string", *v.Plain)
	}
	if v.Tuple[0] != "2020-01-02t03:04:05z" {
		fail("tuple position 0 was rewritten to %v; the document's own bytes are kept", v.Tuple[0])
	}

	// What comes back out. time.Time holds an instant and not the spelling it
	// arrived in, so it writes the canonical upper case one -- the same
	// canonicalisation issue #253 documents for a zero second fraction. That is
	// deliberate and is not what this test is about; it is asserted so that a
	// change to it has to be a decision rather than a discovery.
	out, err := json.Marshal(v)
	if err != nil {
		fail("marshalling: %v", err)
	}
	const upper = ` + "`" + `{"aliased":"2020-01-02T03:04:05Z","bag":{"k":"2020-01-02T03:04:05Z"},"chained":"2020-01-02T03:04:05Z","grid":[["2020-01-02T03:04:05Z"]],"list":["2020-01-02T03:04:05Z"],"mp":{"k":"2020-01-02T03:04:05Z"},"namedlist":["2020-01-02T03:04:05Z"],"nest":{"inner":"2020-01-02T03:04:05Z"},"opt":"2020-01-02T03:04:05Z","plain":"2020-01-02t03:04:05z","req":"2020-01-02T03:04:05Z","tuple":["2020-01-02t03:04:05z","x"],"un":"2020-01-02T03:04:05Z"}` + "`" + `
	if string(out) != upper {
		fail("the marshalled form is not the canonical spelling\n  want: %s\n  got:  %s", upper, string(out))
	}

	// Both mixed spellings, and the upper case one that always worked.
	for _, s := range []string{
		"2020-01-02T03:04:05Z",
		"2020-01-02t03:04:05z",
		"2020-01-02t03:04:05Z",
		"2020-01-02T03:04:05z",
		"2020-01-02t03:04:05.500z",
		"2020-01-02t03:04:05+01:00",
		"2020-01-02t03:04:05-08:00",
	} {
		doc := ` + "`" + `{"req":"` + "`" + ` + s + ` + "`" + `"}` + "`" + `
		var mv DateTimeCase
		if err := json.Unmarshal([]byte(doc), &mv); err != nil {
			fail("decoding %s: %v", doc, err)
		}
		if err := mv.Validate(); err != nil {
			fail("validating %s: %v", doc, err)
		}
	}

	// A value that is not an RFC 3339 date-time is still refused, at every
	// position. Accepting the lower case spelling by loosening the parse into
	// taking anything would be a worse defect than the one being fixed.
	for _, bad := range []string{
		` + "`" + `{"req":"2020-01-02X03:04:05Z"}` + "`" + `,
		` + "`" + `{"req":"2020-13-01T00:00:00Z"}` + "`" + `,
		` + "`" + `{"req":"2020-01-02t13:00:00"}` + "`" + `,
		` + "`" + `{"req":"2020-01-02t99:99:99z"}` + "`" + `,
		` + "`" + `{"req":"not-a-date"}` + "`" + `,
		` + "`" + `{"req":""}` + "`" + `,
		` + "`" + `{"req":"2020-01-02"}` + "`" + `,
		` + "`" + `{"req":"2020-01-02T03:04:05Z","opt":"nope"}` + "`" + `,
		` + "`" + `{"req":"2020-01-02T03:04:05Z","list":["nope"]}` + "`" + `,
		` + "`" + `{"req":"2020-01-02T03:04:05Z","grid":[["nope"]]}` + "`" + `,
		` + "`" + `{"req":"2020-01-02T03:04:05Z","mp":{"k":"nope"}}` + "`" + `,
		` + "`" + `{"req":"2020-01-02T03:04:05Z","nest":{"inner":"nope"}}` + "`" + `,
		` + "`" + `{"req":"2020-01-02T03:04:05Z","bag":{"k":"nope"}}` + "`" + `,
		` + "`" + `{"req":"2020-01-02T03:04:05Z","aliased":"nope"}` + "`" + `,
		` + "`" + `{"req":"2020-01-02T03:04:05Z","chained":"nope"}` + "`" + `,
		` + "`" + `{"req":"2020-01-02T03:04:05Z","namedlist":["nope"]}` + "`" + `,
	} {
		var bv DateTimeCase
		if err := json.Unmarshal([]byte(bad), &bv); err == nil {
			fail("accepted a malformed date-time: %s", bad)
		}
	}

	// The tuple position is judged by re-decoding the element through the
	// position's own type, so its refusal arrives from Validate rather than
	// from the decode.
	badTuple := ` + "`" + `{"req":"2020-01-02T03:04:05Z","tuple":["nope","x"]}` + "`" + `
	var tv DateTimeCase
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
		"testdata/schemas/regression/date_time_case.json",
		"date_time_case_test",
		mainGo,
	)
}

// TestDraft4DateTimeAcceptsTheLowerCaseSpelling is the half of issue #264 that a
// fix modelled too closely on issue #90 would get wrong.
//
// Both defects are cured by decoding through a shadow type, and the walk that
// substitutes those shadows is shared. But the integer leaf is draft-conditional
// and the date-time leaf is not. Draft 3 and draft 4 define an integer as a
// number written without a fraction, so 1.0 is not one there and the plain int64
// decode -- which refuses that notation -- is already the right answer;
// substituting jsonInteger under those drafts would be a new defect, and
// TestDraft4IntegerPositionsKeepTheStrictToken holds that line.
//
// No draft has ever meant a different RFC 3339 by `format: date-time`, and draft
// 3 and draft 4 are two of the five dialects that assert `format` by default --
// so they are among the drafts the defect was worst on, and are exactly the ones
// a single shared gate would have left unfixed. Both leaves are exercised here
// in one document so that a gate applied to the wrong one cannot pass.
func TestDraft4DateTimeAcceptsTheLowerCaseSpelling(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func main() {
	want := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

	const doc = ` + "`" + `{"list":["2020-01-02t03:04:05z"],"n":7,"req":"2020-01-02t03:04:05z"}` + "`" + `
	var v DateTimeCaseDraft4
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		fail("decoding the lower case spelling under draft 4: %v", err)
	}
	if err := v.Validate(); err != nil {
		fail("validating the lower case spelling under draft 4: %v", err)
	}
	if !v.Req.Equal(want) {
		fail("req decoded to %v, want %v", v.Req, want)
	}
	if !v.List[0].Equal(want) {
		fail("list[0] decoded to %v, want %v", v.List[0], want)
	}
	if *v.N != 7 {
		fail("n decoded to %d, want 7", *v.N)
	}

	// The other leaf keeps this draft's answer. 1.0 is not an integer in draft
	// 4, and the plain int64 decode is what refuses it; a shadow applied to both
	// leaves on the same gate would have accepted it here.
	const floatInt = ` + "`" + `{"req":"2020-01-02T03:04:05Z","n":1.0}` + "`" + `
	var fv DateTimeCaseDraft4
	if err := json.Unmarshal([]byte(floatInt), &fv); err == nil {
		fail("draft 4 accepted %s; 1.0 is not an integer in this draft", floatInt)
	}

	// And a malformed date-time is still refused under this draft too.
	for _, bad := range []string{
		` + "`" + `{"req":"not-a-date"}` + "`" + `,
		` + "`" + `{"req":""}` + "`" + `,
		` + "`" + `{"req":"2020-13-01T00:00:00Z"}` + "`" + `,
		` + "`" + `{"req":"2020-01-02T03:04:05Z","list":["nope"]}` + "`" + `,
	} {
		var bv DateTimeCaseDraft4
		if err := json.Unmarshal([]byte(bad), &bv); err == nil {
			fail("accepted a malformed date-time: %s", bad)
		}
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t,
		"testdata/schemas/regression/date_time_case_draft4.json",
		"date_time_case_draft4_test",
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
	runValidationCasesWithConfig(t,
		"testdata/schemas/regression/format_alias_assertions.json",
		formatAssertingConfig(),
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
	runValidationCasesWithConfig(t,
		"testdata/schemas/formats/all_formats.json",
		formatAssertingConfig(),
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
	runValidationCasesWithConfig(t,
		"testdata/schemas/regression/allof_single_branch_type.json",
		formatAssertingConfig(),
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
	runValidationCasesWithConfig(t,
		"testdata/schemas/regression/allof_inline_positions.json",
		formatAssertingConfig(),
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

// TestRuntimeEvaluatorAppliesItsPatterns is the runtime evaluator's ECMA-262
// arms, which are the one part of a helper block that is compiled in
// conditionally -- the engine is a third-party dependency, and a package whose
// schemas name no pattern should not acquire it.
//
// It is also the one helper whose presence cannot be read off a call: the file
// carries the _schemaNode literal and _dynPatternOK is reached from inside the
// block, never from the file. So HelpersReferencedBy takes it from the literal
// instead, and getting that wrong does not break the build -- it emits a node
// with Pattern set and no arm that reads it, which is a check dropped in
// silence. Nothing else in the tree asks the question: with the literal match
// removed, `go test ./...` stays green and "zzz" below is accepted.
//
// Both spellings that set it are covered, in a fixture each. One file carrying
// both would let either match stand in for the other, and the two are separately
// removable. In each fixture the second branch is an integer bound, so a run
// that dropped the pattern arms cannot pass by rejecting everything: the valid
// cases are the control.
func TestRuntimeEvaluatorAppliesItsPatterns(t *testing.T) {
	// "pattern" on a node.
	runValidationCases(t,
		"testdata/schemas/regression/runtime_pattern_branches.json",
		[]string{
			`"abc"`, // matches ^a
			`10`,    // the other branch, untouched by any pattern
		},
		[]string{
			`"zzz"`, // a string the pattern excludes
			`9`,     // below the other branch's minimum
			`{}`,    // no branch admits an object
		},
	)
	// A patternProperties member list, which the additionalProperties of false
	// has to run to know which keys are left over.
	runValidationCases(t,
		"testdata/schemas/regression/runtime_patternprops_branches.json",
		[]string{
			`{"x1":5}`, // the key matches ^x, the value is an integer
			`{}`,       // an object claiming nothing is still an object
			`10`,       // the other branch
		},
		[]string{
			`{"x1":"s"}`, // the member's own type
			`{"y":1}`,    // additionalProperties false, and ^x does not claim "y"
			`9`,          // below the other branch's minimum
			`"abc"`,      // no branch admits a string
		},
	)
}

// TestRefToFalseSchemaForbidsEverything covers #116: the boolean `false` schema
// reached through a $ref rather than written where it is used.
//
// A `false` schema rejects every instance. At the document root that has always
// worked -- generateTypeDef emitted the forbidding wrapper -- but a $defs entry
// holding `false` fell past every arm to the `any` fall-through, and `type B any`
// cannot carry a Validate at all. {"$ref":"#/$defs/b"} then aliased that, so the
// schema accepted "foo" and everything else.
//
// The accept-controls are the other half and matter more than the rejections: a
// $ref to boolean `true` admits every instance, and a fix that answered "$ref to
// a boolean" with a rejection rather than "$ref to `false`" would pass every
// invalid case here and fail `always`, `alwaysAllOf` and `alwaysList`.
func TestRefToFalseSchemaForbidsEverything(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/ref_to_false_schema.json",
		[]string{
			// Nothing present: a `false` schema is about a value that is there,
			// and an absent optional property is the parent's business.
			`{}`,
			// An empty array and an empty object have no position for the
			// forbidding sub-schema to reject.
			`{"list":[]}`,
			`{"map":{}}`,
			`{"tuple":[]}`,
			// Accept-controls: $ref to boolean `true`, which admits everything.
			`{"always":"foo"}`,
			`{"always":5}`,
			`{"always":null}`,
			`{"alwaysAllOf":{"k":1}}`,
			`{"alwaysList":[1,"two",false]}`,
		},
		[]string{
			`{"prop":"foo"}`,      // the $ref itself
			`{"prop":5}`,          //
			`{"prop":{}}`,         //
			`{"viaAllOf":"foo"}`,  // sole branch of an allOf
			`{"viaAnyOf":"foo"}`,  // sole branch of an anyOf
			`{"viaOneOf":"foo"}`,  // sole branch of a oneOf
			`{"viaNested":"foo"}`, // an allOf inside an allOf
			`{"beside":"foo"}`,    // a sibling "type" does not rescue it
			`{"list":[1]}`,        // an array element
			`{"map":{"k":1}}`,     // a map value
			`{"tuple":[1]}`,       // a tuple slot
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

// TestRefToFalseRootForbidsEverything is the shape #116 reproduces with, at the
// position it reproduces at: a root that is nothing but a $ref to `false`.
//
// It carries no valid documents on purpose. A `false` schema admits none, and a
// root that accepted even one would mean the wrapper was not reached.
func TestRefToFalseRootForbidsEverything(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/ref_to_false_root.json",
		nil,
		[]string{`"foo"`, `5`, `true`, `null`, `[]`, `{}`, `{"k":1}`},
	)
}

// TestDraft3DependenciesStringForm covers #117: draft 3 spells a single
// dependency as a bare property name, {"dependencies":{"bar":"foo"}}, where
// every later draft writes the one-element array.
//
// Normalization recognised the array and the sub-schema forms and not the
// string, so unmarshalling a JSON string into a Schema failed and the entry was
// dropped in silence. Nothing was left for the type to be inferred from either,
// so the whole schema came out `type Root any` with no Validate and {"bar":2}
// was accepted.
//
// The array form in the same fixture is the control: it worked before and must
// go on working, so a fix that rerouted the keyword rather than adding the
// missing spelling would show up here. The non-objects are the other control --
// dependencies says nothing about them and all three are valid, which is what
// the suite's draft3/dependencies group asserts.
func TestDraft3DependenciesStringForm(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/draft3_dependencies_string.json",
		[]string{
			`{}`,
			`{"foo":1}`,
			`{"foo":1,"bar":2}`,
			`{"foo":1,"baz":1,"quux":3}`,
			`["bar"]`,
			`"foobar"`,
			`12`,
		},
		[]string{
			`{"bar":2}`,          // string form: bar present, foo missing
			`{"quux":3}`,         // array form: both dependencies missing
			`{"quux":3,"foo":1}`, // array form: baz still missing
			`{"bar":2,"quux":3}`, // both triggers unsatisfied
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

// TestRefSiblingTypeApplies covers #118 from 2019-09 on, where $ref became an
// ordinary applicator and its siblings apply as if the two were an allOf.
//
// hasRefStructuralSiblings listed only the properties and items families, so a
// $ref beside a "type" took the ref-only alias path in every draft and the
// declared type was dropped: {"type":"array","$ref":"#/$defs/a"} generated
// `type Root A` with no Validate, where the identical schema without the $ref
// generates []any and refuses a string.
//
// `plain` is the accept-control for the widening itself -- a $ref with no
// sibling at all still takes the ref-only path and still resolves to the
// definition's own type.
//
// The bounded* positions are the other half, and the half that stops the
// widening from trading one dropped keyword for another: each states a type
// *and* references a definition carrying a bound, so a position that answered
// from the sibling alone would lose the bound exactly as the old one lost the
// type. The element, map-value and tuple positions are the ones that needed a
// merge arm of their own -- they resolve through resolveType, which had none,
// while a property has had one since $ref-beside-properties.
func TestRefSiblingTypeApplies(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/ref_sibling_type.json",
		[]string{
			`{}`,
			`{"arr":[]}`,
			`{"arr":[1,"two"]}`,
			`{"str":"x"}`,
			`{"num":1.5}`,
			`{"bounded":"abc"}`,           // the $ref's minLength is satisfied
			`{"elem":["a"]}`,              // array element: sibling type kept
			`{"mapv":{"k":"v"}}`,          // map value
			`{"slot":["a"]}`,              // tuple slot
			`{"boundedElem":["abc"]}`,     // element: both halves satisfied
			`{"boundedMapv":{"k":"abc"}}`, // map value
			`{"boundedSlot":["abc"]}`,     // tuple slot
			`{"plain":"abc"}`,             // control: $ref with no sibling
			`{"plain":5}`,                 // minLength is vacuous for a number
		},
		[]string{
			`{"arr":"x"}`,      // the declared array type
			`{"arr":5}`,        //
			`{"str":5}`,        // the declared string type
			`{"num":"x"}`,      // the declared number type
			`{"bounded":"ab"}`, // the $ref's own constraint through the merge
			`{"elem":[5]}`,     // the sibling type at an element
			`{"mapv":{"k":5}}`, //
			`{"slot":[5]}`,     //
			// The $ref's bound at the same three positions. These are what a
			// widening that answered from the sibling alone would accept.
			`{"boundedElem":["ab"]}`,
			`{"boundedMapv":{"k":"ab"}}`,
			`{"boundedSlot":["ab"]}`,
			`{"plain":"ab"}`, // control: the definition still constrains
		},
	)
}

// TestAllOfBranchArrayKeywordsAreAdopted covers the array half of the merge:
// which of an allOf branch's items/prefixItems/contains the merged type carries.
//
// The merge leaves them alone by design, because they are scoped to the schema
// object stating them -- a parent's `items` governs what follows the parent's
// own prefix, so folding a branch's prefix in beside it would move what the
// parent's keyword reaches. When the parent states none of them there is no
// such interaction, and leaving the branch's unread meant a $ref beside a
// "type":"array" enforced the type and nothing else: the whole point of #118 is
// that both halves bind.
//
// The controls are the two cases the adoption must decline. `ownPrefix` states
// its own prefixItems, so the branch's must not displace it -- [9] is refused
// by the parent's string prefix, which a fix that let the branch win would
// accept. `twoBranches` has two branches stating prefixItems: satisfying both is
// an allOf of the two, which one merged type cannot express, so neither is
// adopted and both documents pass. That under-enforcement is deliberate and
// pinned here rather than left to be discovered.
func TestAllOfBranchArrayKeywordsAreAdopted(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/allof_branch_array_keywords.json",
		[]string{
			`{}`,
			`{"contains":[1]}`,
			`{"contains":["a",1]}`,
			`{"prefix":["a",9]}`,
			`{"prefix":[]}`, // no position 0, so the prefix says nothing
			`{"viaRef":[1]}`,
			`{"ownPrefix":["a",1]}`,
			// Control: two branches state prefixItems, so neither is adopted.
			`{"twoBranches":["a"]}`,
			`{"twoBranches":[1]}`,
		},
		[]string{
			`{"contains":["a"]}`, // the branch's contains
			`{"contains":[]}`,    // an empty array contains no integer
			`{"contains":"x"}`,   // and the declared array type still binds
			`{"prefix":[9]}`,     // the branch's prefixItems
			`{"viaRef":["a"]}`,   // the same through a $ref beside the type
			// Control: the parent's own prefix decides, not the branch's.
			`{"ownPrefix":[9]}`,
		},
	)
}

// TestRefSiblingTypeSuppressedBeforeDraft2019 is the narrowness control for
// #118, and the half a fix that ignored the draft would break.
//
// Through draft-07 a $ref replaces every keyword beside it, so the reference
// alone decides. `suppressed` declares "type":"array" and $refs the empty
// schema, which admits everything: a string is valid there, and applying the
// sibling would refuse it -- a false rejection, worse than the missing check it
// would be replacing. `bounded` is the same control for a non-structural
// sibling: minLength 5 is suppressed, so a four-character string satisfying the
// target's own minLength 3 passes.
//
// Both are chosen so that suppressing and merging disagree. A sibling "type"
// beside a $ref whose target *also* declares one does not discriminate: the
// merge takes the branch's type over the parent's, so it lands on the same
// answer suppression does and the control would pass under a fix that had the
// draft split backwards.
func TestRefSiblingTypeSuppressedBeforeDraft2019(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/ref_sibling_type_draft7.json",
		[]string{
			`{}`,
			// The $ref alone decides, and it is the empty schema.
			`{"suppressed":"abc"}`,
			`{"suppressed":5}`,
			`{"suppressed":{"k":1}}`,
			`{"suppressed":[1,2]}`,
			// The sibling minLength 5 is suppressed; the target's own 3 is met.
			`{"bounded":"abcd"}`,
			`{"plain":"abc"}`,
		},
		[]string{
			`{"bounded":"ab"}`, // the $ref's own minLength does bind
			`{"plain":"ab"}`,
			`{"plain":5}`, // and so does its type
		},
	)
}

// TestRefSiblingAssertionsBindFrom2019 is #118 and #153's question asked of the
// keywords that only ever assert, which is issue #204.
//
// The sibling list those issues grew named the keywords that decide a Go type --
// the properties and items families, "type", "enum", "const" -- so a keyword
// that merely constrains was answered "nothing is written beside this reference"
// and the ref-only arms aliased the target and dropped it. Every property here
// was accepted with any value the target admitted: `required` said nothing,
// `maxLength` said nothing, and nothing in the generated type recorded that they
// had been read at all.
//
// `untyped` is the half that did not merely under-enforce. Its target is {}, so
// the reference types the property `any`, and where the assertion *was* emitted
// it was emitted against that: utf8.RuneCountInString on an interface, a
// generated file that does not compile. It is here rather than in a compile-only
// test because the check has to be shown working as well as building -- and
// working means saying nothing about the 5, which no string keyword constrains.
//
// The four positions are all covered because "fixed at a property, not at an
// element" has been the shape of this defect's every recurrence.
func TestRefSiblingAssertionsBindFrom2019(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/ref_sibling_assertions_2020.json",
		[]string{
			`{}`,
			`{"req":{"r":1}}`,
			`{"counted":{"a":1,"b":2}}`,
			`{"depends":{}}`,
			`{"depends":{"a":1,"b":2}}`,
			`{"short":"abc"}`,
			`{"shaped":"aa"}`,
			`{"even":4}`,
			`{"several":[1,2]}`,
			`{"distinct":[1,2]}`,
			`{"untyped":"abc"}`,
			// maxLength constrains a string and says nothing about a number, and
			// the target admits both.
			`{"untyped":5}`,
			`{"elements":["abc"]}`,
			`{"values":{"k":"abc"}}`,
			`{"slots":["abc"]}`,
		},
		[]string{
			`{"req":{}}`,
			`{"counted":{"a":1}}`,
			`{"depends":{"a":1}}`,
			`{"short":"abcd"}`,
			`{"shaped":"bb"}`,
			`{"even":3}`,
			`{"several":[1]}`,
			`{"distinct":[1,1]}`,
			`{"untyped":"abcd"}`,
			`{"elements":["abcd"]}`,
			`{"values":{"k":"abcd"}}`,
			`{"slots":["abcd"]}`,
		},
	)
}

// TestRefSiblingAssertionsSuppressedBeforeDraft2019 is the narrowness control
// for the test above, and the half a fix that ignored the draft would break.
//
// Through draft-07 a $ref replaces every keyword beside it, so every one of
// these assertions says nothing and enforcing it would refuse a document the
// schema admits -- a false rejection across four dialects, worse than the missed
// check it would replace. The valid list is therefore exactly the invalid list
// of the 2020-12 test: the same documents, the opposite verdict, decided by the
// $schema alone.
//
// `composed` covers the same rule for an `allOf` written beside the reference.
// That one was not merely dropped: generateTypeDef's allOf arm ran ahead of its
// ref arms and never asked the draft, so the branch was enforced and "a" was
// rejected on draft 4, 6 and 7 -- and where the target was untyped the merge
// emitted the branch's minLength against an interface and the file did not
// compile.
//
// The invalid list is what keeps this from passing under a generator that had
// simply stopped reading these schemas: the reference itself still binds, and
// the type each target declares still refuses the values it always did.
func TestRefSiblingAssertionsSuppressedBeforeDraft2019(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/ref_sibling_assertions_draft7.json",
		[]string{
			`{}`,
			`{"req":{}}`,
			`{"counted":{"a":1}}`,
			`{"short":"abcd"}`,
			`{"shaped":"bb"}`,
			`{"even":3}`,
			`{"several":[1]}`,
			`{"distinct":[1,1]}`,
			`{"untyped":"abcd"}`,
			`{"composed":"a"}`,
			`{"elements":["abcd"]}`,
			`{"values":{"k":"abcd"}}`,
			`{"slots":["abcd"]}`,
		},
		[]string{
			// The reference is the whole schema, and its type still binds.
			`{"short":5}`,
			`{"shaped":5}`,
			`{"even":"x"}`,
			`{"several":"x"}`,
			`{"composed":5}`,
			`{"elements":[5]}`,
		},
	)
}

// TestRefSiblingAssertionsBindAtTheRoot is the same question one position up.
//
// A root is where the defect was reported, and it is the position with no field
// to hang a check on: {"$defs":{"S":{"type":"object"}},"$ref":"#/$defs/S",
// "required":["r"]} became `type Root S` and accepted {}, which the schema
// forbids. Three keywords are written together so that a fix reaching one of
// them and not the others cannot pass.
func TestRefSiblingAssertionsBindAtTheRoot(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/ref_sibling_assertions_root_2020.json",
		[]string{
			`{"r":1,"x":2}`,
			`{"r":1,"a":2,"b":3}`,
		},
		[]string{
			`{}`,            // required, and minProperties
			`{"r":1}`,       // minProperties
			`{"r":1,"a":2}`, // dependentRequired
		},
	)
}

// TestRefSiblingAllOfFollowsTheDraftAtTheRoot is the draft split for an `allOf`
// written beside a $ref, in the position where generateTypeDef decides it.
//
// Its allOf arm ran ahead of its ref arms and never asked the draft, so through
// draft-07 -- where the reference replaces everything beside it -- the branch was
// merged and enforced anyway: "a" was rejected on draft 4, 6 and 7 by a schema
// that admits it, a false rejection across three dialects. The 2020-12 twin is
// what stops the fix from becoming "ignore an allOf beside a reference": there
// both bind, and "a" must still be refused.
//
// The untyped fixture is the same rule where getting it wrong costs the build
// rather than the verdict. Its target is {}, so the merge had `type Anything
// any` to emit the branch's minLength against, and produced a `string()`
// conversion of an interface -- source Go refuses. Suppressing the branch is
// what leaves the reference alone, which is all the draft ever said.
//
// The invalid rows are what keep all three honest: the reference itself binds in
// every draft, so a generator that had stopped reading these schemas fails here.
func TestRefSiblingAllOfFollowsTheDraftAtTheRoot(t *testing.T) {
	t.Run("draft7SuppressesTheBranch", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/ref_sibling_allof_root_draft7.json",
			[]string{`"a"`, `"abc"`},
			[]string{`5`}, // the target's own type still binds
		)
	})
	t.Run("draft7SuppressesTheBranchOverAnUntypedTarget", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/ref_sibling_allof_root_untyped_draft7.json",
			// The target admits everything and the branch is not written at all.
			[]string{`{}`, `{"c":"a"}`, `{"c":"abc"}`, `{"c":5}`},
			nil,
		)
	})
	t.Run("2020AppliesTheBranch", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/ref_sibling_allof_root_2020.json",
			[]string{`"abc"`},
			[]string{`"a"`, `5`},
		)
	})
}

// TestRefSiblingArrayShapesCompile covers the three array shapes beside a $ref
// that generated source Go refuses, which is the half of #204 no amount of
// validation testing catches.
//
// `narrowed` is the merge emitting its delegation as a Go conversion: the branch
// alias was `S []any` and the merged type `[]string`, and `(S(r)).Validate()`
// between those is not a conversion Go has -- "cannot convert r (variable of
// slice type Root) to type S".
//
// `slotted` and `everySlot` are a reference to {}, which generates `type
// Anything any`. A tuple slot emitted `_typed.Validate()` for it regardless, and
// Go permits no method on an interface underlying type: "_typed.Validate
// undefined". That one needed no sibling at all -- a bare $ref to {} at a tuple
// slot was enough -- and it was reached on every draft, since through draft-07 a
// sibling is suppressed and what is left is exactly the bare reference.
//
// The documents matter as much as the compile: a shape that stopped building by
// being dropped would pass a compile-only test.
func TestRefSiblingArrayShapesCompile(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/ref_sibling_array_shapes_2020.json",
		[]string{
			`{}`,
			`{"narrowed":["a"]}`,
			`{"counted":[1,2]}`,
			// The target admits every value, so the slot constrains nothing.
			`{"slotted":[1]}`,
			`{"slotted":["a"]}`,
			`{"everySlot":[1,"a"]}`,
		},
		[]string{
			`{"narrowed":[1]}`,
			`{"counted":[1]}`,
		},
	)
}

// TestNullableFormatIsAssertedEverywhere covers the nullable spelling of a
// formatted string -- {"type":["string","null"],"format":"ipv4"} -- in every
// position it can be written.
//
// It resolved to *string wherever it appeared, losing the netip.Addr or
// time.Time the non-nullable spelling gets, and the named form became `type
// NullableV4 *string`. Go forbids methods on a pointer underlying type, so that
// type carried no Validate at all: a nullable format asserted nothing, in the
// one position where the non-nullable form asserts correctly. Inline it was
// worse than unenforced -- the emitted family check read netip.Addr's methods
// off a *string, and the generated file did not compile.
//
// The valid half is the larger one, and deliberately so. A null is permitted in
// every one of these positions and must stay permitted: turning a missing
// assertion into a rejection of what the schema allows would be the worse trade,
// and a wrapper that rejected null would pass every case in the invalid list
// while being wrong about all of them.
func TestNullableFormatIsAssertedEverywhere(t *testing.T) {
	runValidationCasesWithConfig(t,
		"testdata/schemas/regression/nullable_format_positions.json",
		formatAssertingConfig(),
		[]string{
			`{}`,
			// A conforming address, position by position.
			`{"inline":"192.0.2.7"}`,
			`{"ref":"192.0.2.7"}`,
			`{"chain":"192.0.2.7"}`,
			`{"list":["192.0.2.7","192.0.2.8"]}`,
			`{"map":{"k":"192.0.2.7"}}`,
			`{"tuple":["192.0.2.7",1]}`,
			`{"branch":"192.0.2.7"}`,
			`{"wrapped":"192.0.2.7"}`,
			`{"buckets":{"pp":"192.0.2.7"}}`,
			// The null the type list permits, position by position. These are
			// the accept-controls: every rejection below is only correct if all
			// of these still pass.
			`{"inline":null}`,
			`{"ref":null}`,
			`{"chain":null}`,
			`{"list":[null,"192.0.2.7"]}`,
			`{"map":{"k":null}}`,
			`{"tuple":[null,1]}`,
			`{"wrapped":null}`,
			`{"buckets":{"pp":null}}`,
			// A key the pattern does not match is unconstrained.
			`{"buckets":{"zz":"not-an-ip"}}`,
			// The two other formats, whose Go types differ from ipv4's.
			`{"stamp":"2020-01-02T03:04:05Z"}`, `{"stamp":null}`,
			`{"mail":"a@b.test"}`, `{"mail":null}`,
		},
		[]string{
			// A well-formed address of the wrong family: it parses, so only the
			// format assertion can reject it.
			`{"inline":"2001:db8::1"}`,
			`{"ref":"2001:db8::1"}`,
			`{"chain":"2001:db8::1"}`,
			`{"list":["192.0.2.7","2001:db8::1"]}`,
			`{"map":{"k":"2001:db8::1"}}`,
			`{"tuple":["2001:db8::1",1]}`,
			`{"branch":"2001:db8::1"}`,
			`{"wrapped":"2001:db8::1"}`,
			`{"buckets":{"pp":"2001:db8::1"}}`,
			// Not an address at all.
			`{"inline":"not-an-ip"}`,
			`{"ref":"not-an-ip"}`,
			// A type the list does not carry. "null" widened the schema by
			// exactly one instance type, not to everything.
			`{"inline":5}`,
			`{"ref":{"a":1}}`,
			`{"list":[5]}`,
			`{"map":{"k":true}}`,
			// The other two formats.
			`{"stamp":"not-a-timestamp"}`,
			`{"mail":"not-an-email"}`,
		},
	)
}

// TestUntypedFormatIsAssertedOnStringsOnly covers a `format` written with no
// "type" beside it, in every position it can be written.
//
// It resolved to `any`, and Go forbids methods on `any`, so the format was
// asserted nowhere -- inline, behind a $ref, through an alias chain, as an
// element, a map value, a tuple slot, a oneOf branch, an allOf branch and a
// patternProperties bucket alike.
//
// The narrowness is the point, and it is what the second half of the valid list
// pins. `format` is scoped to string instances: a number, an object, an array, a
// boolean and a null satisfy {"format":"ipv4"} outright, so the fix cannot be
// "give it a string type" -- that would reject documents the schema admits, in
// every one of these positions at once. Each of those five is here beside the
// rejection it must not become.
func TestUntypedFormatIsAssertedOnStringsOnly(t *testing.T) {
	runValidationCasesWithConfig(t,
		"testdata/schemas/regression/untyped_format_positions.json",
		formatAssertingConfig(),
		[]string{
			`{}`,
			// A conforming address, position by position.
			`{"inline":"192.0.2.7"}`,
			`{"ref":"192.0.2.7"}`,
			`{"chain":"192.0.2.7"}`,
			`{"list":["192.0.2.7","192.0.2.8"]}`,
			`{"map":{"k":"192.0.2.7"}}`,
			`{"tuple":["192.0.2.7",1]}`,
			`{"branch":"192.0.2.7"}`,
			`{"wrapped":"192.0.2.7"}`,
			`{"buckets":{"pp":"192.0.2.7"}}`,
			// The accept-controls: five instance types the format says nothing
			// about, in the positions whose Go type the fix changed.
			`{"inline":5}`, `{"inline":{"a":1}}`, `{"inline":[1]}`, `{"inline":true}`, `{"inline":null}`,
			`{"ref":5}`, `{"ref":{"a":1}}`, `{"ref":null}`,
			`{"chain":5}`, `{"chain":null}`,
			`{"list":[5,{"a":1},null,true]}`,
			`{"map":{"k":5}}`, `{"map":{"k":null}}`,
			`{"tuple":[5,1]}`, `{"tuple":[null,1]}`, `{"tuple":[{"a":1},1]}`,
			`{"wrapped":5}`, `{"wrapped":null}`,
			`{"buckets":{"pp":5}}`, `{"buckets":{"pp":null}}`,
			// A key the pattern does not match is unconstrained.
			`{"buckets":{"zz":"not-an-ip"}}`,
			// The two other formats, and their non-string accept-controls.
			`{"stamp":"2020-01-02T03:04:05Z"}`, `{"stamp":5}`, `{"stamp":null}`,
			`{"mail":"a@b.test"}`, `{"mail":5}`, `{"mail":null}`,
		},
		[]string{
			// A well-formed address of the wrong family, which only the format
			// assertion can reject.
			`{"inline":"2001:db8::1"}`,
			`{"ref":"2001:db8::1"}`,
			`{"chain":"2001:db8::1"}`,
			`{"list":["192.0.2.7","2001:db8::1"]}`,
			`{"map":{"k":"2001:db8::1"}}`,
			`{"tuple":["2001:db8::1",1]}`,
			`{"branch":"2001:db8::1"}`,
			`{"wrapped":"2001:db8::1"}`,
			`{"buckets":{"pp":"2001:db8::1"}}`,
			// Not an address at all.
			`{"inline":"not-an-ip"}`,
			`{"ref":"not-an-ip"}`,
			`{"chain":"not-an-ip"}`,
			// The other two formats, on a string instance.
			`{"stamp":"not-a-timestamp"}`,
			`{"mail":"not-an-email"}`,
		},
	)
}

// TestFormatBesideLengthCompilesAndChecksBoth covers a format that maps to a Go
// type written beside a keyword that reads the string's characters.
//
// The two are irreconcilable as they stood: minLength is measured with
// utf8.RuneCountInString, which takes a string, and neither netip.Addr nor
// time.Time converts to one. The generator emitted the length check anyway and
// the result did not compile -- `cannot convert i (variable of struct type
// IPWithLen) to type string` for the alias, and `cannot use *s.B (variable of
// struct type netip.Addr) as string value` for the field. That is the harshest
// failure a generator has, and every one of these six properties produced it.
//
// The fix gives up the Go type rather than the length check, so this test's
// whole point is that *both* keywords still bind: the length half and the format
// half each have a rejection here, and the compile is the third assertion, made
// by runValidationCases building the program at all.
func TestFormatBesideLengthCompilesAndChecksBoth(t *testing.T) {
	runValidationCasesWithConfig(t,
		"testdata/schemas/regression/format_beside_length.json",
		formatAssertingConfig(),
		[]string{
			`{}`,
			`{"declaredV4":"192.0.2.77"}`,
			`{"declaredStamp":"2020-01-02T03:04:05+01:00"}`,
			`{"inferredV4":"192.0.2.77"}`,
			`{"refV4":"192.0.2.77"}`,
			`{"refStamp":"2020-01-02T03:04:05+01:00"}`,
			`{"patternedV4":"192.0.2.7"}`,
			// The type this schema is not about. Only the inferred property can
			// hold one -- the others declare "type":"string" -- and there the
			// length and format keywords are both vacuous.
			`{"inferredV4":5}`, `{"inferredV4":null}`, `{"inferredV4":{"a":1}}`,
		},
		[]string{
			`{"declaredV4":"1.2.3.4"}`,     // 7 characters, under minLength 9
			`{"declaredV4":"2001:db8::1"}`, // long enough, wrong family
			// A valid RFC 3339 date-time, 20 characters, under minLength 25.
			// The length half of the pair, on the format whose Go type is
			// time.Time rather than netip.Addr.
			`{"declaredStamp":"2020-01-02T03:04:05Z"}`,
			`{"declaredStamp":"not-a-timestamp-at-all-xx"}`, // 25 characters, not a date-time
			`{"inferredV4":"1.2.3.4"}`,
			`{"inferredV4":"2001:db8::999"}`,
			`{"refV4":"1.2.3.4"}`,
			`{"refV4":"2001:db8::1"}`,
			`{"refStamp":"2020-01-02T03:04:05Z"}`,
			`{"refStamp":"not-a-timestamp-at-all-xx"}`,
			`{"patternedV4":"10.0.0.1"}`,    // an address the pattern excludes
			`{"patternedV4":"192.0.2.999"}`, // matches the pattern, not an address
		},
	)
}

// TestFormatRootPositionsAssert is the position the two tests above cannot
// reach: the schema as a whole document, where the wrapper is the root type
// itself rather than something a field refers to. Both shapes are here because
// they take different routes to a root type -- the nullable one through the
// "type" union wrapper, the untyped one through the inferred-value wrapper --
// and a fix that reached only the positions inside an object would leave the
// document root asserting nothing, which is where a $defs entry lands when it is
// split into a file of its own.
func TestFormatRootPositionsAssert(t *testing.T) {
	runValidationCasesWithConfig(t,
		"testdata/schemas/regression/nullable_format_root.json",
		formatAssertingConfig(),
		[]string{`"192.0.2.7"`, `null`},
		[]string{`"2001:db8::1"`, `"not-an-ip"`, `5`, `{"a":1}`},
	)
	runValidationCasesWithConfig(t,
		"testdata/schemas/regression/untyped_format_root.json",
		formatAssertingConfig(),
		// Every non-string instance satisfies a bare format, so all four are
		// accept-controls for the two rejections.
		[]string{`"192.0.2.7"`, `null`, `5`, `{"a":1}`, `[1]`, `true`},
		[]string{`"2001:db8::1"`, `"not-an-ip"`},
	)
}

// malformedByFormat is one document per spelling of a format, each carrying a
// value that is well-formed JSON, is of the right instance type, and is not of
// the named format. Whether these are rejected is the whole of the format
// posture, so the same list is used for all three of its answers.
var malformedByFormat = []string{
	`{"typed":"2001:db8::1"}`,
	`{"untyped":"not-an-ip"}`,
	`{"nullable":"2001:db8::1"}`,
	`{"stamp":"not-a-timestamp"}`,
	`{"mail":"not-an-email"}`,
}

// conformingByFormat is the same set of properties carrying values that do
// match. They are the control on every arm below: a posture that accepted
// nothing would pass the annotation arm's expectations by accident.
var conformingByFormat = []string{
	`{}`,
	`{"typed":"192.0.2.7"}`,
	`{"untyped":"192.0.2.7"}`,
	`{"nullable":"192.0.2.7"}`, `{"nullable":null}`,
	`{"stamp":"2020-01-02T03:04:05Z"}`,
	`{"mail":"a@b.test"}`,
	// A format says nothing about an instance of another type, on any draft
	// and under any posture. Only the untyped spelling can hold one.
	`{"untyped":5}`, `{"untyped":null}`, `{"untyped":{"a":1}}`,
}

// TestFormatPostureFollowsTheDialect pins which drafts assert a format, and
// what --format-assertion changes.
//
// The generator asserted `format` on every draft and could not be told not to.
// That is a legitimate reading of draft 3 through 7, which say an implementation
// SHOULD validate a format it recognises and MAY treat it as an annotation. It
// is not a legitimate reading of 2019-09 or 2020-12: their default meta-schema
// declares the format-annotation vocabulary, whose content is that format
// produces an annotation and no assertion, and the official suite marks
// {"format":"email"} satisfied by "2962" accordingly. Rejecting those documents
// is rejecting what the schema permits.
//
// TestEmptyEnumAdmitsNothing pins the third spelling of the empty set.
//
// `enum` asserts that the instance equals one of the listed values. With no
// values listed nothing can, so `{"enum":[]}` says exactly what `false` says --
// and the official suite states it that way: its "empty enum" group marks a
// string, a number, a null, an object, an array and a boolean all invalid. That
// group arrived with the #121 corpus bump, on 2019-09, 2020-12 and v1 alike.
//
// Every arm of generateTypeDef asked len(s.Enum) > 0 and so declined the empty
// list, then read the rest of the schema as if the keyword were absent:
// `{"enum":[]}` came out `type Root any` and accepted all six documents, and
// `{"type":"string","enum":[]}` came out `type Root string` and accepted every
// string. An inline property spelling it went the same way through
// extractValidationRules.
//
// The populated enum beside them is the control. Forbidding every enum would
// satisfy the invalid half of both arms and mean nothing at all.
func TestEmptyEnumAdmitsNothing(t *testing.T) {
	t.Run("root", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/empty_enum_root.json",
			nil,
			[]string{`"foo"`, `42`, `null`, `{}`, `[]`, `false`},
		)
	})
	t.Run("properties", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/empty_enum_positions.json",
			[]string{
				`{}`,
				`{"populated":"a"}`,
				`{"populated":"b"}`,
			},
			[]string{
				// Present at all is the violation, whatever the value and
				// whichever way the sub-schema is written.
				`{"inline":"foo"}`,
				`{"inline":null}`,
				`{"inline":{}}`,
				`{"typed":"foo"}`,
				`{"viaRef":"foo"}`,
				`{"viaRef":42}`,
				// The control still rejects a value outside its list, which is
				// the check a blanket "every enum forbids everything" would have
				// broken in the other direction.
				`{"populated":"c"}`,
			},
		)
	})
}

// v1 is the fourth answer and it is not a repeat of draft 7's. It is *newer*
// than the two drafts that annotate: it drops vocabularies, and the official
// suite moves its format cases out of optional/ into a required top-level
// format/ directory where {"format":"email"} is marked not satisfied by "2962"
// -- the exact document 2020-12 marks valid. Required there means default, so
// the history runs assert, annotate, assert again, and mapping v1 onto 2020-12
// because their keyword sets nearly match would silently stop enforcing every
// format a v1 schema names.
//
// The five arms are the five answers, and each is needed. Without the first the
// change is untested; without the second the older drafts could have been broken
// silently, since nothing else in the tree still asserts by default; without the
// third the assertion flag could be doing nothing at all and every other format
// test in this file -- all of which now pass it -- would still pass; without the
// fourth v1 could be inheriting 2020-12's annotation posture unnoticed; without
// the fifth the annotation flag could be doing nothing, since v1's own answer
// and the flag's would agree on every other arm.
func TestFormatPostureFollowsTheDialect(t *testing.T) {
	t.Run("2020-12 annotates", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/format_posture_2020.json",
			append(append([]string{}, conformingByFormat...), malformedByFormat...),
			nil,
		)
	})
	t.Run("draft-07 asserts", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/format_posture_draft7.json",
			conformingByFormat,
			malformedByFormat,
		)
	})
	t.Run("2020-12 asserts under the flag", func(t *testing.T) {
		runValidationCasesWithConfig(t,
			"testdata/schemas/regression/format_posture_2020.json",
			formatAssertingConfig(),
			conformingByFormat,
			malformedByFormat,
		)
	})
	t.Run("v1 asserts", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/format_posture_v1.json",
			conformingByFormat,
			malformedByFormat,
		)
	})
	t.Run("v1 annotates under the flag", func(t *testing.T) {
		runValidationCasesWithConfig(t,
			"testdata/schemas/regression/format_posture_v1.json",
			formatAnnotatingConfig(),
			append(append([]string{}, conformingByFormat...), malformedByFormat...),
			nil,
		)
	})
}

// TestFormatChecksMatchTheSuite pins the format checks against the cases where
// they used to disagree with the official suite's optional/format corpus.
//
// Every document in the valid list was rejected by the released checks, and a
// rejection of a conforming document is the worst thing this generator can do:
// it is not a missing check that someone might notice, it is a build that
// refuses to accept data the schema permits. They are grouped by what was wrong
// rather than by format, because one cause usually produced several.
//
// The invalid list is the control. Loosening a check until nothing is rejected
// would satisfy every line above and mean nothing, so each cause has a rejection
// beside it -- and the leap-second cases in particular, where the rule is not
// "second 60 is fine" but "second 60 at the last minute of the UTC day".
//
// The stampLen documents are the one group that was never a false rejection.
// They hold issue #274's answer instead: a `date-time` field cannot represent a
// leap second, and the README says so and names the escape route beside it. Both
// halves are stated here -- the typed refusal in the invalid list, the escaped
// acceptance in the valid one -- because a README claim about a verdict is a
// claim nothing else in this tree is watching.
func TestFormatChecksMatchTheSuite(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/format_accuracy.json",
		[]string{
			`{}`,
			// RFC 3339 admits a leap second, and time.Parse does not.
			//
			// Through stampStr rather than stamp, and the difference is not
			// incidental. A declared "type":"string" with format date-time is
			// held as a time.Time, and a time.Time cannot represent 23:59:60 at
			// all -- the value is refused by the decoder, before any check runs.
			// That is a property of the representation rather than of the check,
			// and it is why the untyped spelling, which keeps the JSON string,
			// is the one that can accept a leap second.
			`{"stampStr":"1998-12-31T23:59:60Z"}`,
			`{"stampStr":"1998-12-31T15:59:60.123-08:00"}`,
			// stampLen is the escape route the README names for issue #274, and
			// the reason it works is the one the canonicalisation note gives: a
			// minLength beside the format is a keyword time.Time does not carry,
			// so the mapping is given up, the value stays a string, and the
			// check that runs is the one that admits a leap second. The typed
			// spelling of the very same value is in the invalid list below, so
			// the pair states the whole of the documented behaviour.
			`{"stampLen":"1998-12-31T23:59:60Z"}`,
			`{"stampLen":"1998-12-31T15:59:60.123-08:00"}`,
			`{"clock":"23:59:60Z"}`,
			`{"clock":"23:59:60+00:00"}`,
			`{"clock":"01:29:60+01:30"}`,
			`{"clock":"23:29:60+23:30"}`,
			`{"clock":"15:59:60-08:00"}`,
			`{"clock":"00:29:60-23:30"}`,
			// RFC 3339 says the T and the Z are case-insensitive.
			`{"stampStr":"1963-06-19t08:30:06.283185z"}`,
			`{"clock":"08:30:06z"}`,
			// "format":"regex" is ECMA-262, not Go's RE2, which is a different
			// language: [^] is an ordinary empty negated class there and a
			// syntax error here.
			//
			// Lookbehind is deliberately not in this list. ES2018 added it and
			// the ECMA-262 engine accepts it, while the suite -- which predates
			// that edition -- marks (?<=foo)bar invalid for `format: regex`.
			// Accepting it is an under-enforcement against the suite and a
			// disagreement about which edition of ECMA-262 `format: regex`
			// names, not something either side can call a defect.
			`{"pattern":"[^]"}`,
			// An address literal is a legal domain, and net/mail refuses it.
			`{"mail":"joe.bloggs@[IPv6:::1]"}`,
			`{"mail":"joe.bloggs@[127.0.0.1]"}`,
			// A quoted local part may hold a space, two dots or an "@".
			`{"mail":"\"joe bloggs\"@example.com"}`,
			`{"mail":"\"joe..bloggs\"@example.com"}`,
			`{"mail":"\"joe@bloggs\"@example.com"}`,
			// An internationalized name is not ASCII, which is what the check
			// used to require of it -- so every one of these was refused. They
			// are also the accept-controls for the ContextO and IDNA rejections
			// below: each carries the very character those rules constrain, in a
			// position where the rule is satisfied.
			`{"intlHost":"실례.테스트"}`,
			`{"intlHost":"ßς་〇"}`,
			`{"intlHost":"א״ב"}`, // GERSHAYIM preceded by Hebrew
			`{"intlHost":"א׳ב"}`, // GERESH preceded by Hebrew
			`{"intlHost":"l·l"}`, // MIDDLE DOT between two 'l'
			`{"intlHost":"α͵β"}`, // KERAIA followed by Greek
			`{"intlHost":"・ぁ"}`,  // KATAKANA MIDDLE DOT beside Hiragana
			`{"intlHost":"・ァ"}`,  // ... beside Katakana
			`{"intlHost":"・丈"}`,  // ... beside Han
			`{"intlHost":"۽۾"}`,
			`{"intlHost":"ب٠ب"}`,
			`{"intlHost":"۰0"}`,
			// The four UTS-46 label separators, and a name long enough that
			// counting octets rather than characters would have refused it.
			`{"intlHost":"a｡b"}`, `{"intlHost":"a。b"}`, `{"intlHost":"a．b"}`,
			`{"intlHost":"παράδειγμαπαράδειγμαπαράδειγμαπαράδειγμαπαράδειγμαπα.com"}`,
			`{"intlHost":"παράδειγμαπαράδειγμαπαράδειγμαπαράδειγμαπαράδειγμαπα｡com"}`,
			`{"intlMail":"실례@실례.테스트"}`,
			// A well-formed A-label. The ASCII check accepted every xn-- label on
			// sight; this one has to survive being decoded and judged.
			`{"host":"xn--bcher-kva.example"}`,
			// The PVALID members of RFC 5892 section 2.6, through punycode: the
			// accept-controls for the DISALLOWED members of the same section, which
			// the invalid list refuses in exactly this spelling.
			`{"host":"xn--zca29lwxobi7a"}`, // ßς་〇
			`{"host":"xn--qmbc"}`,          // ۽۾
			`{"host":"xn--9n2bp8q.xn--9t4b11yi5a"}`,
			// The carriers the DISALLOWED exceptions below are embedded in, with
			// the exception taken out. Each is accepted, so the ten rejections are
			// about the code point and not about the script it sits in.
			`{"intlHost":"بب"}`, `{"intlHost":"ߊߊ"}`,
			`{"intlHost":"실례"}`, `{"intlHost":"ああ"}`,
			// An IP-literal host is bracketed, and the brackets are what tell it
			// from a bare IPv6 address with a port.
			`{"link":"ldap://[2001:db8::7]/c=GB?objectClass?one"}`,
			// "" is a URI reference (this document) and a JSON Pointer (the whole
			// document), and each check says so on its own terms rather than by
			// an exemption.
			`{"linkRef":""}`,
			`{"pointer":""}`,
			`{"pattern":""}`,
			// Ordinary conforming values, so a check that rejected everything
			// could not pass this test by accident.
			`{"stamp":"2020-01-02T03:04:05Z"}`,
			`{"clock":"08:30:06Z"}`,
			`{"day":"2020-02-29"}`,
			`{"mail":"a@b.test"}`,
			`{"host":"example.test"}`,
			`{"span":"P4DT12H30M5S"}`,
			`{"span":"P2W"}`,
			// RFC 1123 permits a hyphen anywhere but the first and last
			// character of a label, including two in a row and including the
			// 3rd and 4th position. The 3rd-and-4th restriction is RFC 5891
			// section 4.2.3.1, an IDNA rule about A-labels, and applying it to
			// every label refused these two conforming names. Their control is
			// "XN--aa---o47jg78q" in the invalid list, whose decoded U-label
			// "aa--點看" does break that rule.
			`{"host":"ab--cd.example"}`,
			`{"host":"a--b.com"}`,
			// The duration grammar of RFC 3339 appendix A nests rather than
			// lists, so each designator may be followed only by the one
			// immediately after it -- but any of them may open the half. These
			// are the openings and the legal adjacencies, and they are the
			// control for the three rejections below: a rule that demanded the
			// full YMD or HMS run would refuse every one of them.
			`{"span":"P1Y2M"}`, `{"span":"P1M2D"}`, `{"span":"P1Y2M3D"}`,
			`{"span":"PT1H2M"}`, `{"span":"PT1M2S"}`, `{"span":"PT1H2M3S"}`,
			`{"span":"P1Y2M3DT4H5M6S"}`, `{"span":"P0D"}`, `{"span":"PT0S"}`,
			`{"link":"https://example.test/x"}`,
			`{"id":"123e4567-e89b-12d3-a456-426614174000"}`,
			`{"relPointer":"0"}`, `{"relPointer":"1/a"}`, `{"relPointer":"2#"}`,
			// RFC 6570. These are the accept-controls for the expression
			// rejections below, and the whole risk of issue #169's fix: this is
			// the format where an over-strict reading turns a conforming
			// template into a refused document.
			//
			// The corpus supplies four of them -- the two dictionary templates,
			// "" and the two literal-boundary cases -- and nothing at either
			// end of the max-length range, so "{v:1}" and "{v:9999}" are
			// written here. Without those two a check that refused every prefix
			// would satisfy the whole invalid list below.
			`{"template":"http://example.com/dictionary/{term:1}/{term}"}`,
			`{"template":"dictionary/{term:1}/{term}"}`,
			`{"template":"http://example.com/dictionary"}`,
			`{"template":""}`,
			`{"template":"a%41b"}`, `{"template":"a😀b"}`,
			// The two ends of max-length = %x31-39 0*3DIGIT.
			`{"template":"{v:1}"}`, `{"template":"{v:9999}"}`,
			// Every operator RFC 6570 defines -- op-level2, op-level3 and the
			// five op-reserve characters. "{,x}" is the sharp one: "," is an
			// operator there, so a check that split the body on "," before
			// looking for one would report an empty leading varspec and refuse
			// a template the grammar admits.
			`{"template":"{+path}"}`, `{"template":"{#frag}"}`,
			`{"template":"{.dom}"}`, `{"template":"{/seg}"}`,
			`{"template":"{;p}"}`, `{"template":"{?a,b}"}`, `{"template":"{&q}"}`,
			`{"template":"{=x}"}`, `{"template":"{,x}"}`, `{"template":"{!x}"}`,
			`{"template":"{@x}"}`, `{"template":"{|x}"}`,
			// A bare varspec, a list of them, and both modifiers.
			`{"template":"{x,y,z}"}`, `{"template":"{v*}"}`, `{"template":"{.dom*}"}`,
			`{"template":"{a_b.c%20d}"}`,
			// The under-enforcement the check documents: varname is taken to be
			// any non-empty run, and the literal character set is not checked.
			// RFC 6570 refuses all five. They are here so the gap is a recorded
			// verdict rather than something a later reader has to rediscover by
			// planting it.
			`{"template":"{v-w}"}`, `{"template":"{a b}"}`, `{"template":"{v%2}"}`,
			`{"template":"a b"}`, `{"template":"a%zzb"}`,
		},
		[]string{
			// The leap second is only the last second of the UTC day, so the
			// rule is arithmetic rather than "60 is allowed".
			`{"clock":"22:59:60Z"}`,
			`{"clock":"23:58:60Z"}`,
			`{"clock":"23:59:60+01:00"}`,
			`{"clock":"23:59:60-01:00"}`,
			`{"stampStr":"1998-12-31T23:58:60Z"}`,
			`{"stampStr":"1998-12-31T23:59:61Z"}`,
			// Issue #274, and the reason the README says a date-time field
			// cannot hold a leap second: the suite marks this document valid,
			// and a property held as a time.Time refuses it while decoding,
			// before any check runs. It is here as a recorded verdict rather
			// than an accident -- the same value is accepted three rows up
			// through stampLen, and through stampStr above.
			`{"stamp":"1998-12-31T23:59:60Z"}`,
			// The escape route does not weaken the check. Second 60 is only ever
			// the last second of a UTC day, and stampLen still says so.
			`{"stampLen":"1998-12-31T23:58:60Z"}`,
			`{"stampLen":"1998-12-31T23:59:61Z"}`,
			`{"stampLen":"not-a-timestamp"}`,
			// An offset is not optional in an RFC 3339 full-time.
			`{"clock":"12:00:00"}`,
			`{"clock":"01:02:03Z+00:30"}`,
			`{"clock":"01:02:03+24:00"}`,
			// Non-ASCII digits are not digits here.
			`{"stampStr":"1963-06-11T0৪:00:00Z"}`,
			`{"day":"1963-06-1৪"}`,
			// "" is not an email address, a hostname, a UUID, a duration or a
			// relative JSON Pointer, and the exemption used to accept it as all
			// five.
			`{"mail":""}`,
			`{"host":""}`,
			`{"id":""}`,
			`{"span":""}`,
			`{"relPointer":""}`,
			`{"intlHost":""}`,
			// The duration grammar wants a value for every designator, a "T"
			// followed by something, and a week count that stands alone.
			`{"span":"P"}`,
			`{"span":"PT"}`,
			`{"span":"P1YT"}`,
			`{"span":"P1Y2W"}`,
			`{"span":"PT1D"}`,
			`{"span":"P1"}`,
			// ... and it nests, so a designator may not skip the one after it,
			// and no component carries a fraction. Reading the grammar as "in
			// order" rather than "adjacent" accepted the first two, and dur-second
			// is 1*DIGIT "S" with no fraction at all.
			`{"span":"P1Y2D"}`,
			`{"span":"PT1H2S"}`,
			`{"span":"PT0.5S"}`,
			// A URI's character set excludes each of these, and url.Parse takes
			// them all.
			`{"link":"https://example.org/foo bar.txt"}`,
			`{"link":"https://example.org/foobar\\.txt"}`,
			`{"link":"https://example.org/foobar<>.txt"}`,
			`{"link":"https://example.org/foobar{}.txt"}`,
			`{"link":"https://example.org/foobar|.txt"}`,
			`{"link":"https://example.org/foobar®.txt"}`,
			`{"link":"/abc"}`,
			`{"linkRef":"\\\\WINDOWS\\fileshare"}`,
			// A bare IPv6 address is not a host; only the bracketed form is.
			`{"link":"http://2001:0db8:85a3:0000:0000:8a2e:0370:7334"}`,
			// ContextO, from RFC 5892 appendix A.3-A.9. x/net/idna implements
			// ContextJ and not these, so they are the rules schemagenContextO
			// adds -- and each has its satisfied twin in the valid list above.
			`{"intlHost":"a·l"}`,     // MIDDLE DOT with no preceding 'l'
			`{"intlHost":"·l"}`,      // ... with nothing preceding
			`{"intlHost":"l·a"}`,     // ... with no following 'l'
			`{"intlHost":"l·"}`,      // ... with nothing following
			`{"intlHost":"α͵S"}`,     // KERAIA not followed by Greek
			`{"intlHost":"α͵"}`,      // ... followed by nothing
			`{"intlHost":"׳ב"}`,      // GERESH not preceded by anything
			`{"intlHost":"״ב"}`,      // GERSHAYIM not preceded by anything
			`{"intlHost":"def・abc"}`, // KATAKANA MIDDLE DOT with no Kana or Han
			`{"intlHost":"・"}`,       // ... with nothing else at all
			// The same rules reached through punycode: an A-label is decoded
			// before it is judged, which the ASCII check could not do.
			`{"host":"xn--al-0ea"}`, // MIDDLE DOT with no preceding 'l'
			`{"host":"xn--wva3j"}`,  // KERAIA followed by nothing
			`{"host":"xn--5db1e"}`,  // GERESH preceded by nothing
			`{"host":"xn--vek"}`,    // KATAKANA MIDDLE DOT alone
			`{"host":"xn--X"}`,      // not punycode at all
			// The RFC 5892 section 2.6 exceptions whose derived property is
			// DISALLOWED. UTS-46 lookup marks all ten valid and maps them
			// through, so idna accepts every one of these; schemagenDisallowedException
			// is what refuses them. The accept-controls are the PVALID members of
			// the same section -- "ßς་〇" and "۽۾" in the valid list above -- which
			// a check written over the section rather than over the property would
			// have refused alongside them.
			`{"intlHost":"실〮례.테스트"}`,  // U+302E inside a label
			`{"intlHost":"ـߺ"}`,       // U+0640, U+07FA
			`{"intlHost":"〱〲〳〴〵〮〯〻"}`, // U+3031..U+3035, U+302E, U+302F, U+303B
			// And the same three reached through punycode.
			`{"host":"xn--07jt112bpxg.xn--9t4b11yi5a"}`,
			`{"host":"xn--chb89f"}`,
			`{"host":"xn--07jceefgh4c"}`,
			// The suite spends only three documents on ten code points, so eight of
			// the ten rules could be deleted and all three would still be refused
			// by the two that remained. One case per code point, in a carrier the
			// valid list above shows is accepted on its own, is what makes each of
			// them load-bearing.
			`{"intlHost":"بـب"}`, // U+0640 ARABIC TATWEEL
			`{"intlHost":"ߊߺߊ"}`, // U+07FA NKO LAJANYALAN
			`{"intlHost":"실〮례"}`, // U+302E HANGUL SINGLE DOT TONE MARK
			`{"intlHost":"실〯례"}`, // U+302F HANGUL DOUBLE DOT TONE MARK
			`{"intlHost":"あ〱あ"}`, // U+3031 VERTICAL KANA REPEAT MARK
			`{"intlHost":"あ〲あ"}`, // U+3032 ... WITH VOICED SOUND MARK
			`{"intlHost":"あ〳あ"}`, // U+3033 ... UPPER HALF
			`{"intlHost":"あ〴あ"}`, // U+3034 ... WITH VOICED SOUND MARK UPPER HALF
			`{"intlHost":"あ〵あ"}`, // U+3035 ... LOWER HALF
			`{"intlHost":"あ〻あ"}`, // U+303B VERTICAL IDEOGRAPHIC ITERATION MARK
			// A trailing separator is the DNS root label, which a hostname does
			// not carry. idna's lookup profile tolerates it.
			`{"intlHost":"example."}`,
			`{"intlHost":"example。"}`,
			// The A-label rules of RFC 5890 section 2.3.2.1, which are about what
			// the label decodes to and so are invisible to anything that reads
			// its ASCII spelling. idna accepts all three: the first two are
			// LDH-valid whatever they encode, and the third it re-encodes as
			// plain "example" and hands back without a word.
			//
			// "aa--點看" breaks the 3rd-and-4th hyphen rule; "¡" is punctuation
			// and DISALLOWED by RFC 5892, which UTS-46 does not implement; and
			// "example" is no U-label at all, since a U-label has at least one
			// non-ASCII character. Written against both hostname formats,
			// because the gate that stopped hostname applying the IDNA pass to a
			// plain label must not stop it applying to an A-label.
			`{"host":"XN--aa---o47jg78q"}`,
			`{"host":"xn--7a"}`,
			`{"intlHost":"xn--7a"}`,
			`{"host":"xn--example-"}`,
			`{"intlHost":"xn--example-"}`,
			// RFC 6570, and the four forms issue #169 named. The check balanced
			// braces and looked no further, so an expression could hold
			// anything at all -- including nothing.
			`{"template":"{}"}`,        // no variable-list
			`{"template":"{a,,b}"}`,    // an empty varspec inside one
			`{"template":"{v:0}"}`,     // max-length is 1..9999, so not zero
			`{"template":"{v:10000}"}`, // ... and at most four digits
			// The same three rules at their other edges. Each of the four above
			// is one point of a rule, and a check written to refuse exactly
			// those four strings would pass this test without implementing any
			// of them.
			`{"template":"{,}"}`,    // an operator with no variable-list after it
			`{"template":"{+}"}`,    //
			`{"template":"{a,}"}`,   // a trailing empty varspec
			`{"template":"{v:}"}`,   // a prefix with no max-length
			`{"template":"{v:01}"}`, // a leading zero, which %x31-39 excludes
			`{"template":"{*}"}`,    // an explode with no varname
			`{"template":"{v:1*}"}`, // modifier-level4 is a prefix or an explode, not both
			// The two brace failures, which the balance check already refused
			// and which the rewrite must keep refusing.
			`{"template":"http://example.com/dictionary/{term:1}/{term"}`,
			`{"template":"foo}bar"}`,
			`{"template":"{a{b}"}`, // a "{" inside an expression body
			// Still the ordinary failures.
			`{"mail":"2962"}`,
			`{"host":"-leading-hyphen"}`,
			`{"day":"2021-02-29"}`,
			`{"id":"123e4567-e89b-12d3"}`,
		},
	)
}

// TestTypedFormatIsAssertedEverywhere covers a declared string with a format --
// {"type":"string","format":"ipv4"} -- in every position it can be written.
//
// Four of the nine asserted nothing, and they are the four this test exists
// for. An array element and a map value went through elementRules, whose switch
// dropped every keyword it had not been taught, and `format` was one. A tuple
// slot fell back to checking the position's JSON type, which answered "string":
// true, and silent about the format. A oneOf branch fell back to a bare Go
// string, which carries no Validate for the union's dispatch to call. In all
// four an IPv6 address satisfied an ipv4 schema, while the identical subschema
// written as a property, behind a $ref or under an allOf was checked -- the
// ninth instance in this repository of a keyword bound in one position and
// dropped in its sibling.
//
// Run under the asserting configuration because the fixture declares 2020-12,
// where format is an annotation. TestFormatPostureFollowsTheDialect is what
// holds the other side of that.
func TestTypedFormatIsAssertedEverywhere(t *testing.T) {
	runValidationCasesWithConfig(t,
		"testdata/schemas/regression/typed_format_positions.json",
		formatAssertingConfig(),
		[]string{
			`{}`,
			// A conforming address, position by position.
			`{"inline":"192.0.2.7"}`,
			`{"ref":"192.0.2.7"}`,
			`{"chain":"192.0.2.7"}`,
			`{"list":["192.0.2.7","192.0.2.8"]}`,
			`{"map":{"k":"192.0.2.7"}}`,
			`{"tuple":["192.0.2.7",1]}`,
			`{"branch":"192.0.2.7"}`,
			`{"wrapped":"192.0.2.7"}`,
			`{"buckets":{"pp":"192.0.2.7"}}`,
			// Empty containers, and a key no pattern claims: adding a check to a
			// position must not make the position itself mandatory.
			`{"list":[]}`, `{"map":{}}`, `{"buckets":{}}`,
			`{"buckets":{"zz":"not-an-ip"}}`,
			// The other branch of the oneOf still selects.
			`{"branch":7}`,
			// The two formats whose Go type is not netip.Addr, through the
			// element position that dropped them.
			`{"mailList":["a@b.test"]}`,
			`{"stampList":["2020-01-02T03:04:05Z"]}`,
		},
		[]string{
			// A well-formed address of the wrong family: it parses, so only the
			// format assertion can reject it. The four positions this test is
			// about come first.
			`{"list":["192.0.2.7","2001:db8::1"]}`,
			`{"map":{"k":"2001:db8::1"}}`,
			`{"tuple":["2001:db8::1",1]}`,
			`{"branch":"2001:db8::1"}`,
			// And the five that already worked, so a change that moved the check
			// rather than adding one is still caught.
			`{"inline":"2001:db8::1"}`,
			`{"ref":"2001:db8::1"}`,
			`{"chain":"2001:db8::1"}`,
			`{"wrapped":"2001:db8::1"}`,
			`{"buckets":{"pp":"2001:db8::1"}}`,
			// The other two formats, in the element position.
			`{"mailList":["not-an-email"]}`,
			`{"stampList":["not-a-timestamp"]}`,
		},
	)
}

// TestTypedFormatAnnotatesOnNewerDrafts is the accept-control for the test
// above, at the same fixture and the ordinary configuration.
//
// Every document the assertion run rejects is valid here, because 2020-12 makes
// format an annotation -- so this is what catches a change that reaches the four
// new positions but forgets the posture, which would be a false rejection in
// four more places rather than a fix.
func TestTypedFormatAnnotatesOnNewerDrafts(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/typed_format_positions.json",
		[]string{
			`{"list":["192.0.2.7","2001:db8::1"]}`,
			`{"map":{"k":"2001:db8::1"}}`,
			`{"tuple":["2001:db8::1",1]}`,
			`{"branch":"2001:db8::1"}`,
			`{"inline":"2001:db8::1"}`,
			`{"ref":"2001:db8::1"}`,
			`{"wrapped":"2001:db8::1"}`,
			`{"buckets":{"pp":"2001:db8::1"}}`,
			`{"mailList":["not-an-email"]}`,
			`{"stampList":["not-a-timestamp"]}`,
		},
		nil,
	)
}

// TestFormatHelperPositionsCompileAndCheck covers the formats whose check is a
// shared helper rather than a decode, in the container positions.
//
// The compile is the first assertion and the one this exists for. A format on an
// array element or a map value emitted a call to schemagenFormatIPv4Addr,
// schemagenFormatEmail and the rest while the helper file declared none of them,
// because which helpers a package needs was decided by walking the IR and the
// walk did not look at ItemValidations. Generated code that does not build is
// the worst thing this generator can produce, and nothing caught it: every
// harness derived the helper set from the emitted source instead of asking the
// generator, so every harness wrote the file the generator should have written.
//
// The fixture holds container positions only. A property, a tuple slot or a
// oneOf branch materializes a named type whose own check pulls the helper block
// in, which would mask the omission -- an earlier draft of this fixture had a
// tuple in it and the planted fault walked straight past.
//
// runValidationCases builds and runs the program, so the rejections below are
// the second assertion: the helpers are not merely declared, they are reached.
func TestFormatHelperPositionsCompileAndCheck(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/format_helper_positions.json",
		[]string{
			`{}`,
			`{"v4List":["192.0.2.7"]}`,
			`{"v6List":["2001:db8::1"]}`,
			`{"mailList":["a@b.test"]}`,
			`{"hostList":["example.test"]}`,
			`{"idnHostList":["실례.테스트"]}`,
			`{"idnMailList":["실례@실례.테스트"]}`,
			`{"uuidList":["123e4567-e89b-12d3-a456-426614174000"]}`,
			`{"durationList":["P4DT12H30M5S"]}`,
			`{"dateList":["2020-02-29"]}`,
			`{"timeList":["08:30:06Z"]}`,
			`{"uriList":["https://example.test/x"]}`,
			`{"regexList":["[^]"]}`,
			`{"v4Map":{"k":"192.0.2.7"}}`,
			`{"mailMap":{"k":"a@b.test"}}`,
			`{"hostMap":{"k":"example.test"}}`,
			`{"nested":[["192.0.2.7"]]}`,
			// Empty containers: a check on a position must not make the position
			// mandatory.
			`{"v4List":[],"v4Map":{},"nested":[]}`,
		},
		[]string{
			`{"v4List":["2001:db8::1"]}`,
			`{"v6List":["192.0.2.7"]}`,
			`{"mailList":["not-an-email"]}`,
			`{"hostList":["-leading-hyphen"]}`,
			`{"idnHostList":["example."]}`,
			`{"idnMailList":["not-an-email"]}`,
			`{"uuidList":["123e4567-e89b-12d3"]}`,
			`{"durationList":["P1Y2W"]}`,
			`{"dateList":["2021-02-29"]}`,
			`{"timeList":["12:00:00"]}`,
			`{"uriList":["/abc"]}`,
			`{"v4Map":{"k":"2001:db8::1"}}`,
			`{"mailMap":{"k":"not-an-email"}}`,
			`{"hostMap":{"k":"-leading-hyphen"}}`,
			`{"nested":[["2001:db8::1"]]}`,
		},
	)
}

// TestDraft3FormatSpellingsAreChecked is the behavioural half of draft 3's three
// own format names, compiled and run rather than read off the IR.
//
// "host-name" and "ip-address" are the same formats every later draft spells
// "hostname" and "ipv4", and the two modern properties beside them are the
// control: they were always checked, and a fix that rerouted the keyword rather
// than adding the older spelling would show up as one of them going quiet.
//
// "color" is the one with no counterpart, and the cases are what draft 3 section
// 5.23 names: CSS 2.1, by its dated URI. That is why "#00332520" is refused --
// eight hex digits is #RRGGBBAA, which CSS Color 4 added and 2.1 does not have,
// and the official suite marks it invalid. The accepted list carries the
// notations 2.1 does define, so a check that passed by refusing everything with
// a "#" in it could not get through.
//
// Every property is a format with no declared "type", so the wrapper accepts a
// value of any other JSON type and asserts only on a string. The non-string
// documents below are the controls for that: refusing one would be rejecting
// what the schema permits.
func TestDraft3FormatSpellingsAreChecked(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/draft3_format_spellings.json",
		[]string{
			`{}`,
			`{"host":"www.example.com"}`,
			`{"host":"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijk.com"}`,
			`{"addr":"192.168.0.1"}`,
			`{"colour":"fuchsia"}`,
			`{"colour":"FUCHSIA"}`,
			`{"colour":"#CC8899"}`,
			`{"colour":"#C89"}`,
			`{"colour":"#c89"}`,
			`{"colour":"transparent"}`,
			`{"colour":"ButtonFace"}`,
			`{"colour":"rgb(255,0,0)"}`,
			`{"colour":"rgb(100%, 0%, 0%)"}`,
			`{"colour":"rgb(-10, 300, 0)"}`,
			// The modern spellings, which were checked before and must stay so.
			`{"modernHost":"www.example.com"}`,
			`{"modernAddr":"192.168.0.1"}`,
			// Not strings, so no format speaks about them at all.
			`{"colour":5,"host":true,"addr":null}`,
			`{"host":{"a":1},"addr":["x"]}`,
		},
		[]string{
			`{"host":"not_a_valid_host_name"}`,
			`{"host":"-hostname"}`,
			`{"host":"hostname-"}`,
			`{"host":"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijkl.com"}`,
			`{"host":""}`,
			`{"addr":"127.0.0.0.1"}`,
			`{"addr":"256.256.256.256"}`,
			`{"colour":"puce"}`,
			`{"colour":"light_grayish_red-violet"}`,
			`{"colour":"#00332520"}`,
			`{"colour":"#CC88"}`,
			`{"colour":"#GG8899"}`,
			`{"colour":""}`,
			// CSS Color 3 and 4 additions, which the specification draft 3 names
			// does not have.
			`{"colour":"rebeccapurple"}`,
			`{"colour":"hsl(0, 100%, 50%)"}`,
			// rgb() with the wrong arity, a mixed unit, or a non-number.
			`{"colour":"rgb(255,0)"}`,
			`{"colour":"rgb(255,0%,0)"}`,
			`{"colour":"rgb(a,b,c)"}`,
			// The modern spellings, whose rejections must survive too.
			`{"modernHost":"not_a_valid_host_name"}`,
			`{"modernAddr":"256.256.256.256"}`,
		},
	)
}

// TestOneOfBooleanAndConstBranches is the behavioural half of issue #125.
//
// The sealed-interface union decides a branch by decoding the value into that
// branch's Go type. A branch that resolves to `any` -- a const, an enum, a bare
// bound -- decodes everything, and a `false` branch, which no instance
// satisfies, was given the same `any` and so matched everything too. Either one
// takes the count past 1 for a document satisfying exactly one *other* branch,
// which the exactly-one rule then refuses, and holds it at 1 for a document
// satisfying none, which it then accepts. Both directions are here: every
// document under `valid` naming "mixed", "falseBranch" or "typedEnumBranch" was
// refused before the fix, and {"falseBranch":{"j":"b"}} was accepted by the one
// branch guaranteed to reject it.
//
// The last three properties are the narrowness control. Their branches are ones
// selection judges correctly -- two objects, two scalars, and an object beside
// boolean `true`, which really is matched by every instance -- so they keep the
// union, and the verdicts below are the ones it already reached.
func TestOneOfBooleanAndConstBranches(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/oneof_boolean_and_const_branches.json",
		[]string{
			`{}`,
			`{"mixed":{"k":"a"}}`, // the object branch alone
			`{"mixed":"x"}`,       // the const branch alone
			`{"falseBranch":{"k":"a"}}`,
			`{"typedEnumBranch":{"k":10}}`,
			`{"typedEnumBranch":"a"}`,
			`{"objectsOnly":{"k":"a"}}`,
			`{"objectsOnly":{"j":"b"}}`,
			`{"scalarsOnly":"a"}`,
			`{"scalarsOnly":1}`,
			`{"trueBranch":{"j":"b"}}`, // the true branch alone
			`{"trueBranch":"x"}`,
		},
		[]string{
			`{"mixed":123}`,                     // no branch
			`{"mixed":{"k":1}}`,                 // the object branch's own type
			`{"falseBranch":{"j":"b"}}`,         // `false` matched it before the fix
			`{"falseBranch":"x"}`,               //
			`{"typedEnumBranch":"b"}`,           // the branch's enum, tested nowhere before
			`{"typedEnumBranch":{"k":1}}`,       // the object branch's minimum
			`{"objectsOnly":{"k":"a","j":"b"}}`, // both branches
			`{"objectsOnly":"x"}`,
			`{"scalarsOnly":true}`,
			`{"trueBranch":{"k":"a"}}`, // both branches
		},
	)
}

// TestOneOfBooleanAndConstRoot is issue #125's own reproducer, where the group
// is the whole document.
//
// The root union lives in a Go struct, so the miscount was only half of it:
// UnmarshalJSON opened by decoding the document into that struct, and "x" --
// which the const branch admits -- is not an object, so encoding/json refused it
// before a branch was tried. Both reference implementations the issue was
// checked against accept {"k":"a"} and "x", and reject 123.
func TestOneOfBooleanAndConstRoot(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/oneof_boolean_and_const_root.json",
		[]string{
			`{"k":"a"}`,
			`"x"`,
		},
		[]string{
			`123`,
			`{"k":1}`,
			`{"other":1}`, // satisfies no branch; the `false` branch used to carry it
			`"y"`,
		},
	)
}

// TestOneOfRootScalarBranchDecodes is the other half of the same struct
// problem, on a union that selects perfectly well.
//
// Nothing is wrong with the branches here: an object and a string are told apart
// by decoding. The document still never reached them, because the struct decode
// ran first and a string does not go into a Go struct. The union is kept and
// that decode is dropped, which is sound only because a struct standing for
// nothing but a top-level oneOf has no field for it to fill.
func TestOneOfRootScalarBranchDecodes(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/oneof_root_scalar_branch.json",
		[]string{
			`{"k":"a"}`,
			`"x"`,
		},
		[]string{
			`123`,
			`true`,
			`{"j":"b"}`, // neither branch: no "k", and not a string
		},
	)
}

// TestFalsePropertyRefusesExplicitNull is the behavioural half of issue #127.
//
// A property whose schema is `false` is satisfied by no value, so the key's
// presence is the violation. The rule was emitted as `field != nil`, and
// encoding/json leaves a nil `any` for an explicit null exactly as it does for
// an absent property -- so {"inline":null} passed. The verdict comes from
// _jsonKeys now, which is where the document's own keys survive the decode.
//
// The $ref spelling was already refused, by the forbidding wrapper its target
// generates; it is here as the control that the two spellings agree. The `ok`
// and empty documents are the control that presence, rather than the property
// merely being declared, is what is tested.
func TestFalsePropertyRefusesExplicitNull(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/false_property_explicit_null.json",
		[]string{
			`{}`,
			`{"ok":"y"}`,
			`{"nested":{"ok":"y"}}`,
			`{"nested":{}}`,
			`{"list":[{}]}`,
			`{"list":[]}`,
		},
		[]string{
			`{"inline":null}`, // the defect
			`{"inline":1}`,
			`{"viaRef":null}`,
			`{"viaRef":1}`,
			`{"nested":{"inner":null}}`,
			`{"nested":{"inner":1}}`,
			`{"list":[{"inner":null}]}`,
			`{"list":[{"inner":1}]}`,
		},
	)
}

// TestAnyOfBooleanAndScalarBranches is the behavioural half of issue #133.
//
// The anyOf merge folds every branch into one Go struct and then approximates
// "at least one branch matches" from the branches' required keys and property
// checks. Both halves were wrong at once for a branch that says neither.
//
// The struct is the false rejection: only an object decodes into it, so
// {"mixed":"x"} -- which the string branch admits -- was refused by
// encoding/json before a branch was tried, and so were every const, `not` and
// `true` branch's scalars. It is not only scalars: the struct's fields carry the
// types the merge picked, so {"bareObjectBranch":{"k":1}} was refused too,
// though the bare "type":"object" branch beside it admits every object. The
// approximation is the false acceptance: it answers nil as soon as one branch
// states neither a required key nor a property check, which drops the whole
// assertion, so {"falseBranch":{"j":"b"}} passed on the strength of the branch
// written to reject everything.
//
// The last group is the narrowness control. Two object branches keyed on
// required keys are what the summary was written for; it judges them correctly,
// keeps the merge, and answers exactly what it answered before.
//
// Every verdict is what python-jsonschema and js-ajv both answer.
func TestAnyOfBooleanAndScalarBranches(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/anyof_boolean_and_scalar_branches.json",
		[]string{
			`{}`,
			`{"mixed":{"k":"a"}}`, // the object branch alone
			`{"mixed":"x"}`,       // the string branch alone
			`{"constBranch":{"k":"a"}}`,
			`{"constBranch":"x"}`, // the const branch alone
			`{"falseBranch":{"k":"a"}}`,
			`{"notBranch":{"k":"a"}}`,
			`{"notBranch":"x"}`, // not an object, so the `not` branch admits it
			`{"notBranch":123}`,
			`{"bareObjectBranch":{"k":"a"}}`,
			`{"bareObjectBranch":{"j":"b"}}`,
			`{"bareObjectBranch":{"k":1}}`, // every object satisfies the bare branch
			`{"trueBranch":{"k":"a"}}`,
			`{"trueBranch":"x"}`, // `true` admits every document
			`{"trueBranch":123}`,
			`{"trueBranch":{"j":"b"}}`,
			`{"trueBranch":{"k":1}}`,
			`{"objectsOnly":{"k":"a"}}`,
			`{"objectsOnly":{"j":"b"}}`,
			`{"objectsOnly":{"k":"a","j":"b"}}`,
		},
		[]string{
			`{"mixed":{"j":"b"}}`, // no branch: no "k", and not a string
			`{"mixed":123}`,
			`{"mixed":{"k":1}}`, // the object branch's own type
			`{"constBranch":"y"}`,
			`{"constBranch":{"j":"b"}}`,
			`{"constBranch":123}`,
			`{"falseBranch":{"j":"b"}}`, // `false` carried it before the fix
			`{"falseBranch":"x"}`,
			`{"notBranch":{"j":"b"}}`, // an object, so the `not` branch refuses it
			`{"bareObjectBranch":"x"}`,
			`{"bareObjectBranch":123}`,
			`{"objectsOnly":{"z":1}}`,
			`{"objectsOnly":"x"}`,
		},
	)
}

// TestAnyOfScalarBranchRoot is issue #133's first table, where the group is the
// whole document.
//
// "x" is what the string branch exists to admit, and the merged struct is what
// refused it. {"j":"b"} satisfies neither branch and was accepted because the
// summary the merge attaches had declined to speak for a branch it could not
// read.
func TestAnyOfScalarBranchRoot(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/anyof_scalar_branch_root.json",
		[]string{
			`"x"`,
			`{"k":"a"}`,
		},
		[]string{
			`{"j":"b"}`, // satisfies neither branch
			`123`,
			`{"k":1}`,
			`true`,
		},
	)
}

// TestAnyOfFalseBranchRoot is issue #133's second table.
//
// Nothing satisfies a `false` branch, so it can carry no document out of the
// merged struct and the struct stays. What it cannot do is state a required key
// or a property check, which is the whole vocabulary of the summary -- so the
// summary was dropped and {"j":"b"} was accepted with no branch admitting it.
// The applicator is evaluated against the document instead.
func TestAnyOfFalseBranchRoot(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/anyof_false_branch_root.json",
		[]string{
			`{"k":"a"}`,
		},
		[]string{
			`{"j":"b"}`, // the defect
			`"x"`,
			`123`,
			`{"k":1}`,
		},
	)
}

// TestIfBooleanBranchPositions is issue #134.
//
// An if/then/else whose `if` or whose branch is a boolean schema resolved to
// `any` in a property, and Go forbids methods on `any` -- so the position had no
// Validate for the keyword to live in and json.Unmarshal into it cannot fail,
// which left the keyword enforced nowhere while the same schema at a document
// root was judged correctly. Six of the seven rejections below were accepted
// before issue #126 named the position; the seventh, viaRef, is the control that
// the $ref spelling already agreed and still does.
//
// Every verdict is what python-jsonschema and js-ajv both answer.
func TestIfBooleanBranchPositions(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/if_boolean_branch_positions.json",
		[]string{
			`{}`,
			`{"ifFalse":1}`,     // `if:false` never matches, so `else` governs
			`{"thenFalse":"a"}`, // the `if` does not match, so `then` does not apply
			`{"ifTrue":1}`,      // `if:true` always matches, so `then` governs
			`{"elseFalse":1}`,
			`{"list":[1]}`,
			`{"list":[]}`,
			`{"viaRef":1}`,
		},
		[]string{
			`{"ifFalse":"a"}`,
			`{"ifFalse":true}`,
			`{"thenFalse":1}`, // `then:false` refuses every instance the `if` matches
			`{"ifTrue":"a"}`,
			`{"elseFalse":"a"}`,
			`{"list":["a"]}`,
			`{"viaRef":"a"}`,
		},
	)
}

// TestAnyOfBranchUnevaluatedPropertiesIsPerDocument covers issue #111: an
// `unevaluatedProperties` inside an `anyOf` branch was enforced whether or not
// that branch matched the document.
//
// The first four inputs are the point. {"a":1} satisfies the *second* branch,
// which states no `unevaluatedProperties` at all, and a branch the document
// fails contributes nothing -- neither its assertions nor the annotations the
// keyword reads. The generated code applied the first branch's keyword
// unconditionally and refused it, which two reference implementations call
// valid. A false rejection is worse than a missing check, so these four are the
// accept-controls that must never come back.
//
// The last two are the reject-controls beside them, and they are why the fix is
// an exact evaluation rather than a deletion: {"b":2,"c":3} fails the first
// branch on c and the second on the missing `a`, so no branch holds and the
// document really is invalid. Dropping the keyword would accept it.
func TestAnyOfBranchUnevaluatedPropertiesIsPerDocument(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/anyof_branch_unevaluated_properties.json",
		[]string{
			`{"a":1}`,
			`{"b":2}`,
			`{"a":1,"b":2}`,
			// No key is unevaluated, so the first branch holds vacuously.
			`{}`,
		},
		[]string{
			`{"b":2,"c":3}`,
			`{"c":3}`,
		},
	)
}

// TestAnyOfBranchUnevaluatedPropertiesWithoutParentProperties is the same
// keyword reached by the other route into a struct.
//
// A parent that declares no properties of its own goes through generateAnyOfDef,
// which builds a merged schema without the anyOf and hands that to
// generateStructDef -- so the collector inside it never saw the keyword and the
// applicator went unchecked entirely. The branch here also carries a $ref beside
// its own `properties`, which is the second half: from 2019-09 a $ref is an
// ordinary applicator, so the branch's own keyword applies and what the $ref
// evaluates is exempt from it. Reading only the target, as the allOf collector
// does, found no keyword and left this unchecked too.
func TestAnyOfBranchUnevaluatedPropertiesWithoutParentProperties(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/anyof_branch_unevaluated_no_properties.json",
		[]string{
			// The $ref evaluates x, the sibling properties evaluate y.
			`{"x":1,"y":2}`,
			// The second branch matches and states no unevaluatedProperties.
			`{"q":1,"z":2}`,
			`{}`,
		},
		[]string{
			`{"x":1,"z":2}`,
			`{"y":"no"}`,
		},
	)
}

// TestOneOfBranchUnevaluatedPropertiesIsPerDocument is issue #111 reached
// through `oneOf`, where the count makes the false rejection sharper.
//
// {"a":1,"b":1} satisfies the second branch alone: the first branch declares
// only `b`, so `a` is unevaluated and `unevaluatedProperties:false` fails it.
// The flattened approximation decides whether a branch matches from its required
// keys, its consts and its declared types, none of which mention `a`, so it
// counted two matches and reported "expected exactly 1". That approximation is
// dropped where the applicator is evaluated exactly; this is the control that
// says so.
func TestOneOfBranchUnevaluatedPropertiesIsPerDocument(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/oneof_branch_unevaluated_properties.json",
		[]string{
			`{"a":1}`,
			`{"b":1}`,
			`{"a":1,"b":1}`,
		},
		[]string{
			// Fails the first branch on z and the second on the missing a.
			`{"b":1,"z":2}`,
			`{"z":1}`,
			`{}`,
		},
	)
}

// TestConstraintOnlyPositionsAreChecked covers issue #126: a schema that
// constrains a value without naming a type collapsed to `any` everywhere except
// a document root.
//
// `any` is interface-underlying, so Go forbids methods on it: such a position
// had no Validate for a check to live in and json.Unmarshal into it could not
// fail, which turned every one of these keywords into nothing at all. The
// fixture writes one such schema into each position the generator reaches by a
// different path -- a property, an array element, a map value, a tuple slot, a
// composition branch, a type union, an array of nulls -- because this repository
// has repeatedly fixed one of them and left its siblings, and only a case per
// position can tell the two apart.
//
// The valid half is the narrowness control. Every one of these documents was
// accepted before and must still be: the wrapper introduced here changes the Go
// type of the field, and a wrapper that rejected what the schema permits would
// be a worse defect than the one being fixed.
func TestConstraintOnlyPositionsAreChecked(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/constraint_only_positions.json",
		[]string{
			`{}`,
			`{"prop":1}`,
			`{"list":[1]}`,
			`{"map":{"k":"abcd"}}`,
			`{"tuple":[1]}`,
			`{"branch":5}`,
			`{"branch":"s"}`,
			`{"union":"x"}`,
			`{"union":5}`,
			`{"nulls":[null]}`,
			`{"unevaluated":{"a":1}}`,
			`{"unevaluated":{"b":2}}`,
		},
		[]string{
			`{"prop":{"foo":"x"}}`,
			`{"list":[{"foo":"x"}]}`,
			`{"map":{"k":"ab"}}`,
			`{"tuple":["s"]}`,
			`{"branch":1}`,
			`{"union":true}`,
			`{"nulls":[1]}`,
			`{"unevaluated":{"c":3}}`,
		},
	)
}

// TestPresentNullSurvivesAndDoesNotConstrain is the behavioural half of issue
// #110. The golden pins the shape of the generated code and the round-trip case
// pins the marshalling; this is the part neither reaches -- what Validate does
// with a property the document wrote as null.
//
// Both directions and both configurations are covered, because the defect had a
// different face in each. Under the default flags the null was accepted and then
// dropped on the way out; under --omit-empty=false no optional field is
// pointer-wrapped, so the null left the Go zero and the optional rule -- gated
// on key presence, which a present null satisfies -- measured that zero against
// a bound and rejected a document JSON Schema permits.
func TestPresentNullSurvivesAndDoesNotConstrain(t *testing.T) {
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

func decode(doc string) PresentNullPositions {
	var v PresentNullPositions
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		fail("%s: unmarshal: %v", doc, err)
	}
	return v
}

// The two constraint-only properties are held by the wrapper a schema stating
// no "type" gets since issue #139, whose value is its own business -- so a Go
// literal is spelled as the JSON document it stands for.
func boundOnly(doc string) PresentNullPositionsBoundOnly {
	var v PresentNullPositionsBoundOnly
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		fail("%s: unmarshal: %v", doc, err)
	}
	return v
}

func reqBoundOnly(doc string) PresentNullPositionsReqBoundOnly {
	var v PresentNullPositionsReqBoundOnly
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		fail("%s: unmarshal: %v", doc, err)
	}
	return v
}

func main() {
	// A null the schema permits is not a value any of these keywords judges.
	// Every one of these was rejected for the length of a string the document
	// never supplied.
	for _, doc := range []string{
		` + "`" + `{"untyped":null,"reqBoundOnly":"ok"}` + "`" + `,
		` + "`" + `{"boundOnly":null,"reqBoundOnly":"ok"}` + "`" + `,
		` + "`" + `{"reqBoundOnly":null}` + "`" + `,
		` + "`" + `{"nullableScalar":null,"reqBoundOnly":"ok"}` + "`" + `,
		` + "`" + `{"nullableList":null,"reqBoundOnly":"ok"}` + "`" + `,
		` + "`" + `{"nullableObject":null,"reqBoundOnly":"ok"}` + "`" + `,
		` + "`" + `{"refObject":null,"reqBoundOnly":"ok"}` + "`" + `,
		// A named type whose own Validate reads the nil the null left as an
		// array of length 0 and reports it short of minItems.
		` + "`" + `{"refList":null,"reqBoundOnly":"ok"}` + "`" + `,
	} {
		if err := decode(doc).Validate(); err != nil {
			fail("%s was rejected: %v -- the schema permits a null there", doc, err)
		}
	}

	// And that named type's own check still bites on an array that is there.
	if err := decode(` + "`" + `{"refList":[1],"reqBoundOnly":"ok"}` + "`" + `).Validate(); err == nil {
		fail("refList=[1] passed a minItems of 2")
	}

	// The accept-control: a null is vacuous, an actual short string is not.
	for _, doc := range []string{
		` + "`" + `{"boundOnly":"x","reqBoundOnly":"ok"}` + "`" + `,
		` + "`" + `{"reqBoundOnly":"x"}` + "`" + `,
		` + "`" + `{"nullableScalar":"x","reqBoundOnly":"ok"}` + "`" + `,
	} {
		if err := decode(doc).Validate(); err == nil {
			fail("%s passed a minLength of 2", doc)
		}
	}

	// And the property whose schema forbids a null keeps issue #103's refusal.
	var forbidden PresentNullPositions
	if err := json.Unmarshal([]byte(` + "`" + `{"typedString":null,"reqBoundOnly":"ok"}` + "`" + `), &forbidden); err == nil {
		fail("typedString accepted a null; its schema lists only \"string\"")
	}

	// The null comes back out, and an absent property stays absent.
	roundTrip := func(doc string) string {
		out, err := json.Marshal(decode(doc))
		if err != nil {
			fail("%s: marshal: %v", doc, err)
		}
		return string(out)
	}
	for _, tc := range []struct{ in, want string }{
		{` + "`" + `{"reqBoundOnly":"ok","untyped":null}` + "`" + `, ` + "`" + `{"reqBoundOnly":"ok","untyped":null}` + "`" + `},
		{` + "`" + `{"boundOnly":null,"reqBoundOnly":"ok"}` + "`" + `, ` + "`" + `{"boundOnly":null,"reqBoundOnly":"ok"}` + "`" + `},
		{` + "`" + `{"nullableScalar":null,"reqBoundOnly":"ok"}` + "`" + `, ` + "`" + `{"nullableScalar":null,"reqBoundOnly":"ok"}` + "`" + `},
		{` + "`" + `{"nullableList":null,"reqBoundOnly":"ok"}` + "`" + `, ` + "`" + `{"nullableList":null,"reqBoundOnly":"ok"}` + "`" + `},
		{` + "`" + `{"reqBoundOnly":"ok"}` + "`" + `, ` + "`" + `{"reqBoundOnly":"ok"}` + "`" + `},
		{` + "`" + `{"nullableList":[],"reqBoundOnly":"ok"}` + "`" + `, ` + "`" + `{"nullableList":[],"reqBoundOnly":"ok"}` + "`" + `},
	} {
		if got := roundTrip(tc.in); got != tc.want {
			fail("%s round-tripped to %s, want %s", tc.in, got, tc.want)
		}
	}

	// A value built in Go carried no document, so there is nothing recorded and
	// nothing invented: the absent properties are simply absent.
	built, err := json.Marshal(PresentNullPositions{ReqBoundOnly: reqBoundOnly(` + "`" + `"ok"` + "`" + `)})
	if err != nil {
		fail("marshal of a hand-built value: %v", err)
	}
	if string(built) != ` + "`" + `{"reqBoundOnly":"ok"}` + "`" + ` {
		fail("a hand-built value marshalled to %s", string(built))
	}

	// And an assignment after the decode is newer than what the document said.
	// The record must not write its null back over it.
	assigned := decode(` + "`" + `{"boundOnly":null,"untyped":null,"reqBoundOnly":"ok"}` + "`" + `)
	replacement := boundOnly(` + "`" + `"abc"` + "`" + `)
	assigned.BoundOnly = &replacement
	assigned.Untyped = 7
	out, err := json.Marshal(assigned)
	if err != nil {
		fail("marshal after assignment: %v", err)
	}
	if string(out) != ` + "`" + `{"boundOnly":"abc","reqBoundOnly":"ok","untyped":7}` + "`" + ` {
		fail("assigning over a decoded null marshalled to %s", string(out))
	}
	if err := assigned.Validate(); err != nil {
		fail("assigning over a decoded null was rejected: %v", err)
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t, "testdata/schemas/regression/present_null_positions.json", "present_null_test", mainGo)
}

// The same schema with --omit-empty=false, where no optional field is
// pointer-wrapped and every null therefore leaves a bare Go zero. That is the
// configuration issue #110 reports the false rejection under.
func TestPresentNullUnderValueFields(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	fail := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
		os.Exit(1)
	}
	for _, doc := range []string{
		` + "`" + `{"boundOnly":null,"reqBoundOnly":"ok"}` + "`" + `,
		` + "`" + `{"reqBoundOnly":null}` + "`" + `,
		` + "`" + `{"nullableScalar":null,"reqBoundOnly":"ok"}` + "`" + `,
		// Fields whose own type carries a Validate. Here they are struct values
		// rather than pointers, so the null reaches that Validate as a zero
		// struct missing a required key.
		` + "`" + `{"nullableObject":null,"reqBoundOnly":"ok"}` + "`" + `,
		` + "`" + `{"refObject":null,"reqBoundOnly":"ok"}` + "`" + `,
		` + "`" + `{"refList":null,"reqBoundOnly":"ok"}` + "`" + `,
	} {
		var v PresentNullPositions
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			fail("%s: unmarshal: %v", doc, err)
		}
		if err := v.Validate(); err != nil {
			fail("%s was rejected: %v -- with value fields the null leaves the Go zero, which is not a value the document supplied", doc, err)
		}
		out, err := json.Marshal(v)
		if err != nil {
			fail("%s: marshal: %v", doc, err)
		}
		var back map[string]json.RawMessage
		if err := json.Unmarshal(out, &back); err != nil {
			fail("%s: re-decode: %v", doc, err)
		}
		var probe map[string]any
		if err := json.Unmarshal([]byte(doc), &probe); err != nil {
			fail("%s: probe: %v", doc, err)
		}
		for key, val := range probe {
			if val != nil {
				continue
			}
			if got, ok := back[key]; !ok || string(got) != "null" {
				fail("%s: %q came back as %s, want null", doc, key, string(got))
			}
		}
	}

	// The bound still bites on a string that is actually too short.
	var short PresentNullPositions
	if err := json.Unmarshal([]byte(` + "`" + `{"boundOnly":"x","reqBoundOnly":"ok"}` + "`" + `), &short); err != nil {
		fail("unmarshal: %v", err)
	}
	if err := short.Validate(); err == nil {
		fail("boundOnly=\"x\" passed a minLength of 2")
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgramWithConfig(t,
		"testdata/schemas/regression/present_null_positions.json",
		"present_null_values_test",
		mainGo,
		generator.Config{PackageName: "testpkg", OmitEmpty: false},
	)
}

// TestContentVocabularyAssertsOnlyForStrings is the behavioural half of issue
// #115. The check has to fire for a string the encoding or the media type
// refuses, and must not fire at all for a value of any other JSON type: a
// number, an object and a null satisfy {"contentEncoding":"base64"} trivially,
// so narrowing the Go type would reject documents the schema admits.
func TestContentVocabularyAssertsOnlyForStrings(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	fail := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
		os.Exit(1)
	}
	check := func(doc string, wantErr bool) {
		var v ContentPostureDraft7
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			fail("%s: unmarshal: %v", doc, err)
		}
		err := v.Validate()
		if wantErr && err == nil {
			fail("%s was accepted", doc)
		}
		if !wantErr && err != nil {
			fail("%s was rejected: %v", doc, err)
		}
		if _, mErr := json.Marshal(v); mErr != nil {
			fail("%s: marshal: %v", doc, mErr)
		}
	}

	// Strings the vocabulary refuses.
	check(` + "`" + `{"blob":"eyJmb28iOi%iYmFyIn0K"}` + "`" + `, true)
	check(` + "`" + `{"doc":"{:}"}` + "`" + `, true)
	check(` + "`" + `{"encodedDoc":"ezp9Cg=="}` + "`" + `, true)
	check(` + "`" + `{"encodedDoc":"{}"}` + "`" + `, true)

	// Strings it accepts.
	check(` + "`" + `{"blob":"eyJmb28iOiAiYmFyIn0K"}` + "`" + `, false)
	check(` + "`" + `{"doc":"{\"foo\": \"bar\"}"}` + "`" + `, false)
	check(` + "`" + `{"encodedDoc":"eyJmb28iOiAiYmFyIn0K"}` + "`" + `, false)

	// The narrowness control. Every one of these satisfies a content keyword
	// vacuously, and a Go string could hold none of them.
	for _, doc := range []string{
		` + "`" + `{"blob":100}` + "`" + `,
		` + "`" + `{"blob":null}` + "`" + `,
		` + "`" + `{"blob":{"a":1}}` + "`" + `,
		` + "`" + `{"blob":["x"]}` + "`" + `,
		` + "`" + `{"blob":true}` + "`" + `,
		` + "`" + `{"encodedDoc":100}` + "`" + `,
		` + "`" + `{"unknownEncoding":"anything at all"}` + "`" + `,
		` + "`" + `{"unknownEncoding":100}` + "`" + `,
	} {
		check(doc, false)
	}

	// A non-string is kept verbatim rather than coerced.
	var kept ContentPostureDraft7
	if err := json.Unmarshal([]byte(` + "`" + `{"blob":100}` + "`" + `), &kept); err != nil {
		fail("unmarshal of a number blob: %v", err)
	}
	out, err := json.Marshal(kept)
	if err != nil {
		fail("marshal: %v", err)
	}
	if string(out) != ` + "`" + `{"blob":100}` + "`" + ` {
		fail("a number blob round-tripped to %s", string(out))
	}

	// A bound beside the content keywords is carried by the same wrapper, and
	// applies to the same string.
	check(` + "`" + `{"boundedBlob":"YQ=="}` + "`" + `, false)
	check(` + "`" + `{"boundedBlob":"YQ"}` + "`" + `, true)

	// An element position, where the element is a plain Go string and the check
	// rides the per-element rules rather than a named type.
	check(` + "`" + `{"list":["eyJmb28iOiAiYmFyIn0K"]}` + "`" + `, false)
	check(` + "`" + `{"list":["eyJmb28iOi%iYmFyIn0K"]}` + "`" + `, true)

	// And a branch that states nothing but a content keyword.
	check(` + "`" + `{"viaAllOf":"eyJmb28iOiAiYmFyIn0K"}` + "`" + `, false)
	check(` + "`" + `{"viaAllOf":"eyJmb28iOi%iYmFyIn0K"}` + "`" + `, true)
	check(` + "`" + `{"viaAllOf":100}` + "`" + `, false)

	// A oneOf branch and a tuple slot, the two positions that resolve a value
	// without giving it a type of its own. Both answered "it is a string" and
	// dropped the encoding -- issue #183.
	check(` + "`" + `{"branch":"eyJmb28iOiAiYmFyIn0K"}` + "`" + `, false)
	check(` + "`" + `{"branch":"eyJmb28iOi%iYmFyIn0K"}` + "`" + `, true)
	check(` + "`" + `{"branch":true}` + "`" + `, false)
	check(` + "`" + `{"tuple":["eyJmb28iOiAiYmFyIn0K"]}` + "`" + `, false)
	check(` + "`" + `{"tuple":["eyJmb28iOi%iYmFyIn0K"]}` + "`" + `, true)
	check(` + "`" + `{"tuple":[]}` + "`" + `, false)

	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t, "testdata/schemas/regression/content_posture_draft7.json", "content_draft7_test", mainGo)
}

// The other posture. From 2019-09 the content vocabulary is annotation-only by
// definition, so every document the draft-7 types refuse must be accepted here
// -- while the types themselves still exist and still carry a Validate, which is
// what a bare `any` could not.
func TestContentVocabularyAnnotatesFrom2019(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	fail := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
		os.Exit(1)
	}
	for _, doc := range []string{
		` + "`" + `{"blob":"eyJmb28iOi%iYmFyIn0K"}` + "`" + `,
		` + "`" + `{"doc":"{:}"}` + "`" + `,
		` + "`" + `{"encodedDoc":"ezp9Cg=="}` + "`" + `,
		` + "`" + `{"encodedDoc":"{}"}` + "`" + `,
		` + "`" + `{"withSchema":"{}"}` + "`" + `,
		` + "`" + `{"blob":100}` + "`" + `,
		` + "`" + `{"list":["eyJmb28iOi%iYmFyIn0K"]}` + "`" + `,
		` + "`" + `{"viaAllOf":"eyJmb28iOi%iYmFyIn0K"}` + "`" + `,
		` + "`" + `{"branch":"eyJmb28iOi%iYmFyIn0K"}` + "`" + `,
		` + "`" + `{"branch":true}` + "`" + `,
		` + "`" + `{"tuple":["eyJmb28iOi%iYmFyIn0K"]}` + "`" + `,
	} {
		var v ContentPosture2020
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			fail("%s: unmarshal: %v", doc, err)
		}
		if err := v.Validate(); err != nil {
			fail("%s was rejected: %v -- the content vocabulary annotates from 2019-09 and asserts nothing", doc, err)
		}
	}
	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t, "testdata/schemas/regression/content_posture_2020.json", "content_2020_test", mainGo)
}

// TestAllOfOverflowPositionsAreChecked is the behavioural half of issue #112.
// Two allOf branches each bounding the object's values produced map[string]any
// in a property, an element or a map value, with neither bound checked anywhere.
//
// The controls are what the fix must not cost. A value of a type the bound says
// nothing about -- a string, a null, an object -- has to keep passing, which is
// exactly what a widened merge into the typed overflow map would have broken.
func TestAllOfOverflowPositionsAreChecked(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	fail := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
		os.Exit(1)
	}
	check := func(doc string, wantErr bool) {
		var v AllOfOverflowPositions
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			if wantErr {
				return
			}
			fail("%s: unmarshal: %v", doc, err)
		}
		err := v.Validate()
		if wantErr && err == nil {
			fail("%s was accepted; both branches bound every value", doc)
		}
		if !wantErr && err != nil {
			fail("%s was rejected: %v", doc, err)
		}
	}

	// Each branch bites, in every position.
	check(` + "`" + `{"twoBranches":{"x":1}}` + "`" + `, true)
	check(` + "`" + `{"twoBranches":{"x":20}}` + "`" + `, true)
	check(` + "`" + `{"viaRef":{"x":1}}` + "`" + `, true)
	check(` + "`" + `{"viaRef":{"x":20}}` + "`" + `, true)
	check(` + "`" + `{"items":[{"x":20}]}` + "`" + `, true)
	check(` + "`" + `{"namedKey":{"b":20}}` + "`" + `, true)

	// And a value in range passes.
	check(` + "`" + `{"twoBranches":{"x":7}}` + "`" + `, false)
	check(` + "`" + `{"viaRef":{"x":7}}` + "`" + `, false)
	check(` + "`" + `{"items":[{"x":7}]}` + "`" + `, false)
	check(` + "`" + `{"twoBranches":{}}` + "`" + `, false)
	check(` + "`" + `{}` + "`" + `, false)

	// The narrowness control. "minimum" says nothing about a string, a null or
	// an object, so each of these satisfies both branches. A typed overflow map
	// would refuse them at decode time.
	for _, doc := range []string{
		` + "`" + `{"twoBranches":{"x":"abc"}}` + "`" + `,
		` + "`" + `{"twoBranches":{"x":null}}` + "`" + `,
		` + "`" + `{"twoBranches":{"x":{"a":1}}}` + "`" + `,
		` + "`" + `{"viaRef":{"x":["y"]}}` + "`" + `,
		` + "`" + `{"items":[{"x":"abc"}]}` + "`" + `,
	} {
		check(doc, false)
	}

	// The accept-control: the lone-branch spelling is the one the merge does
	// express, so it keeps the overflow map the merge produced rather than being
	// taken over by the evaluator, and its bound still bites. Its value type is
	// no longer float64 -- issue #137 gave an untyped sub-schema the wrapper
	// there too -- so the same narrowness control applies to it.
	check(` + "`" + `{"soleBranch":{"x":7}}` + "`" + `, false)
	check(` + "`" + `{"soleBranch":{"x":1}}` + "`" + `, true)
	check(` + "`" + `{"soleBranch":{"x":"abc"}}` + "`" + `, false)
	check(` + "`" + `{"soleBranch":{"x":null}}` + "`" + `, false)

	// Round-trip: the wrapper keeps the bytes it was handed.
	var v AllOfOverflowPositions
	doc := ` + "`" + `{"twoBranches":{"x":7},"viaRef":{"y":"abc"}}` + "`" + `
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		fail("%s: unmarshal: %v", doc, err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		fail("marshal: %v", err)
	}
	if string(out) != doc {
		fail("%s round-tripped to %s", doc, string(out))
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t, "testdata/schemas/regression/allof_overflow_positions.json", "allof_overflow_test", mainGo)
}

// TestOverflowMapUntypedValueKeepsEveryKind is the behavioural half of issue
// #137. An overflow map typed from a sub-schema that states no "type" asserted
// what the schema never said: `minimum` constrains numbers and is vacuous for
// every other JSON kind, so map[string]float64 refused {"x":"abc"} and
// {"x":{"a":1}} in the decoder, and turned {"x":null} into the Go zero and
// measured that against the bound.
//
// The valid list is the whole of the defect: every one of those documents is
// admitted by the schema and was refused. It covers the three inferred kinds the
// same narrowing reaches -- minLength typing the map as string, minItems as a
// slice, required as a nested map -- and both spellings of the overflow map, the
// Go map a property becomes and the struct field beside declared properties.
//
// The invalid list is what the fix must not cost. Each bound still bites on a
// value of the kind it speaks about, and `typed` is the control that says the
// narrowing is kept where the sub-schema authorized it: {"typed":{"x":"abc"}}
// is a genuine rejection, at decode time, because that sub-schema does state
// "type":"number".
func TestOverflowMapUntypedValueKeepsEveryKind(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/overflow_map_untyped_value.json",
		[]string{
			// The reported rows, in the Go-map spelling.
			`{"bare":{"x":"abc"}}`,
			`{"bare":{"x":{"a":1}}}`,
			`{"bare":{"x":null}}`,
			`{"bare":{"x":[1,2]}}`,
			`{"bare":{"x":true}}`,
			`{"bare":{"x":7}}`,
			// minLength says nothing about a number or a null.
			`{"strLen":{"x":"abc"}}`,
			`{"strLen":{"x":7}}`,
			`{"strLen":{"x":null}}`,
			// minItems says nothing about a string.
			`{"arrLen":{"x":[1,2]}}`,
			`{"arrLen":{"x":"abc"}}`,
			// required says nothing about a scalar.
			`{"objReq":{"x":{"a":1}}}`,
			`{"objReq":{"x":"abc"}}`,
			// The $defs spelling, which has always been right, still is.
			`{"viaRef":{"x":"abc"}}`,
			`{"viaRef":{"x":7}}`,
			// The struct-field spelling: these keys land in the root's own
			// overflow map, beside the declared properties.
			`{"extra":"abc"}`,
			`{"extra":null}`,
			`{"extra":7}`,
			`{"typed":{"x":7}}`,
			`{}`,
		},
		[]string{
			`{"bare":{"x":3}}`,
			`{"strLen":{"x":"ab"}}`,
			`{"arrLen":{"x":[1]}}`,
			`{"objReq":{"x":{}}}`,
			`{"viaRef":{"x":3}}`,
			`{"extra":3}`,
			// The sub-schema states its type, so the Go type may narrow and a
			// string really is refused -- at decode, which counts as a rejection.
			`{"typed":{"x":3}}`,
			`{"typed":{"x":"abc"}}`,
		},
	)
}

// TestAllOfNestedAnyOfUnevaluatedIsPerDocument is issue #135 through `anyOf`:
// #111's reproducer with the applicator one level down, inside an allOf branch.
//
// The invalid list is the missing check. {"b":2,"c":3} leaves c unevaluated for
// the first branch and states no `a` for the second, so no branch holds -- and
// the static approximation, which is all this shape had, admits it.
//
// The valid list is the accept-control beside it, and it is the same set #111
// established for the direct spelling: {"a":1} satisfies the second branch,
// which states no `unevaluatedProperties` at all, and a branch the document
// fails contributes nothing. Adding the exact check must not turn any of these
// into a rejection.
func TestAllOfNestedAnyOfUnevaluatedIsPerDocument(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/allof_nested_anyof_unevaluated.json",
		[]string{
			`{"a":1}`,
			`{"b":2}`,
			`{"a":1,"b":2}`,
			// No key is unevaluated, so the first branch holds vacuously.
			`{}`,
		},
		[]string{
			`{"b":2,"c":3}`,
			`{"c":3}`,
		},
	)
}

// TestAllOfNestedOneOfUnevaluatedIsPerDocument is issue #135 through `oneOf`,
// where the same gap is a false rejection rather than a missing check.
//
// {"a":1,"b":1,"m":1} satisfies PickOne's second branch alone: the first
// declares b, m and n, so `a` is unevaluated and `unevaluatedProperties:false`
// fails it. The static approximation reads required keys, consts and declared
// types, none of which mention `a`, so it counted two matches and reported
// "expected exactly 1" -- #111's rejection surviving one level down. It is the
// first entry of the valid list.
//
// The last two invalid entries are the control for the suppression being per
// variant slice. The second allOf branch states a oneOf of its own with no
// `unevaluatedProperties`, so it gets no exact check and must keep the
// approximation: {"a":1} matches neither of its branches and {"a":1,"m":1,"n":1}
// matches both. Suppressing per struct -- "some oneOf here was taken over" --
// drops that approximation with its sibling's and accepts both.
//
// PickOne sits behind a $ref, which the merge follows, so this is also the
// control that the two collectors agree on which branch that is. Every verdict
// below was confirmed against python-jsonschema.
func TestAllOfNestedOneOfUnevaluatedIsPerDocument(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/allof_nested_oneof_unevaluated.json",
		[]string{
			`{"a":1,"b":1,"m":1}`,
			`{"b":1,"m":1}`,
			`{"a":1,"m":1}`,
			`{"a":1,"b":1,"m":1,"z":"s"}`,
		},
		[]string{
			// PickOne holds for neither branch: the first wants b, the second a.
			`{"m":1}`,
			`{"z":"s"}`,
			`{}`,
			// The sibling slice's own approximation, which must survive.
			`{"a":1}`,
			`{"a":1,"m":1,"n":1}`,
		},
	)
}

// TestAllOfNestedAnyOfUnevaluatedItems is the same nesting for the array
// keyword, which issue #135 asked to be checked at the same time.
//
// hasCousinUnevaluatedItems read the direct branches of the schema's own
// in-place applicators only, so an anyOf inside an allOf branch was invisible
// and the keyword fell back to static handling. [1,2] leaves index 1
// unevaluated for the first branch and is not a string array for the second, so
// it satisfies neither; it was accepted. The valid list is the accept-control:
// [1] is evaluated entirely by the prefix, ["a","b"] fails the first branch's
// own prefixItems assertion and holds under the second, and [] holds vacuously.
func TestAllOfNestedAnyOfUnevaluatedItems(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/allof_nested_anyof_unevaluated_items.json",
		[]string{
			`[1]`,
			`["a","b"]`,
			`[]`,
		},
		[]string{
			`[1,2]`,
			`[1,"a"]`,
		},
	)
}

// TestInlineUntypedPositionsKeepEveryKind is the behavioural half of issue
// #139. A declared property and an array element were typed from a validation
// keyword rather than from a "type" the sub-schema states, which asserted what
// the schema never said: `minimum` constrains numbers and is vacuous for every
// other JSON kind, so *float64 refused {"num":"abc"} and {"num":{"a":1}} in the
// decoder and []float64 turned [null] into the Go zero and measured that against
// the bound.
//
// The valid list is the whole of the defect, across the keyword families the
// inference reads -- minLength typing the position string, minItems array,
// required a nested map -- in both positions. Every verdict was confirmed
// against python-jsonschema before it was written down.
//
// The invalid list is what the fix must not cost. Each bound still bites on a
// value of the kind it speaks about, and three controls sit beside them:
//
//   - typedNum and typedItems state their type, so the narrowing is authorized
//     and a string really is a rejection -- at decode, which counts as one.
//   - slot is the working sibling. A prefixItems position has always been boxed,
//     so its rows are unchanged in both directions, and a fix reaching it would
//     have gone too wide.
//   - viaRef is the same sub-schema behind a $defs entry, which has had the
//     wrapper all along. The inline spellings now answer as it does.
func TestInlineUntypedPositionsKeepEveryKind(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/inline_untyped_positions.json",
		[]string{
			// The reported property rows.
			`{"num":"abc"}`,
			`{"num":{"a":1}}`,
			`{"num":null}`,
			`{"num":[1]}`,
			`{"num":true}`,
			`{"num":7}`,
			// minLength says nothing about a number or a null.
			`{"str":"abcd"}`,
			`{"str":7}`,
			`{"str":null}`,
			// minItems says nothing about a string.
			`{"arr":[1,2]}`,
			`{"arr":"abc"}`,
			// required says nothing about a scalar or a null.
			`{"obj":{"a":1}}`,
			`{"obj":"abc"}`,
			`{"obj":null}`,
			// The $defs spelling, which has always been right, still is.
			`{"viaRef":"abc"}`,
			`{"viaRef":7}`,
			// The reported element rows.
			`{"numItems":["abc"]}`,
			`{"numItems":[null]}`,
			`{"numItems":[{"a":1}]}`,
			`{"numItems":[7]}`,
			`{"strItems":["abcd"]}`,
			`{"strItems":[7]}`,
			`{"objItems":[{"a":1}]}`,
			`{"objItems":["abc"]}`,
			// The declared-type positions accept what they always did.
			`{"typedNum":7}`,
			`{"typedItems":[7]}`,
			// The prefixItems slot, unchanged in every row.
			`{"slot":["abc"]}`,
			`{"slot":[null]}`,
			`{"slot":[7]}`,
			// A map value reached through the nullable arm.
			`{"nullableMap":{"x":"abc"}}`,
			`{"nullableMap":{"x":null}}`,
			`{"nullableMap":null}`,
			`{"nullableMap":{"x":7}}`,
			`{}`,
		},
		[]string{
			`{"num":3}`,
			`{"str":"ab"}`,
			`{"arr":[1]}`,
			`{"obj":{}}`,
			`{"viaRef":3}`,
			`{"numItems":[3]}`,
			`{"strItems":["ab"]}`,
			`{"objItems":[{}]}`,
			`{"slot":[3]}`,
			`{"nullableMap":{"x":3}}`,
			// The narrowness controls. The sub-schema states its type, so the Go
			// type may narrow and a string is refused at decode.
			`{"typedNum":3}`,
			`{"typedNum":"abc"}`,
			`{"typedItems":[3]}`,
			`{"typedItems":["abc"]}`,
		},
	)
}

// TestInlineForbiddingPositionsRejectEveryValue is the behavioural half of issue
// #142. A sub-schema that admits no instance at all was enforced at a document
// root and behind a $ref and dropped inline, so {"items":false} came out []any
// with no check and accepted [1], and {"additionalProperties":{"enum":[]}} came
// out map[string]any and accepted a value under every key.
//
// The invalid list is the whole of the defect, in both spellings and at every
// position that resolves rather than names. The two are the same statement --
// enum asserts the instance equals one of the values listed, and there are none
// -- so a position that answers one and not the other is answering by spelling
// rather than by meaning.
//
// The valid list is what the fix must not cost, and it carries three kinds of
// control:
//
//   - The empty container beside every rejection. An empty array satisfies
//     {"items":false} and an object with no keys satisfies
//     {"additionalProperties":{"enum":[]}}: the sub-schema speaks about the
//     values that are there, and there are none. `contains` is the one that goes
//     the other way, since it demands a match rather than judging one.
//   - The two documents this generator used to refuse. emptyEnumBranch is a
//     oneOf whose forbidding branch never matches, so "s" matches exactly one
//     branch; notEmptyEnum and notTypedConst are `not` over schemas read as if a
//     keyword were absent, which made the negation wider than the schema. A
//     false rejection is the failure this repository treats as worse than a
//     missing check, and these are where it is watched.
//   - The spellings that were already right and must not move: viaRefFalse, the
//     same element sub-schema behind a $defs entry, and notEmptyItems, the
//     `{"not":{}}` spelling of the empty set. okEnumItems and plainItems are the
//     broad control: a predicate reading len() where it must read the nil would
//     forbid every schema in the corpus, and these are the first to say so.
func TestInlineForbiddingPositionsRejectEveryValue(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/inline_forbidding_positions.json",
		[]string{
			// The empty container satisfies a sub-schema about its contents.
			`{"falseItems":[]}`,
			`{"emptyEnumItems":[]}`,
			`{"typedEmptyEnumItems":[]}`,
			`{"nestedFalseItems":[]}`,
			`{"nestedFalseItems":[[]]}`,
			`{"nullableFalseItems":[]}`,
			`{"nullableFalseItems":null}`,
			`{"emptyEnumValues":{}}`,
			`{"nullableEmptyEnumValues":{}}`,
			`{"nullableEmptyEnumValues":null}`,
			`{"emptyEnumSlot":[]}`,
			`{"emptyEnumPattern":{}}`,
			`{"emptyEnumPattern":{"b":1}}`,
			`{"emptyEnumNames":{}}`,
			`{"emptyEnumDependent":{}}`,
			`{"emptyEnumDependent":{"j":1}}`,
			`{"emptyEnumUnevalItems":[]}`,
			`{"emptyEnumUnevalItems":[1]}`,
			`{"emptyEnumUnevalProps":{}}`,
			`{"emptyEnumUnevalProps":{"k":1}}`,
			// An items-only or prefixItems-only schema says nothing about a
			// value that is not an array.
			`{"inferredEmptyEnumItems":[]}`,
			`{"inferredEmptyEnumItems":"abc"}`,
			`{"inferredEmptyEnumSlot":[]}`,
			`{"inferredEmptyEnumSlot":"abc"}`,
			`{"inferredEmptyEnumTail":[]}`,
			`{"inferredEmptyEnumTail":[1]}`,
			`{"inferredEmptyEnumTail":"abc"}`,
			// The two false rejections.
			`{"emptyEnumBranch":"s"}`,
			`{"notEmptyEnum":1}`,
			`{"notEmptyEnum":"abc"}`,
			`{"notEmptyEnum":null}`,
			`{"notEmptyEnum":{"a":1}}`,
			`{"notTypedConst":"abc"}`,
			`{"notTypedConst":1}`,
			`{"notTypedEmptyEnum":"abc"}`,
			`{"notTypedEmptyEnum":1}`,
			`{"notEmptyEnumBound":7}`,
			`{"notEmptyEnumBound":1}`,
			`{"notEmptyEnumBound":"abc"}`,
			`{"notAnyOfEmptyEnum":7}`,
			`{"notAnyOfEmptyEnum":1}`,
			`{"notAnyOfEmptyEnum":"abc"}`,
			// The spellings that were already right.
			`{"viaRefFalse":[]}`,
			`{"viaRefEmptyEnum":[]}`,
			`{"notEmptyItems":[]}`,
			// A listed enum is not the empty one.
			`{"okEnumItems":[]}`,
			`{"okEnumItems":["a"]}`,
			`{"okEnumItems":["a","b"]}`,
			`{"plainItems":["x"]}`,
			`{}`,
		},
		[]string{
			// The reported element position, in both spellings and through the
			// nested and nullable routes.
			`{"falseItems":[1]}`,
			`{"falseItems":["x",2]}`,
			`{"emptyEnumItems":[1]}`,
			`{"emptyEnumItems":[null]}`,
			`{"typedEmptyEnumItems":["x"]}`,
			`{"nestedFalseItems":[[1]]}`,
			`{"nullableFalseItems":[1]}`,
			// The reported map-value position, and its nullable route.
			`{"emptyEnumValues":{"x":1}}`,
			`{"emptyEnumValues":{"x":"s"}}`,
			`{"nullableEmptyEnumValues":{"x":1}}`,
			// The slot, branch and composition positions.
			`{"emptyEnumSlot":[1]}`,
			`{"emptyEnumBranch":1}`,
			`{"emptyEnumAllOf":"s"}`,
			`{"emptyEnumAllOf":1}`,
			`{"emptyEnumAnyOf":"s"}`,
			`{"emptyEnumAnyOf":1}`,
			`{"refEmptyEnumAnyOf":"s"}`,
			`{"refEmptyEnumAnyOf":1}`,
			// The keyword positions.
			`{"emptyEnumPattern":{"a1":1}}`,
			`{"emptyEnumNames":{"a":1}}`,
			`{"emptyEnumContains":[]}`,
			`{"emptyEnumContains":[1]}`,
			`{"emptyEnumDependent":{"k":1}}`,
			`{"emptyEnumUnevalItems":[1,2]}`,
			`{"emptyEnumUnevalProps":{"x":1}}`,
			// The inferred-array spellings of the element and the slot.
			`{"inferredEmptyEnumItems":[1]}`,
			`{"inferredEmptyEnumSlot":[1]}`,
			`{"inferredEmptyEnumTail":[1,2]}`,
			// The `not` reads its inner schema whole: this one forbids "x" and
			// nothing else.
			`{"notTypedConst":"x"}`,
			// The controls that were already rejecting, and still are.
			`{"viaRefFalse":[1]}`,
			`{"viaRefEmptyEnum":[1]}`,
			`{"notEmptyItems":[1]}`,
			`{"okEnumItems":["c"]}`,
		},
	)
}

// TestNotOverAFormatFollowsTheDialect is the false-rejection guard for the
// predicate #146 reads a `not` through. Whether {"format":"email"} constrains
// anything is the dialect's answer, so a predicate that decides the inner schema
// admits every value without asking the dialect gets the negation backwards
// exactly where format asserts: {"not":{"format":"email"}} then forbade every
// value, and "not-an-email", the number 5 and a null are the documents that say
// so. Under 2020-12's own posture format annotates, the inner schema really does
// admit everything, and the negation really does forbid everything -- so the two
// arms disagree by design and a fix that answered one posture for both would
// fail the other.
//
// formatBranches is the same reading through the other caller. A oneOf whose
// branches both accept every value matches twice and so matches nothing, so
// reading a format branch as accept-all refused every document there too --
// which is why the fix is in one predicate rather than at the `not`.
//
// The documents whose verdict turns on the assertion itself were on neither list
// under assertion, because the generated code refused none of them: the
// evaluator did not model `format`, so a negation over one was declined whole
// (issue #194). Issue #205 closed that, and they are on the invalid list now --
// the conforming e-mail, the oneOf's "neither", and the number that matches both
// format branches.
//
// The number and the null moved the other way, from valid to invalid, and that
// correction is the point of this note. `format` is a no-op on an instance that
// is not a string -- the pinned suite says so outright, in
// draft7/optional/format/email.json, whose cases "all string formats ignore
// integers" and "... ignore nulls" mark 12 and null *valid* against
// {"format":"email"}. So 5 and null satisfy the inner schema, and a `not` over
// it must reject them. Listing them as valid did not describe the assertion
// posture at all; it described a negation that had been declined, which is why
// the same two documents are correctly valid on the annotating arm above only by
// coincidence of a different route. See acceptsEveryInstance.
func TestNotOverAFormatFollowsTheDialect(t *testing.T) {
	t.Run("2020-12 annotates, so the negation forbids everything", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/not_over_format.json",
			[]string{
				`{"notEmailInItems":[]}`,
				`{}`,
			},
			[]string{
				`{"notEmail":"not-an-email"}`,
				`{"notEmail":5}`,
				`{"notEmail":null}`,
				`{"notEmail":"a@b.com"}`,
				`{"notEmailInItems":["not-an-email"]}`,
				`{"notEmailInItems":[5]}`,
				`{"notEmailInItems":["a@b.com"]}`,
				// Both branches accept everything, so every value matches two.
				`{"formatBranches":"a@b.com"}`,
				`{"formatBranches":"1.2.3.4"}`,
				`{"formatBranches":"neither"}`,
				`{"formatBranches":5}`,
			},
		)
	})
	t.Run("under assertion it forbids the conforming string alone", func(t *testing.T) {
		runValidationCasesWithConfig(t,
			"testdata/schemas/regression/not_over_format.json",
			formatAssertingConfig(),
			[]string{
				// The non-conforming string is the only value the inner schema
				// refuses, so it is the only one the negation admits.
				`{"notEmail":"not-an-email"}`,
				`{"notEmailInItems":[]}`,
				`{"notEmailInItems":["not-an-email"]}`,
				// Each satisfies exactly one branch, and the whole schema
				// refused every value of any kind before this.
				`{"formatBranches":"a@b.com"}`,
				`{"formatBranches":"1.2.3.4"}`,
				`{}`,
			},
			[]string{
				// The conforming e-mail: the inner schema accepts it, so the
				// negation refuses it. This is the check issue #194 recorded as
				// missing.
				`{"notEmail":"a@b.com"}`,
				`{"notEmailInItems":["a@b.com"]}`,
				// A format says nothing about an instance that is not a string,
				// so the number and the null *satisfy* {"format":"email"} and
				// the negation refuses them. The suite's own "all string formats
				// ignore integers" and "... ignore nulls" cases are what settle
				// this; see the note above.
				`{"notEmail":5}`,
				`{"notEmail":null}`,
				`{"notEmailInItems":[5]}`,
				// Neither branch: zero matches, so the oneOf fails.
				`{"formatBranches":"neither"}`,
				// Both branches ignore a number, so it matches two of them.
				`{"formatBranches":5}`,
			},
		)
	})
}

// TestForbiddingSubschemaSpellingsAreEnforced is the behavioural half of issue
// #146. #142 closed `false` and `{"enum":[]}` at six keyword positions;
// {"not":{}} and {"oneOf":[false,false]} are the same statement and were dropped
// at every one of them, so the position answered by spelling rather than by
// meaning. Beside that: propertyNames and dependentSchemas were dropped whole on
// an inline object property, whatever the spelling, and an allOf with a branch
// the static merge could not read was enforced by halves. The four object
// keywords dropped alongside them -- required, minProperties, maxProperties and
// dependentRequired -- are here too: not this issue's keywords, but its defect
// in its predicate at its position.
//
// The invalid list is the whole of the defect. The valid list is what the fix
// must not cost, and it carries five kinds of control:
//
//   - The container that satisfies the sub-schema, beside every rejection. An
//     empty object satisfies a forbidding propertyNames and an object without
//     the trigger key satisfies a forbidding dependentSchemas: the sub-schema
//     speaks about what is there, and there is none of it. `contains` is the one
//     that goes the other way, since it demands a match rather than judging one,
//     so its empty array is on the invalid side.
//   - The `not` that does not forbid everything. notEnumBranch is a negation of
//     the empty set, notFalse its boolean spelling, notTypedConst a negation
//     that forbids one string, and notShallowEnum one that forbids two values.
//     Reading any of them as forbidding is a false rejection, and notFalse is
//     the one this change introduced and had to fix: a boolean schema carries
//     its answer in a field with no JSON key, so every keyword test passed for
//     it and `false` read as accept-all. See isAcceptAllSchema.
//   - The composition with one live branch. oneOfOneFalse and anyOfOneFalse each
//     hold a forbidding branch beside a real one; a predicate that answered
//     "some branch forbids" rather than "every branch forbids" would refuse
//     both, and "s" is what says so.
//   - The type-conditional row. inferredNotItems and inferredNotSlot state only
//     array keywords, so a string satisfies them, and nullableInlineNames still
//     admits a null.
//   - The schema the inline object arm must not claim. `required` speaks about
//     objects, so strBranchRequired and unionBranchRequired state it and still
//     describe strings; read as an object shape they get a struct, and the
//     struct refuses "abc" at the decoder. mapWithMinProps is the other
//     declined shape, and the golden rather than this list is what pins it.
//
// Every verdict here was cross-checked against python-jsonschema before it was
// written down; the two agree on all of them.
func TestForbiddingSubschemaSpellingsAreEnforced(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/forbidding_subschema_spellings.json",
		[]string{
			// The container the forbidding sub-schema says nothing about.
			`{"notNames":{}}`,
			`{"oneOfNames":{}}`,
			`{"anyOfNames":{}}`,
			`{"refNotNames":{}}`,
			`{"notDependent":{}}`,
			`{"notDependent":{"j":1}}`,
			`{"oneOfDependent":{}}`,
			`{"notUnevalItems":[]}`,
			`{"notUnevalItems":[1]}`,
			`{"oneOfUnevalItems":[1]}`,
			`{"notUnevalProps":{}}`,
			`{"notUnevalProps":{"k":1}}`,
			`{"oneOfUnevalProps":{"k":1}}`,
			`{"inlineFalseNames":{}}`,
			`{"inlineNotNames":{}}`,
			`{"inlineFalseDependent":{}}`,
			`{"inlineFalseDependent":{"j":1}}`,
			`{"inlineNotDependent":{}}`,
			`{"nullableInlineNames":null}`,
			`{"nullableInlineNames":{}}`,
			// An array-only schema says nothing about a value that is not an
			// array, and a prefix leaves the tail free.
			`{"inferredNotItems":[]}`,
			`{"inferredNotItems":"abc"}`,
			`{"inferredOneOfItems":[]}`,
			`{"inferredOneOfItems":"abc"}`,
			`{"inferredNotTail":[]}`,
			`{"inferredNotTail":[1]}`,
			`{"inferredNotTail":"abc"}`,
			`{"inferredOneOfTail":[1]}`,
			`{"inferredNotSlot":[]}`,
			`{"inferredNotSlot":"abc"}`,
			// The object shapes that hold.
			`{"inlineRequired":{"a":1}}`,
			`{"inlineMinProps":{"a":1,"b":2}}`,
			`{"inlineMaxProps":{"a":1}}`,
			`{"inlineDepRequired":{"k":1,"j":2}}`,
			`{"inlineDepRequired":{"j":1}}`,
			// The map the inline arm declines, whose minProperties is left
			// unenforced so the field keeps map[string]string. The document that
			// would show the gap is on neither list, for the reason the format
			// test gives; the golden is what pins the type.
			`{"mapWithMinProps":{"a":"x","b":"y"}}`,
			// `required` speaks about objects, so a schema stating it may still
			// describe strings alone. Reading that as an object shape gives the
			// branch a struct, which refuses the string at the decoder.
			`{"strBranchRequired":"abc"}`,
			`{"strBranchRequired":5}`,
			`{"unionBranchRequired":"abc"}`,
			`{"unionBranchRequired":5}`,
			`{"unionBranchRequired":{"a":1}}`,
			// The `not` that forbids less than everything.
			`{"notEnumBranch":1}`,
			`{"notEnumBranch":"abc"}`,
			`{"notEnumBranch":null}`,
			`{"notEnumBranch":{"a":1}}`,
			`{"notTypedConst":"abc"}`,
			`{"notTypedConst":1}`,
			`{"notFalse":1}`,
			`{"notFalse":"abc"}`,
			`{"notShallowEnum":"c"}`,
			`{"notShallowEnum":1}`,
			// The composition with a branch still alive.
			`{"oneOfOneFalse":"s"}`,
			`{"anyOfOneFalse":"s"}`,
			// The keyword that constrains without forbidding.
			`{"okNames":{}}`,
			`{"okNames":{"ab":1}}`,
			`{"okContains":[1]}`,
			`{"plainItems":["x"]}`,
			`{}`,
		},
		[]string{
			// propertyNames, in both spellings and behind a reference.
			`{"notNames":{"a":1}}`,
			`{"oneOfNames":{"a":1}}`,
			`{"anyOfNames":{"a":1}}`,
			`{"refNotNames":{"a":1}}`,
			// contains, which refuses the empty array too.
			`{"notContains":[]}`,
			`{"notContains":[1]}`,
			`{"oneOfContains":[]}`,
			`{"oneOfContains":[1]}`,
			`{"allOfContains":[]}`,
			`{"allOfContains":["s"]}`,
			// dependentSchemas, once the trigger key is there.
			`{"notDependent":{"k":1}}`,
			`{"oneOfDependent":{"k":1}}`,
			// The two unevaluated keywords.
			`{"notUnevalItems":[1,2]}`,
			`{"oneOfUnevalItems":[1,2]}`,
			`{"notUnevalProps":{"x":1}}`,
			`{"oneOfUnevalProps":{"x":1}}`,
			// The inferred array's item, tail and slot.
			`{"inferredNotItems":[1]}`,
			`{"inferredOneOfItems":[1]}`,
			`{"inferredNotTail":[1,2]}`,
			`{"inferredOneOfTail":[1,2]}`,
			`{"inferredNotSlot":[1]}`,
			// The allOf whose unreadable branch used to be enforced by halves:
			// the string was accepted and only the number was refused.
			`{"allOfNot":"s"}`,
			`{"allOfNot":1}`,
			`{"anyOfNot":"s"}`,
			`{"anyOfNot":1}`,
			// The keywords an inline object property dropped whole.
			`{"inlineFalseNames":{"a":1}}`,
			`{"inlineNotNames":{"a":1}}`,
			`{"inlineFalseDependent":{"k":1}}`,
			`{"inlineNotDependent":{"k":1}}`,
			`{"inlineRequired":{"b":1}}`,
			`{"inlineMinProps":{"a":1}}`,
			`{"inlineMaxProps":{"a":1,"b":2}}`,
			`{"inlineDepRequired":{"k":1}}`,
			`{"nullableInlineNames":{"a":1}}`,
			// No branch of these accepts an object without "a", and reading the
			// first branch as an object shape would refuse the string instead.
			`{"strBranchRequired":{"a":1}}`,
			`{"unionBranchRequired":{"b":1}}`,
			// The `not` reads its inner schema whole.
			`{"notTypedConst":"x"}`,
			`{"notShallowEnum":"a"}`,
			// A composition still refuses what no branch admits.
			`{"oneOfOneFalse":1}`,
			`{"anyOfOneFalse":1}`,
			// The controls that were already rejecting, and still are.
			`{"okNames":{"abcd":1}}`,
			`{"okContains":[]}`,
			`{"okContains":["x"]}`,
		},
	)
}

// TestEnumOutsideDeclaredTypeAdmitsNothing is the behavioural half of issue
// #145. A schema listing enum or const members its own "type" forbids was read
// as one admitting them: {"type":"string","const":5} emitted `const Root string
// = 5`, which does not build, and {"type":"string","enum":["a",5]} became a raw
// enum listing both and accepted 5.
//
// The invalid list is the whole of the defect. It runs the one-member spelling
// (`const`), the one-member enum, the several-member enum and the partial list
// through every position that resolves rather than names -- a declared property,
// an element, a map value, a tuple slot, patternProperties, an allOf branch, an
// anyOf branch, a oneOf branch and a $ref, which is the route a document root
// takes -- and it runs each declared type against a member of the wrong kind,
// because the base type differs per type name and two of them (the map an
// "object" maps to and the slice an "array" maps to) have no Go constants at
// all, where the build failure reads `invalid constant type` instead.
//
// The valid list is what the fix must not cost, and it carries three kinds of
// control:
//
//   - The partial list, wherever it appears. {"type":"string","enum":["a",5]}
//     admits "a", and a filter that emptied the list rather than narrowing it
//     would refuse it. This is the accept-control the change is most easily got
//     wrong against, which is why it sits beside every rejection rather than
//     once.
//   - untypedEnum, which states no "type" at all and must be filtered by
//     nothing. A type this generator *infers* is not an assertion the schema
//     made, and reading one here would refuse {"untypedEnum":5}. unionEnum and
//     nullableEnum are the same control one step out: a member matching any one
//     of the declared types survives.
//   - integerFloatSpelling, which reads the value and not the notation. A member
//     written 1 and an instance written 1.0 are the same number, and from draft
//     6 on that instance is an integer; judging the notation would drop the
//     member and refuse both spellings.
//
// The empty container beside every container rejection is the third control, and
// it is the one #142 already needed: a sub-schema about an element or a map
// value says nothing about an array or an object that holds none.
func TestEnumOutsideDeclaredTypeAdmitsNothing(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/enum_outside_declared_type.json",
		[]string{
			`{}`,
			// The partial list admits the member its type does admit, at every
			// position it was measured at.
			`{"enumPartialProp":"a"}`,
			`{"enumPartialItems":["a"]}`,
			`{"enumPartialValues":{"x":"a"}}`,
			`{"enumPartialSlot":["a"]}`,
			`{"enumPartialPattern":{"a1":"a"}}`,
			`{"enumPartialRef":"a"}`,
			`{"fracInInteger":1}`,
			// The empty container satisfies a sub-schema about its contents.
			`{"constOutsideItems":[]}`,
			`{"enumPartialItems":[]}`,
			`{"constOutsideValues":{}}`,
			`{"enumPartialValues":{}}`,
			`{"constOutsideSlot":[]}`,
			`{"enumPartialSlot":[]}`,
			`{"constOutsidePattern":{}}`,
			`{"constOutsidePattern":{"b":1}}`,
			`{"enumPartialPattern":{}}`,
			`{"enumPartialPattern":{"b":1}}`,
			// The branch that can match is the one the schema left.
			`{"constOutsideOneOf":5}`,
			// The members the declared type does admit.
			`{"typedConst":"a"}`,
			`{"typedEnum":"a"}`,
			`{"typedEnum":"b"}`,
			`{"untypedEnum":"a"}`,
			`{"untypedEnum":5}`,
			`{"unionEnum":"a"}`,
			`{"unionEnum":5}`,
			`{"nullableEnum":"a"}`,
			`{"integerEnum":1}`,
			`{"integerEnum":2}`,
			`{"integerFloatSpelling":1}`,
			`{"integerFloatSpelling":1.0}`,
			`{"numberEnum":1.5}`,
			`{"numberEnum":2}`,
			`{"objectEnum":{"k":1}}`,
			`{"arrayEnum":[1]}`,
			`{"boolEnum":true}`,
			`{"okItems":[]}`,
			`{"okItems":["a"]}`,
			`{"okItems":["a","b"]}`,
			// The keyword positions, each with the container that satisfies
			// them: a tail that must be empty, an object with nothing
			// unevaluated, an object with no keys, and the trigger absent.
			`{"constOutsideUnevalItems":[]}`,
			`{"constOutsideUnevalItems":[1]}`,
			`{"constOutsideUnevalProps":{}}`,
			`{"constOutsideUnevalProps":{"a":1}}`,
			`{"constOutsideNames":{}}`,
			`{"constOutsideDependent":{}}`,
			`{"constOutsideDependent":{"j":1}}`,
			// The negation of a schema admitting nothing admits everything.
			`{"notConstOutside":"a"}`,
			`{"notConstOutside":5}`,
			`{"notConstOutside":null}`,
			`{"notConstOutside":{"a":1}}`,
		},
		[]string{
			// The reported spelling. constOutsideProp is the control that says
			// the enum rows are a different question: a declared property keeps
			// its type and emits a field-level const rule, and was already
			// right.
			`{"constOutsideProp":"a"}`,
			`{"constOutsideProp":5}`,
			`{"enumOutsideProp":"a"}`,
			`{"enumOutsideProp":5}`,
			`{"enumAllOutsideProp":"a"}`,
			`{"enumAllOutsideProp":1}`,
			`{"constOutsideRef":"a"}`,
			`{"constOutsideRef":5}`,
			// The partial list refuses the member its type forbids, and every
			// value that was never listed.
			`{"enumPartialProp":5}`,
			`{"enumPartialProp":"b"}`,
			`{"enumPartialRef":5}`,
			`{"enumPartialRef":"b"}`,
			`{"fracInInteger":2.5}`,
			`{"fracInInteger":3}`,
			// The element, the map value, the slot and patternProperties.
			`{"constOutsideItems":["a"]}`,
			`{"constOutsideItems":[5]}`,
			`{"enumPartialItems":[5]}`,
			`{"enumPartialItems":["b"]}`,
			`{"constOutsideValues":{"x":"a"}}`,
			`{"enumPartialValues":{"x":5}}`,
			`{"enumPartialValues":{"x":"b"}}`,
			`{"constOutsideSlot":["a"]}`,
			`{"enumPartialSlot":[5]}`,
			`{"enumPartialSlot":["b"]}`,
			`{"constOutsidePattern":{"a1":"a"}}`,
			`{"enumPartialPattern":{"a1":5}}`,
			`{"enumPartialPattern":{"a1":"b"}}`,
			// The compositions.
			`{"constOutsideAllOf":"a"}`,
			`{"constOutsideAllOf":5}`,
			`{"constOutsideAnyOf":"a"}`,
			`{"constOutsideAnyOf":5}`,
			`{"constOutsideOneOf":"a"}`,
			// One member of the wrong kind for each declared type. The base type
			// differs per name, and three of them have no Go constants at all.
			`{"fracOutsideInteger":2.5}`,
			`{"fracOutsideInteger":1}`,
			`{"nullOutsideConst":5}`,
			`{"nullOutsideConst":null}`,
			`{"objectOutsideConst":5}`,
			`{"objectOutsideConst":{}}`,
			`{"boolOutsideConst":5}`,
			`{"boolOutsideConst":true}`,
			`{"arrayOutsideConst":5}`,
			`{"arrayOutsideConst":[]}`,
			`{"numberOutsideConst":"a"}`,
			`{"numberOutsideConst":1}`,
			// The controls still refuse what they always refused, which is what
			// a blanket "every typed enum admits nothing" would have satisfied
			// without meaning anything.
			`{"typedConst":"b"}`,
			`{"typedEnum":"c"}`,
			`{"untypedEnum":"b"}`,
			`{"unionEnum":"b"}`,
			`{"nullableEnum":5}`,
			`{"integerEnum":3}`,
			`{"integerFloatSpelling":2}`,
			`{"numberEnum":3}`,
			`{"objectEnum":{}}`,
			`{"arrayEnum":[]}`,
			`{"boolEnum":false}`,
			`{"okItems":["c"]}`,
			// The keyword positions. Each of these honoured `false` and
			// {"enum":[]} already and read the third spelling as a sub-schema
			// stating nothing.
			`{"constOutsideUnevalItems":[1,2]}`,
			`{"constOutsideUnevalItems":[1,"a"]}`,
			`{"constOutsideUnevalProps":{"x":1}}`,
			`{"constOutsideNames":{"a":1}}`,
			`{"constOutsideDependent":{"k":1}}`,
			`{"constOutsideContains":[]}`,
			`{"constOutsideContains":[1]}`,
			`{"constOutsideContains":["a"]}`,
		},
	)
}

// TestEnumFilterReadsWhatTheTypeAsserts holds the three dialect readings issue
// #145's filter has to get right, each of which would otherwise refuse a
// document the schema states -- the failure this repository treats as worse than
// a missing check.
//
// draft3/any is the half the filter cannot reach at all, and the reason
// generateEnumDef also asks whether its members fit the const form. "any" is a
// type name that maps to no Go type of its own, so the enum's base came out
// `any`: `const X Root = "a"` is `invalid constant type`, and the Validate
// emitted beside it took an interface receiver. Nothing is filtered here -- the
// type admits every member there is -- so the only thing that changes is the
// form the enum is emitted in, and the raw form holds any member because it
// compares JSON encodings rather than Go constants.
//
// draft3/typeAlternatives is the type array that carries a whole schema. Those
// alternatives are held apart from the names, so the names are not the whole of
// the type and a member matching none of them may still match one of the
// schemas. Reading the names alone would drop 5.
//
// draft7/refSibling is the type that asserts nothing. Through draft 7 a $ref
// replaces the schema object it sits in, so a "type" written beside one forbids
// nothing and filtering against it would empty the enum and refuse 5 -- which
// the referenced integer schema admits.
//
// The invalid rows are what keep each of these from being "emit nothing and
// compile": every listed member is still accepted and everything outside the
// list is still refused.
func TestEnumFilterReadsWhatTheTypeAsserts(t *testing.T) {
	t.Run("draft3", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/enum_draft3_type_names.json",
			[]string{
				`{}`,
				`{"anyTyped":"a"}`,
				`{"anyTyped":"b"}`,
				`{"typeAlternatives":"a"}`,
				`{"typeAlternatives":5}`,
			},
			[]string{
				`{"anyTyped":"c"}`,
				`{"anyTyped":5}`,
				`{"anyTyped":null}`,
				`{"typeAlternatives":"b"}`,
				`{"typeAlternatives":7}`,
			},
		)
	})
	t.Run("draft7RefSibling", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/enum_ref_sibling_draft7.json",
			[]string{
				`{}`,
				`{"stringSibling":5}`,
				`{"objectSibling":5}`,
			},
			[]string{
				`{"stringSibling":"a"}`,
				`{"objectSibling":"a"}`,
				`{"objectSibling":{}}`,
			},
		)
	})
}

// TestOneOfSingleBranchOnAProperty is issue #150's first table.
//
// A oneOf of one branch on a declared property is still rendered as the
// sealed-interface union -- one variant, and a variant selection cannot judge is
// matched by every document the field can hold. So the branch was tested
// nowhere, whatever it said: a const, the `false` schema and `{"enum":[]}` all
// accepted the documents they exist to refuse. The same three schemas at a
// document root, at an array element and at a map value have always been right,
// because none of those positions builds a union.
//
// The controls are the rows that must not move. boundBranch and typedBranch are
// decided by selection itself -- the Go type settles the `type` and the union's
// own Checks settle the bound -- so they keep the union and keep rejecting.
// twoBranch, list and map were already correct and stay correct.
func TestOneOfSingleBranchOnAProperty(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/oneof_single_branch_positions.json",
		[]string{
			`{}`,
			`{"constBranch":"a"}`,
			`{"objBranch":{"k":"x"}}`,
			`{"boundBranch":9}`,
			`{"typedBranch":"z"}`,
			`{"twoBranch":"a"}`,
			`{"twoBranch":3}`,
			`{"list":["a"]}`,
			`{"list":[]}`,
			`{"map":{"x":"a"}}`,
			`{"map":{}}`,
		},
		[]string{
			`{"constBranch":"b"}`, // the defect
			`{"falseBranch":1}`,   // the defect: nothing satisfies `false`
			`{"falseBranch":"a"}`,
			`{"emptyEnum":1}`, // the defect: nothing satisfies an empty enum
			`{"emptyEnum":"a"}`,
			`{"objBranch":{}}`,
			`{"boundBranch":1}`,
			`{"typedBranch":1}`,
			`{"twoBranch":"b"}`,
			`{"list":["b"]}`,
			`{"map":{"x":"b"}}`,
		},
	)
}

// TestNullableCompositionKeepsItsBranch is issue #150's second table.
//
// {X, {"type":"null"}} collapses to a pointer at X's own Go type, and X is then
// read by nothing else: the field rules and the element checks a property
// normally carries are taken from the property schema, which here is the wrapper
// and states none of them. Every row below whose branch resolves to a bare Go
// type therefore lost its checks -- a const, a length, an element bound, a
// minItems, a numeric bound, a map-value bound -- and the two rows that admit no
// document at all, `false` and `{"enum":[]}`, accepted everything.
//
// The last four are the controls. anyEnum and anyObj resolve to a type of their
// own, whose Validate the field calls, so they were right before and must stay
// pointers to it rather than becoming wrappers. anyPlain and anyArr state nothing
// their Go type does not already say, and naming them would cost a field type and
// buy no check.
//
// Every null row is an accept-control on the same line: the whole point of the
// shape is that null is admitted, and a fix that forgot the null branch would
// pass every rejection here and still be wrong.
func TestNullableCompositionKeepsItsBranch(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/nullable_composition_branches.json",
		[]string{
			`{}`,
			`{"anyConst":"a"}`,
			`{"anyConst":null}`,
			`{"oneConst":"a"}`,
			`{"oneConst":null}`,
			`{"anyFalse":null}`,
			`{"anyEmpty":null}`,
			`{"anyLen":"abc"}`,
			`{"anyLen":null}`,
			`{"anyItems":["abc","dddd"]}`,
			`{"anyItems":[]}`,
			`{"anyItems":null}`,
			`{"anyMinIt":[1,2]}`,
			`{"anyMinIt":null}`,
			`{"anyBound":5}`,
			`{"anyBound":null}`,
			`{"oneMapVal":{"k":"abc"}}`,
			`{"oneMapVal":{}}`,
			`{"oneMapVal":null}`,
			`{"anyEnum":"a"}`,
			`{"anyEnum":null}`,
			`{"anyObj":{"k":"x"}}`,
			`{"anyObj":null}`,
			`{"anyPlain":"z"}`,
			`{"anyPlain":null}`,
			`{"anyArr":["z"]}`,
			`{"anyArr":null}`,
		},
		[]string{
			`{"anyConst":"b"}`,
			`{"oneConst":"b"}`,
			`{"anyFalse":1}`,
			`{"anyFalse":"a"}`,
			`{"anyEmpty":1}`,
			`{"anyEmpty":"a"}`,
			`{"anyLen":"ab"}`,
			`{"anyItems":["ab"]}`,
			`{"anyMinIt":[]}`,
			`{"anyMinIt":[1]}`,
			`{"anyBound":1}`,
			`{"oneMapVal":{"k":"a"}}`,
			`{"anyEnum":"b"}`,
			`{"anyObj":{}}`,
			`{"anyPlain":1}`,
			`{"anyArr":[1]}`,
		},
	)
}

// TestNullableCompositionRoundTrips is the marshalling half of the same fixture,
// and the guard the validation table cannot be.
//
// Giving these positions a name gives them the raw-JSON wrapper, which is a
// struct: encoding/json's omitempty never drops a struct, and the wrapper's own
// MarshalJSON writes "null" when it holds no bytes. So without ",omitzero" every
// absent property came back as a null the document never carried -- accepted by
// Validate, and wrong. The wrapper reports absence through IsZero, and omitzero
// is what reads it; a present null keeps the bytes and survives.
//
// The rows are chosen so that both directions are watched: {} must stay {}, and
// each explicit null must stay a null.
func TestNullableCompositionRoundTrips(t *testing.T) {
	runRoundTripCases(t,
		"testdata/schemas/regression/nullable_composition_branches.json",
		`{}`,
		`{"anyConst":null}`,
		`{"anyConst":"a"}`,
		`{"oneConst":null}`,
		`{"anyFalse":null}`,
		`{"anyEmpty":null}`,
		`{"anyLen":null}`,
		`{"anyLen":"abc"}`,
		`{"anyItems":null}`,
		`{"anyItems":["abc"]}`,
		`{"anyBound":null}`,
		`{"anyBound":7}`,
		`{"oneMapVal":null}`,
		`{"anyEnum":null}`,
		`{"anyObj":null}`,
		`{"anyPlain":null}`,
		`{"anyArr":null}`,
		`{"anyConst":"a","anyLen":"abc","anyBound":7,"anyEnum":"c","anyPlain":"z"}`,
	)
}

// TestNullableFormatPositionsRoundTrip is the same property on the fixture that
// found it. Three of its eleven properties resolve to the wrapper without being
// null-only, and those three carried no omit tag at all, so a document with none
// of them came back carrying all three as nulls.
func TestNullableFormatPositionsRoundTrip(t *testing.T) {
	runRoundTripCases(t,
		"testdata/schemas/regression/nullable_format_positions.json",
		`{}`,
		`{"inline":null}`,
		`{"inline":"1.2.3.4"}`,
		`{"stamp":null,"mail":null}`,
		`{"mail":"a@b.example"}`,
	)
}

// TestNullableAnyOfWithANamedBranch is the same collapse at the site that answers
// with an alias, `type X *T`.
//
// Go forbids a method on a type whose underlying type is a pointer, so X has no
// Validate and nothing reaches T's -- not a caller holding an X, and not the
// property here, which is typed by a $ref to X. Both definitions accepted every
// object. The enum spelling did worse: the branch was generated under the alias's
// own name, so the same identifier was declared twice and the output did not
// compile, which is why runValidationCases is the right home for this one.
func TestNullableAnyOfWithANamedBranch(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/nullable_anyof_named_branch.json",
		[]string{
			`{}`,
			`{"obj":{"k":"x"}}`,
			`{"obj":null}`,
			`{"word":"a"}`,
			`{"word":"c"}`,
			`{"word":null}`,
		},
		[]string{
			`{"obj":{}}`,
			`{"obj":{"k":1}}`,
			`{"word":"b"}`,
		},
	)
}

// TestRefSiblingValuesFollowTheDraft is issue #151, both sides of the split.
//
// Through draft 7 a $ref replaces everything written beside it, and the enum arm
// of each ladder that turns a schema into a Go type ran ahead of the ref arms --
// so the sibling decided the type and the reference was never followed. That is
// over-enforcement: "abc" satisfies the referenced schema and was refused for a
// const the draft says is not there to be read. All five draft-7 accept rows
// below were rejections before this change.
//
// "ab" is what keeps the fix from being "drop the check": with the sibling
// ignored the *target* still applies, and a schema that fell through to `any`
// would accept it. The 2020-12 fixture is the other control -- there the sibling
// does apply, and every one of its rejections has to survive a change that
// removes the same rejections one draft earlier.
func TestRefSiblingValuesFollowTheDraft(t *testing.T) {
	t.Run("draft7IgnoresTheSibling", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/ref_sibling_values_draft7.json",
			[]string{
				`{}`,
				`{"constSibling":"abc"}`,
				`{"enumSibling":"abc"}`,
				`{"emptyEnumSibling":"abc"}`,
				`{"listSibling":["abc"]}`,
				`{"mapSibling":{"k":"abc"}}`,
				`{"namedSibling":"abc"}`,
				`{"namedEmptyEnum":"abc"}`,
				`{"noSibling":"abc"}`,
			},
			[]string{
				`{"constSibling":"ab"}`,
				`{"enumSibling":"ab"}`,
				`{"emptyEnumSibling":"ab"}`,
				`{"listSibling":["ab"]}`,
				`{"mapSibling":{"k":"ab"}}`,
				`{"namedSibling":"ab"}`,
				`{"namedEmptyEnum":"ab"}`,
				`{"noSibling":"ab"}`,
			},
		)
	})
	t.Run("2020AppliesTheSibling", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/ref_sibling_values_2020.json",
			[]string{
				`{}`,
				`{"constSibling":"a"}`,
				`{"enumSibling":"a"}`,
				`{"enumSibling":"c"}`,
				`{"listSibling":["a"]}`,
				`{"mapSibling":{"k":"a"}}`,
				`{"namedSibling":"a"}`,
				`{"noSibling":"false"}`,
			},
			[]string{
				`{"constSibling":"false"}`,
				`{"enumSibling":"false"}`,
				`{"emptyEnumSibling":"false"}`,
				`{"listSibling":["false"]}`,
				`{"mapSibling":{"k":"false"}}`,
				`{"namedSibling":"false"}`,
				`{"namedEmptyEnum":"false"}`,
				`{"namedEmptyEnum":"a"}`,
			},
		)
	})
}

// TestRefSiblingTargetBindsWithItsSibling is issue #153, the half #151 left.
//
// From 2019-09 on a $ref is an ordinary applicator, so the schema asserts the
// reference and the `enum` or `const` beside it at once. The enum arm of each
// type ladder claimed the schema before its ref arms, so the sibling applied
// *instead of* the reference and the target was never followed: "abc" was
// accepted at every position below although the target's minLength forbids it.
//
// The three rows per position are what say the two halves both bind rather than
// one having replaced the other. "abcde" satisfies both and must be accepted --
// a fix that simply forbade the shape would fail there. "abc" is refused by the
// target alone and is the rejection this issue adds. "zzzzz" is refused by the
// sibling alone and was already refused before it, so it is the control that the
// sibling did not stop being read.
//
// noSibling is the target on its own, noRef the sibling on its own; both keep
// the answers they had. ref_sibling_values_draft7.json is the control on the
// other side of the split, and it must go on accepting the document the sibling
// forbids -- #151's answer there is untouched by this.
func TestRefSiblingTargetBindsWithItsSibling(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/ref_sibling_target_2020.json",
		[]string{
			`{}`,
			`{"constSatisfied":"abcde"}`,
			`{"enumSibling":"abcde"}`,
			`{"listSibling":["abcde"]}`,
			`{"mapSibling":{"k":"abcde"}}`,
			`{"namedEnum":"abcde"}`,
			`{"noSibling":"abcde"}`,
			`{"noRef":"abc"}`,
			`{"noRef":"abcde"}`,
			`{"dynSibling":"abc"}`,
		},
		[]string{
			`{"constSatisfied":"zzzzz"}`,
			`{"constForbidden":"abc"}`,
			`{"constForbidden":"abcde"}`,
			`{"enumSibling":"abc"}`,
			`{"enumSibling":"zzzzz"}`,
			`{"listSibling":["abc"]}`,
			`{"mapSibling":{"k":"abc"}}`,
			`{"namedEnum":"abc"}`,
			`{"namedEnum":"zzzzz"}`,
			`{"namedConst":"abc"}`,
			`{"noSibling":"abc"}`,
			`{"noRef":"zzzzz"}`,
			// A $dynamicRef is the reference the merge cannot follow, so the enum
			// arms go on claiming it and the const goes on being enforced. These
			// two are what catch standing them down for a reference no later arm
			// picks up, which left the property accepting every value.
			`{"dynSibling":"zz"}`,
			`{"dynSibling":"abcde"}`,
		},
	)
}

// TestHiddenKeywordSpellingsAreRead is issue #154.
//
// Five gates decide what a schema states by re-marshalling it and reading the
// keys, and two spellings survive that trip as nothing at all: `"enum": []`,
// because the field is tagged omitempty, and `"const": null`, because
// encoding/json leaves a *any nil for a null and the flag recording it is tagged
// "-". A schema stating only one of those read as a schema stating nothing, so
// acceptsEveryValue answered true for a schema admitting no value whatever and
// each gate handed its position a type with no check on it.
//
// Every rejection below is paired with the same statement in the spelling that
// was always read -- `false` beside the empty enum, a string constant beside the
// null one. The pair is the point: before this the two answered differently, and
// a position answering by spelling rather than by meaning is the shape #142 and
// #146 already had to close twice.
func TestHiddenKeywordSpellingsAreRead(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/hidden_keyword_spellings.json",
		[]string{
			`{}`,
			`{"anyOfSummaryEmptyEnum":{"k":"y"}}`,
			`{"anyOfSummaryFalse":{"k":"y"}}`,
			`{"constNullBranch":null}`,
			`{"constStringBranch":"a"}`,
			`{"oneOfConstNull":null}`,
			`{"oneOfConstNull":"x"}`,
			`{"patternConstNull":{"a1":null}}`,
			`{"patternConstNull":{}}`,
			`{"patternConstString":{"b1":"a"}}`,
			`{"plainEnum":"a"}`,
		},
		[]string{
			`{"anyOfSummaryEmptyEnum":{"a":"x"}}`,
			`{"anyOfSummaryFalse":{"a":"x"}}`,
			`{"constNullBranch":1}`,
			`{"constStringBranch":1}`,
			`{"oneOfConstNull":1}`,
			`{"patternConstNull":{"a1":1}}`,
			`{"patternConstString":{"b1":"z"}}`,
			`{"plainEnum":"z"}`,
		},
	)
}

// TestRecursiveRefBehavesLikeRef is the first half of what a $recursiveRef
// needs, and it is the half the reason on file for these ten groups had wrong.
//
// Nothing here declares $recursiveAnchor: true, so the keyword resolves exactly
// where a $ref would -- the resource it is written in -- and the resolver has
// always got that right. What collapsed the type to `any` was the cycle the
// resolved target closes: myobject's additionalProperties are myobjects, and a
// schema that contains itself is not a tree of composite literals. The evaluator
// carries it as a node variable the cycle points back at.
//
// The valid half is the control, and it is what the whole recursion is for. A
// generated check that rejected `{"foo":{"bar":"hi"}}` would be a false
// rejection reached by "fixing" the recursion into a refusal.
func TestRecursiveRefBehavesLikeRef(t *testing.T) {
	runValidationCasesForType(t,
		"testdata/schemas/regression/recursive_ref_like_ref.json", "Root",
		[]string{
			`1`,
			`{"foo":"hi"}`,
			`{"foo":{"bar":"hi"}}`,
			`{"foo":{"bar":{"baz":"hi"}}}`,
		},
		[]string{
			`{"foo":1}`,
			`{"foo":{"bar":1}}`,
			`{"foo":{"bar":{"baz":1}}}`,
		},
	)
}

// TestRecursiveRefTakesTheOutermostAnchor is the other half: a $recursiveRef
// whose destination the schema text does not fix.
//
// Three resources declare $recursiveAnchor: true, and which of them the keyword
// inside inner.json means depends on whether the document's own property names
// sent evaluation down `then` or `else`. `{"alpha":1.1}` enters anyLeafNode,
// where a float is a leaf like any other; `{"november":1.1}` enters integerNode,
// which admits only objects and integers. The keyword is one line of schema and
// the two documents differ in one letter.
//
// The nested pair matter more than the flat ones. They are the documents that
// prove the anchor is re-read at each level rather than resolved once: the outer
// object picks the branch, and the inner value is judged by the branch the
// *outer* object picked, because that resource is still the outermost one in
// scope when the recursion comes back round.
func TestRecursiveRefTakesTheOutermostAnchor(t *testing.T) {
	runValidationCasesForType(t,
		"testdata/schemas/regression/recursive_ref_outermost_anchor.json", "Root",
		[]string{
			`{"alpha":1.1}`,
			`{"alpha":{"zulu":1.1}}`,
			`{"november":1}`,
			`{"november":{"alpha":2}}`,
		},
		[]string{
			`{"november":1.1}`,
			`{"november":{"alpha":1.1}}`,
			`{"november":"text"}`,
		},
	)
}

// TestDynamicRefResolvesThroughTheScopeChain is the $dynamicRef spelling of the
// same thing, and the shape the keyword was designed for: one generic list
// definition whose element type is supplied by whichever resource referred to
// it.
//
// genericList declares an itemType anchor of its own that admits everything --
// the bookend, without which the reference would be an ordinary $ref -- and
// numberList and stringList each declare one that does not. Both are on the
// dynamic scope when the reference is reached and the outermost wins, so a
// generated check that searched inwards-out would answer with the permissive
// one and accept every list. The valid pairs are what catches the opposite
// mistake, a check that answered with the wrong sibling and rejected the list
// the document asked for.
func TestDynamicRefResolvesThroughTheScopeChain(t *testing.T) {
	runValidationCasesForType(t,
		"testdata/schemas/regression/dynamic_ref_scope_chain.json", "Root",
		[]string{
			`{"kindOfList":"numbers","list":[1.1]}`,
			`{"kindOfList":"strings","list":["foo"]}`,
			`{"kindOfList":"numbers","list":[]}`,
		},
		[]string{
			`{"kindOfList":"numbers","list":["foo"]}`,
			`{"kindOfList":"strings","list":[1.1]}`,
		},
	)
}

// TestDynamicRefIgnoresALeftScope pins the direction the scope is a *stack*
// rather than a set.
//
// first_scope declares thingy as a number and is entered by `if`, which finishes
// before `then` begins -- so by the time the reference is reached that resource
// has been left and its anchor is gone. inner_scope declares thingy as a string
// and is where the reference statically points, which is what makes it a bookend
// and nothing else. The answer is second_scope's, because that is the outermost
// resource still in scope, and it says null.
//
// So the two rejections are each a different mistake: `42` is what a scope that
// never pops accepts, and `"a string"` is what resolving statically to the
// bookend accepts. Only `null` is right, and it is the only document here that
// nothing else would accept.
func TestDynamicRefIgnoresALeftScope(t *testing.T) {
	runValidationCasesForType(t,
		"testdata/schemas/regression/dynamic_ref_left_scope.json", "Root",
		[]string{
			`null`,
		},
		[]string{
			`42`,
			`"a string"`,
		},
	)
}

// TestRecursiveCompositionIsEnforced is the recursion on its own, with no
// dynamic reference anywhere near it.
//
// A root anyOf of "a string, or an object of these" is an ordinary schema and
// was `type Root any` -- a type Go forbids methods on, so json.Unmarshal into it
// could not fail and every document below was accepted. The evaluator refused it
// for the one reason that has nothing to do with $ref semantics: the compiled
// literal would have to contain itself.
//
// It is also the fixture that isolates the first of the three spellings
// HelpersReferencedBy watches for. Nothing here emits a DynamicRef or a resource
// frame, so a file that stopped matching a bare node reference would name
// _schemaNode.Ref with no such field declared, and this is the only test in the
// tree that would fail to build.
func TestRecursiveCompositionIsEnforced(t *testing.T) {
	runValidationCasesForType(t,
		"testdata/schemas/regression/recursive_composition.json", "Root",
		[]string{
			`"hi"`,
			`{}`,
			`{"a":"hi"}`,
			`{"a":{"b":{"c":"hi"}}}`,
		},
		[]string{
			`1`,
			`{"a":1}`,
			`{"a":{"b":{"c":1}}}`,
		},
	)
}

// TestDynamicRefEntersAResourceReferredToInTheMiddle is the case the test suite
// does not have, and the one that decides whether the dynamic scope is a stack
// of *resources entered* or a list of resource roots landed on.
//
// `then` points at outer#/$defs/entry -- inside the resource, not at its root --
// and outer is where the restrictive `leaf` anchor lives. Reading the scope as
// roots landed on never records outer at all, and the reference then answers
// with inner's permissive `leaf`, which admits everything. Reading it as
// resources entered records outer, outer is outermost, and its anchor wins.
//
// The anchor is on outer's own root rather than in its $defs, which is the
// second thing this pins. A frame entered part-way into a resource hangs off the
// node the reference landed on, not off the resource root, so the shorthand that
// says "the node whose frame this is" cannot stand in for the root here -- and
// `{"a":{"b":1}}` is judged by outer's `entry` subschema instead of by outer,
// which admits it.
//
// The if/then is not decoration. A root that takes a static Go type keeps its
// static checks, and this reference is compiled at all only because the root
// composition sends the whole schema to the evaluator -- so the same three
// resources written under a bare root $ref are still unenforced, which is the
// gap noted beside this test rather than closed by it.
//
// The rejections are what the permissive reading accepts, and `5` and
// `{"z":{"b":1}}` are the controls for the two halves of the conditional. The
// expected verdicts are not this repository's opinion: they were taken from
// google/jsonschema-go, run over the same six documents, before this test was
// written.
func TestDynamicRefEntersAResourceReferredToInTheMiddle(t *testing.T) {
	runValidationCasesForType(t,
		"testdata/schemas/regression/dynamic_ref_mid_resource_entry.json", "Root",
		[]string{
			`{"a":{"b":"x"}}`,
			`{"a":{"b":{"c":"x"}}}`,
			`5`,
		},
		[]string{
			`{"a":{"b":1}}`,
			`{"a":{"b":true}}`,
			`{"z":{"b":1}}`,
		},
	)
}

// TestDynamicRefResolvesPerDocumentUnderATypedRoot is issue #160, and the gap
// the comment beside TestDynamicRefEntersAResourceReferredToInTheMiddle named
// rather than closed.
//
// #159 gave the runtime evaluator a dynamic scope, so a bookended $dynamicRef
// resolves per document -- for the schemas that reach the evaluator, which are
// only the ones the static path declines. This root declines nothing: it is an
// object with two properties and takes an ordinary struct, so its $dynamicRef
// was resolved once, against the schema text. genericList's own itemType admits
// everything, that is the answer the one resolution reached, and the generated
// code accepted every list of anything.
//
// The two properties are the whole point. They reach the same genericList
// through different resources, so the reference means "number" down one and
// "string" down the other, and no single Go type can be both. A generator that
// picks one binding is right about half the documents by construction.
//
// Each rejection is paired with the same document under the other property,
// which is what says the check discriminates rather than simply refusing lists.
// The empty and two-element lists are the controls for a fix that rejected every
// list, or that only looked at the first element. The verdicts were taken from
// python-jsonschema, go-jsonschema (santhosh-tekuri) and rust-boon, run over
// these eight documents through Bowtie before the test was written; all three
// agree on every one.
func TestDynamicRefResolvesPerDocumentUnderATypedRoot(t *testing.T) {
	runValidationCasesForType(t,
		"testdata/schemas/regression/dynamic_ref_typed_root.json", "Root",
		[]string{
			`{}`,
			`{"numbers":{"list":[1.1]}}`,
			`{"strings":{"list":["foo"]}}`,
			`{"numbers":{"list":[]},"strings":{"list":[]}}`,
			`{"numbers":{"list":[1,2.5]},"strings":{"list":["a","b"]}}`,
		},
		[]string{
			`{"numbers":{"list":["foo"]}}`,
			`{"strings":{"list":[1.1]}}`,
			`{"numbers":{"list":[1,"x"]}}`,
		},
	)
}

// TestRecursiveRefResolvesPerDocumentUnderATypedRoot is the $recursiveRef half
// of issue #160, and it is the suite's "$recursiveRef with $recursiveAnchor"
// shape with the if/then root taken off.
//
// TestRecursiveRefTakesTheOutermostAnchor asks the same question of the same
// three resources through a conditional root, which is the thing that sent the
// whole schema to the evaluator. Here the root is an object with two properties
// and takes a struct, so the static path claimed it and the keyword was resolved
// once against the schema text -- and the two properties enter different
// anchored resources, so one resolution cannot be right about both.
//
// This is also the only fixture in the tree that makes the $recursiveAnchor
// spelling of the count load-bearing. That anchor has no name: it is filed under
// the empty string and belongs to the *root of the resource that writes it*, so
// counting nodes rather than resource roots would answer differently here and
// nowhere else.
//
// `{"anyLeaf":{"a":1.1}}` and `{"intLeaf":{"a":1.1}}` are one letter apart and
// get opposite verdicts, which is what says the anchor is read per path. The
// nested pair are what say it is re-read at each level rather than once: the
// outer property picks the resource and the inner value is still judged by it.
// The verdicts are Bowtie's, over python-jsonschema, go-jsonschema and
// rust-boon, which agree on all eight.
func TestRecursiveRefResolvesPerDocumentUnderATypedRoot(t *testing.T) {
	runValidationCasesForType(t,
		"testdata/schemas/regression/recursive_ref_typed_root.json", "Root",
		[]string{
			`{}`,
			`{"anyLeaf":{"a":1.1}}`,
			`{"anyLeaf":{"a":{"b":"x"}}}`,
			`{"intLeaf":{"a":1}}`,
			`{"intLeaf":{"a":{"b":1}}}`,
		},
		[]string{
			`{"intLeaf":{"a":1.1}}`,
			`{"intLeaf":{"a":{"b":1.1}}}`,
			`{"intLeaf":{"a":"x"}}`,
		},
	)
}

// TestReferenceKeywordsAreIgnoredByDraftsWithoutThem is issue #161, and it is
// the rarer half of these findings: over-enforcement, which refuses a document
// the draft permits.
//
// $recursiveRef arrived in 2019-09 and $dynamicRef replaced it in 2020-12, so a
// draft-7 schema carrying either states a keyword its dialect never defined --
// and every draft says to ignore an unknown keyword. Schema.EffectiveRef
// returned $recursiveRef whatever the draft, so this generator honoured it
// everywhere, and a draft-7 recNode whose additionalProperties are recNodes
// refused {"recTree":{"a":"x"}}, which a draft-7 reader accepts.
//
// The corpus cannot see this. $recursiveRef appears only under draft2019-09 in
// the pinned suite and $dynamicRef only under draft2020-12 and v1, so the gate
// has no file carrying either keyword into a draft that lacks it, and these four
// fixtures are the only thing that does.
//
// The accept-controls matter more than the rejections here. A gate that simply
// stopped reading both keywords would pass every draft-7 case below and break
// the drafts that define them, so 2019-09 is asserted to go on honouring
// $recursiveRef -- two levels deep, so a fix that dropped only the recursion
// would fail -- and 2020-12 and v1 to go on honouring $dynamicRef. 2019-09 also
// carries the $dynamicRef half, which it does not define either, and that is the
// one draft where the two keywords must answer differently: a gate written per
// draft rather than per keyword cannot pass both of its rows.
//
// The verdicts are Bowtie's, over python-jsonschema, go-jsonschema and
// rust-boon, which agree on every document asserted here -- except v1's, which
// none of the three can answer because none of them declares that dialect. The
// v1 fixture is its 2020-12 twin with two URIs changed, so what it asserts is
// that v1 follows 2020-12, which is what DraftV1 exists to say.
//
// The one case the three split on -- whether 2020-12 still honours
// $recursiveRef, which its core vocabulary no longer defines -- is deliberately
// asserted nowhere, and schemagen goes on honouring it; see
// recursiveRefDefinedForDraft.
func TestReferenceKeywordsAreIgnoredByDraftsWithoutThem(t *testing.T) {
	t.Run("draft7 ignores both", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/ref_keywords_draft7.json",
			[]string{
				`{}`,
				`{"recTree":{"leaf":1},"dynTree":{"leaf":1}}`,
				`{"recTree":{"a":"x"}}`,
				`{"dynTree":{"a":"x"}}`,
				`{"recTree":{"a":{"leaf":1}}}`,
				`{"recTree":{"a":{"leaf":"x"}}}`,
				`{"dynTree":{"a":{"leaf":1}}}`,
				`{"dynTree":{"a":{"leaf":"x"}}}`,
			},
			[]string{
				// `type` is the control: it is read on every draft, so a fixture
				// that rejected nothing at all could not tell a working generator
				// from one that emitted no Validate.
				`{"recTree":"x"}`,
				`{"dynTree":"x"}`,
			},
		)
	})

	t.Run("2019-09 honours $recursiveRef and ignores $dynamicRef", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/ref_keywords_2019.json",
			[]string{
				`{}`,
				`{"recTree":{"leaf":1},"dynTree":{"leaf":1}}`,
				`{"recTree":{"a":{"leaf":1}}}`,
				`{"dynTree":{"a":"x"}}`,
				`{"dynTree":{"a":{"leaf":1}}}`,
				`{"dynTree":{"a":{"leaf":"x"}}}`,
			},
			[]string{
				`{"recTree":{"a":"x"}}`,
				`{"recTree":{"a":{"leaf":"x"}}}`,
				`{"recTree":"x"}`,
				`{"dynTree":"x"}`,
			},
		)
	})

	t.Run("2020-12 honours $dynamicRef", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/ref_keywords_2020.json",
			[]string{
				`{}`,
				`{"dynTree":{"leaf":1}}`,
				`{"dynTree":{"a":{"leaf":1}}}`,
			},
			[]string{
				`{"dynTree":{"a":"x"}}`,
				`{"dynTree":{"a":{"leaf":"x"}}}`,
				`{"dynTree":"x"}`,
			},
		)
	})

	t.Run("v1 honours $dynamicRef", func(t *testing.T) {
		runValidationCases(t,
			"testdata/schemas/regression/ref_keywords_v1.json",
			[]string{
				`{}`,
				`{"dynTree":{"leaf":1}}`,
				`{"dynTree":{"a":{"leaf":1}}}`,
			},
			[]string{
				`{"dynTree":{"a":"x"}}`,
				`{"dynTree":{"a":{"leaf":"x"}}}`,
				`{"dynTree":"x"}`,
			},
		)
	})
}

// TestDynamicAnchorOnANestedIdBelongsToThatResource is issues #163 and #164,
// which are one defect seen from two ends.
//
// #159 replaced findDynamicAnchor with resourceDynamicAnchor for the runtime
// evaluator: an anchor written *on* a node that carries its own $id belongs to
// the resource that node starts, not to the one that merely contains it, so a
// resource nothing ever enters must not answer for evaluations passing
// overhead. The static path went on reading it the other way, and this fixture
// is where the two answers part company.
//
// `strayItemType` is such a node -- a $defs entry with an $id and a
// $dynamicAnchor, referred to by nothing. Credited to the document root, it sits
// on the outermost frame of every evaluation and wins every time, so genericBox
// bound `value` to a number for both paths that reach it. That is the dangerous
// direction: `{"value":"x"}` is valid down either path and was refused, at
// decode, by FirstBox and SecondBox alike -- while Root, which #160 sends to the
// evaluator, accepted the same document. One schema, two answers, decided by
// which generated type the caller happened to hold.
//
// Root is the accept-control for the evaluator half and FirstBox and SecondBox
// for the static half; asserting both is what says the two paths now agree
// rather than that one of them was silenced. `{}` and `{"first":{}}` are the
// controls for a fix that had simply stopped checking: required is still read,
// so a Validate that accepted everything fails here.
//
// The verdicts are Bowtie's over python-jsonschema, go-jsonschema and rust-boon,
// which agree on all ten documents, taken before this test was written. The
// FirstBox rows were asked of a document whose root is `{"$ref": "firstBox"}`
// over the same $defs, which is what "validated as the root of its own
// evaluation" means for a generated type.
func TestDynamicAnchorOnANestedIdBelongsToThatResource(t *testing.T) {
	t.Run("the document root", func(t *testing.T) {
		runValidationCasesForType(t,
			"testdata/schemas/regression/dynamic_ref_boundary_anchor.json", "Root",
			[]string{
				`{}`,
				`{"first":{"value":1}}`,
				`{"first":{"value":"x"}}`,
				`{"second":{"value":"x"}}`,
				`{"first":{"value":{"a":1}}}`,
			},
			[]string{
				`{"first":{}}`,
			},
		)
	})

	// The two $defs types the stray anchor was answering for. They are asserted
	// separately because the defect was invisible from the root: #160 routes the
	// root to the evaluator, which already read the anchor correctly, and only a
	// caller holding one of these ever met the static binding.
	for _, typeName := range []string{"FirstBox", "SecondBox"} {
		t.Run(typeName, func(t *testing.T) {
			runValidationCasesForType(t,
				"testdata/schemas/regression/dynamic_ref_boundary_anchor.json", typeName,
				[]string{
					`{"value":1}`,
					`{"value":"x"}`,
					`{"value":{"a":1}}`,
				},
				[]string{
					`{}`,
				},
			)
		})
	}
}

// TestDynamicAnchorDeclaredByAnEnteredResourceStillWins is the accept-control
// for the change above, and the reason it is not spelled "always take the
// bookend".
//
// Narrowing the reading takes anchors away from resources that never declared
// them; it must take nothing away from a resource that did. `holder` declares
// itemType in its own $defs -- an ordinary node inside the resource, not a
// boundary -- and `strayRes` is the boundary node that used to outrank it by
// sitting one level further out. So the fix has to keep searching after the
// document root answers nothing, and a fix that stopped at the first frame or
// fell straight through to the bookend would accept `{"box":{"value":1}}`,
// which all three implementations refuse.
//
// GenericBox is the same subschema judged as its own root, where the answer is
// the permissive bookend because no evaluation starting there ever enters
// holder. Both rows come from the same generated file, so together they say the
// binding follows the path rather than the text.
//
// Verdicts from Bowtie over python-jsonschema, go-jsonschema and rust-boon,
// unanimous on all eight documents.
func TestDynamicAnchorDeclaredByAnEnteredResourceStillWins(t *testing.T) {
	t.Run("the document root", func(t *testing.T) {
		runValidationCasesForType(t,
			"testdata/schemas/regression/dynamic_ref_inner_frame_anchor.json", "Root",
			[]string{
				`{}`,
				`{"box":{"value":"x"}}`,
				`{"box":{"value":"x","extra":1}}`,
			},
			[]string{
				`{"box":{"value":1}}`,
				`{"box":{}}`,
			},
		)
	})

	t.Run("GenericBox", func(t *testing.T) {
		runValidationCasesForType(t,
			"testdata/schemas/regression/dynamic_ref_inner_frame_anchor.json", "GenericBox",
			[]string{
				`{"value":1}`,
				`{"value":"x"}`,
			},
			[]string{
				`{}`,
			},
		)
	})
}

// TestInferredArrayJudgesItsElementByTheWholeSubSchema pins issue #166.
//
// An array *inferred* from `items` rather than declared with "type":"array"
// reduced its element sub-schema to at most the one JSON type that sub-schema
// declared, and dropped everything else written beside it. So
// {"items":{"type":"object","required":["a"]}} accepted [{}] -- while the
// identical sub-schema under an explicit "type":"array" rejected it, which is
// the `declared` row here and the contrast the issue is named for. A $ref
// element took the same reduction applied to its *target*, so the arm that
// calls a named type's Validate was reached only when the target declared no
// single type.
//
// The reduction sat on four keywords, not one: the element of `items`, each
// slot of `prefixItems`, the tail `items` governs past a prefix, and the
// sub-schema of `contains` -- and `contains` shares its extraction with the
// declared array, which is why `declaredContains` moved with it.
//
// Two rows are false rejections rather than missing checks, and they are why
// the fix is not simply "check more". `allOfBranch` is an inferred array the
// merge types "array" off a branch's `items`; that guess was read as a
// declaration, so the Go type was a plain slice and refused the string, the
// object, the number and the null the schema permits. `nullableSlotOfDeclared`
// is a tuple slot stating {"type":["object","null"]}: the lightweight position
// check names one JSON kind and refuses every other, and answering "object"
// for it refused the null.
//
// Every inferred position carries a non-array instance beside it. `items` says
// nothing about a string, an object, a number or a null, so all four stay valid
// at every one of them -- which is what a fix reaching for the element type too
// eagerly would take away.
//
// Verdicts from Bowtie over python-jsonschema, go-jsonschema and js-ajv,
// unanimous on all 109 documents listed. One further document was tried and is
// deliberately not listed, because the three implementations disagree about it:
// {"containsEvaluates":[{"a":1},{"b":2}]}, valid to js-ajv and invalid to the
// other two. Nothing here asserts a verdict on it.
func TestInferredArrayJudgesItsElementByTheWholeSubSchema(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/inferred_array_element_positions.json",
		[]string{
			`{}`,
			// The reported position, with the four non-array instances it must
			// go on accepting.
			`{"prop":[{"a":1}]}`,
			`{"prop":"str"}`,
			`{"prop":{"k":1}}`,
			`{"prop":7}`,
			`{"prop":null}`,
			`{"prop":[]}`,
			`{"propRef":[{"a":1}]}`,
			`{"propRef":"str"}`,
			// The declared spelling, unchanged.
			`{"declared":[{"a":1}]}`,
			`{"declared":[]}`,
			// The nesting positions.
			`{"inDeclaredArray":[[{"a":1}]]}`,
			`{"inDeclaredArray":["str"]}`,
			`{"inInferredArray":[[{"a":1}]]}`,
			`{"inInferredArray":"str"}`,
			`{"inInferredArray":["str"]}`,
			`{"mapValue":{"k":[{"a":1}]}}`,
			`{"mapValue":{"k":"str"}}`,
			`{"slotOfDeclared":[[{"a":1}]]}`,
			`{"slotOfDeclared":["str"]}`,
			`{"slotOfDeclared":[]}`,
			`{"viaRef":[{"a":1}]}`,
			`{"viaRef":"str"}`,
			// The composition branches. The four non-array rows under
			// allOfBranch are the false rejection.
			`{"allOfBranch":[{"a":1}]}`,
			`{"allOfBranch":"str"}`,
			`{"allOfBranch":{"k":1}}`,
			`{"allOfBranch":7}`,
			`{"allOfBranch":null}`,
			// Its control: a second branch declares the type, so the merge is
			// no longer guessing and the plain slice is right.
			`{"allOfDeclaringBranch":[{"a":1}]}`,
			`{"allOfDeclaringBranch":[]}`,
			`{"anyOfBranch":[{"a":1}]}`,
			`{"anyOfBranch":"str"}`,
			`{"oneOfBranch":[{"a":1}]}`,
			`{"oneOfBranch":"str"}`,
			// The keywords beside `items`.
			`{"ownSlot":[{"a":1}]}`,
			`{"ownSlot":"str"}`,
			`{"ownSlot":[]}`,
			`{"ownTail":[true,{"a":1}]}`,
			`{"ownTail":"str"}`,
			`{"ownTail":[true]}`,
			`{"containsShape":[{"a":1}]}`,
			`{"containsShape":"str"}`,
			`{"declaredContains":[{"a":1}]}`,
			// unevaluatedItems reads `contains` to learn which items it
			// accounted for, so the delegation has to answer that reader too.
			// Not listed: {"containsEvaluates":[{"a":1},{"b":2}]}, on which the
			// oracle splits -- js-ajv calls it valid, python-jsonschema and
			// go-jsonschema invalid -- so nothing here claims a verdict for it.
			`{"containsEvaluates":[{"a":1}]}`,
			`{"containsEvaluates":[{"a":1},{"a":2}]}`,
			`{"containsEvaluates":"str"}`,
			// Element sub-schemas the reduction could only half express.
			`{"elemMinLength":["abcd"]}`,
			`{"elemMinLength":[7]}`,
			`{"elemMinLength":[null]}`,
			`{"elemMinLength":"ab"}`,
			`{"elemPattern":["aa"]}`,
			`{"elemPattern":"b"}`,
			`{"elemEnum":["x"]}`,
			`{"elemEnum":"z"}`,
			`{"elemNot":[7]}`,
			`{"elemNot":"s"}`,
			`{"elemUnion":["s"]}`,
			`{"elemUnion":[7]}`,
			`{"elemUnion":true}`,
			// The nullable union, at the element and at the slot. [null] is the
			// false rejection.
			`{"elemNullableObject":[null]}`,
			`{"elemNullableObject":[{}]}`,
			`{"elemNullableObject":"str"}`,
			`{"nullableSlotOfDeclared":[null]}`,
			`{"nullableSlotOfDeclared":[{}]}`,
			`{"nullableSlotOfDeclared":[]}`,
			// What the reduction never touched, and must not lose.
			`{"keptBounds":[{},{}]}`,
			`{"keptBounds":"str"}`,
			`{"keptUnique":[{"a":1},{"a":2}]}`,
			`{"keptUnique":"str"}`,
			`{"elemTrue":[1,"s",null]}`,
			`{"elemFalse":[]}`,
			`{"elemFalse":"str"}`,
			`{"selfRef":["s"]}`,
			`{"selfRef":"s"}`,
		},
		[]string{
			`{"prop":[{}]}`,
			`{"propRef":[{}]}`,
			`{"declared":[{}]}`,
			`{"inDeclaredArray":[[{}]]}`,
			`{"inInferredArray":[[{}]]}`,
			`{"mapValue":{"k":[{}]}}`,
			`{"slotOfDeclared":[[{}]]}`,
			`{"viaRef":[{}]}`,
			`{"allOfBranch":[{}]}`,
			`{"allOfDeclaringBranch":[{}]}`,
			`{"allOfDeclaringBranch":"str"}`,
			`{"allOfDeclaringBranch":7}`,
			`{"allOfDeclaringBranch":null}`,
			`{"anyOfBranch":[{}]}`,
			`{"oneOfBranch":[{}]}`,
			`{"ownSlot":[{}]}`,
			`{"ownTail":[true,{}]}`,
			// An array whose only element does not match the contains
			// sub-schema contains no matching element, and neither does an
			// empty one.
			`{"containsShape":[{}]}`,
			`{"containsShape":[]}`,
			`{"declaredContains":[{}]}`,
			`{"containsEvaluates":[]}`,
			`{"containsEvaluates":[{}]}`,
			`{"elemMinLength":["ab"]}`,
			`{"elemPattern":["b"]}`,
			`{"elemPattern":[7]}`,
			`{"elemEnum":["z"]}`,
			`{"elemNot":["s"]}`,
			`{"elemUnion":[true]}`,
			`{"elemNullableObject":["s"]}`,
			`{"nullableSlotOfDeclared":["s"]}`,
			`{"keptBounds":[{}]}`,
			`{"keptUnique":[{"a":1},{"a":1}]}`,
			`{"elemFalse":[1]}`,
			// The recursion terminates on the data: the innermost array is the
			// one that fails minItems.
			`{"selfRef":[[[]]]}`,
			`{"selfRef":[[]]}`,
		},
	)
}

// TestInferredArrayAtTheDocumentRootJudgesItsElement is issue #166 at the one
// position a document has only one of.
//
// The whole schema is `items`, so the root type is the wrapper that accepts any
// JSON value; the five non-array documents are what says the element check did
// not turn into a type the decoder enforces.
func TestInferredArrayAtTheDocumentRootJudgesItsElement(t *testing.T) {
	runValidationCasesForType(t,
		"testdata/schemas/regression/inferred_array_root.json", "InferredArrayRoot",
		[]string{
			`[{"a":1}]`,
			`[]`,
			`"str"`,
			`{"k":1}`,
			`7`,
			`true`,
			`null`,
		},
		[]string{
			`[{}]`,
			`[{"a":1},{}]`,
		},
	)
}

// TestInferredArrayFromAnAllOfBranchIsStillInferred is the composition row at
// the root, and it is separate because the null only tells the two readings
// apart there.
//
// The merge types this schema "array" off the branch's `items` so the merged
// schema can be typed at all. Read as a declaration, the Go type became a plain
// slice: the string, the object and the number died in the decoder and the null
// died in the type's own null check, though the schema permits all four. Under
// a *property* the null never reaches that check -- the property is a pointer
// and the struct answers for it -- so a fixture that only had the property
// position left the null rule unpinned.
//
// Verdicts from Bowtie over python-jsonschema, go-jsonschema and js-ajv,
// unanimous on all nine documents.
func TestInferredArrayFromAnAllOfBranchIsStillInferred(t *testing.T) {
	runValidationCasesForType(t,
		"testdata/schemas/regression/inferred_array_allof_branch_root.json", "InferredArrayAllOfBranchRoot",
		[]string{
			`[{"a":1}]`,
			`[]`,
			`"str"`,
			`{"k":1}`,
			`7`,
			`true`,
			`null`,
		},
		[]string{
			`[{}]`,
			`[{"a":1},{}]`,
		},
	)
}

// TestInferredArrayTupleUnderDraft7JudgesEachPosition is the same defect in the
// tuple spelling drafts 4 to 7 use: `items` as an array of schemas, with
// `additionalItems` governing everything past it.
//
// Neither keyword is reachable from a 2020-12 document, so the dialect is the
// point: a fix pinned only by prefixItems cases would leave both of these.
func TestInferredArrayTupleUnderDraft7JudgesEachPosition(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/inferred_array_tuple_draft7.json",
		[]string{
			`{}`,
			`{"tup":[{"a":1}]}`,
			`{"tup":[{"a":1},{"b":1}]}`,
			`{"tup":"str"}`,
			`{"tup":[]}`,
			`{"one":[{"a":1}]}`,
			`{"one":"str"}`,
			`{"ref":[{"a":1}]}`,
			`{"ref":"str"}`,
		},
		[]string{
			`{"tup":[{}]}`,
			`{"tup":[{"a":1},{}]}`,
			`{"one":[{}]}`,
			`{"ref":[{}]}`,
		},
	)
}

// TestRecursiveAnchorNestedResources pins the case that decides whether
// resolveRecursiveRef's scope walk matters.
//
// Two resources both carry $recursiveAnchor: true, and the inner one's
// $recursiveRef: "#" is bookended by both. 2019-09 resolves such a reference to
// the *outermost* anchored resource in the dynamic scope, so the value under
// "v" is judged by the outer schema -- it must carry "o", not "i".
//
// The verdicts are not this project's reasoning. They were taken from
// python-jsonschema, go-jsonschema and js-ajv through Bowtie, which agree
// unanimously: the "o" document is valid and the "i" document is not. The suite
// has no case of this shape, which is why it is written out here.
//
// What it does NOT guard is the *direction* resolveRecursiveRef's scope walk
// takes, and that was established rather than assumed. The walk runs here and
// decides the answer -- it returns the outer resource where the plain $ref
// reading would have returned the inner one -- but it decides it from a scope
// exactly one frame deep, because a resource written directly under the root's
// $defs is generated before the root and so has nothing pushed above it. One
// frame makes innermost-first and outermost-first the same walk, and planting
// either direction leaves this test passing.
//
// TestRecursiveAnchorResolvesOutermost is the fixture that does guard the
// direction, and it is the same rule at a scope two frames deep. Both are kept:
// this one holds the shape a reader reaches for first, and the two differ only
// by where the inner resource is written, which is the whole reason the
// direction went unguarded for so long. Issue #167.
func TestRecursiveAnchorNestedResources(t *testing.T) {
	// ForType, not runValidationCases: extractRootTypeName picks the *last*
	// struct carrying JSON tags, which here is the inner resource, and the
	// document would then be judged by the schema this test exists to show is
	// not the one that applies.
	runValidationCasesForType(t,
		"testdata/schemas/regression/recursive_anchor_nested_resources.json",
		"Root",
		[]string{
			// The inner $recursiveRef resolves outward, so "v" is an outer object.
			`{"o":1,"inner":{"i":1,"v":{"o":9}}}`,
			// No "inner" at all: nothing reaches the reference.
			`{"o":1}`,
			// "v" absent: the reference is never applied.
			`{"o":1,"inner":{"i":1}}`,
		},
		[]string{
			// "v" satisfies the *inner* schema, which is what an innermost-first
			// walk would accept and what all three implementations reject.
			`{"o":1,"inner":{"i":1,"v":{"i":9}}}`,
			// The outer schema's own requirement still binds.
			`{"inner":{"i":1,"v":{"o":9}}}`,
			// And the inner schema's does.
			`{"o":1,"inner":{"v":{"o":9}}}`,
		},
	)
}

// TestRecursiveAnchorResolvesOutermost is the fixture that watches which
// direction resolveRecursiveRef walks the dynamic scope, and it is the one the
// tree did not have.
//
// 2019-09 section 8.2.4.2.1 resolves a bookended $recursiveRef to the
// *outermost* resource in the dynamic scope whose $recursiveAnchor is true.
// resolveRecursiveRef walked innermost-first, and nothing failed when the
// direction was flipped either way, because every case that reached the walk
// reached it with a single frame on the scope, where the two directions are the
// same walk. This schema puts two anchored frames there at once, so they name
// different resources and disagree about every document below.
//
// The shape is doing three things at once and none of them are decoration:
//
//   - The root is a resource ($id) and is anchored, so it is frame 0 and a
//     candidate.
//   - $defs/mid carries neither $id nor $recursiveAnchor. It is therefore not a
//     schema resource: it puts no frame on any validator's dynamic scope, and no
//     anchor declaration into the reach that would route the types below it to
//     the runtime evaluator (#160, dynamicScopeDecidesTheTarget). Anchoring it,
//     or giving it an $id, moves the schema off the static path this test exists
//     to cover.
//   - The anchored resource lives under *mid's* $defs rather than the root's. A
//     root-level $defs entry is generated before the root type, with nothing
//     pushed above it -- which is why recursive_anchor_nested_resources.json
//     resolves at depth 1 and cannot see the direction. One nested a level down
//     is reached only through the $ref that pushes a frame for it.
//
// So the scope when the $recursiveRef is resolved is [outer, deep], both
// anchored. Outermost-first types "v" as the outer resource; innermost-first
// types it as deep. The two accept disjoint documents, and planting the old
// direction back turns both lists below inside out.
//
// Deep and Mid, not Root, are what the cases are run against, and that is the
// point rather than a workaround. Root itself has two anchored resources in
// reach, so it is compiled to the runtime evaluator, whose own walk is
// outermost-first already; the whole-document verdict never asked this function
// anything. What the static path decides is the two named types beside it, and a
// caller holding one of those and calling Validate is who this was wrong for.
//
// The verdicts are not this project's reasoning. python-jsonschema 4.26.0,
// go-jsonschema (santhosh-tekuri v6.0.2) and js-ajv (ajv 8.20.0) were asked
// through Bowtie for the whole document on this exact schema and agree
// unanimously: with "v" set to {"o":9} the document is valid, with {"d":9} it is
// not, and {} is not. No implementation dissented, so there is no split to
// record. The cases below are those documents with the outer levels stripped to
// the type being validated, so a Deep that accepts {"d":1,"v":{"d":9}} is
// accepting a value that the document it was cut from is rejected for.
func TestRecursiveAnchorResolvesOutermost(t *testing.T) {
	const fixture = "testdata/schemas/regression/recursive_anchor_outermost_scope.json"

	// The resource the reference is written in. "v" is typed by the walk, so
	// this is the narrowest place the direction is visible.
	runValidationCasesForType(t, fixture, "Deep",
		[]string{
			// "v" satisfies the outer resource -- the outermost anchored frame,
			// and so the one the reference resolves to.
			`{"d":1,"v":{"o":9}}`,
			// Satisfying both is still satisfying the outer one.
			`{"d":1,"v":{"o":9,"d":9}}`,
			// "v" absent: the reference is never applied.
			`{"d":1}`,
		},
		[]string{
			// Satisfies the *inner* resource and not the outer one: what
			// innermost-first accepts, and what all three implementations reject.
			`{"d":1,"v":{"d":9}}`,
			// Neither.
			`{"d":1,"v":{}}`,
			// The inner resource's own requirement still binds.
			`{"v":{"o":9}}`,
		},
	)

	// And through the $ref that pushes the second frame, which is what made the
	// scope two deep in the first place.
	runValidationCasesForType(t, fixture, "Mid",
		[]string{
			`{"m":1,"deep":{"d":1,"v":{"o":9}}}`,
			`{"m":1,"deep":{"d":1}}`,
			`{"m":1}`,
		},
		[]string{
			`{"m":1,"deep":{"d":1,"v":{"d":9}}}`,
			`{"m":1,"deep":{"d":1,"v":{}}}`,
			`{"deep":{"d":1,"v":{"o":9}}}`,
			`{"m":1,"deep":{"v":{"o":9}}}`,
		},
	)
}

// TestArrayKeywordsSurviveEveryContainerPosition covers one array sub-schema --
// {"type":"array","items":{"type":"string"},"uniqueItems":true,"minItems":2} --
// written in every container position a sub-schema can sit in, plus the
// `contains` family beside it.
//
// Five positions kept minItems and dropped uniqueItems, and three kept nothing
// of `contains` at all. That asymmetry is the whole point of the fixture: the
// minItems case in each position is listed here too, so a repair that moved the
// check rather than adding one still fails. Issues #179, #182 and #183.
//
// The controls -- a property, a $ref and a patternProperties value -- are what
// keeps a widened element rule from being mistaken for a fix. All three already
// carried both keywords, and all three must go on carrying them.
func TestArrayKeywordsSurviveEveryContainerPosition(t *testing.T) {
	runValidationCases(t,
		"testdata/schemas/regression/array_keywords_in_container_positions.json",
		[]string{
			`{}`,
			// A conforming array, position by position.
			`{"prop":["a","b"]}`,
			`{"ref":["a","b"]}`,
			`{"patternValue":{"pk":["a","b"]}}`,
			`{"elem":[["a","b"]]}`,
			`{"mapValue":{"k":["a","b"]}}`,
			`{"deep":[[["a","b"]]]}`,
			`{"tupleSlot":[["a","b"]]}`,
			`{"tupleTail":[true,["a","b"]]}`,
			`{"containsArray":[[1,2]]}`,
			`{"elemContains":[[11]]}`,
			`{"elemContainsObject":[[{"a":1}]]}`,
			`{"elemContainsArray":[[[1,2]]]}`,
			`{"mapContainsObject":{"k":[{"a":1}]}}`,
			`{"elemMinContains":[[1,2]]}`,
			`{"elemMaxContains":[[1]]}`,
			// Empty containers, and a key no pattern claims: adding a check to a
			// position must not make the position itself mandatory.
			`{"elem":[]}`, `{"deep":[]}`, `{"mapValue":{}}`, `{"patternValue":{}}`,
			`{"tupleSlot":[]}`, `{"tupleTail":[]}`, `{"elemContains":[]}`,
			`{"patternValue":{"zz":["a","a"]}}`,
		},
		[]string{
			// uniqueItems. The five positions this test is about come first.
			`{"elem":[["a","a"]]}`,
			`{"mapValue":{"k":["a","a"]}}`,
			`{"deep":[[["a","a"]]]}`,
			`{"tupleSlot":[["a","a"]]}`,
			`{"tupleTail":[true,["a","a"]]}`,
			// And the three that already worked, so a change that moved the
			// check rather than adding one is still caught.
			`{"prop":["a","a"]}`,
			`{"ref":["a","a"]}`,
			`{"patternValue":{"pk":["a","a"]}}`,
			// minItems in the identical slots: the sibling keyword that fired
			// throughout, and whose firing is what made each drop a defect
			// rather than a position nobody had wired up.
			`{"elem":[["a"]]}`,
			`{"mapValue":{"k":["a"]}}`,
			`{"deep":[[["a"]]]}`,
			`{"tupleSlot":[["a"]]}`,
			`{"tupleTail":[true,["a"]]}`,
			`{"prop":["a"]}`,
			`{"ref":["a"]}`,
			`{"patternValue":{"pk":["a"]}}`,
			// contains, and the cardinality bounds beside it.
			`{"containsArray":[[1]]}`,
			`{"containsArray":[]}`,
			`{"elemContains":[[1]]}`,
			`{"elemContains":[[]]}`,
			`{"elemContainsObject":[[{}]]}`,
			`{"elemContainsArray":[[[1]]]}`,
			`{"mapContainsObject":{"k":[{}]}}`,
			`{"elemMinContains":[[1]]}`,
			`{"elemMaxContains":[[1,2]]}`,
		},
	)
}

// TestUnusedDefinitionsDoNotDisableUnevaluatedItems is issue #178 end to end.
//
// The two keyword allow-lists in pkg/generator/annotations.go each carried a
// hand-written copy of "the keywords that carry no constraint", and the copies
// disagreed. A keyword outside an allow-list refuses the whole schema, so an
// entirely unused $defs -- which almost every real document carries -- sent this
// schema back to the static path, and the static path cannot enforce
// unevaluatedItems next to an in-place applicator: ["a", 1] was accepted.
//
// Compiling and running the generated code is the point. The generator can be
// read to say it now compiles the schema; only the program says what it admits.
func TestUnusedDefinitionsDoNotDisableUnevaluatedItems(t *testing.T) {
	runValidationCasesForType(t,
		"testdata/schemas/regression/unused_defs_unevaluated_items.json",
		"UnusedDefsUnevaluatedItems",
		[]string{
			`["a"]`,
			`[]`,
		},
		[]string{
			// One item past the tuple, and unevaluatedItems is false.
			`["a",1]`,
			`["a","b"]`,
			// The allOf branch still binds on its own terms.
			`[1]`,
			`{"a":1}`,
		},
	)
}

// ---------- issue #253: --format-assertion rewrites the caller's bytes ----------

// formatCanonCase is one document and what comes back out of it. want is
// compared byte for byte: the whole point is that the bytes differ while the
// value does not, so a comparison through `any` -- which every other round-trip
// helper here does -- cannot see it.
type formatCanonCase struct {
	in   string
	want string
}

// The README states that asserting `format` changes the representation as well
// as the verdict, and names which formats it happens to. That claim is only
// worth what it is measured against: the types involved are stdlib, their
// canonical spellings are theirs to change, and a table written from the type's
// documentation rather than from its output is a table that can be wrong on the
// day it is written.
//
// So both halves are asserted from the generated code: every rewrite the README
// tabulates, and every format it says is left alone.
func TestFormatAssertionCanonicalisesExactlyTheFormatsTheREADMENames(t *testing.T) {
	rewritten := []formatCanonCase{
		// date-time -> time.Time. A zero fractional second disappears, a
		// trailing zero inside one is trimmed, and a zero offset becomes Z.
		{`{"dt":"2020-01-02T03:04:05.000Z"}`, `{"dt":"2020-01-02T03:04:05Z"}`},
		{`{"dt":"2020-01-02T03:04:05.500+02:00"}`, `{"dt":"2020-01-02T03:04:05.5+02:00"}`},
		{`{"dt":"2020-01-02T03:04:05+00:00"}`, `{"dt":"2020-01-02T03:04:05Z"}`},
		{`{"dt":"2020-01-02T03:04:05-00:00"}`, `{"dt":"2020-01-02T03:04:05Z"}`},
		// ipv6 -> netip.Addr, written in the RFC 5952 form.
		{`{"v6":"2001:0db8:0000:0000:0000:0000:0000:0001"}`, `{"v6":"2001:db8::1"}`},
		{`{"v6":"2001:DB8::1"}`, `{"v6":"2001:db8::1"}`},
		{`{"v6":"::ffff:c0a8:1"}`, `{"v6":"::ffff:192.168.0.1"}`},
	}
	// The controls. A value already in canonical form is not moved; ipv4 has
	// one spelling and so cannot be; the string-typed formats keep their bytes;
	// and a `format` beside minLength keeps the string, which is the escape
	// route the README offers.
	unchanged := []formatCanonCase{
		{`{"dt":"2020-01-02T03:04:05Z"}`, `{"dt":"2020-01-02T03:04:05Z"}`},
		{`{"dt":"2020-01-02T03:04:05.123456789Z"}`, `{"dt":"2020-01-02T03:04:05.123456789Z"}`},
		{`{"v6":"2001:db8::1"}`, `{"v6":"2001:db8::1"}`},
		{`{"v6":"::ffff:192.168.0.1"}`, `{"v6":"::ffff:192.168.0.1"}`},
		{`{"v4":"192.168.0.1"}`, `{"v4":"192.168.0.1"}`},
		{`{"d":"2020-01-02"}`, `{"d":"2020-01-02"}`},
		{`{"tm":"03:04:05.000Z"}`, `{"tm":"03:04:05.000Z"}`},
		{`{"uid":"C73BCDCC-2669-4BF6-81D3-E4AE73FB11FD"}`, `{"uid":"C73BCDCC-2669-4BF6-81D3-E4AE73FB11FD"}`},
		{`{"dur":"P1DT2H"}`, `{"dur":"P1DT2H"}`},
		{`{"bounded":"2020-01-02T03:04:05.000Z"}`, `{"bounded":"2020-01-02T03:04:05.000Z"}`},
	}

	t.Run("asserting", func(t *testing.T) {
		runGeneratedMainProgramWithConfig(t,
			"testdata/schemas/regression/format_assertion_canonicalises.json",
			"format_canon_asserting",
			formatCanonProgram(append(append([]formatCanonCase{}, rewritten...), unchanged...)),
			formatAssertingConfig())
	})

	// The same documents under the dialect's own answer, where `format` is an
	// annotation and every value stays a string. Every case round-trips to
	// itself, including the seven the flag rewrites -- which is what makes this
	// a property of the posture rather than of the fixture.
	t.Run("annotating", func(t *testing.T) {
		var same []formatCanonCase
		for _, c := range append(append([]formatCanonCase{}, rewritten...), unchanged...) {
			same = append(same, formatCanonCase{in: c.in, want: c.in})
		}
		runGeneratedMainProgramWithConfig(t,
			"testdata/schemas/regression/format_assertion_canonicalises.json",
			"format_canon_annotating",
			formatCanonProgram(same),
			generator.Config{PackageName: "testpkg", OmitEmpty: true})
	})
}

// formatCanonProgram renders a program that decodes and re-encodes each
// document and compares the result to want as bytes.
func formatCanonProgram(cases []formatCanonCase) string {
	var rows strings.Builder
	for _, c := range cases {
		fmt.Fprintf(&rows, "\t\t{%q, %q},\n", c.in, c.want)
	}
	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	cases := []struct{ in, want string }{
%s	}
	failed := false
	for _, c := range cases {
		var obj FormatCanon
		if err := json.Unmarshal([]byte(c.in), &obj); err != nil {
			fmt.Fprintf(os.Stderr, "unmarshal %%s: %%v\n", c.in, err)
			failed = true
			continue
		}
		if err := obj.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "validate %%s: %%v\n", c.in, err)
			failed = true
			continue
		}
		out, err := json.Marshal(obj)
		if err != nil {
			fmt.Fprintf(os.Stderr, "marshal %%s: %%v\n", c.in, err)
			failed = true
			continue
		}
		if string(out) != c.want {
			fmt.Fprintf(os.Stderr, "BYTES\n  in:   %%s\n  out:  %%s\n  want: %%s\n", c.in, string(out), c.want)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`, rows.String())
}
