package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The guards for issues #329, #330 and #331: what a run actually prints about
// the keys it was given and the documents it could not read.
//
// They drive the built binary, as the #322 guards in main_test.go do and for the
// same reason: every in-process fixture in cmd/schemagen reads the one stream
// cmd.SetErr redirects, and a message can reach a terminal by another route
// entirely. Where a count is the assertion it is asserted as a count -- a guard
// that greps for a line passes whether the line appears once, twice, or beside a
// second line contradicting it, which is exactly the shape #329 is.

// runIn runs the command in dir and returns its stderr and the run's error.
// Unlike runFailingIn it does not insist on failure: half of what is asserted
// below is what a *successful* run says.
func runIn(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(schemagenBinary(t), args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = new(strings.Builder)
	err := cmd.Run()
	return stderr.String(), err
}

// warningLines is every "warning:" line of a run's stderr, sorted. The set is
// what #329 is about: which keys a run reports depends on nothing but the run.
func warningLines(stderr string) []string {
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, "warning:") {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	sort.Strings(lines)
	return lines
}

func writeSchemaFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------- issue #329 ----------

// The minimal contradiction. One document, one input, one --field-map override
// that renames first_name to a name zip_code already holds: the override matched
// -- it is the reason generation failed -- and the run announced it as having
// matched no property, one line above an error telling the reader to go and
// check --field-map overrides.
//
// Applied overrides are recorded only after gen.Generate returns, so an override
// that fails inside generation counts as unmatched.
func TestAFieldMapOverrideThatFailedGenerationIsNotCalledUnmatched(t *testing.T) {
	dir := t.TempDir()
	writeSchemaFile(t, dir, "leaf.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "Leaf", "type": "object",
		"properties": {"first_name": {"type": "string"}, "zip_code": {"type": "string"}}
	}`)
	writeSchemaFile(t, dir, "fm.json", `{"leaf.json": {"Leaf": {"first_name": "ZipCode"}}}`)

	stderr := runFailingIn(t, dir, "generate", "leaf.json", "--field-map", "fm.json", "-p", "models", "-o", "out")

	// The failure itself is unchanged and still said once.
	const failure = `collides with property "first_name"`
	if got := countLinesContaining(stderr, failure); got != 1 {
		t.Errorf("the collision is reported on %d lines, want 1:\n%s", got, stderr)
	}
	// The line that contradicted it.
	if got := countLinesContaining(stderr, "matched no property"); got != 0 {
		t.Errorf("an override that failed inside generation is reported as matching no property on %d lines, want 0:\n%s", got, stderr)
	}
	if got := countLinesContaining(stderr, "warning:"); got != 0 {
		t.Errorf("a run that failed partway emitted %d warning lines about its keys, want 0:\n%s", got, stderr)
	}
}

// The order dependence. Both runs are the same command line with its two inputs
// swapped, and both fail on b.json for the same reason; a.json is an input of
// both and every key naming it matches in both. Only when b.json is listed first
// did the run grow two warnings, because a.json was never reached.
//
// Equality of the two warning sets is the property under test. The counts are
// asserted beside it so that a future change making both runs equally wrong
// cannot pass.
func TestUnmatchedKeyWarningsDoNotDependOnTheOrderTheInputsWereListed(t *testing.T) {
	dir := t.TempDir()
	writeSchemaFile(t, dir, "a.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "A", "type": "object", "properties": {"first_name": {"type": "string"}}
	}`)
	// Deliberately unresolvable, so the run gives up at this document.
	writeSchemaFile(t, dir, "b.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "B", "type": "object", "properties": {"x": {"$ref": "nope.json"}}
	}`)
	writeSchemaFile(t, dir, "fm.json", `{"a.json": {"A": {"first_name": "GivenName"}}}`)

	common := []string{"--field-map", "fm.json", "--root-name", "a.json=A", "--root-name", "b.json=B", "-p", "models"}
	run := func(first, second, out string) []string {
		t.Helper()
		args := append([]string{"generate", first, second}, common...)
		stderr, err := runIn(t, dir, append(args, "-o", out)...)
		if err == nil {
			t.Fatalf("schemagen generate %s %s exited 0, want the unresolvable $ref to fail it\nstderr:\n%s", first, second, stderr)
		}
		if got := countLinesContaining(stderr, "warning:"); got != 0 {
			t.Errorf("listing %s first produced %d warning lines about keys naming an input the run never reached, want 0:\n%s",
				first, got, stderr)
		}
		return warningLines(stderr)
	}

	forward := run("a.json", "b.json", "out1")
	reverse := run("b.json", "a.json", "out2")
	if strings.Join(forward, "\n") != strings.Join(reverse, "\n") {
		t.Errorf("the same command line reports different keys depending on the order its inputs were listed:\n a.json first: %v\n b.json first: %v",
			forward, reverse)
	}
}

// The control on both guards above, and the reason they cannot be satisfied by
// removing the reports. A run that finishes still says which keys matched
// nothing, and says each of them once.
func TestACompletedRunStillReportsEveryKeyThatMatchedNothing(t *testing.T) {
	dir := t.TempDir()
	writeSchemaFile(t, dir, "a.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "A", "type": "object", "properties": {"first_name": {"type": "string"}}
	}`)
	writeSchemaFile(t, dir, "fm.json", `{"nosuch.json": {"A": {"first_name": "GivenName"}}}`)

	stderr, err := runIn(t, dir, "generate", "a.json", "--field-map", "fm.json",
		"--root-name", "nosuch.json=Whatever", "-p", "models", "-o", "out")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		`warning: field-map key "nosuch.json" does not match any generated schema file`,
		`warning: --root-name "nosuch.json" matched no input schema`,
	} {
		if got := countLinesContaining(stderr, want); got != 1 {
			t.Errorf("%q appears on %d lines, want 1:\n%s", want, got, stderr)
		}
	}
}

// ---------- issue #330 ----------

const yamlBodiedLeaf = `$schema: "https://json-schema.org/draft/2020-12/schema"
$id: "https://ex.test/leaf.yaml"
title: Leaf
type: string
minLength: 3
`

// A JSON body under a YAML extension: legal YAML, and the file that showed the
// two entry points disagreeing. It was refused as an input and generated from
// happily as a $ref target, so the document schemagen said it could not read was
// read and its constraints enforced.
const jsonBodiedLeafYAML = `{"$schema": "https://json-schema.org/draft/2020-12/schema",
 "$id": "https://ex.test/leafjson.yaml", "title": "Leaf", "type": "string", "minLength": 3}`

const refToLeafTemplate = `{"$schema": "https://json-schema.org/draft/2020-12/schema",
 "title": "Root", "type": "object", "properties": {"a": {"$ref": %q}}}`

func TestAYAMLFileIsRefusedAsAnInputAndAsARefTarget(t *testing.T) {
	const refusal = "YAML schema files are not yet supported"

	for _, tc := range []struct {
		name string
		leaf string
		body string
	}{
		{"JSON body under a .yaml extension", "leafjson.yaml", jsonBodiedLeafYAML},
		{"a genuine YAML document", "leaf.yaml", yamlBodiedLeaf},
		{"a genuine YAML document under .yml", "leaf.yml", yamlBodiedLeaf},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSchemaFile(t, dir, tc.leaf, tc.body)
			writeSchemaFile(t, dir, "root.json", strings.Replace(refToLeafTemplate, "%q", `"`+tc.leaf+`"`, 1))

			asInput := runFailingIn(t, dir, "generate", tc.leaf, "-p", "models", "-o", "out1")
			if got := countLinesContaining(asInput, refusal); got != 1 {
				t.Errorf("as an input, the refusal appears on %d lines, want 1:\n%s", got, asInput)
			}

			// The same file reached the other way. This is the half that did not
			// refuse: the JSON-bodied document generated cleanly, and the real
			// YAML one failed at a character offset in a JSON parse.
			asRef := runFailingIn(t, dir, "generate", "root.json", "-p", "models", "-o", "out2")
			if got := countLinesContaining(asRef, refusal); got != 1 {
				t.Errorf("reached by $ref, the refusal appears on %d lines, want 1:\n%s", got, asRef)
			}
			if strings.Contains(asRef, "looking for beginning of value") {
				t.Errorf("a YAML document is reported as a JSON parse failure:\n%s", asRef)
			}
			// The advice that is false here: passing the document as an input is
			// what the first run above just did, and it produced the same answer.
			if strings.Contains(asRef, "pass the referenced document as an input too") {
				t.Errorf("stderr advises supplying a document whose format is the problem:\n%s", asRef)
			}
			if strings.Contains(asRef, "--allow-remote-refs fetches") {
				t.Errorf("stderr advises fetching a local file over the network:\n%s", asRef)
			}
			if !strings.Contains(asRef, "convert the document to JSON") {
				t.Errorf("stderr does not carry the advice that is true here:\n%s", asRef)
			}
			// No output for a document that was refused.
			if _, err := os.Stat(filepath.Join(dir, "out2", "root.go")); err == nil {
				t.Error("a run that refused the referenced document still wrote its output file")
			}
		})
	}
}

// The control: only the two YAML extensions are refused. An unknown extension is
// parsed as JSON from both entry points, which is what the loader's default arm
// has always done and what README states.
func TestAnUnknownExtensionIsStillReadFromBothEntryPoints(t *testing.T) {
	dir := t.TempDir()
	writeSchemaFile(t, dir, "leaf.txt", jsonBodiedLeafYAML)
	writeSchemaFile(t, dir, "root.json", strings.Replace(refToLeafTemplate, "%q", `"leaf.txt"`, 1))

	if stderr, err := runIn(t, dir, "generate", "leaf.txt", "-p", "models", "-o", "out1"); err != nil {
		t.Errorf("a .txt file holding JSON was refused as an input: %v\nstderr:\n%s", err, stderr)
	}
	if stderr, err := runIn(t, dir, "generate", "root.json", "-p", "models", "-o", "out2"); err != nil {
		t.Errorf("a .txt file holding JSON was refused as a $ref target: %v\nstderr:\n%s", err, stderr)
	}
}

// ---------- issue #331 ----------

const refReachedLeaf = `{"$schema": "https://json-schema.org/draft/2020-12/schema",
 "$id": "https://ex.test/leaf.json", "title": "Leaf", "type": "object",
 "properties": {"first_name": {"type": "string", "minLength": 3}}}`

const refReachedRoot = `{"$schema": "https://json-schema.org/draft/2020-12/schema",
 "$id": "https://ex.test/root.json", "title": "Root", "type": "object",
 "properties": {"who": {"$ref": "leaf.json"}}}`

// --root-name naming a document the run reached by $ref sets the prefix that
// document's definitions are qualified with, and does not name its root type.
// With nothing contested there is nothing to qualify either, so the key had no
// effect at all -- and was accepted in silence, because it had been consulted
// and so counted as used. A key naming no document is reported; this one, naming
// a document the run really read and not doing what its help says, was not.
func TestARootNameKeyThatOnlyReachedARefTargetSaysSo(t *testing.T) {
	const inert = "which this run reached by $ref rather than being given as an input"

	for _, key := range []string{
		"id:https://ex.test/leaf.json=PinnedLeaf",
		"file:leaf.json=PinnedLeaf",
		"leaf.json=PinnedLeaf",
	} {
		t.Run(key, func(t *testing.T) {
			dir := t.TempDir()
			writeSchemaFile(t, dir, "leaf.json", refReachedLeaf)
			writeSchemaFile(t, dir, "root.json", refReachedRoot)

			stderr, err := runIn(t, dir, "generate", "root.json", "--root-name", key, "-p", "models", "-o", "out")
			if err != nil {
				t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
			}
			if got := countLinesContaining(stderr, inert); got != 1 {
				t.Errorf("the key is reported on %d lines, want 1:\n%s", got, stderr)
			}
			// The behaviour is unchanged: the name asked for is still not the
			// root type's, which is what the warning says.
			src, readErr := os.ReadFile(filepath.Join(dir, "out", "root.go"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !strings.Contains(string(src), "type Leaf struct") {
				t.Errorf("the referenced document's root type should still be named from its title:\n%s", src)
			}
			if strings.Contains(string(src), "type PinnedLeaf") {
				t.Errorf("--root-name named a $ref-reached document's root type, which the warning says it does not:\n%s", src)
			}
		})
	}
}

// The config spelling of the same key, named as the config's: "--root-name"
// would send the reader to a command line that does not contain it, which is the
// rule every other report in this family already follows.
func TestAConfigRootNameThatOnlyReachedARefTargetSaysSo(t *testing.T) {
	dir := t.TempDir()
	writeSchemaFile(t, dir, "leaf.json", refReachedLeaf)
	writeSchemaFile(t, dir, "root.json", refReachedRoot)
	writeSchemaFile(t, dir, "cfg.json", `{"package": "models", "outputDir": "out",
		"documents": [
			{"path": "root.json", "rootName": "Root"},
			{"id": "https://ex.test/leaf.json", "rootName": "PinnedLeaf"}
		]}`)

	stderr, err := runIn(t, dir, "generate", "--config", "cfg.json")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	const want = `config rootName for "https://ex.test/leaf.json" names leaf.json, which this run reached by $ref`
	if got := countLinesContaining(stderr, want); got != 1 {
		t.Errorf("the entry is reported on %d lines, want 1:\n%s", got, stderr)
	}
	if strings.Contains(stderr, `--root-name "https://ex.test/leaf.json"`) {
		t.Errorf("a config entry is reported as a flag the command line does not carry:\n%s", stderr)
	}
}

// The control, and the reason the warning is conditioned on effect rather than
// on the key's shape. Here two referenced documents contest the Go type name
// Item, the key supplies the prefix that separates them, and the caller can see
// it in the contest diagnostic. Saying "it had no effect" there would be false,
// and saying anything at all would be a line printed on every run of a working
// configuration.
func TestARootNameKeyThatQualifiedAContestedNameIsNotReported(t *testing.T) {
	dir := t.TempDir()
	writeSchemaFile(t, dir, "alpha.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/alpha.json",
		"$defs": {"Item": {"type": "object", "properties": {"a": {"type": "string"}}, "required": ["a"]}}
	}`)
	writeSchemaFile(t, dir, "beta.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/beta.json",
		"$defs": {"Item": {"type": "integer"}}
	}`)
	writeSchemaFile(t, dir, "root.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/root.json", "title": "Root", "type": "object",
		"properties": {"a": {"$ref": "alpha.json#/$defs/Item"}, "b": {"$ref": "beta.json#/$defs/Item"}}
	}`)

	stderr, err := runIn(t, dir, "generate", "root.json",
		"--root-name", "id:https://ex.test/alpha.json=Alfa", "-p", "models", "-o", "out")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	// The key did something, and the contest diagnostic is where it is visible.
	if got := countLinesContaining(stderr, "$defs/Item becomes AlfaItem"); got != 1 {
		t.Errorf("the key's effect appears on %d lines of the contest diagnostic, want 1:\n%s", got, stderr)
	}
	if got := countLinesContaining(stderr, "which this run reached by $ref rather than being given as an input"); got != 0 {
		t.Errorf("a key that qualified a contested name is reported as having had no effect on %d lines, want 0:\n%s", got, stderr)
	}
}
