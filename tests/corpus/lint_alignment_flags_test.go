package corpus

import (
	"os"
	"regexp"
	"sort"
	"testing"

	schemagen "github.com/mgilbir/schemagen/cmd/schemagen"
	"github.com/mgilbir/schemagen/tests/internal/testsupport"
)

// notShapeFlags are the `schemagen generate` flags scripts/lint-alignment.sh
// does not run a configuration for, each with the reason. Everything else the
// command accepts must appear in the script's CONFIGS.
var notShapeFlags = map[string]string{
	"output-dir":              "where files go, not what they declare",
	"package":                 "the package clause, not a type",
	"verbose":                 "progress output",
	"config":                  "a file of the other flags' settings; each setting is covered by its own flag",
	"allow-remote-refs":       "fetches documents over the network; the corpus is self-contained, and a fetched document is generated like any other",
	"draft":                   "which dialect the document is read under; the corpus declares its own $schema across every draft",
	"field-map":               "renames fields, which does not move a field or change its type",
	"root-name":               "renames the root type",
	"root-name-from-filename": "renames the root type",
	"shared-types":            "one package from several schemas; the script generates one package per schema, and --shared-types requires static validation and distinct root names the corpus does not have",
	"schema-package":          "multi-package generation from several documents, the same reason as shared-types; cmd/schemagen/multipkg_compile_test.go compiles those trees",
	"schema-output":           "where a document's file goes; requires schema-package",
}

// TestLintAlignmentCoversEveryShapeFlag holds scripts/lint-alignment.sh to the
// CLI it measures.
//
// The script runs the fieldalignment analyzer over the corpus once per
// generator configuration that changes the shape of a type, and it is the only
// thing in CI that compiles the corpus under any configuration but the
// default. Its list of configurations is hand-written, and --raw-untyped --
// which turns every untyped position into json.RawMessage -- shipped without
// being added to it. This reads the flags off the command itself, so the next
// flag is a failure here until it is either in the script or in
// notShapeFlags with a reason.
func TestLintAlignmentCoversEveryShapeFlag(t *testing.T) {
	script, err := os.ReadFile(testsupport.RepoPath("scripts", "lint-alignment.sh"))
	if err != nil {
		t.Fatal(err)
	}
	inScript := map[string]bool{}
	for _, m := range regexp.MustCompile(`\|--([a-z-]+)`).FindAllSubmatch(script, -1) {
		inScript[string(m[1])] = true
	}

	// The flags are read from the command's own usage table, one line per
	// flag, rather than through pflag's visitor: that would put pflag in this
	// module's direct requirements for the sake of one test.
	usage := ""
	for _, c := range schemagen.NewRootCmd().Commands() {
		if c.Name() == "generate" {
			usage = c.Flags().FlagUsages()
		}
	}
	if usage == "" {
		t.Fatal("no generate subcommand on the root command, or it has no flags")
	}
	var flags []string
	for _, m := range regexp.MustCompile(`(?m)^\s+(?:-\w, )?--([a-z][a-z-]*)`).FindAllStringSubmatch(usage, -1) {
		if m[1] != "help" {
			flags = append(flags, m[1])
		}
	}
	sort.Strings(flags)
	if len(flags) < 10 {
		t.Fatalf("read only %d flags off the generate command; the check is not looking at it", len(flags))
	}

	known := map[string]bool{}
	for _, name := range flags {
		known[name] = true
		_, exempt := notShapeFlags[name]
		switch {
		case inScript[name] && exempt:
			t.Errorf("--%s is both run by scripts/lint-alignment.sh and exempted in notShapeFlags; drop one", name)
		case !inScript[name] && !exempt:
			t.Errorf("--%s is a generate flag that scripts/lint-alignment.sh runs no configuration for. If it can change "+
				"a Go type, add it to CONFIGS there (and to the combined entry); if it cannot, add it to "+
				"notShapeFlags with the reason", name)
		}
	}
	for name := range notShapeFlags {
		if !known[name] {
			t.Errorf("notShapeFlags exempts --%s, which the generate command no longer has", name)
		}
	}
	for name := range inScript {
		if !known[name] {
			t.Errorf("scripts/lint-alignment.sh runs --%s, which the generate command does not have", name)
		}
	}
}
