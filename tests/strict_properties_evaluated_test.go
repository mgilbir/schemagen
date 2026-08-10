package tests

import (
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// TestStrictPropertiesReachesTheEvaluatedPositions is issue #221.
//
// --strict-properties reads "absent additionalProperties is treated as false",
// and it was read on the static path only. A sub-schema the static reading
// declines is compiled to schema data and interpreted at validation time, and
// that lowering carried no ban at all -- so one object refused an undeclared key
// where it compiled to a struct and accepted one where it compiled to a node,
// from the same flag and the same generator run. Four positions in the sweep
// that found it were the second kind: `then`, `else`, `dependentSchemas` and a
// doubly-negated `not`.
//
// The rejections are the point, so this asserts them from Validate rather than
// through runValidationCasesOn, which accepts a decode-time refusal as an
// acceptable failure mode and would pass on a type that could not hold the
// document at all.
//
// The conforming documents are as load-bearing as the rejections. A ban
// synthesised once per allOf branch rather than once on the object the branches
// are pooled into would refuse every one of them, and no document would satisfy
// the composition -- which is why `pooled` is here beside the four positions.
func TestStrictPropertiesReachesTheEvaluatedPositions(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func main() {
	// An undeclared key is refused at every position, and by Validate rather
	// than by the decoder: the generated UnmarshalJSON keeps an undeclared
	// property so that a document carrying one still round-trips, so a decode
	// that refused it would have traded round-trip fidelity for the check.
	for _, doc := range []string{
		` + "`" + `{"viaThen":{"a":1,"x":2}}` + "`" + `,
		` + "`" + `{"viaElse":{"a":1,"x":2}}` + "`" + `,
		` + "`" + `{"viaDependent":{"a":1,"x":2}}` + "`" + `,
		` + "`" + `{"viaNotNot":{"a":1,"x":2}}` + "`" + `,
		` + "`" + `{"pooled":{"a":1,"b":2,"x":3}}` + "`" + `,
	} {
		var v StrictEvaluatedPositions
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			fail("the decoder refused %s; the overflow map exists so it does not: %v", doc, err)
		}
		if err := v.Validate(); err == nil {
			fail("--strict-properties accepted an undeclared key: %s", doc)
		}
	}

	// And every conforming document is still accepted. A ban applied once per
	// allOf branch would refuse "pooled" outright, and one applied inside the
	// "if" would move which documents take the "then".
	for _, doc := range []string{
		` + "`" + `{}` + "`" + `,
		` + "`" + `{"viaThen":{"a":1}}` + "`" + `,
		` + "`" + `{"viaElse":{"a":1}}` + "`" + `,
		` + "`" + `{"viaDependent":{"a":1}}` + "`" + `,
		` + "`" + `{"viaDependent":{}}` + "`" + `,
		` + "`" + `{"viaNotNot":{"a":1}}` + "`" + `,
		` + "`" + `{"pooled":{"a":1,"b":2}}` + "`" + `,
		` + "`" + `{"pooled":{"a":1}}` + "`" + `,
	} {
		var v StrictEvaluatedPositions
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			fail("decoding %s: %v", doc, err)
		}
		if err := v.Validate(); err != nil {
			fail("--strict-properties refused a conforming document %s: %v", doc, err)
		}
	}

	// The undeclared key is still there to be written back out. That is what
	// the overflow map is for, and it is the reason the refusal is a verdict
	// rather than a decode failure.
	var v StrictEvaluatedPositions
	if err := json.Unmarshal([]byte(` + "`" + `{"viaThen":{"a":1,"x":2}}` + "`" + `), &v); err != nil {
		fail("decoding the round-trip document: %v", err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		fail("marshaling: %v", err)
	}
	if !strings.Contains(string(out), ` + "`" + `"x":2` + "`" + `) {
		fail("the undeclared key was dropped rather than captured: %s", out)
	}

	fmt.Println("PASS")
}
`
	runGeneratedMainProgramWithConfig(t,
		"testdata/schemas/regression/strict_evaluated_positions.json",
		"strict_properties_evaluated_test",
		mainGo,
		generator.Config{PackageName: "testpkg", OmitEmpty: true, StrictProperties: true},
	)
}

// TestEvaluatedPositionsAreUnbannedByDefault is the other setting of the same
// matrix. Without the flag the same document is accepted at every position, so
// the rejections above are the flag's doing and not a keyword the schema states.
func TestEvaluatedPositionsAreUnbannedByDefault(t *testing.T) {
	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func main() {
	for _, doc := range []string{
		` + "`" + `{"viaThen":{"a":1,"x":2}}` + "`" + `,
		` + "`" + `{"viaElse":{"a":1,"x":2}}` + "`" + `,
		` + "`" + `{"viaDependent":{"a":1,"x":2}}` + "`" + `,
		` + "`" + `{"viaNotNot":{"a":1,"x":2}}` + "`" + `,
		` + "`" + `{"pooled":{"a":1,"b":2,"x":3}}` + "`" + `,
	} {
		var v StrictEvaluatedPositions
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			fail("decoding %s: %v", doc, err)
		}
		if err := v.Validate(); err != nil {
			fail("the default configuration refused an undeclared key, which the schema permits: %s: %v", doc, err)
		}
	}
	// The typed member is still judged, which is what says the sub-schemas are
	// being evaluated at all rather than skipped.
	var v StrictEvaluatedPositions
	if err := json.Unmarshal([]byte(` + "`" + `{"viaThen":{"a":"not an integer"}}` + "`" + `), &v); err == nil {
		if err := v.Validate(); err == nil {
			fail("the sub-schema's own type constraint was not enforced either")
		}
	}
	fmt.Println("PASS")
}
`
	runGeneratedMainProgram(t,
		"testdata/schemas/regression/strict_evaluated_positions.json",
		"strict_properties_default_test",
		mainGo,
	)
}
