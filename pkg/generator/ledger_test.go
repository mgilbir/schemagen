package generator

import (
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

func ledgerFor(t *testing.T, doc string) (*Generator, *schema.Schema, []LedgerEntry) {
	t.Helper()
	s := parseNormalized(t, doc)
	g := New(Config{PackageName: "testpkg", OmitEmpty: true})
	if _, err := g.Generate(s); err != nil {
		t.Fatalf("generate: %v", err)
	}
	return g, s, g.Unclaimed()
}

func hasEntry(entries []LedgerEntry, location, keyword string) bool {
	for _, e := range entries {
		if e.Location == location && e.Keyword == keyword {
			return true
		}
	}
	return false
}

func describeEntries(entries []LedgerEntry) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString("\n\t" + e.String())
	}
	if b.Len() == 0 {
		return " (none)"
	}
	return b.String()
}

// TestLedgerIsQuietWhereEverythingIsEnforced is the ledger's false-alarm
// check: schemas whose every keyword the generated code carries, in the shapes
// the corpus is made of. An entry here is the ledger failing to read a claim
// the IR makes, and a ledger that cries wolf gets ignored.
func TestLedgerIsQuietWhereEverythingIsEnforced(t *testing.T) {
	for name, doc := range map[string]string{
		"object": `{"type":"object","properties":{"a":{"type":"string","minLength":2,"maxLength":9,"pattern":"^x"},
			"b":{"type":"integer","minimum":1,"maximum":9,"multipleOf":2},"c":{"type":"boolean"}},
			"required":["a"],"additionalProperties":false}`,
		"arrays": `{"type":"object","properties":{"xs":{"type":"array","items":{"type":"string","minLength":1},
			"minItems":1,"maxItems":3,"uniqueItems":true},"ys":{"type":"array","items":{"type":"integer","maximum":5}}}}`,
		"refs": `{"type":"object","properties":{"p":{"$ref":"#/$defs/P"},"q":{"$ref":"#/$defs/Q"}},
			"$defs":{"P":{"type":"object","properties":{"n":{"type":"number","exclusiveMinimum":0}},"required":["n"]},
			"Q":{"type":"string","enum":["a","b"]}}}`,
		"enum-and-const": `{"type":"object","properties":{"e":{"enum":["x","y"]},"k":{"const":"z"},"n":{"type":"string","enum":["abc","abcd"],"minLength":3}}}`,
		"map":            `{"type":"object","additionalProperties":{"type":"integer","minimum":0}}`,
		"allOf":          `{"allOf":[{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]},{"properties":{"b":{"type":"integer"}}}]}`,
		"tuple":          `{"type":"array","prefixItems":[{"type":"string"},{"type":"integer"}],"items":false}`,
		"nullable-oneOf": `{"type":"object","properties":{"p":{"oneOf":[{"$ref":"#/$defs/O"},{"type":"null"}]}},
			"$defs":{"O":{"type":"object","properties":{"v":{"type":"string"}}}}}`,
		"annotations": `{"title":"T","description":"d","type":"object","properties":{"a":{"type":"string","readOnly":true,"deprecated":true,"examples":["x"],"$comment":"c","x-vendor":1}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, entries := ledgerFor(t, doc)
			if len(entries) > 0 {
				t.Errorf("the ledger reports keywords the generated code carries:%s", describeEntries(entries))
			}
		})
	}
}

// TestLedgerReportsWhatTheIRDoesNotCarry holds the ledger to the audit's
// findings it exists to surface -- each one a keyword the generated code drops
// without a word -- by where the keyword is written.
func TestLedgerReportsWhatTheIRDoesNotCarry(t *testing.T) {
	for _, tc := range []struct {
		name, doc, location, keyword string
	}{
		{"allOf keeps the first of two patterns",
			`{"type":"string","allOf":[{"pattern":"^a"},{"pattern":"b$"}]}`, "#/allOf/1", "pattern"},
		{"a $ref's items and the sibling items",
			`{"$defs":{"A":{"type":"array","items":{"type":"integer"}}},"$ref":"#/$defs/A","items":{"maximum":3}}`,
			"#/$defs/A/items", "type"},
		{"a nullable named type has no Validate",
			`{"type":["string","null"],"minLength":2}`, "#", "minLength"},
		{"if/then beside a declared scalar type",
			`{"type":"number","if":{"minimum":5},"then":{"multipleOf":2}}`, "#/then", "multipleOf"},
		{"an inline map property drops maxProperties",
			`{"type":"object","properties":{"m":{"type":"object","maxProperties":1,"additionalProperties":{"type":"string"}}}}`,
			"#/properties/m", "maxProperties"},
		{"a conditional behind a $ref in an allOf",
			`{"type":"object","properties":{"a":{},"b":{}},"$defs":{"C":{"allOf":[{"if":{"required":["a"]},"then":{"required":["b"]}}]}},"allOf":[{"$ref":"#/$defs/C"}]}`,
			"#/$defs/C/allOf/0", "if"},
		// The ledger's own soundness: each of these is an element in the IR
		// that the ledger once credited and the generated code does not run
		// for the document the keyword excludes.
		{"a rule kind the wrapper's template does not render",
			`{"type":"object","properties":{"p":{"enum":[],"format":"date"}}}`, "#/properties/p", "enum"},
		{"object-path checks where a non-object is held unjudged",
			`{"type":"null","properties":{"a":{"oneOf":[{"type":"string"},{"type":"integer"}]}}}`, "#", "type"},
		{"a contains the generator read as always true",
			`{"type":"array","contains":{"if":false,"else":true,"type":"string"}}`, "#/contains", "type"},
		{"a typed ipv4 whose decode and family check take an empty string",
			`{"$schema":"http://json-schema.org/draft-07/schema#","type":"string","format":"ipv4"}`, "#", "format"},
		{"a oneOf every skipped kind satisfies twice",
			`{"maxLength":5,"oneOf":[{"minLength":1},{"maxLength":3}]}`, "#", "oneOf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, entries := ledgerFor(t, tc.doc)
			if !hasEntry(entries, tc.location, tc.keyword) {
				t.Errorf("the ledger does not report %s at %s; it reported:%s", tc.keyword, tc.location, describeEntries(entries))
			}
		})
	}
}

// TestLedgerSeesAnElementTakenOutOfTheIR is the ledger's own negative plant.
// Each case generates a schema the ledger finds nothing wrong with, then takes
// one check out of the IR -- the IR Generate returned, as the emitter would
// have read it -- and runs the ledger again. It must now report exactly the
// keyword that check carried. A ledger that stayed quiet would be reading its
// claims from somewhere other than the code about to be emitted.
func TestLedgerSeesAnElementTakenOutOfTheIR(t *testing.T) {
	for _, tc := range []struct {
		name, doc, location, keyword string
		remove                       func(g *Generator) bool
	}{
		{"a struct field's rule", `{"type":"object","properties":{"a":{"type":"string","minLength":2}}}`,
			"#/properties/a", "minLength", func(g *Generator) bool {
				for _, td := range g.output.TypeDefs {
					if sd, ok := td.(*StructDef); ok {
						for i, r := range sd.Validations {
							if r.RuleType == "minLength" {
								sd.Validations = append(sd.Validations[:i], sd.Validations[i+1:]...)
								return true
							}
						}
					}
				}
				return false
			}},
		{"an alias's own rule", `{"type":"string","maxLength":3}`, "#", "maxLength", func(g *Generator) bool {
			for _, td := range g.output.TypeDefs {
				if ad, ok := td.(*AliasDef); ok && len(ad.Validations) > 0 {
					ad.Validations = nil
					return true
				}
			}
			return false
		}},
		{"an element rule", `{"type":"object","properties":{"xs":{"type":"array","items":{"type":"integer","maximum":5}}}}`,
			"#/properties/xs/items", "maximum", func(g *Generator) bool {
				for _, td := range g.output.TypeDefs {
					if sd, ok := td.(*StructDef); ok {
						for i := range sd.ItemValidations {
							for j := range sd.ItemValidations[i].Levels {
								if len(sd.ItemValidations[i].Levels[j].Rules) > 0 {
									sd.ItemValidations[i].Levels[j].Rules = nil
									return true
								}
							}
						}
					}
				}
				return false
			}},
		{"a contains type arm", `{"type":"array","contains":{"type":"number"}}`,
			"#/contains", "type", func(g *Generator) bool {
				for _, td := range g.output.TypeDefs {
					if ad, ok := td.(*AliasDef); ok && ad.Contains != nil {
						for i, c := range ad.Contains.Checks {
							if c.CheckType == "type" {
								ad.Contains.Checks = append(ad.Contains.Checks[:i], ad.Contains.Checks[i+1:]...)
								return true
							}
						}
					}
				}
				return false
			}},
		{"a contains check", `{"type":"array","contains":{"type":"string","minLength":3}}`,
			"#/contains", "minLength", func(g *Generator) bool {
				for _, td := range g.output.TypeDefs {
					if ad, ok := td.(*AliasDef); ok && ad.Contains != nil {
						for i, c := range ad.Contains.Checks {
							if c.CheckType == "minLength" {
								ad.Contains.Checks = append(ad.Contains.Checks[:i], ad.Contains.Checks[i+1:]...)
								return true
							}
						}
					}
				}
				return false
			}},
		{"the required list", `{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`,
			"#", "required", func(g *Generator) bool {
				for _, td := range g.output.TypeDefs {
					if sd, ok := td.(*StructDef); ok && len(sd.RequiredJSON) > 0 {
						sd.RequiredJSON = nil
						return true
					}
				}
				return false
			}},
		{"a dependentRequired check", `{"type":"object","properties":{"a":{},"b":{}},"dependentRequired":{"a":["b"]}}`,
			"#", "dependentRequired", func(g *Generator) bool {
				for _, td := range g.output.TypeDefs {
					if sd, ok := td.(*StructDef); ok && len(sd.DependentRequired) > 0 {
						sd.DependentRequired = nil
						return true
					}
				}
				return false
			}},
		{"an enum", `{"type":"object","properties":{"e":{"enum":["x","y"]}}}`,
			"#/properties/e", "enum", func(g *Generator) bool {
				for i, td := range g.output.TypeDefs {
					if _, ok := td.(*EnumDef); ok {
						g.output.TypeDefs[i] = &AliasDef{Name: td.TypeName(), Underlying: &PrimitiveType{Name: "string"}}
						return true
					}
				}
				return false
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, s, before := ledgerFor(t, tc.doc)
			if len(before) > 0 {
				t.Fatalf("the unplanted schema already has entries:%s", describeEntries(before))
			}
			if !tc.remove(g) {
				t.Fatal("the plant found nothing to take out; the case no longer builds the element it names")
			}
			g.runLedger(s)
			after := g.Unclaimed()
			if !hasEntry(after, tc.location, tc.keyword) {
				t.Errorf("with the %s taken out of the IR the ledger does not report %s at %s; it reported:%s",
					tc.name, tc.keyword, tc.location, describeEntries(after))
			}
		})
	}
}

// TestLedgerDelegationThroughAWrapperIsPartial: an inferred wrapper holds a
// value of another kind as it came and hands only the kind it decodes to the
// type its ValidateAs names. That type answers for its node for that kind
// alone: A's `type` refuses a string, A's own decode enforces that, and a
// string the wrapper stores raw never reaches A -- so for the wrapper nothing
// enforces it, and delegating A's node whole would hide that.
//
// No schema the generator is given today sets ValidateAs on an inferred
// wrapper, so the wrapper is put into the IR by hand, in place of the alias
// the schema generates, as the emitter would read it.
func TestLedgerDelegationThroughAWrapperIsPartial(t *testing.T) {
	const doc = `{"allOf":[{"$ref":"#/$defs/A"}],"$defs":{"A":{"type":"array","items":{"type":"integer"},"minItems":1}}}`
	g, s, before := ledgerFor(t, doc)
	if len(before) > 0 {
		t.Fatalf("the unplanted schema already has entries:%s", describeEntries(before))
	}
	replaced := false
	for i, td := range g.output.TypeDefs {
		if ad, ok := td.(*AliasDef); ok && ad.Name == "Root" {
			g.output.TypeDefs[i] = &InferredAliasDef{
				Name:             "Root",
				InferredGoType:   ad.Underlying,
				InferredJSONType: "array",
				ValidateAs:       "A",
			}
			replaced = true
		}
	}
	if !replaced {
		t.Fatal("the case no longer generates Root as an alias to put the wrapper in place of")
	}
	g.runLedger(s)
	after := g.Unclaimed()
	found := false
	for _, e := range after {
		if e.Location == "#/$defs/A" && e.Keyword == "type" && e.Def == "Root" {
			found = true
		}
	}
	if !found {
		t.Errorf("with Root a wrapper delegating to A, the ledger does not report A's `type` for Root, which a string Root holds raw never reaches:%s", describeEntries(after))
	}
}

// TestLedgerFoldsWhatADeclinedKeywordHolds holds report's folding: a chain of
// keywords inside one the evaluator declined is one finding, at the top, with
// the rest counted on it -- not one finding per link. Each link's location is
// as long as the link is deep, so the unfolded report of a document n deep is
// quadratic in n, and the fuzz seed 2000 `not`s deep spent seconds building
// eight megabytes of it. The chain here is deeper than the evaluator's depth
// bound, so the decline is at the top, and deeper than any fixed number of
// steps up the holders, so a link found by looking only a few levels up for
// the decline is reported on its own and fails the count.
func TestLedgerFoldsWhatADeclinedKeywordHolds(t *testing.T) {
	const depth = maxRuntimeDepth + 12
	doc := strings.Repeat(`{"not":`, depth) + `{"type":"string"}` + strings.Repeat(`}`, depth)
	_, _, entries := ledgerFor(t, doc)
	if len(entries) != 1 {
		t.Fatalf("a %d-deep chain of declined `not`s is %d findings, want 1:%s", depth, len(entries), describeEntries(entries[:min(len(entries), 5)]))
	}
	e := entries[0]
	if e.Location != "#" || e.Keyword != "not" || !strings.Contains(e.Reason, "declined") {
		t.Fatalf("the finding is %s, want the root's `not`, declined", e)
	}
	// Every `not` below the root's, and the innermost `type`.
	want := "the " + strconv.Itoa(depth) + " keywords written inside it"
	if !strings.Contains(e.Reason, want) {
		t.Errorf("the finding does not count what it holds (%q): %s", want, e.Reason)
	}
}
