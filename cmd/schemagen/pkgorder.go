package schemagen

import (
	"fmt"
	"net/url"
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
	FromDoc string
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
				deps[d.pkg] = append(deps[d.pkg], packageEdge{FromDoc: d.id, Ref: site.Ref, ToPkg: targetPkg})
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
				lines = append(lines, fmt.Sprintf("%s references %q (package %s) via %q", pkg, e.FromDoc, e.ToPkg, e.Ref))
			}
		}
	}
	sort.Strings(lines)
	return fmt.Errorf(
		"packages %s reference each other across documents, which Go cannot express as an import cycle:\n  %s\nassign the mutually-referencing documents to one package, or extract the shared definitions into a third",
		strings.Join(quotedList(stuck), ", "), strings.Join(lines, "\n  "))
}

func quotedList(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}
