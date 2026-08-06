package schema

import "reflect"

// This file holds the one answer to "does this dialect define this keyword",
// and the pass that acts on it.
//
// Every draft says the same thing about a keyword it does not define: ignore it.
// That rule was previously written once per keyword -- recursiveRefDefinedForDraft,
// dynamicRefDefinedForDraft, supportsPrefixItems and supportsDependentRequired
// each state it for one word -- which is the shape issue #178 named and #191
// fixed for a different pair of lists: one list per site, drifting apart. Six
// spellings were gated that way and twenty-nine were not, in both directions
// (issue #203).
//
// So the span is stated once, for every keyword this package can parse, and read
// from there by everything that needs it. Two directions fall out of the same
// row:
//
//   - forward, a keyword enforced in a dialect that predates it: draft 4 has no
//     "const", so {"const":"a"} there states an unknown keyword and constrains
//     nothing;
//   - backward, a removed spelling still honoured: "divisibleBy" is draft 3's
//     alone, so a draft-6 document writing it is likewise writing a word its
//     dialect has never heard of.
//
// A keyword that kept its name and changed the *type* of its value needs the
// span per spelling rather than per name, which is what Forms is for.
// "exclusiveMinimum" is a boolean in drafts 3 and 4 and a number from draft 6,
// and reading either spelling under the other dialect is the sharpest form of
// this bug: {"minimum":3,"exclusiveMinimum":5} declared as draft 4 states a
// number where draft 4 defines a boolean, so only "minimum" binds and 4 is a
// valid document -- which this generator refused.

// keywordGate says what the dialect pass does with a keyword whose dialect does
// not define it.
type keywordGate int

const (
	// gateDrop clears the keyword, which is what "ignore an unknown keyword"
	// means for anything that reads the parsed schema.
	gateDrop keywordGate = iota

	// gateKeep records the span and deliberately does not act on it. Every
	// gateKeep row carries a why, and there are only two reasons for one: the
	// keyword establishes identity or resolution scope, or it is the target of a
	// reference. Clearing either does not make the generator ignore a keyword --
	// it makes a $ref stop resolving, which is a different and worse answer than
	// the one the specification asks for.
	gateKeep
)

// valueForm is the JSON shape of a keyword's value, for the keywords whose shape
// decides which dialect defines them.
type valueForm int

const (
	// formAny is the answer for every keyword whose row has no Forms.
	formAny valueForm = iota
	formBoolean
	formNumber
	formStringArray
)

// keywordForm is one spelling of a keyword and the span in which that spelling
// is defined.
type keywordForm struct {
	Form     valueForm
	From, To Draft
}

// keywordDialect is one row of the table: the drafts that define a keyword, and
// what the pass does outside them.
//
// From and To are inclusive, and To is DraftV1 for a keyword no draft has taken
// away. Forms, when present, replaces From/To with one span per value shape; a
// shape no row names is defined by no draft.
type keywordDialect struct {
	From, To Draft
	Forms    []keywordForm
	Gate     keywordGate

	// Why is required on a gateKeep row and is the reason the pass leaves the
	// keyword alone. TestEveryKeptKeywordSaysWhy holds it.
	Why string
}

// definedIn reports whether the row's keyword, stated in the given shape, is a
// keyword of the dialect.
//
// DraftUnknown answers yes to everything, for the reason normalizeDialectRefKeywords
// gives: it is not only "no $schema" but also what a document declaring a custom
// metaschema answers, and such a metaschema may assemble a vocabulary that
// defines the keyword. Dropping it there would discard a constraint the document
// means, on no evidence beyond a URI this package does not recognise.
func (kd keywordDialect) definedIn(d Draft, form valueForm) bool {
	if d == DraftUnknown {
		return true
	}
	if len(kd.Forms) > 0 {
		for _, f := range kd.Forms {
			if f.Form == form {
				return d >= f.From && d <= f.To
			}
		}
		return false
	}
	return d >= kd.From && d <= kd.To
}

// KeywordDefinedIn reports whether a dialect defines a keyword.
//
// It is the whole-keyword question, so a keyword whose row is split by value
// shape answers yes where any shape is defined. Callers that hold a value ask
// KeywordFormDefinedIn instead; the dialect pass does.
//
// A keyword with no row answers yes, so that a vocabulary extension this package
// does not model is not silently disabled. Nothing this package parses can take
// that arm -- TestEveryParsedKeywordHasADialectRow fails the build if a field of
// Schema has no row.
func KeywordDefinedIn(keyword string, d Draft) bool {
	kd, ok := keywordDialects[keyword]
	if !ok {
		return true
	}
	if len(kd.Forms) == 0 {
		return kd.definedIn(d, formAny)
	}
	for _, f := range kd.Forms {
		if kd.definedIn(d, f.Form) {
			return true
		}
	}
	return d == DraftUnknown
}

// KeywordFormDefinedIn reports whether a dialect defines one spelling of a
// keyword -- the boolean "exclusiveMinimum" of drafts 3 and 4 as against the
// number of draft 6 onwards.
func KeywordFormDefinedIn(keyword string, form valueForm, d Draft) bool {
	kd, ok := keywordDialects[keyword]
	if !ok {
		return true
	}
	return kd.definedIn(d, form)
}

// BooleanRequiredDefinedIn reports whether a dialect defines draft 3's
// per-property `"required": true`. It is exported because the promotion of that
// spelling to the parent's required array happens on the parent, where the
// property's own row cannot be reached.
func BooleanRequiredDefinedIn(d Draft) bool {
	return KeywordFormDefinedIn("required", formBoolean, d)
}

// keywordDialects is the table. One row per keyword this package parses.
//
// The spans are the specifications' own. Where an implementation survey
// contradicted a plain reading of the text the row says so, because this
// repository's rule for a split oracle is to record it rather than pick a
// favourite.
var keywordDialects = map[string]keywordDialect{
	// ── Core: identity, references and containers ────────────────────────
	//
	// Every one of these is gateKeep. They are not assertions, and clearing one
	// does not make a document mean less -- it makes a $ref fail to resolve, an
	// $id stop establishing a base URI, or a definition become unreachable. A
	// draft-7 document writing "$defs" is stating a keyword draft 7 has no
	// validation meaning for, and yet "#/$defs/x" is a JSON Pointer that every
	// implementation resolves, because pointer resolution is about the document's
	// structure and not about its vocabulary.
	"$schema":          {From: Draft03, To: DraftV1, Gate: gateKeep, Why: "names the dialect; the question this table answers is asked of it"},
	"$id":              {From: Draft06, To: DraftV1, Gate: gateKeep, Why: "establishes the base URI; clearing it moves every relative $ref in scope"},
	"id":               {From: Draft03, To: Draft04, Gate: gateKeep, Why: "drafts 3 and 4 spell $id this way; Normalize copies it, and clearing it would strand the same refs"},
	"$ref":             {From: Draft03, To: DraftV1, Gate: gateDrop},
	"$anchor":          {From: Draft201909, To: DraftV1, Gate: gateKeep, Why: "a reference target; clearing it breaks the $ref that names it rather than relaxing a constraint"},
	"$dynamicAnchor":   {From: Draft202012, To: DraftV1, Gate: gateKeep, Why: "reference target, as $anchor"},
	"$recursiveAnchor": {From: Draft201909, To: DraftV1, Gate: gateKeep, Why: "reference target, and $recursiveRef is deliberately still honoured in 2020-12 and v1 -- see its row"},
	"$vocabulary":      {From: Draft201909, To: DraftV1, Gate: gateKeep, Why: "declares which vocabularies bind, including on a metaschema whose own dialect this package cannot identify"},
	"$defs":            {From: Draft201909, To: DraftV1, Gate: gateKeep, Why: "JSON Pointer target; '#/$defs/x' resolves in every draft, and clearing it breaks refs rather than dropping an assertion"},
	"definitions":      {From: Draft03, To: Draft07, Gate: gateKeep, Why: "JSON Pointer target, as $defs"},

	// $recursiveRef arrived in 2019-09, so drafts 3, 4, 6 and 7 do not have it.
	//
	// 2020-12 is deliberately still a yes although $dynamicRef replaced it there.
	// The reading was checked rather than assumed: over
	// {"additionalProperties":{"$recursiveRef":"#"}} declared as 2020-12,
	// python-jsonschema ignores the keyword and both santhosh-tekuri
	// implementations go on honouring it. Two answers from three implementations
	// is not a settled question, and dropping it is the one direction that cannot
	// be taken back cheaply: every document the target refused becomes accepted.
	"$recursiveRef": {From: Draft201909, To: DraftV1, Gate: gateDrop},

	// $dynamicRef replaced $recursiveRef in 2020-12, so 2019-09 does not have it
	// either and the span it is absent from is one draft wider. Not an inference
	// from the text alone: over {"additionalProperties":{"$dynamicRef":"#node"}}
	// declared as 2019-09, python-jsonschema, go-jsonschema and rust-boon all
	// three ignore the keyword, and all three ignore it on drafts 6 and 7.
	"$dynamicRef": {From: Draft202012, To: DraftV1, Gate: gateDrop},

	// ── Any-instance assertions ──────────────────────────────────────────
	"type":  {From: Draft03, To: DraftV1, Gate: gateDrop},
	"enum":  {From: Draft03, To: DraftV1, Gate: gateDrop},
	"const": {From: Draft06, To: DraftV1, Gate: gateDrop},

	// ── Composition ──────────────────────────────────────────────────────
	//
	// Draft 3 has none of the four. It expresses the intersection as "extends"
	// and the complement as "disallow", and has no union at all.
	"allOf": {From: Draft04, To: DraftV1, Gate: gateDrop},
	"anyOf": {From: Draft04, To: DraftV1, Gate: gateDrop},
	"oneOf": {From: Draft04, To: DraftV1, Gate: gateDrop},
	"not":   {From: Draft04, To: DraftV1, Gate: gateDrop},

	// ── Draft 3's own spellings ──────────────────────────────────────────
	//
	// Each is draft 3's alone, and Normalize rewrites it into the modern keyword.
	// The rewrite runs after this pass on the same node, so on any later dialect
	// the source keyword is already gone and the rewrite does not fire -- which
	// is the ordering that keeps the forward and backward directions from
	// cancelling each other out. On draft 3 itself the source survives, the
	// rewrite fires, and the modern keyword it produces is never re-examined by
	// this pass.
	"extends":     {From: Draft03, To: Draft03, Gate: gateDrop},
	"disallow":    {From: Draft03, To: Draft03, Gate: gateDrop},
	"divisibleBy": {From: Draft03, To: Draft03, Gate: gateDrop},

	// ── Objects ──────────────────────────────────────────────────────────
	"properties":           {From: Draft03, To: DraftV1, Gate: gateDrop},
	"additionalProperties": {From: Draft03, To: DraftV1, Gate: gateDrop},
	"patternProperties":    {From: Draft03, To: DraftV1, Gate: gateDrop},
	"minProperties":        {From: Draft04, To: DraftV1, Gate: gateDrop},
	"maxProperties":        {From: Draft04, To: DraftV1, Gate: gateDrop},
	"propertyNames":        {From: Draft06, To: DraftV1, Gate: gateDrop},

	// "required" is two keywords under one name. Draft 3 writes it on the
	// property, as a boolean; draft 4 moved it to the parent as an array of
	// names. Neither dialect knows the other's spelling, so the span has to be
	// per shape: a draft-6 document writing {"a":{"required":true}} states a
	// boolean where the keyword takes an array, and the property is not required.
	"required": {Gate: gateDrop, Forms: []keywordForm{
		{Form: formBoolean, From: Draft03, To: Draft03},
		{Form: formStringArray, From: Draft04, To: DraftV1},
	}},

	// "dependencies" was split into dependentRequired and dependentSchemas in
	// 2019-09 and removed. Normalize performs that split, and -- as with draft
	// 3's spellings above -- it fires only where this pass has left the source
	// keyword standing, so a draft-7 document keeps its "dependencies" and a
	// draft-7 document writing "dependentRequired" states nothing.
	"dependencies":      {From: Draft03, To: Draft07, Gate: gateDrop},
	"dependentRequired": {From: Draft201909, To: DraftV1, Gate: gateDrop},
	"dependentSchemas":  {From: Draft201909, To: DraftV1, Gate: gateDrop},

	// ── Arrays ───────────────────────────────────────────────────────────
	"items":       {From: Draft03, To: DraftV1, Gate: gateDrop},
	"prefixItems": {From: Draft202012, To: DraftV1, Gate: gateDrop},

	// additionalItems was superseded by 2020-12's items-past-the-prefix and
	// removed from the vocabulary there.
	"additionalItems": {From: Draft03, To: Draft201909, Gate: gateDrop},

	"minItems":    {From: Draft03, To: DraftV1, Gate: gateDrop},
	"maxItems":    {From: Draft03, To: DraftV1, Gate: gateDrop},
	"uniqueItems": {From: Draft03, To: DraftV1, Gate: gateDrop},
	"contains":    {From: Draft06, To: DraftV1, Gate: gateDrop},
	"minContains": {From: Draft201909, To: DraftV1, Gate: gateDrop},
	"maxContains": {From: Draft201909, To: DraftV1, Gate: gateDrop},

	// ── Strings ──────────────────────────────────────────────────────────
	"minLength": {From: Draft03, To: DraftV1, Gate: gateDrop},
	"maxLength": {From: Draft03, To: DraftV1, Gate: gateDrop},
	"pattern":   {From: Draft03, To: DraftV1, Gate: gateDrop},

	// "format" is defined in every draft. Which drafts *assert* it is a separate
	// question with a separate answer -- see formatAssertsFor -- and one this
	// table deliberately does not fold in: a keyword that annotates is still a
	// keyword the dialect has, and clearing it would take away the annotation
	// too.
	"format": {From: Draft03, To: DraftV1, Gate: gateDrop},

	// The content vocabulary arrived in draft 7; contentSchema is 2019-09's
	// addition to it. Whether it asserts or annotates is again a separate
	// question -- contentAssertsFor -- asked only of the drafts that have it.
	"contentEncoding":  {From: Draft07, To: DraftV1, Gate: gateDrop},
	"contentMediaType": {From: Draft07, To: DraftV1, Gate: gateDrop},
	"contentSchema":    {From: Draft201909, To: DraftV1, Gate: gateDrop},

	// ── Numbers ──────────────────────────────────────────────────────────
	"minimum": {From: Draft03, To: DraftV1, Gate: gateDrop},
	"maximum": {From: Draft03, To: DraftV1, Gate: gateDrop},

	// The two whose value type changed. Drafts 3 and 4 define a boolean that
	// modifies the sibling minimum/maximum; draft 6 redefined it as the bound
	// itself. Each dialect knows exactly one of the two spellings, and the other
	// is an unknown value it has to ignore -- which is why the row is by shape.
	"exclusiveMinimum": {Gate: gateDrop, Forms: []keywordForm{
		{Form: formBoolean, From: Draft03, To: Draft04},
		{Form: formNumber, From: Draft06, To: DraftV1},
	}},
	"exclusiveMaximum": {Gate: gateDrop, Forms: []keywordForm{
		{Form: formBoolean, From: Draft03, To: Draft04},
		{Form: formNumber, From: Draft06, To: DraftV1},
	}},

	// draft 3 spells this "divisibleBy"; see that row.
	"multipleOf": {From: Draft04, To: DraftV1, Gate: gateDrop},

	// ── Conditionals ─────────────────────────────────────────────────────
	"if":   {From: Draft07, To: DraftV1, Gate: gateDrop},
	"then": {From: Draft07, To: DraftV1, Gate: gateDrop},
	"else": {From: Draft07, To: DraftV1, Gate: gateDrop},

	// ── Unevaluated ──────────────────────────────────────────────────────
	"unevaluatedItems":      {From: Draft201909, To: DraftV1, Gate: gateDrop},
	"unevaluatedProperties": {From: Draft201909, To: DraftV1, Gate: gateDrop},

	// ── Annotations ──────────────────────────────────────────────────────
	//
	// None of them changes a verdict, so a dialect sweep that reads verdicts
	// cannot see them. They are gated all the same: an annotation a dialect does
	// not define is still a word that dialect has never heard of, and this
	// generator does act on them -- "deprecated" and the two access flags reach a
	// doc comment, and --strict-read-write makes the access flags change what the
	// generated type accepts and emits.
	"title":       {From: Draft03, To: DraftV1, Gate: gateDrop},
	"description": {From: Draft03, To: DraftV1, Gate: gateDrop},
	"default":     {From: Draft03, To: DraftV1, Gate: gateDrop},
	"readOnly":    {From: Draft07, To: DraftV1, Gate: gateDrop},
	"writeOnly":   {From: Draft07, To: DraftV1, Gate: gateDrop},
	"deprecated":  {From: Draft201909, To: DraftV1, Gate: gateDrop},

	// "discriminator" is not a JSON Schema keyword in any draft -- it is
	// OpenAPI's, read here because OpenAPI documents are a real input. No dialect
	// defines it, so no dialect can un-define it, and a span would be a fiction.
	"discriminator": {From: Draft03, To: DraftV1, Gate: gateKeep, Why: "OpenAPI's keyword, not JSON Schema's; no draft defines it, so no draft withdraws it"},
}

// hiddenFieldKeywords names the fields of Schema whose JSON tag is "-" and which
// nonetheless stand for a keyword, so that clearing the keyword clears them too.
//
// It is the same classification KeywordsMarshaledFormOmits makes for the same
// three fields, and for the same reason: encoding/json cannot carry them, so
// nothing that reads the marshaled form can see them either.
// TestSchemaFieldsAreClassifiedForPresence holds that list; this one is held by
// TestSchemaFieldsAreClassifiedForDialect.
var hiddenFieldKeywords = map[string]string{
	"ConstIsNull": "const",
	"TypeSchemas": "type",
}

// nonKeywordFields names the exported fields of Schema that carry no keyword at
// all: parse bookkeeping, and the resolution scope computed after parsing.
var nonKeywordFields = map[string]bool{
	"BooleanSchema": true, // the bare true/false in a schema position, not a keyword
	"Extensions":    true, // every keyword this package does not model, kept for pointer resolution
	"DetectedDraft": true,
	"BaseURI":       true,
	"DocumentRoot":  true,
}

// statedForm reports the shape a schema states a keyword in, for the keywords
// whose row is split by shape. Every other keyword answers formAny, which is the
// shape those rows are written against.
//
// A keyword with Forms and no arm here would answer formAny, match no arm of its
// row, and be dropped on every dialect. TestEveryFormedKeywordHasAShapeReader is
// what stops that from being possible.
func (s *Schema) statedForm(keyword string) valueForm {
	switch keyword {
	case "exclusiveMinimum":
		return exclusiveBoundForm(s.ExclusiveMinimum)
	case "exclusiveMaximum":
		return exclusiveBoundForm(s.ExclusiveMaximum)
	case "required":
		if s.Required.IsDraft3Required() {
			return formBoolean
		}
		return formStringArray
	default:
		return formAny
	}
}

func exclusiveBoundForm(b *SchemaOrFloat) valueForm {
	switch {
	case b == nil:
		return formAny
	case b.Bool != nil:
		return formBoolean
	case b.Number != nil:
		return formNumber
	default:
		return formAny
	}
}

// dropKeywordsOutsideDialect clears every keyword this node states that the
// dialect does not define.
//
// It walks the struct rather than naming the fields, so a keyword this package
// learns later is gated the day its field is added: the field carries the JSON
// tag, the tag is the keyword, and the keyword has to have a row before the
// package's tests pass. A hand-written switch here would be one more list to
// forget, which is the defect this whole file replaces.
//
// Clearing is what "ignore an unknown keyword" means to everything downstream. A
// node whose dialect is unknown keeps everything, for the reason
// keywordDialect.definedIn gives.
func (s *Schema) dropKeywordsOutsideDialect(d Draft) {
	if s == nil || d == DraftUnknown {
		return
	}
	v := reflect.ValueOf(s).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		keyword := fieldKeyword(f)
		if keyword == "" {
			continue
		}
		kd, ok := keywordDialects[keyword]
		if !ok || kd.Gate != gateDrop {
			continue
		}
		fv := v.Field(i)
		if fv.IsZero() {
			continue
		}
		if !kd.definedIn(d, s.statedForm(keyword)) {
			fv.Set(reflect.Zero(f.Type))
		}
	}
}

// fieldKeyword returns the keyword a field of Schema carries, or "" when it
// carries none.
//
// An unexported field carries none by construction: encoding/json cannot see it,
// so it has no tag worth reading, and reflect cannot set it either. That is
// asserted rather than special-cased here -- see
// TestSchemaFieldsAreClassifiedForDialect, which fails on a keyword-carrying
// field the pass could not write to, instead of leaving a panic to find it.
func fieldKeyword(f reflect.StructField) string {
	if nonKeywordFields[f.Name] {
		return ""
	}
	if keyword, ok := hiddenFieldKeywords[f.Name]; ok {
		return keyword
	}
	return jsonTagName(f)
}

// jsonTagName is the keyword a field's json tag names, or "" for a field the
// encoding never shows.
func jsonTagName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" || tag == "-" {
		return ""
	}
	for i := 0; i < len(tag); i++ {
		if tag[i] == ',' {
			return tag[:i]
		}
	}
	return tag
}
