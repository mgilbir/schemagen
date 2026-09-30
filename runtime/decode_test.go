package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

var failures int

func fail(format string, args ...any) {
	failures++
	if failures <= 40 {
		fmt.Printf(format+"\n", args...)
	}
}

// selfDecoding stands for a generated type with an UnmarshalJSON of its own.
type selfDecoding struct{ got string }

func (s *selfDecoding) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '[' {
		return errors.New("selfDecoding refuses an array")
	}
	s.got = string(b)
	return nil
}

type named string

// seeds are documents chosen for the places a reader goes wrong.
var seeds = []string{
	"{}", "[]", "null", "true", "false", "0", "-0", "1.5e-3", "1E+2", "-12345678901234567890",
	"1e400", "\"\"", "\"x\"", "\"\\u00e9\\ud83d\\ude00\"", "\"\\ud800\"", "\"\xff\xfe\"",
	" { \"a\" : [ 1 , { \"b\" : \"}]\\\"\" } , true ] , \"c\\u0041\" : null } ",
	"{\"a\":1,\"a\":2}", "{\"k\\\\\":\"v\\\\\\\"\"}", "{\"\xff\":1}", "[[[]],{\"\":{}}]",
	"01", "[1,]", "{\"a\"}", "{\"a\":}", "\"\\u12\"", "tru", "nul", "[1 2]", "{\"a\":1 \"b\":2}",
	"\"\x01\"", "-", "1.", "1e", ".5", "+1", "\"\\/\"", "\"\\x\"", "[", "]", "{,}", "[,1]", " ",
	"{\"a\":1,}", "", "\t\n\r 7 \r\n", "\"2020-01-02T03:04:05Z\"", "\"1.2.3.4\"", "\"::1\"",
	"{\"a\":{\"b\":[1,{\"c\":null}]}}", "[\"x\",1,true,null,{},[]]",
}

// value builds a random well-formed document.
func value(r *rand.Rand, depth int) string {
	pick := r.IntN(12)
	if depth > 4 {
		pick = 3 + r.IntN(9)
	}
	switch pick {
	case 0, 1:
		var b strings.Builder
		b.WriteString("{")
		for i := r.IntN(4); i > 0; i-- {
			if b.Len() > 1 {
				b.WriteString(",")
			}
			b.WriteString(key(r) + ws(r) + ":" + ws(r) + value(r, depth+1))
		}
		return b.String() + "}"
	case 2:
		var parts []string
		for i := r.IntN(4); i > 0; i-- {
			parts = append(parts, ws(r)+value(r, depth+1)+ws(r))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case 3, 4:
		return key(r)
	case 5:
		return []string{"0", "-1", "7", "1.5", "-2.5e3", "1e2", "9223372036854775807", "9223372036854775808", "1e400", "0.000001"}[r.IntN(10)]
	case 6:
		return []string{"true", "false"}[r.IntN(2)]
	case 7:
		return "null"
	default:
		return []string{"\"2020-01-02T03:04:05Z\"", "\"1.2.3.4\"", "\"x\"", "\"\"", "\"\\u00e9\""}[r.IntN(5)]
	}
}

func key(r *rand.Rand) string {
	return []string{"\"a\"", "\"b\"", "\"A\"", "\"\\u0061\"", "\"\xff\"", "\"k\\\"q\"", "\"\"", "\"é\""}[r.IntN(8)]
}

func ws(r *rand.Rand) string { return []string{"", "", " ", "\n\t"}[r.IntN(4)] }

// damage is what mutate writes into a document.
const damage = "{}[],:\"\\ x0-e."

// mutate damages a document the way a truncated or corrupted one is damaged.
func mutate(r *rand.Rand, s string) string {
	b := []byte(s)
	switch r.IntN(4) {
	case 0:
		if len(b) > 0 {
			b = b[:r.IntN(len(b))]
		}
	case 1:
		if len(b) > 0 {
			b[r.IntN(len(b))] = damage[r.IntN(len(damage))]
		}
	case 2:
		i := r.IntN(len(b) + 1)
		b = append(b[:i], append([]byte{damage[r.IntN(len(damage))]}, b[i:]...)...)
	case 3:
		if len(b) > 1 {
			i := r.IntN(len(b) - 1)
			b = append(b[:i], b[i+1:]...)
		}
	}
	return string(b)
}

func compact(b []byte) string {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return "BAD " + err.Error()
	}
	out, _ := json.Marshal(v)
	return string(out)
}

// walk compares every container under sp with encoding/json's reading of it.
func walk(d *jsonDoc, sp jsonSpan, doc string) {
	b := d.raw(sp)
	switch d.data[sp.start] {
	case '{':
		var want map[string]json.RawMessage
		if err := json.Unmarshal(b, &want); err != nil {
			fail("%q: encoding/json refuses the object %q it indexed: %v", doc, b, err)
			return
		}
		got := map[string]jsonSpan{}
		it := d.iter(sp)
		for {
			k, v, ok := it.member()
			if !ok {
				break
			}
			got[k] = v
		}
		if len(got) != len(want) {
			fail("%q: %d members, encoding/json reads %d", doc, len(got), len(want))
			return
		}
		for k, v := range want {
			g, ok := got[k]
			if !ok || compact(d.raw(g)) != compact(v) {
				fail("%q: member %q is %q, encoding/json reads %q", doc, k, d.raw(g), v)
				return
			}
			walk(d, g, doc)
		}
	case '[':
		var want []json.RawMessage
		if err := json.Unmarshal(b, &want); err != nil {
			fail("%q: encoding/json refuses the array %q: %v", doc, b, err)
			return
		}
		it := d.iter(sp)
		for i := 0; ; i++ {
			v, ok := it.elem()
			if !ok {
				if i != len(want) {
					fail("%q: %d elements, encoding/json reads %d", doc, i, len(want))
				}
				return
			}
			if i >= len(want) || compact(d.raw(v)) != compact(want[i]) {
				fail("%q: element %d is %q", doc, i, d.raw(v))
				return
			}
			walk(d, v, doc)
		}
	}
}

func leaf[T any](d *jsonDoc, sp jsonSpan, doc string) {
	var got, want T
	gerr := AtJSON(&got, d, sp)
	werr := json.Unmarshal(d.raw(sp), &want)
	if (gerr == nil) != (werr == nil) || (gerr != nil && gerr.Error() != werr.Error()) {
		fail("%q into %T: %v, encoding/json %v", doc, got, gerr, werr)
		return
	}
	if !reflect.DeepEqual(got, want) {
		fail("%q into %T: %#v, encoding/json %#v", doc, got, got, want)
	}
}

func checkDoc(doc string, deep bool) {
	data := []byte(doc)
	d, sp, err := jsonOpenDoc(data)
	valid := json.Valid(data)
	if (err == nil) != valid {
		fail("%q: jsonOpenDoc err=%v, json.Valid=%v", doc, err, valid)
		return
	}
	if err != nil {
		var v json.RawMessage
		if want := json.Unmarshal(data, &v); want == nil || want.Error() != err.Error() {
			fail("%q: refused with %v, encoding/json says %v", doc, err, want)
		}
		return
	}
	// The check indexes into the storage newJSONDoc gives it, as every decode
	// does; the indexer into plain slices of its own. So the index a document
	// gets does not depend on how many containers fit in that storage.
	checked := newJSONDoc(data)
	var indexed jsonDoc
	indexed.data = data
	checked.scan()
	indexed.index()
	if !slices.Equal(checked.starts, indexed.starts) || !slices.Equal(checked.ends, indexed.ends) {
		fail("%q: the check indexes %v %v, the indexer %v %v", doc, checked.starts, checked.ends, indexed.starts, indexed.ends)
	}
	if deep {
		// A document at encoding/json's nesting limit is checked for the
		// verdict and the index only: comparing every level's members with
		// encoding/json's reading of that level costs the depth squared.
		return
	}
	if compact(d.raw(sp)) != compact(data) {
		fail("%q: the value spans %q", doc, d.raw(sp))
	}
	walk(d, sp, doc)
	leaf[string](d, sp, doc)
	leaf[*string](d, sp, doc)
	leaf[bool](d, sp, doc)
	leaf[*bool](d, sp, doc)
	leaf[float64](d, sp, doc)
	leaf[*float64](d, sp, doc)
	leaf[int64](d, sp, doc)
	leaf[*int64](d, sp, doc)
	leaf[json.Number](d, sp, doc)
	leaf[named](d, sp, doc)
	leaf[[]string](d, sp, doc)
	leaf[map[string]int64](d, sp, doc)
	leaf[any](d, sp, doc)
	leaf[json.RawMessage](d, sp, doc)
	leaf[selfDecoding](d, sp, doc)
	leaf[*selfDecoding](d, sp, doc)
	leaf[time.Time](d, sp, doc)
	leaf[*time.Time](d, sp, doc)
	leaf[netip.Addr](d, sp, doc)
	leaf[*netip.Addr](d, sp, doc)
}

// TestDecodeHelpersAgreeWithEncodingJSON holds the in-place decode's reading of
// a document to encoding/json's, on encoding/json's own ground.
//
// The generated types no longer hand a document to encoding/json level by
// level: Doc checks it, indexes it and splits its objects and arrays into
// members itself, and reads the commonest scalars without a decoder. Each of
// those is a second implementation of something encoding/json already decides,
// and a disagreement is a document one of them accepts and the other refuses,
// or a key one of them reads differently -- invisible to every test that feeds
// only well-formed documents through. So the decoder is put to many thousands of
// documents, well formed and not, beside encoding/json:
//
//   - the check accepts exactly what json.Valid accepts, and the index it builds
//     is the one the non-checking indexer builds;
//   - an object's members and an array's elements are the ones encoding/json
//     reads, keys and all;
//   - AtJSON decodes a value into every kind of leaf -- a scalar, a pointer to
//     one, a slice, a map, an interface, a type that decodes itself and a
//     pointer to one, a type that decodes itself from text -- to the value
//     json.Unmarshal gives, and refuses it with the same words.
//
// It runs under whichever encoding/json the toolchain builds with, which is the
// point: the check has to agree with the one the caller's program links.
func TestDecodeHelpersAgreeWithEncodingJSON(t *testing.T) {
	failures = 0
	r := rand.New(rand.NewPCG(1, 2))
	docs := append([]string(nil), seeds...)
	for i := 0; i < 20000; i++ {
		v := value(r, 0)
		docs = append(docs, v, mutate(r, v))
	}
	for _, s := range seeds {
		for i := 0; i < 20; i++ {
			docs = append(docs, mutate(r, s))
		}
	}
	for _, doc := range docs {
		checkDoc(doc, false)
	}
	// Nesting at and past encoding/json's limit.
	for _, n := range []int{9999, 10000, 10001} {
		checkDoc(strings.Repeat("[", n)+strings.Repeat("]", n), true)
		checkDoc(strings.Repeat("{\"a\":", n)+"1"+strings.Repeat("}", n), true)
	}
	if failures > 0 {
		t.Errorf("%d disagreements over %d documents", failures, len(docs))
	}
	t.Logf("%d documents", len(docs))
}
