package testsupport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CorpusSchemaPaths lists every schema document in testdata/schemas.
func CorpusSchemaPaths(t *testing.T) []string {
	t.Helper()
	var paths []string
	root := RepoPath("testdata", "schemas")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".json") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return paths
}

// SeedCorpus reads the fuzz seed corpus: every schema under testdata/schemas,
// plus the schema of every test group in the external JSON Schema Test Suite
// when that (optional, downloaded) directory is present. Duplicates are
// dropped, and the first origin to state a schema is the one reported for it.
//
// It is shared by FuzzGenerate, by the tests that hold the corpus to the fuzz
// worker's per-input deadline and memory ceiling, and by the determinism sweep,
// which is the whole point: a budget measured over a different corpus to the
// one the fuzzer runs would not be measuring the gate.
func SeedCorpus(collect func(origin string, schema []byte)) (local, external int, err error) {
	seen := make(map[string]bool)
	add := func(origin string, raw []byte) {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || seen[string(trimmed)] {
			return
		}
		seen[string(trimmed)] = true
		collect(origin, trimmed)
	}

	err = filepath.Walk(SchemaDir, func(path string, info os.FileInfo, err error) error {
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
		return local, external, fmt.Errorf("walking %s for seed corpus: %w", SchemaDir, err)
	}
	if local == 0 {
		// Without seeds the target is decorative: the fuzzer would start from
		// nothing and almost never reach the generator.
		return local, external, fmt.Errorf("no seed schemas found under %s", SchemaDir)
	}

	// The external suite is optional. Its absence must not fail or skip.
	if _, statErr := os.Stat(JSTSBaseDir); statErr == nil {
		_ = filepath.Walk(JSTSBaseDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
				return nil //nolint:nilerr // a partial suite is still usable as seeds
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil //nolint:nilerr // unreadable seed file, not a test failure
			}
			var groups []JSTSTestGroup
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
