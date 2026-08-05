package generator

import (
	"encoding/json"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// The tests in this file are about which format a schema is read as stating,
// which is a dialect question twice over: draft 3 has three format names no
// later draft has, and from 2019-09 a metaschema's $vocabulary decides whether
// the keyword asserts at all. Both were answered wrongly in the direction that
// produced no check -- a root resolving to `any`, which Go forbids methods on,
// so nothing about the document was examined.

// rootInferredDef returns the wrapper the root resolved to, or nil when the root
// got no type that can carry a Validate at all.
func rootInferredDef(t *testing.T, doc string, cfg Config) *InferredAliasDef {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	ir, err := New(cfg).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, td := range ir.TypeDefs {
		if def, isInferred := td.(*InferredAliasDef); isInferred && def.Name == "Root" {
			return def
		}
	}
	return nil
}

// ruleValue returns the value of the named rule the definition carries, and
// whether it carries one.
func ruleValue(def *InferredAliasDef, ruleType string) (string, bool) {
	if def == nil {
		return "", false
	}
	for _, r := range def.Validations {
		if r.RuleType == ruleType {
			v, _ := r.Value.(string)
			return v, true
		}
	}
	return "", false
}

// TestDraft3FormatSpellingsAreRecognised covers the three format names draft 3
// has and no later draft does: "host-name", "ip-address" and "color".
//
// Nothing recognised them, so FormatCheckableOnString answered false for the
// keyword, no wrapper was owed, and {"format":"host-name"} came out `type Root
// any` with no Validate at all -- the generated code accepted
// "not_a_valid_host_name", which the official suite marks invalid. Two of the
// three are the modern formats under an older spelling and map onto those names;
// "color" has no counterpart and gets an internal name of its own.
func TestDraft3FormatSpellingsAreRecognised(t *testing.T) {
	for _, tc := range []struct {
		keyword string
		want    string
	}{
		{"host-name", "hostname"},
		{"ip-address", "ipv4"},
		{"color", Draft3ColorFormat},
		// The controls: draft 3 spells these two the way every later draft does,
		// and they must go on being asserted under their own names rather than
		// being caught by the rewrite.
		{"hostname", "hostname"},
		{"ipv4", "ipv4"},
	} {
		t.Run(tc.keyword, func(t *testing.T) {
			doc := `{"format":"` + tc.keyword + `"}`
			def := rootInferredDef(t, doc, Config{PackageName: "testpkg", Draft: schema.Draft03})
			if def == nil {
				t.Fatalf("%s under draft 3 resolves to a type carrying no Validate, so the format is asserted nowhere", doc)
			}
			got, ok := ruleValue(def, "format")
			if !ok {
				t.Fatalf("%s under draft 3 carries no format rule; draft 3 asserts every format it recognises", doc)
			}
			if got != tc.want {
				t.Fatalf("%s under draft 3 asserts format %q, want %q", doc, got, tc.want)
			}
		})
	}
}

// TestDraft3FormatSpellingsReachEveryPosition checks that the rewrite is settled
// on the schema rather than at the one place that happened to be looked at.
//
// The three names had to be recognised before anything asked whether the keyword
// named a checkable format, because every position asks that first and then
// declines to build a rule. Doing it per position is what leaves a format
// asserted behind a $ref and not inline, or on a property and not on an array
// element -- the shape this area has produced more than once.
func TestDraft3FormatSpellingsReachEveryPosition(t *testing.T) {
	const doc = `{
		"$schema": "http://json-schema.org/draft-03/schema#",
		"type": "object",
		"properties": {
			"host": {"format": "host-name"},
			"list": {"type": "array", "items": {"format": "ip-address"}},
			"map":  {"additionalProperties": {"format": "color"}}
		},
		"definitions": {
			"Named": {"format": "host-name"}
		}
	}`
	var s schema.Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	ir, err := New(Config{PackageName: "testpkg", Draft: schema.Draft03}).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	found := map[string]bool{}
	for _, td := range ir.TypeDefs {
		def, isInferred := td.(*InferredAliasDef)
		if !isInferred {
			continue
		}
		if v, ok := ruleValue(def, "format"); ok {
			found[v] = true
		}
	}
	for _, want := range []string{"hostname", "ipv4", Draft3ColorFormat} {
		if !found[want] {
			t.Errorf("no generated type asserts %q; the draft-3 spelling was not settled in every position (found %v)", want, found)
		}
	}
}

// TestDraft3FormatSpellingsStayUnknownOnLaterDrafts is the over-reach guard for
// the rewrite above, and it is the half that matters more.
//
// "host-name" is not a format keyword in draft 4 or anything after it; every
// draft says an unrecognised format is an annotation, so a 2020-12 schema
// writing one must not acquire an assertion. A rewrite applied without reading
// the dialect would refuse "not_a_valid_host_name" under a schema that permits
// it, which is the one failure this generator treats as worse than a missing
// check.
//
// --format-assertion is passed deliberately: it is the configuration under which
// a recognised format is asserted whatever the dialect says, so if the rewrite
// leaked into these dialects this is where it would show. What the flag cannot
// do is invent a meaning for a keyword the dialect has none for.
func TestDraft3FormatSpellingsStayUnknownOnLaterDrafts(t *testing.T) {
	drafts := []schema.Draft{
		schema.Draft04, schema.Draft06, schema.Draft07,
		schema.Draft201909, schema.Draft202012, schema.DraftV1,
	}
	for _, draft := range drafts {
		for _, keyword := range []string{"host-name", "ip-address", "color"} {
			t.Run(draft.String()+"/"+keyword, func(t *testing.T) {
				doc := `{"format":"` + keyword + `"}`
				def := rootInferredDef(t, doc, Config{PackageName: "testpkg", Draft: draft, FormatAssertion: true})
				if got, ok := ruleValue(def, "format"); ok {
					t.Fatalf("%s under %s asserts format %q; the keyword names no format in that dialect and must be carried as an annotation",
						doc, draft, got)
				}
			})
		}
	}
	// The control for the control: the modern spelling *is* recognised under
	// those dialects, so a test that passed by asserting nothing anywhere would
	// fail here.
	def := rootInferredDef(t, `{"format":"hostname"}`, Config{PackageName: "testpkg", Draft: schema.Draft202012, FormatAssertion: true})
	if got, ok := ruleValue(def, "format"); !ok || got != "hostname" {
		t.Fatalf(`{"format":"hostname"} under 2020-12 with --format-assertion asserts %q (ok=%v), want "hostname"`, got, ok)
	}
}

// TestFormatAssertionVocabularyIsRead covers the two suite groups whose schemas
// point $schema at a custom metaschema declaring the format-assertion
// vocabulary.
//
// The dialect URI is that metaschema's own $id, so DetectDraft answers
// DraftUnknown and formatAssertsFor fell to its conservative default: format was
// an annotation, and because the same metaschema declares no validation
// vocabulary the wrapper was declined outright, leaving `type Root any` under
// the one metaschema written to demand a check.
//
// The boolean the declaration carries is deliberately not read. Per the 2020-12
// core specification it tells an implementation that does *not* recognise the
// vocabulary what to do, and says nothing to one that does -- which is why the
// official suite marks "not-an-ipv4" invalid under the false spelling and the
// true one alike, and why the false spelling is the one used here.
func TestFormatAssertionVocabularyIsRead(t *testing.T) {
	metaschema := func(vocab map[string]bool) *schema.Schema {
		vocab["https://json-schema.org/draft/2020-12/vocab/core"] = true
		return &schema.Schema{ID: "http://localhost:1234/meta.json", Vocabulary: vocab}
	}

	for _, tc := range []struct {
		name          string
		vocab         map[string]bool
		wantFormat    string
		wantMinLength bool
	}{
		{
			// The suite's format-assertion-false.json, keyword for keyword.
			name:          "format-assertion without the validation vocabulary",
			vocab:         map[string]bool{"https://json-schema.org/draft/2020-12/vocab/format-assertion": false},
			wantFormat:    "ipv4",
			wantMinLength: false,
		},
		{
			name: "format-assertion beside the validation vocabulary",
			vocab: map[string]bool{
				"https://json-schema.org/draft/2020-12/vocab/format-assertion": true,
				"https://json-schema.org/draft/2020-12/vocab/validation":       true,
			},
			wantFormat:    "ipv4",
			wantMinLength: true,
		},
		{
			// The control: the standard 2020-12 metaschema's own declaration,
			// which says format is an annotation. A change that read any
			// $vocabulary as "assert" would turn every 2020-12 document into a
			// rejecting one.
			name: "format-annotation",
			vocab: map[string]bool{
				"https://json-schema.org/draft/2020-12/vocab/format-annotation": true,
				"https://json-schema.org/draft/2020-12/vocab/validation":        true,
			},
			wantFormat:    "",
			wantMinLength: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := schema.NewMappingResolver(map[string]*schema.Schema{
				"http://localhost:1234/meta.json": metaschema(tc.vocab),
			})
			doc := `{"$schema":"http://localhost:1234/meta.json","format":"ipv4","minLength":9}`
			def := rootInferredDef(t, doc, Config{PackageName: "testpkg", Resolver: resolver})
			if def == nil {
				t.Fatalf("root resolved to a type carrying no Validate; the metaschema declares the keywords this schema states")
			}
			if got, _ := ruleValue(def, "format"); got != tc.wantFormat {
				t.Errorf("asserts format %q, want %q", got, tc.wantFormat)
			}
			// minLength is a validation-vocabulary keyword, so a metaschema
			// leaving that vocabulary out must not have it enforced: reading the
			// format declaration must not drag the rest of the wrapper's rules
			// in with it.
			if _, got := ruleValue(def, "minLength"); got != tc.wantMinLength {
				t.Errorf("carries minLength=%v, want %v", got, tc.wantMinLength)
			}
		})
	}
}

// TestUnknownMetaschemaKeepsTheConservativePosture is the guard on the other
// side of the vocabulary lookup: a $schema nothing can resolve, and a metaschema
// that declares no $vocabulary at all, must both leave the dialect's own answer
// standing rather than being read as a declaration.
func TestUnknownMetaschemaKeepsTheConservativePosture(t *testing.T) {
	silent := &schema.Schema{ID: "http://localhost:1234/silent.json"}
	resolver := schema.NewMappingResolver(map[string]*schema.Schema{
		"http://localhost:1234/silent.json": silent,
	})
	for _, tc := range []struct {
		name     string
		metaURI  string
		resolver schema.SchemaResolver
	}{
		{"metaschema declaring no vocabulary", "http://localhost:1234/silent.json", resolver},
		{"metaschema nothing resolves", "http://localhost:1234/missing.json", resolver},
		{"no resolver at all", "http://localhost:1234/silent.json", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := `{"$schema":"` + tc.metaURI + `","format":"ipv4"}`
			def := rootInferredDef(t, doc, Config{PackageName: "testpkg", Resolver: tc.resolver})
			if def == nil {
				t.Fatalf("root resolved to a type carrying no Validate")
			}
			if got, ok := ruleValue(def, "format"); ok {
				t.Fatalf("asserts format %q; the dialect is unknown, and an unknown dialect withholds the assertion rather than inventing one", got)
			}
		})
	}
}
