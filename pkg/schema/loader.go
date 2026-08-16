package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UnsupportedFormatError refuses a schema file whose extension names a format
// this package does not read.
//
// Typed rather than a bare string because the refusal crosses two boundaries.
// It travels through the resolver chain, where CompositeResolver stacks one
// answer per resolver and this one has to stay recognizable as one of them; and
// it reaches the CLI, whose advice for an unresolved reference is assembled from
// what is true of the references that failed. "Pass the referenced document as
// an input too" is exactly false here -- passing it produces this same refusal,
// from the other entry point -- so the advice has to be able to ask. Issue #330.
type UnsupportedFormatError struct {
	// Format is the format the extension claims, capitalized as the message
	// says it: "YAML".
	Format string
	// Ext is the extension that named it, lowercased.
	Ext string
}

func (e *UnsupportedFormatError) Error() string {
	return fmt.Sprintf("%s schema files are not yet supported", e.Format)
}

// unsupportedFormat reports the refusal for a path whose extension names a
// format this package does not read, and nil for one it will attempt to parse.
//
// Consulted from both places a schema document enters a run -- LoadFromFile for
// a document the caller listed, and FileResolver for one a $ref reached --
// because the two disagreeing about a single file is issue #330. A .yaml file
// holding a JSON body was refused as an input and generated from happily as a
// $ref target, so the file the tool said it could not read was read and its
// constraints enforced; and a genuine YAML document reached by $ref came back as
// "invalid character '$' looking for beginning of value" with advice about
// --allow-remote-refs underneath it, while the sentence the reader needed was
// written a few lines away in LoadFromFile and was not reachable from there.
//
// The extension decides, not the body, and that is the direction the tree
// already states: README's "How It Works" is explicit that input is JSON only,
// that .yaml and .yml are rejected, and that any other extension is parsed as
// JSON -- the wording the 2026-07-12 audit's C12 was closed with. Reading the
// bytes instead would make ".yaml" mean "readable if it happens to be JSON",
// which no caller can predict of a file set they did not write, and it would
// have to be taken back the day YAML is read as YAML rather than as whatever it
// parses as today. An unknown extension is still attempted, as it always was;
// nothing about it claims a format.
func unsupportedFormat(path string) error {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".yaml", ".yml":
		return &UnsupportedFormatError{Format: "YAML", Ext: ext}
	}
	return nil
}

// LoadFromFile reads a JSON Schema from the given file path.
// Currently only JSON files are supported.
func LoadFromFile(path string) (*Schema, error) {
	// Before the read, so that the answer for a .yaml file does not depend on
	// whether it happens to be there: the format is refused either way, and a
	// caller told "no such file" would go and create the one thing that cannot
	// help them.
	if err := unsupportedFormat(path); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading schema file: %w", err)
	}

	var s Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing schema JSON: %w", err)
	}

	return &s, nil
}
