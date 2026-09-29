package corpus

import "github.com/mgilbir/schemagen/tests/internal/testsupport"

// The harness this package shares with the other test packages under tests/,
// under the names its tests were written against when they were one package.
// See tests/internal/testsupport.
const fuzzSchemaDir = testsupport.SchemaDir

var (
	corpusSchemaPaths           = testsupport.CorpusSchemaPaths
	extractRootTypeNameFromCode = testsupport.ExtractRootTypeNameFromCode
	programOutput               = testsupport.ProgramOutput
	writeTestGoMod              = testsupport.WriteTestGoMod
)
