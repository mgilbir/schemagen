package tests

import (
	"context"
	"encoding/json"

	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// canonicalAgreementDocuments are the documents both canonicalisers are asked
// about.
//
// Weighted towards numbers, because that is where the two could disagree
// without anything else noticing: the container and string arms are a few lines
// each and go through encoding/json on both sides, while the number arm is a
// hand-written decimal parser and formatter written twice.
var canonicalAgreementDocuments = []string{
	`null`, `true`, `false`, `"a"`, `"a<b&c>d"`, `""`, `"é"`,
	`[]`, `{}`, `[1,2,3]`, `{"b":2,"a":1}`, `{"k":{"y":1,"x":2}}`, `[[[1]]]`,
	`0`, `-0`, `0.0`, `-0.0`, `0e100`,
	`1`, `1.0`, `1e0`, `0.1e1`, `1.00`, `10e-1`, `1E0`,
	`-1`, `-1.5`, `1.5`, `1.50`, `15e-1`,
	`100`, `1e2`, `0.1e3`,
	`1e20`, `1e21`, `1e22`, `100000000000000000000`, `1000000000000000000000`,
	`1e-6`, `1e-7`, `0.000001`, `0.0000001`, `1.5e-7`,
	`5e-324`, `1e308`, `-1e308`, `1.7976931348623157e308`,
	`123456789012345678901234567890`, `123456789012345678901234567891`,
	`12345678901234567890123456789e1`,
	`0.1`, `0.2`, `0.30000000000000004`, `9007199254740993`, `9223372036854775807`,
	`{"n":[1.50,1e2,123456789012345678901234567890]}`,
	`1e-400`, `1e400`, `1e5000`,
	// Past the exponent either side will read. Both refuse, and the answer both
	// give for a refusal is the literal as written -- which is what makes a
	// refusal safe: a number can then only ever compare equal to itself.
	`1e99999999999999`, `-1e-99999999999999`,
	// Not JSON at all. Nothing a schema states arrives here looking like this,
	// but the baked list is data written by another program, and the arm
	// _jsonCanonicalTexts takes for a literal it cannot read is the one that
	// keeps such an entry matching itself instead of matching everything.
	`not json`,
}

// TestEmittedCanonicaliserAgreesWithTheGenerator compiles the _jsonCanonical
// helper block and checks it answers what schema.CanonicalJSON answers.
//
// There are two implementations of one reduction and there have to be: the
// generated code is standalone Go that does not import this repository, and the
// generator needs the same reduction to deduplicate an enum's members before it
// names them. They are separated by a template, so nothing but a test can hold
// them together -- and the two failure directions are both silent. If the
// emitted one reduces more than this one, a dropped enum member becomes a
// document the schema admits and the generated code refuses. If it reduces
// less, two members that are one value stay two and the generated code accepts
// what the deduplication removed.
//
// The helper block is compiled rather than read, because what is under test is
// what the emitted code does and not what its source looks like.
func TestEmittedCanonicaliserAgreesWithTheGenerator(t *testing.T) {
	em, err := emitter.New()
	if err != nil {
		t.Fatalf("emitter.New: %v", err)
	}
	helpers, ok, err := em.EmitHelpers("main", generator.HelperSet{Canonical: true})
	if err != nil {
		t.Fatalf("emitting the canonical helper block: %v", err)
	}
	if !ok {
		t.Fatal("the canonical helper set emitted no file, so this test would compile nothing")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "helpers.go"), helpers, 0o644); err != nil {
		t.Fatal(err)
	}
	docs, err := json.Marshal(canonicalAgreementDocuments)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs.json"), docs, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(canonicalAgreementMain), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeTestGoMod(dir, "canonical_agreement_test"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = dir
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("running the emitted canonicaliser: %v\n%s", runErr, out)
	}

	var got []string
	if err := json.Unmarshal([]byte(programOutput(out)), &got); err != nil {
		t.Fatalf("reading the emitted canonicaliser's answers (%q): %v", programOutput(out), err)
	}
	if len(got) != len(canonicalAgreementDocuments) {
		t.Fatalf("got %d answers for %d documents", len(got), len(canonicalAgreementDocuments))
	}

	agreed, fellBack := 0, 0
	for i, doc := range canonicalAgreementDocuments {
		var v any
		dec := json.NewDecoder(strings.NewReader(doc))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			// Not a JSON document at all, so there is nothing to reduce and the
			// entry has to survive as itself: an entry the reduction replaced
			// with an empty string would match every document that also failed
			// to reduce.
			if got[i] != doc {
				t.Errorf("%s is not a JSON document, and the emitted canonicaliser answered %s rather than keeping it", doc, got[i])
			}
			fellBack++
			continue
		}
		want, ok := schema.CanonicalJSON(v)
		if !ok {
			// A document neither side reduces. The fallback has to be the same
			// fallback, or the two would disagree exactly where neither can
			// check the other.
			if got[i] != doc {
				t.Errorf("%s: neither canonicaliser reduces this, but the emitted one answered %s rather than the literal", doc, got[i])
			}
			fellBack++
			continue
		}
		if got[i] != want {
			t.Errorf("%s: the emitted canonicaliser answers %s, schema.CanonicalJSON answers %s", doc, got[i], want)
			continue
		}
		agreed++
	}
	if agreed == 0 {
		t.Fatal("no document was compared: this test is watching nothing")
	}
	if fellBack == 0 {
		t.Error("no document reached the fallback either canonicaliser takes for a literal it cannot read, so that arm is untested")
	}
	t.Logf("%d documents reduced and compared, %d fell back to the literal", agreed, fellBack)
}

// canonicalAgreementMain reads the documents and prints what _jsonCanonical
// makes of each, as a JSON array so that the answers cannot be confused with
// each other by a newline inside one.
const canonicalAgreementMain = `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	raw, err := os.ReadFile("docs.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var docs []string
	if err := json.Unmarshal(raw, &docs); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Through _jsonCanonicalTexts rather than through _jsonCanonical directly:
	// that is the entry point the baked member list of every enum and const
	// goes through, so it is the one whose answers have to be checked -- both
	// the reduction and the arm it takes for a literal it cannot read at all.
	out := _jsonCanonicalTexts(docs)
	enc, err := json.Marshal(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(enc))
}
`
