// Package emitter takes IR types from the generator package and emits
// formatted Go source code using Go templates.
package emitter

import (
	"bytes"
	"embed"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"text/template"
	"text/template/parse"

	"github.com/mgilbir/schemagen/pkg/emitter/internal/gocontext"
	"github.com/mgilbir/schemagen/pkg/generator"
)

//go:embed templates/*.go.tmpl
var templateFS embed.FS

// Emitter holds the parsed templates and produces Go source code from IR.
type Emitter struct {
	tmpl *template.Template
}

// New creates a new Emitter with all templates parsed and ready.
//
// The templates are parsed once per process and shared: they are embedded, so
// every Emitter would build the same thing. A parsed template is safe to
// execute from several goroutines at once, and nothing here changes it after
// it is built.
func New() (*Emitter, error) {
	sharedTemplatesOnce.Do(func() {
		sharedTemplates, sharedTemplatesErr = parseTemplates()
	})
	if sharedTemplatesErr != nil {
		return nil, sharedTemplatesErr
	}
	return &Emitter{tmpl: sharedTemplates}, nil
}

var (
	sharedTemplatesOnce sync.Once
	sharedTemplates     *template.Template
	sharedTemplatesErr  error
)

//go:generate go run ./internal/gocontext/guardgen

func parseTemplates() (*template.Template, error) {
	tmpl, err := parseTemplatesUnguarded()
	if err != nil {
		return nil, err
	}
	// Every action gets the guard for the Go context it writes into, before the
	// templates run for the first time. See internal/gocontext.
	if err := applyGuardTable(tmpl); err != nil {
		return nil, err
	}
	return tmpl, nil
}

func parseTemplatesUnguarded() (*template.Template, error) {
	tmpl, err := template.New("").Funcs(FuncMap()).ParseFS(templateFS, "templates/*.go.tmpl")
	if err != nil {
		return nil, fmt.Errorf("emitter: parsing templates: %w", err)
	}
	return tmpl, nil
}

// guardEntry is one row of guardTable: the output action at byte offset pos of
// the named template, and the guard appended to it.
type guardEntry struct {
	template string
	pos      parse.Pos
	guard    string
}

// applyGuardTable appends to every output action the guard guards_gen.go
// names for it.
//
// The table is the context analysis of internal/gocontext, run ahead of time:
// the analysis costs more than the rest of building the emitter, and its
// answer depends on the embedded templates alone. Two checks keep the table
// from answering for other templates than its own. The templates are hashed
// and must be the ones the table was computed from, which is what makes a
// template edited without `go generate ./pkg/emitter` an error here rather
// than a guard on the wrong action; and every output action must have exactly
// one entry, and every entry an action. TestGuardTableIsCurrent recomputes the
// table and holds the checked-in one to it.
func applyGuardTable(t *template.Template) error {
	sub, err := fs.Sub(templateFS, "templates")
	if err != nil {
		return err
	}
	hash, err := gocontext.TemplatesHash(sub)
	if err != nil {
		return fmt.Errorf("emitter: hashing templates: %w", err)
	}
	if hash != guardTableTemplatesHash {
		return fmt.Errorf("emitter: guards_gen.go was computed from other templates than the ones embedded; run go generate ./pkg/emitter")
	}
	// The table is in template-name order and, within a template, in position
	// order, which is the order forEachOutputAction visits a template's
	// actions in; so each template's rows are a run of the table and are
	// matched against its actions one for one, with no index to build.
	//
	// The nodes are allocated in blocks, one of each kind for the whole table:
	// this runs in every process that generates code, and a node at a time was
	// most of what it cost.
	n := len(guardTable)
	cmds := make([]parse.CommandNode, n)
	idents := make([]parse.IdentifierNode, n)
	args := make([]parse.Node, n)
	// Each pipeline's commands are copied into this, with the guard after
	// them, rather than appended to where each one grows its own array.
	pipes := make([]*parse.CommandNode, 0, 4*n)
	applied := 0
	for _, tt := range t.Templates() {
		if tt.Tree == nil || tt.Tree.Root == nil {
			continue
		}
		name := tt.Name()
		row, _ := slices.BinarySearchFunc(guardTable, name, func(e guardEntry, name string) int {
			return strings.Compare(e.template, name)
		})
		var missing error
		forEachOutputAction(tt.Tree.Root, func(a *parse.ActionNode) {
			if missing != nil {
				return
			}
			if row >= n || guardTable[row].template != name || guardTable[row].pos != a.Pos {
				loc, _ := tt.Tree.ErrorContext(a)
				missing = fmt.Errorf("emitter: %s: action %s has no entry in guards_gen.go; run go generate ./pkg/emitter", loc, a)
				return
			}
			id := &idents[row]
			id.NodeType, id.Ident = parse.NodeIdentifier, guardTable[row].guard
			id.SetTree(tt.Tree).SetPos(a.Pos)
			args[row] = id
			cmd := &cmds[row]
			cmd.NodeType, cmd.Pos, cmd.Args = parse.NodeCommand, a.Pos, args[row:row+1:row+1]
			start := len(pipes)
			pipes = append(append(pipes, a.Pipe.Cmds...), cmd)
			a.Pipe.Cmds = pipes[start:len(pipes):len(pipes)]
			row++
			applied++
		})
		if missing != nil {
			return missing
		}
		if row < n && guardTable[row].template == name {
			return fmt.Errorf("emitter: guards_gen.go has an entry for %s at %d, which is not an output action; run go generate ./pkg/emitter", name, guardTable[row].pos)
		}
	}
	if applied != len(guardTable) {
		return fmt.Errorf("emitter: guards_gen.go has %d entries for %d output actions; run go generate ./pkg/emitter", len(guardTable), applied)
	}
	return nil
}

// forEachOutputAction calls f for every action under n that writes output --
// every one that is not a {{$x := ...}} or {{$x = ...}}.
func forEachOutputAction(n parse.Node, f func(*parse.ActionNode)) {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, c := range n.Nodes {
			forEachOutputAction(c, f)
		}
	case *parse.ActionNode:
		if len(n.Pipe.Decl) == 0 {
			f(n)
		}
	case *parse.IfNode:
		forEachOutputAction(n.List, f)
		forEachOutputAction(n.ElseList, f)
	case *parse.RangeNode:
		forEachOutputAction(n.List, f)
		forEachOutputAction(n.ElseList, f)
	case *parse.WithNode:
		forEachOutputAction(n.List, f)
		forEachOutputAction(n.ElseList, f)
	}
}

// checkPackageIdentifiers refuses a package clause or an import alias that is
// not a Go identifier. Both are written into code as they stand -- there is no
// escaping an identifier -- and neither comes from the naming layer: the
// package name is the caller's, and an alias can be derived from an import
// path the caller supplied. A name like "a-b" used to reach gofmt and fail
// there with a dump of the whole file, and one holding a newline could put a
// declaration of its own at the top of the file.
func checkPackageIdentifiers(pkg string, imports []generator.Import) error {
	if !isPackageIdentifier(pkg) {
		return fmt.Errorf("emitter: package name %q is not a Go identifier", pkg)
	}
	for _, imp := range imports {
		if imp.Alias != "" && imp.Alias != "_" && imp.Alias != "." && !isPackageIdentifier(imp.Alias) {
			return fmt.Errorf("emitter: import alias %q for %q is not a Go identifier", imp.Alias, imp.Path)
		}
	}
	return nil
}

// isPackageIdentifier reports whether s can name a package or an import under
// every Go the generated code supports: generator.IsIdentifier answers from the
// oldest supported Go's Unicode tables, where token.IsIdentifier would answer
// from the running one's.
func isPackageIdentifier(s string) bool {
	return generator.IsIdentifier(s) && s != "_"
}

// Emit takes a generator.File and returns gofmt-formatted Go source code.
func (e *Emitter) Emit(f *generator.File) ([]byte, error) {
	// Every file names the runtime module (its API-level marker at the least),
	// whether or not the model of imports that built f claimed it: a File built
	// by hand is as good as one the generator built.
	imports := f.Imports
	if !slices.ContainsFunc(imports, func(imp generator.Import) bool { return imp.Path == generator.RuntimeImportPath }) {
		imports = append(slices.Clone(imports), generator.GeneratedImport(generator.RuntimeImportPath))
	}
	if err := checkPackageIdentifiers(f.PackageName, imports); err != nil {
		return nil, err
	}
	data := fileData{
		PackageName:          f.PackageName,
		Imports:              imports,
		TypeDefs:             wrapTypeDefs(f.TypeDefs),
		ValidationCapability: f.ValidationCapability,
		UnresolvedRefs:       f.UnresolvedRefs,
		UndeclaredRefTypes:   f.UndeclaredRefTypes,
		ElementNodes:         f.ElementNodes,
		AccessMachine:        f.AccessMachine,
		AccessMachineVar:     f.AccessMachineVar,
	}

	var buf bytes.Buffer
	if err := e.tmpl.ExecuteTemplate(&buf, "file.go.tmpl", data); err != nil {
		return nil, fmt.Errorf("emitter: executing template: %w", err)
	}
	if kept, dropped := keepReferencedImports(buf.Bytes(), data.Imports); dropped {
		data.Imports = kept
		buf.Reset()
		if err := e.tmpl.ExecuteTemplate(&buf, "file.go.tmpl", data); err != nil {
			return nil, fmt.Errorf("emitter: executing template: %w", err)
		}
	}

	src, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("emitter: formatting output: %w\nraw output:\n%s", err, buf.String())
	}
	return src, nil
}

// keepReferencedImports returns the subset of imports the rendered file
// actually names, and whether anything was dropped.
//
// The import list is built by a hand-written model of what the templates emit
// (generator.addRequiredImports, and the block in EmitHelpers below). That
// model is a second copy of a decision the templates already make, so it drifts
// -- and an import nothing refers to is not a cosmetic blemish in Go, it is a
// compile error in the file we just wrote. Issue #202 is one instance: a struct
// whose only null rule reaches *below* a property is emitted as a call to the
// shared walker, which needs no fmt, while the model claimed fmt for every null
// rule. That defect can be fixed at the model, and is; this pass is what stops
// the next one from reaching a user, because no amount of validation testing
// looks at whether the output compiles.
//
// Dropping is the safe direction. The model is only ever consulted as "which
// packages might this file name", and a package the rendered text never
// qualifies cannot be needed by it. Under-claiming -- an import the file does
// name and the model omitted -- is a different defect, equally fatal and not
// one this pass can repair; only a compile of the generated file catches that.
func keepReferencedImports(rendered []byte, imports []generator.Import) ([]generator.Import, bool) {
	if len(imports) == 0 {
		return imports, false
	}
	used, ok := packageQualifiers(rendered)
	if !ok {
		// Unparseable output: leave the list alone and let format.Source report
		// the syntax error, which says far more than a missing import would.
		return imports, false
	}
	kept := make([]generator.Import, 0, len(imports))
	for _, imp := range imports {
		name := imp.Alias
		if name == "" {
			name = generator.PackageNameForImportPath(imp.Path)
		}
		// A blank or dot import is not referred to by name; nothing generated
		// here emits one, but neither is silently deleting one this pass's
		// business.
		if name == "_" || name == "." || used[name] {
			kept = append(kept, imp)
		}
	}
	return kept, len(kept) != len(imports)
}

// packageQualifiers collects every identifier the source uses on the left of a
// selector -- `fmt` in `fmt.Errorf`, `json` in `json.RawMessage`. A local
// variable sharing a package's name would be counted too, which keeps an import
// that could have gone: erring towards the list the model already produced.
func packageQualifiers(src []byte) (map[string]bool, bool) {
	file, err := parser.ParseFile(token.NewFileSet(), "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, false
	}
	used := make(map[string]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if ident, ok := sel.X.(*ast.Ident); ok {
				used[ident.Name] = true
			}
		}
		return true
	})
	return used, true
}

// EmitHelpers renders the shared helper file for a destination package.
//
// The helpers proper -- the decoder, the encoder, the identity of a value, the
// number core, the format checkers, the schema evaluator -- are not in it: they
// are the runtime module's, imported by every generated file. What is left to
// declare per package is what the package's own schemas supply, which is the
// regular expressions they name, held as package-level variables compiled once
// when the package is initialised.
//
// Those are package-level, so a package containing two schemas that both name
// one would declare it twice and fail to compile. They live in a single file per
// package instead. Returns ok=false when the set is empty and no file should be
// written.
func (e *Emitter) EmitHelpers(packageName string, helpers generator.HelperSet) ([]byte, bool, error) {
	if helpers.Empty() {
		return nil, false, nil
	}
	// The spec -- and so the name the file spells the package under -- is the
	// generator's (generator.GeneratedImport), which is the table its name
	// registry reserves those names from.
	imports := []generator.Import{generator.GeneratedImport(generator.RuntimeImportPath)}
	if err := checkPackageIdentifiers(packageName, imports); err != nil {
		return nil, false, err
	}
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
	// UnresolvedRefs renders the file-level NOT VALIDATED banner. See
	// generator.File.UnresolvedRefs.
	UnresolvedRefs []string
	// UndeclaredRefTypes renders the DOES NOT COMPILE half of that banner. See
	// generator.File.UndeclaredRefTypes.
	UndeclaredRefTypes []generator.UndeclaredRefType
	// ElementNodes are declared after the types. See generator.ElementNode.
	ElementNodes []*generator.ElementNode
	// AccessMachine is declared as AccessMachineVar after the types. See
	// generator.File.AccessMachine.
	AccessMachine    []generator.AccessState
	AccessMachineVar string
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
