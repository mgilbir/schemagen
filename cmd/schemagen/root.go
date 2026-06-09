package schemagen

import (
	"fmt"
	"os"
	"path/filepath"
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

				cfg := generator.Config{
					PackageName:      pkgName,
					OutputDir:        outputDir,
					OmitEmpty:        omitEmpty,
					StrictProperties: strictProperties,
					BigIntSupport:    bigInt,
					Resolver:         resolver,
					Draft:            draft,
					Validation:       validationMode,
				}
				gen := generator.New(cfg)

				// 4. Generate IR
				ir, err := gen.Generate(s)
				if err != nil {
					return fmt.Errorf("generating IR for %s: %w", schemaPath, err)
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
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Print progress information")

	return cmd
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
// e.g. "person.json" -> "person.go", "my-schema.yaml" -> "my_schema.go"
func deriveOutputFilename(schemaPath string) string {
	base := filepath.Base(schemaPath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	// Replace hyphens with underscores for valid Go filenames.
	name = strings.ReplaceAll(name, "-", "_")
	return name + ".go"
}
