package generator

import "github.com/mgilbir/schemagen/pkg/schema"

// Reading a reference off a schema.
//
// Three keywords put a reference on a schema object -- $ref, $recursiveRef and
// $dynamicRef -- and a site that reads one of them has to decide what it does
// with all three. Schema.EffectiveRef answers for two of them and deliberately
// leaves the third out, so a site written as
//
//	if ref := s.EffectiveRef(); ref != "" { ... }
//
// is a site that cannot see a $dynamicRef at all. Where the site's purpose is to
// reach the sub-schema the reference names, that is not a missing feature: it is
// the sub-schema going unread, and a keyword whose whole content is behind the
// reference then asserts nothing. {"contains":{"$dynamicRef":"#it"}} counted
// every element of the array as matching, so `contains` said only that the array
// was non-empty, while the identical schema written {"$ref":"#/$defs/item"}
// generated the real per-element check (issue #337).
//
// referenceTarget is the funnel that answers for all three -- with
// referenceTargetUncounted for a walk that must leave no trace, and referenceOn
// for a site that asks only whether a reference is there. A site that
// follows a reference to read what is there goes through it; the sites that do
// not are the ones whose subject is a particular *keyword* rather than the
// schema behind it -- the pre-2019-09 rule that a $ref replaces its siblings is
// about $ref and about nothing else -- and each of those is recorded, with its
// reason, in refReadingSites in refsites_test.go. That table is held against the
// source in both directions, so a new site is unclassified until somebody says
// which of the two it is, and a site whose set of reference keywords changes has
// to be reclassified rather than quietly drifting.

// referenceTarget follows whichever reference keyword s carries to the schema it
// reaches, and returns the reference as written beside it.
//
// It returns "" and nil for a schema that carries no reference, and a non-empty
// reference with a nil target for one this generator cannot resolve -- the same
// two outcomes the callers already distinguish.
//
// $ref and $recursiveRef come first and $dynamicRef second, which is the order
// resolveType's arms already take and is what keeps this funnel a widening
// rather than a change: a schema carrying both gets exactly the answer it got
// before, and a schema carrying only the third gets one where it used to get
// nothing. The two orderings differ only for a node stating $ref and $dynamicRef
// together, which no draft forbids and which every one of these sites already
// read as the $ref.
//
// The static half resolves through resolveRefInContext rather than
// resolveEffectiveRefSchema, again to keep the delta to the keyword that was
// invisible: resolveEffectiveRefSchema resolves a bookended $recursiveRef
// against the dynamic scope, which is the right answer at the arm that produces
// a Go type for the node and a different question from the one asked here.
// TestReferenceKindsAgree drives $recursiveRef through the same positions as the
// other two and is what would say if that difference ever costs a verdict.
func (g *Generator) referenceTarget(s *schema.Schema) (string, *schema.Schema) {
	if s == nil {
		return "", nil
	}
	if ref := s.EffectiveRef(); ref != "" {
		return ref, g.resolveRefInContext(ref, s)
	}
	if s.DynamicRef != "" {
		return s.DynamicRef, g.resolveDynamicRef(s.DynamicRef, s)
	}
	return "", nil
}

// referenceTargetUncounted is referenceTarget for the walks that only look.
//
// resolveRefInContext records a reference it cannot serve, so that Generate can
// report it; a walk asking "does anything down this chain state X" must not,
// because it would turn an optimistic look into a reported error for a reference
// the run never needed. See nodeBuilder.resolve, which is the same distinction
// one level down.
func (g *Generator) referenceTargetUncounted(s *schema.Schema) (string, *schema.Schema) {
	if s == nil {
		return "", nil
	}
	if ref := s.EffectiveRef(); ref != "" {
		return ref, g.resolveRefInContextUncounted(ref, s)
	}
	if s.DynamicRef != "" {
		return s.DynamicRef, g.resolveDynamicRefUncounted(s.DynamicRef, s)
	}
	return "", nil
}

// referenceOn reports the reference s carries without resolving it, by the same
// precedence referenceTarget applies.
//
// It is for the sites that ask only whether there is one -- a predicate about
// the node, not about what the node reaches.
func referenceOn(s *schema.Schema) string {
	if s == nil {
		return ""
	}
	if ref := s.EffectiveRef(); ref != "" {
		return ref
	}
	return s.DynamicRef
}
