// Package validationruntime contains shared primitives used by generated
// validators when JSON Schema behavior requires runtime annotation tracking.
package validationruntime

import (
	"encoding/json"
	"fmt"
	"sort"
)

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

// EvalState tracks annotations produced while validating one JSON value.
type EvalState struct {
	EvaluatedProperties map[string]bool
	EvaluatedItems      map[int]bool
}

// NewEvalState creates an empty evaluation annotation state.
func NewEvalState() *EvalState {
	return &EvalState{
		EvaluatedProperties: make(map[string]bool),
		EvaluatedItems:      make(map[int]bool),
	}
}

func (s *EvalState) MarkProperty(name string) {
	if s != nil {
		s.EvaluatedProperties[name] = true
	}
}

func (s *EvalState) MarkItem(index int) {
	if s != nil {
		s.EvaluatedItems[index] = true
	}
}

func (s *EvalState) Merge(other *EvalState) {
	if s == nil || other == nil {
		return
	}
	for name := range other.EvaluatedProperties {
		s.EvaluatedProperties[name] = true
	}
	for index := range other.EvaluatedItems {
		s.EvaluatedItems[index] = true
	}
}

func (s *EvalState) UnevaluatedProperties(obj map[string]json.RawMessage) []string {
	if s == nil || len(obj) == 0 {
		return nil
	}
	var out []string
	for name := range obj {
		if !s.EvaluatedProperties[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (s *EvalState) UnevaluatedItems(length int) []int {
	if s == nil || length <= 0 {
		return nil
	}
	out := make([]int, 0)
	for i := 0; i < length; i++ {
		if !s.EvaluatedItems[i] {
			out = append(out, i)
		}
	}
	return out
}

// Result is the runtime validation result for one schema application.
type Result struct {
	Valid bool
	State *EvalState
	Err   error
}

func ValidResult(state *EvalState) Result {
	if state == nil {
		state = NewEvalState()
	}
	return Result{Valid: true, State: state}
}

func InvalidResult(err error) Result {
	return Result{Valid: false, State: NewEvalState(), Err: err}
}
