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
| `--strict-properties` | | `false` | Treat absent `additionalProperties` as false (no overflow map) |
| `--big-int` | | `false` | Generate `*big.Int` wrapper for integer types |
| `--allow-remote-refs` | | `false` | Allow fetching remote `$ref` schemas over HTTP/HTTPS |
| `--draft` | | *(auto)* | Override JSON Schema draft version (values: `3`, `4`, `6`, `7`, `2019-09`, `2020-12`) |
| `--validation` | | `static` | Validation strategy: `static`, `hybrid`, or `runtime` |
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
