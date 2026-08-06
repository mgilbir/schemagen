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
		"comment":         commentFunc,
		"goType":          goTypeFunc,
		"enumValue":       enumValueFunc,
		"receiverName":    receiverNameFunc,
		"lowerFirst":      lowerFirstFunc,
		"add":             addFunc,
		"wrapTypeDef":     wrapTypeDefFunc,
		"mkOneOfCtx":      mkOneOfCtxFunc,
		"mkAnnotationCtx": mkAnnotationCtxFunc,
		"isOneOfRequired": func(oof any) bool {
			if o, ok := oof.(generator.OneOfDef); ok {
				return o.Required
			}
			if o, ok := oof.(*generator.OneOfDef); ok {
				return o.Required
			}
			return false
		},
		"requiredFieldsList":     requiredFieldsListFunc,
		"hasRequiredFields":      func(fields []string) bool { return len(fields) > 0 },
		"isRawMessage":           isRawMessageFunc,
		"goStringLiteral":        goStringLiteralFunc,
		"goStringQuote":          goStringQuoteFunc,
		"ecmaPattern":            ecmaPatternLiteralFunc,
		"dynNum":                 dynNumFunc,
		"hasManualFields":        hasManualFieldsFunc,
		"ppTypeValue":            ppTypeValueFunc,
		"ppTypeValues":           ppTypeValuesFunc,
		"ppTypeValuesMsg":        ppTypeValuesMsgFunc,
		"deref":                  derefIntFunc,
		"validationFeatures":     validationFeaturesFunc,
		"stringList":             stringListFunc,
		"validationValue":        validationValueFunc,
		"validationNonNil":       validationNonNilFunc,
		"validationStringSet":    validationStringSetFunc,
		"jsonErrorName":          jsonErrorNameFunc,
		"mkCondCtx":              mkCondCtxFunc,
		"mkItemCtx":              mkItemCtxFunc,
		"mkContainsCtx":          mkContainsCtxFunc,
		"mkContainsCtxIn":        mkContainsCtxInFunc,
		"mkTupleCtx":             mkTupleCtxFunc,
		"mkTupleCtxIn":           mkTupleCtxInFunc,
		"mkTupleCase":            mkTupleCaseFunc,
		"mkUnevalItemsCtx":       mkUnevalItemsCtxFunc,
		"mkUnevalItemsCtxIn":     mkUnevalItemsCtxInFunc,
		"mkItemLevelCtx":         mkItemLevelCtxFunc,
		"mkBigIntVariantCtx":     mkBigIntVariantCtxFunc,
		"mkAliasFormatCtx":       mkAliasFormatCtxFunc,
		"mkStringFormatCtx":      mkStringFormatCtxFunc,
		"mkStringFormatCtxArgs":  mkStringFormatCtxArgsFunc,
		"mkStringContentCtx":     mkStringContentCtxFunc,
		"mkStringContentCtxArgs": mkStringContentCtxArgsFunc,
		"formatElemExpr":         formatElemExprFunc,
		"formatHelperName":       formatHelperNameFunc,
		"formatValueExpr":        formatValueExprFunc,
		"itemRange":              itemRangeFunc,
		"itemElem":               itemElemFunc,
		"itemPath":               itemPathFunc,
		"itemArgs":               itemArgsFunc,
		"argPrefix":              argPrefixFunc,
	}
}

// ObjectCondContext is passed to the object_cond_branch template, which needs
// the receiver name alongside the branch to address _jsonRawProps.
type ObjectCondContext struct {
	Recv   string
	Branch generator.ObjectConditionalBranch
}

func mkCondCtxFunc(recv string, branch *generator.ObjectConditionalBranch) ObjectCondContext {
	if branch == nil {
		return ObjectCondContext{Recv: recv}
	}
	return ObjectCondContext{Recv: recv, Branch: *branch}
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

// ContainsContext is passed to the contains_check template. Expr is the Go
// expression naming the slice the count runs over and Path the prefix its
// errors are reported under, which is all that separates an array alias's
// contains check from an array property's.
type ContainsContext struct {
	Expr        string
	Path        string
	Args        string // see TupleContext.Args
	Def         generator.ContainsDef
	MinContains *int
	MaxContains *int
}

func mkContainsCtxFunc(expr, path string, def *generator.ContainsDef, minContains, maxContains *int) ContainsContext {
	ctx := ContainsContext{Expr: expr, Path: path, MinContains: minContains, MaxContains: maxContains}
	if def != nil {
		ctx.Def = *def
	}
	return ctx
}

// mkContainsCtxIn is mkContainsCtx for an array reached inside an enclosing
// loop -- an array that is another container's element -- whose error path
// carries that loop's verbs and needs its variables to fill them.
func mkContainsCtxInFunc(expr, path, args string, def *generator.ContainsDef, minContains, maxContains *int) ContainsContext {
	ctx := mkContainsCtxFunc(expr, path, def, minContains, maxContains)
	ctx.Args = args
	return ctx
}

// TupleContext is passed to the tuple_items_check template. As with
// ContainsContext, Expr names the slice and Path prefixes the errors, which is
// all that separates an array alias's positional checks from an array
// property's. TailStart is derived rather than passed: the tail begins where
// the declared positions end.
// Args is what an enclosing loop contributes to the error path. Path is a fmt
// format string, so a caller nested inside another loop -- a tuple that is
// itself an array's element -- hands in a path carrying that loop's verbs and
// the variables that fill them, and every Errorf below puts them first.
// Callers not inside such a loop pass "", and the emitted code is unchanged.
type TupleContext struct {
	Expr      string
	Path      string
	Args      string
	Items     []generator.TupleItemDef
	Tail      *generator.TupleItemDef
	TailStart int
}

func mkTupleCtxFunc(expr, path string, items []generator.TupleItemDef, tail *generator.TupleItemDef) TupleContext {
	return TupleContext{Expr: expr, Path: path, Items: items, Tail: tail, TailStart: len(items)}
}

// mkTupleCtxIn is mkTupleCtx for a tuple reached inside an enclosing loop.
func mkTupleCtxInFunc(expr, path, args string, items []generator.TupleItemDef, tail *generator.TupleItemDef) TupleContext {
	ctx := mkTupleCtxFunc(expr, path, items, tail)
	ctx.Args = args
	return ctx
}

// TupleCaseContext is one arm of tuple_items_check: the index condition the arm
// governs, and the check to make there. The item arrives by value when it came
// from ranging the position list and by pointer when it is the tail, so both are
// accepted rather than making the template reach for an address it cannot take.
type TupleCaseContext struct {
	Cond string
	Path string
	Args string
	Item generator.TupleItemDef
}

func mkTupleCaseFunc(cond string, item any, path, args string) TupleCaseContext {
	ctx := TupleCaseContext{Cond: cond, Path: path, Args: args}
	switch v := item.(type) {
	case generator.TupleItemDef:
		ctx.Item = v
	case *generator.TupleItemDef:
		if v != nil {
			ctx.Item = *v
		}
	}
	return ctx
}

// UnevalItemsContext is passed to the uneval_items_check template, on the same
// terms as ContainsContext and TupleContext: one definition of the check, and
// the caller says which slice it runs over and how its failures are named.
type UnevalItemsContext struct {
	Expr string
	Path string
	Args string // see TupleContext.Args
	Def  generator.UnevaluatedItemsDef
}

func mkUnevalItemsCtxFunc(expr, path string, def *generator.UnevaluatedItemsDef) UnevalItemsContext {
	ctx := UnevalItemsContext{Expr: expr, Path: path}
	if def != nil {
		ctx.Def = *def
	}
	return ctx
}

// mkUnevalItemsCtxIn is mkUnevalItemsCtx for an array reached inside an
// enclosing loop.
func mkUnevalItemsCtxInFunc(expr, path, args string, def *generator.UnevaluatedItemsDef) UnevalItemsContext {
	ctx := mkUnevalItemsCtxFunc(expr, path, def)
	ctx.Args = args
	return ctx
}

// BigIntVariantContext is passed to the bigint_alias_variant_checks template,
// which needs the receiver name alongside one anyOf / oneOf branch's rules to
// render the big.Float comparisons.
type BigIntVariantContext struct {
	Recv  string
	Rules []generator.ValidationRule
}

func mkBigIntVariantCtxFunc(recv string, rules []generator.ValidationRule) BigIntVariantContext {
	return BigIntVariantContext{Recv: recv, Rules: rules}
}

// AliasFormatContext is passed to the alias_format_check template, which needs
// the receiver name alongside the format the rule names, and whether the alias
// holds the JSON string rather than the Go type the format maps to.
type AliasFormatContext struct {
	Recv         string
	Value        any
	StringBacked bool
}

func mkAliasFormatCtxFunc(recv string, rule generator.ValidationRule) AliasFormatContext {
	return AliasFormatContext{Recv: recv, Value: rule.Value, StringBacked: rule.StringBacked}
}

// StringFormatContext is passed to the string_format_check template, which
// writes a format assertion over an arbitrary Go expression of type string
// rather than over a named field or receiver.
type StringFormatContext struct {
	Expr string
	Path string
	// Args is the argument list a Path carrying format verbs needs -- an element
	// check names its index, so its path is "items[%d]" and cannot be printed
	// without one. Empty for the positions whose path is a literal.
	Args string
	// Value is the format keyword; StringBacked says whether Expr is the JSON
	// string or the Go type the format maps to, which decides which of the two
	// helper spellings is called.
	Value        any
	StringBacked bool
}

func mkStringFormatCtxFunc(expr, path string, value any, stringBacked bool) StringFormatContext {
	return StringFormatContext{Expr: expr, Path: path, Value: value, StringBacked: stringBacked}
}

func mkStringFormatCtxArgsFunc(expr, path, args string, value any, stringBacked bool) StringFormatContext {
	return StringFormatContext{Expr: expr, Path: path, Args: args, Value: value, StringBacked: stringBacked}
}

// StringContentContext is passed to the string_content_check template, which
// writes the content vocabulary's assertion over a Go expression of type string.
//
// The two keywords arrive as one rule and are emitted as one call, because
// contentMediaType judges the bytes contentEncoding produced. See
// generator.ContentCheck.
//
// Args is what an enclosing loop contributes to the error path, on the same
// terms as StringFormatContext.Args: an element check names its index, so its
// path is "items[%d]" and cannot be printed without one.
type StringContentContext struct {
	Expr      string
	Path      string
	Args      string
	Encoding  string
	MediaType string
}

func contentCtx(expr, path, args string, rule generator.ValidationRule) StringContentContext {
	ctx := StringContentContext{Expr: expr, Path: path, Args: args}
	if check, ok := rule.Value.(generator.ContentCheck); ok {
		ctx.Encoding, ctx.MediaType = check.Encoding, check.MediaType
	}
	return ctx
}

func mkStringContentCtxFunc(expr, path string, rule generator.ValidationRule) StringContentContext {
	return contentCtx(expr, path, "", rule)
}

func mkStringContentCtxArgsFunc(expr, path, args string, rule generator.ValidationRule) StringContentContext {
	return contentCtx(expr, path, args, rule)
}

// formatElemExprFunc renders a container element as the value its format helper
// takes. The element is already the Go type the format maps to where there is
// one, so only the string case needs a conversion -- a named string element
// among them.
func formatElemExprFunc(elem string, stringBacked bool) string {
	if stringBacked {
		return "string(" + elem + ")"
	}
	return elem
}

// formatValueExprFunc renders an alias receiver as the value its format helper
// takes: the string it is defined over, or the netip.Addr it is defined over.
func formatValueExprFunc(recv string, stringBacked bool) string {
	if stringBacked {
		return "string(" + recv + ")"
	}
	return "netip.Addr(" + recv + ")"
}

// formatHelperNameFunc maps a format keyword to the shared helper that checks
// it, or "" when this generator has no check for it.
//
// The string-backed half is generator.FormatHelperName, which is where it has to
// live: the same mapping decides which helper *block* a package needs, and that
// question is asked by generator.HelpersReferencedBy before any template runs.
// Two copies of a format-to-function table is the drift this repository has paid
// for before, so there is one, next to FormatCheckableOnString, which is the
// predicate it has to agree with.
func formatHelperNameFunc(v any, stringBacked bool) string {
	format, ok := v.(string)
	if !ok {
		return ""
	}
	if !stringBacked {
		// The value is the Go type the format maps to. Decoding already refused
		// anything the parser rejects, so all that is left is the address
		// family -- and it is read off the netip.Addr rather than off a string
		// that no longer exists.
		switch format {
		case "ipv4":
			return "schemagenFormatIPv4Addr"
		case "ipv6":
			return "schemagenFormatIPv6Addr"
		default:
			return ""
		}
	}
	return generator.FormatHelperName(format)
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
// one verb per level: %d for a slice index, %q for a map key. An alias has no
// property name to lead with, so it reports the position under the keyword that
// constrains it.
func itemPathFunc(def generator.ItemValidationDef, level int) string {
	name := "items"
	switch {
	case def.PathName != "":
		name = jsonErrorNameFunc(def.PathName)
	case def.JSONName != "":
		name = jsonErrorNameFunc(def.JSONName)
	case len(def.Levels) > 0 && def.Levels[0].IsMap:
		name = "properties"
	}
	var b strings.Builder
	b.WriteString(name)
	for i := 0; i <= level; i++ {
		if def.Levels[i].IsMap {
			b.WriteString("[%q]")
		} else {
			b.WriteString("[%d]")
		}
	}
	return b.String()
}

// argPrefixFunc turns an enclosing loop's fmt arguments into the text that goes
// in front of a check's own: "_i0" becomes "_i0, ", and the empty string stays
// empty so a check not nested inside a loop emits exactly what it always did.
func argPrefixFunc(args string) string {
	if args == "" {
		return ""
	}
	return args + ", "
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

// AnnotationContext is what the annotation_comment template renders: the comment
// lines, already grouped into paragraphs, and the indent each sits at.
type AnnotationContext struct {
	Indent string
	Lines  []string
}

// mkAnnotationCtxFunc turns the annotation vocabulary into comment lines.
//
// precededByProse says whether the caller has already written comment lines
// above this point, and it is a parameter because it decides whether the first
// paragraph here needs a blank comment line in front of it. Two paragraphs run
// together read as one, and for "Deprecated: " that is not cosmetic: the Go
// convention is a paragraph beginning with that word, and a notice appended to
// the end of an existing paragraph is not one -- gopls, staticcheck and `go doc`
// all miss it.
//
// It is a bool rather than the description itself because the description is not
// the only prose that can be above. Every named-type kind but the two struct
// ones writes a sentence of its own explaining what the generated wrapper is
// ("X accepts any JSON value and validates ..."), and that sentence needs the
// same break the description does; a caller that passed only the description
// would run "Deprecated: " onto the end of it.
//
// The readOnly and writeOnly wording says what the keyword means rather than
// what the generated code does, because by default the generated code does
// nothing with them. See generator.Config.StrictReadWrite for the half that
// does, and why it is off unless asked for.
func mkAnnotationCtxFunc(a generator.Annotations, indent string, precededByProse bool) AnnotationContext {
	ctx := AnnotationContext{Indent: indent}
	if !a.Any() {
		return ctx
	}
	paragraph := func(lines ...string) {
		if len(lines) == 0 {
			return
		}
		if len(ctx.Lines) > 0 || precededByProse {
			ctx.Lines = append(ctx.Lines, "")
		}
		ctx.Lines = append(ctx.Lines, lines...)
	}

	var body []string
	if a.ReadOnly {
		body = append(body,
			`Read-only: the schema says "readOnly", so the owning authority manages`,
			`this value and an application is not expected to send it.`)
	}
	if a.WriteOnly {
		body = append(body,
			`Write-only: the schema says "writeOnly", so the value is not expected`,
			`to be present when the instance is retrieved.`)
	}
	if len(a.Examples) > 0 {
		body = append(body, "Examples from the schema:")
		for _, ex := range a.Examples {
			body = append(body, "  "+ex)
		}
	}
	paragraph(body...)

	// Last, and alone in its paragraph. Both are the convention rather than a
	// preference.
	if a.Deprecated {
		paragraph("Deprecated: the schema marks this deprecated.")
	}
	return ctx
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
