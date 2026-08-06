package generator

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

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

	// AnnotationsFormats names the "format" arguments the evaluator's nodes
	// carry, and AnnotationsContent says whether any node names the content
	// vocabulary. Both add an arm to the evaluator, exactly as AnnotationsPattern
	// does, and for the same reason: the arm interprets a field only some
	// packages' nodes set.
	//
	// Formats are a list rather than a flag because the dispatcher is written
	// with one case per format actually compiled. A switch naming every format
	// this generator can check would name the four hostname helpers too, and so
	// would put golang.org/x/net/idna on every package whose schemas compile any
	// format at all -- the imposition FormatHostname exists to confine. With the
	// list, a package naming only `format: date` gets a one-arm switch and no
	// new dependency.
	AnnotationsFormats []string
	AnnotationsContent bool

	// AnnotationsDynamic adds the two things a schema needs when it cannot be
	// written as one finite tree: a node that refers to another node, so a
	// schema that contains itself can be expressed at all, and the stack of
	// schema resources a $dynamicRef or $recursiveRef is resolved against.
	//
	// They share a flag because they arrive together. A dynamic reference is
	// almost always what makes a schema recursive, and the resource frames are
	// no use without somewhere for a reference to point. Conditional for the
	// usual reason: a package whose schemas do neither should not carry the
	// stack, nor pay for threading it through every call in the evaluator.
	AnnotationsDynamic bool
	Integer            bool // jsonInteger and the shape-preserving converters
	NullCheck          bool // jsonNullRule and the recursive walker that applies one
	Format             bool // schemagenFormat* -- one function per asserted format
	Content            bool // schemagenContentString -- the content vocabulary's decode-and-parse check

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
		!h.Annotations && !h.Integer && !h.NullCheck && !h.Format && !h.FormatHostname &&
		!h.Content
}

// Merge folds another set into this one.
func (h *HelperSet) Merge(other HelperSet) {
	h.OneOf = h.OneOf || other.OneOf
	h.OneOfDiscriminator = h.OneOfDiscriminator || other.OneOfDiscriminator
	h.Dynamic = h.Dynamic || other.Dynamic
	h.DynamicConst = h.DynamicConst || other.DynamicConst
	h.Annotations = h.Annotations || other.Annotations
	h.AnnotationsPattern = h.AnnotationsPattern || other.AnnotationsPattern
	h.AnnotationsDynamic = h.AnnotationsDynamic || other.AnnotationsDynamic
	h.AnnotationsFormats = mergeSortedUnique(h.AnnotationsFormats, other.AnnotationsFormats)
	h.AnnotationsContent = h.AnnotationsContent || other.AnnotationsContent
	h.Integer = h.Integer || other.Integer
	h.NullCheck = h.NullCheck || other.NullCheck
	h.Format = h.Format || other.Format
	h.Content = h.Content || other.Content
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
		// "format" and the content vocabulary are read the same way, and are the
		// same kind of signal: the node carries the argument and the arm that
		// interprets it lives in the helper block, so what the file shows is the
		// literal rather than a call. Each format found also pulls in the block
		// that declares its checker -- and, where the checker is one of the four
		// that need x/net/idna, the hostname block with it. That mapping is
		// FormatHelperName's, so the dependency decision is made from the same
		// table the emitted arm dispatches through rather than from a second
		// list of names to keep in step.
		for _, name := range annotationFormatNames(src) {
			set.AnnotationsFormats = mergeSortedUnique(set.AnnotationsFormats, []string{name})
			set.Format = true
			if hostnameHelpers[FormatHelperName(name)] {
				set.FormatHostname = true
			}
		}
		if strings.Contains(src, "ContentEncoding: _strPtr(") || strings.Contains(src, "ContentMediaType: _strPtr(") {
			set.AnnotationsContent = true
			set.Content = true
		}
		// The recursive and dynamic arms are read the same way, off the three
		// fields only a file needing them can carry: a node pointing at another
		// node, a reference the dynamic scope resolves, and the frame a schema
		// resource publishes. All three are matched because each can appear
		// without the others -- a recursive schema with no dynamic reference in
		// it names only the first -- and a missed one is a file that names a
		// type the helpers do not declare, which does not compile.
		if strings.Contains(src, "Ref: &_rt") || strings.Contains(src, "DynamicRef:") || strings.Contains(src, "DynamicAnchors:") {
			set.AnnotationsDynamic = true
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
	// The content check is one function, and its name appears at every call
	// site. It is a block of its own rather than part of the format block
	// because it needs encoding/base64, which nothing else here does.
	if strings.Contains(src, "schemagenContentString(") {
		set.Content = true
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

// hostnameHelpers is hostnameHelperCalls as function names rather than call
// prefixes, for the caller that holds a name instead of a piece of source.
//
// Derived from that list rather than written out again: the two must name the
// same four functions, and a fifth added to the block has one place to be added.
var hostnameHelpers = func() map[string]bool {
	set := make(map[string]bool, len(hostnameHelperCalls))
	for _, call := range hostnameHelperCalls {
		set[strings.TrimSuffix(call, "(")] = true
	}
	return set
}()

// annotationNodeFormat matches the "format" argument a compiled _schemaNode
// carries. The literal is written by nodeBuilder.literal with %q, so the value
// is a Go-quoted string and strconv.Unquote is what reads it back.
var annotationNodeFormat = regexp.MustCompile(`Format: _strPtr\((` + "`" + `[^` + "`" + `]*` + "`" + `|"(?:[^"\\]|\\.)*")\)`)

// annotationFormatNames returns the format arguments the compiled nodes in src
// name, deduplicated and in sorted order.
//
// Reading them out of the emitted source is the same choice HelpersReferencedBy
// makes for every other helper, and for the reason given there: the thing being
// asked is exactly the thing that matters -- which formats does this file's
// generated code ask the evaluator to check -- so it cannot drift from an IR
// walk that forgot a position. A name the regexp fails to read is skipped rather
// than guessed at, which under-matches; the arm is then missing and the build
// fails, rather than the check silently disappearing.
func annotationFormatNames(src string) []string {
	var names []string
	for _, m := range annotationNodeFormat.FindAllStringSubmatch(src, -1) {
		name, err := strconv.Unquote(m[1])
		if err != nil {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return slices.Compact(names)
}

// mergeSortedUnique returns the sorted union of two name lists.
func mergeSortedUnique(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	out := append(append([]string(nil), a...), b...)
	sort.Strings(out)
	return slices.Compact(out)
}

// FormatHelperName maps a "format" keyword to the shared helper that checks a
// string against it, or "" when this generator has no check for it.
//
// It is the other half of FormatCheckableOnString: a format that answers true
// there must have a name here, or a rule would be built and then render nothing.
// TestFormatHelperNamesCoverCheckableFormats holds the two together.
//
// Exported from this package rather than the emitter's because two callers need
// it and only one of them is a template: HelpersReferencedBy asks which helper
// block a compiled format pulls in, which decides whether the package takes the
// x/net/idna dependency, and that runs before any template does.
func FormatHelperName(format string) string {
	switch format {
	case "date":
		return "schemagenFormatDate"
	case "time":
		return "schemagenFormatTime"
	case Draft3TimeFormat:
		return "schemagenFormatDraft3Time"
	case Draft3ColorFormat:
		return "schemagenFormatDraft3Color"
	case "date-time":
		return "schemagenFormatDateTime"
	case "duration":
		return "schemagenFormatDuration"
	case "email":
		return "schemagenFormatEmail"
	case "idn-email":
		return "schemagenFormatIDNEmail"
	case "hostname":
		return "schemagenFormatHostname"
	case "idn-hostname":
		return "schemagenFormatIDNHostname"
	case "uri":
		return "schemagenFormatURI"
	case "iri":
		return "schemagenFormatIRI"
	case "uri-reference":
		return "schemagenFormatURIReference"
	case "iri-reference":
		return "schemagenFormatIRIReference"
	case "uri-template":
		return "schemagenFormatURITemplate"
	case "uuid":
		return "schemagenFormatUUID"
	case "json-pointer":
		return "schemagenFormatJSONPointer"
	case "relative-json-pointer":
		return "schemagenFormatRelativeJSONPointer"
	case "regex":
		return "schemagenFormatRegex"
	case "ipv4":
		return "schemagenFormatIPv4"
	case "ipv6":
		return "schemagenFormatIPv6"
	default:
		return ""
	}
}
