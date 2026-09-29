package testsupport

// IdentityCheckSource is a file for a generated package that compares, for
// every value reachable from a decoded document whose type reads its own
// identity, that identity with the identity of what MarshalJSON writes -- and
// the same for every value held as an any, which jsonIDAny reads.
//
// The values are then changed the ways a decoded document never changes them,
// one at a time, and compared again: every string member set to one that is not
// UTF-8 and wants escaping, every float and json.Number to spellings encoding/json
// writes differently from the literal, every time to one in a zone of its own,
// and every declared member set a second time through the overflow maps -- the
// rule by which the last value set is the one written.
func IdentityCheckSource(pkg string) string {
	return "package " + pkg + identityCheckBody
}

const identityCheckBody = `

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"
)

type schemagenIdentityCheck struct {
	diffs          []string
	values, changed int
}

func (c *schemagenIdentityCheck) compare(p reflect.Value, path, how string) {
	id, ok := p.Interface().(jsonIdentifier)
	if !ok {
		return
	}
	got, gErr := id.jsonIdentity(nil)
	b, mErr := json.Marshal(p.Interface())
	if (gErr == nil) != (mErr == nil) {
		c.diffs = append(c.diffs, fmt.Sprintf("%s%s (%s): identity error %v, marshal error %v", path, how, p.Type().Elem(), gErr, mErr))
		return
	}
	if gErr != nil {
		return
	}
	want, err := jsonIDRaw(b)
	if err != nil || got != want {
		c.diffs = append(c.diffs, fmt.Sprintf("%s%s (%s): identity is not that of %s", path, how, p.Type().Elem(), b))
	}
}

func (c *schemagenIdentityCheck) compareAny(v any, path string) {
	got, gErr := jsonIDAny(v, nil)
	b, mErr := json.Marshal(v)
	if (gErr == nil) != (mErr == nil) {
		c.diffs = append(c.diffs, fmt.Sprintf("%s (any): identity error %v, marshal error %v", path, gErr, mErr))
		return
	}
	if gErr == nil {
		want, err := jsonIDRaw(b)
		if err != nil || got != want {
			c.diffs = append(c.diffs, fmt.Sprintf("%s (any %T): identity is not that of %s", path, v, b))
		}
	}
}

// schemagenIdentityChanges lists the ways to change the value at rv -- a
// struct that reads its own identity -- each a function that changes one of its
// members and returns one that puts it back.
func schemagenIdentityChanges(rv reflect.Value) []func() func() {
	var out []func() func()
	set := func(f reflect.Value, v reflect.Value) func() func() {
		return func() func() {
			old := reflect.New(f.Type()).Elem()
			old.Set(f)
			f.Set(v)
			return func() { f.Set(old) }
		}
	}
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		f := rv.Field(i)
		switch {
		case f.Kind() == reflect.String && f.Type() == reflect.TypeOf(json.Number("")):
			for _, n := range []string{"1.50", "-0.0", "1e21", "0.000001", "12345678901234567890", ""} {
				out = append(out, set(f, reflect.ValueOf(json.Number(n))))
			}
		case f.Kind() == reflect.String:
			out = append(out, set(f, reflect.ValueOf("a\xffb<&> \x00").Convert(f.Type())))
		case f.Kind() == reflect.Pointer && f.Type().Elem().Kind() == reflect.String:
			s := reflect.New(f.Type().Elem())
			s.Elem().Set(reflect.ValueOf("\xc3\x28 \"quoted\"").Convert(f.Type().Elem()))
			out = append(out, set(f, s))
		case f.Kind() == reflect.Float64:
			for _, x := range []float64{math.Copysign(0, -1), 1e21, 1e-7, 0.1, math.NaN()} {
				out = append(out, set(f, reflect.ValueOf(x).Convert(f.Type())))
			}
		case f.Kind() == reflect.Pointer && f.Type().Elem() == reflect.TypeOf(time.Time{}):
			tm := time.Date(2001, 2, 3, 4, 5, 6, 700, time.FixedZone("x", 5*3600+30*60))
			out = append(out, set(f, reflect.ValueOf(&tm)))
		case f.Kind() == reflect.Map && (sf.Name == "AdditionalProperties" || sf.Name == "PatternProperties") &&
			f.Type().Key().Kind() == reflect.String && f.Type().Elem() == reflect.TypeOf(json.RawMessage(nil)):
			// Every declared member set a second time, through the map.
			for j := 0; j < t.NumField(); j++ {
				name, _, _ := strings.Cut(t.Field(j).Tag.Get("json"), ",")
				if name == "" || name == "-" {
					continue
				}
				k := reflect.ValueOf(name).Convert(f.Type().Key())
				out = append(out, func() func() {
					wasNil := f.IsNil()
					var old reflect.Value
					if wasNil {
						f.Set(reflect.MakeMap(f.Type()))
					} else if v := f.MapIndex(k); v.IsValid() {
						old = v
					}
					f.SetMapIndex(k, reflect.ValueOf(json.RawMessage(" [ 1.0 , {\"b\":2,\"a\":1,\"a\":3} ] ")))
					return func() {
						switch {
						case wasNil:
							f.Set(reflect.Zero(f.Type()))
						case old.IsValid():
							f.SetMapIndex(k, old)
						default:
							f.SetMapIndex(k, reflect.Value{})
						}
					}
				})
			}
		}
	}
	return out
}

func (c *schemagenIdentityCheck) walk(rv reflect.Value, path string, depth int) {
	if depth > 500 {
		return
	}
	if rv.CanAddr() {
		p := rv.Addr()
		if _, ok := p.Interface().(jsonIdentifier); ok {
			c.values++
			c.compare(p, path, "")
			if rv.Kind() == reflect.Struct {
				for _, change := range schemagenIdentityChanges(rv) {
					undo := change()
					c.changed++
					c.compare(p, path, " changed")
					undo()
				}
			}
		}
	}
	switch rv.Kind() {
	case reflect.Interface:
		if rv.IsNil() {
			return
		}
		if rv.Type().NumMethod() == 0 {
			c.values++
			c.compareAny(rv.Interface(), path)
		}
		e := reflect.New(rv.Elem().Type()).Elem()
		e.Set(rv.Elem())
		c.walk(e, path, depth+1)
	case reflect.Pointer:
		if !rv.IsNil() {
			c.walk(rv.Elem(), path, depth+1)
		}
	case reflect.Struct:
		for i := 0; i < rv.NumField(); i++ {
			if rv.Type().Field(i).IsExported() {
				c.walk(rv.Field(i), path+"."+rv.Type().Field(i).Name, depth+1)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			c.walk(rv.Index(i), fmt.Sprintf("%s[%d]", path, i), depth+1)
		}
	case reflect.Map:
		it := rv.MapRange()
		for it.Next() {
			v := reflect.New(rv.Type().Elem()).Elem()
			v.Set(it.Value())
			c.walk(v, fmt.Sprintf("%s[%v]", path, it.Key()), depth+1)
		}
	}
}

// SchemagenIdentityDiffs walks the value v points at. See identityCheckSource.
func SchemagenIdentityDiffs(v any) (diffs []string, values, changed int) {
	var c schemagenIdentityCheck
	c.walk(reflect.ValueOf(v).Elem(), "$", 0)
	return c.diffs, c.values, c.changed
}
`
