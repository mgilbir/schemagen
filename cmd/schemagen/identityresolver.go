package schemagen

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// canonicalInstanceResolver delegates to an inner resolver and then substitutes
// the already-loaded instance of any document the run owns.
//
// A multi-schema run keys generated types by schema-node identity, so every
// generator must see one instance per document. Enumerating the spellings a $ref
// might use is not enough: a ref resolves against the base URI in effect, so it
// can arrive at a URI that identifies an input document without matching the key
// the mapping resolver was built with, and the file or HTTP resolver then loads a
// second copy. Nodes of that copy are unknown to the registry, so the reference
// would silently materialize a duplicate type instead of importing it.
//
// Substituting on the way out closes the class rather than enumerating it: the
// check is on the identity of the document that came back, not on how it was
// asked for.
type canonicalInstanceResolver struct {
	inner schema.SchemaResolver
	// byID maps a document $id (with and without a trailing "#") to the
	// instance the run loaded for it.
	byID map[string]*schema.Schema
}

func newCanonicalInstanceResolver(inner schema.SchemaResolver, docs map[string]*schema.Schema) *canonicalInstanceResolver {
	byID := make(map[string]*schema.Schema, len(docs)*2)
	for id, s := range docs {
		if id == "" || s == nil {
			continue
		}
		byID[id] = s
		byID[strings.TrimSuffix(id, "#")] = s
	}
	return &canonicalInstanceResolver{inner: inner, byID: byID}
}

func (r *canonicalInstanceResolver) ResolveSchema(ref string, baseURI *url.URL) (*schema.Schema, error) {
	// Resolve the document part on its own first. A resolver asked for a ref
	// with a fragment returns the subschema, and that node carries no identity
	// of its own — no $id and, from a freshly-loaded copy, no DocumentRoot — so
	// the document it came from is only discoverable by asking for it directly.
	if docPart, fragment := splitRef(ref); docPart != "" {
		if known := r.knownDocumentFor(docPart, baseURI); known != nil {
			if fragment == "" {
				return known, nil
			}
			if sub, ferr := schema.NewLocalResolver(known).Resolve("#" + fragment); ferr == nil && sub != nil {
				return sub, nil
			}
		}
	}

	resolved, err := r.inner.ResolveSchema(ref, baseURI)
	if err != nil || resolved == nil {
		return resolved, err
	}

	// The inner resolver may return a whole document or, for a ref carrying a
	// fragment, a subschema of a freshly-loaded copy. In the latter case the
	// copy's root holds the $id, so the enclosing document is consulted too and
	// the fragment is re-resolved inside the instance the run already has —
	// returning the root instead would change what the $ref means.
	_, fragment := splitRef(ref)
	for _, candidate := range []*schema.Schema{resolved, resolved.DocumentRoot} {
		if candidate == nil {
			continue
		}
		for _, id := range documentIDsOf(candidate) {
			known, ok := r.byID[id]
			if !ok || known == candidate {
				continue
			}
			if fragment == "" {
				return known, nil
			}
			// Re-resolve the same fragment against the canonical instance. If it
			// does not resolve there, fall through rather than silently
			// returning a node from a different copy.
			if sub, ferr := schema.NewLocalResolver(known).Resolve("#" + fragment); ferr == nil && sub != nil {
				return sub, nil
			}
		}
	}
	return resolved, nil
}

// splitRef separates a $ref into its document part and its fragment (without
// the leading "#").
func splitRef(ref string) (doc, fragment string) {
	if i := strings.Index(ref, "#"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}

// documentIDsOf returns the declared identities of a document root.
func documentIDsOf(s *schema.Schema) []string {
	if s == nil {
		return nil
	}
	var ids []string
	for _, id := range []string{s.ID, s.LegacyID} {
		if id != "" {
			ids = append(ids, id, strings.TrimSuffix(id, "#"))
		}
	}
	return ids
}

// inputDirs returns the distinct directories holding the run's input schemas,
// in first-seen order. A file resolver rooted only at the first input's
// directory confines reads to it, so a sibling-relative $ref inside any other
// input cannot resolve.
func inputDirs(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	var dirs []string
	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		dir := filepath.Dir(abs)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}
	return dirs
}

// knownDocumentFor resolves a ref's document part and returns the run's own
// instance of it, if that document is one of the run's inputs.
func (r *canonicalInstanceResolver) knownDocumentFor(docPart string, baseURI *url.URL) *schema.Schema {
	doc, err := r.inner.ResolveSchema(docPart, baseURI)
	if err != nil || doc == nil {
		return nil
	}
	for _, id := range documentIDsOf(doc) {
		if known, ok := r.byID[id]; ok {
			return known
		}
	}
	return nil
}
