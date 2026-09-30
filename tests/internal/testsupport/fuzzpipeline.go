package testsupport

import (
	"encoding/json"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// FuzzSeedCfgBits are the cfgBits values every seed schema is registered under.
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
var FuzzSeedCfgBits = []uint8{0x00, 0x1F, 0x6A, 0xB5, 0xC1}

// FuzzConfig derives a generator.Config from the fuzzer's config byte, so the
// fuzzer explores the flag matrix and not just the schema space.
//
// Resolver and CrossPackage stay nil: the fuzz body must never reach the
// network or the filesystem.
func FuzzConfig(cfgBits uint8) generator.Config {
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

// FuzzOnce is the body of FuzzGenerate, called from the fuzz target and from the
// tests that hold the seed corpus to the worker's time and memory limits. Shared
// so that none of them can drift into exercising something the others do not.
//
// The pipeline itself is FuzzPipeline; FuzzOnce is it under the memory gate in
// fuzzgate.go, which panics -- deliberately, and before the runtime can
// reach the `fatal error: out of memory` that no harness can attribute -- if an
// input grows the heap past fuzzMemoryBudget.
func FuzzOnce(em *emitter.Emitter, cfgBits uint8, data []byte) {
	fuzzMemoryGate(cfgBits, data, func() {
		FuzzPipeline(em, cfgBits, data)
	})
}

// FuzzPipeline is parse -> generate -> emit, with nothing watching it. Call
// FuzzOnce instead unless the point is to measure what the watching costs.
func FuzzPipeline(em *emitter.Emitter, cfgBits uint8, data []byte) {
	var s schema.Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return // not a schema document; nothing to exercise
	}
	s.Normalize()

	cfg := FuzzConfig(cfgBits)
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
