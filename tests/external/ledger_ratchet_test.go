package external

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
	"github.com/mgilbir/schemagen/tests/internal/testsupport"
)

// The keyword ledger's findings over every schema this repository generates --
// the corpus under testdata/schemas and, where the JSON Schema Test Suite is on
// disk, every group of it -- held in testdata/ledger/unclaimed.txt.
//
// Every line is a keyword some schema states that the generated code does not
// carry: a document the schema rejects for that keyword alone, which the
// generated Validate accepts. The list is today's, frozen, and held both ways.
// A finding not on it fails the test -- a change that stops enforcing
// something has to say so here. A line whose finding is gone fails too -- a fix
// retires its lines in the same change, so the file only shrinks, and what is
// left on it is the work that remains.
//
// The corpus half runs on every `go test`. The suite half runs where the suite
// is present (make download-test-suite), and its lines are only held then. It
// lives in this package because the suite half generates each group exactly as
// TestExternalValidation does, with the same configuration and resolver.
// `make ledger-update` rewrites the file from the tree.

var ledgerUnclaimedFile = testsupport.RepoPath("testdata", "ledger", "unclaimed.txt")

const ledgerUnclaimedHeader = `# The keyword ledger's findings: every assertion a schema of the corpus
# (testdata/schemas) or of the JSON Schema Test Suite states that the code
# schemagen generates for it does not enforce. Held both ways by
# TestLedgerRatchet: a new finding fails, and so does a line whose finding is
# gone. Fixes remove lines; nothing adds one without the test saying so.
# ` + "`make ledger-update`" + ` rewrites it.
#
# source <TAB> schema <TAB> where the keyword is written <TAB> keyword
`

// ledgerLine is one finding as the file writes it. The declaration and the
// reason are left out: the first is a generated name, which a naming change
// moves without the finding moving, and the second is prose.
func ledgerLine(source, schemaName string, e generator.LedgerEntry) string {
	return fmt.Sprintf("%s\t%s\t%s\t%s", source, schemaName, e.Location, e.Keyword)
}

// ledgerCorpusFindings generates every schema of the corpus the way the golden
// tests do and collects the ledger's findings.
func ledgerCorpusFindings(t *testing.T) map[string]bool {
	t.Helper()
	root := testsupport.SchemaDir
	var files []string
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".json") {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	out := map[string]bool{}
	generated := 0
	for _, f := range files {
		s, err := schema.LoadFromFile(f)
		if err != nil {
			continue
		}
		s.Normalize()
		abs, err := filepath.Abs(f)
		if err != nil {
			t.Fatal(err)
		}
		gen := generator.New(generator.Config{PackageName: "testpkg", OmitEmpty: true, Resolver: schema.NewFileResolver(filepath.Dir(abs))})
		if _, err := gen.Generate(s); err != nil {
			// A schema the generator refuses has nothing generated to hold
			// to anything; whether it should generate is other tests' business.
			continue
		}
		generated++
		rel, _ := filepath.Rel(root, f)
		for _, e := range gen.Unclaimed() {
			out[ledgerLine("corpus", filepath.ToSlash(rel), e)] = true
		}
	}
	if generated < 500 {
		t.Fatalf("only %d corpus schemas generated; the walk has stopped seeing the corpus", generated)
	}
	return out
}

// ledgerSuiteFindings generates every group of the JSON Schema Test Suite as
// TestExternalValidation does and collects the ledger's findings, and reports
// on the groups whose root Validate is `return nil`: whether the ledger has
// something to say about each or the schema asks for nothing the Go type does
// not already refuse.
func ledgerSuiteFindings(t *testing.T) (map[string]bool, bool) {
	t.Helper()
	if _, err := os.Stat(jstsBaseDir); err != nil {
		return nil, false
	}
	resolver := remotesResolver(t)
	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	var returnNil, returnNilExplained int
	explainedBy := map[string]int{}
	var unexplained []string
	for _, draft := range allDrafts {
		draftDir := filepath.Join(jstsBaseDir, draft)
		for _, file := range listJSONFiles(t, draftDir) {
			for _, group := range loadTestGroups(t, filepath.Join(draftDir, file)) {
				if !isCodeGenSuitable(group.Schema) {
					continue
				}
				var s schema.Schema
				trimmed := strings.TrimSpace(string(group.Schema))
				if trimmed == "false" {
					no := false
					s.BooleanSchema = &no
				} else if err := json.Unmarshal(group.Schema, &s); err != nil {
					continue
				}
				s.Normalize()
				gen := generator.New(externalCaseConfig(resolver, draft, file))
				ir, err := gen.Generate(&s)
				if err != nil {
					continue
				}
				name := draft + "/" + filenameWithoutExt(file) + "/" + group.Description
				entries := gen.Unclaimed()
				for _, e := range entries {
					out[ledgerLine("jsts", name, e)] = true
				}
				src, err := em.Emit(ir)
				if err != nil {
					continue
				}
				code := string(src)
				if !testsupport.HasValidateMethod(code) || !rootValidateHasNoChecks(code) {
					continue
				}
				returnNil++
				if len(entries) > 0 {
					returnNilExplained++
					seen := map[string]bool{}
					for _, e := range entries {
						if !seen[e.Keyword] {
							seen[e.Keyword] = true
							explainedBy[e.Keyword]++
						}
					}
				} else {
					unexplained = append(unexplained, name)
				}
			}
		}
	}
	keys := make([]string, 0, len(explainedBy))
	for k := range explainedBy {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if explainedBy[keys[i]] != explainedBy[keys[j]] {
			return explainedBy[keys[i]] > explainedBy[keys[j]]
		}
		return keys[i] < keys[j]
	})
	var by []string
	for _, k := range keys {
		by = append(by, fmt.Sprintf("%s %d", k, explainedBy[k]))
	}
	t.Logf("suite groups whose root Validate is `return nil`: %d; the ledger reports an unenforced keyword in %d (by keyword: %s); "+
		"in the other %d nothing the schema asserts is left to Validate", returnNil, returnNilExplained, strings.Join(by, ", "), len(unexplained))
	for _, u := range unexplained {
		t.Logf("  return nil, nothing unclaimed: %s", u)
	}
	return out, true
}

// TestLedgerRatchet holds testdata/ledger/unclaimed.txt. See the comment at
// the top of this file.
func TestLedgerRatchet(t *testing.T) {
	got := ledgerCorpusFindings(t)
	suite, haveSuite := ledgerSuiteFindings(t)
	for l := range suite {
		got[l] = true
	}
	if os.Getenv("SCHEMAGEN_LEDGER_UPDATE") == "1" {
		if !haveSuite {
			t.Fatal("SCHEMAGEN_LEDGER_UPDATE=1 rewrites the whole file, so it needs the suite: run make download-test-suite")
		}
		lines := make([]string, 0, len(got))
		for l := range got {
			lines = append(lines, l)
		}
		sort.Strings(lines)
		if err := os.MkdirAll(filepath.Dir(ledgerUnclaimedFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ledgerUnclaimedFile, []byte(ledgerUnclaimedHeader+strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d findings to %s", len(lines), ledgerUnclaimedFile)
		return
	}
	data, err := os.ReadFile(ledgerUnclaimedFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		known[line] = true
	}
	var added, gone []string
	for l := range got {
		if !known[l] {
			added = append(added, l)
		}
	}
	for l := range known {
		if strings.HasPrefix(l, "jsts\t") && !haveSuite {
			continue
		}
		if !got[l] {
			gone = append(gone, l)
		}
	}
	sort.Strings(added)
	sort.Strings(gone)
	for _, l := range added {
		t.Errorf("new ledger finding (not in %s):\n\t%s", ledgerUnclaimedFile, l)
	}
	for _, l := range gone {
		t.Errorf("ledger finding no longer reported -- remove it from %s:\n\t%s", ledgerUnclaimedFile, l)
	}
	if !haveSuite {
		t.Logf("the JSON Schema Test Suite is not on disk, so its lines were not checked (make download-test-suite)")
	}
}
