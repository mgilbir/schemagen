package generator

import (
	"slices"
	"strings"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// This file is the keyword ledger's reading of what a schema node states: which
// of its keywords are assertions -- demands the generated code owes a document
// -- and which are annotations it owes nothing. See ledger.go for what is done
// with the answer.

// jsonKinds is a set of JSON instance kinds. "integer" is its own bit because
// JSON Schema's integer is a subset of number: a keyword about numbers applies
// to both, and a type of "number" admits both.
type jsonKinds uint8

const (
	kindNull jsonKinds = 1 << iota
	kindBoolean
	kindObject
	kindArray
	kindString
	kindNumber // a number that is not an integer
	kindInteger

	kindNumeric = kindNumber | kindInteger
	kindAll     = kindNull | kindBoolean | kindObject | kindArray | kindString | kindNumeric
)

// kindsOfType is the set of kinds one JSON Schema type name admits.
func kindsOfType(t string) jsonKinds {
	switch t {
	case "null":
		return kindNull
	case "boolean":
		return kindBoolean
	case "object":
		return kindObject
	case "array":
		return kindArray
	case "string":
		return kindString
	case "number":
		return kindNumeric
	case "integer":
		return kindInteger
	case "any":
		// Draft 3's spelling of every type.
		return kindAll
	}
	return 0
}

// kindsOfTypeList is the set of kinds a "type" keyword admits; kindAll when it
// is absent.
func kindsOfTypeList(types schema.TypeList) jsonKinds {
	if len(types) == 0 {
		return kindAll
	}
	var k jsonKinds
	for _, t := range types {
		k |= kindsOfType(t)
	}
	return k
}

// keywordKinds is the set of instance kinds a keyword says anything about. A
// keyword absent from it applies to every kind. Every other kind satisfies it
// vacuously, which is what makes a keyword whose kind the instance cannot have
// claimed by the type that excludes it (see ledger's vacuous-by-kind).
var keywordKinds = map[string]jsonKinds{
	"minLength": kindString, "maxLength": kindString, "pattern": kindString,
	"format": kindString, "contentEncoding": kindString, "contentMediaType": kindString,
	"minimum": kindNumeric, "maximum": kindNumeric, "exclusiveMinimum": kindNumeric,
	"exclusiveMaximum": kindNumeric, "multipleOf": kindNumeric, "divisibleBy": kindNumeric,
	"items": kindArray, "prefixItems": kindArray, "additionalItems": kindArray,
	"contains": kindArray, "minContains": kindArray, "maxContains": kindArray,
	"minItems": kindArray, "maxItems": kindArray, "uniqueItems": kindArray,
	"unevaluatedItems": kindArray,
	"properties":       kindObject, "patternProperties": kindObject, "additionalProperties": kindObject,
	"required": kindObject, "minProperties": kindObject, "maxProperties": kindObject,
	"dependentRequired": kindObject, "dependentSchemas": kindObject, "propertyNames": kindObject,
	"unevaluatedProperties": kindObject,
}

// falseAssertion and refAssertion are statedAssertions' answers for a false
// schema and for a $ref that replaces its siblings, shared rather than built
// per node. Their capacity is their length, so an append copies.
var (
	falseAssertion = []string{"false"}
	refAssertion   = []string{"$ref"}
)

// ledgerAnnotationKeywords are keywords that state no assertion in any dialect
// and that statedConstraints nonetheless lists, because they have a field on
// schema.Schema: the OpenAPI discriminator, which only steers which branch of a
// oneOf is tried first, and contentSchema, which no dialect that defines it
// asserts (see TestContentSchemaNeverAssertsInAnyDialect).
var ledgerAnnotationKeywords = map[string]bool{
	"discriminator": true,
	"contentSchema": true,
}

// ledgerLegacyKeywords are the pre-2019 spellings Normalize rewrites into the
// ones that replaced them and leaves standing beside them. The obligation is
// carried by the rewritten keyword -- allOf for extends, not for disallow, the
// dependent pair for dependencies, multipleOf for divisibleBy -- so the legacy
// spelling is the same statement a second time, not a second statement.
var ledgerLegacyKeywords = map[string]bool{
	"extends": true, "disallow": true, "dependencies": true, "divisibleBy": true,
}

// validationVocabularyKeywords are the keywords of the validation vocabulary,
// which a metaschema can leave out of $vocabulary and so make annotations.
var validationVocabularyKeywords = map[string]bool{
	"type": true, "enum": true, "const": true,
	"multipleOf": true, "maximum": true, "exclusiveMaximum": true, "minimum": true, "exclusiveMinimum": true,
	"maxLength": true, "minLength": true, "pattern": true,
	"maxItems": true, "minItems": true, "uniqueItems": true, "maxContains": true, "minContains": true,
	"maxProperties": true, "minProperties": true, "required": true, "dependentRequired": true,
}

// statedAssertions lists the keywords of s that assert something, read under
// s's own dialect and the vocabularies its metaschema declares, sorted. A
// boolean `false` states one assertion, spelled "false"; `true` states none.
//
// Everything statedConstraints lists is an assertion except:
//
//   - the siblings of a $ref the dialect says replaces them (drafts 3 to 7),
//     which are not read at all;
//   - a keyword the parser does not know. From 2019-09 an unknown keyword is an
//     annotation, and this generator reads every dialect that way;
//     --strict-keywords is the setting that refuses one instead (see
//     unknownKeywordError);
//   - `format` where the dialect makes it an annotation, or where it names a
//     format this generator does not recognise, which every dialect says to
//     ignore;
//   - the content vocabulary everywhere but draft 7, and where it names an
//     encoding or media type nothing here can judge;
//   - the validation vocabulary where the metaschema leaves it out of
//     $vocabulary;
//   - the legacy spellings Normalize has already rewritten, and the two
//     keywords no dialect asserts (ledgerAnnotationKeywords).
//
// The list is the caller's to read, not to modify: the two one-keyword answers
// are shared.
func (g *Generator) statedAssertions(s *schema.Schema) []string {
	if s == nil {
		return nil
	}
	if s.IsBooleanSchema() {
		if s.IsFalseSchema() {
			return falseAssertion
		}
		return nil
	}
	// statedConstraints' reading, taken without its sort and without the
	// unknown keywords, which are annotations here (see above): this runs once
	// per node the ledger meets, and a node's keywords are read into a buffer
	// the run reuses and copied out once.
	stated, ok := s.AppendMarshaledKeywords(g.ledger.keywordBuf[:0])
	if !ok {
		return nil
	}
	stated = append(stated, s.KeywordsMarshaledFormOmits()...)
	g.ledger.keywordBuf = stated[:0]
	if s.Ref != "" && g.refOverridesSiblingsForSchema(s) {
		return refAssertion
	}
	validation := g.hasValidationVocabulary(s)
	out := make([]string, 0, len(stated))
	for i, key := range stated {
		if !statedKeyConstrains(s, key) || slices.Contains(stated[:i], key) {
			// Not an assertion, or a hidden key the marshaled form also
			// shows (a non-empty enum), listed once.
			continue
		}
		if ledgerAnnotationKeywords[key] || ledgerLegacyKeywords[key] {
			continue
		}
		if !validation && validationVocabularyKeywords[key] {
			continue
		}
		switch key {
		case "pattern":
			// The empty pattern matches every string.
			if s.Pattern != nil && *s.Pattern == "" {
				continue
			}
		case "format":
			if !g.formatAssertsFor(s) || !formatRecognised(g.formatNameForDialect(s)) {
				continue
			}
		case "contentEncoding":
			if !g.contentAssertsFor(s) || !ContentEncodingCheckable(s.ContentEncoding) {
				continue
			}
		case "contentMediaType":
			if !g.contentAssertsFor(s) || !ContentMediaTypeCheckable(s.ContentMediaType) {
				continue
			}
		}
		out = append(out, key)
	}
	return out
}

// formatRecognised reports whether this generator knows a format by name --
// whether it has a check for it or a Go type that decodes only its values.
// An unrecognised format is one every dialect says to ignore.
func formatRecognised(format string) bool {
	return FormatCheckableOnString(format) || formatGoType(format) != nil || formatNeedsValidation(format)
}

// unknownKeywords lists the keywords of s the parser does not know and that
// are not among the handful known to constrain nothing, sorted.
func unknownKeywords(s *schema.Schema) []string {
	if s == nil || len(s.Extensions) == 0 {
		return nil
	}
	var out []string
	for _, key := range sortedKeys(s.Extensions) {
		if inertKeywords[key] {
			continue
		}
		out = append(out, key)
	}
	return out
}

// keywordIsKindScoped reports whether k says something only about instances
// of some kinds, and which.
func keywordIsKindScoped(k string) (jsonKinds, bool) {
	kinds, ok := keywordKinds[k]
	return kinds, ok
}

// describeKinds writes a kind set for a message.
func describeKinds(k jsonKinds) string {
	var names []string
	for _, e := range []struct {
		bit  jsonKinds
		name string
	}{
		{kindNull, "null"}, {kindBoolean, "boolean"}, {kindObject, "object"}, {kindArray, "array"},
		{kindString, "string"}, {kindNumber, "number"}, {kindInteger, "integer"},
	} {
		if k&e.bit != 0 {
			names = append(names, e.name)
		}
	}
	return strings.Join(names, ", ")
}
