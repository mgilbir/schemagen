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
}

type qualifiedType struct {
	ImportPath string
	Name       string

	// Filled in once the owning package's Generate call finishes, for use by
	// referencing packages (validation guards need the zero literal, and only
	// the owning generator knows the underlying type).
	ZeroLiteral string
	Validatable bool
	infoKnown   bool
}

// noteTypeInfo records validation info for a type previously registered via
// RecordType.
func (r *CrossPackageRegistry) noteTypeInfo(s *schema.Schema, zeroLiteral string, validatable bool) {
	if r == nil || s == nil {
		return
	}
	qt, ok := r.types[s]
	if !ok || qt.infoKnown {
		return
	}
	qt.ZeroLiteral = zeroLiteral
	qt.Validatable = validatable
	qt.infoKnown = true
	r.types[s] = qt
}

// NewCrossPackageRegistry creates a registry over the given document-$id →
// import-path assignment.
func NewCrossPackageRegistry(docPackages map[string]string) *CrossPackageRegistry {
	return &CrossPackageRegistry{
		DocPackages:  docPackages,
		types:        make(map[*schema.Schema]qualifiedType),
		nodePackages: make(map[*schema.Schema]string),
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
