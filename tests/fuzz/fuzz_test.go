package fuzz

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/tests/internal/testsupport"
)

// addFuzzSeeds registers the seed corpus with the fuzz target.
func addFuzzSeeds(f *testing.F) {
	f.Helper()

	unique := 0
	local, external, err := fuzzSeedCorpus(func(_ string, schema []byte) {
		unique++
		for _, bits := range fuzzSeedCfgBits {
			f.Add(bits, schema)
		}
	})
	if err != nil {
		f.Fatal(err)
	}

	f.Logf("fuzz seed corpus: %d unique schemas x %d config bytes (%d local files, %d external test groups)",
		unique, len(fuzzSeedCfgBits), local, external)
	for _, line := range fuzzSeedProvenance(external) {
		f.Log(line)
	}
}

// fuzzSeedProvenance says where this run's seeds came from, and every way that
// differs from the seeds CI replays.
//
// The corpus is not fixed by the repository alone, and a difference used to be
// invisible. With the JSON Schema Test Suite checked out, `go test ./...`
// replays 11,580 seeds; without it, a fifth of that -- and CI's test job did
// not check the suite out, so a developer's run and CI's were measuring
// different corpora under the same test name. CI's test job now downloads the
// pinned suite, as the fuzz job always has, so the three agree when the suite
// is present at the pinned commit. What is left is said here: a missing suite,
// a suite at another commit, and inputs in Go's own corpus directory
// (tests/fuzz/testdata/fuzz/FuzzGenerate), which `go test` replays from the working
// tree whether or not they are committed -- the 2026-09-26 audit found one
// there that had replayed locally, and never in CI, for weeks.
func fuzzSeedProvenance(external int) []string {
	var out []string
	pinned := makefileJSTSCommit()
	switch {
	case external == 0:
		out = append(out, fmt.Sprintf("fuzz seed corpus differs from CI's: the JSON Schema Test Suite is not checked out at %s, "+
			"so its test groups are not replayed here and CI replays them; run 'make download-test-suite' to match", jstsBaseDir))
	default:
		head := gitHead(filepath.Dir(jstsBaseDir))
		switch {
		case head == "" || pinned == "":
			out = append(out, fmt.Sprintf("fuzz seed corpus: the suite checkout's commit (%q) or the Makefile's JSTS_COMMIT (%q) "+
				"could not be read, so whether these are CI's seeds is unknown", head, pinned))
		case head != pinned:
			out = append(out, fmt.Sprintf("fuzz seed corpus differs from CI's: the suite checkout is at %s and CI replays JSTS_COMMIT %s; "+
				"run 'make download-test-suite' to move it", head, pinned))
		default:
			out = append(out, fmt.Sprintf("fuzz seed corpus: the suite is at the pinned JSTS_COMMIT %s, as in CI", pinned))
		}
	}
	corpusDir := filepath.Join("testdata", "fuzz", "FuzzGenerate")
	if entries, err := os.ReadDir(corpusDir); err == nil && len(entries) > 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		out = append(out, fmt.Sprintf("fuzz seed corpus differs from CI's unless committed: %d input(s) in %s replay here "+
			"from the working tree (%s); CI replays only what the repository holds. Minimise a real finding into "+
			"testdata/schemas/adversarial, or delete it", len(names), corpusDir, strings.Join(names, ", ")))
	}
	return out
}

// makefileJSTSCommit reads the suite commit the Makefile pins.
func makefileJSTSCommit() string {
	data, err := os.ReadFile(testsupport.RepoPath("Makefile"))
	if err != nil {
		return ""
	}
	m := regexp.MustCompile(`(?m)^JSTS_COMMIT := ([0-9a-f]{40})$`).FindSubmatch(data)
	if m == nil {
		return ""
	}
	return string(m[1])
}

// gitHead is the commit a checkout is at, or "" when it cannot be read.
func gitHead(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// FuzzGenerate exercises parse -> generate -> emit with no compilation of the
// generated code. The single property under test is that the pipeline never
// panics: a generation or emission *error* is a perfectly acceptable outcome
// for arbitrary input and is not a failure. Only a panic (or a hang) is a
// finding.
func FuzzGenerate(f *testing.F) {
	addFuzzSeeds(f)

	// Built once, outside the fuzz body: the templates are embedded, so a
	// failure here is a build problem rather than something an input caused,
	// and it must not be reported as a crasher.
	em, err := emitter.New()
	if err != nil {
		f.Fatalf("emitter.New: %v", err)
	}

	f.Fuzz(func(t *testing.T, cfgBits uint8, data []byte) {
		fuzzOnce(em, cfgBits, data)
	})
}
