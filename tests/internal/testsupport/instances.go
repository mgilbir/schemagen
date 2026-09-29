package testsupport

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/generator"
)

// NotInstance is one document put to a generated type, and the verdict the
// schema gives it.
type NotInstance struct {
	Name  string
	Doc   string
	Valid bool
	// Why says what the case is for, and is printed on failure. A verdict with
	// no reason attached is one the next reader has to re-derive from the schema.
	Why string
}

// NotFixture is one schema, named relative to the repository root, and the
// documents put to the type it generates.
type NotFixture struct {
	Name       string
	SchemaPath string
	Instances  []NotInstance
}

// RunInstanceFixtures compiles each fixture's schema and puts every document in
// it to the generated root type, reporting the ones whose verdict disagrees.
//
// One runner for every group of fixtures written this way. The three that exist
// differed only in the module name written into the throwaway go.mod, and a
// fourth copy is how the next one starts drifting from the rest.
//
// module names the temporary module the compiled program lives in. It has no
// effect on the verdict and exists so that a failure names which group's
// throwaway directory it came from.
func RunInstanceFixtures(t *testing.T, module string, fixtures []NotFixture) {
	t.Helper()
	RunInstanceFixturesWithConfig(t, module, fixtures, generator.Config{
		PackageName:  "testpkg",
		OmitEmpty:    true,
		RootTypeName: "Root",
	})
}

// RunInstanceFixturesWithConfig is the same runner under a stated generator
// configuration, for a defect that only exists under one. A big-int wrapper is
// the clearest case: under the default configuration the same schema comes out a
// plain int64 alias, so a group written for that wrapper has to name the flag or
// it exercises a different type entirely.
//
// PackageName and RootTypeName are the caller's to set, and both are load
// bearing: the package rename below looks for "package testpkg", and the
// document is put to a type named Root rather than to one recovered from the
// emitted source.
func RunInstanceFixturesWithConfig(t *testing.T, module string, fixtures []NotFixture, cfg generator.Config) {
	t.Helper()
	for _, fx := range fixtures {
		t.Run(fx.Name, func(t *testing.T) {
			// The root type is named rather than recovered from the emitted
			// source. extractRootTypeName looks for the last top-level struct,
			// and a root that is a slice or a map does not declare one -- it
			// answered with the *element* wrapper for the items fixture here,
			// which put every document to the wrong type and reported a control
			// case failing for a reason that had nothing to do with the schema.
			generated := GenerateFromSchemaWithConfig(t, fx.SchemaPath, cfg)
			const rootType = "Root"

			tmpDir := t.TempDir()
			generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
			if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
				t.Fatalf("writing types.go: %v", err)
			}
			WriteSharedHelpers(t, tmpDir, generatedMain)

			mainGo, err := NotInstanceMain(rootType, fx.Instances)
			if err != nil {
				t.Fatalf("building main.go: %v", err)
			}
			if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
				t.Fatalf("writing main.go: %v", err)
			}
			if err := WriteTestGoMod(tmpDir, module); err != nil {
				t.Fatalf("writing go.mod: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := testgo.Command(ctx, tmpDir, "run", ".")
			out, runErr := cmd.CombinedOutput()
			text := ProgramOutput(out)
			if runErr != nil || text != "PASS" {
				t.Fatalf("%s:\n%s", fx.SchemaPath, text)
			}
		})
	}
}

// NotInstanceMain writes the program that puts each document to the type.
//
// The Validate call goes through a type assertion rather than a direct call
// because a schema schemagen cannot compile resolves to `type X any`, which Go
// forbids methods on. A direct call would not compile, and a fixture group would
// then fail to build rather than report which document it disagrees about --
// and the `format` group is exactly such a schema, deliberately.
func NotInstanceMain(rootType string, instances []NotInstance) (string, error) {
	var b strings.Builder
	b.WriteString(`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type notCase struct {
	name  string
	doc   string
	valid bool
	why   string
}

func main() {
	cases := []notCase{
`)
	for _, in := range instances {
		if !json.Valid([]byte(in.Doc)) {
			return "", fmt.Errorf("case %q: %s is not valid JSON", in.Name, in.Doc)
		}
		fmt.Fprintf(&b, "\t\t{name: %s, doc: %s, valid: %t, why: %s},\n",
			GoQuote(in.Name), GoQuote(in.Doc), in.Valid, GoQuote(in.Why))
	}
	fmt.Fprintf(&b, `	}

	var errs []string
	for _, c := range cases {
		var v %s
		err := json.Unmarshal([]byte(c.doc), &v)
		if err == nil {
			if val, ok := any(v).(interface{ Validate() error }); ok {
				err = val.Validate()
			}
		}
		accepted := err == nil
		if accepted != c.valid {
			errs = append(errs, fmt.Sprintf("%%s: %%s accepted=%%v want=%%v err=%%v (%%s)",
				c.name, c.doc, accepted, c.valid, err, c.why))
		}
	}
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "FAIL: %%s\n", e)
		}
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`, rootType)
	return b.String(), nil
}

// GoQuote renders s as a Go string literal. strconv.Quote would do, but the
// documents here are JSON and reading a doubly-escaped one in a failure message
// is what this avoids: a raw string literal keeps them legible, and the fallback
// is only reached by a document containing a backquote.
func GoQuote(s string) string {
	if !strings.ContainsAny(s, "`\n\r") {
		return "`" + s + "`"
	}
	return fmt.Sprintf("%q", s)
}
