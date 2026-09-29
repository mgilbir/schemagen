package emitter

import (
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"unicode/utf8"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// FuncMap returns the template.FuncMap used by the emitter templates.
//
// Key functions:
//   - "goType":         takes a GoType interface value (as any) and returns its Go type string
//   - "enumValue":      formats an enum value as a Go literal (quotes strings, etc.)
//   - "receiverName":   takes a type name and returns a 1-char lowercase receiver name
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
		"patternVar":             patternVarFunc,
		"patternEngineSource":    patternEngineSourceFunc,
		"dynChecksMatchPattern":  dynChecksMatchPatternFunc,
		"dynNum":                 dynNumFunc,
		"numOperand":             numOperandFunc,
		"numBound":               numBoundFunc,
		"hasManualFields":        hasManualFieldsFunc,
		"ppTypeValue":            ppTypeValueFunc,
		"ppTypeValues":           ppTypeValuesFunc,
		"ppTypeValuesMsg":        ppTypeValuesMsgFunc,
		"countExpr":              countExprFunc,
		"countText":              countTextFunc,
		"countN":                 countNFunc,
		"validationFeatures":     validationFeaturesFunc,
		"stringList":             stringListFunc,
		"accessRules":            accessRulesFunc,
		"validationValue":        validationValueFunc,
		"validationNonNil":       validationNonNilFunc,
		"validationStringSet":    validationStringSetFunc,
		"jsonErrorName":          jsonErrorNameFunc,
		"mkCondCtx":              mkCondCtxFunc,
		"mkClosedKeysCtx":        mkClosedKeysCtxFunc,
		"mkEncodeCtx":            mkEncodeCtxFunc,
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
		"numBoundMsg":            numBoundMsgFunc,
		"exactMultipleOf":        exactMultipleOfFunc,
		"exactConstViolated":     exactConstViolatedFunc,
		"numberEnumValue":        numberEnumValueFunc,
		"jsonNumberLiteral":      jsonNumberLiteralFunc,
		"mkAliasFormatCtx":       mkAliasFormatCtxFunc,
		"mkStringFormatCtx":      mkStringFormatCtxFunc,
		"mkStringFormatCtxArgs":  mkStringFormatCtxArgsFunc,
		"mkStringContentCtx":     mkStringContentCtxFunc,
		"mkStringContentCtxArgs": mkStringContentCtxArgsFunc,
		"formatElemExpr":         formatElemExprFunc,
		"formatHelperName":       formatHelperNameFunc,
		"formatValueExpr":        formatValueExprFunc,
		"itemRange":              itemRangeFunc,
		"leastKey":               leastKeyFunc,
		"itemElem":               itemElemFunc,
		"itemPath":               itemPathFunc,
		"pathErrf":               pathErrfFunc,
		"pathWrapf":              pathWrapFunc,
		"tupleHeld":              tupleHeldFunc,
		"pathJoin":               pathJoinFunc,
		"itemArgs":               itemArgsFunc,
		"argPrefix":              argPrefixFunc,

		// The escapers, one per Go context. See goescape.go.
		"commentLine": commentLineFunc,
		"fmtText":     fmtTextFunc,
		"numLit":      numLitFunc,
		"fmtCat":      fmtCatFunc,
		"jsonTagName": jsonTagNameFunc,
		"rawString":   rawStringFunc,

		// The guards New appends to every action (guards_gen.go). A template
		// never calls these itself.
		"_goCode":    guardCode,
		"_goComment": guardComment,
		"_goString":  guardString,
		"_goFormat":  guardFormat,
		"_goRaw":     guardRaw,
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
	Path        formatText
	Args        string // see TupleContext.Args
	Def         generator.ContainsDef
	MinContains *generator.CountBound
	MaxContains *generator.CountBound
}

func mkContainsCtxFunc(expr string, path formatText, def *generator.ContainsDef, minContains, maxContains *generator.CountBound) ContainsContext {
	ctx := ContainsContext{Expr: expr, Path: path, MinContains: minContains, MaxContains: maxContains}
	if def != nil {
		ctx.Def = *def
	}
	return ctx
}

// mkContainsCtxIn is mkContainsCtx for an array reached inside an enclosing
// loop -- an array that is another container's element -- whose error path
// carries that loop's verbs and needs its variables to fill them.
func mkContainsCtxInFunc(expr string, path formatText, args string, def *generator.ContainsDef, minContains, maxContains *generator.CountBound) ContainsContext {
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
	Path      formatText
	Args      string
	Items     []generator.TupleItemDef
	Tail      *generator.TupleItemDef
	TailStart int
}

func mkTupleCtxFunc(expr string, path formatText, items []generator.TupleItemDef, tail *generator.TupleItemDef) TupleContext {
	return TupleContext{Expr: expr, Path: path, Items: items, Tail: tail, TailStart: len(items)}
}

// mkTupleCtxIn is mkTupleCtx for a tuple reached inside an enclosing loop.
func mkTupleCtxInFunc(expr string, path formatText, args string, items []generator.TupleItemDef, tail *generator.TupleItemDef) TupleContext {
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
	Path formatText
	Args string
	Item generator.TupleItemDef
}

func mkTupleCaseFunc(cond string, item any, path formatText, args string) TupleCaseContext {
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
	Path formatText
	Args string // see TupleContext.Args
	Def  generator.UnevaluatedItemsDef
}

func mkUnevalItemsCtxFunc(expr string, path formatText, def *generator.UnevaluatedItemsDef) UnevalItemsContext {
	ctx := UnevalItemsContext{Expr: expr, Path: path}
	if def != nil {
		ctx.Def = *def
	}
	return ctx
}

// mkUnevalItemsCtxIn is mkUnevalItemsCtx for an array reached inside an
// enclosing loop.
func mkUnevalItemsCtxInFunc(expr string, path formatText, args string, def *generator.UnevaluatedItemsDef) UnevalItemsContext {
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
	Path formatText
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

func mkStringFormatCtxFunc(expr string, path formatText, value any, stringBacked bool) StringFormatContext {
	return StringFormatContext{Expr: expr, Path: path, Value: value, StringBacked: stringBacked}
}

func mkStringFormatCtxArgsFunc(expr string, path formatText, args string, value any, stringBacked bool) StringFormatContext {
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
	Path      formatText
	Args      string
	Encoding  string
	MediaType string
}

func contentCtx(expr string, path formatText, args string, rule generator.ValidationRule) StringContentContext {
	ctx := StringContentContext{Expr: expr, Path: path, Args: args}
	if check, ok := rule.Value.(generator.ContentCheck); ok {
		ctx.Encoding, ctx.MediaType = check.Encoding, check.MediaType
	}
	return ctx
}

func mkStringContentCtxFunc(expr string, path formatText, rule generator.ValidationRule) StringContentContext {
	return contentCtx(expr, path, "", rule)
}

func mkStringContentCtxArgsFunc(expr string, path formatText, args string, rule generator.ValidationRule) StringContentContext {
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

// leastKeyCtx is the context of least_key_open and least_key_close: the
// loop's key and value variables ("_" for a value the body does not read), the
// map it ranges over, and what the enclosing function returns the refusal with.
type leastKeyCtx struct {
	Key, Val, Container, Ret string
}

// Vars is the loop's variable list, without a blank value.
func (c leastKeyCtx) Vars() string {
	if c.Val == "_" {
		return c.Key
	}
	return c.Key + ", " + c.Val
}

func leastKeyFunc(key, val, container, ret string) leastKeyCtx {
	return leastKeyCtx{Key: key, Val: val, Container: container, Ret: ret}
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
// one verb per level: %d for a slice index, %q for a map key.
//
// A container that is not a declared property has no name to lead with, and
// leads with nothing: the path is the accessors alone, `[1]` or `["kk"]`. It
// used to lead with the keyword that constrains the container instead --
// "items" for an array, "properties" for a map -- which printed, byte for byte,
// what a document with a member of that name would print, so a caller could not
// tell a real member from a keyword the generator had substituted. See issue
// #280. The message such a path opens is marked by pathErrfFunc, since a
// container that does have a name has to glue the two together without a "."
// between them.
func itemPathFunc(def generator.ItemValidationDef, level int) formatText {
	var b strings.Builder
	if def.JSONName != "" {
		b.WriteString(string(jsonErrorNameFunc(def.JSONName)))
	}
	for i := 0; i <= level; i++ {
		if def.Levels[i].IsMap {
			b.WriteString("[%q]")
		} else {
			b.WriteString("[%d]")
		}
	}
	return formatText(b.String())
}

// pathIsAccessorLed reports whether an error path opens with an accessor rather
// than with a name -- which is what a container with no JSON name of its own
// emits, `[%d]` for a slice and `[%q]` for a map.
//
// The test is against the verb and not the bracket, so that it cannot be met by
// a document's own property name: jsonErrorNameFunc doubles a percent sign, so a
// property named "[%d]" renders as "[%%d]" and a property named "[x]" as "[x]".
// Only a path this generator built with no name in front can begin with the two
// characters of a format verb inside the first bracket.
func pathIsAccessorLed(path formatText) bool {
	return strings.HasPrefix(string(path), "[%d]") || strings.HasPrefix(string(path), "[%q]")
}

// pathErrfFunc names the constructor a check under this error path builds its
// error with: the plain one where the path opens with a name, and jsonElemErrorf
// where it opens with an accessor. The second has to be marked, because a
// message opening with "[1]" is glued to its container's path with nothing
// between it, where a message opening with a member name takes a ".".
func pathErrfFunc(path formatText) string {
	if pathIsAccessorLed(path) {
		return "jsonElemErrorf"
	}
	return "fmt.Errorf"
}

// tupleHeldFunc reports whether any position of a tuple is decoded in place
// (TupleItemDef.Decoder), which is when the tuple's elements may be read lazily.
func tupleHeldFunc(items []generator.TupleItemDef, tail *generator.TupleItemDef) bool {
	for _, it := range items {
		if it.Decoder != "" {
			return true
		}
	}
	return tail != nil && tail.Decoder != ""
}

// pathWrapFunc is pathErrfFunc for a message that ends with another error's:
// the helper that puts a prefix in front of it without writing the text out,
// for the reason jsonPathError gives.
func pathWrapFunc(path formatText) string {
	if pathIsAccessorLed(path) {
		return "jsonElemWrapf"
	}
	return "jsonWrapf"
}

// pathJoinFunc is pathErrfFunc for the joiner rather than the constructor: what
// an element's own Validate error is put behind this path with.
func pathJoinFunc(path formatText) string {
	if pathIsAccessorLed(path) {
		return "jsonElemPathf"
	}
	return "jsonPathf"
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

// accessRulesFunc renders --strict-read-write's path table as a Go literal.
//
// The order is the generator's, which sorted it: the rules refuse and delete the
// same things whichever way round they are read, but a generated file that
// changed between runs of one input would be unusable.
func accessRulesFunc(rules []generator.AccessRule) (string, error) {
	if len(rules) == 0 {
		return "nil", nil
	}
	var b strings.Builder
	b.WriteString("[]_accessRule{\n")
	for _, rule := range rules {
		b.WriteString("\t{Path: []_accessStep{")
		for i, step := range rule.Path {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("{Kind: " + accessStepKindName(step.Kind))
			// A pattern step is matched through the package's compiled
			// pattern, never by compiling its text in the walker.
			if step.Kind == generator.AccessPattern {
				name, err := generator.PatternVarName(step.Name)
				if err != nil {
					return "", err
				}
				b.WriteString(", Pattern: " + name)
			} else if step.Name != "" {
				fmt.Fprintf(&b, ", Name: %q", step.Name)
			}
			if step.Index != 0 {
				fmt.Fprintf(&b, ", Index: %d", step.Index)
			}
			if len(step.Except) > 0 {
				b.WriteString(", Except: " + goStringSlice(step.Except))
			}
			if len(step.ExceptPatterns) > 0 {
				names := make([]string, len(step.ExceptPatterns))
				for j, pattern := range step.ExceptPatterns {
					name, err := generator.PatternVarName(pattern)
					if err != nil {
						return "", err
					}
					names[j] = name
				}
				b.WriteString(", ExceptPatterns: []*_schemagenRegexp{" + strings.Join(names, ", ") + "}")
			}
			b.WriteString("}")
		}
		b.WriteString("}")
		if rule.ReadOnly {
			b.WriteString(", ReadOnly: true")
		}
		if rule.WriteOnly {
			b.WriteString(", WriteOnly: true")
		}
		b.WriteString("},\n")
	}
	b.WriteString("}")
	return b.String(), nil
}

func accessStepKindName(k generator.AccessStepKind) string {
	switch k {
	case generator.AccessPattern:
		return "_accessPattern"
	case generator.AccessOther:
		return "_accessOther"
	case generator.AccessItems:
		return "_accessItems"
	case generator.AccessTuple:
		return "_accessTuple"
	}
	return "_accessProperty"
}

func goStringSlice(values []string) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprintf("%q", v)
	}
	return "[]string{" + strings.Join(parts, ", ") + "}"
}

// EncodeContext is what the encode templates render one member of a struct
// with: the receiver, the struct, and the member.
type EncodeContext struct {
	Recv   string
	Struct *generator.StructDef
	Member generator.EncodeMember
}

func mkEncodeCtxFunc(recv string, s *generator.StructDef, m generator.EncodeMember) EncodeContext {
	return EncodeContext{Recv: recv, Struct: s, Member: m}
}

// ClosedKeysContext is what object_branch_closed_keys renders: the receiver
// whose recorded keys are read, and the branch whose closed key sets they are
// held to.
type ClosedKeysContext struct {
	Recv   string
	Branch generator.ObjectOneOfBranch
}

func mkClosedKeysCtxFunc(recv string, branch generator.ObjectOneOfBranch) ClosedKeysContext {
	return ClosedKeysContext{Recv: recv, Branch: branch}
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
// Strings are quoted; a number is written as the schema wrote it.
//
// The number goes to generator.GoNumberLiteral rather than through float64.
// It used to be rendered by converting to int64 and back to test whether the
// float was whole, which decided two things wrongly for one and the same
// reason: 9223372036854775807 had already become 2^63 by the time it arrived,
// the round trip through int64 then said "not whole", and the constant was
// emitted as 9.223372036854776e+18 -- a float literal in an int64 constant
// declaration, which does not compile. Below that threshold the same rounding
// declared a neighbouring integer instead, silently.
func enumValueFunc(v any) string {
	if s, ok := v.(string); ok {
		return fmt.Sprintf("%q", s)
	}
	if lit := generator.GoNumberLiteral(v); lit != "" {
		return lit
	}
	return fmt.Sprintf("%v", v)
}

// receiverNameFunc takes a type name and returns a single lowercase character
// suitable for use as a Go method receiver name.
// numberEnumValueFunc renders one member of an enum whose base type is the
// json.Number a "number" is held as. The member is the literal the schema
// wrote, quoted: json.Number is a string underneath, and 1.5 unquoted is a
// float constant that cannot be assigned to it.
// jsonNumberLiteralFunc renders a schema-supplied number as the quoted decimal
// literal the exact comparisons take as their bound.
func jsonNumberLiteralFunc(v any) string {
	if lit := generator.JSONNumberLiteral(v); lit != "" {
		return strconv.Quote(lit)
	}
	return strconv.Quote(fmt.Sprintf("%v", v))
}

func numberEnumValueFunc(v any) string {
	if lit := generator.JSONNumberLiteral(v); lit != "" {
		return strconv.Quote(lit)
	}
	return strconv.Quote(fmt.Sprintf("%v", v))
}

// receiverNameFunc is a method's receiver: the type name's first letter,
// lowered by the generator's pinned case mapping. strings.ToLower asked the
// Go running the generator, and a newer Go can lower a letter onto one the
// oldest Go the generated code supports does not have -- a receiver that
// compiles on one and not the other. See generator.IdentifierToLower.
func receiverNameFunc(name string) string {
	if name == "" {
		return "x"
	}
	r, _ := utf8.DecodeRuneInString(name)
	return string(generator.IdentifierToLower(r))
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

// goStringQuoteFunc returns a Go quoted string literal (with surrounding quotes).
// This is useful in templates where backtick strings can't be used.
func goStringQuoteFunc(s string) string {
	return fmt.Sprintf("%q", s)
}

// patternVarFunc names the package-level variable a schema pattern is compiled
// into (see generator.PatternVarName). Every pattern generated code matches
// with is reached through one: the helper file compiles each distinct pattern
// once, when the package is initialised, and nothing compiles a pattern where
// it is used. A pattern that is not an ECMA-262 regular expression has no
// variable and fails the emit -- generation refuses such a schema before it
// gets here, so reaching this is IR built by other means, and it is refused
// rather than emitted as a check that could only panic or match nothing.
func patternVarFunc(v any) (string, error) {
	pattern, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("patternVar: a pattern is a string, not %T", v)
	}
	return generator.PatternVarName(pattern)
}

// dynChecksMatchPatternFunc reports whether a dyn_branch expression over these
// checks matches a pattern, and so needs the _undecided local that expression
// records an undecided match in.
func dynChecksMatchPatternFunc(checks []generator.DynamicCheck) bool {
	for _, c := range checks {
		if c.Kind == "pattern" {
			return true
		}
	}
	return false
}

// patternEngineSourceFunc is the strconv-quoted text the engine compiles for a
// schema pattern (see generator.PatternEngineSource): the pattern itself
// whenever it is valid as written.
func patternEngineSourceFunc(pattern string) (string, error) {
	src, err := generator.PatternEngineSource(pattern)
	if err != nil {
		return "", fmt.Errorf("pattern %q is not an ECMA-262 regular expression: %w", pattern, err)
	}
	return strconv.Quote(src), nil
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
//
// The names are the schema's own "type" strings, so they are escaped for the
// format literal they are written into rather than trusted to be the seven
// JSON type names.
func ppTypeValuesMsgFunc(v any) formatText {
	return fmtTextFunc(strings.Join(ppTypeValuesFunc(v), ", "))
}

// countOf reads a count bound out of the shapes a template holds one in: a
// generator.CountBound, a pointer to one, or a plain int (a bound the
// generator derived rather than read, such as a closed tuple's length).
func countOf(v any) (generator.CountBound, bool) {
	switch c := v.(type) {
	case generator.CountBound:
		return c, true
	case *generator.CountBound:
		if c == nil {
			return generator.CountBound{}, false
		}
		return *c, true
	case int:
		return generator.CountBound{N: c}, true
	}
	return generator.CountBound{}, false
}

// countExprFunc writes a value where generated *code* compares against it.
//
// For a count bound it is CountBound.GoExpr, which compiles on every target;
// see CountBound for why the literal does not. GoExpr is built from the bound's
// int alone -- a decimal, or that decimal inside a fixed min/max expression --
// so nothing the schema spelled reaches the code. Every other value -- a
// numeric bound, which the same template positions also compare against -- is
// numLit's: written exactly as printing it always wrote it, and refused unless
// it is a number. Routing a comparison through here therefore changes nothing
// for anything but a count, and admits nothing numLit would refuse.
func countExprFunc(v any) (string, error) {
	if c, ok := countOf(v); ok {
		return c.GoExpr(), nil
	}
	return numLitFunc(v)
}

// countTextFunc writes a count bound where a *message* states it: the number
// the schema wrote, which for a saturated bound is the schema's own literal
// (CountBound.String). The messages that state a count are fmt formats, so the
// text is escaped for one, as fmtText escapes any value; a JSON number needs
// none of it, but the literal came from the schema and the format context
// accepts nothing that has not been through its escaper. Anything that is not a
// count bound is refused: the function exists for the pointer-valued bounds a
// template cannot hand to fmtText directly, and a nil one is a template that
// forgot its presence test.
func countTextFunc(v any) (formatText, error) {
	c, ok := countOf(v)
	if !ok {
		return "", fmt.Errorf("%w: %T %q where a count bound was expected", errEscape, v, printedValue(v))
	}
	return fmtTextFunc(c.String()), nil
}

// countNFunc is a count bound's compared value, for a template's own
// decisions (is minContains zero?).
func countNFunc(v any) int {
	c, _ := countOf(v)
	return c.N
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

// numOperandFunc renders the instance side of a numeric check: converted to
// float64 ordinarily, and left as it is for a rule the generator settled as an
// int64 comparison. See ValidationRule.IntegerCompare.
//
// A rule that never sets the flag -- every one built for a "number" -- emits
// the source it emitted before.
func numOperandFunc(rule generator.ValidationRule, expr string) string {
	if rule.ExactCompare {
		// The whole comparison, folded into the operand so that the four
		// ordering keywords keep the one shape they are written in: each is a
		// relational operator against a bound, and numBoundFunc answers 0 for
		// the other side. json.Number is a string underneath -- float64(x) does
		// not compile against one -- so a rule this flag failed to reach fails
		// the build rather than going on comparing through float64.
		return "jsonNumberCmp(" + exactNumberOperand(expr) + ", " + strconv.Quote(exactNumberBound(rule)) + ")"
	}
	if rule.IntegerCompare {
		return expr
	}
	return "float64(" + expr + ")"
}

// exactNumberOperand converts the instance expression to the json.Number the
// comparison takes.
//
// Written for every operand rather than only the ones that need it. A named
// type over json.Number -- a $defs alias, an enum -- is not a json.Number to
// Go and has to be converted; a field already is one and the conversion is the
// identity. Deciding which by inspecting the expression would be guessing at
// the Go type from a string, and getting it wrong in the first direction does
// not compile while getting it wrong in the second costs nothing.
func exactNumberOperand(expr string) string {
	return "json.Number(" + expr + ")"
}

// exactNumberBound is the bound as the decimal literal jsonNumberCmp reads,
// which is the literal the schema wrote. It is compared digit by digit against
// the value's own literal, so nothing is gained by re-rendering it and one
// thing is lost: 1e308 written out in integer notation is three hundred and
// nine digits of the same number.
func exactNumberBound(rule generator.ValidationRule) string {
	if lit := generator.JSONNumberLiteral(rule.Value); lit != "" {
		return lit
	}
	return fmt.Sprintf("%v", rule.Value)
}

// numBoundFunc renders a rule's bound as the Go constant its comparison needs:
// integer notation when the check is made in int64, and the literal the schema
// wrote otherwise.
func numBoundFunc(rule generator.ValidationRule) string {
	if rule.ExactCompare {
		// numOperandFunc emitted the comparison; what is left for the operator
		// to test it against is zero.
		return "0"
	}
	if lit := generator.GoNumberLiteral(rule.Value); lit != "" {
		return lit
	}
	return fmt.Sprintf("%v", rule.Value)
}

// numBoundMsgFunc is the bound as it is named in an error message, which is
// always a number and never the 0 numBoundFunc answers for an exact
// comparison: "is less than minimum 0" would be a message about the wrong one.
//
// The exact form names the literal the schema wrote, which is what the check it
// accompanies compares against. Everywhere else this is numBoundFunc exactly,
// so no message that existed before moves -- and a Go constant is what those
// checks still compare, so the integer notation GoNumberLiteral chooses for a
// whole number is still the right rendering there.
//
// It is written into a format literal, and returns formatText: what it renders
// is a number wherever the schema reader held one, and escaped wherever it did
// not.
func numBoundMsgFunc(rule generator.ValidationRule) formatText {
	if rule.ExactCompare {
		return fmtTextFunc(exactNumberBound(rule))
	}
	return fmtTextFunc(numBoundFunc(rule))
}

// exactMultipleOfFunc renders the divisibility test for a number held exactly.
// The float64 quotient it replaces was compared against a tolerance of 1e-9,
// which is not a comparison the value can survive; see jsonNumberIsMultipleOf.
func exactMultipleOfFunc(rule generator.ValidationRule, expr string) string {
	return "jsonNumberIsMultipleOf(" + exactNumberOperand(expr) + ", " + strconv.Quote(exactNumberBound(rule)) + ")"
}

// exactConstViolatedFunc renders the const test for a number held exactly, as
// the condition under which the rule is broken -- which is the shape the
// emitted check wants and the shape an equality would need parenthesising to
// reach.
//
// The general arm marshals the value and compares the JSON text, which reads
// 2.50 and 2.5 as different constants and 1.0000000000000000000000000000001 as
// 1; they are one number and two, respectively, and the schema said the
// numbers.
func exactConstViolatedFunc(rule generator.ValidationRule, expr string) string {
	return "jsonNumberCmp(" + exactNumberOperand(expr) + ", " + strconv.Quote(rule.ExactValue) + ") != 0"
}

// dynNumFunc renders a JSON Schema numeric constraint as a Go float64 literal.
//
// The literal the schema wrote is used, so a bound keeps every digit it was
// given; the trailing ".0" is added when the literal has no decimal point or
// exponent, which is what keeps a whole number typed as float64 rather than as
// an untyped integer constant in the expressions these appear in.
func dynNumFunc(v any) string {
	s := generator.GoNumberLiteral(v)
	if s == "" {
		return fmt.Sprintf("%v", v)
	}
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}
