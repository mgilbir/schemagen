package tests

import (
	"encoding/json"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// TestARefIntoARewrittenKeywordGeneratesThroughThePipeline: a $ref naming a
// subschema where a draft 3-7 document wrote it -- inside "dependencies",
// "extends", "disallow" or draft 3's schema-valued "type" -- used to fail
// generation once Normalize had moved the subschema, within one document and
// across two alike.
//
// The refs end in distinct tokens on purpose: a library caller's refs that end
// in the same token ("#/disallow/1", "#/type/1") are handed one Go type name,
// which is a separate naming defect (cmd/schemagen numbers them apart;
// pkg/generator alone does not), and this test is about what they resolve to.
func TestARefIntoARewrittenKeywordGeneratesThroughThePipeline(t *testing.T) {
	const d3 = `"$schema":"http://json-schema.org/draft-03/schema#"`
	const legacy = `"dependencies":{"a":{"type":"integer","minimum":5}},
		"extends":[{"type":"object"},{"properties":{"m":{"type":"string","maxLength":2}}}],
		"disallow":["null","integer",{"type":"boolean"}],
		"type":["object",{"type":"number","maximum":3}]`
	var other schema.Schema
	if err := json.Unmarshal([]byte(`{`+d3+`,`+legacy+`}`), &other); err != nil {
		t.Fatal(err)
	}
	other.Normalize()
	cfg := generator.Config{Resolver: schema.NewMappingResolver(map[string]*schema.Schema{"https://example.test/other.json": &other})}

	instances := []notInstance{
		{Name: "all four satisfied", Doc: `{"dep":7,"ext":"ab","dis":true,"typ":2}`, Valid: true, Why: "control"},
		{Name: "dependencies/a", Doc: `{"dep":4}`, Valid: false, Why: "the dependency's minimum:5"},
		{Name: "extends/1/properties/m", Doc: `{"ext":"abc"}`, Valid: false, Why: "the extended property's maxLength:2"},
		{Name: "disallow/2", Doc: `{"dis":"x"}`, Valid: false, Why: "the disallowed schema is type:boolean"},
		{Name: "type/1", Doc: `{"typ":4}`, Valid: false, Why: "the type alternative's maximum:3"},
	}
	props := func(prefix string) string {
		return `"properties":{
			"dep":{"$ref":"` + prefix + `#/dependencies/a"},
			"ext":{"$ref":"` + prefix + `#/extends/1/properties/m"},
			"dis":{"$ref":"` + prefix + `#/disallow/2"},
			"typ":{"$ref":"` + prefix + `#/type/1"}}`
	}
	t.Run("within one document", func(t *testing.T) {
		// The refs name the root's own legacy keywords, so the root states them
		// too, and the documents below must satisfy them as well.
		src := `{` + d3 + `,` + legacy + `,` + props("") + `}`
		runInlineSchema(t, src, generator.Config{}, append(instances[1:],
			notInstance{Name: "the root's own keywords, satisfied", Doc: `{"dep":7,"ext":"ab","dis":true,"typ":2}`, Valid: true, Why: "control"}))
	})
	t.Run("across documents", func(t *testing.T) {
		src := `{"$id":"https://example.test/root.json","type":"object",` + props("other.json") + `}`
		runInlineSchema(t, src, cfg, instances)
	})
}

// TestARefIntoAKeywordTheDialectDoesNotDefineGenerates is the same through the
// generator: draft 3 has no "not", so the root states no complement, and a
// property referring to "#/not" is typed by that value.
func TestARefIntoAKeywordTheDialectDoesNotDefineGenerates(t *testing.T) {
	src := `{"$schema":"http://json-schema.org/draft-03/schema#","type":"object",
		"not":{"type":"string","maxLength":2},
		"properties":{"x":{"$ref":"#/not"}}}`
	runInlineSchema(t, src, generator.Config{}, []notInstance{
		{Name: "within the referenced schema", Doc: `{"x":"ab"}`, Valid: true, Why: "control"},
		{Name: "past its maxLength", Doc: `{"x":"abc"}`, Valid: false, Why: "#/not names {type:string, maxLength:2}"},
		{Name: "the root is not a complement", Doc: `{}`, Valid: true, Why: "draft 3 has no not, so the root states none"},
	})
}
