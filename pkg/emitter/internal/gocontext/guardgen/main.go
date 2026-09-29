// Command guardgen writes pkg/emitter/guards_gen.go: the Go context of every
// output action of the emitter's templates, and the guard the emitter appends
// to it. Run it through `go generate ./pkg/emitter` after changing a
// template; TestGuardTableIsCurrent fails until it has been run.
package main

import (
	"fmt"
	"os"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/emitter/internal/gocontext"
)

func main() {
	// go generate runs this in pkg/emitter.
	src, err := gocontext.GenerateTable(os.DirFS("templates"), emitter.FuncMap())
	if err != nil {
		fmt.Fprintln(os.Stderr, "guardgen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile("guards_gen.go", src, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "guardgen:", err)
		os.Exit(1)
	}
}
