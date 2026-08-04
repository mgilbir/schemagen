package tests

// Known failures for external JSON Schema Test Suite tests.
// These are categorized by root cause. Bidirectional checking ensures
// that if a known failure starts passing, the test will error (remove from list).

// CodeGen: 0 known failures (2 flaky entries removed — non-deterministic map iteration)
var knownCodeGenFailures = map[string]string{}

// RoundTrip: 0 known failures (2 flaky entries removed — non-deterministic map iteration)
var knownRoundTripFailures = map[string]string{
	// (same $anchor with different base uri — FIXED via findAnchor $id scope boundary fix)
	// (unevaluatedProperties with $dynamicRef — FIXED via dynamicRef evaluated-property collection + alias marshal delegation)
	// (draft3 schema-valued type alternative — FIXED via TypeOnlySchemaDef type branches)
}

// Parse: 0 known failures
var knownParseFailures = map[string]string{}

// Validation: known failures for Validate() correctness testing.
// Only schemas that produce a Validate() method are tested; others are skipped.
// Only exercised entries are listed — schemas that generate type `any` (no Validate())
// are not tracked here since checkKnownFailure is never reached for them.
// Root causes:
//   - ($ref to unknown keyword: FIXED via Schema.Extensions + walkPath extension lookup) (0)
//   - $dynamicRef/$dynamicAnchor: dynamic scope resolution needed (0)
//   - $recursiveRef validation not implemented (0)
//   - unevaluatedItems validation not implemented (0)
//   - custom metaschema vocabulary not supported (0)
//   - ($dynamicRef with required — FIXED via dynamic scope chain) (0)
//   - (draft3/4 zeroTerminatedFloats: FIXED via draft-aware strict integer tokens) (0)
//   - unevaluatedProperties: $recursiveRef not implemented (0)
//   - ($dynamicRef: static resolution picks wrong constraint — FIXED via dynamic scope chain) (0)
//   - cross-draft validation not supported (0)
//   - over-strict validation: valid data rejected (0)
//   - ($dynamicRef: incorrect parent schema: FIXED via alias unmarshal/validation delegation) (0)
//   - (unevaluatedProperties cousin isolation: FIXED via per-branch annotation tracking) (24)
//   - (unevaluatedProperties dependentSchemas: FIXED via runtime conditional evaluation) (4)
//   - (unevaluatedProperties if/then/else: FIXED via runtime conditional evaluation) (6)
//   - (unevaluatedProperties anyOf: FIXED via runtime branch matching) (4)
//   - (unevaluatedProperties oneOf: FIXED via runtime branch matching + flattening) (10)
var knownValidationFailures = map[string]string{
	// (default keyword — FIXED via optional field presence tracking)

	// (float-overflow: FIXED via BigIntSupport for optional/float-overflow test files)

	// (zeroTerminatedFloats optional test — FIXED via draft-aware strict integer tokens)

	// (patternProperties sub-schema validation — FIXED via ppMinItems/ppMaxItems/ppMinLength/ppMaxLength/ppPattern)
	// (additionalProperty invalidates others — FIXED via schema validation on overflow map)
	// (type-inferred schema — FIXED via InferredAliasDef wrapper struct)
	// ($id/$ref evaluation order — FIXED via InferredAliasDef ref handling)
	// (no $schema validation — FIXED via InferredAliasDef wrapper struct)

	// (custom metaschema without validation vocabulary — FIXED via validation vocabulary opt-out)

	// (enum in properties — FIXED via validatable field dispatch)

	// ($dynamicRef with required fields — FIXED via dynamic scope chain resolution)
	// (tests for implementation dynamic anchor and reference link/incorrect extended schema — FIXED via $dynamicRef static resolution)
	// ($ref and $dynamicAnchor are independent of order — FIXED via dynamic scope chain resolution)

	// $dynamicRef/$dynamicAnchor: remaining failures (0 entries)
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

	// (cross-draft dependentRequired — FIXED via resource-dialect-aware allOf merging)

	// (draft3 enum required-as-boolean — FIXED via draft3 required normalization)

	// (extends validation — FIXED via draft3 required normalization + extends→allOf)

	// required with composition validation not implemented (1 entries)
	// (draft3/required — FIXED via draft3 required normalization)

	// unevaluatedItems validation not implemented (0 entries)
	// (uncle keyword isolation: FIXED via unevaluatedItems:false maxItems inference — 2 entries removed)
	// (draft2019-09 unevaluatedItems with $recursiveRef — FIXED via recursive ref item evaluation)
	// (draft2020-12 unevaluatedItems with $dynamicRef — FIXED via dynamicRef item evaluation)

	// unevaluatedProperties: remaining failures (0 entries)
	// (Cousin/uncle isolation: FIXED via per-branch annotation tracking — 24 entries removed)
	// (if/then/else: FIXED via runtime conditional evaluation — 6 entries removed)
	// (anyOf: FIXED via runtime branch matching — 4 entries removed)
	// (oneOf: FIXED via runtime branch matching + recursive flattening — 10 entries removed)
	// (unevaluatedProperties: schema-valued — FIXED via Validations + ValueType on UnevaluatedPropertiesDef)
	// (dependentSchemas: FIXED via runtime conditional evaluation — 4 entries removed)
	// (draft2019-09 unevaluatedProperties with $recursiveRef — FIXED via ref sibling wrapper generation)
	// (draft2020-12 unevaluatedProperties with $dynamicRef — FIXED via dynamicRef evaluated-property collection)

	// ($ref to $dynamicRef finds detached $dynamicAnchor/number is valid — codegen now compiles, generates type any, tests skipped)
	// ($dynamicRef avoids root/data is sufficient — FIXED via $dynamicRef static resolution)

	// (bignum integer: FIXED via BigIntAliasDef wrapper with int64 + *big.Int)
	// (tuple items: FIXED via per-position tuple validation in Validate())

	// ($dynamicRef: incorrect parent schema — FIXED via alias unmarshal/validation delegation)

	// (draft3 schema-valued type alternative — FIXED via TypeOnlySchemaDef type branches)

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
	// ($ref to array: ALL FIXED via tuple and nested item $ref resolution)

	// unevaluatedItems: runtime branch/annotation evaluation required (17 entries)
	// These tests require knowing which anyOf/oneOf/if-then-else branches actually
	// validate at runtime, or evaluating contains annotations in nested contexts.
	// (unevaluatedItems with if/then/else — FIXED via runtime if-condition evaluation with IfItemConstChecks)
	// (unevaluatedItems can see annotations from if without then and else — FIXED via IfEvalCount tracking)

	// cross-draft: cross-draft ref processing issues (0 entries)
	// (draft2019-09/optional/cross-draft/refs to future drafts — FIXED via InferredAliasDef item validation)
	// (draft2020-12/optional/cross-draft/refs to historic drafts — skipped: no Validate() method)
}

// Flaky tests that non-deterministically pass/fail due to Go map iteration order
// in $anchor resolution. These are always skipped regardless of outcome.
// (FIXED: all 6 entries removed — deterministic sorted-key iteration in allSubSchemas
// and scope-aware $anchor indexing in the generator now produce consistent results.)
var knownFlakyTests = map[string]bool{}

// Reasons for knownUnvalidatedRejections. Each names a root-schema shape that
// resolves to `any` — a bare interface carries no Validate, so nothing about
// the group is ever checked. They are constants rather than repeated literals
// so that fixing a shape is one grep and one delete, not 90.
const (
	gapRootFormatOnly       = `root states only "format", which names no Go type, so the root resolves to any and carries no Validate`
	gapRootCompositionOnly  = `root is composition alone (allOf/anyOf/oneOf/not) and states no type of its own, so the root resolves to any and carries no Validate`
	gapRootConditionalOnly  = `root is if/then/else alone and states no type of its own, so the root resolves to any and carries no Validate`
	gapRootRefToFalse       = `root is a bare $ref to a boolean false schema, which resolves to any rather than to a type that refuses everything`
	gapRootContentOnly      = `root states only contentEncoding/contentMediaType, which name no Go type, so the root resolves to any and carries no Validate`
	gapRootDependenciesOnly = `root states only draft3 dependencies, which names no Go type, so the root resolves to any and carries no Validate`
)

// knownUnvalidatedRejections allow-lists groups that produce no Validate()
// method while the suite marks at least one of their documents invalid.
//
// That combination is a provable defect, not a neutral skip: the generated code
// accepts a document the suite says must be rejected, and before this list
// existed it produced no subtest at all — it reached the reader only as a count
// in the closing t.Logf of a -v run. TestExternalValidation now fails on every
// such group whose key is absent here.
//
// The list is a ratchet, and it is bidirectional in the same way
// checkKnownFailure is. An entry whose group has started producing a Validate(),
// has stopped carrying a rejecting case, or has vanished from the corpus is
// reported as stale and must be removed: an allow-list entry that no longer
// allows anything is the same silence this test was written to end.
//
// The key is failureKey(draft, file-without-extension, group description) — the
// shape knownValidationFailures uses, minus the per-case suffix, because the
// skip happens once for the whole group, before any case runs.
//
// Measured 2026-08-03 against suite commit bce6a47: 154 of the 305 skipped
// groups. Sections are ordered by size; fixing a root shape prunes a whole
// section at once.
var knownUnvalidatedRejections = map[string]string{
	// gapRootFormatOnly (90 entries)
	"draft2019-09/optional/format/date-time/validation of date-time strings":                                 gapRootFormatOnly,
	"draft2019-09/optional/format/date/validation of date strings":                                           gapRootFormatOnly,
	"draft2019-09/optional/format/duration/validation of duration strings":                                   gapRootFormatOnly,
	"draft2019-09/optional/format/email/validation of e-mail addresses":                                      gapRootFormatOnly,
	"draft2019-09/optional/format/hostname/validation of A-label (punycode) host names":                      gapRootFormatOnly,
	"draft2019-09/optional/format/hostname/validation of host names":                                         gapRootFormatOnly,
	"draft2019-09/optional/format/idn-email/validation of an internationalized e-mail addresses":             gapRootFormatOnly,
	"draft2019-09/optional/format/idn-hostname/validation of internationalized host names":                   gapRootFormatOnly,
	"draft2019-09/optional/format/idn-hostname/validation of separators in internationalized host names":     gapRootFormatOnly,
	"draft2019-09/optional/format/ipv4/validation of IP addresses":                                           gapRootFormatOnly,
	"draft2019-09/optional/format/ipv6/validation of IPv6 addresses":                                         gapRootFormatOnly,
	"draft2019-09/optional/format/iri-reference/validation of IRI References":                                gapRootFormatOnly,
	"draft2019-09/optional/format/iri/validation of IRIs":                                                    gapRootFormatOnly,
	"draft2019-09/optional/format/json-pointer/validation of JSON-pointers (JSON String Representation)":     gapRootFormatOnly,
	"draft2019-09/optional/format/regex/validation of regular expressions":                                   gapRootFormatOnly,
	"draft2019-09/optional/format/relative-json-pointer/validation of Relative JSON Pointers (RJP)":          gapRootFormatOnly,
	"draft2019-09/optional/format/time/validation of time strings":                                           gapRootFormatOnly,
	"draft2019-09/optional/format/uri-reference/validation of URI References":                                gapRootFormatOnly,
	"draft2019-09/optional/format/uri-template/format: uri-template":                                         gapRootFormatOnly,
	"draft2019-09/optional/format/uri/validation of URIs":                                                    gapRootFormatOnly,
	"draft2019-09/optional/format/uuid/uuid format":                                                          gapRootFormatOnly,
	"draft2020-12/optional/format-assertion/schema that uses custom metaschema with format-assertion: false": gapRootFormatOnly,
	"draft2020-12/optional/format-assertion/schema that uses custom metaschema with format-assertion: true":  gapRootFormatOnly,
	"draft2020-12/optional/format/date-time/validation of date-time strings":                                 gapRootFormatOnly,
	"draft2020-12/optional/format/date/validation of date strings":                                           gapRootFormatOnly,
	"draft2020-12/optional/format/duration/validation of duration strings":                                   gapRootFormatOnly,
	"draft2020-12/optional/format/ecmascript-regex/\\a is not an ECMA 262 control escape":                    gapRootFormatOnly,
	"draft2020-12/optional/format/email/validation of e-mail addresses":                                      gapRootFormatOnly,
	"draft2020-12/optional/format/hostname/validation of A-label (punycode) host names":                      gapRootFormatOnly,
	"draft2020-12/optional/format/hostname/validation of host names":                                         gapRootFormatOnly,
	"draft2020-12/optional/format/idn-email/validation of an internationalized e-mail addresses":             gapRootFormatOnly,
	"draft2020-12/optional/format/idn-hostname/validation of internationalized host names":                   gapRootFormatOnly,
	"draft2020-12/optional/format/idn-hostname/validation of separators in internationalized host names":     gapRootFormatOnly,
	"draft2020-12/optional/format/ipv4/validation of IP addresses":                                           gapRootFormatOnly,
	"draft2020-12/optional/format/ipv6/validation of IPv6 addresses":                                         gapRootFormatOnly,
	"draft2020-12/optional/format/iri-reference/validation of IRI References":                                gapRootFormatOnly,
	"draft2020-12/optional/format/iri/validation of IRIs":                                                    gapRootFormatOnly,
	"draft2020-12/optional/format/json-pointer/validation of JSON-pointers (JSON String Representation)":     gapRootFormatOnly,
	"draft2020-12/optional/format/regex/validation of regular expressions":                                   gapRootFormatOnly,
	"draft2020-12/optional/format/relative-json-pointer/validation of Relative JSON Pointers (RJP)":          gapRootFormatOnly,
	"draft2020-12/optional/format/time/validation of time strings":                                           gapRootFormatOnly,
	"draft2020-12/optional/format/uri-reference/validation of URI References":                                gapRootFormatOnly,
	"draft2020-12/optional/format/uri-template/format: uri-template":                                         gapRootFormatOnly,
	"draft2020-12/optional/format/uri/validation of URIs":                                                    gapRootFormatOnly,
	"draft2020-12/optional/format/uuid/uuid format":                                                          gapRootFormatOnly,
	"draft3/optional/format/color/validation of CSS colors":                                                  gapRootFormatOnly,
	"draft3/optional/format/date-time/validation of date-time strings":                                       gapRootFormatOnly,
	"draft3/optional/format/date/validation of date strings":                                                 gapRootFormatOnly,
	"draft3/optional/format/ecmascript-regex/ECMA 262 regex dialect recognition":                             gapRootFormatOnly,
	"draft3/optional/format/email/validation of e-mail addresses":                                            gapRootFormatOnly,
	"draft3/optional/format/host-name/validation of host names":                                              gapRootFormatOnly,
	"draft3/optional/format/ip-address/validation of IP addresses":                                           gapRootFormatOnly,
	"draft3/optional/format/ipv6/validation of IPv6 addresses":                                               gapRootFormatOnly,
	"draft3/optional/format/regex/validation of regular expressions":                                         gapRootFormatOnly,
	"draft3/optional/format/time/validation of time strings":                                                 gapRootFormatOnly,
	"draft3/optional/format/uri/validation of URIs":                                                          gapRootFormatOnly,
	"draft4/optional/format/date-time/validation of date-time strings":                                       gapRootFormatOnly,
	"draft4/optional/format/email/validation of e-mail addresses":                                            gapRootFormatOnly,
	"draft4/optional/format/hostname/validation of host names":                                               gapRootFormatOnly,
	"draft4/optional/format/ipv4/validation of IP addresses":                                                 gapRootFormatOnly,
	"draft4/optional/format/ipv6/validation of IPv6 addresses":                                               gapRootFormatOnly,
	"draft4/optional/format/uri/validation of URIs":                                                          gapRootFormatOnly,
	"draft6/optional/format/date-time/validation of date-time strings":                                       gapRootFormatOnly,
	"draft6/optional/format/email/validation of e-mail addresses":                                            gapRootFormatOnly,
	"draft6/optional/format/hostname/validation of host names":                                               gapRootFormatOnly,
	"draft6/optional/format/ipv4/validation of IP addresses":                                                 gapRootFormatOnly,
	"draft6/optional/format/ipv6/validation of IPv6 addresses":                                               gapRootFormatOnly,
	"draft6/optional/format/json-pointer/validation of JSON-pointers (JSON String Representation)":           gapRootFormatOnly,
	"draft6/optional/format/uri-reference/validation of URI References":                                      gapRootFormatOnly,
	"draft6/optional/format/uri-template/format: uri-template":                                               gapRootFormatOnly,
	"draft6/optional/format/uri/validation of URIs":                                                          gapRootFormatOnly,
	"draft7/optional/format/date-time/validation of date-time strings":                                       gapRootFormatOnly,
	"draft7/optional/format/date/validation of date strings":                                                 gapRootFormatOnly,
	"draft7/optional/format/email/validation of e-mail addresses":                                            gapRootFormatOnly,
	"draft7/optional/format/hostname/validation of A-label (punycode) host names":                            gapRootFormatOnly,
	"draft7/optional/format/hostname/validation of host names":                                               gapRootFormatOnly,
	"draft7/optional/format/idn-email/validation of an internationalized e-mail addresses":                   gapRootFormatOnly,
	"draft7/optional/format/idn-hostname/validation of internationalized host names":                         gapRootFormatOnly,
	"draft7/optional/format/idn-hostname/validation of separators in internationalized host names":           gapRootFormatOnly,
	"draft7/optional/format/ipv4/validation of IP addresses":                                                 gapRootFormatOnly,
	"draft7/optional/format/ipv6/validation of IPv6 addresses":                                               gapRootFormatOnly,
	"draft7/optional/format/iri-reference/validation of IRI References":                                      gapRootFormatOnly,
	"draft7/optional/format/iri/validation of IRIs":                                                          gapRootFormatOnly,
	"draft7/optional/format/json-pointer/validation of JSON-pointers (JSON String Representation)":           gapRootFormatOnly,
	"draft7/optional/format/regex/validation of regular expressions":                                         gapRootFormatOnly,
	"draft7/optional/format/relative-json-pointer/validation of Relative JSON Pointers (RJP)":                gapRootFormatOnly,
	"draft7/optional/format/time/validation of time strings":                                                 gapRootFormatOnly,
	"draft7/optional/format/uri-reference/validation of URI References":                                      gapRootFormatOnly,
	"draft7/optional/format/uri-template/format: uri-template":                                               gapRootFormatOnly,
	"draft7/optional/format/uri/validation of URIs":                                                          gapRootFormatOnly,

	// gapRootCompositionOnly (37 entries)
	"draft2019-09/not/collect annotations inside a 'not', even if collection is disabled":                    gapRootCompositionOnly,
	"draft2019-09/optional/unknownKeyword/$id inside an unknown keyword is not a real identifier":            gapRootCompositionOnly,
	"draft2019-09/recursiveRef/$recursiveRef with $recursiveAnchor: false works like $ref":                   gapRootCompositionOnly,
	"draft2019-09/recursiveRef/$recursiveRef with no $recursiveAnchor in the initial target schema resource": gapRootCompositionOnly,
	"draft2019-09/recursiveRef/$recursiveRef with no $recursiveAnchor in the outer schema resource":          gapRootCompositionOnly,
	"draft2019-09/recursiveRef/$recursiveRef with no $recursiveAnchor works like $ref":                       gapRootCompositionOnly,
	"draft2019-09/recursiveRef/$recursiveRef without using nesting":                                          gapRootCompositionOnly,
	"draft2020-12/not/collect annotations inside a 'not', even if collection is disabled":                    gapRootCompositionOnly,
	"draft2020-12/optional/unknownKeyword/$id inside an unknown keyword is not a real identifier":            gapRootCompositionOnly,
	"draft6/optional/unknownKeyword/$id inside an unknown keyword is not a real identifier":                  gapRootCompositionOnly,
	"draft7/optional/unknownKeyword/$id inside an unknown keyword is not a real identifier":                  gapRootCompositionOnly,

	// gapRootConditionalOnly (21 entries)
	"draft2019-09/recursiveRef/dynamic $recursiveRef destination (not predictable at schema compile time)": gapRootConditionalOnly,
	"draft2019-09/recursiveRef/multiple dynamic paths to the $recursiveRef keyword":                        gapRootConditionalOnly,
	"draft2020-12/dynamicRef/after leaving a dynamic scope, it is not used by a $dynamicRef":               gapRootConditionalOnly,
	"draft2020-12/dynamicRef/multiple dynamic paths to the $dynamicRef keyword":                            gapRootConditionalOnly,

	// gapRootRefToFalse (2 entries)
	"draft2019-09/ref/$ref to boolean schema false": gapRootRefToFalse,
	"draft2020-12/ref/$ref to boolean schema false": gapRootRefToFalse,

	// gapRootContentOnly (3 entries)
	"draft7/optional/content/validation of binary string-encoding":                     gapRootContentOnly,
	"draft7/optional/content/validation of binary-encoded media type documents":        gapRootContentOnly,
	"draft7/optional/content/validation of string-encoded content based on media type": gapRootContentOnly,

	// gapRootDependenciesOnly (1 entry)
	"draft3/dependencies/dependencies": gapRootDependenciesOnly,
}
