package generator

import (
	"go/format"
	"strings"
	"testing"
)

// TestNodeLiteralSignalsSurviveAlignment feeds HelpersReferencedBy the
// composite literals the emitter really writes -- run through go/format,
// which pads a key to the width of the longest key beside it -- and requires
// every field-literal signal to be read. Each such signal declares a field the
// helper block only has when the signal is seen, so a signal missed under
// padding is a package that does not compile: `Minimum:          _strPtr(` next
// to ExclusiveMaximum once was.
func TestNodeLiteralSignalsSurviveAlignment(t *testing.T) {
	// A slightly longer key in the same literal pads the one under test.
	// Slightly: go/printer stops aligning keys whose lengths differ too much,
	// and the guard below refuses a case that ends up unpadded.
	pad := func(key string) string { return key + "Xx: nil,\n" }
	cases := []struct {
		name  string
		field string
		flag  func(HelperSet) bool
	}{
		{"const", `Const: _strPtr("1"),`, func(h HelperSet) bool { return h.AnnotationsEquality }},
		{"enum", `Enum: []string{"1"},`, func(h HelperSet) bool { return h.AnnotationsEquality }},
		{"uniqueItems", `UniqueItems: true,`, func(h HelperSet) bool { return h.AnnotationsEquality }},
		{"minimum", `Minimum: _strPtr("1"),`, func(h HelperSet) bool { return h.AnnotationsBounds }},
		{"maximum", `Maximum: _strPtr("1"),`, func(h HelperSet) bool { return h.AnnotationsBounds }},
		{"exclusiveMinimum", `ExclusiveMinimum: _strPtr("1"),`, func(h HelperSet) bool { return h.AnnotationsBounds }},
		{"exclusiveMaximum", `ExclusiveMaximum: _strPtr("1"),`, func(h HelperSet) bool { return h.AnnotationsBounds }},
		{"multipleOf", `MultipleOf: _strPtr("1"),`, func(h HelperSet) bool { return h.AnnotationsMultipleOf }},
		{"pattern", `Pattern: ` + patternVarPrefix + `0,`, func(h HelperSet) bool { return h.AnnotationsPattern }},
		{"contentEncoding", `ContentEncoding: _strPtr("base64"),`, func(h HelperSet) bool { return h.AnnotationsContent }},
		{"contentMediaType", `ContentMediaType: _strPtr("application/json"),`, func(h HelperSet) bool { return h.AnnotationsContent }},
		{"format", `Format: _strPtr("date"),`, func(h HelperSet) bool { return len(h.AnnotationsFormats) == 1 }},
		{"ref", `Ref: &_rt0,`, func(h HelperSet) bool { return h.AnnotationsDynamic }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key := c.field[:strings.Index(c.field, ":")]
			src := "package p\n\nvar n = _schemaNode{\n" + pad(key) + c.field + "\n}\n\nvar _ = _evalNode\n"
			formatted, err := format.Source([]byte(src))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(formatted), c.field) {
				t.Fatalf("go/format did not pad %s, so this case tests nothing:\n%s", key, formatted)
			}
			if !c.flag(HelpersReferencedBy(string(formatted))) {
				t.Errorf("%s was not read under go/format's padding:\n%s", key, formatted)
			}
		})
	}

	// The --strict-read-write table's pattern step, read the same way.
	src := "package p\n\nvar r = _accessRule{\n" + pad("Kind") + "Kind: _accessPattern,\n}\n\nvar _ = _accessRefuseReadOnly\n"
	formatted, err := format.Source([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(formatted), "Kind: _accessPattern") {
		t.Fatalf("go/format did not pad Kind, so this case tests nothing:\n%s", formatted)
	}
	h := HelpersReferencedBy(strings.Replace(string(formatted), "_accessRefuseReadOnly", "_accessRefuseReadOnly(", 1))
	if !h.AccessPattern {
		t.Errorf("Kind: _accessPattern was not read under go/format's padding:\n%s", formatted)
	}
}
