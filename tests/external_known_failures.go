package tests

// Known failures for external JSON Schema Test Suite tests.
// These are categorized by root cause. Bidirectional checking ensures
// that if a known failure starts passing, the test will error (remove from list).

// CodeGen: 0 known failures (2 flaky entries removed — non-deterministic map iteration)
var knownCodeGenFailures = map[string]string{}

// RoundTrip: 2 known failures (2 flaky entries removed — non-deterministic map iteration)
var knownRoundTripFailures = map[string]string{
	// (same $anchor with different base uri — FIXED via findAnchor $id scope boundary fix)
	"draft2020-12/unevaluatedProperties/unevaluatedProperties with $dynamicRef/with no unevaluated properties": "round-trip mismatch: $dynamicRef not implemented",
	"draft3/type/applies a nested schema/an object is valid only if it is fully valid":                         "non-structural schema: data shape incompatible with generated type",
}

// Parse: 0 known failures
var knownParseFailures = map[string]string{}

// Validation: 244 known failures for Validate() correctness testing.
// Only schemas that produce a Validate() method are tested; others are skipped.
// Only exercised entries are listed — schemas that generate type `any` (no Validate())
// are not tracked here since checkKnownFailure is never reached for them.
// Root causes:
//   - ($ref to unknown keyword: FIXED via Schema.Extensions + walkPath extension lookup) (0)
//   - $dynamicRef/$dynamicAnchor: dynamic scope resolution needed (4)
//   - $recursiveRef validation not implemented (1)
//   - unevaluatedItems validation not implemented (2)
//   - custom metaschema vocabulary not supported (2)
//   - ($dynamicRef with required — FIXED via dynamic scope chain) (0)
//   - draft3/4 zeroTerminatedFloats: 1.0 accepted as integer by draft-agnostic unmarshal (2)
//   - unevaluatedProperties: $dynamicRef/$recursiveRef not implemented (2)
//   - ($dynamicRef: static resolution picks wrong constraint — FIXED via dynamic scope chain) (0)
//   - cross-draft validation not supported (1)
//   - over-strict validation: valid data rejected (1)
//   - $dynamicRef: incorrect parent schema (1)
//   - (unevaluatedProperties cousin isolation: FIXED via per-branch annotation tracking) (24)
//   - (unevaluatedProperties dependentSchemas: FIXED via runtime conditional evaluation) (4)
//   - (unevaluatedProperties if/then/else: FIXED via runtime conditional evaluation) (6)
//   - (unevaluatedProperties anyOf: FIXED via runtime branch matching) (4)
//   - (unevaluatedProperties oneOf: FIXED via runtime branch matching + flattening) (10)
var knownValidationFailures = map[string]string{
	// (default keyword — FIXED via optional field presence tracking)

	// (float-overflow: FIXED via BigIntSupport for optional/float-overflow test files)

	// zeroTerminatedFloats optional test — draft3/4 treat 1.0 as non-integer, but our json.Number-based
	// UnmarshalJSON accepts it (correct for draft6+). Generated code is draft-agnostic.
	"draft3/optional/zeroTerminatedFloats/some languages do not distinguish between different types of numeric value/a float is not an integer even without fractional part": "draft3/4: 1.0 treated as integer by draft-agnostic json.Number unmarshal",
	"draft4/optional/zeroTerminatedFloats/some languages do not distinguish between different types of numeric value/a float is not an integer even without fractional part": "draft3/4: 1.0 treated as integer by draft-agnostic json.Number unmarshal",

	// (patternProperties sub-schema validation — FIXED via ppMinItems/ppMaxItems/ppMinLength/ppMaxLength/ppPattern)
	// (additionalProperty invalidates others — FIXED via schema validation on overflow map)
	// (type-inferred schema — FIXED via InferredAliasDef wrapper struct)
	// ($id/$ref evaluation order — FIXED via InferredAliasDef ref handling)
	// (no $schema validation — FIXED via InferredAliasDef wrapper struct)

	// custom metaschema vocabulary not supported — vocabulary that disables validation
	"draft2019-09/vocabulary/schema that uses custom metaschema with with no validation vocabulary/no validation: invalid number, but it still validates": "custom metaschema vocabulary not supported",
	"draft2020-12/vocabulary/schema that uses custom metaschema with with no validation vocabulary/no validation: invalid number, but it still validates": "custom metaschema vocabulary not supported",

	// (enum in properties — FIXED via validatable field dispatch)

	// ($dynamicRef with required fields — FIXED via dynamic scope chain resolution)
	// (tests for implementation dynamic anchor and reference link/incorrect extended schema — FIXED via $dynamicRef static resolution)
	// ($ref and $dynamicAnchor are independent of order — FIXED via dynamic scope chain resolution)

	// $dynamicRef/$dynamicAnchor: remaining failures (4 entries)
	// ($dynamicRef to a $dynamicAnchor in same resource — FIXED via $dynamicRef static resolution)
	// ($dynamicRef to an $anchor in same resource — FIXED via $dynamicRef static resolution)
	// ($dynamicRef skips over intermediate resources - direct reference — FIXED via $dynamicRef static resolution)
	// ($dynamicRef skips over intermediate resources - pointer reference — FIXED via $dynamicRef static resolution)
	// (A $dynamicRef resolves to the first $dynamicAnchor in scope — FIXED via dynamic scope chain)
	// (A $dynamicRef with intermediate scopes — FIXED via dynamic scope chain)
	// (A $dynamicRef without anchor in fragment — FIXED via JSON pointer $dynamicRef static resolution)
	// ($dynamicRef points to boolean false schema — FIXED via resolvedToFalseSchema check)
	"draft2020-12/dynamicRef/A $dynamicRef that initially resolves to a schema with a matching $dynamicAnchor resolves to the first $dynamicAnchor in the dynamic scope/The recursive part is not valid against the root": "$dynamicRef/$dynamicAnchor: URI-based $dynamicRef with runtime scope",
	"draft2020-12/dynamicRef/multiple dynamic paths to the $dynamicRef keyword/number list with string values":                                                                                                            "$dynamicRef/$dynamicAnchor: runtime dynamic scope via if/then/else",
	"draft2020-12/dynamicRef/multiple dynamic paths to the $dynamicRef keyword/string list with number values":                                                                                                            "$dynamicRef/$dynamicAnchor: runtime dynamic scope via if/then/else",
	// (strict-tree misspelled field: FIXED via $ref sibling allOf synthesis for unevaluatedProperties + recursive slice validation)

	// ($ref sibling keyword validation — ALL FIXED via $ref sibling allOf synthesis + $ref chain following in mergeAllOfInto)
	// (draft2019-09/ref/ref creates new scope — FIXED via $ref sibling allOf synthesis)
	// (draft2019-09/ref/refs with relative uris and defs — FIXED via $ref sibling allOf synthesis)
	// (draft2019-09/ref/relative refs with absolute uris and defs — FIXED via $ref sibling allOf synthesis)
	// (draft2019-09 URN base URI with $ref — FIXED via non-object validation)
	// (draft2020-12/ref/ref creates new scope — FIXED via $ref sibling allOf synthesis)
	// (draft2020-12/ref/refs with relative uris and defs — FIXED via $ref sibling allOf synthesis)
	// (draft2020-12/ref/relative refs with absolute uris and defs — FIXED via $ref sibling allOf synthesis)
	// (draft2020-12 URN base URI with $ref — FIXED via non-object validation)
	// (draft6/7 refs with relative/absolute uris and defs — FIXED via allOf property resolution in resolveType)
	// (draft7 URN base URI with $ref — FIXED via non-object validation)

	// (additionalProperties: allOf interaction — FIXED via OwnPropertyNames scope isolation)

	// $anchor/$recursiveRef resolution edge cases (1 entry)
	// (same $anchor with different base uri — FIXED via findAnchor $id scope boundary fix)
	"draft2019-09/ref/$ref with $recursiveAnchor/extra items disallowed for root": "$recursiveRef validation not implemented",

	// ($ref to unknown keyword: ALL FIXED via Schema.Extensions + walkPath extension lookup — 8 entries removed)

	// ($ref to $dynamicRef finds detached $dynamicAnchor — codegen now compiles, generates type any, tests skipped)

	// ($dynamicRef avoids root — FIXED via dynamic scope chain resolution)

	// cross-draft validation not supported (1 entries)
	"draft7/optional/cross-draft/refs to future drafts are processed as future drafts/missing bar is invalid": "cross-draft validation not supported",

	// (draft3 enum required-as-boolean — FIXED via draft3 required normalization)

	// (extends validation — FIXED via draft3 required normalization + extends→allOf)

	// required with composition validation not implemented (1 entries)
	// (draft3/required — FIXED via draft3 required normalization)

	// unevaluatedItems validation not implemented (2 entries)
	// (uncle keyword isolation: FIXED via unevaluatedItems:false maxItems inference — 2 entries removed)
	"draft2019-09/unevaluatedItems/unevaluatedItems with $recursiveRef/with unevaluated items": "unevaluatedItems validation not implemented",
	"draft2020-12/unevaluatedItems/unevaluatedItems with $dynamicRef/with unevaluated items":   "unevaluatedItems validation not implemented",

	// unevaluatedProperties: remaining failures (2 entries)
	// (Cousin/uncle isolation: FIXED via per-branch annotation tracking — 24 entries removed)
	// (if/then/else: FIXED via runtime conditional evaluation — 6 entries removed)
	// (anyOf: FIXED via runtime branch matching — 4 entries removed)
	// (oneOf: FIXED via runtime branch matching + recursive flattening — 10 entries removed)
	// (unevaluatedProperties: schema-valued — FIXED via Validations + ValueType on UnevaluatedPropertiesDef)
	// (dependentSchemas: FIXED via runtime conditional evaluation — 4 entries removed)
	// Remaining unevaluatedProperties failures: $dynamicRef/$recursiveRef (2)
	"draft2019-09/unevaluatedProperties/unevaluatedProperties with $recursiveRef/with unevaluated properties": "unevaluatedProperties: $recursiveRef not implemented",
	"draft2020-12/unevaluatedProperties/unevaluatedProperties with $dynamicRef/with unevaluated properties":   "unevaluatedProperties: $dynamicRef not implemented",

	// ($ref to $dynamicRef finds detached $dynamicAnchor/number is valid — codegen now compiles, generates type any, tests skipped)
	// ($dynamicRef avoids root/data is sufficient — FIXED via $dynamicRef static resolution)

	// (bignum integer: FIXED via BigIntAliasDef wrapper with int64 + *big.Int)
	// (tuple items: FIXED via per-position tuple validation in Validate())

	// $dynamicRef: incorrect parent schema (1 entry, previously masked by wrong root type selection)
	"draft2020-12/dynamicRef/tests for implementation dynamic anchor and reference link/incorrect parent schema": "$dynamicRef/$dynamicAnchor not implemented",

	// over-strict validation: valid data rejected (1 additional entry)
	"draft3/type/applies a nested schema/an object is valid only if it is fully valid": "over-strict validation: valid data rejected",

	// ($ref percent-encoding — FIXED via URI fragment percent-decoding in LocalResolver)

	// =========================================================================
	// Inferred type validation gaps (229 entries)
	// These schemas now generate a Validate() method via type inference from
	// structural keywords (items→array, required→object, etc.), but the
	// validation is too permissive — specific validation features are not yet
	// implemented. All failures are "expected INVALID but got VALID".
	// =========================================================================

	// items/additionalItems/prefixItems: item-level type validation not implemented (33 entries)
	// The InferredAliasDef wrapper stores non-matching data as raw and skips validation,
	// but doesn't validate individual array items against the items sub-schema.
	"draft3/additionalItems/additionalItems as schema/additional items do not match schema":                                  "items: item-level type validation not implemented",
	"draft3/items/a schema given for items/wrong type of items":                                                              "items: item-level type validation not implemented",
	"draft3/items/an array of schemas for items/wrong types":                                                                 "items: item-level type validation not implemented",
	"draft4/additionalItems/additionalItems as schema/additional items do not match schema":                                  "items: item-level type validation not implemented",
	"draft4/additionalItems/items validation adjusts the starting index for additionalItems/wrong type of second item":       "items: item-level type validation not implemented",
	"draft4/items/a schema given for items/wrong type of items":                                                              "items: item-level type validation not implemented",
	"draft4/items/an array of schemas for items/wrong types":                                                                 "items: item-level type validation not implemented",
	"draft6/additionalItems/additionalItems as schema/additional items do not match schema":                                  "items: item-level type validation not implemented",
	"draft6/additionalItems/items validation adjusts the starting index for additionalItems/wrong type of second item":       "items: item-level type validation not implemented",
	"draft6/additionalItems/when items is schema, additionalItems does nothing/invalid with a array of mixed types":          "items: item-level type validation not implemented",
	"draft6/items/a schema given for items/wrong type of items":                                                              "items: item-level type validation not implemented",
	"draft6/items/an array of schemas for items/wrong types":                                                                 "items: item-level type validation not implemented",
	"draft6/items/items with boolean schema (false)/any non-empty array is invalid":                                          "items: item-level type validation not implemented",
	"draft6/items/items with boolean schemas/array with two items is invalid":                                                "items: item-level type validation not implemented",
	"draft7/additionalItems/additionalItems as schema/additional items do not match schema":                                  "items: item-level type validation not implemented",
	"draft7/additionalItems/items validation adjusts the starting index for additionalItems/wrong type of second item":       "items: item-level type validation not implemented",
	"draft7/additionalItems/when items is schema, additionalItems does nothing/invalid with a array of mixed types":          "items: item-level type validation not implemented",
	"draft7/items/a schema given for items/wrong type of items":                                                              "items: item-level type validation not implemented",
	"draft7/items/an array of schemas for items/wrong types":                                                                 "items: item-level type validation not implemented",
	"draft7/items/items with boolean schema (false)/any non-empty array is invalid":                                          "items: item-level type validation not implemented",
	"draft7/items/items with boolean schemas/array with two items is invalid":                                                "items: item-level type validation not implemented",
	"draft2019-09/additionalItems/additionalItems as schema/additional items do not match schema":                            "items: item-level type validation not implemented",
	"draft2019-09/additionalItems/items validation adjusts the starting index for additionalItems/wrong type of second item": "items: item-level type validation not implemented",
	"draft2019-09/additionalItems/when items is schema, additionalItems does nothing/invalid with a array of mixed types":    "items: item-level type validation not implemented",
	"draft2019-09/items/a schema given for items/wrong type of items":                                                        "items: item-level type validation not implemented",
	"draft2019-09/items/an array of schemas for items/wrong types":                                                           "items: item-level type validation not implemented",
	"draft2019-09/items/items with boolean schema (false)/any non-empty array is invalid":                                    "items: item-level type validation not implemented",
	"draft2019-09/items/items with boolean schemas/array with two items is invalid":                                          "items: item-level type validation not implemented",
	"draft2020-12/items/a schema given for items/wrong type of items":                                                        "items: item-level type validation not implemented",
	"draft2020-12/items/items with boolean schema (false)/any non-empty array is invalid":                                    "items: item-level type validation not implemented",
	"draft2020-12/items/prefixItems validation adjusts the starting index for items/wrong type of second item":               "items: item-level type validation not implemented",
	"draft2020-12/prefixItems/a schema given for prefixItems/wrong types":                                                    "items: item-level type validation not implemented",
	"draft2020-12/prefixItems/prefixItems with boolean schemas/array with two items is invalid":                              "items: item-level type validation not implemented",

	// contains: contains keyword validation not implemented (39 entries)
	"draft6/contains/contains keyword validation/array without items matching schema is invalid":       "contains: validation not implemented",
	"draft6/contains/contains keyword validation/empty array is invalid":                               "contains: validation not implemented",
	"draft6/contains/contains keyword with boolean schema false/any non-empty array is invalid":        "contains: validation not implemented",
	"draft6/contains/contains keyword with boolean schema false/empty array is invalid":                "contains: validation not implemented",
	"draft6/contains/contains keyword with boolean schema true/empty array is invalid":                 "contains: validation not implemented",
	"draft6/contains/contains keyword with const keyword/array without item 5 is invalid":              "contains: validation not implemented",
	"draft6/contains/items + contains/does not match items, matches contains":                          "contains: validation not implemented",
	"draft6/contains/items + contains/matches items, does not match contains":                          "contains: validation not implemented",
	"draft6/contains/items + contains/matches neither items nor contains":                              "contains: validation not implemented",
	"draft7/contains/contains keyword validation/array without items matching schema is invalid":       "contains: validation not implemented",
	"draft7/contains/contains keyword validation/empty array is invalid":                               "contains: validation not implemented",
	"draft7/contains/contains keyword with boolean schema false/any non-empty array is invalid":        "contains: validation not implemented",
	"draft7/contains/contains keyword with boolean schema false/empty array is invalid":                "contains: validation not implemented",
	"draft7/contains/contains keyword with boolean schema true/empty array is invalid":                 "contains: validation not implemented",
	"draft7/contains/contains keyword with const keyword/array without item 5 is invalid":              "contains: validation not implemented",
	"draft7/contains/contains with false if subschema/empty array is invalid":                          "contains: validation not implemented",
	"draft7/contains/items + contains/does not match items, matches contains":                          "contains: validation not implemented",
	"draft7/contains/items + contains/matches items, does not match contains":                          "contains: validation not implemented",
	"draft7/contains/items + contains/matches neither items nor contains":                              "contains: validation not implemented",
	"draft2019-09/contains/contains keyword validation/array without items matching schema is invalid": "contains: validation not implemented",
	"draft2019-09/contains/contains keyword validation/empty array is invalid":                         "contains: validation not implemented",
	"draft2019-09/contains/contains keyword with boolean schema false/any non-empty array is invalid":  "contains: validation not implemented",
	"draft2019-09/contains/contains keyword with boolean schema false/empty array is invalid":          "contains: validation not implemented",
	"draft2019-09/contains/contains keyword with boolean schema true/empty array is invalid":           "contains: validation not implemented",
	"draft2019-09/contains/contains keyword with const keyword/array without item 5 is invalid":        "contains: validation not implemented",
	"draft2019-09/contains/contains with false if subschema/empty array is invalid":                    "contains: validation not implemented",
	"draft2019-09/contains/items + contains/does not match items, matches contains":                    "contains: validation not implemented",
	"draft2019-09/contains/items + contains/matches items, does not match contains":                    "contains: validation not implemented",
	"draft2019-09/contains/items + contains/matches neither items nor contains":                        "contains: validation not implemented",
	"draft2020-12/contains/contains keyword validation/array without items matching schema is invalid": "contains: validation not implemented",
	"draft2020-12/contains/contains keyword validation/empty array is invalid":                         "contains: validation not implemented",
	"draft2020-12/contains/contains keyword with boolean schema false/any non-empty array is invalid":  "contains: validation not implemented",
	"draft2020-12/contains/contains keyword with boolean schema false/empty array is invalid":          "contains: validation not implemented",
	"draft2020-12/contains/contains keyword with boolean schema true/empty array is invalid":           "contains: validation not implemented",
	"draft2020-12/contains/contains keyword with const keyword/array without item 5 is invalid":        "contains: validation not implemented",
	"draft2020-12/contains/contains with false if subschema/empty array is invalid":                    "contains: validation not implemented",
	"draft2020-12/contains/items + contains/does not match items, matches contains":                    "contains: validation not implemented",
	"draft2020-12/contains/items + contains/matches items, does not match contains":                    "contains: validation not implemented",
	"draft2020-12/contains/items + contains/matches neither items nor contains":                        "contains: validation not implemented",

	// minContains/maxContains: validation not implemented (40 entries)
	"draft2019-09/maxContains/maxContains with contains/all elements match, invalid maxContains":                            "minContains/maxContains: validation not implemented",
	"draft2019-09/maxContains/maxContains with contains/empty data":                                                         "minContains/maxContains: validation not implemented",
	"draft2019-09/maxContains/maxContains with contains/some elements match, invalid maxContains":                           "minContains/maxContains: validation not implemented",
	"draft2019-09/maxContains/maxContains with contains, value with a decimal/too many elements match, invalid maxContains": "minContains/maxContains: validation not implemented",
	"draft2019-09/maxContains/minContains < maxContains/actual < minContains < maxContains":                                 "minContains/maxContains: validation not implemented",
	"draft2019-09/maxContains/minContains < maxContains/minContains < maxContains < actual":                                 "minContains/maxContains: validation not implemented",
	"draft2019-09/minContains/maxContains = minContains/all elements match, invalid maxContains":                            "minContains/maxContains: validation not implemented",
	"draft2019-09/minContains/maxContains = minContains/all elements match, invalid minContains":                            "minContains/maxContains: validation not implemented",
	"draft2019-09/minContains/maxContains = minContains/empty data":                                                         "minContains/maxContains: validation not implemented",
	"draft2019-09/minContains/maxContains < minContains/empty data":                                                         "minContains/maxContains: validation not implemented",
	"draft2019-09/minContains/maxContains < minContains/invalid maxContains":                                                "minContains/maxContains: validation not implemented",
	"draft2019-09/minContains/maxContains < minContains/invalid maxContains and minContains":                                "minContains/maxContains: validation not implemented",
	"draft2019-09/minContains/maxContains < minContains/invalid minContains":                                                "minContains/maxContains: validation not implemented",
	"draft2019-09/minContains/minContains = 0 with maxContains/too many":                                                    "minContains/maxContains: validation not implemented",
	"draft2019-09/minContains/minContains=1 with contains/empty data":                                                       "minContains/maxContains: validation not implemented",
	"draft2019-09/minContains/minContains=1 with contains/no elements match":                                                "minContains/maxContains: validation not implemented",
	"draft2019-09/minContains/minContains=2 with contains/all elements match, invalid minContains":                          "minContains/maxContains: validation not implemented",
	"draft2019-09/minContains/minContains=2 with contains/empty data":                                                       "minContains/maxContains: validation not implemented",
	"draft2019-09/minContains/minContains=2 with contains/some elements match, invalid minContains":                         "minContains/maxContains: validation not implemented",
	"draft2019-09/minContains/minContains=2 with contains with a decimal value/one element matches, invalid minContains":    "minContains/maxContains: validation not implemented",
	"draft2020-12/maxContains/maxContains with contains/all elements match, invalid maxContains":                            "minContains/maxContains: validation not implemented",
	"draft2020-12/maxContains/maxContains with contains/empty data":                                                         "minContains/maxContains: validation not implemented",
	"draft2020-12/maxContains/maxContains with contains/some elements match, invalid maxContains":                           "minContains/maxContains: validation not implemented",
	"draft2020-12/maxContains/maxContains with contains, value with a decimal/too many elements match, invalid maxContains": "minContains/maxContains: validation not implemented",
	"draft2020-12/maxContains/minContains < maxContains/actual < minContains < maxContains":                                 "minContains/maxContains: validation not implemented",
	"draft2020-12/maxContains/minContains < maxContains/minContains < maxContains < actual":                                 "minContains/maxContains: validation not implemented",
	"draft2020-12/minContains/maxContains = minContains/all elements match, invalid maxContains":                            "minContains/maxContains: validation not implemented",
	"draft2020-12/minContains/maxContains = minContains/all elements match, invalid minContains":                            "minContains/maxContains: validation not implemented",
	"draft2020-12/minContains/maxContains = minContains/empty data":                                                         "minContains/maxContains: validation not implemented",
	"draft2020-12/minContains/maxContains < minContains/empty data":                                                         "minContains/maxContains: validation not implemented",
	"draft2020-12/minContains/maxContains < minContains/invalid maxContains":                                                "minContains/maxContains: validation not implemented",
	"draft2020-12/minContains/maxContains < minContains/invalid maxContains and minContains":                                "minContains/maxContains: validation not implemented",
	"draft2020-12/minContains/maxContains < minContains/invalid minContains":                                                "minContains/maxContains: validation not implemented",
	"draft2020-12/minContains/minContains = 0 with maxContains/too many":                                                    "minContains/maxContains: validation not implemented",
	"draft2020-12/minContains/minContains=1 with contains/empty data":                                                       "minContains/maxContains: validation not implemented",
	"draft2020-12/minContains/minContains=1 with contains/no elements match":                                                "minContains/maxContains: validation not implemented",
	"draft2020-12/minContains/minContains=2 with contains/all elements match, invalid minContains":                          "minContains/maxContains: validation not implemented",
	"draft2020-12/minContains/minContains=2 with contains/empty data":                                                       "minContains/maxContains: validation not implemented",
	"draft2020-12/minContains/minContains=2 with contains/some elements match, invalid minContains":                         "minContains/maxContains: validation not implemented",
	"draft2020-12/minContains/minContains=2 with contains with a decimal value/one element matches, invalid minContains":    "minContains/maxContains: validation not implemented",

	// dependentSchemas: sub-schema validation not implemented for property-less objects (38 entries)
	// The generator only handles dependentSchemas with additionalProperties:false;
	// schemas with typed property requirements are not validated.
	"draft3/dependencies/multiple dependencies subschema/wrong type":                                                                           "dependentSchemas: sub-schema property validation not implemented",
	"draft3/dependencies/multiple dependencies subschema/wrong type both":                                                                      "dependentSchemas: sub-schema property validation not implemented",
	"draft3/dependencies/multiple dependencies subschema/wrong type other":                                                                     "dependentSchemas: sub-schema property validation not implemented",
	"draft4/dependencies/dependencies with escaped characters/invalid object 2":                                                                "dependentSchemas: sub-schema property validation not implemented",
	"draft4/dependencies/dependencies with escaped characters/invalid object 3":                                                                "dependentSchemas: sub-schema property validation not implemented",
	"draft4/dependencies/multiple dependencies subschema/wrong type":                                                                           "dependentSchemas: sub-schema property validation not implemented",
	"draft4/dependencies/multiple dependencies subschema/wrong type both":                                                                      "dependentSchemas: sub-schema property validation not implemented",
	"draft4/dependencies/multiple dependencies subschema/wrong type other":                                                                     "dependentSchemas: sub-schema property validation not implemented",
	"draft6/dependencies/dependencies with boolean subschemas/object with both properties is invalid":                                          "dependentSchemas: sub-schema property validation not implemented",
	"draft6/dependencies/dependencies with boolean subschemas/object with property having schema false is invalid":                             "dependentSchemas: sub-schema property validation not implemented",
	"draft6/dependencies/dependencies with escaped characters/invalid object 2":                                                                "dependentSchemas: sub-schema property validation not implemented",
	"draft6/dependencies/dependencies with escaped characters/invalid object 3":                                                                "dependentSchemas: sub-schema property validation not implemented",
	"draft6/dependencies/multiple dependencies subschema/wrong type":                                                                           "dependentSchemas: sub-schema property validation not implemented",
	"draft6/dependencies/multiple dependencies subschema/wrong type both":                                                                      "dependentSchemas: sub-schema property validation not implemented",
	"draft6/dependencies/multiple dependencies subschema/wrong type other":                                                                     "dependentSchemas: sub-schema property validation not implemented",
	"draft7/dependencies/dependencies with boolean subschemas/object with both properties is invalid":                                          "dependentSchemas: sub-schema property validation not implemented",
	"draft7/dependencies/dependencies with boolean subschemas/object with property having schema false is invalid":                             "dependentSchemas: sub-schema property validation not implemented",
	"draft7/dependencies/dependencies with escaped characters/invalid object 2":                                                                "dependentSchemas: sub-schema property validation not implemented",
	"draft7/dependencies/dependencies with escaped characters/invalid object 3":                                                                "dependentSchemas: sub-schema property validation not implemented",
	"draft7/dependencies/multiple dependencies subschema/wrong type":                                                                           "dependentSchemas: sub-schema property validation not implemented",
	"draft7/dependencies/multiple dependencies subschema/wrong type both":                                                                      "dependentSchemas: sub-schema property validation not implemented",
	"draft7/dependencies/multiple dependencies subschema/wrong type other":                                                                     "dependentSchemas: sub-schema property validation not implemented",
	"draft2019-09/dependentSchemas/boolean subschemas/object with both properties is invalid":                                                  "dependentSchemas: sub-schema property validation not implemented",
	"draft2019-09/dependentSchemas/boolean subschemas/object with property having schema false is invalid":                                     "dependentSchemas: sub-schema property validation not implemented",
	"draft2019-09/dependentSchemas/dependencies with escaped characters/quoted quote":                                                          "dependentSchemas: sub-schema property validation not implemented",
	"draft2019-09/dependentSchemas/dependencies with escaped characters/quoted quote invalid under dependent schema":                           "dependentSchemas: sub-schema property validation not implemented",
	"draft2019-09/dependentSchemas/dependencies with escaped characters/quoted tab invalid under dependent schema":                             "dependentSchemas: sub-schema property validation not implemented",
	"draft2019-09/dependentSchemas/single dependency/wrong type":                                                                               "dependentSchemas: sub-schema property validation not implemented",
	"draft2019-09/dependentSchemas/single dependency/wrong type both":                                                                          "dependentSchemas: sub-schema property validation not implemented",
	"draft2019-09/dependentSchemas/single dependency/wrong type other":                                                                         "dependentSchemas: sub-schema property validation not implemented",
	"draft2019-09/optional/dependencies-compatibility/boolean subschemas/object with both properties is invalid":                               "dependentSchemas: sub-schema property validation not implemented",
	"draft2019-09/optional/dependencies-compatibility/boolean subschemas/object with property having schema false is invalid":                  "dependentSchemas: sub-schema property validation not implemented",
	"draft2019-09/optional/dependencies-compatibility/schema dependencies with escaped characters/quoted quote":                                "dependentSchemas: sub-schema property validation not implemented",
	"draft2019-09/optional/dependencies-compatibility/schema dependencies with escaped characters/quoted quote invalid under dependent schema": "dependentSchemas: sub-schema property validation not implemented",
	"draft2019-09/optional/dependencies-compatibility/schema dependencies with escaped characters/quoted tab invalid under dependent schema":   "dependentSchemas: sub-schema property validation not implemented",
	"draft2019-09/optional/dependencies-compatibility/single schema dependency/wrong type":                                                     "dependentSchemas: sub-schema property validation not implemented",
	"draft2019-09/optional/dependencies-compatibility/single schema dependency/wrong type both":                                                "dependentSchemas: sub-schema property validation not implemented",
	"draft2019-09/optional/dependencies-compatibility/single schema dependency/wrong type other":                                               "dependentSchemas: sub-schema property validation not implemented",
	"draft2020-12/dependentSchemas/boolean subschemas/object with both properties is invalid":                                                  "dependentSchemas: sub-schema property validation not implemented",
	"draft2020-12/dependentSchemas/boolean subschemas/object with property having schema false is invalid":                                     "dependentSchemas: sub-schema property validation not implemented",
	"draft2020-12/dependentSchemas/dependencies with escaped characters/quoted quote":                                                          "dependentSchemas: sub-schema property validation not implemented",
	"draft2020-12/dependentSchemas/dependencies with escaped characters/quoted quote invalid under dependent schema":                           "dependentSchemas: sub-schema property validation not implemented",
	"draft2020-12/dependentSchemas/dependencies with escaped characters/quoted tab invalid under dependent schema":                             "dependentSchemas: sub-schema property validation not implemented",
	"draft2020-12/dependentSchemas/single dependency/wrong type":                                                                               "dependentSchemas: sub-schema property validation not implemented",
	"draft2020-12/dependentSchemas/single dependency/wrong type both":                                                                          "dependentSchemas: sub-schema property validation not implemented",
	"draft2020-12/dependentSchemas/single dependency/wrong type other":                                                                         "dependentSchemas: sub-schema property validation not implemented",
	"draft2020-12/optional/dependencies-compatibility/boolean subschemas/object with both properties is invalid":                               "dependentSchemas: sub-schema property validation not implemented",
	"draft2020-12/optional/dependencies-compatibility/boolean subschemas/object with property having schema false is invalid":                  "dependentSchemas: sub-schema property validation not implemented",
	"draft2020-12/optional/dependencies-compatibility/schema dependencies with escaped characters/quoted quote":                                "dependentSchemas: sub-schema property validation not implemented",
	"draft2020-12/optional/dependencies-compatibility/schema dependencies with escaped characters/quoted quote invalid under dependent schema": "dependentSchemas: sub-schema property validation not implemented",
	"draft2020-12/optional/dependencies-compatibility/schema dependencies with escaped characters/quoted tab invalid under dependent schema":   "dependentSchemas: sub-schema property validation not implemented",
	"draft2020-12/optional/dependencies-compatibility/single schema dependency/wrong type":                                                     "dependentSchemas: sub-schema property validation not implemented",
	"draft2020-12/optional/dependencies-compatibility/single schema dependency/wrong type both":                                                "dependentSchemas: sub-schema property validation not implemented",
	"draft2020-12/optional/dependencies-compatibility/single schema dependency/wrong type other":                                               "dependentSchemas: sub-schema property validation not implemented",

	// propertyNames: property name validation not implemented (20 entries)
	"draft6/propertyNames/propertyNames validation/some property names invalid":                                "propertyNames: validation not implemented",
	"draft6/propertyNames/propertyNames validation with pattern/non-matching property name is invalid":         "propertyNames: validation not implemented",
	"draft6/propertyNames/propertyNames with boolean schema false/object with any properties is invalid":       "propertyNames: validation not implemented",
	"draft6/propertyNames/propertyNames with const/object with any other property is invalid":                  "propertyNames: validation not implemented",
	"draft6/propertyNames/propertyNames with enum/object with any other property is invalid":                   "propertyNames: validation not implemented",
	"draft7/propertyNames/propertyNames validation/some property names invalid":                                "propertyNames: validation not implemented",
	"draft7/propertyNames/propertyNames validation with pattern/non-matching property name is invalid":         "propertyNames: validation not implemented",
	"draft7/propertyNames/propertyNames with boolean schema false/object with any properties is invalid":       "propertyNames: validation not implemented",
	"draft7/propertyNames/propertyNames with const/object with any other property is invalid":                  "propertyNames: validation not implemented",
	"draft7/propertyNames/propertyNames with enum/object with any other property is invalid":                   "propertyNames: validation not implemented",
	"draft2019-09/propertyNames/propertyNames validation/some property names invalid":                          "propertyNames: validation not implemented",
	"draft2019-09/propertyNames/propertyNames validation with pattern/non-matching property name is invalid":   "propertyNames: validation not implemented",
	"draft2019-09/propertyNames/propertyNames with boolean schema false/object with any properties is invalid": "propertyNames: validation not implemented",
	"draft2019-09/propertyNames/propertyNames with const/object with any other property is invalid":            "propertyNames: validation not implemented",
	"draft2019-09/propertyNames/propertyNames with enum/object with any other property is invalid":             "propertyNames: validation not implemented",
	"draft2020-12/propertyNames/propertyNames validation/some property names invalid":                          "propertyNames: validation not implemented",
	"draft2020-12/propertyNames/propertyNames validation with pattern/non-matching property name is invalid":   "propertyNames: validation not implemented",
	"draft2020-12/propertyNames/propertyNames with boolean schema false/object with any properties is invalid": "propertyNames: validation not implemented",
	"draft2020-12/propertyNames/propertyNames with const/object with any other property is invalid":            "propertyNames: validation not implemented",
	"draft2020-12/propertyNames/propertyNames with enum/object with any other property is invalid":             "propertyNames: validation not implemented",

	// $ref to array: inferred array type cannot validate item types (12 entries)
	"draft3/ref/relative pointer ref to array/mismatch array":            "$ref to array: item-level validation not implemented",
	"draft3/refRemote/change resolution scope/changed scope ref invalid": "$ref to array: item-level validation not implemented",
	"draft4/ref/relative pointer ref to array/mismatch array":            "$ref to array: item-level validation not implemented",
	"draft4/refRemote/base URI change/base URI change ref invalid":       "$ref to array: item-level validation not implemented",
	"draft6/ref/relative pointer ref to array/mismatch array":            "$ref to array: item-level validation not implemented",
	"draft6/refRemote/base URI change/base URI change ref invalid":       "$ref to array: item-level validation not implemented",
	"draft7/ref/relative pointer ref to array/mismatch array":            "$ref to array: item-level validation not implemented",
	"draft7/refRemote/base URI change/base URI change ref invalid":       "$ref to array: item-level validation not implemented",
	"draft2019-09/ref/relative pointer ref to array/mismatch array":      "$ref to array: item-level validation not implemented",
	"draft2019-09/refRemote/base URI change/base URI change ref invalid": "$ref to array: item-level validation not implemented",
	"draft2020-12/ref/relative pointer ref to array/mismatch array":      "$ref to array: item-level validation not implemented",
	"draft2020-12/refRemote/base URI change/base URI change ref invalid": "$ref to array: item-level validation not implemented",

	// unevaluatedItems: validation not implemented for inferred arrays (30 entries)
	"draft2019-09/unevaluatedItems/unevaluatedItems as schema/with invalid unevaluated items":                                                   "unevaluatedItems: not implemented for inferred arrays",
	"draft2019-09/unevaluatedItems/unevaluatedItems before $ref/with unevaluated items":                                                         "unevaluatedItems: not implemented for inferred arrays",
	"draft2019-09/unevaluatedItems/unevaluatedItems can see annotations from if without then and else/invalid in case if is evaluated":          "unevaluatedItems: not implemented for inferred arrays",
	"draft2019-09/unevaluatedItems/unevaluatedItems false/with unevaluated items":                                                               "unevaluatedItems: not implemented for inferred arrays",
	"draft2019-09/unevaluatedItems/unevaluatedItems with $ref/with unevaluated items":                                                           "unevaluatedItems: not implemented for inferred arrays",
	"draft2019-09/unevaluatedItems/unevaluatedItems with anyOf/when one schema matches and has unevaluated items":                               "unevaluatedItems: not implemented for inferred arrays",
	"draft2019-09/unevaluatedItems/unevaluatedItems with anyOf/when two schemas match and has unevaluated items":                                "unevaluatedItems: not implemented for inferred arrays",
	"draft2019-09/unevaluatedItems/unevaluatedItems with if/then/else/when if doesn't match and it has unevaluated items":                       "unevaluatedItems: not implemented for inferred arrays",
	"draft2019-09/unevaluatedItems/unevaluatedItems with if/then/else/when if matches and it has unevaluated items":                             "unevaluatedItems: not implemented for inferred arrays",
	"draft2019-09/unevaluatedItems/unevaluatedItems with ignored additionalItems/invalid under unevaluatedItems":                                "unevaluatedItems: not implemented for inferred arrays",
	"draft2019-09/unevaluatedItems/unevaluatedItems with nested items/with invalid additional item":                                             "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems and contains interact to control item dependency relationship/only a's and c's are invalid": "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems and contains interact to control item dependency relationship/only b's and c's are invalid": "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems and contains interact to control item dependency relationship/only b's are invalid":         "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems and contains interact to control item dependency relationship/only c's are invalid":         "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems as schema/with invalid unevaluated items":                                                   "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems before $ref/with unevaluated items":                                                         "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems can see annotations from if without then and else/invalid in case if is evaluated":          "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems depends on adjacent contains/contains fails, second item is not evaluated":                  "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems depends on adjacent contains/contains passes, second item is not evaluated":                 "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems false/with unevaluated items":                                                               "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems with $ref/with unevaluated items":                                                           "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems with anyOf/when one schema matches and has unevaluated items":                               "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems with anyOf/when two schemas match and has unevaluated items":                                "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems with if/then/else/when if doesn't match and it has unevaluated items":                       "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems with if/then/else/when if matches and it has unevaluated items":                             "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems with items/invalid under items":                                                             "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems with minContains = 0/no items evaluated by contains":                                        "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems with minContains = 0/some but not all items evaluated by contains":                          "unevaluatedItems: not implemented for inferred arrays",
	"draft2020-12/unevaluatedItems/unevaluatedItems with nested items/with invalid additional item":                                             "unevaluatedItems: not implemented for inferred arrays",

	// cross-draft: inferred array in cross-draft ref (1 entry)
	"draft2019-09/optional/cross-draft/refs to future drafts are processed as future drafts/first item not a string is invalid": "cross-draft: item-level validation not implemented",
}

// Flaky tests that non-deterministically pass/fail due to Go map iteration order
// in $anchor resolution. These are always skipped regardless of outcome.
// (FIXED: all 6 entries removed — deterministic sorted-key iteration in allSubSchemas
// and scope-aware $anchor indexing in the generator now produce consistent results.)
var knownFlakyTests = map[string]bool{}
