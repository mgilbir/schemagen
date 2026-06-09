package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

func personSchema() *schema.Schema {
	return &schema.Schema{
		Title: "Person",
		Type:  schema.TypeList{"object"},
		Properties: map[string]*schema.Schema{
			"first_name": {Type: schema.TypeList{"string"}},
			"last_name":  {Type: schema.TypeList{"string"}},
		},
		Required: []string{"first_name"},
	}
}

func fieldByJSONName(sd *StructDef, jsonName string) (FieldDef, bool) {
	for _, f := range sd.Fields {
		if f.JSONName == jsonName {
			return f, true
		}
	}
	return FieldDef{}, false
}

func TestFieldNameOverrideRenamesField(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FieldNames = FieldNameMap{
		"Person": {"first_name": "GivenName"},
	}

	gen := New(cfg)
	file, err := gen.Generate(personSchema())
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	sd, ok := file.TypeDefs[0].(*StructDef)
	if !ok {
		t.Fatalf("expected *StructDef, got %T", file.TypeDefs[0])
	}

	f, ok := fieldByJSONName(sd, "first_name")
	if !ok {
		t.Fatal("first_name field missing")
	}
	if f.Name != "GivenName" {
		t.Errorf("first_name Go name = %q, want %q", f.Name, "GivenName")
	}
	// The JSON tag must keep the original property name for round-trip fidelity.
	if f.JSONName != "first_name" {
		t.Errorf("JSONName = %q, want %q", f.JSONName, "first_name")
	}

	// Unmapped field keeps its derived name.
	if f, _ := fieldByJSONName(sd, "last_name"); f.Name != "LastName" {
		t.Errorf("last_name Go name = %q, want %q", f.Name, "LastName")
	}

	// The override must be recorded as applied.
	if !gen.AppliedOverrides()["Person"]["first_name"] {
		t.Error("override Person.first_name not recorded as applied")
	}
}

func TestFieldNameOverrideCollisionIsError(t *testing.T) {
	cfg := DefaultConfig()
	// Forcing first_name to "LastName" collides with the derived name of last_name.
	cfg.FieldNames = FieldNameMap{
		"Person": {"first_name": "LastName"},
	}

	gen := New(cfg)
	_, err := gen.Generate(personSchema())
	if err == nil {
		t.Fatal("expected collision error, got nil")
	}
	if !strings.Contains(err.Error(), "collides") {
		t.Errorf("error = %q, want it to mention a collision", err.Error())
	}
}

func TestFieldNameOverrideReservedNameIsError(t *testing.T) {
	// Each of these would produce uncompilable Go (field colliding with a
	// generated method or the synthesized overflow field).
	for _, reserved := range []string{"Validate", "MarshalJSON", "UnmarshalJSON", "SetDefaults", "AdditionalProperties"} {
		t.Run(reserved, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.FieldNames = FieldNameMap{"Person": {"first_name": reserved}}

			gen := New(cfg)
			_, err := gen.Generate(personSchema())
			if err == nil {
				t.Fatalf("expected error for reserved override %q, got nil", reserved)
			}
			if !strings.Contains(err.Error(), "collides") {
				t.Errorf("error = %q, want it to mention a collision", err.Error())
			}
		})
	}
}

func TestFieldNameOverrideNotAppliedForOtherType(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FieldNames = FieldNameMap{
		"Company": {"first_name": "GivenName"}, // different type, should not match
	}

	gen := New(cfg)
	file, err := gen.Generate(personSchema())
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	sd := file.TypeDefs[0].(*StructDef)
	if f, _ := fieldByJSONName(sd, "first_name"); f.Name != "FirstName" {
		t.Errorf("first_name Go name = %q, want derived %q", f.Name, "FirstName")
	}
	if len(gen.AppliedOverrides()) != 0 {
		t.Errorf("expected no applied overrides, got %v", gen.AppliedOverrides())
	}
}

func TestLoadFieldMapFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "names.json")
	content := `{
		"person.json": {
			"Person": {"first_name": "GivenName"}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	fm, err := LoadFieldMapFile(path)
	if err != nil {
		t.Fatalf("LoadFieldMapFile() error: %v", err)
	}
	got, ok := fm["person.json"].Override("Person", "first_name")
	if !ok || got != "GivenName" {
		t.Errorf("Override = (%q, %v), want (%q, true)", got, ok, "GivenName")
	}
}

func TestLoadFieldMapFileRejectsInvalidIdentifier(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "names.json")
	// lower-case (unexported) target is invalid: struct fields must be exported.
	content := `{"person.json": {"Person": {"first_name": "givenName"}}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := LoadFieldMapFile(path); err == nil {
		t.Fatal("expected error for unexported identifier, got nil")
	}
}

func TestIsExportedGoIdentifier(t *testing.T) {
	cases := map[string]bool{
		"GivenName":  true,
		"ID":         true,
		"Field_1":    true,
		"givenName":  false, // not exported
		"123Name":    false, // starts with digit
		"Given Name": false, // space
		"":           false,
		"type":       false, // keyword (also lower-case)
	}
	for in, want := range cases {
		if got := isExportedGoIdentifier(in); got != want {
			t.Errorf("isExportedGoIdentifier(%q) = %v, want %v", in, got, want)
		}
	}
}
