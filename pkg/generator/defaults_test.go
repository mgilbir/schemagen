package generator

import "testing"

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
