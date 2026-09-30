package patterns

import "github.com/mgilbir/schemagen/tests/internal/testsupport"

// The harness this package shares with the other test packages under tests/,
// under the names its tests were written against when they were one package.
// See tests/internal/testsupport.
const (
	goecma262Version = testsupport.Goecma262Version
	goecma262H1      = testsupport.Goecma262H1
	goecma262GoMod   = testsupport.Goecma262GoMod
)

var (
	programOutput   = testsupport.ProgramOutput
	writeCogenGoMod = testsupport.WriteCogenGoMod
)
