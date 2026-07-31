# schemagen — Adversarial Codebase Audit

**Date:** 2026-07-26
**Commit context:** branch `main`, HEAD `a721c88`
**Method:** full read of `main.go`, `cmd/`, `pkg/schema`, `pkg/generator` (all 7,650 lines of `generator.go`), `pkg/emitter` including all 17 templates, `pkg/validationruntime`, plus test suites and golden files. Behavioral probes were generated, compiled, and executed against a fresh `bin/schemagen` build. Every prior-audit finding (2026-07-12, C1–C13) was re-verified. Findings are **CONFIRMED** (reproduced or traced end-to-end) or **PLAUSIBLE** (suspected from reading only).

**Status of the 2026-07-12 audit:** substantially and genuinely fixed. Old-C1 (draft-3 type branches), old-C2 (min/maxProperties now count `_jsonKeys`), old-C3 (`mergeConstraints` now merges to tightest bound), old-C4 (`ObjectAnyOfDef` enforces ≥1 branch), old-C5 (`promoteConstToEnum` copies instead of mutating, with a regression test), old-C6 (`FileResolver.withinBase` confinement), old-C9 (`errors.Join` in CompositeResolver), old-C11 (`checkOutputCollisions`), old-C12 (README rewritten, YAML documented as unsupported, SSRF note added) — all verified fixed by probe or trace. What follows is a **new** finding set; `go test ./... -short` passes while C1–C4 below reproduce with one-line schemas, so the coverage-gap pattern (old-C8) is the one prior finding that survives intact.

---

## 1. Summary table

| ID | Severity | Area | Issue | Location | Status |
|----|----------|------|-------|----------|--------|
| C1 | **High** | Correctness / codegen | Typed schema + constraint-only `oneOf` generates `type X any` — declared type, sibling constraints, and the oneOf itself are all silently dropped | `pkg/generator/generator.go:1030-1038` | CONFIRMED |
| C2 | **High** | Codegen / compile | `pattern` inside `patternProperties` values, schema-valued `unevaluatedProperties`, and non-object rules emits `regexp.MustCompile` but imports only `ecma262` → generated code does not compile; also wrong regex engine vs. README | `validation.go.tmpl:746-756,1213-1219,64-74`; `generator.go:291-296,316-321,357-366` | CONFIRMED |
| C3 | **High** | Codegen / compile | Properties named `validate`, `additionalProperties`, `pattern_properties` (also `marshal_json`, `unmarshal_json`, `set_defaults`) produce uncompilable structs — derived names collide with generated members; the collision guard only covers `--field-map` overrides | `generator.go:1517-1531`, `fieldmap.go:15-21` | CONFIRMED |
| C4 | **High** | Crash | `{"properties":{"a":null}}` panics the generator (nil deref in `isNullable`, also `propSchema.Default`) instead of erroring | `generator.go:5214`, `generator.go:1570,1604` | CONFIRMED |
| C5 | Medium | Unintended path | Reusing a struct across `json.Unmarshal` calls resurrects stale state: overflow maps are never reset; sticky `_nonObject` makes Marshal return the **previous document** verbatim | `unmarshal.go.tmpl` (no reset), `marshal.go.tmpl:6-14` | CONFIRMED |
| C6 | Medium | Affordance / contract | Hand-constructed structs (not built via UnmarshalJSON) can **never** pass `Validate()` when object-level anyOf/oneOf checks exist (`nil _jsonKeys` → no branch matches); required/optional checks take the opposite policy (skip when untracked) | `validation.go.tmpl:466-616` vs `140-151` | CONFIRMED |
| C7 | Medium | Incoherence / docs | `--draft` override loses to an explicit `$schema` in all per-node decisions (`draftForSchema` prefers `DetectDraft`), contradicting `Config.Draft`'s "takes precedence over $schema URI" and the README | `generator.go:5687-5700`, `config.go:15`, README:74-83 | CONFIRMED |
| C8 | Medium | Correctness | `patternProperties` value schema with a type **list** (`["string","null"]`) validates only the first type — legal `null` rejected | `funcmap.go:269-281` (`ppTypeValueFunc`) | CONFIRMED |
| C9 | Medium | Affordance / docs | `--field-map` override to `"PatternProperties"` is accepted and emits a duplicate field (uncompilable); README promises "fails with an actionable error" | `fieldmap.go:15-21` (missing entry) | CONFIRMED |
| C10 | Medium | Correctness | Non-integer `default` on an integer field silently truncates (`4.5` → `int64(4)`) | `defaults.go:27-40` | CONFIRMED |
| C11 | Low | Naming / DX | Properties named after Go keywords get a gratuitous `_` suffix on **exported** names (`type` → `Type_`, `default` → `Default_`) — keyword check runs on the lowercased form of an already-capitalized name | `naming.go:124-127` | CONFIRMED |
| C12 | Low | Docs | `--strict-properties` CLI help says "(no overflow map for extra JSON keys)"; actual behavior (and README/config docs) keeps the overflow map and rejects in Validate | `root.go:191` vs `config.go:10-13` | CONFIRMED |
| C13 | Low | Dead code / docs | `validationruntime`'s EvalState/Result/Mark API is referenced by nothing; "hybrid… enables shared runtime primitives" (README) overstates — generated code only embeds Capability metadata | `pkg/validationruntime/runtime.go:42-124` | CONFIRMED |
| C14 | Low | DX / testing | No CI (`.github/` absent); all suites green while C1–C4 reproduce with one-line schemas | repo root, `tests/` | CONFIRMED |
| C15 | Low | Safety hardening | FileResolver confinement is prefix-based (symlinks can escape, as its own comment admits); HTTPResolver has unbounded body read, default redirect policy, no content-type check | `resolver.go:542-560,707-719` | PLAUSIBLE |
| C16 | Low | Incoherence / dead code | `OneOfVariants` computed but never attached in the primitive-alias path (unreachable due to C1's earlier branch); JSON-pointer `$ref` into boolean-valued `additionalProperties` fails; `Extensions` refs re-parsed per resolution and never `Normalize()`d; `emitter.New()` re-parses templates per input file; explicit discriminator `mapping` matches only `$ref` variants | `generator.go:1160,1187-1196`, `resolver.go:350-354,420-435`, `root.go:160`, `generator.go:3081-3093` | CONFIRMED (by read) |

---

## 2. System map

**Entry point.** `main.go` → cobra `generate`. Per input file (`cmd/schemagen/root.go:91-182`):

1. `schema.LoadFromFile` — `os.ReadFile` + `json.Unmarshal` into the superset `Schema` struct. Custom `UnmarshalJSON` handles boolean schemas, unknown-keyword capture (`Extensions`), `{"const":null}` (`ConstIsNull`), and draft-3 schema-valued `type` entries (`TypeSchemas`).
2. `s.Normalize()` — cross-draft canonicalization: `id`→`$id`, `definitions`↔`$defs` (bidirectional copy), `extends`→`allOf`, `divisibleBy`→`multipleOf`, `disallow`→`not`, `dependencies`→`dependentSchemas`/`dependentRequired`, draft-3 per-property `required`→parent array. Recursive.
3. Resolver chain: `FileResolver` (always, confined to the schema's directory subtree via `withinBase`) + optional `HTTPResolver` behind `--allow-remote-refs`, joined by `CompositeResolver` (errors now `errors.Join`ed).
4. `generator.New(cfg).Generate(s)` — builds `ResourceGraph` (base URIs, document roots, anchors, dynamic anchors), then converts to IR `TypeDef`s. Dispatch order in `generateTypeDef` matters and is a fault line (see C1): const→enum promotion; enums; `$ref`-with-siblings → synthesized `allOf`; `allOf` merge; degenerate anyOf/oneOf; **anyOf-with-properties merge; nullable-anyOf alias; oneOf-without-properties → `any`**; struct; ref-only alias; `$dynamicRef`; `not`-schema wrapper; type-only wrapper; draft-3 type branches; primitive/array alias (with `InferredAliasDef`/`BigIntAliasDef` wrappers); property-less object; fallback `any`.
5. `emitter.New().Emit(ir)` — 17 `text/template`s render, `go/format.Source` gates syntax (but not type-checking — C2/C3 pass gofmt and fail `go build`).
6. `os.WriteFile(outputDir/derived_name.go)` after a pre-flight cross-input collision check.

**Key invariants (as implemented):**
- *Round-trip fidelity* — unknown keys land in `AdditionalProperties`/`PatternProperties` overflow maps (`json:"-"`, rebuilt in custom marshal); non-object data for type-less schemas preserved via `_nonObject`/`_rawNonObject`. Holds on first use; **breaks on struct reuse** (C5).
- *Presence tracking* — `_jsonKeys`/`_jsonRawProps` populated by UnmarshalJSON drive required/optional/dependent/propertyNames/unevaluated/oneOf/anyOf validation. The struct is otherwise presence-blind; `Validate()` on values not built from JSON is a different, undocumented contract (C6).
- *Type-name uniqueness* — `g.generated` set + numeric suffixing for derived duplicates; field-map overrides checked against `reservedFieldNames`. **Derived property names are not checked against generated members** (C3).
- *Draft awareness* — `refOverridesSiblingsForDraft`, `supportsPrefixItems`, `requiresStrictIntegerToken`, keyed off `draftForSchema` (detected > document-root > global). The `--draft` flag only sets the global fallback (C7).
- *Validation vocabulary gating* — metaschemas without the validation vocab disable constraint extraction wholesale.
- *Import correctness* — `addRequiredImports` re-derives imports by scanning the IR, a 550-line parallel reconstruction of what every template might emit. C2 is a desync between that scan and the templates; nothing structurally prevents the next one.

---

## 3. Findings by category

### Correctness

**C1 — Typed schema + constraint-only `oneOf` drops all typing and validation — CONFIRMED (High)**
`pkg/generator/generator.go:1030-1038`

```go
// oneOf without properties in parent or any variant → alias to `any`
if len(s.OneOf) > 0 && !hasProperties(s) && !g.oneOfHasProperties(s) {
    ... Underlying: &PrimitiveType{Name: "any"} ...
```
This branch runs *before* the primitive-type path and looks only at properties, not at `s.Type` or sibling constraints.

**Scenario (reproduced):** `{"type":"integer","oneOf":[{"minimum":10},{"maximum":5}]}` generates literally:
```go
type Root any
```
No `Validate()`, no unmarshal type check. `"hello"`, `{}`, `3.14`, and `7` (which matches zero oneOf branches) are all accepted. Compare: the same schema wrapped in `allOf` gets full `OneOfVariants` checks via `generateAllOfDef` (generator.go:2117, 2179) — the machinery exists, this dispatch arm just bypasses it.

**Direction:** gate the branch on `len(s.Type) == 0 && no constraint keywords`; when a primary type exists, fall through to the alias path and attach `extractOneOfVariantRules` (the alias/inferred templates already render `.OneOfVariants`).

**C8 — patternProperties multi-type checks only the first type — CONFIRMED (Medium)**
`pkg/emitter/funcmap.go:269-281` — `ppTypeValueFunc` on `[]string` returns `val[0]`.

**Scenario (reproduced):** `{"patternProperties":{"^v":{"type":["string","null"]}}}` with data `{"v1":null}` → `Validate()` returns `patternProperties ^v: key "v1" value must be string`. Valid data rejected. Same code path serves `NonObjectValidations`.

**Direction:** emit an OR over all listed types (the JSON-type sniffing switch is already inline; it needs a set membership test, not equality against one name).

**C10 — Non-integer defaults silently truncated — CONFIRMED (Medium)**
`pkg/generator/defaults.go:27-40` — `intVal := int64(v)`.

**Scenario (reproduced):** `{"n":{"type":"integer","default":4.5}}` → `SetDefaults()` assigns `int64(4)`. The schema is invalid (default doesn't satisfy its own type), but the tool neither rejects nor warns — it invents a different default.

**Direction:** if `v != math.Trunc(v)`, fail generation or skip with a warning.

### Codegen produces uncompilable Go

**C2 — std-`regexp` calls emitted without the import (and wrong engine) — CONFIRMED (High)**
Templates emit `regexp.MustCompile(...)` for three rule families:
- `ppPattern` (pattern constraint on patternProperties values) — `validation.go.tmpl:746-756`
- `pattern` in `validation_rule_inline` (schema-valued `unevaluatedProperties`) — `validation.go.tmpl:1213-1219`
- `ppPattern` in non-object validations — `validation.go.tmpl:64-74`

But `addRequiredImports` maps those rule types to `needsRegexp` → the **ecma262** import pair (`generator.go:291-296,316-321,357-366`), not `needsStdRegexp`.

**Scenario (reproduced twice):**
- `{"patternProperties":{"^v":{"type":"string","pattern":"^(?=a)a+$"}}}` → generated file fails `go build`: `undefined: regexp`.
- `{"properties":{"a":{"type":"string"}},"unevaluatedProperties":{"type":"string","pattern":"^x"}}` → fails with `"github.com/mgilbir/goecma262" imported as ecma262 and not used` (plus undefined `regexp`).

Both slip through the test suite because `go/format.Source` only checks syntax. Independently: even with imports fixed, these three sites would use Go RE2 semantics while the README (§Regular Expressions) promises ECMA-262 for exactly these keywords — a lookahead pattern would then panic `regexp.MustCompile` at Validate() time instead.

**Direction:** route all schema-authored patterns through ecma262 like the top-level `pattern` rule does; add a compile-the-output test for each pp*/uneval rule type.

**C3 — Schema property names collide with generated members — CONFIRMED (High)**
`generateStructDef`'s duplicate check (`generator.go:1517-1531`) tests derived names only against *each other*; `reservedFieldNames` (`fieldmap.go:15-21`) is consulted only for `--field-map` overrides.

**Scenarios (reproduced):**
- `{"properties":{"validate":{"type":"string"}},"required":["validate"]}` → field `Validate string` + generated `func (r Root) Validate() error` → `field and method with the same name Validate`.
- `{"properties":{"additionalProperties":{"type":"boolean"}},"additionalProperties":true}` → `AdditionalProperties` declared twice.
- `{"properties":{"pattern_properties":…}, "patternProperties":{…}}` → `PatternProperties` declared twice.

`marshal_json`/`unmarshal_json`/`set_defaults` properties hit the same wall whenever the corresponding method is generated. These are ordinary, spec-legal schemas ("validate" is a plausible API field name), and the failure mode is an opaque `go build` error in generated code.

**Direction:** apply the reserved-name check (conditioned on which members the struct actually generates) to derived names too, suffixing rather than erroring (derived names aren't user-pinned); consider renaming synthesized members to something less collision-prone (e.g. `SchemagenExtra`).

**C4 — `null` property schema panics the generator — CONFIRMED (High)**
`{"type":"object","properties":{"a":null}}` →
```
panic: runtime error: invalid memory address or nil pointer dereference
  pkg/generator.isNullable(...)
```
`isNullable` (`generator.go:5214`) dereferences without a nil check; `generateStructDef` also reads `propSchema.Default` (`:1604`) and `propSchema.Description` (`:1615`) unguarded, so fixing only `isNullable` moves the crash. A malformed-but-parseable schema should produce an error ("property a: schema must be an object or boolean"), not a stack trace. Note the asymmetry: `resolvePropertyType` handles `s == nil` gracefully (`:3457-3459`), so someone anticipated this shape once.

### Unintended paths

**C5 — Struct reuse resurrects stale state — CONFIRMED (Medium)**
Generated `UnmarshalJSON` never resets `AdditionalProperties`, `PatternProperties`, `_jsonKeys`, `_jsonRawProps`, `_nonObject`, or `_rawNonObject`.

**Scenario (reproduced):**
```go
var r Root
json.Unmarshal([]byte(`{"a":1,"stale":true}`), &r)
json.Unmarshal([]byte(`{"a":2}`), &r)
json.Marshal(r)  // → {"a":2,"stale":true}   ← "stale" resurrected
```
Worse with `AcceptNonObject` types: unmarshal `42`, then unmarshal `{"a":3}` into the same variable → `Marshal` returns `42`. The `_nonObject` flag is sticky, so the **entire second document is discarded on output**. Reusing a target value across decodes is idiomatic Go (loop bodies, `json.Decoder` streams, sync.Pool), so this is a realistic corruption path, not an abuse.

**Direction:** clear all synthesized state at the top of every generated `UnmarshalJSON`.

**C6 — `Validate()` has two contradictory contracts — CONFIRMED (Medium)**
For values built by UnmarshalJSON, `_jsonKeys` gates everything. For hand-built values (`_jsonKeys == nil`):
- required-property checks are **skipped** (deliberate, commented — `validation.go.tmpl:140-151`);
- optional-field constraint checks are skipped (`_jsonKeys[...]` on nil map is false);
- but object-level anyOf/oneOf checks **hard-fail**: every branch requires `_jsonKeys[key]` → 0 matches → `"anyOf: no variant matched"` (`validation.go.tmpl:466-616`), reproduced: a zero-value struct of an anyOf type fails Validate even when its fields are populated correctly.

So depending on which keywords a schema uses, validating a programmatically-constructed value is silently lenient, or impossible. Neither behavior is documented; the API shape (`Validate() error` on an ordinary struct) advertises neither.

**Direction:** pick one contract. Either synthesize `_jsonKeys` from non-zero fields when nil (best effort), or skip presence-dependent groups when untracked (consistent with required), or document loudly that Validate is only meaningful post-unmarshal.

### Incoherences

**C7 — `--draft` doesn't override `$schema` — CONFIRMED (Medium)**
`config.go:15`: "when set, this takes precedence over $schema URI." `draftForSchema` (`generator.go:5687-5700`) checks `DetectDraft(s)` **first** and only falls back to `g.draft`. So for any document that declares `$schema`, the flag changes nothing in `refOverridesSiblings`-per-schema, `supportsPrefixItems`, `requiresStrictIntegerToken`, or dependent-required gating.

**Scenario (reproduced):** draft-07 `$schema` + `prefixItems`, generated with and without `--draft 2020-12` → byte-identical output (prefixItems ignored both times). README §Draft Override explicitly offers the flag to "force a specific draft version". Mixed semantics: code paths that consult `g.draft` directly (e.g. `g.refOverridesSiblings()` at `generator.go:1742`) *do* honor the flag — so a forced draft applies to some keywords and not others within one run.

**Direction:** in `draftForSchema`, return `g.config.Draft` first when explicitly set; or re-document the flag as a fallback for `$schema`-less files and rename `Config.Draft`'s comment.

**C9 — field-map reserved list missing `PatternProperties` — CONFIRMED (Medium)**
README §Field Name Overrides: "Generation **fails with an actionable error** when an override would produce uncompilable code — i.e. when it collides with … the synthesized `AdditionalProperties` overflow field." The synthesized `PatternProperties` overflow field (struct.go.tmpl:23) is not in `reservedFieldNames`.

**Scenario (reproduced):** override `pp → "PatternProperties"` on a schema with `patternProperties` → exit 0, duplicate field, uncompilable. Same taxonomy as C3 but on the path the docs claim is guarded.

**C11 — Keyword-suffixed exported names — CONFIRMED (Low)**
`sanitizeGoIdentifier` (`naming.go:124-127`) checks `goKeywords[strings.ToLower(result)]` on names that are already capitalized. Exported identifiers can never collide with Go keywords, so `type` → `Type_`, `default` → `Default_`, `range` → `Range_` etc. — visible throughout `testdata/golden/regression/allof_if_then_branches.go` (`Type_`, `Default_`, enum type `TriggerType_`). Cosmetic, but these are extremely common property names and the trailing underscore reads as a bug to consumers. (The check is only correct for the hypothetical unexported case, which this generator never produces for fields/types.)

**C13 / dead runtime — CONFIRMED (Low)**
`validationruntime.EvalState`, `Result`, `MarkProperty/MarkItem/Merge/Unevaluated*`, `ValidResult/InvalidResult` have zero references outside their own file (verified by grep across `pkg/`, `tests/`, templates). Generated hybrid-mode code imports the package solely for the `Capability` struct literal. README: "Use `--validation hybrid` to … enable shared runtime primitives for features that need annotation tracking" — no primitive is ever invoked; annotation tracking is done by open-coded template logic that doesn't touch this package. `--validation runtime` remains an alias of hybrid (now honestly documented, README:89).

**C16 — assorted incoherences — CONFIRMED by read (Low)**
- `generateTypeDef`'s primitive path computes `oneOfVariants` (`generator.go:1160`) then constructs the `AliasDef` without `OneOfVariants` (`:1187-1196`) — dropped on the floor. Currently masked because C1's branch intercepts every schema that could reach it with a non-empty `OneOf`; fixing C1 without noticing this will resurface it.
- JSON-pointer refs into boolean-valued keywords fail: `$ref: "#/additionalProperties"` where `additionalProperties: false` → "schema has no additionalProperties schema" (`resolver.go:350-354`). Booleans *are* schemas in draft 6+.
- `walkPath`'s Extensions fallback re-parses the raw JSON into a fresh `Schema` on every resolution and never calls `Normalize()` (`resolver.go:420-435`) — two refs to the same extension get distinct nodes (breaks identity-based cycle detection) and draft-3 constructs inside extensions stay un-normalized.
- `emitter.New()` (template re-parse) runs once per input file inside the CLI loop (`root.go:160`).
- Explicit OpenAPI `discriminator.mapping` only matches variants that are `$ref`s (`generator.go:3081-3093`); inline variants silently fall back to heuristic detection. Undocumented.

### Boundary & safety

**C15 — resolver hardening gaps — PLAUSIBLE (Low)**
- `withinBase` (`resolver.go:542-560`) is `filepath.Abs`+`Clean`+prefix — its own comment concedes symlinks aren't resolved. A symlink inside the schema directory pointing at `/etc` re-opens the traversal that old-C6's fix closed. Use `filepath.EvalSymlinks` before the prefix check.
- `HTTPResolver` (`resolver.go:707-719`): `io.ReadAll` with no size cap (a hostile endpoint can OOM generation), default redirect policy (redirects can pivot to hosts the user never referenced, compounding the documented SSRF), no HTTPS-only option, no content-type check. All behind the opt-in flag and now README-documented as an SSRF vector, hence Low — but the flag's blast radius is bigger than the README's framing of "fetches remote schemas".

### Documentation

- **C12:** `root.go:191` flag help: "Treat absent additionalProperties as false (**no overflow map for extra JSON keys**)". Reality (and `config.go:10-13`, README:54): the overflow map **is** generated, data is captured, and `Validate()` rejects. The one place users see at `--help` time is the one place that's wrong.
- **C2 (doc facet):** README §Regular Expressions claims ECMA-262 semantics for `pattern` under `patternProperties`; the pp-value/uneval paths use (or rather, fail to import) std `regexp`.
- **C13 (doc facet):** "enables shared runtime primitives" — nothing is enabled; only metadata is emitted.
- **C7 (doc facet):** README §Draft Override implies the flag forces the draft; it only sets the fallback.
- README otherwise checks out: install/build/test commands work as written; the flag table matches `root.go`; the field-map example round-trips; the security note is accurate about intent (modulo C15's symlink nuance).

### Developer experience

- **C14:** No CI whatsoever (no `.github/`, no workflow files). `make lint` is `fmt`+`vet` on developer discipline. The suite is genuinely extensive (goldens, compile-and-run round-trips, the full JSON-Schema-Test-Suite with a bidirectional known-failures ledger that errors when a known failure starts passing — a good design), yet every High finding above passes it. Systematic blind spots: (a) no test generates from a schema whose property names collide with generated members; (b) no test compiles output containing pp-pattern/uneval-pattern rules; (c) validation tests only run when the *root* type has a `Validate()` method, so `type Root any` degradations (C1) read as "skipped", not "failed"; (d) nothing unmarshals twice into one value.
- The known-failures ledger (`tests/external_known_failures.go`) is exemplary honesty — 10 remaining entries, all unevaluatedItems runtime-annotation cases, each with a reason.
- Newcomer path is good: README quick-start works, `make test-short` completes in ~30s, golden regeneration is one command.

---

## 4. Design tensions

1. **Two sources of truth for imports.** `addRequiredImports` is a 550-line manual mirror of what 17 templates emit. C2 is the proof it drifts; nothing detects the next drift because gofmt validates syntax, not resolution. *Alternative:* have templates declare their imports (e.g. emit `//import:` markers scraped post-render, or run the output through `golang.org/x/tools/imports`), or add a test matrix that compiles one generated file per ValidationRule type.

2. **The schema's property namespace and the generator's member namespace share one struct.** `Validate`, `MarshalJSON`, `AdditionalProperties`, `PatternProperties`, `SetDefaults` all live where user properties land (C3, C9). Every new generated member is a new collision. *Alternative:* reserve a prefix for synthesized members (`SchemagenAdditional`, or unexported fields + accessors), and make the derived-name pass collision-aware against a single canonical "members this struct will have" set used by both the derived path and the field-map path.

3. **Presence lives in shadow fields, so `Validate()` means two different things.** `_jsonKeys`/`_jsonRawProps`/`_nonObject` encode "what the JSON said" onto a struct that users can also construct directly; C5 and C6 are both symptoms (state that outlives its document; checks that require state that was never created). *Alternative:* validate against the raw document (keep the captured `map[string]json.RawMessage` and validate that), and define struct-level Validate as best-effort — or reset+synthesize the shadow state so both entry points share one contract.

4. **`generateTypeDef` is a 500-line ordered dispatch where earlier arms silently swallow later arms' information.** C1 (oneOf→`any` ignoring `type`), the unreachable `OneOfVariants` path, and the anyOf/oneOf special cases all stem from arms that key on one keyword's presence and discard the rest of the node. *Alternative:* compute a small classification record (has type? has composition? has constraints? has properties?) once, and make each arm's preconditions explicit and mutually exclusive — or at least assert in tests that no schema with a declared `type` ever produces bare `any`.

5. **Draft resolution is consulted at ~6 call sites with 3 different precedence rules** (`g.draft`, `draftForSchema`, resource-graph default). C7 is the visible contradiction; the latent one is a single run applying draft-07 semantics to `$ref` siblings and draft-2020-12 semantics to tuple keywords for the same node, depending on which helper the code path happens to call. *Alternative:* resolve one effective draft per schema node at graph-build time (with explicit `Config.Draft` precedence) and store it on the node.

---

## 5. Expectation gaps (expected X, found Y)

- **`{"type":"integer","oneOf":[…]}`** — expected: int64 alias validating type + exactly-one-branch. Found: `type Root any`, everything accepted (C1).
- **Generated code compiles** — expected: always (it's a code generator's prime contract). Found: three regex rule families and half a dozen property names produce `go build` failures (C2, C3, C9).
- **Any parseable schema either generates or errors** — expected. Found: `"a": null` panics (C4).
- **`var x T; json.Unmarshal(doc1,&x); json.Unmarshal(doc2,&x)`** — expected: x reflects doc2 (stdlib-ish semantics). Found: doc1's unknown keys — or doc1 *in its entirety* — resurface in Marshal (C5).
- **`Validate()` on a struct I built in Go** — expected: constraints checked. Found: silently skipped, or unconditionally failing, depending on schema shape (C6).
- **`--draft 2020-12` forces 2020-12** — expected per README/config comment. Found: `$schema` wins node-locally, flag wins in a few global paths (C7).
- **`type: ["string","null"]` under patternProperties** — expected: null accepted. Found: rejected (C8).
- **`--strict-properties` help text** — expected to describe behavior. Found: describes the opposite of what's generated (C12).
- **`--validation hybrid` "enables runtime primitives"** — expected: generated validators call into `validationruntime`. Found: only a metadata struct (C13).
- **Property `type` → field `Type`** — expected. Found: `Type_` (C11).

---

## 6. Open questions (code alone can't resolve)

1. **Is `Validate()` on hand-constructed values a supported operation?** C6 needs a product decision (skip vs. synthesize vs. document-as-unsupported) before a fix direction is right.
2. **Is single-use unmarshal an intended contract?** If generated types are officially one-shot (decode once, never reuse), C5 is a documentation task; if not, it's a reset bug. Nothing in code or docs says either.
3. **What should `--draft` mean for documents that declare `$schema`?** Force (per the config comment) or fallback (per current behavior)? Both are defensible; the current state is neither.
4. **Is the `validationruntime` EvalState API a roadmap or a leftover?** If the planned full-runtime mode is still coming, keep it; otherwise deleting it (and trimming the README claim) removes a misleading affordance.
5. **Trust model for `--allow-remote-refs` in automated pipelines** — is SSRF-with-redirects + unbounded reads acceptable given the documented "only for schemas you trust" stance, or is an allowlist/limit worth the complexity (C15)?
6. **Should generation fail on self-inconsistent schemas** (default violating its own type, C10; null property schemas, C4) **or degrade?** Today it does one of: invent a value, panic, or silently drop — three different answers to the same class of input.

---

*Probes used in this audit (schemas + compile/run harnesses) were executed from a scratch directory against `bin/schemagen` built from HEAD; all reproduce with the commands shown inline. Prior-audit regressions re-tested: allOf tightest-bound merge (7 vs `minimum:10` → rejected ✓), anyOf required (`{}` → rejected ✓), draft-3 schema-valued type (string accepted, boolean rejected ✓), min/maxProperties presence counting (golden `property_count` ✓), const promotion purity (`TestGenerateDoesNotMutateInputSchema` ✓), file-ref confinement (`TestFileResolverConfinesToBaseDir` ✓).*
