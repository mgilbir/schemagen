package names

import "github.com/mgilbir/schemagen/tests/internal/testsupport"

// The harness this package shares with the other test packages under tests/,
// under the names its tests were written against when they were one package.
// See tests/internal/testsupport.
type notInstance = testsupport.NotInstance

var (
	goQuote            = testsupport.GoQuote
	programOutput      = testsupport.ProgramOutput
	writeCogenGoMod    = testsupport.WriteCogenGoMod
	writeSharedHelpers = testsupport.WriteSharedHelpers
	writeTestGoMod     = testsupport.WriteTestGoMod
)
