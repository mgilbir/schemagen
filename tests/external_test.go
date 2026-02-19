package tests

import (
	"context"
	"encoding/json"
	"fmt"
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

// jstsBaseDir is the path to the JSON Schema Test Suite tests directory,
// relative to the tests/ directory where these tests run.
const jstsBaseDir = "../testdata/external/JSON-Schema-Test-Suite/tests"

// jstsRemotesDir is the path to the remotes directory in the test suite.
const jstsRemotesDir = "../testdata/external/JSON-Schema-Test-Suite/remotes"

// remoteBaseURL is the base URL that the JSTS expects for remote schemas.
const remoteBaseURL = "http://localhost:1234"

// goecma262 module metadata for temp go.mod files.
const (
	goecma262Version = "v0.0.0-20260219184840-8bfa4bb752b0"
	goecma262H1      = "h1:g5uVjex1bABu72M6R0A//gQDoVXPSatqP50yZDX5wUQ="
	goecma262GoMod   = "h1:wQvOAFchLrhVSiF4JsSzH+yE6eLpc8gOBrvpuahNucI="
)

// writeTestGoMod writes a go.mod and go.sum in dir that includes the goecma262 dependency.
// moduleName is the module name for the temp project (e.g. "compile_test", "roundtrip_test").
func writeTestGoMod(dir, moduleName string) error {
	goMod := fmt.Sprintf("module %s\n\ngo 1.23\n\nrequire github.com/mgilbir/goecma262 %s\n", moduleName, goecma262Version)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return fmt.Errorf("write go.mod: %w", err)
	}
	goSum := fmt.Sprintf("github.com/mgilbir/goecma262 %s %s\ngithub.com/mgilbir/goecma262 %s/go.mod %s\n",
		goecma262Version, goecma262H1, goecma262Version, goecma262GoMod)
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(goSum), 0o644); err != nil {
		return fmt.Errorf("write go.sum: %w", err)
	}
	return nil
}

// allDrafts lists all draft directories in the test suite.
var allDrafts = []string{"draft3", "draft4", "draft6", "draft7", "draft2019-09", "draft2020-12"}

// draftFromDir maps a test-suite directory name to a schema.Draft constant.
func draftFromDir(dir string) schema.Draft {
	switch dir {
	case "draft3":
		return schema.Draft03
	case "draft4":
		return schema.Draft04
	case "draft6":
		return schema.Draft06
	case "draft7":
		return schema.Draft07
	case "draft2019-09":
		return schema.Draft201909
	case "draft2020-12":
		return schema.Draft202012
	default:
		return schema.DraftUnknown
	}
}

// jstsTestGroup represents a single test group from the JSTS.
type jstsTestGroup struct {
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	Tests       []jstsTestCase  `json:"tests"`
}

// jstsTestCase represents a single test case within a test group.
type jstsTestCase struct {
	Description string          `json:"description"`
	Data        json.RawMessage `json:"data"`
	Valid       bool            `json:"valid"`
}

// loadRemoteSchemas walks the remotes/ directory and builds a map of URL → *Schema.
// This allows the generator to resolve $ref values pointing to http://localhost:1234/...
func loadRemoteSchemas(t *testing.T) map[string]*schema.Schema {
	t.Helper()
	schemas := make(map[string]*schema.Schema)
	err := filepath.Walk(jstsRemotesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var s schema.Schema
		if err := json.Unmarshal(data, &s); err != nil {
			// Skip unparseable schemas (some may be non-schema JSON).
			return nil
		}
		s.Normalize()

		// Build the URL key: remoteBaseURL + relative path from remotes dir.
		rel, err := filepath.Rel(jstsRemotesDir, path)
		if err != nil {
			return err
		}
		// Use forward slashes for URL path.
		urlKey := remoteBaseURL + "/" + filepath.ToSlash(rel)
		schemas[urlKey] = &s
		return nil
	})
	if err != nil {
		t.Logf("warning: could not load remote schemas: %v", err)
	}
	return schemas
}

// remotesResolver returns a SchemaResolver for the test suite's remote schemas.
// Returns nil if remotes can't be loaded.
func remotesResolver(t *testing.T) schema.SchemaResolver {
	t.Helper()
	schemas := loadRemoteSchemas(t)
	if len(schemas) == 0 {
		return nil
	}
	return schema.NewMappingResolver(schemas)
}

// requireTestSuite skips the test if the external test suite is not downloaded.
func requireTestSuite(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(jstsBaseDir); os.IsNotExist(err) {
		t.Skip("JSON Schema Test Suite not found. Run 'make download-test-suite' to enable external tests.")
	}
}

// failureKey builds a lookup key for the known-failures maps.
func failureKey(parts ...string) string {
	return strings.Join(parts, "/")
}

// checkKnownFailure implements bidirectional known-failure checking.
//   - Flaky test → t.Skipf (always skip, regardless of outcome)
//   - Known failure that fails → t.Skipf (expected)
//   - Known failure that passes → t.Errorf (remove from list)
//   - Unknown failure → t.Errorf (regression)
//   - Unknown pass → OK
func checkKnownFailure(t *testing.T, key string, err error, knownFailures map[string]string) {
	t.Helper()
	// Skip flaky tests that non-deterministically pass/fail due to Go map iteration order.
	if _, flaky := knownFlakyTests[key]; flaky {
		if err != nil {
			t.Skipf("flaky test (failed): %v", err)
		} else {
			t.Skipf("flaky test (passed)")
		}
		return
	}
	reason, isKnown := knownFailures[key]
	if err != nil {
		if isKnown {
			t.Skipf("known failure: %v (reason: %s)", err, reason)
		} else {
			t.Errorf("unexpected failure: %v\n  key: %s", err, key)
		}
	} else {
		if isKnown {
			t.Errorf("test passed but is in known-failures list — remove key %q (reason was: %s)", key, reason)
		}
	}
}

// listJSONFiles returns the relative paths of all .json files in a directory, recursively.
// Paths are relative to dir (e.g., "minLength.json", "optional/bignum.json").
func listJSONFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".json") {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("walking directory %s: %v", dir, err)
	}
	return files
}

// filenameWithoutExt strips the .json extension from a filename or relative path.
func filenameWithoutExt(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// loadTestGroups reads and parses a JSTS test file.
func loadTestGroups(t *testing.T, path string) []jstsTestGroup {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading test file %s: %v", path, err)
	}
	var groups []jstsTestGroup
	if err := json.Unmarshal(data, &groups); err != nil {
		t.Fatalf("parsing test file %s: %v", path, err)
	}
	return groups
}

// isCodeGenSuitable checks if a schema can produce a Go type definition.
// All JSON object schemas are suitable. Boolean schemas (true/false) are not.
func isCodeGenSuitable(schemaJSON json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(schemaJSON))
	if trimmed == "true" || trimmed == "false" {
		return false
	}
	// Any JSON object schema can produce a type definition (struct, alias, enum, etc.).
	return len(trimmed) > 0 && trimmed[0] == '{'
}

// isJSONObject checks if a JSON value is an object (starts with '{').
func isJSONObject(data json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(data))
	return len(trimmed) > 0 && trimmed[0] == '{'
}

// extractRootTypeNameFromCode finds the root type in generated code.
// Prefers struct types with JSON tags, then any struct, then type aliases named "Root".
// Returns empty string if none found (does not call t.Fatal).
func extractRootTypeNameFromCode(code string) string {
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
		// Final fallback: look for any type declaration (aliases, defined types).
		// e.g., "type Root = any", "type Root string", "type Root int64"
		// Prefer "Root" if it exists, otherwise use the last type found.
		var lastAlias string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "type ") && !strings.Contains(trimmed, " struct {") && !strings.Contains(trimmed, " interface {") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 3 {
					lastAlias = parts[1]
					if parts[1] == "Root" {
						return "Root"
					}
				}
			}
		}
		lastType = lastAlias
	}

	return lastType
}

// tryParse attempts to parse a JSTS schema into our schema.Schema type.
func tryParse(schemaJSON json.RawMessage) error {
	// Handle boolean schemas
	trimmed := strings.TrimSpace(string(schemaJSON))
	if trimmed == "true" || trimmed == "false" {
		// Boolean schemas are valid JSON Schema but our parser expects objects
		return nil
	}

	var s schema.Schema
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	s.Normalize()
	return nil
}

// tryGenerateAndCompile attempts the full pipeline: parse → generate IR → emit → compile.
func tryGenerateAndCompile(schemaJSON json.RawMessage, resolver schema.SchemaResolver) error {
	var s schema.Schema
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	s.Normalize()

	cfg := generator.Config{PackageName: "testpkg", OmitEmpty: true, Resolver: resolver}
	gen := generator.New(cfg)
	ir, err := gen.Generate(&s)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}

	em, err := emitter.New()
	if err != nil {
		return fmt.Errorf("emitter: %w", err)
	}
	src, err := em.Emit(ir)
	if err != nil {
		return fmt.Errorf("emit: %w", err)
	}

	// Compile in temp dir
	tmpDir, err := os.MkdirTemp("", "schemagen-external-*")
	if err != nil {
		return fmt.Errorf("tmpdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	content := strings.Replace(string(src), "package testpkg", "package compile_test", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(content), 0o644); err != nil {
		return fmt.Errorf("write types: %w", err)
	}
	if err := writeTestGoMod(tmpDir, "compile_test"); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("compile: %s\n%s", err, string(output))
	}
	return nil
}

// tryRoundTrip attempts the full round-trip: parse → generate → compile → unmarshal → marshal → compare.
func tryRoundTrip(schemaJSON, dataJSON json.RawMessage, resolver schema.SchemaResolver) error {
	var s schema.Schema
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	s.Normalize()

	cfg := generator.Config{PackageName: "testpkg", OmitEmpty: true, Resolver: resolver}
	gen := generator.New(cfg)
	ir, err := gen.Generate(&s)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}

	em, err := emitter.New()
	if err != nil {
		return fmt.Errorf("emitter: %w", err)
	}
	src, err := em.Emit(ir)
	if err != nil {
		return fmt.Errorf("emit: %w", err)
	}

	rootType := extractRootTypeNameFromCode(string(src))
	if rootType == "" {
		return fmt.Errorf("could not find root struct type in generated code")
	}

	tmpDir, err := os.MkdirTemp("", "schemagen-rt-*")
	if err != nil {
		return fmt.Errorf("tmpdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	mainContent := strings.Replace(string(src), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(mainContent), 0o644); err != nil {
		return fmt.Errorf("write types: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "fixture.json"), dataJSON, 0o644); err != nil {
		return fmt.Errorf("write fixture: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(generateRoundTripMain(rootType)), 0o644); err != nil {
		return fmt.Errorf("write main: %w", err)
	}
	if err := writeTestGoMod(tmpDir, "roundtrip_test"); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("round-trip: %s\n%s", err, string(output))
	}
	if strings.TrimSpace(string(output)) != "PASS" {
		return fmt.Errorf("round-trip mismatch:\n%s", string(output))
	}
	return nil
}

// hasValidateMethod checks if generated Go code contains a Validate() method.
func hasValidateMethod(code string) bool {
	// Check that the root type (identified by extractRootTypeNameFromCode) has a Validate() method.
	rootType := extractRootTypeNameFromCode(code)
	if rootType == "" {
		return false
	}
	// Look for "func (<recv> <RootType>) Validate() error {" pattern.
	// The receiver is typically a single lowercase letter.
	return strings.Contains(code, rootType+") Validate() error {")
}

// generateValidateMain creates a Go main() that:
// 1. Reads fixture.json
// 2. Unmarshals into the generated type
// 3. Calls Validate()
// 4. Prints "VALID" if no error, "INVALID: <message>" if error
func generateValidateMain(rootType string) string {
	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	data, err := os.ReadFile("fixture.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading fixture: %%v\n", err)
		os.Exit(1)
	}

	var obj %s
	if err := json.Unmarshal(data, &obj); err != nil {
		// Any unmarshal error is a type mismatch: the JSON data doesn't
		// fit the generated Go type. This is equivalent to a JSON Schema
		// validation failure (wrong type, missing required field, etc.).
		fmt.Printf("INVALID: unmarshal: %%v\n", err)
		return
	}

	if err := obj.Validate(); err != nil {
		fmt.Printf("INVALID: %%v\n", err)
	} else {
		fmt.Println("VALID")
	}
}
`, rootType)
}

// tryGenerateWithValidation attempts: parse → generate → emit, returns generated code
// only if it contains a Validate() method. Returns ("", nil) if no Validate() method
// is found (not an error, just a skip condition).
func tryGenerateWithValidation(schemaJSON json.RawMessage, resolver schema.SchemaResolver, draft schema.Draft) (string, error) {
	var s schema.Schema
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	s.Normalize()

	cfg := generator.Config{PackageName: "testpkg", OmitEmpty: true, Resolver: resolver, Draft: draft}
	gen := generator.New(cfg)
	ir, err := gen.Generate(&s)
	if err != nil {
		return "", fmt.Errorf("generate: %w", err)
	}

	em, err := emitter.New()
	if err != nil {
		return "", fmt.Errorf("emitter: %w", err)
	}
	src, err := em.Emit(ir)
	if err != nil {
		return "", fmt.Errorf("emit: %w", err)
	}

	code := string(src)
	if !hasValidateMethod(code) {
		return "", nil // no validation to test
	}
	return code, nil
}

// tryValidation runs a validation test using pre-generated code.
// The code must already have a Validate() method.
func tryValidation(code string, dataJSON json.RawMessage, expectValid bool) error {
	rootType := extractRootTypeNameFromCode(code)
	if rootType == "" {
		return fmt.Errorf("could not find root type in generated code")
	}

	tmpDir, err := os.MkdirTemp("", "schemagen-val-*")
	if err != nil {
		return fmt.Errorf("tmpdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	mainContent := strings.Replace(code, "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(mainContent), 0o644); err != nil {
		return fmt.Errorf("write types: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "fixture.json"), dataJSON, 0o644); err != nil {
		return fmt.Errorf("write fixture: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(generateValidateMain(rootType)), 0o644); err != nil {
		return fmt.Errorf("write main: %w", err)
	}
	if err := writeTestGoMod(tmpDir, "validate_test"); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run: %s\n%s", err, string(output))
	}

	result := strings.TrimSpace(string(output))
	if expectValid {
		if result != "VALID" {
			return fmt.Errorf("expected VALID but got: %s", result)
		}
	} else {
		if !strings.HasPrefix(result, "INVALID:") {
			return fmt.Errorf("expected INVALID but got: %s", result)
		}
	}
	return nil
}

// TestExternalParsing tests that we can parse every schema in the external test suite.
func TestExternalParsing(t *testing.T) {
	requireTestSuite(t)

	for _, draft := range allDrafts {
		t.Run(draft, func(t *testing.T) {
			draftDir := filepath.Join(jstsBaseDir, draft)
			if _, err := os.Stat(draftDir); os.IsNotExist(err) {
				t.Skipf("draft directory %s not found", draft)
				return
			}

			files := listJSONFiles(t, draftDir)
			for _, file := range files {
				t.Run(filenameWithoutExt(file), func(t *testing.T) {
					groups := loadTestGroups(t, filepath.Join(draftDir, file))
					for _, group := range groups {
						t.Run(group.Description, func(t *testing.T) {
							key := failureKey(draft, filenameWithoutExt(file), group.Description)
							err := tryParse(group.Schema)
							checkKnownFailure(t, key, err, knownParseFailures)
						})
					}
				})
			}
		})
	}
}

// TestExternalCodeGen tests that we can generate compilable Go code from object-like schemas.
func TestExternalCodeGen(t *testing.T) {
	requireTestSuite(t)
	resolver := remotesResolver(t)

	for _, draft := range allDrafts {
		t.Run(draft, func(t *testing.T) {
			draftDir := filepath.Join(jstsBaseDir, draft)
			if _, err := os.Stat(draftDir); os.IsNotExist(err) {
				t.Skipf("draft directory %s not found", draft)
				return
			}

			files := listJSONFiles(t, draftDir)
			for _, file := range files {
				t.Run(filenameWithoutExt(file), func(t *testing.T) {
					groups := loadTestGroups(t, filepath.Join(draftDir, file))
					for _, group := range groups {
						if !isCodeGenSuitable(group.Schema) {
							continue
						}
						t.Run(group.Description, func(t *testing.T) {
							key := failureKey(draft, filenameWithoutExt(file), group.Description)
							err := tryGenerateAndCompile(group.Schema, resolver)
							checkKnownFailure(t, key, err, knownCodeGenFailures)
						})
					}
				})
			}
		})
	}
}

// TestExternalRoundTrip tests lossless JSON round-tripping through generated code.
func TestExternalRoundTrip(t *testing.T) {
	requireTestSuite(t)
	resolver := remotesResolver(t)

	for _, draft := range allDrafts {
		t.Run(draft, func(t *testing.T) {
			draftDir := filepath.Join(jstsBaseDir, draft)
			if _, err := os.Stat(draftDir); os.IsNotExist(err) {
				t.Skipf("draft directory %s not found", draft)
				return
			}

			files := listJSONFiles(t, draftDir)
			for _, file := range files {
				t.Run(filenameWithoutExt(file), func(t *testing.T) {
					groups := loadTestGroups(t, filepath.Join(draftDir, file))
					for _, group := range groups {
						if !isCodeGenSuitable(group.Schema) {
							continue
						}

						// Collect valid test cases (objects, primitives, arrays, etc.)
						var validTests []jstsTestCase
						for _, tc := range group.Tests {
							if tc.Valid {
								validTests = append(validTests, tc)
							}
						}
						if len(validTests) == 0 {
							continue
						}

						t.Run(group.Description, func(t *testing.T) {
							for _, tc := range validTests {
								t.Run(tc.Description, func(t *testing.T) {
									key := failureKey(draft, filenameWithoutExt(file), group.Description, tc.Description)
									err := tryRoundTrip(group.Schema, tc.Data, resolver)
									checkKnownFailure(t, key, err, knownRoundTripFailures)
								})
							}
						})
					}
				})
			}
		})
	}
}

// TestExternalValidation tests that generated Validate() methods correctly accept
// valid data and reject invalid data according to the JSON Schema.
//
// Test structure:
//   - Schemas that fail code generation or don't produce a Validate() method are
//     logged as skips (these are tracked by TestExternalCodeGen or are inherently
//     non-validatable schema types like composition-only, format-only, etc.).
//   - Schemas that DO produce a Validate() method are tested per-case, with
//     both valid and invalid data. Only these per-case results use the
//     knownValidationFailures bidirectional checking.
//
// The test logs group-level skip counts so the gap is visible, not hidden.
func TestExternalValidation(t *testing.T) {
	requireTestSuite(t)
	resolver := remotesResolver(t)

	var totalGroups, skippedCG, skippedNoValidate, testedGroups int

	for _, draft := range allDrafts {
		t.Run(draft, func(t *testing.T) {
			draftDir := filepath.Join(jstsBaseDir, draft)
			if _, err := os.Stat(draftDir); os.IsNotExist(err) {
				t.Skipf("draft directory %s not found", draft)
				return
			}

			files := listJSONFiles(t, draftDir)
			for _, file := range files {
				t.Run(filenameWithoutExt(file), func(t *testing.T) {
					groups := loadTestGroups(t, filepath.Join(draftDir, file))
					for _, group := range groups {
						if !isCodeGenSuitable(group.Schema) {
							continue
						}
						totalGroups++

						// Generate code once per group.
						code, cgErr := tryGenerateWithValidation(group.Schema, resolver, draftFromDir(draft))

						if cgErr != nil {
							skippedCG++
							continue
						}
						if code == "" {
							skippedNoValidate++
							continue
						}

						testedGroups++
						t.Run(group.Description, func(t *testing.T) {
							for _, tc := range group.Tests {
								t.Run(tc.Description, func(t *testing.T) {
									key := failureKey(draft, filenameWithoutExt(file), group.Description, tc.Description)
									err := tryValidation(code, tc.Data, tc.Valid)
									checkKnownFailure(t, key, err, knownValidationFailures)
								})
							}
						})
					}
				})
			}
		})
	}

	t.Logf("Validation coverage: %d/%d groups tested (%d skipped: %d codegen failures, %d no Validate() method)",
		testedGroups, totalGroups, skippedCG+skippedNoValidate, skippedCG, skippedNoValidate)
}
