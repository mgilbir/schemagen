package tests

// Known failures for external JSON Schema Test Suite tests.
// Updated after bare object struct generation fix.
// CodeGen: 26 known failures
// RoundTrip: 86 known failures (down from 103, after bare object + unevaluatedProperties fix)

var knownParseFailures = map[string]string{}

var knownCodeGenFailures = map[string]string{
	// --- draft2019-09 ---
	"draft2019-09/ref/Recursive references between schemas":                                         "compile: generated code does not compile",
	"draft2019-09/ref/refs with relative uris and defs":                                             "compile: generated code does not compile",
	"draft2019-09/ref/relative refs with absolute uris and defs":                                    "compile: generated code does not compile",
	"draft2019-09/refRemote/base URI change - change folder":                                        "compile: generated code does not compile",
	"draft2019-09/refRemote/base URI change - change folder in subschema":                           "compile: generated code does not compile",
	"draft2019-09/refRemote/retrieved nested refs resolve relative to their URI not $id":            "compile: generated code does not compile",
	"draft2019-09/vocabulary/schema that uses custom metaschema with with no validation vocabulary": "compile: generated code does not compile",
	// --- draft2020-12 ---
	"draft2020-12/dynamicRef/$dynamicRef skips over intermediate resources - direct reference":      "compile: generated code does not compile",
	"draft2020-12/ref/Recursive references between schemas":                                         "compile: generated code does not compile",
	"draft2020-12/ref/refs with relative uris and defs":                                             "compile: generated code does not compile",
	"draft2020-12/ref/relative refs with absolute uris and defs":                                    "compile: generated code does not compile",
	"draft2020-12/refRemote/base URI change - change folder":                                        "compile: generated code does not compile",
	"draft2020-12/refRemote/base URI change - change folder in subschema":                           "compile: generated code does not compile",
	"draft2020-12/refRemote/retrieved nested refs resolve relative to their URI not $id":            "compile: generated code does not compile",
	"draft2020-12/vocabulary/schema that uses custom metaschema with with no validation vocabulary": "compile: generated code does not compile",
	// --- draft4 ---
	"draft4/ref/Recursive references between schemas":               "compile: generated code does not compile",
	"draft4/refRemote/base URI change - change folder":              "compile: generated code does not compile",
	"draft4/refRemote/base URI change - change folder in subschema": "compile: generated code does not compile",
	// --- draft6 ---
	"draft6/ref/Recursive references between schemas":                              "compile: generated code does not compile",
	"draft6/refRemote/base URI change - change folder":                             "compile: generated code does not compile",
	"draft6/refRemote/base URI change - change folder in subschema":                "compile: generated code does not compile",
	"draft6/refRemote/retrieved nested refs resolve relative to their URI not $id": "compile: generated code does not compile",
	// --- draft7 ---
	"draft7/ref/Recursive references between schemas":                              "compile: generated code does not compile",
	"draft7/refRemote/base URI change - change folder":                             "compile: generated code does not compile",
	"draft7/refRemote/base URI change - change folder in subschema":                "compile: generated code does not compile",
	"draft7/refRemote/retrieved nested refs resolve relative to their URI not $id": "compile: generated code does not compile",
}

var knownRoundTripFailures = map[string]string{
	// --- draft2019-09 ---
	"draft2019-09/additionalProperties/additionalProperties being false does not allow other properties/patternProperties are not additional properties": "round-trip: compilation or execution error",
	"draft2019-09/anyOf/anyOf complex types/both anyOf valid (complex)":                                                                                  "no root struct type in generated code",
	"draft2019-09/anyOf/anyOf complex types/first anyOf valid (complex)":                                                                                 "no root struct type in generated code",
	"draft2019-09/anyOf/anyOf complex types/second anyOf valid (complex)":                                                                                "no root struct type in generated code",

	"draft2019-09/properties/properties, patternProperties, additionalProperties interaction/additionalProperty ignores property":                               "round-trip: compilation or execution error",
	"draft2019-09/properties/properties, patternProperties, additionalProperties interaction/patternProperty validates nonproperty":                             "round-trip: compilation or execution error",
	"draft2019-09/recursiveRef/$recursiveRef with $recursiveAnchor: false works like $ref/single level match":                                                   "no root struct type in generated code",
	"draft2019-09/recursiveRef/$recursiveRef with $recursiveAnchor: false works like $ref/two levels, properties match with inner definition":                   "no root struct type in generated code",
	"draft2019-09/recursiveRef/$recursiveRef with nesting/integer now matches as a property value":                                                              "no root struct type in generated code",
	"draft2019-09/recursiveRef/$recursiveRef with nesting/single level match":                                                                                   "no root struct type in generated code",
	"draft2019-09/recursiveRef/$recursiveRef with nesting/two levels, properties match with $recursiveRef":                                                      "no root struct type in generated code",
	"draft2019-09/recursiveRef/$recursiveRef with nesting/two levels, properties match with inner definition":                                                   "no root struct type in generated code",
	"draft2019-09/recursiveRef/$recursiveRef with no $recursiveAnchor in the initial target schema resource/leaf node matches: recursion uses the inner schema": "no root struct type in generated code",
	"draft2019-09/recursiveRef/$recursiveRef with no $recursiveAnchor in the outer schema resource/leaf node matches: recursion only uses inner schema":         "no root struct type in generated code",
	"draft2019-09/recursiveRef/$recursiveRef with no $recursiveAnchor works like $ref/single level match":                                                       "no root struct type in generated code",
	"draft2019-09/recursiveRef/$recursiveRef with no $recursiveAnchor works like $ref/two levels, properties match with inner definition":                       "no root struct type in generated code",
	"draft2019-09/recursiveRef/$recursiveRef without using nesting/single level match":                                                                          "no root struct type in generated code",
	"draft2019-09/recursiveRef/$recursiveRef without using nesting/two levels, properties match with inner definition":                                          "no root struct type in generated code",
	"draft2019-09/ref/Recursive references between schemas/valid tree":                                                                                          "round-trip: compilation or execution error",
	"draft2019-09/ref/ref applies alongside sibling keywords/ref valid, maxItems valid":                                                                         "round-trip: compilation or execution error",

	"draft2019-09/ref/refs with relative uris and defs/valid on both fields":          "round-trip: compilation or execution error",
	"draft2019-09/ref/relative refs with absolute uris and defs/valid on both fields": "round-trip: compilation or execution error",

	"draft2019-09/refRemote/base URI change - change folder in subschema/number is valid":                "round-trip: compilation or execution error",
	"draft2019-09/refRemote/base URI change - change folder/number is valid":                             "round-trip: compilation or execution error",
	"draft2019-09/refRemote/retrieved nested refs resolve relative to their URI not $id/string is valid": "round-trip: compilation or execution error",
	"draft2019-09/refRemote/root ref in remote ref/null is valid":                                        "round-trip: compilation or execution error",

	"draft2019-09/vocabulary/schema that uses custom metaschema with with no validation vocabulary/no validation: invalid number, but it still validates": "round-trip: compilation or execution error",
	"draft2019-09/vocabulary/schema that uses custom metaschema with with no validation vocabulary/no validation: valid number":                           "round-trip: compilation or execution error",
	// --- draft2020-12 ---
	"draft2020-12/additionalProperties/additionalProperties being false does not allow other properties/patternProperties are not additional properties": "round-trip: compilation or execution error",
	"draft2020-12/anyOf/anyOf complex types/both anyOf valid (complex)":                                                                                  "no root struct type in generated code",
	"draft2020-12/anyOf/anyOf complex types/first anyOf valid (complex)":                                                                                 "no root struct type in generated code",
	"draft2020-12/anyOf/anyOf complex types/second anyOf valid (complex)":                                                                                "no root struct type in generated code",
	"draft2020-12/dynamicRef/$dynamicRef skips over intermediate resources - direct reference/integer property passes":                                   "round-trip: compilation or execution error",

	"draft2020-12/properties/properties, patternProperties, additionalProperties interaction/additionalProperty ignores property":   "round-trip: compilation or execution error",
	"draft2020-12/properties/properties, patternProperties, additionalProperties interaction/patternProperty validates nonproperty": "round-trip: compilation or execution error",
	"draft2020-12/ref/Recursive references between schemas/valid tree":                                                              "round-trip: compilation or execution error",
	"draft2020-12/ref/ref applies alongside sibling keywords/ref valid, maxItems valid":                                             "round-trip: compilation or execution error",

	"draft2020-12/ref/refs with relative uris and defs/valid on both fields":          "round-trip: compilation or execution error",
	"draft2020-12/ref/relative refs with absolute uris and defs/valid on both fields": "round-trip: compilation or execution error",

	"draft2020-12/refRemote/base URI change - change folder in subschema/number is valid":                "round-trip: compilation or execution error",
	"draft2020-12/refRemote/base URI change - change folder/number is valid":                             "round-trip: compilation or execution error",
	"draft2020-12/refRemote/retrieved nested refs resolve relative to their URI not $id/string is valid": "round-trip: compilation or execution error",
	"draft2020-12/refRemote/root ref in remote ref/null is valid":                                        "round-trip: compilation or execution error",

	"draft2020-12/vocabulary/schema that uses custom metaschema with with no validation vocabulary/no validation: invalid number, but it still validates": "round-trip: compilation or execution error",
	"draft2020-12/vocabulary/schema that uses custom metaschema with with no validation vocabulary/no validation: valid number":                           "round-trip: compilation or execution error",
	// --- draft3 ---
	"draft3/additionalProperties/additionalProperties being false does not allow other properties/patternProperties are not additional properties": "round-trip: compilation or execution error",

	"draft3/properties/properties, patternProperties, additionalProperties interaction/additionalProperty ignores property":   "round-trip: compilation or execution error",
	"draft3/properties/properties, patternProperties, additionalProperties interaction/patternProperty validates nonproperty": "round-trip: compilation or execution error",
	"draft3/ref/ref overrides any sibling keywords/remote ref valid":                                                          "round-trip: compilation or execution error",

	// --- draft4 ---
	"draft4/additionalProperties/additionalProperties being false does not allow other properties/patternProperties are not additional properties": "round-trip: compilation or execution error",
	"draft4/anyOf/anyOf complex types/both anyOf valid (complex)":                                                                                  "no root struct type in generated code",
	"draft4/anyOf/anyOf complex types/first anyOf valid (complex)":                                                                                 "no root struct type in generated code",
	"draft4/anyOf/anyOf complex types/second anyOf valid (complex)":                                                                                "no root struct type in generated code",

	"draft4/properties/properties, patternProperties, additionalProperties interaction/additionalProperty ignores property":   "round-trip: compilation or execution error",
	"draft4/properties/properties, patternProperties, additionalProperties interaction/patternProperty validates nonproperty": "round-trip: compilation or execution error",
	"draft4/ref/Recursive references between schemas/valid tree":                                                              "round-trip: compilation or execution error",
	"draft4/ref/ref overrides any sibling keywords/ref valid":                                                                 "round-trip: compilation or execution error",

	"draft4/refRemote/base URI change - change folder in subschema/number is valid": "round-trip: compilation or execution error",
	"draft4/refRemote/base URI change - change folder/number is valid":              "round-trip: compilation or execution error",
	"draft4/refRemote/root ref in remote ref/null is valid":                         "round-trip: compilation or execution error",

	// --- draft6 ---
	"draft6/additionalProperties/additionalProperties being false does not allow other properties/patternProperties are not additional properties": "round-trip: compilation or execution error",
	"draft6/anyOf/anyOf complex types/both anyOf valid (complex)":                                                                                  "no root struct type in generated code",
	"draft6/anyOf/anyOf complex types/first anyOf valid (complex)":                                                                                 "no root struct type in generated code",
	"draft6/anyOf/anyOf complex types/second anyOf valid (complex)":                                                                                "no root struct type in generated code",

	"draft6/properties/properties, patternProperties, additionalProperties interaction/additionalProperty ignores property":   "round-trip: compilation or execution error",
	"draft6/properties/properties, patternProperties, additionalProperties interaction/patternProperty validates nonproperty": "round-trip: compilation or execution error",
	"draft6/ref/Recursive references between schemas/valid tree":                                                              "round-trip: compilation or execution error",
	"draft6/ref/ref overrides any sibling keywords/ref valid":                                                                 "round-trip: compilation or execution error",

	"draft6/refRemote/base URI change - change folder in subschema/number is valid":                "round-trip: compilation or execution error",
	"draft6/refRemote/base URI change - change folder/number is valid":                             "round-trip: compilation or execution error",
	"draft6/refRemote/remote ref with ref to definitions/valid":                                    "round-trip: compilation or execution error",
	"draft6/refRemote/retrieved nested refs resolve relative to their URI not $id/string is valid": "round-trip: compilation or execution error",
	"draft6/refRemote/root ref in remote ref/null is valid":                                        "round-trip: compilation or execution error",

	// --- draft7 ---
	"draft7/additionalProperties/additionalProperties being false does not allow other properties/patternProperties are not additional properties": "round-trip: compilation or execution error",
	"draft7/anyOf/anyOf complex types/both anyOf valid (complex)":                                                                                  "no root struct type in generated code",
	"draft7/anyOf/anyOf complex types/first anyOf valid (complex)":                                                                                 "no root struct type in generated code",
	"draft7/anyOf/anyOf complex types/second anyOf valid (complex)":                                                                                "no root struct type in generated code",

	"draft7/properties/properties, patternProperties, additionalProperties interaction/additionalProperty ignores property":   "round-trip: compilation or execution error",
	"draft7/properties/properties, patternProperties, additionalProperties interaction/patternProperty validates nonproperty": "round-trip: compilation or execution error",
	"draft7/ref/Recursive references between schemas/valid tree":                                                              "round-trip: compilation or execution error",
	"draft7/ref/ref overrides any sibling keywords/ref valid":                                                                 "round-trip: compilation or execution error",

	"draft7/refRemote/base URI change - change folder in subschema/number is valid":                "round-trip: compilation or execution error",
	"draft7/refRemote/base URI change - change folder/number is valid":                             "round-trip: compilation or execution error",
	"draft7/refRemote/remote ref with ref to definitions/valid":                                    "round-trip: compilation or execution error",
	"draft7/refRemote/retrieved nested refs resolve relative to their URI not $id/string is valid": "round-trip: compilation or execution error",
	"draft7/refRemote/root ref in remote ref/null is valid":                                        "round-trip: compilation or execution error",
}
