package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The guards for issue #322: a failing run prints its diagnostic once.
//
// They run the built binary rather than calling NewRootCmd().Execute() in
// process, and that is the whole reason the defect survived. cobra writes to
// cmd.ErrOrStderr(); main.go used to write the same error to os.Stderr as well.
// Every existing guard in cmd/schemagen captures the first stream with
// cmd.SetErr, so the second copy was invisible to all of them -- the message
// appeared once to a test and twice to anyone running the command. Only a real
// process with a real stderr can see both.
//
// They assert the *count*, not the content. A test that greps for the message
// passes whether it appears once or twice, which is how a doubled diagnostic
// went unnoticed while the project rewrote three of these messages for clarity.

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// schemagenBinary builds the command once per test binary and returns its path.
func schemagenBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "schemagen-main-test")
		if err != nil {
			binErr = err
			return
		}
		path := filepath.Join(dir, "schemagen")
		build := exec.Command("go", "build", "-o", path, ".")
		if out, err := build.CombinedOutput(); err != nil {
			binErr = err
			t.Logf("go build: %s", out)
			return
		}
		binPath = path
	})
	if binErr != nil {
		t.Fatalf("building the command under test: %v", binErr)
	}
	return binPath
}

// runFailing runs the command and returns its stderr, insisting it failed: a
// diagnostic that was never emitted trivially appears once, and a guard counting
// it would then pass on a run that did the opposite of what it describes.
func runFailing(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(schemagenBinary(t), args...)
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = new(strings.Builder)
	err := cmd.Run()
	if err == nil {
		t.Fatalf("schemagen %s exited 0, want a failure\nstderr:\n%s",
			strings.Join(args, " "), stderr.String())
	}
	return stderr.String()
}

// countLinesContaining is the count the guards assert, over whole lines: the two
// copies of the message differ only in cobra's "Error: " prefix, so a substring
// count over the joined output is the same number either way.
func countLinesContaining(out, needle string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			n++
		}
	}
	return n
}

// A flag the command does not define. This is the usage path: RunE never runs,
// so #310's SilenceUsage is never set and cobra prints the usage block under the
// error. The diagnostic itself must still appear once.
func TestAFlagErrorPrintsItsDiagnosticOnce(t *testing.T) {
	stderr := runFailing(t, "generate", "--no-such-flag")

	const msg = "unknown flag: --no-such-flag"
	if got := countLinesContaining(stderr, msg); got != 1 {
		t.Errorf("the diagnostic appears on %d lines, want 1:\n%s", got, stderr)
	}
	// The surviving copy is cobra's, so it carries the prefix that marks it as
	// the failure line rather than as one more line of usage.
	if !strings.Contains(stderr, "Error: "+msg) {
		t.Errorf("stderr does not carry cobra's %q prefix on the diagnostic:\n%s", "Error: ", stderr)
	}
}

// An error raised after RunE has begun reading schemas. This is the non-usage
// path: #310 sets SilenceUsage before the first document is read, so cobra
// prints the error and no usage block. The count must be the same one.
func TestAGenerationErrorPrintsItsDiagnosticOnce(t *testing.T) {
	stderr := runFailing(t, "generate", "no-such-schema.json", "-o", "out", "-p", "m")

	const msg = "no-such-schema.json"
	if got := countLinesContaining(stderr, msg); got != 1 {
		t.Errorf("the diagnostic appears on %d lines, want 1:\n%s", got, stderr)
	}
	if !strings.Contains(stderr, "Error: ") {
		t.Errorf("stderr does not carry cobra's %q prefix:\n%s", "Error: ", stderr)
	}
	// The two halves of #310: this path suppresses usage, and the flag path
	// above does not. A fix that silenced cobra's errors instead of main's would
	// leave this line and take the message.
	if strings.Contains(stderr, "Usage:") {
		t.Errorf("a generation failure printed the usage block, which #310 suppresses:\n%s", stderr)
	}
}

// A refusal raised before RunE reads anything, checked because the usage block
// is still printed there and a doubled message is hardest to see underneath it.
func TestARefusalBeforeTheSchemasArePrintedOnce(t *testing.T) {
	stderr := runFailing(t, "generate", "a.json", "-o", "out", "-p", "m",
		"--format-assertion", "--format-annotation")

	const msg = "--format-assertion and --format-annotation are opposites"
	if got := countLinesContaining(stderr, msg); got != 1 {
		t.Errorf("the diagnostic appears on %d lines, want 1:\n%s", got, stderr)
	}
}

// An unknown subcommand, which cobra refuses from ExecuteC before any command
// runs -- the one failure path that does not go through cmd.execute at all.
func TestAnUnknownSubcommandPrintsItsDiagnosticOnce(t *testing.T) {
	stderr := runFailing(t, "generat")

	const msg = `unknown command "generat" for "schemagen"`
	if got := countLinesContaining(stderr, msg); got != 1 {
		t.Errorf("the diagnostic appears on %d lines, want 1:\n%s", got, stderr)
	}
}

// runFailingIn is runFailing with the working directory chosen by the caller, so
// a fixture can be written next to the command's own cwd and named relatively.
func runFailingIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(schemagenBinary(t), args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = new(strings.Builder)
	if err := cmd.Run(); err == nil {
		t.Fatalf("schemagen %s exited 0, want a failure\nstderr:\n%s",
			strings.Join(args, " "), stderr.String())
	}
	return stderr.String()
}

// Issue #307, from the outside. A $dynamicRef naming an anchor no document
// declares was refused with a message that said "$ref" -- a keyword the schema
// does not contain -- and then advised passing the referenced document as an
// input, which for a bare fragment is the document already being read.
//
// Asserted from the built binary and not from NewRootCmd().Execute(), for the
// reason the guards above exist: this message crosses a stream boundary. It is
// cobra that writes it, to a stream cmd.SetErr can redirect, and main.go once
// wrote a second copy to an os.Stderr that it cannot -- so an in-process reading
// of this text is a reading of one of the two copies, and says nothing about
// what a terminal shows. The count is asserted for the same reason.
func TestAnUnresolvedDynamicRefNamesItsKeywordOnStderr(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "dyn.json")
	if err := os.WriteFile(fixture, []byte(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/dyn.json",
		"title": "DynDoc",
		"type": "object",
		"properties": {"p": {"$dynamicRef": "#nowhere"}}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	stderr := runFailingIn(t, dir, "generate", "dyn.json", "-o", "out", "-p", "m")

	const msg = `cannot resolve $dynamicRef "#nowhere"`
	if got := countLinesContaining(stderr, msg); got != 1 {
		t.Errorf("the diagnostic appears on %d lines, want 1:\n%s", got, stderr)
	}
	// The keyword the document does not write must not be in the sentence.
	if strings.Contains(stderr, "cannot resolve $ref ") {
		t.Errorf("stderr calls a $dynamicRef a $ref:\n%s", stderr)
	}
	// The advice that is false here: there is no further document to pass.
	if strings.Contains(stderr, "pass the referenced document as an input too") {
		t.Errorf("stderr advises supplying a document that is already the input:\n%s", stderr)
	}
	if !strings.Contains(stderr, "names a location inside the document that already holds it") {
		t.Errorf("stderr does not carry the advice that is true here:\n%s", stderr)
	}
}
