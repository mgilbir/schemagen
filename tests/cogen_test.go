package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// This is the driver for the co-generation harness; the grammar it drives is
// in cogen_grammar_test.go.
//
// Layer 1 (FuzzGenerate) proves the pipeline does not panic, which says
// nothing about whether the code it emits is correct. Each iteration here
// closes that: build a schema and a conforming instance together, generate and
// compile the bindings, round-trip the instance through them, check Validate
// accepts it, and check Validate rejects a family of mutants that each break
// exactly one keyword.
//
// The negative half is the part that carries the weight. If the generator
// silently fails to emit a constraint check, Validate degenerates to
// `return nil` and every conforming instance still passes, so an all-valid
// corpus cannot see the defect at all. Only a mutant that is supposed to be
// rejected can.
//
// The other axis is the generator configuration. The CLI can emit rather more
// than one shape of code -- big-integer wrappers, strict property checking,
// value fields instead of pointers, the hybrid and runtime validation modes --
// and every one of those is code that ships. FuzzGenerate walks that matrix but
// only asks whether the pipeline panics; it never compiles what comes out. So
// each iteration here also picks a configuration, and the expectations that
// genuinely differ between configurations are written down as such rather than
// blunted into one set of checks weak enough to pass everywhere. See coConfigs.

// coEnvRun gates the whole suite, exactly as SCHEMAGEN_RUN_EXTERNAL gates the
// JSON Schema Test Suite run. Each iteration compiles and runs a throwaway Go
// module, so leaving this in the default `go test ./...` would add minutes to
// a run that takes seconds.
const coEnvRun = "SCHEMAGEN_RUN_COGEN"

// coRunTimeout bounds one generated program's build-and-run. Generated code is
// straight-line validation with no loops over unbounded input, so anything
// near this is a hang, not slow work.
const coRunTimeout = 90 * time.Second

// coVerdict is what the generated program reported for one mutant.
type coVerdict struct {
	outcome string // "ACCEPTED", "UNMARSHAL" or "INVALID"
	msg     string
}

// coResult is the whole report from one compiled-and-run case.
type coResult struct {
	rtOK    bool
	rtMsg   string
	posOK   bool
	posMsg  string
	mutants []coVerdict
	// distinct says the configuration emitted source unlike what coBaseConfig
	// emits for the same document. It is what turns "this configuration
	// exercises a different code path" from an assumption into a count the run
	// prints.
	distinct bool
	// needsRuntime says the emitted code imports pkg/validationruntime, so the
	// throwaway module needs a replace pointing at this checkout to resolve it.
	needsRuntime bool
}

// coFailure is one thing that went wrong, in a form the shrinker can match
// against: kind says which check failed and mutKey identifies the mutation
// when the check was a mutation, so shrinking chases the same defect rather
// than settling for any failure it happens to reach.
type coFailure struct {
	kind   string
	mutKey string
	detail string
}

func (f coFailure) String() string {
	if f.mutKey == "" {
		return fmt.Sprintf("%s: %s", f.kind, f.detail)
	}
	return fmt.Sprintf("%s [%s]: %s", f.kind, f.mutKey, f.detail)
}

// sameDefect reports whether g is the failure f was, for shrinking purposes.
func (f coFailure) sameDefect(g coFailure) bool {
	return f.kind == g.kind && f.mutKey == g.mutKey
}

// ---------------------------------------------------------------------------
// The configuration matrix
// ---------------------------------------------------------------------------

// coPackageName is the package every configuration generates into; coRunCase
// rewrites the clause to `main` before compiling.
const coPackageName = "testpkg"

// coConfig is one generator configuration, together with whatever the harness
// has to say differently about the code it produces.
//
// The two hooks exist for opposite reasons and should not be confused with each
// other. adjust narrows the *input*, to the documents the configuration can
// honestly be held to: either because the grammar's "the instance conforms by
// construction" claim stops being true under the configuration -- a fact about
// the schema rather than about the generator -- or because the construct is a
// known gap, in which case it is a named predicate that says so. extra widens
// the *output* demands: it adds mutants that only this configuration is supposed
// to reject. Neither of them ever relaxes a check --
// the round-trip is exact equality under every configuration, a conforming
// instance is accepted under every configuration, and every mutant the grammar
// derives has to be rejected, with the violated keyword named, under every
// configuration.
type coConfig struct {
	name string
	cfg  generator.Config

	// adjust rewrites a freshly built document in place. Applied to the built
	// document and to every shrink candidate, so the shrinker never lands on a
	// document the configuration cannot be held to.
	adjust func(*coDoc)

	// extra contributes the mutations only this configuration makes meaningful.
	extra func(*coDoc) []coMutation
}

// coConfigs is the matrix. Each entry differs from the historical single
// configuration along one axis, plus one entry that turns everything on at
// once, because flags that are individually sound have still to compose.
//
// What is deliberately *not* here: Resolver, Draft, FieldNames, SharedTypes,
// ImportPath/CrossPackage and RootTypeName. The grammar emits one dialect and
// no remote or cross-document refs, so those either have nothing to act on or
// would need a second document to mean anything.
var coConfigs = []*coConfig{
	// The configuration this harness ran before the matrix existed, kept first
	// so it is the baseline every other one is compared against.
	{
		name: "static",
		cfg:  generator.Config{PackageName: coPackageName, OmitEmpty: true},
	},

	// Hybrid and runtime validation. Both reduce to the static plan unless the
	// schema carries $dynamicRef, $recursiveRef, unevaluatedItems,
	// unevaluatedProperties or resources of mixed drafts -- see
	// analyzeValidationCapability -- none of which this grammar emits, so today
	// they emit byte-identical source to `static` for every document it can
	// build. That is measured rather than assumed: the per-configuration
	// summary counts how many iterations emitted source unlike the baseline's,
	// so "this configuration is currently decorative" is a number the run
	// prints rather than a claim in a comment, and the day the grammar grows an
	// unevaluated* keyword the number stops being zero on its own.
	//
	// They are still run rather than skipped. The mode is threaded through the
	// whole planner, and a regression that made hybrid mode drop or mangle an
	// ordinary check would be invisible to a suite that only ever asked for
	// static.
	{
		name: "hybrid",
		cfg:  generator.Config{PackageName: coPackageName, OmitEmpty: true, Validation: generator.ValidationModeHybrid},
	},
	{
		name: "runtime",
		cfg:  generator.Config{PackageName: coPackageName, OmitEmpty: true, Validation: generator.ValidationModeRuntime},
	},

	// Arbitrary-precision integers. A named integer type -- a $defs entry the
	// grammar's properties $ref -- stops being `type DefA int64` and becomes a
	// struct holding an int64, a *big.Int and a flag, with its own
	// UnmarshalJSON, MarshalJSON and a Validate that compares through big.Float.
	// Every expectation survives unchanged, which is the point of running it:
	//
	//   round-trip  the wrapper's MarshalJSON writes the bare number back, so
	//               the emitted JSON shape is the same as the plain alias's and
	//               exact equality still has to hold. If it did not, the flag
	//               would be silently corrupting documents.
	//   messages    the big.Float branch phrases its errors with %s and the
	//               decimal string where the int64 branch uses %v, but the
	//               wording the mutations match on ("is less than minimum",
	//               "exceeds maximum", "is not a multiple of") is the same.
	//   type mutants a string where an integer belongs still dies in
	//               UnmarshalJSON, which is what Loose already expects.
	//
	// What none of that reaches is the reason the flag exists, since every
	// integer the grammar writes fits in an int64 several times over.
	// coBigIntOverflowMutations supplies a value that does not.
	{
		name:  "bigint",
		cfg:   generator.Config{PackageName: coPackageName, OmitEmpty: true, BigIntSupport: true},
		extra: coBigIntOverflowMutations,
	},

	// Absent additionalProperties treated as false. This is the one
	// configuration that adds a rule rather than changing a representation, so
	// it is also the one that needs a mutation of its own -- no mutant derived
	// from the schema's own keywords can reach a check the schema does not
	// mention. coExtraPropertyMutations supplies it.
	{
		name:   "strict",
		cfg:    generator.Config{PackageName: coPackageName, OmitEmpty: true, StrictProperties: true},
		adjust: coStrictNarrow,
		extra:  coExtraPropertyMutations,
	},

	// Optional properties as value fields rather than pointers.
	{
		name:   "noomit",
		cfg:    generator.Config{PackageName: coPackageName},
		adjust: coForcePresent,
	},

	// Refs that nothing can serve degrade to `any` instead of failing
	// generation. As implemented the flag is a single gate at the end of
	// Generate -- it suppresses UnresolvedRefsError and nothing else -- and
	// every ref this grammar writes points at a $defs entry of the same
	// document, so it has nothing to suppress and cannot change a byte of the
	// output. The summary's distinct count says so on every run.
	//
	// What it is here to catch is the failure that flag has available to it: if
	// the lenient path ever started degrading refs it *could* resolve, the
	// wrapper types behind them would become bare `any`, their Validate would go
	// away, and every mutant aimed through a $ref would be accepted. Nothing
	// cheaper than compiling and running the result can tell that apart from a
	// correct degradation of a ref that really was unservable.
	{
		name: "lenientrefs",
		cfg:  generator.Config{PackageName: coPackageName, OmitEmpty: true, LenientRefs: true},
	},

	// Everything at once. Flags that are individually correct can still
	// interact -- a big-integer wrapper as a value field, an overflow check on a
	// struct whose optional properties are no longer pointers -- and no
	// single-axis entry can see that.
	{
		name: "all",
		cfg: generator.Config{
			PackageName:      coPackageName,
			StrictProperties: true,
			BigIntSupport:    true,
			LenientRefs:      true,
			Validation:       generator.ValidationModeHybrid,
		},
		adjust: func(d *coDoc) { coForcePresent(d); coStrictNarrow(d) },
		extra: func(d *coDoc) []coMutation {
			return append(coExtraPropertyMutations(d), coBigIntOverflowMutations(d)...)
		},
	},
}

// coBaseConfig is the configuration every other one's emitted source is
// compared against, so the summary can say which configurations are actually
// producing different code.
var coBaseConfig = coConfigs[0]

func coConfigNamed(name string) *coConfig {
	for _, cc := range coConfigs {
		if cc.name == name {
			return cc
		}
	}
	return nil
}

func coConfigNames() []string {
	out := make([]string, 0, len(coConfigs))
	for _, cc := range coConfigs {
		out = append(out, cc.name)
	}
	return out
}

// coConfigFor deals the configurations out in blocks: each consecutive run of
// len(coConfigs) iterations contains every configuration exactly once, in an
// order derived from the base seed and the block index.
//
// Dealing rather than drawing is deliberate. An independent draw per iteration
// leaves the counts to chance, and over a hundred iterations across eight
// configurations the thinnest one is routinely half the size of the busiest --
// on a short run a configuration can be missed altogether, and a matrix that
// might not have run a configuration is not evidence about it. A plain
// `iter % len(coConfigs)` would be even, but it would pin each configuration to
// one residue class of the iteration index for good, so a configuration would
// only ever meet the schemas that residue happens to produce. Shuffling within
// the block keeps the counts even to within one and still lets any
// configuration meet any schema.
//
// It stays a pure function of (seed, iter), which is what the replay line in a
// failure report depends on.
func coConfigFor(seed uint64, iter int) *coConfig {
	n := len(coConfigs)
	block, within := iter/n, iter%n
	if within < 0 {
		block, within = block-1, within+n
	}
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	rng := rand.New(rand.NewPCG(seed^0xC0FFEEC0FFEE, coIterSeed(seed, block)))
	rng.Shuffle(n, func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })
	return coConfigs[perm[within]]
}

// coDocFor builds the document an iteration runs, already adjusted for the
// configuration it will run under.
func coDocFor(cc *coConfig, seed uint64) *coDoc {
	d := coBuild(seed)
	if cc.adjust != nil {
		cc.adjust(d)
	}
	return d
}

// coMutationsFor is the mutation catalogue for one document under one
// configuration: everything the grammar derives from the schema, plus whatever
// the configuration adds.
func coMutationsFor(cc *coConfig, d *coDoc) []coMutation {
	muts := d.mutations()
	if cc.extra != nil {
		muts = append(muts, cc.extra(d)...)
	}
	return muts
}

// ---------------------------------------------------------------------------
// Configuration-dependent document adjustments
// ---------------------------------------------------------------------------

// coForcePresent makes every declared property present in the instance.
//
// With OmitEmpty false an optional property is emitted as a plain value field
// rather than a pointer, and its tag carries neither omitempty nor omitzero. An
// *absent* optional property therefore has no representation at all: the field
// decodes to its Go zero and marshals straight back out, so `{"alpha":5}`
// returns as `{"alpha":5,"delta":false,"foxtrot":0}`. That is what the flag is
// for -- always emit every field -- so the honest response is to stop asking
// this configuration to round-trip documents that leave an optional property
// out, not to stop comparing the round-trip exactly.
//
// Nothing else moves. Round-trip equality stays exact, the conforming instance
// still has to be accepted, and every mutant including `required` still has to
// be rejected. In particular validation is *not* weakened by the flag and is
// not being compensated for here: the generated Validate gates each optional
// property's checks on _jsonKeys, which records the keys the source JSON
// actually carried, so an absent optional property is not validated against its
// zero value even when the field holds one.
func coForcePresent(d *coDoc) {
	for _, n := range coNodesOf(d) {
		for _, p := range n.props {
			p.present = true
		}
	}
}

// coStripDiscriminators removes the object-level anyOf / oneOf branches.
//
// Those branches declare their discriminator property inside the branch, and
// additionalProperties does not see inside an applicator. Against
//
//	{"type":"object","properties":{"alpha":{"type":"integer"}},
//	 "required":["alpha"],"additionalProperties":false,
//	 "anyOf":[{"properties":{"tagOne":{"type":"string"}},"required":["tagOne"]},
//	          {"properties":{"tagTwo":{"type":"integer"}},"required":["tagTwo"]}]}
//
// the document {"alpha":5,"tagOne":"x"} is invalid: `tagOne` is not among the
// object's own properties, so additionalProperties:false forbids it, and no
// document can satisfy both the anyOf and the ban. StrictProperties means
// exactly "absent additionalProperties is treated as false", so under it the
// grammar cannot co-generate a conforming instance for a discriminated object
// at all. That is a property of the schema, so the branches come out; the
// alternative would be to declare the instance's own rejection acceptable,
// which would then mask every other mutant of that document behind the same
// error.
//
// allOf branches are left alone. The generator flattens their properties into
// the struct and counts them among the known fields, so a property declared in
// an allOf branch is not overflow and the instance still conforms. (Strictly
// read, additionalProperties does not see inside allOf either, so the two
// applicators are treated differently; that asymmetry is reported as a finding
// rather than worked around here.)
func coStripDiscriminators(d *coDoc) {
	for _, n := range coNodesOf(d) {
		n.branches = nil
		n.branchIx = 0
	}
}

// coStripConditionals removes the object-level if/then.
//
// It is coStripDiscriminators' argument with `then` in place of a branch.
// `then` declares coThenKey and requires it, and the instance carries coThenKey
// exactly when `if` matches -- but additionalProperties reads the `properties`
// and `patternProperties` of the schema object it sits in and nothing else, so
// coThenKey is additional to the object itself. Against
//
//	{"type":"object","properties":{"alpha":{"type":"integer"},
//	                               "condKey":{"type":"string"}},
//	 "required":["alpha"],"additionalProperties":false,
//	 "if":{"properties":{"condKey":{"const":"yes"}},"required":["condKey"]},
//	 "then":{"properties":{"thenKey":{"type":"integer","minimum":1}},
//	         "required":["thenKey"]}}
//
// no document can both match `if` and satisfy the ban: `then` requires a key
// additionalProperties forbids. StrictProperties is exactly that ban, so under
// it the grammar cannot co-generate a conforming instance for a conditional
// object, and the if/then comes out rather than the harness declaring the
// instance's own rejection acceptable. Left in, it was the single largest source
// of failure -- every one of the 22 positive-instance rejections in a 300
// iteration strict-pinned run was `additional property "thenKey" is not
// allowed`, and each of them masked every other mutant of that document behind
// the same error.
//
// Removing the whole conditional rather than just coThenKey is deliberate: `if`
// without `then` constrains nothing, and the mutations the grammar derives here
// -- thenRequired, thenSchema, thenTriggered and the conditional
// unevaluatedProperties mutant -- are all statements about `then`. They keep
// running under the six configurations that do not set StrictProperties.
//
// One narrower exclusion was considered and rejected: keeping the conditional
// alive on objects that also carry unevaluatedProperties, which schemagen used
// to leave outside the ban altogether -- generator.go took an earlier branch
// for that case and left Forbidden false, so such an object emitted
// `unevaluated property %q is not allowed` and no `additional property %q is
// not allowed` at all. That would have had the harness assert that a document
// the flag's own documented meaning -- "absent additionalProperties is treated
// as false" -- rejects is accepted, blessing the inconsistency rather than
// reporting it. It was reported instead, as issue #71, and fixed: the ban is
// now synthesised on that object too, so the exclusion no longer exists to be
// taken. coStripUnevaluatedProperties below is the consequence.
func coStripConditionals(d *coDoc) {
	for _, n := range coNodesOf(d) {
		n.cond = nil
	}
}

// coStripPropertyNames removes the propertyNames constraint.
//
// Unlike the two above this is not about the conforming instance, which
// satisfies propertyNames whatever else is true: every name the grammar writes
// is an identifier. It is about the mutant. propertyNames is violated by giving
// the object a key with a name it refuses, and every name it refuses is a name
// the object's `properties` does not declare -- so under StrictProperties that
// same key is additional, and the ban reports first (the generated Validate
// checks the overflow map before it walks the key names). Against
//
//	config   generator.Config{StrictProperties: true}
//	schema   {"type":"object","properties":{"charlie":{"type":"number"}},
//	          "propertyNames":{"pattern":"^[A-Za-z_][A-Za-z0-9_]*$"}}
//	instance {"charlie":16}
//	mutant   {"charlie":16,"9-bad":1}
//	         Validate reports `additional property "9-bad" is not allowed`
//
// Both are correct refusals of the same edit, and the verdict -- rejected -- is
// right either way; there is simply no key left for propertyNames to be the
// reason for. Rather than accept whichever rule answers, which would stop the
// mutant proving propertyNames is enforced at all, the keyword comes out under
// this configuration and keeps proving it under the other six.
//
// The alternative -- a key that patternProperties covers, so it is not
// additional, but propertyNames still refuses -- does not exist in this grammar:
// coPatternExpr's language is a subset of coPropNamePattern's, so a key matching
// the one always satisfies the other.
func coStripPropertyNames(d *coDoc) {
	for _, n := range coNodesOf(d) {
		if n.extra == coExtraPropNames {
			n.extra = coExtraNone
		}
	}
}

// coStripUnevaluatedProperties removes the unevaluatedProperties constraint.
//
// It is both of the arguments above at once, which is why it needs neither a
// new one nor an exception.
//
// The instance first. A schema-valued unevaluatedProperties is only worth
// writing if something is left for it to judge, so the grammar puts coUnevalKey
// in the instance -- a key no applicator evaluates, which is the point of it.
// That key is not among the object's own `properties` either, so under
// StrictProperties it is additional and the conforming instance is refused:
//
//	config   generator.Config{StrictProperties: true}
//	schema   {"type":"object","properties":{"bravo":{"type":"boolean"}},
//	          "unevaluatedProperties":{"type":"integer","minimum":-3}}
//	instance {"bravo":true,"xtraKey":-3}
//	         Validate reports `additional property "xtraKey" is not allowed`
//
// No document satisfies both, so as with the conditional this is a fact about
// the schema under the flag, not a defect in the code generated for it.
//
// Then the mutant, for unevaluatedProperties: false, whose instance does
// conform. It is violated by adding a key nothing evaluates, and every such key
// is one the object's `properties` does not declare -- so under StrictProperties
// it is additional too, and the ban reports first:
//
//	config   generator.Config{StrictProperties: true}
//	schema   {"type":"object","properties":{"bravo":{"type":"boolean"}},
//	          "unevaluatedProperties":false}
//	instance {}
//	mutant   {"zzExtra":1}
//	         Validate reports `additional property "zzExtra" is not allowed`
//
// Both refusals are correct and the verdict is the same either way, but taking
// whichever answers would stop the mutant proving unevaluatedProperties is
// enforced at all. The keyword comes out under this configuration and keeps
// proving it under the other six.
//
// This is new with the fix for issue #71 and is its cost, honestly stated: the
// ban used not to be synthesised at all on an object carrying
// unevaluatedProperties, so both documents above were accepted and the harness
// was passing on that inconsistency rather than on the flag's meaning.
func coStripUnevaluatedProperties(d *coDoc) {
	for _, n := range coNodesOf(d) {
		if n.extra == coExtraUnevalFalse || n.extra == coExtraUnevalSchema {
			n.extra = coExtraNone
		}
	}
}

// coStripOneOfObjBoth returns a coOneOfObj union to the arrangement where the
// instance carries only the taken branch's required key.
//
// It is coStripConditionals' argument one level down. Under objBoth the union's
// value carries both branches' required keys, and each branch declares only its
// own -- so whichever branch is selected, the other's key is additional to the
// variant type built from that branch. StrictProperties is the ban on exactly
// that:
//
//	config   generator.Config{StrictProperties: true}
//	schema   {"oneOf":[{"type":"object","properties":{"varStr":{"type":"string","minLength":3}},
//	                    "required":["varStr"]},
//	                   {"type":"object","properties":{"varInt":{"type":"integer","minimum":5}},
//	                    "required":["varInt"]}]}
//	instance {"varStr":"ab","varInt":7}
//	         no matching oneOf variant: variant Option1: additional property "varStr" is not allowed
//
// No document carrying both keys satisfies the flag's reading of either branch,
// so this is a fact about the schema under the flag rather than a defect in the
// code generated for it. Widening a branch with additionalProperties:true would
// dodge the ban, but it would also change what the branch means and cost the
// arrangement its point -- the branches must be silent about each other's keys
// for "exactly one branch" to rest on the nested constraints.
//
// The single-key arrangement keeps running here, and the both-keys one keeps
// running under the six configurations that do not set StrictProperties.
func coStripOneOfObjBoth(d *coDoc) {
	for _, n := range coNodesOf(d) {
		if n.kind == coOneOfObj {
			n.objBoth = false
		}
	}
}

// coStrictNarrow is every adjustment StrictProperties needs: the constructs
// whose keys live inside an applicator additionalProperties cannot see, and the
// two keywords whose mutants the ban pre-empts.
func coStrictNarrow(d *coDoc) {
	coStripDiscriminators(d)
	coStripConditionals(d)
	coStripPropertyNames(d)
	coStripUnevaluatedProperties(d)
	coStripOneOfObjBoth(d)
}

// ---------------------------------------------------------------------------
// Configuration-dependent mutations
// ---------------------------------------------------------------------------

// coStrictExtraName is a property name no document this grammar builds
// declares: it is in neither coPropNames nor coDiscNames.
const coStrictExtraName = "zzUndeclared"

// coExtraPropertyMutations adds, for every object in the instance, a mutant
// carrying one property the schema does not declare.
//
// This is the mutation StrictProperties exists for, and the configuration would
// have nothing to catch without it: the flag adds exactly one rule, and no
// mutant derived from a keyword the schema actually writes can reach a rule the
// schema does not mention.
//
// It is deliberately not Loose. The generated UnmarshalJSON keeps an
// undeclared property in AdditionalProperties on purpose, so that a document
// carrying one still round-trips byte-for-byte; the decoder must therefore
// accept it and Validate must be the thing that refuses. A verdict of UNMARSHAL
// here would mean round-trip fidelity had been traded away for the check.
//
// The message is required to name the property, but not which rule refused it.
// An object carrying unevaluatedProperties forbids the same key for its own
// reason, and whichever check runs first is the one that reports -- both are
// correct rejections of the same edit, and insisting on the StrictProperties
// wording would fail the harness for a schema the generator handled properly.
// Naming the property is the part that matters: it is what distinguishes
// "refused this key" from "refused the document for some other reason", which
// is the whole point of checking the message at all.
//
// An object under a live maxProperties is skipped, and that one is not a
// question of wording. The grammar states maxProperties at the instance's own
// key count, so the count is at the bound already and *any* added key exceeds it
// -- and the generated Validate counts keys before it looks at the overflow map,
// so what comes back is
//
//	config   generator.Config{StrictProperties: true}
//	schema   {"type":"object","properties":{"alpha":{"type":"integer"}},
//	          "required":["alpha"],"maxProperties":1}
//	instance {"alpha":5}
//	mutant   {"alpha":5,"zzUndeclared":"unexpected"}
//	         Validate reports `too many properties: 2 exceeds maximum 1`
//
// which never names the property. There is no key that is additional without
// also being one key too many, so at these objects the mutant cannot say what it
// was added to say. Dropping it here is narrower than dropping maxProperties
// from the configuration: the bound keeps its own mutant, and the ban keeps
// being exercised at every other object in the same document.
//
// An object carrying a schema-valued additionalProperties is skipped for a
// different reason again: StrictProperties adds no rule there at all. The flag
// reads "treat *absent* additionalProperties as false", and this object states
// one, so the added key is not banned but governed -- by a subschema that types
// the overflow map, into which the mutant's string value cannot decode. The
// verdict is UNMARSHAL, correctly and by construction, and nothing about the
// configuration under test is being asked. The subschema keeps its own mutant
// (coExtraAddlSchema's), which moves a value rather than its type and so lands
// in Validate where it belongs.
func coExtraPropertyMutations(d *coDoc) []coMutation {
	var out []coMutation
	for _, o := range coObjectPaths(d) {
		if o.node.maxPropsActive() || o.node.addlSchemaActive() {
			continue
		}
		out = append(out, coMutation{
			Keyword: "additionalProperties",
			Path:    coPath(o.path, coStrictExtraName),
			Prop:    coStrictExtraName,
			Value:   "unexpected",
			Want:    []string{coStrictExtraName},
		})
	}
	return out
}

// coBigIntOverflow is an integer twenty-three digits long: far past int64, and
// far past every bound the grammar writes, whose lattice sits within a few
// dozen of zero. It is a json.RawMessage so it survives the mutation's
// marshal/unmarshal round trip as written, rather than being turned into a
// float64 and back.
var coBigIntOverflow = json.RawMessage("99999999999999999999999")

// coBigIntOverflowMutations replaces a named integer with a value no int64 can
// hold, at every place the instance carries one.
//
// Without it BigIntSupport is only ever asked about integers that fit in an
// int64, which is the case its wrapper shares with the plain alias -- the
// int64 field, the fast decode path, no big.Int anywhere. This mutant takes the
// other branch of every one of those: UnmarshalJSON has to fall through to
// big.Int rather than reporting "cannot be represented as int64", and Validate
// has to compare through big.Float and reject it against the bound.
//
// Every integer the instance carries qualifies, wherever it was written. That
// was once restricted to the ones reached through a $ref, because only a *named*
// integer became the wrapper and an inline one stayed a plain int64 field that
// no such value could decode into at all (issue #67); an inline integer is now
// materialized into a wrapper of its own, so the restriction would only be
// hiding the commonest way to write the schema.
//
// A node with no upper bound is skipped: an enormous integer satisfies a lone
// minimum, so there would be nothing to reject.
func coBigIntOverflowMutations(d *coDoc) []coMutation {
	var out []coMutation
	var walk func(n *coNode, path []any, prop string)
	walk = func(n *coNode, path []any, prop string) {
		if n.kind == coRef {
			walk(d.defs[n.refName], path, prop)
			return
		}
		switch n.kind {
		case coObject:
			for _, p := range n.props {
				if p.present {
					walk(p.node, coPath(path, p.name), p.name)
				}
			}
		case coArray:
			if n.numItems > 0 {
				walk(n.elem, coPath(path, 0), prop)
			}
		case coMap:
			// A map's values reach the wrapper the same way an array's elements
			// do -- resolveArrayItemType serves both -- so an integer under
			// additionalProperties is as much a big-integer position as one
			// under items, and skipping it would leave the map half-covered.
			if len(n.mapKeys) > 0 {
				walk(n.elem, coPath(path, n.mapKeys[len(n.mapKeys)-1]), prop)
			}
		case coInteger:
			var want []string
			switch n.maxStyle {
			case coBoundInclusive:
				want = []string{prop, "exceeds maximum"}
			case coBoundExclusive:
				want = []string{prop, "must be less than"}
			default:
				return
			}
			// Via keeps this apart from the grammar's own bound mutant at the
			// same path, which differs only in the size of the number.
			out = append(out, coMutation{
				Keyword: "maximum", Via: "bigint", Path: path, Prop: prop,
				Value: coBigIntOverflow, Want: want,
			})
		}
	}
	walk(d.root, nil, "")
	return out
}

// coObject is one object the document puts in the instance, together with where
// it sits. The node comes along because a configuration deciding what to mutate
// at an object has to be able to ask what that object's schema already says.
type coObjectSite struct {
	node *coNode
	path []any
}

// coObjectPaths lists every object the document puts in the instance, root
// first. Paths run through present properties and through index 0 of a
// non-empty array, which is the same addressing the grammar's own mutations
// use, and $refs are followed so an object reached through one is reported at
// the place it actually appears -- with the node it was dereferenced to, since
// that is the one carrying the keywords.
func coObjectPaths(d *coDoc) []coObjectSite {
	var out []coObjectSite
	var walk func(n *coNode, path []any)
	walk = func(n *coNode, path []any) {
		n = d.deref(n)
		switch n.kind {
		case coObject:
			out = append(out, coObjectSite{node: n, path: path})
			for _, p := range n.props {
				if p.present {
					walk(p.node, coPath(path, p.name))
				}
			}
		case coArray:
			if n.numItems > 0 {
				walk(n.elem, coPath(path, 0))
			}
		}
	}
	walk(d.root, nil)
	return out
}

// ---------------------------------------------------------------------------
// Running one case
// ---------------------------------------------------------------------------

// coGenerate runs the schemagen pipeline over a co-generated schema under one
// configuration. Refs stay strict unless the configuration says otherwise, as
// in the external suite: a ref nothing can serve fails generation instead of
// quietly degrading to any.
func coGenerate(schemaJSON []byte, cc *coConfig) (string, error) {
	var s schema.Schema
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	s.Normalize()

	ir, err := generator.New(cc.cfg).Generate(&s)
	if err != nil {
		return "", fmt.Errorf("generate: %w", err)
	}
	em, err := emitter.New()
	if err != nil {
		return "", fmt.Errorf("emitter: %w", err)
	}
	src, err := em.Emit(ir)
	if err != nil {
		return "", fmt.Errorf("emit: %w", err)
	}
	return string(src), nil
}

// coMainProgram writes the driver that runs inside the generated module. It
// reports every check on its own line so one build and one run cover the
// round-trip, the positive case and every mutant; compiling once per mutant
// would multiply the cost of an iteration by the size of the mutation
// catalogue.
//
// The cases file is decoded into a map rather than a struct so this template
// needs no struct tags, which would otherwise have to carry backquotes through
// a Go string literal.
func coMainProgram(rootType string, hasValidate bool) string {
	validateCall := `fmt.Println("POS OK")`
	mutValidate := `fmt.Printf("MUT %d ACCEPTED\n", i)`
	if hasValidate {
		validateCall = `if err := obj.Validate(); err != nil {
			fmt.Printf("POS FAIL %s\n", oneLine(err.Error()))
		} else {
			fmt.Println("POS OK")
		}`
		mutValidate = `if err := mo.Validate(); err != nil {
			fmt.Printf("MUT %d INVALID %s\n", i, oneLine(err.Error()))
		} else {
			fmt.Printf("MUT %d ACCEPTED\n", i)
		}`
	}

	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
)

// oneLine collapses whitespace so one verdict always occupies one line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func main() {
	raw, err := os.ReadFile("cases.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading cases: %%v\n", err)
		os.Exit(1)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		fmt.Fprintf(os.Stderr, "parsing cases: %%v\n", err)
		os.Exit(1)
	}
	instance := doc["instance"]
	var mutants []json.RawMessage
	if len(doc["mutants"]) > 0 {
		if err := json.Unmarshal(doc["mutants"], &mutants); err != nil {
			fmt.Fprintf(os.Stderr, "parsing mutants: %%v\n", err)
			os.Exit(1)
		}
	}

	var obj %[1]s
	if err := json.Unmarshal(instance, &obj); err != nil {
		fmt.Printf("RT FAIL unmarshal: %%s\n", oneLine(err.Error()))
		fmt.Printf("POS FAIL unmarshal: %%s\n", oneLine(err.Error()))
	} else {
		if out, err := json.Marshal(obj); err != nil {
			fmt.Printf("RT FAIL marshal: %%s\n", oneLine(err.Error()))
		} else {
			var before, after any
			if err := json.Unmarshal(instance, &before); err != nil {
				fmt.Printf("RT FAIL reparse input: %%s\n", oneLine(err.Error()))
			} else if err := json.Unmarshal(out, &after); err != nil {
				fmt.Printf("RT FAIL reparse output: %%s\n", oneLine(err.Error()))
			} else if !reflect.DeepEqual(before, after) {
				fmt.Printf("RT FAIL mismatch in=%%s out=%%s\n", oneLine(string(instance)), oneLine(string(out)))
			} else {
				fmt.Println("RT OK")
			}
		}
		%[2]s
	}

	for i, m := range mutants {
		var mo %[1]s
		if err := json.Unmarshal(m, &mo); err != nil {
			fmt.Printf("MUT %%d UNMARSHAL %%s\n", i, oneLine(err.Error()))
			continue
		}
		%[3]s
	}
}
`, rootType, validateCall, mutValidate)
}

// coRunCase generates, compiles and runs one document plus its mutants under
// one configuration. A non-nil error means the pipeline or the compiler refused
// the case, which is itself a finding: every schema this grammar produces is
// meant to be codegen-suitable under every configuration in the matrix.
func coRunCase(cc *coConfig, doc *coDoc, muts []coMutation) (coResult, error) {
	var res coResult

	schemaJSON, err := json.Marshal(doc.schema())
	if err != nil {
		return res, fmt.Errorf("marshal schema: %w", err)
	}
	instance := doc.instance()
	instanceJSON, err := json.Marshal(instance)
	if err != nil {
		return res, fmt.Errorf("marshal instance: %w", err)
	}

	mutantJSON := make([]json.RawMessage, 0, len(muts))
	for _, m := range muts {
		mutated, err := m.apply(instance)
		if err != nil {
			return res, fmt.Errorf("apply mutation %s: %w", m.key(), err)
		}
		raw, err := json.Marshal(mutated)
		if err != nil {
			return res, fmt.Errorf("marshal mutant %s: %w", m.key(), err)
		}
		mutantJSON = append(mutantJSON, raw)
	}

	code, err := coGenerate(schemaJSON, cc)
	if err != nil {
		return res, err
	}
	if cc != coBaseConfig {
		base, baseErr := coGenerate(schemaJSON, coBaseConfig)
		res.distinct = baseErr != nil || base != code
	}
	// A schema needing the runtime validation package imports it, and the
	// throwaway module has to be told where that lives. This became reachable
	// the moment the grammar grew unevaluatedProperties: analyzeValidationCapability
	// sets RequiresRuntime for it, so under hybrid and runtime the emitted code
	// imports pkg/validationruntime -- which is exactly when hybrid and runtime
	// stop being byte-identical to static and start earning their place in the
	// matrix. writeCogenGoMod resolves the import with a replace onto this
	// checkout; needsRuntimePkg decides whether to bother.
	res.needsRuntime = strings.Contains(code, "pkg/validationruntime")
	rootType := extractRootTypeNameFromCode(code)
	if rootType == "" {
		return res, fmt.Errorf("no root type in generated code")
	}

	dir, err := os.MkdirTemp("", "schemagen-cogen-*")
	if err != nil {
		return res, fmt.Errorf("tmpdir: %w", err)
	}
	defer os.RemoveAll(dir)

	content := strings.Replace(code, "package "+coPackageName, "package main", 1)
	if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(content), 0o644); err != nil {
		return res, fmt.Errorf("write types: %w", err)
	}
	if err := writeSharedHelpersErr(dir, content); err != nil {
		return res, fmt.Errorf("write helpers: %w", err)
	}
	main := coMainProgram(rootType, hasValidateMethod(code))
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644); err != nil {
		return res, fmt.Errorf("write main: %w", err)
	}
	cases, err := json.Marshal(map[string]any{"instance": json.RawMessage(instanceJSON), "mutants": mutantJSON})
	if err != nil {
		return res, fmt.Errorf("marshal cases: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cases.json"), cases, 0o644); err != nil {
		return res, fmt.Errorf("write cases: %w", err)
	}
	if err := writeCogenGoMod(dir, res.needsRuntime); err != nil {
		return res, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), coRunTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = dir
	// The ephemeral GOCACHE is not optional here: a few hundred iterations, each
	// compiling a package that will never be seen again, would otherwise add
	// gigabytes to the user's persistent build cache.
	cmd.Env = ephemeralCacheEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return res, fmt.Errorf("compile/run: %w\n%s", err, string(out))
	}

	res.mutants = make([]coVerdict, len(muts))
	for i := range res.mutants {
		res.mutants[i] = coVerdict{outcome: "MISSING"}
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "go: "):
			continue
		case line == "RT OK":
			res.rtOK = true
		case strings.HasPrefix(line, "RT FAIL "):
			res.rtMsg = strings.TrimPrefix(line, "RT FAIL ")
		case line == "POS OK":
			res.posOK = true
		case strings.HasPrefix(line, "POS FAIL "):
			res.posMsg = strings.TrimPrefix(line, "POS FAIL ")
		case strings.HasPrefix(line, "MUT "):
			fields := strings.SplitN(strings.TrimPrefix(line, "MUT "), " ", 3)
			if len(fields) < 2 {
				return res, fmt.Errorf("unparseable verdict %q", line)
			}
			idx, convErr := strconv.Atoi(fields[0])
			if convErr != nil || idx < 0 || idx >= len(res.mutants) {
				return res, fmt.Errorf("verdict for unknown mutant: %q", line)
			}
			v := coVerdict{outcome: fields[1]}
			if len(fields) == 3 {
				v.msg = fields[2]
			}
			res.mutants[idx] = v
		default:
			return res, fmt.Errorf("unexpected program output %q", line)
		}
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// Judging one case
// ---------------------------------------------------------------------------

// coCheck runs a document under one configuration and turns the report into a
// list of failures. The raw result comes back too so a caller that also
// cross-checks against reference implementations does not have to compile the
// case twice.
func coCheck(cc *coConfig, doc *coDoc) (coResult, []coFailure) {
	muts := coMutationsFor(cc, doc)
	res, err := coRunCase(cc, doc, muts)
	if err != nil {
		return res, []coFailure{{kind: "generate", detail: err.Error()}}
	}

	var out []coFailure
	if !res.rtOK {
		out = append(out, coFailure{kind: "roundtrip", detail: res.rtMsg})
	}
	if !res.posOK {
		// A conforming instance must be accepted. When this fires the schema
		// and the instance disagree, which is either a generator defect or a
		// grammar defect -- both worth stopping for.
		out = append(out, coFailure{kind: "positive", detail: res.posMsg})
	}

	for i, m := range muts {
		v := res.mutants[i]
		switch {
		case v.outcome == "MISSING":
			out = append(out, coFailure{kind: "mutant-missing", mutKey: m.key(),
				detail: "the generated program reported no verdict"})

		case v.outcome == "ACCEPTED":
			out = append(out, coFailure{kind: "mutant-accepted", mutKey: m.key(),
				detail: fmt.Sprintf("violating %s was accepted", m.Keyword)})

		case v.outcome == "UNMARSHAL":
			// Wrong-type mutants are expected to die in the decoder. Anything
			// else reaching the decoder means the constraint is being enforced
			// by the Go type rather than by Validate, which is worth knowing
			// because Validate is what callers of hand-built values run.
			if !m.Loose {
				out = append(out, coFailure{kind: "mutant-unmarshal", mutKey: m.key(),
					detail: fmt.Sprintf("violating %s was rejected by UnmarshalJSON, not Validate: %s", m.Keyword, v.msg)})
			}

		case v.outcome == "INVALID":
			// Assert the violated constraint is actually named. A rejection
			// with an unrelated message usually means a different check fired,
			// so the keyword under test is still unproven.
			var missing []string
			for _, want := range m.Want {
				if !strings.Contains(v.msg, want) {
					missing = append(missing, want)
				}
			}
			if len(missing) > 0 {
				out = append(out, coFailure{kind: "mutant-message", mutKey: m.key(),
					detail: fmt.Sprintf("rejecting %s reported %q, missing %q", m.Keyword, v.msg, missing)})
			}

		default:
			out = append(out, coFailure{kind: "mutant-missing", mutKey: m.key(),
				detail: "unknown outcome " + v.outcome})
		}
	}
	return res, out
}

// ---------------------------------------------------------------------------
// Shrinking
// ---------------------------------------------------------------------------

// coFingerprint identifies a document by what it actually produces, so two
// documents that differ only in a decision neither the schema nor the instance
// records can be told apart from two that really differ.
func coFingerprint(d *coDoc) string {
	s, err := json.Marshal(d.schema())
	if err != nil {
		return "unmarshalable-schema"
	}
	i, err := json.Marshal(d.instance())
	if err != nil {
		return "unmarshalable-instance"
	}
	return string(s) + "\x00" + string(i)
}

// coShrink delta-debugs a failing document: it repeatedly tries the reductions
// the grammar offers and keeps any that still reproduces the *same* defect.
// Budget bounds the number of compile-and-run cycles, since each is the cost
// of a whole iteration.
//
// Every candidate is re-adjusted for the configuration first, so the shrinker
// cannot walk out of the space the configuration can be held to and then report
// the resulting artefact as the defect. Re-adjusting can also leave a candidate
// identical to the document it came from -- coReduce offers "make this optional
// property absent", which coForcePresent immediately undoes -- and such a
// candidate would reproduce the failure, be taken as progress, and be offered
// again for as long as the budget lasted. Comparing fingerprints drops it
// instead.
func coShrink(cc *coConfig, doc *coDoc, target coFailure, budget int) (*coDoc, int) {
	current := doc
	spent := 0
	for spent < budget {
		progressed := false
		currentFP := coFingerprint(current)
		for _, candidate := range coReduce(current) {
			if spent >= budget {
				break
			}
			if cc.adjust != nil {
				cc.adjust(candidate)
			}
			if coFingerprint(candidate) == currentFP {
				continue
			}
			spent++
			reproduced := false
			_, failures := coCheck(cc, candidate)
			for _, f := range failures {
				if target.sameDefect(f) {
					reproduced = true
					break
				}
			}
			if reproduced {
				current = candidate
				progressed = true
				break
			}
		}
		if !progressed {
			break
		}
	}
	return current, spent
}

// coReport renders a document, its instance and the mutant a failure names, so
// the case can be replayed by hand without re-running the harness.
func coReport(t *testing.T, cc *coConfig, doc *coDoc, target coFailure) {
	t.Helper()
	schemaJSON, err := json.MarshalIndent(doc.schema(), "", "  ")
	if err != nil {
		t.Logf("  (schema could not be marshalled: %v)", err)
		return
	}
	instance := doc.instance()
	instanceJSON, _ := json.Marshal(instance)
	t.Logf("  config:   %s", cc.name)
	t.Logf("  schema:   %s", schemaJSON)
	t.Logf("  instance: %s", instanceJSON)
	if target.mutKey == "" {
		return
	}
	for _, m := range coMutationsFor(cc, doc) {
		if m.key() != target.mutKey {
			continue
		}
		mutated, err := m.apply(instance)
		if err != nil {
			t.Logf("  mutant %s: could not be applied: %v", target.mutKey, err)
			return
		}
		raw, _ := json.Marshal(mutated)
		t.Logf("  mutant %s: %s", target.mutKey, raw)
	}
}

// ---------------------------------------------------------------------------
// Bowtie cross-check
// ---------------------------------------------------------------------------

// coBowtieOutcome is one implementation's consensus verdict on one instance.
type coBowtieOutcome int

const (
	coBowtieUnknown coBowtieOutcome = iota
	coBowtieValid
	coBowtieInvalid
)

// coBowtieVerdicts asks independent JSON Schema implementations, driven by
// Bowtie in containers, whether each instance satisfies the schema. Running
// more than one is the point: a single library's opinion is not evidence, and
// where they disagree the answer is "unknown" rather than whichever one is
// listed first.
//
// The invocation and the JSONL shape mirror scripts/validate-seeds.py: one
// case, one line per implementation, results in instance order.
func coBowtieVerdicts(dir string, schemaJSON []byte, instances []json.RawMessage, impls []string) ([]coBowtieOutcome, error) {
	schemaPath := filepath.Join(dir, "bowtie_schema.json")
	if err := os.WriteFile(schemaPath, schemaJSON, 0o644); err != nil {
		return nil, err
	}
	args := []string{"--from", "bowtie-json-schema", "bowtie", "validate"}
	for _, impl := range impls {
		args = append(args, "-i", impl)
	}
	args = append(args, schemaPath)
	for i, inst := range instances {
		p := filepath.Join(dir, fmt.Sprintf("bowtie_inst_%03d.json", i))
		if err := os.WriteFile(p, inst, 0o644); err != nil {
			return nil, err
		}
		args = append(args, p)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "uvx", args...)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("bowtie: %w", err)
	}

	// index -> impl -> valid
	per := make([]map[string]bool, len(instances))
	for i := range per {
		per[i] = map[string]bool{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry struct {
			Implementation string `json:"implementation"`
			Results        []struct {
				Valid *bool `json:"valid"`
			} `json:"results"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil || entry.Implementation == "" {
			continue
		}
		for i, r := range entry.Results {
			if i < len(per) && r.Valid != nil {
				per[i][entry.Implementation] = *r.Valid
			}
		}
	}

	outcomes := make([]coBowtieOutcome, len(instances))
	for i, votes := range per {
		switch {
		case len(votes) == 0:
			outcomes[i] = coBowtieUnknown
		default:
			first := true
			agreed := true
			var value bool
			for _, v := range votes {
				if first {
					value, first = v, false
					continue
				}
				if v != value {
					agreed = false
				}
			}
			switch {
			case !agreed:
				outcomes[i] = coBowtieUnknown
			case value:
				outcomes[i] = coBowtieValid
			default:
				outcomes[i] = coBowtieInvalid
			}
		}
	}
	return outcomes, nil
}

// coBowtieTally accumulates cross-check counts across iterations.
type coBowtieTally struct {
	mu         sync.Mutex
	cases      int
	agree      int
	disagree   int
	unknown    int
	errors     int
	complaints []string
}

func (b *coBowtieTally) note(s string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.complaints) < 20 {
		b.complaints = append(b.complaints, s)
	}
}

// coCrossCheck compares an independent verdict on every instance of one
// iteration against what the generated bindings decided. res is the report
// coCheck already collected, so the case is compiled once per iteration
// whether or not the cross-check is on.
func coCrossCheck(tally *coBowtieTally, cc *coConfig, iter int, doc *coDoc, res coResult, impls []string) {
	muts := coMutationsFor(cc, doc)
	if len(res.mutants) != len(muts) {
		tally.mu.Lock()
		tally.errors++
		tally.mu.Unlock()
		tally.note(fmt.Sprintf("iter %d: the generated program produced no usable verdicts", iter))
		return
	}
	schemaJSON, err := json.Marshal(doc.schema())
	if err != nil {
		return
	}
	instance := doc.instance()
	instanceJSON, err := json.Marshal(instance)
	if err != nil {
		return
	}
	instances := []json.RawMessage{instanceJSON}
	for _, m := range muts {
		mutated, err := m.apply(instance)
		if err != nil {
			return
		}
		raw, err := json.Marshal(mutated)
		if err != nil {
			return
		}
		instances = append(instances, raw)
	}

	dir, err := os.MkdirTemp("", "schemagen-cogen-bowtie-*")
	if err != nil {
		return
	}
	defer os.RemoveAll(dir)

	outcomes, err := coBowtieVerdicts(dir, schemaJSON, instances, impls)
	if err != nil {
		tally.mu.Lock()
		tally.errors++
		tally.mu.Unlock()
		tally.note(fmt.Sprintf("iter %d: bowtie: %v", iter, err))
		return
	}

	record := func(label string, reference coBowtieOutcome, ours bool, oursNote string) {
		tally.mu.Lock()
		tally.cases++
		switch {
		case reference == coBowtieUnknown:
			tally.unknown++
		case (reference == coBowtieValid) == ours:
			tally.agree++
		default:
			tally.disagree++
			tally.mu.Unlock()
			tally.note(fmt.Sprintf("iter %d %s: reference says %s, schemagen says %s (%s)",
				iter, label, coBowtieName(reference), coBowtieName(coBowtieOf(ours)), oursNote))
			return
		}
		tally.mu.Unlock()
	}

	// Mutants past the schema-derived ones were added by the configuration, and
	// a reference implementation reading only the schema cannot be expected to
	// agree about them: the whole point of the StrictProperties mutant is that
	// it violates a rule the schema text does not state. Cross-checking it would
	// manufacture a disagreement on every iteration.
	judged := len(doc.mutations())

	record("instance", outcomes[0], res.posOK, res.posMsg)
	for i, m := range muts {
		if i >= judged {
			break
		}
		v := res.mutants[i]
		record("mutant "+m.key(), outcomes[i+1], v.outcome == "ACCEPTED", v.outcome+" "+v.msg)
	}
}

func coBowtieOf(valid bool) coBowtieOutcome {
	if valid {
		return coBowtieValid
	}
	return coBowtieInvalid
}

func coBowtieName(o coBowtieOutcome) string {
	switch o {
	case coBowtieValid:
		return "valid"
	case coBowtieInvalid:
		return "invalid"
	default:
		return "unknown"
	}
}

// ---------------------------------------------------------------------------
// Driver
// ---------------------------------------------------------------------------

// coIterSeed mixes a base seed with an iteration index (SplitMix64). Deriving
// each iteration from an explicit seed, rather than from a shared stream, is
// what lets a single failing iteration be replayed on its own: the schema
// depends on the pair and on nothing else, including how many workers ran.
func coIterSeed(base uint64, iter int) uint64 {
	x := base + uint64(iter+1)*0x9E3779B97F4A7C15
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return x
}

func coEnvInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func coEnvString(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func coEnvUint(name string, def uint64) uint64 {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.ParseUint(v, 0, 64)
	if err != nil {
		return def
	}
	return n
}

// TestCoGenerated is the harness proper. Every iteration is an independent
// (schema, instance, generator configuration) triple built from its own derived
// seed, so a failure is reported with everything needed to replay it alone:
//
//	SCHEMAGEN_RUN_COGEN=1 SCHEMAGEN_COGEN_SEED=<seed> \
//	SCHEMAGEN_COGEN_ITER0=<iter> SCHEMAGEN_COGEN_ITERS=1 \
//	go test ./tests/... -run TestCoGenerated -v
//
// The configuration follows from (seed, iter) too, so that line replays it
// without being told which one it was. SCHEMAGEN_COGEN_CONFIG=<name> pins every
// iteration to one configuration instead, which is what to reach for when a
// defect has been narrowed to a flag and the question is how far it spreads.
func TestCoGenerated(t *testing.T) {
	if os.Getenv(coEnvRun) != "1" {
		t.Skipf("co-generation harness disabled; set %s=1 or run make cogen", coEnvRun)
	}

	seed := coEnvUint("SCHEMAGEN_COGEN_SEED", 1)
	iter0 := coEnvInt("SCHEMAGEN_COGEN_ITER0", 0)
	iters := coEnvInt("SCHEMAGEN_COGEN_ITERS", 100)
	par := coEnvInt("SCHEMAGEN_COGEN_PAR", runtime.NumCPU())
	maxFail := coEnvInt("SCHEMAGEN_COGEN_MAXFAIL", 5)
	shrinkBudget := coEnvInt("SCHEMAGEN_COGEN_SHRINK", 250)
	if par < 1 {
		par = 1
	}

	var pinned *coConfig
	if name := os.Getenv("SCHEMAGEN_COGEN_CONFIG"); name != "" {
		pinned = coConfigNamed(name)
		if pinned == nil {
			t.Fatalf("SCHEMAGEN_COGEN_CONFIG=%q is not a known configuration; have %s",
				name, strings.Join(coConfigNames(), ", "))
		}
	}
	configFor := func(iter int) *coConfig {
		if pinned != nil {
			return pinned
		}
		return coConfigFor(seed, iter)
	}

	bowtie := os.Getenv("SCHEMAGEN_COGEN_BOWTIE") == "1"
	impls := strings.Split(coEnvString("SCHEMAGEN_COGEN_BOWTIE_IMPLS", "python-jsonschema,js-ajv"), ",")
	bowtieMax := coEnvInt("SCHEMAGEN_COGEN_BOWTIE_MAX", 20)

	configNote := "dealt over " + strings.Join(coConfigNames(), ",")
	if pinned != nil {
		configNote = "pinned to " + pinned.name
	}
	t.Logf("seed=%d iterations=%d (first=%d) parallelism=%d bowtie=%v configs=%s",
		seed, iters, iter0, par, bowtie, configNote)

	type failedIter struct {
		iter     int
		config   string
		failures []coFailure
	}

	// coStat is one configuration's share of the run. distinct counts the
	// iterations whose emitted source differed from what the baseline
	// configuration emits for the same document, which is the run's own answer
	// to "is this configuration exercising anything".
	type coStat struct {
		iters    int
		failed   int
		mutants  int
		distinct int
	}

	var (
		mu       sync.Mutex
		failed   []failedIter
		mutTotal int
		abort    bool
		stats    = map[string]*coStat{}
	)
	for _, cc := range coConfigs {
		stats[cc.name] = &coStat{}
	}
	tally := &coBowtieTally{}

	start := time.Now()
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < par; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iter := range jobs {
				cc := configFor(iter)
				doc := coDocFor(cc, coIterSeed(seed, iter))
				res, failures := coCheck(cc, doc)
				count := len(coMutationsFor(cc, doc))

				mu.Lock()
				mutTotal += count
				st := stats[cc.name]
				st.iters++
				st.mutants += count
				if res.distinct {
					st.distinct++
				}
				if len(failures) > 0 {
					st.failed++
					failed = append(failed, failedIter{iter: iter, config: cc.name, failures: failures})
					if len(failed) >= maxFail {
						abort = true
					}
				}
				mu.Unlock()

				if bowtie && iter-iter0 < bowtieMax {
					coCrossCheck(tally, cc, iter, doc, res, impls)
				}
			}
		}()
	}

	scheduled := 0
	for i := iter0; i < iter0+iters; i++ {
		mu.Lock()
		stop := abort
		mu.Unlock()
		if stop {
			break
		}
		jobs <- i
		scheduled++
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(start)

	rate := float64(scheduled) / elapsed.Seconds()
	t.Logf("ran %d iterations in %s (%.2f iterations/sec), %d mutants exercised, %d iterations failed",
		scheduled, elapsed.Round(time.Millisecond), rate, mutTotal, len(failed))

	// Per-configuration counts. "distinct source" is the number of iterations
	// whose emitted Go differed from what the baseline configuration emits for
	// the same document; a configuration sitting at zero is running the same
	// code the baseline already ran, and is worth only as much as a regression
	// guard on the flag's plumbing.
	t.Logf("per-configuration:")
	for _, cc := range coConfigs {
		st := stats[cc.name]
		if st.iters == 0 {
			continue
		}
		note := ""
		if cc != coBaseConfig {
			note = fmt.Sprintf(", %d with source distinct from %s", st.distinct, coBaseConfig.name)
		}
		t.Logf("  %-12s %4d iterations, %6d mutants%s, %d failed",
			cc.name, st.iters, st.mutants, note, st.failed)
	}

	if bowtie {
		tally.mu.Lock()
		t.Logf("bowtie cross-check via %s: %d instances judged, %d agree, %d disagree, %d unknown, %d errors",
			strings.Join(impls, ", "), tally.cases, tally.agree, tally.disagree, tally.unknown, tally.errors)
		complaints := append([]string(nil), tally.complaints...)
		disagree := tally.disagree
		tally.mu.Unlock()
		for _, c := range complaints {
			t.Logf("  %s", c)
		}
		if disagree > 0 {
			t.Errorf("%d reference disagreements; see the log above", disagree)
		}
	}

	if len(failed) == 0 {
		return
	}

	sort.Slice(failed, func(i, j int) bool { return failed[i].iter < failed[j].iter })
	for _, f := range failed {
		t.Errorf("seed=%d iter=%d config=%s: %d failure(s)", seed, f.iter, f.config, len(f.failures))
		for _, fail := range f.failures {
			t.Logf("  %s", fail)
		}
		t.Logf("  replay: SCHEMAGEN_RUN_COGEN=1 SCHEMAGEN_COGEN_SEED=%d SCHEMAGEN_COGEN_ITER0=%d SCHEMAGEN_COGEN_ITERS=1 SCHEMAGEN_COGEN_CONFIG=%s go test ./tests/... -run TestCoGenerated -v",
			seed, f.iter, f.config)
	}

	// Shrink only the first failure. When something systemic breaks, every
	// iteration fails for the same reason and shrinking each one would cost a
	// full compile per reduction with nothing new to show.
	first := failed[0]
	target := first.failures[0]
	cc := configFor(first.iter)
	doc := coDocFor(cc, coIterSeed(seed, first.iter))
	t.Logf("shrinking iter=%d config=%s against %s (budget %d cycles)", first.iter, cc.name, target.kind, shrinkBudget)
	minimal, spent := coShrink(cc, doc, target, shrinkBudget)
	t.Logf("shrunk in %d cycles; minimal case for %s:", spent, target)
	coReport(t, cc, minimal, target)
}

// writeCogenGoMod writes the throwaway module's go.mod. It is writeTestGoMod
// plus one thing: when the emitted code imports pkg/validationruntime -- which
// hybrid and runtime do as soon as the schema uses a keyword requiring runtime
// evaluation, unevaluatedProperties being the first the grammar reaches -- the
// import has to resolve to something.
//
// It resolves to a stub module rather than to this checkout. Replacing onto the
// repository would drag schemagen's own go.mod into the generated module's
// graph, so the throwaway module would need go.sum entries for cobra and
// everything else, and `go run` would refuse until they were written. The
// package being vendored is 41 lines and imports nothing but fmt, so a stub
// carrying just that file, declaring the same module path and requiring
// nothing, is both smaller and hermetic.
func writeCogenGoMod(dir string, needsRuntime bool) error {
	if !needsRuntime {
		return writeTestGoMod(dir, "cogen_test")
	}

	src, err := os.ReadFile(filepath.Join("..", "pkg", "validationruntime", "runtime.go"))
	if err != nil {
		return fmt.Errorf("read validationruntime source: %w", err)
	}
	stub := filepath.Join(dir, "schemagenstub")
	pkgDir := filepath.Join(stub, "pkg", "validationruntime")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return fmt.Errorf("stub dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "runtime.go"), src, 0o644); err != nil {
		return fmt.Errorf("write stub package: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stub, "go.mod"),
		[]byte("module github.com/mgilbir/schemagen\n\ngo 1.23\n"), 0o644); err != nil {
		return fmt.Errorf("write stub go.mod: %w", err)
	}

	goMod := fmt.Sprintf("module cogen_test\n\ngo 1.23\n\nrequire (\n\tgithub.com/mgilbir/goecma262 %s\n\tgithub.com/mgilbir/schemagen v0.0.0\n)\n\nreplace github.com/mgilbir/schemagen => ./schemagenstub\n",
		goecma262Version)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return fmt.Errorf("write go.mod: %w", err)
	}
	goSum := fmt.Sprintf("github.com/mgilbir/goecma262 %s %s\ngithub.com/mgilbir/goecma262 %s/go.mod %s\n",
		goecma262Version, goecma262H1, goecma262Version, goecma262GoMod)
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(goSum), 0o644); err != nil {
		return fmt.Errorf("write go.sum: %w", err)
	}
	return nil
}
