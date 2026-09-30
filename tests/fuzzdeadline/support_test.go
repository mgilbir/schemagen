package fuzzdeadline

import "github.com/mgilbir/schemagen/tests/internal/testsupport"

// The harness this package shares with the other test packages under tests/,
// under the names its tests were written against when they were one package.
// See tests/internal/testsupport.
var (
	fuzzSeedCorpus  = testsupport.SeedCorpus
	fuzzSeedCfgBits = testsupport.FuzzSeedCfgBits
	fuzzOnce        = testsupport.FuzzOnce
)
