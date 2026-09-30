package format

import "github.com/mgilbir/schemagen/runtime/internal/quote"

// The rule that bounds document text in a message is in internal/quote, shared
// with the runtime. These are the spellings the checkers use for it.

func _schemagenQuote(s string) string { return quote.Quote(s) }

func _schemagenClipErr(err error) error { return quote.ClipErr(err) }
