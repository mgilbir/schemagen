package schema

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// ResourceIndex is the one place a reference is resolved.
//
// A reference is resolved against the base URI of the schema resource it is
// written in (RFC 3986 §5; JSON Schema 2020-12 §8.2.1 and §9.1.2): a relative
// reference becomes an absolute URI, the part before "#" names a resource, and
// the fragment is a JSON Pointer from that resource's root or a plain-name
// anchor declared in that resource. The index answers the second half by
// keeping every resource of every document it has been given, keyed by that
// absolute URI.
//
// What the index replaces is a stack of lookups that each answered part of the
// question from somewhere else. The generator consulted an index of the *root*
// document's $defs and anchors before it looked at the document a reference was
// written in, so "#/$defs/Name" written in other.json meant the root's Name
// whenever the root had one, and "other.json#anchor" dropped its document part
// and did the same. The command line resolved a relative reference against the
// first input's directory, or against whichever of several input directories
// answered first, whatever file the reference was written in. Both read the
// wrong schema in silence. Here there is one key -- (absolute resource URI,
// fragment) -- and every path that resolves a reference asks it.
//
// Every document registered gets an absolute base URI: the URI it was
// retrieved from (file:// for a file, the final URL for an HTTP fetch, the key a
// MappingResolver serves it under), overridden by its own "$id" as usual. A
// document that came from nowhere the index can name -- one a caller decoded
// themselves and handed over -- is given a URI unique to it under the
// "schemagen-document" scheme, which §9.1.1 permits ("a suitable
// implementation-specific default URI"). That default can be resolved against,
// so the "$id"s embedded in such a document are absolute keys like any other,
// and it can never be fetched, so nothing is ever asked for under it.
//
// A document reachable by several URIs -- an input listed by path and named by
// its "$id", or a file reached by two relative spellings -- is registered once
// and answers to all of them, so it is one *Schema instance however it is
// reached. The generated-type registries key on that instance.
//
// Documents the index has not seen are asked of its loader, a SchemaResolver,
// always for a whole document under an absolute URI, and registered before
// anything in them is resolved.
type ResourceIndex struct {
	loader SchemaResolver

	byURI     map[string]*Resource
	byRoot    map[*Schema]*Resource
	documents map[*Schema]*Resource

	anonymous int

	// draft is the dialect a node is read under where normalization settled
	// none for it; see WithIndexDraft.
	draft Draft
}

// AnonymousDocumentScheme is the URI scheme of the base URI a document is given
// when it was registered with no retrieval URI and declares no absolute "$id".
// Nothing is ever fetched under it.
const AnonymousDocumentScheme = "schemagen-document"

// ResourceIndexOption configures a ResourceIndex.
type ResourceIndexOption func(*ResourceIndex)

// WithIndexDraft states the dialect the documents are read under where their
// normalization settled none: a caller that decodes a document, calls
// Normalize, and generates it under Config.Draft. It matters to what a
// resource is -- through draft 7 an "$id" beside a "$ref" is ignored with the
// rest of the $ref's siblings (see refReplacesSiblings) -- so it has to be the
// dialect the generator reads the document under.
func WithIndexDraft(d Draft) ResourceIndexOption {
	return func(x *ResourceIndex) { x.draft = d }
}

// NewResourceIndex returns an index that asks loader for documents it does not
// hold. loader may be nil, and the index then resolves only within the
// documents registered with it.
func NewResourceIndex(loader SchemaResolver, opts ...ResourceIndexOption) *ResourceIndex {
	x := &ResourceIndex{
		loader:    loader,
		byURI:     make(map[string]*Resource),
		byRoot:    make(map[*Schema]*Resource),
		documents: make(map[*Schema]*Resource),
	}
	for _, opt := range opts {
		opt(x)
	}
	return x
}

// Loader is the resolver the index asks for documents it does not hold.
func (x *ResourceIndex) Loader() SchemaResolver { return x.loader }

// DuplicateIdentifierError reports two schema resources that claim one URI.
//
// JSON Schema 2020-12 §9.1.2: "there is no way for a URI to identify more than
// one schema. When multiple schemas try to identify as the same URI, validators
// SHOULD raise an error condition." 2019-09 says the same. The rule is applied
// to every dialect here, and across every document of one index as well as
// within one document, for the reason the spec gives it: the index is the set
// of schemas a run is using, and a URI that names two of them names neither.
// Picking one -- the first registered, or the last -- is how two walks of one
// document came to give two answers (the generator's resource list kept the
// last, the resource graph the first).
type DuplicateIdentifierError struct {
	URI string
	// First and Second describe the two claims: where each resource sits.
	First, Second string
}

func (e *DuplicateIdentifierError) Error() string {
	return fmt.Sprintf("duplicate $id %q: two schemas identify as it (%s, and %s), and a URI identifies one schema, so give one of them another $id", e.URI, e.First, e.Second)
}

// AmbiguousAnchorError reports a plain-name fragment that two nodes of one
// resource declare, reached by a reference.
//
// 2020-12 §8.2.2: "The effect of specifying the same fragment name multiple
// times within the same resource ... is undefined. Implementations MAY raise an
// error if such usage is detected." It is raised here only when a reference
// actually names the fragment, since that is the only time which node answers
// matters, and then it is raised rather than answered, since any answer would
// be a guess the document did not make.
type AmbiguousAnchorError struct {
	Resource string
	Anchor   string
}

func (e *AmbiguousAnchorError) Error() string {
	return fmt.Sprintf("anchor %q is declared by more than one schema in resource %s, so a reference to it names none of them", e.Anchor, e.Resource)
}

// ErrUnregisteredContext is the reason a fragment-only reference is refused when
// the schema it is written on belongs to no document the index holds: which
// resource "#..." is read in is exactly what is unknown, and no resource is
// substituted for it.
var ErrUnregisteredContext = errors.New("the schema holding it belongs to no registered document, so the resource it is read in is unknown")

// ReferenceError reports a reference the index could not resolve: why, and what
// the loader said about each document it was asked for on the way.
type ReferenceError struct {
	Ref string
	// Reason is the index's own conclusion.
	Reason error
	// Loads holds the loader's answer for every document it was asked for and
	// could not supply, in the order asked.
	Loads []error
}

func (e *ReferenceError) Error() string {
	if len(e.Loads) == 0 {
		return fmt.Sprintf("resolving %q: %v", e.Ref, e.Reason)
	}
	return fmt.Sprintf("resolving %q: %v: %v", e.Ref, e.Reason, errors.Join(e.Loads...))
}

func (e *ReferenceError) Unwrap() []error {
	return append([]error{e.Reason}, e.Loads...)
}

// AddDocument registers a whole document, retrieved from retrieval (which may be
// nil when it came from nowhere nameable; doc.RetrievalURI is used then, if
// set). It computes the base URI of every node, registers every resource the
// document holds -- the root, and every subschema whose "$id" starts one -- and
// indexes each resource's anchors.
//
// A document registered already is the same document: nothing is recomputed,
// and retrieval, when given, becomes one more URI it answers to.
//
// Nothing is registered when the document claims a URI that another resource
// already holds, in this document or in another; the error says which two.
func (x *ResourceIndex) AddDocument(doc *Schema, retrieval *url.URL) error {
	if doc == nil {
		return fmt.Errorf("resource index: nil document")
	}
	if res, ok := x.documents[doc]; ok {
		if retrieval != nil {
			return x.alias(retrieval, res)
		}
		return nil
	}
	if retrieval == nil {
		retrieval = doc.RetrievalURI
	}
	base := retrieval
	if base != nil {
		base = withoutFragment(base)
		if doc.RetrievalURI == nil {
			doc.RetrievalURI = base
		}
	} else {
		x.anonymous++
		base = &url.URL{Scheme: AnonymousDocumentScheme, Host: "doc-" + strconv.Itoa(x.anonymous), Path: "/"}
	}
	doc.computeBaseURIs(base, doc, x.draft)

	resources := documentResources(doc, x.draft)
	// Validate everything before committing anything, so a refused document
	// leaves the index as it was.
	claims := make(map[string]*Resource, len(resources)+1)
	claim := func(key string, res *Resource) error {
		if key == "" {
			return nil
		}
		if other, ok := claims[key]; ok && other.Root != res.Root {
			return &DuplicateIdentifierError{URI: key, First: describeResource(other), Second: describeResource(res)}
		}
		if other, ok := x.byURI[key]; ok && other.Root != res.Root {
			return &DuplicateIdentifierError{URI: key, First: describeResource(other), Second: describeResource(res)}
		}
		claims[key] = res
		return nil
	}
	for _, res := range resources {
		if res.Root.BaseURI == nil {
			continue // a boolean document: known by its retrieval URI alone
		}
		if err := claim(resourceKey(res.Root.BaseURI), res); err != nil {
			return err
		}
	}
	if retrieval != nil && retrieval.IsAbs() {
		if err := claim(resourceKey(retrieval), resources[0]); err != nil {
			return err
		}
	}

	// maporder: copies members under their own keys, which are distinct, so no order writes a different map.
	for key, res := range claims {
		x.byURI[key] = res
	}
	for _, res := range resources {
		res.document = doc
		x.byRoot[res.Root] = res
	}
	x.documents[doc] = resources[0]
	return nil
}

// alias makes one more URI name a registered resource.
func (x *ResourceIndex) alias(u *url.URL, res *Resource) error {
	if u == nil || !u.IsAbs() {
		return nil
	}
	key := resourceKey(u)
	if other, ok := x.byURI[key]; ok {
		if other.Root != res.Root {
			return &DuplicateIdentifierError{URI: key, First: describeResource(other), Second: describeResource(res)}
		}
		return nil
	}
	x.byURI[key] = res
	return nil
}

// documentResources lists the resources of a document whose base URIs have been
// computed: the document root first, then every node that is its own document
// root, in the order subSchemas visits them.
func documentResources(doc *Schema, fallback Draft) []*Resource {
	var out []*Resource
	seen := make(map[*Schema]bool)
	var walk func(s *Schema)
	walk = func(s *Schema) {
		if s == nil || seen[s] {
			return
		}
		seen[s] = true
		if s == doc || s.DocumentRoot == s {
			out = append(out, newResource(s, fallback))
		}
		for _, sub := range subSchemas(s) {
			walk(sub)
		}
	}
	walk(doc)
	return out
}

// newResource indexes one resource rooted at root.
func newResource(root *Schema, fallback Draft) *Resource {
	res := &Resource{
		CanonicalURI:   canonicalResourceURI(root),
		Draft:          DetectDraft(root),
		Root:           root,
		Anchors:        make(map[string]*Schema),
		DynamicAnchors: make(map[string]*Schema),
	}
	if root.BaseURI == nil {
		res.CanonicalURI = ""
	}
	collectResourceAnchors(root, res, true, fallback)
	return res
}

// describeResource says where a resource sits, for a duplicate-identifier
// message: the resource's root in its document, and the document's URI.
func describeResource(res *Resource) string {
	doc, tokens, ok := res.Root.SourceLocation()
	file := "its document"
	if ok && doc != nil && doc.RetrievalURI != nil && doc.RetrievalURI.Scheme != AnonymousDocumentScheme {
		file = displayURI(doc.RetrievalURI)
	}
	if ok && len(tokens) > 0 {
		return PointerFragment(tokens...) + " in " + file
	}
	return "the root of " + file
}

// displayURI writes a URI for a message: a file URI as the path it names.
func displayURI(u *url.URL) string {
	if u.Scheme == "file" {
		return filepath.FromSlash(u.Path)
	}
	return u.String()
}

// resourceKey is the key a resource is indexed under: the URI without a
// fragment and without an empty trailing "#", its scheme and host in lower case
// -- the two parts RFC 3986 §6.2.2.1 makes case-insensitive, so that
// "https://EX.test/a.json" names the resource "https://ex.test/a.json" declares.
// Paths are case-sensitive and are left alone.
func resourceKey(u *url.URL) string {
	c := withoutFragment(u)
	c.Scheme = strings.ToLower(c.Scheme)
	c.Host = strings.ToLower(c.Host)
	return strings.TrimSuffix(c.String(), "#")
}

func withoutFragment(u *url.URL) *url.URL {
	c := *u
	c.Fragment, c.RawFragment = "", ""
	return &c
}

// ResourceOf returns the resource a schema node is written in: the resource
// root ComputeBaseURIs recorded for it, or, for a node no document walk has
// visited (a subschema parsed on demand out of an unknown keyword's value), the
// nearest node the document wrote it under that is a registered resource root.
func (x *ResourceIndex) ResourceOf(s *Schema) *Resource {
	if s == nil {
		return nil
	}
	if s.DocumentRoot != nil {
		if res := x.byRoot[s.DocumentRoot]; res != nil {
			return res
		}
	}
	for n := s; n != nil; n = n.src.parent {
		if res := x.byRoot[n]; res != nil {
			return res
		}
		if n.DocumentRoot != nil && n.DocumentRoot != n {
			if res := x.byRoot[n.DocumentRoot]; res != nil {
				return res
			}
		}
	}
	return nil
}

// DocumentOf returns the root of the registered document holding s, or nil.
func (x *ResourceIndex) DocumentOf(s *Schema) *Schema {
	if res := x.ResourceOf(s); res != nil {
		return res.document
	}
	return nil
}

// Resource returns the resource registered under an absolute URI, or nil.
func (x *ResourceIndex) Resource(uri string) *Resource {
	u, err := url.Parse(uri)
	if err != nil {
		return nil
	}
	return x.byURI[resourceKey(u)]
}

// Resolve resolves ref as it is written on the schema node ctx.
//
// The base URI is ctx's; the resource a bare fragment is read in is the one ctx
// is written in. A reference naming another document is looked up by the
// absolute URI it resolves to, and that document is loaded and registered when
// the index does not hold it. Two further readings exist for a relative
// reference, each only where the first reading has nothing to go on:
//
//   - When the base URI in effect is not a file -- the document declares an
//     "$id" such as https://example.com/schemas/x.json, which nothing serves --
//     the reference is also tried as a path next to the file the document was
//     read from. That is where such a reference is read from by a tool working
//     from files, and it is what this repository has always documented.
//   - When the document was registered from nowhere nameable, the loader is
//     handed the reference as written, which is what a caller's own resolver
//     (a FileResolver rooted at their schema directory, a MappingResolver keyed
//     by relative names) was always handed.
//
// Neither applies to a document read from a file whose reference resolves to
// a file: there the absolute URI is the answer, and a miss is a miss.
func (x *ResourceIndex) Resolve(ref string, ctx *Schema) (*Schema, error) {
	from := x.ResourceOf(ctx)
	var base *url.URL
	if ctx != nil {
		base = ctx.BaseURI
	}
	if base == nil && from != nil {
		base = from.Root.BaseURI
	}
	var retrieval *url.URL
	if from != nil {
		if doc := x.DocumentOf(from.Root); doc != nil {
			retrieval = doc.RetrievalURI
		}
	}
	return x.resolve(ref, base, from, retrieval)
}

// ResolveSchema implements SchemaResolver, for a caller that holds only a base
// URI: a bare fragment is read in the resource registered under baseURI.
func (x *ResourceIndex) ResolveSchema(ref string, baseURI *url.URL) (*Schema, error) {
	var from *Resource
	var retrieval *url.URL
	if baseURI != nil {
		from = x.byURI[resourceKey(baseURI)]
		if from != nil {
			if doc := x.DocumentOf(from.Root); doc != nil {
				retrieval = doc.RetrievalURI
			}
		}
	}
	return x.resolve(ref, baseURI, from, retrieval)
}

func (x *ResourceIndex) resolve(ref string, base *url.URL, from *Resource, retrieval *url.URL) (*Schema, error) {
	refURL, err := url.Parse(ref)
	if err != nil {
		return nil, &ReferenceError{Ref: ref, Reason: fmt.Errorf("not a URI reference: %w", err)}
	}
	// Still percent-encoded: FragmentPointer decodes what it is handed, once.
	fragment := refURL.EscapedFragment()
	docRef := withoutFragment(refURL)

	var res *Resource
	var loads []error
	if docRef.String() == "" {
		if from == nil {
			return nil, &ReferenceError{Ref: ref, Reason: ErrUnregisteredContext}
		}
		res = from
	} else {
		res, loads = x.resourceFor(docRef, base, retrieval)
		if res == nil {
			return nil, &ReferenceError{Ref: ref, Reason: fmt.Errorf("no schema resource is known by that URI"), Loads: loads}
		}
	}
	node, err := x.inResource(res, fragment)
	if err != nil {
		return nil, &ReferenceError{Ref: ref, Reason: err, Loads: loads}
	}
	return node, nil
}

// resourceFor finds, or loads, the resource a reference's document part names.
func (x *ResourceIndex) resourceFor(docRef, base, retrieval *url.URL) (*Resource, []error) {
	var loads []error
	referrer := base
	if retrieval != nil && retrieval.Scheme == "file" {
		referrer = retrieval
	}
	try := func(target *url.URL) *Resource {
		if res := x.byURI[resourceKey(target)]; res != nil {
			return res
		}
		if x.loader == nil || target.Scheme == AnonymousDocumentScheme {
			return nil
		}
		doc, err := x.loader.ResolveSchema(resourceKey(target), referrer)
		if err != nil {
			loads = append(loads, err)
			return nil
		}
		res, err := x.registerLoaded(doc, target)
		if err != nil {
			loads = append(loads, err)
			return nil
		}
		return res
	}

	var target *url.URL
	switch {
	case docRef.IsAbs():
		target = docRef
	case base != nil && base.IsAbs() && base.Opaque == "":
		// An opaque base -- a urn: -- has no path for a relative reference to
		// be resolved against; RFC 3986's algorithm would still produce
		// "urn:/other.json", which names nothing anyone wrote.
		target = base.ResolveReference(docRef)
	}
	if target != nil && target.IsAbs() {
		if res := try(target); res != nil {
			return res, loads
		}
	}
	if docRef.IsAbs() {
		return nil, loads
	}
	fileBase := base != nil && base.Scheme == "file"
	if !fileBase && retrieval != nil && retrieval.Scheme == "file" {
		// Next to the file the document was read from.
		if beside := retrieval.ResolveReference(docRef); target == nil || beside.String() != target.String() {
			if res := try(beside); res != nil {
				return res, loads
			}
		}
		return nil, loads
	}
	if !fileBase && (retrieval == nil || retrieval.Scheme == AnonymousDocumentScheme) && x.loader != nil {
		// A document from nowhere nameable: the caller's resolver, handed the
		// reference as written.
		doc, err := x.loader.ResolveSchema(docRef.String(), base)
		if err != nil {
			loads = append(loads, err)
			return nil, loads
		}
		res, err := x.registerLoaded(doc, nil)
		if err != nil {
			loads = append(loads, err)
			return nil, loads
		}
		return res, loads
	}
	return nil, loads
}

// registerLoaded registers a document the loader returned for target and
// returns the resource target names in it.
//
// A loader may hand back a node of a document rather than the document -- a
// resolver asked for a URI naming a subschema's "$id" -- in which case the
// document holding it is registered; and a document the index already holds
// under another URI is that document, with target one more name for it.
func (x *ResourceIndex) registerLoaded(doc *Schema, target *url.URL) (*Resource, error) {
	if doc == nil {
		return nil, fmt.Errorf("the resolver returned no schema")
	}
	if res := x.ResourceOf(doc); res != nil && res.Root == doc {
		if target != nil {
			if err := x.alias(target, res); err != nil {
				return nil, err
			}
		}
		return res, nil
	}
	whole := doc
	if top, _, ok := doc.SourceLocation(); ok && top != nil {
		whole = top
	}
	retrieval := whole.RetrievalURI
	if retrieval == nil && whole == doc {
		retrieval = target
	}
	if err := x.AddDocument(whole, retrieval); err != nil {
		return nil, err
	}
	res := x.byRoot[doc]
	if res == nil {
		res = x.ResourceOf(doc)
	}
	if res == nil {
		return nil, fmt.Errorf("the resolver returned a schema that is not a resource")
	}
	if target != nil && x.byURI[resourceKey(target)] == nil {
		if err := x.alias(target, res); err != nil {
			return nil, err
		}
	}
	if target != nil {
		if named := x.byURI[resourceKey(target)]; named != nil {
			return named, nil
		}
	}
	return res, nil
}

// inResource resolves a fragment, still percent-encoded, within a resource: the
// resource itself for an empty one, a JSON Pointer from the resource's root, or
// a plain-name anchor the resource declares.
func (x *ResourceIndex) inResource(res *Resource, fragment string) (*Schema, error) {
	if fragment == "" {
		return res.Root, nil
	}
	tokens, isPointer, err := FragmentPointer(fragment)
	if err != nil {
		return nil, err
	}
	if isPointer {
		return (&LocalResolver{root: res.Root}).walkPath(res.Root, tokens, "#"+fragment)
	}
	name, _ := DecodeFragment(fragment)
	return res.anchor(name)
}

// anchor returns the node declaring the plain-name fragment name in this
// resource.
func (r *Resource) anchor(name string) (*Schema, error) {
	if r.ambiguous[name] {
		uri := r.CanonicalURI
		if uri == "" {
			uri = "(the document root)"
		}
		return nil, &AmbiguousAnchorError{Resource: uri, Anchor: name}
	}
	if node, ok := r.Anchors[name]; ok {
		return node, nil
	}
	return nil, fmt.Errorf("anchor %q not found", name)
}

// Graph is the resource graph of one registered document: every resource it
// holds, keyed as BuildResourceGraph keys them, read from this index. A
// resource that declares no dialect is given defaultDraft.
func (x *ResourceIndex) Graph(doc *Schema, defaultDraft Draft) *ResourceGraph {
	g := &ResourceGraph{Root: doc, Resources: make(map[string]*Resource)}
	if doc == nil {
		return g
	}
	for _, res := range documentResources(doc, x.draft) {
		// A boolean document is a resource a reference can name, and one with
		// nothing in it to plan validation for: the graph has always left it
		// out, and ResourceCount is emitted into generated code.
		if res.Root.IsBooleanSchema() {
			continue
		}
		if registered := x.byRoot[res.Root]; registered != nil {
			res = registered
		}
		key := res.CanonicalURI
		if key == "" {
			key = "#"
		}
		if _, taken := g.Resources[key]; taken {
			continue
		}
		view := *res
		view.Draft = resourceDraft(res.Root, defaultDraft)
		g.Resources[key] = &view
	}
	return g
}
