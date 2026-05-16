package generator

import (
	"encoding/json"
	"testing"

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
	if got := fields["choices"].Type.GoTypeName(); got != "*[]string" {
		t.Fatalf("choices type = %q, want *[]string", got)
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

func TestOptionalRefToPrimitiveAliasDoesNotBecomePointer(t *testing.T) {
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
			if field.Type.GoTypeName() != "Name" {
				t.Fatalf("nickname type = %q, want Name", field.Type.GoTypeName())
			}
			return
		}
	}
	t.Fatalf("expected nickname field")
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

	// Optional array fields with omitempty are wrapped in *[]T to preserve empty arrays.
	membersField := fieldMap["members"]
	if membersField.Type.GoTypeName() != "*[]string" {
		t.Errorf("members type = %q, want %q", membersField.Type.GoTypeName(), "*[]string")
	}

	scoresField := fieldMap["scores"]
	if scoresField.Type.GoTypeName() != "*[]int64" {
		t.Errorf("scores type = %q, want %q", scoresField.Type.GoTypeName(), "*[]int64")
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
	priorityField := fieldMap["priority"]
	if priorityField.Type.GoTypeName() != "TaskPriority" {
		t.Errorf("priority field type = %q, want %q", priorityField.Type.GoTypeName(), "TaskPriority")
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
