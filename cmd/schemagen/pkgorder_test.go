package schemagen

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// loadDoc loads and prepares a schema the way runMultiPackage does.
func loadDoc(t *testing.T, path, pkg string) packageDoc {
	t.Helper()
	s, err := schema.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	s.ComputeBaseURIs(nil, s)
	id := strings.TrimSuffix(s.ID, "#")
	return packageDoc{id: id, pkg: pkg, path: path, schema: s}
}

// writeDocs writes a two-document set where b.json $refs a.json.
func writeDocs(t *testing.T) (dir, aPath, bPath string) {
	t.Helper()
	dir = t.TempDir()
	aPath = filepath.Join(dir, "a.json")
	bPath = filepath.Join(dir, "b.json")
	if err := os.WriteFile(aPath, []byte(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "app://a.json",
		"title": "ADoc",
		"type": "object",
		"definitions": {
			"widget": {"type": "object", "properties": {"size": {"type": "integer"}}}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "app://b.json",
		"title": "BDoc",
		"type": "object",
		"properties": {
			"widget": {"$ref": "app://a.json#/definitions/widget"}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, aPath, bPath
}

func TestOrderPackagesByDependencies(t *testing.T) {
	_, aPath, bPath := writeDocs(t)
	docPackages := map[string]string{
		"app://a.json": "example.com/m/apkg",
		"app://b.json": "example.com/m/bpkg",
	}
	docs := []packageDoc{
		loadDoc(t, bPath, "example.com/m/bpkg"),
		loadDoc(t, aPath, "example.com/m/apkg"),
	}
	// Caller listed the dependent package first; ordering must fix that.
	got, err := orderPackagesByDependencies(
		[]string{"example.com/m/bpkg", "example.com/m/apkg"}, docs, docPackages)
	if err != nil {
		t.Fatalf("ordering: %v", err)
	}
	want := []string{"example.com/m/apkg", "example.com/m/bpkg"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v (dependency first)", got, want)
	}
}

func TestOrderPackagesDetectsCycle(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// x.json and y.json reference each other: unsatisfiable as Go packages.
	xPath := write("x.json", `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "app://x.json", "title": "XDoc", "type": "object",
		"properties": {"y": {"$ref": "app://y.json#/definitions/thing"}},
		"definitions": {"thing": {"type": "string"}}
	}`)
	yPath := write("y.json", `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "app://y.json", "title": "YDoc", "type": "object",
		"properties": {"x": {"$ref": "app://x.json#/definitions/thing"}},
		"definitions": {"thing": {"type": "string"}}
	}`)
	docPackages := map[string]string{
		"app://x.json": "example.com/m/xpkg",
		"app://y.json": "example.com/m/ypkg",
	}
	docs := []packageDoc{
		loadDoc(t, xPath, "example.com/m/xpkg"),
		loadDoc(t, yPath, "example.com/m/ypkg"),
	}
	_, err := orderPackagesByDependencies(
		[]string{"example.com/m/xpkg", "example.com/m/ypkg"}, docs, docPackages)
	if err == nil {
		t.Fatal("expected an error for mutually-referencing packages")
	}
	if !strings.Contains(err.Error(), "import cycle") {
		t.Errorf("error should explain the import cycle, got: %v", err)
	}
}

// TestMultiPackageWrongCommandLineOrder is the regression that matters: with
// the dependent schema listed first, generation must still import the owning
// package instead of silently materializing a duplicate local type.
func TestMultiPackageWrongCommandLineOrder(t *testing.T) {
	_, aPath, bPath := writeDocs(t)
	outDir := t.TempDir()

	// b.json first — the order that previously produced a local copy.
	err := runGenerateArgs(t, bPath, aPath,
		"-o", outDir,
		"--schema-package", "app://a.json=example.com/m/apkg",
		"--schema-package", "app://b.json=example.com/m/bpkg",
		"--root-name", "a.json=ADoc", "--root-name", "b.json=BDoc",
	)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	bOut, err := os.ReadFile(filepath.Join(outDir, "bpkg", "b.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(bOut)
	if !strings.Contains(got, "example.com/m/apkg") {
		t.Errorf("b package should import apkg, not copy the type:\n%s", got)
	}
	if strings.Contains(got, "type Widget struct") {
		t.Errorf("b package materialized a local Widget copy instead of importing it:\n%s", got)
	}
}

// TestMultiPackageRefByFileURI covers the cross-package registry's instance
// identity invariant: a $ref that names an input document by a spelling other
// than its $id (here a file:// URI) must still resolve to the already-loaded
// instance. Loading a second copy would leave the registry unable to recognize
// the node, silently materializing a local copy instead of an import.
func TestMultiPackageRefByFileURI(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.json")
	bPath := filepath.Join(dir, "b.json")
	if err := os.WriteFile(aPath, []byte(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "app://a.json",
		"title": "ADoc",
		"type": "object",
		"definitions": {
			"widget": {"type": "object", "properties": {"size": {"type": "integer"}}}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Reference a.json by file:// URI rather than by its $id.
	bSrc := `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "app://b.json",
		"title": "BDoc",
		"type": "object",
		"properties": {
			"widget": {"$ref": "file://` + aPath + `#/definitions/widget"}
		}
	}`
	if err := os.WriteFile(bPath, []byte(bSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()
	if err := runGenerateArgs(t, aPath, bPath,
		"-o", outDir,
		"--schema-package", "app://a.json=example.com/m/apkg",
		"--schema-package", "app://b.json=example.com/m/bpkg",
		"--root-name", "a.json=ADoc", "--root-name", "b.json=BDoc",
	); err != nil {
		t.Fatalf("generate: %v", err)
	}
	bOut, err := os.ReadFile(filepath.Join(outDir, "bpkg", "b.go"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(bOut); !strings.Contains(got, "example.com/m/apkg") {
		t.Errorf("b should import apkg for a file:// spelled ref, got:\n%s", got)
	}
}

// A nested $id rescopes relative refs, so resolving them against the
// containing document's own $id points at the wrong document and the
// dependency edge is lost. The base URI in effect at the ref's position must be
// used instead.
func TestOrderingUsesPerNodeBaseURI(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// b.json lives at https://ex.test/dir/, but the ref sits inside a $defs
	// entry that rescopes to https://ex.test/other/, so "widget.json" must
	// resolve to https://ex.test/other/widget.json.
	bPath := write("b.json", `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/dir/b.json",
		"title": "BDoc",
		"type": "object",
		"properties": {"w": {"$ref": "#/$defs/holder"}},
		"$defs": {
			"holder": {
				"$id": "https://ex.test/other/",
				"type": "object",
				"properties": {"inner": {"$ref": "widget.json#/definitions/widget"}}
			}
		}
	}`)
	wPath := write("widget.json", `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/other/widget.json",
		"title": "WDoc",
		"type": "object",
		"definitions": {"widget": {"type": "object", "properties": {"size": {"type": "integer"}}}}
	}`)

	docPackages := map[string]string{
		"https://ex.test/dir/b.json":        "example.com/m/bpkg",
		"https://ex.test/other/widget.json": "example.com/m/wpkg",
	}
	docs := []packageDoc{
		loadDoc(t, bPath, "example.com/m/bpkg"),
		loadDoc(t, wPath, "example.com/m/wpkg"),
	}
	got, err := orderPackagesByDependencies(
		[]string{"example.com/m/bpkg", "example.com/m/wpkg"}, docs, docPackages)
	if err != nil {
		t.Fatalf("ordering: %v", err)
	}
	want := "example.com/m/wpkg,example.com/m/bpkg"
	if strings.Join(got, ",") != want {
		t.Errorf("order = %v, want [wpkg bpkg]: the rescoped ref should still create the edge", got)
	}
}

func TestRefTargetDocumentsEdgeCases(t *testing.T) {
	mustParse := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parsing base %q: %v", raw, err)
		}
		return u
	}
	cases := []struct {
		name     string
		ref      string
		base     *url.URL
		wantSome string // a candidate that must be present ("" = expect none)
	}{
		{"fragment only", "#/definitions/x", mustParse("https://ex.test/a.json"), ""},
		{"empty ref", "", nil, ""},
		{"absolute as written", "https://ex.test/a.json#/definitions/x", nil, "https://ex.test/a.json"},
		{"relative against hierarchical base", "a.json#/definitions/x", mustParse("https://ex.test/dir/b.json"), "https://ex.test/dir/a.json"},
		{"parent relative", "../a.json", mustParse("https://ex.test/dir/b.json"), "https://ex.test/a.json"},
		{"host case normalized", "https://EX.TEST/a.json", nil, "https://ex.test/a.json"},
		{"no base, relative ref still offered verbatim", "a.json", nil, "a.json"},
		{"opaque urn base is not resolved against", "other.json", mustParse("urn:example:thing"), "other.json"},
	}
	for _, tc := range cases {
		got := refTargetDocuments(tc.ref, tc.base)
		if tc.wantSome == "" {
			if len(got) != 0 {
				t.Errorf("%s: expected no candidates, got %v", tc.name, got)
			}
			continue
		}
		found := false
		for _, c := range got {
			if c == tc.wantSome {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: candidates %v should include %q", tc.name, got, tc.wantSome)
		}
		for _, c := range got {
			if strings.HasPrefix(c, "urn:///") {
				t.Errorf("%s: produced a bogus candidate %q", tc.name, c)
			}
		}
	}
}

// writeChain writes n documents where doc i $refs doc i+1, so the only valid
// order is the reverse of the chain.
func writeChain(t *testing.T, n int) (paths []string, docPackages map[string]string) {
	t.Helper()
	dir := t.TempDir()
	docPackages = make(map[string]string, n)
	for i := 0; i < n; i++ {
		var ref string
		if i+1 < n {
			ref = fmt.Sprintf(`"next": {"$ref": "https://ex.test/d%d.json#/definitions/thing"},`, i+1)
		}
		src := fmt.Sprintf(`{
			"$schema": "http://json-schema.org/draft-07/schema#",
			"$id": "https://ex.test/d%d.json",
			"title": "D%d",
			"type": "object",
			"properties": {%s "own": {"type": "string"}},
			"definitions": {"thing": {"type": "object", "properties": {"v": {"type": "integer"}}}}
		}`, i, i, ref)
		p := filepath.Join(dir, fmt.Sprintf("d%d.json", i))
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
		docPackages[fmt.Sprintf("https://ex.test/d%d.json", i)] = fmt.Sprintf("example.com/m/p%d", i)
	}
	return paths, docPackages
}

func TestOrderingChainOfFivePackages(t *testing.T) {
	paths, docPackages := writeChain(t, 5)
	var docs []packageDoc
	var given []string
	for i, p := range paths {
		pkg := fmt.Sprintf("example.com/m/p%d", i)
		docs = append(docs, loadDoc(t, p, pkg))
		given = append(given, pkg)
	}
	got, err := orderPackagesByDependencies(given, docs, docPackages)
	if err != nil {
		t.Fatalf("ordering: %v", err)
	}
	// p0 depends on p1 depends on ... p4, so p4 must come first.
	want := "example.com/m/p4,example.com/m/p3,example.com/m/p2,example.com/m/p1,example.com/m/p0"
	if strings.Join(got, ",") != want {
		t.Errorf("order = %v, want the chain reversed", got)
	}
}

// Independent packages keep the caller's order, so output is reproducible.
func TestOrderingIsDeterministicForIndependentPackages(t *testing.T) {
	dir := t.TempDir()
	var docs []packageDoc
	var given []string
	docPackages := map[string]string{}
	for _, name := range []string{"c", "a", "b"} {
		src := fmt.Sprintf(`{
			"$schema": "http://json-schema.org/draft-07/schema#",
			"$id": "https://ex.test/%s.json", "title": "%sDoc", "type": "object",
			"properties": {"v": {"type": "string"}}
		}`, name, strings.ToUpper(name))
		p := filepath.Join(dir, name+".json")
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		pkg := "example.com/m/" + name
		docs = append(docs, loadDoc(t, p, pkg))
		given = append(given, pkg)
		docPackages["https://ex.test/"+name+".json"] = pkg
	}
	first, err := orderPackagesByDependencies(given, docs, docPackages)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(first, ",") != strings.Join(given, ",") {
		t.Errorf("independent packages should keep the given order: got %v, want %v", first, given)
	}
	for i := 0; i < 20; i++ {
		again, err := orderPackagesByDependencies(given, docs, docPackages)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(again, ",") != strings.Join(first, ",") {
			t.Fatalf("ordering is not deterministic: %v then %v", first, again)
		}
	}
}

// A ref to a document that is not part of the run creates no edge and must not
// block ordering.
func TestOrderingIgnoresRefsToUnmappedDocuments(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.json")
	if err := os.WriteFile(p, []byte(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/a.json", "title": "ADoc", "type": "object",
		"properties": {"x": {"$ref": "https://elsewhere.test/other.json#/definitions/thing"}}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	docs := []packageDoc{loadDoc(t, p, "example.com/m/apkg")}
	got, err := orderPackagesByDependencies([]string{"example.com/m/apkg"}, docs,
		map[string]string{"https://ex.test/a.json": "example.com/m/apkg"})
	if err != nil {
		t.Fatalf("a ref outside the run should not affect ordering: %v", err)
	}
	if len(got) != 1 || got[0] != "example.com/m/apkg" {
		t.Errorf("order = %v, want just apkg", got)
	}
}

// The cycle error should name the documents and refs, not just the packages.
func TestCycleErrorNamesDocumentsAndRefs(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	xPath := write("x.json", `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/x.json", "title": "XDoc", "type": "object",
		"properties": {"y": {"$ref": "https://ex.test/y.json#/definitions/thing"}},
		"definitions": {"thing": {"type": "string"}}
	}`)
	yPath := write("y.json", `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/y.json", "title": "YDoc", "type": "object",
		"properties": {"x": {"$ref": "https://ex.test/x.json#/definitions/thing"}},
		"definitions": {"thing": {"type": "string"}}
	}`)
	docPackages := map[string]string{
		"https://ex.test/x.json": "example.com/m/xpkg",
		"https://ex.test/y.json": "example.com/m/ypkg",
	}
	docs := []packageDoc{
		loadDoc(t, xPath, "example.com/m/xpkg"),
		loadDoc(t, yPath, "example.com/m/ypkg"),
	}
	_, err := orderPackagesByDependencies(
		[]string{"example.com/m/xpkg", "example.com/m/ypkg"}, docs, docPackages)
	if err == nil {
		t.Fatal("expected a cycle error")
	}
	msg := err.Error()
	for _, want := range []string{"import cycle", "https://ex.test/x.json", "https://ex.test/y.json"} {
		if !strings.Contains(msg, want) {
			t.Errorf("cycle error should mention %q, got: %v", want, msg)
		}
	}
}
