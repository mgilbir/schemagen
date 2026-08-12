package schemagen

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// This file resolves the Go type names several documents generated into one Go
// package claim between them.
//
// One package means one name space, and a definition is named after its $defs
// key alone -- so two documents that each declare "$defs/Thing" ask for one Go
// type. When the two definitions agree that is exactly right, and is what
// --shared-types is for. When they differ it cannot be honoured: the name
// registry hands the second document whichever definition was generated first
// and drops its own, so a document's property carries a type its schema never
// described. It generates cleanly and exits 0 -- {"thing":{"note":"hello"}},
// valid per the second document, came back "required property k is missing",
// and a definition typed "string" in one document and "object" in the other
// made a valid document fail to decode at all. Issue #249.
//
// The answer is decided per name, not per document:
//
//   - Definitions that agree keep sharing one type. That is the common case (a
//     definition copied between related schemas) and the reason the mode exists.
//     Agreement is judged on the normalized documents, so two spellings of one
//     definition -- different key order, different whitespace, a keyword the
//     dialect drops -- still share. See shareableNames.
//
//   - Definitions that differ are named after their own document: alpha.json's
//     becomes AlphaThing and beta.json's BetaThing, from the root type name each
//     document already has and --root-name already controls.
//
// Both sides get a prefix, never only the second. Renaming just the loser would
// make the generated API depend on the order the inputs were listed, which is
// the trap issue #228 came out of.
//
// One document can contest a name with itself, and the document prefix says
// nothing there: "$defs/X" and "definitions/X" are two different JSON Pointers
// naming two different schema locations, and a document may write different
// schemas at them. Schema.normalizeNode mirrors the two keywords only when one
// of them is empty, so when both are written both nodes reach the generator,
// which named each after its key alone and let the first claim the name. Issue
// #260. Those are qualified with the keyword that declared them -- DefsX and
// DefinitionsX -- which is the discriminator the document itself wrote.
//
// Each half of the qualifier is added only where it is needed to separate the
// claims: the document root name when more than one document claims the name,
// the keyword when a claim from the same document disagrees with it. So the
// name a definition gets does not change when a single-document run is handed
// --shared-types, and a definition is never moved off a name nothing was about
// to take from it.

// nameClaim is one document's claim on a Go type name.
type nameClaim struct {
	// path is the input path as the caller wrote it: a message naming a file the
	// caller can open is worth more than one naming an $id.
	path string
	// keyword is the container the claim was declared in -- "$defs" or
	// "definitions" -- and is empty for a document's root type. It is both what
	// the message calls the claim and, within one document, what tells two claims
	// on one name apart.
	keyword string
	// defKey is the $defs/definitions key this claim came from, empty for a
	// document's root type.
	defKey string
	node   *schema.Schema
	// final is the name the claim ends up with: the name it asked for, or the
	// qualified one when the claims on that name could not be merged.
	final string
}

// what describes the claim in the schema author's own terms.
func (c nameClaim) what() string {
	if c.defKey == "" {
		return "root type"
	}
	return c.keyword + "/" + c.defKey
}

// keywordPrefix is the Go name part that says which container declared a
// definition, for the one case where that is what tells two claims apart.
func keywordPrefix(keyword string) string {
	if keyword == "definitions" {
		return "Definitions"
	}
	return "Defs"
}

// resolveSharedDefinitionNames decides the Go type name every definition of a
// one-package run is declared under, and reports the ones it had to move.
//
// The returned map is keyed by definition node and holds only the definitions
// whose name changed; it is Config.DefinitionTypeNames. Documents are read in
// the order given, but the answer does not depend on that order -- every claim
// on a contested name is qualified, so the same set of inputs produces the same
// set of type names however it is listed.
func resolveSharedDefinitionNames(paths []string, byPath map[string]*schema.Schema, rootNameOf func(path string, s *schema.Schema) string, warnings io.Writer) map[*schema.Schema]string {
	claims := collectNameClaims(paths, byPath, rootNameOf)
	if len(claims) == 0 {
		return nil
	}

	shareable := shareableNames(claims)

	pinned := make(map[*schema.Schema]string)
	var reports []string
	for _, name := range sortedClaimNames(claims) {
		group := claims[name]
		if len(group) < 2 || shareable[name] {
			continue
		}
		documents := distinctClaimPaths(group)
		moved := false
		for i := range group {
			if group[i].defKey == "" {
				// A root type name is the caller's, set by --root-name or by the
				// document's title, and it is what every other name here is
				// qualified with. Moving it would rename the API the caller
				// asked for in order to make room for a definition.
				continue
			}
			qualified := name
			// The keyword first, so the document prefix stays the outermost part
			// of the name: AlphaDefsX reads as alpha.json's $defs/X, which is the
			// order the two questions are asked in.
			if keywordSeparatesClaim(group, i) {
				qualified = keywordPrefix(group[i].keyword) + qualified
			}
			// The qualified name is built from the contested name and not from
			// the $defs key, so a document that spells the same definition twice
			// and means it both times ("Thing" under $defs and again under
			// definitions, identical) pins both nodes to one name rather than to
			// two it would have to tell apart again.
			if len(documents) > 1 {
				qualified = rootNameOf(group[i].path, byPath[group[i].path]) + qualified
			}
			if qualified == name {
				// Nothing here tells this claim from the others -- it is a
				// document's only claim on the name, under the only keyword that
				// declares it. Pinning it to the name it already has would give
				// two nodes one pin, which the generator refuses as a collision;
				// leaving it alone keeps the behaviour it had. See the note on
				// the claims no qualifier reaches, below.
				continue
			}
			group[i].final = qualified
			pinned[group[i].node] = qualified
			moved = true
		}
		if !moved {
			// A contested name no qualifier could act on. Two $defs keys of one
			// document that fold onto one Go name ("foo-bar" and "foo_bar") are
			// the shape that reaches here: same document, same keyword, so
			// neither half of the qualifier separates them, and they still merge.
			// That is a third collision with a third answer -- there is no
			// discriminator in the document to name them by -- and it is left as
			// it was rather than answered here by guesswork.
			continue
		}
		reports = append(reports, describeNameSplit(name, group, len(documents)))
	}

	if warnings != nil {
		for _, r := range reports {
			fmt.Fprint(warnings, r)
		}
	}
	return pinned
}

// collectNameClaims lists, per Go type name, every claim the documents make on
// it: each document's root type, and each of its $defs and definitions entries.
func collectNameClaims(paths []string, byPath map[string]*schema.Schema, rootNameOf func(string, *schema.Schema) string) map[string][]nameClaim {
	claims := make(map[string][]nameClaim)
	seenPath := make(map[string]bool, len(paths))

	for _, path := range paths {
		s := byPath[path]
		// One input listed twice is one document, not two. Its claims must not
		// be counted against themselves.
		if s == nil || seenPath[path] {
			continue
		}
		seenPath[path] = true

		add := func(name, keyword, defKey string, node *schema.Schema) {
			for _, existing := range claims[name] {
				if existing.node == node {
					return // the same node reached twice ($defs mirrored into definitions)
				}
			}
			claims[name] = append(claims[name], nameClaim{path: path, keyword: keyword, defKey: defKey, node: node, final: name})
		}

		add(rootNameOf(path, s), "", "", s)
		// $defs first, so the mirror Schema.normalizeNode writes when a document
		// declares only one of the two keywords is described by the keyword the
		// dedup above kept rather than by the copy -- and so a document that
		// declares both is read in the order the generator reads them.
		for _, container := range []struct {
			keyword string
			m       map[string]*schema.Schema
		}{{"$defs", s.Defs}, {"definitions", s.Definitions}} {
			for _, key := range sortedSchemaKeys(container.m) {
				if def := container.m[key]; def != nil {
					add(generator.SchemaNameToGoName(key), container.keyword, key, def)
				}
			}
		}
	}
	return claims
}

// claimsBothDefinitionKeywords reports whether the group holds a definition
// under each of the two keywords -- the shape issue #260 is about, as against a
// definition contesting the name with its own document's root type.
func claimsBothDefinitionKeywords(group []nameClaim) bool {
	var defs, definitions bool
	for _, c := range group {
		switch c.keyword {
		case "$defs":
			defs = true
		case "definitions":
			definitions = true
		}
	}
	return defs && definitions
}

// distinctClaimPaths counts the input documents a group of claims comes from.
// One document contesting a name with itself is a different question from two
// documents contesting it, and takes a different qualifier and a different
// message.
func distinctClaimPaths(group []nameClaim) []string {
	seen := make(map[string]bool, len(group))
	paths := make([]string, 0, len(group))
	for _, c := range group {
		if seen[c.path] {
			continue
		}
		seen[c.path] = true
		paths = append(paths, c.path)
	}
	return paths
}

// keywordSeparatesClaim reports whether the claim at index i needs the keyword
// that declared it to tell it from the rest of its group.
//
// It does when its own document makes another claim on the name under a
// different keyword and the two do not describe the same schema. Agreement is
// judged by definitionCanonicalForm, the same comparison shareableNames uses
// across documents: two spellings of one definition -- different key order,
// different whitespace, a keyword the dialect drops -- are one definition here
// too, and qualifying them would split a type the document meant to have once.
//
// Deliberately not the transitive verdict shareableNames computes. That answers
// "may these definitions be one type", which a *reference* the two make into a
// name split elsewhere can turn to no; here the two claims make the same
// reference, so whatever it resolves to they resolve to together.
func keywordSeparatesClaim(group []nameClaim, i int) bool {
	form, _, ok := definitionCanonicalForm(group[i].node)
	for j := range group {
		if j == i || group[j].path != group[i].path || group[j].keyword == group[i].keyword {
			continue
		}
		otherForm, _, otherOK := definitionCanonicalForm(group[j].node)
		if !ok || !otherOK || form != otherForm {
			return true
		}
	}
	return false
}

// shareableNames reports the contested names whose claims may stay one Go type.
//
// A group is shareable when every claim carries the same definition, and the
// test for that is deliberately conservative: sharing is the answer that cannot
// be taken back, since it silently drops one of the definitions, while
// qualifying a name that could have been shared costs a duplicate type and
// nothing else. So a group only shares when
//
//   - every claim's canonical form is identical (definitionCanonicalForm), and
//   - every reference a claim makes is a plain "#/$defs/<name>" or
//     "#/definitions/<name>" pointer into its own document, and
//   - the name each of those pointers reaches is itself shareable.
//
// The last condition is what makes the answer transitive, and it is not
// optional. Two documents can hold a byte-identical {"$ref":"#/$defs/Other"}
// while their $defs/Other differ: the definitions agree about everything they
// say and still describe different types. Demoting until nothing changes is the
// fixpoint of that rule, and it only ever demotes, so it terminates.
func shareableNames(claims map[string][]nameClaim) map[string]bool {
	shareable := make(map[string]bool, len(claims))
	refsOf := make(map[string][]string, len(claims))

	for name, group := range claims {
		if len(group) < 2 {
			continue
		}
		form, refs, ok := definitionCanonicalForm(group[0].node)
		if !ok {
			continue
		}
		agreed := true
		for _, c := range group[1:] {
			otherForm, otherRefs, otherOK := definitionCanonicalForm(c.node)
			if !otherOK || otherForm != form {
				agreed = false
				break
			}
			refs = append(refs, otherRefs...)
		}
		if !agreed {
			continue
		}
		shareable[name] = true
		refsOf[name] = refs
	}

	for changed := true; changed; {
		changed = false
		for name := range shareable {
			for _, target := range refsOf[name] {
				if shareable[target] {
					continue
				}
				delete(shareable, name)
				changed = true
				break
			}
		}
	}
	return shareable
}

// definitionCanonicalForm renders a definition in a form two documents can be
// compared by, and lists the Go names of the definitions it references.
//
// ok is false when the definition cannot be compared this way at all: it
// references something other than a definition of its own document, and what
// that reference reaches is not a claim this file tracks, so nothing here can
// establish that two documents' versions of it are the same. The caller must
// then treat the definition as agreeing with nothing.
//
// The form is built from the *normalized* schema rather than from the bytes on
// disk, which is what makes key order, whitespace and a keyword the document's
// dialect does not define stop mattering: json.Marshal writes struct fields in
// declaration order and map keys in sorted order, so one shape has one
// spelling. What Schema.MarshalJSON leaves out is added back per node, in the
// order WalkSchema visits them: the keywords held outside the tagged fields
// (draft 3's schema-valued "type" entries, {"const":null}, and the vendor
// keywords in Extensions, which the generator reads the presence of) would
// otherwise let two different definitions render identically.
func definitionCanonicalForm(s *schema.Schema) (form string, refs []string, ok bool) {
	if s == nil {
		return "", nil, false
	}
	body, err := json.Marshal(s)
	if err != nil {
		return "", nil, false
	}

	var b strings.Builder
	b.Write(body)
	comparable := true
	generator.WalkSchema(s, func(n *schema.Schema) {
		if !comparable {
			return
		}
		for _, ref := range []string{n.Ref, n.RecursiveRef, n.DynamicRef} {
			if ref == "" {
				continue
			}
			target, local := localDefinitionRef(ref)
			if !local {
				comparable = false
				return
			}
			refs = append(refs, target)
		}
		b.WriteByte('\x1f')
		if n.ConstIsNull {
			b.WriteString("const:null")
		}
		if len(n.TypeSchemas) > 0 {
			if raw, err := json.Marshal(n.TypeSchemas); err == nil {
				b.Write(raw)
			} else {
				comparable = false
				return
			}
		}
		for _, key := range sortedExtensionKeys(n.Extensions) {
			b.WriteString(key)
			b.WriteByte('=')
			canon, err := canonicalJSON(n.Extensions[key])
			if err != nil {
				comparable = false
				return
			}
			b.WriteString(canon)
		}
	})
	if !comparable {
		return "", nil, false
	}
	return b.String(), refs, true
}

// localDefinitionRef reports the Go type name a "#/$defs/<name>" or
// "#/definitions/<name>" pointer reaches, and whether the ref is one.
//
// Only that exact shape counts. A pointer that continues past the definition
// ("#/$defs/A/properties/b"), a pointer into any other part of the document, an
// anchor and a ref naming another document all answer false: what they reach is
// not a claim this file tracks, so nothing here can say whether two documents'
// versions of it agree.
func localDefinitionRef(ref string) (string, bool) {
	for _, prefix := range []string{"#/$defs/", "#/definitions/"} {
		rest, found := strings.CutPrefix(ref, prefix)
		if !found || rest == "" || strings.Contains(rest, "/") {
			continue
		}
		// RFC 6901 escaping, then percent-decoding, exactly as the generator's
		// own ref-to-name conversion does -- the two must agree on which
		// definition a pointer names.
		rest = strings.ReplaceAll(rest, "~1", "/")
		rest = strings.ReplaceAll(rest, "~0", "~")
		return generator.SchemaNameToGoName(rest), true
	}
	return "", false
}

// canonicalJSON re-encodes arbitrary JSON so that two spellings of one value
// compare equal: object keys sorted, insignificant whitespace gone. Numbers keep
// the literal the document wrote (json.Number), since reading them as float64
// would make 9223372036854775807 and 9223372036854775806 the same value.
func canonicalJSON(raw json.RawMessage) (string, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	out, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// describeNameSplit phrases the diagnostic for one contested name: what was
// asked for, what schemagen did with it, why, and what the caller can change.
//
// Two messages, because the two shapes have nothing in common but the symptom.
// Several documents contesting a name is a property of the set the caller
// assembled, and the remedy is about documents; one document contesting a name
// with itself is a property of that document, and telling its author that "1
// input documents" disagree names neither the problem nor anything they can act
// on.
func describeNameSplit(name string, group []nameClaim, documents int) string {
	// One line per claim, deduplicated: a document that spells one definition
	// under both "definitions" and "$defs" and means it both times makes two
	// claims that say and become the same thing, and reporting it twice would
	// read as two definitions.
	seen := make(map[string]bool, len(group))
	lines := make([]string, 0, len(group))
	for _, c := range group {
		line := fmt.Sprintf("  %s %s becomes %s", c.path, c.what(), c.final)
		if c.final == name {
			line = fmt.Sprintf("  %s %s keeps %s", c.path, c.what(), name)
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		lines = append(lines, line)
	}
	sort.Strings(lines)

	if documents == 1 {
		// The remedy depends on which of its own claims the document put in
		// contention, and the two shapes have different ones. Naming a remedy
		// that does not apply is worse than naming none: --root-name moves a root
		// type and every position named after it, and does nothing at all to a
		// definition spelled twice.
		var tail strings.Builder
		tail.WriteString("Each definition is qualified instead with the keyword that declared it, which is the only thing in the document that tells them apart. ")
		if claimsBothDefinitionKeywords(group) {
			tail.WriteString("$defs and definitions name the same container in every draft that defines both, so if these were meant to be one definition make them identical or delete one; otherwise rename one of them in the schema to choose the Go names yourself.")
		} else {
			tail.WriteString("The document's root type keeps the name -- it is the one the caller asked for, by the document's title or by --root-name. " +
				"Rename the definition in the schema, or give the document another root name with --root-name, to choose the Go names yourself.")
		}
		return fmt.Sprintf("warning: %s declares the Go type name %s in %d places, and those declarations do not describe the same type, so they cannot be one:\n%s\n"+
			"one Go package holds one type per name, so declaring them all as %s would have given every $ref whichever was generated first and discarded the rest -- a position typed by a schema the document never wrote there. %s\n",
			group[0].path, name, len(lines), strings.Join(lines, "\n"), name, tail.String())
	}
	return fmt.Sprintf("warning: %d input documents claim the Go type name %s, and those claims do not describe the same type, so they cannot be one:\n%s\n"+
		"one package holds one type per name, so sharing it would have given every document whichever schema was generated first and discarded the rest. "+
		"Each definition is qualified with its own document's root type name -- all of them, not only the later ones, so the generated names do not depend on the order the inputs were listed. "+
		"A document's own root type keeps the name it was given; --root-name sets both. "+
		"Make the definitions identical if they were meant to be one type, or rename one of them in the schema to choose the Go names yourself.\n",
		documents, name, strings.Join(lines, "\n"))
}

// explainPinnedNameCollision turns the generator's refusal of a qualified name
// into the caller's terms: which definition schemagen was moving, where the name
// it chose went instead, and the three ways out.
func explainPinnedNameCollision(schemaPath string, collision *generator.PinnedNameCollisionError, pinned map[*schema.Schema]string, byPath map[string]*schema.Schema, paths []string) error {
	owner := make(map[string]string, len(pinned))
	for _, path := range paths {
		s := byPath[path]
		if s == nil {
			continue
		}
		for _, container := range []struct {
			keyword string
			m       map[string]*schema.Schema
		}{{"$defs", s.Defs}, {"definitions", s.Definitions}} {
			for _, key := range sortedSchemaKeys(container.m) {
				if name, ok := pinned[container.m[key]]; ok {
					if _, seen := owner[name]; !seen {
						owner[name] = fmt.Sprintf("%s/%s in %s", container.keyword, key, path)
					}
				}
			}
		}
	}

	lines := make([]string, 0, len(collision.Names))
	for _, name := range collision.Names {
		if from, ok := owner[name]; ok {
			lines = append(lines, fmt.Sprintf("  %s was renamed to %s, which another schema in this package already declares", from, name))
			continue
		}
		lines = append(lines, fmt.Sprintf("  %s is already declared by another schema in this package", name))
	}
	sort.Strings(lines)
	return fmt.Errorf("generating IR for %s: definitions of the same name in different documents could not be told apart:\n%s\n"+
		"schemagen renames such definitions after their own document's root type, and here that name is taken -- by another definition, or by a type generated for a position inside the document, which is named the same way (a property \"thing\" under a root named Alpha is also AlphaThing, and --root-name moves both of them together). Generation stopped at this document, so no file was written for it. "+
		"Rename the definition, or whatever else holds that name, in the schema; give the document a root name with --root-name that separates them; or generate these documents into separate packages with --schema-package",
		schemaPath, strings.Join(lines, "\n"))
}

func sortedClaimNames(claims map[string][]nameClaim) []string {
	names := make([]string, 0, len(claims))
	for name := range claims {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedSchemaKeys(m map[string]*schema.Schema) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedExtensionKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
