package runtime

import (
	"encoding/json"
	"fmt"
)

// oneofHasRequiredFields reports whether the value at sp is a JSON object
// carrying every one of the named members.
//
// It reads the object's own keys off the document rather than decoding the
// object, so asking costs the number of members and not the size of what they
// hold: a union whose branches are selected by their required members is asked
// this at every level of a recursive document, and a decode of the whole value
// at each of them made the selection alone quadratic in the depth.
func oneofHasRequiredFields(d *jsonDoc, sp jsonSpan, fields ...string) bool {
	if d.data[sp.start] != '{' {
		return false
	}
	found := 0
	seen := make([]bool, len(fields))
	it := d.iter(sp)
	for {
		key, _, ok := it.member()
		if !ok {
			break
		}
		for i, f := range fields {
			if !seen[i] && key == f {
				seen[i] = true
				found++
			}
		}
	}
	return found == len(fields)
}

// oneofDiscriminatorValue extracts the string value of a discriminator property
// from the value at sp.
//
// It reads the one member off the document, as oneofHasRequiredFields does and
// for its reason. A member written twice means its last value, as it does
// everywhere else a document is read. A value that is not a JSON object is
// refused in the words encoding/json refuses one for the map this used to decode
// into, and a member that is not a string in the words it refuses one for a
// string; a null member is read as the empty string, which is what that decode
// made of it.
func oneofDiscriminatorValue(d *jsonDoc, sp jsonSpan, prop string) (string, error) {
	switch d.data[sp.start] {
	case '{':
	case 'n':
		return "", fmt.Errorf("discriminator property %q is missing", prop)
	default:
		return "", fmt.Errorf("discriminator: cannot parse as object: %w", jsonTypeError[map[string]json.RawMessage](d, sp))
	}
	var member jsonSpan
	found := false
	it := d.iter(sp)
	for {
		key, vsp, ok := it.member()
		if !ok {
			break
		}
		if key == prop {
			member, found = vsp, true
		}
	}
	if !found {
		return "", fmt.Errorf("discriminator property %q is missing", prop)
	}
	var val string
	switch d.data[member.start] {
	case '"', 'n':
		if err := AtJSON(&val, d, member); err != nil {
			return "", fmt.Errorf("discriminator property %q is not a string: %w", prop, err)
		}
	default:
		return "", fmt.Errorf("discriminator property %q is not a string: %w", prop, jsonTypeError[string](d, member))
	}
	return val, nil
}
