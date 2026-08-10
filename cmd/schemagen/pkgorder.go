package schemagen

import (
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// refSite is a $ref together with the base URI in effect where it appears. A
// nested $id rescopes relative refs, so the containing document's own $id is not
// a safe base — the per-node BaseURI computed by ComputeBaseURIs is.
type refSite struct {
	Ref  string
	Base *url.URL
}

// collectRefSites reports every $ref/$recursiveRef/$dynamicRef in s along with
// the base URI in effect at its position.
func collectRefSites(s *schema.Schema) []refSite {
	var out []refSite
	generator.WalkSchema(s, func(node *schema.Schema) {
		for _, ref := range []string{node.Ref, node.RecursiveRef, node.DynamicRef} {
			if ref != "" {
				out = append(out, refSite{Ref: ref, Base: node.BaseURI})
			}
		}
	})
	return out
}

// refTargetDocuments returns the candidate identities of the document a ref
// points into, most specific first. Several spellings are returned rather than
// one canonical answer because the identity is matched against caller-supplied
// $ids verbatim: a ref may name a document exactly as its $id, or relative to
// the base URI in effect. Fragment-only refs stay inside their own document and
// return nothing.
func refTargetDocuments(ref string, base *url.URL) []string {
	if ref == "" || strings.HasPrefix(ref, "#") {
		return nil
	}
	docPart := ref
	if i := strings.Index(docPart, "#"); i >= 0 {
		docPart = docPart[:i]
	}
	if docPart == "" {
		return nil
	}

	var candidates []string
	add := func(s string) {
		if s == "" {
			return
		}
		for _, seen := range candidates {
			if seen == s {
				return
			}
		}
		candidates = append(candidates, s)
	}

	// As written, and without a trailing empty fragment.
	add(docPart)
	add(strings.TrimSuffix(docPart, "#"))

	refURL, err := url.Parse(docPart)
	if err != nil {
		return candidates
	}
	if refURL.IsAbs() {
		add(refURL.String())
		add(normalizeURI(refURL))
		return candidates
	}

	// Relative: resolve against the base in effect. An opaque base (urn:, or
	// any scheme with no hierarchical part) cannot meaningfully absorb a
	// relative reference, so resolution is skipped rather than producing
	// something like "urn:///other.json".
	if base != nil && base.Opaque == "" && (base.Host != "" || strings.HasPrefix(base.Path, "/")) {
		resolved := base.ResolveReference(refURL)
		add(resolved.String())
		add(normalizeURI(resolved))
	}
	return candidates
}

// normalizeURI lowercases the scheme and host, which are case-insensitive, and
// drops an empty fragment. Paths are left alone: they are case-sensitive.
func normalizeURI(u *url.URL) string {
	if u == nil {
		return ""
	}
	c := *u
	c.Scheme = strings.ToLower(c.Scheme)
	c.Host = strings.ToLower(c.Host)
	c.Fragment = ""
	return strings.TrimSuffix(c.String(), "#")
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
// the refs responsible.
func packageDependencies(docs []packageDoc, docPackages map[string]string) map[string][]packageEdge {
	deps := make(map[string][]packageEdge)
	for _, d := range docs {
		if _, ok := deps[d.pkg]; !ok {
			deps[d.pkg] = nil
		}
		for _, site := range collectRefSites(d.schema) {
			for _, candidate := range refTargetDocuments(site.Ref, site.Base) {
				targetPkg, ok := docPackages[candidate]
				if !ok {
					continue
				}
				if targetPkg == d.pkg {
					break // same package: not a dependency
				}
				deps[d.pkg] = append(deps[d.pkg], packageEdge{FromDoc: d.id, ToDoc: candidate, Ref: site.Ref, ToPkg: targetPkg})
				break // first matching candidate wins
			}
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
func orderPackagesByDependencies(pkgOrder []string, docs []packageDoc, docPackages map[string]string) ([]string, error) {
	deps := packageDependencies(docs, docPackages)

	dependsOn := make(map[string]map[string]bool, len(deps))
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
		for pkg := range remaining {
			satisfied := true
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
// not one either, and keeps its existing failure.
func checkInputRefCycle(args []string, byPath map[string]*schema.Schema) error {
	// A ref can name another input two ways, and both have to be indexed or the
	// cycle is only found for some of the spellings. By $id, which is what an
	// absolute-URI ref resolves to; and by file path, which is what a relative
	// ref reaches when neither document declares an $id -- the shape of the
	// reproducer in issue #228 that carries no $id at all.
	pathByID := make(map[string]string, len(args)*2)
	pathByFile := make(map[string]string, len(args))
	for _, path := range args {
		s := byPath[path]
		if s == nil {
			continue
		}
		if abs, err := filepath.Abs(path); err == nil {
			pathByFile[filepath.Clean(abs)] = path
		}
		if id := docIDOf(s); id != "" {
			pathByID[id] = path
			if u, err := url.Parse(id); err == nil {
				pathByID[normalizeURI(u)] = path
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
			target := ""
			for _, candidate := range refTargetDocuments(site.Ref, site.Base) {
				if t, ok := pathByID[candidate]; ok {
					target = t
					break // first matching candidate wins
				}
			}
			if target == "" {
				target = pathByFile[refTargetFile(site, path)]
			}
			if target != "" && target != path {
				edges[path] = append(edges[path], docRefEdge{FromPath: path, ToPath: target, Ref: site.Ref})
			}
		}
	}

	cycle := findDocRefCycle(args, edges)
	if cycle == nil {
		return nil
	}
	return docRefCycleError(cycle)
}

// refTargetFile returns the absolute path a relative $ref reads, or "" when the
// ref names something a file resolver would not serve. It mirrors
// schema.FileResolver: a scheme-less ref is a path taken relative to the
// directory holding the referring schema. A ref carrying a base URI from a
// nested $id is left to the $id route above, which is the one that applies to
// it.
func refTargetFile(site refSite, fromPath string) string {
	if site.Base != nil {
		return ""
	}
	docPart := site.Ref
	if i := strings.Index(docPart, "#"); i >= 0 {
		docPart = docPart[:i]
	}
	if docPart == "" {
		return ""
	}
	u, err := url.Parse(docPart)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" {
		return ""
	}
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(fromPath), u.Path))
	if err != nil {
		return ""
	}
	return filepath.Clean(abs)
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

func quotedList(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}
