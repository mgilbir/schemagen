package testsupport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
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

// WriteSharedHelpersErr writes the schemagen_helpers.go companion next to a
// generated file when that file references shared helpers.
//
// Helpers live in one file per destination package rather than in every file
// that needs them, so a package containing two schemas that need the same
// helper still compiles. These harnesses compile a single generated file in
// isolation, so they supply the companion themselves.
func WriteSharedHelpersErr(dir, content string) error {
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

// WriteSharedHelpers is the testing.T-flavoured wrapper.
func WriteSharedHelpers(t *testing.T, dir, content string) {
	t.Helper()
	if err := WriteSharedHelpersErr(dir, content); err != nil {
		t.Fatalf("writing shared helper file: %v", err)
	}
}
