package generator

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
}

// Empty reports whether no helpers are needed at all.
func (h HelperSet) Empty() bool {
	return !h.OneOf && !h.OneOfDiscriminator && !h.Dynamic
}

// Merge folds another set into this one.
func (h *HelperSet) Merge(other HelperSet) {
	h.OneOf = h.OneOf || other.OneOf
	h.OneOfDiscriminator = h.OneOfDiscriminator || other.OneOfDiscriminator
	h.Dynamic = h.Dynamic || other.Dynamic
}

// Helpers reports which shared helpers this file's generated code calls.
func (f *File) Helpers() HelperSet {
	var set HelperSet
	for _, td := range f.TypeDefs {
		switch d := td.(type) {
		case *DynamicSchemaDef:
			set.Dynamic = true
		case *StructDef:
			if len(d.OneOfs) > 0 {
				set.OneOf = true
			}
			for _, o := range d.OneOfs {
				if o.HasDiscriminator() {
					set.OneOfDiscriminator = true
				}
			}
		}
	}
	return set
}
