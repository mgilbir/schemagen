package main

import (
	"os"

	"github.com/mgilbir/schemagen/cmd/schemagen"
)

// The command owns the diagnostic; this function owns the exit status.
//
// Both used to print it. cobra writes "Error: <msg>" from ExecuteC and returns
// the same error, and main printed it again -- so every failure the CLI can
// produce said the same sentence twice, once prefixed and once bare. The longer
// and more specific the message the worse it reads, and this project's are long
// on purpose: which fragment of which document, what to write instead, six named
// things in #316's refusal. Issue #322.
//
// Which half is redundant is not a matter of taste, and deleting whichever line
// leaves the tests green is how the wrong one would go. cobra prints on every
// path that returns a non-nil error from Execute: the command lookup failing
// (with "Run 'schemagen --help' for usage." after it), a flag it cannot parse,
// and RunE returning. The one path where it stays quiet -- a request for help,
// spelled flag.ErrHelp -- returns a nil error, so there is nothing here to print
// for it. There is no failure this function could report that cobra has not
// already reported.
//
// So the print goes from here rather than from cobra, and the reasons are the
// ones a reader would want if they are tempted to put it back:
//
//   - cobra writes to cmd.ErrOrStderr(), which is where every other diagnostic
//     this command emits already goes -- the unmatched --field-map keys, the
//     name-split warnings, the unresolved-ref reports. os.Stderr here is a
//     second stream that cmd.SetErr cannot redirect, so an in-process caller saw
//     the message once and a terminal saw it twice. That is why the existing
//     guards never noticed: they all capture the command's stream.
//   - only cobra knows whether it printed. SilenceErrors, SilenceUsage (which
//     #310 sets partway through RunE) and the ErrHelp case are all its state,
//     and none of it is visible from out here.
//   - the "Error: " prefix is cobra's ErrPrefix, and it is the half a reader
//     recognizes as the failure line.
//
// Exit code 1 for any failure, which is what a caller reads instead.
func main() {
	if err := schemagen.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
