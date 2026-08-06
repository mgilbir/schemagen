package generator

import (
	"encoding/json"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// TestNullRulesClaimFmtOnlyWhereTheyNameThePosition holds
// StructDef.NullChecksNameAPosition, and the import it drives, against the
// three shapes a null rule is emitted in. This is issue #202.
//
// The import list is a hand-written model of what the templates emit, and Go
// gives no quarter on it: a file importing a package it never names does not
// compile at all. UnmarshalJSON writes the offending key itself for a flat rule
// and for the overflow map's rule, but a rule that reaches *below* a property
// is a call to checkJSONNulls, whose error text is built in the helper file --
// so a struct carrying only nested rules needs no fmt on this account, and
// claiming it anyway emitted `"fmt" imported and not used` for a schema as
// ordinary as an array property under a root with no declared type.
//
// Two columns, because they can fail apart. `names` is the predicate itself,
// which is the statement about the templates; `fmt` is whether the file ends up
// claiming the import, which several other arms of addRequiredImports also have
// a say in. The overflow row is the case in point: no shape reaching it leaves
// the overflow values as json.RawMessage, so the additionalProperties arm
// claims fmt there whatever this predicate says, and only the `names` column
// can tell whether the predicate is still right about that arm.
//
// The emitter drops an import the rendered file never names, so the first rows
// compile either way now; that pass is the guard, and this is the model it
// exists to keep honest.
func TestNullRulesClaimFmtOnlyWhereTheyNameThePosition(t *testing.T) {
	for _, tc := range []struct {
		name      string
		input     string
		wantNames bool
		wantFmt   bool
		why       string
	}{
		{
			name:      "nested rule only",
			input:     `{"properties":{"p":{"type":"array","items":{"type":"integer"}}}}`,
			wantNames: false,
			wantFmt:   false,
			why:       "the only null rule is a call to checkJSONNulls, and nothing else in the file names fmt",
		},
		{
			name:      "nested rule under a map",
			input:     `{"properties":{"p":{"type":"object","additionalProperties":{"type":"string"}}}}`,
			wantNames: false,
			wantFmt:   false,
			why:       "a map's element rule is the same walker call as a slice's",
		},
		{
			name:      "flat rule",
			input:     `{"properties":{"p":{"type":"string"}}}`,
			wantNames: true,
			wantFmt:   true,
			why:       "UnmarshalJSON writes `p: null is not allowed` itself",
		},
		{
			name:      "overflow rule",
			input:     `{"additionalProperties":{"type":"string"}}`,
			wantNames: true,
			wantFmt:   true,
			why:       "the overflow arm names the offending key, by fmt.Errorf or fmt.Sprintf",
		},
		{
			name:      "declared object refuses its own null",
			input:     `{"type":"object","properties":{"p":{"type":"array","items":{"type":"integer"}}}}`,
			wantNames: false,
			wantFmt:   true,
			why:       "a root that declares a type refuses a null of its own, which is a fmt.Errorf on a different arm",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s schema.Schema
			if err := json.Unmarshal([]byte(tc.input), &s); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			s.Normalize()

			ir, err := New(Config{PackageName: "testpkg", OmitEmpty: true}).Generate(&s)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}

			var root *StructDef
			for _, td := range ir.TypeDefs {
				if sd, ok := td.(*StructDef); ok {
					root = sd
					break
				}
			}
			if root == nil {
				t.Fatalf("no struct in the generated IR for %s", tc.input)
			}
			if got := root.NullChecksNameAPosition(); got != tc.wantNames {
				t.Errorf("NullChecksNameAPosition() = %v, want %v: %s", got, tc.wantNames, tc.why)
			}

			claimed := false
			for _, imp := range ir.Imports {
				if imp.Path == "fmt" {
					claimed = true
				}
			}
			if claimed != tc.wantFmt {
				t.Errorf("fmt claimed = %v, want %v: %s\nimports: %+v", claimed, tc.wantFmt, tc.why, ir.Imports)
			}
		})
	}
}
