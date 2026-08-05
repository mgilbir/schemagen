// Package schema provides types for parsing JSON Schema documents across all draft versions
// (Draft 3, Draft 4, Draft 6, Draft 7, Draft 2019-09, Draft 2020-12).
package schema

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"strings"
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
// preserved separately on Schema.TypeSchemas.
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
	// Schema-valued alternatives are captured by Schema.UnmarshalJSON.
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

	// boolSchema memoizes the *Schema materialized by AsSchema for the boolean
	// form, so repeated resolutions return the same node.
	boolSchema *Schema
}

// AsSchema returns the value as a *Schema, materializing the boolean form.
// Booleans are schemas in draft 6+, so {"additionalProperties": false} is a
// legal JSON-pointer $ref target. The materialized node is memoized: cycle
// detection compares schema pointers, so a fresh node per resolution would
// break it. Returns nil when neither form is set.
func (s *SchemaOrBool) AsSchema() *Schema {
	if s == nil {
		return nil
	}
	if s.Schema != nil {
		return s.Schema
	}
	if s.Bool == nil {
		return nil
	}
	if s.boolSchema == nil {
		b := *s.Bool
		s.boolSchema = &Schema{BooleanSchema: &b}
	}
	return s.boolSchema
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
// draft3RequiredSentinel is a sentinel value stored in RequiredList when
// Draft 3's "required": true is encountered on a property sub-schema.
// Normalize() converts these to the parent's Required array.
const draft3RequiredSentinel = "\x00__draft3_required_true__"

type RequiredList []string

// IsDraft3Required returns true if this list contains the draft3 sentinel,
// meaning the property had "required": true in Draft 3 format.
func (r RequiredList) IsDraft3Required() bool {
	return len(r) == 1 && r[0] == draft3RequiredSentinel
}

func (r *RequiredList) UnmarshalJSON(data []byte) error {
	// Try array of strings first (Draft 4+).
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*r = RequiredList(arr)
		return nil
	}

	// Try boolean (Draft 3: "required": true on individual properties).
	// Store a sentinel value so Normalize() can detect and convert to
	// the parent schema's Required array.
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		if b {
			*r = RequiredList{draft3RequiredSentinel}
		} else {
			*r = RequiredList{}
		}
		return nil
	}

	return fmt.Errorf("required must be an array of strings or boolean: %s", string(data))
}

func (r RequiredList) MarshalJSON() ([]byte, error) {
	return json.Marshal([]string(r))
}

// Discriminator represents an OpenAPI-style discriminator for oneOf/anyOf polymorphism.
// It identifies a property whose value determines which variant schema applies.
type Discriminator struct {
	// PropertyName is the name of the property that holds the discriminator value.
	PropertyName string `json:"propertyName"`
	// Mapping is an optional map from discriminator values to schema references.
	// If empty, the discriminator value is matched against variant const/enum values.
	Mapping map[string]string `json:"mapping,omitempty"`
}

// Schema represents a JSON Schema document. It is a superset struct that supports
// keywords from all draft versions. Draft-specific normalization is done by Normalize().
type Schema struct {
	// BooleanSchema is non-nil when this schema position contained a bare true/false.
	// In JSON Schema Draft 6+, true is the "always valid" schema and false is "always invalid".
	BooleanSchema *bool `json:"-"`

	// Core identifiers
	ID         string          `json:"$id,omitempty"`
	LegacyID   string          `json:"id,omitempty"` // Draft 3/4 use "id" instead of "$id"
	Schema     string          `json:"$schema,omitempty"`
	Vocabulary map[string]bool `json:"$vocabulary,omitempty"`
	Ref        string          `json:"$ref,omitempty"`
	Anchor     string          `json:"$anchor,omitempty"` // Draft 2019-09+

	// Type
	Type        TypeList  `json:"type,omitempty"`
	TypeSchemas []*Schema `json:"-"` // Draft 3 schema-valued entries in the type array

	// Composition
	AllOf         []*Schema      `json:"allOf,omitempty"`
	AnyOf         []*Schema      `json:"anyOf,omitempty"`
	OneOf         []*Schema      `json:"oneOf,omitempty"`
	Not           *Schema        `json:"not,omitempty"`
	Discriminator *Discriminator `json:"discriminator,omitempty"`

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
	Enum        []any `json:"enum,omitempty"`
	Const       *any  `json:"const,omitempty"`
	ConstIsNull bool  `json:"-"` // true when the schema has {"const": null}; Go's json.Unmarshal leaves *any nil for null
	Default     *any  `json:"default,omitempty"`

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

	// Extensions preserves unknown/vendor-specific keywords as raw JSON so that
	// JSON Pointer $ref (e.g., "#/unknown-keyword") can resolve into them.
	Extensions map[string]json.RawMessage `json:"-"`

	// extensionSchemas memoizes Extensions entries parsed as schemas by
	// extensionSchema, keyed by keyword.
	extensionSchemas map[string]*Schema

	// DetectedDraft is set during parsing to record which draft was detected/used.
	DetectedDraft Draft `json:"-"`

	// BaseURI is the effective base URI for resolving relative $ref values
	// within this schema. It is computed by ComputeBaseURIs and accounts for
	// nested $id declarations that change the resolution scope.
	BaseURI *url.URL `json:"-"`

	// DocumentRoot points to the schema node that serves as the "document root"
	// for JSON Pointer fragment resolution (e.g. $ref: "#/definitions/foo").
	// A new document root is established whenever a subschema declares its own $id.
	// If nil, the top-level schema is the document root.
	DocumentRoot *Schema `json:"-"`
}

// knownSchemaKeys is the set of JSON property names that correspond to struct
// fields on Schema. Anything else is captured in Extensions. Built at init time
// via reflection so it stays in sync with the struct definition automatically.
var knownSchemaKeys map[string]bool

func init() {
	knownSchemaKeys = make(map[string]bool)
	t := reflect.TypeOf(Schema{})
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		// Strip ",omitempty" etc.
		if idx := strings.IndexByte(tag, ','); idx != -1 {
			tag = tag[:idx]
		}
		if tag != "" && tag != "-" {
			knownSchemaKeys[tag] = true
		}
	}
}

// UnmarshalJSON implements custom unmarshaling for Schema to handle boolean schemas.
// In JSON Schema Draft 6+, a bare `true` or `false` is a valid schema.
// Unknown keywords are preserved in Extensions for JSON Pointer resolution.
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

	// Capture unknown keywords in Extensions for JSON Pointer $ref resolution.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil // non-object JSON (shouldn't happen here since we handled booleans above)
	}
	for key, val := range raw {
		if !knownSchemaKeys[key] {
			if s.Extensions == nil {
				s.Extensions = make(map[string]json.RawMessage)
			}
			s.Extensions[key] = val
		}
	}

	// Detect {"const": null} which Go's json.Unmarshal loses (sets *any to nil,
	// indistinguishable from "const not present").
	if constRaw, ok := raw["const"]; ok && string(constRaw) == "null" {
		s.ConstIsNull = true
	}

	// Draft 3 allows schema-valued entries in the type array. Preserve them so
	// validation can treat the type keyword as an anyOf over primitive names and
	// schema branches.
	if typeRaw, ok := raw["type"]; ok {
		var elems []json.RawMessage
		if json.Unmarshal(typeRaw, &elems) == nil {
			for _, elem := range elems {
				var typeName string
				if json.Unmarshal(elem, &typeName) == nil {
					continue
				}
				var typeSchema Schema
				if json.Unmarshal(elem, &typeSchema) == nil && !typeSchema.IsBooleanSchema() {
					s.TypeSchemas = append(s.TypeSchemas, &typeSchema)
				}
			}
		}
	}

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

// ComputeBaseURIs walks the schema tree and sets BaseURI and DocumentRoot on
// every node, accounting for nested $id declarations that change the resolution scope.
// The parentBaseURI is the base URI inherited from the parent (may be nil for the root).
// The documentRoot is the schema node that serves as the current document root for
// fragment resolution (initially the schema itself).
func (s *Schema) ComputeBaseURIs(parentBaseURI *url.URL, documentRoot *Schema) {
	if s == nil || s.IsBooleanSchema() {
		return
	}

	currentBase := parentBaseURI
	currentDocRoot := documentRoot

	// If this schema declares $id, it establishes a new base URI and document root.
	schemaID := s.ID
	if schemaID == "" {
		schemaID = s.LegacyID
	}
	if schemaID != "" {
		if idURL, err := url.Parse(schemaID); err == nil {
			if currentBase != nil {
				currentBase = currentBase.ResolveReference(idURL)
			} else {
				currentBase = idURL
			}
			// A schema with $id becomes the document root for its scope.
			currentDocRoot = s
		}
	}

	s.BaseURI = currentBase
	s.DocumentRoot = currentDocRoot

	// Recurse into all child schemas.
	for _, sub := range s.Properties {
		sub.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	for _, sub := range s.TypeSchemas {
		sub.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	for _, sub := range s.PatternProperties {
		sub.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	for _, sub := range s.Definitions {
		sub.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	for _, sub := range s.Defs {
		sub.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	for _, sub := range s.AllOf {
		sub.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	for _, sub := range s.AnyOf {
		sub.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	for _, sub := range s.OneOf {
		sub.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	if s.Not != nil {
		s.Not.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	if s.Items != nil && s.Items.Schema != nil {
		s.Items.Schema.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	if s.Items != nil {
		for _, sub := range s.Items.Schemas {
			sub.ComputeBaseURIs(currentBase, currentDocRoot)
		}
	}
	for _, sub := range s.PrefixItems {
		sub.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		s.AdditionalProperties.Schema.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	if s.AdditionalItems != nil && s.AdditionalItems.Schema != nil {
		s.AdditionalItems.Schema.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	if s.Contains != nil {
		s.Contains.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	if s.If != nil {
		s.If.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	if s.Then != nil {
		s.Then.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	if s.Else != nil {
		s.Else.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	if s.PropertyNames != nil {
		s.PropertyNames.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	if s.UnevaluatedItems != nil {
		s.UnevaluatedItems.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	if s.UnevaluatedProperties != nil {
		s.UnevaluatedProperties.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	if s.ContentSchema != nil {
		s.ContentSchema.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	for _, sub := range s.DependentSchemas {
		sub.ComputeBaseURIs(currentBase, currentDocRoot)
	}
}

// EffectiveRef returns the effective reference string for this schema.
// It returns $ref if set, otherwise $recursiveRef (draft 2019-09),
// otherwise "".
// Note: $dynamicRef (draft 2020-12) is intentionally excluded because it
// requires dynamic anchor resolution semantics that differ from simple $ref.
func (s *Schema) EffectiveRef() string {
	if s.Ref != "" {
		return s.Ref
	}
	if s.RecursiveRef != "" {
		return s.RecursiveRef
	}
	return ""
}

// extensionSchema parses the raw JSON of an unknown keyword as a schema so a
// JSON-pointer $ref can target it. The result is normalized (draft-3 and other
// legacy constructs inside an extension are canonicalized like anywhere else)
// and memoized on the parent: two refs to the same extension must yield the
// same node, because cycle detection compares schema pointers.
//
// tokens are the JSON Pointer segments still to be walked *inside* the keyword's
// value. They matter because the keyword itself need not be a schema: "examples"
// holds an array, and {"examples":[{"type":"string"}]} is targeted as
// "#/examples/0", so the element is the schema and the array is not.
func (s *Schema) extensionSchema(key string, tokens []string, raw json.RawMessage) (*Schema, error) {
	// Memoize per (keyword, path): "#/examples/0" and "#/examples/1" are
	// different nodes, so keying on the keyword alone would alias them.
	cacheKey := key
	if len(tokens) > 0 {
		cacheKey = key + "/" + strings.Join(tokens, "/")
	}
	if cached, ok := s.extensionSchemas[cacheKey]; ok {
		return cached, nil
	}

	target, err := walkRawJSON(raw, tokens)
	if err != nil {
		return nil, err
	}
	var sub Schema
	if err := json.Unmarshal(target, &sub); err != nil {
		return nil, err
	}
	sub.Normalize()
	if s.extensionSchemas == nil {
		s.extensionSchemas = make(map[string]*Schema)
	}
	s.extensionSchemas[cacheKey] = &sub
	return &sub, nil
}

// walkRawJSON follows JSON Pointer tokens through raw JSON, indexing arrays by
// number and objects by key. It stops at the first token that cannot be
// followed, so a caller can still parse what it reached.
func walkRawJSON(raw json.RawMessage, tokens []string) (json.RawMessage, error) {
	current := raw
	for i, token := range tokens {
		var arr []json.RawMessage
		if err := json.Unmarshal(current, &arr); err == nil {
			idx, err := parseIndex(token)
			if err != nil {
				return nil, fmt.Errorf("segment %q is not an array index", token)
			}
			if idx >= len(arr) {
				return nil, fmt.Errorf("index %d out of range (length %d)", idx, len(arr))
			}
			current = arr[idx]
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(current, &obj); err == nil {
			next, ok := obj[token]
			if !ok {
				return nil, fmt.Errorf("no member %q", token)
			}
			current = next
			continue
		}
		return nil, fmt.Errorf("cannot descend into segment %q at position %d", token, i)
	}
	return current, nil
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

// KeywordsMarshaledFormOmits returns the keywords this schema states that
// marshalling it back to JSON does not show.
//
// Anything asking "what does this schema state" reads the marshaled key set,
// because that reading is fail-closed: a keyword this package learns later comes
// with a struct field that marshals, or lands in Extensions, and either way it is
// counted rather than missed. That property is worth keeping and no enumeration
// written by hand has it -- a field nobody remembered to list would be dropped
// silently, which is the failure the marshaled form was chosen to avoid.
//
// What the marshaled form cannot do is carry a field whose *presence* its
// encoding erases, and there are exactly three:
//
//   - Enum is tagged omitempty, so `"enum": []` -- the schema that admits no
//     value at all -- marshals to nothing and reads as a schema that states
//     nothing.
//   - ConstIsNull is tagged "-", because encoding/json leaves a *any nil for a
//     JSON null and the flag is the only record that `"const": null` was written.
//   - TypeSchemas is tagged "-", and holds the draft 3 schema-valued entries of a
//     "type" array. A schema whose whole type list is schema-valued marshals with
//     no "type" at all.
//
// So the two are read together: the marshaled set for everything it can show,
// this for the three it cannot. Reading the marshaled set alone is what let
// acceptsEveryValue answer "accepts every value" for {"enum":[]}, which admits
// none, and for {"const":null}, which admits one; a position holding either then
// got a Go type with no check on it at all -- issues #142 and #154.
//
// TestSchemaFieldsAreClassifiedForPresence is what keeps this list complete: it
// reflects over Schema and fails when a field is added whose JSON tag hides it
// the same way, until that field is classified.
func (s *Schema) KeywordsMarshaledFormOmits() []string {
	if s == nil {
		return nil
	}
	var hidden []string
	if s.Enum != nil {
		hidden = append(hidden, "enum")
	}
	if s.ConstIsNull {
		hidden = append(hidden, "const")
	}
	if len(s.TypeSchemas) > 0 {
		hidden = append(hidden, "type")
	}
	return hidden
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
