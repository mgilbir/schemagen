package generator

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// Generator converts a parsed Schema into IR types.
type Generator struct {
	config                     Config
	output                     *File
	generated                  map[string]bool // track already-generated type names
	generating                 map[string]bool // track types currently being generated (recursion guard)
	defs                       map[string]*schema.Schema
	rootTypeName               string                // Go type name for the root schema
	rootID                     string                // $id of the root schema (for detecting self-references)
	anchors                    map[string]string     // anchor/id → def ref path (e.g., "#something" → "#/definitions/bar")
	dynamicAnchors             map[string]string     // $dynamicAnchor name → def ref path (e.g., "#items" → "#/$defs/items")
	resolver                   schema.SchemaResolver // external resolver for non-local refs
	baseURI                    *url.URL              // base URI for the root document (from $id or file path)
	rootSchema                 *schema.Schema        // the root schema for local ref resolution
	draft                      schema.Draft          // detected draft version of the root schema
	resourceGraph              *schema.ResourceGraph // document/dialect/anchor graph for validation planning
	validationKeywordsDisabled bool                  // true when the declared metaschema omits the validation vocabulary

	// documentRoots maps canonical $id URIs to the schema nodes that declare them.
	// This enables scoped resolution: when a subschema has $id, $ref: "#/..."
	// within it resolves against that subschema, not the top-level root.
	documentRoots map[string]*schema.Schema

	// dynamicScope tracks the stack of document roots entered via $ref during
	// code generation. This enables $dynamicRef to resolve against the dynamic
	// scope chain (walking from outermost to innermost) rather than only the
	// local document scope. The root schema's document root is always at index 0.
	dynamicScope []*schema.Schema

	// structsInProgress tracks Go type names for structs currently having their
	// fields resolved (on the call stack). Used to detect recursive value-type
	// cycles: if a resolved ref points to a type that references a type in this
	// set, a pointer must be used to break the cycle.
	structsInProgress map[string]bool
}

// New creates a new Generator with the given configuration.
func New(cfg Config) *Generator {
	return &Generator{
		config:            cfg,
		generated:         make(map[string]bool),
		generating:        make(map[string]bool),
		structsInProgress: make(map[string]bool),
	}
}

// Generate processes a schema and returns the IR File.
func (g *Generator) Generate(s *schema.Schema) (*File, error) {
	g.output = &File{
		PackageName: g.config.PackageName,
	}
	g.generated = make(map[string]bool)
	g.generating = make(map[string]bool)
	g.rootSchema = s
	if g.config.Draft != schema.DraftUnknown {
		g.draft = g.config.Draft
	} else {
		g.draft = schema.DetectDraft(s)
	}

	// Determine root type name.
	g.rootTypeName = "Root"
	if s.Title != "" {
		g.rootTypeName = SchemaNameToGoName(s.Title)
	}

	// Store root schema's $id for detecting self-references.
	g.rootID = s.ID
	if g.rootID == "" {
		g.rootID = s.LegacyID
	}

	// Compute base URI from root $id (used for resolving relative refs).
	if g.rootID != "" {
		if u, err := url.Parse(g.rootID); err == nil {
			g.baseURI = u
		}
	}

	// Compute effective base URIs, document roots, and schema resources. This
	// enables scoped $id resolution and gives validation planning a dialect-aware
	// view of the schema graph.
	g.resourceGraph = schema.BuildResourceGraph(s, g.baseURI, g.draft)
	g.documentRoots = make(map[string]*schema.Schema)
	g.buildDocumentRoots(s)

	// Initialize dynamic scope with the root document root.
	g.dynamicScope = []*schema.Schema{s}

	// Store the external resolver from config (may be nil).
	g.resolver = g.config.Resolver
	g.validationKeywordsDisabled = !g.hasValidationVocabulary(s)

	// Collect definitions ($defs and definitions) and build anchor index.
	// Iterate in sorted key order for deterministic anchor registration
	// (important when multiple defs declare the same $anchor in different scopes).
	g.defs = make(map[string]*schema.Schema)
	g.anchors = make(map[string]string)
	g.dynamicAnchors = make(map[string]string)
	for _, name := range sortedKeys(s.Defs) {
		def := s.Defs[name]
		refPath := "#/$defs/" + name
		g.defs[refPath] = def
		g.indexAnchors(def, refPath)
	}
	for _, name := range sortedKeys(s.Definitions) {
		def := s.Definitions[name]
		refPath := "#/definitions/" + name
		g.defs[refPath] = def
		g.indexAnchors(def, refPath)
	}

	// Process definitions first — generate TypeDefs for each.
	defNames := sortedKeys(s.Defs)
	for _, name := range defNames {
		def := s.Defs[name]
		goName := SchemaNameToGoName(name)
		if err := g.generateTypeDef(goName, def); err != nil {
			return nil, fmt.Errorf("generating $defs/%s: %w", name, err)
		}
	}

	defNames = sortedKeys(s.Definitions)
	for _, name := range defNames {
		def := s.Definitions[name]
		goName := SchemaNameToGoName(name)
		if err := g.generateTypeDef(goName, def); err != nil {
			return nil, fmt.Errorf("generating definitions/%s: %w", name, err)
		}
	}

	// Boolean false schema at root level → nothing is valid. Generate a
	// NotSchemaDef that rejects everything (same as {"not": {}}).
	// This is only applied to the root schema; definitions with boolean
	// false schemas are left as-is to avoid type conflicts when referenced.
	if s.IsFalseSchema() {
		g.generated[g.rootTypeName] = true
		g.output.TypeDefs = append(g.output.TypeDefs, &NotSchemaDef{
			Name:        g.rootTypeName,
			Description: s.Description,
			IsForbidden: true,
		})
	} else {
		// Process the root type. This handles objects, compositions, primitive types
		// with validation constraints, enums, arrays, and any other schema that can
		// produce a Go type definition.
		if err := g.generateTypeDef(g.rootTypeName, s); err != nil {
			return nil, fmt.Errorf("generating root type: %w", err)
		}
	}

	// Mark aliases that cannot have methods (underlying resolves to pointer or interface).
	g.resolveAliasMethodability()

	// Populate ValidatableFields on structs — identify fields whose types have Validate().
	// Must run after resolveAliasMethodability so we know which types actually have methods.
	g.populateValidatableFields()
	g.populateAliasDelegates()

	// Add imports based on what was generated.
	g.output.ValidationCapability = analyzeValidationCapability(s, g.resourceGraph, g.config.Validation)
	g.addRequiredImports()

	return g.output, nil
}

// addRequiredImports scans generated TypeDefs and adds necessary imports.
func (g *Generator) addRequiredImports() {
	needsJSON := false
	needsFmt := false
	needsRegexp := false
	needsTime := false
	needsMath := false
	needsUTF8 := false
	needsBytes := false
	needsStrings := false
	needsBigInt := false
	needsNetIP := false
	needsNetMail := false
	needsNetURL := false
	needsStdRegexp := false
	needsValidationRuntime := false

	if g.output.ValidationCapability.RequiresRuntime && g.output.ValidationCapability.Mode != ValidationModeStatic {
		needsValidationRuntime = true
	}

	for _, td := range g.output.TypeDefs {
		if sd, ok := td.(*StructDef); ok {
			if sd.NeedsUnmarshal {
				needsJSON = true // UnmarshalJSON always uses json.Unmarshal
			}
			if sd.NeedsMarshal {
				needsJSON = true // MarshalJSON always uses json.Marshal
			}
			if sd.NeedsNullCheck {
				needsFmt = true // UnmarshalJSON uses fmt.Errorf for null rejection
			}
			if len(sd.OneOfs) > 0 {
				needsJSON = true
				needsFmt = true
			}
			// Check if any fields need manual JSON handling (control chars in names).
			for _, f := range sd.Fields {
				if f.ManualJSON {
					needsJSON = true
					needsFmt = true
					break
				}
			}
			if sd.AdditionalProperties != nil {
				needsJSON = true
				// fmt is needed for non-RawMessage additional properties (typed maps)
				// because the marshal template uses fmt.Errorf for marshaling errors,
				// and for forbidden additional properties validation.
				if sd.AdditionalProperties.ValueType.GoTypeName() != "json.RawMessage" || sd.AdditionalProperties.Forbidden {
					needsFmt = true
				}
			}
			if sd.HasRequiredFields() {
				needsJSON = true // UnmarshalJSON uses json.Unmarshal + json.RawMessage
			}
			if len(sd.Validations) > 0 || sd.HasRequiredFields() {
				needsFmt = true
				for _, v := range sd.Validations {
					if v.RuleType == "pattern" {
						needsRegexp = true
					}
					if v.RuleType == "multipleOf" {
						needsMath = true
					}
					if v.RuleType == "uniqueItems" {
						needsJSON = true
					}
					if v.RuleType == "minLength" || v.RuleType == "maxLength" {
						needsUTF8 = true
					}
				}
			}
			if len(sd.PatternProperties) > 0 {
				needsRegexp = true
				needsJSON = true
			}
			if sd.HasPatternPropertyValidation() {
				needsFmt = true
				for _, pp := range sd.PatternProperties {
					for _, v := range pp.Validations {
						if v.RuleType == "ppType" {
							needsBytes = true
						}
						if v.RuleType == "ppMultipleOf" {
							needsMath = true
						}
						if v.RuleType == "ppMinimum" || v.RuleType == "ppMaximum" ||
							v.RuleType == "ppExclusiveMinimum" || v.RuleType == "ppExclusiveMaximum" ||
							v.RuleType == "ppMultipleOf" ||
							v.RuleType == "ppMinItems" || v.RuleType == "ppMaxItems" ||
							v.RuleType == "ppMinLength" || v.RuleType == "ppMaxLength" {
							needsJSON = true
						}
						if v.RuleType == "ppMinLength" || v.RuleType == "ppMaxLength" {
							needsUTF8 = true
						}
						if v.RuleType == "ppPattern" {
							needsRegexp = true
						}
					}
				}
			}
			// Non-object validations use same pp* rule types.
			if len(sd.NonObjectValidations) > 0 {
				needsFmt = true
				for _, v := range sd.NonObjectValidations {
					if v.RuleType == "ppType" {
						needsBytes = true
					}
					if v.RuleType == "ppMultipleOf" {
						needsMath = true
					}
					if v.RuleType == "ppMinimum" || v.RuleType == "ppMaximum" ||
						v.RuleType == "ppExclusiveMinimum" || v.RuleType == "ppExclusiveMaximum" ||
						v.RuleType == "ppMultipleOf" ||
						v.RuleType == "ppMinItems" || v.RuleType == "ppMaxItems" ||
						v.RuleType == "ppMinLength" || v.RuleType == "ppMaxLength" {
						needsJSON = true
					}
					if v.RuleType == "ppMinLength" || v.RuleType == "ppMaxLength" {
						needsUTF8 = true
					}
					if v.RuleType == "ppPattern" {
						needsRegexp = true
					}
				}
			}
			if len(sd.DependentSchemas) > 0 {
				needsFmt = true  // Validate() uses fmt.Errorf for dependent schema errors
				needsJSON = true // UnmarshalJSON uses json.Unmarshal for _jsonKeys
				for _, ds := range sd.DependentSchemas {
					for _, pt := range ds.PropertyTypes {
						if pt.JSONType == "integer" {
							needsMath = true // math.Trunc for integer type check
						}
					}
				}
			}
			if len(sd.DependentRequired) > 0 {
				needsFmt = true  // Validate() uses fmt.Errorf for dependentRequired errors
				needsJSON = true // UnmarshalJSON uses json.Unmarshal for _jsonKeys
			}
			if sd.PropertyNames != nil {
				needsFmt = true  // Validate() uses fmt.Errorf for propertyNames errors
				needsJSON = true // UnmarshalJSON uses json.Unmarshal for _jsonKeys
				if sd.PropertyNames.MaxLength != nil || sd.PropertyNames.MinLength != nil {
					needsUTF8 = true
				}
				if sd.PropertyNames.Pattern != "" {
					needsRegexp = true // pattern uses ecma262
				}
			}
			if sd.UnevaluatedProperties != nil && !sd.UnevaluatedProperties.IsAllowed && !sd.UnevaluatedProperties.AllEvaluated {
				needsFmt = true // Validate() uses fmt.Errorf for unevaluated property errors
				if len(sd.UnevaluatedProperties.EvaluatedPatterns) > 0 {
					needsRegexp = true
				}
				if sd.UnevaluatedProperties.ValueType != "" {
					needsJSON = true // Validate() uses json.Unmarshal for schema-valued unevaluatedProperties
				}
				for _, v := range sd.UnevaluatedProperties.Validations {
					if v.RuleType == "minLength" || v.RuleType == "maxLength" {
						needsUTF8 = true
					}
					if v.RuleType == "pattern" {
						needsRegexp = true
					}
					if v.RuleType == "multipleOf" {
						needsMath = true
					}
				}
				// Conditional evals may need JSON for const checks and regexp for patterns.
				for _, ce := range sd.UnevaluatedProperties.ConditionalEvals {
					if ce.IfBranch != nil && len(ce.IfBranch.ConstChecks) > 0 {
						needsJSON = true
					}
					for _, b := range ce.Branches {
						if len(b.Patterns) > 0 {
							needsRegexp = true
						}
						if len(b.ConstChecks) > 0 {
							needsJSON = true
						}
					}
					if ce.ThenBranch != nil && len(ce.ThenBranch.Patterns) > 0 {
						needsRegexp = true
					}
					if ce.ElseBranch != nil && len(ce.ElseBranch.Patterns) > 0 {
						needsRegexp = true
					}
					if ce.Branch != nil && len(ce.Branch.Patterns) > 0 {
						needsRegexp = true
					}
				}
			}
			if len(sd.CousinUnevalChecks) > 0 {
				needsFmt = true // Validate() uses fmt.Errorf for cousin isolation errors
				for _, c := range sd.CousinUnevalChecks {
					if len(c.EvalPatterns) > 0 {
						needsRegexp = true
					}
				}
			}
			for _, f := range sd.Fields {
				if usesTimeType(f.Type) {
					needsTime = true
				}
				if usesJSONType(f.Type) {
					needsJSON = true
				}
				if usesNetIPType(f.Type) {
					needsNetIP = true
				}
			}
			// Check format validation rules for their import needs.
			for _, v := range sd.Validations {
				if v.RuleType == "format" {
					needsFmt = true
					switch v.Value.(string) {
					case "date", "time":
						needsTime = true
					case "email", "idn-email":
						needsNetMail = true
					case "uri", "uri-reference", "iri", "iri-reference", "uri-template":
						needsNetURL = true
					case "uuid", "hostname", "idn-hostname", "json-pointer", "relative-json-pointer", "regex", "duration":
						needsStdRegexp = true
					case "ipv4", "ipv6":
						needsNetIP = true
					}
				}
			}
		}
		if ed, ok := td.(*EnumDef); ok {
			needsFmt = true // Validate() uses fmt.Errorf for invalid enum values
			if ed.IsRaw {
				needsJSON = true // raw enums use json.RawMessage + UnmarshalJSON/MarshalJSON
			}
		}
		if ad, ok := td.(*AliasDef); ok {
			if usesTimeType(ad.Underlying) {
				needsTime = true
			}
			if usesNetIPType(ad.Underlying) {
				needsNetIP = true
			}
			if usesJSONType(ad.Underlying) {
				needsJSON = true
			}
			if ad.NeedsNullCheck && ad.CanHaveMethods() {
				needsJSON = true // UnmarshalJSON uses json.Unmarshal
				needsFmt = true  // UnmarshalJSON uses fmt.Errorf
			}
			if ad.IsIntegerType() && ad.CanHaveMethods() {
				needsJSON = true // UnmarshalJSON uses json.Number
				needsFmt = true  // UnmarshalJSON uses fmt.Errorf
				needsMath = true // UnmarshalJSON uses math.Trunc, math.IsInf
			}
			if ad.ValidateAs != "" && ad.CanHaveMethods() {
				needsFmt = true // Validate() wraps delegated validation errors
			}
			if ad.UnmarshalAs != "" && ad.CanHaveMethods() {
				needsJSON = true // UnmarshalJSON delegates through json.Unmarshal
			}
			if ad.MarshalAs != "" && ad.CanHaveMethods() {
				needsJSON = true // MarshalJSON delegates through json.Marshal
			}
			if ad.HasTupleItems() {
				needsJSON = true // Validate() uses json.Marshal/json.Unmarshal for tuple items
				needsFmt = true  // Validate() uses fmt.Errorf for tuple item errors
			}
			if len(ad.Validations) > 0 {
				needsFmt = true
				for _, v := range ad.Validations {
					if v.RuleType == "pattern" {
						needsRegexp = true
					}
					if v.RuleType == "multipleOf" {
						needsMath = true
					}
					if v.RuleType == "uniqueItems" {
						needsJSON = true
					}
					if v.RuleType == "minLength" || v.RuleType == "maxLength" {
						needsUTF8 = true
					}
				}
			}
			if len(ad.AnyOfVariants) > 0 {
				needsFmt = true // anyOf error message uses fmt.Errorf
				for _, variant := range ad.AnyOfVariants {
					for _, v := range variant {
						if v.RuleType == "pattern" {
							needsRegexp = true
						}
						if v.RuleType == "multipleOf" {
							needsMath = true
						}
						if v.RuleType == "uniqueItems" {
							needsJSON = true
						}
						if v.RuleType == "minLength" || v.RuleType == "maxLength" {
							needsUTF8 = true
						}
					}
				}
			}
			// Contains validation imports.
			if ad.Contains != nil {
				if ad.Contains.ConstJSON != "" {
					needsJSON = true
				}
				if len(ad.Contains.EnumJSON) > 0 {
					needsJSON = true
				}
				for _, chk := range ad.Contains.Checks {
					if chk.CheckType == "multipleOf" {
						needsMath = true
					}
					if chk.CheckType == "pattern" {
						needsStdRegexp = true
					}
				}
			}
		}
		if _, ok := td.(*BigIntAliasDef); ok {
			needsJSON = true    // UnmarshalJSON, MarshalJSON
			needsFmt = true     // Validate() errors, String()
			needsMath = true    // math.Trunc, math.IsInf
			needsStrings = true // strings.ContainsAny for float-format bignums
			needsBigInt = true  // math/big for *big.Int
		}
		if tosd, ok := td.(*TypeOnlySchemaDef); ok {
			needsJSON = true // UnmarshalJSON, MarshalJSON, json.RawMessage
			needsFmt = true  // Validate() errors
			for _, at := range tosd.AllowedTypes {
				if at == "integer" {
					needsMath = true // math.Trunc, math.IsInf for integer check
				}
			}
			for _, branch := range tosd.TypeBranches {
				for _, at := range branch.AllowedTypes {
					if at == "integer" {
						needsMath = true
					}
				}
				for _, prop := range branch.Properties {
					if prop.JSONType == "integer" {
						needsMath = true
					}
				}
			}
		}
		if nsd, ok := td.(*NotSchemaDef); ok {
			needsJSON = true // UnmarshalJSON, MarshalJSON, json.RawMessage
			needsFmt = true  // Validate() errors
			if len(nsd.NotTypes) > 0 || len(nsd.NotBranches) > 0 {
				// Type checks for "integer" use math.Trunc and math.IsInf.
				for _, nt := range nsd.NotTypes {
					if nt == "integer" {
						needsMath = true
					}
				}
				for _, branch := range nsd.NotBranches {
					for _, nt := range branch.Types {
						if nt == "integer" {
							needsMath = true
						}
					}
					for _, prop := range branch.Properties {
						if prop.JSONType == "integer" {
							needsMath = true
						}
					}
					for _, v := range branch.Validations {
						if v.RuleType == "multipleOf" {
							needsMath = true
						}
						if v.RuleType == "minLength" || v.RuleType == "maxLength" {
							needsUTF8 = true
						}
						if v.RuleType == "pattern" {
							needsRegexp = true
						}
					}
				}
			}
		}
		if iad, ok := td.(*InferredAliasDef); ok {
			needsJSON = true // UnmarshalJSON, MarshalJSON, json.RawMessage
			needsFmt = true  // Validate() errors, String()
			for _, v := range iad.Validations {
				if v.RuleType == "minLength" || v.RuleType == "maxLength" {
					needsUTF8 = true
				}
				if v.RuleType == "pattern" {
					needsRegexp = true
				}
				if v.RuleType == "multipleOf" {
					needsMath = true
				}
			}
			for _, variant := range iad.AnyOfVariants {
				for _, v := range variant {
					if v.RuleType == "minLength" || v.RuleType == "maxLength" {
						needsUTF8 = true
					}
					if v.RuleType == "pattern" {
						needsRegexp = true
					}
					if v.RuleType == "multipleOf" {
						needsMath = true
					}
				}
			}
			for _, variant := range iad.OneOfVariants {
				for _, v := range variant {
					if v.RuleType == "minLength" || v.RuleType == "maxLength" {
						needsUTF8 = true
					}
					if v.RuleType == "pattern" {
						needsRegexp = true
					}
					if v.RuleType == "multipleOf" {
						needsMath = true
					}
				}
			}
			// Item-level validation may need math.Trunc for integer checks.
			if iad.ItemsType == "integer" || iad.AdditionalItemsType == "integer" ||
				(iad.ItemsNested != nil && iad.ItemsNested.ItemsType == "integer") {
				needsMath = true
			}
			for _, ti := range iad.TupleItems {
				if ti.JSONType == "integer" {
					needsMath = true
				}
			}
			// Items checks imports.
			for _, chk := range iad.ItemsChecks {
				if chk.CheckType == "multipleOf" {
					needsMath = true
				}
				if chk.CheckType == "type" && chk.Value == "integer" {
					needsMath = true
				}
			}
			// Contains validation imports.
			if iad.Contains != nil {
				if iad.Contains.ConstJSON != "" {
					needsJSON = true // json.Marshal for per-element comparison
				}
				if len(iad.Contains.EnumJSON) > 0 {
					needsJSON = true // json.Marshal for per-element enum comparison
				}
				for _, chk := range iad.Contains.Checks {
					if chk.CheckType == "multipleOf" {
						needsMath = true
					}
					if chk.CheckType == "pattern" {
						needsStdRegexp = true
					}
				}
			}
		}
	}

	if needsJSON {
		g.output.Imports = append(g.output.Imports, Import{Path: "encoding/json"})
	}
	if needsFmt {
		g.output.Imports = append(g.output.Imports, Import{Path: "fmt"})
	}
	if needsRegexp {
		g.output.Imports = append(g.output.Imports, Import{Path: "github.com/mgilbir/goecma262", Alias: "ecma262"})
		g.output.Imports = append(g.output.Imports, Import{Path: "github.com/mgilbir/goecma262/flags", Alias: "ecmaflags"})
	}
	if needsMath {
		g.output.Imports = append(g.output.Imports, Import{Path: "math"})
	}
	if needsTime {
		g.output.Imports = append(g.output.Imports, Import{Path: "time"})
	}
	if needsUTF8 {
		g.output.Imports = append(g.output.Imports, Import{Path: "unicode/utf8"})
	}
	if needsBytes {
		g.output.Imports = append(g.output.Imports, Import{Path: "bytes"})
	}
	if needsStrings {
		g.output.Imports = append(g.output.Imports, Import{Path: "strings"})
	}
	if needsBigInt {
		g.output.Imports = append(g.output.Imports, Import{Path: "math/big"})
	}
	if needsNetIP {
		g.output.Imports = append(g.output.Imports, Import{Path: "net/netip"})
	}
	if needsNetMail {
		g.output.Imports = append(g.output.Imports, Import{Path: "net/mail"})
	}
	if needsNetURL {
		g.output.Imports = append(g.output.Imports, Import{Path: "net/url"})
	}
	if needsStdRegexp {
		g.output.Imports = append(g.output.Imports, Import{Path: "regexp"})
	}
	if needsValidationRuntime {
		g.output.Imports = append(g.output.Imports, Import{Path: "github.com/mgilbir/schemagen/pkg/validationruntime"})
	}
}

// isInferredAlias returns true if a type name was generated as an InferredAliasDef.
func (g *Generator) isInferredAlias(name string) bool {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() == name {
			_, ok := td.(*InferredAliasDef)
			return ok
		}
	}
	return false
}

// isBigIntAlias returns true if a type name was generated as a BigIntAliasDef.
func (g *Generator) isBigIntAlias(name string) bool {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() == name {
			_, ok := td.(*BigIntAliasDef)
			return ok
		}
	}
	return false
}

// isNotSchema returns true if a type name was generated as a NotSchemaDef.
func (g *Generator) isNotSchema(name string) bool {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() == name {
			_, ok := td.(*NotSchemaDef)
			return ok
		}
	}
	return false
}

// isTypeOnlySchema returns true if a type name was generated as a TypeOnlySchemaDef.
func (g *Generator) isTypeOnlySchema(name string) bool {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() == name {
			_, ok := td.(*TypeOnlySchemaDef)
			return ok
		}
	}
	return false
}

// resolveAliasMethodability walks all AliasDefs and sets NoMethods=true
// for any whose underlying type chain resolves to a pointer or interface.
// This handles cases like `type Root Bool` where Bool is `type Bool any` —
// Go does not allow methods on types whose ultimate underlying type is
// a pointer or interface type.
func (g *Generator) resolveAliasMethodability() {
	// Build a map of type name → AliasDef for cross-referencing.
	aliases := make(map[string]*AliasDef)
	for _, td := range g.output.TypeDefs {
		if ad, ok := td.(*AliasDef); ok {
			aliases[ad.Name] = ad
		}
	}

	// For each alias, walk the underlying type chain to check if it
	// ultimately resolves to a pointer or interface.
	for _, ad := range aliases {
		if !canHaveMethodsResolved(ad.Underlying, aliases) {
			ad.NoMethods = true
		}
	}
}

// canHaveMethodsResolved checks if a GoType can be used as a method receiver,
// following NamedType references through the alias map. The visited set
// prevents infinite recursion on self-referencing alias cycles.
func canHaveMethodsResolved(t GoType, aliases map[string]*AliasDef) bool {
	visited := make(map[string]bool)
	return canHaveMethodsResolvedImpl(t, aliases, visited)
}

func canHaveMethodsResolvedImpl(t GoType, aliases map[string]*AliasDef, visited map[string]bool) bool {
	if t.IsPointer() {
		return false
	}
	if pt, ok := t.(*PrimitiveType); ok && pt.Name == "any" {
		return false
	}
	if nt, ok := t.(*NamedType); ok {
		if visited[nt.Name] {
			return true // cycle detected — assume safe
		}
		visited[nt.Name] = true
		if ref, exists := aliases[nt.Name]; exists {
			return canHaveMethodsResolvedImpl(ref.Underlying, aliases, visited)
		}
	}
	return true
}

// usesTimeType returns true if the GoType references time.Time.
func usesTimeType(t GoType) bool {
	if t == nil {
		return false
	}
	switch v := t.(type) {
	case *PrimitiveType:
		return v.Name == "time.Time"
	case *PointerType:
		return usesTimeType(v.Inner)
	case *ArrayType:
		return usesTimeType(v.ItemType)
	case *MapType:
		return usesTimeType(v.KeyType) || usesTimeType(v.ValueType)
	}
	return false
}

// usesJSONType returns true if the GoType references a type from encoding/json
// (e.g. json.RawMessage).
func usesJSONType(t GoType) bool {
	if t == nil {
		return false
	}
	switch v := t.(type) {
	case *PrimitiveType:
		return v.Name == "json.RawMessage"
	case *PointerType:
		return usesJSONType(v.Inner)
	case *ArrayType:
		return usesJSONType(v.ItemType)
	case *MapType:
		return usesJSONType(v.KeyType) || usesJSONType(v.ValueType)
	}
	return false
}

// formatGoType returns the Go type for a JSON Schema format string, or nil if
// the format should remain as the default type (string) with validation only.
func formatGoType(format string) GoType {
	switch format {
	case "date-time":
		return &PrimitiveType{Name: "time.Time"}
	case "ipv4", "ipv6":
		return &PrimitiveType{Name: "netip.Addr"}
	default:
		// Formats like date, time, email, hostname, uri, uuid, duration, etc.
		// remain as string with format validation in Validate().
		return nil
	}
}

// formatNeedsValidation returns true if the given format string should produce
// a validation rule in Validate() for string-typed fields that don't get a
// distinct Go type mapping.
func formatNeedsValidation(format string) bool {
	switch format {
	case "date", "time",
		"email", "idn-email",
		"hostname", "idn-hostname",
		"uri", "uri-reference",
		"iri", "iri-reference",
		"uri-template",
		"uuid",
		"duration",
		"json-pointer", "relative-json-pointer",
		"regex":
		return true
	default:
		return false
	}
}

// usesNetIPType returns true if the GoType references netip.Addr.
func usesNetIPType(t GoType) bool {
	if t == nil {
		return false
	}
	switch v := t.(type) {
	case *PrimitiveType:
		return v.Name == "netip.Addr"
	case *PointerType:
		return usesNetIPType(v.Inner)
	case *ArrayType:
		return usesNetIPType(v.ItemType)
	case *MapType:
		return usesNetIPType(v.KeyType) || usesNetIPType(v.ValueType)
	}
	return false
}

// generateTypeDef creates the appropriate TypeDef for a schema and adds it to
// the output File. It skips schemas that have already been generated.
func (g *Generator) generateTypeDef(name string, s *schema.Schema) error {
	if g.generated[name] {
		return nil
	}

	// Const -> treat as single-element enum for validation purposes.
	if g.validationKeywordsEnabled() {
		if s.Const != nil && len(s.Enum) == 0 {
			s.Enum = []any{*s.Const}
		} else if s.ConstIsNull && s.Const == nil && len(s.Enum) == 0 {
			// Handle {"const": null}: Go's json.Unmarshal sets *any to nil for null,
			// so s.Const is nil even though const was present. Use ConstIsNull flag.
			s.Enum = []any{nil}
		}
	}

	// Enum type
	if g.validationKeywordsEnabled() && len(s.Enum) > 0 {
		return g.generateEnumDef(name, s)
	}

	// In draft2019-09+, $ref is an applicator that works alongside sibling keywords.
	// When a schema has both $ref and structural keywords (properties, patternProperties,
	// unevaluatedProperties, additionalProperties, or array-structural keywords like
	// prefixItems, items, unevaluatedItems), synthesize an implicit allOf so both
	// the $ref target and local keywords are merged into a single definition.
	if s.Ref != "" && !g.refOverridesSiblingsForSchema(s) && (hasProperties(s) || len(s.PatternProperties) > 0 || s.UnevaluatedProperties != nil || s.AdditionalProperties != nil ||
		len(s.PrefixItems) > 0 || s.Items != nil || s.UnevaluatedItems != nil) {
		refSub := &schema.Schema{
			Ref:          s.Ref,
			BaseURI:      s.BaseURI,
			DocumentRoot: s.DocumentRoot,
		}
		synth := *s // shallow copy
		synth.Ref = ""
		synth.AllOf = append([]*schema.Schema{refSub}, synth.AllOf...)
		return g.generateAllOfDef(name, &synth)
	}

	// allOf → merge all sub-schemas into one struct
	if len(s.AllOf) > 0 {
		return g.generateAllOfDef(name, s)
	}

	// anyOf/oneOf with only boolean false sub-schemas → nothing can match → forbidden.
	if len(s.AnyOf) > 0 && !hasProperties(s) && allSubsFalse(s.AnyOf) {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, &NotSchemaDef{
			Name:        name,
			Description: s.Description,
			IsForbidden: true,
		})
		return nil
	}
	if len(s.OneOf) > 0 && !hasProperties(s) && len(s.Type) == 0 {
		// oneOf: all false → nothing matches → forbidden.
		// oneOf: multiple true sub-schemas → multiple match → forbidden (oneOf requires exactly one).
		trueCount, falseCount := countBooleanSchemas(s.OneOf)
		total := len(s.OneOf)
		if falseCount == total {
			// All false → nothing matches
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, &NotSchemaDef{
				Name:        name,
				Description: s.Description,
				IsForbidden: true,
			})
			return nil
		}
		if trueCount > 1 {
			// More than one always-true sub-schema → always multiple matches → forbidden
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, &NotSchemaDef{
				Name:        name,
				Description: s.Description,
				IsForbidden: true,
			})
			return nil
		}
	}

	// anyOf without properties → merge all variant properties into one struct,
	// but only if at least one sub-schema actually contributes properties.
	if len(s.AnyOf) > 0 && !hasProperties(s) && g.anyOfHasProperties(s) {
		return g.generateAnyOfDef(name, s)
	}

	// anyOf with null + single non-null variant → nullable alias (e.g., anyOf: [null, string] → *string).
	// This also handles the pattern where the non-null variant is a $ref to a primitive type.
	if len(s.AnyOf) > 0 && !hasProperties(s) {
		nonNull, hasNull := g.separateNullFromOneOf(s.AnyOf)
		if hasNull && len(nonNull) == 1 {
			variant := nonNull[0]
			// If the variant is a $ref, resolve it first so we generate the type
			// based on the target schema rather than the ref string (avoids name
			// collisions when the ref target is a remote schema root).
			effective := variant
			if effRef := variant.EffectiveRef(); effRef != "" {
				if resolved := g.resolveRefInContext(effRef, variant); resolved != nil {
					effective = resolved
				}
			}
			goType := g.resolveType(effective, name)
			if !goType.IsPointer() {
				goType = &PointerType{Inner: goType}
			}
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
				Name:        name,
				Underlying:  goType,
				Description: s.Description,
			})
			return nil
		}
	}

	// oneOf without properties in parent or any variant → alias to `any`
	// (e.g. {"oneOf": [{"maximum": 3}, {"minimum": 5}]} can hold any JSON value)
	if len(s.OneOf) > 0 && !hasProperties(s) && !g.oneOfHasProperties(s) {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
			Name:        name,
			Underlying:  &PrimitiveType{Name: "any"},
			Description: s.Description,
		})
		return nil
	}

	// Object with properties, patternProperties, oneOf fields, or unevaluatedProperties → struct
	if hasProperties(s) || len(s.PatternProperties) > 0 || len(s.OneOf) > 0 || s.UnevaluatedProperties != nil {
		// Only accept non-object data for schemas with object keywords (properties/patternProperties)
		// but without oneOf (which is type-agnostic and should validate all types).
		canAcceptNonObject := (hasProperties(s) || len(s.PatternProperties) > 0 || s.UnevaluatedProperties != nil) && len(s.OneOf) == 0
		return g.generateStructDef(name, s, canAcceptNonObject)
	}

	// Ref only → alias (handles $ref, $recursiveRef)
	if effRef := s.EffectiveRef(); effRef != "" {
		resolved := g.resolveRefInContext(effRef, s)
		if resolved != nil {
			pushed := g.pushDynamicScope(resolved)
			refName := g.goNameForResolvedRef(effRef, resolved, refToGoName(effRef))
			// Generate the referenced type definition (e.g., for remote $ref targets).
			if err := g.generateTypeDef(refName, resolved); err != nil {
				if pushed {
					g.popDynamicScope()
				}
				return err
			}
			// If the ref target was generated as a wrapper struct (InferredAliasDef,
			// BigIntAliasDef, or NotSchemaDef), creating `type Root Target` would not
			// inherit methods. Instead, generate Root directly from the resolved schema.
			if g.isInferredAlias(refName) || g.isBigIntAlias(refName) || g.isNotSchema(refName) || g.isTypeOnlySchema(refName) {
				err := g.generateTypeDef(name, resolved)
				if pushed {
					g.popDynamicScope()
				}
				return err
			}
			if pushed {
				g.popDynamicScope()
			}
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
				Name:        name,
				Underlying:  &NamedType{Name: refName},
				Description: s.Description,
			})
			return nil
		}
	}

	// $dynamicRef → resolve via dynamic scope chain.
	// Plain name fragments (like "#items") resolve via $dynamicAnchor with scope walking.
	// JSON pointer fragments (like "#/$defs/foo") resolve identically to $ref.
	// URI-based $dynamicRef (like "extended#meta") resolves the URI part first, then
	// checks the bookend $dynamicAnchor and walks the dynamic scope.
	if s.DynamicRef != "" {
		resolved := g.resolveDynamicRef(s.DynamicRef, s)
		if resolved != nil {
			refName := g.goNameForResolvedRef(s.DynamicRef, resolved, refToGoName(s.DynamicRef))
			if err := g.generateTypeDef(refName, resolved); err != nil {
				return err
			}
			if g.isInferredAlias(refName) || g.isBigIntAlias(refName) || g.isNotSchema(refName) || g.isTypeOnlySchema(refName) {
				return g.generateTypeDef(name, resolved)
			}
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
				Name:        name,
				Underlying:  &NamedType{Name: refName},
				Description: s.Description,
			})
			return nil
		}
	}

	// Root-level "not" schema: generates a wrapper around json.RawMessage that
	// validates the negated constraint. Only handles schemas where "not" is the
	// sole meaningful keyword (no type, properties, items, etc.).
	if notDef := extractNotSchemaDef(name, s); notDef != nil {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, notDef)
		return nil
	}

	// Multi-type or null-only schemas: generates a wrapper around json.RawMessage
	// that validates the value's JSON type against the allowed types. This handles
	// schemas like {"type": "null"}, {"type": ["integer","string"]}, etc. that
	// don't map to a single Go type.
	if toDef := extractTypeOnlySchemaDef(name, s); toDef != nil {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, toDef)
		return nil
	}

	// Draft 3 allows schema-valued alternatives inside the type array. When mixed
	// with a single primitive type (for example integer OR an object schema), use
	// the same raw wrapper as multi-type schemas so both alternatives can validate.
	if len(s.TypeSchemas) > 0 {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, &TypeOnlySchemaDef{
			Name:         name,
			Description:  s.Description,
			AllowedTypes: s.Type,
			TypeBranches: extractTypeSchemaBranches(s.TypeSchemas),
		})
		return nil
	}

	// Simple primitive type → alias (or defined type if it has validation constraints)
	// When no explicit type is declared, infer from constraint keywords.
	primaryType := primarySchemaType(s)
	isInferred := false
	if primaryType == "" {
		primaryType = g.inferTypeFromConstraints(s)
		if primaryType != "" {
			isInferred = true
		}
	}
	if primaryType != "" && primaryType != "object" && primaryType != "array" {
		goType := g.resolveType(s, name)
		var rules []ValidationRule
		var anyOfVariants [][]ValidationRule
		var oneOfVariants [][]ValidationRule
		if g.validationKeywordsEnabled() {
			rules = extractAliasValidationRules(s, goType)
			anyOfVariants = extractAnyOfVariantRules(s, goType)
			oneOfVariants = extractOneOfVariantRules(s, goType)
		}
		g.generated[name] = true
		if isInferred {
			// Type was inferred from constraints — generate wrapper struct that
			// accepts any JSON value but validates only matching types.
			g.output.TypeDefs = append(g.output.TypeDefs, &InferredAliasDef{
				Name:             name,
				Description:      s.Description,
				InferredGoType:   goType,
				InferredJSONType: primaryType,
				Validations:      rules,
				AnyOfVariants:    anyOfVariants,
				OneOfVariants:    oneOfVariants,
				NeedsNullCheck:   !schemaAllowsNull(s),
			})
		} else if g.config.BigIntSupport && primaryType == "integer" {
			// BigInt support: generate wrapper struct with int64 + *big.Int.
			g.output.TypeDefs = append(g.output.TypeDefs, &BigIntAliasDef{
				Name:           name,
				Description:    s.Description,
				Validations:    rules,
				AnyOfVariants:  anyOfVariants,
				OneOfVariants:  oneOfVariants,
				NeedsNullCheck: !schemaAllowsNull(s),
			})
		} else {
			g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
				Name:           name,
				Underlying:     goType,
				Description:    s.Description,
				Validations:    rules,
				AnyOfVariants:  anyOfVariants,
				StrictInteger:  primaryType == "integer" && g.requiresStrictIntegerToken(s),
				NeedsNullCheck: !schemaAllowsNull(s),
			})
		}
		return nil
	}

	// Array type → alias (or defined type if it has validation constraints)
	if primaryType == "array" {
		goType := g.resolveType(s, name)
		var rules []ValidationRule
		var anyOfVariants [][]ValidationRule
		if g.validationKeywordsEnabled() {
			rules = extractAliasValidationRules(s, goType)
			anyOfVariants = extractAnyOfVariantRules(s, goType)
		}
		// Mark as generated BEFORE buildTupleItemDefs so that any recursive
		// $ref back to this type (via generateTypeDef inside buildTupleItemDefs)
		// will short-circuit and not cause infinite recursion.
		g.generated[name] = true
		if isInferred {
			// Inferred array type — wrapper struct for non-array fallback.
			// Extract item-level validation constraints.
			itemsFalse, itemsType, itemsTypeName, itemsChecks, itemsNested, tupleItems, addlItemsFalse, addlItemsType := g.extractInferredItemConstraints(s, name)
			// Extract contains/minContains/maxContains constraints.
			containsDef, minContains, maxContains := extractContainsDef(s)
			// Extract unevaluatedItems constraint.
			unevalItems := g.buildUnevaluatedItemsDef(s)
			if !g.validationKeywordsEnabled() {
				itemsFalse = false
				itemsType = ""
				itemsTypeName = ""
				itemsChecks = nil
				itemsNested = nil
				tupleItems = nil
				addlItemsFalse = false
				addlItemsType = ""
				containsDef = nil
				minContains = nil
				maxContains = nil
				unevalItems = nil
			}
			// When item-level or contains validation is needed, force GoType to []any so that
			// json.Unmarshal accepts any array (per-element validation in Validate()).
			// If the typed array (e.g., []int64) were used, unmarshal would fail
			// entirely on mixed-type arrays, masking per-element errors.
			inferredGoType := goType
			if itemsFalse || itemsType != "" || itemsTypeName != "" ||
				len(itemsChecks) > 0 || itemsNested != nil ||
				len(tupleItems) > 0 || addlItemsFalse || addlItemsType != "" ||
				containsDef != nil || unevalItems != nil {
				inferredGoType = &ArrayType{ItemType: &PrimitiveType{Name: "any"}}
			}
			g.output.TypeDefs = append(g.output.TypeDefs, &InferredAliasDef{
				Name:                 name,
				Description:          s.Description,
				InferredGoType:       inferredGoType,
				InferredJSONType:     primaryType,
				Validations:          rules,
				AnyOfVariants:        anyOfVariants,
				NeedsNullCheck:       !schemaAllowsNull(s),
				ItemsFalse:           itemsFalse,
				ItemsType:            itemsType,
				ItemsTypeName:        itemsTypeName,
				ItemsChecks:          itemsChecks,
				ItemsNested:          itemsNested,
				TupleItems:           tupleItems,
				AdditionalItemsFalse: addlItemsFalse,
				AdditionalItemsType:  addlItemsType,
				Contains:             containsDef,
				MinContains:          minContains,
				MaxContains:          maxContains,
				UnevaluatedItems:     unevalItems,
			})
		} else {
			tupleItems := g.buildTupleItemDefs(s, name)
			containsDef, minContains, maxContains := extractContainsDef(s)
			if !g.validationKeywordsEnabled() {
				tupleItems = nil
				containsDef = nil
				minContains = nil
				maxContains = nil
			}
			g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
				Name:           name,
				Underlying:     goType,
				Description:    s.Description,
				Validations:    rules,
				AnyOfVariants:  anyOfVariants,
				TupleItems:     tupleItems,
				Contains:       containsDef,
				MinContains:    minContains,
				MaxContains:    maxContains,
				StrictInteger:  primaryType == "integer" && g.requiresStrictIntegerToken(s),
				NeedsNullCheck: !schemaAllowsNull(s),
			})
		}
		return nil
	}

	// Object with no properties → struct with overflow map for lossless round-trip.
	// If additionalProperties is explicitly false, still generate overflow map to capture
	// unknown keys for validation rejection.
	if primaryType == "object" {
		g.generated[name] = true
		var additionalProps *AdditionalPropertiesDef
		if s.AdditionalProperties != nil && s.AdditionalProperties.Bool != nil && !*s.AdditionalProperties.Bool {
			// additionalProperties: false → overflow map with Forbidden flag for validation
			additionalProps = &AdditionalPropertiesDef{
				ValueType: &PrimitiveType{Name: "json.RawMessage"},
				Forbidden: true,
			}
		} else if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
			valueType := g.resolveType(s.AdditionalProperties.Schema, name+"Value")
			additionalProps = &AdditionalPropertiesDef{ValueType: valueType}
		} else {
			// Default or additionalProperties: true → json.RawMessage overflow map
			additionalProps = &AdditionalPropertiesDef{
				ValueType: &PrimitiveType{Name: "json.RawMessage"},
			}
		}
		needsNullCheck := !schemaAllowsNull(s)
		acceptNonObject := !schemaHasExplicitType(s, "object")
		needsMarshal := additionalProps != nil || acceptNonObject
		needsUnmarshal := additionalProps != nil || needsNullCheck || acceptNonObject
		var validations []ValidationRule
		if g.validationKeywordsEnabled() && s.MaxProperties != nil {
			validations = append(validations, ValidationRule{
				RuleType: "maxProperties", Value: s.MaxProperties.Int(),
			})
		}
		if g.validationKeywordsEnabled() && s.MinProperties != nil {
			validations = append(validations, ValidationRule{
				RuleType: "minProperties", Value: s.MinProperties.Int(),
			})
		}
		// Required fields on property-less object schemas (e.g., {"type":"object","required":["foo"]}).
		// All required names land in AdditionalProperties since there are no declared properties.
		var requiredJSON []string
		if g.validationKeywordsEnabled() && len(s.Required) > 0 {
			requiredJSON = s.Required
			needsUnmarshal = true
		}
		// Extract dependentRequired constraints.
		var depRequired []DependentRequiredDef
		if g.validationKeywordsEnabled() {
			for trigger, deps := range s.DependentRequired {
				if len(deps) > 0 {
					sorted := make([]string, len(deps))
					copy(sorted, deps)
					sort.Strings(sorted)
					depRequired = append(depRequired, DependentRequiredDef{
						TriggerKey: trigger,
						Required:   sorted,
					})
				}
			}
		}
		sort.Slice(depRequired, func(i, j int) bool {
			return depRequired[i].TriggerKey < depRequired[j].TriggerKey
		})
		if len(depRequired) > 0 {
			needsUnmarshal = true
		}
		// Extract dependentSchemas constraints.
		var depSchemas []DependentSchemaConstraint
		if g.validationKeywordsEnabled() {
			depSchemas = extractDependentSchemaConstraints(s)
		}
		if len(depSchemas) > 0 {
			needsUnmarshal = true
		}
		// Extract propertyNames constraint.
		var propNames *PropertyNamesDef
		if s.PropertyNames != nil && g.validationKeywordsEnabled() {
			propNames = extractPropertyNamesDef(s.PropertyNames)
			if propNames != nil {
				needsUnmarshal = true // need _jsonKeys for validation
			}
		}
		g.output.TypeDefs = append(g.output.TypeDefs, &StructDef{
			Name:                 name,
			Description:          s.Description,
			AdditionalProperties: additionalProps,
			DependentSchemas:     depSchemas,
			DependentRequired:    depRequired,
			PropertyNames:        propNames,
			Validations:          validations,
			RequiredJSON:         requiredJSON,
			NeedsMarshal:         needsMarshal,
			NeedsUnmarshal:       needsUnmarshal,
			NeedsNullCheck:       needsNullCheck,
			AcceptNonObject:      acceptNonObject,
		})
		return nil
	}

	// Fallback: alias to any
	goType := &PrimitiveType{Name: "any"}
	var rules []ValidationRule
	if g.validationKeywordsEnabled() {
		rules = extractAliasValidationRules(s, goType)
	}
	g.generated[name] = true
	g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
		Name:        name,
		Underlying:  goType,
		Description: s.Description,
		Validations: rules,
	})
	return nil
}

// generateStructDef produces a StructDef from an object schema.
// It also handles oneOf properties within the struct.
// When acceptNonObject is true and the schema has no explicit "type":"object",
// non-object JSON data (numbers, strings, arrays) is silently accepted rather
// than causing an unmarshal error. This should only be true for schemas whose
// constraints are purely object-specific (properties, additionalProperties, etc.)
// and NOT for schemas generated from applicator merging (allOf, anyOf).
func (g *Generator) generateStructDef(name string, s *schema.Schema, acceptNonObject bool) error {
	g.generated[name] = true
	g.structsInProgress[name] = true
	defer delete(g.structsInProgress, name)

	requiredList := s.Required
	if !g.validationKeywordsEnabled() {
		requiredList = nil
	}
	requiredSet := make(map[string]bool, len(requiredList))
	for _, r := range requiredList {
		requiredSet[r] = true
	}

	// Collect regular fields and oneOf fields separately.
	var fields []FieldDef
	var oneOfs []OneOfDef
	needsMarshal := false
	needsUnmarshal := false

	// Sort property names for deterministic output.
	propNames := sortedKeys(s.Properties)

	// First pass: compute Go field names and deduplicate collisions.
	goFieldNames := make(map[string]string, len(propNames)) // JSON name → Go name
	nameCount := make(map[string]int)
	for _, propName := range propNames {
		goName := JSONPropertyToGoName(propName)
		nameCount[goName]++
	}
	// Second pass: assign unique names by appending numeric suffix when collisions exist.
	nameSeen := make(map[string]int)
	for _, propName := range propNames {
		goName := JSONPropertyToGoName(propName)
		if nameCount[goName] > 1 {
			nameSeen[goName]++
			goName = fmt.Sprintf("%s%d", goName, nameSeen[goName])
		}
		goFieldNames[propName] = goName
	}

	for _, propName := range propNames {
		propSchema := s.Properties[propName]
		goFieldName := goFieldNames[propName]
		required := requiredSet[propName]

		// Check if this property uses oneOf
		if propSchema != nil && len(propSchema.OneOf) > 0 {
			oneOfDef, err := g.generateOneOfForProperty(name, propName, goFieldName, propSchema)
			if err != nil {
				return fmt.Errorf("property %s (oneOf): %w", propName, err)
			}
			if oneOfDef != nil {
				oneOfDef.Required = required
				oneOfs = append(oneOfs, *oneOfDef)
				needsMarshal = true
				needsUnmarshal = true
				continue
			}
		}

		goType, err := g.resolvePropertyType(propSchema, name, goFieldName, s)
		if err != nil {
			return fmt.Errorf("property %s: %w", propName, err)
		}

		omitEmpty := g.config.OmitEmpty && !required
		// Never use omitempty for null-typed properties — omitempty strips nil values
		// but {"foo": null} must be preserved in round-trip.
		if omitEmpty && isNullOnly(propSchema) {
			omitEmpty = false
		}
		// Suppress omitempty for properties whose schema explicitly includes null
		// (via type list or anyOf/oneOf composition). These generate pointer types
		// where omitempty would incorrectly drop JSON null values.
		// NOTE: This does NOT suppress omitempty for all pointer types — recursive
		// self-refs also produce pointers but should keep omitempty so that absent
		// optional fields are omitted rather than emitted as null.
		if omitEmpty && isNullable(propSchema) {
			omitEmpty = false
		}
		if omitEmpty && g.isNullableComposition(propSchema) {
			omitEmpty = false
		}
		// For optional array/slice fields with omitempty, wrap in a pointer (*[]T)
		// so that absent → nil (omitted) while {"foo": []} → &[]T{} (preserved).
		// Without this, omitempty treats nil and empty slices identically.
		// Check both the Go type directly and the resolved schema type, since
		// $ref properties resolve to NamedType even when the target is an array.
		if omitEmpty && g.isArrayProperty(goType, propSchema) {
			goType = &PointerType{Inner: goType}
		}
		manualJSON := needsManualJSON(propName)

		// Compute default literal if schema provides a default value.
		var defaultLiteral string
		if propSchema.Default != nil {
			defaultLiteral = defaultToGoLiteral(*propSchema.Default, goType)
		}

		fields = append(fields, FieldDef{
			Name:           goFieldName,
			JSONName:       propName,
			Type:           goType,
			OmitEmpty:      omitEmpty,
			Required:       required,
			Description:    propSchema.Description,
			ManualJSON:     manualJSON,
			DefaultLiteral: defaultLiteral,
		})
	}

	// Handle top-level oneOf (not on a property but on the type itself)
	if len(s.OneOf) > 0 && len(s.Properties) == 0 {
		oneOfDef, err := g.generateOneOfForProperty(name, "", "Value", s)
		if err != nil {
			return fmt.Errorf("top-level oneOf: %w", err)
		}
		if oneOfDef != nil {
			oneOfs = append(oneOfs, *oneOfDef)
			needsMarshal = true
			needsUnmarshal = true
		}
	}

	// Handle additionalProperties.
	// Per JSON Schema spec, absent additionalProperties defaults to true (allow any extra keys).
	// In StrictProperties mode, absent additionalProperties is treated as false (no overflow map).
	var additionalProps *AdditionalPropertiesDef
	if s.AdditionalProperties != nil {
		if s.AdditionalProperties.Bool != nil {
			if *s.AdditionalProperties.Bool {
				// additionalProperties: true → map[string]json.RawMessage
				additionalProps = &AdditionalPropertiesDef{
					ValueType: &PrimitiveType{Name: "json.RawMessage"},
				}
				needsMarshal = true
				needsUnmarshal = true
			} else {
				// additionalProperties: false → still generate overflow map to capture
				// unknown keys, but mark as forbidden so Validate() rejects them.
				additionalProps = &AdditionalPropertiesDef{
					ValueType: &PrimitiveType{Name: "json.RawMessage"},
					Forbidden: true,
				}
				needsMarshal = true
				needsUnmarshal = true
			}
		} else if s.AdditionalProperties.Schema != nil {
			valueType := g.resolveType(s.AdditionalProperties.Schema, name+"Value")
			additionalProps = &AdditionalPropertiesDef{
				ValueType: valueType,
			}
			needsMarshal = true
			needsUnmarshal = true
		}
	} else if s.UnevaluatedProperties != nil {
		// unevaluatedProperties without explicit additionalProperties:
		// need an overflow map to capture unknown keys for unevaluated checking.
		additionalProps = &AdditionalPropertiesDef{
			ValueType: &PrimitiveType{Name: "json.RawMessage"},
		}
		needsMarshal = true
		needsUnmarshal = true
	} else if !g.config.StrictProperties && (len(fields) > 0 || len(s.PatternProperties) > 0) {
		// No additionalProperties specified: per JSON Schema spec, defaults to true.
		// In non-strict mode, add an overflow map to preserve extra properties.
		// Add when there are declared fields or patternProperties (so non-pattern-matched
		// keys are preserved through round-trip).
		additionalProps = &AdditionalPropertiesDef{
			ValueType: &PrimitiveType{Name: "json.RawMessage"},
		}
		needsMarshal = true
		needsUnmarshal = true
	}

	// Collect validation rules.
	// Build maps of field metadata for filtering and annotating rules.
	fieldTypes := make(map[string]GoType)
	pointerFields := make(map[string]bool)
	for _, f := range fields {
		fieldTypes[f.Name] = f.Type
		if f.Type.IsPointer() {
			pointerFields[f.Name] = true
		}
	}
	var validations []ValidationRule

	// Collect required JSON property names for presence-based validation.
	// These are checked via the raw JSON keys during UnmarshalJSON.
	// Include both declared property fields AND schema-level required names
	// without corresponding properties (e.g., {"type":"object","required":["foo"]}
	// with no properties — these land in AdditionalProperties but must be present).
	var requiredJSON []string
	declaredProps := make(map[string]bool, len(propNames))
	for _, pn := range propNames {
		declaredProps[pn] = true
	}
	for _, f := range fields {
		if f.Required {
			requiredJSON = append(requiredJSON, f.JSONName)
		}
	}
	for i := range oneOfs {
		if oneOfs[i].Required && oneOfs[i].JSONName != "" {
			requiredJSON = append(requiredJSON, oneOfs[i].JSONName)
		}
	}
	for _, r := range requiredList {
		if !declaredProps[r] {
			// Required name not declared as a property — still needs presence check.
			requiredJSON = append(requiredJSON, r)
		}
	}

	for _, propName := range propNames {
		propSchema := s.Properties[propName]
		if propSchema == nil {
			continue
		}
		goFieldName := goFieldNames[propName]
		// Boolean schema false → property is forbidden (any value is invalid).
		// Also check if a $ref/$dynamicRef resolves to a false boolean schema.
		if propSchema.IsFalseSchema() || g.resolvedToFalseSchema(propSchema) {
			validations = append(validations, ValidationRule{
				FieldName: goFieldName, JSONName: propName,
				RuleType: "forbidden", Value: true,
			})
			continue
		}
		// In draft3-7, $ref overrides all sibling keywords — skip validation
		// rules from the property schema when it has a $ref.
		if propSchema.EffectiveRef() != "" && g.refOverridesSiblings() {
			continue
		}
		var rules []ValidationRule
		if g.validationKeywordsEnabled() {
			rules = extractValidationRules(goFieldName, propName, propSchema)
			// Also apply constraints from patternProperties whose pattern matches this property name.
			for pattern, patSchema := range s.PatternProperties {
				if re, err := regexp.Compile(pattern); err == nil && re.MatchString(propName) {
					rules = append(rules, extractValidationRules(goFieldName, propName, patSchema)...)
				}
			}
		}
		// Filter out rules that don't make sense for the Go type (e.g.,
		// minimum/maximum on an 'any' field can't be compiled).
		filtered := rules[:0]
		for i := range rules {
			if pointerFields[rules[i].FieldName] {
				rules[i].IsPointer = true
			}
			// Skip numeric/string/array validation on untyped 'any' fields,
			// but keep structural rules like "forbidden" that apply to all types.
			if ft, ok := fieldTypes[rules[i].FieldName]; ok {
				if pt, isPrim := ft.(*PrimitiveType); isPrim && pt.Name == "any" && rules[i].RuleType != "forbidden" {
					continue
				}
			}
			filtered = append(filtered, rules[i])
		}
		// Mark rules as optional when the property is not required.
		// JSON Schema says constraints only apply to present values.
		if !requiredSet[propName] {
			for j := range filtered {
				filtered[j].Optional = true
			}
		}
		validations = append(validations, filtered...)
	}

	// Enable custom marshal/unmarshal if any field has a JSON name that
	// cannot be represented in struct tags (control chars, quotes, etc.).
	for _, f := range fields {
		if f.ManualJSON {
			needsMarshal = true
			needsUnmarshal = true
			break
		}
	}

	// Collect patternProperties patterns.
	// These are regex patterns that match additional property keys which should
	// be preserved through round-trip even when additionalProperties is false.
	var patternProps []PatternPropertyDef
	for _, pattern := range sortedKeys(s.PatternProperties) {
		ppSchema := s.PatternProperties[pattern]
		ppDef := PatternPropertyDef{Pattern: pattern}
		if ppSchema.IsFalseSchema() {
			ppDef.IsForbidden = true
		} else if ppSchema.IsBooleanSchema() {
			// boolean true → no constraints
		} else if g.validationKeywordsEnabled() {
			ppDef.Validations = extractPatternPropertyValidationRules(ppSchema)
		}
		patternProps = append(patternProps, ppDef)
	}
	if len(patternProps) > 0 {
		needsMarshal = true
		needsUnmarshal = true
	}

	// Add struct-level property count validations.
	if g.validationKeywordsEnabled() && s.MaxProperties != nil {
		validations = append(validations, ValidationRule{
			RuleType: "maxProperties", Value: s.MaxProperties.Int(),
		})
	}
	if g.validationKeywordsEnabled() && s.MinProperties != nil {
		validations = append(validations, ValidationRule{
			RuleType: "minProperties", Value: s.MinProperties.Int(),
		})
	}

	// Extract dependent schema constraints.
	var depSchemas []DependentSchemaConstraint
	if g.validationKeywordsEnabled() {
		depSchemas = extractDependentSchemaConstraints(s)
	}
	if len(depSchemas) > 0 {
		needsUnmarshal = true // need to capture _jsonKeys
	}

	// Extract dependentRequired constraints.
	var depRequired []DependentRequiredDef
	if g.validationKeywordsEnabled() {
		for trigger, deps := range s.DependentRequired {
			if len(deps) > 0 {
				sorted := make([]string, len(deps))
				copy(sorted, deps)
				sort.Strings(sorted)
				depRequired = append(depRequired, DependentRequiredDef{
					TriggerKey: trigger,
					Required:   sorted,
				})
			}
		}
	}
	sort.Slice(depRequired, func(i, j int) bool {
		return depRequired[i].TriggerKey < depRequired[j].TriggerKey
	})
	if len(depRequired) > 0 {
		needsUnmarshal = true // need to capture _jsonKeys
	}

	// Enable custom unmarshal if there are optional field validations (to track key presence).
	for _, v := range validations {
		if v.Optional {
			needsUnmarshal = true
			break
		}
	}

	// Enable custom unmarshal if there are required fields (to track key presence).
	if len(requiredJSON) > 0 {
		needsUnmarshal = true
	}

	needsNullCheck := !schemaAllowsNull(s)
	if needsNullCheck {
		needsUnmarshal = true
	}

	// When the caller flags acceptNonObject and the schema has no explicit
	// "type":"object", non-object JSON data is silently accepted.
	acceptNonObj := acceptNonObject && !schemaHasExplicitType(s, "object")
	if acceptNonObj {
		needsUnmarshal = true
		needsMarshal = true // must preserve raw non-object data for roundtrip
	}

	// Extract non-object validation rules from the schema itself (e.g.,
	// minimum/maximum on a schema that has both properties and numeric constraints).
	// These are checked against _rawNonObject when the data is not an object.
	var nonObjRules []ValidationRule
	if acceptNonObj && g.validationKeywordsEnabled() {
		nonObjRules = extractNonObjectValidationRules(s)
	}

	// Build unevaluatedProperties constraint if present.
	var unevalProps *UnevaluatedPropertiesDef
	if s.UnevaluatedProperties != nil {
		unevalProps = g.buildUnevaluatedPropertiesDef(s)
	}

	// Detect cousin isolation: allOf/anyOf sub-schemas with their own
	// unevaluatedProperties need separate validation scoped to their branch.
	cousinChecks := g.collectCousinUnevalChecks(s)
	if len(cousinChecks) > 0 {
		// Cousin checks need an overflow map and _jsonKeys.
		if additionalProps == nil {
			additionalProps = &AdditionalPropertiesDef{
				ValueType: &PrimitiveType{Name: "json.RawMessage"},
			}
			needsMarshal = true
			needsUnmarshal = true
		}
	}

	// Extract propertyNames constraint.
	var propertyNamesDef *PropertyNamesDef
	if s.PropertyNames != nil && g.validationKeywordsEnabled() {
		propertyNamesDef = extractPropertyNamesDef(s.PropertyNames)
		if propertyNamesDef != nil {
			needsUnmarshal = true // need _jsonKeys for validation
		}
	}

	structDef := &StructDef{
		Name:                  name,
		Description:           s.Description,
		Fields:                fields,
		OneOfs:                oneOfs,
		AdditionalProperties:  additionalProps,
		PatternProperties:     patternProps,
		DependentSchemas:      depSchemas,
		DependentRequired:     depRequired,
		PropertyNames:         propertyNamesDef,
		Validations:           validations,
		NonObjectValidations:  nonObjRules,
		UnevaluatedProperties: unevalProps,
		CousinUnevalChecks:    cousinChecks,
		RequiredJSON:          requiredJSON,
		NeedsMarshal:          needsMarshal,
		NeedsUnmarshal:        needsUnmarshal,
		NeedsNullCheck:        needsNullCheck,
		AcceptNonObject:       acceptNonObj,
	}
	g.output.TypeDefs = append(g.output.TypeDefs, structDef)
	return nil
}

// generateAllOfDef merges all allOf sub-schemas into a single struct.
// When no sub-schema contributes properties, it generates an alias type
// instead of an empty struct, using the inferred type from constraints.
func (g *Generator) generateAllOfDef(name string, s *schema.Schema) error {
	// If any allOf sub-schema is boolean false, nothing can satisfy all constraints.
	// Generate a forbidden type (NotSchemaDef).
	if allOfContainsFalseSchema(s.AllOf) {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, &NotSchemaDef{
			Name:        name,
			Description: s.Description,
			IsForbidden: true,
		})
		return nil
	}

	// Merge all properties and required fields from allOf sub-schemas.
	merged := &schema.Schema{
		Title:       s.Title,
		Description: s.Description,
		Properties:  make(map[string]*schema.Schema),
	}

	// Copy any properties from the parent schema itself.
	for k, v := range s.Properties {
		merged.Properties[k] = v
	}
	if g.validationKeywordsEnabled() {
		merged.Required = append(merged.Required, s.Required...)
	}

	// Merge each allOf sub-schema, recursively flattening nested allOf chains.
	g.mergeAllOfInto(merged, s.AllOf)

	// Propagate keywords from the parent schema that aren't merged by allOf logic.
	if s.AdditionalProperties != nil && merged.AdditionalProperties == nil {
		merged.AdditionalProperties = s.AdditionalProperties
	}
	if s.UnevaluatedProperties != nil && merged.UnevaluatedProperties == nil {
		merged.UnevaluatedProperties = s.UnevaluatedProperties
	}
	for k, v := range s.PatternProperties {
		if merged.PatternProperties == nil {
			merged.PatternProperties = make(map[string]*schema.Schema)
		}
		if _, exists := merged.PatternProperties[k]; !exists {
			merged.PatternProperties[k] = v
		}
	}
	// Preserve allOf on the merged schema so that collectEvaluatedProperties
	// can walk the original allOf branches to find evaluated property names
	// and patterns (since mergeAllOfInto only copies properties/required/constraints,
	// not patternProperties or additionalProperties from sub-schemas).
	if len(s.AllOf) > 0 && len(merged.AllOf) == 0 {
		merged.AllOf = s.AllOf
	}
	// Propagate $ref from parent for unevaluatedProperties evaluation.
	if s.Ref != "" && merged.Ref == "" {
		merged.Ref = s.Ref
		// Also copy BaseURI and DocumentRoot so ref resolution works on merged schema.
		if merged.BaseURI == nil && s.BaseURI != nil {
			merged.BaseURI = s.BaseURI
		}
		if merged.DocumentRoot == nil && s.DocumentRoot != nil {
			merged.DocumentRoot = s.DocumentRoot
		}
	}
	// Propagate anyOf/oneOf/if-then-else from parent for unevaluatedProperties evaluation.
	if len(s.AnyOf) > 0 && len(merged.AnyOf) == 0 {
		merged.AnyOf = s.AnyOf
	}
	if len(s.OneOf) > 0 && len(merged.OneOf) == 0 {
		merged.OneOf = s.OneOf
	}
	if s.If != nil && merged.If == nil {
		merged.If = s.If
	}
	if s.Then != nil && merged.Then == nil {
		merged.Then = s.Then
	}
	if s.Else != nil && merged.Else == nil {
		merged.Else = s.Else
	}
	if g.validationKeywordsEnabled() && len(s.DependentSchemas) > 0 && len(merged.DependentSchemas) == 0 {
		merged.DependentSchemas = s.DependentSchemas
	}
	// Propagate array-structural keywords from parent schema.
	// Per JSON Schema spec, items/additionalItems scoping is per-schema (they don't
	// cross into applicator sub-schemas like allOf). So the parent's items applies
	// independently and must be preserved on the merged schema for type inference
	// and validation extraction.
	if s.Items != nil && merged.Items == nil {
		merged.Items = s.Items
	}
	if len(s.PrefixItems) > 0 && len(merged.PrefixItems) == 0 {
		merged.PrefixItems = s.PrefixItems
	}
	if s.Contains != nil && merged.Contains == nil {
		merged.Contains = s.Contains
	}
	if s.MinContains != nil && merged.MinContains == nil {
		merged.MinContains = s.MinContains
	}
	if s.MaxContains != nil && merged.MaxContains == nil {
		merged.MaxContains = s.MaxContains
	}
	if s.AdditionalItems != nil && merged.AdditionalItems == nil {
		merged.AdditionalItems = s.AdditionalItems
	}
	if s.UnevaluatedItems != nil && merged.UnevaluatedItems == nil {
		merged.UnevaluatedItems = s.UnevaluatedItems
	}

	// If no sub-schema contributed properties, don't generate an empty struct.
	// Instead, infer the type from constraints and generate an alias.
	if len(merged.Properties) == 0 {
		// Check for type-only merged result (null-only or multi-type like ["integer","string"]).
		// These don't map to a single Go type, so use TypeOnlySchemaDef.
		// We check the merged type directly rather than calling extractTypeOnlySchemaDef,
		// because the merged schema may have allOf preserved for other purposes.
		if len(merged.Type) > 0 {
			pt := primarySchemaType(merged)
			if pt == "null" || (pt == "" && len(merged.Type) > 1) {
				g.generated[name] = true
				g.output.TypeDefs = append(g.output.TypeDefs, &TypeOnlySchemaDef{
					Name:         name,
					Description:  s.Description,
					AllowedTypes: merged.Type,
					TypeBranches: extractTypeSchemaBranches(merged.TypeSchemas),
				})
				return nil
			}
		}

		primaryType := primarySchemaType(merged)
		if primaryType == "" {
			primaryType = g.inferTypeFromConstraints(merged)
		}
		if primaryType == "array" {
			// Array type — extract item-level constraints and generate InferredAliasDef
			// so that per-element validation works (items, prefixItems, contains, etc.).
			goType := g.resolveType(merged, name)
			var rules []ValidationRule
			var anyOfVariants [][]ValidationRule
			var oneOfVariants [][]ValidationRule
			if g.validationKeywordsEnabled() {
				rules = extractAliasValidationRules(merged, goType)
				anyOfVariants = extractAnyOfVariantRules(s, goType)
				oneOfVariants = extractOneOfVariantRules(s, goType)
			}
			g.generated[name] = true
			itemsFalse, itemsType, itemsTypeName, itemsChecks, itemsNested, tupleItems, addlItemsFalse, addlItemsType := g.extractInferredItemConstraints(merged, name)
			containsDef, minContains, maxContains := extractContainsDef(merged)
			unevalItems := g.buildUnevaluatedItemsDef(merged)
			if !g.validationKeywordsEnabled() {
				itemsFalse = false
				itemsType = ""
				itemsTypeName = ""
				itemsChecks = nil
				itemsNested = nil
				tupleItems = nil
				addlItemsFalse = false
				addlItemsType = ""
				containsDef = nil
				minContains = nil
				maxContains = nil
				unevalItems = nil
			}
			inferredGoType := goType
			if itemsFalse || itemsType != "" || itemsTypeName != "" ||
				len(itemsChecks) > 0 || itemsNested != nil ||
				len(tupleItems) > 0 || addlItemsFalse || addlItemsType != "" ||
				containsDef != nil || unevalItems != nil {
				inferredGoType = &ArrayType{ItemType: &PrimitiveType{Name: "any"}}
			}
			g.output.TypeDefs = append(g.output.TypeDefs, &InferredAliasDef{
				Name:                 name,
				Description:          s.Description,
				InferredGoType:       inferredGoType,
				InferredJSONType:     primaryType,
				Validations:          rules,
				AnyOfVariants:        anyOfVariants,
				OneOfVariants:        oneOfVariants,
				NeedsNullCheck:       !schemaAllowsNull(merged),
				ItemsFalse:           itemsFalse,
				ItemsType:            itemsType,
				ItemsTypeName:        itemsTypeName,
				ItemsChecks:          itemsChecks,
				ItemsNested:          itemsNested,
				TupleItems:           tupleItems,
				AdditionalItemsFalse: addlItemsFalse,
				AdditionalItemsType:  addlItemsType,
				Contains:             containsDef,
				MinContains:          minContains,
				MaxContains:          maxContains,
				UnevaluatedItems:     unevalItems,
			})
			return nil
		}
		if primaryType != "" && primaryType != "object" {
			goType := g.resolveType(merged, name)
			var rules []ValidationRule
			// Carry through anyOf/oneOf variant rules from the parent schema,
			// since these are siblings of allOf and must also be validated.
			var anyOfVariants [][]ValidationRule
			var oneOfVariants [][]ValidationRule
			if g.validationKeywordsEnabled() {
				rules = extractAliasValidationRules(merged, goType)
				anyOfVariants = extractAnyOfVariantRules(s, goType)
				oneOfVariants = extractOneOfVariantRules(s, goType)
			}
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
				Name:           name,
				Underlying:     goType,
				Description:    s.Description,
				Validations:    rules,
				AnyOfVariants:  anyOfVariants,
				OneOfVariants:  oneOfVariants,
				NeedsNullCheck: !schemaAllowsNull(merged),
			})
			return nil
		}
		// No type inferrable → alias to `any` (permissive fallback).
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
			Name:        name,
			Underlying:  &PrimitiveType{Name: "any"},
			Description: s.Description,
		})
		return nil
	}

	// allOf is type-agnostic: don't silently accept non-object data.
	if err := g.generateStructDef(name, merged, false); err != nil {
		return err
	}

	// Per JSON Schema spec, additionalProperties only considers properties defined
	// directly on the same schema — NOT properties from allOf/anyOf sub-schemas.
	// When the parent has additionalProperties and allOf contributed extra properties,
	// record the parent's own property names so the unmarshal template routes
	// allOf-contributed properties into the additionalProperties overflow map.
	if s.AdditionalProperties != nil && len(s.Properties) < len(merged.Properties) {
		ownNames := make([]string, 0, len(s.Properties))
		for k := range s.Properties {
			ownNames = append(ownNames, k)
		}
		sort.Strings(ownNames)
		// Find the StructDef we just appended and set OwnPropertyNames.
		if last := g.output.TypeDefs[len(g.output.TypeDefs)-1]; last.TypeName() == name {
			if sd, ok := last.(*StructDef); ok {
				sd.OwnPropertyNames = ownNames
			}
		}
	}
	return nil
}

// mergeAllOfInto recursively merges properties, required fields, and validation
// constraints from allOf sub-schemas into the target schema. This handles cases
// like remote schemas that themselves contain allOf with internal $ref chains.
func (g *Generator) mergeAllOfInto(target *schema.Schema, allOf []*schema.Schema) {
	for _, sub := range allOf {
		resolved := sub
		// Follow $ref chains until we reach a schema with properties or no more refs.
		var pushedCount int
		for {
			effRef := resolved.EffectiveRef()
			if effRef == "" {
				break
			}
			r := g.resolveRefInContext(effRef, resolved)
			if r == nil {
				break
			}
			if g.pushDynamicScope(r) {
				pushedCount++
			}
			// If the resolved schema has structural content (properties, array keywords,
			// allOf, etc.), stop following $ref and use this schema.
			if len(r.Properties) > 0 || len(r.PatternProperties) > 0 || len(r.AllOf) > 0 ||
				r.Items != nil || len(r.PrefixItems) > 0 || r.Contains != nil || r.AdditionalItems != nil {
				resolved = r
				break
			}
			// The resolved schema has no direct properties — it may itself
			// be a $ref-only schema; follow it.
			resolved = r
		}
		// Copy direct properties.
		for k, v := range resolved.Properties {
			target.Properties[k] = v
		}
		target.Required = append(target.Required, resolved.Required...)
		// Merge patternProperties from allOf sub-schemas.
		for k, v := range resolved.PatternProperties {
			if target.PatternProperties == nil {
				target.PatternProperties = make(map[string]*schema.Schema)
			}
			if _, exists := target.PatternProperties[k]; !exists {
				target.PatternProperties[k] = v
			}
		}
		// Propagate type from sub-schemas if the target doesn't have one.
		if len(resolved.Type) > 0 && len(target.Type) == 0 {
			target.Type = resolved.Type
		}
		if g.validationKeywordsEnabled() && supportsDependentRequired(g.draftForSchema(resolved)) && len(resolved.DependentRequired) > 0 && len(target.DependentRequired) == 0 {
			target.DependentRequired = resolved.DependentRequired
		}
		if g.validationKeywordsEnabled() && supportsDependentRequired(g.draftForSchema(resolved)) && len(resolved.DependentSchemas) > 0 && len(target.DependentSchemas) == 0 {
			target.DependentSchemas = resolved.DependentSchemas
		}
		// Propagate validation constraints (use tightest / first-set-wins).
		if g.validationKeywordsEnabled() {
			mergeConstraints(target, resolved)
		}
		// NOTE: We deliberately do NOT merge array-structural keywords (items,
		// prefixItems, contains, additionalItems) from allOf sub-schemas into
		// the target. Per JSON Schema spec, items/additionalItems scoping is
		// per-schema — merging them would change the scoping semantics (e.g.,
		// parent's `items` would apply only after merged `prefixItems` instead
		// of to all elements). Parent array keywords are propagated separately
		// in generateAllOfDef after merging.
		// However, we DO infer the type from sub-schema array/object keywords
		// so that the merged schema can still generate the right Go type.
		if len(target.Type) == 0 {
			if resolved.Items != nil || len(resolved.PrefixItems) > 0 || resolved.Contains != nil || resolved.AdditionalItems != nil {
				target.Type = []string{"array"}
			}
		}
		// Recursively merge nested allOf chains.
		if len(resolved.AllOf) > 0 {
			g.mergeAllOfInto(target, resolved.AllOf)
		}
		for i := 0; i < pushedCount; i++ {
			g.popDynamicScope()
		}
	}
}

// allOfContainsFalseSchema returns true if any sub-schema in the allOf array
// is a boolean false schema. In that case, nothing can satisfy all constraints
// simultaneously, so the entire allOf is equivalent to false.
func allOfContainsFalseSchema(allOf []*schema.Schema) bool {
	for _, sub := range allOf {
		if sub.IsFalseSchema() {
			return true
		}
		// Recursively check nested allOf: {allOf: [{allOf: [false]}]}
		if len(sub.AllOf) > 0 && allOfContainsFalseSchema(sub.AllOf) {
			return true
		}
	}
	return false
}

// allSubsFalse returns true if every sub-schema in the list is a boolean false schema.
// Used for anyOf: if all variants are false, nothing can match.
func allSubsFalse(subs []*schema.Schema) bool {
	if len(subs) == 0 {
		return false
	}
	for _, sub := range subs {
		if !sub.IsFalseSchema() {
			return false
		}
	}
	return true
}

// countBooleanSchemas counts how many sub-schemas are boolean true and boolean false.
// An empty schema {} or a schema with no constraints is treated as "always true"
// for this purpose.
func countBooleanSchemas(subs []*schema.Schema) (trueCount, falseCount int) {
	for _, sub := range subs {
		if sub.IsFalseSchema() {
			falseCount++
		} else if sub.IsTrueSchema() || isAcceptAllSchema(sub) {
			trueCount++
		}
	}
	return
}

// mergeConstraints propagates validation constraint fields from src into dst,
// using "first set wins" semantics so that the earliest sub-schema's value is
// kept when multiple sub-schemas define the same constraint.
func mergeConstraints(dst, src *schema.Schema) {
	// Numeric constraints
	if dst.Minimum == nil && src.Minimum != nil {
		dst.Minimum = src.Minimum
	}
	if dst.Maximum == nil && src.Maximum != nil {
		dst.Maximum = src.Maximum
	}
	if dst.ExclusiveMinimum == nil && src.ExclusiveMinimum != nil {
		dst.ExclusiveMinimum = src.ExclusiveMinimum
	}
	if dst.ExclusiveMaximum == nil && src.ExclusiveMaximum != nil {
		dst.ExclusiveMaximum = src.ExclusiveMaximum
	}
	if dst.MultipleOf == nil && src.MultipleOf != nil {
		dst.MultipleOf = src.MultipleOf
	}
	// String constraints
	if dst.MinLength == nil && src.MinLength != nil {
		dst.MinLength = src.MinLength
	}
	if dst.MaxLength == nil && src.MaxLength != nil {
		dst.MaxLength = src.MaxLength
	}
	if dst.Pattern == nil && src.Pattern != nil {
		dst.Pattern = src.Pattern
	}
	// Array constraints
	if dst.MinItems == nil && src.MinItems != nil {
		dst.MinItems = src.MinItems
	}
	if dst.MaxItems == nil && src.MaxItems != nil {
		dst.MaxItems = src.MaxItems
	}
	if dst.UniqueItems == nil && src.UniqueItems != nil {
		dst.UniqueItems = src.UniqueItems
	}
	// Object constraints
	if dst.MinProperties == nil && src.MinProperties != nil {
		dst.MinProperties = src.MinProperties
	}
	if dst.MaxProperties == nil && src.MaxProperties != nil {
		dst.MaxProperties = src.MaxProperties
	}
}

// generateAnyOfDef merges all anyOf sub-schemas into a single struct.
// Unlike allOf (where all must match), anyOf means "at least one matches".
// We merge all properties from all variants into one struct, but no field
// is marked required (since only one variant needs to be satisfied).
func (g *Generator) generateAnyOfDef(name string, s *schema.Schema) error {
	merged := &schema.Schema{
		Title:       s.Title,
		Description: s.Description,
		Properties:  make(map[string]*schema.Schema),
	}

	// Copy any properties from the parent schema itself.
	for k, v := range s.Properties {
		merged.Properties[k] = v
	}

	// Merge each anyOf sub-schema's properties.
	for _, sub := range s.AnyOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		for k, v := range resolved.Properties {
			if _, exists := merged.Properties[k]; !exists {
				merged.Properties[k] = v
			}
		}
		// Propagate type from sub-schemas if the parent doesn't have one.
		if len(resolved.Type) > 0 && len(merged.Type) == 0 {
			merged.Type = resolved.Type
		}
	}

	// Don't propagate required — in anyOf, no field is universally required.
	// Also propagate additionalProperties from the parent if set.
	merged.AdditionalProperties = s.AdditionalProperties

	// If none of the anyOf variants contributed properties, this is a union of
	// primitives (e.g. anyOf: [{type:"null"}, {type:"string"}]). Don't generate
	// a struct — fall back to an alias to `any` so that the value can hold any
	// of the variant types.
	if len(merged.Properties) == 0 {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
			Name:        name,
			Underlying:  &PrimitiveType{Name: "any"},
			Description: s.Description,
		})
		return nil
	}

	// anyOf is type-agnostic: don't silently accept non-object data.
	return g.generateStructDef(name, merged, false)
}

// generateOneOfForProperty creates a OneOfDef for a property with oneOf variants.
// It handles the special case of oneOf with null (becomes pointer type instead).
func (g *Generator) generateOneOfForProperty(parentName, jsonName, goFieldName string, s *schema.Schema) (*OneOfDef, error) {
	// Special case: oneOf with exactly one non-null variant → pointer type.
	// e.g., oneOf: [{$ref: "#/$defs/Foo"}, {type: "null"}] → *Foo
	nonNullVariants, hasNull := g.separateNullFromOneOf(s.OneOf)
	if hasNull && len(nonNullVariants) == 1 {
		// This will be handled as a regular nullable pointer field, not a oneOf.
		return nil, nil
	}

	// Build the oneOf definition with sealed interface pattern.
	interfaceName := ToOneOfInterfaceName(parentName, goFieldName)

	var variants []OneOfVariant
	usedNames := make(map[string]int) // track name occurrences for deduplication
	for i, variant := range nonNullVariants {
		result, err := g.resolveOneOfVariant(variant, parentName, goFieldName, i)
		if err != nil {
			return nil, err
		}

		// Deduplicate variant names: if we've already seen this name, append an index.
		name := result.Name
		if count, exists := usedNames[name]; exists {
			name = fmt.Sprintf("%s%d", name, count+1)
		}
		usedNames[result.Name]++

		wrapperName := ToOneOfWrapperName(parentName, name)

		variants = append(variants, OneOfVariant{
			WrapperName:    wrapperName,
			FieldName:      name,
			Type:           result.Type,
			RequiredFields: result.RequiredFields,
		})
	}

	oneOfDef := &OneOfDef{
		InterfaceName: interfaceName,
		FieldName:     goFieldName,
		JSONName:      jsonName,
		Variants:      variants,
	}

	// Try to detect or apply a discriminator for more efficient dispatch.
	g.applyDiscriminator(oneOfDef, s, nonNullVariants)

	return oneOfDef, nil
}

// applyDiscriminator attempts to set discriminator info on a OneOfDef.
// It checks for:
// 1. Explicit OpenAPI-style "discriminator" keyword on the schema
// 2. Heuristic: all variants share a property with distinct const/enum values
func (g *Generator) applyDiscriminator(oneOfDef *OneOfDef, s *schema.Schema, variants []*schema.Schema) {
	if len(variants) < 2 {
		return
	}

	// 1. Explicit discriminator keyword
	if s.Discriminator != nil && s.Discriminator.PropertyName != "" {
		propName := s.Discriminator.PropertyName
		discMap := make(map[string]int)

		if len(s.Discriminator.Mapping) > 0 {
			// Use explicit mapping: value → $ref or type name
			for discValue, ref := range s.Discriminator.Mapping {
				// Find which variant index corresponds to this ref
				for i, variant := range variants {
					variantRef := variant.EffectiveRef()
					if variantRef == ref || refToGoName(variantRef) == refToGoName(ref) {
						discMap[discValue] = i
						oneOfDef.Variants[i].DiscriminatorValue = discValue
						break
					}
				}
			}
		} else {
			// No mapping — try to infer from const/enum in each variant's discriminator property
			g.inferDiscriminatorValues(oneOfDef, variants, propName, discMap)
		}

		if len(discMap) == len(variants) {
			// Successfully mapped all variants
			oneOfDef.DiscriminatorField = propName
			oneOfDef.DiscriminatorMap = discMap
			return
		}
	}

	// 2. Heuristic detection: find a shared property with distinct const/enum values
	g.detectHeuristicDiscriminator(oneOfDef, variants)
}

// inferDiscriminatorValues extracts discriminator values from each variant's property.
// It looks for const or single-value enum on the discriminator property.
func (g *Generator) inferDiscriminatorValues(oneOfDef *OneOfDef, variants []*schema.Schema, propName string, discMap map[string]int) {
	for i, variant := range variants {
		resolved := g.resolveVariantSchema(variant)
		if resolved == nil {
			return
		}
		propSchema, ok := resolved.Properties[propName]
		if !ok {
			return
		}
		val := extractDiscriminatorValue(propSchema)
		if val == "" {
			return
		}
		// Check for duplicate values
		if _, exists := discMap[val]; exists {
			return
		}
		discMap[val] = i
		oneOfDef.Variants[i].DiscriminatorValue = val
	}
}

// detectHeuristicDiscriminator looks for a shared property across all variants
// where each variant has a distinct const or single-value enum for that property.
func (g *Generator) detectHeuristicDiscriminator(oneOfDef *OneOfDef, variants []*schema.Schema) {
	// Collect resolved schemas for all variants
	resolvedVariants := make([]*schema.Schema, len(variants))
	for i, v := range variants {
		resolved := g.resolveVariantSchema(v)
		if resolved == nil || len(resolved.Properties) == 0 {
			return
		}
		resolvedVariants[i] = resolved
	}

	// Find candidate properties that exist in ALL variants
	firstProps := resolvedVariants[0].Properties
	for propName := range firstProps {
		allHaveConst := true
		seenValues := make(map[string]int)
		values := make([]string, len(resolvedVariants))

		for i, resolved := range resolvedVariants {
			propSchema, ok := resolved.Properties[propName]
			if !ok {
				allHaveConst = false
				break
			}
			val := extractDiscriminatorValue(propSchema)
			if val == "" {
				allHaveConst = false
				break
			}
			if _, dup := seenValues[val]; dup {
				allHaveConst = false
				break
			}
			seenValues[val] = i
			values[i] = val
		}

		if allHaveConst && len(seenValues) == len(variants) {
			// Found a valid heuristic discriminator
			oneOfDef.DiscriminatorField = propName
			oneOfDef.DiscriminatorMap = seenValues
			for i, val := range values {
				oneOfDef.Variants[i].DiscriminatorValue = val
			}
			return
		}
	}
}

// resolveVariantSchema resolves a variant schema (following $ref if needed) to get
// its concrete properties for discriminator detection.
func (g *Generator) resolveVariantSchema(variant *schema.Schema) *schema.Schema {
	if effRef := variant.EffectiveRef(); effRef != "" {
		resolved := g.resolveRefInContext(effRef, variant)
		if resolved != nil {
			return resolved
		}
		return nil
	}
	return variant
}

// extractDiscriminatorValue extracts a single string discriminator value from a property schema.
// It recognizes: {"const": "value"} or {"enum": ["single_value"]}.
func extractDiscriminatorValue(propSchema *schema.Schema) string {
	if propSchema == nil {
		return ""
	}
	// Check const
	if propSchema.Const != nil {
		if s, ok := (*propSchema.Const).(string); ok {
			return s
		}
	}
	// Check single-value enum
	if len(propSchema.Enum) == 1 {
		if s, ok := propSchema.Enum[0].(string); ok {
			return s
		}
	}
	return ""
}

// oneOfVariantResult holds the result of resolving a oneOf variant.
type oneOfVariantResult struct {
	Name           string
	Type           GoType
	RequiredFields []string
}

// resolveOneOfVariant determines the name, type, and required fields for a oneOf variant.
// The index parameter is used to disambiguate inline variants with the same structure.
func (g *Generator) resolveOneOfVariant(variant *schema.Schema, parentName, fieldName string, index int) (oneOfVariantResult, error) {
	// Boolean schemas → treat as any
	if variant.IsBooleanSchema() {
		if variant.IsTrueSchema() {
			return oneOfVariantResult{Name: "Any", Type: &PrimitiveType{Name: "any"}}, nil
		}
		// false schema — nothing matches, but include for completeness
		return oneOfVariantResult{Name: "None", Type: &PrimitiveType{Name: "any"}}, nil
	}

	// $ref / $recursiveRef / $dynamicRef variant → use the referenced type
	if effRef := variant.EffectiveRef(); effRef != "" {
		goName := refToGoName(effRef)
		refSchema := g.resolveRefInContext(effRef, variant)
		if refSchema != nil {
			goName = g.goNameForResolvedRef(effRef, refSchema, goName)
			if err := g.generateTypeDef(goName, refSchema); err != nil {
				return oneOfVariantResult{}, err
			}
			return oneOfVariantResult{
				Name:           goName,
				Type:           &NamedType{Name: goName, Pointer: true},
				RequiredFields: refSchema.Required,
			}, nil
		}
		return oneOfVariantResult{
			Name: goName,
			Type: &NamedType{Name: goName, Pointer: true},
		}, nil
	}

	// Inline object variant → create a named type, disambiguated by index
	if hasProperties(variant) {
		variantName := fmt.Sprintf("%s%sOption%d", parentName, fieldName, index)
		if variant.Title != "" {
			variantName = SchemaNameToGoName(variant.Title)
		}
		if !g.generated[variantName] {
			if err := g.generateTypeDef(variantName, variant); err != nil {
				return oneOfVariantResult{}, err
			}
		}
		return oneOfVariantResult{
			Name:           variantName,
			Type:           &NamedType{Name: variantName, Pointer: true},
			RequiredFields: variant.Required,
		}, nil
	}

	// Primitive variant
	pt := primarySchemaType(variant)
	if pt != "" {
		goType := PrimitiveTypeFromSchema(pt)
		if goType != nil {
			goName := SchemaNameToGoName(pt)
			return oneOfVariantResult{Name: goName, Type: goType}, nil
		}
	}

	// Constraint-only or empty schema — fall back to any, but preserve required fields
	// for discrimination (e.g. oneOf variants that differ only by required constraints).
	return oneOfVariantResult{Name: "Any", Type: &PrimitiveType{Name: "any"}, RequiredFields: variant.Required}, nil
}

// separateNullFromOneOf splits oneOf variants into non-null variants and a null flag.
func (g *Generator) separateNullFromOneOf(variants []*schema.Schema) ([]*schema.Schema, bool) {
	var nonNull []*schema.Schema
	hasNull := false

	for _, v := range variants {
		if len(v.Type) == 1 && v.Type[0] == "null" {
			hasNull = true
			continue
		}
		nonNull = append(nonNull, v)
	}

	return nonNull, hasNull
}

// generateEnumDef produces an EnumDef from an enum schema.
func (g *Generator) generateEnumDef(name string, s *schema.Schema) error {
	g.generated[name] = true

	// Check if the enum contains non-primitive or mixed-type values.
	// If so, generate a json.RawMessage-based "raw" enum instead of const-based.
	if isHeterogeneousEnum(s.Enum) {
		return g.generateRawEnumDef(name, s)
	}

	baseType := g.resolveBaseType(s)

	var values []EnumValue
	// First pass: compute raw constant names.
	for _, v := range s.Enum {
		constName := name + enumValueSuffix(v)
		values = append(values, EnumValue{
			Name:  constName,
			Value: v,
		})
	}
	// Second pass: deduplicate collisions by appending numeric suffix.
	nameCount := make(map[string]int, len(values))
	for _, ev := range values {
		nameCount[ev.Name]++
	}
	nameSeen := make(map[string]int, len(values))
	for i, ev := range values {
		nameSeen[ev.Name]++
		if nameCount[ev.Name] > 1 {
			values[i].Name = fmt.Sprintf("%s%d", ev.Name, nameSeen[ev.Name])
		}
	}

	g.output.TypeDefs = append(g.output.TypeDefs, &EnumDef{
		Name:        name,
		BaseType:    baseType,
		Values:      values,
		Description: s.Description,
	})
	return nil
}

// isHeterogeneousEnum returns true if the enum values contain non-primitive
// types (arrays, objects, null) or a mix of different primitive types.
// Such enums cannot be represented as Go typed constants.
func isHeterogeneousEnum(values []any) bool {
	if len(values) == 0 {
		return false
	}
	var seenType string
	for _, v := range values {
		switch v.(type) {
		case string:
			if seenType == "" {
				seenType = "string"
			} else if seenType != "string" {
				return true
			}
		case float64:
			if seenType == "" {
				seenType = "float64"
			} else if seenType != "float64" {
				return true
			}
		case bool:
			if seenType == "" {
				seenType = "bool"
			} else if seenType != "bool" {
				return true
			}
		default:
			// nil (null), []any (array), map[string]any (object)
			return true
		}
	}
	return false
}

// generateRawEnumDef generates a json.RawMessage-based enum for heterogeneous
// enum values that cannot be represented as Go typed constants.
func (g *Generator) generateRawEnumDef(name string, s *schema.Schema) error {
	var values []EnumValue
	for _, v := range s.Enum {
		constName := name + enumValueSuffix(v)
		rawBytes, err := json.Marshal(v)
		if err != nil {
			rawBytes = []byte(fmt.Sprintf("%v", v))
		}
		values = append(values, EnumValue{
			Name:    constName,
			Value:   v,
			RawJSON: string(rawBytes),
		})
	}
	// Deduplicate collision names.
	nameCount := make(map[string]int, len(values))
	for _, ev := range values {
		nameCount[ev.Name]++
	}
	nameSeen := make(map[string]int, len(values))
	for i, ev := range values {
		nameSeen[ev.Name]++
		if nameCount[ev.Name] > 1 {
			values[i].Name = fmt.Sprintf("%s%d", ev.Name, nameSeen[ev.Name])
		}
	}

	g.output.TypeDefs = append(g.output.TypeDefs, &EnumDef{
		Name:        name,
		BaseType:    &PrimitiveType{Name: "json.RawMessage"},
		Values:      values,
		Description: s.Description,
		IsRaw:       true,
	})
	return nil
}

// resolvePropertyType determines the GoType for a property schema, creating
// additional TypeDefs for nested objects. The ctxSchema is the parent schema
// that contains this property, used for scoped $ref resolution.
func (g *Generator) resolvePropertyType(s *schema.Schema, parentName, fieldName string, ctxSchema *schema.Schema) (GoType, error) {
	if s == nil {
		return &PrimitiveType{Name: "any"}, nil
	}

	// Const -> treat as single-element enum for validation purposes.
	// Only promote when no explicit type is specified (the enum is needed to
	// determine the Go type). When type IS specified, keep the natural Go type
	// and rely on the "const" validation rule for enforcement.
	if g.validationKeywordsEnabled() && len(s.Type) == 0 {
		if s.Const != nil && len(s.Enum) == 0 {
			s.Enum = []any{*s.Const}
		} else if s.ConstIsNull && s.Const == nil && len(s.Enum) == 0 {
			s.Enum = []any{nil}
		}
	}

	// Inline enum → generate enum type
	if g.validationKeywordsEnabled() && len(s.Enum) > 0 {
		enumName := parentName + fieldName
		if err := g.generateEnumDef(enumName, s); err != nil {
			return nil, err
		}
		return &NamedType{Name: enumName}, nil
	}

	// oneOf with null + single variant → pointer to the variant type
	if len(s.OneOf) > 0 {
		nonNull, hasNull := g.separateNullFromOneOf(s.OneOf)
		if hasNull && len(nonNull) == 1 {
			variant := nonNull[0]
			if effRef := variant.EffectiveRef(); effRef != "" {
				goName := refToGoName(effRef)
				if refSchema := g.resolveRefInContext(effRef, variant); refSchema != nil {
					if err := g.generateTypeDef(goName, refSchema); err != nil {
						return nil, err
					}
				}
				return &PointerType{Inner: &NamedType{Name: goName}}, nil
			}
			// Inline variant
			innerType, err := g.resolvePropertyType(variant, parentName, fieldName, ctxSchema)
			if err != nil {
				return nil, err
			}
			if !innerType.IsPointer() {
				return &PointerType{Inner: innerType}, nil
			}
			return innerType, nil
		}
		// Multi-variant oneOf is handled by generateStructDef/generateOneOfForProperty
		// and should not reach here (the caller skips it).
	}

	// anyOf with null + single variant → pointer to the variant type (same as oneOf pattern above).
	// Handles patterns like anyOf: [{type: null}, {$ref: "#"}] inside remote schemas
	// where the ref resolves to a primitive type.
	if len(s.AnyOf) > 0 {
		nonNull, hasNull := g.separateNullFromOneOf(s.AnyOf)
		if hasNull && len(nonNull) == 1 {
			variant := nonNull[0]
			// Resolve $ref if present to get the actual target schema.
			effective := variant
			if effRef := variant.EffectiveRef(); effRef != "" {
				if resolved := g.resolveRefInContext(effRef, variant); resolved != nil {
					effective = resolved
				}
			}
			innerType, err := g.resolvePropertyType(effective, parentName, fieldName, ctxSchema)
			if err != nil {
				return nil, err
			}
			if !innerType.IsPointer() {
				return &PointerType{Inner: innerType}, nil
			}
			return innerType, nil
		}
	}

	// $ref / $recursiveRef / $dynamicRef
	if effRef := s.EffectiveRef(); effRef != "" {
		// Self-references (e.g. $ref: "#" or $ref matching root $id).
		if g.isSelfRefInContext(effRef, s) {
			// Only generate *Root if the root schema is explicitly an object type
			// with properties. Otherwise the root can validate non-object values
			// (e.g. numbers, booleans) and we should use json.RawMessage.
			if g.rootIsObjectType() {
				return &PointerType{Inner: &NamedType{Name: g.rootTypeName}}, nil
			}
			return &PrimitiveType{Name: "json.RawMessage"}, nil
		}
		goName := refToGoName(effRef)
		// Ensure the referenced type gets generated.
		refSchema := g.resolveRefInContext(effRef, s)
		if refSchema != nil {
			pushed := g.pushDynamicScope(refSchema)
			goName = g.goNameForResolvedRef(effRef, refSchema, goName)
			if err := g.generateTypeDef(goName, refSchema); err != nil {
				if pushed {
					g.popDynamicScope()
				}
				return nil, err
			}
			if pushed {
				g.popDynamicScope()
			}
			// If the ref resolves to its own enclosing document root, use a pointer.
			if g.isScopedSelfRef(effRef, s, refSchema) {
				return &PointerType{Inner: &NamedType{Name: goName}}, nil
			}
		} else {
			// Ref target could not be resolved (e.g. points to an unknown keyword).
			// Fall back to any to produce compilable code.
			return &PrimitiveType{Name: "any"}, nil
		}
		return &NamedType{Name: goName}, nil
	}

	// Nullable type: ["string", "null"] → *string
	if isNullable(s) {
		inner := nonNullType(s)
		if inner == "" {
			return &PointerType{Inner: &PrimitiveType{Name: "any"}}, nil
		}
		// Nullable object with properties → pointer to named struct
		if inner == "object" && hasProperties(s) {
			nestedName := parentName + fieldName
			if err := g.generateTypeDef(nestedName, s); err != nil {
				return nil, err
			}
			return &PointerType{Inner: &NamedType{Name: nestedName}}, nil
		}
		baseType := PrimitiveTypeFromSchema(inner)
		if baseType == nil {
			baseType = &PrimitiveType{Name: "any"}
		}
		return &PointerType{Inner: baseType}, nil
	}

	// Check for format-based type mapping on string types.
	if s.Format != nil && primarySchemaType(s) == "string" {
		if goType := formatGoType(*s.Format); goType != nil {
			return goType, nil
		}
	}

	return g.resolveType(s, parentName+fieldName), nil
}

// resolveType converts a schema to a GoType, creating nested types if needed.
func (g *Generator) resolveType(s *schema.Schema, contextName string) GoType {
	if s == nil {
		return &PrimitiveType{Name: "any"}
	}

	// Inline enum
	if g.validationKeywordsEnabled() && len(s.Enum) > 0 {
		enumName := contextName
		_ = g.generateEnumDef(enumName, s)
		return &NamedType{Name: enumName}
	}

	// $ref / $recursiveRef
	if effRef := s.EffectiveRef(); effRef != "" {
		if g.isSelfRefInContext(effRef, s) {
			if g.rootIsObjectType() {
				return &PointerType{Inner: &NamedType{Name: g.rootTypeName}}
			}
			return &PrimitiveType{Name: "json.RawMessage"}
		}
		goName := refToGoName(effRef)
		if refSchema := g.resolveRefInContext(effRef, s); refSchema != nil {
			pushed := g.pushDynamicScope(refSchema)
			// If the ref resolved to a scoped document root (not the main root),
			// derive the Go name from that schema rather than the raw ref string.
			// This handles $ref: "#" inside a sub-schema with its own $id.
			goName = g.goNameForResolvedRef(effRef, refSchema, goName)
			_ = g.generateTypeDef(goName, refSchema)
			if pushed {
				g.popDynamicScope()
			}
			// If the ref resolves to its own enclosing document root, it's a
			// local self-reference within a scoped $id context. Use a pointer
			// to break the Go recursive type cycle.
			if g.isScopedSelfRef(effRef, s, refSchema) {
				return &PointerType{Inner: &NamedType{Name: goName}}
			}
		}
		return &NamedType{Name: goName}
	}

	// $dynamicRef — resolve via dynamic scope chain.
	if s.DynamicRef != "" {
		goName := refToGoName(s.DynamicRef)
		if refSchema := g.resolveDynamicRef(s.DynamicRef, s); refSchema != nil {
			goName = g.goNameForResolvedRef(s.DynamicRef, refSchema, goName)
			_ = g.generateTypeDef(goName, refSchema)
			if g.isScopedSelfRef(s.DynamicRef, s, refSchema) {
				return &PointerType{Inner: &NamedType{Name: goName}}
			}
		}
		return &NamedType{Name: goName}
	}

	primaryType := primarySchemaType(s)
	if primaryType == "" {
		primaryType = g.inferTypeFromConstraints(s)
	}

	// Nullable types
	if isNullable(s) {
		inner := nonNullType(s)
		if inner == "" {
			return &PointerType{Inner: &PrimitiveType{Name: "any"}}
		}
		if inner == "object" && hasProperties(s) {
			_ = g.generateTypeDef(contextName, s)
			return &PointerType{Inner: &NamedType{Name: contextName}}
		}
		baseType := PrimitiveTypeFromSchema(inner)
		if baseType == nil {
			baseType = &PrimitiveType{Name: "any"}
		}
		return &PointerType{Inner: baseType}
	}

	// Object with properties → nested struct (explicit type:"object" or inferred from properties keyword)
	if primaryType == "object" && hasProperties(s) {
		_ = g.generateTypeDef(contextName, s)
		return &NamedType{Name: contextName}
	}
	// Properties without explicit type → pointer to struct (nil when absent, enabling omitempty)
	if primaryType == "" && hasProperties(s) {
		_ = g.generateTypeDef(contextName, s)
		return &PointerType{Inner: &NamedType{Name: contextName}}
	}

	// allOf / anyOf-with-properties without direct properties → delegate to generateTypeDef
	// which handles allOf merging and anyOf property collection. This covers schemas like
	// {"allOf": [{"$ref": "#/definitions/inner"}]} where the ref target has properties.
	// Guard against infinite recursion: generateAllOfDef may call resolveType with a merged
	// schema that still has allOf (preserved for unevaluatedProperties evaluation).
	if !g.generating[contextName] && (g.allOfHasProperties(s) || (len(s.AnyOf) > 0 && g.anyOfHasProperties(s))) {
		g.generating[contextName] = true
		_ = g.generateTypeDef(contextName, s)
		delete(g.generating, contextName)
		if g.generated[contextName] {
			return &NamedType{Name: contextName}
		}
	}

	// Array with items
	if primaryType == "array" && s.Items != nil && s.Items.Schema != nil {
		itemType := g.resolveType(s.Items.Schema, contextName+"Item")
		return &ArrayType{ItemType: itemType}
	}

	// Primitive or default
	if primaryType != "" {
		// Check for format-based type mapping on string types.
		if primaryType == "string" && s.Format != nil {
			if goType := formatGoType(*s.Format); goType != nil {
				return goType
			}
		}
		t := PrimitiveTypeFromSchema(primaryType)
		if t != nil {
			return t
		}
	}

	return &PrimitiveType{Name: "any"}
}

// buildDocumentRoots walks the schema tree and registers every node that declares
// an $id into g.documentRoots, keyed by its canonical (fully-resolved) URI.
// This enables scoped resolution: when a subschema has $id, $ref: "#/..."
// within it resolves against that subschema, not the top-level root.
func (g *Generator) buildDocumentRoots(s *schema.Schema) {
	if s == nil || s.IsBooleanSchema() {
		return
	}
	// If this schema has a computed BaseURI and is its own DocumentRoot, register it.
	if s.BaseURI != nil && s.DocumentRoot == s {
		key := s.BaseURI.String()
		// Strip trailing fragment "#" for consistent lookups.
		key = strings.TrimSuffix(key, "#")
		g.documentRoots[key] = s
	}
	// Recurse into all child schemas.
	for _, sub := range s.Properties {
		g.buildDocumentRoots(sub)
	}
	for _, sub := range s.TypeSchemas {
		g.buildDocumentRoots(sub)
	}
	for _, sub := range s.PatternProperties {
		g.buildDocumentRoots(sub)
	}
	for _, sub := range s.Definitions {
		g.buildDocumentRoots(sub)
	}
	for _, sub := range s.Defs {
		g.buildDocumentRoots(sub)
	}
	for _, sub := range s.AllOf {
		g.buildDocumentRoots(sub)
	}
	for _, sub := range s.AnyOf {
		g.buildDocumentRoots(sub)
	}
	for _, sub := range s.OneOf {
		g.buildDocumentRoots(sub)
	}
	if s.Not != nil {
		g.buildDocumentRoots(s.Not)
	}
	if s.Items != nil && s.Items.Schema != nil {
		g.buildDocumentRoots(s.Items.Schema)
	}
	if s.Items != nil {
		for _, sub := range s.Items.Schemas {
			g.buildDocumentRoots(sub)
		}
	}
	for _, sub := range s.PrefixItems {
		g.buildDocumentRoots(sub)
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		g.buildDocumentRoots(s.AdditionalProperties.Schema)
	}
	if s.AdditionalItems != nil && s.AdditionalItems.Schema != nil {
		g.buildDocumentRoots(s.AdditionalItems.Schema)
	}
	if s.Contains != nil {
		g.buildDocumentRoots(s.Contains)
	}
	if s.If != nil {
		g.buildDocumentRoots(s.If)
	}
	if s.Then != nil {
		g.buildDocumentRoots(s.Then)
	}
	if s.Else != nil {
		g.buildDocumentRoots(s.Else)
	}
	if s.PropertyNames != nil {
		g.buildDocumentRoots(s.PropertyNames)
	}
	if s.UnevaluatedItems != nil {
		g.buildDocumentRoots(s.UnevaluatedItems)
	}
	if s.UnevaluatedProperties != nil {
		g.buildDocumentRoots(s.UnevaluatedProperties)
	}
	if s.ContentSchema != nil {
		g.buildDocumentRoots(s.ContentSchema)
	}
	for _, sub := range s.DependentSchemas {
		g.buildDocumentRoots(sub)
	}
}

// resolveRefInContext resolves a $ref string using the given context schema's
// BaseURI and DocumentRoot for scoped resolution. This handles the case where
// a subschema with $id changes the base URI and document root for fragment resolution.
func (g *Generator) resolveRefInContext(ref string, ctx *schema.Schema) *schema.Schema {
	// Determine the effective base URI and document root from context.
	ctxBase := g.baseURI
	ctxDocRoot := g.rootSchema
	if ctx != nil {
		if ctx.BaseURI != nil {
			ctxBase = ctx.BaseURI
		}
		if ctx.DocumentRoot != nil {
			ctxDocRoot = ctx.DocumentRoot
		}
	}

	// 1. Direct defs map lookup (handles #/$defs/Foo, #/definitions/Bar).
	if s, ok := g.defs[ref]; ok {
		return s
	}
	// 2. Check anchor index (handles $id-based and $anchor-based refs).
	if refPath, ok := g.anchors[ref]; ok {
		if s, ok2 := g.defs[refPath]; ok2 {
			return s
		}
	}
	// 3. For URN refs with fragments (e.g. "urn:...#something"), try the fragment as an anchor.
	if idx := strings.LastIndex(ref, "#"); idx > 0 {
		fragment := ref[idx:]
		if refPath, ok := g.anchors[fragment]; ok {
			if s, ok2 := g.defs[refPath]; ok2 {
				return s
			}
		}
	}
	// 3b. Resolve as relative URI against context base URI, then check anchors and document roots.
	if resolved := resolveRelativeURIAgainst(ref, ctxBase); resolved != "" {
		if refPath, ok := g.anchors[resolved]; ok {
			if s, ok2 := g.defs[refPath]; ok2 {
				return s
			}
		}
		// Check document roots by canonical URI.
		resolvedClean := strings.TrimSuffix(resolved, "#")
		if s, ok := g.documentRoots[resolvedClean]; ok {
			return s
		}
	}
	// 4. Fragment-only refs: use the context document root for JSON Pointer traversal.
	if strings.HasPrefix(ref, "#") && ctxDocRoot != nil {
		local := schema.NewLocalResolver(ctxDocRoot)
		if s, err := local.Resolve(ref); err == nil {
			return s
		}
	}
	// 5. Try resolving as absolute/relative URI against context base, then delegate
	//    to the external resolver. For refs with fragments (e.g., "name-defs.json#/$defs/orNull"),
	//    first check document roots for the document part, then resolve the fragment within it.
	if ctxBase != nil {
		refURL, err := url.Parse(ref)
		if err == nil {
			absURL := ctxBase.ResolveReference(refURL)
			fragment := absURL.Fragment
			docURL := *absURL
			docURL.Fragment = ""
			docKey := docURL.String()

			// Check document roots first.
			if docSchema, ok := g.documentRoots[docKey]; ok {
				if fragment != "" {
					local := schema.NewLocalResolver(docSchema)
					if s, err := local.Resolve("#" + fragment); err == nil {
						return s
					}
				} else {
					return docSchema
				}
			}

			// Try external resolver with the absolute URI.
			// When there's a fragment, first load the document root (without fragment)
			// so we can register it properly, then resolve the fragment locally.
			// This ensures ComputeBaseURIs is called on the full document, not a sub-schema.
			if g.resolver != nil {
				if fragment != "" {
					// Load the document root first.
					if docSchema, err := g.resolver.ResolveSchema(docKey, ctxBase); err == nil {
						g.registerRemoteSchema(docSchema, &docURL)
						local := schema.NewLocalResolver(docSchema)
						if resolved, err := local.Resolve("#" + fragment); err == nil {
							return resolved
						}
					}
				}
				// Fallback: try with the full URI (no fragment, or fragment resolution failed above).
				if s, err := g.resolver.ResolveSchema(absURL.String(), ctxBase); err == nil {
					g.registerRemoteSchema(s, &docURL)
					return s
				}
			}
		}
	}
	// 6. Try external resolver with the raw ref (handles absolute URIs, etc.).
	if g.resolver != nil {
		if s, err := g.resolver.ResolveSchema(ref, ctxBase); err == nil {
			// Register the remote schema so its internal $ref chains resolve.
			if refURL, parseErr := url.Parse(ref); parseErr == nil {
				frag := refURL.Fragment
				refURL.Fragment = ""
				g.registerRemoteSchema(s, refURL)
				// If there was a fragment, resolve it within the now-registered schema.
				if frag != "" {
					local := schema.NewLocalResolver(s)
					if resolved, localErr := local.ResolveSchema("#"+frag, refURL); localErr == nil {
						return resolved
					}
				}
			}
			return s
		}
	}
	return nil
}

// registerRemoteSchema computes base URIs for a remotely-resolved schema and
// indexes its $id-bearing nodes into g.documentRoots so that subsequent refs
// (including fragment-only refs like "#" within the remote document) resolve correctly.
func (g *Generator) registerRemoteSchema(s *schema.Schema, docURI *url.URL) {
	if s == nil {
		return
	}
	s.ComputeBaseURIs(docURI, s)
	g.buildDocumentRoots(s)
}

// pushDynamicScope pushes a document root onto the dynamic scope chain when
// following a $ref that crosses a document boundary. Returns true if pushed
// (caller must pop), false if the target is in the same scope or nil.
func (g *Generator) pushDynamicScope(target *schema.Schema) bool {
	if target == nil {
		return false
	}
	docRoot := target.DocumentRoot
	if docRoot == nil {
		docRoot = target
	}
	// Don't push if it's the same document root as the current top of stack.
	if len(g.dynamicScope) > 0 && g.dynamicScope[len(g.dynamicScope)-1] == docRoot {
		return false
	}
	g.dynamicScope = append(g.dynamicScope, docRoot)
	return true
}

// popDynamicScope removes the top entry from the dynamic scope chain.
func (g *Generator) popDynamicScope() {
	if len(g.dynamicScope) > 0 {
		g.dynamicScope = g.dynamicScope[:len(g.dynamicScope)-1]
	}
}

// resolveDynamicRef resolves a $dynamicRef to a schema using the dynamic scope
// chain. Per the JSON Schema 2020-12 spec:
//
//  1. Resolve the $dynamicRef to its initial target (just like $ref).
//  2. Check if the initial target schema has a $dynamicAnchor with the same name
//     as the fragment in the $dynamicRef (the "bookend").
//  3. If a bookend exists, walk the dynamic scope chain (the stack of document
//     roots entered via $ref) from outermost to innermost. The first document
//     root that contains a $dynamicAnchor with the same name wins.
//  4. If no bookend exists at the initial target, behave like a normal $ref.
func (g *Generator) resolveDynamicRef(ref string, ctx *schema.Schema) *schema.Schema {
	// Extract the fragment anchor name for dynamic scope lookup.
	var anchorName string
	if strings.HasPrefix(ref, "#") && !strings.HasPrefix(ref, "#/") {
		anchorName = ref[1:] // plain name fragment, e.g., "#items" → "items"
	} else if idx := strings.LastIndex(ref, "#"); idx > 0 {
		frag := ref[idx+1:]
		if !strings.HasPrefix(frag, "/") {
			anchorName = frag // URI with name fragment, e.g., "extended#meta" → "meta"
		}
	}

	// Step 1: Resolve the $dynamicRef to its initial target (static resolution).
	var initialTarget *schema.Schema
	ctxDocRoot := g.rootSchema
	if ctx != nil && ctx.DocumentRoot != nil {
		ctxDocRoot = ctx.DocumentRoot
	}
	if anchorName != "" && ctxDocRoot != nil {
		// Try $dynamicAnchor lookup in the local document scope first.
		initialTarget = findDynamicAnchor(ctxDocRoot, anchorName)
		if initialTarget == nil {
			// Fall back to standard $anchor resolution.
			local := schema.NewLocalResolver(ctxDocRoot)
			if s, err := local.Resolve("#" + anchorName); err == nil {
				initialTarget = s
			}
		}
	}
	if initialTarget == nil {
		// For JSON pointers, full URIs, or when local resolution failed.
		initialTarget = g.resolveRefInContext(ref, ctx)
	}
	if initialTarget == nil {
		return nil
	}

	// Step 2: Check for the bookend — does the initial target have a matching
	// $dynamicAnchor? If not, $dynamicRef behaves like a normal $ref.
	if anchorName == "" || initialTarget.DynamicAnchor != anchorName {
		return initialTarget
	}

	// Step 3: Bookend exists — walk the dynamic scope chain from outermost to
	// innermost, looking for the first document root that contains a
	// $dynamicAnchor with the same name.
	for _, docRoot := range g.dynamicScope {
		if found := findDynamicAnchor(docRoot, anchorName); found != nil {
			return found
		}
	}

	// Fallback: no override found in dynamic scope — use the bookend.
	return initialTarget
}

// findDynamicAnchor searches a schema tree for a sub-schema with the given
// $dynamicAnchor value. It respects $id scope boundaries.
func findDynamicAnchor(s *schema.Schema, anchor string) *schema.Schema {
	if s == nil || s.IsBooleanSchema() {
		return nil
	}
	if s.DynamicAnchor == anchor {
		return s
	}
	// Search child schemas, respecting $id scope boundaries.
	for _, sub := range allSubSchemas(s) {
		if sub == nil || sub.IsBooleanSchema() {
			continue
		}
		if sub.ID != "" {
			// New document scope — only check this node directly, not descendants.
			if sub.DynamicAnchor == anchor {
				return sub
			}
			continue
		}
		if found := findDynamicAnchor(sub, anchor); found != nil {
			return found
		}
	}
	return nil
}

// allSubSchemas returns all immediate sub-schemas of a schema for tree traversal.
// This is a generator-level helper (not tied to LocalResolver).
// Map-valued fields are iterated in sorted key order for determinism.
func allSubSchemas(s *schema.Schema) []*schema.Schema {
	var subs []*schema.Schema
	for _, k := range sortedKeys(s.Properties) {
		subs = append(subs, s.Properties[k])
	}
	subs = append(subs, s.TypeSchemas...)
	for _, k := range sortedKeys(s.PatternProperties) {
		subs = append(subs, s.PatternProperties[k])
	}
	for _, k := range sortedKeys(s.Defs) {
		subs = append(subs, s.Defs[k])
	}
	for _, k := range sortedKeys(s.Definitions) {
		subs = append(subs, s.Definitions[k])
	}
	subs = append(subs, s.AllOf...)
	subs = append(subs, s.AnyOf...)
	subs = append(subs, s.OneOf...)
	if s.Not != nil {
		subs = append(subs, s.Not)
	}
	if s.If != nil {
		subs = append(subs, s.If)
	}
	if s.Then != nil {
		subs = append(subs, s.Then)
	}
	if s.Else != nil {
		subs = append(subs, s.Else)
	}
	if s.Items != nil && s.Items.Schema != nil {
		subs = append(subs, s.Items.Schema)
	}
	if s.Items != nil {
		subs = append(subs, s.Items.Schemas...)
	}
	subs = append(subs, s.PrefixItems...)
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		subs = append(subs, s.AdditionalProperties.Schema)
	}
	if s.AdditionalItems != nil && s.AdditionalItems.Schema != nil {
		subs = append(subs, s.AdditionalItems.Schema)
	}
	for _, k := range sortedKeys(s.DependentSchemas) {
		subs = append(subs, s.DependentSchemas[k])
	}
	if s.Contains != nil {
		subs = append(subs, s.Contains)
	}
	if s.UnevaluatedProperties != nil {
		subs = append(subs, s.UnevaluatedProperties)
	}
	if s.UnevaluatedItems != nil {
		subs = append(subs, s.UnevaluatedItems)
	}
	return subs
}

// indexAnchors records the $id and $anchor of a definition for anchor-based resolution.
// It stores both the raw $id value and the canonicalized (resolved against base URI)
// form so that both relative and absolute lookups succeed.
//
// When a definition declares its own $id, it creates a new document scope.
// Its $anchor belongs to that scope, not the parent's, so a plain "#anchor"
// lookup from the parent scope must NOT match it. Instead, the anchor is
// registered under the $id-qualified form (e.g., "https://example.com/foo#anchor").
func (g *Generator) indexAnchors(def *schema.Schema, refPath string) {
	hasOwnScope := def.ID != "" || def.LegacyID != ""

	if def.ID != "" {
		g.anchors[def.ID] = refPath
		// Also store the canonicalized URI (resolved against base URI).
		if resolved := g.resolveRelativeURI(def.ID); resolved != "" && resolved != def.ID {
			g.anchors[resolved] = refPath
		}
	}
	if def.LegacyID != "" {
		g.anchors[def.LegacyID] = refPath
		if resolved := g.resolveRelativeURI(def.LegacyID); resolved != "" && resolved != def.LegacyID {
			g.anchors[resolved] = refPath
		}
	}
	if def.Anchor != "" {
		if hasOwnScope {
			// The anchor belongs to the $id's scope. Register it under the
			// $id-qualified URI so it can be found via "$id#anchor" but NOT
			// via a plain "#anchor" from the parent scope.
			if def.ID != "" {
				g.anchors[def.ID+"#"+def.Anchor] = refPath
				if resolved := g.resolveRelativeURI(def.ID); resolved != "" {
					g.anchors[resolved+"#"+def.Anchor] = refPath
				}
			}
			if def.LegacyID != "" {
				g.anchors[def.LegacyID+"#"+def.Anchor] = refPath
				if resolved := g.resolveRelativeURI(def.LegacyID); resolved != "" {
					g.anchors[resolved+"#"+def.Anchor] = refPath
				}
			}
		} else {
			g.anchors["#"+def.Anchor] = refPath
		}
	}
	// $dynamicAnchor is resolvable by both $ref and $dynamicRef.
	// For $ref resolution, treat it like $anchor.
	// Also track separately for $dynamicRef-specific resolution.
	if def.DynamicAnchor != "" {
		if hasOwnScope {
			if def.ID != "" {
				g.anchors[def.ID+"#"+def.DynamicAnchor] = refPath
				if resolved := g.resolveRelativeURI(def.ID); resolved != "" {
					g.anchors[resolved+"#"+def.DynamicAnchor] = refPath
				}
			}
		} else {
			g.anchors["#"+def.DynamicAnchor] = refPath
		}
		if g.dynamicAnchors != nil {
			g.dynamicAnchors["#"+def.DynamicAnchor] = refPath
		}
	}
}

// resolvedToFalseSchema checks if a property schema's $ref, $dynamicRef, or
// $recursiveRef resolves to a boolean false schema ("always invalid").
func (g *Generator) resolvedToFalseSchema(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	// Check $ref / $recursiveRef.
	if effRef := s.EffectiveRef(); effRef != "" {
		if resolved := g.resolveRefInContext(effRef, s); resolved != nil {
			return resolved.IsFalseSchema()
		}
	}
	// Check $dynamicRef.
	if s.DynamicRef != "" {
		if resolved := g.resolveDynamicRef(s.DynamicRef, s); resolved != nil {
			return resolved.IsFalseSchema()
		}
	}
	return false
}

// isScopedSelfRef returns true if the given ref, resolved from the context schema,
// points to the context schema's own document root (creating a recursive cycle).
// This is used to detect cases like $ref: "#" inside a sub-schema with $id
// that should generate a pointer type to break Go's recursive type restriction.
func (g *Generator) isScopedSelfRef(ref string, ctx *schema.Schema, resolved *schema.Schema) bool {
	if ctx == nil || resolved == nil {
		return false
	}
	// If the resolved schema is the context's own document root, it's a scoped self-ref.
	if ctx.DocumentRoot != nil && resolved == ctx.DocumentRoot {
		return true
	}
	// If the resolved schema IS its own document root (has $id) and appears in the
	// dynamic scope, we're inside its generation chain and using it as a value type
	// would create a recursive type cycle. Use a pointer to break the cycle.
	// Only applies to schemas that define their own scope ($id) — not schemas
	// merely defined within the root document.
	if resolved.DocumentRoot == resolved && resolved.ID != "" {
		for _, scope := range g.dynamicScope {
			if scope == resolved {
				return true
			}
		}
	}
	// Check if the resolved type has already been generated as a struct that
	// references a type currently being built. This detects indirect cycles
	// like A → B → A where A is already generated and B is being built.
	if len(g.structsInProgress) > 0 {
		goName := g.goNameForResolvedRef(ref, resolved, refToGoName(ref))
		if g.generated[goName] && g.typeReferencesAnyInProgress(goName) {
			return true
		}
	}
	return false
}

// typeReferencesAnyInProgress checks if a generated type has any value-type field
// that references a type currently being built (in structsInProgress).
func (g *Generator) typeReferencesAnyInProgress(typeName string) bool {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() != typeName {
			continue
		}
		sd, ok := td.(*StructDef)
		if !ok {
			return false
		}
		for _, field := range sd.Fields {
			fieldTypeName := extractTypeName(field.Type)
			if fieldTypeName != "" && g.structsInProgress[fieldTypeName] {
				return true
			}
		}
		return false
	}
	return false
}

// extractTypeName returns the Go type name from a GoType, stripping pointers/slices.
// Returns "" for primitive types or complex types that can't create cycles.
func extractTypeName(t GoType) string {
	switch tt := t.(type) {
	case *NamedType:
		if tt.Pointer {
			return "" // already a pointer, can't create a cycle
		}
		return tt.Name
	case *PointerType:
		return "" // already a pointer, can't create a cycle
	case *ArrayType:
		return "" // arrays are fine
	default:
		return ""
	}
}

// goNameForResolvedRef determines the Go type name for a resolved $ref.
// If the ref is a fragment-only ref (like "#") and the resolved schema is a scoped
// document root different from the main root, the name is derived from the resolved
// schema's title or $id rather than the raw ref string. This ensures that
// "$ref: '#'" inside a sub-schema with its own $id gets a meaningful Go name
// (e.g., "Tree") rather than the default "Root".
func (g *Generator) goNameForResolvedRef(ref string, resolved *schema.Schema, fallback string) string {
	if resolved == nil {
		return fallback
	}
	// Only re-derive the name when the ref would produce a misleading name.
	// This happens primarily for fragment-only refs like "#" or "#/..." that
	// resolved to a scoped document root (not the main root).
	if resolved == g.rootSchema {
		return fallback
	}
	// Check if the resolved schema is a known document root with its own $id.
	if resolved.DocumentRoot == resolved {
		// Use the title if available.
		if resolved.Title != "" {
			return SchemaNameToGoName(resolved.Title)
		}
		// Use the last segment of the $id.
		schemaID := resolved.ID
		if schemaID == "" {
			schemaID = resolved.LegacyID
		}
		if schemaID != "" {
			return SchemaNameToGoName(lastPathSegment(schemaID))
		}
	}
	return fallback
}

// lastPathSegment extracts the last meaningful segment from a URI path.
// e.g., "http://example.com/foo/bar" → "bar", "./tree" → "tree",
// "baseUriChangeFolder/" → "baseUriChangeFolder"
func lastPathSegment(uri string) string {
	// Strip fragment.
	if idx := strings.LastIndex(uri, "#"); idx >= 0 {
		uri = uri[:idx]
	}
	// Strip query.
	if idx := strings.LastIndex(uri, "?"); idx >= 0 {
		uri = uri[:idx]
	}
	// Strip trailing slash.
	uri = strings.TrimSuffix(uri, "/")
	// Get last path segment.
	if idx := strings.LastIndex(uri, "/"); idx >= 0 {
		return uri[idx+1:]
	}
	// No slash — might be scheme-less relative ref like "./tree".
	uri = strings.TrimPrefix(uri, "./")
	return uri
}

// rootIsObjectType returns true if the root schema is explicitly typed as an object
// (has type: "object"). Used to decide whether a self-reference should generate
// *Root (for object schemas) or json.RawMessage (for general schemas).
// Note: having properties alone is not sufficient — without explicit type: "object",
// the schema can validate non-object values (booleans, numbers, arrays, etc.).
func (g *Generator) rootIsObjectType() bool {
	if g.rootSchema == nil {
		return false
	}
	if primarySchemaType(g.rootSchema) == "object" {
		return true
	}
	// Schemas with properties or patternProperties are implicitly object types,
	// even without an explicit "type": "object".
	return hasProperties(g.rootSchema) || len(g.rootSchema.PatternProperties) > 0
}

// isSelfRef returns true if ref points to the root schema itself.
func (g *Generator) isSelfRef(ref string) bool {
	return g.isSelfRefInContext(ref, g.rootSchema)
}

// isSelfRefInContext returns true if ref points to the root schema itself,
// resolving relative refs against the given context schema's base URI.
func (g *Generator) isSelfRefInContext(ref string, ctx *schema.Schema) bool {
	if ref == "#" {
		// "#" in a scoped context points to the context's document root,
		// which is only the top-level root if the context IS the root or
		// the context has no $id of its own.
		if ctx != nil && ctx.DocumentRoot != nil && ctx.DocumentRoot != g.rootSchema {
			return false
		}
		return true
	}
	if g.rootID != "" && (ref == g.rootID || strings.TrimSuffix(ref, "#") == g.rootID) {
		return true
	}
	// Try resolving as a relative URI against the context's base URI.
	ctxBase := g.baseURI
	if ctx != nil && ctx.BaseURI != nil {
		ctxBase = ctx.BaseURI
	}
	if resolved := resolveRelativeURIAgainst(ref, ctxBase); resolved != "" {
		if resolved == g.rootID || strings.TrimSuffix(resolved, "#") == g.rootID {
			return true
		}
	}
	return false
}

// resolveRelativeURI resolves a relative URI against the generator's base URI.
// Returns the resolved absolute URI string, or "" if resolution is not possible.
func (g *Generator) resolveRelativeURI(ref string) string {
	return resolveRelativeURIAgainst(ref, g.baseURI)
}

// resolveRelativeURIAgainst resolves a relative URI against the given base URI.
// Returns the resolved absolute URI string, or "" if resolution is not possible.
func resolveRelativeURIAgainst(ref string, base *url.URL) string {
	if base == nil {
		return ""
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return base.ResolveReference(refURL).String()
}

// resolveRef looks up a $ref path using the root schema as context.
// This is a convenience wrapper around resolveRefInContext for callers that
// don't have a scoped context schema available.
func (g *Generator) resolveRef(ref string) *schema.Schema {
	return g.resolveRefInContext(ref, g.rootSchema)
}

// resolveBaseType determines the Go base type for an enum.
func (g *Generator) resolveBaseType(s *schema.Schema) GoType {
	pt := primarySchemaType(s)
	if pt != "" {
		t := PrimitiveTypeFromSchema(pt)
		if t != nil {
			return t
		}
	}
	// Infer from first enum value.
	if len(s.Enum) > 0 {
		switch s.Enum[0].(type) {
		case string:
			return &PrimitiveType{Name: "string"}
		case float64:
			return &PrimitiveType{Name: "float64"}
		case bool:
			return &PrimitiveType{Name: "bool"}
		}
	}
	return &PrimitiveType{Name: "string"}
}

// needsManualJSON returns true if the JSON property name contains characters
// that cannot be correctly represented in a Go struct tag (backtick-delimited
// raw string). Specifically: double quotes break tag value parsing, newlines
// break tag key:value parsing, carriage returns/form feeds are stripped
// or mishandled by the reflect.StructTag parser, and backticks terminate
// the raw string literal.
func needsManualJSON(jsonName string) bool {
	for _, r := range jsonName {
		switch r {
		case '"', '`', '\\', '\n', '\r', '\t', '\f':
			return true
		}
		// Any non-printable control character
		if r < 0x20 {
			return true
		}
	}
	return false
}

// isArrayType returns true if the GoType is a slice/array type.
func isArrayType(t GoType) bool {
	if t == nil {
		return false
	}
	_, ok := t.(*ArrayType)
	return ok
}

// isArrayProperty returns true if the Go type or the property schema indicates
// an array type. This handles both direct ArrayType and NamedType aliases to
// arrays (e.g., when the property uses $ref to an array-typed definition).
func (g *Generator) isArrayProperty(goType GoType, propSchema *schema.Schema) bool {
	if isArrayType(goType) {
		return true
	}
	// Check the property schema's type.
	if propSchema != nil {
		if primarySchemaType(propSchema) == "array" {
			return true
		}
		// Follow $ref to check the target schema.
		if effRef := propSchema.EffectiveRef(); effRef != "" {
			if resolved := g.resolveRefInContext(effRef, propSchema); resolved != nil {
				if primarySchemaType(resolved) == "array" {
					return true
				}
			}
		}
	}
	return false
}

// isNullOnly returns true if the schema's type is exclusively "null".
func isNullOnly(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	return len(s.Type) == 1 && s.Type[0] == "null"
}

// ---------- helpers ----------

// anyOfHasProperties checks whether at least one anyOf sub-schema (after resolving
// $ref pointers) contributes object properties. If none do, the anyOf is a union of
// primitives and should not be turned into a merged struct.
// Self-references ($ref: "#") are excluded because they point back to the root
// schema and don't represent actual property contributions from the anyOf variant.
// allOfHasProperties returns true if any allOf sub-schema contributes properties
// (directly or via $ref resolution). Used by resolveType to decide whether a
// schema with allOf but no direct properties should generate a struct.
func (g *Generator) allOfHasProperties(s *schema.Schema) bool {
	for _, sub := range s.AllOf {
		if len(sub.Properties) > 0 {
			return true
		}
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				if len(r.Properties) > 0 {
					return true
				}
				// Recursively check resolved schema's allOf chain.
				if g.allOfHasProperties(r) {
					return true
				}
			}
		}
		// Recursively check nested allOf.
		if g.allOfHasProperties(sub) {
			return true
		}
	}
	return false
}

func (g *Generator) anyOfHasProperties(s *schema.Schema) bool {
	for _, sub := range s.AnyOf {
		// Check direct properties on the sub-schema itself.
		if len(sub.Properties) > 0 {
			return true
		}
		// Resolve $ref, but skip self-references to avoid misattributing
		// the root schema's properties to this anyOf variant.
		if effRef := sub.EffectiveRef(); effRef != "" && !g.isSelfRefInContext(effRef, sub) {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				if len(r.Properties) > 0 {
					return true
				}
			}
		}
	}
	return false
}

// oneOfHasProperties returns true if any oneOf variant has properties.
func (g *Generator) oneOfHasProperties(s *schema.Schema) bool {
	for _, sub := range s.OneOf {
		if len(sub.Properties) > 0 {
			return true
		}
		if effRef := sub.EffectiveRef(); effRef != "" && !g.isSelfRefInContext(effRef, sub) {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				if len(r.Properties) > 0 {
					return true
				}
			}
		}
	}
	return false
}

// hasProperties returns true if the schema defines any properties.
func hasProperties(s *schema.Schema) bool {
	return len(s.Properties) > 0
}

// primarySchemaType returns the primary (first non-null) type from the type list.
// schemaAllowsNull returns true if the schema's type list includes "null"
// or if there is no explicit type (which means any type is allowed).
func schemaAllowsNull(s *schema.Schema) bool {
	if len(s.Type) == 0 {
		return true // no explicit type constraint — any type allowed
	}
	for _, t := range s.Type {
		if t == "null" {
			return true
		}
	}
	return false
}

func primarySchemaType(s *schema.Schema) string {
	// Count distinct non-null types. If there are multiple incompatible types
	// (e.g., ["array", "object"] or ["integer", "string"]), return "" so that
	// resolveType falls back to `any` — Go can't represent a union type.
	var nonNull []string
	for _, t := range s.Type {
		if t != "null" {
			nonNull = append(nonNull, t)
		}
	}
	if len(nonNull) == 1 {
		return nonNull[0]
	}
	if len(nonNull) > 1 {
		// Multiple incompatible types — no single Go type can represent this.
		return ""
	}
	// Only "null" type(s) or empty.
	if len(s.Type) > 0 {
		return s.Type[0]
	}
	return ""
}

// inferTypeFromConstraints infers a JSON Schema type from the validation
// keywords present in a schema that has no explicit "type" field. This enables
// generating typed code (and Validate() methods) for constraint-only schemas
// like {"minimum": 5} or {"minLength": 2, "pattern": "^a"}.
//
// Returns "" if the type cannot be inferred.
func (g *Generator) inferTypeFromConstraints(s *schema.Schema) string {
	if !g.validationKeywordsEnabled() {
		if s.Items != nil || (len(s.PrefixItems) > 0 && g.supportsPrefixItems(s)) || s.AdditionalItems != nil ||
			s.Contains != nil || s.UnevaluatedItems != nil {
			return "array"
		}
		if s.AdditionalProperties != nil || s.UnevaluatedProperties != nil {
			return "object"
		}
		return ""
	}

	// Numeric constraints → number
	if s.Minimum != nil || s.Maximum != nil || s.MultipleOf != nil ||
		s.ExclusiveMinimum != nil || s.ExclusiveMaximum != nil {
		return "number"
	}
	// String constraints → string
	if s.MinLength != nil || s.MaxLength != nil || s.Pattern != nil {
		return "string"
	}
	// Array constraints → array
	if s.MinItems != nil || s.MaxItems != nil || s.UniqueItems != nil {
		return "array"
	}
	// unevaluatedItems:false with tuple items and NO sibling applicators/items that
	// could extend or evaluate additional items → safe to infer array with implicit
	// maxItems = tuple length.
	if g.unevaluatedItemsImpliesFixedTuple(s) {
		return "array"
	}
	// Structural array keywords → array
	// items, prefixItems (2020-12 only), additionalItems, contains, and unevaluatedItems
	// only apply to arrays, so their presence implies type "array".
	if s.Items != nil || (len(s.PrefixItems) > 0 && g.supportsPrefixItems(s)) || s.AdditionalItems != nil ||
		s.Contains != nil || s.UnevaluatedItems != nil {
		return "array"
	}
	// Object constraints → object
	if g.validationKeywordsEnabled() && (s.MinProperties != nil || s.MaxProperties != nil) {
		return "object"
	}
	// Structural object keywords → object
	// required, additionalProperties, dependentRequired, dependentSchemas,
	// propertyNames, and unevaluatedProperties only apply to objects.
	if s.AdditionalProperties != nil || s.UnevaluatedProperties != nil {
		return "object"
	}
	if g.validationKeywordsEnabled() && (len(s.Required) > 0 ||
		len(s.DependentRequired) > 0 || len(s.DependentSchemas) > 0 ||
		s.PropertyNames != nil) {
		return "object"
	}
	return ""
}

// unevaluatedItemsImpliesFixedTuple returns true when a schema has
// unevaluatedItems:false alongside a tuple definition (prefixItems or tuple-form
// items) and NO other applicators or keywords that could evaluate additional items.
// In this narrow case, the schema is equivalent to a fixed-length tuple with
// maxItems = tuple length.
func (g *Generator) unevaluatedItemsImpliesFixedTuple(s *schema.Schema) bool {
	if s.UnevaluatedItems == nil || !s.UnevaluatedItems.IsFalseSchema() {
		return false
	}
	tupleLen := 0
	if g.supportsPrefixItems(s) {
		tupleLen = len(s.PrefixItems)
	}
	if tupleLen == 0 && s.Items != nil {
		tupleLen = len(s.Items.Schemas)
	}
	if tupleLen == 0 {
		return false
	}
	// Bail if any applicator or keyword could extend or evaluate additional items.
	if len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 {
		return false
	}
	if s.If != nil || s.Ref != "" || s.Contains != nil {
		return false
	}
	// items as a schema (not tuple form) evaluates all remaining items — no unevaluated ones.
	if s.Items != nil && s.Items.Schema != nil {
		return false
	}
	// additionalItems evaluates items beyond the tuple — no unevaluated ones.
	if s.AdditionalItems != nil {
		return false
	}
	return true
}

func unevaluatedItemsImpliesFixedTuple(s *schema.Schema) bool {
	return (&Generator{}).unevaluatedItemsImpliesFixedTuple(s)
}

// buildUnevaluatedItemsDef builds an UnevaluatedItemsDef from a schema's unevaluatedItems keyword.
// Returns nil if the schema has no unevaluatedItems or if all items are statically evaluated.
func (g *Generator) buildUnevaluatedItemsDef(s *schema.Schema) *UnevaluatedItemsDef {
	if s.UnevaluatedItems == nil {
		return nil
	}

	ui := s.UnevaluatedItems

	// Boolean schemas
	if ui.IsBooleanSchema() {
		if ui.IsTrueSchema() {
			// unevaluatedItems: true — allow anything, no validation needed
			return nil
		}
		// unevaluatedItems: false — reject any unevaluated items
		def := &UnevaluatedItemsDef{IsForbidden: true}
		g.collectEvaluatedItems(s, def)
		// If all items are already evaluated, unevaluatedItems:false is a no-op
		if def.AllEvaluated {
			return nil
		}
		return def
	}

	// Schema-valued unevaluatedItems — validate each unevaluated item
	def := &UnevaluatedItemsDef{}

	// Extract type constraint
	if len(ui.Type) == 1 {
		def.ValueType = ui.Type[0]
	}

	// Extract simple validation checks
	def.Checks = extractUnevalItemChecks(ui)

	if def.ValueType == "" && len(def.Checks) == 0 {
		// Schema-valued but no extractable constraints (complex sub-schema)
		// Still need to reject non-matching items — treat as type check if possible
		// For now, skip (we handle the common cases)
		return nil
	}

	g.collectEvaluatedItems(s, def)
	if def.AllEvaluated {
		return nil
	}
	return def
}

// extractUnevalItemChecks extracts simple validation checks from a unevaluatedItems sub-schema.
func extractUnevalItemChecks(ui *schema.Schema) []ContainsCheck {
	var checks []ContainsCheck
	if ui.Minimum != nil {
		checks = append(checks, ContainsCheck{CheckType: "minimum", Value: *ui.Minimum})
	}
	if ui.Maximum != nil {
		checks = append(checks, ContainsCheck{CheckType: "maximum", Value: *ui.Maximum})
	}
	if ui.MultipleOf != nil {
		checks = append(checks, ContainsCheck{CheckType: "multipleOf", Value: *ui.MultipleOf})
	}
	if ui.ExclusiveMinimum != nil && ui.ExclusiveMinimum.Number != nil {
		checks = append(checks, ContainsCheck{CheckType: "exclusiveMinimum", Value: *ui.ExclusiveMinimum.Number})
	}
	if ui.ExclusiveMaximum != nil && ui.ExclusiveMaximum.Number != nil {
		checks = append(checks, ContainsCheck{CheckType: "exclusiveMaximum", Value: *ui.ExclusiveMaximum.Number})
	}
	return checks
}

// collectEvaluatedItems populates an UnevaluatedItemsDef with information about
// which array positions are "evaluated" by other keywords in the schema.
func (g *Generator) collectEvaluatedItems(s *schema.Schema, def *UnevaluatedItemsDef) {
	// 1. items as a single schema (uniform items) evaluates ALL positions
	if s.Items != nil && s.Items.Schema != nil {
		def.AllEvaluated = true
		return
	}

	// 2. prefixItems / items-as-array evaluates fixed positions
	tupleLen := len(s.PrefixItems)
	if tupleLen == 0 && s.Items != nil {
		tupleLen = len(s.Items.Schemas)
	}
	def.EvaluatedCount = tupleLen

	// 3. additionalItems evaluates positions beyond the tuple only when tuple
	// items exist. Without tuple items, additionalItems is ignored by the spec.
	if tupleLen > 0 && s.AdditionalItems != nil && !(s.AdditionalItems.Bool != nil && !*s.AdditionalItems.Bool) {
		// additionalItems is present and is NOT false — it evaluates all remaining items
		def.AllEvaluated = true
		return
	}

	// 4. contains in Draft 2020-12 evaluates matching items at runtime.
	// Since we cannot determine which items will match contains at compile time,
	// we set ContainsEvaluates so the template generates per-item runtime checks
	// that integrate contains matching with unevaluatedItems validation.
	if s.Contains != nil {
		def.ContainsEvaluates = true
	}

	// 5. if (without then/else) evaluates items via its prefixItems/items annotations
	// only when the if condition holds at runtime. Since we cannot evaluate the if
	// condition at compile time, we cannot statically determine which items are
	// evaluated. The if/then/else block below will add a conditional eval entry,
	// but without then/else, both branches have 0 evaluated — which is correct
	// for the case where if doesn't match. When if DOES match, we'd need runtime
	// evaluation (added as known limitation / known-failure).

	// 6. Walk applicators: allOf, $ref, anyOf, oneOf, if/then/else
	if s.Ref != "" {
		refSchema := g.resolveRefInContext(s.Ref, s)
		if refSchema != nil {
			evalCount, allEval := g.countEvaluatedItemsInSchema(refSchema)
			if allEval {
				def.AllEvaluated = true
				return
			}
			if evalCount > def.EvaluatedCount {
				def.EvaluatedCount = evalCount
			}
		}
	}

	for _, sub := range s.AllOf {
		resolved := sub
		if sub.Ref != "" {
			if r := g.resolveRefInContext(sub.Ref, sub); r != nil {
				resolved = r
			}
		}
		evalCount, allEval := g.countEvaluatedItemsInSchema(resolved)
		if allEval {
			def.AllEvaluated = true
			return
		}
		if evalCount > def.EvaluatedCount {
			def.EvaluatedCount = evalCount
		}
	}

	// anyOf/oneOf: runtime-conditional — the maximum of evaluated counts across branches
	if len(s.AnyOf) > 0 {
		ce := UnevalItemsConditionalEval{Kind: "anyOf"}
		for _, sub := range s.AnyOf {
			resolved := sub
			if sub.Ref != "" {
				if r := g.resolveRefInContext(sub.Ref, sub); r != nil {
					resolved = r
				}
			}
			evalCount, allEval := g.countEvaluatedItemsInSchema(resolved)
			ce.Branches = append(ce.Branches, UnevalItemsBranch{
				EvaluatedCount: evalCount,
				AllEvaluated:   allEval,
			})
		}
		def.ConditionalEvals = append(def.ConditionalEvals, ce)
	}

	if len(s.OneOf) > 0 {
		ce := UnevalItemsConditionalEval{Kind: "oneOf"}
		for _, sub := range s.OneOf {
			resolved := sub
			if sub.Ref != "" {
				if r := g.resolveRefInContext(sub.Ref, sub); r != nil {
					resolved = r
				}
			}
			evalCount, allEval := g.countEvaluatedItemsInSchema(resolved)
			ce.Branches = append(ce.Branches, UnevalItemsBranch{
				EvaluatedCount: evalCount,
				AllEvaluated:   allEval,
			})
		}
		def.ConditionalEvals = append(def.ConditionalEvals, ce)
	}

	// if/then/else
	if s.If != nil {
		ce := UnevalItemsConditionalEval{Kind: "ifThenElse"}
		// Count items evaluated by the if-schema itself (its own annotations).
		ifEvalCount, ifAllEval := g.countEvaluatedItemsInSchema(s.If)
		ce.IfEvalCount = ifEvalCount
		ce.IfAllEval = ifAllEval
		// Extract runtime if-condition checks from the if-schema's prefixItems const values.
		ce.IfItemChecks = g.extractIfItemConstChecks(s.If)
		if s.Then != nil {
			resolved := s.Then
			if s.Then.Ref != "" {
				if r := g.resolveRefInContext(s.Then.Ref, s.Then); r != nil {
					resolved = r
				}
			}
			evalCount, allEval := g.countEvaluatedItemsInSchema(resolved)
			ce.ThenEvalCount = evalCount
			ce.ThenAllEval = allEval
		}
		if s.Else != nil {
			resolved := s.Else
			if s.Else.Ref != "" {
				if r := g.resolveRefInContext(s.Else.Ref, s.Else); r != nil {
					resolved = r
				}
			}
			evalCount, allEval := g.countEvaluatedItemsInSchema(resolved)
			ce.ElseEvalCount = evalCount
			ce.ElseAllEval = allEval
		}
		def.ConditionalEvals = append(def.ConditionalEvals, ce)
	}
}

// extractIfItemConstChecks extracts const checks from an if-schema's prefixItems
// for runtime evaluation of the if-condition in unevaluatedItems validation.
// Returns checks for each prefixItems position that has a const constraint.
func (g *Generator) extractIfItemConstChecks(ifSchema *schema.Schema) []IfItemConstCheck {
	if ifSchema == nil {
		return nil
	}
	var checks []IfItemConstCheck
	// Check prefixItems (Draft 2020-12)
	for i, itemSchema := range ifSchema.PrefixItems {
		if itemSchema == nil || itemSchema.IsTrueSchema() {
			continue // boolean true — no constraint at this position
		}
		if itemSchema.Const != nil {
			b, err := json.Marshal(*itemSchema.Const)
			if err == nil {
				checks = append(checks, IfItemConstCheck{Index: i, JSONValue: string(b)})
			}
		} else if itemSchema.ConstIsNull {
			checks = append(checks, IfItemConstCheck{Index: i, JSONValue: "null"})
		}
	}
	// Check items.Schemas (Draft 7 / 2019-09 tuple form)
	if ifSchema.Items != nil {
		for i, itemSchema := range ifSchema.Items.Schemas {
			if itemSchema == nil || itemSchema.IsTrueSchema() {
				continue
			}
			if itemSchema.Const != nil {
				b, err := json.Marshal(*itemSchema.Const)
				if err == nil {
					checks = append(checks, IfItemConstCheck{Index: i, JSONValue: string(b)})
				}
			} else if itemSchema.ConstIsNull {
				checks = append(checks, IfItemConstCheck{Index: i, JSONValue: "null"})
			}
		}
	}
	return checks
}

// countEvaluatedItemsInSchema returns how many array positions are evaluated by
// a sub-schema, and whether it evaluates all positions.
func (g *Generator) countEvaluatedItemsInSchema(s *schema.Schema) (int, bool) {
	if s == nil {
		return 0, false
	}

	// Boolean true schema → evaluates nothing (no annotations produced).
	// But note: a sub-schema with unevaluatedItems:true evaluates all items.
	if s.IsTrueSchema() {
		return 0, false
	}

	// items as uniform schema → all positions evaluated
	if s.Items != nil && s.Items.Schema != nil {
		return 0, true
	}

	// unevaluatedItems: true in a sub-schema → all items are evaluated by that sub-schema
	if s.UnevaluatedItems != nil && s.UnevaluatedItems.IsTrueSchema() {
		return 0, true
	}

	// prefixItems / items-as-array
	tupleLen := len(s.PrefixItems)
	if tupleLen == 0 && s.Items != nil {
		tupleLen = len(s.Items.Schemas)
	}
	if tupleLen > 0 && s.AdditionalItems != nil && !(s.AdditionalItems.Bool != nil && !*s.AdditionalItems.Bool) {
		return 0, true
	}

	// Recurse into allOf/$ref
	maxEval := tupleLen

	if s.Ref != "" {
		refSchema := g.resolveRefInContext(s.Ref, s)
		if refSchema != nil {
			evalCount, allEval := g.countEvaluatedItemsInSchema(refSchema)
			if allEval {
				return 0, true
			}
			if evalCount > maxEval {
				maxEval = evalCount
			}
		}
	}

	for _, sub := range s.AllOf {
		resolved := sub
		if sub.Ref != "" {
			if r := g.resolveRefInContext(sub.Ref, sub); r != nil {
				resolved = r
			}
		}
		evalCount, allEval := g.countEvaluatedItemsInSchema(resolved)
		if allEval {
			return 0, true
		}
		if evalCount > maxEval {
			maxEval = evalCount
		}
	}

	return maxEval, false
}

// schemaHasExplicitType returns true if the schema declares an explicit "type"
// field that includes the given type name. When the type list is empty (no
// explicit type), the schema is permissive — it accepts any JSON value type.
func schemaHasExplicitType(s *schema.Schema, typeName string) bool {
	for _, t := range s.Type {
		if t == typeName {
			return true
		}
	}
	return false
}

// isNullable returns true if the schema's type list includes "null".
func isNullable(s *schema.Schema) bool {
	for _, t := range s.Type {
		if t == "null" {
			return true
		}
	}
	return false
}

// nonNullType returns the first type in the type list that is not "null".
func nonNullType(s *schema.Schema) string {
	for _, t := range s.Type {
		if t != "null" {
			return t
		}
	}
	return ""
}

// isNullableComposition checks if a property schema uses anyOf/oneOf with a null
// variant, indicating the resolved Go type will be a pointer. This is used to
// determine whether omitempty should be suppressed for lossless null round-tripping.
// It also follows $ref to check the target schema's composition.
func (g *Generator) isNullableComposition(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	// Direct anyOf/oneOf with null variant.
	for _, variants := range [][]*schema.Schema{s.AnyOf, s.OneOf} {
		_, hasNull := g.separateNullFromOneOf(variants)
		if hasNull {
			return true
		}
	}
	// Follow $ref to check the target.
	if effRef := s.EffectiveRef(); effRef != "" {
		if resolved := g.resolveRefInContext(effRef, s); resolved != nil {
			for _, variants := range [][]*schema.Schema{resolved.AnyOf, resolved.OneOf} {
				_, hasNull := g.separateNullFromOneOf(variants)
				if hasNull {
					return true
				}
			}
		}
	}
	return false
}

// refToGoName extracts the Go type name from a $ref string.
// It handles JSON Pointer refs, URN refs, and URI refs:
//
//	"#/$defs/my-type"       → "MyType"
//	"#/definitions/Address" → "Address"
//	"#"                     → "Root"
//	"urn:uuid:dead-beef"    → "DeadBeef" (uses last segment after last colon)
//	"#/definitions/tilde~0field" → "TildeField" (JSON Pointer unescaping)
//	"foo%22bar"             → "FooBar" (URL decoding)
func refToGoName(ref string) string {
	// Strip fragment from URIs/URNs: "urn:...#something" → use "something"
	name := ref
	if idx := strings.LastIndex(ref, "#"); idx >= 0 {
		fragment := ref[idx+1:]
		if fragment == "" {
			// Fragment-only ref "#" — use "Root" as the name.
			return "Root"
		}
		name = fragment
	}

	// For JSON Pointer paths like "/definitions/foo/bar", take the last segment.
	if strings.Contains(name, "/") {
		parts := strings.Split(name, "/")
		// Find the last non-empty segment.
		for i := len(parts) - 1; i >= 0; i-- {
			if parts[i] != "" {
				name = parts[i]
				break
			}
		}
		// If all segments are empty, use a fallback.
		if name == "" || name == ref {
			return "X"
		}
	}

	// For URN refs without fragment (e.g. "urn:uuid:deadbeef-1234"),
	// take the last colon-separated segment.
	if strings.Contains(name, ":") {
		parts := strings.Split(name, ":")
		name = parts[len(parts)-1]
	}

	// Apply JSON Pointer unescaping: ~1 → /, ~0 → ~
	name = strings.ReplaceAll(name, "~1", "/")
	name = strings.ReplaceAll(name, "~0", "~")

	// Apply URL percent-decoding.
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}

	return SchemaNameToGoName(name)
}

// enumValueSuffix returns a suffix for an enum constant name from the value.
func enumValueSuffix(v any) string {
	switch val := v.(type) {
	case string:
		return SchemaNameToGoName(val)
	case float64:
		// Sanitize numeric values for Go identifiers:
		// replace '-' with "Neg", '.' with "_", '+' with "", 'e' with "e"
		s := fmt.Sprintf("%v", val)
		s = strings.ReplaceAll(s, "-", "Neg")
		s = strings.ReplaceAll(s, ".", "_")
		s = strings.ReplaceAll(s, "+", "")
		return s
	case bool:
		if val {
			return "True"
		}
		return "False"
	case nil:
		return "Null"
	default:
		// For objects/arrays, serialize to JSON and sanitize for Go identifier.
		raw, err := json.Marshal(val)
		if err != nil {
			return "Value"
		}
		return SchemaNameToGoName(string(raw))
	}
}

// populateValidatableFields is a post-pass that identifies struct fields whose
// types have Validate() methods and adds them to StructDef.ValidatableFields.
// Must run after resolveAliasMethodability so NoMethods flags are set.
func (g *Generator) populateValidatableFields() {
	// Build set of type names that have Validate() methods.
	validatableTypes := make(map[string]bool)
	for _, td := range g.output.TypeDefs {
		switch d := td.(type) {
		case *EnumDef:
			validatableTypes[d.Name] = true
		case *StructDef:
			validatableTypes[d.Name] = true
		case *AliasDef:
			if d.CanHaveMethods() {
				validatableTypes[d.Name] = true
			}
		case *InferredAliasDef:
			validatableTypes[d.Name] = true // wrapper struct always has Validate()
		case *BigIntAliasDef:
			validatableTypes[d.Name] = true // wrapper struct always has Validate()
		}
	}

	// For each struct, check its fields.
	for _, td := range g.output.TypeDefs {
		sd, ok := td.(*StructDef)
		if !ok {
			continue
		}
		for _, f := range sd.Fields {
			// Direct named type (or pointer to named type).
			typeName := namedTypeName(f.Type)
			if typeName != "" && validatableTypes[typeName] {
				zeroLit := g.zeroLiteralForType(f.Type)
				sd.ValidatableFields = append(sd.ValidatableFields, ValidatableFieldDef{
					FieldName:   f.Name,
					JSONName:    f.JSONName,
					GoType:      f.Type,
					IsPointer:   f.Type.IsPointer(),
					OmitEmpty:   f.OmitEmpty,
					ZeroLiteral: zeroLit,
				})
				continue
			}
			// Slice of named type (or pointer to slice of named type).
			elemName := sliceElementTypeName(f.Type)
			if elemName != "" && validatableTypes[elemName] {
				sd.ValidatableFields = append(sd.ValidatableFields, ValidatableFieldDef{
					FieldName: f.Name,
					JSONName:  f.JSONName,
					GoType:    f.Type,
					IsPointer: f.Type.IsPointer(),
					IsSlice:   true,
					OmitEmpty: f.OmitEmpty,
				})
			}
		}
	}
}

func (g *Generator) populateAliasDelegates() {
	validatableTypes := make(map[string]bool)
	unmarshalTypes := make(map[string]bool)
	marshalTypes := make(map[string]bool)
	for _, td := range g.output.TypeDefs {
		switch d := td.(type) {
		case *StructDef:
			validatableTypes[d.Name] = true
			if d.NeedsUnmarshal {
				unmarshalTypes[d.Name] = true
			}
			if d.NeedsMarshal {
				marshalTypes[d.Name] = true
			}
		case *EnumDef:
			validatableTypes[d.Name] = true
		case *InferredAliasDef:
			validatableTypes[d.Name] = true
		case *BigIntAliasDef:
			validatableTypes[d.Name] = true
		case *TypeOnlySchemaDef:
			validatableTypes[d.Name] = true
		case *NotSchemaDef:
			validatableTypes[d.Name] = true
		case *AliasDef:
			if d.CanHaveMethods() {
				validatableTypes[d.Name] = true
				if d.NeedsNullCheck || d.IsIntegerType() || d.UnmarshalAs != "" {
					unmarshalTypes[d.Name] = true
				}
				if d.MarshalAs != "" {
					marshalTypes[d.Name] = true
				}
			}
		}
	}

	for _, td := range g.output.TypeDefs {
		ad, ok := td.(*AliasDef)
		if !ok || !ad.CanHaveMethods() {
			continue
		}
		name := namedTypeName(ad.Underlying)
		if name == "" || name == ad.Name {
			continue
		}
		if validatableTypes[name] {
			ad.ValidateAs = name
		}
		if unmarshalTypes[name] {
			ad.UnmarshalAs = name
		}
		if marshalTypes[name] {
			ad.MarshalAs = name
		}
	}
}

// namedTypeName extracts the type name from a GoType if it's a NamedType
// (possibly wrapped in a PointerType). Returns "" otherwise.
func namedTypeName(t GoType) string {
	switch v := t.(type) {
	case *NamedType:
		return v.Name
	case *PointerType:
		return namedTypeName(v.Inner)
	default:
		return ""
	}
}

// sliceElementTypeName extracts the element type name from a slice GoType.
// Handles []T, *[]T, []*T, *[]*T where T is a NamedType.
func sliceElementTypeName(t GoType) string {
	inner := t
	if pt, ok := inner.(*PointerType); ok {
		inner = pt.Inner
	}
	st, ok := inner.(*ArrayType)
	if !ok {
		return ""
	}
	return namedTypeName(st.ItemType)
}

// zeroLiteralForType returns the Go zero value literal for a given type.
// For named types, it looks up the generated type definition to find the underlying type.
func (g *Generator) zeroLiteralForType(t GoType) string {
	switch v := t.(type) {
	case *PointerType:
		return "nil"
	case *PrimitiveType:
		return zeroForPrimitive(v.Name)
	case *NamedType:
		// Look up the generated type to find the underlying type.
		for _, td := range g.output.TypeDefs {
			if td.TypeName() == v.Name {
				switch d := td.(type) {
				case *EnumDef:
					return zeroForPrimitive(d.BaseType.GoTypeName())
				case *AliasDef:
					return zeroForPrimitive(d.Underlying.GoTypeName())
				case *StructDef:
					// Structs don't have a meaningful zero literal for comparison.
					return ""
				case *InferredAliasDef:
					// InferredAliasDef is a wrapper struct — no meaningful zero literal.
					return ""
				case *BigIntAliasDef:
					// BigIntAliasDef is a wrapper struct — no meaningful zero literal.
					return ""
				}
			}
		}
		return `""`
	default:
		return `""`
	}
}

// zeroForPrimitive returns the Go zero literal for a primitive type name.
func zeroForPrimitive(name string) string {
	switch name {
	case "string":
		return `""`
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return "0"
	case "bool":
		return "false"
	default:
		return `""`
	}
}

// refOverridesSiblings returns true if the current draft treats $ref as
// overriding all sibling keywords. In draft3 through draft7, $ref causes
// all sibling keywords to be ignored. Starting with draft 2019-09,
// $ref is just another applicator and sibling keywords apply normally.
func (g *Generator) refOverridesSiblings() bool {
	return refOverridesSiblingsForDraft(g.draft)
}

func (g *Generator) refOverridesSiblingsForSchema(s *schema.Schema) bool {
	return refOverridesSiblingsForDraft(g.draftForSchema(s))
}

func refOverridesSiblingsForDraft(draft schema.Draft) bool {
	switch draft {
	case schema.Draft03, schema.Draft04, schema.Draft06, schema.Draft07:
		return true
	default:
		// DraftUnknown: be conservative and assume modern behavior.
		return false
	}
}

func (g *Generator) validationKeywordsEnabled() bool {
	return !g.validationKeywordsDisabled
}

func (g *Generator) requiresStrictIntegerToken(s *schema.Schema) bool {
	switch g.draftForSchema(s) {
	case schema.Draft03, schema.Draft04:
		return true
	default:
		return false
	}
}

func (g *Generator) hasValidationVocabulary(s *schema.Schema) bool {
	if s == nil {
		return true
	}
	if len(s.Vocabulary) > 0 {
		return declaresValidationVocabulary(s.Vocabulary)
	}
	if s.Schema == "" || g.resolver == nil {
		return true
	}
	meta, err := g.resolver.ResolveSchema(s.Schema, nil)
	if err != nil || meta == nil || len(meta.Vocabulary) == 0 {
		return true
	}
	return declaresValidationVocabulary(meta.Vocabulary)
}

func declaresValidationVocabulary(vocabulary map[string]bool) bool {
	for uri, required := range vocabulary {
		if required && strings.HasSuffix(uri, "/vocab/validation") {
			return true
		}
	}
	return false
}

func (g *Generator) draftForSchema(s *schema.Schema) schema.Draft {
	if s == nil {
		return g.draft
	}
	if d := schema.DetectDraft(s); d != schema.DraftUnknown {
		return d
	}
	if s.DocumentRoot != nil {
		if d := schema.DetectDraft(s.DocumentRoot); d != schema.DraftUnknown {
			return d
		}
	}
	return g.draft
}

func (g *Generator) supportsPrefixItems(s *schema.Schema) bool {
	return g.draftForSchema(s) == schema.Draft202012
}

func supportsDependentRequired(draft schema.Draft) bool {
	return draft == schema.Draft201909 || draft == schema.Draft202012
}

// extractValidationRules extracts validation rules from a property schema.
func extractValidationRules(goFieldName, jsonName string, s *schema.Schema) []ValidationRule {
	var rules []ValidationRule
	if s.MinLength != nil {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "minLength", Value: s.MinLength.Int(),
		})
	}
	if s.MaxLength != nil {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "maxLength", Value: s.MaxLength.Int(),
		})
	}
	if s.Minimum != nil {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "minimum", Value: *s.Minimum,
		})
	}
	if s.Maximum != nil {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "maximum", Value: *s.Maximum,
		})
	}
	if s.Pattern != nil {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "pattern", Value: *s.Pattern,
		})
	}
	if s.MinItems != nil {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "minItems", Value: s.MinItems.Int(),
		})
	}
	if s.MaxItems != nil {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "maxItems", Value: s.MaxItems.Int(),
		})
	}
	// additionalItems: false with tuple-form items → implicit maxItems = tuple length.
	// Draft 2020-12 uses prefixItems + items:false instead.
	if s.MaxItems == nil && s.AdditionalItems != nil && s.AdditionalItems.Bool != nil && !*s.AdditionalItems.Bool {
		if s.Items != nil && len(s.Items.Schemas) > 0 {
			rules = append(rules, ValidationRule{
				FieldName: goFieldName, JSONName: jsonName,
				RuleType: "maxItems", Value: len(s.Items.Schemas),
			})
		}
	}
	// Draft 2020-12: prefixItems + items:false → implicit maxItems = len(prefixItems).
	if s.MaxItems == nil && len(s.PrefixItems) > 0 && s.Items != nil && s.Items.Schema != nil && s.Items.Schema.IsFalseSchema() {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "maxItems", Value: len(s.PrefixItems),
		})
	}
	// unevaluatedItems:false with a fixed tuple and no extending applicators →
	// implicit maxItems = tuple length. Only applied when the schema is a simple
	// self-contained tuple (see unevaluatedItemsImpliesFixedTuple).
	if s.MaxItems == nil && unevaluatedItemsImpliesFixedTuple(s) {
		tupleLen := len(s.PrefixItems)
		if tupleLen == 0 && s.Items != nil {
			tupleLen = len(s.Items.Schemas)
		}
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "maxItems", Value: tupleLen,
		})
	}
	// exclusiveMinimum: can be a number (Draft 2020-12) or a boolean (Draft 4).
	// When boolean and true, the constraint uses the value from Minimum.
	if s.ExclusiveMinimum != nil {
		if s.ExclusiveMinimum.Number != nil {
			rules = append(rules, ValidationRule{
				FieldName: goFieldName, JSONName: jsonName,
				RuleType: "exclusiveMinimum", Value: *s.ExclusiveMinimum.Number,
			})
		} else if s.ExclusiveMinimum.Bool != nil && *s.ExclusiveMinimum.Bool && s.Minimum != nil {
			rules = append(rules, ValidationRule{
				FieldName: goFieldName, JSONName: jsonName,
				RuleType: "exclusiveMinimum", Value: *s.Minimum,
			})
		}
	}
	// exclusiveMaximum: same dual semantics as exclusiveMinimum.
	if s.ExclusiveMaximum != nil {
		if s.ExclusiveMaximum.Number != nil {
			rules = append(rules, ValidationRule{
				FieldName: goFieldName, JSONName: jsonName,
				RuleType: "exclusiveMaximum", Value: *s.ExclusiveMaximum.Number,
			})
		} else if s.ExclusiveMaximum.Bool != nil && *s.ExclusiveMaximum.Bool && s.Maximum != nil {
			rules = append(rules, ValidationRule{
				FieldName: goFieldName, JSONName: jsonName,
				RuleType: "exclusiveMaximum", Value: *s.Maximum,
			})
		}
	}
	if s.MultipleOf != nil {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "multipleOf", Value: *s.MultipleOf,
		})
	}
	if s.UniqueItems != nil && *s.UniqueItems {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "uniqueItems", Value: true,
		})
	}
	// not: {} (empty schema) means "forbidden property" — no value can match.
	if s.Not != nil && isAcceptAllSchema(s.Not) {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "forbidden", Value: true,
		})
	}
	// Format validation: for string-typed fields where the format doesn't map to
	// a distinct Go type (e.g. email, uri, uuid), emit a validation rule.
	// For formats that DO map to a distinct type (ipv4/ipv6 → netip.Addr),
	// emit a validation rule to enforce the specific subtype (v4 vs v6).
	if s.Format != nil && primarySchemaType(s) == "string" {
		format := *s.Format
		if formatNeedsValidation(format) || format == "ipv4" || format == "ipv6" {
			rules = append(rules, ValidationRule{
				FieldName: goFieldName, JSONName: jsonName,
				RuleType: "format", Value: format,
			})
		}
	}
	// Const validation: if the schema has a const value and we haven't already
	// handled it through an enum type (e.g., the field is typed as `any`),
	// emit a runtime check that marshals the field value and compares to the
	// expected JSON. This is a safety net for inline properties that didn't
	// get a dedicated enum type. Skip if enum is set (enum type has Validate).
	if (s.Const != nil || s.ConstIsNull) && len(s.Enum) == 0 {
		var constJSON string
		if s.Const != nil {
			b, err := json.Marshal(*s.Const)
			if err == nil {
				constJSON = string(b)
			}
		} else {
			constJSON = "null"
		}
		if constJSON != "" {
			rules = append(rules, ValidationRule{
				FieldName: goFieldName, JSONName: jsonName,
				RuleType: "const", Value: constJSON,
			})
		}
	}
	return rules
}

// isAcceptAllSchema returns true if the schema matches all values (empty schema or boolean true).
func isAcceptAllSchema(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	// An empty schema (no constraints) matches everything.
	// Must check ALL structural and validation keywords to avoid false positives.
	return len(s.Type) == 0 && len(s.Properties) == 0 && s.Not == nil &&
		len(s.AllOf) == 0 && len(s.AnyOf) == 0 && len(s.OneOf) == 0 &&
		s.Minimum == nil && s.Maximum == nil && s.MinLength == nil && s.MaxLength == nil &&
		s.MinItems == nil && s.MaxItems == nil && s.Pattern == nil && len(s.Enum) == 0 &&
		s.Ref == "" && s.DynamicRef == "" && s.RecursiveRef == "" &&
		len(s.Required) == 0 && s.AdditionalProperties == nil &&
		s.Items == nil && len(s.PrefixItems) == 0 && s.AdditionalItems == nil &&
		s.Contains == nil && s.PropertyNames == nil &&
		s.MinProperties == nil && s.MaxProperties == nil &&
		s.MultipleOf == nil && s.ExclusiveMinimum == nil && s.ExclusiveMaximum == nil &&
		s.UniqueItems == nil && s.If == nil &&
		len(s.DependentRequired) == 0 && len(s.DependentSchemas) == 0 &&
		s.UnevaluatedProperties == nil && s.UnevaluatedItems == nil &&
		s.Const == nil && !s.ConstIsNull && len(s.PatternProperties) == 0
}

// extractNotSchemaDef returns a *NotSchemaDef if the schema is a not-only
// schema that we can statically validate. Returns nil for schemas that have
// other constraints or use complex not sub-schemas we can't handle.
func extractNotSchemaDef(name string, s *schema.Schema) *NotSchemaDef {
	if s.Not == nil {
		return nil
	}
	// Only handle "not" as the sole constraint keyword. If the schema also has
	// type, properties, items, allOf, etc., it should be handled by other code paths.
	if len(s.Type) > 0 || hasProperties(s) || s.Items != nil || len(s.PrefixItems) > 0 ||
		len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 ||
		s.If != nil || s.Ref != "" || s.DynamicRef != "" || s.RecursiveRef != "" ||
		len(s.Required) > 0 || s.AdditionalProperties != nil ||
		s.Minimum != nil || s.Maximum != nil || s.MinLength != nil || s.MaxLength != nil ||
		s.Pattern != nil || s.MinItems != nil || s.MaxItems != nil ||
		s.MinProperties != nil || s.MaxProperties != nil ||
		s.Contains != nil || s.PropertyNames != nil ||
		s.UnevaluatedProperties != nil || s.UnevaluatedItems != nil ||
		len(s.DependentRequired) > 0 || len(s.DependentSchemas) > 0 {
		return nil
	}

	not := s.Not

	// not: false (boolean false schema) → allow everything, no validation needed.
	if not.IsFalseSchema() {
		return nil
	}

	// not: {} (empty schema) or not: true → forbid everything.
	if isAcceptAllSchema(not) || not.IsTrueSchema() {
		return &NotSchemaDef{
			Name:        name,
			Description: s.Description,
			IsForbidden: true,
		}
	}

	// not: {not: {}} → double negation of accept-all = accept-all.
	// No validation needed.
	if not.Not != nil && isAcceptAllSchema(not.Not) {
		return nil
	}

	// not: {type: X} or not: {type: [X, Y]} → reject values of those types.
	if len(not.Type) > 0 && isTypeOnlySchema(not) {
		return &NotSchemaDef{
			Name:        name,
			Description: s.Description,
			NotTypes:    not.Type,
		}
	}

	// Draft 3 disallow arrays normalize to not:{anyOf:[...]}. Handle branches
	// with simple type constraints and object property type checks.
	if len(not.AnyOf) > 0 {
		branches := extractNotSchemaBranches(not.AnyOf)
		if len(branches) == len(not.AnyOf) {
			return &NotSchemaDef{
				Name:        name,
				Description: s.Description,
				NotBranches: branches,
			}
		}
	}

	// Complex not sub-schema — can't handle statically.
	return nil
}

func extractNotSchemaBranches(subs []*schema.Schema) []NotSchemaBranch {
	branches := make([]NotSchemaBranch, 0, len(subs))
	for _, sub := range subs {
		if sub == nil || sub.IsBooleanSchema() {
			return nil
		}
		if len(sub.Type) > 0 && isTypeOnlySchema(sub) {
			branches = append(branches, NotSchemaBranch{Types: append([]string(nil), sub.Type...)})
			continue
		}
		if len(sub.Type) == 1 && hasSimpleNotBranchValidations(sub) && isSimpleNotBranchSchema(sub) {
			branches = append(branches, NotSchemaBranch{
				Types:       append([]string(nil), sub.Type...),
				Validations: extractSimpleNotBranchValidations(sub),
			})
			continue
		}
		if len(sub.Properties) > 0 && len(sub.Type) <= 1 && (len(sub.Type) == 0 || sub.Type[0] == "object") {
			branch := NotSchemaBranch{}
			for _, name := range sortedKeys(sub.Properties) {
				prop := sub.Properties[name]
				if prop == nil || len(prop.Type) != 1 || !isTypeOnlySchema(prop) {
					return nil
				}
				branch.Properties = append(branch.Properties, NotPropertyBranch{Name: name, JSONType: prop.Type[0]})
			}
			branches = append(branches, branch)
			continue
		}
		return nil
	}
	return branches
}

func hasSimpleNotBranchValidations(s *schema.Schema) bool {
	return s.Minimum != nil || s.Maximum != nil || s.ExclusiveMinimum != nil || s.ExclusiveMaximum != nil ||
		s.MultipleOf != nil || s.MinLength != nil || s.MaxLength != nil || s.Pattern != nil ||
		s.MinItems != nil || s.MaxItems != nil
}

func extractSimpleNotBranchValidations(s *schema.Schema) []ValidationRule {
	rules := extractValidationRules("", "", s)
	out := make([]ValidationRule, 0, len(rules))
	for _, rule := range rules {
		switch rule.RuleType {
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
			"minLength", "maxLength", "pattern", "minItems", "maxItems":
			out = append(out, rule)
		}
	}
	return out
}

func isSimpleNotBranchSchema(s *schema.Schema) bool {
	return s.Ref == "" && s.DynamicRef == "" && s.RecursiveRef == "" &&
		len(s.AllOf) == 0 && len(s.AnyOf) == 0 && len(s.OneOf) == 0 && s.Not == nil &&
		s.If == nil && s.Then == nil && s.Else == nil &&
		len(s.Properties) == 0 && len(s.PatternProperties) == 0 && s.AdditionalProperties == nil &&
		s.Items == nil && len(s.PrefixItems) == 0 && s.AdditionalItems == nil && s.Contains == nil &&
		len(s.Enum) == 0 && s.Const == nil && !s.ConstIsNull && s.Format == nil &&
		s.UniqueItems == nil && s.MinProperties == nil && s.MaxProperties == nil &&
		len(s.Definitions) == 0 && len(s.Defs) == 0 &&
		s.PropertyNames == nil && s.UnevaluatedItems == nil && s.UnevaluatedProperties == nil &&
		s.DependentSchemas == nil && s.DependentRequired == nil && len(s.Dependencies) == 0
}

// isTypeOnlySchema returns true if the schema has only a "type" constraint and
// nothing else (used for not:{type:X} detection).
func isTypeOnlySchema(s *schema.Schema) bool {
	return len(s.Properties) == 0 && s.Not == nil &&
		len(s.AllOf) == 0 && len(s.AnyOf) == 0 && len(s.OneOf) == 0 &&
		s.Minimum == nil && s.Maximum == nil && s.MinLength == nil && s.MaxLength == nil &&
		s.MinItems == nil && s.MaxItems == nil && s.Pattern == nil && len(s.Enum) == 0 &&
		s.Ref == "" && s.DynamicRef == "" && s.RecursiveRef == "" &&
		len(s.Required) == 0 && s.AdditionalProperties == nil &&
		s.Items == nil && len(s.PrefixItems) == 0 &&
		s.Contains == nil && s.PropertyNames == nil &&
		s.MinProperties == nil && s.MaxProperties == nil &&
		s.If == nil && s.UnevaluatedProperties == nil && s.UnevaluatedItems == nil &&
		len(s.DependentRequired) == 0 && len(s.DependentSchemas) == 0 &&
		s.MultipleOf == nil && s.ExclusiveMinimum == nil && s.ExclusiveMaximum == nil &&
		s.UniqueItems == nil
}

// extractTypeOnlySchemaDef returns a *TypeOnlySchemaDef if the schema has an
// explicit "type" constraint with types that don't map to a single Go type
// (multi-type arrays or null-only) and no other structural constraints.
// Returns nil for schemas that should be handled by other code paths.
func extractTypeOnlySchemaDef(name string, s *schema.Schema) *TypeOnlySchemaDef {
	if len(s.Type) == 0 && len(s.TypeSchemas) == 0 {
		return nil
	}
	// Check if the type maps to a single Go type already handled elsewhere.
	// primarySchemaType returns non-empty for single non-null types and for "null".
	pt := primarySchemaType(s)
	if pt != "" && pt != "null" && len(s.TypeSchemas) == 0 {
		return nil // Already handled by primitive type / object / array paths.
	}
	// At this point: either multi-type (pt == "") or null-only (pt == "null").

	// Only handle schemas where "type" is the sole constraint keyword.
	if hasProperties(s) || s.Items != nil || len(s.PrefixItems) > 0 ||
		len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 ||
		s.Not != nil || s.If != nil || s.Ref != "" || s.DynamicRef != "" || s.RecursiveRef != "" ||
		len(s.Required) > 0 || s.AdditionalProperties != nil ||
		s.Minimum != nil || s.Maximum != nil || s.MinLength != nil || s.MaxLength != nil ||
		s.Pattern != nil || s.MinItems != nil || s.MaxItems != nil ||
		s.MinProperties != nil || s.MaxProperties != nil ||
		s.Contains != nil || s.PropertyNames != nil ||
		s.UnevaluatedProperties != nil || s.UnevaluatedItems != nil ||
		len(s.DependentRequired) > 0 || len(s.DependentSchemas) > 0 ||
		s.MultipleOf != nil || s.ExclusiveMinimum != nil || s.ExclusiveMaximum != nil ||
		s.UniqueItems != nil {
		return nil
	}

	return &TypeOnlySchemaDef{
		Name:         name,
		Description:  s.Description,
		AllowedTypes: s.Type,
		TypeBranches: extractTypeSchemaBranches(s.TypeSchemas),
	}
}

func extractTypeSchemaBranches(typeSchemas []*schema.Schema) []TypeSchemaBranch {
	var branches []TypeSchemaBranch
	for _, typeSchema := range typeSchemas {
		if typeSchema == nil || typeSchema.IsBooleanSchema() || (len(typeSchema.Type) == 0 && len(typeSchema.Properties) == 0) {
			continue
		}
		branch := TypeSchemaBranch{AllowedTypes: append([]string(nil), typeSchema.Type...)}
		required := make(map[string]bool, len(typeSchema.Required))
		for _, name := range typeSchema.Required {
			required[name] = true
		}
		ok := true
		for _, propName := range sortedKeys(typeSchema.Properties) {
			propSchema := typeSchema.Properties[propName]
			if propSchema == nil || len(propSchema.Type) != 1 {
				ok = false
				break
			}
			branch.Properties = append(branch.Properties, TypeSchemaProperty{
				Name:     propName,
				JSONType: propSchema.Type[0],
				Required: required[propName],
			})
		}
		if ok && (len(branch.AllowedTypes) > 0 || len(branch.Properties) > 0) {
			branches = append(branches, branch)
		}
	}
	return branches
}

// isNilCheckable returns true if a Go type can be compared to nil.
// This includes pointers, interfaces (including 'any'), slices, and maps.
func isNilCheckable(t GoType) bool {
	switch v := t.(type) {
	case *PointerType:
		return true
	case *PrimitiveType:
		return v.Name == "any" || v.Name == "json.RawMessage"
	case *ArrayType:
		return true
	case *MapType:
		return true
	case *NamedType:
		return v.Pointer
	default:
		return false
	}
}

// extractAliasValidationRules extracts validation rules applicable to a
// top-level type alias (defined type). Unlike struct field validation, the
// receiver IS the value, so FieldName and JSONName are empty — the template
// uses the receiver name directly.
// Returns nil if the Go type is "any" (untyped schemas can't be validated).
// extractInferredItemConstraints extracts item-level validation info from an inferred
// array schema. It returns the fields needed for InferredAliasDef item validation.
func (g *Generator) extractInferredItemConstraints(s *schema.Schema, parentName string) (
	itemsFalse bool,
	itemsType string,
	itemsTypeName string,
	itemsChecks []ContainsCheck,
	itemsNested *NestedItemsDef,
	tupleItems []InferredTupleItem,
	additionalItemsFalse bool,
	additionalItemsType string,
) {
	hasPrefixItems := len(s.PrefixItems) > 0
	hasTupleItems := s.Items != nil && s.Items.Schemas != nil
	hasSingleItems := s.Items != nil && s.Items.Schema != nil

	// Draft 2020-12: prefixItems defines tuple positions. Older drafts ignore it.
	if hasPrefixItems && g.supportsPrefixItems(s) {
		for _, sub := range s.PrefixItems {
			tupleItems = append(tupleItems, g.inferredTupleItemFromSchema(sub))
		}
		// In draft 2020-12, "items" (as single schema) acts as additionalItems.
		if hasSingleItems {
			itemSchema := s.Items.Schema
			if itemSchema.IsFalseSchema() {
				additionalItemsFalse = true
			} else if len(itemSchema.Type) == 1 {
				additionalItemsType = itemSchema.Type[0]
			}
		}
		return
	}

	// Pre-2020-12: items as array of schemas = tuple form.
	if hasTupleItems {
		for _, sub := range s.Items.Schemas {
			tupleItems = append(tupleItems, g.inferredTupleItemFromSchema(sub))
		}
		// additionalItems constrains elements beyond the tuple.
		if s.AdditionalItems != nil {
			if s.AdditionalItems.Bool != nil && !*s.AdditionalItems.Bool {
				additionalItemsFalse = true
			} else if s.AdditionalItems.Schema != nil {
				addlSchema := s.AdditionalItems.Schema
				if len(addlSchema.Type) == 1 {
					additionalItemsType = addlSchema.Type[0]
				}
			}
		}
		return
	}

	// items as single schema — validates every element.
	if hasSingleItems {
		itemSchema := s.Items.Schema
		if itemSchema.IsFalseSchema() {
			itemsFalse = true
		} else if itemSchema.IsBooleanSchema() {
			// items: true — no constraint
		} else if effRef := itemSchema.EffectiveRef(); effRef != "" {
			// $ref — resolve and check for simple type or named type.
			resolved := g.resolveRefInContext(effRef, itemSchema)
			if resolved != nil && len(resolved.Type) == 1 {
				itemsType = resolved.Type[0]
			} else {
				refName := g.resolveRefTypeName(itemSchema)
				if refName != "" {
					itemsTypeName = refName
				}
			}
		} else if nested := g.extractNestedItemsDef(itemSchema); nested != nil {
			itemsNested = nested
		} else if len(itemSchema.Type) == 1 {
			itemsType = itemSchema.Type[0]
		} else {
			// No explicit type — extract validation checks if present.
			itemsChecks = extractSchemaChecks(itemSchema)
		}
		return
	}

	return
}

func (g *Generator) extractNestedItemsDef(s *schema.Schema) *NestedItemsDef {
	if s == nil || s.Items == nil || s.Items.Schema == nil || len(s.PrefixItems) > 0 || s.AdditionalItems != nil {
		return nil
	}
	itemSchema := s.Items.Schema
	if itemSchema == nil || itemSchema.IsBooleanSchema() {
		return nil
	}
	if effRef := itemSchema.EffectiveRef(); effRef != "" {
		if resolved := g.resolveRefInContext(effRef, itemSchema); resolved != nil && len(resolved.Type) == 1 {
			return &NestedItemsDef{ItemsType: resolved.Type[0]}
		}
	}
	if len(itemSchema.Type) == 1 {
		return &NestedItemsDef{ItemsType: itemSchema.Type[0]}
	}
	return nil
}

// inferredTupleItemFromSchema converts a sub-schema to an InferredTupleItem.
// The generator is needed to resolve $ref sub-schemas.
func (g *Generator) inferredTupleItemFromSchema(sub *schema.Schema) InferredTupleItem {
	if sub.IsFalseSchema() {
		return InferredTupleItem{IsFalse: true}
	}
	if sub.IsTrueSchema() || sub.IsBooleanSchema() {
		return InferredTupleItem{} // true schema — no constraint
	}
	// If the sub-schema has a $ref, resolve it and check the resolved type.
	if effRef := sub.EffectiveRef(); effRef != "" {
		resolved := g.resolveRefInContext(effRef, sub)
		if resolved != nil {
			if len(resolved.Type) == 1 {
				return InferredTupleItem{JSONType: resolved.Type[0]}
			}
			// Could be a named type — generate it and reference it.
			goName := refToGoName(effRef)
			goName = g.goNameForResolvedRef(effRef, resolved, goName)
			if !g.generated[goName] {
				_ = g.generateTypeDef(goName, resolved)
			}
			return InferredTupleItem{TypeName: goName}
		}
	}
	if len(sub.Type) == 1 {
		return InferredTupleItem{JSONType: sub.Type[0]}
	}
	return InferredTupleItem{} // complex schema — skip for now
}

// resolveRefTypeName resolves a $ref schema to a Go type name, generating the
// referenced type if needed. Returns empty string if the ref cannot be resolved.
func (g *Generator) resolveRefTypeName(s *schema.Schema) string {
	effRef := s.EffectiveRef()
	if effRef == "" {
		return ""
	}
	goName := refToGoName(effRef)
	if resolved := g.resolveRefInContext(effRef, s); resolved != nil {
		goName = g.goNameForResolvedRef(effRef, resolved, goName)
		if !g.generated[goName] {
			_ = g.generateTypeDef(goName, resolved)
		}
	}
	return goName
}

// extractPropertyNamesDef builds a PropertyNamesDef from a propertyNames sub-schema.
// Returns nil if the sub-schema is boolean true or has no actionable constraints.
func extractPropertyNamesDef(pn *schema.Schema) *PropertyNamesDef {
	// Boolean false schema: no property name is valid (empty objects only).
	if pn.IsFalseSchema() {
		return &PropertyNamesDef{IsForbidden: true}
	}
	// Boolean true schema: no constraint.
	if pn.IsTrueSchema() {
		return nil
	}

	def := &PropertyNamesDef{}
	hasConstraint := false

	if pn.MaxLength != nil {
		v := int(*pn.MaxLength)
		def.MaxLength = &v
		hasConstraint = true
	}
	if pn.MinLength != nil {
		v := int(*pn.MinLength)
		def.MinLength = &v
		hasConstraint = true
	}
	if pn.Pattern != nil {
		def.Pattern = *pn.Pattern
		hasConstraint = true
	}
	// Handle const (convert to single-element enum) and enum.
	enumValues := pn.Enum
	if pn.Const != nil && len(enumValues) == 0 {
		enumValues = []any{*pn.Const}
	}
	if len(enumValues) > 0 {
		for _, e := range enumValues {
			if str, ok := e.(string); ok {
				def.Enum = append(def.Enum, str)
			}
		}
		if len(def.Enum) > 0 {
			hasConstraint = true
		}
	}

	if !hasConstraint {
		return nil
	}
	return def
}

// isAlwaysTrueSchema returns true if the schema is semantically equivalent to
// "true" — i.e., it matches every possible value. This includes:
// - boolean true schema
// - empty schema (no keywords)
// - {"if": false, "else": true} pattern (if always fails, else always passes)
func isAlwaysTrueSchema(s *schema.Schema) bool {
	if s.IsTrueSchema() {
		return true
	}
	if s.IsBooleanSchema() {
		return false // IsFalseSchema case
	}
	// {"if": false, "else": true} pattern:
	// if is boolean false → always fails → else branch applies → true → always matches.
	if s.If != nil && s.If.IsFalseSchema() && s.Else != nil && s.Else.IsTrueSchema() {
		return true
	}
	// Empty schema (no constraints) matches everything.
	// Check for the absence of any constraining keywords.
	if s.Type == nil && s.Enum == nil && s.Const == nil &&
		s.Minimum == nil && s.Maximum == nil && s.MultipleOf == nil &&
		s.ExclusiveMinimum == nil && s.ExclusiveMaximum == nil &&
		s.MinLength == nil && s.MaxLength == nil && s.Pattern == nil &&
		s.MinItems == nil && s.MaxItems == nil && s.UniqueItems == nil &&
		s.MinProperties == nil && s.MaxProperties == nil &&
		s.Items == nil && len(s.PrefixItems) == 0 && s.AdditionalItems == nil &&
		s.Contains == nil && s.PropertyNames == nil &&
		s.AdditionalProperties == nil && len(s.Properties) == 0 &&
		len(s.Required) == 0 && len(s.AllOf) == 0 && len(s.AnyOf) == 0 &&
		len(s.OneOf) == 0 && s.Not == nil && s.If == nil &&
		len(s.DependentRequired) == 0 && len(s.DependentSchemas) == 0 &&
		s.EffectiveRef() == "" {
		return true
	}
	return false
}

// extractContainsDef builds a ContainsDef from a contains sub-schema.
// Returns nil if the sub-schema cannot be analyzed or is always-true with no
// minContains/maxContains constraints.
// extractSchemaChecks extracts ContainsCheck-style validation checks from a schema.
// This is used for items sub-schemas that have validation keywords but no explicit type.
func extractSchemaChecks(s *schema.Schema) []ContainsCheck {
	var checks []ContainsCheck
	if s.Minimum != nil {
		checks = append(checks, ContainsCheck{CheckType: "minimum", Value: *s.Minimum})
	}
	if s.Maximum != nil {
		checks = append(checks, ContainsCheck{CheckType: "maximum", Value: *s.Maximum})
	}
	if s.ExclusiveMinimum != nil && s.ExclusiveMinimum.Number != nil {
		checks = append(checks, ContainsCheck{CheckType: "exclusiveMinimum", Value: *s.ExclusiveMinimum.Number})
	}
	if s.ExclusiveMaximum != nil && s.ExclusiveMaximum.Number != nil {
		checks = append(checks, ContainsCheck{CheckType: "exclusiveMaximum", Value: *s.ExclusiveMaximum.Number})
	}
	if s.MultipleOf != nil {
		checks = append(checks, ContainsCheck{CheckType: "multipleOf", Value: *s.MultipleOf})
	}
	if len(s.Type) == 1 {
		checks = append(checks, ContainsCheck{CheckType: "type", Value: s.Type[0]})
	}
	return checks
}

// extractDependentSchemaConstraints extracts dependentSchemas constraints from a schema.
// It handles boolean false schemas, additionalProperties:false (allowed-keys check),
// properties with type constraints, required properties, and minProperties/maxProperties.
func extractDependentSchemaConstraints(s *schema.Schema) []DependentSchemaConstraint {
	if len(s.DependentSchemas) == 0 {
		return nil
	}
	var result []DependentSchemaConstraint
	for _, trigger := range sortedKeys(s.DependentSchemas) {
		depSchema := s.DependentSchemas[trigger]
		constraint := DependentSchemaConstraint{TriggerKey: trigger}
		hasConstraint := false

		// Boolean false schema: always reject when trigger is present.
		if depSchema.IsFalseSchema() {
			constraint.IsFalse = true
			result = append(result, constraint)
			continue
		}
		// Boolean true or empty schema: no constraint.
		if depSchema.IsTrueSchema() || isAlwaysTrueSchema(depSchema) {
			continue
		}

		// additionalProperties: false — only listed keys are allowed.
		if depSchema.AdditionalProperties != nil &&
			depSchema.AdditionalProperties.Bool != nil &&
			!*depSchema.AdditionalProperties.Bool {
			constraint.AllowedKeys = sortedKeys(depSchema.Properties)
			hasConstraint = true
		}

		// Properties with type constraints.
		if len(depSchema.Properties) > 0 {
			for _, propName := range sortedKeys(depSchema.Properties) {
				propSchema := depSchema.Properties[propName]
				if len(propSchema.Type) == 1 {
					constraint.PropertyTypes = append(constraint.PropertyTypes, DependentPropertyType{
						PropName: propName,
						JSONType: propSchema.Type[0],
					})
					hasConstraint = true
				}
			}
		}

		// Required properties from the sub-schema.
		if len(depSchema.Required) > 0 {
			sorted := make([]string, len(depSchema.Required))
			copy(sorted, depSchema.Required)
			sort.Strings(sorted)
			constraint.RequiredProps = sorted
			hasConstraint = true
		}

		// minProperties / maxProperties from the sub-schema.
		if depSchema.MinProperties != nil {
			v := depSchema.MinProperties.Int()
			constraint.MinProperties = &v
			hasConstraint = true
		}
		if depSchema.MaxProperties != nil {
			v := depSchema.MaxProperties.Int()
			constraint.MaxProperties = &v
			hasConstraint = true
		}

		if hasConstraint {
			result = append(result, constraint)
		}
	}
	return result
}

func extractContainsDef(s *schema.Schema) (*ContainsDef, *int, *int) {
	if s.Contains == nil {
		return nil, nil, nil
	}

	containsSch := s.Contains

	// Compute minContains and maxContains.
	var minC *int
	var maxC *int
	if s.MinContains != nil {
		v := int(*s.MinContains)
		minC = &v
	}
	if s.MaxContains != nil {
		v := int(*s.MaxContains)
		maxC = &v
	}

	// Boolean false schema: no element can ever match.
	if containsSch.IsFalseSchema() {
		return &ContainsDef{IsFalse: true}, minC, maxC
	}

	// Boolean true or always-true schema: every element matches.
	if isAlwaysTrueSchema(containsSch) {
		return &ContainsDef{IsTrue: true}, minC, maxC
	}

	def := &ContainsDef{}

	// Const → marshal to JSON for exact matching.
	if containsSch.Const != nil {
		b, err := json.Marshal(*containsSch.Const)
		if err == nil {
			def.ConstJSON = string(b)
			return def, minC, maxC
		}
	}

	// Single-value enum → treat as const.
	if len(containsSch.Enum) == 1 {
		b, err := json.Marshal(containsSch.Enum[0])
		if err == nil {
			def.ConstJSON = string(b)
			return def, minC, maxC
		}
	}

	// Multi-value enum → marshal all values, check if element matches any.
	if len(containsSch.Enum) > 1 {
		var enumValues []string
		allOK := true
		for _, v := range containsSch.Enum {
			b, err := json.Marshal(v)
			if err != nil {
				allOK = false
				break
			}
			enumValues = append(enumValues, string(b))
		}
		if allOK {
			def.EnumJSON = enumValues
			return def, minC, maxC
		}
	}

	// Collect constraint checks.
	var checks []ContainsCheck

	if containsSch.Minimum != nil {
		checks = append(checks, ContainsCheck{CheckType: "minimum", Value: *containsSch.Minimum})
	}
	if containsSch.Maximum != nil {
		checks = append(checks, ContainsCheck{CheckType: "maximum", Value: *containsSch.Maximum})
	}
	if containsSch.ExclusiveMinimum != nil && containsSch.ExclusiveMinimum.Number != nil {
		checks = append(checks, ContainsCheck{CheckType: "exclusiveMinimum", Value: *containsSch.ExclusiveMinimum.Number})
	}
	if containsSch.ExclusiveMaximum != nil && containsSch.ExclusiveMaximum.Number != nil {
		checks = append(checks, ContainsCheck{CheckType: "exclusiveMaximum", Value: *containsSch.ExclusiveMaximum.Number})
	}
	if containsSch.MultipleOf != nil {
		checks = append(checks, ContainsCheck{CheckType: "multipleOf", Value: *containsSch.MultipleOf})
	}
	if len(containsSch.Type) == 1 {
		checks = append(checks, ContainsCheck{CheckType: "type", Value: containsSch.Type[0]})
	}
	// String constraints
	if containsSch.MinLength != nil {
		checks = append(checks, ContainsCheck{CheckType: "minLength", Value: *containsSch.MinLength})
	}
	if containsSch.MaxLength != nil {
		checks = append(checks, ContainsCheck{CheckType: "maxLength", Value: *containsSch.MaxLength})
	}
	if containsSch.Pattern != nil && *containsSch.Pattern != "" {
		checks = append(checks, ContainsCheck{CheckType: "pattern", Value: *containsSch.Pattern})
	}

	if len(checks) > 0 {
		def.Checks = checks
		return def, minC, maxC
	}

	// Complex schema we can't extract checks from — skip.
	return nil, nil, nil
}

func extractAliasValidationRules(s *schema.Schema, goType GoType) []ValidationRule {
	// Skip validation on untyped "any" fields — can't compile numeric/string checks.
	if pt, ok := goType.(*PrimitiveType); ok && pt.Name == "any" {
		return nil
	}
	rules := extractValidationRules("", "", s)
	if len(rules) == 0 {
		return nil
	}
	return rules
}

// extractAnyOfVariantRules extracts validation rules from each anyOf sub-schema.
// Each inner slice represents one variant's constraint set.
// Returns nil when the schema has no anyOf or when all variants yield empty rules.
func extractAnyOfVariantRules(s *schema.Schema, goType GoType) [][]ValidationRule {
	if len(s.AnyOf) == 0 {
		return nil
	}
	// Skip for untyped "any" — can't compile checks.
	if pt, ok := goType.(*PrimitiveType); ok && pt.Name == "any" {
		return nil
	}
	var variants [][]ValidationRule
	hasRules := false
	for _, variant := range s.AnyOf {
		rules := extractValidationRules("", "", variant)
		variants = append(variants, rules)
		if len(rules) > 0 {
			hasRules = true
		}
	}
	if !hasRules {
		return nil
	}
	return variants
}

// extractOneOfVariantRules extracts validation rules from each oneOf sub-schema.
// Each inner slice represents one variant's constraint set.
// Returns nil when the schema has no oneOf or when all variants yield empty rules.
func extractOneOfVariantRules(s *schema.Schema, goType GoType) [][]ValidationRule {
	if len(s.OneOf) == 0 {
		return nil
	}
	// Skip for untyped "any" — can't compile checks.
	if pt, ok := goType.(*PrimitiveType); ok && pt.Name == "any" {
		return nil
	}
	var variants [][]ValidationRule
	hasRules := false
	for _, variant := range s.OneOf {
		rules := extractValidationRules("", "", variant)
		variants = append(variants, rules)
		if len(rules) > 0 {
			hasRules = true
		}
	}
	if !hasRules {
		return nil
	}
	return variants
}

// extractPatternPropertyValidationRules extracts validation rules from a
// patternProperties sub-schema. These rules are checked at runtime against
// json.RawMessage values, so we include a "type" rule when the sub-schema
// specifies a type constraint.
func extractPatternPropertyValidationRules(s *schema.Schema) []ValidationRule {
	var rules []ValidationRule
	// Type constraint — checked by inspecting the raw JSON value at runtime.
	if len(s.Type) > 0 {
		// Collect all allowed types (e.g., ["string", "null"]).
		var types []string
		for _, t := range s.Type {
			types = append(types, t)
		}
		if len(types) == 1 {
			rules = append(rules, ValidationRule{
				RuleType: "ppType", Value: types[0],
			})
		} else if len(types) > 1 {
			rules = append(rules, ValidationRule{
				RuleType: "ppType", Value: types,
			})
		}
	}
	// Numeric constraints.
	if s.Minimum != nil {
		rules = append(rules, ValidationRule{RuleType: "ppMinimum", Value: *s.Minimum})
	}
	if s.Maximum != nil {
		rules = append(rules, ValidationRule{RuleType: "ppMaximum", Value: *s.Maximum})
	}
	if s.ExclusiveMinimum != nil && s.ExclusiveMinimum.Number != nil {
		rules = append(rules, ValidationRule{RuleType: "ppExclusiveMinimum", Value: *s.ExclusiveMinimum.Number})
	}
	if s.ExclusiveMaximum != nil && s.ExclusiveMaximum.Number != nil {
		rules = append(rules, ValidationRule{RuleType: "ppExclusiveMaximum", Value: *s.ExclusiveMaximum.Number})
	}
	if s.MultipleOf != nil {
		rules = append(rules, ValidationRule{RuleType: "ppMultipleOf", Value: *s.MultipleOf})
	}
	// String constraints.
	if s.MinLength != nil {
		rules = append(rules, ValidationRule{RuleType: "ppMinLength", Value: s.MinLength.Int()})
	}
	if s.MaxLength != nil {
		rules = append(rules, ValidationRule{RuleType: "ppMaxLength", Value: s.MaxLength.Int()})
	}
	if s.Pattern != nil {
		rules = append(rules, ValidationRule{RuleType: "ppPattern", Value: *s.Pattern})
	}
	// Array constraints.
	if s.MinItems != nil {
		rules = append(rules, ValidationRule{RuleType: "ppMinItems", Value: s.MinItems.Int()})
	}
	if s.MaxItems != nil {
		rules = append(rules, ValidationRule{RuleType: "ppMaxItems", Value: s.MaxItems.Int()})
	}
	return rules
}

// extractNonObjectValidationRules extracts validation rules from the schema
// that apply to non-object data. These use the same pp* rule types as
// patternProperties since both validate json.RawMessage values at runtime.
func extractNonObjectValidationRules(s *schema.Schema) []ValidationRule {
	return extractPatternPropertyValidationRules(s)
}

// buildUnevaluatedPropertiesDef constructs an UnevaluatedPropertiesDef for a schema
// that has an unevaluatedProperties keyword. It walks the schema tree to determine
// which properties are "evaluated" (covered by properties, patternProperties,
// additionalProperties, or nested unevaluatedProperties in applicator subschemas).
func (g *Generator) buildUnevaluatedPropertiesDef(s *schema.Schema) *UnevaluatedPropertiesDef {
	uneval := s.UnevaluatedProperties
	if uneval == nil {
		return nil
	}

	def := &UnevaluatedPropertiesDef{}

	// Check if unevaluatedProperties is a boolean schema.
	if uneval.IsBooleanSchema() {
		if uneval.BooleanSchema != nil && *uneval.BooleanSchema {
			def.IsAllowed = true
			return def
		}
		// false → forbidden
		def.IsForbidden = true
	} else {
		// unevaluatedProperties is a schema constraint (not boolean).
		// Extract validation rules from the schema to apply to each unevaluated value.
		unevalType := primarySchemaType(uneval)
		if unevalType == "" {
			unevalType = g.inferTypeFromConstraints(uneval)
		}
		if unevalType != "" {
			goType := PrimitiveTypeFromSchema(unevalType)
			if goType != nil {
				def.ValueType = goType.GoTypeName()
				rules := extractValidationRules("", "", uneval)
				def.Validations = rules
			} else {
				// Non-primitive type (object/array) — too complex, allow permissively.
				def.IsAllowed = true
				return def
			}
		} else {
			// No type constraint — allow permissively.
			def.IsAllowed = true
			return def
		}
	}

	// Collect evaluated properties from the schema tree.
	names, patterns, allEvaluated, conditionals := g.collectEvaluatedProperties(s)
	def.AllEvaluated = allEvaluated

	// Convert to sorted slices for deterministic output.
	def.EvaluatedNames = sortedKeys(names)
	def.EvaluatedPatterns = sortedKeys(patterns)
	def.ConditionalEvals = conditionals

	return def
}

// collectEvaluatedProperties walks the schema tree and collects property names
// and patterns that are "evaluated" for the purpose of unevaluatedProperties.
// The root schema's own unevaluatedProperties is NOT included (that's the
// constraint we're evaluating); only nested applicator subschemas contribute.
// It returns:
//   - names: set of property names evaluated by always-true sources (properties, allOf, $ref)
//   - patterns: set of regex patterns from always-true sources
//   - allEvaluated: true if additionalProperties or unevaluatedProperties in a nested
//     schema marks ALL remaining properties as evaluated
//   - conditionals: runtime-conditional evaluation branches for anyOf/oneOf/if-then-else/dependentSchemas
func (g *Generator) collectEvaluatedProperties(s *schema.Schema) (names map[string]bool, patterns map[string]bool, allEvaluated bool, conditionals []ConditionalEval) {
	names = make(map[string]bool)
	patterns = make(map[string]bool)

	if s == nil {
		return
	}

	// Direct properties on the root schema — these are always evaluated.
	for k := range s.Properties {
		names[k] = true
	}

	// Pattern properties on the root schema.
	for pattern := range s.PatternProperties {
		patterns[pattern] = true
	}

	// additionalProperties on the root schema marks ALL remaining as evaluated.
	if s.AdditionalProperties != nil {
		allEvaluated = true
		return
	}

	// $ref on the root — evaluated properties from the referenced schema.
	if effRef := s.EffectiveRef(); effRef != "" {
		if resolved := g.resolveRefInContext(effRef, s); resolved != nil {
			g.collectEvaluatedFromNested(resolved, names, patterns, &allEvaluated)
		}
	}
	if s.DynamicRef != "" {
		if resolved := g.resolveDynamicRef(s.DynamicRef, s); resolved != nil {
			g.collectEvaluatedFromNested(resolved, names, patterns, &allEvaluated)
		}
	}

	// Recurse into allOf — all branches always apply.
	// We also check each allOf sub-schema for oneOf/anyOf/if-then-else and
	// build conditional evals for them instead of static over-approximation.
	for _, sub := range s.AllOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		// Collect static evaluated properties (properties, patternProperties, $ref, etc.)
		// but exclude oneOf/anyOf/if-then-else from the static collection.
		g.collectEvaluatedFromNestedExcludeConditional(resolved, names, patterns, &allEvaluated)

		// Build conditional evals for oneOf/anyOf/if-then-else inside allOf sub-schemas.
		if len(resolved.OneOf) > 0 {
			ce := g.collectMultiBranchEval("oneOf", resolved.OneOf)
			if ce != nil {
				conditionals = append(conditionals, *ce)
			} else {
				// Fallback: static over-approximation.
				for _, osub := range resolved.OneOf {
					oresolved := osub
					if effRef := osub.EffectiveRef(); effRef != "" {
						if r := g.resolveRefInContext(effRef, osub); r != nil {
							oresolved = r
						}
					}
					g.collectEvaluatedFromNested(oresolved, names, patterns, &allEvaluated)
				}
			}
		}
		if len(resolved.AnyOf) > 0 {
			ce := g.collectMultiBranchEval("anyOf", resolved.AnyOf)
			if ce != nil {
				conditionals = append(conditionals, *ce)
			} else {
				for _, asub := range resolved.AnyOf {
					aresolved := asub
					if effRef := asub.EffectiveRef(); effRef != "" {
						if r := g.resolveRefInContext(effRef, asub); r != nil {
							aresolved = r
						}
					}
					g.collectEvaluatedFromNested(aresolved, names, patterns, &allEvaluated)
				}
			}
		}
		if resolved.If != nil {
			ifCond := g.extractIfCondition(resolved.If)
			if ifCond != nil {
				thenBranch := g.collectBranchEval(resolved.Then)
				elseBranch := g.collectBranchEval(resolved.Else)
				ifBranch := g.collectBranchEval(resolved.If)
				if ifBranch != nil && thenBranch != nil {
					thenBranch = mergeEvalBranches(ifBranch, thenBranch)
				} else if ifBranch != nil && thenBranch == nil {
					thenBranch = ifBranch
				}
				hasThen := thenBranch != nil && (thenBranch.HasNames() || thenBranch.HasPatterns() || thenBranch.AllEvaluated)
				hasElse := elseBranch != nil && (elseBranch.HasNames() || elseBranch.HasPatterns() || elseBranch.AllEvaluated)
				if hasThen || hasElse {
					conditionals = append(conditionals, ConditionalEval{
						Kind:       "ifThenElse",
						IfBranch:   ifCond,
						ThenBranch: thenBranch,
						ElseBranch: elseBranch,
					})
				}
			} else {
				g.collectEvaluatedFromNested(resolved.If, names, patterns, &allEvaluated)
				if resolved.Then != nil {
					g.collectEvaluatedFromNested(resolved.Then, names, patterns, &allEvaluated)
				}
				if resolved.Else != nil {
					g.collectEvaluatedFromNested(resolved.Else, names, patterns, &allEvaluated)
				}
			}
		}
	}

	// Runtime-conditional branches: anyOf/oneOf/if-then-else/dependentSchemas.
	// Instead of merging all properties statically, we collect per-branch info
	// so the generated Validate() can build the evaluated set dynamically.

	// dependentSchemas: properties evaluated only when the trigger key is present.
	for triggerKey, depSchema := range s.DependentSchemas {
		branch := g.collectBranchEval(depSchema)
		if branch != nil && (branch.HasNames() || branch.HasPatterns() || branch.AllEvaluated) {
			conditionals = append(conditionals, ConditionalEval{
				Kind:       "dependentSchema",
				TriggerKey: triggerKey,
				Branch:     branch,
			})
		}
	}

	// if/then/else: try runtime conditional evaluation via IfConditionDef.
	// If the if-schema is too complex for runtime evaluation, fall back to
	// static over-approximation.
	if s.If != nil {
		ifCond := g.extractIfCondition(s.If)
		if ifCond != nil {
			// Runtime-evaluable if condition: create conditional branches.
			thenBranch := g.collectBranchEval(s.Then)
			elseBranch := g.collectBranchEval(s.Else)
			// Also collect properties from the if-schema itself into both branches,
			// since the if-schema's properties are evaluated when it matches.
			ifBranch := g.collectBranchEval(s.If)
			if ifBranch != nil && thenBranch != nil {
				thenBranch = mergeEvalBranches(ifBranch, thenBranch)
			} else if ifBranch != nil && thenBranch == nil {
				thenBranch = ifBranch
			}
			// Per JSON Schema spec: when if fails, its annotations are discarded.
			// So the else branch does NOT include if-schema properties.
			hasThen := thenBranch != nil && (thenBranch.HasNames() || thenBranch.HasPatterns() || thenBranch.AllEvaluated)
			hasElse := elseBranch != nil && (elseBranch.HasNames() || elseBranch.HasPatterns() || elseBranch.AllEvaluated)
			if hasThen || hasElse {
				conditionals = append(conditionals, ConditionalEval{
					Kind:       "ifThenElse",
					IfBranch:   ifCond,
					ThenBranch: thenBranch,
					ElseBranch: elseBranch,
				})
			}
		} else {
			// Fallback: static over-approximation.
			g.collectEvaluatedFromNested(s.If, names, patterns, &allEvaluated)
			if s.Then != nil {
				g.collectEvaluatedFromNested(s.Then, names, patterns, &allEvaluated)
			}
			if s.Else != nil {
				g.collectEvaluatedFromNested(s.Else, names, patterns, &allEvaluated)
			}
		}
	}

	// anyOf: try runtime conditional evaluation via branch matching.
	// If branches have evaluable matching criteria (required keys + const checks),
	// use runtime evaluation; otherwise fall back to static over-approximation.
	if len(s.AnyOf) > 0 {
		ce := g.collectMultiBranchEval("anyOf", s.AnyOf)
		if ce != nil {
			conditionals = append(conditionals, *ce)
		} else {
			// Fallback: static over-approximation.
			for _, sub := range s.AnyOf {
				resolved := sub
				if effRef := sub.EffectiveRef(); effRef != "" {
					if r := g.resolveRefInContext(effRef, sub); r != nil {
						resolved = r
					}
				}
				g.collectEvaluatedFromNested(resolved, names, patterns, &allEvaluated)
			}
		}
	}

	// oneOf: try runtime conditional evaluation via branch matching.
	// If branches have evaluable matching criteria (required keys + const checks),
	// use runtime evaluation; otherwise fall back to static over-approximation.
	if len(s.OneOf) > 0 {
		ce := g.collectMultiBranchEval("oneOf", s.OneOf)
		if ce != nil {
			conditionals = append(conditionals, *ce)
		} else {
			// Fallback: static over-approximation.
			for _, sub := range s.OneOf {
				resolved := sub
				if effRef := sub.EffectiveRef(); effRef != "" {
					if r := g.resolveRefInContext(effRef, sub); r != nil {
						resolved = r
					}
				}
				g.collectEvaluatedFromNested(resolved, names, patterns, &allEvaluated)
			}
		}
	}

	return
}

// collectEvaluatedFromNested collects evaluated property names and patterns from
// a nested schema (inside allOf, $ref, etc.). Unlike the root schema, nested
// schemas' additionalProperties and unevaluatedProperties DO mark all as evaluated.
func (g *Generator) collectEvaluatedFromNested(s *schema.Schema, names map[string]bool, patterns map[string]bool, allEvaluated *bool) {
	if s == nil {
		return
	}
	if s.IsBooleanSchema() {
		return
	}

	// Direct properties.
	for k := range s.Properties {
		names[k] = true
	}

	// Pattern properties.
	for pattern := range s.PatternProperties {
		patterns[pattern] = true
	}

	// additionalProperties in a nested schema marks ALL remaining as evaluated.
	if s.AdditionalProperties != nil {
		*allEvaluated = true
	}

	// unevaluatedProperties in a nested schema marks ALL remaining as evaluated.
	if s.UnevaluatedProperties != nil {
		*allEvaluated = true
	}

	// $ref — evaluated properties from the referenced schema.
	if effRef := s.EffectiveRef(); effRef != "" {
		if resolved := g.resolveRefInContext(effRef, s); resolved != nil {
			g.collectEvaluatedFromNested(resolved, names, patterns, allEvaluated)
		}
	}
	if s.DynamicRef != "" {
		if resolved := g.resolveDynamicRef(s.DynamicRef, s); resolved != nil {
			g.collectEvaluatedFromNested(resolved, names, patterns, allEvaluated)
		}
	}

	// Recurse into allOf — all branches always apply.
	for _, sub := range s.AllOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		g.collectEvaluatedFromNested(resolved, names, patterns, allEvaluated)
	}

	// Recurse into anyOf/oneOf — collect from all branches (over-approximation).
	for _, sub := range s.AnyOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		g.collectEvaluatedFromNested(resolved, names, patterns, allEvaluated)
	}
	for _, sub := range s.OneOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		g.collectEvaluatedFromNested(resolved, names, patterns, allEvaluated)
	}

	// Recurse into if/then/else.
	if s.If != nil {
		g.collectEvaluatedFromNested(s.If, names, patterns, allEvaluated)
	}
	if s.Then != nil {
		g.collectEvaluatedFromNested(s.Then, names, patterns, allEvaluated)
	}
	if s.Else != nil {
		g.collectEvaluatedFromNested(s.Else, names, patterns, allEvaluated)
	}

	// Recurse into dependentSchemas.
	for _, depSchema := range s.DependentSchemas {
		g.collectEvaluatedFromNested(depSchema, names, patterns, allEvaluated)
	}
}

// collectEvaluatedFromNestedExcludeConditional is like collectEvaluatedFromNested
// but skips oneOf, anyOf, and if/then/else processing. These are handled separately
// by the caller via conditional evaluation instead of static over-approximation.
func (g *Generator) collectEvaluatedFromNestedExcludeConditional(s *schema.Schema, names map[string]bool, patterns map[string]bool, allEvaluated *bool) {
	if s == nil {
		return
	}
	if s.IsBooleanSchema() {
		return
	}

	// Direct properties.
	for k := range s.Properties {
		names[k] = true
	}

	// Pattern properties.
	for pattern := range s.PatternProperties {
		patterns[pattern] = true
	}

	// additionalProperties in a nested schema marks ALL remaining as evaluated.
	if s.AdditionalProperties != nil {
		*allEvaluated = true
	}

	// unevaluatedProperties in a nested schema marks ALL remaining as evaluated.
	if s.UnevaluatedProperties != nil {
		*allEvaluated = true
	}

	// $ref — evaluated properties from the referenced schema.
	if effRef := s.EffectiveRef(); effRef != "" {
		if resolved := g.resolveRefInContext(effRef, s); resolved != nil {
			g.collectEvaluatedFromNested(resolved, names, patterns, allEvaluated)
		}
	}
	if s.DynamicRef != "" {
		if resolved := g.resolveDynamicRef(s.DynamicRef, s); resolved != nil {
			g.collectEvaluatedFromNested(resolved, names, patterns, allEvaluated)
		}
	}

	// Recurse into allOf — all branches always apply.
	for _, sub := range s.AllOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		g.collectEvaluatedFromNested(resolved, names, patterns, allEvaluated)
	}

	// NOTE: oneOf, anyOf, and if/then/else are NOT processed here.
	// The caller handles them via conditional evaluation.

	// Recurse into dependentSchemas.
	for _, depSchema := range s.DependentSchemas {
		g.collectEvaluatedFromNested(depSchema, names, patterns, allEvaluated)
	}
}

// collectBranchEval collects evaluated property names and patterns from a single
// schema branch, returning an EvalBranchDef. Returns nil if the branch is nil.
func (g *Generator) collectBranchEval(s *schema.Schema) *EvalBranchDef {
	if s == nil {
		return nil
	}
	names := make(map[string]bool)
	patterns := make(map[string]bool)
	var allEvaluated bool

	// Collect from this schema and its nested applicators.
	g.collectEvaluatedFromNested(s, names, patterns, &allEvaluated)

	branch := &EvalBranchDef{
		Names:        sortedKeys(names),
		Patterns:     sortedKeys(patterns),
		AllEvaluated: allEvaluated,
	}

	// Collect branch-matching metadata: required keys and const checks.
	branch.RequiredKeys = append([]string(nil), s.Required...)
	sort.Strings(branch.RequiredKeys)
	for propName, propSchema := range s.Properties {
		if propSchema != nil && propSchema.Const != nil {
			jsonVal, err := json.Marshal(*propSchema.Const)
			if err == nil {
				branch.ConstChecks = append(branch.ConstChecks, ConstCheck{
					PropertyName: propName,
					GoFieldName:  JSONPropertyToGoName(propName),
					JSONValue:    string(jsonVal),
				})
			}
		}
	}
	// Sort const checks for deterministic output.
	sort.Slice(branch.ConstChecks, func(i, j int) bool {
		return branch.ConstChecks[i].PropertyName < branch.ConstChecks[j].PropertyName
	})

	return branch
}

// extractIfCondition extracts a runtime-evaluable condition from an if-schema.
// Returns nil if the if-schema is too complex for runtime evaluation.
func (g *Generator) extractIfCondition(s *schema.Schema) *IfConditionDef {
	if s == nil {
		return nil
	}
	// We can evaluate if-schemas that use properties with const constraints
	// and/or required fields.
	var constChecks []ConstCheck
	for propName, propSchema := range s.Properties {
		if propSchema != nil && propSchema.Const != nil {
			jsonVal, err := json.Marshal(*propSchema.Const)
			if err == nil {
				constChecks = append(constChecks, ConstCheck{
					PropertyName: propName,
					GoFieldName:  JSONPropertyToGoName(propName),
					JSONValue:    string(jsonVal),
				})
			}
		}
	}
	requiredKeys := append([]string(nil), s.Required...)
	sort.Strings(requiredKeys)
	sort.Slice(constChecks, func(i, j int) bool {
		return constChecks[i].PropertyName < constChecks[j].PropertyName
	})

	// Must have at least some condition to evaluate.
	if len(constChecks) == 0 && len(requiredKeys) == 0 {
		return nil
	}

	return &IfConditionDef{
		ConstChecks:  constChecks,
		RequiredKeys: requiredKeys,
	}
}

// collectMultiBranchEval collects evaluation branches for anyOf/oneOf.
// Returns a ConditionalEval or nil if any branch is too complex.
func (g *Generator) collectMultiBranchEval(kind string, subs []*schema.Schema) *ConditionalEval {
	if len(subs) == 0 {
		return nil
	}

	branches := g.flattenBranches(subs, 0)
	if branches == nil {
		return nil
	}

	// Check that at least some branches have evaluable properties.
	hasContent := false
	for _, b := range branches {
		if b.HasNames() || b.HasPatterns() || b.AllEvaluated {
			hasContent = true
			break
		}
	}
	if !hasContent {
		return nil
	}

	// Check that ALL branches have matching criteria (required keys or const checks).
	// Without matching criteria, we can't determine which branch matched at runtime
	// and must fall back to static over-approximation.
	for _, b := range branches {
		if len(b.RequiredKeys) == 0 && len(b.ConstChecks) == 0 {
			return nil
		}
	}

	return &ConditionalEval{
		Kind:     kind,
		Branches: branches,
	}
}

// isOneOfOnlySchema returns true if the schema contains ONLY a oneOf (no direct
// properties, patternProperties, required, additionalProperties, etc.) — just
// structural content that can be flattened.
func isOneOfOnlySchema(s *schema.Schema) bool {
	if s == nil || len(s.OneOf) == 0 {
		return false
	}
	return len(s.Properties) == 0 &&
		len(s.PatternProperties) == 0 &&
		len(s.Required) == 0 &&
		s.AdditionalProperties == nil &&
		len(s.AllOf) == 0 &&
		len(s.AnyOf) == 0 &&
		s.If == nil
}

// flattenBranches recursively collects EvalBranchDefs from a list of sub-schemas.
// When a sub-schema resolves to a oneOf-only schema (no direct properties),
// it is expanded into its inner branches. Returns nil if recursion exceeds depth limit.
func (g *Generator) flattenBranches(subs []*schema.Schema, depth int) []EvalBranchDef {
	if depth > 5 {
		return nil // prevent infinite recursion
	}
	var branches []EvalBranchDef
	for _, sub := range subs {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}

		// If the resolved schema is a oneOf-only schema, flatten recursively.
		if isOneOfOnlySchema(resolved) {
			inner := g.flattenBranches(resolved.OneOf, depth+1)
			if inner == nil {
				return nil // propagate failure
			}
			branches = append(branches, inner...)
			continue
		}

		branch := g.collectBranchEval(resolved)
		if branch == nil {
			branch = &EvalBranchDef{}
		}
		// For matching, we need required keys and/or const checks from the original
		// sub-schema (not the resolved one, since required is on the sub itself).
		if len(sub.Required) > 0 && len(branch.RequiredKeys) == 0 {
			branch.RequiredKeys = append([]string(nil), sub.Required...)
			sort.Strings(branch.RequiredKeys)
		}
		for propName, propSchema := range sub.Properties {
			if propSchema != nil && propSchema.Const != nil {
				jsonVal, err := json.Marshal(*propSchema.Const)
				if err == nil {
					// Check if this const check already exists from resolved schema.
					found := false
					for _, cc := range branch.ConstChecks {
						if cc.PropertyName == propName {
							found = true
							break
						}
					}
					if !found {
						branch.ConstChecks = append(branch.ConstChecks, ConstCheck{
							PropertyName: propName,
							GoFieldName:  JSONPropertyToGoName(propName),
							JSONValue:    string(jsonVal),
						})
					}
				}
			}
		}
		sort.Slice(branch.ConstChecks, func(i, j int) bool {
			return branch.ConstChecks[i].PropertyName < branch.ConstChecks[j].PropertyName
		})
		branches = append(branches, *branch)
	}
	return branches
}

// mergeEvalBranches merges two EvalBranchDef into one (union of names and patterns).
func mergeEvalBranches(a, b *EvalBranchDef) *EvalBranchDef {
	names := make(map[string]bool)
	patterns := make(map[string]bool)
	for _, n := range a.Names {
		names[n] = true
	}
	for _, n := range b.Names {
		names[n] = true
	}
	for _, p := range a.Patterns {
		patterns[p] = true
	}
	for _, p := range b.Patterns {
		patterns[p] = true
	}
	return &EvalBranchDef{
		Names:        sortedKeys(names),
		Patterns:     sortedKeys(patterns),
		AllEvaluated: a.AllEvaluated || b.AllEvaluated,
	}
}

// collectCousinUnevalChecks detects allOf/anyOf sub-schemas that have their own
// unevaluatedProperties constraint (cousin isolation). For each such sub-schema,
// it computes the evaluated set scoped to that branch only.
func (g *Generator) collectCousinUnevalChecks(s *schema.Schema) []CousinUnevalCheck {
	var checks []CousinUnevalCheck

	// Check allOf sub-schemas.
	for _, sub := range s.AllOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		if resolved.UnevaluatedProperties == nil {
			continue
		}
		check := g.buildCousinCheck(resolved)
		if check != nil {
			checks = append(checks, *check)
		}
	}

	// Check anyOf sub-schemas.
	for _, sub := range s.AnyOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		if resolved.UnevaluatedProperties == nil {
			continue
		}
		check := g.buildCousinCheck(resolved)
		if check != nil {
			checks = append(checks, *check)
		}
	}

	return checks
}

// buildCousinCheck builds a CousinUnevalCheck for a sub-schema with unevaluatedProperties.
func (g *Generator) buildCousinCheck(s *schema.Schema) *CousinUnevalCheck {
	uneval := s.UnevaluatedProperties
	if uneval == nil {
		return nil
	}

	// Check boolean value.
	if uneval.IsBooleanSchema() {
		if uneval.BooleanSchema != nil && *uneval.BooleanSchema {
			// unevaluatedProperties: true — no constraint, skip.
			return nil
		}
		// unevaluatedProperties: false
	}

	// Collect evaluated properties scoped to this branch only.
	names := make(map[string]bool)
	patterns := make(map[string]bool)
	var allEvaluated bool

	// Direct properties on this sub-schema.
	for k := range s.Properties {
		names[k] = true
	}
	for pattern := range s.PatternProperties {
		patterns[pattern] = true
	}
	if s.AdditionalProperties != nil {
		allEvaluated = true
	}

	// $ref on this sub-schema.
	if effRef := s.EffectiveRef(); effRef != "" {
		if resolved := g.resolveRefInContext(effRef, s); resolved != nil {
			g.collectEvaluatedFromNested(resolved, names, patterns, &allEvaluated)
		}
	}
	if s.DynamicRef != "" {
		if resolved := g.resolveDynamicRef(s.DynamicRef, s); resolved != nil {
			g.collectEvaluatedFromNested(resolved, names, patterns, &allEvaluated)
		}
	}

	// allOf within this sub-schema.
	for _, sub := range s.AllOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		g.collectEvaluatedFromNested(resolved, names, patterns, &allEvaluated)
	}

	isForbidden := uneval.IsBooleanSchema() && (uneval.BooleanSchema == nil || !*uneval.BooleanSchema)

	return &CousinUnevalCheck{
		IsForbidden:    isForbidden,
		EvaluatedNames: sortedKeys(names),
		EvalPatterns:   sortedKeys(patterns),
		AllEvaluated:   allEvaluated,
	}
}

// buildTupleItemDefs extracts per-position type definitions for tuple-form arrays
// (prefixItems in draft 2020-12, or items-as-array in draft 4-7).
// For each position, it resolves the schema (following $ref if needed),
// determines the Go type name, and records it for per-position validation.
// Returns nil if the schema has no tuple items or if no position has a validatable type.
func (g *Generator) buildTupleItemDefs(s *schema.Schema, parentName string) []TupleItemDef {
	var positionSchemas []*schema.Schema

	// Draft 2020-12: prefixItems
	if len(s.PrefixItems) > 0 && g.supportsPrefixItems(s) {
		positionSchemas = s.PrefixItems
	}
	// Draft 4-7: items as array of schemas
	if len(positionSchemas) == 0 && s.Items != nil && len(s.Items.Schemas) > 0 {
		positionSchemas = s.Items.Schemas
	}

	if len(positionSchemas) == 0 {
		return nil
	}

	var tupleItems []TupleItemDef
	hasValidatable := false

	for i, posSch := range positionSchemas {
		if posSch == nil {
			tupleItems = append(tupleItems, TupleItemDef{})
			continue
		}

		// Boolean false schema — reject any value at this position.
		if posSch.IsFalseSchema() {
			tupleItems = append(tupleItems, TupleItemDef{IsFalse: true})
			hasValidatable = true
			continue
		}

		// Boolean true schema — no constraint.
		if posSch.IsTrueSchema() {
			tupleItems = append(tupleItems, TupleItemDef{})
			continue
		}

		// Resolve $ref chain to find the target schema and its generated type name.
		resolved := posSch
		refName := ""
		if ref := posSch.EffectiveRef(); ref != "" {
			if r := g.resolveRefInContext(ref, posSch); r != nil {
				resolved = r
				refName = g.goNameForResolvedRef(ref, resolved, refToGoName(ref))
			}
		}

		// Ensure the ref target type is generated. This is safe from infinite
		// recursion because the caller (generateTypeDef for an array) marks the
		// parent type as generated BEFORE calling buildTupleItemDefs.
		if refName != "" {
			_ = g.generateTypeDef(refName, resolved)
			if g.generated[refName] {
				tupleItems = append(tupleItems, TupleItemDef{TypeName: refName})
				hasValidatable = true
				continue
			}
		}

		// Non-ref position schema: for schemas with structural keywords (type,
		// properties, etc.), generate a named type so positional validation works.
		if hasStructuralKeywords(resolved) {
			posName := fmt.Sprintf("%sItem%d", parentName, i)
			_ = g.generateTypeDef(posName, resolved)
			if g.generated[posName] {
				tupleItems = append(tupleItems, TupleItemDef{TypeName: posName})
				hasValidatable = true
				continue
			}
		}

		// Simple type-only schema (no structural keywords) — record the JSON type
		// for lightweight runtime type checking.
		if jsonType := primarySchemaType(resolved); jsonType != "" {
			tupleItems = append(tupleItems, TupleItemDef{JSONType: jsonType})
			hasValidatable = true
			continue
		}

		tupleItems = append(tupleItems, TupleItemDef{})
	}

	if !hasValidatable {
		return nil
	}
	return tupleItems
}

// sortedKeys returns the sorted keys of a map[string]*schema.Schema.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// hasStructuralKeywords returns true if the schema has keywords that would
// produce a meaningful Go type with validation (properties, type constraints,
// validation keywords, etc.). Used to decide whether an inline tuple position
// schema is worth generating as a named type.
func hasStructuralKeywords(s *schema.Schema) bool {
	if s == nil || s.IsBooleanSchema() {
		return false
	}
	// Object with properties
	if len(s.Properties) > 0 {
		return true
	}
	// Has required fields
	if len(s.Required) > 0 {
		return true
	}
	// Typed schema with validation keywords
	if len(s.Type) > 0 {
		// Check for string constraints
		if s.MinLength != nil || s.MaxLength != nil || (s.Pattern != nil && *s.Pattern != "") {
			return true
		}
		// Numeric constraints
		if s.Minimum != nil || s.Maximum != nil || s.MultipleOf != nil {
			return true
		}
		if s.ExclusiveMinimum != nil || s.ExclusiveMaximum != nil {
			return true
		}
		// Enum/const
		if len(s.Enum) > 0 || s.Const != nil {
			return true
		}
		// Object type with properties or composition
		if len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 {
			return true
		}
	}
	// Composition keywords at top level
	if len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 {
		return true
	}
	return false
}
