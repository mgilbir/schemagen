package generator

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/schema"
)

func TestValidationCapabilityDetectsRuntimeFeatures(t *testing.T) {
	input := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "array",
		"prefixItems": [{"type":"string"}],
		"unevaluatedItems": false,
		"$defs": {
			"node": {"$dynamicAnchor":"node", "type":"object"}
		},
		"$dynamicRef": "#node"
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", Validation: ValidationModeHybrid})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	capability := ir.ValidationCapability
	if capability.Mode != ValidationModeHybrid {
		t.Fatalf("mode = %q, want %q", capability.Mode, ValidationModeHybrid)
	}
	if !capability.RequiresRuntime {
		t.Fatalf("expected runtime requirement")
	}
	if !hasValidationFeature(capability.RuntimeFeatures, ValidationFeatureDynamicRef) {
		t.Fatalf("missing dynamicRef feature: %v", capability.RuntimeFeatures)
	}
	if !hasValidationFeature(capability.RuntimeFeatures, ValidationFeatureUnevaluatedItems) {
		t.Fatalf("missing unevaluatedItems feature: %v", capability.RuntimeFeatures)
	}
}

func hasValidationFeature(features []ValidationFeature, want ValidationFeature) bool {
	for _, got := range features {
		if got == want {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, got := range values {
		if got == want {
			return true
		}
	}
	return false
}

func TestOptionalStringWithOmitEmptyUsesPointer(t *testing.T) {
	input := `{
		"title": "Profile",
		"type": "object",
		"properties": {
			"name": {"type":"string"},
			"description": {"type":"string"}
		},
		"required": ["name"]
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", OmitEmpty: true})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var profile *StructDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*StructDef); ok && d.Name == "Profile" {
			profile = d
			break
		}
	}
	if profile == nil {
		t.Fatalf("expected Profile struct")
	}

	fields := make(map[string]FieldDef)
	for _, f := range profile.Fields {
		fields[f.JSONName] = f
	}
	if got := fields["description"].Type.GoTypeName(); got != "*string" {
		t.Fatalf("optional description type = %q, want *string", got)
	}
	if got := fields["name"].Type.GoTypeName(); got != "string" {
		t.Fatalf("required name type = %q, want string", got)
	}
}

func TestNullableArrayPropertyPreservesItemType(t *testing.T) {
	// Regression: a nullable array node (["array","null"]) must still recurse
	// into items and generate a named element struct, rather than collapsing to
	// *[]any (which happened because PrimitiveTypeFromSchema("array") == []any).
	input := `{
		"title": "Export",
		"type": "object",
		"properties": {
			"rows": {
				"type": ["array", "null"],
				"items": {
					"type": ["object", "null"],
					"properties": {"id": {"type":"string"}}
				}
			},
			"tags": {
				"type": ["array", "null"],
				"items": {"type": ["string", "null"]}
			}
		}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", OmitEmpty: true})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var export *StructDef
	var hasItemStruct bool
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*StructDef); ok {
			if d.Name == "Export" {
				export = d
			}
			if d.Name == "ExportRowsItem" {
				hasItemStruct = true
			}
		}
	}
	if export == nil {
		t.Fatalf("expected Export struct")
	}
	if !hasItemStruct {
		t.Fatalf("expected a named ExportRowsItem struct for the nullable array items")
	}

	fields := make(map[string]FieldDef)
	for _, f := range export.Fields {
		fields[f.JSONName] = f
	}
	// A slice is already nilable, so a nullable array needs no outer pointer:
	// []*T, not *[]*T. The inner *T stays because the items are ["object","null"].
	if got := fields["rows"].Type.GoTypeName(); got != "[]*ExportRowsItem" {
		t.Fatalf("rows type = %q, want []*ExportRowsItem (not *[]any or *[]*…)", got)
	}
	if got := fields["tags"].Type.GoTypeName(); got != "[]*string" {
		t.Fatalf("tags type = %q, want []*string (not *[]any or *[]*…)", got)
	}
}

func TestOptionalNamedSliceFieldPresenceGuardUsesNil(t *testing.T) {
	// Regression: an optional property whose type is a named slice with its own
	// Validate() method (here `tracks` -> `type TrackList []TrackListItem`) must
	// get a nil-based presence guard. zeroLiteralForType previously fell back to
	// `""` for a slice-backed alias, so the emitted guard was `field != ""`,
	// which does not compile for a slice type.
	input := `{
		"title": "Playlist",
		"type": "object",
		"definitions": {
			"trackList": {
				"type": "array",
				"minItems": 1,
				"items": {
					"type": "object",
					"properties": {"title": {"type":"string","minLength":1}},
					"required": ["title"]
				}
			}
		},
		"properties": {
			"name": {"type":"string"},
			"tracks": {"$ref": "#/definitions/trackList"}
		},
		"required": ["name"]
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", OmitEmpty: true})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var playlist *StructDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*StructDef); ok && d.Name == "Playlist" {
			playlist = d
			break
		}
	}
	if playlist == nil {
		t.Fatalf("expected Playlist struct")
	}

	var tracks *ValidatableFieldDef
	for i := range playlist.ValidatableFields {
		if playlist.ValidatableFields[i].JSONName == "tracks" {
			tracks = &playlist.ValidatableFields[i]
			break
		}
	}
	if tracks == nil {
		t.Fatalf("expected tracks to be a validatable field; got %+v", playlist.ValidatableFields)
	}
	if tracks.OmitEmpty && tracks.ZeroLiteral != "nil" {
		t.Fatalf("tracks presence guard zero literal = %q, want \"nil\" (slice-backed named type)", tracks.ZeroLiteral)
	}
}

func TestOptionalNullableEnumFieldPresenceGuardUsesNil(t *testing.T) {
	// Regression: an optional property whose enum contains null becomes a
	// raw enum backed by json.RawMessage (a byte slice). zeroForPrimitive
	// previously fell back to `""` for json.RawMessage, so the emitted
	// presence guard was `field != ""`, which does not compile.
	input := `{
		"title": "Banner",
		"type": "object",
		"properties": {
			"message": {"type":"string"},
			"tone": {"enum": ["info", "warning", null]}
		},
		"required": ["message"]
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", OmitEmpty: true})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var banner *StructDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*StructDef); ok && d.Name == "Banner" {
			banner = d
			break
		}
	}
	if banner == nil {
		t.Fatalf("expected Banner struct")
	}

	var tone *ValidatableFieldDef
	for i := range banner.ValidatableFields {
		if banner.ValidatableFields[i].JSONName == "tone" {
			tone = &banner.ValidatableFields[i]
			break
		}
	}
	if tone == nil {
		t.Fatalf("expected tone to be a validatable field; got %+v", banner.ValidatableFields)
	}
	if tone.OmitEmpty && tone.ZeroLiteral != "nil" {
		t.Fatalf("tone presence guard zero literal = %q, want \"nil\" (json.RawMessage-backed raw enum)", tone.ZeroLiteral)
	}
}

func TestAllOfMergesOneOfVariantProperties(t *testing.T) {
	input := `{
		"title": "Field",
		"type": "object",
		"allOf": [
			{"$ref": "#/$defs/field_base"},
			{
				"oneOf": [
					{
						"properties": {
							"type": {"const":"select"},
							"choices": {"type":"array", "items":{"type":"string"}},
							"default": {"type":"string"},
							"widget": {"enum":["slider"]}
						},
						"required": ["choices"]
					},
					{
						"properties": {
							"type": {"const":"number"},
							"min": {"type":"number"},
							"max": {"type":"number"},
							"default": {"type":"number"},
							"widget": {"enum":["slider", "hours"]}
						}
					}
				]
			}
		],
		"$defs": {
			"field_base": {
				"type": "object",
				"properties": {
					"name": {"type":"string"},
					"type": {"type":"string"},
					"label": {"type":"string"}
				},
				"required": ["name", "type"]
			}
		}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", OmitEmpty: true})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var field *StructDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*StructDef); ok && d.Name == "Field" {
			field = d
			break
		}
	}
	if field == nil {
		t.Fatalf("expected Field struct")
	}

	fields := make(map[string]FieldDef)
	for _, f := range field.Fields {
		fields[f.JSONName] = f
	}
	for _, name := range []string{"name", "type", "label", "choices", "min", "max", "default", "widget"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("missing merged field %q; fields = %#v", name, fields)
		}
	}
	if !fields["name"].Required || !fields["type"].Required {
		t.Fatalf("base required fields not preserved: name=%v type=%v", fields["name"].Required, fields["type"].Required)
	}
	if fields["choices"].Required || fields["min"].Required || fields["max"].Required {
		t.Fatalf("variant-specific fields must not become globally required")
	}
	if got := fields["choices"].Type.GoTypeName(); got != "[]string" {
		t.Fatalf("choices type = %q, want []string", got)
	}
	if got := fields["min"].Type.GoTypeName(); got != "*float64" {
		t.Fatalf("min type = %q, want *float64", got)
	}
	if got := fields["default"].Type.GoTypeName(); got != "any" {
		t.Fatalf("default type = %q, want any", got)
	}
	if len(fields["widget"].Type.GoTypeName()) == 0 {
		t.Fatalf("widget type is empty")
	}
	var widgetEnum *EnumDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*EnumDef); ok && d.Name == "FieldWidget" {
			widgetEnum = d
			break
		}
	}
	if widgetEnum == nil {
		t.Fatalf("expected FieldWidget enum")
	}
	gotValues := make(map[any]bool)
	for _, v := range widgetEnum.Values {
		gotValues[v.Value] = true
	}
	for _, want := range []string{"slider", "hours"} {
		if !gotValues[want] {
			t.Fatalf("widget enum missing %q: %#v", want, widgetEnum.Values)
		}
	}
}

func TestAllOfMergesIfThenBranchProperties(t *testing.T) {
	input := `{
		"title": "Trigger",
		"type": "object",
		"allOf": [
			{"$ref": "#/$defs/base"},
			{
				"if": {"properties": {"type": {"const":"tool"}}, "required": ["type"]},
				"then": {
					"properties": {
						"type": {"enum":["tool"]},
						"tool": {"type":"array", "items":{"type":"object", "properties":{"id":{"type":"string"}}, "required":["id"]}},
						"default": {"type":"string"}
					},
					"required": ["tool"]
				}
			},
			{
				"if": {"properties": {"type": {"const":"notify"}}, "required": ["type"]},
				"then": {
					"properties": {
						"type": {"enum":["notify"]},
						"title": {"type":"string"},
						"message": {"type":"string"},
						"notify": {"type":"array", "items":{"type":"string"}},
						"default": {"type":"boolean"}
					},
					"required": ["title", "message", "notify"]
				}
			}
		],
		"$defs": {
			"base": {
				"type": "object",
				"properties": {
					"delay": {"type":"string"},
					"condition": {"type":"string"}
				}
			}
		}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", OmitEmpty: true})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var trigger *StructDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*StructDef); ok && d.Name == "Trigger" {
			trigger = d
			break
		}
	}
	if trigger == nil {
		t.Fatalf("expected Trigger struct")
	}
	fields := make(map[string]FieldDef)
	for _, f := range trigger.Fields {
		fields[f.JSONName] = f
	}
	for _, name := range []string{"delay", "condition", "type", "tool", "title", "message", "notify", "default"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("missing merged field %q; fields = %#v", name, fields)
		}
	}
	if fields["tool"].Required || fields["title"].Required || fields["message"].Required || fields["notify"].Required {
		t.Fatalf("conditional fields must not become globally required")
	}
	if got := fields["default"].Type.GoTypeName(); got != "any" {
		t.Fatalf("default type = %q, want any", got)
	}
	var typeEnum *EnumDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*EnumDef); ok && enumHasValues(d, "tool", "notify") {
			typeEnum = d
			break
		}
	}
	if typeEnum == nil {
		t.Fatalf("expected TriggerType enum")
	}
}

func enumHasValues(enum *EnumDef, wants ...string) bool {
	if enum == nil {
		return false
	}
	got := make(map[string]bool)
	for _, v := range enum.Values {
		if s, ok := v.Value.(string); ok {
			got[s] = true
		}
	}
	for _, want := range wants {
		if !got[want] {
			return false
		}
	}
	return true
}

func TestDraft3DisallowInlineSchemaGeneratesNotBranches(t *testing.T) {
	input := `{
		"disallow": [
			"string",
			{"type":"object", "properties":{"foo":{"type":"string"}}}
		]
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", Draft: schema.Draft03})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var notDef *NotSchemaDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*NotSchemaDef); ok {
			notDef = d
			break
		}
	}
	if notDef == nil {
		t.Fatalf("expected NotSchemaDef")
	}
	if len(notDef.NotBranches) != 2 {
		t.Fatalf("expected 2 not branches, got %d", len(notDef.NotBranches))
	}
	if len(notDef.NotBranches[0].Types) != 1 || notDef.NotBranches[0].Types[0] != "string" {
		t.Fatalf("first branch = %#v, want string type branch", notDef.NotBranches[0])
	}
	if len(notDef.NotBranches[1].Properties) != 1 || notDef.NotBranches[1].Properties[0].Name != "foo" || notDef.NotBranches[1].Properties[0].JSONType != "string" {
		t.Fatalf("second branch = %#v, want foo:string property branch", notDef.NotBranches[1])
	}
}

func TestDraft3DisallowInlineSchemaGeneratesSimpleValidationBranches(t *testing.T) {
	input := `{
		"disallow": [
			{"type":"integer", "minimum":10},
			{"type":"string", "minLength":3},
			{"type":"array", "maxItems":1}
		]
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", Draft: schema.Draft03})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var notDef *NotSchemaDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*NotSchemaDef); ok {
			notDef = d
			break
		}
	}
	if notDef == nil {
		t.Fatalf("expected NotSchemaDef")
	}
	if len(notDef.NotBranches) != 3 {
		t.Fatalf("expected 3 not branches, got %d", len(notDef.NotBranches))
	}
	wants := []struct {
		jsonType string
		ruleType string
	}{
		{"integer", "minimum"},
		{"string", "minLength"},
		{"array", "maxItems"},
	}
	for i, want := range wants {
		branch := notDef.NotBranches[i]
		if len(branch.Types) != 1 || branch.Types[0] != want.jsonType {
			t.Fatalf("branch %d types = %#v, want %q", i, branch.Types, want.jsonType)
		}
		if len(branch.Validations) != 1 || branch.Validations[0].RuleType != want.ruleType {
			t.Fatalf("branch %d validations = %#v, want rule %q", i, branch.Validations, want.ruleType)
		}
	}
}

func TestUnevaluatedItemsIgnoresAdditionalItemsWithoutTupleItems(t *testing.T) {
	input := `{
		"$schema": "https://json-schema.org/draft/2019-09/schema",
		"additionalItems": {"type":"number"},
		"unevaluatedItems": {"type":"string"}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", Draft: schema.Draft201909})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var alias *InferredAliasDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*InferredAliasDef); ok {
			alias = d
			break
		}
	}
	if alias == nil {
		t.Fatalf("expected InferredAliasDef")
	}
	if alias.UnevaluatedItems == nil {
		t.Fatalf("expected unevaluatedItems validation")
	}
	if alias.UnevaluatedItems.AllEvaluated {
		t.Fatalf("additionalItems without tuple items must not mark all items evaluated")
	}
	if alias.UnevaluatedItems.ValueType != "string" {
		t.Fatalf("unevaluatedItems value type = %q, want string", alias.UnevaluatedItems.ValueType)
	}
}

func TestArrayAliasUnevaluatedItemsCollectsDynamicRefEvaluatedCount(t *testing.T) {
	input := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://example.com/derived",
		"$ref": "./baseSchema",
		"$defs": {
			"derived": {
				"$dynamicAnchor": "addons",
				"prefixItems": [true, {"type":"string"}]
			},
			"baseSchema": {
				"$id": "./baseSchema",
				"unevaluatedItems": false,
				"type": "array",
				"prefixItems": [{"type":"string"}],
				"$dynamicRef": "#addons",
				"$defs": {
					"defaultAddons": {"$dynamicAnchor": "addons"}
				}
			}
		}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", Draft: schema.Draft202012})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var base *AliasDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*AliasDef); ok && d.Name == "BaseSchema" {
			base = d
			break
		}
	}
	if base == nil {
		t.Fatalf("expected BaseSchema AliasDef")
	}
	if base.UnevaluatedItems == nil || !base.UnevaluatedItems.IsForbidden {
		t.Fatalf("expected forbidden unevaluatedItems on BaseSchema, got %#v", base.UnevaluatedItems)
	}
	if base.UnevaluatedItems.EvaluatedCount != 2 {
		t.Fatalf("evaluated count = %d, want 2", base.UnevaluatedItems.EvaluatedCount)
	}
	// The evaluated count above stays right even when the schema is *also*
	// given a length bound, so asserting it alone let a real over-enforcement
	// through: unevaluatedItems:false beside a one-entry prefixItems reads as a
	// fixed tuple, and folding that into maxItems:1 rejects ["foo","bar"] --
	// valid, because the $dynamicRef contributes the second position. What the
	// dynamic reference resolves to is not knowable here, so no bound may be
	// derived from the tuple length at all.
	for _, v := range base.Validations {
		if v.RuleType == "maxItems" || v.RuleType == "minItems" {
			t.Fatalf("BaseSchema got a %s=%v rule; a $dynamicRef can add evaluated positions, so the prefixItems length is not a bound", v.RuleType, v.Value)
		}
	}
}

func TestArrayAliasUnevaluatedItemsCollectsRecursiveRefEvaluatedCount(t *testing.T) {
	input := `{
		"$schema": "https://json-schema.org/draft/2019-09/schema",
		"$id": "https://example.com/extended-tree",
		"$recursiveAnchor": true,
		"$ref": "./tree",
		"items": [true, true, {"type":"string"}],
		"$defs": {
			"tree": {
				"$id": "./tree",
				"$recursiveAnchor": true,
				"type": "array",
				"items": [
					{"type":"number"},
					{"unevaluatedItems": false, "$recursiveRef": "#"}
				]
			}
		}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", Draft: schema.Draft201909})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var item *InferredAliasDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*InferredAliasDef); ok && d.Name == "TreeItem1" {
			item = d
			break
		}
	}
	if item == nil {
		t.Fatalf("expected TreeItem1 InferredAliasDef")
	}
	if item.UnevaluatedItems == nil || !item.UnevaluatedItems.IsForbidden {
		t.Fatalf("expected forbidden unevaluatedItems on TreeItem1, got %#v", item.UnevaluatedItems)
	}
	if item.UnevaluatedItems.EvaluatedCount != 3 {
		t.Fatalf("evaluated count = %d, want 3", item.UnevaluatedItems.EvaluatedCount)
	}
}

func TestUnevaluatedPropertiesCollectsDynamicRefEvaluatedNames(t *testing.T) {
	input := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://example.com/derived",
		"$ref": "./baseSchema",
		"$defs": {
			"derived": {
				"$dynamicAnchor": "addons",
				"properties": {"bar": {"type":"string"}}
			},
			"baseSchema": {
				"$id": "./baseSchema",
				"unevaluatedProperties": false,
				"properties": {"foo": {"type":"string"}},
				"$dynamicRef": "#addons",
				"$defs": {
					"defaultAddons": {"$dynamicAnchor": "addons"}
				}
			}
		}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", Draft: schema.Draft202012})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var base *StructDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*StructDef); ok && d.Name == "BaseSchema" {
			base = d
			break
		}
	}
	if base == nil {
		t.Fatalf("expected BaseSchema StructDef")
	}
	if base.UnevaluatedProperties == nil {
		t.Fatalf("expected unevaluatedProperties definition")
	}
	if !containsString(base.UnevaluatedProperties.EvaluatedNames, "foo") || !containsString(base.UnevaluatedProperties.EvaluatedNames, "bar") {
		t.Fatalf("evaluated names = %#v, want foo and bar", base.UnevaluatedProperties.EvaluatedNames)
	}
}

func TestPropertyRecursiveRefWithUnevaluatedPropertiesGeneratesWrapper(t *testing.T) {
	input := `{
		"$schema": "https://json-schema.org/draft/2019-09/schema",
		"$id": "https://example.com/extended-tree",
		"$recursiveAnchor": true,
		"$ref": "./tree",
		"properties": {"name": {"type":"string"}},
		"$defs": {
			"tree": {
				"$id": "./tree",
				"$recursiveAnchor": true,
				"type": "object",
				"properties": {
					"node": true,
					"branches": {
						"unevaluatedProperties": false,
						"$recursiveRef": "#"
					}
				},
				"required": ["node"]
			}
		}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", Draft: schema.Draft201909})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var wrapper *StructDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*StructDef); ok && d.Name == "TreeBranches" {
			wrapper = d
			break
		}
	}
	if wrapper == nil {
		t.Fatalf("expected TreeBranches StructDef")
	}
	if wrapper.UnevaluatedProperties == nil || !wrapper.UnevaluatedProperties.IsForbidden {
		t.Fatalf("expected forbidden unevaluatedProperties on wrapper, got %#v", wrapper.UnevaluatedProperties)
	}
	if !containsString(wrapper.UnevaluatedProperties.EvaluatedNames, "node") || !containsString(wrapper.UnevaluatedProperties.EvaluatedNames, "name") {
		t.Fatalf("evaluated names = %#v, want node and name", wrapper.UnevaluatedProperties.EvaluatedNames)
	}
}

func TestInferredArrayExtractsNestedRemoteItemType(t *testing.T) {
	input := `{
		"id": "http://localhost:1234/",
		"items": {
			"id": "baseUriChange/",
			"items": {"$ref": "folderInteger.json"}
		}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	remote := &schema.Schema{Type: schema.TypeList{"integer"}}
	resolver := schema.NewMappingResolver(map[string]*schema.Schema{
		"http://localhost:1234/baseUriChange/folderInteger.json": remote,
	})
	gen := New(Config{PackageName: "testpkg", Draft: schema.Draft03, Resolver: resolver})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var alias *InferredAliasDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*InferredAliasDef); ok && d.Name == "Root" {
			alias = d
			break
		}
	}
	if alias == nil {
		t.Fatalf("expected root InferredAliasDef")
	}
	if alias.ItemsNested == nil || alias.ItemsNested.ItemsType != "integer" {
		t.Fatalf("nested items = %#v, want integer", alias.ItemsNested)
	}
	if alias.InferredGoType.GoTypeName() != "[]any" {
		t.Fatalf("inferred Go type = %q, want []any", alias.InferredGoType.GoTypeName())
	}
}

func TestMetaschemaWithoutValidationVocabularyKeepsApplicators(t *testing.T) {
	input := `{
		"$schema": "http://example.test/meta-no-validation",
		"properties": {
			"badProperty": false,
			"numberProperty": {"minimum": 10}
		}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	meta := &schema.Schema{
		ID: "http://example.test/meta-no-validation",
		Vocabulary: map[string]bool{
			"https://json-schema.org/draft/2020-12/vocab/applicator": true,
			"https://json-schema.org/draft/2020-12/vocab/core":       true,
		},
	}
	resolver := schema.NewMappingResolver(map[string]*schema.Schema{
		"http://example.test/meta-no-validation": meta,
	})
	gen := New(Config{PackageName: "testpkg", Draft: schema.Draft202012, Resolver: resolver})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var root *StructDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*StructDef); ok && d.Name == "Root" {
			root = d
			break
		}
	}
	if root == nil {
		t.Fatalf("expected root StructDef")
	}
	if len(root.Validations) != 1 || root.Validations[0].RuleType != "forbidden" || root.Validations[0].JSONName != "badProperty" {
		t.Fatalf("validations = %#v, want only badProperty forbidden", root.Validations)
	}
}

func TestDraft3SchemaValuedTypeGeneratesTypeBranch(t *testing.T) {
	input := `{
		"type": [
			"integer",
			{"properties": {"foo": {"type": "null"}}}
		]
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", Draft: schema.Draft03})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var typeDef *TypeOnlySchemaDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*TypeOnlySchemaDef); ok && d.Name == "Root" {
			typeDef = d
			break
		}
	}
	if typeDef == nil {
		t.Fatalf("expected root TypeOnlySchemaDef")
	}
	if len(typeDef.AllowedTypes) != 1 || typeDef.AllowedTypes[0] != "integer" {
		t.Fatalf("allowed types = %#v, want integer", typeDef.AllowedTypes)
	}
	if len(typeDef.TypeBranches) != 1 || len(typeDef.TypeBranches[0].Properties) != 1 {
		t.Fatalf("type branches = %#v, want one property branch", typeDef.TypeBranches)
	}
	prop := typeDef.TypeBranches[0].Properties[0]
	if prop.Name != "foo" || prop.JSONType != "null" {
		t.Fatalf("branch property = %#v, want foo:null", prop)
	}
}

func TestAliasDelegatesValidationToNamedUnderlyingType(t *testing.T) {
	input := `{
		"$defs": {
			"target": {
				"type": "object",
				"properties": {"elements": {"type": "array"}},
				"required": ["elements"],
				"additionalProperties": false
			}
		},
		"$ref": "#/$defs/target"
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", Draft: schema.Draft202012})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var root *AliasDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*AliasDef); ok && d.Name == "Root" {
			root = d
			break
		}
	}
	if root == nil {
		t.Fatalf("expected root AliasDef")
	}
	if root.ValidateAs != "Target" {
		t.Fatalf("ValidateAs = %q, want Target", root.ValidateAs)
	}
	if root.UnmarshalAs != "Target" {
		t.Fatalf("UnmarshalAs = %q, want Target", root.UnmarshalAs)
	}
	if root.MarshalAs != "Target" {
		t.Fatalf("MarshalAs = %q, want Target", root.MarshalAs)
	}
}

func TestOptionalRefToPrimitiveAliasUsesPointer(t *testing.T) {
	input := `{
		"type": "object",
		"properties": {
			"nickname": {"$ref": "#/$defs/name"}
		},
		"$defs": {
			"name": {"type": "string"}
		}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", Draft: schema.Draft202012, OmitEmpty: true})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var root *StructDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*StructDef); ok && d.Name == "Root" {
			root = d
			break
		}
	}
	if root == nil {
		t.Fatalf("expected Root StructDef")
	}
	for _, field := range root.Fields {
		if field.JSONName == "nickname" {
			// Name is `type Name string`, so an empty nickname is the Go zero
			// and omitempty would drop it. The name over the primitive changes
			// nothing about that — see TestOptionalNamedPrimitiveKeepsZeroValue.
			if field.Type.GoTypeName() != "*Name" {
				t.Fatalf("nickname type = %q, want *Name", field.Type.GoTypeName())
			}
			return
		}
	}
	t.Fatalf("expected nickname field")
}

// TestOptionalNamedPrimitiveKeepsZeroValue covers the three ways a property
// ends up typed as a *named* primitive — a $ref to a primitive definition, an
// inline enum, and a const promoted to a single-value enum. Each was emitted as
// a value with omitempty, so a legitimate 0, "" or false both disappeared from
// the marshalled output and skipped the named type's own Validate(), which the
// owner guarded with a `!= <zero>` presence test.
func TestOptionalNamedPrimitiveKeepsZeroValue(t *testing.T) {
	input := `{
		"type": "object",
		"properties": {
			"count":  {"$ref": "#/$defs/counter"},
			"label":  {"$ref": "#/$defs/tag"},
			"level":  {"enum": ["", "high"]},
			"marker": {"const": ""},
			"note":   {"type": "string"}
		},
		"$defs": {
			"counter": {"type": "integer", "minimum": 0},
			"tag": {"type": "string", "minLength": 0}
		}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", Draft: schema.Draft202012, OmitEmpty: true})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var root *StructDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*StructDef); ok && d.Name == "Root" {
			root = d
			break
		}
	}
	if root == nil {
		t.Fatalf("expected Root StructDef")
	}

	fields := make(map[string]FieldDef, len(root.Fields))
	for _, f := range root.Fields {
		fields[f.JSONName] = f
	}
	want := map[string]string{
		"count":  "*Counter",
		"label":  "*Tag",
		"level":  "*RootLevel",
		"marker": "*RootMarker",
		"note":   "*string", // the bare-primitive case, already correct
	}
	for jsonName, wantType := range want {
		f, ok := fields[jsonName]
		if !ok {
			t.Fatalf("expected field %q", jsonName)
		}
		if got := f.Type.GoTypeName(); got != wantType {
			t.Errorf("%q type = %q, want %q", jsonName, got, wantType)
		}
		if !f.OmitEmpty {
			t.Errorf("%q: OmitEmpty = false, want true", jsonName)
		}
	}

	// The presence guard the owner emits around the named type's Validate()
	// must be a nil check, not a comparison against the zero value: the whole
	// point of the pointer is that the zero value is a present value.
	guards := make(map[string]ValidatableFieldDef, len(root.ValidatableFields))
	for _, vf := range root.ValidatableFields {
		guards[vf.JSONName] = vf
	}
	for _, jsonName := range []string{"count", "label", "level", "marker"} {
		vf, ok := guards[jsonName]
		if !ok {
			t.Fatalf("expected %q among ValidatableFields", jsonName)
		}
		if !vf.IsPointer {
			t.Errorf("%q: IsPointer = false, want true (guard would test the zero value)", jsonName)
		}
		if vf.ZeroLiteral != "nil" {
			t.Errorf("%q: ZeroLiteral = %q, want %q", jsonName, vf.ZeroLiteral, "nil")
		}
	}
}

func TestDraft3IntegerAliasRequiresStrictIntegerToken(t *testing.T) {
	input := `{"type":"integer"}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg", Draft: schema.Draft03})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	root, ok := ir.TypeDefs[0].(*AliasDef)
	if !ok {
		t.Fatalf("root type = %T, want AliasDef", ir.TypeDefs[0])
	}
	if !root.StrictInteger {
		t.Fatalf("StrictInteger = false, want true")
	}
}

// An explicit Config.Draft is the caller's statement about the document, so it
// must win over the document's own $schema in per-node draft decisions — not
// just in the paths that read g.draft directly.
func TestExplicitDraftOverridesDocumentSchemaKeyword(t *testing.T) {
	input := `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "array",
		"prefixItems": [{"type":"string"}, {"type":"string"}]
	}`

	tupleLen := func(t *testing.T, cfg Config) int {
		t.Helper()
		var s schema.Schema
		if err := json.Unmarshal([]byte(input), &s); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		s.Normalize()

		ir, err := New(cfg).Generate(&s)
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		for _, td := range ir.TypeDefs {
			if d, ok := td.(*AliasDef); ok {
				return len(d.TupleItems)
			}
		}
		t.Fatalf("expected AliasDef in %d type defs", len(ir.TypeDefs))
		return 0
	}

	if got := tupleLen(t, Config{PackageName: "testpkg"}); got != 0 {
		t.Fatalf("without override: TupleItems = %d, want 0 (prefixItems is not a draft-07 keyword)", got)
	}
	if got := tupleLen(t, Config{PackageName: "testpkg", Draft: schema.Draft202012}); got != 2 {
		t.Fatalf("with --draft 2020-12: TupleItems = %d, want 2", got)
	}
}

// The one exception to Config.Draft precedence: an embedded resource that
// establishes its own $id-scoped document root with an explicit $schema keeps
// its own dialect, so cross-draft $ref semantics survive the override.
func TestExplicitDraftDoesNotOverrideEmbeddedResourceDialect(t *testing.T) {
	input := `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://example.com/root",
		"type": "object",
		"properties": {
			"legacy": {"$ref": "#/$defs/legacy"}
		},
		"$defs": {
			"legacy": {
				"$id": "https://example.com/legacy",
				"$schema": "http://json-schema.org/draft-07/schema#",
				"type": "array",
				"prefixItems": [{"type":"string"}, {"type":"string"}]
			},
			"modern": {
				"type": "array",
				"prefixItems": [{"type":"string"}, {"type":"string"}]
			}
		}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg", Draft: schema.Draft202012}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	tuples := map[string]int{}
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*AliasDef); ok {
			tuples[d.Name] = len(d.TupleItems)
		}
	}

	// The embedded resource declares its own dialect, so the override does not reach it.
	if got, ok := tuples["Legacy"]; !ok {
		t.Fatalf("expected AliasDef named Legacy, got %v", tuples)
	} else if got != 0 {
		t.Fatalf("embedded draft-07 resource: TupleItems = %d, want 0 (its own $schema wins)", got)
	}
	// A node inside the root document has no dialect of its own, so the override applies.
	if got, ok := tuples["Modern"]; !ok {
		t.Fatalf("expected AliasDef named Modern, got %v", tuples)
	} else if got != 2 {
		t.Fatalf("root-document node: TupleItems = %d, want 2 (override applies)", got)
	}
}

// A default that violates its own declared type must be reported, not silently
// truncated into a different value.
func TestFractionalDefaultOnIntegerPropertyIsRejected(t *testing.T) {
	input := `{
		"type": "object",
		"properties": {
			"retries": {"type": "integer", "default": 4.5}
		}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	_, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err == nil {
		t.Fatalf("expected error for fractional default on an integer property")
	}
	if !strings.Contains(err.Error(), "retries") {
		t.Fatalf("error %q does not name the offending property", err)
	}
}

// ---------- Naming tests ----------

func TestJSONPropertyToGoName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"first_name", "FirstName"},
		{"firstName", "FirstName"},
		{"id", "ID"},
		{"api_url", "APIURL"},
		{"user_id", "UserID"},
		{"html_content", "HTMLContent"},
		{"myJSON", "MyJSON"},
		{"simple", "Simple"},
		{"already_PascalCase", "AlreadyPascalCase"},
		{"ip_address", "IPAddress"},
		{"css_class", "CSSClass"},
		// Special characters stripped
		{"$ref", "Ref"},
		// Property names that lowercase to a Go keyword must NOT get a trailing
		// underscore once capitalized: exported identifiers can never collide with
		// (all-lowercase) Go keywords (regression for C11).
		{"type", "Type"},
		{"default", "Default"},
		{"range", "Range"},
		{"func", "Func"},
		{"foo\"bar", "FooBar"},
		{"foo\\bar", "FooBar"},
		{"foo\nbar", "FooBar"},
		{"foo\tbar", "FooBar"},
		{"foo\rbar", "FooBar"},
		// Empty input
		{"", "X"},
		// All non-identifier chars
		{"$#%", "X"},
		// Starts with digit after sanitization
		{"123abc", "X123abc"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := JSONPropertyToGoName(tt.input)
			if got != tt.want {
				t.Errorf("JSONPropertyToGoName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSchemaNameToGoName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"my-type", "MyType"},
		{"my_type", "MyType"},
		{"MyType", "MyType"},
		{"some-api-thing", "SomeAPIThing"},
		{"tilde~field", "TildeField"},
		{"slash/field", "SlashField"},
		{"percent%field", "PercentField"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SchemaNameToGoName(tt.input)
			if got != tt.want {
				t.Errorf("SchemaNameToGoName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRefToGoName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Standard JSON Pointer refs
		{"#/$defs/my-type", "MyType"},
		{"#/definitions/Address", "Address"},
		{"#/definitions/is-string", "IsString"},
		// Fragment-only ref
		{"#", "Root"},
		// Escaped JSON Pointer segments
		{"#/definitions/tilde~0field", "TildeField"},
		{"#/definitions/slash~1field", "SlashField"},
		// URL-encoded segments
		{"#/definitions/foo%22bar", "FooBar"},
		{"#/definitions/percent%25field", "PercentField"},
		// Empty path segments
		{"#/definitions//definitions/", "Definitions"},
		// URN refs
		{"urn:uuid:deadbeef-1234-ffff-ffff-4321feebdaed", "Deadbeef1234FfffFfff4321feebdaed"},
		// URN with fragment
		{"urn:uuid:deadbeef-1234-ff00-00ff-4321feebdaed#something", "Something"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := refToGoName(tt.input)
			if got != tt.want {
				t.Errorf("refToGoName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeGoIdentifier(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ValidName", "ValidName"},
		{"", "X"},
		{"123", "X123"},
		{"$ref", "ref"},
		{"foo#bar", "foobar"},
		{"break", "break_"},
		{"type", "type_"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeGoIdentifier(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeGoIdentifier(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestToOneOfInterfaceName(t *testing.T) {
	got := ToOneOfInterfaceName("Parent", "Field")
	want := "isParent_Field"
	if got != want {
		t.Errorf("ToOneOfInterfaceName = %q, want %q", got, want)
	}
}

func TestToOneOfWrapperName(t *testing.T) {
	got := ToOneOfWrapperName("Parent", "Variant")
	want := "Parent_Variant"
	if got != want {
		t.Errorf("ToOneOfWrapperName = %q, want %q", got, want)
	}
}

// ---------- Primitive type mapping tests ----------

func TestPrimitiveTypeFromSchema(t *testing.T) {
	tests := []struct {
		input    string
		wantName string
		wantNil  bool
	}{
		{"string", "string", false},
		{"integer", "int64", false},
		{"number", "float64", false},
		{"boolean", "bool", false},
		{"null", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := PrimitiveTypeFromSchema(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Errorf("PrimitiveTypeFromSchema(%q) = %v, want nil", tt.input, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("PrimitiveTypeFromSchema(%q) = nil, want %q", tt.input, tt.wantName)
			}
			if got.GoTypeName() != tt.wantName {
				t.Errorf("PrimitiveTypeFromSchema(%q).GoTypeName() = %q, want %q", tt.input, got.GoTypeName(), tt.wantName)
			}
		})
	}
}

func TestPrimitiveTypeFromSchema_Object(t *testing.T) {
	got := PrimitiveTypeFromSchema("object")
	if got == nil {
		t.Fatal("expected non-nil for object")
	}
	if got.GoTypeName() != "map[string]any" {
		t.Errorf("got %q, want %q", got.GoTypeName(), "map[string]any")
	}
}

func TestPrimitiveTypeFromSchema_Array(t *testing.T) {
	got := PrimitiveTypeFromSchema("array")
	if got == nil {
		t.Fatal("expected non-nil for array")
	}
	if got.GoTypeName() != "[]any" {
		t.Errorf("got %q, want %q", got.GoTypeName(), "[]any")
	}
}

// ---------- GoType tests ----------

func TestGoTypeNames(t *testing.T) {
	tests := []struct {
		name     string
		goType   GoType
		wantName string
		wantPtr  bool
	}{
		{
			"PrimitiveType",
			&PrimitiveType{Name: "string"},
			"string",
			false,
		},
		{
			"NamedType",
			&NamedType{Name: "Person"},
			"Person",
			false,
		},
		{
			"NamedType pointer",
			&NamedType{Name: "Person", Pointer: true},
			"*Person",
			true,
		},
		{
			"ArrayType",
			&ArrayType{ItemType: &PrimitiveType{Name: "string"}},
			"[]string",
			false,
		},
		{
			"MapType",
			&MapType{
				KeyType:   &PrimitiveType{Name: "string"},
				ValueType: &PrimitiveType{Name: "any"},
			},
			"map[string]any",
			false,
		},
		{
			"PointerType",
			&PointerType{Inner: &PrimitiveType{Name: "string"}},
			"*string",
			true,
		},
		{
			"ArrayType of NamedType",
			&ArrayType{ItemType: &NamedType{Name: "Item"}},
			"[]Item",
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.goType.GoTypeName(); got != tt.wantName {
				t.Errorf("GoTypeName() = %q, want %q", got, tt.wantName)
			}
			if got := tt.goType.IsPointer(); got != tt.wantPtr {
				t.Errorf("IsPointer() = %v, want %v", got, tt.wantPtr)
			}
		})
	}
}

// ---------- Generator tests ----------

func TestGenerate_SimpleObject(t *testing.T) {
	s := &schema.Schema{
		Title: "Person",
		Type:  schema.TypeList{"object"},
		Properties: map[string]*schema.Schema{
			"name": {
				Type:        schema.TypeList{"string"},
				Description: "The person's name",
			},
			"age": {
				Type: schema.TypeList{"integer"},
			},
			"email": {
				Type: schema.TypeList{"string"},
			},
		},
		Required: []string{"name", "age"},
	}

	gen := New(DefaultConfig())
	file, err := gen.Generate(s)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if file.PackageName != "generated" {
		t.Errorf("PackageName = %q, want %q", file.PackageName, "generated")
	}

	if len(file.TypeDefs) != 1 {
		t.Fatalf("expected 1 TypeDef, got %d", len(file.TypeDefs))
	}

	sd, ok := file.TypeDefs[0].(*StructDef)
	if !ok {
		t.Fatalf("expected *StructDef, got %T", file.TypeDefs[0])
	}

	if sd.Name != "Person" {
		t.Errorf("StructDef.Name = %q, want %q", sd.Name, "Person")
	}

	if len(sd.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(sd.Fields))
	}

	// Fields should be sorted by JSON name.
	fieldMap := make(map[string]FieldDef)
	for _, f := range sd.Fields {
		fieldMap[f.JSONName] = f
	}

	// Check "name" field
	nameField := fieldMap["name"]
	if nameField.Name != "Name" {
		t.Errorf("name field Go name = %q, want %q", nameField.Name, "Name")
	}
	if nameField.Type.GoTypeName() != "string" {
		t.Errorf("name field type = %q, want %q", nameField.Type.GoTypeName(), "string")
	}
	if !nameField.Required {
		t.Error("name field should be required")
	}
	if nameField.OmitEmpty {
		t.Error("name field should not have omitempty (it's required)")
	}

	// Check "age" field
	ageField := fieldMap["age"]
	if ageField.Name != "Age" {
		t.Errorf("age field Go name = %q, want %q", ageField.Name, "Age")
	}
	if ageField.Type.GoTypeName() != "int64" {
		t.Errorf("age field type = %q, want %q", ageField.Type.GoTypeName(), "int64")
	}

	// Check "email" field (optional)
	emailField := fieldMap["email"]
	if !emailField.OmitEmpty {
		t.Error("email field should have omitempty (it's optional)")
	}
}

func TestGenerate_RefResolution(t *testing.T) {
	s := &schema.Schema{
		Title: "Order",
		Type:  schema.TypeList{"object"},
		Properties: map[string]*schema.Schema{
			"billing_address": {
				Ref: "#/$defs/Address",
			},
			"shipping_address": {
				Ref: "#/$defs/Address",
			},
		},
		Defs: map[string]*schema.Schema{
			"Address": {
				Type: schema.TypeList{"object"},
				Properties: map[string]*schema.Schema{
					"street": {Type: schema.TypeList{"string"}},
					"city":   {Type: schema.TypeList{"string"}},
				},
				Required: []string{"street", "city"},
			},
		},
	}

	gen := New(DefaultConfig())
	file, err := gen.Generate(s)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// Should have Address struct + Order struct = 2 type defs.
	if len(file.TypeDefs) != 2 {
		t.Fatalf("expected 2 TypeDefs, got %d", len(file.TypeDefs))
	}

	// Find the Order struct.
	var order *StructDef
	var address *StructDef
	for _, td := range file.TypeDefs {
		if sd, ok := td.(*StructDef); ok {
			switch sd.Name {
			case "Order":
				order = sd
			case "Address":
				address = sd
			}
		}
	}

	if order == nil {
		t.Fatal("Order struct not found")
	}
	if address == nil {
		t.Fatal("Address struct not found")
	}

	// Check that billing_address references Address.
	fieldMap := make(map[string]FieldDef)
	for _, f := range order.Fields {
		fieldMap[f.JSONName] = f
	}

	billingField := fieldMap["billing_address"]
	if billingField.Type.GoTypeName() != "*Address" {
		t.Errorf("billing_address type = %q, want %q", billingField.Type.GoTypeName(), "*Address")
	}

	// Should be a PointerType wrapping a NamedType
	if pt, ok := billingField.Type.(*PointerType); !ok {
		t.Errorf("billing_address type should be *PointerType, got %T", billingField.Type)
	} else if _, ok := pt.Inner.(*NamedType); !ok {
		t.Errorf("billing_address inner type should be *NamedType, got %T", pt.Inner)
	}
}

func TestGenerate_NestedObject(t *testing.T) {
	s := &schema.Schema{
		Title: "Company",
		Type:  schema.TypeList{"object"},
		Properties: map[string]*schema.Schema{
			"name": {
				Type: schema.TypeList{"string"},
			},
			"address": {
				Type: schema.TypeList{"object"},
				Properties: map[string]*schema.Schema{
					"street": {Type: schema.TypeList{"string"}},
					"city":   {Type: schema.TypeList{"string"}},
				},
			},
		},
	}

	gen := New(DefaultConfig())
	file, err := gen.Generate(s)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// Should have Company struct + CompanyAddress struct = 2 type defs.
	if len(file.TypeDefs) != 2 {
		t.Fatalf("expected 2 TypeDefs, got %d", len(file.TypeDefs))
	}

	names := make(map[string]bool)
	for _, td := range file.TypeDefs {
		names[td.TypeName()] = true
	}

	if !names["Company"] {
		t.Error("expected Company type")
	}
	if !names["CompanyAddress"] {
		t.Error("expected CompanyAddress type")
	}

	// Find Company struct and check that address field uses NamedType.
	for _, td := range file.TypeDefs {
		sd, ok := td.(*StructDef)
		if !ok || sd.Name != "Company" {
			continue
		}
		for _, f := range sd.Fields {
			if f.JSONName == "address" {
				if f.Type.GoTypeName() != "*CompanyAddress" {
					t.Errorf("address field type = %q, want %q", f.Type.GoTypeName(), "*CompanyAddress")
				}
			}
		}
	}
}

func TestGenerate_NullableType(t *testing.T) {
	s := &schema.Schema{
		Title: "Record",
		Type:  schema.TypeList{"object"},
		Properties: map[string]*schema.Schema{
			"name": {
				Type: schema.TypeList{"string"},
			},
			"nickname": {
				Type: schema.TypeList{"string", "null"},
			},
			"score": {
				Type: schema.TypeList{"integer", "null"},
			},
		},
		Required: []string{"name"},
	}

	gen := New(DefaultConfig())
	file, err := gen.Generate(s)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if len(file.TypeDefs) != 1 {
		t.Fatalf("expected 1 TypeDef, got %d", len(file.TypeDefs))
	}

	sd := file.TypeDefs[0].(*StructDef)
	fieldMap := make(map[string]FieldDef)
	for _, f := range sd.Fields {
		fieldMap[f.JSONName] = f
	}

	// "name" should be plain string.
	nameField := fieldMap["name"]
	if nameField.Type.GoTypeName() != "string" {
		t.Errorf("name type = %q, want %q", nameField.Type.GoTypeName(), "string")
	}
	if nameField.Type.IsPointer() {
		t.Error("name should not be a pointer")
	}

	// "nickname" should be *string.
	nicknameField := fieldMap["nickname"]
	if nicknameField.Type.GoTypeName() != "*string" {
		t.Errorf("nickname type = %q, want %q", nicknameField.Type.GoTypeName(), "*string")
	}
	if !nicknameField.Type.IsPointer() {
		t.Error("nickname should be a pointer")
	}

	// "score" should be *int64.
	scoreField := fieldMap["score"]
	if scoreField.Type.GoTypeName() != "*int64" {
		t.Errorf("score type = %q, want %q", scoreField.Type.GoTypeName(), "*int64")
	}
}

func TestGenerate_ArrayWithItems(t *testing.T) {
	s := &schema.Schema{
		Title: "Team",
		Type:  schema.TypeList{"object"},
		Properties: map[string]*schema.Schema{
			"members": {
				Type: schema.TypeList{"array"},
				Items: &schema.SchemaOrSchemaArray{
					Schema: &schema.Schema{
						Type: schema.TypeList{"string"},
					},
				},
			},
			"scores": {
				Type: schema.TypeList{"array"},
				Items: &schema.SchemaOrSchemaArray{
					Schema: &schema.Schema{
						Type: schema.TypeList{"integer"},
					},
				},
			},
		},
	}

	gen := New(DefaultConfig())
	file, err := gen.Generate(s)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	sd := file.TypeDefs[0].(*StructDef)
	fieldMap := make(map[string]FieldDef)
	for _, f := range sd.Fields {
		fieldMap[f.JSONName] = f
	}

	// Array fields are plain []T: a slice is already nilable, so no outer pointer
	// is needed. (Presence/empty constraints are a validation concern, not a type
	// shape concern.)
	membersField := fieldMap["members"]
	if membersField.Type.GoTypeName() != "[]string" {
		t.Errorf("members type = %q, want %q", membersField.Type.GoTypeName(), "[]string")
	}

	scoresField := fieldMap["scores"]
	if scoresField.Type.GoTypeName() != "[]int64" {
		t.Errorf("scores type = %q, want %q", scoresField.Type.GoTypeName(), "[]int64")
	}
}

func TestGenerate_EnumType(t *testing.T) {
	s := &schema.Schema{
		Defs: map[string]*schema.Schema{
			"Status": {
				Type: schema.TypeList{"string"},
				Enum: []any{"active", "inactive", "pending"},
			},
		},
		Title: "User",
		Type:  schema.TypeList{"object"},
		Properties: map[string]*schema.Schema{
			"status": {
				Ref: "#/$defs/Status",
			},
		},
	}

	gen := New(DefaultConfig())
	file, err := gen.Generate(s)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// Should have Status enum + User struct.
	var enumDef *EnumDef
	for _, td := range file.TypeDefs {
		if ed, ok := td.(*EnumDef); ok && ed.Name == "Status" {
			enumDef = ed
		}
	}

	if enumDef == nil {
		t.Fatal("Status enum not found")
	}

	if enumDef.BaseType.GoTypeName() != "string" {
		t.Errorf("BaseType = %q, want %q", enumDef.BaseType.GoTypeName(), "string")
	}

	if len(enumDef.Values) != 3 {
		t.Fatalf("expected 3 values, got %d", len(enumDef.Values))
	}
}

func TestGenerate_InlineEnum(t *testing.T) {
	s := &schema.Schema{
		Title: "Task",
		Type:  schema.TypeList{"object"},
		Properties: map[string]*schema.Schema{
			"title": {
				Type: schema.TypeList{"string"},
			},
			"status": {
				Type: schema.TypeList{"string"},
				Enum: []any{"pending", "in_progress", "completed"},
			},
			"priority": {
				Type: schema.TypeList{"string"},
				Enum: []any{"low", "medium", "high"},
			},
		},
		Required: []string{"title", "status"},
	}

	gen := New(DefaultConfig())
	file, err := gen.Generate(s)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// Should have TaskStatus enum + TaskPriority enum + Task struct = 3 type defs.
	if len(file.TypeDefs) != 3 {
		t.Fatalf("expected 3 TypeDefs, got %d", len(file.TypeDefs))
	}

	// Find the enum defs and the struct def.
	var statusEnum, priorityEnum *EnumDef
	var taskStruct *StructDef
	for _, td := range file.TypeDefs {
		switch d := td.(type) {
		case *EnumDef:
			switch d.Name {
			case "TaskStatus":
				statusEnum = d
			case "TaskPriority":
				priorityEnum = d
			}
		case *StructDef:
			if d.Name == "Task" {
				taskStruct = d
			}
		}
	}

	if statusEnum == nil {
		t.Fatal("TaskStatus enum not found")
	}
	if statusEnum.BaseType.GoTypeName() != "string" {
		t.Errorf("TaskStatus BaseType = %q, want %q", statusEnum.BaseType.GoTypeName(), "string")
	}
	if len(statusEnum.Values) != 3 {
		t.Fatalf("TaskStatus expected 3 values, got %d", len(statusEnum.Values))
	}
	// Check naming convention: "in_progress" → "TaskStatusInProgress"
	found := false
	for _, v := range statusEnum.Values {
		if v.Name == "TaskStatusInProgress" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected TaskStatusInProgress constant, not found")
	}

	if priorityEnum == nil {
		t.Fatal("TaskPriority enum not found")
	}
	if len(priorityEnum.Values) != 3 {
		t.Fatalf("TaskPriority expected 3 values, got %d", len(priorityEnum.Values))
	}

	if taskStruct == nil {
		t.Fatal("Task struct not found")
	}
	// Check that the struct fields reference the enum types.
	fieldMap := make(map[string]FieldDef)
	for _, f := range taskStruct.Fields {
		fieldMap[f.JSONName] = f
	}
	statusField := fieldMap["status"]
	if statusField.Type.GoTypeName() != "TaskStatus" {
		t.Errorf("status field type = %q, want %q", statusField.Type.GoTypeName(), "TaskStatus")
	}
	// priority is optional, so it is pointer-wrapped: TaskPriority is a named
	// string and omitempty would otherwise drop a member that is the empty
	// string. status, being required, keeps the bare type.
	priorityField := fieldMap["priority"]
	if priorityField.Type.GoTypeName() != "*TaskPriority" {
		t.Errorf("priority field type = %q, want %q", priorityField.Type.GoTypeName(), "*TaskPriority")
	}
	// title should remain a plain string
	titleField := fieldMap["title"]
	if titleField.Type.GoTypeName() != "string" {
		t.Errorf("title field type = %q, want %q", titleField.Type.GoTypeName(), "string")
	}
}

func TestGenerate_Definitions(t *testing.T) {
	s := &schema.Schema{
		Definitions: map[string]*schema.Schema{
			"pet": {
				Type: schema.TypeList{"object"},
				Properties: map[string]*schema.Schema{
					"name": {Type: schema.TypeList{"string"}},
					"tag":  {Type: schema.TypeList{"string"}},
				},
			},
		},
		Title: "Store",
		Type:  schema.TypeList{"object"},
		Properties: map[string]*schema.Schema{
			"pet": {
				Ref: "#/definitions/pet",
			},
		},
	}

	gen := New(DefaultConfig())
	file, err := gen.Generate(s)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	names := make(map[string]bool)
	for _, td := range file.TypeDefs {
		names[td.TypeName()] = true
	}

	if !names["Pet"] {
		t.Error("expected Pet type from definitions")
	}
	if !names["Store"] {
		t.Error("expected Store type")
	}
}

func TestGenerate_ArrayOfObjects(t *testing.T) {
	s := &schema.Schema{
		Title: "Catalog",
		Type:  schema.TypeList{"object"},
		Properties: map[string]*schema.Schema{
			"items": {
				Type: schema.TypeList{"array"},
				Items: &schema.SchemaOrSchemaArray{
					Schema: &schema.Schema{
						Type: schema.TypeList{"object"},
						Properties: map[string]*schema.Schema{
							"id":   {Type: schema.TypeList{"integer"}},
							"name": {Type: schema.TypeList{"string"}},
						},
					},
				},
			},
		},
	}

	gen := New(DefaultConfig())
	file, err := gen.Generate(s)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// Should have Catalog struct + CatalogItemsItem struct.
	if len(file.TypeDefs) != 2 {
		t.Fatalf("expected 2 TypeDefs, got %d", len(file.TypeDefs))
	}

	names := make(map[string]bool)
	for _, td := range file.TypeDefs {
		names[td.TypeName()] = true
	}

	if !names["Catalog"] {
		t.Error("expected Catalog type")
	}
	if !names["CatalogItemsItem"] {
		t.Error("expected CatalogItemsItem type for nested array item")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.PackageName != "generated" {
		t.Errorf("PackageName = %q, want %q", cfg.PackageName, "generated")
	}
	if cfg.OutputDir != "." {
		t.Errorf("OutputDir = %q, want %q", cfg.OutputDir, ".")
	}
	if !cfg.OmitEmpty {
		t.Error("OmitEmpty should be true by default")
	}
}

// TestGenerateDoesNotMutateInputSchema ensures a const/const-null property
// schema is not mutated in place (a synthesized Enum used to leak onto the
// shared node), so a second Generate over the same tree is deterministic.
func TestGenerateDoesNotMutateInputSchema(t *testing.T) {
	input := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "Config",
		"type": "object",
		"properties": {
			"version": {"const": "2.0"},
			"kind": {"const": null}
		},
		"required": ["version"]
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	version := s.Properties["version"]
	kind := s.Properties["kind"]
	if len(version.Enum) != 0 || len(kind.Enum) != 0 {
		t.Fatalf("precondition: property schemas already carry an Enum")
	}

	emit := func() string {
		gen := New(Config{PackageName: "testpkg"})
		ir, err := gen.Generate(&s)
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		var names []string
		for _, td := range ir.TypeDefs {
			names = append(names, td.TypeName())
		}
		return strings.Join(names, ",")
	}

	first := emit()

	if len(version.Enum) != 0 {
		t.Errorf("version schema mutated: Enum = %v", version.Enum)
	}
	if len(kind.Enum) != 0 {
		t.Errorf("kind schema mutated: Enum = %v", kind.Enum)
	}

	second := emit()
	if first != second {
		t.Errorf("Generate not idempotent:\n first:  %s\n second: %s", first, second)
	}
}

// TestIntegerConstraintOnlyOneOfPreservesTypeAndVariants is a regression for the
// dispatch arm that turned {"type":"integer","oneOf":[...]} into `type Root any`,
// dropping the declared type, its constraints, and the oneOf itself. The schema
// must instead produce an int64 AliasDef whose OneOfVariants are populated.
func TestIntegerConstraintOnlyOneOfPreservesTypeAndVariants(t *testing.T) {
	input := `{"type":"integer","oneOf":[{"minimum":10},{"maximum":5}]}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg"})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var root *AliasDef
	for _, td := range ir.TypeDefs {
		if a, ok := td.(*AliasDef); ok && a.Name == "Root" {
			root = a
		}
	}
	if root == nil {
		t.Fatalf("expected an AliasDef named Root, got %#v", ir.TypeDefs)
	}
	pt, ok := root.Underlying.(*PrimitiveType)
	if !ok || pt.Name != "int64" {
		t.Fatalf("Root underlying = %#v, want *PrimitiveType{int64} (not any)", root.Underlying)
	}
	if len(root.OneOfVariants) != 2 {
		t.Fatalf("Root.OneOfVariants = %#v, want 2 non-empty variants", root.OneOfVariants)
	}
}

// TestConstraintOnlyOneOfImportsUTF8 guards the import side of the arm above: the
// oneOf branch checks emitted for a string alias call utf8.RuneCountInString, so
// "unicode/utf8" must be imported. The import scan covered Validations and
// AnyOfVariants but not OneOfVariants, producing uncompilable output.
func TestConstraintOnlyOneOfImportsUTF8(t *testing.T) {
	input := `{"type":"string","oneOf":[{"minLength":2},{"maxLength":4}]}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var paths []string
	for _, imp := range ir.Imports {
		paths = append(paths, imp.Path)
	}
	if !containsString(paths, "unicode/utf8") {
		t.Fatalf("imports = %v, want unicode/utf8 (oneOf branches call utf8.RuneCountInString)", paths)
	}
}

// TestRequiredOnlyOneOfGeneratesObjectUnion is a regression for the dispatch arm
// that keyed "is this an object union?" off oneOf variants having properties. A
// variant carrying only required keys constrains the object just as much, and
// narrowing the check dropped the branch validation entirely — Validate() became
// `return nil`, accepting objects that match both branches or neither.
func TestRequiredOnlyOneOfGeneratesObjectUnion(t *testing.T) {
	input := `{"type":"object","oneOf":[{"required":["foo","bar"]},{"required":["foo","baz"]}]}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var root *StructDef
	for _, td := range ir.TypeDefs {
		if sd, ok := td.(*StructDef); ok && sd.Name == "Root" {
			root = sd
		}
	}
	if root == nil {
		t.Fatalf("expected a StructDef named Root, got %#v", ir.TypeDefs)
	}
	if len(root.ObjectOneOfs) != 1 {
		t.Fatalf("Root.ObjectOneOfs = %#v, want exactly 1 oneOf group", root.ObjectOneOfs)
	}
	if got := len(root.ObjectOneOfs[0].Branches); got != 2 {
		t.Fatalf("oneOf group has %d branches, want 2", got)
	}
}

// TestNestedOneOfKeepsItsSiblingProperties is a regression for a property whose
// schema is an object with both its own properties/required and a oneOf. Every
// such property was routed to the sealed-interface union, which is built from
// the oneOf branches alone: the object's own properties never appeared in any
// generated type, so "h" — and the `required` that named it — were gone, and
// {"f":{"tagOne":41}} was accepted. The same schema at the document root, and
// anyOf in the same position, were always flattened correctly; this brings the
// property position in line with both.
func TestNestedOneOfKeepsItsSiblingProperties(t *testing.T) {
	input := `{"type":"object","properties":{
		"f":{"type":"object","properties":{"h":{"type":"boolean"}},"required":["h"],
		     "oneOf":[{"properties":{"tagOne":{"type":"integer"}},"required":["tagOne"]},
		              {"properties":{"tagTwo":{"type":"string"}},"required":["tagTwo"]}]}}}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var root, nested *StructDef
	for _, td := range ir.TypeDefs {
		if sd, ok := td.(*StructDef); ok {
			switch sd.Name {
			case "Root":
				root = sd
			case "RootF":
				nested = sd
			}
		}
	}
	if root == nil {
		t.Fatalf("expected a StructDef named Root, got %#v", ir.TypeDefs)
	}
	if len(root.OneOfs) != 0 {
		t.Fatalf("Root.OneOfs = %#v, want none: the union would drop f's own properties", root.OneOfs)
	}
	if nested == nil {
		t.Fatalf("expected a StructDef named RootF carrying f's own shape, got %#v", ir.TypeDefs)
	}

	var haveH bool
	for _, f := range nested.Fields {
		if f.JSONName == "h" {
			haveH = true
		}
	}
	if !haveH {
		t.Fatalf("RootF.Fields = %#v, want a field for property \"h\"", nested.Fields)
	}
	if !containsString(nested.RequiredJSON, "h") {
		t.Fatalf("RootF.RequiredJSON = %v, want it to contain \"h\"", nested.RequiredJSON)
	}
	if len(nested.ObjectOneOfs) != 1 {
		t.Fatalf("RootF.ObjectOneOfs = %#v, want exactly 1 oneOf group", nested.ObjectOneOfs)
	}
	if got := len(nested.ObjectOneOfs[0].Branches); got != 2 {
		t.Fatalf("oneOf group has %d branches, want 2", got)
	}
}

// TestOneOfOnlyPropertyStillGeneratesUnion pins the shape the arm above must not
// disturb. A property whose schema is nothing but a oneOf of object $refs is the
// discriminated union real callers depend on, and it has to keep generating the
// sealed interface — the sibling rule applies only when there are siblings.
func TestOneOfOnlyPropertyStillGeneratesUnion(t *testing.T) {
	input := `{"type":"object",
		"$defs":{"C":{"type":"object","properties":{"radius":{"type":"number"}},"required":["radius"]},
		         "R":{"type":"object","properties":{"w":{"type":"number"}},"required":["w"]}},
		"properties":{"shape":{"oneOf":[{"$ref":"#/$defs/C"},{"$ref":"#/$defs/R"}]}},
		"required":["shape"]}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var root *StructDef
	for _, td := range ir.TypeDefs {
		if sd, ok := td.(*StructDef); ok && sd.Name == "Root" {
			root = sd
		}
	}
	if root == nil {
		t.Fatalf("expected a StructDef named Root, got %#v", ir.TypeDefs)
	}
	if len(root.OneOfs) != 1 {
		t.Fatalf("Root.OneOfs = %#v, want exactly 1 sealed-interface union", root.OneOfs)
	}
	if got := len(root.OneOfs[0].Variants); got != 2 {
		t.Fatalf("union has %d variants, want 2", got)
	}
}

// TestOneOfVariantSelectionCarriesBranchConstraints is a regression for variant
// selection in a union over typed scalars. Selection asked only whether the raw
// JSON decoded into the variant's Go type, so {"a":"z"} matched the string branch
// despite its minLength 3 — and nothing downstream rechecked, because the wrapper
// holds a plain Go string with no Validate and the parent's Validate does not
// descend into the union. The branch's own keywords have to reach the match.
func TestOneOfVariantSelectionCarriesBranchConstraints(t *testing.T) {
	input := `{"type":"object",
		"properties":{"a":{"oneOf":[{"type":"string","minLength":3},
		                            {"type":"integer","minimum":5}]}},
		"required":["a"]}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var root *StructDef
	for _, td := range ir.TypeDefs {
		if sd, ok := td.(*StructDef); ok && sd.Name == "Root" {
			root = sd
		}
	}
	if root == nil {
		t.Fatalf("expected a StructDef named Root, got %#v", ir.TypeDefs)
	}
	if len(root.OneOfs) != 1 || len(root.OneOfs[0].Variants) != 2 {
		t.Fatalf("Root.OneOfs = %#v, want one union of 2 variants", root.OneOfs)
	}

	want := map[string]string{"String": "minLength", "Integer": "minimum"}
	for _, v := range root.OneOfs[0].Variants {
		ruleType, ok := want[v.FieldName]
		if !ok {
			t.Fatalf("unexpected variant %q", v.FieldName)
		}
		if len(v.Checks) != 1 || v.Checks[0].RuleType != ruleType {
			t.Fatalf("variant %s Checks = %#v, want one %q check", v.FieldName, v.Checks, ruleType)
		}
	}
}

// TestOneOfVariantChecksSkipUncheckableVariants guards the other half of the arm
// above: a check is only emitted when it has a direct expression over the
// candidate's Go type. A constraint-only branch resolves to `any`, and emitting
// utf8.RuneCountInString or float64() over that produces uncompilable output.
func TestOneOfVariantChecksSkipUncheckableVariants(t *testing.T) {
	input := `{"type":"object",
		"properties":{"a":{"oneOf":[{"minLength":3},{"minimum":5}]}},
		"required":["a"]}`

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
		sd, ok := td.(*StructDef)
		if !ok {
			continue
		}
		for _, oof := range sd.OneOfs {
			for _, v := range oof.Variants {
				if pt, isPrim := v.Type.(*PrimitiveType); isPrim && pt.Name == "any" && len(v.Checks) > 0 {
					t.Fatalf("variant %s is typed any but carries checks %#v", v.FieldName, v.Checks)
				}
			}
		}
	}
}

// TestTwoScalarOneOfsOnOneStructGetDistinctMembers is a regression for variant
// naming. A variant's name becomes a package-level wrapper type (Parent_Name)
// and a method (Parent.GetName), but duplicates were only resolved inside a
// single oneOf group. Primitive variants all draw from the same four names, so
// two scalar oneOf properties on one struct both claimed "String" and "Integer"
// and the output did not compile: "Root_String redeclared in this block",
// "method Root.GetString already declared".
func TestTwoScalarOneOfsOnOneStructGetDistinctMembers(t *testing.T) {
	input := `{"type":"object","properties":{
		"bravo":{"oneOf":[{"type":"string","minLength":3},{"type":"integer","minimum":7}]},
		"charlie":{"oneOf":[{"type":"string","minLength":3},{"type":"integer","minimum":-2}]}}}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var root *StructDef
	for _, td := range ir.TypeDefs {
		if sd, ok := td.(*StructDef); ok && sd.Name == "Root" {
			root = sd
		}
	}
	if root == nil {
		t.Fatalf("expected a StructDef named Root, got %#v", ir.TypeDefs)
	}
	if len(root.OneOfs) != 2 {
		t.Fatalf("Root.OneOfs = %#v, want 2 unions", root.OneOfs)
	}

	seenWrapper := map[string]string{}
	seenField := map[string]string{}
	for _, oof := range root.OneOfs {
		for _, v := range oof.Variants {
			if prev, dup := seenWrapper[v.WrapperName]; dup {
				t.Fatalf("wrapper type %q claimed by both %s and %s", v.WrapperName, prev, oof.FieldName)
			}
			seenWrapper[v.WrapperName] = oof.FieldName
			// The getter is Get<FieldName> on the parent, so the field names
			// have to be distinct across groups too, not just the wrappers.
			if prev, dup := seenField[v.FieldName]; dup {
				t.Fatalf("getter Get%s claimed by both %s and %s", v.FieldName, prev, oof.FieldName)
			}
			seenField[v.FieldName] = oof.FieldName
		}
	}
}

// TestStructWhoseOnlyPropertyIsAUnionKeepsUnknownKeys is a regression for the
// round-trip overflow map. A property rendered as a sealed interface leaves
// generateStructDef through OneOfs rather than Fields, so a struct whose
// properties are all unions looked propertyless to the arm that adds the map and
// got none — every key it did not declare was dropped on marshal. That includes
// a key declared only inside one of the struct's own object-level oneOf
// branches, which is precisely the key that says which branch the value took.
func TestStructWhoseOnlyPropertyIsAUnionKeepsUnknownKeys(t *testing.T) {
	input := `{"type":"object",
		"oneOf":[{"properties":{"tagOne":{"type":"integer"}},"required":["tagOne"]},
		         {"properties":{"tagTwo":{"type":"boolean"}},"required":["tagTwo"]}],
		"properties":{"charlie":{"oneOf":[{"type":"string","minLength":3},
		                                  {"type":"integer","minimum":-9}]}}}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var root *StructDef
	for _, td := range ir.TypeDefs {
		if sd, ok := td.(*StructDef); ok && sd.Name == "Root" {
			root = sd
		}
	}
	if root == nil {
		t.Fatalf("expected a StructDef named Root, got %#v", ir.TypeDefs)
	}
	if len(root.Fields) != 0 {
		t.Fatalf("Root.Fields = %#v, want none (charlie is a union)", root.Fields)
	}
	if root.AdditionalProperties == nil {
		t.Fatalf("Root has no overflow map, so tagOne/tagTwo are dropped on marshal")
	}
}

// TestTypeLevelUnionGetsNoOverflowMap pins the other side of the arm above. When
// the oneOf stands for the type itself rather than a property, MarshalJSON writes
// the selected variant as the whole object: there is no aux struct to merge an
// overflow map back into, so adding one would capture keys it could never emit.
func TestTypeLevelUnionGetsNoOverflowMap(t *testing.T) {
	input := `{"type":"object","oneOf":[{"required":["foo","bar"]},{"required":["foo","baz"]}]}`

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
		sd, ok := td.(*StructDef)
		if !ok || sd.Name != "Root" {
			continue
		}
		if len(sd.OneOfs) != 1 || sd.OneOfs[0].JSONName != "" {
			t.Fatalf("Root.OneOfs = %#v, want one type-level union", sd.OneOfs)
		}
		if sd.AdditionalProperties != nil {
			t.Fatalf("Root has an overflow map its MarshalJSON never emits")
		}
	}
}

// TestPropertyNameCollidesWithGeneratedMember is a regression for C3: property
// names that derive to a generated member (Validate method, AdditionalProperties
// overflow field, etc.) must not collide — the derived field name is renamed via
// the numeric-suffix mechanism while the JSON tag keeps the original property
// name, so the wire format is unaffected.
func TestPropertyNameCollidesWithGeneratedMember(t *testing.T) {
	input := `{
		"type": "object",
		"properties": {
			"validate": {"type": "string"},
			"additionalProperties": {"type": "boolean"},
			"pattern_properties": {"type": "string"}
		},
		"required": ["validate"],
		"additionalProperties": true,
		"patternProperties": {"^x": {"type": "string"}}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg"})
	file, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("Generate() returned an error for colliding property names: %v", err)
	}

	// Found by name rather than by position: the schema's patternProperties
	// sub-schema is materialized into a type of its own, and a nested type is
	// emitted before the struct that refers to it, so index 0 is not the root.
	sd := structNamed(t, file, "Root")

	// JSON tags (JSONName) must be preserved verbatim for every property.
	for _, jsonName := range []string{"validate", "additionalProperties", "pattern_properties"} {
		f, ok := fieldByJSONName(sd, jsonName)
		if !ok {
			t.Fatalf("no field with JSON name %q", jsonName)
		}
		// The derived Go name must NOT be a bare generated-member name.
		for _, member := range generatedMemberNames {
			if f.Name == member {
				t.Errorf("property %q kept Go field name %q, which collides with generated member %q", jsonName, f.Name, member)
			}
		}
	}

	// No two Go field names may be equal, and none may equal a generated member.
	seen := map[string]string{}
	taken := map[string]bool{}
	for _, m := range generatedMemberNames {
		taken[m] = true
	}
	for _, f := range sd.Fields {
		if prev, dup := seen[f.Name]; dup {
			t.Errorf("Go field name %q used for both %q and %q", f.Name, prev, f.JSONName)
		}
		seen[f.Name] = f.JSONName
		if taken[f.Name] {
			t.Errorf("Go field name %q for property %q collides with a generated member", f.Name, f.JSONName)
		}
	}
}

// TestNullPropertySchemaReturnsError is a regression for the nil pointer panic on
// {"properties":{"a":null}}: a null property schema must produce an actionable
// error naming the property, not crash the generator.
func TestNullPropertySchemaReturnsError(t *testing.T) {
	input := `{"type":"object","properties":{"a":null}}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	gen := New(Config{PackageName: "testpkg"})
	_, err := gen.Generate(&s)
	if err == nil {
		t.Fatalf("expected an error for a null property schema, got nil")
	}
	// The null is now caught by Generate's up-front sweep of every schema
	// container, which names the offender by JSON Pointer rather than by
	// property name alone -- a strictly more precise location.
	if !strings.Contains(err.Error(), "#/properties/a") {
		t.Fatalf("error %q does not locate the null property schema at %q", err.Error(), "#/properties/a")
	}
}

// A $ref reaching into a resolver-fetched document, whose target then refs a
// plain-name anchor of its own document. The generator's anchor index only
// covers the root document, so the inner "#foo" is resolved by LocalResolver
// against the fetched document -- which used to miss the pre-2019-09 spelling
// ({"id": "#foo"}) and silently degrade the type to any.
func TestRefIntoRemoteDocumentResolvesLegacyAnchor(t *testing.T) {
	var remote schema.Schema
	if err := json.Unmarshal([]byte(`{
		"definitions": {
			"refToInteger": {"$ref": "#foo"},
			"A": {"id": "#foo", "type": "integer"}
		}
	}`), &remote); err != nil {
		t.Fatal(err)
	}
	remote.Normalize()

	const docURI = "http://example.com/legacy/locationIndependentIdentifier.json"
	resolver := schema.NewMappingResolver(map[string]*schema.Schema{docURI: &remote})

	var root schema.Schema
	if err := json.Unmarshal([]byte(`{
		"type": "object",
		"properties": {"v": {"$ref": "`+docURI+`#/definitions/refToInteger"}}
	}`), &root); err != nil {
		t.Fatal(err)
	}
	root.Normalize()

	// Not lenient: an unresolved ref must surface as an error rather than as a
	// silently any-typed field.
	g := New(Config{PackageName: "testpkg", Resolver: resolver, Draft: schema.Draft04})
	if _, err := g.Generate(&root); err != nil {
		t.Fatalf("generate: %v", err)
	}
}

// A $ref to a subschema that carries its own full-URI $id, in a document that
// declares no $id of its own. The subschema is indexed under its $id, but the
// lookup was gated on the document having a base URI -- so in a document
// without one, an absolute $ref never reached the index and degraded to any.
// $id-bearing subschemas of if/then/else are the case the test suite covers.
func TestAbsoluteRefToIDBearingSubschemaWithoutDocumentID(t *testing.T) {
	for _, kw := range []string{"if", "then", "else"} {
		t.Run(kw, func(t *testing.T) {
			src := `{
				"$schema": "https://json-schema.org/draft/2020-12/schema",
				"$ref": "http://example.com/ref/` + kw + `",
				"` + kw + `": {"$id": "http://example.com/ref/` + kw + `", "type": "integer"}
			}`
			var s schema.Schema
			if err := json.Unmarshal([]byte(src), &s); err != nil {
				t.Fatal(err)
			}
			s.Normalize()
			s.ComputeBaseURIs(nil, &s)

			// Not lenient: an unresolved ref must fail rather than degrade.
			if _, err := New(Config{PackageName: "testpkg"}).Generate(&s); err != nil {
				t.Fatalf("generate: %v", err)
			}
		})
	}
}

// A resolver may resolve a ref's fragment itself and hand back the *sub*schema
// rather than the document (MappingResolver does). Registering that subschema as
// though it were the document made it its own DocumentRoot, putting its siblings
// out of scope -- so a $dynamicRef to a $dynamicAnchor declared beside it could
// not be found, and the reference degraded to any.
func TestDynamicRefFindsAnchorBesideTargetInFetchedDocument(t *testing.T) {
	var remote schema.Schema
	if err := json.Unmarshal([]byte(`{
		"$id": "http://example.com/detached-dynamicref.json",
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$defs": {
			"foo": {"$dynamicRef": "#detached"},
			"detached": {"$dynamicAnchor": "detached", "type": "integer"}
		}
	}`), &remote); err != nil {
		t.Fatal(err)
	}
	remote.Normalize()

	const docURI = "http://example.com/detached-dynamicref.json"
	resolver := schema.NewMappingResolver(map[string]*schema.Schema{docURI: &remote})

	var root schema.Schema
	if err := json.Unmarshal([]byte(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$ref": "`+docURI+`#/$defs/foo"
	}`), &root); err != nil {
		t.Fatal(err)
	}
	root.Normalize()
	root.ComputeBaseURIs(nil, &root)

	// Not lenient: the $dynamicRef must resolve, not degrade.
	if _, err := New(Config{PackageName: "testpkg", Resolver: resolver}).Generate(&root); err != nil {
		t.Fatalf("generate: %v", err)
	}
}

// A self-referential document reached through a resolver used to recurse without
// end. Every JSON Schema meta-schema is such a document: {"$ref":"#"} inside a
// property pulls the document root's properties back in, so that property is
// re-entered with a context name one segment longer each time
// (SchemaAdditionalItems, SchemaAdditionalItemsAdditionalItems, ...). The
// generated/in-progress guards are keyed on the name, so they never fired, and
// generation consumed memory until the process was killed.
func TestSelfReferentialFetchedDocumentTerminates(t *testing.T) {
	// The shape that matters, reduced from the draft-04 meta-schema: a property
	// whose anyOf refs the document root, which has that property.
	var meta schema.Schema
	if err := json.Unmarshal([]byte(`{
		"id": "http://example.com/meta.json",
		"type": "object",
		"properties": {
			"additionalItems": {"anyOf": [{"type": "boolean"}, {"$ref": "#"}]}
		}
	}`), &meta); err != nil {
		t.Fatal(err)
	}
	meta.Normalize()

	resolver := schema.NewMappingResolver(map[string]*schema.Schema{
		"http://example.com/meta.json": &meta,
	})

	var root schema.Schema
	if err := json.Unmarshal([]byte(`{"$ref": "http://example.com/meta.json#"}`), &root); err != nil {
		t.Fatal(err)
	}
	root.Normalize()
	root.ComputeBaseURIs(nil, &root)

	done := make(chan int, 1)
	go func() {
		ir, err := New(Config{PackageName: "testpkg", Resolver: resolver, LenientRefs: true, Draft: schema.Draft04}).Generate(&root)
		if err != nil {
			done <- -1
			return
		}
		done <- len(ir.TypeDefs)
	}()

	select {
	case n := <-done:
		if n < 0 {
			t.Fatal("generate returned an error")
		}
		// The exact count is not the point; terminating with a bounded set is.
		if n > 50 {
			t.Errorf("produced %d type defs for a 2-node document; names are still being re-derived per traversal", n)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("generation did not terminate: self-reference is recursing without bound")
	}
}

// generateJSON runs the parse → normalize → generate pipeline the CLI uses, so
// a regression test exercises the same path a user's schema file takes.
func generateJSON(t *testing.T, cfg Config, input string) (*File, error) {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	return New(cfg).Generate(&s)
}

// TestNullSubschemaInContainerReturnsError covers the null-element form of the
// nil dereference across every keyword that holds a list or map of schemas.
// json.Unmarshal turns each of these into a nil *Schema sitting inside the
// container, which used to reach IsFalseSchema and friends and segfault the
// CLI. Every one must instead come back as an error that points at the null.
//
// {"extends":[null]} is in the list because Normalize *manufactures* the nil:
// it appends the parsed "extends" array straight onto AllOf, so a draft-3
// document produces the defect that draft-3 documents cannot express directly.
func TestNullSubschemaInContainerReturnsError(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"allOf", `{"allOf":[null]}`, "#/allOf/0"},
		{"anyOf", `{"anyOf":[null]}`, "#/anyOf/0"},
		{"oneOf", `{"oneOf":[null]}`, "#/oneOf/0"},
		{"defs", `{"$defs":{"a":null}}`, "#/$defs/a"},
		{"definitions", `{"definitions":{"a":null}}`, "#/$defs/a"},
		{"patternProperties", `{"patternProperties":{"^a":null}}`, "#/patternProperties/^a"},
		{"dependentSchemas", `{"dependentSchemas":{"a":null}}`, "#/dependentSchemas/a"},
		{"prefixItems", `{"prefixItems":[null]}`, "#/prefixItems/0"},
		{"itemsArray", `{"items":[null]}`, "#/items/0"},
		{"extends", `{"extends":[null]}`, "#/allOf/0"},
		{"nestedInProperty", `{"type":"object","properties":{"a":{"allOf":[null]}}}`, "#/properties/a/allOf/0"},
		{"nestedInItems", `{"type":"array","items":{"oneOf":[null]}}`, "#/items/oneOf/0"},
		// A vendor keyword's value is only parsed as a schema when a $ref
		// reaches into it, which is after Generate has checked its argument.
		{"insideExtension", `{"examples":[{"allOf":[null]}],"$ref":"#/examples/0"}`, "#/examples/0/allOf/0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := generateJSON(t, Config{PackageName: "testpkg"}, tc.input)
			if err == nil {
				t.Fatalf("expected an error for %s, got nil", tc.input)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not locate the null schema at %q", err.Error(), tc.want)
			}
		})
	}
}

// TestRefCycleTerminates covers $ref cycles that used to run generateTypeDef,
// mergeAllOfInto or its inner ref-following loop without end.
//
// These are not ordinary test failures. A runaway recursion ends in "fatal
// error: stack overflow", which no recover intercepts -- it takes the whole
// test binary down -- and the mergeAllOfInto loop does not even do that: it
// spins at constant stack and constant memory forever. So the work runs on its
// own goroutine behind a deadline, and a regression reports as a failed test
// rather than as a dead or hung process.
func TestRefCycleTerminates(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"rootSelfRef", `{"$ref":"#"}`},
		{"rootSelfRefWithType", `{"type":"object","$ref":"#"}`},
		{"defSelfRef", `{"$defs":{"a":{"$ref":"#/$defs/a"}},"$ref":"#/$defs/a"}`},
		{"mutualDefs", `{"$defs":{"a":{"$ref":"#/$defs/b"},"b":{"$ref":"#/$defs/a"}},"$ref":"#/$defs/a"}`},
		{"threeCycle", `{"$defs":{"a":{"$ref":"#/$defs/b"},"b":{"$ref":"#/$defs/c"},"c":{"$ref":"#/$defs/a"}},"$ref":"#/$defs/a"}`},
		{"anchorSelfRef", `{"$anchor":"x","$ref":"#x"}`},
		{"idSelfRef", `{"$id":"http://a/","$ref":"http://a/"}`},
		{"refIntoOwnProperties", `{"type":"object","properties":{"a":{"$ref":"#/properties/a"}}}`},
		{"refIntoOwnItems", `{"type":"array","items":{"$ref":"#/items"}}`},
		{"dynamicRefSelfAnchor", `{"$schema":"https://json-schema.org/draft/2020-12/schema","$dynamicAnchor":"a","$dynamicRef":"#a"}`},
		{"recursiveRefSelfAnchor", `{"$schema":"https://json-schema.org/draft/2019-09/schema","$recursiveAnchor":true,"$recursiveRef":"#"}`},
		{"allOfSelfRef", `{"$defs":{"a":{"allOf":[{"$ref":"#/$defs/a"}]}},"$ref":"#/$defs/a"}`},
		{"allOfTwoRefsCycle", `{"$defs":{"a":{"allOf":[{"$ref":"#/$defs/b"}]},"b":{"allOf":[{"$ref":"#/$defs/a"}]}},"$ref":"#/$defs/a"}`},
		// Cluster of its own: the ref resolves to the node the merge loop is
		// already sitting on, and none of the structural keywords the loop's
		// break condition tests are present, so it never advanced.
		{"allOfRefToItself", `{"allOf":[{"$ref":"#/allOf/0"}]}`},
		// Cluster of its own, again: a $ref with a *structural sibling*, sitting
		// under "properties". The sibling disqualifies the schema from both
		// ref-only arms of generateTypeDef -- the ones refCycleAliasDef guards --
		// so it is routed to the implicit-allOf/struct path, and the property
		// loop there hands the very same node back to resolvePropertyType under
		// a name one segment longer. Nothing was keyed on node identity along
		// that route, so the names grew and the recursion never closed.
		{"refSiblingItemsSelf", `{"properties":{"a":{"$ref":"#","items":{"type":"string"}}}}`},
		{"refSiblingItemsToDef", `{"properties":{"a":{"$ref":"#","items":{"$ref":"#/$defs/S"}}},"$defs":{"S":{"type":"object"}}}`},
		{"refSiblingItemsSelfRef", `{"type":"object","properties":{"a":{"$ref":"#","items":{"$ref":"#"}}}}`},
		// Every other keyword that hasRefStructuralSiblings recognizes reaches
		// the same loop by the same route; "items" is not special.
		{"refSiblingProperties", `{"properties":{"a":{"$ref":"#","properties":{"b":{"type":"string"}}}}}`},
		{"refSiblingAdditionalProperties", `{"properties":{"a":{"$ref":"#","additionalProperties":{"type":"string"}}}}`},
		{"refSiblingPatternProperties", `{"properties":{"a":{"$ref":"#","patternProperties":{"^b":{"type":"string"}}}}}`},
		{"refSiblingPrefixItems", `{"properties":{"a":{"$ref":"#","prefixItems":[{"type":"string"}]}}}`},
		{"refSiblingUnevaluatedProperties", `{"properties":{"a":{"$ref":"#","unevaluatedProperties":false}}}`},
		// The same shape one level down, so the cycle closes on a $defs node
		// rather than on the document root.
		{"refSiblingItemsViaDefs", `{"$ref":"#/$defs/n","$defs":{"n":{"type":"object","properties":{"a":{"$ref":"#/$defs/n","items":{"type":"string"}}}}}}`},
		// Draft-3 type alternatives, found by the fuzzer. A $ref inside the
		// "type" array is materialized through resolveType, which is the one
		// route into type generation that materializeNamed does not cover; the
		// name derived from the ref string is the one already in flight, so the
		// arm re-entered itself in a single hop.
		{"typeSchemaRefSelfDef", `{"type":"object","$defs":{"C":{"type":[{"$ref":"#/$defs/C"}]}}}`},
		{"typeSchemaRefSelfDefRoot", `{"$defs":{"C":{"type":[{"$ref":"#/$defs/C"}]}},"$ref":"#/$defs/C"}`},
		{"typeSchemaRefMutualDefs", `{"$defs":{"A":{"type":[{"$ref":"#/$defs/B"}]},"B":{"type":[{"$ref":"#/$defs/A"}]}},"$ref":"#/$defs/A"}`},
		// A subschema whose own $id makes its $ref resolve straight back to
		// itself, found by the fuzzer. Nesting "0" under a base of "a:/0"
		// re-normalizes it to "a:///0", which is a URI the root does not answer
		// to -- so isSelfRefInContext says no, the ref resolves to the node it
		// started from, and the applicator ref-following loops spin at constant
		// stack. The legacy draft-4 "id" spelling is how the fuzzer found it;
		// "$id" reaches the same place.
		{"legacyIDRefResolvesToSelf", `{"id":"A:/0","properties":{"":{"id":"0","$ref":"0"}},"$ref":"0"}`},
		{"idRefResolvesToSelf", `{"$id":"a:/0","properties":{"a":{"$id":"0","$ref":"0"}},"$ref":"0"}`},
		// The same self-resolving node reached as an applicator variant, where
		// it spun the second of those loops instead.
		{"variantIDRefResolvesToSelfOneOf", `{"$id":"a:/0","allOf":[{"oneOf":[{"$id":"0","$ref":"0"}]}]}`},
		{"variantIDRefResolvesToSelfAnyOf", `{"$id":"a:/0","allOf":[{"anyOf":[{"$id":"0","$ref":"0"}]}]}`},
		{"variantIDRefResolvesToSelfThen", `{"$id":"a:/0","allOf":[{"then":{"$id":"0","$ref":"0"}}]}`},
		// Cluster of its own: an ordinary $defs cycle closed through an
		// applicator rather than through a ref chain. The variant merge
		// descends into the resolved node's own oneOf/anyOf/allOf/then, so a
		// node naming itself there re-entered the merge forever -- a stack
		// overflow, not a spin.
		{"variantMergeOneOfCycle", `{"allOf":[{"oneOf":[{"$ref":"#/$defs/A"}]}],"$defs":{"A":{"properties":{"x":{"type":"string"}},"oneOf":[{"$ref":"#/$defs/A"}]}}}`},
		{"variantMergeAnyOfCycle", `{"allOf":[{"oneOf":[{"$ref":"#/$defs/A"}]}],"$defs":{"A":{"properties":{"x":{"type":"string"}},"anyOf":[{"$ref":"#/$defs/A"}]}}}`},
		{"variantMergeAllOfCycle", `{"allOf":[{"oneOf":[{"$ref":"#/$defs/A"}]}],"$defs":{"A":{"properties":{"x":{"type":"string"}},"allOf":[{"$ref":"#/$defs/A"}]}}}`},
		{"variantMergeThenCycle", `{"allOf":[{"oneOf":[{"$ref":"#/$defs/A"}]}],"$defs":{"A":{"properties":{"x":{"type":"string"}},"then":{"$ref":"#/$defs/A"}}}}`},
		// And the same cycle read by the branch collector that builds the
		// runtime oneOf discriminator checks, which walks allOf on its own.
		{"oneOfBranchAllOfCycle", `{"type":"object","oneOf":[{"$ref":"#/$defs/A"},{"required":["y"]}],"$defs":{"A":{"required":["x"],"allOf":[{"$ref":"#/$defs/A"}]}}}`},
		{"oneOfBranchAllOfCycleUnderAllOf", `{"type":"object","allOf":[{"oneOf":[{"$ref":"#/$defs/A"},{"required":["y"]}]}],"$defs":{"A":{"required":["x"],"allOf":[{"$ref":"#/$defs/A"}]}}}`},
		// Cluster of its own, found by the fuzzer: a $ref with an *array*
		// structural sibling. The sibling routes the schema through the
		// implicit-allOf arm, whose array branch asks whether the synthesized
		// $ref branch is an array alias and generates it on demand to find out.
		// That branch resolves back to the definition in flight, and the only
		// guard on the on-demand generation was g.generated, which is not set
		// until a definition completes.
		{"refSiblingItemsAtRoot", `{"$ref":"#","items":{}}`},
		{"refSiblingItemsTrueAtRoot", `{"$ref":"#","items":true}`},
		{"refSiblingPrefixItemsAtRoot", `{"$ref":"#","prefixItems":[{}]}`},
		{"refSiblingItemsArrayAtRoot", `{"$ref":"#","items":[{}]}`},
		{"refSiblingUnevaluatedItemsAtRoot", `{"$ref":"#","unevaluatedItems":{}}`},
		// The same shape one level down and across two definitions, so the
		// cycle closes on a $defs node rather than on the document root.
		{"refSiblingItemsSelfDef", `{"$ref":"#/$defs/A","$defs":{"A":{"$ref":"#/$defs/A","items":{}}}}`},
		{"refSiblingItemsMutualDefs", `{"$defs":{"A":{"$ref":"#/$defs/B","items":{}},"B":{"$ref":"#/$defs/A","items":{}}},"$ref":"#/$defs/A"}`},
		// Cluster of its own, found by the fuzzer: the unevaluatedItems and
		// unevaluatedProperties analyses. Deciding what a value has already had
		// evaluated means walking $ref and every in-place applicator, and none
		// of those three walks -- the item counter, the evaluated-property
		// collector, and the allOf property probe that routes the schema in the
		// first place -- kept track of where it had been.
		{"unevaluatedItemsSelfRef", `{"$ref":"#","unevaluatedItems":false}`},
		{"unevaluatedItemsAllOfSelfRef", `{"type":"array","prefixItems":[{}],"unevaluatedItems":false,"allOf":[{"$ref":"#"}]}`},
		{"unevaluatedItemsAllOfSelfDef", `{"$defs":{"A":{"prefixItems":[{}],"unevaluatedItems":false,"allOf":[{"$ref":"#/$defs/A"}]}},"$ref":"#/$defs/A"}`},
		{"unevaluatedPropsAllOfSelfRef", `{"type":"object","properties":{"a":{}},"unevaluatedProperties":false,"allOf":[{"$ref":"#"}]}`},
		{"unevaluatedPropsAnyOfSelfRef", `{"type":"object","properties":{"a":{}},"unevaluatedProperties":false,"anyOf":[{"$ref":"#"}]}`},
		{"unevaluatedPropsOneOfSelfRef", `{"type":"object","properties":{"a":{}},"unevaluatedProperties":false,"oneOf":[{"$ref":"#"}]}`},
		{"unevaluatedPropsIfSelfRef", `{"type":"object","properties":{"a":{}},"unevaluatedProperties":false,"if":{"$ref":"#"}}`},
		{"unevaluatedPropsDependentSchemasSelfRef", `{"type":"object","properties":{"a":{}},"unevaluatedProperties":false,"dependentSchemas":{"a":{"$ref":"#"}}}`},
		{"unevaluatedPropsAllOfSelfDef", `{"$defs":{"A":{"properties":{"a":{}},"unevaluatedProperties":false,"allOf":[{"$ref":"#/$defs/A"}]}},"$ref":"#/$defs/A"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				// The result is deliberately not asserted: what is under test
				// is that generation reaches a conclusion at all. A cycle made
				// only of $ref-only schemas constrains nothing, so succeeding
				// with an `any` alias is as acceptable as reporting an error.
				_, _ = generateJSON(t, Config{PackageName: "testpkg"}, tc.input)
			}()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatalf("generation did not terminate for %s", tc.input)
			}
		})
	}
}

// TestSelfReferentialStructStillResolvesToPointer guards the cycle fix from
// overreaching. A recursive *object* is the shape the generator is supposed to
// handle -- the struct arm claims it, and the back-reference becomes a pointer
// field. If the ref-cycle guard were to fire here instead, the type would
// collapse to `any` and every recursive schema in the corpus would lose its
// shape, which is precisely what testdata/schemas/advanced/recursive_tree.json
// and its golden exist to catch.
func TestSelfReferentialStructStillResolvesToPointer(t *testing.T) {
	input := `{"type":"object","properties":{"value":{"type":"string"},"parent":{"$ref":"#"}},"required":["value"]}`

	file, err := generateJSON(t, Config{PackageName: "testpkg"}, input)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	sd, ok := file.TypeDefs[0].(*StructDef)
	if !ok {
		t.Fatalf("root type is %T, want a *StructDef", file.TypeDefs[0])
	}
	f, ok := fieldByJSONName(sd, "parent")
	if !ok {
		t.Fatalf("no field for the self-referential property %q", "parent")
	}
	if !f.Type.IsPointer() || namedTypeName(f.Type) != sd.Name {
		t.Fatalf("self-reference typed as %s, want a pointer to %s", f.Type.GoTypeName(), sd.Name)
	}
}

// TestRefWithSiblingCycleKeepsStructShape is the other half of the sibling-cycle
// fix: terminating is necessary but not sufficient.
//
// The schema is a $ref back to the root carrying a structural sibling, so the
// property's type is the merge of the root's own shape with that sibling -- a
// struct, generated under a name of its own, whose "a" field closes the cycle
// back onto itself. Breaking the recursion by degrading either end to `any` or
// to json.RawMessage would also make generation terminate, and would silently
// throw the shape away. What is asserted is the shape that survives: a named
// struct, and a self-field that is a *pointer* to it, because Go rejects a
// struct that contains itself by value and the generated package would not
// compile.
func TestRefWithSiblingCycleKeepsStructShape(t *testing.T) {
	input := `{"type":"object","properties":{"a":{"$ref":"#","items":{"type":"string"}}}}`

	file, err := generateJSON(t, Config{PackageName: "testpkg"}, input)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	root, ok := file.TypeDefs[0].(*StructDef)
	if !ok {
		t.Fatalf("root type is %T, want a *StructDef", file.TypeDefs[0])
	}
	rootField, ok := fieldByJSONName(root, "a")
	if !ok {
		t.Fatalf("no field for property %q on the root struct", "a")
	}
	nested := namedTypeName(rootField.Type)
	if nested == "" {
		t.Fatalf("property %q typed as %s, want a reference to a named type", "a", rootField.Type.GoTypeName())
	}

	var nestedDef *StructDef
	for _, td := range file.TypeDefs {
		if sd, ok := td.(*StructDef); ok && sd.Name == nested {
			nestedDef = sd
			break
		}
	}
	if nestedDef == nil {
		t.Fatalf("property %q references %s, which is not a generated struct", "a", nested)
	}

	self, ok := fieldByJSONName(nestedDef, "a")
	if !ok {
		t.Fatalf("no field for property %q on %s; the merged shape was dropped", "a", nested)
	}
	if !self.Type.IsPointer() || namedTypeName(self.Type) != nested {
		t.Fatalf("cycle-closing field typed as %s, want a pointer to %s", self.Type.GoTypeName(), nested)
	}
}

// TestEnumConstantNamesAreUnique is the regression for generated code that was
// gofmt-clean, exited 0 and did not compile.
//
// Sanitizing an enum value is lossy in two directions at once: several values
// can reduce to one identifier ("!" and "!!" both become "X"), and a value can
// reduce onto a name the collision numbering is about to hand out ("1" becomes
// "X1", because a leading digit is not a legal identifier start). Numbering the
// first group 1..n and never checking it against the second produced
// "RootX1, RootX2, RootX1" -- "RootX1 redeclared in this block", plus a
// duplicate case in the generated Validate switch.
func TestEnumConstantNamesAreUnique(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"punctuationAndDigit", `{"type":"string","enum":["!","!!","1"]}`},
		{"mixedScriptsAndEmpty", `{"type":"string","enum":["日本","🎉","","a-b","1","true","null"]}`},
		{"allSanitizeToX", `{"type":"string","enum":["!","@","#","$"]}`},
		{"digitsOnly", `{"type":"string","enum":["1","2","1x","2x"]}`},
		// Heterogeneous values take the json.RawMessage enum path, which names
		// its constants with the same helper and had the same defect.
		{"heterogeneous", `{"enum":["!","!!","1",1,null,true,["a"],{"b":1}]}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			file, err := generateJSON(t, Config{PackageName: "testpkg"}, tc.input)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			var ed *EnumDef
			for _, td := range file.TypeDefs {
				if e, ok := td.(*EnumDef); ok {
					ed = e
					break
				}
			}
			if ed == nil {
				t.Fatalf("no EnumDef generated for %s", tc.input)
			}
			if len(ed.Values) == 0 {
				t.Fatalf("EnumDef for %s has no values", tc.input)
			}
			seen := make(map[string]any, len(ed.Values))
			for _, ev := range ed.Values {
				if prev, dup := seen[ev.Name]; dup {
					t.Errorf("constant name %q used for both %#v and %#v; the generated package would not compile",
						ev.Name, prev, ev.Value)
				}
				seen[ev.Name] = ev.Value
			}
		})
	}
}

// TestNullSubschemaMemoDoesNotWaveThroughSecondRef pins the bookkeeping of the
// null check's memo, which is what makes it affordable to re-run on every ref
// resolution.
//
// Two properties reference the same node of a fetched document, and that node
// holds a null. The first resolution walks it, finds the null and refuses the
// node. If the memo recorded nodes as it entered them rather than once their
// subtree came back clean, the second resolution would find the node already
// "checked", hand it to the generator, and the null would be dereferenced --
// turning a reported error back into the segfault this all exists to prevent.
func TestNullSubschemaMemoDoesNotWaveThroughSecondRef(t *testing.T) {
	var remote schema.Schema
	if err := json.Unmarshal([]byte(`{
		"definitions": {
			"outer": {"type": "object", "properties": {"ok": {"type": "string"}}, "allOf": [null]}
		}
	}`), &remote); err != nil {
		t.Fatal(err)
	}
	remote.Normalize()

	const docURI = "http://example.com/has-null.json"
	resolver := schema.NewMappingResolver(map[string]*schema.Schema{docURI: &remote})

	var root schema.Schema
	if err := json.Unmarshal([]byte(`{
		"type": "object",
		"properties": {
			"a": {"$ref": "`+docURI+`#/definitions/outer"},
			"b": {"$ref": "`+docURI+`#/definitions/outer"}
		}
	}`), &root); err != nil {
		t.Fatal(err)
	}
	root.Normalize()

	_, err := New(Config{PackageName: "testpkg", Resolver: resolver}).Generate(&root)
	if err == nil {
		t.Fatalf("expected an error for a null subschema in the fetched document, got nil")
	}
	if !strings.Contains(err.Error(), "allOf/0") {
		t.Fatalf("error %q does not locate the null subschema", err.Error())
	}
}

// TestNullTypedPropertyIsEnforced pins the representation of a {"type":"null"}
// property. It was *any carrying no validation rule at all: the field accepted
// every JSON value and Validate() had nothing to say about it, so a schema
// stating "this must be null" accepted an integer. It now resolves to the same
// raw-value wrapper a *named* null-only schema already got, whose Validate
// admits nothing but null.
func TestNullTypedPropertyIsEnforced(t *testing.T) {
	input := `{
		"title": "Tombstone",
		"type": "object",
		"properties": {"n": {"type":"null"}},
		"required": ["n"]
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg", OmitEmpty: true}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var tombstone *StructDef
	var wrapper *TypeOnlySchemaDef
	for _, td := range ir.TypeDefs {
		switch d := td.(type) {
		case *StructDef:
			if d.Name == "Tombstone" {
				tombstone = d
			}
		case *TypeOnlySchemaDef:
			if d.Name == "TombstoneN" {
				wrapper = d
			}
		}
	}
	if tombstone == nil {
		t.Fatalf("expected Tombstone struct")
	}
	if wrapper == nil {
		t.Fatalf("expected a TombstoneN raw-value wrapper for the null-typed property")
	}
	if len(wrapper.AllowedTypes) != 1 || wrapper.AllowedTypes[0] != "null" {
		t.Fatalf("wrapper allowed types = %#v, want [null]", wrapper.AllowedTypes)
	}

	n, ok := fieldByJSONName(tombstone, "n")
	if !ok {
		t.Fatalf("expected field for property n")
	}
	if got := n.Type.GoTypeName(); got != "TombstoneN" {
		t.Fatalf("null-typed field type = %q, want TombstoneN", got)
	}
	// The wrapper only enforces anything if the parent's Validate() calls it.
	if !hasValidatableField(tombstone.ValidatableFields, "n") {
		t.Fatalf("null-typed field is not validated by Tombstone: %#v", tombstone.ValidatableFields)
	}
}

// TestOptionalNullTypedPropertyIsOmittedWhenAbsent covers the other half of the
// same representation. The field carried a plain `json:"n"` tag — omitempty was
// suppressed for null-typed properties, because a nil *any could not say whether
// the input held a null or nothing at all — so a property the input omitted came
// back as an explicit null. The wrapper keeps the bytes it was handed, which
// tells the two apart, and ",omitzero" drops exactly the absent one.
func TestOptionalNullTypedPropertyIsOmittedWhenAbsent(t *testing.T) {
	input := `{
		"title": "Tombstone",
		"type": "object",
		"properties": {"n": {"type":"null"}}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg", OmitEmpty: true}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var tombstone *StructDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*StructDef); ok && d.Name == "Tombstone" {
			tombstone = d
		}
	}
	if tombstone == nil {
		t.Fatalf("expected Tombstone struct")
	}
	n, ok := fieldByJSONName(tombstone, "n")
	if !ok {
		t.Fatalf("expected field for property n")
	}
	if !n.OmitEmpty {
		t.Fatalf("optional null-typed field is emitted even when the input omitted it")
	}
	// ",omitzero", not ",omitempty": the wrapper is a struct, which omitempty
	// never considers empty, and its MarshalJSON writes null for an absent value.
	if !n.OmitZero {
		t.Fatalf("optional null-typed field uses ,omitempty, want ,omitzero")
	}
}

// TestNullVariantOfAOneOfStaysAPointer guards the trade the fix must not make.
// A oneOf (or a type list) naming null beside one other alternative is the
// idiomatic spelling of "nullable", and it resolves to a pointer to that
// alternative — not to the raw-value wrapper a bare {"type":"null"} now gets.
// omitempty stays suppressed there, because nil is the only thing such a field
// has to say both "absent" and "null" with.
func TestNullVariantOfAOneOfStaysAPointer(t *testing.T) {
	input := `{
		"title": "Banner",
		"type": "object",
		"properties": {
			"note": {"oneOf": [{"type":"string"}, {"type":"null"}]},
			"tone": {"type": ["string", "null"]}
		}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg", OmitEmpty: true}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var banner *StructDef
	for _, td := range ir.TypeDefs {
		switch d := td.(type) {
		case *StructDef:
			if d.Name == "Banner" {
				banner = d
			}
		case *TypeOnlySchemaDef:
			t.Fatalf("nullable-via-oneOf generated a raw-value wrapper %q", d.Name)
		}
	}
	if banner == nil {
		t.Fatalf("expected Banner struct")
	}
	for _, name := range []string{"note", "tone"} {
		f, ok := fieldByJSONName(banner, name)
		if !ok {
			t.Fatalf("expected field for property %q", name)
		}
		if got := f.Type.GoTypeName(); got != "*string" {
			t.Fatalf("%s type = %q, want *string", name, got)
		}
		if f.OmitEmpty || f.OmitZero {
			t.Fatalf("%s is dropped when nil, which loses a present null", name)
		}
	}
}

// TestNotWrapperPropertyIsValidatedByOwner covers the "not" half of a defect the
// two raw-JSON wrappers shared with nothing else: NotSchemaDef and
// DynamicSchemaDef each carry a correct Validate, but populateValidatableFields
// did not count them as validatable, so no enclosing struct ever called it. The
// constraint was live at the document root and dead everywhere else — {"a":7}
// was accepted against a property whose type forbids integers.
func TestNotWrapperPropertyIsValidatedByOwner(t *testing.T) {
	input := `{
		"title": "Gate",
		"$defs": {"NotInt": {"not": {"type": "integer"}}},
		"type": "object",
		"properties": {"a": {"$ref": "#/$defs/NotInt"}},
		"required": ["a"]
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg", OmitEmpty: true}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var gate *StructDef
	var wrapper *NotSchemaDef
	for _, td := range ir.TypeDefs {
		switch d := td.(type) {
		case *StructDef:
			if d.Name == "Gate" {
				gate = d
			}
		case *NotSchemaDef:
			if d.Name == "NotInt" {
				wrapper = d
			}
		}
	}
	if gate == nil {
		t.Fatalf("expected Gate struct")
	}
	if wrapper == nil {
		t.Fatalf("expected a NotInt wrapper for the not-only definition")
	}
	if !hasValidatableField(gate.ValidatableFields, "a") {
		t.Fatalf("the not wrapper is never validated by Gate: %#v", gate.ValidatableFields)
	}
}

// TestDynamicWrapperPropertyIsValidatedByOwner is the same defect reached
// through the other wrapper: a definition whose only keywords are applicators
// becomes a DynamicSchemaDef, and a property referencing it has to call it.
// Both the required and the optional spelling are checked — the optional one is
// where a missing zero literal would have emitted a guard instead.
func TestDynamicWrapperPropertyIsValidatedByOwner(t *testing.T) {
	input := `{
		"title": "Dial",
		"$defs": {"Window": {"oneOf": [{"type":"integer","minimum":10}, {"type":"string"}]}},
		"type": "object",
		"properties": {
			"a": {"$ref": "#/$defs/Window"},
			"b": {"$ref": "#/$defs/Window"}
		},
		"required": ["a"]
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg", OmitEmpty: true}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var dial *StructDef
	var wrapper *DynamicSchemaDef
	for _, td := range ir.TypeDefs {
		switch d := td.(type) {
		case *StructDef:
			if d.Name == "Dial" {
				dial = d
			}
		case *DynamicSchemaDef:
			if d.Name == "Window" {
				wrapper = d
			}
		}
	}
	if dial == nil {
		t.Fatalf("expected Dial struct")
	}
	if wrapper == nil {
		t.Fatalf("expected a Window wrapper for the applicator-only definition")
	}
	for _, jsonName := range []string{"a", "b"} {
		if !hasValidatableField(dial.ValidatableFields, jsonName) {
			t.Fatalf("%q: the dynamic wrapper is never validated by Dial: %#v", jsonName, dial.ValidatableFields)
		}
	}
}

// TestOptionalWrapperPropertyOmitsZeroAndGuardsNothing pins the two things that
// have to hold once the wrapper is validated at all, both of which are about the
// absent value.
//
// The zero-literal guard must be empty. A wrapper is a struct, so the `""`
// fallback in zeroLiteralForType would have the owner emit `x.A != ""` around
// the call — which does not compile. Nothing is lost by dropping it: the
// wrapper's own Validate returns nil when it holds no bytes, and an optional
// field's call is gated on _jsonKeys anyway (PresenceGuard).
//
// The tag must be ",omitzero" rather than ",omitempty". omitempty never
// considers a struct empty, and the wrapper's MarshalJSON writes null when it
// holds no bytes, so an absent optional property came back as an explicit null.
func TestOptionalWrapperPropertyOmitsZeroAndGuardsNothing(t *testing.T) {
	input := `{
		"title": "Latch",
		"$defs": {
			"NotInt": {"not": {"type": "integer"}},
			"Window": {"oneOf": [{"type":"integer","minimum":10}, {"type":"string"}]}
		},
		"type": "object",
		"properties": {
			"a": {"$ref": "#/$defs/NotInt"},
			"b": {"$ref": "#/$defs/Window"}
		}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg", OmitEmpty: true}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var latch *StructDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*StructDef); ok && d.Name == "Latch" {
			latch = d
		}
	}
	if latch == nil {
		t.Fatalf("expected Latch struct")
	}

	guards := make(map[string]ValidatableFieldDef, len(latch.ValidatableFields))
	for _, vf := range latch.ValidatableFields {
		guards[vf.JSONName] = vf
	}
	for _, jsonName := range []string{"a", "b"} {
		f, ok := fieldByJSONName(latch, jsonName)
		if !ok {
			t.Fatalf("expected field for property %q", jsonName)
		}
		if !f.OmitZero {
			t.Fatalf("%q: optional wrapper field uses ,omitempty, want ,omitzero (absent comes back as null)", jsonName)
		}
		vf, ok := guards[jsonName]
		if !ok {
			t.Fatalf("%q: expected among ValidatableFields", jsonName)
		}
		if vf.ZeroLiteral != "" {
			t.Fatalf("%q: ZeroLiteral = %q, want \"\" (a struct has no zero literal to compare against)", jsonName, vf.ZeroLiteral)
		}
	}
}

// TestRefToDynamicWrapperGeneratesTheWrapperNotAnAlias covers the same dead
// constraint one position over. A $ref whose target is a wrapper struct cannot
// become `type Root Target`: a defined type over a struct inherits no methods,
// so Root would carry neither the UnmarshalJSON that fills the raw value nor the
// Validate that checks it, and emitted an empty Validate instead. The other
// wrappers were already excluded from that path; DynamicSchemaDef was not.
func TestRefToDynamicWrapperGeneratesTheWrapperNotAnAlias(t *testing.T) {
	input := `{
		"$defs": {"Window": {"oneOf": [{"type":"integer","minimum":10}, {"type":"string"}]}},
		"$ref": "#/$defs/Window"
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var root *DynamicSchemaDef
	for _, td := range ir.TypeDefs {
		switch d := td.(type) {
		case *AliasDef:
			if d.Name == "Root" {
				t.Fatalf("Root is an alias over the wrapper, so it inherits no Validate")
			}
		case *DynamicSchemaDef:
			if d.Name == "Root" {
				root = d
			}
		}
	}
	if root == nil {
		t.Fatalf("expected Root to be generated as a DynamicSchemaDef")
	}
	if len(root.OneOf) != 2 {
		t.Fatalf("Root oneOf branches = %d, want 2", len(root.OneOf))
	}
}

func hasValidatableField(fields []ValidatableFieldDef, jsonName string) bool {
	for _, f := range fields {
		if f.JSONName == jsonName {
			return true
		}
	}
	return false
}

// generateForItemTest is the shape the item-validation regressions below share:
// unmarshal, normalize, generate, and hand back the IR.
func generateForItemTest(t *testing.T, input string) *File {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg", OmitEmpty: true}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return ir
}

func structNamed(t *testing.T, ir *File, name string) *StructDef {
	t.Helper()
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*StructDef); ok && d.Name == name {
			return d
		}
	}
	t.Fatalf("expected a %s struct; got %v", name, ir.TypeDefs)
	return nil
}

func itemValidationFor(t *testing.T, sd *StructDef, jsonName string) *ItemValidationDef {
	t.Helper()
	for i := range sd.ItemValidations {
		if sd.ItemValidations[i].JSONName == jsonName {
			return &sd.ItemValidations[i]
		}
	}
	t.Fatalf("expected per-element checks for %q on %s; got %+v", jsonName, sd.Name, sd.ItemValidations)
	return nil
}

func itemRuleTypes(def *ItemValidationDef, level int) []string {
	var out []string
	for _, rule := range def.Levels[level].Rules {
		out = append(out, rule.RuleType)
	}
	return out
}

// TestPrimitiveArrayItemConstraintsAreChecked pins the fix for element
// constraints being dropped whenever the element's Go type is not a named type
// carrying its own Validate. {"items":{"type":"string","minLength":2}} emits
// []string, and before this the emitted Validate was `return nil` -- an
// instance of {"a":["x"]} was accepted.
func TestPrimitiveArrayItemConstraintsAreChecked(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type":"array", "items":{"type":"string", "minLength":2, "maxLength":4}},
			"b": {"type":"array", "items":{"type":"integer", "minimum":3, "multipleOf":2}},
			"c": {"type":"array", "items":{"type":"number", "exclusiveMinimum":1, "exclusiveMaximum":9}}
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	for _, tc := range []struct {
		jsonName string
		want     []string
	}{
		{"a", []string{"minLength", "maxLength"}},
		{"b", []string{"minimum", "multipleOf"}},
		{"c", []string{"exclusiveMinimum", "exclusiveMaximum"}},
	} {
		def := itemValidationFor(t, doc, tc.jsonName)
		if len(def.Levels) != 1 {
			t.Fatalf("%q: %d levels, want 1", tc.jsonName, len(def.Levels))
		}
		got := itemRuleTypes(def, 0)
		for _, want := range tc.want {
			if !containsString(got, want) {
				t.Fatalf("%q: element rules %v are missing %q", tc.jsonName, got, want)
			}
		}
	}
}

// TestConstInItemPositionGetsANamedType pins the second half of the same
// defect: a const in item position was dropped entirely, because the promotion
// of a type-less const to a single-member enum only ran on the property path.
// {"items":{"const":5}} emitted []any and a Validate of `return nil`, while the
// identical {"items":{"enum":[5]}} emitted a named element type that worked.
func TestConstInItemPositionGetsANamedType(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type":"array", "items":{"const":5}}
		}
	}`)

	var elemEnum *EnumDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*EnumDef); ok && d.Name == "DocAItem" {
			elemEnum = d
		}
	}
	if elemEnum == nil {
		t.Fatalf("expected a DocAItem enum for the const items; got %v", ir.TypeDefs)
	}
	if len(elemEnum.Values) != 1 {
		t.Fatalf("DocAItem has %d values, want the single const", len(elemEnum.Values))
	}

	doc := structNamed(t, ir, "Doc")
	var field *FieldDef
	for i := range doc.Fields {
		if doc.Fields[i].JSONName == "a" {
			field = &doc.Fields[i]
		}
	}
	if field == nil {
		t.Fatalf("expected field a on Doc")
	}
	if got := field.Type.GoTypeName(); got != "[]DocAItem" {
		t.Fatalf("a type = %q, want []DocAItem (not []any)", got)
	}

	// The named element type is dispatched to by ValidatableFields, so the
	// per-element checks must stay out of it or every element validates twice.
	var validatable *ValidatableFieldDef
	for i := range doc.ValidatableFields {
		if doc.ValidatableFields[i].JSONName == "a" {
			validatable = &doc.ValidatableFields[i]
		}
	}
	if validatable == nil || !validatable.IsSlice {
		t.Fatalf("expected a to be a validatable slice field; got %+v", doc.ValidatableFields)
	}
	if len(doc.ItemValidations) != 0 {
		t.Fatalf("named element type must not also carry per-element checks; got %+v", doc.ItemValidations)
	}
}

// TestNamedElementTypeIsNotValidatedTwice guards the same no-double-dispatch
// rule for the shapes that always worked, so a later change cannot start
// stacking a second pass on top of the element type's own Validate.
func TestNamedElementTypeIsNotValidatedTwice(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type":"array", "items":{"enum":["red","green"]}},
			"b": {"type":"array", "items":{"type":"object", "properties":{"x":{"type":"string","minLength":2}}}}
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	if len(doc.ItemValidations) != 0 {
		t.Fatalf("elements with their own Validate must carry no per-element checks; got %+v", doc.ItemValidations)
	}
	if len(doc.ValidatableFields) != 2 {
		t.Fatalf("expected both fields to dispatch through ValidatableFields; got %+v", doc.ValidatableFields)
	}
}

// TestNestedArrayItemConstraintsDescend covers the dimension the flat case
// cannot: [][]int64 has no named type at either depth, so the constraint on the
// inner element is only reachable through a nested loop.
func TestNestedArrayItemConstraintsDescend(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type":"array", "items":{"type":"array", "maxItems":2, "items":{"type":"integer","minimum":3}}}
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	def := itemValidationFor(t, doc, "a")
	if len(def.Levels) != 2 {
		t.Fatalf("%d levels, want 2 for a [][]int64 field: %+v", len(def.Levels), def.Levels)
	}
	if got := itemRuleTypes(def, 0); !containsString(got, "maxItems") {
		t.Fatalf("outer element rules %v are missing maxItems", got)
	}
	if got := itemRuleTypes(def, 1); !containsString(got, "minimum") {
		t.Fatalf("inner element rules %v are missing minimum", got)
	}
	if def.Levels[0].IndexVar == def.Levels[1].IndexVar || def.Levels[0].ElemVar == def.Levels[1].ElemVar {
		t.Fatalf("nested loops share variable names: %+v", def.Levels)
	}
}

// TestArrayAliasValidatesItsElements covers the same defect where the array is
// a definition of its own: `type T []string` had a Validate that returned nil,
// dropping both the element constraints and, for a named element type, the
// element's own Validate. An alias has no struct field for ValidatableFields to
// reach, so its outermost element is this pass's job.
func TestArrayAliasValidatesItsElements(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"$defs": {
			"Names": {"type":"array", "items":{"type":"string", "minLength":2}},
			"Rows": {"type":"array", "items":{"type":"object", "properties":{"x":{"type":"string"}}}}
		},
		"type": "object",
		"properties": {
			"a": {"$ref": "#/$defs/Names"},
			"b": {"$ref": "#/$defs/Rows"}
		}
	}`)

	aliases := map[string]*AliasDef{}
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*AliasDef); ok {
			aliases[d.Name] = d
		}
	}

	names := aliases["Names"]
	if names == nil {
		t.Fatalf("expected a Names alias; got %v", ir.TypeDefs)
	}
	if len(names.ItemValidations) != 1 || len(names.ItemValidations[0].Levels) != 1 {
		t.Fatalf("Names carries no per-element checks: %+v", names.ItemValidations)
	}
	if got := itemRuleTypes(&names.ItemValidations[0], 0); !containsString(got, "minLength") {
		t.Fatalf("Names element rules %v are missing minLength", got)
	}
	if names.ItemValidations[0].FieldName != "" {
		t.Fatalf("an alias validates its receiver, not a field: %+v", names.ItemValidations[0])
	}

	rows := aliases["Rows"]
	if rows == nil {
		t.Fatalf("expected a Rows alias; got %v", ir.TypeDefs)
	}
	if len(rows.ItemValidations) != 1 || !rows.ItemValidations[0].Levels[0].CallValidate {
		t.Fatalf("Rows does not dispatch to its element type's Validate: %+v", rows.ItemValidations)
	}
}

// TestTypedAdditionalPropertiesKeepsItsValueType pins issue #84. An object whose
// whole shape is `additionalProperties` is a Go map, and the sub-schema says
// what its values are. The map arm used to keep the value type only when it had
// been materialized into a *named* type, on the reasoning that a bare value type
// carries no Validate to dispatch to -- so
// {"type":"object","additionalProperties":{"type":"string","minLength":3}} came
// out map[string]any, which is both a weaker Go type and, because the value
// schema was dropped with it, a document {"m":{"a":"x"}} the schema forbids and
// the generated Validate accepted.
//
// Both halves are asserted here: the Go type, and the per-value checks that
// answer the reasoning the arm used to give. The checks ride the same
// ItemValidationDef machinery as a []string's elements, so a nested map or a
// map of slices descends exactly as [][]string does.
func TestTypedAdditionalPropertiesKeepsItsValueType(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"strs":   {"type":"object", "additionalProperties":{"type":"string", "minLength":3}},
			"ints":   {"type":"object", "additionalProperties":{"type":"integer", "minimum":5}},
			"lists":  {"type":"object", "additionalProperties":{"type":"array", "maxItems":2, "items":{"type":"string", "maxLength":4}}},
			"nested": {"type":"object", "additionalProperties":{"type":"object", "additionalProperties":{"type":"integer", "maximum":9}}}
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	for jsonName, want := range map[string]string{
		"strs":   "map[string]string",
		"ints":   "map[string]int64",
		"lists":  "map[string][]string",
		"nested": "map[string]map[string]int64",
	} {
		field := fieldNamedJSON(t, doc, jsonName)
		if got := field.Type.GoTypeName(); got != want {
			t.Fatalf("%s type = %q, want %q -- the value schema names the type, and map[string]any drops it", jsonName, got, want)
		}
	}

	// A map level is addressed by key, not by index. The emitter reads IsMap to
	// pick the error-path verb, so a level that forgot it would format a string
	// key with %d.
	for jsonName, wantRule := range map[string]string{"strs": "minLength", "ints": "minimum"} {
		def := itemValidationFor(t, doc, jsonName)
		if len(def.Levels) != 1 {
			t.Fatalf("%s: %d levels, want 1: %+v", jsonName, len(def.Levels), def.Levels)
		}
		if !def.Levels[0].IsMap {
			t.Fatalf("%s: level 0 is not marked as a map; its error path would index a string key with %%d", jsonName)
		}
		if got := itemRuleTypes(def, 0); !containsString(got, wantRule) {
			t.Fatalf("%s value rules %v are missing %s -- nothing enforces it on the map's values", jsonName, got, wantRule)
		}
	}

	// map[string][]string: the map level, then the slice level beneath it.
	lists := itemValidationFor(t, doc, "lists")
	if len(lists.Levels) != 2 {
		t.Fatalf("lists: %d levels, want 2 for map[string][]string: %+v", len(lists.Levels), lists.Levels)
	}
	if !lists.Levels[0].IsMap || lists.Levels[1].IsMap {
		t.Fatalf("lists: level kinds = map:%v map:%v, want map then slice", lists.Levels[0].IsMap, lists.Levels[1].IsMap)
	}
	if got := itemRuleTypes(lists, 0); !containsString(got, "maxItems") {
		t.Fatalf("lists value rules %v are missing maxItems", got)
	}
	if got := itemRuleTypes(lists, 1); !containsString(got, "maxLength") {
		t.Fatalf("lists element rules %v are missing maxLength", got)
	}

	// map[string]map[string]int64: both levels are maps, and the inner one
	// carries the keyword.
	nested := itemValidationFor(t, doc, "nested")
	if len(nested.Levels) != 2 {
		t.Fatalf("nested: %d levels, want 2: %+v", len(nested.Levels), nested.Levels)
	}
	if !nested.Levels[0].IsMap || !nested.Levels[1].IsMap {
		t.Fatalf("nested: level kinds = map:%v map:%v, want two maps", nested.Levels[0].IsMap, nested.Levels[1].IsMap)
	}
	if got := itemRuleTypes(nested, 1); !containsString(got, "maximum") {
		t.Fatalf("nested inner value rules %v are missing maximum", got)
	}
	for _, def := range []*ItemValidationDef{lists, nested} {
		if def.Levels[0].IndexVar == def.Levels[1].IndexVar || def.Levels[0].ElemVar == def.Levels[1].ElemVar {
			t.Fatalf("nested loops share variable names: %+v", def.Levels)
		}
	}
}

// TestNamedAdditionalPropertiesValueStillDispatches guards the half of the arm
// that already worked. A value schema that does materialize into a named type
// keeps that type, and the owner reaches it through ValidatableFields rather
// than through a per-value rule -- emitting both would validate every value
// twice, and emitting a numeric rule against a struct would not compile.
func TestNamedAdditionalPropertiesValueStillDispatches(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"m": {"type":"object", "additionalProperties":{"type":"object", "properties":{"x":{"type":"string","minLength":2}}}}
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	field := fieldNamedJSON(t, doc, "m")
	if got := field.Type.GoTypeName(); got != "map[string]DocMValue" {
		t.Fatalf("m type = %q, want map[string]DocMValue", got)
	}
	if !hasValidatableField(doc.ValidatableFields, "m") {
		t.Fatalf("m is never validated by Doc: %+v", doc.ValidatableFields)
	}
	for i := range doc.ItemValidations {
		if doc.ItemValidations[i].JSONName == "m" {
			t.Fatalf("m carries per-value checks as well as a Validate dispatch: %+v -- every value would be checked twice", doc.ItemValidations[i])
		}
	}
}

// TestOnlyAWholeAdditionalPropertiesObjectBecomesAMap guards the blast radius of
// the arm above, which now keeps every value type rather than only named ones
// and so has more to over-reach with. `additionalProperties` describes a Go map
// only where it describes the *whole* object: beside `properties` or
// `patternProperties` it speaks about the keys those do not claim, and typing
// the property as a map would throw the declared ones away. A boolean
// `additionalProperties` is a verdict on unknown keys, not a description of
// them, and names no value type at all.
//
// The patternProperties case is the one mapValueSchema itself holds, and it is a
// real loss if dropped: `{"^a":{"type":"string"}}` beside
// `additionalProperties:{"type":"integer"}` would become map[string]int64, into
// which a matching key's string value cannot decode at all. The `properties`
// case is held by the arm ahead of this one, which materializes an object with
// declared properties into a struct before the map arm is ever reached -- pinned
// here because moving the map arm ahead of it would now silently retype every
// such property, which it could not before. The `additionalProperties: true`
// and bare-object cases come out map[string]any either way; they are pinned
// because that is the documented answer for a keyword that names no value type,
// not because a widened predicate would visibly change them today.
//
// `withPatt` used to be pinned at map[string]any, and that pin was wrong. It
// asked mapValueSchema the right question -- this is not a Go map -- and then
// answered the wrong one: what such a property *is* instead. Being no map does
// not make it `any`; it makes it the struct generateTypeDef has always built for
// an object that names its keys, with the pattern bucket and its sub-schema
// checks on it. The old pin recorded resolveType materializing a struct only for
// hasProperties, so the property came out map[string]any, the pattern was never
// matched, its value constraints were never checked and the sibling
// additionalProperties was dropped as well -- issue #96, with the same schema
// enforced correctly at a document root and behind a $ref. It now names the
// struct, which is the API change #96 records; the map-arm blast radius this
// test exists to guard is unchanged, and the two cases below still say so.
//
// `boolFalse` moved for a related reason of its own. With no properties
// declared, `additionalProperties: false` permits no key at all, so only {}
// satisfies it -- and the same schema in a $defs entry has always rejected
// {"x":1}, through the Forbidden overflow map generatePropertylessObjectDef
// emits. Pinning map[string]any here recorded the inline position failing to
// reach that struct, which made the answer depend on where the schema was
// written. It is a struct now in both. `boolTrue` is untouched: it permits every
// key and constrains none, so there is nothing for a type to carry.
func TestOnlyAWholeAdditionalPropertiesObjectBecomesAMap(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"withProps":  {"type":"object", "properties":{"x":{"type":"string"}}, "additionalProperties":{"type":"integer"}},
			"withPatt":   {"type":"object", "patternProperties":{"^a":{"type":"string"}}, "additionalProperties":{"type":"integer"}},
			"boolTrue":   {"type":"object", "additionalProperties":true},
			"boolFalse":  {"type":"object", "additionalProperties":false},
			"plainObj":   {"type":"object"}
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	for jsonName, want := range map[string]string{
		"withProps": "*DocWithProps",
		"withPatt":  "*DocWithPatt",
		"boolTrue":  "map[string]any",
		"boolFalse": "*DocBoolFalse",
		"plainObj":  "map[string]any",
	} {
		field := fieldNamedJSON(t, doc, jsonName)
		if got := field.Type.GoTypeName(); got != want {
			t.Fatalf("%s type = %q, want %q -- additionalProperties only types the whole object where it governs the whole object", jsonName, got, want)
		}
	}
	for i := range doc.ItemValidations {
		switch doc.ItemValidations[i].JSONName {
		case "withProps", "withPatt", "boolTrue", "boolFalse", "plainObj":
			t.Fatalf("%s carries per-value checks: %+v -- no single schema governs its values", doc.ItemValidations[i].JSONName, doc.ItemValidations[i])
		}
	}
	// The struct is only worth naming if Doc actually calls into it, and only
	// worth building if it carries what the bare map dropped: the pattern, the
	// string type its values must have, and the sibling overflow map typed by
	// additionalProperties.
	if !hasValidatableField(doc.ValidatableFields, "withPatt") {
		t.Fatalf("withPatt is never validated by Doc: %+v -- naming the struct achieves nothing if Validate does not descend into it", doc.ValidatableFields)
	}
	withPatt := structNamed(t, ir, "DocWithPatt")
	if len(withPatt.PatternProperties) != 1 || withPatt.PatternProperties[0].Pattern != "^a" {
		t.Fatalf("DocWithPatt patternProperties = %+v, want the single ^a bucket", withPatt.PatternProperties)
	}
	// `{"type":"string"}` is a sub-schema the in-place scalar rules say
	// everything about, so it keeps them and mints no type: patternRulesCoverSchema
	// is what holds the blast radius of the materialization down to the buckets
	// that need it.
	if pp := withPatt.PatternProperties[0]; pp.TypeName != "" || !containsString(ruleTypesOf(pp.Validations), "ppType") {
		t.Fatalf("DocWithPatt ^a bucket = %+v, want the in-place ppType rule and no minted type", pp)
	}
	if withPatt.AdditionalProperties == nil || withPatt.AdditionalProperties.ValueType.GoTypeName() != "int64" {
		t.Fatalf("DocWithPatt additionalProperties = %+v, want an int64-valued overflow map -- the sibling keyword was dropped with the struct", withPatt.AdditionalProperties)
	}
	// The same two questions for the key-forbidding struct: Doc has to descend
	// into it, and it has to carry the rejection that makes it worth having.
	if !hasValidatableField(doc.ValidatableFields, "boolFalse") {
		t.Fatalf("boolFalse is never validated by Doc: %+v", doc.ValidatableFields)
	}
	boolFalse := structNamed(t, ir, "DocBoolFalse")
	if boolFalse.AdditionalProperties == nil || !boolFalse.AdditionalProperties.Forbidden {
		t.Fatalf("DocBoolFalse additionalProperties = %+v, want a Forbidden overflow map -- nothing else rejects a key the schema permits none of", boolFalse.AdditionalProperties)
	}
}

// TestNullableTypedAdditionalPropertiesKeepsItsValueType pins issue #91, the
// nullable spelling of #84. resolveType's nullable arm had a branch for an
// array and none for a map, so a ["object","null"] whose whole shape is
// `additionalProperties` never reached the typed-map path at all: it fell
// through to PrimitiveTypeFromSchema("object") and came out *map[string]any,
// with the value schema -- and every keyword under it -- discarded on the way.
//
// The assertions are the same two #84 makes, because the defect is the same
// defect: the Go type the value schema names, and the per-value checks that
// enforce it. Nesting is included because the descent reaches the inner level
// through containerElemSchema, which recomputes the primary type from the
// sub-schema rather than inheriting the outer one.
func TestNullableTypedAdditionalPropertiesKeepsItsValueType(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"strs":   {"type":["object","null"], "additionalProperties":{"type":"string", "minLength":3}},
			"ints":   {"type":["object","null"], "additionalProperties":{"type":"integer", "minimum":5}},
			"lists":  {"type":["object","null"], "additionalProperties":{"type":"array", "maxItems":2, "items":{"type":"string", "maxLength":4}}},
			"nested": {"type":["object","null"], "additionalProperties":{"type":["object","null"], "additionalProperties":{"type":"integer", "maximum":9}}}
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	for jsonName, want := range map[string]string{
		"strs":   "map[string]string",
		"ints":   "map[string]int64",
		"lists":  "map[string][]string",
		"nested": "map[string]map[string]int64",
	} {
		field := fieldNamedJSON(t, doc, jsonName)
		if got := field.Type.GoTypeName(); got != want {
			t.Fatalf("%s type = %q, want %q -- a null alongside the object does not stop the value schema naming the type", jsonName, got, want)
		}
	}

	for jsonName, wantRule := range map[string]string{"strs": "minLength", "ints": "minimum"} {
		def := itemValidationFor(t, doc, jsonName)
		if len(def.Levels) != 1 {
			t.Fatalf("%s: %d levels, want 1: %+v", jsonName, len(def.Levels), def.Levels)
		}
		if !def.Levels[0].IsMap {
			t.Fatalf("%s: level 0 is not marked as a map; its error path would index a string key with %%d", jsonName)
		}
		if got := itemRuleTypes(def, 0); !containsString(got, wantRule) {
			t.Fatalf("%s value rules %v are missing %s -- nothing enforces it on the map's values", jsonName, got, wantRule)
		}
	}

	lists := itemValidationFor(t, doc, "lists")
	if len(lists.Levels) != 2 {
		t.Fatalf("lists: %d levels, want 2 for map[string][]string: %+v", len(lists.Levels), lists.Levels)
	}
	if !lists.Levels[0].IsMap || lists.Levels[1].IsMap {
		t.Fatalf("lists: level kinds = map:%v map:%v, want map then slice", lists.Levels[0].IsMap, lists.Levels[1].IsMap)
	}
	if got := itemRuleTypes(lists, 1); !containsString(got, "maxLength") {
		t.Fatalf("lists element rules %v are missing maxLength", got)
	}

	// A nullable map of nullable maps: both levels have to be recognised, which
	// only happens if the predicate the descent consults reads the sub-schema's
	// own type list rather than the outer object's.
	nested := itemValidationFor(t, doc, "nested")
	if len(nested.Levels) != 2 {
		t.Fatalf("nested: %d levels, want 2: %+v", len(nested.Levels), nested.Levels)
	}
	if !nested.Levels[0].IsMap || !nested.Levels[1].IsMap {
		t.Fatalf("nested: level kinds = map:%v map:%v, want two maps", nested.Levels[0].IsMap, nested.Levels[1].IsMap)
	}
	if got := itemRuleTypes(nested, 1); !containsString(got, "maximum") {
		t.Fatalf("nested inner value rules %v are missing maximum", got)
	}
}

// TestNullableTypedMapKeepsTheNullContract pins the round-trip decision the fix
// had to make, which is why #84 stopped short of the nullable case.
//
// The map is bare -- no outer pointer -- on the precedent the nullable *array*
// branch beside it already set. That is not a loss of state: a nil pointer to a
// map and a pointer to a nil map both marshal to `null`, so *map[string]T never
// distinguished a null from an absent property either. What decides whether
// `null` survives is the tag, and the tag is chosen from the schema (omitempty
// is suppressed for any property whose type list admits null), not from the Go
// type -- so it must stay suppressed now that the type is a map, which is
// exactly what omitzero would undo by dropping a nil map on the way out.
//
// So the contract is: a nil map is a JSON null and is written back as one; a
// present {} decodes to a non-nil empty map and is written back as {}; and an
// absent property is written back as null, which is what the property did
// before this change and what every other nullable property does.
func TestNullableTypedMapKeepsTheNullContract(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"m": {"type":["object","null"], "additionalProperties":{"type":"string", "minLength":3}}
		}
	}`)

	field := fieldNamedJSON(t, structNamed(t, ir, "Doc"), "m")
	if field.Type.IsPointer() {
		t.Fatalf("m type = %q, want an unwrapped map -- a pointer to a map carries no state a nil map does not", field.Type.GoTypeName())
	}
	if field.OmitEmpty || field.OmitZero {
		t.Fatalf("m has omitempty=%v omitzero=%v; either one drops a nil map, turning an explicit null into an absent property", field.OmitEmpty, field.OmitZero)
	}
}

// TestOnlyAWholeNullableAdditionalPropertiesObjectBecomesAMap guards the blast
// radius of the arm above, on the same terms as its non-nullable twin: the map
// branch sits ahead of the fallback in the *nullable* arm, so widening it would
// retype every ["object","null"] property in the corpus.
//
// A nullable object with declared properties is held by the branch before it
// and stays a pointer to a named struct; `additionalProperties: true` and the
// bare object name no value type and constrain nothing, so each keeps the
// *map[string]any the fallback answers. The named-value case is the other half:
// a value schema that materializes into a type of its own keeps that type and is
// reached through ValidatableFields, not through per-value rules, so its values
// are not checked twice.
//
// `withPatt` and `inItems` were pinned at *map[string]any and []*map[string]any,
// and both pins were wrong for the reason the non-nullable twin gives: an object
// that is not a Go map is not therefore `any`, it is the struct that names its
// keys. The nullable arm decided this with hasProperties, so a ["object","null"]
// whose shape is patternProperties fell past it to the fallback and validated
// nothing -- issue #96 in its nullable spelling, and in the array-element
// position that reaches the arm through resolveType alone. Both now name the
// struct, behind the pointer the nullable arm has always used for one.
//
// `boolFalse` moved with them, and for the reason the non-nullable twin records:
// with no key named, `additionalProperties: false` permits none, so only {} (or
// a null) satisfies it -- which the same schema behind a $ref has always
// enforced and which the inline position dropped.
func TestOnlyAWholeNullableAdditionalPropertiesObjectBecomesAMap(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"withProps": {"type":["object","null"], "properties":{"x":{"type":"string"}}, "additionalProperties":{"type":"integer"}},
			"withPatt":  {"type":["object","null"], "patternProperties":{"^a":{"type":"string"}}, "additionalProperties":{"type":"integer"}},
			"boolTrue":  {"type":["object","null"], "additionalProperties":true},
			"boolFalse": {"type":["object","null"], "additionalProperties":false},
			"plainObj":  {"type":["object","null"]},
			"namedVal":  {"type":["object","null"], "additionalProperties":{"type":"object", "properties":{"x":{"type":"string","minLength":2}}}},
			"inItems":   {"type":"array", "items":{"type":["object","null"], "patternProperties":{"^a":{"type":"string"}}, "additionalProperties":{"type":"integer"}}}
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	for jsonName, want := range map[string]string{
		"withProps": "*DocWithProps",
		"withPatt":  "*DocWithPatt",
		"boolTrue":  "*map[string]any",
		"boolFalse": "*DocBoolFalse",
		"plainObj":  "*map[string]any",
		"namedVal":  "map[string]DocNamedValValue",
		// An array element reaches the nullable arm through resolveType alone,
		// without passing resolvePropertyType's copy of the same decision. Both
		// consult the one predicate, and this is the position that says so.
		"inItems": "[]*DocInItemsItem",
	} {
		field := fieldNamedJSON(t, doc, jsonName)
		if got := field.Type.GoTypeName(); got != want {
			t.Fatalf("%s type = %q, want %q -- additionalProperties only types the whole object where it governs the whole object", jsonName, got, want)
		}
	}
	for i := range doc.ItemValidations {
		switch doc.ItemValidations[i].JSONName {
		case "withProps", "withPatt", "boolTrue", "boolFalse", "plainObj", "namedVal":
			t.Fatalf("%s carries per-value checks: %+v -- no bare value schema governs its values", doc.ItemValidations[i].JSONName, doc.ItemValidations[i])
		}
	}
	if !hasValidatableField(doc.ValidatableFields, "namedVal") {
		t.Fatalf("namedVal is never validated by Doc: %+v -- a named value type answers for its own schema", doc.ValidatableFields)
	}
	for _, jsonName := range []string{"withPatt", "inItems", "boolFalse"} {
		if !hasValidatableField(doc.ValidatableFields, jsonName) {
			t.Fatalf("%s is never validated by Doc: %+v -- naming the struct achieves nothing if Validate does not descend into it", jsonName, doc.ValidatableFields)
		}
	}
	if bf := structNamed(t, ir, "DocBoolFalse"); bf.AdditionalProperties == nil || !bf.AdditionalProperties.Forbidden {
		t.Fatalf("DocBoolFalse additionalProperties = %+v, want a Forbidden overflow map -- nothing else rejects a key the schema permits none of", bf.AdditionalProperties)
	}
	for _, name := range []string{"DocWithPatt", "DocInItemsItem"} {
		sd := structNamed(t, ir, name)
		if len(sd.PatternProperties) != 1 || sd.PatternProperties[0].Pattern != "^a" {
			t.Fatalf("%s patternProperties = %+v, want the single ^a bucket", name, sd.PatternProperties)
		}
		if sd.AdditionalProperties == nil || sd.AdditionalProperties.ValueType.GoTypeName() != "int64" {
			t.Fatalf("%s additionalProperties = %+v, want an int64-valued overflow map -- the sibling keyword was dropped with the struct", name, sd.AdditionalProperties)
		}
	}
}

// ruleTypesOf lists the RuleType of each rule, for asserting on a bucket of
// checks without pinning the values beside them.
func ruleTypesOf(rules []ValidationRule) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.RuleType)
	}
	return out
}

// fieldNamedJSON returns the struct field carrying a JSON property name.
func fieldNamedJSON(t *testing.T, sd *StructDef, jsonName string) *FieldDef {
	t.Helper()
	for i := range sd.Fields {
		if sd.Fields[i].JSONName == jsonName {
			return &sd.Fields[i]
		}
	}
	t.Fatalf("expected a field for %q on %s; got %+v", jsonName, sd.Name, sd.Fields)
	return nil
}

// fieldRuleTypes lists the rule types a struct's Validate would check against
// one property.
func fieldRuleTypes(sd *StructDef, jsonName string) []string {
	var out []string
	for _, rule := range sd.Validations {
		if rule.JSONName == jsonName {
			out = append(out, rule.RuleType)
		}
	}
	return out
}

// TestInlineNotPropertyGetsAWrapperType pins one half of the defect where a
// property whose own schema is a bare `not` was dropped before any type was
// chosen. resolveType has no arm for `not`, so the field came out `any`, every
// rule extracted for it was filtered away as uncompilable against `any`, and
// {"a":7} was accepted against {"properties":{"a":{"not":{"type":"integer"}}}}.
//
// The wrapper existing is what this test pins. Whether the enclosing struct
// calls its Validate is decided by localTypeIsValidatable, which is a separate
// defect with its own fix.
func TestInlineNotPropertyGetsAWrapperType(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"not": {"type": "integer"}}
		},
		"required": ["a"]
	}`)

	var wrapper *NotSchemaDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*NotSchemaDef); ok && d.Name == "DocA" {
			wrapper = d
		}
	}
	if wrapper == nil {
		t.Fatalf("expected a DocA not-wrapper for the inline not; got %v", ir.TypeDefs)
	}
	if !containsString(wrapper.NotTypes, "integer") {
		t.Fatalf("DocA forbids %v, want integer", wrapper.NotTypes)
	}

	doc := structNamed(t, ir, "Doc")
	field := fieldNamedJSON(t, doc, "a")
	if got := field.Type.GoTypeName(); got != "DocA" {
		t.Fatalf("a type = %q, want DocA (not any)", got)
	}
}

// TestInlineIfThenElsePropertyGetsAWrapperType is the same defect for the
// conditional keywords: {"properties":{"a":{"if":...,"then":...}}} typed the
// field `any` and accepted {"a":99} against an if/then that forbids it.
func TestInlineIfThenElsePropertyGetsAWrapperType(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"if": {"minimum": 10}, "then": {"maximum": 20}}
		}
	}`)

	var wrapper *DynamicSchemaDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*DynamicSchemaDef); ok && d.Name == "DocA" {
			wrapper = d
		}
	}
	if wrapper == nil {
		t.Fatalf("expected a DocA dynamic wrapper for the inline if/then; got %v", ir.TypeDefs)
	}
	if !wrapper.HasIfThenElse || !wrapper.HasThen {
		t.Fatalf("DocA carries no if/then: %+v", wrapper)
	}

	doc := structNamed(t, ir, "Doc")
	field := fieldNamedJSON(t, doc, "a")
	if got := field.Type.GoTypeName(); got != "DocA" {
		t.Fatalf("a type = %q, want DocA (not any)", got)
	}
	// A raw-JSON wrapper is a struct, so omitempty never drops it and its
	// MarshalJSON writes null for an absent value. Only omitzero omits it, and
	// without that an absent optional property stops round-tripping.
	if !field.OmitZero {
		t.Fatalf("a must be tagged omitzero; got %+v", field)
	}
}

// TestInlineWrapperIsNotTakenForATypedProperty guards the narrowness of the
// arm above. A `not` beside a declared type has a Go type of its own and a path
// that produces it, and hijacking that would lose the type.
func TestInlineWrapperIsNotTakenForATypedProperty(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type": "string", "not": {"const": "x"}}
		},
		"required": ["a"]
	}`)

	doc := structNamed(t, ir, "Doc")
	field := fieldNamedJSON(t, doc, "a")
	if got := field.Type.GoTypeName(); got != "string" {
		t.Fatalf("a type = %q, want string", got)
	}
}

// TestEmptyNotPropertyLeavesNoForbiddenFieldRule pins the interaction that made
// the whole external suite stop compiling. {"properties":{"foo":{"not":{}}}}
// forbids the property outright, and extractValidationRules answers it with a
// "forbidden" rule emitted as `field != nil`. Once the property became a
// wrapper struct that guard stopped compiling -- `r.Foo != nil` against a
// non-nilable type -- in 23 groups across every draft.
//
// The wrapper is the right home for the constraint, so the rule goes rather
// than the wrapper: NotSchemaDef{IsForbidden} rejects every value it is handed
// and accepts an absent one, which is what the schema says, and it is stricter
// than the guard it replaces -- `!= nil` let a present JSON null through.
func TestEmptyNotPropertyLeavesNoForbiddenFieldRule(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"properties": {"foo": {"not": {}}}
	}`)

	var wrapper *NotSchemaDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*NotSchemaDef); ok && d.Name == "DocFoo" {
			wrapper = d
		}
	}
	if wrapper == nil || !wrapper.IsForbidden {
		t.Fatalf("expected a forbidding DocFoo wrapper; got %v", ir.TypeDefs)
	}

	doc := structNamed(t, ir, "Doc")
	if got := fieldRuleTypes(doc, "foo"); containsString(got, "forbidden") {
		t.Fatalf("field rules for foo = %v: a `!= nil` guard does not compile against a wrapper struct", got)
	}
	// The wrapper only enforces anything if its owner calls it.
	var validatable *ValidatableFieldDef
	for i := range doc.ValidatableFields {
		if doc.ValidatableFields[i].JSONName == "foo" {
			validatable = &doc.ValidatableFields[i]
		}
	}
	if validatable == nil {
		t.Fatalf("expected foo to be a validatable field; got %+v", doc.ValidatableFields)
	}
}

// TestScalarAllOfOnAPropertyReachesTheFieldRules pins the defect where an allOf
// whose branches only bound a scalar was dropped. generateAllOfDef flattens
// branches that carry object shape, but a branch that only tightens a string or
// a number leaves the property a plain Go value and never reaches that path:
// {"type":"string","allOf":[{"minLength":3},{"maxLength":10}]} emitted a bare
// string field and accepted {"a":"z"}.
func TestScalarAllOfOnAPropertyReachesTheFieldRules(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type":"string", "allOf":[{"minLength":3},{"maxLength":10}]},
			"b": {"type":"integer", "allOf":[{"minimum":2},{"multipleOf":4}]}
		},
		"required": ["a", "b"]
	}`)

	doc := structNamed(t, ir, "Doc")
	for _, tc := range []struct {
		jsonName string
		want     []string
	}{
		{"a", []string{"minLength", "maxLength"}},
		{"b", []string{"minimum", "multipleOf"}},
	} {
		got := fieldRuleTypes(doc, tc.jsonName)
		for _, want := range tc.want {
			if !containsString(got, want) {
				t.Fatalf("%q: field rules %v are missing %q", tc.jsonName, got, want)
			}
		}
	}
}

// TestScalarAllOfKeepsTheTighterBound checks that two branches bounding the
// same keyword are combined the way allOf means -- both must hold, so the
// tighter one governs -- rather than emitted once per branch.
func TestScalarAllOfKeepsTheTighterBound(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type":"string", "minLength":2, "allOf":[{"minLength":5},{"minLength":3}]}
		},
		"required": ["a"]
	}`)

	doc := structNamed(t, ir, "Doc")
	var bounds []any
	for _, rule := range doc.Validations {
		if rule.JSONName == "a" && rule.RuleType == "minLength" {
			bounds = append(bounds, rule.Value)
		}
	}
	if len(bounds) != 2 {
		t.Fatalf("minLength rules for a = %v, want the property's own bound and the merged branch bound", bounds)
	}
	if bounds[0] != 2 || bounds[1] != 5 {
		t.Fatalf("minLength bounds = %v, want [2 5] (own bound, then the tightest branch)", bounds)
	}
}

// TestScalarAllOfDropsARuleThatWouldNotCompile guards the direction this fix
// must not go. An allOf branch may bound a type the property does not have --
// a contradiction no value satisfies -- and folding a numeric bound onto a
// string field would emit `float64(r.A) < 5`, turning a schema that generates
// today into one that does not.
func TestScalarAllOfDropsARuleThatWouldNotCompile(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type":"string", "allOf":[{"type":"integer","minimum":5}]}
		},
		"required": ["a"]
	}`)

	doc := structNamed(t, ir, "Doc")
	if got := fieldRuleTypes(doc, "a"); len(got) != 0 {
		t.Fatalf("field rules for a = %v, want none: a numeric bound does not compile against a string field", got)
	}
}

// TestScalarAllOfOnAnArrayElementReachesTheElementRules is the same defect in
// item position, which the property path does not cover: an element schema
// carrying its bounds in an allOf produced []string with no per-element check.
func TestScalarAllOfOnAnArrayElementReachesTheElementRules(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type":"array", "items":{"type":"string", "allOf":[{"minLength":2}]}}
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	def := itemValidationFor(t, doc, "a")
	if got := itemRuleTypes(def, 0); !containsString(got, "minLength") {
		t.Fatalf("element rules %v are missing minLength", got)
	}
}

// TestObjectLevelIfThenElseIsChecked pins the third spelling of the dropped
// conditional: an if/then/else beside an object's properties produced no check
// anywhere in the generated Validate, so {"kind":"x","a":"ab"} was accepted
// against a `then` demanding minLength 5.
func TestObjectLevelIfThenElseIsChecked(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"kind": {"type":"string"},
			"a": {"type":"string"}
		},
		"required": ["kind", "a"],
		"if": {"properties": {"kind": {"const":"x"}}, "required": ["kind"]},
		"then": {"properties": {"a": {"minLength": 5}}},
		"else": {"properties": {"a": {"maxLength": 2}}}
	}`)

	doc := structNamed(t, ir, "Doc")
	if len(doc.ObjectConditionals) != 1 {
		t.Fatalf("expected one object-level conditional; got %+v", doc.ObjectConditionals)
	}
	cond := doc.ObjectConditionals[0]
	if !containsString(cond.If.RequiredKeys, "kind") {
		t.Fatalf("if branch requires %v, want kind", cond.If.RequiredKeys)
	}
	if len(cond.If.Properties) != 1 || cond.If.Properties[0].JSONName != "kind" {
		t.Fatalf("if branch checks %+v, want a check on kind", cond.If.Properties)
	}
	if len(cond.If.Properties[0].Checks) != 1 || cond.If.Properties[0].Checks[0].Kind != "const" {
		t.Fatalf("if branch check on kind = %+v, want a const check", cond.If.Properties[0].Checks)
	}
	if cond.Then == nil || len(cond.Then.Properties) != 1 ||
		cond.Then.Properties[0].Checks[0].Kind != "minLength" {
		t.Fatalf("then branch = %+v, want a minLength check on a", cond.Then)
	}
	if cond.Else == nil || len(cond.Else.Properties) != 1 ||
		cond.Else.Properties[0].Checks[0].Kind != "maxLength" {
		t.Fatalf("else branch = %+v, want a maxLength check on a", cond.Else)
	}
	// The check reads the object's raw JSON properties, which only the custom
	// unmarshaler keeps.
	if !doc.NeedsRawProps() || !doc.NeedsUnmarshal {
		t.Fatalf("Doc must capture _jsonRawProps for the conditional; got %+v", doc)
	}
}

// TestObjectLevelIfThenElseInsideAllOfIsChecked covers the shape the keyword is
// usually written in: the conditional sits in an allOf branch that is flattened
// into this same struct, so its group has to be collected from there too.
func TestObjectLevelIfThenElseInsideAllOfIsChecked(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"kind": {"type":"string"},
			"a": {"type":"string"}
		},
		"allOf": [{
			"if": {"properties": {"kind": {"const":"x"}}, "required": ["kind"]},
			"then": {"required": ["a"]}
		}]
	}`)

	doc := structNamed(t, ir, "Doc")
	if len(doc.ObjectConditionals) != 1 {
		t.Fatalf("expected one object-level conditional from the allOf branch; got %+v", doc.ObjectConditionals)
	}
	if then := doc.ObjectConditionals[0].Then; then == nil || !containsString(then.RequiredKeys, "a") {
		t.Fatalf("then branch = %+v, want a required on a", then)
	}
}

// TestObjectLevelConditionalFailsClosed is the guard that matters most here.
// The condition decides which branch binds, so a condition evaluated with one
// of its keywords ignored applies the wrong branch and rejects a document the
// schema allows. An `if` outside what the checks model must drop the whole
// group rather than approximate it.
//
// This is the half that stayed exact when issue #64 relaxed `then` and `else`
// (see TestObjectLevelConditionalThenKeepsItsExpressiblePart). Relaxing the
// condition the same way is what would turn under-enforcement into false
// rejection, so every case below is an `if`, or a `then` that leaves nothing at
// all behind.
func TestObjectLevelConditionalFailsClosed(t *testing.T) {
	for name, input := range map[string]string{
		"if uses an unmodelled keyword": `{
			"title": "Doc", "type": "object",
			"properties": {"a": {"type":"string"}},
			"if": {"properties": {"a": {"items": {"type":"string"}}}},
			"then": {"required": ["a"]}
		}`,
		"if carries a keyword beyond object shape": `{
			"title": "Doc", "type": "object",
			"properties": {"a": {"type":"string"}},
			"if": {"minProperties": 2},
			"then": {"required": ["a"]}
		}`,
		// The dangerous shape: an `if` that is partly expressible. Keeping the
		// part we model and ignoring the rest widens the condition, so `then`
		// binds to objects the schema never pointed it at and valid documents
		// are rejected.
		"if is only partly expressible": `{
			"title": "Doc", "type": "object",
			"properties": {"a": {"type":"string"}, "kind": {"type":"string"}},
			"if": {"required": ["kind"], "minProperties": 2},
			"then": {"required": ["a"]}
		}`,
		// `then` is lenient now, but leniency leaves nothing here: `enum` is the
		// property's only keyword, so the property carries no check, the branch
		// carries no property, and a group with neither branch is no group.
		"then is left with nothing to check": `{
			"title": "Doc", "type": "object",
			"properties": {"a": {"type":"string"}},
			"if": {"required": ["a"]},
			"then": {"properties": {"a": {"enum": ["p","q"]}}}
		}`,
		// The one keyword leniency must not read past. Before draft 2019-09 a
		// `$ref` replaces the schema object it sits in, so this `required` does
		// not apply and enforcing it would reject a document the schema allows.
		"then carries a ref beside its constraints": `{
			"title": "Doc", "type": "object",
			"$defs": {"other": {"type": "object"}},
			"properties": {"a": {"type":"string"}, "kind": {"type":"string"}},
			"if": {"required": ["kind"]},
			"then": {"$ref": "#/$defs/other", "required": ["a"]}
		}`,
		"if constrains nothing": `{
			"title": "Doc", "type": "object",
			"properties": {"a": {"type":"string"}},
			"if": {},
			"then": {"required": ["a"]}
		}`,
	} {
		t.Run(name, func(t *testing.T) {
			ir := generateForItemTest(t, input)
			doc := structNamed(t, ir, "Doc")
			if len(doc.ObjectConditionals) != 0 {
				t.Fatalf("expected no conditional group; got %+v", doc.ObjectConditionals)
			}
		})
	}
}

// TestObjectLevelConditionalThenKeepsItsExpressiblePart pins issue #64. A
// `then` or `else` used to be held to the `if`'s exact-or-nothing bar, so one
// keyword the checks do not model -- an `items` inside a property, say -- cost
// the whole group its check, the condition included. That is more conservative
// than the reasoning requires: a schema object's keywords are conjunctive, so
// enforcing part of a consequence accepts a superset of what the whole one
// accepts and can only ever under-enforce. Only the condition, which selects
// between the branches, has to be exact.
func TestObjectLevelConditionalThenKeepsItsExpressiblePart(t *testing.T) {
	for name, tc := range map[string]struct {
		input        string
		wantRequired []string
		wantChecks   map[string][]string // JSON property name -> check kinds, in order
	}{
		// The reproducer from the issue, reduced: the `then` types a property
		// with `items`, which the checks cannot express. The `type` beside it
		// can be, and dropping only `items` leaves a demand the schema makes.
		"property typed with items keeps its type": {
			input: `{
				"title": "Doc", "type": "object",
				"properties": {"kind": {"type":"string"}},
				"if": {"properties": {"kind": {"const":"tool"}}, "required": ["kind"]},
				"then": {
					"properties": {"tool": {"type":"array","items":{"type":"object"}}},
					"required": ["tool"]
				}
			}`,
			wantRequired: []string{"tool"},
			wantChecks:   map[string][]string{"tool": {"type"}},
		},
		// A branch-level keyword outside object shape. minProperties is a
		// conjunct of the `then`, so ignoring it keeps the `required` honest.
		"branch keyword outside object shape is ignored": {
			input: `{
				"title": "Doc", "type": "object",
				"properties": {"a": {"type":"string"}, "kind": {"type":"string"}},
				"if": {"required": ["kind"]},
				"then": {"required": ["a"], "minProperties": 3}
			}`,
			wantRequired: []string{"a"},
			wantChecks:   map[string][]string{},
		},
		// Per-property mixing: one property is expressible, one is not, and the
		// expressible one survives on its own.
		"an inexpressible property does not take the others with it": {
			input: `{
				"title": "Doc", "type": "object",
				"properties": {"kind": {"type":"string"}},
				"then": {"properties": {
					"a": {"type":"string","minLength":2},
					"b": {"enum": ["p","q"]}
				}},
				"if": {"required": ["kind"]}
			}`,
			wantRequired: nil,
			wantChecks:   map[string][]string{"a": {"type", "minLength"}},
		},
		// `else` is the same rule, and the branch it is paired with being
		// unusable does not stop it.
		"else keeps its part when then has none": {
			input: `{
				"title": "Doc", "type": "object",
				"properties": {"kind": {"type":"string"}},
				"if": {"required": ["kind"]},
				"then": {"properties": {"a": {"enum": ["p","q"]}}},
				"else": {"required": ["b"], "not": {"required": ["c"]}}
			}`,
			wantRequired: []string{"b"},
			wantChecks:   map[string][]string{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			ir := generateForItemTest(t, tc.input)
			doc := structNamed(t, ir, "Doc")
			if len(doc.ObjectConditionals) != 1 {
				t.Fatalf("expected one conditional group; got %+v", doc.ObjectConditionals)
			}
			group := doc.ObjectConditionals[0]
			branch := group.Then
			if branch == nil {
				branch = group.Else
			}
			if branch == nil {
				t.Fatalf("group carries neither then nor else: %+v", group)
			}
			if !slicesEqualString(branch.RequiredKeys, tc.wantRequired) {
				t.Fatalf("%s.RequiredKeys = %v, want %v", branch.Keyword, branch.RequiredKeys, tc.wantRequired)
			}
			got := map[string][]string{}
			for _, prop := range branch.Properties {
				var kinds []string
				for _, c := range prop.Checks {
					kinds = append(kinds, c.Kind)
				}
				got[prop.JSONName] = kinds
			}
			if len(got) != len(tc.wantChecks) {
				t.Fatalf("%s constrains %v, want %v", branch.Keyword, got, tc.wantChecks)
			}
			for name, want := range tc.wantChecks {
				if !slicesEqualString(got[name], want) {
					t.Fatalf("%s property %q checks = %v, want %v", branch.Keyword, name, got[name], want)
				}
			}
		})
	}
}

func slicesEqualString(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestOptionalRefToWrapperTypeIsAPointer covers the third instance of one
// defect: an optional property whose Go type is a materialized name loses the
// difference between "absent" and the Go zero.
//
// A definition that carries constraints but no "type" becomes an
// InferredAliasDef, and BigIntSupport turns a named integer into a
// BigIntAliasDef. Both are structs, and omitempty never omits a struct — so an
// absent optional property came back as the wrapper's zero, was fabricated into
// the output as 0, and was then measured against the definition's bounds, which
// rejected a document that conformed. A pointer is the only representation of
// absence such a wrapper has: unlike the raw-value wrappers it carries no
// IsZero, because its zero is exactly what a present 0 decodes to.
func TestOptionalRefToWrapperTypeIsAPointer(t *testing.T) {
	for name, tc := range map[string]struct {
		input string
		cfg   Config
	}{
		// The default configuration reaches it: no "type" on the definition.
		"inferred wrapper": {
			input: `{
				"title": "Root",
				"$defs": {"DefA": {"exclusiveMinimum": 17}},
				"type": "object",
				"properties": {"charlie": {"$ref": "#/$defs/DefA"}}
			}`,
			cfg: Config{PackageName: "testpkg", OmitEmpty: true},
		},
		// The same defect one flag over: BigIntSupport puts a wrapper over a
		// named integer that would otherwise be `type DefA int64` and a pointer.
		"big-int wrapper": {
			input: `{
				"title": "Root",
				"$defs": {"DefA": {"type": "integer", "exclusiveMinimum": 17}},
				"type": "object",
				"properties": {"charlie": {"$ref": "#/$defs/DefA"}}
			}`,
			cfg: Config{PackageName: "testpkg", OmitEmpty: true, BigIntSupport: true},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var s schema.Schema
			if err := json.Unmarshal([]byte(tc.input), &s); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			s.Normalize()

			ir, err := New(tc.cfg).Generate(&s)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			root := structNamed(t, ir, "Root")
			charlie, ok := fieldByJSONName(root, "charlie")
			if !ok {
				t.Fatalf("expected field for property charlie")
			}
			if got := charlie.Type.GoTypeName(); got != "*DefA" {
				t.Fatalf("charlie type = %q, want *DefA (a value wrapper is never omitted, so absent comes back as 0)", got)
			}
			if !charlie.OmitEmpty {
				t.Fatalf("charlie lost omitempty, so nil marshals as null rather than being omitted")
			}
			// omitzero is for the wrappers that can say "empty" themselves; a
			// pointer says it with nil and omitempty already drops that.
			if charlie.OmitZero {
				t.Fatalf("charlie is tagged ,omitzero as well as being a pointer")
			}
			// The pointer removes the second symptom too: the owner guards the
			// call on nil rather than handing Validate a zero the document
			// never carried.
			var vf ValidatableFieldDef
			for _, f := range root.ValidatableFields {
				if f.JSONName == "charlie" {
					vf = f
				}
			}
			if vf.JSONName == "" {
				t.Fatalf("charlie is never validated by Root: %#v", root.ValidatableFields)
			}
			if !vf.IsPointer {
				t.Fatalf("charlie's Validate call is not nil-guarded: %#v", vf)
			}
		})
	}
}

// TestRequiredRefToWrapperTypeStaysAValue guards the trade the fix must not
// make. A required property is always there, so it has nothing to say with nil
// and stays a value — the pointer rule is about absence, not about wrappers.
// The raw-value wrappers stay values as well: they carry IsZero, so ",omitzero"
// already omits an absent one and their own Validate passes over it.
func TestRequiredRefToWrapperTypeStaysAValue(t *testing.T) {
	input := `{
		"title": "Root",
		"$defs": {
			"DefA": {"exclusiveMinimum": 17},
			"Window": {"oneOf": [{"type":"integer","minimum":10}, {"type":"string"}]}
		},
		"type": "object",
		"properties": {
			"charlie": {"$ref": "#/$defs/DefA"},
			"window": {"$ref": "#/$defs/Window"}
		},
		"required": ["charlie"]
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg", OmitEmpty: true}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	root := structNamed(t, ir, "Root")
	for jsonName, want := range map[string]string{
		"charlie": "DefA",
		"window":  "Window",
	} {
		f, ok := fieldByJSONName(root, jsonName)
		if !ok {
			t.Fatalf("expected field for property %q", jsonName)
		}
		if got := f.Type.GoTypeName(); got != want {
			t.Fatalf("%s type = %q, want %q", jsonName, got, want)
		}
	}
	// The optional raw-value wrapper keeps the tag that omits it.
	window, _ := fieldByJSONName(root, "window")
	if !window.OmitZero {
		t.Fatalf("window lost ,omitzero, so an absent value marshals as null")
	}
}

// TestOptionalNamedFieldValidationIsGuardedByPresence covers the same family
// where no pointer is available. With OmitEmpty false every optional property
// is a value field, so the owner's Validate called the field type's Validate
// with no guard at all: a property the document did not carry was judged by its
// Go zero — `{"alpha":""}` was rejected with `delta.invalid RootDelta value: `
// against a schema that only asks delta to be "red" when it is there.
//
// The information was already on hand. _jsonKeys records the keys the source
// JSON carried, and an optional property with *inline* keywords is gated on it;
// only the arm for a materialized named type was not. A nil _jsonKeys means the
// value was not built from JSON, and there the call still has to run.
func TestOptionalNamedFieldValidationIsGuardedByPresence(t *testing.T) {
	input := `{
		"title": "Root",
		"type": "object",
		"properties": {
			"alpha": {"enum": [""]},
			"delta": {"const": "red"},
			"echo": {"const": "blue"}
		},
		"required": ["echo"]
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	// OmitEmpty false is the configuration that always takes this arm.
	ir, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	root := structNamed(t, ir, "Root")

	guards := make(map[string]ValidatableFieldDef, len(root.ValidatableFields))
	for _, vf := range root.ValidatableFields {
		guards[vf.JSONName] = vf
	}
	for _, jsonName := range []string{"alpha", "delta"} {
		vf, ok := guards[jsonName]
		if !ok {
			t.Fatalf("%q: expected among ValidatableFields", jsonName)
		}
		if vf.IsPointer {
			t.Fatalf("%q: OmitEmpty false is meant to leave the field a value", jsonName)
		}
		if !vf.PresenceGuard {
			t.Fatalf("%q: Validate is called unguarded, so an absent property is judged by its Go zero: %#v", jsonName, vf)
		}
	}
	// A required property is present by definition, and its own check reports a
	// missing key first; gating it would only hide a genuine violation.
	if echo, ok := guards["echo"]; !ok || echo.PresenceGuard {
		t.Fatalf("required property echo is presence-gated: %#v", echo)
	}
	// The guard has to have something to read. _jsonKeys is only emitted when
	// the struct says it needs it.
	if !root.NeedsJSONKeys() {
		t.Fatalf("Root does not carry _jsonKeys, so the presence guard cannot compile")
	}
}

// TestOptionalNamedPointerFieldNeedsNoPresenceGuard pins the other half: with
// omitempty the same properties are pointers, where nil already says "absent".
// Adding _jsonKeys there would gate the check on a second, weaker fact — a
// field set by hand after unmarshal would stop being validated.
func TestOptionalNamedPointerFieldNeedsNoPresenceGuard(t *testing.T) {
	input := `{
		"title": "Root",
		"type": "object",
		"properties": {"delta": {"const": "red"}}
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg", OmitEmpty: true}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	root := structNamed(t, ir, "Root")
	for _, vf := range root.ValidatableFields {
		if vf.JSONName != "delta" {
			continue
		}
		if !vf.IsPointer {
			t.Fatalf("delta is not a pointer under omitempty: %#v", vf)
		}
		if vf.PresenceGuard {
			t.Fatalf("delta is guarded twice, on nil and on _jsonKeys: %#v", vf)
		}
		return
	}
	t.Fatalf("delta is never validated by Root: %#v", root.ValidatableFields)
}

// TestAllOfKeepsParentObjectKeywords is a regression for the allOf flattening
// path rebuilding the schema object from scratch: generateAllOfDef starts from a
// fresh schema.Schema and copies the parent's keywords across one at a time, and
// propertyNames, minProperties, maxProperties and dependentRequired were not on
// that list. Each is enforced on the same object without the allOf, and each
// survives an anyOf, a oneOf or an if/then in the same position -- the allOf
// alone dropped them, so an object that said "no key may start with a digit"
// silently accepted {"9-bad":1}.
func TestAllOfKeepsParentObjectKeywords(t *testing.T) {
	input := `{
		"title": "Doc",
		"type": "object",
		"properties": {"alpha": {"type":"string"}},
		"allOf": [{"properties": {"bravo": {"type":"string"}}}],
		"propertyNames": {"pattern": "^[A-Za-z_][A-Za-z0-9_]*$"},
		"minProperties": 1,
		"maxProperties": 2,
		"dependentRequired": {"alpha": ["bravo"]}
	}`

	doc := structNamed(t, generateForItemTest(t, input), "Doc")

	if doc.PropertyNames == nil {
		t.Fatalf("propertyNames dropped beside allOf; StructDef.PropertyNames is nil")
	}
	if doc.PropertyNames.Pattern != "^[A-Za-z_][A-Za-z0-9_]*$" {
		t.Fatalf("PropertyNames.Pattern = %q, want the parent's pattern", doc.PropertyNames.Pattern)
	}

	var minProps, maxProps bool
	for _, v := range doc.Validations {
		switch v.RuleType {
		case "minProperties":
			minProps = true
			if v.Value != 1 {
				t.Fatalf("minProperties value = %v, want 1", v.Value)
			}
		case "maxProperties":
			maxProps = true
			if v.Value != 2 {
				t.Fatalf("maxProperties value = %v, want 2", v.Value)
			}
		}
	}
	if !minProps || !maxProps {
		t.Fatalf("min/maxProperties dropped beside allOf; rules = %+v", doc.Validations)
	}

	if len(doc.DependentRequired) != 1 ||
		doc.DependentRequired[0].TriggerKey != "alpha" ||
		len(doc.DependentRequired[0].Required) != 1 ||
		doc.DependentRequired[0].Required[0] != "bravo" {
		t.Fatalf("dependentRequired dropped or mangled beside allOf: %+v", doc.DependentRequired)
	}
}

// TestAllOfCombinesPropertyBoundsWithBranches checks the direction the parent's
// bounds are folded in. allOf means every branch binds at once, so the tighter
// of the parent's bound and a branch's is the one that holds, whichever side it
// came from. Propagating the parent's after the merge -- the shape every other
// keyword in generateAllOfDef uses -- would instead let the branch's win by
// having got there first, which is the "parent tighter" row below.
func TestAllOfCombinesPropertyBoundsWithBranches(t *testing.T) {
	for name, bounds := range map[string]struct{ parent, branch string }{
		"parent tighter": {`"minProperties":3,"maxProperties":3`, `"minProperties":1,"maxProperties":5`},
		"branch tighter": {`"minProperties":1,"maxProperties":5`, `"minProperties":3,"maxProperties":3`},
	} {
		t.Run(name, func(t *testing.T) {
			input := `{
				"title": "Doc",
				"type": "object",
				"properties": {"alpha": {"type":"string"}},
				` + bounds.parent + `,
				"allOf": [{"properties": {"bravo": {"type":"string"}}, ` + bounds.branch + `}]
			}`

			doc := structNamed(t, generateForItemTest(t, input), "Doc")

			got := map[string]any{}
			for _, v := range doc.Validations {
				if v.RuleType == "minProperties" || v.RuleType == "maxProperties" {
					got[v.RuleType] = v.Value
				}
			}
			if got["minProperties"] != 3 {
				t.Fatalf("minProperties = %v, want 3 (the tighter lower bound of the two)", got["minProperties"])
			}
			if got["maxProperties"] != 3 {
				t.Fatalf("maxProperties = %v, want 3 (the tighter upper bound of the two)", got["maxProperties"])
			}
		})
	}
}

// TestAllOfUnionsDependentRequired covers the one keyword of the four that a
// branch can also carry. mergeAllOfBranches takes a branch's dependentRequired
// only when the target has none, so seeding the parent's before the merge would
// have silently discarded the branch's. Both bind, so both must survive -- and
// the union must not be written back into the branch's own map.
//
// $schema is stated because mergeAllOfBranches reads a branch's
// dependentRequired only for a 2019-09 or later dialect.
func TestAllOfUnionsDependentRequired(t *testing.T) {
	input := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "Doc",
		"type": "object",
		"properties": {"alpha": {"type":"string"}, "bravo": {"type":"string"}, "charlie": {"type":"string"}},
		"dependentRequired": {"alpha": ["bravo"]},
		"allOf": [{"dependentRequired": {"alpha": ["charlie"], "bravo": ["charlie"]}}]
	}`

	doc := structNamed(t, generateForItemTest(t, input), "Doc")

	got := map[string][]string{}
	for _, dr := range doc.DependentRequired {
		got[dr.TriggerKey] = dr.Required
	}
	if len(got) != 2 {
		t.Fatalf("dependentRequired triggers = %+v, want alpha and bravo", doc.DependentRequired)
	}
	if !containsString(got["alpha"], "bravo") || !containsString(got["alpha"], "charlie") {
		t.Fatalf(`dependentRequired["alpha"] = %v, want both "bravo" (parent) and "charlie" (branch)`, got["alpha"])
	}
	if !containsString(got["bravo"], "charlie") {
		t.Fatalf(`dependentRequired["bravo"] = %v, want "charlie" from the branch`, got["bravo"])
	}
}

// TestContainsIntegerTypeImportsMath guards the import side of the per-element
// contains check. A contains sub-schema whose type is "integer" emits
// math.Trunc for every element, exactly as an items check does, but the import
// scan for a ContainsDef looked only for multipleOf and pattern. The generated
// file called math without importing it and did not compile.
func TestContainsIntegerTypeImportsMath(t *testing.T) {
	input := `{"type":"array","items":{"type":"integer"},"contains":{"type":"integer","minimum":10}}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var paths []string
	for _, imp := range ir.Imports {
		paths = append(paths, imp.Path)
	}
	if !containsString(paths, "math") {
		t.Fatalf("imports = %v, want math (the contains integer check calls math.Trunc)", paths)
	}
}

// TestTupleAliasIntegerPositionImportsMath is the same import gap one type
// definition over. An AliasDef's tuple positions test an "integer" with
// math.Trunc exactly as an InferredAliasDef's do, but only the inferred side's
// TupleItems were scanned for it. A one-position prefixItems typed "integer"
// beside unevaluatedItems:false takes the alias path and emitted a file calling
// math without importing it.
func TestTupleAliasIntegerPositionImportsMath(t *testing.T) {
	input := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "array",
		"prefixItems": [{"type":"integer"}],
		"unevaluatedItems": false
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var paths []string
	for _, imp := range ir.Imports {
		paths = append(paths, imp.Path)
	}
	if !containsString(paths, "math") {
		t.Fatalf("imports = %v, want math (the tuple integer position calls math.Trunc)", paths)
	}
}

// TestUntaggableOptionalFieldsSkipTheirAbsentValue pins issue #63. A property
// whose JSON name cannot go in a struct tag gets `json:"-"` and is written by
// hand in MarshalJSON, so neither omitempty nor omitzero ever reaches it. PR
// #53 taught the hand-written arm to skip an absent *pointer*; the slice, map
// and interface arms still wrote unconditionally, so `{}` came back as
// `{"a\"b":null}` -- an absent optional property invented as an explicit null,
// and a document that no longer round-trips.
//
// The omission follows omitzero's rule, not omitempty's: skip only the value
// unmarshal leaves for an absent property, never one the document carried.
// Unmarshal assigns only when the key is present, so a present [] or {} is
// non-nil and survives; omitempty would erase it.
func TestUntaggableOptionalFieldsSkipTheirAbsentValue(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"s\"l": {"type": "array", "items": {"type": "string"}},
			"m\"p": {"type": "object", "additionalProperties": {"type": "string"}},
			"a\"n": {"type": ["string", "number"]},
			"p\"t": {"type": "string"},
			"w\"r": {"if": {"type": "string"}, "then": {"minLength": 2}}
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	for _, tc := range []struct {
		jsonName string
		goType   string
		want     string
	}{
		{"s\"l", "[]string", "nil"},
		// map[string]string, not map[string]any: issue #84 taught the map arm
		// to keep a bare value type. The map is still nil when absent, which is
		// what this case is here to pin.
		{"m\"p", "map[string]string", "nil"},
		{"a\"n", "any", "nil"},
		{"p\"t", "*string", "nil"},
		{"w\"r", "DocWR", "iszero"},
	} {
		field := fieldNamedJSON(t, doc, tc.jsonName)
		if !field.ManualJSON {
			t.Fatalf("%q: ManualJSON = false, want true (the name cannot go in a struct tag)", tc.jsonName)
		}
		if got := field.Type.GoTypeName(); got != tc.goType {
			t.Fatalf("%q: type = %q, want %q -- the arm under test moved", tc.jsonName, got, tc.goType)
		}
		if field.ManualOmit != tc.want {
			t.Fatalf("%q (%s): ManualOmit = %q, want %q -- an absent optional value would be written as null", tc.jsonName, tc.goType, field.ManualOmit, tc.want)
		}
	}
}

// TestUntaggableRequiredFieldIsWrittenUnconditionally guards the narrowness of
// the arm above. A required property has to appear in the output whatever its
// Go value is, so it must keep writing unconditionally: extending the skip to
// it would drop a required key and produce a document its own schema rejects.
func TestUntaggableRequiredFieldIsWrittenUnconditionally(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"r\"q": {"type": "array", "items": {"type": "string"}}
		},
		"required": ["r\"q"]
	}`)

	field := fieldNamedJSON(t, structNamed(t, ir, "Doc"), "r\"q")
	if !field.ManualJSON {
		t.Fatalf("r\"q: ManualJSON = false, want true")
	}
	if field.ManualOmit != "" {
		t.Fatalf("r\"q: ManualOmit = %q, want \"\" -- a required property must be written even when its Go value is nil", field.ManualOmit)
	}
}

// TestBigIntSupportReachesAnInlineInteger covers the defect that made
// BigIntSupport a flag with no effect on the commonest way to write the schema.
//
// The flag replaces `type DefA int64` with a struct over an int64, a *big.Int
// and a flag, so an integer too large for an int64 still decodes. Only
// generateTypeDef builds that struct, and generateTypeDef is only reached for a
// schema being given a name. An integer written *inline* as a property's schema
// never had a name, so the field stayed an int64 and
// `{"alpha":10000000000000000000000}` failed inside encoding/json before any of
// the flag's machinery ran -- while the identical integer behind a $ref decoded,
// validated and re-marshalled unchanged.
//
// An array element and a map value are the same position one container down:
// []int64 holds no more than an int64 does.
func TestBigIntSupportReachesAnInlineInteger(t *testing.T) {
	ir, err := generateJSON(t, Config{PackageName: "testpkg", OmitEmpty: true, BigIntSupport: true}, `{
		"title": "Root",
		"type": "object",
		"properties": {
			"alpha": {"type": "integer", "maximum": 40},
			"beta": {"type": "array", "items": {"type": "integer", "maximum": 40}},
			"gamma": {"type": "object", "additionalProperties": {"type": "integer", "maximum": 40}}
		},
		"required": ["alpha", "beta", "gamma"]
	}`)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// Each position is named after where it sits, and each name is the
	// arbitrary-precision wrapper rather than an int64 under another name.
	root := structNamed(t, ir, "Root")
	for jsonName, want := range map[string]string{
		"alpha": "RootAlpha",
		"beta":  "[]RootBetaItem",
		"gamma": "map[string]RootGammaValue",
	} {
		field := fieldNamedJSON(t, root, jsonName)
		if got := field.Type.GoTypeName(); got != want {
			t.Fatalf("%s type = %q, want %q (an int64 cannot hold what BigIntSupport is for)", jsonName, got, want)
		}
	}
	for _, name := range []string{"RootAlpha", "RootBetaItem", "RootGammaValue"} {
		var wrapper *BigIntAliasDef
		for _, td := range ir.TypeDefs {
			if d, ok := td.(*BigIntAliasDef); ok && d.Name == name {
				wrapper = d
			}
		}
		if wrapper == nil {
			t.Fatalf("expected a %s big-integer wrapper; got %v", name, ir.TypeDefs)
		}
		// The keyword has to travel with the value: the wrapper's own Validate is
		// the only place a bound can be compared against a number no int64 holds.
		var ruleTypes []string
		for _, r := range wrapper.Validations {
			ruleTypes = append(ruleTypes, r.RuleType)
		}
		if !containsString(ruleTypes, "maximum") {
			t.Fatalf("%s checks %v, want maximum", name, ruleTypes)
		}
	}

	// The owner dispatches to each wrapper's Validate, which is what carries the
	// bound now that the field-level rule is gone.
	for _, jsonName := range []string{"alpha", "beta", "gamma"} {
		if !hasValidatableField(root.ValidatableFields, jsonName) {
			t.Fatalf("%s is never validated by Root: %+v", jsonName, root.ValidatableFields)
		}
	}
	// And it does not *also* check the bound itself. That rule converts the field
	// to a float64; the field is a struct, so the emitted file would not compile
	// at all -- "cannot convert r.Alpha (variable of struct type RootAlpha) to
	// type float64".
	if got := fieldRuleTypes(root, "alpha"); len(got) != 0 {
		t.Fatalf("Root checks %v on alpha itself; the wrapper's Validate carries those, and a numeric rule against a struct does not compile", got)
	}
}

// TestBigIntInlineIntegerStaysAnInt64WithoutTheFlag guards the blast radius of
// the arm above. Materializing a wrapper changes the property's Go type, which
// is a change to the API of the generated code, so it is confined to the flag
// that asks for arbitrary precision. A default run must be what it was: a plain
// int64 field, with the bound checked by the owner.
func TestBigIntInlineIntegerStaysAnInt64WithoutTheFlag(t *testing.T) {
	ir, err := generateJSON(t, Config{PackageName: "testpkg", OmitEmpty: true}, `{
		"title": "Root",
		"type": "object",
		"properties": {
			"alpha": {"type": "integer", "maximum": 40},
			"beta": {"type": "array", "items": {"type": "integer", "maximum": 40}}
		},
		"required": ["alpha", "beta"]
	}`)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	root := structNamed(t, ir, "Root")
	for jsonName, want := range map[string]string{"alpha": "int64", "beta": "[]int64"} {
		field := fieldNamedJSON(t, root, jsonName)
		if got := field.Type.GoTypeName(); got != want {
			t.Fatalf("%s type = %q, want %q; BigIntSupport is off, so nothing here may change", jsonName, got, want)
		}
	}
	if got := fieldRuleTypes(root, "alpha"); !containsString(got, "maximum") {
		t.Fatalf("Root checks %v on alpha, want maximum; with no wrapper to dispatch to, the owner is the only place the bound can live", got)
	}
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*BigIntAliasDef); ok {
			t.Fatalf("generated a %s big-integer wrapper with BigIntSupport off", d.Name)
		}
	}
}

// TestBigIntInlineWrapperOnlyWhereGenerateTypeDefWouldBuildOne guards the other
// side. Materializing a schema generateTypeDef answers some *other* way does not
// leave the property alone -- it silently retypes it to that other answer. So
// the predicate admits only the schema that reaches the BigIntAliasDef arm, and
// every keyword routing generateTypeDef elsewhere disqualifies it.
//
// ["integer","null"] is the sharpest of these, and the reason the rule is stated
// over the whole type list rather than over "the first non-null type": it
// resolves to *int64, which decodes a JSON null and needs nothing from the
// wrapper. Taking this position over would retype the field to a named wrapper
// -- an API change to the generated code -- for a value that already works. The
// wrapper has been able to represent null since issue #85; that made the
// exclusion a choice rather than a necessity, and it is still the choice made
// here.
func TestBigIntInlineWrapperOnlyWhereGenerateTypeDefWouldBuildOne(t *testing.T) {
	for name, tc := range map[string]struct{ schema, want string }{
		"nullable":  {`{"type": ["integer","null"], "maximum": 40}`, "*int64"},
		"enum":      {`{"type": "integer", "enum": [1,2,3]}`, "RootAlpha"},
		"const":     {`{"type": "integer", "const": 5}`, "int64"},
		"allOf":     {`{"type": "integer", "allOf": [{"maximum": 40}]}`, "int64"},
		"ref":       {`{"$ref": "#/$defs/DefA"}`, "DefA"},
		"untyped":   {`{"maximum": 40}`, "float64"},
		"notAnInt":  {`{"type": "number", "maximum": 40}`, "float64"},
		"objectish": {`{"type": "integer", "properties": {"x": {"type": "string"}}}`, "int64"},
	} {
		t.Run(name, func(t *testing.T) {
			ir, err := generateJSON(t, Config{PackageName: "testpkg", OmitEmpty: true, BigIntSupport: true}, `{
				"title": "Root",
				"$defs": {"DefA": {"type": "integer", "maximum": 40}},
				"type": "object",
				"properties": {"alpha": `+tc.schema+`},
				"required": ["alpha"]
			}`)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			field := fieldNamedJSON(t, structNamed(t, ir, "Root"), "alpha")
			if got := field.Type.GoTypeName(); got != tc.want {
				t.Fatalf("alpha type = %q, want %q", got, tc.want)
			}
			// Several of these do keep a definition of their own -- an enum, an
			// inferred wrapper, a struct. What none of them may be is the
			// big-integer wrapper, which is what over-reach here would produce
			// under exactly the same name.
			for _, td := range ir.TypeDefs {
				if d, ok := td.(*BigIntAliasDef); ok && d.Name == "RootAlpha" {
					t.Fatalf("alpha was materialized into a big-integer wrapper (%s); generateTypeDef answers this schema with %s instead, and taking it over loses that", d.Name, tc.want)
				}
			}
		})
	}

	// An array element takes the rule on its own. The property path above has an
	// arm for a nullable schema ahead of this one, so the type-list rule is only
	// load-bearing here: nothing else stands between ["integer","null"] and the
	// wrapper.
	t.Run("nullable element", func(t *testing.T) {
		ir, err := generateJSON(t, Config{PackageName: "testpkg", OmitEmpty: true, BigIntSupport: true}, `{
			"title": "Root",
			"type": "object",
			"properties": {"beta": {"type": "array", "items": {"type": ["integer","null"], "maximum": 40}}},
			"required": ["beta"]
		}`)
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		field := fieldNamedJSON(t, structNamed(t, ir, "Root"), "beta")
		if got := field.Type.GoTypeName(); got != "[]*int64" {
			t.Fatalf("beta type = %q, want []*int64; the inline nullable spelling is not the wrapper's to take", got)
		}
	})
}

// TestNamedNullableBigIntWrapperRepresentsNull pins issue #85. A *named*
// ["integer","null"] does reach the BigIntAliasDef arm -- unlike the inline
// spelling, which resolves to *int64 -- and the wrapper it built held an int64
// and a *big.Int and nothing else. A JSON null decodes into a json.Number as the
// empty string, so every numeric parse failed and the generated UnmarshalJSON
// rejected `{"n":null}` with "value  is not a valid integer", against a schema
// that explicitly permits it.
//
// The repair is a third state on the wrapper. Neither existing field can stand
// in for null: the int64 zero is what a literal 0 decodes to, and a nil *big.Int
// is what every int64-sized value leaves behind. AllowsNull is what turns that
// state on, and it is off wherever the schema does not admit null -- so the
// non-nullable wrapper, and its generated source, are untouched.
func TestNamedNullableBigIntWrapperRepresentsNull(t *testing.T) {
	ir, err := generateJSON(t, Config{PackageName: "testpkg", OmitEmpty: true, BigIntSupport: true}, `{
		"title": "Root",
		"$defs": {
			"Nullable": {"type": ["integer","null"], "maximum": 40},
			"Plain":    {"type": "integer", "maximum": 40}
		},
		"type": "object",
		"properties": {
			"n": {"$ref": "#/$defs/Nullable"},
			"p": {"$ref": "#/$defs/Plain"}
		},
		"required": ["n", "p"]
	}`)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	wrappers := map[string]*BigIntAliasDef{}
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*BigIntAliasDef); ok {
			wrappers[d.Name] = d
		}
	}

	nullable := wrappers["Nullable"]
	if nullable == nil {
		t.Fatalf("expected a Nullable big-integer wrapper; got %v", ir.TypeDefs)
	}
	if !nullable.AllowsNull {
		t.Fatalf("Nullable.AllowsNull = false; the wrapper has no state for the null the schema permits, and decoding one fails as \"not a valid integer\"")
	}
	if nullable.NeedsNullCheck {
		t.Fatalf("Nullable.NeedsNullCheck = true; the schema lists \"null\", so nothing may reject it")
	}
	// The keyword still travels with the value. Representing null must not cost
	// the bound that made the wrapper worth generating.
	var ruleTypes []string
	for _, r := range nullable.Validations {
		ruleTypes = append(ruleTypes, r.RuleType)
	}
	if !containsString(ruleTypes, "maximum") {
		t.Fatalf("Nullable checks %v, want maximum", ruleTypes)
	}

	// The non-nullable wrapper keeps neither the state nor the branches that go
	// with it. Emitting them unconditionally would put a dead field and a dead
	// branch into every big-integer wrapper in every generated file.
	plain := wrappers["Plain"]
	if plain == nil {
		t.Fatalf("expected a Plain big-integer wrapper; got %v", ir.TypeDefs)
	}
	if plain.AllowsNull {
		t.Fatalf("Plain.AllowsNull = true; this schema does not admit null and gains nothing from a state that says it does")
	}
	if !plain.NeedsNullCheck {
		t.Fatalf("Plain.NeedsNullCheck = false; a null is not an integer and must still be rejected")
	}

	// And the field keeps the wrapper. Declining ["integer","null"] at this arm
	// -- resolving it the way the inline spelling does -- was the alternative
	// fix, and it would have cost the arbitrary precision the flag was asked
	// for: a value beyond int64 in a named nullable position would no longer
	// survive the round trip.
	if got := fieldNamedJSON(t, structNamed(t, ir, "Root"), "n").Type.GoTypeName(); got != "Nullable" {
		t.Fatalf("n type = %q, want Nullable; resolving it to a plain integer buys null by giving up precision", got)
	}
}

// oneOfDefFor returns the sealed-interface union group a struct carries for a
// property, or nil when the property is not rendered as a union.
func oneOfDefFor(sd *StructDef, jsonName string) *OneOfDef {
	for i := range sd.OneOfs {
		if sd.OneOfs[i].JSONName == jsonName {
			return &sd.OneOfs[i]
		}
	}
	return nil
}

// TestBigIntWrapperIsDraftAwareAboutFloatNotation covers the same keyword under
// --big-int, in the direction that over-accepts rather than the one that
// over-rejects.
//
// The wrapper decodes through json.Number and takes a float-notation number
// with a zero fractional part, which is right from draft 6 on and wrong before
// it: draft 3 and draft 4 define an integer as a number written with no
// fraction and no exponent. The plain integer alias beside it has answered that
// question through StrictInteger since draft-aware tokens were added; the
// wrapper did not, so `--big-int` on a draft-4 document accepted 1.0 for an
// integer the draft says it is not.
func TestBigIntWrapperIsDraftAwareAboutFloatNotation(t *testing.T) {
	for draft, want := range map[schema.Draft]bool{
		schema.Draft03:     true,
		schema.Draft04:     true,
		schema.Draft06:     false,
		schema.Draft07:     false,
		schema.Draft202012: false,
	} {
		var s schema.Schema
		if err := json.Unmarshal([]byte(`{"type":"integer","minimum":0}`), &s); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		s.Normalize()
		ir, err := New(Config{PackageName: "testpkg", Draft: draft, BigIntSupport: true}).Generate(&s)
		if err != nil {
			t.Fatalf("draft %v: generate: %v", draft, err)
		}
		def, ok := ir.TypeDefs[0].(*BigIntAliasDef)
		if !ok {
			t.Fatalf("draft %v: root type = %T, want BigIntAliasDef", draft, ir.TypeDefs[0])
		}
		if def.StrictInteger != want {
			t.Fatalf("draft %v: StrictInteger = %v, want %v -- the wrapper reads 1.0 the way the draft does or it does not", draft, def.StrictInteger, want)
		}
	}
}

// aliasNamed returns the AliasDef with the given name, or nil.
func aliasNamed(ir *File, name string) *AliasDef {
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*AliasDef); ok && d.Name == name {
			return d
		}
	}
	return nil
}

// validatableFieldFor returns the entry that makes the owner's Validate call the
// field's own Validate, or nil when it carries none.
func validatableFieldFor(sd *StructDef, jsonName string) *ValidatableFieldDef {
	for i := range sd.ValidatableFields {
		if sd.ValidatableFields[i].JSONName == jsonName {
			return &sd.ValidatableFields[i]
		}
	}
	return nil
}

// TestConstraintOnlyOneOfPropertyLeavesTheUnionPath pins the defect where a
// oneOf whose branches state bounds and no type made a property unusable.
//
//	{"type":"integer","oneOf":[{"minimum":10},{"maximum":5}]}
//
// Neither branch says what the value is, so resolveOneOfVariant gave each one
// `any` and oneOfVariantChecks gave each one no checks. The union then held two
// variants that both matched every JSON value: 20, 12 and 3 each satisfy
// exactly one branch and are valid, 7 satisfies none, and all four were
// rejected as "multiple oneOf variants matched (2)". No value was accepted, and
// the one correct rejection named the wrong reason. The sibling "type", the
// only keyword in the schema that says what the value is, went with it.
//
// The repair is to leave the union path -- there is nothing to select on -- and
// materialize the property's own type, where the declared "integer" becomes the
// Go type and the branches become the oneOf rules its Validate counts. That is
// what the identical schema at the document root has always done.
func TestConstraintOnlyOneOfPropertyLeavesTheUnionPath(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type": "integer", "oneOf": [{"minimum": 10}, {"maximum": 5}]}
		},
		"required": ["a"]
	}`)

	doc := structNamed(t, ir, "Doc")
	if got := oneOfDefFor(doc, "a"); got != nil {
		t.Fatalf("a became a sealed-interface union with variants %+v; branches carrying no type give it nothing to select on", got.Variants)
	}

	alias := aliasNamed(ir, "DocA")
	if alias == nil {
		t.Fatalf("expected a DocA alias carrying the branches; got %v", ir.TypeDefs)
	}
	if got := alias.Underlying.GoTypeName(); got != "int64" {
		t.Fatalf("DocA underlying = %q, want int64 (the sibling type the union dropped)", got)
	}
	if len(alias.OneOfVariants) != 2 {
		t.Fatalf("DocA oneOf variants = %+v, want two", alias.OneOfVariants)
	}
	var ruleTypes []string
	for _, variant := range alias.OneOfVariants {
		for _, rule := range variant {
			ruleTypes = append(ruleTypes, rule.RuleType)
		}
	}
	if !containsString(ruleTypes, "minimum") || !containsString(ruleTypes, "maximum") {
		t.Fatalf("DocA oneOf variant rules = %v, want the branch bounds", ruleTypes)
	}

	field := fieldNamedJSON(t, doc, "a")
	if got := field.Type.GoTypeName(); got != "DocA" {
		t.Fatalf("a type = %q, want DocA", got)
	}
	// The branches only enforce anything if the owner calls the field's Validate.
	if validatableFieldFor(doc, "a") == nil {
		t.Fatalf("expected Doc.Validate to call a.Validate; got %+v", doc.ValidatableFields)
	}
}

// TestConstraintOnlyOneOfPropertyWithNoTypeReachesTheDynamicEvaluator is the
// same defect where the schema does not even name a type:
//
//	{"oneOf":[{"minimum":10},{"maximum":5}]}
//
// There is no declared type for the branches to attach to, so the property has
// to become the raw-JSON wrapper whose Validate evaluates them against the
// decoded value -- again what the document root already does. Leaving the union
// path without materializing that wrapper would take the property to a bare
// `any` field and drop the oneOf outright, which is a quieter spelling of the
// same bug.
func TestConstraintOnlyOneOfPropertyWithNoTypeReachesTheDynamicEvaluator(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"oneOf": [{"minimum": 10}, {"maximum": 5}]}
		},
		"required": ["a"]
	}`)

	doc := structNamed(t, ir, "Doc")
	if got := oneOfDefFor(doc, "a"); got != nil {
		t.Fatalf("a became a sealed-interface union with variants %+v", got.Variants)
	}

	var wrapper *DynamicSchemaDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*DynamicSchemaDef); ok && d.Name == "DocA" {
			wrapper = d
		}
	}
	if wrapper == nil {
		t.Fatalf("expected a DocA dynamic wrapper carrying the branches; got %v", ir.TypeDefs)
	}

	field := fieldNamedJSON(t, doc, "a")
	if got := field.Type.GoTypeName(); got != "DocA" {
		t.Fatalf("a type = %q, want DocA", got)
	}
	if validatableFieldFor(doc, "a") == nil {
		t.Fatalf("expected Doc.Validate to call a.Validate; got %+v", doc.ValidatableFields)
	}
}

// TestSelectableOneOfPropertiesStayUnions is the other side of that repair.
// Leaving the union path is only right where the branches give it nothing to
// select on; a branch that names a type, declares properties or lists required
// keys does, and the union is how those are generated. Discriminated unions are
// what the tool emits for the shapes users actually write, so an over-broad
// reading of "constraint-only" would take every one of them off the union path
// at once.
//
// Each case is a branch shape that must keep its union: a typed scalar pair, a
// required-key pair (`any` variants that still discriminate, on the required
// keys rather than on the type), an inline object pair, and a same-typed pair
// separated only by their bounds.
func TestSelectableOneOfPropertiesStayUnions(t *testing.T) {
	cases := []struct {
		name     string
		branches string
	}{
		{"typed scalars", `[{"type":"string"},{"type":"integer"}]`},
		{"required keys only", `[{"required":["x"]},{"required":["y"]}]`},
		{"inline objects", `[{"properties":{"x":{"type":"string"}},"required":["x"]},{"properties":{"y":{"type":"integer"}},"required":["y"]}]`},
		{"same type, different bounds", `[{"type":"integer","maximum":5},{"type":"integer","minimum":10}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ir := generateForItemTest(t, `{
				"title": "Doc",
				"type": "object",
				"properties": {"a": {"oneOf": `+tc.branches+`}},
				"required": ["a"]
			}`)

			doc := structNamed(t, ir, "Doc")
			group := oneOfDefFor(doc, "a")
			if group == nil {
				t.Fatalf("a lost its sealed-interface union; struct oneOfs = %+v, typedefs = %v", doc.OneOfs, ir.TypeDefs)
			}
			if len(group.Variants) != 2 {
				t.Fatalf("a union variants = %+v, want two", group.Variants)
			}
		})
	}
}

// TestBigIntAliasCarriesItsOneOfVariants pins the generator half of the defect
// the co-generation harness found once a constraint-only oneOf could reach a
// property: under BigIntSupport an integer becomes a BigIntAliasDef, and the
// branches have to travel with it. The emitter half -- the Validate template
// that rendered Validations and dropped these -- is pinned in
// pkg/emitter/emitter_test.go, since a template test from here would close an
// import cycle.
func TestBigIntAliasCarriesItsOneOfVariants(t *testing.T) {
	input := `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type": "integer", "oneOf": [{"minimum": 10}, {"maximum": 5}]}
		},
		"required": ["a"]
	}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg", BigIntSupport: true}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var alias *BigIntAliasDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*BigIntAliasDef); ok && d.Name == "DocA" {
			alias = d
		}
	}
	if alias == nil {
		t.Fatalf("expected a DocA big-int alias; got %v", ir.TypeDefs)
	}
	if len(alias.OneOfVariants) != 2 {
		t.Fatalf("DocA oneOf variants = %+v, want two", alias.OneOfVariants)
	}
}

// TestOneOfObjectVariantsAreMarkedValidatable pins the generator half of issue
// #61: Validate never descended into a oneOf union field, so an object
// variant's nested constraints were dead. PR #58 closed the scalar case by
// applying each branch's rules during selection, but selection only decides
// which branch decodes -- {"a":{"x":"z"}} was accepted against a branch
// requiring minLength 3, and a hand-built value escaped checking entirely.
//
// The dispatch the emitter writes is keyed on OneOfVariant.Validatable, so this
// is where the decision is made. The emitter half -- the type switch itself --
// is pinned in pkg/emitter/emitter_test.go, since a template test from here
// would close an import cycle.
func TestOneOfObjectVariantsAreMarkedValidatable(t *testing.T) {
	input := `{"type":"object",
		"properties":{"a":{"oneOf":[
			{"type":"object","properties":{"x":{"type":"string","minLength":3}},"required":["x"]},
			{"type":"object","properties":{"y":{"type":"integer"}},"required":["y"]}]}},
		"required":["a"]}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var root *StructDef
	for _, td := range ir.TypeDefs {
		if sd, ok := td.(*StructDef); ok && sd.Name == "Root" {
			root = sd
		}
	}
	if root == nil {
		t.Fatalf("expected a StructDef named Root, got %#v", ir.TypeDefs)
	}
	if len(root.OneOfs) != 1 || len(root.OneOfs[0].Variants) != 2 {
		t.Fatalf("Root.OneOfs = %#v, want one union of 2 variants", root.OneOfs)
	}
	if !root.OneOfs[0].HasValidatableVariants() {
		t.Fatalf("Root.OneOfs[0].HasValidatableVariants() = false; the union emits no dispatch at all")
	}
	for _, v := range root.OneOfs[0].Variants {
		if !v.Validatable {
			t.Fatalf("variant %s (type %s) Validatable = false, want true: its own type carries the branch's constraints and nothing else applies them",
				v.FieldName, v.Type.GoTypeName())
		}
		// The wrapper holds the variant by pointer, which is what lets the
		// dispatch skip a nil rather than call a value-receiver method through
		// one. The template reads Type.IsPointer directly, so a variant that
		// stopped being a pointer would silently change the emitted guard.
		if !v.Type.IsPointer() {
			t.Fatalf("variant %s type = %s, want a pointer", v.FieldName, v.Type.GoTypeName())
		}
	}
}

// TestOneOfScalarVariantsAreNotMarkedValidatable is the over-reach guard for
// the arm above. A scalar variant's wrapper holds a plain Go string or int64
// and a constraint-only branch resolves to `any`; neither has a Validate, so a
// dispatch case for one would not compile, and a group with no dispatchable
// variant at all would emit a type switch whose bound variable goes unused.
// Their branch constraints ride on OneOfVariant.Checks instead, applied during
// selection.
func TestOneOfScalarVariantsAreNotMarkedValidatable(t *testing.T) {
	input := `{"type":"object","properties":{
		"a":{"oneOf":[{"type":"string","minLength":3},{"type":"integer","minimum":5}]},
		"b":{"oneOf":[{"required":["p"]},{"required":["q"]}]}}}`

	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(Config{PackageName: "testpkg"}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	groups := 0
	for _, td := range ir.TypeDefs {
		sd, ok := td.(*StructDef)
		if !ok {
			continue
		}
		for _, oof := range sd.OneOfs {
			groups++
			if oof.HasValidatableVariants() {
				t.Fatalf("%s.%s HasValidatableVariants() = true; an empty type switch does not compile",
					sd.Name, oof.FieldName)
			}
			for _, v := range oof.Variants {
				if v.Validatable {
					t.Fatalf("%s.%s variant %s (type %s) Validatable = true; that type has no Validate method",
						sd.Name, oof.FieldName, v.FieldName, v.Type.GoTypeName())
				}
			}
		}
	}
	if groups != 2 {
		t.Fatalf("found %d oneOf groups, want 2 (the scalar union and the required-only union)", groups)
	}
}

// variantRuleTypes lists the rule types of one rule set, in order.
func variantRuleTypes(rules []ValidationRule) []string {
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		out = append(out, rule.RuleType)
	}
	return out
}

// TestOneOfBranchVacuousForTheInstanceTypeIsSatisfied pins issue #80. The
// evaluator tested every branch against the value instead of first asking
// whether the branch's keywords say anything about a value of that type:
//
//	{"type":"integer","oneOf":[{"minimum":10},{"minLength":3}]}
//
// minLength speaks about strings, so for an integer it is satisfied vacuously
// and the second branch matches every integer. 20 therefore matches both
// branches and is invalid; 5 matches only the second and is valid. Both
// verdicts came out inverted, because the emitted check was
// utf8.RuneCountInString(string(r)) -- which converts the number to the single
// rune with that code point and measures that, so it failed for every integer.
// python-jsonschema and js-ajv agree on 20=invalid, 5=valid.
//
// The repair is the general rule, not a special case for minLength: a branch
// keyword outside the instance type's domain contributes no check at all, which
// leaves the branch matching everything -- which is what it does.
func TestOneOfBranchVacuousForTheInstanceTypeIsSatisfied(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type": "integer", "oneOf": [{"minimum": 10}, {"minLength": 3}]}
		},
		"required": ["a"]
	}`)

	alias := aliasNamed(ir, "DocA")
	if alias == nil {
		t.Fatalf("expected a DocA alias carrying the branches; got %v", ir.TypeDefs)
	}
	if len(alias.OneOfVariants) != 2 {
		t.Fatalf("DocA oneOf variants = %+v, want two", alias.OneOfVariants)
	}
	if got := variantRuleTypes(alias.OneOfVariants[0]); !containsString(got, "minimum") {
		t.Fatalf("branch 0 rules = %v, want the minimum bound it states", got)
	}
	if got := variantRuleTypes(alias.OneOfVariants[1]); len(got) != 0 {
		t.Fatalf("branch 1 rules = %v, want none: minLength says nothing about an integer, so the branch is satisfied", got)
	}
}

// TestOneOfBranchKeywordInTheInstanceTypeIsKept is the over-reach guard for the
// arm above. A branch keyword that *does* speak about the instance's type is
// the whole of what the branch asserts, and dropping it would make the branch
// match every value -- turning oneOf's count into the branch count and, for a
// two-branch union, rejecting everything.
func TestOneOfBranchKeywordInTheInstanceTypeIsKept(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type": "string", "oneOf": [{"minLength": 3}, {"maxLength": 1}]}
		},
		"required": ["a"]
	}`)

	alias := aliasNamed(ir, "DocA")
	if alias == nil {
		t.Fatalf("expected a DocA alias carrying the branches; got %v", ir.TypeDefs)
	}
	if len(alias.OneOfVariants) != 2 {
		t.Fatalf("DocA oneOf variants = %+v, want two", alias.OneOfVariants)
	}
	if got := variantRuleTypes(alias.OneOfVariants[0]); !containsString(got, "minLength") {
		t.Fatalf("branch 0 rules = %v, want minLength: the value is a string, so the keyword applies", got)
	}
	if got := variantRuleTypes(alias.OneOfVariants[1]); !containsString(got, "maxLength") {
		t.Fatalf("branch 1 rules = %v, want maxLength: the value is a string, so the keyword applies", got)
	}
}

// TestOneOfBranchTypedForAnotherInstanceTypeMatchesNothing is the other half of
// issue #80's general rule. A branch that names a type the value cannot have is
// not vacuously satisfied -- it is unsatisfiable:
//
//	{"type":"integer","oneOf":[{"minimum":10},{"type":"string","minLength":3}]}
//
// The second branch matches no integer at all, so 20 matches exactly one branch
// and is valid while 5 matches none and is invalid, which both reference
// implementations confirm. Dropping the branch's minLength as vacuous without
// also reading its "type" would leave a branch with no checks -- one that
// matches everything -- and invert the answer a second time.
//
// Written at the document root because a branch that names a type is one the
// union path can select on, so in a property position the schema becomes a
// sealed-interface union instead of an alias carrying branch rules.
func TestOneOfBranchTypedForAnotherInstanceTypeMatchesNothing(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "integer",
		"oneOf": [{"minimum": 10}, {"type": "string", "minLength": 3}]
	}`)

	alias := aliasNamed(ir, "Doc")
	if alias == nil {
		t.Fatalf("expected a Doc alias carrying the branches; got %v", ir.TypeDefs)
	}
	if len(alias.OneOfVariants) != 2 {
		t.Fatalf("Doc oneOf variants = %+v, want two", alias.OneOfVariants)
	}
	if got := variantRuleTypes(alias.OneOfVariants[1]); !containsString(got, "never") {
		t.Fatalf("branch 1 rules = %v, want a \"never\" rule: no integer is a string, so the branch matches nothing", got)
	}
}

// TestOneOfBranchTheAliasCannotJudgeDropsTheWholeGroup pins the fail-closed
// half. An enum in a branch has no expression against the alias's single value,
// and a branch judged with one of its keywords ignored is judged as matching
// more values than it does. Under oneOf that inflates the count, and an
// inflated count rejects documents the schema allows: before this,
// {"type":"string","oneOf":[{"minLength":3},{"enum":["a"]}]} rejected "abcd",
// which both reference implementations accept.
//
// Emitting nothing under-enforces, which is the safe direction; emitting a
// count built from a branch nobody read is not.
func TestOneOfBranchTheAliasCannotJudgeDropsTheWholeGroup(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type": "string", "oneOf": [{"minLength": 3}, {"enum": ["a"]}]}
		},
		"required": ["a"]
	}`)

	alias := aliasNamed(ir, "DocA")
	if alias == nil {
		t.Fatalf("expected a DocA alias; got %v", ir.TypeDefs)
	}
	if len(alias.OneOfVariants) != 0 {
		t.Fatalf("DocA oneOf variants = %+v, want none: the enum branch is not expressible here and a partial count is worse than no count", alias.OneOfVariants)
	}
}

// TestFieldKeywordVacuousForItsGoTypeIsDropped is issue #80's general rule in
// the ordinary property position. {"type":"integer","minLength":3} says nothing
// about the integer it types, so there is nothing to check -- and the check
// that was emitted did not even compile, handing an int64 field to
// utf8.RuneCountInString.
func TestFieldKeywordVacuousForItsGoTypeIsDropped(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type": "integer", "minLength": 3},
			"b": {"type": "string", "minLength": 3}
		},
		"required": ["a", "b"]
	}`)

	doc := structNamed(t, ir, "Doc")
	for _, rule := range doc.Validations {
		if rule.JSONName == "a" {
			t.Fatalf("Doc.a kept a %q rule; minLength says nothing about an integer, and the emitted check does not compile", rule.RuleType)
		}
	}
	// The over-reach guard: the same keyword on a string field is the whole of
	// what that property asserts and must survive.
	kept := false
	for _, rule := range doc.Validations {
		if rule.JSONName == "b" && rule.RuleType == "minLength" {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("Doc.b lost its minLength rule; got %+v", doc.Validations)
	}
}

// TestAliasKeywordVacuousForItsGoTypeIsDropped is the same rule at the document
// root. {"type":"integer","minLength":3} generated `type Root int64` whose
// Validate measured utf8.RuneCountInString(string(r)) -- one rune for any
// integer -- and so rejected every value the schema accepts.
func TestAliasKeywordVacuousForItsGoTypeIsDropped(t *testing.T) {
	ir := generateForItemTest(t, `{"title":"Doc","type":"integer","minLength":3,"minimum":2}`)

	alias := aliasNamed(ir, "Doc")
	if alias == nil {
		t.Fatalf("expected a Doc alias; got %v", ir.TypeDefs)
	}
	got := variantRuleTypes(alias.Validations)
	if containsString(got, "minLength") {
		t.Fatalf("Doc validations = %v, want no minLength: it says nothing about an integer", got)
	}
	if !containsString(got, "minimum") {
		t.Fatalf("Doc validations = %v, want the minimum bound it states", got)
	}
}

// TestOneOfRequiredOnlyVariantIsFullyChecked pins the generator half of issue
// #81. Selection gated an object branch on the presence of its required keys
// and never consulted anything else the branch says, so
//
//	{"oneOf":[{"required":["x"],"properties":{"x":{"type":"integer","minimum":10}}},
//	          {"required":["x","y"]}]}
//
// counted two matches for {"x":1,"y":2} -- both branches' required keys are
// there -- and rejected a document both reference implementations accept, since
// x=1 violates the first branch's minimum.
//
// The repair narrows an already-ambiguous selection using what each branch
// actually asserts, which is only sound when every branch in play can be
// judged. FullyChecked is that judgement for a branch with no Validate of its
// own: a required-only branch states nothing selection does not already test.
func TestOneOfRequiredOnlyVariantIsFullyChecked(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"oneOf": [
				{"required": ["x"], "properties": {"x": {"type": "integer", "minimum": 10}}},
				{"required": ["x", "y"]}
			]}
		},
		"required": ["a"]
	}`)

	doc := structNamed(t, ir, "Doc")
	union := oneOfDefFor(doc, "a")
	if union == nil || len(union.Variants) != 2 {
		t.Fatalf("expected a two-variant union on a; got %+v", doc.OneOfs)
	}
	if !union.Variants[0].Validatable {
		t.Fatalf("variant 0 (type %s) Validatable = false; its own type is what carries the branch's minimum", union.Variants[0].Type.GoTypeName())
	}
	if !union.Variants[1].FullyChecked {
		t.Fatalf("variant 1 (type %s) FullyChecked = false; the branch states only `required`, which the presence gate already tests, so selection cannot narrow and the valid document stays rejected",
			union.Variants[1].Type.GoTypeName())
	}
}

// TestOneOfVariantStatingMoreThanSelectionTestsIsNotFullyChecked is the
// over-reach guard for the arm above. Narrowing an ambiguous selection is only
// sound while every branch that matched can be judged; a branch whose type is
// `any` and which says something the presence gate does not test -- here an
// enum -- is not judged by anything, and claiming it was would let the
// narrowing pick a branch while a sibling it never read also matched.
func TestOneOfVariantStatingMoreThanSelectionTestsIsNotFullyChecked(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"oneOf": [
				{"required": ["x"], "properties": {"x": {"type": "integer", "minimum": 10}}},
				{"required": ["y"], "enum": [1, 2]}
			]}
		},
		"required": ["a"]
	}`)

	doc := structNamed(t, ir, "Doc")
	union := oneOfDefFor(doc, "a")
	if union == nil || len(union.Variants) != 2 {
		t.Fatalf("expected a two-variant union on a; got %+v", doc.OneOfs)
	}
	if union.Variants[1].Validatable {
		t.Fatalf("variant 1 (type %s) Validatable = true; the guard below assumes it has no Validate", union.Variants[1].Type.GoTypeName())
	}
	if union.Variants[1].FullyChecked {
		t.Fatalf("variant 1 (type %s) FullyChecked = true; its enum is tested nowhere, so selection cannot claim to have judged the branch",
			union.Variants[1].Type.GoTypeName())
	}
}

// fieldContainsFor is the contains counterpart of itemValidationFor.
func fieldContainsFor(t *testing.T, sd *StructDef, jsonName string) *FieldContainsDef {
	t.Helper()
	for i := range sd.ContainsValidations {
		if sd.ContainsValidations[i].JSONName == jsonName {
			return &sd.ContainsValidations[i]
		}
	}
	t.Fatalf("expected a contains check for %q on %s; got %+v", jsonName, sd.Name, sd.ContainsValidations)
	return nil
}

func containsCheckTypes(def *FieldContainsDef) []string {
	var out []string
	for _, chk := range def.Contains.Checks {
		out = append(out, chk.CheckType)
	}
	return out
}

// TestArrayPropertyContainsIsChecked pins issue #82. The contains machinery was
// complete -- a root array and a $ref'd array definition both enforced
// contains, minContains and maxContains -- but an inline array *property* never
// becomes a named type, so there was no Validate for the check to hang off and
// {"a":[1,2]} was accepted against a contains of {"minimum":10}.
func TestArrayPropertyContainsIsChecked(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type":"array", "contains":{"type":"integer","minimum":10}},
			"b": {"type":"array", "items":{"type":"integer"},
			      "contains":{"type":"integer","minimum":10},
			      "minContains":2, "maxContains":3}
		},
		"required": ["a", "b"]
	}`)

	doc := structNamed(t, ir, "Doc")

	a := fieldContainsFor(t, doc, "a")
	if got := containsCheckTypes(a); !containsString(got, "minimum") || !containsString(got, "type") {
		t.Fatalf("a: contains checks = %v, want minimum and type", got)
	}
	if a.MinContains != nil || a.MaxContains != nil {
		t.Fatalf("a: bounds = %v/%v, want both unset so the default of 1 applies", a.MinContains, a.MaxContains)
	}
	if a.Optional {
		t.Fatalf("a is required; gating its check on key presence would skip it for hand-built values")
	}

	b := fieldContainsFor(t, doc, "b")
	if b.MinContains == nil || *b.MinContains != 2 {
		t.Fatalf("b: minContains = %v, want 2", b.MinContains)
	}
	if b.MaxContains == nil || *b.MaxContains != 3 {
		t.Fatalf("b: maxContains = %v, want 3", b.MaxContains)
	}
}

// TestOptionalArrayPropertyContainsIsGatedOnPresence guards the direction the
// fix could over-reach in. A nil slice is indistinguishable from an empty one
// in Go, and contains rejects the empty array, so an unguarded check would
// reject a document that simply omitted an optional property -- a conforming
// document turned into a failure.
func TestOptionalArrayPropertyContainsIsGatedOnPresence(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"a": {"type":"array", "contains":{"type":"integer","minimum":10}}
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	if def := fieldContainsFor(t, doc, "a"); !def.Optional {
		t.Fatalf("a is not required; its contains check must be gated on the key being present")
	}
	if !doc.NeedsJSONKeys() {
		t.Fatalf("the presence gate reads _jsonKeys, which NeedsJSONKeys has to ask for")
	}
}

// TestNamedArrayPropertyCarriesNoFieldContains is the over-reach guard for the
// fix above. A property that *did* become a named type answers for contains
// through its own Validate, which the struct already dispatches to; a second
// check on the field would count the same elements twice and report the same
// failure twice over -- and would not even compile, since the field is that
// named type rather than the slice the emitted loop ranges over.
//
// Two shapes reach it. A $ref keeps the keyword on the definition, so the
// property schema has no `contains` of its own to read; a multi-valued type
// keeps it inline but gives the property a named wrapper, which is the case the
// Go-type guard in buildFieldContains is there for.
func TestNamedArrayPropertyCarriesNoFieldContains(t *testing.T) {
	for _, tc := range []struct{ name, input string }{
		{"ref", `{
			"title": "Doc",
			"type": "object",
			"properties": {"a": {"$ref": "#/$defs/Bag"}},
			"required": ["a"],
			"$defs": {
				"Bag": {"type":"array", "contains":{"type":"integer","minimum":10}}
			}
		}`},
		{"multi-type", `{
			"title": "Doc",
			"type": "object",
			"properties": {"a": {"type":["array","string"], "contains":{"type":"integer","minimum":10}}},
			"required": ["a"]
		}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ir := generateForItemTest(t, tc.input)
			doc := structNamed(t, ir, "Doc")
			if len(doc.ContainsValidations) != 0 {
				t.Fatalf("a named type validates itself; got a field-level contains too: %+v", doc.ContainsValidations)
			}
			if !hasValidatableField(doc.ValidatableFields, "a") {
				t.Fatalf("a must dispatch to its own type's Validate; otherwise contains is enforced nowhere")
			}
		})
	}
}

// TestVacuousContainsEmitsNoCheck pins two defects the shared contains emitter
// carried. minContains: 0 with no maxContains is satisfied by every array
// whatever the sub-schema says, and emitting for it was wrong twice over:
// {"contains":true,"minContains":0} counted matches into a variable nothing
// read, which Go rejects as "declared and not used" -- generated source that
// does not compile -- and {"contains":false,"minContains":0} returned an error
// unconditionally, rejecting every array against a schema that accepts them all.
func TestVacuousContainsEmitsNoCheck(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"true-min-zero", `"contains":true,"minContains":0`, false},
		{"false-min-zero", `"contains":false,"minContains":0`, false},
		{"checks-min-zero", `"contains":{"type":"integer","minimum":10},"minContains":0`, false},
		{"min-zero-with-max", `"contains":{"type":"integer","minimum":10},"minContains":0,"maxContains":2`, true},
		{"false-default-min", `"contains":false`, true},
		{"checks-default-min", `"contains":{"type":"integer","minimum":10}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ir := generateForItemTest(t, `{
				"title": "Doc",
				"type": "object",
				"properties": {"a": {"type":"array", `+tc.body+`}},
				"required": ["a"]
			}`)
			doc := structNamed(t, ir, "Doc")
			got := len(doc.ContainsValidations) > 0
			if got != tc.want {
				t.Fatalf("emitted a contains check = %v, want %v (%s)", got, tc.want, tc.body)
			}
		})
	}
}

// TestAllOfBranchPropertyNamesIsMerged pins issue #83. #68 made a *parent's*
// propertyNames survive an allOf; a branch's was never read at all, and the
// root type came out as `type Doc any` -- no Validate method, so {"BAD":1}
// could not be rejected.
func TestAllOfBranchPropertyNamesIsMerged(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"allOf": [{"propertyNames": {"pattern": "^[a-z]+$"}}]
	}`)

	doc := structNamed(t, ir, "Doc")
	if doc.PropertyNames == nil {
		t.Fatalf("Doc carries no propertyNames; the branch's was dropped")
	}
	if doc.PropertyNames.Pattern != "^[a-z]+$" {
		t.Fatalf("propertyNames pattern = %q, want %q", doc.PropertyNames.Pattern, "^[a-z]+$")
	}
}

// TestAllOfPropertyNamesMergesBothSides covers the case the issue records as a
// related limitation: parent and branch each state a propertyNames. The length
// bounds keep the tighter of the two; `pattern` cannot be intersected, since
// one regex cannot in general express "matches both" and the emitted check has
// a single slot, so the parent's is kept -- the same single-pattern limitation
// mergeConstraints documents.
func TestAllOfPropertyNamesMergesBothSides(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"propertyNames": {"pattern": "^[a-z]+$", "minLength": 2, "maxLength": 8},
		"allOf": [{"propertyNames": {"pattern": "^x", "minLength": 4, "maxLength": 6}}]
	}`)

	doc := structNamed(t, ir, "Doc")
	if doc.PropertyNames == nil {
		t.Fatalf("Doc carries no propertyNames")
	}
	if doc.PropertyNames.Pattern != "^[a-z]+$" {
		t.Fatalf("pattern = %q, want the parent's %q kept", doc.PropertyNames.Pattern, "^[a-z]+$")
	}
	if doc.PropertyNames.MinLength == nil || *doc.PropertyNames.MinLength != 4 {
		t.Fatalf("minLength = %v, want the tighter bound 4", doc.PropertyNames.MinLength)
	}
	if doc.PropertyNames.MaxLength == nil || *doc.PropertyNames.MaxLength != 6 {
		t.Fatalf("maxLength = %v, want the tighter bound 6", doc.PropertyNames.MaxLength)
	}
}

// TestAllOfBranchObjectKeywordsSurviveWithoutProperties covers the second half
// of #83. Reading propertyNames off a branch is not enough on its own: an allOf
// that contributes no properties used to fall through to `type X any`, which
// carries no Validate, so min/maxProperties, required, dependentRequired and
// dependentSchemas were all dropped there too.
func TestAllOfBranchObjectKeywordsSurviveWithoutProperties(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"allOf": [{"minProperties": 1, "required": ["a"]}]
	}`)

	doc := structNamed(t, ir, "Doc")
	if !containsString(doc.RequiredJSON, "a") {
		t.Fatalf("RequiredJSON = %v, want the branch's required entry", doc.RequiredJSON)
	}
	found := false
	for _, v := range doc.Validations {
		if v.RuleType == "minProperties" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Validations = %+v, want a minProperties rule", doc.Validations)
	}
	// The parent declared "object", so a non-object instance is invalid and the
	// struct must not wave it through.
	if doc.AcceptNonObject {
		t.Fatalf(`AcceptNonObject = true although the schema says "type":"object"`)
	}
}

// TestAllOfWithoutObjectChecksStaysAny is the over-reach guard for the arm
// above. An allOf that merges to an object with nothing to enforce keeps the
// permissive alias: materialising an empty struct for every property-less allOf
// would change the generated API of schemas that gained no check from it.
//
// The type keyword alone is deliberately not enough. A struct built here would
// start rejecting non-object instances of a schema that today accepts them --
// correct per the spec, but a far wider change than the merge gap this arm
// exists to close, and one no part of #83 asks for.
func TestAllOfWithoutObjectChecksStaysAny(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"allOf": [{"type": "object"}]
	}`)

	for _, td := range ir.TypeDefs {
		if sd, ok := td.(*StructDef); ok && sd.Name == "Doc" {
			t.Fatalf("Doc became a struct with nothing to validate: %+v", sd)
		}
	}
	for _, td := range ir.TypeDefs {
		if ad, ok := td.(*AliasDef); ok && ad.Name == "Doc" {
			if ad.Underlying.GoTypeName() != "any" {
				t.Fatalf("Doc underlying = %s, want any", ad.Underlying.GoTypeName())
			}
			return
		}
	}
	t.Fatalf("expected Doc to stay an alias to any; got %v", ir.TypeDefs)
}

// generateForDraft is generateForItemTest with a draft the caller states, so a
// pair of tests can put one schema through both readings of "integer".
func generateForDraft(t *testing.T, input string, draft schema.Draft) *File {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	cfg := Config{PackageName: "testpkg", OmitEmpty: true}
	if draft != schema.DraftUnknown {
		cfg.Draft = draft
	}
	ir, err := New(cfg).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return ir
}

// integerPositionsSchema is the document both integer-decode tests read. Every
// property is a position a schema integer can occupy and reach encoding/json
// through a Go type rather than through a method of its own.
const integerPositionsSchema = `{
	"title": "Doc",
	"type": "object",
	"properties": {
		"req":      {"type":"integer"},
		"opt":      {"type":"integer"},
		"arr":      {"type":"array", "items":{"type":"integer"}},
		"grid":     {"type":"array", "items":{"type":"array", "items":{"type":"integer"}}},
		"mp":       {"type":"object", "additionalProperties":{"type":"integer"}},
		"nullint":  {"type":["integer","null"]},
		"un":       {"oneOf":[{"type":"integer","minimum":10},{"type":"string"}]},
		"namedarr": {"$ref":"#/$defs/Counts"},
		"enm":      {"type":"integer", "enum":[1,2,3]},
		"str":      {"type":"string"},
		"num":      {"type":"number"},
		"free":     {}
	},
	"required": ["req"],
	"$defs": {"Counts": {"type":"array", "items":{"type":"integer"}}}
}`

// TestIntegerPositionsDecodeFloatNotation pins issue #90.
//
// From draft 6 on, a number with a zero fractional part is an integer:
// python-jsonschema and js-ajv both call {"n":1.0} valid against
// {"type":"integer"}, and the official suite's draft6 type.json says the same.
// A document *root* typed integer already agreed -- the alias template decodes
// it through json.Number -- but a struct field was a bare int64 handed to
// encoding/json, which refuses 1.0 outright. So one schema accepted or rejected
// one document depending on where in it the integer sat.
//
// Every position below reaches its int64 through a Go type rather than through
// a method, which is exactly the set that has to move together: fixing the
// scalar field alone would leave the array element rejecting what the field
// accepts, which is the same defect one level down. The three non-integer
// properties are here to say the shadow does not follow anything else.
func TestIntegerPositionsDecodeFloatNotation(t *testing.T) {
	ir := generateForDraft(t, integerPositionsSchema, schema.DraftUnknown)
	doc := structNamed(t, ir, "Doc")

	// The declared field types are untouched: this is a change to how the bytes
	// are read, not to the API of the generated struct.
	for jsonName, wantType := range map[string]string{
		"req":     "int64",
		"opt":     "*int64",
		"arr":     "[]int64",
		"grid":    "[][]int64",
		"mp":      "map[string]int64",
		"nullint": "*int64",
	} {
		field := fieldNamedJSON(t, doc, jsonName)
		if got := field.Type.GoTypeName(); got != wantType {
			t.Fatalf("%s type = %q, want %q -- the decode changes, the type does not", jsonName, got, wantType)
		}
		if field.IntegerDecode == nil {
			t.Fatalf("%s has no integer decode: encoding/json would see the int64 itself and refuse 1.0", jsonName)
		}
	}

	// The shadow has the field's shape with jsonInteger at every leaf, which is
	// what leaves nesting, nils and nulls to encoding/json.
	for jsonName, wantShadow := range map[string]string{
		"req":     "jsonInteger",
		"opt":     "*jsonInteger",
		"arr":     "[]jsonInteger",
		"grid":    "[][]jsonInteger",
		"mp":      "map[string]jsonInteger",
		"nullint": "*jsonInteger",
	} {
		def := fieldNamedJSON(t, doc, jsonName).IntegerDecode
		if got := def.ShadowType.GoTypeName(); got != wantShadow {
			t.Fatalf("%s shadow = %q, want %q -- a shadow of another shape decodes a different document", jsonName, got, wantShadow)
		}
		if !strings.Contains(def.Convert, "_iv") {
			t.Fatalf("%s conversion %q does not read the decoded shadow", jsonName, def.Convert)
		}
	}

	// A oneOf branch is selected by whether the candidate decodes, so an
	// integer branch that read numbers differently from the rest of the file
	// reported "no matching oneOf variant" for a document it accepts.
	var intVariant *OneOfVariant
	for i := range doc.OneOfs {
		for j := range doc.OneOfs[i].Variants {
			if doc.OneOfs[i].Variants[j].Type.GoTypeName() == "int64" {
				intVariant = &doc.OneOfs[i].Variants[j]
			}
		}
	}
	if intVariant == nil {
		t.Fatalf("no int64 oneOf variant on Doc: %+v", doc.OneOfs)
	}
	if intVariant.IntegerDecode == nil {
		t.Fatalf("the integer oneOf branch decodes without the shadow, so selection gates on a different reading of the number than the rest of the file")
	}

	// A named container of integers has its own UnmarshalJSON, and that is where
	// its leaves are reached.
	counts := aliasNamed(ir, "Counts")
	if counts == nil {
		t.Fatalf("expected an alias Counts; got %v", ir.TypeDefs)
	}
	if counts.IntegerDecode == nil {
		t.Fatalf("Counts (%s) decodes its elements as bare int64", counts.Underlying.GoTypeName())
	}

	// An integer enum is a named int64 with no UnmarshalJSON at all, so it
	// refused the notation exactly as a bare field did.
	enm := enumNamed(t, ir, "DocEnm")
	if !enm.IntegerToken {
		t.Fatalf("DocEnm carries no integer decode, so 1.0 never reaches its members")
	}

	// Nothing else acquires one. A string, a float and an untyped value hold no
	// int64, and attaching the shadow to them would either not compile or would
	// change how a number that is *not* an integer is read.
	for _, jsonName := range []string{"str", "num", "free"} {
		if def := fieldNamedJSON(t, doc, jsonName).IntegerDecode; def != nil {
			t.Fatalf("%s has an integer decode (%s) -- it holds no int64", jsonName, def.ShadowType.GoTypeName())
		}
	}
}

// TestDraft4IntegerPositionsKeepTheStrictToken is the other direction, and it
// is the half that a fix aimed only at draft 6 would get wrong.
//
// Draft 4 defines an integer as a number written without a fraction or an
// exponent, so 1.0 is *not* an integer there; the suite carries it as
// draft4/optional/zeroTerminatedFloats.json, expecting invalid. (Bowtie is not
// the authority here: python-jsonschema agrees with the suite and js-ajv does
// not, so the two disagree and the suite decides.) A plain int64 handed to
// encoding/json already refuses the notation, which is the right answer, so
// every position must be left exactly as it was -- accepting 1.0 under draft 4
// would be a new defect, not a fix.
func TestDraft4IntegerPositionsKeepTheStrictToken(t *testing.T) {
	for _, draft := range []schema.Draft{schema.Draft03, schema.Draft04} {
		ir := generateForDraft(t, integerPositionsSchema, draft)
		doc := structNamed(t, ir, "Doc")
		for i := range doc.Fields {
			if doc.Fields[i].IntegerDecode != nil {
				t.Fatalf("draft %v: %s decodes 1.0 as an integer, which this draft says it is not", draft, doc.Fields[i].JSONName)
			}
		}
		if doc.AdditionalProperties != nil && doc.AdditionalProperties.IntegerDecode != nil {
			t.Fatalf("draft %v: the overflow map decodes 1.0 as an integer", draft)
		}
		for i := range doc.OneOfs {
			for j := range doc.OneOfs[i].Variants {
				if doc.OneOfs[i].Variants[j].IntegerDecode != nil {
					t.Fatalf("draft %v: a oneOf branch decodes 1.0 as an integer", draft)
				}
			}
		}
		if counts := aliasNamed(ir, "Counts"); counts == nil || counts.IntegerDecode != nil {
			t.Fatalf("draft %v: Counts was not generated, or decodes 1.0 as an integer", draft)
		}
		if enm := enumNamed(t, ir, "DocEnm"); enm.IntegerToken {
			t.Fatalf("draft %v: DocEnm decodes 1.0 as one of its members", draft)
		}
	}
}

// TestTypedAdditionalPropertiesIntegerValuesDecodeFloatNotation covers the one
// integer position that is not a struct field, a container under one, or a
// named type: the overflow map of an object whose values are all typed by
// additionalProperties. It is reached by a hand-written per-key decode rather
// than by the aux, so it needs its own arm and its own guard.
func TestTypedAdditionalPropertiesIntegerValuesDecodeFloatNotation(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"additionalProperties": {"type":"integer"}
	}`)

	doc := structNamed(t, ir, "Doc")
	if doc.AdditionalProperties == nil {
		t.Fatalf("Doc has no overflow map: %+v", doc)
	}
	if got := doc.AdditionalProperties.ValueType.GoTypeName(); got != "int64" {
		t.Fatalf("overflow value type = %q, want int64", got)
	}
	if doc.AdditionalProperties.IntegerDecode == nil {
		t.Fatalf("the overflow map decodes its values as bare int64, so 1.0 is refused for a key the schema types integer")
	}
	if got := doc.AdditionalProperties.IntegerDecode.ShadowType.GoTypeName(); got != "jsonInteger" {
		t.Fatalf("overflow shadow = %q, want jsonInteger", got)
	}
}

// TestIntegerDecodeNeedsTheSharedHelpers pins the dependency the emitted code
// has on the helper file. jsonInteger and its rebuilders are package-level, so
// a file that names them without the package declaring them does not compile --
// and the helper file is written only for the set a File reports.
func TestIntegerDecodeNeedsTheSharedHelpers(t *testing.T) {
	ir := generateForDraft(t, integerPositionsSchema, schema.DraftUnknown)
	if !ir.Helpers().Integer {
		t.Fatalf("the file decodes integers through jsonInteger but does not ask for it: %+v", ir.Helpers())
	}
	plain := generateForItemTest(t, `{"title":"Doc","type":"object","properties":{"s":{"type":"string"}}}`)
	if plain.Helpers().Integer {
		t.Fatalf("a file with no integer in it asks for the integer helpers")
	}
}

// enumNamed returns the EnumDef with the given name.
func enumNamed(t *testing.T, ir *File, name string) *EnumDef {
	t.Helper()
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*EnumDef); ok && d.Name == name {
			return d
		}
	}
	t.Fatalf("expected an enum %s; got %v", name, ir.TypeDefs)
	return nil
}

// overflowValidationFor returns the per-value checks a struct carries for its
// additionalProperties overflow map, or fails. The overflow map is not a
// declared property, so it is found by the Go field it lands in rather than by
// a JSON name.
func overflowValidationFor(t *testing.T, sd *StructDef) *ItemValidationDef {
	t.Helper()
	for i := range sd.ItemValidations {
		if sd.ItemValidations[i].FieldName == "AdditionalProperties" {
			return &sd.ItemValidations[i]
		}
	}
	t.Fatalf("expected per-value checks for the overflow map on %s; got %+v", sd.Name, sd.ItemValidations)
	return nil
}

// TestOverflowAdditionalPropertiesValuesAreChecked pins the fix for #92. A
// schema-valued `additionalProperties` sitting *beside* declared properties
// governs the keys those do not claim, and those land in the overflow map. The
// map is typed, so a value of the wrong JSON type dies in the decoder, but
// nothing checked the subschema's own keywords:
//
//	{"properties":{"alpha":{"type":"string"}},
//	 "additionalProperties":{"type":"integer","minimum":5}}
//
// accepted {"alpha":"aa","zzExtra":1}. patternProperties in the same position
// was enforced, constraints and all, so the position was the gap rather than
// the keyword.
//
// The whole-object form -- no `properties`, no `patternProperties` -- is a
// different construct that becomes a Go map, and its values have been checked
// since #84.
func TestOverflowAdditionalPropertiesValuesAreChecked(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {"alpha": {"type": "string"}},
		"additionalProperties": {"type": "integer", "minimum": 5}
	}`)

	doc := structNamed(t, ir, "Doc")
	def := overflowValidationFor(t, doc)
	if len(def.Levels) != 1 || !def.Levels[0].IsMap {
		t.Fatalf("overflow checks = %+v, want a single map level", def.Levels)
	}
	if got := itemRuleTypes(def, 0); !containsString(got, "minimum") {
		t.Fatalf("overflow rules = %v, want the subschema's minimum", got)
	}
	// The error path names the keyword that constrains the value, since the
	// overflow map has no JSON property name of its own to report under.
	if def.PathName != "additionalProperties" {
		t.Fatalf("PathName = %q, want %q", def.PathName, "additionalProperties")
	}
}

// TestOverflowAdditionalPropertiesBesidePatternPropertiesIsChecked covers the
// sibling position of the arm above. `patternProperties` claims the keys its
// regexes match and `additionalProperties` claims the rest, so the two occupy
// one struct and the overflow map holds exactly what the second one governs.
// The keyword reaching one position and not the other is the shape the fix has
// to avoid, not a place to stop.
func TestOverflowAdditionalPropertiesBesidePatternPropertiesIsChecked(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"patternProperties": {"^a": {"type": "string", "minLength": 3}},
		"additionalProperties": {"type": "integer", "minimum": 5}
	}`)

	doc := structNamed(t, ir, "Doc")
	def := overflowValidationFor(t, doc)
	if got := itemRuleTypes(def, 0); !containsString(got, "minimum") {
		t.Fatalf("overflow rules = %v, want the subschema's minimum", got)
	}
	// The patternProperties half must survive the addition: the two keywords
	// speak about disjoint key sets and neither answers for the other.
	if !doc.HasPatternPropertyValidation() {
		t.Fatalf("patternProperties lost their checks: %+v", doc.PatternProperties)
	}
}

// TestOverflowAdditionalPropertiesNamedValueTypeIsValidated covers the second
// half of #92. When the value subschema describes an object it is materialized
// as a named type carrying its own Validate, and that call is the whole of what
// enforces the subschema. Nothing dispatched to it: ValidatableFields only
// reaches *declared* properties, and the overflow map is not one, so
// {"additionalProperties":{"type":"object","required":["x"]}} beside a declared
// property accepted an overflow value of {}.
func TestOverflowAdditionalPropertiesNamedValueTypeIsValidated(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {"alpha": {"type": "string"}},
		"additionalProperties": {
			"type": "object",
			"properties": {"x": {"type": "integer", "minimum": 5}},
			"required": ["x"]
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	def := overflowValidationFor(t, doc)
	if len(def.Levels) != 1 {
		t.Fatalf("overflow checks = %+v, want a single map level", def.Levels)
	}
	if !def.Levels[0].CallValidate {
		t.Fatalf("overflow value %q carries no Validate dispatch: %+v -- its subschema is enforced nowhere else",
			def.Levels[0].ElemTypeName, def.Levels[0])
	}
}

// TestDeclaredPropertyElementIsNotValidatedTwice is the over-reach guard for
// the arm above, which reaches a named overflow value's Validate by saying the
// definition owns its outermost element. That permission has to stay on the
// overflow map. A *declared* property's outermost element is already dispatched
// to by ValidatableFields, so granting it here as well would call the element
// type's Validate twice for every element and report the same failure twice
// over.
func TestDeclaredPropertyElementIsNotValidatedTwice(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"$defs": {"Item": {"type": "object", "properties": {"x": {"type": "integer", "minimum": 5}}, "required": ["x"]}},
		"properties": {
			"arr": {"type": "array", "items": {"$ref": "#/$defs/Item"}},
			"alpha": {"type": "string"}
		},
		"additionalProperties": {
			"type": "object",
			"properties": {"y": {"type": "integer", "minimum": 5}},
			"required": ["y"]
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	if !hasValidatableField(doc.ValidatableFields, "arr") {
		t.Fatalf("arr is never validated by Doc: %+v", doc.ValidatableFields)
	}
	for i := range doc.ItemValidations {
		def := &doc.ItemValidations[i]
		if def.FieldName == "AdditionalProperties" {
			continue
		}
		if def.OwnsOutermost {
			t.Fatalf("%q claims its outermost element although ValidatableFields dispatches for it: %+v",
				def.FieldName, def)
		}
		if len(def.Levels) > 0 && def.Levels[0].CallValidate {
			t.Fatalf("%q dispatches to its element's Validate at level 0 as well as through ValidatableFields: %+v -- every element would be checked twice",
				def.FieldName, def.Levels[0])
		}
	}
	// And the overflow map, which nothing else reaches, keeps its own call.
	overflow := overflowValidationFor(t, doc)
	if !overflow.OwnsOutermost || !overflow.Levels[0].CallValidate {
		t.Fatalf("overflow values lost their Validate dispatch: %+v", overflow)
	}
}

// TestDeclaredPropertyKeepsItsOwnChecksBesideOverflowChecks is the other
// over-reach guard. `additionalProperties` speaks only about the keys
// `properties` and `patternProperties` do not claim, so a declared property
// must keep being judged by its own subschema and must not acquire the overflow
// subschema's rules. A definition that ranged the whole object rather than the
// overflow map would reject {"alpha":"aa"} against a schema that allows it.
func TestDeclaredPropertyKeepsItsOwnChecksBesideOverflowChecks(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {"alpha": {"type": "string", "minLength": 2}},
		"additionalProperties": {"type": "integer", "minimum": 5}
	}`)

	doc := structNamed(t, ir, "Doc")
	if got := fieldRuleTypes(doc, "alpha"); !containsString(got, "minLength") {
		t.Fatalf("alpha rules = %v, want its own minLength", got)
	}
	if got := fieldRuleTypes(doc, "alpha"); containsString(got, "minimum") {
		t.Fatalf("alpha acquired the additionalProperties minimum: %v", got)
	}
	def := overflowValidationFor(t, doc)
	if def.FieldName != "AdditionalProperties" {
		t.Fatalf("overflow checks range %q, want the overflow map", def.FieldName)
	}
}

// dependentSchemaFor returns a struct's dependentSchemas entry for one trigger.
func dependentSchemaFor(t *testing.T, sd *StructDef, trigger string) *DependentSchemaConstraint {
	t.Helper()
	for i := range sd.DependentSchemas {
		if sd.DependentSchemas[i].TriggerKey == trigger {
			return &sd.DependentSchemas[i]
		}
	}
	t.Fatalf("expected a dependentSchemas entry for %q on %s; got %+v", trigger, sd.Name, sd.DependentSchemas)
	return nil
}

// TestDependentSchemaBranchConstrainsShapeNotOnlyPresence pins the fix for #93.
// A dependentSchemas branch is an ordinary subschema and may carry any keyword,
// but everything in one except `required` was dropped, so the dependency fired
// on presence and never on shape:
//
//	{"properties":{"alpha":{"type":"string"},"bravo":{"type":"integer"}},
//	 "dependentSchemas":{"alpha":{"properties":{"bravo":{"minimum":5}},
//	                              "required":["bravo"]}}}
//
// accepted {"alpha":"aa","bravo":1}. The branch is the same definition an
// object-level `then` carries, since it is the same thing gated on a key's
// presence rather than on an `if`.
func TestDependentSchemaBranchConstrainsShapeNotOnlyPresence(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {"alpha": {"type": "string"}, "bravo": {"type": "integer"}},
		"dependentSchemas": {
			"alpha": {"properties": {"bravo": {"minimum": 5}}, "required": ["bravo"]}
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	dep := dependentSchemaFor(t, doc, "alpha")
	if !containsString(dep.RequiredProps, "bravo") {
		t.Fatalf("RequiredProps = %v, want the branch's required entry", dep.RequiredProps)
	}
	if dep.Branch == nil {
		t.Fatalf("branch carries no shape constraint: %+v -- its minimum is enforced nowhere", dep)
	}
	if len(dep.Branch.Properties) != 1 || dep.Branch.Properties[0].JSONName != "bravo" {
		t.Fatalf("branch properties = %+v, want one entry for bravo", dep.Branch.Properties)
	}
	var kinds []string
	for _, c := range dep.Branch.Properties[0].Checks {
		kinds = append(kinds, c.Kind)
	}
	if !containsString(kinds, "minimum") {
		t.Fatalf("bravo checks = %v, want the branch's minimum", kinds)
	}
	// `required` is answered by RequiredProps; carrying it on the branch as
	// well would emit the same presence test twice.
	if len(dep.Branch.RequiredKeys) != 0 {
		t.Fatalf("branch RequiredKeys = %v, want them left to RequiredProps", dep.Branch.RequiredKeys)
	}
	// The struct needs the raw JSON to judge the branch against: the checks run
	// on the decoded property value, not on the Go field.
	if !doc.NeedsRawProps() {
		t.Fatalf("Doc does not keep _jsonRawProps, so the branch has nothing to read")
	}
}

// TestDependentSchemaBranchReachesAnUndeclaredProperty covers the half of #93
// the check it replaces could never have reached. That check read the overflow
// map, which holds only the keys the struct does not declare, so a branch
// constraining a *declared* property was tested against a map that could not
// hold it. Reading the raw JSON instead makes the two cases one, and this pins
// the undeclared side so the move does not trade one for the other.
func TestDependentSchemaBranchReachesAnUndeclaredProperty(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {"alpha": {"type": "string"}},
		"dependentSchemas": {
			"alpha": {"properties": {"zzExtra": {"type": "integer", "minimum": 5}}}
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	dep := dependentSchemaFor(t, doc, "alpha")
	if dep.Branch == nil || len(dep.Branch.Properties) != 1 {
		t.Fatalf("branch = %+v, want one property constraint for zzExtra", dep.Branch)
	}
	if dep.Branch.Properties[0].JSONName != "zzExtra" {
		t.Fatalf("branch property = %q, want zzExtra", dep.Branch.Properties[0].JSONName)
	}
}

// TestDependentSchemaBranchStaysUnderItsTrigger is the over-reach guard for the
// two arms above. A dependentSchemas branch binds only while its trigger key is
// present, so its constraints must stay on the entry that names the trigger.
// Folded into the struct's own rules -- or into an ObjectConditional, which
// binds as soon as its condition holds -- they would reject {"bravo":1}, a
// document the schema allows because `alpha` is absent.
func TestDependentSchemaBranchStaysUnderItsTrigger(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {"alpha": {"type": "string"}, "bravo": {"type": "integer"}},
		"dependentSchemas": {"alpha": {"properties": {"bravo": {"minimum": 5}}}}
	}`)

	doc := structNamed(t, ir, "Doc")
	if got := fieldRuleTypes(doc, "bravo"); containsString(got, "minimum") {
		t.Fatalf("bravo rules = %v -- the branch's minimum became unconditional", got)
	}
	if len(doc.ObjectConditionals) != 0 {
		t.Fatalf("ObjectConditionals = %+v -- the branch escaped its trigger", doc.ObjectConditionals)
	}
	dep := dependentSchemaFor(t, doc, "alpha")
	if dep.Branch == nil {
		t.Fatalf("branch carries no shape constraint: %+v", dep)
	}
}

// TestDependentSchemaBranchKeywordItCannotExpressIsDropped is the second
// over-reach guard. The branch is held to the same bar an object-level `then`
// is: a keyword the evaluator cannot express is dropped, so the emitted check
// demands a subset of what the branch says and refuses only documents the
// schema refuses too. Guessing at one instead would reject conforming data,
// which is worse than the missing check.
//
// `items` under a branch property is such a keyword, and a $ref on the branch
// itself is the stronger case: before draft 2019-09 a $ref replaces the schema
// object it sits in, so reading its siblings at all would enforce something the
// schema never said.
func TestDependentSchemaBranchKeywordItCannotExpressIsDropped(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {"alpha": {"type": "string"}},
		"dependentSchemas": {
			"alpha": {"properties": {
				"arr": {"items": {"type": "integer"}},
				"bravo": {"minimum": 5}
			}}
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	dep := dependentSchemaFor(t, doc, "alpha")
	if dep.Branch == nil {
		t.Fatalf("the whole branch was dropped over one unexpressible property: %+v", dep)
	}
	for _, p := range dep.Branch.Properties {
		if p.JSONName == "arr" {
			t.Fatalf("arr acquired checks from an unexpressible subschema: %+v", p.Checks)
		}
	}
	if len(dep.Branch.Properties) != 1 || dep.Branch.Properties[0].JSONName != "bravo" {
		t.Fatalf("branch properties = %+v, want only bravo", dep.Branch.Properties)
	}

	refIR := generateForItemTest(t, `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"title": "Doc",
		"type": "object",
		"definitions": {"Other": {"type": "object"}},
		"properties": {"alpha": {"type": "string"}},
		"dependentSchemas": {"alpha": {
			"$ref": "#/definitions/Other",
			"properties": {"bravo": {"minimum": 5}},
			"required": ["bravo"]
		}}
	}`)
	refDoc := structNamed(t, refIR, "Doc")
	refDep := dependentSchemaFor(t, refDoc, "alpha")
	if refDep.Branch != nil {
		t.Fatalf("a branch carrying $ref was read in part: %+v", refDep.Branch)
	}
	if !containsString(refDep.RequiredProps, "bravo") {
		t.Fatalf("RequiredProps = %v, want the branch's own required entry kept", refDep.RequiredProps)
	}
}

// fieldTupleFor is the prefixItems counterpart of fieldContainsFor.
func fieldTupleFor(t *testing.T, sd *StructDef, jsonName string) *FieldTupleDef {
	t.Helper()
	for i := range sd.TupleValidations {
		if sd.TupleValidations[i].JSONName == jsonName {
			return &sd.TupleValidations[i]
		}
	}
	t.Fatalf("expected per-position checks for %q on %s; got %+v", jsonName, sd.Name, sd.TupleValidations)
	return nil
}

// fieldUnevalItemsFor is the unevaluatedItems counterpart of fieldContainsFor.
func fieldUnevalItemsFor(t *testing.T, sd *StructDef, jsonName string) *FieldUnevalItemsDef {
	t.Helper()
	for i := range sd.UnevalItemsValidations {
		if sd.UnevalItemsValidations[i].JSONName == jsonName {
			return &sd.UnevalItemsValidations[i]
		}
	}
	t.Fatalf("expected an unevaluatedItems check for %q on %s; got %+v",
		jsonName, sd.Name, sd.UnevalItemsValidations)
	return nil
}

// TestArrayPropertyPrefixItemsPositionsAreChecked pins the first half of issue
// #94. The per-position machinery was complete for a named array type -- the
// complex_tuple golden checks three positions -- but an inline array *property*
// never becomes one, so a prefixItems entry contributed nothing but a length
// and {"arr":["a",1]} was accepted with both positions violated.
//
// Each position is asked about twice over, because a position carries two
// separable things: the type it names, and whatever else its sub-schema says.
// A check that read only the type would leave minLength and minimum enforced
// nowhere, which is the shape the issue's own reproducer takes.
func TestArrayPropertyPrefixItemsPositionsAreChecked(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"arr": {"type":"array", "prefixItems":[
				{"type":"string","minLength":2},
				{"type":"integer","minimum":5}
			]}
		},
		"required": ["arr"]
	}`)

	doc := structNamed(t, ir, "Doc")

	// A tuple has no homogeneous Go element type, so the field stays []any and
	// the positions are judged in Validate rather than by the decoder.
	if got := fieldNamedJSON(t, doc, "arr").Type.GoTypeName(); got != "[]any" {
		t.Fatalf("arr type = %s, want []any", got)
	}

	def := fieldTupleFor(t, doc, "arr")
	if len(def.Items) != 2 {
		t.Fatalf("arr: %d positions, want 2: %+v", len(def.Items), def.Items)
	}
	// Both positions carry a bound beside their type, so each has to become a
	// named type of its own -- a bare JSONType would check the type and drop
	// minLength and minimum.
	for i, want := range []string{"DocArrItem0", "DocArrItem1"} {
		if def.Items[i].TypeName != want {
			t.Fatalf("position %d: TypeName = %q (JSONType %q), want %q -- a type-only check drops the position's bound",
				i, def.Items[i].TypeName, def.Items[i].JSONType, want)
		}
	}
	// And those types must actually carry the bounds.
	item0 := aliasNamed(ir, "DocArrItem0")
	if item0 == nil {
		t.Fatalf("expected a DocArrItem0 alias; got %v", ir.TypeDefs)
	}
	if got := aliasRuleTypes(item0); !containsString(got, "minLength") {
		t.Fatalf("DocArrItem0 rules = %v, want minLength", got)
	}
	item1 := aliasNamed(ir, "DocArrItem1")
	if item1 == nil {
		t.Fatalf("expected a DocArrItem1 alias; got %v", ir.TypeDefs)
	}
	if got := aliasRuleTypes(item1); !containsString(got, "minimum") {
		t.Fatalf("DocArrItem1 rules = %v, want minimum", got)
	}
}

func aliasRuleTypes(ad *AliasDef) []string {
	var out []string
	for _, r := range ad.Validations {
		out = append(out, r.RuleType)
	}
	return out
}

// TestTupleWithSiblingItemsKeepsAnyElements pins the second half of issue #94,
// which is the worse half: a *valid* document could not be decoded at all.
//
// In 2020-12 a schema-valued `items` beside prefixItems governs only the
// positions past the prefix. Reading it as the element schema typed the field
// []bool, so {"arr":["aa",7,true]} -- which the schema accepts -- died in
// encoding/json before any check ran.
//
// The tail is still a constraint, so it is checked at the index it starts from
// rather than dropped along with the element type. Retyping the field is a
// change to the API of the generated code, and it is the change correctness
// requires: no homogeneous Go slice can hold a heterogeneous tuple.
func TestTupleWithSiblingItemsKeepsAnyElements(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"arr": {"type":"array",
			        "prefixItems":[{"type":"string"},{"type":"integer"}],
			        "items":{"type":"boolean"}}
		},
		"required": ["arr"]
	}`)

	doc := structNamed(t, ir, "Doc")
	if got := fieldNamedJSON(t, doc, "arr").Type.GoTypeName(); got != "[]any" {
		t.Fatalf("arr type = %s, want []any -- the sibling items describes the tail, not the elements, and %s cannot decode the prefix",
			got, got)
	}

	def := fieldTupleFor(t, doc, "arr")
	if len(def.Items) != 2 {
		t.Fatalf("arr: %d positions, want 2", len(def.Items))
	}
	if def.Items[0].JSONType != "string" || def.Items[1].JSONType != "integer" {
		t.Fatalf("positions = %+v, want string then integer", def.Items)
	}
	if def.Tail == nil {
		t.Fatalf("arr: no tail check; items past the prefix must still be booleans")
	}
	if def.Tail.JSONType != "boolean" {
		t.Fatalf("arr tail = %+v, want a boolean check", def.Tail)
	}
	if def.TupleTailStart() != 2 {
		t.Fatalf("arr tail starts at %d, want 2", def.TupleTailStart())
	}
}

// TestTupleItemsFalseBeyondStatedMaxItems covers the one arrangement where the
// length bound cannot stand in for a false tail. "items": false beside a tuple
// normally becomes an implicit maxItems of the prefix length, but that
// inference only fires when the schema states no maxItems of its own -- so a
// stated bound wider than the prefix disabled the keyword outright, and
// {"prefixItems":[a,b],"items":false,"maxItems":5} accepted a third element.
func TestTupleItemsFalseBeyondStatedMaxItems(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"arr": {"type":"array",
			        "prefixItems":[{"type":"string"},{"type":"integer"}],
			        "items": false, "maxItems": 5}
		},
		"required": ["arr"]
	}`)

	def := fieldTupleFor(t, structNamed(t, ir, "Doc"), "arr")
	if def.Tail == nil || !def.Tail.IsFalse {
		t.Fatalf("arr tail = %+v, want a false tail: maxItems 5 leaves positions 2..4 that items:false forbids", def.Tail)
	}
}

// TestTupleItemsFalseWithinLengthBoundEmitsNoTail is the over-reach guard for
// the test above. Where the implicit maxItems does fire -- or where the stated
// one is already no wider than the prefix -- a false tail would report the same
// rejection a second time, under a worse message.
func TestTupleItemsFalseWithinLengthBoundEmitsNoTail(t *testing.T) {
	for _, tc := range []struct{ name, arr string }{
		{"implicit", `{"type":"array","prefixItems":[{"type":"string"},{"type":"integer"}],"items":false}`},
		{"stated-equal", `{"type":"array","prefixItems":[{"type":"string"},{"type":"integer"}],"items":false,"maxItems":2}`},
		{"stated-narrower", `{"type":"array","prefixItems":[{"type":"string"},{"type":"integer"}],"items":false,"maxItems":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ir := generateForItemTest(t, `{
				"title": "Doc",
				"type": "object",
				"properties": {"arr": `+tc.arr+`},
				"required": ["arr"]
			}`)
			def := fieldTupleFor(t, structNamed(t, ir, "Doc"), "arr")
			if def.Tail != nil {
				t.Fatalf("arr carries a false tail as well as a length bound: %+v", def.Tail)
			}
		})
	}
}

// TestNamedTuplePropertyCarriesNoFieldTuple is the over-reach guard for the
// per-position field checks. A property that became a named type answers for
// its positions through that type's own Validate, which the struct already
// dispatches to; a second set of checks on the field would report every failure
// twice, and would not compile besides -- the field is that named type, not the
// []any the emitted loop ranges over.
//
// Two shapes reach it, and only the second tests the guard. A $ref keeps
// prefixItems on the definition, so the property schema has no positions of its
// own to read and nothing would be emitted whatever the guard said. A
// multi-valued type keeps them inline and still gives the property a named
// wrapper: there the schema says "emit" and only the Go type says "do not",
// which is the case buildFieldTuple's guard exists for.
func TestNamedTuplePropertyCarriesNoFieldTuple(t *testing.T) {
	t.Run("ref", func(t *testing.T) {
		ir := generateForItemTest(t, `{
			"title": "Doc",
			"type": "object",
			"properties": {"arr": {"$ref": "#/$defs/Pair"}},
			"required": ["arr"],
			"$defs": {
				"Pair": {"type":"array","prefixItems":[{"type":"string"},{"type":"integer"}]}
			}
		}`)

		doc := structNamed(t, ir, "Doc")
		if len(doc.TupleValidations) != 0 {
			t.Fatalf("a named type validates its own positions; got field-level ones too: %+v", doc.TupleValidations)
		}
		if !hasValidatableField(doc.ValidatableFields, "arr") {
			t.Fatalf("arr must dispatch to Pair.Validate; otherwise its positions are checked nowhere")
		}
		pair := aliasNamed(ir, "Pair")
		if pair == nil || len(pair.TupleItems) != 2 {
			t.Fatalf("Pair must carry the two positions itself; got %+v", pair)
		}
	})

	t.Run("multi-type", func(t *testing.T) {
		ir := generateForItemTest(t, `{
			"title": "Doc",
			"type": "object",
			"properties": {
				"arr": {"type":["array","string"],
				        "prefixItems":[{"type":"string"},{"type":"integer"}]}
			},
			"required": ["arr"]
		}`)

		doc := structNamed(t, ir, "Doc")
		if got := fieldNamedJSON(t, doc, "arr").Type.GoTypeName(); got == "[]any" {
			t.Fatalf("arr stayed []any; this case only tests the guard while the property is a named wrapper")
		}
		if len(doc.TupleValidations) != 0 {
			t.Fatalf("arr is a named wrapper, not a slice: a field-level position loop would range over %s and not compile: %+v",
				fieldNamedJSON(t, doc, "arr").Type.GoTypeName(), doc.TupleValidations)
		}
		if !hasValidatableField(doc.ValidatableFields, "arr") {
			t.Fatalf("arr must dispatch to its wrapper's Validate; otherwise its positions are checked nowhere")
		}
	})
}

// TestPreTwentyTwentyDraftStillIgnoresPrefixItems is the over-reach guard for
// reading prefixItems under a document that states no dialect. Treating an
// unstated dialect as a modern one is what makes the issue's own reproducers
// work, but a document that *does* declare draft-07 has no prefixItems keyword
// and must keep ignoring it -- the field stays typed from `items`, and no
// position is checked.
func TestPreTwentyTwentyDraftStillIgnoresPrefixItems(t *testing.T) {
	ir := generateForItemTest(t, `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"title": "Doc",
		"type": "object",
		"properties": {
			"arr": {"type":"array",
			        "prefixItems":[{"type":"string"},{"type":"integer"}],
			        "items":{"type":"boolean"}}
		},
		"required": ["arr"]
	}`)

	doc := structNamed(t, ir, "Doc")
	if len(doc.TupleValidations) != 0 {
		t.Fatalf("draft-07 has no prefixItems; got per-position checks: %+v", doc.TupleValidations)
	}
	if got := fieldNamedJSON(t, doc, "arr").Type.GoTypeName(); got != "[]bool" {
		t.Fatalf("arr type = %s, want []bool -- under draft-07 `items` governs every element", got)
	}
}

// TestArrayPropertyUnevaluatedItemsSchemaIsChecked pins the second half of
// issue #95. Only unevaluatedItems: false was honoured on a property; a
// sub-schema was dropped entirely, so {"arr":["a","b"]} was accepted where
// index 1 had to be an integer.
func TestArrayPropertyUnevaluatedItemsSchemaIsChecked(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"arr": {"type":"array",
			        "prefixItems":[{"type":"string"}],
			        "unevaluatedItems":{"type":"integer"}}
		},
		"required": ["arr"]
	}`)

	def := fieldUnevalItemsFor(t, structNamed(t, ir, "Doc"), "arr")
	if def.Def.IsForbidden {
		t.Fatalf("arr: the sub-schema form was read as a rejection: %+v", def.Def)
	}
	if def.Def.ValueType != "integer" {
		t.Fatalf("arr: unevaluatedItems value type = %q, want integer", def.Def.ValueType)
	}
	if def.Def.EvaluatedCount != 1 {
		t.Fatalf("arr: evaluated count = %d, want 1 (the one prefixItems position)", def.Def.EvaluatedCount)
	}
}

// TestArrayPropertyUnevaluatedItemsSeesThroughAllOf pins the first half of
// issue #95. An allOf branch's prefixItems marked nothing evaluated on a
// property, so unevaluatedItems saw an empty evaluation set and
// {"arr":["a","b"]} was accepted with index 1 unevaluated and forbidden.
//
// Two routes answer it, and which one a schema takes turns on whether the
// runtime annotation evaluator can model the subtree.
//
// The evaluator is preferred where it applies, since it interprets the schema
// rather than counting it. Where a branch states a keyword outside the
// evaluator's set the schema falls to the static count instead, which is exact
// for an allOf for a reason worth stating: an allOf branch has to match, so
// what it evaluates is evaluated for every value and no runtime choice enters.
// Both routes must reject; neither may leave the property unchecked.
func TestArrayPropertyUnevaluatedItemsSeesThroughAllOf(t *testing.T) {
	t.Run("modelled-by-the-evaluator", func(t *testing.T) {
		ir := generateForItemTest(t, `{
			"title": "Doc",
			"type": "object",
			"properties": {
				"arr": {"type":"array",
				        "allOf":[{"prefixItems":[{"type":"string"}]}],
				        "unevaluatedItems": false}
			},
			"required": ["arr"]
		}`)

		doc := structNamed(t, ir, "Doc")
		var wrapper *AnnotationSchemaDef
		for _, td := range ir.TypeDefs {
			if d, ok := td.(*AnnotationSchemaDef); ok && d.Name == "DocArr" {
				wrapper = d
			}
		}
		if wrapper == nil {
			t.Fatalf("expected a DocArr annotation wrapper; got %v", ir.TypeDefs)
		}
		// The branch's prefixItems has to reach the node literal, or the
		// evaluator marks nothing evaluated and the check is vacuous.
		if !strings.Contains(wrapper.NodeLiteral, "PrefixItems") ||
			!strings.Contains(wrapper.NodeLiteral, "AllOf") {
			t.Fatalf("DocArr node literal lost the allOf's prefixItems:\n%s", wrapper.NodeLiteral)
		}
		if !hasValidatableField(doc.ValidatableFields, "arr") {
			t.Fatalf("Doc.Validate does not call arr.Validate; the wrapper's schema is interpreted for no one")
		}
	})

	t.Run("static-count", func(t *testing.T) {
		// minLength is outside the evaluator's keyword set, so this subtree
		// cannot be modelled and the static count is what has to be right.
		ir := generateForItemTest(t, `{
			"title": "Doc",
			"type": "object",
			"properties": {
				"arr": {"type":"array",
				        "allOf":[{"prefixItems":[{"type":"string","minLength":2}]}],
				        "unevaluatedItems": false}
			},
			"required": ["arr"]
		}`)

		def := fieldUnevalItemsFor(t, structNamed(t, ir, "Doc"), "arr")
		if !def.Def.IsForbidden {
			t.Fatalf("arr: unevaluatedItems false was not read as a rejection: %+v", def.Def)
		}
		if def.Def.EvaluatedCount != 1 {
			t.Fatalf("arr: evaluated count = %d, want 1 -- the allOf branch's prefixItems evaluates position 0",
				def.Def.EvaluatedCount)
		}
		if def.Def.AllEvaluated {
			t.Fatalf("arr: AllEvaluated is set, so nothing past the prefix would ever be checked: %+v", def.Def)
		}
	})
}

// TestRuntimeUnevaluatedItemsPropertyBecomesAnnotationWrapper covers the
// arrangement no static count can answer: with an anyOf beside
// unevaluatedItems, which items were evaluated depends on which branch matched
// the value in hand. A named type of that shape has been routed to the runtime
// annotation evaluator since the evaluator existed, but the wrapper is only
// built for a schema being given a name, and an array written inline as a
// property never had one -- so the same schema enforced nothing there.
//
// Naming the property does change the field's Go type, which is the trade a
// bare `not` or a bare if/then/else property already makes.
func TestRuntimeUnevaluatedItemsPropertyBecomesAnnotationWrapper(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"arr": {"type":"array",
			        "prefixItems":[{"type":"string"}],
			        "anyOf":[{"prefixItems":[true,{"type":"integer"}]}, true],
			        "unevaluatedItems": false}
		},
		"required": ["arr"]
	}`)

	doc := structNamed(t, ir, "Doc")
	var wrapper *AnnotationSchemaDef
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*AnnotationSchemaDef); ok && d.Name == "DocArr" {
			wrapper = d
		}
	}
	if wrapper == nil {
		t.Fatalf("expected a DocArr annotation wrapper; got %v", ir.TypeDefs)
	}
	if got := fieldNamedJSON(t, doc, "arr").Type.GoTypeName(); got != "DocArr" {
		t.Fatalf("arr type = %s, want DocArr", got)
	}
	// A wrapper the owner never calls enforces nothing, which is how this
	// stayed broken after the wrapper itself was reachable.
	if !hasValidatableField(doc.ValidatableFields, "arr") {
		t.Fatalf("Doc.Validate does not call arr.Validate; the wrapper's schema is then interpreted for no one")
	}
	// And no static field check beside it: the two would disagree, and the
	// static one is the one that would be wrong.
	if len(doc.UnevalItemsValidations) != 0 {
		t.Fatalf("a wrapped property carries a static unevaluatedItems check too: %+v", doc.UnevalItemsValidations)
	}
}

// TestConditionalUnevaluatedItemsEmitsNoStaticCheck is the over-reach guard for
// the field-level unevaluatedItems check, and it guards the direction that
// matters most: a false rejection of a conforming document.
//
// EvaluatedCount counts only what the schema's own keywords evaluate; a branch
// that evaluated more contributes a ConditionalEval instead and leaves the
// count alone. Emitting the static check anyway would forbid items a matching
// branch had in fact evaluated. Where the annotation evaluator cannot model the
// subtree either -- minLength is outside its keyword set -- the answer is to
// emit nothing rather than to emit something wrong.
func TestConditionalUnevaluatedItemsEmitsNoStaticCheck(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"arr": {"type":"array",
			        "prefixItems":[{"type":"string"}],
			        "anyOf":[{"prefixItems":[true,{"type":"string","minLength":2}]}, true],
			        "unevaluatedItems": false}
		},
		"required": ["arr"]
	}`)

	doc := structNamed(t, ir, "Doc")
	for _, fu := range doc.UnevalItemsValidations {
		if fu.JSONName != "arr" {
			continue
		}
		t.Fatalf("arr carries a static unevaluatedItems check with evaluated count %d, "+
			"but the anyOf branch evaluates position 1 when it matches: [\"a\",\"bb\"] would be rejected though it conforms",
			fu.Def.EvaluatedCount)
	}
}

// TestNestedTuplePositionsAreChecked covers the tuple written as an array's
// element rather than as a property. The Go type there is a []any inside a
// [][]any, so it is neither a field the struct-level checks can name nor a type
// with a Validate to dispatch to, and its positions were checked nowhere:
// {"arr":[[7]]} was accepted where position 0 has to be a string.
func TestNestedTuplePositionsAreChecked(t *testing.T) {
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {
			"arr": {"type":"array", "items":{
				"type":"array",
				"prefixItems":[{"type":"string"}],
				"unevaluatedItems":{"type":"integer"}
			}}
		},
		"required": ["arr"]
	}`)

	doc := structNamed(t, ir, "Doc")
	iv := itemValidationFor(t, doc, "arr")
	if len(iv.Levels) != 1 {
		t.Fatalf("arr: %d levels, want 1 -- the descent stops at the tuple, since `items` there governs only its tail", len(iv.Levels))
	}
	level := iv.Levels[0]
	if !level.HasTupleItems() {
		t.Fatalf("arr elements are tuples, but the level carries no positions: %+v", level)
	}
	if len(level.TupleItems) != 1 || level.TupleItems[0].JSONType != "string" {
		t.Fatalf("arr element positions = %+v, want one string position", level.TupleItems)
	}
	if level.UnevalItems == nil || level.UnevalItems.ValueType != "integer" {
		t.Fatalf("arr element unevaluatedItems = %+v, want an integer check", level.UnevalItems)
	}
	if level.UnevalItems.EvaluatedCount != 1 {
		t.Fatalf("arr element evaluated count = %d, want 1", level.UnevalItems.EvaluatedCount)
	}
}

// TestOverflowMapOfTuplesChecksItsPositions covers the position where the fix
// for #92 and the fix for #94 meet: a schema-valued `additionalProperties`
// sitting beside declared properties, whose values are themselves tuples.
//
// Neither fix reaches it alone. #92 gave the overflow map a per-value descent;
// #94 taught that descent to check a tuple element's positions. But #94 named
// the types those positions materialize into from the *field* the container
// came from, and the overflow map is not a field -- it is reached only through
// the keyword. So the two had to agree on where the name comes from, and the
// descent takes it as a prefix each caller supplies: the overflow map passes
// the one its value type was already resolved under, name+"Value".
//
// Without that agreement the positions are either unchecked or minted under a
// name that collides with another container's. This pins both halves: that the
// positions are checked at all, and that they are named under the owning
// struct.
func TestOverflowMapOfTuplesChecksItsPositions(t *testing.T) {
	// Position 0 states a type and nothing else, so it is checked inline by
	// JSONType; position 1 carries a keyword and so has to materialize a type of
	// its own. Both routes run through the prefix, so both are asserted.
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {"alpha": {"type":"string"}},
		"additionalProperties": {
			"type": "array",
			"prefixItems": [{"type":"string"},{"type":"integer","minimum":5}]
		}
	}`)

	doc := structNamed(t, ir, "Doc")
	iv := overflowValidationFor(t, doc)
	if iv.PathName != "additionalProperties" {
		t.Fatalf("overflow PathName = %q, want %q -- the map is no declared property and must report under its keyword", iv.PathName, "additionalProperties")
	}
	if !iv.OwnsOutermost {
		t.Fatalf("overflow definition does not own its outermost element; nothing else dispatches to it, so its values would go unchecked")
	}
	if len(iv.Levels) != 1 {
		t.Fatalf("overflow: %d levels, want 1 -- the descent stops at the tuple: %+v", len(iv.Levels), iv.Levels)
	}

	level := iv.Levels[0]
	if !level.IsMap {
		t.Fatalf("overflow level 0 is not a map; its error path would index a string key with %%d")
	}
	if !level.HasTupleItems() {
		t.Fatalf("the overflow map's values are tuples, but the level carries no positions: %+v -- {\"zz\":[\"a\",1]} would be accepted with both positions violated", level)
	}
	if len(level.TupleItems) != 2 {
		t.Fatalf("overflow value positions = %+v, want 2", level.TupleItems)
	}
	// Position 0: checked inline, so the type constraint has to be stated here.
	if got := level.TupleItems[0].JSONType; got != "string" {
		t.Fatalf("overflow position 0 JSONType = %q, want %q -- nothing else says the position must be a string", got, "string")
	}

	// Position 1: its `minimum` can only be enforced by a type of its own, and
	// that type's name is what the prefix decides. Naming it outside the owning
	// struct is how two containers come to mint the same name.
	pos1 := level.TupleItems[1]
	if pos1.TypeName == "" {
		t.Fatalf("overflow position 1 materialized no type, so its minimum is enforced nowhere: %+v", pos1)
	}
	if !strings.HasPrefix(pos1.TypeName, "Doc") {
		t.Fatalf("overflow position 1 type %q is not named under the owning struct; a name minted outside it can collide with another container's", pos1.TypeName)
	}
}
