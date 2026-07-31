.PHONY: build test lint clean install fmt vet golden download-test-suite test-external

BINARY := schemagen
MODULE := github.com/mgilbir/schemagen
JSTS_DIR := testdata/external/JSON-Schema-Test-Suite

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

# The suite compiles and runs a generated program per test group, so wall time
# tracks machine load: ~25 min on an idle 16-core box, and it has been observed
# to blow through a 30m limit under load. The timeout is generous on purpose --
# a run killed at the deadline reports no failures and looks like a pass.
test-external: download-test-suite
	SCHEMAGEN_RUN_EXTERNAL=1 go test ./tests/... -run TestExternal -v -count=1 -timeout 90m

clean:
	rm -rf bin/
