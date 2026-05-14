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
	case "string":
		if s, ok := defaultVal.(string); ok {
			if s == "" {
				return "" // zero value, no-op
			}
			return strconv.Quote(s)
		}
	case "int64":
		switch v := defaultVal.(type) {
		case float64:
			// JSON numbers are always float64 from json.Unmarshal.
			intVal := int64(v)
			if intVal == 0 {
				return "" // zero value, no-op
			}
			return fmt.Sprintf("%d", intVal)
		case int:
			if v == 0 {
				return ""
			}
			return fmt.Sprintf("%d", v)
		}
	case "float64":
		switch v := defaultVal.(type) {
		case float64:
			if v == 0 {
				return "" // zero value, no-op
			}
			return strconv.FormatFloat(v, 'f', -1, 64)
		case int:
			if v == 0 {
				return ""
			}
			return fmt.Sprintf("%d.0", v)
		}
	case "bool":
		if b, ok := defaultVal.(bool); ok {
			if !b {
				return "" // zero value (false), no-op
			}
			return "true"
		}
	}

	// For complex types (arrays, maps, named types), we don't generate defaults.
	// This keeps the implementation simple and avoids generating invalid code.
	return ""
}
