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

// Validation: 32 known failures for Validate() correctness testing (2 flaky entries in knownFlakyTests).
// Only schemas that produce a Validate() method are tested; others are skipped.
// Only exercised entries are listed — schemas that generate type `any` (no Validate())
// are not tracked here since checkKnownFailure is never reached for them.
// Root causes:
//   - $ref to unknown keyword: unresolved ref falls back to any (8)
//   - $dynamicRef/$dynamicAnchor: dynamic scope resolution needed (7)
//   - $recursiveRef validation not implemented (1)
//   - unevaluatedItems validation not implemented (4)
//   - custom metaschema vocabulary not supported (2)
//   - $dynamicRef with required: dynamic scope needed (2)
//   - draft3/4 zeroTerminatedFloats: 1.0 accepted as integer by draft-agnostic unmarshal (2)
//   - unevaluatedProperties: $dynamicRef/$recursiveRef not implemented (2)
//   - $dynamicRef: static resolution picks wrong constraint (1)
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

	// $dynamicRef with required fields: $dynamicRef/$dynamicAnchor not fully implemented
	// (tests for implementation dynamic anchor and reference link/incorrect extended schema — FIXED via $dynamicRef static resolution)
	"draft2020-12/dynamicRef/$ref and $dynamicAnchor are independent of order - $defs first/incorrect extended schema": "$dynamicRef with required: $dynamicRef not implemented",
	"draft2020-12/dynamicRef/$ref and $dynamicAnchor are independent of order - $ref first/incorrect extended schema":  "$dynamicRef with required: $dynamicRef not implemented",

	// $dynamicRef/$dynamicAnchor: remaining failures requiring dynamic scope resolution (7 entries)
	// ($dynamicRef to a $dynamicAnchor in same resource — FIXED via $dynamicRef static resolution)
	// ($dynamicRef to an $anchor in same resource — FIXED via $dynamicRef static resolution)
	// ($dynamicRef skips over intermediate resources - direct reference — FIXED via $dynamicRef static resolution)
	// ($dynamicRef skips over intermediate resources - pointer reference — FIXED via $dynamicRef static resolution)
	"draft2020-12/dynamicRef/$dynamicRef points to a boolean schema/follow $dynamicRef to a false schema":                                                                                                                 "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/A $dynamicRef resolves to the first $dynamicAnchor still in scope that is encountered when the schema is evaluated/An array containing non-strings is invalid":                               "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/A $dynamicRef that initially resolves to a schema with a matching $dynamicAnchor resolves to the first $dynamicAnchor in the dynamic scope/The recursive part is not valid against the root": "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/A $dynamicRef with intermediate scopes that don't include a matching $dynamicAnchor does not affect dynamic scope resolution/An array containing non-strings is invalid":                     "$dynamicRef/$dynamicAnchor not implemented",
	// (A $dynamicRef without anchor in fragment — FIXED via JSON pointer $dynamicRef static resolution)
	"draft2020-12/dynamicRef/multiple dynamic paths to the $dynamicRef keyword/number list with string values":        "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/multiple dynamic paths to the $dynamicRef keyword/string list with number values":        "$dynamicRef/$dynamicAnchor not implemented",
	"draft2020-12/dynamicRef/strict-tree schema, guards against misspelled properties/instance with misspelled field": "$dynamicRef/$dynamicAnchor not implemented",

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

	// $ref to unknown keyword: unresolved ref falls back to any, no type validation (8 entries)
	"draft2019-09/optional/refOfUnknownKeyword/reference of a root arbitrary keyword /mismatch":                             "$ref to unknown keyword: unresolved ref falls back to any",
	"draft2019-09/optional/refOfUnknownKeyword/reference of a root arbitrary keyword with encoded ref/mismatch":             "$ref to unknown keyword: unresolved ref falls back to any",
	"draft2019-09/optional/refOfUnknownKeyword/reference of an arbitrary keyword of a sub-schema with encoded ref/mismatch": "$ref to unknown keyword: unresolved ref falls back to any",
	"draft2019-09/optional/refOfUnknownKeyword/reference of an arbitrary keyword of a sub-schema/mismatch":                  "$ref to unknown keyword: unresolved ref falls back to any",
	"draft2020-12/optional/refOfUnknownKeyword/reference of a root arbitrary keyword /mismatch":                             "$ref to unknown keyword: unresolved ref falls back to any",
	"draft2020-12/optional/refOfUnknownKeyword/reference of a root arbitrary keyword with encoded ref/mismatch":             "$ref to unknown keyword: unresolved ref falls back to any",
	"draft2020-12/optional/refOfUnknownKeyword/reference of an arbitrary keyword of a sub-schema with encoded ref/mismatch": "$ref to unknown keyword: unresolved ref falls back to any",
	"draft2020-12/optional/refOfUnknownKeyword/reference of an arbitrary keyword of a sub-schema/mismatch":                  "$ref to unknown keyword: unresolved ref falls back to any",

	// ($ref to $dynamicRef finds detached $dynamicAnchor — codegen now compiles, generates type any, tests skipped)

	// $dynamicRef avoids root: codegen now compiles but static resolution picks wrong maxLength (1 entry)
	// (data is sufficient — FIXED: valid data correctly accepted under static resolution)
	"draft2020-12/dynamicRef/$dynamicRef avoids the root of each schema, but scopes are still registered/data is not sufficient for schema at second#/$defs/length": "$dynamicRef: static resolution picks wrong maxLength, needs dynamic scope",

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
