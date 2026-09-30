package runtime

import (
	"encoding/json"
	"math"
	"strconv"
)

// jsonKindAt reads the value at p the way a keyword about one kind of JSON
// value reads it: its kind -- one of the jsonID*Kind tags, a boolean either of
// its two -- and, for a string, its characters, and for a number, its literal.
// A value encoding/json cannot write has kind 0, which no keyword's kind is.
//
// It is what the checks that asked this used to learn by marshalling the value
// and looking at the first byte of the text.
func jsonKindAt[T any](p *T) (byte, string) {
	switch v := any(p).(type) {
	case *any:
		return jsonKindAny(*v)
	case *string:
		return jsonIDStringKind, jsonValidString(*v)
	case *float64:
		if math.IsNaN(*v) || math.IsInf(*v, 0) {
			return 0, ""
		}
		return jsonIDNumberKind, strconv.FormatFloat(*v, 'g', -1, 64)
	case *int64:
		return jsonIDNumberKind, strconv.FormatInt(*v, 10)
	case *bool:
		if *v {
			return jsonIDTrueKind, ""
		}
		return jsonIDFalseKind, ""
	case *json.Number:
		return jsonKindNumber(*v)
	case *json.RawMessage:
		if *v == nil {
			return jsonIDNullKind, ""
		}
		return jsonKindRaw(*v, false)
	}
	return jsonKindAny(any(*p))
}

// jsonKindAny is jsonKindAt for a value held as an any.
func jsonKindAny(v any) (byte, string) {
	switch x := v.(type) {
	case nil:
		return jsonIDNullKind, ""
	case string:
		return jsonIDStringKind, jsonValidString(x)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, ""
		}
		return jsonIDNumberKind, strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		if x {
			return jsonIDTrueKind, ""
		}
		return jsonIDFalseKind, ""
	case json.Number:
		return jsonKindNumber(x)
	case []any:
		if x == nil {
			return jsonIDNullKind, ""
		}
		return jsonIDArrayKind, ""
	case map[string]any:
		if x == nil {
			return jsonIDNullKind, ""
		}
		return jsonIDObjectKind, ""
	case json.RawMessage:
		if x == nil {
			return jsonIDNullKind, ""
		}
		return jsonKindRaw(x, false)
	case jsonLazy:
		return jsonKindRaw(x.d.raw(x.sp), !x.exact)
	}
	// Anything else is read as a tree (see jsonTreeAny), whose kind is the
	// value's: a type that writes itself says what kind it writes by the rules
	// it writes by.
	t, err := jsonTreeAny(v, nil)
	if err != nil {
		return 0, ""
	}
	return jsonKindAny(t)
}

// jsonFloatOf is a number jsonKindAt read, as the float64 encoding/json decodes
// it into; false for any other kind, and for a number no float64 holds.
func jsonFloatOf(kind byte, text string) (float64, bool) {
	if kind != jsonIDNumberKind {
		return 0, false
	}
	f, err := strconv.ParseFloat(text, 64)
	return f, err == nil
}

// jsonKindNumber is jsonKindAt for a json.Number, which encoding/json writes as
// its literal -- an empty one as 0 -- and refuses when it is not a number.
func jsonKindNumber(n json.Number) (byte, string) {
	if n == "" {
		return jsonIDNumberKind, "0"
	}
	if !jsonIsNumberLiteral(string(n)) {
		return 0, ""
	}
	return jsonIDNumberKind, string(n)
}

// jsonKindRaw is jsonKindAt for a JSON text: a string read the way
// encoding/json unescapes one, and a number's literal -- as a float64 would read
// it back where floats says the value is one as decoded into an any.
func jsonKindRaw(b []byte, floats bool) (byte, string) {
	r := jsonIDReader{data: b}
	r.skip()
	if r.pos >= len(b) {
		return 0, ""
	}
	switch c := b[r.pos]; {
	case c == '"':
		s, err := r.str(nil)
		if err != nil {
			return 0, ""
		}
		return jsonIDStringKind, string(s)
	case c == 'n':
		return jsonIDNullKind, ""
	case c == 't':
		return jsonIDTrueKind, ""
	case c == 'f':
		return jsonIDFalseKind, ""
	case c == '[':
		return jsonIDArrayKind, ""
	case c == '{':
		return jsonIDObjectKind, ""
	}
	end := len(b)
	for end > r.pos {
		if c := b[end-1]; c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		end--
	}
	text := string(b[r.pos:end])
	if !jsonIsNumberLiteral(text) {
		return 0, ""
	}
	if floats {
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0, ""
		}
		return jsonIDNumberKind, strconv.FormatFloat(f, 'g', -1, 64)
	}
	return jsonIDNumberKind, text
}
