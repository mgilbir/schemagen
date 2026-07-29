package schemagen

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// Key namespaces accepted by --root-name. An unprefixed key is a schema file's
// base name, which is what the flag has always meant.
const (
	rootNameIDPrefix   = "id:"
	rootNameFilePrefix = "file:"
)

// rootNameSpec resolves the root type name for an input schema.
//
// A key may name the document's $id, the schema path as given on the command
// line, or the file's base name. Base names are not unique across an input set —
// two directories can each hold a common.json — so keying by base name alone
// cannot give such documents different root type names. The $id and path
// namespaces exist for that case; $id is already how --schema-package and
// --schema-output identify documents.
type rootNameSpec struct {
	byID   map[string]string
	byFile map[string]string
	byBase map[string]string
	// bare is the unkeyed form, which applies to a single input.
	bare string

	used map[string]bool
}

func newRootNameSpec() *rootNameSpec {
	return &rootNameSpec{
		byID:   map[string]string{},
		byFile: map[string]string{},
		byBase: map[string]string{},
		used:   map[string]bool{},
	}
}

// parseRootNameFlags builds a spec from the repeatable --root-name values.
func parseRootNameFlags(values []string) (*rootNameSpec, error) {
	spec := newRootNameSpec()
	for _, v := range values {
		// Split on the LAST "=": both $ids and file paths may contain one, and
		// a Go type name never does.
		i := strings.LastIndex(v, "=")
		if i < 0 {
			if spec.bare != "" && spec.bare != v {
				return nil, fmt.Errorf("--root-name given twice without a key (%q then %q); the unkeyed form names the single input schema", spec.bare, v)
			}
			spec.bare = v
			continue
		}
		key, name := v[:i], v[i+1:]
		if key == "" || name == "" {
			return nil, fmt.Errorf("--root-name %q: expected <key>=<Name>, where <key> is a schema base name, %s<document $id> or %s<schema path>", v, rootNameIDPrefix, rootNameFilePrefix)
		}
		var target map[string]string
		switch {
		case strings.HasPrefix(key, rootNameIDPrefix):
			key, target = strings.TrimPrefix(key, rootNameIDPrefix), spec.byID
		case strings.HasPrefix(key, rootNameFilePrefix):
			key, target = strings.TrimPrefix(key, rootNameFilePrefix), spec.byFile
		default:
			target = spec.byBase
		}
		if key == "" {
			return nil, fmt.Errorf("--root-name %q: the key is empty after its prefix", v)
		}
		if prev, ok := target[key]; ok && prev != name {
			return nil, fmt.Errorf("--root-name key %q given twice (%q then %q); each key names one root type", key, prev, name)
		}
		target[key] = name
	}
	return spec, nil
}

// validate rejects a bare --root-name given alongside several inputs, where it
// would name every root identically.
func (r *rootNameSpec) validate(inputCount int) error {
	if r.bare != "" && inputCount > 1 {
		return fmt.Errorf("--root-name %q applies to a single schema file, got %d (use <key>=<Name> pairs, %s<document $id>=<Name>, or --root-name-from-filename)", r.bare, inputCount, rootNameIDPrefix)
	}
	return nil
}

// lookup returns the configured root type name for one input, matching the most
// specific key first: the path as given, its absolute form, the document $id,
// then the file's base name.
func (r *rootNameSpec) lookup(argPath, docID string) string {
	if r == nil {
		return ""
	}
	type candidate struct {
		m   map[string]string
		key string
	}
	var candidates []candidate
	if argPath != "" {
		candidates = append(candidates, candidate{r.byFile, argPath})
		if abs, err := filepath.Abs(argPath); err == nil && abs != argPath {
			candidates = append(candidates, candidate{r.byFile, abs})
		}
	}
	if docID != "" {
		candidates = append(candidates,
			candidate{r.byID, docID},
			candidate{r.byID, strings.TrimSuffix(docID, "#")})
	}
	if argPath != "" {
		candidates = append(candidates, candidate{r.byBase, filepath.Base(argPath)})
	}
	for _, c := range candidates {
		if name, ok := c.m[c.key]; ok {
			r.used[c.key] = true
			return name
		}
	}
	if r.bare != "" {
		r.used[r.bare] = true
		return r.bare
	}
	return ""
}

// warnUnused reports keys that matched no input, which is almost always a typo.
// Mirrors how unused --field-map entries are reported.
//
// A base-name key matching several inputs is deliberately not reported: two
// documents sharing a file name in different packages may legitimately want the
// same root type name. Where that is a genuine conflict — the same package — the
// generator already refuses the duplicate root type.
func (r *rootNameSpec) warnUnused(w io.Writer) {
	if r == nil {
		return
	}
	var unused []string
	for _, m := range []map[string]string{r.byID, r.byFile, r.byBase} {
		for key := range m {
			if !r.used[key] {
				unused = append(unused, key)
			}
		}
	}
	if r.bare != "" && !r.used[r.bare] {
		unused = append(unused, r.bare)
	}
	sort.Strings(unused)
	for _, key := range unused {
		fmt.Fprintf(w, "warning: --root-name %q matched no input schema\n", key)
	}
}

// docIDOf returns a schema's declared identity, preferring $id over the draft-3/4
// "id" spelling, with any trailing empty fragment removed.
func docIDOf(s *schema.Schema) string {
	if s == nil {
		return ""
	}
	if id := strings.TrimSuffix(s.ID, "#"); id != "" {
		return id
	}
	return strings.TrimSuffix(s.LegacyID, "#")
}
