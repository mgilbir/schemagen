package generator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestEveryElementRuleTypeIsClassified is the gate that replaces elementRules'
// old `default: continue`.
//
// That arm is why three separate issues existed at once. A keyword added to
// extractValidationRules reached the property position, the alias position and
// the $ref position, and was dropped on an array element and a map value by an
// arm that said nothing -- and the failure mode of a dropped assertion is silent
// acceptance, so nothing ever reported it. uniqueItems (issue #179) was found
// only because the minItems written beside it in the same sub-schema fired;
// `format` had been the same defect one keyword earlier.
//
// Inverting the runtime default is not available: emitting a rule the item_level
// template has no arm for produces a generated file that does not compile, and
// the generator has no diagnostic channel to complain through. So the loudness
// lives here instead, and it is loud in the only way that matters -- it reads
// the *extractor's own source* for the rule types it builds, rather than a list
// somebody would have to remember to update. Adding `RuleType: "foo"` to
// extractValidationRules fails this test until the element position has an
// answer for foo: a Go kind in elementRuleKinds, or a stated reason in
// elementRulesDeclined.
func TestEveryElementRuleTypeIsClassified(t *testing.T) {
	built := ruleTypesBuiltBy(t, "extractValidationRules")
	if len(built) < 10 {
		t.Fatalf("only %d rule types found in extractValidationRules (%v); the source scan has stopped seeing what it reads, "+
			"so this test would pass no matter what the element position dropped", len(built), built)
	}
	for _, rt := range built {
		_, kept := elementRuleKinds[rt]
		reason, declined := elementRulesDeclined[rt]
		switch {
		case kept && declined:
			t.Errorf("rule type %q is both kept and declined by the element position; one of the two entries is stale", rt)
		case !kept && !declined:
			t.Errorf("extractValidationRules builds rule type %q and the element position has no answer for it. "+
				"An array element and a map value would drop it in silence, which is how uniqueItems came to be "+
				"honoured on an array property and ignored one position over at `items` (issue #179). "+
				"Add it to elementRuleKinds with the Go kind its check compiles against, or to elementRulesDeclined "+
				"with the reason it does not belong here.", rt)
		case declined && strings.TrimSpace(reason) == "":
			t.Errorf("rule type %q is declined by the element position with no reason given; "+
				"an entry with no reason records nothing", rt)
		}
	}

	// And the two tables answer for nothing the extractor does not build. A
	// stale entry is not a false accept, but it is a claim that this position
	// has been thought about for a keyword that no longer exists, which is
	// exactly the impression the test is here to make trustworthy.
	inBuilt := make(map[string]bool, len(built))
	for _, rt := range built {
		inBuilt[rt] = true
	}
	for _, table := range []struct {
		name    string
		entries []string
	}{
		{"elementRuleKinds", sortedMapKeys(elementRuleKinds)},
		{"elementRulesDeclined", sortedMapKeys(elementRulesDeclined)},
	} {
		for _, rt := range table.entries {
			if !inBuilt[rt] {
				t.Errorf("%s classifies rule type %q, which extractValidationRules no longer builds", table.name, rt)
			}
		}
	}
}

// TestElementRuleKindsNamesOnlyKindsElementGoKindAnswers holds the other half:
// a kind nothing answers to would drop its rule in every position at once,
// which reads exactly like a working table.
func TestElementRuleKindsNamesOnlyKindsElementGoKindAnswers(t *testing.T) {
	answered := map[string]bool{
		anyElementKind: true,
		"string":       true,
		"number":       true,
		"slice":        true,
		"raw":          true,
	}
	for _, rt := range sortedMapKeys(elementRuleKinds) {
		if !answered[elementRuleKinds[rt]] {
			t.Errorf("elementRuleKinds maps %q to element kind %q, which elementGoKind never answers, "+
				"so the rule is dropped for every element there is", rt, elementRuleKinds[rt])
		}
	}
}

func sortedMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ruleTypesBuiltBy parses generator.go and returns every string literal
// assigned to a ValidationRule's RuleType field inside the named function.
//
// The AST is what makes this a gate rather than a grep: a `RuleType:` written
// in a comment or in another function's body is not a composite-literal field
// and does not reach here, and the function is located by declaration rather
// than by a line range that would drift.
func ruleTypesBuiltBy(t *testing.T, funcName string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "generator.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing generator.go: %v", err)
	}
	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == funcName && fd.Recv == nil {
			fn = fd
			break
		}
	}
	if fn == nil {
		t.Fatalf("generator.go declares no func %s; this test reads its body and can no longer find it", funcName)
	}
	seen := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "RuleType" {
			return true
		}
		lit, ok := kv.Value.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			t.Errorf("%s assigns RuleType from a non-literal expression, which this scan cannot read; "+
				"the element position's exhaustiveness is no longer decidable from the source", funcName)
			return true
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatalf("unquoting %s: %v", lit.Value, err)
		}
		seen[v] = true
		return true
	})
	out := make([]string, 0, len(seen))
	for rt := range seen {
		out = append(out, rt)
	}
	sort.Strings(out)
	return out
}
