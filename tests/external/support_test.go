package external

import "github.com/mgilbir/schemagen/tests/internal/testsupport"

// The harness this package shares with the other test packages under tests/,
// under the names its tests were written against when they were one package.
// See tests/internal/testsupport.
const jstsBaseDir = testsupport.JSTSBaseDir

type (
	jstsTestCase  = testsupport.JSTSTestCase
	jstsTestGroup = testsupport.JSTSTestGroup
)

var (
	extractRootTypeNameFromCode   = testsupport.ExtractRootTypeNameFromCode
	generateRoundTripMain         = testsupport.GenerateRoundTripMain
	generateRoundTripMainChecking = testsupport.GenerateRoundTripMainChecking
	hasValidateMethod             = testsupport.HasValidateMethod
	identityCheckSource           = testsupport.IdentityCheckSource
	programOutput                 = testsupport.ProgramOutput
	writeSharedHelpersErr         = testsupport.WriteSharedHelpersErr
	writeTestGoMod                = testsupport.WriteTestGoMod
)
