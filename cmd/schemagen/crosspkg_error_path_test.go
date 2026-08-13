package schemagen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMultiPackageErrorPathJoinsAcrossPackages runs generated code from two
// packages and reads the message a nested failure comes out with.
//
// The rule that decides how a message is joined to the path reaching it (see
// jsonPathError in the emitted helpers) is asked of the error by an interface,
// and its method is exported for exactly this configuration: helper blocks are
// written once per destination package, so the referencing package's assertion
// is against its *own* copy of the type. An unexported method belongs to the
// package that declares it and would not satisfy an interface declared next
// door -- the assertion would miss, every nested message would be joined with a
// "." whatever it says, and issues #279 and #280 would be back for anyone
// generating into more than one package.
//
// Single-package output cannot see that: both halves live in one package there,
// so the method's case makes no difference. This is the only shape that can
// fail, which is why it is compiled and run rather than string-matched.
func TestMultiPackageErrorPathJoinsAcrossPackages(t *testing.T) {
	src := t.TempDir()
	// The referenced document is a scalar, so its type has no member to report
	// under and its message is about the value itself -- the case whose join has
	// to be read off the error rather than assumed.
	writeFile(t, filepath.Join(src, "leaf.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/leaf.json",
		"title": "Leaf",
		"type": "string",
		"minLength": 3
	}`)
	writeFile(t, filepath.Join(src, "top.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/top.json",
		"title": "Top",
		"type": "object",
		"properties": {"p": {"$ref": "https://ex.test/leaf.json"}}
	}`)

	out := t.TempDir()
	if err := runGenerateArgs(t, filepath.Join(src, "leaf.json"), filepath.Join(src, "top.json"),
		"-o", out,
		"--schema-package", "https://ex.test/leaf.json=example.com/m/leafpkg",
		"--schema-package", "https://ex.test/top.json=example.com/m/toppkg",
		"--root-name", "leaf.json=Leaf", "--root-name", "top.json=Top",
	); err != nil {
		t.Fatalf("generate: %v", err)
	}

	writeFile(t, filepath.Join(out, "main.go"), `package main

import (
	"encoding/json"
	"fmt"

	"example.com/m/toppkg"
)

func main() {
	var v toppkg.Top
	if err := json.Unmarshal([]byte(`+"`"+`{"p":"ab"}`+"`"+`), &v); err != nil {
		fmt.Print("unmarshal: ", err)
		return
	}
	fmt.Print(v.Validate())
}
`)
	if buildOut, err := buildGenerated(t, out, "example.com/m"); err != nil {
		t.Fatalf("generated multi-package output does not compile: %v\n%s", err, buildOut)
	}

	cmd := exec.Command("go", "run", ".")
	cmd.Dir = out
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	runOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running generated program: %v\n%s", err, string(runOut))
	}
	// The whole message, not a substring: a wrong path is a right path with
	// something extra glued to it, so a containment check passes under the very
	// defect this is written for.
	const want = "p: length 2 is less than minimum 3"
	if got := generatedProgramOutput(runOut); got != want {
		t.Fatalf("cross-package message = %q, want %q -- the referencing package could not read how the message had to be joined", got, want)
	}
}

// generatedProgramOutput is the program's own output with the go tool's
// progress lines dropped. A cold module cache prints "go: downloading ..." onto
// the same stream, which would fail an exact-match assertion for a reason that
// has nothing to do with the message under test.
func generatedProgramOutput(out []byte) string {
	var kept []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "go: ") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}
