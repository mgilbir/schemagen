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
	return PrimitiveTypeFromSchema(schemaType)
}

// exactNumberRuleTypes names the rules whose emitted check reads its instance
// as a number, and so has to be made on the literal when the instance is one
// held exactly. Every other rule is about a string, a collection or a key set
// and is unaffected by how a number is held.
var exactNumberRuleTypes = map[string]bool{
	"minimum":          true,
	"maximum":          true,
	"exclusiveMinimum": true,
	"exclusiveMaximum": true,
	"multipleOf":       true,
	"const":            true,
}

// markExactNumberRules sets ExactCompare on the numeric rules of a position
// whose value is held as a json.Number.
//
// It is called from each place that has both the rules and the Go type they
// will be emitted against, which is a list of places -- the failure mode is
// what makes that acceptable here. A position this misses keeps `float64(x)`
// against a json.Number, and that does not compile: the omission is a build
// failure at the first schema that reaches it, not a check that goes on
// answering from a rounded value. Nothing about it can be silently wrong, which
// is the property a hand-maintained list has to have to be allowed at all.
func markExactNumberRules(rules []ValidationRule, t GoType) {
	if !isExactNumberType(t) {
		return
	}
	for i := range rules {
		markExactNumberRule(&rules[i], t)
	}
}

// markExactNumberRule is markExactNumberRules for one rule, where the caller
// holds a single rule rather than the slice it will end up in.
func markExactNumberRule(r *ValidationRule, t GoType) {
	if !isExactNumberType(t) || !exactNumberRuleTypes[r.RuleType] {
		return
	}
	// A const that is not a number has no literal to compare against, and the
	// general check is already right for it: a number is not equal to a string
	// or to an object under any reading. {"type":"number","const":"1.5"} is the
	// schema that reaches this, and it forbids every value the field can hold.
	if r.RuleType == "const" && r.ExactValue == "" {
		return
	}
	r.ExactCompare = true
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
