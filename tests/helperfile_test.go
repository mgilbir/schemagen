package tests

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
// Deriving it from the source keeps every call site free of extra plumbing --
// and several call sites have only the text, having read a golden file rather
// than generated it, so there is no IR to ask.
//
// The _dyn* family is matched by its prefix and pulled in whole. It used to be
// a list of individual helper names, which had to be kept in step with the
// templates by hand and silently was not: when _dynConstOK joined the family
// and the list did not learn about it, every generated file that called it
// failed to compile here -- 121 subtests of the external suite, reported as a
// generator defect rather than as the harness gap it was. The asymmetry is the
// whole argument. Over-including leaves an unused function in a throwaway file
// that is compiled once and deleted; under-including breaks the build, and
// does it in the one place set up to look like the code generator is at fault.
func sharedHelpersFor(content string) generator.HelperSet {
	var set generator.HelperSet
	if strings.Contains(content, "oneofHasRequiredFields(") {
		set.OneOf = true
	}
	if strings.Contains(content, "oneofDiscriminatorValue(") {
		set.OneOfDiscriminator = true
	}
	if strings.Contains(content, "_dyn") {
		set.Dynamic = true
		set.DynamicConst = true
	}
	// The annotation evaluator calls the _dyn* predicates, so it pulls both in.
	if strings.Contains(content, "_schemaNode") || strings.Contains(content, "_evalNode(") {
		set.Annotations = true
		set.Dynamic = true
	}
	// jsonInteger and its three container rebuilders come as one block, and the
	// name appears in every use of any of them -- the shadow field's type, the
	// closure parameter of a rebuilder, the conversion at the leaf -- so one
	// substring pulls the whole family in.
	if strings.Contains(content, "jsonInteger") {
		set.Integer = true
	}
	return set
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
