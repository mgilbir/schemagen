package generator

import (
	"encoding/json"
	"math/big"
	"sort"
	"unicode/utf8"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// This file is the keyword ledger's answer to a keyword written beside an enum
// or a const that the generated code enforces: the instance can then only be
// one of those values, and a keyword every one of them satisfies demands
// nothing more. {"type":"string","enum":["abc","abcd"],"minLength":3} needs no
// length check once the enum is checked; {"enum":["a","abcd"],"minLength":3}
// does, and the ledger says so when there is none.
//
// Only the keywords a value can be judged against without a validator are
// read here. A keyword it cannot judge is simply not credited, which is the
// direction that reports rather than hides.

// allValuesSatisfy reports whether every value the instance can take satisfies
// keyword k of n, as far as that can be decided.
func allValuesSatisfy(n *schema.Schema, k string, values []any) bool {
	for _, v := range values {
		if ok, known := ledgerValueSatisfies(n, k, v); !ok || !known {
			return false
		}
	}
	return true
}

// ledgerValueSatisfies reports whether the JSON value v satisfies keyword k of
// n, and whether that could be decided at all.
func ledgerValueSatisfies(n *schema.Schema, k string, v any) (satisfied, known bool) {
	kind := kindOfValue(v)
	if kk, scoped := keywordKinds[k]; scoped && kind&kk == 0 {
		return true, true
	}
	switch k {
	case "type":
		return kind&kindsOfTypeList(n.Type) != 0 || (kind == kindInteger && kindsOfTypeList(n.Type)&kindNumeric != 0), true
	case "const":
		return valuesSubset([]any{v}, enumLikeValues(n)), true
	case "enum":
		return valuesSubset([]any{v}, n.Enum), true
	case "minLength", "maxLength":
		s, _ := v.(string)
		count := utf8.RuneCountInString(s)
		if k == "minLength" {
			return n.MinLength != nil && count >= n.MinLength.Int(), true
		}
		return n.MaxLength != nil && count <= n.MaxLength.Int(), true
	case "pattern":
		s, _ := v.(string)
		if n.Pattern == nil {
			return false, false
		}
		ok, err := PatternMatches(*n.Pattern, s)
		if err != nil {
			return false, false
		}
		return ok, true
	case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf":
		r, ok := valueRat(v)
		if !ok {
			return false, false
		}
		return numericKeywordHolds(n, k, r)
	case "minItems", "maxItems":
		a, _ := v.([]any)
		if k == "minItems" {
			return n.MinItems != nil && len(a) >= n.MinItems.Int(), true
		}
		return n.MaxItems != nil && len(a) <= n.MaxItems.Int(), true
	case "uniqueItems":
		a, _ := v.([]any)
		seen := make(map[string]bool, len(a))
		for _, e := range a {
			key := exactEnumValueKey(e)
			if seen[key] {
				return false, true
			}
			seen[key] = true
		}
		return true, true
	case "minProperties", "maxProperties", "required", "additionalProperties":
		m, _ := v.(map[string]any)
		switch k {
		case "minProperties":
			return n.MinProperties != nil && len(m) >= n.MinProperties.Int(), true
		case "maxProperties":
			return n.MaxProperties != nil && len(m) <= n.MaxProperties.Int(), true
		case "required":
			for _, name := range n.Required {
				if _, ok := m[name]; !ok {
					return false, true
				}
			}
			return true, true
		default:
			if !keywordBoolFalse(n, k) || len(n.PatternProperties) > 0 {
				return false, false
			}
			// maporder: a predicate; it answers the same whichever key it stops at.
			for key := range m {
				if _, declared := n.Properties[key]; !declared {
					return false, true
				}
			}
			return true, true
		}
	}
	return false, false
}

// numericKeywordHolds judges one numeric keyword against an exact value.
func numericKeywordHolds(n *schema.Schema, k string, r *big.Rat) (bool, bool) {
	bound := func(num *schema.Number) (*big.Rat, bool) {
		if num == nil {
			return nil, false
		}
		return num.Rat()
	}
	switch k {
	case "minimum":
		b, ok := bound(n.Minimum)
		if !ok {
			return false, false
		}
		if n.ExclusiveMinimum != nil && n.ExclusiveMinimum.Bool != nil && *n.ExclusiveMinimum.Bool {
			return r.Cmp(b) > 0, true
		}
		return r.Cmp(b) >= 0, true
	case "maximum":
		b, ok := bound(n.Maximum)
		if !ok {
			return false, false
		}
		if n.ExclusiveMaximum != nil && n.ExclusiveMaximum.Bool != nil && *n.ExclusiveMaximum.Bool {
			return r.Cmp(b) < 0, true
		}
		return r.Cmp(b) <= 0, true
	case "exclusiveMinimum":
		if n.ExclusiveMinimum == nil {
			return false, false
		}
		if n.ExclusiveMinimum.Number == nil {
			return numericKeywordHolds(n, "minimum", r)
		}
		b, ok := n.ExclusiveMinimum.Number.Rat()
		return ok && r.Cmp(b) > 0, ok
	case "exclusiveMaximum":
		if n.ExclusiveMaximum == nil {
			return false, false
		}
		if n.ExclusiveMaximum.Number == nil {
			return numericKeywordHolds(n, "maximum", r)
		}
		b, ok := n.ExclusiveMaximum.Number.Rat()
		return ok && r.Cmp(b) < 0, ok
	case "multipleOf":
		b, ok := bound(n.MultipleOf)
		if !ok || b.Sign() == 0 {
			return false, false
		}
		return new(big.Rat).Quo(r, b).IsInt(), true
	}
	return false, false
}

// valueRat is a JSON number value, exactly.
func valueRat(v any) (*big.Rat, bool) {
	switch x := v.(type) {
	case json.Number:
		return schema.Number(x).Rat()
	case schema.Number:
		return x.Rat()
	case float64:
		r := new(big.Rat)
		if r.SetFloat64(x) == nil {
			return nil, false
		}
		return r, true
	case int:
		return new(big.Rat).SetInt64(int64(x)), true
	case int64:
		return new(big.Rat).SetInt64(x), true
	}
	return nil, false
}

// intersectValues keeps the values of a that b also holds, by JSON equality.
func intersectValues(a, b []any) []any {
	keys := make(map[string]bool, len(b))
	for _, v := range b {
		keys[exactEnumValueKey(v)] = true
	}
	var out []any
	for _, v := range a {
		if keys[exactEnumValueKey(v)] {
			out = append(out, v)
		}
	}
	return out
}

// projectValues is what the values an instance can take say about one of its
// subschema positions: the member values of a property, the elements of an
// array, the values under keys no property declares. ok is false for a
// position the projection cannot follow.
func projectValues(values []any, parent *schema.Schema, keyword, key string) ([]any, bool) {
	var out []any
	switch keyword {
	case "properties":
		for _, v := range values {
			if m, isObj := v.(map[string]any); isObj {
				if member, has := m[key]; has {
					out = append(out, member)
				}
			}
		}
		return out, true
	case "additionalProperties":
		if len(parent.PatternProperties) > 0 {
			return nil, false
		}
		for _, v := range values {
			if m, isObj := v.(map[string]any); isObj {
				keys := make([]string, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					if _, declared := parent.Properties[k]; !declared {
						out = append(out, m[k])
					}
				}
			}
		}
		return out, true
	case "items":
		if parent.Items == nil || parent.Items.Schema == nil || len(parent.PrefixItems) > 0 {
			return nil, false
		}
		for _, v := range values {
			if a, isArr := v.([]any); isArr {
				out = append(out, a...)
			}
		}
		return out, true
	}
	return nil, false
}
