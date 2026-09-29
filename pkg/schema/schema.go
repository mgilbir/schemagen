// Package schema provides types for parsing JSON Schema documents across all draft versions
// (Draft 3, Draft 4, Draft 6, Draft 7, Draft 2019-09, Draft 2020-12).
package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// FlexInt is the value of a keyword the specification defines as an integer
// count: minLength and maxLength, minItems and maxItems, minProperties and
// maxProperties, minContains and maxContains.
//
// It tolerates an integer written in a float's spelling -- 2.0, 1e3 -- because
// JSON has one number type and from draft 6 on 2.0 *is* the integer 2; the
// official suite writes counts that way on purpose.
//
// The literal is read exactly, as a decimal, and never through float64. The
// float64 reading it replaces was wrong twice over: a count past 2^53 lost its
// low digits, and a count past int64 went through a float-to-int conversion Go
// leaves implementation-defined, which on amd64 is MinInt64. So
// {"maxLength":9223372036854775808} became a maximum of -2^63 and refused "",
// and {"minLength":1e19} became a minimum of -2^63 and accepted "x".
//
// A count too large for an int is held as MaxInt, and that saturation
// preserves every verdict exactly rather than approximately. What the keyword
// is compared against is the length of a value that exists -- a string's
// characters, an array's elements, an object's members, a contains match count
// -- and none of those can reach MaxInt: a Go value that large does not fit in
// any address space the language runs in. So a maximum at or past MaxInt
// admits every value there is, exactly as the unbounded maximum the schema
// wrote does, and a minimum at or past it admits none, exactly as the
// unsatisfiable minimum the schema wrote does. Every reader of these fields
// then reaches the right answer by the comparison it already makes, with no
// special case to forget, and none of them does arithmetic on the bound that
// the saturated value could overflow (TestSaturatedCountsKeepTheirVerdict holds
// the generated code to that).
//
// The saturated value is how the bound is *compared*, and nothing else. It is
// not the number the schema wrote, so a saturated count also keeps the literal
// it was read from (Literal, String), and anything that states the bound to a
// person -- an error message -- states that: {"minLength":1e19} refuses "x" as
// shorter than 1e19, not as shorter than 9223372036854775807, a bound nobody
// wrote. Where the generated code runs is a second place the saturated value is
// not the whole answer: a 32-bit target's int is narrower than the generator's,
// and pkg/generator's CountBound emits the bound per target for that reason.
//
// A negative value is kept, saturating at MinInt the same way, and reported
// through Schema.MalformedKeywords: every dialect's metaschema defines these
// keywords as non-negative, except draft 3's maxLength, which is a plain
// integer there -- and a negative maximum is then a legal schema that admits
// no string at all. Which of the two a node is depends on its dialect, which
// is not known until Normalize, so the value is held and the verdict deferred.
type FlexInt struct {
	n int
	// literal is the number as the schema wrote it, kept only where n is not
	// that number: where it was saturated.
	literal string
}

// NewFlexInt returns the count n, for a Schema assembled in Go.
func NewFlexInt(n int) FlexInt { return FlexInt{n: n} }

// errNegativeCount marks a count the document wrote as a negative integer. It
// is a malformed value in every dialect but one; see FlexInt and
// negativeCountDefinedIn.
var errNegativeCount = errors.New("must be a non-negative integer")

func (f *FlexInt) UnmarshalJSON(data []byte) error {
	lit := trimJSONWhitespace(data)
	n, saturated, negative, err := parseIntegerLiteral(lit)
	if err != nil {
		return err
	}
	*f = FlexInt{n: n}
	if saturated {
		f.literal = lit
	}
	if negative {
		return fmt.Errorf("%s: %w", lit, errNegativeCount)
	}
	return nil
}

// parseIntegerLiteral reads a JSON number literal as an integer, exactly.
//
// It accepts every spelling JSON has for an integer -- 12, 12.0, 1.2e1,
// 120e-1 -- and refuses a literal that names a number with a fractional part,
// or a JSON value that is not a number at all. The value is saturated at the
// bounds of an int, and saturated says so; negative reports whether it was
// below zero.
//
// The literal is taken apart by hand rather than through big.Rat because the
// exponent is written by the document, and big.Rat would build 1e1000000000
// out in full. Here the question "how many digits does this integer have" is
// answered from the exponent without materializing any of them.
func parseIntegerLiteral(lit string) (n int, saturated, negative bool, err error) {
	bad := func() (int, bool, bool, error) {
		return 0, false, false, fmt.Errorf("expected an integer, got: %s", lit)
	}
	i := 0
	if i < len(lit) && lit[i] == '-' {
		negative = true
		i++
	}
	intStart := i
	for i < len(lit) && lit[i] >= '0' && lit[i] <= '9' {
		i++
	}
	intDigits := lit[intStart:i]
	if intDigits == "" || (len(intDigits) > 1 && intDigits[0] == '0') {
		return bad()
	}
	fracDigits := ""
	if i < len(lit) && lit[i] == '.' {
		i++
		fracStart := i
		for i < len(lit) && lit[i] >= '0' && lit[i] <= '9' {
			i++
		}
		fracDigits = lit[fracStart:i]
		if fracDigits == "" {
			return bad()
		}
	}
	// The exponent, clamped: past ±2^62 no literal the size of a document can
	// bring the value back into range, so the clamp changes no answer below.
	var exp int64
	if i < len(lit) && (lit[i] == 'e' || lit[i] == 'E') {
		i++
		expNegative := false
		if i < len(lit) && (lit[i] == '+' || lit[i] == '-') {
			expNegative = lit[i] == '-'
			i++
		}
		expStart := i
		for i < len(lit) && lit[i] >= '0' && lit[i] <= '9' {
			if exp < 1<<62 {
				exp = exp*10 + int64(lit[i]-'0')
			}
			i++
		}
		if i == expStart {
			return bad()
		}
		if expNegative {
			exp = -exp
		}
	}
	if i != len(lit) {
		return bad()
	}

	// value = digits × 10^scale, with digits free of leading and trailing zeros.
	digits := strings.TrimLeft(intDigits+fracDigits, "0")
	scale := exp - int64(len(fracDigits))
	trimmed := strings.TrimRight(digits, "0")
	scale += int64(len(digits) - len(trimmed))
	digits = trimmed
	if digits == "" {
		return 0, false, false, nil // zero, however it was spelled, including -0
	}
	if scale < 0 {
		return 0, false, false, fmt.Errorf("expected an integer, got a fraction: %s", lit)
	}
	saturate := func() (int, bool, bool, error) {
		if negative {
			return math.MinInt, true, true, nil
		}
		return math.MaxInt, true, false, nil
	}
	// An int holds at most 19 decimal digits; anything longer saturates.
	if int64(len(digits))+scale > 19 {
		return saturate()
	}
	u, perr := strconv.ParseUint(digits+strings.Repeat("0", int(scale)), 10, 64)
	if perr != nil {
		return bad()
	}
	if negative {
		if u > uint64(math.MaxInt)+1 {
			return saturate()
		}
		return int(-int64(u-1) - 1), false, true, nil
	}
	if u > uint64(math.MaxInt) {
		return saturate()
	}
	return int(u), false, false, nil
}

// MarshalJSON writes the count as the schema wrote it when it was saturated,
// and as an integer otherwise.
func (f FlexInt) MarshalJSON() ([]byte, error) {
	if f.literal != "" {
		return []byte(f.literal), nil
	}
	return json.Marshal(f.n)
}

// Int returns the count as compared: the number itself, or MaxInt (MinInt) for
// one too large (too far below zero) for an int. See FlexInt.
func (f FlexInt) Int() int { return f.n }

// Saturated reports whether the schema wrote a number an int cannot hold, so
// that Int is its saturated stand-in rather than the number itself.
func (f FlexInt) Saturated() bool { return f.literal != "" }

// String returns the number the schema wrote: the literal for a saturated
// count, the decimal otherwise. It is what an error message states.
func (f FlexInt) String() string {
	if f.literal != "" {
		return f.literal
	}
	return strconv.Itoa(f.n)
}

// Number is the value of a numeric keyword -- minimum, maximum, multipleOf and
// their relatives -- kept exactly as the schema wrote it.
//
// JSON has one number type and no precision limit; float64 has both. Reading
// these keywords into a float64 loses the difference between 2^63-1 and 2^63
// before the generator has seen either, and every consumer downstream then
// works from the rounded value: a bound re-emitted as its shortest decimal is
// a *different* number to big.Float, and an integer const that fits int64
// exactly comes back as a float literal that will not compile. Keeping the
// literal means each consumer decides for itself what the number has to become
// -- a float64 comparison, an int64 constant, an arbitrary-precision bound --
// rather than being handed one reading of it.
//
// It is a distinct type rather than json.Number so that it can refuse a JSON
// string: json.Number is a string underneath and would take {"minimum": "5"},
// which the float64 field it replaces rejected.
type Number string

// UnmarshalJSON stores the number as written.
//
// Precision comes from the literal; range still comes from float64. A keyword
// whose magnitude overflows float64 -- {"minimum": 1e400} -- is refused, which
// is what the float64 field this replaces did, and refusing it here is what
// keeps the generator from writing a constant into Go source that the Go
// compiler then rejects. Nothing is lost that was ever held: no draft's
// numeric keyword had a reading beyond float64's range before this type
// existed.
func (n *Number) UnmarshalJSON(data []byte) error {
	trimmed := trimJSONWhitespace(data)
	if trimmed == "" || trimmed[0] == '"' {
		return fmt.Errorf("expected a number, got: %s", string(data))
	}
	var jn json.Number
	if err := json.Unmarshal([]byte(trimmed), &jn); err != nil {
		return fmt.Errorf("expected a number, got: %s", string(data))
	}
	if _, err := strconv.ParseFloat(string(jn), 64); err != nil {
		return fmt.Errorf("number out of range: %s", string(data))
	}
	*n = Number(jn)
	return nil
}

// MarshalJSON writes the literal back out unchanged, so a schema that is read
// and written again carries the same number it arrived with.
func (n Number) MarshalJSON() ([]byte, error) {
	if n == "" {
		return []byte("null"), nil
	}
	return []byte(n), nil
}

// String returns the literal as written.
func (n Number) String() string { return string(n) }

// Float64 returns the number as a float64, and reports whether it is
// representable as one. A literal whose magnitude overflows float64 (1e400) is
// not, and neither is an empty Number.
func (n Number) Float64() (float64, bool) {
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// Int64 returns the number as an int64 when it names an integer that int64
// holds exactly, and reports whether it does. The literal is read as an exact
// decimal, so 1e2, 100.0 and 100 all answer 100, and 9223372036854775807
// answers itself rather than the float64 it rounds to.
func (n Number) Int64() (int64, bool) {
	r, ok := n.Rat()
	if !ok || !r.IsInt() {
		return 0, false
	}
	num := r.Num()
	if !num.IsInt64() {
		return 0, false
	}
	return num.Int64(), true
}

// ratExponentLimit bounds how far a literal's exponent may reach before Rat
// refuses to build the number.
//
// big.Rat holds an exact decimal by writing out its digits, so 1e1000000000 is
// not a large number to it but a several-hundred-megabyte one. Nothing this
// package answers needs an exponent beyond float64's range, which stops at 308:
// a numeric keyword is refused past it by Number's own decode, and a const or
// enum member past it is compared as JSON text and never asked for its value.
// The limit is set well above 308 so that no question a caller can usefully ask
// is refused, and well below where the allocation matters.
const ratExponentLimit = 5000

// Rat reads the number as an exact rational, and reports whether it could.
//
// This is the exact reading every question about the number's *value* goes
// through -- is it an integer, does int64 hold it, is it larger than that other
// one -- because big.Rat parses the whole JSON number grammar without rounding
// any of it.
func (n Number) Rat() (*big.Rat, bool) {
	if i := strings.IndexAny(string(n), "eE"); i >= 0 {
		exp, err := strconv.Atoi(string(n)[i+1:])
		if err != nil || exp > ratExponentLimit || exp < -ratExponentLimit {
			return nil, false
		}
	}
	r, ok := new(big.Rat).SetString(string(n))
	if !ok {
		return nil, false
	}
	return r, true
}

// NumberFromFloat builds a Number from a float64, for a Schema assembled in Go
// rather than read from a document.
//
// A float64 that names an integer is written out in full rather than as its
// shortest round-tripping decimal, because those are different numbers to
// everything downstream that reads the literal exactly: -2^63 prints as
// -9.223372036854776e+18, and that decimal is 192 larger than the int64 it came
// from and does not fit in one.
func NumberFromFloat(f float64) Number {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return Number(strconv.FormatFloat(f, 'g', -1, 64))
	}
	if bf := new(big.Float).SetFloat64(f); bf.IsInt() {
		i, _ := bf.Int(nil)
		return Number(i.String())
	}
	return Number(strconv.FormatFloat(f, 'g', -1, 64))
}

// TypeList represents a JSON Schema "type" value, which can be either a single
// string (e.g. "string") or an array of strings (e.g. ["string", "null"]).
// Draft 3 also allows an array of schemas as type values; those schemas are
// preserved separately on Schema.TypeSchemas.
type TypeList []string

func (t *TypeList) UnmarshalJSON(data []byte) error {
	// A single type name. Checked by its first byte because encoding/json
	// decodes a JSON null into a string without complaint, as the empty string.
	if trimmed := trimJSONWhitespace(data); len(trimmed) > 0 && trimmed[0] == '"' {
		var single string
		if err := json.Unmarshal(data, &single); err != nil {
			return err
		}
		*t = TypeList{single}
		return nil
	}

	// Draft 3: an array whose entries are type names or schemas. The
	// schema-valued entries are captured by Schema.UnmarshalJSON, into
	// TypeSchemas; this keeps the names.
	//
	// An entry that is neither is refused rather than skipped. No dialect gives
	// one a meaning -- draft 3's entries are "a string or a schema", and draft 3
	// has no boolean schemas; every later draft takes names only -- and skipping
	// it is not neutral: {"type":[1,2]} came out as a schema with no type at all,
	// which admits every value, from a document that plainly meant to restrict
	// it. A malformed value is refused wherever it stands, as a null subschema
	// is; see Schema.MalformedKeywords.
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("type must be a string or array of strings: %s", string(data))
	}

	types := make([]string, 0, len(raw))
	for i, elem := range raw {
		trimmed := trimJSONWhitespace(elem)
		var s string
		switch {
		case json.Unmarshal(elem, &s) == nil && len(trimmed) > 0 && trimmed[0] == '"':
			types = append(types, s)
		case len(trimmed) > 0 && trimmed[0] == '{':
			// A draft 3 schema-valued entry; see Schema.TypeSchemas.
		default:
			// Placed at the entry, so a refusal names where it is.
			return atPath(fmt.Errorf("must be a type name or a schema, got: %s", abbreviateJSON(trimmed)), strconv.Itoa(i))
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

	// src is where the document wrote the boolean form, which the node
	// AsSchema materializes is located at. See Schema.SourceLocation.
	src *source
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
		if s.src != nil {
			s.boolSchema.src = *s.src
		}
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
	Number *Number
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
	var n Number
	if err := n.UnmarshalJSON(data); err != nil {
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

// RequiredList is the draft 4+ "required" keyword: the names of the properties
// an object must have.
//
// Draft 3's spelling of the same idea -- a boolean on the property itself -- is
// a different value of the same keyword and is held on Schema.Draft3Required,
// not here. It used to be folded into this list as a magic member,
// "\x00__draft3_required_true__", and a list of property names cannot carry an
// in-band marker safely, because every string is a legal property name: where
// the marker was not promoted away (at the root of a draft-3 document, or under
// additionalProperties with no $schema) it reached the generator as a required
// property of that name, and the type refused every object; and a 2020-12
// document that really names a property "\x00__draft3_required_true__" had its
// requirement read as draft 3's boolean and silently gated away.
type RequiredList []string

func (r *RequiredList) UnmarshalJSON(data []byte) error {
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil || arr == nil {
		return fmt.Errorf("required must be an array of strings: %s", string(data))
	}
	*r = RequiredList(arr)
	return nil
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
	Properties map[string]*Schema `json:"properties,omitempty"`
	Required   RequiredList       `json:"required,omitempty"`

	// Draft3Required is draft 3's spelling of "required": a boolean on the
	// property's own schema, where draft 4 and later write an array of names on
	// the parent. Normalize moves a true one onto the parent's Required and
	// clears every one, so nothing past Normalize sees it. It is a field of its
	// own, not a member of Required: see RequiredList for what the in-band
	// marker it replaces did.
	Draft3Required       *bool              `json:"-"`
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
	Minimum          *Number        `json:"minimum,omitempty"`
	Maximum          *Number        `json:"maximum,omitempty"`
	ExclusiveMinimum *SchemaOrFloat `json:"exclusiveMinimum,omitempty"`
	ExclusiveMaximum *SchemaOrFloat `json:"exclusiveMaximum,omitempty"`
	MultipleOf       *Number        `json:"multipleOf,omitempty"`

	// Enum and const
	Enum        []any `json:"enum,omitempty"`
	Const       *any  `json:"const,omitempty"`
	ConstIsNull bool  `json:"-"` // true when the schema has {"const": null}; Go's json.Unmarshal leaves *any nil for null
	Default     *any  `json:"default,omitempty"`

	// Metadata
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`

	// The rest of the annotation vocabulary. Pointers because the question each
	// answers is three-valued: a generator has to tell "the schema says false"
	// from "the schema does not say", and only the first of those is a statement
	// worth writing into the generated source.
	//
	// None of them affects a validation verdict, in any draft. From 2019-09 they
	// are the meta-data vocabulary and are annotations by definition; in draft 7
	// they are described as hints to a user agent. A generator is the consumer
	// they were written for, which is why they are read here at all -- and it is
	// also why nothing downstream of this struct may let one reach Validate().
	//
	// "examples" is deliberately not among them. See Examples.
	Deprecated *bool `json:"deprecated,omitempty"` // Draft 2019-09+
	ReadOnly   *bool `json:"readOnly,omitempty"`   // Draft 7+
	WriteOnly  *bool `json:"writeOnly,omitempty"`  // Draft 7+

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
	DivisibleBy *Number         `json:"divisibleBy,omitempty"` // precursor to multipleOf

	// Draft 4/6/7: dependencies (object where values are schemas or string arrays)
	Dependencies json.RawMessage `json:"dependencies,omitempty"`

	// The parsed forms of the three keywords above that hold subschemas. The raw
	// fields are the document's bytes, kept so that the schema marshals back to
	// what it said; these are the same values read as schemas, and they are read
	// when the document is -- not later, when Normalize rewrites them into the
	// keywords that replaced them.
	//
	// The timing is the point. The dialect pass clears, on every node, the
	// keywords that node's dialect does not define, and it can only reach a node
	// that exists. While these keywords stayed raw until the rewrite, the
	// subschemas inside them did not exist when the pass ran, so they were never
	// gated at all: a draft-4 document's {"dependencies":{"a":{"properties":
	// {"b":{"const":1}}}}} enforced a const draft 4 does not have. Parsed here,
	// they are ordinary children (see eachChild) and are gated under their own
	// dialect like any other.
	//
	// DisallowSchemas holds one schema per entry of "disallow", a type name
	// becoming {"type": name}. DependencySchemas and DependencyRequired split
	// "dependencies" by entry shape, as Normalize will: a schema, or the names
	// that must be present (draft 3's single bare name included).
	ExtendsSchemas     []*Schema           `json:"-"`
	DisallowSchemas    []*Schema           `json:"-"`
	DependencySchemas  map[string]*Schema  `json:"-"`
	DependencyRequired map[string][]string `json:"-"`

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

	// malformed records, by keyword, a value this node's document wrote that
	// is not a legal value of the keyword. See MalformedKeywords.
	malformed map[string]error

	// normalized is set once Normalize has rewritten this node. See
	// NormalizeForDraft for why a rewritten node is not read again.
	normalized bool

	// srcChildren is every subschema this node's object holds, at the JSON
	// Pointer reference tokens that lead to it from this node, as the decode
	// found them -- before Normalize moves any of them. It is what locates each
	// node (src; see source.go), and what the resolver answers a pointer into
	// a rewritten keyword from: after Normalize the document's own location of
	// such a subschema ("#/dependencies/a") no longer exists in the tree -- it
	// is under "dependentSchemas", "allOf" or "not" now -- but a $ref written
	// against the document names it there. See LocalResolver.walkPath.
	srcChildren []srcChild

	// src is where the document wrote this node. See SourceLocation.
	src source

	// droppedKeywords holds, as JSON, the value of each keyword the dialect
	// pass cleared because the node's dialect does not define it. Such a
	// keyword is unknown to that dialect, and a pointer reaches an unknown
	// keyword's value (through Extensions, for a keyword this package has no
	// field for); this keeps the same true of one it does -- draft 3's "#/not".
	droppedKeywords map[string]json.RawMessage

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

	// RetrievalURI is the URI this document was actually retrieved from, set on
	// the root of a document a resolver fetched. It is the base URI of a document
	// that declares no $id of its own (RFC 3986 §5.1.3, which draft 2020-12
	// §9.1.1 defers to), and for HTTP that is the effective request URI *after*
	// redirects -- not the URI the reference asked for. Carried here because the
	// two are established in different packages: only the HTTP layer knows which
	// URL answered, and only the generator computes base URIs for what it was
	// handed. A document declaring $id is unaffected, since ComputeBaseURIs lets
	// the $id override whatever base it is given. Nil for a document nothing
	// fetched. Issue #315.
	RetrievalURI *url.URL `json:"-"`
}

// knownSchemaKeys is the set of JSON property names that correspond to struct
// fields on Schema. Anything else is captured in Extensions. Built at init time
// via reflection so it stays in sync with the struct definition automatically.
var knownSchemaKeys map[string]bool

// knownSchemaKeyOrder is knownSchemaKeys as a sorted list, for anything that
// has to visit every keyword in an order that does not change between runs.
var knownSchemaKeyOrder []string

// marshaledKeywordField describes one Schema field in the terms MarshaledKeywords
// needs: the JSON key the encoder writes for it, where to find it, and the
// conditions under which the encoder leaves it out.
//
// omitZero is read with reflect.Value.IsZero, which is what encoding/json does
// for every type that does not carry an IsZero method of its own. No field on
// Schema is tagged omitzero at all today; if one ever is, and its type has such
// a method, TestMarshaledKeywordsMatchesMarshaling is what will say so.
type marshaledKeywordField struct {
	index     int
	key       string
	omitEmpty bool
	omitZero  bool
}

// marshaledKeywordFields is every field the encoder can write, in struct order.
// Like knownSchemaKeys it is built from the json tags themselves, so a keyword
// added as a struct field joins it without anyone having to remember to.
var marshaledKeywordFields []marshaledKeywordField

func init() {
	knownSchemaKeys = make(map[string]bool)
	t := reflect.TypeOf(Schema{})
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		// Strip ",omitempty" etc.
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		knownSchemaKeys[name] = true
		knownSchemaKeyOrder = append(knownSchemaKeyOrder, name)
		schemaKeywordFields = append(schemaKeywordFields, schemaKeywordField{index: i, key: name, typ: t.Field(i).Type})
		marshaledKeywordFields = append(marshaledKeywordFields, marshaledKeywordField{
			index:     i,
			key:       name,
			omitEmpty: slices.Contains(strings.Split(opts, ","), "omitempty"),
			omitZero:  slices.Contains(strings.Split(opts, ","), "omitzero"),
		})
	}
	slices.Sort(knownSchemaKeyOrder)
}

// isEmptyForJSON is encoding/json's isEmptyValue, which is what decides whether
// an omitempty field is written. It is reproduced rather than approximated: the
// whole value of MarshaledKeywords is that it answers what a marshal would have
// answered, and TestMarshaledKeywordsMatchesMarshaling holds the two together
// field by field.
func isEmptyForJSON(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}

// MarshaledKeywords returns the set of top-level keys s.MarshalJSON would write,
// and reports whether s has such a form at all.
//
// The second result is false for a nil schema and for a boolean schema, which
// marshals to `true` or `false` and so has no keys. Neither may be read as "this
// schema states nothing": the key set is unknown, not empty. A boolean schema
// states a great deal -- `false` admits no value -- and every caller asks
// IsBooleanSchema for that.
//
// This is the reading every gate that decides "can this schema be represented"
// and "does this schema state anything" is built on, so what it must not do is
// miss a keyword. It is derived from the struct's own json tags, exactly as
// knownSchemaKeys is, which is the property that makes it fail closed: a keyword
// this package learns later arrives as a tagged field and lands in the set on
// its own. A list written by hand has the opposite default -- the field nobody
// remembered is missed silently -- and that is why the set has never been one.
//
// It is computed rather than serialized, and that is not an optimization detail
// but the fix for issue #233. The obvious implementation, json.Marshal followed
// by a decode into map[string]json.RawMessage, costs the size of the node's
// whole *subtree* for an answer about the node alone; and because Schema has a
// MarshalJSON of its own, encoding/json re-validates each level's output into
// its parent's buffer, so one such marshal of a chain of depth d is already
// O(d^2). Asking it once per node made the generator cubic in nesting depth: the
// 2000-deep `not` under testdata/schemas/adversarial/deep took five seconds to
// generate, past the ten-second per-input deadline Go's fuzzing worker enforces
// once the binary is coverage-instrumented, which killed the worker and left the
// whole fuzz gate unable to get past its seed corpus. Reading the keys off the
// struct is O(1) per node and answers the same question.
//
// What it cannot show is a field whose *presence* the encoding erases, and
// KeywordsMarshaledFormOmits is the one place that knows which those are. Every
// gate reads both.
func (s *Schema) MarshaledKeywords() (map[string]bool, bool) {
	if s == nil || s.BooleanSchema != nil {
		return nil, false
	}
	v := reflect.ValueOf(s).Elem()
	present := make(map[string]bool, len(marshaledKeywordFields))
	for _, f := range marshaledKeywordFields {
		fv := v.Field(f.index)
		if f.omitEmpty && isEmptyForJSON(fv) {
			continue
		}
		if f.omitZero && fv.IsZero() {
			continue
		}
		present[f.key] = true
	}
	return present, true
}

// Examples returns the "examples" annotation as the raw JSON of each element,
// or nil when the schema has none.
//
// It reads Extensions rather than a field of its own, and that is load-bearing
// rather than an omission. knownSchemaKeys is built by reflecting over the json
// tags on this struct, so a field tagged "examples" would take the keyword out
// of Extensions -- and Extensions is how the resolver reaches a JSON Pointer
// into an unknown keyword. "#/examples/0" is exactly that pointer, and the
// official suite's optional/refOfUnknownKeyword group resolves it in draft
// 2019-09, 2020-12 and v1, as do two seeds under testdata/schemas/adversarial.
// A field here would trade three groups of working $ref resolution for an
// annotation that changes no verdict, so the annotation is read the other way
// round instead.
//
// A non-array "examples" annotates nothing and is reported as nothing: the
// keyword is defined over an array, and a scalar there is a schema saying
// something this function has no reading of.
func (s *Schema) Examples() []json.RawMessage {
	raw, ok := s.Extensions["examples"]
	if !ok {
		return nil
	}
	var out []json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// annotationBool reads a three-valued annotation flag as the two-valued question
// a generator actually asks: does the schema assert this? An absent keyword and
// an explicit false are the same answer, and neither is worth emitting.
func annotationBool(b *bool) bool { return b != nil && *b }

// IsDeprecated reports whether the schema asserts "deprecated": true.
func (s *Schema) IsDeprecated() bool { return annotationBool(s.Deprecated) }

// IsReadOnly reports whether the schema asserts "readOnly": true.
func (s *Schema) IsReadOnly() bool { return annotationBool(s.ReadOnly) }

// IsWriteOnly reports whether the schema asserts "writeOnly": true.
func (s *Schema) IsWriteOnly() bool { return annotationBool(s.WriteOnly) }

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

	// If this schema declares a scope-changing $id, it establishes a new base URI
	// and document root.
	//
	// A plain-name fragment id -- the pre-2019-09 {"id": "#name"} spelling of
	// what became $anchor -- is not one of those: it names the subschema inside
	// the *current* scope and leaves the base URI alone. Treating it as a scope
	// change gave the node a BaseURI carrying a fragment, made it its own
	// document root, and so hid it from the resource graph's anchor walk, which
	// stops at a nested resource -- while the resolver's own walk, which asks
	// changesScope, descended into it and found the anchor. Two walks, two
	// answers, for the identical document; see AnchorNames on why that shape is
	// not allowed to stand (issue #307).
	//
	// Which ids those are is scopeID's answer, and the resolver's anchor walk
	// asks the same function: an id this walk could not parse used to be no
	// scope change here and one there, which is the same two-walks shape again.
	if idURL, ok := scopeID(s); ok {
		if currentBase != nil {
			currentBase = currentBase.ResolveReference(idURL)
		} else {
			currentBase = idURL
		}
		// A schema with $id becomes the document root for its scope.
		currentDocRoot = s
	}

	s.BaseURI = currentBase
	s.DocumentRoot = currentDocRoot

	// Recurse into all child schemas.
	// maporder: each member heads its own subtree, and the visit writes only into the subtree it is handed.
	for _, sub := range s.Properties {
		sub.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	for _, sub := range s.TypeSchemas {
		sub.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	// maporder: each member heads its own subtree, and the visit writes only into the subtree it is handed.
	for _, sub := range s.PatternProperties {
		sub.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	// maporder: each member heads its own subtree, and the visit writes only into the subtree it is handed.
	for _, sub := range s.Definitions {
		sub.ComputeBaseURIs(currentBase, currentDocRoot)
	}
	// maporder: each member heads its own subtree, and the visit writes only into the subtree it is handed.
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
	// maporder: each member heads its own subtree, and the visit writes only into the subtree it is handed.
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
	// different nodes, so keying on the keyword alone would alias them. The
	// key is the path's canonical pointer, not its tokens joined on "/": a
	// token may itself hold a "/", so "#/x/a~1b" (one token, "a/b") and
	// "#/x/a/b" (two) joined to one key and the second ref was handed the
	// first one's node.
	cacheKey := PointerFragment(append([]string{key}, tokens...)...)
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
	// The extension's subschema is read under the dialect of the node it sits
	// in, unless it declares its own -- the rule every other subschema follows.
	// Normalizing it as a document of its own read it under no dialect at all,
	// so a draft-4 document's "#/x-vendor" target enforced a const draft 4 does
	// not have.
	d := s.DetectedDraft
	if own := DetectDraft(&sub); own != DraftUnknown {
		d = own
	}
	// It is located inside the keyword's value, in this node's document, before
	// Normalize would take it for the root of a document of its own. Under a
	// node that has no location, it has none either (see unlocated): it is not
	// the root of a document, and naming it as one would be a wrong location.
	if s.src.set {
		s.placeAt(&sub, append([]string{key}, tokens...)...)
	} else {
		sub.src = unlocated
	}
	sub.locateChildren()
	sub.NormalizeForDraft(d)
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
// encoding erases, and there are exactly four:
//
//   - Enum is tagged omitempty, so `"enum": []` -- the schema that admits no
//     value at all -- marshals to nothing and reads as a schema that states
//     nothing.
//   - ConstIsNull is tagged "-", because encoding/json leaves a *any nil for a
//     JSON null and the flag is the only record that `"const": null` was written.
//   - TypeSchemas is tagged "-", and holds the draft 3 schema-valued entries of a
//     "type" array. A schema whose whole type list is schema-valued marshals with
//     no "type" at all.
//   - Draft3Required is tagged "-", and holds draft 3's boolean "required".
//     Normalize consumes it, so only a schema read without normalizing has one.
//
// So the two are read together: the marshaled set for everything it can show,
// this for the four it cannot. Reading the marshaled set alone is what let
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
	if s.Draft3Required != nil {
		hidden = append(hidden, "required")
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
