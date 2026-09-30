package generator

import (
	"github.com/mgilbir/schemagen/pkg/schema"
)

// LedgerEntry is one keyword the keyword ledger has something to say about.
//
// Location is where the document wrote the node stating the keyword: a JSON
// Pointer fragment ("#/properties/a") for the document handed to Generate, and
// that fragment after the document's URI for a node of any other document --
// the spelling every other located diagnostic of this package uses. Def is the
// generated type whose scope the node lies in, and is empty for a node no
// generated type is responsible for at all (invariant I2). Reason says why the
// keyword is listed.
type LedgerEntry struct {
	Location string
	Keyword  string
	Def      string
	Reason   string
}

// String writes the entry the way the command line prints it.
func (e LedgerEntry) String() string {
	where := e.Location
	if e.Def != "" {
		where += " (type " + e.Def + ")"
	}
	return where + ": " + e.Keyword + ": " + e.Reason
}

// Unclaimed lists what the keyword ledger found the last Generate call did not
// enforce: every assertion a schema states that no element of the generated
// code carries, is vacuous for, or hands to a type that does (invariant I1),
// and every schema the document evaluates that no generated type is
// responsible for (I2). Sorted by location, then keyword.
//
// It is a report, not a refusal: the code was generated, and each entry is a
// document the generated Validate may accept although the schema rejects it.
// See ledger.go for what "claimed" means and how it is read off the IR.
func (g *Generator) Unclaimed() []LedgerEntry {
	return append([]LedgerEntry(nil), g.ledger.unclaimed...)
}

// Routed lists the keywords the last Generate call carried by handing them to
// the runtime evaluator where the static checks could not, in the shape
// Unclaimed uses. Nothing routes a keyword yet, so it is empty; it is the
// report the routing that replaces unclaimed keywords fills.
func (g *Generator) Routed() []LedgerEntry {
	return append([]LedgerEntry(nil), g.ledger.routed...)
}

// ledgerState is what one Generate call records for the keyword ledger while
// it generates, and what the ledger found once it had. Reset by Generate.
type ledgerState struct {
	// synthesizedFrom maps a node this generator built to the node it was
	// built from; see noteSynthesized.
	synthesizedFrom map[*schema.Schema]*schema.Schema

	// defs are the declarations generateTypeDef produced, with the node each
	// was generated for, in the order they were produced; see applyLedger.
	defs []ledgerDef

	// declines records, for a keyword a collector offered to the runtime
	// evaluator, why the evaluator declined it; see noteEvaluatorDecline.
	declines map[ledgerKey]string

	unclaimed []LedgerEntry
	routed    []LedgerEntry

	// keywordBuf is statedAssertions' scratch list, reused node to node.
	keywordBuf []string
}

// ledgerDef is one declaration and the node it was generated for.
type ledgerDef struct {
	name string
	node *schema.Schema
}

// ledgerKey names one keyword of one schema node.
type ledgerKey struct {
	node    *schema.Schema
	keyword string
}

// noteSynthesized records that synth is a node this generator built out of
// from and the nodes from reaches in place -- a merge target, or a value copy
// with the bounds of an allOf folded into it -- so the keyword ledger can map
// what an element built from synth enforces back to the nodes the document
// wrote. See Provenance.
func (g *Generator) noteSynthesized(synth, from *schema.Schema) {
	if synth == nil || from == nil || synth == from {
		return
	}
	if g.ledger.synthesizedFrom == nil {
		g.ledger.synthesizedFrom = make(map[*schema.Schema]*schema.Schema)
	}
	if _, ok := g.ledger.synthesizedFrom[synth]; !ok {
		g.ledger.synthesizedFrom[synth] = from
	}
}

// noteEvaluatorDecline records that a collector offered keyword of owner to the
// runtime evaluator and the evaluator declined, with the reason it gave. The
// collector then falls back to whatever static reading it has; if that reading
// does not carry the keyword either, the ledger reports it with this reason.
func (g *Generator) noteEvaluatorDecline(owner *schema.Schema, keyword, reason string) {
	if owner == nil {
		return
	}
	if g.ledger.declines == nil {
		g.ledger.declines = make(map[ledgerKey]string)
	}
	key := ledgerKey{owner, keyword}
	if _, ok := g.ledger.declines[key]; ok {
		return
	}
	if reason == "" {
		reason = "the runtime evaluator declined it"
	} else {
		reason = "the runtime evaluator declined it: " + reason
	}
	g.ledger.declines[key] = reason
}

// applyLedger records the declaration generateTypeDef just produced under
// name, for the node s, so the ledger can hold it to invariant I1 once every
// pass that rewrites the IR has run.
//
// It is read at the end of Generate rather than here because the IR is not
// final here: resolveItemValidations, resolvePatternPropertyTypes and the
// passes beside them still add and remove checks, and a claim read before they
// ran would describe code that is not emitted.
func (g *Generator) applyLedger(name string, s *schema.Schema) {
	g.ledger.defs = append(g.ledger.defs, ledgerDef{name: name, node: s})
}
