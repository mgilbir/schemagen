package schemagen

import (
	"path/filepath"
	"testing"
)

// The tests in this file are issue #314: --draft reached the documents the
// caller listed and not the ones a $ref pulled in beside them, so one command
// line read one schema set under two dialects.
//
// They assert the verdict the generated code gives a document, not which draft a
// struct field holds. The whole defect is that a keyword the stated dialect does
// not define went on binding, and only running the output says whether it did:
// under --draft 3 the `const` of a document reached by $ref survived into an
// enum and a Validate that refused a value draft 3 has no way to refuse.

// constUnderDraft3 is the schema set of the reproduction, in the three spellings
// that must agree: one document with $defs, two documents both listed, and two
// documents of which only the referring one is.
const (
	constRefRoot = `{
		"$id": "https://ex.test/root.json",
		"title": "Root",
		"type": "object",
		"properties": {"t": {"$ref": "t.json"}}
	}`
	constRefTarget = `{
		"$id": "https://ex.test/t.json",
		"title": "TDoc",
		"type": "object",
		"properties": {"k": {"const": "x"}}
	}`
	constInDefs = `{
		"$id": "https://ex.test/one.json",
		"title": "Root",
		"type": "object",
		"properties": {"t": {"$ref": "#/$defs/T"}},
		"$defs": {"T": {"type": "object", "properties": {"k": {"const": "x"}}}}
	}`
)

// Draft 3 has no `const`, so under --draft 3 the keyword states a word the
// dialect has never heard of and constrains nothing. That was true of the
// listing that named both documents and of the single document with $defs, and
// false of the one where t.json arrives through the file resolver.
func TestDraftOverrideReachesADocumentReachedByRef(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "root.json"), constRefRoot)
	writeFile(t, filepath.Join(src, "t.json"), constRefTarget)

	generateCompileRun(t,
		func(modRoot string) []string {
			return []string{
				filepath.Join(src, "root.json"),
				"-o", filepath.Join(modRoot, "gen"), "-p", "gen",
				"--draft", "3", "--root-name", "Root",
			}
		},
		"example.com/m/gen", "Root",
		[]docInstance{
			// The value the const would have refused. Draft 3 does not define
			// const, so it is a document this schema accepts.
			{`{"t":{"k":"y"}}`, true, ""},
			{`{"t":{"k":"x"}}`, true, ""},
		})
}

// The control: the same reference with no --draft at all. The documents state no
// $schema, nothing supplies a dialect, and the gate keeps every keyword -- so the
// const binds and the run above must not be passing because the override
// switched the gate off for everyone.
func TestWithoutADraftOverrideAReachedDocumentKeepsItsConst(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "root.json"), constRefRoot)
	writeFile(t, filepath.Join(src, "t.json"), constRefTarget)

	generateCompileRun(t,
		func(modRoot string) []string {
			return []string{
				filepath.Join(src, "root.json"),
				"-o", filepath.Join(modRoot, "gen"), "-p", "gen",
				"--root-name", "Root",
			}
		},
		"example.com/m/gen", "Root",
		[]docInstance{
			{`{"t":{"k":"y"}}`, false, ""},
			{`{"t":{"k":"x"}}`, true, ""},
		})
}

// The two spellings the reached-by-$ref one has to agree with, run here so a
// change that made all three wrong together could not pass.
func TestDraftOverrideDropsConstInTheSpellingsThatAlwaysWorked(t *testing.T) {
	t.Run("both documents listed", func(t *testing.T) {
		src := t.TempDir()
		writeFile(t, filepath.Join(src, "root.json"), constRefRoot)
		writeFile(t, filepath.Join(src, "t.json"), constRefTarget)
		generateCompileRun(t,
			func(modRoot string) []string {
				// t.json first: --shared-types emits each type once, in the
				// order given, so a document has to be listed before every
				// document that references it.
				return []string{
					filepath.Join(src, "t.json"), filepath.Join(src, "root.json"),
					"-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types",
					"--draft", "3",
					"--root-name", "root.json=Root", "--root-name", "t.json=TDoc",
				}
			},
			"example.com/m/gen", "Root",
			[]docInstance{{`{"t":{"k":"y"}}`, true, ""}})
	})

	t.Run("one document with $defs", func(t *testing.T) {
		src := t.TempDir()
		writeFile(t, filepath.Join(src, "one.json"), constInDefs)
		generateCompileRun(t,
			func(modRoot string) []string {
				return []string{
					filepath.Join(src, "one.json"),
					"-o", filepath.Join(modRoot, "gen"), "-p", "gen",
					"--draft", "3", "--root-name", "Root",
				}
			},
			"example.com/m/gen", "Root",
			[]docInstance{{`{"t":{"k":"y"}}`, true, ""}})
	})
}

// The same gap going forward instead of back. Draft 3's per-property
// `"required": true` is a spelling no later dialect defines, so under
// --draft 2020-12 the reached document states nothing by it and the property is
// optional -- as it already was in the document the caller listed.
func TestDraftOverrideReachesADocumentReachedByRefGoingForward(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "root.json"), `{
		"id": "https://ex.test/root.json",
		"title": "Root",
		"type": "object",
		"properties": {"t": {"$ref": "t.json"}}
	}`)
	writeFile(t, filepath.Join(src, "t.json"), `{
		"id": "https://ex.test/t.json",
		"title": "TDoc",
		"type": "object",
		"properties": {"a": {"type": "string", "required": true}}
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return []string{
				filepath.Join(src, "root.json"),
				"-o", filepath.Join(modRoot, "gen"), "-p", "gen",
				"--draft", "2020-12", "--root-name", "Root",
			}
		},
		"example.com/m/gen", "Root",
		[]docInstance{
			// 2020-12 has no per-property boolean "required", so "a" is optional.
			{`{"t":{}}`, true, ""},
			{`{"t":{"a":"s"}}`, true, ""},
		})
}

// The documented exception, held here so a fix for the case above cannot be
// taken further than it should: a resource reached by $ref that declares a
// $schema of its own keeps that dialect, which is what preserves cross-draft
// $ref semantics. It is also the rule the generator's draftForSchema applies to
// the same node, and normalization has to give the same answer or one node is
// read under two dialects.
//
// t.json declares draft 3 and states the per-property boolean `required`, so
// under --draft 2020-12 the property is still required -- by draft 3, which the
// document asked to be read as.
func TestAReachedDocumentDeclaringItsOwnSchemaKeepsThatDialect(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "root.json"), `{
		"id": "https://ex.test/root.json",
		"title": "Root",
		"type": "object",
		"properties": {"t": {"$ref": "t.json"}}
	}`)
	writeFile(t, filepath.Join(src, "t.json"), `{
		"$schema": "http://json-schema.org/draft-03/schema#",
		"id": "https://ex.test/t.json",
		"title": "TDoc",
		"type": "object",
		"properties": {"a": {"type": "string", "required": true}}
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return []string{
				filepath.Join(src, "root.json"),
				"-o", filepath.Join(modRoot, "gen"), "-p", "gen",
				"--draft", "2020-12", "--root-name", "Root",
			}
		},
		"example.com/m/gen", "Root",
		[]docInstance{
			{`{"t":{}}`, false, ""},
			{`{"t":{"a":"s"}}`, true, ""},
		})
}
