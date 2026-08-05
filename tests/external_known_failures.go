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
	// (draft3 ECMA 262 lookbehind — REMOVED by the #121 corpus bump. The pinned
	// suite was stale and we were right: ES2018 added lookbehind and the ECMA-262
	// engine accepts `(?<=foo)bar`, while bce6a47 still marked it invalid.
	// Upstream flipped that case to valid and made Python-style named groups the
	// invalid one instead. Note how it went, because it is why keyLedger exists:
	// the case was *renamed* as well as flipped, so the key stopped matching
	// anything and the bidirectional check never fired. Nothing would have said
	// this entry had become a lie.)

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

// Reasons for knownUnvalidatedRejections. Each names why a group's root
// resolves to `any` — a bare interface carries no Validate, so nothing about
// the group is ever checked. They are constants rather than repeated literals
// so that fixing one cause is one grep and one delete, not 90.
//
// They named a root *shape* while every one of them was a shape, and the
// remaining one is not: what collapses a $recursiveRef root is a reference the
// branch holds, not the branch. Naming the cause rather than the silhouette is
// the difference between a reason that stays true and one that has to be
// re-derived, which is what the paragraph below is about.
//
// Six are gone rather than left empty. gapRootRefToFalse, which #116 answered
// by giving a $ref to a boolean false the forbidding wrapper the root already
// had; gapRootDependenciesOnly, which #117 answered by reading draft 3's
// bare-string dependency; and gapRootContentOnly, which #115 answered by giving
// a content keyword with no declared type the same string wrapper #106 gave a
// format. A reason nothing cites is a shape that is no longer a gap, and keeping
// the constant would invite the next entry to be filed under something already
// fixed.
//
// The other three went for a different reason: they were not true. Two of them,
// gapRootCompositionOnly and gapRootConditionalOnly, said that a root stating
// only anyOf, or only if/then/else, resolves to any for that reason -- which
// this generator has not done since #113/#114, and the goldens for an anyOf and
// a oneOf root say so. Every group filed under them is a $recursiveRef or a
// $dynamicRef group, and what collapses those roots is the unresolvable
// reference inside the branch, not the branch. The last, gapRootFormatOnly, was
// answered for 88 of its 90 entries by #106 and kept the remaining five on a
// reason that had stopped describing them: three named draft 3's own spellings
// of formats this generator does check under the modern names, and two named a
// format the custom metaschema they declare asserts by vocabulary. Both are now
// read, so the constant has nothing left to cite it.
//
// A wrong reason is worse than no reason. It says the group has been diagnosed,
// so the next reader starts from the diagnosis rather than from the schema --
// which is how five entries sat behind "the root is a format" while two of them
// were about a vocabulary declaration and three about a spelling table.
const (
	gapDynamicRefRoot = `a $recursiveRef/$dynamicRef with no target this generator resolves statically collapses the subschema holding it to any, and the root with it, so the root carries no Validate`
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
// Measured 2026-08-04 against suite commit cf2e5e0: 20 of the 39 skipped
// groups, down from 111 of 213. Fixing one cause prunes a whole section at
// once, which is what happened earlier — a format with no "type" is now the
// wrapper issue #106 asks for rather than `any`, so 88 of the 90 entries filed
// under gapRootFormatOnly went with it, along with the last of gapRootRefToFalse
// and gapRootDependenciesOnly.
//
// That pair of numbers has not been re-measured since: five entries left this
// list below, and each of those groups also stopped being skipped, so both
// figures moved and neither was taken again. Whoever runs the whole corpus next
// should restate them rather than subtract, since the skipped total counts
// groups this list never named.
//
// The #121 corpus bump left every one of the 18 entries measured at bce6a47
// standing — the staleness sweep named none of them — and added exactly two,
// both from the v1 draft this repository had never run. Each is the v1 spelling
// of a root shape already listed for 2020-12, which is the expected shape of the
// delta: v1's keyword set is 2020-12's, so it inherits 2020-12's gaps and no
// others. Both failed by name on the first run that walked v1, rather than
// arriving as a silent skip.
//
// One section is left, of ten. The five that were not in it went for two
// unrelated reasons that had been filed as one: draft 3's "host-name",
// "ip-address" and "color" are now settled onto the spellings this generator
// checks before anything asks whether the keyword names a checkable format, and
// the two format-assertion groups are now read from the $vocabulary of the
// custom metaschema they declare rather than from a --format-assertion the
// harness was passing on that file's behalf.
var knownUnvalidatedRejections = map[string]string{
	// gapDynamicRefRoot (10 entries)
	"draft2019-09/recursiveRef/$recursiveRef with $recursiveAnchor: false works like $ref":                   gapDynamicRefRoot,
	"draft2019-09/recursiveRef/$recursiveRef with no $recursiveAnchor in the initial target schema resource": gapDynamicRefRoot,
	"draft2019-09/recursiveRef/$recursiveRef with no $recursiveAnchor in the outer schema resource":          gapDynamicRefRoot,
	"draft2019-09/recursiveRef/$recursiveRef with no $recursiveAnchor works like $ref":                       gapDynamicRefRoot,
	"draft2019-09/recursiveRef/$recursiveRef without using nesting":                                          gapDynamicRefRoot,
	"draft2019-09/recursiveRef/dynamic $recursiveRef destination (not predictable at schema compile time)":   gapDynamicRefRoot,
	"draft2019-09/recursiveRef/multiple dynamic paths to the $recursiveRef keyword":                          gapDynamicRefRoot,
	"draft2020-12/dynamicRef/after leaving a dynamic scope, it is not used by a $dynamicRef":                 gapDynamicRefRoot,
	"draft2020-12/dynamicRef/multiple dynamic paths to the $dynamicRef keyword":                              gapDynamicRefRoot,
	// v1 has no counterpart to "multiple dynamic paths": its dynamicRef.json is
	// 2020-12's minus that group, so only one of the pair is listed here. An
	// entry for it would be reported stale rather than quietly ignored.
	"v1/dynamicRef/after leaving a dynamic scope, it is not used by a $dynamicRef": gapDynamicRefRoot,
}
