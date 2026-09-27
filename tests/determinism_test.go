package tests

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/importer"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// The dynamic half of the determinism gate: the same schema, generated again,
// is the same file, the same helper file, the same warnings and the same error.
//
// It runs in one process, and that is enough to vary what it has to vary. Go
// does not fix a map's iteration order per process: every map gets its own hash
// seed when it is made, and every range statement starts from a random position
// of its own, so two Generate calls in one process walk every map they build in
// unrelated orders. What a process-wide seed would add -- state computed once
// and cached across calls -- this generator does not have (its package-level
// maps are read-only tables, and a range over one is randomised per range like
// any other); TestCLIOutputIsDeterministicAcrossProcesses in the root package
// runs the built binary as separate processes for the part a process boundary
// can reach, the CLI's own warnings among it.
//
// What varies is not the same for every map, though, and the gate is built
// around that. A Go map of up to eight members iterates in one of only as many
// orders as it has members -- a rotation of one fixed sequence -- so a loop
// over two members repeats its order about four times in five, and a
// nondeterministic site driven only by a two-member map would pass three runs
// most of the time. The corpus runs every schema under every configuration
// determinismRuns times, which catches a site any schema drives with a large
// map; testdata/schemas/determinism holds one schema per site this class has
// had, each driving it with ten or more members, where two runs in a row agree
// about once in a thousand, and those run determinismFixtureRuns times. The
// static half, TestNoMapOrderReachesTheGenerator, is what does not depend on a
// schema reaching the site at all.

const (
	determinismRuns        = 3
	determinismFixtureRuns = 8
	determinismFixtureDir  = "determinism"
	// determinismFullRuns is the run count for every schema under
	// SCHEMAGEN_DETERMINISM_FULL (make test-determinism), which also adds the
	// schema of every group of the JSON Schema Test Suite when it is present.
	determinismFullRuns = 6
)

// determinismFull reports whether the full sweep was asked for.
func determinismFull() bool { return os.Getenv("SCHEMAGEN_DETERMINISM_FULL") != "" }

// determinismConfig is one point of the configuration matrix the corpus is
// generated under. Each one changes which templates and generator arms a schema
// reaches.
type determinismConfig struct {
	name string
	cfg  generator.Config
}

func determinismConfigs() []determinismConfig {
	base := generator.Config{
		PackageName: "gen",
		OutputDir:   ".",
		OmitEmpty:   true,
		Validation:  generator.ValidationModeStatic,
	}
	hybrid := base
	hybrid.Validation = generator.ValidationModeHybrid
	runtime := base
	runtime.Validation = generator.ValidationModeRuntime
	runtime.ExactNumbers = true
	runtime.RawUntyped = true
	strict := base
	strict.StrictProperties = true
	strict.StrictReadWrite = true
	strict.BigIntSupport = true
	strict.FormatAssertion = true
	strict.LenientRefs = true
	noOmit := base
	noOmit.OmitEmpty = false
	return []determinismConfig{
		{"static", base},
		{"hybrid", hybrid},
		{"runtime+exact+raw", runtime},
		{"strict+bigint+formats+lenient", strict},
		{"no-omitempty", noOmit},
	}
}

// generationTranscript is everything one generation lets a caller observe.
type generationTranscript struct {
	src, helpers []byte
	// undeclared is set when the file names types it does not declare, which
	// --lenient-refs is documented to produce for a reference it could not
	// resolve (Generator.UndeclaredRefTypes, warned about by the CLI): such a
	// package does not compile, so it cannot be type-checked either.
	undeclared bool
	// report is the error, if any, and every diagnostic the generator exposes,
	// rendered as text. fmt prints a map in key order, so a map-valued
	// diagnostic compares by content.
	report string
}

func (a generationTranscript) equal(b generationTranscript) bool {
	return bytes.Equal(a.src, b.src) && bytes.Equal(a.helpers, b.helpers) && a.report == b.report
}

// generateTranscript runs one schema through the pipeline under cfg -- load,
// normalise, generate, emit, helper file -- and records what came out.
func generateTranscript(em *emitter.Emitter, path string, cfg generator.Config) generationTranscript {
	var tr generationTranscript
	s, err := schema.LoadFromFile(path)
	if err != nil {
		tr.report = "load: " + err.Error()
		return tr
	}
	s.NormalizeForDraft(cfg.Draft)
	s.ComputeBaseURIs(nil, s)
	abs, err := filepath.Abs(path)
	if err != nil {
		tr.report = "abs: " + err.Error()
		return tr
	}
	cfg.Resolver = schema.NewCompositeResolver(schema.NewFileResolver(filepath.Dir(abs)))
	gen := generator.New(cfg)
	ir, err := gen.Generate(s)
	var b strings.Builder
	if err != nil {
		fmt.Fprintf(&b, "error: %v\n", err)
	}
	fmt.Fprintf(&b, "unenforced: %+v\n", gen.UnenforcedSchemas())
	fmt.Fprintf(&b, "unresolved: %q %v\n", gen.UnresolvedRefs(), gen.UnresolvedRefKeywords())
	fmt.Fprintf(&b, "undeclared: %+v\n", gen.UndeclaredRefTypes())
	tr.undeclared = len(gen.UndeclaredRefTypes()) > 0
	fmt.Fprintf(&b, "unsatisfiable: %+v\n", gen.UnsatisfiableRequiredProperties())
	fmt.Fprintf(&b, "applied: %v\n", gen.AppliedOverrides())
	fmt.Fprintf(&b, "reminted: %+v\n", gen.RemintedInFlight())
	if err == nil && ir != nil {
		src, err := em.Emit(ir)
		if err != nil {
			fmt.Fprintf(&b, "emit: %v\n", err)
		} else {
			tr.src = src
			helpers, has, err := em.EmitHelpers(cfg.PackageName, generator.HelpersReferencedBy(string(src)))
			switch {
			case err != nil:
				fmt.Fprintf(&b, "helpers: %v\n", err)
			case has:
				tr.helpers = helpers
			}
		}
	}
	tr.report = b.String()
	return tr
}

// corpusJob is one schema under one configuration.
type corpusJob struct {
	path string
	// label names the schema in a report: its path, or the suite group it was
	// read from.
	label string
	cfg   determinismConfig
	// first is the transcript of the first generation, which the map-order
	// guard over generated code reads as well.
	first generationTranscript
	// mismatch describes how a later run differed from the first, if one did.
	mismatch string
}

var (
	corpusRunOnce sync.Once
	corpusJobs    []*corpusJob
)

// determinismCorpus generates every corpus schema under every configuration,
// as many times as its directory asks for, and reports each job. It runs once
// per test binary: the map-order guard over generated code reads the same
// first generations the determinism test compared.
func determinismCorpus(t *testing.T) []*corpusJob {
	t.Helper()
	corpusRunOnce.Do(func() {
		paths := corpusSchemaPaths(t)
		sort.Strings(paths)
		labels := make(map[string]string, len(paths))
		for _, p := range paths {
			labels[p] = p
		}
		if determinismFull() {
			// Each suite group's schema, written to a file of its own so that it
			// goes through the same load the corpus does. The files are needed
			// only while this function runs, which is inside the calling test.
			dir := t.TempDir()
			_, external, err := fuzzSeedCorpus(func(origin string, raw []byte) {
				if !strings.HasPrefix(origin, jstsBaseDir) {
					return
				}
				p := filepath.Join(dir, fmt.Sprintf("group%05d.json", len(labels)))
				if err := os.WriteFile(p, raw, 0o644); err != nil {
					panic(err)
				}
				paths = append(paths, p)
				labels[p] = origin
			})
			if err != nil {
				t.Fatal(err)
			}
			if external == 0 {
				t.Fatalf("SCHEMAGEN_DETERMINISM_FULL is set and no JSON Schema Test Suite groups were found under %s; run make download-test-suite", jstsBaseDir)
			}
		}
		for _, p := range paths {
			for _, c := range determinismConfigs() {
				corpusJobs = append(corpusJobs, &corpusJob{path: p, label: labels[p], cfg: c})
			}
		}
		work := make(chan *corpusJob)
		var wg sync.WaitGroup
		for w := 0; w < runtime.GOMAXPROCS(0); w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// One emitter per worker: it is cheap, and it keeps the
				// question of whether one can be shared out of this test.
				em, err := emitter.New()
				if err != nil {
					panic(err)
				}
				for job := range work {
					runs := determinismRuns
					switch {
					case filepath.Base(filepath.Dir(job.path)) == determinismFixtureDir:
						runs = determinismFixtureRuns
					case determinismFull():
						runs = determinismFullRuns
					}
					job.first = generateTranscript(em, job.path, job.cfg.cfg)
					for i := 1; i < runs && job.mismatch == ""; i++ {
						again := generateTranscript(em, job.path, job.cfg.cfg)
						if !again.equal(job.first) {
							job.mismatch = describeTranscriptDiff(job.first, again, i+1)
						}
					}
				}
			}()
		}
		for _, job := range corpusJobs {
			work <- job
		}
		close(work)
		wg.Wait()
	})
	return corpusJobs
}

func describeTranscriptDiff(a, b generationTranscript, run int) string {
	for _, part := range []struct {
		name string
		x, y string
	}{{"source", string(a.src), string(b.src)}, {"helper file", string(a.helpers), string(b.helpers)}, {"diagnostics", a.report, b.report}} {
		if part.x == part.y {
			continue
		}
		xl, yl := strings.Split(part.x, "\n"), strings.Split(part.y, "\n")
		for i := 0; i < len(xl) || i < len(yl); i++ {
			var l1, l2 string
			if i < len(xl) {
				l1 = xl[i]
			}
			if i < len(yl) {
				l2 = yl[i]
			}
			if l1 != l2 {
				return fmt.Sprintf("run %d: %s first differs at line %d:\n\t\trun 1: %s\n\t\trun %d: %s", run, part.name, i+1, l1, run, l2)
			}
		}
	}
	return fmt.Sprintf("run %d differs", run)
}

func TestGenerationIsDeterministic(t *testing.T) {
	jobs := determinismCorpus(t)
	fixtures, generated := 0, 0
	var bad []string
	for _, job := range jobs {
		if filepath.Base(filepath.Dir(job.path)) == determinismFixtureDir {
			fixtures++
		}
		if job.first.src != nil {
			generated++
		}
		if job.mismatch != "" {
			bad = append(bad, fmt.Sprintf("%s [%s]: %s", job.label, job.cfg.name, job.mismatch))
		}
	}
	// A corpus that stopped generating, or lost the fixtures written for this
	// gate, would pass for the wrong reason.
	if generated < 1000 {
		t.Fatalf("only %d of %d generations produced a file; the gate is measuring nothing", generated, len(jobs))
	}
	if fixtures == 0 {
		t.Fatalf("no schemas under testdata/schemas/%s: the fixtures that drive each known site with enough members to vary are gone", determinismFixtureDir)
	}
	t.Logf("%d generations (%d files) compared across runs, %d of them determinism fixtures", len(jobs), generated, fixtures)
	if len(bad) > 0 {
		t.Errorf("%d generations came out different on a later run of the same input:\n\t%s", len(bad), strings.Join(bad, "\n\t"))
	}
}

// ---------- the static guard over generated code ----------

// templateRangeLine is a template line that emits a Go range statement.
type templateRangeLine struct {
	file       string
	line       int
	text       string
	re         *regexp.Regexp
	annotation string
	reached    bool
	// literal is how many characters of the line are not template actions:
	// the line with more of them is the more specific source of an emitted
	// line both match.
	literal int
}

var (
	templateActionRE    = regexp.MustCompile(`{{.*?}}`)
	goRangeRE           = regexp.MustCompile(`\bfor\b.*\brange\b`)
	whitespaceRunRE     = regexp.MustCompile(`\s+`)
	templateCommentOpen = regexp.MustCompile(`{{-?\s*/\*`)
)

// templateRangeLines indexes every template line that writes a Go `for ...
// range` statement, as a pattern the emitted line matches with every action
// standing for any text, together with the `maporder:` reason given in a
// template comment ending on the line above it, if any.
func templateRangeLines(t *testing.T) []*templateRangeLine {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "pkg", "emitter", "templates", "*.tmpl"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no templates found: %v", err)
	}
	var out []*templateRangeLine
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		for i, l := range lines {
			literal := templateActionRE.ReplaceAllString(l, " ")
			if !goRangeRE.MatchString(literal) {
				continue
			}
			var pat strings.Builder
			pat.WriteString("^")
			rest := normalizeSpace(l)
			for {
				loc := templateActionRE.FindStringIndex(rest)
				if loc == nil {
					pat.WriteString(regexp.QuoteMeta(rest))
					break
				}
				pat.WriteString(regexp.QuoteMeta(rest[:loc[0]]))
				pat.WriteString("(.*?)")
				rest = rest[loc[1]:]
			}
			pat.WriteString("$")
			out = append(out, &templateRangeLine{
				file:       filepath.Base(p),
				line:       i + 1,
				text:       strings.TrimSpace(l),
				re:         regexp.MustCompile(pat.String()),
				annotation: templateAnnotationAbove(lines, i),
				literal:    len(templateActionRE.ReplaceAllString(normalizeSpace(l), "")),
			})
		}
	}
	return out
}

func normalizeSpace(s string) string {
	return strings.TrimSpace(whitespaceRunRE.ReplaceAllString(s, " "))
}

// templateAnnotationAbove returns the `maporder:` reason of the template
// comment that ends on the line above line i, if there is one.
func templateAnnotationAbove(lines []string, i int) string {
	j := i - 1
	if j < 0 || !strings.HasSuffix(strings.TrimSpace(lines[j]), "*/}}") {
		return ""
	}
	for k := j; k >= 0; k-- {
		if loc := templateCommentOpen.FindStringIndex(lines[k]); loc != nil {
			text := strings.Join(lines[k:j+1], " ")
			if m := mapOrderAnnotation.FindStringSubmatch(text); m != nil {
				return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(m[1]), "*/}}"))
			}
			return ""
		}
	}
	return ""
}

func TestGeneratedCodeReadsNoMapOrder(t *testing.T) {
	jobs := determinismCorpus(t)
	tmplLines := templateRangeLines(t)

	dir := t.TempDir()
	if err := writeTestGoMod(dir, "maporder"); err != nil {
		t.Fatal(err)
	}
	// One package per distinct output: most schemas generate the same file
	// under several configurations, and checking it once is enough.
	seen := map[[32]byte]bool{}
	var pkgs, pkgFrom []string
	skipped := 0
	for _, job := range jobs {
		if job.first.src == nil {
			continue
		}
		if job.first.undeclared {
			skipped++
			continue
		}
		key := sha256.Sum256(append(append([]byte{}, job.first.src...), job.first.helpers...))
		if seen[key] {
			continue
		}
		seen[key] = true
		sub := filepath.Join(dir, "p"+strconv.Itoa(len(pkgs)))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "types.go"), job.first.src, 0o644); err != nil {
			t.Fatal(err)
		}
		if job.first.helpers != nil {
			if err := os.WriteFile(filepath.Join(sub, "helpers.go"), job.first.helpers, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		pkgs = append(pkgs, sub)
		pkgFrom = append(pkgFrom, fmt.Sprintf("%s [%s]", job.label, job.cfg.name))
	}

	type found struct {
		sites   []mapOrderSite
		ranges  []string // every range line, map or not, for template coverage
		failure string
	}
	useTestgoEnvForImports(t)
	results := make([]found, len(pkgs))
	work := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < runtime.GOMAXPROCS(0); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fset := token.NewFileSet()
			imp := importer.ForCompiler(fset, "source", nil).(types.ImporterFrom)
			for i := range work {
				files, info, src, err := typeCheckDir(fset, imp, pkgs[i])
				if err != nil {
					results[i].failure = err.Error()
					continue
				}
				results[i].sites = findMapOrderSites(fset, files, info, src)
				for _, f := range files {
					lines := src[fset.Position(f.Pos()).Filename]
					ast.Inspect(f, func(n ast.Node) bool {
						if rs, ok := n.(*ast.RangeStmt); ok {
							results[i].ranges = append(results[i].ranges, normalizeSpace(lines[fset.Position(rs.Pos()).Line-1]))
						}
						return true
					})
				}
			}
		}()
	}
	for i := range pkgs {
		work <- i
	}
	close(work)
	wg.Wait()

	// match finds the template lines an emitted line could have come from. A
	// line of all actions -- `for {{.I}}, {{.E}} := range {{.X}} {` -- matches
	// nearly every two-variable range there is, so only the most specific
	// matches are kept: the ones spelling out the most of the line literally.
	// Two equally specific matches are both kept, and both have to answer.
	match := func(line string) []*templateRangeLine {
		var m []*templateRangeLine
		best := -1
		for _, tl := range tmplLines {
			if !tl.re.MatchString(line) {
				continue
			}
			switch {
			case tl.literal > best:
				m, best = []*templateRangeLine{tl}, tl.literal
			case tl.literal == best:
				m = append(m, tl)
			}
		}
		return m
	}

	total := 0
	bad := map[string]bool{}
	for i, r := range results {
		if r.failure != "" {
			t.Errorf("type-checking the package generated from %s: %s", pkgFrom[i], r.failure)
			continue
		}
		for _, line := range r.ranges {
			for _, tl := range match(line) {
				tl.reached = true
			}
		}
		for _, s := range r.sites {
			total++
			if s.idiom {
				continue
			}
			line := normalizeSpace(s.line)
			origins := match(line)
			if len(origins) == 0 {
				bad[fmt.Sprintf("%s (in %s): no template line writes this; generator code emitted it", line, filepath.Base(s.pos.Filename))] = true
				continue
			}
			for _, tl := range origins {
				if tl.annotation == "" {
					bad[fmt.Sprintf("%s:%d: %s: %s", tl.file, tl.line, s.what, tl.text)] = true
				}
			}
		}
	}
	if total < 50 {
		t.Fatalf("found only %d map-order sites in %d generated packages; the analysis is not seeing the code", total, len(pkgs))
	}
	t.Logf("type-checked %d distinct generated packages: %d map-order sites (%d generations left out for naming types --lenient-refs could not declare)", len(pkgs), total, skipped)
	if len(bad) > 0 {
		var list []string
		for k := range bad {
			list = append(list, k)
		}
		sort.Strings(list)
		t.Errorf("generated code ranges over a map where the order can reach what the code does.\n"+
			"Open a loop that can refuse a member with the least_key_open template, or, where the order provably\n"+
			"cannot matter, say why in a `{{- /* maporder: <reason> */}}` comment ending on the line above:\n\t%s",
			strings.Join(list, "\n\t"))
	}

	// A template range line that no generated package reached was not judged
	// at all: nothing typed the expression it ranges over. It has to be judged
	// by a person instead -- a `maporder:` comment saying why its order cannot
	// matter, or that what it ranges is not a map -- or be reached by a corpus
	// schema, so that no template loop escapes both halves of this test.
	var unreached []string
	for _, tl := range tmplLines {
		if !tl.reached && tl.annotation == "" {
			unreached = append(unreached, fmt.Sprintf("%s:%d: %s", tl.file, tl.line, tl.text))
		}
	}
	if len(unreached) > 0 {
		t.Errorf("%d template range lines were reached by no corpus schema and carry no maporder: comment, so nothing\n"+
			"judged what they range over:\n\t%s", len(unreached), strings.Join(unreached, "\n\t"))
	}
}
