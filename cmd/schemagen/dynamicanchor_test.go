package schemagen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file are about a plain-name fragment declared in one input
// document and named by another document's $ref.
//
// 2020-12 §8.2.2 says "$dynamicAnchor" behaves like "$anchor" in that it
// creates a plain name fragment, so a plain $ref naming one has to resolve. It
// did not, across documents, while "$anchor" at the identical location did and
// while the same schema set spelled as one document with an embedded resource
// did too: three indexes of anchors each carried their own list of which
// keywords declare a name, and the one the cross-document path uses was the one
// missing "$dynamicAnchor" (issue #307). The lists are now one function,
// schema.AnchorNames.
//
// They generate through the CLI, compile the result and *run* it against
// documents the referenced schema accepts and rejects, because the failure a
// weaker check would miss is not "generation exited non-zero": it is a $ref
// that resolves to something and enforces nothing. Every case below therefore
// asserts the referenced minLength is applied through the anchor.

// anchorDeclSpelling is one way of writing the referenced document, all of them
// declaring the plain-name fragment "node" over {"type":"string","minLength":3}.
type anchorDeclSpelling struct {
	name string
	doc  string
}

// The referring document is the same in every case: it names the fragment in
// the other document by absolute URI.
const anchorRefDoc = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"$id": "https://ex.test/m.json",
	"title": "MDoc",
	"type": "object",
	"properties": {"a": {"$ref": "https://ex.test/t.json#node"}}
}`

// anchorVerdicts is the verdict the referenced schema gives, and the whole point
// of the fixture: "ab" is refused only if minLength reached the generated type
// through the anchor. The empty document is the control -- the property is
// optional, so a delegation running against the Go zero of an absent property
// would wrongly reject it.
var anchorVerdicts = []docInstance{
	{`{}`, true, ""},
	{`{"a":"abc"}`, true, ""},
	{`{"a":"ab"}`, false, ""},
}

// anchorSpellings covers the rows of the issue's table: the fragment at the
// referenced document's root and inside its $defs, declared by "$anchor", by
// "$dynamicAnchor", and by both at once.
var anchorSpellings = []anchorDeclSpelling{
	{"root/anchor", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/t.json", "title": "TDoc",
		"$anchor": "node", "type": "string", "minLength": 3
	}`},
	{"root/dynamicAnchor", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/t.json", "title": "TDoc",
		"$dynamicAnchor": "node", "type": "string", "minLength": 3
	}`},
	{"root/both", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/t.json", "title": "TDoc",
		"$anchor": "node", "$dynamicAnchor": "node", "type": "string", "minLength": 3
	}`},
	{"defs/anchor", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/t.json", "title": "TDoc", "type": "object",
		"$defs": {"X": {"$anchor": "node", "type": "string", "minLength": 3}}
	}`},
	{"defs/dynamicAnchor", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/t.json", "title": "TDoc", "type": "object",
		"$defs": {"X": {"$dynamicAnchor": "node", "type": "string", "minLength": 3}}
	}`},
	// The pre-2019-09 spelling of the same thing. It is in this table because
	// the three indexes disagreed about it too, in the other direction: the
	// resolver knew it and the resource graph did not.
	{"defs/legacyIDFragment", `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/t.json", "title": "TDoc", "type": "object",
		"definitions": {"X": {"$id": "#node", "type": "string", "minLength": 3}}
	}`},
}

// writeAnchorPair writes the referenced and referring documents into a fresh
// directory and returns their paths, referenced first.
func writeAnchorPair(t *testing.T, referenced, referring string) []string {
	t.Helper()
	dir := t.TempDir()
	tPath := filepath.Join(dir, "t.json")
	mPath := filepath.Join(dir, "m.json")
	writeFile(t, tPath, referenced)
	writeFile(t, mPath, referring)
	return []string{tPath, mPath}
}

// A $ref naming a plain-name fragment in another *input document* must resolve
// and must carry that fragment's constraints into the generated type, under
// every keyword that declares such a fragment.
func TestSharedTypesResolvesARefNamingAnAnchorInAnotherDocument(t *testing.T) {
	for _, sp := range anchorSpellings {
		t.Run(sp.name, func(t *testing.T) {
			paths := writeAnchorPair(t, sp.doc, anchorRefDoc)
			generateCompileRun(t,
				func(modRoot string) []string {
					return append(append([]string{}, paths...),
						"-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types")
				},
				"example.com/m/gen", "MDoc", anchorVerdicts)
		})
	}
}

// The same set spelled as one document -- the second document embedded as a
// resource under the same $id -- and spelled as two input documents must give
// the same verdicts. The single-document spelling was the one that already
// worked, so it is the reference the cross-document path is held to rather than
// a second copy of the expectation.
func TestOneDocumentAndTwoDocumentSpellingsOfAnAnchorAgree(t *testing.T) {
	for _, sp := range anchorSpellings {
		t.Run(sp.name, func(t *testing.T) {
			// Embed the referenced document as a $defs entry of the referring
			// one. It keeps its own $id, so it is the same resource.
			dir := t.TempDir()
			single := fmt.Sprintf(`{
				"$schema": "https://json-schema.org/draft/2020-12/schema",
				"$id": "https://ex.test/m.json",
				"title": "MDoc",
				"type": "object",
				"properties": {"a": {"$ref": "https://ex.test/t.json#node"}},
				"$defs": {"T": %s}
			}`, sp.doc)
			path := filepath.Join(dir, "single.json")
			writeFile(t, path, single)

			generateCompileRun(t,
				func(modRoot string) []string {
					return []string{path, "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
				},
				"example.com/m/gen", "MDoc", anchorVerdicts)
		})
	}
}

// A $dynamicRef naming the fragment across documents was refused by the same
// lookup and with the same message, because it too ends at the anchor index of
// the document it points into. Both the absolute and the relative spelling of
// the reference are checked: they take different routes to the same index.
func TestSharedTypesResolvesADynamicRefNamingAnotherDocument(t *testing.T) {
	referenced := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/t.json", "title": "TDoc",
		"$dynamicAnchor": "node", "type": "string", "minLength": 3
	}`
	for _, ref := range []string{"https://ex.test/t.json#node", "t.json#node"} {
		t.Run(ref, func(t *testing.T) {
			referring := fmt.Sprintf(`{
				"$schema": "https://json-schema.org/draft/2020-12/schema",
				"$id": "https://ex.test/m.json",
				"title": "MDoc",
				"type": "object",
				"properties": {"a": {"$dynamicRef": %q}}
			}`, ref)
			paths := writeAnchorPair(t, referenced, referring)
			generateCompileRun(t,
				func(modRoot string) []string {
					return append(append([]string{}, paths...),
						"-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types")
				},
				"example.com/m/gen", "MDoc", anchorVerdicts)
		})
	}
}

// "$recursiveAnchor" is not in this family and must not be made a member of it.
// 2019-09 gives it a boolean, so it declares no name a "#name" fragment could
// spell, and a $ref inventing one must still be refused rather than silently
// landing on the resource root. The companion row is the reference that does
// work: the document URI with no fragment at all.
func TestRecursiveAnchorDeclaresNoNameARefCanSpell(t *testing.T) {
	referenced := `{
		"$schema": "https://json-schema.org/draft/2019-09/schema",
		"$id": "https://ex.test/t.json", "title": "TDoc",
		"$recursiveAnchor": true,
		"type": "string", "minLength": 3
	}`

	// A fragment naming the anchor keyword or its value resolves to nothing.
	for _, frag := range []string{"#true", "#recursiveAnchor", "#node"} {
		t.Run("refused"+frag, func(t *testing.T) {
			referring := fmt.Sprintf(`{
				"$schema": "https://json-schema.org/draft/2019-09/schema",
				"$id": "https://ex.test/m.json",
				"title": "MDoc",
				"type": "object",
				"properties": {"a": {"$ref": "https://ex.test/t.json%s"}}
			}`, frag)
			paths := writeAnchorPair(t, referenced, referring)
			err := runGenerateArgs(t, append(append([]string{}, paths...),
				"-o", t.TempDir(), "-p", "gen", "--shared-types")...)
			if err == nil {
				t.Fatalf("$ref to %q resolved; $recursiveAnchor declares no plain-name fragment", frag)
			}
			if !strings.Contains(err.Error(), "cannot resolve $ref") {
				t.Fatalf("want an unresolved-$ref refusal, got: %v", err)
			}
		})
	}

	t.Run("documentURIResolves", func(t *testing.T) {
		referring := `{
			"$schema": "https://json-schema.org/draft/2019-09/schema",
			"$id": "https://ex.test/m.json",
			"title": "MDoc",
			"type": "object",
			"properties": {"a": {"$ref": "https://ex.test/t.json"}}
		}`
		paths := writeAnchorPair(t, referenced, referring)
		generateCompileRun(t,
			func(modRoot string) []string {
				return append(append([]string{}, paths...),
					"-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types")
			},
			"example.com/m/gen", "MDoc", anchorVerdicts)
	})
}

// The same question one layer out: a $ref naming a $dynamicAnchor in a document
// this run puts in *another Go package* must import that package's type rather
// than declare a second copy of it. This is what --schema-package exists to do,
// and it could not be asked at all while nothing resolved (issue #307 blocked
// the round-two sweep of it).
func TestMultiPackageRefNamingADynamicAnchorImportsTheForeignType(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "t.json"), `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/t.json", "title": "TDoc",
		"$dynamicAnchor": "node", "type": "string", "minLength": 3
	}`)
	writeFile(t, filepath.Join(src, "m.json"), anchorRefDoc)

	out := t.TempDir()
	err := runGenerateArgs(t,
		filepath.Join(src, "t.json"), filepath.Join(src, "m.json"),
		"--schema-package", "https://ex.test/t.json=example.com/m/tpkg",
		"--schema-package", "https://ex.test/m.json=example.com/m/mpkg",
		"-o", out)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	body, readErr := os.ReadFile(filepath.Join(out, "mpkg", "m.go"))
	if readErr != nil {
		t.Fatalf("read generated file: %v", readErr)
	}
	got := string(body)
	if !strings.Contains(got, `"example.com/m/tpkg"`) {
		t.Errorf("referring package does not import the referenced one:\n%s", got)
	}
	if !strings.Contains(got, "*tpkg.TDoc") {
		t.Errorf("property is not typed by the foreign package's type:\n%s", got)
	}
	// The shape must not have been copied in alongside the import.
	if strings.Contains(got, "func (t TDoc) Validate()") {
		t.Errorf("referring package declared its own copy of the foreign type:\n%s", got)
	}
}
