package emitter

import (
	"errors"
	"fmt"
	"go/scanner"
	"go/token"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mgilbir/schemagen/pkg/emitter/internal/gocontext"
	"github.com/mgilbir/schemagen/pkg/generator"
)

// The Go contexts, under the names this package's guards and tests use.
type goContext = gocontext.Context

const (
	goCode         = gocontext.Code
	goLineComment  = gocontext.LineComment
	goBlockComment = gocontext.BlockComment
	goString       = gocontext.String
	goFormatString = gocontext.FormatString
	goRawString    = gocontext.RawString
	goRune         = gocontext.Rune
)

// This file holds the escapers the templates call and the guards New appends
// to every action (see internal/gocontext and guards_gen.go).
//
// Every escaper returns a type that names the context its output is safe in,
// and each guard accepts that type and no other escaper's. A value that is not
// one of them is accepted only where escaping it would not change it (see
// isInert), which is what a Go identifier, a keyword or a number is: those need
// no ceremony, and anything else that reaches a literal unescaped fails
// generation instead of reaching the file. The one exception is a comment,
// whose escaping is idempotent and changes no meaning, so the comment guard
// escapes what it is given rather than refusing it.
//
// The contexts and their rules:
//
//   - Code. What an action writes into code is an identifier the naming layer
//     minted, a literal a function here rendered (strconv.Quote, a number the
//     schema wrote), or a fragment of code the generator built. The guard
//     cannot tell those from schema text, so it checks what it can: the value
//     must scan as Go tokens with go/scanner -- no unterminated literal, no
//     comment, no character Go source cannot hold. The static lint is what
//     keeps schema text out of this context.
//   - Line comment: commentText, from comment and commentLine. See
//     escapeCommentText for the exact rule.
//   - Interpreted string literal: stringText, from goStringLiteral, which is
//     strconv.Quote without its quotes.
//   - A fmt format literal: formatText, from jsonErrorName and fmtText, which
//     are the same with every "%" doubled, and from the path builders, which
//     add their own verbs. fmtCat joins formatText without laundering it
//     through printf.
//   - Raw string literal, which is only ever a struct tag: tagText, from
//     jsonTagName, which refuses a name encoding/json would not read back.
//   - Block comment and rune literal: no action may write into either;
//     the context analysis refuses such a template, so guardgen cannot
//     write a table for it.

// commentText is text that may follow `// ` on a line of Go source.
type commentText string

// stringText is text that may stand between the quotes of a Go interpreted
// string literal that is not a fmt format.
type stringText string

// formatText is text that may stand between the quotes of a fmt format
// literal: escaped as stringText is, and holding no verb but the ones the
// generator put there itself.
type formatText string

// tagText is a JSON member name as it may stand in `json:"..."`.
type tagText string

// escapeCommentText renders arbitrary text as the remainder of one Go line
// comment. The rule:
//
//   - A rune that unicode.IsGraphic accepts (letters, marks, numbers,
//     punctuation, symbols and space separators) is kept, as is a tab.
//   - Every other rune, and every byte that is not valid UTF-8, is written as
//     strconv.Quote would write it: `\n`, `\x00`, `\ufeff`, `\u2028`, `\u202e`.
//
// That covers everything that could end the comment (a line feed; a carriage
// return, which gofmt turns into one), everything Go source may not contain
// (NUL, a byte order mark, invalid UTF-8), the line and paragraph separators
// editors break lines at, and the bidirectional controls that can make a
// comment display as something other than what it is (CVE-2021-42574). "*/" is
// kept: it ends nothing in a line comment, and no action may write into a
// block comment. The rule is idempotent -- its output is all graphic runes and
// tabs, which it keeps -- and that is what lets the comment guard apply it to a
// value that may already have been through it.
func escapeCommentText(s string) string {
	clean := true
	for _, r := range s {
		if r == utf8.RuneError || (r != '\t' && !unicode.IsGraphic(r)) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '\t' || unicode.IsGraphic(r):
			b.WriteString(s[i : i+size])
		default:
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		}
		i += size
	}
	return b.String()
}

// commentFunc renders text as the tail of a Go line comment. Text spanning
// several lines (schema descriptions may contain newlines) is continued with
// a "//" prefix at the given indent; each line is then held to
// escapeCommentText, so nothing the text holds can end the comment early.
func commentFunc(indent string, v any) commentText {
	text := printedValue(v)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = escapeCommentText(l)
	}
	return commentText(strings.Join(lines, "\n"+indent+"// "))
}

// commentLineFunc renders a value as part of one comment line: a line break
// in it is written as `\n` rather than starting a new line.
func commentLineFunc(v any) commentText {
	return commentText(escapeCommentText(printedValue(v)))
}

// goStringLiteralFunc escapes a value for use inside a Go double-quoted
// string literal that is not a fmt format.
func goStringLiteralFunc(v any) stringText {
	q := strconv.Quote(printedValue(v))
	return stringText(q[1 : len(q)-1])
}

// jsonErrorNameFunc escapes a JSON property name for safe embedding inside a Go
// double-quoted fmt format string. It applies Go string-literal escaping
// (quotes, backslashes, control characters) and doubles percent signs so they
// are not interpreted as format verbs. Without this, a property name containing
// a quote, backslash, or percent produces uncompilable or misformatted code.
func jsonErrorNameFunc(s string) formatText {
	return fmtTextFunc(s)
}

// fmtTextFunc is jsonErrorName for any value: a schema's keyword argument, a
// pattern, a bound, as it is to be printed inside an error message.
func fmtTextFunc(v any) formatText {
	return formatText(strings.ReplaceAll(string(goStringLiteralFunc(v)), "%", "%%"))
}

// fmtCatFunc joins format text. The parts are formatText, and the template
// engine converts a constant written in the template to one but refuses a
// string computed at run time, so a schema value cannot be joined in without
// going through fmtText or jsonErrorName first. printf would join anything,
// and is what this replaces wherever the result is a format.
func fmtCatFunc(parts ...formatText) formatText {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(string(p))
	}
	return formatText(b.String())
}

// jsonTagNameFunc renders a JSON member name for the name position of a
// `json:"..."` struct tag.
//
// It refuses a name the tag cannot carry. The generator reads and writes such
// a name by hand (FieldDef.ManualJSON, generator.TagNameRepresentable), so a
// refusal here means a template reached a name that decision never saw, and
// generation stops rather than emitting a tag encoding/json would silently read
// as another name or none.
func jsonTagNameFunc(name string) (tagText, error) {
	if !generator.TagNameIsRepresentable(name) {
		return "", fmt.Errorf("emitter: property name %q cannot be carried by a struct tag and was not routed to the hand-written JSON path", name)
	}
	return tagText(name), nil
}

// rawStringFunc renders a value as a Go string literal: a raw one when the
// value can be one, so that a struct tag reads as tags are written, and an
// interpreted one otherwise.
func rawStringFunc(s string) string {
	if !strings.ContainsAny(s, "`\r") && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\ufeff") {
		return "`" + s + "`"
	}
	return strconv.Quote(s)
}

// numLitFunc renders a number the schema wrote as a Go number literal, as
// text/template would have printed it, and refuses anything that is not a
// number. It is what a bound written into code goes through, so that a rule
// whose value is unexpectedly a string -- which a bare {{.Value}} would have
// written into the source verbatim -- fails generation instead.
func numLitFunc(v any) (string, error) {
	if generator.JSONNumberLiteral(v) == "" {
		return "", fmt.Errorf("%w: %T %q where a number was expected", errEscape, v, printedValue(v))
	}
	s := printedValue(v)
	if !jsonNumberPattern.MatchString(s) {
		return "", fmt.Errorf("%w: %q is not a number literal", errEscape, s)
	}
	return s, nil
}

// jsonNumberPattern is the JSON number grammar, which every such literal is
// also a Go literal under.
var jsonNumberPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// isInert reports whether escaping s for any context this file knows would
// leave it unchanged: valid UTF-8 of printable runes, none of them a quote, a
// backslash, a backtick or a percent sign. An identifier, a keyword and a
// decimal number are all inert, which is why they may be written anywhere
// without an escaper; so are ordinary words and spaces.
func isInert(s string) bool {
	for _, r := range s {
		if r == utf8.RuneError || !strconv.IsPrint(r) {
			return false
		}
		switch r {
		case '"', '\\', '`', '%':
			return false
		}
	}
	return true
}

// isTagIdent reports whether s may stand in a struct tag unescaped: ASCII
// letters, digits and underscores, which is what an option word is.
func isTagIdent(s string) bool {
	for i := 0; i < len(s); i++ {
		if !gocontext.IsIdentByte(s[i]) {
			return false
		}
	}
	return true
}

// printedValue renders a value the way text/template prints the result of an
// action, so that appending a guard to an action does not change what a
// value that needs no escaping looks like.
func printedValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case commentText:
		return string(x)
	case stringText:
		return string(x)
	case formatText:
		return string(x)
	case tagText:
		return string(x)
	case nil:
		return "<no value>"
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			if rv.Kind() == reflect.Pointer {
				return "<nil>"
			}
			return "<no value>"
		}
		if rv.Type().Implements(errorType) || rv.Type().Implements(stringerType) {
			break
		}
		rv = rv.Elem()
	}
	return fmt.Sprint(rv.Interface())
}

var (
	errorType    = reflect.TypeFor[error]()
	stringerType = reflect.TypeFor[fmt.Stringer]()
)

// errEscape is wrapped by every guard refusal, so that a caller can tell a
// template that reached a value it cannot write from any other failure.
var errEscape = errors.New("value cannot be written here without an escaper")

func wrongContext(ctx goContext, v any) error {
	return fmt.Errorf("%w: a %T in a %s", errEscape, v, ctx)
}

// guardComment is appended to every action that writes into a line comment.
func guardComment(v any) (string, error) {
	switch x := v.(type) {
	case commentText:
		return string(x), nil
	case stringText, formatText, tagText:
		return "", wrongContext(goLineComment, v)
	}
	return escapeCommentText(printedValue(v)), nil
}

// guardString is appended to every action that writes into a string literal
// that is not a format.
func guardString(v any) (string, error) {
	switch x := v.(type) {
	case stringText:
		return string(x), nil
	case commentText, formatText, tagText:
		return "", wrongContext(goString, v)
	}
	s := printedValue(v)
	if !isInert(s) {
		return "", fmt.Errorf("%w: %q in a %s; pass it through goStringLiteral", errEscape, s, goString)
	}
	return s, nil
}

// guardFormat is appended to every action that writes into a fmt format
// literal.
func guardFormat(v any) (string, error) {
	switch x := v.(type) {
	case formatText:
		return string(x), nil
	case commentText, stringText, tagText:
		return "", wrongContext(goFormatString, v)
	}
	s := printedValue(v)
	if !isInert(s) {
		return "", fmt.Errorf("%w: %q in a %s; pass it through fmtText or jsonErrorName", errEscape, s, goFormatString)
	}
	return s, nil
}

// guardRaw is appended to every action that writes into a raw string literal,
// which in these templates is always a struct tag.
func guardRaw(v any) (string, error) {
	switch x := v.(type) {
	case tagText:
		return string(x), nil
	case commentText, stringText, formatText:
		return "", wrongContext(goRawString, v)
	}
	s := printedValue(v)
	if !isTagIdent(s) {
		return "", fmt.Errorf("%w: %q in a struct tag; pass it through jsonTagName", errEscape, s)
	}
	return s, nil
}

// guardCode is appended to every action that writes into code.
func guardCode(v any) (string, error) {
	switch v.(type) {
	case commentText, stringText, formatText, tagText:
		return "", wrongContext(goCode, v)
	}
	s := printedValue(v)
	if err := checkCodeFragment(s); err != nil {
		return "", fmt.Errorf("%w: %q in code: %v", errEscape, s, err)
	}
	return s, nil
}

// checkCodeFragment scans s with Go's own scanner and refuses anything that
// would not stay the tokens it looks like: a scan error (an unterminated
// literal, a NUL, a byte order mark, invalid UTF-8, a character no token
// begins with) or a comment, which would swallow what the template writes
// after it.
func checkCodeFragment(s string) error {
	if isPlainCode(s) {
		return nil
	}
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(s))
	var firstErr error
	var sc scanner.Scanner
	sc.Init(file, []byte(s), func(_ token.Position, msg string) {
		if firstErr == nil {
			firstErr = errors.New(msg)
		}
	}, scanner.ScanComments)
	for {
		_, tok, _ := sc.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.COMMENT {
			return errors.New("it holds a comment")
		}
		if tok == token.ILLEGAL && firstErr == nil {
			firstErr = errors.New("it holds a character no Go token begins with")
		}
	}
	return firstErr
}

// isPlainCode is checkCodeFragment's fast path: ASCII with no character that
// can begin a literal or a comment scans as the tokens it looks like.
func isPlainCode(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= utf8.RuneSelf || c == '"' || c == '\'' || c == '`' || c == '/' || c == '\\' || c < ' ' && c != '\t' && c != '\n' || c == 0x7f {
			return false
		}
	}
	return true
}
