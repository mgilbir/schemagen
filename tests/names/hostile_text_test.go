package names

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// TestHostileSchemaTextNeverBecomesCode is the property test for the emitter's
// escaping. Every string below is placed at every position a schema can put
// text into generated Go -- property names at each position a name is read
// from, the keys of dependentSchemas, dependentRequired and patternProperties,
// a discriminator, $ref strings under --lenient-refs, patterns, enum and const
// strings, title, description, examples, $id, $anchor, default, format, the
// content keywords and $defs keys -- and the result must be one of two things:
//
//   - generation fails with an error from the schema or generator layers,
//     never from the emitter (an emitter failure is an escaping gap, or gofmt
//     choking on a raw dump of broken source); or
//   - the output parses, holds no identifier or import a hostile string spelled
//     (the canaries below are code that would compile if it escaped), declares
//     exactly the number and kinds of top-level declarations the same schema
//     with a benign string does, compiles, passes go vet, and -- under the
//     default configuration -- decodes, validates and re-encodes documents
//     that carry the hostile text as property names and values, under their
//     exact names.
//
// Before the escaping was made structural, the first two canaries compiled into
// the generated package: a dependentSchemas key ended the `//` comment it was
// written into and panicked in Validate, and a $ref string under --lenient-refs
// added an import at the top of the file. The backslash pattern failed with
// "unknown escape sequence" and a NUL in a description with "illegal byte order
// mark"-class errors followed by a raw dump of the source.
func TestHostileSchemaTextNeverBecomesCode(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs generated packages")
	}
	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	// The hybrid configuration's output imports the validation runtime, which
	// the cogen harness already knows how to provide.
	if err := writeCogenGoMod(dir, true); err != nil {
		t.Fatal(err)
	}

	type pkgInfo struct {
		name   string
		checks bool
	}
	var pkgs []pkgInfo
	for ci, cfg := range hostileConfigs {
		for hi, h := range hostileTexts {
			pkg := fmt.Sprintf("c%dh%02d", ci, hi)
			positions := hostilePositions
			// Positions a hostile string may legitimately make ungeneratable
			// -- a pattern that is not a regular expression, a $ref that is
			// not a URI -- are tried alone first. A clean refusal there drops
			// the position for this string; any other outcome is judged on
			// its own terms, and every position that remains goes into one
			// package so that one compilation covers them all.
			var kept []hostilePosition
			for _, p := range positions {
				if p.fallible {
					src, _, err := generateHostile(em, cfg.cfg, pkg, []hostilePosition{p}, h)
					if err != nil {
						requireCleanRefusal(t, cfg.name, p.name, h, err)
						continue
					}
					if p.noCompile {
						checkHostileAST(t, cfg.name+"/"+p.name+"/"+h.name, src)
						continue
					}
				}
				if p.noCompile {
					continue
				}
				kept = append(kept, p)
			}
			src, helpers, err := generateHostile(em, cfg.cfg, pkg, kept, h)
			if err != nil {
				t.Errorf("%s/%s: the positions that are never refused were refused together: %.600s", cfg.name, h.name, err)
				continue
			}
			label := cfg.name + "/" + h.name
			checkHostileAST(t, label, src)
			if helpers != nil {
				checkHostileAST(t, label+"/helpers", helpers)
			}

			// The benign twin: the same schema with the hostile text
			// replaced. Escaping changes how text is spelled, never what is
			// declared, so the hostile file declares nothing of any kind the
			// twin does not. It may declare less: a default of "" is the zero
			// value, and needs no SetDefaults to put it there.
			twinSrc, _, err := generateHostile(em, cfg.cfg, pkg, kept, hostileText{name: "benign", text: "benign"})
			if err != nil {
				t.Fatalf("%s: the benign twin was refused: %.600s", label, err)
			}
			got, want := declShape(t, src), declShape(t, twinSrc)
			for kind, n := range got {
				if n > want[kind] {
					t.Errorf("%s: declares %d top-level %s, where the benign twin declares %d\n got: %v\nwant: %v", label, n, kind, want[kind], got, want)
				}
			}

			pdir := filepath.Join(dir, pkg)
			if err := os.MkdirAll(pdir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pdir, "types.go"), src, 0o644); err != nil {
				t.Fatal(err)
			}
			if helpers != nil {
				if err := os.WriteFile(filepath.Join(pdir, "schemagen_helpers.go"), helpers, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			info := pkgInfo{name: pkg}
			if cfg.documents {
				info.checks = true
				if err := os.WriteFile(filepath.Join(pdir, "zz_checks.go"), []byte(hostileChecks(t, pkg, kept, h)), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			pkgs = append(pkgs, info)
		}
	}
	if t.Failed() {
		return
	}

	var main strings.Builder
	main.WriteString("package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n")
	for _, p := range pkgs {
		if p.checks {
			fmt.Fprintf(&main, "\t%q\n", "cogen_test/"+p.name)
		}
	}
	main.WriteString(")\n\nfunc main() {\n\tvar fails []string\n")
	for _, p := range pkgs {
		if p.checks {
			fmt.Fprintf(&main, "\tfails = append(fails, %s.ZZCheck()...)\n", p.name)
		}
	}
	main.WriteString("\tfor _, f := range fails {\n\t\tfmt.Println(f)\n\t}\n\tif len(fails) > 0 {\n\t\tos.Exit(1)\n\t}\n\tfmt.Println(\"PASS\")\n}\n")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd := testgo.Command(ctx, dir, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	// vet type-checks every package, so it is the compile of the ones main
	// does not import as well.
	run("vet", "./...")
	if out := programOutput([]byte(run("run", "."))); out != "PASS" {
		t.Fatalf("generated code mishandled hostile text:\n%s", out)
	}
	t.Logf("%d packages generated, compiled, vetted and run", len(pkgs))
}

// hostileText is one hostile string. text is what the schema file holds; when
// rawInvalid is set the file holds it as raw bytes that are not UTF-8, which
// encoding/json reads as U+FFFD, and the documents use that reading.
type hostileText struct {
	name       string
	text       string
	rawInvalid bool
}

// The canaries spell code that would compile if the text escaped its context:
// an identifier, a call, an import and an init function, each named so that
// checkHostileAST can find it.
var hostileTexts = []hostileText{
	{name: "empty", text: ""},
	{name: "newline", text: "a\nb"},
	{name: "cr", text: "a\rb"},
	{name: "crlf", text: "a\r\nb"},
	{name: "u2028", text: "a\xe2\x80\xa8b"},
	{name: "u2029", text: "a\xe2\x80\xa9b"},
	{name: "nul", text: "a\x00b"},
	{name: "bom", text: "\xef\xbb\xbfa"},
	{name: "bidi", text: "a\xe2\x80\xaeb"},
	{name: "tab", text: "a\tb"},
	{name: "close-comment", text: "a*/b"},
	{name: "open-comment", text: "a/*b"},
	{name: "line-comment", text: "a//b"},
	{name: "directive", text: "go:generate rm -rf x"},
	{name: "backtick", text: "a`b"},
	{name: "quote", text: `a"b`},
	{name: "backslash", text: `a\b`},
	{name: "digit-class", text: `^\d+$`},
	{name: "percent", text: "a%vb"},
	{name: "verbs", text: "%d%s%q%!%"},
	{name: "invalid-utf8", text: "a\xffb", rawInvalid: true},
	{name: "long", text: strings.Repeat("long", 1250)},
	{name: "inject-comment", text: "t\nvar INJECTEDVAR = INJECTEDCALL()\n//"},
	{name: "inject-import", text: "x\nimport _ \"INJECTEDPKG\"\n//"},
	{name: "inject-string", text: `"+INJECTEDCALL()+"`},
	{name: "inject-raw", text: "`+INJECTEDCALL()+`"},
	{name: "inject-block", text: "*/ INJECTEDCALL() /*"},
	{name: "inject-init", text: "x\n}\nfunc init() { INJECTEDCALL() }\nfunc _() {\n//"},
	{name: "inject-format", text: "%v\", INJECTEDCALL(), \""},
}

type hostileConfig struct {
	name string
	cfg  generator.Config
	// documents says whether the packages generated under this configuration
	// are run against the positions' documents. The others are compiled and
	// vetted only: a flag like --strict-properties changes which documents are
	// valid, and the documents are written for the default.
	documents bool
}

var hostileConfigs = []hostileConfig{
	{name: "default", documents: true, cfg: generator.Config{OmitEmpty: true, LenientRefs: true}},
	{name: "noomit-strictrw", cfg: generator.Config{OmitEmpty: false, StrictReadWrite: true, LenientRefs: true}},
	{name: "all", cfg: generator.Config{
		StrictProperties: true, BigIntSupport: true, LenientRefs: true,
		Validation: generator.ValidationModeHybrid,
	}},
}

// hostilePosition is one place a schema can put text. sub is the subschema of
// one property of the root; defs is anything it needs under the root's $defs.
// valid and invalid are values for that property, and wantErr, when set, is
// text the rejection of an invalid value has to contain.
type hostilePosition struct {
	name      string
	sub       func(h string) any
	defs      func(h string) map[string]any
	valid     func(h string) []any
	invalid   func(h string) []any
	wantErr   func(h string) string
	fallible  bool
	noCompile bool
}

type obj = map[string]any
type arr = []any

func vals(v ...any) func(string) []any { return func(string) []any { return v } }

var hostilePositions = []hostilePosition{
	{
		name: "property",
		sub: func(h string) any {
			return obj{"type": "object", "properties": obj{h: obj{"type": "string", "minLength": 1}}, "required": arr{h}}
		},
		valid:   func(h string) []any { return arr{obj{h: "v"}} },
		invalid: func(h string) []any { return arr{obj{h: ""}, obj{}} },
	},
	{
		name: "property-array",
		sub: func(h string) any {
			return obj{"type": "object", "properties": obj{h: obj{"type": "array", "items": obj{"type": "integer", "minimum": 1}}}}
		},
		valid:   func(h string) []any { return arr{obj{h: arr{1, 2}}, obj{}} },
		invalid: func(h string) []any { return arr{obj{h: arr{0}}} },
	},
	{
		name: "property-map-value",
		sub: func(h string) any {
			return obj{"type": "object", "additionalProperties": obj{"type": "object", "properties": obj{h: obj{"type": "integer"}}, "required": arr{h}}}
		},
		valid:   func(h string) []any { return arr{obj{"k": obj{h: 1}}} },
		invalid: func(h string) []any { return arr{obj{"k": obj{}}} },
	},
	{
		name: "property-nested",
		sub: func(h string) any {
			return obj{"type": "object", "properties": obj{h: obj{"type": "object", "properties": obj{h: obj{"type": "string"}}, "required": arr{h}}}}
		},
		valid:   func(h string) []any { return arr{obj{h: obj{h: "x"}}} },
		invalid: func(h string) []any { return arr{obj{h: obj{}}} },
	},
	{
		name: "additionalProperties-false",
		sub: func(h string) any {
			return obj{"type": "object", "properties": obj{h: obj{"type": "string"}}, "additionalProperties": false}
		},
		valid:   func(h string) []any { return arr{obj{h: "x"}} },
		invalid: func(h string) []any { return arr{obj{"zz-undeclared": 1}} },
	},
	{
		name: "unevaluatedProperties-false",
		sub: func(h string) any {
			return obj{"type": "object", "properties": obj{h: obj{"type": "string"}}, "unevaluatedProperties": false}
		},
		valid:   func(h string) []any { return arr{obj{h: "x"}} },
		invalid: func(h string) []any { return arr{obj{"zz-undeclared": 1}} },
	},
	{
		name:    "required-undeclared",
		sub:     func(h string) any { return obj{"type": "object", "required": arr{h}} },
		valid:   func(h string) []any { return arr{obj{h: 1}} },
		invalid: func(h string) []any { return arr{obj{}} },
	},
	{
		name: "dependentSchemas",
		sub: func(h string) any {
			return obj{"type": "object", "properties": obj{"a": obj{"type": "string"}}, "dependentSchemas": obj{h: obj{"required": arr{"a"}}}}
		},
		valid:   func(h string) []any { return arr{obj{h: 1, "a": "x"}, obj{"a": "x"}} },
		invalid: func(h string) []any { return arr{obj{h: 1}} },
	},
	{
		// The shape of the original report: the trigger key is not a declared
		// property, so under unevaluatedProperties it is refused.
		name: "dependentSchemas-unevaluated",
		sub: func(h string) any {
			return obj{"type": "object", "properties": obj{"a": obj{"type": "string"}}, "dependentSchemas": obj{h: obj{"properties": obj{"b": obj{}}}}, "unevaluatedProperties": false}
		},
		valid:   func(h string) []any { return arr{obj{"a": "x"}} },
		invalid: func(h string) []any { return arr{obj{h: 1}, obj{h: 1, "b": 2}} },
	},
	{
		// The trigger declared, so that it is the dependent schema that
		// decides whether "b" was evaluated.
		name: "dependentSchemas-trigger-evaluates",
		sub: func(h string) any {
			return obj{"type": "object", "properties": obj{"a": obj{"type": "string"}, h: obj{}}, "dependentSchemas": obj{h: obj{"properties": obj{"b": obj{}}}}, "unevaluatedProperties": false}
		},
		valid:   func(h string) []any { return arr{obj{h: 1, "b": 2}, obj{"a": "x"}} },
		invalid: func(h string) []any { return arr{obj{"b": 2}} },
	},
	{
		name:    "dependentRequired",
		sub:     func(h string) any { return obj{"type": "object", "dependentRequired": obj{h: arr{h + "2"}}} },
		valid:   func(h string) []any { return arr{obj{h: 1, h + "2": 2}, obj{}} },
		invalid: func(h string) []any { return arr{obj{h: 1}} },
	},
	{
		name: "if-then",
		sub: func(h string) any {
			return obj{"type": "object", "properties": obj{h: obj{"type": "string"}},
				"if": obj{"properties": obj{h: obj{"const": "a"}}, "required": arr{h}}, "then": obj{"required": arr{"b"}}}
		},
		valid:   func(h string) []any { return arr{obj{h: "x"}, obj{h: "a", "b": 1}} },
		invalid: func(h string) []any { return arr{obj{h: "a"}} },
	},
	{
		name:    "propertyNames-enum",
		sub:     func(h string) any { return obj{"type": "object", "propertyNames": obj{"enum": arr{h, "b"}}} },
		valid:   func(h string) []any { return arr{obj{h: 1}} },
		invalid: func(h string) []any { return arr{obj{"c": 1}} },
	},
	{
		name: "oneOf-property",
		sub: func(h string) any {
			return obj{"type": "object", "properties": obj{h: obj{"oneOf": arr{obj{"type": "string"}, obj{"type": "integer"}}}}}
		},
		valid:   func(h string) []any { return arr{obj{h: "s"}, obj{h: 1}, obj{}} },
		invalid: func(h string) []any { return arr{obj{h: true}} },
	},
	{
		name: "discriminator",
		sub: func(h string) any {
			branch := func(tag string, extra string, typ string) obj {
				return obj{"type": "object", "properties": obj{h: obj{"const": tag}, extra: obj{"type": typ}}, "required": arr{h, extra}}
			}
			return obj{"oneOf": arr{branch("a", "x", "string"), branch("b", "y", "integer")}, "discriminator": obj{"propertyName": h}}
		},
		valid:   func(h string) []any { return arr{obj{h: "a", "x": "s"}, obj{h: "b", "y": 1}} },
		invalid: func(h string) []any { return arr{obj{h: "c"}} },
	},
	{
		name: "discriminator-under-hostile-property",
		sub: func(h string) any {
			branch := func(tag string, extra string, typ string) obj {
				return obj{"type": "object", "properties": obj{"kind": obj{"const": tag}, extra: obj{"type": typ}}, "required": arr{"kind", extra}}
			}
			return obj{"type": "object", "properties": obj{h: obj{"oneOf": arr{branch("a", "x", "string"), branch("b", "y", "integer")}, "discriminator": obj{"propertyName": "kind"}}}}
		},
		valid:   func(h string) []any { return arr{obj{h: obj{"kind": "a", "x": "s"}}, obj{}} },
		invalid: func(h string) []any { return arr{obj{h: obj{"kind": "c"}}} },
	},
	{
		name:    "enum",
		sub:     func(h string) any { return obj{"enum": arr{h, "x"}} },
		valid:   func(h string) []any { return arr{h, "x"} },
		invalid: vals("y"),
	},
	{
		name:    "enum-typed",
		sub:     func(h string) any { return obj{"type": "string", "enum": arr{h, "x"}} },
		valid:   func(h string) []any { return arr{h, "x"} },
		invalid: vals("y"),
	},
	{
		name:    "const",
		sub:     func(h string) any { return obj{"const": h} },
		valid:   func(h string) []any { return arr{h} },
		invalid: vals("zz"),
	},
	{
		name: "const-in-object",
		sub: func(h string) any {
			return obj{"type": "object", "properties": obj{"c": obj{"type": "string", "const": h}}}
		},
		valid:   func(h string) []any { return arr{obj{"c": h}} },
		invalid: func(h string) []any { return arr{obj{"c": "zz"}} },
	},
	{
		name:    "contains-const",
		sub:     func(h string) any { return obj{"type": "array", "contains": obj{"const": h}} },
		valid:   func(h string) []any { return arr{arr{"q", h}} },
		invalid: func(h string) []any { return arr{arr{"q"}} },
	},
	{
		name:    "prefixItems-const",
		sub:     func(h string) any { return obj{"type": "array", "prefixItems": arr{obj{"const": h}}} },
		valid:   func(h string) []any { return arr{arr{h}} },
		invalid: func(h string) []any { return arr{arr{"q"}} },
	},
	{
		name:    "not-const",
		sub:     func(h string) any { return obj{"type": "string", "not": obj{"const": h}} },
		valid:   vals("q"),
		invalid: func(h string) []any { return arr{h} },
	},
	{
		name: "default",
		sub: func(h string) any {
			return obj{"type": "object", "properties": obj{"d": obj{"type": "string", "default": h}}}
		},
		valid: func(h string) []any { return arr{obj{"d": h}, obj{}} },
	},
	{
		name: "annotations",
		sub: func(h string) any {
			return obj{"type": "object", "title": h, "description": h, "$comment": h, "examples": arr{h, obj{h: h}},
				"properties": obj{h: obj{"type": "string", "title": h, "description": h, "examples": arr{h}, "deprecated": true, "readOnly": true}}}
		},
		valid: func(h string) []any { return arr{obj{}} },
	},
	{
		name:  "format",
		sub:   func(h string) any { return obj{"type": "string", "format": h} },
		valid: func(h string) []any { return arr{h} },
	},
	{
		name:  "content",
		sub:   func(h string) any { return obj{"type": "string", "contentMediaType": h, "contentEncoding": h} },
		valid: func(h string) []any { return arr{"x"} },
	},
	{
		name:     "pattern",
		sub:      func(h string) any { return obj{"type": "string", "pattern": h} },
		fallible: true,
	},
	{
		name: "pattern-non-object-branch",
		sub: func(h string) any {
			return obj{"anyOf": arr{obj{"type": "string", "pattern": h}, obj{"type": "integer"}}}
		},
		fallible: true,
	},
	{
		name: "pattern-patternProperties-value",
		sub: func(h string) any {
			return obj{"type": "object", "patternProperties": obj{"^x": obj{"type": "string", "pattern": h}}}
		},
		fallible: true,
	},
	{
		name:     "pattern-items",
		sub:      func(h string) any { return obj{"type": "array", "items": obj{"type": "string", "pattern": h}} },
		fallible: true,
	},
	{
		name:     "pattern-propertyNames",
		sub:      func(h string) any { return obj{"type": "object", "propertyNames": obj{"pattern": h}} },
		fallible: true,
	},
	{
		name:     "pattern-untyped",
		sub:      func(h string) any { return obj{"minLength": 1, "pattern": h} },
		fallible: true,
	},
	{
		// Not a type name any draft defines, so a reader may refuse it; one
		// that does not writes it into the message the runtime type check
		// reports.
		name:     "type-name-patternProperties-value",
		sub:      func(h string) any { return obj{"type": "object", "patternProperties": obj{"^x": obj{"type": h}}} },
		fallible: true,
	},
	{
		name:     "type-name-list",
		sub:      func(h string) any { return obj{"type": arr{h, "null"}} },
		fallible: true,
	},
	{
		name:     "patternProperties-key",
		sub:      func(h string) any { return obj{"type": "object", "patternProperties": obj{h: obj{"type": "string"}}} },
		fallible: true,
	},
	{
		name:     "ref-lenient",
		sub:      func(h string) any { return obj{"$ref": "other.json#/" + h} },
		fallible: true,
	},
	{
		// An unresolved ref at a position that needs a type name makes
		// --lenient-refs write a file that spells a name nothing declares, and
		// say so at the top: DOES NOT COMPILE. That banner lists the refs, so
		// it is checked for injection without being compiled.
		name:      "ref-lenient-undeclared",
		sub:       func(h string) any { return obj{"type": "array", "items": obj{"$ref": "other.json#/" + h}} },
		fallible:  true,
		noCompile: true,
	},
	{
		name: "id",
		sub: func(h string) any {
			return obj{"$id": "https://example.com/" + h + ".json", "type": "object", "properties": obj{"a": obj{"type": "string"}}}
		},
		fallible: true,
	},
	{
		name:     "anchor",
		sub:      func(h string) any { return obj{"$ref": "#" + h} },
		defs:     func(h string) map[string]any { return obj{"anchored": obj{"$anchor": h, "type": "string"}} },
		fallible: true,
	},
	{
		name: "defs-key",
		sub:  func(h string) any { return obj{"$ref": "#/$defs/" + jsonPointerToken(h)} },
		defs: func(h string) map[string]any {
			return obj{h: obj{"type": "object", "properties": obj{h: obj{"type": "string"}}}}
		},
		fallible: true,
	},
}

// jsonPointerToken escapes a $defs key for a JSON Pointer inside a URI
// fragment.
func jsonPointerToken(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-._~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// decoded is the text as a schema reader sees it.
func (h hostileText) decoded() string {
	if h.rawInvalid {
		return strings.ToValidUTF8(h.text, "\ufffd")
	}
	return h.text
}

// propName is the root property a position's subschema is placed under.
func propName(i int) string { return fmt.Sprintf("p%02d", i) }

// hostileSchema builds the root schema holding every position, with h in each.
func hostileSchema(positions []hostilePosition, h hostileText) []byte {
	const placeholder = "\u0001INVALID\u0001"
	text := h.text
	if h.rawInvalid {
		text = strings.ReplaceAll(h.text, "\xff", placeholder)
	}
	props := obj{}
	defs := obj{}
	for i, p := range positions {
		props[propName(i)] = p.sub(text)
		if p.defs != nil {
			for k, v := range p.defs(text) {
				defs[k] = v
			}
		}
	}
	root := obj{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object", "properties": props}
	if len(defs) > 0 {
		root["$defs"] = defs
	}
	raw, err := json.Marshal(root)
	if err != nil {
		panic(err)
	}
	if h.rawInvalid {
		quoted, _ := json.Marshal(placeholder)
		raw = bytes.ReplaceAll(raw, quoted[1:len(quoted)-1], []byte("\xff"))
	}
	return raw
}

// generateHostile runs the pipeline the CLI runs.
func generateHostile(em *emitter.Emitter, cfg generator.Config, pkg string, positions []hostilePosition, h hostileText) (src, helpers []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PANIC: %v", r)
		}
	}()
	var s schema.Schema
	if err := json.Unmarshal(hostileSchema(positions, h), &s); err != nil {
		return nil, nil, fmt.Errorf("schema: %w", err)
	}
	s.NormalizeForDraft(schema.DraftUnknown)
	s.ComputeBaseURIs(nil, &s)
	cfg.PackageName = pkg
	cfg.OutputDir = "."
	if cfg.RootTypeName == "" {
		cfg.RootTypeName = "Root"
	}
	ir, err := generator.New(cfg).Generate(&s)
	if err != nil {
		return nil, nil, fmt.Errorf("generator: %w", err)
	}
	src, err = em.Emit(ir)
	if err != nil {
		return nil, nil, err
	}
	helpers, ok, err := em.EmitHelpers(pkg, generator.HelpersReferencedBy(string(src)))
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		helpers = nil
	}
	return src, helpers, nil
}

// requireCleanRefusal holds a refusal to being the schema's fault: an error
// from the emitter means a value reached a template it could not be written
// by, and a panic is a panic.
func requireCleanRefusal(t *testing.T, cfg, pos string, h hostileText, err error) {
	t.Helper()
	msg := err.Error()
	if strings.Contains(msg, "emitter:") || strings.Contains(msg, "PANIC") || strings.Contains(msg, "raw output") {
		t.Errorf("%s/%s/%s: generation failed in the emitter rather than refusing the schema: %.600s", cfg, pos, h.name, msg)
	}
}

// allowedImports is every package generated code may import.
var allowedImports = map[string]bool{
	"bytes": true, "encoding/base64": true, "encoding/json": true, "errors": true, "fmt": true,
	"math": true, "math/big": true, "net/mail": true, "net/netip": true, "net/url": true,
	"reflect": true, "regexp": true, "sort": true, "strconv": true, "strings": true, "time": true,
	"unicode": true, "unicode/utf8": true,
	// The identity block: seeded hashes, and the lock and pointer a document
	// keeps its values' identities under.
	"hash/maphash": true, "sync": true, "sync/atomic": true,
	"github.com/mgilbir/goecma262": true, "github.com/mgilbir/goecma262/flags": true,
	"golang.org/x/net/idna": true, "github.com/mgilbir/schemagen/pkg/validationruntime": true,
}

// checkHostileAST parses generated source and fails on anything a hostile
// string could only have put there by escaping: an identifier or import it
// spelled, an import outside the set generated code uses, an init function.
func checkHostileAST(t *testing.T, label string, src []byte) {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.ParseComments)
	if err != nil {
		t.Errorf("%s: generated source does not parse: %v", label, err)
		return
	}
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if !allowedImports[path] {
			t.Errorf("%s: generated source imports %q", label, path)
		}
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "init" {
			t.Errorf("%s: generated source declares an init function", label)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && strings.Contains(id.Name, "INJECTED") {
			t.Errorf("%s: generated source holds the identifier %s, spelled by the schema", label, id.Name)
		}
		return true
	})
}

// declShape counts a file's top-level declarations by kind.
func declShape(t *testing.T, src []byte) map[string]int {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		t.Fatalf("generated source does not parse: %v", err)
	}
	counts := map[string]int{}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv != nil {
				counts["method"]++
			} else {
				counts["func"]++
			}
		case *ast.GenDecl:
			counts[d.Tok.String()] += len(d.Specs)
		}
	}
	return counts
}

// hostileChecks writes the package's ZZCheck, which runs every position's
// documents through the generated Root.
func hostileChecks(t *testing.T, pkg string, positions []hostilePosition, h hostileText) string {
	t.Helper()
	text := h.decoded()
	enc := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("encoding a document: %v", err)
		}
		return strconv.Quote(string(raw))
	}
	var valid, invalid []string
	for i, p := range positions {
		if p.valid != nil {
			for _, v := range p.valid(text) {
				valid = append(valid, enc(obj{propName(i): v}))
			}
		}
		if p.invalid != nil {
			want := ""
			if p.wantErr != nil {
				want = p.wantErr(text)
			}
			for _, v := range p.invalid(text) {
				invalid = append(invalid, fmt.Sprintf("{%s, %s}", enc(obj{propName(i): v}), strconv.Quote(want)))
			}
		}
	}
	return fmt.Sprintf(`package %s

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

func zzDecode(b []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	err := d.Decode(&v)
	return v, err
}

func zzValidate(v *Root) error {
	if x, ok := any(v).(interface{ Validate() error }); ok {
		return x.Validate()
	}
	return nil
}

// ZZCheck runs the documents for %s.
func ZZCheck() []string {
	var fails []string
	for _, in := range []string{%s} {
		var v Root
		if err := json.Unmarshal([]byte(in), &v); err != nil {
			fails = append(fails, fmt.Sprintf("%s: valid %%q refused by decode: %%v", in, err))
			continue
		}
		if err := zzValidate(&v); err != nil {
			fails = append(fails, fmt.Sprintf("%s: valid %%q refused by Validate: %%v", in, err))
			continue
		}
		out, err := json.Marshal(&v)
		if err != nil {
			fails = append(fails, fmt.Sprintf("%s: %%q: marshal: %%v", in, err))
			continue
		}
		a, _ := zzDecode([]byte(in))
		b, err := zzDecode(out)
		if err != nil || !reflect.DeepEqual(a, b) {
			fails = append(fails, fmt.Sprintf("%s: round trip changed the document\n  in:  %%q\n  out: %%q", in, out))
		}
	}
	for _, c := range []struct{ in, want string }{%s} {
		var v Root
		err := json.Unmarshal([]byte(c.in), &v)
		if err == nil {
			err = zzValidate(&v)
		}
		if err == nil {
			fails = append(fails, fmt.Sprintf("%s: invalid %%q accepted", c.in))
			continue
		}
		if c.want != "" && !strings.Contains(err.Error(), c.want) {
			fails = append(fails, fmt.Sprintf("%s: %%q refused with %%q, which does not say %%q", c.in, err.Error(), c.want))
		}
	}
	return fails
}
`, pkg, h.name, strings.Join(valid, ", "), pkg+"/"+h.name, pkg+"/"+h.name, pkg+"/"+h.name, pkg+"/"+h.name, strings.Join(invalid, ", "), pkg+"/"+h.name, pkg+"/"+h.name)
}

// TestReceiverNamesAreNeverShadowed compiles the same positions under a root
// type name beginning with every letter. Every type the schema declares is
// named under the root, so every method receiver becomes that letter, and a
// receiver a template's own local variable shadows -- `if v, ok := raw[...]`
// inside a method whose receiver is v -- does not compile.
//
// TestHostileSchemaTextNeverBecomesCode found the first of these: a $defs key
// the naming layer turned into a type named V..., whose hand-written decode of
// an untaggable property then read the property into the raw bytes it had just
// looked up.
func TestReceiverNamesAreNeverShadowed(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles generated packages")
	}
	em, err := emitter.New()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := writeCogenGoMod(dir, true); err != nil {
		t.Fatal(err)
	}
	// An untaggable name is what sends a property down the hand-written paths,
	// which are where the templates declare most of their locals.
	h := hostileText{name: "untaggable", text: "a,b"}
	var positions []hostilePosition
	for _, p := range hostilePositions {
		if !p.noCompile {
			positions = append(positions, p)
		}
	}
	for ci, cfg := range hostileConfigs {
		for l := 'a'; l <= 'z'; l++ {
			c := cfg.cfg
			c.RootTypeName = strings.ToUpper(string(l)) + "root"
			pkg := fmt.Sprintf("c%d%c", ci, l)
			src, helpers, err := generateHostile(em, c, pkg, positions, h)
			if err != nil {
				t.Fatalf("%s/%c: %v", cfg.name, l, err)
			}
			pdir := filepath.Join(dir, pkg)
			if err := os.MkdirAll(pdir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pdir, "types.go"), src, 0o644); err != nil {
				t.Fatal(err)
			}
			if helpers != nil {
				if err := os.WriteFile(filepath.Join(pdir, "schemagen_helpers.go"), helpers, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := testgo.Command(ctx, dir, "vet", "./...")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
}
