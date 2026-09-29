package generator

import (
	"testing"

	"github.com/mgilbir/schemagen/internal/testgo"
)

// TestMain claims the module's shared build cache for this binary and hands it
// back on the way out. The tag tests build internal/tagoracle/probe under both
// encoding/json implementations, and every go command goes through
// testgo.Command, which refuses to run in a binary that skipped this.
func TestMain(m *testing.M) { testgo.Main(m) }
