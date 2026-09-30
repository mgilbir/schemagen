package refs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	schemagen "github.com/mgilbir/schemagen/cmd/schemagen"
	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// A reference is resolved against the base URI of the schema resource it is
// written in, and names a node of the resource the resolved URI identifies
// (RFC 3986 §5; JSON Schema 2020-12 §8.2 and §9.1.2). Two defects broke that,
// and both read a *different schema* in silence:
//
//   - The generator consulted an index of the root document's $defs and anchors
//     before the resource the reference was written in, so "#/$defs/Name" in
//     another document, in an embedded $id resource, or "other.json#anchor"
//     (whose document part was dropped) meant the root's Name whenever the root
//     had one. The URN-anchor, $id-relative and $dynamicRef spellings went the
//     same way.
//   - The CLI resolved a relative reference against the first input's
//     directory (--shared-types) or whichever input directory answered first
//     (--schema-package), whatever file the reference was written in.
//
// This file is the matrix both are held to. One fixture holds every case at
// once: a reference of each kind (JSON Pointer, $anchor, $dynamicRef, a
// relative $id, a URN $id with an anchor, an absolute file:// URI, a relative
// path through "..", and a $ref and a $dynamicRef that name their own document
// before the anchor) written in each place a reference can be written (the root
// document, another file beside it, a file in a subdirectory, an embedded
// resource with an absolute $id, one with a relative $id, and one embedded in
// an embedded resource). Every case exists twice: once alone, and once with a
// decoy -- a node of the same name, reachable by the same spelling from the
// root document, holding a different constant. The right target holds
// "<case>-right"; a decoy holds "<case>-wrong", so exactly one instance of each
// pair is valid and a reference that lands anywhere else is a failure naming the
// case.
//
// The fixture is generated in every mode that resolves references -- the
// library, the default CLI mode, --shared-types, --schema-package -- and under
// every validation mode each accepts, then compiled once and run.

const refMatrixSchema = "https://json-schema.org/draft/2020-12/schema"

// refWriters are the places a reference is written, in the order the cases
// are numbered.
var refWriters = []string{"W1", "W2", "W3", "W4", "W5", "W6"}

// refKinds are the spellings of a reference.
var refKinds = []string{"K1", "K2", "K3", "K4", "K5", "K6", "K7", "K8", "K9"}

// refKindNames says what each kind is, for failure messages.
var refKindNames = map[string]string{
	"K1": "JSON Pointer #/$defs/T",
	"K2": "$anchor #a",
	"K3": "$dynamicRef #d",
	"K4": "relative $id i.json",
	"K5": "URN $id with an anchor urn:uuid:...#u",
	"K6": "absolute file:// URI",
	"K7": "relative path through ..",
	"K8": "$dynamicRef with a document part own.json#e",
	"K9": "$ref with a document part own.json#b",
}

// refWriterNames says what each writer is, for failure messages.
var refWriterNames = map[string]string{
	"W1": "the root document",
	"W2": "another file beside it",
	"W3": "a file in a subdirectory",
	"W4": "an embedded resource with an absolute $id",
	"W5": "an embedded resource with a relative $id",
	"W6": "a resource embedded in an embedded resource",
}

type refMatrixCase struct {
	id, writer, kind string
	decoy            bool
}

func (c refMatrixCase) String() string {
	d := "no decoy"
	if c.decoy {
		d = "a decoy in the root"
	}
	return fmt.Sprintf("%s (%s written in %s, %s)", c.id, refKindNames[c.kind], refWriterNames[c.writer], d)
}

type refFixture struct {
	dir     string
	withIDs bool
	cases   []refMatrixCase
}

// buildRefFixture writes the matrix under dir. withIDs gives root.json,
// w2.json and sub/w3.json an https $id mirroring their layout, which is what
// --schema-package needs and which makes every relative reference in them
// resolve against an $id rather than a file.
func buildRefFixture(t *testing.T, dir string, withIDs bool) *refFixture {
	t.Helper()
	obj := func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	rootDefs, w2Defs, w3Defs := obj(), obj(), obj()
	w4Defs, w5Defs, w6Defs := obj(), obj(), obj()
	props := obj()
	root := obj("$schema", refMatrixSchema, "title", "Root", "type", "object", "properties", props, "$defs", rootDefs)
	w2 := obj("$schema", refMatrixSchema, "title", "W2Doc", "$defs", w2Defs)
	w3 := obj("$schema", refMatrixSchema, "title", "W3Doc", "$defs", w3Defs)
	if withIDs {
		root["$id"] = "https://fx.test/m/root.json"
		w2["$id"] = "https://fx.test/m/w2.json"
		w3["$id"] = "https://fx.test/m/sub/w3.json"
	}
	w4 := obj("$id", "https://ex.test/w4/emb.json", "$defs", w4Defs)
	w6 := obj("$id", "nest/inner.json", "$defs", w6Defs)
	w4Defs["W6"] = w6
	rootDefs["W4"] = w4
	rootDefs["W5"] = obj("$id", "w5/inner.json", "$defs", w5Defs)

	defsOf := map[string]map[string]any{"W1": rootDefs, "W2": w2Defs, "W3": w3Defs, "W4": w4Defs, "W5": w5Defs, "W6": w6Defs}
	hopRef := map[string]string{
		"W1": "#/$defs/",
		"W2": "w2.json#/$defs/",
		"W3": "sub/w3.json#/$defs/",
		"W4": "https://ex.test/w4/emb.json#/$defs/",
		"W5": "w5/inner.json#/$defs/",
		"W6": "https://ex.test/w4/nest/inner.json#/$defs/",
	}
	// How each writer names itself: its own URI, relative to its own base, which
	// the document part of a reference that stays in the writer spells.
	selfRef := map[string]string{
		"W1": "root.json", "W2": "w2.json", "W3": "w3.json",
		"W4": "emb.json", "W5": "inner.json", "W6": "inner.json",
	}
	// The directory a relative file reference is read against: the writer's
	// base when that is a file, and otherwise the file its document was read
	// from.
	fileDir := map[string]string{"W1": "", "W2": "", "W3": "sub", "W4": "", "W5": "w5", "W6": ""}
	if withIDs {
		fileDir["W5"] = ""
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]map[string]any{}

	fx := &refFixture{dir: dir, withIDs: withIDs}
	uuid := 0
	for _, w := range refWriters {
		for _, k := range refKinds {
			for _, decoy := range []bool{false, true} {
				// A decoy is a node the root document reaches by the same
				// spelling. Where the reference is written in the root itself
				// it goes in w2.json instead, the other direction of the same
				// question. A relative $id has a decoy only where the two
				// resolve to different URIs -- the same URI twice is a
				// duplicate identifier, which is refused, not a decoy -- and a
				// file reference is decoyed by the separate wrong-directory
				// tests, not here.
				if decoy && (k == "K7" || (k == "K4" && (w == "W1" || w == "W2"))) {
					continue
				}
				id := w + k
				if decoy {
					id += "d"
				}
				c := refMatrixCase{id: id, writer: w, kind: k, decoy: decoy}
				defs := defsOf[w]
				decoyHome := rootDefs
				if w == "W1" {
					decoyHome = w2Defs
				}
				right := func(kv ...any) map[string]any { return obj(append(kv, "const", id+"-right")...) }
				wrong := func(kv ...any) map[string]any { return obj(append(kv, "const", id+"-wrong")...) }
				var hop map[string]any
				switch k {
				case "K1":
					defs["T_"+id] = right()
					hop = obj("$ref", "#/$defs/T_"+id)
					if decoy {
						decoyHome["T_"+id] = wrong()
					}
				case "K2":
					defs["A_"+id] = right("$anchor", "a_"+id)
					hop = obj("$ref", "#a_"+id)
					if decoy {
						decoyHome["DA_"+id] = wrong("$anchor", "a_"+id)
					}
				case "K3":
					defs["D_"+id] = right("$dynamicAnchor", "d_"+id)
					hop = obj("$dynamicRef", "#d_"+id)
					if decoy {
						// A plain $anchor: a $dynamicAnchor in the root would
						// be the *right* answer for a bookended $dynamicRef
						// entered from the root, and this is about the static
						// half.
						decoyHome["DD_"+id] = wrong("$anchor", "d_"+id)
					}
				case "K4":
					defs["I_"+id] = right("$id", "i_"+id+".json")
					hop = obj("$ref", "i_"+id+".json")
					if decoy {
						rootDefs["DI_"+id] = wrong("$id", "i_"+id+".json")
					}
				case "K5":
					uuid++
					urn := fmt.Sprintf("urn:uuid:00000000-0000-4000-8000-%012d", uuid)
					defs["U_"+id] = obj("$id", urn, "$defs", obj("x", right("$anchor", "u_"+id)))
					hop = obj("$ref", urn+"#u_"+id)
					if decoy {
						decoyHome["DU_"+id] = wrong("$anchor", "u_"+id)
					}
				case "K6":
					rel := "files/f6_" + id + ".json"
					files[rel] = obj("$schema", refMatrixSchema, "$defs", obj("T_"+id, right()))
					u := url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(absDir, rel))}
					hop = obj("$ref", u.String()+"#/$defs/T_"+id)
					if decoy {
						decoyHome["T_"+id] = wrong()
					}
				case "K7":
					rel := "rel7/f7_" + id + ".json"
					files[rel] = obj("$schema", refMatrixSchema, "$defs", obj("T_"+id, right()))
					spelled := "sub/../" + rel
					if fileDir[w] != "" {
						spelled = "../" + rel
					}
					hop = obj("$ref", spelled+"#/$defs/T_"+id)
				case "K8":
					// The document part names the writer itself, so the
					// target is the writer's own; a reading that drops the
					// document part and looks "#e" up elsewhere finds the decoy.
					defs["E_"+id] = right("$dynamicAnchor", "e_"+id)
					hop = obj("$dynamicRef", selfRef[w]+"#e_"+id)
					if decoy {
						decoyHome["DE_"+id] = wrong("$anchor", "e_"+id)
					}
				case "K9":
					defs["B_"+id] = right("$anchor", "b_"+id)
					hop = obj("$ref", selfRef[w]+"#b_"+id)
					if decoy {
						decoyHome["DB_"+id] = wrong("$anchor", "b_"+id)
					}
				}
				defs["Hop_"+id] = hop
				props["p_"+id] = obj("$ref", hopRef[w]+"Hop_"+id)
				fx.cases = append(fx.cases, c)
			}
		}
	}
	files["root.json"] = root
	files["w2.json"] = w2
	files["sub/w3.json"] = w3
	// maporder: writes each document to its own file; which is written first changes nothing.
	for rel, doc := range files {
		data, err := json.MarshalIndent(doc, "", "\t")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return fx
}

// TestEveryReferenceKindResolvesInTheResourceItIsWrittenIn asks the resource
// index -- the only thing that resolves a reference, for the generator and for
// every CLI mode -- where each case lands, without compiling anything, so a
// wrong answer is reported per case with the node it reached.
func TestEveryReferenceKindResolvesInTheResourceItIsWrittenIn(t *testing.T) {
	for _, withIDs := range []bool{false, true} {
		t.Run(fmt.Sprintf("ids=%t", withIDs), func(t *testing.T) {
			dir := t.TempDir()
			fx := buildRefFixture(t, dir, withIDs)
			root, err := schema.LoadFromFile(filepath.Join(dir, "root.json"))
			if err != nil {
				t.Fatal(err)
			}
			root.Normalize()
			index := schema.NewResourceIndex(schema.NewFileResolver(dir))
			if err := index.AddDocument(root, nil); err != nil {
				t.Fatal(err)
			}
			for _, c := range fx.cases {
				prop := root.Properties["p_"+c.id]
				hop, err := index.Resolve(prop.Ref, prop)
				if err != nil {
					t.Errorf("%s: the root's reference to the writer did not resolve: %v", c, err)
					continue
				}
				ref := hop.Ref
				if ref == "" {
					ref = hop.DynamicRef
				}
				target, err := index.Resolve(ref, hop)
				if err != nil {
					t.Errorf("%s: %q did not resolve: %v", c, ref, err)
					continue
				}
				want := c.id + "-right"
				if target.Const == nil || *target.Const != want {
					got := "no const"
					if target.Const != nil {
						got = fmt.Sprint(*target.Const)
					}
					t.Errorf("%s: %q reached the node holding %s, want the one holding %s", c, ref, got, want)
				}
			}
		})
	}
}

// refMatrixRun is one generation of the fixture: a mode, a validation mode, and
// the package it writes.
type refMatrixRun struct {
	pkg     string
	mode    string // lib, default, shared, package
	withIDs bool
	valid   generator.ValidationMode
}

func TestEveryReferenceKindResolvesInEveryModeOfGeneration(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs generated packages")
	}
	var runs []refMatrixRun
	for _, withIDs := range []bool{false, true} {
		v := "a"
		if withIDs {
			v = "b"
		}
		for _, vm := range []generator.ValidationMode{generator.ValidationModeStatic, generator.ValidationModeHybrid, generator.ValidationModeRuntime} {
			runs = append(runs,
				refMatrixRun{pkg: v + "lib" + string(vm), mode: "lib", withIDs: withIDs, valid: vm},
				refMatrixRun{pkg: v + "default" + string(vm), mode: "default", withIDs: withIDs, valid: vm})
		}
		// One package from several documents is static-only: the CLI refuses
		// --shared-types and --schema-package under hybrid and runtime.
		runs = append(runs, refMatrixRun{pkg: v + "shared", mode: "shared", withIDs: withIDs, valid: generator.ValidationModeStatic})
		if withIDs {
			// --schema-package requires every input to declare $id.
			runs = append(runs, refMatrixRun{pkg: v + "package", mode: "package", withIDs: withIDs, valid: generator.ValidationModeStatic})
		}
	}

	mod := t.TempDir()
	if err := writeCogenGoMod(mod); err != nil {
		t.Fatal(err)
	}
	fixtures := map[bool]*refFixture{}
	for _, withIDs := range []bool{false, true} {
		fixtures[withIDs] = buildRefFixture(t, t.TempDir(), withIDs)
	}
	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		generateRefMatrixRun(t, em, fixtures[run.withIDs], run, filepath.Join(mod, run.pkg))
	}

	var body strings.Builder
	var imports strings.Builder
	for _, run := range runs {
		fmt.Fprintf(&imports, "\t%s \"cogen_test/%s\"\n", run.pkg, run.pkg)
		for _, c := range fixtures[run.withIDs].cases {
			for _, verdict := range []struct {
				value string
				valid bool
			}{{c.id + "-right", true}, {c.id + "-wrong", false}} {
				doc := fmt.Sprintf(`{"p_%s":%q}`, c.id, verdict.value)
				fmt.Fprintf(&body, "\t{\n\t\tvar v %s.Root\n\t\tcheck(%q, %q, %t, json.Unmarshal([]byte(%q), &v), &v)\n\t}\n",
					run.pkg, run.pkg+" "+c.String(), doc, verdict.valid, doc)
			}
		}
	}
	driver := fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"

%s)

var failed int

func check(label, doc string, valid bool, decodeErr error, v any) {
	err := decodeErr
	if x, ok := v.(interface{ Validate() error }); ok && err == nil {
		err = x.Validate()
	}
	if got := err == nil; got != valid {
		failed++
		fmt.Printf("FAIL %%s: %%s: want valid=%%t, got valid=%%t (err=%%v)\n", label, doc, valid, got, err)
	}
}

func main() {
%s
	if failed > 0 {
		fmt.Printf("%%d failures\n", failed)
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`, imports.String(), body.String())
	if err := os.WriteFile(filepath.Join(mod, "main.go"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := goCmd(t, mod, nil, "run", ".")
	if err != nil && !bytes.Contains(out, []byte("FAIL ")) {
		t.Fatalf("the driver did not run: %v\n%s", err, out)
	}
	// Every case in every mode, the library included. The library used to type
	// the target of "#/$defs/T_<case>" written in another resource as the
	// root's own decoy "$defs/T_<case>" -- resolution reached the right node,
	// and the name it derived, T<case>, was already the decoy's -- and those ten
	// cases (W2-W6 by a pointer and by an absolute file:// URI) were held here
	// in a ledger. The name registry gives the target a name of its own, so
	// there is no exception left to list.
	var failures []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "FAIL ") {
			failures = append(failures, line)
		}
	}
	if len(failures) > 0 {
		t.Errorf("a reference reached a schema other than the one it names:\n%s", strings.Join(failures, "\n"))
	}
}

// generateRefMatrixRun generates the fixture into pkgDir in one mode.
func generateRefMatrixRun(t *testing.T, em *emitter.Emitter, fx *refFixture, run refMatrixRun, pkgDir string) {
	t.Helper()
	rootPath := filepath.Join(fx.dir, "root.json")
	inputs := []string{filepath.Join(fx.dir, "w2.json"), filepath.Join(fx.dir, "sub", "w3.json"), rootPath}
	switch run.mode {
	case "lib":
		// The library as a caller uses it: a document loaded from its file,
		// and a file resolver rooted at the schema directory.
		s, err := schema.LoadFromFile(rootPath)
		if err != nil {
			t.Fatal(err)
		}
		s.Normalize()
		ir, err := generator.New(generator.Config{
			PackageName: run.pkg,
			Resolver:    schema.NewFileResolver(fx.dir),
			Validation:  run.valid,
		}).Generate(s)
		if err != nil {
			t.Fatalf("%s: generate: %v", run.pkg, err)
		}
		src, err := em.Emit(ir)
		if err != nil {
			t.Fatalf("%s: emit: %v", run.pkg, err)
		}
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "root.go"), src, 0o644); err != nil {
			t.Fatal(err)
		}
		writeSharedHelpers(t, pkgDir, string(src))
		return
	case "default":
		runRefMatrixCLI(t, run.pkg, rootPath, "-o", pkgDir, "-p", run.pkg, "--validation", string(run.valid))
	case "shared":
		// The documents another input refers to come first; see
		// explainRootTypeCollision.
		args := append(append([]string{}, inputs...), "--shared-types", "-o", pkgDir, "-p", run.pkg)
		runRefMatrixCLI(t, run.pkg, args...)
	case "package":
		args := append([]string{}, inputs...)
		for _, id := range []string{"https://fx.test/m/w2.json", "https://fx.test/m/sub/w3.json", "https://fx.test/m/root.json"} {
			args = append(args, "--schema-package", id+"=cogen_test/"+run.pkg)
		}
		args = append(args, "-o", filepath.Dir(pkgDir))
		runRefMatrixCLI(t, run.pkg, args...)
	}
	// A package whose generation was refused has no Root, and the driver would
	// fail to compile with a message about that rather than about the refusal.
	if entries, err := os.ReadDir(pkgDir); err != nil || len(entries) == 0 {
		t.Fatalf("%s: nothing was generated into %s (%v)", run.pkg, pkgDir, err)
	}
}

func runRefMatrixCLI(t *testing.T, label string, args ...string) {
	t.Helper()
	cmd := schemagen.NewRootCmd()
	var stderr bytes.Buffer
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"generate"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("%s: generate %s: %v\n%s", label, strings.Join(args, " "), err, stderr.String())
	}
}

// refMatrixCaseIDs lists the fixture's case ids, sorted -- used by the
// coverage check below.
func refMatrixCaseIDs(fx *refFixture) []string {
	ids := make([]string, 0, len(fx.cases))
	for _, c := range fx.cases {
		ids = append(ids, c.id)
	}
	sort.Strings(ids)
	return ids
}

// TestRefMatrixCoversEveryCell holds the fixture to the matrix it claims: every
// writer with every kind, alone, and with a decoy wherever a decoy can exist.
// A cell dropped from the generator loop above would otherwise be a gap
// nothing reports.
func TestRefMatrixCoversEveryCell(t *testing.T) {
	fx := buildRefFixture(t, t.TempDir(), false)
	got := refMatrixCaseIDs(fx)
	var want []string
	for _, w := range refWriters {
		for _, k := range refKinds {
			want = append(want, w+k)
			if k != "K7" && !(k == "K4" && (w == "W1" || w == "W2")) {
				want = append(want, w+k+"d")
			}
		}
	}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("fixture cases = %v\nwant %v", got, want)
	}
	if len(want) != 6*9+6*8-2 {
		t.Errorf("the matrix has %d cells; 54 alone plus 46 decoyed was the design", len(want))
	}
}
