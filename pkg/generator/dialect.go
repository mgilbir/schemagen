package generator

import (
	"fmt"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// DialectOverride is one "$schema" a document states that Config.Draft (the
// --draft flag) overrode: the document says Stated, and it was read as Applied.
// Location is where the document wrote the node, in the spelling every located
// diagnostic of this package uses.
type DialectOverride struct {
	Location string
	Stated   schema.Draft
	Applied  schema.Draft
}

// DialectOverrides lists every "$schema" the last Generate call's document
// states and Config.Draft overrode, in document order.
//
// A dialect chosen from outside wins for the documents the caller handed over;
// see pkg/schema/dialect.go. Where the document says otherwise, one of the two
// is a mistake, and only the caller knows which -- so the override is carried
// out and reported, not carried out in silence. An embedded resource that
// declares its own $id beside its $schema keeps its dialect and is not listed.
func (g *Generator) DialectOverrides() []DialectOverride {
	return append([]DialectOverride(nil), g.dialectOverrides...)
}

// noteDialectOverrides records, for the document s belongs to, each node whose
// stated $schema the dialect it is read under contradicts.
func (g *Generator) noteDialectOverrides(s *schema.Schema) {
	g.dialectOverrides = nil
	if g.config.Draft == schema.DraftUnknown || g.index == nil {
		return
	}
	doc, _, ok := s.SourceLocation()
	if !ok {
		doc = s
	}
	locator := docLocator{home: g.homeDoc}
	seen := make(map[*schema.Schema]bool)
	var walk func(n *schema.Schema)
	walk = func(n *schema.Schema) {
		if n == nil || seen[n] {
			return
		}
		seen[n] = true
		if stated := schema.DetectDraft(n); stated != schema.DraftUnknown {
			if applied := g.index.DialectOf(n); applied != stated {
				loc, ok := locator.name(n)
				if !ok {
					loc = "#"
				}
				g.dialectOverrides = append(g.dialectOverrides, DialectOverride{Location: loc, Stated: stated, Applied: applied})
			}
		}
		for _, c := range ledgerChildren(n, true) {
			walk(c.node)
		}
	}
	walk(doc)
}

// checkDialectSource refuses a resource index that reads the documents under a
// different dialect than Config.Draft names.
//
// The index decides from the dialect what a resource is, and the generator
// reads every keyword under the dialect the index answers (draftForSchema), so
// there is exactly one answer only while the two were told the same thing. An
// index a caller built -- passed as Config.Resolver or through WithResolver --
// is used as it is, and one built for another dialect would give the document
// resources under one draft and keywords under another, which is the
// inconsistency the single answer exists to end. So the mismatch is an error
// naming both, rather than a choice made on the caller's behalf.
func (g *Generator) checkDialectSource() error {
	if g.index == nil || g.index.Draft() == g.config.Draft {
		return nil
	}
	return fmt.Errorf("Config.Draft is %s but the schema.ResourceIndex generating through reads its documents under %s; "+
		"build the index with schema.WithIndexDraft(%s) so the two answer which dialect a document is in the same way",
		g.config.Draft, g.index.Draft(), g.config.Draft)
}
