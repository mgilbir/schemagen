package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The multi-package / shared-types differential.
//
// --schema-package and --shared-types are two spellings of the same schema set:
// one puts each document's types in a Go package of its own and crosses the
// boundary with an import, the other puts them all in one package. Nothing about
// the JSON differs, so nothing about the verdict may differ either -- the same
// document must decode, validate, marshal and re-decode the same way through
// both.
//
// That comparison is what found issues #295, #296 and #299, and it is a much
// better guard than a golden of today's multi-package output would be. A golden
// pins whatever the generator does; this pins the multi-package answer to an
// answer that is separately generated, separately compiled and separately
// exercised, and which a single-package corpus of several hundred fixtures is
// already holding to the schema. A defect has to be made twice, identically, to
// get past it.
//
// The three issues it is written over:
//
//   - #295: a map value typed by another package's type was validated by
//     nothing. `Root.Validate()` was `return nil` while the shared-types
//     spelling iterated the map and called each value's Validate.
//   - #296: an optional property of a foreign type was tagged ",omitempty",
//     which never omits a struct, so an absent property was written back as
//     null or {} and the generated type refused to read its own output.
//   - #299: a $ref occupying the whole of a document copied its target into the
//     referring package instead of importing it, so two packages declared two Go
//     types for one JSON shape.
//
// Only verdicts are compared, not messages. A path or a type name in an error
// legitimately differs between the two spellings; whether the document was
// accepted, and what it marshalled to, may not.

// crossDoc is one input document of a differential case.
type crossDoc struct {
	// File is the file name written into the source directory, and the key
	// --root-name is given.
	File string
	// ID is the document's $id, and the key --schema-package is given.
	ID string
	// Pkg is the last segment of the Go import path this document is generated
	// into under --schema-package.
	Pkg string
	// RootType is the Go type name the document's root is generated as, under
	// both spellings.
	RootType string
	// Body is the schema.
	Body string
}

type crossPackageCase struct {
	Name string
	Docs []crossDoc
	// Driven names the document whose root type the driver exercises.
	Driven string
	// Instances are the JSON documents put through both spellings.
	Instances []string
	// MustImport, when set, is an import path the driven package's generated
	// source has to name: the point of --schema-package is that a cross-package
	// $ref emits an import rather than a copy.
	MustImport string
	// MustNotDeclare lists Go type names the driven package must not declare of
	// its own, because another package of the run owns them.
	MustNotDeclare []string
}

func crossPackageCases() []crossPackageCase {
	return []crossPackageCase{
		{
			// Issue #295. Every additionalProperties-shaped position disagreed,
			// in the direction that accepts what the schema forbids.
			Name: "map_value_of_a_foreign_type",
			Docs: []crossDoc{
				{
					File: "t.json", ID: "https://ex.test/t.json", Pkg: "tpkg", RootType: "T",
					Body: `{"$schema":"https://json-schema.org/draft/2020-12/schema",
						"$id":"https://ex.test/t.json","title":"T","type":"string","minLength":3}`,
				},
				{
					File: "root.json", ID: "https://ex.test/root.json", Pkg: "rootpkg", RootType: "Root",
					Body: `{"$schema":"https://json-schema.org/draft/2020-12/schema",
						"$id":"https://ex.test/root.json","title":"Root","type":"object",
						"properties":{
							"m":{"type":"object","additionalProperties":{"$ref":"https://ex.test/t.json"}},
							"s":{"type":"array","items":{"$ref":"https://ex.test/t.json"}},
							"n":{"type":"object","additionalProperties":{"type":"array","items":{"$ref":"https://ex.test/t.json"}}}
						}}`,
				},
			},
			Driven: "root.json",
			Instances: []string{
				`{}`,
				`{"m":{}}`,
				`{"m":{"k":"ab"}}`,
				`{"m":{"k":"abc"}}`,
				`{"s":["ab"]}`,
				`{"s":["abc"]}`,
				`{"n":{"k":["ab"]}}`,
				`{"m":{"k":"abc"},"s":["abc"],"n":{"k":["abc"]}}`,
			},
			MustImport: "ex.test/m/tpkg",
		},
		{
			// Issue #296. Six definitions of another package's document, each
			// referenced as an optional property: an absent one was invented
			// into the output, and a present-but-empty collection erased.
			Name: "optional_properties_of_foreign_types",
			Docs: []crossDoc{
				{
					File: "t.json", ID: "https://ex.test/t.json", Pkg: "tpkg", RootType: "T",
					Body: `{"$schema":"https://json-schema.org/draft/2020-12/schema",
						"$id":"https://ex.test/t.json","title":"T","type":"object",
						"$defs":{
							"OneOf":{"oneOf":[{"type":"string"},{"type":"integer"}]},
							"Not":{"not":{"type":"string"}},
							"Arr":{"type":"array","items":{"type":"string"}},
							"Map":{"type":"object","additionalProperties":{"type":"string"}},
							"Obj":{"type":"object","properties":{"x":{"type":"string"}}},
							"Str":{"type":"string"}
						}}`,
				},
				{
					File: "root.json", ID: "https://ex.test/root.json", Pkg: "rootpkg", RootType: "Root",
					Body: `{"$schema":"https://json-schema.org/draft/2020-12/schema",
						"$id":"https://ex.test/root.json","title":"Root","type":"object",
						"properties":{
							"a":{"$ref":"https://ex.test/t.json#/$defs/OneOf"},
							"b":{"$ref":"https://ex.test/t.json#/$defs/Not"},
							"c":{"$ref":"https://ex.test/t.json#/$defs/Arr"},
							"d":{"$ref":"https://ex.test/t.json#/$defs/Map"},
							"e":{"$ref":"https://ex.test/t.json#/$defs/Obj"},
							"f":{"$ref":"https://ex.test/t.json#/$defs/Str"}
						}}`,
				},
			},
			Driven: "root.json",
			Instances: []string{
				`{}`,
				`{"c":[]}`,
				`{"d":{}}`,
				`{"a":"x"}`,
				`{"a":7}`,
				`{"b":7}`,
				`{"c":["x"]}`,
				`{"e":{}}`,
				`{"f":""}`,
				`{"a":"x","c":[],"d":{},"e":{"x":"y"},"f":"z"}`,
			},
			MustImport: "ex.test/m/tpkg",
		},
		{
			// Issue #299. A $ref written at the document root, to another
			// package's root type and to a named definition inside it.
			Name: "ref_at_the_document_root",
			Docs: []crossDoc{
				{
					File: "common.json", ID: "https://ex.test/common.json", Pkg: "one", RootType: "OneC",
					Body: `{"$schema":"https://json-schema.org/draft/2020-12/schema",
						"$id":"https://ex.test/common.json","title":"OneC","type":"object",
						"properties":{"postal_code":{"type":"string"}},
						"$defs":{"Code":{"type":"string","minLength":2}}}`,
				},
				{
					File: "v1.json", ID: "https://ex.test/v1.json", Pkg: "al", RootType: "V1",
					Body: `{"$schema":"https://json-schema.org/draft/2020-12/schema",
						"$id":"https://ex.test/v1.json","title":"V1","$ref":"https://ex.test/common.json"}`,
				},
			},
			Driven: "v1.json",
			Instances: []string{
				`{}`,
				`{"postal_code":"x"}`,
				`{"postal_code":null}`,
				`{"other":1}`,
			},
			MustImport:     "ex.test/m/one",
			MustNotDeclare: []string{"OneC"},
		},
		{
			// The exception the root-level arm makes: a target whose methods a
			// defined type over it would not carry. --shared-types generates
			// such a reference from the schema again rather than aliasing it,
			// and the multi-package spelling has to reach the same verdict
			// whichever of the two routes it takes.
			Name: "ref_at_the_document_root_to_a_raw_value_wrapper",
			Docs: []crossDoc{
				{
					File: "w.json", ID: "https://ex.test/w.json", Pkg: "wp", RootType: "W",
					Body: `{"$schema":"https://json-schema.org/draft/2020-12/schema",
						"$id":"https://ex.test/w.json","title":"W",
						"oneOf":[{"type":"string"},{"type":"integer"}]}`,
				},
				{
					File: "v3.json", ID: "https://ex.test/v3.json", Pkg: "al", RootType: "V3",
					Body: `{"$schema":"https://json-schema.org/draft/2020-12/schema",
						"$id":"https://ex.test/v3.json","title":"V3","$ref":"https://ex.test/w.json"}`,
				},
			},
			Driven: "v3.json",
			Instances: []string{
				`"x"`,
				`7`,
				`7.5`,
				`true`,
				`null`,
				`{}`,
			},
		},
		{
			Name: "ref_at_the_document_root_to_a_named_definition",
			Docs: []crossDoc{
				{
					File: "common.json", ID: "https://ex.test/common.json", Pkg: "one", RootType: "OneC",
					Body: `{"$schema":"https://json-schema.org/draft/2020-12/schema",
						"$id":"https://ex.test/common.json","title":"OneC","type":"object",
						"properties":{"postal_code":{"type":"string"}},
						"$defs":{"Code":{"type":"string","minLength":2}}}`,
				},
				{
					File: "v2.json", ID: "https://ex.test/v2.json", Pkg: "al", RootType: "V2",
					Body: `{"$schema":"https://json-schema.org/draft/2020-12/schema",
						"$id":"https://ex.test/v2.json","title":"V2",
						"$ref":"https://ex.test/common.json#/$defs/Code"}`,
				},
			},
			Driven: "v2.json",
			Instances: []string{
				`"x"`,
				`"xy"`,
				`""`,
				`1`,
			},
			MustImport:     "ex.test/m/one",
			MustNotDeclare: []string{"Code"},
		},
	}
}

// TestMultiPackageAgreesWithSharedTypes generates each case both ways, compiles
// both, drives the same instances through both, and requires the verdicts to
// match.
func TestMultiPackageAgreesWithSharedTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs two generated Go programs per case")
	}
	bin := schemagenBinary(t)
	for _, tc := range crossPackageCases() {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			src := t.TempDir()
			var files []string
			for _, d := range tc.Docs {
				path := filepath.Join(src, d.File)
				writeCrossFile(t, path, d.Body)
				files = append(files, path)
			}

			multiOut := filepath.Join(t.TempDir(), "multi")
			runSchemagen(t, bin, append(append([]string{"generate"}, files...),
				append(multiPackageFlags(tc), "-o", multiOut)...)...)

			sharedOut := filepath.Join(t.TempDir(), "shared")
			runSchemagen(t, bin, append(append([]string{"generate"}, files...),
				append(sharedTypesFlags(tc), "-o", sharedOut, "-p", "models")...)...)

			driven := drivenDoc(t, tc)

			// What --schema-package is documented to do, checked on the source
			// itself: "A $ref that crosses into a document owned by another
			// package emits a qualified reference and an import, instead of
			// materializing a second copy of the type."
			drivenSrc := readPackageSource(t, filepath.Join(multiOut, driven.Pkg))
			if tc.MustImport != "" && !strings.Contains(drivenSrc, strconvQuote(tc.MustImport)) {
				t.Errorf("the generated %s package does not import %q, so the cross-package $ref materialized a "+
					"copy rather than a reference -- the one thing --schema-package is documented to prevent:\n%s",
					driven.Pkg, tc.MustImport, drivenSrc)
			}
			for _, name := range tc.MustNotDeclare {
				if strings.Contains(drivenSrc, "\ntype "+name+" ") {
					t.Errorf("the generated %s package declares its own %s, which another package of the run owns. "+
						"Two Go types for one JSON shape is issue #299:\n%s", driven.Pkg, name, drivenSrc)
				}
			}

			multi := runDifferentialDriver(t, driverSpec{
				Root:       multiOut,
				Module:     "ex.test/m",
				ImportPath: "ex.test/m/" + driven.Pkg,
				TypeName:   driven.RootType,
				Instances:  tc.Instances,
			})
			shared := runDifferentialDriver(t, driverSpec{
				Root:       sharedOut,
				Module:     "ex.test/s",
				ImportPath: "ex.test/s",
				TypeName:   driven.RootType,
				Instances:  tc.Instances,
			})

			if len(multi) != len(shared) || len(multi) != len(tc.Instances) {
				t.Fatalf("driver line counts disagree: multi=%d shared=%d instances=%d", len(multi), len(shared), len(tc.Instances))
			}
			for i := range multi {
				if multi[i] == shared[i] {
					continue
				}
				t.Errorf("instance %s\n  multi-package: %s\n  shared-types:  %s\n"+
					"The same JSON, the same schema, two spellings of where the Go types live. A verdict that "+
					"depends on which spelling was used is a defect in whichever one disagrees with the corpus, "+
					"and the corpus holds the single-package one.",
					tc.Instances[i], multi[i], shared[i])
			}
		})
	}
}

func drivenDoc(t *testing.T, tc crossPackageCase) crossDoc {
	t.Helper()
	for _, d := range tc.Docs {
		if d.File == tc.Driven {
			return d
		}
	}
	t.Fatalf("case %s drives %q, which is not one of its documents", tc.Name, tc.Driven)
	return crossDoc{}
}

func multiPackageFlags(tc crossPackageCase) []string {
	var out []string
	for _, d := range tc.Docs {
		out = append(out, "--schema-package", d.ID+"=ex.test/m/"+d.Pkg)
		out = append(out, "--root-name", d.File+"="+d.RootType)
	}
	return out
}

func sharedTypesFlags(tc crossPackageCase) []string {
	out := []string{"--shared-types"}
	for _, d := range tc.Docs {
		out = append(out, "--root-name", d.File+"="+d.RootType)
	}
	return out
}

func writeCrossFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func readPackageSource(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading generated package %s: %v", dir, err)
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".go" || e.Name() == "schemagen_helpers.go" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		b.Write(data)
	}
	return b.String()
}

// strconvQuote is strconv.Quote without the import, for the one needle above.
func strconvQuote(s string) string { return `"` + s + `"` }

var (
	schemagenBinOnce sync.Once
	schemagenBinPath string
	schemagenBinErr  error
)

// schemagenBinary builds the CLI once for the whole test binary. The
// differential is driven through the command line rather than through the
// library because that is where multi-package generation is wired -- the
// package assignment, the generation order derived from the $refs, and the one
// registry shared by every document of the run.
func schemagenBinary(t *testing.T) string {
	t.Helper()
	schemagenBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "schemagen-bin")
		if err != nil {
			schemagenBinErr = err
			return
		}
		bin := filepath.Join(dir, "schemagen")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
		cmd.Dir = ".."
		if out, err := cmd.CombinedOutput(); err != nil {
			schemagenBinErr = fmt.Errorf("building schemagen: %w\n%s", err, out)
			return
		}
		schemagenBinPath = bin
	})
	if schemagenBinErr != nil {
		t.Fatal(schemagenBinErr)
	}
	return schemagenBinPath
}

func runSchemagen(t *testing.T, bin string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("schemagen %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

type driverSpec struct {
	// Root is the directory the generated tree was written into; it becomes the
	// module root.
	Root string
	// Module is the module path written into go.mod.
	Module string
	// ImportPath is what the driver imports the generated types from.
	ImportPath string
	// TypeName is the generated type the instances are driven through.
	TypeName  string
	Instances []string
}

// runDifferentialDriver compiles the generated tree together with a driver and
// returns one verdict line per instance.
func runDifferentialDriver(t *testing.T, spec driverSpec) []string {
	t.Helper()
	if err := writeTestGoMod(spec.Root, spec.Module); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
	drv := filepath.Join(spec.Root, "differentialdriver")
	if err := os.MkdirAll(drv, 0o755); err != nil {
		t.Fatalf("mkdir driver: %v", err)
	}
	instances, err := json.Marshal(spec.Instances)
	if err != nil {
		t.Fatal(err)
	}
	main := strings.NewReplacer(
		"@IMPORT@", spec.ImportPath,
		"@TYPE@", spec.TypeName,
		"@INSTANCES@", string(instances),
	).Replace(differentialDriverMain)
	if err := os.WriteFile(filepath.Join(drv, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatalf("writing driver: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./differentialdriver")
	cmd.Dir = spec.Root
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("driving the generated %s tree: %v\n%s", spec.Module, err, out)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if strings.HasPrefix(line, "V ") {
			lines = append(lines, strings.TrimPrefix(line, "V "))
		}
	}
	return lines
}

// differentialDriverMain reports, for each instance, only what both spellings
// have to agree on: whether the document decoded, whether it validated, what it
// marshalled back to, and whether that output decodes again.
//
// Error *text* is deliberately not compared. The two spellings name types
// differently and reach a nested value by different routes, so a message may
// legitimately differ; whether the document was accepted may not. The
// marshalled form is canonicalised through a map so that member order is not
// mistaken for a disagreement.
const differentialDriverMain = `package main

import (
	"encoding/json"
	"fmt"

	gen "@IMPORT@"
)

func canonical(b []byte) string {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return "unparseable(" + string(b) + ")"
	}
	out, err := json.Marshal(v)
	if err != nil {
		return "unmarshalable"
	}
	return string(out)
}

func verdict(in string) string {
	var v gen.@TYPE@
	if err := json.Unmarshal([]byte(in), &v); err != nil {
		return "decode=refused"
	}
	if err := v.Validate(); err != nil {
		return "decode=ok validate=refused"
	}
	out, err := json.Marshal(v)
	if err != nil {
		return "decode=ok validate=ok marshal=refused"
	}
	re := "ok"
	var again gen.@TYPE@
	if err := json.Unmarshal(out, &again); err != nil {
		re = "refused"
	}
	return "decode=ok validate=ok marshal=" + canonical(out) + " redecode=" + re
}

func main() {
	var instances []string
	if err := json.Unmarshal([]byte(` + "`" + `@INSTANCES@` + "`" + `), &instances); err != nil {
		panic(err)
	}
	for _, in := range instances {
		fmt.Println("V " + verdict(in))
	}
}
`
