package emitter

import (
	"reflect"
	"regexp"
	"sort"
	"testing"
	"text/template"
	"text/template/parse"

	"github.com/mgilbir/schemagen/internal/gentest"
	_ "github.com/mgilbir/schemagen/pkg/generator" // fills gentest.LedgerRenderedRuleTypes
)

// ledgerRenderSites names, for each list of rules the keyword ledger reads
// (pkg/generator/ledgerrender.go), the template and the {{range}} over the
// list that renders it.
var ledgerRenderSites = map[string]struct{ define, pipe string }{
	"struct":                  {"validate", "$struct.Validations"},
	"struct-non-object":       {"validate", "$struct.NonObjectValidations"},
	"pattern-property":        {"validate", "$pp.Validations"},
	"alias":                   {"alias_validate", ".Validations"},
	"alias-variant":           {"alias_validate", "$variant"},
	"inferred":                {"inferred_alias_validate", ".Validations"},
	"inferred-variant":        {"inferred_alias_validate", "$variant"},
	"bigint":                  {"bigint_alias_validate", ".Validations"},
	"bigint-variant":          {"bigint_alias_variant_checks", ".Rules"},
	"item-level":              {"item_level", "$lv.Rules"},
	"not-branch":              {"not_schema", "$branch.Validations"},
	"oneof-variant":           {"unmarshal", "$v.Checks"},
	"uneval-properties":       {"validate", "$uneval.Validations"},
	"contains":                {"contains_check", ".Def.Checks"},
	"uneval-items":            {"uneval_item_checks", ".Def.Checks"},
	"inferred-items":          {"inferred_alias_validate", ".ItemsChecks"},
	"inferred-contains-evals": {"inferred_alias_validate", ".Contains.Checks"},
}

var ruleKindTest = regexp.MustCompile(`eq \.(?:RuleType|CheckType) "([A-Za-z]+)"`)

// TestLedgerKnowsWhatTheTemplatesRender holds the keyword ledger's table of
// which kinds of rule each list is rendered for to the templates that render
// them.
//
// A template walks its list of rules with an `if eq .RuleType "..."` per kind
// it knows and passes any other kind over in silence. The ledger credits a
// keyword only for a rule its template renders, so a table that names a kind
// the template dropped would have the ledger report enforced a keyword no code
// checks -- the one thing the ledger exists not to do -- and one that misses a
// kind the template added would have it report a keyword that is checked. So
// the table is read back out of the templates' parse trees here, both ways;
// and every {{range}} that tests a rule's kind must be one the table names, so
// a list added to the templates is not a list the ledger has never heard of.
func TestLedgerKnowsWhatTheTemplatesRender(t *testing.T) {
	tmpl, _ := parseTemplatesForLint(t)
	table := gentest.LedgerRenderedRuleTypes()

	found := map[string][][]string{}
	seen := map[string]bool{}
	for _, tt := range tmpl.Templates() {
		if tt.Tree == nil || tt.Tree.Root == nil {
			continue
		}
		forEachRange(tt.Tree.Root, func(r *parse.RangeNode) {
			kinds := ownRuleKinds(tmpl, r.List, map[string]bool{})
			if len(kinds) == 0 {
				return
			}
			key := tt.Name() + " " + r.Pipe.String()
			seen[key] = true
			for list, site := range ledgerRenderSites {
				if site.define == tt.Name() && site.pipe == r.Pipe.String() {
					found[list] = append(found[list], kinds)
				}
			}
		})
	}

	for list, site := range ledgerRenderSites {
		want, ok := table[list]
		if !ok {
			t.Errorf("the ledger has no entry for %q, which this test maps to {{range %s}} in %s", list, site.pipe, site.define)
			continue
		}
		if len(found[list]) == 0 {
			t.Errorf("%q: no {{range %s}} in %s tests a rule's kind; the site moved and this map did not follow",
				list, site.pipe, site.define)
		}
		for _, got := range found[list] {
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%q: {{range %s}} in %s renders %v; the ledger credits %v", list, site.pipe, site.define, got, want)
			}
		}
	}
	for list := range table {
		if _, ok := ledgerRenderSites[list]; !ok {
			t.Errorf("the ledger names a list %q no template site is mapped to", list)
		}
	}
	mapped := map[string]bool{}
	for _, site := range ledgerRenderSites {
		mapped[site.define+" "+site.pipe] = true
	}
	var unmapped []string
	for key := range seen {
		if !mapped[key] {
			unmapped = append(unmapped, key)
		}
	}
	sort.Strings(unmapped)
	for _, key := range unmapped {
		t.Errorf("{{range}} %s tests a rule's kind and is no list the keyword ledger knows; add it to ledgerrender.go and ledgerRenderSites", key)
	}
}

// forEachRange calls fn on every {{range}} under n.
func forEachRange(n parse.Node, fn func(*parse.RangeNode)) {
	switch x := n.(type) {
	case *parse.ListNode:
		if x == nil {
			return
		}
		for _, c := range x.Nodes {
			forEachRange(c, fn)
		}
	case *parse.RangeNode:
		fn(x)
		forEachRange(x.List, fn)
		if x.ElseList != nil {
			forEachRange(x.ElseList, fn)
		}
	case *parse.IfNode:
		forEachRange(x.List, fn)
		if x.ElseList != nil {
			forEachRange(x.ElseList, fn)
		}
	case *parse.WithNode:
		forEachRange(x.List, fn)
		if x.ElseList != nil {
			forEachRange(x.ElseList, fn)
		}
	}
}

// ownRuleKinds lists, sorted, the rule kinds tested under n -- following the
// templates n calls, and leaving out what a nested {{range}} tests, which is
// that range's list and not this one's.
func ownRuleKinds(tmpl *template.Template, n parse.Node, calling map[string]bool) []string {
	set := map[string]bool{}
	var walk func(n parse.Node)
	walk = func(n parse.Node) {
		switch x := n.(type) {
		case *parse.ListNode:
			if x == nil {
				return
			}
			for _, c := range x.Nodes {
				walk(c)
			}
		case *parse.IfNode:
			for _, m := range ruleKindTest.FindAllStringSubmatch(x.Pipe.String(), -1) {
				set[m[1]] = true
			}
			walk(x.List)
			if x.ElseList != nil {
				walk(x.ElseList)
			}
		case *parse.WithNode:
			walk(x.List)
			if x.ElseList != nil {
				walk(x.ElseList)
			}
		case *parse.TemplateNode:
			if calling[x.Name] {
				return
			}
			if called := tmpl.Lookup(x.Name); called != nil && called.Tree != nil {
				calling[x.Name] = true
				for _, k := range ownRuleKinds(tmpl, called.Tree.Root, calling) {
					set[k] = true
				}
				delete(calling, x.Name)
			}
		}
	}
	walk(n)
	var out []string
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
