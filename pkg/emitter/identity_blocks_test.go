package emitter

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// topLevelNames is every name a Go file declares at the top level of its
// package: its functions (not methods), types, variables and constants.
func topLevelNames(f *ast.File) []string {
	var names []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				names = append(names, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					names = append(names, s.Name.Name)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						names = append(names, n.Name)
					}
				}
			}
		}
	}
	sort.Strings(names)
	return names
}

// TestIdentityBlockTableMatchesTheTemplates holds the generator's table of the
// identity blocks -- which a generated file's names are matched against to pick
// the blocks it takes -- to the names each template block declares, rendered
// with and without the in-place decode its lazy arms are written for. A name
// added to a block and not to the table would never pull its block in; one
// moved between blocks would pull in the wrong one.
func TestIdentityBlockTableMatchesTheTemplates(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	table := generator.IdentityBlockDecls()
	if len(table) != 6 {
		t.Fatalf("the generator's table lists %d identity blocks; there are six", len(table))
	}
	for name, want := range table {
		want = append([]string(nil), want...)
		sort.Strings(want)
		for _, decode := range []bool{false, true} {
			var buf bytes.Buffer
			if err := e.tmpl.ExecuteTemplate(&buf, name, generator.HelperSet{Decode: decode}); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			f, err := parser.ParseFile(token.NewFileSet(), name, "package p\n"+buf.String(), parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("%s does not parse as Go on its own: %v", name, err)
			}
			if got := topLevelNames(f); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("%s (decode %v) declares %v; the generator's table lists %v", name, decode, got, want)
			}
		}
	}
}

// TestIdentityBlocksAreMinimal is the point of the split: the identity helpers
// are code in the user's package, compile time and binary size, so a package
// takes the blocks its generated code calls and the blocks those call, and
// nothing else. For each way into them, a schema whose generated code enters
// there is emitted, its helper file parsed, and the identity blocks in it held
// to exactly the ones that entry's closure declares -- and the pair of files
// type-checked, so a closure that is too small fails as surely as one that is
// too large.
func TestIdentityBlocksAreMinimal(t *testing.T) {
	blocks := generator.IdentityBlockDecls()
	owner := map[string]string{}
	for name, decls := range blocks {
		for _, d := range decls {
			owner[d] = name
		}
	}
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	const (
		core  = "identity_core_helpers"
		konst = "identity_const_helpers"
		anyv  = "identity_any_helpers"
		lazy  = "identity_lazy_helpers"
		value = "identity_value_helpers"
		kind  = "identity_kind_helpers"
		d2020 = `"$schema":"https://json-schema.org/draft/2020-12/schema",`
	)
	cases := []struct {
		name      string
		doc       string
		evaluator bool     // the case must carry the runtime evaluator
		want      []string // the identity blocks the helper file must hold, and no others
	}{
		// No comparison of values at all.
		{"a string property", `{"type":"object","properties":{"s":{"type":"string"}}}`, false, nil},
		// The runtime evaluator compares values only for const, enum and
		// uniqueItems; a schema stating none takes no identity block.
		{"the evaluator judging types only", `{` + d2020 + `"anyOf":[{"prefixItems":[{"type":"integer"}]},{"prefixItems":[{"type":"string"}]}],"unevaluatedItems":false}`, true, nil},
		// The evaluator comparing decoded values: the decoded-JSON reader and
		// the const it compares against, and the document's own identities
		// for what it reads lazily -- none of the Go-value walker.
		{"the evaluator judging an enum", `{` + d2020 + `"type":"object","unevaluatedProperties":{"enum":[1,"a"]}}`, true,
			[]string{anyv, konst, core, lazy}},
		{"the evaluator judging uniqueItems", `{` + d2020 + `"type":"object","unevaluatedProperties":{"type":"array","uniqueItems":true}}`, true,
			[]string{anyv, konst, core, lazy}},
		// The same fields beside a longer key, which gofmt pads them to: the
		// signal is the field whatever the white space after its colon.
		{"the evaluator judging a const and an enum beside a bound", `{` + d2020 + `"type":"object","unevaluatedProperties":{"const":1,"enum":[1,"a"],"maxLength":2}}`, true,
			[]string{anyv, konst, core, lazy}},
		{"the evaluator judging uniqueItems beside unevaluatedItems", `{` + d2020 + `"type":"object","unevaluatedProperties":{"uniqueItems":true,"unevaluatedItems":false}}`, true,
			[]string{anyv, konst, core, lazy}},
		// The object-level conditional's const reads the same decoded value.
		{"an object-level conditional's const", `{"type":"object","properties":{"k":{}},"if":{"properties":{"k":{"const":1}}},"then":{"required":["x"]}}`, false,
			[]string{anyv, konst, core, lazy}},
		// An enum held as raw JSON: the const and the raw reader under it.
		{"an enum", `{"enum":[{"a":1},"x"]}`, false, []string{konst, core}},
		{"an object const", `{"type":"object","properties":{"c":{"const":{"a":[1,2]}}}}`, false, []string{konst, core}},
		// Comparing values of this package's types: the walker.
		{"uniqueItems over structs", `{"type":"object","properties":{"a":{"type":"array","uniqueItems":true,"items":{"type":"object","properties":{"x":{"type":"string"}}}}}}`, false,
			[]string{value, konst, core, lazy}},
		// A keyword about one kind of value reads a value that is not decoded
		// JSON as its tree, which is the walker's.
		{"a contains stating a type", `{"type":"object","properties":{"a":{"type":"array","contains":{"type":"integer"}}}}`, false,
			[]string{kind, value, konst, core, lazy}},
	}
	imp := importer.ForCompiler(token.NewFileSet(), "source", nil)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s schema.Schema
			if err := json.Unmarshal([]byte(c.doc), &s); err != nil {
				t.Fatal(err)
			}
			s.Normalize()
			cfg := generator.Config{PackageName: "p", OmitEmpty: true, RootTypeName: "Root"}
			ir, err := generator.New(cfg).Generate(&s)
			if err != nil {
				t.Fatal(err)
			}
			src, err := e.Emit(ir)
			if err != nil {
				t.Fatal(err)
			}
			helpers, ok, err := e.EmitHelpers("p", generator.HelpersReferencedBy(string(src)))
			if err != nil || !ok {
				t.Fatalf("no helper file (%v)", err)
			}
			fset := token.NewFileSet()
			hf, err := parser.ParseFile(fset, "helpers.go", helpers, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			if hasEvaluator := bytes.Contains(helpers, []byte("type _schemaNode struct")); hasEvaluator != c.evaluator {
				t.Fatalf("the case is meant to carry the runtime evaluator: %v; it does: %v", c.evaluator, hasEvaluator)
			}
			got := map[string][]string{}
			for _, n := range topLevelNames(hf) {
				if b, ok := owner[n]; ok {
					got[b] = append(got[b], n)
				}
			}
			wantSet := map[string]bool{}
			for _, b := range c.want {
				wantSet[b] = true
			}
			for b, names := range got {
				if !wantSet[b] {
					t.Errorf("the helper file carries %s (%v), which nothing this schema generates calls", b, names)
				}
			}
			for _, b := range c.want {
				if len(got[b]) != len(blocks[b]) {
					t.Errorf("the helper file carries %d of %s's %d names: %v", len(got[b]), b, len(blocks[b]), got[b])
				}
			}
			// And the two files are one package that type-checks: a block the
			// closure left out is an undefined name here.
			tf, err := parser.ParseFile(fset, "types.go", src, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			conf := types.Config{Importer: imp}
			if _, err := conf.Check("p", fset, []*ast.File{tf, hf}, nil); err != nil {
				t.Errorf("the package does not type-check: %v", err)
			}
		})
	}
}
