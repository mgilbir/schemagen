package schemagen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A relative $ref is read against the base URI of the document it is written
// in -- the file it was read from, unless an $id says otherwise -- in every mode
// the CLI has. --shared-types resolved one against the first input's directory,
// and --schema-package against whichever input directory answered first, so a
// reference in b/y.json to its neighbour z.json read a/z.json whenever there
// was one and generated from it in silence. The error printed when there was
// not one said references are read "next to the referring schema file", which
// was true of the default mode only.
//
// The fixture is that scenario: b/z.json is an integer, a/z.json is a string of
// at most one character, and only the first is the one b/y.json names.

// refBaseLayout writes a/x.json, b/y.json, b/z.json and the decoy a/z.json,
// with or without $ids. It returns the directory and the two input paths.
func refBaseLayout(t *testing.T, withIDs bool) (dir, xPath, yPath string) {
	t.Helper()
	dir = t.TempDir()
	x := map[string]any{"title": "X", "type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}}
	y := map[string]any{"title": "Y", "type": "object", "properties": map[string]any{"z": map[string]any{"$ref": "z.json"}}}
	if withIDs {
		x["$id"] = "https://ex.test/a/x.json"
		y["$id"] = "https://ex.test/b/y.json"
	}
	xPath = filepath.Join(dir, "a", "x.json")
	yPath = filepath.Join(dir, "b", "y.json")
	writeJSONFile(t, xPath, x)
	writeJSONFile(t, yPath, y)
	writeFile(t, filepath.Join(dir, "b", "z.json"), `{"type": "integer"}`)
	writeFile(t, filepath.Join(dir, "a", "z.json"), `{"type": "string", "maxLength": 1}`)
	return dir, xPath, yPath
}

// mkdirs makes a fresh directory holding the named subdirectories.
func mkdirs(t *testing.T, subdirs ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range subdirs {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(data))
}

// refBaseModes are the ways one run can hold both inputs, each spelled as the
// arguments that generate them into modRoot/gen.
var refBaseModes = []struct {
	name    string
	withIDs bool
	args    func(t *testing.T, modRoot, xPath, yPath string) []string
}{
	{"default", false, func(_ *testing.T, modRoot, xPath, yPath string) []string {
		return []string{xPath, yPath, "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
	}},
	{"shared-types", false, func(_ *testing.T, modRoot, xPath, yPath string) []string {
		return []string{xPath, yPath, "--shared-types", "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
	}},
	// With $ids the relative reference resolves against https://ex.test/b/,
	// which nothing serves, and is then read beside the file b/y.json was read
	// from.
	{"default with $ids", true, func(_ *testing.T, modRoot, xPath, yPath string) []string {
		return []string{xPath, yPath, "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
	}},
	{"shared-types with $ids", true, func(_ *testing.T, modRoot, xPath, yPath string) []string {
		return []string{xPath, yPath, "--shared-types", "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
	}},
	{"schema-package", true, func(_ *testing.T, modRoot, xPath, yPath string) []string {
		return []string{xPath, yPath,
			"--schema-package", "https://ex.test/a/x.json=example.com/m/gen",
			"--schema-package", "https://ex.test/b/y.json=example.com/m/gen",
			"-o", modRoot}
	}},
	{"config packages", true, func(t *testing.T, modRoot, xPath, yPath string) []string {
		cfg := filepath.Join(t.TempDir(), "schemagen.json")
		writeJSONFile(t, cfg, map[string]any{
			"outputDir": modRoot,
			"documents": []map[string]any{
				{"id": "https://ex.test/a/x.json", "path": xPath, "package": "example.com/m/gen"},
				{"id": "https://ex.test/b/y.json", "path": yPath, "package": "example.com/m/gen"},
			},
		})
		return []string{"--config", cfg}
	}},
}

func TestRelativeRefIsReadBesideTheFileItIsWrittenInEveryMode(t *testing.T) {
	for _, mode := range refBaseModes {
		t.Run(mode.name, func(t *testing.T) {
			_, xPath, yPath := refBaseLayout(t, mode.withIDs)
			generateCompileRunRoots(t,
				func(modRoot string) []string { return mode.args(t, modRoot, xPath, yPath) },
				"example.com/m/gen",
				[]rootInstance{
					// b/z.json's integer, which is what b/y.json names.
					{"Y", `{"z":5}`, true, `{"z":5}`},
					// a/z.json's string, which it does not.
					{"Y", `{"z":"s"}`, false, ""},
					{"X", `{"q":"s"}`, true, `{"q":"s"}`},
				})
		})
	}
}

// With the file it names missing, the reference fails, and says where it looked:
// beside the referring file. It used to read the decoy beside the first input
// instead, and generate.
func TestMissingRelativeRefNamesTheFileBesideTheReferrer(t *testing.T) {
	for _, mode := range refBaseModes {
		t.Run(mode.name, func(t *testing.T) {
			dir, xPath, yPath := refBaseLayout(t, mode.withIDs)
			if err := os.Remove(filepath.Join(dir, "b", "z.json")); err != nil {
				t.Fatal(err)
			}
			stderr, err := runGenerateCapturing(t, mode.args(t, t.TempDir(), xPath, yPath)...)
			if err == nil {
				t.Fatalf("generation succeeded with b/z.json missing, so it read some other z.json:\n%s", stderr)
			}
			msg := err.Error()
			if !strings.Contains(msg, filepath.Join(dir, "b", "z.json")) {
				t.Errorf("the failure should name %s, the file beside b/y.json:\n%s", filepath.Join(dir, "b", "z.json"), msg)
			}
			if strings.Contains(msg, filepath.Join(dir, "a", "z.json")) {
				t.Errorf("the failure names a/z.json, which b/y.json does not refer to:\n%s", msg)
			}
		})
	}
}

// An input reached by a relative path from another input is the input, not a
// second copy of it read off disk. Under --shared-types the second copy used to
// be a fresh FileResolver instance of a/x.json, which the generated-types
// registry, keyed by node, did not recognise -- so Y's field got a type of its
// own, XJSON, derived from the reference, beside the input's X. Instance
// identity across the input list and the reference is what makes them one type.
//
// The documents are untitled on purpose: a title names the copy X as well, and
// the two then merged by name, which hid the second instance rather than
// avoiding it.
func TestInputReachedByRelativePathIsTheInput(t *testing.T) {
	dir := mkdirs(t, "a", "b")
	xPath := filepath.Join(dir, "a", "x.json")
	yPath := filepath.Join(dir, "b", "y.json")
	writeFile(t, xPath, `{"type": "object", "properties": {"q": {"type": "string", "minLength": 2}}}`)
	writeFile(t, yPath, `{"type": "object", "properties": {"x": {"$ref": "../a/x.json"}}}`)
	names := []string{"--root-name", "x.json=X", "--root-name", "y.json=Y"}

	out := t.TempDir()
	stderr, err := runGenerateCapturing(t, append([]string{xPath, yPath, "--shared-types", "-o", out, "-p", "gen"}, names...)...)
	if err != nil {
		t.Fatalf("generate: %v\n%s", err, stderr)
	}
	// X is declared once, by its own input, and Y's field names it.
	if got := strings.Join(declaredTypeNames(t, out), ","); got != "X,Y" {
		t.Errorf("declared types = %s, want X,Y: a second declaration is a second instance of a/x.json", got)
	}
	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return append([]string{xPath, yPath, "--shared-types", "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}, names...)
		},
		"example.com/m/gen",
		[]rootInstance{
			{"Y", `{"x":{"q":"ab"}}`, true, `{"x":{"q":"ab"}}`},
			{"Y", `{"x":{"q":"a"}}`, false, ""},
		})
}

// Two inputs claiming one $id are refused in every mode, naming both files. The
// default mode used to let such an $id answer no $ref at all; the inputs of a
// run are one set of schemas now, and a URI naming two of them names neither.
func TestTwoInputsClaimingOneIDAreRefusedInEveryMode(t *testing.T) {
	dir := mkdirs(t, "a", "b")
	aPath := filepath.Join(dir, "a", "one.json")
	bPath := filepath.Join(dir, "b", "two.json")
	writeFile(t, aPath, `{"$id": "https://ex.test/same.json", "title": "One", "type": "string"}`)
	writeFile(t, bPath, `{"$id": "https://ex.test/same.json", "title": "Two", "type": "integer"}`)
	for _, extra := range [][]string{nil, {"--shared-types"}, {"--schema-package", "https://ex.test/same.json=example.com/m/gen"}} {
		args := append([]string{aPath, bPath, "-o", t.TempDir(), "-p", "gen"}, extra...)
		if len(extra) == 2 && extra[0] == "--schema-package" {
			args = append([]string{aPath, bPath, "-o", t.TempDir()}, extra...)
		}
		_, err := runGenerateCapturing(t, args...)
		if err == nil {
			t.Fatalf("%v: two inputs with one $id were accepted", extra)
		}
		for _, want := range []string{"duplicate $id", "https://ex.test/same.json", aPath, bPath} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%v: the refusal should mention %q: %v", extra, want, err)
			}
		}
	}
}

// Reads stay confined, and confined to the directory of the file a reference is
// written in. Two inputs in sibling directories do not open either directory to
// the other: b/y.json reaching into a/ for a file nobody listed is refused, as
// it is when b/y.json is generated alone.
func TestRelativeRefStaysConfinedToTheReferrersDirectory(t *testing.T) {
	dir := mkdirs(t, "a", "b")
	xPath := filepath.Join(dir, "a", "x.json")
	yPath := filepath.Join(dir, "b", "y.json")
	writeFile(t, xPath, `{"title": "X", "type": "object"}`)
	writeFile(t, filepath.Join(dir, "a", "private.json"), `{"type": "string"}`)
	writeFile(t, yPath, `{"title": "Y", "type": "object", "properties": {"p": {"$ref": "../a/private.json"}}}`)
	for _, extra := range [][]string{nil, {"--shared-types"}} {
		args := append([]string{xPath, yPath, "-o", t.TempDir(), "-p", "gen"}, extra...)
		stderr, err := runGenerateCapturing(t, args...)
		if err == nil {
			t.Fatalf("%v: reading a/private.json from b/y.json was allowed:\n%s", extra, stderr)
		}
		if !strings.Contains(err.Error(), "refusing to read") {
			t.Errorf("%v: the refusal should say it refused the read: %v", extra, err)
		}
	}
}
