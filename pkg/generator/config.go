package generator

import "github.com/mgilbir/schemagen/pkg/schema"

// Config holds configuration for code generation.
type Config struct {
	PackageName      string // Go package name for generated code
	OutputDir        string // Output directory
	OmitEmpty        bool   // Add omitempty to optional fields
	StrictProperties bool   // When true, absent additionalProperties is treated as false for validation.
	//                      Extra properties are still captured in an overflow map for round-trip fidelity,
	//                      but Validate rejects them. When false (default), absent additionalProperties
	//                      follows JSON Schema spec (defaults to true), so overflow properties are accepted.
	Resolver      schema.SchemaResolver // External schema resolver for $ref resolution (remote, file, etc.)
	Draft         schema.Draft          // Override draft detection; when set, this takes precedence over $schema URI.
	BigIntSupport bool                  // When true, "type":"integer" generates wrapper struct with int64 + *big.Int support for arbitrary-precision integers.
	Validation    ValidationMode        // Controls static vs hybrid/runtime validation planning.
	FieldNames    FieldNameMap          // Optional per-type overrides pinning JSON properties to specific Go field names.
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		PackageName:      "generated",
		OutputDir:        ".",
		OmitEmpty:        true,
		StrictProperties: false,
		Validation:       ValidationModeStatic,
	}
}
