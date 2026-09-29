package generator

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	ecma262 "github.com/mgilbir/goecma262"
	ecmaflags "github.com/mgilbir/goecma262/flags"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// A JSON Schema regular expression -- "pattern", a "patternProperties" key, a
// "propertyNames" pattern, wherever it sits -- is an ECMA-262 regular
// expression, compiled with the "u" flag (JSON Schema 2020-12 core, section
// 6.4; the earlier drafts name the same dialect). This file is the one place
// that says how one is compiled, for every consumer:
//
//   - generation, which refuses a schema whose pattern does not compile (see
//     checkSchemaPatterns), so that a bad pattern is a schema error with a JSON
//     Pointer rather than a panic in generated code or a rule dropped in silence;
//   - the generator's own decisions that must agree with generated code, such as
//     whether a declared property name falls under a patternProperties key
//     (see PatternMatches);
//   - generated code, which compiles each distinct pattern once, into a
//     package-level variable of the helper file, from the text
//     PatternEngineSource returns (see PatternVarName).
//
// Go's regexp package is not used for any of them: it is RE2, a different
// language, which rejects lookaround and backreferences and gives \s, \d, \w
// and case folding different meanings.

// patternFlags are the flags every schema pattern is compiled with.
const patternFlags = ecmaflags.Unicode

// PatternEngineSource returns the text the ECMA-262 engine is given for a
// schema's pattern, or the reason the pattern is not a regular expression.
//
// It is the pattern itself, byte for byte, whenever that compiles. Only a
// pattern the engine refuses is looked at again, for one construct: a
// backslash before an ASCII punctuation character that is not one of
// ECMA-262's syntax characters, such as `\:` or `\-` outside a class. The "u"
// flag makes those an error, and without the flag they are an identity escape
// meaning the character itself -- which is also what every Perl-derived
// dialect a schema might have been written against (PCRE, Python, Java, .NET,
// RE2) reads them as. There is no other reading to preserve, so each is
// rewritten as the \xHH escape of the same character, and the rewritten text
// is used only if the whole pattern then compiles. `^[A-Za-z0-9_\-\.\:]+$` is
// the shape this keeps working: common in published schemas, accepted by the
// validators that do not use the "u" flag, and a syntax error in those that do.
//
// Nothing else is rewritten. A backslash before a letter or a digit is not
// touched: `\e`, `\a`, `\h` and `\z` mean different things in different
// dialects, so reading one as its letter would be a guess, and the pattern is
// refused instead.
func PatternEngineSource(pattern string) (string, error) {
	_, err := ecma262.Compile(pattern, patternFlags)
	if err == nil {
		return pattern, nil
	}
	if rewritten, changed := escapeIdentityPunctuation(pattern); changed {
		if _, rerr := ecma262.Compile(rewritten, patternFlags); rerr == nil {
			return rewritten, nil
		}
	}
	return "", err
}

// ecmaSyntaxCharacters are the characters ECMA-262 lets a backslash escape in
// Unicode mode (SyntaxCharacter, plus "/"). An escape of any of them is
// already valid, so escapeIdentityPunctuation leaves it alone.
const ecmaSyntaxCharacters = `^$\.*+?()[]{}|/`

// escapeIdentityPunctuation rewrites every backslash-escaped ASCII punctuation
// character that is not an ECMA-262 syntax character as the \xHH escape of the
// same character, and reports whether it changed anything. See
// PatternEngineSource for why that preserves the pattern's only meaning.
//
// A backslash always consumes the character after it, so `\\:` is an escaped
// backslash followed by a plain colon and is left as it is.
func escapeIdentityPunctuation(pattern string) (string, bool) {
	var b strings.Builder
	changed := false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c != '\\' || i+1 >= len(pattern) {
			b.WriteByte(c)
			continue
		}
		next := pattern[i+1]
		i++
		if isASCIIPunctuation(next) && !strings.ContainsRune(ecmaSyntaxCharacters, rune(next)) {
			fmt.Fprintf(&b, `\x%02x`, next)
			changed = true
			continue
		}
		b.WriteByte(c)
		b.WriteByte(next)
	}
	return b.String(), changed
}

func isASCIIPunctuation(c byte) bool {
	return (c >= '!' && c <= '/') || (c >= ':' && c <= '@') || (c >= '[' && c <= '`') || (c >= '{' && c <= '~')
}

// PatternMatches reports whether a schema pattern matches s, as generated code
// would decide it. The error is non-nil when there is no answer: the pattern
// does not compile, or the engine ran out of its step budget -- which is not a
// "no".
func PatternMatches(pattern, s string) (bool, error) {
	src, err := PatternEngineSource(pattern)
	if err != nil {
		return false, err
	}
	re, err := ecma262.Compile(src, patternFlags)
	if err != nil {
		return false, err
	}
	return re.MatchStringErr(s)
}

// patternVarPrefix starts the name of every package-level variable generated
// code holds a compiled pattern in. HelpersReferencedBy reads the names back
// out of a generated file by it.
const patternVarPrefix = "_schemagenPattern_"

// patternVarNameLen is the length of one such name: the prefix and sixteen hex
// digits of the SHA-256 of the pattern.
const patternVarNameLen = len(patternVarPrefix) + 16

// patternRegistry maps a variable name PatternVarName handed out back to the
// pattern it names. The name is a digest of the pattern, so the map is a memo
// rather than state: the same pattern gets the same name in every process, and
// a second schema -- or a second file of the same package -- that uses a
// pattern refers to the one variable the package declares for it.
//
// It is how the helper file learns which patterns to compile. A generated file
// names the variables it uses and not the patterns behind them, and
// HelpersReferencedBy, which reads the file, asks the registry for each. The
// registry holds strings only, so what it keeps is proportional to the
// distinct patterns this process has emitted.
var patternRegistry sync.Map // variable name -> pattern

// PatternVarName returns the package-level variable generated code holds a
// schema pattern in, once compiled. The pattern must compile (see
// PatternEngineSource); one that does not has no variable, and the error says
// why.
func PatternVarName(pattern string) (string, error) {
	if _, err := PatternEngineSource(pattern); err != nil {
		return "", fmt.Errorf("pattern %q is not an ECMA-262 regular expression: %w", pattern, err)
	}
	sum := sha256.Sum256([]byte(pattern))
	name := patternVarPrefix + hex.EncodeToString(sum[:8])
	if prev, loaded := patternRegistry.LoadOrStore(name, pattern); loaded && prev.(string) != pattern {
		// Sixty-four bits of SHA-256: not expected in the lifetime of the
		// project, and refused rather than emitted as one variable serving
		// two patterns.
		return "", fmt.Errorf("patterns %q and %q have the same variable name %s", prev, pattern, name)
	}
	return name, nil
}

// patternsReferencedBy returns the patterns whose variables src names, sorted
// and without duplicates. A name the registry does not know -- which can only
// be a file this process did not emit -- is skipped, and the package that
// needs it then fails to build rather than matching against nothing.
func patternsReferencedBy(src string) []string {
	var out []string
	for rest := src; ; {
		i := strings.Index(rest, patternVarPrefix)
		if i < 0 {
			break
		}
		rest = rest[i:]
		if len(rest) < patternVarNameLen {
			break
		}
		name := rest[:patternVarNameLen]
		rest = rest[len(patternVarPrefix):]
		if !isLowerHex(name[len(patternVarPrefix):]) {
			continue
		}
		if p, ok := patternRegistry.Load(name); ok {
			out = append(out, p.(string))
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}

// checkSchemaPatterns refuses the pattern keywords of one schema node that are
// not ECMA-262 regular expressions: its "pattern" and the keys of its
// "patternProperties". A "propertyNames" pattern is the "pattern" of the
// propertyNames subschema, and is reached when the walk reaches that node.
//
// ptr is where the document wrote the node (see nullWalk.at), which the error
// names together with the keyword, encoded as every other refusal on the walk
// encodes a pointer (below), so that the report says where in the document to
// look.
func checkSchemaPatterns(s *schema.Schema, ptr string) error {
	if s.Pattern != nil {
		if _, err := PatternEngineSource(*s.Pattern); err != nil {
			return fmt.Errorf("%s: %q is not an ECMA-262 regular expression (u flag): %w", below(ptr, "pattern"), *s.Pattern, err)
		}
	}
	for _, key := range sortedKeys(s.PatternProperties) {
		if _, err := PatternEngineSource(key); err != nil {
			return fmt.Errorf("%s: %q is not an ECMA-262 regular expression (u flag): %w", below(ptr, "patternProperties", key), key, err)
		}
	}
	return nil
}

// declaredPatternMembers lists the properties s declares whose name one of its
// patternProperties keys matches; see DeclaredPatternMember.
//
// The match is decided here, by PatternMatches -- the engine and flags the
// generated check uses -- only to leave out the names no pattern can reach.
// The generated check matches again at run time, so this list can include a
// name wrongly without effect and must never leave one out: a name whose match
// has no answer here is kept.
func (g *Generator) declaredPatternMembers(s *schema.Schema, propNames []string, goFieldNames map[string]string, fieldTypes map[string]GoType, requiredSet map[string]bool) []DeclaredPatternMember {
	if len(s.PatternProperties) == 0 || !g.validationKeywordsEnabled() {
		return nil
	}
	patterns := sortedKeys(s.PatternProperties)
	var out []DeclaredPatternMember
	for _, name := range propNames {
		matched := false
		for _, pattern := range patterns {
			if m, err := PatternMatches(pattern, name); err != nil || m {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		member := DeclaredPatternMember{JSONName: name, Required: requiredSet[name]}
		if goName, ok := goFieldNames[name]; ok {
			if ft, ok := fieldTypes[goName]; ok {
				member.FieldName = goName
				member.FieldNilable = g.hasNilState(ft)
			}
		}
		out = append(out, member)
	}
	return out
}
