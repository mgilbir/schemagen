package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// TestCLIOutputIsDeterministicAcrossProcesses runs the built binary several
// times over the same input, each run a process of its own, and requires every
// run to write the same files, print the same stderr and exit the same way.
//
// TestGenerationIsDeterministic, in tests/determinism, compares generations inside one process,
// which is where Go already varies every map's order, and covers the corpus
// under the configuration matrix. What it cannot see is the command: the
// warnings the CLI assembles and prints, the order it writes packages and
// files in, the field-map and config handling that happens before a generator
// exists. This test sees all of that, as a separate process sees it.
//
// By default it runs the scenarios below, each written to drive a map with ten
// or more members -- Go iterates a map of up to eight in one of only as many
// orders as it has members, so a small map repeats its order too often to be a
// test. SCHEMAGEN_DETERMINISM_FULL=1 (make test-determinism) adds every schema
// under testdata/schemas, which is too slow to run on every `go test` and is
// run by CI instead.
func TestCLIOutputIsDeterministicAcrossProcesses(t *testing.T) {
	const runs = 5

	type scenario struct {
		name  string
		files map[string]string // written into the run directory
		args  []string
	}
	letters := strings.Split("abcdefghijkl", "")

	var scenarios []scenario
	fixtures, err := filepath.Glob(filepath.Join("testdata", "schemas", "determinism", "*.json"))
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("no determinism fixtures found: %v", err)
	}
	for _, f := range fixtures {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"static", "hybrid"} {
			scenarios = append(scenarios, scenario{
				name:  filepath.Base(f) + "/" + mode,
				files: map[string]string{"schema.json": string(data)},
				args:  []string{"generate", "schema.json", "-o", "out", "-p", "gen", "--validation", mode},
			})
		}
	}

	// A --field-map file with a dozen invalid Go names is refused for one of
	// them, and it has to be the same one every run.
	badNames := map[string]string{}
	for _, l := range letters {
		badNames[l] = "not-an-identifier-" + l
	}
	fieldMap, _ := json.Marshal(map[string]any{"schema": map[string]any{"Root": badNames}})
	objectSchema := `{"type":"object","properties":{` + joinEach(letters, `"%s":{"type":"string"}`) + `}}`
	scenarios = append(scenarios, scenario{
		name:  "field-map/invalid-names",
		files: map[string]string{"schema.json": objectSchema, "fm.json": string(fieldMap)},
		args:  []string{"generate", "schema.json", "-o", "out", "--field-map", "fm.json"},
	})

	// Overrides that match nothing are each warned about, and so is a file key
	// that names no input.
	unmatched := map[string]string{}
	for _, l := range letters {
		unmatched["nope_"+l] = "Nope" + strings.ToUpper(l)
	}
	fieldMap, _ = json.Marshal(map[string]any{"schema.json": map[string]any{"Root": unmatched}, "other.json": map[string]any{"X": map[string]string{"y": "Y"}}})
	scenarios = append(scenarios, scenario{
		name:  "field-map/unmatched",
		files: map[string]string{"schema.json": objectSchema, "fm.json": string(fieldMap)},
		args:  []string{"generate", "schema.json", "-o", "out", "--field-map", "fm.json"},
	})

	// Required and readOnly together, under --strict-read-write, is a warning
	// per property.
	scenarios = append(scenarios, scenario{
		name: "strict-read-write/unsatisfiable",
		files: map[string]string{"schema.json": `{"type":"object","required":[` + joinEach(letters, `"%s"`) + `],"properties":{` +
			joinEach(letters, `"%s":{"type":"string","readOnly":true}`) + `}}`},
		args: []string{"generate", "schema.json", "-o", "out", "--strict-read-write"},
	})

	// Unresolvable references degraded by --lenient-refs are each warned about.
	scenarios = append(scenarios, scenario{
		name:  "lenient-refs/unresolved",
		files: map[string]string{"schema.json": `{"type":"object","properties":{` + joinEach(letters, `"%s":{"$ref":"missing-%[1]s.json"}`) + `}}`},
		args:  []string{"generate", "schema.json", "-o", "out", "--lenient-refs"},
	})

	// Several documents into several packages, ordered by the refs between
	// them, and into one package sharing types.
	multi := map[string]string{}
	var multiArgs, sharedArgs []string
	for i, l := range letters {
		next := letters[(i+1)%len(letters)]
		body := fmt.Sprintf(`{"$id":"https://determinism.example/%s.json","title":"Doc%s","type":"object","properties":{"own":{"type":"string"}%s},"$defs":{"Shared":{"type":"object","properties":{"v":{"type":"integer"}}}}}`,
			l, strings.ToUpper(l), refIf(i < len(letters)-1, fmt.Sprintf(`,"next":{"$ref":"%s.json#/$defs/Shared"}`, next)))
		multi[l+".json"] = body
		multiArgs = append(multiArgs, l+".json", "--schema-package", fmt.Sprintf("https://determinism.example/%s.json=example.com/m/%s", l, l))
		sharedArgs = append(sharedArgs, l+".json")
	}
	scenarios = append(scenarios, scenario{
		name:  "multi-package",
		files: multi,
		args:  append([]string{"generate", "-o", "out"}, multiArgs...),
	})
	scenarios = append(scenarios, scenario{
		name:  "shared-types",
		files: multi,
		args:  append([]string{"generate", "-o", "out", "-p", "gen", "--shared-types"}, sharedArgs...),
	})

	if os.Getenv("SCHEMAGEN_DETERMINISM_FULL") != "" {
		var corpus []string
		err := filepath.Walk(filepath.Join("testdata", "schemas"), func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(p, ".json") {
				corpus = append(corpus, p)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(corpus)
		for _, p := range corpus {
			abs, err := filepath.Abs(p)
			if err != nil {
				t.Fatal(err)
			}
			// Generated where it lies, so a sibling-relative $ref resolves as
			// it does for a user; the output goes to the run directory.
			scenarios = append(scenarios, scenario{name: "corpus/" + p, args: []string{"generate", abs, "-p", "gen", "-o", "out"}})
		}
	}

	bin := schemagenBinary(t)
	// Every run gets a directory of its own under this one, removed as soon as
	// the run has been read; the testing package removes the parent.
	runsDir := t.TempDir()
	type result struct {
		name, mismatch string
	}
	results := make([]result, len(scenarios))
	work := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < runtime.GOMAXPROCS(0); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				sc := scenarios[i]
				results[i].name = sc.name
				var first string
				for r := 0; r < runs; r++ {
					dir, err := os.MkdirTemp(runsDir, "run")
					if err != nil {
						results[i].mismatch = err.Error()
						break
					}
					got, err := runAndCapture(bin, dir, sc.files, sc.args)
					os.RemoveAll(dir)
					if err != nil {
						results[i].mismatch = err.Error()
						break
					}
					if r == 0 {
						first = got
						continue
					}
					if got != first {
						results[i].mismatch = fmt.Sprintf("run %d differs from run 1:\n%s", r+1, firstDifference(first, got))
						break
					}
				}
			}
		}()
	}
	for i := range scenarios {
		work <- i
	}
	close(work)
	wg.Wait()

	var bad []string
	for _, r := range results {
		if r.mismatch != "" {
			bad = append(bad, r.name+": "+r.mismatch)
		}
	}
	t.Logf("%d scenarios, %d runs each", len(scenarios), runs)
	if len(bad) > 0 {
		t.Errorf("%d scenarios produced different output from one run to the next:\n%s", len(bad), strings.Join(bad, "\n"))
	}
}

// runAndCapture writes files into dir, runs the binary there, and renders
// everything the run left behind -- its exit status, stdout, stderr, and every
// file under dir with its contents -- as one text.
func runAndCapture(bin, dir string, files map[string]string, args []string) (string, error) {
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return "", err
		}
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			return "", runErr
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "exit: %v\nstdout:\n%s\nstderr:\n%s\n", runErr, stdout.String(), strings.ReplaceAll(stderr.String(), dir, "<dir>"))
	var paths []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			paths = append(paths, p)
		}
		return err
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		rel, _ := filepath.Rel(dir, p)
		fmt.Fprintf(&b, "== %s\n%s\n", rel, data)
	}
	return b.String(), nil
}

func firstDifference(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) || i < len(bl); i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return fmt.Sprintf("\tline %d:\n\t\trun 1: %s\n\t\tlater: %s", i+1, x, y)
		}
	}
	return "\t(no line differs)"
}

func joinEach(items []string, format string) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = fmt.Sprintf(format, it)
	}
	return strings.Join(parts, ",")
}

func refIf(cond bool, s string) string {
	if cond {
		return s
	}
	return ""
}
