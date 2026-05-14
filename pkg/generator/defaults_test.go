package generator

import "testing"

func TestDefaultToGoLiteral(t *testing.T) {
	tests := []struct {
		name       string
		defaultVal any
		goType     GoType
		want       string
	}{
		// String defaults
		{"string_hello", "hello", &PrimitiveType{Name: "string"}, `"hello"`},
		{"string_empty", "", &PrimitiveType{Name: "string"}, ""},
		{"string_with_quotes", `say "hi"`, &PrimitiveType{Name: "string"}, `"say \"hi\""`},

		// Integer defaults (JSON numbers come as float64)
		{"int_42", float64(42), &PrimitiveType{Name: "int64"}, "42"},
		{"int_0", float64(0), &PrimitiveType{Name: "int64"}, ""},
		{"int_negative", float64(-5), &PrimitiveType{Name: "int64"}, "-5"},

		// Float defaults
		{"float_3.14", float64(3.14), &PrimitiveType{Name: "float64"}, "3.14"},
		{"float_0", float64(0), &PrimitiveType{Name: "float64"}, ""},
		{"float_30.5", float64(30.5), &PrimitiveType{Name: "float64"}, "30.5"},

		// Boolean defaults
		{"bool_true", true, &PrimitiveType{Name: "bool"}, "true"},
		{"bool_false", false, &PrimitiveType{Name: "bool"}, ""},

		// Nil values
		{"nil_default", nil, &PrimitiveType{Name: "string"}, ""},
		{"nil_type", "hello", nil, ""},

		// Complex types (should return empty)
		{"array_type", []any{1, 2, 3}, &ArrayType{ItemType: &PrimitiveType{Name: "int64"}}, ""},
		{"map_type", map[string]any{"a": 1}, &MapType{KeyType: &PrimitiveType{Name: "string"}, ValueType: &PrimitiveType{Name: "any"}}, ""},

		// Type mismatch (should return empty)
		{"string_for_int", "hello", &PrimitiveType{Name: "int64"}, ""},
		{"number_for_string", float64(42), &PrimitiveType{Name: "string"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := defaultToGoLiteral(tt.defaultVal, tt.goType)
			if got != tt.want {
				t.Errorf("defaultToGoLiteral(%v, %v) = %q, want %q", tt.defaultVal, tt.goType, got, tt.want)
			}
		})
	}
}
