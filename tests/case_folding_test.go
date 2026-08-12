package tests

import (
	"fmt"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// The guards for issue #245: a JSON key that differs from a declared property
// only in case must not fill that property.
//
// JSON Schema property names are case-sensitive. "NAME" and "name" are two
// different properties, and a schema declaring only "name" says nothing at all
// about "NAME" -- it is an additional property, and "name" is absent.
// encoding/json reads keys the other way round: a key matching no field exactly
// is matched a second time case-insensitively. The generated decode therefore
// filled the member declared for "name" from a "NAME" while the overflow
// routing filed that same key as an additional property, so both halves landed
// at once:
//
//	in: {"name":"lower","NAME":"upper"}  out: {"NAME":"upper","name":"upper"}
//	in: {"NAME":"upper"}                 out: {"NAME":"upper","name":"upper"}
//
// Nothing reported either. The document validated going in, the value validated
// coming out, and the property in between held a value the caller never sent.
//
// The cases below are written as re-encodings rather than as field reads
// wherever a field read would not distinguish them, because the two halves of
// the defect are only visible together: a value in the wrong member and a key
// invented out of nothing.

// caseFoldingCase is one document put through the generated type: what it
// decodes to, what the value then re-encodes as, and what Validate says.
type caseFoldingCase struct {
	// name titles the case in the failure message.
	name string
	// in is the document handed to json.Unmarshal.
	in string
	// want is the re-encoding of the decoded value. MarshalJSON writes its keys
	// in sorted order, so an upper-case spelling sorts before a lower-case one.
	want string
	// probe is a Go expression over `obj`, the decoded value, and probeWant is
	// what it must produce. Empty probe skips the read. The generated program
	// declares str() for dereferencing an optional string member.
	probe     string
	probeWant string
	// validateErr is the substring Validate's error must contain. Empty means
	// Validate must accept.
	validateErr string
}

// caseFoldingProgram renders the main() that runs the cases against rootType.
func caseFoldingProgram(rootType string, cases []caseFoldingCase) string {
	body := ""
	for _, c := range cases {
		probe := "nil"
		if c.probe != "" {
			probe = fmt.Sprintf("func(obj *%s) string { return %s }", rootType, c.probe)
		}
		body += fmt.Sprintf("\t\t{name: %q, in: %q, want: %q, probe: %s, probeWant: %q, validateErr: %q},\n",
			c.name, c.in, c.want, probe, c.probeWant, c.validateErr)
	}
	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func str(p *string) string {
	if p == nil {
		return "<absent>"
	}
	return *p
}

func main() {
	cases := []struct {
		name        string
		in          string
		want        string
		probe       func(obj *%[1]s) string
		probeWant   string
		validateErr string
	}{
%[2]s	}
	failed := false
	for _, c := range cases {
		var obj %[1]s
		if err := json.Unmarshal([]byte(c.in), &obj); err != nil {
			fmt.Fprintf(os.Stderr, "%%s: decoding %%s: %%v\n", c.name, c.in, err)
			failed = true
			continue
		}
		if c.probe != nil {
			if got := c.probe(&obj); got != c.probeWant {
				fmt.Fprintf(os.Stderr, "%%s: %%s decoded to a member holding %%q, want %%q\n",
					c.name, c.in, got, c.probeWant)
				failed = true
			}
		}
		out, err := json.Marshal(obj)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%%s: re-encoding %%s: %%v\n", c.name, c.in, err)
			failed = true
			continue
		}
		if string(out) != c.want {
			fmt.Fprintf(os.Stderr, "%%s: %%s came back as %%s, want %%s\n", c.name, c.in, out, c.want)
			failed = true
		}
		err = obj.Validate()
		switch {
		case c.validateErr == "" && err != nil:
			fmt.Fprintf(os.Stderr, "%%s: Validate refused %%s: %%v\n", c.name, c.in, err)
			failed = true
		case c.validateErr != "" && err == nil:
			fmt.Fprintf(os.Stderr, "%%s: Validate accepted %%s, want an error naming %%q\n",
				c.name, c.in, c.validateErr)
			failed = true
		case c.validateErr != "" && !strings.Contains(err.Error(), c.validateErr):
			fmt.Fprintf(os.Stderr, "%%s: Validate refused %%s with %%q, want an error naming %%q\n",
				c.name, c.in, err, c.validateErr)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`, rootType, body)
}

// TestCaseDifferingKeyDoesNotFillADeclaredProperty is issue #245 itself, on the
// default configuration and an entirely ordinary schema.
//
// The exact-case controls are the ones that matter most: an ordinary document
// has to keep decoding into the member it always did, and a key related to no
// property at all has to keep reaching the overflow map untouched.
func TestCaseDifferingKeyDoesNotFillADeclaredProperty(t *testing.T) {
	cases := []caseFoldingCase{
		{
			name:      "exact key alone",
			in:        `{"name":"lower"}`,
			want:      `{"name":"lower"}`,
			probe:     "str(obj.Name)",
			probeWant: "lower",
		},
		{
			name:      "case-differing key alone invents nothing",
			in:        `{"NAME":"upper"}`,
			want:      `{"NAME":"upper"}`,
			probe:     "str(obj.Name)",
			probeWant: "<absent>",
		},
		{
			name:      "both keys, exact one first",
			in:        `{"name":"lower","NAME":"upper"}`,
			want:      `{"NAME":"upper","name":"lower"}`,
			probe:     "str(obj.Name)",
			probeWant: "lower",
		},
		{
			// Order decided the outcome before: encoding/json takes keys as they
			// come, so the same two keys the other way round already produced the
			// right value by accident.
			name:      "both keys, case-differing one first",
			in:        `{"NAME":"upper","name":"lower"}`,
			want:      `{"NAME":"upper","name":"lower"}`,
			probe:     "str(obj.Name)",
			probeWant: "lower",
		},
		{
			name:      "three spellings",
			in:        `{"name":"a","Name":"b","nAmE":"c"}`,
			want:      `{"Name":"b","nAmE":"c","name":"a"}`,
			probe:     "str(obj.Name)",
			probeWant: "a",
		},
		{
			// Not an ASCII rule. U+212A KELVIN SIGN folds onto "k", and reached
			// a property named "k" through exactly the same path.
			name:      "unicode folding: kelvin sign",
			in:        "{\"k\":\"ascii\",\"K\":\"kelvin\"}",
			want:      "{\"k\":\"ascii\",\"K\":\"kelvin\"}",
			probe:     "str(obj.K)",
			probeWant: "ascii",
		},
		{
			name:      "unicode folding: kelvin sign alone",
			in:        "{\"K\":\"kelvin\"}",
			want:      "{\"K\":\"kelvin\"}",
			probe:     "str(obj.K)",
			probeWant: "<absent>",
		},
		{
			name:      "a key related to no property is still overflow",
			in:        `{"other":"x"}`,
			want:      `{"other":"x"}`,
			probe:     "str(obj.Name)",
			probeWant: "<absent>",
		},
		{
			name:      "exact keys beside an unrelated one",
			in:        `{"name":"lower","k":"kay","other":"x"}`,
			want:      `{"k":"kay","name":"lower","other":"x"}`,
			probe:     "str(obj.Name)",
			probeWant: "lower",
		},
	}
	runGeneratedMainProgram(t,
		"testdata/schemas/regression/case_sensitive_properties.json",
		"case_folding_test",
		caseFoldingProgram("CaseSensitiveProperties", cases))
}

// TestCaseDifferingKeyUnderATypedOverflowMap is the same defect where
// additionalProperties has a type of its own, so the case-differing key is
// decoded into the map rather than kept as raw bytes.
func TestCaseDifferingKeyUnderATypedOverflowMap(t *testing.T) {
	cases := []caseFoldingCase{
		{
			name:      "exact key alone",
			in:        `{"name":"lower"}`,
			want:      `{"name":"lower"}`,
			probe:     "str(obj.Name)",
			probeWant: "lower",
		},
		{
			name:      "case-differing key is a map entry",
			in:        `{"NAME":"upper"}`,
			want:      `{"NAME":"upper"}`,
			probe:     "str(obj.Name)",
			probeWant: "<absent>",
		},
		{
			name:      "both keys",
			in:        `{"name":"lower","NAME":"upper"}`,
			want:      `{"NAME":"upper","name":"lower"}`,
			probe:     `str(obj.Name) + "/" + obj.AdditionalProperties["NAME"]`,
			probeWant: "lower/upper",
		},
	}
	runGeneratedMainProgram(t,
		"testdata/schemas/regression/case_sensitive_properties_typed.json",
		"case_folding_typed_test",
		caseFoldingProgram("CaseSensitiveTypedOverflow", cases))
}

// TestCaseDifferingKeyIsForbiddenWhereExtraKeysAre covers the two spellings of a
// closed object. Both already refused the key -- the routing below the decode
// was reading the document's own keys exactly all along -- so what these hold is
// the other half: that the declared property is left absent rather than filled
// from the key being refused.
func TestCaseDifferingKeyIsForbiddenWhereExtraKeysAre(t *testing.T) {
	for _, tc := range []struct {
		schema   string
		root     string
		module   string
		refusal  string
		wantName string
	}{
		{
			schema:  "testdata/schemas/regression/case_sensitive_properties_closed.json",
			root:    "CaseSensitiveClosed",
			module:  "case_folding_closed_test",
			refusal: `additional property "NAME" is not allowed`,
		},
		{
			schema:  "testdata/schemas/regression/case_sensitive_properties_unevaluated.json",
			root:    "CaseSensitiveUnevaluated",
			module:  "case_folding_unevaluated_test",
			refusal: `unevaluated property "NAME" is not allowed`,
		},
	} {
		t.Run(tc.root, func(t *testing.T) {
			cases := []caseFoldingCase{
				{
					name:      "exact key alone",
					in:        `{"name":"lower"}`,
					want:      `{"name":"lower"}`,
					probe:     "str(obj.Name)",
					probeWant: "lower",
				},
				{
					name:        "case-differing key alone",
					in:          `{"NAME":"upper"}`,
					want:        `{"NAME":"upper"}`,
					probe:       "str(obj.Name)",
					probeWant:   "<absent>",
					validateErr: tc.refusal,
				},
				{
					name:        "both keys",
					in:          `{"name":"lower","NAME":"upper"}`,
					want:        `{"NAME":"upper","name":"lower"}`,
					probe:       "str(obj.Name)",
					probeWant:   "lower",
					validateErr: tc.refusal,
				},
			}
			runGeneratedMainProgram(t, tc.schema, tc.module, caseFoldingProgram(tc.root, cases))
		})
	}
}

// TestCaseDifferingKeyIsRefusedUnderStrictProperties: --strict-properties reads
// an absent additionalProperties as false, and a key differing only in case is
// an extra key like any other, so it is refused like any other.
func TestCaseDifferingKeyIsRefusedUnderStrictProperties(t *testing.T) {
	cases := []caseFoldingCase{
		{
			name:      "exact key alone is accepted",
			in:        `{"name":"lower"}`,
			want:      `{"name":"lower"}`,
			probe:     "str(obj.Name)",
			probeWant: "lower",
		},
		{
			name:        "case-differing key alone is refused",
			in:          `{"NAME":"upper"}`,
			want:        `{"NAME":"upper"}`,
			probe:       "str(obj.Name)",
			probeWant:   "<absent>",
			validateErr: `additional property "NAME" is not allowed`,
		},
		{
			name:        "both keys: the exact one keeps its value and the other is refused",
			in:          `{"name":"lower","NAME":"upper"}`,
			want:        `{"NAME":"upper","name":"lower"}`,
			probe:       "str(obj.Name)",
			probeWant:   "lower",
			validateErr: `additional property "NAME" is not allowed`,
		},
	}
	runGeneratedMainProgramWithConfig(t,
		"testdata/schemas/regression/case_sensitive_properties.json",
		"case_folding_strict_test",
		caseFoldingProgram("CaseSensitiveProperties", cases),
		generator.Config{PackageName: "testpkg", OmitEmpty: true, StrictProperties: true})
}

// TestDeclaredCaseVariantPropertiesLandIndependently is the sharpest test that
// the dispatch is exact rather than merely stricter: a schema declaring both
// "name" and "NAME" has two properties, and each has to hold the value written
// under its own spelling and reach neither the other member nor the overflow
// map.
func TestDeclaredCaseVariantPropertiesLandIndependently(t *testing.T) {
	cases := []caseFoldingCase{
		{
			name:      "lower-case spelling alone",
			in:        `{"name":"lower"}`,
			want:      `{"name":"lower"}`,
			probe:     "fmt.Sprint(len(obj.AdditionalProperties))",
			probeWant: "0",
		},
		{
			name:      "upper-case spelling alone",
			in:        `{"NAME":"upper"}`,
			want:      `{"NAME":"upper"}`,
			probe:     "fmt.Sprint(len(obj.AdditionalProperties))",
			probeWant: "0",
		},
		{
			name:      "both spellings keep their own values",
			in:        `{"name":"lower","NAME":"upper"}`,
			want:      `{"NAME":"upper","name":"lower"}`,
			probe:     "fmt.Sprint(len(obj.AdditionalProperties))",
			probeWant: "0",
		},
		{
			// The case that reaches the reduced decode with two declared
			// spellings in hand: a third spelling belongs to neither of them, so
			// the document is cut down, and both declared members have to survive
			// the cut. Dropping a folding pair wholesale rather than only the key
			// that is not a property loses them both here and nowhere else.
			name:      "a third spelling beside both declared ones",
			in:        `{"name":"lower","NAME":"upper","nAmE":"mixed"}`,
			want:      `{"NAME":"upper","nAmE":"mixed","name":"lower"}`,
			probe:     "fmt.Sprint(len(obj.AdditionalProperties))",
			probeWant: "1",
		},
	}
	runGeneratedMainProgram(t,
		"testdata/schemas/regression/case_variant_properties.json",
		"case_variant_test",
		caseFoldingProgram("CaseVariantProperties", cases))
}
