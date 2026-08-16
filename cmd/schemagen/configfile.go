package schemagen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// ConfigFile is the JSON form of a generation run.
//
// Multi-document generation otherwise needs one repeatable flag per document per
// concern, which stops being reviewable well before it stops being typeable. The
// file is a CLI input-assembly concern: it carries no generation logic, and every
// setting it holds has an equivalent flag that overrides it.
//
// Global settings are pointers so an omitted setting is distinguishable from one
// explicitly set to a zero value.
type ConfigFile struct {
	OutputDir            *string `json:"outputDir,omitempty"`
	Package              *string `json:"package,omitempty"`
	OmitEmpty            *bool   `json:"omitEmpty,omitempty"`
	StrictProperties     *bool   `json:"strictProperties,omitempty"`
	StrictReadWrite      *bool   `json:"strictReadWrite,omitempty"`
	BigInt               *bool   `json:"bigInt,omitempty"`
	ExactNumbers         *bool   `json:"exactNumbers,omitempty"`
	FormatAssertion      *bool   `json:"formatAssertion,omitempty"`
	FormatAnnotation     *bool   `json:"formatAnnotation,omitempty"`
	AllowRemoteRefs      *bool   `json:"allowRemoteRefs,omitempty"`
	LenientRefs          *bool   `json:"lenientRefs,omitempty"`
	SharedTypes          *bool   `json:"sharedTypes,omitempty"`
	RootNameFromFilename *bool   `json:"rootNameFromFilename,omitempty"`
	Draft                *string `json:"draft,omitempty"`
	Validation           *string `json:"validation,omitempty"`

	Documents []ConfigDocument `json:"documents,omitempty"`
}

// ConfigDocument holds the settings for one schema document.
//
// A document is selected by its $id, by its path, or by both. Explicit selectors
// are used rather than a map keyed by identity because the key would otherwise
// have to be sometimes-$id and sometimes-path: $id is required for multi-package
// runs but optional elsewhere. That ambiguity is what made keying by file base
// name a defect.
type ConfigDocument struct {
	ID   string `json:"id,omitempty"`
	Path string `json:"path,omitempty"`

	// Package and Output are keyed by $id in the CLI and in the generator, so an
	// entry setting either must declare ID.
	Package string `json:"package,omitempty"`
	Output  string `json:"output,omitempty"`

	RootName   string                 `json:"rootName,omitempty"`
	FieldNames generator.FieldNameMap `json:"fieldNames,omitempty"`
}

// LoadConfigFile reads and validates a generation config. Unknown fields are
// rejected: in a file that exists to be reviewed, a mistyped key silently doing
// nothing is worse than a failed run.
func LoadConfigFile(path string) (*ConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cfg ConfigFile
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	if err := cfg.validate(path); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *ConfigFile) validate(path string) error {
	if c.Validation != nil {
		if _, err := parseValidationMode(*c.Validation); err != nil {
			return fmt.Errorf("config %s: %w", path, err)
		}
	}
	if c.Draft != nil {
		if _, err := parseDraft(*c.Draft); err != nil {
			return fmt.Errorf("config %s: %w", path, err)
		}
	}

	byID := map[string]int{}
	byPath := map[string]int{}
	for i, d := range c.Documents {
		where := fmt.Sprintf("config %s: documents[%d]", path, i)
		if d.ID == "" && d.Path == "" {
			return fmt.Errorf("%s: needs an \"id\" or a \"path\" to select a document", where)
		}
		if d.Package != "" && d.ID == "" {
			return fmt.Errorf("%s: \"package\" requires \"id\", since packages are assigned per document $id", where)
		}
		if d.Output != "" && d.ID == "" {
			return fmt.Errorf("%s: \"output\" requires \"id\", since outputs are assigned per document $id", where)
		}
		if d.RootName != "" && !isExportedIdent(d.RootName) {
			return fmt.Errorf("%s: rootName %q is not an exported Go identifier", where, d.RootName)
		}
		// An entry with an "id", no "path" and no setting is read by nothing:
		// "path" is what supplies an input, and the four settings are what an
		// entry can carry. Selecting a document and then saying nothing about it
		// is the same mistake as a mistyped key -- silently doing nothing in a
		// file that exists to be reviewed -- and unlike the settings, which are
		// answered by a warning once the run knows what matched, this one is
		// decidable from the file alone. Found alongside issue #318, which
		// closed the last of the settings that could match nothing in silence.
		if d.Path == "" && d.Package == "" && d.Output == "" && d.RootName == "" && len(d.FieldNames) == 0 {
			return fmt.Errorf("%s: selects $id %q and sets nothing; give it a \"package\", \"output\", \"rootName\" or \"fieldNames\", or a \"path\" to make it an input", where, d.ID)
		}
		if d.ID != "" {
			id := strings.TrimSuffix(d.ID, "#")
			if prev, ok := byID[id]; ok {
				return fmt.Errorf("%s: $id %q already configured by documents[%d]", where, id, prev)
			}
			byID[id] = i
		}
		if d.Path != "" {
			abs := absOrSelf(d.Path)
			if prev, ok := byPath[abs]; ok {
				return fmt.Errorf("%s: path %q already configured by documents[%d]", where, d.Path, prev)
			}
			byPath[abs] = i
		}
	}
	return nil
}

// inputPaths returns the document paths the config supplies, in file order, for
// a run given no input paths on the command line.
func (c *ConfigFile) inputPaths() []string {
	var paths []string
	for _, d := range c.Documents {
		if d.Path != "" {
			paths = append(paths, d.Path)
		}
	}
	return paths
}

// schemaPackages and schemaOutputs return the $id-keyed maps the CLI already
// uses, so config and flag values merge in one place.
func (c *ConfigFile) schemaPackages() map[string]string {
	out := map[string]string{}
	for _, d := range c.Documents {
		if d.Package != "" {
			out[strings.TrimSuffix(d.ID, "#")] = d.Package
		}
	}
	return out
}

func (c *ConfigFile) schemaOutputs() map[string]string {
	out := map[string]string{}
	for _, d := range c.Documents {
		if d.Output != "" {
			out[strings.TrimSuffix(d.ID, "#")] = d.Output
		}
	}
	return out
}

// isExportedIdent reports whether s is an exported Go identifier. This is an
// early check so a bad config fails before any generation; the generator applies
// the authoritative one when the name is used.
func isExportedIdent(s string) bool {
	if !token.IsIdentifier(s) || token.Lookup(s).IsKeyword() {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}

func absOrSelf(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// fieldNameSpec resolves per-document field-name overrides from two sources: the
// config file, keyed by document $id or path, and --field-map, keyed by file base
// name. Base names are not unique across an input set, which is why the config
// keys by document identity; --field-map keeps its historical keying and wins on
// conflict, since an explicit flag outranks the file.
type fieldNameSpec struct {
	byID   map[string]*configFieldNames
	byPath map[string]*configFieldNames
	byBase generator.FieldMapFile

	// The config's entries in file order, so one that took no effect can be
	// reported. Every keyed input in this tool reports a key that matched
	// nothing and this one did not: the report was handed the --field-map file
	// alone, so the identical override written in the config said nothing at
	// all -- while the rootName on the same config entry did. Issue #318.
	configEntries []*configFieldNames

	// The overrides the generator reported as applied, keyed by the input path
	// they were looked up for. warnUnusedFieldMap reads the same facts keyed by
	// file base name, which is all a --field-map key can name; a config entry
	// names one document, and two inputs can share a base name (one/common.json
	// and two/common.json), so answering for it out of a base-name index would
	// let one document's applied override excuse another document's typo.
	applied map[string]map[string]map[string]bool
}

// configFieldNames is one config document entry's fieldNames, kept for
// reporting. Mirrors configRootName, which the rootName on the same entry is
// reported through.
type configFieldNames struct {
	// label names the entry the way its own source would: its $id, or its path
	// where the entry has none.
	label string
	names generator.FieldNameMap

	// matched records that some input selected this entry, and paths the inputs
	// it was applied to -- the two questions the report asks, in that order. An
	// entry nothing selected is one warning; an entry that was selected is
	// judged override by override against what those inputs applied.
	matched bool
	paths   []string
}

// selected records that one input took this entry's overrides.
func (e *configFieldNames) selected(argPath string) {
	if e == nil {
		return
	}
	e.matched = true
	if argPath == "" || slices.Contains(e.paths, argPath) {
		return
	}
	e.paths = append(e.paths, argPath)
}

func newFieldNameSpec(cfg *ConfigFile, fieldMap generator.FieldMapFile) *fieldNameSpec {
	spec := &fieldNameSpec{
		byID:   map[string]*configFieldNames{},
		byPath: map[string]*configFieldNames{},
		byBase: fieldMap,
	}
	if cfg == nil {
		return spec
	}
	for _, d := range cfg.Documents {
		if len(d.FieldNames) == 0 {
			continue
		}
		// One entry, registered under both selectors: an entry is what matched
		// or did not, and reporting its $id key and its path key separately
		// would report one mistake twice and one non-mistake besides, since only
		// one of the two can be the key a lookup hits. Same reason
		// rootNameSpec.seedFromConfig groups its keys by entry.
		entry := &configFieldNames{label: d.ID, names: d.FieldNames}
		if entry.label == "" {
			entry.label = d.Path
		}
		if d.ID != "" {
			spec.byID[strings.TrimSuffix(d.ID, "#")] = entry
		}
		if d.Path != "" {
			spec.byPath[absOrSelf(d.Path)] = entry
		}
		spec.configEntries = append(spec.configEntries, entry)
	}
	return spec
}

// lookup merges the applicable overrides for one document, with --field-map
// entries taking precedence over the config's.
func (s *fieldNameSpec) lookup(argPath, docID string) generator.FieldNameMap {
	if s == nil {
		return nil
	}
	var sources []generator.FieldNameMap
	if docID != "" {
		if e := s.byID[strings.TrimSuffix(docID, "#")]; e != nil {
			e.selected(argPath)
			sources = append(sources, e.names)
		}
	}
	if argPath != "" {
		if e := s.byPath[absOrSelf(argPath)]; e != nil {
			e.selected(argPath)
			sources = append(sources, e.names)
		}
		// Last, so it overrides the config on conflict.
		sources = append(sources, s.byBase[filepath.Base(argPath)])
	}

	var merged generator.FieldNameMap
	for _, src := range sources {
		for typeName, props := range src {
			if merged == nil {
				merged = generator.FieldNameMap{}
			}
			if merged[typeName] == nil {
				merged[typeName] = map[string]string{}
			}
			for prop, name := range props {
				merged[typeName][prop] = name
			}
		}
	}
	return merged
}

// noteApplied records the overrides the generator reported as applied for one
// input. Called from both generation paths, beside where the same facts are
// folded into the base-name index --field-map is reported from.
func (s *fieldNameSpec) noteApplied(argPath string, applied map[string]map[string]bool) {
	if s == nil || argPath == "" || len(applied) == 0 {
		return
	}
	if s.applied == nil {
		s.applied = map[string]map[string]map[string]bool{}
	}
	dst := s.applied[argPath]
	if dst == nil {
		dst = map[string]map[string]bool{}
		s.applied[argPath] = dst
	}
	for typeName, props := range applied {
		if dst[typeName] == nil {
			dst[typeName] = map[string]bool{}
		}
		for prop := range props {
			dst[typeName][prop] = true
		}
	}
}

// warnUnusedConfigFieldNames reports config fieldNames that took no effect: an
// entry whose id/path selected no input at all, and, for an entry that did
// select one, each override that matched no property there.
//
// Same shape as warnUnusedFieldMap and rootNameSpec.warnUnused, and deliberately
// so: a key that matched nothing is one thing, and a caller should not have to
// learn a different sentence for each place one can be written. The entry is
// named as the config's, since "--field-map" would send the reader to a flag the
// run may not have been given at all.
//
// An entry nothing selected warns once for the entry rather than once per
// override, the way warnUnusedFieldMap warns once for a file key naming no
// generated file: the overrides under a dead selector were never asked, and a
// line each would bury the one fact that explains them.
//
// An override is judged matched when the property was renamed for one of the
// inputs the entry reached, whichever source's name won: a --field-map entry
// overriding the same property is precedence working as documented, not a typo.
// Unlike rootName, an entry naming a document the run only reached by $ref
// counts as unmatched -- overrides apply through the generator of a listed
// input, so there is nothing for such an entry to do.
func (s *fieldNameSpec) warnUnusedConfigFieldNames(w io.Writer) {
	if s == nil || w == nil {
		return
	}
	var warnings []string
	for _, e := range s.configEntries {
		if !e.matched {
			warnings = append(warnings, fmt.Sprintf("config fieldNames for %q matched no input schema", e.label))
			continue
		}
		for typeName, props := range e.names {
			for prop := range props {
				applied := false
				for _, path := range e.paths {
					if s.applied[path][typeName][prop] {
						applied = true
						break
					}
				}
				if !applied {
					warnings = append(warnings, fmt.Sprintf(
						"config fieldNames for %q: entry %q matched no property", e.label, typeName+"."+prop))
				}
			}
		}
	}
	sort.Strings(warnings)
	for _, msg := range warnings {
		fmt.Fprintf(w, "warning: %s\n", msg)
	}
}

// applyString sets *dst from a config value unless the flag was given.
func applyString(cmd *cobra.Command, flag string, cfgVal *string, dst *string) {
	if cfgVal != nil && !cmd.Flags().Changed(flag) {
		*dst = *cfgVal
	}
}

// applyBool sets *dst from a config value unless the flag was given.
func applyBool(cmd *cobra.Command, flag string, cfgVal *bool, dst *bool) {
	if cfgVal != nil && !cmd.Flags().Changed(flag) {
		*dst = *cfgVal
	}
}
