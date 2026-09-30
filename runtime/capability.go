package runtime

import "fmt"

// The types generated validators embed to describe their own validation
// completeness. A package generated under --validation hybrid or runtime, whose
// schemas use a behavior static Go checks cannot fully express, declares
// SchemagenValidationCapability() returning a Capability, so that a caller
// needing strict spec compliance can detect the gap. Nothing here performs
// validation itself.

// Feature identifies a schema behavior that may require runtime validation state.
type Feature string

// The features a generated file can record in its capability: the schema
// behaviours that may need runtime state to be judged completely, or that the
// generator cannot judge at all (Unsupported).
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
	RuntimeFeatures []Feature
	Unsupported     []Feature
	ResourceCount   int
	RequiresRuntime bool
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
