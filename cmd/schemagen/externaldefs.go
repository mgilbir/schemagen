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

// This file widens the set resolveSharedDefinitionNames judges from "the inputs
// of this generation unit" to "the schema resources whose type declarations land
// in it".
//
// The rule being enforced is #249's: two declarations of one Go type name that
// do not describe the same type must not silently become one type. It has now
// been broken four times, once per *spelling* of "two declarations", because
// the claim set was keyed on the wrong thing each time.
//
//   - #249 keyed it on the inputs of the run. resolveSharedDefinitionNames is
//     handed the paths the caller listed and packageDecls compares the files
//     those paths produced.
//   - #297 found the spelling neither guard could see: a document reached by
//     $ref off disk is never listed and writes no file, yet its definitions are
//     materialized into the referring document's file all the same. Two of them
//     declaring "$defs/Inner" took one Go type between them and nothing said so.
//   - #308 found it again one level down: two *embedded* resources of a single
//     input file, each with its own $id and its own definition namespace, each
//     declaring "$defs/X". One input, one file, no external document -- so the
//     walk #297 added, which followed refs *out of* a document, never looked.
//     A string definition and an integer one became one `type X string`, and
//     every instance setting the integer property was refused at decode.
//   - #319 found that the resource is the right key for *which* declarations
//     this unit holds and the wrong one for *whose* they are. A resource the run
//     generates as an input was exempted whole, because collectNameClaims claims
//     its definitions -- and it claims only those, so a $ref into any other
//     position of the document reached a node nobody had claimed a name for.
//     "#/$defs/A/properties/x" and "#/$defs/B/properties/x" both derive X and
//     became one `type X string`, discarding the integer. The exemption is per
//     node now rather than per resource; see cross.
//
// A parallel walk per spelling would have made a key per spelling that must
// agree with all the others, which is the shape #178 and #203/#211 came out of.
// So the key is the thing JSON Schema itself names: the **schema resource**. A
// resource is a node that establishes its own base URI -- an input document
// root, a document the resolver fetched, or a subschema carrying $id -- and each
// owns a definition namespace. The first three spellings above are then one
// case: a reference that leaves the resource it is written in reaches a resource
// whose declarations this package holds too, and what that resource declares is
// a claim like any other. The fourth adds that a reference which *stays* in a
// resource can reach a declaration nobody has claimed either, so what a claim is
// judged against is a node rather than the resource around it. See cross.
//
// A resource the run does not generate as an input contributes only the nodes a
// $ref actually reaches, and that restriction is not an optimization. The
// generator declares every definition of a document it is *given*; of any other
// resource it declares what something refers to and nothing else -- an embedded
// resource's unreferenced "$defs/Other" is not emitted at all (verified: it is
// absent from the output). Claiming Other anyway would rename a type of the
// referring document to make room for one that is never declared, which is a
// defect where today there is none.
//
// The name a claim asks for is the name generation will give the node, taken
// from the generator's own published derivations rather than described a second
// time here. That distinction was itself a defect: this walk used to name a
// referenced node after the $defs key it lives under, which agrees with
// generation for a JSON Pointer ref and disagrees for an $anchor -- a reference
// to "a.json#tee" is materialized as Tee, not as the key. So two documents whose
// $defs/P and $defs/Q both carried {"$anchor":"tee"} collapsed onto one Tee with
// the guard watching, in the multi-document spelling as well as the single one.
// See externalClaimName.
//
// The walk resolves through the run's own loaded documents and through the very
// resolver the generator is given, so a node comes back as the instance the
// generator will see -- the pins resolveSharedDefinitionNames produces are keyed
// by node identity, and a second copy would carry none of them. Where this walk
// cannot resolve a ref it records nothing, which leaves that reference exactly
// as it behaves today: the guard can miss, but it cannot invent a collision.

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
	w := &externalWalker{
		resolver:          resolver,
		owned:             owned,
		chosenName:        chosenRootName,
		labelOf:           map[*schema.Schema]string{},
		takenLabel:        map[string]bool{},
		byLabel:           map[string]*schema.Schema{},
		inputPath:         map[*schema.Schema]string{},
		definitionEntries: map[*schema.Schema]map[*schema.Schema]bool{},
		resources:         map[string]*schema.Schema{},
		indexed:           map[*schema.Schema]bool{},
		fileRoot:          map[*schema.Schema]bool{},
		scanned:           map[*schema.Schema]bool{},
		claimed:           map[*schema.Schema]bool{},
	}
	// An input's path is its own label, and a referenced resource must not take
	// one: collectNameClaims counts one path as one document, so two of them
	// sharing a label would have the second's claims read as repeats of the
	// first's and dropped.
	//
	// It is also the path a claim on a position *inside* that input carries, so
	// that such a claim reads as the document the caller listed rather than as a
	// document reached by $ref. First writer wins, for the reason
	// collectNameClaims skips a repeated path: one input listed twice is one
	// document.
	for _, path := range paths {
		w.takenLabel[path] = true
		if s := byPath[path]; s != nil {
			if _, ok := w.inputPath[s]; !ok {
				w.inputPath[s] = path
			}
		}
	}

	var queue []scanTarget
	for _, path := range paths {
		if s := byPath[path]; s != nil {
			// The resources this input embeds, so a $ref naming one by its $id
			// finds it here. No resolver can: an embedded resource's $id is not a
			// file and is not an input's $id, so the run's mapping resolver and
			// its file resolver both answer no -- which is exactly why the walk
			// could not see issue #308's collision.
			w.indexResources(s)
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
	// inputPath maps each listed input's root node to the path the caller wrote,
	// which is what a claim on a position inside that document is filed under.
	inputPath map[*schema.Schema]string
	// definitionEntries memoizes, per resource, the nodes its own $defs and
	// definitions hold. See isDefinitionEntry.
	definitionEntries map[*schema.Schema]map[*schema.Schema]bool

	// resources maps the canonical URI of every schema resource the walk has
	// seen -- the run's inputs, the resources they embed, and the same for every
	// document the resolver hands back -- to that resource's root node. It is
	// what a $ref naming an embedded resource by its $id resolves through.
	resources map[string]*schema.Schema
	indexed   map[*schema.Schema]bool
	// fileRoot marks the resources that are a whole document -- the root of a
	// file the caller could open -- as against a resource embedded inside one.
	// Only the first is named by a file path; an embedded resource's identity is
	// the $id that makes it one, and letting it take its file's name would give
	// two resources one label.
	fileRoot map[*schema.Schema]bool

	scanned map[*schema.Schema]bool
	claimed map[*schema.Schema]bool
	claims  []nameClaim
}

// indexResources records every schema resource in a document, so that a $ref
// naming one by its $id can be resolved to the node instance the generator will
// see.
//
// A resource is a node ComputeBaseURIs made its own document root, which is what
// carrying an $id means; the root itself is one whether or not it declares one.
// The first resource recorded under a URI keeps it: two documents of one run may
// declare the same $id, and picking the later one would move a claim onto a node
// the generator will not reach from here.
func (w *externalWalker) indexResources(root *schema.Schema) {
	if root == nil || w.indexed[root] {
		return
	}
	w.indexed[root] = true
	w.fileRoot[root] = true
	generator.WalkSchema(root, func(node *schema.Schema) {
		if node != root && node.DocumentRoot != node {
			return
		}
		uri := resourceURI(node)
		if uri == "" {
			return
		}
		if _, ok := w.resources[uri]; !ok {
			w.resources[uri] = node
		}
	})
}

// resourceURI is the canonical URI a schema resource is known by: the base URI
// ComputeBaseURIs computed for it, without an empty fragment. A resource whose
// document declares no $id at all has none, and is reachable only from inside
// its own document.
func resourceURI(s *schema.Schema) string {
	if s == nil || s.BaseURI == nil {
		return ""
	}
	return strings.TrimSuffix(s.BaseURI.String(), "#")
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

// follow resolves one $ref and, when it crosses out of the schema resource it
// was written in, records what the resource on the other side declares and
// returns the node to carry on from.
//
// Crossing out of the resource is the whole test, and it is the same test for a
// reference to another file and for one to a resource embedded beside it. What a
// resource declares of itself, under the names its own keys derive, is either
// claimed wholesale by collectNameClaims (an input this unit generates) or
// already counted here once; a reference that stays put reaches nothing new.
func (w *externalWalker) follow(site refSite, fromFile string) (scanTarget, bool) {
	docPart, fragment := splitRef(site.Ref)

	// The run's own documents and the resources they embed, first. A ref that
	// names an embedded resource by its $id reaches nothing else: no resolver
	// derives a file from such a URI, which is why the pre-#308 walk gave up on
	// it, and a fragment-only ref never left this document to begin with.
	if res, node, ok := w.resolveLocally(docPart, fragment, site); ok {
		if !w.cross(site.Scope, res, node, site.Ref, fragment, fromFile, docPart) {
			return scanTarget{}, false
		}
		return scanTarget{node: node, file: fromFile}, true
	}

	if docPart == "" {
		return scanTarget{}, false // stays inside its own document, and unresolvable
	}
	doc, docURL := w.resolveDocument(docPart, site.Base)
	if doc == nil {
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
	w.indexResources(doc)
	node := doc
	if fragment != "" {
		resolved, err := schema.NewLocalResolver(doc).Resolve("#" + fragment)
		if err != nil || resolved == nil {
			return scanTarget{}, false
		}
		node = resolved
	}
	file := externalFilePath(docPart, site.Base, fromFile)
	// The resource the node belongs to, which is the document only when the
	// document embeds none. A fetched document is as free to embed resources as
	// an input is, and recording the claim against the file would put two
	// namespaces under one label -- so which of them declared a contested name
	// would depend on whether the walk reached that file through this arm or
	// through resolveLocally, the second reference through a file having indexed
	// its resources for the first.
	res := node.DocumentRoot
	if res == nil {
		res = doc
	}
	if !w.cross(site.Scope, res, node, site.Ref, fragment, file, docPart) {
		return scanTarget{}, false
	}
	return scanTarget{node: node, file: file}, true
}

// resolveLocally resolves a $ref against the resources the walk already holds:
// the resource named by the ref's document part, or -- for a fragment-only ref
// -- the one the reference is written in.
//
// It answers with the resource the *resolved node* belongs to rather than the
// one the lookup started from, because those differ exactly where issue #308
// lives: "#/$defs/A/$defs/X" is a pointer through the outer document that lands
// inside the resource "$defs/A" establishes, and it is that resource's
// definition namespace the name X is claimed out of.
func (w *externalWalker) resolveLocally(docPart, fragment string, site refSite) (res, node *schema.Schema, ok bool) {
	from := site.Scope
	if docPart != "" {
		from = w.resourceNamed(docPart, site.Base)
	}
	if from == nil {
		return nil, nil, false
	}
	node = from
	if fragment != "" {
		resolved, err := schema.NewLocalResolver(from).Resolve("#" + fragment)
		if err != nil || resolved == nil {
			return nil, nil, false
		}
		node = resolved
	}
	res = node.DocumentRoot
	if res == nil {
		res = from
	}
	return res, node, true
}

// resourceNamed reports the resource a ref's document part names, resolved
// against the base URI in effect. Both spellings are tried -- the URI the ref
// resolves to and the ref as written -- because an $id is matched verbatim and a
// document may declare one that is not the URI it would be reached by.
func (w *externalWalker) resourceNamed(docPart string, base *url.URL) *schema.Schema {
	refURL, err := url.Parse(docPart)
	if err != nil {
		return nil
	}
	if base != nil {
		absolute := *base.ResolveReference(refURL)
		absolute.Fragment = ""
		if res := w.resources[strings.TrimSuffix(absolute.String(), "#")]; res != nil {
			return res
		}
	}
	return w.resources[strings.TrimSuffix(docPart, "#")]
}

// cross reports whether a resolved reference reached a node whose Go type name
// is this walk's to judge, and records the claim when it did.
//
// What is exempt is a *node* another claim already speaks for, and reading it as
// a whole resource is what issue #319 was:
//
//   - An input document root speaks for its root type and for every one of its
//     definitions, because collectNameClaims claims all of them and the
//     generator declares all of them. It speaks for nothing else. A $ref into
//     any other position of that document -- "#/$defs/A/properties/x", a
//     property of a property, a tuple slot -- reaches a node the generator
//     declares a type for all the same, named from the last segment of the
//     pointer, and nobody claimed that name. Two such refs into one document
//     collapsed onto one type with no warning and exit 0: a string at
//     $defs/A/properties/x and an integer at $defs/B/properties/x both derive
//     X, and every instance setting the integer property was judged against the
//     string. Issue #319. Those positions are claimed here, under the input's
//     own path so that the diagnostic names the file the caller listed.
//
//   - An input of the run that this generation unit does not generate: it
//     writes its own file, and two files declaring one name is issue #217's
//     collision and packageDecls' refusal rather than a name to rewrite.
//
//   - A reference that stays inside a resource no reference from outside ever
//     entered. The walk visits every $ref site of its seeds, which includes the
//     ones written inside an embedded resource nothing refers to -- and the
//     generator declares nothing of such a resource, so a claim from there
//     would move a name to make room for a type that is never emitted. Having
//     been given a label is exactly the record of having been entered.
//
// A reference that stays inside a resource the walk *did* enter is not exempt,
// and reading it as one was issue #319 one level out. What a resource declares
// of itself was said to be "counted once when the walk crossed into it", but
// crossing into it counts the single node the reference landed on: a fetched
// a.json whose own $defs/Outer points at "#/$defs/A/properties/x" and
// "#/$defs/B/properties/x" had both materialized into the referring package as
// one X, the string, with the integer discarded and no warning.
func (w *externalWalker) cross(scope, res, node *schema.Schema, ref, fragment, file, docPart string) bool {
	if res == nil || node == nil {
		return false
	}
	if path, listed := w.inputPath[res]; listed {
		if node == res || w.isDefinitionEntry(res, node) {
			return false
		}
		w.record(nameClaim{path: path}, res, node, ref, fragment)
		return true
	}
	if w.owned[res] {
		return false
	}
	if _, entered := w.labelOf[res]; !entered && res == scope {
		return false
	}
	label := w.labelFor(res, file, docPart)
	w.record(nameClaim{path: label, prefix: w.documentPrefix(label, res), external: true}, res, node, ref, fragment)
	return true
}

// isDefinitionEntry reports whether node is one of res's own $defs or
// definitions entries -- the nodes collectNameClaims claims for a listed input,
// as against a position some $ref reached inside one of them.
//
// The set is built once per resource rather than scanned per reference: this is
// asked for every $ref a document holds, and a meta-schema-sized document has
// hundreds of each.
func (w *externalWalker) isDefinitionEntry(res, node *schema.Schema) bool {
	entries, ok := w.definitionEntries[res]
	if !ok {
		entries = make(map[*schema.Schema]bool, len(res.Defs)+len(res.Definitions))
		for _, m := range []map[string]*schema.Schema{res.Defs, res.Definitions} {
			for _, def := range m {
				entries[def] = true
			}
		}
		w.definitionEntries[res] = entries
	}
	return entries[node]
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
	if w.resolver == nil {
		// A run with no resolver still has the resources its own inputs embed,
		// which resolveLocally has already been asked about. There is nothing
		// off disk to reach.
		return nil, nil
	}
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
// node however many references reach it. into carries the identity the caller
// settled on -- the path the claim is filed under, and, for a document nobody
// listed, the prefix a contested name is qualified with.
func (w *externalWalker) record(into nameClaim, doc, node *schema.Schema, ref, fragment string) {
	if node == nil || w.claimed[node] {
		return
	}
	w.claimed[node] = true
	name, keyword, defKey := externalClaimName(doc, node, ref, fragment)
	if name == "" {
		return
	}
	into.keyword = keyword
	into.defKey = defKey
	into.node = node
	into.final = name
	w.claims = append(w.claims, into)
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

// labelFor names a referenced resource for the diagnostic and for the name
// qualifier, preferring the file a caller can open over the $id -- the same
// order of preference nameClaim.path has for an input.
//
// A resource embedded inside a document reverses that preference: its file is
// its *container's*, and a message naming the file would say nothing about which
// of the resources in it declared the contested name. Its $id is what makes it a
// resource at all, so that is what names it.
//
// The label is also the resource's identity for --root-name, whose "file:" and
// "id:" keys are how a caller sets the qualifier for something they never
// listed.
func (w *externalWalker) labelFor(doc *schema.Schema, file, docPart string) string {
	if label, ok := w.labelOf[doc]; ok {
		return label
	}
	candidates := []string{file, docIDOf(doc), docPart}
	if !w.fileRoot[doc] {
		candidates = []string{docIDOf(doc), docPart, file}
	}
	label := ""
	for _, candidate := range candidates {
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
// It has to give the same answer as the generator's goNameForResolvedRef, and
// what is restated here is only which of that function's arms applies: a node
// that is a schema resource in its own right is named after its title or its
// $id, and anything else after the reference that reached it. The names
// themselves are the generator's own -- TypeNameForDocumentID and TypeNameForRef
// -- so there is no second derivation here to drift from the first. Issue #303
// left one behind for a turn, because the work could not touch pkg/generator at
// the time; it is gone.
//
// The name for a node inside a resource is the *reference's*, never the $defs
// key the node lives under. Those two agree for a JSON Pointer, whose last token
// is that key, and this used to answer with the key on the strength of that. They
// disagree for an $anchor: {"$defs":{"P":{"$anchor":"tee",...}}} referred to as
// "a.json#tee" is materialized as Tee and was claimed as P, so the claim was
// filed under a name nothing would ever declare and the collision on Tee went
// unseen -- one document's P and another's Q silently became a single type, with
// the guard running. The key is still read off the resource, because the
// diagnostic has to name the definition the way its author wrote it; it is only
// no longer mistaken for the Go name.
func externalClaimName(res, node *schema.Schema, ref, fragment string) (name, keyword, defKey string) {
	if node == res || node.DocumentRoot == node {
		return documentRootRefName(node, ref), "", ""
	}
	for _, container := range []struct {
		keyword string
		m       map[string]*schema.Schema
	}{{"$defs", res.Defs}, {"definitions", res.Definitions}} {
		for _, key := range sortedSchemaKeys(container.m) {
			if container.m[key] == node {
				return generator.TypeNameForRef(ref), container.keyword, key
			}
		}
	}
	keyword, defKey = pointerClaimParts(fragment)
	return generator.TypeNameForRef(ref), keyword, defKey
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
		return generator.TypeNameForDocumentID(id)
	}
	return generator.TypeNameForRef(ref)
}

// pointerClaimParts splits a ref's fragment into the container the diagnostic
// names and the entry within it, so a claim reads as the location its document
// wrote it at: "/$defs/Inner" is "$defs/Inner" and "/properties/a/properties/b"
// is "properties/a/properties/b". A plain-name anchor has no container.
//
// Each token is decoded the resolver's way, so the message names the key the
// document actually holds rather than the spelling the ref reached it by: a
// claim from "#/properties/%7E1" reads as properties// -- the property really
// is called "/" -- where it used to echo the escape back untouched.
func pointerClaimParts(fragment string) (keyword, defKey string) {
	if fragment == "" {
		return "", ""
	}
	if !strings.HasPrefix(fragment, "/") {
		return "", schema.UnescapePointerToken(fragment)
	}
	parts := strings.Split(strings.TrimPrefix(fragment, "/"), "/")
	for i, p := range parts {
		parts[i] = schema.UnescapePointerToken(p)
	}
	last := len(parts) - 1
	return strings.Join(parts[:last], "/"), parts[last]
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
