package schemagen

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file drive --allow-remote-refs against a server bound to
// 127.0.0.1 by httptest. Nothing here reaches the public internet.

// ---------- issue #315: a redirect is read from the URL that answered ----------

// remoteRedirectServer serves a redirect out of the root directory into /deep/,
// where the answering document sits beside a sub.json that is not the sub.json
// beside the redirect. minLength tells the two apart in the generated source and
// in what the generated Validate accepts.
func remoteRedirectServer(t *testing.T) *httptest.Server {
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
	mux.HandleFunc("/deep/target.json", serveJSON(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "Target", "type": "object",
		"properties": {"s": {"$ref": "sub.json"}}
	}`))
	mux.HandleFunc("/deep/sub.json", serveJSON(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "Sub", "type": "string", "minLength": 3
	}`))
	mux.HandleFunc("/sub.json", serveJSON(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "Sub", "type": "string", "minLength": 9
	}`))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// The emitted verdict, which is where the defect was visible: "abc" satisfies
// the /deep/sub.json the redirected document actually references, and the
// generated Validate refused it because a different file had been fetched,
// parsed and enforced in silence.
func TestRedirectedRemoteRefEnforcesTheDocumentThatAnswered(t *testing.T) {
	server := remoteRedirectServer(t)
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "root.json"), `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/root.json",
		"title": "Root", "type": "object",
		"properties": {"t": {"$ref": "`+server.URL+`/redirect.json"}}
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return []string{
				filepath.Join(src, "root.json"),
				"-o", filepath.Join(modRoot, "gen"), "-p", "gen",
				"--allow-remote-refs", "--root-name", "Root",
			}
		},
		"example.com/m/gen", "Root",
		[]docInstance{
			// minLength 3 is /deep/sub.json's; minLength 9 is the /sub.json
			// beside the redirect, and would refuse this.
			{`{"t":{"s":"abc"}}`, true, ""},
			{`{"t":{"s":"ab"}}`, false, ""},
		})
}

// One document reached under both its URLs is one document, and one Go type. The
// cache keyed by the requested URL alone parsed it twice, and the run reported
// that only as the generic same-name warning -- which reads as a schema-authoring
// problem when it is the resolver having fetched two different files for one
// reference.
func TestOneRedirectedDocumentReachedTwoWaysIsOneType(t *testing.T) {
	server := remoteRedirectServer(t)
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "two.json"), `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/two.json",
		"title": "Root", "type": "object",
		"properties": {
			"a": {"$ref": "`+server.URL+`/redirect.json"},
			"b": {"$ref": "`+server.URL+`/deep/target.json"}
		}
	}`)

	out := t.TempDir()
	stderr, err := runGenerateCapturing(t, filepath.Join(src, "two.json"),
		"-o", out, "-p", "m", "--allow-remote-refs", "--root-name", "Root")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	body, readErr := os.ReadFile(filepath.Join(out, "two.go"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	src2 := string(body)
	if strings.Contains(src2, "Sub2") {
		t.Errorf("one document reached under two URLs produced two types for its $ref target:\n%s", src2)
	}
	if strings.Contains(stderr, "claim the Go type name") {
		t.Errorf("the run warned about a duplicate type name, which is the resolver having fetched two files "+
			"for one reference reported as a schema-authoring problem (issue #315):\n%s", stderr)
	}
	// minLength 9 is the sub.json beside the redirect, which neither reference
	// names once the redirected document is based on the URL that answered it.
	if strings.Contains(src2, "minimum 9") {
		t.Errorf("the generated source enforces the sub.json beside the redirect rather than the one beside the "+
			"document that answered:\n%s", src2)
	}
}

// ---------- issue #314, over the network ----------

// --draft has to reach a document the HTTP resolver fetched for the same reason
// it has to reach one the file resolver read: it is a document of this run. A
// --allow-remote-refs run must not be the one place the caller's dialect stops
// applying.
func TestDraftOverrideReachesADocumentFetchedOverHTTP(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/t.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/schema+json")
		_, _ = w.Write([]byte(`{"title":"TDoc","type":"object","properties":{"k":{"const":"x"}}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	writeRoot := func(t *testing.T) string {
		src := t.TempDir()
		path := filepath.Join(src, "root.json")
		writeFile(t, path, `{
			"$id": "https://ex.test/root.json",
			"title": "Root", "type": "object",
			"properties": {"t": {"$ref": "`+server.URL+`/t.json"}}
		}`)
		return path
	}

	// Draft 3 has no const, so the value the const names is not special.
	t.Run("under --draft 3", func(t *testing.T) {
		path := writeRoot(t)
		generateCompileRun(t,
			func(modRoot string) []string {
				return []string{path, "-o", filepath.Join(modRoot, "gen"), "-p", "gen",
					"--allow-remote-refs", "--draft", "3", "--root-name", "Root"}
			},
			"example.com/m/gen", "Root",
			[]docInstance{{`{"t":{"k":"y"}}`, true, ""}})
	})

	// The control: with no override the fetched document states no dialect
	// either, every keyword binds, and the const is enforced.
	t.Run("with no --draft", func(t *testing.T) {
		path := writeRoot(t)
		generateCompileRun(t,
			func(modRoot string) []string {
				return []string{path, "-o", filepath.Join(modRoot, "gen"), "-p", "gen",
					"--allow-remote-refs", "--root-name", "Root"}
			},
			"example.com/m/gen", "Root",
			[]docInstance{{`{"t":{"k":"y"}}`, false, ""}, {`{"t":{"k":"x"}}`, true, ""}})
	})
}

// ---------- issue #317: four failures, four messages ----------

// Every way a remote $ref can fail produced one byte-identical diagnostic whose
// advice was to pass --allow-remote-refs, which three of the four had passed.
// Each has to say the thing that is actually true of it.
func TestEachRemoteRefFailureSaysWhatHappened(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/missing.json", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/page.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>not a schema</html>"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	dead := httptest.NewServer(http.NewServeMux())
	deadURL := dead.URL
	dead.Close()

	const adviceToPassTheFlag = "--allow-remote-refs fetches http(s) refs over the network instead"

	cases := []struct {
		name string
		ref  string
		args []string
		// want is what the message must say, and notWant what it must not.
		want    []string
		notWant []string
	}{
		{
			name:    "the flag was not passed",
			ref:     server.URL + "/missing.json",
			want:    []string{adviceToPassTheFlag, "--lenient-refs"},
			notWant: []string{"HTTPResolver", "already in effect"},
		},
		{
			name:    "the server returned 404",
			ref:     server.URL + "/missing.json",
			args:    []string{"--allow-remote-refs"},
			want:    []string{"HTTP 404", "already in effect", "--lenient-refs"},
			notWant: []string{adviceToPassTheFlag},
		},
		{
			name:    "nothing is listening",
			ref:     deadURL + "/anything.json",
			args:    []string{"--allow-remote-refs"},
			want:    []string{"connect", "already in effect", "--lenient-refs"},
			notWant: []string{adviceToPassTheFlag},
		},
		{
			name:    "the body is not JSON",
			ref:     server.URL + "/page.html",
			args:    []string{"--allow-remote-refs"},
			want:    []string{`Content-Type "text/html"`, "already in effect", "--lenient-refs"},
			notWant: []string{adviceToPassTheFlag},
		},
	}

	seen := make(map[string]string, len(cases))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := t.TempDir()
			path := filepath.Join(src, "miss.json")
			writeFile(t, path, `{
				"$schema": "https://json-schema.org/draft/2020-12/schema",
				"$id": "https://ex.test/miss.json",
				"title": "Root", "type": "object",
				"properties": {"t": {"$ref": "`+tc.ref+`"}}
			}`)

			args := append([]string{path, "-o", t.TempDir(), "-p", "m", "--root-name", "Root"}, tc.args...)
			_, err := runGenerateCapturing(t, args...)
			if err == nil {
				t.Fatal("expected generation to fail on an unresolvable $ref")
			}
			msg := err.Error()
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("message should say %q, got:\n%s", want, msg)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(msg, notWant) {
					t.Errorf("message should not say %q for this failure, got:\n%s", notWant, msg)
				}
			}
			if prev, dup := seen[msg]; dup {
				t.Errorf("this failure and %q produce a byte-identical message, which is the whole of issue "+
					"#317:\n%s", prev, msg)
			}
			seen[msg] = tc.name
		})
	}
}
