package generator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// Generator converts a parsed Schema into IR types.
type Generator struct {
	config    Config
	output    *File
	generated map[string]bool // track already-generated type names
	defs      map[string]*schema.Schema
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

	// Collect definitions ($defs and definitions).
	g.defs = make(map[string]*schema.Schema)
	for name, def := range s.Defs {
		g.defs["#/$defs/"+name] = def
	}
	for name, def := range s.Definitions {
		g.defs["#/definitions/"+name] = def
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

	// Process the root type if it defines an object, has a title, or uses composition.
	if hasProperties(s) || s.Title != "" || len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 {
		rootName := "Root"
		if s.Title != "" {
			rootName = SchemaNameToGoName(s.Title)
		}
		if err := g.generateTypeDef(rootName, s); err != nil {
			return nil, fmt.Errorf("generating root type: %w", err)
		}
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

	for _, td := range g.output.TypeDefs {
		if sd, ok := td.(*StructDef); ok {
			if len(sd.OneOfs) > 0 {
				needsJSON = true
				needsFmt = true
			}
			if sd.AdditionalProperties != nil {
				needsJSON = true
				needsFmt = true
			}
			if len(sd.Validations) > 0 {
				needsFmt = true
				for _, v := range sd.Validations {
					if v.RuleType == "pattern" {
						needsRegexp = true
					}
				}
			}
			for _, f := range sd.Fields {
				if usesTimeType(f.Type) {
					needsTime = true
				}
			}
		}
		if ad, ok := td.(*AliasDef); ok {
			if usesTimeType(ad.Underlying) {
				needsTime = true
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

	// Object with properties (may also have oneOf fields) → struct
	if hasProperties(s) || len(s.OneOf) > 0 {
		return g.generateStructDef(name, s)
	}

	// Ref only → alias
	if s.Ref != "" {
		resolved := g.resolveRef(s.Ref)
		if resolved != nil {
			refName := refToGoName(s.Ref)
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
				Name:        name,
				Underlying:  &NamedType{Name: refName},
				Description: s.Description,
			})
			return nil
		}
	}

	// Simple primitive type → alias
	primaryType := primarySchemaType(s)
	if primaryType != "" && primaryType != "object" && primaryType != "array" {
		goType := g.resolveType(s, name)
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
			Name:        name,
			Underlying:  goType,
			Description: s.Description,
		})
		return nil
	}

	// Array type → alias
	if primaryType == "array" {
		goType := g.resolveType(s, name)
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
			Name:        name,
			Underlying:  goType,
			Description: s.Description,
		})
		return nil
	}

	// Object with no properties → alias to map[string]any
	if primaryType == "object" {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
			Name: name,
			Underlying: &MapType{
				KeyType:   &PrimitiveType{Name: "string"},
				ValueType: &PrimitiveType{Name: "any"},
			},
			Description: s.Description,
		})
		return nil
	}

	// Fallback: alias to any
	g.generated[name] = true
	g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
		Name:        name,
		Underlying:  &PrimitiveType{Name: "any"},
		Description: s.Description,
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

	for _, propName := range propNames {
		propSchema := s.Properties[propName]
		goFieldName := JSONPropertyToGoName(propName)
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

		goType, err := g.resolvePropertyType(propSchema, name, goFieldName)
		if err != nil {
			return fmt.Errorf("property %s: %w", propName, err)
		}

		omitEmpty := g.config.OmitEmpty && !required

		fields = append(fields, FieldDef{
			Name:        goFieldName,
			JSONName:    propName,
			Type:        goType,
			OmitEmpty:   omitEmpty,
			Required:    required,
			Description: propSchema.Description,
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

	// Handle additionalProperties
	var additionalProps *AdditionalPropertiesDef
	if s.AdditionalProperties != nil {
		if s.AdditionalProperties.Bool != nil && *s.AdditionalProperties.Bool {
			additionalProps = &AdditionalPropertiesDef{
				ValueType: &PrimitiveType{Name: "any"},
			}
			needsMarshal = true
			needsUnmarshal = true
		} else if s.AdditionalProperties.Schema != nil {
			valueType := g.resolveType(s.AdditionalProperties.Schema, name+"Value")
			additionalProps = &AdditionalPropertiesDef{
				ValueType: valueType,
			}
			needsMarshal = true
			needsUnmarshal = true
		}
	}

	// Collect validation rules
	var validations []ValidationRule
	for _, propName := range propNames {
		propSchema := s.Properties[propName]
		if propSchema == nil {
			continue
		}
		goFieldName := JSONPropertyToGoName(propName)
		validations = append(validations, extractValidationRules(goFieldName, propName, propSchema)...)
	}

	structDef := &StructDef{
		Name:                 name,
		Description:          s.Description,
		Fields:               fields,
		OneOfs:               oneOfs,
		AdditionalProperties: additionalProps,
		Validations:          validations,
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

	// Merge each allOf sub-schema.
	for _, sub := range s.AllOf {
		resolved := sub
		if sub.Ref != "" {
			if r := g.resolveRef(sub.Ref); r != nil {
				resolved = r
			}
		}
		for k, v := range resolved.Properties {
			merged.Properties[k] = v
		}
		merged.Required = append(merged.Required, resolved.Required...)
		// If the sub-schema itself has a type, propagate it.
		if len(resolved.Type) > 0 && len(merged.Type) == 0 {
			merged.Type = resolved.Type
		}
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
	for _, variant := range nonNullVariants {
		result, err := g.resolveOneOfVariant(variant, parentName, goFieldName)
		if err != nil {
			return nil, err
		}

		wrapperName := ToOneOfWrapperName(parentName, result.Name)

		variants = append(variants, OneOfVariant{
			WrapperName:    wrapperName,
			FieldName:      result.Name,
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
func (g *Generator) resolveOneOfVariant(variant *schema.Schema, parentName, fieldName string) (oneOfVariantResult, error) {
	// $ref variant → use the referenced type
	if variant.Ref != "" {
		goName := refToGoName(variant.Ref)
		refSchema := g.resolveRef(variant.Ref)
		if refSchema != nil {
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

	// Inline object variant → create a named type
	if hasProperties(variant) {
		variantName := parentName + fieldName + "Variant"
		if variant.Title != "" {
			variantName = SchemaNameToGoName(variant.Title)
		}
		if err := g.generateTypeDef(variantName, variant); err != nil {
			return oneOfVariantResult{}, err
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

	return oneOfVariantResult{Name: "Unknown", Type: &PrimitiveType{Name: "any"}}, nil
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
// additional TypeDefs for nested objects.
func (g *Generator) resolvePropertyType(s *schema.Schema, parentName, fieldName string) (GoType, error) {
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
			if variant.Ref != "" {
				goName := refToGoName(variant.Ref)
				if refSchema := g.resolveRef(variant.Ref); refSchema != nil {
					if err := g.generateTypeDef(goName, refSchema); err != nil {
						return nil, err
					}
				}
				return &PointerType{Inner: &NamedType{Name: goName}}, nil
			}
			// Inline variant
			innerType, err := g.resolvePropertyType(variant, parentName, fieldName)
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

	// $ref
	if s.Ref != "" {
		goName := refToGoName(s.Ref)
		// Ensure the referenced type gets generated.
		if refSchema := g.resolveRef(s.Ref); refSchema != nil {
			if err := g.generateTypeDef(goName, refSchema); err != nil {
				return nil, err
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

	// $ref
	if s.Ref != "" {
		goName := refToGoName(s.Ref)
		if refSchema := g.resolveRef(s.Ref); refSchema != nil {
			_ = g.generateTypeDef(goName, refSchema)
		}
		return &NamedType{Name: goName}
	}

	primaryType := primarySchemaType(s)

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

// resolveRef looks up a $ref path in the collected definitions.
func (g *Generator) resolveRef(ref string) *schema.Schema {
	if s, ok := g.defs[ref]; ok {
		return s
	}
	return nil
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

// ---------- helpers ----------

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

// refToGoName extracts the Go type name from a $ref string.
// e.g. "#/$defs/my-type" → "MyType", "#/definitions/Address" → "Address"
func refToGoName(ref string) string {
	parts := strings.Split(ref, "/")
	last := parts[len(parts)-1]
	return SchemaNameToGoName(last)
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
