package generator

// Config holds configuration for code generation.
type Config struct {
	PackageName      string // Go package name for generated code
	OutputDir        string // Output directory
	OmitEmpty        bool   // Add omitempty to optional fields
	StrictProperties bool   // When true, absent additionalProperties is treated as false (no overflow map).
	//                        When false (default), absent additionalProperties follows JSON Schema spec
	//                        (defaults to true), so an overflow map[string]json.RawMessage is added to
	//                        capture any extra properties during unmarshal and re-emit them during marshal.
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		PackageName:      "generated",
		OutputDir:        ".",
		OmitEmpty:        true,
		StrictProperties: false,
	}
}
