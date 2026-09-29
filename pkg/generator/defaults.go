package generator

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// The DefaultShape values a field can carry, and the arm of the SetDefaults
// template each selects. The empty shape is the last: one of the built-in
// scalars or a pointer to one, which the template picks out by the field's own
// Go type name.
//
// What they choose between is how the *field* is tested for being untouched: a
// slice and a map are compared against nil, a raw-value wrapper asked IsZero,
// everything else compared against its own zero, and they are not
// interchangeable because neither a slice nor a wrapper holding one is
// comparable at all. Whether the document's key set is consulted beside that
// test is a different question, and FieldDef.DefaultAsksJSONKeys answers it.
const (
	defaultShapeNamed        = "named"      // a conversion or composite literal written into a bare named type
	defaultShapePointerNamed = "*named"     // the same, written through a pointer
	defaultShapeCollection   = "collection" // a slice or a map literal, named or not
	defaultShapeIsZero       = "iszero"     // a raw-value wrapper, empty exactly when IsZero says so
	// defaultShapeDecoded is a default no literal spells -- a struct, a
	// time.Time or netip.Addr, a collection of pointers, a oneOf group -- and
	// that is planted by decoding: DefaultLiteral is the Go string literal of
	// the one-member document {"<property>": <default>}, which SetDefaults
	// decodes into a fresh value of the parent and copies the field out of. The
	// field is then exactly what a document carrying the default leaves it --
	// its own _jsonKeys, overflow maps and raw members included -- which no
	// composite literal can say.
	defaultShapeDecoded = "decoded"
)

// # The policy
//
// "default" is an annotation. 2020-12 section 9.2 says it "SHOULD be valid
// against the associated schema" and nothing more, so a generator is free to
// decline one and is not free to plant one the schema refuses: a value written
// into a document that did not carry the property turns a valid document into an
// invalid one. SetDefaults therefore plants a default only when both hold:
//
//  1. the field's Go type can hold it -- SetDefaults leaves the field exactly as
//     decoding the default's JSON into it would, and the value writes back out
//     as the same JSON. Where a literal spells the value it is written as one;
//     where none does, the default is planted by decoding it (see
//     defaultShapeDecoded and decodeHolds); and
//  2. it is valid against the schemas that describe the property's location on
//     every document -- judged here, at generation time, by judgeValue, and
//     where that cannot decide, judged again by the runtime evaluator when
//     SetDefaults runs (DefaultJudge).
//
// A default that fails either is not planted, and never fails generation: a
// legal schema whose default is unusable is still a legal schema. It is reported
// instead, with where the default was written and why it was declined (see
// SkippedDefault), which is the difference from the silence there used to be.
// Before this, which of three things a bad default got depended on how it was
// bad: a string or an array on an integer was dropped without a word, 4.5 or
// 1e30 on an integer and 1e400 on a number refused to generate a schema that is
// perfectly legal, and a value outside the property's enum was planted, so that
// SetDefaults made `{}` invalid.

// defaultCandidate is a property's "default" on its way to SetDefaults: the
// value, the node that states it, and what the generation-time judge said.
type defaultCandidate struct {
	value  any
	source *schema.Schema
	// schemas are what the value was judged against; undecided says the judge
	// could not decide, and the runtime evaluator is asked instead.
	schemas   []*schema.Schema
	undecided bool
	// property is the JSON name, for the diagnostic.
	property string
}

// SkippedDefault is a "default" SetDefaults does not plant, and why.
type SkippedDefault struct {
	TypeName string // the generated struct the property is a field of
	Property string // the JSON property name
	// Location is where the document wrote the default, as a URI reference --
	// a bare fragment for the input document -- or empty for a schema no
	// document wrote.
	Location string
	Reason   string
}

// SkippedDefaults returns the defaults the last Generate call declined to
// plant, in the order the fields were generated.
func (g *Generator) SkippedDefaults() []SkippedDefault {
	return g.skippedDefaults
}

// noteSkippedDefault records a default SetDefaults will not plant.
func (g *Generator) noteSkippedDefault(typeName string, c *defaultCandidate, why string) {
	loc := ""
	if c.source != nil {
		if l, ok := (docLocator{home: g.homeDoc}).name(c.source); ok {
			loc = below(l, "default")
		}
	}
	for _, s := range g.skippedDefaults {
		if s.TypeName == typeName && s.Property == c.property {
			return
		}
	}
	g.skippedDefaults = append(g.skippedDefaults, SkippedDefault{TypeName: typeName, Property: c.property, Location: loc, Reason: why})
}

// defaultCandidateFor finds the default SetDefaults would write for owner's
// property name and judges it (see judgeValue). It returns nil where there is
// none, and where the value is invalid at the property's location, which is
// then reported.
func (g *Generator) defaultCandidateFor(typeName string, owner *schema.Schema, name string, propSchema *schema.Schema) *defaultCandidate {
	value, source := g.propertyDefault(owner, name, propSchema)
	if value == nil {
		return nil
	}
	c := &defaultCandidate{value: *value, source: source, property: name}
	schemas, keyChecks, undecided := g.defaultLocationSchemas(owner, name, propSchema)
	// The key itself: a propertyNames that refuses it refuses every document
	// the default would be written into.
	keyVerdict := g.judgeValue(keyChecks, name)
	switch keyVerdict.j {
	case judgedInvalid:
		g.noteSkippedDefault(typeName, c, fmt.Sprintf("the property name %q is invalid where it is written: %s", name, keyVerdict.why))
		return nil
	case judgedUnknown:
		g.noteSkippedDefault(typeName, c, fmt.Sprintf("whether the property name %q is valid where it is written could not be decided", name))
		return nil
	}
	if undecided != "" {
		g.noteSkippedDefault(typeName, c, undecided)
		return nil
	}
	v := g.judgeValue(schemas, c.value)
	switch v.j {
	case judgedInvalid:
		g.noteSkippedDefault(typeName, c, describeJSONValue(c.value)+" is not valid where it is written: "+v.why)
		return nil
	case judgedUnknown:
		c.undecided = true
	}
	c.schemas = schemas
	return c
}

// defaultLocationSchemas is every schema a value at owner's property name is
// judged against on every document: the property's own schemas (narrowed by
// the merge record, as propertyDefault reads them), the property schemas of
// owner's unconditional reach, each patternProperties value whose pattern
// matches the name, and the additionalProperties -- and, where a branch could
// leave the member unevaluated, the unevaluatedProperties -- of any object in
// that reach that does not claim it. keyChecks are the propertyNames the name
// itself must satisfy.
//
// undecided is non-empty where the set itself cannot be known: a pattern the
// engine cannot decide for the name.
//
// What it does not judge is the rest of the object. A dependentRequired, a
// maxProperties or a branch that turns on which members are present sees the
// default as one more member, and whether that makes the object invalid depends
// on the document it is planted into, which generation never sees.
func (g *Generator) defaultLocationSchemas(owner *schema.Schema, name string, propSchema *schema.Schema) (schemas, keyChecks []*schema.Schema, undecided string) {
	sources, narrowed := g.unconditionalPropertySchemas(owner, name)
	if !narrowed {
		sources = []*schema.Schema{propSchema}
	}
	schemas = append(schemas, sources...)
	for _, r := range g.unconditionalReachAt(g.docSchemaFor(owner), true) {
		claimed := false
		if p, ok := r.Properties[name]; ok {
			claimed = true
			schemas = append(schemas, p)
		}
		for _, pat := range sortedKeys(r.PatternProperties) {
			matched, err := PatternMatches(pat, name)
			if err != nil {
				return nil, nil, fmt.Sprintf("whether the pattern %q applies to %q could not be decided", pat, name)
			}
			if matched {
				claimed = true
				schemas = append(schemas, r.PatternProperties[pat])
			}
		}
		if !claimed && r.AdditionalProperties != nil {
			schemas = append(schemas, r.AdditionalProperties.AsSchema())
		}
		if r.UnevaluatedProperties != nil {
			if except, exceptPatterns, some := g.unevaluatedMembers(r, false); some &&
				accessStepReachesKey(AccessStep{Kind: AccessOther, Except: except, ExceptPatterns: exceptPatterns}, name) {
				schemas = append(schemas, r.UnevaluatedProperties)
			}
		}
		if r.PropertyNames != nil {
			keyChecks = append(keyChecks, r.PropertyNames)
		}
	}
	return schemas, keyChecks, ""
}

// defaultJudgeFor settles a default the generation-time judge could not decide:
// the schemas it was judged against are compiled for the runtime evaluator, and
// SetDefaults asks it before planting. It returns the package variable and the
// default as the tree the evaluator reads, both empty for a default already
// known to be valid, and false where the evaluator declines the schemas, which
// is reported -- a default nobody can judge is not planted.
func (g *Generator) defaultJudgeFor(typeName, goFieldName string, c *defaultCandidate) (judge, value string, ok bool) {
	if !c.undecided {
		return "", "", true
	}
	node := g.defaultJudgeNode(typeName, goFieldName, c.schemas)
	if node == nil {
		g.noteSkippedDefault(typeName, c, "whether "+describeJSONValue(c.value)+" is valid where it is written could be decided neither at generation time nor by the runtime evaluator")
		return "", "", false
	}
	return node.Var, jsonTreeLiteral(c.value), true
}

// defaultJudgeNode compiles schemas, as one conjunction, into a package variable
// the runtime evaluator reads, declared beside the element nodes; nil where the
// evaluator declines them.
func (g *Generator) defaultJudgeNode(typeName, goFieldName string, schemas []*schema.Schema) *ElementNode {
	if !g.validationKeywordsEnabled() {
		return nil
	}
	whole := &schema.Schema{AllOf: schemas}
	base := typeName + goFieldName
	b := &nodeBuilder{
		g:           g,
		allowed:     validatorKeywords,
		inlineRefs:  true,
		stack:       map[*schema.Schema]int{},
		hoistPrefix: "_dj" + base + "Node",
	}
	lit, nodes, ok := b.build(whole)
	if !ok {
		return nil
	}
	owner := typeName + "." + goFieldName
	n := &ElementNode{
		TypeName: owner,
		Purpose:  "the schema SetDefaults judges the default of " + owner + " against",
		Var: g.names.claim("_dj"+base, memberHolder(typeName, "default-judge/"+goFieldName,
			"the schema the default of "+owner+" is judged against")),
		Literal: lit,
		Nodes:   nodes,
	}
	for i, node := range nodes {
		if !g.names.claimExactly(node.Name, memberHolder(typeName, "default-judge/"+goFieldName+"/"+strconv.Itoa(i), "a compiled node of the default judge of "+owner)) {
			held, _ := g.names.holderOf(node.Name)
			g.noteNamingDefect(&NamingDefectError{Name: node.Name, Holder: held.what, Detail: "a compiled default-judge node of " + owner + " was named"})
		}
	}
	g.output.ElementNodes = append(g.output.ElementNodes, n)
	return n
}

// jsonTreeLiteral writes a JSON value as the Go tree the runtime evaluator
// reads: map[string]any, []any, string, bool, nil and json.Number, the last
// holding the literal the schema wrote so that no digit is lost to a float64.
func jsonTreeLiteral(v any) string {
	switch x := v.(type) {
	case nil:
		return "nil"
	case bool:
		return strconv.FormatBool(x)
	case string:
		return strconv.Quote(x)
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = jsonTreeLiteral(item)
		}
		return "[]any{" + strings.Join(parts, ", ") + "}"
	case map[string]any:
		keys := sortedKeys(x)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = strconv.Quote(k) + ": " + jsonTreeLiteral(x[k])
		}
		return "map[string]any{" + strings.Join(parts, ", ") + "}"
	}
	if n, ok := schemaNumber(v); ok {
		return "json.Number(" + strconv.Quote(string(n)) + ")"
	}
	return "nil"
}

// jsonText is v written as JSON, numbers as the literal the schema wrote.
func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// describeJSONValue is v for a diagnostic: its JSON, cut short when long.
func describeJSONValue(v any) string {
	const limit = 60
	text := jsonText(v)
	if len(text) > limit {
		text = text[:limit] + "..."
	}
	return "the default " + text
}

// cannotHold is the reason a Go type declines a value.
func cannotHold(goTypeName string, v any) string {
	return fmt.Sprintf("%s cannot hold %s", strings.TrimPrefix(goTypeName, "*"), strings.TrimPrefix(describeJSONValue(v), "the default "))
}

// jsonNumberOf is schemaNumber for a value decoded from JSON: a Go string is a
// JSON string there, never a number's literal.
func jsonNumberOf(v any) (schema.Number, bool) {
	if isJSONNonNumber(v) {
		return "", false
	}
	return schemaNumber(v)
}

// defaultToGoLiteral writes a default as a Go literal of one of the built-in
// scalars, or a pointer to one, or `any`.
//
// handled says the type is one of those, and then either lit is the literal --
// empty where the default is the type's zero and a bare field could not tell it
// from absence, so planting it is a no-op -- or why says the type cannot hold
// the value: 4.5 or 1e30 in an int64, 1e400 in a float64, a string in a bool.
// Nothing is ever rounded, truncated or converted into a value the default did
// not state. Any other type is not handled here: it is settled by
// resolveDefaults, which can see the declaration it names.
func defaultToGoLiteral(defaultVal any, goType GoType) (lit string, handled bool, why string) {
	if goType == nil {
		return "", false, ""
	}
	typeName := goType.GoTypeName()
	switch typeName {
	case "string", "*string", "int64", "*int64", "float64", "*float64",
		GoNumberTypeName, "*" + GoNumberTypeName, "bool", "*bool", "any":
	default:
		return "", false, ""
	}
	// A null default leaves the field as a null in the document leaves it --
	// untouched -- so there is nothing to plant, and nothing to report.
	if defaultVal == nil {
		return "", true, ""
	}
	switch typeName {
	case "string", "*string":
		s, ok := defaultVal.(string)
		if !ok {
			return "", true, cannotHold(typeName, defaultVal)
		}
		if typeName == "string" && s == "" {
			return "", true, "" // zero value, no-op (for non-pointer)
		}
		return strconv.Quote(s), true, ""
	case "int64", "*int64":
		n, isNum := jsonNumberOf(defaultVal)
		if !isNum {
			return "", true, cannotHold(typeName, defaultVal)
		}
		// Read as an exact integer rather than through float64. A default of
		// 9223372036854775807 is an int64 and is not a float64, and asking
		// float64 first both loses it and accepts 9223372036854775808, which no
		// int64 holds.
		intVal, ok := n.Int64()
		if !ok {
			return "", true, cannotHold(typeName, defaultVal)
		}
		if typeName == "int64" && intVal == 0 {
			return "", true, "" // zero value, no-op (for non-pointer)
		}
		return strconv.FormatInt(intVal, 10), true, ""
	case "float64", "*float64":
		n, isNum := jsonNumberOf(defaultVal)
		if !isNum {
			return "", true, cannotHold(typeName, defaultVal)
		}
		// Only a number the float64 writes back out as itself: see
		// float64Holds.
		f, ok := float64Holds(n)
		if !ok {
			return "", true, cannotHold(typeName, defaultVal)
		}
		if typeName == "float64" && f == 0 {
			return "", true, "" // zero value, no-op (for non-pointer)
		}
		return strconv.FormatFloat(f, 'f', -1, 64), true, ""
	case GoNumberTypeName, "*" + GoNumberTypeName:
		// A number held exactly, so the default is written as the literal the
		// schema wrote and not as the shortest decimal that reads back to the
		// same float64. Those are different numbers to a type that keeps every
		// digit: a default of 1.2345678901234567890 would otherwise be planted
		// as 1.2345678901234567, and the value the caller never set would be
		// the one this flag exists to stop them seeing. It is also the one
		// numeric type that holds 1e400.
		n, isNum := jsonNumberOf(defaultVal)
		if !isNum {
			return "", true, cannotHold(typeName, defaultVal)
		}
		return GoNumberTypeName + "(" + strconv.Quote(string(n)) + ")", true, ""
	case "bool", "*bool":
		b, ok := defaultVal.(bool)
		if !ok {
			return "", true, cannotHold(typeName, defaultVal)
		}
		if typeName == "bool" && !b {
			return "", true, "" // zero value (false), no-op (for non-pointer)
		}
		return strconv.FormatBool(b), true, ""
	}
	// `any`: what encoding/json decodes the same JSON into, which is what the
	// field holds for a document that carried it.
	lit, ok := anyValueLiteral(defaultVal)
	if !ok {
		return "", true, cannotHold(typeName, defaultVal)
	}
	return lit, true, ""
}

// resolveDefaults writes the defaults defaultToGoLiteral could not, once every
// declaration exists to be looked up.
//
// A "default" reached through a $ref almost always lands on such a field: the
// reference survives into the generated source as the field's type, so
// {"f":{"$ref":"#/$defs/D"}} over a string D is `F *D`, not `F *string`, and
// defaultToGoLiteral -- which answers from the Go type name alone -- had nothing
// to say about it. The same is true of an allOf on the property, which is
// synthesized into a type of its own. That is why issue #186 reports the two
// together: following the reference was necessary to find the keyword, and not
// sufficient to emit it.
//
// The other two kinds of target that reach here are the ones issue #251 reports,
// and they are the same gap in a different place: defaultToGoLiteral answers for
// the four built-in scalars and nothing else, so an integer under --big-int --
// whose Go type is a wrapper struct, not an int64 -- and a slice or a map in any
// configuration both left with no literal and no diagnostic. Each is spelled
// here, where the declarations they name can be read.
//
// The kinds of target reached here beyond those: a json.RawMessage (the untyped
// position under --raw-untyped) and a raw-value wrapper, which hold the
// default's JSON as it is; and a big integer past int64 under --big-int, which
// the wrapper holds and whose digits the schema package kept (a "default" is
// decoded with UseNumber, so 1e30 arrives as the literal and not as a float64).
//
// Held to a type this package declared: a type in another generated package has
// no declaration here to read, and its big-int wrapper's fields are unexported
// there in any case. What no literal here spells -- a struct, a time.Time or a
// netip.Addr, a collection of structs or pointers -- is planted by decoding
// (defaultShapeDecoded) where decodeHolds shows the decode holds it, and
// otherwise reported; oneOf groups are always planted that way. A type of
// another package is neither spelled nor decoded here: its declaration is not
// this run's to read.
//
// Runs after the type definitions are complete, in the manner of
// resolveLeafDecodes and for the same reason: the answer is a property of a
// whole declaration, not of the one property that happens to reference it, and
// asking during field construction would make it depend on generation order.
func (g *Generator) resolveDefaults() {
	for _, td := range g.output.TypeDefs {
		structDef, ok := td.(*StructDef)
		if !ok {
			continue
		}
		for i := range structDef.Fields {
			f := &structDef.Fields[i]
			// pendingDefault is set only where defaultToGoLiteral did not
			// handle the type, so there is nothing here to overwrite and no
			// second test for that.
			c := f.pendingDefault
			if c == nil {
				continue
			}
			f.pendingDefault = nil
			lit, shape, why := g.lateDefaultLiteral(c.value, f.Type)
			if why != "" {
				// No literal spells it: plant it by decoding, where the
				// decode holds it exactly.
				if held := g.decodeHolds(c.value, f.Type); held != "" {
					g.noteSkippedDefault(structDef.Name, c, held)
					continue
				}
				lit, shape = decodedDefaultLiteral(f.JSONName, c.value), defaultShapeDecoded
			}
			if lit == "" {
				// A default equal to the zero value of a field that has no way
				// to tell it from absence: planting it changes nothing.
				continue
			}
			judge, judgeValue, ok := g.defaultJudgeFor(structDef.Name, f.Name, c)
			if !ok {
				continue
			}
			f.DefaultLiteral = lit
			f.DefaultShape = shape
			f.DefaultJudge = judge
			f.DefaultJudgeValue = judgeValue
			// A default written into a field with no nil state is settled
			// against the document's key set, which only UnmarshalJSON records.
			// Asked here as well as at field construction, because this is
			// where the literal that makes the field need it is decided; and
			// unreachable here for the same reason it is unreachable there, so
			// see that comment for why it is kept. Issue #248.
			if f.DefaultAsksJSONKeys() {
				structDef.NeedsUnmarshal = true
			}
		}
		for i := range structDef.OneOfs {
			o := &structDef.OneOfs[i]
			c := o.pendingDefault
			if c == nil {
				continue
			}
			o.pendingDefault = nil
			if held := g.oneOfDecodeHolds(c.value, o); held != "" {
				g.noteSkippedDefault(structDef.Name, c, held)
				continue
			}
			judge, judgeValue, ok := g.defaultJudgeFor(structDef.Name, o.FieldName, c)
			if !ok {
				continue
			}
			o.DefaultLiteral = decodedDefaultLiteral(o.JSONName, c.value)
			o.DefaultJudge = judge
			o.DefaultJudgeValue = judgeValue
		}
	}
}

// decodedDefaultLiteral is the Go string literal of the one-member document
// that carries value at the property name: what defaultShapeDecoded decodes.
func decodedDefaultLiteral(name string, value any) string {
	return strconv.Quote("{" + strconv.Quote(name) + ":" + jsonText(value) + "}")
}

// decodeHolds reports why decoding value into a field of type t would not hold
// it exactly, or "" where it would: where what the decode leaves writes back
// out as the same JSON. It walks the value down the declarations the decode
// fills, and asks each leaf the question defaultToGoLiteral asks of a whole
// field -- an int64 holds no fraction, a float64 only a number it writes back
// as itself -- and one more: a time.Time and a netip.Addr hold a string only if
// they write the same bytes back, since each respells what it is given
// ("2020-01-01T00:00:00.000Z" comes back without its fraction).
//
// A declaration it cannot read -- another package's, or a kind it does not
// know -- is a reason, not a pass: a default whose round trip cannot be shown
// is not planted.
func (g *Generator) decodeHolds(value any, t GoType) string {
	return g.decodeHoldsAt(value, t, 0)
}

// maxDecodeHoldsDepth bounds decodeHolds on a recursive declaration; the value
// is finite, so this is for a pathological schema rather than for termination.
const maxDecodeHoldsDepth = 256

func (g *Generator) decodeHoldsAt(v any, t GoType, depth int) string {
	if depth > maxDecodeHoldsDepth {
		return "the default is too deep to follow"
	}
	if v == nil {
		// A null leaves the field as the absent value, and writes back as
		// such; the judge has already decided the location admits it.
		return ""
	}
	switch x := t.(type) {
	case *PointerType:
		return g.decodeHoldsAt(v, x.Inner, depth+1)
	case *ArrayType:
		items, ok := v.([]any)
		if !ok {
			return cannotHold(t.GoTypeName(), v)
		}
		for _, item := range items {
			if why := g.decodeHoldsAt(item, x.ItemType, depth+1); why != "" {
				return why
			}
		}
		return ""
	case *MapType:
		obj, ok := v.(map[string]any)
		if !ok {
			return cannotHold(t.GoTypeName(), v)
		}
		if kt, ok := x.KeyType.(*PrimitiveType); !ok || kt.Name != "string" {
			return noLiteralFor(t)
		}
		for _, k := range sortedKeys(obj) {
			if why := g.decodeHoldsAt(obj[k], x.ValueType, depth+1); why != "" {
				return why
			}
		}
		return ""
	case *PrimitiveType:
		switch x.Name {
		case dateTimeGoTypeName:
			return timeHolds(v)
		case ipAddrGoTypeName:
			return addrHolds(v)
		case rawMessageTypeName:
			return ""
		case "any":
			if _, ok := anyValueLiteral(v); !ok {
				return cannotHold("any", v)
			}
			return ""
		}
		_, handled, why := defaultToGoLiteral(v, t)
		if !handled {
			return noLiteralFor(t)
		}
		return why
	case *NamedType:
		if x.PkgAlias != "" {
			return "the field's type " + t.GoTypeName() + " belongs to another package, whose decode this generator cannot follow"
		}
		return g.namedDecodeHolds(v, x.Name, depth)
	}
	return noLiteralFor(t)
}

// namedDecodeHolds is decodeHolds for a type this run declared.
func (g *Generator) namedDecodeHolds(v any, name string, depth int) string {
	var def TypeDef
	for _, td := range g.output.TypeDefs {
		if td.TypeName() == name {
			def = td
			break
		}
	}
	switch d := def.(type) {
	case *AliasDef:
		return g.decodeHoldsAt(v, d.Underlying, depth+1)
	case *EnumDef:
		return g.decodeHoldsAt(v, d.BaseType, depth+1)
	case *BigIntAliasDef:
		_, why := bigIntDefaultLiteral(name, v)
		return why
	case *TypeOnlySchemaDef, *NotSchemaDef, *DynamicSchemaDef, *AnnotationSchemaDef:
		return "" // holds the value's JSON as it is
	case *StructDef:
		for i := range d.OneOfs {
			if !d.OneOfs[i].IsProperty() {
				return g.oneOfDecodeHoldsAt(v, &d.OneOfs[i], depth+1)
			}
		}
		obj, ok := v.(map[string]any)
		if !ok {
			return cannotHold(name, v)
		}
		for _, key := range sortedKeys(obj) {
			member := obj[key]
			if why := g.structMemberDecodeHolds(d, key, member, depth+1); why != "" {
				return why
			}
		}
		return ""
	}
	return "this generator cannot follow the decode of the field's type " + name
}

// structMemberDecodeHolds is decodeHolds for one member of an object decoded
// into d: into the field that names it, the group that does, or the overflow.
func (g *Generator) structMemberDecodeHolds(d *StructDef, key string, member any, depth int) string {
	for i := range d.Fields {
		if d.Fields[i].JSONName == key {
			return g.decodeHoldsAt(member, d.Fields[i].Type, depth)
		}
	}
	for i := range d.OneOfs {
		if d.OneOfs[i].IsProperty() && d.OneOfs[i].JSONName == key {
			return g.oneOfDecodeHoldsAt(member, &d.OneOfs[i], depth)
		}
	}
	// A member no field names lands in an overflow map: a pattern's, which
	// holds it as raw JSON, or the additional one, which holds it raw or as
	// its value type does. A pattern with no answer is not a pass.
	for _, p := range d.PatternProperties {
		matched, err := PatternMatches(p.Pattern, key)
		if err != nil {
			return fmt.Sprintf("whether the pattern %q applies to %q could not be decided", p.Pattern, key)
		}
		if matched {
			return ""
		}
	}
	if ap := d.AdditionalProperties; ap != nil && ap.ValueType != nil {
		return g.decodeHoldsAt(member, ap.ValueType, depth)
	}
	return ""
}

// oneOfDecodeHolds is decodeHolds for a oneOf group: the decode selects one
// variant, and which one is the document's business, so every variant the
// value could be decoded into has to hold it. A variant of a different JSON
// kind is not one it could be decoded into.
func (g *Generator) oneOfDecodeHolds(v any, o *OneOfDef) string {
	return g.oneOfDecodeHoldsAt(v, o, 0)
}

func (g *Generator) oneOfDecodeHoldsAt(v any, o *OneOfDef, depth int) string {
	for _, variant := range o.Variants {
		if !g.variantTakesKind(variant.Type, v) {
			continue
		}
		if why := g.decodeHoldsAt(v, variant.Type, depth+1); why != "" {
			return why
		}
	}
	return ""
}

// variantTakesKind reports whether a value of v's JSON kind could be decoded
// into t at all, for choosing the variants of a group a value may select.
// Undecided kinds answer yes, which is the direction that checks more.
func (g *Generator) variantTakesKind(t GoType, v any) bool {
	return g.takesKind(t, v, map[string]bool{})
}

func (g *Generator) takesKind(t GoType, v any, seen map[string]bool) bool {
	switch x := t.(type) {
	case *PointerType:
		return g.takesKind(x.Inner, v, seen)
	case *ArrayType:
		_, ok := v.([]any)
		return ok
	case *MapType:
		_, ok := v.(map[string]any)
		return ok
	case *PrimitiveType:
		switch x.Name {
		case "string", dateTimeGoTypeName, ipAddrGoTypeName:
			_, ok := v.(string)
			return ok
		case "bool":
			_, ok := v.(bool)
			return ok
		case "int64", "float64", GoNumberTypeName:
			_, ok := jsonNumberOf(v)
			return ok
		}
		return true
	case *NamedType:
		if x.PkgAlias != "" || seen[x.Name] {
			return true
		}
		seen[x.Name] = true
		for _, td := range g.output.TypeDefs {
			if td.TypeName() != x.Name {
				continue
			}
			switch d := td.(type) {
			case *StructDef:
				if _, isObject := v.(map[string]any); isObject {
					return true
				}
				for i := range d.OneOfs {
					if !d.OneOfs[i].IsProperty() {
						return true
					}
				}
				return false
			case *AliasDef:
				return g.takesKind(d.Underlying, v, seen)
			case *EnumDef:
				return g.takesKind(d.BaseType, v, seen)
			case *BigIntAliasDef:
				_, ok := jsonNumberOf(v)
				return ok
			}
			return true
		}
	}
	return true
}

// timeHolds reports why a time.Time would not hold v exactly: it parses an RFC
// 3339 string and writes it back as RFC 3339 with the nanoseconds it needs,
// which respells "2020-01-01T00:00:00.000Z" and "2020-01-01T00:00:00+00:00".
func timeHolds(v any) string {
	s, ok := v.(string)
	if !ok {
		return cannotHold(dateTimeGoTypeName, v)
	}
	var t time.Time
	if err := t.UnmarshalJSON([]byte(strconv.Quote(s))); err != nil {
		return cannotHold(dateTimeGoTypeName, v)
	}
	back, err := t.MarshalJSON()
	if err != nil || string(back) != strconv.Quote(s) {
		return fmt.Sprintf("%s would write %q back as %s", dateTimeGoTypeName, s, back)
	}
	return ""
}

// addrHolds is timeHolds for netip.Addr, which writes an address back in its
// canonical form: "::0001" comes back as "::1".
func addrHolds(v any) string {
	s, ok := v.(string)
	if !ok {
		return cannotHold(ipAddrGoTypeName, v)
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return cannotHold(ipAddrGoTypeName, v)
	}
	if back := a.String(); back != s {
		return fmt.Sprintf("%s would write %q back as %q", ipAddrGoTypeName, s, back)
	}
	return ""
}

// lateDefaultLiteral is the body of resolveDefaults for one field: the Go
// literal for v at the field's declared type, and the DefaultShape that says
// which arm of the SetDefaults template writes it.
//
// Returns "" and no reason where the default is the field's zero and the field
// cannot tell it from absence, and a reason where nothing can be spelled, which
// is what leaves a property with no default rather than an unsound one.
func (g *Generator) lateDefaultLiteral(v any, t GoType) (lit, shape, why string) {
	if named, pointer := localNamedType(t); named != nil {
		shape = defaultShapeNamed
		if pointer {
			shape = defaultShapePointerNamed
		}
		if prim := g.primitiveUnderlyingOf(named.Name); prim != nil {
			if prim.Name == rawMessageTypeName {
				// A heterogeneous enum is held as the raw JSON of its member,
				// and a slice is not comparable: tested against nil, as a
				// collection is.
				if !pointer {
					shape = defaultShapeCollection
				}
				return named.Name + "(" + rawMessageLiteral(v) + ")", shape, ""
			}
			// The pointer shape is what decides whether a default equal to the
			// zero value is worth emitting: a nil pointer is distinguishable
			// from a present zero, a bare scalar is not. Asking
			// defaultToGoLiteral in the field's own shape keeps that rule in
			// one place.
			inner := GoType(prim)
			if pointer {
				inner = &PointerType{Inner: prim}
			}
			scalar, handled, why := defaultToGoLiteral(v, inner)
			switch {
			case !handled:
				return "", "", noLiteralFor(t)
			case why != "" || scalar == "":
				return "", "", why
			}
			return named.Name + "(" + scalar + ")", shape, ""
		}
		if g.isLocalBigIntAlias(named.Name) {
			lit, why := bigIntDefaultLiteral(named.Name, v)
			if why != "" {
				return "", "", why
			}
			return lit, shape, ""
		}
		// A named slice or map: the composite literal is the same one an
		// unnamed collection gets, written under the declared name.
		if under := g.collectionUnderlyingOf(named.Name); under != nil {
			body, ok := g.compositeBody(v, under)
			if !ok {
				return "", "", cannotHold(t.GoTypeName(), v)
			}
			if !pointer {
				// A bare named collection is compared against nil, exactly as
				// an unnamed one is; behind a pointer it is the pointer that is
				// tested, and the "*named" arm already does that.
				shape = defaultShapeCollection
			}
			return named.Name + body, shape, ""
		}
		// A raw-value wrapper holds the value's JSON and nothing else.
		if g.isLocalRawWrapper(named.Name) {
			if !pointer {
				shape = defaultShapeIsZero
			}
			return named.Name + "{_raw: " + rawMessageLiteral(v) + "}", shape, ""
		}
		return "", "", noLiteralFor(t)
	}
	switch t.(type) {
	case *ArrayType, *MapType:
		// An unnamed slice or map. Neither is ever pointer-wrapped -- a nil
		// slice and a nil map are already the absent value, which is why the
		// optional shape leaves them alone -- so there is no pointer arm to
		// reach here.
		body, ok := g.compositeBody(v, t)
		if !ok {
			return "", "", cannotHold(t.GoTypeName(), v)
		}
		return t.GoTypeName() + body, defaultShapeCollection, ""
	case *PrimitiveType:
		if t.GoTypeName() == rawMessageTypeName {
			return rawMessageLiteral(v), defaultShapeCollection, ""
		}
	}
	return "", "", noLiteralFor(t)
}

// rawMessageTypeName is how the untyped position under --raw-untyped, and the
// base of a heterogeneous enum, spell their type.
const rawMessageTypeName = "json.RawMessage"

// rawMessageLiteral is v as the bytes a json.RawMessage holds: its JSON, with
// every number the literal the schema wrote.
func rawMessageLiteral(v any) string {
	return rawMessageTypeName + "(" + strconv.Quote(jsonText(v)) + ")"
}

// noLiteralFor is the reason a type gets no default: nothing here spells one.
func noLiteralFor(t GoType) string {
	return "this generator writes no literal for the field's type " + t.GoTypeName()
}

// isLocalBigIntAlias reports whether a name belongs to a big-int wrapper this
// run declared. Asked over g.output.TypeDefs rather than over every definition
// in scope, because the literal it authorises names the wrapper's unexported
// fields, and those are reachable only from the package that declares them.
func (g *Generator) isLocalBigIntAlias(name string) bool {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() == name {
			_, ok := td.(*BigIntAliasDef)
			return ok
		}
	}
	return false
}

// isLocalRawWrapper reports whether a name belongs to a raw-value wrapper this
// run declared: a type holding a value's JSON in its unexported _raw field,
// which is what the literal it authorises names -- so, like
// isLocalBigIntAlias, it is asked of this run's declarations only.
func (g *Generator) isLocalRawWrapper(name string) bool {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() == name {
			switch td.(type) {
			case *TypeOnlySchemaDef, *NotSchemaDef, *DynamicSchemaDef, *AnnotationSchemaDef:
				return true
			}
			return false
		}
	}
	return false
}

// collectionUnderlyingOf returns the slice or map a generated named type is an
// alias for, and nil when it is not one. Only the declaration's own underlying
// is read: a chain of aliases ending at a slice would spell the same literal,
// but no route builds one, so a walk here could not be falsified.
func (g *Generator) collectionUnderlyingOf(name string) GoType {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() != name {
			continue
		}
		d, ok := td.(*AliasDef)
		if !ok {
			return nil
		}
		switch d.Underlying.(type) {
		case *ArrayType, *MapType:
			return d.Underlying
		}
		return nil
	}
	return nil
}

// bigIntDefaultLiteral writes a "default" for a field whose integer type
// --big-int has materialized into a wrapper struct.
//
// The literal names the wrapper's unexported fields, which is why the caller
// holds it to a type this run declared. That is a coupling to the shape
// bigint_alias.go.tmpl emits, in the manner of LeafDecodeDef.Convert's
// coupling to the jsonInteger helpers, and it is the only construction the
// wrapper has: it carries no exported field and no constructor, so a value of it
// can otherwise only be reached by decoding JSON.
//
// _isBigInt stays false and _bigInt nil, which is the wrapper's own reading of
// an int64-sized value -- the same state its UnmarshalJSON leaves. So does
// _isNull where the schema admits one: a stated default is a value, not a null.
//
// An integer past int64 is the value the wrapper exists for, and its digits are
// the schema's own: "default" is decoded with UseNumber, so 1e30 arrives as that
// literal. It is written as the wrapper's own decode of the integer's plain
// digits, the one spelling every draft's integer grammar accepts, which leaves
// the wrapper in exactly the state a document carrying it would.
func bigIntDefaultLiteral(typeName string, v any) (lit, why string) {
	n, isNum := jsonNumberOf(v)
	if !isNum {
		return "", cannotHold(typeName, v)
	}
	if i, ok := n.Int64(); ok {
		return typeName + "{_int64: " + strconv.FormatInt(i, 10) + "}", ""
	}
	r, ok := n.Rat()
	if !ok || !r.IsInt() {
		return "", cannotHold(typeName, v)
	}
	digits := strconv.Quote(r.Num().String())
	return "func() " + typeName + " { var _v " + typeName + "; _ = _v.UnmarshalJSON([]byte(" + digits + ")); return _v }()", ""
}

// compositeBody writes the "{...}" of a slice or map literal for the JSON value
// v, or reports that it cannot be written.
//
// The braces are separated from the type name so that a named alias over the
// same slice can be spelled under its own name -- Tags{"z"} rather than
// []string{"z"}, which is not assignable to a Tags field.
//
// Declining is silent and total: one element with no literal takes the whole
// default with it, leaving the property exactly as it was before collections
// were read at all. A partial literal would be a default the schema never
// stated, which is worse than none.
func (g *Generator) compositeBody(v any, t GoType) (string, bool) {
	var b strings.Builder
	b.WriteString("{")
	switch dst := t.(type) {
	case *ArrayType:
		items, ok := v.([]any)
		if !ok {
			return "", false
		}
		for i, item := range items {
			lit, ok := g.jsonValueLiteral(item, dst.ItemType)
			if !ok {
				return "", false
			}
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(elideElementType(lit, dst.ItemType))
		}
	case *MapType:
		obj, ok := v.(map[string]any)
		if !ok {
			return "", false
		}
		if kt, ok := dst.KeyType.(*PrimitiveType); !ok || kt.Name != "string" {
			return "", false
		}
		// Sorted, because a Go map has no order and generated source has to be
		// the same on every run.
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			lit, ok := g.jsonValueLiteral(obj[k], dst.ValueType)
			if !ok {
				return "", false
			}
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(strconv.Quote(k) + ": " + elideElementType(lit, dst.ValueType))
		}
	default:
		return "", false
	}
	b.WriteString("}")
	return b.String(), true
}

// elideElementType drops the type name from a composite literal standing where
// the enclosing slice or map already states it, which is the form gofmt -s
// leaves and the one a person writes: [][]float64{{1.5}}, not
// [][]float64{[]float64{1.5}}.
//
// Held to a literal that opens its own braces. A conversion -- Name("q") --
// looks the same at the front and means something else, and dropping the name
// from an element whose declared type is `any` would elide a type the enclosing
// literal never stated.
func elideElementType(lit string, elem GoType) string {
	name := elem.GoTypeName()
	rest, cut := strings.CutPrefix(lit, name)
	if !cut || !strings.HasPrefix(rest, "{") {
		return lit
	}
	return rest
}

// jsonValueLiteral writes one JSON value as a Go literal of the declared type t,
// for use inside a composite literal.
//
// This is not defaultToGoLiteral's question and does not share its answers. That
// one is asked of a whole field and suppresses a literal equal to the field's
// zero, because a bare scalar cannot tell that value from an absent property; an
// element of a slice has no such ambiguity, and dropping "" from ["a","","b"]
// would write an array of two.
func (g *Generator) jsonValueLiteral(v any, t GoType) (string, bool) {
	switch dst := t.(type) {
	case *PrimitiveType:
		switch dst.Name {
		case "any":
			// The element type imposes nothing, so the literal is whatever
			// encoding/json would have decoded the same JSON into -- which is
			// what makes a defaulted value indistinguishable from a decoded one.
			return anyValueLiteral(v)
		case rawMessageTypeName:
			return rawMessageLiteral(v), true
		}
		return scalarValueLiteral(v, dst.Name)
	case *ArrayType, *MapType:
		body, ok := g.compositeBody(v, dst)
		if !ok {
			return "", false
		}
		return dst.GoTypeName() + body, true
	case *NamedType:
		if dst.PkgAlias != "" || dst.Pointer {
			// Another package's declaration cannot be read from here, and a
			// pointer element has no literal at all: Go has no address-of for
			// a composite literal's scalar members.
			return "", false
		}
		if prim := g.primitiveUnderlyingOf(dst.Name); prim != nil {
			if prim.Name == rawMessageTypeName {
				return dst.Name + "(" + rawMessageLiteral(v) + ")", true
			}
			lit, ok := scalarValueLiteral(v, prim.Name)
			if !ok {
				return "", false
			}
			return dst.Name + "(" + lit + ")", true
		}
		if g.isLocalBigIntAlias(dst.Name) {
			lit, why := bigIntDefaultLiteral(dst.Name, v)
			return lit, why == ""
		}
		if under := g.collectionUnderlyingOf(dst.Name); under != nil {
			body, ok := g.compositeBody(v, under)
			if !ok {
				return "", false
			}
			return dst.Name + body, true
		}
		if g.isLocalRawWrapper(dst.Name) {
			return dst.Name + "{_raw: " + rawMessageLiteral(v) + "}", true
		}
	}
	return "", false
}

// scalarValueLiteral writes a JSON value as a literal of one of the four
// built-in scalars, and reports whether the value is of that kind at all.
func scalarValueLiteral(v any, goTypeName string) (string, bool) {
	switch goTypeName {
	case "string":
		s, ok := v.(string)
		if !ok {
			return "", false
		}
		return strconv.Quote(s), true
	case "int64":
		n, isNum := schemaNumber(v)
		if !isNum {
			return "", false
		}
		i, ok := n.Int64()
		if !ok {
			return "", false
		}
		return strconv.FormatInt(i, 10), true
	case "float64":
		n, isNum := schemaNumber(v)
		if !isNum {
			return "", false
		}
		f, ok := float64Holds(n)
		if !ok {
			return "", false
		}
		return strconv.FormatFloat(f, 'f', -1, 64), true
	case GoNumberTypeName:
		// The literal, for the reason defaultToGoLiteral gives: this type keeps
		// every digit it is given, so re-rendering the number would plant a
		// different one.
		n, isNum := schemaNumber(v)
		if !isNum {
			return "", false
		}
		return GoNumberTypeName + "(" + strconv.Quote(string(n)) + ")", true
	case "bool":
		b, ok := v.(bool)
		if !ok {
			return "", false
		}
		return strconv.FormatBool(b), true
	}
	return "", false
}

// anyValueLiteral writes a JSON value as a Go literal of type any.
//
// Every arm names the type encoding/json decodes that JSON kind into for an
// `any` destination, and a number is written as float64 for the same reason: an
// untyped 1 in a []any literal is an int, and a caller comparing the defaulted
// element against a decoded one would find two values that are not equal and
// not even the same type.
func anyValueLiteral(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "nil", true
	case bool:
		return strconv.FormatBool(x), true
	case string:
		return strconv.Quote(x), true
	case []any:
		lits := make([]string, 0, len(x))
		for _, item := range x {
			lit, ok := anyValueLiteral(item)
			if !ok {
				return "", false
			}
			lits = append(lits, lit)
		}
		return "[]any{" + strings.Join(lits, ", ") + "}", true
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		lits := make([]string, 0, len(keys))
		for _, k := range keys {
			lit, ok := anyValueLiteral(x[k])
			if !ok {
				return "", false
			}
			lits = append(lits, strconv.Quote(k)+": "+lit)
		}
		return "map[string]any{" + strings.Join(lits, ", ") + "}", true
	}
	n, isNum := schemaNumber(v)
	if !isNum {
		return "", false
	}
	f, ok := float64Holds(n)
	if !ok {
		return "", false
	}
	return "float64(" + strconv.FormatFloat(f, 'f', -1, 64) + ")", true
}

// float64Holds reads n as a float64, and reports whether the float64 holds it:
// whether the number it writes back out is the number the schema wrote. 0.1
// does -- the float64 nearest it writes as 0.1 -- and 1.2345678901234567890,
// 12345678901234567890 and 1e400 do not, since the first two come back out as
// other numbers and the last does not fit at all. A default is planted only
// where the field holds it, so a float64 default is one of the first kind; a
// number held exactly (--exact-numbers) holds the others.
func float64Holds(n schema.Number) (float64, bool) {
	f, ok := n.Float64()
	if !ok {
		return 0, false
	}
	back, okBack := schema.Number(strconv.FormatFloat(f, 'g', -1, 64)).CanonicalText()
	want, okWant := n.CanonicalText()
	return f, okBack && okWant && back == want
}

// localNamedType reports the named type a field holds, and whether it holds it
// through a pointer. Both spellings of the pointer are accepted because both are
// built: resolveType marks NamedType.Pointer, and the optional-field wrapping
// puts a PointerType around whatever it is given.
//
// A type qualified with another package's alias is declined. Its declaration
// belongs to a different generation run, and the name it carries is looked up
// here against this run's type definitions -- where a same-named local type
// would answer for it and hand back the underlying of something else entirely.
func localNamedType(t GoType) (named *NamedType, pointer bool) {
	switch v := t.(type) {
	case *NamedType:
		if v.PkgAlias != "" {
			return nil, false
		}
		return v, v.Pointer
	case *PointerType:
		inner, _ := localNamedType(v.Inner)
		if inner == nil {
			return nil, false
		}
		return inner, true
	}
	return nil, false
}

// primitiveUnderlyingOf returns the built-in type at the end of a generated
// named type's chain, and nil when the chain does not end at one.
//
// Which built-ins can actually hold a default is not decided here. That is
// defaultToGoLiteral's question and it is asked next, with the same primitive
// this hands back: "any" and json.RawMessage get no literal from it, so a switch
// here naming the four scalars would only be a second copy of an answer that
// already exists -- one that no planted fault could distinguish, since the
// conversion is never written without a literal to put in it.
//
// A named underlying is followed, because a $ref chain generates one: `{"$ref":
// "#/$defs/A"}` over an A that is itself `{"$ref":"#/$defs/B"}` declares `type A
// B`, and the conversion into A is as sound as the one into B. The chain is
// walked with a visited set for the same reason zeroLiteralForType recurses at
// all -- and because a self-referential $defs entry can declare a name in terms
// of itself, which has no end to reach.
func (g *Generator) primitiveUnderlyingOf(name string) *PrimitiveType {
	seen := map[string]bool{}
	for !seen[name] {
		seen[name] = true
		var underlying GoType
		for _, td := range g.output.TypeDefs {
			if td.TypeName() != name {
				continue
			}
			switch d := td.(type) {
			case *EnumDef:
				underlying = d.BaseType
			case *AliasDef:
				underlying = d.Underlying
			}
			break
		}
		// A named type this run did not declare leaves `underlying` nil and
		// falls through to the default arm, which is also where a struct, a
		// slice, a map and a pointer land. There is no separate test for
		// AliasDef.NoMethods: an alias that cannot carry methods has a pointer
		// or an interface at the end of its chain, and neither can hold a
		// default -- a pointer is a *PointerType and lands in that arm, an
		// interface is the "any" primitive and gets no literal.
		switch u := underlying.(type) {
		case *PrimitiveType:
			return u
		case *NamedType:
			if u.PkgAlias != "" {
				return nil
			}
			name = u.Name
		default:
			return nil
		}
	}
	return nil
}
