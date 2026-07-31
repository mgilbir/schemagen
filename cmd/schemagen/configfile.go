package schemagen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
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
	BigInt               *bool   `json:"bigInt,omitempty"`
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
	byID   map[string]generator.FieldNameMap
	byPath map[string]generator.FieldNameMap
	byBase generator.FieldMapFile
}

func newFieldNameSpec(cfg *ConfigFile, fieldMap generator.FieldMapFile) *fieldNameSpec {
	spec := &fieldNameSpec{
		byID:   map[string]generator.FieldNameMap{},
		byPath: map[string]generator.FieldNameMap{},
		byBase: fieldMap,
	}
	if cfg == nil {
		return spec
	}
	for _, d := range cfg.Documents {
		if len(d.FieldNames) == 0 {
			continue
		}
		if d.ID != "" {
			spec.byID[strings.TrimSuffix(d.ID, "#")] = d.FieldNames
		}
		if d.Path != "" {
			spec.byPath[absOrSelf(d.Path)] = d.FieldNames
		}
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
		sources = append(sources, s.byID[strings.TrimSuffix(docID, "#")])
	}
	if argPath != "" {
		sources = append(sources, s.byPath[absOrSelf(argPath)])
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
