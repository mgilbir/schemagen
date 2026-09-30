package runtime

// --strict-read-write: the path model that reaches the readOnly and writeOnly
// locations no Go field answers for, and the refusal type a Validate check has
// to be able to tell from a real decode failure.

// The kinds of an AccessStep.
const (
	AccessProperty = _accessProperty
	AccessPattern  = _accessPattern
	AccessOther    = _accessOther
	AccessItems    = _accessItems
	AccessTuple    = _accessTuple
)

// AccessRefuseReadOnly is the refusal of a value that sets a readOnly location
// the rules name, or nil.
func AccessRefuseReadOnly(d *Doc, sp Span, rules []AccessRule) error {
	return _accessRefuseReadOnly(d, sp, rules)
}

// AccessStripWriteOnly is data with the writeOnly locations the rules name taken
// out of it.
func AccessStripWriteOnly(data []byte, rules []AccessRule) ([]byte, error) {
	return _accessStripWriteOnly(data, rules)
}

// AccessStripTree is a value's tree with the writeOnly locations the rules name
// taken out of it.
func AccessStripTree(t any, rules []AccessRule) (any, error) { return _accessStripTree(t, rules) }

// DecodeIgnoringReadOnly decodes for a validation check: it runs to the end and
// drops ReadOnlyRefusal, so that the value is fully populated and the verdict is
// about the schema, readOnly being an annotation that constrains no document.
func DecodeIgnoringReadOnly(data []byte, dst any) error { return _decodeIgnoringReadOnly(data, dst) }

// IsReadOnlyRefusal reports whether err is, or wraps, a ReadOnlyRefusal.
func IsReadOnlyRefusal(err error) bool { return _isReadOnlyRefusal(err) }
