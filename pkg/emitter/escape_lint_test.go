package emitter

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"text/template"
	"text/template/parse"

	"github.com/mgilbir/schemagen/pkg/emitter/internal/gocontext"
)

// This file is the static half of the emitter's escaping discipline; the
// dynamic half is the guard the emitter appends to every action (see
// internal/gocontext, guards_gen.go and goescape.go).
//
// The guards make a forgotten escaper unable to produce wrong source, but they
// can only fail when a value that needed escaping actually arrives -- which
// for a template branch no test schema reaches with hostile text is never, and
// for a user is a generation failure on a schema that deserved output. The lint
// below moves that failure to `go test`: it reads every action in every
// template and requires that what it writes into a literal or into code be
// either the output of the escaper for that context, or a value this file says
// is safe there without one, with the reason written beside it.
//
// The allowlists are the part a reviewer should read. An entry is a claim about
// every value that field ever holds, across every IR type that has a field of
// that name, and a wrong claim is caught only at run time by the guard -- as a
// failure, never as injected code. Keep them to identifiers the naming layer
// mints, numbers, and code the generator builds from those.

// inertFields are fields whose every value is a Go identifier minted by the
// naming layer, a keyword this generator chose, or a number: text no escaper
// would change, so it may be written into any context.
var inertFields = map[string]string{
	"Name":                    "a Go identifier: a type, field, constant or getter name",
	"FieldName":               "a Go field identifier",
	"TypeName":                "a Go type identifier",
	"InPlaceType":             "a Go type identifier: a type wrapper this package declares",
	"WrapperName":             "a Go type identifier",
	"InterfaceName":           "a Go type identifier",
	"ItemsTypeName":           "a Go type identifier",
	"AdditionalItemsTypeName": "a Go type identifier",
	"TypeCheckName":           "a Go function identifier",
	"AccessorName":            "a Go method identifier",
	"ElemVar":                 "a Go variable identifier the generator picks",
	"IndexVar":                "a Go variable identifier the generator picks",
	"PackageName":             "the package clause, which Emit refuses unless it is a Go identifier",
	"Alias":                   "an import alias, which Emit refuses unless it is a Go identifier",
	"InferredJSONType":        "a JSON type name: string, number, integer, boolean, array, object, null",
	"EvaluatedCount":          "an int",
	"IfEvalCount":             "an int",
	"ThenEvalCount":           "an int",
	"ElseEvalCount":           "an int",
	"ResourceCount":           "an int",
	"Index":                   "an int",
	"AllEvaluated":            "a bool",
	"Recv":                    "a receiver name, from receiverName",
	"ParentName":              "a Go type identifier",
	"AccessRulesVar":          "a Go package-variable identifier the generator's name registry mints",
	"SchemaVar":               "a Go package-variable identifier the generator's name registry mints",
	"EncodeKeysVar":           "a Go package-variable identifier the generator's name registry mints",
	"StripRulesVar":           "a Go package-variable identifier the generator's name registry mints",
	"AllowedVar":              "a Go package-variable identifier the generator's name registry mints",
	"GetterName":              "a Go method identifier the generator's name registry mints in the parent's member scope",
}

// inertFuncs are template functions whose result is inert whatever they are
// given.
var inertFuncs = map[string]string{
	"len":          "an int",
	"add":          "an int",
	"receiverName": "one lower-case letter of a Go identifier",
}

// escapersFor are the functions whose result is escaped for a context.
var escapersFor = map[goContext]map[string]bool{
	goString:       {"goStringLiteral": true},
	goFormatString: {"jsonErrorName": true, "fmtText": true, "fmtCat": true, "itemPath": true, "numBoundMsg": true, "ppTypeValuesMsg": true, "countText": true},
	goRawString:    {"jsonTagName": true},
}

// codeFuncs render Go code, or a Go literal, that is safe whatever they are
// given: they quote what they take from the schema, or take nothing from it.
var codeFuncs = map[string]string{
	"goType":              "a Go type expression built from minted identifiers",
	"enumValue":           "strconv-quoted string, or a number literal",
	"numberEnumValue":     "a quoted JSON number literal",
	"jsonNumberLiteral":   "a quoted JSON number literal",
	"goStringQuote":       "strconv.Quote",
	"rawString":           "a raw or strconv-quoted string literal",
	"patternVar":          "a package-level identifier: a fixed prefix and the hex digits of a digest",
	"patternEngineSource": "strconv-quoted pattern",
	"requiredFieldsList":  "strconv-quoted names",
	"stringList":          "strconv-quoted feature names",
	"validationFeatures":  "strconv-quoted feature names",
	"accessRules":         "a composite literal of strconv-quoted names and patterns",
	"numBound":            "a Go number literal from generator.GoNumberLiteral",
	"numLit":              "a Go number literal; refuses anything else",
	"countExpr":           "a count bound's int as a decimal or a fixed min/max expression over it (CountBound.GoExpr); anything else goes through numLit",
	"dynNum":              "a Go float literal",
	"numOperand":          "a comparison built from a code expression and a quoted literal",
	"exactMultipleOf":     "a call built from a code expression and a quoted literal",
	"exactConstViolated":  "a call built from a code expression and a quoted literal",
	"validationValue":     "a field access built from minted identifiers",
	"validationNonNil":    "a nil test built from minted identifiers",
	"validationStringSet": "an emptiness test built from minted identifiers",
	"itemRange":           "a slice expression built from minted identifiers",
	"itemElem":            "an element variable the generator picks",
	"itemArgs":            "index variables the generator picks",
	"argPrefix":           "index variables the generator picks",
	"pathErrf":            "one of two function names",
	"pathWrapf":           "one of two function names",
	"pathJoin":            "one of two function names",
	"formatHelperName":    "a helper function name from a fixed table",
	"formatElemExpr":      "a conversion built from a code expression",
	"formatValueExpr":     "a conversion built from a code expression",
}

// codeFields hold Go code or literals the generator built, each from minted
// identifiers and strconv-quoted schema text.
var codeFields = map[string]string{
	"Expr":    "a Go expression built from minted identifiers",
	"Cond":    "a Go condition built from minted identifiers and ints",
	"Args":    "index variables the generator picks",
	"Convert": "a conversion the generator picks",
	// The decode plans generator/decodeplan.go composes: helper names, method
	// expressions and function literals over GoTypeName()s. Nothing from the
	// schema but minted type names reaches them.
	"Decoder":            "a decode plan: a jsonAt expression built from helper names and minted type names",
	"MemberDecoder":      "a decode plan, as Decoder",
	"ValueDecoder":       "a decode plan, as Decoder",
	"UnderlyingDecoder":  "a decode plan, as Decoder",
	"UnmarshalAsDecoder": "a decode plan, as Decoder",
	// The encode plans generator/encodeplan.go composes, the same way.
	"Encoder":      "an encode plan: a jsonEnc expression built from helper names and minted type names",
	"ValueEncoder": "an encode plan, as Encoder",
	// The identity plans generator/identityplan.go composes, the same way.
	"Identifier":             "an identity plan: a jsonIdentify expression built from helper names and minted type names",
	"ValueIdentifier":        "an identity plan, as Identifier",
	"EncodeAdditionalMember": "an int constant",
	"UnmarshalAs":            "a Go type expression",
	"MarshalAs":              "a Go type expression",
	"ValidateAs":             "a Go type expression",
	"ValueType":              "a Go type expression",
	"DefaultLiteral":         "a Go composite literal; generator.defaultLiteral quotes every string",
	"ZeroLiteral":            "a Go zero value for a minted type",
	"NodeLiteral":            "an evaluator node literal; generator quotes every string",
	"Literal":                "a Go literal; generator quotes every string",
	"Indent":                 "whitespace",
	"Vars":                   "a leastKeyCtx's key and value variables; see TestLeastKeyArgumentsAreCode",
	"Container":              "a leastKeyCtx's map expression; see TestLeastKeyArgumentsAreCode",
	"Key":                    "a leastKeyCtx's key variable; see TestLeastKeyArgumentsAreCode",
	"Ret":                    "a leastKeyCtx's return operands; see TestLeastKeyArgumentsAreCode",
}

// formatFields are fields typed formatText (see TestFormatFieldsAreTyped), so
// that nothing but an escaper's result or a template constant can have set
// them. They are safe in a format literal and nowhere else.
var formatFields = map[string]string{
	"Path": "the error path a check context carries",
}

// formatFieldOwners are the types formatFields are claimed for.
var formatFieldOwners = []any{
	ContainsContext{}, TupleContext{}, TupleCaseContext{}, UnevalItemsContext{},
	StringFormatContext{}, StringContentContext{},
}

// TestFormatFieldsAreTyped holds formatFields to its claim.
func TestFormatFieldsAreTyped(t *testing.T) {
	want := reflect.TypeFor[formatText]()
	for _, owner := range formatFieldOwners {
		typ := reflect.TypeOf(owner)
		for name := range formatFields {
			f, ok := typ.FieldByName(name)
			if !ok {
				t.Errorf("%s has no field %s", typ, name)
				continue
			}
			if f.Type != want {
				t.Errorf("%s.%s is %s; the lint admits it into format literals because it is %s", typ, name, f.Type, want)
			}
		}
	}
}

// varsBound are variables bound by {{range}} or {{with}} rather than by :=,
// whose value the lint cannot follow, and what they hold.
var varsBound = map[string]string{
	"$i":   "a range index",
	"$ri":  "a range index",
	"$ci":  "a range index",
	"$bi":  "a range index",
	"$li":  "a range index",
	"$idx": "a range index",
	"$ai":  "a range index",
	"$oi":  "a range index",
	"$vi":  "a range index",
}

type lintTemplate struct {
	name     string
	tree     *parse.Tree
	bindings map[string][]*parse.PipeNode
}

func parseTemplatesForLint(t *testing.T) (*template.Template, map[string]*lintTemplate) {
	t.Helper()
	tmpl, err := template.New("").Funcs(FuncMap()).ParseFS(templateFS, "templates/*.go.tmpl")
	if err != nil {
		t.Fatalf("parsing templates: %v", err)
	}
	byName := map[string]*lintTemplate{}
	for _, tt := range tmpl.Templates() {
		if tt.Tree == nil || tt.Tree.Root == nil {
			continue
		}
		lt := &lintTemplate{name: tt.Name(), tree: tt.Tree, bindings: map[string][]*parse.PipeNode{}}
		collectBindings(tt.Tree.Root, lt.bindings)
		byName[tt.Name()] = lt
	}
	return tmpl, byName
}

func collectBindings(n parse.Node, into map[string][]*parse.PipeNode) {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, c := range n.Nodes {
			collectBindings(c, into)
		}
	case *parse.ActionNode:
		for _, v := range n.Pipe.Decl {
			into[v.Ident[0]] = append(into[v.Ident[0]], n.Pipe)
		}
	case *parse.IfNode:
		collectBindings(n.List, into)
		collectBindings(n.ElseList, into)
	case *parse.RangeNode:
		collectBindings(n.List, into)
		collectBindings(n.ElseList, into)
	case *parse.WithNode:
		collectBindings(n.List, into)
		collectBindings(n.ElseList, into)
	}
}

// lintVerdict says why a node is safe in a context, or "" when it is not.
func (lt *lintTemplate) safe(n parse.Node, ctx goContext, seen map[string]bool) string {
	switch n := n.(type) {
	case *parse.StringNode, *parse.NumberNode, *parse.BoolNode, *parse.NilNode:
		return "a template constant"
	case *parse.PipeNode:
		if n == nil || len(n.Cmds) == 0 {
			return ""
		}
		last := n.Cmds[len(n.Cmds)-1]
		if len(n.Cmds) > 1 && ctx == goCode {
			// printf fed by a pipe takes the piped value as its last argument.
			if id, ok := last.Args[0].(*parse.IdentifierNode); ok && id.Ident == "printf" {
				prefix := &parse.PipeNode{NodeType: parse.NodePipe, Cmds: n.Cmds[:len(n.Cmds)-1]}
				args := append(append([]parse.Node{}, last.Args[1:]...), prefix)
				return lt.safePrintf(args, seen)
			}
		}
		if len(n.Cmds) > 1 {
			// A function fed by a pipe: only the function's result matters,
			// and only escapers and code functions are safe whatever they are
			// fed. Nothing else is written that way in these templates.
			if id, ok := last.Args[0].(*parse.IdentifierNode); ok {
				if escapersFor[ctx][id.Ident] {
					return "escaped by " + id.Ident
				}
				if ctx == goCode && codeFuncs[id.Ident] != "" {
					return codeFuncs[id.Ident]
				}
			}
			return ""
		}
		return lt.safeCommand(last, ctx, seen)
	case *parse.CommandNode:
		return lt.safeCommand(n, ctx, seen)
	case *parse.FieldNode:
		return lt.safeField(n.Ident[len(n.Ident)-1], ctx)
	case *parse.ChainNode:
		if len(n.Field) == 0 {
			return lt.safe(n.Node, ctx, seen)
		}
		return lt.safeField(n.Field[len(n.Field)-1], ctx)
	case *parse.VariableNode:
		if len(n.Ident) > 1 {
			return lt.safeField(n.Ident[len(n.Ident)-1], ctx)
		}
		return lt.safeVariable(n.Ident[0], ctx, seen)
	case *parse.IdentifierNode:
		// A function called with no arguments.
		return lt.safeCommand(&parse.CommandNode{Args: []parse.Node{n}}, ctx, seen)
	}
	return ""
}

func (lt *lintTemplate) safeField(name string, ctx goContext) string {
	if why := inertFields[name]; why != "" {
		return why
	}
	if ctx == goFormatString {
		if why := formatFields[name]; why != "" {
			return why
		}
	}
	if ctx == goCode {
		if why := codeFields[name]; why != "" {
			return why
		}
	}
	return ""
}

func (lt *lintTemplate) safeVariable(name string, ctx goContext, seen map[string]bool) string {
	if why := varsBound[name]; why != "" {
		return why
	}
	pipes := lt.bindings[name]
	if len(pipes) == 0 || seen[name] {
		return ""
	}
	seen[name] = true
	defer delete(seen, name)
	why := ""
	for _, p := range pipes {
		// Every binding has to be safe, since the lint does not follow which
		// one reaches the use.
		why = lt.safe(p, ctx, seen)
		if why == "" {
			return ""
		}
	}
	return "bound to " + why
}

func (lt *lintTemplate) safeCommand(c *parse.CommandNode, ctx goContext, seen map[string]bool) string {
	if len(c.Args) == 0 {
		return ""
	}
	id, ok := c.Args[0].(*parse.IdentifierNode)
	if !ok {
		if len(c.Args) == 1 {
			return lt.safe(c.Args[0], ctx, seen)
		}
		return ""
	}
	name := id.Ident
	if name == "index" && len(c.Args) >= 2 {
		// An element of a collection is as safe as the collection.
		return lt.safe(c.Args[1], ctx, seen)
	}
	if escapersFor[ctx][name] {
		return "escaped by " + name
	}
	if why := inertFuncs[name]; why != "" {
		return why
	}
	if ctx == goCode {
		if why := codeFuncs[name]; why != "" {
			return why
		}
		if name == "printf" {
			return lt.safePrintf(c.Args[1:], seen)
		}
	}
	return ""
}

// safePrintf accepts a printf into code whose verbs are %q, which quotes
// whatever it is given, or %s and %d over arguments that are themselves safe
// in code.
func (lt *lintTemplate) safePrintf(args []parse.Node, seen map[string]bool) string {
	if len(args) == 0 {
		return ""
	}
	format, ok := args[0].(*parse.StringNode)
	if !ok {
		return ""
	}
	verbs := printfVerbs(format.Text)
	if verbs == nil || len(verbs) != len(args)-1 {
		return ""
	}
	for i, verb := range verbs {
		switch verb {
		case 'q':
		case 's', 'd':
			if lt.safe(args[i+1], goCode, seen) == "" {
				return ""
			}
		default:
			return ""
		}
	}
	return "printf of quoted or code-safe arguments"
}

// printfVerbs lists a format's verbs, or nil if it holds one this lint does
// not reason about (flags, widths, argument indexes).
func printfVerbs(format string) []byte {
	verbs := []byte{}
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		if i >= len(format) {
			return nil
		}
		switch format[i] {
		case '%':
		case 'q', 's', 'd':
			verbs = append(verbs, format[i])
		default:
			return nil
		}
	}
	return verbs
}

// TestTemplateActionsEscapeSchemaText is the lint. It fails for every action
// that writes into code, a string literal, a format literal or a struct tag a
// value it cannot show is safe there. A comment needs no entry: its guard
// escapes whatever reaches it, and that escaping is idempotent.
func TestTemplateActionsEscapeSchemaText(t *testing.T) {
	tmpl, byName := parseTemplatesForLint(t)
	an, err := gocontext.Analyze(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	counts := map[goContext]int{}
	for _, site := range an.Sites {
		counts[site.Context]++
		if site.Context == goLineComment {
			continue
		}
		lt := byName[site.Template]
		if lt.safe(site.Node.Pipe, site.Context, map[string]bool{}) == "" {
			problems = append(problems, fmt.Sprintf("%s: %s writes into a %s a value the lint cannot show is escaped; use %s", site.Location(), site.Node, site.Context, escaperHint(site.Context)))
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Errorf("%d template actions write unescaped values:\n%s", len(problems), strings.Join(problems, "\n"))
	}
	if counts[goCode] == 0 || counts[goLineComment] == 0 || counts[goString] == 0 || counts[goFormatString] == 0 || counts[goRawString] == 0 {
		t.Errorf("the analysis found no action in some context (%v); it has stopped seeing the templates", counts)
	}
	t.Logf("actions by context: %v", counts)
}

func escaperHint(ctx goContext) string {
	switch ctx {
	case goString:
		return "goStringLiteral"
	case goFormatString:
		return "fmtText, jsonErrorName or fmtCat"
	case goRawString:
		return "jsonTagName"
	case goCode:
		return "a quoting function (printf \"%q\", goStringQuote, numLit) or a code builder"
	}
	return "an escaper"
}

// TestLeastKeyArgumentsAreCode holds the four leastKeyCtx entries in
// codeFields to their claim. least_key_open and least_key_close write the
// context's fields into code as they stand, so what those fields hold is what
// every leastKey call passes, and each argument has to be code the lint accepts
// in its own right: a template constant, or an expression built from minted
// identifiers and the generator's own variables.
func TestLeastKeyArgumentsAreCode(t *testing.T) {
	_, byName := parseTemplatesForLint(t)
	calls := 0
	for name, lt := range byName {
		forEachCommand(lt.tree.Root, func(c *parse.CommandNode) {
			id, ok := c.Args[0].(*parse.IdentifierNode)
			if !ok || id.Ident != "leastKey" {
				return
			}
			calls++
			for _, arg := range c.Args[1:] {
				if lt.safe(arg, goCode, map[string]bool{}) == "" {
					loc, _ := lt.tree.ErrorContext(c)
					t.Errorf("%s (%s): leastKey argument %s is not shown to be code", loc, name, arg)
				}
			}
		})
	}
	if calls == 0 {
		t.Fatal("no leastKey call found; the walk has stopped seeing the templates")
	}
}

// forEachCommand visits every command in every pipeline under n, including
// the pipelines of if, range and with and the arguments of template calls.
func forEachCommand(n parse.Node, f func(*parse.CommandNode)) {
	var pipe func(p *parse.PipeNode)
	pipe = func(p *parse.PipeNode) {
		if p == nil {
			return
		}
		for _, c := range p.Cmds {
			f(c)
			for _, a := range c.Args {
				if sub, ok := a.(*parse.PipeNode); ok {
					pipe(sub)
				}
			}
		}
	}
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, c := range n.Nodes {
			forEachCommand(c, f)
		}
	case *parse.ActionNode:
		pipe(n.Pipe)
	case *parse.TemplateNode:
		pipe(n.Pipe)
	case *parse.IfNode:
		pipe(n.Pipe)
		forEachCommand(n.List, f)
		forEachCommand(n.ElseList, f)
	case *parse.RangeNode:
		pipe(n.Pipe)
		forEachCommand(n.List, f)
		forEachCommand(n.ElseList, f)
	case *parse.WithNode:
		pipe(n.Pipe)
		forEachCommand(n.List, f)
		forEachCommand(n.ElseList, f)
	}
}

// TestEveryActionIsGuarded holds the rewrite to its promise: after New, every
// action that writes output ends in the guard for the context it writes into.
// A guard missing from one action is that action's escaping resting on the
// template author again.
func TestEveryActionIsGuarded(t *testing.T) {
	e := mustNew(t)
	fresh, _ := parseTemplatesForLint(t)
	an, err := gocontext.Analyze(fresh)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[parse.Pos]string{}
	for _, site := range an.Sites {
		if want[site.Template] == nil {
			want[site.Template] = map[parse.Pos]string{}
		}
		want[site.Template][site.Node.Pos] = site.Context.Guard()
	}
	checked := 0
	for _, tt := range e.tmpl.Templates() {
		if tt.Tree == nil || tt.Tree.Root == nil {
			continue
		}
		forEachOutputAction(tt.Tree.Root, func(a *parse.ActionNode) {
			checked++
			guard := want[tt.Name()][a.Pos]
			last := a.Pipe.Cmds[len(a.Pipe.Cmds)-1]
			id, ok := last.Args[0].(*parse.IdentifierNode)
			if guard == "" || !ok || id.Ident != guard {
				loc, _ := tt.Tree.ErrorContext(a)
				t.Errorf("%s: %s does not end in its guard %q", loc, a, guard)
			}
		})
	}
	if checked != len(an.Sites) {
		t.Errorf("checked %d actions, the analysis found %d", checked, len(an.Sites))
	}
}
