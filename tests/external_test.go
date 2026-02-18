package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

// allDrafts lists all draft directories in the test suite.
var allDrafts = []string{"draft3", "draft4", "draft6", "draft7", "draft2019-09", "draft2020-12"}

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
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module compile_test\n\ngo 1.22\n"), 0o644); err != nil {
		return fmt.Errorf("write go.mod: %w", err)
	}

	cmd := exec.Command("go", "build", ".")
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
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module roundtrip_test\n\ngo 1.22\n"), 0o644); err != nil {
		return fmt.Errorf("write go.mod: %w", err)
	}

	cmd := exec.Command("go", "run", ".")
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
