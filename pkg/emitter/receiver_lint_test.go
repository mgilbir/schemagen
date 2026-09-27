package emitter

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
	"text/template/parse"

	"github.com/mgilbir/schemagen/pkg/emitter/internal/gocontext"
)

// TestTemplatesDeclareNoSingleLetterLocals keeps a method's locals out of the
// namespace its receiver is drawn from.
//
// receiverName names a method's receiver after the first letter of its type,
// so a type the schema calls "Vehicle" has methods on v. A template that
// declares a local v inside such a method shadows the receiver, and every use
// of the receiver after that reads the local instead:
//
//	if v, ok := raw["a,b"]; ok {
//		if err := json.Unmarshal(v, &v.AB); err != nil {
//
// which does not compile, or -- where the local happens to have a type the use
// accepts -- compiles and reads the wrong value: a multipleOf check on a type
// named Q... reported the quotient it had just computed as the value that
// failed. Every letter is some type's receiver, so the rule is that a template
// writing code inside a method declares no single-letter name at all. The
// helper templates emitted beside the types, which declare package-level
// functions with no receiver to shadow, are not held to it.
//
// The check reads the Go the templates write, with template actions and Go
// comments, strings and runes taken out, and looks for the declaration forms
// the templates use: `x :=`, `x, y :=`, `y, x :=`, `var x`, a function
// literal's parameters, and the loop variables a leastKey call names for
// least_key_open to declare.
func TestTemplatesDeclareNoSingleLetterLocals(t *testing.T) {
	tmpl, _ := parseTemplatesForLint(t)
	trees := map[string]*parse.Tree{}
	for _, tt := range tmpl.Templates() {
		if tt.Tree != nil && tt.Tree.Root != nil {
			trees[tt.Name()] = tt.Tree
		}
	}
	// What the per-schema file reaches is what can be inside a method.
	reach := map[string]bool{}
	var visit func(name string)
	visit = func(name string) {
		if reach[name] || trees[name] == nil {
			return
		}
		reach[name] = true
		forEachTemplateCall(trees[name].Root, visit)
	}
	visit("file.go.tmpl")
	if len(reach) < 20 {
		t.Fatalf("only %d templates reached from file.go.tmpl; the walk has stopped seeing them", len(reach))
	}

	decl := []*regexp.Regexp{
		regexp.MustCompile(`(?:^|[^\w.\x00])([a-zA-Z])\s*(?:,\s*[\w\x00]+\s*)*:=`),
		regexp.MustCompile(`[\w\x00]+\s*,\s*([a-zA-Z])\s*:=`),
		regexp.MustCompile(`\bvar\s+([a-zA-Z])\b`),
		regexp.MustCompile(`\bfunc\s*\(\s*([a-zA-Z])\s+[\w*\[\x00]`),
		regexp.MustCompile(`\bfunc\s*\([^)]*,\s*([a-zA-Z])\s+[\w*\[\x00]`),
	}
	var problems []string
	names := make([]string, 0, len(reach))
	for n := range reach {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		code := goCodeText(trees[name].Root)
		for li, line := range strings.Split(code, "\n") {
			for _, re := range decl {
				for _, m := range re.FindAllStringSubmatch(line, -1) {
					problems = append(problems, fmt.Sprintf("%s: declares %q in %q", name, m[1], strings.TrimSpace(strings.ReplaceAll(line, "\x00", "{{...}}"))))
				}
			}
			_ = li
		}
	}
	// least_key_open declares the loop variables its leastKey call names, so
	// those names reach the method body through an action rather than as text.
	for _, name := range names {
		forEachCommand(trees[name].Root, func(c *parse.CommandNode) {
			id, ok := c.Args[0].(*parse.IdentifierNode)
			if !ok || id.Ident != "leastKey" || len(c.Args) < 3 {
				return
			}
			for _, arg := range c.Args[1:3] {
				if sn, ok := arg.(*parse.StringNode); ok && len(sn.Text) == 1 && sn.Text != "_" {
					problems = append(problems, fmt.Sprintf("%s: leastKey declares %q in %s", name, sn.Text, c))
				}
			}
		})
	}
	if len(problems) > 0 {
		t.Errorf("templates that write method bodies declare single-letter locals, which shadow the receiver of any type whose name starts with that letter:\n%s", strings.Join(problems, "\n"))
	}
}

func forEachTemplateCall(n parse.Node, f func(string)) {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, c := range n.Nodes {
			forEachTemplateCall(c, f)
		}
	case *parse.TemplateNode:
		f(n.Name)
	case *parse.IfNode:
		forEachTemplateCall(n.List, f)
		forEachTemplateCall(n.ElseList, f)
	case *parse.RangeNode:
		forEachTemplateCall(n.List, f)
		forEachTemplateCall(n.ElseList, f)
	case *parse.WithNode:
		forEachTemplateCall(n.List, f)
		forEachTemplateCall(n.ElseList, f)
	}
}

// goCodeText is the Go code a template's text writes, in document order: each
// action becomes a NUL, and Go comments, strings and runes are dropped. Branches
// are concatenated, which is what a line-oriented search wants.
func goCodeText(root *parse.ListNode) string {
	var b strings.Builder
	st := gocontext.CodeState()
	var walk func(n parse.Node)
	walk = func(n parse.Node) {
		switch n := n.(type) {
		case *parse.ListNode:
			if n == nil {
				return
			}
			for _, c := range n.Nodes {
				walk(c)
			}
		case *parse.TextNode:
			text := string(n.Text)
			for i := 0; i < len(text); {
				before := st.Context()
				next, j := st.Step(text, i)
				if before == goCode && next.Context() == goCode || text[i] == '\n' {
					b.WriteString(text[i:j])
				}
				st, i = next, j
			}
		case *parse.ActionNode:
			if st.Context() == goCode {
				b.WriteByte(0)
			}
		case *parse.IfNode:
			walk(n.List)
			walk(n.ElseList)
		case *parse.RangeNode:
			walk(n.List)
			walk(n.ElseList)
		case *parse.WithNode:
			walk(n.List)
			walk(n.ElseList)
		}
	}
	walk(root)
	return b.String()
}
