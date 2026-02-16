// Package schema provides types for parsing JSON Schema documents (Draft-07 and Draft 2020-12).
package schema

import (
	"encoding/json"
	"fmt"
)

// TypeList represents a JSON Schema "type" value, which can be either a single
// string (e.g. "string") or an array of strings (e.g. ["string", "null"]).
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
	if err := json.Unmarshal(data, &arr); err != nil {
		return fmt.Errorf("type must be a string or array of strings: %s", string(data))
	}
	*t = TypeList(arr)
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

// SchemaOrSchemaArray represents a value that can be either a single schema or
// an array of schemas. Used for "items".
type SchemaOrSchemaArray struct {
	Schema  *Schema
	Schemas []*Schema
}

func (s *SchemaOrSchemaArray) UnmarshalJSON(data []byte) error {
	// Try array first.
	var arr []*Schema
	if err := json.Unmarshal(data, &arr); err == nil {
		s.Schemas = arr
		s.Schema = nil
		return nil
	}

	// Try single schema.
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

// Schema represents a JSON Schema document. It supports both Draft-07 and
// Draft 2020-12 keywords.
type Schema struct {
	// Core identifiers
	ID     string `json:"$id,omitempty"`
	Schema string `json:"$schema,omitempty"`
	Ref    string `json:"$ref,omitempty"`

	// Type
	Type TypeList `json:"type,omitempty"`

	// Composition
	AllOf []*Schema `json:"allOf,omitempty"`
	AnyOf []*Schema `json:"anyOf,omitempty"`
	OneOf []*Schema `json:"oneOf,omitempty"`
	Not   *Schema   `json:"not,omitempty"`

	// Object keywords
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	AdditionalProperties *SchemaOrBool      `json:"additionalProperties,omitempty"`
	PatternProperties    map[string]*Schema `json:"patternProperties,omitempty"`
	MinProperties        *int               `json:"minProperties,omitempty"`
	MaxProperties        *int               `json:"maxProperties,omitempty"`

	// Array keywords
	Items           *SchemaOrSchemaArray `json:"items,omitempty"`
	PrefixItems     []*Schema            `json:"prefixItems,omitempty"`
	AdditionalItems *SchemaOrBool        `json:"additionalItems,omitempty"`
	MinItems        *int                 `json:"minItems,omitempty"`
	MaxItems        *int                 `json:"maxItems,omitempty"`
	UniqueItems     *bool                `json:"uniqueItems,omitempty"`
	Contains        *Schema              `json:"contains,omitempty"`

	// String keywords
	MinLength *int    `json:"minLength,omitempty"`
	MaxLength *int    `json:"maxLength,omitempty"`
	Pattern   *string `json:"pattern,omitempty"`
	Format    *string `json:"format,omitempty"`

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

	// Conditional
	If   *Schema `json:"if,omitempty"`
	Then *Schema `json:"then,omitempty"`
	Else *Schema `json:"else,omitempty"`
}
