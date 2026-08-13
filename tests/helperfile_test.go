package tests

import (
	"encoding/json"
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

// sharedHelpersFor derives which shared helpers a generated file references.
//
// It is the generator's own function, and calling it rather than reimplementing
// it is the point. This harness used to carry a copy, which meant every compile
// test in the repository proved the *copy* right and never asked what the
// generator would have decided. The generator decided by walking the IR and
// naming the fields a rule can live in; it did not name ItemValidations, so a
// format on an array element or a map value emitted a call to a function the
// helper file never declared -- and no test could see it, because this harness
// wrote the helper file the generator should have written. There is one
// implementation now, so a compile test proves the shipped answer.
func sharedHelpersFor(content string) generator.HelperSet {
	return generator.HelpersReferencedBy(content)
}

// packageNameOf reads the package clause from generated source, so the helper
// file lands in the same package as the file it accompanies.
func packageNameOf(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "package "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return "main"
}

// writeSharedHelpersErr writes the schemagen_helpers.go companion next to a
// generated file when that file references shared helpers.
//
// Helpers live in one file per destination package rather than in every file
// that needs them, so a package containing two schemas that need the same
// helper still compiles. These harnesses compile a single generated file in
// isolation, so they supply the companion themselves.
func writeSharedHelpersErr(dir, content string) error {
	set := sharedHelpersFor(content)
	if set.Empty() {
		return nil
	}
	em, err := emitter.New()
	if err != nil {
		return fmt.Errorf("creating emitter for helpers: %w", err)
	}
	src, needed, err := em.EmitHelpers(packageNameOf(content), set)
	if err != nil {
		return fmt.Errorf("emitting helpers: %w", err)
	}
	if !needed {
		return nil
	}
	return os.WriteFile(filepath.Join(dir, "schemagen_helpers.go"), src, 0o644)
}

// writeSharedHelpers is the testing.T-flavoured wrapper.
func writeSharedHelpers(t *testing.T, dir, content string) {
	t.Helper()
	if err := writeSharedHelpersErr(dir, content); err != nil {
		t.Fatalf("writing shared helper file: %v", err)
	}
}

// helperCallPattern matches a call to any shared helper the generated code can
// make. The families are matched by prefix rather than by name so that a helper
// added to a block is covered the day it is written -- a list of names is what
// failed on PR #59 and again on the format block.
var helperCallPattern = regexp.MustCompile(`\b(schemagenFormat\w*|schemagen[A-Z]\w*|oneofHasRequiredFields|oneofDiscriminatorValue|jsonInteger\w*|jsonExactProperties|jsonDecode\w*|jsonValueErrorf|jsonElemErrorf|jsonPathf|jsonElemPathf|checkJSONNullsAt|_dyn\w*|_schemaNode|_evalNode)\(`)

// TestHelperFileDeclaresEveryHelperCalled compiles the one claim the helper file
// has to satisfy: everything the generated code calls, it declares.
//
// This is the guard that was missing. Which helpers a package needs used to be
// decided by walking the IR and naming the fields a helper-backed rule can live
// in; ItemValidations was not named, so a `format` on an array element or a map
// value emitted the call and never the function, and the generated package did
// not compile. Every harness in this repository derived the set from the emitted
// source instead, so every harness wrote the right helper file and none of them
// asked what the generator would have written. The two answers have been one
// function since, and this walks every fixture in the tree through it.
//
// It reads the emitted source rather than a golden, so a fixture that is not
// pinned as a golden is covered too, and both format postures are exercised:
// which checks are emitted depends on the draft, so a fixture that annotates on
// its own dialect is generated a second time with assertion forced.
func TestHelperFileDeclaresEveryHelperCalled(t *testing.T) {
	schemaFiles := allRegressionSchemas(t)
	if len(schemaFiles) == 0 {
		t.Fatal("no schemas found to check")
	}

	em, err := emitter.New()
	if err != nil {
		t.Fatalf("creating emitter: %v", err)
	}

	for _, path := range schemaFiles {
		for _, cfg := range []struct {
			name    string
			asserts bool
		}{{"dialect", false}, {"format-assertion", true}} {
			t.Run(filepath.Base(path)+"/"+cfg.name, func(t *testing.T) {
				s, err := schema.LoadFromFile(path)
				if err != nil {
					t.Skipf("not loadable: %v", err)
				}
				s.Normalize()
				gen := generator.New(generator.Config{
					PackageName:     "testpkg",
					OmitEmpty:       true,
					FormatAssertion: cfg.asserts,
				})
				ir, err := gen.Generate(s)
				if err != nil {
					t.Skipf("not generatable: %v", err)
				}
				src, err := em.Emit(ir)
				if err != nil {
					t.Skipf("not emittable: %v", err)
				}

				helperSrc, needed, err := em.EmitHelpers("testpkg", generator.HelpersReferencedBy(string(src)))
				if err != nil {
					t.Fatalf("emitting helpers: %v", err)
				}
				declared := ""
				if needed {
					declared = string(helperSrc)
				}

				// The helper file is held to the same claim as the generated
				// types, and against itself as well as against them. Which
				// helpers a package needs is read from what the *types* call,
				// and a call from one helper block to another appears in no
				// types file at all -- the null walker's refusal is built by
				// jsonValueErrorf, and a schema with one string property names
				// the walker and never the constructor. See
				// HelperSet.CloseOverCalls and issue #282.
				for _, src := range []string{string(src), declared} {
					for _, m := range helperCallPattern.FindAllStringSubmatch(src, -1) {
						name := m[1]
						if !declaresFunc(declared, name) {
							t.Errorf("%s calls %s, which the helper file does not declare", filepath.Base(path), name)
						}
					}
				}
			})
		}
	}
}

// TestHelperFileDeclaresWhatItsOwnBlocksCall is the same claim for a call made
// inside the helper file, which is a hole the fixture walk above cannot see.
//
// Which helpers a package needs is read from what the generated *types* call,
// and a call from one helper block to another appears in no types file at all.
// The blocks that refuse a document at decode time -- the null walker, the two
// numeric shadows, the date-time and ip shadows, the decode trace -- all build
// their refusal with the path-join constructors, and a schema can reach any of
// them while naming none of those constructors itself. See
// HelperSet.CloseOverCalls and issue #282.
//
// The schemas below are the smallest that do it, and they are inline rather than
// corpus fixtures because that is the property being pinned: `{"properties":
// {"b":{}}}` names jsonDecodeMemberError and nothing else at all. Every corpus
// schema outside the adversarial set happens to name a constructor for some
// other reason, so the fixture walk passes with the closure removed.
func TestHelperFileDeclaresWhatItsOwnBlocksCall(t *testing.T) {
	em, err := emitter.New()
	if err != nil {
		t.Fatalf("creating emitter: %v", err)
	}
	for _, tc := range []struct {
		name   string
		schema string
		why    string
	}{
		{"one untyped property", `{"properties":{"b":{}}}`,
			"the types file names jsonDecodeMemberError and no other helper"},
		{"a nullable property", `{"properties":{"b":{"type":["string","null"]}}}`,
			"the same, with the null admitted so no null rule is emitted either"},
		{"a container alias", `{"type":"array","items":{"type":"string"}}`,
			"the null walker is reached from the alias template, which has no path to put in front of it"},
		{"an integer property", `{"properties":{"n":{"type":"integer"}}}`,
			"the integer shadow"},
		{"a date-time property", `{"properties":{"d":{"type":"string","format":"date-time"}}}`,
			"the date-time shadow"},
		{"an ipv4 property", `{"properties":{"a":{"type":"string","format":"ipv4"}}}`,
			"the ip shadows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s schema.Schema
			if err := json.Unmarshal([]byte(tc.schema), &s); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			s.Normalize()
			ir, err := generator.New(generator.Config{
				PackageName:     "testpkg",
				OmitEmpty:       true,
				FormatAssertion: true,
			}).Generate(&s)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			src, err := em.Emit(ir)
			if err != nil {
				t.Fatalf("emit: %v", err)
			}
			helperSrc, needed, err := em.EmitHelpers("testpkg", generator.HelpersReferencedBy(string(src)))
			if err != nil {
				t.Fatalf("emitting helpers: %v", err)
			}
			declared := ""
			if needed {
				declared = string(helperSrc)
			}
			for _, text := range []string{string(src), declared} {
				for _, m := range helperCallPattern.FindAllStringSubmatch(text, -1) {
					if !declaresFunc(declared, m[1]) {
						t.Errorf("%s: %s is called and not declared (%s)", tc.name, m[1], tc.why)
					}
				}
			}
		})
	}
}

// declaresFunc reports whether src declares this name. The type parameter list
// is optional: three of the integer rebuilders are generic, and matching only
// "func name(" reported them as undeclared when they were right there.
//
// A type of the same name counts, because the pattern above cannot tell a call
// from a conversion and does not need to: `jsonInteger(_i)` and `jsonIPv4Addr(_a)`
// are conversions to the shadows the same block declares, and what is being
// asked either way is whether the file the name appears in has it.
func declaresFunc(src, name string) bool {
	q := regexp.QuoteMeta(name)
	return regexp.MustCompile(`func\s+`+q+`\s*[\[(]`).MatchString(src) ||
		regexp.MustCompile(`(?m)^type\s+`+q+`\b`).MatchString(src)
}

// allRegressionSchemas lists every schema fixture in the tree, which is the
// population this guard has to cover: a fixture that is not a golden is still a
// schema someone can hand the CLI.
func allRegressionSchemas(t *testing.T) []string {
	t.Helper()
	var out []string
	root := filepath.Join("..", "testdata", "schemas")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}
		// The adversarial corpus is deliberately malformed; whether it generates
		// at all is FuzzGenerate's business, not this test's.
		if strings.Contains(filepath.ToSlash(path), "/adversarial/") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking schemas: %v", err)
	}
	return out
}
