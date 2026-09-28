// A module of its own, so that the validator it runs never reaches the
// repository's go.mod. See main.go.
module github.com/mgilbir/schemagen/scripts/number-oracle

go 1.25

require github.com/santhosh-tekuri/jsonschema/v6 v6.0.2

require golang.org/x/text v0.14.0 // indirect
