package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
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

// metaSchemaDir holds the JSON Schema meta-schemas, fetched by
// "make download-metaschemas". The suite refers to them but does not ship them.
const metaSchemaDir = "../testdata/external/metaschemas"

// Module metadata for the temp go.mod files the harnesses write.
//
// These are the modules *generated code* imports: the ECMA-262 engine for
// `pattern` and `format: regex`, and x/net/idna for the two hostname formats
// (which pulls x/text). They are pinned here as well as in the repository's own
// go.mod because the harness builds a throwaway module per test group, and a
// module with no go.sum entry cannot build offline.
const (
	goecma262Version = "v0.0.0-20260219184840-8bfa4bb752b0"
	goecma262H1      = "h1:g5uVjex1bABu72M6R0A//gQDoVXPSatqP50yZDX5wUQ="
	goecma262GoMod   = "h1:wQvOAFchLrhVSiF4JsSzH+yE6eLpc8gOBrvpuahNucI="

	// Pinned to the newest pair whose own go directive is 1.23, which is what
	// the throwaway modules below declare and what generated code should not
	// need more than. A later x/net raises the floor to Go 1.25 and would make
	// that the requirement for anyone whose schema names a hostname; the idna
	// behaviour is identical across the range, measured against this corpus.
	xnetVersion = "v0.38.0"
	xnetH1      = "h1:vRMAPTMaeGqVhG5QyLJHqNDwecKTomGeqbnfZyKlBI8="
	xnetGoMod   = "h1:ivrbrMbzFq5J41QOQh0siUuly180yBYtLp+CKbEaFx8="

	xtextVersion = "v0.24.0"
	xtextH1      = "h1:dd5Bzh4yt5KYA8f9CJHCP4FB4D51c2c6JvN37xJJkJ0="
	xtextGoMod   = "h1:L8rBsPeo2pSS+xqN0d5u2ikmjtmoJbDBT1b7nHvFCdU="
)

// writeTestGoMod writes a go.mod and go.sum in dir naming every module the
// generated code may import. moduleName is the module name for the temp project
// (e.g. "compile_test", "roundtrip_test").
//
// All three are required unconditionally rather than by inspecting the emitted
// source: an unused require is harmless, a missing one is a build failure in a
// throwaway module nobody will read the go.mod of.
func writeTestGoMod(dir, moduleName string) error {
	// x/text is indirect: nothing generated imports it, but x/net/idna does, and
	// a module that names only its direct requirements does not build offline.
	goMod := fmt.Sprintf("module %s\n\ngo 1.23.0\n\nrequire (\n\tgithub.com/mgilbir/goecma262 %s\n\tgolang.org/x/net %s\n)\n\nrequire golang.org/x/text %s // indirect\n",
		moduleName, goecma262Version, xnetVersion, xtextVersion)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return fmt.Errorf("write go.mod: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(testGoSum()), 0o644); err != nil {
		return fmt.Errorf("write go.sum: %w", err)
	}
	return nil
}

// testGoSum is the go.sum body naming every module writeTestGoMod requires.
func testGoSum() string {
	return fmt.Sprintf(
		"github.com/mgilbir/goecma262 %s %s\ngithub.com/mgilbir/goecma262 %s/go.mod %s\n"+
			"golang.org/x/net %s %s\ngolang.org/x/net %s/go.mod %s\n"+
			"golang.org/x/text %s %s\ngolang.org/x/text %s/go.mod %s\n",
		goecma262Version, goecma262H1, goecma262Version, goecma262GoMod,
		xnetVersion, xnetH1, xnetVersion, xnetGoMod,
		xtextVersion, xtextH1, xtextVersion, xtextGoMod)
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

// loadMetaSchemas reads the downloaded meta-schemas, keyed by the $id each one
// declares rather than by its filename. The download URL and the URI a $ref
// resolves to are then the same string by construction, so there is no
// path-to-URI table to drift. Drafts before 2019-09 spell it "id", and several
// carry a trailing "#" that MappingResolver strips before lookup.
func loadMetaSchemas(t *testing.T) map[string]*schema.Schema {
	t.Helper()
	schemas := make(map[string]*schema.Schema)
	entries, err := os.ReadDir(metaSchemaDir)
	if err != nil {
		return schemas
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(metaSchemaDir, e.Name()))
		if err != nil {
			t.Fatalf("reading meta-schema %s: %v", e.Name(), err)
		}
		var s schema.Schema
		if err := json.Unmarshal(data, &s); err != nil {
			t.Fatalf("parsing meta-schema %s: %v", e.Name(), err)
		}
		s.Normalize()
		id := s.ID
		if id == "" {
			id = s.LegacyID
		}
		if id == "" {
			t.Fatalf("meta-schema %s declares no $id", e.Name())
		}
		schemas[strings.TrimSuffix(id, "#")] = &s
	}
	return schemas
}

// remotesResolver returns a SchemaResolver for the test suite's remote schemas
// and the meta-schemas they reference.
//
// It fails rather than returning nil when nothing loads. requireTestSuite has
// already established that the suite is on disk, so an empty remotes directory
// means the checkout is damaged — and with strict refs a nil resolver turns
// every remote $ref into a code-generation failure, which TestExternalValidation
// counts as a skip. A hundred groups would quietly stop being validated and the
// run would still be green.
func remotesResolver(t *testing.T) schema.SchemaResolver {
	t.Helper()
	schemas := loadRemoteSchemas(t)
	if len(schemas) == 0 {
		t.Fatalf("no remote schemas loaded from %s; the suite checkout is incomplete", jstsRemotesDir)
	}
	metas := loadMetaSchemas(t)
	if len(metas) == 0 {
		t.Fatalf("no meta-schemas loaded from %s; run 'make download-metaschemas'", metaSchemaDir)
	}
	for uri, s := range metas {
		schemas[uri] = s
	}
	return schema.NewMappingResolver(schemas)
}

// requireTestSuite gates the external tests on the corpus being present.
//
// The unset environment variable is a skip: nobody asked for these tests, and
// `go test ./...` must stay usable without a 6 MB checkout. Everything after it
// is a failure, because SCHEMAGEN_RUN_EXTERNAL=1 *is* the request. Skipping a
// requested run reports a green pass having validated nothing, which is the same
// silence this file's coverage gate exists to end — and it is reachable, since
// the variable can be set by hand without going through `make test-external`
// (which depends on both download targets and so is always safe).
func requireTestSuite(t *testing.T) {
	t.Helper()
	if os.Getenv("SCHEMAGEN_RUN_EXTERNAL") != "1" {
		t.Skip("external JSON Schema Test Suite disabled; set SCHEMAGEN_RUN_EXTERNAL=1 or run make test-external")
	}
	if _, err := os.Stat(jstsBaseDir); err != nil {
		t.Fatalf("SCHEMAGEN_RUN_EXTERNAL=1 but the JSON Schema Test Suite is not at %s (%v). Run 'make download-test-suite', or 'make test-external' which does it for you.", jstsBaseDir, err)
	}
	// The suite refers to meta-schemas it does not ship. Without them a large
	// batch of refs cannot resolve, which would look like a wave of real
	// failures rather than a missing prerequisite.
	if _, err := os.Stat(metaSchemaDir); err != nil {
		t.Fatalf("SCHEMAGEN_RUN_EXTERNAL=1 but the meta-schemas are not at %s (%v). Run 'make download-metaschemas', or 'make test-external' which does it for you.", metaSchemaDir, err)
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

// checkUnvalidatedRejection is checkKnownFailure's counterpart for a group that
// produced no Validate() method while the suite marks one of its documents
// invalid. There is no per-case outcome to judge here — the defect is the
// absence itself — so the two arms are "allow-listed" and "not".
//
//   - allow-listed → t.Skipf (a recorded, outstanding gap)
//   - not allow-listed → t.Errorf (a provable defect, or a regression)
//
// The other direction, an allow-list entry that no longer describes anything,
// is checked once at the end of the run by reportStaleUnvalidated: it needs the
// whole corpus walked before it can tell a fixed group from a vanished one.
func checkUnvalidatedRejection(t *testing.T, key string) {
	t.Helper()
	if reason, ok := knownUnvalidatedRejections[key]; ok {
		t.Skipf("known gap: no Validate() produced for a group the suite says must reject a document (reason: %s)", reason)
		return
	}
	t.Errorf("no Validate() method was produced, but the suite marks at least one document in this group invalid — "+
		"the generated code accepts a document that must be rejected\n"+
		"  key: %s\n"+
		"  if this is a genuine outstanding gap rather than a regression, add the key to knownUnvalidatedRejections", key)
}

// groupFate records what became of every code-gen-suitable group in a run, so
// that a stale knownUnvalidatedRejections entry can be reported with the reason
// it went stale rather than a bare "unused".
type groupFate int

const (
	fateUnvalidated  groupFate = iota // no Validate(), and a document must be rejected — the allow-listed state
	fateValidated                     // a Validate() was produced and its cases ran
	fateNoRejection                   // no Validate(), but no document has to be rejected either
	fateCodeGenError                  // code generation failed; TestExternalCodeGen owns this one
)

// reportStaleUnvalidated errors on every knownUnvalidatedRejections entry that
// did not describe a group in this run. A stale entry is the same class of lie
// as the silent skip the list replaced: it reads as "this is still broken" while
// suppressing nothing, and it hides the next regression that lands on that key.
func reportStaleUnvalidated(t *testing.T, fates map[string]groupFate) {
	t.Helper()
	keys := make([]string, 0, len(knownUnvalidatedRejections))
	for key := range knownUnvalidatedRejections {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fate, seen := fates[key]
		if seen && fate == fateUnvalidated {
			continue
		}
		switch {
		case !seen:
			t.Errorf("stale allow-list entry: %q matches no group in the test suite — "+
				"it was renamed or removed upstream; delete the key from knownUnvalidatedRejections", key)
		case fate == fateValidated:
			t.Errorf("stale allow-list entry: %q now produces a Validate() method and its cases are being tested — "+
				"delete the key from knownUnvalidatedRejections", key)
		case fate == fateNoRejection:
			t.Errorf("stale allow-list entry: %q no longer carries a document the suite marks invalid, "+
				"so nothing about it is provable — delete the key from knownUnvalidatedRejections", key)
		case fate == fateCodeGenError:
			t.Errorf("stale allow-list entry: %q now fails code generation outright rather than producing "+
				"a Validate()-less type; that is a code-generation regression (see TestExternalCodeGen), "+
				"and the key no longer belongs in knownUnvalidatedRejections", key)
		}
	}
}

// countCodeGenSuitableGroups walks the corpus and counts the groups
// TestExternalValidation would visit if nothing filtered it.
//
// The aggregate gates at the end of that test — the coverage floor and the
// allow-list staleness sweep — are statements about the whole corpus, and a
// `go test -run TestExternalValidation/draft3` run would fail both of them for
// no reason at all: five drafts never walked reads identically to five drafts
// that stopped producing Validate(). Comparing the visited count against this
// one distinguishes the two, so a filtered run says so and stays quiet rather
// than crying wolf.
func countCodeGenSuitableGroups(t *testing.T) int {
	t.Helper()
	var n int
	for _, draft := range allDrafts {
		draftDir := filepath.Join(jstsBaseDir, draft)
		if _, err := os.Stat(draftDir); os.IsNotExist(err) {
			continue
		}
		for _, file := range listJSONFiles(t, draftDir) {
			for _, group := range loadTestGroups(t, filepath.Join(draftDir, file)) {
				if isCodeGenSuitable(group.Schema) {
					n++
				}
			}
		}
	}
	return n
}

// groupHasRejectingCase reports whether the suite marks any document in the
// group invalid. Without one there is nothing to prove: a group of accept-only
// cases against a type carrying no Validate() is uninformative, not wrong.
func groupHasRejectingCase(group jstsTestGroup) bool {
	for _, tc := range group.Tests {
		if !tc.Valid {
			return true
		}
	}
	return false
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

// isBignumFile returns true if the file path is for tests that require BigIntSupport.
// This includes optional/bignum.json (arbitrary-precision integers) and
// optional/float-overflow.json (1e308 overflows int64 but fits in big.Int).
func isBignumFile(file string) bool {
	return strings.Contains(file, "optional/bignum") || strings.Contains(file, "optional/float-overflow")
}

// isFormatAssertionFile reports whether a file describes the behaviour of an
// implementation that asserts "format", and so has to be generated with
// FormatAssertion set.
//
// The suite states both postures and keeps them apart by directory. The
// non-optional format.json says what a format means by default: from 2019-09
// the format-annotation vocabulary makes it an annotation, so "2962" satisfies
// {"format":"email"} and every one of those cases is marked valid.
// optional/format/*.json says what the assertion means for an implementation
// that opts in, and marks the same document invalid. Neither is wrong; they
// describe different configurations, which is what "optional" means here.
//
// Running the second set under the default configuration would therefore ask
// this generator to fail: it would have to reject a document its own dialect
// permits in order to pass, and rejecting what the schema permits is the one
// thing this repository does not trade away. Running it under the flag asks the
// question the file is actually about -- how accurate the checks are -- which is
// what the file can answer. This is the same per-file configuration switch
// isBignumFile already makes, for the same reason.
func isFormatAssertionFile(file string) bool {
	return strings.Contains(filepath.ToSlash(file), "optional/format")
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
	if trimmed == "true" {
		// Boolean true schema accepts everything — no validation possible.
		return false
	}
	if trimmed == "false" {
		// Boolean false schema rejects everything — generates a forbidden type.
		return true
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

	// The generator always names the root type "Root". Check for it first
	// across all type declarations (struct, alias, defined type).
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "type Root ") {
			return "Root"
		}
	}

	// Fallback: find the last struct with JSON-tagged fields.
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
		var lastAlias string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "type ") && !strings.Contains(trimmed, " struct {") && !strings.Contains(trimmed, " interface {") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 3 {
					lastAlias = parts[1]
				}
			}
		}
		lastType = lastAlias
	}

	return lastType
}

// ephemeralCacheDir holds a per-process temporary GOCACHE directory that is
// cleaned up at process exit. Using a shared ephemeral cache (instead of the
// user's persistent ~/.cache/go-build) prevents the ~14,000 unique compilations
// from bloating the build cache by hundreds of gigabytes.
var ephemeralCacheDir string

func init() {
	dir, err := os.MkdirTemp("", "schemagen-gocache-*")
	if err != nil {
		panic(fmt.Sprintf("creating ephemeral GOCACHE: %v", err))
	}
	ephemeralCacheDir = dir
}

// TestMain cleans up the ephemeral cache directory after all tests finish.
func TestMain(m *testing.M) {
	code := m.Run()
	if ephemeralCacheDir != "" {
		os.RemoveAll(ephemeralCacheDir)
	}
	os.Exit(code)
}

// ephemeralCacheEnv returns a copy of the current environment with GOCACHE
// pointed at an ephemeral temporary directory. This prevents external go
// build/run invocations from bloating the user's persistent build cache —
// each test compiles unique generated code that will never be reused.
func ephemeralCacheEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GOCACHE=") {
			env = append(env, e)
		}
	}
	return append(env, "GOCACHE="+ephemeralCacheDir)
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
func tryGenerateAndCompile(schemaJSON json.RawMessage, resolver schema.SchemaResolver, bigInt bool) error {
	var s schema.Schema
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	s.Normalize()

	// Refs are strict here, matching the CLI default: a ref no resolver can
	// serve fails generation rather than degrading to any. Leniency let a
	// schema the harness had silently emptied out still count as a pass, so
	// the suite measured a validator built from a schema nobody wrote.
	cfg := generator.Config{PackageName: "testpkg", OmitEmpty: true, Resolver: resolver, BigIntSupport: bigInt}
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
	if err := writeSharedHelpersErr(tmpDir, content); err != nil {
		return fmt.Errorf("write helpers: %w", err)
	}
	if err := writeTestGoMod(tmpDir, "compile_test"); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", ".")
	cmd.Dir = tmpDir
	cmd.Env = ephemeralCacheEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("compile: %s\n%s", err, string(output))
	}
	return nil
}

// tryRoundTrip attempts the full round-trip: parse → generate → compile → unmarshal → marshal → compare.
func tryRoundTrip(schemaJSON, dataJSON json.RawMessage, resolver schema.SchemaResolver, bigInt bool) error {
	var s schema.Schema
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	s.Normalize()

	// Refs are strict here, matching the CLI default: a ref no resolver can
	// serve fails generation rather than degrading to any. Leniency let a
	// schema the harness had silently emptied out still count as a pass, so
	// the suite measured a validator built from a schema nobody wrote.
	cfg := generator.Config{PackageName: "testpkg", OmitEmpty: true, Resolver: resolver, BigIntSupport: bigInt}
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
	if err := writeSharedHelpersErr(tmpDir, mainContent); err != nil {
		return fmt.Errorf("write helpers: %w", err)
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
	cmd.Env = ephemeralCacheEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("round-trip: %s\n%s", err, string(output))
	}
	if programOutput(output) != "PASS" {
		return fmt.Errorf("round-trip mismatch:\n%s", string(output))
	}
	return nil
}

// trivialRootValidate matches a root Validate() whose entire body is
// "return nil" — a method that exists, satisfies hasValidateMethod, and checks
// nothing. The receiver name and pointer-ness are left open; everything else is
// pinned, because the point of the pattern is that the body has no statements
// but the return.
var trivialRootValidate = regexp.MustCompile(`(?m)^func \([A-Za-z_]\w* \*?(\w+)\) Validate\(\) error \{\n\treturn nil\n\}`)

// rootValidateHasNoChecks reports whether the root type's Validate() body is
// exactly "return nil".
//
// Such a group counts as tested, and its rejecting cases pass, but they pass on
// the decoder: the document never fit the Go type. Often that is legitimate and
// complete — {"type":"integer"} becomes `type Root int64`, which genuinely
// cannot hold "foo" — but it is not what "Validate() correctness" sounds like,
// and a change that widened the Go type would turn those rejections into
// unnoticed acceptances. The count is reported so the distinction is visible.
func rootValidateHasNoChecks(code string) bool {
	rootType := extractRootTypeNameFromCode(code)
	if rootType == "" {
		return false
	}
	for _, m := range trivialRootValidate.FindAllStringSubmatch(code, -1) {
		if m[1] == rootType {
			return true
		}
	}
	return false
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

// validationVerdict is how a generated program judged a document: accepted, or
// refused — and if refused, by which of the two mechanisms.
//
// Both refusals are real refusals as far as a caller who decodes JSON is
// concerned, so the suite treats them alike when deciding pass or fail. They are
// counted apart because they prove different things, and one of them proves less
// than the test's name suggests: a document refused by the decoder was refused
// by the shape of the Go type, not by any rule the generator emitted.
type validationVerdict int

const (
	verdictMissing          validationVerdict = iota // the program printed nothing recognisable
	verdictValid                                     // decoded, and Validate() returned nil
	verdictDecodeRejected                            // json.Unmarshal refused it; Validate() never ran
	verdictValidateRejected                          // decoded, and Validate() returned an error
)

// generateValidateMain creates a Go main() that:
// 1. Reads fixture.json
// 2. Unmarshals into the generated type
// 3. Calls Validate()
// 4. Prints "VALID", "UNMARSHAL: <message>", or "INVALID: <message>"
//
// The two refusals print distinct tokens rather than a shared "INVALID:" with a
// prefix to disambiguate, matching the vocabulary the cogen harness already uses
// — a Validate() error is free to begin with any words at all, so a prefix test
// on a shared token would be a guess.
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
		// validation failure (wrong type, missing required field, etc.),
		// but it is the Go type refusing the document rather than a rule
		// the generator emitted, so it reports itself separately.
		fmt.Printf("UNMARSHAL: %%v\n", err)
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
func tryGenerateWithValidation(schemaJSON json.RawMessage, resolver schema.SchemaResolver, draft schema.Draft, bigInt, formatAssertion bool) (string, error) {
	var s schema.Schema
	// Handle boolean false schema: "false" is not a JSON object, so we construct
	// the Schema struct manually with BooleanSchema set to false.
	trimmed := strings.TrimSpace(string(schemaJSON))
	if trimmed == "false" {
		boolVal := false
		s.BooleanSchema = &boolVal
	} else if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	s.Normalize()

	// Refs are strict here, matching the CLI default: a ref no resolver can
	// serve fails generation rather than degrading to any. Leniency let a
	// schema the harness had silently emptied out still count as a pass, so
	// the suite measured a validator built from a schema nobody wrote.
	cfg := generator.Config{PackageName: "testpkg", OmitEmpty: true, Resolver: resolver, Draft: draft, BigIntSupport: bigInt, FormatAssertion: formatAssertion}
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
//
// It returns the verdict alongside the pass/fail error so the caller can count
// decode refusals apart from Validate() refusals. The verdict is meaningful even
// when the error is non-nil (that is the "wrong verdict" case); it is
// verdictMissing when the program could not be built or run at all.
func tryValidation(code string, dataJSON json.RawMessage, expectValid bool) (validationVerdict, error) {
	rootType := extractRootTypeNameFromCode(code)
	if rootType == "" {
		return verdictMissing, fmt.Errorf("could not find root type in generated code")
	}

	tmpDir, err := os.MkdirTemp("", "schemagen-val-*")
	if err != nil {
		return verdictMissing, fmt.Errorf("tmpdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	mainContent := strings.Replace(code, "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(mainContent), 0o644); err != nil {
		return verdictMissing, fmt.Errorf("write types: %w", err)
	}
	if err := writeSharedHelpersErr(tmpDir, mainContent); err != nil {
		return verdictMissing, fmt.Errorf("write helpers: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "fixture.json"), dataJSON, 0o644); err != nil {
		return verdictMissing, fmt.Errorf("write fixture: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(generateValidateMain(rootType)), 0o644); err != nil {
		return verdictMissing, fmt.Errorf("write main: %w", err)
	}
	if err := writeTestGoMod(tmpDir, "validate_test"); err != nil {
		return verdictMissing, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	cmd.Env = ephemeralCacheEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return verdictMissing, fmt.Errorf("run: %s\n%s", err, string(output))
	}

	result := programOutput(output)
	var verdict validationVerdict
	switch {
	case result == "VALID":
		verdict = verdictValid
	case strings.HasPrefix(result, "UNMARSHAL:"):
		verdict = verdictDecodeRejected
	case strings.HasPrefix(result, "INVALID:"):
		verdict = verdictValidateRejected
	default:
		return verdictMissing, fmt.Errorf("generated program reported no verdict: %s", result)
	}

	if expectValid {
		if verdict != verdictValid {
			return verdict, fmt.Errorf("expected VALID but got: %s", result)
		}
	} else {
		if verdict == verdictValid {
			return verdict, fmt.Errorf("expected the document to be rejected but got: %s", result)
		}
	}
	return verdict, nil
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
							err := tryGenerateAndCompile(group.Schema, resolver, isBignumFile(file))
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
									err := tryRoundTrip(group.Schema, tc.Data, resolver, isBignumFile(file))
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

// minValidatedGroups is the number of test groups this test reached a generated
// Validate() for, measured on 2026-08-04 against suite commit bce6a47: 1586 of
// the 1799 code-gen-suitable groups (1803 groups in the corpus, less the 4 whose
// schema is boolean `true`, which asserts nothing and so has nothing to test).
//
// It was 1494 when this gate was written, one commit earlier. Compiling the
// schemas that used to become `type X any` to the runtime evaluator raised it by
// 92, and the 43 knownUnvalidatedRejections entries those groups held were
// deleted -- which is the mechanism working: each one failed by name, saying it
// now produces a Validate(), rather than sitting in the list unread.
//
// It is a floor, not a target. A group that produces no Validate() produces no
// subtest either, so a change that stopped generating one would remove tests
// from the run rather than fail any — the run would get greener as it measured
// less. Falling below this number fails.
//
// Rising above it does not fail, only logs: coverage improves constantly, and
// every improvement on a group that matters — one carrying a document the suite
// says must be rejected — already fails loudly through its stale
// knownUnvalidatedRejections entry. Two failures for one event would train
// people to bump numbers rather than read them.
const minValidatedGroups = 1586

// TestExternalValidation tests that generated Validate() methods correctly accept
// valid data and reject invalid data according to the JSON Schema.
//
// Test structure:
//   - Schemas that fail code generation are skipped (TestExternalCodeGen owns
//     them) and counted.
//   - Schemas that produce no Validate() method are counted, and — when the
//     suite marks one of their documents invalid — reported as a failure unless
//     the group is allow-listed in knownUnvalidatedRejections. "No Validate()"
//     plus "this document must be rejected" means the generated code accepts a
//     document that must not be accepted, which is a defect whether or not
//     anyone has got around to it.
//   - Schemas that DO produce a Validate() method are tested per-case, with
//     both valid and invalid data. Only these per-case results use the
//     knownValidationFailures bidirectional checking.
//
// The closing summary asserts the coverage floor and reports how the corpus's
// must-reject documents were actually refused — by the decoder or by Validate().
func TestExternalValidation(t *testing.T) {
	requireTestSuite(t)
	resolver := remotesResolver(t)

	var totalGroups, skippedCG, skippedNoValidate, testedGroups int
	var noChecksGroups, noChecksWithRejection int
	var rejectedAtDecode, rejectedByValidate int
	// fates records what became of every group, so a knownUnvalidatedRejections
	// entry that describes none of them can say why it went stale.
	fates := make(map[string]groupFate, 2048)

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
						groupKey := failureKey(draft, filenameWithoutExt(file), group.Description)

						// Generate code once per group.
						code, cgErr := tryGenerateWithValidation(group.Schema, resolver, draftFromDir(draft), isBignumFile(file), isFormatAssertionFile(file))

						if cgErr != nil {
							skippedCG++
							fates[groupKey] = fateCodeGenError
							continue
						}
						if code == "" {
							skippedNoValidate++
							if !groupHasRejectingCase(group) {
								// Nothing is provable: every document here is
								// meant to be accepted, and a type with no
								// Validate() accepts everything.
								fates[groupKey] = fateNoRejection
								continue
							}
							fates[groupKey] = fateUnvalidated
							t.Run(group.Description, func(t *testing.T) {
								checkUnvalidatedRejection(t, groupKey)
							})
							continue
						}

						testedGroups++
						fates[groupKey] = fateValidated
						if rootValidateHasNoChecks(code) {
							noChecksGroups++
							if groupHasRejectingCase(group) {
								noChecksWithRejection++
							}
						}
						t.Run(group.Description, func(t *testing.T) {
							for _, tc := range group.Tests {
								t.Run(tc.Description, func(t *testing.T) {
									key := failureKey(draft, filenameWithoutExt(file), group.Description, tc.Description)
									verdict, err := tryValidation(code, tc.Data, tc.Valid)
									if !tc.Valid {
										switch verdict {
										case verdictDecodeRejected:
											rejectedAtDecode++
										case verdictValidateRejected:
											rejectedByValidate++
										}
									}
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

	// How the corpus's must-reject documents were actually refused. A decode
	// refusal is a real refusal — a caller decoding JSON gets an error — but it
	// is the Go type refusing, not a rule the generator emitted, so it proves
	// less than the name of this test suggests. It is reported rather than
	// failed: demanding that Validate() do the refusing would be demanding an
	// implementation, and `type Root int64` refusing "foo" is correct and
	// complete. The risk is drift, and drift is already red — widening such a
	// type turns the refusal into an acceptance, which fails the case outright.
	t.Logf("Rejections: %d by Validate(), %d at decode (json.Unmarshal refused the document; Validate() never ran)",
		rejectedByValidate, rejectedAtDecode)
	t.Logf("Of the %d tested groups, %d have a root Validate() whose body is exactly `return nil`, %d of those with a document the suite marks invalid",
		testedGroups, noChecksGroups, noChecksWithRejection)

	// The two gates below judge the whole corpus, so they are meaningless on a
	// run that only walked part of it.
	if corpusGroups := countCodeGenSuitableGroups(t); totalGroups != corpusGroups {
		t.Logf("partial run: walked %d of the corpus's %d code-gen-suitable groups (a -run filter on the subtests?). "+
			"The coverage floor and the knownUnvalidatedRejections staleness sweep judge the whole corpus and are skipped; "+
			"run without a subtest filter, or via 'make test-external', to exercise them",
			totalGroups, corpusGroups)
		return
	}

	if testedGroups < minValidatedGroups {
		t.Errorf("validation coverage regressed from %d to %d groups (of %d code-gen-suitable groups): "+
			"%d fewer groups produced a Validate() method, so their cases were never run. "+
			"Fix the regression, or — if the corpus itself shrank — re-measure and lower minValidatedGroups",
			minValidatedGroups, testedGroups, totalGroups, minValidatedGroups-testedGroups)
	} else if testedGroups > minValidatedGroups {
		t.Logf("validation coverage improved from %d to %d groups — raise minValidatedGroups to %d",
			minValidatedGroups, testedGroups, testedGroups)
	}

	reportStaleUnvalidated(t, fates)
}
