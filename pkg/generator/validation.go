package generator

import "github.com/mgilbir/schemagen/pkg/schema"

// ValidationMode controls how generated Validate methods handle constraints.
type ValidationMode string

const (
	// ValidationModeStatic emits only direct generated checks. This preserves the
	// historical behavior and keeps generated code dependency-free.
	ValidationModeStatic ValidationMode = "static"

	// ValidationModeHybrid emits direct checks and enables runtime hooks for schema
	// features that need annotation tracking or dynamic scope.
	ValidationModeHybrid ValidationMode = "hybrid"

	// ValidationModeRuntime reserves room for full runtime validation. The current
	// implementation uses the same hooks as hybrid mode, but records intent in the
	// generated metadata.
	ValidationModeRuntime ValidationMode = "runtime"
)

// NormalizedValidationMode returns a supported mode, defaulting to static.
func NormalizedValidationMode(mode ValidationMode) ValidationMode {
	switch mode {
	case ValidationModeHybrid, ValidationModeRuntime:
		return mode
	default:
		return ValidationModeStatic
	}
}

// ValidationFeature identifies schema semantics that cannot always be reduced to
// static Go type checks.
type ValidationFeature string

const (
	ValidationFeatureDynamicRef       ValidationFeature = "$dynamicRef"
	ValidationFeatureRecursiveRef     ValidationFeature = "$recursiveRef"
	ValidationFeatureUnevaluatedItems ValidationFeature = "unevaluatedItems"
	ValidationFeatureUnevaluatedProps ValidationFeature = "unevaluatedProperties"
	ValidationFeatureCrossDraftRef    ValidationFeature = "cross-draft $ref"
	ValidationFeatureCustomVocabulary ValidationFeature = "custom vocabulary"
)

// ValidationCapability summarizes how complete generated validation is for a
// schema. The emitter embeds this in generated source for users and tests.
type ValidationCapability struct {
	Mode               ValidationMode
	RequiresRuntime    bool
	Unsupported        []ValidationFeature
	RuntimeFeatures    []ValidationFeature
	ResourceCount      int
	CrossDraftResource bool
}

func (c ValidationCapability) HasLimitations() bool {
	return len(c.Unsupported) > 0 || len(c.RuntimeFeatures) > 0 || c.CrossDraftResource
}

func analyzeValidationCapability(root *schema.Schema, graph *schema.ResourceGraph, mode ValidationMode) ValidationCapability {
	capability := ValidationCapability{Mode: NormalizedValidationMode(mode)}
	if graph != nil {
		capability.ResourceCount = len(graph.Resources)
		capability.CrossDraftResource = hasCrossDraftResources(graph)
		if capability.CrossDraftResource {
			capability.RuntimeFeatures = appendFeature(capability.RuntimeFeatures, ValidationFeatureCrossDraftRef)
		}
	}
	collectValidationFeatures(root, &capability)
	capability.RequiresRuntime = len(capability.RuntimeFeatures) > 0
	return capability
}

func hasCrossDraftResources(graph *schema.ResourceGraph) bool {
	var draft schema.Draft
	for _, uri := range graph.SortedResourceURIs() {
		res := graph.Resources[uri]
		if res == nil || res.Draft == schema.DraftUnknown {
			continue
		}
		if draft == schema.DraftUnknown {
			draft = res.Draft
			continue
		}
		if res.Draft != draft {
			return true
		}
	}
	return false
}

func collectValidationFeatures(s *schema.Schema, capability *ValidationCapability) {
	if s == nil || s.IsBooleanSchema() {
		return
	}
	if s.DynamicRef != "" {
		capability.RuntimeFeatures = appendFeature(capability.RuntimeFeatures, ValidationFeatureDynamicRef)
	}
	if s.RecursiveRef != "" {
		capability.RuntimeFeatures = appendFeature(capability.RuntimeFeatures, ValidationFeatureRecursiveRef)
	}
	if s.UnevaluatedItems != nil {
		capability.RuntimeFeatures = appendFeature(capability.RuntimeFeatures, ValidationFeatureUnevaluatedItems)
	}
	if s.UnevaluatedProperties != nil {
		capability.RuntimeFeatures = appendFeature(capability.RuntimeFeatures, ValidationFeatureUnevaluatedProps)
	}
	if len(s.Vocabulary) > 0 {
		capability.Unsupported = appendFeature(capability.Unsupported, ValidationFeatureCustomVocabulary)
	}
	for _, child := range validationChildren(s) {
		collectValidationFeatures(child, capability)
	}
}

func validationChildren(s *schema.Schema) []*schema.Schema {
	var out []*schema.Schema
	for _, key := range sortedKeys(s.Properties) {
		out = append(out, s.Properties[key])
	}
	out = append(out, s.TypeSchemas...)
	for _, key := range sortedKeys(s.PatternProperties) {
		out = append(out, s.PatternProperties[key])
	}
	for _, key := range sortedKeys(s.Defs) {
		out = append(out, s.Defs[key])
	}
	for _, key := range sortedKeys(s.Definitions) {
		out = append(out, s.Definitions[key])
	}
	out = append(out, s.AllOf...)
	out = append(out, s.AnyOf...)
	out = append(out, s.OneOf...)
	if s.Not != nil {
		out = append(out, s.Not)
	}
	if s.If != nil {
		out = append(out, s.If)
	}
	if s.Then != nil {
		out = append(out, s.Then)
	}
	if s.Else != nil {
		out = append(out, s.Else)
	}
	if s.Items != nil {
		if s.Items.Schema != nil {
			out = append(out, s.Items.Schema)
		}
		out = append(out, s.Items.Schemas...)
	}
	out = append(out, s.PrefixItems...)
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		out = append(out, s.AdditionalProperties.Schema)
	}
	if s.AdditionalItems != nil && s.AdditionalItems.Schema != nil {
		out = append(out, s.AdditionalItems.Schema)
	}
	if s.Contains != nil {
		out = append(out, s.Contains)
	}
	for _, key := range sortedKeys(s.DependentSchemas) {
		out = append(out, s.DependentSchemas[key])
	}
	if s.PropertyNames != nil {
		out = append(out, s.PropertyNames)
	}
	if s.UnevaluatedItems != nil {
		out = append(out, s.UnevaluatedItems)
	}
	if s.UnevaluatedProperties != nil {
		out = append(out, s.UnevaluatedProperties)
	}
	if s.ContentSchema != nil {
		out = append(out, s.ContentSchema)
	}
	return out
}

func appendFeature(features []ValidationFeature, feature ValidationFeature) []ValidationFeature {
	for _, existing := range features {
		if existing == feature {
			return features
		}
	}
	return append(features, feature)
}
