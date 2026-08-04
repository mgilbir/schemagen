// Package emitter takes IR types from the generator package and emits
// formatted Go source code using Go templates.
package emitter

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"text/template"

	"github.com/mgilbir/schemagen/pkg/generator"
)

//go:embed templates/*.go.tmpl
var templateFS embed.FS

// Emitter holds the parsed templates and produces Go source code from IR.
type Emitter struct {
	tmpl *template.Template
}

// New creates a new Emitter with all templates parsed and ready.
func New() (*Emitter, error) {
	tmpl, err := template.New("").Funcs(FuncMap()).ParseFS(templateFS, "templates/*.go.tmpl")
	if err != nil {
		return nil, fmt.Errorf("emitter: parsing templates: %w", err)
	}
	return &Emitter{tmpl: tmpl}, nil
}

// Emit takes a generator.File and returns gofmt-formatted Go source code.
func (e *Emitter) Emit(f *generator.File) ([]byte, error) {
	data := fileData{
		PackageName:          f.PackageName,
		Imports:              f.Imports,
		TypeDefs:             wrapTypeDefs(f.TypeDefs),
		ValidationCapability: f.ValidationCapability,
	}

	var buf bytes.Buffer
	if err := e.tmpl.ExecuteTemplate(&buf, "file.go.tmpl", data); err != nil {
		return nil, fmt.Errorf("emitter: executing template: %w", err)
	}

	src, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("emitter: formatting output: %w\nraw output:\n%s", err, buf.String())
	}
	return src, nil
}

// EmitHelpers renders the shared helper file for a destination package.
//
// Helper functions are package-level, so a package containing two schemas that
// both need one would declare it twice and fail to compile. They live in a
// single file per package instead. Returns ok=false when the set is empty and
// no file should be written.
func (e *Emitter) EmitHelpers(packageName string, helpers generator.HelperSet) ([]byte, bool, error) {
	if helpers.Empty() {
		return nil, false, nil
	}

	// Imports are fixed by which helpers are included, not by the schemas.
	var imports []generator.Import
	// Each path is added at most once: the list goes straight into the file's
	// import block, and naming the same package twice does not compile.
	addAliased := func(cond bool, path, alias string) {
		if !cond {
			return
		}
		for _, existing := range imports {
			if existing.Path == path {
				return
			}
		}
		imports = append(imports, generator.Import{Path: path, Alias: alias})
	}
	add := func(cond bool, path string) { addAliased(cond, path, "") }
	add(helpers.Dynamic || helpers.DynamicConst || helpers.OneOf || helpers.OneOfDiscriminator || helpers.Integer || helpers.NullCheck, "encoding/json")
	add(helpers.OneOfDiscriminator || helpers.Integer || helpers.NullCheck || helpers.Format, "fmt")
	add(helpers.Dynamic || helpers.Integer, "math")
	add(helpers.Annotations, "reflect")
	add(helpers.Annotations, "strconv")
	// The regexp engine only comes in when a compiled schema actually names a
	// pattern: it is a third-party dependency, and a package that never asks for
	// one should not acquire it. The format block needs the same engine for
	// `format: regex`, so both routes go through addAliased rather than
	// appending: a package that compiles a pattern to the runtime evaluator
	// *and* asserts a format reaches this path from both sides, and the list
	// goes straight into the import block. go/format happens to drop a
	// duplicate spec on its way out, which is why appending twice was never
	// seen to break anything -- but that is the formatter's tidying and not a
	// property of the list, and this list is what the rest of this function is
	// written to keep unique.
	addAliased(helpers.AnnotationsPattern, "github.com/mgilbir/goecma262", "ecma262")
	addAliased(helpers.AnnotationsPattern, "github.com/mgilbir/goecma262/flags", "ecmaflags")
	// The walker reports the first offending key in name order, so that a
	// document with several of them fails the same way every time. The runtime
	// evaluator visits an object's properties in the same fixed order, for the
	// same reason.
	add(helpers.NullCheck || helpers.Annotations, "sort")
	// The format helpers are emitted as one block, so they name every package
	// any of them needs whether or not the schema uses that particular format.
	// Splitting the block per format is what would let a package end up with a
	// helper it cannot compile; see HelperSet.Format.
	add(helpers.Format, "net/netip")
	add(helpers.Format, "net/url")
	add(helpers.Format, "regexp")
	add(helpers.Format, "strings")
	add(helpers.Format, "time")
	addAliased(helpers.Format, "github.com/mgilbir/goecma262", "ecma262")
	addAliased(helpers.Format, "github.com/mgilbir/goecma262/flags", "ecmaflags")
	// The hostname block is separate for one reason: x/net/idna. A package whose
	// schemas name no hostname, email or idn-* format neither emits these
	// functions nor imports the module, which is the whole point of the split --
	// generated code putting a dependency on its caller is a real imposition, so
	// it is confined to callers whose schemas ask for it. See
	// HelperSet.FormatHostname.
	add(helpers.FormatHostname, "net/mail")
	add(helpers.FormatHostname, "net/netip")
	add(helpers.FormatHostname, "strings")
	add(helpers.FormatHostname, "unicode")
	add(helpers.FormatHostname, "unicode/utf8")
	add(helpers.FormatHostname, "fmt")
	add(helpers.FormatHostname, "golang.org/x/net/idna")

	data := helperFileData{
		PackageName: packageName,
		Imports:     imports,
		Helpers:     helpers,
	}

	var buf bytes.Buffer
	if err := e.tmpl.ExecuteTemplate(&buf, "helpers_file.go.tmpl", data); err != nil {
		return nil, false, fmt.Errorf("emitter: executing helper template: %w", err)
	}
	src, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, false, fmt.Errorf("emitter: formatting helper output: %w\nraw output:\n%s", err, buf.String())
	}
	return src, true, nil
}

// helperFileData is the data passed to the shared helper file template.
type helperFileData struct {
	PackageName string
	Imports     []generator.Import
	Helpers     generator.HelperSet
}

// fileData is the data passed to the top-level file template.
type fileData struct {
	PackageName          string
	Imports              []generator.Import
	TypeDefs             []typeDefWrapper
	ValidationCapability generator.ValidationCapability
}

func (d fileData) HasValidationCapability() bool {
	return d.NeedsValidationRuntime()
}

func (d fileData) NeedsValidationRuntime() bool {
	return d.ValidationCapability.RequiresRuntime && d.ValidationCapability.Mode != generator.ValidationModeStatic
}

// HasDynamicSchema returns true if the file contains a schema validated against
// an untyped value, which needs the _dyn* helpers.
func (d fileData) HasDynamicSchema() bool {
	for _, td := range d.TypeDefs {
		if _, ok := td.Def.(*generator.DynamicSchemaDef); ok {
			return true
		}
	}
	return false
}

// HasOneOf returns true if any struct in the file has oneOf fields.
func (d fileData) HasOneOf() bool {
	for _, td := range d.TypeDefs {
		if s, ok := td.Def.(*generator.StructDef); ok && len(s.OneOfs) > 0 {
			return true
		}
	}
	return false
}

// HasDiscriminatedOneOf returns true if any struct in the file has a discriminator-based oneOf.
func (d fileData) HasDiscriminatedOneOf() bool {
	for _, td := range d.TypeDefs {
		if s, ok := td.Def.(*generator.StructDef); ok {
			for _, oof := range s.OneOfs {
				if oof.HasDiscriminator() {
					return true
				}
			}
		}
	}
	return false
}

// HasValidation returns true if any type in the file has validation rules.
func (d fileData) HasValidation() bool {
	for _, td := range d.TypeDefs {
		if s, ok := td.Def.(*generator.StructDef); ok {
			if len(s.Validations) > 0 || s.HasRequiredFields() {
				return true
			}
			if s.AdditionalProperties != nil && s.AdditionalProperties.Forbidden {
				return true
			}
		}
		if a, ok := td.Def.(*generator.AliasDef); ok && len(a.Validations) > 0 {
			return true
		}
	}
	return false
}

// typeDefWrapper wraps a generator.TypeDef so that templates can dispatch
// on the concrete type without a type switch (which Go templates don't support).
type typeDefWrapper struct {
	Def generator.TypeDef
}

// IsStruct reports whether the wrapped TypeDef is a *generator.StructDef.
func (w typeDefWrapper) IsStruct() bool {
	_, ok := w.Def.(*generator.StructDef)
	return ok
}

// IsEnum reports whether the wrapped TypeDef is a *generator.EnumDef.
func (w typeDefWrapper) IsEnum() bool {
	_, ok := w.Def.(*generator.EnumDef)
	return ok
}

// IsAlias reports whether the wrapped TypeDef is a *generator.AliasDef.
func (w typeDefWrapper) IsAlias() bool {
	_, ok := w.Def.(*generator.AliasDef)
	return ok
}

// AsStruct returns the wrapped TypeDef as a *generator.StructDef, or nil.
func (w typeDefWrapper) AsStruct() *generator.StructDef {
	s, _ := w.Def.(*generator.StructDef)
	return s
}

// AsEnum returns the wrapped TypeDef as a *generator.EnumDef, or nil.
func (w typeDefWrapper) AsEnum() *generator.EnumDef {
	e, _ := w.Def.(*generator.EnumDef)
	return e
}

// AsAlias returns the wrapped TypeDef as a *generator.AliasDef, or nil.
func (w typeDefWrapper) AsAlias() *generator.AliasDef {
	a, _ := w.Def.(*generator.AliasDef)
	return a
}

// IsInferredAlias reports whether the wrapped TypeDef is a *generator.InferredAliasDef.
func (w typeDefWrapper) IsInferredAlias() bool {
	_, ok := w.Def.(*generator.InferredAliasDef)
	return ok
}

// AsInferredAlias returns the wrapped TypeDef as a *generator.InferredAliasDef, or nil.
func (w typeDefWrapper) AsInferredAlias() *generator.InferredAliasDef {
	d, _ := w.Def.(*generator.InferredAliasDef)
	return d
}

// IsBigIntAlias reports whether the wrapped TypeDef is a *generator.BigIntAliasDef.
func (w typeDefWrapper) IsBigIntAlias() bool {
	_, ok := w.Def.(*generator.BigIntAliasDef)
	return ok
}

// AsBigIntAlias returns the wrapped TypeDef as a *generator.BigIntAliasDef, or nil.
func (w typeDefWrapper) AsBigIntAlias() *generator.BigIntAliasDef {
	d, _ := w.Def.(*generator.BigIntAliasDef)
	return d
}

// IsAnnotationSchema reports whether the wrapped TypeDef is a *generator.AnnotationSchemaDef.
func (w typeDefWrapper) IsAnnotationSchema() bool {
	_, ok := w.Def.(*generator.AnnotationSchemaDef)
	return ok
}

// AsAnnotationSchema returns the wrapped TypeDef as a *generator.AnnotationSchemaDef, or nil.
func (w typeDefWrapper) AsAnnotationSchema() *generator.AnnotationSchemaDef {
	d, _ := w.Def.(*generator.AnnotationSchemaDef)
	return d
}

// IsDynamicSchema reports whether the wrapped TypeDef is a *generator.DynamicSchemaDef.
func (w typeDefWrapper) IsDynamicSchema() bool {
	_, ok := w.Def.(*generator.DynamicSchemaDef)
	return ok
}

// AsDynamicSchema returns the wrapped TypeDef as a *generator.DynamicSchemaDef, or nil.
func (w typeDefWrapper) AsDynamicSchema() *generator.DynamicSchemaDef {
	d, _ := w.Def.(*generator.DynamicSchemaDef)
	return d
}

// IsNotSchema reports whether the wrapped TypeDef is a *generator.NotSchemaDef.
func (w typeDefWrapper) IsNotSchema() bool {
	_, ok := w.Def.(*generator.NotSchemaDef)
	return ok
}

// AsNotSchema returns the wrapped TypeDef as a *generator.NotSchemaDef, or nil.
func (w typeDefWrapper) AsNotSchema() *generator.NotSchemaDef {
	d, _ := w.Def.(*generator.NotSchemaDef)
	return d
}

// IsTypeOnlySchema reports whether the wrapped TypeDef is a *generator.TypeOnlySchemaDef.
func (w typeDefWrapper) IsTypeOnlySchema() bool {
	_, ok := w.Def.(*generator.TypeOnlySchemaDef)
	return ok
}

// AsTypeOnlySchema returns the wrapped TypeDef as a *generator.TypeOnlySchemaDef, or nil.
func (w typeDefWrapper) AsTypeOnlySchema() *generator.TypeOnlySchemaDef {
	d, _ := w.Def.(*generator.TypeOnlySchemaDef)
	return d
}

// wrapTypeDefFunc is the template function that wraps a TypeDef.
// It handles both generator.TypeDef and already-wrapped typeDefWrapper values.
func wrapTypeDefFunc(td any) typeDefWrapper {
	switch v := td.(type) {
	case typeDefWrapper:
		return v
	case generator.TypeDef:
		return typeDefWrapper{Def: v}
	default:
		return typeDefWrapper{}
	}
}

// wrapTypeDefs converts a slice of generator.TypeDef to typeDefWrapper.
func wrapTypeDefs(defs []generator.TypeDef) []typeDefWrapper {
	out := make([]typeDefWrapper, len(defs))
	for i, d := range defs {
		out[i] = typeDefWrapper{Def: d}
	}
	return out
}
