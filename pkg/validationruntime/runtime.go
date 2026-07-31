// Package validationruntime holds the types that generated validators embed to
// describe their own validation completeness.
//
// Generated code calls SchemagenValidationCapability() to report which JSON
// Schema behaviors a schema uses that static Go checks cannot fully express, so
// a caller needing strict spec compliance can detect the gap. The package does
// not perform validation itself — generated validators are self-contained.
package validationruntime

import "fmt"

// Feature identifies a schema behavior that may require runtime validation state.
type Feature string

const (
	FeatureDynamicRef       Feature = "$dynamicRef"
	FeatureRecursiveRef     Feature = "$recursiveRef"
	FeatureUnevaluatedItems Feature = "unevaluatedItems"
	FeatureUnevaluatedProps Feature = "unevaluatedProperties"
	FeatureCrossDraftRef    Feature = "cross-draft $ref"
	FeatureCustomVocabulary Feature = "custom vocabulary"
)

// Capability describes the validation completeness of generated code.
type Capability struct {
	Mode            string
	RequiresRuntime bool
	RuntimeFeatures []Feature
	Unsupported     []Feature
	ResourceCount   int
}

// Check reports unsupported features. It intentionally does not reject runtime
// features: generated static validation still runs first, and callers can inspect
// Capability when they need strict spec-compliance guarantees.
func (c Capability) Check() error {
	if len(c.Unsupported) == 0 {
		return nil
	}
	return fmt.Errorf("validation has unsupported JSON Schema features: %v", c.Unsupported)
}
