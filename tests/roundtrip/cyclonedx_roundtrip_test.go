package roundtrip

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/tests/internal/testsupport"
)

// TestCycloneDXExampleBOMsKeepTheirShape round-trips every valid 1.6 BOM the
// CycloneDX specification ships (testdata/cyclonedx-1.6; see NOTICE there)
// through the types generated from the 1.6 schema.
//
// CycloneDX declares definitions.signature as a $ref to jsf's signature
// definition, which is itself a oneOf. Reached through that chain, an optional
// "signature" came out as a value field that omitempty never omits, so every
// component, service, annotation and the BOM itself was written back with a
// "signature": null it did not have -- which the same types then refused to read
// back. Every BOM here has at least one such position.
//
// What is asserted is the shape: the marshalled document has exactly the members
// the input had, at every depth, and decodes again. The shape comparison is what
// sees an invented member, a dropped one, or an array that changed length. The
// values are not compared: a `format: date-time` value is held as a time.Time
// and written back in its canonical spelling (+00:00 as Z, .000Z as Z), which is
// another defect, and holding it here would hide this one behind it.
//
// Every BOM also has to validate, before and after the round trip. Four did
// not: an object-level oneOf counted a branch closed by "additionalProperties":
// false as matching an object that carries keys it forbids (see
// ObjectOneOfBranch.ClosedKeySets). jsf's signature is a oneOf of {signers},
// {chain} -- both closed -- and a signer, so every simple signature matched all
// three; the model card's datasets are a oneOf of inline data and a closed
// {ref}, so every inline dataset matched both.
//
// That these BOMs are valid is not this test's say-so. Bowtie, run over the 1.6
// schema with jsf and spdx bundled into it, has python-jsonschema 4.26 and ajv
// 8.20 agree that valid-machine-learning is valid. The other three --
// valid-attestation, valid-signatures and valid-standard -- both call invalid,
// and python-jsonschema says why: jsf's signer "algorithm" is a oneOf of an enum
// and a {"format":"uri"} string, and with format an annotation "ES256" satisfies
// both. This generator asserts format on draft 7 (README, "Format: assertion or
// annotation"), and under that reading -- python-jsonschema with its format
// checker, "uri" included -- all four are valid, as the specification ships
// them.
func TestCycloneDXExampleBOMsKeepTheirShape(t *testing.T) {
	if testing.Short() {
		t.Skip("generates and compiles the CycloneDX 1.6 types")
	}
	t.Parallel()
	dir, err := filepath.Abs(testsupport.RepoPath("testdata", "cyclonedx-1.6"))
	if err != nil {
		t.Fatal(err)
	}
	boms, err := filepath.Glob(filepath.Join(dir, "boms", "valid-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(boms) < 40 {
		t.Fatalf("found %d BOMs under %s; the corpus is 45, so the round trip would be measuring almost nothing", len(boms), dir)
	}

	bin := schemagenBinary(t)
	out := t.TempDir()
	runSchemagen(t, bin, "generate", filepath.Join(dir, "schema", "bom-1.6.schema.json"),
		"-o", filepath.Join(out, "cdx"), "-p", "cdx", "--root-name", "bom-1.6.schema.json=Bom")
	if err := writeTestGoMod(out, "ex.test/cdx"); err != nil {
		t.Fatal(err)
	}
	drv := filepath.Join(out, "driver")
	if err := os.MkdirAll(drv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(drv, "main.go"), []byte(cycloneDXDriver), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := testgo.Command(ctx, out, append([]string{"run", "-mod=mod", "./driver"}, boms...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("driver: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != fmt.Sprintf("PASS %d", len(boms)) {
		t.Errorf("CycloneDX 1.6 example BOMs that did not keep their shape:\n%s", got)
	}
}

// cycloneDXDriver decodes each BOM, marshals it, compares the two documents'
// members at every depth, and decodes the output again.
const cycloneDXDriver = `package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"ex.test/cdx/cdx"
)

func decodeAny(b []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	err := d.Decode(&v)
	return v, err
}

// shape reports every place the two documents differ in which members or
// elements they have, or in the JSON kind of a value.
func shape(in, out any, path string, report func(string, ...any)) {
	switch iv := in.(type) {
	case map[string]any:
		ov, ok := out.(map[string]any)
		if !ok {
			report("%s: an object was written back as %T", path, out)
			return
		}
		for k, v := range iv {
			o, ok := ov[k]
			if !ok {
				report("%s.%s: dropped", path, k)
				continue
			}
			shape(v, o, path+"."+k, report)
		}
		for k, v := range ov {
			if _, ok := iv[k]; !ok {
				report("%s.%s: added, as %v", path, k, v)
			}
		}
	case []any:
		ov, ok := out.([]any)
		if !ok {
			report("%s: an array was written back as %T", path, out)
			return
		}
		if len(iv) != len(ov) {
			report("%s: %d elements written back as %d", path, len(iv), len(ov))
			return
		}
		for i := range iv {
			shape(iv[i], ov[i], fmt.Sprintf("%s[%d]", path, i), report)
		}
	default:
		if fmt.Sprintf("%T", in) != fmt.Sprintf("%T", out) {
			report("%s: %v written back as %v", path, in, out)
		}
	}
}

func main() {
	failed := 0
	for _, path := range os.Args[1:] {
		name := filepath.Base(path)
		report := func(format string, args ...any) {
			failed++
			fmt.Printf(name+": "+format+"\n", args...)
		}
		in, err := os.ReadFile(path)
		if err != nil {
			panic(err)
		}
		var bom cdx.Bom
		if err := json.Unmarshal(in, &bom); err != nil {
			report("does not decode: %v", err)
			continue
		}
		if err := bom.Validate(); err != nil {
			report("does not validate: %v", err)
		}
		out, err := json.Marshal(bom)
		if err != nil {
			report("does not marshal: %v", err)
			continue
		}
		inDoc, err := decodeAny(in)
		if err != nil {
			panic(err)
		}
		outDoc, err := decodeAny(out)
		if err != nil {
			report("marshalled to something that is not JSON: %v", err)
			continue
		}
		shape(inDoc, outDoc, "", report)
		var again cdx.Bom
		if err := json.Unmarshal(out, &again); err != nil {
			report("the output does not decode with the same type: %v", err)
		} else if err := again.Validate(); err != nil {
			report("the output does not validate: %v", err)
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
	fmt.Printf("PASS %d\n", len(os.Args)-1)
}
`
