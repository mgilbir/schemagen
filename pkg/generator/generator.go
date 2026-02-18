package generator

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// Generator converts a parsed Schema into IR types.
type Generator struct {
	config       Config
	output       *File
	generated    map[string]bool // track already-generated type names
	defs         map[string]*schema.Schema
	rootTypeName string                // Go type name for the root schema
	rootID       string                // $id of the root schema (for detecting self-references)
	anchors      map[string]string     // anchor/id → def ref path (e.g., "#something" → "#/definitions/bar")
	resolver     schema.SchemaResolver // external resolver for non-local refs
	baseURI      *url.URL              // base URI for the root document (from $id or file path)
	rootSchema   *schema.Schema        // the root schema for local ref resolution

	// documentRoots maps canonical $id URIs to the schema nodes that declare them.
	// This enables scoped resolution: when a subschema has $id, $ref: "#/..."
	// within it resolves against that subschema, not the top-level root.
	documentRoots map[string]*schema.Schema
}

// New creates a new Generator with the given configuration.
func New(cfg Config) *Generator {
	return &Generator{
		config:    cfg,
		generated: make(map[string]bool),
	}
}

// Generate processes a schema and returns the IR File.
func (g *Generator) Generate(s *schema.Schema) (*File, error) {
	g.output = &File{
		PackageName: g.config.PackageName,
	}
	g.generated = make(map[string]bool)
	g.rootSchema = s

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

	// Compute effective base URIs and document roots for all schema nodes.
	// This enables scoped $id resolution: subschemas with $id change the
	// base URI for relative refs within their scope.
	s.ComputeBaseURIs(g.baseURI, s)
	g.documentRoots = make(map[string]*schema.Schema)
	g.buildDocumentRoots(s)

	// Store the external resolver from config (may be nil).
	g.resolver = g.config.Resolver

	// Collect definitions ($defs and definitions) and build anchor index.
	g.defs = make(map[string]*schema.Schema)
	g.anchors = make(map[string]string)
	for name, def := range s.Defs {
		refPath := "#/$defs/" + name
		g.defs[refPath] = def
		g.indexAnchors(def, refPath)
	}
	for name, def := range s.Definitions {
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

	// Process the root type. This handles objects, compositions, primitive types
	// with validation constraints, enums, arrays, and any other schema that can
	// produce a Go type definition.
	if err := g.generateTypeDef(g.rootTypeName, s); err != nil {
		return nil, fmt.Errorf("generating root type: %w", err)
	}

	// Add imports based on what was generated.
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

	for _, td := range g.output.TypeDefs {
		if sd, ok := td.(*StructDef); ok {
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
				// fmt is only needed for non-RawMessage additional properties (typed maps)
				// because the marshal template uses fmt.Errorf for marshaling errors.
				if sd.AdditionalProperties.ValueType.GoTypeName() != "json.RawMessage" {
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
				}
			}
			if len(sd.PatternProperties) > 0 {
				needsRegexp = true
				needsJSON = true
			}
			for _, f := range sd.Fields {
				if usesTimeType(f.Type) {
					needsTime = true
				}
				if usesJSONType(f.Type) {
					needsJSON = true
				}
			}
		}
		if ad, ok := td.(*AliasDef); ok {
			if usesTimeType(ad.Underlying) {
				needsTime = true
			}
			if usesJSONType(ad.Underlying) {
				needsJSON = true
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
		g.output.Imports = append(g.output.Imports, Import{Path: "regexp"})
	}
	if needsMath {
		g.output.Imports = append(g.output.Imports, Import{Path: "math"})
	}
	if needsTime {
		g.output.Imports = append(g.output.Imports, Import{Path: "time"})
	}
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

// generateTypeDef creates the appropriate TypeDef for a schema and adds it to
// the output File. It skips schemas that have already been generated.
func (g *Generator) generateTypeDef(name string, s *schema.Schema) error {
	if g.generated[name] {
		return nil
	}

	// Enum type
	if len(s.Enum) > 0 {
		return g.generateEnumDef(name, s)
	}

	// allOf → merge all sub-schemas into one struct
	if len(s.AllOf) > 0 {
		return g.generateAllOfDef(name, s)
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

	// Object with properties (may also have oneOf fields) → struct
	if hasProperties(s) || len(s.OneOf) > 0 {
		return g.generateStructDef(name, s)
	}

	// Ref only → alias (handles $ref, $recursiveRef, $dynamicRef)
	if effRef := s.EffectiveRef(); effRef != "" {
		resolved := g.resolveRefInContext(effRef, s)
		if resolved != nil {
			refName := g.goNameForResolvedRef(effRef, resolved, refToGoName(effRef))
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
				Name:        name,
				Underlying:  &NamedType{Name: refName},
				Description: s.Description,
			})
			return nil
		}
	}

	// Simple primitive type → alias (or defined type if it has validation constraints)
	// When no explicit type is declared, infer from constraint keywords.
	primaryType := primarySchemaType(s)
	if primaryType == "" {
		primaryType = inferTypeFromConstraints(s)
	}
	if primaryType != "" && primaryType != "object" && primaryType != "array" {
		goType := g.resolveType(s, name)
		rules := extractAliasValidationRules(s, goType)
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
			Name:        name,
			Underlying:  goType,
			Description: s.Description,
			Validations: rules,
		})
		return nil
	}

	// Array type → alias (or defined type if it has validation constraints)
	if primaryType == "array" {
		goType := g.resolveType(s, name)
		rules := extractAliasValidationRules(s, goType)
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
			Name:        name,
			Underlying:  goType,
			Description: s.Description,
			Validations: rules,
		})
		return nil
	}

	// Object with no properties → struct with overflow map for lossless round-trip.
	// If additionalProperties is explicitly false, generate an empty struct.
	if primaryType == "object" {
		g.generated[name] = true
		var additionalProps *AdditionalPropertiesDef
		if s.AdditionalProperties != nil && s.AdditionalProperties.Bool != nil && !*s.AdditionalProperties.Bool {
			// additionalProperties: false → no overflow map
		} else if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
			valueType := g.resolveType(s.AdditionalProperties.Schema, name+"Value")
			additionalProps = &AdditionalPropertiesDef{ValueType: valueType}
		} else {
			// Default or additionalProperties: true → json.RawMessage overflow map
			additionalProps = &AdditionalPropertiesDef{
				ValueType: &PrimitiveType{Name: "json.RawMessage"},
			}
		}
		needsMarshal := additionalProps != nil
		needsUnmarshal := additionalProps != nil
		g.output.TypeDefs = append(g.output.TypeDefs, &StructDef{
			Name:                 name,
			Description:          s.Description,
			AdditionalProperties: additionalProps,
			NeedsMarshal:         needsMarshal,
			NeedsUnmarshal:       needsUnmarshal,
		})
		return nil
	}

	// Fallback: alias to any
	goType := &PrimitiveType{Name: "any"}
	rules := extractAliasValidationRules(s, goType)
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
func (g *Generator) generateStructDef(name string, s *schema.Schema) error {
	g.generated[name] = true

	requiredSet := make(map[string]bool, len(s.Required))
	for _, r := range s.Required {
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

		fields = append(fields, FieldDef{
			Name:        goFieldName,
			JSONName:    propName,
			Type:        goType,
			OmitEmpty:   omitEmpty,
			Required:    required,
			Description: propSchema.Description,
			ManualJSON:  manualJSON,
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
			}
			// additionalProperties: false → no overflow map (strict)
		} else if s.AdditionalProperties.Schema != nil {
			valueType := g.resolveType(s.AdditionalProperties.Schema, name+"Value")
			additionalProps = &AdditionalPropertiesDef{
				ValueType: valueType,
			}
			needsMarshal = true
			needsUnmarshal = true
		}
	} else if !g.config.StrictProperties && len(fields) > 0 {
		// No additionalProperties specified: per JSON Schema spec, defaults to true.
		// In non-strict mode, add an overflow map to preserve extra properties.
		// Only add when there are declared fields (otherwise it's a bare object schema
		// and we're not generating anything useful yet).
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
	// These are checked via the _present set populated during UnmarshalJSON.
	var requiredJSON []string
	for _, f := range fields {
		if f.Required {
			requiredJSON = append(requiredJSON, f.JSONName)
		}
	}

	for _, propName := range propNames {
		propSchema := s.Properties[propName]
		if propSchema == nil {
			continue
		}
		goFieldName := goFieldNames[propName]
		rules := extractValidationRules(goFieldName, propName, propSchema)
		// Filter out rules that don't make sense for the Go type (e.g.,
		// minimum/maximum on an 'any' field can't be compiled).
		filtered := rules[:0]
		for i := range rules {
			if pointerFields[rules[i].FieldName] {
				rules[i].IsPointer = true
			}
			// Skip numeric/string/array validation on untyped 'any' fields.
			if ft, ok := fieldTypes[rules[i].FieldName]; ok {
				if pt, isPrim := ft.(*PrimitiveType); isPrim && pt.Name == "any" {
					continue
				}
			}
			filtered = append(filtered, rules[i])
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
		patternProps = append(patternProps, PatternPropertyDef{Pattern: pattern})
	}
	if len(patternProps) > 0 {
		needsMarshal = true
		needsUnmarshal = true
	}

	// Enable custom unmarshal if there are required fields (to track key presence).
	if len(requiredJSON) > 0 {
		needsUnmarshal = true
	}

	structDef := &StructDef{
		Name:                 name,
		Description:          s.Description,
		Fields:               fields,
		OneOfs:               oneOfs,
		AdditionalProperties: additionalProps,
		PatternProperties:    patternProps,
		Validations:          validations,
		RequiredJSON:         requiredJSON,
		NeedsMarshal:         needsMarshal,
		NeedsUnmarshal:       needsUnmarshal,
	}
	g.output.TypeDefs = append(g.output.TypeDefs, structDef)
	return nil
}

// generateAllOfDef merges all allOf sub-schemas into a single struct.
func (g *Generator) generateAllOfDef(name string, s *schema.Schema) error {
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
	merged.Required = append(merged.Required, s.Required...)

	// Merge each allOf sub-schema, recursively flattening nested allOf chains.
	g.mergeAllOfInto(merged, s.AllOf)

	return g.generateStructDef(name, merged)
}

// mergeAllOfInto recursively merges properties and required fields from allOf
// sub-schemas into the target schema. This handles cases like remote schemas
// that themselves contain allOf with internal $ref chains.
func (g *Generator) mergeAllOfInto(target *schema.Schema, allOf []*schema.Schema) {
	for _, sub := range allOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		// Copy direct properties.
		for k, v := range resolved.Properties {
			target.Properties[k] = v
		}
		target.Required = append(target.Required, resolved.Required...)
		// Propagate type from sub-schemas if the target doesn't have one.
		if len(resolved.Type) > 0 && len(target.Type) == 0 {
			target.Type = resolved.Type
		}
		// Recursively merge nested allOf chains.
		if len(resolved.AllOf) > 0 {
			g.mergeAllOfInto(target, resolved.AllOf)
		}
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

	return g.generateStructDef(name, merged)
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

	return &OneOfDef{
		InterfaceName: interfaceName,
		FieldName:     goFieldName,
		JSONName:      jsonName,
		Variants:      variants,
	}, nil
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

	baseType := g.resolveBaseType(s)

	var values []EnumValue
	for _, v := range s.Enum {
		constName := name + enumValueSuffix(v)
		values = append(values, EnumValue{
			Name:  constName,
			Value: v,
		})
	}

	g.output.TypeDefs = append(g.output.TypeDefs, &EnumDef{
		Name:        name,
		BaseType:    baseType,
		Values:      values,
		Description: s.Description,
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

	// Inline enum → generate enum type
	if len(s.Enum) > 0 {
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
		if refSchema := g.resolveRefInContext(effRef, s); refSchema != nil {
			goName = g.goNameForResolvedRef(effRef, refSchema, goName)
			if err := g.generateTypeDef(goName, refSchema); err != nil {
				return nil, err
			}
			// If the ref resolves to its own enclosing document root, use a pointer.
			if g.isScopedSelfRef(effRef, s, refSchema) {
				return &PointerType{Inner: &NamedType{Name: goName}}, nil
			}
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

	// Check for format: "date-time" on string types → time.Time
	if s.Format != nil && *s.Format == "date-time" && primarySchemaType(s) == "string" {
		return &PrimitiveType{Name: "time.Time"}, nil
	}

	return g.resolveType(s, parentName+fieldName), nil
}

// resolveType converts a schema to a GoType, creating nested types if needed.
func (g *Generator) resolveType(s *schema.Schema, contextName string) GoType {
	if s == nil {
		return &PrimitiveType{Name: "any"}
	}

	// Inline enum
	if len(s.Enum) > 0 {
		enumName := contextName
		_ = g.generateEnumDef(enumName, s)
		return &NamedType{Name: enumName}
	}

	// $ref / $recursiveRef / $dynamicRef
	if effRef := s.EffectiveRef(); effRef != "" {
		if g.isSelfRefInContext(effRef, s) {
			if g.rootIsObjectType() {
				return &PointerType{Inner: &NamedType{Name: g.rootTypeName}}
			}
			return &PrimitiveType{Name: "json.RawMessage"}
		}
		goName := refToGoName(effRef)
		if refSchema := g.resolveRefInContext(effRef, s); refSchema != nil {
			// If the ref resolved to a scoped document root (not the main root),
			// derive the Go name from that schema rather than the raw ref string.
			// This handles $ref: "#" inside a sub-schema with its own $id.
			goName = g.goNameForResolvedRef(effRef, refSchema, goName)
			_ = g.generateTypeDef(goName, refSchema)
			// If the ref resolves to its own enclosing document root, it's a
			// local self-reference within a scoped $id context. Use a pointer
			// to break the Go recursive type cycle.
			if g.isScopedSelfRef(effRef, s, refSchema) {
				return &PointerType{Inner: &NamedType{Name: goName}}
			}
		}
		return &NamedType{Name: goName}
	}

	primaryType := primarySchemaType(s)
	if primaryType == "" {
		primaryType = inferTypeFromConstraints(s)
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

	// Object with properties → nested struct
	if primaryType == "object" && hasProperties(s) {
		_ = g.generateTypeDef(contextName, s)
		return &NamedType{Name: contextName}
	}

	// Array with items
	if primaryType == "array" && s.Items != nil && s.Items.Schema != nil {
		itemType := g.resolveType(s.Items.Schema, contextName+"Item")
		return &ArrayType{ItemType: itemType}
	}

	// Primitive or default
	if primaryType != "" {
		// Check for format: "date-time" on string types → time.Time
		if primaryType == "string" && s.Format != nil && *s.Format == "date-time" {
			return &PrimitiveType{Name: "time.Time"}
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

// indexAnchors records the $id and $anchor of a definition for anchor-based resolution.
// It stores both the raw $id value and the canonicalized (resolved against base URI)
// form so that both relative and absolute lookups succeed.
func (g *Generator) indexAnchors(def *schema.Schema, refPath string) {
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
		g.anchors["#"+def.Anchor] = refPath
	}
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
	return false
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
	return primarySchemaType(g.rootSchema) == "object"
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

// hasProperties returns true if the schema defines any properties.
func hasProperties(s *schema.Schema) bool {
	return len(s.Properties) > 0
}

// primarySchemaType returns the primary (first non-null) type from the type list.
func primarySchemaType(s *schema.Schema) string {
	for _, t := range s.Type {
		if t != "null" {
			return t
		}
	}
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
func inferTypeFromConstraints(s *schema.Schema) string {
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
	// Object constraints → object
	if s.MinProperties != nil || s.MaxProperties != nil {
		return "object"
	}
	return ""
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
		return fmt.Sprintf("%v", val)
	default:
		return fmt.Sprintf("%v", val)
	}
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
	return rules
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

// sortedKeys returns the sorted keys of a map[string]*schema.Schema.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
