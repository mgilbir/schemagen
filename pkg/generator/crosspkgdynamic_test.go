package generator

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// A cross-package $dynamicRef, and the property that lets it be answered by
// name.
//
// Issue #325: a $dynamicRef into a document another package of the run owns
// declared a second Go type for that package's JSON shape and imported nothing,
// where the same reference spelled $ref emitted `type Root tpkg.T` with the
// import. Both packages compiled, so it was silent type duplication -- #299's
// shape surviving in the one arm #302 deliberately left.
//
// #302 left it because aliasing a $dynamicRef to a *fixed* foreign type says the
// reference has one answer, and which answer that would be was #293's question.
// #293 is decided: the generator's dynamic scope is seeded at the type being
// generated, so a bookended reference resolved inside a type body walks at most
// that type's own resource. The target is then either the bookend the reference
// statically lands on or a declaration inside the type's own resource -- fixed
// by the document the type is generated from either way, and not something a
// caller could have decided differently.
//
// That premise is the whole of the fix's soundness, so it is measured here in
// the configuration the fix is about rather than inherited from the
// single-package measurement TestDynamicScopeStaysAtTheTypeItStartedIn makes.

// dynamicOwnerDocument owns the type the referring documents below point at. The
// $dynamicAnchor is what makes the reference bookended, which is the only shape
// where the dynamic scope is consulted at all.
const dynamicOwnerDocument = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://ex.test/own.json",
  "title": "Owned",
  "$dynamicAnchor": "node",
  "type": "string",
  "minLength": 3
}`

const dynamicRootReferrer = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://ex.test/root.json",
  "title": "RootRef",
  "$dynamicRef": "https://ex.test/own.json#node"
}`

const dynamicPropertyReferrer = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://ex.test/prop.json",
  "title": "PropRef",
  "type": "object",
  "properties": {
    "p": {"$dynamicRef": "https://ex.test/own.json#node"},
    "s": {"type": "array", "items": {"$dynamicRef": "https://ex.test/own.json#node"}},
    "m": {"type": "object", "additionalProperties": {"$dynamicRef": "https://ex.test/own.json#node"}}
  }
}`

// crossPackageDynamicRun generates the owning package and then the referring
// one, and hands back the referring package's file and the generator that built
// it -- the second so that the scope counters can be read.
func crossPackageDynamicRun(t *testing.T, referringBody string) (*File, *Generator) {
	t.Helper()

	const (
		ownerPath    = "ex.test/m/ownpkg"
		referrerPath = "ex.test/m/refpkg"
	)
	owner := parseCrossDoc(t, dynamicOwnerDocument)
	referrer := parseCrossDoc(t, referringBody)

	registry := NewCrossPackageRegistry(map[string]string{
		"https://ex.test/own.json": ownerPath,
		referrer.ID:                referrerPath,
	})
	resolver := schema.NewMappingResolver(map[string]*schema.Schema{
		"https://ex.test/own.json": owner,
		referrer.ID:                referrer,
	})

	ownerGen := New(Config{
		PackageName:  "ownpkg",
		ImportPath:   ownerPath,
		CrossPackage: registry,
		Validation:   ValidationModeStatic,
		Resolver:     resolver,
	})
	if _, err := ownerGen.Generate(owner, WithRootTypeName("Owned")); err != nil {
		t.Fatalf("generating the owning package: %v", err)
	}

	refGen := New(Config{
		PackageName:  "refpkg",
		ImportPath:   referrerPath,
		CrossPackage: registry,
		Validation:   ValidationModeStatic,
		Resolver:     resolver,
	})
	file, err := refGen.Generate(referrer)
	if err != nil {
		t.Fatalf("generating the referring package: %v", err)
	}
	return file, refGen
}

func parseCrossDoc(t *testing.T, body string) *schema.Schema {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("parsing document: %v\n%s", err, body)
	}
	s.Normalize()
	s.ComputeBaseURIs(nil, &s)
	return &s
}

// TestCrossPackageDynamicRefNamesTheOwnerInsteadOfCopying is issue #325 at the
// two positions it reports: the whole of a document, and a property.
//
// Two assertions, and the second is the one a compile gate cannot make. The
// referring package must name the owner's type with its import alias -- and it
// must declare nothing of its own for the shape, because a copy compiles
// perfectly well and leaves the two packages with two Go types for one JSON
// shape, which is exactly what --schema-package is documented to prevent.
func TestCrossPackageDynamicRefNamesTheOwnerInsteadOfCopying(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"the document root is the reference", dynamicRootReferrer},
		{"a property, an element and a map value hold it", dynamicPropertyReferrer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, _ := crossPackageDynamicRun(t, tc.body)

			qualified := 0
			for _, nt := range namedTypesIn(file) {
				if nt.PkgAlias == "ownpkg" && nt.Name == "Owned" {
					qualified++
				}
			}
			if qualified == 0 {
				t.Errorf("the referring package never names ownpkg.Owned, so the cross-package $dynamicRef did not "+
					"cross the boundary by importing. A $ref at the same position emits the qualified name and the "+
					"import; a $dynamicRef whose target the type's own document fixes has to give the same answer "+
					"(issue #325).\n%s", typeDefNames(file))
			}
			for _, td := range file.TypeDefs {
				if td.TypeName() == "Owned" {
					t.Errorf("the referring package declares its own Owned, which ownpkg owns. That compiles, which is "+
						"what makes it silent: two Go types for one JSON shape, the thing --schema-package exists to "+
						"prevent (issues #299, #325).\n%s", typeDefNames(file))
				}
			}
			foundImport := false
			for _, imp := range file.Imports {
				if imp.Path == "ex.test/m/ownpkg" {
					foundImport = true
				}
			}
			if !foundImport {
				t.Errorf("the referring package imports nothing from ex.test/m/ownpkg, so whatever it named is not the "+
					"owner's type: %+v", file.Imports)
			}
		})
	}
}

// TestCrossPackageDynamicScopeStandsAtTheType measures, in cross-package mode,
// the premise the fix above rests on.
//
// If a frame pushed inside a type body were still on the scope when the
// reference is resolved, the target would be decided by a resource the type's
// own document does not fix -- and aliasing it to one package's type would be
// freezing a guess at the package boundary, which is the reason #302 declined to
// do it. The consultation count is what stops the depth from being trivially
// zero: without a bookended reference actually reaching the walk, the invariant
// would hold for the uninteresting reason.
//
// It is a separate measurement from TestDynamicScopeStaysAtTheTypeItStartedIn
// because that one walks testdata/schemas with no cross-package registry at all,
// and the registry is what changes which documents the resolver reaches.
func TestCrossPackageDynamicScopeStandsAtTheType(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"the document root is the reference", dynamicRootReferrer},
		{"a property, an element and a map value hold it", dynamicPropertyReferrer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, gen := crossPackageDynamicRun(t, tc.body)
			if gen.dynamicScopeConsultations == 0 {
				t.Fatalf("the dynamic scope was never consulted, so the depth assertion below says nothing. Either the " +
					"reference stopped being bookended -- in which case this fixture no longer exercises a $dynamicRef " +
					"at all -- or resolution stopped going through resolveDynamicRef")
			}
			if gen.framesAboveTypeScope != 0 {
				t.Errorf("a bookended $dynamicRef was resolved %d frames above the depth its type started at, want 0.\n"+
					"The scope then holds a resource the type's own document does not fix, so the target is a choice a "+
					"caller could have made differently -- and the cross-package arm is aliasing that choice to one "+
					"package's type. That is the freeze #302 declined to perform and #293's seed is what removed; if "+
					"this fails, the alias in generateTypeDefBody's $dynamicRef arm is no longer a true statement.",
					gen.framesAboveTypeScope)
			}
		})
	}
}

// namedTypesIn collects every *NamedType reachable from the file, so the
// assertions above ask about names as the emitter will spell them rather than
// about one position that happened to be looked at.
func namedTypesIn(file *File) []*NamedType {
	var out []*NamedType
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
				if nt, ok := v.Interface().(*NamedType); ok {
					out = append(out, nt)
				}
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
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
			}
		}
	}
	walk(reflect.ValueOf(file))
	return out
}

func typeDefNames(file *File) string {
	names := "declared here:"
	for _, td := range file.TypeDefs {
		names += " " + td.TypeName()
	}
	return names
}
