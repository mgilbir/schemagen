# schemagen

A CLI tool that generates idiomatic Go type definitions from JSON Schema files.

## Features

- Generates Go structs with proper `json` struct tags from JSON Schema objects
- Supports primitive types, arrays, nested objects, enums, and type aliases
- Full `$ref` / `$defs` resolution (file-based references rooted at schema directory)
- Optional remote `$ref` resolution over HTTP/HTTPS (`--allow-remote-refs`)
- Composition keywords: `allOf`, `anyOf`, `oneOf` (including nullable via `oneOf` with null)
- Discriminated unions with automatic or heuristic-based discriminator detection
- Validation-aware: string constraints (`minLength`, `maxLength`, `pattern`), numeric constraints
- Format handling (e.g., `date-time`, `email`, `uri`, `uuid`)
- Content handling (`contentEncoding`, `contentMediaType`), asserted under the one dialect that asks for it
- Lossless nulls: an absent optional property, a present `null` and a present empty collection all round-trip as themselves
- Default values for struct fields
- `additionalProperties` and `patternProperties` support with overflow maps
- Optional `*big.Int` wrapper for arbitrary-precision integers
- Supports JSON Schema drafts 3, 4, 6, 7, 2019-09, 2020-12 and v1 (auto-detected or overridden via `--draft`)

## Installation

```bash
go install github.com/mgilbir/schemagen@latest
```

Or build from source:

```bash
make build
# binary is placed in bin/schemagen
```

## Usage

```bash
schemagen generate [schema files...] [flags]
```

### Example

```bash
schemagen generate person.json -o ./models -p models
```

This reads `person.json`, generates Go types, and writes the output to `./models/person.go` with package name `models`.

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--output-dir` | `-o` | `.` | Output directory for generated files |
| `--package` | `-p` | `generated` | Go package name for generated code |
| `--omit-empty` | | `true` | Add `omitempty` to optional JSON fields. With `--omit-empty=false` an optional field is written even when it holds its Go zero — `{}` marshals as `{"s":"","i":0,"b":false}` — except where that zero is a value the schema forbids at that position: a `null` for a typed property, or a zero the property's `const`, `enum`, `minLength`, `pattern` or numeric bounds exclude. Those are omitted rather than written, because there is no value to write there and every candidate is one the schema may equally reject |
| `--strict-properties` | | `false` | Treat absent `additionalProperties` as false for validation while still preserving overflow properties for round-trip output. Read on every object schema, including the sub-schemas the generator compiles to schema data rather than to a struct. An `allOf` branch's properties are pooled into the object the branches compose, as the merged struct pools them; every other applicator's sub-schema is a schema object in its own right and is read on its own terms, which is `additionalProperties`' own reading and can make a discriminated or conditional object unsatisfiable |
| `--strict-read-write` | | `false` | Make `readOnly` and `writeOnly` change what the type accepts and emits, not just its doc comment (see below) |
| `--big-int` | | `false` | Generate `*big.Int` wrapper for integer types |
| `--exact-numbers` | | `false` | Hold `"type":"number"` as the literal the document wrote (`json.Number`) rather than the `float64` it rounds to, and compare every numeric keyword on it exactly (see below) |
| `--format-assertion` | | `false` | Assert `format` on every draft. Without it the dialect decides (see below) |
| `--format-annotation` | | `false` | Treat `format` as an annotation on every draft. The opposite of `--format-assertion`, and mutually exclusive with it |
| `--allow-remote-refs` | | `false` | Allow fetching remote `$ref` schemas over HTTP/HTTPS |
| `--draft` | | *(auto)* | Override JSON Schema draft version (values: `3`, `4`, `6`, `7`, `2019-09`, `2020-12`, `v1`) |
| `--validation` | | `static` | Validation strategy: `static`, `hybrid`, or `runtime` |
| `--field-map` | | | Path to a JSON file pinning JSON properties to specific Go field names (see below) |
| `--lenient-refs` | | `false` | Degrade `$ref`s that no resolver can serve to `any` instead of failing generation |
| `--root-name` | | | Exact Go type name for a root schema, used verbatim. A bare name (single input only), or a repeatable `<key>=<Name>` pair (see below) |
| `--root-name-from-filename` | | `false` | Derive each root type name from the schema filename, e.g. `person.json` → `PersonJSON` |
| `--shared-types` | | `false` | Generate all schemas into one Go package, sharing materialized types and helpers (see below) |
| `--schema-package` | | | Assign a document to a Go package: `<document $id>=<Go import path>`. Repeatable; activates multi-package generation (see below) |
| `--schema-output` | | | Output file for a document: `<document $id>=<file path>`. Requires `--schema-package` |
| `--config` | | | Path to a JSON generation config (see below). Flags override it |
| `--verbose` | `-v` | `false` | Print progress information |

### Format: assertion or annotation

`format` means different things in different drafts, and the generated code
follows the document's own dialect rather than a single house rule.

| Dialect | Default | Why |
| --- | --- | --- |
| draft 3, 4, 6, 7 | **asserts** | The spec says an implementation SHOULD validate a format it recognises, and MAY treat it as an annotation. Asserting is the reading this generator takes. |
| 2019-09, 2020-12 | **annotates** | The default meta-schema declares the `format-annotation` vocabulary, whose content is that `format` produces an annotation and no assertion. `{"format":"email"}` is satisfied by `"2962"`, and the official test suite marks that document valid. |
| v1 | **asserts** | v1 drops vocabularies, and the official test suite states format assertion at the required level — its `format/` directory is top-level rather than under `optional/`, and there `{"format":"email"}` is *not* satisfied by `"2962"`. The annotation reading is what moves to `optional/` instead. |
| no `$schema` | **annotates** | An unknown dialect is read as a modern one everywhere else in the generator, and withholding a rejection is the safe direction. |

So the history runs: asserted in the older drafts, demoted to an annotation in
2019-09 and 2020-12, and promoted back to an assertion in v1.

`--format-assertion` asserts on every draft, for callers who want the older
behaviour on a newer document. `--format-annotation` is its opposite and
annotates on every draft, for callers on a dialect that asserts. Passing both is
an error rather than a silent tie-break.

The flag reaches the Go type as well as the check. `date-time` maps to
`time.Time` and `ipv4`/`ipv6` to `netip.Addr`, and those types assert the format
by decoding it — an unparseable value simply fails `json.Unmarshal`, whatever
`Validate` does. Under an annotating dialect the mapping is therefore withheld
too and the value stays a `string`; pass `--format-assertion` to get the typed
representation back.

That mapping has two costs, and they are the same trade seen twice: the Go type
is the representation, and holding a value as one is not free. The first is that
a `date-time` field cannot hold a leap second; the second is that it does not
give back the bytes it was handed. Both are below, and both have the same escape.

#### A `date-time` field cannot hold a leap second

RFC 3339 §5.7 admits second 60 at the end of a UTC day, and the official test
suite marks `"1998-12-31T23:59:60Z"` valid. `time.Time` cannot represent that
second at all, so a `date-time` property held as one refuses the value while
decoding, before `Validate` is reached. No respelling helps: there is no input a
`time.Time` accepts that denotes second 60, which is what separates this from the
`t`/`z` case-folding a `date-time` does handle.

Only the typed spelling under an asserting posture is affected, and then every
spelling of the leap second alike — the `Z` form, the lowercase `z` RFC 3339 also
admits, and an offset form such as `"1998-12-31T15:59:60.123-08:00"`. Two
spellings beside it accept the same value:

| Schema | Held as | `"1998-12-31T23:59:60Z"` |
| --- | --- | --- |
| `{"type":"string","format":"date-time"}` | `time.Time` | refused, on decode |
| `{"format":"date-time"}` — no `type` | JSON string | accepted |
| `{"type":"string","format":"date-time","minLength":1}` | `string` | accepted |

`format: time` is unaffected in either spelling. It is never mapped to a Go type
— `time.Time` carries a date the keyword does not state — so its own leap second
`"23:59:60Z"` is checked as a string and accepted whether a `"type":"string"`
stands beside it or not.

The third row is the escape route, and it is the same one the canonicalisation
note below gives: a `minLength`, `maxLength` or `pattern` beside the `format`
keeps the value a `string`, and the format is still asserted, in `Validate`. That
check is not the weaker of the two. It refuses `"1998-12-31T23:58:60Z"` and
`"1998-12-31T23:59:61Z"`, because second 60 is only ever the last second of a UTC
day — the `time.Time` path cannot make that distinction, having refused the whole
family already.

Under an annotating posture the question does not arise: the value stays a
`string` and `format` asserts nothing at all.

#### Asserting `format` changes the bytes, not only the verdict

`time.Time` and `netip.Addr` hold a parsed value, not the text it was parsed
from, so marshalling writes that type's canonical spelling rather than the one
the document arrived with. Read a document, change one unrelated field, write it
back, and fields nobody touched come out rewritten — which shows up as a
spurious diff, and breaks a signature taken over the document bytes. Nothing is
lost semantically; the two spellings denote the same instant and the same
address.

| Field | In | Out |
| --- | --- | --- |
| `date-time` | `"2020-01-02T03:04:05.000Z"` | `"2020-01-02T03:04:05Z"` |
| `date-time` | `"2020-01-02T03:04:05.500+02:00"` | `"2020-01-02T03:04:05.5+02:00"` |
| `date-time` | `"2020-01-02T03:04:05+00:00"` | `"2020-01-02T03:04:05Z"` |
| `ipv6` | `"2001:0db8:0000:0000:0000:0000:0000:0001"` | `"2001:db8::1"` |
| `ipv6` | `"2001:DB8::1"` | `"2001:db8::1"` |
| `ipv6` | `"::ffff:c0a8:1"` | `"::ffff:192.168.0.1"` |

Trailing zeros in the fractional second are dropped, `+00:00` and `-00:00`
become `Z`, and an IPv6 address is written in the RFC 5952 form. `ipv4` maps to
`netip.Addr` too, but a dotted quad has only one accepted spelling — a
non-canonical one such as `010.1.1.1` is *rejected* on decode rather than
rewritten — so nothing there changes shape.

Those three formats are the whole of it. `date`, `time`, `duration`, `uuid`,
`email`, `hostname`, `uri` and the rest stay `string`, are checked in `Validate`,
and come back out byte for byte.

This follows the type mapping, not the flag, so it applies wherever `format`
asserts: drafts 3, 4, 6 and 7 and v1 by default, 2019-09 and 2020-12 under
`--format-assertion`. Under an annotating posture — 2019-09 and 2020-12 by
default, or `--format-annotation` anywhere — the value stays a `string` and
round-trips exactly.

To assert the format and keep the caller's bytes, give the schema a
`minLength`, `maxLength` or `pattern` beside the `format`. That combination
already keeps the string (neither type carries those keywords), and the format is
still checked, by parsing the string in `Validate`. It is the same edit that lets
a `date-time` hold a leap second, for the same reason: what the value is held as
is what decides both.

`hostname`, `idn-hostname`, `email` and `idn-email` are checked with
[`golang.org/x/net/idna`](https://pkg.go.dev/golang.org/x/net/idna), which is the
only dependency generated code takes beyond the ECMA-262 engine `pattern`
already needs. It is imported **only** into packages whose schemas name one of
those four formats; a package with, say, a `date-time` in it takes neither.

Two parts of IDNA2008 the library does not answer are added on top of it, and
both are applied to the decoded form so an `xn--` A-label is judged by what it
encodes: the ContextO rules of RFC 5892 appendix A.3–A.9, which `idna` has no
equivalent of, and the ten RFC 5892 section 2.6 exceptions whose derived
property is DISALLOWED, which UTS-46 lookup processing marks valid and maps
rather than refuses. The PVALID and CONTEXTO members of that same section stay
accepted, since refusing them would reject names IDNA2008 permits.

### Content: assertion or annotation

`contentEncoding` and `contentMediaType` are the same question one vocabulary
over, and the dialect decides in the same way.

| Dialect | Default | Why |
| --- | --- | --- |
| draft 7 | **asserts** | The draft that introduced the keywords says an implementation SHOULD decode the string and MAY refuse one it cannot — the same permission `format` is read under. |
| 2019-09, 2020-12 | **annotates** | The content vocabulary is annotation-only by definition. `{"contentEncoding":"base64"}` is satisfied by `"eyJmb28iOi%iYmFyIn0K"`, which is not base64 at all, and the official test suite marks that document valid. |
| draft 3, 4, 6 | **annotates** | The keywords are not defined there, so a document carrying one is carrying an unknown keyword, which every draft says to ignore. |
| v1 | **annotates** | The suite's `content.json` marks every malformed payload valid, as 2019-09 and 2020-12 do. v1 reverses the `format` decision, not this one. |
| no `$schema` | **annotates** | As for `format`: withholding a rejection is the safe direction. |

`base64` is the only `contentEncoding` and `application/json` the only
`contentMediaType` the generated code decides; anything else is carried as an
annotation rather than guessed at. `contentSchema` never asserts — it is an
annotation in every draft that defines it.

The keywords apply to strings and to nothing else, so a number, an object or a
null satisfies them trivially. A schema stating one with no `"type"` therefore
does **not** become a Go `string`: it becomes the same wrapper a `format` with no
`"type"` gets, which holds a Go string when the instance is one, keeps any other
value verbatim, and returns early from `Validate()` for it.

### Null: present, absent, and the round trip

A JSON null and an absent property leave the same thing behind in Go — a nil
pointer, a nil slice or map, or a scalar at its zero — so the two have to be told
apart from the document's own bytes, and the generated `UnmarshalJSON` is where
that happens.

- Where the schema **forbids** a null, the decode refuses it, naming the property.
- Where the schema **permits** one, the key is recorded. `Validate()` then passes
  over the keywords a null satisfies vacuously (a `minLength` says nothing about
  a null), and `MarshalJSON()` writes the null back.

So an absent optional property stays absent, a present null comes back as a
null, and a present empty collection comes back as `[]` or `{}`. All three are
distinguishable. A value built in Go rather than decoded carries no such record,
and its nil fields are simply omitted.

### Numbers: exact, or `float64`

A JSON number has no precision limit and a `float64` has two. By default a
property typed `"number"` is a `float64`, so a document carrying
`1.2345678901234567890` comes back as `1.2345678901234567`, and one carrying
`123456789012345678901234567890` comes back as `1.2345678901234568e+29`. A
read-modify-write then rewrites a field the caller never touched, silently.

Integers have not had that problem for some time: an `int64` holds every integer
up to its own range exactly, and `--big-int` carries the rest. `--exact-numbers`
is the same guarantee for the other JSON numeric type.

```bash
schemagen generate schema.json --exact-numbers
```

With it, a `"number"` is `encoding/json`'s `json.Number` — the literal as
written. Nothing rounds, so the value that arrives is the value that leaves,
byte for byte, in every position the schema gives the type to: a scalar
property, an array element, a map value, a `$defs` alias, an enum member, a
`default`. Values past `float64`'s range (`1e400`) decode rather than being
refused, and `-0.0` comes back as `-0.0` rather than as `-0`.

Every numeric keyword — `minimum`, `maximum`, both `exclusive` forms,
`multipleOf`, `const` and `enum` — is then compared on the literal, through
exact decimal arithmetic rather than through a `float64` that cannot tell
`0.1` from the number it actually holds for `0.1`. Two spellings of one number
compare equal, so `1.50` satisfies a `const` of `1.5` and `1e-1` satisfies a
`maximum` of `0.1`.

What it costs is arithmetic: `json.Number` is a string underneath and has
`Float64()`, `Int64()` and `String()` and no operators. That is the trade the
flag exists to let you make — the alternative is a dependency, and only the
caller knows which precision their sum wants. It is opt-in for the same reason:
turning it on changes the generated Go type.

Where it does not reach: a position the schema gives no type to. Those are held
as `any`, and `encoding/json` makes a `float64` of a JSON number on the way into
one whatever this flag says — a tuple element, an `any` field, a value judged
only by a runtime rule. `--exact-numbers` acts on the declared type, so a schema
that declares none gets what it always got.

### Property names are case-sensitive

JSON Schema property names are case-sensitive: `NAME` and `name` are two
different properties, and a schema declaring only `name` says nothing whatever
about `NAME`. `encoding/json` disagrees by default — a key matching no struct
tag exactly is matched a second time case-insensitively, through Unicode simple
folding rather than merely ASCII case, so U+212A KELVIN SIGN reaches a property
named `k`.

Generated code follows the schema. A key that differs from a declared property
only in case is an **additional property**: it goes to the overflow map (or is
refused, where the schema forbids extra keys), and the declared property is
absent unless the document wrote its exact name.

### Prose: `title` and `description`

Both become the doc comment above the declaration, and where a schema states
both they map onto Go's own shape for one: `title` is the summary line and
`description` the paragraph under it.

```go
// ResourceID - The identifier a resource is addressed by
//
// Assigned when the resource is created and stable for its lifetime.
type ResourceID string
```

A schema stating one of them gets that one as the whole comment. Every kind of
named type carries it — a struct, a struct field, an alias, an enum, an inferred
alias, a big-int alias, a type-only wrapper, a dynamic wrapper, a `not` wrapper
and a runtime-annotation wrapper — and so does every position a type is
synthesized for: a `$defs` entry, a property, an array element, a map value, a
`oneOf` variant.

`title` also **names** two of those, and where it does it is not written above
them as well — the identifier already says it. The two are the positions whose
name would otherwise be a placeholder: the document root, which has no enclosing
location to derive a name from (`Root`), and a `oneOf` variant, whose derived
name is the positional `ParentFieldOption0`. Everywhere else the location
already gives a name that means something — the `$defs` key, the property, the
element — and a title is documentation there rather than a name, so a schema
edit to a comment never renames an exported Go type. `--root-name` takes the
root's name back off the title, and the title is then written above the type
like any other prose.

### Annotations: `deprecated`, `readOnly`, `writeOnly`, `examples`

These four constrain nothing — from 2019-09 they are the meta-data vocabulary
and are annotations by definition, and in draft 7 they are described as hints to
a user agent. A code generator is the consumer they were written for, so they are
read at generation time and land in the generated source rather than in
`Validate()`, which never sees them.

By default all four become doc comments, on every kind of named type the
generator produces — a struct, a struct field, an alias, an enum, an inferred
alias, a big-int alias, a type-only wrapper, a dynamic wrapper, a `not` wrapper
and a runtime-annotation wrapper:

```go
// The identifier this resource used to carry.
//
// Examples from the schema:
//   "abc-123"
//   "def-456"
//
// Deprecated: the schema marks this deprecated.
LegacyID *string `json:"legacyID,omitempty"`
```

`deprecated` uses Go's own spelling — a paragraph beginning `Deprecated: ` — so
gopls, staticcheck and `go doc` report it like any other deprecation. Only
`"deprecated": true` emits it; a schema writing `false` says the property is not
deprecated, and nothing is emitted.

`--strict-read-write` adds behaviour to the other two, and makes the generated
type the **owning authority's** view of the resource, which is the only view in
which they have a direction ([2020-12 §9.4][ro]):

- `UnmarshalJSON` **rejects** a document that sets a `readOnly` property — the
  spec says such an attempt is "expected to be ignored or rejected by an owning
  authority", and this picks rejected.
- `MarshalJSON` **omits** every `writeOnly` property — the spec says the value
  "is never present when the instance is retrieved from the owning authority".

Both bind on a **property**, whichever way the schema says so: written on the
property, reached through its `$ref` (however long the chain), or stated in one
of its `allOf` branches all name the same instance location and all bind. That
holds wherever the property is, including the places the generated code keeps
the value as raw JSON and never decodes it into the type built for the
sub-schema — a `prefixItems` slot, a `contains` element, a `patternProperties`
value, and a schema whose whole shape is `unevaluatedProperties` or
`unevaluatedItems`. Those positions carry a path table rather than a key list,
because there is no Go field at them to key on.

A property that is both `required` and `readOnly` is refused twice under this
flag and satisfied by nothing: a document that sets it fails to decode
(`read-only property may not be set`), and one that omits it decodes and then
fails `Validate` (`required property is missing`). `SetDefaults` does not rescue
it — the required check reads the keys of the document as it arrived, not the
field — and a `default` on the property is inert for the same reason.
`schemagen generate` warns when it generates one. The runtime behaviour is
unchanged: both halves are doing what the schema and the flag say, and which of
the two to give up is the schema author's call.

Neither keyword ever changes a validation verdict, at any of those positions. It
is worth stating twice because three of them used to: the type the generator
builds for such a sub-schema is decoded into by a `Validate` check, so the
decoder's refusal came back out of `Validate()` as `read-only property may not
be set`. `readOnly` constrains no document, so that was a verdict about a
question the schema did not ask.

At a **conditional** branch — `anyOf`, `oneOf`, `if`/`then`/`else`,
`dependentSchemas`, `not` — the two keywords part company, and the asymmetry is
deliberate.

`readOnly` does not follow one. Which branch applies is the document's business,
and a refusal keyed on one would reject documents the schema accepts; a `not`
that *succeeds* is a subschema that *failed*, so nothing inside it marks anything
either. That holds however the branch is reached — including an object-level
conditional inside an `allOf` branch, whose properties are merged into the same
struct: the branch is where such a property gets its Go type, and what it
*asserts* is held back.

`writeOnly` does follow one, at every position and at any depth — including the
plainest spelling of all, where the conditional is written on the object whose own
properties carry the keyword:

```json
{"type": "object", "properties": {"t": {"type": "integer"}},
 "if": {"required": ["t"]},
 "then": {"properties": {"secret": {"type": "string", "writeOnly": true}}}}
```

`secret` is named by no schema that applies to every valid instance, so no Go
field is built for it and it arrives in the overflow map; it is deleted from the
output all the same. The two keywords fail in opposite directions. Over-stripping omits a field: the value is still in hand,
the omission is visible in the payload, and the flag can be turned off.
Under-stripping writes out a property whose whole meaning is "never present when
the instance is retrieved" — the shape a password, a token or a private key has —
and nothing anywhere reports that it happened. `--strict-read-write` is a policy
the caller chose rather than spec validation, so it is allowed to be stricter
than §7.7.1's annotation rules in the direction that fails safe. The cost is
named rather than hidden: a `writeOnly` inside a branch the document does not
match is stripped anyway, because the rules are a static table of locations and
cannot evaluate a condition. `Validate` is untouched by any of it — no verdict
has ever depended on either keyword and none does now.

That last part is not only about annotations. A property an `if`/`then`/`else`
consequence names and no other schema describes gets its Go type from the branch
— a merged `then` is where a materialized enum or item struct comes from at all
— but none of the branch's keywords become rules on the field, because a field's
rules apply to every document and the consequence applies only to the ones its
condition selects. The consequence is still enforced, in the one place that can
put the condition in front of it: the object-level `if`/`then`/`else` check, or
the runtime evaluator where that check cannot read the group whole. Where a group
reaches neither — one nested below a second `allOf`, or one carrying a keyword
the evaluator declines — the field goes on enforcing it, because withdrawing the
only check there is would be the worse bug. An `anyOf` or `oneOf` variant's
properties are merged the same way and are *not* narrowed at all: the static
reading of those decides which variant matched from its required keys and never
applies a variant's property schemas, so nothing yet stands in for the field.

The doc comment follows the same line, so the generated type does not document a
contract it does not enforce. `title` and `description` are read over that same
reach and are narrowed with the rest: prose that answers for the documents one
branch selects would document a different set of documents than the declaration
under it. It stops short in one place: a `$ref` written on the property. That
reference survives into the generated source as the field's own type, and the
referenced type carries the comment already, so the field does not repeat it. An
`allOf` has no such survivor — the merge flattens it away — so what its branches
say about the property is written on the field.

A **named type** reads the same reach, which is what makes the two spellings of
one schema document alike. `{"$defs":{"D":{"allOf":[{"description":"..."}]}}}`
declares a documented `type D`, where before it declared a bare one and lost the
prose outright — a property at least has a field above it to carry what its
`allOf` says, and a definition has nothing.

### `default`

`default` answers the same reach question, and it answers it in the parent
struct's `SetDefaults`, which is the only place a Go type has for it. So it is
read through a `$ref` chain and through `allOf`, including the `$ref` inside an
`allOf` branch, and it is *not* read from an `anyOf`, `oneOf` or `if`/`then`/
`else` branch — a value written on every document from a branch that applies to
some is not the schema's default. Where several unconditional schemas state one,
the nearest wins: the property's own, then its `$ref` chain, then its `allOf`
branches left to right.

A default reached through a `$ref` lands on a field whose Go type is the
referenced type, so the value is written as a conversion into it —
`_default := ResourceID("unset")`. Types a JSON scalar does not convert to (a
struct, a slice, a `time.Time` alias, a big-int wrapper) get no default, as
before.

How the property is typed makes no difference. A property whose `oneOf` becomes
a sealed-interface group is checked exactly as a plain field is.

Outside a property the two keywords stay documentation. A `readOnly` array
element or map value has no property name for the check to key on, and there is
no way to omit an element from an array without changing its length — so the
keyword is said in the doc comment on the element's own type and nowhere else.

It is opt-in for two reasons. A type built this way no longer round-trips, by
design. And it picks a side: one Go type cannot be both the request shape and the
response shape, and `MarshalJSON` is not told which it is being asked for, so a
*client* building a request with the same type would have its `writeOnly`
password dropped. The default declines to guess.

Under neither setting do these keywords change a validation verdict.

[ro]: https://json-schema.org/draft/2020-12/json-schema-validation#section-9.4

### Unresolvable References

A `$ref` is resolved two ways. By **absolute URI**, matched against the `$id` of
the documents given to the same run — so a document a `$ref` names by its `$id`
has to be one of the inputs, in every configuration. And by **relative path**,
read from that path next to the referring schema file. `--allow-remote-refs`
adds a third route, fetching `http(s)` refs over the network.

A `$ref` that no resolver can serve fails generation by default, naming the
refs it could not resolve. Previously such refs degraded silently: property and
items positions became `any`, ref-only definitions emitted references to types
that were never generated, and validation lost the referenced constraints — so
the output looked plausible while dropping type information. Pass
`--lenient-refs` to restore the degrading behaviour.

`--lenient-refs` does not degrade quietly. Every ref it could not serve gets a
`warning:` line on stderr, and the generated file carries a `NOT VALIDATED`
comment naming them: whatever those references said is not in the file, and the
positions that held them check nothing.

**The output may not compile.** `any` fits some positions and not others. A
property, a `$defs` entry, an `allOf` member, a tuple slot and `contains` all
take it, and the package builds. An array element, a map value and a `oneOf` or
`anyOf` variant each need a *name*, so the file spells the name the reference
would have produced — `{"xs":{"type":"array","items":{"$ref":"gone.json"}}}`
emits `[]GoneJSON` — and nothing declares it.

The two cases are told apart rather than lumped together. A ref that degraded
into a name says so, names the identifier, and says the package does not
compile; a ref that became `any` says that instead, and the generated file
repeats the split under a `DOES NOT COMPILE` heading. Supplying the referenced
document is the fix; dropping `--lenient-refs` moves the failure back to
generation time, where it can be read; and declaring the named type by hand in
the same package (`type GoneJSON any`) makes the package build without making it
check anything.

### Root Type Names

By default the root type is named after the schema `title`, falling back to
`Root`; a title that named the root is not repeated in the comment above it.
`--root-name` overrides it, and its key may name the document three ways — the
most specific match wins:

| Key | Matches |
|-----|---------|
| `file:<schema path>` | the path as given on the command line (or its absolute form) |
| `id:<document $id>` | the document's `$id` |
| `<schema base name>` | the file's base name (the original behaviour) |

A bare `--root-name Name` applies to a single input.

Base names are not unique across an input set — two directories can each hold a
`common.json` — so a base-name key names *every* input sharing it. That is
usually what you want when those documents land in different Go packages, since
same-named types in different packages are distinct. Use `id:` or `file:` when
they need different names, or when they share one package:

```bash
schemagen generate one/common.json two/common.json -o out \
  --schema-package 'https://example.com/one/common.json=example.com/m/one' \
  --schema-package 'https://example.com/two/common.json=example.com/m/two' \
  --root-name 'id:https://example.com/one/common.json=OneCommon' \
  --root-name 'id:https://example.com/two/common.json=TwoCommon'
```

Keys are split on the last `=`, so `$ids` and file names containing one work. A
key repeated with a different name is an error, and a key that matches no input
is reported as a warning.

### Several Schemas, One Package

`--shared-types` runs every input through one generator, so a type materialized
by an earlier schema is referenced by the rest instead of re-emitted, and
package-level helpers are emitted once. Each schema needs a distinct root type
name (`--root-name`, or `--root-name-from-filename`), and the mode currently
requires `--validation static`.

```bash
schemagen generate person.json address.json \
  -o out -p models --shared-types \
  --root-name person.json=Person --root-name address.json=Address
```

One generator means one pass over the inputs, so a document has to be listed
after every document it `$ref`s. Documents that reference each other in a circle
have no such order: that set cannot be generated this way and is refused, naming
the refs that form the cycle. Merge them into one document — a `$ref` cycle
*within* a document is supported — or generate each in its own run and package,
which gives each package its own copy of the other's types.

One package is also one name space for the definitions, and two documents can
each declare a `$defs` entry of the same name. When the definitions agree they
stay one Go type — that is what the mode is for, and how they are spelled (key
order, whitespace, a keyword the document's dialect does not define) makes no
difference. When they differ they cannot be: each is named after its own
document's root type instead, so `$defs/Thing` in a document rooted `Alpha`
becomes `AlphaThing` and the one in `Beta` becomes `BetaThing`, and a warning
names both documents. Every claim on the name is qualified, not only the later
one, so the generated names do not depend on the order the inputs were listed;
`--root-name` sets the prefix. A document's own root type keeps its name, and a
definition that collides with another document's root name is the one that
moves.

`--schema-package` shares a name space per package and answers the same
collision the same way, between the documents assigned to one package.

One document can also contest a name with itself, and that needs no second input
to see, so it is answered in every mode. `$defs/X` and `definitions/X` are two
schema locations and may hold different schemas; each is named after the keyword
that declared it, `DefsX` and `DefinitionsX`. A definition whose key derives the
document's own root type name gives way to it — the root type name is what
`--root-name` and the title choose, and every position inside the document is
named after it. And two keys that derive one Go name with nothing left to tell
them apart — `my-type` and `my_type`, the two spellings of a JSON Pointer escape,
a key with no letters in it at all — keep one type each, the second numbered
`MyType2`. Keys that hold the same definition still share one type. Each split is
reported, saying which key kept the name and what to rename to choose the names
yourself.

### Several Schemas, Several Packages

`--schema-package` assigns each document to a Go import path. A `$ref` that
crosses into a document owned by another package emits a qualified reference and
an import, instead of materializing a second copy of the type — so consumers of
either package see the same Go type for the same JSON shape.

```bash
schemagen generate common.json person.json \
  -o out \
  --schema-package 'https://example.com/common.json=example.com/m/common' \
  --schema-package 'https://example.com/person.json=example.com/m/person' \
  --root-name common.json=Common --root-name person.json=Person
```

Notes:

- Every input must declare `$id`; that is how documents are matched to packages.
- Generation order is derived from the `$refs` between documents, so inputs can
  be listed in any order. Documents that reference each other across packages
  cannot be ordered — Go has no import cycles — and are reported as an error.
- Each output directory holds exactly one package. By default a document is
  written to `<output-dir>/<last segment of its import path>/`; override with
  `--schema-output`.
- The mode currently requires `--validation static`, and `--package` does not
  apply (each package is named after its import path).

### Config File

Multi-document generation otherwise needs one repeatable flag per document per
concern, which stops being reviewable well before it stops being typeable.
`--config` moves that to a JSON file:

```json
{
  "outputDir": "./gen",
  "validation": "static",
  "documents": [
    {
      "path": "schemas/common.json",
      "id": "https://example.com/common.json",
      "package": "example.com/m/common",
      "output": "./gen/common/common.go",
      "rootName": "Common",
      "fieldNames": { "Common": { "postal_code": "PostalCode" } }
    },
    {
      "path": "schemas/person.json",
      "id": "https://example.com/person.json",
      "package": "example.com/m/person",
      "rootName": "Person"
    }
  ]
}
```

```bash
schemagen generate --config schemagen.json
```

- **Precedence** is *explicit flag > config > default*, per setting. A flag
  naming a document overrides that document's settings without discarding the
  rest of the file.
- **Inputs** come from the command line as usual; with none given, every entry
  that has a `path` becomes an input, in file order.
- **Selectors**: an entry needs an `id`, a `path`, or both. `package` and
  `output` are assigned per document `$id`, so entries setting either must
  declare `id`.
- **Unknown fields are rejected**, so a mistyped key fails the run instead of
  silently doing nothing. An entry matching no input is reported as a warning.
- The config is used only when `--config` names it: there is no auto-discovery,
  so a build never changes behaviour because of a stray file in the working
  directory.
- Every boolean flag above has a config key of the same name in camel case --
  `omitEmpty`, `strictProperties`, `strictReadWrite`, `bigInt`, `exactNumbers`,
  `formatAssertion`, `formatAnnotation`, `allowRemoteRefs`, `lenientRefs`,
  `sharedTypes`, `rootNameFromFilename`.
- `--field-map` keeps working and takes precedence over a document's
  `fieldNames`. The config form is preferred, since it keys by document rather
  than by file base name, which is not unique across an input set.

### Remote References

By default, `schemagen` only resolves `$ref` references to local files relative to the schema's directory. If your schema references external schemas via HTTP/HTTPS URLs (e.g., `"$ref": "https://example.com/common.json#/$defs/Address"`), you must opt in with `--allow-remote-refs`:

```bash
schemagen generate schema.json --allow-remote-refs
```

This enables the HTTP resolver, which fetches and caches remote schemas at generation time. Remote resolution is disabled by default for security and reproducibility reasons -- schemas should ideally be vendored locally.

> **Security note:** with `--allow-remote-refs`, `$ref` URLs from the input schema are fetched with no host allowlist. Running it on an untrusted schema is a server-side request forgery (SSRF) vector -- a `$ref` can point the fetch at internal services or cloud metadata endpoints. Only enable it for schemas you trust, and prefer vendoring remote schemas locally.
>
> Within that limit, remote fetches are bounded: responses are capped at 10 MiB, redirect chains at 5 hops with `https` → `http` downgrades refused, and a non-JSON `Content-Type` is rejected rather than parsed. Local (`file`) `$ref` resolution is confined to the schema's own directory subtree, with symlinks resolved before the check, so a link inside the subtree cannot read outside it.

### Draft Override

`schemagen` auto-detects the JSON Schema draft version from the `$schema` URI in your schema file. If your schema lacks a `$schema` field or you need to force a specific draft version, use `--draft`:

```bash
schemagen generate legacy.json --draft 4
schemagen generate modern.json --draft 2020-12
schemagen generate current.json --draft v1
```

This affects keyword interpretation (e.g., whether `$ref` overrides siblings, tuple array syntax, exclusive min/max semantics, and whether `format` asserts).

`v1` is the undated stable release that succeeds the dated drafts, dialect URI `https://json-schema.org/v1`. Its keyword set is 2020-12's without the vocabulary machinery, and it is not an alias for 2020-12: `format` asserts under v1 and annotates under 2020-12.

`--draft` forces the draft: it takes precedence over the `$schema` URI declared by the input document, so `--draft 2020-12` on a document that declares draft-07 interprets every keyword under 2020-12 rules. The one exception is an embedded or remote resource that establishes its own `$id` scope *and* declares its own `$schema` -- that resource keeps its declared dialect, so cross-draft `$ref` semantics are preserved.

### Validation Strategy

`schemagen` defaults to `--validation static`, which emits direct Go validation checks and preserves the historical behavior. Use `--validation hybrid` to annotate generated code with validation capability metadata recording which features may need runtime annotation tracking for full spec compliance -- `$dynamicRef`, `$recursiveRef`, `unevaluatedItems`, and `unevaluatedProperties`.

Both modes emit the same self-contained static checks. `hybrid` adds the metadata; it does not change how a value is validated, and there is no separate runtime validator to opt into.

`--validation runtime` is accepted but currently behaves identically to `hybrid`; it only records a different `Mode` string in the generated capability metadata. It is reserved for a future full-runtime validation path.

Generated files expose `SchemagenValidationCapability()` and `SchemagenValidationRuntimeFeatures()` so callers can detect when a schema uses features that may require runtime annotation tracking for full JSON Schema compliance.

#### Validation contract

`Validate()` is authoritative for values produced by `json.Unmarshal`: the generated `UnmarshalJSON` records which JSON keys were present, and that presence information drives the presence-dependent checks.

For **hand-constructed** values (built directly in Go rather than decoded from JSON), JSON key presence is unknown, so presence-dependent checks are skipped: required properties, optional-field constraints, object-level `oneOf`/`anyOf` branch matching, and `dependent*`/`unevaluated*` checks. Type-level and value-range constraints on fields that are set still apply. If you need full validation of a programmatically built value, marshal it to JSON and unmarshal it back before calling `Validate()`.

The same applies to a value **assigned after decoding**. The record of which keys arrived as `null` is about the document, so a field that carried a null and has since been assigned keeps that record: `MarshalJSON()` leaves the new value alone, but `Validate()` still passes over the keywords the null made vacuous. Round-trip through JSON if you need the assignment judged.

#### Schemas with no type of their own

A schema that constrains a value without giving it a Go type -- a root `anyOf`/`oneOf`/`allOf`, an `if`/`then`/`else`, a `not` -- is generated as a small struct wrapping the raw JSON, with `UnmarshalJSON`, `MarshalJSON`, `Raw()`, `String()` and a `Validate()` that checks the schema. It is not `any`: Go forbids methods on a type whose underlying type is an interface, so `type X any` could carry no `Validate()` at all and `json.Unmarshal` into it could never fail.

`type X any` is still what a schema that genuinely constrains nothing produces (`{}`, or a bare `title`). It is also what a schema schemagen cannot compile produces -- and when that happens the generated source says so, in a comment above the declaration naming the keywords being dropped, and `schemagen generate` prints a `warning:` line for it. Treat both as "this value is not validated": there is no `Validate()` to call and no decode that can fail.

### Field Name Overrides

By default, `schemagen` derives Go field names from JSON property names (e.g. `first_name` → `FirstName`).

A derived name is always **exported**, which capitalizing alone cannot guarantee: a script without case has no capital to reach, so a property named `日本語` capitalizes to `日本語`, which is a legal Go identifier and an unexported one — and `encoding/json` ignores an unexported field however good its tag is. Such a name takes a leading `X`, the same prefix a name that cannot start an identifier already takes (`1a` → `X1a`):

| property | Go field | |
|---|---|---|
| `日本語`, `한국어`, `العربية`, `ภาษาไทย` | `X日本語`, `X한국어`, `Xالعربية`, `Xภาษาไทย` | no upper case exists in the script |
| `привет`, `Ωμέγα`, `café` | `Привет`, `Ωμέγα`, `Café` | Cyrillic, Greek and Latin have case, so nothing is prefixed |

The JSON tag keeps the original property name either way, so this changes the Go API of a generated type and not the wire format. Two properties whose derived names collide (`日本語` beside `X日本語`) are numbered apart exactly as any other clash is. Use `--field-map` to pin individual properties to chosen Go field names:

```bash
schemagen generate --field-map names.json person.json address.json
```

The config is JSON, keyed by **schema file base name → Go type name → JSON property name → Go field name**:

```json
{
  "person.json": {
    "Person":  { "first_name": "GivenName" },
    "Address": { "zip": "PostalCode" }
  }
}
```

Notes:

- Only the listed properties are overridden; everything else uses the derived name.
- The JSON tag always keeps the original property name, so round-trip serialization is unaffected.
- Override values must be valid **exported** Go identifiers (struct fields must be exported to (un)marshal).
- Generation **fails with an actionable error** when an override would produce uncompilable code — i.e. when it collides with another field, with a generated method (`Validate`, `MarshalJSON`, `UnmarshalJSON`, `SetDefaults`), or with the synthesized `AdditionalProperties` or `PatternProperties` overflow fields.
- Config that never takes effect emits a `warning:` on stderr (but does not fail the run): a top-level key that doesn't name a generated schema file, or an individual entry that matched no property. These warnings are shown even if generation later fails.

Limitations:

- **Type keys must be the generated Go type name.** For top-level and `$defs` types these are stable and predictable. Types synthesized for nested inline objects (named `ParentType` + `FieldName`), `oneOf`/`anyOf` wrappers, and enums use internal naming that may change as a schema evolves — overriding their fields is possible but more fragile. Note that overriding a field that holds a nested object also renames the nested type, which in turn changes the key needed to reach *its* fields.
- **File keys match by base name.** Two input schemas with the same file name in different directories share one override section (they also already write to the same output file).

### Regular Expressions

JSON Schema `pattern`, `patternProperties`, and `propertyNames.pattern` use ECMA-262 regular expression semantics. Generated code uses `github.com/mgilbir/goecma262` for those checks. To avoid false validation failures from harmless identity escapes inside character classes, `schemagen` normalizes those escapes when emitting generated code. For example, `^[A-Za-z0-9_\-\.\:]+$` is emitted with `\-` and `\:` rewritten to hex escapes, preserving the intended literal `-` and `:` matches.

## How It Works

Input is JSON only. `.yaml`/`.yml` files are rejected (YAML is not yet supported); files with any other extension are parsed as JSON.

The generation pipeline has these stages:

1. **Load** -- Parse the JSON Schema file (`pkg/schema`)
2. **Normalize** -- Canonicalize the schema (resolve shorthand forms, infer types from structural keywords)
3. **Resolve scopes** -- Compute base URIs, document roots, and the resource/anchor graph so scoped `$id` and `$ref` resolution work (`pkg/schema`)
4. **Generate IR** -- Convert the normalized schema into an intermediate representation of Go types, resolving `$ref` targets (`pkg/generator`)
5. **Emit** -- Render the IR into formatted Go source code using templates (`pkg/emitter`)

Note that generation performs I/O: `$ref` targets are read from the local filesystem (confined to the schema's directory subtree), and, when `--allow-remote-refs` is set, fetched over the network.

## Development

```bash
# Run all tests
make test

# Run tests (skip external suite)
make test-short

# Update golden test fixtures
make golden

# Download and run against the official JSON Schema Test Suite.
# Takes ~25 min and needs free space under TMPDIR for the build cache it fills
# (it names the figure and refuses to start below it). Concurrent runs share
# one cache and the last one out deletes it, so the requirement is the same
# however many runs are going. SCHEMAGEN_KEEP_GOCACHE=1 keeps the cache for
# the next run, which is most of those 25 minutes back when the generator has
# not changed.
make test-external

# Fuzz the parse -> generate -> emit pipeline for panics
make fuzz FUZZTIME=5m

# Co-generate schemas with conforming instances, compile the bindings, and
# check that valid data round-trips and is accepted while single-keyword
# violations are rejected
make cogen COGEN_ITERS=400

# Format and vet
make lint
```

## License

See [LICENSE](LICENSE).
