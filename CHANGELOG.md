# Changelog

## Unreleased

### Changed

- A pattern is judged by the ECMA-262 grammar for the `u` flag, the dialect
  JSON Schema names, and nothing looser. ES2025 modifier groups are accepted
  and matched as specified, in every position a pattern occupies:
  `^(?i:ab)c$` accepts `ABc` and refuses `ABC`, and under `i` a class, `\w`,
  `\b`, `\p{...}` and a backreference compare by simple case folding. They were
  refused before, because the engine had none. What the `u` grammar makes an
  error is now refused with the keyword's JSON Pointer where it used to
  compile: a lone `{` or `}` (`a{`, `a{,5}`), a class escape as a range end
  (`[\w-a]`), a quantified lookahead (`(?=a)*`), `\00`, and a `\p{...}` name
  the specification does not list (`\p{WSpace}`; write `\p{White_Space}` or
  `\p{space}`). A property value alias it does list, such as
  `\p{Script=Latn}`, compiles where it used to be refused. `\-` and the other
  ASCII punctuation escapes outside a class keep working, by the rewrite to
  `\xHH` that already covered `\:`. No pattern in the test corpus or the JSON
  Schema Test Suite changes verdict.
- A keyword whose value is not a legal value of it is refused, naming its
  location, wherever the node's dialect defines the keyword, and ignored
  wherever it does not — the policy a null subschema such as
  `{"allOf":[null]}` already had. Before, which of the two a malformed value
  got depended on how it was parsed: `{"type":[1,2]}` became no type at all,
  `{"dependencies":{"a":null}}` a dependency every object satisfies,
  `{"not":null}` and `{"minLength":null}` no keyword, `{"required":["a",null]}`
  a required property named `""`, and `{"disallow":[null]}` a keyword that
  forbids nothing — while `{"divisibleBy":"x"}` was refused under 2020-12,
  which has no `divisibleBy`. An `$id` that is not a URI-reference is one such
  value. Draft 3's `maxLength`, a plain integer in its meta-schema, may be
  negative.
- A keyword written in the form another dialect gives it is refused under a
  dialect — stated, or forced with `--draft` — that defines the keyword but not
  that form, with a message naming whose spelling it is and what to write
  instead: array-form `items` under 2020-12 and v1, a boolean
  `exclusiveMinimum`/`exclusiveMaximum` under draft 6 and later and a number
  under drafts 3 and 4, draft 3's per-property boolean `required` under draft 4
  and later and the `required` array under draft 3, and draft 3's
  schema-valued `type` entries anywhere else. Such a value used to be read
  inconsistently: a draft-07 tuple forced to 2020-12 kept `items` as a tuple
  and dropped `additionalItems`, a reading neither dialect gives, and under
  draft 4 `{"minimum":3,"exclusiveMinimum":5}` accepted 4. With no recognised
  `$schema` every form binds, as before.
- Two schemas of one run that identify as the same URI — one `$id` declared
  twice, in one document or across the inputs, or an `$id` naming another
  input's file — are refused, as JSON Schema says a validator should. A
  resource index cannot answer a URI that names two schemas; the default mode
  used to let such an `$id` answer no `$ref`, and a document holding one kept
  whichever of the two one walk met last and another met first. A plain-name
  anchor declared by two nodes of one resource is refused when a reference
  names it, rather than answered with one of them.
- With several inputs, a file reference is confined to the input directories
  that hold the file it is written in, rather than to the first input's
  directory or to any of them.
- `schema.ResourceIndex` is the library's reference resolver: a `Config.Resolver`
  is used as the loader of an index the generator builds, and a
  `*schema.ResourceIndex` passed as `Config.Resolver` is shared as it is, so
  several generators see one instance of each document. A document read with
  `schema.LoadFromFile` or through a `FileResolver` records the file it came
  from (`RetrievalURI`) and is based on it; one decoded by the caller is given
  a base URI under the `schemagen-document` scheme. `FileResolver` takes
  several confinement roots (`WithFileResolverRoots`).

### Fixed

- Every JSON Schema pattern is matched by one engine, compiled once, and a
  match the engine cannot decide is an error rather than a "no". A `contains`
  whose sub-schema had a `pattern` compiled it with Go's RE2 on every element,
  so `{"contains":{"type":"string","pattern":"^(?!a)"}}` made `Validate()`
  panic; a `patternProperties` key was matched against a *declared* property
  name with RE2, so a lookahead key was dropped in silence; and every
  ECMA-262 match discarded the engine's error, so `^[a-z]+$` rejected a valid
  600,000-character string (its step budget ran out) and quoted all of it back
  in the message. Patterns are now compiled with the ECMA-262 engine and the
  `u` flag into package-level variables of the helper file, `Validate()`
  compiles nothing, and a match with no answer returns an error wrapping
  `ecma262.ErrStepLimit` in every position -- including inside `not`,
  `anyOf`, `oneOf`, `if` and `contains`, where reading it either way would
  flip the verdict. A pattern that is not a regular expression is refused at
  generation time with the JSON Pointer of the keyword. The engine dependency
  moves to goecma262 v0.2.0, which makes matching stack-safe and linear for
  common patterns and parses by the ECMA-262 grammar.
- A property declared in `properties` whose name a `patternProperties` key
  matches is held to the whole of that pattern's schema, as an undeclared
  member is. Only the handful of keywords a field rule could express reached it
  before, and never on an untyped property: `"bar": {"type":"string"}` beside
  `"^b": {"type":"integer"}` accepted `{"bar":"abc"}`.
- Error messages bound the document text they quote: a value, key, `const` or
  `enum` value longer than 128 bytes is cut to its first 64 and marked
  `(N bytes, truncated)`, and a parser error that echoes its input is cut by
  the same rule.
- A `contains` check no longer refuses an element of a type its keyword says
  nothing about: `{"contains":{"minimum":3}}` refused `["a"]`,
  `{"contains":{"pattern":"^a"}}` refused `[5]`, and a null was measured as
  the `0` or `""` encoding/json decodes it into. Each keyword judges its own
  JSON type only -- the numeric bounds and `multipleOf` a number, `minLength`,
  `maxLength` and `pattern` a string -- including where `contains` decides
  which items `unevaluatedItems` may still refuse.
- A `$ref` resolves in the schema resource it is written in. `"#/$defs/Name"`
  written in another document, or in a subschema with its own `$id`, meant the
  root document's `Name` whenever the root had one — `other.json`'s
  `{"$ref":"#/$defs/Name"}` was typed as the root's integer rather than its own
  string, refusing every valid document and accepting invalid ones — and
  `"#anchor"`, `"other.json#anchor"`, a URN `$id` with an anchor, a relative
  `$id` and the static target of a `$dynamicRef` went the same way. Every
  reference now resolves through one index of every document and embedded
  resource of the run, keyed by absolute URI, against the base URI of the
  resource it is written in; an embedded resource named by its `$id` from a
  document without one, which the library could not resolve at all, resolves.
- `--shared-types`, `--schema-package` and a config file's packages read a
  relative `$ref` next to the file it is written in. They read it next to the
  first input, or whichever input directory answered first, so a reference in
  `b/y.json` to `z.json` generated from `a/z.json` in silence. All three modes
  and the default now load their inputs and resolve references the same way,
  and a document an input reaches by relative path is that input, not a second
  copy of it declaring a second type.
- Every Go identifier a generated package declares is now handed out by one
  name registry, so no position is typed by another schema's type and no name
  is declared twice. Names used to be minted per arm, and several arms took a
  name another node already held: an array of objects under property `a` beside
  a `$defs` entry `RootAItem` was typed as that definition (`{"a":[{"z":"q"}]}`
  failed to decode, and with enum items generation was refused as "a defect in
  schemagen"); a `oneOf` of `null` and a `$ref` bound `$defs/a-b`'s type for a
  reference to `$defs/a_b`; a `oneOf` variant titled `Foo` reused `$defs/Foo`;
  a variant titled `String2` beside two `String` variants declared `String2`
  twice; an enum constant `AB` collided with a type `AB`; a union getter
  `GetCat` collided with a field named `getCat`; a definition named
  `SchemagenValidationMode` collided with the capability constant; and a
  sibling package named `ecmaflags` was imported under the name the pattern
  checks spell. A name another holder has is numbered (`Foo2`, or `String2_2`
  for a name ending in a digit); definitions and the root claim their names
  before anything is generated, so a name minted for a position never displaces
  one.
- Through the library, two references whose last pointer token is the same
  (`#/x-alpha/1`, `#/x-beta/1`) got one Go type between them. They are numbered
  apart, as the CLI already did. Likewise a reference that resolved to another
  document's definition was typed as the root's same-keyed definition
  (`other.json#/$defs/Thing` beside the root's own `$defs/Thing`); such a
  definition is now named from the key it is written under in its own
  resource, whichever reference reaches it — a pointer or an anchor — and
  numbered off the root's (`Thing2`).
- The `--shared-types` and `--schema-package` collision warnings are written from
  the names the generator declared, after generation, so a warning no longer
  names a type the package does not have ("other.json $defs/Name becomes
  OtherName" in a run with no `OtherName`). A definition moved off a name
  generated code already spells is reported too.
- A name inside a type that moved used to change the generated API without a
  word: a field numbered off a generated method or another property's field
  (`validate` → `Validate1`, `a-b`/`a_b` → `AB1`/`AB2`), a union getter numbered
  off a field together with its wrapper type (`GetCat2`, `Root_Cat2` beside a
  property `getCat`), an enum constant or package variable numbered off a type.
  Each is now reported, one line saying what the name is, where the document
  wrote it (`#/properties/validate`), what took the one it wanted, and how to
  choose it; `Generator.NameMoves` lists them with the type they belong to and
  their location (`NameMove.Type`, `NameMove.Location`, `NameMove.IsMember`).
- The name collision warnings are shorter: each says what moved, from where,
  why, and how to choose the names in a few lines, and the explanation of how
  names are separated is written once per run instead of about a thousand
  characters of it per contested name.
- A qualified name that lands on a name another definition of the package
  already has, or on the name of a position, is numbered instead of refusing
  the run.
- A draft-07 document's `definitions` entry qualified by its keyword is named
  `DefinitionsX`, not `DefsX`: the `$defs` mirror normalization writes is no
  longer read as the keyword the document used.
- Under `--lenient-refs`, an unresolved `$ref` in a position that needs a type
  name no longer binds a declared type that happens to share the derived name;
  it spells a name of its own, which the file's DOES NOT COMPILE notice names.
  A nullable `oneOf` of `null` and an unresolved `$ref` is `any`, like any other
  property holding an unresolved reference.
- Identifiers and struct-tag names are decided by the Unicode tables of the
  oldest supported Go (the go.mod minimum, Unicode 15.0.0), not by the Go
  running the generator. A generator built with Go 1.27 (Unicode 17) emitted a
  field `Ɤx` for a property `ɤx` and a field `AᲉ` for `aᲉ`, which Go 1.25 does
  not compile, and a tag Go 1.25's `encoding/json` ignores.
- Schema text can no longer become code in the generated file. A property name
  or a `$ref` string was written into a `//` comment as it stood, so a newline
  in it ended the comment and the rest was compiled:
  `{"dependentSchemas":{"t\npanic(\"INJECTED\")\n//":{...}}}` put a
  `panic` in `Validate`, and under `--lenient-refs` a `$ref` of
  `"other.json#/x\nimport _ \"net/http/pprof\"\n//"` added an import to the
  top of the file, whose `init` then runs in every program that links the
  package. Escaping was a function each template had to remember to call; it is
  now a guard the emitter appends to every template action, chosen by where in
  the Go source the action writes (code, comment, string literal, format
  literal, struct tag), and a test reads every template and fails on any action
  that writes a value into a literal or into code without the escaper for that
  place. The same fix covers the failures that were loud rather than silent: a
  `pattern` holding a backslash under `patternProperties` or on a non-object
  branch (`"^\\d+$"`) no longer fails with `unknown escape sequence`, a `%`
  in a pattern is printed rather than read as a verb, a NUL, a byte order mark
  or a bidirectional control in a `description`, `title` or `examples` is
  written as an escape rather than failing with a dump of the source, and a
  discriminator or a `oneOf` property whose name holds a quote or a backtick is
  generated rather than refused.
- A `oneOf` at a property whose name a struct tag cannot carry — `"a,b"`,
  `"-"`, `""`, a name with a quote — is read and written under its exact name.
  The union's member went through a struct tag no matter what the name was, and
  a property named `""` was taken for a union standing for the whole value.
- A package name or import alias that is not a Go identifier is refused with an
  error naming it, instead of reaching gofmt and failing with a dump of the
  file.
- `{"propertyNames":{"pattern":""}}` no longer emits a loop that declares a
  variable it never uses. The empty pattern matches every name and constrains
  nothing.
- A method's own locals no longer shadow its receiver. The receiver is named
  after the type's first letter, and the templates declared single-letter
  locals inside method bodies, so a type named `V…` with a property decoded by
  hand did not compile (`v.AB undefined`), neither did a `multipleOf` on a
  property of a type named `Q…`, and on an alias named `Q…` the refusal reported
  the quotient as the value that failed.
- Schema parsing no longer uses in-band markers, wrapping integers, a
  second percent-decode, or a dialect gate that could not see every subschema.
  - Draft 3's boolean `"required": true` was stored as the property name
    `"\x00__draft3_required_true__"` in the required list. At a draft-3 root, or
    under `additionalProperties` with no `$schema`, it reached the generator as
    a required property of that name and the type refused every object; and a
    2020-12 document that really requires a property of that name had the
    requirement gated away as draft 3's boolean. It is now a field of its own.
  - An integer count past int64 (`minLength`, `maxItems`, `minContains`, …) was
    read through `float64` and wrapped to `MinInt64`, so `{"maxLength":2^63}`
    refused `""` and `{"minLength":1e19}` accepted `"x"`. Counts are read
    exactly, and one too large for an `int` is held at `MaxInt`, which keeps the
    schema's verdict exactly: a maximum that large admits every value and a
    minimum that large admits none. An error message states the bound as the
    schema wrote it (`1e19`, not `9223372036854775807`), and a bound past int32
    is emitted as a constant expression that compiles on a 32-bit target too.
  - A JSON Pointer in a `$ref` is percent-decoded once, then split, then
    RFC 6901-unescaped, by one decoder every resolver and the generator share
    (RFC 6901 §6). A reference into another document was decoded twice, so
    `#/$defs/a%2525b` named the key `a%b` there and `a%25b` locally; and a local
    pointer was split before decoding, so `#/$defs/a%2Fb` named the key `a/b`
    where every implementation Bowtie runs walks `a`, then `b`.
  - The subschemas inside `dependencies`, `extends` and `disallow` are parsed
    with the document, so the dialect pass gates them like any other subschema:
    a draft-4 `dependencies.a.properties.b.const` was enforced although draft 4
    has no `const`. A vendor keyword's value reached by `$ref` is read under
    its parent's dialect for the same reason. A `$ref` naming such a subschema
    where the document wrote it — `#/dependencies/a`, `#/extends/0`,
    `#/disallow/1`, draft 3's `#/type/1` — resolves; it failed once Normalize
    had moved the subschema to the keyword that replaced it. So does a `$ref`
    into any keyword the node's dialect does not define (draft 3's `#/not`),
    as one into an unknown keyword always did.
  - `$schema` is matched as a whole URI (http or https, with or without the
    trailing `#`), so `https://example.com/my-draft-07-extension/schema` is no
    longer read as draft 7.
  - A duplicated key means its last value, for every key of every object:
    `{"properties":{"a":{}},"properties":{"b":{}}}` read as both `a` and `b`,
    and as `b` alone when the object also held an unrelated case-variant key.
  - `Normalize` is idempotent. A second call on a draft-3 document dropped
    the required list the first call had built.
  - A refusal names the location the document wrote the value at, not the path
    into the rewritten document: a null `minLength` in the second `extends`
    entry was reported at `#/allOf/1/minLength` and is now at
    `#/extends/1/minLength`; a draft-07 `definitions` member at `#/$defs/a` is
    now at `#/definitions/a`; `{"disallow":{"not":"a type"}}` at `#/not/not` is
    now at `#/disallow/not`. A null or non-schema entry is named at the entry
    (`#/extends/0`, `#/dependencies/a`, `#/type/1`) rather than at its keyword
    with the entry in the message, and a location is written as a URI
    fragment, so a key a fragment cannot hold literally is percent-encoded
    (`#/patternProperties/%5Ea`). A value in another document is named by that
    document's URI, and a definition in the name-collision warnings by the
    location its document or embedded resource wrote it at
    (`#/definitions/Thing`, not the `$defs/Thing` mirror).
  - Two URLs that redirect to one remote document share one parsed copy of it,
    and a remote document served as `text/plain` — as `raw.githubusercontent.com`
    serves every file — is read. Whether a body is a schema is decided by
    parsing it; the `Content-Type` is named only to explain one that does not
    parse.

### Changed

- Library API: `generator.PinnedNameCollisionError` is gone. Names pinned
  through `Config.DefinitionTypeNames` are held from `New`, so no other node can
  take one and the refusal it reported cannot happen; a name that would land on
  a pinned one is numbered. `ConstCheck.GoFieldName`, which nothing read, is
  gone. `Generator.DeclaredTypeName` and `Generator.NameMoves` report what the
  name registry declared and moved; `NamingDefectError` is what generation
  returns if a declaration ever reaches a name held for another node.
  `NumberedName`, `IsIdentifier`, `IsExportedIdentifier` and
  `IdentifierToLower` are the registry's spelling rule and the pinned-Unicode
  identifier predicates, for callers that name things beside the generator.
- Through the library with `SharedTypes` and no pins, identical definitions of
  two documents are two types (`Thing`, `Thing2`) rather than one: the
  generator cannot judge that two definitions agree, and sharing a name between
  two nodes is what it no longer does unasked. The CLI judges agreement and pins
  agreeing definitions to one name, so `--shared-types` still shares them.
- An optional property reached through a chain of `$ref`s round-trips as
  absent. `{"$ref":"#/$defs/A"}` with `A` a `$ref` to an object became a value
  field that `omitempty` never omits, so `{"name":"x"}` was written back as
  `{"name":"x","sig":{"q":""}}` -- a property the document never had, satisfying
  the definition's own `required` -- or, where the object was a `oneOf`, as
  `"sig":null`, which the same type then refused to read. Every CycloneDX 1.6
  BOM has such a property (`signature`). Whether an optional field needs a
  pointer, and whether it has a nil state, is now decided by the type at the end
  of the chain of names, however long, across documents and across packages;
  under `--schema-package` an alias over another package's alias over `any` or a
  pointer, which did not compile, now does. Under `--omit-empty=false` a union
  whose zero is written as `null` is omitted where the schema forbids `null`.
- Decoding costs time and memory in proportion to the document, however deeply
  it nests. Each level of a recursive type decoded its whole subtree again:
  `{"c":{"c":...}}` 8,000 levels deep took six seconds, a 24 KB document with an
  `if`/`then` beside the members kept 70 MB of copies alive, and a refusal at the
  deepest level took time exponential in the depth -- a 200-byte document did not
  finish. Every generated type now decodes the value it is handed in place, over
  one indexed copy of the document, and reports a refusal without decoding
  anything a second time.
- A decoded value no longer shares memory with the buffer it was decoded from,
  or with a copy of it taken before a later decode. A heterogeneous `enum` kept
  the caller's slice as its value -- under a `json.Decoder` over a stream, 133 of
  400 decoded values changed -- and the raw-JSON wrappers wrote each decode over
  the array they already held. `Raw()`, `MarshalJSON()` and `BigInt()` return
  copies rather than the value's own bytes.
- A type's `UnmarshalJSON` called directly with bytes that are not JSON refuses
  them with `encoding/json`'s own words. The raw-JSON wrappers, the inferred
  wrappers and the heterogeneous enums accepted them and wrote them back out.
- An object-level `oneOf` or `anyOf` branch closed with `"additionalProperties":
  false` no longer matches an object carrying keys it forbids. A `oneOf` counted
  it as a second match and refused the object: four of the example BOMs the
  CycloneDX 1.6 specification ships (a jsf signature, a model card's inline
  dataset) failed `Validate`.

### Changed

- Decoding into a value replaces it. Every generated `UnmarshalJSON` starts from
  the zero value, so a value decoded twice is the second document and nothing of
  the first: `{"a":"x","extra":1}` and then `{}` into one struct used to validate
  as "a: required property is missing" while marshalling as `{"a":"x"}`. This is
  a deliberate difference from `encoding/json`, whose own decode merges. See the
  README's "Decoding: replaced, owned, and linear".
- A property written twice in an object a generated type decodes means its last
  value, as it does where pkg/schema reads a schema: the earlier occurrence is
  not decoded at all. It used to be decoded too, and one that did not decode
  refused the document with a message that named no property. A map or a slice
  of scalars is still decoded whole by `encoding/json`, whose rule inside it is
  the same for the value and refuses an earlier occurrence that does not
  decode.

## 0.1.3

### Added

- `--raw-untyped` (`Config.RawUntyped`, config key `rawUntyped`) holds a
  position the schema gives no type to as the bytes the document wrote
  (`json.RawMessage`) rather than the `any` `encoding/json` decodes them into.
  An untyped position — `{"properties":{"payload":{}}}`, a property whose
  schema is `true`, a `$defs` entry with a description and nothing else — put
  `{"z":9007199254740993,"a":1.10,"big":123456789012345678901234567890}`
  through a `float64` and a `map[string]any` and returned
  `{"a":1.1,"big":1.2345678901234568e+29,"z":9007199254740992}`: the integer
  past 2^53 rounded, the trailing zero dropped, the big integer in exponent
  notation, the members reordered. `--exact-numbers` could not reach it,
  because it acts on the declared type and there is none. Under the new flag
  the value round-trips as written in a property, an array element, a map
  value, a `$defs` alias, a reference cycle with no content, and the values of
  a bare `{"type":"object"}`; a tuple, a bare `{"type":"array"}`, an
  unenforced alias, a lenient `$ref` and a `oneOf` branch keep their `any`,
  for reasons the flag's doc comment gives. Validation verdicts are unchanged:
  the checks that read a raw element from beside its schema — `uniqueItems`,
  and a `contains` naming a `const` or an `enum` — compare canonical JSON
  text rather than bytes, so `[1, 1.0]` is still not unique. Off by default,
  and with it off the generated source is byte-identical to before.

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

## 0.1.2

### Fixed

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
