// Package gocontext is the half of the emitter that knows where in the Go
// source each template action writes.
//
// The templates build Go source as text, and a value a template interpolates
// means something different depending on where it lands: inside a `//` comment
// a newline ends the comment and whatever follows is compiled; inside a "..."
// literal a quote ends the literal, a backslash starts an escape and, when the
// literal is a fmt format, a percent sign starts a verb; inside a `...` struct
// tag a backtick ends the tag. Escaping used to be a function each template
// author had to remember to call at each interpolation, and where it was
// forgotten a schema's own text became code: a property name in
// dependentSchemas holding a newline ended the comment it was written into and
// the rest of the name was compiled into Validate, and a $ref string did the
// same at the top of the file under --lenient-refs, where an import is legal.
//
// Remembering is not a mechanism, so the emitter does not rely on it. Analyze
// walks every template with a small model of the Go lexer -- code, line
// comment, block comment, interpreted string, raw string, rune -- and works
// out, for every action that writes output, which of those it writes into.
// The emitter appends to each such action the guard for its context (see
// goescape.go in the parent package), in the way html/template makes its
// contextual escaping impossible to skip. The guard is what makes a forgotten
// escaper unable to produce wrong source: the worst it can do is fail
// generation with an error that names the template. The emitter's
// TestTemplateActionsEscapeSchemaText, which runs under `go test`, is what
// makes it fail before any schema reaches it.
//
// The analysis does not run when a program generates code. It costs more than
// the rest of building the emitter put together, and its answer is a function
// of the embedded templates alone, so it is computed once, by guardgen (`go
// generate ./pkg/emitter`), and checked in as a table: guards_gen.go. The
// emitter's TestGuardTableIsCurrent recomputes it and fails when the
// checked-in table differs, and the emitter refuses to start when the
// templates it embeds are not the ones the table was computed from.
//
// The model is deliberately a model of the *text*. It does not know what a
// value is going to be, only where it will go, and it assumes -- and the code
// guard checks -- that what an action writes into code is balanced: it opens
// no literal or comment it does not close.
package gocontext

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"
)

// Context is where in Go source a template action writes.
type Context uint8

const (
	Code Context = iota
	LineComment
	BlockComment
	String       // an interpreted "..." literal that is not a fmt format
	FormatString // an interpreted "..." literal that is a fmt format
	RawString    // a `...` literal; struct tags are the only ones with actions in them
	Rune
)

func (c Context) String() string {
	switch c {
	case Code:
		return "code"
	case LineComment:
		return "line comment"
	case BlockComment:
		return "block comment"
	case String:
		return "string literal"
	case FormatString:
		return "format string literal"
	case RawString:
		return "raw string literal"
	case Rune:
		return "rune literal"
	}
	return fmt.Sprintf("Context(%d)", uint8(c))
}

// Guard names the function the emitter appends to an action that
// writes into c, or "" for a context no action may write into.
func (c Context) Guard() string {
	switch c {
	case Code:
		return "_goCode"
	case LineComment:
		return "_goComment"
	case String:
		return "_goString"
	case FormatString:
		return "_goFormat"
	case RawString:
		return "_goRaw"
	}
	return ""
}

// State is one state of the Go lexer model: the context, and the little
// the model remembers about what came before.
//
// Two contexts need to look back. In code, the text before a `"` decides
// whether the literal is a fmt format: the model follows calls to the
// functions in formatFuncs (and to the ones formatCallee recognises an
// action naming) far enough to know which argument a literal is. In a line
// comment, whether anything has been written since the `//` decides whether an
// action would write the first characters of the comment, which is where
// `//go:` and `//line` directives are recognised.
//
// Everything is kept as a few small integers and the identifier being read, not
// as the text itself: the arms of every {{if}} would otherwise leave a
// different tail behind, and the number of states would double at each one.
type State struct {
	ctx Context
	// ident is the identifier the text is in the middle of, in code.
	ident string
	// pending is the format argument index of the function whose name was the
	// last token, or -1.
	pending int8
	// call is the call to a format function the text is inside: which
	// argument is the format, which argument the text is at, and how deeply
	// nested in parentheses within that argument. want is -1 outside one.
	want, arg, depth int8
	// commented is set in a line comment once anything follows the "//".
	commented bool
}

// formatFuncs are the fmt-style functions the templates call, and which
// argument of each is the format.
var formatFuncs = map[string]int8{
	"Errorf":          0,
	"Sprintf":         0,
	"jsonValueErrorf": 0,
	"jsonElemErrorf":  0,
	"jsonStepErrorf":  0,
	"oneofErrf":       0,
	"jsonPathf":       1,
	"jsonElemPathf":   1,
}

// maxIdent bounds the identifier the model remembers; no name in formatFuncs
// is longer.
const maxIdent = 24

func CodeState() State { return State{ctx: Code, pending: -1, want: -1} }

// flushIdent ends the identifier being read, which may be a format function's
// name.
func (s State) flushIdent() State {
	if s.ident == "" {
		return s
	}
	if idx, ok := formatFuncs[s.ident]; ok {
		s.pending = idx
	} else {
		s.pending = -1
	}
	s.ident = ""
	return s
}

// advance runs the lexer model over template text.
func (s State) advance(text string) State {
	for i := 0; i < len(text); {
		s, i = s.step(text, i)
	}
	return s
}

// step runs the lexer model over the character at text[i] -- two, for a
// comment's opener or closer and for an escape -- and returns the state after
// it and the index of what follows.
func (s State) step(text string, i int) (State, int) {
	ch := text[i]
	switch s.ctx {
	case Code:
		if IsIdentByte(ch) {
			// The whole run of identifier characters at once: this is the
			// model's hot loop, and a byte at a time allocated a string per byte.
			j := i + 1
			for j < len(text) && IsIdentByte(text[j]) {
				j++
			}
			if s.ident == "~" || len(s.ident)+j-i > maxIdent {
				s.ident = "~"
			} else {
				s.ident += text[i:j]
			}
			return s, j
		}
		s = s.flushIdent()
		switch {
		case ch == '/' && i+1 < len(text) && text[i+1] == '/':
			s.ctx = LineComment
			s.commented = false
			i++
		case ch == '/' && i+1 < len(text) && text[i+1] == '*':
			s.ctx = BlockComment
			i++
		case ch == '"':
			if s.want >= 0 && s.depth == 0 && s.arg == s.want {
				s.ctx = FormatString
			} else {
				s.ctx = String
			}
			s.pending = -1
		case ch == '`':
			s.ctx = RawString
			s.pending = -1
		case ch == '\'':
			s.ctx = Rune
			s.pending = -1
		case ch == ' ' || ch == '\t' || ch == '\n':
			// Space between a name and its "(" changes nothing, and a call's
			// arguments may run over several lines.
		case ch == '(':
			if s.pending >= 0 {
				s.want, s.arg, s.depth = s.pending, 0, 0
			} else if s.want >= 0 {
				s.depth++
			}
			s.pending = -1
		case ch == ')':
			if s.want >= 0 {
				if s.depth == 0 {
					s.want = -1
				} else {
					s.depth--
				}
			}
			s.pending = -1
		case ch == ',':
			if s.want >= 0 && s.depth == 0 {
				s.arg++
			}
			s.pending = -1
		case ch == '{' || ch == '}' || ch == ';':
			// A statement boundary: whatever call was open is not one the
			// model understood.
			s.want, s.pending = -1, -1
		default:
			s.pending = -1
		}
	case LineComment:
		if ch == '\n' {
			s.ctx = Code
			s.commented = false
			return s, i + 1
		}
		// The rest of the comment up to the line feed at once; nothing in it
		// changes the state but the fact that it is there.
		s.commented = true
		if j := strings.IndexByte(text[i:], '\n'); j >= 0 {
			return s, i + j
		}
		return s, len(text)
	case BlockComment:
		if j := strings.Index(text[i:], "*/"); j >= 0 {
			s.ctx = Code
			return s, i + j + 2
		}
		return s, len(text)
	case String, FormatString:
		switch ch {
		case '\\':
			i++
		case '"':
			s.ctx = Code
		default:
			if j := strings.IndexAny(text[i:], "\\\""); j >= 0 {
				return s, i + j
			}
			return s, len(text)
		}
	case Rune:
		switch ch {
		case '\\':
			i++
		case '\'':
			s.ctx = Code
		}
	case RawString:
		if j := strings.IndexByte(text[i:], '`'); j >= 0 {
			s.ctx = Code
			return s, i + j + 1
		}
		return s, len(text)
	}
	return s, i + 1
}

// afterAction is the state after an action has written into s.
func (s State) afterAction(a *parse.ActionNode) State {
	switch s.ctx {
	case Code:
		s.ident = ""
		if idx, ok := formatCallee(a); ok {
			s.pending = idx
		} else {
			s.pending = -1
		}
	case LineComment:
		s.commented = true
	}
	return s
}

func IsIdentByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// formatCallee reports whether an action writes the name of a fmt-style
// function -- the error constructors pathErrf and pathJoin choose between, and
// the variables the templates bind them to -- and which argument is the format.
func formatCallee(a *parse.ActionNode) (int8, bool) {
	if len(a.Pipe.Cmds) != 1 || len(a.Pipe.Cmds[0].Args) == 0 {
		return 0, false
	}
	name := ""
	switch n := a.Pipe.Cmds[0].Args[0].(type) {
	case *parse.VariableNode:
		if len(n.Ident) == 1 {
			name = n.Ident[0]
		}
	case *parse.IdentifierNode:
		name = n.Ident
	}
	switch name {
	case "$errf", "pathErrf":
		// fmt.Errorf or jsonElemErrorf.
		return 0, true
	case "$joinf", "pathJoin":
		// jsonPathf or jsonElemPathf.
		return 1, true
	}
	return 0, false
}

// stateSet is a set of lexer states. A template branches, and the two arms of
// an {{if}} can leave the lexer in different states -- one arm writes a
// comment line and the other nothing -- so the analysis carries every state the
// text can be in and requires only that an action see one context.
//
// It is a slice without duplicates rather than a map: a set here rarely holds
// more than two or three states, and the analysis builds one for every node of
// every template on every round, so a map's allocation was most of what the
// analysis cost.
type stateSet []State

func singleton(s State) stateSet { return stateSet{s} }

func (a stateSet) has(s State) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

func (a stateSet) add(s State) stateSet {
	if a.has(s) {
		return a
	}
	return append(a, s)
}

func (a stateSet) union(b stateSet) stateSet {
	out := make(stateSet, len(a), len(a)+len(b))
	copy(out, a)
	for _, s := range b {
		out = out.add(s)
	}
	return out
}

func (a stateSet) contains(b stateSet) bool {
	for _, s := range b {
		if !a.has(s) {
			return false
		}
	}
	return true
}

func (a stateSet) contexts() []Context {
	var out []Context
	for _, s := range a {
		if !slices.Contains(out, s.ctx) {
			out = append(out, s.ctx)
		}
	}
	slices.Sort(out)
	return out
}

func (a stateSet) mapStates(f func(State) State) stateSet {
	out := make(stateSet, 0, len(a))
	for _, s := range a {
		out = out.add(f(s))
	}
	return out
}

// Site is one template action that writes output, and where it writes.
type Site struct {
	Template string
	Node     *parse.ActionNode
	Context  Context
	// CommentStart is set for an action in a line comment with nothing but the
	// `//` before it on its line: the one place a value could spell a
	// directive such as `//go:generate`.
	CommentStart bool
	tree         *parse.Tree
}

// Location renders the action's position as "file:line:col".
func (s Site) Location() string {
	loc, _ := s.tree.ErrorContext(s.Node)
	return loc
}

// Analysis is the result of Analyze.
type Analysis struct {
	Sites []Site
}

// Analyze works out the Go lexical context of every output action of
// every template in t.
//
// A {{define}} is entered in every state its callers are in, and a template
// nobody calls is entered in code. A template may be entered in more than one
// context -- the doc-comment templates are called on the heels of a comment
// line and of code alike, and begin with a newline that ends either -- and
// what is required is only that each action in it write into one. Entry and
// exit states are found by iterating to a fixed point, since a template's exit
// state is needed by its callers and its entry state is set by them.
func Analyze(t *template.Template) (*Analysis, error) {
	trees := map[string]*parse.Tree{}
	for _, tt := range t.Templates() {
		if tt.Tree != nil && tt.Tree.Root != nil {
			trees[tt.Name()] = tt.Tree
		}
	}
	names := make([]string, 0, len(trees))
	for n := range trees {
		names = append(names, n)
	}
	sort.Strings(names)

	w := &contextWalker{
		textMemo: map[textMemoKey]State{},
		trees:    trees,
		entry:    map[string]stateSet{},
		exit:     map[string]stateSet{},
	}
	for _, n := range names {
		w.entry[n] = singleton(CodeState())
	}

	// Each round can only add states to an entry or exit set, and the sets are
	// finite, so this terminates; the bound is a backstop against a defect in
	// that argument rather than a limit any real template set approaches.
	const maxRounds = 64
	for round := 0; ; round++ {
		if round == maxRounds {
			return nil, fmt.Errorf("emitter: template context analysis did not settle after %d rounds", maxRounds)
		}
		w.sites = map[*parse.ActionNode]*siteAcc{}
		w.calls = map[string]stateSet{}
		w.errs = nil
		changed := false
		for _, n := range names {
			w.current = n
			out := w.walk(trees[n].Root, w.entry[n])
			if !w.exit[n].contains(out) || !out.contains(w.exit[n]) {
				w.exit[n] = out
				changed = true
			}
		}
		// maporder: each callee's entry is set from its own callers alone, and the fixed point it iterates to is the same in any order.
		for callee, states := range w.calls {
			if _, ok := trees[callee]; !ok {
				continue
			}
			normalized := states.mapStates(func(s State) State { st := CodeState(); st.ctx = s.ctx; return st })
			if !w.entry[callee].contains(normalized) || !normalized.contains(w.entry[callee]) {
				// A template that is called is entered only in its callers'
				// contexts; the default code entry is for the uncalled.
				w.entry[callee] = normalized
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	var errs []string
	errs = append(errs, w.errs...)
	res := &Analysis{}
	// maporder: sites and errors are both sorted before they leave this function.
	for node, acc := range w.sites {
		ctxs := acc.states.contexts()
		site := Site{Template: acc.template, Node: node, tree: acc.tree}
		// Only an error names the position, and working one out scans the
		// template's text from the start, so it is not done for every site.
		loc := func() string { return site.Location() }
		if len(ctxs) != 1 {
			errs = append(errs, fmt.Sprintf("%s: action %s writes into more than one Go context (%v); restructure the template so that each action has one", loc(), node, ctxs))
			continue
		}
		site.Context = ctxs[0]
		if site.Context.Guard() == "" {
			errs = append(errs, fmt.Sprintf("%s: action %s writes into a %s, which no action may write into", loc(), node, site.Context))
			continue
		}
		if site.Context == LineComment {
			for _, s := range acc.states {
				if !s.commented {
					site.CommentStart = true
				}
			}
			if site.CommentStart {
				errs = append(errs, fmt.Sprintf("%s: action %s writes the first characters of a // comment, where a value could spell a directive such as //go:generate; put a space or other text after the //", loc(), node))
				continue
			}
		}
		res.Sites = append(res.Sites, site)
	}
	sort.Slice(res.Sites, func(i, j int) bool {
		a, b := res.Sites[i], res.Sites[j]
		if a.Template != b.Template {
			return a.Template < b.Template
		}
		return a.Node.Pos < b.Node.Pos
	})
	if len(errs) > 0 {
		sort.Strings(errs)
		return nil, fmt.Errorf("emitter: templates write values where they cannot be escaped:\n\t%s", strings.Join(errs, "\n\t"))
	}
	return res, nil
}

type siteAcc struct {
	template string
	tree     *parse.Tree
	states   stateSet
}

type textMemoKey struct {
	in   State
	node *parse.TextNode
}

type contextWalker struct {
	textMemo map[textMemoKey]State
	trees    map[string]*parse.Tree
	entry    map[string]stateSet
	exit     map[string]stateSet
	sites    map[*parse.ActionNode]*siteAcc
	calls    map[string]stateSet
	current  string
	errs     []string
}

func (w *contextWalker) walk(n parse.Node, in stateSet) stateSet {
	switch n := n.(type) {
	case nil:
		return in
	case *parse.ListNode:
		if n == nil {
			return in
		}
		for _, c := range n.Nodes {
			in = w.walk(c, in)
		}
		return in
	case *parse.TextNode:
		return in.mapStates(func(s State) State {
			// The fixed point walks every template once a round, and a state
			// entering a text node leaves it the same way every time.
			key := textMemoKey{s, n}
			if out, ok := w.textMemo[key]; ok {
				return out
			}
			out := s.advance(string(n.Text))
			w.textMemo[key] = out
			return out
		})
	case *parse.ActionNode:
		if len(n.Pipe.Decl) > 0 {
			// {{$x := ...}} and {{$x = ...}} write nothing.
			return in
		}
		acc := w.sites[n]
		if acc == nil {
			acc = &siteAcc{template: w.current, tree: w.trees[w.current], states: stateSet{}}
			w.sites[n] = acc
		}
		acc.states = acc.states.union(in)
		return in.mapStates(func(s State) State { return s.afterAction(n) })
	case *parse.IfNode:
		return w.branch(n.List, n.ElseList, in)
	case *parse.WithNode:
		return w.branch(n.List, n.ElseList, in)
	case *parse.RangeNode:
		// The body runs zero or more times, so the state it starts in is the
		// state before the range joined with the state it ends in.
		body := in
		var end stateSet
		for i := 0; ; i++ {
			end = w.walk(n.List, body)
			if body.contains(end) || i > 64 {
				break
			}
			body = body.union(end)
		}
		after := in
		if n.ElseList != nil {
			after = w.walk(n.ElseList, in)
		}
		return after.union(end)
	case *parse.TemplateNode:
		prev := w.calls[n.Name]
		if prev == nil {
			prev = stateSet{}
		}
		w.calls[n.Name] = prev.union(in)
		if out, ok := w.exit[n.Name]; ok && len(out) > 0 {
			// What the callee leaves on its last line is unknown to this
			// caller's look-back; only the context carries over.
			return out.mapStates(func(s State) State { st := CodeState(); st.ctx = s.ctx; return st })
		}
		return in.mapStates(func(s State) State { st := CodeState(); st.ctx = s.ctx; return st })
	case *parse.CommentNode, *parse.BreakNode, *parse.ContinueNode:
		return in
	}
	w.errs = append(w.errs, fmt.Sprintf("template %q: unhandled node %T", w.current, n))
	return in
}

func (w *contextWalker) branch(then, els *parse.ListNode, in stateSet) stateSet {
	a := w.walk(then, in)
	b := in
	if els != nil {
		b = w.walk(els, in)
	}
	return a.union(b)
}

// Step runs the lexer model over the character at text[i], as advance does
// one step of, for a caller that needs to see each context change.
func (s State) Step(text string, i int) (State, int) { return s.step(text, i) }

// Context is the context the state is in.
func (s State) Context() Context { return s.ctx }
