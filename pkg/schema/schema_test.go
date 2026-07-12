package schema

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildResourceGraphIndexesResourcesAndDynamicAnchors(t *testing.T) {
	input := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://example.com/root",
		"$defs": {
			"base": {
				"$dynamicAnchor": "node",
				"type": "object"
			},
			"legacy": {
				"$schema": "http://json-schema.org/draft-07/schema#",
				"$id": "legacy.json",
				"$anchor": "legacyAnchor",
				"type": "object"
			}
		}
	}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	s.Normalize()

	graph := BuildResourceGraph(&s, nil, DraftUnknown)
	if len(graph.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(graph.Resources))
	}

	root := graph.Resources["https://example.com/root"]
	if root == nil {
		t.Fatalf("missing root resource")
	}
	if root.Draft != Draft202012 {
		t.Fatalf("root draft = %v, want %v", root.Draft, Draft202012)
	}
	if root.DynamicAnchors["node"] == nil {
		t.Fatalf("missing dynamic anchor node")
	}

	legacy := graph.Resources["https://example.com/legacy.json"]
	if legacy == nil {
		t.Fatalf("missing legacy resource")
	}
	if legacy.Draft != Draft07 {
		t.Fatalf("legacy draft = %v, want %v", legacy.Draft, Draft07)
	}
	if legacy.Anchors["legacyAnchor"] == nil {
		t.Fatalf("missing legacy anchor")
	}
}

func TestParseSimpleObjectSchema(t *testing.T) {
	input := `{
		"type": "object",
		"title": "Person",
		"description": "A person",
		"properties": {
			"name": { "type": "string" },
			"age": { "type": "integer" }
		},
		"required": ["name"]
	}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(s.Type) != 1 || s.Type[0] != "object" {
		t.Errorf("expected type [object], got %v", s.Type)
	}
	if s.Title != "Person" {
		t.Errorf("expected title Person, got %s", s.Title)
	}
	if s.Description != "A person" {
		t.Errorf("expected description 'A person', got %s", s.Description)
	}
	if len(s.Properties) != 2 {
		t.Errorf("expected 2 properties, got %d", len(s.Properties))
	}
	if s.Properties["name"] == nil || len(s.Properties["name"].Type) != 1 || s.Properties["name"].Type[0] != "string" {
		t.Errorf("expected name property to be string")
	}
	if s.Properties["age"] == nil || len(s.Properties["age"].Type) != 1 || s.Properties["age"].Type[0] != "integer" {
		t.Errorf("expected age property to be integer")
	}
	if len(s.Required) != 1 || string(s.Required[0]) != "name" {
		t.Errorf("expected required [name], got %v", s.Required)
	}
}

func TestTypeListFromString(t *testing.T) {
	input := `{"type": "string"}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(s.Type) != 1 || s.Type[0] != "string" {
		t.Errorf("expected type [string], got %v", s.Type)
	}
}

func TestTypeListFromArray(t *testing.T) {
	input := `{"type": ["string", "null"]}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(s.Type) != 2 {
		t.Fatalf("expected 2 types, got %d", len(s.Type))
	}
	if s.Type[0] != "string" || s.Type[1] != "null" {
		t.Errorf("expected [string, null], got %v", s.Type)
	}
}

func TestTypeListPreservesDraft3SchemaAlternatives(t *testing.T) {
	input := `{"type": ["integer", {"properties": {"foo": {"type": "null"}}}]}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	s.Normalize()

	if len(s.Type) != 1 || s.Type[0] != "integer" {
		t.Fatalf("expected primitive type [integer], got %v", s.Type)
	}
	if len(s.TypeSchemas) != 1 {
		t.Fatalf("expected 1 schema-valued type alternative, got %d", len(s.TypeSchemas))
	}
	foo := s.TypeSchemas[0].Properties["foo"]
	if foo == nil || len(foo.Type) != 1 || foo.Type[0] != "null" {
		t.Fatalf("expected foo:null schema branch, got %#v", foo)
	}
}

func TestAdditionalPropertiesBoolFalse(t *testing.T) {
	input := `{
		"type": "object",
		"additionalProperties": false
	}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if s.AdditionalProperties == nil {
		t.Fatal("expected additionalProperties to be set")
	}
	if s.AdditionalProperties.Bool == nil {
		t.Fatal("expected additionalProperties.Bool to be set")
	}
	if *s.AdditionalProperties.Bool != false {
		t.Errorf("expected additionalProperties to be false")
	}
	if s.AdditionalProperties.Schema != nil {
		t.Errorf("expected additionalProperties.Schema to be nil")
	}
}

func TestAdditionalPropertiesBoolTrue(t *testing.T) {
	input := `{
		"type": "object",
		"additionalProperties": true
	}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if s.AdditionalProperties == nil {
		t.Fatal("expected additionalProperties to be set")
	}
	if s.AdditionalProperties.Bool == nil {
		t.Fatal("expected additionalProperties.Bool to be set")
	}
	if *s.AdditionalProperties.Bool != true {
		t.Errorf("expected additionalProperties to be true")
	}
}

func TestAdditionalPropertiesSchema(t *testing.T) {
	input := `{
		"type": "object",
		"additionalProperties": { "type": "string" }
	}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if s.AdditionalProperties == nil {
		t.Fatal("expected additionalProperties to be set")
	}
	if s.AdditionalProperties.Schema == nil {
		t.Fatal("expected additionalProperties.Schema to be set")
	}
	if s.AdditionalProperties.Bool != nil {
		t.Errorf("expected additionalProperties.Bool to be nil")
	}
	if len(s.AdditionalProperties.Schema.Type) != 1 || s.AdditionalProperties.Schema.Type[0] != "string" {
		t.Errorf("expected additionalProperties schema type to be string, got %v", s.AdditionalProperties.Schema.Type)
	}
}

func TestSchemaWithDefs(t *testing.T) {
	input := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://example.com/person",
		"type": "object",
		"properties": {
			"address": { "$ref": "#/$defs/Address" }
		},
		"$defs": {
			"Address": {
				"type": "object",
				"properties": {
					"street": { "type": "string" },
					"city": { "type": "string" }
				},
				"required": ["street", "city"]
			}
		}
	}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if s.ID != "https://example.com/person" {
		t.Errorf("expected $id, got %s", s.ID)
	}
	if s.Schema != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("expected $schema, got %s", s.Schema)
	}
	if s.Defs == nil {
		t.Fatal("expected $defs to be set")
	}
	addr, ok := s.Defs["Address"]
	if !ok {
		t.Fatal("expected Address in $defs")
	}
	if len(addr.Type) != 1 || addr.Type[0] != "object" {
		t.Errorf("expected Address type object, got %v", addr.Type)
	}
	if len(addr.Properties) != 2 {
		t.Errorf("expected 2 properties in Address, got %d", len(addr.Properties))
	}
	if s.Properties["address"] == nil || s.Properties["address"].Ref != "#/$defs/Address" {
		t.Errorf("expected address property to have $ref to Address")
	}
}

func TestSchemaWithDefinitionsDraft07(t *testing.T) {
	input := `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "object",
		"properties": {
			"status": { "$ref": "#/definitions/Status" }
		},
		"definitions": {
			"Status": {
				"type": "string",
				"enum": ["active", "inactive"]
			}
		}
	}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if s.Definitions == nil {
		t.Fatal("expected definitions to be set")
	}
	status, ok := s.Definitions["Status"]
	if !ok {
		t.Fatal("expected Status in definitions")
	}
	if len(status.Type) != 1 || status.Type[0] != "string" {
		t.Errorf("expected Status type string, got %v", status.Type)
	}
	if len(status.Enum) != 2 {
		t.Errorf("expected 2 enum values, got %d", len(status.Enum))
	}
}

func TestDetectDraft07(t *testing.T) {
	s := &Schema{
		Schema: "http://json-schema.org/draft-07/schema#",
	}
	d := DetectDraft(s)
	if d != Draft07 {
		t.Errorf("expected Draft07, got %v", d)
	}
}

func TestDetectDraft202012(t *testing.T) {
	s := &Schema{
		Schema: "https://json-schema.org/draft/2020-12/schema",
	}
	d := DetectDraft(s)
	if d != Draft202012 {
		t.Errorf("expected Draft202012, got %v", d)
	}
}

func TestDetectDraftUnknown(t *testing.T) {
	s := &Schema{}
	d := DetectDraft(s)
	if d != DraftUnknown {
		t.Errorf("expected DraftUnknown, got %v", d)
	}
}

func TestResolveRoot(t *testing.T) {
	s := &Schema{Type: TypeList{"object"}}
	r := NewResolver(s)

	resolved, err := r.Resolve("#")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != s {
		t.Errorf("expected root schema")
	}
}

func TestResolveDefs(t *testing.T) {
	addr := &Schema{Type: TypeList{"object"}, Title: "Address"}
	s := &Schema{
		Defs: map[string]*Schema{
			"Address": addr,
		},
	}
	r := NewResolver(s)

	resolved, err := r.Resolve("#/$defs/Address")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != addr {
		t.Errorf("expected Address schema")
	}
	if resolved.Title != "Address" {
		t.Errorf("expected title Address, got %s", resolved.Title)
	}
}

func TestResolveDefinitions(t *testing.T) {
	status := &Schema{Type: TypeList{"string"}, Title: "Status"}
	s := &Schema{
		Definitions: map[string]*Schema{
			"Status": status,
		},
	}
	r := NewResolver(s)

	resolved, err := r.Resolve("#/definitions/Status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != status {
		t.Errorf("expected Status schema")
	}
}

func TestResolveProperties(t *testing.T) {
	name := &Schema{Type: TypeList{"string"}}
	s := &Schema{
		Properties: map[string]*Schema{
			"name": name,
		},
	}
	r := NewResolver(s)

	resolved, err := r.Resolve("#/properties/name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != name {
		t.Errorf("expected name property schema")
	}
}

func TestResolveUnknownRef(t *testing.T) {
	s := &Schema{}
	r := NewResolver(s)

	_, err := r.Resolve("#/$defs/Missing")
	if err == nil {
		t.Fatal("expected error for missing ref")
	}
}

func TestResolveExternalRefError(t *testing.T) {
	s := &Schema{}
	r := NewResolver(s)

	_, err := r.Resolve("https://example.com/schema.json")
	if err == nil {
		t.Fatal("expected error for external ref")
	}
}

func TestNormalizeDefinitionsToDefs(t *testing.T) {
	status := &Schema{Type: TypeList{"string"}}
	s := &Schema{
		Definitions: map[string]*Schema{
			"Status": status,
		},
	}

	s.Normalize()

	if s.Defs == nil {
		t.Fatal("expected $defs to be populated after normalization")
	}
	if s.Defs["Status"] != status {
		t.Error("expected Status to be copied to $defs")
	}
}

func TestNormalizeDefsToDefinitions(t *testing.T) {
	addr := &Schema{Type: TypeList{"object"}}
	s := &Schema{
		Defs: map[string]*Schema{
			"Address": addr,
		},
	}

	s.Normalize()

	if s.Definitions == nil {
		t.Fatal("expected definitions to be populated after normalization")
	}
	if s.Definitions["Address"] != addr {
		t.Error("expected Address to be copied to definitions")
	}
}

func TestExclusiveMinimumAsNumber(t *testing.T) {
	input := `{
		"type": "number",
		"exclusiveMinimum": 0
	}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if s.ExclusiveMinimum == nil {
		t.Fatal("expected exclusiveMinimum to be set")
	}
	if s.ExclusiveMinimum.Number == nil {
		t.Fatal("expected exclusiveMinimum.Number to be set")
	}
	if *s.ExclusiveMinimum.Number != 0 {
		t.Errorf("expected exclusiveMinimum to be 0, got %f", *s.ExclusiveMinimum.Number)
	}
	if s.ExclusiveMinimum.Bool != nil {
		t.Errorf("expected exclusiveMinimum.Bool to be nil")
	}
}

func TestExclusiveMinimumAsBool(t *testing.T) {
	input := `{
		"type": "number",
		"minimum": 0,
		"exclusiveMinimum": true
	}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if s.ExclusiveMinimum == nil {
		t.Fatal("expected exclusiveMinimum to be set")
	}
	if s.ExclusiveMinimum.Bool == nil {
		t.Fatal("expected exclusiveMinimum.Bool to be set")
	}
	if *s.ExclusiveMinimum.Bool != true {
		t.Errorf("expected exclusiveMinimum to be true")
	}
	if s.ExclusiveMinimum.Number != nil {
		t.Errorf("expected exclusiveMinimum.Number to be nil")
	}
}

func TestItemsSingleSchema(t *testing.T) {
	input := `{
		"type": "array",
		"items": { "type": "string" }
	}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if s.Items == nil {
		t.Fatal("expected items to be set")
	}
	if s.Items.Schema == nil {
		t.Fatal("expected items.Schema to be set")
	}
	if len(s.Items.Schema.Type) != 1 || s.Items.Schema.Type[0] != "string" {
		t.Errorf("expected items schema type string, got %v", s.Items.Schema.Type)
	}
	if s.Items.Schemas != nil {
		t.Errorf("expected items.Schemas to be nil")
	}
}

func TestItemsSchemaArray(t *testing.T) {
	input := `{
		"type": "array",
		"items": [
			{ "type": "string" },
			{ "type": "integer" }
		]
	}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if s.Items == nil {
		t.Fatal("expected items to be set")
	}
	if s.Items.Schemas == nil {
		t.Fatal("expected items.Schemas to be set")
	}
	if len(s.Items.Schemas) != 2 {
		t.Fatalf("expected 2 schemas in items, got %d", len(s.Items.Schemas))
	}
	if s.Items.Schemas[0].Type[0] != "string" {
		t.Errorf("expected first item type string, got %v", s.Items.Schemas[0].Type)
	}
	if s.Items.Schemas[1].Type[0] != "integer" {
		t.Errorf("expected second item type integer, got %v", s.Items.Schemas[1].Type)
	}
	if s.Items.Schema != nil {
		t.Errorf("expected items.Schema to be nil")
	}
}

func TestEnumAndConst(t *testing.T) {
	input := `{
		"type": "string",
		"enum": ["a", "b", "c"],
		"const": "a",
		"default": "b"
	}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(s.Enum) != 3 {
		t.Errorf("expected 3 enum values, got %d", len(s.Enum))
	}
	if s.Const == nil {
		t.Fatal("expected const to be set")
	}
	if *s.Const != "a" {
		t.Errorf("expected const 'a', got %v", *s.Const)
	}
	if s.Default == nil {
		t.Fatal("expected default to be set")
	}
	if *s.Default != "b" {
		t.Errorf("expected default 'b', got %v", *s.Default)
	}
}

func TestNumericConstraints(t *testing.T) {
	input := `{
		"type": "number",
		"minimum": 0,
		"maximum": 100,
		"multipleOf": 5
	}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if s.Minimum == nil || *s.Minimum != 0 {
		t.Errorf("expected minimum 0, got %v", s.Minimum)
	}
	if s.Maximum == nil || *s.Maximum != 100 {
		t.Errorf("expected maximum 100, got %v", s.Maximum)
	}
	if s.MultipleOf == nil || *s.MultipleOf != 5 {
		t.Errorf("expected multipleOf 5, got %v", s.MultipleOf)
	}
}

func TestStringConstraints(t *testing.T) {
	input := `{
		"type": "string",
		"minLength": 1,
		"maxLength": 255,
		"pattern": "^[a-z]+$",
		"format": "email"
	}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if s.MinLength == nil || s.MinLength.Int() != 1 {
		t.Errorf("expected minLength 1, got %v", s.MinLength)
	}
	if s.MaxLength == nil || s.MaxLength.Int() != 255 {
		t.Errorf("expected maxLength 255, got %v", s.MaxLength)
	}
	if s.Pattern == nil || *s.Pattern != "^[a-z]+$" {
		t.Errorf("expected pattern, got %v", s.Pattern)
	}
	if s.Format == nil || *s.Format != "email" {
		t.Errorf("expected format email, got %v", s.Format)
	}
}

func TestConditionalSchema(t *testing.T) {
	input := `{
		"type": "object",
		"if": { "properties": { "type": { "const": "a" } } },
		"then": { "properties": { "value": { "type": "string" } } },
		"else": { "properties": { "value": { "type": "integer" } } }
	}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if s.If == nil {
		t.Fatal("expected if to be set")
	}
	if s.Then == nil {
		t.Fatal("expected then to be set")
	}
	if s.Else == nil {
		t.Fatal("expected else to be set")
	}
}

func TestCompositionKeywords(t *testing.T) {
	input := `{
		"allOf": [
			{ "type": "object" },
			{ "properties": { "name": { "type": "string" } } }
		],
		"anyOf": [
			{ "type": "string" },
			{ "type": "integer" }
		],
		"oneOf": [
			{ "minimum": 0 },
			{ "maximum": 100 }
		],
		"not": { "type": "null" }
	}`

	var s Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(s.AllOf) != 2 {
		t.Errorf("expected 2 allOf schemas, got %d", len(s.AllOf))
	}
	if len(s.AnyOf) != 2 {
		t.Errorf("expected 2 anyOf schemas, got %d", len(s.AnyOf))
	}
	if len(s.OneOf) != 2 {
		t.Errorf("expected 2 oneOf schemas, got %d", len(s.OneOf))
	}
	if s.Not == nil {
		t.Error("expected not to be set")
	}
}

func TestLoadFromFileNotFound(t *testing.T) {
	_, err := LoadFromFile("/nonexistent/path/schema.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadFromFileYAMLUnsupported(t *testing.T) {
	_, err := LoadFromFile("test.yaml")
	if err == nil {
		t.Fatal("expected error for YAML file")
	}
}

func TestDraftString(t *testing.T) {
	tests := []struct {
		draft Draft
		want  string
	}{
		{Draft03, "Draft-03"},
		{Draft04, "Draft-04"},
		{Draft06, "Draft-06"},
		{Draft07, "Draft-07"},
		{Draft201909, "Draft 2019-09"},
		{Draft202012, "Draft 2020-12"},
		{DraftUnknown, "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.draft.String(); got != tt.want {
			t.Errorf("Draft(%d).String() = %s, want %s", tt.draft, got, tt.want)
		}
	}
}

func TestResolverCaching(t *testing.T) {
	addr := &Schema{Type: TypeList{"object"}}
	s := &Schema{
		Defs: map[string]*Schema{
			"Address": addr,
		},
	}
	r := NewResolver(s)

	// Resolve twice to exercise the cache path.
	r1, err := r.Resolve("#/$defs/Address")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r2, err := r.Resolve("#/$defs/Address")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r1 != r2 {
		t.Error("expected same pointer from cache")
	}
}

func TestResolvePropertiesPointer(t *testing.T) {
	// Test full JSON Pointer traversal: #/properties/foo
	foo := &Schema{Type: TypeList{"integer"}}
	s := &Schema{
		Properties: map[string]*Schema{
			"foo": foo,
		},
	}
	r := NewResolver(s)

	resolved, err := r.Resolve("#/properties/foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != foo {
		t.Error("expected foo property schema")
	}
}

func TestResolveAllOfIndex(t *testing.T) {
	inner := &Schema{Type: TypeList{"string"}}
	s := &Schema{
		AllOf: []*Schema{inner},
	}
	r := NewResolver(s)

	resolved, err := r.Resolve("#/allOf/0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != inner {
		t.Error("expected allOf[0] schema")
	}
}

func TestResolveAnchor(t *testing.T) {
	anchored := &Schema{Type: TypeList{"number"}, Anchor: "myanchor"}
	s := &Schema{
		Defs: map[string]*Schema{
			"foo": anchored,
		},
	}
	r := NewResolver(s)

	resolved, err := r.Resolve("#myanchor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != anchored {
		t.Error("expected anchored schema")
	}
}

func TestMappingResolver(t *testing.T) {
	remote := &Schema{Type: TypeList{"integer"}}
	schemas := map[string]*Schema{
		"http://example.com/integer.json": remote,
	}
	mr := NewMappingResolver(schemas)

	resolved, err := mr.ResolveSchema("http://example.com/integer.json", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != remote {
		t.Error("expected remote schema")
	}
}

func TestMappingResolverWithFragment(t *testing.T) {
	inner := &Schema{Type: TypeList{"string"}}
	remote := &Schema{
		Defs: map[string]*Schema{
			"name": inner,
		},
	}
	schemas := map[string]*Schema{
		"http://example.com/schema.json": remote,
	}
	mr := NewMappingResolver(schemas)

	resolved, err := mr.ResolveSchema("http://example.com/schema.json#/$defs/name", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != inner {
		t.Error("expected inner defs schema")
	}
}

func TestCompositeResolver(t *testing.T) {
	// First resolver only knows local refs
	localSchema := &Schema{Type: TypeList{"object"}}
	local := NewLocalResolver(localSchema)

	// Second resolver knows remote refs
	remote := &Schema{Type: TypeList{"integer"}}
	mapping := NewMappingResolver(map[string]*Schema{
		"http://example.com/int.json": remote,
	})

	composite := NewCompositeResolver(local, mapping)

	// Should resolve local ref via first resolver
	resolved, err := composite.ResolveSchema("#", nil)
	if err != nil {
		t.Fatalf("local resolve error: %v", err)
	}
	if resolved != localSchema {
		t.Error("expected local schema")
	}

	// Should fall through to mapping resolver for remote ref
	resolved, err = composite.ResolveSchema("http://example.com/int.json", nil)
	if err != nil {
		t.Fatalf("remote resolve error: %v", err)
	}
	if resolved != remote {
		t.Error("expected remote schema")
	}
}

func TestTypeListMarshalSingle(t *testing.T) {
	tl := TypeList{"string"}
	data, err := tl.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if string(data) != `"string"` {
		t.Errorf("expected \"string\", got %s", string(data))
	}
}

func TestTypeListMarshalMultiple(t *testing.T) {
	tl := TypeList{"string", "null"}
	data, err := tl.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if string(data) != `["string","null"]` {
		t.Errorf("expected [\"string\",\"null\"], got %s", string(data))
	}
}

func TestHTTPResolverBasic(t *testing.T) {
	// Set up a test HTTP server serving a schema.
	schemaJSON := `{
		"type": "object",
		"properties": {
			"name": { "type": "string" }
		}
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(schemaJSON))
	}))
	defer server.Close()

	resolver := NewHTTPResolver(WithHTTPClient(server.Client()))
	// Override the client's transport to route to the test server.
	resolver.client = server.Client()
	// Use the test server's URL directly.
	s, err := resolver.ResolveSchema(server.URL+"/person.json", nil)
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(s.Type) != 1 || s.Type[0] != "object" {
		t.Errorf("expected type [object], got %v", s.Type)
	}
	if _, ok := s.Properties["name"]; !ok {
		t.Error("expected property 'name' in resolved schema")
	}
}

func TestHTTPResolverWithFragment(t *testing.T) {
	// Serve a schema with $defs.
	schemaJSON := `{
		"type": "object",
		"$defs": {
			"Address": {
				"type": "object",
				"properties": {
					"street": { "type": "string" },
					"city": { "type": "string" }
				}
			}
		}
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(schemaJSON))
	}))
	defer server.Close()

	resolver := NewHTTPResolver(WithHTTPClient(server.Client()))
	resolver.client = server.Client()

	// Resolve with fragment pointing to a $def.
	s, err := resolver.ResolveSchema(server.URL+"/schema.json#/$defs/Address", nil)
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(s.Type) != 1 || s.Type[0] != "object" {
		t.Errorf("expected type [object], got %v", s.Type)
	}
	if _, ok := s.Properties["street"]; !ok {
		t.Error("expected property 'street' in resolved schema")
	}
	if _, ok := s.Properties["city"]; !ok {
		t.Error("expected property 'city' in resolved schema")
	}
}

func TestHTTPResolverCaching(t *testing.T) {
	// Count requests to verify caching.
	requestCount := 0
	schemaJSON := `{"type": "string"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(schemaJSON))
	}))
	defer server.Close()

	resolver := NewHTTPResolver(WithHTTPClient(server.Client()))
	resolver.client = server.Client()

	ref := server.URL + "/cached.json"
	// First request.
	s1, err := resolver.ResolveSchema(ref, nil)
	if err != nil {
		t.Fatalf("first resolve error: %v", err)
	}
	// Second request (should be cached).
	s2, err := resolver.ResolveSchema(ref, nil)
	if err != nil {
		t.Fatalf("second resolve error: %v", err)
	}

	if requestCount != 1 {
		t.Errorf("expected 1 HTTP request (caching), got %d", requestCount)
	}
	if s1 != s2 {
		t.Error("expected same schema pointer from cache")
	}
}

func TestHTTPResolverRelativeRef(t *testing.T) {
	// Serve different schemas on different paths.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/schemas/address.json":
			w.Write([]byte(`{"type": "object", "properties": {"zip": {"type": "string"}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	resolver := NewHTTPResolver(WithHTTPClient(server.Client()))
	resolver.client = server.Client()

	// Resolve relative ref against a base URI.
	baseURI, _ := url.Parse(server.URL + "/schemas/person.json")
	s, err := resolver.ResolveSchema("address.json", baseURI)
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if _, ok := s.Properties["zip"]; !ok {
		t.Error("expected property 'zip' in resolved schema")
	}
}

func TestHTTPResolverUnsupportedScheme(t *testing.T) {
	resolver := NewHTTPResolver()
	_, err := resolver.ResolveSchema("file:///etc/passwd", nil)
	if err == nil {
		t.Error("expected error for file:// scheme")
	}
}

func TestHTTPResolverHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	resolver := NewHTTPResolver(WithHTTPClient(server.Client()))
	resolver.client = server.Client()

	_, err := resolver.ResolveSchema(server.URL+"/missing.json", nil)
	if err == nil {
		t.Error("expected error for 404 response")
	}
}

func TestHTTPResolverInComposite(t *testing.T) {
	// Simulate a real workflow: local resolver for fragments, file resolver for
	// local files, HTTP resolver for remote refs.
	schemaJSON := `{"type": "integer", "minimum": 0}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(schemaJSON))
	}))
	defer server.Close()

	localSchema := &Schema{Type: TypeList{"object"}}
	local := NewLocalResolver(localSchema)
	httpResolver := NewHTTPResolver(WithHTTPClient(server.Client()))
	httpResolver.client = server.Client()

	composite := NewCompositeResolver(local, httpResolver)

	// Local ref should work.
	resolved, err := composite.ResolveSchema("#", nil)
	if err != nil {
		t.Fatalf("local resolve error: %v", err)
	}
	if resolved != localSchema {
		t.Error("expected local schema")
	}

	// Remote HTTP ref should work.
	resolved, err = composite.ResolveSchema(server.URL+"/positive_int.json", nil)
	if err != nil {
		t.Fatalf("remote resolve error: %v", err)
	}
	if len(resolved.Type) != 1 || resolved.Type[0] != "integer" {
		t.Errorf("expected type [integer], got %v", resolved.Type)
	}
	if resolved.Minimum == nil || *resolved.Minimum != 0 {
		t.Error("expected minimum 0")
	}
}

func TestFileResolverConfinesToBaseDir(t *testing.T) {
	base := t.TempDir()
	// A schema inside the base directory (allowed).
	if err := os.WriteFile(filepath.Join(base, "leaf.json"),
		[]byte(`{"type":"object","properties":{"x":{"type":"string"}}}`), 0o644); err != nil {
		t.Fatalf("writing leaf: %v", err)
	}
	// A sensitive file outside the base directory (must not be readable via $ref).
	outside := filepath.Join(filepath.Dir(base), "secret.json")
	if err := os.WriteFile(outside, []byte(`{"type":"string"}`), 0o644); err != nil {
		t.Fatalf("writing outside file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })

	r := NewFileResolver(base)

	if _, err := r.ResolveSchema("leaf.json", nil); err != nil {
		t.Fatalf("in-base ref should resolve, got: %v", err)
	}

	for _, ref := range []string{
		"../secret.json",
		"../../secret.json",
		"file://" + outside,
	} {
		if _, err := r.ResolveSchema(ref, nil); err == nil {
			t.Errorf("ref %q escaped base directory but was not refused", ref)
		}
	}
}
