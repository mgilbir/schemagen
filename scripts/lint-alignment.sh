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
# --omit-empty=false is missing on purpose: it does not currently produce code
# that compiles (a `!= nil` emitted against a non-pointer field), and the
# analyzer needs a package that type-checks. That is a defect of its own, filed
# separately; when it is fixed, add the configuration here.
declare -a CONFIGS=(
	"default"
	"bigint|--big-int"
	"exact|--exact-numbers"
	"strict-properties|--strict-properties"
	"strict-read-write|--strict-read-write"
	"format-assertion|--format-assertion"
	"hybrid|--validation|hybrid"
	"runtime|--validation|runtime"
	"combined|--big-int|--exact-numbers|--strict-properties|--strict-read-write|--format-assertion|--validation|hybrid"
)

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
if ! (cd "$workdir" && GOFLAGS=-mod=mod GOBIN="$workdir" go install "$ANALYZER" 2>"$workdir/install.err"); then
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

status=0
for entry in "${CONFIGS[@]}"; do
	IFS='|' read -r -a parts <<<"$entry"
	name=${parts[0]}
	flags=("${parts[@]:1}")

	dir="$workdir/$name"
	mkdir -p "$dir"
	# The generated code imports the ECMA-262 regexp engine, x/net/idna, and --
	# under the runtime and hybrid validation modes -- this repository's own
	# validationruntime package.
	cat >"$dir/go.mod" <<EOF
module alignlint

go 1.23.0

require (
	github.com/mgilbir/goecma262 v0.0.0-20260219184840-8bfa4bb752b0
	github.com/mgilbir/schemagen v0.0.0
	golang.org/x/net v0.38.0
)

require golang.org/x/text v0.24.0 // indirect

replace github.com/mgilbir/schemagen => $repo
EOF
	cp "$repo/go.sum" "$dir/go.sum"

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

if [ "$status" -ne 0 ]; then
	echo
	echo "A finding here is a struct schemagen emits whose fields would cost less in another order."
	echo "For a named type that means pkg/generator/layout.go got it wrong; for an anonymous one"
	echo "(reported as \"struct\") it means the template that declares it writes its fields in the"
	echo "wrong order, since the layout pass does not reach inside a function body."
fi
exit $status
