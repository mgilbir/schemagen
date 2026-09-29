package generator

import (
	"go/scanner"
	"go/token"
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

	// Number is jsonNumber, the shadow a "number" held exactly is decoded
	// through, and NumberCompare the exact decimal comparisons its keywords are
	// enforced by. Two flags rather than one because they arrive apart: a
	// schema that types a property "number" and states no numeric keyword needs
	// the shadow and no comparison, and a file that carries only the checks --
	// a $defs alias validated from another document of a shared-types run --
	// needs the comparison and no shadow.
	Number        bool
	NumberCompare bool

	// DateTime is jsonDateTime, the shadow an asserted `format: date-time` is
	// decoded through so that the lower case "t" and "z" RFC 3339 permits are
	// not refused by time.Time's layout-driven parser. See issue #264. It is a
	// block of its own for the reason Number is: a package whose schemas name no
	// date-time should not carry it, and it is the only block that needs `time`.
	DateTime bool

	// IPAddr is jsonIPv4Addr and jsonIPv6Addr, the shadows an asserted
	// `format: ipv4` or `format: ipv6` is decoded through so that an address
	// that does not parse is refused in the schema's words rather than
	// netip.ParseAddr's. See issue #282. A block of its own for the reason
	// DateTime is: a package whose schemas name no ip format should not carry
	// it, and net/netip is a dependency of the format block it would otherwise
	// have to be part of.
	IPAddr bool

	// Canonical is _jsonCanonical, the JSON-equality reduction an "enum" or a
	// "const" held as raw JSON is decided by, and the number canonicalisation
	// under it. One flag, because the three functions are one block: the walker
	// calls the number reader, and the list initialiser calls the walker.
	//
	// Conditional like every other block here. A package whose schemas state
	// no whole-document enum or const emits none of it.
	Canonical bool

	// The identity of a JSON value (jsonID) -- what uniqueItems, const and enum
	// compare values by, read off the value as it is held rather than off an
	// encoding of it -- and the tree a match is confirmed on. It is code in the
	// user's package, compile time and binary size, so it is split along its own
	// calls into six blocks, and a package takes the blocks its generated code
	// calls and the ones those call (see identityBlocks and CloseOverCalls):
	//
	//   - IdentityCore: jsonID itself, the identities of JSON's scalars, and the
	//     reader of raw JSON's.
	//   - IdentityConst: a const or an enum read once into its members'
	//     identities and trees, and a raw value decided against one.
	//   - IdentityAny: a value held as decoded JSON, which is all the runtime
	//     evaluator and the dynamic checks compare.
	//   - IdentityLazy: a value read lazily from a document, whose objects' and
	//     arrays' identities the document keeps.
	//   - IdentityValue: any Go value, by the rules it is written by -- this
	//     package's types by their own jsonIdentity, everything else by its Go
	//     kind through reflect -- and jsonValidation, what one Validate shares
	//     with the values below it.
	//   - IdentityKind: the kind of a value, for a keyword about one kind.
	IdentityCore  bool
	IdentityConst bool
	IdentityAny   bool
	IdentityLazy  bool
	IdentityValue bool
	IdentityKind  bool

	// AnnotationsEquality compiles in the evaluator's const, enum and
	// uniqueItems arms, and the node fields they read, which are its only use of
	// the identity blocks. Read off the node literals, as AnnotationsPattern is.
	AnnotationsEquality bool

	NullCheck bool // jsonNullRule and the recursive walker that applies one
	Format    bool // schemagenFormat* -- one function per asserted format
	Content   bool // schemagenContentString -- the content vocabulary's decode-and-parse check

	// Access is --strict-read-write's raw-JSON walker: the path model that
	// reaches the readOnly/writeOnly locations no Go field answers for, and the
	// refusal type a Validate check has to be able to tell from a real decode
	// failure. Conditional like every other block here -- the default
	// configuration emits none of it, and generated output is byte-identical to
	// what it was before the keywords were parsed at all.
	Access bool
	// AccessPattern is the walker's ECMA-262 arm, split off for the reason
	// AnnotationsPattern is: the engine is a third-party dependency, and a
	// package whose rules match no key by pattern should not acquire it.
	AccessPattern bool

	// Decode is jsonDoc and the helpers around it: the in-place decode every
	// struct, raw-JSON wrapper and container alias reads its value through, in
	// one pass over one indexed document however deeply the types nest.
	// Conditional like every other block here -- a package of nothing but
	// scalars and enums decodes through encoding/json alone.
	Decode bool

	// PathJoin is jsonPathError and the two constructors and two joiners around
	// it: the rule by which a nested validation message is put behind the path
	// that reaches the value it was raised on. Conditional like every other
	// block here -- a package whose schemas nest nothing emits none of it.
	PathJoin bool

	// DecodePath is jsonDecodeMemberError and the three decoders and two
	// message readers around it: what traces a refusal raised while decoding an
	// object back to the position in the document that caused it, and puts a
	// refusal worded from a Go type into the words the schema is written in. See
	// StructDef.DecodeMembers and issue #282. Conditional like every other block
	// here -- a package with no struct decode emits none of it.
	DecodePath bool

	// Patterns are the schema regular expressions the package's generated code
	// matches with, sorted and without duplicates. The helper file declares one
	// package-level variable per pattern, compiled once when the package is
	// initialised, and the type that matches through it; generated code names
	// the variable (see PatternVarName) and never compiles a pattern itself.
	// A list rather than a flag for the reason AnnotationsFormats is one: what
	// the block declares depends on which patterns, not only on whether any.
	// It is also the only block that imports the ECMA-262 engine for a pattern,
	// so a package whose schemas state none does not take the dependency.
	Patterns []string

	// Quote is _schemagenQuote, the rule every message quoting a string taken
	// from the document goes through: quoted whole up to 128 bytes, and cut to
	// its first 64 with its length beyond that, so that an error about a huge
	// value is not itself huge.
	Quote bool

	// Undecided is _schemagenUndecided, the test every site that reads an
	// error as a boolean -- a union branch that did not decode or validate, a
	// contains element that did not count -- asks first, so that a pattern
	// match with no answer is returned rather than read as "no". A block of
	// its own rather than part of the pattern block: the error may come from a
	// type another package declares, in a package that states no pattern.
	Undecided bool

	// Roots is every identifier the package's generated files use, read from
	// their source. The blocks above decide what the helper file renders; of
	// that, only the declarations these names reach are kept (see the
	// emitter's pruneHelpers). Nil, as in a set written by hand rather than read
	// from source, keeps every declaration of every block rendered.
	Roots []string

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
		!h.Annotations && !h.Integer && !h.Number && !h.NumberCompare && !h.DateTime &&
		!h.Canonical && !h.anyIdentity() && !h.NullCheck &&
		!h.Format && !h.FormatHostname && !h.Content && !h.Access && !h.Decode &&
		!h.PathJoin && !h.DecodePath && !h.IPAddr && len(h.Patterns) == 0 && !h.Quote && !h.Undecided
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
	h.Number = h.Number || other.Number
	h.NumberCompare = h.NumberCompare || other.NumberCompare
	h.DateTime = h.DateTime || other.DateTime
	h.IPAddr = h.IPAddr || other.IPAddr
	h.Canonical = h.Canonical || other.Canonical
	h.IdentityCore = h.IdentityCore || other.IdentityCore
	h.IdentityConst = h.IdentityConst || other.IdentityConst
	h.IdentityAny = h.IdentityAny || other.IdentityAny
	h.IdentityLazy = h.IdentityLazy || other.IdentityLazy
	h.IdentityValue = h.IdentityValue || other.IdentityValue
	h.IdentityKind = h.IdentityKind || other.IdentityKind
	h.AnnotationsEquality = h.AnnotationsEquality || other.AnnotationsEquality
	h.NullCheck = h.NullCheck || other.NullCheck
	h.Format = h.Format || other.Format
	h.Content = h.Content || other.Content
	h.FormatHostname = h.FormatHostname || other.FormatHostname
	h.Access = h.Access || other.Access
	h.AccessPattern = h.AccessPattern || other.AccessPattern
	h.Decode = h.Decode || other.Decode
	h.PathJoin = h.PathJoin || other.PathJoin
	h.DecodePath = h.DecodePath || other.DecodePath
	h.Patterns = mergeSortedUnique(h.Patterns, other.Patterns)
	h.Quote = h.Quote || other.Quote
	h.Undecided = h.Undecided || other.Undecided
	h.Roots = mergeSortedUnique(h.Roots, other.Roots)
}

// CloseOverCalls adds the blocks the selected blocks themselves call.
//
// The set is read from what a generated *types* file calls (see
// HelpersReferencedBy), and a call from one helper to another appears in no
// types file at all: a schema with one string property names checkJSONNullsAt
// and never names jsonValueErrorf, which that walker's refusal is built by. Left
// alone, the package gets the walker and not the constructor, and does not
// compile.
//
// Only one direction of dependency exists, so one pass settles it: the message
// helpers are the leaves, and are what the decode-time blocks reach for to say
// where a refusal belongs in the caller's document.
func (h *HelperSet) CloseOverCalls() {
	// The runtime evaluator is here for the same reason: _evalError puts a
	// verdict behind the path that reaches the value it was raised on, and a
	// generated file names _evalNode without ever naming the constructor that
	// answer is built with.
	// jsonNumber, the --exact-numbers shadow, refuses a string through
	// jsonValueErrorf as jsonInteger does, so it closes over the same block.
	//
	// The null walker, the union's key readers and --strict-read-write's
	// walker all read a document in place, through jsonDoc; and the in-place
	// decode reads a member's refusal for the schema's words through the
	// decode-path block. Those are settled first, since the path-join block is
	// what both of them build their messages with.
	if h.NullCheck || h.OneOf || h.OneOfDiscriminator || h.Access {
		h.Decode = true
	}
	if h.Decode {
		h.DecodePath = true
	}
	// The identity blocks, from the callers down; each edge is a call the block
	// makes (TestIdentityBlocksAreMinimal holds them to the source). The
	// evaluator's equality arms and the object-level const compare decoded
	// values; a value's kind is read, for a value that is not decoded JSON, off
	// its tree; the walker confirms a match on trees and reads a lazily read
	// value as the document keeps it; and a const, and the number reader under
	// every identity, read literals as the JSON-equality reduction does.
	if h.AnnotationsEquality || h.DynamicConst {
		h.IdentityAny = true
	}
	if h.IdentityKind {
		h.IdentityValue = true
	}
	if (h.IdentityValue || h.IdentityAny) && h.Decode {
		h.IdentityLazy = true
	}
	if h.IdentityValue || h.IdentityAny {
		h.IdentityConst = true
	}
	if h.IdentityConst || h.IdentityLazy {
		h.IdentityCore = true
	}
	if h.IdentityCore {
		h.Canonical = true
	}
	if h.Integer || h.Number || h.DateTime || h.IPAddr || h.NullCheck || h.DecodePath || h.Annotations {
		h.PathJoin = true
	}
	// Every block below writes a string taken from the document into a
	// message -- a format checker the value it refused, a walker the key it
	// was under -- and does it through the one quoting rule, which lives in a
	// block of its own. See Quote.
	if h.Format || h.FormatHostname || h.DateTime || h.IPAddr || h.NullCheck || h.DecodePath || h.Access || h.Annotations || h.Canonical {
		h.Quote = true
	}
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
	set.Roots = identifiersIn(src)
	// The compiled patterns. A generated file names the package-level variable
	// each pattern is held in, and the registry PatternVarName filled while the
	// file was rendered says which pattern that is. So the one reading here is
	// both which patterns to compile and whether the block that compiles them
	// is needed at all.
	set.Patterns = patternsReferencedBy(src)
	if strings.Contains(src, "_schemagenQuote(") || strings.Contains(src, "_schemagenClipText(") || strings.Contains(src, "_schemagenClipErr(") {
		set.Quote = true
	}
	if strings.Contains(src, "_schemagenUndecided(") {
		set.Undecided = true
	}
	if strings.Contains(src, "oneofHasRequiredFields(") {
		set.OneOf = true
	}
	if strings.Contains(src, "oneofDiscriminatorValue(") {
		set.OneOfDiscriminator = true
	}
	// The in-place decode. Every type that decodes in place names the
	// document type in its method, and every one that opens a document names
	// the function that opens it; the second can appear without the first --
	// a scalar alias whose null rule reaches inside it -- so both are matched.
	if strings.Contains(src, "*jsonDoc") || strings.Contains(src, "jsonOpenDoc(") {
		set.Decode = true
	}
	// The path-join block. Six names reach it -- the four constructors a
	// message states what precedes it with, and the two joiners that read what
	// they recorded -- and a file can carry any one without the others: a leaf
	// alias only ever builds, and a struct whose members are all named only ever
	// joins. All six are matched for that reason, and jsonWrapf with them.
	// jsonValueWrapf is matched without its parenthesis, since a union at the top
	// of a value assigns it rather than calling it.
	if strings.Contains(src, "jsonValueErrorf(") || strings.Contains(src, "jsonElemErrorf(") ||
		strings.Contains(src, "jsonStepErrorf(") || strings.Contains(src, "jsonValueWrapf") ||
		strings.Contains(src, "jsonWrapf(") || strings.Contains(src, "jsonElemWrapf(") ||
		strings.Contains(src, "jsonPathf(") || strings.Contains(src, "jsonElemPathf(") {
		set.PathJoin = true
	}
	// The decode-path block. A file reaches it either by tracing a failed struct
	// decode back to the member at fault or, where it has no struct of its own,
	// by reading a leaf's refusal for what has to be written in front of it -- an
	// overflow map's value decode is the second without the first, so both names
	// are matched.
	if strings.Contains(src, "jsonDecodeMemberError(") || strings.Contains(src, "jsonDecodeRefusal(") {
		set.DecodePath = true
	}
	// The _dyn* family is matched by its prefix and pulled in whole, rather than
	// by a list of names that has to be kept in step with the templates by hand
	// -- all but _dynConstOK, a block of its own because it is the one that
	// compares values, and so the one that takes the identity blocks with it.
	if strings.Contains(src, "_dyn") {
		set.Dynamic = true
	}
	if strings.Contains(src, "_dynConstOK(") {
		set.DynamicConst = true
	}
	// The annotation evaluator calls the _dyn* predicates, so it pulls both in.
	if strings.Contains(src, "_schemaNode") || strings.Contains(src, "_evalNode(") {
		set.Annotations = true
		set.Dynamic = true
		// The evaluator's pattern arms are the one part of a helper block that
		// is compiled in conditionally, because the engine behind them is a
		// third-party dependency and a package that never names a pattern
		// should not acquire it. It is also the one signal here that is not a
		// call: the arms are reached from inside the block and never from the
		// file, so what the file carries is the field the arms exist to
		// interpret. Both spellings that set it are matched -- "pattern" on a
		// node, and a patternProperties member list, which is also what makes
		// an additionalProperties node have to run the patterns to know what is
		// left over. Missing one would leave the field set with nothing reading
		// it, which does not compile: the node field is typed by the pattern
		// block, which the arms are what need.
		if nodeFieldPattern.MatchString(src) || nodeFieldPatternProperties.MatchString(src) {
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
		if nodeFieldContent.MatchString(src) {
			set.AnnotationsContent = true
			set.Content = true
		}
		// const, enum and uniqueItems are the evaluator's only comparisons of
		// values, and so its only use of the identity blocks; read the same way,
		// off the three fields a node stating one carries.
		if nodeFieldEquality.MatchString(src) {
			set.AnnotationsEquality = true
		}
		// The recursive and dynamic arms are read the same way, off the three
		// fields only a file needing them can carry: a node pointing at another
		// node, a reference the dynamic scope resolves, and the frame a schema
		// resource publishes. All three are matched because each can appear
		// without the others -- a recursive schema with no dynamic reference in
		// it names only the first -- and a missed one is a file that names a
		// type the helpers do not declare, which does not compile.
		if nodeFieldRef.MatchString(src) || nodeFieldDynamic.MatchString(src) {
			set.AnnotationsDynamic = true
		}
	}
	// --strict-read-write's walker. Three signals rather than one, because the
	// three callers are independent: a type may carry rules and never be decoded
	// from a Validate check, and a Validate check may have to ignore the refusal
	// in a file whose own types carry no rules at all. The pattern arm is read
	// off the emitted table for the same reason the evaluator's is -- it is a
	// literal in the data rather than a call.
	if strings.Contains(src, "_accessRefuseReadOnly(") || strings.Contains(src, "_accessStripWriteOnly(") ||
		strings.Contains(src, "_decodeIgnoringReadOnly(") || strings.Contains(src, "_isReadOnlyRefusal(") ||
		strings.Contains(src, "_readOnlyRefusal{") {
		set.Access = true
		// Both spellings a pattern takes in a rule are matched: a step that
		// names members by pattern, and the patterns an additionalProperties
		// step steps past. The second was once missed, which left an
		// ExceptPatterns list emitted and nothing reading it -- a member a
		// pattern claims then read as additional. Each is now typed by the
		// pattern block, so a miss no longer compiles.
		if accessFieldPattern.MatchString(src) {
			set.AccessPattern = true
		}
	}
	// jsonInteger and its three container rebuilders come as one block, and the
	// name appears in every use of any of them.
	if strings.Contains(src, "jsonInteger") {
		set.Integer = true
	}
	// The exact-number blocks. The shadow type's name appears wherever a number
	// is decoded through it, and the two comparisons' names at every check they
	// are the argument of, so a substring each pulls in what the file uses. A
	// file holding a container of them names jsonIntegerSlice as well, and so
	// takes the integer block too: those three rebuilders are written over the
	// shape rather than the leaf and serve both, which costs a package a
	// jsonInteger it never calls and keeps one copy of each rebuilder rather
	// than two.
	if strings.Contains(src, "jsonNumber") {
		set.Number = true
	}
	if strings.Contains(src, "jsonNumberCmp(") || strings.Contains(src, "jsonNumberIsMultipleOf(") {
		set.NumberCompare = true
	}
	// The date-time shadow, read the same way and taking the same bargain as the
	// number one: a file holding a container of them names jsonIntegerSlice as
	// well and so takes the integer block too, which is one jsonInteger nobody
	// calls against one copy of each rebuilder rather than three.
	if strings.Contains(src, "jsonDateTime") {
		set.DateTime = true
	}
	// The two ip shadows, read the same way. They are one block: the two types
	// differ only in the word their refusal names, and share the decode under
	// it, so a file naming either takes both.
	if strings.Contains(src, "jsonIPv4Addr") || strings.Contains(src, "jsonIPv6Addr") {
		set.IPAddr = true
	}
	// The JSON-equality reduction. One block reached from two names -- the
	// reduction itself and the list initialiser that applies it at package
	// initialisation -- and the second appears in a file whose enum type is
	// declared elsewhere, so both are matched.
	if strings.Contains(src, "_jsonCanonical(") || strings.Contains(src, "_jsonCanonicalTexts(") {
		set.Canonical = true
	}
	// The identity blocks. A file naming anything a block declares takes the
	// block, and CloseOverCalls the blocks it calls. Read off the file's
	// identifiers, so a name in a comment takes nothing.
	ids := make(map[string]bool, len(set.Roots))
	for _, id := range set.Roots {
		ids[id] = true
	}
	for _, b := range identityBlocks {
		for _, d := range b.decls {
			if ids[d] {
				b.set(&set)
				break
			}
		}
	}
	// jsonNullRule and checkJSONNullsAt come as one block, and the walker's name
	// appears at every call site, so one substring pulls both in. The rule type
	// alone never appears without a call: it exists only as that call's
	// argument. A struct rejecting a null at its own top level writes the check
	// inline and needs neither.
	if strings.Contains(src, "checkJSONNullsAt(") {
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

// The node and rule literal fields the signals above read. A literal field is
// matched as its key, a colon and any run of white space: gofmt aligns the
// values of a composite literal's keys into a column, so the space after a
// key's colon is one space or several depending on its neighbours, and a
// match written with one space missed every key gofmt had padded. A reference
// is matched by the & of a hoisted node, whose name is "_rt..." for a
// runtime-evaluated type and "_et..." for an element schema (see elementNode).
var (
	nodeFieldPattern           = regexp.MustCompile(`\bPattern:\s+` + patternVarPrefix)
	nodeFieldPatternProperties = regexp.MustCompile(`\bPatternProperties:\s`)
	nodeFieldContent           = regexp.MustCompile(`\bContent(Encoding|MediaType):\s+_strPtr\(`)
	nodeFieldRef               = regexp.MustCompile(`\bRef:\s+&_`)
	nodeFieldDynamic           = regexp.MustCompile(`\bDynamic(Ref|Anchors):\s`)
	nodeFieldEquality          = regexp.MustCompile(`\bConst:\s+_strPtr\(|\bEnum:\s+\[\]string\{|\bUniqueItems:\s+true\b`)
	accessFieldPattern         = regexp.MustCompile(`\bKind:\s+_accessPattern|\bExceptPatterns:\s`)
)

// identityBlock is one block of the identity helpers: the template it is
// emitted from, the flag that emits it, and every name it declares at the top
// level of the package.
type identityBlock struct {
	template string
	set      func(*HelperSet)
	decls    []string
}

// identityBlocks lists the identity helpers' blocks. A generated file naming
// anything a block declares takes that block, and CloseOverCalls the blocks it
// calls. Every name is listed, not only the ones generated code names today, so
// a template that starts naming one directly pulls its block in with no change
// here; the emitter's tests hold each list to what its template declares, so
// the two cannot drift.
var identityBlocks = []identityBlock{
	newIdentityBlock("identity_core_helpers", func(h *HelperSet) { h.IdentityCore = true },
		"jsonID", "jsonIDSeeds", "jsonIDNullKind", "jsonIDTrueKind", "jsonIDFalseKind", "jsonIDStringKind",
		"jsonIDNumberKind", "jsonIDLiteralKind", "jsonIDArrayKind", "jsonIDObjectKind", "jsonIDMemberKind",
		"jsonIDMix", "jsonPutUint64", "jsonIDOfKind", "jsonIDBool", "jsonIDText", "jsonIDBytes", "jsonIDString",
		"jsonIDNumber", "jsonNumberDigits", "jsonAppendDigits", "jsonIDInt", "jsonIDUint", "jsonIDFloat",
		"jsonIDNumberLiteral", "jsonIsNumberLiteral", "jsonIDArray", "jsonIDMemberOf", "jsonIDObjectOf", "jsonIDIn", "jsonValidString",
		"jsonIDRaw", "jsonIDRawMessage", "jsonIDRawAsDecoded", "jsonIDReadAll", "jsonIDReader", "jsonIDMaxDepth",
		"jsonIDMember", "jsonIDMembers", "jsonIDKeyAgain"),
	newIdentityBlock("identity_const_helpers", func(h *HelperSet) { h.IdentityConst = true },
		"jsonConst", "jsonConsts", "jsonConstOf", "jsonMatchesConstRaw", "jsonTreeRaw", "jsonTreeEqual", "jsonTreeNumber"),
	newIdentityBlock("identity_any_helpers", func(h *HelperSet) { h.IdentityAny = true },
		"jsonIDJSON", "jsonNotJSON", "jsonTreeJSON", "jsonTreeJSONIn", "jsonMatchesJSON", "jsonFirstDuplicateJSON"),
	newIdentityBlock("identity_lazy_helpers", func(h *HelperSet) { h.IdentityLazy = true },
		"jsonIDCache"),
	newIdentityBlock("identity_value_helpers", func(h *HelperSet) { h.IdentityValue = true },
		"jsonIDTime", "jsonIDObject", "jsonIDObj", "jsonIDObjMember", "jsonIDObjComputed", "jsonIDObjRaw",
		"jsonIDObjDeferred", "jsonIDMemberFunc", "jsonTreeWritten", "jsonTreeWrittenNumber", "jsonIdentifier",
		"jsonValidation", "jsonIDNull", "jsonIDRawIn", "jsonIDNumberIn", "jsonArrayKey", "jsonIdentify",
		"jsonIDPtr", "jsonIDSlice", "jsonIDSliceKept", "jsonIDMap", "jsonIDsOf", "jsonFirstDuplicate",
		"jsonSameValue", "jsonTreeOf", "jsonMatchesConst", "jsonMatchesConstAt", "jsonTreeAny", "jsonTreeView",
		"jsonTreeSame", "jsonTreeOfIdentifier", "jsonTreeReflect", "jsonTreeMarshaled", "jsonMarshalError",
		"jsonMarshalText", "jsonIdentifyAt", "jsonIDAny", "jsonIDAnySlice", "jsonIDAnyMap", "jsonTreeHolder", "jsonTextMarshaler",
		"jsonIdentifierType", "jsonTreeHolderType", "jsonMarshalerType", "jsonTextMarshalerType", "jsonNumberType", "jsonRawMessageType",
		"jsonTimeType", "jsonIDReflect", "jsonIDMarshaled", "jsonOmitEmptyAt", "jsonOmitZeroAt"),
	newIdentityBlock("identity_kind_helpers", func(h *HelperSet) { h.IdentityKind = true },
		"jsonKindAt", "jsonKindAny", "jsonFloatOf", "jsonKindNumber", "jsonKindRaw"),
}

func newIdentityBlock(template string, set func(*HelperSet), decls ...string) identityBlock {
	return identityBlock{template: template, set: set, decls: decls}
}

// IdentityBlockDecls returns, for each block of the identity helpers, the
// template it is emitted from and the names it declares -- the table the
// emitter's tests check the templates and the emitted files against.
func IdentityBlockDecls() map[string][]string {
	out := make(map[string][]string, len(identityBlocks))
	for _, b := range identityBlocks {
		out[b.template] = append([]string(nil), b.decls...)
	}
	return out
}

// anyIdentity reports whether any identity block is set.
func (h HelperSet) anyIdentity() bool {
	return h.IdentityCore || h.IdentityConst || h.IdentityAny || h.IdentityLazy || h.IdentityValue || h.IdentityKind
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
var annotationNodeFormat = regexp.MustCompile(`\bFormat:\s+_strPtr\((` + "`" + `[^` + "`" + `]*` + "`" + `|"(?:[^"\\]|\\.)*")\)`)

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

// wrapProse breaks generator-written comment text into lines no wider than
// width, at spaces, and joins them with newlines.
//
// The emitter's `comment` function continues an embedded newline with the "//"
// of the line it is on, so a paragraph wrapped here arrives in the generated
// source as a Go comment block. Doing the wrapping here rather than in the
// template is what lets the text be assembled from a type name and an anchor
// whose lengths are not known until generation.
//
// Nothing is broken mid-word: a single word longer than width takes a line of
// its own and overruns it, which is right for the things that produce one --
// a Go identifier, a URI, a quoted anchor -- since splitting any of those makes
// the comment say something that is not there.
func wrapProse(text string, width int) string {
	var b strings.Builder
	col := 0
	for i, word := range strings.Fields(text) {
		switch {
		case i == 0:
			col = len(word)
		case col+1+len(word) > width:
			b.WriteString("\n")
			col = len(word)
		default:
			b.WriteString(" ")
			col += 1 + len(word)
		}
		b.WriteString(word)
	}
	return b.String()
}

// identifiersIn is every identifier Go source uses, sorted and without
// repeats: its tokens, so that a name in a comment or a string is not one.
func identifiersIn(src string) []string {
	var sc scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	sc.Init(file, []byte(src), nil, 0)
	seen := map[string]bool{}
	for {
		_, tok, lit := sc.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.IDENT {
			seen[lit] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
