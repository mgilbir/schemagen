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
	Draft         schema.Draft          // Override draft detection; when set, this takes precedence over $schema URI, except for embedded/remote resources that declare both $id and their own $schema.
	BigIntSupport bool                  // When true, "type":"integer" generates wrapper struct with int64 + *big.Int support for arbitrary-precision integers.

	// FormatAssertion turns "format" into an assertion on every draft.
	//
	// Without it the dialect decides. Draft 3, 4, 6 and 7 leave format
	// assertion to the implementation, and this generator has always asserted
	// there. From 2019-09 the default meta-schema declares the
	// format-annotation vocabulary, which makes format an annotation and
	// nothing more: {"format":"email"} is satisfied by "2962", and an
	// implementation that rejects it is rejecting a document the schema
	// permits. Assertion on those drafts is opt-in, and this is the opt-in.
	//
	// It reaches the Go type as well as the emitted check. A format that maps
	// to time.Time or netip.Addr is asserted by the decoder whatever Validate
	// does -- an unparseable value simply fails to decode -- so leaving the
	// mapping in place under an annotation-only dialect would keep three of the
	// sixteen formats assertive and call the posture annotation-only anyway.
	// Under assertion the mapping returns, and with it the typed accessor.
	FormatAssertion bool
	Validation      ValidationMode // Controls static vs hybrid/runtime validation planning.
	FieldNames      FieldNameMap   // Optional per-type overrides pinning JSON properties to specific Go field names.
	LenientRefs     bool           // When true, $refs that no resolver can serve degrade to any instead of failing generation.
	RootTypeName    string         // Overrides the root type name (default: the schema title, or "Root" when there is none).
	SharedTypes     bool           // Preserve generated-type state across Generate calls so several schemas emit into one Go package without duplicating shared types.

	// ImportPath is the Go import path of the package being generated, and
	// CrossPackage the registry shared by every generator of a multi-package
	// run. When both are set, $refs into documents owned by other packages
	// of the run emit qualified names and imports instead of materializing
	// local copies.
	ImportPath   string
	CrossPackage *CrossPackageRegistry
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
