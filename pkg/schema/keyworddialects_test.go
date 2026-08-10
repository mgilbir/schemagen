package schema

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

// TestEveryParsedKeywordHasADialectRow is the anti-drift gate on the table.
//
// The truth-set is the struct: every keyword this package can read is a field of
// Schema carrying that keyword's JSON tag, and every keyword the generator reads
// it reads off one of those fields. So a keyword with no row is a keyword no
// dialect decision is made about -- which is issue #203 itself, twenty-nine
// spellings enforced or honoured in dialects that do not define them, because
// the rule was written per keyword at six sites instead of once for all of them.
//
// Adding a field to Schema without adding a row fails here, and the failure names
// the keyword. That is the property the table is for: it cannot silently fall
// behind what the parser accepts.
func TestEveryParsedKeywordHasADialectRow(t *testing.T) {
	typ := reflect.TypeOf(Schema{})
	seen := 0
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		keyword := fieldKeyword(f)
		if keyword == "" {
			continue
		}
		seen++
		if _, ok := keywordDialects[keyword]; !ok {
			t.Errorf("Schema.%s carries the keyword %q and keywordDialects has no row for it.\n"+
				"Every keyword needs a span, because every draft ignores a keyword it does not define -- in "+
				"both directions. Add a row saying which drafts define it, or classify the field in "+
				"nonKeywordFields with the reason it carries no keyword.", f.Name, keyword)
		}
	}
	// A floor, for the reason keywordsReadBy has one: renaming the tag or
	// breaking fieldKeyword would empty the loop and make this pass on nothing.
	if seen < 50 {
		t.Fatalf("only %d keyword-carrying fields found on Schema; the scan has stopped seeing the "+
			"struct, so this gate would pass whatever the table omitted", seen)
	}
}

// TestSchemaFieldsAreClassifiedForDialect is the other direction: a field that
// carries no keyword has to say so, rather than being missed by the tag reader.
//
// Without it, a field whose json tag someone sets to "-" leaves the dialect pass
// silently, and nothing says which of the two reasons that happened for.
func TestSchemaFieldsAreClassifiedForDialect(t *testing.T) {
	typ := reflect.TypeOf(Schema{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if fieldKeyword(f) != "" {
			// A keyword the pass cannot write to is a keyword it silently fails
			// to clear. reflect refuses to set an unexported field, so this is
			// the difference between a named failure here and a panic on the
			// first document that states the keyword.
			if f.PkgPath != "" {
				t.Errorf("Schema.%s carries the keyword %q and is unexported, so the dialect pass cannot "+
					"clear it", f.Name, fieldKeyword(f))
			}
			continue
		}
		if f.PkgPath != "" {
			continue // unexported and keywordless: memoization
		}
		if nonKeywordFields[f.Name] {
			continue
		}
		t.Errorf("Schema.%s is invisible to the dialect pass and is not classified.\n"+
			"Either give it a json tag naming its keyword, map it in hiddenFieldKeywords to the keyword "+
			"it stands for (as ConstIsNull stands for `const`), or name it in nonKeywordFields.", f.Name)
	}
	for name := range hiddenFieldKeywords {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("hiddenFieldKeywords names %s, which Schema no longer has", name)
		}
	}
	for name := range nonKeywordFields {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("nonKeywordFields names %s, which Schema no longer has", name)
		}
	}
}

// TestEveryFormedKeywordHasAShapeReader holds statedForm against the table.
//
// A row split by value shape is answered by asking the schema which shape it
// states. A keyword with Forms and no arm in statedForm answers formAny, which
// matches no arm of its own row, and definedIn then reports it undefined in
// every dialect -- so the keyword would be dropped everywhere, silently and in
// both directions at once.
func TestEveryFormedKeywordHasAShapeReader(t *testing.T) {
	// One schema stating every formed keyword in every shape its row names, so
	// that "statedForm answers something other than formAny" is checked against a
	// value rather than asserted.
	for keyword, kd := range keywordDialects {
		if len(kd.Forms) == 0 {
			continue
		}
		s := schemaStating(t, keyword)
		if got := s.statedForm(keyword); got == formAny {
			t.Errorf("keywordDialects[%q] is split by value shape and statedForm answers formAny for a "+
				"schema that states it. Every shape in the row would then match no arm and the keyword "+
				"would be dropped on every dialect. Add an arm to statedForm.", keyword)
		}
	}
}

// schemaStating parses a schema that states the keyword in the first shape its
// row names, so the shape reader is exercised on a real value.
func schemaStating(t *testing.T, keyword string) *Schema {
	t.Helper()
	bodies := map[string]string{
		"exclusiveMinimum": `{"exclusiveMinimum": 5}`,
		"exclusiveMaximum": `{"exclusiveMaximum": 5}`,
		"required":         `{"required": ["a"]}`,
	}
	body, ok := bodies[keyword]
	if !ok {
		t.Fatalf("keywordDialects[%q] is split by value shape and this test has no document stating it; "+
			"add one, or the shape reader for it is never exercised", keyword)
	}
	var s Schema
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	return &s
}

// TestEveryKeptKeywordSaysWhy holds the one judgement the table contains.
//
// gateKeep is the arm where the span is recorded and deliberately not acted on,
// and there are only two admissible reasons -- the keyword establishes identity
// or resolution scope, or it is the target of a reference. Both are cases where
// clearing the keyword does not make the generator ignore it but makes a $ref
// stop resolving. A row taking that arm without saying which is an exemption
// nobody can review.
func TestEveryKeptKeywordSaysWhy(t *testing.T) {
	for _, keyword := range sortedKeywords() {
		kd := keywordDialects[keyword]
		if kd.Gate == gateKeep && kd.Why == "" {
			t.Errorf("keywordDialects[%q] is gateKeep with no Why", keyword)
		}
		if kd.Gate == gateDrop && kd.Why != "" {
			t.Errorf("keywordDialects[%q] is gateDrop and carries a Why (%q); the field is the reason a "+
				"row is exempt, so a reason on a row that is not exempt reads as one that is", keyword, kd.Why)
		}
	}
}

// TestEveryRowNamesADraftSpan catches a row left at its zero value, which would
// read as DraftUnknown..DraftUnknown -- a span no dialect falls in, so the
// keyword would be dropped everywhere except where the dialect is unknown.
func TestEveryRowNamesADraftSpan(t *testing.T) {
	for _, keyword := range sortedKeywords() {
		kd := keywordDialects[keyword]
		spans := [][2]Draft{{kd.From, kd.To}}
		if len(kd.Forms) > 0 {
			spans = spans[:0]
			for _, f := range kd.Forms {
				spans = append(spans, [2]Draft{f.From, f.To})
			}
		}
		for _, span := range spans {
			if span[0] == DraftUnknown || span[1] == DraftUnknown {
				t.Errorf("keywordDialects[%q] has a span bounded by DraftUnknown (%v..%v); no dialect "+
					"falls in it and the keyword would be dropped in all of them", keyword, span[0], span[1])
			}
			if span[0] > span[1] {
				t.Errorf("keywordDialects[%q] spans %v..%v, which is empty", keyword, span[0], span[1])
			}
		}
	}
}

// TestDraftConstantsAreOrdered pins the assumption every span comparison makes.
//
// definedIn compares Draft values with < and >, which is only a reading of "this
// dialect is older than that one" while the constants are declared oldest-first.
// Inserting a draft in the wrong place, or giving one an explicit value, would
// leave every span quietly wrong rather than failing to compile.
func TestDraftConstantsAreOrdered(t *testing.T) {
	ordered := []Draft{Draft03, Draft04, Draft06, Draft07, Draft201909, Draft202012, DraftV1}
	for i := 1; i < len(ordered); i++ {
		if !(ordered[i-1] < ordered[i]) {
			t.Fatalf("%v is not ordered before %v; keyword spans are read as ranges over these constants",
				ordered[i-1], ordered[i])
		}
	}
	if !(DraftUnknown < ordered[0]) {
		t.Fatalf("DraftUnknown must sort before every named draft")
	}
}

func sortedKeywords() []string {
	out := make([]string, 0, len(keywordDialects))
	for k := range keywordDialects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestExclusiveBoundSpellingFollowsTheDialect is issue #203's sharpest case, at
// the level the decision is taken.
//
// Drafts 3 and 4 define exclusiveMinimum as a boolean that modifies the sibling
// minimum; draft 6 redefined it as the bound itself. Each dialect knows one
// spelling and has to ignore the other, and the document the issue reports --
// {"minimum":3,"exclusiveMinimum":5} declared as draft 4 -- is the case where
// ignoring it changes the verdict: only minimum binds, so 4 is valid.
func TestExclusiveBoundSpellingFollowsTheDialect(t *testing.T) {
	const d4 = `"http://json-schema.org/draft-04/schema#"`
	const d6 = `"http://json-schema.org/draft-06/schema#"`

	for _, tt := range []struct {
		name      string
		doc       string
		wantBound bool // the exclusive keyword survives normalization
		wantMin   bool // the sibling minimum survives with it
	}{
		{"draft 4 keeps its boolean", `{"$schema":` + d4 + `,"minimum":5,"exclusiveMinimum":true}`, true, true},
		{"draft 4 drops a number", `{"$schema":` + d4 + `,"minimum":3,"exclusiveMinimum":5}`, false, true},
		{"draft 6 keeps its number", `{"$schema":` + d6 + `,"minimum":3,"exclusiveMinimum":5}`, true, true},
		{"draft 6 drops a boolean", `{"$schema":` + d6 + `,"minimum":5,"exclusiveMinimum":true}`, false, true},
		{"2020-12 keeps its number", `{"$schema":"https://json-schema.org/draft/2020-12/schema","minimum":3,"exclusiveMinimum":5}`, true, true},
		{"draft 3 keeps its boolean", `{"$schema":"http://json-schema.org/draft-03/schema#","minimum":5,"exclusiveMinimum":true}`, true, true},
		{"no dialect keeps whatever is written", `{"minimum":5,"exclusiveMinimum":true}`, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var s Schema
			if err := json.Unmarshal([]byte(tt.doc), &s); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			s.Normalize()
			if got := s.ExclusiveMinimum != nil; got != tt.wantBound {
				t.Errorf("exclusiveMinimum survived = %v, want %v: %s", got, tt.wantBound, tt.doc)
			}
			// The sibling is the half that made this a false reject *and* a
			// silent discard: dropping the unknown spelling must not take the
			// keyword beside it with it.
			if got := s.Minimum != nil; got != tt.wantMin {
				t.Errorf("minimum survived = %v, want %v: %s", got, tt.wantMin, tt.doc)
			}
		})
	}
}

// TestDialectGateRunsBeforeTheLegacyRewrites is the ordering the two directions
// depend on.
//
// Five rewrites read a keyword one dialect alone defines and write one that
// dialect does not have. Gating after them would delete what they had just
// legitimately produced; gating before makes each fire exactly where its source
// keyword belongs. Both halves are asserted here, because a gate that ran in the
// wrong place would still pass the half that only needs it to run at all.
func TestDialectGateRunsBeforeTheLegacyRewrites(t *testing.T) {
	const d3 = `"http://json-schema.org/draft-03/schema#"`
	const d6 = `"http://json-schema.org/draft-06/schema#"`

	t.Run("draft 3 keeps what its own spellings produce", func(t *testing.T) {
		var s Schema
		doc := `{"$schema":` + d3 + `,"extends":{"minimum":5},"divisibleBy":2,"disallow":"string",
			"properties":{"a":{"type":"integer","required":true}}}`
		if err := json.Unmarshal([]byte(doc), &s); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		s.Normalize()
		if len(s.AllOf) != 1 {
			t.Errorf("allOf = %v, want the one branch `extends` produced; the gate deleted the rewrite's output", s.AllOf)
		}
		if s.MultipleOf == nil || *s.MultipleOf != "2" {
			t.Errorf("multipleOf = %v, want 2 from `divisibleBy`", s.MultipleOf)
		}
		if s.Not == nil {
			t.Errorf("not = nil, want the schema `disallow` produced")
		}
		if len(s.Required) != 1 || s.Required[0] != "a" {
			t.Errorf("required = %v, want [a] promoted from the property's boolean", s.Required)
		}
	})

	t.Run("draft 6 honours none of them", func(t *testing.T) {
		var s Schema
		doc := `{"$schema":` + d6 + `,"extends":{"minimum":5},"divisibleBy":2,"disallow":"string",
			"properties":{"a":{"type":"integer","required":true}}}`
		if err := json.Unmarshal([]byte(doc), &s); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		s.Normalize()
		if len(s.AllOf) != 0 {
			t.Errorf("allOf = %v, want none: `extends` is draft 3's alone", s.AllOf)
		}
		if s.MultipleOf != nil {
			t.Errorf("multipleOf = %v, want none: `divisibleBy` is draft 3's alone", *s.MultipleOf)
		}
		if s.Not != nil {
			t.Errorf("not = %+v, want none: `disallow` is draft 3's alone", s.Not)
		}
		if len(s.Required) != 0 {
			t.Errorf("required = %v, want none: the per-property boolean is draft 3's spelling", s.Required)
		}
		if prop := s.Properties["a"]; prop == nil || len(prop.Required) != 0 {
			t.Errorf("the property kept %v in its required; the sentinel that was not promoted must be cleared too",
				s.Properties["a"].Required)
		}
	})

	// A draft-7 document's `dependencies` is the same shape one draft later: the
	// keyword the dialect has must survive to be rewritten, and the 2019-09
	// spelling of it must not be honoured there.
	t.Run("draft 7 splits dependencies and ignores the 2019-09 pair", func(t *testing.T) {
		var s Schema
		doc := `{"$schema":"http://json-schema.org/draft-07/schema#",
			"dependencies":{"a":["b"]},"dependentRequired":{"c":["d"]}}`
		if err := json.Unmarshal([]byte(doc), &s); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		s.Normalize()
		if got := s.DependentRequired["a"]; len(got) != 1 || got[0] != "b" {
			t.Errorf("dependentRequired[a] = %v, want [b] from `dependencies`", got)
		}
		if _, ok := s.DependentRequired["c"]; ok {
			t.Errorf("dependentRequired = %v: draft 7 honoured a keyword 2019-09 introduced", s.DependentRequired)
		}
	})
}

// TestDialectGateFollowsAnEmbeddedResourcesOwnSchema pins the per-node half.
//
// A resource that declares its own $schema is written in that dialect, and the
// host document's dialect does not reach into it -- nor the other way about.
func TestDialectGateFollowsAnEmbeddedResourcesOwnSchema(t *testing.T) {
	doc := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$defs": {
			"legacy": {
				"$id": "https://ex.test/legacy",
				"$schema": "http://json-schema.org/draft-03/schema#",
				"divisibleBy": 2,
				"multipleOf": 3
			},
			"modern": {"multipleOf": 3, "divisibleBy": 2}
		}
	}`
	var s Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	legacy := s.Defs["legacy"]
	if legacy.MultipleOf == nil || *legacy.MultipleOf != "2" {
		t.Errorf("embedded draft-3 resource: multipleOf = %v, want 2 from its own `divisibleBy` "+
			"(and its `multipleOf`, which draft 3 does not define, dropped)", legacy.MultipleOf)
	}
	modern := s.Defs["modern"]
	if modern.MultipleOf == nil || *modern.MultipleOf != "3" {
		t.Errorf("2020-12 node: multipleOf = %v, want 3; its `divisibleBy` is draft 3's alone", modern.MultipleOf)
	}
	if modern.DivisibleBy != nil {
		t.Errorf("2020-12 node kept divisibleBy = %v", *modern.DivisibleBy)
	}
}

// TestBooleanRequiredIsPromotedByTheParentsDialect covers the one gate that
// cannot be left to the keyword's own node.
//
// Draft 3 writes `"required": true` on the property and means "required in the
// parent", so the promotion happens on the parent and the parent's dialect is
// what decides. A draft-3 resource embedded in a draft-6 document states the
// spelling legitimately -- and the draft-6 parent still has no reading of it, so
// nothing is promoted.
func TestBooleanRequiredIsPromotedByTheParentsDialect(t *testing.T) {
	doc := `{
		"$schema": "http://json-schema.org/draft-06/schema#",
		"type": "object",
		"properties": {
			"a": {
				"$id": "https://ex.test/legacy",
				"$schema": "http://json-schema.org/draft-03/schema#",
				"type": "integer",
				"required": true
			}
		}
	}`
	var s Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	if len(s.Required) != 0 {
		t.Errorf("required = %v, want none: the promotion is the parent's, and draft 6 has no reading "+
			"of draft 3's per-property boolean", s.Required)
	}
}

// TestNormalizeForDraftOverridesOnlyTheRoot pins what --draft reaches.
func TestNormalizeForDraftOverridesOnlyTheRoot(t *testing.T) {
	doc := `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"const": "x",
		"$defs": {"kept": {"$schema": "https://json-schema.org/draft/2020-12/schema", "const": "y"}}
	}`
	var s Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.NormalizeForDraft(Draft04)
	if s.Const != nil {
		t.Errorf("root const survived %v; --draft 4 stands in for the root's own $schema, and draft 4 "+
			"has no const", *s.Const)
	}
	if kept := s.Defs["kept"]; kept.Const == nil {
		t.Errorf("the embedded 2020-12 resource lost its const; --draft supplies the root's dialect only")
	}

	// DraftUnknown means "read the dialect from the document", not "this document
	// has none": without this the override would be indistinguishable from
	// switching the gate off.
	var byDoc Schema
	if err := json.Unmarshal([]byte(`{"$schema":"http://json-schema.org/draft-04/schema#","const":"x"}`), &byDoc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	byDoc.NormalizeForDraft(DraftUnknown)
	if byDoc.Const != nil {
		t.Errorf("const survived NormalizeForDraft(DraftUnknown) on a draft-4 document")
	}
}

// TestDialectGateReachesEveryPosition holds the gate to the same traversal the
// rewrites use.
//
// A keyword under propertyNames or contentSchema is as much the dialect's
// business as one at the root, and those two positions are exactly the ones an
// earlier pass of this shape missed.
func TestDialectGateReachesEveryPosition(t *testing.T) {
	doc := `{
		"$schema": "http://json-schema.org/draft-04/schema#",
		"type": "object",
		"propertyNames": {"const": "a"},
		"contentSchema": {"const": "b"},
		"properties": {"p": {"const": "c"}},
		"items": {"const": "d"},
		"allOf": [{"const": "e"}]
	}`
	var s Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	// propertyNames and contentSchema are themselves keywords draft 4 does not
	// define, so the gate clears them outright; the positions that survive are
	// where the recursion is checked.
	if s.PropertyNames != nil {
		t.Errorf("propertyNames survived on draft 4")
	}
	if s.ContentSchema != nil {
		t.Errorf("contentSchema survived on draft 4")
	}
	for name, node := range map[string]*Schema{
		"properties.p": s.Properties["p"],
		"items":        s.Items.Schema,
		"allOf[0]":     s.AllOf[0],
	} {
		if node == nil {
			t.Fatalf("%s is missing from the parsed document", name)
		}
		if node.Const != nil {
			t.Errorf("%s kept its const under draft 4; the gate did not reach it", name)
		}
	}
}

// TestAKeywordWithNoRowIsLeftAlone pins the direction the table fails in.
//
// dropKeywordsOutsideDialect fails open: a keyword with no row is kept in every
// dialect rather than dropped in every dialect. That choice is not reachable
// from any document -- TestEveryParsedKeywordHasADialectRow forbids the state it
// needs -- so it is exercised by removing a row here and putting it back. Fail
// open is the only survivable default: a keyword this package models and nobody
// wrote a row for would otherwise stop constraining anything, everywhere, and
// the generated code would still compile and still look right.
func TestAKeywordWithNoRowIsLeftAlone(t *testing.T) {
	saved, ok := keywordDialects["const"]
	if !ok {
		t.Fatalf("no row for const to remove")
	}
	delete(keywordDialects, "const")
	defer func() { keywordDialects["const"] = saved }()

	var s Schema
	if err := json.Unmarshal([]byte(`{"$schema":"http://json-schema.org/draft-04/schema#","const":"a"}`), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	if s.Const == nil {
		t.Errorf("a keyword with no row was dropped on draft 4; a forgotten row must leave the keyword " +
			"binding as it did before, not disable it in every dialect")
	}
	if !KeywordDefinedIn("const", Draft04) {
		t.Errorf("KeywordDefinedIn answers no for a keyword with no row")
	}
	if !KeywordFormDefinedIn("const", formBoolean, Draft04) {
		t.Errorf("KeywordFormDefinedIn answers no for a keyword with no row")
	}
}

// TestAKeywordThisPackageDoesNotModelIsLeftAlone is the same default asked the
// way a caller reaches it: a vendor keyword, or one from a vocabulary this
// package has never heard of, is not something the table can speak for.
func TestAKeywordThisPackageDoesNotModelIsLeftAlone(t *testing.T) {
	for _, d := range []Draft{Draft03, Draft07, DraftV1} {
		if !KeywordDefinedIn("x-vendor-thing", d) {
			t.Errorf("KeywordDefinedIn(x-vendor-thing, %v) = false; an unmodelled keyword must not be "+
				"reported as one the dialect withdrew", d)
		}
	}
}

// TestConstNullIsGatedWithConst covers the field whose presence the marshaled
// form erases.
//
// encoding/json leaves a *any nil for a JSON null, so ConstIsNull is the only
// record that `{"const": null}` was written -- and it is a separate struct field
// from Const. A gate that cleared only the field carrying the json tag would
// leave the flag standing, and draft 4 would go on asserting a const it does not
// define, over the one value the tag cannot carry.
func TestConstNullIsGatedWithConst(t *testing.T) {
	for _, tt := range []struct {
		uri  string
		want bool
	}{
		{"http://json-schema.org/draft-04/schema#", false},
		{"http://json-schema.org/draft-06/schema#", true},
	} {
		var s Schema
		doc := `{"$schema":"` + tt.uri + `","const":null}`
		if err := json.Unmarshal([]byte(doc), &s); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !s.ConstIsNull {
			t.Fatalf("parsing %s did not record const:null at all", doc)
		}
		s.Normalize()
		if s.ConstIsNull != tt.want {
			t.Errorf("ConstIsNull = %v, want %v after normalizing %s", s.ConstIsNull, tt.want, doc)
		}
	}
}

// TestDependenciesIsHonouredAfterItsRemoval holds the one row where the table
// and the specification disagree.
//
// 2019-09 split "dependencies" into dependentRequired and dependentSchemas and
// removed it, so gating it to drafts 3-7 is what a plain reading gives. That
// reading was measured against the pinned suite and failed 25 groups: upstream
// ships optional/dependencies-compatibility.json for draft2019-09, draft2020-12
// and v1 -- tests/latest holds a fourth copy of the path but is a symlink to
// draft2020-12, so it is the same corpus rather than another dialect -- and
// every case in those files marks the keyword binding. This repository treats
// them as corpus, so the keyword is honoured in every dialect.
//
// The point of asserting it here as well is speed of failure. Re-gating the row
// is a one-word edit whose only alarm was a `make test-external` run that takes
// minutes and is not part of `go test ./...`; this fails in milliseconds and
// says why.
func TestDependenciesIsHonouredAfterItsRemoval(t *testing.T) {
	for _, uri := range []string{
		"http://json-schema.org/draft-03/schema#",
		"http://json-schema.org/draft-04/schema#",
		"http://json-schema.org/draft-06/schema#",
		"http://json-schema.org/draft-07/schema#",
		"https://json-schema.org/draft/2019-09/schema",
		"https://json-schema.org/draft/2020-12/schema",
		"https://json-schema.org/v1",
	} {
		var s Schema
		doc := `{"$schema":"` + uri + `","dependencies":{"bar":["foo"]}}`
		if err := json.Unmarshal([]byte(doc), &s); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		s.Normalize()
		got := s.DependentRequired["bar"]
		if len(got) != 1 || got[0] != "foo" {
			t.Errorf("dependentRequired[bar] = %v under %s, want [foo].\n"+
				"`dependencies` must reach the split in every dialect: the suite ships "+
				"optional/dependencies-compatibility.json for 2019-09, 2020-12 and v1, and gating the "+
				"keyword to drafts 3-7 fails 25 of its groups", got, uri)
		}
		if !KeywordDefinedIn("dependencies", DetectDraft(&s)) {
			t.Errorf("KeywordDefinedIn(dependencies, %s) = false; the row and the pass must agree, or a "+
				"later caller reading the table gets the answer the corpus rejects", uri)
		}
	}

	// The reverse compatibility is deliberately not granted: no suite file asks
	// draft 7 to honour the 2019-09 spellings, and inventing it would be the
	// forward direction of #203 all over again.
	var s Schema
	if err := json.Unmarshal([]byte(`{"$schema":"http://json-schema.org/draft-07/schema#",`+
		`"dependentRequired":{"bar":["foo"]}}`), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	if len(s.DependentRequired) != 0 {
		t.Errorf("draft 7 honoured dependentRequired = %v; the compatibility the suite ships runs one way",
			s.DependentRequired)
	}
}
