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
| `--allow-remote-refs` | | `false` | Allow fetching remote `$ref` schemas over HTTP/HTTPS |
| `--draft` | | *(auto)* | Override JSON Schema draft version (values: `3`, `4`, `6`, `7`, `2019-09`, `2020-12`) |
| `--validation` | | `static` | Validation strategy: `static`, `hybrid`, or `runtime` |
| `--field-map` | | | Path to a JSON file pinning JSON properties to specific Go field names (see below) |
| `--verbose` | `-v` | `false` | Print progress information |

### Remote References

By default, `schemagen` only resolves `$ref` references to local files relative to the schema's directory. If your schema references external schemas via HTTP/HTTPS URLs (e.g., `"$ref": "https://example.com/common.json#/$defs/Address"`), you must opt in with `--allow-remote-refs`:

```bash
schemagen generate schema.json --allow-remote-refs
```

This enables the HTTP resolver, which fetches and caches remote schemas at generation time. Remote resolution is disabled by default for security and reproducibility reasons -- schemas should ideally be vendored locally.

### Draft Override

`schemagen` auto-detects the JSON Schema draft version from the `$schema` URI in your schema file. If your schema lacks a `$schema` field or you need to force a specific draft version, use `--draft`:

```bash
schemagen generate legacy.json --draft 4
schemagen generate modern.json --draft 2020-12
```

This affects keyword interpretation (e.g., whether `$ref` overrides siblings, tuple array syntax, exclusive min/max semantics).

### Validation Strategy

`schemagen` defaults to `--validation static`, which emits direct Go validation checks and preserves the historical behavior. Use `--validation hybrid` to annotate generated code with runtime validation capability metadata and enable shared runtime primitives for features that need annotation tracking, such as `$dynamicRef`, `$recursiveRef`, `unevaluatedItems`, and `unevaluatedProperties`.

Generated files expose `SchemagenValidationCapability()` and `SchemagenValidationRuntimeFeatures()` so callers can detect when a schema uses features that may require runtime annotation tracking for full JSON Schema compliance.

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
- If an override would collide with another field's name, generation fails with an error rather than silently renaming.
- Entries that match no generated property (e.g. a typo in a type or property name) emit a `warning:` on stderr but do not fail the run.

### Regular Expressions

JSON Schema `pattern`, `patternProperties`, and `propertyNames.pattern` use ECMA-262 regular expression semantics. Generated code uses `github.com/mgilbir/goecma262` for those checks. To avoid false validation failures from harmless identity escapes inside character classes, `schemagen` normalizes those escapes when emitting generated code. For example, `^[A-Za-z0-9_\-\.\:]+$` is emitted with `\-` and `\:` rewritten to hex escapes, preserving the intended literal `-` and `:` matches.

## How It Works

The generation pipeline has four stages:

1. **Load** -- Parse the JSON Schema file (`pkg/schema`)
2. **Normalize** -- Canonicalize the schema (resolve shorthand forms, infer types from structural keywords)
3. **Generate IR** -- Convert the normalized schema into an intermediate representation of Go types (`pkg/generator`)
4. **Emit** -- Render the IR into formatted Go source code using templates (`pkg/emitter`)

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

# Format and vet
make lint
```

## License

See [LICENSE](LICENSE).
