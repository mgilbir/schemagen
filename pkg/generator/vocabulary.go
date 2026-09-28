package generator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// knownVocabularies are the vocabularies this generator implements, by the URI
// a metaschema's $vocabulary names them with. The hyper-schema vocabularies
// are listed because every keyword they define is an annotation for
// validation, and this generator reads those as annotations.
var knownVocabularies = map[string]bool{
	"https://json-schema.org/draft/2019-09/vocab/core":         true,
	"https://json-schema.org/draft/2019-09/vocab/applicator":   true,
	"https://json-schema.org/draft/2019-09/vocab/validation":   true,
	"https://json-schema.org/draft/2019-09/vocab/meta-data":    true,
	"https://json-schema.org/draft/2019-09/vocab/format":       true,
	"https://json-schema.org/draft/2019-09/vocab/content":      true,
	"https://json-schema.org/draft/2019-09/vocab/hyper-schema": true,

	"https://json-schema.org/draft/2020-12/vocab/core":              true,
	"https://json-schema.org/draft/2020-12/vocab/applicator":        true,
	"https://json-schema.org/draft/2020-12/vocab/unevaluated":       true,
	"https://json-schema.org/draft/2020-12/vocab/validation":        true,
	"https://json-schema.org/draft/2020-12/vocab/meta-data":         true,
	"https://json-schema.org/draft/2020-12/vocab/format-annotation": true,
	"https://json-schema.org/draft/2020-12/vocab/format-assertion":  true,
	"https://json-schema.org/draft/2020-12/vocab/content":           true,
	"https://json-schema.org/draft/2020-12/vocab/hyper-schema":      true,
}

// UnknownKeyword is one keyword a schema uses that this generator does not
// know, where the document wrote it.
type UnknownKeyword struct {
	Location string
	Keyword  string
}

// UnknownKeywordsError is Config.StrictKeywords refusing a schema: every
// keyword it uses that this generator does not know, in document order.
type UnknownKeywordsError struct {
	Keywords []UnknownKeyword
}

func (e *UnknownKeywordsError) Error() string {
	lines := make([]string, 0, len(e.Keywords))
	for _, k := range e.Keywords {
		lines = append(lines, fmt.Sprintf("%s: %q", k.Location, k.Keyword))
	}
	return fmt.Sprintf("the schema uses %d keyword(s) schemagen does not know, which --strict-keywords refuses "+
		"(without it they are annotations and constrain nothing):\n  %s", len(lines), strings.Join(lines, "\n  "))
}

// UnknownVocabularyError is a metaschema requiring a vocabulary this generator
// does not implement. JSON Schema 2020-12 §8.1.2 (and 2019-09 §8.1.2): "If
// the value is true, then implementations that do not recognize the vocabulary
// MUST refuse to process any schemas that declare this meta-schema".
type UnknownVocabularyError struct {
	Location   string
	Metaschema string
	Vocabulary []string
}

func (e *UnknownVocabularyError) Error() string {
	return fmt.Sprintf("%s: its metaschema %s requires vocabulary %s, which schemagen does not implement; "+
		"a schema declaring a metaschema with a required vocabulary its implementation does not recognise must be refused",
		e.Location, e.Metaschema, strings.Join(e.Vocabulary, ", "))
}

// checkKeywordsAndVocabularies refuses the document s belongs to where its
// metaschema requires a vocabulary this generator does not implement, and --
// under Config.StrictKeywords -- where it uses a keyword this generator does
// not know. Both walk every node the document holds, its definitions included:
// every one of them is generated as a type.
func (g *Generator) checkKeywordsAndVocabularies(s *schema.Schema) error {
	doc, _, ok := s.SourceLocation()
	if !ok {
		doc = s
	}
	locator := docLocator{home: g.homeDoc}
	where := func(n *schema.Schema) string {
		if loc, ok := locator.name(n); ok {
			return loc
		}
		return "#"
	}
	// The walk visits every node in no particular order and keeps only the
	// few that say something here -- a $schema, or, under StrictKeywords, a
	// keyword nobody knows -- which are then taken in the order of where the
	// document wrote them, so the refusal names the same place on every run.
	type site struct {
		loc string
		n   *schema.Schema
	}
	var sites []site
	seen := make(map[*schema.Schema]bool)
	var walk func(n *schema.Schema)
	walk = func(n *schema.Schema) {
		if n == nil || seen[n] {
			return
		}
		seen[n] = true
		if n.Schema != "" || (g.config.StrictKeywords && len(n.Extensions) > 0) {
			sites = append(sites, site{loc: where(n), n: n})
		}
		eachSubschema(n, true, walk)
	}
	walk(doc)
	sort.SliceStable(sites, func(i, j int) bool { return sites[i].loc < sites[j].loc })

	var unknown []UnknownKeyword
	seenMeta := make(map[string]bool)
	for _, st := range sites {
		if meta := st.n.Schema; meta != "" && !seenMeta[meta] {
			seenMeta[meta] = true
			if missing := g.missingVocabularies(meta); len(missing) > 0 {
				return &UnknownVocabularyError{Location: st.loc, Metaschema: meta, Vocabulary: missing}
			}
		}
		if g.config.StrictKeywords {
			for _, k := range unknownKeywords(st.n) {
				unknown = append(unknown, UnknownKeyword{Location: st.loc, Keyword: k})
			}
		}
	}
	if len(unknown) > 0 {
		return &UnknownKeywordsError{Keywords: unknown}
	}
	return nil
}

// missingVocabularies lists, sorted, the vocabularies the metaschema meta
// requires that this generator does not implement: those of the metaschema
// meta names, not a $vocabulary the node states itself -- a metaschema's own
// $vocabulary is about the documents that name it, and is not a demand on it.
//
// A metaschema of draft 7 or earlier requires none: $vocabulary is a keyword
// from 2019-09 on, and no earlier dialect reads it, so the metaschema is not
// looked up at all.
func (g *Generator) missingVocabularies(meta string) []string {
	if d := schema.DetectDraft(&schema.Schema{Schema: meta}); d != schema.DraftUnknown && d <= schema.Draft07 {
		return nil
	}
	var missing []string
	// maporder: missing is sorted before it is read.
	for uri, required := range g.declaredVocabulary(&schema.Schema{Schema: meta}) {
		if required && !knownVocabularies[uri] {
			missing = append(missing, uri)
		}
	}
	sort.Strings(missing)
	return missing
}
