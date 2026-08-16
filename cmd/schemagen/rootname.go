package schemagen

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
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
	// usedForInput is the subset of used that answered for a document the
	// caller listed, as against one a $ref reached. See warnUnused.
	usedForInput map[string]bool
	// externalKey maps the label of each $ref-reached document a key answered
	// for to that key, first answer kept, and prefixApplied marks the labels
	// whose answer was actually used to qualify a contested name.
	//
	// A key naming such a document sets the name its definitions are qualified
	// with and does not name its root type, which is the documented split. What
	// it must not do is claim to have done something and be silent about it:
	// with nothing to qualify, the key had no effect a caller could observe,
	// and it was still counted as used and so reported as nothing at all. Issue
	// #331.
	externalKey   map[string]string
	prefixApplied map[string]bool

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
		byID:          map[string]string{},
		byFile:        map[string]string{},
		byBase:        map[string]string{},
		used:          map[string]bool{},
		usedForInput:  map[string]bool{},
		externalKey:   map[string]string{},
		prefixApplied: map[string]bool{},
		configKeys:    map[string]bool{},
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

// lookup returns the configured root type name for one input, and records the
// key that answered as having named a listed document.
func (r *rootNameSpec) lookup(argPath, docID string) string {
	key, name := r.match(argPath, docID)
	if key == "" {
		return ""
	}
	r.used[key] = true
	r.usedForInput[key] = true
	return name
}

// lookupExternal is lookup for a document nobody listed -- one a $ref reached,
// whose definitions this package materializes all the same. label is what
// collectExternalClaims settled on for it, which is a file path where the
// document is one and its $id where it is a resource embedded in another.
//
// The answer is the same; only the bookkeeping differs. Such a key does not name
// the document's root type -- that is named from the document's own title -- so
// a key that only ever answered here has done less than the flag's help says,
// and where nothing needed qualifying it has done nothing at all. warnUnused is
// what says so, and this is what lets it tell the two uses apart.
func (r *rootNameSpec) lookupExternal(label, docID string) string {
	key, name := r.match(label, docID)
	if key == "" {
		return ""
	}
	r.used[key] = true
	if _, seen := r.externalKey[label]; !seen {
		r.externalKey[label] = key
	}
	return name
}

// notePrefixApplied records that the name this spec gave the document labelled
// label was used to qualify a contested Go type name -- the one effect a key
// naming a $ref-reached document has. See resolveSharedDefinitionNames, which
// calls it, and warnUnused, which reads it.
func (r *rootNameSpec) notePrefixApplied(label string) {
	if r == nil || label == "" {
		return
	}
	r.prefixApplied[label] = true
}

// match reports the key that answers for one document and the name it carries,
// trying the most specific namespace first: the path as given, its absolute
// form, the document $id, then the file's base name. The empty key means no key
// answered.
func (r *rootNameSpec) match(argPath, docID string) (key, name string) {
	if r == nil {
		return "", ""
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
			return c.key, name
		}
	}
	if r.bare != "" {
		return r.bare, r.bare
	}
	return "", ""
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

	r.warnInertExternalKeys(w)
}

// warnInertExternalKeys reports the keys whose only answer was for a document
// this run reached by $ref, where that answer changed nothing.
//
// Such a key is honoured, and for exactly one thing: it names the prefix that
// document's definitions are qualified with where two documents claim one Go
// type name. It does not name the document's root type, which is derived from
// the document's own title -- the split the name-contest diagnostic states in as
// many words. So a key that reached a contest did something the caller can see,
// in that diagnostic, and says nothing more here.
//
// A key that reached no contest did nothing whatsoever, and was silent: it had
// been consulted, which is all warnUnused above asks, so it counted as used. The
// caller asked for PinnedLeaf, got Leaf, and was told nothing -- while a key
// naming no document at all is reported. Issue #331. This is the line that was
// missing, and it changes no behaviour: what the key does and does not do is
// what it always did.
func (r *rootNameSpec) warnInertExternalKeys(w io.Writer) {
	labels := make([]string, 0, len(r.externalKey))
	for label := range r.externalKey {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		key := r.externalKey[label]
		// A key that also named a listed input is doing the job the flag
		// advertises, whatever else it reached.
		if r.usedForInput[key] || r.prefixApplied[label] {
			continue
		}
		source := fmt.Sprintf("--root-name %q", key)
		if r.configKeys[key] {
			source = fmt.Sprintf("config rootName for %q", r.configEntryLabel(key))
		}
		fmt.Fprintf(w, "warning: %s names %s, which this run reached by $ref rather than being given as an input. "+
			"For such a document the name sets the prefix its definitions are qualified with where another document claims one of their Go type names, "+
			"and nothing here was contested, so it had no effect. The document's own root type is named from its title; "+
			"list the document as an input of the run to name that.\n", source, label)
	}
}

// configEntryLabel names the config entry a seeded key came from, so a warning
// about it points at the file that holds it rather than at a flag the command
// line may not carry. Falls back to the key, which is one of the entry's own
// selectors.
func (r *rootNameSpec) configEntryLabel(key string) string {
	for _, entry := range r.configEntries {
		if slices.Contains(entry.keys, key) {
			return entry.label
		}
	}
	return key
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
