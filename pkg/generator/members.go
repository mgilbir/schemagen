package generator

// A generated struct is written out of several sources at once. Most of its
// members come from the schema's `properties`; the rest are the union field a
// oneOf property becomes, the overflow maps additionalProperties and
// patternProperties need, and a handful of unexported members the decoder fills
// so that Validate can see what the document actually carried.
//
// Until the layout pass existed each of those was a separate loop in
// struct.go.tmpl, emitted in a fixed order. That is a fine way to write a struct
// and a poor way to lay one out: `_nonObject bool` between two maps costs seven
// bytes of padding and drags the garbage collector's scan across both. So the
// sources are collected into one list here, and the template writes that list.
// The order within it is the layout pass's to choose; see layout.go.
//
// StructDef.Fields keeps its own order -- the JSON property names, sorted --
// because everything else reads it: the decoder's member list, the validation
// order, the property names in an error. Only the declaration moves.

// StructMember is one member of a generated struct, as it will be declared.
//
// Which of the three kinds it is decides how the template writes it, and the
// kind is which of Field and OneOf is set. A property carries the whole FieldDef
// because its declaration needs the doc comment, the annotations and the JSON
// tag; a union member carries the whole OneOfDef for the same reason. Everything
// else is a member this generator writes itself, and Name, Type, Tag and Comment
// are all there is to say about it.
type StructMember struct {
	// Field is the property this member declares, for a member that is one.
	Field *FieldDef
	// OneOf is the union this member declares, for a member that is one.
	OneOf *OneOfDef

	// Name is the Go field name, and Type what it is declared as. Both are set
	// for every kind, including the two above -- Type is what the layout pass
	// measures, and it must be able to measure a member without asking which
	// kind it is.
	Type GoType
	Name string
	// Tag is the struct tag, without backquotes, or empty for a member that
	// takes none.
	Tag string
	// Comment is the line comment to write after the declaration, if any.
	Comment string
}

// IsProperty and IsUnion report the member's kind. They exist for the template,
// which cannot switch on a Go type.
func (m StructMember) IsProperty() bool { return m.Field != nil }
func (m StructMember) IsUnion() bool    { return m.OneOf != nil }

// Members lists this struct's members in the order they are declared: the order
// the layout pass chose, or, where that pass has not run, the order they are
// built in.
//
// The fallback is deliberate and is what keeps the two safe to have apart. A
// StructDef built by hand -- which is what most of the emitter's own tests do --
// has no chosen order, and answering with an empty list would silently emit a
// struct with no members at all. Answering with the built order emits exactly
// what this generator emitted before the pass existed.
func (d *StructDef) Members() []StructMember {
	members := d.builtMembers()
	if len(d.memberOrder) != len(members) {
		return members
	}
	ordered := make([]StructMember, len(members))
	for i, index := range d.memberOrder {
		ordered[i] = members[index]
	}
	return ordered
}

// builtMembers collects the struct's members from the several places they come
// from, in the order struct.go.tmpl declared them in before the layout pass
// existed.
func (d *StructDef) builtMembers() []StructMember {
	members := make([]StructMember, 0, len(d.Fields)+len(d.OneOfs)+6)

	for i := range d.Fields {
		f := &d.Fields[i]
		members = append(members, StructMember{
			Field: f,
			Name:  f.Name,
			Type:  f.Type,
		})
	}
	for i := range d.OneOfs {
		o := &d.OneOfs[i]
		members = append(members, StructMember{
			OneOf: o,
			Name:  o.FieldName,
			Type:  &InterfaceType{Name: o.InterfaceName},
			Tag:   `json:"-"`,
		})
	}
	if d.AdditionalProperties != nil {
		members = append(members, StructMember{
			Name: "AdditionalProperties",
			Type: &MapType{KeyType: &PrimitiveType{Name: "string"}, ValueType: d.AdditionalProperties.ValueType},
			Tag:  `json:"-"`,
		})
	}
	if d.HasPatternProperties() {
		members = append(members, StructMember{
			Name: "PatternProperties",
			Type: rawMessageMap(),
			Tag:  `json:"-"`,
		})
	}
	if d.AcceptNonObject {
		members = append(members,
			StructMember{
				Name:    "_nonObject",
				Type:    &PrimitiveType{Name: "bool"},
				Comment: "set by UnmarshalJSON when the JSON data is not an object",
			},
			StructMember{
				Name:    "_rawNonObject",
				Type:    &PrimitiveType{Name: "json.RawMessage"},
				Comment: "raw bytes of non-object data for lossless roundtrip",
			},
		)
	}
	if d.NeedsJSONKeys() {
		members = append(members, StructMember{
			Name:    "_jsonKeys",
			Type:    boolMap(),
			Comment: "set by UnmarshalJSON for optional field / dependentSchemas validation",
		})
	}
	if d.NeedsRawProps() {
		members = append(members, StructMember{
			Name:    "_jsonRawProps",
			Type:    rawMessageMap(),
			Comment: "set by UnmarshalJSON for runtime conditional evaluation (if/then/else, anyOf const checks)",
		})
	}
	if d.NeedsJSONNulls() {
		members = append(members, StructMember{
			Name:    "_jsonNulls",
			Type:    boolMap(),
			Comment: "set by UnmarshalJSON for the properties written as null, which the decoded value cannot hold",
		})
	}
	return members
}

func rawMessageMap() GoType {
	return &MapType{KeyType: &PrimitiveType{Name: "string"}, ValueType: &PrimitiveType{Name: "json.RawMessage"}}
}

func boolMap() GoType {
	return &MapType{KeyType: &PrimitiveType{Name: "string"}, ValueType: &PrimitiveType{Name: "bool"}}
}

// Members lists the three members of an inferred alias in declaration order.
//
// The wrapper holds the value the schema's constraints are about, the raw bytes
// of a document whose type they are not about, and the flag saying which of the
// two it is holding. Their order is the layout pass's, and it has to be, because
// the first of them has whatever type the schema inferred: `_value float64`
// carries no pointer and would push the collector's scan across it to reach
// `_raw`, where `_value string` carries one and does not.
func (d *InferredAliasDef) Members() []StructMember {
	members := d.builtMembers()
	if len(d.memberOrder) != len(members) {
		return members
	}
	ordered := make([]StructMember, len(members))
	for i, index := range d.memberOrder {
		ordered[i] = members[index]
	}
	return ordered
}

// builtMembers is the wrapper's three members before the layout pass has had
// its say. Kept apart from Members for the same reason StructDef's is: the pass
// orders what this returns, so asking Members for it would have the pass permute
// a list it had already permuted the moment anything called it twice.
func (d *InferredAliasDef) builtMembers() []StructMember {
	return []StructMember{
		{Name: "_value", Type: d.InferredGoType},
		{Name: "_raw", Type: &PrimitiveType{Name: "json.RawMessage"}},
		{Name: "_isRaw", Type: &PrimitiveType{Name: "bool"}},
	}
}
