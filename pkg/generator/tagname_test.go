package generator

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode"
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

// encodingJSONCarries reports what encoding/json actually does with the tag the
// emitter would write for this property name: true only when, for every spelling
// the template can produce, marshalling writes exactly this key and unmarshalling
// reads exactly this key back.
//
// It is deliberately an experiment rather than a re-implementation. The whole
// defect behind issues #246 and #247 was a hand-written model of the tag grammar
// that had drifted from the parser, and a second hand-written model would only
// move the drift somewhere else.
//
// The struct is built with reflect.StructOf, which models everything below
// encoding/json but not the Go source layer above it: a backtick would close the
// raw string literal the tag is written in, and the scanner drops a carriage
// return from one. Neither is visible here, and neither has to be --
// tagNameIsRepresentable must refuse both anyway, which
// TestTagRepresentabilityRefusesTheSourceLayerHazards states separately.
//
// The field is called "F" -- exported -- on purpose, and that is the boundary of
// what this file measures. The question here is only whether a *tag* can carry a
// name; whether the *field* the emitter mints for it can be serialized at all is
// a separate mechanism, and a broken one: a property named 日本語 becomes a Go
// field named 日本語, which has no upper case and so is unexported, and
// encoding/json ignores an unexported field however good its tag is. That is not
// something tagNameIsRepresentable can or should decide -- `json:"日本語"` is a
// perfectly valid tag, as this oracle will confirm -- and repairing it means
// changing the Go name the generator hands its callers. It belongs to
// JSONPropertyToGoName, whose doc comment already promises "a Go exported field
// name", not to the tag grammar.
func encodingJSONCarries(jsonName string) bool {
	for _, opts := range emittedTagSpellings {
		typ := reflect.StructOf([]reflect.StructField{{
			Name: "F",
			Type: reflect.TypeOf(""),
			Tag:  reflect.StructTag(`json:"` + jsonName + opts + `"`),
		}})

		// Encode: the one key written must be this name.
		v := reflect.New(typ).Elem()
		v.Field(0).SetString("written")
		out, err := json.Marshal(v.Interface())
		if err != nil {
			return false
		}
		var got map[string]string
		if err := json.Unmarshal(out, &got); err != nil {
			return false
		}
		if len(got) != 1 || got[jsonName] != "written" {
			return false
		}

		// Decode: this name must reach the field.
		in, err := json.Marshal(map[string]string{jsonName: "read"})
		if err != nil {
			return false
		}
		p := reflect.New(typ)
		if err := json.Unmarshal(in, p.Interface()); err != nil {
			return false
		}
		if p.Elem().Field(0).String() != "read" {
			return false
		}
	}
	return true
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
// characters somebody has been bitten by.
//
// The load-bearing direction is the first: a name the predicate calls
// representable but encoding/json does not carry is a key silently renamed or
// dropped on every document -- that is exactly what issues #246 and #247 were,
// and what the "-" and 🎉 cases were before anyone filed them. It is asserted
// unconditionally.
//
// The second direction -- that a name the predicate refuses is one
// encoding/json would really have mangled -- keeps the hand-written path from
// quietly swallowing names a tag could have carried. It is asserted only when
// encoding/json is running its v1 tag rules, because GOEXPERIMENT=jsonv2 accepts
// names v1 discards (it reserves only , \ ' " and the backtick) and the extra
// permissiveness is not something this generator should depend on: a type
// generated here has to work in a caller's build, and the caller chooses the
// experiment.
func TestTagRepresentabilityMatchesEncodingJSON(t *testing.T) {
	v1Rules := !encodingJSONCarries("🎉")
	if !v1Rules {
		t.Logf("encoding/json is carrying a tag name its v1 rules reject, so this build has " +
			"GOEXPERIMENT=jsonv2 semantics; only the direction that matters for correctness is checked")
	}

	for _, name := range tagNameCorpus() {
		representable := tagNameIsRepresentable(name)
		carried := encodingJSONCarries(name)

		if representable && !carried {
			t.Errorf("tagNameIsRepresentable(%s) = true, but encoding/json does not carry that name "+
				"through the tag the emitter writes -- the key would be silently renamed or dropped on "+
				"every document. The predicate has to follow encoding/json's own grammar (parseTag, "+
				"isValidTag, and the `json:\"-\"` special case); see needsManualJSON",
				strconv.Quote(name))
		}
		if v1Rules && !representable && carried {
			t.Errorf("tagNameIsRepresentable(%s) = false, but encoding/json carries that name fine -- "+
				"the property is being pushed onto the hand-written marshal path for no reason",
				strconv.Quote(name))
		}
	}

	// needsManualJSON is the complement every caller asks for, and it is the one
	// that would be edited by somebody who never reads the predicate.
	for _, name := range tagNameCorpus() {
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
// against the standard library it was transcribed from, character by character.
//
// The corpus test above would catch a character wrongly added to it (that name
// then fails to round-trip through encoding/json) and, under v1 rules, one
// wrongly removed. This states the source of the list, so the failure names the
// cause instead of a list of surprising strings.
func TestValidTagPunctuationMatchesEncodingJSON(t *testing.T) {
	for r := rune(0); r < 0x300; r++ {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue // admitted by the letter/digit arm, not by the list
		}
		want := encodingJSONCarries(string(r))
		got := strings.ContainsRune(validTagPunctuation, r)
		// "-" alone is the tag that means "skip this field", so encoding/json
		// does not carry it even though isValidTag admits the character.
		if r == '-' {
			want = true
		}
		if got != want {
			t.Errorf("validTagPunctuation %s %q, and encoding/json %s it",
				map[bool]string{true: "contains", false: "omits"}[got], r,
				map[bool]string{true: "accepts", false: "rejects"}[want])
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
