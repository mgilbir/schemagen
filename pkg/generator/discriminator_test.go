package generator

import (
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

func TestExtractDiscriminatorValue(t *testing.T) {
	tests := []struct {
		name string
		prop *schema.Schema
		want string
	}{
		{
			name: "const_string",
			prop: func() *schema.Schema {
				val := any("click")
				return &schema.Schema{Const: &val}
			}(),
			want: "click",
		},
		{
			name: "single_enum",
			prop: &schema.Schema{Enum: []any{"circle"}},
			want: "circle",
		},
		{
			name: "multi_enum_returns_empty",
			prop: &schema.Schema{Enum: []any{"a", "b"}},
			want: "",
		},
		{
			name: "nil_schema",
			prop: nil,
			want: "",
		},
		{
			name: "no_const_or_enum",
			prop: &schema.Schema{},
			want: "",
		},
		{
			name: "const_non_string",
			prop: func() *schema.Schema {
				val := any(42)
				return &schema.Schema{Const: &val}
			}(),
			want: "",
		},
		{
			name: "single_enum_non_string",
			prop: &schema.Schema{Enum: []any{123}},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDiscriminatorValue(tt.prop)
			if got != tt.want {
				t.Errorf("extractDiscriminatorValue() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectHeuristicDiscriminator(t *testing.T) {
	g := &Generator{
		generated: make(map[string]bool),
	}

	// Test: all variants have a shared "type" property with distinct const values
	t.Run("detects_shared_const_property", func(t *testing.T) {
		clickConst := any("click")
		keypressConst := any("keypress")

		variants := []*schema.Schema{
			{
				Properties: map[string]*schema.Schema{
					"kind": {Const: &clickConst},
					"x":    {},
				},
			},
			{
				Properties: map[string]*schema.Schema{
					"kind": {Const: &keypressConst},
					"key":  {},
				},
			},
		}

		oneOfDef := &OneOfDef{
			Variants: []OneOfVariant{
				{FieldName: "Click"},
				{FieldName: "Keypress"},
			},
		}

		g.detectHeuristicDiscriminator(oneOfDef, variants)

		if oneOfDef.DiscriminatorField != "kind" {
			t.Errorf("DiscriminatorField = %q, want %q", oneOfDef.DiscriminatorField, "kind")
		}
		if len(oneOfDef.DiscriminatorMap) != 2 {
			t.Fatalf("DiscriminatorMap has %d entries, want 2", len(oneOfDef.DiscriminatorMap))
		}
		if oneOfDef.Variants[0].DiscriminatorValue != "click" {
			t.Errorf("Variants[0].DiscriminatorValue = %q, want %q", oneOfDef.Variants[0].DiscriminatorValue, "click")
		}
		if oneOfDef.Variants[1].DiscriminatorValue != "keypress" {
			t.Errorf("Variants[1].DiscriminatorValue = %q, want %q", oneOfDef.Variants[1].DiscriminatorValue, "keypress")
		}
	})

	// Test: variants with no shared const property — fallback
	t.Run("no_shared_const_property", func(t *testing.T) {
		variants := []*schema.Schema{
			{
				Properties: map[string]*schema.Schema{
					"radius": {},
				},
			},
			{
				Properties: map[string]*schema.Schema{
					"width":  {},
					"height": {},
				},
			},
		}

		oneOfDef := &OneOfDef{
			Variants: []OneOfVariant{
				{FieldName: "Circle"},
				{FieldName: "Rectangle"},
			},
		}

		g.detectHeuristicDiscriminator(oneOfDef, variants)

		if oneOfDef.DiscriminatorField != "" {
			t.Errorf("DiscriminatorField = %q, want empty", oneOfDef.DiscriminatorField)
		}
	})

	// Test: duplicate const values — not a valid discriminator
	t.Run("duplicate_const_values", func(t *testing.T) {
		sameConst := any("same")

		variants := []*schema.Schema{
			{
				Properties: map[string]*schema.Schema{
					"kind": {Const: &sameConst},
				},
			},
			{
				Properties: map[string]*schema.Schema{
					"kind": {Const: &sameConst},
				},
			},
		}

		oneOfDef := &OneOfDef{
			Variants: []OneOfVariant{
				{FieldName: "A"},
				{FieldName: "B"},
			},
		}

		g.detectHeuristicDiscriminator(oneOfDef, variants)

		if oneOfDef.DiscriminatorField != "" {
			t.Errorf("DiscriminatorField = %q, want empty (duplicate values)", oneOfDef.DiscriminatorField)
		}
	})
}
