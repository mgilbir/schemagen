package generator

import (
	"encoding/json"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// The backstop generateTypeDef carries for a node that arrives while its own
// generation is still in flight, under a name other than the one it is being
// generated under.
//
// No document reaches it -- TestNoCorpusDocumentReachesTheRemintBackstop in
// tests/ asserts that over the whole corpus, and every arm that mints a
// position-derived name asks materializeAtPosition before it gets here. That is
// exactly why it needs a test of its own: a safety net nothing exercises is a
// safety net nobody has seen hold. The condition is set up directly, because the
// only schema that could set it up is one this package no longer has an arm for.
func TestTheRemintBackstopAnswersWithTheNameAlreadyInFlight(t *testing.T) {
	doc := `{
		"type": "object",
		"properties": {
			"a": {"type": "object", "properties": {"b": {"type": "string"}}}
		}
	}`
	s := new(schema.Schema)
	if err := json.Unmarshal([]byte(doc), s); err != nil {
		t.Fatalf("document: %v", err)
	}
	s.ComputeBaseURIs(nil, s)

	g := New(Config{Validation: ValidationModeStatic, PackageName: "gen"})
	if _, err := g.Generate(s); err != nil {
		t.Fatalf("generate: %v", err)
	}

	node := s.Properties["a"]
	canonical, ok := g.nodeTypeNames[node]
	if !ok {
		t.Fatalf("the a-property node was never materialized; the fixture no longer sets the condition up")
	}
	if len(g.RemintedInFlight()) != 0 {
		t.Fatalf("the document itself reached the backstop: %v", g.RemintedInFlight())
	}

	// The state the arm that has no guard would leave behind: the node's own
	// generateTypeDef frame is still open, and a position one level down asks
	// for it under a name minted from that position.
	g.nodesInProgress[node] = true
	defer delete(g.nodesInProgress, node)

	before := len(g.output.TypeDefs)
	const posName = "RootAValue"
	if err := g.generateTypeDef(posName, node); err != nil {
		t.Fatalf("generateTypeDef: %v", err)
	}

	reminted := g.RemintedInFlight()
	if len(reminted) != 1 {
		t.Fatalf("the backstop recorded %v, want exactly one entry: an unrecorded backstop is one "+
			"the corpus gate cannot see", reminted)
	}
	if reminted[0].Name != posName || reminted[0].Canonical != canonical {
		t.Errorf("recorded %+v, want {Name:%s Canonical:%s}", reminted[0], posName, canonical)
	}

	added := g.output.TypeDefs[before:]
	if len(added) != 1 {
		t.Fatalf("the call added %d definitions, want exactly one. More than one means the node was "+
			"generated a second time, which is the unbounded case this stops", len(added))
	}
	alias, ok := added[0].(*AliasDef)
	if !ok {
		t.Fatalf("the backstop emitted a %T, want an AliasDef: the position has to be given a type "+
			"it can name, or the caller references something that was never declared", added[0])
	}
	if alias.Name != posName {
		t.Errorf("the alias is named %s, want %s: the caller uses the name it asked for", alias.Name, posName)
	}
	named, ok := alias.Underlying.(*NamedType)
	if !ok || named.Name != canonical {
		t.Errorf("the alias stands on %v, want the name already in flight (%s)", alias.Underlying, canonical)
	}
	if !g.generated[posName] {
		t.Errorf("the name was not marked generated, so a second arrival would run the arms again")
	}
}
