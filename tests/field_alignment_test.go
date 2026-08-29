package tests

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/emitter"
)

// TestGeneratedCorpusIsFieldAligned generates every schema in the corpus,
// compiles it, and asks the *running program* whether any of the structs it
// declared would be smaller, or cheaper for the garbage collector to scan, with
// their fields in another order.
//
// It is an oracle rather than a second opinion. pkg/generator/layout.go decides
// the order from a transcription of cmd/compile's rules; the checker built below
// walks reflect.Type, which is what the compiler actually did, and reaches its
// verdict without consulting the generator at all. A mistake in the
// transcription -- a standard library type whose fields moved, a template that
// grew a member the model does not know about, an arm of the layout table
// reporting the wrong shape for a kind of declaration -- shows up here as a
// named type with a cheaper order, and shows up nowhere else at all: the
// generated code still compiles, still round-trips and still validates. It is
// only bigger.
//
// The verdict is fieldalignment's, and the intent is that running
// `fieldalignment ./...` over this same output reports nothing. Its two
// diagnostics are reproduced exactly -- a struct that could be smaller, and a
// struct of the same size whose pointers could sit closer to the front -- from
// the gcSizes model that analyzer measures with. What is not covered here is the
// struct types declared inside a function body, which no reflect walk can reach;
// `make lint-alignment` runs the analyzer itself over the same corpus and does
// see those.
func TestGeneratedCorpusIsFieldAligned(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the whole generated corpus")
	}

	schemas := corpusSchemaPaths(t)
	sort.Strings(schemas)
	if len(schemas) == 0 {
		t.Fatalf("no schemas found under %s", filepath.Join("..", "testdata", "schemas"))
	}

	em, err := emitter.New()
	if err != nil {
		t.Fatalf("emitter.New: %v", err)
	}

	dir := t.TempDir()
	if err := writeTestGoMod(dir, alignModule); err != nil {
		t.Fatal(err)
	}

	// pkgSchema maps the generated package back to the schema it came from, so
	// a report names a file someone can open rather than a directory in a temp
	// tree that no longer exists.
	pkgSchema := make(map[string]string, len(schemas))
	var pkgs []string
	measured := 0
	for i, path := range schemas {
		src, helpers, ok := generateForCompile(t, em, path)
		if !ok {
			continue
		}
		name := fmt.Sprintf("p%04d", i)
		sub := filepath.Join(dir, name)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "types.go"), src, 0o644); err != nil {
			t.Fatal(err)
		}
		if len(helpers) > 0 {
			if err := os.WriteFile(filepath.Join(sub, "helpers.go"), helpers, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		structs := declaredStructNames(t, path, src, helpers)
		if len(structs) == 0 {
			// A schema whose whole output is aliases and enums declares no
			// struct. Nothing to measure, and nothing to report.
			continue
		}
		if err := os.WriteFile(filepath.Join(sub, "alignz.go"), []byte(alignTypesFile(structs)), 0o644); err != nil {
			t.Fatal(err)
		}
		pkgSchema[name] = path
		pkgs = append(pkgs, name)
		measured += len(structs)
	}
	if len(pkgs) == 0 {
		t.Fatal("no package declared a struct; the gate is measuring nothing")
	}

	checkDir := filepath.Join(dir, "aligncheck")
	if err := os.MkdirAll(checkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkDir, "main.go"), []byte(alignCheckMain(pkgs)), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Logf("measuring %d struct declarations across %d packages generated from %d corpus schemas",
		measured, len(pkgs), len(schemas))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./aligncheck")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	report := string(output)
	for name, path := range pkgSchema {
		report = strings.ReplaceAll(report, name+".", path+" -> ")
		report = strings.ReplaceAll(report, alignModule+"/"+name, path)
	}
	if err == nil {
		return
	}
	if ctx.Err() != nil {
		t.Fatalf("the alignment check did not finish: %v\n%s", ctx.Err(), report)
	}
	t.Errorf("generated structs are not laid out as cheaply as they could be (%v).\n"+
		"Each line names a declaration, what it costs, and what the same fields cost in the best order.\n"+
		"pkg/generator/layout.go chose the order; either its model of some type is wrong, or a template "+
		"declares a member the model does not know it declares.\n%s", err, report)
}

// alignModule is the module the corpus is built as. It is spelled out because
// the report rewriting above matches on it.
const alignModule = "corpus_align_test"

// declaredStructNames lists every struct type declared at the top level of the
// emitted source, by parsing it.
//
// Parsing rather than reading the IR is deliberate: it sees the declarations the
// IR has no node for -- the oneOf variant wrappers the struct template mints,
// and every hand-written helper in helpers.go -- and it cannot fall out of step
// with what the templates emit, because it is reading what they emitted.
func declaredStructNames(t *testing.T, schemaPath string, files ...[]byte) []string {
	t.Helper()
	var names []string
	for _, src := range files {
		if len(src) == 0 {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), "types.go", src, parser.SkipObjectResolution)
		if err != nil {
			// Unparseable output is a different gate's business
			// (TestGeneratedCorpusCompiles), and it fails there with the
			// compiler's own words.
			t.Logf("%s: emitted source does not parse, so its structs are not measured here: %v", schemaPath, err)
			continue
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Assign.IsValid() {
					continue
				}
				if _, ok := ts.Type.(*ast.StructType); ok {
					names = append(names, ts.Name.Name)
				}
			}
		}
	}
	sort.Strings(names)
	return names
}

// alignTypesFile is the file added to each generated package so that the
// checker can reach its declarations. It is in the package rather than importing
// it, which is what lets it name the unexported helper types too.
// The Go package name every corpus package is generated under, which is
// generateForCompile's, and is not the directory name -- so the checker imports
// each directory under an alias of its own.
func alignTypesFile(structs []string) string {
	var b strings.Builder
	b.WriteString("package gen\n\nimport \"reflect\"\n\n")
	b.WriteString("// AlignTypes is every struct type this package declares, for the\n")
	b.WriteString("// field-alignment gate. Written by TestGeneratedCorpusIsFieldAligned.\n")
	b.WriteString("var AlignTypes = []reflect.Type{\n")
	for _, name := range structs {
		fmt.Fprintf(&b, "\treflect.TypeOf((*%s)(nil)).Elem(),\n", name)
	}
	b.WriteString("}\n")
	return b.String()
}

// alignCheckMain is the program that does the measuring. It is a program rather
// than a library call because the only way to ask the compiler what it did with
// a declaration is to run code the compiler compiled.
func alignCheckMain(pkgs []string) string {
	var b strings.Builder
	b.WriteString("// Command aligncheck reports every struct in the generated corpus that\n")
	b.WriteString("// would be smaller, or cheaper to scan, with its fields in another order.\n")
	b.WriteString("package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\t\"reflect\"\n\t\"sort\"\n\n")
	for _, pkg := range pkgs {
		fmt.Fprintf(&b, "\t%s %q\n", pkg, alignModule+"/"+pkg)
	}
	b.WriteString(")\n\n")
	b.WriteString("var packages = map[string][]reflect.Type{\n")
	for _, pkg := range pkgs {
		fmt.Fprintf(&b, "\t%q: %s.AlignTypes,\n", pkg, pkg)
	}
	b.WriteString("}\n")
	b.WriteString(alignCheckBody)
	return b.String()
}

// alignCheckBody is cmd/compile's layout rules over reflect.Type, and
// fieldalignment's choice of the best order over the result.
//
// It is a transcription of the analyzer, not of pkg/generator/layout.go, and it
// is written against a different input -- the compiled type rather than the IR.
// Two transcriptions of the same rules from different sources agreeing is the
// evidence this gate offers; sharing code between them would remove exactly the
// disagreement it exists to find.
const alignCheckBody = `
const wordSize = int64(8)

// ptrdata is the offset just past the last byte of a value that can hold a
// pointer: what the garbage collector has to scan.
func ptrdata(t reflect.Type) int64 {
	switch t.Kind() {
	case reflect.String, reflect.UnsafePointer,
		reflect.Chan, reflect.Map, reflect.Pointer, reflect.Func, reflect.Slice:
		return wordSize
	case reflect.Interface:
		return 2 * wordSize
	case reflect.Array:
		n := int64(t.Len())
		if n == 0 {
			return 0
		}
		elem := ptrdata(t.Elem())
		if elem == 0 {
			return 0
		}
		return (n-1)*int64(t.Elem().Size()) + elem
	case reflect.Struct:
		var last int64
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if p := ptrdata(f.Type); p != 0 {
				last = int64(f.Offset) + p
			}
		}
		return last
	}
	return 0
}

type field struct {
	align   int64
	size    int64
	ptrdata int64
}

func alignUp(x, a int64) int64 {
	y := x + a - 1
	return y - y%a
}

// lay reports what a struct of these fields, in this order, costs.
func lay(fields []field) (size, ptr int64) {
	if len(fields) == 0 {
		return 0, 0
	}
	var offset int64
	widest := int64(1)
	for i, f := range fields {
		if f.align > widest {
			widest = f.align
		}
		start := alignUp(offset, f.align)
		if f.ptrdata != 0 {
			ptr = start + f.ptrdata
		}
		sz := f.size
		if i == len(fields)-1 && sz == 0 && offset != 0 {
			sz = 1
		}
		offset = start + sz
	}
	return alignUp(offset, widest), ptr
}

// best is fieldalignment's optimalOrder: zero-sized first, then most tightly
// aligned, then pointerful ahead of pointer-free, then least trailing
// non-pointer bytes, then largest.
func best(fields []field) []field {
	out := append([]field(nil), fields...)
	sort.SliceStable(out, func(i, j int) bool {
		fi, fj := out[i], out[j]
		if zi, zj := fi.size == 0, fj.size == 0; zi != zj {
			return zi
		}
		if fi.align != fj.align {
			return fi.align > fj.align
		}
		ni, nj := fi.ptrdata == 0, fj.ptrdata == 0
		if ni != nj {
			return nj
		}
		if !ni {
			if ti, tj := fi.size-fi.ptrdata, fj.size-fj.ptrdata; ti != tj {
				return ti < tj
			}
		}
		return fi.size > fj.size
	})
	return out
}

func main() {
	names := make([]string, 0, len(packages))
	for name := range packages {
		names = append(names, name)
	}
	sort.Strings(names)

	var findings []string
	measured := 0
	for _, pkg := range names {
		for _, t := range packages[pkg] {
			measured++
			fields := make([]field, t.NumField())
			for i := 0; i < t.NumField(); i++ {
				ft := t.Field(i).Type
				fields[i] = field{int64(ft.Align()), int64(ft.Size()), ptrdata(ft)}
			}
			// Measured from the compiled type, not from the model above: this
			// is the layout the compiler chose for the declaration as written.
			actualSize, actualPtr := int64(t.Size()), ptrdata(t)
			optimalSize, optimalPtr := lay(best(fields))
			switch {
			case actualSize != optimalSize:
				findings = append(findings, fmt.Sprintf("%s.%s: size %d, but the same fields fit in %d",
					pkg, t.Name(), actualSize, optimalSize))
			case actualPtr != optimalPtr:
				findings = append(findings, fmt.Sprintf("%s.%s: %d leading bytes of pointer data, but the same fields need only %d scanned",
					pkg, t.Name(), actualPtr, optimalPtr))
			}
		}
	}

	if measured == 0 {
		fmt.Fprintln(os.Stderr, "no struct types reached the checker; it is measuring nothing")
		os.Exit(1)
	}
	if len(findings) == 0 {
		return
	}
	for _, f := range findings {
		fmt.Fprintln(os.Stderr, f)
	}
	fmt.Fprintf(os.Stderr, "\n%d of %d struct declarations are not laid out as cheaply as they could be\n",
		len(findings), measured)
	os.Exit(1)
}
`
