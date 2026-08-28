.PHONY: build test lint clean install fmt vet golden download-test-suite test-suite-drift download-metaschemas test-external fuzz fuzz-seeds cogen validate-seeds

BINARY := schemagen
MODULE := github.com/mgilbir/schemagen
JSTS_DIR := testdata/external/JSON-Schema-Test-Suite
JSTS_REPO := https://github.com/json-schema-org/JSON-Schema-Test-Suite.git
METASCHEMA_DIR := testdata/external/metaschemas

# The version `schemagen --version` reports, stamped into the binary at link
# time instead of being written into the source. See cmd/schemagen/version.go
# for why a constant in the source would be wrong.
#
# `git describe --tags --always --dirty` is the derivation: "v0.1.0" standing on
# the release tag, "v0.1.0-3-gabc1234" three commits past it, and either with
# "-dirty" appended when the tree has uncommitted changes -- so a build that is
# not the release cannot claim to be one. Before the first tag exists, which is
# the state of this repository today, --always falls back to the abbreviated
# commit, which still names the build exactly.
#
# It can also produce nothing: a source tree that is no longer a git checkout,
# or a machine with no git on it. The stamp is then dropped rather than passed
# empty, and the binary answers from what the Go toolchain recorded about
# itself, which is a real version for `go install ...@v0.1.0` and a
# commit-naming pseudo-version for a build made inside a checkout. "dev" is what
# comes out only when neither source knows anything.
#
# Override for a build that must claim a particular version:
#
#	make build VERSION=v0.1.0
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null)
GO_LDFLAGS := $(if $(VERSION),-X $(MODULE)/cmd/schemagen.version=$(VERSION))

build:
	go build -ldflags "$(GO_LDFLAGS)" -o bin/$(BINARY) .

install:
	go install -ldflags "$(GO_LDFLAGS)" .

test:
	go test ./... -v -count=1

test-short:
	go test ./... -short -count=1

golden:
	UPDATE_GOLDEN=true go test ./tests/... -v -count=1

fmt:
	go fmt ./...

vet:
	go vet ./...

lint: fmt vet

# The one commit of the JSON Schema Test Suite every run measures against.
#
# It has to be pinned because the compliance gate reports absolute numbers -- a
# coverage floor, a rejection count, an allow-list that must describe exactly
# the groups it names -- and every one of those is a statement about a specific
# corpus. Before this pin existed the target cloned the default branch and then
# never touched the checkout again, so what a machine measured was whatever
# upstream HEAD happened to be on the day it first cloned, and nothing ever said
# so. Two developers then compare figures that were never comparable: that cost
# a session two hours, including a "coverage regression" that was a newer corpus
# arriving on one machine and not the other.
#
# So the pin is enforced rather than merely recorded. The recipe below moves an
# existing checkout onto JSTS_COMMIT, fetching if it has to, and refuses to run
# against a modified one -- a suite checkout with local edits is a corpus nobody
# else has.
#
# To bump it: `make test-suite-drift` prints how far behind upstream this is and
# the exact line to paste back here. Then re-run `make test-external` and triage
# the delta before committing, because bumping the corpus is what makes every
# figure in tests/external_known_failures.go and minValidatedGroups stale.
JSTS_COMMIT := cc73f5fa64c3b0d11f6c277db4edc22938994b54

download-test-suite:
	@if [ ! -d "$(JSTS_DIR)/.git" ]; then \
		if [ -e "$(JSTS_DIR)" ]; then \
			echo "$(JSTS_DIR) exists but is not a git checkout, so its contents cannot be pinned."; \
			echo "Remove it and re-run: rm -rf $(JSTS_DIR)"; \
			exit 1; \
		fi; \
		echo "Cloning JSON Schema Test Suite..."; \
		mkdir -p testdata/external; \
		git clone $(JSTS_REPO) "$(JSTS_DIR)" || exit 1; \
	fi
	@if [ -n "$$(git -C "$(JSTS_DIR)" status --porcelain)" ]; then \
		echo "$(JSTS_DIR) has local modifications, so it is not the corpus JSTS_COMMIT names."; \
		echo "Discard them and re-run: git -C $(JSTS_DIR) reset --hard && git -C $(JSTS_DIR) clean -fd"; \
		exit 1; \
	fi
	@if [ "$$(git -C "$(JSTS_DIR)" rev-parse HEAD)" != "$(JSTS_COMMIT)" ]; then \
		echo "Moving JSON Schema Test Suite to $(JSTS_COMMIT)..."; \
		if ! git -C "$(JSTS_DIR)" cat-file -e "$(JSTS_COMMIT)^{commit}" 2>/dev/null; then \
			unshallow=; \
			if [ -f "$(JSTS_DIR)/$$(git -C "$(JSTS_DIR)" rev-parse --git-dir)/shallow" ] || \
			   [ -f "$$(git -C "$(JSTS_DIR)" rev-parse --git-dir)/shallow" ]; then \
				unshallow=--unshallow; \
			fi; \
			git -C "$(JSTS_DIR)" fetch $$unshallow $(JSTS_REPO) || \
				{ echo "could not fetch $(JSTS_REPO)"; exit 1; }; \
		fi; \
		git -C "$(JSTS_DIR)" checkout --quiet --detach "$(JSTS_COMMIT)" || \
			{ echo "commit $(JSTS_COMMIT) is not in $(JSTS_REPO)"; exit 1; }; \
	fi
	@echo "JSON Schema Test Suite pinned at $(JSTS_COMMIT)"

# Reports how far JSTS_COMMIT has fallen behind upstream, and prints the line to
# paste into this file to bump it.
#
# Informational on purpose: it exits 0 whatever it finds. Upstream lands commits
# most weeks, so a target that failed on any drift at all would be red almost
# always, and a check that is always red is a check nobody reads -- which is the
# state that let the pin go six months and 87 commits stale in the first place.
# A nightly CI job that runs this and surfaces the count is the intended use; the
# decision to bump stays a person's, because bumping re-baselines the gate.
test-suite-drift: download-test-suite
	@git -C "$(JSTS_DIR)" fetch --quiet $(JSTS_REPO) main || { echo "could not fetch $(JSTS_REPO)"; exit 1; }
	@upstream=$$(git -C "$(JSTS_DIR)" rev-parse FETCH_HEAD); \
	if [ "$$upstream" = "$(JSTS_COMMIT)" ]; then \
		echo "JSTS_COMMIT is upstream main; nothing to bump."; \
	else \
		behind=$$(git -C "$(JSTS_DIR)" rev-list --count "$(JSTS_COMMIT)..$$upstream"); \
		when=$$(git -C "$(JSTS_DIR)" log -1 --format=%cs "$$upstream"); \
		echo "JSTS_COMMIT is $$behind commit(s) behind upstream main ($$when)."; \
		echo "To bump, replace the line in the Makefile with:"; \
		echo ""; \
		echo "JSTS_COMMIT := $$upstream"; \
		echo ""; \
		echo "then re-run 'make test-external' and triage the delta before committing."; \
	fi

# Meta-schemas the suite refers to but does not ship. They are fetched rather
# than vendored, like the suite itself: the spec repo is dual-licensed
# (BSD-3-Clause and AFL-3.0) and the documents carry no embedded copyright
# notice, so redistributing them would mean hand-maintaining an attribution
# file. Downloading makes us a consumer instead.
#
# Each document declares its own $id, so the loader keys on that rather than on
# the filename -- the download URL and the URI the resolver looks up are the
# same string by construction, and cannot drift apart.
METASCHEMA_URLS := \
	https://json-schema.org/draft/2020-12/schema \
	https://json-schema.org/draft/2020-12/meta/core \
	https://json-schema.org/draft/2020-12/meta/applicator \
	https://json-schema.org/draft/2020-12/meta/unevaluated \
	https://json-schema.org/draft/2020-12/meta/validation \
	https://json-schema.org/draft/2020-12/meta/meta-data \
	https://json-schema.org/draft/2020-12/meta/format-annotation \
	https://json-schema.org/draft/2020-12/meta/content \
	https://json-schema.org/draft/2019-09/schema \
	https://json-schema.org/draft/2019-09/meta/core \
	https://json-schema.org/draft/2019-09/meta/applicator \
	https://json-schema.org/draft/2019-09/meta/validation \
	https://json-schema.org/draft/2019-09/meta/meta-data \
	https://json-schema.org/draft/2019-09/meta/format \
	https://json-schema.org/draft/2019-09/meta/content \
	http://json-schema.org/draft-07/schema \
	http://json-schema.org/draft-06/schema \
	http://json-schema.org/draft-04/schema \
	http://json-schema.org/draft-03/schema

download-metaschemas:
	@if [ -d "$(METASCHEMA_DIR)" ]; then \
		echo "Meta-schemas already present at $(METASCHEMA_DIR)"; \
	else \
		echo "Downloading JSON Schema meta-schemas..."; \
		mkdir -p "$(METASCHEMA_DIR).tmp"; \
		for url in $(METASCHEMA_URLS); do \
			name=$$(echo "$$url" | sed -e 's|^https\{0,1\}://||' -e 's|/|_|g'); \
			if ! curl -fsSL --retry 3 --max-time 30 "$$url" -o "$(METASCHEMA_DIR).tmp/$$name.json"; then \
				echo "failed to download $$url"; \
				rm -rf "$(METASCHEMA_DIR).tmp"; \
				exit 1; \
			fi; \
		done; \
		mv "$(METASCHEMA_DIR).tmp" "$(METASCHEMA_DIR)"; \
		echo "Downloaded $(words $(METASCHEMA_URLS)) meta-schemas to $(METASCHEMA_DIR)"; \
	fi

# The suite compiles and runs a generated program per test group, so wall time
# tracks machine load: ~25 min on an idle 16-core box, and it has been observed
# to blow through a 30m limit under load. The timeout is generous on purpose --
# a run killed at the deadline reports no failures and looks like a pass.
#
# It also needs disk. The ~27,000 compilations go into a build cache under
# TMPDIR rather than into ~/.cache/go-build, which is no place for them.
# Concurrent runs share that one directory -- two full runs measured 1.9G
# between them -- and the last one out deletes it, so the requirement is the
# same for any number of runs rather than that much each. A run refuses to
# start when the volume is below the figure it names, instead of dying halfway
# through and reporting a full volume as a wave of schema failures. Set
# SCHEMAGEN_KEEP_GOCACHE=1 to leave the cache behind for the next run, which
# gets most of those 25 minutes back when the generator has not changed, and
# costs another cache's worth of disk when it has.
test-external: download-test-suite download-metaschemas
	SCHEMAGEN_RUN_EXTERNAL=1 go test ./tests/... -run TestExternal -v -count=1 -timeout 90m

# Fuzzing has no natural end: `go test -fuzz` keeps mutating inputs until it
# finds a crash or something kills it, so a run without -fuzztime never returns
# and cannot sit in a script. FUZZTIME is that bound. The 60s default is sized
# for someone who wants a quick sanity pass before pushing; override it for a
# real hunt with `make fuzz FUZZTIME=10m`. Nightly CI passes a much larger
# value, which is where the actual searching gets done.
#
# Budget accordingly: the seed corpus is large enough that go spends roughly
# the first 25s of any run just gathering baseline coverage before it mutates
# anything, and that time comes out of FUZZTIME. At the 60s default only about
# half the run is actually fuzzing, so anything below ~30s is a smoke test that
# the harness still builds, not a search.
#
# Deliberately NOT a dependency of `test`: fuzzing is a search, not an
# assertion. It spends its whole budget every time, finds nothing on a tree
# that is already clean, and would make `make test` cost a fixed FUZZTIME for
# no added signal. The regression half is already covered -- FuzzGenerate walks
# testdata/schemas for seeds, so every reproducer under
# testdata/schemas/adversarial replays as an ordinary test case under plain
# `go test ./...`. So a bug found once stays caught without anyone re-running
# the search.
#
# Minimise crashers by hand rather than committing what go test leaves in
# tests/testdata/fuzz/: when a crash kills the minimizer, the file saved is the
# last input *sent*, which is usually truncated and reproduces nothing. A
# readable .json under testdata/schemas/adversarial is the better artifact, and
# `make validate-seeds` can then confirm it is a legal document.
#
# -run '^$$' skips the package's ordinary tests; the fuzzing phase is selected
# by -fuzz alone and is unaffected. -fuzztime is also not clipped by -timeout:
# the test binary stops its timeout alarm before entering the fuzzing loop.
FUZZTIME ?= 60s

# Depends on the suite for its seeds. FuzzGenerate takes the schema of every
# JSON Schema Test Suite group as a seed when the suite is present, and simply
# does without when it is not -- so a run in a bare checkout silently searches a
# fifth of the corpus. That is exactly what the first nightly did: 410 unique
# schemas against 1956 locally, missing all 2241 suite schemas, which are the
# most realistic shapes available. Making the dependency explicit stops the
# weaker run from looking like the same run.
#
# fuzz-seeds runs first, and the ordering is the point. `go test -fuzz` gathers
# baseline coverage over every seed before it mutates anything, and a seed the
# worker process cannot survive stops the whole run there -- reported as
# "fuzzing process hung or terminated unexpectedly: exit status 2" against a
# corpus *position*, with the worker's stderr discarded and no crasher file
# written. That is what issue #233 was, and read cold it says nothing about
# which seed or why. fuzz-seeds asks the same question in a form that answers
# it, in about fifteen seconds, before FUZZTIME is committed to a search that
# cannot start.
fuzz: download-test-suite fuzz-seeds
	go test ./tests/... -run '^$$' -fuzz '^FuzzGenerate$$' -fuzztime $(FUZZTIME)

# The seed corpus, replayed under both of the limits a fuzz worker imposes and
# cannot report on: the ten-second per-input deadline (...WorkerDeadline, timed)
# and memory (...MemoryCeiling, sampled). This is what `go test ./...` runs too --
# it is here so that a fuzz run reports a bad seed as a bad seed rather than as a
# dead worker, and the memory half is here for the same reason the time half is:
# a seed that allocates without bound dies as `fatal error: out of memory`, which
# takes the worker with it and leaves the coordinator with nothing to say.
fuzz-seeds:
	go test ./tests/... -run '^(FuzzGenerate|TestFuzzSeedCorpusFitsTheWorkerDeadline|TestFuzzSeedCorpusFitsTheMemoryCeiling)$$' -count=1

# Layer 2 of the fuzzing effort. FuzzGenerate only proves the pipeline does not
# panic, which says nothing about whether the code it emits is correct. This
# target builds a JSON Schema and a conforming instance *together*, compiles the
# generated bindings, round-trips the instance through them, and then feeds in
# mutants that each violate exactly one keyword and must be rejected.
#
# The negative half is the point. A generator that silently drops a constraint
# check turns Validate into `return nil`, and every conforming instance still
# passes -- so a corpus of valid documents cannot see the defect at all. Only a
# document that is supposed to be rejected can, which is why the mutants are
# co-generated rather than mutated at random: a random edit is quite likely to
# still be valid, and then a validator that accepts it is right.
#
# Deliberately NOT part of `test`: each iteration compiles and runs a throwaway
# Go module, so the default `go test ./...` would grow from seconds to minutes.
# Same gating style as test-external, and the same reason.
#
# COGEN_SEED and COGEN_ITERS bound the search the way FUZZTIME bounds fuzzing,
# except that this search is reproducible: every iteration derives its schema
# from (seed, iteration index) and nothing else, so a failure prints the exact
# command that replays that one case. Fixing the default seed rather than
# taking the clock means a clean run is evidence about a specific 400 cases and
# a later failure on the same seed is a real regression, not a different draw.
#
# The generator configuration is part of that draw. The CLI can emit rather
# more than one shape of code -- big-integer wrappers, strict property
# checking, value fields instead of pointers, the hybrid and runtime validation
# modes -- and each iteration runs one of them, dealt so that every consecutive
# block of len(coConfigs) iterations covers every configuration exactly once.
# The run prints how many iterations each got, and how many of those emitted
# source that actually differed from the baseline configuration's, which is how
# to tell a configuration that is exercising something from one that is not.
#
# Optional switches, all off by default:
#
#   SCHEMAGEN_COGEN_CONFIG=<name>         pin every iteration to one generator
#                                         configuration instead of dealing
#                                         them. One of: static, hybrid,
#                                         runtime, bigint, strict, noomit,
#                                         lenientrefs, all. This is what a
#                                         failure report names, and what to
#                                         reach for when a defect has been
#                                         narrowed to a flag and the question
#                                         is how far it spreads.
#
#   SCHEMAGEN_COGEN_INCLUDE_KNOWN_GAPS=1  re-admit the constructs the harness
#                                         steps around because schemagen is
#                                         already known to mishandle them.
#                                         Expected to fail: it is what keeps
#                                         each exclusion honest rather than a
#                                         claim in a comment. There are no such
#                                         exclusions at present -- the last of
#                                         them went with issue #92 -- so the
#                                         variable currently changes nothing.
#
#   SCHEMAGEN_COGEN_BOWTIE=1              cross-check every (schema, instance)
#                                         pair against independent JSON Schema
#                                         implementations, as validate-seeds
#                                         does. Needs docker and uv, and costs
#                                         a container round-trip per iteration.
#
COGEN_SEED ?= 1
COGEN_ITERS ?= 400

cogen:
	SCHEMAGEN_RUN_COGEN=1 SCHEMAGEN_COGEN_SEED=$(COGEN_SEED) SCHEMAGEN_COGEN_ITERS=$(COGEN_ITERS) \
		go test ./tests/... -run TestCoGenerated -v -count=1 -timeout 60m

# Checks that every fuzz seed under testdata/schemas/adversarial is a legal
# JSON Schema document, by validating it as an *instance* against the
# meta-schema for its own dialect.
#
# This matters because the corpus mixes two kinds of case. Some seeds are
# deliberately malformed -- the property under test is that the generator
# refuses them gracefully instead of panicking. The rest are meant to be legal
# documents, and there the whole claim is that a *valid* schema crashed the
# tool. Confusing the two silently downgrades a real defect, which has already
# happened twice: four cycle seeds were written in draft-07 or draft-03 syntax
# without declaring $schema, so they were not legal documents at all, and the
# JSON Pointer overflow cases used empty containers 2020-12 forbids, which made
# a crash on entirely valid input look like a malformed-input problem.
#
# Bowtie drives several independent implementations in containers and their
# answers are compared: one library's opinion is not evidence, and a
# disagreement is reported as "unknown" rather than resolved by picking a
# favourite. It also reaches draft-03, which some modern validators drop
# entirely.
#
# Deliberately not part of `make test` and not a Go dependency: it needs docker
# and uv, neither of which should gate the build. Invalid seeds are reported,
# not fatal -- only a seed that nothing could judge fails the check.
validate-seeds: download-metaschemas
	python3 scripts/validate-seeds.py

clean:
	rm -rf bin/
