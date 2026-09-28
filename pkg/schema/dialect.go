package schema

import "strings"

// This file is the one statement of which dialect a schema node is read under.
//
// Three layers ask the question: normalization, which drops the keywords a
// node's dialect does not define and rewrites the legacy spellings; the
// resource index, which decides from the dialect whether an $id beside a $ref
// starts a resource (drafts 3 to 7 say the $ref replaces it); and the
// generator, which decides from it what every keyword means. They used to
// answer from three places, and under a dialect chosen from outside the
// document -- the --draft flag, Config.Draft -- they did not agree: a document
// normalized under the draft its own $schema states and then generated under
// Config.Draft had its resources computed under the first and its keywords read
// under the second, so an $id beside a $ref named no resource while the
// minLength beside the same $ref was enforced. Neither dialect reads a document
// that way.
//
// The rule:
//
//   - A dialect chosen from outside is the caller's statement about the
//     documents they handed over, and it wins for them: the root's own $schema,
//     and a $schema written deeper in the same resource, are overridden. The
//     generator reports each $schema it overrides (see
//     generator.Generator.DialectOverrides), because an override that
//     contradicts the document is either what the caller meant or a mistake,
//     and only the caller can say which.
//   - An embedded resource -- a node that declares its own $id beside its own
//     $schema -- keeps its own dialect, and so does a document a reference
//     reaches that states its $schema. Cross-draft references exist to mean
//     the other draft, and --draft is not a statement about documents the
//     caller did not list.
//   - With no dialect chosen from outside, each node is read under the nearest
//     $schema at or above it, as it always has been.

// declaresOwnResource reports whether n states an identifier that starts a
// resource of its own -- one that is not a plain-name fragment.
func declaresOwnResource(n *Schema) bool {
	id := n.ID
	if id == "" {
		id = n.LegacyID
	}
	return id != "" && !strings.HasPrefix(id, "#")
}

// ownDialect is the dialect n switches itself and its subtree to, or
// DraftUnknown where it switches nothing. given says the dialect around n was
// chosen from outside the document, where only an embedded resource switches.
func ownDialect(n *Schema, given bool) Draft {
	d := DetectDraft(n)
	if d == DraftUnknown || (given && !declaresOwnResource(n)) {
		return DraftUnknown
	}
	return d
}

// writtenUnder is the node the document wrote n inside, or nil for a
// document's root and for a node no document wrote.
func (s *Schema) writtenUnder() *Schema {
	if !s.src.set {
		return nil
	}
	return s.src.parent
}

// isDocumentRoot reports whether s is the root of a document that was read.
func (s *Schema) isDocumentRoot() bool {
	return s.src.set && s.src.parent == nil && s.src.doc == s
}

// dialectOf answers the rule above for node s: override is the dialect chosen
// from outside (DraftUnknown when none was), and input reports whether a node
// is the root of a document the caller handed over rather than one a
// reference reached.
func dialectOf(s *Schema, override Draft, input func(*Schema) bool) Draft {
	if s == nil {
		return override
	}
	given := override != DraftUnknown
	for n := s; n != nil; n = n.writtenUnder() {
		if given && n.isDocumentRoot() && input != nil && input(n) {
			return override
		}
		if d := ownDialect(n, given); d != DraftUnknown {
			return d
		}
		if n.isDocumentRoot() {
			// A document a reference reached, which states its own dialect.
			if d := DetectDraft(n); d != DraftUnknown {
				return d
			}
			break
		}
	}
	if !s.src.set && s.DocumentRoot != nil && s.DocumentRoot != s {
		// A node no document wrote -- one the generator built, or one built
		// through the Go API -- that says nothing itself: it is read where its
		// resource is.
		return dialectOf(s.DocumentRoot, override, input)
	}
	if given {
		return override
	}
	return s.DetectedDraft
}

// DialectOf answers which dialect s is read under, by the rule this file
// states, for the documents this index holds: the dialect WithIndexDraft
// chose for the documents registered with AddDocument, where one was chosen.
// DraftUnknown means nothing decides it -- no $schema anywhere above s and no
// dialect chosen from outside -- and the caller reads such a node under its
// own default.
func (x *ResourceIndex) DialectOf(s *Schema) Draft {
	return dialectOf(s, x.draft, x.isInput)
}

// Draft is the dialect WithIndexDraft chose for the documents this index is
// handed, or DraftUnknown.
func (x *ResourceIndex) Draft() Draft { return x.draft }

// isInput reports whether doc is the root of a document registered with
// AddDocument, as opposed to one a reference made the index load.
func (x *ResourceIndex) isInput(doc *Schema) bool {
	return x.inputs[doc]
}

// DialectOf answers which dialect s is read under when override is the dialect
// chosen from outside for the document s belongs to (DraftUnknown for none).
// It is ResourceIndex.DialectOf for a node no index holds.
func DialectOf(s *Schema, override Draft) Draft {
	root := s
	for n := s; n != nil; n = n.writtenUnder() {
		root = n
	}
	return dialectOf(s, override, func(n *Schema) bool { return n == root })
}
