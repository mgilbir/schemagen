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

	// FormatAnnotation is FormatAssertion's opposite: it makes "format" an
	// annotation on every draft, including the ones whose dialect asserts.
	//
	// It exists because v1 asserts by default and states the annotation reading
	// as a legitimate alternative configuration -- the official suite files it
	// under optional/format-annotation.json, the mirror image of the
	// optional/format/ directory that states assertion for 2019-09 and 2020-12.
	// Without a downward override that configuration is unreachable, and a v1
	// schema naming a format this generator checks imperfectly would have no way
	// to stop it rejecting a document the author considers fine.
	//
	// Mutually exclusive with FormatAssertion; the CLI refuses both at once. If
	// both are set programmatically this one wins, because it withholds a
	// rejection rather than inventing one.
	FormatAnnotation bool

	// StrictReadWrite makes the "readOnly" and "writeOnly" annotations change
	// what the generated type decodes and encodes.
	//
	// Off, which is the default, they are documentation: the doc comment says
	// what the schema said and nothing else changes, so the type stays a
	// faithful shape for the document and round-trips exactly.
	//
	// On, the generated type becomes the *owning authority's* view of the
	// resource, which is the only view in which the two keywords have a
	// direction. RFC-wise this is JSON Schema 2020-12 section 9.4: readOnly says
	// an application's attempt to set the value is "expected to be ignored or
	// rejected by an owning authority", and this picks rejected -- UnmarshalJSON
	// refuses a document that carries the property. writeOnly says the value "is
	// never present when the instance is retrieved from the owning authority",
	// so MarshalJSON leaves the property out.
	//
	// It binds on a property, and the property is what it keys on: the check
	// lives in the parent struct's decoder and encoder, which are the only things
	// that ever see a property name. Where the keyword is written does not
	// matter -- on the property, at the end of its $ref chain, or in one of its
	// allOf branches, all of which apply at the same instance location. See
	// readWriteAtLocation for that reach, and for why an anyOf branch is not part
	// of it.
	//
	// Outside a property it stays documentation, and that is the boundary rather
	// than a gap. A readOnly array element or map value has no property name for
	// the check to key on, and writeOnly has no action available there either: a
	// property can be left out of an object, but an element cannot be left out of
	// an array without changing its length, which minItems can see. The doc
	// comment on the element's own type is where those are said (issue #172).
	//
	// Two consequences are deliberate and are why it is opt-in rather than the
	// default:
	//
	// A type built this way no longer round-trips. A writeOnly value goes in and
	// does not come out, and a readOnly document does not decode at all. That is
	// the flag doing its job, and it is why the round-trip helpers in tests/
	// refuse a config carrying it outright rather than growing an exception.
	//
	// And it picks a side. One Go type cannot be both the request shape and the
	// response shape, and MarshalJSON is not told which it is being asked for, so
	// a *client* using the same type to build a request would have its writeOnly
	// password dropped. The default declines to guess; the flag is the caller
	// saying which side they are on.
	//
	// What it never does is change a validation verdict. Validate() does not see
	// these keywords under either setting. In 2019-09 and 2020-12 they are the
	// meta-data vocabulary and are annotations by definition; the official suite
	// has no case for either keyword, so a Validate() that consulted one would be
	// non-conformant with nothing in the corpus to say so.
	StrictReadWrite bool

	Validation   ValidationMode // Controls static vs hybrid/runtime validation planning.
	FieldNames   FieldNameMap   // Optional per-type overrides pinning JSON properties to specific Go field names.
	LenientRefs  bool           // When true, $refs that no resolver can serve degrade to any instead of failing generation.
	RootTypeName string         // Overrides the root type name (default: the schema title, or "Root" when there is none).
	SharedTypes  bool           // Preserve generated-type state across Generate calls so several schemas emit into one Go package without duplicating shared types.

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
