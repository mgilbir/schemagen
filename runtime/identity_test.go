package runtime

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
	got, err := IdentifyAt(&v, nil)
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
	var v, vExact any
	if err := json.Unmarshal(doc, &v); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(doc)))
	dec.UseNumber()
	if err := dec.Decode(&vExact); err != nil {
		t.Fatal(err)
	}
	l := jsonLazy{d, sp, false}
	lExact := jsonLazy{d, sp, true}
	for round := 0; round < 2; round++ {
		// The second round reads what the first kept on the document -- two
		// caches, one per reading of its numbers.
		got, err := l.SchemagenJSONIdentity(nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != refID(t, v) {
			t.Errorf("round %d: a lazily read value is not what encoding/json decodes it into", round)
		}
		gotExact, err := lExact.SchemagenJSONIdentity(nil)
		if err != nil {
			t.Fatal(err)
		}
		if gotExact != refID(t, vExact) {
			t.Errorf("round %d: an exact lazily read value is not what encoding/json decodes it into with UseNumber", round)
		}
		if gotExact == got {
			t.Errorf("round %d: 12345678901234567890 read exactly and as a float64 are one identity; the document is not telling the readings apart", round)
		}
	}
	a := l.jsonLevel().(map[string]any)["a"].(jsonLazy).jsonLevel().([]any)
	ids := make([]jsonID, len(a))
	for i := range a {
		if ids[i], err = jsonIDAny(a[i], nil); err != nil {
			t.Fatal(err)
		}
	}
	if jsonFirstDuplicate(a, ids, IdentifyAt[any]) >= 0 {
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
		lazy := jsonLazy{d, sp, false}
		lazyExact := jsonLazy{d, sp, true}
		for name, v := range map[string]any{"whole": whole, "exact": exact, "lazy": lazy, "levelled": jsonTop(lazy),
			"lazy exact": lazyExact, "levelled exact": jsonTop(lazyExact)} {
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
			// Read as a float64 where the value was decoded that way, and exactly
			// where its numbers are the literals -- the value decoded with
			// UseNumber, and a lazy value read exactly, which is how the
			// evaluator reads one.
			if ok, err := jsonMatchesJSON(v, jsonConstOf(!strings.Contains(name, "exact"), doc)); err != nil || !ok {
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
	if got := jsonFirstDuplicate(s, one, IdentifyAt[any]); got != 3 {
		t.Errorf("first duplicate: got %d, want 3", got)
	}
	big := make([]any, 20)
	for i := range big {
		big[i] = float64(i)
	}
	big[19] = 3.0
	if got := jsonFirstDuplicate(big, make([]jsonID, len(big)), IdentifyAt[any]); got != 19 {
		t.Errorf("first duplicate among colliding identities: got %d, want 19", got)
	}
}

func TestAConstIsDecidedExactly(t *testing.T) {
	// A const the value's identity collides with: the identity is the value's,
	// the literal is another. An identity may only ever decide a mismatch, so
	// this must not admit the value.
	v := map[string]any{"a": json.Number("1"), "b": []any{"x"}}
	id, err := IdentifyAt(&v, nil)
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
	if got := jsonFirstDuplicate(s, []jsonID{id, id}, IdentifyAt[any]); got != -1 {
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

// TestCollidingKeysAreReadAsEncodingJSONWritesThem: two keys of a Go map that
// are not valid UTF-8 can read as one name, each byte that is not UTF-8 read as
// U+FFFD -- as can such a key and one that spells U+FFFD itself. encoding/json
// writes every member, in the order of the keys, so a reader of what it writes
// keeps the member with the greatest key. The tree filed each member under its
// name as the map was ranged, so which of them it kept was the map's order's
// choice. Each value is read many times over, since one reading is right by
// chance as often as not.
func TestCollidingKeysAreReadAsEncodingJSONWritesThem(t *testing.T) {
	anyTwo := map[string]any{"\xff": 1.0, "\xfe": 2.0}
	anyMixed := map[string]any{"\xef\xbf\xbd": "valid", "\xff": "invalid", "a\xff": 3.0, "a\xfe": 4.0, "a\xfd": 5.0}
	strings3 := map[string]string{"k\xff": "x", "k\xfe": "y", "k\xfd": "z"}
	raws := map[string]json.RawMessage{"\xff": json.RawMessage("1"), "\xfe": json.RawMessage("[2]"), "\xef\xbf\xbd": json.RawMessage("{}")}
	// Each by its own type, which is the path its identity is read by; held as
	// an any, the same value is read again by that path.
	for i := 0; i < 64; i++ {
		check(t, anyTwo)
		check(t, anyMixed)
		check(t, strings3)
		check(t, raws)
	}
	// And by jsonIDMap, which a map the generator planned is read through: its
	// identity, and the tree it gathers member by member when reading trees.
	byMap := func(mp map[string]any) (jsonID, any, error) {
		id, err := jsonIDMap(mp, nil, IdentifyAt[any])
		if err != nil {
			return id, nil, err
		}
		tree, err := jsonTreeOf(&mp, func(p *map[string]any, m *jsonValidation) (jsonID, error) {
			return jsonIDMap(*p, m, IdentifyAt[any])
		})
		return id, tree, err
	}
	for _, mp := range []map[string]any{anyTwo, anyMixed} {
		b, _ := json.Marshal(mp)
		want, err := jsonTreeRaw(b, false)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 64; i++ {
			id, tree, err := byMap(mp)
			if err != nil {
				t.Fatalf("%#v: %v", mp, err)
			}
			if id != refID(t, mp) {
				t.Errorf("%#v: jsonIDMap's identity is not that of %s", mp, b)
				break
			}
			if !jsonTreeEqual(tree, want) {
				t.Errorf("%#v: jsonIDMap's tree %#v is not what encoding/json decodes %s into", mp, tree, b)
				break
			}
		}
	}
	values := []any{anyTwo, anyMixed, strings3, raws}
	for _, v := range values {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		want, err := jsonTreeRaw(b, false)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 64; i++ {
			got, err := jsonTreeAny(v, nil)
			if err != nil {
				t.Fatalf("%#v: %v", v, err)
			}
			if !jsonTreeEqual(got, want) {
				t.Errorf("%#v: tree %#v is not what encoding/json decodes %s into", v, got, b)
				break
			}
		}
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
