package generator

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// The three DefaultShape values a field can carry, and the arm of the
// SetDefaults template each selects. The empty shape is the fourth: one of the
// four built-in scalars or a pointer to one, which the template picks out by the
// field's own Go type name.
//
// What they choose between is how the *field* is tested for being untouched: a
// slice and a map are compared against nil, everything else against its own
// zero, and the two are not interchangeable because a slice is not comparable at
// all. Whether the document's key set is consulted beside that test is a
// different question, and FieldDef.DefaultAsksJSONKeys answers it.
const (
	defaultShapeNamed        = "named"      // a conversion or composite literal written into a bare named type
	defaultShapePointerNamed = "*named"     // the same, written through a pointer
	defaultShapeCollection   = "collection" // a slice or a map literal, named or not
)

// defaultToGoLiteral converts a JSON Schema default value to a Go literal string
// appropriate for the given Go type. Returns empty string if conversion is not possible
// or if the default value is the zero value for the type (which would be a no-op).
//
// It returns an error when the default value cannot be represented in the target
// Go type without silently inventing a different value — currently, when an
// integer field carries a default with a fractional part (e.g. 4.5 for an
// "integer" type). Type-mismatched defaults (e.g. a string default on an integer
// field) remain silently skipped.
func defaultToGoLiteral(defaultVal any, goType GoType) (string, error) {
	if defaultVal == nil || goType == nil {
		return "", nil
	}

	typeName := goType.GoTypeName()

	switch typeName {
	case "string", "*string":
		if s, ok := defaultVal.(string); ok {
			if typeName == "string" && s == "" {
				return "", nil // zero value, no-op (for non-pointer)
			}
			return strconv.Quote(s), nil
		}
	case "int64", "*int64":
		n, isNum := schemaNumber(defaultVal)
		if !isNum {
			break
		}
		// Read as an exact integer rather than through float64. A default of
		// 9223372036854775807 is an int64 and is not a float64, and asking
		// float64 first both loses it and accepts 9223372036854775808, which no
		// int64 holds.
		intVal, ok := n.Int64()
		if !ok {
			if f, fine := n.Float64(); fine && f == math.Trunc(f) && !math.IsNaN(f) {
				return "", fmt.Errorf("default value %s does not fit in int64", n)
			}
			return "", fmt.Errorf("default value %s is not an integer", n)
		}
		if typeName == "int64" && intVal == 0 {
			return "", nil // zero value, no-op (for non-pointer)
		}
		return strconv.FormatInt(intVal, 10), nil
	case "float64", "*float64":
		n, isNum := schemaNumber(defaultVal)
		if !isNum {
			break
		}
		f, ok := n.Float64()
		if !ok {
			return "", fmt.Errorf("default value %s is not a float64", n)
		}
		if typeName == "float64" && f == 0 {
			return "", nil // zero value, no-op (for non-pointer)
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	case GoNumberTypeName, "*" + GoNumberTypeName:
		// A number held exactly, so the default is written as the literal the
		// schema wrote and not as the shortest decimal that reads back to the
		// same float64. Those are different numbers to a type that keeps every
		// digit: a default of 1.2345678901234567890 would otherwise be planted
		// as 1.2345678901234567, and the value the caller never set would be
		// the one this flag exists to stop them seeing.
		n, isNum := schemaNumber(defaultVal)
		if !isNum {
			break
		}
		// The empty json.Number is the untouched field, so a default that is
		// not written at all cannot be told from one that is. Every other
		// spelling of zero is, and is planted.
		if typeName == GoNumberTypeName && n == "" {
			return "", nil
		}
		return GoNumberTypeName + "(" + strconv.Quote(string(n)) + ")", nil
	case "bool", "*bool":
		if b, ok := defaultVal.(bool); ok {
			if typeName == "bool" && !b {
				return "", nil // zero value (false), no-op (for non-pointer)
			}
			return strconv.FormatBool(b), nil
		}
	}

	// For complex types (arrays, maps, named types), we don't generate defaults.
	// This keeps the implementation simple and avoids generating invalid code.
	// A named type over one of the four scalars above is settled later instead,
	// by resolveNamedTypeDefaults, which can see the declaration it names.
	return "", nil
}

// resolveNamedTypeDefaults writes the defaults defaultToGoLiteral could not,
// once every declaration exists to be looked up.
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
// Held to a type this package declared: a type in another generated package has
// no declaration here to read, and its big-int wrapper's fields are unexported
// there in any case. An alias over time.Time and a raw-value wrapper are structs
// no JSON value converts to, and both keep today's answer, which is to emit no
// default at all.
//
// Runs after the type definitions are complete, in the manner of
// resolveLeafDecodes and for the same reason: the answer is a property of a
// whole declaration, not of the one property that happens to reference it, and
// asking during field construction would make it depend on generation order.
func (g *Generator) resolveNamedTypeDefaults() error {
	for _, td := range g.output.TypeDefs {
		structDef, ok := td.(*StructDef)
		if !ok {
			continue
		}
		for i := range structDef.Fields {
			f := &structDef.Fields[i]
			// pendingDefault is set only where defaultToGoLiteral wrote no
			// literal, so there is nothing here to overwrite and no second test
			// for that.
			if f.pendingDefault == nil {
				continue
			}
			lit, shape, err := g.lateDefaultLiteral(*f.pendingDefault, f.Type)
			if err != nil {
				// A default the target cannot represent -- 4.5 for an integer.
				// Reported rather than dropped, because the identical schema
				// written without the reference reports it, and which of the
				// two an author wrote is exactly the difference issue #172 said
				// nothing may turn on.
				return fmt.Errorf("property %q: %w", f.JSONName, err)
			}
			if lit == "" {
				// No literal and no error is the pass declining: a default of
				// the wrong JSON type for the target, one equal to the zero
				// value of a field that has no way to tell it from absence, or
				// a value no composite literal spells. Emitting the conversion
				// anyway would write T(), which does not compile.
				continue
			}
			f.DefaultLiteral = lit
			f.DefaultShape = shape
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
	}
	return nil
}

// lateDefaultLiteral is the body of resolveNamedTypeDefaults for one field: the
// Go literal for v at the field's declared type, and the DefaultShape that says
// which arm of the SetDefaults template writes it.
//
// Returns "" with no error where nothing can be spelled, which is what leaves a
// property with no default rather than an unsound one.
func (g *Generator) lateDefaultLiteral(v any, t GoType) (lit, shape string, err error) {
	if named, pointer := localNamedType(t); named != nil {
		shape = defaultShapeNamed
		if pointer {
			shape = defaultShapePointerNamed
		}
		if prim := g.primitiveUnderlyingOf(named.Name); prim != nil {
			// The pointer shape is what decides whether a default equal to the
			// zero value is worth emitting: a nil pointer is distinguishable
			// from a present zero, a bare scalar is not. Asking
			// defaultToGoLiteral in the field's own shape keeps that rule in
			// one place.
			inner := GoType(prim)
			if pointer {
				inner = &PointerType{Inner: prim}
			}
			scalar, err := defaultToGoLiteral(v, inner)
			if err != nil || scalar == "" {
				return "", "", err
			}
			return named.Name + "(" + scalar + ")", shape, nil
		}
		if g.isLocalBigIntAlias(named.Name) {
			lit, err := bigIntDefaultLiteral(named.Name, v)
			if err != nil || lit == "" {
				return "", "", err
			}
			return lit, shape, nil
		}
		// A named slice or map: the composite literal is the same one an
		// unnamed collection gets, written under the declared name.
		if under := g.collectionUnderlyingOf(named.Name); under != nil {
			body, ok := g.compositeBody(v, under)
			if !ok {
				return "", "", nil
			}
			if !pointer {
				// A bare named collection is compared against nil, exactly as
				// an unnamed one is; behind a pointer it is the pointer that is
				// tested, and the "*named" arm already does that.
				shape = defaultShapeCollection
			}
			return named.Name + body, shape, nil
		}
		return "", "", nil
	}
	// An unnamed slice or map. Neither is ever pointer-wrapped -- a nil slice
	// and a nil map are already the absent value, which is why the optional
	// shape leaves them alone -- so there is no pointer arm to reach here.
	switch t.(type) {
	case *ArrayType, *MapType:
		body, ok := g.compositeBody(v, t)
		if !ok {
			return "", "", nil
		}
		return t.GoTypeName() + body, defaultShapeCollection, nil
	}
	return "", "", nil
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
func bigIntDefaultLiteral(typeName string, v any) (string, error) {
	n, isNum := schemaNumber(v)
	if !isNum {
		// A default of the wrong JSON type for the target is silently skipped
		// here exactly as it is everywhere else. See defaultToGoLiteral.
		return "", nil
	}
	if i, ok := n.Int64(); ok {
		return typeName + "{_int64: " + strconv.FormatInt(i, 10) + "}", nil
	}
	if f, fine := n.Float64(); fine && f == math.Trunc(f) && !math.IsNaN(f) {
		// An integer the wrapper could hold, and whose digits the generator no
		// longer has: "default" is decoded into an `any`, so a literal past
		// float64's exact range -- 1e30, or int64's own maximum -- arrived here
		// already rounded. Writing the rounded digits would put a number in the
		// generated source that the schema never stated, so nothing is written.
		// The plain int64 target reports this case instead of declining, and can
		// afford to: no int64 holds the value at all, whereas this wrapper does.
		return "", nil
	}
	return "", fmt.Errorf("default value %s is not an integer", n)
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
		if dst.Name == "any" {
			// The element type imposes nothing, so the literal is whatever
			// encoding/json would have decoded the same JSON into -- which is
			// what makes a defaulted value indistinguishable from a decoded one.
			return anyValueLiteral(v)
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
			lit, ok := scalarValueLiteral(v, prim.Name)
			if !ok {
				return "", false
			}
			return dst.Name + "(" + lit + ")", true
		}
		if g.isLocalBigIntAlias(dst.Name) {
			lit, err := bigIntDefaultLiteral(dst.Name, v)
			return lit, err == nil && lit != ""
		}
		if under := g.collectionUnderlyingOf(dst.Name); under != nil {
			body, ok := g.compositeBody(v, under)
			if !ok {
				return "", false
			}
			return dst.Name + body, true
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
		f, ok := n.Float64()
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
	f, ok := n.Float64()
	if !ok {
		return "", false
	}
	return "float64(" + strconv.FormatFloat(f, 'f', -1, 64) + ")", true
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
