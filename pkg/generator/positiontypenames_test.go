package generator

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The enumeration this file pins: for every call that asks generateTypeDef to
// declare a type, where does the *name* come from -- and therefore which of them
// need the node-keyed guard.
//
// generateTypeDef's own re-entrancy guard is g.generated[name]. That is enough
// for a name drawn from a set the document fixes: a $defs key, the root type
// name, a name derived from a reference string. Those repeat, so the second
// arrival at a cycle finds the name already generated and stops. It is enough
// for nothing else. A name minted from the *position* a sub-schema was reached
// through is parentName+segment, so a self-referential document arrives under
// RootA, RootAA, RootAAA ... -- a fresh name at every level, and a guard keyed
// on the name that can never fire. The run then ends in "fatal error: out of
// memory", which no recover intercepts and which no error path reports.
//
// That has now been three separate defects in three separate arms, each found by
// its own fuzz run: the property positions (fixed when cyclicNodeName was
// written), the array element and tuple slot (#348), and the patternProperties
// and per-branch overflow buckets (#349). Adding a fourth call and waiting for
// the fourth finding is what this file exists to stop. The question is made
// total the way #313 made the type-name *emission* question total and #337 the
// reference-*reading* one: every site is enumerated, its kind is derived from
// the source rather than asserted, and the table below fails in both directions
// -- a new site is unclassified, a removed one is stale, and a site whose name
// stops being reference-derived changes kind.
//
// The enumeration is not the only thing standing between a new arm and a dead
// process. generateTypeDef carries a backstop for a node already in flight under
// another name (see its comment, and RemintedInFlight), so an arm written
// without the guard degrades to an alias that is recorded rather than to an OOM.
// This gate is what makes the arm visible at review time instead.

// nameKind is where the name a generateTypeDef call declares under comes from,
// derived from the source by nameKindOf.
type nameKind string

const (
	// kindDocument: a name the document fixes -- a $defs or definitions key
	// through definitionGoName, or the root type name. One per definition, so
	// the name space is finite and g.generated terminates any cycle through it.
	kindDocument nameKind = "document"
	// kindReference: a name derived from the reference string, through
	// refToGoName or goNameForResolvedRef. A cycle revisits the same reference,
	// so the name repeats and g.generated terminates it.
	kindReference nameKind = "reference"
	// kindPosition: a name minted for the position, through unclaimedTypeName.
	// It grows a segment per level; the node-keyed guard is what terminates it.
	kindPosition nameKind = "position"
	// kindParameter: a name this function received and cannot see the
	// derivation of. Treated exactly as kindPosition -- assuming it repeats is
	// the assumption that cost three defects -- so it needs a guard too.
	kindParameter nameKind = "parameter"
)

// refNameFuncs derive a name from the reference string.
var refNameFuncs = map[string]bool{
	"refToGoName":          true,
	"goNameForResolvedRef": true,
	"resolveRefTypeName":   true,
}

// documentNameFuncs derive a name from a key the document states.
var documentNameFuncs = map[string]bool{"definitionGoName": true}

// positionNameFuncs mint a name for a position inside the document.
var positionNameFuncs = map[string]bool{
	"unclaimedTypeName": true,
	"numberedTypeName":  true,
}

// passThroughNameFuncs answer with the name they were given, disambiguated;
// the kind is their first argument's.
var passThroughNameFuncs = map[string]bool{"uniqueTypeName": true}

// typeDefSite is one generateTypeDef call site, keyed by the enclosing function
// and the argument expressions as written.
type typeDefSite struct {
	Func   string
	Name   string
	Schema string
}

func (s typeDefSite) String() string {
	return fmt.Sprintf("%s: generateTypeDef(%s, %s)", s.Func, s.Name, s.Schema)
}

// expectation is what the table records about a site.
type expectation struct {
	// Count is how many times this exact call appears in that function. It is
	// part of the pin: a second identical call added to a guarded arm is a new
	// site even though the key does not change.
	Count int
	Kind  nameKind
	// Guard is the node-keyed guard the enclosing function consults, named by
	// the function it calls or the map it indexes. Required for kindPosition and
	// kindParameter, and it must mention the same schema expression this call
	// passes -- guarding one node says nothing about another.
	Guard string
	// Why states what makes the classification true, for the reader who has to
	// judge whether a new entry belongs.
	Why string
}

// typeDefSites is the enumeration. Adding a generateTypeDef call anywhere in
// this package fails the gate below until it appears here, and removing one
// fails it until the entry goes.
var typeDefSites = map[typeDefSite]expectation{
	{"Generate", "goName", "def"}: {
		Count: 2, Kind: kindDocument,
		Why: "the $defs and definitions entries, named by their own key through definitionGoName; one per key, so the name space is the document's",
	},
	{"Generate", "g.rootTypeName", "s"}: {
		Count: 1, Kind: kindDocument,
		Why: "the root type, named once per document",
	},
	{"generateTypeDefBody", "refName", "resolved"}: {
		Count: 2, Kind: kindReference,
		Why: "the $ref and $dynamicRef arms generating their target under the name the reference string derives",
	},
	{"generateTypeDefBody", "name", "resolved"}: {
		Count: 2, Kind: kindParameter, Guard: "refCycleAliasDef",
		Why: "re-generating under the name this frame already holds, for a target whose type would not carry the target's methods. refCycleAliasDef above reads nodesInProgress[resolved] and returns an alias before this runs",
	},
	{"resolveOneOfVariant", "goName", "refSchema"}: {
		Count: 1, Kind: kindReference,
		Why: "a $ref variant, named from the reference through refToGoName and goNameForResolvedRef",
	},
	{"resolveOneOfVariant", "variantName", "variant"}: {
		Count: 3, Kind: kindParameter, Guard: "cyclicNodeName",
		Why: "the inline object, allOf and format variants, named parentName+fieldName+Option+index -- which grows with parentName. The guard above them answers a variant that is the node in flight with the name it already holds",
	},
	{"resolvePropertyType", "posName", "s"}: {
		Count: 9, Kind: kindPosition, Guard: "cyclicNodeName",
		Why: "every arm that names a property position; the guard is the first statement of the function",
	},
	{"resolvePropertyType", "goName", "refSchema"}: {
		Count: 2, Kind: kindReference,
		Why: "the nullable-oneOf variant and the $ref arm, both named from the reference string",
	},
	{"resolveType", "goName", "refSchema"}: {
		Count: 2, Kind: kindReference,
		Why: "the $ref and $dynamicRef arms of the element, map-value and branch positions",
	},
	{"resolveType", "contextName", "s"}: {
		Count: 1, Kind: kindParameter, Guard: "nodeTypeNames",
		Why: "the composition arm; the statement above it answers a node already named with nodeTypeNames[s], and g.generating guards the name",
	},
	{"materializeNamed", "contextName", "s"}: {
		Count: 1, Kind: kindParameter, Guard: "nodeTypeNames",
		Why: "the object path's funnel: it answers a node already materialized with its first name before generating anything",
	},
	{"materializeAtPosition", "posName", "s"}: {
		Count: 1, Kind: kindParameter, Guard: "cyclicNodeName",
		Why: "the funnel every position-derived mint goes through; the guard is the statement above",
	},
	{"firstAllOfArrayAliasName", "name", "resolved"}: {
		Count: 2, Kind: kindReference,
		Why: "the $ref and $dynamicRef branches of an allOf, named from the reference; the arm also declines a node in flight outright",
	},
	{"inferredTupleItemFromSchema", "goName", "resolved"}: {
		Count: 1, Kind: kindReference,
		Why: "an inferred tuple slot behind a $ref, named from the reference",
	},
	{"resolveRefTypeName", "goName", "resolved"}: {
		Count: 1, Kind: kindReference,
		Why: "the shared $ref-to-type-name resolver",
	},
	{"rawValueTypeName", "refName", "r"}: {
		Count: 1, Kind: kindReference,
		Why: "a patternProperties or per-branch overflow bucket whose sub-schema is a plain $ref: the target's own type is reused rather than a copy minted, so the name is the reference's",
	},
	{"tupleItemCheckFor", "refName", "resolved"}: {
		Count: 1, Kind: kindReference,
		Why: "a tuple slot behind a plain $ref, named from the reference",
	},
}

// TestEveryTypeNameMintingSiteIsAccountedFor reads the package and holds the
// table above to it in both directions.
func TestEveryTypeNameMintingSiteIsAccountedFor(t *testing.T) {
	fset, files := parsePackageSources(t)
	seen := map[typeDefSite]int{}
	guards := map[string]map[string][]string{} // func -> guard name -> schema expressions it names

	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			recordGuards(fset, fn, guards)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || calleeName(call) != "generateTypeDef" || len(call.Args) != 2 {
					return true
				}
				seen[typeDefSite{
					Func:   fn.Name.Name,
					Name:   exprText(fset, call.Args[0]),
					Schema: exprText(fset, call.Args[1]),
				}]++
				return true
			})
		}
	}

	if len(seen) == 0 {
		// A gate measuring nothing passes for the wrong reason.
		t.Fatal("no generateTypeDef call sites found; the scan is broken, not the package")
	}

	for _, site := range sortedSites(seen) {
		want, ok := typeDefSites[site]
		if !ok {
			t.Errorf("unclassified type-name minting site %s.\n"+
				"Every call that declares a type has to say where its name comes from, because "+
				"generateTypeDef's own guard keys on the name: a name derived from a reference "+
				"string or a document key repeats and terminates a cycle, and a name minted from "+
				"the position does not -- it grows a segment per level and the run dies of memory. "+
				"Classify it in typeDefSites, and if the name is the position's, route the call "+
				"through materializeAtPosition (or guard it on the node) first", site)
			continue
		}
		if got := seen[site]; got != want.Count {
			t.Errorf("%s appears %d times, the table records %d. A call added beside an existing "+
				"one is a new site: re-derive its kind and update the count", site, got, want.Count)
		}
	}
	for site, want := range typeDefSites {
		if _, ok := seen[site]; !ok {
			t.Errorf("stale entry %s: the table records a site the package no longer has", site)
		}
		if want.Why == "" {
			// A classification with no reasoning behind it is the next reader's
			// problem and the next defect's cover.
			t.Errorf("%s is classified %s with no reason recorded", site, want.Kind)
		}
	}

	// The kind is derived from the source, not read from the table, so an entry
	// cannot claim a provenance the code does not have. This is what closes the
	// hole a classification-only gate leaves (see #339 against #313's).
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || calleeName(call) != "generateTypeDef" || len(call.Args) != 2 {
					return true
				}
				site := typeDefSite{fn.Name.Name, exprText(fset, call.Args[0]), exprText(fset, call.Args[1])}
				want, ok := typeDefSites[site]
				if !ok {
					return true
				}
				got := nameKindOf(fset, fn, call.Args[0])
				if got != want.Kind {
					t.Errorf("%s: the name is %s-derived in the source, the table records %s",
						site, got, want.Kind)
				}
				if want.Kind != kindPosition && want.Kind != kindParameter {
					if want.Guard != "" {
						t.Errorf("%s: the table names a guard for a %s-derived name, which does not need one",
							site, want.Kind)
					}
					return true
				}
				if want.Guard == "" {
					t.Errorf("%s: a %s-derived name needs a node-keyed guard and the table names none",
						site, want.Kind)
					return true
				}
				named := guards[fn.Name.Name][want.Guard]
				if named == nil {
					t.Errorf("%s: the table names the guard %q, and %s does not consult it. "+
						"A position-derived name with no node-keyed guard is the shape of #348 and #349",
						site, want.Guard, fn.Name.Name)
					return true
				}
				if !containsString(named, site.Schema) {
					t.Errorf("%s: %s consults %s about %v, not about %s. Guarding one node says "+
						"nothing about another", site, fn.Name.Name, want.Guard, named, site.Schema)
				}
				return true
			})
		}
	}
}

// TestTheGuardedSitesAreTheOnesTheFunnelsCover states the other half of the
// enumeration in a form a reader can check at a glance: every position- or
// parameter-derived site sits in one of the four functions that hold a
// node-keyed guard, and nowhere else.
//
// It is deliberately redundant with the table above. The table can be edited to
// match a change; this fails on the change itself.
func TestTheGuardedSitesAreTheOnesTheFunnelsCover(t *testing.T) {
	guarded := map[string]bool{
		"generateTypeDefBody":   true, // refCycleAliasDef, on the ref target
		"resolveOneOfVariant":   true, // cyclicNodeName, at the top of the naming arms
		"resolvePropertyType":   true, // cyclicNodeName, first statement
		"resolveType":           true, // nodeTypeNames, at the composition arm
		"materializeNamed":      true, // nodeTypeNames, first statement
		"materializeAtPosition": true, // cyclicNodeName, first statement
	}
	for site, want := range typeDefSites {
		if want.Kind != kindPosition && want.Kind != kindParameter {
			continue
		}
		if !guarded[site.Func] {
			t.Errorf("%s mints a %s-derived name in %s, which is not one of the functions that "+
				"holds a node-keyed guard. Route it through materializeAtPosition, or add %s to "+
				"this list with the guard it consults", site, want.Kind, site.Func, site.Func)
		}
	}
}

// recordGuards collects, per function, which node-keyed guards it consults and
// about which schema expressions.
func recordGuards(fset *token.FileSet, fn *ast.FuncDecl, out map[string]map[string][]string) {
	add := func(guard, arg string) {
		if out[fn.Name.Name] == nil {
			out[fn.Name.Name] = map[string][]string{}
		}
		out[fn.Name.Name][guard] = append(out[fn.Name.Name][guard], arg)
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			name := calleeName(v)
			switch name {
			case "cyclicNodeName", "materializeNamed", "materializeAtPosition", "refCycleAliasDef":
				for _, a := range v.Args {
					add(name, exprText(fset, a))
				}
			}
		case *ast.IndexExpr:
			// g.nodeTypeNames[s] / g.nodesInProgress[s]: the same question asked
			// of the map directly.
			sel, ok := v.X.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "nodeTypeNames", "nodesInProgress":
				add(sel.Sel.Name, exprText(fset, v.Index))
			}
		}
		return true
	})
}

// nameKindOf derives where the name expression comes from, by following the
// assignments to it inside the function it is used in. Nothing interprocedural:
// a name a function receives and does not derive is kindParameter, which is the
// conservative answer -- it is the caller that knows whether it grows, and
// assuming it does not is the assumption that cost #348 and #349.
func nameKindOf(fset *token.FileSet, fn *ast.FuncDecl, expr ast.Expr) nameKind {
	return nameKindWithin(fset, fn, expr, map[string]bool{})
}

func nameKindWithin(fset *token.FileSet, fn *ast.FuncDecl, expr ast.Expr, seen map[string]bool) nameKind {
	switch v := expr.(type) {
	case *ast.SelectorExpr:
		if v.Sel.Name == "rootTypeName" {
			return kindDocument
		}
	case *ast.CallExpr:
		name := calleeName(v)
		switch {
		case refNameFuncs[name]:
			return kindReference
		case documentNameFuncs[name]:
			return kindDocument
		case positionNameFuncs[name]:
			return kindPosition
		case passThroughNameFuncs[name] && len(v.Args) > 0:
			return nameKindWithin(fset, fn, v.Args[0], seen)
		}
	case *ast.Ident:
		if seen[v.Name] {
			return kindParameter
		}
		seen[v.Name] = true
		kinds := map[nameKind]bool{}
		for _, rhs := range assignmentsTo(fn, v.Name) {
			kinds[nameKindWithin(fset, fn, rhs, seen)] = true
		}
		// An identifier that is only ever the parameter, or that is reassigned
		// from something none of the rules recognise, is a name this function
		// cannot vouch for.
		delete(kinds, kindParameter)
		switch {
		case len(kinds) == 1:
			for k := range kinds {
				return k
			}
		case kinds[kindReference] && !kinds[kindDocument] && !kinds[kindPosition]:
			// Every recognised derivation is the reference's; the rest are the
			// parameter it started as.
			return kindReference
		}
	}
	return kindParameter
}

// assignmentsTo returns every expression assigned to the named identifier in the
// function, including the := that introduces it.
func assignmentsTo(fn *ast.FuncDecl, name string) []ast.Expr {
	var out []ast.Expr
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != len(as.Rhs) {
			return true
		}
		for i, lhs := range as.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
				out = append(out, as.Rhs[i])
			}
		}
		return true
	})
	return out
}

func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

func exprText(fset *token.FileSet, e ast.Expr) string {
	var b strings.Builder
	if err := printer.Fprint(&b, fset, e); err != nil {
		return "<unprintable>"
	}
	return b.String()
}

func parsePackageSources(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing package sources: %v", err)
	}
	sort.Strings(names)
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no package sources parsed")
	}
	return fset, files
}

func sortedSites(m map[typeDefSite]int) []typeDefSite {
	out := make([]typeDefSite, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
