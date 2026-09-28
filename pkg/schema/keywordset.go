package schema

// This file is how MarshaledKeywords reads a node without reflection.
//
// The answer -- which keys a marshal would write -- is a function of the
// node's fields and their json tags, and reflection was how it was read: a
// reflect.Value per field, per node, per question. That is exact and slow, and
// the keyword ledger asks it of every node of every document a run generates.
// So the same reading is written out as plain Go, one comparison per field,
// in keywordset_gen.go, and that file is generated from the struct's own tags
// by renderKeywordSet in keywordset_test.go. It is computed from the fields each time it is
// asked, so it cannot fall out of step with a field a rewrite or the generator
// set after parsing; TestKeywordSetFileIsCurrent fails when a field is added,
// removed or retagged and the file was not regenerated, and
// TestMarshaledKeywordsMatchesMarshaling holds the answer to a real marshal
// field by field.
//
//go:generate env SCHEMAGEN_REGENERATE=1 go test -run ^TestKeywordSetFileIsCurrent$ .
