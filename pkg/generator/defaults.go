package generator

import (
	"fmt"
	"strconv"
)

// defaultToGoLiteral converts a JSON Schema default value to a Go literal string
// appropriate for the given Go type. Returns empty string if conversion is not possible
// or if the default value is the zero value for the type (which would be a no-op).
func defaultToGoLiteral(defaultVal any, goType GoType) string {
	if defaultVal == nil || goType == nil {
		return ""
	}

	typeName := goType.GoTypeName()

	switch typeName {
	case "string", "*string":
		if s, ok := defaultVal.(string); ok {
			if typeName == "string" && s == "" {
				return "" // zero value, no-op (for non-pointer)
			}
			return strconv.Quote(s)
		}
	case "int64", "*int64":
		switch v := defaultVal.(type) {
		case float64:
			// JSON numbers are always float64 from json.Unmarshal.
			intVal := int64(v)
			if typeName == "int64" && intVal == 0 {
				return "" // zero value, no-op (for non-pointer)
			}
			return fmt.Sprintf("%d", intVal)
		case int:
			if typeName == "int64" && v == 0 {
				return ""
			}
			return fmt.Sprintf("%d", v)
		}
	case "float64", "*float64":
		switch v := defaultVal.(type) {
		case float64:
			if typeName == "float64" && v == 0 {
				return "" // zero value, no-op (for non-pointer)
			}
			return strconv.FormatFloat(v, 'f', -1, 64)
		case int:
			if typeName == "float64" && v == 0 {
				return ""
			}
			return fmt.Sprintf("%d.0", v)
		}
	case "bool", "*bool":
		if b, ok := defaultVal.(bool); ok {
			if typeName == "bool" && !b {
				return "" // zero value (false), no-op (for non-pointer)
			}
			return strconv.FormatBool(b)
		}
	}

	// For complex types (arrays, maps, named types), we don't generate defaults.
	// This keeps the implementation simple and avoids generating invalid code.
	return ""
}
