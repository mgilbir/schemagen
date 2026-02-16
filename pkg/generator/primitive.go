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
