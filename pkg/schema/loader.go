package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadFromFile reads a JSON Schema from the given file path.
// Currently only JSON files are supported.
func LoadFromFile(path string) (*Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading schema file: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		// supported
	case ".yaml", ".yml":
		return nil, fmt.Errorf("YAML schema files are not yet supported")
	default:
		// Attempt JSON parsing for unknown extensions.
	}

	var s Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing schema JSON: %w", err)
	}

	return &s, nil
}
