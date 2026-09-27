package schemagen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// refSite is a $ref together with the node it is written on. A nested $id
// rescopes relative refs, so the containing document's own $id is not a safe
// base; resolving the ref on Node through the run's resource index reads the
// base URI in effect there.
type refSite struct {
	Ref  string
	Node *schema.Schema
	// Scope is the schema resource the reference is written in: the nearest
	// enclosing $id, or the document root where there is none. It is what a
	// fragment-only ref resolves against, and it is what says whether a
	// reference stays inside its own resource or crosses out of it — the
	// question collectExternalClaims is keyed on. ComputeBaseURIs computes it.
	Scope *schema.Schema
}

// collectRefSites reports every $ref/$recursiveRef/$dynamicRef in s along with
// the node it is written on and the schema resource in effect there.
func collectRefSites(s *schema.Schema) []refSite {
	var out []refSite
	generator.WalkSchema(s, func(node *schema.Schema) {
		for _, ref := range []string{node.Ref, node.RecursiveRef, node.DynamicRef} {
			if ref != "" {
				out = append(out, refSite{Ref: ref, Node: node, Scope: node.DocumentRoot})
			}
		}
	})
	return out
}

// siteTargetDocument resolves a reference site through the run's resource index
// and returns the root of the registered document it lands in, or nil for a
// reference that does not resolve.
//
// The two orderings below -- packages by the $refs between their documents, and
// --shared-types inputs by the same -- used to read a ref's target off a list of
// URI spellings matched against input $ids, plus a file path joined by hand
// onto the referring input's directory. Both were a second resolver beside the
// generator's, and a second resolver answers differently: a ref inside an
// embedded resource with a relative $id was joined onto the input's directory
// rather than the resource's. The edge is now the answer generation itself
// will get. A $dynamicRef or $recursiveRef is resolved as the plain reference it
// is spelled as, which is where its static target is and so the document it
// needs generated first.
func siteTargetDocument(index *schema.ResourceIndex, site refSite) *schema.Schema {
	if index == nil || site.Node == nil {
		return nil
	}
	target, err := index.Resolve(site.Ref, site.Node)
	if err != nil || target == nil {
		return nil
	}
	return index.DocumentOf(target)
}

// packageDoc is the minimal view of an input document needed to order packages.
type packageDoc struct {
	id     string
	pkg    string
	path   string
	schema *schema.Schema
}

// packageEdge records why one package depends on another, so a cycle can be
// reported in terms of the refs that formed it.
type packageEdge struct {
	// FromDoc is the document holding the $ref; ToDoc is the document it
	// reaches, owned by ToPkg. Cycle reporting names ToDoc, since that is the
	// document that has to move to break the cycle.
	FromDoc string
	ToDoc   string
	Ref     string
	ToPkg   string
}

// packageDependencies reports, for each package, the packages it $refs into and
// the refs responsible. A ref's target is found through the run's resource
// index, and the package it lands in is the package of the input document that
// holds it -- which covers a resource embedded in that document as well.
func packageDependencies(docs []packageDoc, index *schema.ResourceIndex) map[string][]packageEdge {
	owner := make(map[*schema.Schema]packageDoc, len(docs))
	for _, d := range docs {
		if _, ok := owner[d.schema]; !ok {
			owner[d.schema] = d
		}
	}
	deps := make(map[string][]packageEdge)
	for _, d := range docs {
		if _, ok := deps[d.pkg]; !ok {
			deps[d.pkg] = nil
		}
		for _, site := range collectRefSites(d.schema) {
			to, ok := owner[siteTargetDocument(index, site)]
			if !ok || to.pkg == d.pkg {
				continue // a document no package owns, or this package
			}
			deps[d.pkg] = append(deps[d.pkg], packageEdge{FromDoc: d.id, ToDoc: to.id, Ref: site.Ref, ToPkg: to.pkg})
		}
	}
	return deps
}

// orderPackagesByDependencies returns pkgOrder rearranged so every package is
// generated after the packages it $refs into. A $ref into a package that has
// not been generated yet cannot be emitted as an import, so the order is derived
// rather than trusted from the command line. Ties keep the caller's original
// order, making the result deterministic. Mutually-referencing packages cannot
// be ordered — that would be an import cycle in Go — so they are reported.
func orderPackagesByDependencies(pkgOrder []string, docs []packageDoc, index *schema.ResourceIndex) ([]string, error) {
	deps := packageDependencies(docs, index)

	dependsOn := make(map[string]map[string]bool, len(deps))
	// maporder: fills a set; the same members end up in it in any order.
	for pkg, edges := range deps {
		dependsOn[pkg] = make(map[string]bool, len(edges))
		for _, e := range edges {
			dependsOn[pkg][e.ToPkg] = true
		}
	}

	position := make(map[string]int, len(pkgOrder))
	for i, pkg := range pkgOrder {
		position[pkg] = i
	}

	remaining := make(map[string]bool, len(pkgOrder))
	for _, pkg := range pkgOrder {
		remaining[pkg] = true
	}

	ordered := make([]string, 0, len(pkgOrder))
	for len(remaining) > 0 {
		ready := make([]string, 0, len(remaining))
		// maporder: the ready set is sorted by input position, which is distinct per package, before it is used.
		for pkg := range remaining {
			satisfied := true
			// maporder: a predicate; it returns the same answer whichever member it stops at.
			for dep := range dependsOn[pkg] {
				if remaining[dep] {
					satisfied = false
					break
				}
			}
			if satisfied {
				ready = append(ready, pkg)
			}
		}
		if len(ready) == 0 {
			return nil, cycleError(remaining, deps)
		}
		sort.Slice(ready, func(i, j int) bool { return position[ready[i]] < position[ready[j]] })
		for _, pkg := range ready {
			ordered = append(ordered, pkg)
			delete(remaining, pkg)
		}
	}
	return ordered, nil
}

// cycleError describes the refs that made the remaining packages unorderable.
func cycleError(remaining map[string]bool, deps map[string][]packageEdge) error {
	stuck := make([]string, 0, len(remaining))
	for pkg := range remaining {
		stuck = append(stuck, pkg)
	}
	sort.Strings(stuck)

	var lines []string
	for _, pkg := range stuck {
		for _, e := range deps[pkg] {
			if remaining[e.ToPkg] {
				lines = append(lines, fmt.Sprintf("%s (%q) references %q (package %s) via %q", pkg, e.FromDoc, e.ToDoc, e.ToPkg, e.Ref))
			}
		}
	}
	sort.Strings(lines)
	return fmt.Errorf(
		"packages %s reference each other across documents, which Go cannot express as an import cycle:\n  %s\nassign the mutually-referencing documents to one package, or extract the shared definitions into a third",
		strings.Join(quotedList(stuck), ", "), strings.Join(lines, "\n  "))
}

// docRefEdge records that one input document $refs another, and the ref that
// did it, so a cycle can be reported in the schema author's own terms.
type docRefEdge struct {
	// FromPath/ToPath are the input paths as the caller wrote them: a message
	// naming a file the caller can open is worth more than one naming a $id.
	FromPath string
	ToPath   string
	Ref      string
}

// checkInputRefCycle refuses a --shared-types run whose input documents $ref
// each other in a circle.
//
// One package means one generator and one pass over the inputs, so a document
// has to be generated after every document it references: the first pass to
// reach a type materializes it, and a later input whose root would be that same
// type finds the name taken. When the references run in a circle no order
// satisfies that, and the failure the caller used to get named the root type
// names ("root type %q was already generated by an earlier schema...") and
// advised making them distinct — which describes a different problem and cannot
// fix this one, since the names already are distinct. Issue #228.
//
// Only genuine cycles are reported. A document referencing itself is not one:
// a $ref back into the document being generated is resolved inside that
// document and materializes nothing new. A set that merely needs reordering is
// not one either — an order exists there, so the run is attempted and the
// collision it may hit is explained by explainRootTypeCollision.
func checkInputRefCycle(args []string, edges map[string][]docRefEdge) error {
	cycle := findDocRefCycle(args, edges)
	if cycle == nil {
		return nil
	}
	return docRefCycleError(cycle)
}

// buildDocRefEdges maps each input document to the other input documents it
// $refs, keyed and valued by the paths the caller gave.
//
// Both the cycle refusal and the wrong-order diagnostic read this graph, and
// both are about the same relation -- "this document has to be generated after
// that one" -- so they must agree on which refs form it. Self-edges are left
// out: a $ref back into the document being generated is resolved inside that
// document and materializes nothing new.
//
// A ref's target is found through the run's resource index, so an input named
// by its $id, by a relative path, or by a path through an embedded resource's
// own base URI is one edge by the same rule generation resolves it by. See
// siteTargetDocument.
func buildDocRefEdges(args []string, byPath map[string]*schema.Schema, index *schema.ResourceIndex) map[string][]docRefEdge {
	// One document listed under two paths is one document, reported under the
	// first path given.
	pathOf := make(map[*schema.Schema]string, len(args))
	for _, path := range args {
		if s := byPath[path]; s != nil {
			if _, ok := pathOf[s]; !ok {
				pathOf[s] = path
			}
		}
	}

	edges := make(map[string][]docRefEdge, len(args))
	for _, path := range args {
		s := byPath[path]
		if s == nil {
			continue
		}
		if _, ok := edges[path]; !ok {
			edges[path] = nil
		}
		for _, site := range collectRefSites(s) {
			target, ok := pathOf[siteTargetDocument(index, site)]
			if ok && target != path && pathOf[s] != target {
				edges[path] = append(edges[path], docRefEdge{FromPath: path, ToPath: target, Ref: site.Ref})
			}
		}
	}
	return edges
}

// findDocRefCycle returns the edges of one cycle in the document reference
// graph, in order, or nil when the graph is acyclic. Inputs are visited in the
// order the caller gave them so the reported cycle is the same on every run.
func findDocRefCycle(args []string, edges map[string][]docRefEdge) []docRefEdge {
	const (
		unvisited = 0
		onStack   = 1
		done      = 2
	)
	state := make(map[string]int, len(args))
	var stack []docRefEdge

	var walk func(path string) []docRefEdge
	walk = func(path string) []docRefEdge {
		state[path] = onStack
		for _, e := range edges[path] {
			switch state[e.ToPath] {
			case onStack:
				// Cut the stack back to where the target sits: everything from
				// there on is the cycle, and anything before it is only the way
				// in.
				cycle := append(append([]docRefEdge{}, stack...), e)
				for i, se := range cycle {
					if se.FromPath == e.ToPath {
						return cycle[i:]
					}
				}
				return cycle
			case unvisited:
				stack = append(stack, e)
				if found := walk(e.ToPath); found != nil {
					return found
				}
				stack = stack[:len(stack)-1]
			}
		}
		state[path] = done
		return nil
	}

	for _, path := range args {
		if state[path] == unvisited {
			stack = stack[:0]
			if found := walk(path); found != nil {
				return found
			}
		}
	}
	return nil
}

// docRefCycleError phrases the refusal: what is wrong, what schemagen did about
// it, and the two shapes that do generate.
func docRefCycleError(cycle []docRefEdge) error {
	lines := make([]string, 0, len(cycle))
	for _, e := range cycle {
		lines = append(lines, fmt.Sprintf("%s references %s via %q", e.FromPath, e.ToPath, e.Ref))
	}
	return fmt.Errorf(
		"circular reference between input documents:\n  %s\n"+
			"--shared-types emits each type once and generates the inputs in one pass, so every document must be generated after the documents it references. A cycle has no such order, so this set cannot be generated and nothing was written. "+
			"Merge the mutually-referencing documents into one document (a $ref cycle *within* a document is supported), or generate each of them in its own run and Go package, which gives each package its own copy of the other's types",
		strings.Join(lines, "\n  "))
}

// explainRootTypeCollision turns a --shared-types root type collision into the
// message for whichever of its two causes actually applies.
//
// A collision says only that the name was taken; it does not say by what. Two
// unrelated documents can claim one root name, and then the generator's own
// message is right: rename one. But the name is equally taken when an earlier
// input $refs this document, because reaching a document materializes its root
// type — and there the names are already distinct, so "give each schema a
// distinct root name" sends the caller to fix something that is not broken. It
// is the input order that is wrong. Issue #228.
//
// Which one it is, is decided from what the run has already generated rather
// than guessed: generatedRoots holds the root type name each earlier input
// claimed for itself, so a name found there is a genuine duplicate. That test
// comes first, because a set can be both — an earlier document that references
// this one *and* claims its name is not fixed by reordering — and only the
// duplicate message is right about it then.
//
// Cycles never arrive here: checkInputRefCycle refuses them before any document
// is generated.
func explainRootTypeCollision(collided string, collision *generator.RootTypeCollisionError, generated []string, generatedRoots map[string]string, edges map[string][]docRefEdge) error {
	wrapped := fmt.Errorf("generating IR for %s: %w", collided, collision)
	if _, duplicate := generatedRoots[collision.Name]; duplicate {
		return wrapped
	}
	chain := findRefChainTo(collided, generated, edges)
	if chain == nil {
		// The name was materialized by something other than a root or a ref
		// into this document -- a definition inside an earlier document that
		// happens to be named the same. Nothing here is better informed than
		// the generator's own message.
		return wrapped
	}
	return docRefOrderError(collided, collision.Name, chain)
}

// findRefChainTo returns the $ref chain by which one of the already generated
// inputs reaches target, starting from the earliest input that reaches it at
// all, or nil when none does. Inputs are tried in the order the caller gave
// them, and each search takes a shortest route, so the chain reported for a set
// is the same on every run.
//
// target is never itself in generated: an input that generated cannot then
// collide, and an input listed twice collides on its own root name, which the
// duplicate test in explainRootTypeCollision answers before this runs.
func findRefChainTo(target string, generated []string, edges map[string][]docRefEdge) []docRefEdge {
	for _, from := range generated {
		if chain := refChain(from, target, edges); chain != nil {
			return chain
		}
	}
	return nil
}

// refChain returns the edges of a shortest path from one document to another.
func refChain(from, target string, edges map[string][]docRefEdge) []docRefEdge {
	visited := map[string]bool{from: true}
	var queue [][]docRefEdge
	extend := func(prefix []docRefEdge, at string) {
		for _, e := range edges[at] {
			if visited[e.ToPath] {
				continue
			}
			visited[e.ToPath] = true
			queue = append(queue, append(append([]docRefEdge{}, prefix...), e))
		}
	}
	extend(nil, from)
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		last := path[len(path)-1]
		if last.ToPath == target {
			return path
		}
		extend(path, last.ToPath)
	}
	return nil
}

// docRefOrderError phrases the wrong-order refusal: which document went first,
// what that materialized, which input then collided, and the order that works.
//
// The order it names is the chain read backwards, which is the constraint the
// chain itself states and nothing more. Deriving an order for the whole input
// set is deliberately not attempted.
//
// The claim that renaming will not help is likewise held to what is known: the
// document that went first has already been generated under a name that is not
// the one that collided, so those two are certainly distinct. Whether some
// input still to come also wants this name is not known here, and is not
// claimed.
func docRefOrderError(collided, rootType string, chain []docRefEdge) error {
	lines := make([]string, 0, len(chain))
	for _, e := range chain {
		lines = append(lines, fmt.Sprintf("%s references %s via %q", e.FromPath, e.ToPath, e.Ref))
	}
	order := make([]string, 0, len(chain)+1)
	for i := len(chain) - 1; i >= 0; i-- {
		order = append(order, chain[i].ToPath)
	}
	order = append(order, chain[0].FromPath)

	first := chain[0].FromPath
	return fmt.Errorf(
		"input documents are in the wrong order: %s was generated before %s, which it reaches:\n  %s\n"+
			"Generating %s materialized %s's root type %q, so the later pass over %s found that name already taken and stopped -- after the earlier inputs had been written. "+
			"--shared-types emits each type once and generates the inputs in the order given, so a document must be listed before every document that references it. "+
			"%s and %s already claim different root names, so renaming them will not help; list these documents in the order: %s",
		first, collided, strings.Join(lines, "\n  "),
		first, collided, rootType, collided,
		first, collided, strings.Join(order, ", "))
}

func quotedList(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}
