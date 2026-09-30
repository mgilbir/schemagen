package runtime

import (
	"fmt"
	"strings"
)

// PathError is a validation message that does not open with the name of a
// member, together with what has to be written between it and the path of
// whatever value contains it.
//
// A message that does open with a member name is joined to its container's path
// with a ".", which is how a caller reads "a.b.r" as three steps into their
// document. Two other kinds of message cannot be joined that way, and gluing a
// "." in front of either invents a step the document does not have:
//
//   - a message about the value itself ("length 2 is less than minimum 3", "too
//     many properties: 2 exceeds maximum 1"), which names no member at all and
//     is separated from the path by ": "; and
//   - a message opening with an accessor ("[2].name: ...", `["kk"]: ...`),
//     which is already the next step and needs nothing between.
//
// A type reports what it can see, and only its container knows the path that
// reaches it, so which of the three applies is a fact about the message that has
// to travel with it. See issues #279 and #280.
//
// A message a container put its path step in front of holds the step, and what
// joins it to the message it was put in front of, rather than the joined text:
// the text is written out once, when it is asked for. Joined as each container
// added its step, the message was copied once per level -- a refusal at the
// bottom of a document nested ten thousand deep cost a hundred megabytes of
// strings to report, however short the document.
type PathError struct {
	err  error
	sep  string
	seg  string
	join string
}

// Error writes the path and the message joined.
func (e PathError) Error() string {
	if e.seg == "" && e.join == "" {
		return e.err.Error()
	}
	var b strings.Builder
	var cur error = e
	for {
		p, ok := cur.(jsonPathError)
		if !ok {
			b.WriteString(cur.Error())
			return b.String()
		}
		b.WriteString(p.seg)
		b.WriteString(p.join)
		cur = p.err
	}
}

// Unwrap returns the error the path was put in front of.
func (e PathError) Unwrap() error { return e.err }

// JSONPathSeparator reports what to write between a containing value's path and
// this message. It is exported so that a type generated into another package is
// joined by the same rule -- an unexported method belongs to the package that
// declares it, and would not satisfy the interface across that boundary.
// Nothing outside generated code needs to call it.
func (e PathError) JSONPathSeparator() string { return e.sep }

// jsonValueErrorf builds a message about the value it is raised on rather than
// about a member inside it.
func jsonValueErrorf(format string, args ...any) error {
	return jsonPathError{sep: ": ", err: fmt.Errorf(format, args...)}
}

// jsonElemErrorf builds a message that opens with an accessor into the value it
// is raised on -- the spelling used where a container has no name of its own to
// report under, such as an array or map that is the whole document.
func jsonElemErrorf(format string, args ...any) error {
	return jsonPathError{sep: "", err: fmt.Errorf(format, args...)}
}

// jsonStepErrorf builds a message that already opens with a step into the value
// it is raised on -- a member of it, or the schema keyword a sub-schema that
// refused is filed under -- which a container's path is joined to with a ".".
//
// A "." is what an unmarked message is joined with anyway, so this records what
// was going to happen rather than changing it. Recorded rather than left
// implicit because unmarked is also what a refusal from encoding/json or from a
// leaf's own parser looks like, and that one is a sentence: the two can only be
// told apart by the message saying which it is. See jsonDecodeRefusal.
func jsonStepErrorf(format string, args ...any) error {
	return jsonPathError{sep: ".", err: fmt.Errorf(format, args...)}
}

// jsonPathf puts the path segment that reaches a value in front of an error the
// value itself raised, joined the way that error says it has to be.
//
// What it builds opens with a member name, and says so, so that a container of
// its own joins it with the "." a member name takes. That was already the
// default for an unmarked error, and is recorded rather than assumed because a
// decode refusal arriving from encoding/json or from a leaf's own parser is
// unmarked too and is a sentence rather than a path -- the two have to be told
// apart, and only the message that is a path can say so. See jsonDecodeRefusal.
func jsonPathf(err error, segFormat string, args ...any) error {
	sep := "."
	if p, ok := err.(interface{ JSONPathSeparator() string }); ok {
		sep = p.JSONPathSeparator()
	}
	return jsonPathError{sep: ".", seg: fmt.Sprintf(segFormat, args...), join: sep, err: err}
}

// jsonElemPathf is jsonPathf for a segment that is an accessor rather than a
// name, so what it builds opens with an accessor too and its own container has
// to be told as much.
func jsonElemPathf(err error, segFormat string, args ...any) error {
	return jsonPathError{sep: "", err: jsonPathf(err, segFormat, args...)}
}

// jsonValueWrapf is jsonValueErrorf for a message that ends with another
// error's: prefix, then what err says, as fmt.Errorf(prefix+"%w", err) would
// write it and unwrapping to err as that would. Built as a step of the chain
// Error writes out once rather than as text, for the reason jsonPathError
// gives: a union refusing at every level of a recursive document puts each
// level's reason in front of the one below it.
func jsonValueWrapf(err error, prefix string) error {
	return jsonPathError{sep: ": ", seg: prefix, err: err}
}

// jsonWrapf is fmt.Errorf(prefix+"%w", err) built as a step of the chain, for
// the same reason: the text is prefix and then what err says, it unwraps to err,
// and a container joins its path to it with the "." it would join an unmarked
// error with. A patternProperties value that refuses at every level of a
// recursive document puts each level's key in front of the one below it.
func jsonWrapf(err error, prefix string) error {
	return jsonPathError{sep: ".", seg: prefix, err: err}
}

// jsonElemWrapf is jsonWrapf for a prefix that opens with an accessor, as
// jsonElemErrorf is for a message that does.
func jsonElemWrapf(err error, prefix string) error {
	return jsonPathError{sep: "", seg: prefix, err: err}
}
