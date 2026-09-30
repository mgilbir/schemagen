package fuzz

import "github.com/mgilbir/schemagen/tests/internal/testsupport"

// The harness this package shares with the other test packages under tests/,
// under the names its tests were written against when they were one package.
// See tests/internal/testsupport.
const (
	fuzzSchemaDir = testsupport.SchemaDir
	jstsBaseDir   = testsupport.JSTSBaseDir
)

var (
	corpusSchemaPaths = testsupport.CorpusSchemaPaths
	fuzzSeedCorpus    = testsupport.SeedCorpus
	fuzzSeedCfgBits   = testsupport.FuzzSeedCfgBits
	fuzzConfig        = testsupport.FuzzConfig
	fuzzOnce          = testsupport.FuzzOnce
)
