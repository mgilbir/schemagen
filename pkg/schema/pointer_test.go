package schema

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pointerDoc is a document whose definitions are told apart only by how a
// pointer to them is decoded. Each definition's title names it.
const pointerDoc = `{
	"$defs": {
		"a%25b":       {"title": "the key a%25b"},
		"a%b":         {"title": "the key a%b"},
		"a/b":         {"title": "the key a/b"},
		"a":           {"title": "a", "properties": {"b": {"title": "a then b"}}},
		"~1":          {"title": "the key ~1"},
		"/":           {"title": "the key /"},
		"sp ace":      {"title": "the key sp ace"}
	}
}`

// pointerCases are refs into pointerDoc and the title of the node each one
// names, per RFC 6901 §6: percent-decode the fragment once, split on "/",
// unescape "~1" then "~0". python-jsonschema, js-ajv and go-jsonschema under
// Bowtie agree on the separator case, the one this package used to read
// differently.
var pointerCases = []struct {
	fragment, title string
}{
	{"/$defs/a%2525b", "the key a%25b"}, // one decode: %2525 -> %25
	{"/$defs/a%25b", "the key a%b"},
	{"/$defs/a~1b", "the key a/b"},
	{"/$defs/a%7E1b", "the key a/b"},
	{"/$defs/a%2Fproperties%2Fb", "a then b"}, // %2F is a separator once decoded
	{"/$defs/a%2fproperties/b", "a then b"},
	{"/$defs/~01", "the key ~1"},
	{"/$defs/%7E01", "the key ~1"},
	{"/$defs/~1", "the key /"},
	{"/$defs/sp%20ace", "the key sp ace"},
}

// TestEveryResolverReadsAPointerTheSameWay holds the four resolvers to one
// reading of a fragment. The three that load a document split a reference
// with url.Parse, which percent-decodes the fragment, and then handed the
// decoded fragment to a LocalResolver that decoded it again -- so a pointer
// reached one definition from inside its document and another from any other
// document.
func TestEveryResolverReadsAPointerTheSameWay(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.json"), []byte(pointerDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/schema+json")
		_, _ = w.Write([]byte(pointerDoc))
	}))
	defer server.Close()

	var mapped Schema
	if err := json.Unmarshal([]byte(pointerDoc), &mapped); err != nil {
		t.Fatal(err)
	}
	mapped.Normalize()

	resolvers := map[string]func(fragment string) (*Schema, error){
		"LocalResolver": func(fragment string) (*Schema, error) {
			return NewLocalResolver(&mapped).Resolve("#" + fragment)
		},
		"MappingResolver": func(fragment string) (*Schema, error) {
			return NewMappingResolver(map[string]*Schema{"https://example.test/doc.json": &mapped}).
				ResolveSchema("https://example.test/doc.json#"+fragment, nil)
		},
		"MappingResolver, relative": func(fragment string) (*Schema, error) {
			base, _ := url.Parse("https://example.test/dir/root.json")
			return NewMappingResolver(map[string]*Schema{"https://example.test/doc.json": &mapped}).
				ResolveSchema("../doc.json#"+fragment, base)
		},
		"FileResolver": func(fragment string) (*Schema, error) {
			return NewFileResolver(dir).ResolveSchema("doc.json#"+fragment, nil)
		},
		"HTTPResolver": func(fragment string) (*Schema, error) {
			return NewHTTPResolver(WithHTTPClient(server.Client())).ResolveSchema(server.URL+"/doc.json#"+fragment, nil)
		},
	}
	for name, resolve := range resolvers {
		for _, tc := range pointerCases {
			got, err := resolve(tc.fragment)
			if err != nil {
				t.Errorf("%s: #%s: %v", name, tc.fragment, err)
				continue
			}
			if got.Title != tc.title {
				t.Errorf("%s: #%s reached %q, want %q", name, tc.fragment, got.Title, tc.title)
			}
		}
	}
}

// TestFragmentPointerIsTheOneDecoder pins the decoder itself, and the
// canonical spelling it pairs with.
func TestFragmentPointerIsTheOneDecoder(t *testing.T) {
	for _, tc := range []struct {
		fragment  string
		tokens    []string
		isPointer bool
	}{
		{"", nil, true},
		{"/", []string{""}, true},
		{"/a/b", []string{"a", "b"}, true},
		{"/a%2Fb", []string{"a", "b"}, true},
		{"%2Fa", []string{"a"}, true},
		{"/a~1b", []string{"a/b"}, true},
		{"/~01", []string{"~1"}, true},
		{"/%7E01", []string{"~1"}, true},
		{"/%2525", []string{"%25"}, true},
		{"/sp%20ace/%C3%A9", []string{"sp ace", "é"}, true},
		{"anchor", nil, false},
		{"an%63hor", nil, false},
	} {
		tokens, isPointer, err := FragmentPointer(tc.fragment)
		if err != nil {
			t.Errorf("%q: %v", tc.fragment, err)
			continue
		}
		if isPointer != tc.isPointer || strings.Join(tokens, "\x00") != strings.Join(tc.tokens, "\x00") || len(tokens) != len(tc.tokens) {
			t.Errorf("FragmentPointer(%q) = %q, %v; want %q, %v", tc.fragment, tokens, isPointer, tc.tokens, tc.isPointer)
		}
		if isPointer {
			// The canonical spelling reads back as the same tokens.
			back, _, err := FragmentPointer(PointerFragment(tokens...)[1:])
			if err != nil || strings.Join(back, "\x00") != strings.Join(tokens, "\x00") {
				t.Errorf("PointerFragment(%q) = %q does not read back", tokens, PointerFragment(tokens...))
			}
		}
	}
	if _, _, err := FragmentPointer("/100%"); err == nil {
		t.Error("a malformed percent-escape was accepted; url.Parse refuses the same fragment on a reference to another document")
	}
}

// TestExtensionTargetsAreMemoizedByTheirWholePointer: a vendor keyword's
// subschemas are memoized per pointer, and the key joined the tokens on "/",
// which a token may itself contain -- so "#/x-v/0/a~1b" and "#/x-v/0/a/b"
// shared a cache entry and the second ref got the first one's node.
func TestExtensionTargetsAreMemoizedByTheirWholePointer(t *testing.T) {
	var s Schema
	if err := json.Unmarshal([]byte(`{"x-v":[{"a/b":{"title":"the key a/b"},"a":{"b":{"title":"a then b"}}}]}`), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	r := NewLocalResolver(&s)
	for _, order := range [][]string{{"#/x-v/0/a~1b", "#/x-v/0/a/b"}, {"#/x-v/0/a/b", "#/x-v/0/a~1b"}} {
		s.extensionSchemas = nil
		r.cache = map[string]*Schema{}
		for _, ref := range order {
			got, err := r.Resolve(ref)
			if err != nil {
				t.Fatalf("%s: %v", ref, err)
			}
			want := map[string]string{"#/x-v/0/a~1b": "the key a/b", "#/x-v/0/a/b": "a then b"}[ref]
			if got.Title != want {
				t.Errorf("%v: %s reached %q, want %q", order, ref, got.Title, want)
			}
		}
	}
}
