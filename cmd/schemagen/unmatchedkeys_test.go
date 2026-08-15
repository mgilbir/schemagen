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

// ---------- issue #318: the config's fieldNames ----------
//
// The same two overrides, naming the same nonexistent type and property against
// the same document: --field-map said so and the config's fieldNames did not,
// while the rootName on that very config entry did. Written the way #298 wrote
// the config's package and output keys, and warnUnmatchedDocumentKeys words
// them.

// The reference the two spellings are held against: --field-map, which has
// reported this since before the config existed.
func TestFieldMapEntryMatchingNoPropertyIsReported(t *testing.T) {
	dir, paths := unmatchedKeysInputs(t)
	fieldMapPath := filepath.Join(dir, "fm.json")
	writeFile(t, fieldMapPath, `{"t.json": {"T": {"nosuchprop": "Renamed"}, "NoSuchType": {"k": "Renamed"}}}`)

	stderr, err := runGenerateCapturing(t, paths[0], "-o", filepath.Join(dir, "gen"), "-p", "models",
		"--field-map", fieldMapPath)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		`warning: field-map entry "t.json/T.nosuchprop" matched no property`,
		`warning: field-map entry "t.json/NoSuchType.k" matched no property`,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}
}

// The identical overrides through the config say the same, named as the
// config's -- "--field-map" would send the reader to a flag this run never had.
func TestConfigFieldNamesEntryMatchingNoPropertyIsReported(t *testing.T) {
	dir, paths := unmatchedKeysInputs(t)
	cfgPath := filepath.Join(dir, "cfg.json")
	writeFile(t, cfgPath, `{
		"outputDir": `+quoteJSON(filepath.Join(dir, "gen"))+`, "package": "models",
		"documents": [
			{"id": "https://ex.test/t.json", "path": `+quoteJSON(paths[0])+`,
			 "fieldNames": {"T": {"nosuchprop": "Renamed"}, "NoSuchType": {"k": "Renamed"}}}
		]}`)

	stderr, err := runGenerateCapturing(t, "--config", cfgPath)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		`warning: config fieldNames for "https://ex.test/t.json": entry "T.nosuchprop" matched no property`,
		`warning: config fieldNames for "https://ex.test/t.json": entry "NoSuchType.k" matched no property`,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "--field-map") {
		t.Errorf("no --field-map was given on this run:\n%s", stderr)
	}
}

// The easier half of the same gap, and the one the two sides of a single config
// entry disagreed on: the rootName on an entry selecting nothing warned and the
// fieldNames beside it did not.
func TestConfigFieldNamesEntryMatchingNoInputIsReported(t *testing.T) {
	dir, paths := unmatchedKeysInputs(t)
	cfgPath := filepath.Join(dir, "cfg.json")
	writeFile(t, cfgPath, `{
		"outputDir": `+quoteJSON(filepath.Join(dir, "gen"))+`, "package": "models",
		"documents": [
			{"id": "https://ex.test/t.json", "path": `+quoteJSON(paths[0])+`},
			{"id": "https://ex.test/typo.json", "rootName": "Typo",
			 "fieldNames": {"T": {"k": "Renamed"}}}
		]}`)

	stderr, err := runGenerateCapturing(t, "--config", cfgPath)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		`warning: config rootName for "https://ex.test/typo.json" matched no input schema`,
		`warning: config fieldNames for "https://ex.test/typo.json" matched no input schema`,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the two halves of one entry must agree, missing %q in:\n%s", want, stderr)
		}
	}
	// A dead selector warns once for the entry, not once per override under it.
	if strings.Contains(stderr, `for "https://ex.test/typo.json": entry`) {
		t.Errorf("overrides under a dead selector were never asked:\n%s", stderr)
	}
}

// An entry keyed by path rather than $id is named by its path, since that is
// what its own source wrote.
func TestConfigFieldNamesEntryKeyedByPathIsNamedByPath(t *testing.T) {
	dir, paths := unmatchedKeysInputs(t)
	cfgPath := filepath.Join(dir, "cfg.json")
	writeFile(t, cfgPath, `{
		"outputDir": `+quoteJSON(filepath.Join(dir, "gen"))+`, "package": "models",
		"documents": [
			{"path": `+quoteJSON(paths[0])+`, "fieldNames": {"T": {"nosuchprop": "Renamed"}}}
		]}`)

	stderr, err := runGenerateCapturing(t, "--config", cfgPath)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	want := `config fieldNames for ` + quoteJSON(paths[0]) + `: entry "T.nosuchprop" matched no property`
	if !strings.Contains(stderr, want) {
		t.Errorf("missing %q in:\n%s", want, stderr)
	}
}

// The warning must not fire on an override that did its job, or it is noise on
// every correct run.
func TestConfigFieldNamesThatMatchedAreNotReported(t *testing.T) {
	dir, paths := unmatchedKeysInputs(t)
	cfgPath := filepath.Join(dir, "cfg.json")
	writeFile(t, cfgPath, `{
		"outputDir": `+quoteJSON(filepath.Join(dir, "gen"))+`, "package": "models",
		"documents": [
			{"id": "https://ex.test/t.json", "path": `+quoteJSON(paths[0])+`,
			 "fieldNames": {"T": {"k": "Renamed"}}}
		]}`)

	stderr, err := runGenerateCapturing(t, "--config", cfgPath)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stderr, "config fieldNames") {
		t.Errorf("an override that was applied must not be reported:\n%s", stderr)
	}
}

// Two documents with one base name and one root type name, in different
// packages, are what a base-name index gets wrong: the property exists in one
// document and not the other, and answering "was it applied" out of a
// base-name index lets the first document's rename excuse the second's typo.
// The config keys by document identity precisely so it need not.
func TestOneDocumentsAppliedOverrideDoesNotExcuseAnothersTypo(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"one", "two"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	onePath := filepath.Join(dir, "one", "common.json")
	twoPath := filepath.Join(dir, "two", "common.json")
	writeFile(t, onePath, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/one/common.json",
		"title": "Common", "type": "object", "properties": {"postal_code": {"type": "string"}}}`)
	// The same base name, the same root type name, and no postal_code: the
	// entry below names a property this document does not have.
	writeFile(t, twoPath, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/two/common.json",
		"title": "Common", "type": "object", "properties": {"city": {"type": "string"}}}`)

	cfgPath := filepath.Join(dir, "cfg.json")
	writeFile(t, cfgPath, `{
		"outputDir": `+quoteJSON(filepath.Join(dir, "gen"))+`,
		"documents": [
			{"id": "https://ex.test/one/common.json", "path": `+quoteJSON(onePath)+`,
			 "package": "ex.test/m/one", "rootName": "Common",
			 "fieldNames": {"Common": {"postal_code": "PostalCode"}}},
			{"id": "https://ex.test/two/common.json", "path": `+quoteJSON(twoPath)+`,
			 "package": "ex.test/m/two", "rootName": "Common",
			 "fieldNames": {"Common": {"postal_code": "PostalCode"}}}
		]}`)

	stderr, err := runGenerateCapturing(t, "--config", cfgPath)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	want := `warning: config fieldNames for "https://ex.test/two/common.json": entry "Common.postal_code" matched no property`
	if !strings.Contains(stderr, want) {
		t.Errorf("the sibling document's applied override must not excuse this one, missing %q in:\n%s", want, stderr)
	}
	if strings.Contains(stderr, `"https://ex.test/one/common.json": entry`) {
		t.Errorf("the document that does have the property must not be reported:\n%s", stderr)
	}
}

// A --field-map entry renaming the same property is precedence working as
// documented, not a typo in the config: the property matched, and only the name
// came from elsewhere.
func TestConfigFieldNamesOverriddenByTheFlagIsNotReportedUnused(t *testing.T) {
	dir, paths := unmatchedKeysInputs(t)
	fieldMapPath := filepath.Join(dir, "fm.json")
	writeFile(t, fieldMapPath, `{"t.json": {"T": {"k": "FromFlag"}}}`)
	cfgPath := filepath.Join(dir, "cfg.json")
	out := filepath.Join(dir, "gen")
	writeFile(t, cfgPath, `{
		"outputDir": `+quoteJSON(out)+`, "package": "models",
		"documents": [
			{"id": "https://ex.test/t.json", "path": `+quoteJSON(paths[0])+`,
			 "fieldNames": {"T": {"k": "FromConfig"}}}
		]}`)

	stderr, err := runGenerateCapturing(t, "--config", cfgPath, "--field-map", fieldMapPath)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stderr, "config fieldNames") {
		t.Errorf("the property matched; the flag only chose the name:\n%s", stderr)
	}
	body, err := os.ReadFile(filepath.Join(out, "t.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "FromFlag") {
		t.Errorf("the flag should have won the name:\n%s", body)
	}
}

// A run that stops before it reaches its inputs has matched nothing, and the
// new report has to hold its tongue there like the ones beside it. Issue #298.
func TestARunRefusedBeforeGeneratingReportsNoUnusedConfigFieldNames(t *testing.T) {
	dir, paths := unmatchedKeysInputs(t)
	cfgPath := filepath.Join(dir, "cfg.json")
	writeFile(t, cfgPath, `{
		"outputDir": `+quoteJSON(filepath.Join(dir, "gen"))+`,
		"documents": [
			{"id": "https://ex.test/t.json", "path": `+quoteJSON(paths[0])+`,
			 "fieldNames": {"T": {"k": "Key"}}}
		]}`)

	// Refused for the flag combination, before any input is read.
	stderr, err := runGenerateCapturing(t, "--config", cfgPath, "--shared-types", "--validation", "runtime")
	if err == nil {
		t.Fatal("--shared-types with --validation runtime should fail")
	}
	if strings.Contains(stderr, "config fieldNames") {
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
