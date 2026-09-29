package testsupport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// RunEngineProgram generates schemaJSON into package enginetest/gen, with
// Root as its root type, and runs mainSrc against it. It returns the
// program's output, toolchain chatter removed.
func RunEngineProgram(t *testing.T, schemaJSON string, cfg generator.Config, mainSrc string) string {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(schemaJSON), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	cfg.PackageName = "gen"
	ir, err := generator.New(cfg).Generate(&s, generator.WithRootTypeName("Root"))
	if err != nil {
		t.Fatal(err)
	}
	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}
	src, err := em.Emit(ir)
	if err != nil {
		t.Fatal(err)
	}
	helpers, _, err := em.EmitHelpers("gen", generator.HelpersReferencedBy(string(src)))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "gen"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"gen/types.go": src, "main.go": []byte(mainSrc)}
	// A package that names no compiled pattern has no helper file: its
	// helpers are the runtime module's.
	if len(helpers) > 0 {
		files["gen/schemagen_helpers.go"] = helpers
	}
	// maporder: writes each file once; the order they are written in changes
	// nothing.
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteCogenGoMod(dir); err != nil {
		t.Fatal(err)
	}
	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(strings.Replace(string(mod), "module cogen_test", "module enginetest", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := testgo.Command(ctx, dir, "run", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("running the program: %v\n%s", err, out)
	}
	return ProgramOutput(out)
}
