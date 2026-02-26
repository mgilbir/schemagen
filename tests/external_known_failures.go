package tests

// Known failures for external JSON Schema Test Suite tests.
// These are categorized by root cause. Bidirectional checking ensures
// that if a known failure starts passing, the test will error (remove from list).

// CodeGen: 2 known failures (2 flaky entries removed — non-deterministic map iteration)
var knownCodeGenFailures = map[string]string{
	"draft2020-12/dynamicRef/$dynamicRef avoids the root of each schema, but scopes are still registered":                     "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/A $ref to a $dynamicAnchor in the same schema resource behaves like a normal $ref to an $anchor": "$anchor resolution not fully implemented",
}

// RoundTrip: 124 known failures (2 flaky entries removed — non-deterministic map iteration)
var knownRoundTripFailures = map[string]string{
	"draft2019-09/anchor/same $anchor with different base uri/$ref resolves to /$defs/A/allOf/1":                                                        "$anchor resolution not fully implemented",
	"draft2019-09/optional/bignum/integer/a bignum is an integer":                                                                                       "non-structural schema: data shape incompatible with generated type",
	"draft2019-09/optional/bignum/integer/a negative bignum is an integer":                                                                              "non-structural schema: data shape incompatible with generated type",
	"draft2019-09/optional/float-overflow/all integers are multiples of 0.5, if overflow is handled/valid if optional overflow handling is implemented": "non-structural schema: data shape incompatible with generated type",

	"draft2019-09/type/integer type matches integers/a float with zero fractional part is an integer":                                                           "non-structural schema: data shape incompatible with generated type",
	"draft2020-12/anchor/same $anchor with different base uri/$ref resolves to /$defs/A/allOf/1":                                                                "$anchor resolution not fully implemented",
	"draft2020-12/dynamicRef/$dynamicRef avoids the root of each schema, but scopes are still registered/data is sufficient for schema at second#/$defs/length": "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/A $ref to a $dynamicAnchor in the same schema resource behaves like a normal $ref to an $anchor/An array of strings is valid":      "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/unevaluatedProperties/unevaluatedProperties with $dynamicRef/with no unevaluated properties":                                                  "round-trip mismatch: $dynamicRef not implemented",
	"draft2020-12/optional/bignum/integer/a bignum is an integer":                                                                                               "non-structural schema: data shape incompatible with generated type",
	"draft2020-12/optional/bignum/integer/a negative bignum is an integer":                                                                                      "non-structural schema: data shape incompatible with generated type",
	"draft2020-12/optional/float-overflow/all integers are multiples of 0.5, if overflow is handled/valid if optional overflow handling is implemented":         "non-structural schema: data shape incompatible with generated type",

	"draft2020-12/type/integer type matches integers/a float with zero fractional part is an integer":                                             "non-structural schema: data shape incompatible with generated type",
	"draft3/optional/bignum/integer/a bignum is an integer":                                                                                       "non-structural schema: data shape incompatible with generated type",
	"draft3/optional/bignum/integer/a negative bignum is an integer":                                                                              "non-structural schema: data shape incompatible with generated type",
	"draft3/type/applies a nested schema/an object is valid only if it is fully valid":                                                            "non-structural schema: data shape incompatible with generated type",
	"draft4/optional/bignum/integer/a bignum is an integer":                                                                                       "non-structural schema: data shape incompatible with generated type",
	"draft4/optional/bignum/integer/a negative bignum is an integer":                                                                              "non-structural schema: data shape incompatible with generated type",
	"draft6/optional/bignum/integer/a bignum is an integer":                                                                                       "non-structural schema: data shape incompatible with generated type",
	"draft6/optional/bignum/integer/a negative bignum is an integer":                                                                              "non-structural schema: data shape incompatible with generated type",
	"draft6/optional/float-overflow/all integers are multiples of 0.5, if overflow is handled/valid if optional overflow handling is implemented": "non-structural schema: data shape incompatible with generated type",
	"draft6/type/integer type matches integers/a float with zero fractional part is an integer":                                                   "non-structural schema: data shape incompatible with generated type",
	"draft7/optional/bignum/integer/a bignum is an integer":                                                                                       "non-structural schema: data shape incompatible with generated type",
	"draft7/optional/bignum/integer/a negative bignum is an integer":                                                                              "non-structural schema: data shape incompatible with generated type",
	"draft7/optional/float-overflow/all integers are multiples of 0.5, if overflow is handled/valid if optional overflow handling is implemented": "non-structural schema: data shape incompatible with generated type",
	"draft7/type/integer type matches integers/a float with zero fractional part is an integer":                                                   "non-structural schema: data shape incompatible with generated type",

	// Type-inferred schemas: constraint-only schemas (no "type" field) now infer a Go type
	// from the constraint keywords. JSTS tests these with incompatible data types (e.g.,
	// {"minimum": 5} with data "hello") which are "valid" per JSON Schema but can't
	// unmarshal into the inferred Go type (float64).
	"draft3/divisibleBy/by int/ignores non-numbers":                                      "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/maxItems/maxItems validation/ignores non-arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/maxLength/maxLength validation/ignores non-strings":                          "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/maximum/maximum validation (explicit false exclusivity)/ignores non-numbers": "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/maximum/maximum validation/ignores non-numbers":                              "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/minItems/minItems validation/ignores non-arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/minLength/minLength validation/ignores non-strings":                          "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/minimum/minimum validation with signed integer/ignores non-numbers":          "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/minimum/minimum validation/ignores non-numbers":                              "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/pattern/pattern validation/ignores arrays":                                   "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/pattern/pattern validation/ignores booleans":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/pattern/pattern validation/ignores floats":                                   "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/pattern/pattern validation/ignores integers":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/pattern/pattern validation/ignores null":                                     "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/pattern/pattern validation/ignores objects":                                  "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/maxItems/maxItems validation/ignores non-arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/maxLength/maxLength validation/ignores non-strings":                          "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/maximum/maximum validation (explicit false exclusivity)/ignores non-numbers": "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/maximum/maximum validation/ignores non-numbers":                              "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/minItems/minItems validation/ignores non-arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/minLength/minLength validation/ignores non-strings":                          "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/minimum/minimum validation (explicit false exclusivity)/ignores non-numbers": "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/minimum/minimum validation with signed integer/ignores non-numbers":          "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/minimum/minimum validation/ignores non-numbers":                              "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/multipleOf/by int/ignores non-numbers":                                       "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/pattern/pattern validation/ignores arrays":                                   "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/pattern/pattern validation/ignores booleans":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/pattern/pattern validation/ignores floats":                                   "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/pattern/pattern validation/ignores integers":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/pattern/pattern validation/ignores null":                                     "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/pattern/pattern validation/ignores objects":                                  "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/exclusiveMaximum/exclusiveMaximum validation/ignores non-numbers":            "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/exclusiveMinimum/exclusiveMinimum validation/ignores non-numbers":            "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/maxItems/maxItems validation/ignores non-arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/maxLength/maxLength validation/ignores non-strings":                          "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/maximum/maximum validation/ignores non-numbers":                              "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/minItems/minItems validation/ignores non-arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/minLength/minLength validation/ignores non-strings":                          "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/minimum/minimum validation with signed integer/ignores non-numbers":          "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/minimum/minimum validation/ignores non-numbers":                              "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/multipleOf/by int/ignores non-numbers":                                       "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/pattern/pattern validation/ignores arrays":                                   "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/pattern/pattern validation/ignores booleans":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/pattern/pattern validation/ignores floats":                                   "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/pattern/pattern validation/ignores integers":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/pattern/pattern validation/ignores null":                                     "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/pattern/pattern validation/ignores objects":                                  "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/exclusiveMaximum/exclusiveMaximum validation/ignores non-numbers":            "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/exclusiveMinimum/exclusiveMinimum validation/ignores non-numbers":            "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/maxItems/maxItems validation/ignores non-arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/maxLength/maxLength validation/ignores non-strings":                          "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/maximum/maximum validation/ignores non-numbers":                              "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/minItems/minItems validation/ignores non-arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/minLength/minLength validation/ignores non-strings":                          "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/minimum/minimum validation with signed integer/ignores non-numbers":          "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/minimum/minimum validation/ignores non-numbers":                              "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/multipleOf/by int/ignores non-numbers":                                       "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/pattern/pattern validation/ignores arrays":                                   "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/pattern/pattern validation/ignores booleans":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/pattern/pattern validation/ignores floats":                                   "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/pattern/pattern validation/ignores integers":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/pattern/pattern validation/ignores null":                                     "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/pattern/pattern validation/ignores objects":                                  "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/exclusiveMaximum/exclusiveMaximum validation/ignores non-numbers":      "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/exclusiveMinimum/exclusiveMinimum validation/ignores non-numbers":      "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/maxItems/maxItems validation/ignores non-arrays":                       "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/maxLength/maxLength validation/ignores non-strings":                    "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/maximum/maximum validation/ignores non-numbers":                        "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/minItems/minItems validation/ignores non-arrays":                       "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/minLength/minLength validation/ignores non-strings":                    "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/minimum/minimum validation with signed integer/ignores non-numbers":    "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/minimum/minimum validation/ignores non-numbers":                        "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/multipleOf/by int/ignores non-numbers":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/optional/no-schema/validation without $schema/a non-string is valid":   "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/pattern/pattern validation/ignores arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/pattern/pattern validation/ignores booleans":                           "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/pattern/pattern validation/ignores floats":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/pattern/pattern validation/ignores integers":                           "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/pattern/pattern validation/ignores null":                               "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/pattern/pattern validation/ignores objects":                            "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/exclusiveMaximum/exclusiveMaximum validation/ignores non-numbers":      "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/exclusiveMinimum/exclusiveMinimum validation/ignores non-numbers":      "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/maxItems/maxItems validation/ignores non-arrays":                       "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/maxLength/maxLength validation/ignores non-strings":                    "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/maximum/maximum validation/ignores non-numbers":                        "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/minItems/minItems validation/ignores non-arrays":                       "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/minLength/minLength validation/ignores non-strings":                    "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/minimum/minimum validation with signed integer/ignores non-numbers":    "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/minimum/minimum validation/ignores non-numbers":                        "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/multipleOf/by int/ignores non-numbers":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/optional/no-schema/validation without $schema/a non-string is valid":   "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/pattern/pattern validation/ignores arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/pattern/pattern validation/ignores booleans":                           "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/pattern/pattern validation/ignores floats":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/pattern/pattern validation/ignores integers":                           "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/pattern/pattern validation/ignores null":                               "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/pattern/pattern validation/ignores objects":                            "type-inferred schema: data type incompatible with inferred Go type",
}

// Parse: 0 known failures
var knownParseFailures = map[string]string{}

// Validation: 241 known failures for Validate() correctness testing (2 flaky entries in knownFlakyTests).
// Only schemas that produce a Validate() method are tested; others are skipped.
// Only exercised entries are listed — schemas that generate type `any` (no Validate())
// are not tracked here since checkKnownFailure is never reached for them.
// Root causes:
//   - type-inferred schema: data type incompatible with inferred Go type (89)
//   - unevaluatedProperties: cousin isolation requires per-branch annotation tracking (24)
//   - non-object data: cannot unmarshal number into generated Go type (16)
//   - tuple items: Root []any cannot validate sub-item structure (15)
//   - $dynamicRef/$dynamicAnchor not implemented (13)
//   - unevaluatedProperties: dynamic oneOf evaluation over-approximation (10)
//   - $ref sibling keyword validation not implemented (10)
//   - $ref to unknown keyword: unresolved ref falls back to any (8)
//   - unevaluatedProperties: if/then/else/anyOf static over-approximation (10)
//   - codegen produces code that fails to compile for validation binary (6)
//   - additionalProperties: allOf interaction (6)
//   - unevaluatedProperties: schema-valued constraint not yet validated (4)
//   - unevaluatedProperties: dependentSchemas static over-approximation (4)
//   - unevaluatedItems validation not implemented (4)
//   - float-overflow optional test: 1e308 overflows int64 Go type (4)
//   - $anchor resolution edge cases (4)
//   - $dynamicRef with required: $dynamicRef not implemented (3)
//   - $id/$ref evaluation order edge case (2)
//   - custom metaschema vocabulary not supported (2)
//   - type-inferred schema: no $schema to guide validation (2)
//   - $recursiveRef not implemented (2)
//   - unevaluatedProperties: $dynamicRef not implemented (1)
//   - cross-draft validation not supported (1)
//   - over-strict validation: valid data rejected (1)
var knownValidationFailures = map[string]string{
	// (default keyword — FIXED via optional field presence tracking)

	// float-overflow optional test — 1e308 can't be unmarshaled into int64 Go type
	"draft6/optional/float-overflow/all integers are multiples of 0.5, if overflow is handled/valid if optional overflow handling is implemented":       "1e308 overflows int64 Go type",
	"draft7/optional/float-overflow/all integers are multiples of 0.5, if overflow is handled/valid if optional overflow handling is implemented":       "1e308 overflows int64 Go type",
	"draft2019-09/optional/float-overflow/all integers are multiples of 0.5, if overflow is handled/valid if optional overflow handling is implemented": "1e308 overflows int64 Go type",
	"draft2020-12/optional/float-overflow/all integers are multiples of 0.5, if overflow is handled/valid if optional overflow handling is implemented": "1e308 overflows int64 Go type",

	// (patternProperties sub-schema validation — FIXED via ppMinItems/ppMaxItems/ppMinLength/ppMaxLength/ppPattern)
	// (additionalProperty invalidates others — FIXED via schema validation on overflow map)

	// type-inferred schema: data type incompatible with inferred Go type
	// When a schema has constraints but no explicit "type", we infer the type from constraints.
	// This means data of a different type (which JSON Schema says should "ignore" the constraint)
	// fails to unmarshal into the inferred Go type.
	"draft3/divisibleBy/by int/ignores non-numbers":                                      "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/maximum/maximum validation/ignores non-numbers":                              "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/maximum/maximum validation (explicit false exclusivity)/ignores non-numbers": "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/maxItems/maxItems validation/ignores non-arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/maxLength/maxLength validation/ignores non-strings":                          "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/minimum/minimum validation/ignores non-numbers":                              "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/minimum/minimum validation with signed integer/ignores non-numbers":          "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/minItems/minItems validation/ignores non-arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/minLength/minLength validation/ignores non-strings":                          "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/pattern/pattern validation/ignores arrays":                                   "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/pattern/pattern validation/ignores booleans":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/pattern/pattern validation/ignores floats":                                   "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/pattern/pattern validation/ignores integers":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft3/pattern/pattern validation/ignores objects":                                  "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/maximum/maximum validation/ignores non-numbers":                              "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/maximum/maximum validation (explicit false exclusivity)/ignores non-numbers": "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/maxItems/maxItems validation/ignores non-arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/maxLength/maxLength validation/ignores non-strings":                          "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/minimum/minimum validation/ignores non-numbers":                              "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/minimum/minimum validation (explicit false exclusivity)/ignores non-numbers": "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/minimum/minimum validation with signed integer/ignores non-numbers":          "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/minItems/minItems validation/ignores non-arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/minLength/minLength validation/ignores non-strings":                          "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/multipleOf/by int/ignores non-numbers":                                       "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/pattern/pattern validation/ignores arrays":                                   "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/pattern/pattern validation/ignores booleans":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/pattern/pattern validation/ignores floats":                                   "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/pattern/pattern validation/ignores integers":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft4/pattern/pattern validation/ignores objects":                                  "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/exclusiveMaximum/exclusiveMaximum validation/ignores non-numbers":            "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/exclusiveMinimum/exclusiveMinimum validation/ignores non-numbers":            "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/maximum/maximum validation/ignores non-numbers":                              "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/maxItems/maxItems validation/ignores non-arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/maxLength/maxLength validation/ignores non-strings":                          "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/minimum/minimum validation/ignores non-numbers":                              "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/minimum/minimum validation with signed integer/ignores non-numbers":          "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/minItems/minItems validation/ignores non-arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/minLength/minLength validation/ignores non-strings":                          "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/multipleOf/by int/ignores non-numbers":                                       "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/pattern/pattern validation/ignores arrays":                                   "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/pattern/pattern validation/ignores booleans":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/pattern/pattern validation/ignores floats":                                   "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/pattern/pattern validation/ignores integers":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft6/pattern/pattern validation/ignores objects":                                  "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/exclusiveMaximum/exclusiveMaximum validation/ignores non-numbers":            "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/exclusiveMinimum/exclusiveMinimum validation/ignores non-numbers":            "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/maximum/maximum validation/ignores non-numbers":                              "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/maxItems/maxItems validation/ignores non-arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/maxLength/maxLength validation/ignores non-strings":                          "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/minimum/minimum validation/ignores non-numbers":                              "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/minimum/minimum validation with signed integer/ignores non-numbers":          "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/minItems/minItems validation/ignores non-arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/minLength/minLength validation/ignores non-strings":                          "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/multipleOf/by int/ignores non-numbers":                                       "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/pattern/pattern validation/ignores arrays":                                   "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/pattern/pattern validation/ignores booleans":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/pattern/pattern validation/ignores floats":                                   "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/pattern/pattern validation/ignores integers":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft7/pattern/pattern validation/ignores objects":                                  "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/exclusiveMaximum/exclusiveMaximum validation/ignores non-numbers":      "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/exclusiveMinimum/exclusiveMinimum validation/ignores non-numbers":      "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/maximum/maximum validation/ignores non-numbers":                        "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/maxItems/maxItems validation/ignores non-arrays":                       "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/maxLength/maxLength validation/ignores non-strings":                    "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/minimum/minimum validation/ignores non-numbers":                        "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/minimum/minimum validation with signed integer/ignores non-numbers":    "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/minItems/minItems validation/ignores non-arrays":                       "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/minLength/minLength validation/ignores non-strings":                    "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/multipleOf/by int/ignores non-numbers":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/pattern/pattern validation/ignores arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/pattern/pattern validation/ignores booleans":                           "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/pattern/pattern validation/ignores floats":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/pattern/pattern validation/ignores integers":                           "type-inferred schema: data type incompatible with inferred Go type",
	"draft2019-09/pattern/pattern validation/ignores objects":                            "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/exclusiveMaximum/exclusiveMaximum validation/ignores non-numbers":      "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/exclusiveMinimum/exclusiveMinimum validation/ignores non-numbers":      "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/maximum/maximum validation/ignores non-numbers":                        "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/maxItems/maxItems validation/ignores non-arrays":                       "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/maxLength/maxLength validation/ignores non-strings":                    "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/minimum/minimum validation/ignores non-numbers":                        "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/minimum/minimum validation with signed integer/ignores non-numbers":    "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/minItems/minItems validation/ignores non-arrays":                       "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/minLength/minLength validation/ignores non-strings":                    "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/multipleOf/by int/ignores non-numbers":                                 "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/pattern/pattern validation/ignores arrays":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/pattern/pattern validation/ignores booleans":                           "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/pattern/pattern validation/ignores floats":                             "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/pattern/pattern validation/ignores integers":                           "type-inferred schema: data type incompatible with inferred Go type",
	"draft2020-12/pattern/pattern validation/ignores objects":                            "type-inferred schema: data type incompatible with inferred Go type",

	// $id/$ref evaluation order — codegen resolves $id and $ref in wrong order
	"draft2019-09/ref/order of evaluation: $id and $ref/data is invalid against first definition": "$id/$ref evaluation order edge case",
	"draft2020-12/ref/order of evaluation: $id and $ref/data is invalid against first definition": "$id/$ref evaluation order edge case",

	// custom metaschema vocabulary not supported — vocabulary that disables validation
	"draft2019-09/vocabulary/schema that uses custom metaschema with with no validation vocabulary/no validation: invalid number, but it still validates": "custom metaschema vocabulary not supported",
	"draft2020-12/vocabulary/schema that uses custom metaschema with with no validation vocabulary/no validation: invalid number, but it still validates": "custom metaschema vocabulary not supported",

	// type-inferred schema: no $schema to guide validation
	"draft2019-09/optional/no-schema/validation without $schema/a non-string is valid": "type-inferred schema: no $schema to guide validation",
	"draft2020-12/optional/no-schema/validation without $schema/a non-string is valid": "type-inferred schema: no $schema to guide validation",

	// (enum in properties — FIXED via validatable field dispatch)

	// $dynamicRef with required fields: $dynamicRef/$dynamicAnchor not implemented
	"draft2020-12/dynamicRef/tests for implementation dynamic anchor and reference link/incorrect extended schema":     "$dynamicRef with required: $dynamicRef not implemented",
	"draft2020-12/dynamicRef/$ref and $dynamicAnchor are independent of order - $defs first/incorrect extended schema": "$dynamicRef with required: $dynamicRef not implemented",
	"draft2020-12/dynamicRef/$ref and $dynamicAnchor are independent of order - $ref first/incorrect extended schema":  "$dynamicRef with required: $dynamicRef not implemented",

	// $dynamicRef/$dynamicAnchor not implemented (12 entries)
	"draft2020-12/dynamicRef/$dynamicRef points to a boolean schema/follow $dynamicRef to a false schema":                                                                                                                 "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/$dynamicRef skips over intermediate resources - direct reference/string property fails":                                                                                                      "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/A $dynamicRef resolves to the first $dynamicAnchor still in scope that is encountered when the schema is evaluated/An array containing non-strings is invalid":                               "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/A $dynamicRef that initially resolves to a schema with a matching $dynamicAnchor resolves to the first $dynamicAnchor in the dynamic scope/The recursive part is not valid against the root": "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/A $dynamicRef to a $dynamicAnchor in the same schema resource behaves like a normal $ref to an $anchor/An array containing non-strings is invalid":                                           "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/A $dynamicRef to an $anchor in the same schema resource behaves like a normal $ref to an $anchor/An array containing non-strings is invalid":                                                 "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/A $dynamicRef with intermediate scopes that don't include a matching $dynamicAnchor does not affect dynamic scope resolution/An array containing non-strings is invalid":                     "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/A $dynamicRef without anchor in fragment behaves identical to $ref/An array of strings is invalid":                                                                                           "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/multiple dynamic paths to the $dynamicRef keyword/number list with string values":                                                                                                            "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/multiple dynamic paths to the $dynamicRef keyword/string list with number values":                                                                                                            "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/strict-tree schema, guards against misspelled properties/instance with misspelled field":                                                                                                     "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/optional/dynamicRef/$dynamicRef skips over intermediate resources - pointer reference across resource boundary/string property fails":                                                                   "$dynamicRef/$dynamicAnchor not implemented",

	// $ref sibling keyword validation not implemented (10 entries)
	"draft2019-09/ref/ref creates new scope when adjacent to keywords/referenced subschema doesn't see annotations from properties": "$ref sibling keyword validation not implemented",
	"draft2019-09/ref/refs with relative uris and defs/invalid on outer field":                                                      "$ref sibling keyword validation not implemented",
	"draft2019-09/ref/relative refs with absolute uris and defs/invalid on outer field":                                             "$ref sibling keyword validation not implemented",
	// (draft2019-09 URN base URI with $ref — FIXED via non-object validation)
	"draft2020-12/ref/ref creates new scope when adjacent to keywords/referenced subschema doesn't see annotations from properties": "$ref sibling keyword validation not implemented",
	"draft2020-12/ref/refs with relative uris and defs/invalid on outer field":                                                      "$ref sibling keyword validation not implemented",
	"draft2020-12/ref/relative refs with absolute uris and defs/invalid on outer field":                                             "$ref sibling keyword validation not implemented",
	// (draft2020-12 URN base URI with $ref — FIXED via non-object validation)
	"draft6/ref/refs with relative uris and defs/invalid on inner field":          "$ref sibling keyword validation not implemented",
	"draft6/ref/relative refs with absolute uris and defs/invalid on inner field": "$ref sibling keyword validation not implemented",
	// (draft6 URN base URI with $ref — FIXED via non-object validation)
	"draft7/ref/refs with relative uris and defs/invalid on inner field":          "$ref sibling keyword validation not implemented",
	"draft7/ref/relative refs with absolute uris and defs/invalid on inner field": "$ref sibling keyword validation not implemented",
	// (draft7 URN base URI with $ref — FIXED via non-object validation)

	// additionalProperties validation remaining (6 entries — allOf interaction)
	"draft2019-09/additionalProperties/additionalProperties does not look in applicators/properties defined in allOf are not examined": "additionalProperties: allOf properties not considered",
	"draft2020-12/additionalProperties/additionalProperties does not look in applicators/properties defined in allOf are not examined": "additionalProperties: allOf properties not considered",
	"draft3/additionalProperties/additionalProperties does not look in applicators/properties defined in extends are not examined":     "additionalProperties: extends properties not considered",
	"draft4/additionalProperties/additionalProperties does not look in applicators/properties defined in allOf are not examined":       "additionalProperties: allOf properties not considered",
	"draft6/additionalProperties/additionalProperties does not look in applicators/properties defined in allOf are not examined":       "additionalProperties: allOf properties not considered",
	"draft7/additionalProperties/additionalProperties does not look in applicators/properties defined in allOf are not examined":       "additionalProperties: allOf properties not considered",

	// $anchor/$recursiveRef resolution edge cases (5 entries)
	"draft2019-09/anchor/same $anchor with different base uri/$ref resolves to /$defs/A/allOf/1":         "$anchor resolution produces wrong unmarshal type",
	"draft2019-09/anchor/same $anchor with different base uri/$ref does not resolve to /$defs/A/allOf/0": "$anchor resolution: allOf alias exposes wrong type after composition fix",
	"draft2019-09/ref/$ref with $recursiveAnchor/extra items disallowed for root":                        "$recursiveRef validation not implemented",
	"draft2020-12/anchor/same $anchor with different base uri/$ref resolves to /$defs/A/allOf/1":         "$anchor resolution produces wrong unmarshal type",
	"draft2020-12/anchor/same $anchor with different base uri/$ref does not resolve to /$defs/A/allOf/0": "$anchor resolution: allOf alias exposes wrong type after composition fix",

	// $ref to unknown keyword: unresolved ref falls back to any, no type validation (8 entries)
	"draft2019-09/optional/refOfUnknownKeyword/reference of a root arbitrary keyword /mismatch":                             "$ref to unknown keyword: unresolved ref falls back to any",
	"draft2019-09/optional/refOfUnknownKeyword/reference of a root arbitrary keyword with encoded ref/mismatch":             "$ref to unknown keyword: unresolved ref falls back to any",
	"draft2019-09/optional/refOfUnknownKeyword/reference of an arbitrary keyword of a sub-schema with encoded ref/mismatch": "$ref to unknown keyword: unresolved ref falls back to any",
	"draft2019-09/optional/refOfUnknownKeyword/reference of an arbitrary keyword of a sub-schema/mismatch":                  "$ref to unknown keyword: unresolved ref falls back to any",
	"draft2020-12/optional/refOfUnknownKeyword/reference of a root arbitrary keyword /mismatch":                             "$ref to unknown keyword: unresolved ref falls back to any",
	"draft2020-12/optional/refOfUnknownKeyword/reference of a root arbitrary keyword with encoded ref/mismatch":             "$ref to unknown keyword: unresolved ref falls back to any",
	"draft2020-12/optional/refOfUnknownKeyword/reference of an arbitrary keyword of a sub-schema with encoded ref/mismatch": "$ref to unknown keyword: unresolved ref falls back to any",
	"draft2020-12/optional/refOfUnknownKeyword/reference of an arbitrary keyword of a sub-schema/mismatch":                  "$ref to unknown keyword: unresolved ref falls back to any",

	// codegen produces code that fails to compile for validation binary (6 entries)
	"draft2020-12/dynamicRef/$dynamicRef avoids the root of each schema, but scopes are still registered/data is not sufficient for schema at second#/$defs/length":      "codegen produces code that fails to compile for validation binary",
	"draft2020-12/dynamicRef/$ref to $dynamicRef finds detached $dynamicAnchor/non-number is invalid":                                                                    "codegen produces code that fails to compile for validation binary",
	"draft2020-12/dynamicRef/A $ref to a $dynamicAnchor in the same schema resource behaves like a normal $ref to an $anchor/An array containing non-strings is invalid": "codegen produces code that fails to compile for validation binary",

	// cross-draft validation not supported (1 entries)
	"draft7/optional/cross-draft/refs to future drafts are processed as future drafts/missing bar is invalid": "cross-draft validation not supported",

	// (draft3 enum required-as-boolean — FIXED via draft3 required normalization)

	// (extends validation — FIXED via draft3 required normalization + extends→allOf)

	// required with composition validation not implemented (1 entries)
	// (draft3/required — FIXED via draft3 required normalization)

	// unevaluatedItems validation not implemented (4 entries)
	"draft2019-09/unevaluatedItems/item is evaluated in an uncle schema to unevaluatedItems/uncle keyword evaluation is not significant": "unevaluatedItems validation not implemented",
	"draft2019-09/unevaluatedItems/unevaluatedItems with $recursiveRef/with unevaluated items":                                           "unevaluatedItems validation not implemented",
	"draft2020-12/unevaluatedItems/item is evaluated in an uncle schema to unevaluatedItems/uncle keyword evaluation is not significant": "unevaluatedItems validation not implemented",
	"draft2020-12/unevaluatedItems/unevaluatedItems with $dynamicRef/with unevaluated items":                                             "unevaluatedItems validation not implemented",

	// unevaluatedProperties: remaining failures (54 entries)
	// Cousin/uncle isolation requires per-branch annotation tracking (24)
	"draft2019-09/unevaluatedProperties/cousin unevaluatedProperties, true and false, false with properties/with nested unevaluated properties":          "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2019-09/unevaluatedProperties/cousin unevaluatedProperties, true and false, true with properties/with nested unevaluated properties":           "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2019-09/unevaluatedProperties/cousin unevaluatedProperties, true and false, true with properties/with no nested unevaluated properties":        "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2019-09/unevaluatedProperties/in-place applicator siblings, allOf has unevaluated/base case: both properties present":                          "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2019-09/unevaluatedProperties/in-place applicator siblings, allOf has unevaluated/in place applicator siblings, foo is missing":                "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2019-09/unevaluatedProperties/in-place applicator siblings, anyOf has unevaluated/base case: both properties present":                          "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2019-09/unevaluatedProperties/in-place applicator siblings, anyOf has unevaluated/in place applicator siblings, bar is missing":                "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2019-09/unevaluatedProperties/nested unevaluatedProperties, outer true, inner false, properties inside/with nested unevaluated properties":     "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2019-09/unevaluatedProperties/nested unevaluatedProperties, outer true, inner false, properties outside/with nested unevaluated properties":    "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2019-09/unevaluatedProperties/nested unevaluatedProperties, outer true, inner false, properties outside/with no nested unevaluated properties": "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2020-12/unevaluatedProperties/cousin unevaluatedProperties, true and false, false with properties/with nested unevaluated properties":          "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2020-12/unevaluatedProperties/cousin unevaluatedProperties, true and false, true with properties/with nested unevaluated properties":           "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2020-12/unevaluatedProperties/cousin unevaluatedProperties, true and false, true with properties/with no nested unevaluated properties":        "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2020-12/unevaluatedProperties/in-place applicator siblings, allOf has unevaluated/base case: both properties present":                          "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2020-12/unevaluatedProperties/in-place applicator siblings, allOf has unevaluated/in place applicator siblings, foo is missing":                "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2020-12/unevaluatedProperties/in-place applicator siblings, anyOf has unevaluated/base case: both properties present":                          "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2020-12/unevaluatedProperties/in-place applicator siblings, anyOf has unevaluated/in place applicator siblings, bar is missing":                "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2020-12/unevaluatedProperties/nested unevaluatedProperties, outer true, inner false, properties inside/with nested unevaluated properties":     "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2020-12/unevaluatedProperties/nested unevaluatedProperties, outer true, inner false, properties outside/with nested unevaluated properties":    "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2020-12/unevaluatedProperties/nested unevaluatedProperties, outer true, inner false, properties outside/with no nested unevaluated properties": "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	// Static over-approximation: if/then/else and anyOf properties over-counted as evaluated (10)
	"draft2020-12/unevaluatedProperties/unevaluatedProperties with if/then/else/when if is true and has unevaluated properties":                    "unevaluatedProperties: if/then/else static over-approximation",
	"draft2020-12/unevaluatedProperties/unevaluatedProperties with if/then/else/when if is false and has unevaluated properties":                   "unevaluatedProperties: if/then/else static over-approximation",
	"draft2020-12/unevaluatedProperties/unevaluatedProperties with if/then/else, then not defined/when if is false and has unevaluated properties": "unevaluatedProperties: if/then/else static over-approximation",
	"draft2019-09/unevaluatedProperties/unevaluatedProperties with if/then/else/when if is true and has unevaluated properties":                    "unevaluatedProperties: if/then/else static over-approximation",
	"draft2019-09/unevaluatedProperties/unevaluatedProperties with if/then/else/when if is false and has unevaluated properties":                   "unevaluatedProperties: if/then/else static over-approximation",
	"draft2019-09/unevaluatedProperties/unevaluatedProperties with if/then/else, then not defined/when if is false and has unevaluated properties": "unevaluatedProperties: if/then/else static over-approximation",
	"draft2019-09/unevaluatedProperties/unevaluatedProperties with anyOf/when one matches and has unevaluated properties":                          "unevaluatedProperties: anyOf static over-approximation",
	"draft2019-09/unevaluatedProperties/unevaluatedProperties with anyOf/when two match and has unevaluated properties":                            "unevaluatedProperties: anyOf static over-approximation",
	"draft2020-12/unevaluatedProperties/unevaluatedProperties with anyOf/when one matches and has unevaluated properties":                          "unevaluatedProperties: anyOf static over-approximation",
	"draft2020-12/unevaluatedProperties/unevaluatedProperties with anyOf/when two match and has unevaluated properties":                            "unevaluatedProperties: anyOf static over-approximation",
	// Schema-valued unevaluatedProperties not yet validated (4)
	"draft2019-09/unevaluatedProperties/unevaluatedProperties not affected by propertyNames/string property is invalid": "unevaluatedProperties: schema-valued constraint not yet validated",
	"draft2019-09/unevaluatedProperties/unevaluatedProperties schema/with invalid unevaluated properties":               "unevaluatedProperties: schema-valued constraint not yet validated",
	"draft2020-12/unevaluatedProperties/unevaluatedProperties schema/with invalid unevaluated properties":               "unevaluatedProperties: schema-valued constraint not yet validated",
	"draft2020-12/unevaluatedProperties/unevaluatedProperties not affected by propertyNames/string property is invalid": "unevaluatedProperties: schema-valued constraint not yet validated",
	// dependentSchemas with unevaluatedProperties: dependent properties over-counted as evaluated (4)
	"draft2019-09/unevaluatedProperties/unevaluatedProperties with dependentSchemas/with unevaluated properties":                               "unevaluatedProperties: dependentSchemas static over-approximation",
	"draft2019-09/unevaluatedProperties/dependentSchemas with unevaluatedProperties/unevaluatedProperties doesn't see bar when foo2 is absent": "unevaluatedProperties: dependentSchemas static over-approximation",
	"draft2020-12/unevaluatedProperties/unevaluatedProperties with dependentSchemas/with unevaluated properties":                               "unevaluatedProperties: dependentSchemas static over-approximation",
	"draft2020-12/unevaluatedProperties/dependentSchemas with unevaluatedProperties/unevaluatedProperties doesn't see bar when foo2 is absent": "unevaluatedProperties: dependentSchemas static over-approximation",
	// Remaining unevaluatedProperties failures: $dynamicRef/$recursiveRef, dynamic oneOf evaluation (12)
	"draft2019-09/unevaluatedProperties/unevaluatedProperties with $recursiveRef/with unevaluated properties":             "unevaluatedProperties: $recursiveRef not implemented",
	"draft2019-09/unevaluatedProperties/unevaluatedProperties can't see inside cousins (reverse order)/always fails":      "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2019-09/unevaluatedProperties/unevaluatedProperties can't see inside cousins/always fails":                      "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2019-09/unevaluatedProperties/dynamic evalation inside nested refs/xx + foo is invalid":                         "unevaluatedProperties: dynamic oneOf evaluation over-approximation",
	"draft2019-09/unevaluatedProperties/unevaluatedProperties + ref inside allOf / oneOf/Empty is invalid (no x or y)":    "unevaluatedProperties: dynamic oneOf evaluation over-approximation",
	"draft2019-09/unevaluatedProperties/unevaluatedProperties + ref inside allOf / oneOf/a and b and x and y are invalid": "unevaluatedProperties: dynamic oneOf evaluation over-approximation",
	"draft2019-09/unevaluatedProperties/unevaluatedProperties + ref inside allOf / oneOf/a and b are invalid (no x or y)": "unevaluatedProperties: dynamic oneOf evaluation over-approximation",
	"draft2019-09/unevaluatedProperties/unevaluatedProperties + ref inside allOf / oneOf/x and y are invalid":             "unevaluatedProperties: dynamic oneOf evaluation over-approximation",
	"draft2020-12/unevaluatedProperties/unevaluatedProperties with $dynamicRef/with unevaluated properties":               "unevaluatedProperties: $dynamicRef not implemented",
	"draft2020-12/unevaluatedProperties/unevaluatedProperties can't see inside cousins (reverse order)/always fails":      "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2020-12/unevaluatedProperties/unevaluatedProperties can't see inside cousins/always fails":                      "unevaluatedProperties: cousin isolation requires per-branch annotation tracking",
	"draft2020-12/unevaluatedProperties/dynamic evalation inside nested refs/xx + foo is invalid":                         "unevaluatedProperties: dynamic oneOf evaluation over-approximation",
	"draft2020-12/unevaluatedProperties/unevaluatedProperties + ref inside allOf / oneOf/Empty is invalid (no x or y)":    "unevaluatedProperties: dynamic oneOf evaluation over-approximation",
	"draft2020-12/unevaluatedProperties/unevaluatedProperties + ref inside allOf / oneOf/a and b and x and y are invalid": "unevaluatedProperties: dynamic oneOf evaluation over-approximation",
	"draft2020-12/unevaluatedProperties/unevaluatedProperties + ref inside allOf / oneOf/a and b are invalid (no x or y)": "unevaluatedProperties: dynamic oneOf evaluation over-approximation",
	"draft2020-12/unevaluatedProperties/unevaluatedProperties + ref inside allOf / oneOf/x and y are invalid":             "unevaluatedProperties: dynamic oneOf evaluation over-approximation",

	// codegen produces code that fails to compile for validation binary (3 additional entries — other half of same groups)
	"draft2020-12/dynamicRef/$dynamicRef avoids the root of each schema, but scopes are still registered/data is sufficient for schema at second#/$defs/length": "codegen produces code that fails to compile for validation binary",
	"draft2020-12/dynamicRef/$ref to $dynamicRef finds detached $dynamicAnchor/number is valid":                                                                 "codegen produces code that fails to compile for validation binary",
	"draft2020-12/dynamicRef/A $ref to a $dynamicAnchor in the same schema resource behaves like a normal $ref to an $anchor/An array of strings is valid":      "codegen produces code that fails to compile for validation binary",

	// non-object data: cannot unmarshal number into generated Go type (16 entries)
	"draft2019-09/optional/bignum/integer/a bignum is an integer":                                     "non-object data: cannot unmarshal number into generated Go type",
	"draft2019-09/optional/bignum/integer/a negative bignum is an integer":                            "non-object data: cannot unmarshal number into generated Go type",
	"draft2019-09/type/integer type matches integers/a float with zero fractional part is an integer": "non-object data: cannot unmarshal number into generated Go type",
	"draft2020-12/optional/bignum/integer/a bignum is an integer":                                     "non-object data: cannot unmarshal number into generated Go type",
	"draft2020-12/optional/bignum/integer/a negative bignum is an integer":                            "non-object data: cannot unmarshal number into generated Go type",
	"draft2020-12/type/integer type matches integers/a float with zero fractional part is an integer": "non-object data: cannot unmarshal number into generated Go type",
	"draft3/optional/bignum/integer/a bignum is an integer":                                           "non-object data: cannot unmarshal number into generated Go type",
	"draft3/optional/bignum/integer/a negative bignum is an integer":                                  "non-object data: cannot unmarshal number into generated Go type",
	"draft4/optional/bignum/integer/a bignum is an integer":                                           "non-object data: cannot unmarshal number into generated Go type",
	"draft4/optional/bignum/integer/a negative bignum is an integer":                                  "non-object data: cannot unmarshal number into generated Go type",
	"draft6/optional/bignum/integer/a bignum is an integer":                                           "non-object data: cannot unmarshal number into generated Go type",
	"draft6/optional/bignum/integer/a negative bignum is an integer":                                  "non-object data: cannot unmarshal number into generated Go type",
	"draft6/type/integer type matches integers/a float with zero fractional part is an integer":       "non-object data: cannot unmarshal number into generated Go type",
	"draft7/optional/bignum/integer/a bignum is an integer":                                           "non-object data: cannot unmarshal number into generated Go type",
	"draft7/optional/bignum/integer/a negative bignum is an integer":                                  "non-object data: cannot unmarshal number into generated Go type",
	"draft7/type/integer type matches integers/a float with zero fractional part is an integer":       "non-object data: cannot unmarshal number into generated Go type",

	// tuple items: Root type is []any, sub-item type constraints not validated (15 entries)
	"draft4/items/items and subitems/too many sub-items":       "tuple items: Root []any cannot validate sub-item structure",
	"draft4/items/items and subitems/wrong item":               "tuple items: Root []any cannot validate sub-item structure",
	"draft4/items/items and subitems/wrong sub-item":           "tuple items: Root []any cannot validate sub-item structure",
	"draft6/items/items and subitems/too many sub-items":       "tuple items: Root []any cannot validate sub-item structure",
	"draft6/items/items and subitems/wrong item":               "tuple items: Root []any cannot validate sub-item structure",
	"draft6/items/items and subitems/wrong sub-item":           "tuple items: Root []any cannot validate sub-item structure",
	"draft7/items/items and subitems/too many sub-items":       "tuple items: Root []any cannot validate sub-item structure",
	"draft7/items/items and subitems/wrong item":               "tuple items: Root []any cannot validate sub-item structure",
	"draft7/items/items and subitems/wrong sub-item":           "tuple items: Root []any cannot validate sub-item structure",
	"draft2019-09/items/items and subitems/too many sub-items": "tuple items: Root []any cannot validate sub-item structure",
	"draft2019-09/items/items and subitems/wrong item":         "tuple items: Root []any cannot validate sub-item structure",
	"draft2019-09/items/items and subitems/wrong sub-item":     "tuple items: Root []any cannot validate sub-item structure",
	"draft2020-12/items/items and subitems/too many sub-items": "tuple items: Root []any cannot validate sub-item structure",
	"draft2020-12/items/items and subitems/wrong item":         "tuple items: Root []any cannot validate sub-item structure",
	"draft2020-12/items/items and subitems/wrong sub-item":     "tuple items: Root []any cannot validate sub-item structure",

	// $dynamicRef: incorrect parent schema (1 entry, previously masked by wrong root type selection)
	"draft2020-12/dynamicRef/tests for implementation dynamic anchor and reference link/incorrect parent schema": "$dynamicRef/$dynamicAnchor not implemented",

	// over-strict validation: valid data rejected (1 additional entry)
	"draft3/type/applies a nested schema/an object is valid only if it is fully valid": "over-strict validation: valid data rejected",

	// ($ref percent-encoding — FIXED via URI fragment percent-decoding in LocalResolver)
}

// Flaky tests that non-deterministically pass/fail due to Go map iteration order
// in $anchor resolution. These are always skipped regardless of outcome.
var knownFlakyTests = map[string]bool{
	"draft2019-09/ref/order of evaluation: $id and $anchor and $ref":                                          true,
	"draft2019-09/ref/order of evaluation: $id and $anchor and $ref/data is valid against first definition":   true,
	"draft2019-09/ref/order of evaluation: $id and $anchor and $ref/data is invalid against first definition": true,
	"draft2020-12/ref/order of evaluation: $id and $anchor and $ref":                                          true,
	"draft2020-12/ref/order of evaluation: $id and $anchor and $ref/data is valid against first definition":   true,
	"draft2020-12/ref/order of evaluation: $id and $anchor and $ref/data is invalid against first definition": true,
}
