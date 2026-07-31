# Plan: Type-Inferred Schema Wrappers & Integer Handling

## Branch: `feature/type-inferred-wrapper`

## Overview

Three changes, in execution order:
1. **B1**: Fix `1.0`-as-integer with `json.Number` in `UnmarshalJSON` (8 fixes)
2. **A**: `InferredAliasDef` wrapper struct for type-inferred schemas (186 fixes)
3. **B2**: `BigIntSupport` config option for arbitrary-precision integers (18 fixes)

Total: ~212 test case fixes across validation and round-trip suites.

---

## B1: Fix `1.0` as Integer (4 val + 4 RT = 8 fixes)

### Problem
Schema `{"type": "integer"}` generates `type Root int64`. Go's `json.Unmarshal` rejects `1.0` for `int64` targets, but JSON Schema considers `1.0` a valid integer (zero fractional part).

### Approach
Modify the `alias_unmarshal` template to use `json.Number` when the underlying type is `int64`. This always applies (not behind a config flag).

### File Changes

#### `pkg/generator/types.go`
Add method to `AliasDef`:
```go
// IsIntegerType returns true if the underlying type is int64 (from "integer" schema type).
func (d *AliasDef) IsIntegerType() bool {
    if pt, ok := d.Underlying.(*PrimitiveType); ok {
        return pt.Name == "int64"
    }
    return false
}
```

#### `pkg/emitter/templates/alias.go.tmpl`
Change the `alias_unmarshal` template. Currently:
```gotemplate
{{define "alias_unmarshal"}}
{{- $recv := receiverName .Name -}}
func ({{$recv}} *{{.Name}}) UnmarshalJSON(data []byte) error {
    if string(data) == "null" {
        return fmt.Errorf("null is not allowed for type {{.Name}}")
    }
    type Alias {{.Name}}
    return json.Unmarshal(data, (*Alias)({{$recv}}))
}
{{- end}}
```

Change to:
```gotemplate
{{define "alias_unmarshal"}}
{{- $recv := receiverName .Name -}}
func ({{$recv}} *{{.Name}}) UnmarshalJSON(data []byte) error {
    if string(data) == "null" {
        return fmt.Errorf("null is not allowed for type {{.Name}}")
    }
    {{- if .IsIntegerType}}
    // Use json.Number to accept 1.0 as a valid integer (zero fractional part).
    var n json.Number
    if err := json.Unmarshal(data, &n); err != nil {
        return err
    }
    if i, err := n.Int64(); err == nil {
        *{{$recv}} = {{.Name}}(i)
        return nil
    }
    // Try float with zero fractional part (e.g., 1.0)
    if f, err := n.Float64(); err == nil {
        if f == math.Trunc(f) && !math.IsInf(f, 0) && f >= -9.223372036854776e+18 && f <= 9.223372036854776e+18 {
            *{{$recv}} = {{.Name}}(int64(f))
            return nil
        }
    }
    return fmt.Errorf("value %s cannot be represented as int64", n.String())
    {{- else}}
    type Alias {{.Name}}
    return json.Unmarshal(data, (*Alias)({{$recv}}))
    {{- end}}
}
{{- end}}
```

#### `pkg/generator/generator.go` — `addRequiredImports`
In the `AliasDef` section, add:
```go
if ad.IsIntegerType() && ad.NeedsNullCheck && ad.CanHaveMethods() {
    needsMath = true // math.Trunc, math.IsInf
}
```

Also: integer aliases without `NeedsNullCheck` currently don't get `UnmarshalJSON`. We need to ensure ALL integer aliases get it. Either:
- Force `NeedsNullCheck = true` for all integer aliases, OR
- Add a separate condition: generate unmarshal when `IsIntegerType()` even without `NeedsNullCheck`

The template dispatch in `alias.go.tmpl` line 6-8 is:
```gotemplate
{{- if and .CanHaveMethods .NeedsNullCheck}}
{{template "alias_unmarshal" .}}
{{- end}}
```
Change to:
```gotemplate
{{- if and .CanHaveMethods (or .NeedsNullCheck .IsIntegerType)}}
{{template "alias_unmarshal" .}}
{{- end}}
```
And update `alias_unmarshal` to conditionally include the null check:
```gotemplate
func ({{$recv}} *{{.Name}}) UnmarshalJSON(data []byte) error {
    {{- if .NeedsNullCheck}}
    if string(data) == "null" {
        return fmt.Errorf("null is not allowed for type {{.Name}}")
    }
    {{- end}}
    {{- if .IsIntegerType}}
    // ... json.Number approach ...
    {{- else}}
    type Alias {{.Name}}
    return json.Unmarshal(data, (*Alias)({{$recv}}))
    {{- end}}
}
```

#### `tests/external_known_failures.go`
Remove 4 validation entries:
```
draft6/type/integer type matches integers/a float with zero fractional part is an integer
draft7/type/integer type matches integers/a float with zero fractional part is an integer
draft2019-09/type/integer type matches integers/a float with zero fractional part is an integer
draft2020-12/type/integer type matches integers/a float with zero fractional part is an integer
```
Remove 4 roundtrip entries (same keys, in `knownRoundTripFailures`).

Update counts: validation 216→212, roundtrip 124→120.

### Testing
- Run targeted: `TestExternalValidation/.*/type/integer_type_matches_integers`
- Run full: all 4 suites, all 6 drafts
- Verify no regressions in existing integer handling

### Commit message
```
Accept 1.0 as valid integer in UnmarshalJSON via json.Number, fixing 8 test cases
```

---

## A: InferredAliasDef Wrapper Struct (89 val + ~97 RT = ~186 fixes)

### Problem
Schemas with no explicit `type` like `{"minimum": 5}`, `{"maxLength": 2}`, `{"pattern": "^a*$"}`, `{"maxItems": 2}` infer a Go type from constraints. But JSON Schema says constraints only apply to matching types — non-matching types pass silently. Our generated `type Root float64` rejects strings via `json.Unmarshal`.

### Approach
New IR type `InferredAliasDef` that generates a wrapper struct with typed value + raw fallback + method accessors + Stringer.

### New IR Type: `InferredAliasDef`

```go
// InferredAliasDef represents a type where the Go type was inferred from
// constraint keywords (not explicitly declared). It generates a wrapper struct
// that accepts any JSON value but provides typed access for the expected type.
// Non-matching JSON types are silently accepted per JSON Schema semantics.
type InferredAliasDef struct {
    Name             string
    Description      string
    InferredGoType   GoType   // float64, string, or []any
    InferredJSONType string   // "number", "string", "array" — for accessor naming
    Validations      []ValidationRule
    AnyOfVariants    [][]ValidationRule
    OneOfVariants    [][]ValidationRule
    NeedsNullCheck   bool
}

func (d *InferredAliasDef) TypeName() string { return d.Name }
func (d *InferredAliasDef) typeDef()         {}

// AccessorName returns the Go method name for typed access (e.g., "Float64", "StringValue", "Slice").
func (d *InferredAliasDef) AccessorName() string {
    switch d.InferredJSONType {
    case "number":  return "Float64"
    case "string":  return "StringValue"
    case "array":   return "Slice"
    default:        return "Value"
    }
}

// TypeCheckName returns the Go method name for type checking (e.g., "IsNumber", "IsString", "IsArray").
func (d *InferredAliasDef) TypeCheckName() string {
    switch d.InferredJSONType {
    case "number":  return "IsNumber"
    case "string":  return "IsString"
    case "array":   return "IsArray"
    default:        return "IsTyped"
    }
}
```

### Generated Code Pattern

For `{"minimum": 5}` (inferred number → float64):
```go
type Root struct {
    _value float64
    _raw   json.RawMessage
    _isRaw bool
}

func (r *Root) UnmarshalJSON(data []byte) error {
    // Try typed unmarshal first.
    if err := json.Unmarshal(data, &r._value); err == nil {
        return nil
    }
    // Non-matching type — store raw bytes, accept silently per JSON Schema.
    r._raw = append(r._raw[:0], data...)
    r._isRaw = true
    return nil
}

func (r Root) MarshalJSON() ([]byte, error) {
    if r._isRaw {
        if len(r._raw) == 0 {
            return []byte("null"), nil
        }
        return r._raw, nil
    }
    return json.Marshal(r._value)
}

func (r Root) Float64() float64           { return r._value }
func (r Root) IsNumber() bool             { return !r._isRaw }
func (r Root) Raw() json.RawMessage       { if r._isRaw { return r._raw }; b, _ := json.Marshal(r._value); return b }
func (r Root) String() string {
    if r._isRaw {
        return string(r._raw)
    }
    return fmt.Sprintf("%v", r._value)
}

func (r Root) Validate() error {
    if r._isRaw {
        return nil // Constraints don't apply to non-matching types.
    }
    if float64(r._value) < 5 {
        return fmt.Errorf("value: %v is less than minimum 5", r._value)
    }
    return nil
}
```

For `{"maxLength": 5}` (inferred string):
```go
type Root struct {
    _value string
    _raw   json.RawMessage
    _isRaw bool
}
// Same pattern but: StringValue() string, IsString() bool
// Validate() checks utf8.RuneCountInString(_value) when !_isRaw
```

For `{"maxItems": 2}` (inferred array → []any):
```go
type Root struct {
    _value []any
    _raw   json.RawMessage
    _isRaw bool
}
// Same pattern but: Slice() []any, IsArray() bool
// Validate() checks len(_value) when !_isRaw
```

### File Changes

#### `pkg/generator/types.go`
- Add `InferredAliasDef` struct with methods as described above.

#### `pkg/generator/generator.go`

In `generateTypeDef` (~line 549-569), the primitive alias path:
```go
primaryType := primarySchemaType(s)
isInferred := false
if primaryType == "" {
    primaryType = inferTypeFromConstraints(s)
    isInferred = true
}
if primaryType != "" && primaryType != "object" && primaryType != "array" {
    if isInferred {
        // Generate InferredAliasDef wrapper struct
        goType := g.resolveType(s, name)
        rules := extractAliasValidationRules(s, goType)
        anyOfVariants := extractAnyOfVariantRules(s, goType)
        g.generated[name] = true
        g.output.TypeDefs = append(g.output.TypeDefs, &InferredAliasDef{
            Name:             name,
            Description:      s.Description,
            InferredGoType:   goType,
            InferredJSONType: primaryType,
            Validations:      rules,
            AnyOfVariants:    anyOfVariants,
            NeedsNullCheck:   !schemaAllowsNull(s),
        })
        return nil
    }
    // ... existing AliasDef path ...
}
```

Similarly for the array path (~line 572): when `primaryType == "array"` and `isInferred`, generate `InferredAliasDef` instead.

In `addRequiredImports`:
```go
if iad, ok := td.(*InferredAliasDef); ok {
    needsJSON = true  // always: UnmarshalJSON, MarshalJSON
    needsFmt = true   // always: String(), Validate() errors
    for _, v := range iad.Validations {
        // same rule-type checks as for AliasDef
        if v.RuleType == "minLength" || v.RuleType == "maxLength" { needsUTF8 = true }
        if v.RuleType == "pattern" { needsRegexp = true }
        if v.RuleType == "multipleOf" { needsMath = true }
    }
    // same for AnyOfVariants, OneOfVariants
}
```

In `resolveAliasMethodability`: `InferredAliasDef` is a struct, always has methods — no action needed (the function only processes `AliasDef`).

In `populateValidatableFields`: recognize `InferredAliasDef` as having `Validate()` method.
Search for where type names are checked against generated types to see if Validate() is detected. The key function is `canHaveMethodsResolved` — it walks AliasDefs. InferredAliasDef is a struct, so it always has methods. We need to make sure the existing checks work.

#### `pkg/emitter/emitter.go`
Add to `typeDefWrapper`:
```go
func (w typeDefWrapper) IsInferredAlias() bool {
    _, ok := w.td.(*generator.InferredAliasDef)
    return ok
}
func (w typeDefWrapper) AsInferredAlias() *generator.InferredAliasDef {
    return w.td.(*generator.InferredAliasDef)
}
```

Register the new template file in `New()`.

#### `pkg/emitter/templates/typedef.go.tmpl`
Add dispatch:
```gotemplate
{{- if $w.IsInferredAlias}}{{template "inferred_alias" $w.AsInferredAlias}}
```

#### `pkg/emitter/templates/inferred_alias.go.tmpl` (NEW FILE)
Contains templates:
- `inferred_alias` — struct definition + all methods
- `inferred_alias_unmarshal` — try typed, fallback to raw
- `inferred_alias_marshal` — raw if set, else typed
- `inferred_alias_validate` — skip if raw, else apply rules (reuse rule patterns from alias_validate)
- Accessor methods, String() method

The validation rules in `inferred_alias_validate` reference `{{$recv}}._value` instead of `{{$recv}}` directly. The rules are the same patterns as in `alias_validate` but with the `._value` suffix.

#### `tests/external_known_failures.go`
Remove 89 validation entries with reason "type-inferred schema: data type incompatible with inferred Go type".
Remove 2 validation entries with reason "type-inferred schema: no $schema to guide validation".
Remove ~97 roundtrip entries with the same reason pattern.
Update counts.

### Key edge cases

1. **Null handling**: If `NeedsNullCheck` is true, the unmarshal should reject `null` before trying typed unmarshal. If false, `null` should go to `_raw`.

2. **Pattern tests (30 entries)**: Each sends 5 non-string types (array, boolean, float, integer, object). All should unmarshal into `_raw` and validate as OK.

3. **`$defs` usage**: When an inferred-type schema is in `$defs` and used as a `$ref` target, `resolveType` returns a `NamedType` pointing to it. Properties that reference it via `$ref` would have the wrapper struct as their field type. This works because the wrapper has `UnmarshalJSON`/`MarshalJSON`.

4. **`resolveAliasMethodability`**: Currently walks `AliasDef` chains. `InferredAliasDef` is not an alias — it's a struct. It always has methods. The function should skip it or handle it as "always has methods".

5. **`canHaveMethodsResolved` (used by populateValidatableFields)**: This function checks if a type name resolves to an AliasDef with NoMethods. For InferredAliasDef, it should return true (has methods). Need to add handling.

### Testing
- Run targeted tests for each sub-category:
  - `TestExternalValidation/.*/minimum/minimum_validation/ignores_non-numbers`
  - `TestExternalValidation/.*/maxLength/maxLength_validation/ignores_non-strings`
  - `TestExternalValidation/.*/pattern/pattern_validation/ignores_.*`
  - `TestExternalValidation/.*/maxItems/maxItems_validation/ignores_non-arrays`
- Run full: all 4 suites, all 6 drafts
- Generate code for sample schemas and manually inspect output

### Commit message
```
Generate wrapper struct for type-inferred schemas, fixing ~186 test cases

Schemas without explicit "type" (e.g., {"minimum": 5}) now generate a
wrapper struct instead of a plain type alias. The wrapper accepts any
JSON value via UnmarshalJSON (typed unmarshal with raw fallback), applies
constraints only to matching types in Validate(), and preserves round-trip
fidelity via MarshalJSON. Method accessors (Float64/StringValue/Slice,
IsNumber/IsString/IsArray, Raw, String) provide typed access.
```

---

## B2: BigIntSupport Config Option (12 val + ~6 RT = ~18 fixes)

### Problem
Bignums like `12345678910111213141516171819202122232425262728293031` are valid integers per JSON Schema but overflow Go's `int64`.

### Approach
New `Config.BigIntSupport bool` field. When enabled, `{"type": "integer"}` generates a wrapper struct with `*big.Int` support.

### Generated Code (when BigIntSupport=true)

```go
type Root struct {
    _int64    int64
    _bigInt   *big.Int
    _isBigInt bool
}

func (r *Root) UnmarshalJSON(data []byte) error {
    if string(data) == "null" {
        return fmt.Errorf("null is not allowed for type Root")
    }
    var n json.Number
    if err := json.Unmarshal(data, &n); err != nil {
        return err
    }
    // Try int64 first
    if i, err := n.Int64(); err == nil {
        r._int64 = i
        return nil
    }
    // Try float with zero fractional part
    if f, err := n.Float64(); err == nil {
        if f == math.Trunc(f) && !math.IsInf(f, 0) && f >= -9.223372036854776e+18 && f <= 9.223372036854776e+18 {
            r._int64 = int64(f)
            return nil
        }
    }
    // Try big.Int
    bi := new(big.Int)
    s := n.String()
    // Handle float-format bignums (e.g., 1e100)
    if strings.ContainsAny(s, ".eE") {
        f := new(big.Float)
        if _, ok := f.SetString(s); ok {
            if f.IsInt() {
                f.Int(bi)
                r._bigInt = bi
                r._isBigInt = true
                return nil
            }
        }
        return fmt.Errorf("value %s is not an integer", s)
    }
    if _, ok := bi.SetString(s, 10); ok {
        r._bigInt = bi
        r._isBigInt = true
        return nil
    }
    return fmt.Errorf("value %s is not a valid integer", s)
}

func (r Root) MarshalJSON() ([]byte, error) {
    if r._isBigInt && r._bigInt != nil {
        return []byte(r._bigInt.String()), nil
    }
    return json.Marshal(r._int64)
}

func (r Root) Int64() int64     { return r._int64 }
func (r Root) BigInt() *big.Int {
    if r._isBigInt && r._bigInt != nil { return r._bigInt }
    return big.NewInt(r._int64)
}
func (r Root) IsBigInt() bool   { return r._isBigInt }
func (r Root) String() string   { return r.BigInt().String() }

func (r Root) Validate() error {
    // Validation rules (minimum, maximum, etc.) need big.Int arithmetic
    // when _isBigInt is true.
    return nil
}
```

### File Changes

#### `pkg/generator/generator.go`
- Add `BigIntSupport bool` to `Config` struct.
- New IR type: `BigIntAliasDef` (or extend AliasDef with a `BigIntMode` flag).
- In `generateTypeDef`, when config has `BigIntSupport` and schema type is "integer", generate the wrapper.

#### `pkg/emitter/templates/bigint_alias.go.tmpl` (NEW FILE)
- Template for the big.Int wrapper struct.

#### CLI / `cmd/` changes
- Add `--big-int` flag to the `generate` command.

#### Test infrastructure
- `tests/external_test.go`: Add test variant that uses `BigIntSupport = true`.
- `tests/external_known_failures.go`: Move 12 bignum entries to a separate map that is only checked when BigIntSupport is disabled.

### Commit message
```
Add BigIntSupport config for arbitrary-precision integers, fixing 18 test cases

When Config.BigIntSupport is true, schemas with "type": "integer" generate
a wrapper struct with int64 + *big.Int support instead of a plain int64
alias. Bignums that overflow int64 are preserved with full precision.
```

---

## Execution Order

1. **B1** (1.0-as-integer): smallest change, commit and verify
2. **A** (inferred type wrapper): biggest change, commit and verify
3. **B2** (big.Int): optional feature, commit and verify

All work on branch `feature/type-inferred-wrapper`.

## Risk Mitigation

- Each step is independently committable and testable
- Branch isolation prevents main breakage
- B1 is backward-compatible (only changes unmarshal behavior for a valid edge case)
- A changes generated API for a specific subset of schemas (no explicit type)
- B2 is opt-in via config flag
