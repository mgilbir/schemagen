package generator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/mgilbir/schemagen/internal/tagoracle"
	"github.com/mgilbir/schemagen/internal/testgo"
)

// emittedTagSpellings are every `json:"..."` tag body the struct template can
// write around a property name: bare, with ",omitempty", and with ",omitzero".
// The distinction matters -- a property named "-" is carried by two of them and
// erased by the third -- so the oracle below requires a name to survive all
// three before calling it representable.
//
// TestEmittedTagSpellingsMatchTheStructTemplate holds this list against the
// template, so a fourth option added there is a test failure here rather than a
// silent hole in what this file measures.
var emittedTagSpellings = []string{"", ",omitempty", ",omitzero"}

// jsonImpl names one of the two encoding/json implementations Go ships.
type jsonImpl bool

const (
	jsonV1 jsonImpl = false // the original: the default through Go 1.26, GOEXPERIMENT=nojsonv2
	jsonV2 jsonImpl = true  // backed by encoding/json/v2: the default from Go 1.27, GOEXPERIMENT=jsonv2
)

func (i jsonImpl) String() string {
	if i == jsonV2 {
		return "the encoding/json/v2-backed encoding/json (GOEXPERIMENT=jsonv2, the Go 1.27 default)"
	}
	return "the original encoding/json (GOEXPERIMENT=nojsonv2, the default through Go 1.26)"
}

// tagVerdicts is what each encoding/json implementation says about one name.
type tagVerdicts map[jsonImpl]bool

// carriedByAll is the only answer the generator may act on. Generated code is
// compiled by its caller, with the caller's Go and the caller's GOEXPERIMENT,
// and neither is the generator's to choose: a name the predicate calls
// representable has to survive whichever encoding/json that turns out to be.
func (v tagVerdicts) carriedByAll() bool { return v[jsonV1] && v[jsonV2] }

// rejectedBy names the implementations that do not carry the name.
func (v tagVerdicts) rejectedBy() string {
	var out []string
	for _, impl := range []jsonImpl{jsonV1, jsonV2} {
		if !v[impl] {
			out = append(out, impl.String())
		}
	}
	return strings.Join(out, " and ")
}

// encodingJSONVerdicts asks both encoding/json implementations, by experiment
// (see tagoracle.Carries), whether the tag the emitter writes for each name
// carries that name.
//
// A test binary is linked against one of them, so the other is asked through
// internal/tagoracle/probe, built with the opposite GOEXPERIMENT setting on the
// same toolchain. Both are asked that way, so the two answers come from the
// same code built two ways; the in-process answer for this binary's own
// implementation is then required to match the probe's, which is what makes
// the probe evidence rather than a second opinion that could be wrong on its
// own.
//
// Before this, the tests below asked only the implementation the test binary
// happened to link, which made them a statement about the toolchain running the
// tests rather than about the code being generated: on Go 1.27, whose default
// is the v2-backed implementation, the punctuation check failed 138 times on a
// clean checkout -- every character v2 accepts and the original does not,
// reported as missing from the constant -- although the constant was right.
// Adding those characters would have been the wrong fix: the generated code
// would then silently rename those properties for every caller on the original
// implementation, which is every caller on Go 1.25 or 1.26 by default.
func encodingJSONVerdicts(t *testing.T, names []string) []tagVerdicts {
	t.Helper()
	out := make([]tagVerdicts, len(names))
	for i := range out {
		out[i] = tagVerdicts{}
	}
	for _, impl := range []jsonImpl{jsonV1, jsonV2} {
		carried := probeEncodingJSON(t, impl, names)
		for i := range names {
			out[i][impl] = carried[i]
		}
	}
	// The probe built as this binary is built has to agree with this binary.
	self := jsonImpl(tagoracle.JSONv2)
	for i, name := range names {
		if got := tagoracle.Carries(name, emittedTagSpellings); got != out[i][self] {
			t.Fatalf("in process, %s says %s is carried=%v; the probe built with the same GOEXPERIMENT says %v. "+
				"The probe is not measuring what this binary measures, so nothing it says about the other "+
				"implementation can be trusted", self, strconv.Quote(name), got, out[i][self])
		}
	}
	return out
}

// probeEncodingJSON runs internal/tagoracle/probe built with the GOEXPERIMENT
// setting that selects impl, and returns its answer for every name.
func probeEncodingJSON(t *testing.T, impl jsonImpl, names []string) []bool {
	t.Helper()
	req, err := json.Marshal(tagoracle.Request{Names: names, Spellings: emittedTagSpellings})
	if err != nil {
		t.Fatal(err)
	}
	setting := "nojsonv2"
	if impl == jsonV2 {
		setting = "jsonv2"
	}
	// Appended to whatever the caller already asked for, so the rest of their
	// experiment set is kept and this one setting wins: the go command reads
	// the list left to right.
	experiment := setting
	if base := os.Getenv("GOEXPERIMENT"); base != "" {
		experiment = base + "," + setting
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := testgo.Command(ctx, filepath.Join("..", ".."), "run", "./internal/tagoracle/probe")
	cmd.Env = append(cmd.Env, "GOEXPERIMENT="+experiment)
	cmd.Stdin = bytes.NewReader(req)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("running the tag probe under GOEXPERIMENT=%s: %v\n%s\n"+
			"Both encoding/json implementations have to be reachable on this toolchain for the tag grammar to be "+
			"checked against what callers can compile with; GOEXPERIMENT=jsonv2 exists from Go 1.25, the go.mod minimum",
			experiment, err, stderr.String())
	}
	var resp tagoracle.Response
	if err := json.Unmarshal(stdout, &resp); err != nil {
		t.Fatalf("reading the tag probe's answer under GOEXPERIMENT=%s: %v\n%s", experiment, err, stdout)
	}
	if jsonImpl(resp.JSONv2) != impl {
		t.Fatalf("the probe built under GOEXPERIMENT=%s reports that it linked %s; the setting did not take, "+
			"so the answer is about the wrong implementation", experiment, jsonImpl(resp.JSONv2))
	}
	if len(resp.Carried) != len(names) || len(resp.Names) != len(names) {
		t.Fatalf("the probe answered %d of %d names", len(resp.Carried), len(names))
	}
	for i := range names {
		if resp.Names[i] != names[i] {
			t.Fatalf("the probe was asked about %s and received %s; the transport altered the name",
				strconv.Quote(names[i]), strconv.Quote(resp.Names[i]))
		}
	}
	return resp.Carried
}

// tagNameCorpus is the population both directions of the agreement check run
// over: every ASCII code point alone and embedded between two letters, a spread
// of Unicode categories chosen so that "is it a letter or a digit" is actually
// asked, and the specific strings the four defects were reported as.
func tagNameCorpus() []string {
	names := []string{
		"",            // issue #246
		"a,b",         // issue #247
		"x,omitempty", // issue #247
		"-",           // a bare `json:"-"` is "skip this field"
		"--", "-a", "a-", "a-b", "-,", "-,omitempty",
		"omitempty", "omitzero", ",omitempty", "string",
		"ok", "A", "z0", "1a", "_x", "a b", " ", "  ",
		"a\"b", "a\\b", "a`b", "a'b",
		"café", "日本語", "Ωμέγα", "ǅ", "ᴀ", "〇",
		"🎉", "a🎉b", "©", "±", "€", "→", "—", "½", "Ⅳ", "٣", "൧",
		"e\u0301",      // a letter followed by a combining acute (Mn)
		"\u0301",       // that combining mark on its own
		"a\u00a0b",     // no-break space, which is not the ASCII one (Zs)
		"a\u200db",     // zero-width joiner (Cf)
		"a\ufffdb",     // the rune invalid UTF-8 decodes to
		"a\U0001D7D9b", // MATHEMATICAL DOUBLE-STRUCK DIGIT ONE (Nd)
		"a\x00b",       // NUL inside a name
		"$id", "$ref", "#x", "@type", "a.b", "a/b", "a:b", "a;b", "a[0]", "{x}",
	}
	for c := 0; c < 128; c++ {
		names = append(names, string(rune(c)), "a"+string(rune(c))+"b")
	}
	return names
}

// TestTagRepresentabilityMatchesEncodingJSON is the guard that makes
// needsManualJSON follow encoding/json's tag grammar rather than a list of
// characters somebody has been bitten by -- the grammar of both
// implementations, because a caller may compile the generated code with either.
//
// The predicate has to be exactly the conjunction of their verdicts, in both
// directions.
//
// A name the predicate calls representable but some implementation does not
// carry is a key silently renamed or dropped on every document, for every
// caller building with that implementation -- that is exactly what issues #246
// and #247 were, and what the "-" and 🎉 cases were before anyone filed them.
// 🎉 is carried by the v2-backed implementation and discarded by the original,
// so a predicate that followed whichever Go the generator was built with would
// reintroduce the bug for every caller on the other one.
//
// A name the predicate refuses although both implementations carry it is
// pushed onto the hand-written marshal path for no reason. That direction used
// to be asserted only when the test binary happened to be linked against the
// original implementation, which on Go 1.27 meant never; it is asserted on
// every toolchain now, because both implementations are asked on every
// toolchain.
func TestTagRepresentabilityMatchesEncodingJSON(t *testing.T) {
	names := tagNameCorpus()
	verdicts := encodingJSONVerdicts(t, names)

	for i, name := range names {
		representable := tagNameIsRepresentable(name)
		carried := verdicts[i].carriedByAll()

		if representable && !carried {
			t.Errorf("tagNameIsRepresentable(%s) = true, but %s does not carry that name "+
				"through the tag the emitter writes -- the key would be silently renamed or dropped on "+
				"every document a caller building with it reads or writes. The predicate has to follow the "+
				"intersection of both implementations' grammars (the original's parseTag and isValidTag, and "+
				"the `json:\"-\"` special case); see needsManualJSON",
				strconv.Quote(name), verdicts[i].rejectedBy())
		}
		if !representable && carried {
			t.Errorf("tagNameIsRepresentable(%s) = false, but every encoding/json implementation carries "+
				"that name fine -- the property is being pushed onto the hand-written marshal path for no reason",
				strconv.Quote(name))
		}
	}

	// needsManualJSON is the complement every caller asks for, and it is the one
	// that would be edited by somebody who never reads the predicate.
	for _, name := range names {
		if needsManualJSON(name) == tagNameIsRepresentable(name) {
			t.Fatalf("needsManualJSON(%s) is not the complement of tagNameIsRepresentable", strconv.Quote(name))
		}
	}
}

// TestTagRepresentabilityRefusesTheSourceLayerHazards states the part
// reflect.StructOf cannot: the tag is written into Go source inside a
// backtick-delimited raw string literal, where a backtick ends the literal
// (the generated file would not compile) and a carriage return is dropped by
// the scanner (the name would silently lose a character before encoding/json
// ever saw it). Both are refused by the character rule anyway; this says so out
// loud, because an oracle built on reflect agrees whichever way that goes.
func TestTagRepresentabilityRefusesTheSourceLayerHazards(t *testing.T) {
	for _, name := range []string{"`", "a`b", "\r", "a\rb"} {
		if tagNameIsRepresentable(name) {
			t.Errorf("tagNameIsRepresentable(%s) = true; the tag is emitted inside a raw string "+
				"literal, which a backtick closes and which drops a carriage return", strconv.Quote(name))
		}
	}
}

// TestValidTagPunctuationMatchesEncodingJSON pins the one transcribed constant
// against the standard library it was transcribed from, character by character
// -- against both of the standard library's encoding/json implementations.
//
// The property is that validTagPunctuation is exactly the punctuation *every*
// implementation a caller can compile generated code with accepts in a tag
// name: the intersection, not whichever one the test binary links. Go 1.25 and
// 1.26 default to the original implementation and 1.27 to the v2-backed one,
// and each offers the other through GOEXPERIMENT, so the question is the same
// on every toolchain the module supports and so is the answer. This test used
// to compare the constant with the running binary alone, and on Go 1.27 it
// failed 138 times on a clean checkout, asking for characters only v2 accepts
// -- characters that, added to the constant, would become keys the original
// implementation silently renames.
//
// Each character is asked about inside a name ("a" + c + "b") rather than
// alone, because the constant is a statement about characters: alone, "-" is
// the tag that means "skip this field" and is refused for that reason, which
// tagNameIsRepresentable handles separately and the corpus test above checks.
//
// The corpus test would catch a character wrongly added to the constant (that
// name then fails to round-trip) and one wrongly removed. This states the
// source of the list, so the failure names the cause instead of a list of
// surprising strings.
func TestValidTagPunctuationMatchesEncodingJSON(t *testing.T) {
	var runes []rune
	var names []string
	for r := rune(0); r < 0x300; r++ {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue // admitted by the letter/digit arm, not by the list
		}
		runes = append(runes, r)
		names = append(names, "a"+string(r)+"b")
	}
	verdicts := encodingJSONVerdicts(t, names)
	for i, r := range runes {
		want := verdicts[i].carriedByAll()
		got := strings.ContainsRune(validTagPunctuation, r)
		switch {
		case got && !want:
			t.Errorf("validTagPunctuation contains %q, and %s rejects it", r, verdicts[i].rejectedBy())
		case !got && want:
			t.Errorf("validTagPunctuation omits %q, and every encoding/json implementation accepts it", r)
		}
	}
}

// TestEmittedTagSpellingsMatchTheStructTemplate ties the spellings the oracle
// tries to the ones the emitter can actually write.
//
// Without it the oracle measures a tag nobody emits. That is not hypothetical:
// the whole reason "-" was broken and "" was not caught by the older tests is
// that the bare spelling -- the one a *required* property gets, with no option
// after the name -- was the only one that erased the field, and nothing in the
// tree exercised a required property with an untaggable name against a real
// encoder.
func TestEmittedTagSpellingsMatchTheStructTemplate(t *testing.T) {
	src, err := os.ReadFile("../emitter/templates/struct.go.tmpl")
	if err != nil {
		t.Fatalf("reading the struct template: %v", err)
	}
	const tagExpr = "`json:\"{{.JSONName}}{{if .OmitZero}},omitzero{{else if .OmitEmpty}},omitempty{{end}}\"`"
	if !strings.Contains(string(src), tagExpr) {
		t.Fatalf("the struct template no longer writes the field tag as\n\t%s\n"+
			"so emittedTagSpellings may no longer be every spelling a property name is put into. "+
			"Re-derive that list from the template before changing this test", tagExpr)
	}
	for _, opts := range emittedTagSpellings {
		if opts != "" && !strings.Contains(tagExpr, opts) {
			t.Errorf("emittedTagSpellings names %q, which the template never writes", opts)
		}
	}
	if fmt.Sprint(emittedTagSpellings) != "[ ,omitempty ,omitzero]" {
		t.Errorf("emittedTagSpellings = %q; the bare spelling is the one a required property gets "+
			"and it is the only one that erases a field named \"-\"", emittedTagSpellings)
	}
}
