package generator

import (
	"fmt"
	"math"
	"strconv"
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

// resolveNamedTypeDefaults writes the defaults of the fields whose Go type is a
// named type, once every declaration exists to be looked up.
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
// Held to a built-in underlying, and to a type this package declared. The
// emitted literal is a conversion, D("dflt"), which is only sound when the
// conversion is: an alias over time.Time, a big-int wrapper and a raw-value
// wrapper are all structs whose declaration a JSON scalar does not convert to,
// and a type in another generated package has no declaration here to read. Each
// of those keeps today's answer, which is to emit no default at all.
//
// Runs after the type definitions are complete, in the manner of
// resolveIntegerDecodes and for the same reason: the answer is a property of a
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
			named, pointer := localNamedType(f.Type)
			if named == nil {
				continue
			}
			prim := g.primitiveUnderlyingOf(named.Name)
			if prim == nil {
				continue
			}
			// The pointer shape is what decides whether a default equal to the
			// zero value is worth emitting: a nil pointer is distinguishable
			// from a present zero, a bare scalar is not. Asking
			// defaultToGoLiteral in the field's own shape keeps that rule in
			// one place.
			shape := GoType(prim)
			if pointer {
				shape = &PointerType{Inner: prim}
			}
			lit, err := defaultToGoLiteral(*f.pendingDefault, shape)
			if err != nil {
				// A default the target cannot represent -- 4.5 for an integer.
				// Reported rather than dropped, because the identical schema
				// written without the reference reports it, and which of the
				// two an author wrote is exactly the difference issue #172 said
				// nothing may turn on.
				return fmt.Errorf("property %q: %w", f.JSONName, err)
			}
			if lit == "" {
				// No literal and no error is defaultToGoLiteral declining: a
				// default of the wrong JSON type for the target, or one equal to
				// the zero value of a field that has no way to tell it from
				// absence. Emitting the conversion anyway would write T(),
				// which does not compile.
				continue
			}
			f.DefaultLiteral = named.Name + "(" + lit + ")"
			f.DefaultShape = "named"
			if pointer {
				f.DefaultShape = "*named"
			}
		}
	}
	return nil
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
