package schemagen

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

func TestDeriveOutputFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"person.json", "person.go"},
		{"my-schema.json", "my_schema.go"},
		{"my-schema.yaml", "my_schema.go"},
		{"CamelCase.json", "CamelCase.go"},
		{"path/to/schema.json", "schema.go"},
		{"no_ext", "no_ext.go"},
		{"dots.in.name.json", "dots.in.name.go"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := deriveOutputFilename(tt.input)
			if got != tt.want {
				t.Errorf("deriveOutputFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRootCommandHelp(t *testing.T) {
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Generate Go types from JSON Schema files") {
		t.Errorf("help output missing root description, got:\n%s", output)
	}
	if !strings.Contains(output, "generate") {
		t.Errorf("help output missing generate subcommand, got:\n%s", output)
	}
}

func TestGenerateCommandHelp(t *testing.T) {
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--help"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	// Check all flags are documented
	for _, flag := range []string{
		"--output-dir", "--package", "--omit-empty",
		"--strict-properties", "--big-int", "--allow-remote-refs", "--verbose",
	} {
		if !strings.Contains(output, flag) {
			t.Errorf("help output missing flag %s, got:\n%s", flag, output)
		}
	}
	// Check short flags
	for _, flag := range []string{"-o", "-p", "-v"} {
		if !strings.Contains(output, flag) {
			t.Errorf("help output missing short flag %s, got:\n%s", flag, output)
		}
	}
}

func TestGenerateRequiresArgs(t *testing.T) {
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no args provided, got nil")
	}
	if !strings.Contains(err.Error(), "requires at least 1 arg") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestGenerateNonExistentSchema(t *testing.T) {
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	tmpDir := t.TempDir()
	cmd.SetArgs([]string{"generate", "--output-dir", tmpDir, "nonexistent.json"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent schema file")
	}
	if !strings.Contains(err.Error(), "loading") {
		t.Errorf("expected loading error, got: %v", err)
	}
}

func TestGenerateInvalidSchema(t *testing.T) {
	tmpDir := t.TempDir()
	// Write invalid JSON
	badSchema := filepath.Join(tmpDir, "bad.json")
	if err := os.WriteFile(badSchema, []byte(`{not valid json`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--output-dir", tmpDir, badSchema})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid schema")
	}
	if !strings.Contains(err.Error(), "loading") {
		t.Errorf("expected loading error, got: %v", err)
	}
}

func TestGenerateSimpleSchema(t *testing.T) {
	// Find the testdata schema relative to this file
	schemaPath := findTestdataSchema(t, "basic/simple_object.json")

	tmpDir := t.TempDir()
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--output-dir", tmpDir, "--package", "testpkg", schemaPath})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check output file was created
	outPath := filepath.Join(tmpDir, "simple_object.go")
	content, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("expected output file %s to exist: %v", outPath, err)
	}

	src := string(content)
	if !strings.Contains(src, "package testpkg") {
		t.Error("generated code missing package declaration")
	}
	if !strings.Contains(src, "Person") {
		t.Error("generated code missing Person type")
	}
}

func TestGenerateWithFieldMap(t *testing.T) {
	schemaPath := findTestdataSchema(t, "basic/simple_object.json")

	tmpDir := t.TempDir()
	mapPath := filepath.Join(tmpDir, "names.json")
	mapContent := `{
		"simple_object.json": {
			"Person": {"name": "FullName"}
		}
	}`
	if err := os.WriteFile(mapPath, []byte(mapContent), 0o644); err != nil {
		t.Fatalf("write field map: %v", err)
	}

	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--output-dir", tmpDir, "--field-map", mapPath, schemaPath})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "simple_object.go"))
	if err != nil {
		t.Fatalf("expected output file: %v", err)
	}
	src := string(content)
	// Field renamed, but JSON tag preserved.
	if !strings.Contains(src, "FullName ") {
		t.Error("generated code missing renamed FullName field")
	}
	if !strings.Contains(src, `json:"name`) {
		t.Error("generated code should preserve the original JSON tag \"name\"")
	}
	if strings.Contains(buf.String(), "warning:") {
		t.Errorf("did not expect a warning, got: %s", buf.String())
	}
}

func TestGenerateFieldMapUnusedEntryWarns(t *testing.T) {
	schemaPath := findTestdataSchema(t, "basic/simple_object.json")

	tmpDir := t.TempDir()
	mapPath := filepath.Join(tmpDir, "names.json")
	// "Persn" is a typo and matches no type.
	mapContent := `{"simple_object.json": {"Persn": {"name": "FullName"}}}`
	if err := os.WriteFile(mapPath, []byte(mapContent), 0o644); err != nil {
		t.Fatalf("write field map: %v", err)
	}

	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--output-dir", tmpDir, "--field-map", mapPath, schemaPath})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "warning:") || !strings.Contains(out, "simple_object.json/Persn.name") {
		t.Errorf("expected unused-entry warning, got: %s", out)
	}
}

func TestGenerateFieldMapUnknownFileKeyWarns(t *testing.T) {
	schemaPath := findTestdataSchema(t, "basic/simple_object.json")

	tmpDir := t.TempDir()
	mapPath := filepath.Join(tmpDir, "names.json")
	// Top-level key "Person" is a type name, not a schema file base name (a
	// common mistake), so it names no generated file and the section is dead.
	mapContent := `{"Person": {"Person": {"name": "FullName"}}}`
	if err := os.WriteFile(mapPath, []byte(mapContent), 0o644); err != nil {
		t.Fatalf("write field map: %v", err)
	}

	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--output-dir", tmpDir, "--field-map", mapPath, schemaPath})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "does not match any generated schema file") || !strings.Contains(out, `"Person"`) {
		t.Errorf("expected unknown-file-key warning, got: %s", out)
	}
}

func TestGenerateVerboseOutput(t *testing.T) {
	schemaPath := findTestdataSchema(t, "basic/simple_object.json")

	tmpDir := t.TempDir()
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--output-dir", tmpDir, "--verbose", schemaPath})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Processing") {
		t.Errorf("verbose output missing 'Processing', got:\n%s", output)
	}
	if !strings.Contains(output, "->") {
		t.Errorf("verbose output missing '->' output path, got:\n%s", output)
	}
}

func TestGenerateMultipleSchemas(t *testing.T) {
	schema1 := findTestdataSchema(t, "basic/simple_object.json")
	schema2 := findTestdataSchema(t, "basic/array_types.json")

	tmpDir := t.TempDir()
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--output-dir", tmpDir, schema1, schema2})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both files should exist
	for _, name := range []string{"simple_object.go", "array_types.go"} {
		outPath := filepath.Join(tmpDir, name)
		if _, err := os.Stat(outPath); os.IsNotExist(err) {
			t.Errorf("expected output file %s to exist", outPath)
		}
	}
}

func TestGenerateDefaultOutputDir(t *testing.T) {
	schemaPath := findTestdataSchema(t, "basic/simple_object.json")

	// Use a temp dir as working directory so we don't pollute the project
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	// No --output-dir flag, defaults to "."
	cmd.SetArgs([]string{"generate", schemaPath})

	err = cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check output file was created in current directory (tmpDir)
	outPath := filepath.Join(tmpDir, "simple_object.go")
	if _, err := os.Stat(outPath); os.IsNotExist(err) {
		t.Errorf("expected output file %s in default output dir", outPath)
	}
}

func TestGenerateStrictProperties(t *testing.T) {
	schemaPath := findTestdataSchema(t, "basic/simple_object.json")

	tmpDir := t.TempDir()
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{
		"generate",
		"--output-dir", tmpDir,
		"--strict-properties",
		schemaPath,
	})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With strict-properties, there should be no AdditionalProperties map
	outPath := filepath.Join(tmpDir, "simple_object.go")
	content, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}

	src := string(content)
	// With strict-properties, AdditionalProperties is still generated (for round-trip
	// fidelity) but Validate() should reject them. Verify the overflow map exists
	// and that the validation rejects unknown keys.
	if !strings.Contains(src, "AdditionalProperties") {
		t.Error("strict-properties should still generate AdditionalProperties for round-trip capture")
	}
	if !strings.Contains(src, "additional property") {
		t.Error("strict-properties should make Validate() reject additional properties")
	}
}

func TestGenerateOmitEmptyDisabled(t *testing.T) {
	schemaPath := findTestdataSchema(t, "basic/simple_object.json")

	tmpDir := t.TempDir()
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{
		"generate",
		"--output-dir", tmpDir,
		"--omit-empty=false",
		schemaPath,
	})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	outPath := filepath.Join(tmpDir, "simple_object.go")
	content, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}

	src := string(content)
	// Optional fields (age, active) should NOT have omitempty when disabled
	// Required fields (name, email) never have omitempty regardless
	if strings.Contains(src, "omitempty") {
		t.Error("expected no omitempty tags when --omit-empty=false")
	}
}

func TestGenerateCreatesOutputDir(t *testing.T) {
	schemaPath := findTestdataSchema(t, "basic/simple_object.json")

	tmpDir := t.TempDir()
	deepDir := filepath.Join(tmpDir, "a", "b", "c")

	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--output-dir", deepDir, schemaPath})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	outPath := filepath.Join(deepDir, "simple_object.go")
	if _, err := os.Stat(outPath); os.IsNotExist(err) {
		t.Errorf("expected output file in nested dir %s", outPath)
	}
}

func TestGenerateWithRefs(t *testing.T) {
	schemaPath := findTestdataSchema(t, "refs/defs_ref.json")

	tmpDir := t.TempDir()
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--output-dir", tmpDir, "--package", "refs", schemaPath})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	outPath := filepath.Join(tmpDir, "defs_ref.go")
	content, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("expected output file: %v", err)
	}

	if !strings.Contains(string(content), "package refs") {
		t.Error("generated code missing correct package declaration")
	}
}

func TestGenerateEnumSchema(t *testing.T) {
	schemaPath := findTestdataSchema(t, "enum/string_enum.json")

	tmpDir := t.TempDir()
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--output-dir", tmpDir, schemaPath})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	outPath := filepath.Join(tmpDir, "string_enum.go")
	if _, err := os.Stat(outPath); os.IsNotExist(err) {
		t.Errorf("expected output file %s", outPath)
	}
}

func TestGenerateHyphenatedFilename(t *testing.T) {
	// Create a schema with a hyphenated name
	tmpDir := t.TempDir()
	schemaPath := filepath.Join(tmpDir, "my-awesome-schema.json")
	schemaContent := `{
		"type": "object",
		"title": "MyAwesome",
		"properties": {
			"value": {"type": "string"}
		}
	}`
	if err := os.WriteFile(schemaPath, []byte(schemaContent), 0o644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "out")
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--output-dir", outDir, schemaPath})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Hyphens should be replaced with underscores
	outPath := filepath.Join(outDir, "my_awesome_schema.go")
	if _, err := os.Stat(outPath); os.IsNotExist(err) {
		t.Errorf("expected hyphenated schema to produce %s", outPath)
	}
}

// findTestdataSchema locates a testdata schema file relative to the project root.
func findTestdataSchema(t *testing.T, relPath string) string {
	t.Helper()

	// Walk up from the test file's directory to find the project root
	// (identified by go.mod).
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			schemaPath := filepath.Join(dir, "testdata", "schemas", relPath)
			if _, err := os.Stat(schemaPath); err == nil {
				return schemaPath
			}
			t.Fatalf("schema file not found: %s", schemaPath)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (go.mod)")
		}
		dir = parent
	}
}

func TestParseDraft(t *testing.T) {
	tests := []struct {
		input   string
		want    schema.Draft
		wantErr bool
	}{
		{"3", schema.Draft03, false},
		{"03", schema.Draft03, false},
		{"draft-03", schema.Draft03, false},
		{"4", schema.Draft04, false},
		{"04", schema.Draft04, false},
		{"draft-04", schema.Draft04, false},
		{"6", schema.Draft06, false},
		{"06", schema.Draft06, false},
		{"7", schema.Draft07, false},
		{"07", schema.Draft07, false},
		{"draft-07", schema.Draft07, false},
		{"2019-09", schema.Draft201909, false},
		{"draft-2019-09", schema.Draft201909, false},
		{"2019", schema.Draft201909, false},
		{"2020-12", schema.Draft202012, false},
		{"draft-2020-12", schema.Draft202012, false},
		{"2020", schema.Draft202012, false},
		{"invalid", schema.DraftUnknown, true},
		{"5", schema.DraftUnknown, true},
		{"", schema.DraftUnknown, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseDraft(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseDraft(%q) expected error, got %v", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("parseDraft(%q) unexpected error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("parseDraft(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestGenerateDraftFlag(t *testing.T) {
	schemaPath := findTestdataSchema(t, "basic/simple_object.json")

	tmpDir := t.TempDir()
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--output-dir", tmpDir, "--draft", "2020-12", schemaPath})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify file was generated
	outPath := filepath.Join(tmpDir, "simple_object.go")
	if _, err := os.Stat(outPath); os.IsNotExist(err) {
		t.Error("expected output file to exist with --draft flag")
	}
}

func TestGenerateDraftFlagInvalid(t *testing.T) {
	schemaPath := findTestdataSchema(t, "basic/simple_object.json")

	tmpDir := t.TempDir()
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--output-dir", tmpDir, "--draft", "invalid-draft", schemaPath})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid draft value")
	}
	if !strings.Contains(err.Error(), "unknown draft version") {
		t.Errorf("expected 'unknown draft version' error, got: %v", err)
	}
}

func TestParseValidationMode(t *testing.T) {
	tests := []struct {
		input   string
		want    generator.ValidationMode
		wantErr bool
	}{
		{"", generator.ValidationModeStatic, false},
		{"static", generator.ValidationModeStatic, false},
		{"hybrid", generator.ValidationModeHybrid, false},
		{"runtime", generator.ValidationModeRuntime, false},
		{" HYBRID ", generator.ValidationModeHybrid, false},
		{"invalid", generator.ValidationModeStatic, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseValidationMode(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseValidationMode(%q) expected error, got %v", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("parseValidationMode(%q) unexpected error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("parseValidationMode(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestGenerateValidationHybridEmitsCapability(t *testing.T) {
	tmpDir := t.TempDir()
	schemaPath := filepath.Join(tmpDir, "runtime_features.json")
	schemaContent := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "array",
		"prefixItems": [{"type":"string"}],
		"unevaluatedItems": false
	}`
	if err := os.WriteFile(schemaPath, []byte(schemaContent), 0o644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "out")
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--output-dir", outDir, "--package", "testpkg", "--validation", "hybrid", schemaPath})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	outPath := filepath.Join(outDir, "runtime_features.go")
	content, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("expected output file: %v", err)
	}

	src := string(content)
	if !strings.Contains(src, `const SchemagenValidationMode = "hybrid"`) {
		t.Errorf("generated code missing hybrid validation mode, got:\n%s", src)
	}
	if !strings.Contains(src, `func SchemagenValidationCapability() validationruntime.Capability`) {
		t.Errorf("generated code missing runtime capability helper, got:\n%s", src)
	}
	if !strings.Contains(src, `validationruntime.Feature("unevaluatedItems")`) {
		t.Errorf("generated code missing unevaluatedItems runtime feature, got:\n%s", src)
	}
}

func TestGenerateValidationRuntimeEmitsCapability(t *testing.T) {
	tmpDir := t.TempDir()
	schemaPath := filepath.Join(tmpDir, "runtime_features.json")
	schemaContent := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "array",
		"prefixItems": [{"type":"string"}],
		"unevaluatedItems": false
	}`
	if err := os.WriteFile(schemaPath, []byte(schemaContent), 0o644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "out")
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--output-dir", outDir, "--package", "testpkg", "--validation", "runtime", schemaPath})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	outPath := filepath.Join(outDir, "runtime_features.go")
	content, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("expected output file: %v", err)
	}

	src := string(content)
	if !strings.Contains(src, `const SchemagenValidationMode = "runtime"`) {
		t.Errorf("generated code missing runtime validation mode, got:\n%s", src)
	}
	if !strings.Contains(src, `func SchemagenValidationCapability() validationruntime.Capability`) {
		t.Errorf("generated code missing runtime capability helper, got:\n%s", src)
	}
}

func TestGenerateDraftFlagInHelp(t *testing.T) {
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--help"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "--draft") {
		t.Errorf("help output missing --draft flag, got:\n%s", output)
	}
	if !strings.Contains(output, "2020-12") {
		t.Errorf("help output missing draft value examples, got:\n%s", output)
	}
}

func TestGenerateOutputFilenameCollision(t *testing.T) {
	tmpDir := t.TempDir()
	dirA := filepath.Join(tmpDir, "a")
	dirB := filepath.Join(tmpDir, "b")
	for _, d := range []string{dirA, dirB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	schemaJSON := `{"type":"object","properties":{"x":{"type":"string"}}}`
	aPath := filepath.Join(dirA, "user.json")
	bPath := filepath.Join(dirB, "user.json")
	if err := os.WriteFile(aPath, []byte(schemaJSON), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(bPath, []byte(schemaJSON), 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}

	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", "--output-dir", filepath.Join(tmpDir, "out"), aPath, bPath})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for colliding output filenames")
	}
	if !strings.Contains(err.Error(), "user.go") {
		t.Errorf("expected collision error naming user.go, got: %v", err)
	}

	// The same file listed twice is not a collision.
	cmd2 := NewRootCmd()
	cmd2.SetOut(new(bytes.Buffer))
	cmd2.SetErr(new(bytes.Buffer))
	cmd2.SetArgs([]string{"generate", "--output-dir", filepath.Join(tmpDir, "out2"), aPath, aPath})
	if err := cmd2.Execute(); err != nil {
		t.Errorf("same file twice should not be a collision, got: %v", err)
	}
}

// TestGenerateMultipleSchemasMatchesSingleRuns guards the shared emitter: it is
// now constructed once per run instead of once per input file, so a multi-file
// run must still produce byte-identical output to generating each file on its
// own. Any state the emitter carried between files would show up here.
func TestGenerateMultipleSchemasMatchesSingleRuns(t *testing.T) {
	schema1 := findTestdataSchema(t, "basic/simple_object.json")
	schema2 := findTestdataSchema(t, "advanced/pattern_properties.json")

	run := func(t *testing.T, args ...string) string {
		t.Helper()
		dir := t.TempDir()
		cmd := NewRootCmd()
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		cmd.SetArgs(append([]string{"generate", "--output-dir", dir}, args...))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("generate %v: %v\n%s", args, err, buf.String())
		}
		return dir
	}

	together := run(t, schema1, schema2)
	apart1 := run(t, schema1)
	apart2 := run(t, schema2)

	for _, tc := range []struct {
		file    string
		aloneIn string
	}{
		{"simple_object.go", apart1},
		{"pattern_properties.go", apart2},
	} {
		combined, err := os.ReadFile(filepath.Join(together, tc.file))
		if err != nil {
			t.Fatalf("reading %s from the combined run: %v", tc.file, err)
		}
		alone, err := os.ReadFile(filepath.Join(tc.aloneIn, tc.file))
		if err != nil {
			t.Fatalf("reading %s from its single run: %v", tc.file, err)
		}
		if !bytes.Equal(combined, alone) {
			t.Errorf("%s differs between a combined run and a single-file run", tc.file)
		}
	}
}

// TestGenerateMultipleSchemasSharingHelpersCompiles is a regression for shared
// helper functions being emitted into every file that needed them: two schemas
// in one package then declared the same package-level function twice and the
// generated package did not compile. The helpers now live in one file per
// destination package.
func TestGenerateMultipleSchemasSharingHelpersCompiles(t *testing.T) {
	dir := t.TempDir()
	// Three schemas with overlapping helper needs: a struct-level oneOf (needs
	// oneofHasRequiredFields), and two untyped applicator schemas (need the
	// _dyn* predicates), one of which also forces math into its own file.
	sources := map[string]string{
		"a.json": `{"title":"A","type":"object","properties":{"p":{"oneOf":[{"type":"string"},{"type":"integer"}]}}}`,
		"b.json": `{"title":"B","oneOf":[{"type":"integer"},{"minimum":2}]}`,
		"c.json": `{"title":"C","oneOf":[{"multipleOf":3},{"maximum":1}]}`,
	}
	var args []string
	for name, body := range sources {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
		args = append(args, p)
	}
	sort.Strings(args)

	outDir := filepath.Join(dir, "out")
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(append([]string{"generate", "--output-dir", outDir, "--package", "gen"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("generate: %v\n%s", err, buf.String())
	}

	// The helper file is written once, not per schema.
	if _, err := os.Stat(filepath.Join(outDir, "schemagen_helpers.go")); err != nil {
		t.Fatalf("expected a shared helper file: %v", err)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("reading output dir: %v", err)
	}
	var helperFiles int
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(outDir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		if bytes.Contains(body, []byte("func _dynNumber(")) || bytes.Contains(body, []byte("func oneofHasRequiredFields(")) {
			helperFiles++
		}
	}
	if helperFiles != 1 {
		t.Fatalf("%d files declare shared helpers, want exactly 1", helperFiles)
	}
}

// writeUnresolvableRefFixture writes a schema whose $ref no resolver can serve.
func writeUnresolvableRefFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gadget.json")
	src := `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "app://models/gadget.json",
		"type": "object",
		"properties": {
			"widget": {"$ref": "app://models/widget.json#/definitions/widget"}
		}
	}`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runGenerateArgs(t *testing.T, args ...string) error {
	t.Helper()
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(append([]string{"generate"}, args...))
	return cmd.Execute()
}

func TestGenerateFailsOnUnresolvedRef(t *testing.T) {
	mainPath := writeUnresolvableRefFixture(t)

	err := runGenerateArgs(t, mainPath, "-o", t.TempDir())
	if err == nil {
		t.Fatal("expected generation to fail on an unresolvable $ref")
	}
	msg := err.Error()
	if !strings.Contains(msg, "app://models/widget.json#/definitions/widget") {
		t.Errorf("error should name the unresolved ref, got: %v", msg)
	}
	if !strings.Contains(msg, "--lenient-refs") {
		t.Errorf("error should mention the --lenient-refs escape hatch, got: %v", msg)
	}
}

func TestGenerateLenientRefsDegradesToAny(t *testing.T) {
	mainPath := writeUnresolvableRefFixture(t)
	outDir := t.TempDir()

	if err := runGenerateArgs(t, mainPath, "-o", outDir, "-p", "models", "--lenient-refs"); err != nil {
		t.Fatalf("generate --lenient-refs: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(outDir, "gadget.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Widget") {
		t.Errorf("expected a Widget field in the degraded output:\n%s", out)
	}
}

func writeSimpleSchema(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	src := `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "object",
		"properties": {"name": {"type": "string"}}
	}`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func execGenerate(t *testing.T, args ...string) error {
	t.Helper()
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(append([]string{"generate"}, args...))
	return cmd.Execute()
}

func TestGenerateRootName(t *testing.T) {
	path := writeSimpleSchema(t, "person.json")
	outDir := t.TempDir()

	if err := execGenerate(t, path, "-o", outDir, "--root-name", "Employee"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(outDir, "person.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "type Employee struct") {
		t.Errorf("expected root type Employee, got:\n%s", out)
	}
}

func TestGenerateRootNameFromFilename(t *testing.T) {
	path := writeSimpleSchema(t, "trigger_fixture.json")
	outDir := t.TempDir()

	if err := execGenerate(t, path, "-o", outDir, "--root-name-from-filename"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(outDir, "trigger_fixture.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "type TriggerFixtureJSON struct") {
		t.Errorf("expected root type TriggerFixtureJSON, got:\n%s", out)
	}
}

func TestGenerateRootNameOverridesTitle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "titled.json")
	src := `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"title": "Some Title",
		"type": "object",
		"properties": {"name": {"type": "string"}}
	}`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()

	if err := execGenerate(t, path, "-o", outDir, "--root-name", "Wanted"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(outDir, "titled.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "type Wanted struct") {
		t.Errorf("--root-name should override the schema title, got:\n%s", out)
	}
}

func TestGenerateRootNameRejectsMultipleSchemas(t *testing.T) {
	a := writeSimpleSchema(t, "a.json")
	b := writeSimpleSchema(t, "b.json")

	err := execGenerate(t, a, b, "-o", t.TempDir(), "--root-name", "X")
	if err == nil {
		t.Fatal("expected an error for --root-name with multiple schemas")
	}
	if !strings.Contains(err.Error(), "--root-name-from-filename") {
		t.Errorf("error should point at --root-name-from-filename, got: %v", err)
	}
}

func TestGenerateRootNameUsedVerbatim(t *testing.T) {
	// Initialism rules must not rewrite an explicit name (TopicJson must
	// not become TopicJSON) — exact names matter when replacing previously
	// generated code.
	path := writeSimpleSchema(t, "topic.json")
	outDir := t.TempDir()

	if err := execGenerate(t, path, "-o", outDir, "--root-name", "TopicJson"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(outDir, "topic.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "type TopicJson struct") {
		t.Errorf("expected verbatim root type TopicJson, got:\n%s", out)
	}
}

func TestGenerateRootNameRejectsInvalidIdentifier(t *testing.T) {
	path := writeSimpleSchema(t, "person.json")

	for _, bad := range []string{"lowercase", "Has Space", "1Digit"} {
		if err := execGenerate(t, path, "-o", t.TempDir(), "--root-name", bad); err == nil {
			t.Errorf("expected an error for root name %q", bad)
		}
	}
}

// TestGenerateItemsOneOfCrossDocumentVariants covers two regressions at once:
// an inline oneOf in array-items position must produce a typed sealed wrapper
// (not []any), and same-named definitions from different documents must
// materialize as distinct types instead of silently reusing the first one.
func TestGenerateItemsOneOfCrossDocumentVariants(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("board.json", `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"title": "Board",
		"type": "object",
		"properties": {
			"elements": {
				"type": "array",
				"items": {
					"oneOf": [
						{"$ref": "alpha.json#/definitions/element"},
						{"$ref": "beta.json#/definitions/element"}
					]
				}
			}
		}
	}`)
	write("alpha.json", `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"definitions": {
			"element": {
				"type": "object",
				"properties": {"kind": {"enum": ["alpha"]}, "alpha": {"type": "string"}},
				"required": ["kind", "alpha"]
			}
		}
	}`)
	write("beta.json", `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"definitions": {
			"element": {
				"type": "object",
				"properties": {"kind": {"enum": ["beta"]}, "beta": {"type": "number"}},
				"required": ["kind", "beta"]
			}
		}
	}`)

	outDir := t.TempDir()
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"generate", filepath.Join(dir, "board.json"), "-o", outDir, "-p", "board"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(outDir, "board.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(out)

	if !strings.Contains(src, "[]BoardElementsItem") {
		t.Errorf("items oneOf should produce a typed element slice, got:\n%s", src)
	}
	// Two distinct variant types, disambiguated by owning document.
	if !strings.Contains(src, "type Element struct") || !strings.Contains(src, "type BetaElement struct") {
		t.Errorf("expected distinct Element and BetaElement variant types, got:\n%s", src)
	}
	if strings.Contains(src, "Element2 *Element") {
		t.Errorf("same-named cross-document variants collapsed onto one type:\n%s", src)
	}
}
