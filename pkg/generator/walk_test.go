package generator

import (
	"encoding/json"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// WalkSchema must reach every position a $ref can occupy: a missed keyword means
// a missed dependency for callers that collect refs.
func TestWalkSchemaVisitsEveryApplicatorPosition(t *testing.T) {
	// Each ref value doubles as the name of the position it sits in.
	//
	// The document deliberately declares no $schema. The positions below span
	// every draft -- prefixItems is 2020-12's and additionalItems was removed
	// there, dependentSchemas is 2019-09's and definitions is draft 7's -- so no
	// one dialect defines them all, and normalization drops the ones the declared
	// dialect does not have. WalkSchema is asked whether it reaches a position,
	// not whether a dialect has it; naming a dialect here would silently narrow
	// the question to that dialect's keywords.
	src := `{
		"$id": "https://ex.test/root.json",
		"allOf":     [{"$ref": "allOf"}],
		"anyOf":     [{"$ref": "anyOf"}],
		"oneOf":     [{"$ref": "oneOf"}],
		"not":       {"$ref": "not"},
		"if":        {"$ref": "if"},
		"then":      {"$ref": "then"},
		"else":      {"$ref": "else"},
		"contains":  {"$ref": "contains"},
		"propertyNames": {"$ref": "propertyNames"},
		"contentSchema": {"$ref": "contentSchema"},
		"unevaluatedItems": {"$ref": "unevaluatedItems"},
		"unevaluatedProperties": {"$ref": "unevaluatedProperties"},
		"properties":        {"p": {"$ref": "properties"}},
		"patternProperties": {"^x": {"$ref": "patternProperties"}},
		"dependentSchemas":  {"d": {"$ref": "dependentSchemas"}},
		"$defs":             {"a": {"$ref": "defs"}},
		"definitions":       {"b": {"$ref": "definitions"}},
		"prefixItems":       [{"$ref": "prefixItems"}],
		"items":             {"$ref": "items"},
		"additionalProperties": {"$ref": "additionalProperties"},
		"additionalItems":      {"$ref": "additionalItems"},
		"x-vendor":              {"$ref": "extensions"}
	}`
	var s schema.Schema
	if err := json.Unmarshal([]byte(src), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	s.ComputeBaseURIs(nil, &s)

	found := map[string]bool{}
	WalkSchema(&s, func(node *schema.Schema) {
		if node.Ref != "" {
			found[node.Ref] = true
		}
	})

	for _, want := range []string{
		"allOf", "anyOf", "oneOf", "not", "if", "then", "else", "contains",
		"propertyNames", "contentSchema", "unevaluatedItems", "unevaluatedProperties",
		"properties", "patternProperties", "dependentSchemas", "defs", "definitions",
		"prefixItems", "items", "additionalProperties", "additionalItems",
		"extensions",
	} {
		if !found[want] {
			t.Errorf("WalkSchema did not reach a $ref in %q position", want)
		}
	}
}

// $recursiveRef and $dynamicRef are refs too.
func TestWalkSchemaReachesRecursiveAndDynamicRefs(t *testing.T) {
	var s schema.Schema
	if err := json.Unmarshal([]byte(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"properties": {
			"a": {"$recursiveRef": "#"},
			"b": {"$dynamicRef": "#node"}
		}
	}`), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	var recursive, dynamic bool
	WalkSchema(&s, func(node *schema.Schema) {
		if node.RecursiveRef != "" {
			recursive = true
		}
		if node.DynamicRef != "" {
			dynamic = true
		}
	})
	if !recursive {
		t.Error("WalkSchema did not reach a $recursiveRef")
	}
	if !dynamic {
		t.Error("WalkSchema did not reach a $dynamicRef")
	}
}

// Tuple-form items (an array of schemas) must be walked as well as the single
// schema form.
func TestWalkSchemaWalksTupleItems(t *testing.T) {
	var s schema.Schema
	if err := json.Unmarshal([]byte(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "array",
		"items": [{"$ref": "first"}, {"$ref": "second"}]
	}`), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	seen := map[string]bool{}
	WalkSchema(&s, func(node *schema.Schema) {
		if node.Ref != "" {
			seen[node.Ref] = true
		}
	})
	if !seen["first"] || !seen["second"] {
		t.Errorf("tuple items should both be walked, saw %v", seen)
	}
}

// A self-referential structure must not send the walker into a loop, and each
// node must be visited once.
func TestWalkSchemaVisitsEachNodeOnceDespiteCycles(t *testing.T) {
	inner := &schema.Schema{Type: schema.TypeList{"object"}}
	root := &schema.Schema{
		Type:       schema.TypeList{"object"},
		Properties: map[string]*schema.Schema{"a": inner, "b": inner},
	}
	inner.Properties = map[string]*schema.Schema{"self": root}

	counts := map[*schema.Schema]int{}
	WalkSchema(root, func(node *schema.Schema) { counts[node]++ })

	if counts[root] != 1 || counts[inner] != 1 {
		t.Errorf("each node should be visited once, got root=%d inner=%d", counts[root], counts[inner])
	}
}
