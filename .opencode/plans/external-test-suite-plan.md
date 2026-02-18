# External JSON Schema Test Suite Integration — Implementation Plan

## Overview

Integrate the official [JSON Schema Test Suite](https://github.com/json-schema-org/JSON-Schema-Test-Suite) (JSTS) to expand test coverage. The suite is designed for validators, so we adapt it for our three code-generator use cases: parsing, code generation, and round-trip correctness.

## Design Decisions (Confirmed)

- **Acquisition**: `git clone` into `testdata/external/JSON-Schema-Test-Suite/`, pinned to a commit
- **Drafts**: All drafts tested (`draft3`, `draft4`, `draft6`, `draft7`, `draft2019-09`, `draft2020-12`), with known-failures for unsupported ones
- **Known failures**: Go maps in `tests/external_known_failures.go`

## JSTS Format

Each `.json` file is an array of test groups:

```json
[
  {
    "description": "integer type matches integers",
    "schema": { "type": "integer" },
    "tests": [
      { "description": "an integer is an integer", "data": 1, "valid": true },
      { "description": "a string is not an integer", "data": "foo", "valid": false }
    ]
  }
]
```

## Files to Create/Modify

### 1. `.gitignore` — Add `testdata/external/`

```gitignore
# External test suites (downloaded, not committed)
testdata/external/
```

### 2. `Makefile` — Add targets

```makefile
JSTS_DIR := testdata/external/JSON-Schema-Test-Suite
JSTS_COMMIT := latest  # or a specific commit hash

.PHONY: download-test-suite test-external

download-test-suite:
	@if [ ! -d "$(JSTS_DIR)" ]; then \
		echo "Cloning JSON Schema Test Suite..."; \
		git clone https://github.com/json-schema-org/JSON-Schema-Test-Suite.git $(JSTS_DIR); \
	else \
		echo "JSON Schema Test Suite already present at $(JSTS_DIR)"; \
	fi

test-external: download-test-suite
	go test ./tests/... -run TestExternal -v -count=1
```

### 3. `tests/external_known_failures.go` — Known failure maps

```go
package tests

// Keys are "draft/filename/group_description" (e.g., "draft7/type/integer type matches integers")
// Values are a short reason string.

var knownParseFailures = map[string]string{
    // Start empty — populated after first run
}

var knownCodeGenFailures = map[string]string{
    // Start empty — populated after first run
}

var knownRoundTripFailures = map[string]string{
    // Start empty — populated after first run
}
```

### 4. `tests/external_test.go` — Main test file

#### Types for JSTS parsing

```go
type jstsTestGroup struct {
    Description string          `json:"description"`
    Schema      json.RawMessage `json:"schema"`
    Tests       []jstsTestCase  `json:"tests"`
}

type jstsTestCase struct {
    Description string          `json:"description"`
    Data        json.RawMessage `json:"data"`
    Valid       bool            `json:"valid"`
}
```

Note: `Schema` is `json.RawMessage` — we need the raw bytes both for parsing into our `schema.Schema` and for writing to temp files. `Data` is also `json.RawMessage` since it can be any JSON value.

#### Skip logic

```go
const jstsBaseDir = "../testdata/external/JSON-Schema-Test-Suite/tests"

func requireTestSuite(t *testing.T) {
    if _, err := os.Stat(jstsBaseDir); os.IsNotExist(err) {
        t.Skip("JSON Schema Test Suite not found. Run 'make download-test-suite' to enable external tests.")
    }
}
```

#### Draft discovery

Walk all draft directories:

```go
var allDrafts = []string{"draft3", "draft4", "draft6", "draft7", "draft2019-09", "draft2020-12"}
```

For each draft, list all `.json` files in the top-level directory (skip `optional/` subdirectory for now — those are edge cases).

#### Key for known failures

```go
func failureKey(draft, filename, groupDesc string) string {
    return draft + "/" + filename + "/" + groupDesc
}
```

#### Test Category 1: `TestExternalParsing`

For each draft, for each `.json` file, for each test group:
1. Parse the `schema` field into `*schema.Schema` via `json.Unmarshal`
2. Call `s.Normalize()`
3. If error: check known-failures list
4. Use bidirectional checking:
   - In known-failures + fails = OK (expected)
   - In known-failures + passes = FAIL (`t.Errorf("unexpectedly passed, remove from known failures")`)
   - Not in known-failures + fails = FAIL (`t.Errorf(...)`)
   - Not in known-failures + passes = OK

```go
func TestExternalParsing(t *testing.T) {
    requireTestSuite(t)
    for _, draft := range allDrafts {
        t.Run(draft, func(t *testing.T) {
            draftDir := filepath.Join(jstsBaseDir, draft)
            files := listJSONFiles(t, draftDir)
            for _, file := range files {
                t.Run(filenameWithoutExt(file), func(t *testing.T) {
                    groups := loadTestGroups(t, filepath.Join(draftDir, file))
                    for _, group := range groups {
                        t.Run(group.Description, func(t *testing.T) {
                            key := failureKey(draft, filenameWithoutExt(file), group.Description)
                            var s schema.Schema
                            err := json.Unmarshal(group.Schema, &s)
                            if err == nil {
                                s.Normalize()
                            }
                            checkKnownFailure(t, key, err, knownParseFailures)
                        })
                    }
                })
            }
        })
    }
}
```

#### Test Category 2: `TestExternalCodeGen`

For each test group where the schema is suitable for code generation (has `properties`, or `type: "object"`, or `allOf`/`oneOf`/`anyOf`):
1. Parse schema
2. Generate IR via `generator.Generate()`
3. Emit Go code via `emitter.Emit()`
4. Compile in a temp directory

```go
func TestExternalCodeGen(t *testing.T) {
    requireTestSuite(t)
    for _, draft := range allDrafts {
        t.Run(draft, func(t *testing.T) {
            draftDir := filepath.Join(jstsBaseDir, draft)
            files := listJSONFiles(t, draftDir)
            for _, file := range files {
                t.Run(filenameWithoutExt(file), func(t *testing.T) {
                    groups := loadTestGroups(t, filepath.Join(draftDir, file))
                    for _, group := range groups {
                        t.Run(group.Description, func(t *testing.T) {
                            key := failureKey(draft, filenameWithoutExt(file), group.Description)
                            err := tryGenerateAndCompile(group.Schema)
                            checkKnownFailure(t, key, err, knownCodeGenFailures)
                        })
                    }
                })
            }
        })
    }
}
```

The `tryGenerateAndCompile` function:
1. `json.Unmarshal` into `schema.Schema`
2. `s.Normalize()`
3. `generator.New(cfg).Generate(&s)` → IR
4. `emitter.New().Emit(ir)` → Go source bytes
5. Write to temp dir with `go.mod`, `go build .`

#### Test Category 3: `TestExternalRoundTrip`

For each test group where:
- The schema is suitable for code generation (same filter as CodeGen)
- The test has `valid: true` data instances
- The data is a JSON object (not a primitive or array — our code generates structs)

For each valid object data instance:
1. Generate and compile the code (reuse from CodeGen)
2. Write the data as `fixture.json`
3. Write a `main.go` that does unmarshal → marshal → compare (reuse `generateRoundTripMain`)
4. `go run .`

```go
func TestExternalRoundTrip(t *testing.T) {
    requireTestSuite(t)
    for _, draft := range allDrafts {
        t.Run(draft, func(t *testing.T) {
            draftDir := filepath.Join(jstsBaseDir, draft)
            files := listJSONFiles(t, draftDir)
            for _, file := range files {
                t.Run(filenameWithoutExt(file), func(t *testing.T) {
                    groups := loadTestGroups(t, filepath.Join(draftDir, file))
                    for _, group := range groups {
                        t.Run(group.Description, func(t *testing.T) {
                            // Skip if schema can't generate code
                            if !isCodeGenSuitable(group.Schema) {
                                t.Skip("schema not suitable for code generation")
                                return
                            }
                            for _, tc := range group.Tests {
                                if !tc.Valid {
                                    continue // only test valid data
                                }
                                if !isJSONObject(tc.Data) {
                                    continue // only test objects
                                }
                                t.Run(tc.Description, func(t *testing.T) {
                                    key := failureKey(draft, filenameWithoutExt(file), group.Description+"/"+tc.Description)
                                    err := tryRoundTrip(group.Schema, tc.Data)
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
```

#### Helper: `checkKnownFailure`

```go
func checkKnownFailure(t *testing.T, key string, err error, knownFailures map[string]string) {
    t.Helper()
    _, isKnown := knownFailures[key]
    if err != nil {
        if isKnown {
            t.Skipf("known failure: %s (reason: %s)", err, knownFailures[key])
        } else {
            t.Errorf("unexpected failure: %v", err)
        }
    } else {
        if isKnown {
            t.Errorf("test passed but is in known-failures list — remove key %q (reason was: %s)", key, knownFailures[key])
        }
        // else: pass, as expected
    }
}
```

Note: Known failures that fail as expected use `t.Skipf` (not `t.Logf`) so they show up clearly in verbose output but don't count as failures or passes.

#### Helper: `isCodeGenSuitable`

```go
func isCodeGenSuitable(schemaJSON json.RawMessage) bool {
    var probe struct {
        Type       json.RawMessage        `json:"type"`
        Properties map[string]json.RawMessage `json:"properties"`
        AllOf      json.RawMessage        `json:"allOf"`
        OneOf      json.RawMessage        `json:"oneOf"`
        AnyOf      json.RawMessage        `json:"anyOf"`
    }
    if err := json.Unmarshal(schemaJSON, &probe); err != nil {
        return false
    }
    // Has properties → object-like
    if len(probe.Properties) > 0 {
        return true
    }
    // Has type: "object"
    if probe.Type != nil {
        var t string
        if json.Unmarshal(probe.Type, &t) == nil && t == "object" {
            return true
        }
    }
    // Has composition keywords
    if probe.AllOf != nil || probe.OneOf != nil || probe.AnyOf != nil {
        return true
    }
    return false
}
```

#### Helper: `isJSONObject`

```go
func isJSONObject(data json.RawMessage) bool {
    return len(data) > 0 && data[0] == '{'
}
```

#### Helper: `tryGenerateAndCompile`

```go
func tryGenerateAndCompile(schemaJSON json.RawMessage) error {
    var s schema.Schema
    if err := json.Unmarshal(schemaJSON, &s); err != nil {
        return fmt.Errorf("parse: %w", err)
    }
    s.Normalize()

    cfg := generator.Config{PackageName: "testpkg", OmitEmpty: true}
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
        return err
    }
    defer os.RemoveAll(tmpDir)

    content := strings.Replace(string(src), "package testpkg", "package compile_test", 1)
    if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(content), 0o644); err != nil {
        return err
    }
    if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module compile_test\n\ngo 1.22\n"), 0o644); err != nil {
        return err
    }

    cmd := exec.Command("go", "build", ".")
    cmd.Dir = tmpDir
    output, err := cmd.CombinedOutput()
    if err != nil {
        return fmt.Errorf("compile: %s\n%s", err, string(output))
    }
    return nil
}
```

#### Helper: `tryRoundTrip`

```go
func tryRoundTrip(schemaJSON, dataJSON json.RawMessage) error {
    var s schema.Schema
    if err := json.Unmarshal(schemaJSON, &s); err != nil {
        return fmt.Errorf("parse: %w", err)
    }
    s.Normalize()

    cfg := generator.Config{PackageName: "testpkg", OmitEmpty: true}
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

    rootType := extractRootTypeName2(string(src))
    if rootType == "" {
        return fmt.Errorf("could not find root type in generated code")
    }

    tmpDir, err := os.MkdirTemp("", "schemagen-rt-*")
    if err != nil {
        return err
    }
    defer os.RemoveAll(tmpDir)

    mainContent := strings.Replace(string(src), "package testpkg", "package main", 1)
    os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(mainContent), 0o644)
    os.WriteFile(filepath.Join(tmpDir, "fixture.json"), dataJSON, 0o644)
    os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(generateRoundTripMain(rootType)), 0o644)
    os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module roundtrip_test\n\ngo 1.22\n"), 0o644)

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
```

## Implementation Steps

1. **Update `.gitignore`** — Add `testdata/external/` line
2. **Update `Makefile`** — Add `download-test-suite` and `test-external` targets
3. **Create `tests/external_known_failures.go`** — Start with empty maps
4. **Create `tests/external_test.go`** — All types, helpers, and 3 test functions
5. **Run `make download-test-suite`** — Clone the test suite
6. **Run external tests** — Collect failures
7. **Populate known-failures maps** — Add all legitimate failures with reason strings
8. **Verify** — `make test` (all existing tests still pass) + `make test-external` (external tests pass with known failures skipped)

## Notes

- The `extractRootTypeName` function from `roundtrip_test.go` takes a `testing.T` — we'll need a variant (`extractRootTypeName2`) that returns an empty string instead of calling `t.Fatal`
- Test data that is not a JSON object (primitives, arrays) is skipped in round-trip tests since our generator produces Go structs
- `optional/` subdirectories are excluded initially — can be added later
- Boolean schemas (`true` / `false` as entire schemas) will fail parsing and should be in known-failures
- The `refRemote.json` tests reference remote schemas that need a server — these should be in known-failures
