package golden

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/tests/internal/testsupport"
)

// TestCompile verifies that every golden file on disk compiles.
//
// It walks testdata/golden rather than naming directories. The list it used to
// carry named eleven of the twelve directories and not exactnum, so the
// --exact-numbers goldens were never compiled: a golden planted there that did
// not build passed the whole suite. Walking means a directory added for the next
// flag is compiled the day it appears; TestEveryGoldenFileHasAGenerator is the
// other half, holding every file here to a runner that regenerates it, and the
// configuration that runner uses is named in each subtest.
//
// Each golden is generated again, by its own set's configuration, before it is
// compiled, and the output is thrown away. What that buys is the helper file.
// Generated source names every compiled pattern by a package-level variable
// whose name is a digest of the pattern, and the helper file learns which
// pattern a name stands for from the registry the generator fills as it emits
// (generator.PatternVarName); a file this process did not emit names variables
// the registry has never seen, and its helper file compiles none of them. This
// test used to get away without it only because TestGoldenFiles, earlier in
// the same binary, had emitted every golden first: `go test -run '^TestCompile$'`
// on its own failed for every golden with a pattern in it, and so did moving
// the test into a file that sorts before golden_test.go. Regenerating here
// makes the compile a statement about the golden and not about what happened
// to run before it. A golden whose generator now names different patterns
// fails to build, which is the right answer for a stale golden -- and
// TestGoldenFiles says so in its own words.
func TestCompile(t *testing.T) {
	type goldenSource struct {
		set        string
		schemaPath string
		cfg        generator.Config
	}
	owner := map[string]goldenSource{}
	for _, set := range goldenSets() {
		for _, tc := range set.cases {
			owner[filepath.ToSlash(tc.GoldenPath)] = goldenSource{set.name, tc.SchemaPath, set.cfg}
		}
	}
	files := goldenFilesOnDisk(t)
	if len(files) == 0 {
		t.Fatal("no golden files found under testdata/golden; the walk is not looking at the corpus")
	}

	// We can't compile all files together since they may have conflicting type names
	// (e.g., Address in nested_object.go and defs_ref.go). Instead, compile each separately.
	for _, rel := range files {
		src, registered := owner[rel]
		set := src.set
		if !registered {
			set = "unregistered"
		}
		t.Run(set+"/"+strings.TrimPrefix(rel, "testdata/golden/"), func(t *testing.T) {
			if registered {
				generateFromSchemaWithConfig(t, src.schemaPath, src.cfg)
			}
			singleTmpDir := t.TempDir()

			if err := writeTestGoMod(singleTmpDir, "compile_test"); err != nil {
				t.Fatalf("writing go.mod: %v", err)
			}

			data, err := os.ReadFile(testsupport.RepoPath(rel))
			if err != nil {
				t.Fatalf("reading golden file: %v", err)
			}

			name := filepath.Base(rel)
			content := strings.Replace(string(data), "package testpkg", "package compile_test", 1)
			if err := os.WriteFile(filepath.Join(singleTmpDir, name), []byte(content), 0o644); err != nil {
				t.Fatalf("writing file: %v", err)
			}
			writeSharedHelpers(t, singleTmpDir, content)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := testgo.Command(ctx, singleTmpDir, "build", ".")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("compilation failed:\n%s\nerror: %v", string(output), err)
			}
		})
	}
}
