package runtime

import (
	"encoding/json"
)

// jsonNumber is the decode destination for a "number" that this package holds
// exactly, as the literal the document wrote rather than as the float64 it
// rounds to.
//
// The declared type is encoding/json's json.Number, which keeps the literal and
// writes it back unchanged. What it does not do is refuse a JSON *string*:
// json.Number is a string underneath, so {"n":"1.5"} fills one as readily as
// {"n":1.5} does, and the float64 this replaces refused that outright. A
// document the schema forbids would have been accepted and then written back
// out unquoted, as a number nobody sent.
//
// encoding/json decides what a token may fill from the destination type alone,
// so a type of our own is the only place that decision can be intercepted --
// the same reason jsonInteger exists for the other JSON numeric type. The
// declared type is unchanged; this one is only ever the shadow the decode runs
// through.
type jsonNumber = Number

// Number is jsonNumber, under the name generated code refers to it by.
type Number json.Number

// SchemagenGenerated marks Number as a type whose UnmarshalJSON may be handed
// bytes already checked to be one JSON value. See jsonGenerated.
func (*Number) SchemagenGenerated() {}

// UnmarshalJSON keeps the literal a JSON number was written as, and refuses a
// JSON string. See jsonNumber.
func (j *Number) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		// In the schema's words for the reason jsonInteger's is: a type that
		// answers for its own decode is handed the bytes, so encoding/json never
		// words this one and `Go value of type number` is the only thing the
		// caller is told. See jsonDecodeRefusal and issue #282.
		return jsonValueErrorf("expected number, got string")
	}
	var _n json.Number
	if _err := json.Unmarshal(data, &_n); _err != nil {
		return _err
	}
	*j = jsonNumber(_n)
	return nil
}

// The functions below, and the number blocks built on them, are the one
// reading of a JSON number this package makes. Every numeric keyword --
// minimum, maximum, both exclusive bounds, multipleOf, the "integer" type, and
// the numeric half of const, enum and uniqueItems -- is decided through them,
// whatever Go type the number happens to be held in, so that a position's
// representation can never change the verdict on it.
//
// JSON Schema defines those keywords over numbers as mathematical values, and a
// JSON number has no precision limit. float64 cannot be asked any of these
// questions: it holds 2^53 and 2^53+1 as one number, holds 0.1 as a binary
// fraction that is not a tenth, and holds 1e400 as an infinity. So the number
// is kept as the decimal the document wrote and the arithmetic is done on its
// digits, at a cost set by the length of the literal rather than by the
// magnitude it names.
