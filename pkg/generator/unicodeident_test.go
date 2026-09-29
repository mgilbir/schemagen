package generator

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode"

	"github.com/mgilbir/schemagen/pkg/generator/internal/unicodepin"
)

// TestUnicodeTablesArePinned holds unicode_tables.go to what it claims.
//
// Under a toolchain of the pinned Unicode version the file must be exactly what
// the renderer writes -- regenerated and compared -- and under the go.mod
// minimum's toolchain the running Unicode version must be the pinned one, which
// is what fails when the minimum moves and the tables do not. CI runs the suite
// under the minimum.
//
// Under any later toolchain the pin must still hold for that toolchain: every
// rune the pinned tables call a letter, a digit, upper or lower case, it calls
// the same, and every case mapping between two pinned letters is the one it
// uses. That is Unicode's stability policy, asserted rather than assumed: it is
// the whole argument for why the oldest version's tables answer for every later
// Go.
func TestUnicodeTablesArePinned(t *testing.T) {
	if unicode.Version == pinnedUnicodeVersion {
		want, err := unicodepin.Render()
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile("unicode_tables.go")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("unicode_tables.go is not what this toolchain (Unicode %s) renders; run `go generate ./pkg/generator`", unicode.Version)
		}
	}
	if minimum := goModMinimum(t); minimum != "" && strings.HasPrefix(runtime.Version(), "go"+minimum) {
		if unicode.Version != pinnedUnicodeVersion {
			t.Errorf("this is the go.mod minimum (go %s), whose Unicode is %s, and the tables are pinned to %s; regenerate them under this toolchain",
				minimum, unicode.Version, pinnedUnicodeVersion)
		}
	}

	// Letters, digits and upper case, which decide whether an identifier
	// compiles and whether a field is exported -- and so whether encoding/json
	// sees it at all. Lower case is not among them, because it is not stable:
	// U+0295 ʕ was Ll in Unicode 15 and is Lo from 16 on. The generator reads
	// lower case only to find word boundaries in a name it derives, where the
	// pinned answer is used under every Go, so the derivation is the same
	// whichever toolchain runs it, and a letter is a letter in either category.
	for _, pair := range []struct {
		name          string
		pinned, local *unicode.RangeTable
	}{
		{"letter", pinnedLetter, unicode.Letter},
		{"upper", pinnedUpper, unicode.Upper},
		{"digit", pinnedDigit, unicode.Digit},
	} {
		missing := 0
		for r := rune(0); r <= unicode.MaxRune; r++ {
			if unicode.Is(pair.pinned, r) && !unicode.Is(pair.local, r) {
				if missing < 5 {
					t.Errorf("%U is a pinned %s and not one to %s (Unicode %s)", r, pair.name, runtime.Version(), unicode.Version)
				}
				missing++
			}
		}
		if missing > 0 {
			t.Errorf("%d pinned %s runes are not %ss to the running toolchain: the pin no longer answers for every supported Go", missing, pair.name, pair.name)
		}
	}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if !identLetter(r) {
			continue
		}
		for _, c := range []struct {
			name         string
			pinned, here rune
		}{
			{"upper", identToUpper(r), unicode.ToUpper(r)},
			{"lower", identToLower(r), unicode.ToLower(r)},
		} {
			if c.pinned != c.here && identLetter(c.here) {
				t.Errorf("%U: the pinned %s case is %U and the running toolchain's is %U, both pinned letters", r, c.name, c.pinned, c.here)
			}
			if !identLetter(c.pinned) {
				t.Errorf("%U: the pinned %s case %U is not a pinned letter", r, c.name, c.pinned)
			}
		}
	}
}

// goModMinimum reads the major.minor of the module's go directive.
func goModMinimum(t *testing.T) string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "go "); ok {
			parts := strings.SplitN(strings.TrimSpace(v), ".", 3)
			if len(parts) >= 2 {
				return parts[0] + "." + parts[1]
			}
		}
	}
	t.Fatal("go.mod has no go directive")
	return ""
}

// TestNamesAndTagsUseOnlyPinnedUnicode puts runes whose standing differs
// between the oldest supported Go and later ones through every question the
// generator asks of a rune on its way into source. They are fixed here, not
// read off the running toolchain, so the test says the same under every Go.
func TestNamesAndTagsUseOnlyPinnedUnicode(t *testing.T) {
	const (
		tje       = 'Ᲊ' // CYRILLIC CAPITAL LETTER TJE: a letter from Unicode 16, not in 15
		ramsHornL = 'ɤ' // LATIN SMALL LETTER RAMS HORN: a letter in 15 with no upper case until 16
		ramsHornU = 'Ɤ' // LATIN CAPITAL LETTER RAMS HORN: its upper case, new in 16
	)
	if unicode.Is(pinnedLetter, tje) || unicode.Is(pinnedLetter, ramsHornU) {
		t.Fatalf("the fixtures assume Unicode 15 tables; the pin is %s", pinnedUnicodeVersion)
	}

	// A letter newer than the pin is punctuation to a derived name: dropped,
	// never spelled.
	if got := JSONPropertyToGoName("a" + string(tje) + "b"); strings.ContainsRune(got, tje) || got != "AB" {
		t.Errorf(`JSONPropertyToGoName("aᲉb") = %q; a Unicode 16 letter must not reach an identifier Go 1.25 compiles`, got)
	}
	// A case mapping newer than the pin is not applied: ɤ has no upper case in
	// Unicode 15, so the exported name takes the X prefix a caseless script does.
	if got := JSONPropertyToGoName(string(ramsHornL) + "x"); strings.ContainsRune(got, ramsHornU) || got != "X"+string(ramsHornL)+"x" {
		t.Errorf("JSONPropertyToGoName(%q) = %q, want %q: uppercasing onto U+A7CB spells a letter Go 1.25 lacks", string(ramsHornL)+"x", got, "X"+string(ramsHornL)+"x")
	}
	if got := receiverSafeLower(ramsHornU); got == ramsHornL {
		t.Errorf("IdentifierToLower(U+A7CB) = U+0264, a mapping Unicode 15 does not have")
	}
	// A tag holding a newer letter goes to the hand-written path: Go 1.25's
	// encoding/json would drop it and read the property by the field name.
	if tagNameIsRepresentable("a" + string(tje)) {
		t.Errorf("a struct tag naming %q was accepted; Go 1.25's encoding/json ignores it", "a"+string(tje))
	}
	if !tagNameIsRepresentable("a" + string(ramsHornL)) {
		t.Errorf("a struct tag naming %q was refused; it is a letter in every supported Go", "a"+string(ramsHornL))
	}
	// And the identifier predicates the emitter and the CLI ask.
	if IsIdentifier("a" + string(tje)) {
		t.Errorf("IsIdentifier accepted a Unicode 16 letter")
	}
	if IsExportedIdentifier(string(ramsHornU) + "x") {
		t.Errorf("IsExportedIdentifier accepted a Unicode 16 upper-case letter")
	}
	if !IsExportedIdentifier("Éx") || !IsIdentifier("ɤx") {
		t.Errorf("the predicates refused letters every supported Go has")
	}
}

func receiverSafeLower(r rune) rune { return IdentifierToLower(r) }
