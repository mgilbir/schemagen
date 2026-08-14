package generator

import (
	"github.com/mgilbir/schemagen/pkg/schema"
)

// This file answers the three questions a $recursiveRef or a $dynamicRef asks of
// the generator, and each is a different question.
//
// Where does it point when nothing dynamic happens? That is dynamicRefTarget's
// first result, and it is the whole answer for the majority of these keywords:
// a reference whose target does not carry the matching anchor is *bookended* by
// nothing and means exactly what a plain $ref means.
//
// Which anchor does the dynamic scope get searched for? A $dynamicRef names one
// in its fragment. A $recursiveRef never does -- it always means "the outermost
// resource in scope that says $recursiveAnchor: true" -- so it searches under
// the empty name, which is the spelling pkg/schema already files a
// $recursiveAnchor under and is not a legal $dynamicAnchor, so the two cannot
// collide.
//
// And can the reference loop? That is the question the other two make it
// possible to ask, and the one that has to be answered before any of this is
// compiled at all: see dynamicRefCanLoop.

// dynamicRefTarget reads the $recursiveRef or $dynamicRef on s and says where it
// points.
//
// target is the plain-$ref reading: where the reference goes when the dynamic
// scope has nothing to say about it. It is also what the generated evaluator
// falls back to, so it is needed whether or not the reference is dynamic.
//
// dynamic reports whether the dynamic scope is searched at all. The rule is
// bookending: the reference resolves dynamically only when the schema it
// statically lands on declares the very anchor the reference names. Written
// without that, the keyword is a $ref with a longer name -- which is what makes
// "$recursiveRef with no $recursiveAnchor works like $ref" a test with the
// answer in its title.
//
// The third result is false when the reference names nothing this generator can
// find. The caller must then refuse the schema rather than compile a node that
// checks less than the schema says.
func (g *Generator) dynamicRefTarget(s *schema.Schema) (target *schema.Schema, anchor string, dynamic bool, ok bool) {
	switch {
	case s == nil:
		return nil, "", false, false

	case s.RecursiveRef != "":
		// "#" is the only value draft 2019-09 gives $recursiveRef, and it means
		// the resource the keyword is written in. Anything else is read as the
		// plain reference it is spelled as, rather than guessed at.
		target = g.resolveRefInContextUncounted(s.RecursiveRef, s)
		if target == nil {
			return nil, "", false, false
		}
		if s.RecursiveRef != "#" {
			return target, "", false, true
		}
		return target, "", target.RecursiveAnchor != nil && *target.RecursiveAnchor, true

	case s.DynamicRef != "":
		target, anchor = g.dynamicRefInitialTarget(s.DynamicRef, s, g.resolveRefInContextUncounted)
		if target == nil {
			return nil, "", false, false
		}
		return target, anchor, anchor != "" && target.DynamicAnchor == anchor, true
	}
	return nil, "", false, false
}

// dynamicAnchorDeclarations lists every schema in the document that declares the
// given anchor, which is the set of places a $dynamicRef or $recursiveRef naming
// it could land.
//
// The empty name is the $recursiveAnchor namespace, and a $recursiveAnchor
// always anchors the root of the resource it is written in -- never a subschema
// -- so only resource roots answer to it.
//
// $defs is walked like anything else. A declaration inside one is reachable
// exactly when something refers to it, which is the case this whole mechanism
// exists for.
//
// The answer is cached because the loop check asks it once per schema it walks
// past, and it is a walk of the whole document each time.
func (g *Generator) dynamicAnchorDeclarations(name string) []*schema.Schema {
	if found, ok := g.dynamicAnchorDecls[name]; ok {
		return found
	}
	found := g.findDynamicAnchorDeclarations(name)
	if g.dynamicAnchorDecls == nil {
		g.dynamicAnchorDecls = map[string][]*schema.Schema{}
	}
	g.dynamicAnchorDecls[name] = found
	return found
}

func (g *Generator) findDynamicAnchorDeclarations(name string) []*schema.Schema {
	var found []*schema.Schema
	seen := map[*schema.Schema]bool{}
	var walk func(s *schema.Schema)
	walk = func(s *schema.Schema) {
		if s == nil || s.IsBooleanSchema() || seen[s] {
			return
		}
		seen[s] = true
		if name == "" {
			if s.DocumentRoot == s && s.RecursiveAnchor != nil && *s.RecursiveAnchor {
				found = append(found, s)
			}
		} else if s.DynamicAnchor == name {
			found = append(found, s)
		}
		for _, sub := range allSubSchemas(s) {
			walk(sub)
		}
	}
	walk(g.rootSchema)
	return found
}

// resourceDynamicAnchor finds the anchor a schema resource declares under a
// given name, or nil.
//
// The scope rule is the one pkg/schema's resource graph applies, and it is the
// reason this is not findDynamicAnchor: a nested $id starts a resource of its
// own, so an anchor written *on* that nested root belongs to it and not to the
// resource being asked about. findDynamicAnchor stops descending at such a
// boundary but still reads the boundary node itself, which is right for "what
// can this document reach" and wrong for "what does this resource contribute to
// the dynamic scope".
//
// The difference is not theoretical. In the suite's "after leaving a dynamic
// scope" schema the document root holds $defs/thingy, whose own $id makes it the
// resource inner_scope and whose $dynamicAnchor is thingy. Credited to the
// document root it would put thingy on the outermost frame of every evaluation,
// and the $dynamicRef would resolve to it every time -- which is the answer that
// schema exists to say is wrong.
func resourceDynamicAnchor(root *schema.Schema, name string) *schema.Schema {
	if root == nil || root.IsBooleanSchema() {
		return nil
	}
	if name == "" {
		if root.RecursiveAnchor != nil && *root.RecursiveAnchor {
			return root
		}
		return nil
	}
	var search func(s *schema.Schema, isRoot bool) *schema.Schema
	search = func(s *schema.Schema, isRoot bool) *schema.Schema {
		if s == nil || s.IsBooleanSchema() {
			return nil
		}
		if !isRoot && s.DocumentRoot == s {
			return nil
		}
		if s.DynamicAnchor == name {
			return s
		}
		for _, sub := range allSubSchemas(s) {
			if found := search(sub, false); found != nil {
				return found
			}
		}
		return nil
	}
	return search(root, true)
}

// dynamicScopeDecidesTheTarget reports whether some bookended dynamic reference
// under s has a target the schema text does not fix -- which is to say, whether
// generating a Go type for s means choosing one of several answers and calling
// it the answer.
//
// The static path resolves such a reference once, against a dynamic scope the
// generator maintains while it walks. That is sound only where the walk and the
// evaluation agree about the path, and they cannot agree in general: the whole
// point of the keyword is that the path is a property of the *instance*. One Go
// type is emitted per schema, so a definition two resources reach through
// different scopes gets one binding and the other resource's documents are
// judged by it. #159 gave the runtime evaluator the scope that answers this, but
// only schemas the static path declined ever reached it.
//
// The question asked here is what makes the answer narrow. A bookended reference
// whose anchor is declared exactly once among the schemas s can reach has one
// possible target however the instance arrives, so the static resolution is the
// dynamic one and the type it produces stays. Only a second declaration puts the
// answer in the document's hands, and only then is the whole schema compiled to
// the evaluator. That is why {"$recursiveAnchor":true,"additionalProperties":
// {"$recursiveRef":"#"}} -- the recursive tree every draft-2019-09 schema in the
// suite is written as -- keeps the type it has today.
//
// Reachability is asked of s rather than of the document because a generated
// type is validated as the root of its own evaluation: a caller holding a
// NumberList and calling Validate on it starts the dynamic scope at numberList,
// exactly as a validator handed that subschema would. So the declarations that
// can win are the ones s reaches, and a sibling definition s has no route to
// cannot be on any scope this type sees.
//
// $defs is followed like anything else, which over-counts a definition nothing
// refers to. Over-counting compiles a schema that did not have to be compiled;
// under-counting leaves the reference resolved to a guess, so the reach is
// deliberately the generous one.
//
// Nothing here reads g.dynamicScope, and that is worth saying where #293 will be
// read. The routing question is answered from what s reaches, so which scope the
// generator maintains -- document-rooted as today, or seeded at the type being
// generated -- cannot change which schemas arrive at the evaluator. It changes
// only what the static path resolves for the ones that stay, and
// resolveRecursiveRef records what is settled and what is not about that.
func (g *Generator) dynamicScopeDecidesTheTarget(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	reach := g.dynamicallyReachable(s)
	for node := range reach {
		if node.RecursiveRef == "" && node.DynamicRef == "" {
			continue
		}
		_, anchor, dynamic, ok := g.dynamicRefTarget(node)
		if !ok || !dynamic {
			continue
		}
		if countDynamicAnchorDeclarations(reach, anchor) > 1 {
			return true
		}
	}
	return false
}

// dynamicallyReachable collects every schema an evaluation rooted at s could
// arrive at, following references as well as subschemas.
//
// References have to be followed, because a resource reached only through one is
// still on the dynamic scope while it is being evaluated -- that is what makes
// the generic-container shape work at all, and a walk of the tree alone would
// miss every publisher of the anchor it looks for.
func (g *Generator) dynamicallyReachable(s *schema.Schema) map[*schema.Schema]bool {
	seen := map[*schema.Schema]bool{}
	var walk func(*schema.Schema)
	walk = func(n *schema.Schema) {
		if n == nil || n.IsBooleanSchema() || seen[n] {
			return
		}
		seen[n] = true
		for _, sub := range allSubSchemas(n) {
			walk(sub)
		}
		// allSubSchemas answers "which subschemas produce a Go type" and leaves
		// out two that hold a schema all the same; an anchor or a reference under
		// either is as real as any other.
		walk(n.PropertyNames)
		walk(n.ContentSchema)
		if n.Ref != "" {
			walk(g.resolveRefInContextUncounted(n.Ref, n))
		}
		if n.RecursiveRef != "" || n.DynamicRef != "" {
			if target, _, _, ok := g.dynamicRefTarget(n); ok {
				walk(target)
			}
		}
	}
	walk(s)
	return seen
}

// countDynamicAnchorDeclarations counts the schemas in reach that declare the
// given anchor.
//
// The empty name is the $recursiveAnchor namespace, and a $recursiveAnchor
// always anchors the root of the resource it is written in, so only resource
// roots answer to it -- the same rule findDynamicAnchorDeclarations applies.
func countDynamicAnchorDeclarations(reach map[*schema.Schema]bool, name string) int {
	count := 0
	for node := range reach {
		if name == "" {
			if node.DocumentRoot == node && node.RecursiveAnchor != nil && *node.RecursiveAnchor {
				count++
			}
			continue
		}
		if node.DynamicAnchor == name {
			count++
		}
	}
	return count
}

// dynamicRefCanLoop reports whether evaluating the reference on s could arrive
// back at s with the same value still in hand, which is an evaluation that never
// finishes.
//
// A $dynamicRef is the one keyword that can build a loop the node builder's own
// cycle check cannot see. That check refuses a cycle in the *static* tree; a
// dynamic reference's target is chosen per document, so the cycle it closes is
// not in the tree at all -- {"$id":"a","$recursiveAnchor":true,"$recursiveRef":"#"}
// is three keywords, no cycle a builder walking the tree would meet, and a
// generated program that hangs.
//
// The question is asked of every schema the reference could land on, because any
// one of them is a real destination for some document. A candidate that reaches
// the reference again *without descending into the value* has closed the loop:
// the same value arrives at the same keyword, so the next hop asks the identical
// question and so does the one after it. A candidate that can only get back by
// way of properties, items or additionalProperties has not, because the value
// gets smaller each time round and a finite document ends the recursion -- which
// is what every recursive schema in the test suite does, and why refusing all
// recursion here would refuse the schemas this is being built for.
//
// Only the in-place applicators are followed, for exactly that reason. $defs is
// not among them: a definition applies where something refers to it, and the
// reference is what is being followed.
func (g *Generator) dynamicRefCanLoop(s *schema.Schema, target *schema.Schema, anchor string) bool {
	candidates := g.dynamicAnchorDeclarations(anchor)
	if target != nil {
		candidates = append(candidates, target)
	}
	seen := map[*schema.Schema]bool{}
	queue := append([]*schema.Schema(nil), candidates...)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == nil || cur.IsBooleanSchema() || seen[cur] {
			continue
		}
		seen[cur] = true
		if cur == s {
			return true
		}
		queue = append(queue, g.inPlaceSuccessors(cur)...)
	}
	return false
}

// inPlaceSuccessors lists the schemas that apply to the *same* value as s does.
//
// These are the edges a value travels for free: a reference, the conjunctions
// and disjunctions, the two sides of a conditional, and a dependent schema,
// which applies to the object itself rather than to the property that triggers
// it. Everything left out -- properties, items, contains, propertyNames,
// additionalProperties and the unevaluated pair -- moves to a smaller value, and
// that is the whole distinction dynamicRefCanLoop rests on.
//
// A dynamic reference met along the way is followed to every schema it could
// resolve to, since which one it picks is not decided here.
func (g *Generator) inPlaceSuccessors(s *schema.Schema) []*schema.Schema {
	var out []*schema.Schema
	out = append(out, s.AllOf...)
	out = append(out, s.AnyOf...)
	out = append(out, s.OneOf...)
	if s.Not != nil {
		out = append(out, s.Not)
	}
	if s.If != nil {
		out = append(out, s.If)
	}
	if s.Then != nil {
		out = append(out, s.Then)
	}
	if s.Else != nil {
		out = append(out, s.Else)
	}
	for _, key := range sortedKeys(s.DependentSchemas) {
		out = append(out, s.DependentSchemas[key])
	}
	if s.Ref != "" {
		if resolved := g.resolveRefInContextUncounted(s.Ref, s); resolved != nil {
			out = append(out, resolved)
		}
	}
	if s.RecursiveRef != "" || s.DynamicRef != "" {
		target, anchor, dynamic, ok := g.dynamicRefTarget(s)
		if ok {
			out = append(out, target)
			if dynamic {
				out = append(out, g.dynamicAnchorDeclarations(anchor)...)
			}
		}
	}
	return out
}
