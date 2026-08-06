package generator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// The gates these tests hold are the ones that decide whether a sub-schema
// keyword's static reduction has read the whole sub-schema, or whether the
// keyword has to be compiled to the runtime evaluator instead.
//
// They are read from the *extractors' own source*, in the way
// TestEveryElementRuleTypeIsClassified reads extractValidationRules, and for the
// same reason. A gate written as a hand-kept list of keywords is a list somebody
// has to remember to update, and the failure mode of forgetting is silent: the
// gate says the reduction read the whole sub-schema, the reduction did not, and
// the keyword goes on asserting less than it says. That is how issues #180 and
// #181 came about -- extractPropertyNamesDef reads five keywords and
// extractDependentSchemaConstraints reads five, and neither had anything asking
// what else the sub-schema stated, so the `$ref` everybody writes was read as a
// sub-schema stating nothing.
//
// Reading the source instead means the gate cannot silently fall behind the
// extractor: teaching the extractor a keyword fails these tests until the gate
// has an answer for it, and taking one away fails them until the gate stops
// claiming it.

// TestPropertyNamesGateNamesEveryKeywordExtractorReads holds
// propertyNamesKeywordsRead against extractPropertyNamesDef.
func TestPropertyNamesGateNamesEveryKeywordExtractorReads(t *testing.T) {
	read := keywordsReadBy(t, "extractPropertyNamesDef", "pn", 3)
	assertSameKeywords(t, "propertyNamesKeywordsRead", propertyNamesKeywordsRead, "extractPropertyNamesDef", read,
		"A keyword extractPropertyNamesDef reads and the gate does not name would send the sub-schema "+
			"to the evaluator for no reason; one the gate names and the extractor does not read is a "+
			"sub-schema declared fully read that nothing reads, which is issue #180 again.")
}

// TestDependentBranchGateNamesEveryKeywordExtractorReads holds
// dependentBranchKeywordsRead against extractDependentSchemaConstraints.
//
// The receiver name is the branch variable, not the schema the keyword sits on:
// this gate speaks for what one dependentSchemas branch states, and `s` in that
// function is the object carrying the keyword.
func TestDependentBranchGateNamesEveryKeywordExtractorReads(t *testing.T) {
	read := keywordsReadBy(t, "extractDependentSchemaConstraints", "depSchema", 3)
	assertSameKeywords(t, "dependentBranchKeywordsRead", dependentBranchKeywordsRead, "extractDependentSchemaConstraints", read,
		"A branch keyword the extractor reads and the gate does not name would send every branch "+
			"stating it to the evaluator; one the gate names and the extractor does not read is a "+
			"branch declared fully read that nothing reads, which is issue #181 again.")
}

// TestLenientPropertyGateNamesEveryKeywordTheChecksCarry holds
// lenientPropertyCheckKeywords against the two functions that build the checks a
// dependentSchemas branch's own properties compile to.
//
// This is the finer half of the branch gate. objectConditionalBranchLenient reads
// `properties` and hands each property to objectPropertyChecksLenient, which
// drops the keywords it cannot turn into a check -- so "the extractor read
// `properties`" is only true property by property, and this list is what decides
// it. A keyword modelledChecks stops building and this list goes on naming is a
// property read as fully checked when it is not.
func TestLenientPropertyGateNamesEveryKeywordTheChecksCarry(t *testing.T) {
	built := keywordsReadBy(t, "modelledChecks", "s", 6)
	for k := range keywordsReadBy(t, "objectPropertyChecksLenient", "s", 1) {
		built[k] = true
	}
	assertSameKeywords(t, "lenientPropertyCheckKeywords", lenientPropertyCheckKeywords,
		"modelledChecks and objectPropertyChecksLenient", built,
		"A property keyword the lenient checks carry and this list omits sends its branch to the "+
			"evaluator needlessly; one this list names and the checks no longer carry leaves the "+
			"property read in part while the branch is declared fully read.")
}

// TestConditionalBranchGateNamesEveryKeywordTheReadingKeeps holds
// conditionalBranchKeywordsRead against objectConditionalBranchLenient, the
// function that reduces a `then` or an `else`.
//
// The branch level only: what the reading keeps of each property it names is
// lenientPropertyCheckKeywords' business, and the test above holds that. The two
// together are what objectConditionalReadWhole means by "read whole".
func TestConditionalBranchGateNamesEveryKeywordTheReadingKeeps(t *testing.T) {
	read := keywordsReadBy(t, "objectConditionalBranchLenient", "s", 2)
	assertSameKeywords(t, "conditionalBranchKeywordsRead", conditionalBranchKeywordsRead,
		"objectConditionalBranchLenient", read,
		"A branch keyword the reduction keeps and the gate does not name sends every `then` stating "+
			"it to the evaluator needlessly; one the gate names and the reduction has stopped keeping "+
			"is a consequence declared fully read that nothing reads, which is issue #209 again.")
}

func assertSameKeywords(t *testing.T, gateName string, gate map[string]bool, sourceName string, read map[string]bool, why string) {
	t.Helper()
	for _, key := range sortedBoolKeys(read) {
		if !gate[key] {
			t.Errorf("%s reads %q and %s does not name it.\n%s", sourceName, key, gateName, why)
		}
	}
	for _, key := range sortedBoolKeys(gate) {
		if !read[key] {
			t.Errorf("%s names %q and %s does not read it.\n%s", gateName, key, sourceName, why)
		}
	}
}

func sortedBoolKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// keywordsReadBy parses generator.go and dynamic.go and returns the JSON Schema
// keywords the named function reads off the named variable.
//
// A read is a selector on that identifier whose field is a field of
// schema.Schema; the keyword is that field's own JSON tag, so the mapping comes
// from the parser's struct rather than from a list here. A field tagged "-" is
// skipped: it has no keyword of its own, and the two that stand for one --
// ConstIsNull for `const` and TypeSchemas for `type` -- are only ever read beside
// the field that does carry it.
//
// floor is the number of reads below which the scan is assumed to have stopped
// working rather than to have found the truth. Renaming the variable would
// otherwise empty the result and turn both directions of the comparison into
// silence.
func keywordsReadBy(t *testing.T, funcName, recv string, floor int) map[string]bool {
	t.Helper()
	fn := findFuncDecl(t, funcName)
	byField := schemaFieldKeywords()
	found := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != recv {
			return true
		}
		if keyword, isField := byField[sel.Sel.Name]; isField && keyword != "" {
			found[keyword] = true
		}
		return true
	})
	if len(found) < floor {
		t.Fatalf("only %d schema keyword reads of %q found in %s (%v); the source scan has stopped "+
			"seeing what it reads, so this gate would pass whatever the extractor dropped",
			len(found), recv, funcName, sortedBoolKeys(found))
	}
	return found
}

// findFuncDecl locates a top-level function by name across the package's own
// sources, so a function moving between files does not silently empty a gate.
func findFuncDecl(t *testing.T, funcName string) *ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	for _, file := range []string{"generator.go", "dynamic.go", "annotations.go"} {
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", file, err)
		}
		for _, decl := range parsed.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == funcName && fd.Body != nil {
				return fd
			}
		}
	}
	t.Fatalf("no func %s in the package sources this gate reads; it can no longer be held to its own source", funcName)
	return nil
}

// schemaFieldKeywords maps each field of schema.Schema to the keyword it
// carries, read off the struct tags.
func schemaFieldKeywords() map[string]string {
	typ := reflect.TypeOf(schema.Schema{})
	out := make(map[string]string, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			out[f.Name] = ""
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		out[f.Name] = name
	}
	return out
}

// TestSubschemaKeywordTakenByTheEvaluatorIsNotAlsoReadStatically pins the other
// half of the routing: where the evaluator takes a keyword over, the partial
// static reading of that same keyword is dropped rather than run beside it.
//
// For `dependentSchemas` that is a correctness requirement, and the fixture
// dependentschemas_subschema_shapes.json shows why: the static allowed-key list
// is built from the branch's `properties` alone, so a branch that also names keys
// by pattern refused documents it permits. Running it beside the exact check
// would keep that rejection.
//
// For `propertyNames` it is not -- every field PropertyNamesDef carries is a
// conjunct of the sub-schema, so running it beside the exact check could only
// repeat a verdict, never contradict one. It is dropped anyway, so that one
// keyword has one check and the partial reading has no second life to drift in;
// and this test is what that decision is falsifiable by, since no document can
// tell the two apart.
func TestSubschemaKeywordTakenByTheEvaluatorIsNotAlsoReadStatically(t *testing.T) {
	// The sub-schema states a keyword the static reading does read, beside one it
	// does not. A bare $ref would prove nothing here: the static reading answers
	// nil for it whether it is suppressed or not, so only a sub-schema that would
	// have produced a PropertyNamesDef can tell the two apart.
	ir := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"propertyNames": {"minLength": 2, "$ref": "#/$defs/short"},
		"$defs": {"short": {"maxLength": 3}}
	}`)
	doc := structNamed(t, ir, "Doc")
	if doc.PropertyNames != nil {
		t.Errorf("PropertyNames = %+v: the evaluator took the keyword and the partial reading was kept too", doc.PropertyNames)
	}
	if !hasRuntimeKeyword(doc, "propertyNames") {
		t.Errorf("no propertyNames runtime check on %s; the keyword is enforced by nothing: %+v", doc.Name, doc.RuntimeBranchChecks)
	}

	// The same, one property along. An object that declares a property is built
	// by a different function from one that declares none, and each reads the
	// keyword for itself, so a suppression written in one of them says nothing
	// about the other.
	withProps := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"properties": {"alpha": {"type": "string"}},
		"propertyNames": {"minLength": 2, "$ref": "#/$defs/short"},
		"$defs": {"short": {"maxLength": 3}}
	}`)
	propsDoc := structNamed(t, withProps, "Doc")
	if propsDoc.PropertyNames != nil {
		t.Errorf("PropertyNames = %+v on the struct-with-properties path: the partial reading was kept beside the exact check", propsDoc.PropertyNames)
	}
	if !hasRuntimeKeyword(propsDoc, "propertyNames") {
		t.Errorf("no propertyNames runtime check on the struct-with-properties path: %+v", propsDoc.RuntimeBranchChecks)
	}

	// A sub-schema the static reading covers keeps it, and gets no runtime check.
	// Without this the assertion above could be satisfied by never producing a
	// PropertyNamesDef at all.
	plain := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"propertyNames": {"maxLength": 3}
	}`)
	plainDoc := structNamed(t, plain, "Doc")
	if plainDoc.PropertyNames == nil || plainDoc.PropertyNames.MaxLength == nil || *plainDoc.PropertyNames.MaxLength != 3 {
		t.Errorf("PropertyNames = %+v, want the static reading kept for a sub-schema it reads whole", plainDoc.PropertyNames)
	}
	if hasRuntimeKeyword(plainDoc, "propertyNames") {
		t.Errorf("runtime check emitted for a sub-schema the static reading covers: %+v", plainDoc.RuntimeBranchChecks)
	}

	// dependentSchemas, one trigger routed and one not. The two are independent,
	// so the branch the evaluator declined must keep the reading it has today.
	//
	// The routed branch states a `required` the static reading would keep beside
	// a `not` it cannot read, for the reason the propertyNames case above uses a
	// sibling: a branch the static reading answers nothing for cannot show whether
	// the suppression happened.
	mixed := generateForItemTest(t, `{
		"title": "Doc",
		"type": "object",
		"dependentSchemas": {
			"routed": {"required": ["cvv"], "not": {"required": ["banned"]}},
			"kept": {"required": ["plain"]}
		}
	}`)
	mixedDoc := structNamed(t, mixed, "Doc")
	if dep := dependentSchemaIfAny(mixedDoc, "routed"); dep != nil {
		t.Errorf("the routed trigger kept a static constraint as well: %+v", dep)
	}
	kept := dependentSchemaIfAny(mixedDoc, "kept")
	if kept == nil || !containsString(kept.RequiredProps, "plain") {
		t.Errorf("the trigger the evaluator did not take lost its static reading: %+v", mixedDoc.DependentSchemas)
	}
	lit := dependentSchemasNodeLiteral(t, mixedDoc)
	if !strings.Contains(lit, `{Key: "routed"`) {
		t.Errorf("compiled literal does not name the routed trigger:\n%s", lit)
	}
	if strings.Contains(lit, `{Key: "kept"`) {
		t.Errorf("compiled literal names a trigger the static reading already covers, so it is checked twice:\n%s", lit)
	}
}

func hasRuntimeKeyword(sd *StructDef, keyword string) bool {
	for _, c := range sd.RuntimeBranchChecks {
		if c.Keyword == keyword {
			return true
		}
	}
	return false
}

// TestSubschemaKeywordsAreNotCompiledWithoutTheValidationVocabulary holds the
// switch both readings sit behind.
//
// A metaschema that leaves the validation vocabulary out is saying these
// keywords do not bind at all, so neither the static reduction nor the compiled
// evaluator may run: a check emitted here would enforce a schema the document
// says it is not written against.
func TestSubschemaKeywordsAreNotCompiledWithoutTheValidationVocabulary(t *testing.T) {
	const novalidation = `"$vocabulary":{"https://json-schema.org/draft/2020-12/vocab/core":true,` +
		`"https://json-schema.org/draft/2020-12/vocab/applicator":true}`
	ir := generateForItemTest(t, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		`+novalidation+`,
		"title": "Doc",
		"type": "object",
		"properties": {"alpha": {"type": "string"}},
		"propertyNames": {"$ref": "#/$defs/short"},
		"dependentSchemas": {"card": {"$ref": "#/$defs/needsCvv"}},
		"$defs": {"short": {"maxLength": 3}, "needsCvv": {"required": ["cvv"]}}
	}`)
	doc := structNamed(t, ir, "Doc")
	if len(doc.RuntimeBranchChecks) != 0 {
		t.Errorf("RuntimeBranchChecks = %+v: the keywords were compiled although the metaschema declares no validation vocabulary",
			doc.RuntimeBranchChecks)
	}
	if doc.PropertyNames != nil || len(doc.DependentSchemas) != 0 {
		t.Errorf("PropertyNames = %+v, DependentSchemas = %+v, want neither read", doc.PropertyNames, doc.DependentSchemas)
	}

	// The same document with the vocabulary declaration removed, so the assertion
	// above cannot be satisfied by the keywords reaching nothing for some other
	// reason.
	bound := generateForItemTest(t, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": "Doc",
		"type": "object",
		"properties": {"alpha": {"type": "string"}},
		"propertyNames": {"$ref": "#/$defs/short"},
		"dependentSchemas": {"card": {"$ref": "#/$defs/needsCvv"}},
		"$defs": {"short": {"maxLength": 3}, "needsCvv": {"required": ["cvv"]}}
	}`)
	boundDoc := structNamed(t, bound, "Doc")
	if !hasRuntimeKeyword(boundDoc, "propertyNames") || !hasRuntimeKeyword(boundDoc, "dependentSchemas") {
		t.Errorf("control: both keywords should be compiled when the validation vocabulary binds: %+v", boundDoc.RuntimeBranchChecks)
	}
}

// TestPropertyNamesFormatIsReadOnlyWhereSomethingCanJudgeIt holds the two
// conditions on the `format` read.
//
// The dialect decides whether the keyword asserts, and FormatCheckableOnString
// whether this generator has a check for the format named. Neither answer can be
// seen from a document -- a format nothing judges and a format that passes look
// alike from outside -- so it is pinned on the IR: a def built for a format the
// emitter renders nothing for is an empty loop over every key of every instance,
// and a _jsonKeys map kept to feed it.
func TestPropertyNamesFormatIsReadOnlyWhereSomethingCanJudgeIt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema string
		want   string
	}{
		{"asserted and checkable", `{"$schema":"http://json-schema.org/draft-07/schema#","title":"Doc",` +
			`"type":"object","propertyNames":{"format":"email"}}`, "email"},
		{"asserted, nothing judges it", `{"$schema":"http://json-schema.org/draft-07/schema#","title":"Doc",` +
			`"type":"object","propertyNames":{"format":"not-a-known-format"}}`, ""},
		{"annotation vocabulary", `{"$schema":"https://json-schema.org/draft/2020-12/schema","title":"Doc",` +
			`"type":"object","propertyNames":{"format":"email"}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ir := generateForItemTest(t, tc.schema)
			doc := structNamed(t, ir, "Doc")
			got := ""
			if doc.PropertyNames != nil {
				got = doc.PropertyNames.Format
			}
			if got != tc.want {
				t.Errorf("PropertyNames.Format = %q, want %q (%+v)", got, tc.want, doc.PropertyNames)
			}
			if tc.want == "" && doc.PropertyNames != nil {
				t.Errorf("PropertyNames = %+v: a def was built for a keyword that renders no check", doc.PropertyNames)
			}
		})
	}
}

// TestContainsGateNamesEveryKeywordTheChecksRead holds containsCheckKeywords
// against extractContainsDef, the way the two gates above are held against their
// own extractors.
//
// This gate used to be a deny-list of struct fields, and `patternProperties` was
// the field nobody wrote down: it is not `properties`, so hasProperties did not
// see it, and a `contains` naming an object shape by pattern counted every
// object (issue #207). A deny-list has to be remembered keyword by keyword and
// fails silently when it is not; an allow-list read from the sub-schema's own
// keyword set fails closed, and this test is what keeps the list the extractor's
// vocabulary rather than a second thing to remember.
//
// `const` and `enum` are named here and not in containsCheckKeywords on purpose:
// extractContainsDef answers both in arms that return before the gate is
// consulted, so the extractor reads them while the gate must not admit them.
func TestContainsGateNamesEveryKeywordTheChecksRead(t *testing.T) {
	read := keywordsReadBy(t, "extractContainsDef", "containsSch", 8)

	gate := map[string]bool{"const": true, "enum": true}
	for key := range containsCheckKeywords {
		gate[key] = true
	}
	assertSameKeywords(t, "containsCheckKeywords plus the const and enum arms", gate,
		"extractContainsDef", read,
		"A keyword extractContainsDef reads and the gate does not name sends every `contains` stating "+
			"it to a materialized type for no reason; one the gate names and the extractor does not "+
			"read is a sub-schema declared fully checked that nothing checks, which is issue #207 again.")
}
