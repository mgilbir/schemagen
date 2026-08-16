package schemagen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfigRun writes two documents where person.json $refs common.json, plus
// a config selecting both, and returns the config path and output dir.
func writeConfigRun(t *testing.T, extra string) (cfgPath, outDir string) {
	t.Helper()
	src := t.TempDir()
	outDir = t.TempDir()
	writeFile(t, filepath.Join(src, "common.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://example.com/common.json", "title": "CommonDoc", "type": "object",
		"definitions": {"address": {"type": "object", "properties": {"postal_code": {"type": "string"}, "city": {"type": "string"}}, "required": ["city"]}}
	}`)
	writeFile(t, filepath.Join(src, "person.json"), `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://example.com/person.json", "title": "PersonDoc", "type": "object",
		"properties": {"home": {"$ref": "https://example.com/common.json#/definitions/address"}},
		"required": ["home"]
	}`)
	cfgPath = filepath.Join(src, "schemagen.json")
	// person is listed first on purpose: generation order is derived from $refs.
	writeFile(t, cfgPath, `{
		"outputDir": `+jsonString(outDir)+`,
		"validation": "static"`+extra+`,
		"documents": [
			{"path": `+jsonString(filepath.Join(src, "person.json"))+`,
			 "id": "https://example.com/person.json",
			 "package": "example.com/m/person", "rootName": "Person"},
			{"path": `+jsonString(filepath.Join(src, "common.json"))+`,
			 "id": "https://example.com/common.json",
			 "package": "example.com/m/common", "rootName": "Common",
			 "fieldNames": {"Address": {"postal_code": "PostalCode"}}}
		]
	}`)
	return cfgPath, outDir
}

func jsonString(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}

// A whole run described by one config file, with no positional arguments.
func TestConfigDrivesAnEntireRun(t *testing.T) {
	cfgPath, outDir := writeConfigRun(t, "")

	cmd := NewRootCmd()
	var out, errOut strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"generate", "--config", cfgPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config-driven run: %v", err)
	}
	if strings.Contains(errOut.String(), "matched no input") {
		t.Errorf("config entries that were used must not be reported unused:\n%s", errOut.String())
	}

	person, err := os.ReadFile(filepath.Join(outDir, "person", "person.go"))
	if err != nil {
		t.Fatal(err)
	}
	common, err := os.ReadFile(filepath.Join(outDir, "common", "common.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(person), "type Person struct") {
		t.Errorf("rootName from config not applied:\n%s", person)
	}
	if !strings.Contains(string(common), "type Common struct") {
		t.Errorf("rootName from config not applied:\n%s", common)
	}
	if !strings.Contains(string(common), "PostalCode") {
		t.Errorf("per-document fieldNames from config not applied:\n%s", common)
	}
	if !strings.Contains(string(person), "example.com/m/common") {
		t.Errorf("cross-package import missing:\n%s", person)
	}
	if buildOut, err := buildGenerated(t, outDir, "example.com/m"); err != nil {
		t.Errorf("config-driven output does not compile: %v\n%s", err, buildOut)
	}
}

// An explicit flag overrides the config, per setting.
func TestFlagsOverrideConfig(t *testing.T) {
	cfgPath, _ := writeConfigRun(t, "")
	flagOut := t.TempDir()

	cmd := NewRootCmd()
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs([]string{"generate", "--config", cfgPath,
		"-o", flagOut,
		"--root-name", "id:https://example.com/person.json=OverriddenPerson",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	person, err := os.ReadFile(filepath.Join(flagOut, "person", "person.go"))
	if err != nil {
		t.Fatalf("-o should have overridden the config's outputDir: %v", err)
	}
	if !strings.Contains(string(person), "type OverriddenPerson struct") {
		t.Errorf("--root-name should override the config's rootName:\n%s", person)
	}
	// The document the flag did not target keeps its config name.
	common, err := os.ReadFile(filepath.Join(flagOut, "common", "common.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(common), "type Common struct") {
		t.Errorf("overriding one document must not discard the rest of the config:\n%s", common)
	}
}

// A config entry selecting a document that is not in the run is reported.
func TestConfigStaleEntryIsReported(t *testing.T) {
	cfgPath, _ := writeConfigRun(t, "")
	// Append a document that no input matches.
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(body), `"documents": [`,
		`"documents": [{"id": "https://example.com/gone.json", "rootName": "Gone"},`, 1)
	writeFile(t, cfgPath, patched)

	cmd := NewRootCmd()
	var errOut strings.Builder
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"generate", "--config", cfgPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("a stale entry should warn, not fail: %v", err)
	}
	if !strings.Contains(errOut.String(), "gone.json") {
		t.Errorf("a config entry matching no input should be reported:\n%s", errOut.String())
	}
}

func TestConfigValidation(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "c.json")
		writeFile(t, p, body)
		return p
	}
	cases := []struct {
		name, body, want string
	}{
		{"unknown field", `{"outputDirr": "x"}`, "unknown field"},
		{"no selector", `{"documents":[{"package":"example.com/m/a"}]}`, `needs an "id" or a "path"`},
		{"package without id", `{"documents":[{"path":"a.json","package":"example.com/m/a"}]}`, `"package" requires "id"`},
		{"output without id", `{"documents":[{"path":"a.json","output":"a.go"}]}`, `"output" requires "id"`},
		{"bad rootName", `{"documents":[{"id":"x","rootName":"lowercase"}]}`, "not an exported Go identifier"},
		{"duplicate id", `{"documents":[{"id":"x","rootName":"A"},{"id":"x","rootName":"B"}]}`, "already configured"},
		// An id with no path and no setting is read by nothing: the last way a
		// config entry could select a document and silently do nothing.
		{"selector with no settings", `{"documents":[{"id":"https://ex.test/a.json"}]}`, "sets nothing"},
		{"bad validation", `{"validation":"nonsense"}`, "validation"},
		{"bad draft", `{"draft":"99"}`, "draft"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfigFile(write(tc.body))
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should mention %q, got: %v", tc.want, err)
			}
		})
	}
}

// Without inputs and without a config there is nothing to generate.
func TestNoInputsAndNoConfigIsAnError(t *testing.T) {
	cmd := NewRootCmd()
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs([]string{"generate"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no input schemas") {
		t.Errorf("expected a no-inputs error, got: %v", err)
	}
}

// A config with no document paths cannot supply inputs.
func TestConfigWithoutPathsCannotSupplyInputs(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "c.json")
	writeFile(t, cfgPath, `{"documents":[{"id":"https://example.com/a.json","rootName":"A"}]}`)

	cmd := NewRootCmd()
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs([]string{"generate", "--config", cfgPath})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no input schemas") {
		t.Errorf("expected a no-inputs error when no entry has a path, got: %v", err)
	}
}
