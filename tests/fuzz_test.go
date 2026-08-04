package tests

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// addFuzzSeeds registers the seed corpus: every schema under testdata/schemas,
// plus the schema of every test group in the external JSON Schema Test Suite
// when that (optional, downloaded) directory is present.
func addFuzzSeeds(f *testing.F) {
	f.Helper()

	seen := make(map[string]bool)
	add := func(raw []byte) {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || seen[string(trimmed)] {
			return
		}
		seen[string(trimmed)] = true
		for _, bits := range fuzzSeedCfgBits {
			f.Add(bits, trimmed)
		}
	}

	local := 0
	err := filepath.Walk(fuzzSchemaDir, func(path string, info os.FileInfo, err error) error {
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
		add(data)
		return nil
	})
	if err != nil {
		f.Fatalf("walking %s for seed corpus: %v", fuzzSchemaDir, err)
	}
	if local == 0 {
		// Without seeds the target is decorative: the fuzzer would start from
		// nothing and almost never reach the generator.
		f.Fatalf("no seed schemas found under %s", fuzzSchemaDir)
	}

	// The external suite is optional. Its absence must not fail or skip.
	external := 0
	if _, err := os.Stat(jstsBaseDir); err == nil {
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
			for _, g := range groups {
				if len(g.Schema) == 0 {
					continue
				}
				external++
				add(g.Schema)
			}
			return nil
		})
	}

	f.Logf("fuzz seed corpus: %d unique schemas x %d config bytes (%d local files, %d external test groups)",
		len(seen), len(fuzzSeedCfgBits), local, external)
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
	})
}
