package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/emitter"
)

// A decoded value is the document just decoded, and nothing else: not what an
// earlier document left in the same value, not the bytes of a buffer the caller
// goes on to reuse, and not something a caller can rewrite through what an
// accessor hands out.
//
// All three were false:
//
//   - Decoding into a value the caller had decoded into before merged the two
//     documents. The members the second document left out kept the first
//     document's values -- while the record of which keys were present was
//     reset -- so {"a":"x","extra":1} and then {} into one value validated as
//     "a: required property is missing" and marshalled as {"a":"x"}. Every
//     generated UnmarshalJSON now starts from the zero value: decoding is
//     replacing, as it is for a protobuf message, where encoding/json's own
//     decode merges. This is the deliberate difference from encoding/json.
//   - A heterogeneous enum kept the decoder's own buffer as its value, which
//     encoding/json's contract for an Unmarshaler forbids; a json.Decoder over a
//     stream reuses that buffer for every document, and 133 of 400 decoded
//     values changed under a chunked reader. The raw-JSON wrappers appended the
//     bytes over the array they already held, so a copy of the value made
//     before a decode was rewritten by it; and their MarshalJSON and Raw
//     returned that array itself.
//
// The corpus is every schema under testdata/schemas the generator accepts, and
// the documents are built for each schema out of what it names: its property
// names, the values its enums, consts, examples and defaults spell, and scalars
// of every kind, nested a few levels. Most such documents are refused, which is
// part of the point -- a refused decode has to leave a value in the same state
// whatever the value held before.
func TestDecodedValueIsExactlyTheDocument(t *testing.T) {
	if testing.Short() {
		t.Skip("generates, compiles and drives the whole corpus")
	}
	t.Parallel()
	schemas := corpusSchemaPaths(t)
	sort.Strings(schemas)
	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := writeTestGoMod(dir, "ownership_test"); err != nil {
		t.Fatal(err)
	}
	var imports, entries strings.Builder
	packages := 0
	for i, path := range schemas {
		src, helpers, err := generateForCompile(em, path)
		if err != nil {
			continue // TestGeneratedCorpusCompiles pins which ones are refused
		}
		root := extractRootTypeNameFromCode(string(src))
		if root == "" {
			t.Fatalf("%s: no root type in the generated source", path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		docs := ownershipDocuments(raw, uint64(i))
		name := fmt.Sprintf("p%04d", i)
		sub := filepath.Join(dir, name)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "types.go"), src, 0o644); err != nil {
			t.Fatal(err)
		}
		if len(helpers) > 0 {
			if err := os.WriteFile(filepath.Join(sub, "helpers.go"), helpers, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		docsJSON, err := json.Marshal(docs)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "docs.json"), docsJSON, 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&imports, "\t%s \"ownership_test/%s\"\n", name, name)
		fmt.Fprintf(&entries, "\t{name: %q, schema: %q, mk: func() any { return new(%s.%s) }},\n", name, path, name, root)
		packages++
	}
	if packages < 500 {
		t.Fatalf("only %d corpus schemas generated; the corpus is measured in the hundreds", packages)
	}
	drv := filepath.Join(dir, "driver")
	if err := os.MkdirAll(drv, 0o755); err != nil {
		t.Fatal(err)
	}
	main := strings.NewReplacer("@IMPORTS@", imports.String(), "@ENTRIES@", entries.String()).Replace(ownershipDriver)
	if err := os.WriteFile(filepath.Join(drv, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	output, err := testgo.Command(ctx, dir, "run", "./driver", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("driver: %v\n%s", err, output)
	}
	out := programOutput(output)
	var kept []string
	crashed := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "CRASH "); ok {
			path, _, _ := strings.Cut(rest, ": ")
			path = filepath.ToSlash(strings.TrimPrefix(path, fuzzSchemaDir+"/"))
			crashed[path] = true
			if _, pinned := ownershipKnownCrashes[path]; pinned {
				continue
			}
		}
		kept = append(kept, line)
	}
	for path, why := range ownershipKnownCrashes {
		if !crashed[path] {
			t.Errorf("%s no longer crashes (%s); take it out of ownershipKnownCrashes so the checks run over it", path, why)
		}
	}
	last := kept[len(kept)-1]
	t.Log(last)
	if !strings.HasPrefix(last, "PASS ") || len(kept) > 1 {
		t.Errorf("decoded values that were not exactly their documents:\n%s", strings.Join(kept, "\n"))
	}
}

// ownershipKnownCrashes are the corpus schemas whose generated code crashes
// the process on the documents this test builds, for reasons that are not
// about decoding into a value -- each is the same crash on the code this
// change started from. A crash is fatal and ends the schema's checks, so these
// are pinned rather than counted as passes, and one that stops crashing fails
// the test until it is taken out of the list and checked like the rest.
//
// None is left: the type-schema $ref cycles no longer recurse (see
// TestTypeSchemaCyclesAreJudgedWithoutLooping), and a pattern that is not a
// regular expression is no longer compiled with MustCompile.
var ownershipKnownCrashes = map[string]string{}

// ownershipDocuments builds the documents one schema is driven with. The same
// seed builds the same documents, so a failure reproduces by name.
func ownershipDocuments(schemaJSON []byte, seed uint64) []json.RawMessage {
	var s any
	_ = json.Unmarshal(schemaJSON, &s)
	v := &ownershipVocabulary{rng: rand.New(rand.NewPCG(seed, 0x5eed))}
	v.collect(s)
	sort.Strings(v.keys)
	var docs []json.RawMessage
	for _, value := range v.values {
		docs = append(docs, value)
	}
	for len(docs) < 24 {
		b, err := json.Marshal(v.value(0))
		if err != nil {
			panic(err)
		}
		docs = append(docs, b)
	}
	return docs
}

type ownershipVocabulary struct {
	rng    *rand.Rand
	keys   []string
	values []json.RawMessage
}

// collect gathers the property names a schema declares and the values it
// spells out, at any depth.
func (v *ownershipVocabulary) collect(s any) {
	switch n := s.(type) {
	case map[string]any:
		keys := make([]string, 0, len(n))
		for k := range n {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			switch k {
			case "properties", "dependentRequired", "dependentSchemas":
				if props, ok := n[k].(map[string]any); ok {
					for name := range props {
						v.keys = append(v.keys, name)
					}
				}
			case "enum", "examples":
				if list, ok := n[k].([]any); ok {
					for _, e := range list {
						if b, err := json.Marshal(e); err == nil {
							v.values = append(v.values, b)
						}
					}
				}
			case "const", "default":
				if b, err := json.Marshal(n[k]); err == nil {
					v.values = append(v.values, b)
				}
			}
			v.collect(n[k])
		}
	case []any:
		for _, e := range n {
			v.collect(e)
		}
	}
}

// value builds a random JSON value out of the vocabulary.
func (v *ownershipVocabulary) value(depth int) any {
	r := v.rng
	pick := r.IntN(10)
	if depth > 3 {
		pick = 4 + r.IntN(6)
	}
	switch {
	case pick < 3:
		obj := map[string]any{}
		n := r.IntN(5)
		for i := 0; i < n; i++ {
			key := "zz"
			if len(v.keys) > 0 && r.IntN(5) > 0 {
				key = v.keys[r.IntN(len(v.keys))]
				if r.IntN(8) == 0 {
					key = strings.ToUpper(key)
				}
			}
			obj[key] = v.value(depth + 1)
		}
		return obj
	case pick < 4:
		arr := []any{}
		for i := r.IntN(4); i > 0; i-- {
			arr = append(arr, v.value(depth+1))
		}
		return arr
	case pick < 5 && len(v.values) > 0:
		return json.RawMessage(v.values[r.IntN(len(v.values))])
	case pick < 6:
		return []string{"", "x", "abc", "2020-01-02T03:04:05Z", "1.2.3.4", "a@b.c"}[r.IntN(6)]
	case pick < 7:
		return []json.Number{"0", "1", "-7", "1.5", "1e2", "12345678901234567890"}[r.IntN(6)]
	case pick < 8:
		return r.IntN(2) == 0
	case pick < 9:
		return nil
	default:
		if len(v.keys) > 0 {
			return v.keys[r.IntN(len(v.keys))]
		}
		return "k"
	}
}

// ownershipDriver runs every property over every schema's documents.
const ownershipDriver = `package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

@IMPORTS@
)

type entry struct {
	name, schema string
	mk           func() any
}

var entries = []entry{
@ENTRIES@
}

// snapshot is what a value says: its Validate verdict and what it marshals to.
func snapshot(v any) string {
	s := "validate="
	if x, ok := v.(interface{ Validate() error }); ok {
		if err := x.Validate(); err != nil {
			s += err.Error()
		} else {
			s += "ok"
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return s + " marshal-error=" + err.Error()
	}
	return s + " marshal=" + string(b)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// scribble overwrites b.
func scribble(b []byte) {
	for i := range b {
		b[i] = '#'
	}
}

// generated reports whether t is one of the generated types -- whose
// accessors are the ones under test. A json.RawMessage in an exported field is
// the caller's own bytes, and its MarshalJSON hands them back by design.
func generated(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return strings.HasPrefix(t.PkgPath(), "ownership_test/p")
}

// scribbleAccessors calls every Raw and MarshalJSON of a generated type
// reachable from v through exported fields, and scribbles over what each
// returns.
func scribbleAccessors(v reflect.Value, depth int) {
	if depth > 64 || !v.IsValid() {
		return
	}
	if v.CanInterface() && generated(v.Type()) && !(v.Kind() == reflect.Pointer && v.IsNil()) {
		if m, ok := v.Interface().(interface{ Raw() json.RawMessage }); ok {
			scribble(m.Raw())
		}
		if m, ok := v.Interface().(json.Marshaler); ok {
			if b, err := m.MarshalJSON(); err == nil {
				scribble(b)
			}
		}
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			scribbleAccessors(v.Elem(), depth+1)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				scribbleAccessors(v.Field(i), depth+1)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			scribbleAccessors(v.Index(i), depth+1)
		}
	case reflect.Map:
		it := v.MapRange()
		for it.Next() {
			scribbleAccessors(it.Value(), depth+1)
		}
	}
}

// chunked reads its data a few bytes at a time, which makes a json.Decoder
// refill and shift its buffer as it goes.
type chunked struct {
	data []byte
	n    int
}

func (c *chunked) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := c.n
	if n > len(p) {
		n = len(p)
	}
	if n > len(c.data) {
		n = len(c.data)
	}
	copy(p, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

// main runs each schema's checks in a process of its own, so that a schema
// whose generated code crashes -- a stack overflow is fatal, and cannot be
// recovered from -- is reported by name and does not take the rest with it.
func main() {
	if len(os.Args) > 2 {
		for _, e := range entries {
			if e.name == os.Args[2] {
				checks := check(e)
				fmt.Printf("CHECKS %d\n", checks)
			}
		}
		return
	}
	failures, checks := 0, 0
	for _, e := range entries {
		out, err := exec.Command(os.Args[0], os.Args[1], e.name).CombinedOutput()
		if err != nil {
			line := string(out)
			if i := strings.Index(line, "fatal error:"); i >= 0 {
				line = line[i:]
			}
			if i := strings.IndexByte(line, '\n'); i >= 0 {
				line = line[:i]
			}
			fmt.Printf("CRASH %s: %s\n", e.schema, line)
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if n, ok := strings.CutPrefix(line, "CHECKS "); ok {
				c := 0
				fmt.Sscan(n, &c)
				checks += c
				continue
			}
			failures++
			if failures <= 60 {
				fmt.Println(line)
			}
		}
	}
	if failures > 0 {
		fmt.Printf("FAIL %d of %d checks\n", failures, checks)
		return
	}
	fmt.Printf("PASS %d checks over %d schemas\n", checks, len(entries))
}

// check runs every property over one schema's documents, printing each
// failure, and returns how many checks it made.
func check(e entry) int {
	checks := 0
	fail := func(e entry, format string, args ...any) {
		fmt.Printf("%s (%s): %s\n", e.schema, e.name, fmt.Sprintf(format, args...))
	}
	{
		raw, err := os.ReadFile(filepath.Join(os.Args[1], e.name, "docs.json"))
		if err != nil {
			panic(err)
		}
		var docs []json.RawMessage
		if err := json.Unmarshal(raw, &docs); err != nil {
			panic(err)
		}
		_, decodesItself := e.mk().(json.Unmarshaler)
		for i, a := range docs {
			b := docs[(i+1)%len(docs)]

			// Replace: a then b into one value is b.
			checks++
			v := e.mk()
			_ = json.Unmarshal(append([]byte(nil), a...), v)
			errB := json.Unmarshal(append([]byte(nil), b...), v)
			w := e.mk()
			errFresh := json.Unmarshal(append([]byte(nil), b...), w)
			if errText(errB) != errText(errFresh) {
				fail(e, "%s then %s: %q, where %s alone gives %q", a, b, errText(errB), b, errText(errFresh))
			} else if errB == nil || decodesItself {
				// A type that decodes itself starts from the zero value either
				// way; one that leaves the decode to encoding/json -- a named
				// scalar, an interface -- is left as it was by a refused one.
				if !reflect.DeepEqual(v, w) {
					fail(e, "%s then %s holds %#v, where %s alone holds %#v", a, b, v, b, w)
				} else if errB == nil && snapshot(v) != snapshot(w) {
					fail(e, "%s then %s says %s, where %s alone says %s", a, b, snapshot(v), b, snapshot(w))
				}
			}

			// Ownership: the value does not change when the buffer it was
			// decoded from is rewritten, when a copy of it is decoded into, or
			// when what its accessors hand out is rewritten.
			checks++
			buf := append([]byte(nil), a...)
			x := e.mk()
			if json.Unmarshal(buf, x) != nil {
				continue
			}
			want := snapshot(x)
			scribble(buf)
			if got := snapshot(x); got != want {
				fail(e, "%s: rewriting the buffer it was decoded from changed the value from %s to %s", a, want, got)
			}
			// A root that is itself a pointer -- {"type":["string","null"]}
			// becomes one -- is the caller's pointer: a copy of it is a copy of
			// the pointer, and encoding/json decodes through it into what it
			// points at, as it does for every Go pointer a caller holds. Held in
			// a generated struct it is replaced like every other member.
			if k := reflect.TypeOf(x).Elem().Kind(); k == reflect.Pointer || k == reflect.Interface {
				continue
			}
			y := reflect.New(reflect.TypeOf(x).Elem())
			y.Elem().Set(reflect.ValueOf(x).Elem())
			_ = json.Unmarshal(append([]byte(nil), b...), x)
			if got := snapshot(y.Interface()); got != want {
				fail(e, "%s: decoding %s into the value changed a copy of it made earlier from %s to %s", a, b, want, got)
			}
			scribbleAccessors(y, 0)
			if got := snapshot(y.Interface()); got != want {
				fail(e, "%s: rewriting what its accessors returned changed the value from %s to %s", a, want, got)
			}
		}

		// A stream: every document decoded from one json.Decoder over a reader
		// that hands it a few bytes at a time, and each value then compared with
		// the same document decoded on its own.
		var stream bytes.Buffer
		for _, d := range docs {
			stream.Write(d)
			stream.WriteByte('\n')
		}
		dec := json.NewDecoder(&chunked{data: stream.Bytes(), n: 7})
		var decoded []any
		var errs []string
		for range docs {
			v := e.mk()
			err := dec.Decode(v)
			decoded = append(decoded, v)
			errs = append(errs, errText(err))
		}
		for i, d := range docs {
			checks++
			w := e.mk()
			err := json.Unmarshal(d, w)
			if errs[i] != errText(err) {
				fail(e, "%s from a stream: %q, alone %q", d, errs[i], errText(err))
				continue
			}
			if err == nil && snapshot(decoded[i]) != snapshot(w) {
				fail(e, "%s from a stream says %s once the stream has moved on, alone %s", d, snapshot(decoded[i]), snapshot(w))
			}
		}
	}
	return checks
}
`
