package runtime

import (
	"sort"
)

// NullRule says where a JSON null is forbidden beneath one value: at the
// value itself when Reject is set, and beneath it at the level Elem names. A
// rule whose Elem is nil ends the walk.
type NullRule struct {
	Elem   *NullRule
	Reject bool
	IsMap  bool
}

// checkJSONNullsAt reports the first position beneath data at which the document
// carries a null the schema does not admit, as a message relative to data
// itself: the caller puts its own path in front, by the rule that path takes.
//
// Relative rather than given a path to print, because who joins decides how. The
// same walk is reached from a named member, from an entry of an overflow map,
// and from a container alias whose own value its container has already answered
// for -- a member name, an accessor and nothing at all -- and a walk that built
// the path itself could not tell a "." from a "[" without being told. See
// jsonPathError.
//
// It reads the document rather than the decoded value because the decoded value
// no longer holds the answer. encoding/json turns an explicit null into a nil
// pointer, a nil slice or map, or a scalar left untouched at its zero -- all of
// them exactly what an absent property leaves behind -- so once the document has
// been decoded, "present and null" and "absent" are one state.
//
// It walks the document's own structure (see jsonDoc) rather than decoding each
// level into raw members, so a level costs the number of its members and not
// the size of what they hold. The members may be objects of this package's own
// types, which hold further members of their own, and a decode of every level
// here -- done again by each enclosing type for its own rule -- was quadratic in
// the depth of the document.
//
// A value whose shape does not match the rule is passed over rather than
// reported. The decoder has already had its say about the type, and a second
// complaint from here would be a worse-worded version of the same one.
func checkJSONNullsAt(d *jsonDoc, sp jsonSpan, rule *jsonNullRule) error {
	if rule == nil {
		return nil
	}
	if d.data[sp.start] == 'n' {
		if rule.Reject {
			return jsonValueErrorf("null is not allowed")
		}
		return nil
	}
	if rule.Elem == nil {
		return nil
	}
	if rule.IsMap {
		if d.data[sp.start] != '{' {
			return nil
		}
		// A key written twice means its last value.
		members := make(map[string]jsonSpan)
		it := d.iter(sp)
		for {
			k, vsp, ok := it.member()
			if !ok {
				break
			}
			members[k] = vsp
		}
		keys := make([]string, 0, len(members))
		for k := range members {
			keys = append(keys, k)
		}
		// Range order over a map is deliberately unspecified, and the first
		// offending key is what the error names, so an object carrying two of
		// them would otherwise fail differently from one run to the next.
		sort.Strings(keys)
		for _, k := range keys {
			if err := checkJSONNullsAt(d, members[k], rule.Elem); err != nil {
				return jsonElemPathf(err, "[%s]", _schemagenQuote(k))
			}
		}
		return nil
	}
	if d.data[sp.start] != '[' {
		return nil
	}
	it := d.iter(sp)
	for i := 0; ; i++ {
		esp, ok := it.elem()
		if !ok {
			break
		}
		if err := checkJSONNullsAt(d, esp, rule.Elem); err != nil {
			return jsonElemPathf(err, "[%d]", i)
		}
	}
	return nil
}
