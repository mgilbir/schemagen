package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// numberNamedType matches a generated declaration of a named type over
// json.Number: `type Temperature json.Number`.
var numberNamedType = regexp.MustCompile(`(?m)^type (\w+) json\.Number$`)

// numberDeclaration matches json.Number where it is a *declared type* rather
// than a conversion or a local: a named type over it, a struct field, and the
// three container shapes a field can hold it in. The bare name is no use as a
// signal -- the integer decode names json.Number inside its own UnmarshalJSON
// under every configuration, and so does the helper block.
var numberDeclaration = regexp.MustCompile(`(?m)^type \w+ json\.Number$|\[\]json\.Number|map\[string\]json\.Number|^\t[A-Z]\w* +\*?json\.Number\b`)

// TestExactNumberNamedTypesCarryBothDirections holds every named type over
// json.Number to having an UnmarshalJSON and a MarshalJSON of its own.
//
// A defined type inherits none of its underlying type's methods, and
// json.Number's whole behaviour is in its methods. Without an UnmarshalJSON,
// `type Temperature json.Number` is a plain Go string to encoding/json and
// refuses to decode a number at all -- loud, and caught by any round trip.
// Without a MarshalJSON it is a plain Go string in the other direction too, and
// that half is silent: the value leaves as "1.5" instead of 1.5, a document
// nobody sent, with no error anywhere and nothing but a byte comparison to
// notice. That asymmetry is why this is a check over the whole corpus rather
// than a fixture or two.
//
// It reads the emitted source rather than the IR for the reason
// HelpersReferencedBy does: the question is exactly what the generated file
// declares, so it cannot drift from a walk that forgot a kind of type
// definition. Every kind that can be declared over json.Number is covered by
// construction -- an alias, an enum, and anything later -- because the pattern
// is the declaration itself.
func TestExactNumberNamedTypesCarryBothDirections(t *testing.T) {
	paths := corpusSchemaPaths(t)
	if len(paths) < 100 {
		t.Fatalf("corpus walk found only %d schemas; the sweep is measuring nothing", len(paths))
	}
	declared := 0
	for _, path := range paths {
		src, ok := generateExactOrSkip(t, path)
		if !ok {
			continue
		}
		for _, m := range numberNamedType.FindAllStringSubmatch(src, -1) {
			name := m[1]
			declared++
			for _, want := range []string{
				fmt.Sprintf(" *%s) UnmarshalJSON(", name),
				fmt.Sprintf(" %s) MarshalJSON(", name),
			} {
				if !strings.Contains(src, want) {
					t.Errorf("%s: type %s json.Number has no %s -- a named type over json.Number "+
						"inherits neither direction, and without MarshalJSON it encodes as a JSON string",
						path, name, strings.TrimSuffix(want, "("))
				}
			}
		}
	}
	// The check is only worth anything if the corpus actually declares such
	// types. It does -- $defs entries typed "number", and enums over one.
	if declared == 0 {
		t.Fatal("no named type over json.Number in the whole corpus: this test is watching nothing")
	}
	t.Logf("checked %d named types over json.Number across %d schemas", declared, len(paths))
}

// TestExactNumberFieldsDecodeThroughTheShadow holds every declared json.Number
// to being decoded through jsonNumber rather than filled directly.
//
// json.Number is a string underneath and encoding/json will fill one from a
// JSON string, so {"n":"1.5"} would satisfy {"type":"number"} -- a document the
// schema forbids, accepted and then written back out unquoted. The float64 this
// replaces refused it for free. The shadow is what refuses it here, and a
// position that acquired the type without the decode would accept it in
// silence.
func TestExactNumberFieldsDecodeThroughTheShadow(t *testing.T) {
	checked := 0
	for _, path := range corpusSchemaPaths(t) {
		src, ok := generateExactOrSkip(t, path)
		if !ok || !numberDeclaration.MatchString(src) {
			continue
		}
		checked++
		if !strings.Contains(src, "jsonNumber") {
			t.Errorf("%s: declares a json.Number and never names the jsonNumber shadow, so "+
				"encoding/json fills it directly and a JSON string is taken for a number", path)
		}
	}
	if checked == 0 {
		t.Fatal("no schema in the corpus produced a json.Number: this test is watching nothing")
	}
	t.Logf("checked %d schemas that declare a json.Number", checked)
}

// corpusSchemaPaths lists every schema document in testdata/schemas.
func corpusSchemaPaths(t *testing.T) []string {
	t.Helper()
	var paths []string
	root := filepath.Join("..", "testdata", "schemas")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".json") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return paths
}

// generateExactOrSkip runs one schema through the pipeline under
// --exact-numbers, and reports ok=false for a document this generator declines
// to generate at all.
//
// A declined document is not a failure here: the corpus holds schemas written
// to be refused -- an unresolvable $ref, a cycle no type can express -- and
// which those are is the business of the tests that measure it. What matters to
// this sweep is that every document it *does* generate obeys the rules above.
func generateExactOrSkip(t *testing.T, path string) (string, bool) {
	t.Helper()
	s, err := schema.LoadFromFile(path)
	if err != nil {
		return "", false
	}
	s.NormalizeForDraft(schema.DraftUnknown)
	gen := generator.New(generator.Config{
		PackageName:  "testpkg",
		OmitEmpty:    true,
		ExactNumbers: true,
	})
	ir, err := gen.Generate(s)
	if err != nil {
		return "", false
	}
	em, err := emitter.New()
	if err != nil {
		t.Fatalf("creating emitter: %v", err)
	}
	src, err := em.Emit(ir)
	if err != nil {
		return "", false
	}
	return string(src), true
}
