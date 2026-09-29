package schema

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestHTTPResolverReadsTheBodyNotTheHeader: the Content-Type used to gate the
// fetch, so text/plain -- what raw.githubusercontent.com serves every file as --
// was refused before its body was read.
func TestHTTPResolverReadsTheBodyNotTheHeader(t *testing.T) {
	for _, tc := range []struct {
		contentType, body string
		ok                bool
	}{
		{"text/plain; charset=utf-8", `{"type":"string"}`, true},
		{"application/octet-stream", `{"type":"string"}`, true},
		{"application/json", `{"type":"string"}`, true},
		{"text/html", `{"type":"string"}`, true},
		{"text/html; charset=utf-8", `<html>not found</html>`, false},
		{"text/plain", `not json`, false},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", tc.contentType)
			_, _ = w.Write([]byte(tc.body))
		}))
		_, err := NewHTTPResolver(WithHTTPClient(server.Client())).ResolveSchema(server.URL+"/s.json", nil)
		server.Close()
		if (err == nil) != tc.ok {
			t.Errorf("%s %q: err = %v, want ok=%v", tc.contentType, tc.body, err, tc.ok)
		}
		if err != nil && !strings.Contains(err.Error(), "parsing schema") && !strings.Contains(err.Error(), "Content-Type") {
			t.Errorf("%s: the refusal does not say why: %v", tc.contentType, err)
		}
		if err != nil && tc.contentType != "text/plain" && !strings.Contains(err.Error(), `Content-Type "`+tc.contentType+`"`) {
			t.Errorf("%s: a non-JSON body served as a non-JSON type should name the type: %v", tc.contentType, err)
		}
	}
}

// TestHTTPResolverRedirectAliasesShareOneDocument: two URLs that redirect to one
// document are one document. The cache was keyed by the final URL as well as
// the requested one, but only after a fetch, and the lookup before a fetch can
// only ask the requested one -- so the second alias parsed the document again,
// and two instances of one document are two Go types.
func TestHTTPResolverRedirectAliasesShareOneDocument(t *testing.T) {
	var fetched atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/a.json", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/doc.json", http.StatusFound) })
	mux.HandleFunc("/b.json", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/doc.json", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/doc.json", func(w http.ResponseWriter, r *http.Request) {
		fetched.Add(1)
		_, _ = w.Write([]byte(`{"$defs":{"x":{"type":"string"}}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	r := NewHTTPResolver(WithHTTPClient(server.Client()))
	a, err := r.ResolveSchema(server.URL+"/a.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.ResolveSchema(server.URL+"/b.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := r.ResolveSchema(server.URL+"/doc.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if a != b || a != direct {
		t.Errorf("one document came back as %d instances", distinct(a, b, direct))
	}
	bx, err := r.ResolveSchema(server.URL+"/b.json#/$defs/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if bx != a.Defs["x"] {
		t.Error("a fragment through the second alias reached a node of a different instance")
	}
	if n := fetched.Load(); n > 2 {
		t.Errorf("the document was requested %d times", n)
	}
}

func distinct(ss ...*Schema) int {
	seen := map[*Schema]bool{}
	for _, s := range ss {
		seen[s] = true
	}
	return len(seen)
}
