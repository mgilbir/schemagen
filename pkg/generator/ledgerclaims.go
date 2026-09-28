package generator

import (
	"slices"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// This file reads the claims out of one declaration's IR: claimsOf walks every
// element the declaration will be emitted from and credits each keyword the
// element enforces, through the provenance the element carries (see
// Provenance) or, for the handful of facts the IR keeps as plain data on the
// declaration itself -- the Go type, the required names, the enum values -- by
// comparing that data with what each node of the declaration's in-place group
// states.
//
// Nothing here asks an arm what it meant to build. An element the passes after
// the arms removed is not in the IR, and so is not read.

// claimsOf fills d's claim pool and dispatch sets from its IR.
func (l *ledgerRun) claimsOf(d *ledgerDefInfo) {
	switch td := d.td.(type) {
	case *StructDef:
		l.structClaims(d, td)
	case *AliasDef:
		l.aliasClaims(d, td)
	case *InferredAliasDef:
		l.inferredAliasClaims(d, td)
	case *BigIntAliasDef:
		l.typeClaims(d, l.group(d), kindInteger)
		l.rulesClaims(d, td.Validations, rulesBigInt)
		l.variantClaims(d, td.AnyOfVariants, "anyOf", rulesBigIntVariant, 0)
		l.variantClaims(d, td.OneOfVariants, "oneOf", rulesBigIntVariant, 0)
	case *EnumDef:
		l.enumClaims(d, td)
	case *NotSchemaDef:
		l.notClaims(d, td)
	case *DynamicSchemaDef:
		l.dynamicSchemaClaims(d, td)
	case *AnnotationSchemaDef:
		l.provenanceClaims(d, td.Claim)
	case *TypeOnlySchemaDef:
		l.typeOnlyClaims(d, td)
	}
}

// group is the in-place group of d's root: the nodes that bind on every
// instance the declaration holds, unconditionally.
func (l *ledgerRun) group(d *ledgerDefInfo) []*schema.Schema {
	if d.root == nil {
		return nil
	}
	return l.inPlaceReach(l.scopeRoot(d))
}

// scopeRoot is the node a declaration's scope starts at: the node it was
// generated for, or -- for a declaration whose recorded node is a merge target
// the generator built -- the node the merge was built from.
func (l *ledgerRun) scopeRoot(d *ledgerDefInfo) *schema.Schema {
	if d.root == nil {
		return nil
	}
	if from, ok := l.g.mergeDocSources[d.root]; ok {
		return l.canonical(from)
	}
	if from, ok := l.g.ledger.synthesizedFrom[d.root]; ok {
		return l.canonical(from)
	}
	return d.root
}

// provenanceClaims credits whatever one element's provenance names.
func (l *ledgerRun) provenanceClaims(d *ledgerDefInfo, p Provenance) {
	for _, n := range p.Evaluated {
		l.claimWhole(d, n, claimEvaluator)
	}
	if p.Source != nil && p.Keyword != "" {
		l.claim(d, p.Source, p.Keyword, claimStatic)
	}
}

// rulesClaims credits the keywords a list of rules enforces: the rules of the
// kinds the list's template renders (see ledgerrender.go).
func (l *ledgerRun) rulesClaims(d *ledgerDefInfo, rules []ValidationRule, list ruleList) {
	for _, r := range rendered(rules, list) {
		src := r.Claim.Source
		if src == nil {
			continue
		}
		switch r.RuleType {
		case "content":
			if c, ok := r.Value.(ContentCheck); ok {
				if c.Encoding != "" {
					l.claim(d, src, "contentEncoding", claimStatic)
				}
				if c.MediaType != "" {
					l.claim(d, src, "contentMediaType", claimStatic)
				}
			}
			continue
		case "forbidden":
			// A value that is present at all is refused, so everything the
			// sub-schema states is vacuous past that.
			l.claimWhole(d, src, claimStatic)
			continue
		case "const":
			// A const the enum arm promoted and the rule carries as a const, and
			// an enum of one: the one value, either way.
			l.claim(d, src, "const", claimStatic)
			if len(src.Enum) == 1 {
				l.claim(d, src, "enum", claimStatic)
			}
			continue
		case "format":
			// Against a value decoded into the format's Go type the check
			// is what is left after the decode -- the address family -- and
			// the two together carry the format only where the decode
			// refuses everything else; see typedFormatDecodeEnforces.
			if f, ok := r.Value.(string); ok && !r.StringBacked && !typedFormatDecodeEnforces[f] {
				continue
			}
		}
		l.claim(d, src, r.Claim.Keyword, claimStatic)
	}
}

// structRulesClaims credits a struct's two rule lists. Validations run for an
// object; NonObjectValidations, where the struct holds a document that is not
// an object (AcceptNonObject), run for everything else. A list that runs for
// only some kinds carries a keyword whole only where the kinds it skips are
// answered for another way; see scopedRulesClaims.
func (l *ledgerRun) structRulesClaims(d *ledgerDefInfo, sd *StructDef) {
	if !sd.AcceptNonObject {
		// Every other kind is refused on decode.
		l.rulesClaims(d, sd.Validations, rulesStruct)
		return
	}
	if sd.RejectObject {
		// Every object is refused, so the non-object list is the whole of it.
		l.rulesClaims(d, sd.NonObjectValidations, rulesStructNonObject)
		return
	}
	objRules := rendered(sd.Validations, rulesStruct)
	nonObjRules := rendered(sd.NonObjectValidations, rulesStructNonObject)
	l.scopedRulesClaims(d, objRules, rulesStruct, kindObject, nonObjRules)
	l.scopedRulesClaims(d, nonObjRules, rulesStructNonObject, kindAll&^kindObject, objRules)
}

// scopedRulesClaims credits rules that run only for an instance of kinds
// runsFor; every other kind passes them unchecked. A keyword that says nothing
// about the skipped kinds -- minLength where only strings are checked -- is
// carried whole. One that binds on every kind is carried only where the kinds
// skipped here are checked by another list, elsewhere, stating the same
// keyword of the same node, or, for `type`, where the type admits every kind
// skipped and so is satisfied by them. Crediting it otherwise would hide the
// document of a skipped kind that the keyword excludes and nothing refuses.
//
// Only a rule about the value itself -- one read from a node of the
// declaration's own group -- is judged so. A rule about a property's value runs
// wherever the value holding the property is an object, which is every case
// the property can arise in.
func (l *ledgerRun) scopedRulesClaims(d *ledgerDefInfo, rules []ValidationRule, list ruleList, runsFor jsonKinds, elsewhere []ValidationRule) {
	own := make(map[*schema.Schema]bool)
	for _, n := range l.group(d) {
		own[l.canonical(n)] = true
	}
	aboutValue := func(src *schema.Schema) bool {
		if src == nil || own[l.canonical(src)] {
			return true
		}
		return slices.ContainsFunc(l.authors(src), func(a *schema.Schema) bool { return own[l.canonical(a)] })
	}
	var kept []ValidationRule
	for _, r := range rendered(rules, list) {
		if !aboutValue(r.Claim.Source) {
			kept = append(kept, r)
			continue
		}
		kw := ruleClaimKeyword(r)
		if kinds, scoped := keywordKinds[kw]; scoped {
			if kinds&^runsFor == 0 {
				kept = append(kept, r)
			}
			continue
		}
		if kw == "type" && r.Claim.Source != nil && (kindAll&^runsFor)&^kindsOfTypeList(r.Claim.Source.Type) == 0 {
			kept = append(kept, r)
			continue
		}
		for _, o := range elsewhere {
			if o.Claim.Source == r.Claim.Source && ruleClaimKeyword(o) == kw {
				kept = append(kept, r)
				break
			}
		}
	}
	l.rulesClaims(d, kept, list)
}

// ruleClaimKeyword is the keyword rulesClaims credits a rule to, for the rules
// scopedRulesClaims has to judge by keyword.
func ruleClaimKeyword(r ValidationRule) string {
	switch r.RuleType {
	case "content":
		return "contentEncoding"
	case "forbidden", "const":
		return r.RuleType
	}
	return r.Claim.Keyword
}

// variantClaims credits a group of per-branch rule lists -- an alias's anyOf or
// oneOf, judged branch by branch against the one Go value. The group itself is
// claimed for the node whose branches the lists were read from, found by
// matching branch sources; a group whose every list is empty -- each branch
// satisfied by every value of the type -- has none to match and says so by
// being vacuous where the scope walk finds it.
//
// skips is the kinds of value the lists are never run for -- the wrapper of
// an alias that holds a value of another kind as it came. Such a value passes
// every branch unchecked, so it satisfies an anyOf, which the group is then
// credited with only where some branch states nothing about that kind, and it
// satisfies every branch of a oneOf at once, which the group is then not
// credited with. A group with a rule its template does not render is judged
// on less than its branches state, and is not credited either.
func (l *ledgerRun) variantClaims(d *ledgerDefInfo, variants [][]ValidationRule, keyword string, list ruleList, skips jsonKinds) {
	if len(variants) == 0 {
		return
	}
	whole := true
	for _, rules := range variants {
		if len(rendered(rules, list)) != len(rules) {
			whole = false
		}
		l.rulesClaims(d, rules, list)
	}
	if !whole {
		return
	}
	for _, owner := range l.group(d) {
		branches := owner.AnyOf
		if keyword == "oneOf" {
			branches = owner.OneOf
		}
		if len(branches) != len(variants) {
			continue
		}
		match := true
		for i, rules := range variants {
			for _, r := range rules {
				if r.Claim.Source != nil && l.canonical(r.Claim.Source) != l.canonical(branches[i]) {
					match = false
				}
			}
		}
		if match && skips != 0 && !l.groupHoldsForSkipped(branches, keyword, skips) {
			match = false
		}
		if match {
			l.claim(d, owner, keyword, claimStatic)
			// Every branch keyword the list does not carry was judged vacuous
			// for the Go type by aliasVariantRules, which fails the whole group
			// closed on anything it cannot judge; the kind check in the scope
			// walk credits exactly those.
		}
	}
}

// groupHoldsForSkipped reports whether an anyOf or oneOf over branches is
// satisfied by every value of the kinds skips, which reach none of the
// branches' checks: an anyOf where some branch says nothing about those kinds,
// a oneOf where exactly one does and every other refuses them by its type.
func (l *ledgerRun) groupHoldsForSkipped(branches []*schema.Schema, keyword string, skips jsonKinds) bool {
	silent, refusing := 0, 0
	for _, b := range branches {
		switch {
		case l.saysNothingAbout(b, skips):
			silent++
		case l.refusesKinds(b, skips):
			refusing++
		}
	}
	if keyword == "oneOf" {
		return silent == 1 && silent+refusing == len(branches)
	}
	return silent > 0
}

// refusesKinds reports whether n refuses every value of kinds: something it
// reaches in place is false, or states a type admitting none of them.
func (l *ledgerRun) refusesKinds(n *schema.Schema, kinds jsonKinds) bool {
	for _, m := range l.inPlaceReach(n) {
		if m.IsFalseSchema() {
			return true
		}
		if !m.IsBooleanSchema() && len(m.Type) > 0 && kindsOfTypeList(m.Type)&kinds == 0 {
			return true
		}
	}
	return false
}

// saysNothingAbout reports whether every assertion n and what it reaches in
// place state is about kinds other than those of kinds, so that a value of
// those kinds satisfies n whatever it is.
func (l *ledgerRun) saysNothingAbout(n *schema.Schema, kinds jsonKinds) bool {
	for _, m := range l.inPlaceReach(n) {
		if m.IsBooleanSchema() {
			if m.IsFalseSchema() {
				return false
			}
			continue
		}
		for _, k := range l.assertions(m) {
			scope, scoped := keywordKinds[k]
			if !scoped || scope&kinds != 0 {
				return false
			}
		}
	}
	return true
}

// typeClaims credits `type` on every node of nodes whose stated type admits
// every kind the Go type can hold. The Go type is what the decoder enforces:
// a value of any other kind does not decode. JSON null is left out of the
// comparison; whether a null reaches the value is a question the ledger does
// not answer (see the grid).
func (l *ledgerRun) typeClaims(d *ledgerDefInfo, nodes []*schema.Schema, kinds jsonKinds) {
	kinds &^= kindNull
	if kinds == 0 || kinds == kindAll&^kindNull {
		return
	}
	for _, n := range nodes {
		if n == nil || n.IsBooleanSchema() || len(n.Type) == 0 {
			continue
		}
		if kinds&^kindsOfTypeList(n.Type) == 0 {
			l.claim(d, n, "type", claimStatic)
		}
	}
}

// goTypeClaims credits what a Go type enforces at a position whose nodes are
// nodes: the kind, and the formats whose Go type decodes only their values.
func (l *ledgerRun) goTypeClaims(d *ledgerDefInfo, nodes []*schema.Schema, t GoType) {
	l.goTypeClaimsAt(d, nodes, t, 0)
}

func (l *ledgerRun) goTypeClaimsAt(d *ledgerDefInfo, nodes []*schema.Schema, t GoType, depth int) {
	if t == nil || len(nodes) == 0 || depth > maxItemLevels {
		return
	}
	l.typeClaims(d, nodes, l.goKinds(t, 0))
	noteTypeRefs(d, t)
	base := t
	if pt, ok := base.(*PointerType); ok {
		base = pt.Inner
	}
	// A slice or a map holds its elements as a Go type too, and what that type
	// decodes is a claim on the schema of the elements.
	switch ct := base.(type) {
	case *ArrayType:
		var items []*schema.Schema
		for _, n := range nodes {
			if n != nil && !n.IsBooleanSchema() && n.Items != nil && n.Items.Schema != nil {
				items = append(items, n.Items.Schema)
			}
		}
		l.goTypeClaimsAt(d, l.withReach(items), ct.ItemType, depth+1)
	case *MapType:
		var values []*schema.Schema
		for _, n := range nodes {
			if n != nil && !n.IsBooleanSchema() && n.AdditionalProperties != nil && n.AdditionalProperties.Schema != nil {
				values = append(values, n.AdditionalProperties.Schema)
			}
		}
		l.goTypeClaimsAt(d, l.withReach(values), ct.ValueType, depth+1)
	}
	if prim, ok := base.(*PrimitiveType); ok && (prim.Name == "time.Time" || prim.Name == "netip.Addr") {
		for _, n := range nodes {
			if n == nil || n.Format == nil {
				continue
			}
			// Only a decode that refuses every string the format excludes
			// carries it; see typedFormatDecodeEnforces.
			if ft := formatGoType(*n.Format); ft != nil && ft.GoTypeName() == prim.Name && typedFormatDecodeEnforces[*n.Format] {
				l.claim(d, n, "format", claimStatic)
			}
		}
	}
}

// noteTypeRefs records every generated type t holds a value as.
func noteTypeRefs(d *ledgerDefInfo, t GoType) {
	switch v := t.(type) {
	case *PointerType:
		noteTypeRefs(d, v.Inner)
	case *ArrayType:
		noteTypeRefs(d, v.ItemType)
	case *MapType:
		noteTypeRefs(d, v.ValueType)
	case *NamedType:
		if v.PkgAlias == "" {
			if d.typeRefs == nil {
				d.typeRefs = make(map[string]bool)
			}
			d.typeRefs[v.Name] = true
		}
	}
}

// goKinds is the set of JSON kinds a value of Go type t can be decoded from.
func (l *ledgerRun) goKinds(t GoType, depth int) jsonKinds {
	if t == nil || depth > 32 {
		return kindAll
	}
	switch v := t.(type) {
	case *PointerType:
		return l.goKinds(v.Inner, depth+1) | kindNull
	case *ArrayType:
		return kindArray
	case *MapType:
		return kindObject
	case *InterfaceType:
		return kindAll
	case *PrimitiveType:
		switch v.Name {
		case "string", "time.Time", "netip.Addr":
			return kindString
		case "int64", "int", "int32":
			return kindInteger
		case "float64", GoNumberTypeName:
			return kindNumeric
		case "bool":
			return kindBoolean
		}
		return kindAll
	case *NamedType:
		if v.PkgAlias != "" {
			return kindAll
		}
		target := l.byName[v.Name]
		if target == nil {
			return kindAll
		}
		switch td := target.td.(type) {
		case *StructDef:
			if td.AcceptNonObject {
				return kindAll
			}
			return kindObject
		case *AliasDef:
			if td.AcceptNonMatching {
				return kindAll
			}
			return l.goKinds(td.Underlying, depth+1)
		case *EnumDef:
			var k jsonKinds
			for _, ev := range td.Values {
				k |= kindOfValue(ev.Value)
			}
			return k | kindNull
		case *BigIntAliasDef:
			return kindInteger | kindNull
		case *TypeOnlySchemaDef:
			if len(td.TypeBranches) > 0 || len(td.AllowedTypes) == 0 {
				return kindAll
			}
			return kindsOfTypeList(td.AllowedTypes)
		}
		return kindAll
	}
	return kindAll
}

// kindOfValue is the kind of a decoded JSON value.
func kindOfValue(v any) jsonKinds {
	switch x := v.(type) {
	case nil:
		return kindNull
	case bool:
		return kindBoolean
	case string:
		return kindString
	case map[string]any:
		return kindObject
	case []any:
		return kindArray
	case float64:
		if x == float64(int64(x)) {
			return kindInteger
		}
		return kindNumber
	case int, int64:
		return kindInteger
	}
	if n, ok := v.(interface{ String() string }); ok {
		num := schema.Number(n.String())
		if _, isInt := num.Int64(); isInt {
			return kindInteger
		}
		return kindNumeric
	}
	return kindAll
}

// requiredClaims credits `required` on every node of nodes whose required
// names the IR's list includes.
func (l *ledgerRun) requiredClaims(d *ledgerDefInfo, nodes []*schema.Schema, names []string) {
	for _, n := range nodes {
		if n == nil || len(n.Required) == 0 {
			continue
		}
		if stringSubset(n.Required, names) {
			l.claim(d, n, "required", claimStatic)
		}
	}
}

// valueClaims credits `enum` and `const` on every node of nodes whose values
// include every value the IR admits.
func (l *ledgerRun) valueClaims(d *ledgerDefInfo, nodes []*schema.Schema, admitted []any) {
	for _, n := range nodes {
		if n == nil || n.IsBooleanSchema() {
			continue
		}
		if n.Enum != nil && valuesSubset(admitted, n.Enum) {
			l.claim(d, n, "enum", claimStatic)
		}
		if (n.Const != nil || n.ConstIsNull) && valuesSubset(admitted, enumLikeValues(n)) {
			l.claim(d, n, "const", claimStatic)
		}
		if len(n.Type) > 0 {
			var k jsonKinds
			for _, v := range admitted {
				k |= kindOfValue(v)
			}
			if k&^kindsOfTypeList(n.Type) == 0 {
				l.claim(d, n, "type", claimStatic)
			}
		}
	}
}

// dispatchPartly is dispatch for a type d hands a value to only when it is not
// of kinds skips; see scopeWalk.partlyDelegated.
func (d *ledgerDefInfo) dispatchPartly(prop, name string, skips jsonKinds) {
	if name == "" {
		return
	}
	d.dispatch(prop, name)
	if skips != 0 {
		if d.dispatchSkips == nil {
			d.dispatchSkips = make(map[string]jsonKinds)
		}
		d.dispatchSkips[name] |= skips
	}
}

// dispatch records that d's Validate reaches a value through the type named,
// through property prop where one is known ("" for anywhere).
func (d *ledgerDefInfo) dispatch(prop, name string) {
	if name == "" {
		return
	}
	if d.dispatchAll == nil {
		d.dispatchAll = make(map[string]bool)
	}
	d.dispatchAll[name] = true
	if prop == "" {
		return
	}
	if d.dispatchProp == nil {
		d.dispatchProp = make(map[string][]string)
	}
	if !slices.Contains(d.dispatchProp[prop], name) {
		d.dispatchProp[prop] = append(d.dispatchProp[prop], name)
	}
}

// dispatches reports whether d validates through the type named at the
// position prop ("" when the position is not a struct property).
func (d *ledgerDefInfo) dispatches(prop, name string) bool {
	if prop != "" {
		return slices.Contains(d.dispatchProp[prop], name)
	}
	return d.dispatchAll[name]
}

// ---------------------------------------------------------------------------
// Per declaration kind.
// ---------------------------------------------------------------------------

func (l *ledgerRun) structClaims(d *ledgerDefInfo, sd *StructDef) {
	group := l.group(d)
	if !sd.AcceptNonObject {
		l.typeClaims(d, group, kindObject)
	}
	l.requiredClaims(d, group, sd.RequiredJSON)
	var buf []*schema.Schema
	for _, f := range sd.Fields {
		buf = propertyNodes(buf, group, f.JSONName)
		props := l.withReach(buf)
		l.goTypeClaims(d, props, f.Type)
		for _, p := range props {
			l.nullableUnionClaims(d, p, f.Type)
		}
	}
	for _, vf := range sd.ValidatableFields {
		d.dispatch(vf.JSONName, namedTypeName(vf.GoType))
		if at, ok := vf.GoType.(*ArrayType); ok {
			d.dispatch(vf.JSONName, namedTypeName(at.ItemType))
		}
		if mt, ok := vf.GoType.(*MapType); ok {
			d.dispatch(vf.JSONName, namedTypeName(mt.ValueType))
		}
		if pt, ok := vf.GoType.(*PointerType); ok {
			if at, ok := pt.Inner.(*ArrayType); ok {
				d.dispatch(vf.JSONName, namedTypeName(at.ItemType))
			}
			if mt, ok := pt.Inner.(*MapType); ok {
				d.dispatch(vf.JSONName, namedTypeName(mt.ValueType))
			}
		}
	}
	for i := range sd.OneOfs {
		l.oneOfClaims(d, &sd.OneOfs[i])
	}
	if ap := sd.AdditionalProperties; ap != nil && ap.Claim.Source != nil {
		owner := ap.Claim.Source
		if ap.Forbidden {
			l.claim(d, owner, "additionalProperties", claimStatic)
		}
		if owner.AdditionalProperties != nil && owner.AdditionalProperties.Schema != nil {
			l.goTypeClaims(d, l.withReach([]*schema.Schema{owner.AdditionalProperties.Schema}), ap.ValueType)
			d.dispatch("", namedTypeName(ap.ValueType))
		}
	}
	for i := range sd.PatternProperties {
		pp := &sd.PatternProperties[i]
		owner := pp.Claim.Source
		if owner == nil {
			continue
		}
		child := owner.PatternProperties[pp.Pattern]
		l.claim(d, owner, "patternProperties", claimStatic)
		if pp.IsForbidden {
			l.claimWhole(d, child, claimStatic)
		}
		d.dispatch("", pp.TypeName)
		l.rulesClaims(d, pp.Validations, rulesPatternProperty)
	}
	for _, dr := range sd.DependentRequired {
		l.dependentRequiredClaim(d, dr)
	}
	for i := range sd.DependentSchemas {
		l.dependentSchemaClaims(d, &sd.DependentSchemas[i])
	}
	if pn := sd.PropertyNames; pn != nil {
		l.propertyNamesClaims(d, pn)
	}
	l.structRulesClaims(d, sd)
	for i := range sd.ItemValidations {
		l.itemValidationClaims(d, &sd.ItemValidations[i])
	}
	for i := range sd.ContainsValidations {
		fc := &sd.ContainsValidations[i]
		l.containsClaims(d, fc.Claim.Source, fc.Contains, fc.MinContains, fc.MaxContains, fc.JSONName)
	}
	for i := range sd.TupleValidations {
		ft := &sd.TupleValidations[i]
		for j := range ft.Items {
			l.tupleItemClaims(d, &ft.Items[j], ft.JSONName)
		}
		if ft.Tail != nil {
			l.tupleItemClaims(d, ft.Tail, ft.JSONName)
		}
	}
	for i := range sd.UnevalItemsValidations {
		l.unevalItemsClaims(d, sd.UnevalItemsValidations[i].Def)
	}
	if up := sd.UnevaluatedProperties; up != nil {
		l.unevalPropertiesClaims(d, up)
	}
	for i := range sd.BranchOverflowChecks {
		bc := &sd.BranchOverflowChecks[i]
		owner := bc.Claim.Source
		if owner == nil {
			continue
		}
		d.dispatch("", bc.TypeName)
		if bc.IsForbidden || bc.TypeName != "" {
			l.claim(d, owner, bc.Keyword, claimStatic)
		}
	}
	for i := range sd.RuntimeBranchChecks {
		l.provenanceClaims(d, sd.RuntimeBranchChecks[i].Claim)
	}
	if len(sd.ObjectEnum) > 0 {
		var values []any
		for _, raw := range sd.ObjectEnum {
			var v any
			if err := jsonUnmarshalNumber([]byte(raw), &v); err == nil {
				values = append(values, v)
			}
		}
		l.valueClaims(d, group, values)
	}
	for i := range sd.ObjectOneOfs {
		l.objectBranchGroupClaims(d, sd.ObjectOneOfs[i].Claim, sd.ObjectOneOfs[i].Branches)
	}
	for i := range sd.ObjectAnyOfs {
		l.objectBranchGroupClaims(d, sd.ObjectAnyOfs[i].Claim, sd.ObjectAnyOfs[i].Branches)
	}
	for i := range sd.ObjectConditionals {
		oc := &sd.ObjectConditionals[i]
		l.provenanceClaims(d, oc.Claim)
		l.conditionalBranchClaims(d, &oc.If)
		if oc.Then != nil {
			l.conditionalBranchClaims(d, oc.Then)
		}
		if oc.Else != nil {
			l.conditionalBranchClaims(d, oc.Else)
		}
	}
	if sd.AcceptNonObject && !sd.RejectObject {
		l.objectPathOnly(d, sd)
	}
}

// objectPathOnly takes back what a struct's object path claimed about the
// value itself, where the struct also holds a document that is not an object
// and judges it by NonObjectValidations alone. Every element above but those
// runs only for an object, so a keyword of the struct's own nodes it carries
// is carried for an object and for nothing else: whole where the keyword says
// something only about objects, and for no other. What NonObjectValidations
// carry is credited again after, by structRulesClaims.
func (l *ledgerRun) objectPathOnly(d *ledgerDefInfo, sd *StructDef) {
	own := make(map[*schema.Schema]bool)
	for _, n := range l.group(d) {
		own[n] = true
		own[l.canonical(n)] = true
	}
	nonObject := kindAll &^ kindObject
	carried := func(n *schema.Schema, k string) bool {
		kinds, scoped := keywordKinds[k]
		return (scoped && kinds&^kindObject == 0) || l.holdsForKinds(n, k, nonObject)
	}
	// maporder: deleting while iterating visits every entry once whatever the
	// order, and what is deleted depends on the entry alone.
	for k := range d.pool {
		if own[k.node] && !carried(k.node, k.keyword) {
			delete(d.pool, k)
		}
	}
	var wholes []*schema.Schema
	// maporder: the nodes are collected and each is handled on its own.
	for n := range d.whole {
		if own[n] {
			wholes = append(wholes, n)
		}
	}
	for _, n := range wholes {
		how := d.whole[n]
		delete(d.whole, n)
		for _, k := range l.assertions(n) {
			if carried(n, k) {
				d.setPool(ledgerKey{n, k}, how)
			}
		}
	}
	l.structRulesClaims(d, sd)
}

// holdsForKinds reports whether keyword of n is satisfied by every value of
// kinds whatever the value is -- because what it applies says nothing about
// those kinds, or refuses them where refusing is what satisfies it. It is
// answered for the keywords that bind on every kind; false means not known.
func (l *ledgerRun) holdsForKinds(n *schema.Schema, keyword string, kinds jsonKinds) bool {
	if n == nil || n.IsBooleanSchema() {
		return false
	}
	silent := func(s *schema.Schema) bool { return s == nil || l.saysNothingAbout(s, kinds) }
	switch keyword {
	case "type":
		return len(n.Type) > 0 && kinds&^kindsOfTypeList(n.Type) == 0
	case "anyOf":
		return l.groupHoldsForSkipped(n.AnyOf, "anyOf", kinds)
	case "oneOf":
		return l.groupHoldsForSkipped(n.OneOf, "oneOf", kinds)
	case "allOf":
		return !slices.ContainsFunc(n.AllOf, func(b *schema.Schema) bool { return !silent(b) })
	case "not":
		return n.Not != nil && l.refusesKinds(n.Not, kinds)
	case "if":
		if n.If == nil {
			return false
		}
		return (silent(n.If) && silent(n.Then)) || (l.refusesKinds(n.If, kinds) && silent(n.Else))
	}
	return false
}

// nullableUnionClaims credits the union a Go value that can be nil stands for:
// a oneOf or anyOf of one branch admitting only null and one other, held as a
// pointer to what the other branch decodes into. The null branch is the nil;
// the other is the pointed-to value, whose own type and checks answer for it.
//
// For oneOf the credit needs the other branch to refuse null itself: a branch
// that also admits null makes a null match twice, which oneOf refuses and the
// pointer does not (see the audit's oneOf{null, X-admitting-null} case).
func (l *ledgerRun) nullableUnionClaims(d *ledgerDefInfo, p *schema.Schema, t GoType) {
	if p == nil || p.IsBooleanSchema() || l.goKinds(t, 0)&kindNull == 0 {
		return
	}
	for _, group := range []struct {
		keyword  string
		branches []*schema.Schema
	}{{"oneOf", p.OneOf}, {"anyOf", p.AnyOf}} {
		if len(group.branches) != 2 {
			continue
		}
		var nulls, others []*schema.Schema
		for _, b := range group.branches {
			if admitsOnlyNull(b) {
				nulls = append(nulls, b)
			} else {
				others = append(others, b)
			}
		}
		if len(nulls) != 1 || len(others) != 1 {
			continue
		}
		other := others[0]
		if target := l.refTarget(other); target != nil && len(other.Type) == 0 {
			other = target
		}
		if group.keyword == "oneOf" && (len(other.Type) == 0 || kindsOfTypeList(other.Type)&kindNull != 0) {
			continue
		}
		l.claim(d, p, group.keyword, claimStatic)
		l.claimWhole(d, nulls[0], claimStatic)
		l.goTypeClaims(d, l.withReach([]*schema.Schema{others[0]}), t)
	}
}

// admitsOnlyNull reports whether b is a schema whose only instance is null.
func admitsOnlyNull(b *schema.Schema) bool {
	if b == nil || b.IsBooleanSchema() {
		return false
	}
	if len(b.Type) == 1 && b.Type[0] == "null" {
		return true
	}
	if b.ConstIsNull {
		return true
	}
	return len(b.Enum) == 1 && b.Enum[0] == nil
}

// propertyNodes lists the node each member of nodes states for property name.
//
// The list is written over dst, a buffer the caller reuses from field to field.
func propertyNodes(dst, nodes []*schema.Schema, name string) []*schema.Schema {
	out := dst[:0]
	for _, n := range nodes {
		if n == nil || n.IsBooleanSchema() {
			continue
		}
		if p := n.Properties[name]; p != nil {
			out = append(out, p)
		}
	}
	return out
}

// withReach extends nodes by everything each reaches in place.
func (l *ledgerRun) withReach(nodes []*schema.Schema) []*schema.Schema {
	switch len(nodes) {
	case 0:
		return nil
	case 1:
		return l.inPlaceReach(nodes[0])
	}
	var out []*schema.Schema
	seen := make(map[*schema.Schema]bool)
	for _, n := range nodes {
		for _, m := range l.inPlaceReach(n) {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}

func (l *ledgerRun) oneOfClaims(d *ledgerDefInfo, od *OneOfDef) {
	l.provenanceClaims(d, od.Claim)
	for i := range od.Variants {
		v := &od.Variants[i]
		branch := v.Claim.Source
		l.rulesClaims(d, v.Checks, rulesOneOfVariant)
		if branch != nil {
			l.goTypeClaims(d, l.withReach([]*schema.Schema{branch}), v.Type)
			l.requiredClaims(d, l.withReach([]*schema.Schema{branch}), v.RequiredFields)
		}
		if v.Validatable {
			d.dispatch(od.JSONName, namedTypeName(v.Type))
		}
	}
}

func (l *ledgerRun) aliasClaims(d *ledgerDefInfo, ad *AliasDef) {
	group := l.group(d)
	if !ad.AcceptNonMatching {
		l.goTypeClaims(d, group, ad.Underlying)
	}
	if !ad.CanHaveMethods() {
		// No Validate is emitted for a type Go gives no methods, so nothing
		// below reaches a value. The Go type above is all it enforces.
		return
	}
	d.dispatch("", ad.ValidateAs)
	l.rulesClaims(d, ad.Validations, rulesAlias)
	l.variantClaims(d, ad.AnyOfVariants, "anyOf", rulesAliasVariant, 0)
	l.variantClaims(d, ad.OneOfVariants, "oneOf", rulesAliasVariant, 0)
	for i := range ad.TupleItems {
		l.tupleItemClaims(d, &ad.TupleItems[i], "")
	}
	if ad.TupleTail != nil {
		l.tupleItemClaims(d, ad.TupleTail, "")
	}
	for i := range ad.ItemValidations {
		l.itemValidationClaims(d, &ad.ItemValidations[i])
	}
	if ad.Contains != nil {
		l.containsClaims(d, l.containsOwner(ad.Contains), ad.Contains, ad.MinContains, ad.MaxContains, "")
	}
	if ad.UnevaluatedItems != nil {
		l.unevalItemsClaims(d, ad.UnevaluatedItems)
	}
	// An array alias holds its elements as a Go slice whose element type
	// decodes only some kinds; that is a claim on `items`.
	if at, ok := underlyingArray(ad.Underlying); ok {
		for _, n := range group {
			if n.Items != nil && n.Items.Schema != nil {
				l.goTypeClaims(d, l.withReach([]*schema.Schema{n.Items.Schema}), at.ItemType)
				d.dispatch("", namedTypeName(at.ItemType))
			}
		}
	}
	if mt, ok := underlyingMap(ad.Underlying); ok {
		for _, n := range group {
			if n.AdditionalProperties != nil && n.AdditionalProperties.Schema != nil {
				l.goTypeClaims(d, l.withReach([]*schema.Schema{n.AdditionalProperties.Schema}), mt.ValueType)
				d.dispatch("", namedTypeName(mt.ValueType))
			}
		}
	}
}

func underlyingArray(t GoType) (*ArrayType, bool) {
	if pt, ok := t.(*PointerType); ok {
		t = pt.Inner
	}
	at, ok := t.(*ArrayType)
	return at, ok
}

func underlyingMap(t GoType) (*MapType, bool) {
	if pt, ok := t.(*PointerType); ok {
		t = pt.Inner
	}
	mt, ok := t.(*MapType)
	return mt, ok
}

func (l *ledgerRun) inferredAliasClaims(d *ledgerDefInfo, ia *InferredAliasDef) {
	group := l.group(d)
	// The wrapper holds a value of any other kind as it came and validates
	// nothing about it; null too, unless it refuses null outright.
	runsFor := kindsOfType(ia.InferredJSONType)
	skips := kindAll &^ runsFor
	if ia.NeedsNullCheck {
		skips &^= kindNull
	}
	// ValidateAs is handed only the value the wrapper decoded, so the type
	// it names answers for its node only for the kinds that decode.
	d.dispatchPartly("", ia.ValidateAs, skips)
	l.scopedRulesClaims(d, ia.Validations, rulesInferred, kindAll&^skips, nil)
	l.variantClaims(d, ia.AnyOfVariants, "anyOf", rulesInferredVariant, skips)
	l.variantClaims(d, ia.OneOfVariants, "oneOf", rulesInferredVariant, skips)
	for _, n := range group {
		if n.IsBooleanSchema() {
			continue
		}
		if n.Items != nil && n.Items.Schema != nil {
			item := n.Items.Schema
			if ia.ItemsFalse {
				l.claimWhole(d, item, claimStatic)
				l.claim(d, n, "items", claimStatic)
			}
			if ia.ItemsType != "" {
				l.typeClaims(d, l.withReach([]*schema.Schema{item}), kindsOfType(ia.ItemsType))
			}
			if ia.ItemsTypeName != "" {
				d.dispatch("", ia.ItemsTypeName)
			}
			if ia.ItemsNested != nil && item.Items != nil && item.Items.Schema != nil {
				l.typeClaims(d, l.withReach([]*schema.Schema{item.Items.Schema}), kindsOfType(ia.ItemsNested.ItemsType))
			}
		}
		tail := ""
		if n.AdditionalItems != nil {
			tail = "additionalItems"
		} else if len(n.PrefixItems) > 0 && n.Items != nil && n.Items.Schema != nil {
			tail = "items"
		}
		if tail != "" {
			if ia.AdditionalItemsFalse {
				l.claim(d, n, tail, claimStatic)
				if tail == "items" {
					l.claimWhole(d, n.Items.Schema, claimStatic)
				}
			}
			var tailNode *schema.Schema
			if tail == "additionalItems" {
				tailNode = n.AdditionalItems.Schema
			} else {
				tailNode = n.Items.Schema
			}
			if tailNode != nil && ia.AdditionalItemsType != "" {
				l.typeClaims(d, l.withReach([]*schema.Schema{tailNode}), kindsOfType(ia.AdditionalItemsType))
			}
		}
	}
	for _, c := range renderedChecks(ia.ItemsChecks, checksInferredItems) {
		l.claim(d, c.Claim.Source, c.Claim.Keyword, claimStatic)
	}
	d.dispatch("", ia.ItemsTypeName)
	d.dispatch("", ia.AdditionalItemsTypeName)
	for i := range ia.TupleItems {
		ti := &ia.TupleItems[i]
		pos := ti.Claim.Source
		if pos == nil {
			continue
		}
		switch {
		case ti.IsFalse:
			l.claimWhole(d, pos, claimStatic)
		case ti.TypeName != "":
			d.dispatch("", ti.TypeName)
		case ti.JSONType != "":
			l.typeClaims(d, l.withReach([]*schema.Schema{pos}), kindsOfType(ti.JSONType))
		}
	}
	if ia.Contains != nil {
		l.containsClaims(d, l.containsOwner(ia.Contains), ia.Contains, ia.MinContains, ia.MaxContains, "")
	}
	if ia.UnevaluatedItems != nil {
		// Where contains decides which items it evaluates, this wrapper reads
		// the contains checks through a shorter list than contains_check does;
		// an item a check left out would be marked evaluated although contains
		// does not match it, and escape unevaluatedItems.
		if !ia.UnevaluatedItems.ContainsEvaluates || ia.Contains == nil ||
			len(renderedChecks(ia.Contains.Checks, checksInferredContainsEval)) == len(ia.Contains.Checks) {
			l.unevalItemsClaims(d, ia.UnevaluatedItems)
		}
	}
}

func (l *ledgerRun) enumClaims(d *ledgerDefInfo, ed *EnumDef) {
	values := make([]any, 0, len(ed.Values))
	for _, ev := range ed.Values {
		if ed.IsRaw && ev.RawJSON != "" {
			var v any
			if err := jsonUnmarshalNumber([]byte(ev.RawJSON), &v); err == nil {
				values = append(values, v)
				continue
			}
		}
		values = append(values, ev.Value)
	}
	l.valueClaims(d, l.group(d), values)
}

func (l *ledgerRun) notClaims(d *ledgerDefInfo, nd *NotSchemaDef) {
	root := l.scopeRoot(d)
	if nd.IsForbidden {
		// No instance is accepted, so every keyword in the scope is vacuous.
		for _, n := range l.inPlaceReach(root) {
			l.claimWhole(d, n, claimVacuous)
		}
		d.setWhole(root, claimVacuous)
		d.forbidsAll = true
		return
	}
	var operands []*schema.Schema
	for _, n := range l.group(d) {
		if n.Not != nil {
			operands = append(operands, n.Not)
		}
	}
	if len(nd.NotTypes) > 0 {
		for _, n := range l.group(d) {
			if n.Not != nil {
				l.claim(d, n, "not", claimStatic)
			}
		}
		for _, op := range operands {
			if len(op.Type) > 0 && stringSubset(op.Type, nd.NotTypes) && stringSubset(nd.NotTypes, op.Type) {
				l.claim(d, op, "type", claimStatic)
			}
		}
	}
	if len(nd.NotBranches) > 0 {
		for _, n := range l.group(d) {
			if n.Not != nil {
				l.claim(d, n, "not", claimStatic)
			}
		}
		for _, op := range operands {
			if len(op.AnyOf) > 0 {
				l.claim(d, op, "anyOf", claimStatic)
			}
		}
		for i := range nd.NotBranches {
			nb := &nd.NotBranches[i]
			branch := nb.Claim.Source
			if branch == nil {
				continue
			}
			if len(nb.Types) > 0 {
				l.claim(d, branch, "type", claimStatic)
			}
			l.rulesClaims(d, nb.Validations, rulesNotBranch)
			if len(nb.Properties) > 0 {
				for _, p := range nb.Properties {
					if child := branch.Properties[p.Name]; child != nil {
						l.claim(d, child, "type", claimStatic)
					}
				}
			}
		}
	}
}

func (l *ledgerRun) dynamicSchemaClaims(d *ledgerDefInfo, dd *DynamicSchemaDef) {
	root := l.scopeRoot(d)
	claimChecks := func(checks []DynamicCheck) {
		for _, c := range checks {
			l.claim(d, c.Claim.Source, c.Claim.Keyword, claimStatic)
		}
	}
	if len(dd.OneOf) > 0 {
		l.claim(d, root, "oneOf", claimStatic)
		for _, b := range dd.OneOf {
			claimChecks(b)
		}
	}
	if len(dd.AnyOf) > 0 {
		l.claim(d, root, "anyOf", claimStatic)
		for _, b := range dd.AnyOf {
			claimChecks(b)
		}
	}
	if dd.HasIfThenElse {
		l.claim(d, root, "if", claimStatic)
		claimChecks(dd.If)
		claimChecks(dd.Then)
		claimChecks(dd.Else)
	}
}

func (l *ledgerRun) typeOnlyClaims(d *ledgerDefInfo, td *TypeOnlySchemaDef) {
	group := l.group(d)
	var kinds jsonKinds
	for _, t := range td.AllowedTypes {
		kinds |= kindsOfType(t)
	}
	for i := range td.TypeBranches {
		b := &td.TypeBranches[i]
		d.dispatch("", b.TypeName)
		if b.TypeName != "" {
			kinds |= l.goKinds(&NamedType{Name: b.TypeName}, 0)
		}
		for _, t := range b.AllowedTypes {
			kinds |= kindsOfType(t)
		}
		branch := b.Claim.Source
		if branch == nil {
			continue
		}
		if len(b.AllowedTypes) > 0 {
			l.claim(d, branch, "type", claimStatic)
		}
		var required []string
		for _, p := range b.Properties {
			if child := branch.Properties[p.Name]; child != nil {
				l.claim(d, child, "type", claimStatic)
			}
			if p.Required {
				required = append(required, p.Name)
			}
		}
		l.requiredClaims(d, []*schema.Schema{branch}, required)
		// A branch standing for one alternative of the node's own type union
		// or of its anyOf is the node's keyword judged branch by branch.
		for _, n := range group {
			for _, alt := range n.AnyOf {
				if l.canonical(alt) == l.canonical(branch) {
					l.claim(d, n, "anyOf", claimStatic)
				}
			}
		}
	}
	if kinds != 0 {
		l.typeClaims(d, group, kinds)
	}
}

// ---------------------------------------------------------------------------
// Per element.
// ---------------------------------------------------------------------------

// containsOwner is the node whose `contains` a ContainsDef was read from.
func (l *ledgerRun) containsOwner(cd *ContainsDef) *schema.Schema {
	if cd == nil || cd.Claim.Source == nil {
		return nil
	}
	if p, ok := l.parentOf(l.canonical(cd.Claim.Source)); ok && p.keyword == "contains" {
		return p.node
	}
	return nil
}

// containsClaims credits a contains check on owner, the node stating it: the
// existence the keyword asserts, its sub-schema as far as the check reads it,
// and the two bounds.
func (l *ledgerRun) containsClaims(d *ledgerDefInfo, owner *schema.Schema, cd *ContainsDef, minC, maxC *CountBound, prop string) {
	if cd == nil {
		return
	}
	child := cd.Claim.Source
	if owner != nil {
		l.claim(d, owner, "contains", claimStatic)
		if minC != nil && owner.MinContains != nil && minC.N >= owner.MinContains.Int() {
			l.claim(d, owner, "minContains", claimStatic)
		}
		if maxC != nil && owner.MaxContains != nil && maxC.N <= owner.MaxContains.Int() {
			l.claim(d, owner, "maxContains", claimStatic)
		}
	}
	if child == nil {
		return
	}
	switch {
	case cd.IsFalse:
		// No element can match, so every array is refused wherever the
		// check runs: nothing the sub-schema states is left to reject.
		l.claimWhole(d, child, claimStatic)
	case cd.IsTrue:
		// The check matches every element. That carries the sub-schema only
		// where it asserts nothing, which the scope walk judges from what the
		// sub-schema states -- not from the generator's reading that it holds
		// for every value, which is the reading to check.
	case cd.ConstJSON != "":
		l.claim(d, child, "const", claimStatic)
		l.claim(d, child, "enum", claimStatic)
	case len(cd.EnumJSON) > 0:
		l.claim(d, child, "enum", claimStatic)
	}
	for _, c := range renderedChecks(cd.Checks, checksContains) {
		l.claim(d, c.Claim.Source, c.Claim.Keyword, claimStatic)
	}
	d.dispatch(prop, cd.TypeName)
}

func (l *ledgerRun) tupleItemClaims(d *ledgerDefInfo, ti *TupleItemDef, prop string) {
	pos := ti.Claim.Source
	if pos == nil {
		return
	}
	switch {
	case ti.IsFalse:
		l.claimWhole(d, pos, claimStatic)
		if p, ok := l.parentOf(l.canonical(pos)); ok {
			l.claim(d, p.node, p.keyword, claimStatic)
		}
	case ti.TypeName != "":
		d.dispatch(prop, ti.TypeName)
	case ti.JSONType != "":
		l.typeClaims(d, l.withReach([]*schema.Schema{pos}), kindsOfType(ti.JSONType))
	}
}

func (l *ledgerRun) itemValidationClaims(d *ledgerDefInfo, iv *ItemValidationDef) {
	prop := iv.JSONName
	for i := range iv.Levels {
		lv := &iv.Levels[i]
		elem := lv.Claim.Source
		if lv.CallValidate {
			d.dispatch(prop, lv.ElemTypeName)
		}
		if elem != nil {
			l.goTypeClaims(d, l.withReach([]*schema.Schema{elem}), lv.ElemType)
		}
		l.rulesClaims(d, lv.Rules, rulesItemLevel)
		if lv.Contains != nil {
			owner := elem
			l.containsClaims(d, owner, lv.Contains, lv.MinContains, lv.MaxContains, prop)
		}
		for j := range lv.TupleItems {
			l.tupleItemClaims(d, &lv.TupleItems[j], prop)
		}
		if lv.TupleTail != nil {
			l.tupleItemClaims(d, lv.TupleTail, prop)
		}
		if lv.UnevalItems != nil {
			l.unevalItemsClaims(d, lv.UnevalItems)
		}
	}
}

func (l *ledgerRun) unevalItemsClaims(d *ledgerDefInfo, ud *UnevaluatedItemsDef) {
	if ud == nil || ud.Claim.Source == nil {
		return
	}
	owner := ud.Claim.Source
	ui := owner.UnevaluatedItems
	if ud.IsAllowed && (ui == nil || !ui.IsTrueSchema()) {
		return
	}
	l.claim(d, owner, "unevaluatedItems", claimStatic)
	if ui == nil {
		return
	}
	if ud.IsForbidden {
		l.claimWhole(d, ui, claimStatic)
		return
	}
	if ud.ValueType != "" {
		l.typeClaims(d, l.withReach([]*schema.Schema{ui}), kindsOfType(ud.ValueType))
	}
	for _, c := range renderedChecks(ud.Checks, checksUnevalItems) {
		l.claim(d, c.Claim.Source, c.Claim.Keyword, claimStatic)
	}
}

func (l *ledgerRun) unevalPropertiesClaims(d *ledgerDefInfo, up *UnevaluatedPropertiesDef) {
	owner := up.Claim.Source
	if owner == nil {
		return
	}
	uneval := owner.UnevaluatedProperties
	if up.IsAllowed && (uneval == nil || !uneval.IsTrueSchema()) {
		// Permitted wholesale although the sub-schema says something: nothing
		// here carries it.
		return
	}
	l.claim(d, owner, "unevaluatedProperties", claimStatic)
	if uneval == nil {
		return
	}
	switch {
	case up.IsForbidden:
		l.claimWhole(d, uneval, claimStatic)
	case up.ValueIsNull:
		l.claim(d, uneval, "type", claimStatic)
	case up.ValueType != "":
		if k := goPrimitiveKinds(up.ValueType); k != 0 {
			l.typeClaims(d, l.withReach([]*schema.Schema{uneval}), k)
		}
	}
	l.rulesClaims(d, up.Validations, rulesUnevalProperties)
}

// goPrimitiveKinds is goKinds for a primitive Go type named by its spelling.
func goPrimitiveKinds(name string) jsonKinds {
	switch name {
	case "string":
		return kindString
	case "int64":
		return kindInteger
	case "float64", GoNumberTypeName:
		return kindNumeric
	case "bool":
		return kindBoolean
	}
	return 0
}

func (l *ledgerRun) dependentRequiredClaim(d *ledgerDefInfo, dr DependentRequiredDef) {
	owner := dr.Claim.Source
	if owner == nil {
		return
	}
	d.dependentRequired = append(d.dependentRequired, dr)
	for _, n := range append([]*schema.Schema{owner}, l.authors(owner)...) {
		if len(n.DependentRequired) == 0 {
			continue
		}
		covered := true
		for _, trigger := range sortedKeys(n.DependentRequired) {
			need := n.DependentRequired[trigger]
			if len(need) == 0 {
				continue
			}
			found := false
			for _, have := range d.dependentRequired {
				if have.TriggerKey == trigger && stringSubset(need, have.Required) {
					found = true
					break
				}
			}
			if !found {
				covered = false
				break
			}
		}
		if covered {
			d.setPool(ledgerKey{n, "dependentRequired"}, claimStatic)
		}
	}
}

func (l *ledgerRun) dependentSchemaClaims(d *ledgerDefInfo, dc *DependentSchemaConstraint) {
	owner := dc.Claim.Source
	if owner == nil {
		return
	}
	l.claim(d, owner, "dependentSchemas", claimStatic)
	dep := owner.DependentSchemas[dc.TriggerKey]
	if dep == nil {
		return
	}
	if dc.IsFalse {
		l.claimWhole(d, dep, claimStatic)
		return
	}
	if len(dc.AllowedKeys) > 0 {
		l.claim(d, dep, "additionalProperties", claimStatic)
	}
	if len(dc.RequiredProps) > 0 {
		l.requiredClaims(d, []*schema.Schema{dep}, dc.RequiredProps)
	}
	if dc.MinProperties != nil {
		l.claim(d, dep, "minProperties", claimStatic)
	}
	if dc.MaxProperties != nil {
		l.claim(d, dep, "maxProperties", claimStatic)
	}
	if dc.Branch != nil {
		l.conditionalBranchClaims(d, dc.Branch)
	}
}

func (l *ledgerRun) propertyNamesClaims(d *ledgerDefInfo, pn *PropertyNamesDef) {
	child := pn.Claim.Source
	if child == nil {
		return
	}
	if pn.IsForbidden {
		l.claimWhole(d, child, claimStatic)
		return
	}
	if pn.MaxLength != nil {
		l.claim(d, child, "maxLength", claimStatic)
	}
	if pn.MinLength != nil {
		l.claim(d, child, "minLength", claimStatic)
	}
	if pn.Pattern != "" {
		l.claim(d, child, "pattern", claimStatic)
	}
	if len(pn.Enum) > 0 {
		l.claim(d, child, "enum", claimStatic)
		l.claim(d, child, "const", claimStatic)
	}
	if pn.Format != "" {
		l.claim(d, child, "format", claimStatic)
	}
	// A property name is a string, so a type admitting strings is satisfied
	// by every name.
	if len(child.Type) > 0 && kindsOfTypeList(child.Type)&kindString != 0 {
		l.claim(d, child, "type", claimVacuous)
	}
}

// objectBranchGroupClaims credits an object-level anyOf or oneOf judged by
// required keys and property checks.
func (l *ledgerRun) objectBranchGroupClaims(d *ledgerDefInfo, group Provenance, branches []ObjectOneOfBranch) {
	l.provenanceClaims(d, group)
	for i := range branches {
		b := &branches[i]
		if b.Claim.Source == nil {
			continue
		}
		reach := l.withReach([]*schema.Schema{b.Claim.Source})
		l.requiredClaims(d, reach, b.RequiredKeys)
		for _, c := range b.Checks {
			l.objectPropertyCheckClaims(d, &c)
		}
	}
}

func (l *ledgerRun) objectPropertyCheckClaims(d *ledgerDefInfo, c *ObjectPropertyCheck) {
	prop := c.Claim.Source
	if prop == nil {
		return
	}
	if c.JSONType != "" {
		l.claim(d, prop, "type", claimStatic)
	}
	if len(c.AllowedValues) > 0 {
		l.claim(d, prop, "enum", claimStatic)
		l.claim(d, prop, "const", claimStatic)
	}
}

func (l *ledgerRun) conditionalBranchClaims(d *ledgerDefInfo, b *ObjectConditionalBranch) {
	node := b.Claim.Source
	if node == nil {
		return
	}
	l.requiredClaims(d, []*schema.Schema{node}, b.RequiredKeys)
	for _, pc := range b.Properties {
		for _, c := range pc.Checks {
			l.claim(d, c.Claim.Source, c.Claim.Keyword, claimStatic)
		}
	}
}
