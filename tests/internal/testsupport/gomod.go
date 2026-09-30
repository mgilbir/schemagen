package testsupport

import "github.com/mgilbir/schemagen/internal/testgo"

// Module metadata for the temp go.mod files the harnesses write.
//
// These are the modules *generated code* imports: the runtime module every
// generated package shares (replaced onto this checkout's ./runtime, so a test
// builds against the runtime being changed), the ECMA-262 engine it uses for
// `pattern` and `format: regex`, and x/net/idna for the two hostname formats
// (which pulls x/text). The pins and the go.mod/go.sum writers live in
// internal/testgo, which cmd/schemagen and pkg/emitter tests can reach too.
const (
	Goecma262Version = testgo.Goecma262Version
	Goecma262H1      = testgo.Goecma262H1
	Goecma262GoMod   = testgo.Goecma262GoMod
	XnetVersion      = testgo.XnetVersion
	XtextVersion     = testgo.XtextVersion
)

// WriteTestGoMod writes a go.mod and go.sum in dir naming every module the
// generated code may import. moduleName is the module name for the temp project
// (e.g. "compile_test", "roundtrip_test"). See testgo.WriteModule.
func WriteTestGoMod(dir, moduleName string) error {
	return testgo.WriteModule(dir, moduleName)
}

// TestGoSum is the go.sum body naming every module WriteTestGoMod requires.
func TestGoSum() string { return testgo.GoSum() }

// WriteCogenGoMod writes the throwaway module the co-generation sweeps build in.
// It used to stub pkg/validationruntime, the package hybrid and runtime output
// imported from this repository, because replacing onto the repository would
// have dragged schemagen's own go.mod -- cobra and everything else -- into the
// generated module's graph. Generated code no longer imports the repository: what
// it imports is the runtime module, which requires the engine and idna and
// nothing else, so the cogen module is any other throwaway module.
func WriteCogenGoMod(dir string) error {
	return testgo.WriteModule(dir, "cogen_test")
}
