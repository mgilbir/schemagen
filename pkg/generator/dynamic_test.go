package generator

import (
	"encoding/json"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

func generateOne(t *testing.T, input string) []TypeDef {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	// These cases assert which TypeDef a schema shape selects, and run with no
	// resolver configured, so some inputs carry $refs that cannot resolve here.
	// LenientRefs keeps that from failing generation for reasons unrelated to
	// what is being tested; unresolvable-ref reporting has its own tests.
	ir, err := New(Config{PackageName: "testpkg", LenientRefs: true}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return ir.TypeDefs
}

// A schema whose only constraints come from applicators declares no type, so it
// used to become `type Root any` — which Go forbids methods on, silently
// dropping every constraint. It must now become a wrapper that can carry
// Validate().
func TestUntypedApplicatorsProduceValidatableWrapper(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantOneOf int
		wantAnyOf int
		wantIf    bool
	}{
		{
			name:      "oneOf",
			input:     `{"oneOf":[{"type":"integer"},{"minimum":2}]}`,
			wantOneOf: 2,
		},
		{
			name:      "anyOf",
			input:     `{"anyOf":[{"type":"integer"},{"minimum":2}]}`,
			wantAnyOf: 2,
		},
		{
			name:   "if/then",
			input:  `{"if":{"exclusiveMaximum":0},"then":{"minimum":-10}}`,
			wantIf: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defs := generateOne(t, tt.input)
			var got *DynamicSchemaDef
			for _, td := range defs {
				if d, ok := td.(*DynamicSchemaDef); ok {
					got = d
				}
			}
			if got == nil {
				t.Fatalf("expected a DynamicSchemaDef, got %T", defs[0])
			}
			if len(got.OneOf) != tt.wantOneOf {
				t.Errorf("OneOf branches = %d, want %d", len(got.OneOf), tt.wantOneOf)
			}
			if len(got.AnyOf) != tt.wantAnyOf {
				t.Errorf("AnyOf branches = %d, want %d", len(got.AnyOf), tt.wantAnyOf)
			}
			if got.HasIfThenElse != tt.wantIf {
				t.Errorf("HasIfThenElse = %v, want %v", got.HasIfThenElse, tt.wantIf)
			}
		})
	}
}

// The evaluator must fail closed. A branch using a keyword it cannot express
// has to fall back to the historical `type X any` with no validation, because
// emitting the checks it *does* understand would reject values the schema
// allows — a wrong answer is worse than a missing one.
func TestUnrepresentableBranchFallsBackToAny(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"required is not expressible", `{"oneOf":[{"required":["a"]},{"minimum":2}]}`},
		{"nested properties", `{"oneOf":[{"properties":{"a":{"type":"string"}}},{"minimum":2}]}`},
		{"enum", `{"anyOf":[{"enum":[1,2]},{"minimum":2}]}`},
		{"type union in a branch", `{"oneOf":[{"type":["string","null"]},{"minimum":2}]}`},
		{"nested applicator", `{"oneOf":[{"not":{"type":"string"}},{"minimum":2}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, td := range generateOne(t, tt.input) {
				if _, ok := td.(*DynamicSchemaDef); ok {
					t.Fatalf("built a dynamic validator for a branch it cannot express: %s", tt.input)
				}
			}
		})
	}
}

// Keyword coverage is decided from the re-marshaled schema rather than a
// hand-maintained field list, so a keyword the evaluator does not know about
// fails closed rather than being dropped.
func TestDynamicBranchChecksRejectsUnknownKeywords(t *testing.T) {
	var s schema.Schema
	if err := json.Unmarshal([]byte(`{"minimum":2,"x-vendor":{"a":1}}`), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	if _, ok := dynamicBranchChecks(&s); ok {
		t.Fatal("a schema carrying an unknown keyword was reported as representable")
	}
}

// Annotations constrain nothing, so their presence must not defeat the gate.
func TestDynamicBranchChecksAllowsAnnotations(t *testing.T) {
	var s schema.Schema
	if err := json.Unmarshal([]byte(`{"minimum":2,"title":"T","description":"d","default":1}`), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	checks, ok := dynamicBranchChecks(&s)
	if !ok {
		t.Fatal("annotations should not make a branch unrepresentable")
	}
	if len(checks) != 1 || checks[0].Kind != "minimum" {
		t.Fatalf("checks = %#v, want a single minimum check", checks)
	}
}

// A schema carrying constraints the dynamic evaluator does not own must be left
// to the path that handles them. {"$ref":...,"if":...} takes its constraint from
// the $ref — the "if" is only the ref's target — so owning it here produced a
// validator that accepted everything and silently disabled ref validation.
func TestDynamicSchemaDefDoesNotHijackOtherKeywords(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"$ref sibling", `{"$ref":"http://example.com/ref/if","if":{"$id":"http://example.com/ref/if","type":"integer"}}`},
		{"allOf sibling", `{"allOf":[{"type":"integer"}],"oneOf":[{"minimum":2},{"maximum":1}]}`},
		{"not sibling", `{"not":{"type":"string"},"oneOf":[{"minimum":2},{"maximum":1}]}`},
		{"properties sibling", `{"properties":{"a":{"type":"string"}},"oneOf":[{"minimum":2},{"maximum":1}]}`},
		{"if without then or else", `{"if":{"type":"integer"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, td := range generateOne(t, tt.input) {
				if _, ok := td.(*DynamicSchemaDef); ok {
					t.Fatalf("dynamic evaluator took over a schema it does not fully own: %s", tt.input)
				}
			}
		})
	}
}
