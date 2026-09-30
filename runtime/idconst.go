package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
)

// Const is what a const or an enum permits: each member's identity and its
// tree, read once from the literal the schema wrote. Numbers are read as the
// literal writes them, or, with floats, as a float64 -- which is how the
// runtime evaluator, reading a decoded value, compares them.
type Const struct {
	ids   []jsonID
	trees []any
}

var jsonConsts sync.Map

// jsonConstOf is the const or enum the texts write, read once per process. A
// text that is not JSON permits nothing.
func jsonConstOf(floats bool, texts ...string) *jsonConst {
	key := make([]byte, 0, 64)
	if floats {
		key = append(key, 'f')
	}
	for _, t := range texts {
		key = append(append(key, 0), t...)
	}
	if c, ok := jsonConsts.Load(string(key)); ok {
		return c.(*jsonConst)
	}
	c := &jsonConst{}
	for _, t := range texts {
		tree, err := jsonTreeRaw([]byte(t), floats)
		if err != nil {
			continue
		}
		var id jsonID
		if floats {
			id, err = jsonIDRawAsDecoded([]byte(t))
		} else {
			id, err = jsonIDRaw([]byte(t))
		}
		if err != nil {
			continue
		}
		c.ids = append(c.ids, id)
		c.trees = append(c.trees, tree)
	}
	got, _ := jsonConsts.LoadOrStore(string(key), c)
	return got.(*jsonConst)
}

// jsonMatchesConstRaw is jsonMatchesConst for raw JSON a value holds.
func jsonMatchesConstRaw(b []byte, c *jsonConst) (bool, error) {
	id, err := jsonIDRaw(b)
	if err != nil {
		return false, err
	}
	if !jsonIDIn(id, c.ids) {
		return false, nil
	}
	t, err := jsonTreeRaw(b, false)
	if err != nil {
		return false, err
	}
	for i := range c.ids {
		if c.ids[i] == id && jsonTreeEqual(t, c.trees[i]) {
			return true, nil
		}
	}
	return false, nil
}

// jsonTreeRaw is the tree of raw JSON: what encoding/json decodes it into, with
// every number a json.Number -- or, with floats, a float64. It is a decode of
// bytes already held, not an encoding.
func jsonTreeRaw(b []byte, floats bool) (any, error) {
	var v any
	if floats {
		err := json.Unmarshal(b, &v)
		return v, err
	}
	if !json.Valid(b) {
		// encoding/json's own words for what is wrong with it.
		return nil, json.Unmarshal(b, &v)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	err := dec.Decode(&v)
	return v, err
}

// jsonTreeEqual reports whether two trees are one JSON value: numbers by their
// canonical reading (see _jsonCanonicalNumber), members in any order.
func jsonTreeEqual(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case float64, json.Number:
		xs, _ := jsonTreeNumber(a)
		ys, ok := jsonTreeNumber(b)
		return ok && _jsonCanonicalNumber(xs) == _jsonCanonicalNumber(ys)
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonTreeEqual(x[i], y[i]) {
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
			if !ok || !jsonTreeEqual(xv, yv) {
				return false
			}
		}
		return true
	}
	return false
}

// jsonTreeNumber is a tree's number as a literal.
func jsonTreeNumber(v any) (string, bool) {
	switch n := v.(type) {
	case float64:
		return strconv.FormatFloat(n, 'g', -1, 64), true
	case json.Number:
		return string(n), true
	}
	return "", false
}

// jsonIDJSON is the identity (see jsonID) of a value held as decoded JSON: what
// encoding/json decodes into an any -- nil, a bool, a string, a float64 or a
// json.Number, a []any, a map[string]any -- raw JSON, and a value of a document
// read lazily (see jsonLazy), at any depth. That is every value the runtime
// evaluator and the dynamic checks compare: they judge decoded documents, and a
// value built in Go reaches the evaluator only as its tree (see jsonTreeView).
// Reading nothing else, they need none of what reads a Go value by the rules it
// is written by (see IdentifyAt), and a package whose only comparisons are
// theirs carries none of it.
//
// Anything else is not decoded JSON, and is refused rather than guessed at.
func jsonIDJSON(v any) (jsonID, error) {
	switch x := v.(type) {
	case nil:
		return jsonIDOfKind(jsonIDNullKind), nil
	case bool:
		return jsonIDBool(x), nil
	case string:
		return jsonIDString(x), nil
	case float64:
		return jsonIDFloat(x, 64)
	case json.Number:
		return jsonIDNumberLiteral(x)
	case []any:
		if x == nil {
			return jsonIDOfKind(jsonIDNullKind), nil
		}
		var a jsonIDArray
		for _, e := range x {
			id, err := jsonIDJSON(e)
			if err != nil {
				return jsonID{}, err
			}
			a.add(id)
		}
		return a.id(), nil
	case map[string]any:
		if x == nil {
			return jsonIDOfKind(jsonIDNullKind), nil
		}
		// A sum over the members, as jsonIDObject's: a member's place in the
		// object is no part of the value. Of members whose keys read as one
		// name, the one encoding/json writes last counts (see jsonIDKeep).
		var sum jsonID
		var n uint64
		var shared jsonIDShared
		// maporder: sums the members, and of members sharing a name counts the greatest key's (jsonIDKeep), which no order of the loop changes.
		for k, e := range x {
			id, err := jsonIDJSON(e)
			if err != nil {
				return jsonID{}, err
			}
			m := jsonIDMemberOf(jsonIDString(k), id)
			if jsonKeyMayShare(k) {
				keep, old, replaces := jsonIDKeep(&shared, k, m)
				if !keep {
					continue
				}
				if replaces {
					sum.a -= old.a
					sum.b -= old.b
					n--
				}
			}
			sum.a += m.a
			sum.b += m.b
			n++
		}
		return jsonIDObjectOf(sum, n), nil
	case json.RawMessage:
		return jsonIDRawMessage(x)
	case jsonLazy:
		return x.jsonDocID()
	}
	return jsonID{}, jsonNotJSON(v)
}

// jsonNotJSON is the refusal of a value that is not decoded JSON.
func jsonNotJSON(v any) error {
	return fmt.Errorf("schemagen: a value of type %T is not decoded JSON", v)
}

// jsonTreeJSON is the tree (see jsonTreeEqual) of a value held as decoded JSON:
// the value itself, with raw JSON and every value read lazily from a document
// decoded as encoding/json decodes it. A container is copied only where it holds
// one of those.
func jsonTreeJSON(v any) (any, error) {
	t, _, err := jsonTreeJSONIn(v)
	return t, err
}

// jsonTreeJSONIn is jsonTreeJSON, and whether the tree is other than v.
func jsonTreeJSONIn(v any) (any, bool, error) {
	switch x := v.(type) {
	case nil, bool, string, float64:
		return v, false, nil
	case json.Number:
		if x == "" {
			return json.Number("0"), true, nil
		}
		return x, false, nil
	case []any:
		var out []any
		for i := range x {
			t, changed, err := jsonTreeJSONIn(x[i])
			if err != nil {
				return nil, false, err
			}
			if changed && out == nil {
				out = make([]any, len(x))
				copy(out, x)
			}
			if changed {
				out[i] = t
			}
		}
		if out == nil {
			return x, false, nil
		}
		return out, true, nil
	case map[string]any:
		var out map[string]any
		// maporder: replaces members under their own keys, and copies the map on the first; which member is first changes only when the copy is taken, not what it holds.
		for k, e := range x {
			t, changed, err := jsonTreeJSONIn(e)
			if err != nil {
				return nil, false, err
			}
			if !changed {
				continue
			}
			if out == nil {
				out = make(map[string]any, len(x))
				// maporder: copies the map, member for member.
				for k2, e2 := range x {
					out[k2] = e2
				}
			}
			out[k] = t
		}
		if out == nil {
			return x, false, nil
		}
		return out, true, nil
	case json.RawMessage:
		if x == nil {
			return nil, true, nil
		}
		t, err := jsonTreeRaw(x, false)
		return t, true, err
	case jsonLazy:
		t, err := jsonTreeRaw(x.d.raw(x.sp), !x.exact)
		return t, true, err
	}
	return nil, false, jsonNotJSON(v)
}

// jsonMatchesJSON is jsonMatchesConst for a value held as decoded JSON: its
// identity decides a mismatch, and a match is confirmed by comparing its tree
// with the member it shares that identity with.
func jsonMatchesJSON(v any, c *jsonConst) (bool, error) {
	id, err := jsonIDJSON(v)
	if err != nil {
		return false, err
	}
	if !jsonIDIn(id, c.ids) {
		return false, nil
	}
	t, err := jsonTreeJSON(v)
	if err != nil {
		return false, err
	}
	for i := range c.ids {
		if c.ids[i] == id && jsonTreeEqual(t, c.trees[i]) {
			return true, nil
		}
	}
	return false, nil
}

// jsonFirstDuplicateJSON is jsonFirstDuplicate for the elements of an array held
// as decoded JSON: the index of the first element equal to an earlier one, or
// -1. Two elements sharing an identity are compared as trees before they are
// called duplicates.
func jsonFirstDuplicateJSON(arr []any) (int, error) {
	if len(arr) < 2 {
		return -1, nil
	}
	ids := make([]jsonID, len(arr))
	for i := range arr {
		id, err := jsonIDJSON(arr[i])
		if err != nil {
			return -1, err
		}
		ids[i] = id
	}
	same := func(i, j int) (bool, error) {
		ti, err := jsonTreeJSON(arr[i])
		if err != nil {
			return false, err
		}
		tj, err := jsonTreeJSON(arr[j])
		if err != nil {
			return false, err
		}
		return jsonTreeEqual(ti, tj), nil
	}
	if len(ids) <= 8 {
		for j := 1; j < len(ids); j++ {
			for i := 0; i < j; i++ {
				if ids[i] != ids[j] {
					continue
				}
				if eq, err := same(i, j); err != nil || eq {
					return j, err
				}
			}
		}
		return -1, nil
	}
	first := make(map[jsonID]int, len(ids))
	for j, id := range ids {
		i, ok := first[id]
		if !ok {
			first[id] = j
			continue
		}
		for k := i; k < j; k++ {
			if ids[k] != id {
				continue
			}
			if eq, err := same(k, j); err != nil || eq {
				return j, err
			}
		}
	}
	return -1, nil
}
