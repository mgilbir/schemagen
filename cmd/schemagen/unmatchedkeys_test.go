package schemagen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every keyed input in the tool reports a key that matched nothing -- and these
// two did not, so a mistyped $id was dropped in silence. Issue #298.

const unmatchedKeysDocT = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"$id": "https://ex.test/t.json",
	"title": "T", "type": "object", "properties": {"k": {"type": "string"}}}`

const unmatchedKeysDocRoot = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"$id": "https://ex.test/root.json",
	"title": "Root", "type": "object",
	"properties": {"t": {"$ref": "t.json"}}}`

func unmatchedKeysInputs(t *testing.T) (string, []string) {
	t.Helper()
	return writeSchemas(t, "t.json", unmatchedKeysDocT, "root.json", unmatchedKeysDocRoot)
}

func TestSchemaPackageKeyMatchingNoInputIsReported(t *testing.T) {
	dir, paths := unmatchedKeysInputs(t)
	stderr, err := runGenerateCapturing(t, paths[0], paths[1], "-o", filepath.Join(dir, "gen"),
		"--schema-package", "https://ex.test/t.json=ex.test/m/t",
		"--schema-package", "https://ex.test/root.json=ex.test/m/r",
		"--schema-package", "https://ex.test/typo.json=ex.test/m/typo")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	want := `warning: --schema-package "https://ex.test/typo.json" matched no input schema`
	if !strings.Contains(stderr, want) {
		t.Errorf("missing %q in:\n%s", want, stderr)
	}
	// The keys that did match say nothing.
	if strings.Contains(stderr, "https://ex.test/t.json\" matched no") {
		t.Errorf("a key that matched an input must not be reported:\n%s", stderr)
	}
}

// --schema-output is the more damaging of the two: the flag has a silent
// fallback, so a mistyped key left the document at its default path and the run
// read as having honoured it.
func TestSchemaOutputKeyMatchingNoInputIsReported(t *testing.T) {
	dir, paths := unmatchedKeysInputs(t)
	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, paths[0], paths[1], "-o", out,
		"--schema-package", "https://ex.test/t.json=ex.test/m/t",
		"--schema-package", "https://ex.test/root.json=ex.test/m/r",
		"--schema-output", "https://ex.test/typo.json="+filepath.Join(out, "nope.go"))
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	want := `warning: --schema-output "https://ex.test/typo.json" matched no input schema`
	if !strings.Contains(stderr, want) {
		t.Errorf("missing %q in:\n%s", want, stderr)
	}
	if _, err := os.Stat(filepath.Join(out, "nope.go")); err == nil {
		t.Error("the unmatched --schema-output path should not have been written")
	}
}

// A config entry is named as the config's. "--schema-package" would send the
// reader to a command line that does not contain it.
func TestConfigPackageAndOutputEntriesMatchingNoInputAreReported(t *testing.T) {
	dir, paths := unmatchedKeysInputs(t)
	cfgPath := filepath.Join(dir, "cfg.json")
	writeFile(t, cfgPath, `{
		"documents": [
			{"id": "https://ex.test/t.json", "path": `+quoteJSON(paths[0])+`, "package": "ex.test/m/t"},
			{"id": "https://ex.test/root.json", "path": `+quoteJSON(paths[1])+`, "package": "ex.test/m/r"},
			{"id": "https://ex.test/typo.json", "package": "ex.test/m/typo", "output": "nope.go"}
		]}`)

	stderr, err := runGenerateCapturing(t, "--config", cfgPath, "-o", filepath.Join(dir, "gen"))
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		`warning: config package for "https://ex.test/typo.json" matched no input schema`,
		`warning: config output for "https://ex.test/typo.json" matched no input schema`,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}
}

// A flag that overwrites a config entry takes the key with it: reporting the
// pair as the config's would name the wrong source.
func TestAFlagOverwritingAConfigKeyIsReportedAsTheFlags(t *testing.T) {
	dir, paths := unmatchedKeysInputs(t)
	cfgPath := filepath.Join(dir, "cfg.json")
	writeFile(t, cfgPath, `{
		"documents": [
			{"id": "https://ex.test/t.json", "path": `+quoteJSON(paths[0])+`, "package": "ex.test/m/t"},
			{"id": "https://ex.test/root.json", "path": `+quoteJSON(paths[1])+`, "package": "ex.test/m/r"},
			{"id": "https://ex.test/typo.json", "package": "ex.test/m/typo"}
		]}`)

	stderr, err := runGenerateCapturing(t, "--config", cfgPath, "-o", filepath.Join(dir, "gen"),
		"--schema-package", "https://ex.test/typo.json=ex.test/m/other")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	want := `warning: --schema-package "https://ex.test/typo.json" matched no input schema`
	if !strings.Contains(stderr, want) {
		t.Errorf("missing %q in:\n%s", want, stderr)
	}
	if strings.Contains(stderr, "config package for") {
		t.Errorf("the flag owns the key now:\n%s", stderr)
	}
}

// The typo and the refusal it causes are one mistake, and the refusal can only
// name the input -- the document that is fine. The two have to be readable
// together, so the warning comes first.
func TestAnUnmatchedSchemaPackageKeyIsReportedBeforeTheRefusalItCauses(t *testing.T) {
	dir, paths := unmatchedKeysInputs(t)
	stderr, err := runGenerateCapturing(t, paths[0], paths[1], "-o", filepath.Join(dir, "gen"),
		"--schema-package", "https://ex.test/NOPE.json=ex.test/m/x",
		"--schema-package", "https://ex.test/root.json=ex.test/m/r")
	if err == nil {
		t.Fatal("an input with no mapping should fail")
	}
	if !strings.Contains(err.Error(), "no --schema-package mapping for") {
		t.Errorf("the refusal should stand, got: %v", err)
	}
	want := `warning: --schema-package "https://ex.test/NOPE.json" matched no input schema`
	if !strings.Contains(stderr, want) {
		t.Errorf("the key that caused it should be named too, missing %q in:\n%s", want, stderr)
	}
}

// A run that stops before it reaches its inputs has matched nothing, and
// reporting every key as unmatched there points the reader at a non-problem
// while the real error is below it. Issue #298.
//
// This one is refused for the flag combination, which is decided before the
// deferred reports are even set up -- the reproducer the issue filed.
func TestARunRefusedForItsFlagsReportsNoUnmatchedKeys(t *testing.T) {
	dir, paths := unmatchedKeysInputs(t)
	fieldMapPath := filepath.Join(dir, "fields.json")
	writeFile(t, fieldMapPath, `{"t.json": {"T": {"k": "Key"}}}`)

	// Refused for the flag combination, which is decided before any input is
	// read: both --root-name keys name inputs and would have matched.
	stderr, err := runGenerateCapturing(t, paths[0], paths[1], "-o", filepath.Join(dir, "gen"),
		"--shared-types", "--validation", "runtime",
		"--field-map", fieldMapPath,
		"--root-name", "t.json=T", "--root-name", "root.json=Root")
	if err == nil {
		t.Fatal("--shared-types with --validation runtime should fail")
	}
	if strings.Contains(stderr, "matched no input schema") || strings.Contains(stderr, "does not match any generated schema file") {
		t.Errorf("nothing was matched or unmatched on this run:\n%s", stderr)
	}
}

// The same, refused further along -- past the point where the deferred reports
// are set up, so it is the reports themselves that have to hold their tongue.
func TestARunRefusedBeforeGeneratingReportsNoUnmatchedKeys(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Two inputs deriving one output file name: refused after the reports are
	// deferred and before any input is generated.
	aPath := filepath.Join(dir, "a", "user.json")
	bPath := filepath.Join(dir, "b", "user.json")
	writeFile(t, aPath, `{"$schema":"https://json-schema.org/draft/2020-12/schema","title":"UserA","type":"object"}`)
	writeFile(t, bPath, `{"$schema":"https://json-schema.org/draft/2020-12/schema","title":"UserB","type":"object"}`)
	fieldMapPath := filepath.Join(dir, "fields.json")
	writeFile(t, fieldMapPath, `{"user.json": {"UserA": {"k": "Key"}}}`)

	stderr, err := runGenerateCapturing(t, aPath, bPath, "-o", filepath.Join(dir, "gen"), "-p", "gen",
		"--field-map", fieldMapPath,
		"--root-name", "file:"+aPath+"=UserA", "--root-name", "file:"+bPath+"=UserB")
	if err == nil {
		t.Fatal("two inputs mapping to one output file should fail")
	}
	if strings.Contains(stderr, "matched no input schema") || strings.Contains(stderr, "does not match any generated schema file") {
		t.Errorf("nothing was matched or unmatched on this run:\n%s", stderr)
	}
}

// The gate above must not swallow the report on a run that did reach its
// inputs: a key that matched nothing there is the thing the warning exists for.
func TestARunThatReachedItsInputsStillReportsUnmatchedKeys(t *testing.T) {
	dir, paths := unmatchedKeysInputs(t)
	stderr, err := runGenerateCapturing(t, paths[0], "-o", filepath.Join(dir, "gen"), "-p", "gen",
		"--root-name", "id:https://ex.test/typo.json=Nope")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	want := `warning: --root-name "https://ex.test/typo.json" matched no input schema`
	if !strings.Contains(stderr, want) {
		t.Errorf("missing %q in:\n%s", want, stderr)
	}
}
