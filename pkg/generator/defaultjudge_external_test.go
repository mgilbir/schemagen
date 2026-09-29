package generator

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// The suite's paths from this package, and the drafts it holds. The tests/
// package owns the harness; this is the one suite reading that needs the
// generator's unexported judge.
const (
	judgeSuiteTests   = "../../testdata/external/JSON-Schema-Test-Suite/tests"
	judgeSuiteRemotes = "../../testdata/external/JSON-Schema-Test-Suite/remotes"
	judgeMetaSchemas  = "../../testdata/external/metaschemas"
	judgeRemoteBase   = "http://localhost:1234"
)

var judgeSuiteDrafts = map[string]schema.Draft{
	"draft3": schema.Draft03, "draft4": schema.Draft04, "draft6": schema.Draft06, "draft7": schema.Draft07,
	"draft2019-09": schema.Draft201909, "draft2020-12": schema.Draft202012, "v1": schema.DraftV1,
}

// judgeSuiteResolver serves the suite's remotes and the meta-schemas, as the
// external harness does.
func judgeSuiteResolver(t *testing.T) schema.SchemaResolver {
	t.Helper()
	schemas := map[string]*schema.Schema{}
	load := func(path string) *schema.Schema {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var s schema.Schema
		if json.Unmarshal(data, &s) != nil {
			return nil
		}
		s.Normalize()
		return &s
	}
	err := filepath.Walk(judgeSuiteRemotes, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		if s := load(path); s != nil {
			rel, _ := filepath.Rel(judgeSuiteRemotes, path)
			schemas[judgeRemoteBase+"/"+filepath.ToSlash(rel)] = s
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	metas, err := os.ReadDir(judgeMetaSchemas)
	if err != nil {
		t.Fatalf("reading the meta-schemas: %v; run 'make download-metaschemas'", err)
	}
	for _, e := range metas {
		if s := load(filepath.Join(judgeMetaSchemas, e.Name())); s != nil {
			id := s.ID
			if id == "" {
				id = s.LegacyID
			}
			schemas[strings.TrimSuffix(id, "#")] = s
		}
	}
	return schema.NewMappingResolver(schemas)
}

// TestExternalValueJudgeAgreesWithTheSuite holds the generation-time judge of
// a default (judgeValue) to the JSON Schema Test Suite: for every instance of
// every required group it decides, its verdict is the suite's.
//
// The judge is a second reading of the specification, beside the generated
// checks, and a wrong "valid" plants a default that makes a document invalid.
// It is allowed to say it cannot decide -- that defers the default to the
// runtime evaluator -- and never allowed to disagree. Run with the external
// suite (make test-external); a run that asked for it and cannot find it
// fails, as the harness in tests/ does.
func TestExternalValueJudgeAgreesWithTheSuite(t *testing.T) {
	if os.Getenv("SCHEMAGEN_RUN_EXTERNAL") != "1" {
		t.Skip("set SCHEMAGEN_RUN_EXTERNAL=1 (make test-external) to run against the JSON Schema Test Suite")
	}
	if _, err := os.Stat(judgeSuiteTests); err != nil {
		t.Fatalf("the JSON Schema Test Suite is not at %s: %v; run 'make download-test-suite'", judgeSuiteTests, err)
	}
	resolver := judgeSuiteResolver(t)
	decided, undecided, skippedGroups := 0, 0, 0
	// maporder: counts, and reports each disagreement it finds whatever the order.
	for dir, draft := range judgeSuiteDrafts {
		files, err := filepath.Glob(filepath.Join(judgeSuiteTests, dir, "*.json"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no test files for %s: %v", dir, err)
		}
		// The optional groups too -- bignums, ECMA-262 patterns, float
		// overflow, the format directories -- which say what a validator that
		// implements them answers. The judge answers them or leaves them.
		optional, err := filepath.Glob(filepath.Join(judgeSuiteTests, dir, "optional", "*.json"))
		if err != nil {
			t.Fatal(err)
		}
		formats, err := filepath.Glob(filepath.Join(judgeSuiteTests, dir, "optional", "format", "*.json"))
		if err != nil {
			t.Fatal(err)
		}
		files = append(append(files, optional...), formats...)
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var groups []struct {
				Description string          `json:"description"`
				Schema      json.RawMessage `json:"schema"`
				Tests       []struct {
					Description string          `json:"description"`
					Data        json.RawMessage `json:"data"`
					Valid       bool            `json:"valid"`
				} `json:"tests"`
			}
			if err := json.Unmarshal(data, &groups); err != nil {
				t.Fatalf("%s: %v", file, err)
			}
			for _, grp := range groups {
				var s schema.Schema
				if err := json.Unmarshal(grp.Schema, &s); err != nil {
					skippedGroups++
					continue
				}
				s.NormalizeForDraft(draft)
				// The posture each file asks about, as the harness in tests/
				// reads it (formatPostureFor there).
				cfg := Config{PackageName: "judge", Resolver: resolver, Draft: draft}
				switch p := filepath.ToSlash(file); {
				case strings.Contains(p, "optional/format-annotation"):
					cfg.FormatAnnotation = true
				case strings.Contains(p, "optional/format-assertion"):
				case strings.Contains(p, "optional/format"):
					cfg.FormatAssertion = true
				}
				g := New(cfg)
				if _, err := g.Generate(&s); err != nil {
					// A schema the generator refuses has no generated checks
					// to agree with, and plants no default.
					skippedGroups++
					continue
				}
				for _, tc := range grp.Tests {
					dec := json.NewDecoder(bytes.NewReader(tc.Data))
					dec.UseNumber()
					var v any
					if err := dec.Decode(&v); err != nil {
						t.Fatalf("%s: %s: %v", file, tc.Description, err)
					}
					got := g.judgeValue([]*schema.Schema{&s}, v)
					if got.j == judgedUnknown {
						undecided++
						continue
					}
					decided++
					if (got.j == judgedValid) != tc.Valid {
						t.Errorf("%s: %s / %s: the judge says valid=%v (%s), the suite says %v",
							filepath.Join(dir, filepath.Base(file)), grp.Description, tc.Description, got.j == judgedValid, got.why, tc.Valid)
					}
				}
			}
		}
	}
	t.Logf("decided %d instances, left %d to the runtime evaluator, skipped %d groups the generator refused", decided, undecided, skippedGroups)
	// The judge has to decide the bulk of the suite, or agreeing with it is
	// agreement about nothing. Measured: see the commit that set this floor.
	if decided < minJudgedSuiteInstances {
		t.Errorf("the judge decided %d instances, below the floor of %d", decided, minJudgedSuiteInstances)
	}
}

// minJudgedSuiteInstances is a floor, and only ever raised: a change that
// makes the judge decide less of the suite is one that defers more defaults to
// the runtime evaluator, and should say so by lowering nothing.
const minJudgedSuiteInstances = 7311
