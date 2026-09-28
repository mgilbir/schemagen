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
	Resolver      schema.SchemaResolver // Loads the documents a $ref names that the generator does not hold (file, remote, etc.). References are resolved by a schema.ResourceIndex built over it; a *schema.ResourceIndex given here is used as it is, and shared.
	Draft         schema.Draft          // Override draft detection; when set, this takes precedence over $schema URI, except for embedded/remote resources that declare both $id and their own $schema.
	BigIntSupport bool                  // When true, "type":"integer" generates wrapper struct with int64 + *big.Int support for arbitrary-precision integers.

	// ExactNumbers holds "type":"number" as the literal the document wrote
	// rather than as the float64 it rounds to.
	//
	// JSON has one number type and no precision limit; float64 has both. A
	// property typed "number" is a float64 by default, so a document carrying
	// 1.2345678901234567890 comes back as 1.2345678901234567 and one carrying
	// 123456789012345678901234567890 comes back as 1.2345678901234568e+29 --
	// a read-modify-write silently rewrites a field the caller never touched
	// (issue #252). Integers have not had that problem since #230: an int64
	// holds every integer JSON Schema's "integer" can name up to its own range,
	// and --big-int carries the rest. "number" had no equivalent, and that
	// asymmetry is what this closes.
	//
	// On, the Go type becomes encoding/json's json.Number, which is the literal
	// as written. Nothing rounds, so the value that arrives is the value that
	// leaves, byte for byte, in every position the schema gives the type to: a
	// scalar property, an array element, a map value, a $defs alias, a default.
	// Every numeric keyword -- minimum, maximum, the two exclusive forms,
	// multipleOf, const and enum -- is then compared exactly through math/big
	// rather than through the float64 that cannot tell 2^53+1 from 2^53.
	//
	// It is opt-in because it changes the generated type, and a caller who is
	// doing arithmetic on a float64 field wants the float64. json.Number is a
	// string underneath: it is exact, it costs no dependency, and it hands the
	// arithmetic question back to the caller, who is the only one who knows
	// which precision their sum wants.
	//
	// Independent of BigIntSupport, which answers the same question for the
	// other JSON numeric type and can be set beside this or without it.
	//
	// Where it does not reach: a position the schema gives no type to. Those
	// are held as `any`, and encoding/json makes a float64 of a JSON number on
	// the way into one whatever this says -- a tuple element, an overflow value
	// judged by a runtime rule, an `any` field. The type is what this flag acts
	// on, so a schema that states none gets what it always got.
	ExactNumbers bool

	// RawUntyped holds a position the schema gives no type to as the bytes the
	// document wrote, rather than as the `any` encoding/json decodes them into.
	//
	// A schema-free position -- {"properties":{"payload":{}}}, a property whose
	// schema is `true`, a $defs entry carrying a description and nothing else --
	// has always been `any`, and encoding/json fills an `any` with whatever it
	// makes of the JSON: a float64 for every number, a map[string]any for every
	// object. So a document carrying
	//
	//	{"z": 9007199254740993, "a": 1.10, "big": 123456789012345678901234567890}
	//
	// in such a position comes back as
	//
	//	{"a":1.1,"big":1.2345678901234568e+29,"z":9007199254740992}
	//
	// -- an integer one past 2^53 rounded to its neighbour, a trailing zero
	// dropped, a big integer rewritten in exponent notation, and the members
	// reordered -- through a field the caller never touched. That is the gap
	// ExactNumbers names at the end of its own comment: it acts on the declared
	// type, and here there is no type to act on.
	//
	// On, every such position is encoding/json's json.RawMessage. The bytes that
	// arrive are the bytes that leave, up to what encoding/json does to every
	// RawMessage it writes: insignificant whitespace is dropped and the
	// characters it always escapes inside a string are escaped. Number spelling,
	// member order and every digit are kept. It reaches a property, an array
	// element, a map value, a $defs alias and a reference cycle that carries no
	// content -- the positions ExactNumbers reaches for a "number", read for a
	// schema that states nothing -- and it reaches the values of an object typed
	// "object" with no property schema at all, which is the same statement made
	// about an object's members. A position held as a Go map -- that bare
	// object, and additionalProperties: {} -- keeps each value's bytes but
	// writes its own members in sorted order, as encoding/json writes every
	// map; only a position held whole as a RawMessage keeps its member order.
	//
	// A caller cannot get there from outside, and that is why it is a
	// configuration rather than advice. Setting UseNumber on their own
	// json.Decoder never reaches the field: every generated struct has an
	// UnmarshalJSON of its own and decodes its members through json.Unmarshal,
	// which knows nothing of the decoder that called it. The field's type is the
	// only thing that decides how the field is filled, and the type is what this
	// changes.
	//
	// Opt-in for the reason ExactNumbers is: it changes the generated type, and a
	// caller reading an `any` through a type switch wants the `any`. RawMessage
	// hands the decode back to the caller, who is the only one who knows what a
	// value the schema did not describe is for.
	//
	// What Validate says does not change. A schema that states nothing has
	// nothing to check, and the two keywords that do judge an untyped value --
	// const and enum -- have held it raw and compared it by JSON equality since
	// #272, so they are not this flag's business. The comparisons that read a
	// raw element from *beside* the untyped schema -- uniqueItems on an array of
	// them, a contains naming a const or an enum -- are made through the same
	// JSON-equality reduction rather than on the bytes, so [1, 1.0] is still
	// not unique and 1.0 still satisfies {"const":1}. Byte equality is not
	// JSON equality, and a flag about the bytes must not change a verdict
	// defined over the values.
	//
	// Where it does not reach, and why:
	//
	//   - A tuple. Its elements are `any` because they differ from one another,
	//     not because the schema left them untyped: each position has a schema
	//     of its own, and the check on each reads the decoded value's kind. The
	//     same slice is what an array with no `items` at all, and one carrying
	//     unevaluatedItems, are held as, so a bare {"type":"array"} keeps its
	//     []any; {"type":"array","items":{}} is the spelling that reaches this.
	//   - An `any` that reports a gap rather than a schema. An alias whose
	//     schema states keywords this generator could not compile stays `any`
	//     under the NOT VALIDATED comment that says so, and a $ref that
	//     LenientRefs degraded stays what that flag documents. Making either
	//     raw would round-trip the value and hide that nothing is checking it.
	//   - A oneOf branch. Which branch a value is held under is decided by its
	//     shape, and the untyped branch is the one that catches what the others
	//     did not; that is the union's own machinery, not a position this types.
	//
	// Independent of ExactNumbers and BigIntSupport, which act on a declared
	// type and so never meet this at one position. The three compose.
	RawUntyped bool

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
	// readWriteAtLocation for that reach.
	//
	// A conditional branch -- anyOf, oneOf, if/then/else, dependentSchemas, not
	// -- is where the two keywords part company. readOnly does not follow one: a
	// refusal keyed on a branch the document did not select rejects a document the
	// schema accepts. writeOnly does: over-stripping omits a field visibly and
	// recoverably, under-stripping emits a secret silently, and this flag is a
	// policy its caller chose rather than spec validation. conditionalReachAt
	// argues it in full.
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

	// StrictKeywords refuses a schema that uses a keyword this generator does
	// not know, with an error naming where each one is written.
	//
	// Off, which is the default, such a keyword is an annotation: it is
	// carried in the schema and constrains nothing, which is what JSON Schema
	// 2019-09 and 2020-12 say an unknown keyword is, and what every earlier
	// draft says to do with one. That is the right reading for the vendor
	// keywords real schemas carry -- "x-go-type", OpenAPI's "example" -- and the
	// wrong one for a keyword that was meant to assert and is misspelled
	// ("minLenght") or comes from a vocabulary schemagen does not implement.
	// This is the setting that tells the two apart, by refusing both.
	//
	// A metaschema that declares a vocabulary this generator does not
	// implement as required is refused whatever this says: the specification
	// says an implementation that does not recognise a required vocabulary must
	// refuse the schema.
	StrictKeywords bool

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

	// DefinitionTypeNames pins the Go type name a document's definition is
	// declared under, keyed by the definition's own schema node rather than by
	// its $defs key.
	//
	// It exists for one situation, and only a caller holding the whole input set
	// can see it: several documents generated into one package (SharedTypes)
	// that each define a $defs entry of the same name but with different
	// content. The name registry keys on the Go name, so the first definition
	// generated claims it and every later one is skipped -- the second
	// document's property then silently carries the first document's type
	// (issue #249). The caller resolves that by naming each definition after its
	// own document and passing the result here; the key is the node because that
	// is what survives $ref resolution, so a reference from another document
	// reaches the same answer as the definition itself.
	//
	// Entries for nodes this run never generates are ignored, so one map may
	// describe every document of the run.
	DefinitionTypeNames map[*schema.Schema]string
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
