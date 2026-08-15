package schema

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file are about what a resolver decides a document it just
// loaded *is*: the dialect it is read under (issue #314) and the URI it is based
// on (issue #315).

// ---------- issue #314: --draft reaches a document reached by $ref ----------

// A document a $ref pulls in is a document of the run, and the run's draft is
// what it is read under. Both resolvers, because --allow-remote-refs must not
// give the same command line a second answer.
//
// The keyword is `const`, which draft 3 does not define: reading the same
// document under DraftUnknown keeps it, which is exactly the reading a resolver
// that ignored the run's draft gave every document it fetched.
func TestResolversReadAReachedDocumentUnderTheRunsDraft(t *testing.T) {
	const doc = `{"$id":"https://ex.test/t.json","type":"object","properties":{"k":{"const":"x"}}}`

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "t.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(doc))
	}))
	defer server.Close()

	load := map[string]func(d Draft) (*Schema, error){
		"FileResolver": func(d Draft) (*Schema, error) {
			return NewFileResolver(dir, WithFileResolverDraft(d)).ResolveSchema("t.json", nil)
		},
		"HTTPResolver": func(d Draft) (*Schema, error) {
			r := NewHTTPResolver(WithHTTPClient(server.Client()), WithHTTPResolverDraft(d))
			return r.ResolveSchema(server.URL+"/t.json", nil)
		},
	}
	for name, loadWith := range load {
		t.Run(name, func(t *testing.T) {
			under3, err := loadWith(Draft03)
			if err != nil {
				t.Fatalf("resolve under draft 3: %v", err)
			}
			if under3.Properties["k"].Const != nil {
				t.Errorf("const survived in a document reached by $ref under --draft 3. Draft 3 has no const, "+
					"so the caller's dialect was not the one this document was read under and the run enforces "+
					"two dialects at once (issue #314). const = %v", *under3.Properties["k"].Const)
			}

			// The control, and the reason the assertion above is about the
			// dialect and not about the gate having been switched on for
			// everyone: with no --draft the document states none either, and
			// every keyword binds.
			byDoc, err := loadWith(DraftUnknown)
			if err != nil {
				t.Fatalf("resolve with no draft override: %v", err)
			}
			if byDoc.Properties["k"].Const == nil {
				t.Errorf("const was dropped from a document with no dialect and no override; DraftUnknown means " +
					"'read the dialect from the document', not 'this document has none'")
			}
		})
	}
}

// The documented exception, in the position that is easiest to take too far: a
// reached document that declares a $schema of its own keeps that dialect. It is
// what preserves cross-draft $ref semantics, it is the README's one exception to
// --draft, and it is the answer the generator's draftForSchema gives for the
// same node -- so normalization giving a different one would read one node under
// two dialects.
func TestAReachedDocumentDeclaringItsOwnSchemaKeepsThatDialect(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "t.json"), []byte(
		`{"$schema":"http://json-schema.org/draft-07/schema#","$id":"https://ex.test/t.json","const":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := NewFileResolver(dir, WithFileResolverDraft(Draft03)).ResolveSchema("t.json", nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if s.Const == nil {
		t.Error("the reached document declared draft 7 and lost its const to --draft 3. A resource reached by " +
			"$ref keeps a $schema of its own -- that is what preserves cross-draft $ref semantics, and it is the " +
			"answer draftForSchema gives the same node")
	}
}

// The other half of the same rule, held where a fix for one could break the
// other: --draft supplies the *root's* dialect, so a resource embedded in the
// reached document under its own $schema keeps that $schema for its subtree.
func TestAReachedDocumentsEmbeddedResourceKeepsItsOwnDialect(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "t.json"), []byte(`{
		"$id": "https://ex.test/t.json",
		"const": "root",
		"$defs": {"kept": {"$schema": "https://json-schema.org/draft/2020-12/schema", "const": "embedded"}}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := NewFileResolver(dir, WithFileResolverDraft(Draft03)).ResolveSchema("t.json", nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if s.Const != nil {
		t.Errorf("the reached document's own root kept its const under --draft 3; the override stands in for the "+
			"$schema this document does not state. const = %v", *s.Const)
	}
	if kept := s.Defs["kept"]; kept.Const == nil {
		t.Error("the embedded 2020-12 resource inside the reached document lost its const; --draft supplies the " +
			"dialect of a document root, and an embedded resource declaring its own $schema is not one")
	}
}

// ---------- issue #315: a redirect is based on the URL that answered ----------

// redirectServer serves /redirect.json as a 302 into /deep/, where the answering
// document lives beside a sub.json that is *not* the sub.json beside the
// redirect. Which of the two a relative $ref reaches is the whole question.
func redirectServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/redirect.json", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/deep/target.json", http.StatusFound)
	})
	serveJSON := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/schema+json")
			_, _ = w.Write([]byte(body))
		}
	}
	mux.HandleFunc("/deep/target.json", serveJSON(
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"s":{"$ref":"sub.json"}}}`))
	mux.HandleFunc("/deep/sub.json", serveJSON(
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"string","minLength":3}`))
	mux.HandleFunc("/sub.json", serveJSON(
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"string","minLength":9}`))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// The base URI of a document that declares no $id is the URI it was retrieved
// from (RFC 3986 §5.1.3, deferred to by draft 2020-12 §9.1.1), and over HTTP
// that is the effective request URI after redirects. Keyed by the URL that was
// asked for, a relative $ref inside the answer read the directory the request
// started in and silently fetched, parsed and enforced a different document.
func TestHTTPResolverBasesARedirectedDocumentOnTheURLThatAnswered(t *testing.T) {
	server := redirectServer(t)
	r := NewHTTPResolver(WithHTTPClient(server.Client()))

	s, err := r.ResolveSchema(server.URL+"/redirect.json", nil)
	if err != nil {
		t.Fatalf("resolve through the redirect: %v", err)
	}
	if s.RetrievalURI == nil {
		t.Fatal("the fetched document records no retrieval URI, so nothing downstream can tell which URL answered")
	}
	if got, want := s.RetrievalURI.String(), server.URL+"/deep/target.json"; got != want {
		t.Errorf("retrieval URI = %q, want %q -- the URL that answered, not the one that was asked for", got, want)
	}
}

// The consequence, end to end within this package: the relative $ref inside the
// redirected document has to reach /deep/sub.json (minLength 3) and not the
// /sub.json beside the redirect (minLength 9).
func TestARelativeRefInsideARedirectedDocumentReadsTheAnsweringDirectory(t *testing.T) {
	server := redirectServer(t)
	r := NewHTTPResolver(WithHTTPClient(server.Client()))

	doc, err := r.ResolveSchema(server.URL+"/redirect.json", nil)
	if err != nil {
		t.Fatalf("resolve through the redirect: %v", err)
	}
	// The base a caller computes for this document, exactly as the generator
	// does: the URI the $ref named, unless the document says which URI answered.
	// Written this way round on purpose -- with the retrieval URI unrecorded this
	// is the requested URL, which is the defect rather than a broken fixture.
	base, err := url.Parse(server.URL + "/redirect.json")
	if err != nil {
		t.Fatal(err)
	}
	if doc.RetrievalURI != nil {
		base = doc.RetrievalURI
	}
	doc.ComputeBaseURIs(base, doc)

	sub, err := r.ResolveSchema("sub.json", doc.Properties["s"].BaseURI)
	if err != nil {
		t.Fatalf("resolve the relative $ref inside the redirected document: %v", err)
	}
	if sub.MinLength == nil {
		t.Fatal("the relative $ref resolved to a document with no minLength; neither fixture looks like this")
	}
	if *sub.MinLength != 3 {
		t.Errorf("minLength = %d, want 3: the $ref was read against %q, which is the directory the request started "+
			"in rather than the one that answered it, so a different document was fetched and enforced (issue #315)",
			*sub.MinLength, doc.Properties["s"].BaseURI)
	}
}

// One document reached under both URLs is one document. Keyed by the requested
// URL alone it was parsed twice, and two instances of one document become two Go
// types -- reported, if at all, as a same-name warning that reads like a
// schema-authoring problem.
func TestHTTPResolverCachesARedirectedDocumentUnderBothURLs(t *testing.T) {
	server := redirectServer(t)
	r := NewHTTPResolver(WithHTTPClient(server.Client()))

	viaRedirect, err := r.ResolveSchema(server.URL+"/redirect.json", nil)
	if err != nil {
		t.Fatalf("resolve through the redirect: %v", err)
	}
	direct, err := r.ResolveSchema(server.URL+"/deep/target.json", nil)
	if err != nil {
		t.Fatalf("resolve directly: %v", err)
	}
	if viaRedirect != direct {
		t.Error("the same document reached under two URLs came back as two instances, which downstream is two " +
			"Go types for one schema (issue #315)")
	}
}

// A document that states its own $id is not affected: the $id is authoritative
// and outranks any base a fetch establishes.
func TestADocumentsOwnIDOutranksTheURLThatAnswered(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/r/doc.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/schema+json")
		_, _ = w.Write([]byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/elsewhere/other.json","type":"object"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	r := NewHTTPResolver(WithHTTPClient(server.Client()))
	s, err := r.ResolveSchema(server.URL+"/r/doc.json", nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	s.ComputeBaseURIs(s.RetrievalURI, s)
	if got, want := s.BaseURI.String(), "https://ex.test/elsewhere/other.json"; got != want {
		t.Errorf("base URI = %q, want %q: a document's own $id decides its base, whatever it was fetched from", got, want)
	}
}

// ---------- issue #317: each way a fetch fails says which one it was ----------

// The four ways a remote $ref fails produced one byte-identical diagnostic. The
// three that reached the network are told from the one that did not by the error
// type, so the advice can stop naming a flag that is already on.
func TestEachRemoteFetchFailureIsItsOwnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/missing.json", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/page.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>not a schema</html>"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	// A port with nothing listening. The listener is opened and closed so the
	// address is one this machine really has nobody on.
	dead := httptest.NewServer(http.NewServeMux())
	deadURL := dead.URL
	dead.Close()

	for _, tc := range []struct {
		name string
		url  string
		want string
	}{
		{"a 404", server.URL + "/missing.json", "HTTP 404"},
		{"a non-JSON body", server.URL + "/page.html", `Content-Type "text/html"`},
		{"nothing listening", deadURL + "/anything.json", "connect"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewHTTPResolver(WithHTTPClient(server.Client()))
			_, err := r.ResolveSchema(tc.url, nil)
			if err == nil {
				t.Fatal("expected the fetch to fail")
			}
			var fetch *RemoteFetchError
			if !errors.As(err, &fetch) {
				t.Fatalf("error is not a RemoteFetchError, so nothing can tell that the network was reached "+
					"and the --allow-remote-refs advice applies to it: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should say %q, got: %v", tc.want, err)
			}
			if !strings.Contains(err.Error(), tc.url) {
				t.Errorf("error should name the URL it fetched, got: %v", err)
			}
		})
	}
}

// A ref that names no http(s) document never reaches the HTTP resolver, and the
// resolver that a --allow-remote-refs run does not configure cannot report a
// fetch either. Both are the case the flag is the answer to, and neither may
// look like a fetch that failed.
func TestARefThatWasNeverFetchedIsNotAFetchFailure(t *testing.T) {
	chain := NewCompositeResolver(
		NewMappingResolver(map[string]*Schema{}),
		NewFileResolver(t.TempDir()),
	)
	_, err := chain.ResolveSchema("https://ex.test/nowhere.json", nil)
	if err == nil {
		t.Fatal("expected resolution to fail")
	}
	var fetch *RemoteFetchError
	if errors.As(err, &fetch) {
		t.Errorf("a chain with no HTTP resolver reported a fetch failure: %v", err)
	}
	var chainErr *ResolveError
	if !errors.As(err, &chainErr) {
		t.Fatalf("the chain's answers were not carried in a ResolveError, so a caller can only have the joined "+
			"string: %v", err)
	}
	if len(chainErr.Errs) != 2 {
		t.Errorf("the chain has 2 resolvers and carried %d answers; the caller has to be able to see each one",
			len(chainErr.Errs))
	}
}

// The composite's joined rendering is what a caller that only prints the error
// sees, and it did not change when the type did.
func TestResolveErrorStillReadsAsTheJoinedChain(t *testing.T) {
	chain := NewCompositeResolver(NewFileResolver(t.TempDir()), NewHTTPResolver())
	_, err := chain.ResolveSchema("nope.json", &url.URL{Scheme: "file", Path: "/base/x.json"})
	if err == nil {
		t.Fatal("expected resolution to fail")
	}
	msg := err.Error()
	for _, want := range []string{`resolving "nope.json"`, "FileResolver", "HTTPResolver"} {
		if !strings.Contains(msg, want) {
			t.Errorf("joined message should contain %q, got: %s", want, msg)
		}
	}
}
