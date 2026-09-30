package schemagen

import (
	"path/filepath"
	"strings"
	"testing"
)

// The command line's side of the keyword ledger, the dialect rule and the
// unknown-keyword setting: each is a report the generator makes, and a report
// the command never prints is one nobody reads. Every test here inspects
// stderr, which is where the reports go.

// An assertion the generated code does not carry is named on stderr, where it
// was written; a schema whose every assertion is carried adds nothing there.
func TestUnclaimedKeywordsAreReported(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "dropped.json"), `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "Dropped", "type": "string",
		"allOf": [{"pattern": "^a"}, {"pattern": "b$"}]
	}`)
	writeFile(t, filepath.Join(src, "carried.json"), `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "Carried", "type": "object",
		"properties": {"a": {"type": "string", "minLength": 2}},
		"required": ["a"]
	}`)

	stderr, err := runGenerateCapturing(t, filepath.Join(src, "dropped.json"), "-o", t.TempDir(), "-p", "d")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	want := "#/allOf/1 (type Dropped): pattern: "
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr does not name the second pattern, which the generated code does not enforce; want a line containing %q in:\n%s", want, stderr)
	}

	// --schema-package generates through a path of its own, which reports the
	// same way.
	writeFile(t, filepath.Join(src, "dropped-id.json"), `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/dropped.json",
		"title": "Dropped", "type": "string",
		"allOf": [{"pattern": "^a"}, {"pattern": "b$"}]
	}`)
	stderr, err = runGenerateCapturing(t, filepath.Join(src, "dropped-id.json"), "-o", t.TempDir(),
		"--schema-package", "https://ex.test/dropped.json=example.com/m/gen")
	if err != nil {
		t.Fatalf("generate --schema-package: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, want) {
		t.Errorf("under --schema-package, stderr does not name the second pattern; want a line containing %q in:\n%s", want, stderr)
	}

	stderr, err = runGenerateCapturing(t, filepath.Join(src, "carried.json"), "-o", t.TempDir(), "-p", "c")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stderr, "warning:") {
		t.Errorf("a schema whose every assertion is enforced drew a warning:\n%s", stderr)
	}
}

// --draft wins for the documents listed, and where a document's own $schema
// says otherwise the run says which it followed and where the other is.
func TestDraftOverridingAStatedSchemaIsReported(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "s.json"), `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "S", "type": "object",
		"properties": {"a": {"type": "string"}}
	}`)

	stderr, err := runGenerateCapturing(t, filepath.Join(src, "s.json"), "--draft", "7", "-o", t.TempDir(), "-p", "s")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	want := "#: $schema names Draft 2020-12, and --draft reads it as Draft-07"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr does not report the overridden $schema; want %q in:\n%s", want, stderr)
	}

	// The same document without --draft is read as it says, and nothing is
	// reported.
	stderr, err = runGenerateCapturing(t, filepath.Join(src, "s.json"), "-o", t.TempDir(), "-p", "s")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stderr, "--draft reads it as") {
		t.Errorf("a run without --draft reported an override:\n%s", stderr)
	}
}

// A keyword schemagen does not know is an annotation unless --strict-keywords
// is set -- on the command line or in the config file -- and then it refuses
// the schema, naming where the keyword is written.
func TestStrictKeywordsRefusesAnUnknownKeyword(t *testing.T) {
	src := t.TempDir()
	path := filepath.Join(src, "k.json")
	writeFile(t, path, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "K", "type": "object",
		"properties": {"a": {"type": "string", "minLenght": 3}}
	}`)

	if stderr, err := runGenerateCapturing(t, path, "-o", t.TempDir(), "-p", "k"); err != nil {
		t.Fatalf("an unknown keyword refused the schema without --strict-keywords: %v\nstderr:\n%s", err, stderr)
	}

	_, err := runGenerateCapturing(t, path, "--strict-keywords", "-o", t.TempDir(), "-p", "k")
	if err == nil {
		t.Fatal("--strict-keywords accepted a schema with an unknown keyword")
	}
	if !strings.Contains(err.Error(), "#/properties/a") || !strings.Contains(err.Error(), "minLenght") {
		t.Errorf("the refusal does not say where the unknown keyword is: %v", err)
	}

	cfg := filepath.Join(src, "schemagen.json")
	writeFile(t, cfg, `{"strictKeywords": true}`)
	if _, err := runGenerateCapturing(t, path, "--config", cfg, "-o", t.TempDir(), "-p", "k"); err == nil {
		t.Error(`"strictKeywords": true in the config file accepted a schema with an unknown keyword`)
	}
}
