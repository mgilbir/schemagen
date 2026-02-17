package tests

// Known failures for external JSON Schema Test Suite tests.
// Updated after *[]T pointer-to-slice fix for array omitempty round-trip.
// CodeGen: 2 known failures
// RoundTrip: 4 known failures (down from 16, after array pointer fix)

var knownParseFailures = map[string]string{}

var knownCodeGenFailures = map[string]string{
	// --- draft2019-09 ---
	"draft2019-09/vocabulary/schema that uses custom metaschema with with no validation vocabulary": "compile: generated code does not compile",
	// --- draft2020-12 ---
	"draft2020-12/vocabulary/schema that uses custom metaschema with with no validation vocabulary": "compile: generated code does not compile",
}

var knownRoundTripFailures = map[string]string{
	// --- draft2019-09 ---
	"draft2019-09/vocabulary/schema that uses custom metaschema with with no validation vocabulary/no validation: invalid number, but it still validates": "custom vocabulary metaschema",
	"draft2019-09/vocabulary/schema that uses custom metaschema with with no validation vocabulary/no validation: valid number":                           "custom vocabulary metaschema",
	// --- draft2020-12 ---
	"draft2020-12/vocabulary/schema that uses custom metaschema with with no validation vocabulary/no validation: invalid number, but it still validates": "custom vocabulary metaschema",
	"draft2020-12/vocabulary/schema that uses custom metaschema with with no validation vocabulary/no validation: valid number":                           "custom vocabulary metaschema",
}
