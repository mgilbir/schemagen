#!/usr/bin/env bash
#
# Run golang.org/x/tools' fieldalignment analyzer over the whole generated
# corpus, under every generator configuration that changes the shape of a type.
#
# The Go tests already hold the layout to account: TestGeneratedCorpusIsFieldAligned
# measures every struct *declaration* the corpus produces, from the compiled
# types, and agrees with this analyzer by construction. What it cannot reach is a
# struct type declared inside a function body -- reflect has no way to name one
# -- and the generated MarshalJSON and UnmarshalJSON each build one. Six of them
# were misordered when the layout pass first landed, and only the analyzer could
# see it.
#
# So this is the check that covers everything, and it is deliberately not a Go
# test: it needs the analyzer, which is a build of another module, and nothing
# third-party reaches go.mod for the same reason validate-seeds keeps Bowtie out
# of it. Run it after changing a template that declares a struct.
#
# Usage: scripts/lint-alignment.sh [output directory]
#
# With no argument it works in a temporary directory and removes it afterwards.
# Pass one to keep the generated corpus around for a look.

set -u -o pipefail

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# Pinned rather than @latest, for the same reason the JSON Schema Test Suite is
# pinned: a check whose tool changes underneath it reports a delta nobody asked
# for, on a day nobody changed anything.
ANALYZER=golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.49.0

# One entry per generator configuration whose output shape differs. The last is
# everything at once, which is what reaches the combinations no single flag does.
#
# The analyzer needs a package that type-checks, so this sweep is also the only
# thing in CI that compiles the corpus under anything but the default
# configuration. That is not a side effect worth losing: --omit-empty=false is
# here because it changes the Go type of every optional property, which is
# exactly how it came to emit a `!= nil` against a plain string and produce a
# package that did not build for any schema forbidding a property.
#
# Every flag of `schemagen generate` that can change a Go type is here, and
# TestLintAlignmentCoversEveryShapeFlag (tests/corpus) holds this list to the CLI's
# flags, so a new one is a test failure until it is added or its absence is
# explained there. --raw-untyped was missing for a release: it turns every
# untyped position into json.RawMessage, a different type in a different
# place, and nothing compiled or measured that shape. --format-annotation takes
# the time.Time and netip.Addr mapping away from the drafts that assert, and
# --lenient-refs makes schemas generate that are otherwise refused, holding
# their unresolvable refs as `any`.
declare -a CONFIGS=(
	"default"
	"bigint|--big-int"
	"exact|--exact-numbers"
	"raw-untyped|--raw-untyped"
	"no-omit-empty|--omit-empty=false"
	"strict-properties|--strict-properties"
	"strict-read-write|--strict-read-write"
	"format-assertion|--format-assertion"
	"format-annotation|--format-annotation"
	"lenient-refs|--lenient-refs"
	"hybrid|--validation|hybrid"
	"runtime|--validation|runtime"
	"combined|--big-int|--exact-numbers|--raw-untyped|--omit-empty=false|--strict-properties|--strict-read-write|--format-assertion|--lenient-refs|--validation|hybrid"
)

# The builds below are of throwaway modules, and the caller's GOFLAGS is a
# setting for the builds they asked for, not for these: -mod=vendor fails every
# one of them, and -race or a build tag changes what is measured. GOWORK is
# off for the same reason, and -trimpath makes a second run's compilations
# hits in the build cache rather than another copy of the corpus in it.
export GOFLAGS=-trimpath
export GOWORK=off

workdir=${1:-}
cleanup=false
if [ -z "$workdir" ]; then
	workdir=$(mktemp -d) || exit 1
	cleanup=true
fi
trap '[ "$cleanup" = true ] && rm -rf "$workdir"' EXIT

echo "building schemagen..."
binary="$workdir/schemagen"
(cd "$repo" && go build -o "$binary" .) || exit 1

echo "fetching the analyzer..."
analyzer="$workdir/fieldalignment"
if ! (cd "$workdir" && GOFLAGS="-mod=mod -trimpath" GOBIN="$workdir" go install "$ANALYZER" 2>"$workdir/install.err"); then
	echo "could not build $ANALYZER:"
	cat "$workdir/install.err"
	echo
	echo "It is fetched rather than vendored, so this needs the module proxy to be reachable."
	exit 1
fi

mapfile -t schemas < <(find "$repo/testdata/schemas" -name '*.json' | sort)
if [ "${#schemas[@]}" -eq 0 ]; then
	echo "no schemas found under $repo/testdata/schemas; the check would pass by measuring nothing"
	exit 1
fi

# The versions of the modules generated code builds against are the runtime
# module's own, read from runtime/go.mod rather than written out here: generated
# code imports the runtime and nothing else, so the runtime's requirements are
# the whole graph. The go.sum copied below is the runtime's, and a version named
# only in this file went stale once already, leaving the corpus built against an
# engine nothing else used.
modversion() {
	(cd "$repo/runtime" && go list -m -f '{{.Version}}' "$1")
}
ecma_version=$(modversion github.com/mgilbir/goecma262) || exit 1
xnet_version=$(modversion golang.org/x/net) || exit 1
xtext_version=$(modversion golang.org/x/text) || exit 1

status=0
for entry in "${CONFIGS[@]}"; do
	IFS='|' read -r -a parts <<<"$entry"
	name=${parts[0]}
	flags=("${parts[@]:1}")

	dir="$workdir/$name"
	mkdir -p "$dir"
	# The generated code imports the runtime module, replaced onto this
	# checkout's ./runtime so the corpus builds against the runtime being
	# changed; the engine and x/net/idna are the runtime's own requirements and
	# are listed so the module builds offline. It never imports the main module,
	# which is why nothing here requires it.
	cat >"$dir/go.mod" <<EOF
module alignlint

go 1.23.0

require (
	github.com/mgilbir/goecma262 $ecma_version
	github.com/mgilbir/schemagen/runtime v0.0.0
	golang.org/x/net $xnet_version
)

require golang.org/x/text $xtext_version // indirect

replace github.com/mgilbir/schemagen/runtime => $repo/runtime
EOF
	cp "$repo/runtime/go.sum" "$dir/go.sum"

	emitted=0
	i=0
	for schema in "${schemas[@]}"; do
		i=$((i + 1))
		pkg=$(printf "p%04d" "$i")
		if "$binary" generate "$schema" -o "$dir/$pkg" -p "$pkg" "${flags[@]}" >/dev/null 2>&1; then
			emitted=$((emitted + 1))
			echo "$pkg $schema" >>"$dir/packages.txt"
		else
			# A schema this generator refuses is a legitimate answer; half the
			# adversarial corpus is malformed on purpose.
			rm -rf "${dir:?}/$pkg"
		fi
	done
	if [ "$emitted" -eq 0 ]; then
		echo "$name: the generator emitted nothing; this configuration measures nothing"
		status=1
		continue
	fi

	(cd "$dir" && go mod tidy >/dev/null 2>&1)
	if ! (cd "$dir" && go build ./... >"$dir/build.err" 2>&1); then
		echo "$name: the generated corpus does not compile, so it cannot be analyzed:"
		head -20 "$dir/build.err"
		status=1
		continue
	fi

	findings="$dir/findings.txt"
	(cd "$dir" && "$analyzer" ./... >"$findings" 2>&1)
	count=$(wc -l <"$findings")
	if [ "$count" -eq 0 ]; then
		printf '%-20s ok (%d packages)\n' "$name:" "$emitted"
		continue
	fi
	printf '%-20s %d findings across %d packages\n' "$name:" "$count" "$emitted"
	# Name the schema rather than the temp directory, so a finding points at a
	# file someone can open.
	while read -r pkg schema; do
		sed -i "s|$dir/$pkg/|$schema -> |g" "$findings"
	done <"$dir/packages.txt"
	sed 's/^/  /' "$findings"
	status=1
done

# The runtime module's own types. They used to be generated into every package
# and were measured with the corpus; they are source now, and this is what still
# measures them. Test files are left out: a table's unkeyed literals fix its
# field order.
runtime_findings="$workdir/runtime-findings.txt"
(cd "$repo/runtime" && "$analyzer" -test=false ./... >"$runtime_findings" 2>&1)
runtime_count=$(wc -l <"$runtime_findings")
if [ "$runtime_count" -eq 0 ]; then
	printf '%-20s ok\n' "runtime module:"
else
	printf '%-20s %d findings\n' "runtime module:" "$runtime_count"
	sed 's/^/  /' "$runtime_findings"
	status=1
fi

if [ "$status" -ne 0 ]; then
	echo
	echo "A finding here is a struct schemagen emits whose fields would cost less in another order."
	echo "For a named type that means pkg/generator/layout.go got it wrong; for an anonymous one"
	echo "(reported as \"struct\") it means the template that declares it writes its fields in the"
	echo "wrong order, since the layout pass does not reach inside a function body."
fi
exit $status
