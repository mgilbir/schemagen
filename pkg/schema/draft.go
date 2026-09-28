package schema

import (
	"slices"
	"sort"
	"strings"
)

// Draft represents a JSON Schema draft version.
type Draft int

const (
	DraftUnknown Draft = iota
	Draft03
	Draft04
	Draft06
	Draft07
	Draft201909
	Draft202012

	// DraftV1 is the undated stable release that succeeds the dated drafts,
	// dialect URI https://json-schema.org/v1.
	//
	// It is not an alias for Draft202012 even though the keyword set is nearly
	// the same, because the two disagree about the one keyword whose posture
	// this generator reads from the dialect. 2020-12 declares the
	// format-annotation vocabulary and the suite marks {"format":"email"}
	// satisfied by "2962"; v1 drops vocabularies and moves its format tests out
	// of optional/ into a required top-level format/ directory, where the same
	// document is marked invalid. Mapping v1 onto Draft202012 would silently
	// take the annotation reading and stop enforcing every format a v1 schema
	// names. See formatAssertsFor.
	DraftV1
)

// String returns a human-readable name for the draft.
func (d Draft) String() string {
	switch d {
	case Draft03:
		return "Draft-03"
	case Draft04:
		return "Draft-04"
	case Draft06:
		return "Draft-06"
	case Draft07:
		return "Draft-07"
	case Draft201909:
		return "Draft 2019-09"
	case Draft202012:
		return "Draft 2020-12"
	case DraftV1:
		return "v1"
	default:
		return "Unknown"
	}
}

// DetectDraft inspects the $schema URI to determine which draft version is used.
// See DraftForURI.
func DetectDraft(s *Schema) Draft {
	return DraftForURI(s.Schema)
}

// dialectURIs maps each meta-schema URI this package recognises to its draft,
// written without the scheme and without the trailing empty fragment. Each
// draft's hyper-schema is included: it is that draft's validation vocabulary
// plus hyper-schema keywords, which land in Extensions like any other keyword
// this package does not model.
var dialectURIs = map[string]Draft{
	"json-schema.org/draft-03/schema":            Draft03,
	"json-schema.org/draft-03/hyper-schema":      Draft03,
	"json-schema.org/draft-04/schema":            Draft04,
	"json-schema.org/draft-04/hyper-schema":      Draft04,
	"json-schema.org/draft-06/schema":            Draft06,
	"json-schema.org/draft-06/hyper-schema":      Draft06,
	"json-schema.org/draft-07/schema":            Draft07,
	"json-schema.org/draft-07/hyper-schema":      Draft07,
	"json-schema.org/draft/2019-09/schema":       Draft201909,
	"json-schema.org/draft/2019-09/hyper-schema": Draft201909,
	"json-schema.org/draft/2020-12/schema":       Draft202012,
	"json-schema.org/draft/2020-12/hyper-schema": Draft202012,
	"json-schema.org/v1":                         DraftV1,
}

// DraftForURI answers which draft a "$schema" URI names, or DraftUnknown for a
// URI that names none of them.
//
// The match is on the whole URI. A dialect is identified by its meta-schema's
// URI and nothing else, and a URI that merely contains a draft's name --
// https://example.com/my-draft-07-extension/schema, a custom meta-schema whose
// vocabulary this package cannot see -- is not that draft; reading it as one
// gated the document's keywords by a dialect it never declared. What is allowed
// to vary is what does not change the resource the URI names: the scheme
// (http or https; the drafts' own meta-schemas are published under both, and
// documents in the wild write both), and a trailing empty fragment ("#"), which
// the draft-03 to draft-07 URIs carry by convention and later drafts dropped.
func DraftForURI(uri string) Draft {
	rest, ok := strings.CutPrefix(uri, "https://")
	if !ok {
		if rest, ok = strings.CutPrefix(uri, "http://"); !ok {
			return DraftUnknown
		}
	}
	return dialectURIs[strings.TrimSuffix(rest, "#")]
}

// Normalize ensures the schema is consistent regardless of which draft it was
// authored in. It performs the following normalizations:
//   - Drops every keyword the node's own dialect does not define
//   - Copies definitions <-> $defs bidirectionally
//   - Copies Draft 3/4 "id" to "$id"
//   - Converts Draft 3 "extends" to allOf
//   - Converts Draft 3 "divisibleBy" to multipleOf
//   - Converts Draft 4-7 "dependencies" to dependentSchemas/dependentRequired
//   - Recursively normalizes all nested schemas
//
// The dialect is the document's own $schema, inherited by every node that does
// not declare one of its own. NormalizeForDraft is the same walk with the root's
// dialect supplied from outside, which is what --draft does.
func (s *Schema) Normalize() {
	s.NormalizeForDraft(DraftUnknown)
}

// NormalizeForDraft normalizes under a dialect chosen from outside the document,
// which stands in for the root's own $schema exactly as the --draft flag does.
//
// Only the root's dialect is supplied: a nested node declaring its own $schema
// is an embedded resource and keeps it, for the subtree below it too. Passing
// DraftUnknown is the same as calling Normalize -- it means "read the dialect
// from the document" and not "this document has no dialect".
//
// Normalizing is done once per node, and a second call is a no-op for every
// node the first one reached: Normalize(Normalize(x)) is Normalize(x). That is
// not an optimisation. The rewrites turn a keyword one dialect defines into the
// keyword a later dialect replaced it with -- draft 3's per-property boolean
// "required" into the parent's array of names, "extends" into "allOf" -- and
// the result is this package's internal spelling, not something the document
// said. Read again as if it were the document, the dialect pass finds a draft-3
// node stating an array-form "required", which draft 3 does not define, and
// deletes the requirement the first pass had just carried over. So a node
// records that it has been normalized, and under which dialect, and is not read
// as a document again; its children are still visited, so a subtree attached
// to an already-normalized node is normalized under the dialect it inherits.
//
// A dialect supplied from outside is the one the whole document is read under
// but for an embedded resource -- a node declaring its own $id beside its own
// $schema -- which keeps its own; a $schema written on any other nested node is
// overridden with the root's. Without one, every nested $schema switches the
// subtree below it, as it always has. That is the rule of dialect.go, which the
// resource index and the generator read too.
func (s *Schema) NormalizeForDraft(d Draft) {
	s.normalizeForDraft(d, d != DraftUnknown)
}

// normalizeForDraft is NormalizeForDraft, with given saying whether d was
// chosen from outside the document -- which decides which nested $schema may
// switch a subtree's dialect (ownDialect). A subschema normalized on demand
// passes on the answer its parent was normalized under.
func (s *Schema) normalizeForDraft(d Draft, given bool) {
	if s == nil {
		return
	}
	// Every node is located before anything moves: this is the last point at
	// which the tree has the document's shape, and where a node was written is
	// what every diagnostic after this reports it by. A node already located --
	// a subtree of a document normalized before, or this document a second
	// time -- keeps the location it has. See source.go. A document that is a
	// bare boolean is located too, though there is nothing in it to rewrite.
	s.locate()
	if s.IsBooleanSchema() {
		return
	}
	if d == DraftUnknown {
		d = DetectDraft(s)
	}

	// The dialect gate is a pass of its own, run over the whole tree before the
	// first rewrite. Both halves of that sentence are load-bearing.
	//
	// *Before*, because five of the rewrites read a keyword one dialect alone
	// defines and write one that dialect does not have -- extends into allOf,
	// divisibleBy into multipleOf, disallow into not, the per-property boolean
	// required into the parent's array, dependencies into the 2019-09 pair.
	// Gating afterwards would delete what the rewrite had just legitimately
	// produced: a draft-3 document's allOf, arrived at from its own "extends",
	// dropped as a keyword draft 3 does not define. Gating first makes each
	// rewrite fire exactly where its source keyword survived the gate, which is
	// exactly the dialect that defines it.
	//
	// *A pass of its own*, because the rewrites also synthesize nodes -- draft
	// 3's {"disallow":["a","b"]} becomes a "not" holding an "anyOf", and draft 3
	// has no anyOf. A gate interleaved with the rewrites would reach that
	// synthesized node on the way down and clear the branch list it had just
	// built. The gate answers what the *document* states; the rewrites' output is
	// this package's internal spelling of what it states, and is not re-read.
	//
	// The subschemas *inside* the rewritten keywords are the document's, though,
	// and the pass reaches every one of them: they are parsed when the document
	// is (see ExtendsSchemas) and are children like any other.
	declared := DetectDraft(s)
	if declared == DraftUnknown {
		declared = d
	}
	s.gateDialectKeywords(d, declared, given)
	s.normalizeNode(d, given)
}

// gateDialectKeywords clears, over the whole tree, every keyword a node's own
// dialect does not define. A nested node whose own $schema switches the
// dialect (ownDialect) takes it, for itself and everything below it. A node
// already normalized is not read again; see NormalizeForDraft. declared is the
// dialect the document itself states for the node, which differs from d only
// under a dialect chosen from outside it.
func (s *Schema) gateDialectKeywords(d, declared Draft, given bool) {
	if s == nil || s.IsBooleanSchema() {
		return
	}
	if s.normalized {
		d, declared = s.DetectedDraft, s.DetectedDraft
		given = s.dialectGiven
	} else {
		s.dropKeywordsOutsideDialect(d, declared)
		s.settleMalformedKeywords(d)
	}
	s.eachChild(func(sub *Schema) {
		child, childDeclared := d, declared
		if own := ownDialect(sub, given); own != DraftUnknown {
			child, childDeclared = own, own
		} else if stated := DetectDraft(sub); stated != DraftUnknown {
			childDeclared = stated
		}
		sub.gateDialectKeywords(child, childDeclared, given)
	})
}

// normalizeInherited normalizes a nested node under the dialect it inherits,
// which its own $schema overrides for it and everything below it where it may
// (ownDialect).
func (s *Schema) normalizeInherited(d Draft, given bool) {
	if s == nil || s.IsBooleanSchema() {
		return
	}
	if own := ownDialect(s, given); own != DraftUnknown {
		d = own
	}
	s.normalizeNode(d, given)
}

func (s *Schema) normalizeNode(d Draft, given bool) {
	if s.normalized {
		d = s.DetectedDraft
		given = s.dialectGiven
	} else {
		s.rewriteLegacyKeywords(d)
		s.DetectedDraft = d
		s.dialectGiven = given
		s.normalized = true
	}
	s.normalizeChildren(d, given)
}

// rewriteLegacyKeywords rewrites every keyword this node states in a spelling
// a later draft replaced into the spelling that replaced it.
//
// Only one of the rewrites asks which dialect the node is in, and the others
// need not: the dialect pass has already cleared every keyword the node's
// dialect does not define, so a source keyword still standing here is one the
// dialect defines. Under DraftUnknown -- no recognised $schema, read as the union of
// every dialect -- the source and its replacement can both stand, and then
// both bind: each rewrite combines with what is already there rather than
// assuming it is alone.
func (s *Schema) rewriteLegacyKeywords(d Draft) {
	// Copy Draft 3/4 "id" to "$id" if $id is empty.
	if s.ID == "" && s.LegacyID != "" {
		s.ID = s.LegacyID
	}

	// Copy definitions → $defs if $defs is empty.
	if len(s.Defs) == 0 && len(s.Definitions) > 0 {
		s.Defs = make(map[string]*Schema, len(s.Definitions))
		// maporder: copies members under their own keys, which are distinct, so no order writes a different map.
		for k, v := range s.Definitions {
			s.Defs[k] = v
		}
		s.MirroredDefinitions = "$defs"
	}

	// Copy $defs → definitions if definitions is empty.
	if len(s.Definitions) == 0 && len(s.Defs) > 0 {
		s.Definitions = make(map[string]*Schema, len(s.Defs))
		// maporder: copies members under their own keys, which are distinct, so no order writes a different map.
		for k, v := range s.Defs {
			s.Definitions[k] = v
		}
		s.MirroredDefinitions = "definitions"
	}

	// Draft 3: "extends" is allOf.
	if s.Extends != nil || s.ExtendsSchemas != nil {
		s.AllOf = append(s.AllOf, s.ExtendsSchemas...)
		s.Extends, s.ExtendsSchemas = nil, nil
	}

	// Draft 3: a property's own "required": true is the parent's array entry.
	//
	// This is the one rewrite that asks the dialect, and it asks this node's
	// rather than the property's. The boolean is stated on the property, and
	// the property's dialect has already decided whether that spelling is
	// legal there; but what it means -- "the parent must have this member" --
	// is a constraint on the parent, and only a parent whose dialect defines
	// the spelling reads it. A draft-3 resource embedded as a property of a
	// draft-6 object says "required": true legitimately, and the draft-6 object
	// applies that schema only to a member that is present, so nothing makes
	// the member required. The promotion happens here, before the property's
	// own pass clears the field.
	if BooleanRequiredDefinedIn(d) {
		s.promoteDraft3Required()
	}
	// On this node the boolean has now done whatever it could: it was promoted
	// by the parent's pass above this one, or it sits where draft 3 gives it no
	// meaning -- the root of a document, an additionalProperties schema, an
	// items schema -- because "required" there asks whether a value is present
	// and there is always one. Either way nothing downstream reads it.
	s.Draft3Required = nil

	// Draft 3: "divisibleBy" is multipleOf.
	if s.DivisibleBy != nil {
		switch {
		case s.MultipleOf == nil:
			s.MultipleOf = s.DivisibleBy
		case *s.MultipleOf != *s.DivisibleBy:
			s.AllOf = append(s.AllOf, s.placeAt(&Schema{MultipleOf: s.DivisibleBy}, "divisibleBy"))
		}
	}

	// Draft 3: "disallow" is "not" of the union of its entries.
	if s.Disallow != nil || s.DisallowSchemas != nil {
		var forbidden *Schema
		switch len(s.DisallowSchemas) {
		case 0:
			// {"disallow":[]} forbids nothing.
		case 1:
			forbidden = s.DisallowSchemas[0]
		default:
			forbidden = s.placeAt(&Schema{AnyOf: s.DisallowSchemas}, "disallow")
		}
		if forbidden != nil {
			if s.Not == nil {
				s.Not = forbidden
			} else {
				s.AllOf = append(s.AllOf, s.placeAt(&Schema{Not: forbidden}, "disallow"))
			}
		}
		s.Disallow, s.DisallowSchemas = nil, nil
	}

	// Drafts 3-7: "dependencies" is 2019-09's dependentSchemas and
	// dependentRequired, split by the shape of each member.
	if s.Dependencies != nil || s.DependencySchemas != nil || s.DependencyRequired != nil {
		s.mergeDependencies()
	}

	// Every draft: drop the enum members a member before them already admits.
	if len(s.Enum) > 1 {
		s.dedupeEnum()
	}
}

// promoteDraft3Required adds to this node's Required every property whose own
// schema says "required": true, in name order so the result does not depend on
// map iteration.
func (s *Schema) promoteDraft3Required() {
	for _, name := range sortedKeys(s.Properties) {
		prop := s.Properties[name]
		if prop != nil && prop.Draft3Required != nil && *prop.Draft3Required && !slices.Contains(s.Required, name) {
			s.Required = append(s.Required, name)
		}
	}
}

// mergeDependencies folds "dependencies" into dependentSchemas and
// dependentRequired.
//
// A property can already have an entry there -- a document with no recognised
// dialect may state both spellings -- and then both constraints hold: the
// schemas are joined under allOf and the name lists are unioned. Writing over
// the existing entry, as this used to, dropped whichever the document stated
// first.
func (s *Schema) mergeDependencies() {
	for _, key := range sortedKeys(s.DependencySchemas) {
		dep := s.DependencySchemas[key]
		if s.DependentSchemas == nil {
			s.DependentSchemas = make(map[string]*Schema)
		}
		if have, ok := s.DependentSchemas[key]; ok && have != nil {
			// Both spellings name this property. The node that joins them is
			// located at the one this rewrite brought in; each half keeps its
			// own location.
			s.DependentSchemas[key] = s.placeAt(&Schema{AllOf: []*Schema{have, dep}}, "dependencies", key)
			continue
		}
		s.DependentSchemas[key] = dep
	}
	reqKeys := make([]string, 0, len(s.DependencyRequired))
	for key := range s.DependencyRequired {
		reqKeys = append(reqKeys, key)
	}
	sort.Strings(reqKeys)
	for _, key := range reqKeys {
		if s.DependentRequired == nil {
			s.DependentRequired = make(map[string][]string)
		}
		names := s.DependentRequired[key]
		for _, name := range s.DependencyRequired[key] {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		if names == nil {
			names = []string{}
		}
		s.DependentRequired[key] = names
	}
	s.Dependencies, s.DependencySchemas, s.DependencyRequired = nil, nil, nil
}

// dedupeEnum drops every enum member some earlier member is already equal to.
//
// The specification says members SHOULD be unique -- a recommendation, not a
// requirement -- so {"enum":["a","a","a"]} is a legal schema that admits "a"
// and nothing else. It reached the generator as three members, which became
// three Go constants of one value and a switch naming all three: "duplicate
// case RootA2 in expression switch", gofmt-clean generated code that does not
// compile, behind a zero exit code. See issue #269.
//
// Deduplicating here rather than at the one site that emitted the switch is
// deliberate. "enum" is read in a dozen places -- to pick the Go base type, to
// name the constants, to build the runtime evaluator's node, to write the raw
// member list -- and a duplicate is meaningless to every one of them. One pass
// over the parsed document is what makes them agree; a filter at the emitter
// would leave the others counting members that say nothing.
//
// Equality is JSON equality and not text equality, which is what the keyword is
// defined over: 1, 1.0 and 1e0 are one member, and so are {"a":1,"b":2} and
// {"b":2,"a":1}. A member that cannot be canonicalised at all is kept, and kept
// distinct from every other, because dropping it would be dropping a value the
// enum admits.
//
// Order is the document's, and the first spelling of a repeated value is the
// one that survives -- so the constant an enum member is named by does not move
// when a later duplicate is removed.
func (s *Schema) dedupeEnum() {
	seen := make(map[string]bool, len(s.Enum))
	out := s.Enum[:0:0]
	for _, v := range s.Enum {
		key, ok := CanonicalJSON(v)
		if ok {
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		out = append(out, v)
	}
	s.Enum = out
}

// normalizeChildren recursively normalizes all nested sub-schemas under the
// dialect they inherit from this one.
func (s *Schema) normalizeChildren(d Draft, given bool) {
	s.eachChild(func(sub *Schema) { sub.normalizeInherited(d, given) })
}

// eachChild calls fn for every sub-schema this node holds directly.
//
// It is the one traversal the dialect gate and the legacy rewrites share, so
// that a keyword position added to Schema cannot be reached by one and missed by
// the other -- the failure that let a $recursiveRef under propertyNames escape
// the pass that was meant to clear it.
func (s *Schema) eachChild(fn func(*Schema)) {
	visit := func(sub *Schema) {
		if sub != nil {
			fn(sub)
		}
	}
	// maporder: each member heads its own subtree, and the visit writes only into the subtree it is handed.
	for _, sub := range s.Properties {
		visit(sub)
	}
	for _, sub := range s.TypeSchemas {
		visit(sub)
	}
	// maporder: each member heads its own subtree, and the visit writes only into the subtree it is handed.
	for _, sub := range s.PatternProperties {
		visit(sub)
	}
	// maporder: each member heads its own subtree, and the visit writes only into the subtree it is handed.
	for _, sub := range s.Defs {
		visit(sub)
	}
	// maporder: each member heads its own subtree, and the visit writes only into the subtree it is handed.
	for _, sub := range s.Definitions {
		visit(sub)
	}
	for _, sub := range s.AllOf {
		visit(sub)
	}
	for _, sub := range s.AnyOf {
		visit(sub)
	}
	for _, sub := range s.OneOf {
		visit(sub)
	}
	for _, sub := range s.PrefixItems {
		visit(sub)
	}
	visit(s.Not)
	if s.Items != nil {
		visit(s.Items.Schema)
		for _, sub := range s.Items.Schemas {
			visit(sub)
		}
	}
	if s.AdditionalProperties != nil {
		visit(s.AdditionalProperties.Schema)
	}
	if s.AdditionalItems != nil {
		visit(s.AdditionalItems.Schema)
	}
	visit(s.If)
	visit(s.Then)
	visit(s.Else)
	visit(s.Contains)
	visit(s.PropertyNames)
	visit(s.ContentSchema)
	visit(s.UnevaluatedItems)
	visit(s.UnevaluatedProperties)
	// maporder: each member heads its own subtree, and the visit writes only into the subtree it is handed.
	for _, sub := range s.DependentSchemas {
		visit(sub)
	}
	// The subschemas of draft 3's and drafts 3-7's keywords, parsed but not yet
	// rewritten. They are here so the dialect pass reaches them under their own
	// dialect before the rewrite moves them into allOf, not and
	// dependentSchemas; see ExtendsSchemas.
	for _, sub := range s.ExtendsSchemas {
		visit(sub)
	}
	for _, sub := range s.DisallowSchemas {
		visit(sub)
	}
	for _, key := range sortedKeys(s.DependencySchemas) {
		visit(s.DependencySchemas[key])
	}
}
