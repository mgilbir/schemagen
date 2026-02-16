.PHONY: build test lint clean install fmt vet golden

BINARY := schemagen
MODULE := github.com/mgilbir/schemagen

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

clean:
	rm -rf bin/
