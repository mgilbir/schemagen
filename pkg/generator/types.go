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
	Name                  string
	Description           string
	Fields                []FieldDef
	OneOfs                []OneOfDef
	AdditionalProperties  *AdditionalPropertiesDef
	PatternProperties     []PatternPropertyDef
	DependentSchemas      []DependentSchemaConstraint // dependent sub-schemas with additionalProperties:false
	Validations           []ValidationRule
	ValidatableFields     []ValidatableFieldDef     // fields whose types have their own Validate() method
	RequiredJSON          []string                  // JSON property names that must be present (for required validation)
	NonObjectValidations  []ValidationRule          // constraints that apply to non-object data (e.g., minimum on a schema that is both object and numeric)
	UnevaluatedProperties *UnevaluatedPropertiesDef // unevaluatedProperties constraint (Draft 2019-09+)
	OwnPropertyNames      []string                  // JSON names of properties declared directly on this schema (not merged from allOf/anyOf). When set, only these are "known" for additionalProperties routing.
	NeedsMarshal          bool
	NeedsUnmarshal        bool
	NeedsNullCheck        bool // true when the schema's type does not include "null" — reject null JSON data
	AcceptNonObject       bool // true when schema has no explicit "type":"object" — silently accept non-object JSON data
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

// HasOwnPropertyNames returns true if the struct tracks own (non-merged) property names
// for additionalProperties scope isolation (e.g., allOf merges). A non-nil but empty
// slice means "no own properties" — all properties came from allOf sub-schemas.
func (d *StructDef) HasOwnPropertyNames() bool {
	return d.OwnPropertyNames != nil
}

// HasPatternProperties returns true if the struct has pattern properties.
func (d *StructDef) HasPatternProperties() bool {
	return len(d.PatternProperties) > 0
}

// HasPatternPropertyValidation returns true if any pattern property has validation
// constraints (IsForbidden or Validations) that need to be checked in Validate().
func (d *StructDef) HasPatternPropertyValidation() bool {
	for _, pp := range d.PatternProperties {
		if pp.IsForbidden || len(pp.Validations) > 0 {
			return true
		}
	}
	return false
}

// HasDependentSchemas returns true if the struct has dependent schema constraints.
func (d *StructDef) HasDependentSchemas() bool {
	return len(d.DependentSchemas) > 0
}

// HasUnevaluatedProperties returns true if the struct has an unevaluatedProperties constraint.
func (d *StructDef) HasUnevaluatedProperties() bool {
	return d.UnevaluatedProperties != nil
}

// HasSchemaValuedUnevalProps returns true if the unevaluatedProperties constraint
// is a schema (not just true/false) with validation rules for each unevaluated value.
func (u *UnevaluatedPropertiesDef) HasSchemaValuedUnevalProps() bool {
	return u.ValueType != "" || len(u.Validations) > 0
}

// NeedsJSONKeys returns true if the struct needs _jsonKeys for optional field
// validation or dependent schema validation.
func (d *StructDef) NeedsJSONKeys() bool {
	if len(d.DependentSchemas) > 0 {
		return true
	}
	for _, v := range d.Validations {
		if v.Optional {
			return true
		}
	}
	return false
}

// PatternPropertyDef describes a patternProperties entry on a struct.
// Pattern-matched keys are stored in a single overflow map (json.RawMessage values)
// to preserve them through marshal/unmarshal round-trips. The patterns are used
// during unmarshal to distinguish pattern-matched keys from truly additional keys.
type PatternPropertyDef struct {
	Pattern     string           // regex pattern (e.g., "^v", "f.o")
	IsForbidden bool             // true when sub-schema is boolean false (matching keys rejected)
	Validations []ValidationRule // constraints on matched values (type, minimum, etc.)
}

// AdditionalPropertiesDef describes an additionalProperties field on a struct.
type AdditionalPropertiesDef struct {
	ValueType GoType // the type of the map values (e.g., PrimitiveType{Name: "string"} or PrimitiveType{Name: "any"})
	Forbidden bool   // true when additionalProperties: false (overflow map is still generated to capture unknown keys for validation)
}

// UnevaluatedPropertiesDef describes an unevaluatedProperties constraint on a struct.
// Properties are "evaluated" if they are covered by properties, patternProperties,
// additionalProperties, or unevaluatedProperties in nested applicator subschemas.
type UnevaluatedPropertiesDef struct {
	IsForbidden       bool             // true when unevaluatedProperties: false (reject any unevaluated property)
	IsAllowed         bool             // true when unevaluatedProperties: true (allow any unevaluated property — no-op)
	EvaluatedNames    []string         // statically known evaluated property names from allOf/$ref/properties
	EvaluatedPatterns []string         // regex patterns from patternProperties in allOf/$ref
	AllEvaluated      bool             // true when additionalProperties or nested unevaluatedProperties marks all as evaluated
	Validations       []ValidationRule // validation rules for schema-valued unevaluatedProperties (e.g., type/minLength constraints on each unevaluated value)
	ValueType         string           // JSON type required for unevaluated property values (e.g., "string", "number"); empty if no type constraint
}

// ValidationRule describes a validation constraint on a struct field.
type ValidationRule struct {
	FieldName string // Go field name (PascalCase)
	JSONName  string // JSON property name (original)
	RuleType  string // "minLength", "maxLength", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "pattern", "minItems", "maxItems", "uniqueItems", "required"
	Value     any    // the constraint value (int for lengths, float64 for min/max, string for pattern, bool for uniqueItems)
	IsPointer bool   // true if the field is a pointer type (needs nil check + dereference)
	Optional  bool   // true if the field is optional (not required) — validation is skipped when absent
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
	IsRaw       bool // true for heterogeneous enums → json.RawMessage-based instead of const-based
}

func (d *EnumDef) TypeName() string { return d.Name }
func (d *EnumDef) typeDef()         {}

// EnumValue represents one enum constant.
type EnumValue struct {
	Name    string // Go constant name
	Value   any    // actual value (string or int)
	RawJSON string // JSON-encoded form (only set when EnumDef.IsRaw is true)
}

// TupleItemDef describes one position in a tuple-form array (prefixItems/items-as-array).
// The generated Validate() method will re-unmarshal each element into the position's
// type and call its Validate() method.
type TupleItemDef struct {
	TypeName string // Go type name for this position (e.g., "Item", "SubItem")
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
	TupleItems        []TupleItemDef     // per-position type validation for tuple arrays (prefixItems / items-as-array)
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

// IsIntegerType returns true if the underlying type is int64 (from "integer" schema type).
// Used to generate json.Number-based UnmarshalJSON that accepts 1.0 as a valid integer.
func (d *AliasDef) IsIntegerType() bool {
	if pt, ok := d.Underlying.(*PrimitiveType); ok {
		return pt.Name == "int64"
	}
	return false
}

// HasTupleItems returns true if this alias has per-position tuple validation.
func (d *AliasDef) HasTupleItems() bool {
	return len(d.TupleItems) > 0
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
