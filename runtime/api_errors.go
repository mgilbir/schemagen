package runtime

// The messages generated code reports a refusal in, and how each is put behind
// the path that reaches the value it was raised on.

// ValueErrorf builds a message about the value it is raised on rather than
// about a member inside it. A container puts its path in front of it with
// ": ".
func ValueErrorf(format string, args ...any) error { return jsonValueErrorf(format, args...) }

// ElemErrorf builds a message that opens with an accessor into the value it is
// raised on, such as "[2]", which a container's path is joined to with nothing
// between.
func ElemErrorf(format string, args ...any) error { return jsonElemErrorf(format, args...) }

// StepErrorf builds a message that already opens with a step into the value it
// is raised on -- a member of it, or the keyword a sub-schema that refused is
// filed under -- which a container's path is joined to with a ".".
func StepErrorf(format string, args ...any) error { return jsonStepErrorf(format, args...) }

// Pathf puts the path segment that reaches a value in front of an error the
// value itself raised, joined the way that error says it has to be.
func Pathf(err error, segFormat string, args ...any) error {
	return jsonPathf(err, segFormat, args...)
}

// ElemPathf is Pathf for a segment that is an accessor rather than a name.
func ElemPathf(err error, segFormat string, args ...any) error {
	return jsonElemPathf(err, segFormat, args...)
}

// ValueWrapf puts a sentence in front of an error's message without writing the
// text out, keeping how the error joins to a container's path.
func ValueWrapf(err error, prefix string) error { return jsonValueWrapf(err, prefix) }

// Wrapf is ValueWrapf for a message that opens with a member name.
func Wrapf(err error, prefix string) error { return jsonWrapf(err, prefix) }

// ElemWrapf is ValueWrapf for a message that opens with an accessor.
func ElemWrapf(err error, prefix string) error { return jsonElemWrapf(err, prefix) }

// Quote writes a string taken from the document into a message: Go-quoted, and
// cut short when it is long, so that an error about a huge value is not itself
// huge.
func Quote(s string) string { return _schemagenQuote(s) }

// ClipText is Quote for a message that writes the text as a step of a path
// rather than as a quoted value.
func ClipText(s string) string { return _schemagenClipText(s) }

// Undecided reports whether err is, or wraps, a pattern match the regular
// expression engine gave no answer for. Generated code asks it wherever an
// error is read as a boolean -- a union branch that did not decode, a contains
// element that did not count -- so that no answer is returned rather than read
// as "no".
func Undecided(err error) bool { return _schemagenUndecided(err) }

// CompilePattern compiles one JSON Schema regular expression with the ECMA-262
// engine and the "u" flag. pattern is the text the schema wrote, which messages
// quote; source is the text the engine is given. The generated helper file of a
// package calls it once per pattern, when the package is initialised. A pattern
// the engine cannot compile is not a panic: every match against it reports an
// error of its own.
func CompilePattern(pattern, source string) *Pattern {
	return _schemagenCompilePattern(pattern, source)
}

// Matches reports whether s contains a match -- a search, not a whole-string
// match, which is how JSON Schema applies a pattern. A non-nil error means
// there is no answer, which is not the same as "no": the engine ran out of its
// execution budget on this input, or the pattern does not compile.
func (p *Pattern) Matches(s string) (bool, error) { return p.matches(s) }

// MatchesValue is Matches for a value decoded into any: a value that is not a
// string satisfies a pattern. An undecided match is recorded in *undecided (the
// first one only) and answers true; the caller returns *undecided before it acts
// on the answer.
func (p *Pattern) MatchesValue(v any, undecided *error) bool { return p.matchesValue(v, undecided) }
