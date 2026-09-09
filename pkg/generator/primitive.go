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

// rawElementSlice reports whether a Go type is a slice of json.RawMessage,
// through a pointer if there is one: the shape an array of untyped elements has
// under Config.RawUntyped, and the one shape whose elements a uniqueItems or a
// contains check must compare as JSON values rather than as bytes.
//
// Asked of the type rather than of the schema, for the reason isExactNumberType
// gives: the several places that build such a check reach the element's Go type
// by different routes, and the type is the one answer they share.
func rawElementSlice(t GoType) bool {
	if pt, ok := t.(*PointerType); ok {
		t = pt.Inner
	}
	at, ok := t.(*ArrayType)
	if !ok {
		return false
	}
	prim, ok := at.ItemType.(*PrimitiveType)
	return ok && prim.Name == GoRawTypeName
}

// markRawElementRules sets RawElements on the uniqueItems rule of a position
// whose elements are held as json.RawMessage, so that the emitted check reduces
// each element to its canonical JSON text before comparing.
//
// json.Marshal of a RawMessage keeps the bytes as written, and the bytes are
// not the value: [1, 1.0] and [{"a":1,"b":2},{"b":2,"a":1}] are each two
// spellings of one element, which uniqueItems is defined to refuse. The check
// on an `any` element gets the reduction for free from the decode; a raw
// element has to ask for it. Called from each place that has both the rules and
// the Go type they will be emitted against, on the same terms as
// markExactNumberRules -- but with the failure mode reversed: a position this
// misses compiles, and compares bytes. That is why the emitted branch takes the
// marshalled bytes rather than the element, so it is right for an `any` element
// as well, and why the position matrix in tests/ puts a uniqueItems beside an
// untyped element in each shape this is called for: a property, an alias, and
// an element that is itself an array.
func markRawElementRules(rules []ValidationRule, t GoType) {
	if !rawElementSlice(t) {
		return
	}
	for i := range rules {
		markRawElementRule(&rules[i], t)
	}
}

// markRawElementRule is markRawElementRules for one rule.
func markRawElementRule(r *ValidationRule, t GoType) {
	if r.RuleType == "uniqueItems" && rawElementSlice(t) {
		r.RawElements = true
	}
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
