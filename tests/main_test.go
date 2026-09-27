package tests

import (
	"os"
	"testing"

	"github.com/mgilbir/schemagen/internal/testgo"
)

// TestMain claims the module's shared build cache for this binary and hands it
// back on the way out, deleting it if this was the last test binary using it.
// Every go command this package runs goes through testgo.Command, which refuses
// to run in a binary that skipped this.
//
// It also prints what an UPDATE_GOLDEN run changed, last, so a regeneration
// ends on the list of goldens it rewrote; see checkGolden.
func TestMain(m *testing.M) {
	code := testgo.Run(m)
	reportGoldenChanges()
	os.Exit(code)
}
