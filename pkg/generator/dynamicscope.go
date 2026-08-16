package generator

import (
	"strconv"

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

// typeRootedScope is the dynamic scope a generated type starts from: what an
// evaluation that begins at s has entered before it has done anything.
//
// It has entered s itself when s is a schema resource, and nothing at all when
// it is not. That is the emitted evaluator's rule read back into the generator:
// _evalNode is handed a nil scope and the node it starts on contributes a frame
// only where it publishes anchors of its own, which a resource root is the only
// kind of node to do.
//
// Anchoredness is not the question here. A frame goes on for every resource
// entered, anchored or not, because "is this frame the one the reference wants"
// is asked at the walk -- by resolveRecursiveRef of $recursiveAnchor and by
// resolveDynamicRef of the anchor the reference names -- and a scope that
// filtered here would answer a different question for each of them.
//
// See generateTypeDefBody, which is the only caller and where the seed is
// argued; and #293, which is the decision.
func typeRootedScope(s *schema.Schema) []*schema.Schema {
	if s == nil || s.DocumentRoot != s {
		return nil
	}
	return []*schema.Schema{s}
}

// vacuousBookend is one bookended dynamic reference that came back with nothing
// to enforce, named by the keyword that wrote it and the anchor it searched for.
//
// The anchor is empty for a $recursiveRef, which names none: it always means
// "the outermost resource in scope that says $recursiveAnchor: true", and that
// is the spelling pkg/schema files a $recursiveAnchor under.
type vacuousBookend struct {
	keyword string
	anchor  string
}

// noteVacuousBookend records that a bookended reference, resolved against the
// scope the type being generated starts at, landed on a schema that enforces
// nothing -- while the document declares the same anchor somewhere that does.
//
// This is the price of seeding the scope at the type (#293, candidate 1), and it
// is a real one. The suite's typical-dynamic-resolution shape is a list resource
// whose element is a $dynamicRef to "items" and which declares "items" itself,
// as an empty schema, only to satisfy the bookending requirement; the document
// around it declares "items" again with a type. Resolved for the list judged
// alone the reference finds the list's own declaration, which is the right answer
// for that resource and constrains nothing -- so `type List []Foo` becomes `type
// List []Items` with Items an unconstrained any, and a caller holding List gets a
// Validate that accepts every element. The document's own type is unaffected,
// because a document with two declarations in reach is compiled to the runtime
// evaluator, which resolves the reference per value.
//
// So the type is right about the schema it is being asked to judge and weaker
// than the same schema is inside its document, and nothing about the declaration
// says so: it has a Validate, the Validate is not missing, and it passes. That is
// the shape AliasDef.Unenforced already exists for at the one position where even
// a missing Validate is invisible, and this is the same statement one step over.
// See Doc.Caveats, which is where it is said.
//
// All three parts of the condition are needed, and each was put there by a
// document the other two got wrong.
//
// The target enforcing nothing is the loss itself. Where it constrains something
// the type enforces *a* reading of the schema, which is a different question
// (#332) and not a silent weakening.
//
// Another resource answering the same anchor with constraints is what makes the
// loss a loss. Without one the reference means the same thing down every path and
// the empty schema is simply what the author wrote -- which is most of the
// recursive schemas in the corpus, and every one of them would otherwise carry
// this note.
//
// And that resource has to be one an evaluation could enter *before* reaching
// this reference, or the alternative is not an alternative.
// regression/dynamic_ref_boundary_anchor.json is why: its "stray" definition
// declares itemType with a type and sits under $defs with an $id of its own, so
// nothing refers to it and no evaluation ever puts it on a scope. The reference
// in genericBox resolves to genericBox's own empty bookend for every document
// there is, including the whole one, so a note saying the document has a better
// answer would be false. Reachability is asked of the *resource*, since entering
// it is what puts the declaration in scope.
func (g *Generator) noteVacuousBookend(keyword, anchor string, resolved, ctx *schema.Schema) {
	if resolved == nil || ctx == nil || !g.acceptsEveryValue(resolved, 0, map[*schema.Schema]bool{}) {
		return
	}
	// dynamicAnchorDeclarations walks the whole document rather than this type's
	// reach, which is the point: what is lost is exactly what this type cannot
	// see. What each declaration is worth, though, is asked of the resource that
	// owns it -- resourceDynamicAnchor is the rule the scope walk itself applies,
	// and an anchor written on a nested $id belongs to that nested resource
	// rather than to the one containing it (#163, #164).
	other := false
	for _, decl := range g.dynamicAnchorDeclarations(anchor) {
		owner := decl.DocumentRoot
		if owner == nil {
			owner = decl
		}
		contributed := resourceDynamicAnchor(owner, anchor)
		if contributed == nil || contributed == resolved {
			continue
		}
		if g.acceptsEveryValue(contributed, 0, map[*schema.Schema]bool{}) {
			continue
		}
		if g.dynamicallyReachable(owner)[ctx] {
			other = true
			break
		}
	}
	if !other {
		return
	}
	note := vacuousBookend{keyword: keyword, anchor: anchor}
	for _, have := range g.vacuousBookends {
		if have == note {
			return
		}
	}
	g.vacuousBookends = append(g.vacuousBookends, note)
}

// caveat renders one recorded reference as the paragraph that goes above the
// declaration, which is where a caller will meet it.
//
// It says three things, and each is there because a reader who knows only the
// generated file cannot get it anywhere else: that this type checks less than
// its schema states, why -- the reference is answered by the resources an
// evaluation entered, and validating a value as this type enters only this one
// -- and what to reach for instead, which is the type generated for the whole
// document.
func (v vacuousBookend) caveat(name string) string {
	anchor := "the $recursiveAnchor of the outermost resource in scope"
	if v.anchor != "" {
		anchor = "$dynamicAnchor " + strconv.Quote(v.anchor)
	}
	return wrapProse(name+" checks less than its schema states. A "+v.keyword+
		" under it is answered by "+anchor+
		", which is a question about the resources an evaluation has entered -- and a value validated as a "+name+
		" has entered nothing beyond the resource this schema is written in. So the reference resolves to the declaration there, which constrains nothing, and the position it governs goes unchecked. "+
		"Another resource in this document declares the same anchor with constraints and can be entered on the way here, and a value arriving through it is judged by them. "+
		"Validate through the type generated for the document as a whole to get that reading.", caveatWidth)
}

// caveatWidth is the column a caveat paragraph wraps at, chosen so that the
// "// " the emitter puts in front of every line leaves the result inside the
// 80 columns the rest of this repository's generated comments keep to.
const caveatWidth = 77

// attachVacuityCaveats hangs the caveats a body collected on the declaration
// that body produced.
//
// The last definition under the name is the one, and "last" rather than "first"
// is what makes it the right one: a body resolves the references in its members
// before it appends itself, so any definition another type appended in the
// meantime is already behind it.
//
// A name nothing declared gets nothing. Several arms mint a name, find they can
// carry nothing under it and decline, and a caveat has nowhere to go then -- the
// same reason UnenforcedSchemas drops a name that did not survive into the file.
func (g *Generator) attachVacuityCaveats(name string) {
	if len(g.vacuousBookends) == 0 {
		return
	}
	for i := len(g.output.TypeDefs) - 1; i >= 0; i-- {
		td := g.output.TypeDefs[i]
		if td.TypeName() != name {
			continue
		}
		// Every kind of named type embeds Doc, so the pointer method below is
		// promoted onto all of them; a kind that ever stops embedding it stops
		// matching here rather than silently dropping the note.
		if doc, ok := td.(interface{ addCaveat(string) }); ok {
			for _, v := range g.vacuousBookends {
				doc.addCaveat(v.caveat(name))
			}
		}
		return
	}
}

// noteDynamicScopeConsulted records one consultation of the dynamic scope by a
// bookended $recursiveRef or $dynamicRef: that it happened, and how many frames
// stood above the depth the type being generated started at.
//
// It exists because resolveRecursiveRef's direction only means something on a
// scope more than one frame deep, and the claim that a type-rooted scope is
// never that deep is a measurement -- which is exactly the kind of claim this
// function has already been wrong about once. #167 recorded "the walk selects
// nothing, always at depth 1" in a comment, backed by an instrumented run that
// was then thrown away; it was wrong, and nothing in the tree could say so for
// months. TestDynamicScopeStaysAtTheTypeItStartedIn reads both counters, so the
// claim now fails from inside the repository when it stops holding.
//
// Two counters and not one, because either half can go quiet on its own.
// framesAboveTypeScope is the invariant. dynamicScopeConsultations is what keeps
// a zero from being vacuous: a refactor that stopped consulting the scope at all
// -- or a corpus that stopped containing a bookended reference -- would leave the
// invariant trivially true, which is the shape of guard #167 left behind.
//
// It is unconditional rather than behind a build tag because there is nothing to
// buy by making it conditional, and a guard that only exists under a tag is one
// nobody runs. The cost is an increment, a subtraction and a compare, on a path
// reached 36 times across the whole 608-schema corpus. Measured rather than
// assumed: generating that corpus takes 164-167 ms with these three lines and
// 166-168 ms with them and the base bookkeeping removed (8 iterations, 5 runs
// each), so the difference is smaller than the spread between runs of the same
// binary.
func (g *Generator) noteDynamicScopeConsulted() {
	g.dynamicScopeConsultations++
	if above := len(g.dynamicScope) - g.typeScopeBase; above > g.framesAboveTypeScope {
		g.framesAboveTypeScope = above
	}
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
// Nothing here reads g.dynamicScope, and that is what made #293 answerable
// without moving anything else. The routing question is answered from what s
// reaches, so the seed -- document-rooted, as it was, or at the type being
// generated, as typeRootedScope now makes it -- cannot change which schemas
// arrive at the evaluator. It changed only what the static path resolves for the
// ones that stay: four documents in this repository's corpus and seven groups of
// the external suite, none of whose document-level verdicts moved.
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
