package validation

import "github.com/mgilbir/schemagen/tests/internal/testsupport"

// The harness this package shares with the other test packages under tests/,
// under the names its tests were written against when they were one package.
// See tests/internal/testsupport.
type (
	notFixture  = testsupport.NotFixture
	notInstance = testsupport.NotInstance
)

var (
	generateFromSchema            = testsupport.GenerateFromSchema
	generateFromSchemaWithConfig  = testsupport.GenerateFromSchemaWithConfig
	goQuote                       = testsupport.GoQuote
	notInstanceMain               = testsupport.NotInstanceMain
	programOutput                 = testsupport.ProgramOutput
	runInstanceFixtures           = testsupport.RunInstanceFixtures
	runInstanceFixturesWithConfig = testsupport.RunInstanceFixturesWithConfig
	writeCogenGoMod               = testsupport.WriteCogenGoMod
	writeSharedHelpers            = testsupport.WriteSharedHelpers
	writeTestGoMod                = testsupport.WriteTestGoMod
)
