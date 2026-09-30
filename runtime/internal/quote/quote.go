// Package quote bounds the text a document supplies when it is written into a
// message. It is internal to the runtime module; the runtime and its format
// checkers share it so that one rule cuts every message.
package quote

import (
	"strconv"
	"unicode/utf8"
)

// Quote writes a string taken from the document into a message:
// Go-quoted, and cut short when it is long. A message quoting a 600 KB value in
// full is a 600 KB error string, logged wherever the error goes. Up to 128
// bytes are quoted whole; a longer string is quoted to its first 64 bytes,
// backed off to the start of a character, followed by its length and the word
// "truncated", so the cut is deterministic and says that it happened.
func Quote(s string) string {
	if head, cut := Cut(s); cut {
		return strconv.Quote(head) + "... (" + strconv.Itoa(len(s)) + " bytes, truncated)"
	}
	return strconv.Quote(s)
}

// ClipText is the same rule for a message that writes the text as a
// step of a path rather than as a quoted value -- the runtime evaluator's
// "property k", the --strict-read-write walker's "a.k" -- and so leaves a short
// string exactly as it was.
func ClipText(s string) string {
	if head, cut := Cut(s); cut {
		return head + "... (" + strconv.Itoa(len(s)) + " bytes, truncated)"
	}
	return s
}

// Cut is the rule itself: a string of up to 128 bytes is kept whole,
// and a longer one is cut to its first 64, backed off to the start of a
// character so that the cut never splits one.
func Cut(s string) (string, bool) {
	const whole, head = 128, 64
	if len(s) <= whole {
		return s, false
	}
	cut := head
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// ClipErr bounds the message of an error from a parser the document's
// text was handed to. time.Parse, url.Parse, netip.ParseAddr and the IDNA
// profile all quote their input back, so wrapping one reintroduces the whole
// value Quote just cut short. The error is kept underneath for
// errors.Is and errors.As; only the text is cut, by the same rule and with the
// same marker.
func ClipErr(err error) error {
	msg := err.Error()
	if _, cut := Cut(msg); !cut {
		return err
	}
	return &clippedError{err: err, msg: ClipText(msg)}
}

type clippedError struct {
	err error
	msg string
}

func (e *clippedError) Error() string { return e.msg }
func (e *clippedError) Unwrap() error { return e.err }
