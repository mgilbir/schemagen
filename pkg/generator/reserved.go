package generator

import "fmt"

const (
	// RuntimeImportPath is the runtime module's package every generated package
	// imports: the code that is the same for every schema (the decoder, the
	// encoder, the exact number core, the format checkers, the schema
	// evaluator) lives there once, instead of in a copy per generated package.
	RuntimeImportPath = "github.com/mgilbir/schemagen/runtime"

	// RuntimeAlias is the name generated code refers to the runtime package by.
	// The templates and the generator's own strings spell it as fixed text; a
	// test holds them to this constant. It is reserved, so no schema-derived
	// name can land on it.
	RuntimeAlias = "rt"
)

// The identifiers generated code spells as fixed text, which the name registry
// holds before any schema-derived name is claimed (see names.go). A schema
// cannot move them: the templates that refer to them are not rewritten per
// schema, so a type, a constant or an import alias landing on one of them is a
// package that does not compile -- or, for an import alias, a file whose own
// helper calls resolve to the wrong package.
//
// The two tables below are held to the templates by tests in pkg/emitter that
// render every helper and read back what the rendering declares and imports, in
// both directions: a helper added to a template without being listed here fails,
// and an entry nothing declares any more fails too.

// generatedImport is one package a generated file may import, and the name the
// file spells it under.
type generatedImport struct {
	Path string
	// Name is the identifier the file refers to the package by.
	Name string
	// Alias is written in the import spec when the package's own name is not
	// Name (or is not derivable from the path, as for goecma262).
	Alias string
}

// generatedImports is every import a generated file -- a schema's file or the
// package's helper file -- can carry. addRequiredImports and the emitter's
// helper-file imports both go through GeneratedImport, which refuses a path
// that is not here, so an import cannot be added without its name being
// reserved. The list is short because the code that needed the rest (the
// decoder, the encoder, the number core, the format checkers) is in the runtime
// module, which is imported as one package.
var generatedImports = []generatedImport{
	{Path: "bytes", Name: "bytes"},
	{Path: "encoding/json", Name: "json"},
	{Path: "fmt", Name: "fmt"},
	{Path: RuntimeImportPath, Name: RuntimeAlias, Alias: RuntimeAlias},
	{Path: "math", Name: "math"},
	{Path: "math/big", Name: "big"},
	{Path: "net/mail", Name: "mail"},
	{Path: "net/netip", Name: "netip"},
	{Path: "net/url", Name: "url"},
	{Path: "strings", Name: "strings"},
	{Path: "time", Name: "time"},
	{Path: "unicode/utf8", Name: "utf8"},
}

// GeneratedImport returns the import spec a generated file uses for path. It
// panics for a path the generator has not reserved a name for: that is a
// template or an import model gaining a dependency without the name registry
// knowing, which would let a cross-package alias or a helper land on the name.
// The panic is reached only by schemagen's own code paths, never by input.
func GeneratedImport(path string) Import {
	for _, imp := range generatedImports {
		if imp.Path == path {
			return Import{Path: imp.Path, Alias: imp.Alias}
		}
	}
	panic(fmt.Sprintf("schemagen: generated code imports %q, which reserved.go does not list; add it to generatedImports so the name registry holds its name", path))
}

// GeneratedImportNames lists the name each reserved import is spelled under,
// for the tests that hold this table to the templates.
func GeneratedImportNames() map[string]string {
	out := make(map[string]string, len(generatedImports))
	for _, imp := range generatedImports {
		out[imp.Path] = imp.Name
	}
	return out
}

// helperIdentifiers is every package-level identifier a generated package
// declares under a name that does not come from a schema: the constructors of
// the validation-capability block of a schema file. The helpers themselves are
// the runtime module's (github.com/mgilbir/schemagen/runtime), reached as
// rt.Name, so their names are in that package's scope and not the generated
// package's; the pattern variables are named by PatternVarName and cannot
// collide with anything a schema derives, which is why they are not listed.
var helperIdentifiers = []string{
	"SchemagenValidationCapability",
	"SchemagenValidationMode",
	"SchemagenValidationRuntimeFeatures",
}

// HelperIdentifiers lists helperIdentifiers, for the tests that hold it to the
// templates.
func HelperIdentifiers() []string {
	return append([]string(nil), helperIdentifiers...)
}

// predeclaredIdentifiers is Go's universe scope. Declaring one of them at
// package scope is legal Go and shadows it for the whole package -- including
// the generated code that calls len, compares to nil and converts to string --
// and a --schema-package named "string" or "len" is an import alias that does
// the same to one file. The list is the union over every Go release that can
// compile generated code; a test holds it to the running toolchain's
// types.Universe.
var predeclaredIdentifiers = []string{
	"any", "append", "bool", "byte", "cap", "clear", "close", "comparable",
	"complex", "complex128", "complex64", "copy", "delete", "error", "false",
	"float32", "float64", "imag", "int", "int16", "int32", "int64", "int8",
	"iota", "len", "make", "max", "min", "new", "nil", "panic", "print",
	"println", "real", "recover", "rune", "string", "true", "uint", "uint16",
	"uint32", "uint64", "uint8", "uintptr",
}

// PredeclaredIdentifiers lists predeclaredIdentifiers, for the test that holds
// it to the running toolchain.
func PredeclaredIdentifiers() []string {
	return append([]string(nil), predeclaredIdentifiers...)
}
