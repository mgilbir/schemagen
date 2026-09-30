package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// --strict-read-write's reach below a value the generated code keeps as raw
// JSON.
//
// A struct's own members are named in the two key lists its UnmarshalJSON and
// MarshalJSON carry, which is what a Go field can answer for. The positions
// below one of them cannot be: a prefixItems slot, a contains element and a
// patternProperties value are all held as raw JSON or as `any`, so the type the
// generator built for the sub-schema is decoded into by nothing but a Validate
// check -- and a type whose whole schema is held as data has no fields at all.
// At those positions the flag used to do nothing, and a `writeOnly` secret was
// written straight back out (issue #219).
//
// What the schema says there is a machine. Each state is the moves a walk in it
// makes from the value in hand to the members or elements inside it; a move
// says which ones it reaches, whether the schema marks them readOnly or
// writeOnly, and the state they are walked in next. A schema that refers to
// itself is a cycle in the machine, so a walk follows a recursive value to
// whatever depth the document has. A walk carries the set of states a value is
// walked in -- several routes through a schema can describe one value, and the
// set never holds more states than the machine has -- so each value of the
// document is read once, whatever the number of routes. "Do not accept this"
// and "do not write this" act only on an object member: an array element cannot
// be left out without changing the array's length, which minItems can see.
//
// Everything is walked as json.RawMessage. Decoding into `any` would turn every
// number into a float64 and write it back rounded, so a leaf this walker does
// not touch is copied through byte for byte.
const (
	_accessProperty = iota
	_accessPattern
	_accessOther
	_accessItems
	_accessTuple
)

// AccessMove is one move out of a state of a --strict-read-write machine.
// Declared with the members that carry a pointer first and the plain values
// last, which is what keeps the garbage collector's scan of a machine short.
type AccessMove struct {
	// Pattern is an AccessPattern move's key test, one of the package's
	// compiled patterns.
	Pattern *Pattern
	Name    string
	// Except and ExceptPatterns are what an AccessOther move steps past: the
	// members the same schema object declares by name and by pattern, which are
	// exactly the ones additionalProperties and unevaluatedProperties do not
	// reach.
	Except         []string
	ExceptPatterns []*Pattern
	Kind           int
	Index          int
	// Next is the state what the move reaches is walked in, plus one; zero
	// where it is walked in none.
	Next int
	// ReadOnly and WriteOnly say the schema marks what the move reaches.
	ReadOnly  bool
	WriteOnly bool
	// SeekReadOnly and SeekWriteOnly say a walk in Next can find a readOnly or
	// a writeOnly member, so each half of the walker follows only the moves that
	// matter to it.
	SeekReadOnly  bool
	SeekWriteOnly bool
}

// AccessState is one state of a --strict-read-write machine: the moves a walk
// in it makes.
type AccessState struct {
	Moves []AccessMove
}

// AccessRules is where a type's --strict-read-write walk starts: a state of its
// file's machine.
type AccessRules struct {
	States []AccessState
	Start  int
}

// ReadOnlyRefusal is the error --strict-read-write's decoder returns for a
// document that sets a readOnly property.
//
// It is a type rather than a formatted string because two callers have to tell
// it from every other decode failure. The decoder holds one arriving from a
// nested type and goes on filling the value, so that a refusal is the answer
// rather than a half-decoded struct; and a Validate check that decodes raw JSON
// into a generated type has to ignore it outright, because readOnly is an
// annotation and constrains no document -- a Validate that consulted one would
// answer a question the schema did not ask.
type ReadOnlyRefusal struct {
	Path string
}

// Error names the property that was set.
func (e *ReadOnlyRefusal) Error() string {
	return e.Path + ": read-only property may not be set"
}

// _isReadOnlyRefusal reports whether err is, or wraps, the refusal above.
func _isReadOnlyRefusal(err error) bool {
	var target *_readOnlyRefusal
	return err != nil && errors.As(err, &target)
}

// _decodeIgnoringReadOnly decodes for a *validation* check.
//
// The generated Validate methods decode raw JSON into the type the sub-schema
// produced, so that the type's own decoder enforces the shape and its Validate
// enforces the rest. Under --strict-read-write that decoder also refuses a
// document setting a readOnly property, and a check that let the refusal through
// would put an annotation into a validation verdict -- which is the one thing
// both keywords must never do. The decode is left to run to the end and the
// refusal dropped, so the value is fully populated and the verdict is about the
// schema.
func _decodeIgnoringReadOnly(data []byte, dst any) error {
	if err := json.Unmarshal(data, dst); err != nil && !_isReadOnlyRefusal(err) {
		return err
	}
	return nil
}

// _accessKeyMatches reports whether one object member is the one a move names.
// The error is a pattern match with no answer, which the walker reports rather
// than guessing either way: a guess of "no" lets a readOnly member in or a
// writeOnly one out, and a guess of "yes" does the opposite to one the move
// does not name.
func _accessKeyMatches(move *_accessMove, key string) (bool, error) {
	switch move.Kind {
	case _accessProperty:
		return key == move.Name, nil
	case _accessPattern:
		return move.Pattern.matches(key)
	case _accessOther:
		for _, name := range move.Except {
			if key == name {
				return false, nil
			}
		}
		for _, pat := range move.ExceptPatterns {
			matched, err := pat.matches(key)
			if err != nil {
				return false, err
			}
			if matched {
				return false, nil
			}
		}
		return true, nil
	}
	return false, nil
}

// _accessElementMatches reports whether the element at index i is one a move
// names.
func _accessElementMatches(move *_accessMove, i int) bool {
	switch move.Kind {
	case _accessItems:
		return i >= move.Index
	case _accessTuple:
		return i == move.Index
	}
	return false
}

// _accessMember is what the moves of the states in set say about the object
// member key: whether it is marked readOnly or writeOnly, and the states it is
// walked in next -- those that can find a readOnly member where readOnly is
// set, and a writeOnly one where writeOnly is.
func _accessMember(states []_accessState, set []int, key string, readOnly, writeOnly bool) (ro, wo bool, next []int, err error) {
	for _, s := range set {
		for i := range states[s].Moves {
			move := &states[s].Moves[i]
			if move.Kind >= _accessItems {
				continue
			}
			matched, mErr := _accessKeyMatches(move, key)
			if mErr != nil {
				return false, false, nil, fmt.Errorf("%s: %w", _schemagenQuote(key), mErr)
			}
			if !matched {
				continue
			}
			ro = ro || move.ReadOnly
			wo = wo || move.WriteOnly
			if move.Next > 0 && (readOnly && move.SeekReadOnly || writeOnly && move.SeekWriteOnly) {
				next = _accessAdd(next, move.Next-1)
			}
		}
	}
	return ro, wo, next, nil
}

// _accessElement is _accessMember for the element at index i.
func _accessElement(states []_accessState, set []int, i int, readOnly, writeOnly bool) []int {
	var next []int
	for _, s := range set {
		for j := range states[s].Moves {
			move := &states[s].Moves[j]
			if move.Next > 0 && _accessElementMatches(move, i) && (readOnly && move.SeekReadOnly || writeOnly && move.SeekWriteOnly) {
				next = _accessAdd(next, move.Next-1)
			}
		}
	}
	return next
}

// _accessAdd adds a state to a set.
func _accessAdd(set []int, s int) []int {
	for _, have := range set {
		if have == s {
			return set
		}
	}
	return append(set, s)
}

// _accessRefuseReadOnly is the decoder's half: a document that sets any location
// the schema marked readOnly is refused outright.
//
// It walks the value in place (see jsonDoc), so each value costs the moves of
// the states it is walked in rather than a decode of everything below it.
func _accessRefuseReadOnly(d *jsonDoc, sp jsonSpan, rules _accessRules) error {
	path, found, err := _accessFind(d, sp, rules.States, []int{rules.Start})
	if err != nil {
		return err
	}
	if found {
		return &_readOnlyRefusal{Path: _accessPathText(path)}
	}
	return nil
}

// _accessPathStep is one step of the path to a member _accessFind found: a key,
// or an element's index.
type _accessPathStep struct {
	key   string
	index int
	elem  bool
}

// _accessPathText writes the path _accessFind returns, innermost step first, as
// the refusal names it: keys joined by dots, an index in brackets. The path is
// only ever a message, so a key taken from the document is cut short by the
// quoting rule when it is long.
func _accessPathText(path []_accessPathStep) string {
	var b strings.Builder
	for i := len(path) - 1; i >= 0; i-- {
		switch st := path[i]; {
		case st.elem:
			fmt.Fprintf(&b, "[%d]", st.index)
		case i == len(path)-1:
			b.WriteString(_schemagenClipText(st.key))
		default:
			b.WriteString(".")
			b.WriteString(_schemagenClipText(st.key))
		}
	}
	return b.String()
}

// _accessFind reports whether a readOnly member lies below the value at sp, and
// where the first one is, in the order members are visited -- by key, a key
// written twice by its last value, elements by index. A key no pattern match
// could be decided for is reported as an error.
//
// The path is returned innermost step first, each level adding its own step on
// the way back from the member found, so a walk that finds nothing builds none
// and one that finds a member builds it once. A path written out at every level
// on the way down was a copy of the path so far at every level: quadratic in
// the depth of the document.
func _accessFind(d *jsonDoc, sp jsonSpan, states []_accessState, set []int) ([]_accessPathStep, bool, error) {
	switch d.data[sp.start] {
	case '{':
		members := make(map[string]jsonSpan)
		it := d.iter(sp)
		for {
			key, vsp, ok := it.member()
			if !ok {
				break
			}
			members[key] = vsp
		}
		keys := make([]string, 0, len(members))
		// maporder: gathers the keys, which are sorted before any is read.
		for key := range members {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			ro, _, next, err := _accessMember(states, set, key, true, false)
			if err != nil {
				return nil, false, err
			}
			if ro {
				return []_accessPathStep{{key: key}}, true, nil
			}
			if len(next) > 0 {
				path, found, err := _accessFind(d, members[key], states, next)
				if err != nil || found {
					return append(path, _accessPathStep{key: key}), found, err
				}
			}
		}
	case '[':
		it := d.iter(sp)
		for i := 0; ; i++ {
			esp, ok := it.elem()
			if !ok {
				break
			}
			next := _accessElement(states, set, i, true, false)
			if len(next) == 0 {
				continue
			}
			path, found, err := _accessFind(d, esp, states, next)
			if err != nil || found {
				return append(path, _accessPathStep{index: i, elem: true}), found, err
			}
		}
	}
	return nil, false, nil
}

// _accessStrip deletes every writeOnly member below raw and returns the
// rebuilt document, raw itself where nothing was deleted.
//
// A value of the wrong JSON kind is not an error and not a match: a move
// descends where the schema described an object or an array, and a document
// putting something else there has already failed or will, by a check that is
// about the document rather than about who owns which field.
//
// raw is read once and what is returned written once. The first pass walks the
// machine over the document in place (see jsonDoc) and marks the objects and
// arrays something is deleted from, at any depth below; the second writes those
// alone and copies everything else as it stands. An object written is written
// as encoding/json writes a map of its members -- keys sorted, a key written
// twice by its last value -- and an array element by element: what the walk
// wrote when it decoded and encoded every level again, which read and wrote the
// rest of the value at every level of it, quadratic in the depth.
func _accessStrip(raw json.RawMessage, states []_accessState, set []int) (json.RawMessage, error) {
	d, sp, err := jsonOpenDoc(raw)
	if err != nil {
		return raw, nil
	}
	s := _accessStripper{d: d, states: states, members: map[int][]_accessStripMember{}, changed: map[int]bool{}}
	changed, err := s.mark(sp, set)
	if err != nil || !changed {
		return raw, err
	}
	return json.RawMessage(s.write(make([]byte, 0, len(raw)), sp, set)), nil
}

// _accessStripper is one _accessStrip over one document.
//
// The members are declared in the order the garbage collector scans least of:
// the pointers and maps first, and the slice, whose pointer leads, last.
type _accessStripper struct {
	d *jsonDoc
	// members is every object mark walked, by where it starts: its members in
	// key order, a key once, by its last value.
	members map[int][]_accessStripMember
	// changed is every object and array something is deleted from, at any
	// depth below it, by where it starts.
	changed map[int]bool
	states  []_accessState
}

// _accessStripMember is one member of an object _accessStripper walked, its
// members declared in the order the garbage collector scans least of.
type _accessStripMember struct {
	key string
	// next is the states a member something below is deleted from is walked
	// in, nil for any other; gone is a member the machine marks writeOnly.
	next []int
	val  jsonSpan
	gone bool
}

// mark walks the value at sp in the states set, and reports whether anything
// is deleted below it. Keys are visited in order, and each member's subtree
// before the next key, as _accessStrip always visited them, so that a key no
// pattern match could be decided for is the same one reported.
func (s *_accessStripper) mark(sp jsonSpan, set []int) (bool, error) {
	changed := false
	switch s.d.data[sp.start] {
	case '{':
		var ms []_accessStripMember
		it := s.d.iter(sp)
		for {
			key, vsp, ok := it.member()
			if !ok {
				break
			}
			ms = append(ms, _accessStripMember{key: key, val: vsp})
		}
		sort.SliceStable(ms, func(i, j int) bool { return ms[i].key < ms[j].key })
		// A key written twice is its last value, the last of its run.
		kept := ms[:0]
		for i := range ms {
			if i+1 < len(ms) && ms[i+1].key == ms[i].key {
				continue
			}
			kept = append(kept, ms[i])
		}
		for i := range kept {
			m := &kept[i]
			_, wo, next, err := _accessMember(s.states, set, m.key, false, true)
			if err != nil {
				return false, err
			}
			if wo {
				m.gone, changed = true, true
				continue
			}
			if len(next) == 0 {
				continue
			}
			below, err := s.mark(m.val, next)
			if err != nil {
				return false, err
			}
			if below {
				m.next, changed = next, true
			}
		}
		s.members[sp.start] = kept
	case '[':
		it := s.d.iter(sp)
		for i := 0; ; i++ {
			esp, ok := it.elem()
			if !ok {
				break
			}
			next := _accessElement(s.states, set, i, false, true)
			if len(next) == 0 {
				continue
			}
			below, err := s.mark(esp, next)
			if err != nil {
				return false, err
			}
			changed = changed || below
		}
	}
	if changed {
		s.changed[sp.start] = true
	}
	return changed, nil
}

// write appends the value at sp, which mark found something to delete below,
// without what it deletes.
func (s *_accessStripper) write(out []byte, sp jsonSpan, set []int) []byte {
	data := s.d.data
	if data[sp.start] == '{' {
		out = append(out, '{')
		first := true
		for _, m := range s.members[sp.start] {
			if m.gone {
				continue
			}
			if !first {
				out = append(out, ',')
			}
			first = false
			key, _ := json.Marshal(m.key)
			out = append(append(out, key...), ':')
			if m.next != nil {
				out = s.write(out, m.val, m.next)
			} else {
				out = append(out, data[m.val.start:m.val.end]...)
			}
		}
		return append(out, '}')
	}
	out = append(out, '[')
	it := s.d.iter(sp)
	for i := 0; ; i++ {
		esp, ok := it.elem()
		if !ok {
			break
		}
		if i > 0 {
			out = append(out, ',')
		}
		if s.changed[esp.start] {
			if next := _accessElement(s.states, set, i, false, true); len(next) > 0 {
				out = s.write(out, esp, next)
				continue
			}
		}
		out = append(out, data[esp.start:esp.end]...)
	}
	return append(out, ']')
}

// stripWriteOnly is _accessStripWriteOnly over an object whose members are still
// gathered (see jsonObj): the start state's moves are applied to the members,
// member by member, and a member no move names is never read. Applied to the
// written object instead, the walk parsed the whole of it and wrote it out
// again -- the value's whole subtree, at every level of a recursive one.
//
// The result is the same: a marked member is deleted, and a member a move walks
// into is read as encoding/json writes it and rewritten only where the walk
// deletes something inside it.
func (o *jsonObj) stripWriteOnly(rules _accessRules, member jsonMemberEnc) error {
	// In key order, as _accessStrip reads an object, so that a key no pattern
	// match could be decided for is the same one reported.
	live := make([]*jsonMember, 0, len(o.ms))
	for i := range o.ms {
		if !o.ms[i].gone {
			live = append(live, &o.ms[i])
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].key < live[j].key })
	set := []int{rules.Start}
	for _, m := range live {
		_, wo, next, err := _accessMember(rules.States, set, m.key, false, true)
		if err != nil {
			return err
		}
		if wo {
			m.gone = true
			continue
		}
		if len(next) == 0 {
			continue
		}
		var val []byte
		switch {
		case m.idx >= 0:
			val, err = member(m.idx, m.key, nil)
		case m.raw:
			val, err = AppendLeaf(json.RawMessage(m.val), nil)
		default:
			val = m.val
		}
		if err != nil {
			return err
		}
		stripped, err := _accessStrip(val, rules.States, next)
		if err != nil {
			return err
		}
		m.val, m.idx, m.raw = stripped, -1, false
	}
	return nil
}

// _accessStripWriteOnly is the encoder's half: every location the schema marked
// writeOnly is deleted on the way out.
func _accessStripWriteOnly(data []byte, rules _accessRules) ([]byte, error) {
	return _accessStrip(json.RawMessage(data), rules.States, []int{rules.Start})
}

// _accessStripTree is _accessStripWriteOnly over a tree (see jsonValidation):
// the members the machine marks writeOnly are taken out of it, in the same key
// order, and nothing is written out or read back to do it. It is how the
// identity of a value whose writing strips members is read.
//
// The tree is changed in place. Every tree a reading leaves is built for that
// reading -- copied off the value, or decoded from bytes -- so nothing else
// holds it.
func _accessStripTree(t any, rules _accessRules) (any, error) {
	return _accessStripTreeAt(t, rules.States, []int{rules.Start})
}

// _accessStripTreeAt is _accessStrip over a tree. A value of the wrong JSON kind
// is left as it is, as _accessStrip leaves it.
func _accessStripTreeAt(t any, states []_accessState, set []int) (any, error) {
	switch v := t.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		// maporder: gathers the keys, which are sorted before any is read.
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			_, wo, next, err := _accessMember(states, set, key, false, true)
			if err != nil {
				return t, err
			}
			if wo {
				delete(v, key)
				continue
			}
			if len(next) == 0 {
				continue
			}
			value, err := _accessStripTreeAt(v[key], states, next)
			if err != nil {
				return t, err
			}
			v[key] = value
		}
		return v, nil
	case []any:
		for i := range v {
			next := _accessElement(states, set, i, false, true)
			if len(next) == 0 {
				continue
			}
			value, err := _accessStripTreeAt(v[i], states, next)
			if err != nil {
				return t, err
			}
			v[i] = value
		}
		return v, nil
	}
	return t, nil
}

// idStripWriteOnly is jsonObj.stripWriteOnly for an identity, over trees: a
// marked member is taken out, and a member a move walks into is read as its
// tree and has what the walk marks taken out of that. m reads trees, so every
// member read so far holds one.
func (o *jsonIDObj) idStripWriteOnly(rules _accessRules, member jsonIDMemberFunc, m *jsonValidation) error {
	// In key order, as _accessStrip reads an object, so that a key no pattern
	// match could be decided for is the same one reported.
	live := make([]*jsonIDObjMember, 0, len(o.ms))
	for i := range o.ms {
		if !o.ms[i].gone {
			live = append(live, &o.ms[i])
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].key < live[j].key })
	set := []int{rules.Start}
	for _, mb := range live {
		_, wo, next, err := _accessMember(rules.States, set, mb.key, false, true)
		if err != nil {
			return err
		}
		if wo {
			mb.gone = true
			continue
		}
		if len(next) == 0 {
			continue
		}
		t, _, err := o.idTree(mb, member, m)
		if err != nil {
			return err
		}
		stripped, err := _accessStripTreeAt(t, rules.States, next)
		if err != nil {
			return err
		}
		mb.tree, mb.kind = stripped, jsonIDObjComputed
	}
	return nil
}
