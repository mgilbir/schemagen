package generator

import "strings"

// HelperSet records which shared helper functions a generated file depends on.
//
// Helpers are package-level functions, so emitting them into every file that
// needs them breaks as soon as two schemas in one package need the same helper:
// the package then declares it twice and does not compile. They are collected
// here instead and written once per destination package.
type HelperSet struct {
	OneOf              bool // oneofHasRequiredFields
	OneOfDiscriminator bool // oneofDiscriminatorValue
	Dynamic            bool // _dyn* value predicates
	DynamicConst       bool // _dynConstOK, only reached by object-level conditionals
	Annotations        bool // _schemaNode and the runtime schema evaluator
	AnnotationsPattern bool // the evaluator's ECMA-262 arms, and the engine they need
	Integer            bool // jsonInteger and the shape-preserving converters
	NullCheck          bool // jsonNullRule and the recursive walker that applies one
	Format             bool // schemagenFormat* -- one function per asserted format

	// FormatHostname pulls in the two hostname checks, which are kept apart
	// from the rest because they are the only ones that need a dependency the
	// caller would not otherwise take: golang.org/x/net/idna, for punycode, the
	// IDNA2008 derived properties, the bidi rule and the ContextJ rules. None of
	// that is expressible without it, and none of it is wanted by a package
	// whose schemas name no hostname. `format: email` sets this too -- an email
	// domain is a hostname and is judged by the same function.
	FormatHostname bool
}

// Empty reports whether no helpers are needed at all.
func (h HelperSet) Empty() bool {
	return !h.OneOf && !h.OneOfDiscriminator && !h.Dynamic && !h.DynamicConst &&
		!h.Annotations && !h.Integer && !h.NullCheck && !h.Format && !h.FormatHostname
}

// Merge folds another set into this one.
func (h *HelperSet) Merge(other HelperSet) {
	h.OneOf = h.OneOf || other.OneOf
	h.OneOfDiscriminator = h.OneOfDiscriminator || other.OneOfDiscriminator
	h.Dynamic = h.Dynamic || other.Dynamic
	h.DynamicConst = h.DynamicConst || other.DynamicConst
	h.Annotations = h.Annotations || other.Annotations
	h.AnnotationsPattern = h.AnnotationsPattern || other.AnnotationsPattern
	h.Integer = h.Integer || other.Integer
	h.NullCheck = h.NullCheck || other.NullCheck
	h.Format = h.Format || other.Format
	h.FormatHostname = h.FormatHostname || other.FormatHostname
}

// HelpersReferencedBy reports which shared helpers a generated file calls, read
// from the emitted source rather than from the IR it came out of.
//
// The IR walk this replaces asked each definition what rules it carried, which
// meant naming every field a rule can live in. It named the ones a format check
// was known to sit in and not ItemValidations, so a schema whose only format was
// on an array element or a map value emitted the call and never declared the
// function: generated code that did not compile. The two hostname formats in an
// element position did the same. That is the same failure the _dyn* family had
// on PR #59, from the same cause -- a hand-maintained list of places to look.
//
// Reading the source cannot drift, because the thing being asked is exactly the
// thing that matters: does this file contain a call to that function. It is also
// what every harness in this repository has always done, which is why they all
// compiled while the generator's own answer was wrong -- they were not testing
// the generator's answer at all. There is one implementation now, and the
// compile tests exercise it.
//
// The asymmetry makes over-matching safe: a name appearing in a comment pulls in
// a function that nothing calls, which Go permits and which costs a few lines in
// a file that is written once. Under-matching breaks the build.
func HelpersReferencedBy(src string) HelperSet {
	var set HelperSet
	if strings.Contains(src, "oneofHasRequiredFields(") {
		set.OneOf = true
	}
	if strings.Contains(src, "oneofDiscriminatorValue(") {
		set.OneOfDiscriminator = true
	}
	// The _dyn* family is matched by its prefix and pulled in whole, rather than
	// by a list of names that has to be kept in step with the templates by hand.
	if strings.Contains(src, "_dyn") {
		set.Dynamic = true
		set.DynamicConst = true
	}
	// The annotation evaluator calls the _dyn* predicates, so it pulls both in.
	if strings.Contains(src, "_schemaNode") || strings.Contains(src, "_evalNode(") {
		set.Annotations = true
		set.Dynamic = true
		// The evaluator's ECMA-262 arms are the one part of a helper block that
		// is compiled in conditionally, because the engine is a third-party
		// dependency and a package that never names a pattern should not
		// acquire it. It is also the one signal here that is not a call:
		// _dynPatternOK is reached from inside the block and never from the
		// file, so what the file carries is the literal the arms exist to
		// interpret. Both spellings that set it are matched -- "pattern" on a
		// node, and a patternProperties member list, which is also what makes
		// an additionalProperties node have to run the patterns to know what is
		// left over. Missing one would leave the field set with nothing reading
		// it, which is a check dropped in silence rather than a build failure,
		// so this errs towards matching; naming the arms where they are not
		// needed costs an import and compiles.
		if strings.Contains(src, "Pattern: _strPtr(") || strings.Contains(src, "PatternProperties:") {
			set.AnnotationsPattern = true
		}
	}
	// jsonInteger and its three container rebuilders come as one block, and the
	// name appears in every use of any of them.
	if strings.Contains(src, "jsonInteger") {
		set.Integer = true
	}
	// jsonNullRule and checkJSONNulls come as one block, and the walker's name
	// appears at every call site, so one substring pulls both in. The rule type
	// alone never appears without a call: it exists only as that call's
	// argument. A struct rejecting a null at its own top level writes the check
	// inline and needs neither.
	if strings.Contains(src, "checkJSONNulls(") {
		set.NullCheck = true
	}
	// Every format check calls a schemagenFormat* function, and the general
	// block is emitted whole, so the prefix pulls all of it in.
	if strings.Contains(src, "schemagenFormat") {
		set.Format = true
	}
	// The hostname block is separate because it is the only one needing
	// x/net/idna, so it is matched by the four calls that reach it rather than
	// by the prefix. A package whose schemas name no hostname must not take that
	// dependency, which is the whole reason the split exists.
	for _, call := range hostnameHelperCalls {
		if strings.Contains(src, call) {
			set.FormatHostname = true
		}
	}
	return set
}

// hostnameHelperCalls names every function the hostname helper block declares
// that generated code calls directly.
var hostnameHelperCalls = []string{
	"schemagenFormatHostname(",
	"schemagenFormatIDNHostname(",
	"schemagenFormatEmail(",
	"schemagenFormatIDNEmail(",
}
