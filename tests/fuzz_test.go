package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// fuzzSchemaDir holds the hand-written schemas used as the fuzz seed corpus,
// relative to the tests/ directory where these tests run.
const fuzzSchemaDir = "../testdata/schemas"

// fuzzSeedCfgBits are the cfgBits values every seed schema is registered under.
// One seed per schema would only ever exercise a single point of the flag
// matrix, leaving the rest of it to be discovered by mutation; these five cover
// every boolean flag in both positions, all three validation modes plus the
// zero value, and four of the draft overrides:
//
//	0x00 — all flags off, static validation, draft auto-detected
//	0x1F — all flags on, hybrid validation, draft auto-detected
//	0x6A — strict + lenient refs, runtime validation, draft-04 override
//	0xB5 — omitempty + bigint, zero-value validation mode, draft-07 override
//	0xC1 — omitempty only, static validation, draft 2020-12 override
var fuzzSeedCfgBits = []uint8{0x00, 0x1F, 0x6A, 0xB5, 0xC1}

// fuzzConfig derives a generator.Config from the fuzzer's config byte, so the
// fuzzer explores the flag matrix and not just the schema space.
//
// Resolver and CrossPackage stay nil: the fuzz body must never reach the
// network or the filesystem.
func fuzzConfig(cfgBits uint8) generator.Config {
	cfg := generator.Config{
		PackageName:      "fuzzpkg",
		OutputDir:        ".",
		OmitEmpty:        cfgBits&0x01 != 0,
		StrictProperties: cfgBits&0x02 != 0,
		BigIntSupport:    cfgBits&0x04 != 0,
		LenientRefs:      cfgBits&0x08 != 0,
	}

	switch (cfgBits >> 4) & 0x03 {
	case 0:
		cfg.Validation = generator.ValidationModeStatic
	case 1:
		cfg.Validation = generator.ValidationModeHybrid
	case 2:
		cfg.Validation = generator.ValidationModeRuntime
	default:
		// The zero value, as a Config literal that omits the field has. It is
		// meant to normalize to static; leaving it in the matrix keeps that
		// path exercised.
		cfg.Validation = ""
	}

	switch (cfgBits >> 6) & 0x03 {
	case 0:
		cfg.Draft = schema.DraftUnknown // detect from $schema
	case 1:
		cfg.Draft = schema.Draft04
	case 2:
		cfg.Draft = schema.Draft07
	default:
		cfg.Draft = schema.Draft202012
	}

	return cfg
}

// fuzzSeedCorpus reads the seed corpus: every schema under testdata/schemas, plus
// the schema of every test group in the external JSON Schema Test Suite when that
// (optional, downloaded) directory is present. Duplicates are dropped, and the
// first origin to state a schema is the one reported for it.
//
// It is shared by FuzzGenerate and by the test that holds the corpus to the fuzz
// worker's per-input deadline, which is the whole point: a budget measured over a
// different corpus to the one the fuzzer runs would not be measuring the gate.
func fuzzSeedCorpus(collect func(origin string, schema []byte)) (local, external int, err error) {
	seen := make(map[string]bool)
	add := func(origin string, raw []byte) {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || seen[string(trimmed)] {
			return
		}
		seen[string(trimmed)] = true
		collect(origin, trimmed)
	}

	err = filepath.Walk(fuzzSchemaDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		local++
		add(path, data)
		return nil
	})
	if err != nil {
		return local, external, fmt.Errorf("walking %s for seed corpus: %w", fuzzSchemaDir, err)
	}
	if local == 0 {
		// Without seeds the target is decorative: the fuzzer would start from
		// nothing and almost never reach the generator.
		return local, external, fmt.Errorf("no seed schemas found under %s", fuzzSchemaDir)
	}

	// The external suite is optional. Its absence must not fail or skip.
	if _, statErr := os.Stat(jstsBaseDir); statErr == nil {
		_ = filepath.Walk(jstsBaseDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
				return nil //nolint:nilerr // a partial suite is still usable as seeds
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil //nolint:nilerr // unreadable seed file, not a test failure
			}
			var groups []jstsTestGroup
			if err := json.Unmarshal(data, &groups); err != nil {
				return nil //nolint:nilerr // not a test-group file
			}
			for i, g := range groups {
				if len(g.Schema) == 0 {
					continue
				}
				external++
				add(fmt.Sprintf("%s (group %d)", path, i), g.Schema)
			}
			return nil
		})
	}
	return local, external, nil
}

// fuzzOnce is the body of FuzzGenerate, called from the fuzz target and from the
// test that times the seed corpus. Shared so that neither can drift into
// exercising something the other does not.
func fuzzOnce(em *emitter.Emitter, cfgBits uint8, data []byte) {
	var s schema.Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return // not a schema document; nothing to exercise
	}
	s.Normalize()

	cfg := fuzzConfig(cfgBits)
	ir, err := generator.New(cfg).Generate(&s)
	if err != nil || ir == nil {
		return // generation errors are an acceptable outcome
	}

	// Both emit paths run regardless of each other's outcome: an emission
	// error is acceptable, and skipping the helper path on it would leave
	// that code unexercised for every input the file template rejects. The
	// helper set is read from whatever the file emitted, which is nothing
	// when it failed -- an empty set is a legitimate input to EmitHelpers.
	src, _ := em.Emit(ir)
	_, _, _ = em.EmitHelpers(cfg.PackageName, generator.HelpersReferencedBy(string(src)))
}

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
}

// fuzzSeedBudget is the wall-clock ceiling one seed may take through the fuzz
// body.
//
// It exists because Go's fuzzing engine has a per-input deadline of its own that
// nothing in this repository can change: internal/fuzz gives the worker process
// ten seconds per call to the fuzz function and panics it as deadlocked past
// that. The coordinator sees a worker that died, reports "fuzzing process hung or
// terminated unexpectedly: exit status 2" against whichever seed it was holding,
// and stops -- while still gathering baseline coverage, so no fuzzing happens at
// all. The worker's stderr is discarded, so there is no stack and no crasher
// file, and `go test ./...` does not reproduce it because the ordinary seed
// replay runs in-process with no deadline. That is issue #233, and it left the
// fuzz gate inert for as long as it took someone to look.
//
// Two seconds is Go's ten mapped onto this binary. The worker runs a
// coverage-instrumented build, measured at roughly four to five times slower than
// an ordinary one on the same seeds, so a seed at two seconds here is at the
// deadline there. The budget is therefore the point of failure rather than a
// margin below it -- which is the only threshold that does not go stale, since a
// slower machine moves both sides of it together.
//
// The corpus runs about five times under it as this is written: the slowest seed
// is the 2000-deep `not` at a third of a second. That same seed took five seconds
// before #233, and the 1000-deep anyOf that first killed the gate took two.
const fuzzSeedBudget = 2 * time.Second

// TestFuzzSeedCorpusFitsTheWorkerDeadline runs every fuzz seed through the fuzz
// body and fails on any that takes longer than fuzzSeedBudget.
//
// `go test ./...` already replays the seed corpus -- that is what a fuzz target
// does when it is run without -fuzz -- but it replays it without a clock, so a
// seed that has become slow enough to kill a fuzz worker passes there and takes
// the whole fuzz gate down separately, on a nightly schedule, with a message that
// names no cause. This is the check that fails on the pull request instead.
func TestFuzzSeedCorpusFitsTheWorkerDeadline(t *testing.T) {
	em, err := emitter.New()
	if err != nil {
		t.Fatalf("emitter.New: %v", err)
	}

	type slow struct {
		origin string
		bits   uint8
		took   time.Duration
	}
	var worst slow
	var over []slow
	seeds := 0

	local, external, err := fuzzSeedCorpus(func(origin string, schema []byte) {
		for _, bits := range fuzzSeedCfgBits {
			seeds++
			start := time.Now()
			fuzzOnce(em, bits, schema)
			took := time.Since(start)
			if took > worst.took {
				worst = slow{origin, bits, took}
			}
			if took > fuzzSeedBudget {
				over = append(over, slow{origin, bits, took})
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("timed %d seeds (%d local files, %d external test groups); slowest %v, %s with cfgBits 0x%02X",
		seeds, local, external, worst.took.Round(time.Millisecond), worst.origin, worst.bits)

	for _, s := range over {
		t.Errorf("seed %s with cfgBits 0x%02X took %v, over the %v budget. Go's fuzzing worker panics after "+
			"ten seconds on one input and the worker binary is coverage-instrumented and several times "+
			"slower than this one, so a seed here is on its way to taking the fuzz gate down entirely -- "+
			"see issue #233. Make the pipeline handle this schema faster; do not drop the seed, and do not "+
			"raise the budget",
			s.origin, s.bits, s.took.Round(time.Millisecond), fuzzSeedBudget)
	}
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
