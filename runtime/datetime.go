package runtime

import (
	"encoding/json"
	"time"
)

// jsonDateTime is the decode destination for an asserted `format: date-time`,
// and it exists because time.Time's own decoder is stricter than the format it
// names.
//
// RFC 3339 section 5.6 defines date-time as full-date "T" full-time, and its
// ABNF carries the NOTE that the "T" and "Z" characters "may alternatively be
// lower case 't' or 'z' respectively". time.Time is read from a layout, and a
// layout matches those two characters literally, so {"a":"2020-01-02t03:04:05z"}
// came back as `cannot parse "t03:04:05z" as "T"` -- a document the format
// permits, refused at decode.
//
// encoding/json decides what a token may fill from the destination type alone,
// so a type of our own is the only place that decision can be intercepted --
// the same reason jsonInteger and jsonNumber exist. The declared type is
// unchanged; this one is only ever the shadow the decode runs through.
//
// The retry is deliberately narrow: it respells those two positions and hands
// the result to the same decoder, so what is accepted is exactly what that
// decoder accepts once the case is settled. Nothing else is loosened, and no
// value the format admits reaches the refusal below.
//
// What is refused is refused in the schema's words rather than the parser's.
// time.Time is read from a layout, and a layout's complaint is about the layout
// -- `parsing time "nope" as "2006-01-02T15:04:05Z07:00": cannot parse "nope" as
// "2006"` names neither the keyword nor anything the caller wrote. The same
// format one sibling keyword away is held as a string and answered with `"nope"
// is not a valid date-time (RFC 3339)`, which is the message this one now gives
// too. See schemagenFormatDateTime and issue #282.
//
// The respelling is done to the JSON literal's own bytes rather than to a
// decoded string, so there is nothing to re-quote afterwards and no escape to
// reason about. An escaped character is not a byte being looked for: "t" and
// "z" are not hex digits, so neither position can fall inside a \uXXXX, and a
// "\t" or "\z" respelled here is an escape the retry refuses -- leaving the
// original error, which is the answer that was wanted anyway.
//
// Nothing checks that data is a JSON string at all, and no check would be
// reachable: the two bytes touched are neither the first nor the last, so a
// value that was not a quoted string before the respelling is not one after,
// and the retry hands the result to a decoder that accepts nothing else.
// Twenty-two bytes is the shortest a quoted RFC 3339 date-time can be, so
// anything below it has nothing to respell whatever its case.
type jsonDateTime = DateTime

// DateTime is jsonDateTime, under the name generated code refers to it by.
type DateTime time.Time

// SchemagenGenerated marks DateTime as a type whose UnmarshalJSON may be handed
// bytes already checked to be one JSON value. See jsonGenerated.
func (*DateTime) SchemagenGenerated() {}

// UnmarshalJSON reads an RFC 3339 date-time as time.Time does, and also with the
// lower case 't' and 'z' the format permits. A value that is not one is refused
// in the format's words. See jsonDateTime.
func (j *DateTime) UnmarshalJSON(data []byte) error {
	var _t time.Time
	_err := json.Unmarshal(data, &_t)
	if _err == nil {
		*j = jsonDateTime(_t)
		return nil
	}
	if len(data) >= 22 {
		// Index 11 is the "T" position of the value; the offset's "Z" is the
		// byte before the closing quote.
		_upper := append([]byte(nil), data...)
		if _upper[11] == 't' {
			_upper[11] = 'T'
		}
		if _upper[len(_upper)-2] == 'z' {
			_upper[len(_upper)-2] = 'Z'
		}
		if json.Unmarshal(_upper, &_t) == nil {
			*j = jsonDateTime(_t)
			return nil
		}
	}
	return jsonDateTimeRefusal(data)
}

// jsonDateTimeRefusal words the refusal above. A value that is a JSON string is
// a date-time that does not hold, and is answered for as one.
//
// A value that is not a JSON string is a type error, and is reported by the
// string decode rather than by time.Time's: `Time.UnmarshalJSON: input is not a
// JSON string` names a Go method and not the token the document carried, while
// the string decode's refusal names the token and is read for the schema's own
// words by jsonDecodeRefusal.
func jsonDateTimeRefusal(data []byte) error {
	var _s string
	if _err := json.Unmarshal(data, &_s); _err != nil {
		return _err
	}
	return jsonValueErrorf("%s is not a valid date-time (RFC 3339)", _schemagenQuote(_s))
}
