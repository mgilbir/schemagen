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

// NamedType references a generated type by name, optionally qualified with
// the import alias of another generated package.
type NamedType struct {
	Name     string
	Pointer  bool
	PkgAlias string // import alias when the type lives in another generated package

	// Validation info carried over from the owning package for qualified
	// types (the local typedef lookup cannot see foreign definitions).
	foreignZeroLiteral string
	foreignValidatable bool
}

func (t *NamedType) GoTypeName() string {
	name := t.Name
	if t.PkgAlias != "" {
		name = t.PkgAlias + "." + name
	}
	if t.Pointer {
		return "*" + name
	}
	return name
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
	DependentRequired     []DependentRequiredDef      // dependentRequired constraints
	PropertyNames         *PropertyNamesDef           // propertyNames constraint (Draft 6+)
	Validations           []ValidationRule
	ValidatableFields     []ValidatableFieldDef     // fields whose types have their own Validate() method
	ItemValidations       []ItemValidationDef       // per-element checks for slice fields whose element type has no Validate()
	RequiredJSON          []string                  // JSON property names that must be present (for required validation)
	NonObjectValidations  []ValidationRule          // constraints that apply to non-object data (e.g., minimum on a schema that is both object and numeric)
	UnevaluatedProperties *UnevaluatedPropertiesDef // unevaluatedProperties constraint (Draft 2019-09+)
	CousinUnevalChecks    []CousinUnevalCheck       // unevaluatedProperties checks from allOf/anyOf sub-schemas (cousin isolation)
	ObjectOneOfs          []ObjectOneOfDef          // object-level oneOf branch validation for flattened applicator schemas
	ObjectAnyOfs          []ObjectAnyOfDef          // object-level anyOf branch validation for flattened applicator schemas (>=1 branch must match)
	OwnPropertyNames      []string                  // JSON names of properties declared directly on this schema (not merged from allOf/anyOf). When set, only these are "known" for additionalProperties routing.
	NeedsMarshal          bool
	NeedsUnmarshal        bool
	NeedsNullCheck        bool // true when the schema's type does not include "null" — reject null JSON data
	AcceptNonObject       bool // true when schema has no explicit "type":"object" — silently accept non-object JSON data
}

// DependentRequiredDef describes a dependentRequired constraint: when the
// trigger property is present, the listed dependent properties must also be present.
type DependentRequiredDef struct {
	TriggerKey string   // JSON property name that activates the constraint
	Required   []string // JSON property names that must be present when trigger is present
}

// PropertyNamesDef describes a propertyNames constraint on a struct.
// All property names in the JSON object must satisfy these string validation rules.
type PropertyNamesDef struct {
	IsForbidden bool     // true when propertyNames: false (any property is invalid)
	MaxLength   *int     // maximum length of property names
	MinLength   *int     // minimum length of property names
	Pattern     string   // regex pattern property names must match
	Enum        []string // allowed property name values (from enum or const)
}

// DependentSchemaConstraint describes a dependentSchemas entry. When the trigger key
// is present in the JSON object, the sub-schema's constraints are applied.
type DependentSchemaConstraint struct {
	TriggerKey    string                  // JSON property name that activates the constraint
	IsFalse       bool                    // boolean false schema — always reject when trigger is present
	AllowedKeys   []string                // set of JSON property names allowed (additionalProperties: false)
	RequiredProps []string                // required properties from the sub-schema
	MinProperties *int                    // minProperties from the sub-schema
	MaxProperties *int                    // maxProperties from the sub-schema
	PropertyTypes []DependentPropertyType // per-property type constraints from the sub-schema
}

// DependentPropertyType describes a JSON type constraint on a specific property
// within a dependentSchemas sub-schema.
type DependentPropertyType struct {
	PropName string // JSON property name
	JSONType string // required JSON type (e.g., "integer", "string")
}

// ValidatableFieldDef describes a struct field whose type has a Validate() method
// that should be called from the parent struct's Validate().
type ValidatableFieldDef struct {
	FieldName string // Go field name (PascalCase)
	JSONName  string // JSON property name (for error path context)
	GoType    GoType // the Go type of the field (for zero-value comparison)
	IsPointer bool   // true if the field is a pointer type (needs nil check)
	IsSlice   bool   // true if the field is a slice of validatable elements (needs iteration)
	IsMap     bool   // true if the field is a map of validatable values (needs iteration, keyed error path)

	// ElemIsPointer is set when a slice's elements or a map's values are
	// pointers. encoding/json turns a JSON null into a nil entry without
	// consulting the element type's UnmarshalJSON, so the emitted loop meets a
	// nil it must not call a value-receiver Validate through.
	ElemIsPointer bool
	// ElemRejectsNull is set when the element type's own schema does not admit
	// null, which makes that nil entry a validation failure rather than
	// something to pass over.
	ElemRejectsNull bool

	OmitEmpty   bool   // true if the field can be zero-value (optional, no validate on zero)
	ZeroLiteral string // Go zero value literal for the type (e.g., `""`, `0`, `false`)
}

// HasRequiredFields returns true if the struct has required field validation.
func (d *StructDef) HasRequiredFields() bool {
	return len(d.RequiredJSON) > 0
}

// HasDefaults returns true if any field has a default value.
func (d *StructDef) HasDefaults() bool {
	for _, f := range d.Fields {
		if f.DefaultLiteral != "" {
			return true
		}
	}
	return false
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

// HasDependentRequired returns true if the struct has dependentRequired constraints.
func (d *StructDef) HasDependentRequired() bool {
	return len(d.DependentRequired) > 0
}

// HasPropertyNames returns true if the struct has a propertyNames constraint.
func (d *StructDef) HasPropertyNames() bool {
	return d.PropertyNames != nil
}

// HasUnevaluatedProperties returns true if the struct has an unevaluatedProperties constraint.
func (d *StructDef) HasUnevaluatedProperties() bool {
	return d.UnevaluatedProperties != nil
}

// HasCousinUnevalChecks returns true if the struct has cousin isolation checks.
func (d *StructDef) HasCousinUnevalChecks() bool {
	return len(d.CousinUnevalChecks) > 0
}

// HasSchemaValuedUnevalProps returns true if the unevaluatedProperties constraint
// is a schema (not just true/false) with validation rules for each unevaluated value.
func (u *UnevaluatedPropertiesDef) HasSchemaValuedUnevalProps() bool {
	return u.ValueType != "" || len(u.Validations) > 0
}

// NeedsRawProps returns true if the struct needs _jsonRawProps for runtime
// conditional evaluation that involves const checks (if/then/else, anyOf, oneOf).
func (d *StructDef) NeedsRawProps() bool {
	if len(d.ObjectOneOfs) > 0 || len(d.ObjectAnyOfs) > 0 {
		return true
	}
	if d.UnevaluatedProperties == nil {
		return false
	}
	for _, ce := range d.UnevaluatedProperties.ConditionalEvals {
		switch ce.Kind {
		case "ifThenElse":
			if ce.IfBranch != nil && len(ce.IfBranch.ConstChecks) > 0 {
				return true
			}
		case "anyOf", "oneOf":
			for _, b := range ce.Branches {
				if len(b.ConstChecks) > 0 {
					return true
				}
			}
		}
	}
	return false
}

// NeedsJSONKeys returns true if the struct needs _jsonKeys for optional field
// validation, dependent schema/required validation, propertyNames validation,
// or unevaluatedProperties with conditional evaluation or cousin isolation.
func (d *StructDef) NeedsJSONKeys() bool {
	if d.HasRequiredFields() {
		// Required-property presence is checked in Validate() via _jsonKeys so the
		// error is path-qualified by the parent and reported as a validation error
		// rather than a parse error.
		return true
	}
	if len(d.ObjectOneOfs) > 0 || len(d.ObjectAnyOfs) > 0 {
		return true
	}
	if len(d.DependentSchemas) > 0 {
		return true
	}
	if len(d.DependentRequired) > 0 {
		return true
	}
	if d.PropertyNames != nil {
		return true
	}
	if len(d.CousinUnevalChecks) > 0 {
		return true
	}
	if d.UnevaluatedProperties != nil && d.UnevaluatedProperties.HasConditionalEvals() {
		return true
	}
	for _, v := range d.Validations {
		if v.Optional {
			return true
		}
		// minProperties/maxProperties count present JSON keys, which are tracked
		// in _jsonKeys. Without it the count would be a compile-time constant
		// (number of declared fields) rather than the number of present ones.
		if v.RuleType == "minProperties" || v.RuleType == "maxProperties" {
			return true
		}
	}
	return false
}

// HasPropertyCountValidation reports whether the struct carries a
// minProperties or maxProperties constraint that must count present JSON keys.
func (d *StructDef) HasPropertyCountValidation() bool {
	for _, v := range d.Validations {
		if v.RuleType == "minProperties" || v.RuleType == "maxProperties" {
			return true
		}
	}
	return false
}

// ObjectOneOfDef describes one object-level oneOf group whose variants should be
// checked against raw JSON properties after a schema has been flattened.
type ObjectOneOfDef struct {
	Branches []ObjectOneOfBranch
}

// ObjectAnyOfDef describes one object-level anyOf group. It shares the branch
// shape with ObjectOneOfDef but requires at least one branch to match rather
// than exactly one.
type ObjectAnyOfDef struct {
	Branches []ObjectOneOfBranch
}

// ObjectOneOfBranch describes one variant in an object-level oneOf group.
type ObjectOneOfBranch struct {
	RequiredKeys []string
	Checks       []ObjectPropertyCheck
}

// ObjectPropertyCheck describes a JSON property constraint used to match an
// object-level oneOf branch. Checks only apply when the property is present.
type ObjectPropertyCheck struct {
	JSONName      string
	JSONType      string
	AllowedValues []string // JSON-encoded enum/const values
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
	IsForbidden       bool              // true when unevaluatedProperties: false (reject any unevaluated property)
	IsAllowed         bool              // true when unevaluatedProperties: true (allow any unevaluated property — no-op)
	EvaluatedNames    []string          // statically known evaluated property names from allOf/$ref/properties (always-true sources)
	EvaluatedPatterns []string          // regex patterns from patternProperties in allOf/$ref (always-true sources)
	AllEvaluated      bool              // true when additionalProperties or nested unevaluatedProperties marks all as evaluated
	Validations       []ValidationRule  // validation rules for schema-valued unevaluatedProperties (e.g., type/minLength constraints on each unevaluated value)
	ValueType         string            // JSON type required for unevaluated property values (e.g., "string", "number"); empty if no type constraint
	ConditionalEvals  []ConditionalEval // runtime-conditional evaluation branches (if/then/else, dependentSchemas, anyOf, oneOf)
}

// HasConditionalEvals returns true if there are conditional evaluation branches.
func (u *UnevaluatedPropertiesDef) HasConditionalEvals() bool {
	return len(u.ConditionalEvals) > 0
}

// ConditionalEval describes a set of properties that are conditionally evaluated
// based on a runtime condition. At validation time, the condition is checked and
// matching properties are added to the "evaluated" set dynamically.
type ConditionalEval struct {
	Kind string // "dependentSchema", "ifThenElse", "anyOf", "oneOf"
	// dependentSchema: properties evaluated only when TriggerKey is present
	TriggerKey string         // JSON property name that triggers the branch
	Branch     *EvalBranchDef // properties evaluated when trigger is present
	// ifThenElse: ThenBranch evaluated when if matches, ElseBranch when it doesn't
	IfBranch   *IfConditionDef // describes how to evaluate the if condition
	ThenBranch *EvalBranchDef  // properties evaluated when if matches
	ElseBranch *EvalBranchDef  // properties evaluated when if doesn't match
	// anyOf/oneOf: each branch's properties are evaluated only if that branch matches
	Branches []EvalBranchDef // per-branch property info for anyOf/oneOf
}

// EvalBranchDef describes a set of properties evaluated by a schema branch.
type EvalBranchDef struct {
	Names        []string // property names evaluated by this branch
	Patterns     []string // regex patterns evaluated by this branch
	AllEvaluated bool     // if true, this branch evaluates ALL remaining properties
	// For branch matching in anyOf/oneOf:
	RequiredKeys []string     // keys that must be present for this branch to match
	ConstChecks  []ConstCheck // property const value checks
}

// HasNames returns true if this branch has any evaluated property names.
func (b *EvalBranchDef) HasNames() bool { return len(b.Names) > 0 }

// HasPatterns returns true if this branch has any evaluated pattern properties.
func (b *EvalBranchDef) HasPatterns() bool { return len(b.Patterns) > 0 }

// IfConditionDef describes a simple if-schema condition that can be evaluated
// at runtime by checking property values in the JSON object.
type IfConditionDef struct {
	ConstChecks  []ConstCheck // property const value checks
	RequiredKeys []string     // keys that must be present
}

// ConstCheck describes a property const value check (property must equal a specific JSON value).
type ConstCheck struct {
	PropertyName string // JSON property name
	GoFieldName  string // Go field name for struct access
	JSONValue    string // expected JSON-encoded value (e.g., `"bar"`, `42`)
}

// CousinUnevalCheck describes an unevaluatedProperties check from an allOf/anyOf
// sub-schema ("cousin"). Per JSON Schema spec, unevaluatedProperties inside an
// applicator branch can only see annotations from its own branch, not siblings.
type CousinUnevalCheck struct {
	IsForbidden    bool     // true when the cousin's unevaluatedProperties: false
	EvaluatedNames []string // property names evaluated in the cousin's own scope
	EvalPatterns   []string // regex patterns evaluated in the cousin's own scope
	AllEvaluated   bool     // true when the cousin's branch has additionalProperties
}

// ValidationRule describes a validation constraint on a struct field.
type ValidationRule struct {
	FieldName string // Go field name (PascalCase)
	JSONName  string // JSON property name (original)
	RuleType  string // "minLength", "maxLength", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "pattern", "minItems", "maxItems", "uniqueItems", "required"
	Value     any    // the constraint value (int for lengths, float64 for min/max, string for pattern, bool for uniqueItems)
	IsPointer bool   // true if the field is a pointer type (needs nil check + dereference)
	Optional  bool   // true if the field is optional (not required) — validation is skipped when absent

	// StringConvert is set on string-valued rules (minLength, maxLength,
	// pattern, format) whose field is typed as a named string rather than
	// string itself — a property that $refs a "type": "string" definition, for
	// instance. Those rules hand the field to functions declared to take a
	// string (ecma262.MatchString, url.Parse, utf8.RuneCountInString), and Go
	// does not convert a named type implicitly, so the emitter wraps the value
	// in an explicit conversion.
	StringConvert bool
}

func (d *StructDef) TypeName() string { return d.Name }
func (d *StructDef) typeDef()         {}

// FieldDef represents a struct field.
type FieldDef struct {
	Name           string // Go field name (PascalCase)
	JSONName       string // JSON property name (original)
	Type           GoType // resolved Go type
	OmitEmpty      bool
	OmitZero       bool // use ",omitzero" instead of ",omitempty" (optional slice/map fields, to preserve a present-but-empty collection while still omitting an absent one)
	Required       bool
	Description    string
	ManualJSON     bool   // true if JSONName contains chars that break struct tags (control chars, quotes)
	DefaultLiteral string // Go literal for the default value (empty string means no default)
}

// OneOfDef represents a oneOf group on a struct.
type OneOfDef struct {
	InterfaceName      string // unexported: isTypeName_FieldName
	FieldName          string // exported field name on parent struct
	JSONName           string // JSON property name
	Variants           []OneOfVariant
	DiscriminatorField string         // JSON property name used as discriminator (empty = use required-fields heuristic)
	DiscriminatorMap   map[string]int // maps discriminator value → variant index (when DiscriminatorField is set)
	Required           bool           // true when this oneOf's JSONName is in the parent schema's required array
}

// HasDiscriminator returns true if this oneOf uses discriminator-based dispatch.
func (d *OneOfDef) HasDiscriminator() bool {
	return d.DiscriminatorField != ""
}

// HasVariantChecks reports whether any variant carries branch constraints that
// selection has to test. Templates use it to emit the extra bookkeeping only
// where it is needed, leaving a union without such constraints unchanged.
func (d *OneOfDef) HasVariantChecks() bool {
	for _, v := range d.Variants {
		if len(v.Checks) > 0 {
			return true
		}
	}
	return false
}

// OneOfVariant represents one variant of a oneOf.
type OneOfVariant struct {
	WrapperName        string   // TypeName_VariantName
	FieldName          string   // exported field inside wrapper
	Type               GoType   // the actual type of this variant
	RequiredFields     []string // JSON field names that must be present for this variant to match
	DiscriminatorValue string   // first discriminator value selecting this variant (empty if no discriminator)
	// DiscriminatorValues holds every discriminator value selecting this
	// variant. A variant that is itself a oneOf can be selected by several
	// values (the union of its sub-variants' values), and a variant may
	// constrain the discriminator with a multi-value enum.
	DiscriminatorValues []string
	// Checks are the branch's own constraints, expressed over the decoded
	// candidate, that variant selection must satisfy before this variant counts
	// as matched. See oneOfVariantChecks.
	Checks []ValidationRule
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
// type and call its Validate() method, or check JSON type for simple schemas.
type TupleItemDef struct {
	TypeName string // Go type name for this position (e.g., "Item", "SubItem")
	JSONType string // simple JSON type constraint (e.g., "integer", "string", "number", "boolean", "null", "array", "object")
	IsFalse  bool   // boolean false schema — reject any value at this position
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
	AnyOfVariants     [][]ValidationRule  // each inner slice is one anyOf variant's rules; at least one must pass
	OneOfVariants     [][]ValidationRule  // each inner slice is one oneOf variant's rules; exactly one must pass
	TupleItems        []TupleItemDef      // per-position type validation for tuple arrays (prefixItems / items-as-array)
	ItemValidations   []ItemValidationDef // per-element checks when the alias is an array with a single items sub-schema
	Contains          *ContainsDef        // contains sub-schema validation
	MinContains       *int                // minContains (default 1 if contains is present)
	MaxContains       *int                // maxContains
	UnevaluatedItems  *UnevaluatedItemsDef
	ValidateAs        string // named underlying type whose Validate method should be delegated to
	UnmarshalAs       string // named underlying type whose UnmarshalJSON behavior should be delegated to
	MarshalAs         string // named underlying type whose MarshalJSON behavior should be delegated to
	StrictInteger     bool   // true when integer JSON must use an integer token, not 1.0/1e0
	NoMethods         bool   // set by resolveAliasMethodability when underlying chain resolves to pointer/interface
	NeedsNullCheck    bool   // true when the schema's type does not include "null" — reject null JSON data
	AcceptNonMatching bool   // true when schema has no explicit type — silently accept non-matching JSON data
}

func (d *AliasDef) TypeName() string { return d.Name }
func (d *AliasDef) typeDef()         {}

// HasContainsValidation returns true if the AliasDef has contains validation.
func (d *AliasDef) HasContainsValidation() bool {
	return d.Contains != nil
}

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

// HasUnevaluatedItems returns true if this alias has unevaluatedItems validation.
func (d *AliasDef) HasUnevaluatedItems() bool {
	return d.UnevaluatedItems != nil
}

// HasItemValidations returns true if this alias has per-element checks.
func (d *AliasDef) HasItemValidations() bool {
	return len(d.ItemValidations) > 0
}

// ItemValidationDef carries the constraints an `items` sub-schema places on the
// elements of a slice. Dispatching to an element's own Validate only works when
// the element Go type is named; a []string, a []any or a nested [][]int64
// carries no such method, so without this the keywords under `items` are
// enforced nowhere and the generated Validate accepts data the schema forbids.
//
// Levels holds one entry per array dimension — [][]string carries two — and
// each level owns the checks for the value at that depth.
type ItemValidationDef struct {
	FieldName string // Go field name; empty when the slice is the receiver itself (an array alias)
	JSONName  string // JSON property name for the error path; empty for an array alias
	IsPointer bool   // the field is *[]T, so the loop needs a nil guard
	Levels    []ItemLevel
}

// ItemLevel is one array dimension of an ItemValidationDef.
type ItemLevel struct {
	IndexVar      string // loop index variable for this dimension
	ElemVar       string // element variable for this dimension
	ElemIsPointer bool   // the element is a pointer, so a JSON null left nil behind
	ElemTypeName  string // the element's named Go type, when it has one
	ElemType      GoType // the element's Go type
	CallValidate  bool   // settled after generation: dispatch to the element's own Validate
	Rules         []ValidationRule
}

// carries reports whether this level emits a check of its own.
func (l ItemLevel) carries() bool { return l.CallValidate || len(l.Rules) > 0 }

// pending reports whether this level may still acquire a check. A named element
// type's Validate call is only settled once every type def exists, so before
// then the name alone is reason enough to keep the level.
func (l ItemLevel) pending() bool { return l.carries() || l.ElemTypeName != "" }

// trim drops trailing levels that keep rejects, and returns whether any remain.
// A level that emits nothing would leave its loop variables unused,
// which Go rejects outright; a barren level *above* one that does carry a check
// stays, because the inner loop and the error path still name its variables.
func (d *ItemValidationDef) trim(keep func(ItemLevel) bool) bool {
	for len(d.Levels) > 0 && !keep(d.Levels[len(d.Levels)-1]) {
		d.Levels = d.Levels[:len(d.Levels)-1]
	}
	return len(d.Levels) > 0
}

// InferredAliasDef represents a type where the Go type was inferred from
// constraint keywords (not explicitly declared via "type"). It generates a
// wrapper struct that accepts any JSON value but provides typed access for
// the expected type. Non-matching JSON types are silently accepted per JSON
// Schema semantics (constraints only apply to matching types).
type InferredAliasDef struct {
	Name             string
	Description      string
	InferredGoType   GoType           // float64, string, or []any
	InferredJSONType string           // "number", "string", "array" — for accessor naming
	Validations      []ValidationRule // constraint rules (minimum, maxLength, etc.)
	AnyOfVariants    [][]ValidationRule
	OneOfVariants    [][]ValidationRule
	ValidateAs       string
	NeedsNullCheck   bool

	// Item-level validation for inferred arrays:
	ItemsFalse           bool                // items: false — reject any non-empty array
	ItemsType            string              // items as single schema with simple JSON type (e.g., "integer", "string")
	ItemsTypeName        string              // items as single schema referencing a named Go type (call Validate())
	ItemsChecks          []ContainsCheck     // per-element validation checks from items sub-schema (multipleOf, minimum, etc.)
	ItemsNested          *NestedItemsDef     // per-element nested array item validation from an items sub-schema
	TupleItems           []InferredTupleItem // per-position schemas (prefixItems / items-as-array)
	AdditionalItemsFalse bool                // additionalItems: false (or items: false in draft 2020-12 with prefixItems)
	AdditionalItemsType  string              // additionalItems as simple JSON type

	// Contains validation for inferred arrays:
	Contains    *ContainsDef // contains sub-schema validation
	MinContains *int         // minContains (default 1 when contains is present)
	MaxContains *int         // maxContains (nil = no upper bound)

	// UnevaluatedItems validation for inferred arrays:
	UnevaluatedItems *UnevaluatedItemsDef // unevaluatedItems constraint (Draft 2019-09+)
}

// NestedItemsDef describes nested array item validation for schemas like
// {"items":{"items":{"$ref":"..."}}}. It covers a narrow but common case
// where outer array elements are arrays whose own elements have constraints.
type NestedItemsDef struct {
	ItemsType string
}

// ContainsDef describes a contains constraint on an array.
type ContainsDef struct {
	IsFalse   bool            // contains: false — no element can ever match
	IsTrue    bool            // contains: true — every element matches
	ConstJSON string          // JSON-encoded const value for exact matching (e.g., "5")
	EnumJSON  []string        // JSON-encoded enum values for multi-value matching
	Checks    []ContainsCheck // per-element validation checks
}

// ContainsCheck describes one validation check applied to each element
// when evaluating whether it matches the contains sub-schema.
type ContainsCheck struct {
	CheckType string // "minimum", "maximum", "multipleOf", "type", "exclusiveMinimum", "exclusiveMaximum", "minLength", "maxLength", "pattern"
	Value     any    // the constraint value
}

// UnevaluatedItemsDef describes an unevaluatedItems constraint on an array.
// Items are "evaluated" if covered by items, prefixItems, additionalItems, contains,
// or by sub-schemas in allOf/$ref/anyOf/oneOf/if-then-else.
type UnevaluatedItemsDef struct {
	IsForbidden       bool            // unevaluatedItems: false — reject any unevaluated items
	IsAllowed         bool            // unevaluatedItems: true — allow any unevaluated item (no-op)
	AllEvaluated      bool            // true when items (uniform) or additionalItems covers all positions
	EvaluatedCount    int             // number of statically evaluated positions (from prefixItems/tuple)
	ContainsEvaluates bool            // true when adjacent contains marks matching items as evaluated (runtime check)
	ValueType         string          // JSON type constraint on unevaluated items (e.g., "string", "integer")
	Checks            []ContainsCheck // validation checks on each unevaluated item
	// ConditionalEvals holds runtime-conditional evaluation branches (allOf prefixItems, anyOf/oneOf items, if/then/else)
	ConditionalEvals []UnevalItemsConditionalEval
}

// HasUnevaluatedItems returns true if the InferredAliasDef has unevaluatedItems validation.
func (d *InferredAliasDef) HasUnevaluatedItems() bool {
	return d.UnevaluatedItems != nil
}

// UnevalItemsConditionalEval describes a conditional evaluation branch for unevaluatedItems.
// At runtime, if a branch matches, its evaluated item count is used.
type UnevalItemsConditionalEval struct {
	Kind string // "allOf", "anyOf", "oneOf", "ifThenElse", "ref", "contains"
	// For allOf/$ref: items are always evaluated (static)
	EvaluatedCount int  // number of additional evaluated positions from this branch
	AllEvaluated   bool // branch covers all items (has uniform items)
	// For anyOf/oneOf: branches are tried at runtime
	Branches []UnevalItemsBranch
	// For ifThenElse:
	IfItemChecks  []IfItemConstCheck // runtime checks on array items to evaluate the if-condition
	IfEvalCount   int                // items evaluated by the if-schema itself (its prefixItems length)
	IfAllEval     bool               // if-schema covers all items
	ThenEvalCount int                // items evaluated by then branch
	ThenAllEval   bool               // then branch covers all items
	ElseEvalCount int                // items evaluated by else branch
	ElseAllEval   bool               // else branch covers all items
	// For contains:
	ContainsAllEval bool // if contains evaluates all items
}

// IfItemConstCheck describes a const check on a specific array position for if-condition evaluation.
type IfItemConstCheck struct {
	Index     int    // array position to check (0-based)
	JSONValue string // expected JSON-marshaled value (e.g., `"bar"`)
}

// UnevalItemsBranch describes one branch in an anyOf/oneOf for unevaluatedItems evaluation.
type UnevalItemsBranch struct {
	EvaluatedCount int  // number of evaluated positions in this branch
	AllEvaluated   bool // branch covers all items (has uniform items)
}

// InferredTupleItem describes a per-position item schema for inferred arrays.
type InferredTupleItem struct {
	IsFalse  bool   // boolean false schema — reject any value at this position
	JSONType string // simple JSON type constraint (e.g., "integer", "string")
	TypeName string // named Go type for $ref-based items (unmarshal + Validate())
}

// HasItemValidation returns true if the InferredAliasDef has any item-level validation.
func (d *InferredAliasDef) HasItemValidation() bool {
	return d.ItemsFalse || d.ItemsType != "" || d.ItemsTypeName != "" ||
		len(d.ItemsChecks) > 0 || d.ItemsNested != nil ||
		len(d.TupleItems) > 0 || d.AdditionalItemsFalse || d.AdditionalItemsType != ""
}

// HasContainsValidation returns true if the InferredAliasDef has contains validation.
func (d *InferredAliasDef) HasContainsValidation() bool {
	return d.Contains != nil
}

func (d *InferredAliasDef) TypeName() string { return d.Name }
func (d *InferredAliasDef) typeDef()         {}

// AccessorName returns the Go method name for typed access (e.g., "Float64", "StringValue", "Slice").
func (d *InferredAliasDef) AccessorName() string {
	switch d.InferredJSONType {
	case "number":
		return "Float64"
	case "string":
		return "StringValue"
	case "array":
		return "Slice"
	default:
		return "Value"
	}
}

// TypeCheckName returns the Go method name for type checking (e.g., "IsNumber", "IsString", "IsArray").
func (d *InferredAliasDef) TypeCheckName() string {
	switch d.InferredJSONType {
	case "number":
		return "IsNumber"
	case "string":
		return "IsString"
	case "array":
		return "IsArray"
	default:
		return "IsTyped"
	}
}

// GoTypeName returns the Go type name of the inferred type.
func (d *InferredAliasDef) GoTypeName() string {
	return d.InferredGoType.GoTypeName()
}

// BigIntAliasDef represents an integer type with arbitrary-precision support.
// It generates a wrapper struct with int64 + *big.Int fields. Values that fit
// in int64 are stored there; larger values use big.Int. This is only generated
// when Config.BigIntSupport is true and the schema type is "integer".
type BigIntAliasDef struct {
	Name           string
	Description    string
	Validations    []ValidationRule
	AnyOfVariants  [][]ValidationRule
	OneOfVariants  [][]ValidationRule
	NeedsNullCheck bool
}

func (d *BigIntAliasDef) TypeName() string { return d.Name }
func (d *BigIntAliasDef) typeDef()         {}

// NotSchemaDef represents a schema whose only constraint is a root-level "not".
// It generates a wrapper struct around json.RawMessage that validates the negated
// constraint. The generated type accepts any JSON value and rejects those that
// match the not sub-schema.
type NotSchemaDef struct {
	Name        string
	Description string
	IsForbidden bool              // not:{} or not:true — reject everything
	NotTypes    []string          // not:{type:X} — reject values of these JSON types
	NotBranches []NotSchemaBranch // not:anyOf branches from draft3 disallow arrays
}

type NotSchemaBranch struct {
	Types       []string
	Properties  []NotPropertyBranch
	Validations []ValidationRule
}

type NotPropertyBranch struct {
	Name     string
	JSONType string
}

// DynamicCheck is one constraint evaluated against a decoded JSON value whose
// Go type is not known statically. Kind names the JSON Schema keyword; Value is
// its argument.
//
// Evaluation follows JSON Schema semantics: every keyword except "type" is
// vacuously satisfied by a value of an inapplicable type, so {"minimum":2} is
// satisfied by "abc".
type DynamicCheck struct {
	Kind  string // "type", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength", "pattern"
	Value any
}

// DynamicSchemaDef represents a root schema that constrains values through
// applicators (oneOf/anyOf/if-then-else) while declaring no type of its own.
//
// Such a schema accepts any JSON value, so the natural Go type is `any` -- but
// Go forbids methods on a type whose underlying type is an interface, which is
// why these schemas used to generate a bare `type X any` with no Validate() and
// silently dropped every constraint. Wrapping the raw JSON in a struct, exactly
// as NotSchemaDef does for root-level "not", makes a Validate() method possible.
type DynamicSchemaDef struct {
	Name        string
	Description string

	OneOf [][]DynamicCheck // exactly one branch must match
	AnyOf [][]DynamicCheck // at least one branch must match

	HasIfThenElse bool
	If            []DynamicCheck
	Then          []DynamicCheck
	Else          []DynamicCheck
	HasThen       bool
	HasElse       bool
}

// AnnotationSchemaDef represents a schema whose unevaluatedItems depends on
// which items its in-place applicators evaluated for the value being validated.
//
// That set is a property of the instance, not of the schema, so it cannot be
// computed at generation time: {"anyOf":[A,B],"unevaluatedItems":false} allows
// whatever the branches that matched *this value* evaluated. The schema is
// emitted as a data literal and interpreted by the runtime evaluator in the
// package's shared helper file.
type AnnotationSchemaDef struct {
	Name        string
	Description string
	NodeLiteral string // Go composite literal for the root _schemaNode
}

func (d *AnnotationSchemaDef) TypeName() string { return d.Name }
func (d *AnnotationSchemaDef) typeDef()         {}

func (d *DynamicSchemaDef) TypeName() string { return d.Name }
func (d *DynamicSchemaDef) typeDef()         {}

// NeedsPattern reports whether any check uses "pattern", which requires the
// ECMA-262 regexp import in the generated file.
func (d *DynamicSchemaDef) NeedsPattern() bool {
	return d.anyCheck(func(c DynamicCheck) bool { return c.Kind == "pattern" })
}

// NeedsMultipleOf reports whether any check needs math.Mod in the generated
// file itself.
func (d *DynamicSchemaDef) NeedsMultipleOf() bool {
	return d.anyCheck(func(c DynamicCheck) bool { return c.Kind == "multipleOf" })
}

// NeedsUTF8 reports whether any check measures string length.
func (d *DynamicSchemaDef) NeedsUTF8() bool {
	return d.anyCheck(func(c DynamicCheck) bool { return c.Kind == "minLength" || c.Kind == "maxLength" })
}

func (d *DynamicSchemaDef) anyCheck(pred func(DynamicCheck) bool) bool {
	groups := [][][]DynamicCheck{d.OneOf, d.AnyOf, {d.If, d.Then, d.Else}}
	for _, g := range groups {
		for _, branch := range g {
			for _, c := range branch {
				if pred(c) {
					return true
				}
			}
		}
	}
	return false
}

func (d *NotSchemaDef) TypeName() string { return d.Name }
func (d *NotSchemaDef) typeDef()         {}

// TypeOnlySchemaDef represents a schema whose sole constraint is a "type" field
// with types that don't map to a single Go type (e.g., "null", ["integer","string"],
// ["array","object","null"]). It generates a wrapper around json.RawMessage that
// validates the value's JSON type against the allowed types.
type TypeOnlySchemaDef struct {
	Name         string
	Description  string
	AllowedTypes []string           // JSON types: "null", "integer", "number", "string", "boolean", "array", "object"
	TypeBranches []TypeSchemaBranch // one per alternative: draft-3 schema-valued type entries, anyOf variants, or the types of a multi-type union
}

type TypeSchemaBranch struct {
	AllowedTypes []string
	Properties   []TypeSchemaProperty

	// TypeName delegates the whole branch to a generated type: the raw value
	// matches when it unmarshals into that type and the type's own Validate
	// accepts it. A schema-valued alternative that is a $ref carries no inline
	// type or properties to check, so without this the branch would enforce
	// nothing.
	TypeName string
}

type TypeSchemaProperty struct {
	Name     string
	JSONType string
	Required bool
}

func (d *TypeOnlySchemaDef) TypeName() string { return d.Name }
func (d *TypeOnlySchemaDef) typeDef()         {}

// ---------- File ----------

// File represents a generated Go source file.
type File struct {
	PackageName          string
	TypeDefs             []TypeDef
	Imports              []Import
	ValidationCapability ValidationCapability
}

// Import represents a Go import.
type Import struct {
	Path  string
	Alias string
}
