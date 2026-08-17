package schemagen

import "testing"

// The requirement that made this a link-time value rather than a constant. A
// `go test` build passes no -ldflags, so version is whatever the source says --
// and the source must say nothing. Someone writing "0.1.0" here to make
// --version look right would break no other guard: -X overrides an initializer
// as readily as an empty default, so the release build would still be correct
// and every build after the release would quietly claim to be it.
func TestTheVersionVariableCarriesNoLiteralDefault(t *testing.T) {
	if version != "" {
		t.Errorf("version defaults to %q in the source; a version literal is a claim every build carrying it repeats, and only one of them is the release -- leave it empty and let the build say", version)
	}
}

// Every answer the two sources can give, put to the choice between them. The
// binary-level guards in version_test.go at the repository root exercise one
// column of this table each -- whichever the machine running them happens to
// produce -- and cannot reach the rest: no test can arrange for `go install
// ...@v0.1.0` or for a build with no version control information behind it.
func TestPickVersionPrefersTheStampThenTheModule(t *testing.T) {
	tests := []struct {
		name    string
		stamped string
		module  string
		want    string
	}{{
		name:    "the Makefile stamped a release tag",
		stamped: "v0.1.0",
		module:  "v0.0.0-20260817134839-56a8d020cea8",
		want:    "v0.1.0",
	}, {
		// A stamped build in a checkout has both, and the stamp is the one
		// that was told what the release is called.
		name:    "the stamp outranks a pseudo-version",
		stamped: "v0.1.0-3-gabc1234-dirty",
		module:  "v0.0.0-20260817134839-56a8d020cea8+dirty",
		want:    "v0.1.0-3-gabc1234-dirty",
	}, {
		// `go install github.com/mgilbir/schemagen@v0.1.0`, which runs no
		// Makefile and can pass no ldflags.
		name:   "an installed binary reports the tag it was installed from",
		module: "v0.1.0",
		want:   "v0.1.0",
	}, {
		// A plain `go build` in this checkout. Names the commit, and says
		// whether the tree was clean.
		name:   "a local build reports the commit it came from",
		module: "v0.0.0-20260817134839-56a8d020cea8+dirty",
		want:   "v0.0.0-20260817134839-56a8d020cea8+dirty",
	}, {
		// A build from an extracted tarball or a copied directory: a main
		// module the toolchain could not put a version on.
		//
		// The want is spelled out rather than written as devVersion, here and
		// below. Comparing the constant against itself would pass whatever it
		// held, including the release number this whole arrangement exists to
		// keep out of the source -- the guard has to name what an unidentified
		// build is allowed to say.
		name:   "the toolchain's placeholder is not a version",
		module: "(devel)",
		want:   "dev",
	}, {
		name: "no stamp and no build information at all",
		want: "dev",
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickVersion(tc.stamped, tc.module); got != tc.want {
				t.Errorf("pickVersion(%q, %q) = %q, want %q", tc.stamped, tc.module, got, tc.want)
			}
		})
	}
}
