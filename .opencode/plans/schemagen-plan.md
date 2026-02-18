# schemagen — JSON Schema to Go Code Generator

## 1. Project Goals

- Generate Go structs + marshal/unmarshal methods from JSON Schema (Draft-07 & 2020-12, extensible to future drafts)
- First-class `oneOf` support using the protobuf sealed-interface pattern
- Generated code is **stdlib-only** (`encoding/json`, no external deps)
- **Lossless round-trip**: `JSON → Go struct → JSON` preserves all data
- Usable as both a **CLI tool** and an **importable Go library**
- **Go templates** for code generation (easier to debug/iterate than programmatic AST emission)
- Comprehensive testing: golden files + compiled generated code + round-trip JSON tests

## 2. Project Structure

```
github.com/mgilbir/schemagen/
├── main.go                          # CLI entry point
├── go.mod
├── go.sum
├── Makefile                         # build, test, lint, generate targets
│
├── cmd/
│   └── schemagen/
│       └── root.go                  # cobra CLI setup (generate command, flags)
│
├── pkg/
│   ├── schema/                      # JSON Schema parsing & model
│   │   ├── schema.go                # Core schema model types
│   │   ├── draft07.go               # Draft-07 specific handling
│   │   ├── draft202012.go           # Draft 2020-12 specific handling
│   │   ├── loader.go                # File/HTTP loading with caching
│   │   ├── resolver.go              # $ref resolution
│   │   └── schema_test.go
│   │
│   ├── generator/                   # Schema → intermediate representation
│   │   ├── generator.go             # Main generator orchestration
│   │   ├── config.go                # Generation config (package name, output dir, etc.)
│   │   ├── types.go                 # IR types (StructDef, FieldDef, OneOfDef, EnumDef, etc.)
│   │   ├── resolve.go               # Type resolution & naming
│   │   ├── object.go                # Object/struct generation
│   │   ├── oneof.go                 # oneOf → sealed interface pattern
│   │   ├── allof.go                 # allOf handling (merge properties)
│   │   ├── anyof.go                 # anyOf handling
│   │   ├── enum.go                  # Enum generation
│   │   ├── array.go                 # Array/slice generation
│   │   ├── primitive.go             # Primitive type mapping
│   │   └── generator_test.go
│   │
│   ├── emitter/                     # IR → Go source code via templates
│   │   ├── emitter.go               # Template execution engine
│   │   ├── funcmap.go               # Template helper functions
│   │   ├── emitter_test.go
│   │   └── templates/
│   │       ├── file.go.tmpl         # Top-level file (package, imports)
│   │       ├── struct.go.tmpl       # Struct type declaration
│   │       ├── enum.go.tmpl         # Enum type + constants
│   │       ├── oneof.go.tmpl        # Interface + wrapper structs + marshal/unmarshal
│   │       ├── marshal.go.tmpl      # MarshalJSON method
│   │       ├── unmarshal.go.tmpl    # UnmarshalJSON method
│   │       ├── validation.go.tmpl   # Validation helpers
│   │       └── alias.go.tmpl        # Type aliases
│   │
│   └── validation/                  # Schema validation constraints
│       ├── numeric.go               # min/max/multipleOf
│       ├── string.go                # minLength/maxLength/pattern
│       ├── array.go                 # minItems/maxItems/uniqueItems
│       ├── object.go                # required/minProperties/maxProperties
│       └── validation_test.go
│
├── testdata/                        # Test schemas + expected output + JSON fixtures
│   ├── schemas/                     # Input JSON Schema files
│   │   ├── basic/
│   │   │   ├── simple_object.json
│   │   │   ├── nested_object.json
│   │   │   ├── primitive_types.json
│   │   │   └── ...
│   │   ├── refs/
│   │   │   ├── local_ref.json
│   │   │   ├── defs_ref.json
│   │   │   └── ...
│   │   ├── composition/
│   │   │   ├── allof_simple.json
│   │   │   ├── anyof_simple.json
│   │   │   ├── oneof_simple.json
│   │   │   ├── oneof_complex.json
│   │   │   ├── oneof_with_null.json
│   │   │   └── ...
│   │   ├── enum/
│   │   │   ├── string_enum.json
│   │   │   ├── mixed_enum.json
│   │   │   └── ...
│   │   ├── validation/
│   │   │   ├── numeric_bounds.json
│   │   │   ├── string_constraints.json
│   │   │   └── ...
│   │   └── formats/
│   │       ├── datetime.json
│   │       └── ...
│   │
│   ├── golden/                      # Expected generated Go code (golden files)
│   │   ├── basic/
│   │   ├── refs/
│   │   ├── composition/
│   │   ├── enum/
│   │   ├── validation/
│   │   └── formats/
│   │
│   └── fixtures/                    # JSON test data for round-trip testing
│       ├── basic/
│       ├── composition/
│       └── ...
│
└── tests/                           # Integration tests
    ├── golden_test.go               # Compare generated output to golden files
    ├── roundtrip_test.go            # Compile generated code, unmarshal+marshal JSON, verify lossless
    └── compile_test.go              # Verify generated code compiles
```

## 3. Core Architecture

### 3.1 Three-Phase Pipeline

```
JSON Schema files          Intermediate Representation         Go Source Code
     │                              │                                │
     ▼                              ▼                                ▼
┌─────────┐    parse &     ┌──────────────┐    render via    ┌─────────────┐
│  Schema  │───resolve────▶│   IR Types   │───templates─────▶│  .go files  │
│  Model   │    $refs      │ (StructDef,  │                  │  (gofmt'd)  │
│          │               │  OneOfDef,   │                  │             │
└─────────┘               │  EnumDef..)  │                  └─────────────┘
    pkg/schema/            └──────────────┘                   pkg/emitter/
                            pkg/generator/
```

**Phase 1 — Parse**: Load JSON Schema files, resolve `$ref` references, normalize across draft versions (e.g., `definitions` ↔ `$defs`, `items` array ↔ `prefixItems`). Output: a tree of `schema.Schema` objects.

**Phase 2 — Generate IR**: Walk the schema tree and produce an intermediate representation of Go types. This is where the interesting logic lives: deciding type names, handling composition (`allOf`/`anyOf`/`oneOf`), enums, nullable types, validation constraints. Output: a tree of `generator.TypeDef` objects.

**Phase 3 — Emit**: Feed the IR into Go templates to produce `.go` source code. Run `go/format.Source()` (gofmt) on the output. Output: formatted Go source files.

### 3.2 Schema Model (`pkg/schema/`)

The schema model closely mirrors JSON Schema structure:

```go
type Schema struct {
    ID          string             // $id / id
    Schema      string             // $schema (used for draft detection)
    Ref         string             // $ref

    // Type
    Type        TypeList           // can be string or []string

    // Composition
    AllOf       []*Schema
    AnyOf       []*Schema
    OneOf       []*Schema
    Not         *Schema

    // Object
    Properties           map[string]*Schema
    Required             []string
    AdditionalProperties *SchemaOrBool
    PatternProperties    map[string]*Schema
    MinProperties        *int
    MaxProperties        *int

    // Array
    Items                *SchemaOrSchemaArray  // Draft-07: items; 2020-12: items (single) + prefixItems (array)
    PrefixItems          []*Schema             // 2020-12
    AdditionalItems      *SchemaOrBool
    MinItems             *int
    MaxItems             *int
    UniqueItems          *bool
    Contains             *Schema

    // String
    MinLength  *int
    MaxLength  *int
    Pattern    *string
    Format     *string

    // Numeric
    Minimum          *float64
    Maximum          *float64
    ExclusiveMinimum *SchemaOrFloat  // Draft-07: bool; 2020-12: number
    ExclusiveMaximum *SchemaOrFloat
    MultipleOf       *float64

    // Enum/Const
    Enum    []interface{}
    Const   *interface{}
    Default *interface{}

    // Annotations
    Title       string
    Description string

    // Definitions
    Definitions map[string]*Schema  // Draft-07
    Defs        map[string]*Schema  // 2020-12 ($defs)

    // Conditional
    If   *Schema
    Then *Schema
    Else *Schema
}
```

### 3.3 Intermediate Representation (`pkg/generator/`)

```go
// TypeDef is the top-level IR node for a generated type
type TypeDef interface {
    TypeName() string
}

type StructDef struct {
    Name        string
    Description string
    Fields      []FieldDef
    OneOfs      []OneOfDef      // oneOf groups on this struct
    Validation  []ValidationRule
    NeedsMarshal   bool
    NeedsUnmarshal bool
}

type FieldDef struct {
    Name       string          // Go field name
    JSONName   string          // JSON property name
    Type       GoType          // resolved Go type
    OmitEmpty  bool
    Required   bool
    Validation []ValidationRule
}

type OneOfDef struct {
    InterfaceName string        // unexported: isTypeName_FieldName
    FieldName     string        // exported field name on parent struct
    Variants      []OneOfVariant
}

type OneOfVariant struct {
    WrapperName string          // TypeName_VariantName
    FieldName   string          // exported field inside wrapper
    Type        GoType
    Schema      *schema.Schema  // original schema for this variant
}

type EnumDef struct {
    Name       string
    BaseType   GoType           // usually string
    Values     []EnumValue
}

type EnumValue struct {
    Name  string               // Go constant name
    Value interface{}          // actual value
}

type AliasDef struct {
    Name       string
    Underlying GoType
}
```

### 3.4 oneOf: The Sealed Interface Pattern

For a schema like:
```json
{
  "type": "object",
  "properties": {
    "shape": {
      "oneOf": [
        { "$ref": "#/$defs/Circle" },
        { "$ref": "#/$defs/Rectangle" }
      ]
    }
  }
}
```

We generate:

```go
// Sealed interface — unexported method prevents external implementation
type isDrawing_Shape interface {
    isDrawing_Shape()
}

// Wrapper structs
type Drawing_Circle struct {
    Circle *Circle
}
func (*Drawing_Circle) isDrawing_Shape() {}

type Drawing_Rectangle struct {
    Rectangle *Rectangle
}
func (*Drawing_Rectangle) isDrawing_Shape() {}

// Parent struct
type Drawing struct {
    Shape isDrawing_Shape `json:"-"`  // excluded from default marshal
}

// Custom UnmarshalJSON tries each variant
func (d *Drawing) UnmarshalJSON(data []byte) error {
    // 1. Unmarshal into raw map to get "shape" field
    // 2. Try each variant for the "shape" value
    // 3. Exactly one must succeed (oneOf semantics)
    // 4. Assign the matching wrapper to d.Shape
}

// Custom MarshalJSON emits the active variant
func (d Drawing) MarshalJSON() ([]byte, error) {
    // 1. Marshal other fields normally
    // 2. For Shape, type-switch and marshal the inner value
    // 3. Merge into the output JSON object
}

// Typed getters (nil-safe)
func (d *Drawing) GetCircle() *Circle { ... }
func (d *Drawing) GetRectangle() *Rectangle { ... }
```

### 3.5 Template-Based Code Emission

Templates live in `pkg/emitter/templates/` as `.go.tmpl` files embedded via `go:embed`. Example `struct.go.tmpl`:

```gotemplate
{{- define "struct" -}}
{{- if .Description}}
// {{.Name}} {{.Description | wrapComment}}
{{- end}}
type {{.Name}} struct {
{{- range .Fields}}
    {{.Name}} {{.Type | goType}} `json:"{{.JSONName}}{{if .OmitEmpty}},omitempty{{end}}"`
{{- end}}
{{- range .OneOfs}}
    {{.FieldName}} {{.InterfaceName}}
{{- end}}
}
{{- end -}}
```

Template function map includes: `goType`, `wrapComment`, `camelCase`, `pascalCase`, `snakeCase`, `zeroValue`, `imports`, etc.

## 4. JSON Schema Feature Coverage (Initial Scope)

| Category | Features | Priority |
|----------|----------|----------|
| **Types** | object, array, string, number/integer, boolean, null | P0 |
| **Type composition** | type as string, type as array (nullable via `["type", "null"]`) | P0 |
| **References** | `$ref`, `$defs`/`definitions`, local + cross-file | P0 |
| **Object** | properties, required, additionalProperties (bool + schema) | P0 |
| **Array** | items (single schema), minItems, maxItems | P0 |
| **Composition** | allOf, anyOf, oneOf | P0 |
| **Enum** | string enums, integer enums, const | P0 |
| **Validation** | minimum/maximum, exclusiveMin/Max, multipleOf, minLength/maxLength, pattern, minItems/maxItems | P1 |
| **Formats** | date-time (→ `time.Time`), date, time, duration, ipv4/ipv6, uri, email | P1 |
| **Nullable** | `oneOf: [{type: "null"}, {$ref: "..."}]` → pointer type | P0 |
| **Annotations** | title, description → Go comments, default → struct field defaults | P1 |
| **Conditional** | if/then/else | P2 |
| **Array tuples** | prefixItems / items-as-array | P2 |
| **Advanced object** | patternProperties, dependencies, propertyNames | P2 |

## 5. Testing Strategy

### Level 1: Golden File Tests
```
Input: testdata/schemas/composition/oneof_simple.json
Expected: testdata/golden/composition/oneof_simple.go
Test: Generate code, compare byte-for-byte with golden file
Regenerate: UPDATE_GOLDEN=true go test ./tests/...
```

### Level 2: Compilation Tests
Verify that all golden files (and freshly generated code) compile successfully. Use Go workspaces or `go build` in a temporary directory.

### Level 3: Round-Trip Tests
```
For each test case:
  1. Load schema from testdata/schemas/
  2. Generate Go code
  3. Compile it (in a temp module)
  4. Load JSON fixture from testdata/fixtures/
  5. Unmarshal JSON → Go struct
  6. Marshal Go struct → JSON
  7. Compare: original JSON ≡ round-tripped JSON (semantic equality, not byte equality)
  8. Also verify: struct fields are populated correctly (spot checks)
```

For round-trip tests, we'll use `go test` with `-run` patterns and a test harness that compiles generated code into a plugin or uses `os/exec` to run it. An alternative (and likely better) approach: the golden files live in a Go package under `testdata/golden/` that is part of a Go workspace, so they're directly importable in tests.

### Level 4: Fuzz Testing (future)
Use `testing/F` to fuzz the unmarshaling of generated types.

## 6. CLI Design

```
schemagen generate [flags] <schema-file...>

Flags:
  -o, --output-dir string      Output directory (default ".")
  -p, --package string         Go package name (default: directory name)
      --tags string            Additional struct tags (comma-separated)
      --all-required           Treat all properties as required
      --omit-empty             Add omitempty to all optional fields (default true)
      --format                 Run gofmt on output (default true)
      --draft string           Force schema draft version (auto-detect by default)
  -v, --verbose                Verbose output
```

The library API:

```go
import "github.com/mgilbir/schemagen/pkg/generator"

cfg := generator.Config{
    PackageName: "models",
    OutputDir:   "./generated",
    OmitEmpty:   true,
}
g := generator.New(cfg)
err := g.GenerateFiles([]string{"schema.json"})
```

## 7. Key Dependencies (for the tool itself, NOT generated code)

| Dependency | Purpose |
|------------|---------|
| `github.com/spf13/cobra` | CLI framework |
| `golang.org/x/tools/imports` | goimports on generated code (auto-add imports) |
| `github.com/google/go-cmp` | Test comparisons |

Generated code has **zero** external dependencies — stdlib only.

## 8. Implementation Plan (Iterative Phases)

### Phase 1: Foundation (MVP)
1. Initialize Go module, Makefile, CI basics
2. Implement `pkg/schema/` — parse JSON Schema into model (Draft-07 + 2020-12)
3. Implement `pkg/generator/` — basic type resolution for primitive types, objects with properties, `$ref`
4. Implement `pkg/emitter/` — template engine with `struct.go.tmpl`, `file.go.tmpl`
5. Wire up CLI (`cmd/schemagen/`)
6. First test: simple object schema → generates compilable Go struct
7. Test: round-trip a simple JSON file

### Phase 2: Composition
1. `allOf` — merge properties from multiple schemas
2. `anyOf` — generate struct with all possible fields + unmarshal tries each
3. `oneOf` — sealed interface pattern with wrapper structs + marshal/unmarshal
4. Nullable types via `oneOf: [null, X]` → `*X`
5. Golden file tests for each composition pattern
6. Round-trip tests for each

### Phase 3: Enums & Arrays
1. String enums → typed constants
2. Integer enums
3. `const`
4. Array types with `items`
5. Nested objects / inline types
6. Tests for all

### Phase 4: Validation
1. Numeric validation (min/max/multipleOf)
2. String validation (minLength/maxLength/pattern)
3. Array validation (minItems/maxItems)
4. Object validation (required — already partially done, additionalProperties)
5. Generated validation in UnmarshalJSON

### Phase 5: Polish & Advanced
1. Format support (date-time, etc.)
2. Cross-file `$ref` resolution
3. Multiple output files (one per schema / one per type)
4. Better error messages (source location tracking)
5. `go:generate` integration docs
6. Conditional schemas (if/then/else) — stretch goal

## 9. Design Decisions & Rationale

| Decision | Rationale |
|----------|-----------|
| **Go templates** instead of programmatic emission | Easier to read, debug, and iterate. Template files are self-documenting. go-jsonschema's 34KB generator file with `//nolint:gocyclo` is a cautionary tale. |
| **Three-phase pipeline** (parse → IR → emit) | Clean separation of concerns. Allows testing each phase independently. IR is draft-agnostic. |
| **Sealed interface for oneOf** | Proven pattern from protobuf. Compile-time safety. Prevents invalid states. |
| **Stdlib-only generated code** | Minimizes friction for consumers. No dependency management burden on users of generated code. |
| **`go:embed` for templates** | Templates are compiled into the binary. No need to distribute template files separately. Works for both CLI and library usage. |
| **Draft-agnostic IR** | Parse phase normalizes draft differences. Generator and emitter don't care about which draft. Future drafts only need a new parser. |
| **Golden file tests** | Industry standard for code generators. Easy to review changes. `UPDATE_GOLDEN` pattern for regeneration. |
| **Round-trip tests** | The ultimate correctness check: generated code actually works with real JSON data. |

## 10. Open Questions / Risks

1. **oneOf try-each-variant ordering**: When two variants could both unmarshal successfully (e.g., overlapping schemas), the first match wins. This is a known limitation of the try-each approach. Mitigation: error if multiple variants match (strict oneOf semantics).

2. **Nested oneOf**: A oneOf variant that itself contains a oneOf. The sealed interface pattern nests naturally, but naming gets complex. We'll use `ParentType_FieldName` scoping.

3. **additionalProperties as map**: When `additionalProperties` is a schema, the struct needs both typed fields and a `map[string]T` for extras. This requires custom marshal/unmarshal. We'll handle this in Phase 4.

4. **Circular references**: Schemas can have circular `$ref`. We'll handle this with pointer types and a visited-set during resolution.

## 11. Research Notes

### go-jsonschema (github.com/omissis/go-jsonschema) Analysis

- Uses programmatic code emission (no templates), resulting in a 34KB core file with high cyclomatic complexity
- `oneOf` is parsed but **completely ignored** during code generation — falls back to `interface{}`
- `anyOf` uses a structural merge strategy (union of all properties into one struct) — not a discriminated union
- Testing uses golden file comparison + Go workspaces for compiling generated code
- Known issues with complex schemas (arrays with enums in anyOf, nullable oneOf, etc.)

### Protobuf oneOf Pattern

The protobuf Go codegen uses a sealed interface pattern for oneof:
- Unexported interface with unexported marker method: `isMessageName_FieldName`
- Wrapper structs: `MessageName_VariantName` with single exported field
- Nil-safe typed getters on parent message
- Type switch for variant inspection
- Runtime uses registered `OneofWrappers` for marshal/unmarshal dispatch
- The pattern is ergonomic: compile-time safe, impossible to set multiple variants, clean type switch usage
