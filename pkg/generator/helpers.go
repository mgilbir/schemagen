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
	Integer            bool // jsonInteger and the shape-preserving converters
}

// Empty reports whether no helpers are needed at all.
func (h HelperSet) Empty() bool {
	return !h.OneOf && !h.OneOfDiscriminator && !h.Dynamic && !h.DynamicConst && !h.Annotations && !h.Integer
}

// Merge folds another set into this one.
func (h *HelperSet) Merge(other HelperSet) {
	h.OneOf = h.OneOf || other.OneOf
	h.OneOfDiscriminator = h.OneOfDiscriminator || other.OneOfDiscriminator
	h.Dynamic = h.Dynamic || other.Dynamic
	h.DynamicConst = h.DynamicConst || other.DynamicConst
	h.Annotations = h.Annotations || other.Annotations
	h.Integer = h.Integer || other.Integer
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
		case *AliasDef:
			if d.IntegerDecode != nil {
				set.Integer = true
			}
		case *EnumDef:
			if d.IntegerToken {
				set.Integer = true
			}
		case *StructDef:
			for i := range d.Fields {
				if d.Fields[i].IntegerDecode != nil {
					set.Integer = true
				}
			}
			if d.AdditionalProperties != nil && d.AdditionalProperties.IntegerDecode != nil {
				set.Integer = true
			}
			for _, o := range d.OneOfs {
				for _, v := range o.Variants {
					if v.IntegerDecode != nil {
						set.Integer = true
					}
				}
			}
			// Object-level if/then/else checks call the _dyn* predicates against
			// each property's decoded value, and so does a dependentSchemas
			// branch: it is the same branch definition, gated on a key's
			// presence rather than on an `if`.
			if d.HasObjectConditionals() || d.HasDependentSchemaBranches() {
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
