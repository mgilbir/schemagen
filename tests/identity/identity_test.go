package identity

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
)

// The checks that compare values -- uniqueItems, const, enum -- compare them by
// identity (the emitted jsonID), read off each value as it is held: a struct's
// jsonIdentity reads its members by the rules its appendJSON writes them by. They
// used to marshal the value and compare the text, which at every level of a
// document wrote out every subtree below it -- 0.8 to 3 ms of Validate per
// CycloneDX BOM. The two tests here hold the replacement to what it replaced.
//
// TestIdentityIsWhatMarshalJSONWrites is the correctness half: an identity read
// off a value is the identity of the text MarshalJSON writes for it, for every
// value of every type that reads its own, in every CycloneDX example BOM -- and
// again with the value changed the ways a document never changes it, which is
// where the rules a decoded value never exercises are: a member set twice by
// the overflow map, a string that is not UTF-8, the spellings of a number.
//
// TestValidateWritesNothing is the other half: Validate, run over every BOM,
// executes no statement that writes a value out.

func TestIdentityIsWhatMarshalJSONWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("generates and compiles the CycloneDX 1.6 types")
	}
	t.Parallel()
	root, boms := cycloneDXModule(t, map[string]string{
		"cdx/identity_check.go": identityCheckSource("cdx"),
		"idcheck/main.go":       identityCheckDriver,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := testgo.Command(ctx, root, append([]string{"run", "-mod=mod", "./idcheck"}, boms...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("driver: %v\n%s", err, output)
	}
	got := strings.TrimSpace(string(output))
	m := regexp.MustCompile(`^PASS (\d+) values, (\d+) changed$`).FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("identities that are not what MarshalJSON writes:\n%s", got)
	}
	// A floor, so that a walk that reached nothing -- no type reading its own
	// identity, or none of the values -- does not pass for having found no
	// difference.
	values, _ := strconv.Atoi(m[1])
	changed, _ := strconv.Atoi(m[2])
	if values < 500 || changed < 3000 {
		t.Fatalf("the walk compared %d values and %d changed ones; the BOMs hold far more", values, changed)
	}
	t.Logf("compared %d values and %d changed values across %d BOMs", values, changed, len(boms))
}

const identityCheckDriver = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"ex.test/cdx/cdx"
)

func main() {
	values, changed, bad := 0, 0, 0
	for _, path := range os.Args[1:] {
		in, err := os.ReadFile(path)
		if err != nil {
			panic(err)
		}
		var bom cdx.Bom
		if err := json.Unmarshal(in, &bom); err != nil {
			fmt.Printf("%s: does not decode: %v\n", filepath.Base(path), err)
			bad++
			continue
		}
		diffs, v, c := cdx.SchemagenIdentityDiffs(&bom)
		values += v
		changed += c
		for _, d := range diffs {
			fmt.Printf("%s: %s\n", filepath.Base(path), d)
			bad++
		}
	}
	if bad == 0 {
		fmt.Printf("PASS %d values, %d changed\n", values, changed)
	}
}
`

// TestIdentityIsJSONEquality holds the identity helpers to what an identity is
// for: two values equal as JSON share one, and two that differ do not. The
// differential above cannot see a rule both of its sides get wrong -- both read
// numbers and strings through the same functions -- so the rules are held here
// to JSON itself: numbers compared by value however spelled, members in any
// order, a key written twice meaning its last value, strings compared by their
// characters however escaped, and a value read lazily from a document as
// encoding/json decodes it into an any.
func TestIdentityIsJSONEquality(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs the identity helpers")
	}
	t.Parallel()
	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}
	src, needed, err := em.EmitHelpers("idsem", generator.HelperSet{IdentityValue: true, IdentityKind: true, IdentityAny: true, Decode: true})
	if err != nil || !needed {
		t.Fatalf("emitting the identity helpers: needed %v, %v", needed, err)
	}
	dir := t.TempDir()
	if err := writeTestGoMod(dir, "ex.test/idsem"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "helpers.go"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "semantics_test.go"), []byte(identitySemanticsTest), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if out, err := testgo.Command(ctx, dir, "test", "-mod=mod", "-count=1", ".").CombinedOutput(); err != nil {
		t.Fatalf("the identity helpers do not read JSON equality:\n%s", out)
	}
}

const identitySemanticsTest = `package idsem

import (
	"encoding/json"
	"math"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func rawID(t *testing.T, raw string) jsonID {
	t.Helper()
	id, err := jsonIDRaw([]byte(raw))
	if err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return id
}

func TestEqualAsJSONIsOneIdentity(t *testing.T) {
	same := [][2]string{
		{"1", "1.0"}, {"100", "1e2"}, {"0", "-0.0"}, {"0.5", "5e-1"}, {"-12.50", "-1.25e1"},
		{"123456789012345678901234567890", "1.2345678901234567890123456789e29"},
		{"{\"a\":1,\"b\":2}", "{\"b\":2,\"a\":1}"},
		{"{\"a\":1,\"a\":2}", "{\"a\":2}"},
		{"\"\u00e9\"", "\"\\u00e9\""}, {"\"\\ud800\"", "\"\\ufffd\""}, {"\"\\ud83d\\ude00\"", "\"\U0001F600\""},
		{"\"a\\/b\"", "\"a/b\""}, {"[1,[2]]", " [ 1 , [ 2.0 ] ] "},
	}
	for _, p := range same {
		if rawID(t, p[0]) != rawID(t, p[1]) {
			t.Errorf("%s and %s are one JSON value and have two identities", p[0], p[1])
		}
	}
	diff := [][2]string{
		{"1", "\"1\""}, {"[]", "{}"}, {"null", "false"}, {"true", "false"}, {"[1,2]", "[2,1]"},
		{"{\"a\":1}", "{\"a\":1,\"b\":1}"}, {"{\"a\":[1]}", "{\"a\":1}"},
		{"123456789012345678901234567890", "123456789012345678901234567891"}, {"1.5", "15"}, {"0.1", "1"},
		{"[[]]", "[]"}, {"{\"a\":{}}", "{\"a\":[]}"}, {"\"\"", "null"}, {"{\"a\":1}", "{\"b\":1}"},
		{"[1,[2,3]]", "[[1,2],3]"}, {"{\"ab\":\"c\"}", "{\"a\":\"bc\"}"},
	}
	for _, p := range diff {
		if rawID(t, p[0]) == rawID(t, p[1]) {
			t.Errorf("%s and %s are different JSON values and share an identity", p[0], p[1])
		}
	}
}

type namedString string

func refID(t *testing.T, v any) jsonID {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %#v: %v", v, err)
	}
	return rawID(t, string(b))
}

func check[T any](t *testing.T, v T) {
	t.Helper()
	got, err := jsonIdentifyAt(&v, nil)
	if err != nil {
		t.Fatalf("%#v: %v", v, err)
	}
	if got != refID(t, v) {
		b, _ := json.Marshal(v)
		t.Errorf("%T %#v: its identity is not that of %s", v, v, b)
	}
	if got, err := jsonIDAny(v, nil); err != nil || got != refID(t, v) {
		t.Errorf("%T %#v held as an any: %v", v, v, err)
	}
}

func TestAGoValueIsWhatEncodingJSONWrites(t *testing.T) {
	zone := time.FixedZone("x", -(5*3600 + 30*60))
	check(t, "plain")
	check(t, "h\xffi\xfe")
	check(t, "<&>\u2028")
	check(t, namedString("n\xc3"))
	check(t, 0.0)
	check(t, math.Copysign(0, -1))
	check(t, 1e21)
	check(t, 1e-7)
	check(t, 123456789.125)
	check(t, float32(0.1))
	check(t, []float32{0.1, 3, -2.5e-9})
	check(t, int64(-42))
	check(t, uint8(200))
	check(t, json.Number("1.0"))
	check(t, json.Number("-0.000e5"))
	check(t, json.Number(""))
	check(t, json.RawMessage(" { \"b\" : [1, 2.50, \"\\u00e9\\ud800x\"], \"a\":{\"z\":null,\"z\":true} } "))
	check(t, json.RawMessage(nil))
	check(t, map[string]any{"k": []any{1.5, "x", nil, true, map[string]any{}}, "j": json.Number("7")})
	check(t, []any{})
	check(t, []any(nil))
	check(t, map[string]any(nil))
	check(t, []string{"a", "b\x80"})
	check(t, map[string]string{"a": "b", "c": ""})
	check(t, []byte("bytes"))
	check(t, [3]byte{1, 2, 3})
	check(t, time.Date(2024, 2, 29, 12, 0, 0, 500, time.UTC))
	check(t, time.Date(1999, 12, 31, 23, 59, 59, 0, zone))
	check(t, netip.MustParseAddr("::1"))
	check(t, map[string]*int64{"a": nil})
	s := "p"
	check(t, &s)
	check(t, map[string]json.RawMessage{"x": json.RawMessage("[1,1.0]")})
}

func TestAValueEncodingJSONRefusesHasNoIdentity(t *testing.T) {
	for _, v := range []any{math.NaN(), math.Inf(1), json.Number("1x"), json.RawMessage("{"), json.RawMessage{},
		time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1, 1, 1, 0, 0, 0, 0, time.FixedZone("far", 25*3600))} {
		if _, err := jsonIDAny(v, nil); err == nil {
			t.Errorf("%#v: encoding/json refuses it, and it was given an identity", v)
		}
		if _, err := json.Marshal(v); err == nil {
			t.Errorf("%#v: encoding/json writes it; the case is wrong", v)
		}
	}
}

func TestALazyValueIsReadAsDecoded(t *testing.T) {
	doc := []byte(" {\"a\":[12345678901234567890, 1.0, \"\\ud800\", {\"k\":1,\"k\":2}],\"b\":{}} ")
	d, sp, err := jsonOpenDoc(doc)
	if err != nil {
		t.Fatal(err)
	}
	// As every lazily read value's document is: its parts kept, and the
	// caller's buffer let go of.
	d.keep(sp)
	d.finish(nil)
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		t.Fatal(err)
	}
	l := jsonLazy{d, sp}
	for round := 0; round < 2; round++ {
		// The second round reads what the first kept on the document.
		got, err := l.jsonIdentity(nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != refID(t, v) {
			t.Errorf("round %d: a lazily read value is not what encoding/json decodes it into", round)
		}
	}
	a := l.jsonLevel().(map[string]any)["a"].(jsonLazy).jsonLevel().([]any)
	ids := make([]jsonID, len(a))
	for i := range a {
		if ids[i], err = jsonIDAny(a[i], nil); err != nil {
			t.Fatal(err)
		}
	}
	if jsonFirstDuplicate(a, ids, jsonIdentifyAt[any]) >= 0 {
		t.Errorf("distinct elements reported as duplicates")
	}
}

// The decoded-JSON reader (jsonIDJSON, jsonTreeJSON) is what the evaluator and
// the dynamic checks compare by, without the walker; it must read every decoded
// value -- whole, with json.Number, and lazily, a level at a time -- exactly as
// the walker does, or a const read one way would be refused a value read the
// other.
func TestADecodedValueIsReadAsTheWalkerReadsIt(t *testing.T) {
	docs := []string{
		"null", "true", "0", "-0.0", "1e2", "12345678901234567890", "\"a\\u00e9\"",
		"[]", "{}", "[1, 1.0, \"x\", [null], {\"b\":2,\"a\":1}]",
		" {\"a\":[12345678901234567890, 1.0, \"\\ud800\", {\"k\":1,\"k\":2}],\"b\":{},\"c\":[[[]]]} ",
	}
	for _, doc := range docs {
		var whole, exact any
		if err := json.Unmarshal([]byte(doc), &whole); err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(strings.NewReader(doc))
		dec.UseNumber()
		if err := dec.Decode(&exact); err != nil {
			t.Fatal(err)
		}
		d, sp, err := jsonOpenDoc([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		d.keep(sp)
		d.finish(nil)
		lazy := jsonLazy{d, sp}
		for name, v := range map[string]any{"whole": whole, "exact": exact, "lazy": lazy, "levelled": jsonTop(lazy)} {
			got, err := jsonIDJSON(v)
			if err != nil {
				t.Fatalf("%s %s: %v", doc, name, err)
			}
			want, err := jsonIDAny(v, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("%s %s: the decoded-JSON reader's identity is not the walker's", doc, name)
			}
			tree, err := jsonTreeJSON(v)
			if err != nil {
				t.Fatalf("%s %s: %v", doc, name, err)
			}
			wantTree, err := jsonTreeAny(v, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !jsonTreeEqual(tree, wantTree) || !jsonTreeEqual(wantTree, tree) {
				t.Errorf("%s %s: tree %v is not the walker's %v", doc, name, tree, wantTree)
			}
			// Read as a float64, as the evaluator reads a decoded value, but for
			// the value decoded with its numbers exact.
			if ok, err := jsonMatchesJSON(v, jsonConstOf(name != "exact", doc)); err != nil || !ok {
				t.Errorf("%s %s: not a match for its own text (%v)", doc, name, err)
			}
		}
	}
	var arr []any
	if err := json.Unmarshal([]byte("[{\"a\":1,\"b\":[2]}, \"x\", {\"b\":[2.0],\"a\":1e0}]"), &arr); err != nil {
		t.Fatal(err)
	}
	if dup, err := jsonFirstDuplicateJSON(arr); err != nil || dup != 2 {
		t.Errorf("the duplicate is at 2; found %d (%v)", dup, err)
	}
	if dup, err := jsonFirstDuplicateJSON(arr[:2]); err != nil || dup != -1 {
		t.Errorf("no duplicate in two distinct elements; found %d (%v)", dup, err)
	}
	// A value that is not decoded JSON is refused, not guessed at.
	if _, err := jsonIDJSON(struct{ A int }{1}); err == nil {
		t.Errorf("a Go struct was read as decoded JSON")
	}
	if _, err := jsonMatchesJSON([]any{int64(1)}, jsonConstOf(false, "[1]")); err == nil {
		t.Errorf("an int64 inside a decoded array was read as decoded JSON")
	}
}

func TestADuplicateIsConfirmedAndACollisionIsNot(t *testing.T) {
	// Identities made to collide: every element is given one identity. Only
	// the elements that really are equal may be called duplicates.
	s := []any{1.0, "x", 2.0, "x"}
	one := make([]jsonID, len(s))
	if got := jsonFirstDuplicate(s, one, jsonIdentifyAt[any]); got != 3 {
		t.Errorf("first duplicate: got %d, want 3", got)
	}
	big := make([]any, 20)
	for i := range big {
		big[i] = float64(i)
	}
	big[19] = 3.0
	if got := jsonFirstDuplicate(big, make([]jsonID, len(big)), jsonIdentifyAt[any]); got != 19 {
		t.Errorf("first duplicate among colliding identities: got %d, want 19", got)
	}
}

func TestAConstIsDecidedExactly(t *testing.T) {
	// A const the value's identity collides with: the identity is the value's,
	// the literal is another. An identity may only ever decide a mismatch, so
	// this must not admit the value.
	v := map[string]any{"a": json.Number("1"), "b": []any{"x"}}
	id, err := jsonIdentifyAt(&v, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := jsonTreeRaw([]byte("{\"a\":2,\"b\":[\"x\"]}"), false)
	if err != nil {
		t.Fatal(err)
	}
	colliding := &jsonConst{ids: []jsonID{id}, trees: []any{other}}
	if ok, err := jsonMatchesConstAt(&v, colliding); err != nil || ok {
		t.Errorf("a value sharing an identity with a const it is not was admitted: %v, %v", ok, err)
	}
	if ok, err := jsonMatchesConstRaw([]byte("{\"b\":[\"x\"],\"a\":1.0}"), &jsonConst{ids: []jsonID{rawID(t, "{\"a\":1,\"b\":[\"x\"]}")}, trees: []any{other}}); err != nil || ok {
		t.Errorf("raw JSON sharing an identity with a const it is not was admitted: %v, %v", ok, err)
	}
	// And the value itself, spelled otherwise, is admitted.
	same := jsonConstOf(false, "{\"b\":[\"x\"],\"a\":1.0}")
	if ok, err := jsonMatchesConstAt(&v, same); err != nil || !ok {
		t.Errorf("a value equal to the const as JSON was refused: %v, %v", ok, err)
	}
	// A duplicate is confirmed the same way.
	s := []any{v, map[string]any{"a": json.Number("2"), "b": []any{"x"}}}
	if got := jsonFirstDuplicate(s, []jsonID{id, id}, jsonIdentifyAt[any]); got != -1 {
		t.Errorf("two different elements sharing an identity were called duplicates")
	}
}

func TestATreeIsWhatEncodingJSONDecodes(t *testing.T) {
	for _, v := range []any{"a\xff", 1.5, float32(0.1), int64(-3), json.Number("2.50"), nil, true, []byte("xy"),
		map[string]any{"k": []any{json.RawMessage(" {\"a\":1,\"a\":2} "), namedString("n")}},
		time.Date(1999, 12, 31, 23, 59, 59, 0, time.FixedZone("x", 3600)), netip.MustParseAddr("::1"), map[string]string{"x": "y"}} {
		got, err := jsonTreeAny(v, nil)
		if err != nil {
			t.Fatalf("%#v: %v", v, err)
		}
		b, _ := json.Marshal(v)
		want, err := jsonTreeRaw(b, false)
		if err != nil || !jsonTreeEqual(got, want) {
			t.Errorf("%#v: tree %#v is not what encoding/json decodes %s into", v, got, b)
		}
	}
	if jsonTreeEqual(json.Number("1"), "1") || !jsonTreeEqual(json.Number("1.0"), 1.0) || jsonTreeEqual([]any{}, map[string]any{}) {
		t.Errorf("tree equality is not JSON equality")
	}
}

func TestAKindIsWhatEncodingJSONWrites(t *testing.T) {
	cases := []struct {
		v    any
		kind byte
		text string
	}{
		{"a\xff", jsonIDStringKind, "a\ufffd"}, {1.5, jsonIDNumberKind, "1.5"}, {int64(-3), jsonIDNumberKind, "-3"},
		{json.Number("2.50"), jsonIDNumberKind, "2.50"}, {nil, jsonIDNullKind, ""}, {true, jsonIDTrueKind, ""},
		{[]any{}, jsonIDArrayKind, ""}, {map[string]any{}, jsonIDObjectKind, ""},
		{json.RawMessage(" \"x\\u0041\" "), jsonIDStringKind, "xA"}, {json.RawMessage(" 1e2 "), jsonIDNumberKind, "1e2"},
		{namedString("n"), jsonIDStringKind, "n"}, {math.NaN(), 0, ""}, {netip.MustParseAddr("::1"), jsonIDStringKind, "::1"},
	}
	for _, c := range cases {
		kind, text := jsonKindAny(c.v)
		if kind != c.kind || text != c.text {
			t.Errorf("%#v: kind %q text %q, want %q %q", c.v, kind, text, c.kind, c.text)
		}
	}
}
`

func TestValidateWritesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("generates, compiles and runs the CycloneDX 1.6 types with coverage")
	}
	t.Parallel()
	root, boms := cycloneDXModule(t, map[string]string{"covdrv/main.go": validateCoverageDriver})
	writes, executed := validateUnderCoverage(t, root, "cdx", boms, fmt.Sprintf("validated %d", len(boms)))
	// A floor, as in the identity test: counters that recorded nothing would
	// find nothing.
	if executed < 1000 {
		t.Fatalf("only %d blocks of the generated package ran during Validate; the counters are not reading it", executed)
	}
	if len(writes) > 0 {
		t.Errorf("Validate wrote values out, %d places, over the %d CycloneDX example BOMs:\n\t%s",
			len(writes), len(boms), strings.Join(writes, "\n\t"))
	}
}

// validateUnderCoverage builds root's ./covdrv with coverage over the whole
// module, runs it with args after the counters directory -- the driver clears
// the counters once it has decoded what it validates -- checks it printed want,
// and returns the statements of package dir that ran and write a value out,
// and how many of that package's blocks ran.
func validateUnderCoverage(t *testing.T, root, dir string, args []string, want string) ([]string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	bin := filepath.Join(root, "covdrv.bin")
	build := testgo.Command(ctx, root, "build", "-mod=mod", "-cover", "-covermode=atomic", "-coverpkg=./...", "-o", bin, "./covdrv")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the driver with coverage: %v\n%s", err, out)
	}
	counters := filepath.Join(root, "counters")
	if err := os.MkdirAll(counters, 0o755); err != nil {
		t.Fatal(err)
	}
	run := exec.CommandContext(ctx, bin, append([]string{counters}, args...)...)
	run.Env = append(os.Environ(), "GOCOVERDIR="+counters)
	if out, err := run.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != want {
		t.Fatalf("driver: %v\n%s", err, out)
	}
	profile := filepath.Join(root, "profile.txt")
	textfmt := testgo.Command(ctx, root, "tool", "covdata", "textfmt", "-i="+counters, "-o="+profile)
	if out, err := textfmt.CombinedOutput(); err != nil {
		t.Fatalf("reading the counters: %v\n%s", err, out)
	}
	// The module is named after dir and so is the package in it: a coverage
	// profile names the package's files by its import path.
	writes, executed, err := executedWrites(profile, filepath.Join(root, dir), "/"+dir+"/"+dir+"/")
	if err != nil {
		t.Fatal(err)
	}
	return writes, executed
}

// TestHeldElementsAreJudgedWithoutWriting is TestValidateWritesNothing for the
// elements held as decoded JSON whose sub-schema has a type of its own -- a
// tuple position, a contains, an inferred array's items, a tuple's tail. They
// were marshalled and decoded into the type to be judged; now they are judged
// as they are held, by the type's schema compiled for the evaluator (see
// ElementNode). The values are decoded ones and ones built in Go -- an element
// holding a value of the generated type itself, a pointer to one, an int64 --
// and each is held to the verdict the schema gives it.
func TestHeldElementsAreJudgedWithoutWriting(t *testing.T) {
	if testing.Short() {
		t.Skip("generates, compiles and runs a package with coverage")
	}
	t.Parallel()
	out := t.TempDir()
	schemaPath := filepath.Join(out, "held.json")
	if err := os.WriteFile(schemaPath, []byte(heldElementsSchema), 0o644); err != nil {
		t.Fatal(err)
	}
	runSchemagen(t, schemagenBinary(t), "generate", schemaPath, "-o", filepath.Join(out, "held"), "-p", "held", "--root-name", "held.json=Root")
	if err := writeTestGoMod(out, "ex.test/held"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(out, "covdrv"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "covdrv", "main.go"), []byte(heldElementsDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	writes, executed := validateUnderCoverage(t, out, "held", nil, "PASS")
	if executed < 50 {
		t.Fatalf("only %d blocks of the generated package ran during Validate; the counters are not reading it", executed)
	}
	if len(writes) > 0 {
		t.Errorf("Validate wrote values out to judge held elements, %d places:\n\t%s", len(writes), strings.Join(writes, "\n\t"))
	}
}

const heldElementsSchema = `{"$schema":"https://json-schema.org/draft/2020-12/schema",
 "$defs":{"P":{"type":"object","properties":{"a":{"type":"integer","minimum":1},"k":{"type":"array","items":{"$ref":"#/$defs/P"}}},"required":["a"]},
          "Loose":{"items":{"$ref":"#/$defs/P"},"minItems":1}},
 "type":"object",
 "properties":{
   "tup":{"type":"array","prefixItems":[{"$ref":"#/$defs/P"},{"type":"string"}],"items":{"$ref":"#/$defs/P"}},
   "has":{"type":"array","contains":{"$ref":"#/$defs/P"}},
   "loose":{"$ref":"#/$defs/Loose"},
   "tail":{"type":"array","prefixItems":[{"type":"string"}],"items":{"type":"object","required":["a"],"properties":{"a":{"const":2}}}}
 }}`

const heldElementsDriver = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/coverage"

	"ex.test/held/held"
)

func main() {
	docs := []struct {
		doc   string
		valid bool
	}{
		{` + "`" + `{"tup":[{"a":1,"k":[{"a":2}]},"x",{"a":3}],"has":[1,{"a":1}],"loose":[{"a":1}],"tail":["s",{"a":2}]}` + "`" + `, true},
		{` + "`" + `{"tup":[{"a":0},"x"]}` + "`" + `, false},
		{` + "`" + `{"tup":[{"a":1,"k":[{"a":-1}]}]}` + "`" + `, false},
		{` + "`" + `{"has":[1,"x",{"b":1}]}` + "`" + `, false},
		{` + "`" + `{"loose":[{"a":1},{}]}` + "`" + `, false},
		{` + "`" + `{"tail":["s",{"a":3}]}` + "`" + `, false},
		{` + "`" + `{"tup":[{"a":1},"x",{"a":1.5}]}` + "`" + `, false},
	}
	var decoded []held.Root
	for _, d := range docs {
		var r held.Root
		if err := json.Unmarshal([]byte(d.doc), &r); err != nil {
			fmt.Println("decode:", d.doc, err)
			os.Exit(1)
		}
		decoded = append(decoded, r)
	}
	built := []struct {
		r     held.Root
		valid bool
	}{
		{held.Root{Tup: []any{held.P{A: 1, K: []held.P{{A: 2}}}, "x", &held.P{A: 3}}, Has: []any{int64(1), held.P{A: 1}}, Tail: []any{"s", map[string]any{"a": int64(2)}}}, true},
		{held.Root{Tup: []any{held.P{A: 0}, "x"}}, false},
		{held.Root{Tup: []any{map[string]any{"a": int64(1), "k": []any{held.P{A: -1}}}}}, false},
		{held.Root{Tail: []any{"s", held.P{A: 2}}}, true},
		{held.Root{Tail: []any{"s", map[string]any{"a": 3}}}, false},
		{held.Root{Tup: []any{&held.P{A: 1}, "x", float32(1.5)}}, false},
	}
	if err := coverage.ClearCounters(); err != nil {
		panic(err)
	}
	for i, r := range decoded {
		if err := r.Validate(); (err == nil) != docs[i].valid {
			fmt.Println("decoded:", docs[i].doc, "valid:", docs[i].valid, "Validate:", err)
			os.Exit(1)
		}
	}
	for i, b := range built {
		if err := b.r.Validate(); (err == nil) != b.valid {
			fmt.Println("built:", i, "valid:", b.valid, "Validate:", err)
			os.Exit(1)
		}
	}
	if err := coverage.WriteCountersDir(os.Args[1]); err != nil {
		panic(err)
	}
	fmt.Println("PASS")
}
`

// TestStrippedAndNulledValuesAreReadWithoutWriting holds the three rulings
// appendJSON makes by what it writes -- the writeOnly locations
// --strict-read-write strips from below a struct's members and from a value
// held whole, the nulls a document wrote that are written back, and a
// hand-written member whose Go zero is left out -- to what they were when the
// identity was read off the text appendJSON wrote. Now they are made on trees:
// each identity, and each tree, is compared with what MarshalJSON writes, for
// decoded values and for the same values changed the ways a document never
// changes them; and then Validate, comparing the values by uniqueItems, runs
// with coverage and must write nothing out.
func TestStrippedAndNulledValuesAreReadWithoutWriting(t *testing.T) {
	if testing.Short() {
		t.Skip("generates, compiles and runs a package with coverage")
	}
	t.Parallel()
	for _, c := range []struct {
		name  string
		flags []string
	}{
		{"strict", []string{"--strict-read-write"}},
		// Named without a comma: covdata reads one in its input directory as
		// a list of two.
		{"strict-noomit", []string{"--strict-read-write", "--omit-empty=false"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out := t.TempDir()
			schemaPath := filepath.Join(out, "sn.json")
			if err := os.WriteFile(schemaPath, []byte(strippedNulledSchema), 0o644); err != nil {
				t.Fatal(err)
			}
			runSchemagen(t, schemagenBinary(t), append([]string{"generate", schemaPath, "-o", filepath.Join(out, "sn"), "-p", "sn", "--root-name", "sn.json=Root"}, c.flags...)...)
			if err := writeTestGoMod(out, "ex.test/sn"); err != nil {
				t.Fatal(err)
			}
			for rel, content := range map[string]string{
				"sn/identity_check.go": identityCheckSource("sn"),
				"covdrv/main.go":       strippedNulledDriver,
			} {
				p := filepath.Join(out, rel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			writes, executed := validateUnderCoverage(t, out, "sn", nil, "PASS")
			if executed < 50 {
				t.Fatalf("only %d blocks of the generated package ran during Validate; the counters are not reading it", executed)
			}
			if len(writes) > 0 {
				t.Errorf("Validate wrote values out to compare them, %d places:\n\t%s", len(writes), strings.Join(writes, "\n\t"))
			}
		})
	}
}

// strippedNulledSchema has a type of each kind: Holder strips writeOnly members
// from below a tuple slot and a patternProperties value, Leftover is held whole
// and strips from its bytes, Nulls writes back the nulls a document wrote, and
// Zero's members, named so no struct tag can carry them, are left out where they
// write what their Go zero writes.
const strippedNulledSchema = `{"$schema":"https://json-schema.org/draft/2020-12/schema",
 "$defs":{
  "Access":{"type":"object","properties":{"ro":{"type":"integer","readOnly":true},"wo":{"type":"integer","writeOnly":true},"ok":{"type":"integer"}}},
  "Holder":{"type":"object","properties":{
      "tuple":{"type":"array","prefixItems":[{"$ref":"#/$defs/Access"}]},
      "patterned":{"type":"object","patternProperties":{"^k":{"$ref":"#/$defs/Access"}}},
      "plain":{"type":"string"}}},
  "Nulls":{"type":"object","properties":{
      "ns":{"type":["string","null"]},
      "nn":{"type":["number","null"]},
      "nl":{"type":["array","null"],"items":{"type":"string"}},
      "any":{},
      "s":{"type":"string"}}},
  "Zero":{"type":"object","properties":{"c,d":{"const":"fixed"},"e,f":{"type":"integer","minimum":5},"g,h":{"minLength":2},"i,j":{"type":"number","minimum":5}}},
  "Leftover":{"type":"object","unevaluatedProperties":{"$ref":"#/$defs/Access"}}
 },
 "type":"object",
 "properties":{
   "holders":{"type":"array","uniqueItems":true,"items":{"$ref":"#/$defs/Holder"}},
   "nulls":{"type":"array","uniqueItems":true,"items":{"$ref":"#/$defs/Nulls"}},
   "zeros":{"type":"array","uniqueItems":true,"items":{"$ref":"#/$defs/Zero"}},
   "leftovers":{"type":"array","uniqueItems":true,"items":{"$ref":"#/$defs/Leftover"}}
 }}`

// strippedNulledDriver checks every decoded document's identities against what
// MarshalJSON writes (see identityCheckSource), then clears the counters and
// validates each, holding it to its verdict. The duplicates are values written
// the same: the same members in another order, a number spelled another way, a
// null written back where the document wrote one.
const strippedNulledDriver = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/coverage"

	"ex.test/sn/sn"
)

func main() {
	docs := []struct {
		doc   string
		valid bool
	}{
		{` + "`" + `{"holders":[{"tuple":[{"wo":1,"ok":2,"x":[1,{"a":1.50}]}],"patterned":{"k1":{"wo":3,"ok":4},"z":{"wo":5}},"plain":"p"},{"tuple":[{"ok":3}]}]}` + "`" + `, true},
		{` + "`" + `{"holders":[{"tuple":[{"ok":2}],"plain":"p"},{"plain":"p","tuple":[{"ok":2.0}]}]}` + "`" + `, false},
		{` + "`" + `{"nulls":[{"ns":null,"nn":null,"nl":null,"any":null,"s":"a"},{"ns":"x","nn":-0.0,"nl":["a"],"any":{"b":[1,2]},"s":"a"},{"s":"b"}]}` + "`" + `, true},
		{` + "`" + `{"nulls":[{"ns":null,"s":"a"},{"s":"a","ns":null}]}` + "`" + `, false},
		{` + "`" + `{"zeros":[{"c,d":"fixed","e,f":7,"g,h":"xy"},{"e,f":8},{}]}` + "`" + `, true},
		{` + "`" + `{"zeros":[{"e,f":7},{"e,f":7.0}]}` + "`" + `, false},
		{` + "`" + `{"leftovers":[{"a":{"wo":1,"ok":2}},{"a":{"ok":3}},{"b":{"ok":2}}]}` + "`" + `, true},
		{` + "`" + `{"leftovers":[{"a":{"ok":2e0,"wo":1}},{"a":{"wo":2,"ok":2}}]}` + "`" + `, false},
	}
	var decoded []sn.Root
	values, changed := 0, 0
	for _, d := range docs {
		var r sn.Root
		if err := json.Unmarshal([]byte(d.doc), &r); err != nil {
			fmt.Println("decode:", d.doc, err)
			os.Exit(1)
		}
		diffs, v, c := sn.SchemagenIdentityDiffs(&r)
		for _, diff := range diffs {
			fmt.Println(d.doc, diff)
		}
		if len(diffs) > 0 {
			os.Exit(1)
		}
		values += v
		changed += c
		decoded = append(decoded, r)
	}
	// A floor: a walk that reached nothing would find nothing.
	if values < 30 || changed < 50 {
		fmt.Println("the identity walk compared", values, "values and", changed, "changed ones")
		os.Exit(1)
	}
	if err := coverage.ClearCounters(); err != nil {
		panic(err)
	}
	for i, r := range decoded {
		if err := r.Validate(); (err == nil) != docs[i].valid {
			fmt.Println("decoded:", docs[i].doc, "valid:", docs[i].valid, "Validate:", err)
			os.Exit(1)
		}
	}
	if err := coverage.WriteCountersDir(os.Args[1]); err != nil {
		panic(err)
	}
	fmt.Println("PASS")
}
`

// encodingCall is every way generated code writes a value out: encoding/json,
// a MarshalJSON, the generated encoder, and the reduction to canonical text,
// which writes strings through encoding/json.
var encodingCall = regexp.MustCompile(`json\.Marshal\(|json\.MarshalIndent\(|json\.NewEncoder\(|\.MarshalJSON\(\)|\.appendJSON\(|jsonAppendLeaf|jsonLeafOmit|_jsonCanonical\(`)

// executedWrites reads a coverage profile and reports every block of the
// package in dir that ran and writes a value out, and how many of its blocks
// ran at all.
func executedWrites(profile, dir, marker string) ([]string, int, error) {
	data, err := os.ReadFile(profile)
	if err != nil {
		return nil, 0, err
	}
	sources := map[string][]string{}
	found := map[string]bool{}
	executed := 0
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		// file:startLine.startCol,endLine.endCol statements count
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[2] == "0" {
			continue
		}
		file, span, ok := strings.Cut(fields[0], ":")
		if !ok || !strings.Contains(file, marker) {
			continue
		}
		executed++
		base := filepath.Base(file)
		src, ok := sources[base]
		if !ok {
			b, err := os.ReadFile(filepath.Join(dir, base))
			if err != nil {
				return nil, 0, err
			}
			src = strings.Split(string(b), "\n")
			sources[base] = src
		}
		from, to, _ := strings.Cut(span, ",")
		start, _ := strconv.Atoi(strings.SplitN(from, ".", 2)[0])
		end, _ := strconv.Atoi(strings.SplitN(to, ".", 2)[0])
		if start < 1 || end > len(src) || start > end {
			return nil, 0, fmt.Errorf("%s: block %s is outside the file", base, span)
		}
		for i := start; i <= end; i++ {
			if encodingCall.MatchString(src[i-1]) {
				found[fmt.Sprintf("%s:%d: %s", base, i, strings.TrimSpace(src[i-1]))] = true
			}
		}
	}
	var out []string
	for k := range found {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, executed, nil
}

// validateCoverageDriver decodes every BOM, clears the coverage counters, and
// validates every BOM, so that the counters it writes hold what Validate ran and
// nothing else.
const validateCoverageDriver = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/coverage"

	"ex.test/cdx/cdx"
)

func main() {
	var boms []cdx.Bom
	for _, path := range os.Args[2:] {
		in, err := os.ReadFile(path)
		if err != nil {
			panic(err)
		}
		var bom cdx.Bom
		if err := json.Unmarshal(in, &bom); err != nil {
			fmt.Printf("%s: does not decode: %v\n", path, err)
			os.Exit(1)
		}
		boms = append(boms, bom)
	}
	if err := coverage.ClearCounters(); err != nil {
		panic(err)
	}
	for i := range boms {
		if err := boms[i].Validate(); err != nil {
			fmt.Printf("%s: does not validate: %v\n", os.Args[2+i], err)
			os.Exit(1)
		}
	}
	if err := coverage.WriteCountersDir(os.Args[1]); err != nil {
		panic(err)
	}
	fmt.Printf("validated %d\n", len(boms))
}
`
