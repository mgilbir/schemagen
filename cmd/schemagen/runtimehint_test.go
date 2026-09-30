package schemagen

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// TestRuntimeHintNamesTheVersionAReleaseHas holds what the hint tells a reader to
// type. A release names the version it was released as, which is the tag the
// runtime was released under; every other build says it has none, because a
// pseudo-version or a describe string is a version `go get` cannot find.
func TestRuntimeHintNamesTheVersionAReleaseHas(t *testing.T) {
	const get = "go get github.com/mgilbir/schemagen/runtime@"
	for _, tc := range []struct {
		version string
		release bool
	}{
		{"v0.1.3", true},
		{"v1.20.300", true},
		{"v0.2.0-rc.1", true},
		{"v0.1.3-5-gabc1234", false},                  // git describe, after a tag
		{"v0.1.3-5-gabc1234-dirty", false},            //
		{"abc1234", false},                            // git describe --always with no tag
		{"v0.0.0-20260817134839-56a8d020cea8", false}, // the toolchain's pseudo-version
		{"v0.0.0-20260817134839-56a8d020cea8+dirty", false},
		{"v0.1.3+dirty", false},
		{"dev", false},
		{"", false},
	} {
		hint := runtimeHint(tc.version)
		if !strings.HasPrefix(hint, runtimeHintPrefix+runtimeModule) || strings.Count(hint, "\n") != 1 {
			t.Errorf("%q: not one hint line: %q", tc.version, hint)
		}
		if tc.release {
			if !strings.Contains(hint, get+tc.version+"\n") {
				t.Errorf("%q: a release must name its own version to get: %q", tc.version, hint)
			}
			continue
		}
		if strings.Contains(hint, get) {
			t.Errorf("%q: not a release, but the hint tells the reader to get a version: %q", tc.version, hint)
		}
		if !strings.Contains(hint, "development build") {
			t.Errorf("%q: not a release, and the hint does not say so: %q", tc.version, hint)
		}
	}
}

// TestRuntimeHintIsTheModuleTheGeneratorImports keeps the restated module path
// the generator's own.
func TestRuntimeHintIsTheModuleTheGeneratorImports(t *testing.T) {
	if runtimeModule != generator.RuntimeImportPath {
		t.Errorf("the hint names %s, but generated code imports %s", runtimeModule, generator.RuntimeImportPath)
	}
}

// TestASuccessfulRunEndsWithTheRuntimeHintOnce runs the command: one hint, last,
// on a run that generated something, and none on a run that failed.
func TestASuccessfulRunEndsWithTheRuntimeHintOnce(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "a.json")
	other := filepath.Join(dir, "b.json")
	writeFile(t, good, `{"title":"A","type":"object","properties":{"s":{"type":"string"}}}`)
	writeFile(t, other, `{"title":"B","type":"object","properties":{"n":{"type":"integer"}}}`)

	for name, args := range map[string][]string{
		"one input":  {good, "-o", filepath.Join(dir, "one"), "-p", "one"},
		"two inputs": {good, other, "-o", filepath.Join(dir, "two"), "-p", "two"},
	} {
		cmd := NewRootCmd()
		var stdout, stderr strings.Builder
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs(append([]string{"generate"}, args...))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := stderr.String()
		if strings.Count(got, runtimeHintPrefix) != 1 || !strings.HasSuffix(got, "\n") || strings.Count(got, "\n") != 1 {
			t.Errorf("%s: stderr = %q, want exactly the one hint line", name, got)
		}
		// A test binary carries no release version, so the hint says so.
		if !strings.Contains(got, "development build") {
			t.Errorf("%s: %q", name, got)
		}
	}

	cmd := NewRootCmd()
	var stderr strings.Builder
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"generate", filepath.Join(dir, "missing.json"), "-o", filepath.Join(dir, "none"), "-p", "none"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("a missing input was accepted")
	}
	if strings.Contains(stderr.String(), runtimeHintPrefix) {
		t.Errorf("a run that failed printed the hint: %q", stderr.String())
	}
}
