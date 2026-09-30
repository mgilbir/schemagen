package runtime

// The runtime schema evaluator: a schema held as data (Node), interpreted
// against a value decoded into an any. Generated code emits the nodes as
// composite literals and calls EvalNode; the helpers here are what the literals
// and the dynamic checks are built from.

// StrPtr, IntPtr and BoolPtr point to a copy of their argument, for the optional
// fields of a Node literal.
func StrPtr(s string) *string { return &s }

// IntPtr points to a copy of i. See StrPtr.
func IntPtr(i int) *int { return &i }

// BoolPtr points to a copy of b. See StrPtr.
func BoolPtr(b bool) *bool { return &b }

// NodePtr points to a copy of n, for the fields of a Node that hold another.
func NodePtr(n Node) *Node { return &n }

// EvalNode judges the value v against the schema n: the verdict, the reason it
// failed, and the annotations it produced.
func EvalNode(n *Node, v any) EvalResult { return _evalNode(n, v) }

// EvalError turns a failed evaluation into the error a Validate answers with,
// carrying what has to be written between the reason and the path of the value
// it was raised on.
func EvalError(r EvalResult) error { return _evalError(r) }

// OK reports whether the value satisfied the schema.
func (r EvalResult) OK() bool { return r.ok }

// Undecided is the error that kept the evaluation from reaching a verdict -- a
// pattern the engine gave no answer for -- or nil.
func (r EvalResult) Undecided() error { return r.undecided }

// Reason is the reason the evaluation failed, as text.
func (r EvalResult) Reason() string { return r.why.String() }

// DynIsString reports whether v is a string.
func DynIsString(v any) bool { return _dynIsString(v) }

// DynIsBool reports whether v is a boolean.
func DynIsBool(v any) bool { return _dynIsBool(v) }

// DynIsObject reports whether v is an object.
func DynIsObject(v any) bool { return _dynIsObject(v) }

// DynIsArray reports whether v is an array.
func DynIsArray(v any) bool { return _dynIsArray(v) }

// DynStrOK applies a string constraint to v, vacuously true for a non-string.
func DynStrOK(v any, ok func(string) bool) bool { return _dynStrOK(v, ok) }

// DynConstOK reports whether the decoded value v equals the constant want,
// which is passed already encoded, both read with every number exact.
func DynConstOK(v any, want string) bool { return _dynConstOK(v, want) }
