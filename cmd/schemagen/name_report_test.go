package schemagen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// namedInReport matches every name a name report hands the reader: the name a
// claim keeps or becomes (group 1), the name a moved one is declared as (group
// 2), the name it wanted (group 3), and either the name it is numbered together
// with (group 4) or what the wanted name is (group 5).
var namedInReport = regexp.MustCompile(`(?m)^  .* (?:keeps|becomes) ([A-Za-z_][A-Za-z0-9_]*)$|^warning: .* is ([A-Za-z_][A-Za-z0-9_]*), not ([A-Za-z_][A-Za-z0-9_]*)(?:: it is numbered together with ([A-Za-z_][A-Za-z0-9_]*), which is [^;]*|: [A-Za-z_][A-Za-z0-9_]* is ([^;]*))?;`)

// declaredIdentifiers is every identifier the generated package in dir
// declares: at package level, and as a field or method of one of its types.
func declaredIdentifiers(t *testing.T, dir string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				names[d.Name.Name] = true
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						names[s.Name.Name] = true
						if st, ok := s.Type.(*ast.StructType); ok {
							for _, f := range st.Fields.List {
								for _, n := range f.Names {
									names[n.Name] = true
								}
							}
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							names[n.Name] = true
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

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
		{
			// Members: a field numbered off a generated method and off another
			// property's field, and a union getter numbered off a field together
			// with its wrapper type.
			name: "members of a type",
			files: []string{
				"a.json", `{"title":"Root","type":"object","properties":{"getCat":{"type":"string"},"validate":{"type":"integer"},"a-b":{"type":"string"},"a_b":{"type":"string"},
					"p":{"oneOf":[{"title":"Cat","type":"object","properties":{"m":{"type":"string"}},"required":["m"]},{"type":"integer"}]}}}`,
			},
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
			declared := declaredIdentifiers(t, out)
			reported := namedInReport.FindAllStringSubmatch(stderr, -1)
			if len(reported) == 0 {
				t.Fatalf("the run reported no name at all, so this checks nothing:\n%s", stderr)
			}
			for _, m := range reported {
				// The name a claim became, and the name whose holder kept it
				// off the one it wanted: that holder's own name where the two
				// are numbered together, else the name it wanted -- unless the
				// report says the wanted name is only reserved, or only derived
				// by several properties at once, neither of which says anything
				// declares it. A name the generated helper file declares counts:
				// a helper can be what holds a name.
				mentioned := []string{m[1] + m[2]}
				switch {
				case m[4] != "":
					mentioned = append(mentioned, m[4])
				case m[3] != "" && !strings.HasPrefix(m[5], "reserved for") && !strings.HasPrefix(m[5], "also what "):
					mentioned = append(mentioned, m[3])
				}
				for _, name := range mentioned {
					if !declared[name] {
						t.Errorf("the report names %s, which the package does not declare\nstderr:\n%s", name, stderr)
					}
				}
			}
			if n := strings.Count(stderr, "note: "); n != 1 {
				t.Errorf("the explanation of how names are separated was written %d times, want once per run:\n%s", n, stderr)
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

// A name inside a type -- a field, a union getter, and the wrapper type the
// getter is numbered together with -- is the generated package's API as much as
// a type name is, and moving one used to be silent: a property "getCat" beside a
// variant titled Cat turned the getter into GetCat2 and the wrapper into
// Root_Cat2 without a word. Each move is one line saying what moved, why, and
// how to choose; the paragraph on how names are separated is written once.
func TestMemberMovesAreReported(t *testing.T) {
	dir, paths := writeSchemas(t, "a.json", `{"title":"Root","type":"object","properties":{
		"getCat":{"type":"string"},"validate":{"type":"integer"},"a-b":{"type":"string"},"a_b":{"type":"string"},
		"p":{"oneOf":[{"title":"Cat","type":"object","properties":{"m":{"type":"string"}},"required":["m"]},{"type":"integer"}]}}}`)
	stderr, err := runGenerateCapturing(t, paths[0], "-o", filepath.Join(dir, "gen"), "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	const field = "to choose: rename the property, or map it with --field-map"
	const other = "to choose: rename whichever of the two should give way"
	p := "warning: " + paths[0] + ": "
	want := p + `the field for property "a-b" of Root (#/properties/a-b) is AB1, not AB: AB is also what property "a_b" derives; ` + field + "\n" +
		nameSeparationNote +
		p + `the field for property "a_b" of Root (#/properties/a_b) is AB2, not AB: AB is also what property "a-b" derives; ` + field + "\n" +
		p + `the field for property "validate" of Root (#/properties/validate) is Validate1, not Validate: Validate is reserved for a member every generated type may declare; ` + field + "\n" +
		p + `the getter of variant 0 of Root.P (#/properties/p/oneOf/0) is GetCat2, not GetCat: GetCat is the field for property "getCat"; ` + other + "\n" +
		p + `the wrapper type of variant 0 of Root.P (#/properties/p/oneOf/0) is Root_Cat2, not Root_Cat: it is numbered together with GetCat, which is the field for property "getCat"; ` + other + "\n"
	if stderr != want {
		t.Errorf("stderr =\n%s\nwant\n%s", stderr, want)
	}
}
