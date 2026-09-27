package tests

import (
	"encoding/json"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// TestACrossDocumentPointerIsDecodedOnce: a pointer into another document was
// percent-decoded by url.Parse and then again by the LocalResolver it was
// handed to, so "#/$defs/a%2525b" named the key "a%b" there and "a%25b" inside
// its own document.
func TestACrossDocumentPointerIsDecodedOnce(t *testing.T) {
	var other schema.Schema
	if err := json.Unmarshal([]byte(`{"$defs":{"a%25b":{"type":"string"},"a%b":{"type":"integer"},
		"a":{"properties":{"b":{"type":"boolean"}}},"a/properties/b":{"type":"null"}}}`), &other); err != nil {
		t.Fatal(err)
	}
	other.Normalize()
	resolver := schema.NewMappingResolver(map[string]*schema.Schema{"https://example.test/other.json": &other})
	cfg := generator.Config{Resolver: resolver}
	for _, tc := range []struct {
		name, src string
		instances []notInstance
	}{
		{"a percent-escaped percent", `{"$id":"https://example.test/root.json","$ref":"other.json#/$defs/a%2525b"}`, []notInstance{
			{Name: "a string", Doc: `"x"`, Valid: true, Why: "the key a%25b holds a string schema"},
			{Name: "an integer", Doc: `1`, Valid: false, Why: "decoding twice reached the key a%b, which holds the integer schema"},
		}},
		{"a percent-escaped separator", `{"$id":"https://example.test/root.json","$ref":"other.json#/$defs/a%2Fproperties%2Fb"}`, []notInstance{
			{Name: "a boolean", Doc: `true`, Valid: true, Why: "%2F is a separator once decoded (RFC 6901 §6): $defs, a, properties, b"},
			{Name: "a null", Doc: `null`, Valid: false, Why: "splitting before decoding read the single key a/properties/b"},
		}},
		{"a percent-escaped separator in the same document", `{"$defs":{"a":{"properties":{"b":{"type":"boolean"}}},"a/properties/b":{"type":"null"}},"$ref":"#/$defs/a%2Fproperties%2Fb"}`, []notInstance{
			{Name: "a boolean", Doc: `true`, Valid: true, Why: "the same pointer reaches the same node in its own document"},
			{Name: "a null", Doc: `null`, Valid: false, Why: "splitting before decoding read the single key a/properties/b"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runInlineSchema(t, tc.src, cfg, tc.instances)
		})
	}
}
