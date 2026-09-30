package fuzzmemory

import (
	"testing"

	"github.com/mgilbir/schemagen/internal/testgo"
)

// TestMain claims the module's shared build cache for this binary and hands it
// back on the way out, deleting it if this was the last test binary using it.
//
// Nothing here runs the go tool today -- the fuzz body is parse, generate and
// emit, in process -- but the package imports tests/internal/testsupport, which
// does, and a binary that reaches testgo.Command without this panics.
// TestEveryTestBinaryThatRunsTheGoToolInstallsMain holds every importer to it
// rather than waiting for the first call to find out.
func TestMain(m *testing.M) { testgo.Main(m) }
