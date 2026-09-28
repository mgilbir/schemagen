package keywordgrid_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/gentest"
	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
	"github.com/mgilbir/schemagen/tests/internal/testsupport"
	"github.com/mgilbir/schemagen/tests/keywordgrid"
)

// The keyword grid harness. See tests/keywordgrid for what the grid is.
//
// Every cell is generated twice -- by the static generator, as a caller gets
// it, and with the root forced through the runtime evaluator
// (gentest.ForceEvaluator) -- and each generated type decodes and validates
// every document of its cell. The verdicts are held to Bowtie's, frozen in
// testdata/grid/verdicts.json: a cell whose verdict is not recorded there fails,
// so the grid cannot grow a cell nobody has an answer for. `make grid-oracle`
// (TestKeywordGridOracle) records them.
//
// Where schemagen's verdict and Bowtie's differ, the difference is one of three
// things -- the static code gets it wrong, the evaluator does, or both -- and
// testdata/grid/known_failing.txt lists every one, by mode. The list is held
// both ways: a failure not on it fails the test, and so does a line whose cell
// now passes. The file only shrinks as fixes land, and a fix cannot land
// without the line it retires.
//
// `go test` runs a sample (gridSampled) chosen by a hash of each cell's key,
// so the sample is the same on every machine; `make grid`
// (SCHEMAGEN_GRID=full) runs every cell.

var (
	gridVerdictsFile = testsupport.RepoPath("testdata", "grid", "verdicts.json")
	gridFailingFile  = testsupport.RepoPath("testdata", "grid", "known_failing.txt")
)

const (
	// gridJudgedFloor is the least fraction of the grid's documents the
	// oracle must have judged. Bowtie's implementations disagreeing on a
	// document marks it unknown, which is honest; most of the grid being
	// unknown would be an oracle that is not answering.
	gridJudgedFloor = 0.95
	// gridSampleOneIn is the sampling rate outside the full run.
	gridSampleOneIn = 20
	// gridOtherDialectSampleOneIn is the same for the dialects but 2020-12.
	gridOtherDialectSampleOneIn = 100
)

// gridVerdictFile is testdata/grid/verdicts.json.
type gridVerdictFile struct {
	Dialects        []string          `json:"dialects"`
	Implementations []string          `json:"implementations"`
	Verdicts        map[string]string `json:"verdicts"` // "valid", "invalid", or "unknown"
}

func loadGridVerdicts(t *testing.T) gridVerdictFile {
	t.Helper()
	var f gridVerdictFile
	data, err := os.ReadFile(gridVerdictsFile)
	if err != nil {
		t.Fatalf("reading %s: %v (run `make grid-oracle` to record the grid's verdicts)", gridVerdictsFile, err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parsing %s: %v", gridVerdictsFile, err)
	}
	return f
}

// gridSampled reports whether a cell is in the sample outside the full run:
// under 2020-12, every root cell and a stable twentieth of the rest; under
// each other dialect, a stable hundredth, which is enough to notice a
// dialect-wide break without multiplying the time `go test` takes.
func gridSampled(c keywordgrid.Cell) bool {
	sum := sha256.Sum256([]byte(c.Key))
	h := binary.BigEndian.Uint32(sum[:4])
	if c.Dialect != keywordgrid.Dialects[0].Name {
		return h%gridOtherDialectSampleOneIn == 0
	}
	return c.Position == "root" || h%gridSampleOneIn == 0
}

func gridCells(full bool) []keywordgrid.Cell {
	var out []keywordgrid.Cell
	for _, c := range keywordgrid.Cells() {
		if full || gridSampled(c) {
			out = append(out, c)
		}
	}
	return out
}

// gridOutcome is what schemagen did with one document of one cell.
type gridOutcome string

const (
	gridValid     gridOutcome = "VALID"
	gridInvalid   gridOutcome = "INVALID" // Validate refused it
	gridDecode    gridOutcome = "DECODE"  // json.Unmarshal refused it
	gridPanic     gridOutcome = "PANIC"
	gridGenError  gridOutcome = "GENERR"
	gridGenPanic  gridOutcome = "GENPANIC"
	gridCompile   gridOutcome = "COMPILE"
	gridDeclined  gridOutcome = "DECLINED" // the evaluator refused the schema
	gridNoVerdict gridOutcome = "MISSING"
)

// gridGenerated is one cell's generated source for one mode, or why there is
// none.
type gridGenerated struct {
	cell    keywordgrid.Cell
	root    string
	code    string
	outcome gridOutcome // set when there is no code
	detail  string
}

func gridRootName(c keywordgrid.Cell) string { return "Cell" + c.Prefix }

// generateGridCell generates one cell, recovering a generator panic as the
// finding it is.
func generateGridCell(c keywordgrid.Cell, evaluator bool) (g gridGenerated) {
	g = gridGenerated{cell: c, root: gridRootName(c)}
	defer func() {
		if r := recover(); r != nil {
			g.outcome, g.detail = gridGenPanic, fmt.Sprint(r)
		}
	}()
	var s schema.Schema
	if err := json.Unmarshal(c.Schema, &s); err != nil {
		g.outcome, g.detail = gridGenError, "parse: "+err.Error()
		return g
	}
	s.Normalize()
	opts := []generator.GenerateOption{generator.WithRootTypeName(g.root)}
	if evaluator {
		opts = append(opts, gentest.ForceEvaluator().(generator.GenerateOption))
	}
	ir, err := generator.New(generator.Config{PackageName: "main", OmitEmpty: true}).Generate(&s, opts...)
	if err != nil {
		if evaluator && strings.Contains(err.Error(), "runtime evaluator declined") {
			g.outcome, g.detail = gridDeclined, err.Error()
		} else {
			g.outcome, g.detail = gridGenError, err.Error()
		}
		return g
	}
	em, err := emitter.New()
	if err != nil {
		g.outcome, g.detail = gridGenError, "emitter: "+err.Error()
		return g
	}
	src, err := em.Emit(ir)
	if err != nil {
		g.outcome, g.detail = gridGenError, "emit: "+err.Error()
		return g
	}
	g.code = string(src)
	return g
}

// gridDriver is the main program of one package of cells: it reads the
// documents, decodes each into its cell's root type and asks Validate, and
// prints one verdict per line. Each check runs under recover, so a panicking
// Validate is a verdict and not the end of the package.
func gridDriver(cells []gridGenerated) string {
	var b strings.Builder
	b.WriteString(`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type gridCase struct {
	Cell string          ` + "`json:\"cell\"`" + `
	I    int             ` + "`json:\"i\"`" + `
	Doc  json.RawMessage ` + "`json:\"doc\"`" + `
}

func judge[T any](doc []byte) (verdict string) {
	defer func() {
		if r := recover(); r != nil {
			verdict = "PANIC"
		}
	}()
	var v T
	if err := json.Unmarshal(doc, &v); err != nil {
		return "DECODE"
	}
	if x, ok := any(&v).(interface{ Validate() error }); ok {
		if err := x.Validate(); err != nil {
			return "INVALID"
		}
	}
	return "VALID"
}

var gridJudges = map[string]func([]byte) string{
`)
	for _, g := range cells {
		fmt.Fprintf(&b, "\t%q: judge[%s],\n", g.cell.Prefix, g.root)
	}
	b.WriteString(`}

func main() {
	raw, err := os.ReadFile("cases.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var cases []gridCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, c := range cases {
		fmt.Printf("%s %d %s\n", c.Cell, c.I, gridJudges[c.Cell](c.Doc))
	}
}
`)
	return b.String()
}

var gridCompileErrFile = regexp.MustCompile(`(?m)^\./(cell_G[0-9a-f]+)\.go:\d+`)

// runGridPackage builds and runs one package of generated cells and returns
// each document's outcome, keyed by cell prefix and document index. A cell
// whose code does not compile is taken out and marked, and the rest are built
// again without it; a document that kills the driver outright -- a fatal
// error recover cannot catch, such as a stack overflow -- is marked and the
// driver run again on the documents after it. It runs on a worker goroutine,
// so it reports trouble as an error rather than through t.
func runGridPackage(cells []gridGenerated) (map[string][]gridOutcome, []string, error) {
	out := make(map[string][]gridOutcome)
	var notes []string
	live := append([]gridGenerated(nil), cells...)
	for attempt := 0; len(live) > 0; attempt++ {
		dir, err := testgo.MkdirWorkTemp("schemagen-grid-")
		if err != nil {
			return nil, notes, fmt.Errorf("work dir: %w", err)
		}
		cleanup := func() { os.RemoveAll(dir) }
		var all strings.Builder
		for _, g := range live {
			if err := os.WriteFile(filepath.Join(dir, "cell_"+g.cell.Prefix+".go"), []byte(g.code), 0o644); err != nil {
				cleanup()
				return nil, notes, err
			}
			all.WriteString(g.code)
			all.WriteString("\n")
		}
		if err := testsupport.WriteSharedHelpersErr(dir, all.String()); err != nil {
			// A helper set the emitter cannot write is the package failing to
			// build, the same finding for every cell in it.
			for _, g := range live {
				out[g.cell.Prefix] = repeatOutcome(gridCompile, len(g.cell.Instances))
			}
			notes = append(notes, "helper file: "+err.Error())
			cleanup()
			return out, notes, nil
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(gridDriver(live)), 0o644); err != nil {
			cleanup()
			return nil, notes, err
		}
		if err := testsupport.WriteTestGoMod(dir, "grid"); err != nil {
			cleanup()
			return nil, notes, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		// -e: every error, not the first ten, so every broken cell is named
		// on the first attempt.
		build := testgo.Command(ctx, dir, "build", "-gcflags=-e", "-o", "grid.bin", ".")
		buildOut, buildErr := build.CombinedOutput()
		if buildErr != nil {
			cancel()
			cleanup()
			broken := map[string]bool{}
			for _, m := range gridCompileErrFile.FindAllStringSubmatch(string(buildOut), -1) {
				broken[strings.TrimPrefix(m[1], "cell_")] = true
			}
			if len(broken) == 0 || attempt > 8 {
				// The failure names no cell file: the helper file or the driver
				// does not compile with this set, which no cell can be blamed
				// for alone. Every cell is marked.
				for _, g := range live {
					out[g.cell.Prefix] = repeatOutcome(gridCompile, len(g.cell.Instances))
				}
				notes = append(notes, "package did not build and no cell file is named:\n"+firstLines(string(buildOut), 20))
				return out, notes, nil
			}
			var next []gridGenerated
			for _, g := range live {
				if broken[g.cell.Prefix] {
					out[g.cell.Prefix] = repeatOutcome(gridCompile, len(g.cell.Instances))
				} else {
					next = append(next, g)
				}
			}
			live = next
			continue
		}
		type gridCase struct {
			Cell string          `json:"cell"`
			I    int             `json:"i"`
			Doc  json.RawMessage `json:"doc"`
		}
		var pending []gridCase
		for _, g := range live {
			out[g.cell.Prefix] = repeatOutcome(gridNoVerdict, len(g.cell.Instances))
			for i, doc := range g.cell.Instances {
				pending = append(pending, gridCase{Cell: g.cell.Prefix, I: i, Doc: doc})
			}
		}
		for len(pending) > 0 {
			casesJSON, err := json.Marshal(pending)
			if err != nil {
				cancel()
				cleanup()
				return nil, notes, err
			}
			if err := os.WriteFile(filepath.Join(dir, "cases.json"), casesJSON, 0o644); err != nil {
				cancel()
				cleanup()
				return nil, notes, err
			}
			run := exec.CommandContext(ctx, filepath.Join(dir, "grid.bin"))
			run.Dir = dir
			runOut, runErr := run.Output()
			done := 0
			sc := bufio.NewScanner(bytes.NewReader(runOut))
			for sc.Scan() {
				f := strings.Fields(sc.Text())
				if len(f) != 3 {
					cancel()
					cleanup()
					return nil, notes, fmt.Errorf("grid driver printed %q", sc.Text())
				}
				i, err := strconv.Atoi(f[1])
				if err != nil || i >= len(out[f[0]]) {
					cancel()
					cleanup()
					return nil, notes, fmt.Errorf("grid driver printed %q", sc.Text())
				}
				out[f[0]][i] = gridOutcome(f[2])
				done++
			}
			if runErr == nil {
				break
			}
			if done >= len(pending) {
				cancel()
				cleanup()
				return nil, notes, fmt.Errorf("grid driver failed after answering every document: %w", runErr)
			}
			// The document after the last answered one killed the process.
			crashed := pending[done]
			out[crashed.Cell][crashed.I] = gridPanic
			notes = append(notes, fmt.Sprintf("cell %s document %d killed the driver: %v", crashed.Cell, crashed.I, runErr))
			pending = pending[done+1:]
		}
		cancel()
		cleanup()
		return out, notes, nil
	}
	return out, notes, nil
}

func repeatOutcome(o gridOutcome, n int) []gridOutcome {
	out := make([]gridOutcome, n)
	for i := range out {
		out[i] = o
	}
	return out
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = append(lines[:n], "...")
	}
	return strings.Join(lines, "\n")
}

// gridFailure is one document of one cell where schemagen's verdict is not the
// oracle's, as a line of known_failing.txt.
type gridFailure struct {
	mode, key string
	i         int
	what      string
}

func (f gridFailure) line() string {
	return fmt.Sprintf("%s\t%s\t%d\t%s", f.mode, f.key, f.i, f.what)
}

// gridExpected is the verdict a document is held to: Bowtie's where its
// implementations agree, and the construction's where they do not and the
// construction decides one -- two independent derivations, the second standing
// in only where the first abstains. "" where neither says anything.
func gridExpected(c keywordgrid.Cell, i int, oracle map[string]string) string {
	want := oracle[keywordgrid.VerdictKey(c.Content, c.Instances[i])]
	if want == "valid" || want == "invalid" {
		return want
	}
	if c.Constructed[i] != nil {
		return map[bool]string{true: "valid", false: "invalid"}[*c.Constructed[i]]
	}
	return ""
}

// judgeGrid compares one mode's outcomes with the expected verdicts.
func judgeGrid(mode string, cells []keywordgrid.Cell, outcomes map[string][]gridOutcome, oracle map[string]string) []gridFailure {
	var fails []gridFailure
	for _, c := range cells {
		got := outcomes[c.Prefix]
		for i := range c.Instances {
			want := gridExpected(c, i, oracle)
			if want == "" {
				continue
			}
			o := gridNoVerdict
			if i < len(got) {
				o = got[i]
			}
			var what string
			switch o {
			case gridValid:
				if want == "invalid" {
					what = "accepts-invalid"
				}
			case gridInvalid, gridDecode:
				if want == "valid" {
					what = "rejects-valid"
				}
			default:
				what = strings.ToLower(string(o))
			}
			if what != "" {
				fails = append(fails, gridFailure{mode: mode, key: c.Key, i: i, what: what})
			}
		}
	}
	return fails
}

func readGridFailing(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(gridFailingFile)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bool{}
	}
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		known[line] = true
	}
	return known
}

const gridFailingHeader = `# The keyword grid's known failures: every document of every cell where
# schemagen's verdict is not Bowtie's, by mode -- "static" is the generated
# code a caller gets, "evaluator" the runtime evaluator forced over the root.
# Held both ways by TestKeywordGrid: a failure not listed fails the test, and
# so does a listed line whose cell now passes. Fixes remove lines; nothing
# adds one without the test saying so.
#
# mode <TAB> dialect/row/position/composition <TAB> document (0 valid, 1 invalid, 2 other kind) <TAB> what happened
`

// TestKeywordGrid runs the grid. See the comment at the top of this file.
func TestKeywordGrid(t *testing.T) {
	full := os.Getenv("SCHEMAGEN_GRID") == "full"
	oracle := loadGridVerdicts(t)
	cells := gridCells(full)
	if len(cells) == 0 {
		t.Fatal("the grid built no cells")
	}

	// Every document needs a recorded verdict. A cell nobody has asked the
	// oracle about is one this test would pass by comparing with nothing.
	var missing []string
	judged, total := 0, 0
	for _, c := range keywordgrid.Cells() {
		for _, inst := range c.Instances {
			total++
			switch oracle.Verdicts[keywordgrid.VerdictKey(c.Content, inst)] {
			case "":
				missing = append(missing, c.Key)
			case "valid", "invalid":
				judged++
			}
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d grid documents have no recorded verdict (first: %s); run `make grid-oracle`",
			len(missing), missing[0])
	}
	if frac := float64(judged) / float64(total); frac < gridJudgedFloor {
		t.Fatalf("the oracle judged %d of %d grid documents (%.1f%%), under the %.0f%% floor: "+
			"implementations disagreeing on most of the grid is an oracle that is not answering",
			judged, total, 100*frac, 100*gridJudgedFloor)
	}

	// The construction is a check on the oracle and on the grid itself: where
	// the position and composition preserve a row's verdict, Bowtie has to
	// give it. A disagreement is a mis-written row, not a schemagen defect.
	for _, c := range cells {
		for i, inst := range c.Instances {
			if c.Constructed[i] == nil {
				continue
			}
			want := "invalid"
			if *c.Constructed[i] {
				want = "valid"
			}
			if got := oracle.Verdicts[keywordgrid.VerdictKey(c.Content, inst)]; got != "unknown" && got != want {
				t.Errorf("grid construction: %s document %d (%s) is %s by construction and %s by the oracle",
					c.Key, i, inst, want, got)
			}
		}
	}

	var fails []gridFailure
	for _, mode := range []string{"static", "evaluator"} {
		outcomes := runGridMode(t, cells, mode == "evaluator", full)
		fails = append(fails, judgeGrid(mode, cells, outcomes, oracle.Verdicts)...)
	}

	got := map[string]bool{}
	for _, f := range fails {
		got[f.line()] = true
	}
	ran := map[string]bool{}
	for _, c := range cells {
		ran[c.Key] = true
	}
	checkLedgerSeesGridFailures(t, cells, fails)
	if os.Getenv("SCHEMAGEN_GRID_UPDATE") == "1" {
		if !full {
			t.Fatal("SCHEMAGEN_GRID_UPDATE=1 rewrites the whole list, so it needs the whole grid: set SCHEMAGEN_GRID=full")
		}
		lines := make([]string, 0, len(got))
		for l := range got {
			lines = append(lines, l)
		}
		sort.Strings(lines)
		if err := os.WriteFile(gridFailingFile, []byte(gridFailingHeader+strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d known failures to %s", len(lines), gridFailingFile)
		return
	}
	known := readGridFailing(t)
	var unexpected, fixed []string
	for l := range got {
		if !known[l] {
			unexpected = append(unexpected, l)
		}
	}
	for l := range known {
		key := strings.Split(l, "\t")[1]
		if ran[key] && !got[l] {
			fixed = append(fixed, l)
		}
	}
	sort.Strings(unexpected)
	sort.Strings(fixed)
	for _, l := range unexpected {
		t.Errorf("new grid failure (not in %s):\n\t%s", gridFailingFile, l)
	}
	for _, l := range fixed {
		t.Errorf("listed grid failure no longer fails -- remove it from %s:\n\t%s", gridFailingFile, l)
	}
	t.Logf("keyword grid: %d cells (%s), %d documents failing across both modes", len(cells),
		map[bool]string{true: "full", false: "sample"}[full], len(got))
}

// ledgerBlindCells are the grid cells whose generated code accepts a document
// the schema rejects and where the keyword ledger reports nothing, each with
// why the ledger cannot see it. checkLedgerSeesGridFailures holds the list both
// ways.
//
// It is empty, and an entry needs a reason the ledger cannot observe the gap
// from the IR at all.
var ledgerBlindCells = map[string]string{}

// checkLedgerSeesGridFailures is the keyword ledger held to the grid. Every
// cell whose static code accepts a document the schema rejects is one where
// some assertion is not enforced, so the ledger, run over the same schema, has
// to report something -- or the cell is in ledgerBlindCells, saying why it
// cannot. A ledger that is quiet there is crediting a check with a keyword the
// check does not carry, which is the one thing it must not do.
func checkLedgerSeesGridFailures(t *testing.T, cells []keywordgrid.Cell, fails []gridFailure) {
	t.Helper()
	underEnforced := map[string]bool{}
	for _, f := range fails {
		if f.mode == "static" && f.what == "accepts-invalid" {
			underEnforced[f.key] = true
		}
	}
	for _, c := range cells {
		reason, blind := ledgerBlindCells[c.Key]
		if !underEnforced[c.Key] {
			if blind {
				t.Errorf("grid cell %s is listed as one the ledger cannot see (%s), and its code no longer accepts an invalid document -- remove it from ledgerBlindCells", c.Key, reason)
			}
			continue
		}
		var s schema.Schema
		if err := json.Unmarshal(c.Schema, &s); err != nil {
			t.Fatalf("%s: %v", c.Key, err)
		}
		s.Normalize()
		g := generator.New(generator.Config{PackageName: "main", OmitEmpty: true})
		if _, err := g.Generate(&s, generator.WithRootTypeName(gridRootName(c))); err != nil {
			continue
		}
		reported := len(g.Unclaimed()) > 0
		switch {
		case !reported && !blind:
			t.Errorf("grid cell %s accepts a document its schema rejects, and the keyword ledger reports nothing for %s", c.Key, c.Schema)
		case reported && blind:
			t.Errorf("grid cell %s is listed as one the ledger cannot see (%s), and the ledger now reports it -- remove it from ledgerBlindCells", c.Key, reason)
		}
	}
}

// runGridMode generates every cell in one mode and runs them, one package per
// position and composition in the full run and one package for the sample.
func runGridMode(t *testing.T, cells []keywordgrid.Cell, evaluator, full bool) map[string][]gridOutcome {
	t.Helper()
	outcomes := make(map[string][]gridOutcome)
	groups := map[string][]gridGenerated{}
	var order []string
	for _, c := range cells {
		g := generateGridCell(c, evaluator)
		if g.code == "" {
			outcomes[c.Prefix] = repeatOutcome(g.outcome, len(c.Instances))
			continue
		}
		key := "sample"
		if full {
			key = c.Position + "/" + c.Composition
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], g)
	}
	var mu sync.Mutex
	var errs, notes []string
	sem := make(chan struct{}, max(1, runtime.NumCPU()/2))
	var wg sync.WaitGroup
	for _, key := range order {
		wg.Add(1)
		sem <- struct{}{}
		go func(key string) {
			defer wg.Done()
			defer func() { <-sem }()
			res, n, err := runGridPackage(groups[key])
			mu.Lock()
			defer mu.Unlock()
			for _, note := range n {
				notes = append(notes, key+": "+note)
			}
			if err != nil {
				errs = append(errs, key+": "+err.Error())
				return
			}
			for k, v := range res {
				outcomes[k] = v
			}
		}(key)
	}
	wg.Wait()
	sort.Strings(notes)
	for _, n := range notes {
		t.Log(n)
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		t.Fatalf("the grid harness failed:\n%s", strings.Join(errs, "\n"))
	}
	return outcomes
}

// TestKeywordGridOracle records Bowtie's verdict on every grid document in
// testdata/grid/verdicts.json. It runs only when asked (`make grid-oracle`,
// SCHEMAGEN_GRID_ORACLE=1), because it drives containers through uvx; the
// verdicts it writes are what TestKeywordGrid reads on every run.
func TestKeywordGridOracle(t *testing.T) {
	if os.Getenv("SCHEMAGEN_GRID_ORACLE") != "1" {
		t.Skip("set SCHEMAGEN_GRID_ORACLE=1 (make grid-oracle) to record the grid's verdicts with Bowtie")
	}
	if _, err := exec.LookPath("uvx"); err != nil {
		t.Fatalf("SCHEMAGEN_GRID_ORACLE=1 but uvx is not on PATH (%v); the oracle is Bowtie driven through uvx", err)
	}
	implList := os.Getenv("SCHEMAGEN_GRID_IMPLS")
	if implList == "" {
		implList = "js-ajv,python-jsonschema,go-jsonschema,rust-boon"
	}
	impls := strings.Split(implList, ",")
	file := gridVerdictFile{Implementations: impls, Verdicts: map[string]string{}}
	unknown := 0
	for _, dl := range keywordgrid.Dialects {
		var cells []keywordgrid.Cell
		for _, c := range keywordgrid.Cells() {
			if c.Dialect == dl.Name {
				cells = append(cells, c)
			}
		}
		file.Dialects = append(file.Dialects, dl.URI)
		var voting []string
		for _, impl := range impls {
			if reason, out := gridOracleExcluded[dl.Name][impl]; out {
				t.Logf("%s does not vote under %s: %s", impl, dl.Name, reason)
				continue
			}
			voting = append(voting, impl)
		}
		unknown += recordGridVerdicts(t, dl, voting, cells, file.Verdicts)
	}
	data, err := json.MarshalIndent(file, "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(gridVerdictsFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gridVerdictsFile, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("recorded %d verdicts (%d unknown) from %s", len(file.Verdicts), unknown, strings.Join(impls, ", "))
}

// gridOracleExcluded names, per dialect, an implementation whose answers the
// oracle does not count, and why. Each is a departure from the dialect's
// specification the JSON Schema Test Suite itself records -- js-ajv, run
// through Bowtie, calls the suite's draft 6 and 7 "ref overrides any sibling
// keywords" document invalid -- not a disagreement the grid found
// inconvenient: every other disagreement leaves the document unknown.
var gridOracleExcluded = map[string]map[string]string{
	"draft7": {"js-ajv": "applies the keywords beside a $ref, which draft 7 says the $ref replaces"},
	"draft6": {"js-ajv": "applies the keywords beside a $ref, which draft 6 says the $ref replaces"},
	"draft4": {"js-ajv": "has no draft 4"},
}

// recordGridVerdicts asks Bowtie for its verdict on every document of cells,
// which are all of dialect dl, records each into verdicts, and returns how
// many came out unknown.
func recordGridVerdicts(t *testing.T, dl keywordgrid.Dialect, impls []string, cells []keywordgrid.Cell, verdicts map[string]string) int {
	t.Helper()
	var in bytes.Buffer
	type bowtieTest struct {
		Description string          `json:"description"`
		Instance    json.RawMessage `json:"instance"`
	}
	for _, c := range cells {
		tests := make([]bowtieTest, len(c.Instances))
		for i, inst := range c.Instances {
			tests[i] = bowtieTest{Description: strconv.Itoa(i), Instance: inst}
		}
		line, err := json.Marshal(map[string]any{"description": c.Key, "schema": c.Content, "tests": tests})
		if err != nil {
			t.Fatal(err)
		}
		in.Write(line)
		in.WriteByte('\n')
	}
	dir := t.TempDir()
	casesPath := filepath.Join(dir, "cases.jsonl")
	if err := os.WriteFile(casesPath, in.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"--from", "bowtie-json-schema", "bowtie", "run", "--dialect", dl.URI}
	for _, impl := range impls {
		args = append(args, "-i", impl)
	}
	args = append(args, casesPath)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "uvx", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		t.Fatalf("bowtie: %v\n%s", err, firstLines(stderr.String(), 30))
	}

	// seq is 1-based and follows the input order.
	votes := make([][]map[string]bool, len(cells))
	for i, c := range cells {
		votes[i] = make([]map[string]bool, len(c.Instances))
		for j := range votes[i] {
			votes[i][j] = map[string]bool{}
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	responded := map[string]int{}
	for sc.Scan() {
		var entry struct {
			Seq            int    `json:"seq"`
			Implementation string `json:"implementation"`
			Results        []struct {
				Valid *bool `json:"valid"`
			} `json:"results"`
		}
		if err := json.Unmarshal(sc.Bytes(), &entry); err != nil || entry.Implementation == "" || entry.Seq < 1 || entry.Seq > len(cells) {
			continue
		}
		responded[entry.Implementation]++
		for j, r := range entry.Results {
			if r.Valid != nil && j < len(votes[entry.Seq-1]) {
				votes[entry.Seq-1][j][entry.Implementation] = *r.Valid
			}
		}
	}
	answering := 0
	for _, impl := range impls {
		if responded[impl] > 0 {
			answering++
		} else {
			t.Logf("implementation %s answered no %s case", impl, dl.Name)
		}
	}
	// An implementation may not support a dialect (ajv has no draft 4); the
	// rest still have to be enough to be an oracle.
	if answering < max(2, len(impls)-1) {
		t.Fatalf("only %d of %d implementations answered under %s; the oracle would be one opinion fewer than it says\n%s",
			answering, len(impls), dl.Name, firstLines(stderr.String(), 30))
	}

	unknown := 0
	for i, c := range cells {
		for j, inst := range c.Instances {
			v := votes[i][j]
			verdict := "unknown"
			// A verdict needs every implementation that answered to agree, and
			// all but at most one to have answered: an implementation can refuse
			// a schema outright (ajv refuses a format it has no validator for),
			// which is no vote either way, but an oracle of one voice is not an
			// oracle.
			if len(v) >= max(2, len(impls)-1) {
				agreed, first := true, true
				var value bool
				for _, impl := range impls {
					answer, ok := v[impl]
					if !ok {
						continue
					}
					if first {
						value, first = answer, false
					} else if answer != value {
						agreed = false
					}
				}
				if agreed {
					verdict = map[bool]string{true: "valid", false: "invalid"}[value]
				}
			}
			if verdict == "unknown" {
				unknown++
				t.Logf("unknown: %s document %d %s: %v", c.Key, j, inst, v)
			}
			verdicts[keywordgrid.VerdictKey(c.Content, inst)] = verdict
		}
	}
	return unknown
}
