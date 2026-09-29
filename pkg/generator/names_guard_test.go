package generator

import (
	"go/ast"
	"sort"
	"testing"
)

// The static half of the name registry's promise (names.go). The runtime half
// is generateTypeDef, declare and appendDef refusing a name the registry holds
// for another node; these gates are what stop the next arm from minting a name
// the runtime check never sees, because it references a type without declaring
// one -- which is exactly how a titled oneOf variant and a nullable oneOf's
// reference came to bind another node's type.

// registryNameFuncs answer with a name the registry settled: claimed, or free
// for the node and claimed where it is declared.
var registryNameFuncs = map[string]string{
	"unclaimedTypeName":       "a position's name, checked against every holder",
	"claimTypeName":           "claims and holds",
	"claimVariantTypeName":    "an inline oneOf variant's type name",
	"goNameForResolvedRef":    "a $ref target's name, checked against every holder",
	"definitionGoName":        "the name claimDefinitionNames claimed",
	"unresolvedRefTypeName":   "the placeholder an unresolved $ref holds",
	"cyclicNodeName":          "the name a node in flight is being declared under",
	"materializeNamed":        "materializes under unclaimedTypeName, or answers a canonical name",
	"materializeAtPosition":   "materializes under unclaimedTypeName, or answers a canonical name",
	"delegatedBranchType":     "a name resolveType or materializeNamed answered",
	"resolveRefTypeName":      "goNameForResolvedRef, or the unresolved placeholder",
	"rawValueTypeName":        "materializeAtPosition or goNameForResolvedRef",
	"foreignDelegateTypeName": "another package's type, qualified with a claimed import alias",
	"inferredItemTypeName":    "one of the above, for an inferred array's element",
}

// rawNameFuncs derive a name with no registry in sight. Their result may feed a
// registry function, and nothing else.
var rawNameFuncs = map[string]bool{
	"refToGoName":           true,
	"SchemaNameToGoName":    true,
	"JSONPropertyToGoName":  true,
	"TypeNameForRef":        true,
	"TypeNameForDocumentID": true,
	"Sprintf":               true,
	"ToOneOfWrapperName":    true,
	"ToOneOfInterfaceName":  true,
}

// nameSource is where the name expression at a declaration or reference site
// comes from.
type nameSource int

const (
	sourceRegistry  nameSource = iota // a registry answer
	sourceParameter                   // the enclosing function's parameter: its callers answer for it
	sourceRaw                         // a derivation with no registry in it
	sourceOther                       // something this gate cannot classify
)

// nameSourceOf classifies expr within fn, following local assignments. A name
// with several assignments is as bad as the worst of them.
func nameSourceOf(fn *ast.FuncDecl, expr ast.Expr, seen map[string]bool) (nameSource, string) {
	switch v := expr.(type) {
	case *ast.CallExpr:
		name := calleeName(v)
		if _, ok := registryNameFuncs[name]; ok {
			return sourceRegistry, ""
		}
		if rawNameFuncs[name] {
			return sourceRaw, name + "(...)"
		}
		return sourceOther, name + "(...)"
	case *ast.SelectorExpr:
		if v.Sel.Name == "rootTypeName" {
			return sourceRegistry, ""
		}
		return sourceOther, "." + v.Sel.Name
	case *ast.IndexExpr:
		if sel, ok := v.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "nodeTypeNames" {
			return sourceRegistry, ""
		}
		return sourceOther, "an index expression"
	case *ast.BasicLit:
		if v.Value == `""` {
			// No name at all: the zero a variable starts at before a
			// registry answer is assigned, which every site tests for.
			return sourceRegistry, ""
		}
		return sourceRaw, v.Value
	case *ast.BinaryExpr:
		return sourceRaw, "a concatenation"
	case *ast.Ident:
		if isParameter(fn, v.Name) {
			return sourceParameter, ""
		}
		if seen[v.Name] {
			return sourceRegistry, ""
		}
		seen[v.Name] = true
		worst, why := sourceRegistry, ""
		rhs := assignmentsTo(fn, v.Name)
		rhs = append(rhs, multiAssignmentsFrom(fn, v.Name)...)
		if len(rhs) == 0 {
			return sourceOther, v.Name + " (assigned nowhere this gate can see)"
		}
		for _, r := range rhs {
			if src, w := nameSourceOf(fn, r, seen); src > worst {
				worst, why = src, w
			}
		}
		return worst, why
	}
	return sourceOther, "an expression of another shape"
}

// multiAssignmentsFrom returns the calls a name is one of several results of:
// `name, cyclic := g.materializeNamed(...)`.
func multiAssignmentsFrom(fn *ast.FuncDecl, name string) []ast.Expr {
	var out []ast.Expr
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch as := n.(type) {
		case *ast.AssignStmt:
			if len(as.Lhs) > 1 && len(as.Rhs) == 1 {
				for _, lhs := range as.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
						out = append(out, as.Rhs[0])
					}
				}
			}
		case *ast.ValueSpec:
			for i, id := range as.Names {
				if id.Name == name && i < len(as.Values) {
					out = append(out, as.Values[i])
				}
			}
		}
		return true
	})
	return out
}

// setsField reports whether a composite literal sets the named field.
func setsField(lit *ast.CompositeLit, field string) bool {
	for _, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if k, ok := kv.Key.(*ast.Ident); ok && k.Name == field {
				return true
			}
		}
	}
	return false
}

func isParameter(fn *ast.FuncDecl, name string) bool {
	for _, lists := range [][]*ast.Field{fn.Type.Params.List, resultsOf(fn)} {
		for _, f := range lists {
			for _, n := range f.Names {
				if n.Name == name {
					return true
				}
			}
		}
	}
	return false
}

func resultsOf(fn *ast.FuncDecl) []*ast.Field {
	if fn.Type.Results == nil {
		return nil
	}
	return fn.Type.Results.List
}

// nameSites are the calls whose first argument is a type name being declared
// or claimed for a node, and the composite literal fields that reference a type
// of this package by name.
var nameSiteCalls = map[string]bool{
	"generateTypeDef":    true,
	"generateTypeDefFor": true,
	"declareFor":         true,
	"holdFor":            true,
	"emitDefAs":          true,
	"declare":            true,
	"generateEnumDef":    true,
	"generateStructDef":  true,
}

// nameSiteAllowances are the sites whose name the classification above cannot
// follow, each with the reason it comes out of the registry all the same.
var nameSiteAllowances = map[string]string{
	"emitDef: declare(def.TypeName(), ...)":                   "the name the def was built under; emitDefAs checks it is the name its caller holds, and the one direct caller (generateTypeDefFor's backstop) builds it from its own parameter",
	"Generate: NamedType{Name: name}":                         "the cross-package publishing loop, naming each def already in the file by the name it was declared under",
	"resolveTypeBranchesInPlace: NamedType{Name: b.TypeName}": "a type-schema branch's type, already named by the registry when the branch was built; looked up, never declared",
	"resolveEncodePlans: NamedType{Name: d.MarshalAs}":        "the type an alias's MarshalJSON already delegates to, named when the delegate was settled",
	"resolveEncodePlans: NamedType{Name: d.Name}":             "the encode plan of an alias already in the file, written over the alias itself: the name it was declared under",
	"aliasEncodes: NamedType{Name: ad.MarshalAs}":             "the type an alias's MarshalJSON already delegates to; looked up, never declared",
	"identityReach: NamedType{Name: d.MarshalAs}":             "the type an alias's MarshalJSON already delegates to; looked up, never declared",
	"identityReach: NamedType{Name: n.TypeName}":              "the type an element node was compiled from, named by the registry when its position was built; looked up, never declared",
	"resolveIdentityPlans: NamedType{Name: d.MarshalAs}":      "the type an alias's MarshalJSON already delegates to, named when the delegate was settled",
	"resolveIdentityPlans: NamedType{Name: d.Name}":           "the identity of an alias already in the file, read over the alias itself: the name it was declared under",
	"resolveDecodePlans: NamedType{Name: d.Name}":             "the decode plan of an alias already in the file, written over the alias itself: the name it was declared under",
}

// TestNoTypeNameIsBuiltOutsideTheRegistry reads every place pkg/generator names
// a type of this package -- the name a declaration is made under, and every
// NamedType it references -- and requires the name to come out of the name
// registry, or to be the enclosing function's parameter (whose callers this
// same gate reads).
//
// It is what a new arm has to get past. Every arm that typed a position with
// another node's schema built its name the way this refuses: refToGoName straight into a NamedType (the nullable-oneOf arm), a
// title through SchemaNameToGoName into a type that already existed (the titled
// variant), a count appended with Sprintf (the variant suffix).
func TestNoTypeNameIsBuiltOutsideTheRegistry(t *testing.T) {
	fset, files := parsePackageSources(t)
	var problems []string
	sites := 0
	allowanceUsed := map[string]bool{}
	for _, f := range files {
		if fset.Position(f.Pos()).Filename == "names.go" {
			continue
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			check := func(site string, expr ast.Expr) {
				sites++
				src, why := nameSourceOf(fn, expr, map[string]bool{})
				if src != sourceRaw && src != sourceOther {
					return
				}
				key := fn.Name.Name + ": " + site
				if _, ok := nameSiteAllowances[key]; ok {
					allowanceUsed[key] = true
					return
				}
				problems = append(problems, fset.Position(expr.Pos()).String()+": "+key+" takes its name from "+why)
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.CallExpr:
					if nameSiteCalls[calleeName(v)] && len(v.Args) > 0 {
						check(calleeName(v)+"("+exprText(fset, v.Args[0])+", ...)", v.Args[0])
					}
				case *ast.CompositeLit:
					if id, ok := v.Type.(*ast.Ident); ok && id.Name == "NamedType" && !setsField(v, "PkgAlias") {
						// A qualified NamedType is another package's type, named
						// by that package's registry and published through the
						// cross-package registry; its import alias is claimed
						// here (importAlias).
						for _, elt := range v.Elts {
							kv, ok := elt.(*ast.KeyValueExpr)
							if !ok {
								continue
							}
							if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Name" {
								check("NamedType{Name: "+exprText(fset, kv.Value)+"}", kv.Value)
							}
						}
					}
				}
				return true
			})
		}
	}
	if sites < 100 {
		t.Fatalf("only %d naming sites found; the scan has stopped seeing the package, so this gate would pass on nothing", sites)
	}
	for key := range nameSiteAllowances {
		if !allowanceUsed[key] {
			t.Errorf("nameSiteAllowances excuses %q, which no longer needs it; drop the stale entry", key)
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Errorf("%s.\nA type of this package is named only by the name registry (names.go): "+
			"route the name through unclaimedTypeName, goNameForResolvedRef or another registry function, "+
			"or -- if it does come from the registry by a route this gate cannot follow -- say so in nameSiteAllowances", p)
	}
}

// TestTypeDefsEnterTheFileOnlyThroughTheRegistry holds the declaration side:
// the file's type definitions are added and removed only by appendDef and
// withdrawDefs, which is where the registry records and releases what is
// declared. An arm that appended a def itself would declare a name the registry
// never saw.
func TestTypeDefsEnterTheFileOnlyThroughTheRegistry(t *testing.T) {
	allowed := map[string]bool{"appendDef": true, "withdrawDefs": true}
	fset, files := parsePackageSources(t)
	found := 0
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for _, lhs := range as.Lhs {
					if exprText(fset, lhs) != "g.output.TypeDefs" {
						continue
					}
					found++
					if !allowed[fn.Name.Name] {
						t.Errorf("%s: %s assigns g.output.TypeDefs; declarations go through emitDef/appendDef so the name registry sees them",
							fset.Position(as.Pos()), fn.Name.Name)
					}
				}
				return true
			})
		}
	}
	if found == 0 {
		t.Fatal("no assignment to g.output.TypeDefs found at all; the scan is broken, not the package")
	}
}

// registryState are the fields of nameRegistry that hold its answers. Outside
// names.go they are read only by the functions listed, each for the reason
// given; everything else asks the registry's methods.
var registryState = []string{"held", "declared", "pinned", "members", "unresolved", "pending", "byNode", "moves"}

var registryStateReaders = map[string]string{
	"Generate":              "the shared-types root check asks whether the root name is declared already",
	"isDeclared":            "the registry's own predicate, on the generator",
	"declare":               "commits a declaration",
	"appendDef":             "checks the name was declared",
	"unresolvedRefTypeName": "records the placeholder it claimed",
	"undeclaredRefTypes":    "reads the placeholders back for the file's banner",
}

func TestRegistryStateIsReadThroughItsMethods(t *testing.T) {
	fset, files := parsePackageSources(t)
	fields := map[string]bool{}
	for _, f := range registryState {
		fields[f] = true
	}
	for _, f := range files {
		if fset.Position(f.Pos()).Filename == "names.go" {
			continue
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || !fields[sel.Sel.Name] {
					return true
				}
				inner, ok := sel.X.(*ast.SelectorExpr)
				if !ok || inner.Sel.Name != "names" {
					return true
				}
				if _, ok := registryStateReaders[fn.Name.Name]; !ok {
					t.Errorf("%s: %s reads the registry's %s directly; ask its methods, or record why not in registryStateReaders",
						fset.Position(sel.Pos()), fn.Name.Name, sel.Sel.Name)
				}
				return true
			})
		}
	}
}
