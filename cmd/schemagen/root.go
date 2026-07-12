package schemagen

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// NewRootCmd creates the root cobra command with a "generate" subcommand.
func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "schemagen",
		Short: "Generate Go types from JSON Schema files",
	}

	rootCmd.AddCommand(newGenerateCmd())
	return rootCmd
}

func newGenerateCmd() *cobra.Command {
	var (
		outputDir        string
		pkgName          string
		omitEmpty        bool
		strictProperties bool
		bigInt           bool
		verbose          bool
		allowRemoteRefs  bool
		draftStr         string
		validationStr    string
		fieldMapPath     string
	)

	cmd := &cobra.Command{
		Use:   "generate [schema files...]",
		Short: "Generate Go source files from JSON Schema definitions",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse draft override if specified.
			var draft schema.Draft
			if draftStr != "" {
				var err error
				draft, err = parseDraft(draftStr)
				if err != nil {
					return err
				}
			}

			validationMode, err := parseValidationMode(validationStr)
			if err != nil {
				return err
			}

			// Load optional field-name overrides. Keyed by schema-file base name.
			var fieldMap generator.FieldMapFile
			if fieldMapPath != "" {
				fieldMap, err = generator.LoadFieldMapFile(fieldMapPath)
				if err != nil {
					return err
				}
			}
			// Track which (file, type, property) overrides were applied, and which
			// schema files were actually generated, so we can warn about entries
			// that never matched. Reported via defer so warnings still surface even
			// if generation fails partway through.
			appliedByFile := make(map[string]map[string]map[string]bool)
			processedFiles := make(map[string]bool)
			defer warnUnusedFieldMap(cmd.ErrOrStderr(), fieldMap, appliedByFile, processedFiles)

			// Reject input sets where two schemas map to the same output file
			// (same base name in different directories). Without this the second
			// silently overwrites the first.
			if err := checkOutputCollisions(args); err != nil {
				return err
			}

			// Ensure output directory exists.
			if err := os.MkdirAll(outputDir, 0o755); err != nil {
				return fmt.Errorf("creating output directory: %w", err)
			}

			for _, schemaPath := range args {
				if verbose {
					fmt.Fprintf(cmd.OutOrStdout(), "Processing %s\n", schemaPath)
				}

				// 1. Load schema
				s, err := schema.LoadFromFile(schemaPath)
				if err != nil {
					return fmt.Errorf("loading %s: %w", schemaPath, err)
				}

				// 2. Normalize
				s.Normalize()

				// 3. Create generator with config, including a file resolver
				//    rooted at the schema file's directory.
				absPath, _ := filepath.Abs(schemaPath)
				schemaDir := filepath.Dir(absPath)
				fileResolver := schema.NewFileResolver(schemaDir)

				// Build resolver chain. File resolver is always available;
				// HTTP resolver is opt-in via --allow-remote-refs.
				var resolver schema.SchemaResolver
				if allowRemoteRefs {
					httpResolver := schema.NewHTTPResolver()
					resolver = schema.NewCompositeResolver(fileResolver, httpResolver)
				} else {
					resolver = fileResolver
				}

				// Field-name overrides are keyed by the schema file's base name.
				fileKey := filepath.Base(schemaPath)
				processedFiles[fileKey] = true

				cfg := generator.Config{
					PackageName:      pkgName,
					OutputDir:        outputDir,
					OmitEmpty:        omitEmpty,
					StrictProperties: strictProperties,
					BigIntSupport:    bigInt,
					Resolver:         resolver,
					Draft:            draft,
					Validation:       validationMode,
					FieldNames:       fieldMap[fileKey],
				}
				gen := generator.New(cfg)

				// 4. Generate IR
				ir, err := gen.Generate(s)
				if err != nil {
					return fmt.Errorf("generating IR for %s: %w", schemaPath, err)
				}

				// Record applied overrides for unused-entry reporting.
				if applied := gen.AppliedOverrides(); len(applied) > 0 {
					if appliedByFile[fileKey] == nil {
						appliedByFile[fileKey] = make(map[string]map[string]bool)
					}
					for typeName, props := range applied {
						if appliedByFile[fileKey][typeName] == nil {
							appliedByFile[fileKey][typeName] = make(map[string]bool)
						}
						for prop := range props {
							appliedByFile[fileKey][typeName][prop] = true
						}
					}
				}

				// 5. Create emitter
				em, err := emitter.New()
				if err != nil {
					return fmt.Errorf("creating emitter: %w", err)
				}

				// 6. Emit Go code
				src, err := em.Emit(ir)
				if err != nil {
					return fmt.Errorf("emitting code for %s: %w", schemaPath, err)
				}

				// 7. Write output file
				outFile := deriveOutputFilename(schemaPath)
				outPath := filepath.Join(outputDir, outFile)

				if err := os.WriteFile(outPath, src, 0o644); err != nil {
					return fmt.Errorf("writing %s: %w", outPath, err)
				}

				if verbose {
					fmt.Fprintf(cmd.OutOrStdout(), "  -> %s\n", outPath)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", ".", "Output directory for generated files")
	cmd.Flags().StringVarP(&pkgName, "package", "p", "generated", "Go package name for generated code")
	cmd.Flags().BoolVar(&omitEmpty, "omit-empty", true, "Add omitempty to optional JSON fields")
	cmd.Flags().BoolVar(&strictProperties, "strict-properties", false, "Treat absent additionalProperties as false (no overflow map for extra JSON keys)")
	cmd.Flags().BoolVar(&bigInt, "big-int", false, "Generate *big.Int wrapper for integer types (supports arbitrary-precision integers)")
	cmd.Flags().BoolVar(&allowRemoteRefs, "allow-remote-refs", false, "Allow fetching remote $ref schemas over HTTP/HTTPS")
	cmd.Flags().StringVar(&draftStr, "draft", "", "Override JSON Schema draft version (auto-detected from $schema if omitted). Values: 3, 4, 6, 7, 2019-09, 2020-12")
	cmd.Flags().StringVar(&validationStr, "validation", string(generator.ValidationModeStatic), "Validation strategy: static, hybrid, or runtime")
	cmd.Flags().StringVar(&fieldMapPath, "field-map", "", "Path to a JSON file mapping schema properties to specific Go field names (keyed by schema file base name → Go type name → JSON property)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Print progress information")

	return cmd
}

// warnUnusedFieldMap emits warnings for field-map config that never took effect:
// top-level keys that don't name any generated schema file (often a typo or a
// missing nesting level), and individual overrides that matched no property. All
// warnings are sorted for deterministic output.
func warnUnusedFieldMap(w io.Writer, fieldMap generator.FieldMapFile, applied map[string]map[string]map[string]bool, processedFiles map[string]bool) {
	var warnings []string
	for file, types := range fieldMap {
		if !processedFiles[file] {
			// The whole section is dead: warn once for the file rather than
			// emitting a confusing "matched no property" line per entry.
			warnings = append(warnings, fmt.Sprintf(
				"field-map key %q does not match any generated schema file (expected a schema file base name)", file))
			continue
		}
		for typeName, props := range types {
			for prop := range props {
				if !applied[file][typeName][prop] {
					warnings = append(warnings, fmt.Sprintf(
						"field-map entry %q matched no property", fmt.Sprintf("%s/%s.%s", file, typeName, prop)))
				}
			}
		}
	}
	sort.Strings(warnings)
	for _, msg := range warnings {
		fmt.Fprintf(w, "warning: %s\n", msg)
	}
}

// checkOutputCollisions reports an error if two distinct input schema paths
// would derive the same output file name. deriveOutputFilename uses only the
// base name, so a/user.json and b/user.json both write to user.go; the second
// would silently clobber the first. A schema listed twice is not a collision.
func checkOutputCollisions(args []string) error {
	seen := make(map[string]string, len(args))
	for _, schemaPath := range args {
		out := deriveOutputFilename(schemaPath)
		if prev, ok := seen[out]; ok && prev != schemaPath {
			return fmt.Errorf("input schemas %q and %q both map to output file %q; rename one or generate them into separate directories", prev, schemaPath, out)
		}
		seen[out] = schemaPath
	}
	return nil
}

// parseDraft converts a user-supplied draft version string to a schema.Draft value.
func parseDraft(s string) (schema.Draft, error) {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "3", "03", "draft-03", "draft03":
		return schema.Draft03, nil
	case "4", "04", "draft-04", "draft04":
		return schema.Draft04, nil
	case "6", "06", "draft-06", "draft06":
		return schema.Draft06, nil
	case "7", "07", "draft-07", "draft07":
		return schema.Draft07, nil
	case "2019-09", "draft-2019-09", "2019":
		return schema.Draft201909, nil
	case "2020-12", "draft-2020-12", "2020":
		return schema.Draft202012, nil
	default:
		return schema.DraftUnknown, fmt.Errorf("unknown draft version %q: valid values are 3, 4, 6, 7, 2019-09, 2020-12", s)
	}
}

func parseValidationMode(s string) (generator.ValidationMode, error) {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "", "static":
		return generator.ValidationModeStatic, nil
	case "hybrid":
		return generator.ValidationModeHybrid, nil
	case "runtime":
		return generator.ValidationModeRuntime, nil
	default:
		return generator.ValidationModeStatic, fmt.Errorf("unknown validation mode %q: valid values are static, hybrid, runtime", s)
	}
}

// deriveOutputFilename converts a schema filename to a Go source filename.
// e.g. "person.json" -> "person.go", "my-schema.json" -> "my_schema.go"
// (the extension is dropped regardless of its value; only JSON input is supported).
func deriveOutputFilename(schemaPath string) string {
	base := filepath.Base(schemaPath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	// Replace hyphens with underscores for valid Go filenames.
	name = strings.ReplaceAll(name, "-", "_")
	return name + ".go"
}
