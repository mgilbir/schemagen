package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// How a golden file is compared, and how one is rewritten.
//
// Comparison is the default, and it is what CI runs: a golden that no longer
// matches what the generator emits fails its test with the differing lines.
//
// Rewriting is UPDATE_GOLDEN=1 (`make golden`; "true" is accepted as the older
// spelling). It used to write every golden and return, saying nothing -- five
// runners, each with its own copy of that branch -- so a change in generated
// output was blessed by the same command that surfaced it. That is how the
// audit of 2026-09-26 planted a defect in the big-integer decoder, ran
// UPDATE_GOLDEN=true, and got a green run and rewritten goldens with nothing on
// the screen to say anything had moved.
//
// So a rewrite now reports what it changed, file by file, with a diffstat and
// the changed lines, and the run fails for every golden it changed. The files
// are written either way, so `git diff` shows the change to review; what the
// failure refuses is the silent blessing. GOLDEN_ACCEPT=1 is the explicit
// acknowledgement -- "I have read the changes and they are what I meant" -- and
// turns those failures into log lines, so a regeneration can be scripted once
// it has been looked at. Nothing else accepts a change: a run that rewrites a
// golden without it is red.
//
// Rewriting is refused outright on CI (GITHUB_ACTIONS=true): a CI job has
// nobody to read the report, and a job that regenerated its goldens before
// comparing them would pass whatever the generator emitted.

// goldenMode reads UPDATE_GOLDEN.
func goldenMode(t *testing.T) (update, accept bool) {
	t.Helper()
	switch v := os.Getenv("UPDATE_GOLDEN"); v {
	case "":
		return false, false
	case "1", "true":
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("UPDATE_GOLDEN=%s on CI: a CI run compares goldens, it does not rewrite them -- "+
				"a job that regenerated them first would pass whatever the generator emits", v)
		}
		switch a := os.Getenv("GOLDEN_ACCEPT"); a {
		case "":
			return true, false
		case "1":
			return true, true
		default:
			t.Fatalf("GOLDEN_ACCEPT=%q; the acknowledgement is GOLDEN_ACCEPT=1", a)
		}
	default:
		t.Fatalf("UPDATE_GOLDEN=%q; use UPDATE_GOLDEN=1 to rewrite goldens, or leave it unset to compare", v)
	}
	return false, false
}

// goldenChange is one golden a rewrite changed.
type goldenChange struct {
	path             string
	added, removed   int
	created          bool
	acknowledgedByMe bool
}

var (
	goldenChangesMu sync.Mutex
	goldenChanges   []goldenChange
)

// checkGolden compares got with the golden file at rel (relative to the
// repository root), or rewrites it under UPDATE_GOLDEN=1. It is the only place
// a golden is read or written.
func checkGolden(t *testing.T, rel string, got []byte) {
	t.Helper()
	path := filepath.Join("..", rel)
	update, accept := goldenMode(t)

	want, err := os.ReadFile(path)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		t.Fatalf("reading golden file %s: %v", rel, err)
	}

	if !update {
		if missing {
			t.Fatalf("golden file %s does not exist; run `make golden` to create it, and review what it writes", rel)
		}
		if string(got) != string(want) {
			added, removed, listing := lineDiff(string(want), string(got), 60)
			t.Errorf("generated output differs from golden file %s (+%d -%d lines):\n%s\n"+
				"If the change is intended, regenerate with `make golden` and review the diff it reports.",
				rel, added, removed, listing)
		}
		return
	}

	if !missing && string(got) == string(want) {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating golden dir: %v", err)
	}
	if err := os.WriteFile(path, got, 0o644); err != nil {
		t.Fatalf("updating golden file %s: %v", rel, err)
	}
	added, removed, listing := lineDiff(string(want), string(got), 60)
	change := goldenChange{path: rel, added: added, removed: removed, created: missing, acknowledgedByMe: accept}
	goldenChangesMu.Lock()
	goldenChanges = append(goldenChanges, change)
	goldenChangesMu.Unlock()

	verb := "rewrote"
	if missing {
		verb = "created"
	}
	msg := fmt.Sprintf("%s golden %s (+%d -%d lines):\n%s", verb, rel, added, removed, listing)
	if accept {
		t.Logf("%s\n(acknowledged by GOLDEN_ACCEPT=1)", msg)
		return
	}
	t.Errorf("%s\nThe file has been written; review it with `git diff -- %s`. A golden changed by a "+
		"regeneration is not accepted silently: re-run with GOLDEN_ACCEPT=1 once the change has been read "+
		"and is what was meant.", msg, rel)
}

// reportGoldenChanges prints the run's rewrite summary. TestMain calls it after
// the tests, so it is the last thing a `make golden` prints.
func reportGoldenChanges() {
	goldenChangesMu.Lock()
	defer goldenChangesMu.Unlock()
	if len(goldenChanges) == 0 {
		return
	}
	sort.Slice(goldenChanges, func(i, j int) bool { return goldenChanges[i].path < goldenChanges[j].path })
	var b strings.Builder
	var added, removed int
	acknowledged := true
	for _, c := range goldenChanges {
		note := ""
		if c.created {
			note = " (new)"
		}
		fmt.Fprintf(&b, "  %-70s +%-5d -%d%s\n", c.path, c.added, c.removed, note)
		added += c.added
		removed += c.removed
		acknowledged = acknowledged && c.acknowledgedByMe
	}
	status := "NOT acknowledged: the run fails until it is re-run with GOLDEN_ACCEPT=1"
	if acknowledged {
		status = "acknowledged with GOLDEN_ACCEPT=1"
	}
	fmt.Fprintf(os.Stderr, "\nUPDATE_GOLDEN changed %d golden file(s), +%d -%d lines, %s:\n%s",
		len(goldenChanges), added, removed, status, b.String())
}

// lineDiff counts the lines added and removed between two texts, and renders
// the changed lines, at most limit of them, as a unified-style listing.
//
// It is Myers' O(ND) algorithm over lines, because the goldens run to
// thousands of lines and a quadratic table over two of them is hundreds of
// megabytes; an edit script is also what makes the count a diffstat rather than
// a guess. Past maxDiffEdits edits -- a golden rewritten wholesale -- the
// script is not worth its memory, and the counts fall back to the lines each
// side has that the other lacks, which is a lower bound and is labelled so. The
// listing is for reading, not applying: it carries line numbers and no context.
func lineDiff(a, b string, limit int) (added, removed int, listing string) {
	x, y := splitLines(a), splitLines(b)
	ops, ok := myers(x, y, maxDiffEdits)
	if !ok {
		added, removed = multisetDiff(x, y)
		return added, removed, fmt.Sprintf("  (more than %d lines changed; counts are the lines each side lacks, a lower bound)\n", maxDiffEdits)
	}
	var out strings.Builder
	shown := 0
	for _, op := range ops {
		switch op.kind {
		case '-':
			removed++
		case '+':
			added++
		default:
			continue
		}
		if shown < limit {
			fmt.Fprintf(&out, "  %c %5d  %s\n", op.kind, op.line, strings.TrimSuffix(op.text, "\n"))
		}
		shown++
	}
	if shown > limit {
		fmt.Fprintf(&out, "  ... and %d more changed line(s)\n", shown-limit)
	}
	return added, removed, out.String()
}

// maxDiffEdits bounds the edit script lineDiff computes. The trace Myers keeps
// is quadratic in the number of edits, so this caps it near 32 MB.
const maxDiffEdits = 2000

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	// SplitAfter leaves an empty element after a final newline, which is not
	// a line.
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func multisetDiff(x, y []string) (added, removed int) {
	count := map[string]int{}
	for _, l := range x {
		count[l]++
	}
	for _, l := range y {
		count[l]--
	}
	for _, c := range count {
		if c > 0 {
			removed += c
		} else {
			added -= c
		}
	}
	return added, removed
}

type diffOp struct {
	kind byte // '=', '-', '+'
	line int  // 1-based line in the old text for '-' and '=', in the new text for '+'
	text string
}

// myers returns an edit script turning x into y, or false when that takes more
// than maxD edits.
func myers(x, y []string, maxD int) ([]diffOp, bool) {
	n, m := len(x), len(y)
	if n+m == 0 {
		return nil, true
	}
	max := n + m
	if max > maxD {
		max = maxD
	}
	// v[k] is the furthest i reached on diagonal k; trace[d] keeps v for
	// diagonals -d..d only, so the whole trace is O(D^2) rather than O(D*(N+M)).
	v := map[int]int{1: 0}
	var trace []map[int]int
	for d := 0; d <= max; d++ {
		snapshot := make(map[int]int, 2*d+3)
		for k := -d - 1; k <= d+1; k++ {
			if val, ok := v[k]; ok {
				snapshot[k] = val
			}
		}
		trace = append(trace, snapshot)
		for k := -d; k <= d; k += 2 {
			var i int
			if k == -d || (k != d && v[k-1] < v[k+1]) {
				i = v[k+1]
			} else {
				i = v[k-1] + 1
			}
			j := i - k
			for i < n && j < m && x[i] == y[j] {
				i, j = i+1, j+1
			}
			v[k] = i
			if i >= n && j >= m {
				return backtrack(x, y, trace, d), true
			}
		}
	}
	return nil, false
}

func backtrack(x, y []string, trace []map[int]int, d int) []diffOp {
	var ops []diffOp
	i, j := len(x), len(y)
	for ; d > 0; d-- {
		v := trace[d]
		k := i - j
		var prevK int
		if k == -d || (k != d && v[k-1] < v[k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevI := v[prevK]
		prevJ := prevI - prevK
		for i > prevI && j > prevJ {
			ops = append(ops, diffOp{kind: '=', line: i, text: x[i-1]})
			i, j = i-1, j-1
		}
		if i == prevI {
			ops = append(ops, diffOp{kind: '+', line: j, text: y[j-1]})
		} else {
			ops = append(ops, diffOp{kind: '-', line: i, text: x[i-1]})
		}
		i, j = prevI, prevJ
	}
	for i > 0 && j > 0 {
		ops = append(ops, diffOp{kind: '=', line: i, text: x[i-1]})
		i, j = i-1, j-1
	}
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}
	return ops
}

// goldenFilesOnDisk lists every .go file under testdata/golden, as paths
// relative to the repository root with forward slashes, sorted.
func goldenFilesOnDisk(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "testdata", "golden")
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel("..", path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	sort.Strings(files)
	return files
}

// TestEveryGoldenFileHasAGenerator holds the files under testdata/golden and the
// registry in goldenSets to each other, in both directions.
//
// A file no runner names is never regenerated -- UPDATE_GOLDEN walks the
// registry, not the directory -- so it stops describing the generator the day
// the generator moves, and TestCompile goes on compiling a fossil. A registered
// golden with no file is caught by its runner already; it is checked here too
// so the two sets are stated as one equality. And a golden named by two runners
// is regenerated twice, under two configurations, and holds whichever ran last.
func TestEveryGoldenFileHasAGenerator(t *testing.T) {
	registered := map[string]string{}
	for _, set := range goldenSets() {
		for _, tc := range set.cases {
			rel := filepath.ToSlash(tc.GoldenPath)
			if prev, dup := registered[rel]; dup {
				t.Errorf("%s is generated by both the %s and the %s golden sets", rel, prev, set.name)
			}
			registered[rel] = set.name
		}
	}
	onDisk := map[string]bool{}
	for _, rel := range goldenFilesOnDisk(t) {
		onDisk[rel] = true
		if registered[rel] == "" {
			t.Errorf("%s is a golden file no runner generates; add it to goldenSets (with the configuration "+
				"it was generated under) or delete it", rel)
		}
	}
	for rel, set := range registered {
		if !onDisk[rel] {
			t.Errorf("the %s golden set names %s, which does not exist", set, rel)
		}
	}
}

// TestLineDiffIsAMinimalEditScript holds the diffstat checkGolden reports to
// what it claims to be, against a quadratic longest-common-subsequence on small
// random texts: the counts are minimal, and the script, applied, turns the old
// text into the new one.
func TestLineDiffIsAMinimalEditScript(t *testing.T) {
	rng := uint64(0x9E3779B97F4A7C15)
	next := func(n int) int {
		rng ^= rng << 13
		rng ^= rng >> 7
		rng ^= rng << 17
		return int(rng % uint64(n))
	}
	gen := func() []string {
		n := next(12)
		out := make([]string, n)
		for i := range out {
			out[i] = string(rune('a'+next(4))) + "\n"
		}
		return out
	}
	for iter := 0; iter < 2000; iter++ {
		x, y := gen(), gen()
		ops, ok := myers(x, y, maxDiffEdits)
		if !ok {
			t.Fatalf("no script for %q -> %q", x, y)
		}
		var rebuilt []string
		added, removed := 0, 0
		for _, op := range ops {
			switch op.kind {
			case '=':
				rebuilt = append(rebuilt, op.text)
			case '+':
				rebuilt = append(rebuilt, op.text)
				added++
			case '-':
				removed++
			}
		}
		if strings.Join(rebuilt, "") != strings.Join(y, "") {
			t.Fatalf("applying the script to %q gives %q, not %q", x, rebuilt, y)
		}
		lcs := lcsLen(x, y)
		if added != len(y)-lcs || removed != len(x)-lcs {
			t.Fatalf("%q -> %q: +%d -%d, but the minimal script is +%d -%d", x, y, added, removed, len(y)-lcs, len(x)-lcs)
		}
	}
	// The fallback past the edit bound says it is one.
	big := strings.Repeat("x\n", maxDiffEdits+1)
	if a, r, listing := lineDiff("", big, 10); a != maxDiffEdits+1 || r != 0 || !strings.Contains(listing, "lower bound") {
		t.Errorf("a change past the edit bound reported +%d -%d with %q", a, r, listing)
	}
}

func lcsLen(x, y []string) int {
	dp := make([][]int, len(x)+1)
	for i := range dp {
		dp[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	return dp[0][0]
}
