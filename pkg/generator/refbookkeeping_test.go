package generator

import (
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// A ref that failed against one context but resolved against another must not
// be reported: resolveRefInContext is probed optimistically by most callers.
func TestNeverResolvedRefsIgnoresRefsResolvedElsewhere(t *testing.T) {
	g := New(Config{})
	g.unresolvedRefs["app://a.json#/definitions/only-failed"] = true
	g.unresolvedRefs["app://a.json#/definitions/also-succeeded"] = true
	g.resolvedRefs["app://a.json#/definitions/also-succeeded"] = true

	got := g.neverResolvedRefs()
	if len(got) != 1 || got[0] != "app://a.json#/definitions/only-failed" {
		t.Errorf("neverResolvedRefs() = %v, want only the ref that never resolved", got)
	}
}

// Generate must not carry ref bookkeeping across calls: in shared-types mode
// one generator runs several schemas.
func TestGenerateResetsRefBookkeeping(t *testing.T) {
	g := New(Config{Validation: ValidationModeStatic})
	g.unresolvedRefs["app://stale.json#/definitions/x"] = true

	s := &schema.Schema{Title: "T", Type: schema.TypeList{"object"}}
	if _, err := g.Generate(s); err != nil {
		t.Fatalf("Generate should not inherit a previous schema's unresolved refs: %v", err)
	}
	if g.unresolvedRefs["app://stale.json#/definitions/x"] {
		t.Error("unresolvedRefs should be reset per Generate call")
	}
}
