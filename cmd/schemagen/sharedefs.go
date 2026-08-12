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

// nameClaim is one document's claim on a Go type name.
type nameClaim struct {
	// path is the input path as the caller wrote it: a message naming a file the
	// caller can open is worth more than one naming an $id.
	path string
	// defKey is the $defs/definitions key this claim came from, empty for a
	// document's root type.
	defKey string
	node   *schema.Schema
	// final is the name the claim ends up with: the name it asked for, or the
	// document-qualified one when the claims on that name could not be merged.
	final string
}

// what describes the claim in the schema author's own terms.
func (c nameClaim) what() string {
	if c.defKey == "" {
		return "root type"
	}
	return "$defs/" + c.defKey
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
		for i := range group {
			if group[i].defKey == "" {
				// A root type name is the caller's, set by --root-name or by the
				// document's title, and it is what every other name here is
				// qualified with. Moving it would rename the API the caller
				// asked for in order to make room for a definition.
				continue
			}
			// The qualified name is built from the contested name and not from
			// the $defs key, so a document that spells the same definition twice
			// ("Thing" under $defs and again under definitions) pins both nodes
			// to one name rather than to two it would have to tell apart again.
			group[i].final = rootNameOf(group[i].path, byPath[group[i].path]) + name
			pinned[group[i].node] = group[i].final
		}
		reports = append(reports, describeNameSplit(name, group))
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

		add := func(name, defKey string, node *schema.Schema) {
			for _, existing := range claims[name] {
				if existing.node == node {
					return // the same node reached twice ($defs mirrored into definitions)
				}
			}
			claims[name] = append(claims[name], nameClaim{path: path, defKey: defKey, node: node, final: name})
		}

		add(rootNameOf(path, s), "", s)
		for _, m := range []map[string]*schema.Schema{s.Defs, s.Definitions} {
			for _, key := range sortedSchemaKeys(m) {
				if def := m[key]; def != nil {
					add(generator.SchemaNameToGoName(key), key, def)
				}
			}
		}
	}
	return claims
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
func describeNameSplit(name string, group []nameClaim) string {
	// One line per claim, deduplicated: a document that spells one definition
	// under both "definitions" and "$defs" makes two claims that say and become
	// the same thing, and reporting it twice would read as two definitions.
	seen := make(map[string]bool, len(group))
	docs := make(map[string]bool, len(group))
	lines := make([]string, 0, len(group))
	for _, c := range group {
		docs[c.path] = true
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
	return fmt.Sprintf("warning: %d input documents claim the Go type name %s, and those claims do not describe the same type, so they cannot be one:\n%s\n"+
		"one package holds one type per name, so sharing it would have given every document whichever schema was generated first and discarded the rest. "+
		"Each definition is qualified with its own document's root type name -- all of them, not only the later ones, so the generated names do not depend on the order the inputs were listed. "+
		"A document's own root type keeps the name it was given; --root-name sets both. "+
		"Make the definitions identical if they were meant to be one type, or rename one of them in the schema to choose the Go names yourself.\n",
		len(docs), name, strings.Join(lines, "\n"))
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
		for _, m := range []map[string]*schema.Schema{s.Defs, s.Definitions} {
			for _, key := range sortedSchemaKeys(m) {
				if name, ok := pinned[m[key]]; ok {
					if _, seen := owner[name]; !seen {
						owner[name] = fmt.Sprintf("$defs/%s in %s", key, path)
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
