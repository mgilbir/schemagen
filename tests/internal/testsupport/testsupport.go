// Package testsupport is the harness the test packages under tests/ share:
// writing and running the throwaway modules generated code is compiled in, the
// generation pipeline as a test drives it, the fixture runner that puts
// documents to a compiled type, and the corpora the sweeps walk.
//
// It exists because those tests used to be one package, tests, whose single
// test binary grew to within reach of go test's default ten-minute timeout: on
// a GitHub runner it was killed at 600s, mid-run, while every other package in
// the module finished in under 95s. The fix was to split it by area into
// packages that go test runs as separate binaries in parallel -- not to raise
// the timeout, which is the control that turned a run nobody was watching into
// a failure somebody saw. What more than one of those packages needs lives
// here; what only one needs stays in that package.
//
// It sits under tests/internal so that nothing outside tests/ can import it:
// it is test machinery, it imports testing, and it runs the go tool through
// internal/testgo. A test package that imports it therefore needs the TestMain
// every such package has,
//
//	func TestMain(m *testing.M) { testgo.Main(m) }
//
// which TestEveryTestBinaryThatRunsTheGoToolInstallsMain in internal/testgo
// holds every package in the module to.
package testsupport

import (
	"encoding/json"
	"path/filepath"
)

// Root is the repository root, relative to the directory a test package under
// tests/ runs in. go test runs a package's tests in the package's own
// directory, and every test package here sits exactly one level below tests/ --
// TestEveryTestPackageSitsOneLevelBelowTests holds the layout to that, since a
// package one level deeper would read every fixture from the wrong place.
//
// It is relative rather than absolute on purpose: the paths the tests report,
// and the corpus paths the refusal ledgers pin, are spelled from it.
const Root = "../.."

// RepoPath joins elem onto Root: the path, from a test package's directory, of
// a file named relative to the repository root.
func RepoPath(elem ...string) string {
	return filepath.Join(append([]string{Root}, elem...)...)
}

// SchemaDir holds the hand-written schemas: the golden and round-trip inputs,
// the regression fixtures, and the fuzz seed corpus.
const SchemaDir = Root + "/testdata/schemas"

// JSTSBaseDir is the path to the JSON Schema Test Suite tests directory. The
// suite is fetched by `make download-test-suite` and is optional everywhere but
// in the external conformance run.
const JSTSBaseDir = Root + "/testdata/external/JSON-Schema-Test-Suite/tests"

// JSTSTestGroup represents a single test group from the JSTS.
type JSTSTestGroup struct {
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	Tests       []JSTSTestCase  `json:"tests"`
}

// JSTSTestCase represents a single test case within a test group.
type JSTSTestCase struct {
	Description string          `json:"description"`
	Data        json.RawMessage `json:"data"`
	Valid       bool            `json:"valid"`
}
