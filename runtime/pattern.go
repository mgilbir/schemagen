package runtime

import (
	"errors"

	ecma262 "github.com/mgilbir/goecma262"
	ecmaflags "github.com/mgilbir/goecma262/flags"
)

// Pattern is one JSON Schema regular expression -- a "pattern", a
// "patternProperties" key, a "propertyNames" pattern -- compiled once, when the
// generated package is initialised (see CompilePattern), with the ECMA-262
// engine and the "u" flag JSON Schema calls for. Every check in a generated
// package that matches a pattern goes through one of the variables its helper
// file declares; nothing compiles a pattern where it is used.
//
// source is the text the engine was given and pattern the text the schema
// wrote, which is what a message quotes. They differ only for a pattern that
// escapes punctuation the "u" flag does not let it escape; see schemagen's
// README.
type Pattern struct {
	re      *ecma262.Regexp
	err     error
	pattern string
}

func _schemagenCompilePattern(pattern, source string) *_schemagenRegexp {
	re, err := ecma262.Compile(source, ecmaflags.Unicode)
	return &_schemagenRegexp{re: re, err: err, pattern: pattern}
}

// matches reports whether s contains a match -- a search, not a whole-string
// match, which is how JSON Schema applies a pattern.
//
// A non-nil error means there is no answer, which is not the same as "no": the
// engine ran out of its execution budget on this input (errors.Is the error
// ecma262.ErrStepLimit), or the pattern does not compile under the version of
// the engine this package was built with. Every caller reports it as an error
// of its own, so a value is never accepted, nor refused, on a match that was
// not decided.
func (p *_schemagenRegexp) matches(s string) (bool, error) {
	if p.err != nil {
		return false, &_schemagenPatternError{err: p.err, pattern: p.pattern, compile: true}
	}
	matched, err := p.re.MatchStringErr(s)
	if err != nil {
		return false, &_schemagenPatternError{err: err, pattern: p.pattern}
	}
	return matched, nil
}

// matchesValue is matches for a value decoded into any, as the checks written
// against an untyped value read it: a value that is not a string satisfies a
// pattern, which constrains strings only. It has no error result because its
// callers are boolean expressions; an undecided match is instead recorded in
// *undecided (the first one only, so the error reported does not depend on the
// order the expression is evaluated in) and answers true, and the caller
// returns *undecided before it acts on the expression's value.
func (p *_schemagenRegexp) matchesValue(v any, undecided *error) bool {
	s, ok := v.(string)
	if !ok {
		return true
	}
	matched, err := p.matches(s)
	if err != nil {
		if *undecided == nil {
			*undecided = err
		}
		return true
	}
	return matched
}

// _schemagenPatternError is a match with no answer. Its message names the
// pattern and not the value: the value can be arbitrarily long, and a match
// reached from inside a loop over an object's members would otherwise report
// whichever member the loop happened to reach first.
type _schemagenPatternError struct {
	err     error
	pattern string
	compile bool
}

func (e *_schemagenPatternError) Error() string {
	if e.compile {
		return "pattern " + e.pattern + " does not compile under the ECMA-262 engine this package is built with: " + e.err.Error()
	}
	return "pattern " + e.pattern + " could not be evaluated within the regular expression engine's step budget"
}

func (e *_schemagenPatternError) Unwrap() error { return e.err }

// SchemagenUndecided marks the error as a match with no answer, for
// _schemagenUndecided. It is exported so that a type generated into another
// package, whose Validate this package calls, is recognised by the same test:
// an unexported method belongs to the package that declares it.
func (e *_schemagenPatternError) SchemagenUndecided() bool { return true }

// _schemagenUndecided reports whether err is, or wraps, a pattern match the
// engine gave no answer for (see _schemagenRegexp.matches).
//
// It is asked wherever generated code reads an error as a boolean -- a branch
// of an anyOf or oneOf that did not decode or did not validate, a contains
// element that did not count -- because there such an error would silently
// become "this branch does not match", which is a verdict the engine never
// gave. Those sites return the error instead.
func _schemagenUndecided(err error) bool {
	var u interface{ SchemagenUndecided() bool }
	return errors.As(err, &u) && u.SchemagenUndecided()
}
