package generator

import (
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// twoDocs returns a document owning a definition and a second document
// referencing it, both prepared the way a multi-package run prepares inputs.
func twoDocs(t *testing.T) (owner, referrer *schema.Schema) {
	t.Helper()
	owner = &schema.Schema{
		ID:    "https://ex.test/a.json",
		Title: "ADoc",
		Type:  schema.TypeList{"object"},
		Definitions: map[string]*schema.Schema{
			"widget": {
				Type:       schema.TypeList{"object"},
				Properties: map[string]*schema.Schema{"size": {Type: schema.TypeList{"integer"}}},
			},
		},
	}
	referrer = &schema.Schema{
		ID:    "https://ex.test/b.json",
		Title: "BDoc",
		Type:  schema.TypeList{"object"},
		Properties: map[string]*schema.Schema{
			"w": {Ref: "https://ex.test/a.json#/definitions/widget"},
		},
	}
	for _, s := range []*schema.Schema{owner, referrer} {
		s.Normalize()
		s.ComputeBaseURIs(nil, s)
	}
	return owner, referrer
}

// A $ref into a document owned by another package of the run must not silently
// materialize a local copy when that package never registered the type: the two
// packages would then expose incompatible Go types for one JSON shape.
func TestGenerateFailsWhenForeignTypeWasNotRegistered(t *testing.T) {
	owner, referrer := twoDocs(t)

	registry := NewCrossPackageRegistry(map[string]string{
		"https://ex.test/a.json": "example.com/m/apkg",
		"https://ex.test/b.json": "example.com/m/bpkg",
	})
	// Deliberately skip generating apkg, so nothing records the widget type.
	_ = owner

	gen := New(Config{
		PackageName:  "bpkg",
		ImportPath:   "example.com/m/bpkg",
		CrossPackage: registry,
		Validation:   ValidationModeStatic,
		Resolver:     schema.NewMappingResolver(map[string]*schema.Schema{"https://ex.test/a.json": owner}),
	})

	_, err := gen.Generate(referrer)
	if err == nil {
		t.Fatal("expected generation to fail: the referenced type is owned by another package that did not generate it")
	}
	var miss *CrossPackageMissError
	if !errors.As(err, &miss) {
		t.Fatalf("expected CrossPackageMissError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "example.com/m/apkg") {
		t.Errorf("error should name the owning package, got: %v", err)
	}
	// This is the cause the old message described for both of them, and the
	// only one it described correctly: nothing from a.json was generated, so
	// the answer is about the run's inputs. The other cause -- a document that
	// *was* generated, referenced at a node it gives no declaration of its own
	// -- got this sentence too and was sent to check inputs that were fine
	// (issue #310). Held here so the two do not collapse back into one.
	if !strings.Contains(err.Error(), "generated no type from it") {
		t.Errorf("a document its owning package never generated should say so, and point at the inputs.\ngot: %v", err)
	}
	if strings.Contains(err.Error(), "no Go type of its own") {
		t.Errorf("that is the sentence for a node inside a document that *was* generated; this document was not.\ngot: %v", err)
	}
	// And the reference is named as it was written, which is what a reader has
	// to grep for when a document carries several.
	if !strings.Contains(err.Error(), "https://ex.test/a.json#/definitions/widget") {
		t.Errorf("error should name the $ref that failed, got: %v", err)
	}
}

// When the owning package registered the type, the reference resolves to a
// qualified import and generation succeeds.
func TestGenerateSucceedsWhenForeignTypeIsRegistered(t *testing.T) {
	owner, referrer := twoDocs(t)

	registry := NewCrossPackageRegistry(map[string]string{
		"https://ex.test/a.json": "example.com/m/apkg",
		"https://ex.test/b.json": "example.com/m/bpkg",
	})
	registry.RecordType(owner.Definitions["widget"], "example.com/m/apkg", "Widget")

	gen := New(Config{
		PackageName:  "bpkg",
		ImportPath:   "example.com/m/bpkg",
		CrossPackage: registry,
		Validation:   ValidationModeStatic,
		Resolver:     schema.NewMappingResolver(map[string]*schema.Schema{"https://ex.test/a.json": owner}),
	})

	if _, err := gen.Generate(referrer); err != nil {
		t.Fatalf("generation should succeed once the owner registered the type: %v", err)
	}
}

// A package that does not own a document must not be able to claim its types:
// otherwise a fallback local copy hijacks the entry and later packages import
// the copy's package instead of the owner's.
func TestRecordTypeRejectsNonOwningPackage(t *testing.T) {
	owner, _ := twoDocs(t)
	widget := owner.Definitions["widget"]

	registry := NewCrossPackageRegistry(map[string]string{
		"https://ex.test/a.json": "example.com/m/apkg",
	})
	registry.RecordType(widget, "example.com/m/bpkg", "Widget")
	if qt, ok := registry.lookup(widget); ok {
		t.Errorf("bpkg must not claim a type owned by apkg, but recorded %+v", qt)
	}

	registry.RecordType(widget, "example.com/m/apkg", "Widget")
	qt, ok := registry.lookup(widget)
	if !ok || qt.ImportPath != "example.com/m/apkg" {
		t.Errorf("the owning package should claim the type, got %+v (ok=%v)", qt, ok)
	}
}

// A registry built as a struct literal must not panic on first use.
func TestCrossPackageRegistryLiteralDoesNotPanic(t *testing.T) {
	owner, _ := twoDocs(t)
	reg := &CrossPackageRegistry{DocPackages: map[string]string{"https://ex.test/a.json": "example.com/m/apkg"}}
	reg.RecordType(owner.Definitions["widget"], "example.com/m/apkg", "Widget")
	if _, ok := reg.lookup(owner.Definitions["widget"]); !ok {
		t.Error("RecordType on a literal-constructed registry should have recorded the type")
	}
}
