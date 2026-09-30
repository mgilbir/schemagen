package validation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/gentest"
	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
	"github.com/mgilbir/schemagen/tests/internal/testsupport"
)

// typedFormatSamples are, for each format a field is decoded into a Go type
// for, strings the format excludes and strings it admits (from the JSON Schema
// Test Suite's optional/format files and RFC 3339, 2673 and 4291).
var typedFormatSamples = map[string]struct {
	goType             string
	excluded, admitted []string
}{
	"date-time": {"time.Time",
		[]string{"", "2020-01-01", "1990-02-31T15:59:59.123-08:00", "1990-12-31T15:59:59-24:00",
			"1963-06-19T08:30:06.28123+01:00Z", "1998-12-31T23:59:61Z", "2013-350T01:01:01", "06/19/1963 08:30:06 PST"},
		[]string{"1963-06-19T08:30:06.283185Z", "1963-06-19T08:30:06Z", "1963-06-19t08:30:06.283185z", "1990-12-31T15:59:50.123-08:00"}},
	"ipv4": {"netip.Addr",
		[]string{"", "127.0.0.0.1", "256.256.256.256", "087.10.0.1", "1.2.3", "::1"},
		[]string{"192.168.0.1", "0.0.0.0"}},
	"ipv6": {"netip.Addr",
		[]string{"", "fe80::a%eth1", "12345::", ":::", "1.2.3.4", "::laptop"},
		[]string{"::1", "fe80::1", "::ffff:192.168.0.1"}},
}

// TestTypedFormatDecodes holds the keyword ledger's table of what a typed
// format decode enforces (typedFormatDecodeEnforces, pkg/generator) to the
// code the generator writes. For each format mapped to a Go type it generates
// a field of that format under draft 7, where format asserts, decodes and
// validates strings the format excludes, and requires: an entry saying the
// decode enforces the format lets none of them through, and one saying it
// does not lets at least one through. A decode made stricter, or looser,
// fails here until the table says so -- the ledger credits `format` to a
// field's Go type only where the table says the type carries it.
func TestTypedFormatDecodes(t *testing.T) {
	table := gentest.LedgerTypedFormatDecodes()
	var formats []string
	for f := range table {
		formats = append(formats, f)
	}
	sort.Strings(formats)
	for _, f := range formats {
		if _, ok := typedFormatSamples[f]; !ok {
			t.Errorf("the ledger's table names format %q, which this test has no samples for", f)
		}
	}
	for f := range typedFormatSamples {
		if _, ok := table[f]; !ok {
			t.Errorf("this test has samples for format %q, which the ledger's table does not name", f)
		}
	}

	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := testgo.MkdirWorkTemp("schemagen-val-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	var all strings.Builder
	var driver strings.Builder
	driver.WriteString("package main\n\nimport (\n\t\"encoding/json\"\n\t\"fmt\"\n)\n\nfunc main() {\n")
	for i, f := range formats {
		smp := typedFormatSamples[f]
		doc := fmt.Sprintf(`{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"v":{"type":"string","format":%q}},"required":["v"]}`, f)
		var s schema.Schema
		if err := json.Unmarshal([]byte(doc), &s); err != nil {
			t.Fatal(err)
		}
		s.Normalize()
		root := fmt.Sprintf("F%d", i)
		ir, err := generator.New(generator.Config{PackageName: "main", OmitEmpty: true}).Generate(&s, generator.WithRootTypeName(root))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		src, err := em.Emit(ir)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if !strings.Contains(string(src), smp.goType) {
			t.Fatalf("format %s is not decoded into %s under draft 7 any more; the probe would be testing a string check:\n%s", f, smp.goType, src)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.go", i)), src, 0o644); err != nil {
			t.Fatal(err)
		}
		all.Write(src)
		for _, list := range []struct {
			tag     string
			samples []string
		}{{"excluded", smp.excluded}, {"admitted", smp.admitted}} {
			for _, sample := range list.samples {
				inst, _ := json.Marshal(map[string]string{"v": sample})
				fmt.Fprintf(&driver, "\t{\n\t\tvar v %s\n\t\tverdict := \"accept\"\n\t\tif err := json.Unmarshal([]byte(%q), &v); err != nil {\n\t\t\tverdict = \"reject\"\n\t\t} else if err := v.Validate(); err != nil {\n\t\t\tverdict = \"reject\"\n\t\t}\n\t\tfmt.Printf(\"%%s\\t%%s\\t%%q\\t%%s\\n\", %q, %q, %q, verdict)\n\t}\n",
					root, inst, f, list.tag, sample)
			}
		}
	}
	driver.WriteString("}\n")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(driver.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := testsupport.WriteSharedHelpersErr(dir, all.String()); err != nil {
		t.Fatal(err)
	}
	if err := testsupport.WriteTestGoMod(dir, "typedformats"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	out, err := testgo.Command(ctx, dir, "run", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("running the probe: %v\n%s", err, out)
	}
	leaks := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) != 4 {
			t.Fatalf("unexpected probe output line %q", line)
		}
		f, tag, sample, verdict := parts[0], parts[1], parts[2], parts[3]
		switch {
		case tag == "excluded" && verdict == "accept":
			leaks[f] = append(leaks[f], sample)
		case tag == "admitted" && verdict == "reject":
			t.Logf("format %s: the typed decode refuses %s, which the format admits (a false rejection, not the ledger's to report)", f, sample)
		}
	}
	for _, f := range formats {
		switch {
		case table[f] && len(leaks[f]) > 0:
			t.Errorf("the ledger credits the %s decode with format %q, and it accepts %s, which the format excludes",
				typedFormatSamples[f].goType, f, strings.Join(leaks[f], ", "))
		case !table[f] && len(leaks[f]) == 0:
			t.Errorf("the ledger does not credit the %s decode with format %q, and it refused every excluded sample; if it enforces the format now, say so in typedFormatDecodeEnforces",
				typedFormatSamples[f].goType, f)
		default:
			t.Logf("format %s: enforced by the decode %v; excluded samples it accepts: %v", f, table[f], leaks[f])
		}
	}
}
