package testsupport

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
)

// ExtractRootTypeNameFromCode finds the root type in generated code.
// Prefers struct types with JSON tags, then any struct, then type aliases named "Root".
// Returns empty string if none found (does not call t.Fatal).
func ExtractRootTypeNameFromCode(code string) string {
	lines := strings.Split(code, "\n")

	// The generator always names the root type "Root". Check for it first
	// across all type declarations (struct, alias, defined type).
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "type Root ") {
			return "Root"
		}
	}

	// Fallback: find the last struct with JSON-tagged fields. Only a
	// declaration at the top level of the file is a candidate: a decoder
	// declares `type Alias T` inside its own body, which no caller can name.
	var lastType string
	var currentType string
	var hasJSONTag bool

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(line, "type ") && strings.Contains(trimmed, " struct {") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 2 {
				currentType = parts[1]
				hasJSONTag = false
			}
		}
		if currentType != "" && strings.Contains(trimmed, "`json:\"") {
			hasJSONTag = true
		}
		if trimmed == "}" && currentType != "" {
			if hasJSONTag {
				lastType = currentType
			}
			currentType = ""
		}
	}

	if lastType == "" {
		// Fallback: just find the last struct
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(line, "type ") && strings.Contains(trimmed, " struct {") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 2 {
					lastType = parts[1]
				}
			}
		}
	}

	if lastType == "" {
		// Final fallback: look for any type declaration (aliases, defined types).
		var lastAlias string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(line, "type ") && !strings.Contains(trimmed, " struct {") && !strings.Contains(trimmed, " interface {") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 3 {
					lastAlias = parts[1]
				}
			}
		}
		lastType = lastAlias
	}

	return lastType
}

// HasValidateMethod checks if generated Go code contains a Validate() method.
func HasValidateMethod(code string) bool {
	// Check that the root type (identified by ExtractRootTypeNameFromCode) has a Validate() method.
	rootType := ExtractRootTypeNameFromCode(code)
	if rootType == "" {
		return false
	}
	// Look for "func (<recv> <RootType>) Validate() error {" pattern.
	// The receiver is typically a single lowercase letter.
	return strings.Contains(code, rootType+") Validate() error {")
}

// GoCmd runs `go <args...>` in dir, in testgo's environment with env appended,
// under a two-minute bound, and returns its combined output.
func GoCmd(t *testing.T, dir string, env []string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := testgo.Command(ctx, dir, args...)
	cmd.Env = append(cmd.Env, env...)
	return cmd.CombinedOutput()
}

// GenerateRoundTripMain creates a Go main() that:
// 1. Reads fixture.json
// 2. Unmarshals into the generated type
// 3. Marshals back to JSON
// 4. Compares original and round-tripped JSON for semantic equality
func GenerateRoundTripMain(rootType string) string {
	return GenerateRoundTripMainChecking(rootType, false)
}

// GenerateRoundTripMainChecking is GenerateRoundTripMain, which with
// checkIdentity also holds every value the fixture decodes into to its identity
// being that of what MarshalJSON writes; the package then carries
// IdentityCheckSource("main").
func GenerateRoundTripMainChecking(rootType string, checkIdentity bool) string {
	imports, identity := "", ""
	if checkIdentity {
		imports = "\n\t\"strings\""
		identity = `
	if diffs, _, _ := SchemagenIdentityDiffs(&obj); len(diffs) > 0 {
		fmt.Fprintf(os.Stderr, "IDENTITY MISMATCH\n%s\n", strings.Join(diffs, "\n"))
		os.Exit(1)
	}
`
	}
	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"%s
)

func main() {
	// Read fixture
	data, err := os.ReadFile("fixture.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading fixture: %%v\n", err)
		os.Exit(1)
	}

	// Unmarshal into typed struct
	var obj %s
	if err := json.Unmarshal(data, &obj); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal: %%v\n", err)
		os.Exit(1)
	}

	// Marshal back to JSON
	roundTripped, err := json.Marshal(obj)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %%v\n", err)
		os.Exit(1)
	}

	// Compare semantically: unmarshal both into any (handles objects, arrays, primitives)
	var original, result any
	if err := json.Unmarshal(data, &original); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal original: %%v\n", err)
		os.Exit(1)
	}
	if err := json.Unmarshal(roundTripped, &result); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal result: %%v\n", err)
		os.Exit(1)
	}

	if !reflect.DeepEqual(original, result) {
		fmt.Fprintf(os.Stderr, "ROUND-TRIP MISMATCH\n")
		fmt.Fprintf(os.Stderr, "Original:     %%s\n", string(data))
		fmt.Fprintf(os.Stderr, "Round-tripped: %%s\n", string(roundTripped))
		os.Exit(1)
	}
%s
	fmt.Println("PASS")
}
`, imports, rootType, identity)
}
