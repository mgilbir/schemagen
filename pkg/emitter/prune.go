package emitter

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// pruneHelpers keeps, of a rendered helper file, the declarations the package
// reaches: those named by roots -- every identifier the package's generated
// files use -- and, transitively, those named by a declaration kept. Everything
// else is removed, with the comments and blank lines in front of it, and so is
// every import the declarations left no longer qualify.
//
// The helper templates are written as blocks a package takes whole (see
// generator.HelperSet), and a block is far larger than what one package calls
// of it: a struct with one string property called four functions of the
// in-place decoder and carried all sixty. Every helper file is compiled into
// the user's package, so what it carries is compile time and binary size paid
// by every build. The blocks still decide what is rendered -- a field whose
// type lives in a block, an arm compiled in for one keyword -- and this pass
// decides what of it is kept, from the source itself, so there is no list of
// names to keep in step with the templates.
//
// What is kept:
//
//   - a function, type, variable or constant whose name a root or a kept
//     declaration mentions. A declaration group (a parenthesised const or var
//     block) is one unit, kept whole when any name in it is mentioned;
//   - a method of a kept type whose name a root or a kept declaration mentions
//     -- a call, or an interface listing it -- or whose name is exported:
//     encoding/json, fmt and errors call MarshalJSON, Error, Unwrap and As on a
//     value without naming them, so an exported method is kept for its type;
//   - an init function, and a declaration of the blank identifier, which run
//     or check something whether or not anything names them.
//
// Mentions are read as identifiers, wherever they appear: a local variable or a
// field sharing a helper's name keeps that helper. That errs towards keeping,
// which costs lines; erring the other way would be a package that does not
// compile, and TestHelperDeclarationsAreAllNeeded holds the result to the
// compiler in both directions.
//
// A rendered file is parsed once and the parse kept (see preparedHelpers): the
// same blocks are rendered for package after package, and only the roots
// differ.
func pruneHelpers(src []byte, roots []string) ([]byte, error) {
	p, err := prepareHelpers(src)
	if err != nil {
		return nil, err
	}
	return p.prune(roots), nil
}

// helperUnit is one top-level declaration of a rendered helper file.
type helperUnit struct {
	names    []string // top-level names it declares; a method declares none
	recv     string   // for a method, its receiver's type name
	method   string   // for a method, its name
	refs     []string // every identifier it mentions
	quals    []string // every identifier it uses on the left of a selector
	from, to int      // its span: from the end of the declaration before it
	always   bool
}

// helperImport is one import spec of a rendered helper file.
type helperImport struct {
	name     string
	from, to int
}

// preparedHelpers is a rendered helper file, parsed.
type preparedHelpers struct {
	src     []byte
	units   []helperUnit
	imports []helperImport
}

// preparedCache keeps the parse of the last rendered files. Bounded: it is
// emptied when it fills, so a process generating for many sets of blocks holds
// at most this many parses.
var preparedCache struct {
	sync.Mutex
	m map[string]*preparedHelpers
}

const preparedCacheSize = 64

func prepareHelpers(src []byte) (*preparedHelpers, error) {
	key := string(src)
	preparedCache.Lock()
	p, ok := preparedCache.m[key]
	preparedCache.Unlock()
	if ok {
		return p, nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("emitter: parsing the helper file to prune it: %w", err)
	}
	// Its own copy: the key is a string, and the caller's buffer is theirs.
	p = &preparedHelpers{src: []byte(key)}
	off := func(pos token.Pos) int { return fset.Position(pos).Offset }
	prevEnd := off(file.Name.End())
	for _, d := range file.Decls {
		u := helperUnit{from: prevEnd, to: off(d.End())}
		prevEnd = u.to
		switch d := d.(type) {
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				for _, s := range d.Specs {
					imp := s.(*ast.ImportSpec)
					from := off(imp.Pos())
					if imp.Doc != nil {
						from = off(imp.Doc.Pos())
					}
					p.imports = append(p.imports, helperImport{name: importName(imp), from: from, to: off(imp.End())})
				}
				continue
			}
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					u.names = append(u.names, s.Name.Name)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.Name == "_" {
							u.always = true
						}
						u.names = append(u.names, n.Name)
					}
				}
			}
		case *ast.FuncDecl:
			switch {
			case d.Recv != nil && len(d.Recv.List) > 0:
				u.recv, u.method = receiverTypeName(d.Recv.List[0].Type), d.Name.Name
			case d.Name.Name == "init":
				u.always = true
			default:
				u.names = append(u.names, d.Name.Name)
			}
		}
		ast.Inspect(d, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.Ident:
				u.refs = append(u.refs, n.Name)
			case *ast.SelectorExpr:
				if id, ok := n.X.(*ast.Ident); ok {
					u.quals = append(u.quals, id.Name)
				}
			}
			return true
		})
		p.units = append(p.units, u)
	}
	preparedCache.Lock()
	if preparedCache.m == nil || len(preparedCache.m) >= preparedCacheSize {
		preparedCache.m = make(map[string]*preparedHelpers)
	}
	preparedCache.m[key] = p
	preparedCache.Unlock()
	return p, nil
}

// prune is the file with only what roots reach; see pruneHelpers.
func (p *preparedHelpers) prune(roots []string) []byte {
	mentioned := make(map[string]bool, len(roots))
	for _, r := range roots {
		mentioned[r] = true
	}
	kept := make([]bool, len(p.units))
	keptType := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for i := range p.units {
			u := &p.units[i]
			if kept[i] {
				continue
			}
			need := u.always
			if u.method != "" {
				need = keptType[u.recv] && (mentioned[u.method] || isExported(u.method))
			}
			for _, n := range u.names {
				need = need || mentioned[n]
			}
			if !need {
				continue
			}
			kept[i], changed = true, true
			for _, r := range u.refs {
				mentioned[r] = true
			}
			for _, n := range u.names {
				keptType[n] = true
			}
		}
	}

	// Each removed declaration takes with it everything between the end of the
	// declaration before it and its own end: its doc comment, and the blank
	// lines and prose that introduce it. An import goes where no kept
	// declaration qualifies its name.
	used := map[string]bool{}
	type cut struct{ from, to int }
	var cuts []cut
	for i := range p.units {
		if !kept[i] {
			cuts = append(cuts, cut{p.units[i].from, p.units[i].to})
			continue
		}
		for _, q := range p.units[i].quals {
			used[q] = true
		}
	}
	for _, imp := range p.imports {
		if imp.name != "_" && imp.name != "." && !used[imp.name] {
			cuts = append(cuts, cut{imp.from, imp.to})
		}
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].from < cuts[j].from })
	out := make([]byte, 0, len(p.src))
	at := 0
	for _, c := range cuts {
		out = append(out, p.src[at:c.from]...)
		at = c.to
	}
	return append(out, p.src[at:]...)
}

// receiverTypeName is the name of a method receiver's type: T in T, *T, T[K]
// and *T[K, V].
func receiverTypeName(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.StarExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		case *ast.Ident:
			return x.Name
		default:
			return ""
		}
	}
}

// importName is the name an import is referred to by in the file: its alias,
// or its package name.
func importName(imp *ast.ImportSpec) string {
	if imp.Name != nil {
		return imp.Name.Name
	}
	path, err := strconv.Unquote(imp.Path.Value)
	if err != nil {
		return ""
	}
	return generator.PackageNameForImportPath(path)
}

func isExported(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}
