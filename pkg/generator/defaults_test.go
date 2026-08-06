package generator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// TestDefaultThroughRefReportsAnUnrepresentableValue holds the two spellings of
// the same schema to the same answer.
//
// A fractional default on an integer field fails generation rather than being
// truncated into a different value. Behind a $ref the keyword is found by the
// same walk but written by a later pass, and dropping it quietly there would
// mean whether the run failed depended on where the author put the keyword --
// the spelling-dependence issue #172 was about.
func TestDefaultThroughRefReportsAnUnrepresentableValue(t *testing.T) {
	const doc = `{"type":"object","properties":{"n":{"$ref":"#/$defs/N"}},` +
		`"$defs":{"N":{"type":"integer","default":4.5}}}`
	var s schema.Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	_, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err == nil {
		t.Fatalf("generation succeeded; the same default written inline is an error")
	}
	if !strings.Contains(err.Error(), "not an integer") {
		t.Fatalf("generation failed for the wrong reason: %v", err)
	}
}

// TestNamedDefaultDeclinesAForeignType is the guard no schema can reach from
// here: a field typed by another generated package.
//
// The literal this pass writes is a conversion into the named type, and whether
// that conversion compiles is read off the declaration this run holds under that
// name. For a qualified type there is no such declaration -- and worse, a local
// type of the same name would answer in its place and hand back the underlying
// of something else. The IR is built directly because a cross-package run is the
// only way to produce the shape, and the shape is one field.
func TestNamedDefaultDeclinesAForeignType(t *testing.T) {
	value := any("dflt")
	g := New(Config{PackageName: "testpkg"})
	g.output = &File{TypeDefs: []TypeDef{
		&AliasDef{Name: "Token", Underlying: &PrimitiveType{Name: "string"}},
		&StructDef{Name: "Holder", Fields: []FieldDef{
			{Name: "Local", JSONName: "local",
				Type: &NamedType{Name: "Token", Pointer: true}, pendingDefault: &value},
			{Name: "Foreign", JSONName: "foreign",
				Type: &NamedType{Name: "Token", Pointer: true, PkgAlias: "other"}, pendingDefault: &value},
		}},
	}}
	if err := g.resolveNamedTypeDefaults(); err != nil {
		t.Fatalf("resolveNamedTypeDefaults: %v", err)
	}
	holder := g.output.TypeDefs[1].(*StructDef)
	// The local field is the control: without it a pass that declined
	// everything would satisfy the assertion below.
	if got := holder.Fields[0].DefaultLiteral; got != `Token("dflt")` {
		t.Errorf("the local field got %q, want %q", got, `Token("dflt")`)
	}
	if got := holder.Fields[1].DefaultLiteral; got != "" {
		t.Errorf("the field typed by another package got the literal %q, "+
			"read off this package's declaration of the same name", got)
	}
}

func TestDefaultToGoLiteral(t *testing.T) {
	tests := []struct {
		name       string
		defaultVal any
		goType     GoType
		want       string
		wantErr    bool
	}{
		// String defaults
		{"string_hello", "hello", &PrimitiveType{Name: "string"}, `"hello"`, false},
		{"string_empty", "", &PrimitiveType{Name: "string"}, "", false},
		{"string_with_quotes", `say "hi"`, &PrimitiveType{Name: "string"}, `"say \"hi\""`, false},

		// Integer defaults (JSON numbers come as float64)
		{"int_42", float64(42), &PrimitiveType{Name: "int64"}, "42", false},
		{"int_0", float64(0), &PrimitiveType{Name: "int64"}, "", false},
		{"int_negative", float64(-5), &PrimitiveType{Name: "int64"}, "-5", false},

		// Fractional defaults on an integer field are rejected, not truncated.
		{"int_fractional", float64(4.5), &PrimitiveType{Name: "int64"}, "", true},
		{"int_fractional_negative", float64(-0.5), &PrimitiveType{Name: "int64"}, "", true},
		{"int_pointer_fractional", float64(4.5), &PointerType{Inner: &PrimitiveType{Name: "int64"}}, "", true},
		{"int_pointer_zero_kept", float64(0), &PointerType{Inner: &PrimitiveType{Name: "int64"}}, "0", false},

		// Out-of-range integer defaults are rejected: float64→int64 conversion is
		// undefined in Go outside the int64 range.
		{"int_overflow_high", 1e30, &PrimitiveType{Name: "int64"}, "", true},
		{"int_overflow_low", -1e30, &PrimitiveType{Name: "int64"}, "", true},
		{"int_overflow_exactly_2_63", float64(1 << 63), &PrimitiveType{Name: "int64"}, "", true},
		{"int_min_int64_ok", -float64(1 << 63), &PrimitiveType{Name: "int64"}, "-9223372036854775808", false},

		// Float defaults
		{"float_3.14", float64(3.14), &PrimitiveType{Name: "float64"}, "3.14", false},
		{"float_0", float64(0), &PrimitiveType{Name: "float64"}, "", false},
		{"float_30.5", float64(30.5), &PrimitiveType{Name: "float64"}, "30.5", false},

		// Boolean defaults
		{"bool_true", true, &PrimitiveType{Name: "bool"}, "true", false},
		{"bool_false", false, &PrimitiveType{Name: "bool"}, "", false},

		// Nil values
		{"nil_default", nil, &PrimitiveType{Name: "string"}, "", false},
		{"nil_type", "hello", nil, "", false},

		// Complex types (should return empty)
		{"array_type", []any{1, 2, 3}, &ArrayType{ItemType: &PrimitiveType{Name: "int64"}}, "", false},
		{"map_type", map[string]any{"a": 1}, &MapType{KeyType: &PrimitiveType{Name: "string"}, ValueType: &PrimitiveType{Name: "any"}}, "", false},

		// Type mismatch (should return empty, without an error)
		{"string_for_int", "hello", &PrimitiveType{Name: "int64"}, "", false},
		{"number_for_string", float64(42), &PrimitiveType{Name: "string"}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := defaultToGoLiteral(tt.defaultVal, tt.goType)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("defaultToGoLiteral(%v, %v) = %q, want error", tt.defaultVal, tt.goType, got)
				}
				if got != "" {
					t.Errorf("defaultToGoLiteral(%v, %v) returned literal %q alongside error", tt.defaultVal, tt.goType, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("defaultToGoLiteral(%v, %v) unexpected error: %v", tt.defaultVal, tt.goType, err)
			}
			if got != tt.want {
				t.Errorf("defaultToGoLiteral(%v, %v) = %q, want %q", tt.defaultVal, tt.goType, got, tt.want)
			}
		})
	}
}
