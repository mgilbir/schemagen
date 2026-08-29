# Changelog

## 0.1.2

### Changed

- Generated structs now declare their fields in the order that costs the least
  memory, rather than in the order they were built in. A struct's fields are
  laid out where they are written, and the compiler pads between them to reach
  each field's alignment, so `{"properties":{"flag":{"type":"boolean"},
  "count":{"type":"integer"}}}` spent seven bytes on nothing; the garbage
  collector reads the same declaration a second way and scans a value up to its
  last possible pointer, so a pointerless field written between two pointers was
  scanned along with them. Both costs are paid by every value of the type. The
  order chosen is `fieldalignment`'s, so that analyzer now reports nothing
  against schemagen's output, and it covers the whole declaration — the
  properties, the union field a `oneOf` becomes, the overflow maps, and the
  unexported members the decoder fills.

  Two visible consequences. Fields no longer appear in JSON-name order in the
  declaration, though every list that is read rather than laid out still is: the
  decoder's members, the validation order, and the property names in an error
  message are all unchanged. And because `encoding/json` writes an object's
  members in declaration order, the *key order of emitted JSON changes* — the
  same members with the same values, in a different order, which JSON gives no
  meaning to. Nothing about decoding, validation, or round-tripping changes.

### Fixed

- `--omit-empty=false` no longer emits a package that does not compile for a
  schema that forbids a property. The check for a property no value satisfies —
  `false`, `{"enum":[]}`, `{"not":{}}` — was written as `field != nil`, which
  holds under the default configuration because every optional property is
  pointer-wrapped there. That flag takes the pointer away, so the same property
  is a plain `string` and the emitted check was `e.Typed != nil (mismatched
  types string and untyped nil)`. Whether a field has a nil state is now asked
  of the resolved Go type rather than assumed, and where it has none the
  document's own key set answers alone — which is the better question anyway,
  and the one the rest of the check was already asking. Where the rule sits
  inside the presence guard an optional property gets, that guard *is* the
  question, so the refusal is now unconditional there instead of re-testing what
  the guard established. No output changes under the default configuration.
- A keyword spelled in another casing is no longer enforced as the keyword it
  resembles. `encoding/json` matches a key that matches no struct field exactly
  a second time case-insensitively, so every JSON Schema keyword was accepted in
  every casing — and the specification says an unrecognised keyword is ignored.
  It went wrong in four directions at once: `{"type":"string","MinLength":5}`
  refused `"ab"` against a constraint the document does not state,
  `{"$rEf":"#/$defs/S"}` took a type from a reference nobody wrote,
  `{"MinLength":"not a number"}` was refused at parse time as a malformed
  document rather than read as the legal one it is, and where both spellings
  appeared together the one that won was decided by their order in the document.
  A case variant is now an unrecognised keyword and nothing else — still
  preserved, still reachable by JSON Pointer, and constraining nothing. The
  discriminator's own fields are read by the same rule. The keyword list is the
  struct's own json tags, so a keyword added later is covered without a second
  list to keep in step, and the guard asks the question of every keyword rather
  than of the ones the issue named.
- A `$ref` written beside a keyword that survives it no longer runs the
  generator out of memory when it is reached through a `patternProperties`
  bucket or a per-branch `additionalProperties`/`unevaluatedProperties` check
  and leads back to the schema that holds it.
  `{"patternProperties":{"^x":{"$ref":"#","minLength":1}}}` took the process
  down with `fatal error: out of memory`, which no `recover` intercepts, and
  `{"patternProperties":{"^x":{"$rEf":"#"}}}` did it in thirty-nine bytes with
  no sibling written out at all. This is the same failure #348 fixed at the
  array element and tuple positions, at a third one; every arm that names a
  type after the position it was reached through now asks one guard, the
  enumeration of which arms those are is pinned by a test, and
  `generateTypeDef` carries a backstop so an arm added without the guard
  degrades to a recorded alias rather than to a dead process. Found by the fuzz
  memory gate.

### Internal

- The fuzz seed corpus and the fuzz body now carry a memory budget and a stack
  budget. An unbounded allocation is named, with the input that caused it,
  instead of killing the worker and leaving `fuzzing process hung or terminated
  unexpectedly` against a truncated artifact that does not reproduce. The fix
  above was found by that gate on its first run.

## 0.1.1

### Fixed

- A `$ref` written beside a keyword that survives it no longer runs the
  generator out of memory when it is reached through an array element or a
  tuple position and leads back to the array. `{"items":{"$ref":"#",
  "minItems":1}}` is thirty-five bytes of legal schema and took the process
  down with `fatal error: out of memory`, which no `recover` intercepts; the
  same shape written under a property was already handled. Found by the nightly
  fuzz job.

## 0.1.0

First release.

`schemagen` generates Go types from JSON Schema documents, with validation
compiled into the type rather than performed against the schema at run time. A
generated type decodes JSON, validates it, and marshals it back — and for the
shapes where that cannot be done statically, generation says so rather than
guessing.

### What it covers

- **Drafts 3, 4, 6, 7, 2019-09, 2020-12 and v1**, auto-detected from `$schema`
  or overridden with `--draft`. Each keyword is honoured over the dialect range
  that defines it, from a single table (`pkg/schema/keyworddialects.go`), so a
  document is read as the draft it declares.
- **Structural keywords** — objects, arrays, tuples, enums, aliases,
  `additionalProperties` and `patternProperties` with overflow maps, and
  `$ref`/`$defs` resolution against files, remote URLs (`--allow-remote-refs`)
  or documents given on the command line.
- **Composition** — `allOf`, `anyOf`, `oneOf`, `not`, `if`/`then`/`else`,
  `dependentSchemas`, `unevaluatedProperties`/`unevaluatedItems`, and
  discriminated unions.
- **Dynamic references** — `$anchor`, `$dynamicAnchor`/`$dynamicRef` and
  `$recursiveAnchor`/`$recursiveRef`, resolved statically where the schema
  decides the target and compiled to a runtime evaluator where the document
  does.
- **Lossless round-trips** — an absent optional property, a present `null` and
  a present empty collection all come back as themselves. Numbers keep the
  literal the document wrote when asked to (`--exact-numbers`), and
  `--big-int` carries integers past `int64`.
- **Multi-package output** — `--schema-package` gives each document its own Go
  package, and a `$ref` across the boundary emits an import rather than a second
  copy of the type. `--shared-types` puts everything in one package instead.

### What it deliberately does not do

These are decisions with reasoning recorded in the code, not gaps waiting to be
filled:

- **YAML input is not supported.** `.yaml` and `.yml` are refused by extension,
  wherever a document enters a run — as an input or through a `$ref` — and
  holding a JSON body does not change that.
- **A reference the *document* decides is refused, not guessed.** Where a
  `$recursiveRef` or `$dynamicRef` could resolve to more than one anchored
  resource depending on the value being validated, and the runtime evaluator
  cannot compile the schema, generation fails with a message naming the
  keyword, the anchor, how many declarations are in reach and what stopped the
  evaluator. A Go type would have to pick one and be wrong in both directions
  for every document that took another path.
- **A `date-time` field cannot hold a leap second.** `time.Time` cannot
  represent second 60, so a typed property refuses `1998-12-31T23:59:60Z` while
  decoding. A `minLength`, `maxLength` or `pattern` beside the `format` keeps
  the value a string and accepts it, with the format still asserted.
- **Asserting `format` changes the bytes, not only the verdict.** A value held
  as its Go type comes back canonicalised. The same escape applies.
- **Some schemas generate a type that enforces less than the schema states.**
  Where that happens the generated source says so — a `NOT VALIDATED` comment,
  or a caveat naming what is unchecked and how to get the stricter reading —
  rather than being silently weaker.

### Compatibility

Held against the [JSON Schema Test Suite](https://github.com/json-schema-org/JSON-Schema-Test-Suite):
2237 of 2252 groups exercised, 0 failures. The 15 untested groups produce no
`Validate()` method to call. Verdicts are also checked against
python-jsonschema, js-ajv, go-jsonschema and rust-boon through
[Bowtie](https://github.com/bowtie-json-schema/bowtie) where the suite has no
case; where those implementations disagree with each other, the disagreement is
recorded in the code rather than resolved by picking a side.

### Versioning

`schemagen --version` reports the tag for a released build, the module version
for `go install`, and the VCS pseudo-version for a local `go build`.

This is a `0.x` release: the generated output and the flag surface may change
between minor versions.
