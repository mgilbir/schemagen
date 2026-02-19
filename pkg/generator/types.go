// Package generator converts parsed JSON Schema into an intermediate
// representation (IR) of Go types for code generation.
package generator

// GoType represents a Go type in the IR.
type GoType interface {
	GoTypeName() string // e.g. "string", "*Person", "[]Item", "map[string]any"
	IsPointer() bool
}

// PrimitiveType represents built-in Go types.
type PrimitiveType struct {
	Name string // "string", "int64", "float64", "bool", "any"
}

func (t *PrimitiveType) GoTypeName() string { return t.Name }
func (t *PrimitiveType) IsPointer() bool    { return false }

// NamedType references a generated type by name.
type NamedType struct {
	Name    string
	Pointer bool
}

func (t *NamedType) GoTypeName() string {
	if t.Pointer {
		return "*" + t.Name
	}
	return t.Name
}
func (t *NamedType) IsPointer() bool { return t.Pointer }

// ArrayType represents []T.
type ArrayType struct {
	ItemType GoType
}

func (t *ArrayType) GoTypeName() string { return "[]" + t.ItemType.GoTypeName() }
func (t *ArrayType) IsPointer() bool    { return false }

// MapType represents map[K]V.
type MapType struct {
	KeyType   GoType
	ValueType GoType
}

func (t *MapType) GoTypeName() string {
	return "map[" + t.KeyType.GoTypeName() + "]" + t.ValueType.GoTypeName()
}
func (t *MapType) IsPointer() bool { return false }

// PointerType wraps another type as *T.
type PointerType struct {
	Inner GoType
}

func (t *PointerType) GoTypeName() string { return "*" + t.Inner.GoTypeName() }
func (t *PointerType) IsPointer() bool    { return true }

// ---------- TypeDef hierarchy ----------

// TypeDef is the top-level IR node for a generated type.
type TypeDef interface {
	TypeName() string
	typeDef() // sealed
}

// StructDef represents a Go struct.
type StructDef struct {
	Name                 string
	Description          string
	Fields               []FieldDef
	OneOfs               []OneOfDef
	AdditionalProperties *AdditionalPropertiesDef
	PatternProperties    []PatternPropertyDef
	DependentSchemas     []DependentSchemaConstraint // dependent sub-schemas with additionalProperties:false
	Validations          []ValidationRule
	ValidatableFields    []ValidatableFieldDef // fields whose types have their own Validate() method
	RequiredJSON         []string              // JSON property names that must be present (for required validation)
	NeedsMarshal         bool
	NeedsUnmarshal       bool
	NeedsNullCheck       bool // true when the schema's type does not include "null" — reject null JSON data
	AcceptNonObject      bool // true when schema has no explicit "type":"object" — silently accept non-object JSON data
}

// DependentSchemaConstraint describes a dependentSchemas entry where the sub-schema
// has additionalProperties: false. When the trigger key is present in the JSON object,
// only the keys listed in AllowedKeys are valid.
type DependentSchemaConstraint struct {
	TriggerKey  string   // JSON property name that activates the constraint
	AllowedKeys []string // set of JSON property names allowed by the dependent sub-schema
}

// ValidatableFieldDef describes a struct field whose type has a Validate() method
// that should be called from the parent struct's Validate().
type ValidatableFieldDef struct {
	FieldName   string // Go field name (PascalCase)
	GoType      GoType // the Go type of the field (for zero-value comparison)
	IsPointer   bool   // true if the field is a pointer type (needs nil check)
	OmitEmpty   bool   // true if the field can be zero-value (optional, no validate on zero)
	ZeroLiteral string // Go zero value literal for the type (e.g., `""`, `0`, `false`)
}

// HasRequiredFields returns true if the struct has required field validation.
func (d *StructDef) HasRequiredFields() bool {
	return len(d.RequiredJSON) > 0
}

// HasPatternProperties returns true if the struct has pattern properties.
func (d *StructDef) HasPatternProperties() bool {
	return len(d.PatternProperties) > 0
}

// HasDependentSchemas returns true if the struct has dependent schema constraints.
func (d *StructDef) HasDependentSchemas() bool {
	return len(d.DependentSchemas) > 0
}

// PatternPropertyDef describes a patternProperties entry on a struct.
// Pattern-matched keys are stored in a single overflow map (json.RawMessage values)
// to preserve them through marshal/unmarshal round-trips. The patterns are used
// during unmarshal to distinguish pattern-matched keys from truly additional keys.
type PatternPropertyDef struct {
	Pattern string // regex pattern (e.g., "^v", "f.o")
}

// AdditionalPropertiesDef describes an additionalProperties field on a struct.
type AdditionalPropertiesDef struct {
	ValueType GoType // the type of the map values (e.g., PrimitiveType{Name: "string"} or PrimitiveType{Name: "any"})
	Forbidden bool   // true when additionalProperties: false (overflow map is still generated to capture unknown keys for validation)
}

// ValidationRule describes a validation constraint on a struct field.
type ValidationRule struct {
	FieldName string // Go field name (PascalCase)
	JSONName  string // JSON property name (original)
	RuleType  string // "minLength", "maxLength", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "pattern", "minItems", "maxItems", "uniqueItems", "required"
	Value     any    // the constraint value (int for lengths, float64 for min/max, string for pattern, bool for uniqueItems)
	IsPointer bool   // true if the field is a pointer type (needs nil check + dereference)
}

func (d *StructDef) TypeName() string { return d.Name }
func (d *StructDef) typeDef()         {}

// FieldDef represents a struct field.
type FieldDef struct {
	Name        string // Go field name (PascalCase)
	JSONName    string // JSON property name (original)
	Type        GoType // resolved Go type
	OmitEmpty   bool
	Required    bool
	Description string
	ManualJSON  bool // true if JSONName contains chars that break struct tags (control chars, quotes)
}

// OneOfDef represents a oneOf group on a struct.
type OneOfDef struct {
	InterfaceName string // unexported: isTypeName_FieldName
	FieldName     string // exported field name on parent struct
	JSONName      string // JSON property name
	Variants      []OneOfVariant
}

// OneOfVariant represents one variant of a oneOf.
type OneOfVariant struct {
	WrapperName    string   // TypeName_VariantName
	FieldName      string   // exported field inside wrapper
	Type           GoType   // the actual type of this variant
	RequiredFields []string // JSON field names that must be present for this variant to match
}

// EnumDef represents an enum type.
type EnumDef struct {
	Name        string
	BaseType    GoType
	Values      []EnumValue
	Description string
}

func (d *EnumDef) TypeName() string { return d.Name }
func (d *EnumDef) typeDef()         {}

// EnumValue represents one enum constant.
type EnumValue struct {
	Name  string // Go constant name
	Value any    // actual value (string or int)
}

// AliasDef represents a defined type (type Name Underlying).
// A Validate() method is always emitted. For types whose underlying
// is a pointer or interface (e.g., *T or any), Validate() cannot be
// attached — CanHaveMethods() returns false and the template skips it.
type AliasDef struct {
	Name              string
	Underlying        GoType
	Description       string
	Validations       []ValidationRule
	AnyOfVariants     [][]ValidationRule // each inner slice is one anyOf variant's rules; at least one must pass
	OneOfVariants     [][]ValidationRule // each inner slice is one oneOf variant's rules; exactly one must pass
	NoMethods         bool               // set by resolveAliasMethodability when underlying chain resolves to pointer/interface
	NeedsNullCheck    bool               // true when the schema's type does not include "null" — reject null JSON data
	AcceptNonMatching bool               // true when schema has no explicit type — silently accept non-matching JSON data
}

func (d *AliasDef) TypeName() string { return d.Name }
func (d *AliasDef) typeDef()         {}

// CanHaveMethods returns true if this defined type can have methods attached.
// The NoMethods flag is set by resolveAliasMethodability() after generation,
// which walks the full type chain to detect pointer or interface underlying types.
func (d *AliasDef) CanHaveMethods() bool {
	return !d.NoMethods
}

// ---------- File ----------

// File represents a generated Go source file.
type File struct {
	PackageName string
	TypeDefs    []TypeDef
	Imports     []Import
}

// Import represents a Go import.
type Import struct {
	Path  string
	Alias string
}
