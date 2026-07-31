package generator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

func annotationDefFor(t *testing.T, input string) *AnnotationSchemaDef {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	ir, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*AnnotationSchemaDef); ok {
			return d
		}
	}
	return nil
}

// unevaluatedItems next to in-place applicators needs the runtime evaluator:
// which items count as evaluated depends on which branches match the value.
func TestUnevaluatedItemsWithApplicatorsUsesRuntimeEvaluator(t *testing.T) {
	tests := []string{
		`{"prefixItems":[{"const":"foo"}],"anyOf":[{"prefixItems":[true,{"const":"bar"}]}],"unevaluatedItems":false}`,
		`{"unevaluatedItems":{"type":"boolean"},"anyOf":[{"items":{"type":"string"}},true]}`,
		`{"allOf":[{"prefixItems":[true]},{"unevaluatedItems":false}]}`,
		`{"allOf":[{"contains":{"multipleOf":2}},{"contains":{"multipleOf":3}}],"unevaluatedItems":{"multipleOf":5}}`,
	}
	for _, in := range tests {
		t.Run(in[:40], func(t *testing.T) {
			if annotationDefFor(t, in) == nil {
				t.Fatalf("expected the runtime evaluator for %s", in)
			}
		})
	}
}

// Schemas static analysis already handles keep their existing shape, so the
// change does not churn output for cases that were correct.
func TestStaticUnevaluatedItemsUnchanged(t *testing.T) {
	tests := []string{
		`{"type":"array","prefixItems":[{"type":"string"}],"unevaluatedItems":false}`,
		`{"type":"array","items":{"type":"string"}}`,
	}
	for _, in := range tests {
		t.Run(in[:30], func(t *testing.T) {
			if d := annotationDefFor(t, in); d != nil {
				t.Fatalf("static case was routed to the runtime evaluator: %s", in)
			}
		})
	}
}

// A keyword outside the evaluator's model must keep the static path rather than
// be interpreted with that keyword silently ignored.
func TestAnnotationModelFailsClosed(t *testing.T) {
	tests := []string{
		`{"allOf":[{"uniqueItems":true}],"unevaluatedItems":false}`,
		`{"allOf":[{"properties":{"a":{"type":"string"}}}],"unevaluatedItems":false}`,
		`{"allOf":[{"$ref":"#/$defs/x"}],"unevaluatedItems":false,"$defs":{"x":{"type":"array"}}}`,
	}
	for _, in := range tests {
		t.Run(in[:36], func(t *testing.T) {
			if d := annotationDefFor(t, in); d != nil {
				t.Fatalf("unsupported keyword was routed to the evaluator: %s\n%s", in, d.NodeLiteral)
			}
		})
	}
}

// additionalItems without a tuple is ignored by the spec: it neither validates
// nor evaluates. Modelling it as items made allOf reject values it must accept.
func TestIgnoredAdditionalItemsIsNotModelledAsItems(t *testing.T) {
	d := annotationDefFor(t, `{"allOf":[{"additionalItems":{"type":"number"}}],"unevaluatedItems":{"type":"string"}}`)
	if d == nil {
		t.Fatal("expected the runtime evaluator")
	}
	// The allOf branch must carry no Items: the additionalItems is inert.
	branch := d.NodeLiteral[strings.Index(d.NodeLiteral, "AllOf"):]
	if strings.Contains(branch[:strings.Index(branch, "UnevaluatedItems")], "Items:") {
		t.Fatalf("ignored additionalItems was modelled as items:\n%s", d.NodeLiteral)
	}
}
