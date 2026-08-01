package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// This is the driver for the co-generation harness; the grammar it drives is
// in cogen_grammar_test.go.
//
// Layer 1 (FuzzGenerate) proves the pipeline does not panic, which says
// nothing about whether the code it emits is correct. Each iteration here
// closes that: build a schema and a conforming instance together, generate and
// compile the bindings, round-trip the instance through them, check Validate
// accepts it, and check Validate rejects a family of mutants that each break
// exactly one keyword.
//
// The negative half is the part that carries the weight. If the generator
// silently fails to emit a constraint check, Validate degenerates to
// `return nil` and every conforming instance still passes, so an all-valid
// corpus cannot see the defect at all. Only a mutant that is supposed to be
// rejected can.

// coEnvRun gates the whole suite, exactly as SCHEMAGEN_RUN_EXTERNAL gates the
// JSON Schema Test Suite run. Each iteration compiles and runs a throwaway Go
// module, so leaving this in the default `go test ./...` would add minutes to
// a run that takes seconds.
const coEnvRun = "SCHEMAGEN_RUN_COGEN"

// coRunTimeout bounds one generated program's build-and-run. Generated code is
// straight-line validation with no loops over unbounded input, so anything
// near this is a hang, not slow work.
const coRunTimeout = 90 * time.Second

// coVerdict is what the generated program reported for one mutant.
type coVerdict struct {
	outcome string // "ACCEPTED", "UNMARSHAL" or "INVALID"
	msg     string
}

// coResult is the whole report from one compiled-and-run case.
type coResult struct {
	rtOK    bool
	rtMsg   string
	posOK   bool
	posMsg  string
	mutants []coVerdict
}

// coFailure is one thing that went wrong, in a form the shrinker can match
// against: kind says which check failed and mutKey identifies the mutation
// when the check was a mutation, so shrinking chases the same defect rather
// than settling for any failure it happens to reach.
type coFailure struct {
	kind   string
	mutKey string
	detail string
}

func (f coFailure) String() string {
	if f.mutKey == "" {
		return fmt.Sprintf("%s: %s", f.kind, f.detail)
	}
	return fmt.Sprintf("%s [%s]: %s", f.kind, f.mutKey, f.detail)
}

// sameDefect reports whether g is the failure f was, for shrinking purposes.
func (f coFailure) sameDefect(g coFailure) bool {
	return f.kind == g.kind && f.mutKey == g.mutKey
}

// ---------------------------------------------------------------------------
// Running one case
// ---------------------------------------------------------------------------

// coGenerate runs the schemagen pipeline over a co-generated schema. The
// config matches the external suite's: strict refs, so a ref nothing can serve
// fails generation instead of quietly degrading to any.
func coGenerate(schemaJSON []byte) (string, error) {
	var s schema.Schema
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	s.Normalize()

	cfg := generator.Config{PackageName: "testpkg", OmitEmpty: true}
	ir, err := generator.New(cfg).Generate(&s)
	if err != nil {
		return "", fmt.Errorf("generate: %w", err)
	}
	em, err := emitter.New()
	if err != nil {
		return "", fmt.Errorf("emitter: %w", err)
	}
	src, err := em.Emit(ir)
	if err != nil {
		return "", fmt.Errorf("emit: %w", err)
	}
	return string(src), nil
}

// coMainProgram writes the driver that runs inside the generated module. It
// reports every check on its own line so one build and one run cover the
// round-trip, the positive case and every mutant; compiling once per mutant
// would multiply the cost of an iteration by the size of the mutation
// catalogue.
//
// The cases file is decoded into a map rather than a struct so this template
// needs no struct tags, which would otherwise have to carry backquotes through
// a Go string literal.
func coMainProgram(rootType string, hasValidate bool) string {
	validateCall := `fmt.Println("POS OK")`
	mutValidate := `fmt.Printf("MUT %d ACCEPTED\n", i)`
	if hasValidate {
		validateCall = `if err := obj.Validate(); err != nil {
			fmt.Printf("POS FAIL %s\n", oneLine(err.Error()))
		} else {
			fmt.Println("POS OK")
		}`
		mutValidate = `if err := mo.Validate(); err != nil {
			fmt.Printf("MUT %d INVALID %s\n", i, oneLine(err.Error()))
		} else {
			fmt.Printf("MUT %d ACCEPTED\n", i)
		}`
	}

	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
)

// oneLine collapses whitespace so one verdict always occupies one line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func main() {
	raw, err := os.ReadFile("cases.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading cases: %%v\n", err)
		os.Exit(1)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		fmt.Fprintf(os.Stderr, "parsing cases: %%v\n", err)
		os.Exit(1)
	}
	instance := doc["instance"]
	var mutants []json.RawMessage
	if len(doc["mutants"]) > 0 {
		if err := json.Unmarshal(doc["mutants"], &mutants); err != nil {
			fmt.Fprintf(os.Stderr, "parsing mutants: %%v\n", err)
			os.Exit(1)
		}
	}

	var obj %[1]s
	if err := json.Unmarshal(instance, &obj); err != nil {
		fmt.Printf("RT FAIL unmarshal: %%s\n", oneLine(err.Error()))
		fmt.Printf("POS FAIL unmarshal: %%s\n", oneLine(err.Error()))
	} else {
		if out, err := json.Marshal(obj); err != nil {
			fmt.Printf("RT FAIL marshal: %%s\n", oneLine(err.Error()))
		} else {
			var before, after any
			if err := json.Unmarshal(instance, &before); err != nil {
				fmt.Printf("RT FAIL reparse input: %%s\n", oneLine(err.Error()))
			} else if err := json.Unmarshal(out, &after); err != nil {
				fmt.Printf("RT FAIL reparse output: %%s\n", oneLine(err.Error()))
			} else if !reflect.DeepEqual(before, after) {
				fmt.Printf("RT FAIL mismatch in=%%s out=%%s\n", oneLine(string(instance)), oneLine(string(out)))
			} else {
				fmt.Println("RT OK")
			}
		}
		%[2]s
	}

	for i, m := range mutants {
		var mo %[1]s
		if err := json.Unmarshal(m, &mo); err != nil {
			fmt.Printf("MUT %%d UNMARSHAL %%s\n", i, oneLine(err.Error()))
			continue
		}
		%[3]s
	}
}
`, rootType, validateCall, mutValidate)
}

// coRunCase generates, compiles and runs one document plus its mutants.
// A non-nil error means the pipeline or the compiler refused the case, which
// is itself a finding: every schema this grammar produces is meant to be
// codegen-suitable.
func coRunCase(doc *coDoc, muts []coMutation) (coResult, error) {
	var res coResult

	schemaJSON, err := json.Marshal(doc.schema())
	if err != nil {
		return res, fmt.Errorf("marshal schema: %w", err)
	}
	instance := doc.instance()
	instanceJSON, err := json.Marshal(instance)
	if err != nil {
		return res, fmt.Errorf("marshal instance: %w", err)
	}

	mutantJSON := make([]json.RawMessage, 0, len(muts))
	for _, m := range muts {
		mutated, err := m.apply(instance)
		if err != nil {
			return res, fmt.Errorf("apply mutation %s: %w", m.key(), err)
		}
		raw, err := json.Marshal(mutated)
		if err != nil {
			return res, fmt.Errorf("marshal mutant %s: %w", m.key(), err)
		}
		mutantJSON = append(mutantJSON, raw)
	}

	code, err := coGenerate(schemaJSON)
	if err != nil {
		return res, err
	}
	rootType := extractRootTypeNameFromCode(code)
	if rootType == "" {
		return res, fmt.Errorf("no root type in generated code")
	}

	dir, err := os.MkdirTemp("", "schemagen-cogen-*")
	if err != nil {
		return res, fmt.Errorf("tmpdir: %w", err)
	}
	defer os.RemoveAll(dir)

	content := strings.Replace(code, "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(content), 0o644); err != nil {
		return res, fmt.Errorf("write types: %w", err)
	}
	if err := writeSharedHelpersErr(dir, content); err != nil {
		return res, fmt.Errorf("write helpers: %w", err)
	}
	main := coMainProgram(rootType, hasValidateMethod(code))
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644); err != nil {
		return res, fmt.Errorf("write main: %w", err)
	}
	cases, err := json.Marshal(map[string]any{"instance": json.RawMessage(instanceJSON), "mutants": mutantJSON})
	if err != nil {
		return res, fmt.Errorf("marshal cases: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cases.json"), cases, 0o644); err != nil {
		return res, fmt.Errorf("write cases: %w", err)
	}
	if err := writeTestGoMod(dir, "cogen_test"); err != nil {
		return res, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), coRunTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = dir
	// The ephemeral GOCACHE is not optional here: a few hundred iterations, each
	// compiling a package that will never be seen again, would otherwise add
	// gigabytes to the user's persistent build cache.
	cmd.Env = ephemeralCacheEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return res, fmt.Errorf("compile/run: %w\n%s", err, string(out))
	}

	res.mutants = make([]coVerdict, len(muts))
	for i := range res.mutants {
		res.mutants[i] = coVerdict{outcome: "MISSING"}
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "go: "):
			continue
		case line == "RT OK":
			res.rtOK = true
		case strings.HasPrefix(line, "RT FAIL "):
			res.rtMsg = strings.TrimPrefix(line, "RT FAIL ")
		case line == "POS OK":
			res.posOK = true
		case strings.HasPrefix(line, "POS FAIL "):
			res.posMsg = strings.TrimPrefix(line, "POS FAIL ")
		case strings.HasPrefix(line, "MUT "):
			fields := strings.SplitN(strings.TrimPrefix(line, "MUT "), " ", 3)
			if len(fields) < 2 {
				return res, fmt.Errorf("unparseable verdict %q", line)
			}
			idx, convErr := strconv.Atoi(fields[0])
			if convErr != nil || idx < 0 || idx >= len(res.mutants) {
				return res, fmt.Errorf("verdict for unknown mutant: %q", line)
			}
			v := coVerdict{outcome: fields[1]}
			if len(fields) == 3 {
				v.msg = fields[2]
			}
			res.mutants[idx] = v
		default:
			return res, fmt.Errorf("unexpected program output %q", line)
		}
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// Judging one case
// ---------------------------------------------------------------------------

// coCheck runs a document and turns the report into a list of failures. The
// raw result comes back too so a caller that also cross-checks against
// reference implementations does not have to compile the case twice.
func coCheck(doc *coDoc) (coResult, []coFailure) {
	muts := doc.mutations()
	res, err := coRunCase(doc, muts)
	if err != nil {
		return res, []coFailure{{kind: "generate", detail: err.Error()}}
	}

	var out []coFailure
	if !res.rtOK {
		out = append(out, coFailure{kind: "roundtrip", detail: res.rtMsg})
	}
	if !res.posOK {
		// A conforming instance must be accepted. When this fires the schema
		// and the instance disagree, which is either a generator defect or a
		// grammar defect -- both worth stopping for.
		out = append(out, coFailure{kind: "positive", detail: res.posMsg})
	}

	for i, m := range muts {
		v := res.mutants[i]
		switch {
		case v.outcome == "MISSING":
			out = append(out, coFailure{kind: "mutant-missing", mutKey: m.key(),
				detail: "the generated program reported no verdict"})

		case v.outcome == "ACCEPTED":
			out = append(out, coFailure{kind: "mutant-accepted", mutKey: m.key(),
				detail: fmt.Sprintf("violating %s was accepted", m.Keyword)})

		case v.outcome == "UNMARSHAL":
			// Wrong-type mutants are expected to die in the decoder. Anything
			// else reaching the decoder means the constraint is being enforced
			// by the Go type rather than by Validate, which is worth knowing
			// because Validate is what callers of hand-built values run.
			if !m.Loose {
				out = append(out, coFailure{kind: "mutant-unmarshal", mutKey: m.key(),
					detail: fmt.Sprintf("violating %s was rejected by UnmarshalJSON, not Validate: %s", m.Keyword, v.msg)})
			}

		case v.outcome == "INVALID":
			// Assert the violated constraint is actually named. A rejection
			// with an unrelated message usually means a different check fired,
			// so the keyword under test is still unproven.
			var missing []string
			for _, want := range m.Want {
				if !strings.Contains(v.msg, want) {
					missing = append(missing, want)
				}
			}
			if len(missing) > 0 {
				out = append(out, coFailure{kind: "mutant-message", mutKey: m.key(),
					detail: fmt.Sprintf("rejecting %s reported %q, missing %q", m.Keyword, v.msg, missing)})
			}

		default:
			out = append(out, coFailure{kind: "mutant-missing", mutKey: m.key(),
				detail: "unknown outcome " + v.outcome})
		}
	}
	return res, out
}

// ---------------------------------------------------------------------------
// Shrinking
// ---------------------------------------------------------------------------

// coShrink delta-debugs a failing document: it repeatedly tries the reductions
// the grammar offers and keeps any that still reproduces the *same* defect.
// Budget bounds the number of compile-and-run cycles, since each is the cost
// of a whole iteration.
func coShrink(doc *coDoc, target coFailure, budget int) (*coDoc, int) {
	current := doc
	spent := 0
	for spent < budget {
		progressed := false
		for _, candidate := range coReduce(current) {
			if spent >= budget {
				break
			}
			spent++
			reproduced := false
			_, failures := coCheck(candidate)
			for _, f := range failures {
				if target.sameDefect(f) {
					reproduced = true
					break
				}
			}
			if reproduced {
				current = candidate
				progressed = true
				break
			}
		}
		if !progressed {
			break
		}
	}
	return current, spent
}

// coReport renders a document, its instance and the mutant a failure names, so
// the case can be replayed by hand without re-running the harness.
func coReport(t *testing.T, doc *coDoc, target coFailure) {
	t.Helper()
	schemaJSON, err := json.MarshalIndent(doc.schema(), "", "  ")
	if err != nil {
		t.Logf("  (schema could not be marshalled: %v)", err)
		return
	}
	instance := doc.instance()
	instanceJSON, _ := json.Marshal(instance)
	t.Logf("  schema:   %s", schemaJSON)
	t.Logf("  instance: %s", instanceJSON)
	if target.mutKey == "" {
		return
	}
	for _, m := range doc.mutations() {
		if m.key() != target.mutKey {
			continue
		}
		mutated, err := m.apply(instance)
		if err != nil {
			t.Logf("  mutant %s: could not be applied: %v", target.mutKey, err)
			return
		}
		raw, _ := json.Marshal(mutated)
		t.Logf("  mutant %s: %s", target.mutKey, raw)
	}
}

// ---------------------------------------------------------------------------
// Bowtie cross-check
// ---------------------------------------------------------------------------

// coBowtieOutcome is one implementation's consensus verdict on one instance.
type coBowtieOutcome int

const (
	coBowtieUnknown coBowtieOutcome = iota
	coBowtieValid
	coBowtieInvalid
)

// coBowtieVerdicts asks independent JSON Schema implementations, driven by
// Bowtie in containers, whether each instance satisfies the schema. Running
// more than one is the point: a single library's opinion is not evidence, and
// where they disagree the answer is "unknown" rather than whichever one is
// listed first.
//
// The invocation and the JSONL shape mirror scripts/validate-seeds.py: one
// case, one line per implementation, results in instance order.
func coBowtieVerdicts(dir string, schemaJSON []byte, instances []json.RawMessage, impls []string) ([]coBowtieOutcome, error) {
	schemaPath := filepath.Join(dir, "bowtie_schema.json")
	if err := os.WriteFile(schemaPath, schemaJSON, 0o644); err != nil {
		return nil, err
	}
	args := []string{"--from", "bowtie-json-schema", "bowtie", "validate"}
	for _, impl := range impls {
		args = append(args, "-i", impl)
	}
	args = append(args, schemaPath)
	for i, inst := range instances {
		p := filepath.Join(dir, fmt.Sprintf("bowtie_inst_%03d.json", i))
		if err := os.WriteFile(p, inst, 0o644); err != nil {
			return nil, err
		}
		args = append(args, p)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "uvx", args...)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("bowtie: %w", err)
	}

	// index -> impl -> valid
	per := make([]map[string]bool, len(instances))
	for i := range per {
		per[i] = map[string]bool{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry struct {
			Implementation string `json:"implementation"`
			Results        []struct {
				Valid *bool `json:"valid"`
			} `json:"results"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil || entry.Implementation == "" {
			continue
		}
		for i, r := range entry.Results {
			if i < len(per) && r.Valid != nil {
				per[i][entry.Implementation] = *r.Valid
			}
		}
	}

	outcomes := make([]coBowtieOutcome, len(instances))
	for i, votes := range per {
		switch {
		case len(votes) == 0:
			outcomes[i] = coBowtieUnknown
		default:
			first := true
			agreed := true
			var value bool
			for _, v := range votes {
				if first {
					value, first = v, false
					continue
				}
				if v != value {
					agreed = false
				}
			}
			switch {
			case !agreed:
				outcomes[i] = coBowtieUnknown
			case value:
				outcomes[i] = coBowtieValid
			default:
				outcomes[i] = coBowtieInvalid
			}
		}
	}
	return outcomes, nil
}

// coBowtieTally accumulates cross-check counts across iterations.
type coBowtieTally struct {
	mu         sync.Mutex
	cases      int
	agree      int
	disagree   int
	unknown    int
	errors     int
	complaints []string
}

func (b *coBowtieTally) note(s string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.complaints) < 20 {
		b.complaints = append(b.complaints, s)
	}
}

// coCrossCheck compares an independent verdict on every instance of one
// iteration against what the generated bindings decided. res is the report
// coCheck already collected, so the case is compiled once per iteration
// whether or not the cross-check is on.
func coCrossCheck(tally *coBowtieTally, iter int, doc *coDoc, res coResult, impls []string) {
	muts := doc.mutations()
	if len(res.mutants) != len(muts) {
		tally.mu.Lock()
		tally.errors++
		tally.mu.Unlock()
		tally.note(fmt.Sprintf("iter %d: the generated program produced no usable verdicts", iter))
		return
	}
	schemaJSON, err := json.Marshal(doc.schema())
	if err != nil {
		return
	}
	instance := doc.instance()
	instanceJSON, err := json.Marshal(instance)
	if err != nil {
		return
	}
	instances := []json.RawMessage{instanceJSON}
	for _, m := range muts {
		mutated, err := m.apply(instance)
		if err != nil {
			return
		}
		raw, err := json.Marshal(mutated)
		if err != nil {
			return
		}
		instances = append(instances, raw)
	}

	dir, err := os.MkdirTemp("", "schemagen-cogen-bowtie-*")
	if err != nil {
		return
	}
	defer os.RemoveAll(dir)

	outcomes, err := coBowtieVerdicts(dir, schemaJSON, instances, impls)
	if err != nil {
		tally.mu.Lock()
		tally.errors++
		tally.mu.Unlock()
		tally.note(fmt.Sprintf("iter %d: bowtie: %v", iter, err))
		return
	}

	record := func(label string, reference coBowtieOutcome, ours bool, oursNote string) {
		tally.mu.Lock()
		tally.cases++
		switch {
		case reference == coBowtieUnknown:
			tally.unknown++
		case (reference == coBowtieValid) == ours:
			tally.agree++
		default:
			tally.disagree++
			tally.mu.Unlock()
			tally.note(fmt.Sprintf("iter %d %s: reference says %s, schemagen says %s (%s)",
				iter, label, coBowtieName(reference), coBowtieName(coBowtieOf(ours)), oursNote))
			return
		}
		tally.mu.Unlock()
	}

	record("instance", outcomes[0], res.posOK, res.posMsg)
	for i, m := range muts {
		v := res.mutants[i]
		record("mutant "+m.key(), outcomes[i+1], v.outcome == "ACCEPTED", v.outcome+" "+v.msg)
	}
}

func coBowtieOf(valid bool) coBowtieOutcome {
	if valid {
		return coBowtieValid
	}
	return coBowtieInvalid
}

func coBowtieName(o coBowtieOutcome) string {
	switch o {
	case coBowtieValid:
		return "valid"
	case coBowtieInvalid:
		return "invalid"
	default:
		return "unknown"
	}
}

// ---------------------------------------------------------------------------
// Driver
// ---------------------------------------------------------------------------

// coIterSeed mixes a base seed with an iteration index (SplitMix64). Deriving
// each iteration from an explicit seed, rather than from a shared stream, is
// what lets a single failing iteration be replayed on its own: the schema
// depends on the pair and on nothing else, including how many workers ran.
func coIterSeed(base uint64, iter int) uint64 {
	x := base + uint64(iter+1)*0x9E3779B97F4A7C15
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return x
}

func coEnvInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func coEnvString(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func coEnvUint(name string, def uint64) uint64 {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.ParseUint(v, 0, 64)
	if err != nil {
		return def
	}
	return n
}

// TestCoGenerated is the harness proper. Every iteration is an independent
// (schema, instance) pair built from its own derived seed, so a failure is
// reported with everything needed to replay it alone:
//
//	SCHEMAGEN_RUN_COGEN=1 SCHEMAGEN_COGEN_SEED=<seed> \
//	SCHEMAGEN_COGEN_ITER0=<iter> SCHEMAGEN_COGEN_ITERS=1 \
//	go test ./tests/... -run TestCoGenerated -v
func TestCoGenerated(t *testing.T) {
	if os.Getenv(coEnvRun) != "1" {
		t.Skipf("co-generation harness disabled; set %s=1 or run make cogen", coEnvRun)
	}

	seed := coEnvUint("SCHEMAGEN_COGEN_SEED", 1)
	iter0 := coEnvInt("SCHEMAGEN_COGEN_ITER0", 0)
	iters := coEnvInt("SCHEMAGEN_COGEN_ITERS", 100)
	par := coEnvInt("SCHEMAGEN_COGEN_PAR", runtime.NumCPU())
	maxFail := coEnvInt("SCHEMAGEN_COGEN_MAXFAIL", 5)
	shrinkBudget := coEnvInt("SCHEMAGEN_COGEN_SHRINK", 250)
	if par < 1 {
		par = 1
	}

	bowtie := os.Getenv("SCHEMAGEN_COGEN_BOWTIE") == "1"
	impls := strings.Split(coEnvString("SCHEMAGEN_COGEN_BOWTIE_IMPLS", "python-jsonschema,js-ajv"), ",")
	bowtieMax := coEnvInt("SCHEMAGEN_COGEN_BOWTIE_MAX", 20)

	t.Logf("seed=%d iterations=%d (first=%d) parallelism=%d bowtie=%v",
		seed, iters, iter0, par, bowtie)

	type failedIter struct {
		iter     int
		failures []coFailure
	}

	var (
		mu       sync.Mutex
		failed   []failedIter
		mutTotal int
		abort    bool
	)
	tally := &coBowtieTally{}

	start := time.Now()
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < par; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iter := range jobs {
				doc := coBuild(coIterSeed(seed, iter))
				res, failures := coCheck(doc)
				count := len(doc.mutations())

				mu.Lock()
				mutTotal += count
				if len(failures) > 0 {
					failed = append(failed, failedIter{iter: iter, failures: failures})
					if len(failed) >= maxFail {
						abort = true
					}
				}
				mu.Unlock()

				if bowtie && iter-iter0 < bowtieMax {
					coCrossCheck(tally, iter, doc, res, impls)
				}
			}
		}()
	}

	scheduled := 0
	for i := iter0; i < iter0+iters; i++ {
		mu.Lock()
		stop := abort
		mu.Unlock()
		if stop {
			break
		}
		jobs <- i
		scheduled++
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(start)

	rate := float64(scheduled) / elapsed.Seconds()
	t.Logf("ran %d iterations in %s (%.2f iterations/sec), %d mutants exercised, %d iterations failed",
		scheduled, elapsed.Round(time.Millisecond), rate, mutTotal, len(failed))

	if bowtie {
		tally.mu.Lock()
		t.Logf("bowtie cross-check via %s: %d instances judged, %d agree, %d disagree, %d unknown, %d errors",
			strings.Join(impls, ", "), tally.cases, tally.agree, tally.disagree, tally.unknown, tally.errors)
		complaints := append([]string(nil), tally.complaints...)
		disagree := tally.disagree
		tally.mu.Unlock()
		for _, c := range complaints {
			t.Logf("  %s", c)
		}
		if disagree > 0 {
			t.Errorf("%d reference disagreements; see the log above", disagree)
		}
	}

	if len(failed) == 0 {
		return
	}

	sort.Slice(failed, func(i, j int) bool { return failed[i].iter < failed[j].iter })
	for _, f := range failed {
		t.Errorf("seed=%d iter=%d: %d failure(s)", seed, f.iter, len(f.failures))
		for _, fail := range f.failures {
			t.Logf("  %s", fail)
		}
		t.Logf("  replay: SCHEMAGEN_RUN_COGEN=1 SCHEMAGEN_COGEN_SEED=%d SCHEMAGEN_COGEN_ITER0=%d SCHEMAGEN_COGEN_ITERS=1 go test ./tests/... -run TestCoGenerated -v",
			seed, f.iter)
	}

	// Shrink only the first failure. When something systemic breaks, every
	// iteration fails for the same reason and shrinking each one would cost a
	// full compile per reduction with nothing new to show.
	first := failed[0]
	target := first.failures[0]
	doc := coBuild(coIterSeed(seed, first.iter))
	t.Logf("shrinking iter=%d against %s (budget %d cycles)", first.iter, target.kind, shrinkBudget)
	minimal, spent := coShrink(doc, target, shrinkBudget)
	t.Logf("shrunk in %d cycles; minimal case for %s:", spent, target)
	coReport(t, minimal, target)
}
