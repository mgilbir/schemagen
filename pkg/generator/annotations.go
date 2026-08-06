package generator

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// allowing builds a keyword allow-list: the keywords a path models, plus every
// keyword that constrains nothing wherever it sits.
//
// The second half is not written out at the call sites, and that is the whole
// point of this function. There were two allow-lists here, each carrying its own
// hand-written copy of "the keywords that carry no constraint", and they
// disagreed: annotationKeywords was missing $defs, definitions, id, $anchor,
// $dynamicAnchor and $recursiveAnchor, and both were missing $vocabulary. A
// keyword outside an allow-list refuses the whole schema, so an entirely unused
// $defs -- which almost every real document carries -- was enough to send a
// schema back to the static path and leave its unevaluatedItems unenforced
// (issue #178), while a $vocabulary did the same to the runtime evaluator.
//
// nonConstrainingKeywords and inertKeywords are the two halves of that set, and
// they already existed: unenforcedKeywords reads exactly this union to decide
// which keywords an `any` alias is really dropping. Reading it here as well is
// what makes the allow-lists agree by construction rather than by everybody
// remembering to edit both. The lists may still differ over the keywords they
// *model*, which is a real design difference -- see
// TestKeywordAllowListsAgreeOnWhatConstrainsNothing, which enumerates it.
func allowing(modelled map[string]bool) map[string]bool {
	allowed := make(map[string]bool, len(modelled)+len(nonConstrainingKeywords)+len(inertKeywords))
	for _, set := range []map[string]bool{modelled, nonConstrainingKeywords, inertKeywords} {
		for key := range set {
			allowed[key] = true
		}
	}
	return allowed
}

// annotationKeywords are the keywords the unevaluatedItems path compiles to the
// runtime evaluator. Anything outside this set makes a schema ineligible, so it
// keeps today's static checks instead of being evaluated with a keyword silently
// ignored.
//
// The literal below names only the keywords this path *models*; allowing() adds
// the ones that constrain nothing, which are not this list's business to decide.
//
// It is deliberately narrower than validatorKeywords below. This path takes over
// schemas that already generate working static checks, and widening it would
// change the shape of code that is not broken; the other path only ever takes
// over a schema that was about to become `type X any`.
var annotationKeywords = allowing(map[string]bool{
	"type": true, "const": true, "multipleOf": true, "minimum": true, "maximum": true,
	"prefixItems": true, "items": true, "additionalItems": true,
	"contains": true, "minContains": true, "maxContains": true,
	"allOf": true, "anyOf": true, "oneOf": true,
	"if": true, "then": true, "else": true,
	"unevaluatedItems": true,
})

// validatorKeywords are the keywords the runtime evaluator models when it is
// asked to enforce a whole schema, rather than only the array-annotation subset.
//
// The keywords that are absent are as much of the design as the ones present.
// "format" is left out because schemagen asserts a format only where the schema
// gives the position a string type, and a node evaluator that quietly ignored it
// would enforce a different schema here than the static path does two lines
// away. The content keywords are left out because nothing here models them.
// "dependencies", "extends" and "disallow" are left out because Normalize
// rewrites them into modern keywords but leaves the originals in place, so
// accepting the key would risk reading a schema twice.
//
// "$dynamicRef" and "$recursiveRef" are here, but they are the two entries a
// caller may still be refused over: the tree is inlined, and where such a
// reference resolves is a property of the path the *instance* evaluation took to
// it, which no inlined tree states. What is compiled is the search rather than
// its answer -- the anchor name, and the resources that publish it -- and a
// caller that cannot emit the package-level variables that needs is declined.
// The anchors themselves stay non-constraining, which they always were: an
// anchor nothing looks up constrains nothing.
//
// Everything not listed fails the schema closed, so a keyword the parser learns
// later cannot be dropped silently.
//
// As with annotationKeywords, the literal names only the keywords this path
// models; the ones that carry no constraint where they sit come from allowing().
var validatorKeywords = allowing(map[string]bool{
	"type": true, "const": true, "enum": true,
	"multipleOf": true, "minimum": true, "maximum": true,
	"exclusiveMinimum": true, "exclusiveMaximum": true, "divisibleBy": true,
	"minLength": true, "maxLength": true, "pattern": true,
	"prefixItems": true, "items": true, "additionalItems": true,
	"minItems": true, "maxItems": true, "uniqueItems": true,
	"contains": true, "minContains": true, "maxContains": true,
	"properties": true, "patternProperties": true, "additionalProperties": true,
	"propertyNames": true, "required": true,
	"minProperties": true, "maxProperties": true,
	"dependentRequired": true, "dependentSchemas": true,
	"allOf": true, "anyOf": true, "oneOf": true, "not": true,
	"if": true, "then": true, "else": true,
	"unevaluatedItems":      true,
	"unevaluatedProperties": true,
	"$ref":                  true,
	"$dynamicRef":           true,
	"$recursiveRef":         true,
})

// inertKeywords are standard keywords that constrain nothing and have no field
// on schema.Schema, so the parser files them under Extensions alongside genuinely
// unknown ones. Listing them here is what keeps a $comment from costing a schema
// its validation; every other extension still refuses the schema, because a
// keyword schemagen has never seen could demand anything at all.
//
// The list is closed on purpose. Each entry is an annotation in every draft that
// defines it, which is the property that makes ignoring it sound.
//
// "deprecated", "readOnly" and "writeOnly" were here and are not any more. They
// have fields on schema.Schema now, so the parser no longer files them under
// Extensions and the eligible() loop above can never see them; they moved to
// nonConstrainingKeywords, which is the list that speaks for a keyword the
// marshaled form shows. Removing them from here without adding them there is a
// mistake that costs nothing visible -- see
// TestAnnotationVocabularyConstrainsNothing, which is what caught it.
var inertKeywords = map[string]bool{
	"$comment": true, "examples": true,
}

// maxRuntimeNodes and maxRuntimeDepth bound how large a compiled schema may
// get. A $ref is inlined at the point of use, so a document that refers to the
// same large definition from many places multiplies rather than shares; and a
// node's literal contains every literal below it, so cost grows with the product
// of size and depth rather than with size. A schema that hits either bound is
// refused, which is the same answer as any other thing the evaluator cannot
// carry.
//
// The depth bound is the one that bites: testdata/schemas/adversarial/deep holds
// a thousand nested anyOf and two thousand nested not, which is a legal document
// and no use to anybody. Without the bound the builder spends minutes assembling
// a literal for it. Real schemas nest an order of magnitude less than this.
//
// maxRuntimeRounds bounds a different thing: the fixpoint the hoisting builder
// runs. Each round either discovers a schema that has to become a node of its
// own or an anchor name the dynamic scope has to carry, and both sets only grow,
// so the loop ends on its own for any schema. The bound is what keeps a
// pathological one from spending the time to prove it.
//
// maxRuntimeHoists bounds the other factor of the same product. A round
// re-renders every hoisted node as well as the root, so the cost is rounds times
// nodes and neither may be left to the schema.
const (
	maxRuntimeNodes  = 4000
	maxRuntimeDepth  = 48
	maxRuntimeRounds = 64
	maxRuntimeHoists = 32
)

// nodeBuilder renders a schema as a Go _schemaNode composite literal.
//
// allowed decides which keywords the caller is prepared to have modelled;
// anything else refuses the whole subtree. inlineRefs turns on $ref resolution,
// which is what makes the stack necessary: a reference that leads back to a
// schema already being rendered would inline for ever.
//
// hoistPrefix is what a caller able to emit package-level variables sets, and it
// changes the answer to that cycle. A reference that closes a loop by descending
// into the value -- an object whose additionalProperties are objects of the same
// shape -- terminates for any finite document, and is emitted as a node
// variable the cycle points back at rather than refused. Without a prefix the
// cycle is refused as before, which is the answer for a caller emitting a
// literal inside a function body, where there is nowhere to put such a variable.
type nodeBuilder struct {
	g           *Generator
	allowed     map[string]bool
	inlineRefs  bool
	hoistPrefix string

	// stack maps a schema being rendered to the number of value-descending
	// steps taken when rendering of it began. Meeting it again with a larger
	// count is a cycle that shrinks the value each time round; meeting it with
	// the same count is a schema that applies to the same value for ever.
	stack    map[*schema.Schema]int
	descents int

	// resource is the schema resource the node being rendered belongs to. A node
	// whose resource differs from it is *entering* one, which is what puts that
	// resource on the dynamic scope -- and a reference into the middle of a
	// resource does that as much as arriving at its root does, which is why this
	// is tracked rather than recognised from the node.
	resource *schema.Schema

	depth       int
	nodes       int
	usesPattern bool

	// hoisted names the schemas rendered as variables of their own, in the order
	// they were found, so the names and the emitted order are the same on every
	// run. rendering is the one whose body is being produced right now, which is
	// the single place a hoisted schema is written out rather than referred to.
	hoisted    map[*schema.Schema]string
	hoistOrder []*schema.Schema
	rendering  *schema.Schema

	// dynAnchors holds the anchor names some compiled reference resolves through
	// the dynamic scope, which is what decides whether a schema resource
	// contributes a frame at all. It is discovered while rendering rather than
	// before it, because a reference only becomes dynamic once its target is
	// known -- so a name learned late sets restart and the whole literal is
	// built again with the frames it now needs.
	dynAnchors map[string]bool
	restart    bool
}

// reset clears the per-round rendering state, keeping everything the rounds
// accumulate: the hoisted set, the anchor names, and whether a pattern was seen.
func (b *nodeBuilder) reset() {
	b.stack = map[*schema.Schema]int{}
	b.descents = 0
	b.depth = 0
	b.nodes = 0
	// Nothing has been entered at the start of a literal, so the first node
	// publishes its own resource -- which is right for the root and right for a
	// hoisted node, since a reference reaching one enters whatever resource it
	// belongs to.
	b.resource = nil
}

// build renders s, together with every schema hoisted out of it.
//
// It runs to a fixpoint because the two things a round can discover are both
// invisible until something has been rendered: a cycle is only a cycle once the
// schema closing it has been reached, and a schema resource only has to publish
// its anchors once a reference below it turns out to need them. Either
// discovery invalidates what was rendered before it, so the round is thrown away
// and run again against the larger set. Both sets grow monotonically, so the
// loop terminates; maxRuntimeRounds is there for the schema that would take too
// long proving it.
func (b *nodeBuilder) build(s *schema.Schema) (string, []RuntimeNodeVar, bool) {
	for round := 0; round < maxRuntimeRounds; round++ {
		b.restart = false
		b.reset()
		root, ok := b.literal(s, 0)
		if !ok {
			return "", nil, false
		}
		// hoistOrder may grow while this loop runs, and the entries added are
		// rendered by the same loop; a growth also sets restart, so the round
		// they were found in is discarded and they are rendered again against
		// the complete set.
		var vars []RuntimeNodeVar
		for i := 0; i < len(b.hoistOrder); i++ {
			if b.overBudget() {
				return "", nil, false
			}
			target := b.hoistOrder[i]
			b.reset()
			b.rendering = target
			lit, ok := b.literal(target, 1)
			b.rendering = nil
			if !ok {
				return "", nil, false
			}
			vars = append(vars, RuntimeNodeVar{Name: b.hoisted[target], Literal: lit})
		}
		if !b.restart {
			return root, vars, true
		}
	}
	return "", nil, false
}

// hoistRef records that s needs a variable of its own and returns the node that
// refers to it.
func (b *nodeBuilder) hoistRef(s *schema.Schema) string {
	name, ok := b.hoisted[s]
	if !ok {
		if b.hoisted == nil {
			b.hoisted = map[*schema.Schema]string{}
		}
		name = fmt.Sprintf("%s%d", b.hoistPrefix, len(b.hoistOrder)+1)
		b.hoisted[s] = name
		b.hoistOrder = append(b.hoistOrder, s)
		b.restart = true
	}
	return "_schemaNode{Ref: &" + name + "}"
}

// overBudget reports whether the fixpoint has grown past what it is willing to
// spend. Each hoisted node is a whole literal of its own and a round re-renders
// all of them, so the work is the product of the two counts -- and both are
// bounded here rather than left to the schema. A schema that hits it is refused,
// which is the same answer as any other thing the evaluator cannot carry.
func (b *nodeBuilder) overBudget() bool { return len(b.hoistOrder) > maxRuntimeHoists }

// needAnchor records that the dynamic scope has to carry a name, so that the
// schema resources declaring it publish it on the way past.
func (b *nodeBuilder) needAnchor(name string) {
	if b.dynAnchors[name] {
		return
	}
	if b.dynAnchors == nil {
		b.dynAnchors = map[string]bool{}
	}
	b.dynAnchors[name] = true
	b.restart = true
}

// sub renders a subschema that applies to a *different* value than its parent --
// an item, a property, a key. Crossing one of these is what makes a reference
// cycle terminate on a finite document, and hoistRef is only reached through
// having crossed one.
func (b *nodeBuilder) sub(s *schema.Schema, indent int) (string, bool) {
	b.descents++
	lit, ok := b.literal(s, indent)
	b.descents--
	return lit, ok
}

func (b *nodeBuilder) subList(subs []*schema.Schema, indent int) (string, bool) {
	b.descents++
	lit, ok := b.list(subs, indent)
	b.descents--
	return lit, ok
}

func (b *nodeBuilder) subMemberList(members map[string]*schema.Schema, indent int) (string, bool) {
	b.descents++
	lit, ok := b.memberList(members, indent)
	b.descents--
	return lit, ok
}

// resolve looks up a schema's $ref without recording the outcome.
//
// This is a probe, not a use: a reference that no resolver can serve costs the
// caller nothing but the compiled form, and it falls back to what it would have
// emitted anyway. Recording it would make an optimistic look here decide whether
// Generate reports an unresolved-reference error, which is the business of the
// paths that need the reference to produce a type.
func (b *nodeBuilder) resolve(s *schema.Schema) *schema.Schema {
	return b.g.resolveRefInContextUncounted(s.Ref, s)
}

func (b *nodeBuilder) literal(s *schema.Schema, indent int) (string, bool) {
	if s == nil {
		return "", false
	}
	b.nodes++
	if b.nodes > maxRuntimeNodes || b.depth >= maxRuntimeDepth {
		return "", false
	}
	// A schema already given a variable of its own is referred to, not written
	// out again -- except in the one place its body is being produced.
	if name, ok := b.hoisted[s]; ok && s != b.rendering {
		return "_schemaNode{Ref: &" + name + "}", true
	}
	if at, on := b.stack[s]; on {
		// A reference cycle. Where it closes without the value having got any
		// smaller, inlining cannot terminate and neither could evaluation, so
		// the schema is refused; enforcing the part above the cycle would be a
		// different schema. Where the value descends on the way round, a finite
		// document ends the recursion and the cycle becomes a node variable
		// pointing back at itself.
		if b.hoistPrefix == "" || b.descents <= at {
			return "", false
		}
		return b.hoistRef(s), true
	}
	if b.stack == nil {
		b.stack = map[*schema.Schema]int{}
	}
	b.stack[s] = b.descents
	b.depth++
	// Which schema resource this node belongs to, which is what decides whether
	// arriving at it *enters* one. A reference into the middle of a resource
	// enters it exactly as arriving at its root does -- the dynamic scope is the
	// resources entered, not the roots landed on -- so the resource is tracked
	// rather than the root recognised.
	enclosing := b.resource
	entered := s.DocumentRoot != nil && s.DocumentRoot != enclosing
	if entered {
		b.resource = s.DocumentRoot
	}
	defer func() {
		delete(b.stack, s)
		b.depth--
		b.resource = enclosing
	}()

	pad := strings.Repeat("\t", indent)
	inner := strings.Repeat("\t", indent+1)

	if s.IsBooleanSchema() {
		return fmt.Sprintf("_schemaNode{Boolean: _boolPtr(%t)}", s.IsTrueSchema()), true
	}

	// `"enum": []` admits nothing, so it compiles to the node the boolean
	// `false` schema compiles to on the line above. It has to be answered before
	// the keyword emission below, which asks len(s.Enum) > 0 and so left the
	// empty list out: the node came back `_schemaNode{}`, which admits
	// everything, and a `false` branch inside a oneOf turned into a matching one.
	// Conditioned on the validation vocabulary, since without it `enum` asserts
	// nothing at all. See emptyEnumSchema and schemaForbidsEveryValue.
	if b.g != nil && b.g.schemaForbidsEveryValue(s) {
		return "_schemaNode{Boolean: _boolPtr(false)}", true
	}

	// Before draft 2019-09 a $ref replaces the schema object it sits in, so the
	// siblings say nothing and must not be read -- neither to enforce them nor
	// to refuse the schema for carrying one the evaluator does not model.
	if b.inlineRefs && s.Ref != "" && b.g.refOverridesSiblingsForSchema(s) {
		resolved := b.resolve(s)
		if resolved == nil {
			return "", false
		}
		return b.literal(resolved, indent)
	}

	if !b.keywordsOnly(s) {
		return "", false
	}

	var fields []string
	add := func(f string) { fields = append(fields, inner+f) }

	if len(s.Type) > 0 {
		add(fmt.Sprintf("Type: %s,", goStringSliceLiteral([]string(s.Type))))
	}
	if s.Const != nil {
		raw, err := json.Marshal(*s.Const)
		if err != nil {
			return "", false
		}
		add(fmt.Sprintf("Const: _strPtr(%q),", string(raw)))
	} else if s.ConstIsNull {
		add(`Const: _strPtr("null"),`)
	}
	if len(s.Enum) > 0 {
		encoded := make([]string, 0, len(s.Enum))
		for _, value := range s.Enum {
			raw, err := json.Marshal(value)
			if err != nil {
				return "", false
			}
			encoded = append(encoded, string(raw))
		}
		add(fmt.Sprintf("Enum: %s,", goStringSliceLiteral(encoded)))
	}

	// Normalize copies draft 3's divisibleBy onto multipleOf, but only when
	// multipleOf is absent. A schema carrying both with different arguments is
	// not legal in any draft, and reading only one of them would enforce a
	// schema nobody wrote.
	if s.DivisibleBy != nil && s.MultipleOf != nil && *s.DivisibleBy != *s.MultipleOf {
		return "", false
	}
	if s.MultipleOf != nil {
		add(fmt.Sprintf("MultipleOf: _floatPtr(%s),", formatFloatLiteral(*s.MultipleOf)))
	}
	minimum, maximum, exclusiveMin, exclusiveMax := numericBounds(s)
	if minimum != nil {
		add(fmt.Sprintf("Minimum: _floatPtr(%s),", formatFloatLiteral(*minimum)))
	}
	if maximum != nil {
		add(fmt.Sprintf("Maximum: _floatPtr(%s),", formatFloatLiteral(*maximum)))
	}
	if exclusiveMin != nil {
		add(fmt.Sprintf("ExclusiveMinimum: _floatPtr(%s),", formatFloatLiteral(*exclusiveMin)))
	}
	if exclusiveMax != nil {
		add(fmt.Sprintf("ExclusiveMaximum: _floatPtr(%s),", formatFloatLiteral(*exclusiveMax)))
	}

	if s.MinLength != nil {
		add(fmt.Sprintf("MinLength: _intPtr(%d),", s.MinLength.Int()))
	}
	if s.MaxLength != nil {
		add(fmt.Sprintf("MaxLength: _intPtr(%d),", s.MaxLength.Int()))
	}
	if s.Pattern != nil {
		add(fmt.Sprintf("Pattern: _strPtr(%q),", *s.Pattern))
		b.usesPattern = true
	}

	if s.MinItems != nil {
		add(fmt.Sprintf("MinItems: _intPtr(%d),", s.MinItems.Int()))
	}
	if s.MaxItems != nil {
		add(fmt.Sprintf("MaxItems: _intPtr(%d),", s.MaxItems.Int()))
	}
	if s.UniqueItems != nil && *s.UniqueItems {
		add("UniqueItems: true,")
	}

	// prefixItems, or the pre-2020 tuple form of items.
	var tuple []*schema.Schema
	if b.g.supportsPrefixItems(s) {
		tuple = s.PrefixItems
	}
	var itemsSchema *schema.Schema
	if s.Items != nil {
		if len(s.Items.Schemas) > 0 {
			if len(tuple) > 0 {
				return "", false // both forms at once is not modeled
			}
			tuple = s.Items.Schemas
		} else if s.Items.Schema != nil {
			itemsSchema = s.Items.Schema
		}
	}
	if len(tuple) > 0 {
		list, ok := b.subList(tuple, indent+2)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("PrefixItems: %s,", list))
	}
	if itemsSchema != nil {
		lit, ok := b.sub(itemsSchema, indent+1)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("Items: _node(%s),", lit))
	}
	// additionalItems applies only alongside a tuple. On its own it is ignored:
	// it evaluates nothing and constrains nothing, so mapping it onto items
	// would both validate elements it must not and mark them evaluated.
	if s.AdditionalItems != nil && len(tuple) > 0 {
		if itemsSchema != nil {
			return "", false // items already claims the positions past the tuple
		}
		additional := s.AdditionalItems.AsSchema()
		if additional == nil {
			return "", false
		}
		lit, ok := b.sub(additional, indent+1)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("Items: _node(%s),", lit))
	}

	if s.Contains != nil {
		lit, ok := b.sub(s.Contains, indent+1)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("Contains: _node(%s),", lit))
		if s.MinContains != nil {
			add(fmt.Sprintf("MinContains: _intPtr(%d),", s.MinContains.Int()))
		}
		if s.MaxContains != nil {
			add(fmt.Sprintf("MaxContains: _intPtr(%d),", s.MaxContains.Int()))
		}
	}

	if len(s.Required) > 0 {
		add(fmt.Sprintf("Required: %s,", goStringSliceLiteral([]string(s.Required))))
	}
	if s.MinProperties != nil {
		add(fmt.Sprintf("MinProperties: _intPtr(%d),", s.MinProperties.Int()))
	}
	if s.MaxProperties != nil {
		add(fmt.Sprintf("MaxProperties: _intPtr(%d),", s.MaxProperties.Int()))
	}
	// dependentSchemas is not in this group, and the difference is the one sub
	// draws: its branches apply to the object itself, where a property's or a
	// pattern's apply to a member of it.
	for _, group := range []struct {
		name    string
		members map[string]*schema.Schema
	}{{"Properties", s.Properties}, {"PatternProperties", s.PatternProperties}} {
		if len(group.members) == 0 {
			continue
		}
		list, ok := b.subMemberList(group.members, indent+2)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("%s: %s,", group.name, list))
		if group.name == "PatternProperties" {
			b.usesPattern = true
		}
	}
	if len(s.DependentSchemas) > 0 {
		list, ok := b.memberList(s.DependentSchemas, indent+2)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("DependentSchemas: %s,", list))
	}
	if s.AdditionalProperties != nil {
		additional := s.AdditionalProperties.AsSchema()
		if additional == nil {
			return "", false
		}
		lit, ok := b.sub(additional, indent+1)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("AdditionalProperties: _node(%s),", lit))
		if len(s.PatternProperties) > 0 {
			// additionalProperties skips whatever patternProperties claimed, so
			// the evaluator has to run the patterns to know what is left.
			b.usesPattern = true
		}
	}
	if len(s.DependentRequired) > 0 {
		add(fmt.Sprintf("DependentRequired: %s,", dependentRequiredLiteral(s.DependentRequired, indent+2)))
	}

	// A $ref that is an applicator rather than a replacement (draft 2019-09 and
	// later) is another conjunct, which is exactly what allOf means.
	allOf := s.AllOf
	if b.inlineRefs && s.Ref != "" {
		resolved := b.resolve(s)
		if resolved == nil {
			return "", false
		}
		allOf = append(append([]*schema.Schema(nil), allOf...), resolved)
	}

	// $recursiveRef and $dynamicRef exist only in drafts where a reference is an
	// applicator, so they are conjuncts like the $ref above -- with the one
	// difference that where they lead may not be decidable here at all.
	if s.RecursiveRef != "" || s.DynamicRef != "" {
		if !b.inlineRefs {
			return "", false
		}
		target, anchor, dynamic, ok := b.g.dynamicRefTarget(s)
		if !ok {
			return "", false
		}
		if !dynamic {
			// Nothing declares the anchor this reference would have searched
			// for, so it is a $ref by a longer name and is inlined like one.
			allOf = append(append([]*schema.Schema(nil), allOf...), target)
		} else {
			lit, ok := b.dynamicRefLiteral(s, target, anchor, indent+1)
			if !ok {
				return "", false
			}
			add(lit)
		}
	}

	// A schema resource publishes its anchors for as long as it is being
	// evaluated, which is what a reference below it searches. Only the names
	// some reference actually searches for are published: an anchor nothing
	// looks up costs a frame and changes no answer.
	if len(b.dynAnchors) > 0 {
		if s.DocumentRoot == nil {
			// Which resource this node belongs to was never computed, so whether
			// it publishes anything is unknown -- and an unpublished anchor is
			// not an anchor that changes nothing. It hands the reference to
			// whatever resource is further out, which is a different schema and
			// can reject a document this one allows.
			return "", false
		}
		if entered {
			anchors, ok := b.dynamicAnchorsLiteral(s, s.DocumentRoot, indent+2)
			if !ok {
				return "", false
			}
			if anchors != "" {
				add(fmt.Sprintf("DynamicAnchors: %s,", anchors))
			}
		}
	}
	for _, group := range []struct {
		name string
		subs []*schema.Schema
	}{{"AllOf", allOf}, {"AnyOf", s.AnyOf}, {"OneOf", s.OneOf}} {
		if len(group.subs) == 0 {
			continue
		}
		list, ok := b.list(group.subs, indent+2)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("%s: %s,", group.name, list))
	}

	for _, branch := range []struct {
		name string
		sub  *schema.Schema
	}{{"Not", s.Not}, {"If", s.If}, {"Then", s.Then}, {"Else", s.Else}} {
		if branch.sub == nil {
			continue
		}
		lit, ok := b.literal(branch.sub, indent+1)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("%s: _node(%s),", branch.name, lit))
	}
	// propertyNames judges a key rather than the object, and the unevaluated
	// pair judges what is left of an item or a member, so all three descend.
	for _, branch := range []struct {
		name string
		sub  *schema.Schema
	}{{"PropertyNames", s.PropertyNames}, {"UnevaluatedItems", s.UnevaluatedItems},
		{"UnevaluatedProperties", s.UnevaluatedProperties}} {
		if branch.sub == nil {
			continue
		}
		lit, ok := b.sub(branch.sub, indent+1)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("%s: _node(%s),", branch.name, lit))
	}

	if len(fields) == 0 {
		return "_schemaNode{}", true
	}
	sort.Strings(fields)
	return "_schemaNode{\n" + strings.Join(fields, "\n") + "\n" + pad + "}", true
}

// dynamicRefLiteral emits the field for a $recursiveRef or $dynamicRef whose
// target the dynamic scope decides.
//
// The fallback is compiled too, and it is not decoration: the scope is searched
// from the outermost resource in, and a document whose path enters no resource
// declaring the anchor leaves the reference meaning what it statically says.
//
// A reference that could arrive back at itself with the same value in hand is
// refused rather than compiled, because that is a generated program that does
// not finish. The builder's own cycle check cannot see it -- the loop is closed
// by a target chosen per document, not by an edge in the tree -- so it is asked
// separately. See dynamicRefCanLoop.
func (b *nodeBuilder) dynamicRefLiteral(s, target *schema.Schema, anchor string, indent int) (string, bool) {
	if b.hoistPrefix == "" {
		// A dynamic reference resolves to a schema resource, which is a node
		// somewhere else in the tree, and the frames that carry those are only
		// emitted where package-level variables can be. See hoistPrefix.
		return "", false
	}
	if b.g.dynamicRefCanLoop(s, target, anchor) {
		return "", false
	}
	b.needAnchor(anchor)
	fallback, ok := b.literal(target, indent+1)
	if !ok {
		return "", false
	}
	pad := strings.Repeat("\t", indent)
	inner := strings.Repeat("\t", indent+1)
	return fmt.Sprintf("DynamicRef: &_dynamicRef{\n%sAnchor: %q,\n%sFallback: _node(%s),\n%s},",
		inner, anchor, inner, fallback, pad), true
}

// dynamicAnchorsLiteral emits the anchors a schema resource contributes to the
// dynamic scope, or "" when it declares none of the names in play.
//
// An anchor on the node the frame hangs off is emitted with no node of its own.
// That is not a saving: it is the only way to express it. Such an anchor names
// the very node that carries it, and Go rejects a cycle between package-level
// variable initialisers, so a node holding a pointer to itself cannot be
// written. The evaluator reads a nil node as "the node whose frame this is".
//
// entry and resource are two schemas and not one, because a reference into the
// middle of a resource enters it: the frame is published on the node the
// reference lands on, while the anchors it publishes are the resource's. So a
// $recursiveAnchor -- which always names the resource root -- gets the nil
// spelling when the reference landed on that root and an ordinary node when it
// did not, and the two cases resolve to the same schema either way. Reading the
// nil spelling as "the resource root" instead would be wrong for the same reason
// in reverse, since the root is not what a mid-resource frame hangs off.
func (b *nodeBuilder) dynamicAnchorsLiteral(entry, resource *schema.Schema, indent int) (string, bool) {
	names := make([]string, 0, len(b.dynAnchors))
	for name := range b.dynAnchors {
		names = append(names, name)
	}
	sort.Strings(names)

	pad := strings.Repeat("\t", indent)
	closePad := strings.Repeat("\t", indent-1)
	var parts []string
	for _, name := range names {
		declared := resourceDynamicAnchor(resource, name)
		if declared == nil {
			continue
		}
		if declared == entry {
			parts = append(parts, fmt.Sprintf("%s{Name: %q},", pad, name))
			continue
		}
		lit, ok := b.literal(declared, indent+1)
		if !ok {
			return "", false
		}
		parts = append(parts, fmt.Sprintf("%s{Name: %q, Node: _node(%s)},", pad, name, lit))
	}
	if len(parts) == 0 {
		return "", true
	}
	return "[]_schemaAnchor{\n" + strings.Join(parts, "\n") + "\n" + closePad + "}", true
}

func (b *nodeBuilder) list(subs []*schema.Schema, indent int) (string, bool) {
	pad := strings.Repeat("\t", indent)
	closePad := strings.Repeat("\t", indent-1)
	parts := make([]string, 0, len(subs))
	for _, sub := range subs {
		lit, ok := b.literal(sub, indent)
		if !ok {
			return "", false
		}
		parts = append(parts, pad+lit+",")
	}
	return "[]_schemaNode{\n" + strings.Join(parts, "\n") + "\n" + closePad + "}", true
}

// memberList renders a keyword whose argument maps names or patterns to
// schemas. Keys are emitted in sorted order: the map has none of its own, and a
// generated file that changed between runs of the same input would be unusable.
func (b *nodeBuilder) memberList(members map[string]*schema.Schema, indent int) (string, bool) {
	pad := strings.Repeat("\t", indent)
	closePad := strings.Repeat("\t", indent-1)
	parts := make([]string, 0, len(members))
	for _, key := range sortedKeys(members) {
		lit, ok := b.literal(members[key], indent+1)
		if !ok {
			return "", false
		}
		parts = append(parts, fmt.Sprintf("%s{Key: %q, Node: %s},", pad, key, lit))
	}
	return "[]_schemaMember{\n" + strings.Join(parts, "\n") + "\n" + closePad + "}", true
}

func dependentRequiredLiteral(deps map[string][]string, indent int) string {
	pad := strings.Repeat("\t", indent)
	closePad := strings.Repeat("\t", indent-1)
	keys := make([]string, 0, len(deps))
	for key := range deps {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s{Key: %q, Keys: %s},", pad, key, goStringSliceLiteral(deps[key])))
	}
	return "[]_schemaDependency{\n" + strings.Join(parts, "\n") + "\n" + closePad + "}"
}

func goStringSliceLiteral(values []string) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, strconv.Quote(v))
	}
	return "[]string{" + strings.Join(parts, ", ") + "}"
}

// numericBounds reads the four numeric bounds, resolving draft 4's boolean
// exclusiveMinimum/exclusiveMaximum into the modern numeric form.
//
// In draft 4 the boolean does not stand on its own: it modifies the sibling
// `minimum`/`maximum`, so {"minimum":3,"exclusiveMinimum":true} means "> 3" and
// the sibling must not also be enforced as ">= 3". Written without that sibling
// it constrains nothing at all, and is dropped rather than guessed at.
func numericBounds(s *schema.Schema) (minimum, maximum, exclusiveMin, exclusiveMax *float64) {
	minimum, maximum = s.Minimum, s.Maximum
	if s.ExclusiveMinimum != nil {
		switch {
		case s.ExclusiveMinimum.Number != nil:
			exclusiveMin = s.ExclusiveMinimum.Number
		case s.ExclusiveMinimum.Bool != nil && *s.ExclusiveMinimum.Bool:
			exclusiveMin, minimum = minimum, nil
		}
	}
	if s.ExclusiveMaximum != nil {
		switch {
		case s.ExclusiveMaximum.Number != nil:
			exclusiveMax = s.ExclusiveMaximum.Number
		case s.ExclusiveMaximum.Bool != nil && *s.ExclusiveMaximum.Bool:
			exclusiveMax, maximum = maximum, nil
		}
	}
	return minimum, maximum, exclusiveMin, exclusiveMax
}

// keywordsOnly gates on the keywords actually present, read from the re-marshaled
// schema, so a keyword the parser learns later fails closed.
//
// Draft 3's schema-valued `type` entries are refused separately: they do not
// survive re-marshaling -- only the primitive names in the array do -- so a
// schema written as {"type":[{"minimum":1},"string"]} would be read as
// {"type":"string"} and reject a number the schema allows.
func (b *nodeBuilder) keywordsOnly(s *schema.Schema) bool {
	if len(s.TypeSchemas) > 0 {
		return false
	}
	// An unknown keyword refuses the schema, because nothing is known about
	// what it demands -- except for the handful that are known to demand
	// nothing. Those have no field on Schema, so they arrive as extensions and
	// would otherwise cost a schema its checks for carrying a comment.
	for key := range s.Extensions {
		if !inertKeywords[key] {
			return false
		}
	}
	present, ok := schemaKeywordSet(s)
	if !ok {
		return false
	}
	for key := range present {
		if !b.allowed[key] {
			return false
		}
	}
	return true
}

// needsRuntimeAnnotations reports whether a schema's unevaluatedItems depends on
// which items sibling applicators evaluated for the actual value.
//
// Static analysis handles unevaluatedItems next to a fixed tuple. It cannot
// handle it next to in-place applicators, because the evaluated set then
// depends on which branches match the value in hand. Only that case is routed
// to the evaluator, so schemas that already work keep their generated shape.
func needsRuntimeAnnotations(s *schema.Schema) bool {
	if s == nil || s.UnevaluatedItems == nil {
		return false
	}
	if len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 || s.If != nil {
		return true
	}
	return false
}

// hasCousinUnevaluatedItems reports whether any in-place applicator subschema
// carries its own unevaluatedItems. Those are cousins of their siblings and see
// no annotations from them, which static merging gets wrong.
//
// The allOf branches are searched as well as the schema's own applicators, which
// is the reach collectRuntimeBranchChecks takes for unevaluatedProperties and
// the reason #135 asked about this keyword too. Every allOf branch binds, so an
// applicator inside one is an applicator of this schema by another spelling, and
// {"type":"array","allOf":[{"anyOf":[{"prefixItems":[{"type":"integer"}],"unevaluatedItems":false}]}]}
// accepted [1,2] while the identical anyOf written directly has rejected it
// since the cousin case was recognised. A branch reached through a $ref is
// resolved, so the two spellings of the same branch answer alike.
func (g *Generator) hasCousinUnevaluatedItems(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	if statesCousinUnevaluatedItems(s) {
		return true
	}
	for _, sub := range s.AllOf {
		if statesCousinUnevaluatedItems(g.resolveSchemaForApplicator(sub)) {
			return true
		}
	}
	return false
}

// statesCousinUnevaluatedItems is hasCousinUnevaluatedItems for one schema
// object: does any direct branch of its own in-place applicators state the
// keyword.
func statesCousinUnevaluatedItems(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	groups := [][]*schema.Schema{s.AllOf, s.AnyOf, s.OneOf}
	for _, g := range groups {
		for _, sub := range g {
			if sub != nil && sub.UnevaluatedItems != nil {
				return true
			}
		}
	}
	return false
}

func formatFloatLiteral(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// annotationSchemaDef builds an AnnotationSchemaDef when a schema's
// unevaluatedItems depends on runtime annotations and the whole subtree fits the
// evaluator's model. Returns nil to keep the existing static handling.
func (g *Generator) annotationSchemaDef(name string, s *schema.Schema) *AnnotationSchemaDef {
	if !g.validationKeywordsEnabled() {
		return nil
	}
	if !needsRuntimeAnnotations(s) && !g.hasCousinUnevaluatedItems(s) {
		return nil
	}
	b := &nodeBuilder{g: g, allowed: annotationKeywords, stack: map[*schema.Schema]int{}}
	lit, ok := b.literal(s, 0)
	if !ok {
		return nil
	}
	return &AnnotationSchemaDef{Name: name, Description: s.Description, Annotations: annotationsOf(s), NodeLiteral: lit, NeedsPattern: b.usesPattern}
}

// dynamicScopeSchemaDef compiles a schema whose bookended dynamic reference has
// no single answer, and returns nil for every other schema.
//
// It is the second of the two arms that claim a schema *before* the static ones,
// and it is there for the same reason the first is: the constraint it carries
// cannot be decided at generation time, so an arm that produced a Go type from
// it would be answering a question the schema does not settle. Where a
// $dynamicRef's anchor is declared twice among the schemas this one reaches, its
// target is a property of the instance, and one binding chosen for the generated
// type is right for the documents that take one path and wrong for the rest --
// which is what left `{"numbers":{"list":["foo"]}}` accepted by a schema saying
// that list holds numbers (issue #160).
//
// The narrowing is dynamicScopeDecidesTheTarget's, and it is what keeps this
// from claiming every schema that mentions the two keywords. A reference whose
// anchor has one declaration in reach means the same thing down every path, so
// the static resolution is already the dynamic one and the struct or named type
// it produces is untouched. Only the schema whose answer moves is compiled, and
// it is compiled whole, because the scope a reference resolves against is built
// by the resources entered on the way to it and no check local to the keyword
// can see the frames its callers pushed.
func (g *Generator) dynamicScopeSchemaDef(name string, s *schema.Schema) *AnnotationSchemaDef {
	if !g.validationKeywordsEnabled() {
		return nil
	}
	if !g.dynamicScopeDecidesTheTarget(s) {
		return nil
	}
	return g.runtimeSchemaDef(name, s)
}

// rawWrapperDef is what every arm that was about to emit `type X any` asks
// first: a definition that keeps the schema enforceable, or nil if there is
// genuinely nothing to enforce.
//
// The two attempts are ordered by how specific they are. dynamicSchemaDef
// compiles a root composition into inline boolean expressions and produces the
// smaller, more readable code, so it goes first and the shapes it already
// handled are unchanged. runtimeSchemaDef takes everything else it can read.
//
// `type X any` is the answer only when both decline, and then it is an answer
// rather than a shrug: Go forbids methods on a type whose underlying type is an
// interface, so a schema that lands there has no Validate at all and
// json.Unmarshal into it cannot fail. That is the right type for a schema which
// constrains nothing, and a silent lie for any other.
func (g *Generator) rawWrapperDef(name string, s *schema.Schema) TypeDef {
	if def := g.dynamicSchemaDef(name, s); def != nil {
		return def
	}
	if def := g.runtimeSchemaDef(name, s); def != nil {
		return def
	}
	return nil
}

// constraintOnlyWrapperDef is rawWrapperDef's answer for a position that is not
// being given a name of its own -- an array element, a map value, a tuple slot,
// a property whose schema names no type.
//
// It is the same ladder generateTypeDef walks once every type-producing arm has
// declined, and in the same order: the `not` wrapper, then the type-union
// wrapper, then the two raw-JSON wrappers. Walking that ladder rather than a
// second one of its own is the point -- a schema written inline gets the type it
// would have got from a $defs entry and a $ref, instead of a different answer
// depending on where it was written.
//
// nil means the position keeps whatever type it had. That covers the schema that
// constrains nothing, which is what `any` describes, and the schema the evaluator
// cannot model, which must not gain a Validate that checks less than it says.
func (g *Generator) constraintOnlyWrapperDef(name string, s *schema.Schema) TypeDef {
	if s == nil || name == "" || !g.validationKeywordsEnabled() {
		return nil
	}
	if g.acceptsEveryValue(s, 0, map[*schema.Schema]bool{}) {
		return nil
	}
	if def := g.extractNotSchemaDef(name, s); def != nil {
		return def
	}
	if def := g.extractTypeOnlySchemaDef(name, s); def != nil {
		return def
	}
	if def := g.rawWrapperDef(name, s); def != nil {
		return def
	}
	return nil
}

// acceptsEveryValue reports whether a schema forbids nothing, so that a wrapper
// built for it would carry a Validate that can never fail.
//
// unownedNodeLiterals answers the same question off the compiled literal, but
// only for a schema that reduces to a bare node. It cannot see that
// {"allOf":[{"$ref":"#/$defs/always"}]} -- a composition whose every branch is
// the `true` schema -- says exactly as much as {} does, so the position would
// trade a plain `any` for a struct wrapping raw JSON and gain no check for it.
// That is a worse type for the caller and no better a check, which is the one
// thing the constraint-only arm must never do.
//
// The composition keywords are read for what they assert here, not for what
// they usually assert. An `allOf` or an `anyOf` over branches that each accept
// everything accepts everything. A `oneOf` does not: two branches that both
// accept everything both match, and "exactly one" then holds for no value at
// all, so only a single-branch oneOf can pass. Everything else answers false,
// which builds the wrapper -- the safe direction, since a wrapper for a schema
// that turns out to constrain nothing costs a type and never a document.
func (g *Generator) acceptsEveryValue(s *schema.Schema, depth int, onPath map[*schema.Schema]bool) bool {
	if s == nil {
		return true
	}
	// A reference cycle, or a schema deeper than the evaluator would compile.
	// Neither can be answered here, and false is the answer that keeps the check.
	if depth > maxRuntimeDepth || onPath[s] {
		return false
	}
	if s.IsBooleanSchema() {
		return s.IsTrueSchema()
	}
	onPath[s] = true
	defer delete(onPath, s)

	// unenforcedKeywords is the list of keywords present that state a constraint,
	// read from the re-marshaled schema, so a keyword the parser learns later is
	// counted rather than missed.
	for _, key := range unenforcedKeywords(s) {
		switch key {
		case "allOf", "anyOf", "oneOf", "$ref":
		default:
			return false
		}
	}
	for _, sub := range s.AllOf {
		if !g.acceptsEveryValue(sub, depth+1, onPath) {
			return false
		}
	}
	for _, sub := range s.AnyOf {
		if !g.acceptsEveryValue(sub, depth+1, onPath) {
			return false
		}
	}
	if len(s.OneOf) > 1 {
		return false
	}
	for _, sub := range s.OneOf {
		if !g.acceptsEveryValue(sub, depth+1, onPath) {
			return false
		}
	}
	if s.Ref != "" {
		resolved := g.resolveRefInContextUncounted(s.Ref, s)
		return resolved != nil && g.acceptsEveryValue(resolved, depth+1, onPath)
	}
	return true
}

// unenforcedAliasDef builds the `any` alias, and says in the generated source
// which keywords it is dropping when there are any.
//
// A schema that constrains nothing is exactly what `any` describes, and gets no
// comment. A schema that constrains something and still ends up here is a
// schema schemagen could not compile, and the caller has no way to find that
// out from the type: it has no Validate method to be missing, and unmarshalling
// into it always succeeds. The comment is the only place that fact can be
// stated where somebody will read it.
func (g *Generator) unenforcedAliasDef(name string, s *schema.Schema) *AliasDef {
	dropped := unenforcedKeywords(s)
	def := &AliasDef{
		Name:        name,
		Underlying:  &PrimitiveType{Name: "any"},
		Description: s.Description,
		Annotations: annotationsOf(s),
	}
	if len(dropped) > 0 {
		def.Unenforced = strings.Join(dropped, ", ")
		g.unenforced = append(g.unenforced, UnenforcedSchema{TypeName: name, Keywords: dropped})
	}
	return def
}

// schemaKeywordSet returns the keywords a schema states. It is the reading every
// gate in this package that decides "can this schema be represented" and "does
// this schema state anything" is built on.
//
// The re-marshaled key set is the base, and that is deliberate: a keyword the
// parser learns later arrives with a struct field that marshals, so it lands in
// the set and the gate reading it fails closed. A hand-written list of struct
// fields has the opposite default -- the field nobody remembered is missed
// silently -- which is why the gates were written this way and why they stay
// this way.
//
// What the base cannot do is show a field whose *presence* its encoding erases,
// and Schema.KeywordsMarshaledFormOmits is the one place that knows which those
// are. Every gate reads both, because a gate that forgets the difference has each
// time turned into a defect of its own: `{"enum":[]}` read as a schema stating
// nothing (#142, and the local patch dynamicBranchChecks carried until this
// existed), and `{"const":null}` read the same way at three gates at once, which
// is issue #154. Sharing the reading is the point -- a per-gate patch is what has
// to be remembered five times and was remembered once.
//
// The second result is false when the schema cannot be marshaled. No gate may
// read that as "states nothing": the set is unknown, not empty.
func schemaKeywordSet(s *schema.Schema) (map[string]bool, bool) {
	if s == nil {
		return nil, false
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, false
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
		return nil, false
	}
	hidden := s.KeywordsMarshaledFormOmits()
	seen := make(map[string]bool, len(present)+len(hidden))
	for key := range present {
		seen[key] = true
	}
	for _, key := range hidden {
		seen[key] = true
	}
	return seen, true
}

// unenforcedKeywords lists the keywords on a schema that state a constraint,
// in the order they would be read.
//
// Everything that constrains nothing is left out: the identifiers, the
// annotations, and the definition containers, which hold schemas that apply
// only where something refers to them. What is left is the part of the schema a
// bare `any` throws away.
func unenforcedKeywords(s *schema.Schema) []string {
	if s == nil {
		return nil
	}
	seen, ok := schemaKeywordSet(s)
	if !ok {
		return nil
	}
	for key := range s.Extensions {
		seen[key] = true
	}
	// A keyword whose partner is absent asserts nothing, so listing it would be
	// a false alarm -- and a diagnostic that cries wolf gets ignored, which
	// would cost the ones that are real.
	if s.Contains == nil {
		delete(seen, "minContains")
		delete(seen, "maxContains")
	}
	if s.If == nil {
		delete(seen, "then")
		delete(seen, "else")
	}
	if len(s.PrefixItems) == 0 && (s.Items == nil || len(s.Items.Schemas) == 0) {
		delete(seen, "additionalItems")
	}

	var dropped []string
	for key := range seen {
		if nonConstrainingKeywords[key] || inertKeywords[key] {
			continue
		}
		dropped = append(dropped, key)
	}
	sort.Strings(dropped)
	return dropped
}

// nonConstrainingKeywords are the keywords whose absence from a generated check
// costs a document nothing: they identify a schema, describe it, or hold
// subschemas that apply only where a reference reaches them.
//
// Together with inertKeywords -- the same idea for the keywords that have no
// field on schema.Schema and so arrive through Extensions -- this is the single
// place the package decides that a keyword constrains nothing. Both are read by
// unenforcedKeywords, which says what an `any` alias is dropping, and by
// allowing(), which builds the two evaluator allow-lists. That sharing is
// load-bearing: when the allow-lists carried their own copies they drifted, and
// an unused $defs stopped unevaluatedItems being enforced at all (#178). A
// keyword added here reaches every one of those readers at once.
var nonConstrainingKeywords = map[string]bool{
	"$schema": true, "$id": true, "id": true, "$vocabulary": true,
	"$anchor": true, "$dynamicAnchor": true, "$recursiveAnchor": true,
	"$defs": true, "definitions": true,
	"title": true, "description": true, "default": true,
	// The rest of the annotation vocabulary. They describe a position rather
	// than constraining it, which puts them beside "title" and "description"
	// here -- and they need saying here rather than in inertKeywords because
	// they have fields on schema.Schema, so schemaKeywordSet reads them off the
	// marshaled form instead of out of Extensions. "examples" has no field, by
	// design, so it stays on the Extensions side and inertKeywords covers it.
	"deprecated": true, "readOnly": true, "writeOnly": true,
}

// runtimeSchemaDef compiles a whole schema to the runtime evaluator, for the
// positions where the alternative is no validation at all.
//
// Every arm that calls it has already tried everything more specific, and what
// came next was `type X any` -- a type Go forbids methods on, so the constraints
// were not weakened but dropped, and json.Unmarshal into it cannot fail. A root
// anyOf whose branch the static evaluator could not read, or a root "not" over
// an object shape, became a type that accepts every document including the ones
// the schema forbids.
//
// Being last is also what makes it safe. It cannot take a schema away from a
// path that handles it better, because every such path has already run; and a
// schema that reduces to a bare node -- {}, a bare description, a bare boolean --
// is handed back, so it keeps the type it has today rather than acquiring a
// wrapper for a check it does not need or already gets elsewhere.
func (g *Generator) runtimeSchemaDef(name string, s *schema.Schema) *AnnotationSchemaDef {
	if !g.validationKeywordsEnabled() {
		return nil
	}
	b := &nodeBuilder{
		g:          g,
		allowed:    validatorKeywords,
		inlineRefs: true,
		stack:      map[*schema.Schema]int{},
		// The variables are named after the type they belong to, which is unique
		// in its package, so two schemas compiled into one package cannot claim
		// the same name.
		hoistPrefix: "_rt" + name + "Node",
	}
	lit, nodes, ok := b.build(s)
	if !ok || unownedNodeLiterals[lit] {
		return nil
	}
	return &AnnotationSchemaDef{
		Name:         name,
		Description:  s.Description,
		Annotations:  annotationsOf(s),
		NodeLiteral:  lit,
		Nodes:        nodes,
		NeedsPattern: b.usesPattern,
	}
}

// unownedNodeLiterals are the compiled forms this path hands back rather than
// wraps, read off the emitted literal because the question is what the generated
// code would check and this is that code.
//
// The empty node accepts every value, which is exactly what `any` describes, so
// there is nothing to enforce and a wrapper would only cost the caller the
// convenient type. A bare boolean is handed back for a different reason: `true`
// is the empty node by another name, and `false` already has paths of its own --
// the root arm that answers it with a rejecting wrapper, and the parent-level
// forbidden-property rule -- both of which are written for the type it produces
// today. Taking it over here gains no check and breaks those.
//
// Only a *whole* schema reducing to one of these is handed back. The same nodes
// nested inside a larger one are what a boolean branch compiles to, which is
// half the point of this path.
var unownedNodeLiterals = map[string]bool{
	"_schemaNode{}":                         true,
	"_schemaNode{Boolean: _boolPtr(true)}":  true,
	"_schemaNode{Boolean: _boolPtr(false)}": true,
}
