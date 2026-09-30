package runtime

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"sync"
	"time"
)

// jsonIDCache is what a document keeps of the identities asked of its values:
// those of its objects and arrays, by where each starts. See jsonDoc.ids.
type jsonIDCache struct {
	ids map[int]jsonID
	mu  sync.Mutex
}

func (c *jsonIDCache) get(at int) (jsonID, bool) {
	c.mu.Lock()
	id, ok := c.ids[at]
	c.mu.Unlock()
	return id, ok
}

func (c *jsonIDCache) put(at int, id jsonID) {
	c.mu.Lock()
	c.ids[at] = id
	c.mu.Unlock()
}

// jsonDocID is the value's identity as JSON, read the way its MarshalJSON
// writes it: every number the literal the document wrote where the value is
// exact, and the float64 encoding/json decodes it into where it is not. It is
// read off the document, and every object and array read on the way is kept
// there: a check that compares values asks at every level of a document for the
// identity of what is below it, and each object and array is read once however
// many levels ask.
func (l jsonLazy) jsonDocID() (jsonID, error) {
	cache := &l.d.ids
	if l.exact {
		cache = &l.d.idsExact
	}
	c := cache.Load()
	if c == nil {
		cache.CompareAndSwap(nil, &jsonIDCache{ids: make(map[int]jsonID)})
		c = cache.Load()
	}
	return l.d.jsonIDAt(l.sp, c, l.exact, 0)
}

func (d *jsonDoc) jsonIDAt(sp jsonSpan, c *jsonIDCache, exact bool, depth int) (jsonID, error) {
	data := d.data
	switch data[sp.start] {
	case '{', '[':
	default:
		if exact {
			return jsonIDRaw(data[sp.start:sp.end])
		}
		return jsonIDRawAsDecoded(data[sp.start:sp.end])
	}
	if id, ok := c.get(sp.start); ok {
		return id, nil
	}
	if depth > jsonIDMaxDepth {
		return jsonID{}, errors.New("json: exceeded max depth")
	}
	var id jsonID
	if data[sp.start] == '[' {
		var a jsonIDArray
		it := d.iter(sp)
		for {
			e, ok := it.elem()
			if !ok {
				break
			}
			eid, err := d.jsonIDAt(e, c, exact, depth+1)
			if err != nil {
				return jsonID{}, err
			}
			a.add(eid)
		}
		id = a.id()
	} else {
		var small [16]jsonIDMember
		ms := small[:0]
		for pos, stop := sp.start+1, sp.end-1; ; {
			i := jsonSkipSpace(data, pos)
			if i >= stop {
				break
			}
			if data[i] == ',' {
				i = jsonSkipSpace(data, i+1)
			}
			keyEnd := jsonStringEnd(data, i)
			var kbuf [64]byte
			r := jsonIDReader{data: data[i:keyEnd]}
			k, err := r.str(kbuf[:0])
			if err != nil {
				return jsonID{}, err
			}
			kid := jsonIDBytes(jsonIDStringKind, k)
			v := jsonSkipSpace(data, jsonSkipSpace(data, keyEnd)+1)
			end := d.valueEnd(v)
			vid, err := d.jsonIDAt(jsonSpan{v, end}, c, exact, depth+1)
			if err != nil {
				return jsonID{}, err
			}
			ms = append(ms, jsonIDMember{key: kid, mem: jsonIDMemberOf(kid, vid)})
			pos = end
		}
		id = jsonIDMembers(ms)
	}
	c.put(sp.start, id)
	return id, nil
}

// jsonIDTime is the identity of a time.Time as its MarshalJSON writes it, which
// refuses a year RFC 3339 cannot write and an offset of a day or more.
func jsonIDTime(t time.Time) (jsonID, error) {
	if y := t.Year(); y < 0 || y > 9999 {
		return jsonID{}, errors.New("Time.MarshalJSON: year outside of range [0,9999]")
	}
	if _, off := t.Zone(); off <= -24*3600 || off >= 24*3600 {
		return jsonID{}, errors.New("Time.MarshalJSON: timezone hour outside of range [0,23]")
	}
	var buf [64]byte
	return jsonIDBytes(jsonIDStringKind, t.AppendFormat(buf[:0], time.RFC3339Nano)), nil
}

// IDObject sums the identities of an object's members: a sum, because a
// member's place in the object is no part of the value. With m reading trees
// (see jsonValidation.reading) it gathers the members' trees instead.
type IDObject struct {
	m    *jsonValidation
	tree map[string]any
	// renamed is jsonTreeKeep's record of the members of tree filed under a
	// name other than their key, and shared jsonIDKeep's of the members of sum
	// whose keys may share a name.
	renamed map[string]string
	shared  jsonIDShared
	sum     jsonID
	n       uint64
}

func (o *jsonIDObject) addID(key, val jsonID) {
	m := jsonIDMemberOf(key, val)
	o.sum.a += m.a
	o.sum.b += m.b
	o.n++
}

// add counts a member whose identity was just read -- and, reading trees, takes
// the tree that reading left.
func (o *jsonIDObject) add(key string, val jsonID) {
	if o.m.reading() {
		if o.tree == nil {
			o.tree = map[string]any{}
		}
		t := o.m.pop()
		if name := jsonValidString(key); jsonTreeKeep(o.tree, &o.renamed, key, name) {
			o.tree[name] = t
		}
		return
	}
	if !jsonKeyMayShare(key) {
		o.addID(jsonIDString(key), val)
		return
	}
	// A key that may be read as another's name: of the members sharing one,
	// the one encoding/json writes last counts. See jsonIDKeep.
	m := jsonIDMemberOf(jsonIDString(key), val)
	keep, old, replaces := jsonIDKeep(&o.shared, key, m)
	if !keep {
		return
	}
	if replaces {
		o.sum.a -= old.a
		o.sum.b -= old.b
		o.n--
	}
	o.sum.a += m.a
	o.sum.b += m.b
	o.n++
}

// jsonTreeKeep reports whether the member under key k of a Go map is the one a
// tree keeps under name, the text k is read as, and records it when it is.
//
// Two keys that are not valid UTF-8 can be read as one name, each byte that is
// not UTF-8 read as U+FFFD -- as can such a key and one that spells U+FFFD. The
// tree has room for one member per name, and a map is ranged in no fixed order,
// so filing each member as it came kept whichever the order put last.
// encoding/json writes every member, in the order of the keys, and a reader of
// what it writes keeps the last of them: so of the members sharing a name the
// tree keeps the one with the greatest key, whatever the order. renamed holds
// the key of each member kept under a name other than its own; until the first
// such member it is nil, and a member whose key is its name is kept at once.
func jsonTreeKeep(tree map[string]any, renamed *map[string]string, k, name string) bool {
	if name == k && *renamed == nil {
		return true
	}
	if _, taken := tree[name]; taken {
		held, ok := (*renamed)[name]
		if !ok {
			held = name
		}
		if held > k {
			return false
		}
	}
	if name != k {
		if *renamed == nil {
			*renamed = make(map[string]string, 1)
		}
		(*renamed)[name] = k
	} else {
		delete(*renamed, name)
	}
	return true
}

func (o *jsonIDObject) id() jsonID {
	if o.m.reading() {
		if o.tree == nil {
			o.tree = map[string]any{}
		}
		o.m.push(o.tree)
		return jsonID{}
	}
	return jsonIDObjectOf(o.sum, o.n)
}

// IDObj is Obj for an identity: the object a struct whose MarshalJSON
// gathers its members writes, read by the same rules -- a member set twice
// holds the last value set, and one taken out is gone. A member holding this
// package's types, and raw JSON a value holds, are read only once the object is
// complete, as jsonObj writes them only then: a member that is replaced or taken
// out before is never read, as it was never written.
type IDObj struct {
	m     *jsonValidation
	at    map[string]int
	ms    []jsonIDObjMember
	small [8]jsonIDObjMember
}

type jsonIDObjMember struct {
	tree any
	key  string
	raw  []byte
	id   jsonID
	idx  int
	kind byte
	gone bool
}

// The three things a gathered member can be: its identity, raw JSON, or the
// index the value's jsonIdentityMember reads it under.
const (
	jsonIDObjComputed = 'i'
	jsonIDObjRaw      = 'r'
	jsonIDObjDeferred = 'd'
)

// IDMemberFunc reads a deferred member: the value's jsonIdentityMember,
// told the member's index and key -- the key for an additionalProperties value,
// which all share one index.
type IDMemberFunc func(idx int, key string, m *Validation) (ID, error)

func (o *jsonIDObj) idFind(key string) int {
	if o.at != nil {
		if i, ok := o.at[key]; ok {
			return i
		}
		return -1
	}
	for i := range o.ms {
		if o.ms[i].key == key {
			return i
		}
	}
	return -1
}

func (o *jsonIDObj) idSet(m jsonIDObjMember) {
	if i := o.idFind(m.key); i >= 0 {
		o.ms[i] = m
		return
	}
	if o.ms == nil {
		o.ms = o.small[:0]
	}
	o.ms = append(o.ms, m)
	if o.at == nil && len(o.ms) > 16 {
		o.at = make(map[string]int, 2*len(o.ms))
		for i := range o.ms {
			o.at[o.ms[i].key] = i
		}
	} else if o.at != nil {
		o.at[m.key] = len(o.ms) - 1
	}
}

// computed sets a member to the identity just read -- and, reading trees, to
// the tree that reading left.
func (o *jsonIDObj) computed(key string, id jsonID) {
	mb := jsonIDObjMember{key: key, id: id, kind: jsonIDObjComputed}
	if o.m.reading() {
		mb.tree = o.m.pop()
	}
	o.idSet(mb)
}

func (o *jsonIDObj) idHeld(key string, raw []byte) {
	o.idSet(jsonIDObjMember{key: key, raw: raw, kind: jsonIDObjRaw})
}

func (o *jsonIDObj) idDeferred(key string, idx int) {
	o.idSet(jsonIDObjMember{key: key, idx: idx, kind: jsonIDObjDeferred})
}

func (o *jsonIDObj) idDel(key string) {
	if i := o.idFind(key); i >= 0 {
		o.ms[i].gone = true
	}
}

// nulled sets a member to null.
func (o *jsonIDObj) nulled(key string) {
	o.idSet(jsonIDObjMember{key: key, id: jsonIDOfKind(jsonIDNullKind), kind: jsonIDObjComputed})
}

// idTree is the tree of a member, m reading trees: a deferred member is read for
// the asking, and holds its tree from then on. empty says the member is raw JSON
// of no bytes at all, which jsonObj.memberBytes hands back as it is.
func (o *jsonIDObj) idTree(mb *jsonIDObjMember, member jsonIDMemberFunc, m *jsonValidation) (any, bool, error) {
	switch mb.kind {
	case jsonIDObjDeferred:
		if _, err := member(mb.idx, mb.key, m); err != nil {
			return nil, false, err
		}
		mb.tree, mb.kind = m.pop(), jsonIDObjComputed
	case jsonIDObjRaw:
		if len(mb.raw) == 0 {
			return nil, true, nil
		}
		t, err := jsonTreeRaw(mb.raw, false)
		if err != nil {
			return nil, false, err
		}
		mb.tree, mb.kind = t, jsonIDObjComputed
	}
	return mb.tree, false, nil
}

// idValue is jsonObj.memberBytes for an identity, m reading trees: the member's tree,
// whether it is there, and whether it is raw JSON of no bytes.
func (o *jsonIDObj) idValue(key string, member jsonIDMemberFunc, m *jsonValidation) (any, bool, bool, error) {
	i := o.idFind(key)
	if i < 0 || o.ms[i].gone {
		return nil, false, false, nil
	}
	t, empty, err := o.idTree(&o.ms[i], member, m)
	return t, true, empty, err
}

// jsonTreeWritten reports whether two trees are written as the same bytes by
// encoding/json -- which is what a check that compared what two values wrote
// decided. It is jsonTreeEqual with numbers compared by the literal each is
// written as rather than by its value: 0 and 0.0, or 0 and -0, are one JSON
// value written two ways. Strings compare by their text, members in any order,
// as encoding/json writes both one way.
func jsonTreeWritten(a, b any) bool {
	switch x := a.(type) {
	case float64, json.Number:
		xs, _ := jsonTreeWrittenNumber(a)
		ys, ok := jsonTreeWrittenNumber(b)
		return ok && xs == ys
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonTreeWritten(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		// maporder: a conjunction over every member, which no order of the loop changes.
		for k, xv := range x {
			yv, ok := y[k]
			if !ok || !jsonTreeWritten(xv, yv) {
				return false
			}
		}
		return true
	}
	return jsonTreeEqual(a, b)
}

// jsonTreeWrittenNumber is the literal encoding/json writes a tree's number as:
// a json.Number's own text, and a float64 as encoding/json formats one.
func jsonTreeWrittenNumber(v any) (string, bool) {
	switch n := v.(type) {
	case json.Number:
		return string(n), true
	case float64:
		format := byte('f')
		if abs := math.Abs(n); abs != 0 && (abs < 1e-6 || abs >= 1e21) {
			format = 'e'
		}
		b := strconv.AppendFloat(nil, n, format, -1, 64)
		if format == 'e' {
			// clean up e-09 to e-9, as encoding/json does
			if k := len(b); k >= 4 && b[k-4] == 'e' && b[k-3] == '-' && b[k-2] == '0' {
				b[k-2] = b[k-1]
				b = b[:k-1]
			}
		}
		return string(b), true
	}
	return "", false
}

// idOf is the object's identity, its deferred and raw members read now.
func (o *jsonIDObj) idOf(member jsonIDMemberFunc, m *jsonValidation) (jsonID, error) {
	obj := jsonIDObject{m: m}
	for i := range o.ms {
		mb := &o.ms[i]
		if mb.gone {
			continue
		}
		id := mb.id
		var err error
		switch mb.kind {
		case jsonIDObjDeferred:
			id, err = member(mb.idx, mb.key, m)
		case jsonIDObjRaw:
			id, err = jsonIDRawIn(mb.raw, m)
		default:
			if m.reading() {
				m.push(mb.tree)
			}
		}
		if err != nil {
			return jsonID{}, err
		}
		obj.add(mb.key, id)
	}
	return obj.id(), nil
}
