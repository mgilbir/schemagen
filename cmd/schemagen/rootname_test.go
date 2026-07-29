package schemagen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile writes a test fixture, failing the test if it cannot.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseRootNameFlagsSplitsOnLastEquals(t *testing.T) {
	// Both $ids and file names may contain "=", but a Go type name never does.
	spec, err := parseRootNameFlags([]string{
		"a=b.json=Weird",
		"id:https://ex.test/q.json?v=1=Versioned",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.byBase["a=b.json"]; got != "Weird" {
		t.Errorf(`base key "a=b.json" = %q, want "Weird"`, got)
	}
	if got := spec.byID["https://ex.test/q.json?v=1"]; got != "Versioned" {
		t.Errorf(`id key with a query = %q, want "Versioned"`, got)
	}
}

func TestParseRootNameFlagsRejectsConflictingDuplicates(t *testing.T) {
	if _, err := parseRootNameFlags([]string{"a.json=First", "a.json=Second"}); err == nil {
		t.Error("a repeated key with a different name should be rejected, not silently overwritten")
	}
	if _, err := parseRootNameFlags([]string{"a.json=Same", "a.json=Same"}); err != nil {
		t.Errorf("a repeated key with the same name is harmless: %v", err)
	}
	if _, err := parseRootNameFlags([]string{"Bare", "Other"}); err == nil {
		t.Error("two different bare names should be rejected")
	}
	for _, bad := range []string{"=NoKey", "a.json=", "id:=Empty"} {
		if _, err := parseRootNameFlags([]string{bad}); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestRootNameSpecPrecedence(t *testing.T) {
	spec, err := parseRootNameFlags([]string{
		"common.json=FromBase",
		"id:https://ex.test/a/common.json=FromID",
		"file:one/common.json=FromFile",
	})
	if err != nil {
		t.Fatal(err)
	}
	// file: beats id: beats base name.
	if got := spec.lookup("one/common.json", "https://ex.test/a/common.json"); got != "FromFile" {
		t.Errorf("path key should win, got %q", got)
	}
	if got := spec.lookup("other/common.json", "https://ex.test/a/common.json"); got != "FromID" {
		t.Errorf("$id key should beat the base name, got %q", got)
	}
	if got := spec.lookup("other/common.json", "https://ex.test/elsewhere.json"); got != "FromBase" {
		t.Errorf("base name is the fallback, got %q", got)
	}
	if got := spec.lookup("unrelated.json", "https://ex.test/nope.json"); got != "" {
		t.Errorf("no key should match, got %q", got)
	}
}

func TestRootNameSpecBareRequiresSingleInput(t *testing.T) {
	spec, err := parseRootNameFlags([]string{"OnlyOne"})
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.validate(1); err != nil {
		t.Errorf("a bare name with one input is fine: %v", err)
	}
	if err := spec.validate(2); err == nil {
		t.Error("a bare name with several inputs should be rejected")
	}
	// The bare form applies whatever the input is called.
	if got := spec.lookup("whatever.json", ""); got != "OnlyOne" {
		t.Errorf("bare name should apply to the single input, got %q", got)
	}
}

func TestRootNameSpecWarnsOnKeysThatMatchedNothing(t *testing.T) {
	spec, err := parseRootNameFlags([]string{"used.json=Used", "typo.json=Typo"})
	if err != nil {
		t.Fatal(err)
	}
	spec.lookup("used.json", "")

	var out strings.Builder
	spec.warnUnused(&out)
	if !strings.Contains(out.String(), "typo.json") {
		t.Errorf("a key matching nothing should be reported, got: %q", out.String())
	}
	if strings.Contains(out.String(), "used.json") {
		t.Errorf("a key that matched should not be reported, got: %q", out.String())
	}
}

// The bare form must reach the generator, including its identifier validation.
func TestRootNameBareFormIsApplied(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"title": "Ignored", "type": "object", "properties": {"n": {"type": "integer"}}
	}`)
	out := t.TempDir()
	if err := runGenerateArgs(t, filepath.Join(src, "a.json"), "-o", out, "-p", "m", "--root-name", "Chosen"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(out, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "type Chosen struct") {
		t.Errorf("the bare --root-name should override the schema title:\n%s", body)
	}
	if err := runGenerateArgs(t, filepath.Join(src, "a.json"), "-o", t.TempDir(), "-p", "m", "--root-name", "lowercase"); err == nil {
		t.Error("a bare name that is not an exported Go identifier should be rejected")
	}
}
