package generator

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// annotationKeywords are the keywords the unevaluatedItems path compiles to the
// runtime evaluator. Anything outside this set makes a schema ineligible, so it
// keeps today's static checks instead of being evaluated with a keyword silently
// ignored.
//
// It is deliberately narrower than validatorKeywords below. This path takes over
// schemas that already generate working static checks, and widening it would
// change the shape of code that is not broken; the other path only ever takes
// over a schema that was about to become `type X any`.
var annotationKeywords = map[string]bool{
	"type": true, "const": true, "multipleOf": true, "minimum": true, "maximum": true,
	"prefixItems": true, "items": true, "additionalItems": true,
	"contains": true, "minContains": true, "maxContains": true,
	"allOf": true, "anyOf": true, "oneOf": true,
	"if": true, "then": true, "else": true,
	"unevaluatedItems": true,

	"$schema": true, "$id": true, "title": true, "description": true,
	"$comment": true, "default": true, "examples": true,
	"deprecated": true, "readOnly": true, "writeOnly": true,
}

// validatorKeywords are the keywords the runtime evaluator models when it is
// asked to enforce a whole schema, rather than only the array-annotation subset.
//
// The keywords that are absent are as much of the design as the ones present.
// "format" is left out because schemagen asserts a format only where the schema
// gives the position a string type, and a node evaluator that quietly ignored it
// would enforce a different schema here than the static path does two lines
// away. "unevaluatedProperties" and the content keywords are left out because
// nothing here models them. "$dynamicRef" and "$recursiveRef" are left out
// because resolving them needs the dynamic scope of the *instance* evaluation,
// which an inlined tree does not have -- which is also what makes the anchors
// safe to accept and ignore: an anchor with nothing pointing at it constrains
// nothing. "dependencies", "extends" and "disallow" are left out because
// Normalize rewrites them into modern keywords but leaves the originals in
// place, so accepting the key would risk reading a schema twice.
//
// Everything not listed fails the schema closed, so a keyword the parser learns
// later cannot be dropped silently.
var validatorKeywords = map[string]bool{
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
	"unevaluatedItems": true,
	"$ref":             true,

	// Carry no constraint of their own where they sit.
	"$defs": true, "definitions": true,
	"$anchor": true, "$dynamicAnchor": true, "$recursiveAnchor": true,
	"$schema": true, "$id": true, "id": true, "title": true, "description": true,
	"$comment": true, "default": true, "examples": true,
	"deprecated": true, "readOnly": true, "writeOnly": true,
}

// inertKeywords are standard keywords that constrain nothing and have no field
// on schema.Schema, so the parser files them under Extensions alongside genuinely
// unknown ones. Listing them here is what keeps a $comment from costing a schema
// its validation; every other extension still refuses the schema, because a
// keyword schemagen has never seen could demand anything at all.
//
// The list is closed on purpose. Each entry is an annotation in every draft that
// defines it, which is the property that makes ignoring it sound.
var inertKeywords = map[string]bool{
	"$comment": true, "examples": true,
	"deprecated": true, "readOnly": true, "writeOnly": true,
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
const (
	maxRuntimeNodes = 4000
	maxRuntimeDepth = 48
)

// nodeBuilder renders a schema as a Go _schemaNode composite literal.
//
// allowed decides which keywords the caller is prepared to have modelled;
// anything else refuses the whole subtree. inlineRefs turns on $ref resolution,
// which is what makes the stack necessary: a reference that leads back to a
// schema already being rendered would inline for ever, so it is refused, and the
// caller falls back to whatever it would have done without this path.
type nodeBuilder struct {
	g          *Generator
	allowed    map[string]bool
	inlineRefs bool

	stack       map[*schema.Schema]bool
	depth       int
	nodes       int
	usesPattern bool
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
	if b.stack[s] {
		// A reference cycle. Inlining cannot terminate, and enforcing the part
		// above the cycle would be a different schema.
		return "", false
	}
	b.stack[s] = true
	b.depth++
	defer func() {
		delete(b.stack, s)
		b.depth--
	}()

	pad := strings.Repeat("\t", indent)
	inner := strings.Repeat("\t", indent+1)

	if s.IsBooleanSchema() {
		return fmt.Sprintf("_schemaNode{Boolean: _boolPtr(%t)}", s.IsTrueSchema()), true
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
		list, ok := b.list(tuple, indent+2)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("PrefixItems: %s,", list))
	}
	if itemsSchema != nil {
		lit, ok := b.literal(itemsSchema, indent+1)
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
		lit, ok := b.literal(additional, indent+1)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("Items: _node(%s),", lit))
	}

	if s.Contains != nil {
		lit, ok := b.literal(s.Contains, indent+1)
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
	for _, group := range []struct {
		name    string
		members map[string]*schema.Schema
	}{{"Properties", s.Properties}, {"PatternProperties", s.PatternProperties}, {"DependentSchemas", s.DependentSchemas}} {
		if len(group.members) == 0 {
			continue
		}
		list, ok := b.memberList(group.members, indent+2)
		if !ok {
			return "", false
		}
		add(fmt.Sprintf("%s: %s,", group.name, list))
		if group.name == "PatternProperties" {
			b.usesPattern = true
		}
	}
	if s.AdditionalProperties != nil {
		additional := s.AdditionalProperties.AsSchema()
		if additional == nil {
			return "", false
		}
		lit, ok := b.literal(additional, indent+1)
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
	}{{"Not", s.Not}, {"If", s.If}, {"Then", s.Then}, {"Else", s.Else},
		{"PropertyNames", s.PropertyNames}, {"UnevaluatedItems", s.UnevaluatedItems}} {
		if branch.sub == nil {
			continue
		}
		lit, ok := b.literal(branch.sub, indent+1)
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
	raw, err := json.Marshal(s)
	if err != nil {
		return false
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
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
func hasCousinUnevaluatedItems(s *schema.Schema) bool {
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
	if !needsRuntimeAnnotations(s) && !hasCousinUnevaluatedItems(s) {
		return nil
	}
	b := &nodeBuilder{g: g, allowed: annotationKeywords, stack: map[*schema.Schema]bool{}}
	lit, ok := b.literal(s, 0)
	if !ok {
		return nil
	}
	return &AnnotationSchemaDef{Name: name, Description: s.Description, NodeLiteral: lit, NeedsPattern: b.usesPattern}
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
	}
	if len(dropped) > 0 {
		def.Unenforced = strings.Join(dropped, ", ")
		g.unenforced = append(g.unenforced, UnenforcedSchema{TypeName: name, Keywords: dropped})
	}
	return def
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
	raw, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
		return nil
	}
	seen := make(map[string]bool, len(present)+len(s.Extensions))
	for key := range present {
		seen[key] = true
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
var nonConstrainingKeywords = map[string]bool{
	"$schema": true, "$id": true, "id": true, "$vocabulary": true,
	"$anchor": true, "$dynamicAnchor": true, "$recursiveAnchor": true,
	"$defs": true, "definitions": true,
	"title": true, "description": true, "default": true,
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
	b := &nodeBuilder{g: g, allowed: validatorKeywords, inlineRefs: true, stack: map[*schema.Schema]bool{}}
	lit, ok := b.literal(s, 0)
	if !ok || unownedNodeLiterals[lit] {
		return nil
	}
	return &AnnotationSchemaDef{Name: name, Description: s.Description, NodeLiteral: lit, NeedsPattern: b.usesPattern}
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
