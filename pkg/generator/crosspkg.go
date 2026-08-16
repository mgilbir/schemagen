package generator

import (
	"path"
	"strings"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// CrossPackageRegistry coordinates a generation run that spans several Go
// packages. Every generator of the run records the types it emits; when a
// $ref resolves into a document owned by another package, the referencing
// generator emits a qualified name and an import instead of materializing a
// local copy.
//
// The registry keys types by schema node identity, so all generators of a
// run must resolve documents through shared loaded instances (one document
// loaded once, e.g. via a common MappingResolver).
type CrossPackageRegistry struct {
	// DocPackages maps a document $id (without fragment) to the Go import
	// path of the package generated from it.
	DocPackages map[string]string

	types map[*schema.Schema]qualifiedType

	// nodePackages records the owning package of every node of every registered
	// document, so ownership does not depend on a node declaring an $id or on
	// DocumentRoot having been populated by this generator.
	nodePackages map[*schema.Schema]string

	// documentsWithTypes records the documents at least one Go type was
	// registered from. It is what tells apart the two causes of a cross-package
	// miss: a document its owning package never generated in this run, and a
	// document it did generate that gives *this node* no declaration of its own.
	// The two need opposite advice, and reporting the first for both is issue
	// #310 -- a run whose own output held out/tpkg/t.go on disk was told t.json
	// had not been generated.
	documentsWithTypes map[string]bool
}

type qualifiedType struct {
	ImportPath string
	Name       string

	// Shape is filled in once the owning package's Generate call finishes, for
	// use by referencing packages.
	Shape     typeShape
	infoKnown bool
}

// typeShape is everything a referencing package needs to know about a type it
// holds nothing of but the name.
//
// Every field is an answer the *owning* generator worked out by running its own
// predicate over its own declaration, and none of them can be worked out again
// on the other side. A referencing package's type table does not contain the
// foreign declaration, so asking it answers "no" for every question; and where
// that package happens to declare a type of the same name, it answers about the
// wrong type entirely, which is worse -- a Go type whose shape decides whether
// an absent property is omitted or invented would be chosen from a namesake.
// That is issue #296: `tpkg.OneOf`, `tpkg.Not` and `tpkg.Arr` were all judged by
// a local lookup that had never heard of them, came back as ordinary structs,
// and were tagged ",omitempty" -- which never omits a struct, so a property the
// document did not carry was written out as null or {} and the type then refused
// to read its own output back.
//
// isZeroLossyNamedType was the one predicate that had been given this treatment,
// and the rest of the family is here for the same reason. Adding a question a
// referencing package asks of a foreign type means adding a field here; see
// TestEveryTypeShapeFieldIsPublished, which reads the publication in Generate
// and fails until it is.
type typeShape struct {
	// ZeroLiteral is the Go literal for the type's zero value, or "" where it
	// has none (a struct). Validation guards compare against it.
	ZeroLiteral string
	// Validatable says the type carries a Validate() method to dispatch to.
	Validatable bool
	// ZeroLossy says an optional field of this type needs a pointer to tell
	// absent from a present zero. Published rather than left to the referencing
	// package to infer from ZeroLiteral, which cannot see a type whose zero has
	// no literal at all -- an alias over time.Time, for one.
	ZeroLossy bool
	// Struct says the type was emitted as a Go struct, which omitempty never
	// omits: an optional property of one is pointer-wrapped instead.
	Struct bool
	// Collection says the type is, or is an alias over, a slice or a map. Its
	// nil is what an absent property leaves and an empty [] or {} is not, so an
	// optional field of one takes ",omitzero" rather than ",omitempty".
	Collection bool
	// Interface says the type is, or is an alias over, `any`. Like a pointer and
	// a collection its nil means absent -- and it cannot carry methods.
	Interface bool
	// RawWrapper says the type is one of the wrappers that keep the value as raw
	// JSON and judge it afterwards. It is a struct with a custom MarshalJSON, so
	// omitempty never drops it; it carries IsZero, so ",omitzero" drops exactly
	// the absent case.
	RawWrapper bool
	// AliasDropsMethods says `type X T` would leave X without the methods T
	// carries, so a schema whose whole content is a $ref to this type has to be
	// generated from the schema again rather than aliased to it.
	AliasDropsMethods bool
	// Unmarshaler and Marshaler say the type declares an UnmarshalJSON or a
	// MarshalJSON of its own. A defined type over it inherits neither, so an
	// alias that does not delegate reaches JSON through the underlying
	// representation instead -- which for a struct with no exported field is
	// "refuse every document that is not an object", for a schema that accepts
	// them. See populateAliasDelegates, which asks exactly these two questions
	// of a local target through its own tables.
	Unmarshaler bool
	Marshaler   bool
}

// noteTypeInfo records the owning generator's shape for a type previously
// registered via RecordType.
func (r *CrossPackageRegistry) noteTypeInfo(s *schema.Schema, shape typeShape) {
	if r == nil || s == nil {
		return
	}
	qt, ok := r.types[s]
	if !ok || qt.infoKnown {
		return
	}
	qt.Shape = shape
	qt.infoKnown = true
	r.types[s] = qt
}

// NewCrossPackageRegistry creates a registry over the given document-$id →
// import-path assignment.
func NewCrossPackageRegistry(docPackages map[string]string) *CrossPackageRegistry {
	return &CrossPackageRegistry{
		DocPackages:        docPackages,
		types:              make(map[*schema.Schema]qualifiedType),
		nodePackages:       make(map[*schema.Schema]string),
		documentsWithTypes: make(map[string]bool),
	}
}

// RecordType registers that s was generated as typeName in the package with
// the given import path.
//
// Only the package that owns s's document may claim it. Without that check a
// package that materialized a local copy of a foreign type — because the owning
// package had not been generated yet — would claim the node, and every later
// package referencing it would import the copy's package instead of the owner's.
func (r *CrossPackageRegistry) RecordType(s *schema.Schema, importPath, typeName string) {
	if r == nil || s == nil || importPath == "" {
		return
	}
	if owner := r.packageFor(s); owner != "" && owner != importPath {
		return
	}
	if r.types == nil {
		r.types = make(map[*schema.Schema]qualifiedType)
	}
	if _, ok := r.types[s]; !ok {
		r.types[s] = qualifiedType{ImportPath: importPath, Name: typeName}
	}
	if r.documentsWithTypes == nil {
		r.documentsWithTypes = make(map[string]bool)
	}
	r.documentsWithTypes[documentIdentityOf(s)] = true
}

// documentWasGenerated reports whether any Go type was registered from the
// document a miss landed in, which is what says whether the document or only
// the node is missing. See crossPackageMiss.DocumentGenerated.
func (r *CrossPackageRegistry) documentWasGenerated(document string) bool {
	return r != nil && r.documentsWithTypes[document]
}

// forgetType removes s's registration, for a type that was materialized and
// then withdrawn from the file. Leaving it would let another package of the same
// run import a name that is not declared anywhere.
func (r *CrossPackageRegistry) forgetType(s *schema.Schema) {
	if r == nil || s == nil {
		return
	}
	delete(r.types, s)
}

// lookup returns the package and type name s was generated as, if any.
func (r *CrossPackageRegistry) lookup(s *schema.Schema) (qualifiedType, bool) {
	if r == nil {
		return qualifiedType{}, false
	}
	qt, ok := r.types[s]
	return qt, ok
}

// packageFor returns the import path assigned to the document owning s.
//
// Node identity registered via RegisterDocument is authoritative, because a
// subschema usually declares no $id and its DocumentRoot is only populated by
// the generator that walked it. The $id lookups are the fallback for documents
// the caller mapped but did not register.
func (r *CrossPackageRegistry) packageFor(s *schema.Schema) string {
	if r == nil || s == nil {
		return ""
	}
	if pkg, ok := r.nodePackages[s]; ok {
		return pkg
	}
	for _, node := range []*schema.Schema{s, s.DocumentRoot} {
		if node == nil {
			continue
		}
		for _, id := range documentIdentities(node) {
			if pkg, ok := r.DocPackages[id]; ok {
				return pkg
			}
		}
	}
	return ""
}

// RegisterDocument records that every node of doc belongs to importPath.
//
// Ownership cannot be recovered from a node on its own: most subschemas declare
// no $id, and DocumentRoot is populated per generator, so a node reached from
// another package's document has none set. Recording node identity — the same
// currency the type registry uses — makes ownership independent of both. Nested
// $ids are indexed as well, so a $ref naming such a resource directly also
// resolves to the owning package.
func (r *CrossPackageRegistry) RegisterDocument(doc *schema.Schema, importPath string) {
	if r == nil || doc == nil || importPath == "" {
		return
	}
	if r.DocPackages == nil {
		r.DocPackages = make(map[string]string)
	}
	if r.nodePackages == nil {
		r.nodePackages = make(map[*schema.Schema]string)
	}
	WalkSchema(doc, func(node *schema.Schema) {
		if _, taken := r.nodePackages[node]; !taken {
			r.nodePackages[node] = importPath
		}
		if node.ID == "" && node.LegacyID == "" {
			return
		}
		for _, id := range documentIdentities(node) {
			if id == "" {
				continue
			}
			if _, taken := r.DocPackages[id]; !taken {
				r.DocPackages[id] = importPath
			}
		}
	})
}

// documentIdentities lists the URIs a schema node may be known by.
func documentIdentities(s *schema.Schema) []string {
	var ids []string
	for _, id := range []string{s.ID, s.LegacyID} {
		if id != "" {
			ids = append(ids, id, strings.TrimSuffix(id, "#"))
		}
	}
	if s.BaseURI != nil {
		base := s.BaseURI.String()
		ids = append(ids, base, strings.TrimSuffix(base, "#"))
	}
	return ids
}

// PackageNameForImportPath derives a Go package name from an import path
// (its last segment).
func PackageNameForImportPath(importPath string) string {
	return path.Base(importPath)
}
