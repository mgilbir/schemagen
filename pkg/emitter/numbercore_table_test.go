package emitter

import (
	"bytes"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// numberCoreSections are the templates the exact-number core is written in,
// rendered together under HelperSet.NumberCompare.
var numberCoreSections = []string{
	"number_reader_helpers",
	"number_value_helpers",
	"number_compare_helpers",
	"number_integer_helpers",
	"number_multipleof_helpers",
	"number_bigint_helpers",
	"number_decode_helpers",
}

// TestNumberCoreTableMatchesTheTemplates holds the generator's list of the
// names the exact-number core declares -- which a generated file's names are
// matched against to take the core -- to what its sections declare, rendered
// with and without the in-place decode whose lazily read values they read. A
// name added to a section and not to the list would never pull the core in
// for a file that named only it; a name left in the list after its
// declaration went would pull in nothing.
func TestNumberCoreTableMatchesTheTemplates(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	want := generator.NumberCoreDecls()
	sort.Strings(want)
	for _, decode := range []bool{false, true} {
		var src bytes.Buffer
		src.WriteString("package p\n")
		for _, name := range numberCoreSections {
			if err := e.tmpl.ExecuteTemplate(&src, name, generator.HelperSet{Decode: decode}); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		f, err := parser.ParseFile(token.NewFileSet(), "core.go", src.String(), parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("the core does not parse as Go on its own (decode %v): %v", decode, err)
		}
		if got := topLevelNames(f); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("the core (decode %v) declares %v; the generator's list is %v", decode, got, want)
		}
	}
}
