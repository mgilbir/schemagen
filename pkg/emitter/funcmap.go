package emitter

import (
	"fmt"
	"strings"
	"text/template"
	"unicode"
	"unicode/utf8"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// FuncMap returns the template.FuncMap used by the emitter templates.
//
// Key functions:
//   - "goType":         takes a GoType interface value (as any) and returns its Go type string
//   - "enumValue":      formats an enum value as a Go literal (quotes strings, etc.)
//   - "receiverName":   takes a type name and returns a 1-char lowercase receiver name
//   - "lowerFirst":     lowercases the first character of a string
//   - "add":            adds two ints (useful in templates)
//   - "wrapTypeDef":    wraps a TypeDef for template type-dispatch
//   - "mkOneOfCtx":     creates a context map for oneOf templates
//   - "isOneOfRequired": returns true if the given oneOf field is required on its parent struct
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"goType":       goTypeFunc,
		"enumValue":    enumValueFunc,
		"receiverName": receiverNameFunc,
		"lowerFirst":   lowerFirstFunc,
		"add":          addFunc,
		"wrapTypeDef":  wrapTypeDefFunc,
		"mkOneOfCtx":   mkOneOfCtxFunc,
		"isOneOfRequired": func(oof any) bool {
			if o, ok := oof.(generator.OneOfDef); ok {
				return o.Required
			}
			if o, ok := oof.(*generator.OneOfDef); ok {
				return o.Required
			}
			return false
		},
		"requiredFieldsList": requiredFieldsListFunc,
		"hasRequiredFields":  func(fields []string) bool { return len(fields) > 0 },
		"isRawMessage":       isRawMessageFunc,
		"goStringLiteral":    goStringLiteralFunc,
		"goStringQuote":      goStringQuoteFunc,
		"hasManualFields":    hasManualFieldsFunc,
		"ppTypeValue":        ppTypeValueFunc,
		"deref":              derefIntFunc,
		"validationFeatures": validationFeaturesFunc,
		"stringList":         stringListFunc,
	}
}

func validationFeaturesFunc(features []generator.ValidationFeature) string {
	if len(features) == 0 {
		return "nil"
	}
	parts := make([]string, len(features))
	for i, feature := range features {
		parts[i] = fmt.Sprintf("validationruntime.Feature(%q)", string(feature))
	}
	return "[]validationruntime.Feature{" + strings.Join(parts, ", ") + "}"
}

func stringListFunc(features []generator.ValidationFeature) string {
	if len(features) == 0 {
		return "nil"
	}
	parts := make([]string, len(features))
	for i, feature := range features {
		parts[i] = fmt.Sprintf("%q", string(feature))
	}
	return "[]string{" + strings.Join(parts, ", ") + "}"
}

// OneOfContext is passed to oneof_interface and oneof_getters templates.
type OneOfContext struct {
	OneOf      any // generator.OneOfDef
	ParentName string
}

// mkOneOfCtxFunc creates a context object for oneOf templates.
func mkOneOfCtxFunc(oneof any, parentName string) OneOfContext {
	return OneOfContext{OneOf: oneof, ParentName: parentName}
}

// goTypeFunc accepts any value that implements GoTypeName() string and returns the
// Go type name. This is needed because Go templates pass interface values as any.
func goTypeFunc(v any) string {
	if gt, ok := v.(interface{ GoTypeName() string }); ok {
		return gt.GoTypeName()
	}
	return fmt.Sprintf("%v", v)
}

// enumValueFunc formats an enum value as a Go literal.
// Strings are quoted, numeric types are left as-is.
func enumValueFunc(v any) string {
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("%q", val)
	case int:
		return fmt.Sprintf("%d", val)
	case int64:
		return fmt.Sprintf("%d", val)
	case float64:
		// If the float is an integer value, emit without decimal.
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// receiverNameFunc takes a type name and returns a single lowercase character
// suitable for use as a Go method receiver name.
func receiverNameFunc(name string) string {
	if name == "" {
		return "x"
	}
	r, _ := utf8.DecodeRuneInString(name)
	return strings.ToLower(string(r))
}

// lowerFirstFunc lowercases the first character of a string.
func lowerFirstFunc(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[size:]
}

// addFunc adds two integers.
func addFunc(a, b int) int {
	return a + b
}

// isRawMessageFunc returns true if the given GoType is json.RawMessage.
// Used in templates to avoid unnecessary unmarshal when capturing additional properties.
func isRawMessageFunc(v any) bool {
	if gt, ok := v.(interface{ GoTypeName() string }); ok {
		return gt.GoTypeName() == "json.RawMessage"
	}
	return false
}

// goStringLiteralFunc escapes a string for use inside a Go double-quoted string literal.
// This handles characters like double quotes and backslashes that would otherwise
// break the generated Go source code.
func goStringLiteralFunc(s string) string {
	// Use %q to get a properly quoted string, then strip the surrounding quotes.
	q := fmt.Sprintf("%q", s)
	return q[1 : len(q)-1]
}

// goStringQuoteFunc returns a Go quoted string literal (with surrounding quotes).
// This is useful in templates where backtick strings can't be used.
func goStringQuoteFunc(s string) string {
	return fmt.Sprintf("%q", s)
}

// hasManualFieldsFunc returns true if any FieldDef in the slice has ManualJSON set.
// Used in templates to add manual field handling in marshal/unmarshal methods.
func hasManualFieldsFunc(fields any) bool {
	if fs, ok := fields.([]generator.FieldDef); ok {
		for _, f := range fs {
			if f.ManualJSON {
				return true
			}
		}
	}
	return false
}

// ppTypeValueFunc extracts the type name from a patternProperties "ppType" validation
// rule value. The value can be a single string or a []string for multi-type.
// Returns the first (or only) type name as a string.
func ppTypeValueFunc(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case []string:
		if len(val) > 0 {
			return val[0]
		}
		return "any"
	default:
		return fmt.Sprintf("%v", val)
	}
}

// derefIntFunc dereferences an *int pointer for use in templates.
// Returns 0 if the pointer is nil.
func derefIntFunc(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

// requiredFieldsListFunc formats a list of required field names as Go string literals.
// e.g., ["radius"] → `"radius"`
// e.g., ["width", "height"] → `"width", "height"`
func requiredFieldsListFunc(fields []string) string {
	quoted := make([]string, len(fields))
	for i, f := range fields {
		quoted[i] = fmt.Sprintf("%q", f)
	}
	return strings.Join(quoted, ", ")
}
