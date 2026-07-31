.PHONY: build test lint clean install fmt vet golden download-test-suite download-metaschemas test-external

BINARY := schemagen
MODULE := github.com/mgilbir/schemagen
JSTS_DIR := testdata/external/JSON-Schema-Test-Suite
METASCHEMA_DIR := testdata/external/metaschemas

build:
	go build -o bin/$(BINARY) .

install:
	go install .

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

download-test-suite:
	@if [ ! -d "$(JSTS_DIR)" ]; then \
		echo "Cloning JSON Schema Test Suite..."; \
		mkdir -p testdata/external; \
		git clone https://github.com/json-schema-org/JSON-Schema-Test-Suite.git $(JSTS_DIR); \
	else \
		echo "JSON Schema Test Suite already present at $(JSTS_DIR)"; \
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
test-external: download-test-suite download-metaschemas
	SCHEMAGEN_RUN_EXTERNAL=1 go test ./tests/... -run TestExternal -v -count=1 -timeout 90m

clean:
	rm -rf bin/
