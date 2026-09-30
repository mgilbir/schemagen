package patterns

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	ecma262 "github.com/mgilbir/goecma262"
	ecmaflags "github.com/mgilbir/goecma262/flags"

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
	"github.com/mgilbir/schemagen/tests/internal/testsupport"
)

// This file holds generated code to one regular expression engine, and to one
// answer for a match that engine could not decide.
//
// JSON Schema patterns are ECMA-262 regular expressions with the "u" flag.
// Generated code used to match them three ways -- through the ECMA-262 engine
// recompiled on every call, through Go's RE2 (a different language) in the
// contains check and in the generator's own decisions, and not at all where
// RE2 could not compile the pattern -- and it read the engine's "no answer"
// as "no match". The tests below do not pick a position and check it: they
// take a set of patterns on which the two engines disagree, put each into
// every position a pattern can occupy, and require the generated verdict to be
// the one a direct ECMA-262 evaluation gives. The same positions are then
// driven with a million-character input that must match and return quickly,
// and with an input the engine cannot decide within its budget, which must
// come back as an error wrapping ecma262.ErrStepLimit -- never as valid, never
// as an ordinary refusal.

// engineCase is one generated package: a schema, the config it is generated
// under, and the documents the program decodes into its root type.
type engineCase struct {
	pkg    string
	schema string
	cfg    generator.Config
	docs   []engineDoc
}

// engineDoc is one document and what its verdict must be.
type engineDoc struct {
	id   string
	json string
	want string // "valid", "invalid" or "undecided"
}

// engineVerdict is what the program reported for one document.
type engineVerdict struct {
	verdict string
	msg     string
	elapsed time.Duration
}

// runEngineCases generates every case into a package of its own in one
// throwaway module, and runs one program that decodes and validates every
// document. The root type of each package is named Root.
//
// A decode refusal counts as "invalid" -- the document was refused, which is
// what an invalid verdict means -- and one wrapping ecma262.ErrStepLimit as
// "undecided", like a Validate error that does.
func runEngineCases(t *testing.T, cases []engineCase) map[string]engineVerdict {
	t.Helper()
	dir := t.TempDir()
	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}
	var imports, body strings.Builder
	for _, c := range cases {
		var s schema.Schema
		if err := json.Unmarshal([]byte(c.schema), &s); err != nil {
			t.Fatalf("%s: schema: %v", c.pkg, err)
		}
		s.Normalize()
		cfg := c.cfg
		cfg.PackageName = c.pkg
		ir, err := generator.New(cfg).Generate(&s, generator.WithRootTypeName("Root"))
		if err != nil {
			t.Fatalf("%s: generate: %v\nschema: %s", c.pkg, err, c.schema)
		}
		src, err := em.Emit(ir)
		if err != nil {
			t.Fatalf("%s: emit: %v", c.pkg, err)
		}
		pkgDir := filepath.Join(dir, c.pkg)
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "types.go"), src, 0o644); err != nil {
			t.Fatal(err)
		}
		helpers, needed, err := em.EmitHelpers(c.pkg, generator.HelpersReferencedBy(string(src)))
		if err != nil {
			t.Fatalf("%s: helpers: %v", c.pkg, err)
		}
		if needed {
			if err := os.WriteFile(filepath.Join(pkgDir, "schemagen_helpers.go"), helpers, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		fmt.Fprintf(&imports, "\t%q\n", "enginetest/"+c.pkg)
		for _, d := range c.docs {
			fmt.Fprintf(&body, "\tcheck(%q, %q, func(in []byte) error {\n\t\tvar v %s.Root\n\t\tif err := json.Unmarshal(in, &v); err != nil {\n\t\t\treturn decodeErr{err}\n\t\t}\n\t\treturn v.Validate()\n\t})\n",
				d.id, d.json, c.pkg)
		}
	}
	mainSrc := `package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	ecma262 "github.com/mgilbir/goecma262"
` + imports.String() + `)

type decodeErr struct{ err error }

func (e decodeErr) Error() string { return e.err.Error() }
func (e decodeErr) Unwrap() error { return e.err }

func check(id, doc string, run func([]byte) error) {
	in := []byte(doc)
	if strings.HasPrefix(doc, "@") {
		b, err := os.ReadFile(doc[1:])
		if err != nil {
			panic(err)
		}
		in = b
	}
	start := time.Now()
	err := run(in)
	elapsed := time.Since(start)
	verdict := "valid"
	switch {
	case err == nil:
	case errors.Is(err, ecma262.ErrStepLimit):
		verdict = "undecided"
	default:
		verdict = "invalid"
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	fmt.Printf("%s\t%s\t%d\t%d\t%q\n", id, verdict, elapsed.Nanoseconds(), len(msg), msg)
}

func main() {
	var _ = json.Unmarshal
` + body.String() + `}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeCogenGoMod(dir); err != nil {
		t.Fatal(err)
	}
	// The module is named after the import path the program uses.
	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	mod = []byte(strings.Replace(string(mod), "module cogen_test", "module enginetest", 1))
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), mod, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	out, err := testgo.Command(ctx, dir, "run", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("building or running the generated program: %v\n%s", err, out)
	}
	got := map[string]engineVerdict{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		f := strings.SplitN(sc.Text(), "\t", 5)
		if len(f) != 5 {
			continue // toolchain chatter
		}
		ns, _ := strconv.ParseInt(f[2], 10, 64)
		msg, err := strconv.Unquote(f[4])
		if err != nil {
			t.Fatalf("unreadable line %q: %v", sc.Text(), err)
		}
		got[f[0]] = engineVerdict{verdict: f[1], msg: msg, elapsed: time.Duration(ns)}
	}
	for _, c := range cases {
		for _, d := range c.docs {
			if _, ok := got[d.id]; !ok {
				t.Fatalf("no verdict for %s; program output:\n%s", d.id, out)
			}
		}
	}
	return got
}

// ecmaMatch is the reference: the pattern evaluated directly by the ECMA-262
// engine, with the flag JSON Schema calls for.
func ecmaMatch(t *testing.T, pattern, s string) bool {
	t.Helper()
	re, err := ecma262.Compile(pattern, ecmaflags.Unicode)
	if err != nil {
		t.Fatalf("reference: %q does not compile: %v", pattern, err)
	}
	m, err := re.MatchStringErr(s)
	if err != nil {
		t.Fatalf("reference: %q on %q: %v", pattern, s, err)
	}
	return m
}

// divergentPatterns are patterns on which ECMA-262 (with the "u" flag) and
// Go's RE2 disagree -- about what a class matches, or about whether the
// pattern is a regular expression at all -- each with inputs that tell the two
// readings apart. TestDivergentPatternsAreJudgedAsECMA262 checks that the set
// really does divide the engines, so it cannot quietly decay into patterns both
// read alike.
var divergentPatterns = []struct {
	name    string
	pattern string
	inputs  []string
}{
	// ECMA-262 \s is WhiteSpace plus LineTerminator: U+FEFF, U+2028, U+00A0
	// among them. RE2's is ASCII.
	{"whitespace", `^\s$`, []string{" ", "\t", "\ufeff", "\u2028", "\u00a0", "\u0085", "a"}},
	// \w and \d stay ASCII in both, but RE2 has no \p class for the
	// non-ASCII digits below, and ECMA-262 has no Unicode \d: both refuse them.
	// The disagreement is in \b, below, and in the classes RE2 cannot parse.
	{"digit", `^\d+$`, []string{"123", "١٢٣", "߀", "12a"}},
	{"word-boundary", `\bé`, []string{"é", "aé", " é"}},
	// [^] is any character in ECMA-262 and a syntax error in RE2.
	{"any-class", `^[^]$`, []string{"a", "\n", "", "ab", "😀"}},
	// Lookbehind and lookahead: not RE2 at all.
	{"lookbehind", `(?<=a)b`, []string{"ab", "cb", "b"}},
	{"negative-lookahead", `^(?!a)`, []string{"a", "b", ""}},
	// Named groups and a backreference to one: RE2 parses (?<x>..) but has no
	// backreferences.
	{"named-backref", `^(?<x>a|b)\k<x>$`, []string{"aa", "bb", "ab"}},
	// A code point escape, and "." over an astral character, which the "u"
	// flag reads as one character.
	{"code-point", `^\u{1F600}$`, []string{"😀", "a"}},
	{"astral-dot", `^.$`, []string{"😀", "a", "\n"}},
	// A Unicode property class.
	{"property-class", `^\p{L}+$`, []string{"héllo", "123", "ſ"}},
}

// patternPosition is one place a pattern can sit, and how a candidate string
// becomes a document there.
type patternPosition struct {
	name string
	// schema is the position's schema with P standing for the quoted pattern.
	schema string
	// doc turns the candidate into the position's value. A position matching
	// keys rather than values returns an object keyed by the candidate.
	doc func(quoted string) string
	// validWhenMatched is the verdict a matching candidate gets; a
	// non-matching one gets the other.
	validWhenMatched bool
}

// rootPositions are generated as the whole schema of a package of their own
// rather than as a property of the shared root object. Nested as a property,
// a string's own if/then, an object's if/then and an object's schema-valued
// unevaluatedProperties are all dropped by the static generator whatever the
// keyword under them says -- a keyword-coverage defect of its own, outside
// what this file tests, which would otherwise hide what it does test.
var rootPositions = map[string]bool{
	"unevaluated-properties-value": true,
	"if-then":                      true,
	"object-if-conditional":        true,
	"object-if-evaluator":          true,
}

var patternPositions = []patternPosition{
	{"property", `{"type":"string","pattern":P}`, func(q string) string { return q }, true},
	{"untyped-property", `{"pattern":P}`, func(q string) string { return q }, true},
	{"ref-alias", `{"$ref":"#/$defs/S"}`, func(q string) string { return q }, true},
	{"items", `{"type":"array","items":{"type":"string","pattern":P}}`, func(q string) string { return "[" + q + "]" }, true},
	{"prefix-items", `{"type":"array","prefixItems":[{"type":"string","pattern":P}]}`, func(q string) string { return "[" + q + "]" }, true},
	{"contains", `{"type":"array","contains":{"type":"string","pattern":P}}`, func(q string) string { return "[" + q + "]" }, true},
	{"unevaluated-items", `{"type":"array","prefixItems":[{"type":"integer"}],"unevaluatedItems":{"type":"string","pattern":P}}`, func(q string) string { return "[0," + q + "]" }, true},
	{"map-value", `{"type":"object","additionalProperties":{"type":"string","pattern":P}}`, func(q string) string { return `{"k":` + q + `}` }, true},
	{"pattern-properties-value", `{"type":"object","patternProperties":{"^k$":{"type":"string","pattern":P}}}`, func(q string) string { return `{"k":` + q + `}` }, true},
	{"pattern-properties-key", `{"type":"object","patternProperties":{P:{"type":"integer"}}}`, func(q string) string { return `{` + q + `:"x"}` }, false},
	{"pattern-properties-key-beside-additional", `{"type":"object","patternProperties":{P:true},"additionalProperties":false}`, func(q string) string { return `{` + q + `:"x"}` }, true},
	{"property-names", `{"type":"object","propertyNames":{"pattern":P}}`, func(q string) string { return `{` + q + `:1}` }, true},
	{"unevaluated-properties-value", `{"type":"object","unevaluatedProperties":{"type":"string","pattern":P}}`, func(q string) string { return `{"k":` + q + `}` }, true},
	{"unevaluated-properties-key", `{"type":"object","patternProperties":{P:true},"unevaluatedProperties":false}`, func(q string) string { return `{` + q + `:"x"}` }, true},
	{"any-of", `{"anyOf":[{"type":"string","pattern":P},{"type":"integer"}]}`, func(q string) string { return q }, true},
	{"one-of", `{"oneOf":[{"type":"string","pattern":P},{"type":"integer"}]}`, func(q string) string { return q }, true},
	{"not", `{"type":"string","not":{"pattern":P}}`, func(q string) string { return q }, false},
	{"if-then", `{"if":{"pattern":P},"then":false}`, func(q string) string { return q }, false},
	{"object-if-conditional", `{"type":"object","properties":{"s":{"type":"string"}},"if":{"properties":{"s":{"pattern":P}}},"then":{"required":["never"]}}`, func(q string) string { return `{"s":` + q + `}` }, false},
	{"object-if-evaluator", `{"type":"object","if":{"properties":{"s":{"pattern":P}},"required":["s"]},"then":{"required":["never"]}}`, func(q string) string { return `{"s":` + q + `}` }, false},
	{"evaluator", `{"type":"array","anyOf":[{"prefixItems":[{"type":"string","pattern":P}]}],"unevaluatedItems":false}`, func(q string) string { return "[" + q + "]" }, true},
	// Positions where a branch or an element is judged by decoding it into a
	// generated type and calling its Validate, and the error is read as a
	// boolean: an undecided match below must come back up as the error it is.
	{"contains-typed", `{"type":"array","contains":{"type":"object","properties":{"s":{"type":"string","pattern":P}},"required":["s"]}}`, func(q string) string { return `[{"s":` + q + `}]` }, true},
	{"one-of-object", `{"oneOf":[{"type":"object","properties":{"s":{"type":"string","pattern":P}},"required":["s"]},{"type":"integer"}]}`, func(q string) string { return `{"s":` + q + `}` }, true},
	{"one-of-objects", `{"oneOf":[{"type":"object","properties":{"s":{"type":"string","pattern":P}},"required":["s"]},{"type":"object","required":["t"]}]}`, func(q string) string { return `{"s":` + q + `}` }, true},
	// A branch whose own decode has to match: the key decides which of its
	// overflow maps a member is filed in.
	{"one-of-key-routing", `{"oneOf":[{"type":"object","patternProperties":{P:{"type":"string"}},"additionalProperties":false},{"type":"object","properties":{"z":{}},"additionalProperties":false}]}`, func(q string) string { return `{` + q + `:"x"}` }, true},
	// The second branch accepts every such document, so the verdict turns on
	// the first alone: read as "no match", an undecided first branch would
	// select the second and make the document valid.
	{"one-of-objects-exclusive", `{"oneOf":[{"type":"object","properties":{"s":{"type":"string","pattern":P}},"required":["s"]},{"type":"object","properties":{"s":{"type":"string","maxLength":100000000}}}]}`, func(q string) string { return `{"s":` + q + `}` }, false},
	{"unevaluated-items-contains-typed", `{"prefixItems":[{"type":"integer"}],"contains":{"type":"object","properties":{"s":{"pattern":P}},"required":["s"]},"unevaluatedItems":false}`, func(q string) string { return `[0,{"s":` + q + `}]` }, true},
	{"unevaluated-items-contains", `{"prefixItems":[{"type":"integer"}],"contains":{"type":"string","pattern":P},"unevaluatedItems":false}`, func(q string) string { return `[0,` + q + `]` }, true},
}

// patternToken finds the P placeholder in a position's schema.
var patternToken = regexp.MustCompile(`([:{\[])P([}:,\]])`)

// positionCases lays every position out for one pattern: the nested ones as
// properties of one root object in package pkg, and each of rootPositions as
// a package of its own. docs returns a position's documents, given how to
// place a value at that position.
func positionCases(pkg, pattern string, cfg generator.Config, docs func(pos patternPosition, place func(value string) string) []engineDoc) []engineCase {
	q := strconv.Quote(pattern)
	// P is a token of its own, a JSON value or key, never a letter of a
	// keyword such as patternProperties.
	sub := func(s string) string {
		return patternToken.ReplaceAllString(s, "${1}"+strings.ReplaceAll(q, "$", "$$")+"${2}")
	}
	nested := engineCase{pkg: pkg, cfg: cfg}
	var props []string
	var own []engineCase
	for i, p := range patternPositions {
		if rootPositions[p.name] {
			own = append(own, engineCase{
				pkg:    fmt.Sprintf("%sr%d", pkg, i),
				schema: sub(p.schema),
				cfg:    cfg,
				docs:   docs(p, func(v string) string { return v }),
			})
			continue
		}
		props = append(props, strconv.Quote(p.name)+":"+sub(p.schema))
		name := p.name
		nested.docs = append(nested.docs, docs(p, func(v string) string { return `{` + strconv.Quote(name) + `:` + v + `}` })...)
	}
	nested.schema = `{"type":"object","$defs":{"S":{"type":"string","pattern":` + q + `}},"properties":{` + strings.Join(props, ",") + `}}`
	return append([]engineCase{nested}, own...)
}

// jsonQuote writes s as a JSON string. strconv.Quote is not one: it escapes
// U+2028 and a lone surrogate the Go way, and \x.. is not JSON.
func jsonQuote(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var validationModes = []generator.ValidationMode{generator.ValidationModeStatic, generator.ValidationModeHybrid, generator.ValidationModeRuntime}

// engineConfig is the default configuration in one validation mode, with
// "format" asserted so that a format beside a pattern is a check to get right
// rather than an annotation.
func engineConfig(mode generator.ValidationMode) generator.Config {
	cfg := generator.DefaultConfig()
	cfg.Validation = mode
	cfg.FormatAssertion = true
	return cfg
}

// TestDivergentPatternsAreJudgedAsECMA262 is the property test: every pattern
// on which RE2 and ECMA-262 disagree, in every position, in every validation
// mode, gives the verdict a direct ECMA-262 evaluation gives.
func TestDivergentPatternsAreJudgedAsECMA262(t *testing.T) {
	// The set has to divide the engines, or it tests nothing RE2 would get
	// wrong: count the (pattern, input) pairs where RE2 refuses the pattern or
	// answers differently.
	divided := 0
	for _, dp := range divergentPatterns {
		re, err := regexp.Compile(dp.pattern)
		for _, in := range dp.inputs {
			if err != nil || re.MatchString(in) != ecmaMatch(t, dp.pattern, in) {
				divided++
			}
		}
	}
	if divided < len(divergentPatterns) {
		t.Fatalf("only %d (pattern, input) pairs tell RE2 and ECMA-262 apart; the set has stopped being divergent", divided)
	}

	var cases []engineCase
	for mi, mode := range validationModes {
		for pi, dp := range divergentPatterns {
			cases = append(cases, positionCases(fmt.Sprintf("m%dp%d", mi, pi), dp.pattern, engineConfig(mode),
				func(pos patternPosition, place func(string) string) []engineDoc {
					var docs []engineDoc
					for ii, in := range dp.inputs {
						verdict := "invalid"
						if ecmaMatch(t, dp.pattern, in) == pos.validWhenMatched {
							verdict = "valid"
						}
						docs = append(docs, engineDoc{
							id:   fmt.Sprintf("%s/%s/%s/%d", mode, dp.name, pos.name, ii),
							json: place(pos.doc(jsonQuote(t, in))),
							want: verdict,
						})
					}
					return docs
				})...)
		}
	}
	got := runEngineCases(t, cases)
	var wrong []string
	for _, c := range cases {
		for _, d := range c.docs {
			if g := got[d.id]; g.verdict != d.want {
				wrong = append(wrong, fmt.Sprintf("%s: got %s, want %s (%s) on %s", d.id, g.verdict, d.want, g.msg, d.json))
			}
		}
	}
	sort.Strings(wrong)
	if len(wrong) > 0 {
		t.Errorf("%d verdicts disagree with ECMA-262:\n%s", len(wrong), strings.Join(wrong, "\n"))
	}
}

// modifierPatterns use ES2025 modifier groups, (?ims-ims:...), which JSON
// Schema's dialect admits as it admits any other ECMA-262 syntax. Each is
// written beside the same pattern with the modifiers taken out, and each input
// carries the answer the specification gives, written out here rather than
// asked of the engine: the modifier must decide at least one input of every
// pattern, or the pattern tests nothing a missing modifier would get wrong.
var modifierPatterns = []struct {
	name, pattern, plain string
	inputs               []string
	match                []bool
}{
	// i scoped to a group: the "c" outside it stays case-sensitive.
	{"ignore-case", `^(?i:ab)c$`, `^(?:ab)c$`,
		[]string{"abc", "ABc", "aBc", "ABC", "xbc"},
		[]bool{true, true, true, false, false}},
	// i switched back off inside a group that switched it on.
	{"ignore-case-off", `^(?i:a(?-i:b))$`, `^(?:a(?:b))$`,
		[]string{"Ab", "ab", "AB", "aB"},
		[]bool{true, true, false, false}},
	// s: "." takes a line terminator.
	{"dot-all", `^(?s:.)$`, `^(?:.)$`,
		[]string{"\n", "\u2028", "a", "ab"},
		[]bool{true, true, true, false}},
	// m: "$" inside the group matches before a line terminator; "^" outside it
	// still means the start of the input.
	{"multiline", `^(?m:a$)`, `^(?:a$)`,
		[]string{"a\nb", "a", "ab", "b\na"},
		[]bool{true, true, false, false}},
	// Two flags in one group.
	{"ignore-case-dot-all", `^(?is:a.)$`, `^(?:a.)$`,
		[]string{"A\n", "a\n", "Ab", "A"},
		[]bool{true, true, true, false}},
	// Under i and u, \w takes the characters whose simple case folding is a
	// word character: U+017F LATIN SMALL LETTER LONG S and U+212A KELVIN SIGN.
	{"ignore-case-word", `^(?i:\w)$`, `^(?:\w)$`,
		[]string{"\u017f", "\u212a", "a", "é", "-"},
		[]bool{true, true, true, false, false}},
	// A class is matched by case folding both sides: KELVIN SIGN folds to k.
	{"ignore-case-class", `^(?i:[a-z]+)$`, `^(?:[a-z]+)$`,
		[]string{"HeLLo", "hello", "\u212a", "héllo"},
		[]bool{true, true, true, false}},
	// A property class too: "a" folds to the same thing as "A", which is Lu.
	{"ignore-case-property", `^(?i:\p{Lu})$`, `^(?:\p{Lu})$`,
		[]string{"a", "A", "1"},
		[]bool{true, true, false}},
	// A backreference inside the group compares under the group's flags.
	{"ignore-case-backreference", `^(a)(?i:\1)$`, `^(a)(?:\1)$`,
		[]string{"aA", "aa", "ab"},
		[]bool{true, true, false}},
}

// TestModifierPatternsAreJudgedAsECMA262 puts every modifier pattern in every
// position a pattern occupies, in every validation mode, and requires the
// verdict the specification gives. The engine schemagen used to pin had no
// modifier groups and such a schema was refused; this is what holds the
// generated code to their meaning now that it is accepted.
func TestModifierPatternsAreJudgedAsECMA262(t *testing.T) {
	for _, mp := range modifierPatterns {
		if len(mp.inputs) != len(mp.match) {
			t.Fatalf("%s: %d inputs, %d answers", mp.name, len(mp.inputs), len(mp.match))
		}
		decided := false
		for i, in := range mp.inputs {
			if got := ecmaMatch(t, mp.pattern, in); got != mp.match[i] {
				t.Fatalf("reference: %q on %q = %v; the specification says %v", mp.pattern, in, got, mp.match[i])
			}
			if ecmaMatch(t, mp.plain, in) != mp.match[i] {
				decided = true
			}
		}
		if !decided {
			t.Fatalf("%s: %q answers every input as %q does; the modifier decides nothing", mp.name, mp.pattern, mp.plain)
		}
	}

	var cases []engineCase
	for mi, mode := range validationModes {
		for pi, mp := range modifierPatterns {
			cases = append(cases, positionCases(fmt.Sprintf("m%dmod%d", mi, pi), mp.pattern, engineConfig(mode),
				func(pos patternPosition, place func(string) string) []engineDoc {
					var docs []engineDoc
					for ii, in := range mp.inputs {
						verdict := "invalid"
						if mp.match[ii] == pos.validWhenMatched {
							verdict = "valid"
						}
						docs = append(docs, engineDoc{
							id:   fmt.Sprintf("%s/%s/%s/%d", mode, mp.name, pos.name, ii),
							json: place(pos.doc(jsonQuote(t, in))),
							want: verdict,
						})
					}
					return docs
				})...)
		}
	}
	got := runEngineCases(t, cases)
	var wrong []string
	for _, c := range cases {
		for _, d := range c.docs {
			if g := got[d.id]; g.verdict != d.want {
				wrong = append(wrong, fmt.Sprintf("%s: got %s, want %s (%s) on %s", d.id, g.verdict, d.want, g.msg, d.json))
			}
		}
	}
	sort.Strings(wrong)
	if len(wrong) > 0 {
		t.Errorf("%d verdicts disagree with ECMA-262:\n%s", len(wrong), strings.Join(wrong, "\n"))
	}
}

// engineStressDoc writes a stress input to a file, which the program reads in
// place of an inline document: a million characters do not belong in its
// source.
func engineStressDoc(t *testing.T, dir, name, doc string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return "@" + path
}

// TestPatternStepLimitIsAnErrorInEveryPosition drives every position with two
// inputs. A 1,000,000-character string that ^[a-z]+$ matches must be accepted,
// and quickly: the engine's budget grows with the input, and the pattern is
// linear. And a string on which ^(.*?,){30}P backtracks past any budget must
// come back as an error wrapping ecma262.ErrStepLimit -- the document is not
// known to be valid, and not known to be invalid -- whose message names the
// pattern and stays short, however long the value.
func TestPatternStepLimitIsAnErrorInEveryPosition(t *testing.T) {
	const linear = `^[a-z]+$`
	const catastrophic = `^(.*?,){30}P`
	long := strings.Repeat("a", 1_000_000)
	hard := strings.Repeat("a,", 400)
	// The reference agrees on what these inputs are.
	if !ecmaMatch(t, linear, long) {
		t.Fatal("reference: the long input does not match")
	}
	if re := ecma262.MustCompile(catastrophic, ecmaflags.Unicode); true {
		if _, err := re.MatchStringErr(hard); !errors.Is(err, ecma262.ErrStepLimit) {
			t.Fatalf("reference: the hard input did not exhaust the budget (err %v); pick a harder one", err)
		}
	}
	dir := t.TempDir()
	var cases []engineCase
	for mi, mode := range validationModes {
		for _, spec := range []struct {
			pattern, input, want, tag string
		}{
			{linear, long, "", "long"},
			// The same length with one character the pattern refuses: every
			// position that reports a mismatch writes the value into its
			// message, and each must cut it short.
			{linear, long + "1", "", "longbad"},
			{catastrophic, hard, "undecided", "hard"},
		} {
			cases = append(cases, positionCases(fmt.Sprintf("m%d%s", mi, spec.tag), spec.pattern, engineConfig(mode),
				func(pos patternPosition, place func(string) string) []engineDoc {
					want := spec.want
					switch spec.tag {
					case "long":
						want = "invalid"
						if pos.validWhenMatched {
							want = "valid"
						}
					case "longbad":
						want = "valid"
						if pos.validWhenMatched {
							want = "invalid"
						}
					}
					id := fmt.Sprintf("%s/%s/%s", mode, spec.tag, pos.name)
					doc := engineStressDoc(t, dir, strings.ReplaceAll(id, "/", "_")+".json", place(pos.doc(jsonQuote(t, spec.input))))
					return []engineDoc{{id: id, json: doc, want: want}}
				})...)
		}
	}
	got := runEngineCases(t, cases)
	for _, c := range cases {
		for _, d := range c.docs {
			g := got[d.id]
			if g.verdict != d.want {
				t.Errorf("%s: got %s, want %s: %.300s", d.id, g.verdict, d.want, g.msg)
				continue
			}
			if len(g.msg) > 1024 {
				t.Errorf("%s: the message is %d bytes; a value in it is not cut short: %.300s", d.id, len(g.msg), g.msg)
			}
			if strings.Contains(d.id, "/hard/") && !strings.Contains(g.msg, "could not be evaluated within the regular expression engine's step budget") {
				t.Errorf("%s: an undecided match must say so: %s", d.id, g.msg)
			}
			if strings.Contains(d.id, "/long/") && g.elapsed > 2*time.Second {
				t.Errorf("%s: a linear pattern over 1,000,000 characters took %v", d.id, g.elapsed)
			}
		}
	}
}

// declaredPatternFeatures are patternProperties keys that need ECMA-262 --
// Go's RE2 refuses the lookahead and the backreference, which is how a rule on
// a declared property used to be dropped -- each with a declared property name
// it matches and one it does not.
var declaredPatternFeatures = []struct {
	name, pattern, match, miss string
}{
	{"lookahead", `^b(?=a)`, "bar", "bor"},
	{"property-class", `^\p{L}+$`, "bär", "b4r"},
	{"backreference", `^(b)a\1$`, "bab", "bac"},
}

// declaredPatternCells are the declared property's schema crossed with what the
// matching pattern's sub-schema says, with a value the sub-schema refuses and,
// where one exists, a value it accepts -- both admitted by the declared schema,
// so the pattern is the only thing deciding.
var declaredPatternCells = []struct {
	name, declared, sub, bad, good string
}{
	{"string-type", `{"type":"string"}`, `{"type":"integer"}`, `"abc"`, ``},
	{"string-enum", `{"type":"string"}`, `{"enum":["x"]}`, `"abc"`, `"x"`},
	{"string-maxLength", `{"type":"string"}`, `{"maxLength":2}`, `"abc"`, `"ab"`},
	{"untyped-type", `{}`, `{"type":"integer"}`, `"abc"`, `5`},
	{"untyped-enum", `{}`, `{"enum":["x"]}`, `"abc"`, `"x"`},
	{"untyped-const", `{}`, `{"const":1}`, `2`, `1`},
	{"untyped-maxLength", `{}`, `{"maxLength":2}`, `"abc"`, `"ab"`},
	{"untyped-minimum", `{}`, `{"minimum":5}`, `3`, `7`},
	{"untyped-required", `{}`, `{"required":["x"]}`, `{}`, `{"x":1}`},
	{"untyped-format", `{}`, `{"format":"date"}`, `"2024-13-99"`, `"2024-01-02"`},
	{"ref-type", `{"$ref":"#/$defs/Str"}`, `{"type":"integer"}`, `"abc"`, ``},
	{"ref-enum", `{"$ref":"#/$defs/Str"}`, `{"enum":["x"]}`, `"abc"`, `"x"`},
	{"ref-maxLength", `{"$ref":"#/$defs/Str"}`, `{"maxLength":2}`, `"abc"`, `"ab"`},
	{"number-minimum", `{"type":"number"}`, `{"minimum":5}`, `3`, `7`},
	{"number-type", `{"type":"number"}`, `{"type":"integer"}`, `1.5`, `2`},
	{"object-required", `{"type":"object"}`, `{"required":["x"]}`, `{}`, `{"x":1}`},
	{"object-properties", `{"type":"object"}`, `{"properties":{"x":{"type":"string"}}}`, `{"x":1}`, `{"x":"a"}`},
}

// TestPatternPropertiesApplyToDeclaredProperties is the matrix for a declared
// property whose name a patternProperties key matches. JSON Schema applies both
// keywords to it: its own schema from "properties", and the whole of every
// matching pattern's schema. Each cell declares two properties with the same
// schema, one whose name the pattern matches and one whose name it does not,
// so a value the pattern's schema refuses is invalid under the first name and
// valid under the second -- in every validation mode.
func TestPatternPropertiesApplyToDeclaredProperties(t *testing.T) {
	var cases []engineCase
	for mi, mode := range validationModes {
		for fi, f := range declaredPatternFeatures {
			var props []string
			var docs []engineDoc
			for _, cell := range declaredPatternCells {
				props = append(props, fmt.Sprintf(`%q:{"type":"object","properties":{%q:%s,%q:%s},"patternProperties":{%q:%s}}`,
					cell.name, f.match, cell.declared, f.miss, cell.declared, f.pattern, cell.sub))
				at := func(key, value string) string {
					return fmt.Sprintf(`{%q:{%q:%s}}`, cell.name, key, value)
				}
				id := fmt.Sprintf("%s/%s/%s", mode, f.name, cell.name)
				docs = append(docs,
					engineDoc{id: id + "/matched-bad", json: at(f.match, cell.bad), want: "invalid"},
					engineDoc{id: id + "/unmatched-bad", json: at(f.miss, cell.bad), want: "valid"},
				)
				if cell.good != "" {
					docs = append(docs, engineDoc{id: id + "/matched-good", json: at(f.match, cell.good), want: "valid"})
				}
			}
			cases = append(cases, engineCase{
				pkg:    fmt.Sprintf("m%df%d", mi, fi),
				schema: `{"type":"object","$defs":{"Str":{"type":"string"}},"properties":{` + strings.Join(props, ",") + `}}`,
				cfg:    engineConfig(mode),
				docs:   docs,
			})
		}
	}
	got := runEngineCases(t, cases)
	var wrong []string
	for _, c := range cases {
		for _, d := range c.docs {
			if g := got[d.id]; g.verdict != d.want {
				wrong = append(wrong, fmt.Sprintf("%s: got %s, want %s (%s) on %s", d.id, g.verdict, d.want, g.msg, d.json))
			}
		}
	}
	sort.Strings(wrong)
	if len(wrong) > 0 {
		t.Errorf("%d verdicts wrong:\n%s", len(wrong), strings.Join(wrong, "\n"))
	}
}

// TestValidateCompilesNoPattern measures what Validate allocates against what
// one pattern compile allocates. Every pattern is compiled once, when the
// package is initialised, so a Validate whose only work is matching allocates
// what the matches allocate and nothing like a compile; when each call
// compiled its pattern -- which the checks used to do, through
// ecma262.MatchString -- it allocated a compile per check.
func TestValidateCompilesNoPattern(t *testing.T) {
	const pattern = `^(?:[a-z]+-)*[a-z]+$`
	schemaJSON := `{"type":"object","properties":{` +
		`"s":{"type":"string","pattern":` + strconv.Quote(pattern) + `},` +
		`"xs":{"type":"array","items":{"type":"string","pattern":` + strconv.Quote(pattern) + `}},` +
		`"m":{"type":"object","additionalProperties":{"type":"string","pattern":` + strconv.Quote(pattern) + `}}}}`
	out := runEngineProgram(t, schemaJSON, generator.DefaultConfig(), `package main

import (
	"encoding/json"
	"fmt"
	"testing"

	ecma262 "github.com/mgilbir/goecma262"
	ecmaflags "github.com/mgilbir/goecma262/flags"

	"enginetest/gen"
)

func main() {
	const pattern = `+strconv.Quote(pattern)+`
	var v gen.Root
	doc := `+"`"+`{"s":"one-two-three","xs":["a-b","c-d","e-f"],"m":{"k":"x-y-z"}}`+"`"+`
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		panic(err)
	}
	if err := v.Validate(); err != nil {
		panic(err)
	}
	validate := testing.AllocsPerRun(200, func() { _ = v.Validate() })
	re := ecma262.MustCompile(pattern, ecmaflags.Unicode)
	match := testing.AllocsPerRun(200, func() { _, _ = re.MatchStringErr("one-two-three") })
	compile := testing.AllocsPerRun(200, func() { _, _ = ecma262.Compile(pattern, ecmaflags.Unicode) })
	fmt.Printf("%v %v %v\n", validate, match, compile)
}
`)
	var validate, match, compile float64
	if _, err := fmt.Sscan(out, &validate, &match, &compile); err != nil {
		t.Fatalf("unreadable output %q: %v", out, err)
	}
	// Five strings are matched per Validate. Half a compile's worth above
	// that is already more than the rest of Validate allocates, and a single
	// compile per call crosses it.
	const matched = 5
	t.Logf("allocations: Validate %.0f, one match %.0f, one compile %.0f", validate, match, compile)
	if compile <= match {
		t.Fatalf("a compile allocates %.0f and a match %.0f; the measurement cannot tell them apart", compile, match)
	}
	if validate >= matched*match+compile/2 {
		t.Errorf("Validate allocates %.0f, more than the %d matches it makes (%.0f each) and half a compile (%.0f): a pattern is being compiled per call",
			validate, matched, match, compile)
	}
}

// TestPatternRegressions pins the smaller defects the one-engine rework
// turned up, each against the generated code of a schema written for it.
func TestPatternRegressions(t *testing.T) {
	strict := engineConfig(generator.ValidationModeStatic)
	strict.StrictReadWrite = true
	cases := []engineCase{
		{
			// A pattern constrains strings only: a contains element that is a
			// number or a null satisfies it. It used to be counted as not
			// matching -- a null decoded into a Go string as "" and was
			// matched against the pattern.
			pkg:    "containsvacuous",
			schema: `{"type":"array","contains":{"pattern":"^a"}}`,
			cfg:    engineConfig(generator.ValidationModeStatic),
			docs: []engineDoc{
				{id: "contains/number", json: `[5]`, want: "valid"},
				{id: "contains/null", json: `[null]`, want: "valid"},
				{id: "contains/mismatch", json: `["b"]`, want: "invalid"},
			},
		},
		{
			// The same holds for every keyword a contains check reads: each
			// judges its own JSON type. {"contains":{"minLength":3}} also used
			// to fail generation outright (a count written through numLit).
			pkg:    "containsvacuousall",
			schema: `{"type":"object","properties":{"min":{"type":"array","contains":{"minimum":3}},"xmin":{"type":"array","contains":{"exclusiveMinimum":3}},"max":{"type":"array","contains":{"maximum":-1}},"xmax":{"type":"array","contains":{"exclusiveMaximum":-1}},"mult":{"type":"array","contains":{"multipleOf":7}},"minlen":{"type":"array","contains":{"minLength":3}},"maxlen":{"type":"array","contains":{"maxLength":0}}}}`,
			cfg:    engineConfig(generator.ValidationModeStatic),
			docs: []engineDoc{
				{id: "contains-all/minimum-string", json: `{"min":["a"]}`, want: "valid"},
				{id: "contains-all/minimum-small", json: `{"min":[1]}`, want: "invalid"},
				{id: "contains-all/exclusiveMinimum-null", json: `{"xmin":[null]}`, want: "valid"},
				{id: "contains-all/maximum-null", json: `{"max":[null]}`, want: "valid"},
				{id: "contains-all/maximum-big", json: `{"max":[0]}`, want: "invalid"},
				{id: "contains-all/exclusiveMaximum-string", json: `{"xmax":["a"]}`, want: "valid"},
				{id: "contains-all/multipleOf-bool", json: `{"mult":[true]}`, want: "valid"},
				{id: "contains-all/multipleOf-miss", json: `{"mult":[8]}`, want: "invalid"},
				{id: "contains-all/minLength-number", json: `{"minlen":[5]}`, want: "valid"},
				{id: "contains-all/minLength-null", json: `{"minlen":[null]}`, want: "valid"},
				{id: "contains-all/minLength-short", json: `{"minlen":["ab"]}`, want: "invalid"},
				{id: "contains-all/maxLength-number", json: `{"maxlen":[5]}`, want: "valid"},
				{id: "contains-all/maxLength-long", json: `{"maxlen":["a"]}`, want: "invalid"},
			},
		},
		{
			// The same, where contains decides which items unevaluatedItems
			// may still refuse: an item a numeric contains cannot speak about
			// is one it evaluates.
			pkg:    "unevalcontainsnumeric",
			schema: `{"prefixItems":[{"type":"integer"}],"contains":{"minimum":3},"unevaluatedItems":false}`,
			cfg:    engineConfig(generator.ValidationModeStatic),
			docs: []engineDoc{
				{id: "uneval-contains-numeric/string", json: `[0,"a",5]`, want: "valid"},
				{id: "uneval-contains-numeric/small", json: `[0,1,5]`, want: "invalid"},
			},
		},
		{
			// unevaluatedItems beside a contains whose sub-schema is a pattern:
			// an item the pattern refuses is not evaluated by contains, so
			// unevaluatedItems: false refuses it. The pattern arm of this check
			// was missing, and every item contains looked at counted as
			// evaluated.
			pkg:    "unevalcontains",
			schema: `{"prefixItems":[{"type":"integer"}],"contains":{"type":"string","pattern":"^a"},"unevaluatedItems":false}`,
			cfg:    engineConfig(generator.ValidationModeStatic),
			docs: []engineDoc{
				{id: "uneval-contains/all-matched", json: `[0,"a","ab"]`, want: "valid"},
				{id: "uneval-contains/one-unmatched", json: `[0,"a","b"]`, want: "invalid"},
			},
		},
		{
			// --strict-read-write: an additionalProperties rule steps past the
			// members patternProperties claims. That list was only read where
			// the package also had a rule naming members by pattern, so here a
			// member the pattern claims was taken for an additional one and
			// refused for a readOnly property it is not governed by.
			pkg:    "accessexcept",
			schema: `{"type":"object","properties":{"o":{"type":"object","patternProperties":{"^p":{"type":"object"}},"additionalProperties":{"type":"object","properties":{"ro":{"type":"string","readOnly":true}}}}}}`,
			cfg:    strict,
			docs: []engineDoc{
				{id: "access/pattern-claimed", json: `{"o":{"px":{"ro":"x"}}}`, want: "valid"},
				{id: "access/additional", json: `{"o":{"zx":{"ro":"x"}}}`, want: "invalid"},
			},
		},
	}
	got := runEngineCases(t, cases)
	for _, c := range cases {
		for _, d := range c.docs {
			if g := got[d.id]; g.verdict != d.want {
				t.Errorf("%s: got %s, want %s (%s) on %s", d.id, g.verdict, d.want, g.msg, d.json)
			}
		}
	}
}

// TestHandBuiltDeclaredMemberIsHeldToThePattern covers a value built in Go
// rather than decoded: there is no record of the document's bytes, so a
// declared member a pattern matches is checked from its field, marshalled,
// wherever the member counts as present -- a required property always, an
// optional one when its field is non-nil.
func TestHandBuiltDeclaredMemberIsHeldToThePattern(t *testing.T) {
	out := runEngineProgram(t,
		`{"type":"object","properties":{"bar":{"type":"string"},"baz":{"type":"string"}},"required":["baz"],"patternProperties":{"^ba(?=[rz])":{"maxLength":2}}}`,
		generator.DefaultConfig(), `package main

import (
	"encoding/json"
	"fmt"

	"enginetest/gen"
)

// handBuilt decodes doc and copies its exported fields into a zero value, so
// the result carries what the fields say and nothing the decoder recorded.
func handBuilt(doc string) gen.Root {
	var v gen.Root
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		panic(err)
	}
	var h gen.Root
	h.Bar, h.Baz = v.Bar, v.Baz
	return h
}

func main() {
	for _, doc := range []string{
		`+"`"+`{"baz":"abc"}`+"`"+`,
		`+"`"+`{"baz":"ab"}`+"`"+`,
		`+"`"+`{"baz":"ab","bar":"abc"}`+"`"+`,
		`+"`"+`{"baz":"ab","bar":"ab"}`+"`"+`,
	} {
		fmt.Println(handBuilt(doc).Validate() != nil)
	}
}
`)
	if want := "true\nfalse\ntrue\nfalse"; out != want {
		t.Fatalf("hand-built verdicts (refused?) = %q, want %q", out, want)
	}
}

// TestThrowawayModulesPinTheEngineThisModuleRequires holds the version the test
// harnesses write into their throwaway modules to the one this repository's
// go.mod requires, and its checksums to go.sum. Generated code calls the
// engine's error-returning match API, so a harness left on an older engine
// would not compile, and one left on a different engine would test code
// against an engine generation never compiled a pattern with.
//
// It holds the runtime module to the same pins. Generated code runs the engine
// through the runtime, so that module's go.mod is what a user of a generated
// package builds against; the throwaway modules replace the runtime onto this
// checkout and need the same versions and checksums it names, or they would
// not build offline -- and the pins that reach a user would be ones no test
// ever compiled with.
func TestThrowawayModulesPinTheEngineThisModuleRequires(t *testing.T) {
	requirements := func(gomod string) map[string]string {
		mod, err := os.ReadFile(testsupport.RepoPath(gomod))
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, line := range strings.Split(string(mod), "\n") {
			f := strings.Fields(line)
			// A single-line requirement spells "require path version".
			if len(f) > 0 && f[0] == "require" {
				f = f[1:]
			}
			if len(f) >= 2 {
				out[f[0]] = f[1]
			}
		}
		return out
	}
	for _, gomod := range []string{"go.mod", "runtime/go.mod"} {
		req := requirements(gomod)
		if req["github.com/mgilbir/goecma262"] != goecma262Version {
			t.Errorf("%s requires goecma262 %s; the harnesses pin %s", gomod, req["github.com/mgilbir/goecma262"], goecma262Version)
		}
	}
	// The other two modules the runtime imports, at the versions the harnesses
	// write into their go.mod.
	rt := requirements("runtime/go.mod")
	if rt["golang.org/x/net"] != testsupport.XnetVersion || rt["golang.org/x/text"] != testsupport.XtextVersion {
		t.Errorf("runtime/go.mod requires x/net %s and x/text %s; the harnesses pin %s and %s",
			rt["golang.org/x/net"], rt["golang.org/x/text"], testsupport.XnetVersion, testsupport.XtextVersion)
	}
	for _, gosum := range []string{"go.sum", "runtime/go.sum"} {
		sum, err := os.ReadFile(testsupport.RepoPath(gosum))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"github.com/mgilbir/goecma262 " + goecma262Version + " " + goecma262H1,
			"github.com/mgilbir/goecma262 " + goecma262Version + "/go.mod " + goecma262GoMod,
		} {
			if !strings.Contains(string(sum), want+"\n") {
				t.Errorf("%s has no line %q", gosum, want)
			}
		}
	}
}
