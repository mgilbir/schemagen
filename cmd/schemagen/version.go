package schemagen

import "runtime/debug"

// version is what `schemagen --version` reports, when the build said so.
//
// It is set at link time, by the Makefile's build and install targets:
//
//	go build -ldflags "-X github.com/mgilbir/schemagen/cmd/schemagen.version=v0.1.0"
//
// Deliberately empty rather than a literal like "0.1.0". A version written into
// the source is a claim that every build carrying that source repeats, and it
// is false from the commit after the release it names until somebody remembers
// to bump it -- so the one build it describes correctly is the release, and
// every other build lies in the same words. That is the version a bug report
// quotes, and it cannot be placed. Empty means "the build did not say", which
// is a thing resolveVersion can act on.
var version string

// devVersion is the answer when nothing can name the build: no linker stamp and
// no module version, which is what a `go build` outside a version control
// checkout leaves -- an extracted tarball, a vendored copy, a `go build` in a
// directory somebody copied the sources into.
//
// A word rather than a number, because the reader has to be able to tell this
// case from a release. A plausible-looking number here would send them looking
// for a tag that does not exist; "dev" says outright that this build is not one
// anybody can look up.
const devVersion = "dev"

// resolveVersion is the string cobra prints for --version. See NewRootCmd.
func resolveVersion() string {
	return pickVersion(version, moduleVersion())
}

// moduleVersion is the version the Go toolchain recorded for the main module in
// this binary, or "" when there is no build information at all.
//
// This is what makes an unstamped build report something better than "dev", and
// two builds depend on it. `go install github.com/mgilbir/schemagen@v0.1.0`
// records the tag the module proxy resolved, so an installed binary reports
// "v0.1.0" -- and that route cannot be stamped by any means, because it never
// runs the Makefile and takes no ldflags. A plain `go build` inside a checkout
// records the version control system's answer instead, a pseudo-version such as
// "v0.0.0-20260817134839-56a8d020cea8+dirty", which names the exact commit and
// whether the tree had uncommitted changes when it was built.
func moduleVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return bi.Main.Version
}

// pickVersion chooses between the two, and is separate from the two functions
// that read the build so that every answer they can produce can be put to it.
//
// The linker stamp wins where there is one: it is the only source that can be
// told what a release is called, and the Makefile sets it from the git tag.
//
// "(devel)" is the third thing Main.Version can hold -- the toolchain's own
// placeholder for a main module it could not put a version on. It is not a
// version, and it is the string a reader is least likely to recognize, so it is
// treated as the absence that it is.
func pickVersion(stamped, module string) string {
	if stamped != "" {
		return stamped
	}
	if module != "" && module != "(devel)" {
		return module
	}
	return devVersion
}
