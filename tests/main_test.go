package tests

import (
	"testing"

	"github.com/mgilbir/schemagen/internal/testgo"
)

// TestMain claims the module's shared build cache for this binary and hands it
// back on the way out, deleting it if this was the last test binary using it.
// Every go command this package runs goes through testgo.Command, which refuses
// to run in a binary that skipped this.
func TestMain(m *testing.M) { testgo.Main(m) }
