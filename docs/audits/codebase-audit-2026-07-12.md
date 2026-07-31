# schemagen — Adversarial Codebase Audit

**Date:** 2026-07-12
**Auditor role:** senior staff engineer / skeptical first-time API consumer / adversarial reviewer
**Commit context:** branch `main`, HEAD `723720b`
**Method:** full read of `cmd/`, `pkg/schema`, `pkg/generator`, `pkg/emitter` (incl. all templates), `pkg/validationruntime`; behavioral probes generated + compiled + executed against `bin/schemagen`. Each finding marked **CONFIRMED** (traced end-to-end or reproduced) or **PLAUSIBLE** (suspected from reading, not reproduced).

The tool is a JSON-Schema→Go type generator. It is genuinely ambitious: it covers drafts 3–2020-12, `$ref`/`$dynamicRef`/`$recursiveRef` scoping, `unevaluatedProperties` with runtime conditional evaluation, discriminated unions, and round-trip fidelity. The architecture (Load → Normalize → IR → template Emit) is coherent and the schema-parsing layer is careful. The defects below concentrate in **validation code generation**, where the emitted `Validate()` logic diverges from JSON Schema semantics in ways the golden tests don't catch.

---

## 1. Summary table

| ID | Severity | Area | Issue | Location | Status |
|----|----------|------|-------|----------|--------|
| C1 | **High** | Correctness / codegen | Draft-3 schema-valued `type` array emits **non-compiling** Go (unused `_obj`) and, even fixed, validates the branch wrongly | `pkg/emitter/templates/type_only_schema.go.tmpl:54-57` | CONFIRMED |
| C2 | **High** | Correctness | `minProperties`/`maxProperties` on a struct count **all declared fields as present**, rejecting valid objects | `pkg/emitter/templates/validation.go.tmpl:313-328` | CONFIRMED |
| C3 | **High** | Correctness | `allOf` with overlapping numeric/string/array constraints merges "first-set-wins" instead of tightest → under-validation | `pkg/generator/generator.go:2665-2709` | CONFIRMED |
| C4 | Medium | Correctness / affordance | `anyOf` flattened to a struct drops variant `required` + per-variant constraints → `Validate()` is a no-op; data matching no branch passes | `pkg/generator/generator.go:2711-2766` | CONFIRMED |
| C5 | Medium | Correctness / statefulness | `generateTypeDef`/`resolvePropertyType` mutate the shared parsed `*schema.Schema` (`s.Enum = …`); breaks a second `Generate` and `$ref`-shared const schemas | `pkg/generator/generator.go:915,3195` | PLAUSIBLE |
| C6 | Medium | Safety | `FileResolver` resolves `$ref` relative paths with no containment check → arbitrary-path file reads during generation | `pkg/schema/resolver.go:560-585` | CONFIRMED |
| C7 | Low | Safety | `--allow-remote-refs` HTTP resolver has no host allowlist → SSRF (localhost, cloud metadata) to `docKey` from schema input | `pkg/schema/resolver.go:636-704` | CONFIRMED |
| C8 | Medium | DX / testing | Golden + roundtrip suites pass while C1–C4 are broken; no CI workflow present | `tests/`, no `.github/` | CONFIRMED |
| C9 | Low | Incoherence | `CompositeResolver` returns only the last resolver's error, masking the informative file-resolver error | `pkg/schema/resolver.go:719-733` | CONFIRMED |
| C10 | Low | Affordance / docs | `--validation runtime` is a documented "strategy" but behaves identically to `hybrid` | `pkg/generator/validation.go:17-21`, README:83-88 | CONFIRMED |
| C11 | Low | Affordance / data-loss | Two input schemas sharing a basename (different dirs) silently overwrite one output file | `cmd/schemagen/root.go:165-170,259-266` | CONFIRMED |
| C12 | Low | Docs | README "How It Works" omits base-URI/resource-graph pass and remote-ref side effects; YAML support contradictory | README:125-132, `pkg/schema/loader.go:19-27` | CONFIRMED |
| C13 | Low | Correctness | `oneofMatched > 1` guard runs after assigning the field; multi-match still populates before error (harmless but dead assignment); heuristic discriminator can silently mis-dispatch | `pkg/emitter/templates/unmarshal.go:99-116`, `generator.go:2891-2938` | PLAUSIBLE |

---

## 2. System map

**Entry point.** `main.go` → `cmd/schemagen.NewRootCmd()` → cobra `generate` subcommand. The real per-file pipeline (`root.go:84-175`):

1. `schema.LoadFromFile` — `os.ReadFile` + `json.Unmarshal` into the superset `Schema` struct (custom `UnmarshalJSON` handles boolean schemas, `Extensions`, `const:null`, draft-3 schema-valued `type`).
2. `s.Normalize()` — draft cross-normalization (`id`→`$id`, `definitions`↔`$defs`, `extends`→`allOf`, `divisibleBy`→`multipleOf`, `disallow`→`not`, `dependencies` split, draft-3 per-property `required`). Recurses all children.
3. Resolver chain built: `FileResolver` always; `CompositeResolver(file, http)` if `--allow-remote-refs`.
4. `generator.New(cfg).Generate(s)` — builds `ResourceGraph` (base URIs, document roots, anchors), then walks `$defs`/`definitions`/root producing IR `TypeDef`s. This is the ~7.3k-line core.
5. `emitter.New().Emit(ir)` — `text/template` render + `go/format.Source`.
6. `os.WriteFile(outPath, …)` where `outPath = deriveOutputFilename(schemaPath)`.

**Key invariants (as implemented):**
- *Type-name uniqueness* — `g.generated[name]` dedupes; collisions on derived names get numeric suffixes (`generator.go:3084-3095`, `1466-1482`).
- *Round-trip fidelity* — unknown keys captured in an `AdditionalProperties`/`PatternProperties` overflow map even when `additionalProperties:false` (validation rejects, marshal preserves).
- *Recursion breaking* — value-type cycles broken with pointers via `structsInProgress` + `isScopedSelfRef` + `pushDynamicScope`.
- *Draft-aware `$ref`* — drafts 3–7 treat `$ref` as overriding siblings; 2019-09+ as an applicator (implicit `allOf` synthesis at `generator.go:933-944`).
- *Validation vocabulary gating* — if the declared metaschema omits the validation vocab, all constraint extraction is skipped (`validationKeywordsDisabled`).

**Where invariants are enforced vs assumed:** parsing/normalization is defensive. IR generation *assumes* it fully owns each schema node — but it mutates shared nodes (C5). The emitted validators *assume* struct-field presence equals object-property presence (C2) and that `allOf`/`anyOf` sub-constraints were merged (C3, C4) — assumptions that don't hold.

---

## 3. Findings by category (severity order)

### C1 — Draft-3 schema-valued `type` array: non-compiling + wrong logic — CONFIRMED (High)
`pkg/emitter/templates/type_only_schema.go.tmpl:54-57`

The `TypeBranches` loop emits, **unconditionally** (outside the `range $branch.Properties`):
```go
_obj, _objOk := _v.(map[string]any)
if !_objOk { _branchMatches = false }
```
For a branch that has `AllowedTypes` but **no** `Properties`, `_obj` is declared and never used.

**Scenario:** `{"$schema":".../draft-03/schema#","type":["integer",{"type":"string"}]}`. The `{type:string}` branch has allowed type `string`, no properties. Generated `Validate()`:
- **Fails `go build`**: `declared and not used: _obj` (reproduced: `go build` errored on the emitted file).
- Even after removing the unused var, the branch demands the value be a `map[string]any`, so a string can *never* satisfy the schema-valued branch, and the fallback `switch` hits `case string: return fmt.Errorf("type: string is not allowed")`. So a value the schema explicitly permits is rejected.

The `knownValidationFailures`/`knownRoundTripFailures` comments claim "draft3 schema-valued type alternative — FIXED via TypeOnlySchemaDef type branches", but the fix only works when the branch carries object properties (the external suite's draft-3 cases do). A primitive-only branch is untested and broken.

**Direction:** gate the `_obj` block on `len(.Properties) > 0`, and treat `AllowedTypes` and `Properties` as an OR/independent match (a branch with only a type should match on type alone).

### C2 — `minProperties`/`maxProperties` count declared fields, not present ones — CONFIRMED (High)
`pkg/emitter/templates/validation.go.tmpl:313-328`
```go
totalProps := {{len $struct.Fields}} + len({{$recv}}.AdditionalProperties)
```
`{{len $struct.Fields}}` is a compile-time constant = number of *declared* properties, treated as if all are always present.

**Scenario (reproduced):** schema `{type:object, properties:{a,b,c}, maxProperties:2}`, input `{"a":"x"}` (one property present) →
```
maxprops {a} => too many properties: 3 exceeds maximum 2
```
Valid data rejected. Symmetrically, `minProperties` (`minp.json`, `minProperties:2`) computes `totalProps := 2 + len(AdditionalProperties)` and would pass `{}` — a false accept for the min case. The count must be actual present keys.

**Direction:** count presence via `_jsonKeys` (already populated when validation needs it) rather than `len(Fields)`. Requires routing `min/maxProperties` through `NeedsJSONKeys` and counting distinct present keys (declared + overflow + pattern).

### C3 — `allOf` overlapping constraints use first-set-wins, not tightest — CONFIRMED (High)
`pkg/generator/generator.go:2665-2709` (`mergeConstraints`)

`mergeConstraints` only copies a constraint from `src` when `dst`'s is nil ("first set wins"). For two `allOf` branches constraining the same keyword, only the first survives; JSON Schema requires **both** to hold (i.e. `minimum` = max of minimums, `maximum` = min of maximums, `maxLength` = min, etc.).

**Scenario (reproduced):** `{type:integer, allOf:[{minimum:5},{minimum:10}]}`, input `7`:
```
allofmin 7 (needs >=10) => <nil>
```
`7` is accepted though it violates `minimum:10`. Any value in the looser-but-not-tighter band is a false accept. Same class of bug for `maxLength`/`minLength`/`maxItems`/`multipleOf` overlaps.

**Direction:** replace first-set-wins with keyword-aware tightening (max for lower bounds, min for upper bounds; `multipleOf` → lcm or emit both checks). Simplest correct fix: emit *every* branch's rules rather than merging, so all checks run.

### C4 — `anyOf`-as-struct drops variant `required` and constraints — CONFIRMED (Medium)
`pkg/generator/generator.go:2711-2766` (`generateAnyOfDef`)

When `anyOf` variants contribute properties, they're merged into one permissive struct: "no field is marked required" and per-variant validation rules are not carried into `Validate()`.

**Scenario (reproduced):** `{anyOf:[{properties:{a},required:[a]},{properties:{b},required:[b]}]}` generates a struct whose `Validate()` is literally:
```go
func (a AnyOfReq) Validate() error { return nil }
```
An empty object `{}` — which satisfies *neither* branch — validates successfully. The README advertises "Composition keywords: allOf, anyOf, oneOf"; a consumer reasonably expects `anyOf` to reject data matching no branch. This is an affordance mismatch: the API shape (a struct with `Validate()`) promises validation the code doesn't deliver.

**Direction:** emit an `ObjectOneOf`-style branch check for `anyOf` (≥1 branch must match) analogous to the existing `ObjectOneOfDef` machinery for `oneOf`.

### C5 — Generator mutates the shared parsed schema — PLAUSIBLE (Medium)
`pkg/generator/generator.go:915-920`, `3195-3198`
```go
if s.Const != nil && len(s.Enum) == 0 { s.Enum = []any{*s.Const} }
```
This writes back into the input `*schema.Schema`. Because schema nodes are shared by pointer across `$ref` targets, `$defs` maps, and (for callers) across multiple `Generate` invocations on the same tree, the mutation persists. A schema node reached first as a standalone const and later via a path expecting the raw const now carries a synthesized `Enum`. Also `mergeAllOfInto`/`generateAnyOfDef` build `merged` schemas that alias sub-schema pointers, so a later in-place edit can bleed across types.

**Why PLAUSIBLE not CONFIRMED:** single-shot CLI runs mask it (each file gets a fresh parse). It bites library consumers calling `Generate` twice or reusing a `*Schema`. No reproduction attempted for the cross-ref case.

**Direction:** treat the parsed schema as immutable in the generator; compute a local `enumValues` variable instead of assigning `s.Enum`.

### C6 — `FileResolver` path traversal — CONFIRMED (Medium, gated to default local mode)
`pkg/schema/resolver.go:560-585`

Relative `$ref` paths are resolved with `filepath.Join(baseDir, relPath)` (or `filepath.Join(filepath.Dir(baseURI.Path), relPath)`) and passed straight to `os.ReadFile`, with no check that the result stays within `baseDir`.

**Scenario (reproduced):** a schema with `{"$ref":"../../../../../../etc/hostname"}` causes `schemagen` to `ReadFile("/etc/hostname")` during generation. It happens to fail JSON parse and falls back to `any`, so no crash — but the read *is* attempted, and a `$ref` to any well-formed JSON file outside the intended tree would succeed and influence output. This is default behavior (file resolver is always on), so an untrusted schema can probe the filesystem.

**Direction:** after `filepath.Join`, `filepath.Clean` and verify `strings.HasPrefix(resolved, baseDir)` (or resolve symlinks) before reading; reject escapes with a clear error.

### C7 — Remote-ref SSRF — CONFIRMED-by-read (Low, opt-in)
`pkg/schema/resolver.go:636-704`

With `--allow-remote-refs`, `HTTPResolver` performs `h.client.Get(docKey)` where `docKey` derives from `$ref` values in the schema, with no host allowlist, no redirect policy, no block on private/link-local ranges. A schema can force requests to `http://169.254.169.254/…` or internal services. The README frames remote refs as a security/reproducibility tradeoff but does not mention SSRF.

**Direction:** document the SSRF surface; optionally add an allowlist / deny private ranges when the resolver is enabled in automated contexts.

### C8 — Test coverage gap + missing CI — CONFIRMED (Medium, DX)
`tests/`, absence of `.github/`

`go test ./... -short` passes cleanly, yet C1–C4 are all reproducible with three-line schemas. The golden/roundtrip suites exercise the *external JSON Schema Test Suite* shapes (which happen to dodge these cases: object-bearing draft-3 branches, no struct-level `minProperties`, non-overlapping `allOf` bounds) plus curated goldens — but there is no targeted regression for (a) `min/maxProperties` on a property-bearing struct, (b) overlapping `allOf` numeric bounds, (c) primitive-only draft-3 type branches, (d) `anyOf` required enforcement. There is also no CI workflow, so `make lint`/`make test` are developer-discipline only.

**Direction:** add focused validation goldens for these four shapes; add a CI workflow running `make test` + `go vet` + a compile check on generated output.

### C9 — `CompositeResolver` masks the useful error — CONFIRMED (Low)
`pkg/schema/resolver.go:719-733`

It returns `lastErr` — the *last* resolver tried (HTTP). When a local `$ref` is simply missing, the user sees the HTTP resolver's "unsupported scheme"/network error rather than the file resolver's "no such file". Confusing diagnostics.

**Direction:** aggregate errors (e.g. `errors.Join`) or return the first resolver's error when the ref is scheme-less.

### C10 — `--validation runtime` is a no-op distinct from `hybrid` — CONFIRMED (Low, affordance)
`pkg/generator/validation.go:17-21`, README:83-88

The comment concedes: "The current implementation uses the same hooks as hybrid mode, but records intent in the generated metadata." The CLI offers three strategies; two are identical. A user selecting `runtime` expecting stronger guarantees gets `hybrid` behavior with a different `Mode` string.

**Direction:** either collapse to two modes or document explicitly that `runtime` is reserved/aliased.

### C11 — Same-basename inputs overwrite one output — CONFIRMED (Low)
`cmd/schemagen/root.go:165-170`, `259-266`

`deriveOutputFilename` uses `filepath.Base`, so `schemagen generate a/user.json b/user.json` writes both to `./user.go`; the second silently clobbers the first. The README only warns about this in the field-map section, not for generation generally.

**Direction:** detect output-path collisions across the input set and error (or namespace by directory).

### C12 — Docs drift — CONFIRMED (Low)
- README "How It Works" (125-132) lists four stages but omits the `ComputeBaseURIs` / `BuildResourceGraph` pass and the fact that generation performs **network/file I/O** (remote-ref side effects).
- `loader.go:19-27`: `.yaml`/`.yml` explicitly error ("not yet supported"), but the README's feature list and the `deriveOutputFilename` docstring (`"my-schema.yaml" -> "my_schema.go"`) imply YAML is handled. A user following the example hits a runtime error.

**Direction:** align docstrings/README with the JSON-only reality; note the resource-graph pass and I/O.

### C13 — oneOf dispatch nits — PLAUSIBLE (Low)
`pkg/emitter/templates/unmarshal.go:88-116`, `generator.go:2891-2938`
- In non-discriminated `UnmarshalJSON`, each matching variant assigns `{{$recv}}.{{FieldName}}` before the `oneofMatched > 1` check; on multi-match the field holds the last match and *then* errors — harmless (error returned) but the assignment order reads as accidental.
- `detectHeuristicDiscriminator` picks *any* shared property whose values are distinct consts across variants. If two unrelated variants coincidentally have a distinct-valued shared const property, dispatch keys off the wrong field. This is inherent to heuristics but undocumented as a footgun.

**Direction:** document the heuristic's selection rule; consider requiring the property be `required` in all variants.

---

## 4. Design tensions

1. **Validation is generated as open-coded templates, one keyword at a time, per type kind.** The same constraint (`minimum`, `pattern`, `multipleOf`) is re-implemented across `validation.go.tmpl`, `alias.go.tmpl`, `inferred_alias.go.tmpl`, `not_schema.go.tmpl`, `type_only_schema.go.tmpl`, and `bigint_alias.go.tmpl` — six copies with subtly different receiver expressions. This is why C1–C3 exist in *some* paths but not others, and why they slip past tests: there is no single source of truth for "what does `minimum` mean". **Alternative:** lower all constraints to a small IR of `Check` nodes and emit them through one shared template partial (the `validation_rule_inline` partial already hints at this) — or better, generate calls into a tiny runtime validator library (the `validationruntime` package is a started-but-unused vehicle for exactly this).

2. **The struct is treated as the schema, but JSON Schema is about the instance.** Encoding presence, property count, and `unevaluated*` on a Go struct forces a shadow model (`_jsonKeys`, `_jsonRawProps`, `_nonObject`, `AdditionalProperties` overflow) that must be threaded consistently. C2 is a direct symptom: `len(Fields)` conflates "declared" with "present". **Alternative:** for validation, operate over the raw `map[string]json.RawMessage` (already captured) rather than the typed struct; keep the typed struct purely for ergonomic access.

3. **`allOf`/`anyOf`/`oneOf` are handled by *merging into a single struct* rather than validating each branch.** Merging is lossy — it discards which constraints came from which branch, forcing "first-wins" (C3) and dropped requireds (C4). The `ObjectOneOfDef` path shows the correct model (per-branch runtime checks) exists but isn't applied uniformly. **Alternative:** always model composition as branch-checks over the raw object, and use the merged struct only to *type* the accessors.

4. **The generator mutates its input and relies on global-ish maps (`anchors`, `documentRoots`, `dynamicScope`) that are per-`Generate` but populated by side effect during recursion.** This makes `Generate` non-reentrant and order-sensitive (map iteration is sorted precisely to paper over this). C5 is the sharp edge. **Alternative:** a pure analysis pass that produces an immutable resolution index, consumed by a side-effect-free IR builder.

5. **Draft coverage breadth vs. depth.** Supporting drafts 3–2020-12 in one superset struct is elegant for parsing but means every validation path must consider six dialects; the `requiresStrictIntegerToken`, `supportsPrefixItems`, `refOverridesSiblings` gates are scattered. The primitive-only draft-3 branch (C1) fell through precisely because it's a rarely-exercised dialect corner. **Alternative:** narrow the *validation* target to the modern drafts and treat older drafts as parse-and-normalize-to-2020-12, so validation has one semantics.

---

## 5. Expectation gaps (expected X, found Y)

- **`anyOf` validation.** Expected: object matching no `anyOf` branch is rejected. Found: `Validate()` returns nil (C4).
- **`allOf` numeric bounds.** Expected: value must satisfy *all* branches. Found: only the first branch's bound is checked (C3).
- **`minProperties`/`maxProperties` on typed objects.** Expected: count present properties. Found: counts declared struct fields, so optional-field objects mis-validate (C2).
- **Draft-3 union types.** Expected: `type:["integer",{"type":"string"}]` accepts strings and integers, and compiles. Found: doesn't compile; rejects strings (C1).
- **`--validation runtime`.** Expected: a distinct, stronger strategy. Found: identical to `hybrid` (C10).
- **YAML.** Expected (from README feature list + docstring example): `.yaml` input works. Found: explicit "not yet supported" error (C12).
- **Multiple inputs.** Expected: each input → its own output. Found: same-basename inputs collide silently (C11).
- **Untrusted schema safety.** Expected (from "vendored locally … for security" framing): local mode is safe. Found: local `$ref` can traverse out of the schema dir (C6).

---

## 6. Open questions (code alone can't resolve)

1. **Intended trust model.** Is `schemagen` meant to run only on first-party schemas (making C6/C7 non-issues), or is running it on third-party schemas a supported use? The README's "vendored locally … for security" language suggests the latter matters.
2. **Is `Generate` intended to be reentrant / library-safe** (bearing on C5's severity), or is the CLI the only supported entry point?
3. **Validation completeness contract.** The `SchemagenValidationCapability()` surface implies callers can detect incomplete validation — but C2–C4 produce *silently wrong* results with `RequiresRuntime=false`. Is "static validation may be incomplete but never wrong" a goal? If so, C2–C4 are contract violations; if "best-effort", they're known gaps that should be surfaced in the capability metadata.
4. **Draft-3 support level.** Is full draft-3 validation a goal (C1), or is draft-3 parse-only with modern-draft validation acceptable?
5. **`--validation runtime` roadmap.** Is a genuinely different runtime path planned, or should the mode be removed?
