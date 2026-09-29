package generator

import "fmt"

// The identifiers generated code spells as fixed text, which the name registry
// holds before any schema-derived name is claimed (see names.go). A schema
// cannot move them: the templates that refer to them are not rewritten per
// schema, so a type, a constant or an import alias landing on one of them is a
// package that does not compile -- or, for an import alias, a file whose own
// helper calls resolve to the wrong package.
//
// The two tables below are held to the templates by tests in pkg/emitter that
// render every helper and read back what the rendering declares and imports, in
// both directions: a helper added to a template without being listed here fails,
// and an entry nothing declares any more fails too.

// generatedImport is one package a generated file may import, and the name the
// file spells it under.
type generatedImport struct {
	Path string
	// Name is the identifier the file refers to the package by.
	Name string
	// Alias is written in the import spec when the package's own name is not
	// Name (or is not derivable from the path, as for goecma262).
	Alias string
}

// generatedImports is every import a generated file -- a schema's file or the
// package's helper file -- can carry. addRequiredImports and the emitter's
// helper-file imports both go through GeneratedImport, which refuses a path
// that is not here, so an import cannot be added without its name being
// reserved.
var generatedImports = []generatedImport{
	{Path: "bytes", Name: "bytes"},
	{Path: "encoding/base64", Name: "base64"},
	{Path: "encoding/json", Name: "json"},
	{Path: "errors", Name: "errors"},
	{Path: "fmt", Name: "fmt"},
	{Path: "github.com/mgilbir/goecma262", Name: "ecma262", Alias: "ecma262"},
	{Path: "github.com/mgilbir/goecma262/flags", Name: "ecmaflags", Alias: "ecmaflags"},
	{Path: "github.com/mgilbir/schemagen/pkg/validationruntime", Name: "validationruntime"},
	{Path: "golang.org/x/net/idna", Name: "idna"},
	{Path: "math", Name: "math"},
	{Path: "math/big", Name: "big"},
	{Path: "net/mail", Name: "mail"},
	{Path: "net/netip", Name: "netip"},
	{Path: "net/url", Name: "url"},
	{Path: "reflect", Name: "reflect"},
	{Path: "sort", Name: "sort"},
	{Path: "strconv", Name: "strconv"},
	{Path: "strings", Name: "strings"},
	{Path: "time", Name: "time"},
	{Path: "unicode", Name: "unicode"},
	{Path: "unicode/utf8", Name: "utf8"},
}

// GeneratedImport returns the import spec a generated file uses for path. It
// panics for a path the generator has not reserved a name for: that is a
// template or an import model gaining a dependency without the name registry
// knowing, which would let a cross-package alias or a helper land on the name.
// The panic is reached only by schemagen's own code paths, never by input.
func GeneratedImport(path string) Import {
	for _, imp := range generatedImports {
		if imp.Path == path {
			return Import{Path: imp.Path, Alias: imp.Alias}
		}
	}
	panic(fmt.Sprintf("schemagen: generated code imports %q, which reserved.go does not list; add it to generatedImports so the name registry holds its name", path))
}

// GeneratedImportNames lists the name each reserved import is spelled under,
// for the tests that hold this table to the templates.
func GeneratedImportNames() map[string]string {
	out := make(map[string]string, len(generatedImports))
	for _, imp := range generatedImports {
		out[imp.Path] = imp.Name
	}
	return out
}

// helperIdentifiers is every package-level identifier a generated package's
// helper file declares, under any combination of helpers, and the ones the
// validation-capability block of a schema file declares.
var helperIdentifiers = []string{
	"SchemagenValidationCapability",
	"SchemagenValidationMode",
	"SchemagenValidationRuntimeFeatures",
	"_accessApply",
	"_accessFind",
	"_accessItems",
	"_accessKeyMatches",
	"_accessOther",
	"_accessPattern",
	"_accessProperty",
	"_accessRefuseReadOnly",
	"_accessRule",
	"_accessSameBytes",
	"_accessSortedKeys",
	"_accessStep",
	"_accessStripWriteOnly",
	"_accessTuple",
	"_boolPtr",
	"_decodeIgnoringReadOnly",
	"_dynConstOK",
	"_dynEqualsJSON",
	"_dynFormatOK",
	"_dynIsArray",
	"_dynIsBool",
	"_dynIsInteger",
	"_dynIsNumber",
	"_dynIsObject",
	"_dynIsString",
	"_dynItoa",
	"_dynJoin",
	"_dynMemberMatches",
	"_dynMemberNamed",
	"_dynMultipleOf",
	"_dynNumOK",
	"_dynNumber",
	"_dynResolveRef",
	"_dynRuneLen",
	"_dynSortedKeys",
	"_dynStrOK",
	"_dynStrOr",
	"_dynTypeMatches",
	"_dynTypeMatchesAny",
	"_dynUnique",
	"_dynamicRef",
	"_evalUndecidedError",
	"_schemaPatternMember",
	"_schemagenClipErr",
	"_schemagenClipText",
	"_schemagenClippedError",
	"_schemagenCut",
	"_schemagenQuote",
	"_schemagenUndecided",
	"_evalError",
	"_evalNode",
	"_evalNodeIn",
	"_evalResult",
	"_floatPtr",
	"_intPtr",
	"_isReadOnlyRefusal",
	"_jsonCanonical",
	"_jsonCanonicalNumber",
	"_jsonCanonicalNumberLimit",
	"_jsonCanonicalParts",
	"_jsonCanonicalTexts",
	"_jsonCanonicalValue",
	"_newEval",
	"_node",
	"_readOnlyRefusal",
	"_schemaAnchor",
	"_schemaDependency",
	"_schemaFrame",
	"_schemaMember",
	"_schemaNode",
	"_strPtr",
	"checkJSONNullsAt",
	"jsonAt",
	"jsonAtJSON",
	"jsonAttempt",
	"jsonDateTime",
	"jsonDateTimeRefusal",
	"jsonDecodeArticled",
	"jsonDecodeItems",
	"jsonDecodeMap",
	"jsonDecodePtr",
	"jsonDecodeRefusal",
	"jsonDecodeSlice",
	"jsonDecodeTokenName",
	"jsonDecodeTypeName",
	"jsonDecodeValue",
	"jsonDecodeValues",
	"jsonDoc",
	"jsonElemErrorf",
	"jsonElemPathf",
	"jsonIPAddrDecode",
	"jsonIPv4Addr",
	"jsonIPv6Addr",
	"jsonInteger",
	"jsonIntegerFromLiteral",
	"jsonIntegerMap",
	"jsonIntegerPtr",
	"jsonIntegerSlice",
	"jsonIsHex",
	"jsonIsNullDocument",
	"jsonIsScalarKind",
	"jsonIter",
	"jsonKey",
	"jsonKindError",
	"jsonMaxDepth",
	"jsonNullRule",
	"jsonNumber",
	"jsonNumberCmp",
	"jsonNumberIsMultipleOf",
	"jsonNumberParts",
	"jsonOpenDoc",
	"jsonOwnPackage",
	"jsonOwnType",
	"jsonPathError",
	"jsonPathf",
	"jsonPlainString",
	"jsonProbeLeaf",
	"jsonProbeMap",
	"jsonProbeSlice",
	"jsonScalarRefusal",
	"jsonScanKey",
	"jsonScanLiteral",
	"jsonScanNumber",
	"jsonScanString",
	"jsonSkipSpace",
	"jsonSmallIndex",
	"jsonSpan",
	"jsonStepErrorf",
	"jsonStringEnd",
	"jsonTextUnmarshalerType",
	"jsonTypeError",
	"jsonUnmarshalerType",
	"jsonValueErrorf",
	"jsonValueWrapf",
	"oneofDiscriminatorValue",
	"newJSONDoc",
	"oneofHasRequiredFields",
	"schemagenASCIIDigits",
	"schemagenAllASCII",
	"schemagenAtoi",
	"schemagenCheckEmail",
	"schemagenCheckFullTime",
	"schemagenCheckIDNA",
	"schemagenCheckURI",
	"schemagenContentString",
	"schemagenContextO",
	"schemagenDisallowedCodePoint",
	"schemagenDisallowedException",
	"schemagenDraft3CSSNumber",
	"schemagenDraft3ColorKeywords",
	"schemagenDraft3RGB",
	"schemagenDurationComponents",
	"schemagenFormatDate",
	"schemagenFormatDateTime",
	"schemagenFormatDraft3Color",
	"schemagenFormatDraft3Time",
	"schemagenFormatDuration",
	"schemagenFormatEmail",
	"schemagenFormatHostname",
	"schemagenFormatIDNEmail",
	"schemagenFormatIDNHostname",
	"schemagenFormatIPv4",
	"schemagenFormatIPv4Addr",
	"schemagenFormatIPv6",
	"schemagenFormatIPv6Addr",
	"schemagenFormatIRI",
	"schemagenFormatIRIReference",
	"schemagenFormatJSONPointer",
	"schemagenFormatRegex",
	"schemagenFormatRelativeJSONPointer",
	"schemagenFormatTime",
	"schemagenFormatURI",
	"schemagenFormatURIReference",
	"schemagenFormatURITemplate",
	"schemagenFormatUUID",
	"schemagenIDNAProfile",
	"schemagenIsALabel",
	"schemagenJSONPointerBody",
	"schemagenLabelSeparator",
	"schemagenPVALIDException",
	"schemagenURIChars",
	"schemagenURITemplateExpression",
	"schemagenURITemplateMaxLength",
	"schemagenURITemplateOperators",
	"schemagenURITemplateVarspec",
	"schemagenValidScheme",
}

// HelperIdentifiers lists helperIdentifiers, for the tests that hold it to the
// templates.
func HelperIdentifiers() []string {
	return append([]string(nil), helperIdentifiers...)
}

// predeclaredIdentifiers is Go's universe scope. Declaring one of them at
// package scope is legal Go and shadows it for the whole package -- including
// the generated code that calls len, compares to nil and converts to string --
// and a --schema-package named "string" or "len" is an import alias that does
// the same to one file. The list is the union over every Go release that can
// compile generated code; a test holds it to the running toolchain's
// types.Universe.
var predeclaredIdentifiers = []string{
	"any", "append", "bool", "byte", "cap", "clear", "close", "comparable",
	"complex", "complex128", "complex64", "copy", "delete", "error", "false",
	"float32", "float64", "imag", "int", "int16", "int32", "int64", "int8",
	"iota", "len", "make", "max", "min", "new", "nil", "panic", "print",
	"println", "real", "recover", "rune", "string", "true", "uint", "uint16",
	"uint32", "uint64", "uint8", "uintptr",
}

// PredeclaredIdentifiers lists predeclaredIdentifiers, for the test that holds
// it to the running toolchain.
func PredeclaredIdentifiers() []string {
	return append([]string(nil), predeclaredIdentifiers...)
}
