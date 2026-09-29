package roundtrip

import "github.com/mgilbir/schemagen/tests/internal/testsupport"

// The harness this package shares with the other test packages under tests/,
// under the names its tests were written against when they were one package.
// See tests/internal/testsupport.
var (
	backquote                    = testsupport.Backquote
	generateFromSchema           = testsupport.GenerateFromSchema
	generateFromSchemaWithConfig = testsupport.GenerateFromSchemaWithConfig
	generateRoundTripMain        = testsupport.GenerateRoundTripMain
	generateWithRootName         = testsupport.GenerateWithRootName
	programOutput                = testsupport.ProgramOutput
	runEngineProgram             = testsupport.RunEngineProgram
	runSchemagen                 = testsupport.RunSchemagen
	schemagenBinary              = testsupport.SchemagenBinary
	writeCrossFile               = testsupport.WriteCrossFile
	writeSharedHelpers           = testsupport.WriteSharedHelpers
	writeTestGoMod               = testsupport.WriteTestGoMod
)
