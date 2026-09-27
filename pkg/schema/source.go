package schema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// This file is where a node remembers where the document wrote it.
//
// Normalize moves subschemas. It rewrites draft 3's "extends" into "allOf",
// "disallow" into "not", "dependencies" into "dependentSchemas", draft 3's
// schema-valued "type" entries into an anyOf, and mirrors "definitions" and
// "$defs" into each other -- and every walk that runs after it, which is every
// walk the generator does, sees the rewritten tree. A diagnostic that described
// a node by the path such a walk took to it named a location in this package's
// internal spelling of the document rather than in the document: a null
// "minLength" inside the second "extends" entry was reported at
// "#/allOf/1/minLength", and a malformed draft-07 definition at "#/$defs/a",
// neither of which the author wrote or can find.
//
// So where a node was written is recorded when it is read, and nothing works it
// out afterwards. The decode notes, for every subschema a node's object holds,
// the reference tokens that lead to it from the node (srcChildren); Normalize,
// before it moves anything, turns those into each node's location from its
// document's root (locate); and a node a rewrite creates is placed at the
// keyword that produced it. The resolver's answer to a pointer into a rewritten
// keyword ("#/extends/0") reads the same record, so the $ref a document writes
// and the location a diagnostic prints are one fact about the node, not two
// that can disagree.

// srcChild is one subschema a node's object holds, at the reference tokens that
// lead to it from that node.
type srcChild struct {
	tokens []string
	// node is the subschema. It is nil when holder is set.
	node *Schema
	// holder is a boolean held by a SchemaOrBool ({"additionalProperties":
	// false}), which becomes a node only when something asks for it
	// (SchemaOrBool.AsSchema) and takes this location then.
	holder *SchemaOrBool
	// typeName marks a node read from a type name rather than a schema: draft
	// 3's {"disallow":["string"]} is read as {"type":"string"}. It is located
	// at the name, which is what the document wrote in its place, but the
	// document holds no schema there for a pointer to reach (childAt).
	typeName bool
}

// source is where a document wrote a node, as a link to the node whose object
// holds it: the location from the document's root is the chain of links, read
// only when something asks for it (SourceLocation). Holding the whole path on
// every node instead cost every node a slice as long as its depth.
type source struct {
	// parent is the node whose object holds this one; nil at a document's root.
	parent *Schema
	// rel is the reference tokens from parent to this node.
	rel []string
	// doc is set on a document's root, to the root itself. A root whose doc
	// is nil is one that must not be given a location (see unlocated).
	doc *Schema
	// set records that the node has been located.
	set bool
}

// unlocated is the location of a node that has none and must not be given one
// by the Normalize that follows -- one parsed from a vendor keyword of a node
// that is itself unlocated, which Normalize would otherwise take for the root
// of a document. SourceLocation reports no location for it or for anything
// below it.
var unlocated = source{set: true}

// SourceLocation reports where the document wrote this node: the root node of
// the document it was read with, and the JSON Pointer reference tokens from that
// root to the node, as the document spelled the path -- "extends" and not the
// "allOf" Normalize moved it into, "definitions" and not its "$defs" mirror.
//
// A node is located by Normalize, over the document it is called on, before any
// rewrite; one Normalize synthesizes is located at the keyword that produced it;
// one parsed later from a vendor keyword's value is located inside that value.
// ok is false for a node no document wrote -- one built through the Go API -- and
// for a document not normalized yet.
//
// PointerFragment(tokens...) writes the location as a URI fragment that
// FragmentPointer reads back to the same tokens.
func (s *Schema) SourceLocation() (doc *Schema, tokens []string, ok bool) {
	root, tokens, ok := s.pathUpTo(nil)
	if !ok || root.src.doc == nil {
		return nil, nil, false
	}
	return root.src.doc, tokens, true
}

// SourceLocationWithin reports where the document wrote this node relative to
// root: the reference tokens from root to it, when root is a node the document
// wrote this one inside. It is the location to write after the URI of an
// embedded resource, whose fragments are relative to the resource and not to
// the file that holds it.
func (s *Schema) SourceLocationWithin(root *Schema) ([]string, bool) {
	if root == nil {
		return nil, false
	}
	top, tokens, ok := s.pathUpTo(root)
	if !ok || top != root {
		return nil, false
	}
	return tokens, true
}

// pathUpTo follows the location links up from s until it reaches stop, or a
// document's root when stop is nil or not on the way, and returns the node it
// stopped at and the reference tokens from there to s. ok is false for a node
// never located.
func (s *Schema) pathUpTo(stop *Schema) (*Schema, []string, bool) {
	if s == nil || !s.src.set {
		return nil, nil, false
	}
	n, depth := s, 0
	for n != stop && n.src.parent != nil {
		depth += len(n.src.rel)
		n = n.src.parent
	}
	tokens := make([]string, depth)
	for m := s; m != n; m = m.src.parent {
		depth -= len(m.src.rel)
		copy(tokens[depth:], m.src.rel)
	}
	return n, tokens, true
}

// locate records this node as the root of the document it was read with, and
// every node of the subtree not located already below it. A node keeps the
// first location it is given: that is where its document wrote it, and a later
// call -- Normalize on a subtree, or a second Normalize -- has less to go on.
func (s *Schema) locate() {
	if s == nil || s.src.set {
		return
	}
	s.src = source{doc: s, set: true}
	s.locateChildren()
}

// locateChildren locates the subschemas this node's object holds, below this
// node.
func (s *Schema) locateChildren() {
	if !s.src.set {
		return
	}
	for _, c := range s.srcChildren {
		if c.holder != nil {
			if c.holder.src == nil {
				c.holder.src = &source{parent: s, rel: c.tokens, set: true}
			}
			continue
		}
		if !c.node.src.set {
			c.node.src = source{parent: s, rel: c.tokens, set: true}
			c.node.locateChildren()
		}
	}
}

// placeAt locates n, a node this one's rewrite synthesized, at tokens below
// this node: the keyword whose value it stands for. It returns n.
func (s *Schema) placeAt(n *Schema, tokens ...string) *Schema {
	if s.src.set && n != nil && !n.src.set {
		n.src = source{parent: s, rel: tokens, set: true}
	}
	return n
}

// noteChild records that tokens, from this node, lead to the subschema child.
func (s *Schema) noteChild(child *Schema, tokens ...string) {
	if child == nil {
		return
	}
	s.srcChildren = append(s.srcChildren, srcChild{tokens: tokens, node: child})
}

// noteChildrenOf records every subschema a decoded keyword value holds, at its
// location under key. v is the value as decoded into its field: a *Schema, a
// []*Schema, a map[string]*Schema, a *SchemaOrBool or a *SchemaOrSchemaArray.
// Any other type holds no subschema.
func (s *Schema) noteChildrenOf(key string, v reflect.Value) {
	switch x := v.Interface().(type) {
	case *Schema:
		s.noteChild(x, key)
	case []*Schema:
		for i, sub := range x {
			s.noteChild(sub, key, strconv.Itoa(i))
		}
	case map[string]*Schema:
		for _, k := range sortedKeys(x) {
			s.noteChild(x[k], key, k)
		}
	case *SchemaOrBool:
		switch {
		case x == nil:
		case x.Schema != nil:
			s.noteChild(x.Schema, key)
		case x.Bool != nil:
			s.srcChildren = append(s.srcChildren, srcChild{tokens: []string{key}, holder: x})
		}
	case *SchemaOrSchemaArray:
		switch {
		case x == nil:
		case x.Schema != nil:
			s.noteChild(x.Schema, key)
		default:
			for i, sub := range x.Schemas {
				s.noteChild(sub, key, strconv.Itoa(i))
			}
		}
	}
}

// childAt returns the subschema the document wrote at tokens below this node, if
// the decode recorded one there.
func (s *Schema) childAt(tokens ...string) *Schema {
	for _, c := range s.srcChildren {
		if c.node != nil && !c.typeName && slicesEqual(c.tokens, tokens) {
			return c.node
		}
	}
	return nil
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// subPathError places a malformed value below the keyword that holds it: the
// null second entry of an "allOf" is at allOf/1, and the keyword's value as a
// whole -- an array, which "allOf" may hold -- is not what is wrong. See
// KeywordError.Path.
type subPathError struct {
	path []string
	err  error
}

func (e *subPathError) Error() string {
	return fmt.Sprintf("%s: %v", strings.Join(escapeTokens(e.path), "/"), e.err)
}

func (e *subPathError) Unwrap() error { return e.err }

// atPath places err at the reference tokens path below its keyword. An error
// already placed deeper is placed below path in turn.
func atPath(err error, path ...string) error {
	if inner, ok := err.(*subPathError); ok {
		return &subPathError{path: append(append([]string(nil), path...), inner.path...), err: inner.err}
	}
	return &subPathError{path: path, err: err}
}

// escapeTokens applies RFC 6901's escaping to each token.
func escapeTokens(tokens []string) []string {
	out := make([]string, len(tokens))
	for i, t := range tokens {
		t = strings.ReplaceAll(t, "~", "~0")
		out[i] = strings.ReplaceAll(t, "/", "~1")
	}
	return out
}

// notASubschema is the error for a value in a subschema position that is
// neither an object nor a boolean -- the two things a schema can be.
func notASubschema(raw json.RawMessage) error {
	return fmt.Errorf("a schema must be an object or a boolean, got: %s", abbreviateJSON(trimJSONWhitespace(raw)))
}

// checkSubschemaEntry reports what is wrong with one entry of a subschema
// container, or nil if it can be a schema.
func checkSubschemaEntry(raw json.RawMessage) error {
	t := trimJSONWhitespace(raw)
	switch {
	case t == "null":
		return errNullSchema
	case t == "true", t == "false", len(t) > 0 && t[0] == '{':
		return nil
	}
	return notASubschema(raw)
}

// nilSubschemaEntry finds the first nil entry of a decoded subschema container
// -- what a null the document wrote there decodes into -- and says where it is
// below the keyword: a slice's entries in order, a map's members by name in
// sorted order.
//
// This is what lets a null entry be reported where the document wrote it. Left
// in the tree, only a walk of it could find the nil -- and every walk after
// Normalize sees "definitions" under its "$defs" mirror, and "extends" under
// "allOf".
func nilSubschemaEntry(v reflect.Value) ([]string, bool) {
	var list []*Schema
	switch x := v.Interface().(type) {
	case []*Schema:
		list = x
	case *SchemaOrSchemaArray:
		if x != nil {
			list = x.Schemas
		}
	case map[string]*Schema:
		hasNil := false
		// maporder: a predicate; it returns the same answer whichever member it stops at.
		for _, sub := range x {
			if sub == nil {
				hasNil = true
				break
			}
		}
		if !hasNil {
			return nil, false
		}
		for _, k := range sortedKeys(x) {
			if x[k] == nil {
				return []string{k}, true
			}
		}
		return nil, false
	}
	for i, sub := range list {
		if sub == nil {
			return []string{strconv.Itoa(i)}, true
		}
	}
	return nil, false
}

// badSubschemaEntry finds, in a value that failed to decode, the first entry of
// a subschema container that is not a schema, and says where it is below the
// keyword, in nilSubschemaEntry's order. typ is the field's type; a value whose
// shape is not the container's own was refused as a whole, and is left so.
func badSubschemaEntry(raw json.RawMessage, typ reflect.Type) ([]string, error) {
	t := trimJSONWhitespace(raw)
	switch typ {
	case reflect.TypeOf([]*Schema(nil)), reflect.TypeOf(&SchemaOrSchemaArray{}):
		if len(t) == 0 || t[0] != '[' {
			return nil, nil
		}
		var elems []json.RawMessage
		if json.Unmarshal(raw, &elems) != nil {
			return nil, nil
		}
		for i, elem := range elems {
			if err := checkSubschemaEntry(elem); err != nil {
				return []string{strconv.Itoa(i)}, err
			}
		}
	case reflect.TypeOf(map[string]*Schema(nil)):
		if len(t) == 0 || t[0] != '{' {
			return nil, nil
		}
		var members map[string]json.RawMessage
		if json.Unmarshal(raw, &members) != nil {
			return nil, nil
		}
		for _, k := range sortedRawKeys(members) {
			if err := checkSubschemaEntry(members[k]); err != nil {
				return []string{k}, err
			}
		}
	}
	return nil, nil
}
