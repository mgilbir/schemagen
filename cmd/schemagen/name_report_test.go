package schemagen

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// namedInReport matches every type name a name report hands the reader: the
// name a claim keeps, the name it becomes, the name a moved definition is
// declared as.
var namedInReport = regexp.MustCompile(`(?m)^  .* (?:keeps|becomes) ([A-Za-z_][A-Za-z0-9_]*)$|declared as ([A-Za-z_][A-Za-z0-9_]*);`)

// The collision warnings are written from the generator's name registry, after
// the types are declared -- so no warning can describe a type the package does
// not have. They used to be written from the names the CLI chose before
// generating, and a node that generation then named otherwise, or never
// declared, was reported under a name nothing declares: "other.json $defs/Name
// becomes OtherName" for a run with no OtherName in it.
func TestNameReportsNameOnlyDeclaredTypes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string
		args  []string
	}{
		{
			// Two documents, one definition name, different schemas; and the
			// referenced document's definition is resolved elsewhere, so it is
			// never declared at all.
			name: "external definition never declared",
			files: []string{
				"root.json", `{"$defs":{"Name":{"type":"integer"}},"properties":{"a":{"$ref":"other.json"}}}`,
				"other.json", `{"$defs":{"Name":{"type":"string"}},"properties":{"n":{"$ref":"#/$defs/Name"}}}`,
			},
		},
		{
			name: "folded keys of one document",
			files: []string{
				"a.json", `{"title":"Doc","properties":{"x":{"$ref":"#/$defs/a-b"},"y":{"$ref":"#/$defs/a_b"}},
					"$defs":{"a-b":{"type":"string"},"a_b":{"type":"integer"}}}`,
			},
		},
		{
			name: "shared types, contested and qualified onto a taken name",
			files: []string{
				"a.json", `{"title":"ADoc","properties":{"t":{"$ref":"#/$defs/Thing"},"thing":{"type":"object","properties":{"z":{"type":"boolean"}}}},
					"$defs":{"Thing":{"type":"object","properties":{"k":{"type":"string"}}},"ADocThing":{"type":"integer"}}}`,
				"b.json", `{"title":"BDoc","properties":{"t":{"$ref":"#/$defs/Thing"}},"$defs":{"Thing":{"type":"integer"}}}`,
			},
			args: []string{"--shared-types"},
		},
		{
			name: "a definition keyed like a generated helper",
			files: []string{
				"a.json", `{"$schema":"https://json-schema.org/draft/2020-12/schema","$dynamicAnchor":"node",
					"properties":{"x":{"$ref":"#/$defs/SchemagenValidationMode"},"kids":{"type":"array","items":{"$dynamicRef":"#node"}}},
					"$defs":{"SchemagenValidationMode":{"type":"string"}}}`,
			},
			args: []string{"--validation", "hybrid"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, paths := writeSchemas(t, tc.files...)
			out := filepath.Join(dir, "gen")
			args := []string{paths[0]}
			if len(tc.args) > 0 && tc.args[0] == "--shared-types" {
				args = paths
			}
			args = append(args, "-o", out, "-p", "gen")
			args = append(args, tc.args...)
			stderr, err := runGenerateCapturing(t, args...)
			if err != nil {
				t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
			}
			declared := map[string]bool{}
			for _, name := range declaredTypeNames(t, out) {
				declared[name] = true
			}
			reported := namedInReport.FindAllStringSubmatch(stderr, -1)
			if len(reported) == 0 {
				t.Fatalf("the run reported no name at all, so this checks nothing:\n%s", stderr)
			}
			for _, m := range reported {
				name := m[1] + m[2]
				if !declared[name] {
					t.Errorf("the report names %s, which the package does not declare (declared: %s)\nstderr:\n%s",
						name, strings.Join(declaredTypeNames(t, out), ","), stderr)
				}
			}
		})
	}
}

// A draft-07 document writes "definitions"; normalization mirrors the entries
// into $defs as well, and the mirror used to be read first -- so a definition
// qualified by its keyword came out DefsThing, and the report said "$defs/Thing",
// a keyword the document never wrote. The mirror is skipped: the claim is the
// document's own spelling.
func TestKeywordQualifierIsTheKeywordTheDocumentWrote(t *testing.T) {
	dir, paths := writeSchemas(t, "a.json", `{"$schema":"http://json-schema.org/draft-07/schema#","title":"Thing","type":"object",
		"properties":{"t":{"$ref":"#/definitions/Thing"}},"definitions":{"Thing":{"type":"integer"}}}`)
	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, paths[0], "-o", out, "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if got := strings.Join(declaredTypeNames(t, out), ","); got != "DefinitionsThing,Thing" {
		t.Errorf("declared types = %s, want DefinitionsThing,Thing", got)
	}
	if !strings.Contains(stderr, "definitions/Thing becomes DefinitionsThing") || strings.Contains(stderr, "$defs/") {
		t.Errorf("the report should name the keyword the document wrote:\n%s", stderr)
	}
}
