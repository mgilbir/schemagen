package schema

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
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

// TestDetectDraftV1 pins the undated dialect that succeeds the dated drafts.
//
// It matters more than the other five because getting it wrong is silent: v1's
// URI matches none of the "draft-NN" or "draft/YYYY-MM" patterns, so a missing
// case answers DraftUnknown, and DraftUnknown is read as a modern draft nearly
// everywhere -- prefixItems, $ref siblings and integer tokens would all come out
// right, and only the format posture would be wrong. A v1 schema would then stop
// enforcing every format it names and nothing would fail.
//
// The trailing "#" is the spelling MappingResolver strips elsewhere and several
// drafts carry, so both forms are checked. The last two are the near misses:
// neither is the v1 dialect and neither may answer DraftV1.
func TestDetectDraftV1(t *testing.T) {
	for _, tt := range []struct {
		uri  string
		want Draft
	}{
		{"https://json-schema.org/v1", DraftV1},
		{"https://json-schema.org/v1#", DraftV1},
		{"http://json-schema.org/v1", DraftV1},
		{"https://json-schema.org/draft/2020-12/schema", Draft202012},
		{"https://example.test/v1", DraftUnknown},
	} {
		t.Run(tt.uri, func(t *testing.T) {
			if got := DetectDraft(&Schema{Schema: tt.uri}); got != tt.want {
				t.Errorf("DetectDraft(%q) = %v, want %v", tt.uri, got, tt.want)
			}
		})
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

// A pointer in a fragment is percent-decoded before it is read as a pointer,
// and the resolver is where that order is decided for everything downstream.
//
// Every case below is a key that one spelling reaches and another spelling of
// the same characters does not, so a resolver that took the two decodings in
// the other order would not fail to resolve -- it would resolve to the *other*
// definition and hand back a schema. Nothing about the answer would look wrong.
// That is the shape of issue #305, and this file had no case that could see it:
// with the order reversed inside UnescapePointerToken, the whole of pkg/schema
// still passed.
func TestResolvePercentEscapedPointerToken(t *testing.T) {
	slash := &Schema{Type: TypeList{"string"}, Title: "slash"}
	tildeOne := &Schema{Type: TypeList{"integer"}, Title: "tilde-one"}
	tilde := &Schema{Type: TypeList{"boolean"}, Title: "tilde"}
	tildeZero := &Schema{Type: TypeList{"number"}, Title: "tilde-zero"}
	s := &Schema{Defs: map[string]*Schema{
		"/":  slash,
		"~1": tildeOne,
		"~":  tilde,
		"~0": tildeZero,
	}}
	r := NewResolver(s)

	for _, tt := range []struct {
		ref  string
		want *Schema
	}{
		// "%7E1" decodes to "~1", which then names "/". Unescaping first finds
		// no literal "~1" and would name the key "~1" instead.
		{"#/$defs/%7E1", slash},
		{"#/$defs/~1", slash},
		// The key literally called "~1" is written "~01" either way.
		{"#/$defs/~01", tildeOne},
		// The same pair one escape along.
		{"#/$defs/%7E0", tilde},
		{"#/$defs/~0", tilde},
		{"#/$defs/~00", tildeZero},
		// A percent-escaped separator is a character of the key, not a step of
		// the pointer: "%2F" is the key "/" and does not descend anywhere.
		{"#/$defs/%2F", slash},
	} {
		resolved, err := r.Resolve(tt.ref)
		if err != nil {
			t.Errorf("Resolve(%q): unexpected error: %v", tt.ref, err)
			continue
		}
		if resolved != tt.want {
			t.Errorf("Resolve(%q) reached %q, want %q", tt.ref, resolved.Title, tt.want.Title)
		}
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
	if *s.ExclusiveMinimum.Number != "0" {
		t.Errorf("expected exclusiveMinimum to be 0, got %s", *s.ExclusiveMinimum.Number)
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

	if s.Minimum == nil || *s.Minimum != "0" {
		t.Errorf("expected minimum 0, got %v", s.Minimum)
	}
	if s.Maximum == nil || *s.Maximum != "100" {
		t.Errorf("expected maximum 100, got %v", s.Maximum)
	}
	if s.MultipleOf == nil || *s.MultipleOf != "5" {
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
		{DraftV1, "v1"},
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
	if resolved.Minimum == nil || *resolved.Minimum != "0" {
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

func TestCompositeResolverJoinsErrors(t *testing.T) {
	// FileResolver will fail (no such file); HTTPResolver will fail (scheme).
	// The joined error must surface the file resolver's message, not only the
	// last resolver's.
	fileR := NewFileResolver(t.TempDir())
	httpR := NewHTTPResolver()
	c := NewCompositeResolver(fileR, httpR)

	_, err := c.ResolveSchema("missing.json", nil)
	if err == nil {
		t.Fatal("expected error resolving a missing file")
	}
	msg := err.Error()
	if !strings.Contains(msg, "FileResolver") {
		t.Errorf("joined error should include the file resolver failure, got: %v", err)
	}
	if !strings.Contains(msg, "missing.json") {
		t.Errorf("joined error should reference the ref, got: %v", err)
	}
}

// TestFileResolverRefusesSymlinkEscape covers the case a purely lexical prefix
// check misses: a symlink that lives inside the base directory but points
// outside it. The lexical form of the path is confined; the file it names is
// not, so confinement has to resolve symlinks.
func TestFileResolverRefusesSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	outsideDir := t.TempDir()

	outside := filepath.Join(outsideDir, "secret.json")
	if err := os.WriteFile(outside, []byte(`{"type":"string"}`), 0o644); err != nil {
		t.Fatalf("writing outside file: %v", err)
	}

	// A symlink inside the base pointing at the file outside it.
	link := filepath.Join(base, "link.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// A symlinked *directory* inside the base, reached by a normal-looking path.
	dirLink := filepath.Join(base, "sub")
	if err := os.Symlink(outsideDir, dirLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	r := NewFileResolver(base)
	for _, ref := range []string{"link.json", "sub/secret.json"} {
		if _, err := r.ResolveSchema(ref, nil); err == nil {
			t.Errorf("ref %q escaped the base directory via symlink but was not refused", ref)
		}
	}

	// A real file inside the base must still resolve, including when the base
	// directory itself is reached through a symlink (macOS /var → /private/var).
	if err := os.WriteFile(filepath.Join(base, "leaf.json"), []byte(`{"type":"object"}`), 0o644); err != nil {
		t.Fatalf("writing leaf: %v", err)
	}
	if _, err := r.ResolveSchema("leaf.json", nil); err != nil {
		t.Fatalf("in-base ref should resolve, got: %v", err)
	}
}

// TestHTTPResolverCapsResponseSize covers the unbounded io.ReadAll: a hostile
// endpoint could otherwise exhaust memory during generation.
func TestHTTPResolverCapsResponseSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"type":"object","description":"` + strings.Repeat("x", 4096) + `"}`))
	}))
	defer server.Close()

	small := NewHTTPResolver(WithHTTPClient(server.Client()), WithMaxResponseBytes(512))
	if _, err := small.ResolveSchema(server.URL+"/big.json", nil); err == nil {
		t.Fatal("oversized response was accepted, want an error")
	} else if !strings.Contains(err.Error(), "response limit") {
		t.Fatalf("error = %v, want it to mention the response limit", err)
	}

	// The same document is fine under a cap that accommodates it.
	big := NewHTTPResolver(WithHTTPClient(server.Client()), WithMaxResponseBytes(1<<20))
	if _, err := big.ResolveSchema(server.URL+"/big.json", nil); err != nil {
		t.Fatalf("resolve under a sufficient cap failed: %v", err)
	}
}

// TestHTTPResolverRejectsNonJSONContentType keeps an HTML error page from being
// reported as a JSON parse failure.
func TestHTTPResolverRejectsNonJSONContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><body>not found</body></html>`))
	}))
	defer server.Close()

	r := NewHTTPResolver(WithHTTPClient(server.Client()))
	if _, err := r.ResolveSchema(server.URL+"/schema.json", nil); err == nil {
		t.Fatal("HTML response was accepted, want an error")
	} else if !strings.Contains(err.Error(), "Content-Type") {
		t.Fatalf("error = %v, want it to name the Content-Type", err)
	}
}

// TestHTTPResolverAcceptsJSONContentTypeVariants guards the Content-Type check
// against being too strict: schema hosts commonly use +json suffixes, and some
// send no Content-Type at all.
func TestHTTPResolverAcceptsJSONContentTypeVariants(t *testing.T) {
	for _, ct := range []string{
		"application/json",
		"application/json; charset=utf-8",
		"application/schema+json",
		"Application/JSON",
		"", // omitted entirely
	} {
		t.Run("ct="+ct, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if ct != "" {
					w.Header().Set("Content-Type", ct)
				} else {
					// Stop Go from sniffing a Content-Type onto the response.
					w.Header()["Content-Type"] = nil
				}
				w.Write([]byte(`{"type":"object"}`))
			}))
			defer server.Close()

			r := NewHTTPResolver(WithHTTPClient(server.Client()))
			if _, err := r.ResolveSchema(server.URL+"/schema.json", nil); err != nil {
				t.Fatalf("Content-Type %q was rejected: %v", ct, err)
			}
		})
	}
}

// TestHTTPResolverRefusesHTTPSDowngradeRedirect covers the redirect policy: a
// remote schema must not be able to move the fetch onto a plaintext connection.
func TestHTTPResolverRefusesHTTPSDowngradeRedirect(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"type":"string"}`))
	}))
	defer plain.Close()

	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/downgraded.json", http.StatusFound)
	}))
	defer tls.Close()

	r := NewHTTPResolver(WithHTTPClient(tls.Client()))
	if _, err := r.ResolveSchema(tls.URL+"/schema.json", nil); err == nil {
		t.Fatal("https→http redirect was followed, want an error")
	} else if !strings.Contains(err.Error(), "refusing redirect") {
		t.Fatalf("error = %v, want it to mention the refused redirect", err)
	}
}

// TestHTTPResolverBoundsRedirectChain covers the hop limit on a redirect loop.
func TestHTTPResolverBoundsRedirectChain(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL+"/next.json", http.StatusFound)
	}))
	defer server.Close()

	r := NewHTTPResolver(WithHTTPClient(server.Client()))
	if _, err := r.ResolveSchema(server.URL+"/schema.json", nil); err == nil {
		t.Fatal("redirect loop was followed indefinitely, want an error")
	} else if !strings.Contains(err.Error(), "stopped after") {
		t.Fatalf("error = %v, want it to mention the redirect limit", err)
	}
}

// TestLocalResolverRefIntoBooleanKeyword covers JSON-pointer refs that land on a
// boolean-valued keyword. Booleans are schemas in draft 6+, so
// "#/additionalProperties" against {"additionalProperties": false} must resolve
// to the false schema rather than reporting that no schema is there.
func TestLocalResolverRefIntoBooleanKeyword(t *testing.T) {
	root := &Schema{}
	if err := json.Unmarshal([]byte(`{
		"type": "object",
		"additionalProperties": false,
		"additionalItems": true
	}`), root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	root.Normalize()

	r := NewLocalResolver(root)

	ap, err := r.ResolveLocal("#/additionalProperties")
	if err != nil {
		t.Fatalf("resolving #/additionalProperties: %v", err)
	}
	if !ap.IsFalseSchema() {
		t.Errorf("#/additionalProperties = %#v, want the false schema", ap)
	}

	ai, err := r.ResolveLocal("#/additionalItems")
	if err != nil {
		t.Fatalf("resolving #/additionalItems: %v", err)
	}
	if !ai.IsTrueSchema() {
		t.Errorf("#/additionalItems = %#v, want the true schema", ai)
	}

	// Repeated resolution must return the same node: cycle detection compares
	// schema pointers, so a fresh node each time would defeat it.
	again, err := r.ResolveLocal("#/additionalProperties")
	if err != nil {
		t.Fatalf("re-resolving #/additionalProperties: %v", err)
	}
	if again != ap {
		t.Errorf("repeated resolution returned a different node (%p vs %p)", again, ap)
	}
}

// TestLocalResolverExtensionRefIsStableAndNormalized covers the two problems
// with re-parsing an Extensions entry on every resolution: the nodes had
// distinct identities, and they never went through Normalize, so legacy
// constructs inside an extension stayed un-canonicalized.
func TestLocalResolverExtensionRefIsStableAndNormalized(t *testing.T) {
	root := &Schema{}
	if err := json.Unmarshal([]byte(`{
		"type": "object",
		"x-shared": {"type": "object", "divisibleBy": 3, "extends": {"type": "object"}}
	}`), root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	root.Normalize()

	r := NewLocalResolver(root)

	first, err := r.ResolveLocal("#/x-shared")
	if err != nil {
		t.Fatalf("resolving #/x-shared: %v", err)
	}
	second, err := r.ResolveLocal("#/x-shared")
	if err != nil {
		t.Fatalf("re-resolving #/x-shared: %v", err)
	}
	if first != second {
		t.Errorf("two refs to the same extension returned different nodes (%p vs %p)", first, second)
	}

	// Normalize maps the draft-3/4 spellings onto their modern equivalents.
	if first.MultipleOf == nil {
		t.Errorf("divisibleBy inside an extension was not normalized to multipleOf")
	}
	if len(first.AllOf) == 0 {
		t.Errorf("extends inside an extension was not normalized to allOf")
	}
}

// Drafts before 2019-09 had no "$anchor": a location-independent identifier was
// written as {"id": "#foo"}. LocalResolver must find it, because a document
// reached through a SchemaResolver is searched here rather than through the
// generator's own anchor index (which does understand "id", but only covers the
// root document).
func TestLocalResolverFindsLegacyIDAnchor(t *testing.T) {
	var s Schema
	if err := json.Unmarshal([]byte(`{
		"definitions": {
			"refToInteger": {"$ref": "#foo"},
			"A": {"id": "#foo", "type": "integer"}
		}
	}`), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()

	got, err := NewLocalResolver(&s).Resolve("#foo")
	if err != nil {
		t.Fatalf("resolving legacy id anchor: %v", err)
	}
	if len(got.Type) != 1 || got.Type[0] != "integer" {
		t.Errorf("resolved to the wrong node: type = %v, want [integer]", got.Type)
	}
}

// A plain-name fragment id names a node inside the current scope, so the search
// must descend into it. Only a scope-changing id (an actual URI) hides its
// subtree from the parent's anchor search.
func TestLocalResolverAnchorScoping(t *testing.T) {
	var s Schema
	if err := json.Unmarshal([]byte(`{
		"definitions": {
			"inFragmentScope": {"id": "#named", "definitions": {
				"nested": {"$anchor": "reachable", "type": "string"}
			}},
			"inOwnScope": {"id": "http://example.com/other.json", "definitions": {
				"nested": {"$anchor": "hidden", "type": "string"}
			}}
		}
	}`), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	r := NewLocalResolver(&s)

	if _, err := r.Resolve("#named"); err != nil {
		t.Errorf("a plain-name fragment id should be findable: %v", err)
	}
	if _, err := r.Resolve("#reachable"); err != nil {
		t.Errorf("an anchor under a fragment-id node stays in the parent scope: %v", err)
	}
	if _, err := r.Resolve("#hidden"); err == nil {
		t.Error("an anchor under a scope-changing id must not leak into the parent scope")
	}
}

// A $ref may point inside a keyword whose value is not itself a schema.
// {"examples":[{"type":"string"}]} referenced as "#/examples/0" needs the array
// indexed before the element is parsed; parsing the whole keyword value failed
// with "cannot unmarshal array into Go value of type schemaAlias".
func TestLocalResolverRefIntoExtensionCollection(t *testing.T) {
	tests := []struct {
		name     string
		doc      string
		ref      string
		wantType string
	}{
		{"array element", `{"$id":"/base","examples":[{"type":"string"}]}`, "#/examples/0", "string"},
		{"later array element", `{"examples":[{"type":"integer"},{"type":"string"}]}`, "#/examples/1", "string"},
		{"object member", `{"x-defs":{"a":{"type":"boolean"}}}`, "#/x-defs/a", "boolean"},
		// A vendor keyword whose value *is* a schema still resolves through the
		// schema path, with the remaining tokens naming schema fields.
		{"schema-valued keyword", `{"x-thing":{"properties":{"p":{"type":"number"}}}}`, "#/x-thing/properties/p", "number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s Schema
			if err := json.Unmarshal([]byte(tt.doc), &s); err != nil {
				t.Fatal(err)
			}
			s.Normalize()
			got, err := NewLocalResolver(&s).Resolve(tt.ref)
			if err != nil {
				t.Fatalf("resolving %s: %v", tt.ref, err)
			}
			if len(got.Type) != 1 || got.Type[0] != tt.wantType {
				t.Errorf("type = %v, want [%s]", got.Type, tt.wantType)
			}
		})
	}
}

// Extension subschemas are memoized so that repeated refs yield the same node
// (cycle detection compares pointers). The memo key must include the pointer
// path, or distinct array elements would alias onto whichever was parsed first.
func TestLocalResolverExtensionElementsDoNotAlias(t *testing.T) {
	var s Schema
	if err := json.Unmarshal([]byte(`{"examples":[{"type":"integer"},{"type":"string"}]}`), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	r := NewLocalResolver(&s)

	first, err := r.Resolve("#/examples/0")
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Resolve("#/examples/1")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("distinct array elements resolved to the same node")
	}
	again, err := r.Resolve("#/examples/0")
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Error("the same pointer must resolve to the same node on re-resolution")
	}
}

// Out-of-range and non-existent members are errors, not silent successes.
func TestLocalResolverRefIntoExtensionCollectionErrors(t *testing.T) {
	var s Schema
	if err := json.Unmarshal([]byte(`{"examples":[{"type":"string"}],"x-defs":{"a":{"type":"boolean"}}}`), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	r := NewLocalResolver(&s)
	for _, ref := range []string{"#/examples/5", "#/examples/notanindex", "#/x-defs/missing"} {
		if _, err := r.Resolve(ref); err == nil {
			t.Errorf("%s should not resolve", ref)
		}
	}
}

// TestResolveOverflowingIndexIsRefused covers array-index tokens too large for
// an int. Accumulating them digit by digit wraps the value negative, and every
// bound check in walkPath is a one-sided "idx >= len", which a negative index
// passes -- so the subscript that followed panicked with "index out of range
// [-9219744073709551616]" instead of reporting an unresolvable ref. The fuzzer
// found it as {"items":[],"properties":{"":{"$ref":"#/items/9227000000000000000"}}}.
//
// The test asserts an error rather than a value: no array has such an element,
// so refusing the ref is the same answer the bound check meant to give.
func TestResolveOverflowingIndexIsRefused(t *testing.T) {
	inner := &Schema{Type: TypeList{"string"}}
	s := &Schema{
		Items:       &SchemaOrSchemaArray{Schemas: []*Schema{inner}},
		PrefixItems: []*Schema{inner},
		AllOf:       []*Schema{inner},
		AnyOf:       []*Schema{inner},
		OneOf:       []*Schema{inner},
	}
	r := NewResolver(s)

	// 9227000000000000000 is just past MaxInt64, so it wraps negative; the
	// twenty-nines form overflows further and must be refused just the same.
	refs := []string{
		"#/items/9227000000000000000",
		"#/prefixItems/9227000000000000000",
		"#/allOf/9227000000000000000",
		"#/anyOf/9227000000000000000",
		"#/oneOf/9227000000000000000",
		"#/items/18446744073709551617",
		"#/items/99999999999999999999",
	}
	for _, ref := range refs {
		if _, err := r.Resolve(ref); err == nil {
			t.Errorf("expected error for out-of-range index ref %q", ref)
		}
	}
}

// TestResolveIndexStillWorksAtTheBoundary guards the overflow check from
// overreaching: an index that fits must still resolve, and one that merely
// exceeds the slice must still report out of range rather than being rejected
// as unparseable.
func TestResolveIndexStillWorksAtTheBoundary(t *testing.T) {
	inner := &Schema{Type: TypeList{"string"}}
	s := &Schema{AllOf: []*Schema{inner}}
	r := NewResolver(s)

	resolved, err := r.Resolve("#/allOf/0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != inner {
		t.Error("expected allOf[0] schema")
	}
	if _, err := r.Resolve("#/allOf/1"); err == nil {
		t.Fatal("expected error for index past the end")
	}
}

// TestKeywordsMarshaledFormOmitsFindsTheHiddenSpellings pins the three keyword
// spellings that do not survive a round trip through JSON.
//
// Each is a keyword the schema states and the marshaled form does not show, so
// anything reading the marshaled key set alone sees a schema stating nothing.
// The controls beside them are the spellings that do survive: a named "type" and
// a non-null const are already visible and must not be reported here, and a
// schema stating neither must report nothing at all.
func TestKeywordsMarshaledFormOmitsFindsTheHiddenSpellings(t *testing.T) {
	strConst := any("a")
	cases := []struct {
		name   string
		schema Schema
		want   []string
	}{
		{"emptyEnum", Schema{Enum: []any{}}, []string{"enum"}},
		{"constNull", Schema{ConstIsNull: true}, []string{"const"}},
		{"schemaValuedType", Schema{TypeSchemas: []*Schema{{Type: TypeList{"string"}}}}, []string{"type"}},
		{"nonEmptyEnum", Schema{Enum: []any{"a"}}, []string{"enum"}},
		{"namedType", Schema{Type: TypeList{"string"}}, nil},
		{"stringConst", Schema{Const: &strConst}, nil},
		{"statesNothing", Schema{}, nil},
		{"minLengthOnly", Schema{MinLength: flexIntPtr(3)}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.schema.KeywordsMarshaledFormOmits()
			if len(got) != len(tc.want) {
				t.Fatalf("KeywordsMarshaledFormOmits() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("KeywordsMarshaledFormOmits() = %v, want %v", got, tc.want)
				}
			}
		})
	}
	if (*Schema)(nil).KeywordsMarshaledFormOmits() != nil {
		t.Fatal("KeywordsMarshaledFormOmits() on a nil schema must be nil")
	}
}

// TestSchemaFieldsAreClassifiedForPresence is what keeps
// KeywordsMarshaledFormOmits from lagging the struct it reads.
//
// Deciding what a schema states from its marshaled key set is fail-closed for
// every field JSON can carry, and blind to every field it cannot. Three such
// fields have each produced a defect already (issues #142 and #154), and the way
// a fourth arrives is somebody adding a field tagged `json:"-"`, or one whose
// omitempty hides a value that means something when empty, and nobody noticing
// that a predicate two packages away now reads it as absent.
//
// So every field is classified here and the default is failure. A new field with
// no entry fails this test until its author decides which kind it is, and a field
// classified as a hidden assertion that KeywordsMarshaledFormOmits does not
// report fails the last block below.
func TestSchemaFieldsAreClassifiedForPresence(t *testing.T) {
	// hiddenAssertions are the fields whose presence the marshaled form erases
	// and which assert something. KeywordsMarshaledFormOmits must report each.
	hiddenAssertions := map[string]string{
		"Enum":        "enum",  // omitempty: `"enum": []` admits nothing and marshals to nothing
		"ConstIsNull": "const", // json:"-": the only record that `"const": null` was written
		"TypeSchemas": "type",  // json:"-": draft 3 schema-valued entries of a "type" array
	}
	// notKeywords are the fields the marshaled form also erases and which state
	// nothing a keyword reader needs. The value is the reason.
	notKeywords := map[string]string{
		"BooleanSchema":    "a bare true/false; every reader asks IsBooleanSchema first, and it has no keyword name to report",
		"Extensions":       "unknown keywords, unioned in by name at each reader that wants them",
		"extensionSchemas": "a parse cache for Extensions",
		"DetectedDraft":    "which draft the document was read under, not something it asserts",
		"BaseURI":          "where a relative $ref resolves from",
		"DocumentRoot":     "where a JSON Pointer fragment resolves from",
		"RetrievalURI":     "which URL answered a fetch, which is where a relative $ref resolves from when the document declares no $id",
	}
	// emptyIsAbsent are the slice and map fields whose omitempty tag drops an
	// empty value and for which that is the right reading: written empty they
	// assert nothing, so nothing is lost. Enum is the one that is not here.
	emptyIsAbsent := map[string]bool{
		"Vocabulary": true, "Type": true, "AllOf": true, "AnyOf": true, "OneOf": true,
		"Properties": true, "Required": true, "PatternProperties": true,
		"PrefixItems": true, "Definitions": true, "Defs": true,
		"DependentSchemas": true, "DependentRequired": true,
		"Extends": true, "Disallow": true, "Dependencies": true,
	}

	tp := reflect.TypeOf(Schema{})
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		tag := f.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")

		if tag == "-" || tag == "" {
			if _, ok := hiddenAssertions[f.Name]; ok {
				continue
			}
			if _, ok := notKeywords[f.Name]; ok {
				continue
			}
			t.Fatalf("field %s is invisible to a marshaled schema and is classified neither as a hidden assertion "+
				"nor as a non-keyword. Every gate that decides what a schema states reads the marshaled key set, so "+
				"an unclassified field of this shape is read as absent -- add it to hiddenAssertions (and to "+
				"Schema.KeywordsMarshaledFormOmits) or to notKeywords with the reason it states nothing.", f.Name)
		}

		if strings.Contains(opts, "omitempty") {
			switch f.Type.Kind() {
			case reflect.Slice, reflect.Map:
				if _, hidden := hiddenAssertions[f.Name]; hidden {
					continue
				}
				if emptyIsAbsent[f.Name] {
					continue
				}
				t.Fatalf("field %s (keyword %q) is a slice or map tagged omitempty, so written empty it marshals to "+
					"nothing. Say which that is: add it to emptyIsAbsent if an empty value asserts nothing, or to "+
					"hiddenAssertions (and to Schema.KeywordsMarshaledFormOmits) if it asserts something the way "+
					"`\"enum\": []` does.", f.Name, name)
			}
		}
	}

	// A classification is only worth having if it has to agree with the code. So
	// every classified field is set on a schema of its own and the method's answer
	// is held to what the classification says it should be -- reported for a
	// hidden assertion, silent for everything else.
	//
	// Both directions matter. Without the first, a field called a hidden assertion
	// that KeywordsMarshaledFormOmits never learned about is read as absent by
	// every gate. Without the second, moving `Enum` to emptyIsAbsent passes while
	// the method goes on reporting it -- the inventory then says `"enum": []`
	// asserts nothing, which is exactly the reading issue #142 was, sitting in a
	// test that claims to have checked it.
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		tag := f.Tag.Get("json")
		_, opts, _ := strings.Cut(tag, ",")
		hiddenTag := tag == "-" || tag == ""
		emptyTag := strings.Contains(opts, "omitempty") &&
			(f.Type.Kind() == reflect.Slice || f.Type.Kind() == reflect.Map)
		if !hiddenTag && !emptyTag {
			continue
		}

		s := reflect.New(tp).Elem()
		v := s.Field(i)
		if !v.CanSet() {
			// An unexported field, which no keyword reader can be asked about.
			continue
		}
		switch f.Type.Kind() {
		case reflect.Slice:
			// Empty for the omitempty question ("written empty, does it assert?"),
			// one element for the json:"-" question ("present at all").
			if emptyTag {
				v.Set(reflect.MakeSlice(f.Type, 0, 0))
			} else {
				v.Set(reflect.MakeSlice(f.Type, 1, 1))
			}
		case reflect.Map:
			v.Set(reflect.MakeMap(f.Type))
		case reflect.Bool:
			v.SetBool(true)
		case reflect.String:
			v.SetString("x")
		case reflect.Ptr:
			v.Set(reflect.New(f.Type.Elem()))
		case reflect.Int, reflect.Int64:
			v.SetInt(1)
		default:
			t.Fatalf("field %s has kind %s, which this test does not know how to set; the classification below "+
				"is unchecked until it does", f.Name, f.Type.Kind())
		}

		got := s.Addr().Interface().(*Schema).KeywordsMarshaledFormOmits()
		if keyword, hidden := hiddenAssertions[f.Name]; hidden {
			if len(got) != 1 || got[0] != keyword {
				t.Fatalf("KeywordsMarshaledFormOmits() for a schema stating only %s = %v, want [%q]: the "+
					"classification calls the field a hidden assertion and the method does not report it",
					f.Name, got, keyword)
			}
			continue
		}
		if len(got) != 0 {
			t.Fatalf("KeywordsMarshaledFormOmits() for a schema stating only %s = %v, want nothing: the "+
				"classification says this field asserts nothing the marshaled form hides, and the method "+
				"disagrees. One of the two is wrong, and a gate reading the method is what decides documents",
				f.Name, got)
		}
	}
}

func flexIntPtr(v int) *FlexInt {
	f := FlexInt(v)
	return &f
}

// marshaledKeywordsBySerializing is the reading MarshaledKeywords replaced: send
// the schema through its own MarshalJSON and decode the result back into an
// object. It is the definition of the answer, and it is kept here as the oracle
// the fast implementation is held to, because the fast one is only worth having
// while the two agree.
//
// It is not usable outside a test. It costs the size of the node's whole subtree
// per call, which is what made the generator cubic in nesting depth and left the
// fuzz gate unable to get past its own seed corpus -- issue #233.
func marshaledKeywordsBySerializing(s *Schema) (map[string]bool, bool) {
	if s == nil {
		return nil, false
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, false
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
		return nil, false
	}
	seen := make(map[string]bool, len(present))
	for key := range present {
		seen[key] = true
	}
	return seen, true
}

// nonZeroFieldValues holds a value for the field types where the generic
// construction below would build something that is non-empty but not valid JSON.
// A json.RawMessage of one zero byte is exactly that: encoding/json refuses it,
// so the oracle would report "cannot be marshaled" and the comparison would be
// against nothing.
var nonZeroFieldValues = map[reflect.Type]any{
	reflect.TypeOf(json.RawMessage(nil)): json.RawMessage(`1`),
}

// nonZeroFieldValue builds a value of type t that encoding/json will not drop
// for omitempty: a non-nil pointer, a one-element slice or map, a non-empty
// string. What it holds does not matter -- omitempty asks about the container.
func nonZeroFieldValue(t reflect.Type) (reflect.Value, bool) {
	if v, ok := nonZeroFieldValues[t]; ok {
		return reflect.ValueOf(v), true
	}
	switch t.Kind() {
	case reflect.Pointer:
		return reflect.New(t.Elem()), true
	case reflect.Slice:
		return reflect.MakeSlice(t, 1, 1), true
	case reflect.Map:
		m := reflect.MakeMap(t)
		key, ok := nonZeroFieldValue(t.Key())
		if !ok {
			return reflect.Value{}, false
		}
		m.SetMapIndex(key, reflect.Zero(t.Elem()))
		return m, true
	case reflect.String:
		return reflect.ValueOf("x").Convert(t), true
	case reflect.Bool:
		return reflect.ValueOf(true).Convert(t), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflect.ValueOf(int64(1)).Convert(t), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return reflect.ValueOf(uint64(1)).Convert(t), true
	case reflect.Float32, reflect.Float64:
		return reflect.ValueOf(float64(1)).Convert(t), true
	case reflect.Struct:
		return reflect.Zero(t), true
	}
	return reflect.Value{}, false
}

// TestMarshaledKeywordsMatchesMarshaling is what makes MarshaledKeywords worth
// trusting.
//
// The method answers "which top-level keys would marshaling this schema produce"
// without marshaling anything, by reading encoding/json's own rules off the
// struct tags. That is only sound while it agrees with encoding/json, and the two
// can drift for reasons nobody in this package controls: a field added with a tag
// option the reader does not model, a field type that grows an IsZero method, a
// change in what the encoder considers empty. So every field is compared against
// a real marshal, one at a time and all together, and the corpus is compared node
// by node.
//
// The gates in pkg/generator that decide what a schema states are built on this
// reading. A field this method silently stops reporting is a constraint the
// generator silently stops enforcing.
func TestMarshaledKeywordsMatchesMarshaling(t *testing.T) {
	same := func(t *testing.T, what string, s *Schema) {
		t.Helper()
		want, wantOK := marshaledKeywordsBySerializing(s)
		got, gotOK := s.MarshaledKeywords()
		if wantOK != gotOK {
			t.Fatalf("%s: MarshaledKeywords reported ok=%v, marshaling reported ok=%v", what, gotOK, wantOK)
		}
		if !wantOK {
			return
		}
		if !reflect.DeepEqual(want, got) {
			for key := range want {
				if !got[key] {
					t.Errorf("%s: MarshaledKeywords is missing %q, which marshaling writes. Every gate that "+
						"decides what a schema states reads this set, so a keyword missing here is a "+
						"constraint the generator stops seeing", what, key)
				}
			}
			for key := range got {
				if !want[key] {
					t.Errorf("%s: MarshaledKeywords reports %q, which marshaling does not write", what, key)
				}
			}
			t.FailNow()
		}
	}

	t.Run("no key set at all", func(t *testing.T) {
		if _, ok := (*Schema)(nil).MarshaledKeywords(); ok {
			t.Fatal("MarshaledKeywords on a nil schema must report ok=false: the set is unknown, not empty")
		}
		for _, b := range []bool{true, false} {
			s := &Schema{BooleanSchema: &b}
			// The oracle agrees for its own reason: `true` and `false` do not
			// decode into an object, so it cannot read a key set off them either.
			if _, ok := marshaledKeywordsBySerializing(s); ok {
				t.Fatalf("a %v boolean schema marshals to an object?", b)
			}
			if _, ok := s.MarshaledKeywords(); ok {
				t.Fatalf("MarshaledKeywords on the %v boolean schema must report ok=false: `%v` states "+
					"everything about the values it admits and names no keyword to say it with", b, b)
			}
		}
	})

	tp := reflect.TypeOf(Schema{})
	all := &Schema{}
	allValue := reflect.ValueOf(all).Elem()

	t.Run("one field at a time", func(t *testing.T) {
		for i := range tp.NumField() {
			f := tp.Field(i)
			tag := f.Tag.Get("json")
			if tag == "" || tag == "-" || !f.IsExported() {
				continue
			}
			value, ok := nonZeroFieldValue(f.Type)
			if !ok {
				t.Fatalf("field %s is of type %s, which this test does not know how to fill. It cannot "+
					"compare MarshaledKeywords against marshaling for a field it cannot set, so teach "+
					"nonZeroFieldValue that kind rather than leaving the field unchecked", f.Name, f.Type)
			}

			// Zero, then set. Both directions matter: a reader that reported every
			// tagged field unconditionally would pass the second check alone.
			bare := &Schema{}
			same(t, "no field set, asking about "+f.Name, bare)

			one := &Schema{}
			reflect.ValueOf(one).Elem().Field(i).Set(value)
			if _, ok := marshaledKeywordsBySerializing(one); !ok {
				t.Fatalf("a schema stating only %s does not marshal, so this test has no oracle for it. "+
					"Give nonZeroFieldValues an entry for %s that marshals", f.Name, f.Type)
			}
			same(t, "only "+f.Name+" set", one)

			allValue.Field(i).Set(value)
		}
	})

	t.Run("every field at once", func(t *testing.T) {
		if _, ok := marshaledKeywordsBySerializing(all); !ok {
			t.Fatal("a schema with every field set does not marshal, so this test has no oracle for it")
		}
		same(t, "every field set", all)
	})

	t.Run("corpus", func(t *testing.T) {
		root := filepath.Join("..", "..", "testdata", "schemas")
		files := 0
		nodes := 0
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var s Schema
			if json.Unmarshal(data, &s) != nil {
				return nil // not a schema document; the corpus holds malformed ones on purpose
			}
			files++
			eachSchemaNode(reflect.ValueOf(&s), map[*Schema]bool{}, func(node *Schema) {
				nodes++
				// The oracle is asked about the node with its subschemas pruned,
				// and the answer is held against MarshaledKeywords on the node
				// itself. Pruning is what keeps this affordable: the oracle costs
				// the size of the whole subtree, so asking it at all 2000 levels
				// of testdata/schemas/adversarial/deep/deep-not-2000.json is the
				// very cubic behaviour issue #233 was. Nothing is given up. The
				// values a subschema keyword holds are schemas, and each is
				// compared in its own right when the walk reaches it; every other
				// field keeps the value the document wrote, which is the part a
				// marshal can disagree about. And the comparison is across the
				// pruning, so a prune that changed which keywords are present
				// fails here rather than hiding a difference.
				want, wantOK := marshaledKeywordsBySerializing(pruneSubschemas(node))
				got, gotOK := node.MarshaledKeywords()
				if wantOK != gotOK || !reflect.DeepEqual(want, got) {
					t.Fatalf("%s: MarshaledKeywords = %v (ok=%v), marshaling = %v (ok=%v)",
						path, keywordNames(got), gotOK, keywordNames(want), wantOK)
				}
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
		// The corpus is what makes this check about real documents rather than
		// synthesized ones, so an empty walk must fail rather than pass silently.
		if files == 0 || nodes < files {
			t.Fatalf("walked %s and found %d documents and %d schema nodes; the comparison checked nothing",
				root, files, nodes)
		}
		t.Logf("compared %d schema nodes across %d corpus documents", nodes, files)
	})
}

func keywordNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// schemaBearingFields are the indices of the Schema fields whose type can hold
// another Schema. Computed by reflection, so a subschema keyword added later is
// included without anyone remembering to.
var schemaBearingFields = func() []int {
	var out []int
	tp := reflect.TypeOf(Schema{})
	for i := range tp.NumField() {
		f := tp.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" || !f.IsExported() {
			continue
		}
		if typeReaches(f.Type, reflect.TypeOf(Schema{}), map[reflect.Type]bool{}) {
			out = append(out, i)
		}
	}
	return out
}()

func typeReaches(t, target reflect.Type, seen map[reflect.Type]bool) bool {
	if t == target {
		return true
	}
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return typeReaches(t.Elem(), target, seen)
	case reflect.Map:
		return typeReaches(t.Key(), target, seen) || typeReaches(t.Elem(), target, seen)
	case reflect.Struct:
		for i := range t.NumField() {
			if typeReaches(t.Field(i).Type, target, seen) {
				return true
			}
		}
	}
	return false
}

// pruneSubschemas returns a copy of s in which every keyword whose value can
// hold a subschema is replaced by the smallest value of its type that
// encoding/json still writes. Which keywords the copy states is unchanged; what
// they say is thrown away.
func pruneSubschemas(s *Schema) *Schema {
	pruned := *s
	v := reflect.ValueOf(&pruned).Elem()
	for _, i := range schemaBearingFields {
		field := v.Field(i)
		if isEmptyForJSON(field) {
			continue
		}
		stand, ok := nonZeroFieldValue(field.Type())
		if !ok {
			continue // left whole; the comparison is still correct, only slower
		}
		field.Set(stand)
	}
	return &pruned
}

// eachSchemaNode calls visit for every *Schema reachable from v, once each.
//
// It navigates by reflection rather than by a list of the keywords that hold
// subschemas, so a keyword added later is walked without anyone remembering to
// add it here. Unexported fields are skipped: the only one that reaches a Schema
// is extensionSchemas, a parse cache whose entries are reachable through
// Extensions anyway.
func eachSchemaNode(v reflect.Value, seen map[*Schema]bool, visit func(*Schema)) {
	if !v.IsValid() || !v.CanInterface() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		if node, ok := v.Interface().(*Schema); ok {
			if seen[node] {
				return
			}
			seen[node] = true
			visit(node)
		}
		eachSchemaNode(v.Elem(), seen, visit)
	case reflect.Interface:
		if !v.IsNil() {
			eachSchemaNode(v.Elem(), seen, visit)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if !v.Type().Field(i).IsExported() {
				continue
			}
			eachSchemaNode(v.Field(i), seen, visit)
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			eachSchemaNode(v.Index(i), seen, visit)
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			eachSchemaNode(v.MapIndex(key), seen, visit)
		}
	}
}
