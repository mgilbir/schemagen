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
	DynamicConst       bool // _dynConstOK, only reached by object-level conditionals
	Annotations        bool // _schemaNode and the runtime annotation evaluator
}

// Empty reports whether no helpers are needed at all.
func (h HelperSet) Empty() bool {
	return !h.OneOf && !h.OneOfDiscriminator && !h.Dynamic && !h.DynamicConst && !h.Annotations
}

// Merge folds another set into this one.
func (h *HelperSet) Merge(other HelperSet) {
	h.OneOf = h.OneOf || other.OneOf
	h.OneOfDiscriminator = h.OneOfDiscriminator || other.OneOfDiscriminator
	h.Dynamic = h.Dynamic || other.Dynamic
	h.DynamicConst = h.DynamicConst || other.DynamicConst
	h.Annotations = h.Annotations || other.Annotations
}

// Helpers reports which shared helpers this file's generated code calls.
func (f *File) Helpers() HelperSet {
	var set HelperSet
	for _, td := range f.TypeDefs {
		switch d := td.(type) {
		case *DynamicSchemaDef:
			set.Dynamic = true
		case *AnnotationSchemaDef:
			// The evaluator calls the _dyn* predicates.
			set.Annotations = true
			set.Dynamic = true
		case *StructDef:
			// Object-level if/then/else checks call the _dyn* predicates against
			// each property's decoded value.
			if d.HasObjectConditionals() {
				set.Dynamic = true
				if d.anyConditionalCheck(func(c DynamicCheck) bool { return c.Kind == "const" }) {
					set.DynamicConst = true
				}
			}
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
