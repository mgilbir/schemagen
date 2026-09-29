package emitter

import (
	"errors"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// commentHostile are strings that could end a comment early or that Go source
// cannot hold. The escapes are written as bytes so this file holds none of
// the characters it is about.
var commentHostile = []string{
	"a\nb", "a\rb", "a\r\nb",
	"a\x00b",
	"\xef\xbb\xbfa",  // U+FEFF, a byte order mark
	"a\xe2\x80\xa8b", // U+2028, line separator
	"a\xe2\x80\xa9b", // U+2029, paragraph separator
	"a\xe2\x80\xaeb", // U+202E, right-to-left override
	"a\xffb",         // not UTF-8
	"a*/b", "a/*b", "go:generate x",
	"\x7f\x1b[31m",
}

// TestEscapeCommentText pins the comment rule: graphic runes and tabs are kept,
// everything else is written the way strconv.Quote writes it.
func TestEscapeCommentText(t *testing.T) {
	for in, want := range map[string]string{
		"plain words, é, 日本語, 🎉": "plain words, é, 日本語, 🎉",
		"tab\there":              "tab\there",
		"a\nb":                   `a\nb`,
		"a\r\nb":                 `a\r\nb`,
		"a\x00b":                 `a\x00b`,
		"\xef\xbb\xbfa":          `\ufeffa`,
		"a\xe2\x80\xa8b":         `a\u2028b`,
		"a\xe2\x80\xaeb":         `a\u202eb`,
		"a\xffb":                 `a\xffb`,
		"a*/b":                   "a*/b",
		`back\slash "quoted"`:    `back\slash "quoted"`,
	} {
		if got := escapeCommentText(in); got != want {
			t.Errorf("escapeCommentText(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCommentEscapingIsSafeAndIdempotent puts every hostile string through both
// comment escapers into a real comment and parses the file: the comment has to
// stay one comment, and escaping twice has to change nothing, which is what
// lets the comment guard escape a value that has already been escaped.
func TestCommentEscapingIsSafeAndIdempotent(t *testing.T) {
	for _, h := range commentHostile {
		line := escapeCommentText(h)
		if again := escapeCommentText(line); again != line {
			t.Errorf("escapeCommentText is not idempotent on %q: %q then %q", h, line, again)
		}
		para := string(commentFunc("\t", h))
		for _, text := range []string{line, para} {
			src := "package p\n\n// " + text + "\nvar x = 1\n"
			f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.ParseComments)
			if err != nil {
				t.Errorf("%q escaped to %q, which does not parse: %v", h, text, err)
				continue
			}
			if len(f.Decls) != 1 {
				t.Errorf("%q escaped to %q, and the file now declares %d things", h, text, len(f.Decls))
			}
		}
	}
}

func TestCommentFuncContinuesLines(t *testing.T) {
	got := string(commentFunc("\t", "one\r\ntwo\rthree\nfour\x00"))
	want := "one\n\t// two\n\t// three\n\t// four\\x00"
	if got != want {
		t.Errorf("commentFunc = %q, want %q", got, want)
	}
}

// TestGuardsRefuseWhatTheyCannotWrite holds each guard to its contract: its
// own escaper's type passes, another context's does not, and an untyped value
// passes only where escaping it would change nothing.
func TestGuardsRefuseWhatTheyCannotWrite(t *testing.T) {
	type call struct {
		name  string
		guard func(any) (string, error)
		in    any
		ok    bool
	}
	for _, c := range []call{
		{"comment/plain", guardComment, "Name", true},
		{"comment/newline", guardComment, "a\nb", true}, // escaped, not refused
		{"comment/typed", guardComment, commentText("x\n// y"), true},
		{"comment/string", guardComment, stringText("x"), false},
		{"string/ident", guardString, "Name", true},
		{"string/quote", guardString, `a"b`, false},
		{"string/percent", guardString, "a%b", false},
		{"string/typed", guardString, goStringLiteralFunc(`a"b`), true},
		{"string/format", guardString, fmtTextFunc("a"), false},
		{"format/ident", guardFormat, "Name", true},
		{"format/percent", guardFormat, "a%b", false},
		{"format/typed", guardFormat, fmtTextFunc("a%b\n"), true},
		{"format/string", guardFormat, goStringLiteralFunc("a"), false},
		{"raw/ident", guardRaw, "omitempty", true},
		{"raw/comma", guardRaw, "a,b", false},
		{"raw/typed", guardRaw, tagText("a b"), true},
		{"code/expr", guardCode, `x.Y("q\n")`, true},
		{"code/number", guardCode, 1.5, true},
		{"code/open-string", guardCode, `"abc`, false},
		{"code/comment", guardCode, "a // b", false},
		{"code/block-comment", guardCode, "a /* b */", false},
		{"code/nul", guardCode, "a\x00", false},
		{"code/bom", guardCode, "a\xef\xbb\xbf", false},
		{"code/typed", guardCode, stringText("x"), false},
	} {
		_, err := c.guard(c.in)
		if (err == nil) != c.ok {
			t.Errorf("%s: guard(%#v) error = %v, want ok=%v", c.name, c.in, err, c.ok)
		}
		if err != nil && !errors.Is(err, errEscape) {
			t.Errorf("%s: refusal %v does not wrap errEscape", c.name, err)
		}
	}
}

func TestFmtTextAndNumLit(t *testing.T) {
	if got := string(fmtTextFunc(`^\d+%$`)); got != `^\\d+%%$` {
		t.Errorf("fmtText = %q", got)
	}
	if got := string(fmtCatFunc(fmtTextFunc("a%"), ": items")); got != "a%%: items" {
		t.Errorf("fmtCat = %q", got)
	}
	for _, ok := range []any{1, int64(-3), 2.5, "not a number"} {
		_, err := numLitFunc(ok)
		if _, isString := ok.(string); isString != (err != nil) {
			t.Errorf("numLit(%#v) error = %v", ok, err)
		}
	}
	if _, err := jsonTagNameFunc("a,b"); err == nil {
		t.Error("jsonTagName accepted a name encoding/json reads as a name and an option")
	}
	if got, err := jsonTagNameFunc("a b"); err != nil || got != "a b" {
		t.Errorf("jsonTagName(\"a b\") = %q, %v", got, err)
	}
}

// TestEmitEscapesTextTheSchemaCannotCarry drives the emitter with IR a schema
// reader could never produce -- invalid UTF-8 survives no JSON decoder -- so
// that the escaping is shown to hold for whatever a library caller hands it.
func TestEmitEscapesTextTheSchemaCannotCarry(t *testing.T) {
	e := mustNew(t)
	build := func(hostile string) *generator.File {
		return &generator.File{
			PackageName: "model",
			TypeDefs: []generator.TypeDef{
				&generator.StructDef{
					Name: "Doc",
					Doc:  generator.Doc{Title: hostile, Description: hostile, Caveats: []string{hostile}},
					Fields: []generator.FieldDef{{
						Name: "Name", JSONName: "name", Type: &generator.PrimitiveType{Name: "string"},
						Doc: generator.Doc{Title: hostile, Description: hostile},
					}},
				},
			},
			UnresolvedRefs: []string{hostile},
		}
	}
	decls := func(text string) int {
		t.Helper()
		out, err := e.Emit(build(text))
		if err != nil {
			t.Fatalf("Emit: %v", err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), "", out, 0)
		if err != nil {
			t.Fatalf("output does not parse: %v\n%s", err, out)
		}
		return len(file.Decls)
	}
	if got, want := decls(strings.Join(commentHostile, " | ")), decls("benign"); got != want {
		t.Fatalf("hostile text changed the declarations: %d, want %d", got, want)
	}
}

func TestEmitRefusesAPackageNameThatIsNotAnIdentifier(t *testing.T) {
	e := mustNew(t)
	for _, name := range []string{"a-b", "x\nimport _ \"net/http/pprof\"", "func", ""} {
		if _, err := e.Emit(&generator.File{PackageName: name}); err == nil {
			t.Errorf("Emit accepted package name %q", name)
		}
	}
	if _, err := e.Emit(&generator.File{PackageName: "p", Imports: []generator.Import{{Path: "fmt", Alias: "a b"}}}); err == nil {
		t.Error("Emit accepted an import alias that is not an identifier")
	}
}
