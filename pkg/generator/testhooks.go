package generator

import (
	"fmt"

	"github.com/mgilbir/schemagen/internal/gentest"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// testHooks are the settings internal/gentest reaches, for this repository's
// tests only. Reset by every Generate call.
type testHooks struct {
	// forceEvaluator generates the root through the runtime evaluator; see
	// gentest.ForceEvaluator.
	forceEvaluator bool
	// forcedDecline is why the evaluator refused the root, when it did.
	forcedDecline string
	forcedRefused bool
}

func init() {
	gentest.ForceEvaluator = func() any {
		return GenerateOption(func(o *generateOptions) { o.forceEvaluator = true })
	}
}

// forcedEvaluatorDef answers the root of a Generate call run under
// gentest.ForceEvaluator with the whole-schema evaluator, and nil for every
// other declaration and every call not run under it.
func (g *Generator) forcedEvaluatorDef(name string, s *schema.Schema) TypeDef {
	if !g.hooks.forceEvaluator || s != g.rootSchema || name != g.rootTypeName {
		return nil
	}
	def, refused, reason := g.runtimeSchemaDefBuilding(name, s)
	if refused {
		g.hooks.forcedRefused = true
		g.hooks.forcedDecline = reason
		return nil
	}
	if def == nil {
		// A schema that reduces to a bare node: {} or a boolean. The static
		// declaration is the evaluator's answer by construction there.
		return nil
	}
	return def
}

// forcedEvaluatorError is Generate's refusal when the evaluator declined a root
// it was asked to take whole.
func (g *Generator) forcedEvaluatorError() error {
	if !g.hooks.forcedRefused {
		return nil
	}
	reason := g.hooks.forcedDecline
	if reason == "" {
		reason = "no reason recorded"
	}
	return fmt.Errorf("the runtime evaluator declined the schema: %s", reason)
}
