package schemagen

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// This file widens the set of documents resolveSharedDefinitionNames judges
// from "the inputs of this generation unit" to "the documents whose type
// declarations land in it".
//
// Those two sets are not the same, and the difference is what issue #297 is.
// #249 settled that two documents declaring a same-named definition must not
// silently become one Go type, and both of the guards it left behind are keyed
// on inputs: resolveSharedDefinitionNames is handed the paths the caller listed,
// and packageDecls compares the files those paths produced. A document reached
// by $ref off disk is neither -- it is never listed and never writes a file --
// yet its definitions are materialized into the referring document's file all
// the same. So two referenced documents each declaring "$defs/Inner" took one Go
// type between them, the second was discarded, and nothing said so: the exact
// outcome #249 rejected, in the one configuration its guards could not see.
//
// The answer is to make the existing rule see the case rather than to add a
// third guard beside it. Two guards that must agree and are keyed differently is
// the shape issues #178 and #203/#211 came out of, and a third would only widen
// the gap. So the claims of a referenced document are collected here and handed
// to resolveSharedDefinitionNames as claims like any other; everything that
// decides what happens to a contested name -- agreement, qualification,
// numbering, the diagnostic -- is that function's, unchanged.
//
// A referenced document contributes only the nodes a $ref actually reaches, and
// that restriction is not an optimization. The generator materializes a
// definition of another document when something refers to it and not otherwise:
// a document referenced for its "$defs/Inner" leaves its "$defs/Other" ungenerated.
// Claiming Other anyway would rename a type of the referring document to make
// room for one that is never declared, which is a defect where today there is
// none.
//
// The walk resolves through the very resolver the generator is given, so a
// document comes back as the instance the generator will see -- the pins
// resolveSharedDefinitionNames produces are keyed by node identity, and a second
// copy would carry none of them. Where this walk cannot resolve a ref it records
// nothing, which leaves that reference exactly as it behaves today: the guard
// can miss, but it cannot invent a collision.

// externalClaims is what the walk found: the claims referenced documents make,
// and the identities under which the diagnostic and the qualifier prefix know
// them.
type externalClaims struct {
	// claims are ready to be merged into collectNameClaims' map, each already
	// carrying the Go type name the generator will ask for.
	claims []nameClaim
	// byLabel maps each referenced document's label -- nameClaim.path for its
	// claims -- to the document, so the name qualifier can be derived from it
	// exactly as an input's is.
	byLabel map[string]*schema.Schema
	// labels is byLabel's keys in discovery order.
	labels []string
}

// merge returns byPath extended with the referenced documents, so a claim's path
// resolves to a document whichever kind of claim it is. The input map is not
// modified.
func (e externalClaims) merge(byPath map[string]*schema.Schema) map[string]*schema.Schema {
	if len(e.byLabel) == 0 {
		return byPath
	}
	merged := make(map[string]*schema.Schema, len(byPath)+len(e.byLabel))
	for k, v := range byPath {
		merged[k] = v
	}
	for k, v := range e.byLabel {
		merged[k] = v
	}
	return merged
}

// documentPaths returns paths followed by the referenced documents' labels, for
// the diagnostics that walk a run's documents looking for a definition by node.
func (e externalClaims) documentPaths(paths []string) []string {
	if len(e.labels) == 0 {
		return paths
	}
	out := make([]string, 0, len(paths)+len(e.labels))
	out = append(out, paths...)
	return append(out, e.labels...)
}

// collectExternalClaims walks out from the given inputs through their $refs and
// reports what the documents on the other side declare in the package the inputs
// generate into.
//
// owned is the set of document roots the run generates itself. A ref that lands
// in one of those reaches a document that already speaks for itself -- as an
// input of this unit, whose claims collectNameClaims has, or as an input that
// writes its own file, where two files declaring one name is issue #217's
// collision and packageDecls' refusal. Either way it is not this walk's question.
//
// chosenRootName is the name --root-name gives a document, or "" for a document
// no key names -- the caller's say over what a referenced document's claims are
// qualified with, reached by the flag's "id:" and "file:" keys.
func collectExternalClaims(paths []string, byPath map[string]*schema.Schema, resolver schema.SchemaResolver, owned map[*schema.Schema]bool, chosenRootName func(path string, s *schema.Schema) string) externalClaims {
	if resolver == nil {
		return externalClaims{}
	}
	w := &externalWalker{
		resolver:   resolver,
		owned:      owned,
		chosenName: chosenRootName,
		labelOf:    map[*schema.Schema]string{},
		takenLabel: map[string]bool{},
		byLabel:    map[string]*schema.Schema{},
		scanned:    map[*schema.Schema]bool{},
		claimed:    map[*schema.Schema]bool{},
	}
	// An input's path is its own label, and a referenced document must not take
	// one: collectNameClaims counts one path as one document, so two documents
	// sharing a label would have the second's claims read as repeats of the
	// first's and dropped.
	for _, path := range paths {
		w.takenLabel[path] = true
	}

	var queue []scanTarget
	for _, path := range paths {
		if s := byPath[path]; s != nil {
			queue = append(queue, scanTarget{node: s, file: path})
		}
	}
	w.run(queue)
	return externalClaims{claims: w.claims, byLabel: w.byLabel, labels: w.labels}
}

// scanTarget is a schema node whose $refs have yet to be followed, together with
// the file its document was read from -- which is what a further relative ref
// inside it is written against.
type scanTarget struct {
	node *schema.Schema
	file string
}

type externalWalker struct {
	resolver   schema.SchemaResolver
	owned      map[*schema.Schema]bool
	chosenName func(string, *schema.Schema) string

	labelOf    map[*schema.Schema]string
	takenLabel map[string]bool
	byLabel    map[string]*schema.Schema
	labels     []string

	scanned map[*schema.Schema]bool
	claimed map[*schema.Schema]bool
	claims  []nameClaim
}

// run follows every $ref reachable from the seeds, breadth first. A node is
// scanned once; a document reached again through a second reference contributes
// its claim once, which is what keeps a $ref cycle finite.
func (w *externalWalker) run(queue []scanTarget) {
	for len(queue) > 0 {
		target := queue[0]
		queue = queue[1:]
		if target.node == nil || w.scanned[target.node] {
			continue
		}
		w.scanned[target.node] = true
		for _, site := range collectRefSites(target.node) {
			if next, ok := w.follow(site, target.file); ok {
				queue = append(queue, next)
			}
		}
	}
}

// follow resolves one $ref and, when it crosses into a document this run does
// not generate, records what that document declares and returns the node to
// carry on from.
func (w *externalWalker) follow(site refSite, fromFile string) (scanTarget, bool) {
	docPart, fragment := splitRef(site.Ref)
	if docPart == "" {
		return scanTarget{}, false // stays inside its own document
	}
	doc, docURL := w.resolveDocument(docPart, site.Base)
	if doc == nil || w.owned[doc] {
		return scanTarget{}, false
	}
	// What the generator does with a document the resolver handed it, and for
	// the same reason: the refs inside it are written against its own base URI,
	// and DocumentRoot is what says whether a node reached inside it is a
	// resource root in its own right. Only a document that has not been through
	// it already, so a second reference cannot rebase the first one's document.
	if doc.DocumentRoot == nil {
		doc.ComputeBaseURIs(docURL, doc)
	}
	node := doc
	if fragment != "" {
		resolved, err := schema.NewLocalResolver(doc).Resolve("#" + fragment)
		if err != nil || resolved == nil {
			return scanTarget{}, false
		}
		node = resolved
	}
	file := externalFilePath(docPart, site.Base, fromFile)
	w.record(w.labelFor(doc, file, docPart), doc, node, site.Ref, fragment)
	return scanTarget{node: node, file: file}, true
}

// resolveDocument asks the run's own resolver for the document a ref names, in
// the order the generator asks: the URI the ref resolves to against the base in
// effect, then the ref as written. The two differ for exactly the case this walk
// exists for -- a relative path under a document whose $id is an absolute URI,
// where only the second reaches the file resolver.
//
// The URL returned is the one the successful call was made with, which is what
// the generator passes to ComputeBaseURIs for the document it just loaded.
func (w *externalWalker) resolveDocument(docPart string, base *url.URL) (*schema.Schema, *url.URL) {
	refURL, err := url.Parse(docPart)
	if err != nil {
		return nil, nil
	}
	if base != nil {
		absolute := base.ResolveReference(refURL)
		docURL := *absolute
		docURL.Fragment = ""
		if s, err := w.resolver.ResolveSchema(docURL.String(), base); err == nil && s != nil {
			return s, &docURL
		}
	}
	if s, err := w.resolver.ResolveSchema(docPart, base); err == nil && s != nil {
		docURL := *refURL
		docURL.Fragment = ""
		return s, &docURL
	}
	return nil, nil
}

// record adds the claim a referenced node makes on a Go type name, once per
// node however many references reach it.
func (w *externalWalker) record(label string, doc, node *schema.Schema, ref, fragment string) {
	if node == nil || w.claimed[node] {
		return
	}
	w.claimed[node] = true
	name, keyword, defKey := externalClaimName(doc, node, ref, fragment)
	if name == "" {
		return
	}
	w.claims = append(w.claims, nameClaim{
		path:     label,
		keyword:  keyword,
		defKey:   defKey,
		node:     node,
		final:    name,
		prefix:   w.documentPrefix(label, doc),
		external: true,
	})
}

// documentPrefix is the Go name a referenced document's contested claims are
// qualified with.
//
// A listed document is qualified with its root type name, which the caller
// chose. A referenced one often has no title and so no root type name at all,
// and "Root" in front of every untitled document separates nothing -- so the
// document's own file name stands in, which is the same derivation the
// generator's uniqueTypeName already falls back to (element in alpha.json
// becomes AlphaElement). --root-name still wins where a key names the document.
func (w *externalWalker) documentPrefix(label string, doc *schema.Schema) string {
	if w.chosenName != nil {
		if chosen := w.chosenName(label, doc); chosen != "" {
			return chosen
		}
	}
	if doc.Title != "" {
		return generator.SchemaNameToGoName(doc.Title)
	}
	if name := documentFileGoName(doc, label); name != "" {
		return name
	}
	return "Root"
}

// documentFileGoName derives a Go name from the file a document was read from:
// its base name without extension, from the base URI the generator names it by
// where there is one and from the label otherwise.
func documentFileGoName(doc *schema.Schema, label string) string {
	base := ""
	if u := documentBaseURI(doc); u != nil {
		base = path.Base(u.Path)
	}
	if base == "" || base == "." || base == "/" {
		base = filepath.Base(label)
	}
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "" {
		return ""
	}
	return generator.SchemaNameToGoName(base)
}

// documentBaseURI is the URI a document is known by, preferring its resource
// root's, exactly as the generator's documentGoName reads it.
func documentBaseURI(doc *schema.Schema) *url.URL {
	if doc == nil {
		return nil
	}
	if doc.DocumentRoot != nil && doc.DocumentRoot.BaseURI != nil {
		return doc.DocumentRoot.BaseURI
	}
	return doc.BaseURI
}

// labelFor names a referenced document for the diagnostic and for the name
// qualifier, preferring the file a caller can open over the $id -- the same
// order of preference nameClaim.path has for an input.
//
// The label is also the document's identity for --root-name, whose "file:" and
// "id:" keys are how a caller sets the qualifier for a document they never
// listed.
func (w *externalWalker) labelFor(doc *schema.Schema, file, docPart string) string {
	if label, ok := w.labelOf[doc]; ok {
		return label
	}
	label := ""
	for _, candidate := range []string{file, docIDOf(doc), docPart} {
		if candidate == "" || w.takenLabel[candidate] {
			continue
		}
		label = candidate
		break
	}
	if label == "" {
		// Every identity this document has is spoken for by another document of
		// the run. Two documents under one label would have the second's claims
		// read as the first's and dropped, so the label is numbered instead --
		// ugly in a message, but a message is all it costs.
		base := file
		if base == "" {
			base = docPart
		}
		for i := 2; ; i++ {
			candidate := fmt.Sprintf("%s (%d)", base, i)
			if !w.takenLabel[candidate] {
				label = candidate
				break
			}
		}
	}
	w.labelOf[doc] = label
	w.takenLabel[label] = true
	w.byLabel[label] = doc
	w.labels = append(w.labels, label)
	return label
}

// externalClaimName reports the Go type name a referenced node is materialized
// under, and how to describe where it came from.
//
// It has to give the same answer as the generator's goNameForResolvedRef, which
// this mirrors: a node that is a document root in its own right is named after
// its title or the last segment of its $id, and anything else after the
// reference that reached it. Where the node is one of its document's own
// definitions those two agree -- the last pointer segment is the $defs key --
// and the keyword is read off the document so the diagnostic can name the
// definition the way its author wrote it.
func externalClaimName(doc, node *schema.Schema, ref, fragment string) (name, keyword, defKey string) {
	if node == doc || node.DocumentRoot == node {
		return documentRootRefName(node, ref), "", ""
	}
	for _, container := range []struct {
		keyword string
		m       map[string]*schema.Schema
	}{{"$defs", doc.Defs}, {"definitions", doc.Definitions}} {
		for _, key := range sortedSchemaKeys(container.m) {
			if container.m[key] == node {
				return generator.SchemaNameToGoName(key), container.keyword, key
			}
		}
	}
	keyword, defKey = pointerClaimParts(fragment)
	return refDerivedTypeName(ref), keyword, defKey
}

// documentRootRefName is the Go type name a reference to a whole document
// produces: the generator's title-then-$id derivation for a resolved document
// root.
func documentRootRefName(doc *schema.Schema, ref string) string {
	if doc.Title != "" {
		return generator.SchemaNameToGoName(doc.Title)
	}
	id := doc.ID
	if id == "" {
		id = doc.LegacyID
	}
	if id != "" {
		return generator.SchemaNameToGoName(lastURIPathSegment(id))
	}
	return refDerivedTypeName(ref)
}

// pointerClaimParts splits a ref's fragment into the container the diagnostic
// names and the entry within it, so a claim reads as the location its document
// wrote it at: "/$defs/Inner" is "$defs/Inner" and "/properties/a/properties/b"
// is "properties/a/properties/b". A plain-name anchor has no container.
func pointerClaimParts(fragment string) (keyword, defKey string) {
	if fragment == "" {
		return "", ""
	}
	if !strings.HasPrefix(fragment, "/") {
		return "", unescapeRefToken(fragment)
	}
	parts := strings.Split(strings.TrimPrefix(fragment, "/"), "/")
	for i, p := range parts {
		parts[i] = unescapeRefToken(p)
	}
	last := len(parts) - 1
	return strings.Join(parts[:last], "/"), parts[last]
}

// refDerivedTypeName is the Go type name a $ref produces when what it reaches is
// not a document root: the last non-empty segment of its fragment, or of the ref
// itself when it has none.
//
// This must agree with the generator's refToGoName, which is what actually names
// the type; the two are compared against a shared table in the tests. Every step
// below is one of that function's: fragment first, then the last pointer
// segment, then the last colon-separated segment of a URN, then JSON Pointer
// unescaping and percent-decoding, then the shared identifier derivation.
func refDerivedTypeName(ref string) string {
	name := ref
	if idx := strings.LastIndex(ref, "#"); idx >= 0 {
		fragment := ref[idx+1:]
		if fragment == "" {
			return "Root"
		}
		name = fragment
	}
	if strings.Contains(name, "/") {
		parts := strings.Split(name, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			if parts[i] != "" {
				name = parts[i]
				break
			}
		}
		if name == "" || name == ref {
			return "X"
		}
	}
	if strings.Contains(name, ":") {
		parts := strings.Split(name, ":")
		name = parts[len(parts)-1]
	}
	name = unescapeRefToken(name)
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	return generator.SchemaNameToGoName(name)
}

// unescapeRefToken applies RFC 6901 unescaping, the same order the generator and
// the resolver use.
func unescapeRefToken(token string) string {
	token = strings.ReplaceAll(token, "~1", "/")
	return strings.ReplaceAll(token, "~0", "~")
}

// lastURIPathSegment mirrors the generator's lastPathSegment: the part of a
// document $id a type name is derived from.
func lastURIPathSegment(uri string) string {
	if idx := strings.LastIndex(uri, "#"); idx >= 0 {
		uri = uri[:idx]
	}
	if idx := strings.LastIndex(uri, "?"); idx >= 0 {
		uri = uri[:idx]
	}
	uri = strings.TrimSuffix(uri, "/")
	if idx := strings.LastIndex(uri, "/"); idx >= 0 {
		return uri[idx+1:]
	}
	return strings.TrimPrefix(uri, "./")
}

// externalFilePath reports the file a referenced document was read from, as a
// caller would write it. It mirrors schema.FileResolver's path derivation: a
// scheme-less ref is a path relative to the document holding it, and a file://
// ref names its path outright. A ref with any other scheme names no file, and
// the document is identified by its $id instead.
//
// Display only -- resolution itself is the resolver's, above.
func externalFilePath(docPart string, base *url.URL, fromFile string) string {
	u, err := url.Parse(docPart)
	if err != nil {
		return ""
	}
	switch {
	case u.Scheme == "file":
		return u.Path
	case u.Scheme != "":
		return ""
	case base != nil && base.Scheme == "file":
		return filepath.Join(filepath.Dir(base.Path), u.Path)
	case fromFile != "":
		return filepath.Join(filepath.Dir(fromFile), u.Path)
	default:
		return u.Path
	}
}

// ownedDocuments is the set of document roots a run generates itself.
func ownedDocuments(byPath map[string]*schema.Schema) map[*schema.Schema]bool {
	owned := make(map[*schema.Schema]bool, len(byPath))
	for _, s := range byPath {
		if s != nil {
			owned[s] = true
		}
	}
	return owned
}
