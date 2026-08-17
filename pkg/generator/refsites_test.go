package generator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The sites that read a reference off a schema, and the one question every one
// of them has to answer.
//
// Three keywords put a reference on a schema object: $ref, $recursiveRef and
// $dynamicRef. Schema.EffectiveRef answers for two of them and leaves the third
// out on purpose, so a function written
//
//	if ref := s.EffectiveRef(); ref != "" { ... }
//
// cannot see a $dynamicRef at all. Where such a function's business is to reach
// the sub-schema behind the reference, that is not a missing feature but a
// keyword going unread -- and a schema whose whole content is behind the
// reference then asserts nothing. {"contains":{"$dynamicRef":"#it"}} counted
// every element of the array as matching, so the keyword said only that the
// array was non-empty, while the same document written {"$ref":"#/$defs/item"}
// got the real per-element check. That is issue #337.
//
// So the sites are enumerated from the source rather than from memory, and every
// one is classified here. It is the same shape as #313's
// TestEveryTypeNameEmissionSiteIsAccountedFor, one question over: there the
// enumeration is total over the *positions* that write a type name, here it is
// total over the functions that read a reference.
//
// #339 is why the table pins the keyword set as well as the verdict. #313's gate
// was total over positions and blind over reference kinds -- its probe set held
// no dynamic reference at all -- so a site could be classified, and stay
// classified, while quietly answering for two keywords out of three. Reads below
// is the exact set of reference keywords the function names, checked against the
// source, so a function that starts or stops reading one has to be reclassified
// rather than drifting.
//
// The behavioural half is tests/reference_kind_agreement_test.go, which writes
// the same position three ways -- $ref, $recursiveRef and $dynamicRef, chosen so
// that the three documents mean the same thing -- and requires the same verdicts
// through generate-compile-run. A table saying a site is total is a claim; that
// test is what makes it evidence.

type refVerdict int

const (
	// refReadsWhicheverIsThere: the function asks "the reference on this node",
	// and gets the same answer whichever keyword spells it -- because it names
	// all three, or because it goes through referenceTarget/referenceOn, which
	// do.
	refReadsWhicheverIsThere refVerdict = iota
	// refAsksAboutOneKeyword: the function's subject is a particular keyword's
	// own rule, so reading the others would be answering a different question.
	// A reason is required and says which rule.
	refAsksAboutOneKeyword
	// refNotASchemaReference: the selector is a field called Ref on something
	// that is not a schema.
	refNotASchemaReference
)

type refReadingSite struct {
	Verdict refVerdict
	// Reads is the set of reference keywords the function names, sorted and
	// comma-joined exactly as the scan reports it. "EffectiveRef()" stands for a
	// call of that method, which is $ref-or-$recursiveRef and never $dynamicRef.
	Reads string
	Why   string
}

// refReadingSites is keyed by "<package-relative file> | <function>".
var refReadingSites = map[string]refReadingSite{
	// ---------------------------------------------------------------- the funnel
	"generator/references.go | (*Generator).referenceTarget": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,EffectiveRef()",
		Why: "the funnel itself: it resolves whichever keyword is there by the rule that keyword states",
	},
	"generator/references.go | (*Generator).referenceTargetUncounted": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,EffectiveRef()",
		Why: "referenceTarget for a walk that only looks; same totality, no miss recorded",
	},
	"generator/references.go | referenceOn": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,EffectiveRef()",
		Why: "the presence half of the funnel, for the sites that ask only whether a reference is there",
	},

	// ------------------------------------------------ reads whichever is there
	"generator/dynamic.go | schemaCarriesRef": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,EffectiveRef()",
		Why: "a branch carrying any reference is left alone whole rather than read in part",
	},
	"generator/dynamicscope.go | (*Generator).dynamicReach": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$recursiveRef,$ref",
		Why: "the reach an evaluation could arrive at follows every reference, however spelled",
	},
	"generator/dynamicscope.go | (*Generator).inPlaceSuccessors": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$recursiveRef,$ref",
		Why: "every reference is an edge a value travels without getting smaller",
	},
	"generator/annotations.go | (*nodeBuilder).literal": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$recursiveRef,$ref",
		Why: "the runtime node builder compiles all three: a $ref as a conjunct, and the dynamic pair as a " +
			"conjunct too where the document settles their target and as a scope-resolved frame where it does not",
	},
	"generator/generator.go | refKeywordOf": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$recursiveRef,$ref",
		Why: "it exists to say which of the three a reference string was written as",
	},
	"generator/generator.go | isAcceptAllSchema": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$recursiveRef,$ref",
		Why: "a schema carrying any reference is not one that accepts every value",
	},
	"generator/generator.go | hasUnrepresentedConstraints": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$recursiveRef,$ref",
		Why: "any reference is a constraint the flat reading cannot represent",
	},
	"generator/generator.go | hasNonTypeScopedConstraints": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$recursiveRef,$ref",
		Why: "any reference states something that is not scoped to a declared type",
	},
	"generator/generator.go | oneOfUnionKeepsWholeSchema": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$recursiveRef,$ref",
		Why: "a sibling reference is something the union would drop, whichever keyword writes it",
	},
	"generator/generator.go | (*Generator).allOfNeedsNamedType": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$recursiveRef,EffectiveRef()",
		Why: "a merge target carrying any reference disqualifies the flat reading",
	},
	"generator/generator.go | (*Generator).typeIsInferredFromConstraints": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$recursiveRef,EffectiveRef()",
		Why: "a schema whose type comes from a reference is not one inferred from its own constraints",
	},
	"generator/generator.go | (*Generator).extractNotSchemaDef": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$recursiveRef,$ref,EffectiveRef()",
		Why: "the EffectiveRef read is the pre-2019-09 sibling rule; the three-keyword read beside it is what " +
			"decides that the negated schema carries a reference at all",
	},
	"generator/generator.go | (*Generator).collectEvaluatedItems": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$recursiveRef,$ref",
		Why: "what a reference evaluates counts however it is spelled",
	},
	"generator/generator.go | (*Generator).countEvaluatedItemsOnPath": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$recursiveRef,$ref",
		Why: "as collectEvaluatedItems",
	},
	"generator/generator.go | (*Generator).unevaluatedItemsImpliesFixedTuple": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$recursiveRef,$ref",
		Why: "a reference under the tuple can add positions, whichever keyword writes it",
	},
	"generator/generator.go | (*Generator).collectEvaluatedProperties": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,EffectiveRef()",
		Why: "the node's own reference is followed in two arms, one per rule; the branches under it go " +
			"through referenceTarget",
	},
	"generator/generator.go | (*Generator).collectEvaluatedFromNestedOnPath": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,EffectiveRef()",
		Why: "as collectEvaluatedProperties",
	},
	"generator/generator.go | (*Generator).collectEvaluatedFromNestedExcludeConditional": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,EffectiveRef()",
		Why: "as collectEvaluatedProperties",
	},
	"generator/generator.go | (*Generator).buildBranchUnevalCheck": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,EffectiveRef()",
		Why: "as collectEvaluatedProperties",
	},
	"generator/generator.go | (*Generator).resolvedToFalseSchema": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,EffectiveRef()",
		Why: "a reference to the false schema forbids every value whichever keyword reaches it; both arms are here",
	},
	"generator/generator.go | (*Generator).bigIntInlineWrapper": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,EffectiveRef()",
		Why: "a schema that references is not the bare integer this wrapper is for",
	},
	"generator/generator.go | (*Generator).inlineConstraintWrapper": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,EffectiveRef()",
		Why: "as bigIntInlineWrapper",
	},
	"generator/generator.go | (*Generator).rawValueTypeName": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,EffectiveRef()",
		Why: "a patternProperties bucket reaches its type through either spelling; both arms are here",
	},
	"generator/generator.go | (*Generator).firstAllOfArrayAliasName": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,EffectiveRef()",
		Why: "the array alias an allOf branch names is found through either spelling; both arms are here",
	},
	"generator/generator.go | (*Generator).tupleItemCheckFor": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,EffectiveRef()",
		Why: "the sibling arm names both keywords, and a bare dynamic reference falls through to " +
			"constraintOnlyNamedType, which materializes it through generateTypeDefBody's own dynamic arm",
	},
	"generator/generator.go | (*Generator).resolveType": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,EffectiveRef()",
		Why: "the type ladder has one arm per rule, because the two resolve differently and name their target " +
			"differently; every other position that types a sub-schema ends here",
	},
	"generator/generator.go | (*Generator).generateTypeDefBody": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$ref,EffectiveRef()",
		Why: "as resolveType, for the declaration rather than the use",
	},
	"cmd/schemagen/pkgorder.go | collectRefSites": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$recursiveRef,$ref",
		Why: "package ordering follows every reference out of a document, whichever keyword writes it",
	},
	"cmd/schemagen/sharedefs.go | definitionCanonicalForm": {
		Verdict: refReadsWhicheverIsThere, Reads: "$dynamicRef,$recursiveRef,$ref",
		Why: "two definitions are the same definition only if every reference under them is",
	},

	// ------------------------------------------------ asks about one keyword
	"schema/schema.go | (*Schema).EffectiveRef": {
		Verdict: refAsksAboutOneKeyword, Reads: "$recursiveRef,$ref",
		Why: "the accessor whose contract is 'the reference that resolves like a $ref'. $dynamicRef is excluded " +
			"because it does not: its target is chosen from the resources an evaluation entered. Widening it " +
			"would silently hand every caller a reference resolved by the wrong rule",
	},
	"schema/schema.go | (*Schema).UnmarshalJSON": {
		Verdict: refAsksAboutOneKeyword, Reads: "$ref",
		Why: `rewrites the empty "$ref": "" to "#". No draft gives $recursiveRef or $dynamicRef that spelling`,
	},
	"generator/generator.go | (*Generator).normalizeDialectRefKeywords": {
		Verdict: refAsksAboutOneKeyword, Reads: "$dynamicRef,$recursiveRef",
		Why: "clears a reference keyword the node's own dialect does not define. $ref is defined in every draft, " +
			"so there is nothing to clear",
	},
	"generator/dynamicscope.go | (*Generator).dynamicRefTarget": {
		Verdict: refAsksAboutOneKeyword, Reads: "$dynamicRef,$recursiveRef",
		Why: "says where a dynamic reference points and whether the scope is consulted at all; a $ref is not a " +
			"question it is asked",
	},
	"generator/dynamicscope.go | (*Generator).dynamicScopeDecidesTheTarget": {
		Verdict: refAsksAboutOneKeyword, Reads: "$dynamicRef,$recursiveRef",
		Why: "a $ref has one target by construction, so only the dynamic pair can put the answer in the " +
			"document's hands",
	},
	"generator/annotations.go | (*Generator).dynamicScopeRefusal": {
		Verdict: refAsksAboutOneKeyword, Reads: "$dynamicRef,$recursiveRef",
		Why: "the refusal message for a dynamic reference nothing can settle; a $ref never reaches it",
	},
	"generator/validation.go | collectValidationFeatures": {
		Verdict: refAsksAboutOneKeyword, Reads: "$dynamicRef,$recursiveRef",
		Why: "records the features the generated code needs a runtime evaluator for. A $ref needs none",
	},
	"generator/annotations.go | (*nodeBuilder).resolve": {
		Verdict: refAsksAboutOneKeyword, Reads: "$ref",
		Why: "the $ref half of the builder's reference handling; literal above it takes the dynamic pair " +
			"through dynamicRefTarget, which resolves them by their own rule",
	},
	"generator/annotations.go | (*Generator).acceptsEveryValue": {
		Verdict: refAsksAboutOneKeyword, Reads: "$ref",
		Why: "the allow-list above this read admits allOf/anyOf/oneOf/$ref and refuses everything else, so a " +
			"node stating $recursiveRef or $dynamicRef has already answered false. Following one here would mean " +
			"claiming a target the dynamic scope chooses, which is the claim this predicate must not make",
	},
	"generator/generator.go | (*Generator).resolveEffectiveRefSchema": {
		Verdict: refAsksAboutOneKeyword, Reads: "$recursiveRef,EffectiveRef()",
		Why: "resolves the $ref/$recursiveRef pair, the second against the dynamic scope. resolveDynamicRef is " +
			"the third keyword's own resolver and referenceTarget is what puts a caller in front of both",
	},
	"generator/generator.go | (*Generator).refDisplacesSiblingValues": {
		Verdict: refAsksAboutOneKeyword, Reads: "EffectiveRef()",
		Why: "the pre-2019-09 rule that a $ref replaces the schema object it sits in. $dynamicRef arrived in " +
			"2020-12, where nothing replaces its siblings -- and normalizeDialectRefKeywords clears it on the " +
			"drafts where the rule applies, so it cannot be there to read",
	},
	"generator/generator.go | (*Generator).refMergesSiblingValues": {
		Verdict: refAsksAboutOneKeyword, Reads: "$ref",
		Why: "refDisplacesSiblingValues on the other side of the same draft split",
	},
	"generator/generator.go | (*Generator).siblingsWouldDropNot": {
		Verdict: refAsksAboutOneKeyword, Reads: "EffectiveRef()",
		Why: "the same pre-2019-09 sibling rule as refDisplacesSiblingValues",
	},
	"generator/generator.go | (*Generator).generateStructDef": {
		Verdict: refAsksAboutOneKeyword, Reads: "EffectiveRef()",
		Why: "the same pre-2019-09 sibling rule: a property's own assertions are not read beside a $ref there",
	},
	"generator/generator.go | (*Generator).enumMembersDeclaredTypeAdmits": {
		Verdict: refAsksAboutOneKeyword, Reads: "$ref",
		Why: "the same pre-2019-09 sibling rule, for the enum and the type written beside a $ref",
	},
	"generator/generator.go | (*Generator).generateAllOfDef": {
		Verdict: refAsksAboutOneKeyword, Reads: "$ref",
		Why: "copies the parent's $ref onto the merged node so the unevaluated pair can follow it. A dynamic " +
			"reference cannot be copied that way -- its target is chosen from the resources entered on the way " +
			"to the node it was written on, and the merged node is a synthesized one that no evaluation enters " +
			"-- so a node carrying one keeps it where it was written and is read there. " +
			"TestReferenceKindsAgree/anchor_2020/unevaluated_properties_beside_allof is the case",
	},
	"generator/generator.go | oneOfBranchIsUnselectable": {
		Verdict: refAsksAboutOneKeyword, Reads: "EffectiveRef()",
		Why: "asks whether the sealed-interface union could tell this branch apart. A $ref branch it can, " +
			"because resolveOneOfVariant gives the target a named Go type; a $dynamicRef branch it cannot -- " +
			"that function falls through to `any` -- so calling such a branch unselectable is what declines the " +
			"union and sends the schema to the runtime evaluator, which resolves the reference per value. " +
			"Widening this to referenceOn was tried and measured: the union was then rendered over an `any` " +
			"variant and TestReferenceKindsAgree/anchor_2020/one_of_branch failed in both directions",
	},
	"generator/generator.go | (*Generator).oneOfVariantSelectionType": {
		Verdict: refAsksAboutOneKeyword, Reads: "EffectiveRef()",
		Why: "as oneOfBranchIsUnselectable: what selection can decide from, on the union path a dynamic " +
			"reference does not take",
	},
	"generator/generator.go | (*Generator).resolveOneOfVariant": {
		Verdict: refAsksAboutOneKeyword, Reads: "EffectiveRef()",
		Why: "the union's variant-typing arm, which oneOfBranchIsUnselectable keeps a dynamic reference away from",
	},
	"generator/generator.go | (*Generator).resolveVariantSchema": {
		Verdict: refAsksAboutOneKeyword, Reads: "EffectiveRef()",
		Why: "as resolveOneOfVariant, for the variant's schema rather than its type",
	},
	"generator/generator.go | variantMatchesMapping": {
		Verdict: refAsksAboutOneKeyword, Reads: "EffectiveRef()",
		Why: "matches a discriminator mapping's value against the variant it names. The mapping is written as a " +
			"$ref string, and the variants it can name are the ones the union renders -- which a dynamic " +
			"reference is not, per oneOfBranchIsUnselectable",
	},
	"generator/generator.go | (*Generator).resolvePropertyType": {
		Verdict: refAsksAboutOneKeyword, Reads: "EffectiveRef()",
		Why: "the $ref/$recursiveRef alias arm of the property ladder. This function ends in resolveType, whose " +
			"dynamic arm is where a $dynamicRef property is typed, so the two arms together are total and the " +
			"split keeps each target named by its own rule",
	},

	// ------------------------------------------- not a schema's reference
	"generator/generator.go | (crossPackageMiss).describe": {
		Verdict: refNotASchemaReference, Reads: "$ref",
		Why: "crossPackageMiss.Ref is the reference the miss was recorded against, already a string",
	},
	"schema/resolver.go | (*ResolveError).Error": {
		Verdict: refNotASchemaReference, Reads: "$ref",
		Why: "ResolveError.Ref is the reference string the error is about",
	},
	"cmd/schemagen/pkgorder.go | packageDependencies": {
		Verdict: refNotASchemaReference, Reads: "$ref",
		Why: "refSite.Ref, already collected from all three keywords by collectRefSites",
	},
	"cmd/schemagen/pkgorder.go | buildDocRefEdges": {
		Verdict: refNotASchemaReference, Reads: "$ref",
		Why: "as packageDependencies",
	},
	"cmd/schemagen/pkgorder.go | refTargetFile": {
		Verdict: refNotASchemaReference, Reads: "$ref",
		Why: "as packageDependencies",
	},
	"cmd/schemagen/pkgorder.go | cycleError": {
		Verdict: refNotASchemaReference, Reads: "$ref",
		Why: "packageEdge.Ref, the reference string printed in the message",
	},
	"cmd/schemagen/pkgorder.go | docRefCycleError": {
		Verdict: refNotASchemaReference, Reads: "$ref",
		Why: "docRefEdge.Ref, the reference string printed in the message",
	},
	"cmd/schemagen/pkgorder.go | docRefOrderError": {
		Verdict: refNotASchemaReference, Reads: "$ref",
		Why: "as docRefCycleError",
	},
	"cmd/schemagen/externaldefs.go | (*externalWalker).follow": {
		Verdict: refNotASchemaReference, Reads: "$ref",
		Why: "refSite.Ref, as packageDependencies",
	},
	"cmd/schemagen/root.go | warnUnresolvedRefs": {
		Verdict: refNotASchemaReference, Reads: "$ref",
		Why: "generator.UndeclaredRefType.Ref, the reference string the warning is about",
	},
}

// refScanDirs are the directories read, keyed by the label the table uses.
//
// All of them rather than pkg/generator alone: the question is where a reference
// is read, and cmd/schemagen reads them for package ordering and for the
// shared-definitions comparison, while pkg/schema owns EffectiveRef itself. A
// directory that contributes no site still has to be read, because "no sites
// here" is a finding and a directory that quietly stopped being scanned would
// look the same.
var refScanDirs = map[string]string{
	"generator":         ".",
	"schema":            "../schema",
	"emitter":           "../emitter",
	"validationruntime": "../validationruntime",
	"cmd/schemagen":     "../../cmd/schemagen",
	"main":              "../..",
}

// refKeywordSelectors are the field and method names that read a reference.
var refKeywordSelectors = map[string]string{
	"Ref":          "$ref",
	"RecursiveRef": "$recursiveRef",
	"DynamicRef":   "$dynamicRef",
	"EffectiveRef": "EffectiveRef()",
}

// scanRefReadingSites returns, per "<label>/<file> | <function>", the set of
// reference keywords that function names.
//
// Syntactic rather than type-checked, deliberately: the names above belong to
// schema.Schema and to a handful of small structs that carry a reference string,
// and a struct that is not a schema is a classification (refNotASchemaReference)
// rather than something to filter out here. A filter is a place for a real site
// to disappear; #313 made the same choice for the dereferenced receivers its
// template scan matches.
func scanRefReadingSites(t *testing.T) map[string]map[string]bool {
	t.Helper()
	found := map[string]map[string]bool{}
	files := 0
	for _, label := range sortedRefKeys(refScanDirs) {
		dir := refScanDirs[label]
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
				continue
			}
			files++
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
			if err != nil {
				t.Fatalf("parsing %s/%s: %v", dir, name, err)
			}
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				fn := fd.Name.Name
				if fd.Recv != nil && len(fd.Recv.List) > 0 {
					fn = "(" + refReceiverName(fd.Recv.List[0].Type) + ")." + fn
				}
				key := label + "/" + name + " | " + fn
				ast.Inspect(fd, func(n ast.Node) bool {
					sel, ok := n.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					keyword, ok := refKeywordSelectors[sel.Sel.Name]
					if !ok {
						return true
					}
					if found[key] == nil {
						found[key] = map[string]bool{}
					}
					found[key][keyword] = true
					return true
				})
			}
		}
	}
	if files < 25 {
		t.Fatalf("only %d source files read across %d directories; the scan has stopped seeing what it reads "+
			"and would pass whatever the source says", files, len(refScanDirs))
	}
	if len(found) < 40 {
		t.Fatalf("only %d reference-reading functions found across %d files; the scan is no longer matching "+
			"the source it reads", len(found), files)
	}
	return found
}

func refReceiverName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return "*" + refReceiverName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

// TestEveryReferenceReadingSiteIsAccountedFor holds the enumeration against the
// source in both directions and on both axes: nothing that reads a reference is
// unclassified, nothing classified has gone away, and no classified site has
// changed which reference keywords it names.
func TestEveryReferenceReadingSiteIsAccountedFor(t *testing.T) {
	found := scanRefReadingSites(t)

	for _, key := range sortedRefKeys(found) {
		reads := joinRefKeywords(found[key])
		site, ok := refReadingSites[key]
		if !ok {
			t.Errorf("%s reads %s off a schema and is classified nowhere.\n"+
				"Every such site decides, on its own, what it does with each of the three keywords that put a "+
				"reference on a schema object. The ones that got it wrong read EffectiveRef(), which is $ref or "+
				"$recursiveRef and never $dynamicRef, so the sub-schema behind a $dynamicRef went unread and the "+
				"keyword that reached it asserted nothing (issue #337).\n"+
				"Add an entry to refReadingSites saying whether this site reads whichever reference is there -- "+
				"referenceTarget and referenceOn are the funnels that do -- or is about one keyword's own rule, "+
				"and why.", key, reads)
			continue
		}
		if strings.TrimSpace(site.Why) == "" {
			t.Errorf("%s is classified with no reason given; an entry with no reason records nothing", key)
		}
		if site.Reads != reads {
			t.Errorf("%s now reads %s, and is classified as reading %s.\n"+
				"Which keywords a site names is half of what this table records: #313's gate was total over "+
				"positions and blind over reference kinds, and a site could stay classified while quietly "+
				"answering for two keywords out of three (issue #339). Re-read the site and update its entry -- "+
				"including its verdict, if what it does with a keyword has changed.",
				key, reads, site.Reads)
		}
	}

	for _, key := range sortedRefKeys(refReadingSites) {
		if _, ok := found[key]; !ok {
			t.Errorf("refReadingSites classifies %q, which no longer reads a reference off a schema. "+
				"A stale entry is a claim that a site has been thought about, which is what this table exists "+
				"to make trustworthy.", key)
		}
	}
}

// TestReferenceFunnelIsWhereTheKeywordsMeet holds the one structural claim the
// table rests on: referenceTarget and its two companions name all three
// reference keywords between them, so a site that goes through them cannot lose
// one.
//
// Read from the source rather than asserted about behaviour, because the failure
// it guards against is somebody narrowing the funnel -- at which point every
// site classified refReadsWhicheverIsThere on the strength of using it would
// still be classified and would no longer be true.
func TestReferenceFunnelIsWhereTheKeywordsMeet(t *testing.T) {
	found := scanRefReadingSites(t)
	union := map[string]bool{}
	for _, fn := range []string{
		"generator/references.go | (*Generator).referenceTarget",
		"generator/references.go | (*Generator).referenceTargetUncounted",
		"generator/references.go | referenceOn",
	} {
		reads, ok := found[fn]
		if !ok {
			t.Fatalf("%s reads no reference at all; it is the funnel every widened site was routed through", fn)
		}
		for k := range reads {
			union[k] = true
		}
	}
	// EffectiveRef() stands for $ref and $recursiveRef together, which is what
	// makes the pair of names below the whole of the three keywords.
	for _, want := range []string{"EffectiveRef()", "$dynamicRef"} {
		if !union[want] {
			t.Errorf("the reference funnel names %s nowhere, so a site going through it is blind to that "+
				"keyword -- and every entry in refReadingSites classified refReadsWhicheverIsThere on the "+
				"strength of the funnel is now a false claim", want)
		}
	}
}

func joinRefKeywords(set map[string]bool) string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func sortedRefKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
