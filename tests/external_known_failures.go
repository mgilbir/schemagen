package tests

// Known failures for external JSON Schema Test Suite tests.
// Updated after remote ref resolution, anyOf null+single-variant, and alias round-trip fixes.
// CodeGen: 2 known failures
// RoundTrip: 16 known failures (down from 35, after remote ref and alias round-trip fixes)

var knownParseFailures = map[string]string{}

var knownCodeGenFailures = map[string]string{
	// --- draft2019-09 ---
	"draft2019-09/vocabulary/schema that uses custom metaschema with with no validation vocabulary": "compile: generated code does not compile",
	// --- draft2020-12 ---
	"draft2020-12/vocabulary/schema that uses custom metaschema with with no validation vocabulary": "compile: generated code does not compile",
}

var knownRoundTripFailures = map[string]string{
	// --- draft2019-09 ---
	"draft2019-09/properties/properties, patternProperties, additionalProperties interaction/additionalProperty ignores property": "round-trip: array omitempty drops empty []",
	"draft2019-09/ref/ref applies alongside sibling keywords/ref valid, maxItems valid":                                           "ref alongside sibling keywords",

	"draft2019-09/vocabulary/schema that uses custom metaschema with with no validation vocabulary/no validation: invalid number, but it still validates": "custom vocabulary metaschema",
	"draft2019-09/vocabulary/schema that uses custom metaschema with with no validation vocabulary/no validation: valid number":                           "custom vocabulary metaschema",
	// --- draft2020-12 ---
	"draft2020-12/properties/properties, patternProperties, additionalProperties interaction/additionalProperty ignores property": "round-trip: array omitempty drops empty []",
	"draft2020-12/ref/ref applies alongside sibling keywords/ref valid, maxItems valid":                                           "ref alongside sibling keywords",

	"draft2020-12/vocabulary/schema that uses custom metaschema with with no validation vocabulary/no validation: invalid number, but it still validates": "custom vocabulary metaschema",
	"draft2020-12/vocabulary/schema that uses custom metaschema with with no validation vocabulary/no validation: valid number":                           "custom vocabulary metaschema",
	// --- draft3 ---
	"draft3/properties/properties, patternProperties, additionalProperties interaction/additionalProperty ignores property": "round-trip: array omitempty drops empty []",
	"draft3/ref/ref overrides any sibling keywords/remote ref valid":                                                        "ref overrides sibling keywords",

	// --- draft4 ---
	"draft4/properties/properties, patternProperties, additionalProperties interaction/additionalProperty ignores property": "round-trip: array omitempty drops empty []",
	"draft4/ref/ref overrides any sibling keywords/ref valid":                                                               "ref overrides sibling keywords",

	// --- draft6 ---
	"draft6/properties/properties, patternProperties, additionalProperties interaction/additionalProperty ignores property": "round-trip: array omitempty drops empty []",
	"draft6/ref/ref overrides any sibling keywords/ref valid":                                                               "ref overrides sibling keywords",

	// --- draft7 ---
	"draft7/properties/properties, patternProperties, additionalProperties interaction/additionalProperty ignores property": "round-trip: array omitempty drops empty []",
	"draft7/ref/ref overrides any sibling keywords/ref valid":                                                               "ref overrides sibling keywords",
}
