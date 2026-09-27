package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// This file holds the name registry to its one promise: whatever a document
// spells, every identifier the generated package declares is declared once, and
// every position is typed by its own schema.
//
// The schemas are generated to collide. Every source of a Go name draws from one
// small alphabet chosen so the sources fold onto one another: $defs keys that
// differ only by punctuation or case ("root_a", "root-a", "RootA"), keys spelled
// as the names positions derive (RootA, RootAItem, RootAValue, RootAOption0),
// titles of the root and of oneOf variants, enum members whose constants spell a
// definition's name (enum A with member "B" is AB; so is $defs "a_b"), getters
// against fields ("getCat" beside a variant titled Cat), and the names generated
// code already spells (SchemagenValidationMode). Each schema holds a handful of
// properties, each one a different way of reaching a schema node: inline, by
// $ref, as array items, as a map value, as a oneOf variant, behind a nullable
// oneOf, as an enum.
//
// Every node a property reaches is a payload of its own -- an object requiring
// one key no other payload has, and admitting no other -- so a document tells
// them apart: one that satisfies the node's own payload must be accepted at that
// property, and one that satisfies a different payload must be refused. A
// position bound to another node's type fails exactly that, in one direction or
// the other, which is how the wrong bindings showed up in the first place.
//
// What is required of every schema, all compiled into one module and run:
//
//   - generation succeeds (a "defect in schemagen" refusal is a failure here);
//   - the package builds and `go vet` is clean;
//   - every document gets its schema's verdict at every property;
//   - every name the generator's NameMoves mentions is one the package declares.

// collisionAlphabet is what names are drawn from. Chosen to fold, not to be
// realistic.
var (
	collisionDefKeys = []string{
		"RootA", "root_a", "root-a", "rootA", "ROOT_A",
		"RootAItem", "root_a_item", "RootAValue", "RootAOption0", "RootAOption1",
		"A", "a", "a_", "AB", "a_b", "a-b", "Cat", "cat", "GetCat", "RootGetCat",
		"String", "String2", "Integer2", "X1", "Root", "root",
		"SchemagenValidationMode", "SchemagenValidationCapability",
		"RootAAccessRules", "RootASchema", "RootKids",
	}
	collisionPropKeys = []string{"a", "A", "a_", "a-b", "a_b", "get_cat", "getCat", "item", "b", "cat"}
	collisionTitles   = []string{"A", "Root A", "Cat", "String2", "AB", "RootAItem", "Root"}
)

// collisionSchema is one generated schema and the documents that tell its
// positions apart.
type collisionSchema struct {
	Name      string
	Schema    map[string]any
	Config    generator.Config
	Instances []notInstance
}

type collisionBuilder struct {
	r        *rand.Rand
	defs     map[string]any
	props    map[string]any
	payloads int
	cases    []notInstance
}

// payload is a node only one kind of document satisfies: an object that must
// hold key k<n> and nothing else.
func (b *collisionBuilder) payload() (map[string]any, int) {
	n := b.payloads
	b.payloads++
	key := fmt.Sprintf("k%d", n)
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{key: map[string]any{"type": "string"}},
		"required":             []any{key},
		"additionalProperties": false,
	}, n
}

func witness(n int) string { return fmt.Sprintf(`{"k%d":"v"}`, n) }

// other is the payload made after n -- another node of the same schema,
// usually, whose document this node must refuse. A position bound to that
// node's type accepts it.
func (b *collisionBuilder) other(n int) int { return n + 1 }

func (b *collisionBuilder) pick(from []string) string { return from[b.r.IntN(len(from))] }

// def adds a payload definition under a key from the alphabet that is not yet
// taken, and returns its key and payload number.
func (b *collisionBuilder) def() (string, int) {
	for {
		key := b.pick(collisionDefKeys)
		if _, taken := b.defs[key]; taken {
			continue
		}
		p, n := b.payload()
		b.defs[key] = p
		return key, n
	}
}

func refTo(key string) map[string]any {
	return map[string]any{"$ref": "#/$defs/" + escapePointerToken(key)}
}

func escapePointerToken(s string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(s)
}

// addProperty adds one property of a randomly chosen shape, and the documents
// that decide whether its position carries its own schema.
func (b *collisionBuilder) addProperty() {
	var prop string
	for {
		prop = b.pick(collisionPropKeys)
		if _, taken := b.props[prop]; !taken {
			break
		}
	}
	doc := func(value string) string { return fmt.Sprintf(`{%q:%s}`, prop, value) }
	accept := func(what, value string) {
		b.cases = append(b.cases, notInstance{Name: prop + ": " + what, Doc: doc(value), Valid: true,
			Why: "a document this property's own schema admits"})
	}
	refuse := func(what, value string) {
		b.cases = append(b.cases, notInstance{Name: prop + ": " + what, Doc: doc(value), Valid: false,
			Why: "a document only another node's schema admits: accepted means the position carries that node's type"})
	}

	switch b.r.IntN(9) {
	case 0: // inline object: a position-derived name
		p, n := b.payload()
		b.props[prop] = p
		accept("own", witness(n))
		refuse("another's", witness(b.other(n)))
	case 1: // a $ref to a definition
		key, n := b.def()
		b.props[prop] = refTo(key)
		accept("own", witness(n))
		refuse("another's", witness(b.other(n)))
	case 2: // array items, inline
		p, n := b.payload()
		b.props[prop] = map[string]any{"type": "array", "items": p}
		accept("own", "["+witness(n)+"]")
		refuse("another's", "["+witness(b.other(n))+"]")
	case 3: // array items behind a $ref
		key, n := b.def()
		b.props[prop] = map[string]any{"type": "array", "items": refTo(key)}
		accept("own", "["+witness(n)+"]")
		refuse("another's", "["+witness(b.other(n))+"]")
	case 4: // a map value
		p, n := b.payload()
		b.props[prop] = map[string]any{"type": "object", "additionalProperties": p}
		accept("own", `{"m":`+witness(n)+`}`)
		refuse("another's", `{"m":`+witness(b.other(n))+`}`)
	case 5: // a oneOf whose object variants may carry colliding titles
		first, n1 := b.payload()
		second, n2 := b.payload()
		if b.r.IntN(2) == 0 {
			first["title"] = b.pick(collisionTitles)
		}
		if b.r.IntN(2) == 0 {
			second["title"] = b.pick(collisionTitles)
		}
		b.props[prop] = map[string]any{"oneOf": []any{first, second, map[string]any{"type": "integer"}}}
		accept("first variant", witness(n1))
		accept("second variant", witness(n2))
		accept("scalar variant", "7")
		refuse("another's", witness(b.other(n2)))
	case 6: // a nullable reference
		key, n := b.def()
		b.props[prop] = map[string]any{"oneOf": []any{map[string]any{"type": "null"}, refTo(key)}}
		accept("own", witness(n))
		accept("null", "null")
		refuse("another's", witness(b.other(n)))
	case 7: // an enum definition, whose constants spell names too
		var key string
		for {
			key = b.pick(collisionDefKeys)
			if _, taken := b.defs[key]; !taken {
				break
			}
		}
		b.defs[key] = map[string]any{"enum": []any{"B", "c", "Item"}}
		b.props[prop] = refTo(key)
		accept("member", `"B"`)
		accept("member", `"Item"`)
		refuse("non-member", `"zz"`)
	case 8: // an inline enum
		b.props[prop] = map[string]any{"enum": []any{"B", "Cat", "item"}}
		accept("member", `"Cat"`)
		refuse("non-member", `"b"`)
	}
}

// buildCollisionSchema generates the i-th schema of the sweep.
func buildCollisionSchema(seed uint64) collisionSchema {
	b := &collisionBuilder{
		r:     rand.New(rand.NewPCG(seed, 0x5eed)),
		defs:  map[string]any{},
		props: map[string]any{},
	}
	n := 3 + b.r.IntN(6)
	for i := 0; i < n; i++ {
		b.addProperty()
	}
	// A few definitions nothing references, which still claim their names.
	for i := 1 + b.r.IntN(4); i > 0; i-- {
		b.def()
	}
	root := map[string]any{
		"$schema":    "https://json-schema.org/draft/2020-12/schema",
		"type":       "object",
		"properties": b.props,
		"$defs":      b.defs,
	}
	cfg := generator.Config{OmitEmpty: true}
	if b.r.IntN(3) == 0 {
		root["title"] = b.pick(collisionTitles)
	}
	// A runtime feature, so that --validation hybrid emits the capability block
	// and its SchemagenValidationMode is in the package.
	if b.r.IntN(3) == 0 {
		root["$dynamicAnchor"] = "node"
		b.props["kids"] = map[string]any{"type": "array", "items": map[string]any{"$dynamicRef": "#node"}}
		cfg.Validation = generator.ValidationModeHybrid
	}
	return collisionSchema{
		Name:      fmt.Sprintf("seed%d", seed),
		Schema:    root,
		Config:    cfg,
		Instances: b.cases,
	}
}

// auditCollisionSchemas are the collisions the audit reported, as fixed members
// of the sweep: each of them is reproduced exactly, with documents that tell the
// colliding nodes apart.
func auditCollisionSchemas() []collisionSchema {
	obj := func(key string, typ string) map[string]any {
		return map[string]any{"type": "object", "properties": map[string]any{key: map[string]any{"type": typ}}, "required": []any{key}}
	}
	return []collisionSchema{
		{
			// A $defs entry holding the name a position derives. The items
			// type took RootAItem, the $defs integer, and {"a":[{"z":"q"}]}
			// did not decode.
			Name: "position_vs_defs",
			Schema: map[string]any{
				"$defs":      map[string]any{"RootAItem": map[string]any{"type": "integer", "minimum": 5}},
				"properties": map[string]any{"a": map[string]any{"type": "array", "items": obj("z", "string")}, "d": refTo("RootAItem")},
			},
			Instances: []notInstance{
				{Name: "items", Doc: `{"a":[{"z":"q"}]}`, Valid: true, Why: "the elements are objects, not the $defs integer"},
				{Name: "items refused", Doc: `{"a":[7]}`, Valid: false, Why: "7 is the $defs integer's, not the element's"},
				{Name: "defs", Doc: `{"d":7}`, Valid: true, Why: "control: the definition keeps its own type"},
				{Name: "defs bound", Doc: `{"d":3}`, Valid: false, Why: "control: and its minimum"},
			},
		},
		{
			// The same with enum items: a second declaration of RootAItem, and a
			// "defect in schemagen" refusal.
			Name: "enum_items_vs_defs",
			Schema: map[string]any{
				"$defs":      map[string]any{"RootAItem": map[string]any{"type": "integer", "minimum": 5}},
				"properties": map[string]any{"a": map[string]any{"type": "array", "items": map[string]any{"enum": []any{"x", "y"}}}, "d": refTo("RootAItem")},
			},
			Instances: []notInstance{
				{Name: "member", Doc: `{"a":["x"]}`, Valid: true, Why: "generation used to be refused"},
				{Name: "non-member", Doc: `{"a":["z"]}`, Valid: false, Why: "control"},
				{Name: "defs", Doc: `{"d":7}`, Valid: true, Why: "control"},
			},
		},
		{
			// $defs a-b and a_b fold to one name; the nullable-oneOf arm named
			// its target off the reference text and bound a-b's type.
			Name: "nullable_ref_folded_defs",
			Schema: map[string]any{
				"$defs": map[string]any{
					"a-b": obj("s", "string"),
					"a_b": obj("n", "integer"),
				},
				"properties": map[string]any{
					"x": refTo("a-b"),
					"y": map[string]any{"oneOf": []any{map[string]any{"type": "null"}, refTo("a_b")}},
				},
			},
			Instances: []notInstance{
				{Name: "y own", Doc: `{"y":{"n":1}}`, Valid: true, Why: "refused as a-b's struct (y.s required)"},
				{Name: "y other", Doc: `{"y":{"s":"q"}}`, Valid: false, Why: "n is required at y"},
				{Name: "x own", Doc: `{"x":{"s":"q"}}`, Valid: true, Why: "control"},
			},
		},
		{
			// A titled inline variant took the existing $defs/Foo.
			Name: "titled_variant_vs_defs",
			Schema: map[string]any{
				"$defs": map[string]any{"Foo": obj("a", "string")},
				"properties": map[string]any{
					"f": refTo("Foo"),
					"p": map[string]any{"oneOf": []any{
						map[string]any{"title": "Foo", "type": "object", "properties": map[string]any{"b": map[string]any{"type": "integer"}}, "required": []any{"b"}},
						map[string]any{"type": "string"},
					}},
				},
			},
			Instances: []notInstance{
				{Name: "variant own", Doc: `{"p":{"b":1}}`, Valid: true, Why: "refused for a missing p.a"},
				{Name: "defs own", Doc: `{"f":{"a":"x"}}`, Valid: true, Why: "control"},
				{Name: "defs other", Doc: `{"f":{"b":1}}`, Valid: false, Why: "control: f requires a"},
			},
		},
		{
			// A variant titled String2 beside two "String" variants: the old
			// suffix appended a count without asking.
			Name: "variant_suffix_vs_title",
			Schema: map[string]any{
				"properties": map[string]any{
					"p": map[string]any{"oneOf": []any{
						map[string]any{"type": "string", "maxLength": 2},
						map[string]any{"type": "string", "minLength": 5},
						map[string]any{"title": "String2", "type": "object", "properties": map[string]any{"z": map[string]any{"type": "boolean"}}, "required": []any{"z"}},
					}},
				},
			},
			Instances: []notInstance{
				{Name: "short", Doc: `{"p":"ab"}`, Valid: true, Why: "control"},
				{Name: "long", Doc: `{"p":"abcdef"}`, Valid: true, Why: "control"},
				{Name: "object", Doc: `{"p":{"z":true}}`, Valid: true, Why: "the titled variant, whose wrapper and getter String2 collided"},
				{Name: "neither", Doc: `{"p":"abc"}`, Valid: false, Why: "control"},
			},
		},
		{
			// An enum constant spelling a definition's type name.
			Name: "enum_const_vs_type",
			Schema: map[string]any{
				"$defs":      map[string]any{"A": map[string]any{"enum": []any{"B", "c"}}, "a_b": map[string]any{"type": "integer"}},
				"properties": map[string]any{"x": refTo("A"), "y": refTo("a_b")},
			},
			Instances: []notInstance{
				{Name: "member", Doc: `{"x":"B"}`, Valid: true, Why: "generation used to be refused (AB declared twice)"},
				{Name: "non-member", Doc: `{"x":"b"}`, Valid: false, Why: "control"},
				{Name: "integer", Doc: `{"y":1}`, Valid: true, Why: "control"},
				{Name: "not integer", Doc: `{"y":"B"}`, Valid: false, Why: "control: y is a_b's integer, not the constant's enum"},
			},
		},
		{
			// A getter and a field of one name.
			Name: "getter_vs_field",
			Schema: map[string]any{
				"properties": map[string]any{
					"getCat": map[string]any{"type": "string"},
					"p": map[string]any{"oneOf": []any{
						map[string]any{"title": "Cat", "type": "object", "properties": map[string]any{"m": map[string]any{"type": "string"}}, "required": []any{"m"}},
						map[string]any{"type": "integer"},
					}},
				},
			},
			Instances: []notInstance{
				{Name: "field", Doc: `{"getCat":"x"}`, Valid: true, Why: "did not compile (field and method GetCat)"},
				{Name: "variant", Doc: `{"p":{"m":"y"}}`, Valid: true, Why: "control"},
				{Name: "neither", Doc: `{"p":{"q":"y"}}`, Valid: false, Why: "control"},
			},
		},
		{
			// A definition named like the capability block's constant.
			Name: "helper_shaped_def",
			Schema: map[string]any{
				"$schema":        "https://json-schema.org/draft/2020-12/schema",
				"$dynamicAnchor": "node",
				"$defs":          map[string]any{"SchemagenValidationMode": map[string]any{"type": "string", "minLength": 2}},
				"properties": map[string]any{
					"x":    refTo("SchemagenValidationMode"),
					"kids": map[string]any{"type": "array", "items": map[string]any{"$dynamicRef": "#node"}},
				},
			},
			Config: generator.Config{Validation: generator.ValidationModeHybrid},
			Instances: []notInstance{
				{Name: "own", Doc: `{"x":"ab"}`, Valid: true, Why: "generation used to be refused under hybrid"},
				{Name: "bound", Doc: `{"x":"a"}`, Valid: false, Why: "control: the definition's minLength"},
			},
		},
		{
			// Two references whose last pointer token is the same, into nodes no
			// position had named first: the library used to give both one type.
			Name: "same_last_token_refs",
			Schema: map[string]any{
				"properties": map[string]any{
					"a": map[string]any{"$ref": "#/x-alpha/1"},
					"b": map[string]any{"$ref": "#/x-beta/1"},
				},
				"x-alpha": []any{map[string]any{}, obj("s", "string")},
				"x-beta":  []any{map[string]any{}, obj("i", "integer")},
			},
			Instances: []notInstance{
				{Name: "a own", Doc: `{"a":{"s":"x"}}`, Valid: true, Why: "control"},
				{Name: "b own", Doc: `{"b":{"i":1}}`, Valid: true, Why: "(h): refused as a's type (b.s required)"},
				{Name: "b other", Doc: `{"b":{"s":"x"}}`, Valid: false, Why: "(h): i is required at b"},
			},
		},
	}
}

// collisionSweepSize is how many generated schemas the sweep runs beside the
// fixed ones, and collisionSharedPairs how many pairs of them it also generates
// into one package with SharedTypes -- two documents drawing their $defs keys
// from one alphabet, so that same-named definitions of different documents
// contest one name space too. One module compiles them all, so the cost is one
// build.
const (
	collisionSweepSize   = 80
	collisionSharedPairs = 20
)

// collisionUnit is one generated package: a document, or several generated
// into one package with SharedTypes.
type collisionUnit struct {
	Name   string
	Docs   []collisionSchema
	Shared bool
}

func TestNameCollisionsNeverShareOrBreakATypeName(t *testing.T) {
	var units []collisionUnit
	for _, c := range auditCollisionSchemas() {
		units = append(units, collisionUnit{Name: c.Name, Docs: []collisionSchema{c}})
	}
	for seed := uint64(1); seed <= collisionSweepSize; seed++ {
		c := buildCollisionSchema(seed)
		units = append(units, collisionUnit{Name: c.Name, Docs: []collisionSchema{c}})
	}
	for pair := uint64(1); pair <= collisionSharedPairs; pair++ {
		a, b := buildCollisionSchema(1000+2*pair), buildCollisionSchema(1001+2*pair)
		// Shared types and a validation-capability block are refused together
		// (the block is per file); the pairs are the name-space question alone.
		a.Config.Validation, b.Config.Validation = "", ""
		units = append(units, collisionUnit{Name: fmt.Sprintf("shared%d", pair), Docs: []collisionSchema{a, b}, Shared: true})
	}

	dir := t.TempDir()
	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}
	var mainBody strings.Builder
	var imports []string
	// How much the sweep exercised: a sweep in which nothing collided proves
	// nothing about collisions, so the count is asserted below.
	moves, documents, colliding := 0, 0, 0
	for i, unit := range units {
		pkg := fmt.Sprintf("c%d", i)
		pkgDir := filepath.Join(dir, pkg)
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		cfg := unit.Docs[0].Config
		cfg.PackageName = pkg
		cfg.OmitEmpty = true
		cfg.SharedTypes = unit.Shared
		gen := generator.New(cfg)
		var sources strings.Builder
		var raws []string
		for d, tc := range unit.Docs {
			raw, err := json.Marshal(tc.Schema)
			if err != nil {
				t.Fatal(err)
			}
			raws = append(raws, string(raw))
			schemaPath := filepath.Join(pkgDir, fmt.Sprintf("schema%d.json", d))
			if err := os.WriteFile(schemaPath, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			s, err := schema.LoadFromFile(schemaPath)
			if err != nil {
				t.Fatalf("%s: loading: %v", unit.Name, err)
			}
			s.NormalizeForDraft(schema.DraftUnknown)
			var opts []generator.GenerateOption
			if unit.Shared {
				// Distinct roots, which shared types requires; everything else
				// the two documents name is left to collide.
				opts = append(opts, generator.WithRootTypeName(fmt.Sprintf("Doc%d", d)))
			}
			ir, err := gen.Generate(s, opts...)
			if err != nil {
				t.Fatalf("%s: generation refused a legal schema: %v\nschemas: %s", unit.Name, err, strings.Join(raws, "\n"))
			}
			// The root keeps whatever name its title gives it, so that the
			// title is one of the colliding sources; the registry says what it
			// became.
			rootType, ok := gen.DeclaredTypeName(s)
			if !ok {
				t.Fatalf("%s: the registry has no declaration for the root\nschemas: %s", unit.Name, strings.Join(raws, "\n"))
			}
			src, err := em.Emit(ir)
			if err != nil {
				t.Fatalf("%s: emit: %v\nschemas: %s", unit.Name, err, strings.Join(raws, "\n"))
			}
			if err := os.WriteFile(filepath.Join(pkgDir, fmt.Sprintf("types%d.go", d)), src, 0o644); err != nil {
				t.Fatal(err)
			}
			sources.Write(src)
			documents += len(tc.Instances)
			fmt.Fprintf(&mainBody, "\trun(%q, %s, func(b []byte) error { var v %s.%s; if err := json.Unmarshal(b, &v); err != nil { return err }; if x, ok := any(v).(interface{ Validate() error }); ok { return x.Validate() }; return nil })\n",
				unit.Name+"/"+tc.Name, collisionCasesLiteral(tc.Instances), pkg, rootType)
		}
		writeSharedHelpers(t, pkgDir, sources.String())

		// Every name a move report mentions is one the package declares.
		declared := packageLevelNames(t, pkgDir)
		if len(gen.NameMoves()) > 0 {
			colliding++
		}
		moves += len(gen.NameMoves())
		for _, m := range gen.NameMoves() {
			for _, name := range []string{m.Got, m.Wanted} {
				if !declared[name] && !isGeneratedHelper(name) {
					t.Errorf("%s: NameMoves reports %+v, and the package declares no %s\nschemas: %s", unit.Name, m, name, strings.Join(raws, "\n"))
				}
			}
		}
		imports = append(imports, fmt.Sprintf("\t%s \"cogen_test/%s\"", pkg, pkg))
	}

	t.Logf("%d packages (%d with a name moved off one something else held), %d documents, %d moves",
		len(units), colliding, documents, moves)
	if 2*colliding < len(units) {
		t.Fatalf("only %d of %d packages had a name collide: the alphabet has stopped colliding, and the sweep with it",
			colliding, len(units))
	}

	mainGo := fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"

%s
)

type docCase struct {
	name, doc, why string
	valid          bool
}

var failed int

func run(schema string, cases []docCase, decode func([]byte) error) {
	for _, c := range cases {
		err := decode([]byte(c.doc))
		if (err == nil) != c.valid {
			failed++
			fmt.Printf("FAIL %%s %%s: %%s accepted=%%v want=%%v err=%%v (%%s)\n", schema, c.name, c.doc, err == nil, c.valid, err, c.why)
		}
	}
}

func main() {
%s
	if failed > 0 {
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`, strings.Join(imports, "\n"), mainBody.String())
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatal(err)
	}
	// The module the cogen sweep writes: the generated packages' own
	// dependencies, and a stub of validationruntime for the hybrid-mode ones.
	if err := writeCogenGoMod(dir, true); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if out, err := testgo.Command(ctx, dir, "vet", "./...").CombinedOutput(); err != nil {
		t.Fatalf("go vet on the generated packages: %v\n%s", err, programOutput(out))
	}
	out, err := testgo.Command(ctx, dir, "run", ".").CombinedOutput()
	if text := programOutput(out); err != nil || text != "PASS" {
		t.Fatalf("a position was not typed by its own schema: %v\n%s", err, text)
	}
}

func collisionCasesLiteral(cases []notInstance) string {
	var b strings.Builder
	b.WriteString("[]docCase{")
	for _, c := range cases {
		fmt.Fprintf(&b, "{name: %s, doc: %s, why: %s, valid: %t}, ", goQuote(c.Name), goQuote(c.Doc), goQuote(c.Why), c.Valid)
	}
	b.WriteString("}")
	return b.String()
}

// packageLevelNames reads every package-level identifier a generated package
// declares.
func packageLevelNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, path := range files {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						names[s.Name.Name] = true
					case *ast.ValueSpec:
						for _, n := range s.Names {
							names[n.Name] = true
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil {
					names[d.Name.Name] = true
				}
			}
		}
	}
	return names
}

// isGeneratedHelper reports whether name is one generated code spells as fixed
// text: a move away from it names a helper, not a declaration of this package's.
func isGeneratedHelper(name string) bool {
	for _, h := range generator.HelperIdentifiers() {
		if h == name {
			return true
		}
	}
	return false
}
