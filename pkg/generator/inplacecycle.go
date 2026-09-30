package generator

// resolveTypeBranchesInPlace marks every schema-valued type branch whose type
// is itself a schema-valued type wrapper of this package. See
// TypeSchemaBranch.InPlaceType.
//
// A draft 3 "type" entry that is a schema is judged against the very value the
// wrapper holds: the value is decoded into the entry's type and handed to that
// type's Validate. When the entry's type is another such wrapper, and its own
// entries lead back, the judgement never gets any further into the value --
// {"$defs":{"C":{"type":[{"$ref":"#/$defs/C"}]}}} asks whether a value is a C
// by asking whether it is a C -- and the generated Validate called itself until
// the goroutine's stack ran out, on any document at all.
//
// JSON Schema leaves such a schema's meaning undefined, and a validator must not
// loop on it. The reading taken here is the least one: a branch that re-enters a
// wrapper already judging this same value contributes nothing, and the value is
// held to the rest of the alternatives. For {"type":["string",{"$ref":"#/$defs/C"}]}
// that is exactly the strings, which is what every finite unfolding of the
// definition admits. Recursion that descends into the value -- a property, an
// element -- starts a fresh judgement at each level, so the work is bounded by
// the depth of the document.
func (g *Generator) resolveTypeBranchesInPlace() {
	for _, td := range g.output.TypeDefs {
		d, ok := td.(*TypeOnlySchemaDef)
		if !ok {
			continue
		}
		for i := range d.TypeBranches {
			b := &d.TypeBranches[i]
			if b.TypeName == "" {
				continue
			}
			end := g.aliasEndOf(&NamedType{Name: b.TypeName})
			if wrapper, ok := end.Def.(*TypeOnlySchemaDef); ok {
				b.InPlaceType = wrapper.Name
				wrapper.VisitTarget = true
			}
		}
	}
}

// HasInPlaceBranch reports whether some branch of the wrapper is judged through
// another wrapper's validateVisiting.
func (d *TypeOnlySchemaDef) HasInPlaceBranch() bool {
	for _, b := range d.TypeBranches {
		if b.InPlaceType != "" {
			return true
		}
	}
	return false
}
