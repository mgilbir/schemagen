package runtime

import "github.com/mgilbir/schemagen/runtime/internal/quote"

// The rule that bounds document text in a message is in internal/quote, which
// the format checkers share. These are the spellings the rest of this package
// uses for it.

func _schemagenQuote(s string) string { return quote.Quote(s) }

func _schemagenClipText(s string) string { return quote.ClipText(s) }

func _schemagenClipErr(err error) error { return quote.ClipErr(err) }
