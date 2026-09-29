package generator

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// generateForDefaults runs Generate over doc under cfg and returns the IR and
// the defaults the run declined.
func generateForDefaults(t *testing.T, doc string, cfg Config) (*File, []SkippedDefault) {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	if cfg.PackageName == "" {
		cfg.PackageName = "testpkg"
	}
	g := New(cfg)
	ir, err := g.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return ir, g.SkippedDefaults()
}

// fieldNamed finds the field whose property is jsonName, and fails the test if
// there is none: a field that was never found would read as a zero FieldDef, and
// an empty default would then pass for a field that does not exist.
func fieldNamed(t *testing.T, ir *File, jsonName string) FieldDef {
	t.Helper()
	for _, td := range ir.TypeDefs {
		sd, ok := td.(*StructDef)
		if !ok {
			continue
		}
		for _, f := range sd.Fields {
			if f.JSONName == jsonName {
				return f
			}
		}
	}
	t.Fatalf("no field for property %q", jsonName)
	return FieldDef{}
}

// TestAnUnusableDefaultIsSkippedWhereverItIsWritten holds every spelling of an
// unusable default to one answer: generation succeeds, SetDefaults writes
// nothing, and the default is reported with where it was written.
//
// 4.5 on an integer used to fail generation of a perfectly legal schema, and
// did so whether the keyword was written on the property or behind a $ref --
// which of the two an author wrote is exactly the difference issue #172 said
// nothing may turn on. It still may not: both are now skipped the same way.
func TestAnUnusableDefaultIsSkippedWhereverItIsWritten(t *testing.T) {
	for _, tc := range []struct {
		name, doc, loc string
	}{
		{"inline", `{"type":"object","properties":{"n":{"type":"integer","default":4.5}}}`, "#/properties/n/default"},
		{"through a $ref", `{"type":"object","properties":{"n":{"$ref":"#/$defs/N"}},` +
			`"$defs":{"N":{"type":"integer","default":4.5}}}`, "#/$defs/N/default"},
		{"under --big-int", `{"type":"object","properties":{"n":{"type":"integer","default":4.5}}}`, "#/properties/n/default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{BigIntSupport: tc.name == "under --big-int"}
			ir, skipped := generateForDefaults(t, tc.doc, cfg)
			if f := fieldNamed(t, ir, "n"); f.DefaultLiteral != "" {
				t.Errorf("the default was planted as %q", f.DefaultLiteral)
			}
			if len(skipped) != 1 {
				t.Fatalf("want one skipped default, got %+v", skipped)
			}
			if skipped[0].Property != "n" || skipped[0].Location != tc.loc || !strings.Contains(skipped[0].Reason, `"type"`) {
				t.Errorf("the report is %+v, want property n at %s failing \"type\"", skipped[0], tc.loc)
			}
		})
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
	cand := func() *defaultCandidate { return &defaultCandidate{value: "dflt", property: "p"} }
	g := New(Config{PackageName: "testpkg"})
	g.output = &File{TypeDefs: []TypeDef{
		&AliasDef{Name: "Token", Underlying: &PrimitiveType{Name: "string"}},
		&StructDef{Name: "Holder", Fields: []FieldDef{
			{Name: "Local", JSONName: "local",
				Type: &NamedType{Name: "Token", Pointer: true}, pendingDefault: cand()},
			{Name: "Foreign", JSONName: "foreign",
				Type: &NamedType{Name: "Token", Pointer: true, PkgAlias: "other"}, pendingDefault: cand()},
		}},
	}}
	g.resolveDefaults()
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
	// Nor is it decoded: whether that type's decode holds the value is a
	// question about a declaration this run cannot read.
	if got := holder.Fields[1].DefaultShape; got != "" {
		t.Errorf("the field typed by another package was given the shape %q", got)
	}
	if len(g.skippedDefaults) != 1 || !strings.Contains(g.skippedDefaults[0].Reason, "another package") {
		t.Errorf("the declined default is reported as %+v, want one report naming the other package", g.skippedDefaults)
	}
}

func TestDefaultToGoLiteral(t *testing.T) {
	num := func(s string) any { return json.Number(s) }
	tests := []struct {
		name       string
		defaultVal any
		goType     GoType
		want       string
		handled    bool
		wantWhy    bool
	}{
		// String defaults
		{"string_hello", "hello", &PrimitiveType{Name: "string"}, `"hello"`, true, false},
		{"string_empty", "", &PrimitiveType{Name: "string"}, "", true, false},
		{"string_with_quotes", `say "hi"`, &PrimitiveType{Name: "string"}, `"say \"hi\""`, true, false},

		// Integer defaults
		{"int_42", num("42"), &PrimitiveType{Name: "int64"}, "42", true, false},
		{"int_0", num("0"), &PrimitiveType{Name: "int64"}, "", true, false},
		{"int_negative", num("-5"), &PrimitiveType{Name: "int64"}, "-5", true, false},
		{"int_float_spelling", num("4.0"), &PrimitiveType{Name: "int64"}, "4", true, false},

		// A value an int64 cannot hold is declined with a reason, never
		// truncated or wrapped.
		{"int_fractional", num("4.5"), &PrimitiveType{Name: "int64"}, "", true, true},
		{"int_fractional_negative", num("-0.5"), &PrimitiveType{Name: "int64"}, "", true, true},
		{"int_pointer_fractional", num("4.5"), &PointerType{Inner: &PrimitiveType{Name: "int64"}}, "", true, true},
		{"int_pointer_zero_kept", num("0"), &PointerType{Inner: &PrimitiveType{Name: "int64"}}, "0", true, false},
		{"int_overflow_high", num("1e30"), &PrimitiveType{Name: "int64"}, "", true, true},
		{"int_overflow_low", num("-1e30"), &PrimitiveType{Name: "int64"}, "", true, true},
		{"int_overflow_exactly_2_63", num("9223372036854775808"), &PrimitiveType{Name: "int64"}, "", true, true},
		{"int_max_int64_ok", num("9223372036854775807"), &PrimitiveType{Name: "int64"}, "9223372036854775807", true, false},
		{"int_min_int64_ok", num("-9223372036854775808"), &PrimitiveType{Name: "int64"}, "-9223372036854775808", true, false},

		// Float defaults
		{"float_3.14", num("3.14"), &PrimitiveType{Name: "float64"}, "3.14", true, false},
		{"float_0", num("0"), &PrimitiveType{Name: "float64"}, "", true, false},
		{"float_30.5", num("30.5"), &PrimitiveType{Name: "float64"}, "30.5", true, false},
		{"float_out_of_range", num("1e400"), &PrimitiveType{Name: "float64"}, "", true, true},

		// An exact number holds what a float64 cannot.
		{"number_exact_1e400", num("1e400"), &PrimitiveType{Name: GoNumberTypeName}, `json.Number("1e400")`, true, false},

		// Boolean defaults
		{"bool_true", true, &PrimitiveType{Name: "bool"}, "true", true, false},
		{"bool_false", false, &PrimitiveType{Name: "bool"}, "", true, false},

		// An untyped field holds what encoding/json decodes the JSON into.
		{"any_object", map[string]any{"a": []any{num("1"), "x"}}, &PrimitiveType{Name: "any"}, `map[string]any{"a": []any{float64(1), "x"}}`, true, false},

		// Nil values
		{"nil_default", nil, &PrimitiveType{Name: "string"}, "", true, false},
		{"nil_type", "hello", nil, "", false, false},

		// Other types are resolveDefaults' to spell.
		{"array_type", []any{num("1")}, &ArrayType{ItemType: &PrimitiveType{Name: "int64"}}, "", false, false},
		{"map_type", map[string]any{"a": num("1")}, &MapType{KeyType: &PrimitiveType{Name: "string"}, ValueType: &PrimitiveType{Name: "any"}}, "", false, false},

		// A value of the wrong JSON kind is declined with a reason, where it
		// used to be dropped without one.
		{"string_for_int", "hello", &PrimitiveType{Name: "int64"}, "", true, true},
		{"array_for_int", []any{}, &PrimitiveType{Name: "int64"}, "", true, true},
		{"number_for_string", num("42"), &PrimitiveType{Name: "string"}, "", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, handled, why := defaultToGoLiteral(tt.defaultVal, tt.goType)
			if handled != tt.handled {
				t.Fatalf("handled = %v, want %v", handled, tt.handled)
			}
			if (why != "") != tt.wantWhy {
				t.Fatalf("why = %q, want a reason: %v", why, tt.wantWhy)
			}
			if got != tt.want {
				t.Errorf("literal = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBigIntDefaultWritesAValuePastInt64 is the integer default --big-int
// exists to hold. It used to write nothing, on the ground that "default" was
// decoded into an `any` and a literal past float64's exact range arrived
// rounded; the schema package decodes it with UseNumber, so the digits are the
// schema's own and the wrapper holds them.
func TestBigIntDefaultWritesAValuePastInt64(t *testing.T) {
	const doc = `{"type":"object","properties":{` +
		`"big":{"type":"integer","default":1e30},` +
		`"small":{"type":"integer","default":7}}}`
	ir, skipped := generateForDefaults(t, doc, Config{BigIntSupport: true})
	if len(skipped) != 0 {
		t.Fatalf("nothing should be skipped, got %+v", skipped)
	}
	if big := fieldNamed(t, ir, "big"); !strings.Contains(big.DefaultLiteral, `"1000000000000000000000000000000"`) {
		t.Errorf("the default past int64 is %q, want the wrapper's decode of its digits", big.DefaultLiteral)
	}
	if small := fieldNamed(t, ir, "small"); !strings.Contains(small.DefaultLiteral, "_int64: 7") {
		t.Errorf("the in-range default beside it is %q, want the wrapper literal", small.DefaultLiteral)
	}
}

// TestCollectionDefaultDeclinesAnElementWithNoLiteral names the two element
// kinds a composite literal cannot hold, and which no schema in the corpus
// produces in this position -- so the IR is built directly, exactly as
// TestNamedDefaultDeclinesAForeignType does, and for the same reason.
//
// A pointer element has no literal at all: Go has no address-of for a scalar
// written inside a composite. It is planted by decoding instead, which holds
// it. An element typed by another generated package has no declaration here to
// read -- a local type of the same name would answer in its place -- so neither
// a literal nor the decode can be shown to hold it, and the whole default is
// declined rather than half-written, and reported.
func TestCollectionDefaultDeclinesAnElementWithNoLiteral(t *testing.T) {
	cand := func(p string) *defaultCandidate { return &defaultCandidate{value: []any{"z"}, property: p} }
	g := New(Config{PackageName: "testpkg"})
	g.output = &File{TypeDefs: []TypeDef{
		&AliasDef{Name: "Token", Underlying: &PrimitiveType{Name: "string"}},
		&StructDef{Name: "Holder", Fields: []FieldDef{
			{Name: "Plain", JSONName: "plain",
				Type: &ArrayType{ItemType: &NamedType{Name: "Token"}}, pendingDefault: cand("plain")},
			{Name: "Pointed", JSONName: "pointed",
				Type: &ArrayType{ItemType: &NamedType{Name: "Token", Pointer: true}}, pendingDefault: cand("pointed")},
			{Name: "Foreign", JSONName: "foreign",
				Type: &ArrayType{ItemType: &NamedType{Name: "Token", PkgAlias: "other"}}, pendingDefault: cand("foreign")},
		}},
	}}
	g.resolveDefaults()
	fields := g.output.TypeDefs[1].(*StructDef).Fields
	if want := `[]Token{Token("z")}`; fields[0].DefaultLiteral != want {
		t.Errorf("the control element is %q, want %q", fields[0].DefaultLiteral, want)
	}
	if f := fields[1]; f.DefaultShape != defaultShapeDecoded || f.DefaultLiteral != strconv.Quote(`{"pointed":["z"]}`) {
		t.Errorf("the pointer elements are %q / %q, want the decoded document", f.DefaultShape, f.DefaultLiteral)
	}
	if f := fields[2]; f.DefaultLiteral != "" {
		t.Errorf("foreign: wrote %q for an element nothing here can hold", f.DefaultLiteral)
	}
	if len(g.skippedDefaults) != 1 || g.skippedDefaults[0].Property != "foreign" {
		t.Errorf("want the foreign default reported, got %+v", g.skippedDefaults)
	}
}
