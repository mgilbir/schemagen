package testsupport

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
)

var (
	schemagenBinOnce sync.Once
	schemagenBinPath string
	schemagenBinErr  error
)

// SchemagenBinary builds the CLI once for the whole test binary. The
// differential is driven through the command line rather than through the
// library because that is where multi-package generation is wired -- the
// package assignment, the generation order derived from the $refs, and the one
// registry shared by every document of the run.
func SchemagenBinary(t *testing.T) string {
	t.Helper()
	schemagenBinOnce.Do(func() {
		// Removed by testgo.Main when this binary's tests finish, and swept by
		// name if it is killed first; see testgo.MkdirProcessTemp.
		dir, err := testgo.MkdirProcessTemp()
		if err != nil {
			schemagenBinErr = err
			return
		}
		bin := filepath.Join(dir, "schemagen")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cmd := testgo.Command(ctx, Root, "build", "-o", bin, ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			schemagenBinErr = fmt.Errorf("building schemagen: %w\n%s", err, out)
			return
		}
		schemagenBinPath = bin
	})
	if schemagenBinErr != nil {
		t.Fatal(schemagenBinErr)
	}
	return schemagenBinPath
}

// RunSchemagen runs the CLI SchemagenBinary built with args, failing the test
// with its output if it fails.
func RunSchemagen(t *testing.T, bin string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("schemagen %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// WriteCrossFile writes body to path, failing the test if it cannot.
func WriteCrossFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
