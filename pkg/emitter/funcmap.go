package emitter

import (
	"fmt"
	"strconv"
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
		"comment":      commentFunc,
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
		"requiredFieldsList":  requiredFieldsListFunc,
		"hasRequiredFields":   func(fields []string) bool { return len(fields) > 0 },
		"isRawMessage":        isRawMessageFunc,
		"goStringLiteral":     goStringLiteralFunc,
		"goStringQuote":       goStringQuoteFunc,
		"ecmaPattern":         ecmaPatternLiteralFunc,
		"dynNum":              dynNumFunc,
		"hasManualFields":     hasManualFieldsFunc,
		"ppTypeValue":         ppTypeValueFunc,
		"ppTypeValues":        ppTypeValuesFunc,
		"ppTypeValuesMsg":     ppTypeValuesMsgFunc,
		"deref":               derefIntFunc,
		"validationFeatures":  validationFeaturesFunc,
		"stringList":          stringListFunc,
		"validationValue":     validationValueFunc,
		"validationNonNil":    validationNonNilFunc,
		"validationStringSet": validationStringSetFunc,
		"jsonErrorName":       jsonErrorNameFunc,
		"mkItemCtx":           mkItemCtxFunc,
		"mkItemLevelCtx":      mkItemLevelCtxFunc,
		"itemRange":           itemRangeFunc,
		"itemElem":            itemElemFunc,
		"itemPath":            itemPathFunc,
		"itemArgs":            itemArgsFunc,
	}
}

// ItemValidationContext is passed to the item_validations template, which needs
// the receiver name alongside the definitions to render the slice expressions.
type ItemValidationContext struct {
	Recv string
	Defs []generator.ItemValidationDef
}

func mkItemCtxFunc(recv string, defs []generator.ItemValidationDef) ItemValidationContext {
	return ItemValidationContext{Recv: recv, Defs: defs}
}

// ItemLevelContext addresses one dimension of an ItemValidationDef. The
// item_level template recurses on it, so the level index has to travel with the
// definition and the receiver name.
type ItemLevelContext struct {
	Recv  string
	Def   generator.ItemValidationDef
	Level int
}

func mkItemLevelCtxFunc(recv string, def generator.ItemValidationDef, level int) ItemLevelContext {
	return ItemLevelContext{Recv: recv, Def: def, Level: level}
}

// itemRangeFunc renders what a level's loop ranges over: the slice itself at
// the outermost level, the enclosing level's element below that.
func itemRangeFunc(recv string, def generator.ItemValidationDef, level int) string {
	if level == 0 {
		expr := recv
		if def.FieldName != "" {
			expr += "." + def.FieldName
		}
		if def.IsPointer {
			return "*" + expr
		}
		return expr
	}
	return itemElemFunc(def, level-1)
}

// itemElemFunc renders a level's element, dereferenced when the element type is
// a pointer. The loop has already passed over a nil at that point.
func itemElemFunc(def generator.ItemValidationDef, level int) string {
	lv := def.Levels[level]
	if lv.ElemIsPointer {
		return "*" + lv.ElemVar
	}
	return lv.ElemVar
}

// itemPathFunc renders the error path down to a level, as a format string with
// one %d per dimension. An array alias has no property name to lead with, so it
// reports the position under the keyword that constrains it.
func itemPathFunc(def generator.ItemValidationDef, level int) string {
	name := "items"
	if def.JSONName != "" {
		name = jsonErrorNameFunc(def.JSONName)
	}
	return name + strings.Repeat("[%d]", level+1)
}

// itemArgsFunc renders the loop indices that fill in itemPathFunc's verbs.
func itemArgsFunc(def generator.ItemValidationDef, level int) string {
	parts := make([]string, level+1)
	for i := 0; i <= level; i++ {
		parts[i] = def.Levels[i].IndexVar
	}
	return strings.Join(parts, ", ")
}

// commentFunc renders text as the tail of a Go line comment. Text spanning
// several lines (schema descriptions may contain newlines) is continued with
// a "//" prefix at the given indent, so the emitted source stays valid Go
// instead of breaking out of the comment.
func commentFunc(indent, text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.ReplaceAll(text, "\n", "\n"+indent+"// ")
}

// jsonErrorNameFunc escapes a JSON property name for safe embedding inside a Go
// double-quoted fmt format string. It applies Go string-literal escaping
// (quotes, backslashes, control characters) and doubles percent signs so they
// are not interpreted as format verbs. Without this, a property name containing
// a quote, backslash, or percent produces uncompilable or misformatted code.
func jsonErrorNameFunc(s string) string {
	return strings.ReplaceAll(goStringLiteralFunc(s), "%", "%%")
}

func validationValueFunc(recv string, rule generator.ValidationRule) string {
	field := recv + "." + rule.FieldName
	if rule.IsPointer {
		field = "*" + field
	}
	if rule.StringConvert {
		return "string(" + field + ")"
	}
	return field
}

func validationNonNilFunc(recv string, rule generator.ValidationRule) string {
	return recv + "." + rule.FieldName + " != nil"
}

func validationStringSetFunc(recv string, rule generator.ValidationRule) string {
	value := validationValueFunc(recv, rule)
	if rule.IsPointer {
		return validationNonNilFunc(recv, rule) + " && " + value + " != \"\""
	}
	return value + " != \"\""
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

func ecmaPatternLiteralFunc(v any) string {
	return fmt.Sprintf("%q", normalizeECMA262Pattern(fmt.Sprintf("%v", v)))
}

func normalizeECMA262Pattern(pattern string) string {
	var out strings.Builder
	out.Grow(len(pattern))
	inClass := false

	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if ch == '\\' {
			if i+1 >= len(pattern) {
				out.WriteByte(ch)
				continue
			}
			next := pattern[i+1]
			if inClass && shouldHexEscapeClassIdentity(next) {
				out.WriteString(fmt.Sprintf("\\x%02x", next))
				i++
				continue
			}
			out.WriteByte(ch)
			out.WriteByte(next)
			i++
			continue
		}

		if ch == '[' && !inClass {
			inClass = true
		} else if ch == ']' && inClass && !isLiteralClassClosingBracket(pattern, i) {
			inClass = false
		}
		out.WriteByte(ch)
	}

	return out.String()
}

func isLiteralClassClosingBracket(pattern string, idx int) bool {
	return idx > 0 && idx+1 < len(pattern) && pattern[idx-1] == '['
}

func shouldHexEscapeClassIdentity(ch byte) bool {
	if ch < 0x21 || ch > 0x7e {
		return false
	}
	if strings.ContainsRune(`bBdDsSwWpPxuc0123456789fnrtv^$\.*+?()[]{}|/`, rune(ch)) {
		return false
	}
	return true
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

// ppTypeValuesFunc returns the full list of allowed type names from a
// patternProperties "ppType" validation rule value. The value can be a single
// string or a []string for a multi-type constraint (e.g. ["string","null"]).
func ppTypeValuesFunc(v any) []string {
	switch val := v.(type) {
	case string:
		return []string{val}
	case []string:
		if len(val) > 0 {
			return val
		}
		return []string{"any"}
	default:
		return []string{fmt.Sprintf("%v", val)}
	}
}

// ppTypeValuesMsgFunc renders the allowed type list for an error message,
// e.g. ["string","null"] → `string, null`.
func ppTypeValuesMsgFunc(v any) string {
	return strings.Join(ppTypeValuesFunc(v), ", ")
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

// dynNumFunc renders a JSON Schema numeric constraint as a Go float64 literal.
// strconv with 'g' and -1 precision round-trips the value exactly, and the
// explicit decimal point keeps whole numbers typed as float64 rather than an
// untyped integer constant.
func dynNumFunc(v any) string {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case float32:
		f = float64(n)
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	default:
		return fmt.Sprintf("%v", v)
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}
