package generator

// Config holds configuration for code generation.
type Config struct {
	PackageName string // Go package name for generated code
	OutputDir   string // Output directory
	OmitEmpty   bool   // Add omitempty to optional fields
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		PackageName: "generated",
		OutputDir:   ".",
		OmitEmpty:   true,
	}
}
