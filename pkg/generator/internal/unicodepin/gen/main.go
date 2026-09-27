// Command gen writes pkg/generator/unicode_tables.go from the running
// toolchain's unicode package. Run it under the oldest Go the generated code
// supports -- the go.mod go directive -- which is what `go generate
// ./pkg/generator` does. See package unicodepin.
package main

import (
	"fmt"
	"os"

	"github.com/mgilbir/schemagen/pkg/generator/internal/unicodepin"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gen <output file>")
		os.Exit(2)
	}
	src, err := unicodepin.Render()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[1], src, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
