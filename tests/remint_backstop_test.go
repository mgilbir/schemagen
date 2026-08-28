package tests

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// TestNoCorpusDocumentReachesTheRemintBackstop is the measured half of the
// position-derived-name enumeration.
//
// generateTypeDef carries a backstop for a node that arrives while its own
// generation is still in flight, under a name other than the one it is being
// generated under: it answers with an alias to the name already in flight and
// records the fact. That condition is exactly the unbounded case -- a node the
// generator keeps re-materialising under a new name, because the name is minted
// from the position and grows a segment per level -- and it is what took the
// process down in #348 and #349, twice, as "fatal error: out of memory".
//
// The backstop is not the fix. Every arm that mints a position-derived name asks
// materializeAtPosition first, and answers with the canonical name exactly,
// which is why no document here reaches the backstop at all. This gate is what
// keeps that true: an arm added tomorrow without the guard is caught here by
// *name*, on the pull request, instead of by a fuzz worker dying six weeks later
// with its stderr discarded. It is the coverage measurement the enumeration in
// pkg/generator/positiontypenames_test.go cannot make on its own -- that one
// reads the source, this one runs it.
//
// Every configuration the fuzz seeds are registered under is exercised, because
// which arm claims a schema depends on the flags: the draft overrides decide
// whether a $ref displaces its siblings at all, and that is the difference
// between a document that reaches the merge arm and one that does not.
func TestNoCorpusDocumentReachesTheRemintBackstop(t *testing.T) {
	paths := corpusSchemaPaths(t)
	sort.Strings(paths)
	if len(paths) == 0 {
		// A gate measuring nothing passes for the wrong reason.
		t.Fatalf("no schemas found under %s", fuzzSchemaDir)
	}

	generated := 0
	for _, path := range paths {
		for _, bits := range fuzzSeedCfgBits {
			s, err := schema.LoadFromFile(path)
			if err != nil {
				continue // a document this generator never sees is not this gate's business
			}
			cfg := fuzzConfig(bits)
			abs, err := filepath.Abs(path)
			if err != nil {
				t.Fatalf("resolving %s: %v", path, err)
			}
			cfg.Resolver = schema.NewCompositeResolver(schema.NewFileResolver(filepath.Dir(abs)))
			s.NormalizeForDraft(cfg.Draft)
			s.ComputeBaseURIs(nil, s)

			g := generator.New(cfg)
			if _, err := g.Generate(s); err != nil {
				// A refusal is a legitimate answer; the backstop is still
				// recorded on the way to it, so the check below still applies.
				_ = err
			}
			generated++
			for _, r := range g.RemintedInFlight() {
				t.Errorf("%s at cfgBits 0x%02X: the type %q was minted for a position while the "+
					"node was already being generated as %q, and generateTypeDef's backstop had to "+
					"answer it. That means an arm reached generateTypeDef with a position-derived "+
					"name and no node-keyed guard -- the shape of #348 and #349, which is unbounded "+
					"recursion everywhere the backstop is not. Route that arm through "+
					"materializeAtPosition and classify it in pkg/generator/positiontypenames_test.go",
					path, bits, r.Name, r.Canonical)
			}
		}
	}
	t.Logf("generated %d (document, configuration) pairs from %d corpus documents; none reached the backstop",
		generated, len(paths))
}
