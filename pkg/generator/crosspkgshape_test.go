package generator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// packageFiles parses every non-test .go file of this package once, for the
// source-reading gates below.
func packageFiles(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) < 5 {
		t.Fatalf("only %d source files parsed; the scans below would be reading almost nothing", len(files))
	}
	return fset, files
}

// goTypeImplementations returns the name of every type in this package that
// implements GoType, read from the source rather than from a list: a type
// implements it exactly when it declares both GoTypeName() string and
// IsPointer() bool on a pointer receiver.
//
// Read from the source because a list is what goes stale. InferredAliasDef
// declares a GoTypeName of its own and is not a GoType -- it has no IsPointer --
// which is why both methods are required rather than the one.
func goTypeImplementations(t *testing.T, files []*ast.File) map[string]*ast.StructType {
	t.Helper()
	methods := map[string]map[string]bool{}
	structs := map[string]*ast.StructType{}
	for _, f := range files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil || len(d.Recv.List) != 1 {
					continue
				}
				star, ok := d.Recv.List[0].Type.(*ast.StarExpr)
				if !ok {
					continue
				}
				id, ok := star.X.(*ast.Ident)
				if !ok {
					continue
				}
				if methods[id.Name] == nil {
					methods[id.Name] = map[string]bool{}
				}
				methods[id.Name][d.Name.Name] = true
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if st, ok := ts.Type.(*ast.StructType); ok {
						structs[ts.Name.Name] = st
					}
				}
			}
		}
	}
	impls := map[string]*ast.StructType{}
	for name, ms := range methods {
		if ms["GoTypeName"] && ms["IsPointer"] {
			st, ok := structs[name]
			if !ok {
				t.Fatalf("%s implements GoType but is not a struct type this scan can read fields off", name)
			}
			impls[name] = st
		}
	}
	return impls
}

// goTypeFields lists the fields of a GoType implementation that themselves hold
// a GoType -- the positions a qualified name can hide in.
func goTypeFields(st *ast.StructType) []string {
	var out []string
	for _, f := range st.Fields.List {
		id, ok := f.Type.(*ast.Ident)
		if !ok || id.Name != "GoType" {
			continue
		}
		for _, n := range f.Names {
			out = append(out, n.Name)
		}
	}
	sort.Strings(out)
	return out
}

// TestCrossPackageNamedDescendsEveryGoTypePosition is the gate that replaces
// crossPackageNamed's old three-arm switch.
//
// That switch walked a pointer and a slice element and stopped, so
// map[string]<foreign>.T answered "no foreign type here" and the map value's
// Validate was dispatched by nobody: {"additionalProperties":{"$ref":"<other
// package>"}} generated `func (r Root) Validate() error { return nil }` and
// accepted a value the referenced schema forbids (issue #295). Every
// neighbouring spelling worked, which is exactly why nothing found it -- a
// keyword honoured in one position and dropped in the one beside it is this
// project's most repeated defect, and the failure mode is silent acceptance.
//
// Inverting the runtime default is not available: there is no answer
// crossPackageNamed could give for a constructor it does not know. So the
// loudness lives here, and it reads the function's own source against the GoType
// implementations the package declares. Adding a constructor, or adding a
// GoType-typed field to an existing one, fails this test until crossPackageNamed
// has an answer for it: a descent, or an entry in crossPackageDescentDeclined
// saying why not.
func TestCrossPackageNamedDescendsEveryGoTypePosition(t *testing.T) {
	fset, files := packageFiles(t)
	impls := goTypeImplementations(t, files)
	if len(impls) < 5 {
		t.Fatalf("only %d GoType implementations found (%v); the source scan has stopped seeing what it reads, "+
			"so this test would pass no matter which constructor crossPackageNamed dropped",
			len(impls), sortedKeysOf(impls))
	}

	fn := findFuncDecl(t, "crossPackageNamed")
	descends := map[string][]string{} // implementation name -> field names walked
	var cased []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		var names []string
		for _, expr := range clause.List {
			star, ok := expr.(*ast.StarExpr)
			if !ok {
				continue
			}
			if id, ok := star.X.(*ast.Ident); ok {
				names = append(names, id.Name)
			}
		}
		if len(names) == 0 {
			return true
		}
		cased = append(cased, names...)
		var walked []string
		ast.Inspect(clause, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok || id.Name != "crossPackageNamed" || len(call.Args) != 1 {
				return true
			}
			sel, ok := call.Args[0].(*ast.SelectorExpr)
			if !ok {
				t.Errorf("crossPackageNamed is called at %s with an argument this scan cannot read as a field of the "+
					"case's value; the descent is no longer decidable from the source", fset.Position(call.Pos()))
				return true
			}
			walked = append(walked, sel.Sel.Name)
			return true
		})
		for _, name := range names {
			descends[name] = append(descends[name], walked...)
		}
		return true
	})

	for _, name := range sortedKeysOf(impls) {
		if !contains(cased, name) {
			t.Errorf("crossPackageNamed has no case for %s, so a qualified name inside one is invisible to it. "+
				"Every caller reads that as \"no foreign type at this position\": the field is left out of "+
				"ValidatableFields and the type's own Validate is called by nobody, which is silent acceptance "+
				"of what the schema forbids (issue #295). Add a case that descends it, or -- if nothing can hide "+
				"there -- a case that says so.", name)
			continue
		}
		for _, field := range goTypeFields(impls[name]) {
			if contains(descends[name], field) {
				continue
			}
			key := name + "." + field
			reason, declined := crossPackageDescentDeclined[key]
			switch {
			case !declined:
				t.Errorf("crossPackageNamed does not descend %s, and crossPackageDescentDeclined does not say why. "+
					"A GoType can hold a qualified name at every position it has; one walked and one not is how "+
					"map[string]<foreign>.T came to be the only unvalidated shape in the family (issue #295). "+
					"Descend it, or record the reason it cannot hold one.", key)
			case strings.TrimSpace(reason) == "":
				t.Errorf("%s is declined with no reason given; an entry with no reason records nothing", key)
			}
		}
	}

	// And the table answers for nothing the traversal already walks, or for a
	// position that no longer exists. A stale entry is a claim that a position
	// has been thought about, which is precisely the impression this table is
	// here to make trustworthy.
	for _, key := range sortedMapKeys(crossPackageDescentDeclined) {
		parts := strings.SplitN(key, ".", 2)
		if len(parts) != 2 {
			t.Errorf("crossPackageDescentDeclined key %q is not of the form Type.Field", key)
			continue
		}
		st, ok := impls[parts[0]]
		if !ok {
			t.Errorf("crossPackageDescentDeclined names %q, and %s is not a GoType implementation", key, parts[0])
			continue
		}
		if !contains(goTypeFields(st), parts[1]) {
			t.Errorf("crossPackageDescentDeclined names %q, and %s has no GoType field %s", key, parts[0], parts[1])
		}
		if contains(descends[parts[0]], parts[1]) {
			t.Errorf("crossPackageDescentDeclined declines %q, which crossPackageNamed walks; one of the two is stale", key)
		}
	}
}

// TestCrossPackageNamedFindsAQualifiedNameAtEveryDepth is the behavioural half:
// the gate above holds that the source names every constructor, and this holds
// that naming it actually finds anything. A case that fell through to nil would
// satisfy the scan and answer nothing.
func TestCrossPackageNamedFindsAQualifiedNameAtEveryDepth(t *testing.T) {
	foreign := &NamedType{Name: "T", PkgAlias: "tpkg"}
	local := &NamedType{Name: "T"}
	str := &PrimitiveType{Name: "string"}

	for _, tc := range []struct {
		name string
		typ  GoType
	}{
		{"the type itself", foreign},
		{"*tpkg.T", &PointerType{Inner: foreign}},
		{"[]tpkg.T", &ArrayType{ItemType: foreign}},
		{"map[string]tpkg.T", &MapType{KeyType: str, ValueType: foreign}},
		{"map[string][]tpkg.T", &MapType{KeyType: str, ValueType: &ArrayType{ItemType: foreign}}},
		{"[]map[string]tpkg.T", &ArrayType{ItemType: &MapType{KeyType: str, ValueType: foreign}}},
		{"*map[string]*tpkg.T", &PointerType{Inner: &MapType{KeyType: str, ValueType: &PointerType{Inner: foreign}}}},
	} {
		if got := crossPackageNamed(tc.typ); got != foreign {
			t.Errorf("crossPackageNamed(%s) = %v, want the qualified tpkg.T. Every caller reads nil as "+
				"\"this position holds no foreign type\", and drops the check the foreign type carries", tc.name, got)
		}
	}

	for _, tc := range []struct {
		name string
		typ  GoType
	}{
		{"string", str},
		{"a local T", local},
		{"[]string", &ArrayType{ItemType: str}},
		{"map[string]string", &MapType{KeyType: str, ValueType: str}},
		{"map[tpkg.T]string", &MapType{KeyType: foreign, ValueType: str}},
	} {
		if got := crossPackageNamed(tc.typ); got != nil {
			t.Errorf("crossPackageNamed(%s) = %v, want nil", tc.name, got)
		}
	}
}

// ---------------------------------------------------------------------------
// typeShape: the answers a referencing package cannot work out for itself.
// ---------------------------------------------------------------------------

// TestEveryTypeShapeFieldIsPublished holds that the owning generator fills every
// field of the record it publishes.
//
// A field left out of the publication is zero for every foreign type, and zero
// is a plausible answer for all of them -- "not a struct", "not a collection",
// "carries no Validate" -- so nothing downstream complains. What happens instead
// is that the referencing package silently takes the shape of a type it has
// never seen to be the shape of nothing, which is the whole of issue #296: an
// absent optional property of a foreign type was written out as null or {} and
// the type then refused to read its own output back.
func TestEveryTypeShapeFieldIsPublished(t *testing.T) {
	_, files := packageFiles(t)
	declared := typeShapeFieldNames()
	if len(declared) < 5 {
		t.Fatalf("typeShape declares only %d fields (%v); this test is reading the wrong type", len(declared), declared)
	}

	assigned := map[string]bool{}
	found := false
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			id, ok := lit.Type.(*ast.Ident)
			if !ok || id.Name != "typeShape" {
				return true
			}
			// Only the publication has keys; a bare typeShape{} elsewhere is
			// not a claim about any field.
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok {
					assigned[key.Name] = true
					found = true
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("no keyed typeShape literal found in the package source; the publication this test reads has moved or gone")
	}
	for _, name := range declared {
		if !assigned[name] {
			t.Errorf("typeShape.%s is never assigned in the published record, so every referencing package "+
				"reads it as its zero value -- an answer that looks exactly like a real one. "+
				"Fill it in Generate's publication from the predicate the referencing side will ask.", name)
		}
	}
}

// TestEveryTypeShapeFieldIsRead holds the other direction: a field published and
// read by nobody is a claim no code depends on and no test can break, which is
// the shape a stale record takes.
func TestEveryTypeShapeFieldIsRead(t *testing.T) {
	_, files := packageFiles(t)
	read := map[string]bool{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			outer, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			inner, ok := outer.X.(*ast.SelectorExpr)
			if !ok || inner.Sel.Name != "foreign" {
				return true
			}
			read[outer.Sel.Name] = true
			return true
		})
	}
	for _, name := range typeShapeFieldNames() {
		if !read[name] {
			t.Errorf("nothing reads typeShape.%s off a foreign NamedType. Either a predicate that should be "+
				"asking the owning package is still asking this package's own type table -- which answers about "+
				"a namesake or about nothing (issue #296) -- or the field is dead and should go.", name)
		}
	}
}

// typeShapeFieldNames lists typeShape's fields, so the gates above enumerate
// what the type declares rather than what somebody remembered to list.
func typeShapeFieldNames() []string {
	rt := reflect.TypeOf(typeShape{})
	out := make([]string, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		out = append(out, rt.Field(i).Name)
	}
	sort.Strings(out)
	return out
}

// TestForeignTypeIsNeverJudgedByALocalNamesake is the behavioural gate, and it
// is the one that fails under the defect rather than under a refactor.
//
// Each case sets this package's type table to hold a type called T whose shape
// is the *opposite* of the foreign tpkg.T being asked about. Answering from the
// local table is then not merely uninformative but wrong in a way that shows: it
// gives the namesake's answer. crossPackageNamed's own comment has named this
// hazard since it was written -- "the local table happening to hold the same name
// says nothing about the foreign type" -- and the omitzero family was deciding by
// exactly that lookup.
func TestForeignTypeIsNeverJudgedByALocalNamesake(t *testing.T) {
	// The local T: a plain string alias. Not a struct, not a collection, not an
	// interface, not a raw wrapper, and it does carry a Validate.
	localDefs := []TypeDef{
		&AliasDef{Name: "T", Underlying: &PrimitiveType{Name: "string"}},
	}
	newGen := func() *Generator {
		g := New(Config{PackageName: "rootpkg"})
		g.output = &File{PackageName: "rootpkg", TypeDefs: append([]TypeDef(nil), localDefs...)}
		return g
	}
	// Every answer the opposite of what the local namesake would give.
	published := typeShape{
		ZeroLiteral:       "tpkgZero",
		Validatable:       false,
		ZeroLossy:         true,
		Struct:            true,
		Collection:        true,
		Interface:         true,
		RawWrapper:        true,
		AliasDropsMethods: true,
		Unmarshaler:       true,
		Marshaler:         true,
		// A single byte with no pointer in it, which is the opposite of the
		// two-word, pointer-carrying string the local namesake resolves to.
		Layout:      goLayout{Size: 1, Align: 1},
		LayoutKnown: true,
	}
	foreign := func() *NamedType {
		return &NamedType{Name: "T", PkgAlias: "tpkg", foreign: published}
	}

	// One check per typeShape field, and the count is held against the type
	// below so a new field cannot be added without one.
	checks := map[string]func(t *testing.T){
		"ZeroLiteral": func(t *testing.T) {
			got, ok := crossPackageZeroLiteral(foreign())
			if !ok || got != "tpkgZero" {
				t.Errorf("crossPackageZeroLiteral = %q (ok=%v), want the published %q", got, ok, "tpkgZero")
			}
		},
		"Validatable": func(t *testing.T) {
			// The local namesake is the one that carries a Validate. Reading
			// the local table puts a dispatch on a foreign type that declares
			// no such method, which does not compile.
			local := map[string]bool{"T": true}
			for _, position := range []GoType{
				foreign(),
				&ArrayType{ItemType: foreign()},
				&MapType{KeyType: &PrimitiveType{Name: "string"}, ValueType: foreign()},
			} {
				if namedTypeValidatable(position, "T", local) {
					t.Errorf("namedTypeValidatable took the local table's answer for %s; "+
						"the two sources must be asked in order, not or-ed together", position.GoTypeName())
				}
			}
		},
		"ZeroLossy": func(t *testing.T) {
			if !newGen().isZeroLossyNamedType(foreign()) {
				t.Error("isZeroLossyNamedType answered from the local string alias rather than the published record")
			}
		},
		"Struct": func(t *testing.T) {
			if !newGen().isStructTypeNamed(foreign()) {
				t.Error("isStructTypeNamed answered no for a foreign type published as a struct. An optional " +
					"property of one is not pointer-wrapped then, and omitempty never omits a struct, so a property " +
					"the document did not carry is written back out as {} (issue #296)")
			}
		},
		"Collection": func(t *testing.T) {
			if !newGen().isCollectionType(foreign()) {
				t.Error("isCollectionType answered no for a foreign type published as a slice or map alias, " +
					"so the field takes ,omitempty instead of ,omitzero and a present-but-empty [] is erased")
			}
		},
		"Interface": func(t *testing.T) {
			g := newGen()
			if !g.isInterfaceType(foreign()) {
				t.Error("isInterfaceType answered from the local table for a foreign name")
			}
			aliases := map[string]*AliasDef{"T": {Name: "T", Underlying: &PrimitiveType{Name: "string"}}}
			if canHaveMethodsResolved(foreign(), aliases) {
				t.Error("canHaveMethodsResolved followed a local alias of the same name to decide whether a " +
					"foreign type can carry methods; Go permits none on a type whose underlying resolves to any")
			}
		},
		"RawWrapper": func(t *testing.T) {
			if !newGen().isRawValueWrapperType(foreign()) {
				t.Error("isRawValueWrapperType answered no for a foreign type published as a raw-value wrapper, " +
					"so an absent optional property of it marshals as null (issue #296)")
			}
		},
		"AliasDropsMethods": func(t *testing.T) {
			if !newGen().aliasDropsMethods(foreign()) {
				t.Error("aliasDropsMethods answered no for a foreign wrapper, so a $ref at a document root " +
					"would be aliased to a type whose UnmarshalJSON and Validate the alias does not inherit")
			}
		},
		"Unmarshaler": func(t *testing.T) {
			ad := aliasOverForeign(t, foreign())
			if ad.UnmarshalAs != "tpkg.T" {
				t.Errorf("UnmarshalAs = %q, want the qualified tpkg.T: an alias over a foreign type that declares "+
					"its own UnmarshalJSON inherits none of it and has to convert and delegate", ad.UnmarshalAs)
			}
		},
		"Marshaler": func(t *testing.T) {
			ad := aliasOverForeign(t, foreign())
			if ad.MarshalAs != "tpkg.T" {
				t.Errorf("MarshalAs = %q, want the qualified tpkg.T", ad.MarshalAs)
			}
		},
		"Layout": func(t *testing.T) {
			got, ok := newLayoutTable(localDefs).of(foreign())
			if !ok {
				t.Fatal("the published layout was not read at all, so a struct holding a foreign member " +
					"keeps the order its members were built in for no reason")
			}
			if got != published.Layout {
				t.Errorf("layout = %+v, want the published %+v. The local namesake is a string, so an answer "+
					"of {16 8 8} is that alias's layout and not the foreign type's -- and a member placed by "+
					"it is placed by a size and a pointer extent the type does not have", got, published.Layout)
			}
		},
		"LayoutKnown": func(t *testing.T) {
			// The owning package could not work its own type out. Falling back
			// to the local table here is the same defect as above, arrived at
			// from the other direction.
			unknown := &NamedType{Name: "T", PkgAlias: "tpkg", foreign: typeShape{}}
			if got, ok := newLayoutTable(localDefs).of(unknown); ok {
				t.Errorf("layout = %+v for a foreign type published with none; an unknown layout has to stay "+
					"unknown, since the only other answer available is the namesake's", got)
			}
		},
	}

	declared := typeShapeFieldNames()
	for _, name := range declared {
		if _, ok := checks[name]; !ok {
			t.Errorf("typeShape.%s has no check here. A published answer nothing is held to is one nobody "+
				"can see go wrong, and the whole family it belongs to went wrong in silence (issue #296).", name)
		}
	}
	for name := range checks {
		if !contains(declared, name) {
			t.Errorf("this test checks typeShape.%s, which typeShape no longer declares", name)
		}
	}
	for _, name := range declared {
		if check, ok := checks[name]; ok {
			t.Run(name, check)
		}
	}
}

// aliasOverForeign runs populateAliasDelegates over `type Local tpkg.T` and
// returns the alias, so the two delegate fields can be read off it.
func aliasOverForeign(t *testing.T, foreign *NamedType) *AliasDef {
	t.Helper()
	g := New(Config{PackageName: "rootpkg"})
	ad := &AliasDef{Name: "Local", Underlying: foreign}
	g.output = &File{
		PackageName: "rootpkg",
		TypeDefs: []TypeDef{
			// The namesake, declaring neither method. Reading the delegate
			// decision off it leaves the alias with no delegation at all.
			&AliasDef{Name: "T", Underlying: &PrimitiveType{Name: "string"}},
			ad,
		},
	}
	g.populateAliasDelegates()
	return ad
}

func sortedKeysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
