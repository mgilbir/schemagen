package validationruntime

import (
	"strings"
	"testing"
)

func TestCapabilityCheck(t *testing.T) {
	tests := []struct {
		name       string
		capability Capability
		wantErr    bool
	}{
		{
			name:       "empty capability is fine",
			capability: Capability{},
		},
		{
			// Runtime features are deliberately not an error: static validation
			// still runs, and the caller decides whether the gap matters.
			name: "runtime features alone do not fail",
			capability: Capability{
				Mode:            "hybrid",
				RequiresRuntime: true,
				RuntimeFeatures: []Feature{FeatureDynamicRef, FeatureUnevaluatedItems},
			},
		},
		{
			name: "unsupported features fail",
			capability: Capability{
				Mode:        "static",
				Unsupported: []Feature{FeatureCustomVocabulary},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.capability.Check()
			if tt.wantErr {
				if err == nil {
					t.Fatal("Check() = nil, want an error")
				}
				if !strings.Contains(err.Error(), string(FeatureCustomVocabulary)) {
					t.Errorf("error %q does not name the unsupported feature", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Check() = %v, want nil", err)
			}
		})
	}
}
