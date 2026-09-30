package generator

import (
	"fmt"
	"math/big"
	"strings"
	"unicode/utf8"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// A "default" is an annotation: 2020-12 section 9.2 says it "SHOULD be valid
// against the associated schema", and a generator that plants one the schema
// refuses turns a document that was valid into one that is not. So SetDefaults
// plants a default only once it is known to be valid where it lands, and this
// file is where that is decided at generation time.
//
// The judge reads the schema the way the generated checks read it -- per node
// dialect, per format posture, with the validation vocabulary switched off where
// the document's metaschema leaves it out -- and answers one of three things. A
// default it can prove valid is planted; one it can prove invalid is not, and
// the reason is reported; one it cannot decide is compiled for the runtime
// evaluator and judged when SetDefaults runs (see defaultJudgeNode). What it
// cannot decide is what the generated code decides with machinery the generator
// does not have: an asserted format, the content vocabulary, a dynamic
// reference, and the two unevaluated keywords, whose answer is an evaluation of
// every applicator beside them.
//
// It answers only for the value it is given. Every combination is three-valued
// and never guesses: an allOf is invalid when any branch is and valid only when
// every branch is, an anyOf the other way round, and a result that turns on a
// branch the judge could not decide is undecided.

// judgement is the three-valued answer.
type judgement int

const (
	judgedUnknown judgement = iota
	judgedValid
	judgedInvalid
)

// maxJudgeDepth bounds the judge's recursion. The value is finite, so a walk
// that follows it down terminates by itself; the bound is for a schema that
// applies to the same value in place without end -- a $ref cycle through
// allOf -- which the active set below catches first, and for breadth.
const maxJudgeDepth = 256

type valueJudge struct {
	g       *Generator
	locator docLocator
	// active holds the in-place applications in progress, by node and by how
	// deep in the value they are, each with the number of branches (see
	// branches) open when it began: the same node applied to the same value
	// twice on one path is a cycle.
	active map[judgeFrame]int
	// branches counts the conditional applicators -- anyOf, oneOf, not,
	// if/then/else, dependentSchemas -- the current path is inside.
	branches int
	depth    int
}

type judgeFrame struct {
	node  *schema.Schema
	level int
}

// verdict is a judgement and, for an invalid one, why.
type verdict struct {
	j   judgement
	why string
}

var (
	valid   = verdict{j: judgedValid}
	unknown = verdict{j: judgedUnknown}
)

func invalid(why string) verdict { return verdict{j: judgedInvalid, why: why} }

// judgeValue decides whether v, a JSON value as the schema package decodes one
// (numbers as json.Number), is valid against every schema in schemas.
func (g *Generator) judgeValue(schemas []*schema.Schema, v any) verdict {
	j := &valueJudge{g: g, locator: docLocator{home: g.homeDoc}, active: map[judgeFrame]int{}}
	acc := valid
	for _, s := range schemas {
		acc = acc.and(j.judge(s, v, 0))
		if acc.j == judgedInvalid {
			return acc
		}
	}
	return acc
}

// and is the conjunction: invalid wins, then undecided.
func (a verdict) and(b verdict) verdict {
	switch {
	case a.j == judgedInvalid:
		return a
	case b.j == judgedInvalid:
		return b
	case a.j == judgedUnknown || b.j == judgedUnknown:
		return unknown
	}
	return valid
}

// at names a keyword of node for a reason: where the document wrote it, or the
// keyword alone for a node no document wrote.
func (j *valueJudge) at(node *schema.Schema, keyword string) string {
	if loc, ok := j.locator.name(node); ok {
		return fmt.Sprintf("%q at %s", keyword, below(loc, keyword))
	}
	return fmt.Sprintf("%q", keyword)
}

func (j *valueJudge) judge(node *schema.Schema, v any, level int) verdict {
	if node == nil {
		return valid
	}
	if node.BooleanSchema != nil {
		if *node.BooleanSchema {
			return valid
		}
		if loc, ok := j.locator.name(node); ok {
			return invalid("the schema at " + loc + " is false")
		}
		return invalid("the schema is false")
	}
	frame := judgeFrame{node: node, level: level}
	if opened, cycling := j.active[frame]; cycling {
		// A cycle through nothing but $ref and allOf repeats a conjunction
		// already being judged, and adds nothing to it: the schema is the
		// conjunction of what the loop passes through, which the frames below
		// are deciding. {"allOf":[{"$ref":"#"},{"type":"string"}]} is a
		// string. A cycle through a branch has no such reading -- anyOf over
		// itself is true or false depending on which fixed point one takes --
		// and is left undecided.
		if opened == j.branches {
			return valid
		}
		return unknown
	}
	if j.depth >= maxJudgeDepth {
		return unknown
	}
	j.active[frame] = j.branches
	j.depth++
	defer func() {
		delete(j.active, frame)
		j.depth--
	}()

	// A dynamic reference resolves by the path the evaluation took to it,
	// which no static reading states.
	if node.DynamicRef != "" || node.RecursiveRef != "" {
		return unknown
	}
	acc := valid
	add := func(r verdict) bool {
		acc = acc.and(r)
		return acc.j != judgedInvalid
	}
	if node.Ref != "" {
		_, target := j.g.referenceTargetUncounted(node)
		if target == nil {
			return unknown
		}
		if !add(j.judge(target, v, level)) {
			return acc
		}
		// Through draft 7 a $ref replaces everything written beside it.
		if j.g.refOverridesSiblingsForSchema(node) {
			return acc
		}
	}
	if len(node.TypeSchemas) > 0 {
		// Draft 3's schema-valued type entries: rare enough to leave to the
		// evaluator rather than model here.
		return unknown
	}
	if j.g.validationKeywordsEnabled() {
		if !add(j.assertions(node, v)) {
			return acc
		}
	}
	add(j.formatAndContent(node, v))
	if !add(j.applicators(node, v, level)) {
		return acc
	}
	return acc
}

// assertions are the validation vocabulary's keywords: what the value itself
// must be.
func (j *valueJudge) assertions(node *schema.Schema, v any) verdict {
	if len(node.Type) > 0 && !j.typeMatches(node, v) {
		return invalid("it is not of a type " + j.at(node, "type") + " admits")
	}
	if node.Enum != nil {
		found := false
		for _, member := range node.Enum {
			if jsonValuesEqual(member, v) {
				found = true
				break
			}
		}
		if !found {
			return invalid("it is not a member of " + j.at(node, "enum"))
		}
	}
	if node.ConstIsNull && v != nil {
		return invalid("it is not " + j.at(node, "const"))
	}
	if node.Const != nil && !jsonValuesEqual(*node.Const, v) {
		return invalid("it is not " + j.at(node, "const"))
	}
	acc := valid
	switch x := v.(type) {
	case string:
		acc = acc.and(j.stringAssertions(node, x))
	case []any:
		acc = acc.and(j.arrayAssertions(node, x))
	case map[string]any:
		acc = acc.and(j.objectAssertions(node, x))
	default:
		if n, ok := schemaNumber(v); ok {
			acc = acc.and(j.numberAssertions(node, n))
		}
	}
	return acc
}

func (j *valueJudge) typeMatches(node *schema.Schema, v any) bool {
	for _, t := range node.Type {
		switch t {
		case "any":
			return true
		case "null":
			if v == nil {
				return true
			}
		case "boolean":
			if _, ok := v.(bool); ok {
				return true
			}
		case "string":
			if _, ok := v.(string); ok {
				return true
			}
		case "array":
			if _, ok := v.([]any); ok {
				return true
			}
		case "object":
			if _, ok := v.(map[string]any); ok {
				return true
			}
		case "number":
			if _, ok := schemaNumber(v); ok && !isJSONNonNumber(v) {
				return true
			}
		case "integer":
			n, ok := schemaNumber(v)
			if !ok || isJSONNonNumber(v) {
				continue
			}
			if j.g.requiresStrictIntegerToken(node) {
				// Drafts 3 and 4 read an integer off the literal: 1.0 and 1e2
				// are numbers there, not integers.
				if !strings.ContainsAny(string(n), ".eE") {
					return true
				}
				continue
			}
			if r, ok := n.Rat(); ok && r.IsInt() {
				return true
			}
		}
	}
	return false
}

// isJSONNonNumber is the kinds schemaNumber must not be asked about: it reads a
// Go string as a number's literal, which a JSON string is not.
func isJSONNonNumber(v any) bool {
	switch v.(type) {
	case nil, bool, string, []any, map[string]any:
		return true
	}
	return false
}

func (j *valueJudge) numberAssertions(node *schema.Schema, n schema.Number) verdict {
	r, ok := n.Rat()
	if !ok {
		return unknown
	}
	cmp := func(bound *schema.Number) (int, bool) {
		b, ok := bound.Rat()
		if !ok {
			return 0, false
		}
		return r.Cmp(b), true
	}
	exclusive := func(b *schema.SchemaOrFloat) bool { return b != nil && b.Bool != nil && *b.Bool }
	if node.Minimum != nil {
		c, ok := cmp(node.Minimum)
		if !ok {
			return unknown
		}
		if c < 0 || (c == 0 && exclusive(node.ExclusiveMinimum)) {
			return invalid("it is below " + j.at(node, "minimum"))
		}
	}
	if node.Maximum != nil {
		c, ok := cmp(node.Maximum)
		if !ok {
			return unknown
		}
		if c > 0 || (c == 0 && exclusive(node.ExclusiveMaximum)) {
			return invalid("it is above " + j.at(node, "maximum"))
		}
	}
	if b := node.ExclusiveMinimum; b != nil && b.Number != nil {
		c, ok := cmp(b.Number)
		if !ok {
			return unknown
		}
		if c <= 0 {
			return invalid("it is not above " + j.at(node, "exclusiveMinimum"))
		}
	}
	if b := node.ExclusiveMaximum; b != nil && b.Number != nil {
		c, ok := cmp(b.Number)
		if !ok {
			return unknown
		}
		if c >= 0 {
			return invalid("it is not below " + j.at(node, "exclusiveMaximum"))
		}
	}
	if node.MultipleOf != nil {
		m, ok := node.MultipleOf.Rat()
		if !ok || m.Sign() <= 0 {
			return unknown
		}
		if !new(big.Rat).Quo(r, m).IsInt() {
			return invalid("it is not a multiple of " + j.at(node, "multipleOf"))
		}
	}
	return valid
}

func (j *valueJudge) stringAssertions(node *schema.Schema, s string) verdict {
	length := utf8.RuneCountInString(s)
	if node.MinLength != nil && length < node.MinLength.Int() {
		return invalid("it is shorter than " + j.at(node, "minLength"))
	}
	if node.MaxLength != nil && length > node.MaxLength.Int() {
		return invalid("it is longer than " + j.at(node, "maxLength"))
	}
	if node.Pattern != nil {
		matched, err := PatternMatches(*node.Pattern, s)
		switch {
		case err != nil:
			return unknown
		case !matched:
			return invalid("it does not match " + j.at(node, "pattern"))
		}
	}
	return valid
}

// formatAndContent is the format and content vocabularies, which are not the
// validation vocabulary and bind by their own posture. A format the generated
// code asserts is judged by a helper the generator does not run, and the
// content vocabulary likewise, so either leaves a string undecided.
func (j *valueJudge) formatAndContent(node *schema.Schema, v any) verdict {
	if _, isString := v.(string); !isString {
		return valid
	}
	if node.Format != nil && j.g.formatAssertsFor(node) && FormatCheckableOnString(*node.Format) {
		return unknown
	}
	if (node.ContentEncoding != "" || node.ContentMediaType != "" || node.ContentSchema != nil) && j.g.contentAssertsFor(node) {
		return unknown
	}
	return valid
}

func (j *valueJudge) arrayAssertions(node *schema.Schema, a []any) verdict {
	if node.MinItems != nil && len(a) < node.MinItems.Int() {
		return invalid("it has fewer items than " + j.at(node, "minItems"))
	}
	if node.MaxItems != nil && len(a) > node.MaxItems.Int() {
		return invalid("it has more items than " + j.at(node, "maxItems"))
	}
	if node.UniqueItems != nil && *node.UniqueItems {
		for i := range a {
			for k := i + 1; k < len(a); k++ {
				if jsonValuesEqual(a[i], a[k]) {
					return invalid("its items are not unique, as " + j.at(node, "uniqueItems") + " requires")
				}
			}
		}
	}
	return valid
}

func (j *valueJudge) objectAssertions(node *schema.Schema, o map[string]any) verdict {
	for _, name := range node.Required {
		if _, ok := o[name]; !ok {
			return invalid(fmt.Sprintf("it lacks %q, which %s names", name, j.at(node, "required")))
		}
	}
	if node.MinProperties != nil && len(o) < node.MinProperties.Int() {
		return invalid("it has fewer members than " + j.at(node, "minProperties"))
	}
	if node.MaxProperties != nil && len(o) > node.MaxProperties.Int() {
		return invalid("it has more members than " + j.at(node, "maxProperties"))
	}
	for _, key := range sortedKeys(node.DependentRequired) {
		if _, ok := o[key]; !ok {
			continue
		}
		for _, name := range node.DependentRequired[key] {
			if _, ok := o[name]; !ok {
				return invalid(fmt.Sprintf("it has %q and lacks %q, which %s names", key, name, j.at(node, "dependentRequired")))
			}
		}
	}
	return valid
}

// applicators are the keywords that apply subschemas, to the value in place or
// to the values inside it.
func (j *valueJudge) applicators(node *schema.Schema, v any, level int) verdict {
	acc := valid
	add := func(r verdict) bool {
		acc = acc.and(r)
		return acc.j != judgedInvalid
	}
	for _, sub := range node.AllOf {
		if !add(j.judge(sub, v, level)) {
			return acc
		}
	}
	// Everything below is a branch: see the cycle rule in judge.
	j.branches++
	defer func() { j.branches-- }()
	if len(node.AnyOf) > 0 {
		if !add(j.anyOf(node, v, level)) {
			return acc
		}
	}
	if len(node.OneOf) > 0 {
		if !add(j.oneOf(node, v, level)) {
			return acc
		}
	}
	if node.Not != nil {
		switch r := j.judge(node.Not, v, level); r.j {
		case judgedValid:
			return acc.and(invalid("it is valid against " + j.at(node, "not")))
		case judgedUnknown:
			acc = acc.and(unknown)
		}
	}
	if node.If != nil {
		var r verdict
		switch cond := j.judge(node.If, v, level); cond.j {
		case judgedValid:
			r = j.judge(node.Then, v, level)
		case judgedInvalid:
			r = j.judge(node.Else, v, level)
		default:
			// Either branch may apply: decided only where both agree.
			t, e := j.judge(node.Then, v, level), j.judge(node.Else, v, level)
			r = unknown
			if t.j == e.j && t.j != judgedUnknown {
				r = t
			}
		}
		if !add(r) {
			return acc
		}
	}
	switch x := v.(type) {
	case []any:
		add(j.arrayApplicators(node, x, level))
	case map[string]any:
		add(j.objectApplicators(node, x, level))
	}
	return acc
}

func (j *valueJudge) anyOf(node *schema.Schema, v any, level int) verdict {
	undecided := false
	for _, sub := range node.AnyOf {
		switch j.judge(sub, v, level).j {
		case judgedValid:
			return valid
		case judgedUnknown:
			undecided = true
		}
	}
	if undecided {
		return unknown
	}
	return invalid("it matches no branch of " + j.at(node, "anyOf"))
}

func (j *valueJudge) oneOf(node *schema.Schema, v any, level int) verdict {
	matched, undecided := 0, 0
	for _, sub := range node.OneOf {
		switch j.judge(sub, v, level).j {
		case judgedValid:
			matched++
		case judgedUnknown:
			undecided++
		}
	}
	switch {
	case matched > 1:
		return invalid("it matches more than one branch of " + j.at(node, "oneOf"))
	case undecided > 0:
		return unknown
	case matched == 0:
		return invalid("it matches no branch of " + j.at(node, "oneOf"))
	}
	return valid
}

func (j *valueJudge) arrayApplicators(node *schema.Schema, a []any, level int) verdict {
	acc := valid
	add := func(r verdict) bool {
		acc = acc.and(r)
		return acc.j != judgedInvalid
	}
	tuple := j.g.accessTupleOf(node)
	for i, slot := range tuple {
		if i >= len(a) {
			break
		}
		if !add(j.judge(slot, a[i], level+1)) {
			return acc
		}
	}
	var rest *schema.Schema
	switch {
	case node.Items != nil && node.Items.Schema != nil:
		rest = node.Items.Schema
	case j.g.additionalItemsApplies(node):
		rest = node.AdditionalItems.AsSchema()
	}
	if rest != nil {
		for i := len(tuple); i < len(a); i++ {
			if !add(j.judge(rest, a[i], level+1)) {
				return acc
			}
		}
	}
	if node.Contains != nil {
		matched, undecided := 0, 0
		for _, item := range a {
			switch j.judge(node.Contains, item, level+1).j {
			case judgedValid:
				matched++
			case judgedUnknown:
				undecided++
			}
		}
		least, most := 1, -1
		if j.g.validationKeywordsEnabled() {
			if node.MinContains != nil {
				least = node.MinContains.Int()
			}
			if node.MaxContains != nil {
				most = node.MaxContains.Int()
			}
		}
		switch {
		case matched+undecided < least:
			return acc.and(invalid("too few of its items match " + j.at(node, "contains")))
		case most >= 0 && matched > most:
			return acc.and(invalid("too many of its items match " + j.at(node, "contains")))
		case matched < least || (most >= 0 && matched+undecided > most):
			acc = acc.and(unknown)
		}
	}
	if node.UnevaluatedItems != nil && len(a) > 0 {
		acc = acc.and(unknown)
	}
	return acc
}

func (j *valueJudge) objectApplicators(node *schema.Schema, o map[string]any, level int) verdict {
	acc := valid
	add := func(r verdict) bool {
		acc = acc.and(r)
		return acc.j != judgedInvalid
	}
	for _, key := range sortedKeys(o) {
		member := o[key]
		claimed := false
		if sub, ok := node.Properties[key]; ok {
			claimed = true
			if !add(j.judge(sub, member, level+1)) {
				return acc
			}
		}
		for _, pat := range sortedKeys(node.PatternProperties) {
			matched, err := PatternMatches(pat, key)
			if err != nil {
				// The member may or may not be the pattern's, and may or may
				// not be a leftover.
				acc = acc.and(unknown)
				claimed = true
				continue
			}
			if matched {
				claimed = true
				if !add(j.judge(node.PatternProperties[pat], member, level+1)) {
					return acc
				}
			}
		}
		if !claimed && node.AdditionalProperties != nil {
			if !add(j.judge(node.AdditionalProperties.AsSchema(), member, level+1)) {
				return acc
			}
		}
		if node.PropertyNames != nil {
			if !add(j.judge(node.PropertyNames, key, level+1)) {
				return acc
			}
		}
	}
	for _, key := range sortedKeys(node.DependentSchemas) {
		if _, ok := o[key]; !ok {
			continue
		}
		if !add(j.judge(node.DependentSchemas[key], o, level)) {
			return acc
		}
	}
	if node.UnevaluatedProperties != nil && len(o) > 0 {
		acc = acc.and(unknown)
	}
	return acc
}

// jsonValuesEqual is JSON equality over values the schema package decoded:
// numbers by value, so 1 and 1.0 are one number, objects by members whatever
// their order, arrays element by element.
func jsonValuesEqual(a, b any) bool {
	na, aNum := schemaNumber(a)
	nb, bNum := schemaNumber(b)
	aNum = aNum && !isJSONNonNumber(a)
	bNum = bNum && !isJSONNonNumber(b)
	if aNum || bNum {
		if !aNum || !bNum {
			return false
		}
		ca, okA := na.CanonicalText()
		cb, okB := nb.CanonicalText()
		return okA && okB && ca == cb
	}
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonValuesEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		// maporder: a predicate; it returns the same answer whichever member it stops at.
		for k, xv := range x {
			yv, ok := y[k]
			if !ok || !jsonValuesEqual(xv, yv) {
				return false
			}
		}
		return true
	}
	return false
}
