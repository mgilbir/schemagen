package schemagen

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// runDocuments is what every mode of a run starts from: the input documents,
// loaded once, and the one resource index every generator of the run resolves
// references through.
//
// It is built here, once and the same way, for the default mode, for
// --shared-types and for --schema-package and the config file's packages. The
// three used to wire their own resolvers, and they disagreed about the thing a
// resolver is for. The default mode rooted a file resolver at each input's
// directory; --shared-types rooted one at the first input's directory for every
// input; --schema-package rooted one at every input directory and took the first
// that answered. A relative $ref in b/y.json then read b/z.json, a/z.json or
// a/z.json again depending on the flag, and the two wrong answers were given in
// silence. Every document now has an absolute base URI -- the file:// URI of
// the file it was read from, unless its $id says otherwise -- and a reference
// is resolved against the base URI of the document it is written in, whichever
// mode is running.
type runDocuments struct {
	index *schema.ResourceIndex
	// byPath maps each input path, as the caller wrote it, to its document.
	// Two paths naming one file map to one document.
	byPath map[string]*schema.Schema
}

// loadRunDocuments loads every input, normalizes it under the run's --draft,
// and registers it with a new resource index under the file it was read from.
//
// The index loads what the inputs refer to through one loader chain: a file
// resolver confined to the input directories -- each reference to the
// directories that hold the file it is written in, so listing b/y.json does
// not open b/ to a/x.json -- and, with --allow-remote-refs, an HTTP resolver.
// A document the run lists and a reference reaches is not loaded twice: the
// index answers the reference with the instance registered here, whether the
// reference spells it by path or by $id.
//
// Two inputs that claim one URI -- the same $id, or one's $id naming the other's
// file -- are refused: see schema.DuplicateIdentifierError.
func loadRunDocuments(args []string, draft schema.Draft, allowRemoteRefs bool) (*runDocuments, error) {
	loaders := []schema.SchemaResolver{
		// The same --draft the inputs are normalized under. A document a $ref
		// pulls in is a document of this run too, and reading it under a
		// dialect nobody asked for is how one command line came to enforce two
		// (issue #314).
		schema.NewFileResolver("", schema.WithFileResolverRoots(inputDirs(args)...), schema.WithFileResolverDraft(draft)),
	}
	if allowRemoteRefs {
		loaders = append(loaders, schema.NewHTTPResolver(schema.WithHTTPResolverDraft(draft)))
	}
	docs := &runDocuments{
		index:  schema.NewResourceIndex(schema.NewCompositeResolver(loaders...), schema.WithIndexDraft(draft)),
		byPath: make(map[string]*schema.Schema, len(args)),
	}
	byFile := make(map[string]*schema.Schema, len(args))
	for _, schemaPath := range args {
		file := schemaPath
		if abs, err := filepath.Abs(schemaPath); err == nil {
			file = abs
		}
		if s, ok := byFile[file]; ok {
			docs.byPath[schemaPath] = s
			continue
		}
		s, err := schema.LoadFromFile(schemaPath)
		if err != nil {
			return nil, fmt.Errorf("loading %s: %w", schemaPath, err)
		}
		// --draft is the caller's statement about the document, and it has to
		// reach normalization as well as generation: normalization is where a
		// keyword the dialect does not define is dropped, and answering "which
		// dialect" in two places from two sources is issue #203 in miniature.
		// Config.Draft carries the same value on to the generator.
		s.NormalizeForDraft(draft)
		if err := docs.index.AddDocument(s, nil); err != nil {
			return nil, fmt.Errorf("loading %s: %w", schemaPath, err)
		}
		byFile[file] = s
		docs.byPath[schemaPath] = s
	}
	return docs, nil
}

// inputDirs returns the distinct directories holding the run's input schemas,
// in first-seen order: the directory subtrees a reference may read files from.
func inputDirs(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	var dirs []string
	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		dir := filepath.Dir(abs)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}
	return dirs
}

// splitRef separates a $ref into its document part and its fragment (without
// the leading "#").
func splitRef(ref string) (doc, fragment string) {
	if i := strings.Index(ref, "#"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}
