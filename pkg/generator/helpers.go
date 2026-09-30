package generator

import (
	"slices"
	"sort"
	"strings"
)

// HelperSet records what the helper file of a generated package has to declare.
//
// Almost everything generated code leans on -- the in-place decoder, the
// encoder, the identity of a value, the exact number core, the format checkers,
// the runtime schema evaluator -- is ordinary Go in the runtime module
// (github.com/mgilbir/schemagen/runtime), which the generated code imports. What
// remains package-specific is what the schemas themselves supply: the regular
// expressions they name.
//
// Those are package-level variables, so emitting them into every file that needs
// them breaks as soon as two schemas in one package name the same pattern: the
// package then declares it twice and does not compile. They are collected here
// instead and written once per destination package.
type HelperSet struct {
	// Patterns are the schema regular expressions the package's generated code
	// matches with, sorted and without duplicates. The helper file declares one
	// package-level variable per pattern, compiled once when the package is
	// initialised, and the type that matches through it; generated code names
	// the variable (see PatternVarName) and never compiles a pattern itself.
	Patterns []string
}

// Empty reports whether no helpers are needed at all.
func (h HelperSet) Empty() bool {
	return len(h.Patterns) == 0
}

// Merge folds another set into this one.
func (h *HelperSet) Merge(other HelperSet) {
	h.Patterns = mergeSortedUnique(h.Patterns, other.Patterns)
}

// HelpersReferencedBy reports which of a package's helper declarations a
// generated file uses, read from the emitted source rather than from the IR it
// came out of.
//
// A generated file names the package-level variable each pattern is held in,
// and the registry PatternVarName filled while the file was rendered says which
// pattern that is. So the one reading here is both which patterns to compile and
// whether the helper file is needed at all.
//
// Reading the source cannot drift, because the thing being asked is exactly the
// thing that matters: does this file name that variable. The asymmetry makes
// over-matching safe -- a name appearing in a comment compiles a pattern that
// nothing uses, which Go permits -- and under-matching is a package that does
// not build.
func HelpersReferencedBy(src string) HelperSet {
	return HelperSet{Patterns: patternsReferencedBy(src)}
}

// FormatHelperName maps a "format" keyword to the runtime function that checks
// a string against it, or "" when this generator has no check for it.
//
// It is the other half of FormatCheckableOnString: a format that answers true
// there must have a name here, or a rule would be built and then render nothing.
// TestFormatHelperNamesCoverCheckableFormats holds the two together, and the
// runtime module's own tests hold each name to a function it exports.
func FormatHelperName(format string) string {
	switch format {
	case "date":
		return "rt.FormatDate"
	case "time":
		return "rt.FormatTime"
	case Draft3TimeFormat:
		return "rt.FormatDraft3Time"
	case Draft3ColorFormat:
		return "rt.FormatDraft3Color"
	case "date-time":
		return "rt.FormatDateTime"
	case "duration":
		return "rt.FormatDuration"
	case "email":
		return "rt.FormatEmail"
	case "idn-email":
		return "rt.FormatIDNEmail"
	case "hostname":
		return "rt.FormatHostname"
	case "idn-hostname":
		return "rt.FormatIDNHostname"
	case "uri":
		return "rt.FormatURI"
	case "iri":
		return "rt.FormatIRI"
	case "uri-reference":
		return "rt.FormatURIReference"
	case "iri-reference":
		return "rt.FormatIRIReference"
	case "uri-template":
		return "rt.FormatURITemplate"
	case "uuid":
		return "rt.FormatUUID"
	case "json-pointer":
		return "rt.FormatJSONPointer"
	case "relative-json-pointer":
		return "rt.FormatRelativeJSONPointer"
	case "regex":
		return "rt.FormatRegex"
	case "ipv4":
		return "rt.FormatIPv4"
	case "ipv6":
		return "rt.FormatIPv6"
	default:
		return ""
	}
}

// wrapProse breaks generator-written comment text into lines no wider than
// width, at spaces, and joins them with newlines.
//
// The emitter's `comment` function continues an embedded newline with the "//"
// of the line it is on, so a paragraph wrapped here arrives in the generated
// source as a Go comment block. Doing the wrapping here rather than in the
// template is what lets the text be assembled from a type name and an anchor
// whose lengths are not known until generation.
//
// Nothing is broken mid-word: a single word longer than width takes a line of
// its own and overruns it, which is right for the things that produce one --
// a Go identifier, a URI, a quoted anchor -- since splitting any of those makes
// the comment say something that is not there.
func wrapProse(text string, width int) string {
	var b strings.Builder
	col := 0
	for i, word := range strings.Fields(text) {
		switch {
		case i == 0:
			col = len(word)
		case col+1+len(word) > width:
			b.WriteString("\n")
			col = len(word)
		default:
			b.WriteString(" ")
			col += 1 + len(word)
		}
		b.WriteString(word)
	}
	return b.String()
}

// mergeSortedUnique returns the sorted union of two name lists.
func mergeSortedUnique(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	out := append(append([]string(nil), a...), b...)
	sort.Strings(out)
	return slices.Compact(out)
}
