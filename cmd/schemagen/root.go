package schemagen

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// helperFileName is where shared helper functions are written. One file per
// destination package: the helpers are package-level, so emitting them per
// schema breaks any package containing two schemas that need the same one.
const helperFileName = "schemagen_helpers.go"

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
		formatAssertion  bool
		formatAnnotation bool
		verbose          bool
		allowRemoteRefs  bool
		draftStr         string
		validationStr    string
		fieldMapPath     string
		lenientRefs      bool
		rootNameFlags    []string
		rootNameFromFile bool
		sharedTypes      bool
		schemaPkgFlags   []string
		schemaOutFlags   []string
		configPath       string
	)

	cmd := &cobra.Command{
		Use:   "generate [schema files...]",
		Short: "Generate Go source files from JSON Schema definitions",
		// Inputs may come from --config instead of the command line, so the
		// count is validated in RunE rather than here.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && configPath == "" {
				return fmt.Errorf("no input schemas: pass them as arguments, or supply them via --config")
			}

			// A config supplies settings for documents and defaults for the
			// run. Anything set explicitly on the command line wins, which
			// Flags().Changed distinguishes from "left at its default".
			var cfg *ConfigFile
			if configPath != "" {
				var err error
				cfg, err = LoadConfigFile(configPath)
				if err != nil {
					return err
				}
				applyString(cmd, "output-dir", cfg.OutputDir, &outputDir)
				applyString(cmd, "package", cfg.Package, &pkgName)
				applyString(cmd, "draft", cfg.Draft, &draftStr)
				applyString(cmd, "validation", cfg.Validation, &validationStr)
				applyBool(cmd, "omit-empty", cfg.OmitEmpty, &omitEmpty)
				applyBool(cmd, "strict-properties", cfg.StrictProperties, &strictProperties)
				applyBool(cmd, "big-int", cfg.BigInt, &bigInt)
				applyBool(cmd, "format-assertion", cfg.FormatAssertion, &formatAssertion)
				applyBool(cmd, "format-annotation", cfg.FormatAnnotation, &formatAnnotation)
				applyBool(cmd, "allow-remote-refs", cfg.AllowRemoteRefs, &allowRemoteRefs)
				applyBool(cmd, "lenient-refs", cfg.LenientRefs, &lenientRefs)
				applyBool(cmd, "shared-types", cfg.SharedTypes, &sharedTypes)
				applyBool(cmd, "root-name-from-filename", cfg.RootNameFromFilename, &rootNameFromFile)

				// With no inputs given, the config's documents are the inputs.
				if len(args) == 0 {
					args = cfg.inputPaths()
					if len(args) == 0 {
						return fmt.Errorf("no input schemas: pass them as arguments, or give config %s documents with a \"path\"", configPath)
					}
				}
			}

			// The two format flags name opposite postures, so asking for both
			// says nothing. Refusing is better than picking one silently: the
			// generator's tie-break is documented but nobody reading the command
			// line would know which half of it was ignored.
			if formatAssertion && formatAnnotation {
				return fmt.Errorf("--format-assertion and --format-annotation are opposites: pass one, or neither to let the dialect decide")
			}

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

			rootNames, err := parseRootNameFlags(rootNameFlags)
			if err != nil {
				return err
			}
			rootNames.seedFromConfig(cfg)
			if err := rootNames.validate(len(args)); err != nil {
				return err
			}
			defer rootNames.warnUnused(cmd.ErrOrStderr())
			if (sharedTypes || len(schemaPkgFlags) > 0) && validationMode != generator.ValidationModeStatic {
				return fmt.Errorf("--shared-types and --schema-package currently require --validation static (per-file validation capability reporting would collide)")
			}

			// --schema-package activates multi-package generation. Config
			// entries seed the maps; flags overwrite per $id.
			schemaPackages := make(map[string]string)
			schemaOutputsFromConfig := map[string]string{}
			if cfg != nil {
				for id, pkg := range cfg.schemaPackages() {
					schemaPackages[id] = pkg
				}
				schemaOutputsFromConfig = cfg.schemaOutputs()
			}
			for _, sp := range schemaPkgFlags {
				id, pkg, ok := strings.Cut(sp, "=")
				if !ok || id == "" || pkg == "" {
					return fmt.Errorf("--schema-package %q: expected <document $id>=<Go import path>", sp)
				}
				schemaPackages[strings.TrimSuffix(id, "#")] = pkg
			}
			schemaOutputs := make(map[string]string)
			for id, out := range schemaOutputsFromConfig {
				schemaOutputs[id] = out
			}
			for _, so := range schemaOutFlags {
				id, outPath, ok := strings.Cut(so, "=")
				if !ok || id == "" || outPath == "" {
					return fmt.Errorf("--schema-output %q: expected <document $id>=<output file path>", so)
				}
				schemaOutputs[strings.TrimSuffix(id, "#")] = outPath
			}
			if len(schemaOutputs) > 0 && len(schemaPackages) == 0 {
				return fmt.Errorf("--schema-output requires --schema-package mappings")
			}

			// Load optional field-name overrides. Keyed by schema-file base name.
			var fieldMap generator.FieldMapFile
			if fieldMapPath != "" {
				fieldMap, err = generator.LoadFieldMapFile(fieldMapPath)
				if err != nil {
					return err
				}
			}
			fieldNames := newFieldNameSpec(cfg, fieldMap)

			// Track which (file, type, property) overrides were applied, and which
			// schema files were actually generated, so we can warn about entries
			// that never matched. Reported via defer so warnings still surface even
			// if generation fails partway through.
			appliedByFile := make(map[string]map[string]map[string]bool)
			processedFiles := make(map[string]bool)
			defer warnUnusedFieldMap(cmd.ErrOrStderr(), fieldMap, appliedByFile, processedFiles)

			// Reject input sets where two schemas map to the same output file
			// (same base name in different directories). Without this the second
			// silently overwrites the first. Multi-package runs place files in
			// per-package directories and check the resolved paths instead.
			if len(schemaPackages) == 0 {
				if err := checkOutputCollisions(args); err != nil {
					return err
				}
			}

			// Ensure output directory exists.
			if err := os.MkdirAll(outputDir, 0o755); err != nil {
				return fmt.Errorf("creating output directory: %w", err)
			}

			if len(schemaPackages) > 0 && cmd.Flags().Changed("package") {
				return fmt.Errorf("--package cannot be combined with --schema-package: each package is named after the last segment of its --schema-package import path")
			}

			if len(schemaPackages) > 0 {
				return runMultiPackage(cmd.OutOrStdout(), args, multiPackageParams{
					schemaPackages:   schemaPackages,
					schemaOutputs:    schemaOutputs,
					rootNames:        rootNames,
					rootNameFromFile: rootNameFromFile,
					outputDir:        outputDir,
					omitEmpty:        omitEmpty,
					strictProperties: strictProperties,
					bigInt:           bigInt,
					formatAssertion:  formatAssertion,
					formatAnnotation: formatAnnotation,
					allowRemoteRefs:  allowRemoteRefs,
					verbose:          verbose,
					draft:            draft,
					validationMode:   validationMode,
					fieldNames:       fieldNames,
					lenientRefs:      lenientRefs,
					processedFiles:   processedFiles,
					appliedByFile:    appliedByFile,
					warnings:         cmd.ErrOrStderr(),
				})
			}

			// One emitter for the whole run: constructing it parses the full
			// template set, which does not depend on the input schema. Emit is
			// read-only with respect to the emitter, so it is reused per file.
			em, err := emitter.New()
			if err != nil {
				return fmt.Errorf("creating emitter: %w", err)
			}

			// Shared helpers are package-level functions, so they are written
			// once for the whole destination package rather than into each file
			// that happens to need them.
			var helpers generator.HelperSet

			// In shared-types mode all schemas run through one generator so
			// types materialized by an earlier schema are referenced, not
			// re-emitted. The inputs are pre-loaded and indexed by $id so a
			// cross-input $ref resolves to the same loaded instance as the
			// input itself (instance identity is what the generated-types
			// registry keys on).
			var sharedGen *generator.Generator
			inputByPath := make(map[string]*schema.Schema)
			if sharedTypes {
				inputByID := make(map[string]*schema.Schema)
				for _, schemaPath := range args {
					s, err := schema.LoadFromFile(schemaPath)
					if err != nil {
						return fmt.Errorf("loading %s: %w", schemaPath, err)
					}
					s.Normalize()
					s.ComputeBaseURIs(nil, s)
					inputByPath[schemaPath] = s
					id := strings.TrimSuffix(s.ID, "#")
					if id == "" {
						id = strings.TrimSuffix(s.LegacyID, "#")
					}
					if id != "" {
						if _, ok := inputByID[id]; ok {
							return fmt.Errorf("duplicate $id %q across input schemas", id)
						}
						inputByID[id] = s
					}
				}
				absPath, _ := filepath.Abs(args[0])
				resolvers := []schema.SchemaResolver{
					schema.NewMappingResolver(inputByID),
					schema.NewFileResolver(filepath.Dir(absPath)),
				}
				if allowRemoteRefs {
					resolvers = append(resolvers, schema.NewHTTPResolver())
				}
				sharedGen = generator.New(generator.Config{
					PackageName:      pkgName,
					OutputDir:        outputDir,
					OmitEmpty:        omitEmpty,
					StrictProperties: strictProperties,
					BigIntSupport:    bigInt,
					FormatAssertion:  formatAssertion,
					FormatAnnotation: formatAnnotation,
					Resolver:         schema.NewCompositeResolver(resolvers...),
					Draft:            draft,
					Validation:       validationMode,
					LenientRefs:      lenientRefs,
					SharedTypes:      true,
				})
			}

			for _, schemaPath := range args {
				if verbose {
					fmt.Fprintf(cmd.OutOrStdout(), "Processing %s\n", schemaPath)
				}

				// Field-name overrides are keyed by the schema file's base name.
				fileKey := filepath.Base(schemaPath)
				processedFiles[fileKey] = true

				var gen *generator.Generator
				var s *schema.Schema
				if sharedTypes {
					gen = sharedGen
					s = inputByPath[schemaPath]
				} else {
					// 1. Load schema
					var err error
					s, err = schema.LoadFromFile(schemaPath)
					if err != nil {
						return fmt.Errorf("loading %s: %w", schemaPath, err)
					}

					// 2. Normalize
					s.Normalize()

					// 3. Create generator with config, including a file resolver
					//    rooted at the schema file's directory.
					absPath, _ := filepath.Abs(schemaPath)
					fileResolver := schema.NewFileResolver(filepath.Dir(absPath))

					// Build resolver chain. File resolver is always available;
					// HTTP resolver is opt-in via --allow-remote-refs.
					var resolver schema.SchemaResolver
					if allowRemoteRefs {
						resolver = schema.NewCompositeResolver(fileResolver, schema.NewHTTPResolver())
					} else {
						resolver = fileResolver
					}

					gen = generator.New(generator.Config{
						PackageName:      pkgName,
						OutputDir:        outputDir,
						OmitEmpty:        omitEmpty,
						StrictProperties: strictProperties,
						BigIntSupport:    bigInt,
						FormatAssertion:  formatAssertion,
						FormatAnnotation: formatAnnotation,
						Resolver:         resolver,
						Draft:            draft,
						Validation:       validationMode,
						LenientRefs:      lenientRefs,
					})
				}

				// The $id is only known once the schema is loaded, and it is a
				// --root-name key, so the name is resolved here rather than
				// before the branch above.
				rootTypeName := rootNames.lookup(schemaPath, docIDOf(s))
				if rootTypeName == "" && rootNameFromFile {
					// e.g. "http_api_url.json" → "HTTPAPIURLJSON"; the extension
					// is kept as a naming word so person.json and person.yaml
					// derive distinct type names.
					rootTypeName = generator.SchemaNameToGoName(fileKey)
				}

				// 4. Generate IR
				ir, err := gen.Generate(s,
					generator.WithRootTypeName(rootTypeName),
					generator.WithFieldNames(fieldNames.lookup(schemaPath, docIDOf(s))),
				)
				if err != nil {
					var unresolved *generator.UnresolvedRefsError
					if errors.As(err, &unresolved) {
						return fmt.Errorf("generating IR for %s: %w\n(place the referenced documents alongside the schema, enable --allow-remote-refs for http(s) refs, or pass --lenient-refs to degrade unresolved refs to any)", schemaPath, err)
					}
					return fmt.Errorf("generating IR for %s: %w", schemaPath, err)
				}

				warnUnenforcedSchemas(cmd.ErrOrStderr(), schemaPath, gen.UnenforcedSchemas())

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

				// 5. Emit Go code (emitter created once, above the loop)
				src, err := em.Emit(ir)
				if err != nil {
					return fmt.Errorf("emitting code for %s: %w", schemaPath, err)
				}

				// Which shared helpers this file needs is read from what it
				// actually calls. Asking the IR meant naming every field a
				// helper-backed rule can live in, and a field that was missed
				// emitted the call without the declaration -- generated code
				// that did not compile. See HelpersReferencedBy.
				helpers.Merge(generator.HelpersReferencedBy(string(src)))

				// 6. Write output file
				outFile := deriveOutputFilename(schemaPath)
				outPath := filepath.Join(outputDir, outFile)

				if err := os.WriteFile(outPath, src, 0o644); err != nil {
					return fmt.Errorf("writing %s: %w", outPath, err)
				}

				if verbose {
					fmt.Fprintf(cmd.OutOrStdout(), "  -> %s\n", outPath)
				}
			}

			// 7. Write the shared helper file, if anything referenced one.
			helperSrc, needed, err := em.EmitHelpers(pkgName, helpers)
			if err != nil {
				return err
			}
			if needed {
				helperPath := filepath.Join(outputDir, helperFileName)
				if err := os.WriteFile(helperPath, helperSrc, 0o644); err != nil {
					return fmt.Errorf("writing %s: %w", helperPath, err)
				}
				if verbose {
					fmt.Fprintf(cmd.OutOrStdout(), "  -> %s\n", helperPath)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", ".", "Output directory for generated files")
	cmd.Flags().StringVarP(&pkgName, "package", "p", "generated", "Go package name for generated code")
	cmd.Flags().BoolVar(&omitEmpty, "omit-empty", true, "Add omitempty to optional JSON fields")
	cmd.Flags().BoolVar(&strictProperties, "strict-properties", false, "Treat absent additionalProperties as false for validation (extra JSON keys are still captured for round-trip but rejected by Validate)")
	cmd.Flags().BoolVar(&bigInt, "big-int", false, "Generate *big.Int wrapper for integer types (supports arbitrary-precision integers)")
	cmd.Flags().BoolVar(&formatAssertion, "format-assertion", false, "Assert \"format\" on every draft. Without it the dialect decides: draft 3-7 and v1 assert, 2019-09 and 2020-12 treat format as an annotation (the format-annotation vocabulary), and a document with no $schema follows 2020-12. Assertion also restores the Go type mapping, so date-time is time.Time and ipv4/ipv6 netip.Addr")
	cmd.Flags().BoolVar(&formatAnnotation, "format-annotation", false, "Treat \"format\" as an annotation on every draft, including the ones whose dialect asserts (draft 3-7 and v1). The opposite of --format-assertion, and mutually exclusive with it")
	cmd.Flags().BoolVar(&allowRemoteRefs, "allow-remote-refs", false, "Allow fetching remote $ref schemas over HTTP/HTTPS")
	cmd.Flags().BoolVar(&lenientRefs, "lenient-refs", false, "Degrade $refs that no resolver can serve to any instead of failing generation")
	cmd.Flags().StringVar(&draftStr, "draft", "", "Override JSON Schema draft version (auto-detected from $schema if omitted). Values: 3, 4, 6, 7, 2019-09, 2020-12, v1")
	cmd.Flags().StringVar(&validationStr, "validation", string(generator.ValidationModeStatic), "Validation strategy: static, hybrid, or runtime")
	cmd.Flags().StringVar(&fieldMapPath, "field-map", "", "Path to a JSON file mapping schema properties to specific Go field names (keyed by schema file base name → Go type name → JSON property)")
	cmd.Flags().StringArrayVar(&rootNameFlags, "root-name", nil, "Exact Go type name for a root schema, used verbatim. A bare name (single input only), or a repeatable \"<key>=<Name>\" pair where <key> is the schema file base name, \"id:<document $id>\" or \"file:<schema path>\" — most specific wins (default: schema title, or Root)")
	cmd.Flags().BoolVar(&rootNameFromFile, "root-name-from-filename", false, "Derive each root type name from the schema filename (Go initialism rules apply), e.g. person.json → PersonJSON, http_api_url.json → HTTPAPIURLJSON")
	cmd.Flags().BoolVar(&sharedTypes, "shared-types", false, "Generate all schemas into one Go package sharing materialized types and helpers (each schema needs a distinct root type name; requires --validation static)")
	cmd.Flags().StringArrayVar(&schemaPkgFlags, "schema-package", nil, "Assign a schema document to a Go package: \"<document $id>=<Go import path>\". Repeatable; activates multi-package generation where cross-package $refs emit imports instead of copies. Generation order is derived from the $refs between documents")
	cmd.Flags().StringArrayVar(&schemaOutFlags, "schema-output", nil, "Output file for a schema document: \"<document $id>=<file path>\" (default: <output-dir>/<package>/<derived name>.go). Requires --schema-package")
	cmd.Flags().StringVar(&configPath, "config", "", "Path to a JSON generation config (per-document package/output/root name/field names, plus defaults). Flags override it; see README")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Print progress information")

	return cmd
}

// warnUnenforcedSchemas reports every type that came out as `type X any` while
// its schema still said something.
//
// This is the one dropped check the generated code cannot show you. A missing
// constraint elsewhere still leaves a Validate method that can be read and a
// decode that can fail; `type X any` has neither, so a schema schemagen could
// not compile is indistinguishable from a schema that asked for nothing. The
// generated source carries the same statement as a comment above the
// declaration; this puts it in front of whoever ran the command.
func warnUnenforcedSchemas(w io.Writer, schemaPath string, unenforced []generator.UnenforcedSchema) {
	if w == nil {
		return
	}
	for _, u := range unenforced {
		fmt.Fprintf(w, "warning: %s: type %s is `any` and validates nothing, but the schema states %s\n",
			schemaPath, u.TypeName, strings.Join(u.Keywords, ", "))
	}
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
	// v1 is the undated stable release that succeeds the dated drafts. It has no
	// "draft-" spelling on purpose.
	case "v1", "1":
		return schema.DraftV1, nil
	default:
		return schema.DraftUnknown, fmt.Errorf("unknown draft version %q: valid values are 3, 4, 6, 7, 2019-09, 2020-12, v1", s)
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

// multiPackageParams carries the settings for a multi-package generation run.
type multiPackageParams struct {
	schemaPackages   map[string]string // document $id → Go import path
	schemaOutputs    map[string]string // document $id → output file path
	rootNames        *rootNameSpec     // resolves each input's root type name
	rootNameFromFile bool
	outputDir        string
	omitEmpty        bool
	strictProperties bool
	bigInt           bool
	formatAssertion  bool
	formatAnnotation bool
	allowRemoteRefs  bool
	verbose          bool
	draft            schema.Draft
	validationMode   generator.ValidationMode
	fieldNames       *fieldNameSpec
	lenientRefs      bool
	// Shared with the caller so --field-map entries that matched nothing can be
	// reported: without these, every top-level key looks unused in a
	// multi-package run.
	processedFiles map[string]bool
	appliedByFile  map[string]map[string]map[string]bool
	// Where "warning:" lines go. Kept apart from the out writer runMultiPackage
	// takes for progress: a warning belongs on stderr whether or not anyone
	// asked for progress.
	warnings io.Writer
}

// runMultiPackage generates several Go packages in one run. Every input schema
// is pre-loaded once and indexed by $id, so cross-package $refs resolve to the
// instances being generated and emit qualified references with imports instead
// of materialized copies. Packages are generated in an order derived from the
// $refs between documents, and a $ref into a document owned by another package
// of the run that was not generated there is an error rather than a silently
// duplicated type.
func runMultiPackage(out io.Writer, args []string, p multiPackageParams) error {
	type input struct {
		path string
		s    *schema.Schema
		id   string
		pkg  string
	}

	inputs := make([]*input, 0, len(args))
	byID := make(map[string]*schema.Schema)
	for _, schemaPath := range args {
		s, err := schema.LoadFromFile(schemaPath)
		if err != nil {
			return fmt.Errorf("loading %s: %w", schemaPath, err)
		}
		s.Normalize()
		s.ComputeBaseURIs(nil, s)
		id := strings.TrimSuffix(s.ID, "#")
		if id == "" {
			id = strings.TrimSuffix(s.LegacyID, "#")
		}
		if id == "" {
			return fmt.Errorf("multi-package generation requires every schema to declare $id; %s has none", schemaPath)
		}
		pkg := p.schemaPackages[id]
		if pkg == "" {
			return fmt.Errorf("no --schema-package mapping for %s ($id %q)", schemaPath, id)
		}
		if _, ok := byID[id]; ok {
			return fmt.Errorf("duplicate $id %q across input schemas", id)
		}
		byID[id] = s
		inputs = append(inputs, &input{path: schemaPath, s: s, id: id, pkg: pkg})
	}

	// Resolve $refs through the instances this run already loaded, and root a
	// file resolver at every input's directory so a sibling-relative ref inside
	// any input resolves, not just inside the first one.
	resolvers := []schema.SchemaResolver{schema.NewMappingResolver(byID)}
	for _, dir := range inputDirs(args) {
		resolvers = append(resolvers, schema.NewFileResolver(dir))
	}
	if p.allowRemoteRefs {
		resolvers = append(resolvers, schema.NewHTTPResolver())
	}
	// Whatever spelling a ref uses, a document this run owns must come back as
	// the instance already loaded for it: the cross-package registry keys types
	// by node identity, so a second copy would go unrecognized and be
	// duplicated locally.
	resolver := newCanonicalInstanceResolver(schema.NewCompositeResolver(resolvers...), byID)
	// A $ref may target a subschema carrying its own $id, which is a resource
	// root in its own right and cannot be traced back to the file containing
	// it. Register every resource $id inside each input against that input's
	// package so such refs are still recognized as foreign.
	registry := generator.NewCrossPackageRegistry(p.schemaPackages)
	for _, in := range inputs {
		registry.RegisterDocument(in.s, in.pkg)
	}

	// Group inputs by package, keeping command-line order within each package.
	var pkgOrder []string
	pkgInputs := make(map[string][]*input)
	for _, in := range inputs {
		if _, ok := pkgInputs[in.pkg]; !ok {
			pkgOrder = append(pkgOrder, in.pkg)
		}
		pkgInputs[in.pkg] = append(pkgInputs[in.pkg], in)
	}

	// Order packages so each is generated after the packages it $refs into: a
	// ref into a package that has not been generated yet would silently emit a
	// local copy of the type instead of importing it. Deriving the order also
	// means callers no longer have to hand-maintain a dependency-first command
	// line.
	docs := make([]packageDoc, 0, len(inputs))
	for _, in := range inputs {
		docs = append(docs, packageDoc{id: in.id, pkg: in.pkg, path: in.path, schema: in.s})
	}
	orderedPkgs, err := orderPackagesByDependencies(pkgOrder, docs, p.schemaPackages)
	if err != nil {
		return err
	}
	if p.verbose && !slices.Equal(orderedPkgs, pkgOrder) {
		fmt.Fprintf(out, "Reordered packages by $ref dependencies: %s\n", strings.Join(orderedPkgs, ", "))
	}
	pkgOrder = orderedPkgs

	// Reject resolved output-path collisions before generating anything, and
	// require each output directory to hold exactly one package: files in one
	// directory share a package clause, so two import paths writing there
	// produce a directory Go cannot compile. Default paths use the import
	// path's last segment, so distinct import paths ending in the same segment
	// land in the same directory — comparing whole file paths does not catch it.
	outPaths := make(map[string]string)
	dirPackages := make(map[string]string)
	for _, in := range inputs {
		outPath := p.schemaOutputs[in.id]
		if outPath == "" {
			outPath = filepath.Join(p.outputDir, generator.PackageNameForImportPath(in.pkg), deriveOutputFilename(in.path))
		}
		if other, ok := outPaths[outPath]; ok {
			return fmt.Errorf("input schemas %q and %q both write to %q; set distinct --schema-output paths", other, in.path, outPath)
		}
		outPaths[outPath] = in.path

		dir := filepath.Dir(outPath)
		if other, ok := dirPackages[dir]; ok && other != in.pkg {
			return fmt.Errorf("packages %q and %q both write into %q; a directory holds one Go package, so give them distinct --schema-output directories", other, in.pkg, dir)
		}
		dirPackages[dir] = in.pkg
	}

	em, err := emitter.New()
	if err != nil {
		return fmt.Errorf("creating emitter: %w", err)
	}

	for _, pkg := range pkgOrder {
		gen := generator.New(generator.Config{
			PackageName:      generator.PackageNameForImportPath(pkg),
			OutputDir:        p.outputDir,
			OmitEmpty:        p.omitEmpty,
			StrictProperties: p.strictProperties,
			BigIntSupport:    p.bigInt,
			FormatAssertion:  p.formatAssertion,
			FormatAnnotation: p.formatAnnotation,
			Resolver:         resolver,
			Draft:            p.draft,
			Validation:       p.validationMode,
			LenientRefs:      p.lenientRefs,
			SharedTypes:      true,
			ImportPath:       pkg,
			CrossPackage:     registry,
		})

		// Helpers are package-level functions, so each generated package gets
		// its own helper file. Every input of a package writes into the same
		// directory (enforced above), so one file per package is enough.
		var helpers generator.HelperSet
		var pkgDir string
		for _, in := range pkgInputs[pkg] {
			if p.verbose {
				fmt.Fprintf(out, "Processing %s -> %s\n", in.path, pkg)
			}
			fileKey := filepath.Base(in.path)
			if p.processedFiles != nil {
				p.processedFiles[fileKey] = true
			}

			rootTypeName := p.rootNames.lookup(in.path, in.id)
			if rootTypeName == "" && p.rootNameFromFile {
				rootTypeName = generator.SchemaNameToGoName(fileKey)
			}

			ir, err := gen.Generate(in.s,
				generator.WithRootTypeName(rootTypeName),
				generator.WithFieldNames(p.fieldNames.lookup(in.path, in.id)),
			)
			if err != nil {
				var unresolved *generator.UnresolvedRefsError
				if errors.As(err, &unresolved) {
					return fmt.Errorf("generating IR for %s: %w\n(provide the referenced documents as inputs, enable --allow-remote-refs, or pass --lenient-refs to degrade unresolved refs to any)", in.path, err)
				}
				return fmt.Errorf("generating IR for %s: %w", in.path, err)
			}

			warnUnenforcedSchemas(p.warnings, in.path, gen.UnenforcedSchemas())

			if applied := gen.AppliedOverrides(); len(applied) > 0 && p.appliedByFile != nil {
				if p.appliedByFile[fileKey] == nil {
					p.appliedByFile[fileKey] = make(map[string]map[string]bool)
				}
				for typeName, props := range applied {
					if p.appliedByFile[fileKey][typeName] == nil {
						p.appliedByFile[fileKey][typeName] = make(map[string]bool)
					}
					for prop := range props {
						p.appliedByFile[fileKey][typeName][prop] = true
					}
				}
			}

			src, err := em.Emit(ir)
			if err != nil {
				return fmt.Errorf("emitting code for %s: %w", in.path, err)
			}
			helpers.Merge(generator.HelpersReferencedBy(string(src)))

			outPath := p.schemaOutputs[in.id]
			if outPath == "" {
				// Default layout: one sub-directory per package.
				outPath = filepath.Join(p.outputDir, generator.PackageNameForImportPath(pkg), deriveOutputFilename(in.path))
			}
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return fmt.Errorf("creating output directory for %s: %w", outPath, err)
			}
			if err := os.WriteFile(outPath, src, 0o644); err != nil {
				return fmt.Errorf("writing %s: %w", outPath, err)
			}
			pkgDir = filepath.Dir(outPath)
			if p.verbose {
				fmt.Fprintf(out, "  -> %s\n", outPath)
			}
		}

		// One helper file per generated package.
		helperSrc, needed, err := em.EmitHelpers(generator.PackageNameForImportPath(pkg), helpers)
		if err != nil {
			return err
		}
		if needed {
			helperPath := filepath.Join(pkgDir, helperFileName)
			if err := os.WriteFile(helperPath, helperSrc, 0o644); err != nil {
				return fmt.Errorf("writing %s: %w", helperPath, err)
			}
			if p.verbose {
				fmt.Fprintf(out, "  -> %s\n", helperPath)
			}
		}
	}
	return nil
}
