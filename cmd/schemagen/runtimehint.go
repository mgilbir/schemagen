package schemagen

import (
	"fmt"
	"io"
	"regexp"
)

// runtimeModule is the module every generated file imports. It is the
// generator's own constant restated here for the message, and a test holds the
// two together.
const runtimeModule = "github.com/mgilbir/schemagen/runtime"

// runtimeHintPrefix opens the hint. It is "hint:" and not "note:" or "warning:":
// the other two are what the run says about the schemas, and this is what it says
// about the code it wrote.
const runtimeHintPrefix = "hint: the generated code imports "

// releaseVersion matches a version a release is tagged with: v1.2.3, or a
// pre-release such as v1.2.3-rc.1. What `git describe` writes for a build after
// a tag (v1.2.3-5-gabc1234, -dirty) and what the toolchain writes for a build
// from a checkout (v0.0.0-20260817134839-56a8d020cea8+dirty) do not match: a
// pre-release here has to start with a letter, and build metadata is not
// allowed. Neither of those names a tag, so neither names a runtime release.
var releaseVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[A-Za-z][0-9A-Za-z.]*)?$`)

// runtimeHint is the note a successful run ends with: generated code imports the
// runtime module, and the module that holds it has to require a runtime at least
// as new as this schemagen.
//
// A release names the version to ask for, the one released together with it (the
// tag runtime/vX.Y.Z, which `go get ...@vX.Y.Z` finds). A build that is not a
// release has no runtime release to name, and pretending it had would send the
// reader to a version that does not exist; it says so, and says what to do
// instead.
func runtimeHint(version string) string {
	if releaseVersion.MatchString(version) {
		return fmt.Sprintf(runtimeHintPrefix+"%s; the module that holds it needs it: go get %s@%s\n",
			runtimeModule, runtimeModule, version)
	}
	return fmt.Sprintf(runtimeHintPrefix+"%s; this schemagen is a development build (%s), so no runtime release matches it -- "+
		"require the runtime from the same checkout (replace %s => <checkout>/runtime) or generate with a released schemagen\n",
		runtimeModule, version, runtimeModule)
}

// printRuntimeHint writes the runtime note, once.
func printRuntimeHint(w io.Writer) {
	fmt.Fprint(w, runtimeHint(resolveVersion()))
}
