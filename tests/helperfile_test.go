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
// Deriving it from the source keeps every call site free of extra plumbing.
func sharedHelpersFor(content string) generator.HelperSet {
	var set generator.HelperSet
	if strings.Contains(content, "oneofHasRequiredFields(") {
		set.OneOf = true
	}
	if strings.Contains(content, "oneofDiscriminatorValue(") {
		set.OneOfDiscriminator = true
	}
	for _, ref := range []string{"_dynNumber(", "_dynIs", "_dynNumOK(", "_dynStrOK("} {
		if strings.Contains(content, ref) {
			set.Dynamic = true
			break
		}
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
