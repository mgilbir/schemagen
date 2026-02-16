// Package schema provides types for parsing JSON Schema documents across all draft versions
// (Draft 3, Draft 4, Draft 6, Draft 7, Draft 2019-09, Draft 2020-12).
package schema

import (
	"encoding/json"
	"fmt"
	"math"
)

// FlexInt is an integer type that tolerates float-encoded integers in JSON (e.g. 2.0).
// JSON has no distinction between integers and floats, so test suites often use 2.0 where
// an integer is expected.
type FlexInt int

func (f *FlexInt) UnmarshalJSON(data []byte) error {
	// Try int first.
	var i int
	if err := json.Unmarshal(data, &i); err == nil {
		*f = FlexInt(i)
		return nil
	}

	// Try float and check if it's a whole number.
	var n float64
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("expected integer, got: %s", string(data))
	}
	if n != math.Trunc(n) {
		return fmt.Errorf("expected integer, got float: %s", string(data))
	}
	*f = FlexInt(int(n))
	return nil
}

func (f FlexInt) MarshalJSON() ([]byte, error) {
	return json.Marshal(int(f))
}

// Int returns the FlexInt as a plain int.
func (f FlexInt) Int() int {
	return int(f)
}

// TypeList represents a JSON Schema "type" value, which can be either a single
// string (e.g. "string") or an array of strings (e.g. ["string", "null"]).
// Draft 3 also allows an array of schemas as type values; those schemas are
// ignored for type extraction, but the string type names are preserved.
type TypeList []string

func (t *TypeList) UnmarshalJSON(data []byte) error {
	// Try single string first.
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*t = TypeList{single}
		return nil
	}

	// Try array of strings.
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*t = TypeList(arr)
		return nil
	}

	// Draft 3: try array that may contain schemas or strings.
	// Extract string elements and schema objects with "type" fields.
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("type must be a string or array of strings: %s", string(data))
	}

	var types []string
	for _, elem := range raw {
		// Try as string.
		var s string
		if json.Unmarshal(elem, &s) == nil {
			types = append(types, s)
			continue
		}
		// Try as schema with a "type" field.
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(elem, &probe) == nil && probe.Type != "" {
			types = append(types, probe.Type)
			continue
		}
		// Skip elements we can't extract type info from.
	}
	*t = TypeList(types)
	return nil
}

func (t TypeList) MarshalJSON() ([]byte, error) {
	if len(t) == 1 {
		return json.Marshal(t[0])
	}
	return json.Marshal([]string(t))
}

// SchemaOrBool represents a value that can be either a JSON Schema or a boolean.
// Used for additionalProperties, additionalItems, etc.
type SchemaOrBool struct {
	Schema *Schema
	Bool   *bool
}

func (s *SchemaOrBool) UnmarshalJSON(data []byte) error {
	// Try boolean first.
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		s.Bool = &b
		s.Schema = nil
		return nil
	}

	// Try schema object.
	var sc Schema
	if err := json.Unmarshal(data, &sc); err != nil {
		return fmt.Errorf("must be a boolean or schema object: %s", string(data))
	}
	s.Schema = &sc
	s.Bool = nil
	return nil
}

func (s SchemaOrBool) MarshalJSON() ([]byte, error) {
	if s.Bool != nil {
		return json.Marshal(*s.Bool)
	}
	return json.Marshal(s.Schema)
}

// SchemaOrFloat represents a value that can be either a number (Draft 2020-12)
// or a boolean (Draft-07) for exclusiveMinimum/exclusiveMaximum.
type SchemaOrFloat struct {
	Number *float64
	Bool   *bool
}

func (s *SchemaOrFloat) UnmarshalJSON(data []byte) error {
	// Try boolean first.
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		s.Bool = &b
		s.Number = nil
		return nil
	}

	// Try number.
	var n float64
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("must be a boolean or number: %s", string(data))
	}
	s.Number = &n
	s.Bool = nil
	return nil
}

func (s SchemaOrFloat) MarshalJSON() ([]byte, error) {
	if s.Bool != nil {
		return json.Marshal(*s.Bool)
	}
	if s.Number != nil {
		return json.Marshal(*s.Number)
	}
	return json.Marshal(nil)
}

// SchemaOrSchemaArray represents a value that can be either a single schema,
// a boolean schema, or an array of schemas (possibly containing booleans).
// Used for "items" and "prefixItems".
type SchemaOrSchemaArray struct {
	Schema  *Schema
	Schemas []*Schema
}

func (s *SchemaOrSchemaArray) UnmarshalJSON(data []byte) error {
	// Try boolean first (e.g., items: false).
	trimmed := trimJSONWhitespace(data)
	if trimmed == "true" || trimmed == "false" {
		var sc Schema
		if err := json.Unmarshal(data, &sc); err != nil {
			return err
		}
		s.Schema = &sc
		s.Schemas = nil
		return nil
	}

	// Try array.
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var arr []*Schema
		if err := json.Unmarshal(data, &arr); err != nil {
			return fmt.Errorf("must be a schema or array of schemas: %s", string(data))
		}
		s.Schemas = arr
		s.Schema = nil
		return nil
	}

	// Try single schema object.
	var sc Schema
	if err := json.Unmarshal(data, &sc); err != nil {
		return fmt.Errorf("must be a schema or array of schemas: %s", string(data))
	}
	s.Schema = &sc
	s.Schemas = nil
	return nil
}

func (s SchemaOrSchemaArray) MarshalJSON() ([]byte, error) {
	if s.Schemas != nil {
		return json.Marshal(s.Schemas)
	}
	return json.Marshal(s.Schema)
}

// RequiredList represents the "required" keyword, which is an array of strings
// in Draft 4+ but a boolean in Draft 3 (on individual properties).
// When parsed as a boolean (Draft 3), it is stored as an empty list — the
// Normalize() function on the parent schema handles the conversion.
type RequiredList []string

func (r *RequiredList) UnmarshalJSON(data []byte) error {
	// Try array of strings first (Draft 4+).
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*r = RequiredList(arr)
		return nil
	}

	// Try boolean (Draft 3: "required": true on individual properties).
	// We accept it without error but store nothing — the parent Schema's
	// RequiredBool field captures the boolean value via raw JSON parsing.
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		*r = RequiredList{}
		return nil
	}

	return fmt.Errorf("required must be an array of strings or boolean: %s", string(data))
}

func (r RequiredList) MarshalJSON() ([]byte, error) {
	return json.Marshal([]string(r))
}

// Schema represents a JSON Schema document. It is a superset struct that supports
// keywords from all draft versions. Draft-specific normalization is done by Normalize().
type Schema struct {
	// BooleanSchema is non-nil when this schema position contained a bare true/false.
	// In JSON Schema Draft 6+, true is the "always valid" schema and false is "always invalid".
	BooleanSchema *bool `json:"-"`

	// Core identifiers
	ID       string `json:"$id,omitempty"`
	LegacyID string `json:"id,omitempty"` // Draft 3/4 use "id" instead of "$id"
	Schema   string `json:"$schema,omitempty"`
	Ref      string `json:"$ref,omitempty"`
	Anchor   string `json:"$anchor,omitempty"` // Draft 2019-09+

	// Type
	Type TypeList `json:"type,omitempty"`

	// Composition
	AllOf []*Schema `json:"allOf,omitempty"`
	AnyOf []*Schema `json:"anyOf,omitempty"`
	OneOf []*Schema `json:"oneOf,omitempty"`
	Not   *Schema   `json:"not,omitempty"`

	// Object keywords
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             RequiredList       `json:"required,omitempty"`
	AdditionalProperties *SchemaOrBool      `json:"additionalProperties,omitempty"`
	PatternProperties    map[string]*Schema `json:"patternProperties,omitempty"`
	MinProperties        *FlexInt           `json:"minProperties,omitempty"`
	MaxProperties        *FlexInt           `json:"maxProperties,omitempty"`

	// Array keywords
	Items           *SchemaOrSchemaArray `json:"items,omitempty"`
	PrefixItems     []*Schema            `json:"prefixItems,omitempty"`
	AdditionalItems *SchemaOrBool        `json:"additionalItems,omitempty"`
	MinItems        *FlexInt             `json:"minItems,omitempty"`
	MaxItems        *FlexInt             `json:"maxItems,omitempty"`
	UniqueItems     *bool                `json:"uniqueItems,omitempty"`
	Contains        *Schema              `json:"contains,omitempty"`

	// String keywords
	MinLength *FlexInt `json:"minLength,omitempty"`
	MaxLength *FlexInt `json:"maxLength,omitempty"`
	Pattern   *string  `json:"pattern,omitempty"`
	Format    *string  `json:"format,omitempty"`

	// Numeric keywords
	Minimum          *float64       `json:"minimum,omitempty"`
	Maximum          *float64       `json:"maximum,omitempty"`
	ExclusiveMinimum *SchemaOrFloat `json:"exclusiveMinimum,omitempty"`
	ExclusiveMaximum *SchemaOrFloat `json:"exclusiveMaximum,omitempty"`
	MultipleOf       *float64       `json:"multipleOf,omitempty"`

	// Enum and const
	Enum    []any `json:"enum,omitempty"`
	Const   *any  `json:"const,omitempty"`
	Default *any  `json:"default,omitempty"`

	// Metadata
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`

	// Definitions (Draft-07 uses "definitions", 2020-12 uses "$defs")
	Definitions map[string]*Schema `json:"definitions,omitempty"`
	Defs        map[string]*Schema `json:"$defs,omitempty"`

	// Conditional (Draft 7+)
	If   *Schema `json:"if,omitempty"`
	Then *Schema `json:"then,omitempty"`
	Else *Schema `json:"else,omitempty"`

	// Draft 3 specific
	Extends     json.RawMessage `json:"extends,omitempty"`     // Schema or array of schemas
	Disallow    json.RawMessage `json:"disallow,omitempty"`    // string or array of strings
	DivisibleBy *float64        `json:"divisibleBy,omitempty"` // precursor to multipleOf

	// Draft 4/6/7: dependencies (object where values are schemas or string arrays)
	Dependencies json.RawMessage `json:"dependencies,omitempty"`

	// Draft 2019-09+
	DependentSchemas  map[string]*Schema  `json:"dependentSchemas,omitempty"`
	DependentRequired map[string][]string `json:"dependentRequired,omitempty"`
	RecursiveRef      string              `json:"$recursiveRef,omitempty"`
	RecursiveAnchor   *bool               `json:"$recursiveAnchor,omitempty"`

	// Draft 2020-12
	DynamicRef    string `json:"$dynamicRef,omitempty"`
	DynamicAnchor string `json:"$dynamicAnchor,omitempty"`

	// Max/MinContains (Draft 2019-09+)
	MaxContains *FlexInt `json:"maxContains,omitempty"`
	MinContains *FlexInt `json:"minContains,omitempty"`

	// Content (Draft 7+)
	ContentMediaType string  `json:"contentMediaType,omitempty"`
	ContentEncoding  string  `json:"contentEncoding,omitempty"`
	ContentSchema    *Schema `json:"contentSchema,omitempty"` // Draft 2019-09+

	// PropertyNames (Draft 6+)
	PropertyNames *Schema `json:"propertyNames,omitempty"`

	// Unevaluated (Draft 2019-09+)
	UnevaluatedItems      *Schema `json:"unevaluatedItems,omitempty"`
	UnevaluatedProperties *Schema `json:"unevaluatedProperties,omitempty"`

	// DetectedDraft is set during parsing to record which draft was detected/used.
	DetectedDraft Draft `json:"-"`
}

// UnmarshalJSON implements custom unmarshaling for Schema to handle boolean schemas.
// In JSON Schema Draft 6+, a bare `true` or `false` is a valid schema.
func (s *Schema) UnmarshalJSON(data []byte) error {
	// Check for boolean schema.
	trimmed := trimJSONWhitespace(data)
	if trimmed == "true" {
		b := true
		s.BooleanSchema = &b
		return nil
	}
	if trimmed == "false" {
		b := false
		s.BooleanSchema = &b
		return nil
	}

	// Use an alias to avoid infinite recursion.
	type schemaAlias Schema
	var alias schemaAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*s = Schema(alias)
	return nil
}

// MarshalJSON implements custom marshaling for Schema to handle boolean schemas.
func (s Schema) MarshalJSON() ([]byte, error) {
	if s.BooleanSchema != nil {
		return json.Marshal(*s.BooleanSchema)
	}
	type schemaAlias Schema
	return json.Marshal(schemaAlias(s))
}

// IsBooleanSchema returns true if this schema is a bare true/false.
func (s *Schema) IsBooleanSchema() bool {
	return s.BooleanSchema != nil
}

// IsTrueSchema returns true if this is a boolean schema with value true.
func (s *Schema) IsTrueSchema() bool {
	return s.BooleanSchema != nil && *s.BooleanSchema
}

// IsFalseSchema returns true if this is a boolean schema with value false.
func (s *Schema) IsFalseSchema() bool {
	return s.BooleanSchema != nil && !*s.BooleanSchema
}

// trimJSONWhitespace strips leading/trailing whitespace from JSON data
// and returns it as a string for easy comparison.
func trimJSONWhitespace(data []byte) string {
	// Manual trim for speed — JSON whitespace is space, tab, newline, carriage return.
	start, end := 0, len(data)
	for start < end && (data[start] == ' ' || data[start] == '\t' || data[start] == '\n' || data[start] == '\r') {
		start++
	}
	for end > start && (data[end-1] == ' ' || data[end-1] == '\t' || data[end-1] == '\n' || data[end-1] == '\r') {
		end--
	}
	return string(data[start:end])
}
