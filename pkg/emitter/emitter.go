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
	if err := checkPackageIdentifiers(f.PackageName, f.Imports); err != nil {
		return nil, err
	}
	data := fileData{
		PackageName:          f.PackageName,
		Imports:              f.Imports,
		TypeDefs:             wrapTypeDefs(f.TypeDefs),
		ValidationCapability: f.ValidationCapability,
		UnresolvedRefs:       f.UnresolvedRefs,
		UndeclaredRefTypes:   f.UndeclaredRefTypes,
		ElementNodes:         f.ElementNodes,
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
// Helper functions are package-level, so a package containing two schemas that
// both need one would declare it twice and fail to compile. They live in a
// single file per package instead. Returns ok=false when the set is empty and
// no file should be written.
func (e *Emitter) EmitHelpers(packageName string, helpers generator.HelperSet) ([]byte, bool, error) {
	if helpers.Empty() {
		return nil, false, nil
	}
	// A block one block calls is a block this file has to carry, and nothing
	// upstream can see that: the set is read from what the *generated types*
	// call, and a call from one helper to another appears in neither.
	helpers.CloseOverCalls()

	// Imports are fixed by which helpers are included, not by the schemas.
	var imports []generator.Import
	// Each path is added at most once: the list goes straight into the file's
	// import block, and naming the same package twice does not compile.
	//
	// The spec -- and so the name the file spells the package under -- is the
	// generator's (generator.GeneratedImport), which is the table its name
	// registry reserves those names from. A path that table does not list is a
	// panic there rather than an import here, so the helper file cannot gain a
	// package whose name a cross-package alias or a schema-derived identifier
	// could still take.
	add := func(cond bool, path string) {
		if !cond {
			return
		}
		for _, existing := range imports {
			if existing.Path == path {
				return
			}
		}
		imports = append(imports, generator.GeneratedImport(path))
	}
	addAliased := func(cond bool, path, alias string) {
		if spec := generator.GeneratedImport(path); spec.Alias != alias {
			panic(fmt.Sprintf("emitter: %q is imported as %q, and the generator reserves it as %q", path, alias, spec.Alias))
		}
		add(cond, path)
	}
	add(helpers.Dynamic || helpers.DynamicConst || helpers.OneOf || helpers.OneOfDiscriminator || helpers.Integer || helpers.Number || helpers.NumberCompare || helpers.DateTime || helpers.Canonical || helpers.NullCheck || helpers.Decode || helpers.DecodePath, "encoding/json")
	add(helpers.OneOfDiscriminator || helpers.Integer || helpers.Number || helpers.Canonical || helpers.NullCheck || helpers.Format || helpers.PathJoin || helpers.DecodePath, "fmt")
	// The JSON-equality reduction: a decoder over the document's own bytes, a
	// builder for the text it reduces to, sorted member names, and strconv for
	// the exponent it writes a number's scale as.
	add(helpers.Canonical, "bytes")
	add(helpers.Canonical, "strconv")
	add(helpers.Canonical, "strings")
	// The identity of a value, block by block. The core: two seeded hashes, the
	// spelling of numbers, strings read as UTF-8, and a raw JSON reader that
	// sorts an object's members to find a key written twice.
	add(helpers.IdentityCore, "encoding/json")
	add(helpers.IdentityCore, "errors")
	add(helpers.IdentityCore, "hash/maphash")
	add(helpers.IdentityCore, "math")
	add(helpers.IdentityCore, "sort")
	add(helpers.IdentityCore, "strconv")
	add(helpers.IdentityCore, "unicode/utf8")
	// A const read once per process, and decoded to confirm a match.
	add(helpers.IdentityConst, "bytes")
	add(helpers.IdentityConst, "encoding/json")
	add(helpers.IdentityConst, "strconv")
	add(helpers.IdentityConst, "sync")
	// A decoded value, and the refusal of one that is not.
	add(helpers.IdentityAny, "encoding/json")
	add(helpers.IdentityAny, "fmt")
	// A document keeps the identities of the values read lazily from it, which
	// a value judged from several goroutines at once shares.
	add(helpers.IdentityLazy, "errors")
	add(helpers.IdentityLazy, "sync")
	add(helpers.IdentityLazy, "sync/atomic")
	// Any Go value: the Go kind a value this package does not write itself is
	// read by, the base64 encoding/json writes a []byte as, and the time.Time
	// whose MarshalJSON it reads as a string.
	add(helpers.IdentityValue, "encoding/base64")
	add(helpers.IdentityValue, "encoding/json")
	add(helpers.IdentityValue, "errors")
	add(helpers.IdentityValue, "math")
	add(helpers.IdentityValue, "reflect")
	add(helpers.IdentityValue, "strconv")
	add(helpers.IdentityValue, "time")
	add(helpers.IdentityKind, "encoding/json")
	add(helpers.IdentityKind, "math")
	add(helpers.IdentityKind, "strconv")
	// The exact-number comparisons read the literal as decimal digits: strconv
	// for the exponent, math/big for the one question -- does this divide that
	// -- that digit arithmetic alone does not answer. Neither is needed by the
	// shadow type, which only decides whether a token is a number at all.
	add(helpers.NumberCompare, "strconv")
	add(helpers.NumberCompare, "math/big")
	// The in-place decode: the document's index is searched by offset, an
	// object key that is not plain ASCII is handed to encoding/json after a
	// UTF-8 scan, the commonest scalars are read with strconv, and a value of
	// the wrong kind is refused with the reflect.Type encoding/json would have
	// named.
	add(helpers.Decode, "reflect")
	add(helpers.Decode, "sort")
	add(helpers.Decode, "strconv")
	add(helpers.Decode, "unicode/utf8")
	// A path error writes out the chain of steps it holds with a builder, once.
	add(helpers.PathJoin, "strings")
	add(helpers.Dynamic, "math")
	// jsonIntegerFromLiteral reads the number as decimal digits, which is what
	// makes it exact where a parse into float64 could not be.
	add(helpers.Integer, "strconv")
	add(helpers.Integer, "strings")
	// jsonDateTime is a defined type over time.Time and hands every value it is
	// given to that type's own decoder; the respelling it retries through needs
	// nothing else.
	add(helpers.DateTime, "time")
	// The two ip shadows are defined types over netip.Addr and hand what they
	// are given to that package's own parser.
	add(helpers.IPAddr, "net/netip")
	add(helpers.IPAddr, "encoding/json")
	add(helpers.Annotations, "reflect")
	add(helpers.Annotations, "strconv")
	// The regexp engine only comes in when a compiled schema actually names a
	// pattern: it is a third-party dependency, and a package that never asks for
	// one should not acquire it. Every pattern a package matches with is
	// compiled in the pattern block, once, so that block is the one importer
	// for patterns -- the evaluator's arms, the --strict-read-write walker and
	// every generated check match through the variables it declares. The
	// format block needs the same engine for `format: regex`, whose argument is
	// the document's own text and so cannot be compiled ahead. Both routes go
	// through addAliased rather than appending, because the list goes straight
	// into the import block and must name each package once.
	addAliased(len(helpers.Patterns) > 0, "github.com/mgilbir/goecma262", "ecma262")
	addAliased(len(helpers.Patterns) > 0, "github.com/mgilbir/goecma262/flags", "ecmaflags")
	// The quoting rule counts and cuts bytes at a character boundary.
	add(helpers.Quote, "strconv")
	add(helpers.Quote, "unicode/utf8")
	add(helpers.Undecided, "errors")
	// The walker reports the first offending key in name order, so that a
	// document with several of them fails the same way every time. The runtime
	// evaluator visits an object's properties in the same fixed order, for the
	// same reason.
	add(helpers.NullCheck || helpers.Annotations || helpers.Canonical || helpers.DecodePath, "sort")
	// The decode-path block reads what a refusal was about: errors.As for the
	// one encoding/json raises from the Go type it was filling, and strings to
	// take the package qualifier off a shadow's name before reading it.
	add(helpers.DecodePath, "errors")
	add(helpers.DecodePath, "strings")
	// The format helpers are emitted as one block, so they name every package
	// any of them needs whether or not the schema uses that particular format.
	// Splitting the block per format is what would let a package end up with a
	// helper it cannot compile; see HelperSet.Format.
	add(helpers.Format, "net/netip")
	add(helpers.Format, "net/url")
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
	// The content check is its own block for the same kind of reason, one
	// import smaller: encoding/base64 is standard library, but nothing else
	// here needs it and a package whose schemas name no contentEncoding should
	// not carry the function that uses it. See HelperSet.Content.
	add(helpers.Content, "encoding/base64")
	add(helpers.Content, "encoding/json")
	add(helpers.Content, "fmt")
	// --strict-read-write's walker. `errors` is the one import here nothing else
	// in a helper file needs, and it is what lets a Validate check tell the
	// flag's refusal from a real decode failure by type rather than by message.
	add(helpers.Access, "encoding/json")
	add(helpers.Access, "errors")
	add(helpers.Access, "fmt")
	add(helpers.Access, "sort")

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
	// A set read from source is pruned to what its roots reach, which takes
	// the imports nothing kept names with it; see pruneHelpers.
	if helpers.Roots != nil {
		pruned, err := pruneHelpers(buf.Bytes(), helpers.Roots)
		if err != nil {
			return nil, false, err
		}
		return formatHelpers(pruned)
	}
	if kept, dropped := keepReferencedImports(buf.Bytes(), data.Imports); dropped {
		data.Imports = kept
		buf.Reset()
		if err := e.tmpl.ExecuteTemplate(&buf, "helpers_file.go.tmpl", data); err != nil {
			return nil, false, fmt.Errorf("emitter: executing helper template: %w", err)
		}
	}
	return formatHelpers(buf.Bytes())
}

// formattedHelpers keeps the formatted text of the last helper files: the same
// file is written for package after package. Bounded as preparedCache is.
var formattedHelpers struct {
	sync.Mutex
	m map[string][]byte
}

const formattedHelpersSize = 256

// formatHelpers is the rendered helper file, gofmt'ed.
func formatHelpers(rendered []byte) ([]byte, bool, error) {
	key := string(rendered)
	formattedHelpers.Lock()
	src, ok := formattedHelpers.m[key]
	formattedHelpers.Unlock()
	if ok {
		return append([]byte(nil), src...), true, nil
	}
	src, err := format.Source(rendered)
	if err != nil {
		return nil, false, fmt.Errorf("emitter: formatting helper output: %w\nraw output:\n%s", err, rendered)
	}
	formattedHelpers.Lock()
	if formattedHelpers.m == nil || len(formattedHelpers.m) >= formattedHelpersSize {
		formattedHelpers.m = make(map[string][]byte)
	}
	formattedHelpers.m[key] = src
	formattedHelpers.Unlock()
	return append([]byte(nil), src...), true, nil
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
