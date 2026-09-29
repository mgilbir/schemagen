package generator

import "strings"

// This file plans how a check that compares values -- uniqueItems, a const --
// reads the identity of the values it compares: the counterpart of
// encodeplan.go for jsonID, the identity the emitted helpers define.
//
// Those checks used to marshal each value and compare the text. A value of this
// package's types was written out member by member into a buffer, subtree and
// all, only to be compared and thrown away; an array of objects with uniqueItems
// wrote each object out, the same check one level down wrote the same subtrees
// out again, and a document whose arrays nest d deep was written d times over
// to be judged once -- 0.8 to 3 ms per CycloneDX BOM went there. Now a value's
// identity is read off the value as it is held: a struct's jsonIdentity reads
// its members in the order and by the rules its appendJSON writes them (see
// struct_identity), a type with a MarshalJSON of its own reads what that writes,
// and every other value is read from its Go kind by jsonIdentifyAt. The
// identities of the elements of an array whose uniqueItems is checked are kept
// in the jsonValidation one Validate shares with the values below it, so the
// check of the array beneath reads what the check above computed, and each
// element's identity is computed once.

// resolveIdentityPlans settles which types read their own identity and how
// every position that compares values reads one. It runs after
// resolveEncodePlans: a struct's identity reads its members in the order and by
// the rules its encode writes them.
func (g *Generator) resolveIdentityPlans() {
	// Which uniqueItems checks keep their elements' identities: those whose
	// elements are more than a scalar, where computing an identity costs the
	// element's size and a check below would compute it again.
	for _, td := range g.output.TypeDefs {
		switch d := td.(type) {
		case *StructDef:
			for i := range d.Validations {
				r := &d.Validations[i]
				if r.RuleType != "uniqueItems" {
					continue
				}
				if f := structField(d, r.FieldName); f != nil {
					r.KeepIDs = !g.identityIsScalar(g.itemType(f.Type))
				}
			}
			for i := range d.ItemValidations {
				g.markItemLevelKeeps(d.ItemValidations[i].Levels)
			}
		case *AliasDef:
			if elem := g.aliasItemType(d); elem != nil {
				for i := range d.Validations {
					if r := &d.Validations[i]; r.RuleType == "uniqueItems" {
						r.KeepIDs = !g.identityIsScalar(elem)
					}
				}
			}
			for i := range d.ItemValidations {
				g.markItemLevelKeeps(d.ItemValidations[i].Levels)
			}
		}
	}

	need := g.identityReach()
	for _, td := range g.output.TypeDefs {
		switch d := td.(type) {
		case *StructDef:
			d.HasIdentity = need[d.Name]
		case *AliasDef:
			d.HasIdentity = need[d.Name] && d.CanHaveMethods() && (d.EncodeTo || d.MarshalAs != "" || aliasKeepsIDs(d))
		case *EnumDef:
			d.HasIdentity = need[d.Name] && (d.IsRaw || d.IsNumberBase())
		case *BigIntAliasDef:
			d.HasIdentity = need[d.Name]
		case *InferredAliasDef:
			d.HasIdentity = need[d.Name]
		case *AnnotationSchemaDef:
			d.HasIdentity = need[d.Name]
		case *TypeOnlySchemaDef:
			d.HasIdentity = need[d.Name]
		case *DynamicSchemaDef:
			d.HasIdentity = need[d.Name]
		case *NotSchemaDef:
			d.HasIdentity = need[d.Name]
		}
	}

	// An alias's own reading first: one whose shape turns out to hold nothing
	// to read structurally reads no identity of its own after all, and the
	// positions that hold it read it by its Go kind.
	for _, td := range g.output.TypeDefs {
		d, ok := td.(*AliasDef)
		if !ok || !d.HasIdentity {
			continue
		}
		if d.MarshalAs != "" {
			// Read as the value MarshalJSON converts it to and writes: a
			// pointer conversion, since the two share one underlying type.
			d.Identifier = g.jsonIdentifier(&NamedType{Name: d.MarshalAs}, false)
		} else if d.Identifier = g.structuralIdentifier(&NamedType{Name: d.Name}, d.Underlying, aliasKeepsIDs(d)); d.Identifier == "" {
			d.HasIdentity = false
		}
	}

	// The expressions, now that every declaration says whether it reads its own
	// identity.
	for _, td := range g.output.TypeDefs {
		switch d := td.(type) {
		case *StructDef:
			keeps := make(map[string]bool)
			for i := range d.Validations {
				r := &d.Validations[i]
				switch r.RuleType {
				case "uniqueItems":
					if f := structField(d, r.FieldName); f != nil {
						r.Identifier = g.elemIdentifier(f.Type)
						if r.KeepIDs {
							keeps[f.Name] = true
						}
					}
				case "const":
					if f := structField(d, r.FieldName); f != nil && !r.ExactCompare {
						r.Identifier = g.jsonIdentifier(f.Type, false)
					}
				}
			}
			for i := range d.ItemValidations {
				g.resolveItemLevelIdentifiers(d.ItemValidations[i].Levels)
			}
			if !d.HasIdentity {
				continue
			}
			for i := range d.Fields {
				f := &d.Fields[i]
				f.Identifier = g.jsonIdentifier(f.Type, keeps[f.Name])
			}
			for i := range d.OneOfs {
				for j := range d.OneOfs[i].Variants {
					v := &d.OneOfs[i].Variants[j]
					v.Identifier = g.jsonIdentifier(v.Type, false)
				}
			}
			if ap := d.AdditionalProperties; ap != nil {
				ap.ValueIdentifier = g.jsonIdentifier(ap.ValueType, false)
			}
		case *AliasDef:
			if elem := g.aliasItemType(d); elem != nil {
				for i := range d.Validations {
					if r := &d.Validations[i]; r.RuleType == "uniqueItems" {
						r.Identifier = g.jsonIdentifier(elem, false)
					}
				}
			}
			for i := range d.ItemValidations {
				g.resolveItemLevelIdentifiers(d.ItemValidations[i].Levels)
			}
		}
	}

	g.resolveValidateIn()
}

// markItemLevelKeeps sets KeepIDs on the uniqueItems rules of an element that
// is itself an array, where that array's elements are more than a scalar.
func (g *Generator) markItemLevelKeeps(levels []ItemLevel) {
	for i := range levels {
		lv := &levels[i]
		for j := range lv.Rules {
			if r := &lv.Rules[j]; r.RuleType == "uniqueItems" {
				r.KeepIDs = !g.identityIsScalar(g.itemType(lv.ElemType))
			}
		}
	}
}

func (g *Generator) resolveItemLevelIdentifiers(levels []ItemLevel) {
	for i := range levels {
		lv := &levels[i]
		for j := range lv.Rules {
			switch r := &lv.Rules[j]; r.RuleType {
			case "uniqueItems":
				r.Identifier = g.elemIdentifier(lv.ElemType)
			case "const":
				if !r.ExactCompare {
					r.Identifier = g.jsonIdentifier(lv.ElemType, false)
				}
			}
		}
	}
}

// aliasKeepsIDs reports whether an alias over an array checks uniqueItems over
// elements whose identities are kept: its identity keeps them too, so that the
// check reads them back.
func aliasKeepsIDs(d *AliasDef) bool {
	for i := range d.Validations {
		if r := &d.Validations[i]; r.RuleType == "uniqueItems" && r.KeepIDs {
			return true
		}
	}
	return false
}

// aliasItemType is the element type of the array an alias stands for, or nil
// where it stands for something else.
func (g *Generator) aliasItemType(d *AliasDef) GoType {
	end := g.aliasEndOf(d.Underlying)
	if at, ok := end.Go.(*ArrayType); ok {
		return at.ItemType
	}
	return nil
}

func structField(d *StructDef, name string) *FieldDef {
	for i := range d.Fields {
		if d.Fields[i].Name == name {
			return &d.Fields[i]
		}
	}
	return nil
}

// itemType is the element type of a slice, or of a pointer to one, through any
// chain of names; nil for anything else.
func (g *Generator) itemType(t GoType) GoType {
	if p, ok := t.(*PointerType); ok {
		t = p.Inner
	}
	if at, ok := g.aliasEndOf(t).Go.(*ArrayType); ok {
		return at.ItemType
	}
	return nil
}

// elemIdentifier is jsonIdentifier for the elements of a slice of type t, and
// the generic jsonIdentifyAt, inferred from the slice, where t's elements
// cannot be named.
func (g *Generator) elemIdentifier(t GoType) string {
	if elem := g.itemType(t); elem != nil {
		return g.jsonIdentifier(elem, false)
	}
	return "jsonIdentifyAt"
}

// identityIsScalar reports whether reading a value of t's identity costs a
// constant: a string, a number, a boolean, or a name over one. Anything else --
// a struct, a container, an any, raw JSON -- costs its size.
func (g *Generator) identityIsScalar(t GoType) bool {
	if t == nil {
		return false
	}
	end := g.aliasEndOf(t)
	switch {
	case end.Go != nil:
		if p, ok := end.Go.(*PrimitiveType); ok {
			switch p.Name {
			case "any", GoRawTypeName:
				return false
			}
			return true
		}
	case end.Def != nil:
		if e, ok := end.Def.(*EnumDef); ok {
			return !e.IsRaw
		}
	}
	return false
}

// identityReach names every type of this file whose identity a check that
// compares values can ask for: the types of the values those checks compare,
// and everything those types hold.
func (g *Generator) identityReach() map[string]bool {
	need := make(map[string]bool)
	var visit func(t GoType)
	visit = func(t GoType) {
		switch v := t.(type) {
		case *PointerType:
			visit(v.Inner)
		case *ArrayType:
			visit(v.ItemType)
		case *MapType:
			visit(v.ValueType)
		case *NamedType:
			if v.PkgAlias != "" || strings.Contains(v.Name, ".") || need[v.Name] {
				return
			}
			td := g.localTypeDef(v.Name)
			if td == nil {
				return
			}
			need[v.Name] = true
			switch d := td.(type) {
			case *StructDef:
				for i := range d.Fields {
					visit(d.Fields[i].Type)
				}
				for i := range d.OneOfs {
					for j := range d.OneOfs[i].Variants {
						visit(d.OneOfs[i].Variants[j].Type)
					}
				}
				if d.AdditionalProperties != nil {
					visit(d.AdditionalProperties.ValueType)
				}
			case *AliasDef:
				visit(d.Underlying)
				if d.MarshalAs != "" {
					visit(&NamedType{Name: d.MarshalAs})
				}
			}
		}
	}
	// The type an element held as decoded JSON is judged against (see
	// ElementNode) is the type a caller building the value in Go puts there, and
	// the evaluator reads such an element as a tree -- by the type's identity
	// functions, reading trees.
	for _, n := range g.output.ElementNodes {
		visit(&NamedType{Name: n.TypeName})
	}
	for _, td := range g.output.TypeDefs {
		switch d := td.(type) {
		case *StructDef:
			for i := range d.Validations {
				r := &d.Validations[i]
				f := structField(d, r.FieldName)
				if f == nil {
					continue
				}
				switch {
				case r.RuleType == "uniqueItems":
					visit(g.itemType(f.Type))
				case r.RuleType == "const" && !r.ExactCompare:
					visit(f.Type)
				}
			}
			// A contains const or enum compares each element by identity.
			for _, fc := range d.ContainsValidations {
				if f := structField(d, fc.FieldName); f != nil {
					visit(g.itemType(f.Type))
				}
			}
			visitItemLevels(d.ItemValidations, g, visit)
		case *AliasDef:
			for i := range d.Validations {
				if d.Validations[i].RuleType == "uniqueItems" {
					visit(g.aliasItemType(d))
				}
			}
			if d.Contains != nil {
				visit(g.aliasItemType(d))
			}
			visitItemLevels(d.ItemValidations, g, visit)
		}
	}
	return need
}

// visitItemLevels visits the values the rules at a level of a container
// compare: a uniqueItems rule the elements of the array the level holds, and a
// const the level's element itself.
func visitItemLevels(ivs []ItemValidationDef, g *Generator, visit func(GoType)) {
	for i := range ivs {
		for _, lv := range ivs[i].Levels {
			for _, r := range lv.Rules {
				switch {
				case r.RuleType == "uniqueItems":
					visit(g.itemType(lv.ElemType))
				case r.RuleType == "const" && !r.ExactCompare:
					visit(lv.ElemType)
				}
			}
			if lv.Contains != nil {
				visit(g.itemType(lv.ElemType))
			}
		}
	}
}

// localTypeDef is the declaration of name among the types this call emits: the
// only ones a method can still be added to. A type an earlier call of a
// shared-types run declared is read through jsonIdentifyAt, which finds its
// jsonIdentity where it has one.
func (g *Generator) localTypeDef(name string) TypeDef {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() == name {
			return td
		}
	}
	return nil
}

// defHasIdentity reports whether a declaration of this file reads its own
// identity.
func defHasIdentity(td TypeDef) bool {
	switch d := td.(type) {
	case *StructDef:
		return d.HasIdentity
	case *AliasDef:
		return d.HasIdentity
	case *EnumDef:
		return d.HasIdentity
	case *BigIntAliasDef:
		return d.HasIdentity
	case *InferredAliasDef:
		return d.HasIdentity
	case *AnnotationSchemaDef:
		return d.HasIdentity
	case *TypeOnlySchemaDef:
		return d.HasIdentity
	case *DynamicSchemaDef:
		return d.HasIdentity
	case *NotSchemaDef:
		return d.HasIdentity
	}
	return false
}

// jsonIdentifier is how a value of t reads its identity, as a jsonIdentify[T]
// expression. keep says the value is an array whose uniqueItems check reads its
// elements' identities back (see jsonIDSliceKept).
//
// A type that reads its own is its jsonIdentity; a pointer, a slice and a map
// are read through the helper for their shape, down to what they hold; and
// every other value through jsonIdentifyAt, which reads it from its Go kind.
func (g *Generator) jsonIdentifier(t GoType, keep bool) string {
	switch v := t.(type) {
	case *NamedType:
		if v.Pointer {
			return g.pointerIdentifier(t, &NamedType{Name: v.Name, PkgAlias: v.PkgAlias, foreign: v.foreign})
		}
		if v.PkgAlias == "" && !strings.Contains(v.Name, ".") {
			if td := g.localTypeDef(v.Name); td != nil {
				if defHasIdentity(td) {
					return "(*" + v.Name + ").jsonIdentity"
				}
				// An alias with no MarshalJSON is written by encoding/json as its
				// underlying type, and read as that.
				if ad, ok := td.(*AliasDef); ok && (!ad.CanHaveMethods() || !ad.EncodeTo && ad.MarshalAs == "") {
					if id := g.structuralIdentifier(v, ad.Underlying, keep); id != "" {
						return id
					}
				}
			}
		}
	case *PointerType:
		return g.pointerIdentifier(t, v.Inner)
	case *ArrayType:
		return g.sliceIdentifier(t, v.ItemType, keep)
	case *MapType:
		if isStringKey(v.KeyType) {
			return g.mapIdentifier(t, v.ValueType)
		}
	}
	return "jsonIdentifyAt[" + t.GoTypeName() + "]"
}

func (g *Generator) pointerIdentifier(self, inner GoType) string {
	return jsonIdentifyLiteral(self, "jsonIDPtr["+self.GoTypeName()+", "+inner.GoTypeName()+"](*_p, _m, "+g.jsonIdentifier(inner, false)+")")
}

func (g *Generator) sliceIdentifier(self, elem GoType, keep bool) string {
	helper := "jsonIDSlice"
	if keep {
		helper = "jsonIDSliceKept"
	}
	return jsonIdentifyLiteral(self, helper+"["+self.GoTypeName()+", "+elem.GoTypeName()+"](*_p, _m, "+g.jsonIdentifier(elem, false)+")")
}

func (g *Generator) mapIdentifier(self, value GoType) string {
	return jsonIdentifyLiteral(self, "jsonIDMap["+self.GoTypeName()+", "+value.GoTypeName()+"](*_p, _m, "+g.jsonIdentifier(value, false)+")")
}

// structuralIdentifier reads a value of the named type self, whose underlying
// type is underlying, by the shape underlying ends at. It answers "" where that
// shape is not a container, and the value is read by jsonIdentifyAt.
func (g *Generator) structuralIdentifier(self *NamedType, underlying GoType, keep bool) string {
	end := g.aliasEndOf(underlying)
	switch v := end.Go.(type) {
	case *PointerType:
		return g.pointerIdentifier(self, v.Inner)
	case *NamedType:
		if v.Pointer {
			return g.pointerIdentifier(self, &NamedType{Name: v.Name, PkgAlias: v.PkgAlias, foreign: v.foreign})
		}
	case *ArrayType:
		return g.sliceIdentifier(self, v.ItemType, keep)
	case *MapType:
		if isStringKey(v.KeyType) {
			return g.mapIdentifier(self, v.ValueType)
		}
	}
	if end.Def != nil && defHasIdentity(end.Def) {
		// A declaration at the end of the chain that reads its own: over the
		// value converted to it, which the shared underlying type allows.
		name := end.Def.TypeName()
		return jsonIdentifyLiteral(self, "(*"+name+")(_p).jsonIdentity(_m)")
	}
	return ""
}

// jsonIdentifyLiteral writes a jsonIdentify[T] over t as a function literal
// returning body, which reads its arguments as _p and _m.
func jsonIdentifyLiteral(t GoType, body string) string {
	return "func(_p *" + t.GoTypeName() + ", _m *jsonValidation) (jsonID, error) { return " + body + " }"
}

// resolveValidateIn settles which types' Validate shares a jsonValidation with
// the values below it: every type whose check keeps identities, and every type
// whose Validate reaches one that does. The rest keep Validate as it was.
func (g *Generator) resolveValidateIn() {
	in := make(map[string]bool)
	for _, td := range g.output.TypeDefs {
		switch d := td.(type) {
		case *StructDef:
			for _, r := range d.Validations {
				if r.RuleType == "uniqueItems" && r.KeepIDs {
					in[d.Name] = true
				}
			}
			if itemLevelsKeep(d.ItemValidations) {
				in[d.Name] = true
			}
		case *AliasDef:
			if aliasKeepsIDs(d) || itemLevelsKeep(d.ItemValidations) {
				in[d.Name] = true
			}
		}
	}
	calls := func(name string) bool {
		if name == "" || strings.Contains(name, ".") {
			return false
		}
		return in[name]
	}
	for changed := true; changed; {
		changed = false
		for _, td := range g.output.TypeDefs {
			switch d := td.(type) {
			case *StructDef:
				if in[d.Name] {
					continue
				}
				reaches := false
				for _, vf := range d.ValidatableFields {
					if calls(validatedTypeName(vf.GoType)) {
						reaches = true
					}
				}
				for _, o := range d.OneOfs {
					for _, v := range o.Variants {
						if v.Validatable && calls(validatedTypeName(v.Type)) {
							reaches = true
						}
					}
				}
				for _, iv := range d.ItemValidations {
					for _, lv := range iv.Levels {
						if lv.CallValidate && calls(lv.ElemTypeName) {
							reaches = true
						}
					}
				}
				if reaches {
					in[d.Name] = true
					changed = true
				}
			case *AliasDef:
				if in[d.Name] || !d.CanHaveMethods() {
					continue
				}
				reaches := calls(d.ValidateAs)
				for _, iv := range d.ItemValidations {
					for _, lv := range iv.Levels {
						if lv.CallValidate && calls(lv.ElemTypeName) {
							reaches = true
						}
					}
				}
				if reaches {
					in[d.Name] = true
					changed = true
				}
			}
		}
	}
	for _, td := range g.output.TypeDefs {
		switch d := td.(type) {
		case *StructDef:
			d.ValidateIn = in[d.Name]
			for i := range d.ValidatableFields {
				vf := &d.ValidatableFields[i]
				vf.ValidateIn = calls(validatedTypeName(vf.GoType))
			}
			for i := range d.OneOfs {
				for j := range d.OneOfs[i].Variants {
					v := &d.OneOfs[i].Variants[j]
					v.ValidateIn = v.Validatable && calls(validatedTypeName(v.Type))
				}
			}
			markItemLevelsIn(d.ItemValidations, calls)
		case *AliasDef:
			d.ValidateIn = in[d.Name]
			d.ValidateAsIn = calls(d.ValidateAs)
			markItemLevelsIn(d.ItemValidations, calls)
		}
	}
}

func itemLevelsKeep(ivs []ItemValidationDef) bool {
	for _, iv := range ivs {
		for _, lv := range iv.Levels {
			for _, r := range lv.Rules {
				if r.RuleType == "uniqueItems" && r.KeepIDs {
					return true
				}
			}
		}
	}
	return false
}

func markItemLevelsIn(ivs []ItemValidationDef, calls func(string) bool) {
	for i := range ivs {
		for j := range ivs[i].Levels {
			lv := &ivs[i].Levels[j]
			lv.ValidateIn = lv.CallValidate && calls(lv.ElemTypeName)
		}
	}
}

// validatedTypeName is the name of the type whose Validate a position of type t
// dispatches to: t's own name, through a pointer, a slice or a map of it.
func validatedTypeName(t GoType) string {
	for {
		switch v := t.(type) {
		case *PointerType:
			t = v.Inner
		case *ArrayType:
			t = v.ItemType
		case *MapType:
			t = v.ValueType
		case *NamedType:
			if v.PkgAlias != "" {
				return ""
			}
			return v.Name
		default:
			return ""
		}
	}
}
