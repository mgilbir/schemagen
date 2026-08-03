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
- Default values for struct fields
- `additionalProperties` and `patternProperties` support with overflow maps
- Optional `*big.Int` wrapper for arbitrary-precision integers
- Supports JSON Schema drafts 3, 4, 6, 7, 2019-09, and 2020-12 (auto-detected or overridden via `--draft`)

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
| `--omit-empty` | | `true` | Add `omitempty` to optional JSON fields |
| `--strict-properties` | | `false` | Treat absent `additionalProperties` as false for validation while still preserving overflow properties for round-trip output |
| `--big-int` | | `false` | Generate `*big.Int` wrapper for integer types |
| `--format-assertion` | | `false` | Assert `format` on every draft. Without it the dialect decides (see below) |
| `--allow-remote-refs` | | `false` | Allow fetching remote `$ref` schemas over HTTP/HTTPS |
| `--draft` | | *(auto)* | Override JSON Schema draft version (values: `3`, `4`, `6`, `7`, `2019-09`, `2020-12`) |
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
| no `$schema` | **annotates** | An unknown dialect is read as a modern one everywhere else in the generator, and withholding a rejection is the safe direction. |

`--format-assertion` asserts on every draft, for callers who want the older
behaviour on a newer document.

The flag reaches the Go type as well as the check. `date-time` maps to
`time.Time` and `ipv4`/`ipv6` to `netip.Addr`, and those types assert the format
by decoding it — an unparseable value simply fails `json.Unmarshal`, whatever
`Validate` does. Under an annotating dialect the mapping is therefore withheld
too and the value stays a `string`; pass `--format-assertion` to get the typed
representation back. One consequence is worth stating: `time.Time` cannot
represent a leap second, so `1998-12-31T23:59:60Z` — which RFC 3339 admits — is
refused by a `date-time` field held as one. A `format` written without a `type`
keeps the JSON string and accepts it.

`hostname`, `idn-hostname`, `email` and `idn-email` are checked with
[`golang.org/x/net/idna`](https://pkg.go.dev/golang.org/x/net/idna), which is the
only dependency generated code takes beyond the ECMA-262 engine `pattern`
already needs. It is imported **only** into packages whose schemas name one of
those four formats; a package with, say, a `date-time` in it takes neither.

One gap remains there, and it is recorded in the generated helpers rather than
left implicit: IDNA2008's "exceptions that are DISALLOWED" table, which UTS-46
lookup processing maps rather than refuses. Three documents per hostname format
are accepted that the official suite rejects. That is under-enforcement; nothing
is rejected that the spec permits.

### Unresolvable References

A `$ref` that no resolver can serve fails generation by default, naming the
refs it could not resolve. Previously such refs degraded silently: property and
items positions became `any`, ref-only definitions emitted references to types
that were never generated, and validation lost the referenced constraints — so
the output looked plausible while dropping type information. Pass
`--lenient-refs` to restore the degrading behaviour.

### Root Type Names

By default the root type is named after the schema `title`, falling back to
`Root`. `--root-name` overrides it, and its key may name the document three
ways — the most specific match wins:

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
```

This affects keyword interpretation (e.g., whether `$ref` overrides siblings, tuple array syntax, exclusive min/max semantics).

`--draft` forces the draft: it takes precedence over the `$schema` URI declared by the input document, so `--draft 2020-12` on a document that declares draft-07 interprets every keyword under 2020-12 rules. The one exception is an embedded or remote resource that establishes its own `$id` scope *and* declares its own `$schema` -- that resource keeps its declared dialect, so cross-draft `$ref` semantics are preserved.

### Validation Strategy

`schemagen` defaults to `--validation static`, which emits direct Go validation checks and preserves the historical behavior. Use `--validation hybrid` to annotate generated code with validation capability metadata recording which features may need runtime annotation tracking for full spec compliance -- `$dynamicRef`, `$recursiveRef`, `unevaluatedItems`, and `unevaluatedProperties`.

Both modes emit the same self-contained static checks. `hybrid` adds the metadata; it does not change how a value is validated, and there is no separate runtime validator to opt into.

`--validation runtime` is accepted but currently behaves identically to `hybrid`; it only records a different `Mode` string in the generated capability metadata. It is reserved for a future full-runtime validation path.

Generated files expose `SchemagenValidationCapability()` and `SchemagenValidationRuntimeFeatures()` so callers can detect when a schema uses features that may require runtime annotation tracking for full JSON Schema compliance.

#### Validation contract

`Validate()` is authoritative for values produced by `json.Unmarshal`: the generated `UnmarshalJSON` records which JSON keys were present, and that presence information drives the presence-dependent checks.

For **hand-constructed** values (built directly in Go rather than decoded from JSON), JSON key presence is unknown, so presence-dependent checks are skipped: required properties, optional-field constraints, object-level `oneOf`/`anyOf` branch matching, and `dependent*`/`unevaluated*` checks. Type-level and value-range constraints on fields that are set still apply. If you need full validation of a programmatically built value, marshal it to JSON and unmarshal it back before calling `Validate()`.

#### Schemas with no type of their own

A schema that constrains a value without giving it a Go type -- a root `anyOf`/`oneOf`/`allOf`, an `if`/`then`/`else`, a `not` -- is generated as a small struct wrapping the raw JSON, with `UnmarshalJSON`, `MarshalJSON`, `Raw()`, `String()` and a `Validate()` that checks the schema. It is not `any`: Go forbids methods on a type whose underlying type is an interface, so `type X any` could carry no `Validate()` at all and `json.Unmarshal` into it could never fail.

`type X any` is still what a schema that genuinely constrains nothing produces (`{}`, or a bare `title`). It is also what a schema schemagen cannot compile produces -- and when that happens the generated source says so, in a comment above the declaration naming the keywords being dropped, and `schemagen generate` prints a `warning:` line for it. Treat both as "this value is not validated": there is no `Validate()` to call and no decode that can fail.

### Field Name Overrides

By default, `schemagen` derives Go field names from JSON property names (e.g. `first_name` → `FirstName`). When migrating an existing codebase to schema-generated types, you may need specific field names to stay compatible with code that already references them. Use `--field-map` to pin individual properties to chosen Go field names:

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

# Download and run against the official JSON Schema Test Suite
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
