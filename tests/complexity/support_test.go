package complexity

import "github.com/mgilbir/schemagen/tests/internal/testsupport"

// The harness this package shares with the other test packages under tests/,
// under the names its tests were written against when they were one package.
// See tests/internal/testsupport.
var (
	backquote       = testsupport.Backquote
	programOutput   = testsupport.ProgramOutput
	runSchemagen    = testsupport.RunSchemagen
	schemagenBinary = testsupport.SchemagenBinary
	writeCrossFile  = testsupport.WriteCrossFile
	writeTestGoMod  = testsupport.WriteTestGoMod
)
