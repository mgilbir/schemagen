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

	// Keys seeded from a config file, grouped by the entry that supplied them.
	// One entry contributes several keys ($id and path) but only the
	// highest-precedence one can match, so an unused key is not evidence of a
	// problem — the entry as a whole is what may have matched nothing.
	configEntries []configRootName
	configKeys    map[string]bool
}

// configRootName is one config entry's contribution, kept for reporting.
type configRootName struct {
	label string
	keys  []string
}

func newRootNameSpec() *rootNameSpec {
	return &rootNameSpec{
		byID:       map[string]string{},
		byFile:     map[string]string{},
		byBase:     map[string]string{},
		used:       map[string]bool{},
		configKeys: map[string]bool{},
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

// flagTargets reports whether a --root-name flag already names the document a
// config entry selects. Only flag keys are present when the config is seeded.
func (r *rootNameSpec) flagTargets(d ConfigDocument) bool {
	if r.bare != "" {
		// The bare form names the single input, whatever it is.
		return true
	}
	if d.ID != "" {
		if _, ok := r.byID[strings.TrimSuffix(d.ID, "#")]; ok {
			return true
		}
	}
	if d.Path != "" {
		for _, key := range []string{d.Path, absOrSelf(d.Path), filepath.Base(d.Path)} {
			if _, ok := r.byFile[key]; ok {
				return true
			}
			if _, ok := r.byBase[key]; ok {
				return true
			}
		}
	}
	return false
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
			if !r.used[key] && !r.configKeys[key] {
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

	for _, entry := range r.configEntries {
		matched := false
		for _, key := range entry.keys {
			if r.used[key] {
				matched = true
				break
			}
		}
		if !matched {
			fmt.Fprintf(w, "warning: config rootName for %q matched no input schema\n", entry.label)
		}
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

// seedFromConfig adds root names from a config file. Flag-provided keys already
// present are left alone, so an explicit flag overrides the file.
func (r *rootNameSpec) seedFromConfig(cfg *ConfigFile) {
	if r == nil || cfg == nil {
		return
	}
	for _, d := range cfg.Documents {
		if d.RootName == "" {
			continue
		}
		// An explicit flag naming this document wins outright. Comparing key by
		// key would let a config entry keyed by path beat a flag keyed by $id,
		// since path is the more specific namespace — source has to dominate
		// namespace, or "flags override the config" would not hold.
		if r.flagTargets(d) {
			continue
		}
		entry := configRootName{label: d.ID}
		if entry.label == "" {
			entry.label = d.Path
		}
		add := func(m map[string]string, key string) {
			if key == "" {
				return
			}
			if _, taken := m[key]; !taken {
				m[key] = d.RootName
			}
			r.configKeys[key] = true
			entry.keys = append(entry.keys, key)
		}
		if d.ID != "" {
			add(r.byID, strings.TrimSuffix(d.ID, "#"))
		}
		if d.Path != "" {
			add(r.byFile, d.Path)
			if abs := absOrSelf(d.Path); abs != d.Path {
				add(r.byFile, abs)
			}
		}
		r.configEntries = append(r.configEntries, entry)
	}
}
