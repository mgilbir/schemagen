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

// Validation: known failures for Validate() correctness testing.
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
	// (URI-based $dynamicRef initial resolution — FIXED via removing fragment-only guard + cycle detection)
	// ($dynamicRef/$dynamicAnchor const validation — FIXED via const validation in resolvePropertyType)
	// (multiple dynamic paths via if/then/else — FIXED via runtime if/then/else + const validation)
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

	// $anchor/$recursiveRef resolution edge cases
	// (same $anchor with different base uri — FIXED via findAnchor $id scope boundary fix)
	// ($ref with $recursiveAnchor/extra items disallowed for root — FIXED via URI-based $dynamicRef support + cycle detection)

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

	// (disallow with inline sub-schema — FIXED via disallow array → not:anyOf normalization)

	// ($ref percent-encoding — FIXED via URI fragment percent-decoding in LocalResolver)

	// =========================================================================
	// Inferred type validation gaps (229 entries)
	// These schemas now generate a Validate() method via type inference from
	// structural keywords (items→array, required→object, etc.), but the
	// validation is too permissive — specific validation features are not yet
	// implemented. All failures are "expected INVALID but got VALID".
	// =========================================================================

	// (items/additionalItems/prefixItems: ALL 33 entries FIXED via InferredAliasDef item-level validation)

	// (contains/minContains/maxContains: ALL 79 entries FIXED via InferredAliasDef contains + items checks validation)

	// (dependentSchemas: ALL 38 entries FIXED via expanded DependentSchemaConstraint extraction)

	// (propertyNames: ALL 20 entries FIXED via PropertyNamesDef extraction + _jsonKeys validation)
	// ($ref to array: 6 of 12 entries FIXED via tuple item $ref resolution)
	// Remaining 6 refRemote entries: remote $ref item schemas can't be resolved at codegen time (too permissive).
	"draft3/refRemote/change resolution scope/changed scope ref invalid": "$ref to array: remote item schema not resolvable at codegen time",
	"draft4/refRemote/base URI change/base URI change ref invalid":       "$ref to array: remote item schema not resolvable at codegen time",
	"draft6/refRemote/base URI change/base URI change ref invalid":       "$ref to array: remote item schema not resolvable at codegen time",
	"draft7/refRemote/base URI change/base URI change ref invalid":       "$ref to array: remote item schema not resolvable at codegen time",
	"draft2019-09/refRemote/base URI change/base URI change ref invalid": "$ref to array: remote item schema not resolvable at codegen time",
	"draft2020-12/refRemote/base URI change/base URI change ref invalid": "$ref to array: remote item schema not resolvable at codegen time",

	// unevaluatedItems: runtime branch/annotation evaluation required (17 entries)
	// These tests require knowing which anyOf/oneOf/if-then-else branches actually
	// validate at runtime, or evaluating contains annotations in nested contexts.
	// (unevaluatedItems with if/then/else — FIXED via runtime if-condition evaluation with IfItemConstChecks)
	// (unevaluatedItems can see annotations from if without then and else — FIXED via IfEvalCount tracking)
	"draft2019-09/unevaluatedItems/unevaluatedItems with anyOf/when one schema matches and has unevaluated items":                             "unevaluatedItems: requires runtime anyOf branch evaluation",
	"draft2019-09/unevaluatedItems/unevaluatedItems with ignored additionalItems/invalid under unevaluatedItems":                              "unevaluatedItems: requires runtime additionalItems interaction",
	"draft2019-09/unevaluatedItems/unevaluatedItems with ignored applicator additionalItems/invalid under unevaluatedItems":                   "unevaluatedItems: requires runtime additionalItems interaction",
	"draft2019-09/unevaluatedItems/unevaluatedItems with nested items/with invalid additional item":                                           "unevaluatedItems: requires runtime anyOf branch evaluation",
	"draft2019-09/unevaluatedItems/unevaluatedItems can't see inside cousins/always fails":                                                    "unevaluatedItems: requires cousin scope isolation",
	"draft2020-12/unevaluatedItems/unevaluatedItems with nested items/with invalid additional item":                                           "unevaluatedItems: requires runtime anyOf branch evaluation",
	"draft2020-12/unevaluatedItems/unevaluatedItems with anyOf/when one schema matches and has unevaluated items":                             "unevaluatedItems: requires runtime anyOf branch evaluation",
	"draft2020-12/unevaluatedItems/unevaluatedItems can't see inside cousins/always fails":                                                    "unevaluatedItems: requires cousin scope isolation",
	"draft2020-12/unevaluatedItems/unevaluatedItems depends on multiple nested contains/7 not evaluated, fails unevaluatedItems":              "unevaluatedItems: requires runtime nested contains evaluation",
	"draft2020-12/unevaluatedItems/unevaluatedItems and contains interact to control item dependency relationship/only a's are valid":         "unevaluatedItems: requires runtime if/contains annotation propagation",
	"draft2020-12/unevaluatedItems/unevaluatedItems and contains interact to control item dependency relationship/a's and b's are valid":      "unevaluatedItems: requires runtime if/contains annotation propagation",
	"draft2020-12/unevaluatedItems/unevaluatedItems and contains interact to control item dependency relationship/a's, b's and c's are valid": "unevaluatedItems: requires runtime if/contains annotation propagation",

	// cross-draft: cross-draft ref processing issues (1 entry)
	// (draft2019-09/optional/cross-draft/refs to future drafts — FIXED via InferredAliasDef item validation)
	"draft2020-12/optional/cross-draft/refs to historic drafts are processed as historic drafts/first item not a string is valid": "cross-draft: $ref target not processed with correct schema dialect",
}

// Flaky tests that non-deterministically pass/fail due to Go map iteration order
// in $anchor resolution. These are always skipped regardless of outcome.
// (FIXED: all 6 entries removed — deterministic sorted-key iteration in allSubSchemas
// and scope-aware $anchor indexing in the generator now produce consistent results.)
var knownFlakyTests = map[string]bool{}
