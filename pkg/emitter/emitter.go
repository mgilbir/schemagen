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
		PackageName: f.PackageName,
		Imports:     f.Imports,
		TypeDefs:    wrapTypeDefs(f.TypeDefs),
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

// fileData is the data passed to the top-level file template.
type fileData struct {
	PackageName string
	Imports     []generator.Import
	TypeDefs    []typeDefWrapper
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
