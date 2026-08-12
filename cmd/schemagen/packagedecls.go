package schemagen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
)

// packageDecls records the package-level declarations of the files a run writes
// into one Go package, and refuses a second file that declares a name the
// package already has.
//
// Every input is generated on its own, and a document a $ref reaches into is
// materialized by whichever file references it. Two inputs of one package that
// share such a document therefore each carry a copy of its types, and the
// package does not compile -- while generation exits 0 and reports nothing,
// because no single file is wrong. That is issue #217, and the only place the
// collision is visible is here, where the files of a package meet.
//
// The check reads the emitted source rather than the IR's type definitions
// because it has to see everything a Go compiler would: an enum contributes
// constants, a oneOf group contributes wrapper types and methods, and a type
// definition list names none of those.
//
// Reporting rather than repairing is deliberate. Two copies of a type are only
// interchangeable when the schemas behind them agree, which nothing here can
// establish -- two unrelated documents may each define an "Address" that means
// something different -- so dropping one would silently generate a package whose
// types are not the types its schemas describe. The modes that share a type
// properly (--shared-types, --schema-package) exist, and the message names them.
type packageDecls struct {
	pkgName string
	byName  map[string]string // declaration → the schema path that produced it
	seen    map[string]bool   // schema paths already recorded
}

func newPackageDecls(pkgName string) *packageDecls {
	return &packageDecls{
		pkgName: pkgName,
		byName:  make(map[string]string),
		seen:    make(map[string]bool),
	}
}

// add records the declarations of one generated file, or reports the names it
// declares that the package already has.
func (p *packageDecls) add(schemaPath string, src []byte) error {
	// One input named twice on the command line is generated twice into the
	// same output file, which checkOutputCollisions deliberately permits. It is
	// one file of the package, not two, so it cannot collide with itself.
	if p.seen[schemaPath] {
		return nil
	}
	p.seen[schemaPath] = true
	names, err := topLevelDecls(src)
	if err != nil {
		return fmt.Errorf("reading generated code for %s: %w", schemaPath, err)
	}
	var clashes []string
	var owner string
	for _, name := range names {
		if prev, ok := p.byName[name]; ok {
			clashes = append(clashes, name)
			owner = prev
			continue
		}
		p.byName[name] = schemaPath
	}
	if len(clashes) == 0 {
		return nil
	}
	sort.Strings(clashes)
	const limit = 5
	shown := clashes
	suffix := ""
	if len(shown) > limit {
		shown, suffix = shown[:limit], fmt.Sprintf(" (and %d more)", len(clashes)-limit)
	}
	// The name was already held by the file this one is, which is to say one
	// generated file declares it twice. That is a different report: it is not two
	// documents meeting in a package, and neither mode below would change it,
	// because there is no second document to move anywhere. "a.json and a.json
	// both declare [Thing]" named the same document twice and sent the reader
	// after inputs they do not have (issue #259, where {"$defs":{"Thing":
	// {"$ref":"#"}}} referenced from a property emitted Thing twice).
	//
	// Nothing a schema can say asks for this: within one file the generator
	// declares each name once, and the guard reads the emitted source precisely
	// so that a route which slips past that becomes visible instead of reaching
	// the caller as a compile error in generated code. So the message says the
	// defect is schemagen's.
	if owner == schemaPath {
		return fmt.Errorf("the file generated for %s declares %v%s twice in package %q, so it would not compile; "+
			"one file declares each type once, whatever the document says, so this is a defect in schemagen rather than in the schema "+
			"-- --shared-types and --schema-package will not change it. Please report it, with the schema that produced it",
			schemaPath, shown, suffix, p.pkgName)
	}
	return fmt.Errorf("%s and %s both declare %v%s in package %q; "+
		"a document reached by $ref is materialized into every file that references it, "+
		"so the package would not compile. Generate the set with --shared-types "+
		"(one package, each type emitted once) or with --schema-package "+
		"(one package per document, cross-document $refs become imports)",
		owner, schemaPath, shown, suffix, p.pkgName)
}

// topLevelDecls lists the package-level names a generated file declares: types,
// constants, variables, functions, and methods (qualified by receiver type, the
// only name space in which a method can clash).
func topLevelDecls(src []byte) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "generated.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					names = append(names, s.Name.Name)
				case *ast.ValueSpec:
					for _, id := range s.Names {
						if id.Name != "_" {
							names = append(names, id.Name)
						}
					}
				}
			}
		case *ast.FuncDecl:
			if d.Recv == nil || len(d.Recv.List) == 0 {
				// "init" is the one function name Go lets a package declare
				// more than once, and the generator emits one per file that
				// compiles a pattern. It is not a collision.
				if d.Name.Name != "_" && d.Name.Name != "init" {
					names = append(names, d.Name.Name)
				}
				continue
			}
			if recv := receiverTypeName(d.Recv.List[0].Type); recv != "" {
				names = append(names, recv+"."+d.Name.Name)
			}
		}
	}
	return names, nil
}

// receiverTypeName is the name of the type a method hangs off, through the
// pointer and the type-parameter list a receiver may carry.
func receiverTypeName(t ast.Expr) string {
	switch e := t.(type) {
	case *ast.StarExpr:
		return receiverTypeName(e.X)
	case *ast.IndexExpr:
		return receiverTypeName(e.X)
	case *ast.IndexListExpr:
		return receiverTypeName(e.X)
	case *ast.Ident:
		return e.Name
	}
	return ""
}
