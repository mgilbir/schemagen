package emitter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// The type-name emission sites, and the one question every one of them has to
// answer.
//
// A generated file names Go types in two different currencies. Most of them go
// through the `goType` funnel, which renders a GoType through GoTypeName() and
// therefore carries a foreign package's import alias whether or not the site
// remembered to think about it. The rest are *strings* the generator put into
// the IR, and a string carries whatever the producer chose to put in it -- so
// each of those sites is a separate opportunity to write another package's type
// name without its qualifier.
//
// That is not hypothetical. `{"anyOf":[{"$ref":"<other package>"},{"type":
// "integer"}]}` in a property emitted `var _bv T` for a type called tpkg.T, and
// no import for tpkg. With no local T the package did not compile, behind a zero
// exit code; with one -- because the referring document happened to declare a
// namesake -- it compiled and judged the branch against the local schema, which
// is a false rejection and a false acceptance from the same line. Issue #306,
// and the third time this project has met a question answered in some positions
// and not in the one beside them (#295 for the map value, #299 for the document
// root).
//
// So the sites are enumerated from the templates rather than from memory, and
// every one of them is classified here. Adding an action in a Go type position
// to any template fails TestEveryTypeNameEmissionSiteIsAccountedFor until it has
// an entry; classifying it nameForeignPossible then fails
// TestEveryForeignPossibleSiteQualifiesAForeignName until a schema in this file
// drives a cross-package $ref through it and the name comes out qualified.

type nameKind int

const (
	// nameGoType: rendered through the goType funnel, which is total -- a
	// NamedType's GoTypeName() prepends the import alias. Nothing to decide.
	nameGoType nameKind = iota
	// nameNotAType: the action is not a type at all -- a method name, or a
	// receiver being dereferenced.
	nameNotAType
	// nameLocalDecl: a name this generator minted for a declaration in the very
	// file being emitted. It cannot name another package's type, because the
	// declaration it names is in this one.
	nameLocalDecl
	// namePrimitive: a built-in Go type derived from a JSON type, never a
	// generated type name.
	namePrimitive
	// nameForeignPossible: a generated type's name, which may be another
	// package's. These are the sites the qualification question is about.
	nameForeignPossible
)

type emissionSite struct {
	Kind nameKind
	// Holder and Field name the IR field the action reads, for
	// nameForeignPossible sites. The behavioural gate keys on them.
	Holder reflect.Type
	Field  string
	Why    string
}

func fieldOf(v any, name string) (reflect.Type, string) { return reflect.TypeOf(v), name }

// typeNameEmissionSites is keyed by "<template file> | <action>" -- the action
// exactly as the template writes it, so the same field read in two different
// contexts is two entries and each is classified on its own.
var typeNameEmissionSites = map[string]emissionSite{
	// ---- the goType funnel ----
	"alias.go.tmpl | goType .LeafDecode.ShadowType": {Kind: nameGoType},
	"oneof.go.tmpl | goType .Type":                  {Kind: nameGoType},
	"set_defaults.go.tmpl | goType .Type":           {Kind: nameGoType},
	// struct.go.tmpl no longer spells the overflow map out: the whole member is
	// a StructMember whose Type is the map, so `goType .Type` renders it -- and
	// renders every other member of the struct too. Still the same funnel, one
	// call further out.
	"unmarshal.go.tmpl | goType $struct.AdditionalProperties.LeafDecode.ShadowType": {Kind: nameGoType},
	"unmarshal.go.tmpl | goType $struct.AdditionalProperties.ValueType":             {Kind: nameGoType},
	"unmarshal.go.tmpl | goType $v.LeafDecode.ShadowType":                           {Kind: nameGoType},
	"unmarshal.go.tmpl | goType $v.Type":                                            {Kind: nameGoType},
	"unmarshal.go.tmpl | goType .LeafDecode.ShadowType":                             {Kind: nameGoType},

	// ---- not a type ----
	"alias.go.tmpl | $recv":                   {Kind: nameNotAType, Why: "`*<recv> = ...` assigns through the receiver; the star is a dereference"},
	"enum.go.tmpl | $recv":                    {Kind: nameNotAType, Why: "as alias.go.tmpl: a dereferenced receiver, not a type"},
	"validation.go.tmpl | $recv":              {Kind: nameNotAType, Why: "as alias.go.tmpl: a dereferenced receiver, not a type"},
	"inferred_alias.go.tmpl | .AccessorName":  {Kind: nameNotAType, Why: "the name of the accessor method being declared"},
	"inferred_alias.go.tmpl | .TypeCheckName": {Kind: nameNotAType, Why: "the name of the predicate method being declared"},
	"oneof.go.tmpl | $oneof.InterfaceName":    {Kind: nameNotAType, Why: "the sealing method's name, declared on the interface and on each wrapper"},
	"oneof.go.tmpl | .FieldName":              {Kind: nameNotAType, Why: "spliced into the Get<Field>() method name"},

	// ---- a declaration in the file being emitted ----
	"alias.go.tmpl | .Name":                  {Kind: nameLocalDecl, Why: "the alias this template is declaring"},
	"annotation_schema.go.tmpl | .Name":      {Kind: nameLocalDecl, Why: "the type this template is declaring"},
	"bigint_alias.go.tmpl | .Name":           {Kind: nameLocalDecl, Why: "the type this template is declaring"},
	"dynamic_schema.go.tmpl | .Name":         {Kind: nameLocalDecl, Why: "the type this template is declaring"},
	"enum.go.tmpl | .Name":                   {Kind: nameLocalDecl, Why: "the enum this template is declaring"},
	"inferred_alias.go.tmpl | .Name":         {Kind: nameLocalDecl, Why: "the wrapper this template is declaring"},
	"not_schema.go.tmpl | .Name":             {Kind: nameLocalDecl, Why: "the type this template is declaring"},
	"type_only_schema.go.tmpl | .Name":       {Kind: nameLocalDecl, Why: "the wrapper this template is declaring"},
	"marshal.go.tmpl | .WrapperName":         {Kind: nameLocalDecl, Why: "a oneOf variant wrapper, minted and declared by this package"},
	"oneof.go.tmpl | .WrapperName":           {Kind: nameLocalDecl, Why: "a oneOf variant wrapper, minted and declared by this package"},
	"validation.go.tmpl | $v.WrapperName":    {Kind: nameLocalDecl, Why: "a oneOf variant wrapper, minted and declared by this package"},
	"oneof.go.tmpl | $parent":                {Kind: nameLocalDecl, Why: "the struct the oneOf group hangs off, declared here"},
	"set_defaults.go.tmpl | $struct.Name":    {Kind: nameLocalDecl, Why: "the struct this method is declared on"},
	"unmarshal.go.tmpl | $struct.Name":       {Kind: nameLocalDecl, Why: "the struct this method is declared on"},
	"unmarshal.go.tmpl | $oof.InterfaceName": {Kind: nameLocalDecl, Why: "the sealed interface minted for this struct's oneOf group, declared in this file"},

	// ---- a JSON type's Go primitive ----
	"validation.go.tmpl | $uneval.ValueType": {
		Kind: namePrimitive,
		Why:  "buildUnevaluatedPropertiesDef fills it from primitiveTypeFromSchema, so it is string/float64/bool and never a generated name",
	},

	// ---- the sites the qualification question is about ----
	"alias.go.tmpl | .UnmarshalAs": siteOf(generator.AliasDef{}, "UnmarshalAs", "an alias over another package's type inherits none of its methods and has to convert to the qualified name"),
	"alias.go.tmpl | .MarshalAs":   siteOf(generator.AliasDef{}, "MarshalAs", "as UnmarshalAs"),
	"alias.go.tmpl | .ValidateAs":  siteOf(generator.AliasDef{}, "ValidateAs", "as UnmarshalAs"),
	"inferred_alias.go.tmpl | .ValidateAs": siteOf(generator.InferredAliasDef{}, "ValidateAs",
		"emitted as a conversion of the wrapper's value; a local namesake would convert cleanly and dispatch the wrong Validate"),
	"inferred_alias.go.tmpl | .ItemsTypeName": siteOf(generator.InferredAliasDef{}, "ItemsTypeName",
		"the element type an inferred array decodes each item into"),
	"inferred_alias.go.tmpl | .AdditionalItemsTypeName": siteOf(generator.InferredAliasDef{}, "AdditionalItemsTypeName",
		"the type the positions past a tuple's prefix decode into"),
	"inferred_alias.go.tmpl | $item.TypeName": siteOf(generator.InferredTupleItem{}, "TypeName",
		"one tuple position of an inferred array"),
	"inferred_alias.go.tmpl | .Contains.TypeName": siteOf(generator.ContainsDef{}, "TypeName",
		"the type a `contains` element is decoded into and validated through"),
	"type_only_schema.go.tmpl | $branch.TypeName": siteOf(generator.TypeSchemaBranch{}, "TypeName",
		"one alternative of a raw-value wrapper -- an anyOf branch, a draft-3 schema-valued type, a member of a multi-type union. Issue #306 is this site"),
	"validation.go.tmpl | $branch.TypeName": siteOf(generator.BranchOverflowCheck{}, "TypeName",
		"the type an unaccounted property is decoded into for a branch's additionalProperties/unevaluatedProperties"),
	"validation.go.tmpl | $pp.TypeName": siteOf(generator.PatternPropertyDef{}, "TypeName",
		"the type a patternProperties bucket decodes a matched value into"),
	"validation.go.tmpl | .Def.TypeName": siteOf(generator.ContainsDef{}, "TypeName",
		"the `contains` of an array *property*, which never became a named type of its own"),
	"validation.go.tmpl | .TypeName": siteOf(generator.TupleItemDef{}, "TypeName",
		"one tuple position of an array *property*"),
}

func siteOf(holder any, field, why string) emissionSite {
	t, f := fieldOf(holder, field)
	return emissionSite{Kind: nameForeignPossible, Holder: t, Field: f, Why: why}
}

// ---------------------------------------------------------------------------
// The scan.
// ---------------------------------------------------------------------------

var (
	tmplVarDecl  = regexp.MustCompile(`\bvar\s+[A-Za-z_]\w*\s+$`)
	tmplSelector = regexp.MustCompile(`^\$?[A-Za-z_]\w*(\.\w+)+$|^(\.\w+)+$`)
	tmplMapKey   = regexp.MustCompile(`map\[[^\]]*\]$`)
)

// scanTypePositions reads the templates and returns every action that lands in
// a Go type position, keyed the way typeNameEmissionSites is.
//
// Four shapes, and between them they cover every way a template can put a name
// where the Go parser expects a type: the declared type of a `var`, a
// conversion, the element of a slice or map, and a pointer. A dereferenced
// receiver looks like the last of those and is classified nameNotAType rather
// than filtered out here, deliberately: a filter is a place for a real site to
// disappear, and the classification is on the record instead.
func scanTypePositions(t *testing.T) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir("templates")
	if err != nil {
		t.Fatalf("reading templates: %v", err)
	}
	found := map[string][]string{}
	files := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".tmpl" {
			continue
		}
		files++
		raw, err := os.ReadFile(filepath.Join("templates", e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		src := string(raw)
		for i := 0; i < len(src); {
			open := strings.Index(src[i:], "{{")
			if open < 0 {
				break
			}
			open += i
			end := strings.Index(src[open:], "}}")
			if end < 0 {
				break
			}
			end += open
			action := strings.TrimSpace(strings.Trim(strings.TrimSpace(src[open+2:end]), "-"))
			before := src[strings.LastIndexByte(src[:open], '\n')+1 : open]
			var after byte
			if end+2 < len(src) {
				after = src[end+2]
			}
			shape := ""
			switch {
			case tmplVarDecl.MatchString(before):
				shape = "var"
			case after == '(' && tmplSelector.MatchString(action):
				shape = "conversion"
			case strings.HasSuffix(before, "*"):
				shape = "pointer"
			case strings.HasSuffix(before, "[]"):
				shape = "slice element"
			case tmplMapKey.MatchString(before):
				shape = "map value"
			}
			if shape != "" {
				key := e.Name() + " | " + action
				if !containsString(found[key], shape) {
					found[key] = append(found[key], shape)
				}
			}
			i = end + 2
		}
	}
	if files < 20 {
		t.Fatalf("only %d templates read; the scan has stopped seeing what it reads and would pass whatever the templates say", files)
	}
	if len(found) < 40 {
		t.Fatalf("only %d type positions found across %d templates; the scan is no longer matching the source it reads",
			len(found), files)
	}
	return found
}

// TestEveryTypeNameEmissionSiteIsAccountedFor holds the enumeration against the
// templates in both directions: nothing written into a Go type position is
// unclassified, and nothing classified has gone away.
func TestEveryTypeNameEmissionSiteIsAccountedFor(t *testing.T) {
	found := scanTypePositions(t)
	for _, key := range sortedKeys(found) {
		site, ok := typeNameEmissionSites[key]
		if !ok {
			t.Errorf("%s writes a name into a Go %s position and is classified nowhere.\n"+
				"Every such site decides, on its own, whether another package's type is spelled with its import "+
				"alias there. The one that got it wrong emitted `var _bv T` for a tpkg.T and no import: no local T "+
				"and the package did not compile behind a zero exit code, a local T and it silently validated "+
				"against the wrong schema (issue #306).\n"+
				"Add an entry to typeNameEmissionSites saying which it is.", key, strings.Join(found[key], "/"))
			continue
		}
		if site.Kind != nameGoType && strings.TrimSpace(site.Why) == "" {
			t.Errorf("%s is classified with no reason given; an entry with no reason records nothing", key)
		}
		if site.Kind == nameForeignPossible {
			if site.Holder == nil || site.Field == "" {
				t.Errorf("%s is a foreign-possible site with no IR field named; the behavioural gate has nothing to check", key)
				continue
			}
			f, ok := site.Holder.FieldByName(site.Field)
			if !ok {
				t.Errorf("%s names %s.%s, which that type does not declare", key, site.Holder.Name(), site.Field)
			} else if f.Type.Kind() != reflect.String {
				t.Errorf("%s names %s.%s, which is a %s and not the string this site renders",
					key, site.Holder.Name(), site.Field, f.Type)
			}
		}
	}
	for _, key := range sortedKeys(typeNameEmissionSites) {
		if _, ok := found[key]; !ok {
			t.Errorf("typeNameEmissionSites classifies %q, which no template writes into a type position any more. "+
				"A stale entry is a claim that a site has been thought about, which is what this table exists to "+
				"make trustworthy.", key)
		}
	}
}

// ---------------------------------------------------------------------------
// The behavioural half.
// ---------------------------------------------------------------------------

// foreignNamePrefix is carried by every type the owner document declares, so a
// walk of the referring package's IR can tell a foreign name from a local one
// by looking at it. The referring documents below declare nothing with this
// prefix.
const foreignNamePrefix = "Foreign"

var (
	bareForeign      = regexp.MustCompile(`^Foreign\w*$`)
	qualifiedForeign = regexp.MustCompile(`^ownpkg\.Foreign\w*$`)
)

// ownerDocument declares one named definition per shape a delegating position
// can reach: a string with a keyword on it, an object, and an array.
//
// The two ForeignDyn* entries carry a $dynamicAnchor so that a referring
// document can reach them with a *bookended* $dynamicRef, which is the only
// spelling for which the dynamic scope is consulted at all. Their anchor names
// are chosen so that the Go name a referring package would derive from the
// reference text is Foreign-prefixed like the rest: that is what lets the walk
// below tell a copy minted for the foreign shape from a name of this package's
// own. Separate definitions rather than anchors on the existing ones, so that
// adding them cannot move what the $ref documents above generate.
const ownerDocument = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://ex.test/own.json",
  "title": "ForeignRoot",
  "type": "object",
  "properties": {"anchor": {"type": "string"}},
  "$defs": {
    "ForeignStr": {"type": "string", "minLength": 3},
    "ForeignObj": {"type": "object", "properties": {"x": {"type": "string"}}, "required": ["x"]},
    "ForeignArr": {"type": "array", "items": {"type": "string"}, "minItems": 1},
    "ForeignUnion": {"oneOf": [{"type": "string", "minLength": 3}, {"type": "integer", "minimum": 5}]},
    "ForeignAny": {},
    "ForeignDynStr": {"$dynamicAnchor": "foreignDynStr", "type": "string", "minLength": 3},
    "ForeignDynObj": {"$dynamicAnchor": "foreignDynObj", "type": "object",
                      "properties": {"x": {"type": "string"}}, "required": ["x"]}
  }
}`

// referringDocuments drive a cross-package $ref through each delegating
// position. Named for the position rather than for the field, because the
// position is what a schema author writes and the field is where it lands.
var referringDocuments = map[string]string{
	"anyof_branch_in_a_property": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r1.json","title":"R1",
	  "type":"object",
	  "properties":{"a":{"anyOf":[{"$ref":"https://ex.test/own.json#/$defs/ForeignStr"},{"type":"integer"}]}}}`,

	"type_union_beside_a_type_scoped_keyword": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r2.json","title":"R2",
	  "type":"object",
	  "properties":{"a":{"type":["string","array"],"items":{"$ref":"https://ex.test/own.json#/$defs/ForeignStr"},"minLength":3}}}`,

	"pattern_properties": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r3.json","title":"R3",
	  "type":"object",
	  "patternProperties":{"^k":{"$ref":"https://ex.test/own.json#/$defs/ForeignObj"}}}`,

	"prefix_items_of_a_property": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r4.json","title":"R4",
	  "type":"object",
	  "properties":{"a":{"type":"array","prefixItems":[{"$ref":"https://ex.test/own.json#/$defs/ForeignStr"},{"type":"integer"}]}}}`,

	"contains_of_a_property": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r5.json","title":"R5",
	  "type":"object",
	  "properties":{"a":{"type":"array","contains":{"$ref":"https://ex.test/own.json#/$defs/ForeignObj"}}}}`,

	"branch_overflow": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r6.json","title":"R6",
	  "type":"object","properties":{"x":{"type":"string"}},
	  "allOf":[{"additionalProperties":{"$ref":"https://ex.test/own.json#/$defs/ForeignObj"}}]}`,

	"document_root_is_the_reference": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r7.json","title":"R7",
	  "$ref":"https://ex.test/own.json#/$defs/ForeignObj"}`,

	"inferred_array_items": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r8.json","title":"R8",
	  "type":"object",
	  "properties":{"a":{"$ref":"#/$defs/Bag"}},
	  "$defs":{"Bag":{"items":{"$ref":"https://ex.test/own.json#/$defs/ForeignObj"},"minItems":1}}}`,

	"inferred_array_prefix_items_and_tail": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r9.json","title":"R9",
	  "type":"object",
	  "properties":{"a":{"$ref":"#/$defs/Bag"}},
	  "$defs":{"Bag":{"prefixItems":[{"$ref":"https://ex.test/own.json#/$defs/ForeignStr"}],
	                  "items":{"$ref":"https://ex.test/own.json#/$defs/ForeignObj"},"minItems":1}}}`,

	"inferred_array_contains": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r10.json","title":"R10",
	  "type":"object",
	  "properties":{"a":{"$ref":"#/$defs/Bag"}},
	  "$defs":{"Bag":{"contains":{"$ref":"https://ex.test/own.json#/$defs/ForeignObj"},"minItems":1}}}`,

	// A draft-3 schema-valued "type" entry. It reaches TypeSchemaBranch.TypeName
	// by a different route from the anyOf above -- extractTypeSchemaBranches
	// rather than delegatedBranchType -- and a fix applied to one of the two
	// leaves the other exactly as it was.
	"draft3_schema_valued_type_entry": `{
	  "$schema":"http://json-schema.org/draft-03/schema#","id":"https://ex.test/r13.json","title":"R13",
	  "type":"object",
	  "properties":{"a":{"type":[{"$ref":"https://ex.test/own.json#/$defs/ForeignObj"},"integer"]}}}`,

	// A target whose sub-schema names no single JSON type. The ladder the item
	// and tuple positions ask first declines it, so the arms behind it -- the
	// ones that build a name out of the reference string -- are the ones that
	// answer, and they are a second place the same question is put.
	"inferred_array_items_of_a_typeless_target": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r14.json","title":"R14",
	  "type":"object",
	  "properties":{"a":{"$ref":"#/$defs/Bag"}},
	  "$defs":{"Bag":{"items":{"$ref":"https://ex.test/own.json#/$defs/ForeignUnion"},"minItems":1}}}`,

	"inferred_array_prefix_items_of_a_typeless_target": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r15.json","title":"R15",
	  "type":"object",
	  "properties":{"a":{"$ref":"#/$defs/Bag"}},
	  "$defs":{"Bag":{"prefixItems":[{"$ref":"https://ex.test/own.json#/$defs/ForeignUnion"}],"minItems":1}}}`,

	// A target that constrains nothing: `type ForeignAny any`, which Go permits
	// no methods on, so the positions below cannot delegate to it and have to
	// say so rather than mint a name for it.
	"inferred_array_items_of_a_methodless_target": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r18.json","title":"R18",
	  "type":"object",
	  "properties":{"a":{"$ref":"#/$defs/Bag"}},
	  "$defs":{"Bag":{"items":{"$ref":"https://ex.test/own.json#/$defs/ForeignAny"},"minItems":1}}}`,

	"inferred_array_prefix_items_of_a_methodless_target": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r19.json","title":"R19",
	  "type":"object",
	  "properties":{"a":{"$ref":"#/$defs/Bag"}},
	  "$defs":{"Bag":{"prefixItems":[{"$ref":"https://ex.test/own.json#/$defs/ForeignAny"}],"minItems":1}}}`,

	"tuple_slot_of_a_typeless_target": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r16.json","title":"R16",
	  "type":"object",
	  "properties":{"a":{"type":"array","prefixItems":[{"$ref":"https://ex.test/own.json#/$defs/ForeignUnion"}]}}}`,

	"pattern_properties_of_a_typeless_target": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r17.json","title":"R17",
	  "type":"object",
	  "patternProperties":{"^u":{"$ref":"https://ex.test/own.json#/$defs/ForeignUnion"}}}`,

	// The delegation an array alias merged out of an allOf takes to the branch
	// that names it. This one is here for what it must *not* produce: the branch
	// is another package's, and the name that used to come out of it was that
	// package's type spelled bare, beside a local re-declaration of it. See
	// firstAllOfArrayAliasName.
	"allof_over_a_foreign_array_alias": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r11.json","title":"R11",
	  "type":"object",
	  "properties":{"a":{"$ref":"#/$defs/Bag"}},
	  "$defs":{"Bag":{"allOf":[{"$ref":"https://ex.test/own.json#/$defs/ForeignArr"}],"minItems":2}}}`,

	"allof_over_a_foreign_array_alias_inferred": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r12.json","title":"R12",
	  "type":"object",
	  "properties":{"a":{"$ref":"#/$defs/Bag"}},
	  "$defs":{"Bag":{"allOf":[{"$ref":"https://ex.test/own.json#/$defs/ForeignArr"}],"uniqueItems":true}}}`,

	// The same positions again, written with a bookended $dynamicRef instead of
	// a $ref. Until issue #325 the probe set held no dynamic reference at all,
	// so the qualification question was never put to the arms that answer one --
	// and those arms answered it wrongly: the referring package declared its own
	// copy of the foreign shape and imported nothing, which compiles and leaves
	// two Go types for one JSON shape (issue #299's shape, in the one arm #302
	// deliberately left). A gate that enumerates positions and not keywords has
	// exactly this hole, so the keyword is enumerated here too.
	//
	// Bookended, deliberately: the target carries the very anchor the reference
	// names, which is what makes the dynamic scope be consulted rather than the
	// reference being a $ref with a longer name. Only one resource in reach
	// declares each anchor, so the target is fixed and the static path keeps the
	// schema instead of handing it to the runtime evaluator.
	"document_root_is_the_dynamic_reference": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r20.json","title":"R20",
	  "$dynamicRef":"https://ex.test/own.json#foreignDynObj"}`,

	"anyof_branch_in_a_property_by_dynamic_reference": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r21.json","title":"R21",
	  "type":"object",
	  "properties":{"a":{"anyOf":[{"$dynamicRef":"https://ex.test/own.json#foreignDynStr"},{"type":"integer"}]}}}`,

	"pattern_properties_by_dynamic_reference": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r22.json","title":"R22",
	  "type":"object",
	  "patternProperties":{"^k":{"$dynamicRef":"https://ex.test/own.json#foreignDynObj"}}}`,

	"branch_overflow_by_dynamic_reference": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r23.json","title":"R23",
	  "type":"object","properties":{"x":{"type":"string"}},
	  "allOf":[{"additionalProperties":{"$dynamicRef":"https://ex.test/own.json#foreignDynObj"}}]}`,

	"property_element_and_map_value_by_dynamic_reference": `{
	  "$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/r24.json","title":"R24",
	  "type":"object",
	  "properties":{
	    "p":{"$dynamicRef":"https://ex.test/own.json#foreignDynStr"},
	    "s":{"type":"array","items":{"$dynamicRef":"https://ex.test/own.json#foreignDynStr"}},
	    "m":{"type":"object","additionalProperties":{"$dynamicRef":"https://ex.test/own.json#foreignDynObj"}}}}`,
}

// sitesNotProbed records foreign-possible sites no schema here reaches, and
// why.
//
// An entry withdraws only the *coverage* claim, not the check: the walk below
// judges every foreign-possible field of every document generated here, so a
// site listed as unprobed is still held to the rule on every one of them. What
// is missing is a document known to drive a foreign name into it, and therefore
// the assurance that the rule has been seen to apply there.
var sitesNotProbed = map[string]string{
	"InferredAliasDef.ValidateAs": "no schema found reaches it with another package's type. Both producers decline " +
		"first: populateAliasDelegates' inferred arm reads the wrapper's own InferredGoType, which the arms that " +
		"build an InferredAliasDef fill with a primitive, a slice or a map and never with a foreign NamedType; and " +
		"firstAllOfArrayAliasValidateAs skips a branch another package owns outright. A schema that reaches it " +
		"would be welcome here -- this is a gap in the probe, not a claim that the field is safe.",
}

// TestEveryForeignPossibleSiteQualifiesAForeignName is the half that fails
// under the defect rather than under a refactor.
//
// It generates a two-package run for real -- an owning package that declares the
// Foreign* types, then a referring package whose schema drives a $ref into each
// delegating position -- and reads the referring package's IR back. Every field
// classified nameForeignPossible above must be seen holding the foreign type's
// name, and every one of them must be seen holding it *qualified*. A bare
// `ForeignStr` in any of those fields is the defect: it is `var _bv ForeignStr`
// in a package that declares no such type, or, where it happens to declare a
// namesake, a check against the wrong schema.
func TestEveryForeignPossibleSiteQualifiesAForeignName(t *testing.T) {
	owner := parseDoc(t, ownerDocument)

	seenQualified := map[string]bool{}
	for _, name := range sortedKeys(referringDocuments) {
		name, body := name, referringDocuments[name]
		t.Run(name, func(t *testing.T) {
			file := generateAgainstOwner(t, owner, body)
			for _, hit := range walkTypeNameFields(file) {
				switch {
				case qualifiedForeign.MatchString(hit.Value):
					seenQualified[hit.Key] = true
				case bareForeign.MatchString(hit.Value):
					t.Errorf("%s holds %q -- another package's type name, with its import alias dropped.\n"+
						"Generated source spells a foreign type with its qualifier; without it the package does not "+
						"compile, or, where it declares a namesake, compiles and enforces the wrong schema in "+
						"silence (issue #306). Produce this field with emittedTypeName or foreignDelegateTypeName.\n"+
						"If instead this position deliberately materialized a local copy of the target -- the "+
						"exception the document-root arm makes for a type whose methods an alias would not carry -- "+
						"then the copy has taken the owning package's name for it, which is the duplicate "+
						"declaration of issue #299; give the copy a name of this package's own.",
						hit.Key, hit.Value)
				}
			}
		})
	}

	for _, key := range sortedKeys(typeNameEmissionSites) {
		site := typeNameEmissionSites[key]
		if site.Kind != nameForeignPossible {
			continue
		}
		field := site.Holder.Name() + "." + site.Field
		if seenQualified[field] {
			continue
		}
		if reason, declined := sitesNotProbed[field]; declined {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("%s is recorded as unprobed with no reason given", field)
			}
			continue
		}
		t.Errorf("no schema here drove a cross-package $ref through %s (read by %s), so nothing checked what that "+
			"site does with a foreign name. Add a document to referringDocuments that reaches the position, or "+
			"record in sitesNotProbed why it cannot be reached.", field, key)
	}
}

// hit is one string field of the IR that a template renders as a type.
type hit struct {
	Key   string // "<struct>.<field>"
	Value string
}

// walkTypeNameFields walks everything reachable from the generated file and
// returns every value of a field classified nameForeignPossible.
//
// Reflection rather than a hand-written descent per position: the positions are
// exactly what keeps being added one at a time, and a descent that knows about
// some of them is the shape of the defect this file is about.
func walkTypeNameFields(file *generator.File) []hit {
	want := map[string]bool{}
	for _, site := range typeNameEmissionSites {
		if site.Kind == nameForeignPossible {
			want[site.Holder.Name()+"."+site.Field] = true
		}
	}
	var out []hit
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Ptr, reflect.Interface:
			if v.IsNil() {
				return
			}
			if v.Kind() == reflect.Ptr {
				if seen[v.Pointer()] {
					return
				}
				seen[v.Pointer()] = true
			}
			walk(v.Elem())
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Map:
			for _, k := range v.MapKeys() {
				walk(v.MapIndex(k))
			}
		case reflect.Struct:
			tn := v.Type().Name()
			for i := 0; i < v.NumField(); i++ {
				f := v.Type().Field(i)
				fv := v.Field(i)
				key := tn + "." + f.Name
				if want[key] && fv.Kind() == reflect.String {
					if s := fv.String(); s != "" {
						out = append(out, hit{Key: key, Value: s})
					}
					continue
				}
				if !f.IsExported() {
					continue
				}
				walk(fv)
			}
		}
	}
	walk(reflect.ValueOf(file))
	return out
}

// generateAgainstOwner runs the owning package first so it registers its types,
// then generates the referring document into a package of its own, and returns
// the referring package's IR.
func generateAgainstOwner(t *testing.T, owner *schema.Schema, referringBody string) *generator.File {
	t.Helper()
	referrer := parseDoc(t, referringBody)

	const (
		ownerPath    = "ex.test/m/ownpkg"
		referrerPath = "ex.test/m/refpkg"
	)
	registry := generator.NewCrossPackageRegistry(map[string]string{
		"https://ex.test/own.json": ownerPath,
	})
	registry.RegisterDocument(owner, ownerPath)
	registry.RegisterDocument(referrer, referrerPath)
	registry.DocPackages[docID(referrer)] = referrerPath

	resolver := schema.NewMappingResolver(map[string]*schema.Schema{
		"https://ex.test/own.json": owner,
		docID(referrer):            referrer,
	})

	ownerGen := generator.New(generator.Config{
		PackageName:  "ownpkg",
		ImportPath:   ownerPath,
		CrossPackage: registry,
		Validation:   generator.ValidationModeStatic,
		Resolver:     resolver,
	})
	if _, err := ownerGen.Generate(owner, generator.WithRootTypeName("ForeignRoot")); err != nil {
		t.Fatalf("generating the owning package: %v", err)
	}

	refGen := generator.New(generator.Config{
		PackageName:  "refpkg",
		ImportPath:   referrerPath,
		CrossPackage: registry,
		Validation:   generator.ValidationModeStatic,
		Resolver:     resolver,
	})
	file, err := refGen.Generate(referrer)
	if err != nil {
		t.Fatalf("generating the referring package: %v", err)
	}
	return file
}

func docID(s *schema.Schema) string { return s.ID }

func parseDoc(t *testing.T, body string) *schema.Schema {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("parsing document: %v\n%s", err, body)
	}
	s.Normalize()
	s.ComputeBaseURIs(nil, &s)
	return &s
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
