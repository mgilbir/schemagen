package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// This file is how a schema object is read: one keyword at a time, each from
// the key that names it exactly, with a value that is not a legal value of its
// keyword recorded against that keyword rather than failing the document.
//
// Three rules, and each replaces a behaviour that depended on something it
// should not have.
//
// A keyword is the key that spells it, exactly. JSON Schema keywords are
// case-sensitive and a keyword an implementation does not recognise constrains
// nothing: 2020-12 core §6.5 treats it as an annotation, draft 7 §4.3.1 and
// draft 4 §5.6 say to ignore it. encoding/json reads struct fields the other way
// round -- a key matching no field exactly is matched again case-insensitively
// -- so {"type":"string","MinLength":5} refused "ab", and "$ſchema" (U+017F
// folds to "s") chose the document's dialect. Issue #350. Reading each keyword
// by looking its own name up in the object's key set cannot fold anything,
// because nothing here asks encoding/json to match a name.
//
// A key stated twice means its last value, for every key of every object in
// the document. RFC 8259 §4 leaves duplicate names' meaning to the
// implementation; last-wins is what encoding/json does for a map, which is how
// the key set is read, and what it does for a struct field holding a scalar.
// What it does *not* do is apply that to a struct field holding a map: it
// decodes the second value into the map the first one already filled, so
// {"properties":{"a":{}},"properties":{"b":{}}} read as properties a and b. And
// the case-folding workaround this file replaces rebuilt the object from the
// key set -- where the second value alone survives -- whenever some unrelated
// key happened to fold onto a keyword, so adding {"Title":"x"} flipped the same
// properties to b alone. Here every keyword's value is taken from the key set,
// so the answer is the last value, whatever the keyword's type and whatever
// else the object holds. The same policy was the only one available for the
// alternative of refusing: encoding/json reports no duplicate to refuse on.
//
// A malformed value is held against its keyword until the node's dialect is
// known. {"minLength":"x"} is not a schema in any dialect that defines
// minLength, and refusing it is right there -- it is what the struct decode did,
// and what a null subschema gets (pkg/generator's checkNullSubschemas). But
// "defines" is the operative word: a dialect that does not define the keyword
// has to ignore it, whatever its value, and a draft 2020-12 document writing
// {"divisibleBy":"x"} is a legal 2020-12 document. The decode cannot tell those
// two apart: which dialect a node is read under comes from its own $schema, an
// ancestor's, or --draft, none of which is known until Normalize. So the decode
// records, and the dialect pass decides: a record for a keyword the node's
// dialect does not define is dropped with the keyword, and every record left
// standing is a refusal (MalformedKeywords, read by the generator before it
// generates from a node).

// schemaKeywordField is one keyword Schema has a field for, as the decoder
// fills it.
type schemaKeywordField struct {
	index int
	key   string
	typ   reflect.Type
}

// schemaKeywordFields is every json-tagged field of Schema, in struct order.
// Built beside knownSchemaKeys, from the same tags, so that a keyword added as a
// field is decoded the day it is added.
var schemaKeywordFields []schemaKeywordField

// nullableKeywords are the keywords whose value may legally be JSON null: the
// two that hold an instance value rather than a schema-shaped one. Everywhere
// else null is a malformed value -- no metaschema admits it for any other
// keyword -- and it used to be indistinguishable from absence, because a nil
// pointer is what encoding/json makes of both: {"not":null} and
// {"minLength":null} constrained nothing, while {"allOf":[null]} was refused.
var nullableKeywords = map[string]bool{
	"const":   true,
	"default": true,
}

// errNullSchema is the error for a JSON null where a schema is required. The
// wording is the one checkNullSubschemas has always reported a null subschema
// in, so the two refusals of one mistake read alike.
var errNullSchema = errors.New("schema is null (a schema must be an object or boolean)")

// errNullValue is the error for a JSON null as the value of a keyword that
// takes something else.
var errNullValue = errors.New("value is null")

// KeywordError is a keyword whose value is not a legal value of that keyword.
type KeywordError struct {
	// Keyword is the keyword as the document spelled it.
	Keyword string
	// Path is where, below the keyword, the value that is wrong sits, as JSON
	// Pointer reference tokens: ["1"] for the null second entry of an "allOf",
	// ["a"] for a "dependencies" member. It is empty when the keyword's value as
	// a whole is what is wrong. Keyword followed by Path is the value's location
	// relative to the node, as the document wrote it.
	Path []string
	// Err says what is wrong with the value.
	Err error
}

func (e *KeywordError) Error() string {
	return fmt.Sprintf("%s: %v", strings.Join(escapeTokens(append([]string{e.Keyword}, e.Path...)), "/"), e.Err)
}

func (e *KeywordError) Unwrap() error { return e.Err }

// MalformedKeywords reports the keywords of this node -- this node only, not
// its subschemas -- whose value is not a legal value of the keyword, sorted by
// keyword.
//
// A keyword reported here was not read: its field holds the zero value, so
// nothing downstream acts on a value the document did not legally state. The
// exception is a negative count, which is held as written, because one dialect
// defines it (see FlexInt).
//
// After Normalize, only the keywords the node's dialect defines are reported;
// a keyword the dialect does not define is ignored whatever its value, as every
// draft says an unknown keyword is. Before Normalize every malformed keyword is
// reported, which fails closed: a caller that generates without normalizing is
// refused rather than handed a schema with a keyword silently missing.
func (s *Schema) MalformedKeywords() []*KeywordError {
	if s == nil || len(s.malformed) == 0 {
		return nil
	}
	keywords := make([]string, 0, len(s.malformed))
	for kw := range s.malformed {
		keywords = append(keywords, kw)
	}
	sort.Strings(keywords)
	out := make([]*KeywordError, 0, len(keywords))
	for _, kw := range keywords {
		ke := &KeywordError{Keyword: kw, Err: s.malformed[kw]}
		if placed, ok := ke.Err.(*subPathError); ok {
			ke.Path, ke.Err = append([]string(nil), placed.path...), placed.err
		}
		out = append(out, ke)
	}
	return out
}

func (s *Schema) noteMalformed(keyword string, err error) {
	if s.malformed == nil {
		s.malformed = make(map[string]error)
	}
	s.malformed[keyword] = err
}

// holdsSchemas reports whether a field of this type holds a schema or a
// container of them, so a null there is reported as the null subschema it is.
func holdsSchemas(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	switch t {
	case reflect.TypeOf(Schema{}), reflect.TypeOf(SchemaOrBool{}), reflect.TypeOf(SchemaOrSchemaArray{}):
		return true
	}
	return false
}

// holdsInstanceData reports whether a field of this type holds arbitrary JSON --
// const, enum, default -- where null is an ordinary value.
func holdsInstanceData(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	return t.Kind() == reflect.Interface
}

// containsJSONNull reports whether a null appears anywhere in a JSON value.
func containsJSONNull(raw json.RawMessage) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if tok == nil {
			return true
		}
	}
}

// isJSONNull reports whether a raw value is the JSON literal null.
func isJSONNull(raw json.RawMessage) bool {
	return trimJSONWhitespace(raw) == "null"
}

// decodeKeywordValue decodes one keyword's value into target.
//
// Through a json.Decoder with UseNumber rather than json.Unmarshal so that the
// keywords typed `any` -- const, enum, default, and anything nested inside them
// -- hold the number the schema wrote instead of the float64 it rounds to. An
// enum member of 9223372036854775807 is an int64 exactly and a float64 not at
// all, and the generator has to emit it as a Go constant. The numeric keywords
// with a type of their own (Number, FlexInt) decode from the raw bytes either
// way.
func decodeKeywordValue(raw json.RawMessage, target any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(target)
}

// UnmarshalJSON reads one schema: a bare true or false (draft 6+'s boolean
// schemas), or an object whose keys are keywords. See the top of this file for
// how the keywords are read.
//
// The error it returns is for a value that is not a schema at all -- a string,
// a number, an array, null. A schema object with a malformed keyword is not an
// error here; see MalformedKeywords.
func (s *Schema) UnmarshalJSON(data []byte) error {
	trimmed := trimJSONWhitespace(data)
	switch trimmed {
	case "true", "false":
		b := trimmed == "true"
		*s = Schema{BooleanSchema: &b}
		return nil
	case "null":
		return errNullSchema
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("a schema must be an object or a boolean, got: %s", abbreviateJSON(trimmed))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	*s = Schema{}
	v := reflect.ValueOf(s).Elem()
	for _, f := range schemaKeywordFields {
		val, ok := raw[f.key]
		if !ok {
			continue
		}
		if s.decodeSpecialKeyword(f.key, val) {
			continue
		}
		if isJSONNull(val) && !nullableKeywords[f.key] {
			if holdsSchemas(f.typ) {
				s.noteMalformed(f.key, errNullSchema)
			} else {
				s.noteMalformed(f.key, errNullValue)
			}
			continue
		}
		// A null *inside* the value is the same mistake one level down, and
		// encoding/json is as silent about it: it decodes null into a string
		// or a bool as the zero value, so {"required":["a",null]} required a
		// property named "". A value that holds neither schemas (each of which
		// answers for its own nulls) nor instance data (where null is a value)
		// has no position a null can legally fill.
		if !holdsSchemas(f.typ) && !holdsInstanceData(f.typ) && containsJSONNull(val) {
			s.noteMalformed(f.key, fmt.Errorf("contains null: %s", abbreviateJSON(trimJSONWhitespace(val))))
			continue
		}
		ptr := reflect.New(f.typ)
		err := decodeKeywordValue(val, ptr.Interface())
		if err != nil && !errors.Is(err, errNegativeCount) {
			// An entry of a subschema container that is not a schema is
			// recorded at the entry, where the document wrote it. Found only
			// once the decode has failed: it reads the value a second time.
			if path, entryErr := badSubschemaEntry(val, f.typ); entryErr != nil {
				err = atPath(entryErr, path...)
			}
			s.noteMalformed(f.key, err)
			continue
		}
		// A null entry decodes without complaint, into a nil *Schema; it is
		// recorded at the entry too, and the keyword left unset.
		if path, ok := nilSubschemaEntry(ptr.Elem()); ok {
			s.noteMalformed(f.key, atPath(errNullSchema, path...))
			continue
		}
		if err != nil {
			// A negative count: held as written, and malformed unless the
			// node's dialect turns out to define it. See FlexInt.
			s.noteMalformed(f.key, err)
		}
		v.Field(f.index).Set(ptr.Elem())
		s.noteChildrenOf(f.key, ptr.Elem())
	}

	// An identifier Go cannot parse as a URI-reference establishes no base URI
	// anyone can compute, and every draft requires $id to be one (2020-12 core
	// §8.2.1, draft 7 §8.2, draft 4 §7.2). Holding it anyway is what let two
	// walks of one document disagree: ComputeBaseURIs skipped the scope change
	// when url.Parse failed, so the resource graph's anchor walk descended into
	// the node, while the resolver's changesScope only asked whether an id was
	// present and stopped there (both now ask scopeID). Cleared here, the id is
	// absent for both, and the record refuses the document wherever its
	// dialect defines the keyword.
	for _, id := range []struct {
		keyword string
		field   *string
	}{{"$id", &s.ID}, {"id", &s.LegacyID}} {
		if *id.field == "" {
			continue
		}
		if _, err := url.Parse(*id.field); err != nil {
			s.noteMalformed(id.keyword, fmt.Errorf("not a URI-reference: %w", err))
			*id.field = ""
		}
	}

	// Every key no field answers to -- vendor keywords, keywords this package
	// does not model, case variants of ones it does -- is kept verbatim, so it
	// asserts nothing and stays reachable by JSON Pointer.
	// maporder: copies members under their own keys, which are distinct, so no order writes a different map.
	for key, val := range raw {
		if !knownSchemaKeys[key] {
			if s.Extensions == nil {
				s.Extensions = make(map[string]json.RawMessage)
			}
			s.Extensions[key] = val
		}
	}
	return nil
}

// decodeSpecialKeyword decodes the keywords whose value a plain decode into
// their field cannot read, and reports whether it handled key.
func (s *Schema) decodeSpecialKeyword(key string, val json.RawMessage) bool {
	trimmed := trimJSONWhitespace(val)
	switch key {
	case "const":
		// encoding/json leaves a *any nil for a JSON null, which would make
		// {"const": null} indistinguishable from no const at all.
		if trimmed == "null" {
			s.ConstIsNull = true
			return true
		}
		return false

	case "$ref":
		// {"$ref": ""} is a reference, and an empty Ref field is how this
		// package spells "this schema has no $ref" -- so the keyword
		// disappeared and the position it stood in became `any` (issue #272).
		// The empty string is a URI-reference like any other and RFC 3986 §5.2
		// resolves it against the base URI, which is the same target "#" names:
		// a same-document reference to the resource in scope. So the two
		// spellings are made one here, at the only point that can still tell
		// {"$ref": ""} from a schema with no $ref. "#" is itself resolved against
		// the base URI in scope, so under a nested $id it reaches that resource
		// and not the document root.
		if trimmed == `""` {
			s.Ref = "#"
			return true
		}
		return false

	case "required":
		// Two keywords under one name: draft 3's boolean on the property, and
		// draft 4's array of names on the parent. Which one a node states is
		// the shape of its value; which one its dialect defines is the dialect
		// pass's question (keywordDialects["required"]).
		if trimmed == "true" || trimmed == "false" {
			b := trimmed == "true"
			s.Draft3Required = &b
			return true
		}
		return false

	case "type":
		if trimmed == "null" {
			s.noteMalformed(key, errNullValue)
			return true
		}
		var types TypeList
		if err := json.Unmarshal(val, &types); err != nil {
			s.noteMalformed(key, err)
			return true
		}
		s.Type = types
		// Draft 3 allows schema-valued entries in the type array. They are kept
		// apart from the names so that validation can read the keyword as an
		// anyOf over primitive names and schema branches. TypeList has already
		// refused every entry that is neither.
		if trimmed[0] == '[' {
			var elems []json.RawMessage
			_ = json.Unmarshal(val, &elems)
			for i, elem := range elems {
				if t := trimJSONWhitespace(elem); len(t) == 0 || t[0] != '{' {
					continue
				}
				var typeSchema Schema
				if err := json.Unmarshal(elem, &typeSchema); err != nil {
					s.noteMalformed(key, atPath(err, strconv.Itoa(i)))
					s.Type, s.TypeSchemas = nil, nil
					s.dropChildrenUnder(key)
					return true
				}
				s.TypeSchemas = append(s.TypeSchemas, &typeSchema)
				s.noteChild(&typeSchema, key, strconv.Itoa(i))
			}
		}
		return true

	case "extends":
		schemas, err := parseDraft3Schemas(val, false)
		if err != nil {
			s.noteMalformed(key, err)
			return true
		}
		s.Extends = append(json.RawMessage(nil), val...)
		s.ExtendsSchemas = schemas
		s.noteDraft3Targets(key, val, schemas)
		return true

	case "disallow":
		schemas, err := parseDraft3Schemas(val, true)
		if err != nil {
			s.noteMalformed(key, err)
			return true
		}
		s.Disallow = append(json.RawMessage(nil), val...)
		s.DisallowSchemas = schemas
		s.noteDraft3Targets(key, val, schemas)
		return true

	case "dependencies":
		schemas, required, err := parseDependencies(val)
		if err != nil {
			s.noteMalformed(key, err)
			return true
		}
		s.Dependencies = append(json.RawMessage(nil), val...)
		s.DependencySchemas, s.DependencyRequired = schemas, required
		for _, name := range sortedKeys(schemas) {
			s.noteChild(schemas[name], key, name)
		}
		return true
	}
	return false
}

// dropChildrenUnder forgets the subschemas recorded under key, for a keyword
// whose value was refused after some of its entries had been read.
func (s *Schema) dropChildrenUnder(key string) {
	kept := s.srcChildren[:0]
	for _, c := range s.srcChildren {
		if c.tokens[0] != key {
			kept = append(kept, c)
		}
	}
	s.srcChildren = kept
}

// noteDraft3Targets records where the document wrote each schema of "extends"
// or "disallow": the value itself when it is one schema, and each entry of an
// array by its index. A "disallow" entry that is a type name is recorded at
// that name: the {"type": name} it is read as is this package's spelling, and
// the string is what the document wrote in its place. The resolver does not
// answer a pointer to it (see childAt), since the document holds no schema
// there.
func (s *Schema) noteDraft3Targets(key string, val json.RawMessage, schemas []*Schema) {
	note := func(elem json.RawMessage, sc *Schema, tokens ...string) {
		if t := trimJSONWhitespace(elem); len(t) > 0 && t[0] == '{' {
			s.noteChild(sc, tokens...)
			return
		}
		s.srcChildren = append(s.srcChildren, srcChild{tokens: tokens, node: sc, typeName: true})
	}
	trimmed := trimJSONWhitespace(val)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		note(val, schemas[0], key)
		return
	}
	var elems []json.RawMessage
	if json.Unmarshal(val, &elems) != nil || len(elems) != len(schemas) {
		return
	}
	for i, sc := range schemas {
		note(elems[i], sc, key, strconv.Itoa(i))
	}
}

// parseDraft3Schemas reads the value of draft 3's "extends" (a schema or an
// array of schemas) or "disallow" (a type name, a schema, or an array of
// either; typeNames says names are allowed). A name becomes the schema
// {"type": name}, which is what it means there.
//
// Only draft 3 defines either keyword, and draft 3 has no boolean schemas, so a
// schema here is an object. Anything else -- null, a number, a boolean -- is a
// malformed value and is refused, the same as a null subschema of any other
// keyword. It used to be skipped: an entry that named nothing legible
// contributed no branch, which turned {"disallow":[null]} into a keyword that
// forbids nothing and {"extends":null} into allOf:[{}]. Both are documents no
// dialect gives a meaning, and reading a meaning into them is the silent
// guess the refusal of {"allOf":[null]} exists to prevent.
func parseDraft3Schemas(val json.RawMessage, typeNames bool) ([]*Schema, error) {
	one := func(elem json.RawMessage) (*Schema, error) {
		trimmed := trimJSONWhitespace(elem)
		switch {
		case typeNames && len(trimmed) > 0 && trimmed[0] == '"':
			var name string
			if err := json.Unmarshal(elem, &name); err != nil {
				return nil, err
			}
			return &Schema{Type: TypeList{name}}, nil
		case len(trimmed) > 0 && trimmed[0] == '{':
			var sc Schema
			if err := json.Unmarshal(elem, &sc); err != nil {
				return nil, err
			}
			return &sc, nil
		case trimmed == "null":
			return nil, errNullSchema
		}
		if typeNames {
			return nil, fmt.Errorf("must be a type name or a schema object, got: %s", abbreviateJSON(trimmed))
		}
		return nil, fmt.Errorf("must be a schema object, got: %s", abbreviateJSON(trimmed))
	}

	trimmed := trimJSONWhitespace(val)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		sc, err := one(val)
		if err != nil {
			return nil, err
		}
		return []*Schema{sc}, nil
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(val, &elems); err != nil {
		return nil, err
	}
	out := make([]*Schema, 0, len(elems))
	for i, elem := range elems {
		sc, err := one(elem)
		if err != nil {
			return nil, atPath(err, strconv.Itoa(i))
		}
		out = append(out, sc)
	}
	return out, nil
}

// parseDependencies reads the value of "dependencies": an object whose every
// member is either the names another property requires or a schema the object
// must satisfy when the property is present.
//
// The names are an array of strings, or in draft 3 a single bare string; the
// schema is an object or, from draft 6, a boolean. That is every shape any
// dialect gives a member. The keyword is honoured in every dialect (see its row
// in keywordDialects), so every shape is read in every dialect too.
//
// Anything else is refused. {"dependencies":{"a":null}} used to be read as the
// empty schema, which every object satisfies, while {"dependentSchemas":
// {"a":null}} -- the same constraint in 2019-09's spelling -- was refused as the
// null subschema it is.
func parseDependencies(val json.RawMessage) (map[string]*Schema, map[string][]string, error) {
	trimmed := trimJSONWhitespace(val)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, nil, fmt.Errorf("must be an object, got: %s", abbreviateJSON(trimmed))
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(val, &members); err != nil {
		return nil, nil, err
	}
	var schemas map[string]*Schema
	var required map[string][]string
	for _, key := range sortedRawKeys(members) {
		elem := members[key]
		t := trimJSONWhitespace(elem)
		switch {
		case len(t) > 0 && t[0] == '"':
			var name string
			if err := json.Unmarshal(elem, &name); err != nil {
				return nil, nil, atPath(err, key)
			}
			if required == nil {
				required = make(map[string][]string)
			}
			required[key] = []string{name}
		case len(t) > 0 && t[0] == '[':
			var names []string
			if err := json.Unmarshal(elem, &names); err != nil || containsJSONNull(elem) {
				return nil, nil, atPath(fmt.Errorf("must be an array of property names, got: %s", abbreviateJSON(t)), key)
			}
			if required == nil {
				required = make(map[string][]string)
			}
			required[key] = names
		case t == "null":
			return nil, nil, atPath(errNullSchema, key)
		case len(t) > 0 && (t[0] == '{' || t == "true" || t == "false"):
			var sc Schema
			if err := json.Unmarshal(elem, &sc); err != nil {
				return nil, nil, atPath(err, key)
			}
			if schemas == nil {
				schemas = make(map[string]*Schema)
			}
			schemas[key] = &sc
		default:
			return nil, nil, atPath(fmt.Errorf("must be a schema or an array of property names, got: %s", abbreviateJSON(t)), key)
		}
	}
	return schemas, required, nil
}

func sortedRawKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// abbreviateJSON shortens a value for an error message, so that a refusal
// naming a malformed value does not quote a whole subtree back.
func abbreviateJSON(s string) string {
	const limit = 60
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}

// UnmarshalJSON reads the discriminator's two fields by their exact names,
// last value winning, as a schema's keywords are read.
//
// The discriminator is OpenAPI's keyword rather than JSON Schema's, but a key it
// does not define is as much nothing to it as an unrecognised keyword is to a
// schema: encoding/json's case-insensitive second match made
// {"discriminator":{"PropertyName":"kind"}} name a property the document never
// named, and the generated oneOf dispatched on it (issue #350).
func (d *Discriminator) UnmarshalJSON(data []byte) error {
	trimmed := trimJSONWhitespace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("discriminator must be an object, got: %s", abbreviateJSON(trimmed))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*d = Discriminator{}
	if val, ok := raw["propertyName"]; ok {
		if isJSONNull(val) {
			return fmt.Errorf("propertyName: %w", errNullValue)
		}
		if err := json.Unmarshal(val, &d.PropertyName); err != nil {
			return fmt.Errorf("propertyName: %w", err)
		}
	}
	if val, ok := raw["mapping"]; ok {
		if containsJSONNull(val) {
			return fmt.Errorf("mapping: %w", errNullValue)
		}
		if err := json.Unmarshal(val, &d.Mapping); err != nil {
			return fmt.Errorf("mapping: %w", err)
		}
	}
	return nil
}
