// Package gentest reaches the settings of pkg/generator that exist for this
// repository's tests and nobody else.
//
// They cannot be exported from pkg/generator without becoming part of its API,
// and the tests that need them live in package tests, outside pkg/generator,
// where an unexported option cannot be named. An internal package is visible to
// both and to no caller outside this module: pkg/generator fills the variables
// below in an init function, and a test reads them. A GenerateOption is held as
// `any` because this package cannot import the one that fills it.
package gentest

// ForceEvaluator returns a GenerateOption that generates the root schema of a
// Generate call as the runtime evaluator's whole-schema literal -- the
// AnnotationSchemaDef every "the static path cannot carry this" arm falls back
// to -- in place of the static checks, so a test can judge a document by the
// evaluator schemagen ships and compare that verdict with an independent
// implementation's. Generate fails with an error naming the reason when the
// evaluator declines the schema.
var ForceEvaluator func() any

// LedgerRenderedRuleTypes returns the keyword ledger's table of which kinds of
// ValidationRule each list of rules in the IR is rendered for, by list name,
// each list sorted -- for the emitter test that holds the table to the
// templates. See pkg/generator/ledgerrender.go.
var LedgerRenderedRuleTypes func() map[string][]string

// LedgerTypedFormatDecodes returns the keyword ledger's table of whether the
// Go type a format is decoded into enforces the format, by format -- for the
// test that probes the generated decode. See pkg/generator/ledgerrender.go.
var LedgerTypedFormatDecodes func() map[string]bool
