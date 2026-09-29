package emitter

import (
	"bytes"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/emitter/internal/gocontext"
)

// TestGuardTableIsCurrent recomputes guards_gen.go from the templates -- the
// context analysis New no longer runs -- and fails when the checked-in file
// differs. A template edited without `go generate ./pkg/emitter` fails here,
// by name, before New refuses it on the hash at run time.
func TestGuardTableIsCurrent(t *testing.T) {
	sub, err := fs.Sub(templateFS, "templates")
	if err != nil {
		t.Fatal(err)
	}
	want, err := gocontext.GenerateTable(sub, FuncMap())
	if err != nil {
		t.Fatalf("recomputing the guard table: %v", err)
	}
	got, err := os.ReadFile("guards_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		// The entries first: a guard that moved is the difference worth
		// naming, and the hash line differs for any edit at all.
		gl, wl := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gl) || i < len(wl); i++ {
			if i < len(gl) && strings.HasPrefix(gl[i], "const guardTableTemplatesHash") {
				continue
			}
			var g, w string
			if i < len(gl) {
				g = gl[i]
			}
			if i < len(wl) {
				w = wl[i]
			}
			if g != w {
				t.Fatalf("guards_gen.go is stale; run go generate ./pkg/emitter\nfirst difference at line %d:\n checked in: %s\n recomputed: %s", i+1, g, w)
			}
		}
		t.Fatal("guards_gen.go is stale: the guards are the same, but the templates they were computed from have changed; run go generate ./pkg/emitter")
	}
}

// TestNewRefusesAStaleGuardTable holds applyGuardTable's count checks: an
// action the table does not name is refused by name, and an entry that names
// no action is refused too. (The hash check is a constant compared with the
// embedded templates; editing a template without regenerating is what
// exercises it.)
func TestNewRefusesAStaleGuardTable(t *testing.T) {
	saved := guardTable
	defer func() { guardTable = saved }()

	parse := func() error {
		tmpl, err := parseTemplatesUnguarded()
		if err != nil {
			t.Fatal(err)
		}
		return applyGuardTable(tmpl)
	}
	if err := parse(); err != nil {
		t.Fatalf("the checked-in table does not apply: %v", err)
	}
	guardTable = saved[1:]
	if err := parse(); err == nil || !strings.Contains(err.Error(), "no entry in guards_gen.go") {
		t.Errorf("a table missing an action was accepted: %v", err)
	}
	guardTable = append(append([]guardEntry{}, saved...), guardEntry{"file.go.tmpl", 1 << 30, "_goCode"})
	if err := parse(); err == nil || !strings.Contains(err.Error(), "entries for") {
		t.Errorf("a table with an entry for no action was accepted: %v", err)
	}
}
