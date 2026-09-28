package generator

// PrimitiveTypeFromSchema maps a JSON Schema type string to the corresponding
// Go PrimitiveType.
//
// Mapping:
//
//	"string"  → string
//	"integer" → int64
//	"number"  → float64
//	"boolean" → bool
//	"null"    → nil (caller should handle with PointerType)
//	"object"  → map[string]any (object with no properties)
//	"array"   → []any (array with no items schema)
//
// Returns nil for "null" since it is handled specially by the caller.
func PrimitiveTypeFromSchema(schemaType string) GoType {
	switch schemaType {
	case "string":
		return &PrimitiveType{Name: "string"}
	case "integer":
		return &PrimitiveType{Name: "int64"}
	case "number":
		return &PrimitiveType{Name: "float64"}
	case "boolean":
		return &PrimitiveType{Name: "bool"}
	case "null":
		return nil
	case "object":
		return &MapType{
			KeyType:   &PrimitiveType{Name: "string"},
			ValueType: &PrimitiveType{Name: "any"},
		}
	case "array":
		return &ArrayType{
			ItemType: &PrimitiveType{Name: "any"},
		}
	default:
		return &PrimitiveType{Name: "any"}
	}
}

// GoNumberTypeName is the Go type a "number" is held as under
// Config.ExactNumbers: encoding/json's json.Number, which is the literal the
// document wrote and so has not decided how much of the number to keep.
const GoNumberTypeName = "json.Number"

// primitiveTypeFromSchema is PrimitiveTypeFromSchema read through this
// generator's configuration.
//
// One mapping depends on it. "number" is a float64 by default and a
// json.Number under Config.ExactNumbers, and every position that types a
// schema goes through here so that the two cannot disagree about which -- a
// property, an array element, a map value, a oneOf variant and an alias's
// underlying are all the same question asked at different places.
//
// The exported function keeps the mapping it always had, for the callers that
// are asking about the *shape* of a type rather than choosing one.
func (g *Generator) primitiveTypeFromSchema(schemaType string) GoType {
	if schemaType == "number" && g != nil && g.config.ExactNumbers {
		return &PrimitiveType{Name: GoNumberTypeName}
	}
	// An object with no property schema at all. Its members are positions the
	// schema gives no type to, exactly as {"additionalProperties":{}} would
	// make them, so under Config.RawUntyped they are held the same way. The
	// "array" mapping is deliberately not read through the flag: []any is
	// what a tuple and an array carrying unevaluatedItems are held as, and
	// the checks on those read each element as a decoded value. See
	// Config.RawUntyped for the boundary.
	if schemaType == "object" && g != nil && g.config.RawUntyped {
		return &MapType{
			KeyType:   &PrimitiveType{Name: "string"},
			ValueType: &PrimitiveType{Name: GoRawTypeName},
		}
	}
	return PrimitiveTypeFromSchema(schemaType)
}

// GoRawTypeName is the Go type a position the schema gives no type to is held
// as under Config.RawUntyped: encoding/json's json.RawMessage, which is the
// document's own bytes and so has not decided anything about the value.
const GoRawTypeName = "json.RawMessage"

// untypedType is the Go type for a position whose schema states nothing: `any`
// by default, and json.RawMessage under Config.RawUntyped.
//
// It is asked only where `any` is what the schema *means* -- the fallback every
// arm of resolveType declined, an alias over a schema with no keywords, a
// reference cycle with no content. An `any` that stands for something the
// generator could not compile is not built here and keeps its type under either
// setting, so that the flag round-trips what the schema left open without
// hiding what the generator gave up on. See Config.RawUntyped.
func (g *Generator) untypedType() GoType {
	if g != nil && g.config.RawUntyped {
		return &PrimitiveType{Name: GoRawTypeName}
	}
	return &PrimitiveType{Name: "any"}
}

// numberRuleTypes names the rules whose emitted check reads its instance as a
// number. Every other rule is about a string, a collection or a key set and is
// unaffected by how a number is held.
var numberRuleTypes = map[string]bool{
	"minimum":          true,
	"maximum":          true,
	"exclusiveMinimum": true,
	"exclusiveMaximum": true,
	"multipleOf":       true,
	"const":            true,
}

// markNumberRules sets NumOperand on the numeric rules of a position from the
// Go type the position's value is held as.
//
// It is called from each place that has both the rules and the Go type they
// will be emitted against. A place this misses leaves NumOperandAny, whose
// check reads the number through the core whatever it is held as -- correct,
// and slower -- so a hand-maintained list of call sites cannot make a verdict
// wrong. What it can make is a fast comparison, and only from a type that
// supports one.
func markNumberRules(rules []ValidationRule, t GoType) {
	for i := range rules {
		markNumberRule(&rules[i], t)
	}
}

// markNumberRule is markNumberRules for one rule, where the caller holds a
// single rule rather than the slice it will end up in.
func markNumberRule(r *ValidationRule, t GoType) {
	if !numberRuleTypes[r.RuleType] {
		return
	}
	// A const that is not a number is decided by its canonical text, and no
	// representation of a number changes that: a number is not equal to a
	// string or an object under any reading.
	if r.RuleType == "const" && r.ExactValue == "" {
		return
	}
	r.NumOperand = numOperandOf(t)
}

// clearNumberOperands resets every rule to NumOperandAny, for a position whose
// check is handed the number as something other than the Go type the rules
// were marked from -- a literal the wrapper kept, a big-int wrapper.
func clearNumberOperands(rules []ValidationRule) {
	for i := range rules {
		rules[i].NumOperand = NumOperandAny
	}
}

// numOperandOf maps a Go type to the NumOperandKind its checks are written for,
// through any number of pointers. Only the three primitive number types have a
// form of their own; everything else is read through the core.
func numOperandOf(t GoType) NumOperandKind {
	switch v := t.(type) {
	case *PrimitiveType:
		switch v.Name {
		case "int64":
			return NumOperandInt64
		case "float64":
			return NumOperandFloat64
		case GoNumberTypeName:
			return NumOperandJSONNumber
		}
	case *PointerType:
		return numOperandOf(v.Inner)
	}
	return NumOperandAny
}

// isExactNumberType reports whether a Go type is the json.Number a "number"
// becomes under Config.ExactNumbers, through any number of pointers.
//
// The question is asked of the type rather than of the schema deliberately.
// Whether a comparison can be made exactly is a fact about what the value is
// held as, and the several places that build a numeric rule reach a Go type by
// different routes -- a property, an array element, a map value, an alias's own
// underlying. Asking the type is the one answer all of them share.
func isExactNumberType(t GoType) bool {
	switch v := t.(type) {
	case *PrimitiveType:
		return v.Name == GoNumberTypeName
	case *PointerType:
		return isExactNumberType(v.Inner)
	}
	return false
}
