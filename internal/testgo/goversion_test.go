package testgo

import (
	"bytes"
	"encoding/json"
	"go/version"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// goDirectiveOf reads the go directive of a go.mod.
func goDirectiveOf(t *testing.T, gomod string) string {
	t.Helper()
	data, err := os.ReadFile(gomod)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "go" {
			return f[1]
		}
	}
	t.Fatalf("%s has no go directive", gomod)
	return ""
}

// TestRuntimeGoDirectiveIsCoherent holds the Go versions the runtime module and
// the modules around it declare to one another.
//
// The runtime is imported by every module that holds generated code, so its go
// directive -- and the directive of every module it requires, which `go mod
// tidy` would raise its own to -- is a floor on the Go of those modules. Raising
// it is a breaking change for people who did nothing. Three things are held:
//
//   - the runtime declares the go directive the throwaway modules declare, which
//     is the oldest Go generated code is promised to build with (GoDirective);
//   - that is no newer than the main module's, so the generator can always be
//     built by a toolchain that can build what it generates;
//   - no module in the runtime's build list declares a newer one.
//
// What it cannot hold is that the runtime compiles with the oldest toolchain:
// that takes the toolchain, and CI's runtime-oldest-go job builds and tests
// with it.
func TestRuntimeGoDirectiveIsCoherent(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	rtDir, err := RuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	rtGo := goDirectiveOf(t, filepath.Join(rtDir, "go.mod"))
	mainGo := goDirectiveOf(t, filepath.Join(root, "go.mod"))

	if rtGo != GoDirective {
		t.Errorf("runtime/go.mod declares go %s; the throwaway modules, and the promise to users, are go %s", rtGo, GoDirective)
	}
	if version.Compare("go"+rtGo, "go"+mainGo) > 0 {
		t.Errorf("runtime/go.mod declares go %s, newer than the main module's go %s", rtGo, mainGo)
	}

	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir = rtDir
	cmd.Env = Env()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -m all in the runtime: %v\n%s", err, stderr.String())
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	seen := 0
	for {
		var m struct {
			Path      string
			Main      bool
			GoVersion string
		}
		if err := dec.Decode(&m); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		seen++
		if m.Main || m.GoVersion == "" {
			continue
		}
		if version.Compare("go"+m.GoVersion, "go"+rtGo) > 0 {
			t.Errorf("the runtime requires %s, which declares go %s, newer than the runtime's go %s: users on go %s could not build", m.Path, m.GoVersion, rtGo, rtGo)
		}
	}
	if seen < 3 {
		t.Fatalf("go list -m all reported %d modules; the runtime's build list was not read", seen)
	}
}
