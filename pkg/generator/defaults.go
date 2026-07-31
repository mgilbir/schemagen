package generator

import (
	"fmt"
	"math"
	"strconv"
)

// defaultToGoLiteral converts a JSON Schema default value to a Go literal string
// appropriate for the given Go type. Returns empty string if conversion is not possible
// or if the default value is the zero value for the type (which would be a no-op).
//
// It returns an error when the default value cannot be represented in the target
// Go type without silently inventing a different value — currently, when an
// integer field carries a default with a fractional part (e.g. 4.5 for an
// "integer" type). Type-mismatched defaults (e.g. a string default on an integer
// field) remain silently skipped.
func defaultToGoLiteral(defaultVal any, goType GoType) (string, error) {
	if defaultVal == nil || goType == nil {
		return "", nil
	}

	typeName := goType.GoTypeName()

	switch typeName {
	case "string", "*string":
		if s, ok := defaultVal.(string); ok {
			if typeName == "string" && s == "" {
				return "", nil // zero value, no-op (for non-pointer)
			}
			return strconv.Quote(s), nil
		}
	case "int64", "*int64":
		switch v := defaultVal.(type) {
		case float64:
			// JSON numbers are always float64 from json.Unmarshal.
			if math.IsNaN(v) || v != math.Trunc(v) {
				return "", fmt.Errorf("default value %v is not an integer", v)
			}
			// Converting an out-of-range float64 to int64 is undefined in Go, so
			// reject rather than emit an arbitrary literal. -2^63 is exactly
			// representable and is a valid int64, hence the asymmetric bounds.
			const int64Bound = float64(1 << 63) // 2^63
			if v >= int64Bound || v < -int64Bound {
				return "", fmt.Errorf("default value %v does not fit in int64", v)
			}
			intVal := int64(v)
			if typeName == "int64" && intVal == 0 {
				return "", nil // zero value, no-op (for non-pointer)
			}
			return fmt.Sprintf("%d", intVal), nil
		case int:
			if typeName == "int64" && v == 0 {
				return "", nil
			}
			return fmt.Sprintf("%d", v), nil
		}
	case "float64", "*float64":
		switch v := defaultVal.(type) {
		case float64:
			if typeName == "float64" && v == 0 {
				return "", nil // zero value, no-op (for non-pointer)
			}
			return strconv.FormatFloat(v, 'f', -1, 64), nil
		case int:
			if typeName == "float64" && v == 0 {
				return "", nil
			}
			return fmt.Sprintf("%d.0", v), nil
		}
	case "bool", "*bool":
		if b, ok := defaultVal.(bool); ok {
			if typeName == "bool" && !b {
				return "", nil // zero value (false), no-op (for non-pointer)
			}
			return strconv.FormatBool(b), nil
		}
	}

	// For complex types (arrays, maps, named types), we don't generate defaults.
	// This keeps the implementation simple and avoids generating invalid code.
	return "", nil
}
