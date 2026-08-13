package generator

import (
	"reflect"
	"strings"
)

// UndeclaredRefType names a $ref that --lenient-refs degraded into a reference
// to a Go type the generated package never declares.
//
// The two ways a degraded ref lands are not equally bad, and a diagnostic that
// cannot tell them apart is worth much less than one that can. Where the
// position can hold `any` -- a property, a $defs entry, an allOf member -- the
// package still builds and the only loss is the checking the reference carried,
// which is what UnresolvedRefs already reports. Where it cannot -- an array
// element, a map value, a oneOf variant -- the emitted source still spells the
// name the reference would have produced, nothing declares it, and the caller's
// `go build` fails on a package schemagen said it had generated. Issue #240.
type UndeclaredRefType struct {
	// Ref is the $ref value, exactly as it appears in File.UnresolvedRefs.
	Ref string
	// TypeName is the Go identifier the emitted source spells and the package
	// does not declare.
	TypeName string
}

// undeclaredRefTypes pairs each never-resolved ref with the undeclared type
// name it left behind, for those refs that left one. refs must be the sorted
// list from neverResolvedRefs; the result keeps that order.
//
// The name is derived the way every emitting path derives it: with no resolved
// schema to take a name from, each of them falls back to refToGoName, and the
// disambiguating steps that follow (goNameForResolvedRef, uniqueTypeName) all
// need the resolved schema this ref does not have.
//
// A ref is reported only when that name is both referenced by the IR and not
// declared by it. Referenced, because a ref whose position became `any` leaves
// no name at all and reporting it would be crying wolf -- the distinction is
// the entire point. Not declared, because a name some other definition already
// claims compiles; it is then the wrong type, which is what the unresolved-ref
// warning itself says, but it is not a build failure.
func (g *Generator) undeclaredRefTypes(refs []string) []UndeclaredRefType {
	if g.output == nil || len(refs) == 0 {
		return nil
	}
	// typeDefsInScope, not this call's own TypeDefs: under --shared-types the
	// other documents of the run write into the same Go package, and a name one
	// of them already declared is one this file may spell and the compiler will
	// find.
	inScope := g.typeDefsInScope()
	declared := make(map[string]bool, len(inScope))
	for _, td := range inScope {
		declared[td.TypeName()] = true
	}
	referenced := referencedTypeNames(g.output)

	var out []UndeclaredRefType
	for _, ref := range refs {
		name := refToGoName(ref)
		if name == "" || declared[name] || !referenced[name] {
			continue
		}
		out = append(out, UndeclaredRefType{Ref: ref, TypeName: name})
	}
	return out
}

// namedTypeType is the IR node that carries a reference to a type by name.
var namedTypeType = reflect.TypeOf(NamedType{})

// referencedTypeNames collects every type name of this package that the IR asks
// the emitted source to spell.
//
// The walk is reflective rather than hand-written on purpose. Type references
// live in GoType trees hanging off struct fields, alias underlyings, oneOf
// variants, map values and tuple positions, and in a further dozen `...TypeName`
// strings that the item-validation and pattern-property defs carry -- a list
// written out here would go stale the first time a def grows a field, and the
// failure mode of a stale list is a build-breaking ref reported as harmless.
// Reflection cannot go stale that way: a new field is walked because it is
// there.
//
// Only the names of this package count. A NamedType with a PkgAlias belongs to
// another generated package and is declared there, so it is skipped -- the
// import, not this file, is what would fail if it were wrong.
func referencedTypeNames(f *File) map[string]bool {
	names := make(map[string]bool)
	if f == nil {
		return names
	}

	type visitKey struct {
		ptr uintptr
		typ reflect.Type
	}
	seen := make(map[visitKey]bool)

	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			// Load-bearing, not insurance. The IR is not a tree: a
			// RuntimeBranchCheck holds the schema node it was compiled from,
			// and a schema node holds DocumentRoot, a back-pointer to the
			// document it belongs to -- so an object-level conditional closes a
			// loop and an unguarded walk recurses until the stack is gone.
			// Removing these three lines takes seven pkg/generator tests down
			// with a stack overflow.
			k := visitKey{v.Pointer(), v.Type()}
			if seen[k] {
				return
			}
			seen[k] = true
			walk(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			walk(v.Elem())
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Map:
			iter := v.MapRange()
			for iter.Next() {
				walk(iter.Value())
			}
		case reflect.Struct:
			if v.Type() == namedTypeType {
				if v.FieldByName("PkgAlias").String() != "" {
					return
				}
				if name := v.FieldByName("Name").String(); name != "" {
					names[name] = true
				}
				return
			}
			for i := 0; i < v.NumField(); i++ {
				fv := v.Field(i)
				if fv.Kind() == reflect.String {
					// `ElemTypeName`, `ItemsTypeName`, `AdditionalItemsTypeName`
					// and friends name a type the template writes out. A plain
					// `Name` is the declaration's own, not a reference to one.
					if strings.HasSuffix(v.Type().Field(i).Name, "TypeName") {
						if s := fv.String(); s != "" {
							names[s] = true
						}
					}
					continue
				}
				walk(fv)
			}
		}
	}
	// reflect can read the value of an unexported field, and that is all this
	// walk does -- it never calls Interface(), which is the operation the
	// read-only flag forbids.
	walk(reflect.ValueOf(f))
	return names
}
