package generator

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// Generator converts a parsed Schema into IR types.
type Generator struct {
	config                     Config
	output                     *File
	generated                  map[string]bool // track already-generated type names
	generating                 map[string]bool // track types currently being generated (recursion guard)
	defs                       map[string]*schema.Schema
	rootTypeName               string                // Go type name for the root schema
	rootID                     string                // $id of the root schema (for detecting self-references)
	anchors                    map[string]string     // anchor/id → def ref path (e.g., "#something" → "#/definitions/bar")
	dynamicAnchors             map[string]string     // $dynamicAnchor name → def ref path (e.g., "#items" → "#/$defs/items")
	resolver                   schema.SchemaResolver // external resolver for non-local refs
	baseURI                    *url.URL              // base URI for the root document (from $id or file path)
	rootSchema                 *schema.Schema        // the root schema for local ref resolution
	draft                      schema.Draft          // effective draft version of the root schema
	draftOverridden            bool                  // true when Config.Draft explicitly set the draft (takes precedence over $schema)
	resourceGraph              *schema.ResourceGraph // document/dialect/anchor graph for validation planning
	validationKeywordsDisabled bool                  // true when the declared metaschema omits the validation vocabulary

	// metaschemaVocabularies memoises declaredVocabulary by $schema URI,
	// including the misses. formatAssertsFor asks the question once per schema
	// node rather than once per document, and the resolver behind it may be
	// fetching over the network -- an HTTPResolver caches what it retrieves but
	// not what it failed to retrieve, so an unreachable metaschema would be
	// re-attempted, with its timeout, at every call site.
	metaschemaVocabularies map[string]map[string]bool

	// documentRoots maps canonical $id URIs to the schema nodes that declare them.
	// This enables scoped resolution: when a subschema has $id, $ref: "#/..."
	// within it resolves against that subschema, not the top-level root.
	documentRoots map[string]*schema.Schema

	// dynamicAnchorDecls memoises, by anchor name, every schema in the document
	// that declares it. The node builder's loop check asks the question once per
	// schema it walks past and the answer is a walk of the whole document, so
	// without this a schema with several dynamic references pays for it
	// quadratically. The empty name is the $recursiveAnchor namespace; see
	// dynamicAnchorDeclarations.
	dynamicAnchorDecls map[string][]*schema.Schema

	// dynamicScope tracks the stack of document roots entered via $ref during
	// code generation. This enables $dynamicRef to resolve against the dynamic
	// scope chain (walking from outermost to innermost) rather than only the
	// local document scope. The root schema's document root is always at index 0.
	dynamicScope []*schema.Schema

	// structsInProgress tracks Go type names for structs currently having their
	// fields resolved (on the call stack). Used to detect recursive value-type
	// cycles: if a resolved ref points to a type that references a type in this
	// set, a pointer must be used to break the cycle.
	structsInProgress map[string]bool

	// oneOfMemberNames counts the variant member names claimed on each parent
	// type, so that two oneOf groups on the same struct cannot claim the same
	// one. Every variant name becomes both a package-level wrapper type
	// (Parent_Name) and a method (Parent.GetName), and the vocabulary primitive
	// variants draw from is tiny — a struct with two scalar oneOf properties
	// named both of them "String" and emitted Go that does not compile. Keyed by
	// parent type name; the count drives the same numeric suffix already used
	// for duplicates inside one group.
	oneOfMemberNames map[string]map[string]int

	// appliedOverrides records which FieldNames overrides were actually used,
	// keyed by type name → JSON property name. The CLI inspects this after
	// generation to warn about configured overrides that matched no property.
	appliedOverrides map[string]map[string]bool

	// unenforced records the types that came out as `type X any` while their
	// schema still stated a constraint, so the CLI can say so. The type has no
	// Validate method and cannot fail to unmarshal, which makes this the one
	// kind of dropped check a caller has no way to notice.
	unenforced []UnenforcedSchema

	// unresolvedRefs records $ref values that resolveRefInContext could not
	// resolve anywhere (local defs, anchors, document roots, or the external
	// resolver). Unless Config.LenientRefs is set, Generate fails when this
	// is non-empty: an unresolvable ref means the generated code silently
	// degrades (any-typed fields, incomplete validation).
	unresolvedRefs map[string]bool

	// resolvedRefs records $ref values that resolved successfully in at least
	// one context, so a ref that failed against one context but succeeded
	// against another is not reported as unresolvable.
	resolvedRefs map[string]bool

	// crossPackageMisses records $ref targets owned by another package of a
	// cross-package run that were not registered by that package. Generate
	// fails on these: emitting a local copy instead would silently duplicate
	// the type across packages.
	crossPackageMisses map[crossPackageMiss]bool

	// typeSchemas records which schema node claimed each generated type name,
	// so callers can detect a name already taken by a different schema and
	// pick a disambiguated one instead of silently reusing the wrong type.
	typeSchemas map[string]*schema.Schema

	// nodeTypeNames is the inverse of typeSchemas: the canonical Go name a
	// schema node was first materialized under. A self-referential document
	// (every meta-schema is one) is reached by a different context-derived name
	// on each traversal, so without this a "$ref":"#" inside it mints a fresh
	// type every time -- the name growing by one segment per level -- and
	// generation never terminates.
	nodeTypeNames map[*schema.Schema]string

	// arrayTypeInferredFromBranch lists the schemas mergeAllOfBranches synthesized
	// and then typed "array" off a branch's items/prefixItems/contains, rather
	// than off a "type" anything declared. The distinction is the same one
	// generateTypeDef draws with inferTypeFromConstraints, and it decides
	// whether the Go type may refuse a non-array outright or has to be the
	// wrapper that accepts one. Keyed on the merged node, which is synthesized
	// per allOf and so cannot collide with another schema's answer.
	arrayTypeInferredFromBranch map[*schema.Schema]bool

	// patternMintedTypes maps a name minted for a patternProperties bucket to the
	// node it was minted from. Only names invented here are listed -- a bucket
	// whose sub-schema is a $ref uses the target's own name, which this mechanism
	// does not own and must not withdraw. resolvePatternPropertyTypes reads it to
	// take back a type it turns out cannot carry a Validate, so the package is
	// not left exporting a name nothing refers to.
	patternMintedTypes map[string]*schema.Schema

	// nodesInProgress marks schema nodes whose type is still being generated
	// further up the stack. A reference back to one of those closes a cycle, so
	// it must be emitted as a pointer -- Go rejects a struct that contains
	// itself by value ("invalid recursive type").
	nodesInProgress map[*schema.Schema]bool

	// rootNameOverride is the per-call root type name from WithRootTypeName;
	// unlike Config.RootTypeName it is reset on every Generate call.
	rootNameOverride string

	// crossImports maps foreign-package import paths to the aliases the
	// current file references them by; reset on every Generate call.
	crossImports map[string]string

	// nullChecked is the set of schema nodes already cleared of null
	// subschemas, shared by every checkNullSubschemas call of one Generate so
	// each node is walked once however many refs reach it.
	nullChecked map[*schema.Schema]bool

	// nullSubschemaErr holds the first null-subschema defect found in a
	// document that arrived through ref resolution rather than through
	// Generate's argument. Such a document never passed the up-front check --
	// a vendor keyword is only parsed as a schema when a $ref reaches into it,
	// and a resolver-fetched document was never part of the tree at all -- so
	// it is checked on arrival and refused. Generate reports this in preference
	// to the "cannot resolve $ref" that refusing it produces.
	nullSubschemaErr error
}

// New creates a new Generator with the given configuration.
func New(cfg Config) *Generator {
	return &Generator{
		config:             cfg,
		generated:          make(map[string]bool),
		generating:         make(map[string]bool),
		structsInProgress:  make(map[string]bool),
		unresolvedRefs:     make(map[string]bool),
		resolvedRefs:       make(map[string]bool),
		crossPackageMisses: make(map[crossPackageMiss]bool),
		typeSchemas:        make(map[string]*schema.Schema),
		nodeTypeNames:      make(map[*schema.Schema]string),
		patternMintedTypes: make(map[string]*schema.Schema),
		nodesInProgress:    make(map[*schema.Schema]bool),
		nullChecked:        make(map[*schema.Schema]bool),

		arrayTypeInferredFromBranch: make(map[*schema.Schema]bool),
	}
}

// Generate processes a schema and returns the IR File.
func (g *Generator) Generate(s *schema.Schema, opts ...GenerateOption) (*File, error) {
	var options generateOptions
	for _, opt := range opts {
		opt(&options)
	}
	// Per-call overrides must not leak into the next Generate call in
	// shared-types mode, where one generator runs several schemas. The root
	// name lives on the generator; the options that do have to reach the
	// config are restored when this call returns, so every option is
	// consistently scoped to the single call that passed it.
	g.rootNameOverride = options.rootTypeName
	if options.resolver != nil {
		prev := g.config.Resolver
		g.config.Resolver = options.resolver
		defer func() { g.config.Resolver = prev }()
	}
	if options.fieldNamesSet {
		prev := g.config.FieldNames
		g.config.FieldNames = options.fieldNames
		defer func() { g.config.FieldNames = prev }()
	}

	// Reject JSON nulls in schema positions before anything walks the tree.
	// Every traversal below (anchor indexing, resource graph, type generation)
	// assumes the elements of allOf/$defs/patternProperties/... are schemas, and
	// a null element is a nil *schema.Schema that panics on first use. Checking
	// once here, rather than at each of the dozen sites that iterate a schema
	// container, keeps the diagnosis in one place and guarantees no site is
	// missed. See checkNullSubschemas for why the nulls are an error and not
	// something to skip over.
	g.nullChecked = make(map[*schema.Schema]bool)
	g.nullSubschemaErr = nil
	if err := checkNullSubschemas(s, "#", g.nullChecked); err != nil {
		return nil, err
	}

	g.output = &File{
		PackageName: g.config.PackageName,
	}
	// In shared-types mode the generated/typeSchemas registries survive
	// across calls, so types materialized by an earlier schema of the same
	// package are referenced instead of re-emitted.
	if !g.config.SharedTypes {
		g.generated = make(map[string]bool)
	}
	g.generating = make(map[string]bool)
	g.crossImports = make(map[string]string)
	// Ref-resolution bookkeeping is per schema: in shared-types mode the same
	// generator runs several schemas, and carrying entries over would report an
	// earlier schema's unresolved refs against a later one.
	g.unresolvedRefs = make(map[string]bool)
	g.resolvedRefs = make(map[string]bool)
	g.crossPackageMisses = make(map[crossPackageMiss]bool)
	g.rootSchema = s
	if g.config.Draft != schema.DraftUnknown {
		g.draft = g.config.Draft
		g.draftOverridden = true
	} else {
		g.draft = schema.DetectDraft(s)
		g.draftOverridden = false
	}

	// Determine root type name.
	g.rootTypeName = "Root"
	if s.Title != "" {
		g.rootTypeName = SchemaNameToGoName(s.Title)
	}
	if override := g.rootNameOverride; override == "" {
		override = g.config.RootTypeName
		if override != "" {
			// An explicit override is used verbatim — callers may need an
			// exact name (e.g. to stay compatible with previously generated
			// code) that SchemaNameToGoName's initialism rules would rewrite.
			if !isExportedGoIdentifier(override) {
				return nil, fmt.Errorf("root type name %q is not an exported Go identifier", override)
			}
			g.rootTypeName = override
		}
	} else {
		if !isExportedGoIdentifier(override) {
			return nil, fmt.Errorf("root type name %q is not an exported Go identifier", override)
		}
		g.rootTypeName = override
	}
	// In shared-types mode a root name already claimed by an earlier schema
	// would be silently skipped by the generated-types registry; require the
	// caller to name every schema's root distinctly.
	if g.config.SharedTypes && g.generated[g.rootTypeName] {
		return nil, fmt.Errorf("root type %q was already generated by an earlier schema in this package; give each schema a distinct root name", g.rootTypeName)
	}

	// Store root schema's $id for detecting self-references.
	g.rootID = s.ID
	if g.rootID == "" {
		g.rootID = s.LegacyID
	}

	// Compute base URI from root $id (used for resolving relative refs).
	if g.rootID != "" {
		if u, err := url.Parse(g.rootID); err == nil {
			g.baseURI = u
		}
	}

	// Compute effective base URIs, document roots, and schema resources. This
	// enables scoped $id resolution and gives validation planning a dialect-aware
	// view of the schema graph.
	g.resourceGraph = schema.BuildResourceGraph(s, g.baseURI, g.draft)
	g.documentRoots = make(map[string]*schema.Schema)
	g.buildDocumentRoots(s)

	// Initialize dynamic scope with the root document root.
	g.dynamicScope = []*schema.Schema{s}
	// The anchor index is about this document, so a Generator reused for another
	// one must not answer from the last one's.
	g.dynamicAnchorDecls = nil

	// Store the external resolver from config (may be nil).
	g.resolver = g.config.Resolver
	g.validationKeywordsDisabled = !g.hasValidationVocabulary(s)

	// Settle draft 3's own format spellings before anything asks what a format
	// keyword says. buildDocumentRoots has run, so every node's dialect is
	// answerable.
	g.normalizeDialectFormats(s)

	// And drop the two reference keywords from the dialects that do not define
	// them, for the same reason and in the same place.
	g.normalizeDialectRefKeywords(s)

	// Collect definitions ($defs and definitions) and build anchor index.
	// Iterate in sorted key order for deterministic anchor registration
	// (important when multiple defs declare the same $anchor in different scopes).
	g.defs = make(map[string]*schema.Schema)
	g.anchors = make(map[string]string)
	g.dynamicAnchors = make(map[string]string)
	for _, name := range sortedKeys(s.Defs) {
		def := s.Defs[name]
		refPath := "#/$defs/" + name
		g.defs[refPath] = def
		g.indexAnchors(def, refPath)
	}
	for _, name := range sortedKeys(s.Definitions) {
		def := s.Definitions[name]
		refPath := "#/definitions/" + name
		g.defs[refPath] = def
		g.indexAnchors(def, refPath)
	}

	// Process definitions first — generate TypeDefs for each.
	defNames := sortedKeys(s.Defs)
	for _, name := range defNames {
		def := s.Defs[name]
		goName := SchemaNameToGoName(name)
		if err := g.generateTypeDef(goName, def); err != nil {
			return nil, fmt.Errorf("generating $defs/%s: %w", name, err)
		}
	}

	defNames = sortedKeys(s.Definitions)
	for _, name := range defNames {
		def := s.Definitions[name]
		goName := SchemaNameToGoName(name)
		if err := g.generateTypeDef(goName, def); err != nil {
			return nil, fmt.Errorf("generating definitions/%s: %w", name, err)
		}
	}

	// Process the root type. This handles objects, compositions, primitive types
	// with validation constraints, enums, arrays, and any other schema that can
	// produce a Go type definition -- including a boolean `false` root, which
	// generateTypeDef now answers with the same everything-is-forbidden wrapper
	// this used to special-case here. Keeping the one arm is the point: two
	// copies of the rule are what let the root and the $defs entry disagree
	// about the same schema in the first place.
	if err := g.generateTypeDef(g.rootTypeName, s); err != nil {
		return nil, fmt.Errorf("generating root type: %w", err)
	}

	// Mark aliases that cannot have methods (underlying resolves to pointer or interface).
	g.resolveAliasMethodability()

	// Populate ValidatableFields on structs — identify fields whose types have Validate().
	// Must run after resolveAliasMethodability so we know which types actually have methods.
	g.populateValidatableFields()
	g.resolveItemValidations()
	g.resolvePatternPropertyTypes()
	// Must run before populateAliasDelegates, which has to know whether an enum
	// carries an UnmarshalJSON before it can decide that an alias over that enum
	// must borrow it.
	g.resolveEnumIntegerTokens()
	g.populateAliasDelegates()
	// Must run after resolveAliasMethodability: an alias that cannot carry
	// methods has nowhere to put a tolerant decode. And after
	// populateAliasDelegates, whose UnmarshalAs it reads.
	g.resolveIntegerDecodes()

	// Publish validation info about this call's types so packages generated
	// later in a cross-package run can emit correct guards for them.
	if g.config.CrossPackage != nil {
		for _, td := range g.output.TypeDefs {
			name := td.TypeName()
			s := g.typeSchemas[name]
			if s == nil {
				continue
			}
			g.config.CrossPackage.noteTypeInfo(s,
				g.zeroLiteralForType(&NamedType{Name: name}),
				localTypeIsValidatable(td),
				g.isZeroLossyNamedType(&NamedType{Name: name}))
		}
	}

	// Add imports based on what was generated.
	g.output.ValidationCapability = analyzeValidationCapability(s, g.resourceGraph, g.config.Validation)
	g.addRequiredImports()

	// A null subschema in a document reached through a $ref is a defect in the
	// input, not a ref that could not be served, so it is reported ahead of the
	// unresolved-ref error it caused and regardless of LenientRefs: degrading a
	// malformed document to `any` would be exactly the silent acceptance the
	// up-front check exists to prevent.
	if g.nullSubschemaErr != nil {
		return nil, g.nullSubschemaErr
	}

	// Unless lenient, refuse to hand back an IR that was degraded by
	// unresolvable $refs (any-typed fields, dangling names, weaker validation).
	if !g.config.LenientRefs {
		if refs := g.neverResolvedRefs(); len(refs) > 0 {
			return nil, &UnresolvedRefsError{Refs: refs}
		}
	}

	if len(g.crossPackageMisses) > 0 {
		return nil, newCrossPackageMissError(g.crossPackageMisses)
	}

	// Imports of sibling generated packages (cross-package refs), sorted for
	// deterministic output.
	crossPaths := make([]string, 0, len(g.crossImports))
	for importPath := range g.crossImports {
		crossPaths = append(crossPaths, importPath)
	}
	sort.Strings(crossPaths)
	for _, importPath := range crossPaths {
		alias := g.crossImports[importPath]
		if alias == PackageNameForImportPath(importPath) {
			alias = "" // no alias needed when it matches the package name
		}
		g.output.Imports = append(g.output.Imports, Import{Path: importPath, Alias: alias})
	}

	return g.output, nil
}

// UnresolvedRefsError reports $refs that no resolver could serve during
// generation. Callers can set Config.LenientRefs to accept the degraded
// output instead.
type UnresolvedRefsError struct {
	Refs []string
}

func (e *UnresolvedRefsError) Error() string {
	return fmt.Sprintf("cannot resolve $ref %s", strings.Join(quoteAll(e.Refs), ", "))
}

// neverResolvedRefs returns the sorted $refs that failed to resolve and never
// resolved in any other context. Resolution is context-dependent, so a ref is
// only genuinely unresolvable if no attempt succeeded.
// crossPackageMiss identifies a $ref into a document owned by another package
// of the run whose type that package did not register.
type crossPackageMiss struct {
	Package  string
	Document string
}

// CrossPackageMissError reports $ref targets that belong to another package of
// a cross-package run but were not generated there, so they cannot be imported.
type CrossPackageMissError struct {
	Misses []string
}

func (e *CrossPackageMissError) Error() string {
	return fmt.Sprintf("cross-package $ref targets not generated by their owning package: %s", strings.Join(e.Misses, ", "))
}

func newCrossPackageMissError(misses map[crossPackageMiss]bool) *CrossPackageMissError {
	out := make([]string, 0, len(misses))
	for m := range misses {
		out = append(out, fmt.Sprintf("%q (owned by %q)", m.Document, m.Package))
	}
	sort.Strings(out)
	return &CrossPackageMissError{Misses: out}
}

// documentIdentityOf returns the most specific URI identifying s, for
// diagnostics.
func documentIdentityOf(s *schema.Schema) string {
	if s == nil {
		return ""
	}
	for _, node := range []*schema.Schema{s, s.DocumentRoot} {
		if node == nil {
			continue
		}
		if ids := documentIdentities(node); len(ids) > 0 {
			return ids[0]
		}
	}
	return "<unidentified schema node>"
}

func (g *Generator) neverResolvedRefs() []string {
	refs := make([]string, 0, len(g.unresolvedRefs))
	for ref := range g.unresolvedRefs {
		if g.resolvedRefs[ref] {
			continue
		}
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// noteItemValidationImports records what the emitted per-element checks need.
// Every one of them wraps its failure in fmt.Errorf, and the individual rules
// pull in the same packages their field-level counterparts do.
func noteItemValidationImports(defs []ItemValidationDef, needsFmt, needsJSON, needsMath, needsUTF8, needsRegexp *bool) {
	if len(defs) == 0 {
		return
	}
	*needsFmt = true
	for _, def := range defs {
		for _, level := range def.Levels {
			for _, rule := range level.Rules {
				switch rule.RuleType {
				case "minLength", "maxLength":
					*needsUTF8 = true
				case "pattern":
					*needsRegexp = true
				case "multipleOf":
					*needsMath = true
				case "const":
					*needsJSON = true
				}
			}
			// A level whose element is a tuple emits the same arms an array
			// property's positions do, so it reaches for the same imports.
			if len(level.TupleItems) > 0 {
				noteFieldTupleImports([]FieldTupleDef{{Items: level.TupleItems, Tail: level.TupleTail}},
					needsFmt, needsJSON, needsMath)
			}
			if level.UnevalItems != nil {
				noteFieldUnevalItemsImports([]FieldUnevalItemsDef{{Def: level.UnevalItems}},
					needsFmt, needsJSON, needsMath)
			}
		}
	}
}

// addRequiredImports scans generated TypeDefs and adds necessary imports.
func (g *Generator) addRequiredImports() {
	needsJSON := false
	needsFmt := false
	needsRegexp := false
	needsTime := false
	needsMath := false
	needsUTF8 := false
	needsBytes := false
	needsStrings := false
	needsBigInt := false
	needsNetIP := false
	needsNetMail := false
	needsNetURL := false
	needsStdRegexp := false
	needsValidationRuntime := false

	if g.output.ValidationCapability.RequiresRuntime && g.output.ValidationCapability.Mode != ValidationModeStatic {
		needsValidationRuntime = true
	}

	for _, td := range g.output.TypeDefs {
		if sd, ok := td.(*StructDef); ok {
			if len(sd.ObjectOneOfs) > 0 || len(sd.ObjectAnyOfs) > 0 {
				needsJSON = true
				needsFmt = true
				// bytes.TrimSpace is only emitted for branch property checks
				// (JSON type / allowed value); a group whose branches are keyed
				// solely on required properties does not use it.
				if objectBranchesHaveChecks(sd.ObjectOneOfs, sd.ObjectAnyOfs) {
					needsBytes = true
				}
			}
			// A dependentSchemas branch is judged by the same evaluator as an
			// object-level conditional, so it needs the same imports.
			if sd.HasObjectConditionals() || sd.HasDependentSchemaBranches() {
				needsJSON = true // the raw property value is decoded before it is checked
				needsFmt = true  // Validate() errors
				if sd.ConditionalNeedsMultipleOf() {
					needsMath = true
				}
				if sd.ConditionalNeedsUTF8() {
					needsUTF8 = true
				}
				if sd.ConditionalNeedsPattern() {
					needsRegexp = true // pattern uses ecma262
				}
			}
			if sd.NeedsUnmarshal {
				needsJSON = true // UnmarshalJSON always uses json.Unmarshal
			}
			if sd.NeedsMarshal {
				needsJSON = true // MarshalJSON always uses json.Marshal
			}
			if sd.NeedsNullCheck {
				needsFmt = true // UnmarshalJSON uses fmt.Errorf for null rejection
			}
			if sd.HasNullChecks() {
				needsJSON = true // the raw properties are decoded before they are judged
				needsFmt = true  // UnmarshalJSON uses fmt.Errorf to name the position
			}
			if len(sd.OneOfs) > 0 {
				needsJSON = true
				needsFmt = true
				// The per-variant constraint checks emitted into UnmarshalJSON
				// use the same helpers the validation template does.
				for _, oof := range sd.OneOfs {
					for _, v := range oof.Variants {
						for _, c := range v.Checks {
							switch c.RuleType {
							case "minLength", "maxLength":
								needsUTF8 = true
							case "pattern":
								needsRegexp = true
							case "multipleOf":
								needsMath = true
							}
						}
					}
				}
			}
			// Check if any fields need manual JSON handling (control chars in names).
			for _, f := range sd.Fields {
				if f.ManualJSON {
					needsJSON = true
					needsFmt = true
					break
				}
			}
			if sd.AdditionalProperties != nil {
				needsJSON = true
				// fmt is needed for non-RawMessage additional properties (typed maps)
				// because the marshal template uses fmt.Errorf for marshaling errors,
				// and for forbidden additional properties validation.
				if sd.AdditionalProperties.ValueType.GoTypeName() != "json.RawMessage" || sd.AdditionalProperties.Forbidden {
					needsFmt = true
				}
			}
			if sd.HasRequiredFields() {
				needsJSON = true // UnmarshalJSON uses json.Unmarshal + json.RawMessage
			}
			if len(sd.Validations) > 0 || sd.HasRequiredFields() {
				needsFmt = true
				for _, v := range sd.Validations {
					if v.RuleType == "pattern" {
						needsRegexp = true
					}
					if v.RuleType == "multipleOf" {
						needsMath = true
					}
					if v.RuleType == "uniqueItems" {
						needsJSON = true
					}
					if v.RuleType == "minLength" || v.RuleType == "maxLength" {
						needsUTF8 = true
					}
				}
			}
			if len(sd.PatternProperties) > 0 {
				needsRegexp = true
				needsJSON = true
			}
			if sd.HasPatternPropertyValidation() {
				needsFmt = true
				for _, pp := range sd.PatternProperties {
					if pp.TypeName != "" {
						// The bucket decodes the raw value into that type and
						// calls its Validate.
						needsJSON = true
						needsFmt = true
					}
					for _, v := range pp.Validations {
						if v.RuleType == "ppType" {
							needsBytes = true
							if !pp.StrictInteger {
								// The lenient integer classification parses the
								// number instead of scanning its token.
								needsJSON = true
								needsMath = true
							}
						}
						if v.RuleType == "ppMultipleOf" {
							needsMath = true
						}
						if v.RuleType == "ppMinimum" || v.RuleType == "ppMaximum" ||
							v.RuleType == "ppExclusiveMinimum" || v.RuleType == "ppExclusiveMaximum" ||
							v.RuleType == "ppMultipleOf" ||
							v.RuleType == "ppMinItems" || v.RuleType == "ppMaxItems" ||
							v.RuleType == "ppMinLength" || v.RuleType == "ppMaxLength" {
							needsJSON = true
						}
						if v.RuleType == "ppMinLength" || v.RuleType == "ppMaxLength" {
							needsUTF8 = true
						}
						if v.RuleType == "ppPattern" {
							needsRegexp = true
						}
					}
				}
			}
			// Non-object validations use same pp* rule types.
			if len(sd.NonObjectValidations) > 0 {
				needsFmt = true
				for _, v := range sd.NonObjectValidations {
					if v.RuleType == "ppType" {
						needsBytes = true
					}
					if v.RuleType == "ppMultipleOf" {
						needsMath = true
					}
					if v.RuleType == "ppMinimum" || v.RuleType == "ppMaximum" ||
						v.RuleType == "ppExclusiveMinimum" || v.RuleType == "ppExclusiveMaximum" ||
						v.RuleType == "ppMultipleOf" ||
						v.RuleType == "ppMinItems" || v.RuleType == "ppMaxItems" ||
						v.RuleType == "ppMinLength" || v.RuleType == "ppMaxLength" {
						needsJSON = true
					}
					if v.RuleType == "ppMinLength" || v.RuleType == "ppMaxLength" {
						needsUTF8 = true
					}
					if v.RuleType == "ppPattern" {
						needsRegexp = true
					}
				}
			}
			if len(sd.DependentSchemas) > 0 {
				needsFmt = true  // Validate() uses fmt.Errorf for dependent schema errors
				needsJSON = true // UnmarshalJSON uses json.Unmarshal for _jsonKeys
			}
			if len(sd.DependentRequired) > 0 {
				needsFmt = true  // Validate() uses fmt.Errorf for dependentRequired errors
				needsJSON = true // UnmarshalJSON uses json.Unmarshal for _jsonKeys
			}
			if sd.PropertyNames != nil {
				needsFmt = true  // Validate() uses fmt.Errorf for propertyNames errors
				needsJSON = true // UnmarshalJSON uses json.Unmarshal for _jsonKeys
				if sd.PropertyNames.MaxLength != nil || sd.PropertyNames.MinLength != nil {
					needsUTF8 = true
				}
				if sd.PropertyNames.Pattern != "" {
					needsRegexp = true // pattern uses ecma262
				}
			}
			if sd.UnevaluatedProperties != nil && !sd.UnevaluatedProperties.IsAllowed && !sd.UnevaluatedProperties.AllEvaluated {
				needsFmt = true // Validate() uses fmt.Errorf for unevaluated property errors
				if len(sd.UnevaluatedProperties.EvaluatedPatterns) > 0 {
					needsRegexp = true
				}
				if sd.UnevaluatedProperties.ValueType != "" {
					needsJSON = true // Validate() uses json.Unmarshal for schema-valued unevaluatedProperties
				}
				for _, v := range sd.UnevaluatedProperties.Validations {
					if v.RuleType == "minLength" || v.RuleType == "maxLength" {
						needsUTF8 = true
					}
					if v.RuleType == "pattern" {
						needsRegexp = true
					}
					if v.RuleType == "multipleOf" {
						needsMath = true
					}
				}
				// Conditional evals may need JSON for const checks and regexp for patterns.
				for _, ce := range sd.UnevaluatedProperties.ConditionalEvals {
					if ce.IfBranch != nil && len(ce.IfBranch.ConstChecks) > 0 {
						needsJSON = true
					}
					for _, b := range ce.Branches {
						if len(b.Patterns) > 0 {
							needsRegexp = true
						}
						if len(b.ConstChecks) > 0 {
							needsJSON = true
						}
					}
					if ce.ThenBranch != nil && len(ce.ThenBranch.Patterns) > 0 {
						needsRegexp = true
					}
					if ce.ElseBranch != nil && len(ce.ElseBranch.Patterns) > 0 {
						needsRegexp = true
					}
					if ce.Branch != nil && len(ce.Branch.Patterns) > 0 {
						needsRegexp = true
					}
				}
			}
			if len(sd.BranchOverflowChecks) > 0 {
				needsFmt = true // Validate() uses fmt.Errorf for per-branch overflow errors
				for _, c := range sd.BranchOverflowChecks {
					if len(c.AccountedPatterns) > 0 {
						needsRegexp = true
					}
					if c.TypeName != "" {
						needsJSON = true // the unaccounted value is decoded into the branch's type
					}
				}
			}
			if len(sd.RuntimeBranchChecks) > 0 {
				// The document is rebuilt from the raw map before the compiled
				// applicator is evaluated over it, and a failure is reported
				// with the reason the evaluator gave.
				needsFmt = true
				needsJSON = true
			}
			if len(sd.ObjectEnum) > 0 {
				// The document is re-encoded to compare it against the members.
				needsFmt = true
				needsJSON = true
			}
			// Validatable fields emit fmt.Errorf to wrap the nested error path,
			// so the file needs fmt even when the struct has no other fmt use.
			if len(sd.ValidatableFields) > 0 {
				needsFmt = true
			}
			noteItemValidationImports(sd.ItemValidations,
				&needsFmt, &needsJSON, &needsMath, &needsUTF8, &needsRegexp)
			noteFieldContainsImports(sd.ContainsValidations,
				&needsFmt, &needsJSON, &needsMath, &needsStdRegexp)
			noteFieldTupleImports(sd.TupleValidations, &needsFmt, &needsJSON, &needsMath)
			noteFieldUnevalItemsImports(sd.UnevalItemsValidations, &needsFmt, &needsJSON, &needsMath)
			for _, f := range sd.Fields {
				if usesTimeType(f.Type) {
					needsTime = true
				}
				if usesJSONType(f.Type) {
					needsJSON = true
				}
				if usesNetIPType(f.Type) {
					needsNetIP = true
				}
			}
			// The overflow map's value type is written out three times -- the
			// field, the make() in UnmarshalJSON and the per-key decode -- but
			// it is not a FieldDef, so the loop above never saw it. A schema
			// whose only typed value is its additionalProperties (an object
			// with no declared properties whose values are `format: date-time`)
			// therefore named time.Time in a file that did not import time.
			if sd.AdditionalProperties != nil {
				if usesTimeType(sd.AdditionalProperties.ValueType) {
					needsTime = true
				}
				if usesJSONType(sd.AdditionalProperties.ValueType) {
					needsJSON = true
				}
				if usesNetIPType(sd.AdditionalProperties.ValueType) {
					needsNetIP = true
				}
			}
			// Check format validation rules for their import needs.
			for _, v := range sd.Validations {
				if v.RuleType == "format" {
					needsFmt = true
					noteFormatImports(v, &needsFmt, &needsNetIP)
				}
				// The content check is a call to a shared helper whose error
				// this file wraps, so fmt is all it names here.
				if v.RuleType == "content" {
					needsFmt = true
				}
			}
		}
		if ed, ok := td.(*EnumDef); ok {
			needsFmt = true // Validate() uses fmt.Errorf for invalid enum values
			if ed.IsRaw {
				needsJSON = true // raw enums use json.RawMessage + UnmarshalJSON/MarshalJSON
			}
		}
		if ad, ok := td.(*AliasDef); ok {
			if usesTimeType(ad.Underlying) {
				needsTime = true
			}
			if usesNetIPType(ad.Underlying) {
				needsNetIP = true
			}
			if usesJSONType(ad.Underlying) {
				needsJSON = true
			}
			// Everything below is about code that hangs off a method, and an
			// alias whose underlying chain resolves to a pointer or an interface
			// gets none: Go forbids the receiver, so resolveAliasMethodability
			// marks it and the template emits the bare declaration alone.
			// Counting its rules anyway imports packages nothing then uses, and
			// the file does not compile. `type Root *string` carrying a
			// minLength -- which is what {"type":["string","null"],"minLength":2}
			// produces -- was enough to reach it.
			//
			// Several arms below already ask this question one at a time. Asking
			// it once here covers the ones that did not, and it is why no rule
			// list needs its own copy of the test. Nothing is skipped that the
			// declaration itself needs: the three underlying-type checks above
			// are about the type written on the right of the `=`, not about a
			// method, and they stay.
			if !ad.CanHaveMethods() {
				continue
			}
			if ad.NeedsNullCheck && ad.CanHaveMethods() {
				needsJSON = true // UnmarshalJSON uses json.Unmarshal
				needsFmt = true  // UnmarshalJSON uses fmt.Errorf
			}
			if ad.NullCheck != nil && ad.CanHaveMethods() {
				needsJSON = true // the walker reads the raw elements
				needsFmt = true  // and names the position it refuses
			}
			if ad.IsIntegerType() && ad.CanHaveMethods() {
				needsJSON = true // UnmarshalJSON uses json.Number
				needsFmt = true  // UnmarshalJSON uses fmt.Errorf
				needsMath = true // UnmarshalJSON uses math.Trunc, math.IsInf
			}
			if ad.ValidateAs != "" && ad.CanHaveMethods() {
				needsFmt = true // Validate() wraps delegated validation errors
			}
			if ad.UnmarshalAs != "" && ad.CanHaveMethods() {
				needsJSON = true // UnmarshalJSON delegates through json.Unmarshal
			}
			if ad.MarshalAs != "" && ad.CanHaveMethods() {
				needsJSON = true // MarshalJSON delegates through json.Marshal
			}
			if ad.HasTupleItems() {
				needsJSON = true // Validate() uses json.Marshal/json.Unmarshal for tuple items
				needsFmt = true  // Validate() uses fmt.Errorf for tuple item errors
				positions := ad.TupleItems
				if ad.TupleTail != nil {
					// The tail is checked by the same arms as a position, so it
					// reaches for the same imports.
					positions = append(append([]TupleItemDef{}, positions...), *ad.TupleTail)
				}
				for _, ti := range positions {
					// The per-position "integer" test is math.Trunc, as it is
					// for an InferredAliasDef's tuple positions.
					if ti.JSONType == "integer" {
						needsMath = true
					}
				}
			}
			noteItemValidationImports(ad.ItemValidations,
				&needsFmt, &needsJSON, &needsMath, &needsUTF8, &needsRegexp)
			if ad.HasUnevaluatedItems() {
				needsFmt = true
				if ad.UnevaluatedItems.ContainsEvaluates || ad.UnevaluatedItems.ValueType != "" || len(ad.UnevaluatedItems.Checks) > 0 {
					needsJSON = true
				}
				if ad.UnevaluatedItems.ValueType == "integer" {
					needsMath = true
				}
				for _, chk := range ad.UnevaluatedItems.Checks {
					if chk.CheckType == "multipleOf" {
						needsMath = true
					}
				}
			}
			if len(ad.Validations) > 0 {
				needsFmt = true
				for _, v := range ad.Validations {
					if v.RuleType == "pattern" {
						needsRegexp = true
					}
					if v.RuleType == "multipleOf" {
						needsMath = true
					}
					if v.RuleType == "uniqueItems" {
						needsJSON = true
					}
					if v.RuleType == "minLength" || v.RuleType == "maxLength" {
						needsUTF8 = true
					}
					// A format check on the alias's own value reaches for the
					// same packages the struct-field arm does.
					if v.RuleType == "format" {
						noteFormatImports(v, &needsFmt, &needsNetIP)
					}
					if v.RuleType == "content" {
						needsFmt = true
					}
				}
			}
			if len(ad.AnyOfVariants) > 0 {
				needsFmt = true // anyOf error message uses fmt.Errorf
				for _, variant := range ad.AnyOfVariants {
					for _, v := range variant {
						if v.RuleType == "pattern" {
							needsRegexp = true
						}
						if v.RuleType == "multipleOf" {
							needsMath = true
						}
						if v.RuleType == "uniqueItems" {
							needsJSON = true
						}
						if v.RuleType == "minLength" || v.RuleType == "maxLength" {
							needsUTF8 = true
						}
					}
				}
			}
			if len(ad.OneOfVariants) > 0 {
				needsFmt = true // oneOf error message uses fmt.Errorf
				for _, variant := range ad.OneOfVariants {
					for _, v := range variant {
						if v.RuleType == "pattern" {
							needsRegexp = true
						}
						if v.RuleType == "multipleOf" {
							needsMath = true
						}
						if v.RuleType == "uniqueItems" {
							needsJSON = true
						}
						if v.RuleType == "minLength" || v.RuleType == "maxLength" {
							needsUTF8 = true
						}
					}
				}
			}
			// Contains validation imports.
			if ad.Contains != nil {
				if ad.Contains.ConstJSON != "" {
					needsJSON = true
				}
				if len(ad.Contains.EnumJSON) > 0 {
					needsJSON = true
				}
				if ad.Contains.TypeName != "" {
					needsJSON = true // marshal the element, decode it into the type
				}
				for _, chk := range ad.Contains.Checks {
					if chk.CheckType == "multipleOf" {
						needsMath = true
					}
					// The per-element "integer" test is math.Trunc, exactly as
					// it is for an items check.
					if chk.CheckType == "type" && chk.Value == "integer" {
						needsMath = true
					}
					if chk.CheckType == "pattern" {
						needsStdRegexp = true
					}
				}
			}
		}
		if _, ok := td.(*BigIntAliasDef); ok {
			needsJSON = true    // UnmarshalJSON, MarshalJSON
			needsFmt = true     // Validate() errors, String()
			needsMath = true    // math.Trunc, math.IsInf
			needsStrings = true // strings.ContainsAny for float-format bignums
			needsBigInt = true  // math/big for *big.Int
		}
		if tosd, ok := td.(*TypeOnlySchemaDef); ok {
			needsJSON = true // UnmarshalJSON, MarshalJSON, json.RawMessage
			needsFmt = true  // Validate() errors
			for _, at := range tosd.AllowedTypes {
				if at == "integer" {
					needsMath = true // math.Trunc, math.IsInf for integer check
				}
			}
			for _, branch := range tosd.TypeBranches {
				for _, at := range branch.AllowedTypes {
					if at == "integer" {
						needsMath = true
					}
				}
				for _, prop := range branch.Properties {
					if prop.JSONType == "integer" {
						needsMath = true
					}
				}
			}
		}
		if _, ok := td.(*AnnotationSchemaDef); ok {
			needsJSON = true // UnmarshalJSON, MarshalJSON, decode to any
			needsFmt = true  // Validate() errors
		}
		if dsd, ok := td.(*DynamicSchemaDef); ok {
			needsJSON = true // UnmarshalJSON, MarshalJSON, json.RawMessage, decode to any
			needsFmt = true  // Validate() errors
			// math is needed in this file only for a multipleOf check's math.Mod.
			// The integer type test lives in the shared helper file, which
			// carries its own math import.
			if dsd.NeedsMultipleOf() {
				needsMath = true
			}
			if dsd.NeedsUTF8() {
				needsUTF8 = true
			}
			if dsd.NeedsPattern() {
				needsRegexp = true
			}
		}
		if nsd, ok := td.(*NotSchemaDef); ok {
			needsJSON = true // UnmarshalJSON, MarshalJSON, json.RawMessage
			needsFmt = true  // Validate() errors
			if len(nsd.NotTypes) > 0 || len(nsd.NotBranches) > 0 {
				// Type checks for "integer" use math.Trunc and math.IsInf.
				for _, nt := range nsd.NotTypes {
					if nt == "integer" {
						needsMath = true
					}
				}
				for _, branch := range nsd.NotBranches {
					for _, nt := range branch.Types {
						if nt == "integer" {
							needsMath = true
						}
					}
					for _, prop := range branch.Properties {
						if prop.JSONType == "integer" {
							needsMath = true
						}
					}
					for _, v := range branch.Validations {
						if v.RuleType == "multipleOf" {
							needsMath = true
						}
						if v.RuleType == "minLength" || v.RuleType == "maxLength" {
							needsUTF8 = true
						}
						if v.RuleType == "pattern" {
							needsRegexp = true
						}
					}
				}
			}
		}
		if iad, ok := td.(*InferredAliasDef); ok {
			needsJSON = true // UnmarshalJSON, MarshalJSON, json.RawMessage
			needsFmt = true  // Validate() errors, String()
			if iad.ValidateAs != "" {
				needsFmt = true
			}
			for _, v := range iad.Validations {
				if v.RuleType == "minLength" || v.RuleType == "maxLength" {
					needsUTF8 = true
				}
				if v.RuleType == "pattern" {
					needsRegexp = true
				}
				if v.RuleType == "multipleOf" {
					needsMath = true
				}
				// The wrapper a format with no declared "type" resolves to
				// carries the same check the alias and struct-field arms do,
				// and reaches for the same packages. The content vocabulary
				// resolves to the same wrapper and needs only fmt.
				if v.RuleType == "format" {
					noteFormatImports(v, &needsFmt, &needsNetIP)
				}
				if v.RuleType == "content" {
					needsFmt = true
				}
			}
			for _, variant := range iad.AnyOfVariants {
				for _, v := range variant {
					if v.RuleType == "minLength" || v.RuleType == "maxLength" {
						needsUTF8 = true
					}
					if v.RuleType == "pattern" {
						needsRegexp = true
					}
					if v.RuleType == "multipleOf" {
						needsMath = true
					}
				}
			}
			for _, variant := range iad.OneOfVariants {
				for _, v := range variant {
					if v.RuleType == "minLength" || v.RuleType == "maxLength" {
						needsUTF8 = true
					}
					if v.RuleType == "pattern" {
						needsRegexp = true
					}
					if v.RuleType == "multipleOf" {
						needsMath = true
					}
				}
			}
			// Item-level validation may need math.Trunc for integer checks.
			if iad.ItemsType == "integer" || iad.AdditionalItemsType == "integer" ||
				(iad.ItemsNested != nil && iad.ItemsNested.ItemsType == "integer") {
				needsMath = true
			}
			for _, ti := range iad.TupleItems {
				if ti.JSONType == "integer" {
					needsMath = true
				}
			}
			// Items checks imports.
			for _, chk := range iad.ItemsChecks {
				if chk.CheckType == "multipleOf" {
					needsMath = true
				}
				if chk.CheckType == "type" && chk.Value == "integer" {
					needsMath = true
				}
			}
			// Contains validation imports.
			if iad.Contains != nil {
				if iad.Contains.ConstJSON != "" {
					needsJSON = true // json.Marshal for per-element comparison
				}
				if len(iad.Contains.EnumJSON) > 0 {
					needsJSON = true // json.Marshal for per-element enum comparison
				}
				if iad.Contains.TypeName != "" {
					needsJSON = true // marshal the element, decode it into the type
				}
				for _, chk := range iad.Contains.Checks {
					if chk.CheckType == "multipleOf" {
						needsMath = true
					}
					// The per-element "integer" test is math.Trunc, exactly as
					// it is for an items check.
					if chk.CheckType == "type" && chk.Value == "integer" {
						needsMath = true
					}
					if chk.CheckType == "pattern" {
						needsStdRegexp = true
					}
				}
			}
		}
	}

	if needsJSON {
		g.output.Imports = append(g.output.Imports, Import{Path: "encoding/json"})
	}
	if needsFmt {
		g.output.Imports = append(g.output.Imports, Import{Path: "fmt"})
	}
	if needsRegexp {
		g.output.Imports = append(g.output.Imports, Import{Path: "github.com/mgilbir/goecma262", Alias: "ecma262"})
		g.output.Imports = append(g.output.Imports, Import{Path: "github.com/mgilbir/goecma262/flags", Alias: "ecmaflags"})
	}
	if needsMath {
		g.output.Imports = append(g.output.Imports, Import{Path: "math"})
	}
	if needsTime {
		g.output.Imports = append(g.output.Imports, Import{Path: "time"})
	}
	if needsUTF8 {
		g.output.Imports = append(g.output.Imports, Import{Path: "unicode/utf8"})
	}
	if needsBytes {
		g.output.Imports = append(g.output.Imports, Import{Path: "bytes"})
	}
	if needsStrings {
		g.output.Imports = append(g.output.Imports, Import{Path: "strings"})
	}
	if needsBigInt {
		g.output.Imports = append(g.output.Imports, Import{Path: "math/big"})
	}
	if needsNetIP {
		g.output.Imports = append(g.output.Imports, Import{Path: "net/netip"})
	}
	if needsNetMail {
		g.output.Imports = append(g.output.Imports, Import{Path: "net/mail"})
	}
	if needsNetURL {
		g.output.Imports = append(g.output.Imports, Import{Path: "net/url"})
	}
	if needsStdRegexp {
		g.output.Imports = append(g.output.Imports, Import{Path: "regexp"})
	}
	if needsValidationRuntime {
		g.output.Imports = append(g.output.Imports, Import{Path: "github.com/mgilbir/schemagen/pkg/validationruntime"})
	}
}

// isInferredAlias returns true if a type name was generated as an InferredAliasDef.
func (g *Generator) isInferredAlias(name string) bool {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() == name {
			_, ok := td.(*InferredAliasDef)
			return ok
		}
	}
	return false
}

// isBigIntAlias returns true if a type name was generated as a BigIntAliasDef.
func (g *Generator) isBigIntAlias(name string) bool {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() == name {
			_, ok := td.(*BigIntAliasDef)
			return ok
		}
	}
	return false
}

// isBigIntAliasType is isBigIntAlias for a field's Go type: an optional field of
// such a type is pointer-wrapped (its zero is exactly what a present 0 decodes
// to), and the wrapper behind the pointer is still what carries the keywords.
func (g *Generator) isBigIntAliasType(t GoType) bool {
	name := namedTypeName(t)
	return name != "" && g.isBigIntAlias(name)
}

// isInferredAliasType is isInferredAlias for a field's Go type, on the same
// terms: an optional field of such a type is pointer-wrapped, and the wrapper
// behind the pointer is what carries the keywords.
func (g *Generator) isInferredAliasType(t GoType) bool {
	name := namedTypeName(t)
	return name != "" && g.isInferredAlias(name)
}

// isNotSchema returns true if a type name was generated as a NotSchemaDef.
func (g *Generator) isNotSchema(name string) bool {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() == name {
			_, ok := td.(*NotSchemaDef)
			return ok
		}
	}
	return false
}

// isTypeOnlySchema returns true if a type name was generated as a TypeOnlySchemaDef.
func (g *Generator) isTypeOnlySchema(name string) bool {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() == name {
			_, ok := td.(*TypeOnlySchemaDef)
			return ok
		}
	}
	return false
}

// isDynamicSchema returns true if a type name was generated as a DynamicSchemaDef.
func (g *Generator) isDynamicSchema(name string) bool {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() == name {
			_, ok := td.(*DynamicSchemaDef)
			return ok
		}
	}
	return false
}

// resolveAliasMethodability walks all AliasDefs and sets NoMethods=true
// for any whose underlying type chain resolves to a pointer or interface.
// This handles cases like `type Root Bool` where Bool is `type Bool any` —
// Go does not allow methods on types whose ultimate underlying type is
// a pointer or interface type.
func (g *Generator) resolveAliasMethodability() {
	// Build a map of type name → AliasDef for cross-referencing.
	aliases := make(map[string]*AliasDef)
	for _, td := range g.output.TypeDefs {
		if ad, ok := td.(*AliasDef); ok {
			aliases[ad.Name] = ad
		}
	}

	// For each alias, walk the underlying type chain to check if it
	// ultimately resolves to a pointer or interface.
	for _, ad := range aliases {
		if !canHaveMethodsResolved(ad.Underlying, aliases) {
			ad.NoMethods = true
		}
	}
}

// canHaveMethodsResolved checks if a GoType can be used as a method receiver,
// following NamedType references through the alias map. The visited set
// prevents infinite recursion on self-referencing alias cycles.
func canHaveMethodsResolved(t GoType, aliases map[string]*AliasDef) bool {
	visited := make(map[string]bool)
	return canHaveMethodsResolvedImpl(t, aliases, visited)
}

func canHaveMethodsResolvedImpl(t GoType, aliases map[string]*AliasDef, visited map[string]bool) bool {
	if t.IsPointer() {
		return false
	}
	if pt, ok := t.(*PrimitiveType); ok && pt.Name == "any" {
		return false
	}
	if nt, ok := t.(*NamedType); ok {
		if visited[nt.Name] {
			return true // cycle detected — assume safe
		}
		visited[nt.Name] = true
		if ref, exists := aliases[nt.Name]; exists {
			return canHaveMethodsResolvedImpl(ref.Underlying, aliases, visited)
		}
	}
	return true
}

// usesTimeType returns true if the GoType references time.Time.
func usesTimeType(t GoType) bool {
	if t == nil {
		return false
	}
	switch v := t.(type) {
	case *PrimitiveType:
		return v.Name == "time.Time"
	case *PointerType:
		return usesTimeType(v.Inner)
	case *ArrayType:
		return usesTimeType(v.ItemType)
	case *MapType:
		return usesTimeType(v.KeyType) || usesTimeType(v.ValueType)
	}
	return false
}

// usesJSONType returns true if the GoType references a type from encoding/json
// (e.g. json.RawMessage).
func usesJSONType(t GoType) bool {
	if t == nil {
		return false
	}
	switch v := t.(type) {
	case *PrimitiveType:
		return v.Name == "json.RawMessage"
	case *PointerType:
		return usesJSONType(v.Inner)
	case *ArrayType:
		return usesJSONType(v.ItemType)
	case *MapType:
		return usesJSONType(v.KeyType) || usesJSONType(v.ValueType)
	}
	return false
}

// formatGoType returns the Go type for a JSON Schema format string, or nil if
// the format should remain as the default type (string) with validation only.
func formatGoType(format string) GoType {
	switch format {
	case "date-time":
		return &PrimitiveType{Name: "time.Time"}
	case "ipv4", "ipv6":
		return &PrimitiveType{Name: "netip.Addr"}
	default:
		// Formats like date, time, email, hostname, uri, uuid, duration, etc.
		// remain as string with format validation in Validate().
		return nil
	}
}

// formatNeedsValidation returns true if the given format string should produce
// a validation rule in Validate() for string-typed fields that don't get a
// distinct Go type mapping.
func formatNeedsValidation(format string) bool {
	switch format {
	case "date", "time",
		"email", "idn-email",
		"hostname", "idn-hostname",
		"uri", "uri-reference",
		"iri", "iri-reference",
		"uri-template",
		"uuid",
		"duration",
		"json-pointer", "relative-json-pointer",
		"regex":
		return true
	default:
		return false
	}
}

// noteFormatImports records the standard-library packages a "format" check
// reaches for, for whichever of the three positions emits it -- a struct field,
// an alias over its own value, or the wrapper a format with no declared type
// resolves to. All three emit the same assertions, so they cannot answer this
// question differently; keeping one copy is what stops a new format arm from
// compiling in one position and failing to import in another.
func noteFormatImports(r ValidationRule, needsFmt, needsNetIP *bool) {
	format, ok := r.Value.(string)
	if !ok {
		return
	}
	// The check itself is a call to a shared helper, so the file needs fmt to
	// wrap the error it returns and nothing else. Every package the checks
	// themselves reach for -- net/mail, net/url, regexp, time, the ECMA-262
	// engine -- is imported by the helper file, once per destination package,
	// which is where the functions live.
	*needsFmt = true
	// Except netip, which is named by the emitted call rather than by the
	// helper: an ipv4 or ipv6 value held as a netip.Addr is converted at the
	// call site. The field or alias type names netip.Addr too, so this is
	// belt and braces -- but the conversion is what the line would not compile
	// without.
	switch format {
	case "ipv4", "ipv6":
		if !r.StringBacked {
			*needsNetIP = true
		}
	}
}

// FormatCheckableOnString reports whether this generator can assert the format
// against a value it holds as a plain Go string.
//
// Two sets meet here. formatNeedsValidation names the formats that never had a
// Go type of their own, and whose checks were always written over a string.
// ipv4, ipv6 and date-time do have one, and are checked by handing the string to
// the same parser the Go type's decoder would have used -- which is the only
// place their assertion can live once the mapping is given up. Everything else,
// including an unrecognised format, answers false: `format` is an annotation
// unless something can judge it, and inventing a type for one nothing checks
// would change the generated API for nothing.
//
// Exported because the emitter has to answer the same question from the other
// side -- every format admitted here needs a helper function to call, or a rule
// is built and then renders nothing at all. See emitter.formatHelperNameFunc
// and the test that walks the two together.
func FormatCheckableOnString(format string) bool {
	switch format {
	case "ipv4", "ipv6", "date-time", Draft3TimeFormat, Draft3ColorFormat:
		return true
	default:
		return formatNeedsValidation(format)
	}
}

// Draft3TimeFormat is the internal name for draft 3's "time", which is a
// different format under the same keyword: HH:MM:SS, with no offset. Every later
// draft means RFC 3339's full-time, whose offset is mandatory, so a single check
// cannot serve both without accepting a bare local time on the drafts that
// forbid one.
//
// The rewrite happens where the draft is known -- see formatRulesForDialect --
// and the name is deliberately not a legal format keyword, so a schema cannot
// ask for it and nothing downstream can confuse it for one.
const Draft3TimeFormat = "time (draft 3)"

// Draft3ColorFormat is the internal name for draft 3's "color", which no later
// draft has at all. Draft 3 section 5.23 defines it as a CSS colour "based on
// CSS 2.1", naming that specification by its dated W3C URI, so the grammar this
// checks is a fixed one rather than a moving target: the hash notations and the
// rgb() function of CSS 2.1 section 4.3.6, and the keywords that specification
// lists as values of type <color>.
//
// Pinning it there is also what the official suite asks for. It marks
// "#00332520" invalid -- eight hex digits is #RRGGBBAA, which CSS Color 4 added
// and CSS 2.1 does not have -- so reading "color" as modern CSS would accept a
// document draft 3 forbids. The naming is likewise deliberate: it is not a legal
// format keyword, so a 2020-12 schema writing "color" still gets the annotation
// every draft after 3 says an unknown format is.
const Draft3ColorFormat = "color (draft 3)"

// draft3FormatSpellings maps the format names that belong to draft 3 alone onto
// the name this generator checks them under.
//
// Two of them are the same format every later draft has under a different
// spelling: draft 3's "host-name" is "hostname" and its "ip-address" is "ipv4",
// which is why they map onto those names outright rather than onto an internal
// one -- the check, the Go type mapping and the helper are all the modern
// format's, because it *is* the modern format. "color" has no later counterpart
// and maps to an internal name of its own.
//
// This is a separate mechanism from formatRulesForDialect, and the split is
// forced rather than chosen. "time" is spelled the same in every draft, so it
// survives every checkability test on the way to a rule and the rewrite can
// happen on the rule. These three are unknown format keywords outside draft 3,
// which is what they must stay -- so the spelling has to be settled on the
// schema itself, before the first thing that asks FormatCheckableOnString what
// the keyword says. A rule-level rewrite would run after the rule had already
// been dropped.
var draft3FormatSpellings = map[string]string{
	"host-name":  "hostname",
	"ip-address": "ipv4",
	"color":      Draft3ColorFormat,
}

// normalizeDialectFormats settles the "format" keyword of every schema reachable
// from the root onto the spelling this generator checks it under, for the nodes
// whose own dialect is draft 3.
//
// It is a pass rather than a lookup at each of the seven places that read
// s.Format because those places do not agree on what they are asking: one wants
// the Go type, one whether a wrapper is owed, one whether a rule can be built.
// Answering the spelling question once, where the draft is known and before any
// of them run, is what stops the next one that gets added from being the one
// that forgets.
//
// The gate is per node, not per document, so a draft-3 resource embedded in a
// later-draft document is rewritten and its host document is not. A node the
// walk never reaches -- one a resolver produced for a remote reference -- keeps
// its draft-3 spelling and is read as the unknown format it is in every other
// draft, which withholds a check rather than inventing a rejection.
func (g *Generator) normalizeDialectFormats(s *schema.Schema) {
	seen := make(map[*schema.Schema]bool)
	var walk func(*schema.Schema)
	walk = func(n *schema.Schema) {
		if n == nil || seen[n] {
			return
		}
		seen[n] = true
		if n.Format != nil && g.draftForSchema(n) == schema.Draft03 {
			if renamed, ok := draft3FormatSpellings[*n.Format]; ok {
				n.Format = &renamed
			}
		}
		for _, sub := range allSubSchemas(n) {
			walk(sub)
		}
		// allSubSchemas answers a different question -- which subschemas produce
		// a Go type -- and leaves out two that hold a schema all the same. A
		// format under either of them is as much a draft-3 spelling as any
		// other.
		walk(n.PropertyNames)
		walk(n.ContentSchema)
	}
	walk(s)
}

// normalizeDialectRefKeywords removes $recursiveRef and $dynamicRef from the
// nodes whose own dialect does not define them.
//
// Each keyword arrived in a particular draft, and the drafts before it do not
// have it. $recursiveRef arrived in 2019-09, so drafts 3, 4, 6 and 7 have never
// heard of it; $dynamicRef arrived in 2020-12, so 2019-09 has not heard of that
// one either. A schema writing one where its dialect does not define it states
// an unknown keyword, and every draft says the same thing about those: ignore
// them. This generator honoured both on every draft, which is over-enforcement
// rather than the under-enforcement most of these findings are -- a draft-7
// schema whose $recursiveRef target is narrower than the schema itself refused a
// document draft-7 readers, ignoring the keyword, accept (issue #161). No file
// in the pinned suite exercises it: $recursiveRef appears only under
// draft2019-09 and $dynamicRef only under draft2020-12 and v1, so the corpus
// could never have caught this and a hand-written fixture is the only way in.
//
// A pass rather than a test at each reader, for the reason normalizeDialectFormats
// gives above it: sixty-nine calls to EffectiveRef and getting on for a hundred
// direct reads of the two fields stand outside the tests, they do not agree on
// what they are asking, and a rule applied per reader is a rule the next reader
// added will not apply. Clearing the field
// once, where the dialect is known and before anything has looked, is what makes
// the answer the same in all of them -- including Schema.EffectiveRef, which
// returns $recursiveRef unconditionally and is the function the issue names.
//
// The gate is per node, so a 2019-09 resource embedded in a draft-7 document
// keeps its $recursiveRef and the host document does not gain one.
//
// A dialect this generator cannot identify is left alone. DraftUnknown is not
// only "no $schema": it is also what a document declaring a custom metaschema
// answers, and such a metaschema may perfectly well assemble a vocabulary that
// defines the keyword -- two groups of the suite declare one. Dropping the
// keyword there would discard a constraint the document means, on no evidence
// beyond this generator not recognising a URI.
func (g *Generator) normalizeDialectRefKeywords(s *schema.Schema) {
	seen := make(map[*schema.Schema]bool)
	var walk func(*schema.Schema)
	walk = func(n *schema.Schema) {
		if n == nil || seen[n] {
			return
		}
		seen[n] = true
		if n.RecursiveRef != "" && !recursiveRefDefinedForDraft(g.draftForSchema(n)) {
			n.RecursiveRef = ""
		}
		if n.DynamicRef != "" && !dynamicRefDefinedForDraft(g.draftForSchema(n)) {
			n.DynamicRef = ""
		}
		for _, sub := range allSubSchemas(n) {
			walk(sub)
		}
		// The two allSubSchemas leaves out; a reference under either is as much
		// the dialect's business as any other.
		walk(n.PropertyNames)
		walk(n.ContentSchema)
	}
	walk(s)
}

// recursiveRefDefinedForDraft reports whether $recursiveRef is a keyword of the
// draft, rather than a word the draft has never heard of.
//
// It arrived in 2019-09, so drafts 3, 4, 6 and 7 do not have it and a schema
// writing it there states an unknown keyword. That is the whole of what this
// answers, and the four early drafts are the whole of what it says no to.
//
// 2020-12 is the interesting one and it is deliberately a yes. The keyword is
// not in 2020-12's core vocabulary -- $dynamicRef replaced it -- so a reading of
// the specification alone would drop it there too, and the reading was checked
// rather than assumed: over {"additionalProperties":{"$recursiveRef":"#"}}
// declared as 2020-12, python-jsonschema ignores the keyword and both
// santhosh-tekuri implementations (go-jsonschema and rust-boon) go on honouring
// it. Two answers from three implementations is not a settled question, and this
// repository's rule for a split oracle is to record it rather than pick a
// favourite. Dropping the keyword there would also be the one direction that
// cannot be taken back cheaply: every document the target refused becomes
// accepted. The four early drafts have no such split, because the keyword did
// not exist for anything to disagree about.
//
// DraftUnknown answers true for a different reason again -- see
// normalizeDialectRefKeywords.
func recursiveRefDefinedForDraft(d schema.Draft) bool {
	switch d {
	case schema.Draft03, schema.Draft04, schema.Draft06, schema.Draft07:
		return false
	default:
		return true
	}
}

// dynamicRefDefinedForDraft reports whether $dynamicRef is a keyword of the
// draft.
//
// It replaced $recursiveRef in 2020-12, so 2019-09 does not have it either and
// the span this says no to is one draft wider. That is not an inference from the
// specification alone: over {"additionalProperties":{"$dynamicRef":"#node"}}
// declared as 2019-09, python-jsonschema, go-jsonschema and rust-boon all three
// ignore the keyword, and all three ignore it on draft 6 and draft 7 as well.
func dynamicRefDefinedForDraft(d schema.Draft) bool {
	switch d {
	case schema.Draft03, schema.Draft04, schema.Draft06, schema.Draft07, schema.Draft201909:
		return false
	default:
		return true
	}
}

// formatRulesForDialect rewrites a rule set's format keywords to the spelling
// the schema's own draft gives them. Only "time" differs by draft under a name
// every draft shares; the names draft 3 alone has are settled earlier, on the
// schema -- see draft3FormatSpellings.
func (g *Generator) formatRulesForDialect(s *schema.Schema, rules []ValidationRule) []ValidationRule {
	if g.draftForSchema(s) != schema.Draft03 {
		return rules
	}
	for i := range rules {
		if rules[i].RuleType == "format" && rules[i].Value == "time" {
			rules[i].Value = Draft3TimeFormat
		}
	}
	return rules
}

// formatGoTypeForSchema returns the Go type the schema's "format" maps to, or
// nil when the value must stay a string.
//
// It is formatGoType with one exclusion. minLength, maxLength and pattern are
// about the characters of the JSON string, and neither netip.Addr nor time.Time
// carries them: `utf8.RuneCountInString(netip.Addr(v))` is not a conversion Go
// admits, so a schema that stated both keywords produced source that did not
// compile at all -- as a $defs alias and as a struct field alike.
//
// Giving the mapping up rather than dropping the length check is what keeps both
// keywords: the value is kept as the string the JSON carried, minLength reads it
// directly, and the format is asserted by parsing it (see formatCheckableOnString).
// The alternative -- keeping netip.Addr and discarding minLength as
// inexpressible, which is what the alias path does for a format it cannot write
// -- would compile while silently enforcing less than the schema says.
//
// Nor when the dialect makes format an annotation. time.Time and netip.Addr
// enforce the format by decoding it: an unparseable value fails
// json.Unmarshal, which is a rejection the schema does not license. See
// formatAssertsFor.
func (g *Generator) formatGoTypeForSchema(s *schema.Schema) GoType {
	if s == nil || s.Format == nil {
		return nil
	}
	if s.MinLength != nil || s.MaxLength != nil || s.Pattern != nil {
		return nil
	}
	if !g.formatAssertsFor(s) {
		return nil
	}
	return formatGoType(*s.Format)
}

// formatAssertsFor reports whether "format" binds as an assertion for a schema
// read under its own dialect, or is an annotation the generated code must not
// act on.
//
// The drafts disagree, and the disagreement is normative rather than a matter of
// taste. Draft 3 through draft 7 say an implementation SHOULD validate a format
// it recognises and MAY treat it as an annotation, so asserting is a legitimate
// reading and the one this generator has always taken. From 2019-09 the default
// meta-schema declares the format-annotation vocabulary, whose whole content is
// that format produces an annotation and no assertion: {"format":"email"} is
// satisfied by "2962", {"format":"regex"} by "^(abc]", and the official test
// suite marks both documents valid. Rejecting them is rejecting what the schema
// permits, which is the one failure mode this generator treats as worse than a
// missing check.
//
// v1 reverses 2019-09's decision and asserts again. It drops vocabularies
// altogether, and the official suite moves its format tests out of optional/
// into a required top-level format/ directory, where {"format":"email"} is
// marked *not* satisfied by "2962" -- the exact document 2020-12 marks valid.
// Required in that suite means default behaviour, so a v1 schema that names a
// format is asking for it to be enforced.
//
// So the dialect decides, and the two Config fields override it in either
// direction: FormatAssertion for a caller who wants the older behaviour on a
// newer draft, FormatAnnotation for one who wants the 2019-09 reading on a
// draft that asserts. They are documented as mutually exclusive and the CLI
// refuses both at once; if they arrive together anyway, annotation wins,
// because withholding a rejection is the safe direction and inventing one is
// the failure this generator treats as worst.
//
// A document that declares no $schema at all answers "annotation", which is the
// same conservative choice refOverridesSiblingsForDraft already makes for an
// unknown dialect -- and it is the safe direction here, since it withholds a
// rejection rather than inventing one.
func (g *Generator) formatAssertsFor(s *schema.Schema) bool {
	if g.config.FormatAnnotation {
		return false
	}
	if g.config.FormatAssertion {
		return true
	}
	// A metaschema that names a format vocabulary has said which of the two
	// readings its schemas take, and it outranks the draft the metaschema is
	// written in: that is the whole point of declaring one. 2020-12's own
	// metaschema declares format-annotation and so answers exactly what the
	// switch below would; a custom one declaring format-assertion is asking for
	// the other reading, and nothing but this reads it.
	if asserts, declared := g.metaschemaFormatPosture(s); declared {
		return asserts
	}
	switch g.draftForSchema(s) {
	case schema.Draft03, schema.Draft04, schema.Draft06, schema.Draft07, schema.DraftV1:
		return true
	default:
		return false
	}
}

// metaschemaFormatPosture reads the format posture out of the $vocabulary of the
// metaschema a schema declares, and reports whether it said anything at all.
//
// 2019-09 split "format" into two vocabularies that cannot both be in force:
// format-annotation, which the standard metaschemas declare and whose content is
// that format produces an annotation, and format-assertion, which says the
// keyword is checked and a document failing it is invalid. A schema pointing
// $schema at a metaschema declaring the second is asking for assertion in a
// dialect whose default is annotation, and that request is legible nowhere else
// -- the dialect URI is the custom metaschema's own $id, so DetectDraft answers
// DraftUnknown for it and the switch in formatAssertsFor would fall to the
// conservative default and enforce nothing.
//
// The boolean the declaration carries is deliberately ignored. Per the 2020-12
// core specification it says what an implementation that does *not* recognise
// the vocabulary must do -- refuse the schema when true, ignore the vocabulary
// when false -- and says nothing to one that does. This generator recognises
// format-assertion, so both spellings assert, which is what the official suite
// asserts too: optional/format-assertion.json marks "not-an-ipv4" invalid under
// the false metaschema and the true one alike.
//
// Only the declared metaschema is read, not the whole metaschema chain. A
// vocabulary a metaschema inherits through $ref is a keyword *definition*, not a
// declaration that it is in force; $vocabulary is the only thing that declares
// one, and it is not inherited.
func (g *Generator) metaschemaFormatPosture(s *schema.Schema) (asserts, declared bool) {
	vocab := g.declaredVocabulary(s)
	if len(vocab) == 0 {
		return false, false
	}
	for uri := range vocab {
		if strings.HasSuffix(uri, "/vocab/format-assertion") {
			return true, true
		}
	}
	for uri := range vocab {
		if strings.HasSuffix(uri, "/vocab/format-annotation") {
			return false, true
		}
	}
	return false, false
}

// declaredVocabulary returns the $vocabulary of the metaschema s is written
// against: its own when s is a metaschema, otherwise the one the document it
// belongs to points $schema at, resolved through the configured resolver.
//
// It is hasValidationVocabulary's lookup, lifted out so that the two questions
// asked of a metaschema -- does the validation vocabulary bind, does format
// assert -- read the same document by the same rule instead of two.
func (g *Generator) declaredVocabulary(s *schema.Schema) map[string]bool {
	if s == nil {
		return nil
	}
	if len(s.Vocabulary) > 0 {
		return s.Vocabulary
	}
	uri := s.Schema
	if uri == "" && s.DocumentRoot != nil {
		uri = s.DocumentRoot.Schema
	}
	if uri == "" || g.resolver == nil {
		return nil
	}
	if vocab, ok := g.metaschemaVocabularies[uri]; ok {
		return vocab
	}
	var vocab map[string]bool
	if meta, err := g.resolver.ResolveSchema(uri, nil); err == nil && meta != nil {
		vocab = meta.Vocabulary
	}
	if g.metaschemaVocabularies == nil {
		g.metaschemaVocabularies = make(map[string]map[string]bool)
	}
	g.metaschemaVocabularies[uri] = vocab
	return vocab
}

// contentAssertsFor reports whether the content vocabulary binds as an
// assertion for a schema read under its own dialect, or is an annotation the
// generated code must not act on.
//
// Only draft 7 asserts. It is the draft that introduced contentEncoding and
// contentMediaType, and it says an implementation SHOULD decode the string and
// MAY refuse one it cannot -- the same permission formatAssertsFor reads for
// "format", and the suite files those cases under optional/ for the same reason.
// From 2019-09 the content vocabulary is annotation-only by definition: the
// official suite marks {"contentEncoding":"base64"} satisfied by
// "eyJmb28iOi%iYmFyIn0K", which is not base64 at all, and rejecting it would be
// rejecting what the schema permits.
//
// Earlier drafts do not define the keywords, so a document that carries one is
// carrying an unknown keyword, which every draft says to ignore. Asserting there
// would invent a rejection out of a word the dialect has no meaning for.
//
// There is deliberately no --format-assertion-style override. That flag is about
// "format", and a caller who wants a draft-7 reading of the content keywords can
// say so the same way the format posture is chosen: by naming the dialect.
func (g *Generator) contentAssertsFor(s *schema.Schema) bool {
	return g.draftForSchema(s) == schema.Draft07
}

// ContentEncodingCheckable reports whether a contentEncoding names a decoding
// the generated code can perform, and so a string it can refuse.
//
// Only base64 is listed. RFC 4648 gives it one unambiguous spelling, which is
// what makes "this string does not decode" a fact rather than an opinion; the
// remaining names in RFC 2045 either encode every byte string (7bit, 8bit,
// binary) or have enough dialects that a strict reading would refuse documents
// another implementation accepts. An encoding not listed here is carried as an
// annotation, exactly as it is from 2019-09 onwards.
func ContentEncodingCheckable(encoding string) bool {
	return encoding == "base64"
}

// ContentMediaTypeCheckable reports whether a contentMediaType names a format
// the generated code can parse, and so a string it can refuse.
//
// Only JSON, which the standard library already decides. Anything else would
// mean shipping a parser per media type into generated code, and a media type
// this cannot judge is carried as an annotation rather than guessed at.
func ContentMediaTypeCheckable(mediaType string) bool {
	return mediaType == "application/json"
}

// contentCheckFor reads the content keywords a schema states into the argument
// of a "content" rule, or reports that there is nothing to check. The keywords
// are one rule rather than two because they compose: contentMediaType judges the
// bytes contentEncoding produced, so "{}" fails a base64 encoding and
// "ezp9Cg==" decodes cleanly to a document that is not JSON.
func contentCheckFor(s *schema.Schema) (ContentCheck, bool) {
	var c ContentCheck
	if ContentEncodingCheckable(s.ContentEncoding) {
		c.Encoding = s.ContentEncoding
	}
	if ContentMediaTypeCheckable(s.ContentMediaType) {
		c.MediaType = s.ContentMediaType
	}
	if c.Encoding == "" && c.MediaType == "" {
		return ContentCheck{}, false
	}
	return c, true
}

// statesContentVocabulary reports whether a schema says anything from the
// content vocabulary, checkable or not. It is what decides that a schema is
// *about* a string, which is a separate question from whether the dialect lets
// the generated code assert it.
func statesContentVocabulary(s *schema.Schema) bool {
	return s != nil && (s.ContentEncoding != "" || s.ContentMediaType != "" || s.ContentSchema != nil)
}

// withoutContentRules returns rules with every "content" entry removed, or the
// slice unchanged when it carries none.
func withoutContentRules(rules []ValidationRule) []ValidationRule {
	kept := rules[:0:0]
	dropped := false
	for _, r := range rules {
		if r.RuleType == "content" {
			dropped = true
			continue
		}
		kept = append(kept, r)
	}
	if !dropped {
		return rules
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// aliasValidationRules is extractAliasValidationRules under the dialect's format
// posture: the rules a named type carries for its own value, with the format
// assertion withheld where the dialect makes format an annotation.
//
// This and the field-rule filter in generateStructDef are the only two places a
// format rule reaches emitted code. Every other consumer of
// extractValidationRules already drops it -- oneOfVariantChecks and
// aliasVariantRules by keyword whitelist, elementRules and allOfConstraintRules
// by a default-deny switch, and the unevaluatedProperties template by having no
// arm for it -- so gating those two gates the keyword.
func (g *Generator) aliasValidationRules(s *schema.Schema, goType GoType) []ValidationRule {
	rules := extractAliasValidationRules(s, goType)
	if !g.contentAssertsFor(s) {
		rules = withoutContentRules(rules)
	}
	if !g.formatAssertsFor(s) {
		return withoutFormatRules(rules)
	}
	return g.formatRulesForDialect(s, rules)
}

// withoutFormatRules returns rules with every "format" entry removed, or the
// slice unchanged when it carries none.
func withoutFormatRules(rules []ValidationRule) []ValidationRule {
	kept := rules[:0:0]
	dropped := false
	for _, r := range rules {
		if r.RuleType == "format" {
			dropped = true
			continue
		}
		kept = append(kept, r)
	}
	if !dropped {
		return rules
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// selfMarshallingTypeName names the type t is, when that type carries its own
// JSON representation -- a representation a defined type over it does not
// inherit. It answers "" for everything else, including a container of such a
// type: only the outermost type of an alias is at risk, since encoding/json
// still reaches an element's or a field's own methods.
//
// `type Timestamp time.Time` is the case that matters. Timestamp has none of
// time.Time's methods, so encoding/json falls through to the *underlying
// representation* -- time.Time's unexported wall/ext/loc fields -- and an
// ordinary RFC 3339 string fails to decode into it, while a value marshals back
// out as `{}`. The `type Alias Timestamp` shadow the recursion-breaking idiom
// declares is not what loses the methods; they were never there. But the shadow
// is also what hides the problem, because it makes the emitted code look like
// every other alias's.
//
// netip.Addr is the same story through encoding.TextUnmarshaler, which
// encoding/json consults for a JSON string, and json.RawMessage through
// json.Unmarshaler -- an alias over it would base64 a []byte instead of keeping
// the bytes. The cure in all three cases is to decode into the named type and
// convert, which is exactly what UnmarshalAs and MarshalAs already emit for an
// alias over a *generated* type whose methods it has to borrow.
func selfMarshallingTypeName(t GoType) string {
	pt, ok := t.(*PrimitiveType)
	if !ok {
		return ""
	}
	switch pt.Name {
	case "time.Time", "netip.Addr", "json.RawMessage":
		return pt.Name
	}
	return ""
}

// usesNetIPType returns true if the GoType references netip.Addr.
func usesNetIPType(t GoType) bool {
	if t == nil {
		return false
	}
	switch v := t.(type) {
	case *PrimitiveType:
		return v.Name == "netip.Addr"
	case *PointerType:
		return usesNetIPType(v.Inner)
	case *ArrayType:
		return usesNetIPType(v.ItemType)
	case *MapType:
		return usesNetIPType(v.KeyType) || usesNetIPType(v.ValueType)
	}
	return false
}

// refCycleAliasDef returns the definition to emit when a $ref (or $dynamicRef)
// resolves to a node whose own type is still being generated further up the
// stack, and nil when the reference is not part of such a cycle.
//
// Both ref arms of generateTypeDef resolve the target and then recurse into
// generateTypeDef for it. Nothing stops that recursion on its own: the
// re-entrancy guard is g.generated[name], which is set when a definition
// *completes*, so a ref chain that leads back to a definition still in flight
// re-enters the same arm forever. The result is "fatal error: stack overflow",
// which no recover can catch -- it takes the process down, and {"$ref":"#"} is
// enough to trigger it. The struct path never had this problem because it marks
// nodesInProgress on entry; this reads that same mark for the ref paths.
//
// The reply is an alias to `any`, not an error. Reaching here means every arm
// above declined every schema on the cycle, which is to say the cycle is made
// only of $ref-only schemas and carries no constraint at all: a root of
// {"$ref":"#"} asserts "valid if valid", which every JSON value satisfies. `any`
// is what that schema describes, not a degradation of it. A cycle that does pass
// through a schema with content never gets here -- that schema's arm claims it,
// marks g.generated on entry, and the reference resolves to the named type.
func (g *Generator) refCycleAliasDef(name string, s, resolved *schema.Schema) TypeDef {
	if !g.nodesInProgress[resolved] {
		return nil
	}
	return &AliasDef{
		Name:        name,
		Underlying:  &PrimitiveType{Name: "any"},
		Description: s.Description,
	}
}

// generateTypeDef creates the appropriate TypeDef for a schema and adds it to
// the output File. It skips schemas that have already been generated.
func (g *Generator) generateTypeDef(name string, s *schema.Schema) error {
	// unevaluatedItems next to in-place applicators cannot be decided
	// statically: which items count as evaluated depends on which branches
	// match the value. Route those to the runtime evaluator before any other
	// arm claims the schema.
	if def := g.annotationSchemaDef(name, s); def != nil {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, def)
		return nil
	}

	if g.generated[name] {
		return nil
	}

	// A bookended $dynamicRef or $recursiveRef whose anchor is declared more than
	// once in reach cannot be decided statically either: which declaration wins
	// is settled by the resources the *instance* evaluation entered. Route it
	// here, ahead of the arms that would pick one of them and emit a type as if
	// it were the answer.
	//
	// Behind the re-entrancy guard above rather than beside annotationSchemaDef,
	// because the same name arrives here more than once -- a $defs entry is
	// generated in its own right and again through every $ref that reaches it --
	// and an arm in front of the guard emits its definition each time.
	if def := g.dynamicScopeSchemaDef(name, s); def != nil {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, def)
		return nil
	}

	if _, ok := g.typeSchemas[name]; !ok {
		g.typeSchemas[name] = s
	}
	if _, ok := g.nodeTypeNames[s]; !ok {
		g.nodeTypeNames[s] = name
	}
	// Keyed on the node as it arrived: promoteConstToEnum below may rebind s.
	inProgressNode := s
	g.nodesInProgress[inProgressNode] = true
	defer delete(g.nodesInProgress, inProgressNode)
	g.config.CrossPackage.RecordType(s, g.config.ImportPath, name)

	// The boolean `false` schema: no instance satisfies it, anywhere it appears.
	// Emitted as the same wrapper a root-level `false` has always produced, so
	// the answer no longer depends on the position the schema was reached from.
	//
	// The root arm above used to be the only one, on the reasoning that a
	// definition left alone "avoids type conflicts when referenced". It avoided
	// nothing: every other arm here declines a boolean schema -- it carries no
	// enum, no $ref, no type and no applicator -- so `false` fell all the way to
	// the `any` fallback and came out `type B any`, which cannot carry a Validate
	// at all. A $ref to it then aliased that, so {"$ref":"#/$defs/b"} over
	// {"b":false} accepted every document, including the ones the schema exists
	// to reject. The referencing arm below already re-generates the referrer
	// directly from a resolved NotSchemaDef rather than aliasing it, which is
	// what carries the rejection through the $ref.
	//
	// Boolean `true` is deliberately left to fall through: it admits every
	// instance, `type B any` is an exact description of that, and giving it a
	// Validate would be inventing a check the schema does not state.
	if s.IsFalseSchema() {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, &NotSchemaDef{
			Name:        name,
			Description: s.Description,
			IsForbidden: true,
		})
		return nil
	}

	// `"enum": []` is the same schema written a third way. enum asserts that the
	// instance equals one of the listed values, and there are none, so nothing
	// satisfies it -- which is what `false` says and what the official suite's
	// "empty enum" group states: string, number, null, object, array and boolean
	// are all marked invalid.
	//
	// It has to be caught before the arm below, which asks len(s.Enum) > 0 and so
	// declines the empty list, and before every arm after that, which read the
	// other keywords and answer as if the enum were not there. `{"enum":[]}` came
	// out `type Root any` and accepted all six documents; `{"type":"string",
	// "enum":[]}` came out `type Root string` and accepted every string. Both are
	// this generator accepting a document the schema forbids.
	//
	// The distinction between an absent enum and an empty one is the nil check:
	// encoding/json leaves the field nil when the keyword is absent and allocates
	// an empty slice for `[]`, so len() alone cannot tell them apart and would
	// forbid every schema in the corpus.
	//
	// refDisplacesSiblingValues is what keeps all three enum arms behind the ref
	// arms on the drafts that say the reference wins; see issue #151.
	//
	// refMergesSiblingValues stands the two value arms down on the drafts that say
	// both bind, so the implicit-allOf arm below reaches them (issue #153). The
	// forbidden arm immediately below is not one of them: `{"enum":[]}` admits
	// nothing whatever the reference says, so this is already the whole answer.
	refDisplacesEnum := g.refDisplacesSiblingValues(s)
	refMergesEnum := g.refMergesSiblingValues(s)

	if g.validationKeywordsEnabled() && !refDisplacesEnum && s.Enum != nil && len(s.Enum) == 0 {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, &NotSchemaDef{
			Name:        name,
			Description: s.Description,
			IsForbidden: true,
		})
		return nil
	}

	// Const -> treat as single-element enum for validation purposes.
	if g.validationKeywordsEnabled() && !refDisplacesEnum && !refMergesEnum {
		s = promoteConstToEnum(s)
	}

	// Enum type
	if g.validationKeywordsEnabled() && !refDisplacesEnum && !refMergesEnum && len(s.Enum) > 0 {
		return g.generateEnumDef(name, s)
	}

	// In draft2019-09+, $ref is an applicator that works alongside sibling keywords.
	// When a schema has both $ref and a sibling keyword that has to survive the
	// reference (see hasRefStructuralSiblings), synthesize an implicit allOf so
	// both the $ref target and the local keywords are merged into a single
	// definition.
	//
	// The condition is the predicate rather than a second copy of its keyword
	// list: the list used to be written out twice, here and in
	// hasRefStructuralSiblings, and the two decide the same question -- this arm
	// claims exactly the schemas the ref-only arms below decline. Two copies is
	// how one of them came to be widened without the other.
	if s.Ref != "" && !g.refOverridesSiblingsForSchema(s) && hasRefStructuralSiblings(s) {
		refSub := &schema.Schema{
			Ref:          s.Ref,
			BaseURI:      s.BaseURI,
			DocumentRoot: s.DocumentRoot,
		}
		synth := *s // shallow copy
		synth.Ref = ""
		synth.AllOf = append([]*schema.Schema{refSub}, synth.AllOf...)
		return g.generateAllOfDef(name, &synth)
	}

	// allOf → merge all sub-schemas into one struct
	if len(s.AllOf) > 0 {
		return g.generateAllOfDef(name, s)
	}

	// anyOf/oneOf with only boolean false sub-schemas → nothing can match → forbidden.
	if len(s.AnyOf) > 0 && !hasProperties(s) && g.allSubsFalse(s.AnyOf) {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, &NotSchemaDef{
			Name:        name,
			Description: s.Description,
			IsForbidden: true,
		})
		return nil
	}
	if len(s.OneOf) > 0 && !hasProperties(s) && len(s.Type) == 0 {
		// oneOf: all false → nothing matches → forbidden.
		// oneOf: multiple true sub-schemas → multiple match → forbidden (oneOf requires exactly one).
		trueCount, falseCount := g.countBooleanSchemas(s.OneOf)
		total := len(s.OneOf)
		if falseCount == total {
			// All false → nothing matches
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, &NotSchemaDef{
				Name:        name,
				Description: s.Description,
				IsForbidden: true,
			})
			return nil
		}
		if trueCount > 1 {
			// More than one always-true sub-schema → always multiple matches → forbidden
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, &NotSchemaDef{
				Name:        name,
				Description: s.Description,
				IsForbidden: true,
			})
			return nil
		}
	}

	// anyOf without properties → merge all variant properties into one struct,
	// but only if at least one sub-schema actually contributes properties.
	if len(s.AnyOf) > 0 && !hasProperties(s) && g.anyOfHasProperties(s) {
		return g.generateAnyOfDef(name, s)
	}

	// anyOf with null + single non-null variant → nullable alias (e.g., anyOf: [null, string] → *string).
	// This also handles the pattern where the non-null variant is a $ref to a primitive type.
	if len(s.AnyOf) > 0 && !hasProperties(s) {
		nonNull, hasNull := g.separateNullFromOneOf(s.AnyOf)
		if hasNull && len(nonNull) == 1 {
			variant := nonNull[0]
			// If the variant is a $ref, resolve it first so we generate the type
			// based on the target schema rather than the ref string (avoids name
			// collisions when the ref target is a remote schema root).
			effective := variant
			if effRef := variant.EffectiveRef(); effRef != "" {
				if resolved := g.resolveRefInContext(effRef, variant); resolved != nil {
					effective = resolved
				}
			}
			// The variant is generated under a name of its own rather than under
			// this one. Where it materializes a type -- an enum, an object, a
			// `not` -- resolveType claimed the very name the alias below is about
			// to declare, and the two declarations of it did not compile at all:
			// {"anyOf":[{"enum":["a","c"]},{"type":"null"}]} emitted `Doc
			// redeclared in this block`. Where it materializes nothing, which is
			// every scalar, the name is never taken and the suffix costs nothing.
			goType := g.resolveType(effective, name+"Value")
			if !g.nullableAliasCarriesTheBranch(effective, goType) {
				// A branch whose own assertions the pointer cannot carry. The
				// evaluator reads the whole group off the raw value and needs no Go
				// type per branch, which is the move #125 made for a oneOf leaving
				// the union and #133 for an anyOf branch the merge could not hold.
				// Where it cannot read the group the alias stays: wrong as it is
				// about this branch, it still gives the caller the branch's type,
				// and the alternative here enforces nothing either.
				if def := g.rawWrapperDef(name, s); def != nil {
					g.generated[name] = true
					g.output.TypeDefs = append(g.output.TypeDefs, def)
					return nil
				}
			}
			if !goType.IsPointer() {
				goType = &PointerType{Inner: goType}
			}
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
				Name:        name,
				Underlying:  goType,
				Description: s.Description,
			})
			return nil
		}
	}

	// oneOf that describes no object in parent or any variant, and with no usable
	// type information → alias to `any` (e.g. {"oneOf": [{"maximum": 3}, {"minimum": 5}]}
	// can hold any JSON value). When the schema declares (or implies via sibling
	// constraints) a primary type, fall through to the primitive/array alias paths
	// below so the declared type and the oneOf branches are both preserved.
	if len(s.OneOf) > 0 && !hasProperties(s) && !g.oneOfDescribesObject(s) &&
		primarySchemaType(s) == "" && g.inferTypeFromConstraints(s) == "" {
		// The branches still constrain the value even though the parent declares
		// no type: {"oneOf":[{"type":"integer"},{"minimum":2}]} rejects 1 and
		// rejects "foo". A bare `type X any` cannot carry a Validate() method
		// (Go forbids methods on interface-underlying types), so when the
		// branches are expressible, wrap the raw JSON in a struct instead.
		if def := g.rawWrapperDef(name, s); def != nil {
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, def)
			return nil
		}
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, g.unenforcedAliasDef(name, s))
		return nil
	}

	// A oneOf whose branches the sealed-interface union cannot judge. The arm
	// above answers the shape where no branch describes an object; this is the
	// shape where one does, which is what would otherwise put the group on the
	// struct path and give it a union. See oneOfUnionOutrunsBranches for what the
	// union gets wrong there — a false rejection for the documents that satisfy
	// exactly one branch, and a false acceptance for the documents that satisfy
	// none.
	//
	// The evaluator judges every branch against the raw value, so it needs no Go
	// type per branch: a `false` branch refuses everything and a `const` branch
	// admits its own value, which is what the union could express for neither.
	// That also settles the second half of the same defect — the union lives in a
	// struct, so a scalar the schema allows had nowhere to decode into at all.
	//
	// It claims the schema only when the evaluator reads the whole of it. Where
	// it cannot, the union stays: wrong as it is about these branches, it still
	// enforces more than the `any` alias that would otherwise follow.
	//
	// The guards are the arm above's, with oneOfDescribesObject inverted, so
	// between them the two claim every bare oneOf and nothing else. In
	// particular a schema that declares or implies its own type keeps the
	// alias-with-OneOfVariants path, where the branches are already evaluated
	// against the typed value: {"type":"integer","oneOf":[{"minimum":10},
	// {"maximum":5}]} must stay an int64, not become a raw wrapper.
	if len(s.OneOf) > 0 && !hasProperties(s) && g.oneOfDescribesObject(s) &&
		primarySchemaType(s) == "" && g.inferTypeFromConstraints(s) == "" &&
		oneOfUnionKeepsWholeSchema(s) && g.oneOfUnionOutrunsBranches(s) {
		if def := g.rawWrapperDef(name, s); def != nil {
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, def)
			return nil
		}
	}

	// Object with properties, patternProperties, object oneOf variants, or
	// unevaluatedProperties → struct. A oneOf whose variants are constraint-only
	// (they say nothing about object shape) is not an object union and must not
	// force a struct — those fall through to the primitive/array alias paths so
	// the oneOf branches attach to the declared/inferred type.
	if namesObjectKeys(s) || g.oneOfDescribesObject(s) || s.UnevaluatedProperties != nil {
		// Only accept non-object data for schemas with object keywords (properties/patternProperties)
		// but without oneOf (which is type-agnostic and should validate all types).
		canAcceptNonObject := (namesObjectKeys(s) || s.UnevaluatedProperties != nil) && len(s.OneOf) == 0
		return g.generateStructDef(name, s, canAcceptNonObject)
	}

	// Ref only → alias (handles $ref, $recursiveRef)
	if effRef := s.EffectiveRef(); effRef != "" && (g.refOverridesSiblingsForSchema(s) || !hasRefStructuralSiblings(s)) {
		resolved := g.resolveRefInContext(effRef, s)
		if resolved != nil {
			if def := g.refCycleAliasDef(name, s, resolved); def != nil {
				g.generated[name] = true
				g.output.TypeDefs = append(g.output.TypeDefs, def)
				return nil
			}
			pushed := g.pushDynamicScope(resolved)
			refName := g.goNameForResolvedRef(effRef, resolved, refToGoName(effRef))
			// Generate the referenced type definition (e.g., for remote $ref targets).
			if err := g.generateTypeDef(refName, resolved); err != nil {
				if pushed {
					g.popDynamicScope()
				}
				return err
			}
			// If the ref target was generated as a wrapper struct (InferredAliasDef,
			// BigIntAliasDef, NotSchemaDef, TypeOnlySchemaDef or DynamicSchemaDef),
			// creating `type Root Target` would not inherit methods -- Root would
			// carry neither the UnmarshalJSON that fills the raw value nor the
			// Validate that checks it, so the constraint would be silently dropped.
			// Instead, generate Root directly from the resolved schema.
			if g.isInferredAlias(refName) || g.isBigIntAlias(refName) || g.isNotSchema(refName) || g.isTypeOnlySchema(refName) || g.isDynamicSchema(refName) {
				err := g.generateTypeDef(name, resolved)
				if pushed {
					g.popDynamicScope()
				}
				return err
			}
			if pushed {
				g.popDynamicScope()
			}
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
				Name:        name,
				Underlying:  &NamedType{Name: refName},
				Description: s.Description,
			})
			return nil
		}
	}

	// $dynamicRef → resolve via dynamic scope chain.
	// Plain name fragments (like "#items") resolve via $dynamicAnchor with scope walking.
	// JSON pointer fragments (like "#/$defs/foo") resolve identically to $ref.
	// URI-based $dynamicRef (like "extended#meta") resolves the URI part first, then
	// checks the bookend $dynamicAnchor and walks the dynamic scope.
	if s.DynamicRef != "" && (g.refOverridesSiblingsForSchema(s) || !hasRefStructuralSiblings(s)) {
		resolved := g.resolveDynamicRef(s.DynamicRef, s)
		if resolved != nil {
			if def := g.refCycleAliasDef(name, s, resolved); def != nil {
				g.generated[name] = true
				g.output.TypeDefs = append(g.output.TypeDefs, def)
				return nil
			}
			refName := g.goNameForResolvedRef(s.DynamicRef, resolved, refToGoName(s.DynamicRef))
			if err := g.generateTypeDef(refName, resolved); err != nil {
				return err
			}
			if g.isInferredAlias(refName) || g.isBigIntAlias(refName) || g.isNotSchema(refName) || g.isTypeOnlySchema(refName) || g.isDynamicSchema(refName) {
				return g.generateTypeDef(name, resolved)
			}
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
				Name:        name,
				Underlying:  &NamedType{Name: refName},
				Description: s.Description,
			})
			return nil
		}
	}

	// Root-level "not" schema: generates a wrapper around json.RawMessage that
	// validates the negated constraint. Only handles schemas where "not" is the
	// sole meaningful keyword (no type, properties, items, etc.).
	if notDef := g.extractNotSchemaDef(name, s); notDef != nil {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, notDef)
		return nil
	}

	// Multi-type or null-only schemas: generates a wrapper around json.RawMessage
	// that validates the value's JSON type against the allowed types. This handles
	// schemas like {"type": "null"}, {"type": ["integer","string"]}, etc. that
	// don't map to a single Go type.
	if toDef := g.extractTypeOnlySchemaDef(name, s); toDef != nil {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, toDef)
		return nil
	}

	// A format with no "type": `type X any` carries no Validate, so the format
	// was asserted nowhere. The wrapper accepts every JSON value and checks the
	// format only when the value arrived as a string. See stringAnnotationOnlySchema.
	if fDef := g.stringAnnotationOnlyDef(name, s); fDef != nil {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, fDef)
		return nil
	}

	// Draft 3 allows schema-valued alternatives inside the type array. When mixed
	// with a single primitive type (for example integer OR an object schema), use
	// the same raw wrapper as multi-type schemas so both alternatives can validate.
	if len(s.TypeSchemas) > 0 {
		g.generated[name] = true
		branches, allowed := g.typeUnionBranches(s, name)
		g.output.TypeDefs = append(g.output.TypeDefs, &TypeOnlySchemaDef{
			Name:         name,
			Description:  s.Description,
			AllowedTypes: allowed,
			TypeBranches: branches,
		})
		return nil
	}

	// Simple primitive type → alias (or defined type if it has validation constraints)
	// When no explicit type is declared, infer from constraint keywords.
	primaryType := primarySchemaType(s)
	isInferred := false
	if primaryType == "" {
		primaryType = g.inferTypeFromConstraints(s)
		if primaryType != "" {
			isInferred = true
		}
	}
	if primaryType != "" && primaryType != "object" && primaryType != "array" {
		goType := g.resolveType(s, name)
		var rules []ValidationRule
		var anyOfVariants [][]ValidationRule
		var oneOfVariants [][]ValidationRule
		if g.validationKeywordsEnabled() {
			rules = g.aliasValidationRules(s, goType)
			anyOfVariants = extractAnyOfVariantRules(s, goType)
			oneOfVariants = extractOneOfVariantRules(s, goType)
		}
		g.generated[name] = true
		if isInferred {
			// Type was inferred from constraints — generate wrapper struct that
			// accepts any JSON value but validates only matching types.
			g.output.TypeDefs = append(g.output.TypeDefs, &InferredAliasDef{
				Name:             name,
				Description:      s.Description,
				InferredGoType:   goType,
				InferredJSONType: primaryType,
				Validations:      rules,
				AnyOfVariants:    anyOfVariants,
				OneOfVariants:    oneOfVariants,
				NeedsNullCheck:   !schemaAllowsNull(s),
			})
		} else if g.config.BigIntSupport && primaryType == "integer" {
			// BigInt support: generate wrapper struct with int64 + *big.Int.
			g.output.TypeDefs = append(g.output.TypeDefs, &BigIntAliasDef{
				Name:           name,
				Description:    s.Description,
				Validations:    rules,
				AnyOfVariants:  anyOfVariants,
				OneOfVariants:  oneOfVariants,
				NeedsNullCheck: !schemaAllowsNull(s),
				AllowsNull:     schemaAllowsNull(s),
				StrictInteger:  g.requiresStrictIntegerToken(s),
			})
		} else {
			g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
				Name:           name,
				Underlying:     goType,
				Description:    s.Description,
				Validations:    rules,
				AnyOfVariants:  anyOfVariants,
				OneOfVariants:  oneOfVariants,
				StrictInteger:  primaryType == "integer" && g.requiresStrictIntegerToken(s),
				NeedsNullCheck: !schemaAllowsNull(s),
			})
		}
		return nil
	}

	// Array type → alias (or defined type if it has validation constraints)
	if primaryType == "array" {
		// Mark as generated before resolving the item type, not after. Both
		// resolveType and buildTupleItemDefs descend into this schema's items,
		// and an item that refers back here re-enters generateTypeDef; without
		// the flag already set, that re-entry runs to completion and appends a
		// second, identical declaration under the same name -- which Go rejects
		// as a redeclaration.
		g.generated[name] = true
		goType := g.resolveType(s, name)
		var rules []ValidationRule
		var anyOfVariants [][]ValidationRule
		var oneOfVariants [][]ValidationRule
		if g.validationKeywordsEnabled() {
			rules = g.aliasValidationRules(s, goType)
			anyOfVariants = extractAnyOfVariantRules(s, goType)
			oneOfVariants = extractOneOfVariantRules(s, goType)
		}
		if isInferred {
			// Inferred array type — wrapper struct for non-array fallback.
			// Extract item-level validation constraints.
			elemGoType, _ := containerElem(goType)
			itemsFalse, itemsType, itemsTypeName, itemsChecks, itemsNested, tupleItems, addlItemsFalse, addlItemsType, addlItemsTypeName := g.extractInferredItemConstraints(s, name, elemGoType)
			// Extract contains/minContains/maxContains constraints.
			containsDef, minContains, maxContains := g.extractContainsDef(s, name)
			// Extract unevaluatedItems constraint.
			unevalItems := g.buildUnevaluatedItemsDef(s)
			if !g.validationKeywordsEnabled() {
				itemsFalse = false
				itemsType = ""
				itemsTypeName = ""
				itemsChecks = nil
				itemsNested = nil
				tupleItems = nil
				addlItemsFalse = false
				addlItemsType = ""
				addlItemsTypeName = ""
				containsDef = nil
				minContains = nil
				maxContains = nil
				unevalItems = nil
			}
			// When item-level or contains validation is needed, force GoType to []any so that
			// json.Unmarshal accepts any array (per-element validation in Validate()).
			// If the typed array (e.g., []int64) were used, unmarshal would fail
			// entirely on mixed-type arrays, masking per-element errors.
			inferredGoType := goType
			if itemsFalse || itemsType != "" || itemsTypeName != "" ||
				len(itemsChecks) > 0 || itemsNested != nil ||
				len(tupleItems) > 0 || addlItemsFalse || addlItemsType != "" ||
				addlItemsTypeName != "" || containsDef != nil || unevalItems != nil {
				inferredGoType = &ArrayType{ItemType: &PrimitiveType{Name: "any"}}
			}
			g.output.TypeDefs = append(g.output.TypeDefs, &InferredAliasDef{
				Name:                    name,
				Description:             s.Description,
				InferredGoType:          inferredGoType,
				InferredJSONType:        primaryType,
				Validations:             rules,
				AnyOfVariants:           anyOfVariants,
				OneOfVariants:           oneOfVariants,
				NeedsNullCheck:          !schemaAllowsNull(s),
				ItemsFalse:              itemsFalse,
				ItemsType:               itemsType,
				ItemsTypeName:           itemsTypeName,
				ItemsChecks:             itemsChecks,
				ItemsNested:             itemsNested,
				TupleItems:              tupleItems,
				AdditionalItemsFalse:    addlItemsFalse,
				AdditionalItemsType:     addlItemsType,
				AdditionalItemsTypeName: addlItemsTypeName,
				Contains:                containsDef,
				MinContains:             minContains,
				MaxContains:             maxContains,
				UnevaluatedItems:        unevalItems,
			})
		} else {
			tupleItems := g.buildTupleItemDefs(s, name)
			tupleTail := g.buildTupleTailDef(s, name)
			containsDef, minContains, maxContains := g.extractContainsDef(s, name)
			unevalItems := g.buildUnevaluatedItemsDef(s)
			var itemValidations []ItemValidationDef
			if g.validationKeywordsEnabled() {
				// The alias *is* the slice, so the checks hang off the
				// receiver rather than off a field.
				if iv := g.buildItemValidation(name, "", "", goType, s); iv != nil {
					itemValidations = append(itemValidations, *iv)
				}
			} else {
				tupleItems = nil
				tupleTail = nil
				containsDef = nil
				minContains = nil
				maxContains = nil
				unevalItems = nil
			}
			g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
				Name:             name,
				Underlying:       goType,
				Description:      s.Description,
				Validations:      rules,
				AnyOfVariants:    anyOfVariants,
				OneOfVariants:    oneOfVariants,
				TupleItems:       tupleItems,
				TupleTail:        tupleTail,
				ItemValidations:  itemValidations,
				Contains:         containsDef,
				MinContains:      minContains,
				MaxContains:      maxContains,
				UnevaluatedItems: unevalItems,
				StrictInteger:    primaryType == "integer" && g.requiresStrictIntegerToken(s),
				NeedsNullCheck:   !schemaAllowsNull(s),
				NullCheck:        g.aliasNullCheck(goType, s),
			})
		}
		return nil
	}

	// Object with no properties → struct with overflow map for lossless round-trip.
	// If additionalProperties is explicitly false, still generate overflow map to capture
	// unknown keys for validation rejection.
	if primaryType == "object" {
		g.generatePropertylessObjectDef(name, s)
		return nil
	}

	// Fallback: alias to any. Before giving up on validation entirely, check
	// whether the schema still constrains the value -- through an applicator, a
	// "not", a shape stated without a type of its own. `type X any` cannot carry
	// a Validate() method, so every one of those would be dropped silently; a
	// raw-JSON wrapper keeps them enforceable.
	if def := g.rawWrapperDef(name, s); def != nil {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, def)
		return nil
	}

	// The rules aliasValidationRules would return are dropped here on
	// purpose, and always have been: an alias whose underlying type is `any` is
	// interface-underlying, Go forbids methods on it, and the emitter's
	// CanHaveMethods gate never writes the Validate they would go in. Building
	// them only to discard them said the opposite. unenforcedAliasDef reports
	// what is being lost instead.
	g.generated[name] = true
	g.output.TypeDefs = append(g.output.TypeDefs, g.unenforcedAliasDef(name, s))
	return nil
}

// generatePropertylessObjectDef emits the struct an object schema with no
// declared properties gets: an overflow map, so the document round-trips
// losslessly, and the object-level keywords that have no field of their own to
// hang off -- min/maxProperties, required, dependentRequired, dependentSchemas
// and propertyNames.
//
// Split out of generateTypeDef so that generateAllOfDef can reach it too. An
// allOf whose branches contribute no properties lands on a merged schema of
// exactly this shape, and without a struct to carry them every one of those
// keywords is dropped.
func (g *Generator) generatePropertylessObjectDef(name string, s *schema.Schema) {
	g.generated[name] = true
	var additionalProps *AdditionalPropertiesDef
	// The keys of an object with no declared properties are all "additional",
	// so a schema-valued additionalProperties governs every value the overflow
	// map holds. Nothing else checks them: the map's Go value type stops a
	// wrong JSON type in the decoder and says nothing about any other keyword.
	var overflowValidation *ItemValidationDef
	if s.AdditionalProperties != nil && s.AdditionalProperties.Bool != nil && !*s.AdditionalProperties.Bool {
		// additionalProperties: false → overflow map with Forbidden flag for validation
		additionalProps = &AdditionalPropertiesDef{
			ValueType: &PrimitiveType{Name: "json.RawMessage"},
			Forbidden: true,
		}
	} else if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		valueType, ok := g.boxedInferredType(s.AdditionalProperties.Schema, name+"Value")
		if !ok {
			valueType = g.resolveType(s.AdditionalProperties.Schema, name+"Value")
		}
		additionalProps = &AdditionalPropertiesDef{ValueType: valueType}
		overflowValidation = g.buildOverflowValidation(name, valueType, s.AdditionalProperties.Schema)
	} else {
		// Default or additionalProperties: true → json.RawMessage overflow map
		additionalProps = &AdditionalPropertiesDef{
			ValueType: &PrimitiveType{Name: "json.RawMessage"},
		}
	}
	needsNullCheck := !schemaAllowsNull(s)
	acceptNonObject := !schemaHasExplicitType(s, "object")
	needsMarshal := additionalProps != nil || acceptNonObject
	needsUnmarshal := additionalProps != nil || needsNullCheck || acceptNonObject
	var itemValidations []ItemValidationDef
	if g.validationKeywordsEnabled() && overflowValidation != nil {
		itemValidations = append(itemValidations, *overflowValidation)
	}
	var validations []ValidationRule
	if g.validationKeywordsEnabled() && s.MaxProperties != nil {
		validations = append(validations, ValidationRule{
			RuleType: "maxProperties", Value: s.MaxProperties.Int(),
		})
	}
	if g.validationKeywordsEnabled() && s.MinProperties != nil {
		validations = append(validations, ValidationRule{
			RuleType: "minProperties", Value: s.MinProperties.Int(),
		})
	}
	// Required fields on property-less object schemas (e.g., {"type":"object","required":["foo"]}).
	// All required names land in AdditionalProperties since there are no declared properties.
	var requiredJSON []string
	if g.validationKeywordsEnabled() && len(s.Required) > 0 {
		requiredJSON = dedupeStrings(s.Required)
		needsUnmarshal = true
	}
	// Extract dependentRequired constraints.
	var depRequired []DependentRequiredDef
	if g.validationKeywordsEnabled() {
		for trigger, deps := range s.DependentRequired {
			if len(deps) > 0 {
				sorted := make([]string, len(deps))
				copy(sorted, deps)
				sort.Strings(sorted)
				depRequired = append(depRequired, DependentRequiredDef{
					TriggerKey: trigger,
					Required:   sorted,
				})
			}
		}
	}
	sort.Slice(depRequired, func(i, j int) bool {
		return depRequired[i].TriggerKey < depRequired[j].TriggerKey
	})
	if len(depRequired) > 0 {
		needsUnmarshal = true
	}
	// Extract dependentSchemas constraints.
	var depSchemas []DependentSchemaConstraint
	if g.validationKeywordsEnabled() {
		depSchemas = g.extractDependentSchemaConstraints(s)
	}
	if len(depSchemas) > 0 {
		needsUnmarshal = true
	}
	// Extract propertyNames constraint.
	var propNames *PropertyNamesDef
	if s.PropertyNames != nil && g.validationKeywordsEnabled() {
		propNames = g.extractPropertyNamesDef(s.PropertyNames)
		if propNames != nil {
			needsUnmarshal = true // need _jsonKeys for validation
		}
	}
	// Every key of an object with no declared properties lands in the overflow
	// map, decoded by hand, where json.Unmarshal of `null` into a string leaves
	// "" and reports nothing.
	var overflowNullCheck *NullCheckDef
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil && additionalProps != nil {
		overflowNullCheck = g.buildNullCheck("", additionalProps.ValueType, s.AdditionalProperties.Schema)
		if overflowNullCheck != nil {
			needsUnmarshal = true
		}
	}
	// A branch's own additionalProperties/unevaluatedProperties binds here too.
	// The one the merge adopted is skipped by pointer identity, so the overflow
	// map above and a check here never both answer for the same keyword.
	branchChecks := g.collectBranchOverflowChecks(s, name)
	runtimeBranchChecks := g.collectRuntimeBranchChecks(s)
	if len(branchChecks) > 0 || len(runtimeBranchChecks) > 0 {
		needsUnmarshal = true
	}
	g.output.TypeDefs = append(g.output.TypeDefs, &StructDef{
		Name:                 name,
		Description:          s.Description,
		AdditionalProperties: additionalProps,
		DependentSchemas:     depSchemas,
		DependentRequired:    depRequired,
		PropertyNames:        propNames,
		Validations:          validations,
		ItemValidations:      itemValidations,
		BranchOverflowChecks: branchChecks,
		RuntimeBranchChecks:  runtimeBranchChecks,
		RequiredJSON:         requiredJSON,
		OverflowNullCheck:    overflowNullCheck,
		NeedsMarshal:         needsMarshal,
		NeedsUnmarshal:       needsUnmarshal,
		NeedsNullCheck:       needsNullCheck,
		AcceptNonObject:      acceptNonObject,
	})
}

// propertylessObjectHasChecks reports whether an object schema with no declared
// properties still states something generatePropertylessObjectDef would emit a
// check for. Only these keywords are considered, because only these are what
// that struct enforces: an overflow map on its own validates nothing, and a
// struct built for one would be a worse type than the `any` it replaced.
//
// A schema-valued `additionalProperties` is one of them, because that struct
// does check it: with nothing declared beside it the overflow map holds every
// key, and generatePropertylessObjectDef hangs the sub-schema's own keywords off
// it (see buildOverflowValidation). Leaving it out was the reason
// {"allOf":[{"type":"object","additionalProperties":{"type":"string",
// "minLength":7}}]} came out `type X any` -- the merge had the keyword by then
// and this predicate did not ask for it.
//
// `additionalProperties: false` is another, for the reason forbidsEveryKey
// gives: with no properties declared it permits no key at all, and the struct
// rejects the whole overflow map. It was left out on the grounds that honouring
// it turns a schema accepting any object into one accepting only {} -- which is
// what the schema says, and what the same schema in a $defs entry has always
// done, so leaving it out here made the answer depend on where the schema was
// written rather than on what it says.
//
// One keyword is still deliberately absent: `type` alone would make the struct
// reject non-object instances, which the `any` alias accepts today. That is a
// correction the spec supports and a far larger claim than the merge gap this
// predicate exists to close. A boolean `true` is absent for the opposite
// reason -- it permits everything, so there is nothing to enforce.
func (g *Generator) propertylessObjectHasChecks(s *schema.Schema) bool {
	if !g.validationKeywordsEnabled() {
		return false
	}
	return mapValueSchema(s, "object") != nil ||
		forbidsEveryKey(s) ||
		s.PropertyNames != nil ||
		s.MinProperties != nil || s.MaxProperties != nil ||
		len(s.Required) > 0 ||
		len(s.DependentRequired) > 0 ||
		len(s.DependentSchemas) > 0
}

// dedupeStrings returns the input with repeats removed, order preserved. An
// allOf merge appends every branch's `required` list, so the same name can
// arrive more than once; the emitted presence loop would then report it twice.
func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// promoteConstToEnum returns a schema whose Enum encodes a lone const value so
// that const can be validated as a single-value enum. It never mutates s: when
// promotion applies it returns a shallow copy. Schema nodes are shared across
// $ref targets and reused across Generate calls, so mutating the input in place
// would leak a synthesized Enum into unrelated types (or a second generation of
// the same tree). When s already has an Enum, or no const, s is returned as-is.
func promoteConstToEnum(s *schema.Schema) *schema.Schema {
	if len(s.Enum) > 0 {
		return s
	}
	if s.Const != nil {
		c := *s
		c.Enum = []any{*s.Const}
		return &c
	}
	// {"const": null}: Go's json.Unmarshal leaves *any nil for null, so s.Const
	// is nil even though const was present. ConstIsNull records that.
	if s.ConstIsNull {
		c := *s
		c.Enum = []any{nil}
		return &c
	}
	return s
}

// fieldNameFor returns the Go field name for a JSON property of the given type.
// A configured override (from Config.FieldNames) wins over the derived name and
// is recorded as applied so the caller can report unused overrides.
func (g *Generator) fieldNameFor(typeName, jsonProp string) (name string, overridden bool) {
	if override, ok := g.config.FieldNames.Override(typeName, jsonProp); ok {
		if g.appliedOverrides == nil {
			g.appliedOverrides = make(map[string]map[string]bool)
		}
		if g.appliedOverrides[typeName] == nil {
			g.appliedOverrides[typeName] = make(map[string]bool)
		}
		g.appliedOverrides[typeName][jsonProp] = true
		return override, true
	}
	return JSONPropertyToGoName(jsonProp), false
}

// AppliedOverrides reports the FieldNames overrides that were applied during
// generation, keyed by type name → JSON property name.
func (g *Generator) AppliedOverrides() map[string]map[string]bool {
	return g.appliedOverrides
}

// UnenforcedSchema names a generated type whose schema stated a constraint that
// the generated code does not check.
type UnenforcedSchema struct {
	TypeName string
	Keywords []string
}

// UnenforcedSchemas returns the types that came out as `type X any` despite
// their schema constraining something, in the order they were generated.
//
// This is the one dropped check that leaves no trace in the output a caller
// could act on: `any` has no Validate method to be missing, and unmarshalling
// into it always succeeds, so a schema schemagen could not compile looks exactly
// like a schema that said nothing. Reporting it is what turns "this is
// unconstrained" back into "I could not read this".
//
// A name that did not survive into the file is dropped from the report. Some
// arms mint a type, discover it can carry nothing, and leave it undeclared;
// naming one of those would send the reader looking for a declaration that is
// not there, and a diagnostic nobody can act on is one they learn to skip.
func (g *Generator) UnenforcedSchemas() []UnenforcedSchema {
	declared := make(map[string]bool, len(g.output.TypeDefs))
	for _, td := range g.output.TypeDefs {
		declared[td.TypeName()] = true
	}
	kept := make([]UnenforcedSchema, 0, len(g.unenforced))
	for _, u := range g.unenforced {
		if declared[u.TypeName] {
			kept = append(kept, u)
		}
	}
	return kept
}

// generateStructDef produces a StructDef from an object schema.
// It also handles oneOf properties within the struct.
// When acceptNonObject is true and the schema has no explicit "type":"object",
// non-object JSON data (numbers, strings, arrays) is silently accepted rather
// than causing an unmarshal error. This should only be true for schemas whose
// constraints are purely object-specific (properties, additionalProperties, etc.)
// and NOT for schemas generated from applicator merging (allOf, anyOf).
func (g *Generator) generateStructDef(name string, s *schema.Schema, acceptNonObject bool) error {
	g.generated[name] = true
	g.structsInProgress[name] = true
	defer delete(g.structsInProgress, name)

	requiredList := s.Required
	if !g.validationKeywordsEnabled() {
		requiredList = nil
	}
	requiredSet := make(map[string]bool, len(requiredList))
	for _, r := range requiredList {
		requiredSet[r] = true
	}

	// Collect regular fields and oneOf fields separately.
	var fields []FieldDef
	var oneOfs []OneOfDef
	needsMarshal := false
	needsUnmarshal := false

	// Sort property names for deterministic output.
	propNames := sortedKeys(s.Properties)

	// First pass: compute Go field names, honoring configured overrides.
	goFieldNames := make(map[string]string, len(propNames)) // JSON name → Go name
	overridden := make(map[string]bool, len(propNames))     // JSON name → came from an override
	derived := make(map[string]string, len(propNames))      // JSON name → name before suffixing
	for _, propName := range propNames {
		goName, isOverride := g.fieldNameFor(name, propName)
		derived[propName] = goName
		overridden[propName] = isOverride
	}
	// Second pass: deduplicate only derived (non-overridden) names by appending a
	// numeric suffix. Overrides are pinned by the user and never suffixed.
	//
	// The generated-member names (Validate/MarshalJSON/UnmarshalJSON/SetDefaults
	// methods and the AdditionalProperties/PatternProperties overflow fields) are
	// pre-occupied so that a DERIVED field name colliding with one of them is
	// renamed by the same numeric-suffix mechanism used for property-name clashes.
	// The JSON tag keeps the original property name, so the wire format is
	// unaffected. We reserve these names UNCONDITIONALLY — even when this struct
	// does not actually generate the corresponding member (e.g. no overflow field
	// because additionalProperties is absent) — because it is simpler and the only
	// cost is a numeric suffix on an exported field that would otherwise be one of
	// these reserved words. Field-map overrides are handled separately below and
	// still ERROR on these names (see reservedFieldNames).
	nameCount := make(map[string]int)
	for _, member := range generatedMemberNames {
		nameCount[member] = 1
	}
	for _, propName := range propNames {
		if !overridden[propName] {
			nameCount[derived[propName]]++
		}
	}
	nameSeen := make(map[string]int)
	for _, propName := range propNames {
		goName := derived[propName]
		if !overridden[propName] && nameCount[goName] > 1 {
			nameSeen[goName]++
			goName = fmt.Sprintf("%s%d", goName, nameSeen[goName])
		}
		goFieldNames[propName] = goName
	}
	// An override may collide with another field's name, with a generated method,
	// or with the synthesized overflow field; any of these produce uncompilable
	// Go, so reject them with an actionable error rather than silently suffixing
	// (which would defeat the override).
	finalNames := make(map[string]string, len(propNames)) // Go name → JSON name
	for _, propName := range propNames {
		goName := goFieldNames[propName]
		if overridden[propName] {
			if reason, reserved := reservedFieldNames[goName]; reserved {
				return fmt.Errorf("type %s: field-map override maps property %q to %q, which collides with %s; choose a different name",
					name, propName, goName, reason)
			}
		}
		if other, dup := finalNames[goName]; dup {
			return fmt.Errorf("type %s: field name %q for property %q collides with property %q (check --field-map overrides)",
				name, goName, propName, other)
		}
		finalNames[goName] = propName
	}

	for _, propName := range propNames {
		propSchema := s.Properties[propName]
		goFieldName := goFieldNames[propName]
		required := requiredSet[propName]

		// A null JSON value for a property schema (e.g. {"properties":{"a":null}})
		// is not a valid schema — a property schema must be an object or a boolean.
		// Reject it with an actionable error rather than dereferencing nil below.
		if propSchema == nil {
			return fmt.Errorf("property %q: schema is null (a property schema must be an object or boolean)", propName)
		}

		// Check if this property uses oneOf. Only when the union would carry the
		// whole property schema — otherwise the siblings it declares, its own
		// properties and required most damagingly, never reach any generated
		// type — only when the branches give it something to select on, and only
		// when what it selects on agrees with what the branches say. See
		// oneOfUnionKeepsWholeSchema, oneOfIsUnselectableUnion and
		// oneOfUnionOutrunsBranches.
		if propSchema != nil && len(propSchema.OneOf) > 0 && g.oneOfRendersAsUnion(propSchema) {
			oneOfDef, err := g.generateOneOfForProperty(name, propName, goFieldName, propSchema)
			if err != nil {
				return fmt.Errorf("property %s (oneOf): %w", propName, err)
			}
			if oneOfDef != nil {
				oneOfDef.Required = required
				oneOfs = append(oneOfs, *oneOfDef)
				needsMarshal = true
				needsUnmarshal = true
				continue
			}
		}

		goType, err := g.resolvePropertyType(propSchema, name, goFieldName, s)
		if err != nil {
			return fmt.Errorf("property %s: %w", propName, err)
		}

		omitEmpty := g.config.OmitEmpty && !required
		// A null-only property resolves to the raw-value wrapper, which keeps the
		// bytes it was handed: there a present null and an absent property are
		// different values, and the ",omitzero" tag computed below drops exactly
		// the absent one.
		//
		// Every other spelling of "may be null" resolves to a pointer or a
		// collection, whose nil means both -- and the answer used to be to drop
		// omitempty, so that nil was written as null. That kept an explicit null
		// at the cost of inventing one for a property the document never carried,
		// which is the same round-trip break in the other direction. Now that
		// UnmarshalJSON records which keys arrived as null (see
		// nullPresenceTracked), the tag no longer has to carry that distinction:
		// omitempty drops the absent property and MarshalJSON writes back exactly
		// the nulls the document had. See issue #110.
		//
		// The wrapper answers for every schema it types, not only a null-only one.
		// What keeps the two apart is the bytes it holds -- "null" for a present
		// null and nothing at all for an absent property -- and that is the same
		// whatever else the schema says, which is why nullPresenceTracked declines
		// to track the property separately for exactly this type. Reading the
		// wrapper only under isNullOnly is what left a nullable composition the
		// evaluator claims, {"anyOf":[{"type":"string","const":"a"},{"type":
		// "null"}]}, with neither tag: the field was written unconditionally and an
		// absent property came back as an invented null.
		nullSurvivesOmit := g.isRawValueWrapperType(goType) ||
			g.nullPresenceTracked(propSchema, goType)
		if omitEmpty && !nullSurvivesOmit {
			// Suppress omitempty for properties whose schema explicitly includes null
			// (via type list or anyOf/oneOf composition). These generate pointer types
			// where omitempty would incorrectly drop JSON null values.
			// NOTE: This does NOT suppress omitempty for all pointer types — recursive
			// self-refs also produce pointers but should keep omitempty so that absent
			// optional fields are omitted rather than emitted as null.
			if isNullable(propSchema) || g.isNullableComposition(propSchema) {
				omitEmpty = false
			}
		}
		// Optional array/slice fields are left as []T: a slice is already nilable,
		// so omitempty omits it when nil. (Absent and an explicit empty [] both
		// serialize as omitted — they are not distinguished.)
		// For optional struct fields with omitempty, wrap in a pointer (*T) so that
		// absent → nil (omitted). encoding/json's omitempty never considers struct
		// values as empty, and types with custom MarshalJSON are always marshaled
		// (producing non-null output even when all fields are zero).
		if omitEmpty && !goType.IsPointer() && g.isObjectProperty(goType, propSchema) {
			goType = &PointerType{Inner: goType}
		}
		// For optional scalar fields with omitempty, wrap in a pointer so that the
		// zero value ("", false, 0, 0.0) is distinguishable from
		// absent. Without this, omitempty conflates "absent" with "zero value".
		//
		// A name over the primitive changes nothing about that: a $ref to a
		// "type":"integer" definition, or an inline enum or const, produces a
		// named type whose underlying is still int64, and a legitimate 0 in it
		// is just as invisible to omitempty. It is worse there, in fact —
		// because the named type carries a Validate(), Validate() on the owner
		// guards the call with `!= <zero>` (see populateValidatableFields and
		// the validatable-field arm of the validation template), so the zero
		// value both vanished from the output and skipped every constraint the
		// definition declares. The pointer removes both: nil is the absent
		// value omitempty drops, and the guard becomes a nil check.
		if omitEmpty && !goType.IsPointer() && (isZeroLossyPrimitive(goType) || g.isZeroLossyNamedType(goType)) {
			goType = &PointerType{Inner: goType}
		}
		manualJSON := needsManualJSON(propName)

		// Optional slice/map fields use ",omitzero" rather than ",omitempty" so a
		// present-but-empty collection ([] or {}) survives a marshal round-trip
		// while an absent one is still omitted. After unmarshal a present empty
		// collection is non-nil and an absent one is nil, and omitzero omits only
		// the nil (zero) value. omitempty would drop both, conflating absent with
		// empty. (Slices stay []T — no pointer — so nil-safe access is preserved.)
		// A raw-value wrapper is a struct, so omitempty never drops it and its
		// MarshalJSON writes "null" for an absent value. It carries IsZero, so
		// omitzero omits exactly the absent case.
		omitZero := omitEmpty && (g.isCollectionType(goType) || g.isRawValueWrapperType(goType))

		// A property name that cannot go in a struct tag gets `json:"-"` and is
		// written by hand in MarshalJSON, so neither omitempty nor omitzero ever
		// reaches it -- the omission has to be spelled out. The rule is
		// omitzero's, not omitempty's: skip only the value that unmarshal leaves
		// when the property was absent, never a value the document actually
		// carried. A pointer, a collection and an interface are nil exactly in
		// that case -- unmarshal assigns only when the key is present, so a
		// present [] or {} comes back non-nil -- and a raw-value wrapper reports
		// it through IsZero. omitempty would additionally drop a
		// present-but-empty collection, inventing an absence the document did
		// not have: the same class of round-trip break as the invented null this
		// replaces, in the other direction.
		//
		// An interface is also nil for an explicit null, which the nil arm would
		// drop. The null is put back afterwards: a property whose schema admits
		// one is recorded by UnmarshalJSON and re-written by MarshalJSON from
		// that record, after the hand-written fields have had their say.
		//
		// A scalar is left unconditional. Its zero is indistinguishable from
		// absence without presence tracking, and writing it can only ever add a
		// property, while omitting it would erase an explicit "", 0 or false --
		// though in practice an optional scalar has been pointer-wrapped by now
		// and takes the nil arm.
		manualOmit := ""
		if manualJSON && omitEmpty {
			switch {
			case goType.IsPointer() || g.isCollectionType(goType) || g.isInterfaceType(goType):
				manualOmit = "nil"
			case g.isRawValueWrapperType(goType):
				manualOmit = "iszero"
			}
		}

		// Compute default literal if schema provides a default value.
		var defaultLiteral string
		if propSchema.Default != nil {
			lit, err := defaultToGoLiteral(*propSchema.Default, goType)
			if err != nil {
				return fmt.Errorf("property %q: %w", propName, err)
			}
			defaultLiteral = lit
		}

		fields = append(fields, FieldDef{
			Name:           goFieldName,
			JSONName:       propName,
			Type:           goType,
			OmitEmpty:      omitEmpty,
			OmitZero:       omitZero,
			Required:       required,
			Description:    propSchema.Description,
			Annotations:    annotationsOf(propSchema),
			ManualJSON:     manualJSON,
			ManualOmit:     manualOmit,
			DefaultLiteral: defaultLiteral,
			IntegerDecode:  g.integerDecodeFor(goType, propSchema),
		})
		if needsUnmarshalForIntegers(fields[len(fields)-1]) {
			needsUnmarshal = true
		}
	}

	// Handle top-level oneOf (not on a property but on the type itself). Same
	// rule as for a property: the union only stands in for the whole schema when
	// the schema says nothing else. Otherwise the struct keeps its own fields and
	// the branches are flattened into ObjectOneOfs below.
	if len(s.OneOf) > 0 && len(s.Properties) == 0 && oneOfUnionKeepsWholeSchema(s) {
		oneOfDef, err := g.generateOneOfForProperty(name, "", "Value", s)
		if err != nil {
			return fmt.Errorf("top-level oneOf: %w", err)
		}
		if oneOfDef != nil {
			oneOfs = append(oneOfs, *oneOfDef)
			needsMarshal = true
			needsUnmarshal = true
		}
	}

	// Handle additionalProperties.
	// Per JSON Schema spec, absent additionalProperties defaults to true (allow any extra keys).
	// In StrictProperties mode, absent additionalProperties is treated as false (no overflow map).
	var additionalProps *AdditionalPropertiesDef
	if s.AdditionalProperties != nil {
		if s.AdditionalProperties.Bool != nil {
			if *s.AdditionalProperties.Bool {
				// additionalProperties: true → map[string]json.RawMessage
				additionalProps = &AdditionalPropertiesDef{
					ValueType: &PrimitiveType{Name: "json.RawMessage"},
				}
				needsMarshal = true
				needsUnmarshal = true
			} else {
				// additionalProperties: false → still generate overflow map to capture
				// unknown keys, but mark as forbidden so Validate() rejects them.
				additionalProps = &AdditionalPropertiesDef{
					ValueType: &PrimitiveType{Name: "json.RawMessage"},
					Forbidden: true,
				}
				needsMarshal = true
				needsUnmarshal = true
			}
		} else if s.AdditionalProperties.Schema != nil {
			valueType, ok := g.boxedInferredType(s.AdditionalProperties.Schema, name+"Value")
			if !ok {
				valueType = g.resolveType(s.AdditionalProperties.Schema, name+"Value")
			}
			additionalProps = &AdditionalPropertiesDef{
				ValueType: valueType,
			}
			needsMarshal = true
			needsUnmarshal = true
		}
	} else if s.UnevaluatedProperties != nil {
		// unevaluatedProperties without explicit additionalProperties:
		// need an overflow map to capture unknown keys for unevaluated checking.
		//
		// additionalProperties is still absent here, so StrictProperties applies
		// exactly as it does in the arm below: the flag is documented as "treat
		// absent additionalProperties as false", and an unrelated keyword being
		// present is no reason for it to stop meaning that. The two bans are not
		// the same rule -- unevaluatedProperties also counts keys evaluated by
		// $ref/allOf/if-then subschemas, which additionalProperties:false does
		// not -- so leaving this one out let a key through on exactly the objects
		// where they differ.
		additionalProps = &AdditionalPropertiesDef{
			ValueType: &PrimitiveType{Name: "json.RawMessage"},
			Forbidden: g.config.StrictProperties,
		}
		needsMarshal = true
		needsUnmarshal = true
	} else if len(fields) > 0 || hasPropertyOneOf(oneOfs) || len(s.PatternProperties) > 0 {
		// No additionalProperties specified: per JSON Schema spec, defaults to true.
		// Add an overflow map to preserve extra properties for round-trip fidelity.
		// In StrictProperties mode, mark as Forbidden so Validate() rejects them,
		// but the data is still captured (not silently dropped).
		//
		// A oneOf on a *property* counts as much as a field: it leaves this
		// struct through oneOfs rather than fields, so a struct whose properties
		// are all unions used to look propertyless here and got no overflow map
		// at all. Every key it did not declare was then dropped on marshal —
		// including a key declared only inside one of its own object-level oneOf
		// branches, which is exactly the key that decides which branch the value
		// was in. A oneOf on the type itself does not count: MarshalJSON writes
		// the selected variant as the whole object there, so there is no aux
		// struct for an overflow map to be merged back into and nothing outside
		// the variant to preserve.
		additionalProps = &AdditionalPropertiesDef{
			ValueType: &PrimitiveType{Name: "json.RawMessage"},
			Forbidden: g.config.StrictProperties,
		}
		needsMarshal = true
		needsUnmarshal = true
	}

	// Collect validation rules.
	// Build maps of field metadata for filtering and annotating rules.
	fieldTypes := make(map[string]GoType)
	pointerFields := make(map[string]bool)
	for _, f := range fields {
		fieldTypes[f.Name] = f.Type
		if f.Type.IsPointer() {
			pointerFields[f.Name] = true
		}
	}
	var validations []ValidationRule
	var itemValidations []ItemValidationDef
	var containsValidations []FieldContainsDef
	var tupleValidations []FieldTupleDef
	var unevalItemsValidations []FieldUnevalItemsDef
	var nullChecks []NullCheckDef

	// Collect required JSON property names for presence-based validation.
	// These are checked via the raw JSON keys during UnmarshalJSON.
	// Include both declared property fields AND schema-level required names
	// without corresponding properties (e.g., {"type":"object","required":["foo"]}
	// with no properties — these land in AdditionalProperties but must be present).
	var requiredJSON []string
	declaredProps := make(map[string]bool, len(propNames))
	for _, pn := range propNames {
		declaredProps[pn] = true
	}
	for _, f := range fields {
		if f.Required {
			requiredJSON = append(requiredJSON, f.JSONName)
		}
	}
	for i := range oneOfs {
		if oneOfs[i].Required && oneOfs[i].JSONName != "" {
			requiredJSON = append(requiredJSON, oneOfs[i].JSONName)
		}
	}
	for _, r := range requiredList {
		if !declaredProps[r] {
			// Required name not declared as a property — still needs presence check.
			requiredJSON = append(requiredJSON, r)
		}
	}

	for _, propName := range propNames {
		propSchema := s.Properties[propName]
		if propSchema == nil {
			continue
		}
		goFieldName := goFieldNames[propName]
		// Boolean schema false → property is forbidden (any value is invalid).
		// Also check if a $ref/$dynamicRef resolves to a false boolean schema.
		//
		// Not when the field's own type is a raw-value wrapper, on the reasoning
		// the filter below already applies to every other rule: the rule is
		// emitted as `field != nil` and a wrapper struct is not nilable, so it
		// does not compile there. Nothing is lost. The wrapper is the forbidding
		// type generated from this very schema, and its Validate -- dispatched
		// from here like any other field's -- says the same thing more
		// completely: `!= nil` misses a property present with a JSON null, which
		// a `false` schema forbids too.
		if propSchema.IsFalseSchema() || g.resolvedToFalseSchema(propSchema) {
			if !g.isRawValueWrapperType(fieldTypes[goFieldName]) {
				validations = append(validations, ValidationRule{
					FieldName: goFieldName, JSONName: propName,
					RuleType: "forbidden", Value: true,
					PresenceTracked: true,
				})
			}
			continue
		}
		// In draft3-7, $ref overrides all sibling keywords — skip validation
		// rules from the property schema when it has a $ref.
		if propSchema.EffectiveRef() != "" && g.refOverridesSiblings() {
			continue
		}
		var rules []ValidationRule
		if g.validationKeywordsEnabled() {
			rules = extractValidationRules(goFieldName, propName, propSchema)
			// An allOf on the property itself tightens the same value. When the
			// branches carry object shape it is generateAllOfDef that flattens
			// them, but a branch that only bounds a scalar leaves the property a
			// plain Go string or int64 and its keywords reach nothing.
			rules = append(rules, allOfConstraintRules(goFieldName, propName, propSchema, fieldTypes[goFieldName])...)
			// Also apply constraints from patternProperties whose pattern matches this property name.
			for pattern, patSchema := range s.PatternProperties {
				if re, err := regexp.Compile(pattern); err == nil && re.MatchString(propName) {
					rules = append(rules, extractValidationRules(goFieldName, propName, patSchema)...)
				}
			}
		}
		// Filter out rules that don't make sense for the Go type (e.g.,
		// minimum/maximum on an 'any' field can't be compiled).
		filtered := rules[:0]
		for i := range rules {
			if pointerFields[rules[i].FieldName] {
				rules[i].IsPointer = true
			}
			// These rules belong to a property of this struct, so the struct's
			// own key map can say whether the document wrote it. That is the
			// only thing a forbidden rule wants to know, and the nil test it is
			// otherwise emitted as cannot tell a present null from an absent
			// property. See ValidationRule.PresenceTracked.
			if rules[i].RuleType == "forbidden" {
				rules[i].PresenceTracked = true
			}
			// Skip numeric/string/array validation on untyped 'any' fields,
			// but keep structural rules like "forbidden" that apply to all types.
			if ft, ok := fieldTypes[rules[i].FieldName]; ok {
				if pt, isPrim := ft.(*PrimitiveType); isPrim && pt.Name == "any" && rules[i].RuleType != "forbidden" {
					continue
				}
				// A keyword about a JSON type this field cannot hold is
				// satisfied by every value it can, so there is nothing to
				// check. Emitting it anyway does not even typecheck:
				// {"type":"integer","minLength":3} hands an int64 to
				// utf8.RuneCountInString. See ruleVacuousForType.
				if ruleVacuousForType(ft, rules[i].RuleType) {
					continue
				}
				// A raw-value wrapper keeps the value as JSON and enforces the
				// whole schema itself, through the branch types generated from
				// it. A field-level rule here would be both redundant and
				// uncompilable: the field is a struct, not the slice or string
				// the rule assumes.
				//
				// "forbidden" is no exception, though it used to be. It is
				// emitted as `field != nil`, which needs a nilable field; a
				// wrapper struct is not one, so the rule does not compile there
				// either. Nothing is lost by dropping it: the only property
				// schema that produces the rule is {"not":{}}, which is exactly
				// the schema the wrapper is generated from, and the wrapper's
				// own Validate says the same thing more completely -- `!= nil`
				// misses a present JSON null, which {"not":{}} also forbids.
				if g.isRawValueWrapperType(ft) {
					continue
				}
				// The same applies to the arbitrary-precision wrapper an inline
				// integer is materialized into under BigIntSupport (see
				// bigIntInlineWrapper). It was generated from this property's
				// own schema, so its Validate already carries these keywords --
				// compared through big.Float, which is the only comparison that
				// holds for a value no int64 can express. Emitted here the rule
				// would not compile at all: it converts the field to a float64,
				// and the field is a struct.
				if g.isBigIntAliasType(ft) {
					continue
				}
				// And to the wrapper a "format" with no declared type is
				// materialized into (see stringAnnotationOnlySchema), for the same
				// two reasons: it was built from this property's own schema, so
				// its Validate already carries the format and any length bound
				// or pattern beside it -- and it carries them *correctly*, only
				// when the instance turned out to be a string, which a
				// field-level rule cannot express. Emitted here the rule would
				// not compile: it hands the field to utf8.RuneCountInString, and
				// the field is a struct.
				if g.isInferredAliasType(ft) {
					continue
				}
				// A const promoted to a single-value enum type is enforced by that
				// type's own Validate(); an additional const rule on the field would
				// be redundant.
				if rules[i].RuleType == "const" {
					// namedTypeName, not a type assertion: an optional field of
					// such a type is pointer-wrapped so its zero value survives
					// the round trip, and the enum behind the pointer is still
					// the thing enforcing the const.
					if name := namedTypeName(ft); name != "" && g.isEnumType(name) {
						continue
					}
				}
				// The string rules pass the field to functions that take a
				// string; a field typed as a named string needs an explicit
				// conversion for that to compile.
				if ruleTakesStringValue(rules[i].RuleType) && g.isStringBackedNamedType(ft) {
					rules[i].StringConvert = true
				}
				// The content vocabulary is a dialect question and nothing
				// else: only draft 7 asserts it, and everywhere else the
				// keywords annotate and the check must not be written. The
				// shape question the format rule asks below does not arise
				// here -- ruleVacuousForType has already dropped the rule for
				// every field whose value is not a Go string.
				if rules[i].RuleType == "content" && !g.contentAssertsFor(propSchema) {
					continue
				}

				// A format is asserted one way against the Go type it maps to
				// and another against the raw string, and against several field
				// types not at all. Deciding here rather than where the rule was
				// built is what lets extractValidationRules admit a format whose
				// schema named no "type": the positions that cannot carry the
				// check drop it again, instead of emitting one that does not
				// compile. This is the same decision aliasFormatCheckable makes
				// for a $defs alias, through the same function.
				if rules[i].RuleType == "format" {
					// And dropped outright where the dialect makes format an
					// annotation, which is the other half of the same question:
					// whether the check is written at all comes before how.
					if !g.formatAssertsFor(propSchema) {
						continue
					}
					g.formatRulesForDialect(propSchema, rules[i:i+1])
					stringBacked, ok := formatRuleShape(ft, rules[i], rules[i].StringConvert)
					if !ok {
						continue
					}
					rules[i].StringBacked = stringBacked
				}
			}
			filtered = append(filtered, rules[i])
		}
		// Mark rules as optional when the property is not required.
		// JSON Schema says constraints only apply to present values.
		if !requiredSet[propName] {
			for j := range filtered {
				filtered[j].Optional = true
			}
		}
		// And skip the ones a present null satisfies vacuously. Optional is not
		// enough on its own: the key *is* present, so the guard above lets the
		// rule run, and it then reads the zero encoding/json left behind -- which
		// is how {"constrOnly":null} was rejected for a length of 0 against a
		// schema that permits null. A required property has no guard at all and
		// reaches the same reading, so this is not conditioned on Optional.
		if g.nullPresenceTracked(propSchema, fieldTypes[goFieldName]) {
			for j := range filtered {
				if ruleVacuousForNull(filtered[j].RuleType) {
					filtered[j].NullKey = propName
				}
			}
		}
		validations = append(validations, filtered...)

		// Constraints under `items` land on no field of their own, so they are
		// collected separately and checked element by element.
		if g.validationKeywordsEnabled() {
			if iv := g.buildItemValidation(name, goFieldName, propName, fieldTypes[goFieldName], propSchema); iv != nil {
				itemValidations = append(itemValidations, *iv)
			}
			// `contains` counts across the whole array rather than judging each
			// element, so it needs its own definition beside the per-element one.
			if fc := g.buildFieldContains(name, goFieldName, propName, fieldTypes[goFieldName], propSchema, !requiredSet[propName]); fc != nil {
				containsValidations = append(containsValidations, *fc)
			}
			// prefixItems names each position separately, so it is neither a
			// per-element check nor a count across the array.
			if ft := g.buildFieldTuple(goFieldName, propName, name, fieldTypes[goFieldName], propSchema); ft != nil {
				tupleValidations = append(tupleValidations, *ft)
			}
			// unevaluatedItems judges the positions the other keywords left over,
			// so it is decided from the whole array schema rather than per element.
			if fu := g.buildFieldUnevalItems(goFieldName, propName, fieldTypes[goFieldName], propSchema); fu != nil {
				unevalItemsValidations = append(unevalItemsValidations, *fu)
			}
		}
	}

	// An explicit null is a type error wherever the schema does not admit one,
	// and by the time any of the checks above run it is no longer there to see:
	// encoding/json leaves a nil pointer, a nil collection, or a scalar
	// untouched at its zero, all of which are what an *absent* property leaves
	// too. The verdict has to be taken from the document's own bytes, so it is
	// collected here and emitted into UnmarshalJSON. See NullCheckDef.
	//
	// Not gated on validationKeywordsEnabled, on the precedent of the struct's
	// own NeedsNullCheck a few lines below: refusing a null where the schema
	// states a type is a decoding question, and every validation mode has to
	// answer it the same way.
	for _, propName := range propNames {
		propSchema := s.Properties[propName]
		if propSchema == nil {
			continue
		}
		if ft, ok := fieldTypes[goFieldNames[propName]]; ok {
			if nc := g.buildNullCheck(propName, ft, propSchema); nc != nil {
				nullChecks = append(nullChecks, *nc)
			}
			continue
		}
		// A oneOf property is held by a sealed interface rather than by a
		// field, so there is no Go type to walk -- and there is nothing
		// anonymous below the union to walk into, since every branch is its own
		// named type. Only the property itself is judged.
		if g.schemaForbidsNull(propSchema) {
			nullChecks = append(nullChecks, NullCheckDef{JSONName: propName, Reject: true})
		}
	}
	// The other half of the same erasure, for the properties whose schema
	// *permits* a null. There is nothing to reject there, so the null is simply
	// lost: it leaves the same nil pointer or untouched zero an absent property
	// does, MarshalJSON writes it back as absent, and -- where the field is not
	// pointer-wrapped, which is every optional field under --omit-empty=false --
	// the optional rule then measures that zero against a bound the document
	// never supplied a value for. {"constrOnly":null} against
	// {"constrOnly":{"minLength":2}} was rejected for a length of 0. That is
	// issue #110.
	//
	// The repair is the third state the decoded value cannot hold: the keys that
	// arrived as null, recorded from the document's own bytes exactly as the
	// rejections above are. Validate skips the keywords a null satisfies
	// vacuously, and MarshalJSON writes the null back.
	var nullPresenceKeys []string
	for _, propName := range propNames {
		if g.nullPresenceTracked(s.Properties[propName], fieldTypes[goFieldNames[propName]]) {
			nullPresenceKeys = append(nullPresenceKeys, propName)
		}
	}
	if len(nullPresenceKeys) > 0 {
		needsUnmarshal = true
		needsMarshal = true
	}

	// The overflow map's values are governed by a schema-valued
	// additionalProperties, and they are decoded by hand in the routing loop --
	// json.Unmarshal of `null` into a string leaves "" and reports no error, so
	// this is the same erasure one level out.
	var overflowNullCheck *NullCheckDef
	if additionalProps != nil && s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		overflowNullCheck = g.buildNullCheck("", additionalProps.ValueType, s.AdditionalProperties.Schema)
	}
	if len(nullChecks) > 0 || overflowNullCheck != nil {
		needsUnmarshal = true
	}

	// So do the constraints a schema-valued additionalProperties puts on the
	// keys this object does not declare: they land in the overflow map, which
	// no field rule reaches.
	if g.validationKeywordsEnabled() && additionalProps != nil &&
		s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		if iv := g.buildOverflowValidation(name, additionalProps.ValueType, s.AdditionalProperties.Schema); iv != nil {
			itemValidations = append(itemValidations, *iv)
		}
	}

	// Enable custom marshal/unmarshal if any field has a JSON name that
	// cannot be represented in struct tags (control chars, quotes, etc.).
	for _, f := range fields {
		if f.ManualJSON {
			needsMarshal = true
			needsUnmarshal = true
			break
		}
	}

	// Collect patternProperties patterns.
	// These are regex patterns that match additional property keys which should
	// be preserved through round-trip even when additionalProperties is false.
	var patternProps []PatternPropertyDef
	for i, pattern := range sortedKeys(s.PatternProperties) {
		ppSchema := s.PatternProperties[pattern]
		ppDef := PatternPropertyDef{Pattern: pattern}
		// A sub-schema admitting nothing forbids every key the pattern matches.
		// `{"enum":[]}` says that as much as `false` does, and reached neither
		// this arm nor patternValueTypeName below -- so
		// {"patternProperties":{"^a":{"enum":[]}}} accepted {"a1":1}.
		if g.schemaForbidsEveryValue(ppSchema) {
			ppDef.IsForbidden = true
		} else if ppSchema.IsBooleanSchema() {
			// boolean true → no constraints
		} else if g.validationKeywordsEnabled() {
			// Both routes are prepared here and one is chosen later, by
			// resolvePatternPropertyTypes: whether the materialized type carries
			// a Validate is only knowable once every type def exists, and the
			// scalar rules are what the pattern falls back to when it does not.
			// The name is indexed over the sorted patterns, which is the only
			// stable identifier a regex position has -- a pattern is arbitrary
			// text and cannot be turned into a Go name.
			ppDef.TypeName = g.patternValueTypeName(ppSchema, fmt.Sprintf("%sPattern%d", name, i))
			ppDef.Validations = extractPatternPropertyValidationRules(ppSchema)
			ppDef.StrictInteger = g.requiresStrictIntegerToken(ppSchema)
		}
		patternProps = append(patternProps, ppDef)
	}
	if len(patternProps) > 0 {
		needsMarshal = true
		needsUnmarshal = true
	}

	// Add struct-level property count validations. These count present JSON keys
	// (tracked in _jsonKeys), so they require the custom unmarshaler.
	if g.validationKeywordsEnabled() && s.MaxProperties != nil {
		validations = append(validations, ValidationRule{
			RuleType: "maxProperties", Value: s.MaxProperties.Int(),
		})
		needsUnmarshal = true
	}
	if g.validationKeywordsEnabled() && s.MinProperties != nil {
		validations = append(validations, ValidationRule{
			RuleType: "minProperties", Value: s.MinProperties.Int(),
		})
		needsUnmarshal = true
	}

	// Extract dependent schema constraints.
	var depSchemas []DependentSchemaConstraint
	if g.validationKeywordsEnabled() {
		depSchemas = g.extractDependentSchemaConstraints(s)
	}
	if len(depSchemas) > 0 {
		needsUnmarshal = true // need to capture _jsonKeys
	}

	// Extract dependentRequired constraints.
	var depRequired []DependentRequiredDef
	if g.validationKeywordsEnabled() {
		for trigger, deps := range s.DependentRequired {
			if len(deps) > 0 {
				sorted := make([]string, len(deps))
				copy(sorted, deps)
				sort.Strings(sorted)
				depRequired = append(depRequired, DependentRequiredDef{
					TriggerKey: trigger,
					Required:   sorted,
				})
			}
		}
	}
	sort.Slice(depRequired, func(i, j int) bool {
		return depRequired[i].TriggerKey < depRequired[j].TriggerKey
	})
	if len(depRequired) > 0 {
		needsUnmarshal = true // need to capture _jsonKeys
	}

	// Enable custom unmarshal if there are optional field validations (to track key presence).
	for _, v := range validations {
		if v.Optional {
			needsUnmarshal = true
			break
		}
	}

	// Enable custom unmarshal if there are required fields (to track key presence).
	if len(requiredJSON) > 0 {
		needsUnmarshal = true
	}

	needsNullCheck := !schemaAllowsNull(s)
	if needsNullCheck {
		needsUnmarshal = true
	}

	// When the caller flags acceptNonObject and the schema has no explicit
	// "type":"object", non-object JSON data is silently accepted.
	acceptNonObj := acceptNonObject && !schemaHasExplicitType(s, "object")
	if acceptNonObj {
		needsUnmarshal = true
		needsMarshal = true // must preserve raw non-object data for roundtrip
	}

	// Extract non-object validation rules from the schema itself (e.g.,
	// minimum/maximum on a schema that has both properties and numeric constraints).
	// These are checked against _rawNonObject when the data is not an object.
	var nonObjRules []ValidationRule
	if acceptNonObj && g.validationKeywordsEnabled() {
		nonObjRules = extractNonObjectValidationRules(s)
	}

	// Build unevaluatedProperties constraint if present.
	var unevalProps *UnevaluatedPropertiesDef
	if s.UnevaluatedProperties != nil {
		unevalProps = g.buildUnevaluatedPropertiesDef(s)
	}

	// An allOf/anyOf branch stating additionalProperties or unevaluatedProperties
	// keeps its own view of which keys it leaves unaccounted for; neither keyword
	// can be folded into this struct's overflow map. See
	// collectBranchOverflowChecks and collectRuntimeBranchChecks.
	branchChecks := g.collectBranchOverflowChecks(s, name)
	runtimeBranchChecks := g.collectRuntimeBranchChecks(s)

	// Where an applicator is evaluated exactly, the flattened approximation of
	// that same applicator is dropped rather than run beside it, which is what
	// the two extractors take runtimeBranchChecks for.
	//
	// The two do not merely duplicate work: the approximation decides whether a
	// branch matches from its required keys, its consts and its declared types,
	// and a branch stating unevaluatedProperties can fail on a key none of those
	// mention. It then counts a branch that does not hold, which for `oneOf` is
	// the difference between one match and two -- so
	// {"oneOf":[{"properties":{"b":{}},"required":["b"],
	//            "unevaluatedProperties":false},
	//           {"properties":{"a":{}},"required":["a"]}]}
	// rejected {"a":1,"b":1}, which satisfies the second branch alone. That is
	// the same false rejection as #111 reached by the other keyword, and the
	// exact check is what settles it.
	//
	// Which slice, not which struct: an allOf branch's applicator lands in this
	// same struct, and dropping its approximation because the *schema's own*
	// applicator was taken over would leave it checked by nothing (issue #135).
	objectOneOfs := g.extractObjectOneOfDefs(s, runtimeBranchChecks)
	if len(objectOneOfs) > 0 {
		needsUnmarshal = true
	}

	// anyOf that sits alongside direct properties is flattened into this struct;
	// attach object-level anyOf checks so "at least one branch must match" is
	// enforced. The properties-less anyOf path is handled in generateAnyOfDef.
	objectAnyOfs := g.extractObjectAnyOfDefs(s, runtimeBranchChecks)
	if len(objectAnyOfs) > 0 {
		needsUnmarshal = true
	}

	// An if/then/else beside the object's properties applies to the object as a
	// whole. Its branches are not folded into any one field's rules -- they only
	// bind when the condition holds -- so they are checked here against the raw
	// JSON the unmarshaler kept.
	objectConditionals := g.extractObjectConditionalDefs(s)
	if len(objectConditionals) > 0 {
		needsUnmarshal = true
	}

	if len(branchChecks) > 0 || len(runtimeBranchChecks) > 0 {
		// The checks read the raw JSON, which only the custom unmarshaler keeps,
		// and every key that is not a declared field must survive the round trip
		// for the marshaler to put it back.
		needsUnmarshal = true
		if additionalProps == nil {
			additionalProps = &AdditionalPropertiesDef{
				ValueType: &PrimitiveType{Name: "json.RawMessage"},
			}
			needsMarshal = true
		}
	}

	// An enum a branch contributed is about the whole document, not any one
	// field, so it cannot become a field rule and the merged struct has to carry
	// it. generateTypeDef routes a schema stating its own enum to generateEnumDef
	// long before this, so the only way one reaches a struct is the allOf merge.
	//
	// A `const` is read through the same promotion the rest of the generator
	// uses: it is a one-member enum, and the merge carries it in the Const slot
	// rather than the Enum one, so asking only about Enum would enforce
	// {"allOf":[{"enum":[{"k":1}]}]} and drop {"allOf":[{"const":{"k":1}}]}.
	var objectEnum []string
	if g.validationKeywordsEnabled() {
		if promoted := promoteConstToEnum(s); len(promoted.Enum) > 0 {
			objectEnum = canonicalJSONValues(promoted.Enum)
		}
	}
	if len(objectEnum) > 0 {
		needsUnmarshal = true
	}

	// Extract propertyNames constraint.
	var propertyNamesDef *PropertyNamesDef
	if s.PropertyNames != nil && g.validationKeywordsEnabled() {
		propertyNamesDef = g.extractPropertyNamesDef(s.PropertyNames)
		if propertyNamesDef != nil {
			needsUnmarshal = true // need _jsonKeys for validation
		}
	}

	// The behavioural half of readOnly/writeOnly, which exists only when the
	// caller asked for it. Both lists stay nil otherwise, so no template arm
	// fires and no generated byte moves.
	var readOnlyKeys, writeOnlyKeys []string
	if g.config.StrictReadWrite {
		for _, f := range fields {
			if f.Annotations.ReadOnly {
				readOnlyKeys = append(readOnlyKeys, f.JSONName)
			}
			if f.Annotations.WriteOnly {
				writeOnlyKeys = append(writeOnlyKeys, f.JSONName)
			}
		}
		// A struct that would otherwise have taken the default decoder or
		// encoder needs its own, or the check has nowhere to be.
		if len(readOnlyKeys) > 0 {
			needsUnmarshal = true
		}
		if len(writeOnlyKeys) > 0 {
			needsMarshal = true
		}
	}

	structDef := &StructDef{
		Name:                   name,
		Description:            s.Description,
		Annotations:            annotationsOf(s),
		ReadOnlyKeys:           readOnlyKeys,
		WriteOnlyKeys:          writeOnlyKeys,
		Fields:                 fields,
		OneOfs:                 oneOfs,
		AdditionalProperties:   additionalProps,
		PatternProperties:      patternProps,
		DependentSchemas:       depSchemas,
		DependentRequired:      depRequired,
		PropertyNames:          propertyNamesDef,
		Validations:            validations,
		ItemValidations:        itemValidations,
		ContainsValidations:    containsValidations,
		TupleValidations:       tupleValidations,
		UnevalItemsValidations: unevalItemsValidations,
		NonObjectValidations:   nonObjRules,
		UnevaluatedProperties:  unevalProps,
		BranchOverflowChecks:   branchChecks,
		RuntimeBranchChecks:    runtimeBranchChecks,
		ObjectEnum:             objectEnum,
		ObjectOneOfs:           objectOneOfs,
		ObjectAnyOfs:           objectAnyOfs,
		ObjectConditionals:     objectConditionals,
		RequiredJSON:           requiredJSON,
		NullChecks:             nullChecks,
		OverflowNullCheck:      overflowNullCheck,
		NullPresenceKeys:       nullPresenceKeys,
		NeedsMarshal:           needsMarshal,
		NeedsUnmarshal:         needsUnmarshal,
		NeedsNullCheck:         needsNullCheck,
		AcceptNonObject:        acceptNonObj,
	}
	g.output.TypeDefs = append(g.output.TypeDefs, structDef)
	return nil
}

// generateAllOfDef merges all allOf sub-schemas into a single struct.
// When no sub-schema contributes properties, it generates an alias type
// instead of an empty struct, using the inferred type from constraints.
func (g *Generator) generateAllOfDef(name string, s *schema.Schema) error {
	// This function resolves its own merged schema (resolveType(merged, name)),
	// and the merge keeps the allOf on it so collectEvaluatedProperties can walk
	// the branches later. So the schema handed back to resolveType still looks
	// like an allOf that needs a name -- this one -- and resolveType's
	// delegation arm would re-enter here for it. Both frames then reach an emit
	// and the type is declared twice, which does not compile.
	//
	// The arm already has the mark for exactly this ("guard against infinite
	// recursion"); it was only ever set by resolveType itself, which is why the
	// hazard stayed latent while the arm claimed object-shaped allOfs alone --
	// those re-entered generateStructDef, which has a guard of its own. Setting
	// it here covers the scalar shapes too. Saved and restored rather than
	// deleted: a caller further up may hold the same mark.
	wasGenerating := g.generating[name]
	g.generating[name] = true
	defer func() {
		if wasGenerating {
			g.generating[name] = true
		} else {
			delete(g.generating, name)
		}
	}()

	// If any allOf sub-schema is boolean false, nothing can satisfy all constraints.
	// Generate a forbidden type (NotSchemaDef).
	if g.allOfContainsFalseSchema(s.AllOf) {
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, &NotSchemaDef{
			Name:        name,
			Description: s.Description,
			IsForbidden: true,
		})
		return nil
	}

	// Merge all properties and required fields from allOf sub-schemas.
	merged := &schema.Schema{
		Title:       s.Title,
		Description: s.Description,
		Properties:  make(map[string]*schema.Schema),
	}

	// Copy any properties from the parent schema itself.
	for k, v := range s.Properties {
		merged.Properties[k] = v
	}
	if g.validationKeywordsEnabled() {
		merged.Required = append(merged.Required, s.Required...)
		// The parent's own property-count bounds bind the same object the allOf
		// branches do. Seeding them before the merge lets mergeConstraints keep
		// whichever bound is tighter; propagating them afterwards would instead
		// let a branch's bound win by having got there first.
		merged.MinProperties = s.MinProperties
		merged.MaxProperties = s.MaxProperties
		// Same for propertyNames, now that a branch's is merged too: seeding the
		// parent's makes it the left-hand side of mergePropertyNames, so where
		// only one pattern can be kept it is the parent's -- which is what #68
		// established when a branch had none to offer.
		merged.PropertyNames = s.PropertyNames
		// And the same for enum and const, which say which values are legal at
		// all. mergeConstraints reads a branch's onto a target that has none, so
		// without this seeding a parent's list was dropped outright -- and from
		// 2019-09 on {"$ref":T,"const":c} is rewritten into exactly this shape,
		// with the reference as the only branch and the const on the parent, so
		// the const vanished on the way in (issue #153).
		//
		// Seeded before the merge rather than propagated after it, for the reason
		// the bounds above are: mergeConstraints keeps the first list it is given,
		// so seeding is what makes the parent's the one that survives where a
		// branch states one too. Both bind, one slot holds them, and keeping the
		// parent's under-enforces rather than refusing values the schema allows --
		// the direction mergeConstraints already documents for this pair.
		merged.Enum, merged.Const, merged.ConstIsNull = s.Enum, s.Const, s.ConstIsNull
	}

	// Merge each allOf sub-schema, recursively flattening nested allOf chains.
	g.mergeAllOfInto(merged, s.AllOf)

	// Propagate keywords from the parent schema that aren't merged by allOf logic.
	if s.AdditionalProperties != nil && merged.AdditionalProperties == nil {
		merged.AdditionalProperties = s.AdditionalProperties
	}
	if s.UnevaluatedProperties != nil && merged.UnevaluatedProperties == nil {
		merged.UnevaluatedProperties = s.UnevaluatedProperties
	}
	for k, v := range s.PatternProperties {
		if merged.PatternProperties == nil {
			merged.PatternProperties = make(map[string]*schema.Schema)
		}
		if _, exists := merged.PatternProperties[k]; !exists {
			merged.PatternProperties[k] = v
		}
	}
	// A branch's own additionalProperties, in the one case where the parent's
	// overflow map holds exactly the keys that branch governs.
	//
	// additionalProperties is scoped to the schema stating it, so a branch's
	// keyword speaks about every key of the instance -- including any the parent
	// or a sibling branch declares. Hanging it off the parent's overflow map,
	// which holds only the keys nothing declares, would check it on a smaller
	// set than the schema demands; that is why the merge drops it, and why
	// issue #96 records the general case as needing a per-branch overflow notion
	// rather than this.
	//
	// When nothing anywhere in the merge names a property or a pattern, the gap
	// closes: every key is additional in the branch's scope and every key lands
	// in the overflow map in the parent's, so the two sets are the same one and
	// the keyword is enforced exactly. Only a lone branch is taken -- two
	// branches each stating additionalProperties would have to be satisfied
	// together, which one overflow map cannot say.
	if merged.AdditionalProperties == nil && len(merged.Properties) == 0 && len(merged.PatternProperties) == 0 {
		merged.AdditionalProperties = g.soleBranchAdditionalProperties(s.AllOf, make(map[*schema.Schema]bool))
	}
	// Preserve allOf on the merged schema so that collectEvaluatedProperties
	// can walk the original allOf branches to find evaluated property names
	// and patterns (since mergeAllOfInto only copies properties/required/constraints,
	// not patternProperties or additionalProperties from sub-schemas).
	if len(s.AllOf) > 0 && len(merged.AllOf) == 0 {
		merged.AllOf = s.AllOf
	}
	// Propagate $ref from parent for unevaluatedProperties evaluation.
	if s.Ref != "" && merged.Ref == "" {
		merged.Ref = s.Ref
		// Also copy BaseURI and DocumentRoot so ref resolution works on merged schema.
		if merged.BaseURI == nil && s.BaseURI != nil {
			merged.BaseURI = s.BaseURI
		}
		if merged.DocumentRoot == nil && s.DocumentRoot != nil {
			merged.DocumentRoot = s.DocumentRoot
		}
	}
	// Propagate anyOf/oneOf/if-then-else from parent for unevaluatedProperties evaluation.
	if len(s.AnyOf) > 0 && len(merged.AnyOf) == 0 {
		merged.AnyOf = s.AnyOf
	}
	if len(s.OneOf) > 0 && len(merged.OneOf) == 0 {
		merged.OneOf = s.OneOf
	}
	if s.If != nil && merged.If == nil {
		merged.If = s.If
	}
	if s.Then != nil && merged.Then == nil {
		merged.Then = s.Then
	}
	if s.Else != nil && merged.Else == nil {
		merged.Else = s.Else
	}
	if g.validationKeywordsEnabled() && len(s.DependentSchemas) > 0 && len(merged.DependentSchemas) == 0 {
		merged.DependentSchemas = s.DependentSchemas
	}
	// dependentRequired constrains the object the parent declares, and an allOf
	// beside it says nothing about it. mergeAllOfBranches only reads it off a
	// branch when the target has none, so without this the keyword vanishes for
	// no reason other than the allOf being there. (propertyNames is seeded from
	// the parent before the merge instead, so that mergePropertyNames sees both
	// sides at once.)
	if g.validationKeywordsEnabled() && len(s.DependentRequired) > 0 {
		// A branch may already have contributed a map of its own. Both bind, so
		// the two are unioned into a fresh map -- mutating merged's would write
		// through to the sub-schema mergeAllOfBranches took it from.
		combined := make(map[string][]string, len(merged.DependentRequired)+len(s.DependentRequired))
		for trigger, deps := range merged.DependentRequired {
			combined[trigger] = deps
		}
		for trigger, deps := range s.DependentRequired {
			combined[trigger] = mergeStringSets(combined[trigger], deps)
		}
		merged.DependentRequired = combined
	}
	// Propagate array-structural keywords from parent schema.
	// Per JSON Schema spec, items/additionalItems scoping is per-schema (they don't
	// cross into applicator sub-schemas like allOf). So the parent's items applies
	// independently and must be preserved on the merged schema for type inference
	// and validation extraction.
	if s.Items != nil && merged.Items == nil {
		merged.Items = s.Items
	}
	if len(s.PrefixItems) > 0 && len(merged.PrefixItems) == 0 {
		merged.PrefixItems = s.PrefixItems
	}
	if s.Contains != nil && merged.Contains == nil {
		merged.Contains = s.Contains
	}
	if s.MinContains != nil && merged.MinContains == nil {
		merged.MinContains = s.MinContains
	}
	if s.MaxContains != nil && merged.MaxContains == nil {
		merged.MaxContains = s.MaxContains
	}
	if s.AdditionalItems != nil && merged.AdditionalItems == nil {
		merged.AdditionalItems = s.AdditionalItems
	}
	if s.UnevaluatedItems != nil && merged.UnevaluatedItems == nil {
		merged.UnevaluatedItems = s.UnevaluatedItems
	}

	// If no sub-schema contributed a named key, don't generate an empty struct.
	// Instead, infer the type from constraints and generate an alias.
	//
	// patternProperties counts as a named key here, for the reason namesObjectKeys
	// gives: a merge that produced patterns has an object shape only a struct can
	// carry, and the alias arms below answer `any` for it -- so
	// {"allOf":[{"type":"object","patternProperties":{...}}]} validated nothing,
	// while the identical branch written without the allOf around it validated
	// fully. generateStructDef builds the pattern bucket whether or not any
	// property was declared beside it.
	if len(merged.Properties) == 0 && len(merged.PatternProperties) == 0 {
		// The merge only ever takes a type off a *branch*, so a type the parent
		// declared beside its allOf has not reached `merged` at all. It is still
		// a declared type and it still binds, and every arm below reads its
		// answer off `merged` -- resolveType for the Go type, schemaAllowsNull
		// for the decoder's null check, extractAliasValidationRules for the
		// rules, primarySchemaType for which arm runs. Two of those arms had
		// grown a local patch that re-read s.Type for their own branch decision
		// while still handing `merged` to resolveType, which is how
		// {"type":"string","allOf":[{}]} came out `type Root any`: the arm knew
		// it was a string and the type it asked for did not.
		//
		// Putting the type on the merged schema once is what makes the answer
		// consistent, and it is what carries a declared type through the
		// synthesized allOf a $ref-beside-a-type is rewritten into (#118) --
		// without it the merge produced a wrapper that accepted every instance.
		//
		// Only inside this block: a merge that produced properties goes to
		// generateStructDef, which has its own reading of the parent and is not
		// part of the defect.
		if len(merged.Type) == 0 && len(s.Type) > 0 {
			merged.Type = s.Type
			merged.TypeSchemas = s.TypeSchemas
		}
		// The schema the array arms below read their per-position checks from.
		// Normally the merged one; when nothing in it describes the array's
		// positions, a lone branch that does. See soleBranchArrayKeywords.
		//
		// It is the branch *node*, not a copy of its keywords onto `merged`.
		// Which keywords a schema even has depends on the dialect it is read
		// under -- 2019-09 has no prefixItems and must ignore one written there
		// -- and a node carries its dialect through its own $id and $schema,
		// which draftForSchema reads. A copy onto the synthesized `merged` would
		// be read under the *parent's* dialect instead, and
		// optional/cross-draft says that is wrong in both directions: a 2019-09
		// document referencing a 2020-12 one must honour its prefixItems, and a
		// 2020-12 document referencing a 2019-09 one must ignore it.
		arraySchema := merged
		if !statesArrayStructure(merged) {
			if src := g.soleBranchArrayKeywords(s.AllOf, make(map[*schema.Schema]bool)); src != nil {
				arraySchema = src
				if len(merged.Type) == 0 {
					merged.Type = src.Type
				}
			}
		}
		// An enum a branch contributed is the whole of what that branch asserts,
		// and it is the one keyword nothing downstream can infer a type from. So
		// {"allOf":[{"$ref":"#/$defs/SomeEnum"}]} reached the `any` fall-through
		// at the end of this block, and `type X any` carries no Validate: every
		// value outside the enum was accepted. This is the same dispatch
		// generateTypeDef makes before it looks at anything else, for the same
		// reason -- the enum decides the type as well as the constraint.
		if g.validationKeywordsEnabled() {
			merged = promoteConstToEnum(merged)
			if len(merged.Enum) > 0 {
				// An enum type checks membership and nothing else, so a merge
				// stating anything further has to be carried somewhere that can
				// hold both. That is what {"$ref":T,"const":c} is rewritten into
				// from 2019-09 on, and taking the enum arm for it dropped T
				// outright: with T = {"type":"string","minLength":5} the type
				// admitted "abc", which T forbids (issue #153).
				//
				// The evaluator is asked for the *original* schema rather than the
				// merge, so it reads the reference itself rather than the subset
				// mergeConstraints was able to lift onto `merged`. When it declines
				// -- a keyword it does not model, a cycle it cannot inline -- the
				// enum arm below is still the answer, which is what it has always
				// been here.
				if !enumTypeCarriesSchema(merged) {
					if def := g.rawWrapperDef(name, s); def != nil {
						g.generated[name] = true
						g.output.TypeDefs = append(g.output.TypeDefs, def)
						return nil
					}
				}
				g.generated[name] = true
				return g.generateEnumDef(name, merged)
			}
		}
		// Check for type-only merged result (null-only or multi-type like ["integer","string"]).
		// These don't map to a single Go type, so use TypeOnlySchemaDef.
		// We check the merged type directly rather than calling extractTypeOnlySchemaDef,
		// because the merged schema may have allOf preserved for other purposes.
		if len(merged.Type) > 0 {
			pt := primarySchemaType(merged)
			if pt == "null" || (pt == "" && len(merged.Type) > 1) {
				g.generated[name] = true
				g.output.TypeDefs = append(g.output.TypeDefs, &TypeOnlySchemaDef{
					Name:         name,
					Description:  s.Description,
					AllowedTypes: merged.Type,
					TypeBranches: g.extractTypeSchemaBranches(merged.TypeSchemas, name),
				})
				return nil
			}
		}

		// The two format shapes, on the schema the merge produced. A branch is
		// what carries the format here -- {"allOf":[{"format":"ipv4"}]} -- and
		// mergeConstraints has already lifted it onto `merged`, so the same
		// answer the schema would get without the allOf around it is available;
		// without this the arms below reached `type X any` or `type X *string`
		// and the format was enforced nowhere, which is the position half of
		// issues #104 and #106.
		//
		// Asked of a copy with "allOf" cleared: the merge preserves it for
		// unevaluatedProperties, and both predicates refuse an unmerged
		// applicator. The other applicators are left in place deliberately --
		// an anyOf or a oneOf beside the allOf is carried by the alias arms
		// below through extractAnyOfVariantRules, and a wrapper here would drop
		// it.
		mergedNoAllOf := *merged
		mergedNoAllOf.AllOf = nil
		if g.nullableFormatUnion(&mergedNoAllOf) {
			if _, ok := g.typeUnionWrapper(&mergedNoAllOf, name); ok {
				return nil
			}
		}
		if fDef := g.stringAnnotationOnlyDef(name, &mergedNoAllOf); fDef != nil {
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, fDef)
			return nil
		}

		primaryType := primarySchemaType(merged)
		// Whether the type was *declared* or *inferred from a bound* decides the
		// shape, exactly as it does in generateTypeDef: a declared type may be
		// enforced by the Go type itself, an inferred one may not. A keyword
		// about strings is satisfied vacuously by every instance that is not a
		// string, so {"allOf":[{"minLength":3}]} accepts 5, [1,2] and true --
		// and this arm, which recorded only the answer and not where it came
		// from, made every one of them `type X string` and refused all three.
		// The array arm below has always used the wrapper; the scalar arm did
		// not, so the two disagreed about the same question.
		inferredFromConstraints := false
		if primaryType == "" {
			primaryType = g.inferTypeFromConstraints(merged)
			if primaryType != "" {
				inferredFromConstraints = true
			}
		}
		// The merge infers "array" from a branch's array keywords so that the
		// merged schema can be typed at all (see mergeAllOfBranches). That is
		// the same guess inferTypeFromConstraints makes, and it has to be read
		// as one: nothing here *declared* an array, so the arm below refused
		// every instance that is not one, and {"allOf":[{"items":{...}}]}
		// rejected the string, the object, the number and the null it permits
		// -- while the identical schema without the allOf around it accepted
		// all four. Same rule as the scalar case above, one keyword along.
		// len(s.Type) == 0 is the other half: the parent's own declaration is
		// copied onto `merged` further up, and only when the merge left it
		// empty -- which the guess does not, so a parent stating "array" beside
		// a branch stating `contains` still reaches here carrying the mark.
		if primaryType == "array" && !inferredFromConstraints &&
			len(s.Type) == 0 && g.arrayTypeInferredFromBranch[merged] {
			inferredFromConstraints = true
		}
		if primaryType == "array" && !inferredFromConstraints {
			// A *declared* array type is carried by the Go type itself, exactly
			// as it is when there is no allOf: `type X []any` refuses a string
			// in the decoder. The wrapper the inferred arm below emits cannot,
			// because its whole contract is to accept a non-matching instance
			// silently -- right for a type guessed from a bound, wrong for one
			// the schema states. This arm did not exist, so every declared array
			// beside an allOf got the wrapper: {"type":"array","allOf":[{}]}
			// accepted "foo", and so did the synthesized allOf that
			// {"type":"array","$ref":...} is rewritten into (#118), which is why
			// putting the type on `merged` was not on its own enough to fix it.
			//
			// The body is generateTypeDef's own non-inferred array arm, reading
			// `arraySchema` for the array's positions, `merged` for the bounds
			// and the null rule, and `s` for the anyOf/oneOf siblings of the
			// allOf -- the same split the inferred arm makes.
			g.generated[name] = true
			goType := g.resolveType(merged, name)
			// The merge leaves a branch's array keywords where they are on
			// purpose, so when a branch is what supplies `items` the slice
			// resolved from `merged` holds `any` -- and the per-element descent
			// below, alone among the array arms here, then had nothing to walk
			// and {"allOf":[{"items":{"type":"object","required":["a"]}},
			// {"type":"array"}]} accepted [{}]. The branch's own answer is
			// borrowed for the element, and only for the element: it may
			// replace an `any` with something narrower and may do nothing else.
			//
			// Narrow because the branch is not the schema this type is being
			// built from. It is read under its own dialect and states only what
			// it states, so a branch whose keywords that dialect ignores
			// resolves to no array at all -- which, taken wholesale, turned a
			// {"type":"array","$ref":<2019-09 doc>} into `type X any` and lost
			// the array check the parent had declared.
			if _, holdsAny := anyElementSliceField(goType); arraySchema != merged && holdsAny {
				alt := g.resolveType(arraySchema, name)
				if _, altHoldsAny := anyElementSliceField(alt); !altHoldsAny {
					if _, isSlice := alt.(*ArrayType); isSlice {
						goType = alt
					}
				}
			}
			var rules []ValidationRule
			var anyOfVariants [][]ValidationRule
			var oneOfVariants [][]ValidationRule
			var tupleItems []TupleItemDef
			var tupleTail *TupleItemDef
			var containsDef *ContainsDef
			var minContains, maxContains *int
			var unevalItems *UnevaluatedItemsDef
			var itemValidations []ItemValidationDef
			if g.validationKeywordsEnabled() {
				rules = g.aliasValidationRules(merged, goType)
				anyOfVariants = extractAnyOfVariantRules(s, goType)
				oneOfVariants = extractOneOfVariantRules(s, goType)
				tupleItems = g.buildTupleItemDefs(arraySchema, name)
				tupleTail = g.buildTupleTailDef(arraySchema, name)
				containsDef, minContains, maxContains = g.extractContainsDef(arraySchema, name)
				unevalItems = g.buildUnevaluatedItemsDef(merged)
				// The alias *is* the slice, so the per-element checks hang off
				// the receiver rather than off a field.
				if iv := g.buildItemValidation(name, "", "", goType, arraySchema); iv != nil {
					itemValidations = append(itemValidations, *iv)
				}
			}
			g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
				Name:             name,
				Underlying:       goType,
				Description:      s.Description,
				Validations:      rules,
				AnyOfVariants:    anyOfVariants,
				OneOfVariants:    oneOfVariants,
				TupleItems:       tupleItems,
				TupleTail:        tupleTail,
				ItemValidations:  itemValidations,
				Contains:         containsDef,
				MinContains:      minContains,
				MaxContains:      maxContains,
				UnevaluatedItems: unevalItems,
				ValidateAs:       g.firstAllOfArrayAliasValidateAs(s.AllOf),
				NeedsNullCheck:   !schemaAllowsNull(merged),
				NullCheck:        g.aliasNullCheck(goType, merged),
			})
			return nil
		}
		if primaryType == "array" {
			// Array type — extract item-level constraints and generate InferredAliasDef
			// so that per-element validation works (items, prefixItems, contains, etc.).
			goType := g.resolveType(merged, name)
			validateAs := g.firstAllOfArrayAliasValidateAs(s.AllOf)
			var rules []ValidationRule
			var anyOfVariants [][]ValidationRule
			var oneOfVariants [][]ValidationRule
			if g.validationKeywordsEnabled() {
				rules = g.aliasValidationRules(merged, goType)
				anyOfVariants = extractAnyOfVariantRules(s, goType)
				oneOfVariants = extractOneOfVariantRules(s, goType)
			}
			g.generated[name] = true
			// A JSON null is refused by a *declared* array and permitted by an
			// inferred one, so the question has to be put to the schema without
			// the merge's guess in it. Nothing here said "array"; a branch's
			// items did, and `items` says nothing about a null.
			nullSchema := merged
			if g.arrayTypeInferredFromBranch[merged] {
				withoutGuess := *merged
				withoutGuess.Type = nil
				nullSchema = &withoutGuess
			}
			// Only when the merge itself describes the positions does the Go
			// type resolveType built from it speak about this array's element.
			// A lone branch supplying them is a different node, and `merged`
			// resolved to []any.
			var elemGoType GoType
			if arraySchema == merged {
				elemGoType, _ = containerElem(goType)
			}
			itemsFalse, itemsType, itemsTypeName, itemsChecks, itemsNested, tupleItems, addlItemsFalse, addlItemsType, addlItemsTypeName := g.extractInferredItemConstraints(arraySchema, name, elemGoType)
			containsDef, minContains, maxContains := g.extractContainsDef(arraySchema, name)
			unevalItems := g.buildUnevaluatedItemsDef(merged)
			if !g.validationKeywordsEnabled() {
				itemsFalse = false
				itemsType = ""
				itemsTypeName = ""
				itemsChecks = nil
				itemsNested = nil
				tupleItems = nil
				addlItemsFalse = false
				addlItemsType = ""
				addlItemsTypeName = ""
				containsDef = nil
				minContains = nil
				maxContains = nil
				unevalItems = nil
			}
			inferredGoType := goType
			if itemsFalse || itemsType != "" || itemsTypeName != "" ||
				len(itemsChecks) > 0 || itemsNested != nil ||
				len(tupleItems) > 0 || addlItemsFalse || addlItemsType != "" ||
				addlItemsTypeName != "" || containsDef != nil || unevalItems != nil {
				inferredGoType = &ArrayType{ItemType: &PrimitiveType{Name: "any"}}
			}
			g.output.TypeDefs = append(g.output.TypeDefs, &InferredAliasDef{
				Name:                    name,
				Description:             s.Description,
				InferredGoType:          inferredGoType,
				InferredJSONType:        primaryType,
				Validations:             rules,
				AnyOfVariants:           anyOfVariants,
				OneOfVariants:           oneOfVariants,
				ValidateAs:              validateAs,
				NeedsNullCheck:          !schemaAllowsNull(nullSchema),
				ItemsFalse:              itemsFalse,
				ItemsType:               itemsType,
				ItemsTypeName:           itemsTypeName,
				ItemsChecks:             itemsChecks,
				ItemsNested:             itemsNested,
				TupleItems:              tupleItems,
				AdditionalItemsFalse:    addlItemsFalse,
				AdditionalItemsType:     addlItemsType,
				AdditionalItemsTypeName: addlItemsTypeName,
				Contains:                containsDef,
				MinContains:             minContains,
				MaxContains:             maxContains,
				UnevaluatedItems:        unevalItems,
			})
			return nil
		}
		if primaryType != "" && primaryType != "object" {
			goType := g.resolveType(merged, name)
			var rules []ValidationRule
			// Carry through anyOf/oneOf variant rules from the parent schema,
			// since these are siblings of allOf and must also be validated.
			var anyOfVariants [][]ValidationRule
			var oneOfVariants [][]ValidationRule
			if g.validationKeywordsEnabled() {
				rules = g.aliasValidationRules(merged, goType)
				anyOfVariants = extractAnyOfVariantRules(s, goType)
				oneOfVariants = extractOneOfVariantRules(s, goType)
			}
			g.generated[name] = true
			if inferredFromConstraints {
				// A bound is all the merge had to go on, so the type is a guess
				// about what the schema is *about*, not a statement that the
				// instance must be one. The wrapper keeps the guess -- the
				// constraint is checked when the value does turn out to be a
				// string -- without making the Go type refuse everything else.
				g.output.TypeDefs = append(g.output.TypeDefs, &InferredAliasDef{
					Name:             name,
					Description:      s.Description,
					InferredGoType:   goType,
					InferredJSONType: primaryType,
					Validations:      rules,
					AnyOfVariants:    anyOfVariants,
					OneOfVariants:    oneOfVariants,
					NeedsNullCheck:   !schemaAllowsNull(merged),
				})
				return nil
			}
			if g.config.BigIntSupport && primaryType == "integer" {
				// The same wrapper an integer without an allOf gets. Without this
				// arm, --big-int silently stopped applying the moment a
				// definition was written {"allOf":[{"$ref": someInteger}]}: the
				// alias was a plain int64, and the arbitrary precision the flag
				// exists to provide was gone with no diagnostic.
				g.output.TypeDefs = append(g.output.TypeDefs, &BigIntAliasDef{
					Name:           name,
					Description:    s.Description,
					Validations:    rules,
					AnyOfVariants:  anyOfVariants,
					OneOfVariants:  oneOfVariants,
					NeedsNullCheck: !schemaAllowsNull(merged),
					AllowsNull:     schemaAllowsNull(merged),
					StrictInteger:  g.requiresStrictIntegerToken(merged),
				})
				return nil
			}
			g.output.TypeDefs = append(g.output.TypeDefs, &AliasDef{
				Name:           name,
				Underlying:     goType,
				Description:    s.Description,
				Validations:    rules,
				AnyOfVariants:  anyOfVariants,
				OneOfVariants:  oneOfVariants,
				StrictInteger:  primaryType == "integer" && g.requiresStrictIntegerToken(merged),
				NeedsNullCheck: !schemaAllowsNull(merged),
			})
			return nil
		}
		// An object the merge left with no properties still has object keywords to
		// answer for -- propertyNames, the property-count bounds, required,
		// dependentRequired, dependentSchemas. `type X any` carries no Validate,
		// so every one of them would be dropped here and the schema would accept
		// anything. Give it the same property-less struct an object without an
		// allOf gets.
		//
		// Gated on there being something to enforce: a merged schema that says
		// nothing about the object keeps the `any` alias, which is a smaller and
		// more convenient type for callers than an empty struct.
		if primaryType == "object" && g.propertylessObjectHasChecks(merged) {
			// merged already carries the parent's declared type (seeded at the
			// top of this block), which is what decides whether a non-object
			// instance is silently accepted.
			g.generatePropertylessObjectDef(name, merged)
			return nil
		}
		// No type inferrable → alias to `any` (permissive fallback), unless the
		// schema's applicators still constrain the value, in which case wrap the
		// raw JSON so a Validate() can be attached.
		if def := g.rawWrapperDef(name, s); def != nil {
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, def)
			return nil
		}
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, g.unenforcedAliasDef(name, s))
		return nil
	}

	// allOf is type-agnostic: don't silently accept non-object data.
	if err := g.generateStructDef(name, merged, false); err != nil {
		return err
	}

	// Per JSON Schema spec, additionalProperties only considers properties defined
	// directly on the same schema — NOT properties from allOf/anyOf sub-schemas.
	// When the parent has additionalProperties and allOf contributed extra properties,
	// record the parent's own property names so the unmarshal template routes
	// allOf-contributed properties into the additionalProperties overflow map.
	if s.AdditionalProperties != nil && len(s.Properties) < len(merged.Properties) {
		ownNames := make([]string, 0, len(s.Properties))
		for k := range s.Properties {
			ownNames = append(ownNames, k)
		}
		sort.Strings(ownNames)
		// Find the StructDef we just appended and set OwnPropertyNames.
		if last := g.output.TypeDefs[len(g.output.TypeDefs)-1]; last.TypeName() == name {
			if sd, ok := last.(*StructDef); ok {
				sd.OwnPropertyNames = ownNames
			}
		}
	}
	return nil
}

// soleBranchAdditionalProperties returns the one additionalProperties stated
// anywhere in an allOf, or nil when none is or more than one is.
//
// It follows the same routes into a branch that mergeAllOfBranches does -- a
// $ref chain, a nested allOf -- so it sees the branches the merge saw, and
// onPath is shared with nothing: each call starts a fresh set, and a node
// already on the path answers nothing so a self-referential allOf terminates.
//
// "More than one" answers nil rather than picking: two branches each stating
// the keyword both govern every key, and satisfying both is an allOf of the two
// sub-schemas, which the single overflow value type this feeds cannot express.
// A boolean additionalProperties is returned like any other -- the caller's own
// arms decide what to do with `false`, exactly as they do for one written on the
// schema itself.
func (g *Generator) soleBranchAdditionalProperties(allOf []*schema.Schema, onPath map[*schema.Schema]bool) *schema.SchemaOrBool {
	var found *schema.SchemaOrBool
	take := func(ap *schema.SchemaOrBool) bool {
		if ap == nil {
			return true
		}
		if found != nil {
			return false
		}
		found = ap
		return true
	}
	for _, sub := range allOf {
		if sub == nil || onPath[sub] {
			continue
		}
		onPath[sub] = true
		resolved := sub
		for {
			if !take(resolved.AdditionalProperties) {
				return nil
			}
			if nested := g.soleBranchAdditionalProperties(resolved.AllOf, onPath); nested != nil {
				if found != nil && found != nested {
					return nil
				}
				found = nested
			}
			effRef := resolved.EffectiveRef()
			if effRef == "" {
				break
			}
			r := g.resolveRefInContext(effRef, resolved)
			if r == nil || onPath[r] {
				break
			}
			onPath[r] = true
			resolved = r
		}
	}
	return found
}

// soleBranchArrayKeywords returns the one schema in an allOf that states any
// array-structural keyword, and nil when none does or more than one does.
//
// It exists for the same reason soleBranchAdditionalProperties does, and closes
// the gap on the same terms. The merge deliberately leaves a branch's items,
// prefixItems, additionalItems and contains alone, because those keywords are
// scoped to the schema object stating them: a parent's `items` governs what
// follows the parent's *own* prefix, so folding a branch's prefixItems in beside
// it would change which elements the parent's keyword reaches.
//
// When the parent states none of them the objection disappears -- there is no
// parent prefix for a merged one to shift -- and the branch's keywords speak
// about exactly the array the merged type decodes. {"type":"array","$ref":X}
// with X carrying prefixItems is that case, and it is the shape
// draft2019-09/optional/cross-draft reaches: the type was enforced and the
// prefix was not, so [1,2,3] passed a schema whose first element must be a
// string.
//
// A whole schema object is returned rather than keyword by keyword, because the
// keywords are scoped together: prefixItems and items only mean what they mean
// relative to each other. Two branches stating them answer nil rather than
// picking, exactly as two additionalProperties do -- satisfying both is an allOf
// of the two, which one merged type cannot express.
func (g *Generator) soleBranchArrayKeywords(allOf []*schema.Schema, onPath map[*schema.Schema]bool) *schema.Schema {
	var found *schema.Schema
	take := func(s *schema.Schema) bool {
		if s == nil || !statesArrayStructure(s) {
			return true
		}
		if found != nil && found != s {
			return false
		}
		found = s
		return true
	}
	for _, sub := range allOf {
		if sub == nil || onPath[sub] {
			continue
		}
		onPath[sub] = true
		resolved := sub
		for {
			if !take(resolved) {
				return nil
			}
			if nested := g.soleBranchArrayKeywords(resolved.AllOf, onPath); nested != nil {
				if found != nil && found != nested {
					return nil
				}
				found = nested
			}
			effRef := resolved.EffectiveRef()
			if effRef == "" {
				break
			}
			r := g.resolveRefInContext(effRef, resolved)
			if r == nil || onPath[r] {
				break
			}
			onPath[r] = true
			resolved = r
		}
	}
	return found
}

// statesArrayStructure reports whether a schema states any of the keywords that
// describe the positions of an array. unevaluatedItems is not among them: what
// it evaluates depends on the other applicators, so it is not a keyword one
// schema object can hand to another.
func statesArrayStructure(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	return len(s.PrefixItems) > 0 || s.Items != nil || s.AdditionalItems != nil || s.Contains != nil
}

// mergeAllOfInto recursively merges properties, required fields, and validation
// constraints from allOf sub-schemas into the target schema. This handles cases
// like remote schemas that themselves contain allOf with internal $ref chains.
func (g *Generator) mergeAllOfInto(target *schema.Schema, allOf []*schema.Schema) {
	g.mergeAllOfBranches(target, allOf, make(map[*schema.Schema]bool))
}

// mergeAllOfBranches carries the set of schema nodes on the current merge path
// so an allOf that refers back into itself terminates.
//
// allOf closes a loop in two independent ways and both are fatal untreated.
// {"allOf":[{"$ref":"#/allOf/0"}]} makes the $ref chain below resolve to the
// very node it started from: none of the structural keywords the break
// condition tests are present, so the loop reassigns the same pointer forever
// at constant stack and constant memory -- a silent hang, not a crash. And an
// allOf whose $ref leads back to the schema that owns it re-enters this
// function forever, which is a stack overflow. A node already on the path has
// contributed everything it has to the target, so stopping at it loses nothing.
//
// The marks are removed again once a branch is done, so this is the path and
// not everything ever seen. Two branches that legitimately merge the same node
// -- allOf: [{$ref: X}, {$ref: X}] -- must each merge it, exactly as before.
func (g *Generator) mergeAllOfBranches(target *schema.Schema, allOf []*schema.Schema, onPath map[*schema.Schema]bool) {
	for _, sub := range allOf {
		if onPath[sub] {
			continue
		}
		onPath[sub] = true
		resolved := sub
		// Follow $ref chains until we reach a schema with properties or no more refs.
		var pushedCount int
		chain := []*schema.Schema{sub}
		for {
			effRef := resolved.EffectiveRef()
			if effRef == "" {
				break
			}
			r := g.resolveRefInContext(effRef, resolved)
			if r == nil {
				break
			}
			if onPath[r] {
				break
			}
			onPath[r] = true
			chain = append(chain, r)
			if g.pushDynamicScope(r) {
				pushedCount++
			}
			// If the resolved schema has structural content (properties, array keywords,
			// allOf, etc.), stop following $ref and use this schema.
			if len(r.Properties) > 0 || len(r.PatternProperties) > 0 || len(r.AllOf) > 0 ||
				r.Items != nil || len(r.PrefixItems) > 0 || r.Contains != nil || r.AdditionalItems != nil {
				resolved = r
				break
			}
			// The resolved schema has no direct properties — it may itself
			// be a $ref-only schema; follow it.
			resolved = r
		}
		// Copy direct properties.
		for k, v := range resolved.Properties {
			target.Properties[k] = v
		}
		target.Required = append(target.Required, resolved.Required...)
		// Merge patternProperties from allOf sub-schemas.
		for k, v := range resolved.PatternProperties {
			if target.PatternProperties == nil {
				target.PatternProperties = make(map[string]*schema.Schema)
			}
			if _, exists := target.PatternProperties[k]; !exists {
				target.PatternProperties[k] = v
			}
		}
		// A branch that states a "type" settles what an earlier branch's array
		// keywords only let the merge guess, whichever order the two arrive in.
		if len(resolved.Type) > 0 {
			delete(g.arrayTypeInferredFromBranch, target)
		}
		// Propagate type from sub-schemas if the target doesn't have one.
		if len(resolved.Type) > 0 && len(target.Type) == 0 {
			target.Type = resolved.Type
		}
		if g.validationKeywordsEnabled() && supportsDependentRequired(g.draftForSchema(resolved)) && len(resolved.DependentRequired) > 0 && len(target.DependentRequired) == 0 {
			target.DependentRequired = resolved.DependentRequired
		}
		if g.validationKeywordsEnabled() && supportsDependentRequired(g.draftForSchema(resolved)) && len(resolved.DependentSchemas) > 0 && len(target.DependentSchemas) == 0 {
			target.DependentSchemas = resolved.DependentSchemas
		}
		// Propagate validation constraints (use tightest / first-set-wins).
		if g.validationKeywordsEnabled() {
			mergeConstraints(target, resolved)
			// propertyNames constrains the same object the branch's other object
			// keywords do, and until this was read off the branch it was the one
			// keyword mergeAllOfBranches never looked at -- so an allOf branch
			// that stated it enforced nothing at all.
			target.PropertyNames = mergePropertyNames(target.PropertyNames, resolved.PropertyNames)
		}
		// NOTE: We deliberately do NOT merge array-structural keywords (items,
		// prefixItems, contains, additionalItems) from allOf sub-schemas into
		// the target. Per JSON Schema spec, items/additionalItems scoping is
		// per-schema — merging them would change the scoping semantics (e.g.,
		// parent's `items` would apply only after merged `prefixItems` instead
		// of to all elements). Parent array keywords are propagated separately
		// in generateAllOfDef after merging.
		// However, we DO infer the type from sub-schema array/object keywords
		// so that the merged schema can still generate the right Go type.
		if len(target.Type) == 0 {
			if resolved.Items != nil || len(resolved.PrefixItems) > 0 || resolved.Contains != nil || resolved.AdditionalItems != nil {
				target.Type = []string{"array"}
				// Recorded, because "array" here is a guess about what the
				// branch is *about* and not something any schema on the merge
				// stated. generateAllOfDef reads this to tell the two apart;
				// without it a merged type read as declared, and the array arm
				// it picked refuses every instance that is not an array.
				g.arrayTypeInferredFromBranch[target] = true
			}
		}
		// Recursively merge nested allOf chains.
		if len(resolved.AllOf) > 0 {
			g.mergeAllOfBranches(target, resolved.AllOf, onPath)
		}
		// Merge object shape contributed by variants inside an allOf branch. This
		// handles schemas like allOf: [$ref base, {oneOf: [variant objects]}]
		// by making variant-specific properties typed fields on the merged struct.
		// Variant required lists are intentionally not promoted to global required.
		// The variant merge gets a path set of its own rather than sharing this
		// function's. The two walks visit the document differently -- this one
		// follows allOf branches, that one descends oneOf/anyOf/then/else -- and
		// a node the allOf chain merely passed through has no bearing on whether
		// the variant walk is looping.
		variantPath := make(map[*schema.Schema]bool)
		g.mergeApplicatorVariantPropertiesInto(target, resolved.OneOf, variantPath)
		g.mergeApplicatorVariantPropertiesInto(target, resolved.AnyOf, variantPath)
		g.mergeConditionalBranchPropertiesInto(target, resolved, variantPath)
		for i := 0; i < pushedCount; i++ {
			g.popDynamicScope()
		}
		for _, node := range chain {
			delete(onPath, node)
		}
	}
}

func (g *Generator) mergeApplicatorVariantPropertiesInto(target *schema.Schema, variants []*schema.Schema, onPath map[*schema.Schema]bool) {
	for _, variant := range variants {
		g.mergeVariantObjectPropertiesInto(target, variant, onPath)
	}
}

func (g *Generator) mergeConditionalBranchPropertiesInto(target *schema.Schema, s *schema.Schema, onPath map[*schema.Schema]bool) {
	if s == nil {
		return
	}
	// Conditional branch required lists are not globally required. They are only
	// required when their corresponding if condition matches at validation time.
	g.mergeVariantObjectPropertiesInto(target, s.Then, onPath)
	g.mergeVariantObjectPropertiesInto(target, s.Else, onPath)
}

// extractObjectOneOfDefs collects the object-level oneOf groups that apply to a
// flattened object: the one written beside its properties, and one from each
// allOf branch, since those branches are merged into this same struct.
//
// taken names the variant slices collectRuntimeBranchChecks already compiled to
// an exact check. Each is skipped here rather than approximated as well: the
// approximation decides whether a branch matches from its required keys, its
// consts and its declared types, and a branch stating unevaluatedProperties can
// fail on a key none of those mention, so the two disagree about the match count
// and `oneOf` turns that disagreement into a rejection. See #111 for the
// schema's own slice and #135 for an allOf branch's.
func (g *Generator) extractObjectOneOfDefs(s *schema.Schema, taken []RuntimeBranchCheck) []ObjectOneOfDef {
	if s == nil || !g.validationKeywordsEnabled() {
		return nil
	}
	var defs []ObjectOneOfDef
	if !runtimeBranchTaken(taken, s, "oneOf") {
		if def := g.objectOneOfDefFromVariants(s.OneOf); def != nil {
			defs = append(defs, *def)
		}
	}
	for _, sub := range s.AllOf {
		resolved := g.resolveSchemaForApplicator(sub)
		if runtimeBranchTaken(taken, resolved, "oneOf") {
			continue
		}
		if def := g.objectOneOfDefFromVariants(resolved.OneOf); def != nil {
			defs = append(defs, *def)
		}
	}
	return defs
}

// extractObjectConditionalDefs collects the object-level if/then/else groups
// that apply to a flattened object: the one written beside its properties, and
// one from each allOf branch, since those branches are merged into this same
// struct and their conditionals would otherwise land nowhere.
func (g *Generator) extractObjectConditionalDefs(s *schema.Schema) []ObjectConditionalDef {
	if s == nil || !g.validationKeywordsEnabled() {
		return nil
	}
	var defs []ObjectConditionalDef
	if def := objectConditionalDef(s); def != nil {
		defs = append(defs, *def)
	}
	for _, sub := range s.AllOf {
		resolved := g.resolveSchemaForApplicator(sub)
		if def := objectConditionalDef(resolved); def != nil {
			defs = append(defs, *def)
		}
	}
	return defs
}

// extractObjectAnyOfDefs is extractObjectOneOfDefs for `anyOf`, and takes the
// same per-slice suppression for the same reason.
func (g *Generator) extractObjectAnyOfDefs(s *schema.Schema, taken []RuntimeBranchCheck) []ObjectAnyOfDef {
	if s == nil || !g.validationKeywordsEnabled() {
		return nil
	}
	var defs []ObjectAnyOfDef
	if !runtimeBranchTaken(taken, s, "anyOf") {
		if def := g.objectAnyOfDefFromVariants(s.AnyOf); def != nil {
			defs = append(defs, *def)
		}
	}
	for _, sub := range s.AllOf {
		resolved := g.resolveSchemaForApplicator(sub)
		if runtimeBranchTaken(taken, resolved, "anyOf") {
			continue
		}
		if def := g.objectAnyOfDefFromVariants(resolved.AnyOf); def != nil {
			defs = append(defs, *def)
		}
	}
	return defs
}

func (g *Generator) objectOneOfDefFromVariants(variants []*schema.Schema) *ObjectOneOfDef {
	if len(variants) == 0 {
		return nil
	}
	branches := make([]ObjectOneOfBranch, 0, len(variants))
	for _, variant := range variants {
		branch := g.objectOneOfBranchFromSchema(variant)
		if len(branch.RequiredKeys) == 0 && len(branch.Checks) == 0 {
			return nil
		}
		branches = append(branches, branch)
	}
	return &ObjectOneOfDef{Branches: branches}
}

func (g *Generator) objectOneOfBranchFromSchema(s *schema.Schema) ObjectOneOfBranch {
	return g.objectOneOfBranchOnPath(s, nil)
}

// objectOneOfBranchOnPath collects the required keys and property checks that
// identify one oneOf/anyOf branch, gathering them from the branch schema and
// from every allOf it pulls in.
//
// onPath holds the schemas whose allOf this collection is already inside. A
// branch whose allOf leads back to one of them -- $defs.A with
// allOf: [{$ref: "#/$defs/A"}] -- re-enters this function forever otherwise,
// which is a stack overflow. A schema already on the path has had its keys and
// checks folded into the branch being built, so stopping at it loses nothing.
// The mark comes off on the way out, leaving sibling allOf entries that name
// the same schema free to contribute as before. The set is only allocated for
// a schema that actually has an allOf to descend into.
func (g *Generator) objectOneOfBranchOnPath(s *schema.Schema, onPath map[*schema.Schema]bool) ObjectOneOfBranch {
	resolved := g.resolveSchemaForApplicator(s)
	if resolved == nil || resolved.IsBooleanSchema() || onPath[resolved] {
		return ObjectOneOfBranch{}
	}
	branch := ObjectOneOfBranch{RequiredKeys: append([]string(nil), resolved.Required...)}
	sort.Strings(branch.RequiredKeys)
	propNames := sortedKeys(resolved.Properties)
	for _, propName := range propNames {
		if check := objectPropertyCheckFromSchema(propName, resolved.Properties[propName]); check != nil {
			branch.Checks = append(branch.Checks, *check)
		}
	}
	if len(resolved.AllOf) > 0 {
		if onPath == nil {
			onPath = make(map[*schema.Schema]bool)
		}
		onPath[resolved] = true
		for _, sub := range resolved.AllOf {
			subBranch := g.objectOneOfBranchOnPath(sub, onPath)
			branch.RequiredKeys = mergeStringSets(branch.RequiredKeys, subBranch.RequiredKeys)
			branch.Checks = append(branch.Checks, subBranch.Checks...)
		}
		delete(onPath, resolved)
	}
	return branch
}

func (g *Generator) resolveSchemaForApplicator(s *schema.Schema) *schema.Schema {
	if s == nil {
		return nil
	}
	resolved := s
	// A $ref chain that closes on itself would spin here forever. The loop only
	// keeps going while a hop lands on a schema with nothing in it, and a hop
	// back onto a node already stood on lands on exactly such a schema again --
	// nothing about the second visit differs from the first, so there is no
	// iteration that ends it. isSelfRefInContext catches only refs aimed at the
	// document root, not a subschema whose own $id makes its $ref resolve back
	// to itself, so it is no defence here.
	//
	// The visited set makes the chain finite: arriving twice at the same node
	// stops and hands back the last node reached. A chain that terminates never
	// revisits a node, so nothing that used to resolve resolves differently.
	// It is allocated on the first hop, leaving the common ref-free schema free.
	var visited map[*schema.Schema]bool
	for {
		effRef := resolved.EffectiveRef()
		if effRef == "" || g.isSelfRefInContext(effRef, resolved) {
			return resolved
		}
		r := g.resolveRefInContext(effRef, resolved)
		if r == nil {
			return resolved
		}
		if visited == nil {
			visited = map[*schema.Schema]bool{resolved: true}
		}
		if visited[r] {
			return resolved
		}
		visited[r] = true
		resolved = r
		if len(resolved.Properties) > 0 || len(resolved.AllOf) > 0 || len(resolved.OneOf) > 0 || len(resolved.AnyOf) > 0 || resolved.If != nil || resolved.Then != nil || resolved.Else != nil || len(resolved.Type) > 0 || len(resolved.Enum) > 0 || resolved.Const != nil || resolved.ConstIsNull {
			return resolved
		}
	}
}

func objectPropertyCheckFromSchema(jsonName string, s *schema.Schema) *ObjectPropertyCheck {
	if s == nil || s.IsBooleanSchema() {
		return nil
	}
	check := &ObjectPropertyCheck{JSONName: jsonName}
	if len(s.Type) == 1 {
		check.JSONType = s.Type[0]
	}
	values := enumLikeValues(s)
	if len(values) > 0 {
		for _, value := range values {
			b, err := json.Marshal(value)
			if err != nil {
				continue
			}
			check.AllowedValues = append(check.AllowedValues, string(b))
		}
	}
	if check.JSONType == "" && len(check.AllowedValues) == 0 {
		return nil
	}
	return check
}

func mergeStringSets(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, value := range a {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	for _, value := range b {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

// mergeVariantObjectPropertiesInto folds the object shape of one applicator
// variant into target, following the variant's $ref chain and then descending
// into whatever applicators the variant itself carries.
//
// onPath holds every node this descent is already inside. Without it a variant
// that leads back to one of them -- oneOf: [{$ref: the schema that owns the
// oneOf}] -- re-enters this function forever, which is a stack overflow, and a
// $ref that resolves to its own node spins in the chain loop below instead. A
// node already being merged has contributed everything it has to the target, so
// stopping at it loses nothing.
//
// The marks come off again on the way out, so this is the path and not
// everything ever seen. Two variants that legitimately name the same node --
// oneOf: [{$ref: X}, {$ref: X}] -- must each merge it, exactly as before.
func (g *Generator) mergeVariantObjectPropertiesInto(target *schema.Schema, variant *schema.Schema, onPath map[*schema.Schema]bool) {
	if target == nil || variant == nil || variant.IsBooleanSchema() {
		return
	}
	if onPath == nil {
		onPath = make(map[*schema.Schema]bool)
	}
	if onPath[variant] {
		return
	}
	onPath[variant] = true
	chain := []*schema.Schema{variant}
	resolved := variant
	var pushedCount int
	for {
		effRef := resolved.EffectiveRef()
		if effRef == "" || g.isSelfRefInContext(effRef, resolved) {
			break
		}
		r := g.resolveRefInContext(effRef, resolved)
		if r == nil {
			break
		}
		// Stopping before the repeat rather than after also keeps pushedCount
		// honest: no scope is pushed for the hop that is not taken.
		if onPath[r] {
			break
		}
		onPath[r] = true
		chain = append(chain, r)
		if g.pushDynamicScope(r) {
			pushedCount++
		}
		resolved = r
		if len(r.Properties) > 0 || len(r.PatternProperties) > 0 || len(r.AllOf) > 0 || len(r.OneOf) > 0 || len(r.AnyOf) > 0 || r.If != nil || r.Then != nil || r.Else != nil {
			break
		}
	}

	for k, v := range resolved.Properties {
		if existing, exists := target.Properties[k]; exists {
			target.Properties[k] = mergeVariantPropertySchemas(existing, v)
		} else {
			target.Properties[k] = v
		}
	}
	for k, v := range resolved.PatternProperties {
		if target.PatternProperties == nil {
			target.PatternProperties = make(map[string]*schema.Schema)
		}
		if _, exists := target.PatternProperties[k]; !exists {
			target.PatternProperties[k] = v
		}
	}
	if len(target.Type) == 0 && (len(resolved.Properties) > 0 || len(resolved.PatternProperties) > 0) {
		target.Type = []string{"object"}
	}

	for _, sub := range resolved.AllOf {
		g.mergeVariantObjectPropertiesInto(target, sub, onPath)
	}
	g.mergeApplicatorVariantPropertiesInto(target, resolved.OneOf, onPath)
	g.mergeApplicatorVariantPropertiesInto(target, resolved.AnyOf, onPath)
	g.mergeConditionalBranchPropertiesInto(target, resolved, onPath)

	for i := 0; i < pushedCount; i++ {
		g.popDynamicScope()
	}
	for _, node := range chain {
		delete(onPath, node)
	}
}

func mergeVariantPropertySchemas(existing, next *schema.Schema) *schema.Schema {
	if existing == nil {
		return next
	}
	if next == nil {
		return existing
	}
	if canUnionEnumLikeSchemas(existing, next) {
		merged := *existing
		merged.Enum = unionEnumValues(enumLikeValues(existing), enumLikeValues(next))
		merged.Const = nil
		merged.ConstIsNull = false
		if len(merged.Type) == 0 {
			merged.Type = enumValueTypeList(merged.Enum)
		}
		return &merged
	}
	if schemasHaveCompatibleType(existing, next) {
		return existing
	}
	merged := *existing
	merged.Type = nil
	merged.Enum = nil
	merged.Const = nil
	merged.ConstIsNull = false
	merged.Format = nil
	merged.MinLength = nil
	merged.MaxLength = nil
	merged.Pattern = nil
	merged.Minimum = nil
	merged.Maximum = nil
	merged.ExclusiveMinimum = nil
	merged.ExclusiveMaximum = nil
	merged.MultipleOf = nil
	merged.Items = nil
	merged.PrefixItems = nil
	merged.MinItems = nil
	merged.MaxItems = nil
	merged.UniqueItems = nil
	merged.Properties = nil
	merged.Required = nil
	merged.PatternProperties = nil
	return &merged
}

func canUnionEnumLikeSchemas(a, b *schema.Schema) bool {
	av := enumLikeValues(a)
	bv := enumLikeValues(b)
	if len(av) == 0 || len(bv) == 0 {
		return false
	}
	return enumValuesHaveSingleJSONType(av) && enumValuesHaveSingleJSONType(bv) && enumValueJSONType(av[0]) == enumValueJSONType(bv[0])
}

func enumLikeValues(s *schema.Schema) []any {
	if s == nil {
		return nil
	}
	if len(s.Enum) > 0 {
		return s.Enum
	}
	if s.Const != nil {
		return []any{*s.Const}
	}
	if s.ConstIsNull {
		return []any{nil}
	}
	return nil
}

func unionEnumValues(values ...[]any) []any {
	var out []any
	seen := make(map[string]bool)
	for _, group := range values {
		for _, value := range group {
			key := enumValueKey(value)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, value)
		}
	}
	return out
}

func enumValueKey(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%T:%v", value, value)
	}
	return string(b)
}

func enumValuesHaveSingleJSONType(values []any) bool {
	if len(values) == 0 {
		return false
	}
	want := enumValueJSONType(values[0])
	for _, value := range values[1:] {
		if enumValueJSONType(value) != want {
			return false
		}
	}
	return true
}

func enumValueJSONType(value any) string {
	switch value.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64, int, int64:
		return "number"
	case nil:
		return "null"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return ""
	}
}

func enumValueTypeList(values []any) schema.TypeList {
	if len(values) == 0 || !enumValuesHaveSingleJSONType(values) {
		return nil
	}
	t := enumValueJSONType(values[0])
	if t == "number" {
		return schema.TypeList{"number"}
	}
	if t == "" {
		return nil
	}
	return schema.TypeList{t}
}

func schemasHaveCompatibleType(a, b *schema.Schema) bool {
	at := propertySchemaTypeSignature(a)
	bt := propertySchemaTypeSignature(b)
	return at != "" && at == bt
}

func propertySchemaTypeSignature(s *schema.Schema) string {
	if s == nil {
		return ""
	}
	if len(s.Type) > 0 {
		return strings.Join(sortedNonNullTypes(s.Type), "|")
	}
	if values := enumLikeValues(s); len(values) > 0 && enumValuesHaveSingleJSONType(values) {
		return enumValueJSONType(values[0])
	}
	return ""
}

func sortedNonNullTypes(types schema.TypeList) []string {
	out := make([]string, 0, len(types))
	for _, typ := range types {
		if typ != "null" {
			out = append(out, typ)
		}
	}
	sort.Strings(out)
	return out
}

// allOfContainsFalseSchema returns true if any sub-schema in the allOf array
// is a boolean false schema. In that case, nothing can satisfy all constraints
// simultaneously, so the entire allOf is equivalent to false.
//
// A $ref is followed, because {"allOf":[{"$ref":"#/$defs/b"}]} over {"b":false}
// is the same schema as {"allOf":[false]} written out -- and it was the shape
// that got past this: the branch is not itself the boolean, so the merge ran, a
// definition saying nothing merged to nothing, and the result was `any` with the
// rejection gone. onPath is what makes following safe against
// {"$defs":{"a":{"allOf":[{"$ref":"#/$defs/a"}]}}}: a node already being
// examined one frame up has nothing to add.
func (g *Generator) allOfContainsFalseSchema(allOf []*schema.Schema) bool {
	return g.allOfContainsFalseSchemaOnPath(allOf, nil)
}

func (g *Generator) allOfContainsFalseSchemaOnPath(allOf []*schema.Schema, onPath map[*schema.Schema]bool) bool {
	for _, sub := range allOf {
		if sub == nil {
			continue
		}
		if g.schemaForbidsEveryValue(sub) {
			return true
		}
		if onPath[sub] {
			continue
		}
		if onPath == nil {
			onPath = make(map[*schema.Schema]bool)
		}
		onPath[sub] = true
		hit := len(sub.AllOf) > 0 && g.allOfContainsFalseSchemaOnPath(sub.AllOf, onPath)
		if !hit {
			if effRef := sub.EffectiveRef(); effRef != "" {
				if r := g.resolveRefInContext(effRef, sub); r != nil && !onPath[r] {
					onPath[r] = true
					hit = g.schemaForbidsEveryValue(r) ||
						(len(r.AllOf) > 0 && g.allOfContainsFalseSchemaOnPath(r.AllOf, onPath))
					delete(onPath, r)
				}
			}
		}
		delete(onPath, sub)
		if hit {
			return true
		}
	}
	return false
}

// subIsFalse reports whether a composition branch admits nothing -- it is the
// boolean `false` itself, it says the same thing another way, or it is a
// reference to one that does.
//
// The reference has to count, for the reason #116 records: a $ref is not the
// schema it names, so a predicate that only asks IsFalseSchema sees an ordinary
// branch, and {"anyOf":[{"$ref":"#/$defs/b"}]} over {"b":false} came out
// `type Root any` while the identical {"anyOf":[false]} was already refused.
// The other spelling counts for the same reason one keyword further out: an
// {"anyOf":[{"enum":[]}]} admits nothing, and reading it as an ordinary branch
// left the schema `any`.
func (g *Generator) subIsFalse(sub *schema.Schema) bool {
	if sub == nil {
		return false
	}
	return g.schemaForbidsEveryValue(sub) || g.resolvedToFalseSchema(sub)
}

// compositionAdmitsNothing reports whether an anyOf or a oneOf on this schema
// makes it unsatisfiable, on exactly the terms generateTypeDef decides it: every
// anyOf branch admitting nothing, every oneOf branch admitting nothing, or more
// than one oneOf branch admitting everything (oneOf wants exactly one match).
//
// It exists because those arms live in generateTypeDef alone. An inline position
// -- a property, an element, a map value -- resolves rather than names, and
// resolveType has no arm that reads a composition, so the schema fell to `any`
// and the rejection went with it. Delegating makes the inline answer the named
// one. The allOf case is not here: allOfNeedsNamedType already answers for it,
// through the same allOfContainsFalseSchema generateAllOfDef uses.
//
// The parent-keyword guards mirror generateTypeDef's, so this claims only
// schemas that arm will actually forbid: a schema with properties of its own, or
// a oneOf beside a declared type, goes to a struct or an alias there and would
// come back from the delegation typed but not forbidding.
func (g *Generator) compositionAdmitsNothing(s *schema.Schema) bool {
	if s == nil || hasProperties(s) {
		return false
	}
	if len(s.AnyOf) > 0 && g.allSubsFalse(s.AnyOf) {
		return true
	}
	if len(s.OneOf) > 0 && len(s.Type) == 0 {
		trueCount, falseCount := g.countBooleanSchemas(s.OneOf)
		if falseCount == len(s.OneOf) || trueCount > 1 {
			return true
		}
	}
	return false
}

// allSubsFalse returns true if every sub-schema in the list admits nothing.
// Used for anyOf: if all variants are false, nothing can match.
func (g *Generator) allSubsFalse(subs []*schema.Schema) bool {
	if len(subs) == 0 {
		return false
	}
	for _, sub := range subs {
		if !g.subIsFalse(sub) {
			return false
		}
	}
	return true
}

// countBooleanSchemas counts how many sub-schemas are boolean true and boolean false.
// An empty schema {} or a schema with no constraints is treated as "always true"
// for this purpose. A $ref is read through to its target on both sides, so a
// reference to `false` counts as false and one to `true` as true.
func (g *Generator) countBooleanSchemas(subs []*schema.Schema) (trueCount, falseCount int) {
	for _, sub := range subs {
		if g.subIsFalse(sub) {
			falseCount++
		} else if sub.IsTrueSchema() || g.acceptsEveryInstance(sub) {
			trueCount++
		}
	}
	return
}

// mergeConstraints propagates validation constraint fields from src into dst.
// A value from an allOf sub-schema must be satisfied simultaneously with the
// others, so overlapping constraints are combined to the *tightest* bound (max
// of lower bounds, min of upper bounds) rather than kept first-set-wins — which
// would silently drop every branch's constraint but the first.
func mergeConstraints(dst, src *schema.Schema) {
	// Numeric constraints: keep the tighter bound.
	dst.Minimum = tighterLowerFloat(dst.Minimum, src.Minimum)
	dst.Maximum = tighterUpperFloat(dst.Maximum, src.Maximum)
	dst.ExclusiveMinimum = tighterExclusive(dst.ExclusiveMinimum, src.ExclusiveMinimum, true)
	dst.ExclusiveMaximum = tighterExclusive(dst.ExclusiveMaximum, src.ExclusiveMaximum, false)
	dst.MultipleOf = combineMultipleOf(dst.MultipleOf, src.MultipleOf)
	// String constraints.
	dst.MinLength = tighterLowerFlexInt(dst.MinLength, src.MinLength)
	dst.MaxLength = tighterUpperFlexInt(dst.MaxLength, src.MaxLength)
	if dst.Pattern == nil && src.Pattern != nil {
		// Two distinct patterns must both match; a single regex can't express
		// that in general, so the first is kept (a known narrow limitation).
		dst.Pattern = src.Pattern
	}
	if dst.Format == nil && src.Format != nil {
		// A branch's `format` binds the same instance the branch's `type` does,
		// and the type was already being read off the branch -- so leaving the
		// format behind produced a merged schema that said "string" where the
		// branch said "a date-time string". {"allOf":[{"$ref":"#/$defs/Stamp"}]}
		// then generated `type X string` while Stamp itself was time.Time: two
		// Go types for one schema, and the format assertion enforced on one of
		// them only.
		//
		// First-set-wins, as `pattern` above: two different formats on one
		// instance is a schema that almost nothing can satisfy, and there is one
		// slot to put an answer in.
		dst.Format = src.Format
	}
	// The content vocabulary is carried on the same terms and for the same
	// reason: a branch's contentEncoding binds the same string the branch's
	// other keywords do, and leaving it behind gave a merged schema that said
	// less than the branch it came from. {"allOf":[{"contentEncoding":"base64"}]}
	// then had nothing left to name a type from and fell through to `type X any`
	// -- which is issue #115 in its allOf spelling.
	//
	// First-set-wins, as `pattern` and `format` above, and for the same reason:
	// there is one slot per keyword and two different answers in it cannot both
	// be written. The direction is under-enforcement, never a rejection the
	// schema does not state.
	if dst.ContentEncoding == "" && src.ContentEncoding != "" {
		dst.ContentEncoding = src.ContentEncoding
	}
	if dst.ContentMediaType == "" && src.ContentMediaType != "" {
		dst.ContentMediaType = src.ContentMediaType
	}
	if dst.ContentSchema == nil && src.ContentSchema != nil {
		dst.ContentSchema = src.ContentSchema
	}
	// enum and const say which values are legal at all, so a branch stating one
	// is the whole of what that branch asserts. Dropped, the merged schema had
	// nothing left to infer a type from and fell through to `type X any`, which
	// carries no Validate -- so every member of the enum and every value outside
	// it were accepted alike. Intersecting two enums is what allOf means, but a
	// merged schema holds one list; the first is kept, which under-enforces
	// rather than rejecting values the schema allows. Same direction as
	// mergePropertyNames, which already resolves this pair that way.
	if len(dst.Enum) == 0 && dst.Const == nil && !dst.ConstIsNull {
		dst.Enum, dst.Const, dst.ConstIsNull = src.Enum, src.Const, src.ConstIsNull
	}
	// Array constraints.
	dst.MinItems = tighterLowerFlexInt(dst.MinItems, src.MinItems)
	dst.MaxItems = tighterUpperFlexInt(dst.MaxItems, src.MaxItems)
	if src.UniqueItems != nil && *src.UniqueItems {
		t := true
		dst.UniqueItems = &t
	} else if dst.UniqueItems == nil && src.UniqueItems != nil {
		dst.UniqueItems = src.UniqueItems
	}
	// Object constraints.
	dst.MinProperties = tighterLowerFlexInt(dst.MinProperties, src.MinProperties)
	dst.MaxProperties = tighterUpperFlexInt(dst.MaxProperties, src.MaxProperties)
}

// mergePropertyNames combines the two propertyNames sub-schemas an allOf brings
// to bear on the same object. Both bind at once, so the result must satisfy
// both, and it returns a fresh node rather than writing through to either --
// schema nodes are shared across $ref targets, and mutating one would leak the
// merge into every other use of it.
//
// What can be intersected is: a false schema on either side (no name is legal),
// a true schema on either side (that side says nothing), and the length bounds,
// where the tighter wins. `pattern` and `enum` cannot be: a single regex cannot
// in general express "matches both", and PropertyNamesDef has one slot for
// each. There the first stated wins -- the same single-pattern limitation
// mergeConstraints already documents for `pattern`. That under-enforces (a name
// the other pattern would reject is let through) rather than rejecting names
// the schema allows, which is the safer direction for generated code.
func mergePropertyNames(dst, src *schema.Schema) *schema.Schema {
	if src == nil {
		return dst
	}
	if dst == nil {
		return src
	}
	if dst.IsFalseSchema() || src.IsTrueSchema() {
		return dst
	}
	if src.IsFalseSchema() || dst.IsTrueSchema() {
		return src
	}
	combined := *dst // shallow copy; mergeConstraints writes into its first argument
	mergeConstraints(&combined, src)
	if len(combined.Enum) == 0 && combined.Const == nil {
		combined.Enum, combined.Const = src.Enum, src.Const
	}
	return &combined
}

// tighterLowerFloat returns the larger of two lower bounds (the tighter one).
// The returned pointer is one of the inputs; neither is mutated.
func tighterLowerFloat(a, b *float64) *float64 {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if *b > *a {
		return b
	}
	return a
}

// tighterUpperFloat returns the smaller of two upper bounds (the tighter one).
func tighterUpperFloat(a, b *float64) *float64 {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if *b < *a {
		return b
	}
	return a
}

// tighterLowerFlexInt returns the larger of two lower bounds.
func tighterLowerFlexInt(a, b *schema.FlexInt) *schema.FlexInt {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if b.Int() > a.Int() {
		return b
	}
	return a
}

// tighterUpperFlexInt returns the smaller of two upper bounds.
func tighterUpperFlexInt(a, b *schema.FlexInt) *schema.FlexInt {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if b.Int() < a.Int() {
		return b
	}
	return a
}

// tighterExclusive combines two exclusiveMinimum/exclusiveMaximum values. Both
// carry a numeric bound (Draft 6+) or a boolean flag (Draft 4). When both are
// numeric, the tighter bound is kept (max for minimum, min for maximum). When
// either uses the boolean form the first is retained (they aren't comparable
// without the sibling minimum/maximum, and mixing draft dialects in one allOf
// is not a case worth special handling).
func tighterExclusive(a, b *schema.SchemaOrFloat, lower bool) *schema.SchemaOrFloat {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.Number == nil || b.Number == nil {
		return a
	}
	if lower {
		if *b.Number > *a.Number {
			return b
		}
		return a
	}
	if *b.Number < *a.Number {
		return b
	}
	return a
}

// combineMultipleOf combines two multipleOf divisors: a value must be divisible
// by both. For integral divisors this is their least common multiple. When one
// divisor is an exact multiple of the other, the larger (tighter) one is kept.
// Otherwise (incompatible non-integral divisors) the first is retained.
func combineMultipleOf(a, b *float64) *float64 {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	av, bv := *a, *b
	if av == 0 || bv == 0 {
		return a
	}
	if av == math.Trunc(av) && bv == math.Trunc(bv) {
		l := lcmInt64(int64(av), int64(bv))
		f := float64(l)
		return &f
	}
	if q := av / bv; q == math.Trunc(q) { // av is a multiple of bv → av is tighter
		return a
	}
	if q := bv / av; q == math.Trunc(q) { // bv is a multiple of av → bv is tighter
		return b
	}
	return a
}

// lcmInt64 returns the least common multiple of two non-negative integers.
func lcmInt64(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	if a == 0 || b == 0 {
		return 0
	}
	return a / gcdInt64(a, b) * b
}

func gcdInt64(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// generateAnyOfDef merges all anyOf sub-schemas into a single struct.
// Unlike allOf (where all must match), anyOf means "at least one matches".
// We merge all properties from all variants into one struct, but no field
// is marked required (since only one variant needs to be satisfied).
func (g *Generator) generateAnyOfDef(name string, s *schema.Schema) error {
	// A branch the merged struct has no room for. The merge is a struct, so the
	// only values it can hold are objects, and a branch admitting anything else
	// is refused by encoding/json before a branch is tried. That is issue #133's
	// false rejection, and it is the same defect #125 answered for oneOf: the
	// evaluator judges every branch against the raw value, so it needs no Go
	// type per branch at all.
	//
	// Only when the evaluator reads the whole schema. Where it cannot, the merge
	// stays: wrong as it is about the escaping branch, it still enforces the
	// merged properties, where the alternative would enforce nothing.
	if g.anyOfMergeCannotHoldBranches(s) {
		if def := g.rawWrapperDef(name, s); def != nil {
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, def)
			return nil
		}
	}

	merged := &schema.Schema{
		Title:       s.Title,
		Description: s.Description,
		Properties:  make(map[string]*schema.Schema),
	}

	// Copy any properties from the parent schema itself.
	for k, v := range s.Properties {
		merged.Properties[k] = v
	}

	// Merge each anyOf sub-schema's properties.
	for _, sub := range s.AnyOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		for k, v := range resolved.Properties {
			if _, exists := merged.Properties[k]; !exists {
				merged.Properties[k] = v
			}
		}
		// Propagate type from sub-schemas if the parent doesn't have one.
		if len(resolved.Type) > 0 && len(merged.Type) == 0 {
			merged.Type = resolved.Type
		}
	}

	// Don't propagate required — in anyOf, no field is universally required.
	// Also propagate additionalProperties from the parent if set.
	merged.AdditionalProperties = s.AdditionalProperties

	// If none of the anyOf variants contributed properties, this is a union of
	// primitives (e.g. anyOf: [{type:"null"}, {type:"string"}]). Don't generate
	// a struct — fall back to an alias to `any` so that the value can hold any
	// of the variant types.
	if len(merged.Properties) == 0 {
		// The variants still constrain the value even with no properties to
		// merge, so prefer a wrapper carrying a Validate() over a bare
		// `type X any` that drops them.
		if def := g.rawWrapperDef(name, s); def != nil {
			g.generated[name] = true
			g.output.TypeDefs = append(g.output.TypeDefs, def)
			return nil
		}
		g.generated[name] = true
		g.output.TypeDefs = append(g.output.TypeDefs, g.unenforcedAliasDef(name, s))
		return nil
	}

	// anyOf is type-agnostic: don't silently accept non-object data.
	if err := g.generateStructDef(name, merged, false); err != nil {
		return err
	}

	// Merging variant properties into one permissive struct loses the "at least
	// one branch must match" constraint. Attach an object-level anyOf check
	// (>=1 branch, evaluated over the raw JSON) so validation enforces it. When
	// any branch has no matchable criteria (required keys / const / type checks),
	// that branch matches any object and the whole anyOf is unconstrained, so the
	// check is skipped to avoid false rejections.
	//
	// A branch stating unevaluatedProperties is taken over whole instead: the
	// merge builds `merged` without the anyOf, so generateStructDef never sees the
	// keyword and neither collector inside it fires. That is why the same schema
	// written with a property of its own was checked and this one was not --
	// {"type":"object","anyOf":[{"properties":{"b":{}},
	//                            "unevaluatedProperties":false}]}
	// accepted {"b":1,"c":2}, which no branch admits.
	if last := g.output.TypeDefs[len(g.output.TypeDefs)-1]; last.TypeName() == name {
		if sd, ok := last.(*StructDef); ok {
			runtimeChecks := g.collectRuntimeBranchChecks(s)
			if len(runtimeChecks) > 0 {
				sd.RuntimeBranchChecks = append(sd.RuntimeBranchChecks, runtimeChecks...)
				sd.NeedsUnmarshal = true
			}
			if !runtimeBranchTaken(runtimeChecks, s, "anyOf") {
				if anyOfDef := g.objectAnyOfDefFromVariants(s.AnyOf); anyOfDef != nil {
					sd.ObjectAnyOfs = append(sd.ObjectAnyOfs, *anyOfDef)
					sd.NeedsUnmarshal = true
				}
			}
		}
	}
	return nil
}

// objectBranchesHaveChecks reports whether any oneOf/anyOf branch carries a
// property check (JSON type or allowed value), which is what the generated
// validator inspects with bytes.TrimSpace. Branches keyed only on required
// property presence do not need the bytes import.
func objectBranchesHaveChecks(oneOfs []ObjectOneOfDef, anyOfs []ObjectAnyOfDef) bool {
	for _, g := range oneOfs {
		for _, b := range g.Branches {
			if len(b.Checks) > 0 {
				return true
			}
		}
	}
	for _, g := range anyOfs {
		for _, b := range g.Branches {
			if len(b.Checks) > 0 {
				return true
			}
		}
	}
	return false
}

// objectAnyOfDefFromVariants builds an ObjectAnyOfDef from anyOf variants,
// reusing the oneOf branch extraction. Returns nil when the branches are not
// all distinguishable by required keys or property checks.
func (g *Generator) objectAnyOfDefFromVariants(variants []*schema.Schema) *ObjectAnyOfDef {
	def := g.objectOneOfDefFromVariants(variants)
	if def == nil {
		return nil
	}
	return &ObjectAnyOfDef{Branches: def.Branches}
}

// anyOfBranchKeyedOnObjectShape reports whether a branch says something the
// ObjectAnyOf summary can read: a required key, or a type or allowed value for
// one of the properties it names.
//
// Those two are the whole of ObjectOneOfBranch, and both are statements about an
// object. A branch that makes neither is a branch the summary cannot judge and
// the merge cannot place -- which is the one question the two predicates below
// ask, from their two different sides.
func (g *Generator) anyOfBranchKeyedOnObjectShape(v *schema.Schema) bool {
	branch := g.objectOneOfBranchFromSchema(v)
	return len(branch.RequiredKeys) > 0 || len(branch.Checks) > 0
}

// anyOfMergeCannotHoldBranches reports whether some branch of s's anyOf admits a
// value the merged struct cannot decode.
//
// The merge is a Go struct, so the values it can hold are objects and nothing
// else. A branch naming any other type -- a scalar, an array, a const or an enum
// over one, a bare bound, a `not`, or the `true` schema every value satisfies --
// admits documents the schema permits and encoding/json refuses before a branch
// is ever consulted. {"anyOf":[{"type":"object","required":["k"],...},
// {"type":"string"}]} rejected "x", which is issue #133's first table.
//
// Two shapes are deliberately left with the merge. A `false` branch admits
// nothing at all, so it can carry no document out of the struct; it is still an
// under-enforcement, and that half is settled by the runtime check rather than
// by moving the type. And a branch keyed on object shape -- one stating a
// required key or a type or allowed value for a property it names -- is the
// shape the merge and its summary were written for.
//
// Reading `required` and `properties` as naming an object is not what the spec
// says, since a scalar satisfies both vacuously; it is what this generator has
// always done for any schema carrying them, at a root as much as here, and
// narrowing that belongs to the keyword rather than to this composition. A bare
// `"type":"object"` gets no such exemption: it says nothing about which objects,
// so the branch admits every object the merged struct's own field types refuse.
func (g *Generator) anyOfMergeCannotHoldBranches(s *schema.Schema) bool {
	if s == nil || len(s.AnyOf) == 0 || !g.validationKeywordsEnabled() {
		return false
	}
	for _, sub := range s.AnyOf {
		resolved := g.resolveSchemaForApplicator(sub)
		if resolved == nil {
			continue
		}
		if resolved.IsBooleanSchema() {
			// `false` admits nothing, so nothing escapes the struct through it.
			// `true` admits every document, including every scalar.
			if resolved.IsTrueSchema() {
				return true
			}
			continue
		}
		if g.anyOfBranchKeyedOnObjectShape(resolved) {
			continue
		}
		return true
	}
	return false
}

// anyOfSummaryCannotJudgeBranches reports whether the ObjectAnyOf summary would
// be dropped for branches that do constrain the document.
//
// objectAnyOfDefFromVariants answers nil as soon as one branch states neither a
// required key nor a property check, and the comment on the caller reads that as
// "this branch matches any object, so the group is unconstrained". That is true
// of `true` and of {}, and false of everything else that reaches it -- a `false`
// branch matches nothing, a const branch matches one document, a `not` matches
// the complement of another schema. Dropping the group for one of those drops
// the only check standing between the type and a document no branch admits:
// {"anyOf":[{"type":"object","required":["k"],...},false]} accepted {"j":"b"}
// on the strength of the branch written to reject everything (issue #133).
//
// acceptsEveryValue is what tells the two apart, and it is the same question
// #126 asks before naming a constraint-only position. A group holding such a
// branch is genuinely satisfied by every document and keeps today's answer,
// which is no check at all.
func (g *Generator) anyOfSummaryCannotJudgeBranches(subs []*schema.Schema) bool {
	if len(subs) == 0 {
		return false
	}
	if g.objectAnyOfDefFromVariants(subs) != nil {
		return false
	}
	for _, sub := range subs {
		if g.acceptsEveryValue(sub, 0, map[*schema.Schema]bool{}) {
			return false
		}
	}
	return true
}

// generateOneOfForProperty creates a OneOfDef for a property with oneOf variants.
// It handles the special case of oneOf with null (becomes pointer type instead).
func (g *Generator) generateOneOfForProperty(parentName, jsonName, goFieldName string, s *schema.Schema) (*OneOfDef, error) {
	// Special case: oneOf with exactly one non-null variant → pointer type.
	// e.g., oneOf: [{$ref: "#/$defs/Foo"}, {type: "null"}] → *Foo
	nonNullVariants, hasNull := g.separateNullFromOneOf(s.OneOf)
	if hasNull && len(nonNullVariants) == 1 {
		// This will be handled as a regular nullable pointer field, not a oneOf.
		return nil, nil
	}

	// Build the oneOf definition with sealed interface pattern.
	interfaceName := ToOneOfInterfaceName(parentName, goFieldName)

	var variants []OneOfVariant
	// Name occurrences are tracked per parent type, not per oneOf group. Each
	// variant name becomes a wrapper type (Parent_Name) and a method
	// (Parent.GetName), both of which live on the parent rather than inside the
	// group, so two groups on one struct claiming the same name — which two
	// scalar oneOf properties do immediately, since primitive variants are all
	// called String / Integer / Number / Boolean — emitted a redeclared type and
	// a redeclared method. Widening the scope of the existing suffix mechanism
	// resolves that the same way a duplicate inside one group is resolved.
	usedNames := g.oneOfMemberNames[parentName]
	if usedNames == nil {
		if g.oneOfMemberNames == nil {
			g.oneOfMemberNames = make(map[string]map[string]int)
		}
		usedNames = make(map[string]int)
		g.oneOfMemberNames[parentName] = usedNames
	}
	for i, variant := range nonNullVariants {
		result, err := g.resolveOneOfVariant(variant, parentName, goFieldName, i)
		if err != nil {
			return nil, err
		}

		// Deduplicate variant names: if we've already seen this name, append an index.
		name := result.Name
		if count, exists := usedNames[name]; exists {
			name = fmt.Sprintf("%s%d", name, count+1)
		}
		usedNames[result.Name]++

		wrapperName := ToOneOfWrapperName(parentName, name)

		checks := oneOfVariantChecks(variant, result.Type)
		variants = append(variants, OneOfVariant{
			WrapperName:    wrapperName,
			FieldName:      name,
			Type:           result.Type,
			RequiredFields: result.RequiredFields,
			Checks:         checks,
			FullyChecked:   oneOfVariantFullyChecked(variant, result.Type, result.RequiredFields, checks),
			IntegerDecode:  g.integerDecodeFor(result.Type, variant),
		})
	}

	oneOfDef := &OneOfDef{
		InterfaceName: interfaceName,
		FieldName:     goFieldName,
		JSONName:      jsonName,
		Variants:      variants,
	}

	// Try to detect or apply a discriminator for more efficient dispatch.
	g.applyDiscriminator(oneOfDef, s, nonNullVariants)

	return oneOfDef, nil
}

// applyDiscriminator attempts to set discriminator info on a OneOfDef.
// It checks for:
// 1. Explicit OpenAPI-style "discriminator" keyword on the schema
// 2. Heuristic: all variants share a property with distinct const/enum values
func (g *Generator) applyDiscriminator(oneOfDef *OneOfDef, s *schema.Schema, variants []*schema.Schema) {
	if len(variants) < 2 {
		return
	}

	// 1. Explicit discriminator keyword
	if s.Discriminator != nil && s.Discriminator.PropertyName != "" {
		propName := s.Discriminator.PropertyName
		discMap := make(map[string]int)

		if len(s.Discriminator.Mapping) > 0 {
			// Mapping values are either a $ref ("#/$defs/Dog") or a bare schema
			// name ("Dog"). Match a $ref variant on its ref, then fall back to
			// the generated Go type name so that name-form values and inline
			// (ref-less) variants resolve too — matching only on EffectiveRef
			// silently left those unmapped.
			//
			// Keys are visited in sorted order and each variant is claimed at
			// most once: with map iteration order, two mapping values that can
			// match the same variant produced different output run to run.
			claimed := make(map[int]bool, len(variants))
			for _, discValue := range sortedMappingKeys(s.Discriminator.Mapping) {
				ref := s.Discriminator.Mapping[discValue]
				if ref == "" {
					continue
				}
				wantName := refToGoName(ref)
				for i, variant := range variants {
					if claimed[i] {
						continue
					}
					if !variantMatchesMapping(variant, oneOfDef.Variants[i], ref, wantName) {
						continue
					}
					claimed[i] = true
					discMap[discValue] = i
					oneOfDef.Variants[i].DiscriminatorValue = discValue
					// Each variant is claimed at most once above, so this is a
					// single-element set; the template reads DiscriminatorValues.
					oneOfDef.Variants[i].DiscriminatorValues = []string{discValue}
					break
				}
			}
		} else {
			// No mapping — try to infer from const/enum in each variant's discriminator property
			g.inferDiscriminatorValues(oneOfDef, variants, propName, discMap)
		}

		if len(discMap) == len(variants) {
			// Successfully mapped all variants
			oneOfDef.DiscriminatorField = propName
			oneOfDef.DiscriminatorMap = discMap
			return
		}
	}

	// 2. Heuristic detection: find a shared property with distinct const/enum values
	g.detectHeuristicDiscriminator(oneOfDef, variants)
}

// sortedMappingKeys returns the discriminator mapping's keys in a stable order
// so that generation is deterministic.
func sortedMappingKeys(mapping map[string]string) []string {
	keys := make([]string, 0, len(mapping))
	for k := range mapping {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// variantMatchesMapping reports whether a oneOf variant is the one an OpenAPI
// discriminator mapping value names. ref is the raw mapping value and wantName
// its Go-name form.
func variantMatchesMapping(variant *schema.Schema, generated OneOfVariant, ref, wantName string) bool {
	if variantRef := variant.EffectiveRef(); variantRef != "" {
		if variantRef == ref || refToGoName(variantRef) == wantName {
			return true
		}
	}
	// Name form, or an inline variant that has no ref to compare against: the
	// generated Go type carries the name the mapping refers to. Object variants
	// are pointer-wrapped (*Dog), so compare against the pointee.
	if wantName != "" && generated.Type != nil {
		if strings.TrimPrefix(generated.Type.GoTypeName(), "*") == wantName {
			return true
		}
	}
	return false
}

// inferDiscriminatorValues extracts discriminator values from each variant's property.
// It looks for const or single-value enum on the discriminator property.
func (g *Generator) inferDiscriminatorValues(oneOfDef *OneOfDef, variants []*schema.Schema, propName string, discMap map[string]int) {
	for i, variant := range variants {
		resolved := g.resolveVariantSchema(variant)
		if resolved == nil {
			return
		}
		propSchema, ok := resolved.Properties[propName]
		if !ok {
			return
		}
		val := extractDiscriminatorValue(propSchema)
		if val == "" {
			return
		}
		// Check for duplicate values
		if _, exists := discMap[val]; exists {
			return
		}
		discMap[val] = i
		oneOfDef.Variants[i].DiscriminatorValue = val
		oneOfDef.Variants[i].DiscriminatorValues = []string{val}
	}
}

// detectHeuristicDiscriminator looks for a shared property across all variants
// where each variant has a distinct const or single-value enum for that property
// AND requires that property.
//
// The "required in every variant" condition matters for correctness: the
// generated discriminator-based UnmarshalJSON demands the property be present.
// A const on an optional property only constrains it when present, so keying
// dispatch on an optional property would reject objects that legitimately omit
// it. When the property is not required in all variants, no discriminator is
// detected and unmarshaling falls back to the try-each-variant path (gated by
// each variant's required fields), which handles that case correctly.
//
// Candidate properties are examined in sorted order so the chosen field is
// deterministic when more than one qualifies.
func (g *Generator) detectHeuristicDiscriminator(oneOfDef *OneOfDef, variants []*schema.Schema) {
	// Collect resolved schemas for all variants. A variant may itself be a
	// oneOf (no properties of its own); it is discriminable when every one
	// of its sub-variants is, contributing the union of their values.
	resolvedVariants := make([]*schema.Schema, len(variants))
	for i, v := range variants {
		resolved := g.resolveVariantSchema(v)
		if resolved == nil {
			return
		}
		resolvedVariants[i] = resolved
	}

	// Candidate properties come from the first variant (descending into a
	// nested oneOf's first sub-variant when it has no properties of its own).
	for _, propName := range g.discriminatorCandidateProps(resolvedVariants[0], 0) {
		seenValues := make(map[string]int)
		valueSets := make([][]string, len(resolvedVariants))
		valid := true

		for i, resolved := range resolvedVariants {
			vals := g.discriminatorValuesForVariant(resolved, propName, 0)
			if len(vals) == 0 {
				valid = false
				break
			}
			for _, val := range vals {
				if _, dup := seenValues[val]; dup {
					valid = false
					break
				}
				seenValues[val] = i
			}
			if !valid {
				break
			}
			valueSets[i] = vals
		}

		if valid {
			// Found a valid heuristic discriminator
			oneOfDef.DiscriminatorField = propName
			oneOfDef.DiscriminatorMap = seenValues
			for i, vals := range valueSets {
				oneOfDef.Variants[i].DiscriminatorValue = vals[0]
				oneOfDef.Variants[i].DiscriminatorValues = vals
			}
			return
		}
	}
}

// variantRequiresProperty reports whether the schema lists prop in its required array.
func variantRequiresProperty(s *schema.Schema, prop string) bool {
	for _, r := range s.Required {
		if r == prop {
			return true
		}
	}
	return false
}

// resolveVariantSchema resolves a variant schema (following $ref if needed) to get
// its concrete properties for discriminator detection.
func (g *Generator) resolveVariantSchema(variant *schema.Schema) *schema.Schema {
	if effRef := variant.EffectiveRef(); effRef != "" {
		resolved := g.resolveRefInContext(effRef, variant)
		if resolved != nil {
			return resolved
		}
		return nil
	}
	return variant
}

// extractDiscriminatorValue extracts a single string discriminator value from a property schema.
// It recognizes: {"const": "value"} or {"enum": ["single_value"]}.
func extractDiscriminatorValue(propSchema *schema.Schema) string {
	if propSchema == nil {
		return ""
	}
	// Check const
	if propSchema.Const != nil {
		if s, ok := (*propSchema.Const).(string); ok {
			return s
		}
	}
	// Check single-value enum
	if len(propSchema.Enum) == 1 {
		if s, ok := propSchema.Enum[0].(string); ok {
			return s
		}
	}
	return ""
}

// hasPropertyOneOf reports whether any of the groups is a oneOf on a property
// (it carries a JSON name) rather than one standing for the type itself.
func hasPropertyOneOf(oneOfs []OneOfDef) bool {
	for _, o := range oneOfs {
		if o.JSONName != "" {
			return true
		}
	}
	return false
}

// oneOfVariantChecks returns the constraints a oneOf variant declares that the
// union's UnmarshalJSON can test directly against the decoded candidate.
//
// Variant selection otherwise asks only whether the raw JSON decodes into the
// variant's Go type, and nothing downstream makes up for it: the wrapper holds a
// plain Go string or int64, which carries no Validate, and the parent's Validate
// does not descend into the union. So {"oneOf":[{"type":"string","minLength":3},
// {"type":"integer","minimum":5}]} accepted "z" — it decodes as a string, and
// minLength was never consulted anywhere. Testing the branch here is also what
// gives oneOf's "exactly one" its meaning when two branches share a Go type,
// which decodability alone cannot distinguish at all.
//
// Only keywords with a direct expression over the candidate's Go type are kept,
// and only for the scalar and array types the union materializes by value. A
// variant that resolved to `any` (a constraint-only branch) or to a named type
// (a $ref or an inline object, whose own type carries the constraints) gets
// none.
func oneOfVariantChecks(variant *schema.Schema, goType GoType) []ValidationRule {
	if variant == nil || goType == nil {
		return nil
	}
	var kind string
	switch t := goType.(type) {
	case *PrimitiveType:
		switch t.Name {
		case "string":
			kind = "string"
		case "int64", "float64":
			kind = "number"
		}
	case *ArrayType:
		kind = "array"
	}
	if kind == "" {
		return nil
	}
	var checks []ValidationRule
	for _, r := range extractValidationRules("", "", variant) {
		switch r.RuleType {
		case "minLength", "maxLength", "pattern":
			if kind == "string" {
				checks = append(checks, r)
			}
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf":
			if kind == "number" {
				checks = append(checks, r)
			}
		case "minItems", "maxItems":
			if kind == "array" {
				checks = append(checks, r)
			}
		}
	}
	return checks
}

// oneOfSelectableKeywords are the keywords a oneOf branch may carry for
// selection alone to decide whether the branch is satisfied.
//
// `required` is here because selection tests it directly, through the presence
// gate built from the branch's required list. `type` is here because the Go type
// the candidate decodes into can settle it. The bounds keywords are here because
// Checks tests them against that candidate. Everything else -- properties,
// $ref, enum, const, format, allOf, not -- is either enforced by the variant
// type's own Validate or not enforced at all, and neither is something selection
// can claim to have decided.
var oneOfSelectableKeywords = map[string]bool{
	"type": true, "required": true,
	"minimum": true, "maximum": true,
	"exclusiveMinimum": true, "exclusiveMaximum": true, "multipleOf": true,
	"minLength": true, "maxLength": true, "pattern": true,
	"minItems": true, "maxItems": true,

	"$schema": true, "$id": true, "title": true, "description": true,
	"$comment": true, "default": true, "examples": true,
	"deprecated": true, "readOnly": true, "writeOnly": true,
}

// oneOfVariantFullyChecked reports whether selection decides this branch on its
// own, so that a candidate which matched has satisfied the branch rather than
// merely decoded as it.
//
// The answer is yes only when every keyword the branch states is answered by
// something selection actually emits: the presence gate over requiredFields, the
// Go type the candidate decodes into, or one of checks. A keyword that is
// vacuous for that Go type is answered too -- it is satisfied by every value the
// candidate can hold.
//
// It fails closed. A branch whose type resolved to `any` while stating anything
// beyond `required` is not decided, and neither is one carrying a keyword this
// function has not been taught about: the marshaled key set is what is
// consulted, so a keyword the parser learns later arrives here as unknown rather
// than as absent.
func oneOfVariantFullyChecked(variant *schema.Schema, goType GoType, requiredFields []string, checks []ValidationRule) bool {
	if variant == nil {
		return false
	}
	if variant.IsTrueSchema() {
		return true
	}
	if variant.IsBooleanSchema() {
		// `false` matches nothing, yet selection decodes it into `any` and
		// counts it as matched. Refusing to speak for it is what takes the whole
		// group off the union path — see oneOfBranchOutrunsSelection, which
		// reads this answer — and claiming the branch was decided would put it
		// back with the defect intact (issue #125).
		return false
	}
	if len(variant.Extensions) > 0 || len(variant.TypeSchemas) > 0 {
		return false
	}
	raw, err := json.Marshal(variant)
	if err != nil {
		return false
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
		return false
	}
	for key := range present {
		if !oneOfSelectableKeywords[key] {
			return false
		}
	}
	if !sameStringSet(variant.Required, requiredFields) {
		return false
	}
	if len(variant.Type) > 0 {
		kind := jsonKindForGoType(goType)
		if kind == "" || branchTypeVerdict(variant.Type, kind) != typeVerdictAlways {
			return false
		}
	}
	checked := make(map[string]bool, len(checks))
	for _, c := range checks {
		checked[c.RuleType] = true
	}
	for _, r := range extractValidationRules("", "", variant) {
		if ruleVacuousForType(goType, r.RuleType) {
			continue
		}
		if !checked[r.RuleType] {
			return false
		}
	}
	return true
}

// sameStringSet reports whether two lists name the same set of strings,
// ignoring order and repetition.
func sameStringSet(a, b []string) bool {
	left := make(map[string]struct{}, len(a))
	for _, v := range a {
		left[v] = struct{}{}
	}
	right := make(map[string]struct{}, len(b))
	for _, v := range b {
		right[v] = struct{}{}
	}
	if len(left) != len(right) {
		return false
	}
	for v := range left {
		if _, ok := right[v]; !ok {
			return false
		}
	}
	return true
}

// oneOfVariantResult holds the result of resolving a oneOf variant.
type oneOfVariantResult struct {
	Name           string
	Type           GoType
	RequiredFields []string
}

// resolveOneOfVariant determines the name, type, and required fields for a oneOf variant.
// The index parameter is used to disambiguate inline variants with the same structure.
func (g *Generator) resolveOneOfVariant(variant *schema.Schema, parentName, fieldName string, index int) (oneOfVariantResult, error) {
	// Boolean schemas → treat as any
	if variant.IsBooleanSchema() {
		if variant.IsTrueSchema() {
			return oneOfVariantResult{Name: "Any", Type: &PrimitiveType{Name: "any"}}, nil
		}
		// false schema — nothing matches, but include for completeness
		return oneOfVariantResult{Name: "None", Type: &PrimitiveType{Name: "any"}}, nil
	}

	// $ref / $recursiveRef / $dynamicRef variant → use the referenced type
	if effRef := variant.EffectiveRef(); effRef != "" {
		goName := refToGoName(effRef)
		refSchema := g.resolveRefInContext(effRef, variant)
		if refSchema != nil {
			// A ref into a document owned by another package of this run
			// references that package's type instead of materializing a copy.
			// The variant name carries the package to stay unique when
			// several packages export same-named types (field.Element,
			// header.Element → FieldElement, HeaderElement).
			if foreign, ok := g.foreignTypeFor(refSchema); ok {
				foreign.Pointer = true
				return oneOfVariantResult{
					Name:           SchemaNameToGoName(foreign.PkgAlias) + foreign.Name,
					Type:           foreign,
					RequiredFields: refSchema.Required,
				}, nil
			}
			goName = g.goNameForResolvedRef(effRef, refSchema, goName)
			// Definitions in different documents may share a name (each
			// document declaring e.g. #/definitions/element); without
			// disambiguation every variant would silently reuse whichever
			// type claimed the name first.
			goName = g.uniqueTypeName(goName, refSchema)
			if err := g.generateTypeDef(goName, refSchema); err != nil {
				return oneOfVariantResult{}, err
			}
			return oneOfVariantResult{
				Name:           goName,
				Type:           &NamedType{Name: goName, Pointer: true},
				RequiredFields: refSchema.Required,
			}, nil
		}
		return oneOfVariantResult{
			Name: goName,
			Type: &NamedType{Name: goName, Pointer: true},
		}, nil
	}

	// Inline object variant → create a named type, disambiguated by index.
	// The predicate is objectShapeNeedsNamedType rather than hasProperties: a
	// branch whose whole shape is patternProperties, or a schema-valued
	// additionalProperties, describes an object just as much as one that
	// declares property names. The primitive arm below answers map[string]any
	// for those -- a variant with no Validate for the union's dispatch to call,
	// so the branch's own keywords decide nothing.
	if g.objectShapeNeedsNamedType(variant) {
		variantName := fmt.Sprintf("%s%sOption%d", parentName, fieldName, index)
		if variant.Title != "" {
			variantName = SchemaNameToGoName(variant.Title)
		}
		if !g.generated[variantName] {
			if err := g.generateTypeDef(variantName, variant); err != nil {
				return oneOfVariantResult{}, err
			}
		}
		return oneOfVariantResult{
			Name:           variantName,
			Type:           &NamedType{Name: variantName, Pointer: true},
			RequiredFields: variant.Required,
		}, nil
	}

	// A variant whose whole schema is an allOf. It states no type of its own, so
	// every arm above declines it and the fallback below answers `any` -- which
	// carries no Validate, so the branch's constraints are enforced nowhere and
	// selection cannot tell it from any other untyped branch. The merge is what
	// knows the type; name the variant after it, as the inline-object arm does
	// one type up. See allOfNeedsNamedType.
	if g.allOfNeedsNamedType(variant) {
		variantName := fmt.Sprintf("%s%sOption%d", parentName, fieldName, index)
		if variant.Title != "" {
			variantName = SchemaNameToGoName(variant.Title)
		}
		if !g.generated[variantName] {
			if err := g.generateTypeDef(variantName, variant); err != nil {
				return oneOfVariantResult{}, err
			}
		}
		if g.generated[variantName] {
			return oneOfVariantResult{
				Name:           variantName,
				Type:           &NamedType{Name: variantName},
				RequiredFields: variant.Required,
			}, nil
		}
	}

	// A branch whose value is a formatted string that no Go primitive can carry
	// the assertion for: a format stated without a "type", or the nullable
	// spelling of one. The arms below answer `any` for the first and a bare
	// string for the second -- and the string rejects the null the branch
	// permits, while neither carries a Validate for the union's dispatch to
	// call, so the branch is both over- and under-enforced at once. See
	// stringAnnotationOnlySchema and nullableFormatUnion.
	// A declared string with a format joins them: the primitive arm below
	// answers a bare Go string, which carries no Validate, so
	// {"oneOf":[{"type":"string","format":"ipv4"}, ...]} accepted an IPv6
	// address through that branch while the identical subschema behind a $ref
	// was checked. Naming it gives the union's dispatch something to call, which
	// is what every other branch shape already has.
	if g.stringAnnotationOnlySchema(variant) || g.nullableFormatUnion(variant) || g.declaredFormatStringSchema(variant) {
		variantName := fmt.Sprintf("%s%sOption%d", parentName, fieldName, index)
		if variant.Title != "" {
			variantName = SchemaNameToGoName(variant.Title)
		}
		if !g.generated[variantName] {
			if err := g.generateTypeDef(variantName, variant); err != nil {
				return oneOfVariantResult{}, err
			}
		}
		if g.generated[variantName] {
			return oneOfVariantResult{
				Name: variantName,
				Type: &NamedType{Name: variantName},
			}, nil
		}
	}

	// Primitive variant
	pt := primarySchemaType(variant)
	if pt != "" {
		goType := PrimitiveTypeFromSchema(pt)
		if goType != nil {
			goName := SchemaNameToGoName(pt)
			return oneOfVariantResult{Name: goName, Type: goType}, nil
		}
	}

	// Constraint-only or empty schema — fall back to any, but preserve required fields
	// for discrimination (e.g. oneOf variants that differ only by required constraints).
	return oneOfVariantResult{Name: "Any", Type: &PrimitiveType{Name: "any"}, RequiredFields: variant.Required}, nil
}

// separateNullFromOneOf splits oneOf variants into non-null variants and a null flag.
func (g *Generator) separateNullFromOneOf(variants []*schema.Schema) ([]*schema.Schema, bool) {
	var nonNull []*schema.Schema
	hasNull := false

	for _, v := range variants {
		if len(v.Type) == 1 && v.Type[0] == "null" {
			hasNull = true
			continue
		}
		nonNull = append(nonNull, v)
	}

	return nonNull, hasNull
}

// nullableCollapseCarriesTheBranch reports whether the pointer produced by
// collapsing {X, {"type":"null"}} to X's own Go type still tests everything X
// asserts.
//
// Three arms make that collapse -- the two in resolvePropertyType and the anyOf
// one in generateTypeDef -- and each of them resolves X alone and wraps the
// answer in a pointer. Nothing else reads X. The keywords a property normally
// carries on its parent's field, and the element checks an array normally
// carries beside it, are both read from the *property* schema, which here is the
// {X, "null"} wrapper and states none of them. So the collapse keeps whatever X's
// own type carries and drops the rest, which is issue #150's second half:
// {"anyOf":[{"type":"string","const":"a"},{"type":"null"}]} came out *string and
// accepted "b", and so did the same schema written with a minLength, a pattern,
// a minItems, or an element bound.
//
// A named type is X's own type, and its Validate is where X's keywords went, so
// nothing is lost. A bare string, int64, bool, []T or map[string]T carries the
// shape and nothing else, and then X may state nothing but that shape.
//
// A boolean branch is read for what it admits rather than for what it states.
// `true` admits every document, so the collapse loses nothing; `false` admits
// none, and the pointer to `any` it collapses to admits them all.
func (g *Generator) nullableCollapseCarriesTheBranch(branch *schema.Schema, inner GoType) bool {
	if branch == nil || !g.validationKeywordsEnabled() {
		return true
	}
	if branch.IsBooleanSchema() {
		return branch.IsTrueSchema()
	}
	// A branch that admits no instance at all, however it is spelled. It is asked
	// first because the two spellings the keyword list cannot show are here --
	// `"enum": []` is dropped from the marshaled form by omitempty, and a `type`
	// that filters every member out leaves the list non-empty -- and because the
	// answer does not depend on the Go type: no value satisfies the branch, and
	// every value satisfies the pointer.
	if g.schemaForbidsEveryValue(branch) {
		return false
	}
	if goTypeIsGenerated(inner) {
		return true
	}
	return !g.schemaStatesMoreThanItsGoType(branch, inner, 0)
}

// nullableAliasCarriesTheBranch is nullableCollapseCarriesTheBranch asked at the
// one site that answers with an alias rather than with a field: the anyOf arm of
// generateTypeDef, whose reply is `type X *T`.
//
// A struct field typed *T is validated through T -- populateValidatableFields
// finds the method and the parent calls it, which is why a branch that
// materializes a type loses nothing at a property. An alias has no such caller.
// Go forbids a method on a type whose underlying type is a pointer, so X gets no
// Validate at all, and a field typed by a $ref to X gets one that is not there
// either: {"$defs":{"N":{"anyOf":[{"type":"object","required":["k"]},
// {"type":"null"}]}}} accepted {} through a property referring to N.
//
// So a branch with a type of its own is the worst case here rather than the
// exempt one, and only a branch whose checks were never going anywhere -- a
// plain string, a plain []T -- may keep the alias.
func (g *Generator) nullableAliasCarriesTheBranch(branch *schema.Schema, inner GoType) bool {
	if goTypeIsGenerated(inner) {
		return false
	}
	return g.nullableCollapseCarriesTheBranch(branch, inner)
}

// nullableCollapseNamedType gives a {X, {"type":"null"}} property a name of its
// own when the pointer the collapse would produce leaves X's assertions untested,
// and reports whether it did.
//
// It is the property-position counterpart of the diversion the anyOf arm of
// generateTypeDef makes, and it asks the same question of the same function: the
// evaluator judges every branch against the raw value, so it needs no Go type per
// branch and the null branch costs it nothing.
//
// rawWrapperDef is asked directly rather than through generateTypeDef because
// the two spellings would not arrive at the same place. generateTypeDef reads a
// oneOf whose branch describes an object on the struct path, which flattens the
// branch into ObjectOneOfs and drops what does not fit that summary --
// {"oneOf":[{"type":"object","additionalProperties":{"minLength":3}},
// {"type":"null"}]} accepted {"k":"a"} through it. The wrapper reads the whole
// schema or declines.
//
// Nothing is claimed when it declines: the caller keeps today's pointer, which
// is wrong about this branch but still gives the field the branch's Go type,
// where a name with no Validate would give neither.
func (g *Generator) nullableCollapseNamedType(s, branch *schema.Schema, inner GoType, parentName, fieldName string) (GoType, bool) {
	if g.nullableCollapseCarriesTheBranch(branch, inner) {
		return nil, false
	}
	nestedName := parentName + fieldName
	if nestedName == "" || g.generated[nestedName] || g.generating[nestedName] || g.nodesInProgress[s] {
		return nil, false
	}
	def := g.rawWrapperDef(nestedName, s)
	if def == nil {
		return nil, false
	}
	g.generated[nestedName] = true
	g.output.TypeDefs = append(g.output.TypeDefs, def)
	return &NamedType{Name: nestedName}, true
}

// goTypeIsGenerated reports whether t names a type this run defines, whose own
// Validate is where the schema it was built from went. A primitive, a slice or a
// map carries its shape and no check.
func goTypeIsGenerated(t GoType) bool {
	switch v := t.(type) {
	case *NamedType:
		return true
	case *PointerType:
		return goTypeIsGenerated(v.Inner)
	}
	return false
}

// schemaStatesMoreThanItsGoType reports whether s asserts anything the bare Go
// type t does not already express.
//
// "type" is what t is. "items" is what t's element type is, and an element is
// resolved by these same rules, so the question recurses there and stops at the
// first keyword neither answers. Everything else -- a bound, a pattern, a const,
// an enum, a format, additionalProperties, a composition, a `not` -- is a check
// that has to be written somewhere, and a plain Go type has nowhere to write it.
//
// The keyword list is read off the re-marshaled schema by unenforcedKeywords, so
// a keyword the parser learns later arrives here as one more thing t does not
// express rather than as one that was never there.
func (g *Generator) schemaStatesMoreThanItsGoType(s *schema.Schema, t GoType, depth int) bool {
	if s == nil {
		return false
	}
	if depth > maxRuntimeDepth {
		return true
	}
	// {"const":null} is spelled with a field the marshaler skips, so the keyword
	// list below cannot show it. Read directly, like the two evaluators do.
	if s.ConstIsNull {
		return true
	}
	for _, key := range unenforcedKeywords(s) {
		if key == "type" {
			continue
		}
		if key != "items" {
			return true
		}
		// A single items schema over a slice: the element type answers for it.
		// A tuple (draft-7 `items` as an array) does not reduce to one element
		// type, so it is left with everything else.
		at, ok := t.(*ArrayType)
		if !ok || s.Items == nil || s.Items.Schema == nil || len(s.Items.Schemas) > 0 {
			return true
		}
		if goTypeIsGenerated(at.ItemType) {
			continue
		}
		if g.schemaStatesMoreThanItsGoType(s.Items.Schema, at.ItemType, depth+1) {
			return true
		}
	}
	return false
}

// generateEnumDef produces an EnumDef from an enum schema.
func (g *Generator) generateEnumDef(name string, s *schema.Schema) error {
	g.generated[name] = true

	// A member the schema's own "type" forbids is a member no instance can ever
	// equal, so it is dropped before anything reads the list. This has to happen
	// first: everything below -- the heterogeneity test, the base type, the
	// constant names, the emitted `case` arms -- answers from the members, and
	// each of them answered from members that were never admissible.
	//
	// {"type":"string","enum":["a",5]} then keeps "a" and becomes a string enum,
	// where it used to be a raw enum listing 5 as well, and accepted 5.
	//
	// When nothing survives, the schema admits no instance at all -- a value
	// cannot be both a string and 5 -- and the answer is the wrapper
	// generateTypeDef gives the boolean `false` schema and `{"enum":[]}`. That is
	// issue #145, whose symptom was the const emitted against the type the schema
	// declares: {"type":"string","const":5} came out `const Root string = 5`,
	// which does not compile. See declaredTypeAdmitsNoEnumMember.
	if kept, filtered := g.enumMembersDeclaredTypeAdmits(s); filtered {
		if len(kept) == 0 {
			g.output.TypeDefs = append(g.output.TypeDefs, &NotSchemaDef{
				Name:        name,
				Description: s.Description,
				IsForbidden: true,
			})
			return nil
		}
		narrowed := *s
		narrowed.Enum = kept
		s = &narrowed
	}

	// Check if the enum contains non-primitive or mixed-type values.
	// If so, generate a json.RawMessage-based "raw" enum instead of const-based.
	if isHeterogeneousEnum(s.Enum) {
		return g.generateRawEnumDef(name, s)
	}

	baseType := g.resolveBaseType(s)

	// The const form declares one Go constant per member against baseType, so a
	// member that is not a constant of that type is a build failure rather than a
	// wrong answer -- `const Root string = 5`, or `invalid constant type Root`
	// where the base is a map. The raw form holds any member at all, because it
	// compares the JSON encodings, so it is what a mismatch falls back to.
	//
	// The filter above removes the whole of this in every case it applies to, and
	// this is the residue it cannot reach: a schema whose "type" asserts nothing
	// because a draft 3-7 $ref displaces it, and a type name that maps to no Go
	// type of its own (draft 3's "any"). Both used to reach the const form with a
	// member it could not hold.
	if !enumFitsConstForm(baseType, s.Enum) {
		return g.generateRawEnumDef(name, s)
	}

	constNames := enumConstNames(name, s.Enum)
	values := make([]EnumValue, len(s.Enum))
	for i, v := range s.Enum {
		values[i] = EnumValue{
			Name:  constNames[i],
			Value: v,
		}
	}

	g.output.TypeDefs = append(g.output.TypeDefs, &EnumDef{
		Name:        name,
		BaseType:    baseType,
		Values:      values,
		Description: s.Description,
	})
	return nil
}

// isHeterogeneousEnum returns true if the enum values contain non-primitive
// types (arrays, objects, null) or a mix of different primitive types.
// Such enums cannot be represented as Go typed constants.
func isHeterogeneousEnum(values []any) bool {
	if len(values) == 0 {
		return false
	}
	var seenType string
	for _, v := range values {
		switch v.(type) {
		case string:
			if seenType == "" {
				seenType = "string"
			} else if seenType != "string" {
				return true
			}
		case float64:
			if seenType == "" {
				seenType = "float64"
			} else if seenType != "float64" {
				return true
			}
		case bool:
			if seenType == "" {
				seenType = "bool"
			} else if seenType != "bool" {
				return true
			}
		default:
			// nil (null), []any (array), map[string]any (object)
			return true
		}
	}
	return false
}

// canonicalJSONValues renders enum members to the form an instance is compared
// against: the JSON encoding of the decoded value, which is what makes the
// comparison sound at all. An enum member and an instance are equal as JSON
// *documents*, not as byte strings -- {"a":1,"b":2} and {"b":2,"a":1} are the
// same document, and so are 1, 1.0 and 1e0 -- so both sides are put through one
// encoder before being compared. encoding/json writes a map's keys in sorted
// order and a number in one format, which settles both.
//
// This is the form generateRawEnumDef already stores and the form its Validate
// already computes for the instance. Sharing it is what keeps a whole-document
// enum answering the same way whether the merge left it a standalone raw enum
// type or a struct that carries the enum itself.
//
// A member that cannot be encoded is dropped rather than compared as its Go
// rendering: an entry no instance could ever equal would turn the enum into a
// rejection of documents the schema admits.
func canonicalJSONValues(values []any) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		out = append(out, string(b))
	}
	if len(out) != len(values) {
		return nil
	}
	return out
}

// generateRawEnumDef generates a json.RawMessage-based enum for heterogeneous
// enum values that cannot be represented as Go typed constants.
func (g *Generator) generateRawEnumDef(name string, s *schema.Schema) error {
	constNames := enumConstNames(name, s.Enum)
	values := make([]EnumValue, len(s.Enum))
	for i, v := range s.Enum {
		rawBytes, err := json.Marshal(v)
		if err != nil {
			rawBytes = []byte(fmt.Sprintf("%v", v))
		}
		values[i] = EnumValue{
			Name:    constNames[i],
			Value:   v,
			RawJSON: string(rawBytes),
		}
	}

	g.output.TypeDefs = append(g.output.TypeDefs, &EnumDef{
		Name:        name,
		BaseType:    &PrimitiveType{Name: "json.RawMessage"},
		Values:      values,
		Description: s.Description,
		IsRaw:       true,
	})
	return nil
}

// resolvePropertyType determines the GoType for a property schema, creating
// additional TypeDefs for nested objects. The ctxSchema is the parent schema
// that contains this property, used for scoped $ref resolution.
func (g *Generator) resolvePropertyType(s *schema.Schema, parentName, fieldName string, ctxSchema *schema.Schema) (GoType, error) {
	if s == nil {
		return &PrimitiveType{Name: "any"}, nil
	}

	// A property whose schema *is* the node being generated further up the stack
	// closes a cycle, and several arms below would re-enter generateTypeDef for
	// it under a name one segment longer than the last.
	//
	// A $ref carrying a structural sibling is the cheapest way to build one:
	// {"properties":{"a":{"$ref":"#","items":{"type":"string"}}}} is disqualified
	// from both ref-only arms of generateTypeDef by the sibling -- the arms
	// refCycleAliasDef guards -- so it is merged into an implicit allOf instead;
	// the merge pulls the root's own properties back in, and "a" is re-entered
	// here as RootA, RootAA, RootAAA ... until the stack is gone. 57 bytes of
	// schema took the process down. See cyclicNodeName; a field must be a
	// pointer, since the type would otherwise contain itself by value.
	if canonical, ok := g.cyclicNodeName(s); ok {
		return namedOrPointer(canonical, true), nil
	}

	// Const -> treat as single-element enum for validation purposes.
	// Only promote when no explicit type is specified (the enum is needed to
	// determine the Go type). When type IS specified, keep the natural Go type
	// and rely on the "const" validation rule for enforcement.
	//
	// refDisplacesSiblingValues holds both arms behind the ref arms below on the
	// drafts where the reference displaces what is written beside it (issue #151),
	// and refMergesSiblingValues holds them behind the same arms on the drafts
	// where both bind and only the merge can say so (issue #153).
	refDisplacesEnum := g.refDisplacesSiblingValues(s)
	refMergesEnum := g.refMergesSiblingValues(s)
	if g.validationKeywordsEnabled() && !refDisplacesEnum && !refMergesEnum && len(s.Type) == 0 {
		s = promoteConstToEnum(s)
	}

	// Inline enum → generate enum type
	if g.validationKeywordsEnabled() && !refDisplacesEnum && !refMergesEnum && len(s.Enum) > 0 {
		enumName := parentName + fieldName
		if err := g.generateEnumDef(enumName, s); err != nil {
			return nil, err
		}
		return &NamedType{Name: enumName}, nil
	}

	// A property whose unevaluatedItems can only be settled by running the
	// schema. Claimed before every arm below, because each of them would type
	// the value and leave the keyword behind -- and unlike the static shapes
	// there is no field-level check to fall back on, since which items count as
	// evaluated depends on which branches matched the value in hand.
	if g.inlineAnnotationWrapper(s) {
		nestedName := parentName + fieldName
		if err := g.generateTypeDef(nestedName, s); err != nil {
			return nil, err
		}
		if g.generated[nestedName] {
			return &NamedType{Name: nestedName}, nil
		}
	}

	// oneOf with null + single variant → pointer to the variant type
	if len(s.OneOf) > 0 {
		nonNull, hasNull := g.separateNullFromOneOf(s.OneOf)
		if hasNull && len(nonNull) == 1 {
			variant := nonNull[0]
			if effRef := variant.EffectiveRef(); effRef != "" {
				goName := refToGoName(effRef)
				if refSchema := g.resolveRefInContext(effRef, variant); refSchema != nil {
					if foreign, ok := g.foreignTypeFor(refSchema); ok {
						foreign.Pointer = true
						return foreign, nil
					}
					if err := g.generateTypeDef(goName, refSchema); err != nil {
						return nil, err
					}
				}
				return &PointerType{Inner: &NamedType{Name: goName}}, nil
			}
			// Inline variant
			innerType, err := g.resolvePropertyType(variant, parentName, fieldName, ctxSchema)
			if err != nil {
				return nil, err
			}
			if t, ok := g.nullableCollapseNamedType(s, variant, innerType, parentName, fieldName); ok {
				return t, nil
			}
			if !innerType.IsPointer() {
				return &PointerType{Inner: innerType}, nil
			}
			return innerType, nil
		}
		// A multi-variant oneOf reaches here when the caller declined to render it
		// as a sealed-interface union because the schema asserts more than the
		// union would carry (see oneOfUnionKeepsWholeSchema). When the branches
		// still describe an object, materialize the named type so generateTypeDef
		// builds a struct that keeps both: the schema's own object keywords, and
		// the branches flattened into ObjectOneOfs. Without this the object arm
		// below sees no properties of its own and collapses the whole schema —
		// oneOf, required and all — to map[string]any.
		//
		// hasProperties is excluded because resolveType already materializes a
		// named struct for it; a $ref is excluded so the ref arms keep it.
		if !oneOfUnionKeepsWholeSchema(s) && s.EffectiveRef() == "" && !hasProperties(s) && g.oneOfDescribesObject(s) {
			nestedName := parentName + fieldName
			if err := g.generateTypeDef(nestedName, s); err != nil {
				return nil, err
			}
			return &NamedType{Name: nestedName}, nil
		}
		// A oneOf the caller declined for one of the other two reasons, both of
		// which are about the branches rather than about the siblings: there is
		// nothing to select on (see oneOfIsUnselectableUnion), or what selection
		// would decide disagrees with what a branch says (see
		// oneOfUnionOutrunsBranches). Materialize the named type so
		// generateTypeDef evaluates the branches against the value — as an alias
		// carrying OneOfVariants when a type is declared or inferred, and as the
		// dynamic or runtime wrapper when it is not. Without this the arms below
		// would take the declared type and drop the oneOf entirely.
		if oneOfUnionKeepsWholeSchema(s) && !g.oneOfRendersAsUnion(s) && s.EffectiveRef() == "" && !hasProperties(s) {
			nestedName := parentName + fieldName
			if err := g.generateTypeDef(nestedName, s); err != nil {
				return nil, err
			}
			return &NamedType{Name: nestedName}, nil
		}
	}

	// anyOf with null + single variant → pointer to the variant type (same as oneOf pattern above).
	// Handles patterns like anyOf: [{type: null}, {$ref: "#"}] inside remote schemas
	// where the ref resolves to a primitive type.
	if len(s.AnyOf) > 0 {
		nonNull, hasNull := g.separateNullFromOneOf(s.AnyOf)
		if hasNull && len(nonNull) == 1 {
			variant := nonNull[0]
			// Resolve $ref if present to get the actual target schema.
			effective := variant
			if effRef := variant.EffectiveRef(); effRef != "" {
				if resolved := g.resolveRefInContext(effRef, variant); resolved != nil {
					effective = resolved
				}
			}
			innerType, err := g.resolvePropertyType(effective, parentName, fieldName, ctxSchema)
			if err != nil {
				return nil, err
			}
			if t, ok := g.nullableCollapseNamedType(s, effective, innerType, parentName, fieldName); ok {
				return t, nil
			}
			if !innerType.IsPointer() {
				return &PointerType{Inner: innerType}, nil
			}
			return innerType, nil
		}
	}

	// $ref / $recursiveRef / $dynamicRef
	if effRef := s.EffectiveRef(); effRef != "" {
		if !g.refOverridesSiblingsForSchema(s) && hasRefStructuralSiblings(s) {
			nestedName := parentName + fieldName
			if err := g.generateTypeDef(nestedName, s); err != nil {
				return nil, err
			}
			return &NamedType{Name: nestedName}, nil
		}
		// Self-references (e.g. $ref: "#" or $ref matching root $id).
		if g.isSelfRefInContext(effRef, s) {
			// Only generate *Root if the root schema is explicitly an object type
			// with properties. Otherwise the root can validate non-object values
			// (e.g. numbers, booleans) and we should use json.RawMessage.
			if g.rootIsObjectType() {
				return &PointerType{Inner: &NamedType{Name: g.rootTypeName}}, nil
			}
			return &PrimitiveType{Name: "json.RawMessage"}, nil
		}
		goName := refToGoName(effRef)
		// Ensure the referenced type gets generated.
		refSchema := g.resolveEffectiveRefSchema(s)
		if refSchema != nil {
			if foreign, ok := g.foreignTypeFor(refSchema); ok {
				return foreign, nil
			}
			pushed := g.pushDynamicScope(refSchema)
			goName = g.goNameForResolvedRef(effRef, refSchema, goName)
			if err := g.generateTypeDef(goName, refSchema); err != nil {
				if pushed {
					g.popDynamicScope()
				}
				return nil, err
			}
			if pushed {
				g.popDynamicScope()
			}
			// If the ref resolves to its own enclosing document root, use a pointer.
			if g.isScopedSelfRef(effRef, s, refSchema) {
				return &PointerType{Inner: &NamedType{Name: goName}}, nil
			}
		} else {
			// Ref target could not be resolved (e.g. points to an unknown keyword).
			// Fall back to any to produce compilable code.
			return &PrimitiveType{Name: "any"}, nil
		}
		return &NamedType{Name: goName}, nil
	}

	// Draft 3 allows a schema to sit inside the "type" array as an alternative.
	// The alternatives rarely share a Go type -- the draft-3 meta-schema's
	// "items" is a schema OR an array of schemas -- so materialize the
	// raw-value wrapper that can hold either. Without this the property takes
	// the type of whichever alternative the fallback below happens to pick, and
	// unmarshalling any value matching one of the others fails outright.
	//
	// Not when the schema also states something that is not scoped to a type:
	// that cannot be divided among the branches, so those keep the older
	// behaviour rather than losing it.
	if len(s.TypeSchemas) > 0 && !hasNonTypeScopedConstraints(s) {
		nestedName := parentName + fieldName
		if err := g.generateTypeDef(nestedName, s); err != nil {
			return nil, err
		}
		return &NamedType{Name: nestedName}, nil
	}

	// A type union spanning several Go representations, carrying siblings that
	// only one of them would keep. Checked before the nullable case, which only
	// handles a single non-null type.
	if goType, ok := g.multiTypeUnionType(s, parentName+fieldName); ok {
		return goType, nil
	}

	// ["string","null"] beside a format, which is the one type union the pointer
	// below would take and answer with a type that cannot carry the format. See
	// nullableFormatUnion.
	if goType, ok := g.nullableFormatUnionType(s, parentName+fieldName); ok {
		return goType, nil
	}

	// A format with no "type", which the fallback would answer `any` -- and
	// `any` carries no Validate, so the format would be asserted nowhere. See
	// stringAnnotationOnlySchema.
	if goType, ok := g.stringAnnotationOnlyWrapperType(s, parentName+fieldName); ok {
		return goType, nil
	}

	// A property that must be null. Checked before the nullable case, whose
	// pointer says neither of the two things this schema needs said.
	if goType, ok := g.nullOnlyWrapperType(s, parentName+fieldName); ok {
		return goType, nil
	}

	// An allOf whose branches each bound the object's values. The map arm below
	// would answer map[string]any and carry none of them. See
	// allOfStatesUnmergeableOverflow.
	if goType, ok := g.overflowAllOfWrapperType(s, parentName+fieldName); ok {
		return goType, nil
	}

	// Nullable type: ["string", "null"] → *string
	if isNullable(s) {
		inner := nonNullType(s)
		if inner == "" {
			return &PointerType{Inner: &PrimitiveType{Name: "any"}}, nil
		}
		// Nullable object that is a struct rather than a map → pointer to named struct
		if inner == "object" && objectIsStruct(s) {
			nestedName := parentName + fieldName
			if err := g.generateTypeDef(nestedName, s); err != nil {
				return nil, err
			}
			return &PointerType{Inner: &NamedType{Name: nestedName}}, nil
		}
		// Nullable array → delegate to resolveType, which preserves the element
		// type via its array-with-items branch. Without this, the fallback below
		// resolves to PrimitiveTypeFromSchema("array") == []any and the items
		// schema (and any named element struct) is dropped, collapsing a
		// ["array","null"] property to *[]any.
		if inner == "array" {
			return g.resolveType(s, parentName+fieldName), nil
		}
		// Nullable map → delegate for the same reason, to the map arm this time.
		// A ["object","null"] whose whole shape is additionalProperties has no
		// declared property names, so the arm above does not take it and the
		// fallback below answers *map[string]any -- dropping the value type and
		// every keyword under it, which is defect #84 in its nullable spelling.
		// mapValueSchema is the predicate resolveType's own map arm consults, so
		// the two agree on exactly which nodes are maps.
		if mapValueSchema(s, inner) != nil {
			return g.resolveType(s, parentName+fieldName), nil
		}
		baseType := PrimitiveTypeFromSchema(inner)
		if baseType == nil {
			baseType = &PrimitiveType{Name: "any"}
		}
		return &PointerType{Inner: baseType}, nil
	}

	// Check for format-based type mapping on string types.
	if primarySchemaType(s) == "string" {
		if goType := g.formatGoTypeForSchema(s); goType != nil {
			return goType, nil
		}
	}

	// anyOf across unrelated Go representations -- a string enum or an array of
	// them, say. No single Go type holds both, so keep the value raw and check
	// the alternatives; the fallback below would otherwise type it `any` and
	// validate nothing.
	if goType, ok := g.anyOfUnionType(s, parentName+fieldName); ok {
		return goType, nil
	}

	// A property whose schema constrains the value without naming a type -- a
	// bare `not`, or a bare if/then/else -- has no arm in resolveType below,
	// which answers `any`. `any` carries no Validate and no rule survives the
	// `any` filter in generateStructDef, so the keyword is enforced nowhere.
	// generateTypeDef already has a wrapper for each shape (NotSchemaDef for
	// `not`, DynamicSchemaDef for if/then/else), both of them a struct over the
	// raw JSON with a Validate of their own; name the property after one so the
	// constraint has somewhere to live. This is the same shape the property
	// would get from a $ref to a definition holding that schema.
	if g.inlineConstraintWrapper(s) {
		nestedName := parentName + fieldName
		if err := g.generateTypeDef(nestedName, s); err != nil {
			return nil, err
		}
		return &NamedType{Name: nestedName}, nil
	}

	// An integer written inline under BigIntSupport, which resolveType below
	// would answer with a bare int64 -- the one Go type the flag exists to get
	// away from. See bigIntInlineWrapper.
	if g.bigIntInlineWrapper(s) {
		nestedName := parentName + fieldName
		if err := g.generateTypeDef(nestedName, s); err != nil {
			return nil, err
		}
		return &NamedType{Name: nestedName}, nil
	}

	// A property whose schema states no "type" and would be given one by
	// resolveType, read off a validation keyword. {"properties":{"p":{"minimum":
	// 5}}} typed p *float64, so {"p":"abc"} and {"p":{"a":1}} died in the
	// decoder although `minimum` says nothing about either. See
	// boxedInferredType, and issue #139.
	//
	// Last, for the reason the arms above it are ordered as they are: a schema
	// one of them types is typed by it, and this must not take the property away
	// from an answer that is already right. It is the same call the overflow-map
	// positions take, so a sub-schema written under `properties` and the same one
	// written under `additionalProperties` get the same type.
	if goType, ok := g.boxedInferredType(s, parentName+fieldName); ok {
		return goType, nil
	}

	return g.resolveType(s, parentName+fieldName), nil
}

// bigIntInlineWrapper reports whether an integer written inline -- as a
// property's schema, or as the item schema of an array or a map -- has to be
// materialized into a named type of its own because BigIntSupport is on.
//
// BigIntSupport replaces `type DefA int64` with a struct holding an int64, a
// *big.Int and a flag, so that an integer too large for an int64 still decodes.
// generateTypeDef builds that struct, and generateTypeDef is only ever reached
// for a schema that is being given a name -- a $defs entry, the target of a
// $ref. An integer written inline never had a name, so it never reached the
// arm: the field stayed an int64 and `{"alpha":10000000000000000000000}` failed
// in encoding/json before any of the flag's machinery ran. That is the
// commonest way to write the schema, and the flag did nothing there.
//
// Naming the position is the whole fix: the wrapper's behaviour is decided by
// its own UnmarshalJSON, MarshalJSON and Validate, all of which the $ref case
// already exercises, and a property named after its position is the shape
// several other arms here already produce (an inline enum, an inline `not`, an
// inline object). It does change the field's Go type from int64 to that name,
// which is why it is confined to the flag: without BigIntSupport nothing here
// fires.
//
// The predicate is exact rather than approximate, because materializing under a
// schema generateTypeDef would answer some other way does not leave the type
// alone -- it changes it to that other answer. So it admits only a schema whose
// "type" is exactly ["integer"] and which states no keyword that routes
// generateTypeDef to an arm ahead of the primitive one: an enum or const
// (generateEnumDef), a $ref or $dynamicRef, a composition, a draft-3 type
// alternative, object keywords (generateStructDef), or the unevaluatedItems
// shapes that go to the runtime annotation evaluator. What is left is the
// schema that reaches the BigIntAliasDef arm and nothing else.
func (g *Generator) bigIntInlineWrapper(s *schema.Schema) bool {
	if s == nil || !g.config.BigIntSupport {
		return false
	}
	// Exactly {"type":"integer"}. A second type alongside it makes the property
	// a raw-value wrapper that already keeps the bytes. ["integer","null"] is
	// excluded for a sharper reason: it resolves to *int64, which decodes a JSON
	// null correctly, so there is no defect here to fix -- and taking the
	// position over would retype the field from *int64 to a named wrapper, which
	// is a change to the API of the generated code with nothing behind it.
	//
	// The wrapper itself can say null since issue #85 (see
	// BigIntAliasDef.AllowsNull), so this exclusion is no longer forced. It is
	// kept deliberately: widening it is a separate decision about which
	// positions trade a Go type for arbitrary precision, not a bug fix.
	if len(s.Type) != 1 || s.Type[0] != "integer" {
		return false
	}
	if len(s.Enum) > 0 || s.Const != nil || s.ConstIsNull ||
		s.EffectiveRef() != "" || s.DynamicRef != "" ||
		len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 ||
		len(s.TypeSchemas) > 0 ||
		hasProperties(s) || len(s.PatternProperties) > 0 || s.UnevaluatedProperties != nil {
		return false
	}
	return g.annotationSchemaDef("", s) == nil
}

// inlineAnnotationWrapper reports whether a property's schema is one that
// generateTypeDef answers with the runtime annotation evaluator: an
// unevaluatedItems whose evaluated set depends on which of its sibling
// applicators matched the value, or an applicator branch carrying an
// unevaluatedItems of its own.
//
// This is the array counterpart of inlineConstraintWrapper, and it exists for
// the same reason: the wrapper is only ever built for a schema being given a
// name, and an array written inline as a property never had one. The keyword
// was enforced at the root of a document and in a $defs entry, and nowhere
// else -- {"arr":{"type":"array","prefixItems":[{"type":"string"}],
// "anyOf":[{"prefixItems":[true,{"type":"integer"}]},true],
// "unevaluatedItems":false}} accepted ["a","b"], which the same schema written
// as the whole document has always rejected.
//
// It defers to the function that builds the wrapper, so a subtree the evaluator
// cannot model keeps the type it would otherwise have had rather than gaining a
// Validate that silently checks less than the schema says.
//
// Naming the property does change the field's Go type, from the []any the array
// would otherwise be to the wrapper's name. That is the same trade a bare `not`
// or a bare if/then/else property already makes, and the alternative is a field
// whose schema is enforced nowhere.
func (g *Generator) inlineAnnotationWrapper(s *schema.Schema) bool {
	if s == nil || !g.validationKeywordsEnabled() {
		return false
	}
	return g.annotationSchemaDef("", s) != nil
}

// inlineConstraintWrapper reports whether a property's own schema is one that
// generateTypeDef answers with a raw-JSON wrapper carrying a Validate, and that
// resolveType would otherwise collapse to `any`.
//
// The predicate is deliberately narrow. Every keyword that would give the value
// a Go type of its own -- a type, properties, a $ref, an enum, a const, a
// composition -- disqualifies the schema here, because each of those already
// has a path that produces that type, and taking the schema over would drop
// what that path knows. What is left is a schema whose only content is the
// negative or conditional keyword, which is exactly the case that has nowhere
// else to go.
//
// Both arms then defer to the function that builds the wrapper, so a shape
// neither of them can express (a `not` over something too complex to check
// statically, an `if` using a keyword the dynamic evaluator does not model)
// keeps today's `any` rather than gaining a wrapper whose Validate would be
// silently incomplete.
func (g *Generator) inlineConstraintWrapper(s *schema.Schema) bool {
	if s == nil || !g.validationKeywordsEnabled() {
		return false
	}
	if len(s.Type) > 0 || len(s.TypeSchemas) > 0 || hasProperties(s) ||
		len(s.PatternProperties) > 0 || s.AdditionalProperties != nil ||
		s.EffectiveRef() != "" || s.DynamicRef != "" ||
		len(s.Enum) > 0 || s.Const != nil || s.ConstIsNull ||
		len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 {
		return false
	}
	if s.Not != nil {
		return g.extractNotSchemaDef("", s) != nil
	}
	if s.If != nil && (s.Then != nil || s.Else != nil) {
		return g.dynamicSchemaDef("", s) != nil
	}
	return false
}

// resolveType converts a schema to a GoType, creating nested types if needed.
func (g *Generator) resolveType(s *schema.Schema, contextName string) GoType {
	if s == nil {
		return &PrimitiveType{Name: "any"}
	}

	// Const with no explicit type -> single-member enum, exactly as
	// resolvePropertyType does for a property. The const is what fixes the Go
	// type, and the enum is what carries the check; without this an
	// items:{"const":5} resolves to `any` and the const is enforced nowhere.
	// The promotion returns a copy, but the enum arm below consumes it and
	// returns, so it never reaches the node-identity bookkeeping further down.
	//
	// Both arms stand behind the ref arms on the drafts where a $ref displaces
	// what is written beside it (refDisplacesSiblingValues, issue #151), and
	// equally on the drafts where it applies beside it and only the merge arm
	// below can carry both (refMergesSiblingValues, issue #153).
	refDisplacesEnum := g.refDisplacesSiblingValues(s)
	refMergesEnum := g.refMergesSiblingValues(s)
	if g.validationKeywordsEnabled() && !refDisplacesEnum && !refMergesEnum && len(s.Type) == 0 {
		s = promoteConstToEnum(s)
	}

	// Inline enum
	if g.validationKeywordsEnabled() && !refDisplacesEnum && !refMergesEnum && len(s.Enum) > 0 {
		enumName := contextName
		_ = g.generateEnumDef(enumName, s)
		return &NamedType{Name: enumName}
	}

	// A $ref beside a keyword that applies alongside it, from 2019-09 on. The
	// position is neither the target nor the sibling: only the allOf
	// generateTypeDef synthesizes for the pair merges them, and only a named
	// type can carry the result. Every arm below answers from one half -- the
	// ref-only arm just below from the target, the type arms further down from
	// the sibling -- so whichever runs, something the schema states is dropped.
	//
	// resolvePropertyType has had this arm since $ref-beside-properties; the
	// element, map-value and tuple positions come through resolveType instead
	// and had none, which only became visible when "type" joined the sibling
	// list: {"items":{"type":"string","$ref":"#/$defs/minLen3"}} then stopped
	// taking the ref-only arm and came out []string with the minLength gone.
	//
	// The exclusions are what keep it from claiming work that is already done:
	//
	//   - objectIsStruct: the object arms below materialize this same node
	//     through materializeNamed and know two things this does not -- an
	//     object with no declared type is a *pointer there, and a node already
	//     materialized keeps its first name.
	//   - nodesInProgress: the schema whose own generateTypeDef frame called us
	//     is being generated under this very name, so materializeNamed would
	//     answer with it and the alias would be its own underlying.
	//   - generating: generateAllOfDef marks the name for the length of its
	//     body and hands us a merged schema that still carries the $ref it
	//     merged. Re-entering for that is the same declaration twice.
	if effRef := s.EffectiveRef(); effRef != "" && !g.refOverridesSiblingsForSchema(s) &&
		hasRefStructuralSiblings(s) && !objectIsStruct(s) &&
		!g.nodesInProgress[s] && !g.generating[contextName] {
		if n, cyclic := g.materializeNamed(s, contextName); g.generated[n] {
			return namedOrPointer(n, cyclic)
		}
	}

	// $ref / $recursiveRef
	if effRef := s.EffectiveRef(); effRef != "" && (g.refOverridesSiblingsForSchema(s) || !hasRefStructuralSiblings(s)) {
		if g.isSelfRefInContext(effRef, s) {
			if g.rootIsObjectType() {
				return &PointerType{Inner: &NamedType{Name: g.rootTypeName}}
			}
			return &PrimitiveType{Name: "json.RawMessage"}
		}
		goName := refToGoName(effRef)
		// resolveEffectiveRefSchema, not resolveRefInContext: a $recursiveRef
		// resolves against the dynamic scope, and resolving it statically here
		// would pick the document that lexically contains it rather than the
		// one the reference was entered from.
		if refSchema := g.resolveEffectiveRefSchema(s); refSchema != nil {
			if foreign, ok := g.foreignTypeFor(refSchema); ok {
				return foreign
			}
			pushed := g.pushDynamicScope(refSchema)
			// If the ref resolved to a scoped document root (not the main root),
			// derive the Go name from that schema rather than the raw ref string.
			// This handles $ref: "#" inside a sub-schema with its own $id.
			goName = g.goNameForResolvedRef(effRef, refSchema, goName)
			// A ref leading back to a node whose type is still being generated
			// must not be generated again: goName is then the name already in
			// flight, g.generated is not set for it until that frame finishes,
			// and the call re-enters the identical arm.
			// {"$defs":{"C":{"type":[{"$ref":"#/$defs/C"}]}}} closes the loop in
			// a single hop through the draft-3 type-alternatives path, which
			// arrives here rather than through materializeNamed. Only the
			// recursion is suppressed -- the reference keeps the name and the
			// pointer rule it had, so a recursive []T does not become []*T.
			if canonical, cyclic := g.cyclicNodeName(refSchema); cyclic {
				goName = canonical
			} else {
				_ = g.generateTypeDef(goName, refSchema)
			}
			if pushed {
				g.popDynamicScope()
			}
			// If the ref resolves to its own enclosing document root, it's a
			// local self-reference within a scoped $id context. Use a pointer
			// to break the Go recursive type cycle.
			if g.isScopedSelfRef(effRef, s, refSchema) {
				return &PointerType{Inner: &NamedType{Name: goName}}
			}
		}
		return &NamedType{Name: goName}
	}

	// $dynamicRef — resolve via dynamic scope chain.
	if s.DynamicRef != "" && (g.refOverridesSiblingsForSchema(s) || !hasRefStructuralSiblings(s)) {
		goName := refToGoName(s.DynamicRef)
		if refSchema := g.resolveDynamicRef(s.DynamicRef, s); refSchema != nil {
			goName = g.goNameForResolvedRef(s.DynamicRef, refSchema, goName)
			_ = g.generateTypeDef(goName, refSchema)
			if g.isScopedSelfRef(s.DynamicRef, s, refSchema) {
				return &PointerType{Inner: &NamedType{Name: goName}}
			}
		}
		return &NamedType{Name: goName}
	}

	primaryType := primarySchemaType(s)
	if primaryType == "" {
		primaryType = g.inferTypeFromConstraints(s)
	}

	// The two format shapes, before the arms whose answer they must displace:
	// the nullable pointer below, and the `any` fallback at the end. Both
	// materialize a named type, so an array element, a map value, a tuple slot
	// and a composition branch all reach the same one the property arm does.
	if goType, ok := g.nullableFormatUnionType(s, contextName); ok {
		return goType
	}
	if goType, ok := g.stringAnnotationOnlyWrapperType(s, contextName); ok {
		return goType
	}

	// The same for an allOf that bounds an object's values from more than one
	// branch, which the map arm further down would answer map[string]any.
	if goType, ok := g.overflowAllOfWrapperType(s, contextName); ok {
		return goType
	}

	// Nullable types
	if isNullable(s) {
		inner := nonNullType(s)
		if inner == "" {
			return &PointerType{Inner: &PrimitiveType{Name: "any"}}
		}
		if inner == "object" && objectIsStruct(s) {
			n, _ := g.materializeNamed(s, contextName)
			return &PointerType{Inner: &NamedType{Name: n}}
		}
		// Nullable array: recurse into items so the element type is preserved
		// (mirrors the non-nullable array branch below). Without this, a
		// ["array","null"] union falls through to PrimitiveTypeFromSchema and
		// loses its items, collapsing to *[]any. No outer pointer is needed: a
		// slice's nil value already represents JSON null, so []T round-trips
		// null/empty/populated faithfully (unlike nullable scalars/objects,
		// whose zero values are indistinguishable from null).
		if inner == "array" && s.Items != nil && s.Items.Schema != nil && !g.isTupleArray(s) {
			itemType := g.resolveArrayItemType(s.Items.Schema, contextName+"Item")
			return &ArrayType{ItemType: itemType}
		}
		// Nullable map: an object whose whole shape is additionalProperties, on
		// the same terms as the non-nullable map arm below -- one predicate
		// decides both, so a node cannot be typed a map here and read as
		// something else when its value keywords are collected.
		//
		// No outer pointer, for the reason the slice above gives: a nil map is
		// what encoding/json leaves for a JSON null and what it marshals back as
		// null, while a present {} decodes to a non-nil empty map. So null,
		// absent and empty stay as distinguishable as the *map[string]any this
		// replaces made them -- a pointer to a map adds no state, since a nil
		// pointer and a pointer to a nil map both marshal to null.
		if mapVals := mapValueSchema(s, inner); mapVals != nil {
			valueType := g.resolveArrayItemType(mapVals, contextName+"Value")
			return &MapType{KeyType: &PrimitiveType{Name: "string"}, ValueType: valueType}
		}
		baseType := PrimitiveTypeFromSchema(inner)
		if baseType == nil {
			baseType = &PrimitiveType{Name: "any"}
		}
		return &PointerType{Inner: baseType}
	}

	// Object that is a struct rather than a map → nested struct (explicit
	// type:"object", or inferred from an object-only keyword). See objectIsStruct.
	if primaryType == "object" && objectIsStruct(s) {
		n, cyclic := g.materializeNamed(s, contextName)
		return namedOrPointer(n, cyclic)
	}
	// The same without an explicit type → pointer to struct (nil when absent, enabling omitempty)
	if primaryType == "" && objectIsStruct(s) {
		n, _ := g.materializeNamed(s, contextName)
		return &PointerType{Inner: &NamedType{Name: n}}
	}

	// allOf / anyOf-with-properties without direct properties → delegate to generateTypeDef
	// which handles allOf merging and anyOf property collection. This covers schemas like
	// {"allOf": [{"$ref": "#/definitions/inner"}]} where the ref target has properties.
	// Guard against infinite recursion: generateAllOfDef may call resolveType with a merged
	// schema that still has allOf (preserved for unevaluatedProperties evaluation).
	//
	// A composition that admits nothing delegates for the same reason: only
	// generateTypeDef can emit the forbidding wrapper, and `any` here would drop
	// the rejection entirely. See compositionAdmitsNothing.
	if canonical, ok := g.nodeTypeNames[s]; ok && (g.allOfNeedsNamedType(s) || g.compositionAdmitsNothing(s) || (len(s.AnyOf) > 0 && g.anyOfHasProperties(s))) {
		return namedOrPointer(canonical, g.nodesInProgress[s])
	}
	if !g.generating[contextName] && (g.allOfNeedsNamedType(s) || g.compositionAdmitsNothing(s) || (len(s.AnyOf) > 0 && g.anyOfHasProperties(s))) {
		g.generating[contextName] = true
		_ = g.generateTypeDef(contextName, s)
		delete(g.generating, contextName)
		if g.generated[contextName] {
			return &NamedType{Name: contextName}
		}
	}

	// Array with items. A tuple is excluded: `items` there governs only what
	// follows the prefix, so it does not describe the array's elements and
	// typing the slice from it cannot decode the prefix. See isTupleArray.
	if primaryType == "array" && s.Items != nil && s.Items.Schema != nil && !g.isTupleArray(s) {
		itemType := g.resolveArrayItemType(s.Items.Schema, contextName+"Item")
		return &ArrayType{ItemType: itemType}
	}

	// Object whose whole shape is additionalProperties: no declared property
	// names, every value governed by one schema. That is a map, and its value
	// type is the one the schema names -- map[string]any would drop the schema
	// and with it any validation of what the object holds.
	//
	// The value type is kept whether or not it is a named one. A named value
	// answers for its own schema through its Validate, which the parent
	// dispatches over the map; a bare value type carries no such method, and its
	// keywords ride buildItemValidation instead -- the same machinery that
	// reaches the elements of a []string. Stopping at named types, as this arm
	// once did, left `additionalProperties: {"type":"string","minLength":3}`
	// typed map[string]any with minLength enforced nowhere.
	if mapVals := mapValueSchema(s, primaryType); mapVals != nil {
		valueType, ok := g.boxedInferredType(mapVals, contextName+"Value")
		if !ok {
			valueType = g.resolveArrayItemType(mapVals, contextName+"Value")
		}
		return &MapType{KeyType: &PrimitiveType{Name: "string"}, ValueType: valueType}
	}

	// Primitive or default
	if primaryType != "" {
		// Check for format-based type mapping on string types.
		if primaryType == "string" {
			if goType := g.formatGoTypeForSchema(s); goType != nil {
				return goType
			}
		}
		t := PrimitiveTypeFromSchema(primaryType)
		if t != nil {
			return t
		}
	}

	// Nothing above gave the value a Go type, and the fallback is `any`: a type
	// Go forbids methods on, so a schema landing here is not weakened but
	// dropped. json.Unmarshal into it cannot fail and there is no Validate for a
	// check to live in, which is right for a schema that constrains nothing and a
	// silent lie for any other.
	//
	// A name for the position is the whole fix, and it is the last thing tried so
	// it cannot take a schema away from an arm that types it better. This is
	// issue #126, and it is the same gap #113 and #114 closed at a document root:
	// {"properties":{"b":{"not":{"type":"object","required":["foo"]}}}} came out
	// `B any` and accepted {"b":{"foo":"x"}}, which the `not` forbids, while the
	// identical schema written as the whole document has rejected it since #114.
	// Every position that reaches here shares the arm -- an element, a map value,
	// a tuple slot, a oneOf branch -- so the answer no longer depends on where
	// the schema was written.
	//
	// It does change the field's Go type from `any` to the wrapper's name, across
	// every such position. That is the same trade the root made, and the
	// alternative is a field whose schema is enforced nowhere.
	if def := g.constraintOnlyNamedType(s, contextName); def != nil {
		return def
	}

	return &PrimitiveType{Name: "any"}
}

// constraintOnlyNamedType materializes a schema that constrains without naming a
// type, and returns the type to reference it by. nil keeps today's `any`.
//
// The guards are what keep it from claiming a name that is already spoken for.
// A name already generated belongs to another schema (or to this one, reached by
// another route), and a name being generated is the frame that called us -- both
// would have the wrapper stand in for something else.
func (g *Generator) constraintOnlyNamedType(s *schema.Schema, contextName string) GoType {
	if contextName == "" || g.generated[contextName] || g.generating[contextName] || g.nodesInProgress[s] {
		return nil
	}
	def := g.constraintOnlyWrapperDef(contextName, s)
	if def == nil {
		return nil
	}
	g.generated[contextName] = true
	g.output.TypeDefs = append(g.output.TypeDefs, def)
	return &NamedType{Name: contextName}
}

// materializeNamed generates s under contextName and returns the name to
// reference it by -- unless the node was already materialized, in which case its
// first (canonical) name is reused.
//
// A self-referential document reaches the same node by a different route at
// every level: in a JSON Schema meta-schema, {"$ref":"#"} inside a property
// pulls the document root's properties back in, so that property is re-entered
// with the context name one segment longer each time. Keyed on the name alone,
// the generated/in-progress guards never fire; the node is regenerated forever
// and the names grow without bound. Keying on node identity terminates it.
// It also reports whether the reference closes a cycle -- the node is still
// being generated further up the stack -- in which case the caller must emit a
// pointer, since Go rejects a type that contains itself by value.
func (g *Generator) materializeNamed(s *schema.Schema, contextName string) (string, bool) {
	if canonical, ok := g.nodeTypeNames[s]; ok {
		return canonical, g.nodesInProgress[s]
	}
	_ = g.generateTypeDef(contextName, s)
	return contextName, false
}

// cyclicNodeName reports the name s is already being generated under, when s's
// own generation is still in flight further up the stack.
//
// This is the read side of the mark generateTypeDef sets on entry. Every route
// into generateTypeDef names its target after the position it was reached from
// -- parentName+fieldName, a contextName, a name derived from the $ref string
// -- and re-entrancy there is guarded by g.generated[name], which is only set
// when a definition *completes*. A cycle that arrives back at the same schema
// *node* is therefore not recognised, whether it arrives under a name one
// segment longer each time (RootA, RootAA, RootAAA ...) or under the identical
// name already in flight. Either way the arm re-enters itself and the run ends
// in "fatal error: stack overflow", which no recover intercepts and which took
// the process down for 57 bytes of schema. materializeNamed already applies
// this rule on the object path of resolveType; the callers below apply it to
// the two routes materializeNamed does not cover.
//
// Answering with the name, rather than with a type, is deliberate: what the
// caller must do with it differs by position. A struct *field* has to be a
// pointer, since a cycle otherwise makes the type contain itself by value and
// Go rejects that outright; a slice element or a delegated branch already has
// the indirection and must stay by value, or every recursive []T in the corpus
// silently becomes []*T. Neither caller degrades the reference: the node is
// being generated, so the name will exist and naming it is exact.
func (g *Generator) cyclicNodeName(s *schema.Schema) (string, bool) {
	if s == nil || !g.nodesInProgress[s] {
		return "", false
	}
	canonical, ok := g.nodeTypeNames[s]
	return canonical, ok
}

// namedOrPointer builds a reference to name, pointer-wrapped when it closes a
// recursive cycle.
func namedOrPointer(name string, cyclic bool) GoType {
	if cyclic {
		return &PointerType{Inner: &NamedType{Name: name}}
	}
	return &NamedType{Name: name}
}

// buildDocumentRoots walks the schema tree and registers every node that declares
// an $id into g.documentRoots, keyed by its canonical (fully-resolved) URI.
// This enables scoped resolution: when a subschema has $id, $ref: "#/..."
// within it resolves against that subschema, not the top-level root.
func (g *Generator) buildDocumentRoots(s *schema.Schema) {
	if s == nil || s.IsBooleanSchema() {
		return
	}
	// If this schema has a computed BaseURI and is its own DocumentRoot, register it.
	if s.BaseURI != nil && s.DocumentRoot == s {
		key := s.BaseURI.String()
		// Strip trailing fragment "#" for consistent lookups.
		key = strings.TrimSuffix(key, "#")
		g.documentRoots[key] = s
	}
	// Recurse into all child schemas.
	for _, sub := range s.Properties {
		g.buildDocumentRoots(sub)
	}
	for _, sub := range s.TypeSchemas {
		g.buildDocumentRoots(sub)
	}
	for _, sub := range s.PatternProperties {
		g.buildDocumentRoots(sub)
	}
	for _, sub := range s.Definitions {
		g.buildDocumentRoots(sub)
	}
	for _, sub := range s.Defs {
		g.buildDocumentRoots(sub)
	}
	for _, sub := range s.AllOf {
		g.buildDocumentRoots(sub)
	}
	for _, sub := range s.AnyOf {
		g.buildDocumentRoots(sub)
	}
	for _, sub := range s.OneOf {
		g.buildDocumentRoots(sub)
	}
	if s.Not != nil {
		g.buildDocumentRoots(s.Not)
	}
	if s.Items != nil && s.Items.Schema != nil {
		g.buildDocumentRoots(s.Items.Schema)
	}
	if s.Items != nil {
		for _, sub := range s.Items.Schemas {
			g.buildDocumentRoots(sub)
		}
	}
	for _, sub := range s.PrefixItems {
		g.buildDocumentRoots(sub)
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		g.buildDocumentRoots(s.AdditionalProperties.Schema)
	}
	if s.AdditionalItems != nil && s.AdditionalItems.Schema != nil {
		g.buildDocumentRoots(s.AdditionalItems.Schema)
	}
	if s.Contains != nil {
		g.buildDocumentRoots(s.Contains)
	}
	if s.If != nil {
		g.buildDocumentRoots(s.If)
	}
	if s.Then != nil {
		g.buildDocumentRoots(s.Then)
	}
	if s.Else != nil {
		g.buildDocumentRoots(s.Else)
	}
	if s.PropertyNames != nil {
		g.buildDocumentRoots(s.PropertyNames)
	}
	if s.UnevaluatedItems != nil {
		g.buildDocumentRoots(s.UnevaluatedItems)
	}
	if s.UnevaluatedProperties != nil {
		g.buildDocumentRoots(s.UnevaluatedProperties)
	}
	if s.ContentSchema != nil {
		g.buildDocumentRoots(s.ContentSchema)
	}
	for _, sub := range s.DependentSchemas {
		g.buildDocumentRoots(sub)
	}
}

// resolveRefInContext resolves a $ref string using the given context schema's
// BaseURI and DocumentRoot for scoped resolution. This handles the case where
// a subschema with $id changes the base URI and document root for fragment
// resolution.
//
// It also records the outcome. A ref is reported as unresolved only if no
// attempt anywhere resolved it: resolution depends on the context schema, and
// most callers probe optimistically and handle a nil result, so a failure
// against one context is not evidence that the ref is unresolvable.
func (g *Generator) resolveRefInContext(ref string, ctx *schema.Schema) *schema.Schema {
	resolved := g.resolveRefInContextUncounted(ref, ctx)
	// A ref can be the first thing to materialize a schema: into a vendor
	// keyword's raw JSON, or into a document the resolver fetched. Neither was
	// present when Generate checked its argument, so a null subschema in one
	// would reach the generator unexamined and panic. Refuse the node instead;
	// the recorded error is what Generate reports.
	if resolved != nil {
		if err := checkNullSubschemas(resolved, ref, g.nullChecked); err != nil {
			if g.nullSubschemaErr == nil {
				g.nullSubschemaErr = err
			}
			resolved = nil
		}
	}
	if resolved != nil {
		g.resolvedRefs[ref] = true
	} else {
		g.unresolvedRefs[ref] = true
	}
	return resolved
}

func (g *Generator) resolveRefInContextUncounted(ref string, ctx *schema.Schema) *schema.Schema {
	// Determine the effective base URI and document root from context.
	ctxBase := g.baseURI
	ctxDocRoot := g.rootSchema
	if ctx != nil {
		if ctx.BaseURI != nil {
			ctxBase = ctx.BaseURI
		}
		if ctx.DocumentRoot != nil {
			ctxDocRoot = ctx.DocumentRoot
		}
	}

	// 1. Direct defs map lookup (handles #/$defs/Foo, #/definitions/Bar).
	if s, ok := g.defs[ref]; ok {
		return s
	}
	// 2. Check anchor index (handles $id-based and $anchor-based refs).
	if refPath, ok := g.anchors[ref]; ok {
		if s, ok2 := g.defs[refPath]; ok2 {
			return s
		}
	}
	// 3. For URN refs with fragments (e.g. "urn:...#something"), try the fragment as an anchor.
	if idx := strings.LastIndex(ref, "#"); idx > 0 {
		fragment := ref[idx:]
		if refPath, ok := g.anchors[fragment]; ok {
			if s, ok2 := g.defs[refPath]; ok2 {
				return s
			}
		}
	}
	// 3b. Resolve as relative URI against context base URI, then check anchors and document roots.
	if resolved := resolveRelativeURIAgainst(ref, ctxBase); resolved != "" {
		if refPath, ok := g.anchors[resolved]; ok {
			if s, ok2 := g.defs[refPath]; ok2 {
				return s
			}
		}
		// Check document roots by canonical URI.
		resolvedClean := strings.TrimSuffix(resolved, "#")
		if s, ok := g.documentRoots[resolvedClean]; ok {
			return s
		}
	}
	// 4. Fragment-only refs: use the context document root for JSON Pointer traversal.
	if strings.HasPrefix(ref, "#") && ctxDocRoot != nil {
		local := schema.NewLocalResolver(ctxDocRoot)
		if s, err := local.Resolve(ref); err == nil {
			return s
		}
	}
	// 5. Try resolving as absolute/relative URI against context base, then delegate
	//    to the external resolver. For refs with fragments (e.g., "name-defs.json#/$defs/orNull"),
	//    first check document roots for the document part, then resolve the fragment within it.
	if ctxBase != nil {
		refURL, err := url.Parse(ref)
		if err == nil {
			absURL := ctxBase.ResolveReference(refURL)
			fragment := absURL.Fragment
			docURL := *absURL
			docURL.Fragment = ""
			docKey := docURL.String()

			// Check document roots first.
			if docSchema, ok := g.documentRoots[docKey]; ok {
				if fragment != "" {
					local := schema.NewLocalResolver(docSchema)
					if s, err := local.Resolve("#" + fragment); err == nil {
						return s
					}
				} else {
					return docSchema
				}
			}

			// Try external resolver with the absolute URI.
			// When there's a fragment, first load the document root (without fragment)
			// so we can register it properly, then resolve the fragment locally.
			// This ensures ComputeBaseURIs is called on the full document, not a sub-schema.
			if g.resolver != nil {
				if fragment != "" {
					// Load the document root first.
					if docSchema, err := g.resolver.ResolveSchema(docKey, ctxBase); err == nil {
						g.registerRemoteSchema(docSchema, &docURL)
						local := schema.NewLocalResolver(docSchema)
						if resolved, err := local.Resolve("#" + fragment); err == nil {
							return resolved
						}
					}
				}
				// Fallback: try with the full URI (no fragment, or fragment resolution failed above).
				if s, err := g.resolver.ResolveSchema(absURL.String(), ctxBase); err == nil {
					g.registerRemoteSchema(s, &docURL)
					return s
				}
			}
		}
	}
	// 6. Try external resolver with the raw ref (handles absolute URIs, etc.).
	if g.resolver != nil {
		// Load the document itself before the fragment, exactly as step 5 does.
		// A resolver may resolve the fragment for us and hand back the *sub*schema
		// (MappingResolver does), and registering that as though it were the
		// document makes it its own DocumentRoot -- so its siblings fall out of
		// scope and a later "#anchor" or "$dynamicAnchor" lookup misses them.
		if refURL, parseErr := url.Parse(ref); parseErr == nil && refURL.Fragment != "" {
			frag := refURL.Fragment
			docURL := *refURL
			docURL.Fragment = ""
			if docSchema, err := g.resolver.ResolveSchema(docURL.String(), ctxBase); err == nil {
				g.registerRemoteSchema(docSchema, &docURL)
				local := schema.NewLocalResolver(docSchema)
				if resolved, err := local.Resolve("#" + frag); err == nil {
					return resolved
				}
			}
		}
		if s, err := g.resolver.ResolveSchema(ref, ctxBase); err == nil {
			// Register the remote schema so its internal $ref chains resolve.
			if refURL, parseErr := url.Parse(ref); parseErr == nil {
				frag := refURL.Fragment
				refURL.Fragment = ""
				g.registerRemoteSchema(s, refURL)
				// If there was a fragment, resolve it within the now-registered schema.
				if frag != "" {
					local := schema.NewLocalResolver(s)
					if resolved, localErr := local.ResolveSchema("#"+frag, refURL); localErr == nil {
						return resolved
					}
				}
			}
			return s
		}
	}
	return nil
}

func (g *Generator) resolveEffectiveRefSchema(s *schema.Schema) *schema.Schema {
	if s == nil {
		return nil
	}
	if s.RecursiveRef != "" {
		if resolved := g.resolveRecursiveRef(s.RecursiveRef, s); resolved != nil {
			return resolved
		}
	}
	if effRef := s.EffectiveRef(); effRef != "" {
		return g.resolveRefInContext(effRef, s)
	}
	return nil
}

func (g *Generator) resolveRecursiveRef(ref string, ctx *schema.Schema) *schema.Schema {
	resolved := g.resolveRefInContext(ref, ctx)
	if resolved == nil {
		return nil
	}
	if ref != "#" || ctx == nil || ctx.DocumentRoot == nil || ctx.DocumentRoot.RecursiveAnchor == nil || !*ctx.DocumentRoot.RecursiveAnchor {
		return resolved
	}
	// This walk is innermost-first, and 2019-09 says a $recursiveRef resolves to
	// the *outermost* resource in the dynamic scope whose $recursiveAnchor is
	// true. The order is left as it is because the walk selects nothing at all:
	// instrumented over all 832 schemas in testdata (the internal corpus plus
	// the whole JSON Schema Test Suite), this function is called three times,
	// reaches this loop three times, always with len(g.dynamicScope) == 1, and
	// in every case the single frame carries no $recursiveAnchor -- so the loop
	// falls through to `resolved`, the ordinary $ref answer, and the order it
	// walked in never decided anything.
	//
	// Two things keep it that way rather than luck. A schema with two anchored
	// resources in reach routes to the runtime evaluator instead (#160), and
	// that evaluator walks outermost-first, which is where the spec's rule is
	// actually implemented -- see _dynResolveRef and resolveDynamicRef. What is
	// left here is the single-resource case, where the two orders name the same
	// frame anyway.
	//
	// So this is not a latent false rejection, and swapping the order would not
	// fix a reachable defect.
	//
	// It is worth knowing what does not cover it. The obvious shape -- two nested
	// resources both carrying $recursiveAnchor, in
	// testdata/schemas/regression/recursive_anchor_nested_resources.json -- calls
	// this function exactly once and answers from `resolved` above, the ordinary
	// $ref resolution, before this walk decides anything. Planting the opposite
	// resolution leaves that fixture passing. It pins a verdict three independent
	// implementations agree on, which is worth having, but it is not a guard on
	// the order, and no fixture in the tree is. That absence is the open part of
	// #167, and it should be closed by finding the shape rather than by assuming
	// one exists.
	for i := len(g.dynamicScope) - 1; i >= 0; i-- {
		scope := g.dynamicScope[i]
		if scope != nil && scope.RecursiveAnchor != nil && *scope.RecursiveAnchor {
			return scope
		}
	}
	return resolved
}

// registerRemoteSchema computes base URIs for a remotely-resolved schema and
// indexes its $id-bearing nodes into g.documentRoots so that subsequent refs
// (including fragment-only refs like "#" within the remote document) resolve correctly.
func (g *Generator) registerRemoteSchema(s *schema.Schema, docURI *url.URL) {
	if s == nil {
		return
	}
	s.ComputeBaseURIs(docURI, s)
	g.buildDocumentRoots(s)
}

// pushDynamicScope pushes a document root onto the dynamic scope chain when
// following a $ref that crosses a document boundary. Returns true if pushed
// (caller must pop), false if the target is in the same scope or nil.
func (g *Generator) pushDynamicScope(target *schema.Schema) bool {
	if target == nil {
		return false
	}
	docRoot := target.DocumentRoot
	if docRoot == nil {
		docRoot = target
	}
	// Don't push if it's the same document root as the current top of stack.
	if len(g.dynamicScope) > 0 && g.dynamicScope[len(g.dynamicScope)-1] == docRoot {
		return false
	}
	g.dynamicScope = append(g.dynamicScope, docRoot)
	return true
}

// popDynamicScope removes the top entry from the dynamic scope chain.
func (g *Generator) popDynamicScope() {
	if len(g.dynamicScope) > 0 {
		g.dynamicScope = g.dynamicScope[:len(g.dynamicScope)-1]
	}
}

// resolveDynamicRef resolves a $dynamicRef to a schema using the dynamic scope
// chain. Per the JSON Schema 2020-12 spec:
//
//  1. Resolve the $dynamicRef to its initial target (just like $ref).
//  2. Check if the initial target schema has a $dynamicAnchor with the same name
//     as the fragment in the $dynamicRef (the "bookend").
//  3. If a bookend exists, walk the dynamic scope chain (the stack of resources
//     entered via $ref) from outermost to innermost. The first resource that
//     *declares* a $dynamicAnchor with the same name wins.
//  4. If no bookend exists at the initial target, behave like a normal $ref.
//
// Step 3 asks resourceDynamicAnchor, which is pkg/schema's resource rule and the
// same question the generated evaluator asks of each frame it pushes. The other
// reading -- findDynamicAnchor, which stops descending at a nested $id but still
// reads the boundary node -- credits an anchor written on such a node to the
// resource that merely contains it, and a resource nothing ever enters then
// answers for every evaluation that passes overhead. That is issues #163 and
// #164: the two paths through this generator disagreed about the same rule, and
// the disagreement was a false rejection down the static one.
func (g *Generator) resolveDynamicRef(ref string, ctx *schema.Schema) *schema.Schema {
	// Steps 1 and 2, which the runtime evaluator needs too: see
	// dynamicRefInitialTarget.
	initialTarget, anchorName := g.dynamicRefInitialTarget(ref, ctx, g.resolveRefInContext)
	if initialTarget == nil {
		return nil
	}
	if anchorName == "" || initialTarget.DynamicAnchor != anchorName {
		return initialTarget
	}

	// Step 3: Bookend exists — walk the dynamic scope chain from outermost to
	// innermost, looking for the first resource that declares a $dynamicAnchor
	// with the same name.
	for _, resource := range g.dynamicScope {
		if found := resourceDynamicAnchor(resource, anchorName); found != nil {
			return found
		}
	}

	// Fallback: no resource in scope declares the anchor — use the bookend.
	return initialTarget
}

// dynamicRefInitialTarget performs the static half of a $dynamicRef: the schema
// the reference would reach if it were spelled $ref, and the plain-name anchor
// its fragment carries.
//
// Both halves are needed by two callers that do different things with them.
// resolveDynamicRef compares the anchor against the target to decide whether the
// generation-time dynamic scope is consulted at all; the node builder compares
// them for the same reason, and then keeps the target as the fallback the
// generated evaluator uses when no resource in the document's dynamic scope
// claims the anchor. Sharing the computation is what keeps the two from
// disagreeing about where a reference points.
//
// The anchor is empty for a fragment that is a JSON pointer or absent, which is
// the shape that can never resolve dynamically: bookending is a statement about
// a plain-name anchor.
//
// resolve is the caller's own reference resolver rather than a fixed one,
// because the two callers differ in whether a miss should be recorded.
// resolveDynamicRef is producing a type and its failure is an unresolved
// reference the run must report; the node builder is probing, and a reference it
// cannot serve costs the caller only this compiled form -- it falls back to what
// it would have emitted anyway, and recording the miss would turn an optimistic
// look into a reported error.
func (g *Generator) dynamicRefInitialTarget(ref string, ctx *schema.Schema, resolve func(string, *schema.Schema) *schema.Schema) (*schema.Schema, string) {
	var anchorName string
	if strings.HasPrefix(ref, "#") && !strings.HasPrefix(ref, "#/") {
		anchorName = ref[1:] // plain name fragment, e.g., "#items" → "items"
	} else if idx := strings.LastIndex(ref, "#"); idx > 0 {
		frag := ref[idx+1:]
		if !strings.HasPrefix(frag, "/") {
			anchorName = frag // URI with name fragment, e.g., "extended#meta" → "meta"
		}
	}

	var initialTarget *schema.Schema
	ctxDocRoot := g.rootSchema
	if ctx != nil && ctx.DocumentRoot != nil {
		ctxDocRoot = ctx.DocumentRoot
	}
	if anchorName != "" && ctxDocRoot != nil {
		// Try $dynamicAnchor lookup in the local document scope first.
		initialTarget = findDynamicAnchor(ctxDocRoot, anchorName)
		if initialTarget == nil {
			// Fall back to standard $anchor resolution.
			local := schema.NewLocalResolver(ctxDocRoot)
			if s, err := local.Resolve("#" + anchorName); err == nil {
				initialTarget = s
			}
		}
	}
	if initialTarget == nil {
		// For JSON pointers, full URIs, or when local resolution failed.
		initialTarget = resolve(ref, ctx)
	}
	return initialTarget, anchorName
}

// findDynamicAnchor searches a schema tree for a sub-schema with the given
// $dynamicAnchor value. It respects $id scope boundaries.
func findDynamicAnchor(s *schema.Schema, anchor string) *schema.Schema {
	if s == nil || s.IsBooleanSchema() {
		return nil
	}
	if s.DynamicAnchor == anchor {
		return s
	}
	// Search child schemas, respecting $id scope boundaries.
	for _, sub := range allSubSchemas(s) {
		if sub == nil || sub.IsBooleanSchema() {
			continue
		}
		if sub.ID != "" {
			// New document scope — only check this node directly, not descendants.
			if sub.DynamicAnchor == anchor {
				return sub
			}
			continue
		}
		if found := findDynamicAnchor(sub, anchor); found != nil {
			return found
		}
	}
	return nil
}

// allSubSchemas returns all immediate sub-schemas of a schema for tree traversal.
// This is a generator-level helper (not tied to LocalResolver).
// Map-valued fields are iterated in sorted key order for determinism.
func allSubSchemas(s *schema.Schema) []*schema.Schema {
	var subs []*schema.Schema
	for _, k := range sortedKeys(s.Properties) {
		subs = append(subs, s.Properties[k])
	}
	subs = append(subs, s.TypeSchemas...)
	for _, k := range sortedKeys(s.PatternProperties) {
		subs = append(subs, s.PatternProperties[k])
	}
	for _, k := range sortedKeys(s.Defs) {
		subs = append(subs, s.Defs[k])
	}
	for _, k := range sortedKeys(s.Definitions) {
		subs = append(subs, s.Definitions[k])
	}
	subs = append(subs, s.AllOf...)
	subs = append(subs, s.AnyOf...)
	subs = append(subs, s.OneOf...)
	if s.Not != nil {
		subs = append(subs, s.Not)
	}
	if s.If != nil {
		subs = append(subs, s.If)
	}
	if s.Then != nil {
		subs = append(subs, s.Then)
	}
	if s.Else != nil {
		subs = append(subs, s.Else)
	}
	if s.Items != nil && s.Items.Schema != nil {
		subs = append(subs, s.Items.Schema)
	}
	if s.Items != nil {
		subs = append(subs, s.Items.Schemas...)
	}
	subs = append(subs, s.PrefixItems...)
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		subs = append(subs, s.AdditionalProperties.Schema)
	}
	if s.AdditionalItems != nil && s.AdditionalItems.Schema != nil {
		subs = append(subs, s.AdditionalItems.Schema)
	}
	for _, k := range sortedKeys(s.DependentSchemas) {
		subs = append(subs, s.DependentSchemas[k])
	}
	if s.Contains != nil {
		subs = append(subs, s.Contains)
	}
	if s.UnevaluatedProperties != nil {
		subs = append(subs, s.UnevaluatedProperties)
	}
	if s.UnevaluatedItems != nil {
		subs = append(subs, s.UnevaluatedItems)
	}
	return subs
}

// indexAnchors records the $id and $anchor of a definition for anchor-based resolution.
// It stores both the raw $id value and the canonicalized (resolved against base URI)
// form so that both relative and absolute lookups succeed.
//
// When a definition declares its own $id, it creates a new document scope.
// Its $anchor belongs to that scope, not the parent's, so a plain "#anchor"
// lookup from the parent scope must NOT match it. Instead, the anchor is
// registered under the $id-qualified form (e.g., "https://example.com/foo#anchor").
func (g *Generator) indexAnchors(def *schema.Schema, refPath string) {
	hasOwnScope := def.ID != "" || def.LegacyID != ""

	if def.ID != "" {
		g.anchors[def.ID] = refPath
		// Also store the canonicalized URI (resolved against base URI).
		if resolved := g.resolveRelativeURI(def.ID); resolved != "" && resolved != def.ID {
			g.anchors[resolved] = refPath
		}
	}
	if def.LegacyID != "" {
		g.anchors[def.LegacyID] = refPath
		if resolved := g.resolveRelativeURI(def.LegacyID); resolved != "" && resolved != def.LegacyID {
			g.anchors[resolved] = refPath
		}
	}
	if def.Anchor != "" {
		if hasOwnScope {
			// The anchor belongs to the $id's scope. Register it under the
			// $id-qualified URI so it can be found via "$id#anchor" but NOT
			// via a plain "#anchor" from the parent scope.
			if def.ID != "" {
				g.anchors[def.ID+"#"+def.Anchor] = refPath
				if resolved := g.resolveRelativeURI(def.ID); resolved != "" {
					g.anchors[resolved+"#"+def.Anchor] = refPath
				}
			}
			if def.LegacyID != "" {
				g.anchors[def.LegacyID+"#"+def.Anchor] = refPath
				if resolved := g.resolveRelativeURI(def.LegacyID); resolved != "" {
					g.anchors[resolved+"#"+def.Anchor] = refPath
				}
			}
		} else {
			g.anchors["#"+def.Anchor] = refPath
		}
	}
	// $dynamicAnchor is resolvable by both $ref and $dynamicRef.
	// For $ref resolution, treat it like $anchor.
	// Also track separately for $dynamicRef-specific resolution.
	if def.DynamicAnchor != "" {
		if hasOwnScope {
			if def.ID != "" {
				g.anchors[def.ID+"#"+def.DynamicAnchor] = refPath
				if resolved := g.resolveRelativeURI(def.ID); resolved != "" {
					g.anchors[resolved+"#"+def.DynamicAnchor] = refPath
				}
			}
		} else {
			g.anchors["#"+def.DynamicAnchor] = refPath
		}
		if g.dynamicAnchors != nil {
			g.dynamicAnchors["#"+def.DynamicAnchor] = refPath
		}
	}
}

// resolvedToFalseSchema checks if a property schema's $ref, $dynamicRef, or
// $recursiveRef resolves to a schema no instance satisfies ("always invalid").
// Both spellings count; see schemaForbidsEveryValue.
func (g *Generator) resolvedToFalseSchema(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	// Check $ref / $recursiveRef.
	if effRef := s.EffectiveRef(); effRef != "" {
		if resolved := g.resolveRefInContext(effRef, s); resolved != nil {
			return g.schemaForbidsEveryValue(resolved)
		}
	}
	// Check $dynamicRef.
	if s.DynamicRef != "" {
		if resolved := g.resolveDynamicRef(s.DynamicRef, s); resolved != nil {
			return g.schemaForbidsEveryValue(resolved)
		}
	}
	return false
}

// isScopedSelfRef returns true if the given ref, resolved from the context schema,
// points to the context schema's own document root (creating a recursive cycle).
// This is used to detect cases like $ref: "#" inside a sub-schema with $id
// that should generate a pointer type to break Go's recursive type restriction.
func (g *Generator) isScopedSelfRef(ref string, ctx *schema.Schema, resolved *schema.Schema) bool {
	if ctx == nil || resolved == nil {
		return false
	}
	// If the resolved schema is the context's own document root, it's a scoped self-ref.
	if ctx.DocumentRoot != nil && resolved == ctx.DocumentRoot {
		return true
	}
	// If the resolved schema IS its own document root (has $id) and appears in the
	// dynamic scope, we're inside its generation chain and using it as a value type
	// would create a recursive type cycle. Use a pointer to break the cycle.
	// Only applies to schemas that define their own scope ($id) — not schemas
	// merely defined within the root document.
	if resolved.DocumentRoot == resolved && resolved.ID != "" {
		for _, scope := range g.dynamicScope {
			if scope == resolved {
				return true
			}
		}
	}
	// Check if the resolved type has already been generated as a struct that
	// references a type currently being built. This detects indirect cycles
	// like A → B → A where A is already generated and B is being built.
	if len(g.structsInProgress) > 0 {
		goName := g.goNameForResolvedRef(ref, resolved, refToGoName(ref))
		if g.generated[goName] && g.typeReferencesAnyInProgress(goName) {
			return true
		}
	}
	return false
}

// typeReferencesAnyInProgress checks if a generated type has any value-type field
// that references a type currently being built (in structsInProgress).
func (g *Generator) typeReferencesAnyInProgress(typeName string) bool {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() != typeName {
			continue
		}
		sd, ok := td.(*StructDef)
		if !ok {
			return false
		}
		for _, field := range sd.Fields {
			fieldTypeName := extractTypeName(field.Type)
			if fieldTypeName != "" && g.structsInProgress[fieldTypeName] {
				return true
			}
		}
		return false
	}
	return false
}

// extractTypeName returns the Go type name from a GoType, stripping pointers/slices.
// Returns "" for primitive types or complex types that can't create cycles.
func extractTypeName(t GoType) string {
	switch tt := t.(type) {
	case *NamedType:
		if tt.Pointer {
			return "" // already a pointer, can't create a cycle
		}
		return tt.Name
	case *PointerType:
		return "" // already a pointer, can't create a cycle
	case *ArrayType:
		return "" // arrays are fine
	default:
		return ""
	}
}

// goNameForResolvedRef determines the Go type name for a resolved $ref.
// If the ref is a fragment-only ref (like "#") and the resolved schema is a scoped
// document root different from the main root, the name is derived from the resolved
// schema's title or $id rather than the raw ref string. This ensures that
// "$ref: '#'" inside a sub-schema with its own $id gets a meaningful Go name
// (e.g., "Tree") rather than the default "Root".
func (g *Generator) goNameForResolvedRef(ref string, resolved *schema.Schema, fallback string) string {
	if resolved == nil {
		return fallback
	}
	// A node already materialized under a name keeps it. Each traversal of a
	// self-referential document arrives with a different context-derived
	// fallback, so deriving a fresh name here is what let "$ref":"#" inside a
	// fetched meta-schema recurse without end.
	if existing, ok := g.nodeTypeNames[resolved]; ok {
		return existing
	}
	// Only re-derive the name when the ref would produce a misleading name.
	// This happens primarily for fragment-only refs like "#" or "#/..." that
	// resolved to a scoped document root (not the main root).
	if resolved == g.rootSchema {
		return fallback
	}
	// Check if the resolved schema is a known document root with its own $id.
	if resolved.DocumentRoot == resolved {
		// Use the title if available.
		if resolved.Title != "" {
			return SchemaNameToGoName(resolved.Title)
		}
		// Use the last segment of the $id.
		schemaID := resolved.ID
		if schemaID == "" {
			schemaID = resolved.LegacyID
		}
		if schemaID != "" {
			return SchemaNameToGoName(lastPathSegment(schemaID))
		}
	}
	return fallback
}

// lastPathSegment extracts the last meaningful segment from a URI path.
// e.g., "http://example.com/foo/bar" → "bar", "./tree" → "tree",
// "baseUriChangeFolder/" → "baseUriChangeFolder"
func lastPathSegment(uri string) string {
	// Strip fragment.
	if idx := strings.LastIndex(uri, "#"); idx >= 0 {
		uri = uri[:idx]
	}
	// Strip query.
	if idx := strings.LastIndex(uri, "?"); idx >= 0 {
		uri = uri[:idx]
	}
	// Strip trailing slash.
	uri = strings.TrimSuffix(uri, "/")
	// Get last path segment.
	if idx := strings.LastIndex(uri, "/"); idx >= 0 {
		return uri[idx+1:]
	}
	// No slash — might be scheme-less relative ref like "./tree".
	uri = strings.TrimPrefix(uri, "./")
	return uri
}

// rootIsObjectType returns true if the root schema is explicitly typed as an object
// (has type: "object"). Used to decide whether a self-reference should generate
// *Root (for object schemas) or json.RawMessage (for general schemas).
// Note: having properties alone is not sufficient — without explicit type: "object",
// the schema can validate non-object values (booleans, numbers, arrays, etc.).
func (g *Generator) rootIsObjectType() bool {
	if g.rootSchema == nil {
		return false
	}
	if primarySchemaType(g.rootSchema) == "object" {
		return true
	}
	// Schemas with properties or patternProperties are implicitly object types,
	// even without an explicit "type": "object".
	return hasProperties(g.rootSchema) || len(g.rootSchema.PatternProperties) > 0
}

// isSelfRef returns true if ref points to the root schema itself.
func (g *Generator) isSelfRef(ref string) bool {
	return g.isSelfRefInContext(ref, g.rootSchema)
}

// isSelfRefInContext returns true if ref points to the root schema itself,
// resolving relative refs against the given context schema's base URI.
func (g *Generator) isSelfRefInContext(ref string, ctx *schema.Schema) bool {
	if ref == "#" {
		// "#" in a scoped context points to the context's document root,
		// which is only the top-level root if the context IS the root or
		// the context has no $id of its own.
		if ctx != nil && ctx.DocumentRoot != nil && ctx.DocumentRoot != g.rootSchema {
			return false
		}
		return true
	}
	if g.rootID != "" && (ref == g.rootID || strings.TrimSuffix(ref, "#") == g.rootID) {
		return true
	}
	// Try resolving as a relative URI against the context's base URI.
	ctxBase := g.baseURI
	if ctx != nil && ctx.BaseURI != nil {
		ctxBase = ctx.BaseURI
	}
	if resolved := resolveRelativeURIAgainst(ref, ctxBase); resolved != "" {
		if resolved == g.rootID || strings.TrimSuffix(resolved, "#") == g.rootID {
			return true
		}
	}
	return false
}

// resolveRelativeURI resolves a relative URI against the generator's base URI.
// Returns the resolved absolute URI string, or "" if resolution is not possible.
func (g *Generator) resolveRelativeURI(ref string) string {
	return resolveRelativeURIAgainst(ref, g.baseURI)
}

// resolveRelativeURIAgainst resolves a relative URI against the given base URI.
// Returns the resolved absolute URI string, or "" if resolution is not possible.
func resolveRelativeURIAgainst(ref string, base *url.URL) string {
	refURL, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	if base == nil {
		// An absolute ref resolves to itself against any base, so a document
		// that declares no $id (leaving the base nil) must still be able to
		// look it up. Returning "" here hid $id-bearing subschemas of such a
		// document from every URI-keyed lookup, even though they were indexed.
		if refURL.IsAbs() {
			return refURL.String()
		}
		return ""
	}
	return base.ResolveReference(refURL).String()
}

// resolveRef looks up a $ref path using the root schema as context.
// This is a convenience wrapper around resolveRefInContext for callers that
// don't have a scoped context schema available.
func (g *Generator) resolveRef(ref string) *schema.Schema {
	return g.resolveRefInContext(ref, g.rootSchema)
}

// resolveBaseType determines the Go base type for an enum.
func (g *Generator) resolveBaseType(s *schema.Schema) GoType {
	pt := primarySchemaType(s)
	if pt != "" {
		t := PrimitiveTypeFromSchema(pt)
		if t != nil {
			return t
		}
	}
	// Infer from first enum value.
	if len(s.Enum) > 0 {
		switch s.Enum[0].(type) {
		case string:
			return &PrimitiveType{Name: "string"}
		case float64:
			return &PrimitiveType{Name: "float64"}
		case bool:
			return &PrimitiveType{Name: "bool"}
		}
	}
	return &PrimitiveType{Name: "string"}
}

// needsManualJSON returns true if the JSON property name contains characters
// that cannot be correctly represented in a Go struct tag (backtick-delimited
// raw string). Specifically: double quotes break tag value parsing, newlines
// break tag key:value parsing, carriage returns/form feeds are stripped
// or mishandled by the reflect.StructTag parser, and backticks terminate
// the raw string literal.
func needsManualJSON(jsonName string) bool {
	for _, r := range jsonName {
		switch r {
		case '"', '`', '\\', '\n', '\r', '\t', '\f':
			return true
		}
		// Any non-printable control character
		if r < 0x20 {
			return true
		}
	}
	return false
}

// isObjectProperty returns true if the Go type resolves to a struct (NamedType that
// is not an array) or the schema is an object with properties. Used to wrap optional
// struct fields in pointers for correct omitempty behavior.
func (g *Generator) isObjectProperty(goType GoType, propSchema *schema.Schema) bool {
	// A named type already emitted as a struct is an object property.
	if nt, ok := goType.(*NamedType); ok && g.isStructType(nt.Name) {
		return true
	}
	// Otherwise fall through to the property schema. The named type may be a
	// struct that is still mid-generation — this happens with mutually recursive
	// $ref/$dynamicRef chains (A → B → A), where the target struct has not been
	// appended to output.TypeDefs yet. Inspecting the resolved schema still
	// recognizes it as an optional object so the field is pointer-wrapped rather
	// than materializing as an always-present value struct (which marshals to
	// "{}" even when absent). Enum and slice/map aliases resolve to non-object
	// schemas here, so they are correctly not treated as object properties.
	if propSchema != nil {
		if primarySchemaType(propSchema) == "object" && hasProperties(propSchema) {
			return true
		}
		if effRef := propSchema.EffectiveRef(); effRef != "" {
			if resolved := g.resolveRefInContext(effRef, propSchema); resolved != nil {
				if primarySchemaType(resolved) == "object" && hasProperties(resolved) {
					return true
				}
			}
		}
	}
	return false
}

// isRawValueWrapperType reports whether t names a generated type that keeps the
// value as raw JSON and validates it after the fact: the wrappers built for a
// draft-3 schema-valued "type", a multi-type union, and an anyOf across
// unrelated representations (TypeOnlySchemaDef), for a bare "not"
// (NotSchemaDef), and for a schema constrained only by oneOf / anyOf /
// if-then-else (DynamicSchemaDef). Such a type is a struct with a custom
// MarshalJSON, so it is never omitted by omitempty and needs omitzero instead --
// without which an absent optional property of that type marshals as null and
// the document no longer round-trips.
func (g *Generator) isRawValueWrapperType(t GoType) bool {
	nt, ok := t.(*NamedType)
	if !ok {
		return false
	}
	for _, td := range g.output.TypeDefs {
		if td.TypeName() == nt.Name {
			switch td.(type) {
			case *TypeOnlySchemaDef, *NotSchemaDef, *DynamicSchemaDef, *AnnotationSchemaDef:
				return true
			}
			return false
		}
	}
	return false
}

// ruleTakesStringValue reports whether a rule's emitted code hands the field
// value to something declared to take a string (ecma262.MatchString,
// utf8.RuneCountInString, url.Parse, time.Parse, ...). The ipv4/ipv6 formats
// are the exception among "format" rules: they test a netip.Addr through its
// own methods and never touch the string value, so a conversion flag on them
// is inert rather than wrong.
func ruleTakesStringValue(ruleType string) bool {
	switch ruleType {
	case "minLength", "maxLength", "pattern", "format", "content":
		return true
	default:
		return false
	}
}

// isStringBackedNamedType reports whether t names a generated type whose
// underlying type is string, so `string(v)` converts it. Types not yet
// registered, and wrapper structs such as InferredAliasDef, answer false: a
// conversion emitted for those would not compile.
//
// A pointer is looked through: an optional property of such a type is
// pointer-wrapped so its "" survives the round trip, and the rules that ask
// this dereference the field before converting it.
func (g *Generator) isStringBackedNamedType(t GoType) bool {
	if pt, ok := t.(*PointerType); ok {
		t = pt.Inner
	}
	nt, ok := t.(*NamedType)
	if !ok {
		return false
	}
	return g.isStringBackedTypeName(nt.Name, 0)
}

func (g *Generator) isStringBackedTypeName(name string, depth int) bool {
	// Alias chains are short; the bound only stops a malformed cycle.
	if depth > 16 {
		return false
	}
	for _, td := range g.output.TypeDefs {
		if td.TypeName() != name {
			continue
		}
		switch def := td.(type) {
		case *AliasDef:
			return g.isStringBackedGoType(def.Underlying, depth)
		case *EnumDef:
			return g.isStringBackedGoType(def.BaseType, depth)
		default:
			return false
		}
	}
	return false
}

func (g *Generator) isStringBackedGoType(t GoType, depth int) bool {
	switch u := t.(type) {
	case *PrimitiveType:
		return u.Name == "string"
	case *NamedType:
		if u.Pointer || u.PkgAlias != "" {
			return false
		}
		return g.isStringBackedTypeName(u.Name, depth+1)
	default:
		return false
	}
}

// isEnumType returns true if a type name corresponds to an already-generated enum.
func (g *Generator) isEnumType(name string) bool {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() == name {
			_, isEnum := td.(*EnumDef)
			return isEnum
		}
	}
	return false
}

// isStructType returns true if a type name corresponds to an already-generated struct.
func (g *Generator) isStructType(name string) bool {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() == name {
			_, isStruct := td.(*StructDef)
			return isStruct
		}
	}
	return false
}

// isZeroLossyPrimitive returns true if the Go type is a primitive whose zero value
// would be lost with omitempty ("", false, int64=0, float64=0.0).
//
// time.Time and netip.Addr are worse off than the scalars, not better, and for
// the reason the whole rule exists. omitempty never omits a struct, so an
// optional property the document did not carry is not merely invisible -- it is
// *invented into the output*: an absent `format: date-time` marshals as
// "0001-01-01T00:00:00Z" through time.Time's own MarshalJSON, and an absent
// `format: ipv4` as "" through netip.Addr's MarshalText. Both are values the
// document never held and the schema never saw.
//
// ",omitzero" would omit them, since time.Time has IsZero and netip.Addr is
// comparable, but it omits by *value*: a document that genuinely carries
// "0001-01-01T00:00:00Z" would come back without the property. That trades
// inventing a value for dropping one, which is the same round-trip break in the
// other direction. The pointer distinguishes the two exactly -- nil is absent,
// non-nil is present whatever the instant -- which is the contract every other
// zero-lossy type here already has.
func isZeroLossyPrimitive(goType GoType) bool {
	pt, ok := goType.(*PrimitiveType)
	if !ok {
		return false
	}
	switch pt.Name {
	case "string", "bool", "int64", "float64", "time.Time", "netip.Addr":
		return true
	}
	return false
}

// isZeroLossyNamedType reports whether t names a generated type that has no
// representation for "absent" of its own — either because its underlying Go
// type is a zero-lossy primitive (a $ref to a "type":"string" definition, an
// inline enum, a const promoted to a single-value enum), or because it is a
// wrapper struct over one (see zeroLossyTypeName). The name does not give the
// value a nil to be absent in, so such a field loses a legitimate "", 0 or
// false to omitempty exactly as a bare primitive would.
//
// The answer comes from the generated type rather than from the property's
// schema because a $ref says nothing about the shape of its target. Both are
// available by the time this is asked: resolvePropertyType generates a ref
// target, and generateEnumDef an inline enum, before returning a name for it.
func (g *Generator) isZeroLossyNamedType(t GoType) bool {
	nt, ok := t.(*NamedType)
	if !ok || nt.Pointer {
		return false
	}
	if nt.PkgAlias != "" {
		// A type owned by another package of a cross-package run. The owning
		// generator ran this same predicate over it and published the answer;
		// a type whose owner has not been generated yet published nothing and
		// answers no.
		//
		// The answer is carried rather than re-derived from the published zero
		// literal, which is what this used to do. The two are not the same
		// question, and reading one for the other made a change to the literal
		// silently change which foreign fields got a pointer: an alias over
		// time.Time has no zero literal at all -- it is a struct -- yet it is
		// exactly the kind of type that needs one.
		return nt.foreignZeroLossy
	}
	return g.zeroLossyTypeName(nt.Name, 0)
}

func (g *Generator) zeroLossyTypeName(name string, depth int) bool {
	// Alias chains are short; the bound only stops a malformed cycle.
	if depth > 16 {
		return false
	}
	for _, td := range g.output.TypeDefs {
		if td.TypeName() != name {
			continue
		}
		switch def := td.(type) {
		case *AliasDef:
			return g.zeroLossyGoType(def.Underlying, depth)
		case *EnumDef:
			// A heterogeneous enum is backed by json.RawMessage, whose zero is
			// nil — absent already has a representation there.
			return g.zeroLossyGoType(def.BaseType, depth)
		case *InferredAliasDef, *BigIntAliasDef:
			// A wrapper struct over a scalar: the InferredAliasDef built for a
			// definition that carries constraints but no "type", and the
			// BigIntAliasDef that BigIntSupport puts over a named integer. It is
			// worse off than a named primitive, not better — omitempty never
			// omits a struct, so an absent optional property is fabricated into
			// the output as the wrapper's zero and then measured against the
			// definition's constraints. And unlike the raw-value wrappers below
			// it carries no IsZero to hand ",omitzero": its zero is exactly what
			// a present 0 or "" decodes to, so omitzero would drop a legitimate
			// value. The pointer is the only representation of absence left.
			return true
		default:
			// A struct is pointer-wrapped by isObjectProperty, and the raw-value
			// wrappers (TypeOnlySchemaDef, NotSchemaDef, DynamicSchemaDef) keep
			// the bytes they were handed: an absent one holds no bytes, which
			// their IsZero reports to ",omitzero" and their Validate treats as
			// nothing to check. Neither needs a pointer here.
			return false
		}
	}
	return false
}

func (g *Generator) zeroLossyGoType(t GoType, depth int) bool {
	switch u := t.(type) {
	case *PrimitiveType:
		return isZeroLossyPrimitive(u)
	case *NamedType:
		if u.Pointer || u.PkgAlias != "" {
			return false
		}
		return g.zeroLossyTypeName(u.Name, depth+1)
	}
	return false
}

// isNullOnly returns true if the schema's type is exclusively "null".
func isNullOnly(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	return len(s.Type) == 1 && s.Type[0] == "null"
}

// ---------- helpers ----------

// anyOfHasProperties checks whether at least one anyOf sub-schema (after resolving
// $ref pointers) contributes object properties. If none do, the anyOf is a union of
// primitives and should not be turned into a merged struct.
// Self-references ($ref: "#") are excluded because they point back to the root
// schema and don't represent actual property contributions from the anyOf variant.
// allOfNamesObjectKeys returns true if any allOf sub-schema contributes a named
// key -- a property, or a pattern the keys must match -- directly or via $ref
// resolution. Used by resolveType to decide whether a schema with allOf but no
// direct properties should generate a struct.
//
// patternProperties counts because the merge carries it into the struct exactly
// as it carries properties (see mergeAllOfBranches), so a branch that states one
// does produce a type with checks on it. Asking about properties alone left
// {"allOf":[{"type":"object","patternProperties":{...}}]} typed `any`.
func (g *Generator) allOfNamesObjectKeys(s *schema.Schema) bool {
	return g.allOfNamesObjectKeysOnPath(s, nil)
}

// allOfStatesUnmergeableOverflow reports whether an allOf says something about
// the keys of an object that the merged struct cannot say back.
//
// It is the complement of the narrow case the merge does take, and it is written
// from the same two predicates so the two cannot drift: some branch states an
// additionalProperties, no branch and no parent names a key, and
// soleBranchAdditionalProperties declines -- which it does exactly when more than
// one branch states the keyword, because satisfying both is an allOf of the two
// sub-schemas and one overflow value type cannot express that.
//
// Such a schema resolved to map[string]any in a property, an element or a map
// value, with no check anywhere: {"x":20} passed a schema whose second branch
// bounds every value at 9 (issue #112). At the document root the same schema is
// already answered by the runtime evaluator, which reads the allOf whole; this
// says that a position deserves the same answer.
func (g *Generator) allOfStatesUnmergeableOverflow(s *schema.Schema) bool {
	if s == nil || len(s.AllOf) == 0 || !g.validationKeywordsEnabled() {
		return false
	}
	if namesObjectKeys(s) || s.AdditionalProperties != nil || g.allOfNamesObjectKeys(s) {
		return false
	}
	if g.soleBranchAdditionalProperties(s.AllOf, make(map[*schema.Schema]bool)) != nil {
		return false
	}
	return g.allOfBranchStatesAdditionalProperties(s.AllOf, make(map[*schema.Schema]bool))
}

// allOfBranchStatesAdditionalProperties reports whether any branch states an
// additionalProperties at all. It walks the same routes into a branch that
// soleBranchAdditionalProperties does -- a $ref chain, a nested allOf -- so the
// two answer about the same set of branches.
func (g *Generator) allOfBranchStatesAdditionalProperties(allOf []*schema.Schema, onPath map[*schema.Schema]bool) bool {
	for _, sub := range allOf {
		if sub == nil || onPath[sub] {
			continue
		}
		onPath[sub] = true
		resolved := sub
		for {
			if resolved.AdditionalProperties != nil {
				return true
			}
			if g.allOfBranchStatesAdditionalProperties(resolved.AllOf, onPath) {
				return true
			}
			effRef := resolved.EffectiveRef()
			if effRef == "" {
				break
			}
			r := g.resolveRefInContext(effRef, resolved)
			if r == nil || onPath[r] {
				break
			}
			onPath[r] = true
			resolved = r
		}
	}
	return false
}

// overflowAllOfWrapperType materializes the runtime-evaluator wrapper for a
// position whose schema is the allOf allOfStatesUnmergeableOverflow describes.
//
// The evaluator rather than a widened merge, deliberately. Widening
// soleBranchAdditionalProperties to combine several branches would route the
// schema into the typed overflow map, which reads {"additionalProperties":
// {"minimum":5}} as map[string]float64 -- and {"x":"abc"} then fails to decode,
// though the schema admits it, because `minimum` says nothing about a string.
// The evaluator judges the decoded JSON value instead, so a string, a null and
// an object all pass and only an out-of-range number is refused, which is what
// the root already does for the same document.
//
// A schema the evaluator cannot read is left exactly where it was. Minting a
// named `any` alias in its place would trade a map[string]any the caller can use
// for an interface it cannot, and buy no check with it.
func (g *Generator) overflowAllOfWrapperType(s *schema.Schema, contextName string) (GoType, bool) {
	if !g.allOfStatesUnmergeableOverflow(s) {
		return nil, false
	}
	if g.generated[contextName] {
		return &NamedType{Name: contextName}, true
	}
	def := g.runtimeSchemaDef(contextName, s)
	if def == nil {
		return nil, false
	}
	g.generated[contextName] = true
	g.output.TypeDefs = append(g.output.TypeDefs, def)
	return &NamedType{Name: contextName}, true
}

// allOfBuildsObjectType reports whether generateAllOfDef would answer this
// schema's allOf with a type that carries object checks, which is what
// resolveType needs to know before delegating to it rather than typing the
// value from the schema's own (absent) keywords.
//
// The second arm mirrors the narrow case the merge takes: no branch names a key,
// and exactly one states an additionalProperties the merged struct will enforce
// -- a schema value, whose keywords are checked against the overflow map, or the
// boolean false, which rejects it outright. Those are the two
// propertylessObjectHasChecks admits, so the delegation and the merge agree on
// which allOfs produce a type worth naming. A boolean `true` is neither.
func (g *Generator) allOfBuildsObjectType(s *schema.Schema) bool {
	if s == nil || len(s.AllOf) == 0 {
		return false
	}
	if g.allOfNamesObjectKeys(s) {
		return true
	}
	// The parent's own keys are the other half of what the merge sees, and the
	// merge's overflow arm requires the merged schema to name none at all. The
	// arm above answered for the branches; this answers for the parent, so the
	// two conditions are the same one.
	if namesObjectKeys(s) {
		return false
	}
	ap := g.soleBranchAdditionalProperties(s.AllOf, make(map[*schema.Schema]bool))
	if ap == nil {
		return false
	}
	if ap.Bool != nil {
		return !*ap.Bool
	}
	return ap.Schema != nil && !ap.Schema.IsBooleanSchema()
}

// allOfNeedsNamedType reports whether an allOf has to be materialized into a
// named type for the value to keep what the allOf says about it.
//
// The properties case is the one this arm was built for: the merged struct is
// the only place the branches' fields can live. The scalar case is the same
// argument one type down. resolveType has no arm that reads an allOf, so a
// property whose whole schema is {"allOf":[{"$ref":"#/$defs/Stamp"}]} fell past
// every arm to `any` -- and `any` carries no Validate and is filtered out of the
// field's own rules, so the Go type and every constraint the branch states were
// both gone. `type Stamp time.Time` sitting correctly generated in the same file
// made no difference: it is the *position* that lost it.
//
// The second condition is what keeps this from claiming schemas that already
// resolve. Anything on s itself that gives resolveType an answer -- a type, a
// $ref, an enum, properties, array or object structure, a sibling composition --
// disqualifies it, because those arms know things the merge does not and taking
// the schema over would drop them. What is left is a schema whose allOf is the
// only thing saying what the value is.
func (g *Generator) allOfNeedsNamedType(s *schema.Schema) bool {
	if g.allOfBuildsObjectType(s) {
		return true
	}
	if s == nil || len(s.AllOf) == 0 {
		return false
	}
	// A branch that is the boolean `false`, or a $ref to one, makes the whole
	// allOf unsatisfiable, and generateAllOfDef answers exactly that with the
	// forbidden wrapper. Nothing else can: an inline position typed from the
	// keywords that survive the merge -- or from `any` when none do -- drops the
	// rejection entirely, which is #116 everywhere the schema is resolved rather
	// than named.
	//
	// It is asked before the disqualifying keywords below, because none of them
	// can make an unsatisfiable schema satisfiable: {"type":"string","allOf":[false]}
	// admits no string either.
	if g.allOfContainsFalseSchema(s.AllOf) {
		return true
	}
	if len(s.Type) > 0 || len(s.TypeSchemas) > 0 || hasProperties(s) ||
		len(s.PatternProperties) > 0 || s.AdditionalProperties != nil ||
		s.EffectiveRef() != "" || s.DynamicRef != "" || s.RecursiveRef != "" ||
		len(s.Enum) > 0 || s.Const != nil || s.ConstIsNull ||
		len(s.AnyOf) > 0 || len(s.OneOf) > 0 ||
		s.Items != nil || len(s.PrefixItems) > 0 {
		return false
	}
	return g.allOfNamesATypeOnPath(s, nil)
}

// allOfNamesATypeOnPath reports whether some branch of the allOf states a
// keyword that fixes the value's Go type. It follows $refs and nested allOf
// chains exactly as allOfHasPropertiesOnPath does, and carries the same on-path
// set for the same reason: {"allOf":[{"$ref":"#"}]} would otherwise re-enter
// forever.
//
// "type", "enum" and "const" state a type outright. A bound counts too, through
// the same inferTypeFromConstraints the no-allOf path has always used --
// minLength/maxLength/pattern say the schema is about a string, the numeric
// bounds that it is about a number -- and the merge answers such a branch with
// the InferredAliasDef wrapper, which applies the bound to a matching value and
// accepts every other instance type unchanged. Without a bound here,
// {"allOf":[{"minLength":3}]} written inline resolved to `any` and the bound was
// enforced nowhere.
//
// Asking inferTypeFromConstraints rather than listing keywords is what keeps
// this from drifting: it is the same function the merge will run on the merged
// schema, so a branch this answers yes for is one generateAllOfDef can type.
func (g *Generator) allOfNamesATypeOnPath(s *schema.Schema, onPath map[*schema.Schema]bool) bool {
	if s == nil || len(s.AllOf) == 0 || onPath[s] {
		return false
	}
	if onPath == nil {
		onPath = make(map[*schema.Schema]bool)
	}
	onPath[s] = true
	defer delete(onPath, s)
	for _, sub := range s.AllOf {
		if sub == nil {
			continue
		}
		if g.branchNamesAType(sub) {
			return true
		}
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				if g.branchNamesAType(r) {
					return true
				}
				if g.allOfNamesATypeOnPath(r, onPath) {
					return true
				}
			}
		}
		if g.allOfNamesATypeOnPath(sub, onPath) {
			return true
		}
	}
	return false
}

func (g *Generator) branchNamesAType(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	if len(s.Type) > 0 || len(s.Enum) > 0 || s.Const != nil || s.ConstIsNull {
		return true
	}
	// A format names a type in the sense this asks about: the merge answers such
	// a branch with the wrapper stringAnnotationOnlyDef builds, which checks the
	// format when the value is a string and accepts every other instance type.
	// Without it, {"allOf":[{"format":"ipv4"}]} written inline resolved to `any`
	// and the format was enforced nowhere, while the same branch behind a $ref
	// was checked -- the position half of issue #106.
	//
	// Not asked of inferTypeFromConstraints, which deliberately does not read
	// "format": a format states nothing about the *Go* type, only about what a
	// string instance must look like, so inferring "string" there would narrow
	// every position that consults it and reject the numbers the schema allows.
	//
	// The content vocabulary is the same keyword shape and gets the same answer
	// for the same reason -- it is the other half of what stringAnnotationOnlyDef
	// builds a wrapper for, and inferTypeFromConstraints must go on not reading
	// it, or {"contentEncoding":"base64"} would become a Go string and refuse the
	// numbers and objects the schema admits (issue #115).
	if s.Format != nil && FormatCheckableOnString(*s.Format) {
		return true
	}
	if statesContentVocabulary(s) {
		return true
	}
	return g.inferTypeFromConstraints(s) != ""
}

// allOfNamesObjectKeysOnPath is allOfNamesObjectKeys carrying the set of schemas
// whose allOf the search is already inside. Both recursions below follow $ref,
// so an allOf branch pointing back at the schema that owns it --
// {"allOf": [{"$ref": "#"}]} -- re-enters this function forever, a stack
// overflow. A schema already on the path is having its branches examined in the
// frame above, so the repeat has nothing to add and answers no; if a property
// is there to be found, that frame finds it. The mark comes off on the way out,
// leaving sibling branches that name the same schema free to answer for
// themselves, and the set is only allocated for a schema that has an allOf.
func (g *Generator) allOfNamesObjectKeysOnPath(s *schema.Schema, onPath map[*schema.Schema]bool) bool {
	if s == nil || len(s.AllOf) == 0 || onPath[s] {
		return false
	}
	if onPath == nil {
		onPath = make(map[*schema.Schema]bool)
	}
	onPath[s] = true
	defer delete(onPath, s)
	for _, sub := range s.AllOf {
		if namesObjectKeys(sub) {
			return true
		}
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				if namesObjectKeys(r) {
					return true
				}
				// Recursively check resolved schema's allOf chain.
				if g.allOfNamesObjectKeysOnPath(r, onPath) {
					return true
				}
			}
		}
		// Recursively check nested allOf.
		if g.allOfNamesObjectKeysOnPath(sub, onPath) {
			return true
		}
	}
	return false
}

func (g *Generator) anyOfHasProperties(s *schema.Schema) bool {
	for _, sub := range s.AnyOf {
		// Check direct properties on the sub-schema itself.
		if len(sub.Properties) > 0 {
			return true
		}
		// Resolve $ref, but skip self-references to avoid misattributing
		// the root schema's properties to this anyOf variant.
		if effRef := sub.EffectiveRef(); effRef != "" && !g.isSelfRefInContext(effRef, sub) {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				if len(r.Properties) > 0 {
					return true
				}
			}
		}
	}
	return false
}

// oneOfDescribesObject returns true if any oneOf variant constrains the shape of
// an object. That covers variants with properties, but also variants carrying only
// object-applicable keywords — {"required":["a","b"]} constrains an object even
// though it declares no properties. Such a oneOf is an object union and must be
// generated as a struct so the branch checks are emitted; a constraint-only oneOf
// over scalars (e.g. [{"minimum":10},{"maximum":5}]) is not, and falls through to
// the alias paths where its branches attach to the declared/inferred type.
func (g *Generator) oneOfDescribesObject(s *schema.Schema) bool {
	for _, sub := range s.OneOf {
		if g.constrainsObjectShape(sub) {
			return true
		}
		if effRef := sub.EffectiveRef(); effRef != "" && !g.isSelfRefInContext(effRef, sub) {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				if g.constrainsObjectShape(r) {
					return true
				}
			}
		}
	}
	return false
}

// constrainsObjectShape reports whether a schema says something about the shape of
// an object: properties/patternProperties, an explicit "object" type, or one of the
// keywords that only apply to objects. Mirrors the object-keyword list that
// inferTypeFromConstraints uses to infer type "object".
func (g *Generator) constrainsObjectShape(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	if len(s.Properties) > 0 || len(s.PatternProperties) > 0 {
		return true
	}
	if primarySchemaType(s) == "object" {
		return true
	}
	if s.AdditionalProperties != nil || s.UnevaluatedProperties != nil || s.PropertyNames != nil {
		return true
	}
	if g.validationKeywordsEnabled() && (len(s.Required) > 0 ||
		len(s.DependentRequired) > 0 || len(s.DependentSchemas) > 0 ||
		s.MinProperties != nil || s.MaxProperties != nil) {
		return true
	}
	return false
}

// hasProperties returns true if the schema defines any properties.
func hasProperties(s *schema.Schema) bool {
	return len(s.Properties) > 0
}

// namesObjectKeys reports whether an object schema decides its keys one group at
// a time -- by naming them under `properties`, or by naming the patterns they
// have to match under `patternProperties`. Either way the object is not one map
// of uniform values, so it is a struct: the generated type keeps a field per
// declared property and a pattern bucket beside it, and the sub-schemas hang off
// those.
//
// It is the object half of the condition generateTypeDef has always used to
// route a schema to generateStructDef. resolveType used hasProperties alone, so an object whose
// entire shape was patternProperties had nothing to declare, fell past the
// object arm and came out map[string]any -- with the patterns unmatched, the
// value sub-schemas unchecked and any sibling additionalProperties dropped
// (issue #96, and the map half of #98). The two now agree, so a node cannot be
// routed to a struct when it is named and to a bare map when it is written
// inline.
func namesObjectKeys(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	return hasProperties(s) || len(s.PatternProperties) > 0
}

// forbidsEveryKey reports whether an object schema permits no key at all: its
// additionalProperties is the boolean false and it names none of its keys, so
// every key an instance could carry is an additional one and forbidden. Only
// {} satisfies such a schema.
//
// The boolean is read the way generatePropertylessObjectDef reads it -- the
// Bool arm of the SchemaOrBool -- so the predicate admits exactly the schemas
// the struct it asks for will actually enforce.
func forbidsEveryKey(s *schema.Schema) bool {
	if s == nil || namesObjectKeys(s) {
		return false
	}
	ap := s.AdditionalProperties
	return ap != nil && ap.Bool != nil && !*ap.Bool
}

// constrainsObjectShape reports whether an object schema demands something of
// the object as a whole that a bare Go map has nowhere to hold.
//
// The six keywords are the ones generatePropertylessObjectDef already emits
// checks for, and a $defs entry stating any of them has enforced it since long
// before this: {"$defs":{"N":{"type":"object","propertyNames":false}}} refuses
// {"a":1} through the named type's Validate. Written inline the same schema came
// out map[string]any -- a type with no Validate for a check to live in -- and
// accepted it. That is the second half of issue #146, and it is the same
// inline-versus-named asymmetry #113, #114, #116, #126, #137, #139 and #142
// closed one position at a time; here the position is the object itself.
//
// It matters to the first half as well. propertyNames and dependentSchemas are
// two of the six keywords that drop a forbidding sub-schema, and inline they
// were not dropping the *sub-schema* -- they were not being read at all, for
// `false` as much as for {"not":{}}. #142's fixture reaches both through a $defs
// entry and records why in a $comment; with this, the inline spelling answers
// too and the workaround is no longer the only way to test them.
//
// Three things are excluded, and only three. A schema-valued
// additionalProperties keeps the exclusion objectIsStruct already states: that
// schema describes the values, one for all of them, and it is a Go map. Naming
// the position would keep the value typing -- the struct's overflow map is
// map[string]string either way -- and gain the shape check with it, but it would
// take map[string]string away from the *field*, which is the whole of #84, so
// such a schema keeps the map and the keyword stays unenforced there. The golden
// is where that trade is pinned.
//
// A stated type has to be one the propertyless struct can hold, and an enum, a
// const or a draft-3 type alternative takes the schema away altogether. Both are
// the same rule: materializing a schema some other arm answers does not leave
// its type alone, it replaces it. {"type":"string","required":["a"]} describes
// strings and a struct refuses "abc" at the decoder; {"required":["y"],
// "enum":[1,2]} is an enum, and read as an object shape it rendered a union
// whose selection tested `required` against the number 1 -- which satisfies the
// branch, since `required` speaks only about objects -- and refused a document
// the schema permits.
//
// Nothing else is refused, and the usual fail-closed reading is upside down
// here. Answering yes routes the position to generateTypeDef under a name, which
// is the whole ladder rather than one rung of it, so a keyword this predicate has
// never heard of is answered there at least as well as map[string]any answers it
// -- which is not at all. It is the fallback that is lossy, so a gate refusing
// every unfamiliar keyword cost checks and bought nothing: it left
// {"type":"object","required":["a"],"unevaluatedProperties":false} a bare map.
//
// The exclusions are written against the struct fields rather than a re-marshaled
// key set for the reason emptyEnumSchema records: `enum` is tagged omitempty and
// `const: null` leaves no key at all, so a key-set gate cannot see either.
func constrainsObjectShape(s *schema.Schema) bool {
	if s == nil || mapValueSchema(s, "object") != nil {
		return false
	}
	if s.PropertyNames == nil && len(s.DependentSchemas) == 0 &&
		len(s.DependentRequired) == 0 && len(s.Required) == 0 &&
		s.MinProperties == nil && s.MaxProperties == nil {
		return false
	}
	// A stated type has to be one the propertyless struct answers. "object"
	// alone is the plain case and ["object","null"] the nullable one, which
	// resolveType's nullable arm materializes through this same predicate.
	for _, t := range s.Type {
		if t != "object" && t != "null" {
			return false
		}
	}
	// The enum arm of resolveType, and generateEnumDef behind it, answer these
	// better than the struct does.
	if s.Enum != nil || s.Const != nil || s.ConstIsNull || len(s.TypeSchemas) > 0 {
		return false
	}
	return true
}

// objectIsStruct reports whether an object schema has to be materialized rather
// than answered with a bare Go map: it names its keys, it forbids every key, or
// it constrains the object's shape in a way no map can carry.
//
// The second is not a map because there is nothing for a map to hold, and it is
// the shape resolveType used to collapse to map[string]any --
// {"type":"object","additionalProperties":false} written as a property accepted
// {"x":1}, while the identical schema in a $defs entry has always rejected it
// through the Forbidden overflow map generatePropertylessObjectDef emits. The
// named position was right and the inline one was the outlier. The third is that
// same reading applied to the rest of what a propertyless object can state; see
// constrainsObjectShape.
//
// What is deliberately absent is a *schema-valued* additionalProperties: that
// does describe the values, one schema for all of them, and it is a Go map --
// which is the whole of #84 and must not be undone here.
func objectIsStruct(s *schema.Schema) bool {
	return namesObjectKeys(s) || forbidsEveryKey(s) || constrainsObjectShape(s)
}

// objectShapeNeedsNamedType reports whether an object schema states something
// that only a generated type of its own can enforce -- because the position
// holding it dispatches through a Validate method and has no other way to reach
// the schema.
//
// Three shapes qualify, and they are the three generateTypeDef already answers
// with a type carrying checks: an object that names its keys (a struct, see
// namesObjectKeys), an object that forbids every key (a struct whose overflow
// map is rejected outright), and an object whose whole shape is a schema-valued
// additionalProperties (a struct whose overflow map is checked against that
// sub-schema).
//
// A boolean `true` is not one of them: it permits every key and says nothing
// about any of them, so there is nothing for a Validate to carry.
func (g *Generator) objectShapeNeedsNamedType(s *schema.Schema) bool {
	if s == nil || s.IsBooleanSchema() {
		return false
	}
	return objectIsStruct(s) || mapValueSchema(s, g.effectiveType(s)) != nil
}

// resolveArrayItemType resolves the Go type for an array's items schema.
// An inline oneOf-only items schema describing an object union is materialized
// as a named sealed-interface type — the same output an items $ref to a named
// oneOf definition already produces — instead of collapsing to any and losing
// the element typing entirely.
//
// The guard mirrors generateTypeDef's own struct condition (oneOfDescribesObject
// with no properties of its own), so the name introduced here is always backed
// by the sealed struct. A constraint-only oneOf over scalars does not qualify
// and falls through to the alias paths, where its branches attach to the
// declared or inferred type.
func (g *Generator) resolveArrayItemType(items *schema.Schema, itemContext string) GoType {
	// An element (or map value) whose sub-schema admits nothing. See
	// forbiddingInlineType, and issue #142.
	//
	// First, not last, and that is the one place in this ladder where the
	// ordering rule is inverted. Every arm below answers "what Go type holds the
	// values this schema admits", and the answer here is that there are none, so
	// an arm that types the position can only be describing values the schema
	// forbids: {"type":"string","enum":[]} is a string type over an empty set,
	// and []string then accepts every string. generateTypeDef puts its two
	// forbidding arms ahead of the enum and type arms for the same reason.
	if goType, ok := g.forbiddingInlineType(items, itemContext); ok {
		return goType
	}
	// An element (or map value) whose unevaluatedItems only the runtime
	// annotation evaluator can settle. Same reasoning as the property arm in
	// resolvePropertyType: nothing below names the position, so the keyword
	// would be enforced nowhere. It matters here in particular because the
	// per-element checks cannot stand in -- they read the element schema's own
	// prefixItems, and a prefix behind an allOf is not that.
	if g.inlineAnnotationWrapper(items) {
		_ = g.generateTypeDef(itemContext, items)
		if g.generated[itemContext] {
			return &NamedType{Name: itemContext}
		}
	}
	if items.EffectiveRef() == "" && len(items.OneOf) > 0 && !hasProperties(items) && g.oneOfDescribesObject(items) {
		_ = g.generateTypeDef(itemContext, items)
		return &NamedType{Name: itemContext}
	}
	// An inline integer element of an array (or value of a map) is the same gap
	// as an inline integer property: []int64 cannot hold what BigIntSupport is
	// for. See bigIntInlineWrapper.
	if g.bigIntInlineWrapper(items) {
		_ = g.generateTypeDef(itemContext, items)
		return &NamedType{Name: itemContext}
	}
	// An element or map value whose "type" names no single Go type -- a union of
	// two JSON types, or "null" alone. resolveType answers *any for both, and a
	// *any decodes a null and every other value besides, so
	// {"items":{"type":"null"}} accepted [1] and {"items":{"type":["string",
	// "number"]}} accepted [true]. The property position has always materialized
	// this shape; the element and map-value ones came through resolveType and did
	// not. Part of issue #126.
	//
	// extractTypeOnlySchemaDef is narrow by construction and is what keeps this
	// from claiming anything else: it declines a single non-null type, so
	// []string and *string are untouched, and it declines a schema stating any
	// keyword besides the type.
	if !g.generated[itemContext] {
		if def := g.extractTypeOnlySchemaDef(itemContext, items); def != nil {
			g.generated[itemContext] = true
			g.output.TypeDefs = append(g.output.TypeDefs, def)
			return &NamedType{Name: itemContext}
		}
	}
	// An element (or map value) whose schema states no "type" and would be given
	// one by resolveType, read off a validation keyword. {"type":"array","items":
	// {"minimum":5}} typed the slice []float64, so ["abc"] died in the decoder
	// and [null] was measured against the bound as the Go zero, although
	// `minimum` says nothing about a string or a null. See boxedInferredType, and
	// issue #139 -- which is #137's third row reached from the element position.
	//
	// The map-value callers reach this through the same tail, so a ["object",
	// "null"] whose values are constraint-only is boxed here rather than only in
	// the non-nullable map arm resolveType boxes directly.
	//
	// Last, after the arms above, for the reason every other position puts it
	// last: a schema one of them can type is typed by it.
	if goType, ok := g.boxedInferredType(items, itemContext); ok {
		return goType
	}
	return g.resolveType(items, itemContext)
}

// hasRefStructuralSiblings reports whether a schema carrying a $ref also states
// something the reference must not swallow -- so the position has to merge the
// two rather than alias the target.
//
// Every caller pairs it with refOverridesSiblingsForSchema, which is the draft
// half of the same question: through draft-07 a $ref replaces everything beside
// it and this predicate is never consulted, while from 2019-09 on $ref is an
// ordinary applicator and a sibling applies as if the two were an allOf. That
// split is why the list here is a list of *keywords* and carries no draft test
// of its own.
//
// "type" belongs on it for the same reason the properties and items families
// do, and its absence was #118: {"type":"array","$ref":"#/$defs/a"} took the
// ref-only path in every draft, so the alias described the target alone and the
// declared type was dropped -- 2020-12 documents of any other type were accepted
// where the identical schema without the $ref generates []any with a Validate.
// Adding it here rather than at the individual call sites is what keeps the
// draft split correct: draft-07 and earlier short-circuit before this is
// reached, so they still suppress the sibling, and only 2019-09 onward changes.
//
// "enum" and "const" join it for exactly that reason, and that is #153. They are
// what a type ladder reads first, so where the other keywords on this list lost
// the *sibling* to the reference these lost the *reference* to the sibling:
// {"$defs":{"Long":{"type":"string","minLength":5}},"$ref":"#/$defs/Long",
// "const":"abc"} became the const's own enum type, the $ref was never followed,
// and "abc" was accepted although the target forbids it. refMergesSiblingValues
// is what stands the enum arms down so the merge this predicate selects is
// reached; on draft-07 and earlier refDisplacesSiblingValues stands them down
// instead and the short-circuit above keeps the reference alone, so #151's
// answer there is untouched.
func hasRefStructuralSiblings(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	return len(s.Type) > 0 || hasProperties(s) || len(s.PatternProperties) > 0 || s.UnevaluatedProperties != nil || s.AdditionalProperties != nil ||
		len(s.PrefixItems) > 0 || s.Items != nil || s.UnevaluatedItems != nil ||
		statesEnumOrConst(s)
}

// primarySchemaType returns the primary (first non-null) type from the type list.
// schemaAllowsNull returns true if the schema's type list includes "null"
// or if there is no explicit type (which means any type is allowed).
func schemaAllowsNull(s *schema.Schema) bool {
	if len(s.Type) == 0 {
		return true // no explicit type constraint — any type allowed
	}
	for _, t := range s.Type {
		if t == "null" {
			return true
		}
	}
	return false
}

// maxNullRefDepth bounds how far schemaForbidsNull follows $refs and
// composition. A recursive definition would otherwise walk itself forever;
// nothing legitimate needs anywhere near this many hops, and stopping early only
// ever loses a rejection.
const maxNullRefDepth = 12

// schemaForbidsNull reports whether a schema positively excludes a JSON null at
// its own position.
//
// It is deliberately not the negation of schemaAllowsNull, which answers from
// the type list alone and so says "allowed" for every schema that states no
// "type" -- including a bare {"$ref": ...}, which is how a property behind a
// definition escaped the check entirely. The question here is the one the
// generated decoder has to answer, so it follows the keywords that can settle
// it and gives up on the ones that cannot:
//
//   - "type" that lists something and not "null" settles it outright.
//   - $ref is conjunctive with its siblings from 2019-09 on, and *is* the whole
//     schema before that, so either way the target's answer counts.
//   - allOf is a conjunction: one branch that excludes null excludes it.
//   - anyOf and oneOf are disjunctions: null survives if any branch admits it,
//     so they exclude it only when every branch does.
//   - enum and const enumerate the permitted values, and null is excluded when
//     it is not among them.
//
// if/then/else and not are left alone. A `then` binds only when the condition
// held, and reading it as a conjunction would refuse documents the schema
// allows -- a false rejection, which is worse than the missed one it replaces.
// Everything this cannot decide answers false, so the check only ever narrows
// to what the schema demonstrably forbids.
func (g *Generator) schemaForbidsNull(s *schema.Schema) bool {
	return g.schemaForbidsNullAt(s, 0)
}

func (g *Generator) schemaForbidsNullAt(s *schema.Schema, depth int) bool {
	if s == nil || depth >= maxNullRefDepth || s.IsBooleanSchema() {
		return false
	}
	ref := s.EffectiveRef()
	if ref != "" && g.refOverridesSiblings() {
		// Before 2019-09 a $ref replaces everything beside it, so the siblings
		// have no say at all. Reading them anyway would refuse a null the
		// target admits.
		target := g.resolveRefInContext(ref, s)
		return target != nil && g.schemaForbidsNullAt(target, depth+1)
	}
	if len(s.Type) > 0 && !schemaAllowsNull(s) {
		return true
	}
	if ref != "" {
		if target := g.resolveRefInContext(ref, s); target != nil && g.schemaForbidsNullAt(target, depth+1) {
			return true
		}
	}
	for _, branch := range s.AllOf {
		if g.schemaForbidsNullAt(branch, depth+1) {
			return true
		}
	}
	for _, group := range [][]*schema.Schema{s.AnyOf, s.OneOf} {
		if len(group) == 0 {
			continue
		}
		everyBranch := true
		for _, branch := range group {
			if !g.schemaForbidsNullAt(branch, depth+1) {
				everyBranch = false
				break
			}
		}
		if everyBranch {
			return true
		}
	}
	// {"const": null} is spelled with ConstIsNull because Go's decoder leaves
	// *any nil for a JSON null, so a null const and an absent one are the same
	// pointer. Asking the flag keeps the two apart.
	if s.ConstIsNull {
		return false
	}
	if s.Const != nil {
		return *s.Const != nil
	}
	if len(s.Enum) > 0 {
		for _, v := range s.Enum {
			if v == nil {
				return false
			}
		}
		return true
	}
	return false
}

// nullPresenceTracked reports whether a property needs the fact that its value
// arrived as a JSON null recorded alongside the decoded struct.
//
// It needs it wherever the schema does not forbid the null -- so the decoder
// keeps it rather than refusing it -- and the Go value cannot say that it was
// there. A nil pointer, a nil slice or map, a nil interface and an untouched
// scalar zero are all exactly what an absent property leaves too, so without the
// record "present and null" and "absent" are one state: the null is dropped on
// the way out, and any keyword the property states is measured against a zero
// the document never supplied.
//
// The two wrapper structs are excluded because they already hold the answer.
// Both keep the bytes they were handed, so a null is still a null in the decoded
// value: the raw-value wrapper reports absence through IsZero, which is what the
// ",omitzero" tag drops, and the inferred-alias wrapper marshals its raw bytes
// back and returns early from Validate for a value that is not of its type.
//
// Only while the field holds the wrapper itself. Behind a pointer encoding/json
// never reaches it: for a JSON null the decoder sets the pointer to nil and the
// wrapper's UnmarshalJSON is not called at all, so its raw bytes stay as empty
// as an absent property's and the record is again the only thing that can say
// the null was there. That is the shape an optional constraint-only property
// takes -- {"properties":{"boundOnly":{"minLength":2}}} is typed by the
// inferred-alias wrapper and then pointer-wrapped for omitempty -- and a $ref to
// the same sub-schema has had it since long before (issue #139 gave the inline
// spelling the type the $ref spelling already had, and this is what keeps
// #110's record where the pointer puts the null out of the wrapper's reach).
//
// schemaForbidsNull is deliberately the question rather than "does the schema
// list null": it is the same predicate the rejection half (issue #103) uses, so
// exactly the properties that path lets through are the ones this one catches,
// and no property is claimed by both or by neither.
func (g *Generator) nullPresenceTracked(propSchema *schema.Schema, t GoType) bool {
	if propSchema == nil || g.schemaForbidsNull(propSchema) {
		return false
	}
	if t != nil && !t.IsPointer() && (g.isRawValueWrapperType(t) || g.isInferredAliasType(t)) {
		return false
	}
	return true
}

// ruleVacuousForNull reports whether a JSON null satisfies a rule of this type
// whatever its argument, because the keyword speaks about some other JSON type.
//
// It is the null column of ruleKeywordJSONKinds, which no keyword there names --
// minLength speaks about strings, minimum about numbers, minItems about arrays,
// and a null is none of them. "format" is asked separately because it is not in
// that table at all: it is built only for a schema whose type is a string or
// unstated, and against a null it is as vacuous as the rest.
//
// Everything else answers false, which is the safe direction: const, enum and
// the forbidding rules judge a null on the same terms as any other value, and a
// rule this cannot place keeps its check.
func ruleVacuousForNull(ruleType string) bool {
	if _, judged := ruleKeywordJSONKinds[ruleType]; judged {
		return true
	}
	return ruleType == "format"
}

func primarySchemaType(s *schema.Schema) string {
	// Count distinct non-null types. If there are multiple incompatible types
	// (e.g., ["array", "object"] or ["integer", "string"]), return "" so that
	// resolveType falls back to `any` — Go can't represent a union type.
	var nonNull []string
	for _, t := range s.Type {
		if t != "null" {
			nonNull = append(nonNull, t)
		}
	}
	if len(nonNull) == 1 {
		return nonNull[0]
	}
	if len(nonNull) > 1 {
		// Multiple incompatible types — no single Go type can represent this.
		return ""
	}
	// Only "null" type(s) or empty.
	if len(s.Type) > 0 {
		return s.Type[0]
	}
	return ""
}

// inferTypeFromConstraints infers a JSON Schema type from the validation
// keywords present in a schema that has no explicit "type" field. This enables
// generating typed code (and Validate() methods) for constraint-only schemas
// like {"minimum": 5} or {"minLength": 2, "pattern": "^a"}.
//
// Returns "" if the type cannot be inferred.
func (g *Generator) inferTypeFromConstraints(s *schema.Schema) string {
	if !g.validationKeywordsEnabled() {
		if s.Items != nil || (len(s.PrefixItems) > 0 && g.supportsPrefixItems(s)) || s.AdditionalItems != nil ||
			s.Contains != nil || s.UnevaluatedItems != nil {
			return "array"
		}
		if s.AdditionalProperties != nil || s.UnevaluatedProperties != nil {
			return "object"
		}
		return ""
	}

	// Numeric constraints → number
	if s.Minimum != nil || s.Maximum != nil || s.MultipleOf != nil ||
		s.ExclusiveMinimum != nil || s.ExclusiveMaximum != nil {
		return "number"
	}
	// String constraints → string
	if s.MinLength != nil || s.MaxLength != nil || s.Pattern != nil {
		return "string"
	}
	// Array constraints → array
	if s.MinItems != nil || s.MaxItems != nil || s.UniqueItems != nil {
		return "array"
	}
	// unevaluatedItems:false with tuple items and NO sibling applicators/items that
	// could extend or evaluate additional items → safe to infer array with implicit
	// maxItems = tuple length.
	if g.unevaluatedItemsImpliesFixedTuple(s) {
		return "array"
	}
	// Structural array keywords → array
	// items, prefixItems (2020-12 only), additionalItems, contains, and unevaluatedItems
	// only apply to arrays, so their presence implies type "array".
	if s.Items != nil || (len(s.PrefixItems) > 0 && g.supportsPrefixItems(s)) || s.AdditionalItems != nil ||
		s.Contains != nil || s.UnevaluatedItems != nil {
		return "array"
	}
	// Object constraints → object
	if g.validationKeywordsEnabled() && (s.MinProperties != nil || s.MaxProperties != nil) {
		return "object"
	}
	// Structural object keywords → object
	// required, additionalProperties, dependentRequired, dependentSchemas,
	// propertyNames, and unevaluatedProperties only apply to objects.
	//
	// patternProperties is deliberately not one of them, though it is just as
	// object-only. resolveType's own no-declared-type arm already materializes
	// the struct for it (see namesObjectKeys), so inferring the type here would
	// be a second route to the same answer -- and neither could then be shown to
	// be doing the work, while this one is read by four other callers that have
	// nothing to do with the defect.
	if s.AdditionalProperties != nil || s.UnevaluatedProperties != nil {
		return "object"
	}
	if g.validationKeywordsEnabled() && (len(s.Required) > 0 ||
		len(s.DependentRequired) > 0 || len(s.DependentSchemas) > 0 ||
		s.PropertyNames != nil) {
		return "object"
	}
	return ""
}

// unevaluatedItemsImpliesFixedTuple returns true when a schema has
// unevaluatedItems:false alongside a tuple definition (prefixItems or tuple-form
// items) and NO other applicators or keywords that could evaluate additional items.
// In this narrow case, the schema is equivalent to a fixed-length tuple with
// maxItems = tuple length.
func (g *Generator) unevaluatedItemsImpliesFixedTuple(s *schema.Schema) bool {
	if !g.schemaForbidsEveryValue(s.UnevaluatedItems) {
		return false
	}
	tupleLen := 0
	if g.supportsPrefixItems(s) {
		tupleLen = len(s.PrefixItems)
	}
	if tupleLen == 0 && s.Items != nil {
		tupleLen = len(s.Items.Schemas)
	}
	if tupleLen == 0 {
		return false
	}
	// Bail if any applicator or keyword could extend or evaluate additional items.
	if len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 {
		return false
	}
	if s.If != nil || s.Ref != "" || s.Contains != nil {
		return false
	}
	// A dynamic reference is the one applicator whose target is not known until
	// the instance is validated, so what it contributes to the evaluated set
	// cannot be read off the document at all. The suite's "unevaluatedItems with
	// $dynamicRef" case is exactly this: a base carrying prefixItems of length 1
	// and unevaluatedItems:false, whose $dynamicRef resolves in the deriving
	// document to a second prefixItems entry. Folding the tuple length into a
	// maxItems there rejects ["foo","bar"], which is valid. Under-enforcing is
	// the safe direction, so bail and leave the pair to the evaluator that can
	// see the resolution.
	if s.DynamicRef != "" || s.RecursiveRef != "" {
		return false
	}
	// items as a schema (not tuple form) evaluates all remaining items — no unevaluated ones.
	if s.Items != nil && s.Items.Schema != nil {
		return false
	}
	// additionalItems evaluates items beyond the tuple — no unevaluated ones.
	if s.AdditionalItems != nil {
		return false
	}
	return true
}

func unevaluatedItemsImpliesFixedTuple(s *schema.Schema) bool {
	return (&Generator{}).unevaluatedItemsImpliesFixedTuple(s)
}

// buildUnevaluatedItemsDef builds an UnevaluatedItemsDef from a schema's unevaluatedItems keyword.
// Returns nil if the schema has no unevaluatedItems or if all items are statically evaluated.
func (g *Generator) buildUnevaluatedItemsDef(s *schema.Schema) *UnevaluatedItemsDef {
	if s.UnevaluatedItems == nil {
		return nil
	}

	ui := s.UnevaluatedItems

	// unevaluatedItems: true — allow anything, no validation needed
	if ui.IsTrueSchema() {
		return nil
	}
	// A sub-schema admitting nothing — reject any unevaluated item.
	// `{"enum":[]}` says that as much as `false` does, and fell to the
	// schema-valued path below, which extracts no check from it and returns nil.
	if g.schemaForbidsEveryValue(ui) {
		def := &UnevaluatedItemsDef{IsForbidden: true}
		g.collectEvaluatedItems(s, def)
		// If all items are already evaluated, unevaluatedItems:false is a no-op
		if def.AllEvaluated {
			return nil
		}
		return def
	}

	// Schema-valued unevaluatedItems — validate each unevaluated item
	def := &UnevaluatedItemsDef{}

	// Extract type constraint
	if len(ui.Type) == 1 {
		def.ValueType = ui.Type[0]
	}

	// Extract simple validation checks
	def.Checks = extractUnevalItemChecks(ui)

	if def.ValueType == "" && len(def.Checks) == 0 {
		// Schema-valued but no extractable constraints (complex sub-schema)
		// Still need to reject non-matching items — treat as type check if possible
		// For now, skip (we handle the common cases)
		return nil
	}

	g.collectEvaluatedItems(s, def)
	if def.AllEvaluated {
		return nil
	}
	return def
}

// extractUnevalItemChecks extracts simple validation checks from a unevaluatedItems sub-schema.
func extractUnevalItemChecks(ui *schema.Schema) []ContainsCheck {
	var checks []ContainsCheck
	if ui.Minimum != nil {
		checks = append(checks, ContainsCheck{CheckType: "minimum", Value: *ui.Minimum})
	}
	if ui.Maximum != nil {
		checks = append(checks, ContainsCheck{CheckType: "maximum", Value: *ui.Maximum})
	}
	if ui.MultipleOf != nil {
		checks = append(checks, ContainsCheck{CheckType: "multipleOf", Value: *ui.MultipleOf})
	}
	if ui.ExclusiveMinimum != nil && ui.ExclusiveMinimum.Number != nil {
		checks = append(checks, ContainsCheck{CheckType: "exclusiveMinimum", Value: *ui.ExclusiveMinimum.Number})
	}
	if ui.ExclusiveMaximum != nil && ui.ExclusiveMaximum.Number != nil {
		checks = append(checks, ContainsCheck{CheckType: "exclusiveMaximum", Value: *ui.ExclusiveMaximum.Number})
	}
	return checks
}

// collectEvaluatedItems populates an UnevaluatedItemsDef with information about
// which array positions are "evaluated" by other keywords in the schema.
func (g *Generator) collectEvaluatedItems(s *schema.Schema, def *UnevaluatedItemsDef) {
	// 1. items as a single schema (uniform items) evaluates ALL positions
	if s.Items != nil && s.Items.Schema != nil {
		def.AllEvaluated = true
		return
	}

	// 2. prefixItems / items-as-array evaluates fixed positions
	tupleLen := len(s.PrefixItems)
	if tupleLen == 0 && s.Items != nil {
		tupleLen = len(s.Items.Schemas)
	}
	def.EvaluatedCount = tupleLen

	// 3. additionalItems evaluates positions beyond the tuple only when tuple
	// items exist. Without tuple items, additionalItems is ignored by the spec.
	if tupleLen > 0 && s.AdditionalItems != nil && !(s.AdditionalItems.Bool != nil && !*s.AdditionalItems.Bool) {
		// additionalItems is present and is NOT false — it evaluates all remaining items
		def.AllEvaluated = true
		return
	}

	// 4. contains in Draft 2020-12 evaluates matching items at runtime.
	// Since we cannot determine which items will match contains at compile time,
	// we set ContainsEvaluates so the template generates per-item runtime checks
	// that integrate contains matching with unevaluatedItems validation.
	if s.Contains != nil {
		def.ContainsEvaluates = true
	}

	// 5. if (without then/else) evaluates items via its prefixItems/items annotations
	// only when the if condition holds at runtime. Since we cannot evaluate the if
	// condition at compile time, we cannot statically determine which items are
	// evaluated. The if/then/else block below will add a conditional eval entry,
	// but without then/else, both branches have 0 evaluated — which is correct
	// for the case where if doesn't match. When if DOES match, we'd need runtime
	// evaluation (added as known limitation / known-failure).

	// 6. Walk applicators: allOf, $ref, anyOf, oneOf, if/then/else
	if s.Ref != "" || s.RecursiveRef != "" {
		if refSchema := g.resolveEffectiveRefSchema(s); refSchema != nil {
			evalCount, allEval := g.countEvaluatedItemsInSchema(refSchema)
			if allEval {
				def.AllEvaluated = true
				return
			}
			if evalCount > def.EvaluatedCount {
				def.EvaluatedCount = evalCount
			}
		}
	}
	if s.DynamicRef != "" {
		if resolved := g.resolveDynamicRef(s.DynamicRef, s); resolved != nil {
			evalCount, allEval := g.countEvaluatedItemsInSchema(resolved)
			if allEval {
				def.AllEvaluated = true
				return
			}
			if evalCount > def.EvaluatedCount {
				def.EvaluatedCount = evalCount
			}
		}
	}

	for _, sub := range s.AllOf {
		resolved := sub
		if sub.Ref != "" || sub.RecursiveRef != "" {
			if r := g.resolveEffectiveRefSchema(sub); r != nil {
				resolved = r
			}
		}
		if sub.DynamicRef != "" {
			if r := g.resolveDynamicRef(sub.DynamicRef, sub); r != nil {
				resolved = r
			}
		}
		evalCount, allEval := g.countEvaluatedItemsInSchema(resolved)
		if allEval {
			def.AllEvaluated = true
			return
		}
		if evalCount > def.EvaluatedCount {
			def.EvaluatedCount = evalCount
		}
	}

	// anyOf/oneOf: runtime-conditional — the maximum of evaluated counts across branches
	if len(s.AnyOf) > 0 {
		ce := UnevalItemsConditionalEval{Kind: "anyOf"}
		for _, sub := range s.AnyOf {
			resolved := sub
			if sub.Ref != "" || sub.RecursiveRef != "" {
				if r := g.resolveEffectiveRefSchema(sub); r != nil {
					resolved = r
				}
			}
			if sub.DynamicRef != "" {
				if r := g.resolveDynamicRef(sub.DynamicRef, sub); r != nil {
					resolved = r
				}
			}
			evalCount, allEval := g.countEvaluatedItemsInSchema(resolved)
			ce.Branches = append(ce.Branches, UnevalItemsBranch{
				EvaluatedCount: evalCount,
				AllEvaluated:   allEval,
			})
		}
		def.ConditionalEvals = append(def.ConditionalEvals, ce)
	}

	if len(s.OneOf) > 0 {
		ce := UnevalItemsConditionalEval{Kind: "oneOf"}
		for _, sub := range s.OneOf {
			resolved := sub
			if sub.Ref != "" || sub.RecursiveRef != "" {
				if r := g.resolveEffectiveRefSchema(sub); r != nil {
					resolved = r
				}
			}
			if sub.DynamicRef != "" {
				if r := g.resolveDynamicRef(sub.DynamicRef, sub); r != nil {
					resolved = r
				}
			}
			evalCount, allEval := g.countEvaluatedItemsInSchema(resolved)
			ce.Branches = append(ce.Branches, UnevalItemsBranch{
				EvaluatedCount: evalCount,
				AllEvaluated:   allEval,
			})
		}
		def.ConditionalEvals = append(def.ConditionalEvals, ce)
	}

	// if/then/else
	if s.If != nil {
		ce := UnevalItemsConditionalEval{Kind: "ifThenElse"}
		// Count items evaluated by the if-schema itself (its own annotations).
		ifEvalCount, ifAllEval := g.countEvaluatedItemsInSchema(s.If)
		ce.IfEvalCount = ifEvalCount
		ce.IfAllEval = ifAllEval
		// Extract runtime if-condition checks from the if-schema's prefixItems const values.
		ce.IfItemChecks = g.extractIfItemConstChecks(s.If)
		if s.Then != nil {
			resolved := s.Then
			if s.Then.Ref != "" || s.Then.RecursiveRef != "" {
				if r := g.resolveEffectiveRefSchema(s.Then); r != nil {
					resolved = r
				}
			}
			if s.Then.DynamicRef != "" {
				if r := g.resolveDynamicRef(s.Then.DynamicRef, s.Then); r != nil {
					resolved = r
				}
			}
			evalCount, allEval := g.countEvaluatedItemsInSchema(resolved)
			ce.ThenEvalCount = evalCount
			ce.ThenAllEval = allEval
		}
		if s.Else != nil {
			resolved := s.Else
			if s.Else.Ref != "" || s.Else.RecursiveRef != "" {
				if r := g.resolveEffectiveRefSchema(s.Else); r != nil {
					resolved = r
				}
			}
			if s.Else.DynamicRef != "" {
				if r := g.resolveDynamicRef(s.Else.DynamicRef, s.Else); r != nil {
					resolved = r
				}
			}
			evalCount, allEval := g.countEvaluatedItemsInSchema(resolved)
			ce.ElseEvalCount = evalCount
			ce.ElseAllEval = allEval
		}
		def.ConditionalEvals = append(def.ConditionalEvals, ce)
	}
}

// extractIfItemConstChecks extracts const checks from an if-schema's prefixItems
// for runtime evaluation of the if-condition in unevaluatedItems validation.
// Returns checks for each prefixItems position that has a const constraint.
func (g *Generator) extractIfItemConstChecks(ifSchema *schema.Schema) []IfItemConstCheck {
	if ifSchema == nil {
		return nil
	}
	var checks []IfItemConstCheck
	// Check prefixItems (Draft 2020-12)
	for i, itemSchema := range ifSchema.PrefixItems {
		if itemSchema == nil || itemSchema.IsTrueSchema() {
			continue // boolean true — no constraint at this position
		}
		if itemSchema.Const != nil {
			b, err := json.Marshal(*itemSchema.Const)
			if err == nil {
				checks = append(checks, IfItemConstCheck{Index: i, JSONValue: string(b)})
			}
		} else if itemSchema.ConstIsNull {
			checks = append(checks, IfItemConstCheck{Index: i, JSONValue: "null"})
		}
	}
	// Check items.Schemas (Draft 7 / 2019-09 tuple form)
	if ifSchema.Items != nil {
		for i, itemSchema := range ifSchema.Items.Schemas {
			if itemSchema == nil || itemSchema.IsTrueSchema() {
				continue
			}
			if itemSchema.Const != nil {
				b, err := json.Marshal(*itemSchema.Const)
				if err == nil {
					checks = append(checks, IfItemConstCheck{Index: i, JSONValue: string(b)})
				}
			} else if itemSchema.ConstIsNull {
				checks = append(checks, IfItemConstCheck{Index: i, JSONValue: "null"})
			}
		}
	}
	return checks
}

// countEvaluatedItemsInSchema returns how many array positions are evaluated by
// a sub-schema, and whether it evaluates all positions.
func (g *Generator) countEvaluatedItemsInSchema(s *schema.Schema) (int, bool) {
	return g.countEvaluatedItemsOnPath(s, nil)
}

// countEvaluatedItemsOnPath is countEvaluatedItemsInSchema carrying the set of
// schemas the count is already inside.
//
// The walk follows $ref, $dynamicRef and allOf, so a schema that references
// itself -- {"$ref": "#", "unevaluatedItems": false} is the whole of it --
// re-enters this function forever, which is a stack overflow. A schema already
// on the path is contributing its positions in the frame above, so the repeat
// answers "nothing more, and not everything": zero is neutral to the max the
// callers take, and declining to claim full evaluation leaves the
// unevaluatedItems check in place rather than silently dropping it.
//
// The mark comes off on the way out, so sibling allOf branches that name the
// same schema each count it, exactly as before. The set is allocated once per
// top-level count, on the first node that has somewhere to recurse to.
func (g *Generator) countEvaluatedItemsOnPath(s *schema.Schema, onPath map[*schema.Schema]bool) (int, bool) {
	if s == nil || onPath[s] {
		return 0, false
	}

	// Boolean true schema → evaluates nothing (no annotations produced).
	// But note: a sub-schema with unevaluatedItems:true evaluates all items.
	if s.IsTrueSchema() {
		return 0, false
	}

	// items as uniform schema → all positions evaluated
	if s.Items != nil && s.Items.Schema != nil {
		return 0, true
	}

	// unevaluatedItems: true in a sub-schema → all items are evaluated by that sub-schema
	if s.UnevaluatedItems != nil && s.UnevaluatedItems.IsTrueSchema() {
		return 0, true
	}

	// prefixItems / items-as-array
	tupleLen := len(s.PrefixItems)
	if tupleLen == 0 && s.Items != nil {
		tupleLen = len(s.Items.Schemas)
	}
	if tupleLen > 0 && s.AdditionalItems != nil && !(s.AdditionalItems.Bool != nil && !*s.AdditionalItems.Bool) {
		return 0, true
	}

	// Recurse into allOf/$ref
	maxEval := tupleLen

	if onPath == nil {
		onPath = make(map[*schema.Schema]bool)
	}
	onPath[s] = true
	defer delete(onPath, s)

	if s.Ref != "" || s.RecursiveRef != "" {
		if refSchema := g.resolveEffectiveRefSchema(s); refSchema != nil {
			evalCount, allEval := g.countEvaluatedItemsOnPath(refSchema, onPath)
			if allEval {
				return 0, true
			}
			if evalCount > maxEval {
				maxEval = evalCount
			}
		}
	}
	if s.DynamicRef != "" {
		if resolved := g.resolveDynamicRef(s.DynamicRef, s); resolved != nil {
			evalCount, allEval := g.countEvaluatedItemsOnPath(resolved, onPath)
			if allEval {
				return 0, true
			}
			if evalCount > maxEval {
				maxEval = evalCount
			}
		}
	}

	for _, sub := range s.AllOf {
		resolved := sub
		if sub.Ref != "" || sub.RecursiveRef != "" {
			if r := g.resolveEffectiveRefSchema(sub); r != nil {
				resolved = r
			}
		}
		if sub.DynamicRef != "" {
			if r := g.resolveDynamicRef(sub.DynamicRef, sub); r != nil {
				resolved = r
			}
		}
		evalCount, allEval := g.countEvaluatedItemsOnPath(resolved, onPath)
		if allEval {
			return 0, true
		}
		if evalCount > maxEval {
			maxEval = evalCount
		}
	}

	return maxEval, false
}

// schemaHasExplicitType returns true if the schema declares an explicit "type"
// field that includes the given type name. When the type list is empty (no
// explicit type), the schema is permissive — it accepts any JSON value type.
func schemaHasExplicitType(s *schema.Schema, typeName string) bool {
	for _, t := range s.Type {
		if t == typeName {
			return true
		}
	}
	return false
}

// isNullable returns true if the schema's type list includes "null".
func isNullable(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	for _, t := range s.Type {
		if t == "null" {
			return true
		}
	}
	return false
}

// nonNullType returns the first type in the type list that is not "null".
func nonNullType(s *schema.Schema) string {
	if s == nil {
		return ""
	}
	for _, t := range s.Type {
		if t != "null" {
			return t
		}
	}
	return ""
}

// isNullableComposition checks if a property schema uses anyOf/oneOf with a null
// variant, indicating the resolved Go type will be a pointer. This is used to
// determine whether omitempty should be suppressed for lossless null round-tripping.
// It also follows $ref to check the target schema's composition.
func (g *Generator) isNullableComposition(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	// Direct anyOf/oneOf with null variant.
	for _, variants := range [][]*schema.Schema{s.AnyOf, s.OneOf} {
		_, hasNull := g.separateNullFromOneOf(variants)
		if hasNull {
			return true
		}
	}
	// Follow $ref to check the target.
	if effRef := s.EffectiveRef(); effRef != "" {
		if resolved := g.resolveRefInContext(effRef, s); resolved != nil {
			for _, variants := range [][]*schema.Schema{resolved.AnyOf, resolved.OneOf} {
				_, hasNull := g.separateNullFromOneOf(variants)
				if hasNull {
					return true
				}
			}
		}
	}
	return false
}

// refToGoName extracts the Go type name from a $ref string.
// It handles JSON Pointer refs, URN refs, and URI refs:
//
//	"#/$defs/my-type"       → "MyType"
//	"#/definitions/Address" → "Address"
//	"#"                     → "Root"
//	"urn:uuid:dead-beef"    → "DeadBeef" (uses last segment after last colon)
//	"#/definitions/tilde~0field" → "TildeField" (JSON Pointer unescaping)
//	"foo%22bar"             → "FooBar" (URL decoding)
func refToGoName(ref string) string {
	// Strip fragment from URIs/URNs: "urn:...#something" → use "something"
	name := ref
	if idx := strings.LastIndex(ref, "#"); idx >= 0 {
		fragment := ref[idx+1:]
		if fragment == "" {
			// Fragment-only ref "#" — use "Root" as the name.
			return "Root"
		}
		name = fragment
	}

	// For JSON Pointer paths like "/definitions/foo/bar", take the last segment.
	if strings.Contains(name, "/") {
		parts := strings.Split(name, "/")
		// Find the last non-empty segment.
		for i := len(parts) - 1; i >= 0; i-- {
			if parts[i] != "" {
				name = parts[i]
				break
			}
		}
		// If all segments are empty, use a fallback.
		if name == "" || name == ref {
			return "X"
		}
	}

	// For URN refs without fragment (e.g. "urn:uuid:deadbeef-1234"),
	// take the last colon-separated segment.
	if strings.Contains(name, ":") {
		parts := strings.Split(name, ":")
		name = parts[len(parts)-1]
	}

	// Apply JSON Pointer unescaping: ~1 → /, ~0 → ~
	name = strings.ReplaceAll(name, "~1", "/")
	name = strings.ReplaceAll(name, "~0", "~")

	// Apply URL percent-decoding.
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}

	return SchemaNameToGoName(name)
}

// enumConstNames derives the Go constant name for every value of an enum and
// guarantees the names are distinct, in input order.
//
// Distinctness needs two mechanisms, and the second is not optional. Sanitizing
// is lossy, so several values routinely reduce to the same identifier ("!" and
// "!!" both become "X"); a group that shares a raw name is numbered 1..n. That
// numbering alone is not enough, because a *different* value can sanitize
// straight onto a name the numbering just handed out: "1" becomes "X1" on its
// own (a leading digit is not a legal identifier start, so sanitizeGoIdentifier
// prefixes "X"), which is exactly what the first member of an "X" pair becomes.
// {"enum":["!","!!","1"]} used to emit RootX1, RootX2, RootX1 -- gofmt-clean Go
// that does not compile ("RootX1 redeclared in this block", plus a duplicate
// case in the generated switch) behind a zero exit code. The claimed set below
// is what closes that hole: whatever a name is derived from, it is only used if
// nothing has taken it yet.
func enumConstNames(typeName string, values []any) []string {
	raw := make([]string, len(values))
	count := make(map[string]int, len(values))
	for i, v := range values {
		raw[i] = typeName + enumValueSuffix(v)
		count[raw[i]]++
	}

	names := make([]string, len(values))
	seen := make(map[string]int, len(values))
	claimed := make(map[string]bool, len(values))
	for i, base := range raw {
		name := base
		if count[base] > 1 {
			seen[base]++
			name = fmt.Sprintf("%s%d", base, seen[base])
		}
		// The underscore keeps the fallback out of the numbering scheme's own
		// namespace, so bumping cannot land on a name a later group is owed.
		for n := 2; claimed[name]; n++ {
			name = fmt.Sprintf("%s_%d", base, n)
		}
		claimed[name] = true
		names[i] = name
	}
	return names
}

// enumValueSuffix returns a suffix for an enum constant name from the value.
func enumValueSuffix(v any) string {
	switch val := v.(type) {
	case string:
		return SchemaNameToGoName(val)
	case float64:
		// Sanitize numeric values for Go identifiers:
		// replace '-' with "Neg", '.' with "_", '+' with "", 'e' with "e"
		s := fmt.Sprintf("%v", val)
		s = strings.ReplaceAll(s, "-", "Neg")
		s = strings.ReplaceAll(s, ".", "_")
		s = strings.ReplaceAll(s, "+", "")
		return s
	case bool:
		if val {
			return "True"
		}
		return "False"
	case nil:
		return "Null"
	default:
		// For objects/arrays, serialize to JSON and sanitize for Go identifier.
		raw, err := json.Marshal(val)
		if err != nil {
			return "Value"
		}
		return SchemaNameToGoName(string(raw))
	}
}

// populateValidatableFields is a post-pass that identifies struct fields whose
// types have Validate() methods and adds them to StructDef.ValidatableFields.
// Must run after resolveAliasMethodability so NoMethods flags are set.
// localTypeIsValidatable reports whether a generated type def carries a
// Validate() method.
func localTypeIsValidatable(td TypeDef) bool {
	switch d := td.(type) {
	case *EnumDef, *StructDef, *InferredAliasDef, *BigIntAliasDef, *TypeOnlySchemaDef,
		*NotSchemaDef, *DynamicSchemaDef, *AnnotationSchemaDef:
		// An AnnotationSchemaDef carries a Validate like the rest, and it has to
		// be named here for a *property* of that shape to be checked at all: the
		// struct only dispatches to a field type it believes validatable, so
		// leaving it out gave a field whose whole content was an
		// unevaluatedItems the parent then never asked about.
		return true
	case *AliasDef:
		return d.CanHaveMethods()
	default:
		return false
	}
}

func (g *Generator) populateValidatableFields() {
	// Build set of type names that have Validate() methods.
	validatableTypes := make(map[string]bool)
	for _, td := range g.output.TypeDefs {
		if localTypeIsValidatable(td) {
			validatableTypes[td.TypeName()] = true
		}
	}

	// For each struct, check its fields.
	for _, td := range g.output.TypeDefs {
		sd, ok := td.(*StructDef)
		if !ok {
			continue
		}
		nullKeys := make(map[string]bool, len(sd.NullPresenceKeys))
		for _, k := range sd.NullPresenceKeys {
			nullKeys[k] = true
		}
		for _, f := range sd.Fields {
			// Direct named type (or pointer to named type).
			typeName := namedTypeName(f.Type)
			if typeName != "" && (validatableTypes[typeName] || crossPackageValidatable(f.Type)) {
				zeroLit := g.zeroLiteralForType(f.Type)
				if foreignZero, ok := crossPackageZeroLiteral(f.Type); ok {
					zeroLit = foreignZero
				}
				sd.ValidatableFields = append(sd.ValidatableFields, ValidatableFieldDef{
					FieldName:   f.Name,
					JSONName:    f.JSONName,
					GoType:      f.Type,
					IsPointer:   f.Type.IsPointer(),
					OmitEmpty:   f.OmitEmpty,
					ZeroLiteral: zeroLit,
					// A pointer already says "absent" with nil. Anything else
					// optional needs _jsonKeys to say it, or the Go zero of a
					// property the document never carried is measured against
					// the schema — which is how OmitEmpty false, where no
					// optional field is a pointer, rejected conforming
					// documents.
					PresenceGuard: !f.Required && !f.Type.IsPointer(),
					// A present null leaves the same zero an absent property
					// does, and the schema permits it, so the type's Validate
					// must not be handed that zero. See NullGuard.
					NullGuard: nullKeys[f.JSONName] && !f.Type.IsPointer(),
				})
				continue
			}
			// Slice of named type (or pointer to slice of named type).
			elemName := sliceElementTypeName(f.Type)
			if elemName != "" && (validatableTypes[elemName] || crossPackageValidatable(f.Type)) {
				sd.ValidatableFields = append(sd.ValidatableFields, ValidatableFieldDef{
					FieldName:       f.Name,
					JSONName:        f.JSONName,
					GoType:          f.Type,
					IsPointer:       f.Type.IsPointer(),
					IsSlice:         true,
					ElemIsPointer:   sliceElementIsPointer(f.Type),
					ElemRejectsNull: g.typeRejectsNull(elemName),
					OmitEmpty:       f.OmitEmpty,
				})
				continue
			}
			// Map of named type: an object whose shape is additionalProperties,
			// so every value carries the same schema and validates against it.
			valueName := mapValueTypeName(f.Type)
			if valueName != "" && (validatableTypes[valueName] || crossPackageValidatable(f.Type)) {
				sd.ValidatableFields = append(sd.ValidatableFields, ValidatableFieldDef{
					FieldName:       f.Name,
					JSONName:        f.JSONName,
					GoType:          f.Type,
					IsPointer:       f.Type.IsPointer(),
					IsMap:           true,
					ElemIsPointer:   mapValueIsPointer(f.Type),
					ElemRejectsNull: g.typeRejectsNull(valueName),
					OmitEmpty:       f.OmitEmpty,
				})
			}
		}

		// A oneOf union field is not in sd.Fields as its variant type — it is
		// the sealed interface — so the loop above cannot reach it. Mark the
		// variants whose own type answers for the branch's constraints, and the
		// parent's Validate dispatches on the wrapper it actually holds.
		for oi := range sd.OneOfs {
			for vi := range sd.OneOfs[oi].Variants {
				v := &sd.OneOfs[oi].Variants[vi]
				typeName := namedTypeName(v.Type)
				if typeName == "" {
					continue
				}
				if validatableTypes[typeName] || crossPackageValidatable(v.Type) {
					v.Validatable = true
				}
			}
		}
	}
}

// maxItemLevels bounds how many array dimensions a single ItemValidationDef
// descends. Nothing in a well-formed schema comes near it; the bound only stops
// a pathological input from generating an unbounded nest of loops.
const maxItemLevels = 8

// singleItemsSchema returns the sub-schema that governs every element of an
// array schema, or nil when there is none to speak of. A tuple form, `items:
// true`, and a 2020-12 prefixItems (where `items` governs only the tail) all
// answer nil: those positions are validated elsewhere, and guessing here would
// reject data the schema allows.
//
// `items: false` was answered nil beside `items: true`, and it is not the same
// case. `true` says nothing about an element and there is nothing for the
// descent to carry; `false` says no element is admissible, and since issue #142
// the position holds the type that says so -- so stopping here left that type's
// Validate dispatched by nobody. It showed one level down, where the descent is
// the only thing that reaches an element:
// {"type":"array","items":{"type":"array","items":false}} typed the inner
// element correctly and then accepted [[1]].
func (g *Generator) singleItemsSchema(s *schema.Schema) *schema.Schema {
	if s == nil || s.AdditionalItems != nil {
		return nil
	}
	if len(s.PrefixItems) > 0 && g.supportsPrefixItems(s) {
		return nil
	}
	if s.Items == nil || s.Items.Schema == nil || s.Items.Schema.IsTrueSchema() {
		return nil
	}
	return s.Items.Schema
}

// mapValueSchema returns the sub-schema that governs every value of an object
// whose whole shape is additionalProperties -- no declared property names, no
// patternProperties, one schema for everything the object holds. It answers nil
// for any other object, including one whose additionalProperties is a boolean:
// there the keyword is a verdict on unknown keys, not a description of them.
//
// This is the map counterpart of singleItemsSchema, and it is the same test
// resolveType makes before typing such a node as a Go map. The two callers must
// agree: the descent below hangs the value schema's keywords off the map the
// other one produced, and a predicate that admitted more here would attach them
// to a Go type that did not come from this schema.
//
// primaryType is passed in rather than recomputed because resolveType may have
// inferred it from constraint keywords where the schema states no "type".
func mapValueSchema(s *schema.Schema, primaryType string) *schema.Schema {
	if s == nil || primaryType != "object" || hasProperties(s) || len(s.PatternProperties) > 0 {
		return nil
	}
	if s.AdditionalProperties == nil || s.AdditionalProperties.Schema == nil ||
		s.AdditionalProperties.Schema.IsBooleanSchema() {
		return nil
	}
	return s.AdditionalProperties.Schema
}

// boxedInferredType names an inline position whose sub-schema would otherwise
// be typed from a keyword rather than from a "type" it states. The second
// result is false when the position keeps the type it has today, which every
// caller answers by resolving as before.
//
// A type read off a keyword asserts what the sub-schema did not say. `minimum`
// constrains numbers and is vacuous for every other JSON kind, so every one of
//
//	{"type":"object","additionalProperties":{"minimum":5}}   admits {"x":"abc"}, {"x":{"a":1}}, {"x":null}
//	{"type":"object","properties":{"p":{"minimum":5}}}       admits {"p":"abc"}, {"p":{"a":1}}
//	{"type":"array","items":{"minimum":5}}                   admits ["abc"], [null]
//
// and map[string]float64, *float64 and []float64 refuse them: a string or an
// object dies in the decoder, and a null decodes to the Go zero, which is then
// measured against the bound. That is issue #137 for the overflow map and
// issue #139 for the other two, and it is the same narrowing for `minLength`
// (string refuses a number), `minItems` ([]any refuses a string) and `required`
// (map[string]any refuses a scalar).
//
// The type the position should have already exists, and is what the identical
// sub-schema gets the moment it is written as a $defs entry and referenced:
// generateTypeDef's inferred arms build a wrapper that keeps the raw JSON when
// the value is of another kind and applies the keywords only when it is not --
// InferredAliasDef for a number, a string or an array, and for an inferred
// object the propertyless struct, whose AcceptNonObject arm does the same job.
// So {"properties":{"p":{"$ref":"#/$defs/M"}}} with M of {"minimum":5} has
// always accepted all of the documents above while the inline spelling refused
// them. Naming the position is what routes the inline one to the same type, and
// the two stop disagreeing about a schema neither of them changed.
//
// Only an *inferred* type is claimed. A sub-schema that states its type has
// authorized the narrowing -- {"type":"number","minimum":5} really does forbid
// a string, and a decode failure there is a correct rejection -- so it keeps
// float64 and the convenient Go type that goes with it. The keywords that fix a
// type through an arm of resolveType above the inference are excluded for the
// same reason: an enum, a const and a $ref each already have a path that
// answers better than this one.
//
// A tuple slot does not come through here and does not need to: tupleItemDefFor
// already names every position it cannot type, and the raw wrapper it builds
// keeps the value whatever kind it arrives as.
func (g *Generator) boxedInferredType(sub *schema.Schema, name string) (GoType, bool) {
	if !g.boxedInferredTypeNeedsName(sub, name) {
		return nil, false
	}
	return g.namedInlineType(sub, name)
}

// boxedInferredTypeNeedsName is boxedInferredType's question, split out so the
// two halves of the answer -- whether to name the position, and what the name
// resolves to -- can be read apart.
func (g *Generator) boxedInferredTypeNeedsName(sub *schema.Schema, name string) bool {
	if !g.validationKeywordsEnabled() || !g.inlineNameAvailable(sub, name) {
		return false
	}
	return g.typeIsInferredFromConstraints(sub)
}

// forbiddingInlineType names an inline position whose sub-schema admits no
// instance at all, so that the position gets the type generateTypeDef builds for
// exactly that schema. The second result is false when the position keeps the
// type it has today, which every caller answers by resolving as before.
//
// An element and a map value were the two positions with no such path.
//
//	{"type":"object","properties":{"a":{"type":"array","items":false}}}
//	{"type":"object","properties":{"a":{"type":"object","additionalProperties":{"enum":[]}}}}
//
// `items: false` forbids every element, so any non-empty array is invalid, and
// an empty enum forbids every value, so an object with any key at all is. Both
// came out `[]any` and `map[string]any` with no check and accepted {"a":[1]} and
// {"a":{"x":1}}. That is issue #142, and it is the last position of the family
// #113, #114, #116 and #126 walked: a schema that constrains without naming a Go
// type is enforced at a document root and behind a $ref, and was dropped inline.
//
// The type the position should have already exists and needed nothing.
// generateTypeDef answers a schema that admits nothing with a raw-JSON wrapper
// whose Validate refuses every value present -- the arm #116 gave the boolean
// `false` schema in every position, and the one #121 gave `{"enum":[]}` beside
// it. So the identical sub-schema written as a $defs entry and referenced has
// always rejected these documents while the inline spelling accepted them;
// {"items":{"$ref":"#/$defs/F"}} with F of `false` is the control, and it was
// already right. Naming the position is what routes the inline one to the same
// type.
//
// This is a different question from boxedInferredType's, and the two are not
// one predicate wearing two names. That one asks whether a type would be
// *inferred* from a keyword the schema did not state, and its answer is a box
// that keeps the value whatever kind it arrives as and applies the keywords only
// where they speak; it declines a schema that states a "type", because such a
// schema authorized the narrowing. Here the schema's own answer is that no value
// is admissible, which no Go type expresses, so a stated "type" changes nothing
// -- {"type":"string","enum":[]} admits no string either -- and the wrapper is
// not a box for a value but a refusal of every one.
func (g *Generator) forbiddingInlineType(sub *schema.Schema, name string) (GoType, bool) {
	if !g.schemaForbidsEveryValue(sub) || !g.inlineNameAvailable(sub, name) {
		return nil, false
	}
	return g.namedInlineType(sub, name)
}

// inlineNameAvailable reports whether an inline position may be materialized
// under name.
//
// The guards are constraintOnlyNamedType's, with one relaxation: a name already
// generated belongs to another schema and is refused, *unless* this very node
// has a canonical name of its own, in which case materializeNamed hands that
// back and declares nothing. A name being generated is the frame that called
// us, and a node already in flight would have the wrapper stand in for the
// definition being built above it; both are refused outright, and the caller
// then resolves the position exactly as it did before.
func (g *Generator) inlineNameAvailable(sub *schema.Schema, name string) bool {
	if sub == nil || name == "" {
		return false
	}
	if g.generating[name] || g.nodesInProgress[sub] {
		return false
	}
	if _, named := g.nodeTypeNames[sub]; !named && g.generated[name] {
		return false
	}
	return true
}

// namedInlineType materializes sub under name and answers with the reference to
// it, or (nil, false) when generateTypeDef declined to declare anything.
func (g *Generator) namedInlineType(sub *schema.Schema, name string) (GoType, bool) {
	if n, cyclic := g.materializeNamed(sub, name); g.generated[n] {
		return namedOrPointer(n, cyclic), true
	}
	return nil, false
}

// emptyEnumSchema reports whether a schema states `"enum": []`, which admits no
// instance at all: enum asserts that the instance equals one of the values
// listed, and there are none. That is what the boolean `false` schema says, and
// what the official suite's "empty enum" group states -- a string, a number, a
// null, an object, an array and a boolean all marked invalid.
//
// The nil test is what separates an absent enum from an empty one:
// encoding/json leaves the field nil for an absent keyword and allocates an
// empty slice for `[]`, so len() alone would answer yes for every schema in the
// corpus. Read `s.Enum != nil` and never `len(s.Enum) == 0` on its own.
//
// It is also the reason this question cannot be asked of the re-marshaled
// keyword set the representability gates read: `enum` is tagged omitempty, so an
// empty one leaves no key behind and a gate that decides from those keys sees a
// schema stating nothing.
func emptyEnumSchema(s *schema.Schema) bool {
	return s != nil && s.Enum != nil && len(s.Enum) == 0
}

// schemaForbidsEveryValue reports whether a sub-schema admits no instance at
// all. Every arm below is a theorem about the empty set rather than a guess,
// because the answer is what makes a position emit a rejection: an arm that is
// merely usually right would refuse documents the schema permits, which this
// repository treats as worse than a missing check.
//
// The two direct spellings are the ones generateTypeDef answers with the
// forbidding wrapper -- the boolean `false` schema, and `"enum": []`. The empty
// enum is conditioned on the validation vocabulary, exactly as generateTypeDef's
// own arm is: where the declared metaschema omits that vocabulary `enum` asserts
// nothing, and reading it as a refusal there would reject every document the
// schema permits.
//
// The composition arms are issue #146. `{"not":{}}` and `{"oneOf":[false,false]}`
// say what `false` says, and six keywords that answered `false` and the empty
// enum after #142 -- propertyNames, contains, dependentSchemas,
// unevaluatedItems, unevaluatedProperties, and an inferred array's item and tail
// -- read only those two and dropped these. Each of them holds a *forbidding*
// arm and nothing else, so widening what counts as forbidding is what reaches
// them; there is no per-keyword ladder to build because there is no other shape
// those positions can express.
//
//	not      not(T) is the empty set exactly when T is every value. The inner
//	         schema is read by acceptsEveryInstance, which is a whitelist and
//	         must stay one -- a keyword it fails to notice makes the negation
//	         wider than the schema, which is the #142 false rejection again.
//	allOf    a conjunction with one empty conjunct is empty, whatever the others
//	         say. This is allOfContainsFalseSchema's rule, reached from here so
//	         that {"allOf":[{"not":{}},{"type":"string"}]} is refused whole
//	         rather than by the one branch the static merge could read.
//	anyOf    a disjunction is empty when every branch is.
//	oneOf    "exactly one branch matches" holds for no value when no branch
//	         matches any. The mirror rule -- two branches that each match
//	         everything -- is compositionAdmitsNothing's and stays there, since
//	         it needs the accept-all reading rather than the forbidding one.
//
// A branch is judged after its $ref is followed, for the reason #116 recorded:
// a $ref is not the schema it names, so {"oneOf":[{"$ref":"#/$defs/never"}]}
// would otherwise read as an ordinary branch. The schema in hand is judged as
// written, and a top-level $ref is not followed here -- resolvedToFalseSchema is
// the caller that asks that question, and following it here would take
// {"items":{"$ref":"#/$defs/Never"}} away from the arm that resolves the
// reference and materialize the element under an inline name instead.
func (g *Generator) schemaForbidsEveryValue(s *schema.Schema) bool {
	return g.forbidsEveryValueOnPath(s, 0, nil)
}

// forbidsEveryValueOnPath is schemaForbidsEveryValue's recursion. onPath is the
// set of nodes already being judged one frame up, which is what terminates
// {"$defs":{"a":{"allOf":[{"$ref":"#/$defs/a"}]}}}: a node whose own answer is
// still in flight has nothing to add, and false is the answer that keeps the
// document. depth bounds the same thing for a schema that is merely very deep.
func (g *Generator) forbidsEveryValueOnPath(s *schema.Schema, depth int, onPath map[*schema.Schema]bool) bool {
	if s == nil || depth > maxRuntimeDepth || onPath[s] {
		return false
	}
	if s.IsBooleanSchema() {
		return s.IsFalseSchema()
	}
	// Two spellings of "this states members and admits none of them": the list
	// written empty, and a list its own "type" filters empty (#145). Both are
	// behind the validation vocabulary, since a metaschema withholding `enum`
	// and `type` makes neither assert anything.
	//
	// Neither is read beside a $ref on a draft that displaces its siblings, for
	// the reason declaredTypeAdmitsNoEnumMember already declines there: the enum
	// is not written as far as that draft is concerned, so it states no members
	// and cannot make the schema empty. Reading it refuses documents the
	// referenced schema admits, which is issue #151's over-enforcement reached
	// by the one route that is not a type ladder -- a $defs entry spelled
	// {"$ref":"#/definitions/Word","enum":[]} made its property forbidden on
	// draft 7.
	if g.validationKeywordsEnabled() && !g.refDisplacesSiblingValues(s) &&
		(emptyEnumSchema(s) || g.declaredTypeAdmitsNoEnumMember(s)) {
		return true
	}
	if onPath == nil {
		onPath = make(map[*schema.Schema]bool)
	}
	onPath[s] = true
	defer delete(onPath, s)

	// The arms below are applicators rather than validation keywords, so none of
	// them is conditioned on the validation vocabulary the way the empty enum
	// above is: a metaschema can withhold `enum` and still assert `not`, and
	// allOfContainsFalseSchema and compositionAdmitsNothing have always read
	// these unconditionally.
	if s.Not != nil && g.acceptsEveryInstance(s.Not) {
		return true
	}
	for _, sub := range s.AllOf {
		if g.branchForbidsEveryValue(sub, depth+1, onPath) {
			return true
		}
	}
	if g.everyBranchForbids(s.AnyOf, depth, onPath) {
		return true
	}
	if g.everyBranchForbids(s.OneOf, depth, onPath) {
		return true
	}
	return false
}

// everyBranchForbids reports whether a disjunction has branches and every one of
// them admits nothing.
//
// The emptiness test is the arm every schema in the corpus takes, since almost
// none states an anyOf or a oneOf at all: a bare `for` over no branches falls
// out of its loop reporting that they all forbid, which would make the empty set
// of every schema there is.
func (g *Generator) everyBranchForbids(subs []*schema.Schema, depth int, onPath map[*schema.Schema]bool) bool {
	if len(subs) == 0 {
		return false
	}
	for _, sub := range subs {
		if !g.branchForbidsEveryValue(sub, depth+1, onPath) {
			return false
		}
	}
	return true
}

// branchForbidsEveryValue judges a composition branch, following a $ref before
// reading it. A branch is the one position where the reference has to be
// resolved: it is the schema the composition applies, not a name standing for
// something else the caller will reach by another route.
func (g *Generator) branchForbidsEveryValue(sub *schema.Schema, depth int, onPath map[*schema.Schema]bool) bool {
	if sub == nil || depth > maxRuntimeDepth {
		return false
	}
	if g.forbidsEveryValueOnPath(sub, depth, onPath) {
		return true
	}
	if effRef := sub.EffectiveRef(); effRef != "" {
		if resolved := g.resolveRefInContext(effRef, sub); resolved != nil {
			return g.forbidsEveryValueOnPath(resolved, depth+1, onPath)
		}
	}
	return false
}

// acceptsEveryInstance reports whether a schema admits every JSON value, so that
// a `not` over it admits none.
//
// It is isAcceptAllSchema plus the one keyword that predicate cannot judge on
// its own. `format` binds as an assertion on some dialects and as an annotation
// on others, so whether {"format":"email"} constrains anything is the
// generator's question and not the schema's -- and read as accept-all under
// assertion it made {"not":{"format":"email"}} forbid every value, including the
// strings that are not email addresses and the numbers `format` never speaks
// about. That is a false rejection, and it was reachable before this change
// through extractNotSchemaDef; the fix is here so that both callers get it.
func (g *Generator) acceptsEveryInstance(s *schema.Schema) bool {
	if s == nil || !isAcceptAllSchema(s) {
		return false
	}
	return !(s.Format != nil && g.formatAssertsFor(s))
}

// declaredTypeAdmitsNoEnumMember reports whether a schema lists members its own
// "type" forbids every one of. {"type":"string","const":5} is the shortest
// spelling: a value cannot be both a string and 5, so the schema admits no
// instance -- the same statement as `false` and as `{"enum":[]}`, which is why
// it belongs beside them rather than in a predicate of its own.
//
// It is the enum half of issue #145. The symptom was a build failure rather than
// a wrong answer, because the const was emitted against the declared type and
// `const Root string = 5` does not compile; but the schema had already been read
// as one admitting a string, and every position that types from it -- an
// element, a map value, a branch, a tuple slot -- described values the schema
// forbids.
//
// Sound in one direction only, which is the direction that matters: a member the
// type rejects can be dropped whatever else the schema says, because `type` and
// the enum are both assertions on the instance and an instance has to satisfy
// every one. Nothing here claims the surviving members *are* admissible -- a
// `minLength` or a `not` may still forbid them -- so the answer is a refusal
// only when the list empties.
func (g *Generator) declaredTypeAdmitsNoEnumMember(s *schema.Schema) bool {
	kept, filtered := g.enumMembersDeclaredTypeAdmits(s)
	return filtered && len(kept) == 0
}

// enumMembersDeclaredTypeAdmits returns the members of a schema's enum (or of
// the const it promotes to) that its own "type" admits, and reports whether the
// question applies at all. When the second result is false the first is
// meaningless and the caller must read s.Enum unchanged.
//
// The question does not apply in four cases, and each of them is a reading the
// schema does not license:
//
//   - No "type" of its own. Nothing to filter against; a type this generator
//     *infers* is not an assertion the schema made.
//   - No enum and no const. Nothing to filter.
//   - A draft 3 schema-valued entry in the type array, held on TypeSchemas
//     rather than on Type. Those alternatives widen the union past the names
//     Type lists, so a member matching none of the names may still match one of
//     them.
//   - A $ref on a draft where it displaces its siblings (3 through 7). There the
//     "type" asserts nothing at all, so it cannot forbid anything either, and
//     treating it as a filter would refuse documents the referenced schema
//     admits. From 2019-09 on $ref is an applicator and the sibling applies, so
//     the filter does.
//
// The vocabulary gate is the caller's: schemaForbidsEveryValue asks it once for
// both spellings, and generateEnumDef is only ever reached through one.
func (g *Generator) enumMembersDeclaredTypeAdmits(s *schema.Schema) ([]any, bool) {
	if s == nil || len(s.Type) == 0 || len(s.TypeSchemas) > 0 {
		return nil, false
	}
	if s.Ref != "" && g.refOverridesSiblingsForSchema(s) {
		return nil, false
	}
	// promoteConstToEnum rather than a second reading of Const beside it: the two
	// spellings are one keyword to every other part of this generator, and a
	// const written {"const":null} is only visible through ConstIsNull, which is
	// exactly the sort of detail a second copy loses.
	members := promoteConstToEnum(s).Enum
	if len(members) == 0 {
		return nil, false
	}
	kept := make([]any, 0, len(members))
	for _, m := range members {
		if jsonValueMatchesAnySchemaType(m, s.Type) {
			kept = append(kept, m)
		}
	}
	if len(kept) == len(members) {
		return nil, false
	}
	return kept, true
}

// jsonValueMatchesAnySchemaType reports whether a decoded JSON value is of at
// least one of the JSON Schema types named. An empty list answers false, which
// no caller reaches.
func jsonValueMatchesAnySchemaType(v any, types schema.TypeList) bool {
	for _, t := range types {
		if jsonValueMatchesSchemaType(v, t) {
			return true
		}
	}
	return false
}

// jsonValueMatchesSchemaType reports whether a decoded JSON value is of the JSON
// Schema type named by t.
//
// An unrecognised name answers true, which is what keeps this from inventing a
// constraint: draft 3's "any" matches everything by definition, and a name this
// generator does not model is a name it must not judge against.
//
// "integer" reads the value and not the notation. A schema is decoded by
// encoding/json, so a member written 1 and one written 1.0 are the same float64
// by the time this sees them, and the drafts that require an integer *token* of
// an instance (3 and 4) still admit an instance written 1 for a member written
// 1.0 -- the two are equal as numbers, which is what `enum` and `const` compare.
// Judging the notation here would drop a member those drafts admit.
func jsonValueMatchesSchemaType(v any, t string) bool {
	switch t {
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "number":
		_, ok := jsonNumericValue(v)
		return ok
	case "integer":
		f, ok := jsonNumericValue(v)
		return ok && !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f)
	default:
		return true
	}
}

// jsonNumericValue reads a schema-supplied value as a number.
//
// float64 is what encoding/json produces and is the only case a schema read
// from a document reaches. The integer kinds are for a Schema built in Go --
// this package's own callers do that, and a caller who wrote Enum: []any{5}
// beside Type: "integer" must not have the member read as a non-number and
// filtered away.
func jsonNumericValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	}
	return 0, false
}

// enumFitsConstForm reports whether every member can be declared as a Go
// constant of base, which is what the const form of an enum emits.
//
// A base that is not a primitive -- the map an "object" type maps to, the slice
// an "array" type maps to -- has no constants at all, and `any` has none either.
// A primitive rejects a member of the wrong Go kind. Either way the emitted
// source does not build, so the caller falls back to the raw form.
func enumFitsConstForm(base GoType, values []any) bool {
	pt, ok := base.(*PrimitiveType)
	if !ok {
		return false
	}
	for _, v := range values {
		switch pt.Name {
		case "string":
			if _, ok := v.(string); !ok {
				return false
			}
		case "bool":
			if _, ok := v.(bool); !ok {
				return false
			}
		case "float64":
			if _, ok := jsonNumericValue(v); !ok {
				return false
			}
		case "int64":
			f, ok := jsonNumericValue(v)
			if !ok || math.IsInf(f, 0) || math.IsNaN(f) || f != math.Trunc(f) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// typeIsInferredFromConstraints reports whether a schema states no "type" of its
// own and yet resolveType would give it one, read off a validation keyword.
//
// The exclusions name the arms of resolveType that run before the inference and
// answer from something the schema does state: a boolean schema, a type list, a
// draft-3 type alternative, an enum, a const (which promoteConstToEnum turns
// into one), and a reference of any spelling. None of those is inferred, so
// none of them is this predicate's business.
func (g *Generator) typeIsInferredFromConstraints(s *schema.Schema) bool {
	if s == nil || s.IsBooleanSchema() {
		return false
	}
	if len(s.Type) > 0 || len(s.TypeSchemas) > 0 {
		return false
	}
	if len(s.Enum) > 0 || s.Const != nil || s.ConstIsNull {
		return false
	}
	if s.EffectiveRef() != "" || s.DynamicRef != "" || s.RecursiveRef != "" {
		return false
	}
	return g.inferTypeFromConstraints(s) != ""
}

// containerElem returns what a slice or a map holds, and whether the container
// was a map. A type that is neither answers nil, which ends the descent.
func containerElem(t GoType) (GoType, bool) {
	switch v := t.(type) {
	case *ArrayType:
		return v.ItemType, false
	case *MapType:
		return v.ValueType, true
	}
	return nil, false
}

// containerElemSchema returns the sub-schema governing what a container holds:
// `items` for a slice, `additionalProperties` for a map.
//
// The map arm asks effectiveType, not primarySchemaType, because that is the
// question resolveType asked when it made this position a Go map in the first
// place. Asking the narrower one instead answered nil for every object that
// states no "type" of its own -- {"additionalProperties":{"type":"string",
// "minLength":7}} is an object by inference and was typed map[string]string,
// but the descent stopped before it and minLength was enforced nowhere.
func (g *Generator) containerElemSchema(s *schema.Schema, isMap bool) *schema.Schema {
	if isMap {
		return mapValueSchema(s, g.effectiveType(s))
	}
	return g.singleItemsSchema(s)
}

// effectiveType is the JSON type a schema is generated as: the one it declares,
// or the one its keywords imply when it declares none. resolveType picks a Go
// type by exactly this rule, so anything that must agree with the Go type it
// produced has to ask the same question.
func (g *Generator) effectiveType(s *schema.Schema) string {
	if t := primarySchemaType(s); t != "" {
		return t
	}
	return g.inferTypeFromConstraints(s)
}

// buildItemValidation walks a slice- or map-typed field's Go type and its
// schema's `items` / `additionalProperties` chain in step, collecting the
// constraints that apply at each level. Returns nil when nothing is left to
// check.
//
// A map is here for the same reason a slice is. Dispatching to a value's own
// Validate only works when the value Go type is named; a map[string]string
// carries no such method, so without this the keywords under
// additionalProperties are enforced nowhere and the generated Validate accepts
// data the schema forbids. What differs is only how the level is addressed --
// by key rather than by index -- which the emitter reads off ItemLevel.IsMap.
//
// The descent stops at a named element type: that type was generated from this
// very sub-schema and answers for it through its own Validate. Whether such a
// call is actually emitted is settled later, by resolveItemValidations, since
// which types carry methods is only known once every type def exists.
func (g *Generator) buildItemValidation(parentName, fieldName, jsonName string, fieldType GoType, s *schema.Schema) *ItemValidationDef {
	if fieldType == nil {
		return nil
	}
	base := fieldType
	isPointer := false
	if pt, ok := base.(*PointerType); ok {
		base = pt.Inner
		isPointer = true
	}
	elemType, isMap := containerElem(base)
	if elemType == nil {
		return nil
	}

	def := &ItemValidationDef{FieldName: fieldName, JSONName: jsonName, IsPointer: isPointer}
	g.descendItemLevels(def, elemType, g.containerElemSchema(s, isMap), isMap, parentName+fieldName)
	if !def.trim(ItemLevel.pending) {
		return nil
	}
	return def
}

// buildNullCheck describes where a JSON null is forbidden beneath one value:
// at the value itself, and at each level of anonymous array or map nesting
// below it that the schema speaks about. Returns nil when nothing at any level
// forbids one.
//
// The walk mirrors descendItemLevels -- Go type and sub-schema chain in step,
// bounded by the same maxItemLevels -- because it has to describe exactly the
// nesting the Go type spells out. It stops at a named element type, whose own
// generated code answers for everything beneath it.
//
// jsonName labels the outermost level; a value that is not a named property
// (an alias's own value, an overflow entry) passes "".
func (g *Generator) buildNullCheck(jsonName string, t GoType, s *schema.Schema) *NullCheckDef {
	return g.nullCheckLevel(jsonName, t, s, 0).prune()
}

func (g *Generator) nullCheckLevel(jsonName string, t GoType, s *schema.Schema, depth int) *NullCheckDef {
	if t == nil || s == nil || depth >= maxItemLevels {
		return nil
	}
	def := &NullCheckDef{JSONName: jsonName, Reject: g.schemaForbidsNull(s)}
	// A named type is where the walk ends: it was generated from this very
	// sub-schema and carries its own rules. Only the level itself is judged
	// here, because a pointer to it never reaches its UnmarshalJSON on a null.
	if namedTypeName(t) != "" {
		return def
	}
	base := t
	if pt, ok := base.(*PointerType); ok {
		base = pt.Inner
	}
	elemType, isMap := containerElem(base)
	if elemType == nil {
		return def
	}
	elemSchema := g.containerElemSchema(s, isMap)
	if elemSchema == nil {
		return def
	}
	def.IsMap = isMap
	def.Elem = g.nullCheckLevel("", elemType, elemSchema, depth+1)
	return def
}

// aliasNullCheck is buildNullCheck for a named container -- `type SArr
// []string`, `type M map[string]string` -- whose own value is already answered
// for by the NeedsNullCheck arm of its UnmarshalJSON. Only what it holds is left
// to judge, so the outermost level is cleared before the rule is pruned: without
// that, every such alias would carry a rule that merely restates the check
// sitting three lines above it.
func (g *Generator) aliasNullCheck(t GoType, s *schema.Schema) *NullCheckDef {
	def := g.nullCheckLevel("", t, s, 0)
	if def == nil {
		return nil
	}
	def.Reject = false
	return def.prune()
}

// buildOverflowValidation collects what a schema-valued `additionalProperties`
// demands of the values it governs, in the position where the keyword sits
// beside `properties` or `patternProperties` and so speaks only about the keys
// those do not claim.
//
// That position is the one buildItemValidation cannot reach. It derives the
// value schema through mapValueSchema, which answers nil the moment a sibling
// `properties` or `patternProperties` exists -- correctly, since the object is
// then a struct with an overflow map rather than a Go map. The overflow map is
// typed, so a value of the wrong JSON type dies in the decoder, but until this
// existed nothing checked a `minimum` on one and
// {"properties":{"alpha":{"type":"string"}},
//
//	"additionalProperties":{"type":"integer","minimum":5}}
//
// accepted {"alpha":"aa","zzExtra":1}.
//
// The value schema is applied to exactly the keys the overflow map holds, which
// is exactly the set `additionalProperties` governs: a key `properties` or
// `patternProperties` claims never lands there, and a key one of the schema's
// *sibling* applicators declares does, which is what the spec asks for.
// parentName is the owning struct's name. It prefixes any type the descent has
// to materialize below the value -- a tuple position, say -- and is the same
// prefix the value type itself was resolved under (name+"Value"), so a nested
// name cannot collide with one minted anywhere else for this struct.
func (g *Generator) buildOverflowValidation(parentName string, valueType GoType, valueSchema *schema.Schema) *ItemValidationDef {
	if valueType == nil || valueSchema == nil || valueSchema.IsBooleanSchema() {
		return nil
	}
	def := &ItemValidationDef{
		FieldName:     overflowFieldName,
		PathName:      "additionalProperties",
		OwnsOutermost: true,
	}
	g.descendItemLevels(def, valueType, valueSchema, true, parentName+"Value")
	if !def.trim(ItemLevel.pending) {
		return nil
	}
	return def
}

// overflowFieldName is the Go field the generated struct keeps unknown keys in.
// fieldmap.go reserves the name, so no declared property can take it.
const overflowFieldName = "AdditionalProperties"

// descendItemLevels walks a container's Go type and its sub-schema chain in
// step, appending one level per container depth. The caller supplies the
// outermost element and the schema that governs it, which is what lets the
// overflow map -- whose value schema is not reachable from its container's --
// share the descent.
//
// namePrefix names any type a level has to materialize -- the positions of an
// element that is itself a tuple. Each caller passes the prefix its own value
// type was resolved under, so a name minted here agrees with the one the type
// carries and cannot collide with another container's.
func (g *Generator) descendItemLevels(def *ItemValidationDef, elemType GoType, elemSchema *schema.Schema, isMap bool, namePrefix string) {
	for elemSchema != nil && len(def.Levels) < maxItemLevels {
		level := ItemLevel{
			IndexVar:      itemLevelVar(isMap, len(def.Levels)),
			ElemVar:       fmt.Sprintf("_e%d", len(def.Levels)),
			IsMap:         isMap,
			ElemIsPointer: elemType.IsPointer(),
			ElemType:      elemType,
			ElemTypeName:  namedTypeName(elemType),
		}
		if level.ElemTypeName == "" {
			level.Rules = elementRules(elemType, elemSchema)
			// The format posture is the schema's, not the container's: an
			// element is read under the dialect of the document it is written
			// in, like every other position. elementRules cannot ask, having no
			// generator.
			if !g.formatAssertsFor(elemSchema) {
				level.Rules = withoutFormatRules(level.Rules)
			} else {
				level.Rules = g.formatRulesForDialect(elemSchema, level.Rules)
			}
			// And the content posture, asked of the same schema for the same
			// reason. Only draft 7 asserts these keywords; everywhere else they
			// annotate, and a check written here would reject what the schema
			// permits.
			if !g.contentAssertsFor(elemSchema) {
				level.Rules = withoutContentRules(level.Rules)
			}
			// An element that is a tuple in its own right. The descent stops
			// here either way -- singleItemsSchema answers nil for a tuple,
			// since `items` there governs only the tail -- so without this the
			// positions would be reached by nothing at all.
			if _, isAnySlice := anyElementSliceField(elemType); isAnySlice {
				tupleName := fmt.Sprintf("%sItems%d", namePrefix, len(def.Levels))
				level.TupleItems = g.buildTupleItemDefs(elemSchema, tupleName)
				level.TupleTail = g.buildTupleTailDef(elemSchema, tupleName)
				level.UnevalItems = g.elemUnevalItemsDef(elemSchema)
			}
		}
		def.Levels = append(def.Levels, level)
		if level.ElemTypeName != "" {
			break
		}
		inner := elemType
		if pt, ok := inner.(*PointerType); ok {
			inner = pt.Inner
		}
		nested, nestedIsMap := containerElem(inner)
		if nested == nil {
			break
		}
		elemSchema = g.containerElemSchema(elemSchema, nestedIsMap)
		elemType, isMap = nested, nestedIsMap
	}
}

// itemLevelVar names a level's loop variable, so that generated source reads as
// what it iterates: an index over a slice, a key over a map.
func itemLevelVar(isMap bool, level int) string {
	if isMap {
		return fmt.Sprintf("_k%d", level)
	}
	return fmt.Sprintf("_i%d", level)
}

// buildFieldContains collects the `contains` constraint an array property
// states, for the case where the property stayed a plain Go slice. Returns nil
// when there is nothing to check.
//
// The guard is the Go type, not the schema. A property that was materialized as
// a named type carries the very same constraint in its own Validate, which the
// struct already dispatches to through ValidatableFields, so emitting here as
// well would count the elements twice and report the failure twice over.
func (g *Generator) buildFieldContains(parentName, fieldName, jsonName string, fieldType GoType, s *schema.Schema, optional bool) *FieldContainsDef {
	if fieldType == nil || s == nil || s.Contains == nil {
		return nil
	}
	base := fieldType
	isPointer := false
	if pt, ok := base.(*PointerType); ok {
		base = pt.Inner
		isPointer = true
	}
	if _, ok := base.(*ArrayType); !ok {
		return nil
	}
	def, minContains, maxContains := g.extractContainsDef(s, parentName+fieldName)
	if !containsCanReject(def, minContains, maxContains) {
		return nil
	}
	return &FieldContainsDef{
		FieldName:   fieldName,
		JSONName:    jsonName,
		IsPointer:   isPointer,
		Optional:    optional,
		Contains:    def,
		MinContains: minContains,
		MaxContains: maxContains,
	}
}

// noteFieldContainsImports records what the emitted contains checks need. Every
// test marshals the element first, so json is needed for all but the boolean
// forms, and the count is always reported through fmt. The pattern test uses
// the standard library's regexp, not ecma262, which is why it reports through
// needsStdRegexp -- matching what the alias form of the same check already does.
func noteFieldContainsImports(defs []FieldContainsDef, needsFmt, needsJSON, needsMath, needsStdRegexp *bool) {
	for _, fc := range defs {
		*needsFmt = true
		if fc.Contains == nil || fc.Contains.IsFalse {
			continue
		}
		if !fc.Contains.IsTrue {
			*needsJSON = true
		}
		for _, chk := range fc.Contains.Checks {
			switch {
			case chk.CheckType == "multipleOf":
				*needsMath = true
			case chk.CheckType == "type" && chk.Value == "integer":
				*needsMath = true
			case chk.CheckType == "pattern":
				*needsStdRegexp = true
			}
		}
	}
}

// anyElementSliceField reports whether a property's Go type is a slice of
// `any`, and whether it arrived behind a pointer.
//
// That is the shape both new field checks below need, and it is not an
// arbitrary restriction. The per-position tuple arms and the unevaluatedItems
// arms read each element as an `any` -- a type assertion for the cheap tests, a
// json.Marshal for the rest -- and []any is what resolveType answers for any
// array these keywords apply to: a tuple has no homogeneous element type (see
// isTupleArray), and an array carrying unevaluatedItems is one whose tail the
// generator declines to type. A field of any other slice type did not come from
// such a schema.
func anyElementSliceField(fieldType GoType) (isPointer, ok bool) {
	base := fieldType
	if pt, isPtr := base.(*PointerType); isPtr {
		base = pt.Inner
		isPointer = true
	}
	at, isArray := base.(*ArrayType)
	if !isArray {
		return isPointer, false
	}
	prim, isPrim := at.ItemType.(*PrimitiveType)
	return isPointer, isPrim && prim.Name == "any"
}

// buildFieldTuple collects the positional checks an array property states
// through prefixItems (2020-12) or items-as-array (draft 4-7), for the case
// where the property stayed a plain Go slice. Returns nil when there is nothing
// to check.
//
// The guard is the Go type, exactly as buildFieldContains's is: a property that
// was materialized as a named type carries the same positions in its own
// Validate, which the struct dispatches to through ValidatableFields, so
// emitting here as well would report every failure twice.
func (g *Generator) buildFieldTuple(fieldName, jsonName, parentName string, fieldType GoType, s *schema.Schema) *FieldTupleDef {
	if s == nil {
		return nil
	}
	isPointer, ok := anyElementSliceField(fieldType)
	if !ok {
		return nil
	}
	posName := parentName + fieldName
	items := g.buildTupleItemDefs(s, posName)
	if len(items) == 0 {
		return nil
	}
	return &FieldTupleDef{
		FieldName: fieldName,
		JSONName:  jsonName,
		IsPointer: isPointer,
		Items:     items,
		Tail:      g.buildTupleTailDef(s, posName),
	}
}

// elemUnevalItemsDef builds the unevaluatedItems constraint of an array that is
// not a named type of its own -- a property's slice, or a slice nested inside
// one -- keeping only the shapes the static check can decide. See
// buildFieldUnevalItems for why the rest are refused rather than approximated.
func (g *Generator) elemUnevalItemsDef(s *schema.Schema) *UnevaluatedItemsDef {
	if s == nil || s.UnevaluatedItems == nil {
		return nil
	}
	def := g.buildUnevaluatedItemsDef(s)
	if def == nil || len(def.ConditionalEvals) > 0 || def.ContainsEvaluates {
		return nil
	}
	return def
}

// buildFieldUnevalItems collects the `unevaluatedItems` constraint an array
// property states, for the case where the property stayed a plain Go slice and
// the constraint can be decided without running the schema.
//
// A def carrying ConditionalEvals or ContainsEvaluates is refused rather than
// emitted. Both say the evaluated set depends on the value in hand -- which
// anyOf branch matched, which elements `contains` matched -- and neither is
// reflected in EvaluatedCount, so emitting the static check would forbid items
// that a matching branch had in fact evaluated. That is a false rejection of a
// conforming document, which is worse than the missing check: those shapes
// belong to the runtime annotation evaluator, which the property reaches by
// being materialized into a named type (see inlineAnnotationWrapper), and where
// the evaluator cannot model the subtree either, nothing is emitted at all.
func (g *Generator) buildFieldUnevalItems(fieldName, jsonName string, fieldType GoType, s *schema.Schema) *FieldUnevalItemsDef {
	isPointer, ok := anyElementSliceField(fieldType)
	if !ok {
		return nil
	}
	def := g.elemUnevalItemsDef(s)
	if def == nil {
		return nil
	}
	return &FieldUnevalItemsDef{
		FieldName: fieldName,
		JSONName:  jsonName,
		IsPointer: isPointer,
		Def:       def,
	}
}

// noteFieldTupleImports records what the emitted per-position checks need. The
// named-type arm marshals the element and calls the position type's Validate,
// so it needs json; the "integer" test is math.Trunc; every arm reports through
// fmt.
func noteFieldTupleImports(defs []FieldTupleDef, needsFmt, needsJSON, needsMath *bool) {
	for _, ft := range defs {
		*needsFmt = true
		items := ft.Items
		if ft.Tail != nil {
			items = append(append([]TupleItemDef{}, items...), *ft.Tail)
		}
		for _, ti := range items {
			if ti.TypeName != "" {
				*needsJSON = true
			}
			if ti.JSONType == "integer" {
				*needsMath = true
			}
		}
	}
}

// noteFieldUnevalItemsImports records what the emitted unevaluatedItems checks
// need. Anything past the forbidden form marshals each unevaluated element, and
// the integer and multipleOf tests reach for math.
func noteFieldUnevalItemsImports(defs []FieldUnevalItemsDef, needsFmt, needsJSON, needsMath *bool) {
	for _, fu := range defs {
		*needsFmt = true
		if fu.Def == nil {
			continue
		}
		if fu.Def.ValueType != "" || len(fu.Def.Checks) > 0 {
			*needsJSON = true
		}
		if fu.Def.ValueType == "integer" {
			*needsMath = true
		}
		for _, chk := range fu.Def.Checks {
			if chk.CheckType == "multipleOf" {
				*needsMath = true
			}
		}
	}
}

// elementRules keeps the constraints from an element schema that compile
// against the element's Go type: the string keywords need a string, the numeric
// keywords a number, the length keywords a slice. const goes through
// json.Marshal, so it applies to any element the emitter can marshal back to
// the form the const value was written in.
//
// An allOf on the element schema is folded in the same way it is for a
// property: a branch that only bounds a scalar leaves the element a plain Go
// value with nothing to dispatch to, so its keywords would otherwise reach
// nothing. See allOfConstraintRules.
func elementRules(elemType GoType, s *schema.Schema) []ValidationRule {
	kind := elementGoKind(elemType)
	var out []ValidationRule
	elemRules := extractValidationRules("", "", s)
	elemRules = append(elemRules, allOfConstraintRules("", "", s, elemType)...)
	for _, rule := range elemRules {
		switch rule.RuleType {
		case "minLength", "maxLength", "pattern", "content":
			if kind != "string" {
				continue
			}
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf":
			if kind != "number" {
				continue
			}
		case "minItems", "maxItems":
			if kind != "slice" {
				continue
			}
		case "const":
			// json.RawMessage marshals back byte for byte, whitespace and all,
			// so a textual comparison against the const would reject values the
			// schema allows.
			if kind == "raw" {
				continue
			}
		case "format":
			// The element carries a format check on the same terms a field
			// does, decided from the element's own Go type. Until this arm
			// existed the keyword fell through the default below and
			// {"items":{"type":"string","format":"ipv4"}} accepted an IPv6
			// address in every element, while the identical subschema written
			// as a property was checked.
			stringBacked, ok := formatRuleShape(elemType, rule, false)
			if !ok {
				continue
			}
			rule.StringBacked = stringBacked
		default:
			continue
		}
		out = append(out, rule)
	}
	return out
}

// elementGoKind classifies an element Go type by what an emitted check may
// assume of it. An unclassified type ("") still admits the const check, which
// needs nothing but json.Marshal.
func elementGoKind(t GoType) string {
	if pt, ok := t.(*PointerType); ok {
		t = pt.Inner
	}
	switch v := t.(type) {
	case *ArrayType:
		return "slice"
	case *PrimitiveType:
		switch v.Name {
		case "string":
			return "string"
		case "int64", "float64":
			return "number"
		case "json.RawMessage":
			return "raw"
		}
	}
	return ""
}

// resolveItemValidations settles the part of the per-element checks that could
// only be decided once every type def existed: whether a named element type
// carries a Validate to dispatch to. A struct field's outermost element is left
// alone, since ValidatableFields already dispatches for it and a second call
// would validate every element twice -- unless the definition says otherwise
// through OwnsOutermost, which the overflow map does: no field dispatch reaches
// it, so its own definition has to make the call.
func (g *Generator) resolveItemValidations() {
	validatableTypes := make(map[string]bool)
	for _, td := range g.output.TypeDefs {
		if localTypeIsValidatable(td) {
			validatableTypes[td.TypeName()] = true
		}
	}

	for _, td := range g.output.TypeDefs {
		var defs *[]ItemValidationDef
		// An array alias has no field for ValidatableFields to reach, so its
		// outermost element is this pass's responsibility.
		ownsOutermost := false
		switch d := td.(type) {
		case *StructDef:
			defs = &d.ItemValidations
		case *AliasDef:
			if !d.CanHaveMethods() {
				d.ItemValidations = nil
				continue
			}
			defs, ownsOutermost = &d.ItemValidations, true
		default:
			continue
		}

		kept := (*defs)[:0]
		for i := range *defs {
			def := &(*defs)[i]
			for li := range def.Levels {
				level := &def.Levels[li]
				if level.ElemTypeName == "" || (li == 0 && !ownsOutermost && !def.OwnsOutermost) {
					continue
				}
				// A name from another package is only answered by that
				// package's record of it; the local table happening to hold
				// the same name says nothing about the foreign type.
				if foreign := crossPackageNamed(level.ElemType); foreign != nil {
					level.CallValidate = foreign.foreignValidatable
					continue
				}
				level.CallValidate = validatableTypes[level.ElemTypeName]
			}
			if def.trim(ItemLevel.carries) {
				kept = append(kept, *def)
			}
		}
		if len(kept) == 0 {
			*defs = nil
		} else {
			*defs = kept
		}
	}
}

func (g *Generator) populateAliasDelegates() {
	// An alias over a type that carries its own JSON representation borrows
	// none of it (selfMarshallingTypeName says which types those are), so it
	// has to route both directions through that type explicitly. This runs
	// before the tables below are built so that an alias *of* such an alias is
	// seen to have marshalling of its own and delegates in turn -- the chain
	// `type B A; type A time.Time` is one $ref away in any schema.
	for _, td := range g.output.TypeDefs {
		ad, ok := td.(*AliasDef)
		if !ok || !ad.CanHaveMethods() {
			continue
		}
		name := selfMarshallingTypeName(ad.Underlying)
		if name == "" {
			continue
		}
		if ad.UnmarshalAs == "" {
			ad.UnmarshalAs = name
		}
		if ad.MarshalAs == "" {
			ad.MarshalAs = name
		}
	}

	validatableTypes := make(map[string]bool)
	unmarshalTypes := make(map[string]bool)
	marshalTypes := make(map[string]bool)
	for _, td := range g.output.TypeDefs {
		switch d := td.(type) {
		case *StructDef:
			validatableTypes[d.Name] = true
			if d.NeedsUnmarshal {
				unmarshalTypes[d.Name] = true
			}
			if d.NeedsMarshal {
				marshalTypes[d.Name] = true
			}
		case *EnumDef:
			validatableTypes[d.Name] = true
			// An enum is not always just its base type to encoding/json. A
			// heterogeneous one is a json.RawMessage that keeps the bytes it
			// was handed, and an int64 one whose draft admits 1.0 reads the
			// number through jsonInteger; both say so in methods of their own,
			// which an alias over the enum does not inherit. Left out of these
			// tables, `type Root RawEnum` decoded its own "a" as base64 and
			// `type Root IntEnum` refused the 1.0 the enum exists to accept.
			if d.IsRaw || d.IntegerToken {
				unmarshalTypes[d.Name] = true
			}
			if d.IsRaw {
				marshalTypes[d.Name] = true
			}
		case *InferredAliasDef:
			validatableTypes[d.Name] = true
		case *BigIntAliasDef:
			validatableTypes[d.Name] = true
		// The raw-JSON wrappers. Each is a struct holding one unexported field
		// and reaching JSON only through its own UnmarshalJSON and MarshalJSON,
		// which `type Root Wrapper` does not inherit -- encoding/json then sees a
		// struct with no exported field and refuses every document that is not an
		// object, for a schema that accepts them. Naming them here is what makes
		// the alias delegate both directions, exactly as it does for a raw enum.
		case *TypeOnlySchemaDef:
			validatableTypes[d.Name] = true
			unmarshalTypes[d.Name] = true
			marshalTypes[d.Name] = true
		case *NotSchemaDef:
			validatableTypes[d.Name] = true
			unmarshalTypes[d.Name] = true
			marshalTypes[d.Name] = true
		case *DynamicSchemaDef:
			validatableTypes[d.Name] = true
			unmarshalTypes[d.Name] = true
			marshalTypes[d.Name] = true
		case *AnnotationSchemaDef:
			validatableTypes[d.Name] = true
			unmarshalTypes[d.Name] = true
			marshalTypes[d.Name] = true
		case *AliasDef:
			if d.CanHaveMethods() {
				validatableTypes[d.Name] = true
				if d.NeedsNullCheck || d.IsIntegerType() || d.UnmarshalAs != "" {
					unmarshalTypes[d.Name] = true
				}
				if d.MarshalAs != "" {
					marshalTypes[d.Name] = true
				}
			}
		}
	}

	// An alias that gains a delegate becomes worth delegating to in turn, and
	// the tables above were built before any of that was assigned. `type C2 C1;
	// type C1 D; type D netip.Addr` is two $refs in a schema and was the case
	// that failed: C1 was recorded as having no marshalling of its own -- true
	// when the table was built, false by the end of this loop -- so C2 got no
	// UnmarshalJSON at all. A defined type inherits none of netip.Addr's
	// methods, so C2 fell through to the underlying representation and refused
	// the ordinary address string that both C1 and D accept. A document the
	// schema permits, rejected in the decoder, because two loops ran in that
	// order.
	//
	// Recording each assignment as it is made is what closes it, and one pass is
	// enough because a chain is always generated innermost first: reaching C2
	// resolves its $ref to C1, which resolves to D, and each is appended when it
	// completes. The loop below therefore meets a delegate before whatever
	// borrows from it.
	//
	// That is an assumption about g.output.TypeDefs being in generation order,
	// and it is the thing to check first if a long alias chain ever loses its
	// UnmarshalJSON again. Anything that appends a definition before the one it
	// is defined over -- a reordering pass, a definition materialized eagerly
	// from a table rather than on the way down a $ref -- breaks it silently, and
	// the symptom is the decode failure described above rather than anything
	// this loop reports. A fixed-point loop was written for it and removed: no
	// schema produces that ordering today, so nothing could make the loop fail,
	// and unguarded machinery is worse than none.
	for _, td := range g.output.TypeDefs {
		if ia, ok := td.(*InferredAliasDef); ok && ia.ValidateAs == "" {
			if name := namedTypeName(ia.InferredGoType); name != "" && name != ia.Name && validatableTypes[name] {
				ia.ValidateAs = name
			}
		}

		ad, ok := td.(*AliasDef)
		if !ok || !ad.CanHaveMethods() {
			continue
		}
		name := namedTypeName(ad.Underlying)
		if name == "" || name == ad.Name {
			continue
		}
		if validatableTypes[name] {
			ad.ValidateAs = name
		}
		if unmarshalTypes[name] {
			ad.UnmarshalAs = name
		}
		if marshalTypes[name] {
			ad.MarshalAs = name
		}
		// What this alias just gained, the next one along may borrow.
		if ad.UnmarshalAs != "" {
			unmarshalTypes[ad.Name] = true
		}
		if ad.MarshalAs != "" {
			marshalTypes[ad.Name] = true
		}
		if ad.ValidateAs != "" || ad.CanHaveMethods() {
			validatableTypes[ad.Name] = true
		}
	}
}

// firstAllOfArrayAliasValidateAs names the array alias among the branches of an
// allOf whose element validation the merged alias can delegate to, generating
// that branch's definition on demand if nothing has yet.
//
// The on-demand generation is guarded by g.generated, which is only set when a
// definition *completes*, so it does not stop a branch that resolves back to
// the definition currently in flight -- {"$ref": "#", "items": {}} is enough:
// the items sibling routes the root through the implicit-allOf arm, and the
// synthesized $ref branch resolves to the root again, whose type is still being
// generated. That re-enters generateTypeDef forever, a stack overflow no
// recover intercepts. nodesInProgress is the mark that says so, and skipping
// such a branch costs nothing: a definition still in flight has emitted no
// TypeDef, so isArrayAlias could only have answered no anyway.
func (g *Generator) firstAllOfArrayAliasValidateAs(allOf []*schema.Schema) string {
	for _, sub := range allOf {
		if sub == nil {
			continue
		}
		if effRef := sub.EffectiveRef(); effRef != "" {
			if resolved := g.resolveEffectiveRefSchema(sub); resolved != nil {
				name := g.goNameForResolvedRef(effRef, resolved, refToGoName(effRef))
				if !g.generated[name] && !g.nodesInProgress[resolved] {
					_ = g.generateTypeDef(name, resolved)
				}
				if g.isArrayAlias(name) {
					return name
				}
			}
		}
		if sub.DynamicRef != "" {
			if resolved := g.resolveDynamicRef(sub.DynamicRef, sub); resolved != nil {
				name := g.goNameForResolvedRef(sub.DynamicRef, resolved, refToGoName(sub.DynamicRef))
				if !g.generated[name] && !g.nodesInProgress[resolved] {
					_ = g.generateTypeDef(name, resolved)
				}
				if g.isArrayAlias(name) {
					return name
				}
			}
		}
	}
	return ""
}

func (g *Generator) isArrayAlias(name string) bool {
	for _, td := range g.output.TypeDefs {
		if d, ok := td.(*AliasDef); ok && d.Name == name {
			_, ok := d.Underlying.(*ArrayType)
			return ok
		}
	}
	return false
}

// isMapAlias reports whether a named type is an alias whose underlying type is a map.
func (g *Generator) isMapAlias(name string) bool {
	for _, td := range g.output.TypeDefs {
		if d, ok := td.(*AliasDef); ok && d.Name == name {
			_, ok := d.Underlying.(*MapType)
			return ok
		}
	}
	return false
}

// isCollectionType reports whether a Go type is a slice or map (directly or via a
// named alias). Such optional fields use ",omitzero" so a present-but-empty
// collection is preserved on marshal.
func (g *Generator) isCollectionType(t GoType) bool {
	switch v := t.(type) {
	case *ArrayType, *MapType:
		return true
	case *NamedType:
		return g.isArrayAlias(v.Name) || g.isMapAlias(v.Name)
	}
	return false
}

// isInterfaceType reports whether a Go type is the empty interface (directly or
// via a named alias). Like a pointer or a collection, its nil is what unmarshal
// leaves when the property was absent, and it marshals to null.
func (g *Generator) isInterfaceType(t GoType) bool {
	switch v := t.(type) {
	case *PrimitiveType:
		return v.Name == "any"
	case *NamedType:
		for _, td := range g.output.TypeDefs {
			if d, ok := td.(*AliasDef); ok && d.Name == v.Name {
				pt, isPrim := d.Underlying.(*PrimitiveType)
				return isPrim && pt.Name == "any"
			}
		}
	}
	return false
}

// namedTypeName extracts the type name from a GoType if it's a NamedType
// (possibly wrapped in a PointerType). Returns "" otherwise.
func namedTypeName(t GoType) string {
	switch v := t.(type) {
	case *NamedType:
		return v.Name
	case *PointerType:
		return namedTypeName(v.Inner)
	default:
		return ""
	}
}

// crossPackageNamed returns the qualified NamedType inside t (through
// pointers and slice elements), if any.
func crossPackageNamed(t GoType) *NamedType {
	switch v := t.(type) {
	case *NamedType:
		if v.PkgAlias != "" {
			return v
		}
	case *PointerType:
		return crossPackageNamed(v.Inner)
	case *ArrayType:
		return crossPackageNamed(v.ItemType)
	}
	return nil
}

// crossPackageValidatable reports whether t references a type from another
// generated package that carries a Validate() method (per the owning
// package's registry entry).
func crossPackageValidatable(t GoType) bool {
	nt := crossPackageNamed(t)
	return nt != nil && nt.foreignValidatable
}

// crossPackageZeroLiteral returns the zero literal recorded by the owning
// package for a qualified type, qualified with the local import alias where
// it names the type itself.
func crossPackageZeroLiteral(t GoType) (string, bool) {
	nt := crossPackageNamed(t)
	if nt == nil {
		return "", false
	}
	zero := nt.foreignZeroLiteral
	if zero == nt.Name+"{}" {
		zero = nt.PkgAlias + "." + zero
	}
	return zero, true
}

// sliceElementTypeName extracts the element type name from a slice GoType.
// Handles []T, *[]T, []*T, *[]*T where T is a NamedType.
func sliceElementTypeName(t GoType) string {
	inner := t
	if pt, ok := inner.(*PointerType); ok {
		inner = pt.Inner
	}
	st, ok := inner.(*ArrayType)
	if !ok {
		return ""
	}
	return namedTypeName(st.ItemType)
}

// mapValueTypeName extracts the name of a map's value type when it is a named
// type (possibly behind a pointer). Returns "" for anything else.
func mapValueTypeName(t GoType) string {
	inner := t
	if pt, ok := inner.(*PointerType); ok {
		inner = pt.Inner
	}
	mt, ok := inner.(*MapType)
	if !ok {
		return ""
	}
	return namedTypeName(mt.ValueType)
}

// sliceElementIsPointer reports whether a slice's elements are pointers.
func sliceElementIsPointer(t GoType) bool {
	inner := t
	if pt, ok := inner.(*PointerType); ok {
		inner = pt.Inner
	}
	st, ok := inner.(*ArrayType)
	if !ok {
		return false
	}
	return st.ItemType.IsPointer()
}

// typeRejectsNull reports whether a generated type's schema declared a type
// that does not include null, so a JSON null in that position is invalid rather
// than merely absent. Types that carry no such answer report false, which
// leaves a null passed over rather than wrongly rejected.
func (g *Generator) typeRejectsNull(name string) bool {
	for _, td := range g.output.TypeDefs {
		if td.TypeName() != name {
			continue
		}
		switch d := td.(type) {
		case *StructDef:
			return d.NeedsNullCheck
		case *AliasDef:
			return d.NeedsNullCheck
		case *InferredAliasDef:
			return d.NeedsNullCheck
		default:
			return false
		}
	}
	return false
}

// mapValueIsPointer reports whether a map's values are pointers.
func mapValueIsPointer(t GoType) bool {
	inner := t
	if pt, ok := inner.(*PointerType); ok {
		inner = pt.Inner
	}
	mt, ok := inner.(*MapType)
	if !ok {
		return false
	}
	return mt.ValueType.IsPointer()
}

// zeroLiteralForType returns the Go zero value literal for a given type.
// For named types, it looks up the generated type definition to find the underlying type.
func (g *Generator) zeroLiteralForType(t GoType) string {
	switch v := t.(type) {
	case *PointerType:
		return "nil"
	case *ArrayType, *MapType:
		// Slices and maps have a nil zero value, not "".
		return "nil"
	case *PrimitiveType:
		return zeroForPrimitive(v.Name)
	case *NamedType:
		// Look up the generated type to find the underlying type.
		for _, td := range g.output.TypeDefs {
			if td.TypeName() == v.Name {
				switch d := td.(type) {
				case *EnumDef:
					return zeroForPrimitive(d.BaseType.GoTypeName())
				case *AliasDef:
					// Recurse so an alias backed by a slice/map/pointer
					// (e.g. `type Condition []any`) yields "nil" rather
					// than the "" fallback, which would emit an invalid
					// `field != ""` presence guard for a slice field.
					return g.zeroLiteralForType(d.Underlying)
				case *StructDef:
					// Structs don't have a meaningful zero literal for comparison.
					return ""
				case *InferredAliasDef:
					// InferredAliasDef is a wrapper struct — no meaningful zero literal.
					return ""
				case *BigIntAliasDef:
					// BigIntAliasDef is a wrapper struct — no meaningful zero literal.
					return ""
				case *TypeOnlySchemaDef, *NotSchemaDef, *DynamicSchemaDef, *AnnotationSchemaDef:
					// A raw-value wrapper struct — no meaningful zero literal.
					// Its Validate accepts the absent value, so the caller does
					// not need a presence guard. Without a case here the `""`
					// fallback below would emit `field != ""` against a struct,
					// which does not compile.
					return ""
				}
			}
		}
		return `""`
	default:
		return `""`
	}
}

// zeroForPrimitive returns the Go zero literal for a primitive type name.
func zeroForPrimitive(name string) string {
	switch name {
	case "string":
		return `""`
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return "0"
	case "bool":
		return "false"
	case "json.RawMessage":
		// A byte slice: its zero value is nil, not "". Raw (heterogeneous)
		// enums and multi-type wrappers are backed by json.RawMessage.
		return "nil"
	case "time.Time", "netip.Addr":
		// Structs, and not comparable to any literal. `format: date-time` and
		// `format: ipv4` behind a $ref become aliases over these, and the ""
		// fallback below then emitted `field != ""` against one -- generated
		// source that does not compile at all. Answering "no literal" sends the
		// caller to the _jsonKeys presence guard, which is what an optional
		// property of any other struct-shaped type already uses.
		return ""
	default:
		return `""`
	}
}

// refOverridesSiblings returns true if the current draft treats $ref as
// overriding all sibling keywords. In draft3 through draft7, $ref causes
// all sibling keywords to be ignored. Starting with draft 2019-09,
// $ref is just another applicator and sibling keywords apply normally.
func (g *Generator) refOverridesSiblings() bool {
	return refOverridesSiblingsForDraft(g.draft)
}

func (g *Generator) refOverridesSiblingsForSchema(s *schema.Schema) bool {
	return refOverridesSiblingsForDraft(g.draftForSchema(s))
}

func refOverridesSiblingsForDraft(draft schema.Draft) bool {
	switch draft {
	case schema.Draft03, schema.Draft04, schema.Draft06, schema.Draft07:
		return true
	default:
		// DraftUnknown: be conservative and assume modern behavior.
		return false
	}
}

// refDisplacesSiblingValues reports whether an `enum` or a `const` written
// beside a $ref is one the draft says to ignore.
//
// Through draft 7 a $ref replaces everything written beside it -- the rule
// refOverridesSiblingsForSchema states, and the one #118 applied to `type`. The
// three ladders that turn a schema into a Go type each read the enum *before*
// their ref arms: generateTypeDef, resolvePropertyType and resolveType all
// promote a const, then answer with the enum type. So on those drafts the
// sibling decided the type and the reference was never followed, and
// {"$ref":"#/definitions/aString","const":"a"} rejected "b" -- a document the
// draft admits, since the const is not there to be read (issue #151).
//
// From 2019-09 on $ref is an ordinary applicator and the sibling does apply, so
// the arms keep their order there and nothing changes. That is the whole of the
// difference: this is not a claim about which of the two should win where both
// apply, only about the drafts where one of them is not written at all.
//
// It is asked of the schema rather than of the run because $schema is a
// per-document declaration and a $ref may cross into a document that makes a
// different one -- the same reason refOverridesSiblingsForSchema exists beside
// refOverridesSiblings.
func (g *Generator) refDisplacesSiblingValues(s *schema.Schema) bool {
	return s != nil && s.EffectiveRef() != "" && g.refOverridesSiblingsForSchema(s)
}

// refMergesSiblingValues is refDisplacesSiblingValues on the other side of the
// same split: the drafts where an `enum` or a `const` written beside a $ref
// applies *beside* the reference rather than instead of it.
//
// From 2019-09 on $ref is an ordinary applicator, so the schema asserts the
// reference and the sibling at once -- an implicit allOf of the two. The enum
// arms of the three type ladders run ahead of their ref arms, so what happened
// instead was that the sibling applied *instead of* the reference: the enum
// decided the type, the $ref was never followed and the target's own keywords
// were dropped. {"$defs":{"Long":{"type":"string","minLength":5}},
// "$ref":"#/$defs/Long","const":"abc"} accepted "abc", which the target forbids
// (issue #153).
//
// So on these drafts the enum arms stand down too, and for the opposite reason:
// not because the sibling is absent, but because the merge arm below them is the
// only one that can state both halves. hasRefStructuralSiblings lists `enum` and
// `const` for the same reason it lists `type` (#118), which is what points the
// ref arms at that merge.
//
// The empty enum is deliberately not exempted here and not routed to the merge:
// `{"enum":[]}` admits nothing whatever the reference says, so generateTypeDef's
// forbidden arm above is already the complete answer and reaches it first.
//
// It asks for `$ref` and not for the other two references, because `$ref` is
// what the merge arm can claim: generateTypeDef synthesizes an allOf whose first
// branch carries the reference, and mergeAllOfBranches follows a `$ref` and a
// `$recursiveRef` from there but resolves neither a `$dynamicRef` nor the
// dynamic scope a `$recursiveRef` needs. Standing the enum arms down for a
// reference no later arm picks up drops the sibling as well as the target:
// {"$dynamicRef":"#T","const":"abc"} was left with no check at all, where before
// it at least enforced the const. Under-enforcement for those two spellings is
// what this leaves in place, and it is what they had.
//
// Asked of the schema rather than of the run, as refDisplacesSiblingValues is
// and for the same reason: $schema is a per-document declaration and a $ref may
// cross into a document that makes a different one.
func (g *Generator) refMergesSiblingValues(s *schema.Schema) bool {
	return s != nil && s.Ref != "" &&
		!g.refOverridesSiblingsForSchema(s) && statesEnumOrConst(s)
}

// enumTypeCarriesSchema reports whether the enum type generateEnumDef would
// build for a schema checks everything that schema asserts.
//
// An EnumDef holds one thing: the list of admissible values. Its Validate
// compares the instance against them and stops, so every other assertion on the
// schema is dropped the moment the enum arm claims it -- silently, because the
// type looks complete.
//
// Three keywords are carried anyway and are not counted here:
//
//   - "enum" and "const" are the list itself.
//   - "type" is carried by enumMembersDeclaredTypeAdmits, which drops the members
//     the declared type forbids before the type is built (#145). A list that
//     empties becomes a forbidden type, so the declared type is enforced exactly.
//   - "allOf" and "$ref", which generateAllOfDef preserves on its merged schema
//     for collectEvaluatedProperties and for unevaluatedProperties rather than as
//     assertions still to be met -- the branches were merged into the very schema
//     being asked about. Reading them as unmet would send every merged enum to a
//     wrapper, including the ones the merge carried in full.
//
// Anything else answers false, which is the direction that keeps a check: the
// caller then offers the position to the runtime evaluator and falls back to the
// enum when that declines.
func enumTypeCarriesSchema(s *schema.Schema) bool {
	present, ok := schemaKeywordSet(s)
	if !ok {
		return false
	}
	for key := range present {
		switch key {
		case "enum", "const", "type", "allOf", "$ref":
			continue
		}
		if nonConstrainingKeywords[key] || inertKeywords[key] {
			continue
		}
		return false
	}
	// A keyword with no field on Schema arrives as an extension, and nothing is
	// known about what it demands -- except for the handful known to demand
	// nothing, which keywordsOnly lets through here for the same reason: a schema
	// must not lose its enum type for carrying a comment.
	for key := range s.Extensions {
		if !inertKeywords[key] {
			return false
		}
	}
	return true
}

// statesEnumOrConst reports whether a schema pins the set of values it admits by
// listing them -- `enum` in any spelling, `const` in any spelling.
//
// The three spellings have to be asked for separately because two of them do not
// survive a round trip through JSON: `Enum` is tagged omitempty, so `"enum": []`
// re-marshals to nothing at all, and `ConstIsNull` is tagged "-" because
// encoding/json leaves a *any nil for a JSON null and the flag is the only record
// that `"const": null` was written. Reading the struct is what tells those two
// apart from a schema that states neither; see issue #154.
func statesEnumOrConst(s *schema.Schema) bool {
	return s != nil && (s.Enum != nil || s.Const != nil || s.ConstIsNull)
}

func (g *Generator) validationKeywordsEnabled() bool {
	return !g.validationKeywordsDisabled
}

func (g *Generator) requiresStrictIntegerToken(s *schema.Schema) bool {
	switch g.draftForSchema(s) {
	case schema.Draft03, schema.Draft04:
		return true
	default:
		return false
	}
}

// hasValidationVocabulary reports whether the validation keywords bind for a
// schema, which they do unless the metaschema it declares lists its vocabularies
// and leaves that one out. A document naming no metaschema, or one whose
// metaschema declares no $vocabulary at all, says nothing about the question and
// keeps the default.
func (g *Generator) hasValidationVocabulary(s *schema.Schema) bool {
	vocab := g.declaredVocabulary(s)
	if len(vocab) == 0 {
		return true
	}
	return declaresValidationVocabulary(vocab)
}

func declaresValidationVocabulary(vocabulary map[string]bool) bool {
	for uri, required := range vocabulary {
		if required && strings.HasSuffix(uri, "/vocab/validation") {
			return true
		}
	}
	return false
}

func (g *Generator) draftForSchema(s *schema.Schema) schema.Draft {
	if s == nil {
		return g.draft
	}
	if g.draftOverridden {
		// An explicit --draft (Config.Draft) is the user's statement about the
		// document they passed in. It takes precedence over the root document's
		// own $schema and over any $schema-less node. The one exception: an
		// embedded or remote resource that establishes its own $id-scoped
		// document root with an explicit $schema keeps its dialect, so
		// cross-draft $ref semantics are preserved.
		if root := s.DocumentRoot; root != nil && root != g.rootSchema {
			if d := schema.DetectDraft(root); d != schema.DraftUnknown {
				return d
			}
		}
		return g.draft
	}
	if d := schema.DetectDraft(s); d != schema.DraftUnknown {
		return d
	}
	if s.DocumentRoot != nil {
		if d := schema.DetectDraft(s.DocumentRoot); d != schema.DraftUnknown {
			return d
		}
	}
	return g.draft
}

// supportsPrefixItems reports whether prefixItems is a keyword of the dialect
// the schema is read under.
//
// A document that states no dialect is read as a modern one, the same rule
// refOverridesSiblingsForDraft already applies, and the same one every
// reference implementation applies -- python-jsonschema and ajv both fall back
// to the newest draft they know. Treating DraftUnknown as pre-2020 instead left
// the keyword half-read: extractValidationRules and collectEvaluatedItems take
// the *length* of prefixItems with no draft gate at all, so an undialected
// document already got a maxItems out of it while every positional subschema
// was dropped. That is what made {"prefixItems":[{"type":"string"}]} accept an
// integer at position 0 and still cap the array's length.
//
// A document that *does* declare a pre-2020 dialect is unaffected: draft-07 has
// no prefixItems and still ignores it, which TestExplicitDraftOverridesDocumentSchemaKeyword
// pins.
func (g *Generator) supportsPrefixItems(s *schema.Schema) bool {
	switch g.draftForSchema(s) {
	case schema.Draft202012, schema.DraftV1, schema.DraftUnknown:
		return true
	default:
		return false
	}
}

// isTupleArray reports whether prefixItems positions the array's elements, so
// the elements are heterogeneous by construction and no single Go element type
// can hold them.
//
// The sibling `items` is what makes this a decision rather than an observation.
// In 2020-12 it governs only the positions *past* the tuple, so
// {"prefixItems":[{"type":"string"},{"type":"integer"}],"items":{"type":"boolean"}}
// is a string, then an integer, then booleans. Reading `items` as the element
// schema typed the field []bool, and the tuple prefix could not decode into it
// at all -- a valid document rejected in the decoder, before any check ran.
// A tuple's Go type is []any, and its positions are checked in Validate.
func (g *Generator) isTupleArray(s *schema.Schema) bool {
	return s != nil && len(s.PrefixItems) > 0 && g.supportsPrefixItems(s)
}

func supportsDependentRequired(draft schema.Draft) bool {
	return draft == schema.Draft201909 || draft == schema.Draft202012 || draft == schema.DraftV1
}

// extractValidationRules extracts validation rules from a property schema.
func extractValidationRules(goFieldName, jsonName string, s *schema.Schema) []ValidationRule {
	var rules []ValidationRule
	if s.MinLength != nil {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "minLength", Value: s.MinLength.Int(),
		})
	}
	if s.MaxLength != nil {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "maxLength", Value: s.MaxLength.Int(),
		})
	}
	if s.Minimum != nil {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "minimum", Value: *s.Minimum,
		})
	}
	if s.Maximum != nil {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "maximum", Value: *s.Maximum,
		})
	}
	if s.Pattern != nil {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "pattern", Value: *s.Pattern,
		})
	}
	if s.MinItems != nil {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "minItems", Value: s.MinItems.Int(),
		})
	}
	if s.MaxItems != nil {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "maxItems", Value: s.MaxItems.Int(),
		})
	}
	// additionalItems: false with tuple-form items → implicit maxItems = tuple length.
	// Draft 2020-12 uses prefixItems + items:false instead.
	if s.MaxItems == nil && s.AdditionalItems != nil && s.AdditionalItems.Bool != nil && !*s.AdditionalItems.Bool {
		if s.Items != nil && len(s.Items.Schemas) > 0 {
			rules = append(rules, ValidationRule{
				FieldName: goFieldName, JSONName: jsonName,
				RuleType: "maxItems", Value: len(s.Items.Schemas),
			})
		}
	}
	// Draft 2020-12: prefixItems + items:false → implicit maxItems = len(prefixItems).
	if s.MaxItems == nil && len(s.PrefixItems) > 0 && s.Items != nil && s.Items.Schema != nil && s.Items.Schema.IsFalseSchema() {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "maxItems", Value: len(s.PrefixItems),
		})
	}
	// unevaluatedItems:false with a fixed tuple and no extending applicators →
	// implicit maxItems = tuple length. Only applied when the schema is a simple
	// self-contained tuple (see unevaluatedItemsImpliesFixedTuple).
	if s.MaxItems == nil && unevaluatedItemsImpliesFixedTuple(s) {
		tupleLen := len(s.PrefixItems)
		if tupleLen == 0 && s.Items != nil {
			tupleLen = len(s.Items.Schemas)
		}
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "maxItems", Value: tupleLen,
		})
	}
	// exclusiveMinimum: can be a number (Draft 2020-12) or a boolean (Draft 4).
	// When boolean and true, the constraint uses the value from Minimum.
	if s.ExclusiveMinimum != nil {
		if s.ExclusiveMinimum.Number != nil {
			rules = append(rules, ValidationRule{
				FieldName: goFieldName, JSONName: jsonName,
				RuleType: "exclusiveMinimum", Value: *s.ExclusiveMinimum.Number,
			})
		} else if s.ExclusiveMinimum.Bool != nil && *s.ExclusiveMinimum.Bool && s.Minimum != nil {
			rules = append(rules, ValidationRule{
				FieldName: goFieldName, JSONName: jsonName,
				RuleType: "exclusiveMinimum", Value: *s.Minimum,
			})
		}
	}
	// exclusiveMaximum: same dual semantics as exclusiveMinimum.
	if s.ExclusiveMaximum != nil {
		if s.ExclusiveMaximum.Number != nil {
			rules = append(rules, ValidationRule{
				FieldName: goFieldName, JSONName: jsonName,
				RuleType: "exclusiveMaximum", Value: *s.ExclusiveMaximum.Number,
			})
		} else if s.ExclusiveMaximum.Bool != nil && *s.ExclusiveMaximum.Bool && s.Maximum != nil {
			rules = append(rules, ValidationRule{
				FieldName: goFieldName, JSONName: jsonName,
				RuleType: "exclusiveMaximum", Value: *s.Maximum,
			})
		}
	}
	if s.MultipleOf != nil {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "multipleOf", Value: *s.MultipleOf,
		})
	}
	if s.UniqueItems != nil && *s.UniqueItems {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "uniqueItems", Value: true,
		})
	}
	// not: {} (empty schema) means "forbidden property" — no value can match.
	//
	// `"enum": []` says the same thing and reaches this position by a different
	// route: generateTypeDef gives an unsatisfiable schema the forbidding type,
	// which a property picks up through a $ref, but an inline property schema is
	// read here instead and came out `any` with no check at all. Both spellings
	// are the empty set, so both are the same rule. The nil test is what
	// separates an absent enum from an empty one; see generateTypeDef.
	//
	// An inner `format` is declined outright rather than read. Whether
	// {"format":"email"} constrains anything is the dialect's answer and not the
	// schema's, and this function has no generator to ask -- read as accept-all
	// it made {"not":{"format":"email"}} forbid every value, including the
	// strings that are not email addresses. Declining costs the check on a
	// dialect where `format` is an annotation, which is a check the two callers
	// that do hold a generator still emit; see acceptsEveryInstance.
	if (s.Not != nil && s.Not.Format == nil && isAcceptAllSchema(s.Not)) || (s.Enum != nil && len(s.Enum) == 0) {
		rules = append(rules, ValidationRule{
			FieldName: goFieldName, JSONName: jsonName,
			RuleType: "forbidden", Value: true,
		})
	}
	// Format validation: for string-typed fields where the format doesn't map to
	// a distinct Go type (e.g. email, uri, uuid), emit a validation rule.
	// For formats that DO map to a distinct type (ipv4/ipv6 → netip.Addr),
	// emit a validation rule to enforce the specific subtype (v4 vs v6).
	//
	// A schema that names no "type" is admitted too. `format` applies to strings
	// and to nothing else, so such a schema is still one whose string instances
	// this generator can judge -- and the rule was skipped there purely because
	// "type" was unwritten, which is why {"format":"ipv4"} asserted nothing
	// anywhere (issue #106). It is this guard that lets the rule reach the
	// wrapper stringAnnotationOnlyDef builds, whose Validate is where the check
	// belongs.
	//
	// Which positions can actually carry the check is decided afterwards from
	// the Go type, by formatRuleShape: a field typed `any` or a wrapper struct
	// drops it again, so admitting it here cannot put a rule somewhere it does
	// not compile.
	if s.Format != nil && (primarySchemaType(s) == "string" || len(s.Type) == 0) {
		if format := *s.Format; FormatCheckableOnString(format) {
			rules = append(rules, ValidationRule{
				FieldName: goFieldName, JSONName: jsonName,
				RuleType: "format", Value: format,
			})
		}
	}
	// The content vocabulary, on exactly the same terms and for the same reason
	// (issue #115). contentEncoding and contentMediaType apply to strings and to
	// nothing else, so a schema stating one and no "type" is still one whose
	// string instances can be judged -- and a number or an object satisfies it
	// trivially, which is why the type is not narrowed and the check has to be
	// conditioned on the instance turning out to be a string.
	//
	// The dialect decides whether the rule survives: only draft 7 asserts these
	// keywords, and contentAssertsFor drops the rule everywhere else. This
	// extractor does not know the draft, so it builds the rule and the two
	// dialect-aware filters -- aliasValidationRules and the field-rule filter in
	// generateStructDef -- decide, which is how the format rule is handled one
	// keyword up.
	if primarySchemaType(s) == "string" || len(s.Type) == 0 {
		if check, ok := contentCheckFor(s); ok {
			rules = append(rules, ValidationRule{
				FieldName: goFieldName, JSONName: jsonName,
				RuleType: "content", Value: check,
			})
		}
	}
	// Const validation: if the schema has a const value and we haven't already
	// handled it through an enum type (e.g., the field is typed as `any`),
	// emit a runtime check that marshals the field value and compares to the
	// expected JSON. This is a safety net for inline properties that didn't
	// get a dedicated enum type. Skip if enum is set (enum type has Validate).
	if (s.Const != nil || s.ConstIsNull) && len(s.Enum) == 0 {
		var constJSON string
		if s.Const != nil {
			b, err := json.Marshal(*s.Const)
			if err == nil {
				constJSON = string(b)
			}
		} else {
			constJSON = "null"
		}
		if constJSON != "" {
			rules = append(rules, ValidationRule{
				FieldName: goFieldName, JSONName: jsonName,
				RuleType: "const", Value: constJSON,
			})
		}
	}
	return rules
}

// allOfConstraintRules returns the field rules an allOf on a property
// contributes beyond the ones the property states directly.
//
// An allOf whose branches describe object shape is flattened by
// generateAllOfDef, which gives the property a named struct built from the
// merged branches. A branch that only bounds a scalar has no shape to flatten:
// the property keeps its plain Go type, generateAllOfDef is never reached, and
// {"type":"string","allOf":[{"minLength":3}]} emits a bare string with no
// length check at all. Folding the branch bounds into the field's own rules
// closes that without changing the field's type.
//
// mergeConstraints is what does the folding, and its keyword set is the reason
// this is safe: it reads only the bounds keywords (the numeric window,
// multipleOf, the length window, pattern, the item and property counts) and
// keeps the tighter of the two, which is exactly what an allOf means. Nothing
// structural is taken from a branch here, so a branch carrying properties,
// required or a $ref contributes only whatever bounds it also states -- less
// than the branch says, never more.
//
// fieldType is the Go type the property resolved to, and rules that would not
// compile against it are dropped. A branch may bound a type the property does
// not have -- allOf: [{"type":"integer","minimum":5}] beside "type":"string" is
// a contradiction no value satisfies -- and emitting `float64(r.A) < 5` for a
// string field would turn a schema that generates today into one that does not.
func allOfConstraintRules(goFieldName, jsonName string, s *schema.Schema, fieldType GoType) []ValidationRule {
	if s == nil || len(s.AllOf) == 0 {
		return nil
	}
	// A value copy: mergeConstraints only reassigns the bound fields, and every
	// tighter* helper returns one of its arguments rather than mutating it, so
	// the property schema itself is left untouched.
	merged := *s
	folded := false
	for _, branch := range s.AllOf {
		if branch == nil || branch.IsBooleanSchema() {
			continue
		}
		mergeConstraints(&merged, branch)
		folded = true
	}
	if !folded {
		return nil
	}

	// Only what the merge added or tightened: the property's own keywords have
	// already produced their rules, and repeating them would emit the same check
	// twice.
	base := extractValidationRules(goFieldName, jsonName, s)
	baseByType := make(map[string]any, len(base))
	for _, r := range base {
		baseByType[r.RuleType] = r.Value
	}
	var out []ValidationRule
	for _, r := range extractValidationRules(goFieldName, jsonName, &merged) {
		if had, ok := baseByType[r.RuleType]; ok && had == r.Value {
			continue
		}
		if !ruleCompilesForType(fieldType, r.RuleType) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// ruleCompilesForType reports whether a field-level rule of this type is
// expressed against a Go value the emitted check can accept: the length and
// pattern checks need a string, the numeric window a number, the item checks a
// slice.
//
// Unknown rule types answer false. This gate guards rules synthesized from a
// branch rather than written on the property, so a keyword it has not been
// taught about is better dropped -- under-enforcing a contradictory schema --
// than emitted against a field it does not typecheck against.
func ruleCompilesForType(t GoType, ruleType string) bool {
	if t == nil {
		return false
	}
	if pt, ok := t.(*PointerType); ok {
		t = pt.Inner
	}
	switch ruleType {
	case "minLength", "maxLength", "pattern":
		prim, ok := t.(*PrimitiveType)
		return ok && prim.Name == "string"
	case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf":
		prim, ok := t.(*PrimitiveType)
		return ok && (prim.Name == "int64" || prim.Name == "float64")
	case "minItems", "maxItems", "uniqueItems":
		_, ok := t.(*ArrayType)
		return ok
	}
	return false
}

// jsonKindForGoType names the JSON type every value of this Go type carries, or
// "" when the type admits more than one and nothing here can tell them apart.
//
// It is the bridge between a Go type and the JSON Schema rule that "a keyword
// applies to instances of one type and says nothing about the rest". A field
// typed int64 only ever holds a JSON integer, so a keyword about strings has
// nothing to say about it. `any`, json.RawMessage and every named type answer
// "" -- they can hold anything, or their contents are the business of their own
// Validate -- and a "" answer makes every judgement below fall back to leaving
// the rule alone.
func jsonKindForGoType(t GoType) string {
	for {
		pt, ok := t.(*PointerType)
		if !ok {
			break
		}
		t = pt.Inner
	}
	switch v := t.(type) {
	case *PrimitiveType:
		switch v.Name {
		case "string":
			return "string"
		case "int64":
			return "integer"
		case "float64":
			return "number"
		case "bool":
			return "boolean"
		}
	case *ArrayType:
		return "array"
	case *MapType:
		return "object"
	}
	return ""
}

// ruleKeywordJSONKinds records the JSON types each validation keyword speaks
// about. A keyword is *vacuously satisfied* by an instance of any other type --
// {"minLength":3} accepts 20, and {"minimum":10} accepts "ab" -- which is a
// general rule of JSON Schema and not a property of any one keyword.
//
// Only the keywords whose rule types can be emitted as a value check are listed.
// A rule absent from the map is one that either applies to every type (const,
// enum, forbidden) or is already gated on the instance type where it is built
// (format is only produced for a string-typed schema), and is left alone.
var ruleKeywordJSONKinds = map[string]map[string]bool{
	"minLength": {"string": true},
	"maxLength": {"string": true},
	"pattern":   {"string": true},
	"content":   {"string": true},

	"minimum":          {"integer": true, "number": true},
	"maximum":          {"integer": true, "number": true},
	"exclusiveMinimum": {"integer": true, "number": true},
	"exclusiveMaximum": {"integer": true, "number": true},
	"multipleOf":       {"integer": true, "number": true},

	"minItems":    {"array": true},
	"maxItems":    {"array": true},
	"uniqueItems": {"array": true},
}

// ruleVacuousForType reports whether a rule of this type is satisfied by every
// value the Go type can hold, because the keyword speaks about some other JSON
// type entirely.
//
// Emitting such a rule is never merely redundant. `utf8.RuneCountInString` over
// an int64 converts the number to the single rune with that code point and
// measures that, so {"type":"integer","minLength":3} rejects every integer --
// and inside a oneOf the same reading flips the branch count, turning an invalid
// document into an accepted one and a valid one into a rejection.
//
// The answer is false wherever the Go type does not pin down a single JSON type,
// so a value that could still be of the keyword's own type keeps its check.
func ruleVacuousForType(t GoType, ruleType string) bool {
	kinds, judged := ruleKeywordJSONKinds[ruleType]
	if !judged {
		return false
	}
	kind := jsonKindForGoType(t)
	if kind == "" {
		return false
	}
	return !kinds[kind]
}

// isAcceptAllSchema returns true if the schema matches all values (empty schema or boolean true).
func isAcceptAllSchema(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	// A boolean schema carries its answer in a field with no JSON key and no
	// keyword of its own, so every test below passes for it and `false` -- the
	// schema that matches no value at all -- read as matching every one. Through
	// the `not` callers that is the negation turned inside out: {"not":false}
	// admits everything and was refused outright.
	if s.IsBooleanSchema() {
		return s.IsTrueSchema()
	}
	// An empty schema (no constraints) matches everything.
	// Must check ALL structural and validation keywords to avoid false positives.
	//
	// The enum test is `s.Enum == nil` and not `len(s.Enum) == 0`, because the
	// empty list is the one enum that is not an absent enum: it admits nothing,
	// which is the opposite of what this predicate reports. Read through the one
	// caller that matters, {"not":{"enum":[]}} is a `not` over the empty set and
	// so admits *every* value -- and len() said "accepts all" of the inner
	// schema, which turned into a forbidden-property rule and refused every
	// document the schema permits. See emptyEnumSchema.
	return len(s.Type) == 0 && len(s.Properties) == 0 && s.Not == nil &&
		len(s.AllOf) == 0 && len(s.AnyOf) == 0 && len(s.OneOf) == 0 &&
		s.Minimum == nil && s.Maximum == nil && s.MinLength == nil && s.MaxLength == nil &&
		s.MinItems == nil && s.MaxItems == nil && s.Pattern == nil && s.Enum == nil &&
		s.Ref == "" && s.DynamicRef == "" && s.RecursiveRef == "" &&
		len(s.Required) == 0 && s.AdditionalProperties == nil &&
		s.Items == nil && len(s.PrefixItems) == 0 && s.AdditionalItems == nil &&
		s.Contains == nil && s.PropertyNames == nil &&
		s.MinProperties == nil && s.MaxProperties == nil &&
		s.MultipleOf == nil && s.ExclusiveMinimum == nil && s.ExclusiveMaximum == nil &&
		s.UniqueItems == nil && s.If == nil &&
		len(s.DependentRequired) == 0 && len(s.DependentSchemas) == 0 &&
		s.UnevaluatedProperties == nil && s.UnevaluatedItems == nil &&
		s.Const == nil && !s.ConstIsNull && len(s.PatternProperties) == 0
}

// extractNotSchemaDef returns a *NotSchemaDef if the schema is a not-only
// schema that we can statically validate. Returns nil for schemas that have
// other constraints or use complex not sub-schemas we can't handle.
func (g *Generator) extractNotSchemaDef(name string, s *schema.Schema) *NotSchemaDef {
	if s.Not == nil {
		return nil
	}
	// Only handle "not" as the sole constraint keyword. If the schema also has
	// type, properties, items, allOf, etc., it should be handled by other code paths.
	if len(s.Type) > 0 || hasProperties(s) || s.Items != nil || len(s.PrefixItems) > 0 ||
		len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 ||
		s.If != nil || s.Ref != "" || s.DynamicRef != "" || s.RecursiveRef != "" ||
		len(s.Required) > 0 || s.AdditionalProperties != nil ||
		s.Minimum != nil || s.Maximum != nil || s.MinLength != nil || s.MaxLength != nil ||
		s.Pattern != nil || s.MinItems != nil || s.MaxItems != nil ||
		s.MinProperties != nil || s.MaxProperties != nil ||
		s.Contains != nil || s.PropertyNames != nil ||
		s.UnevaluatedProperties != nil || s.UnevaluatedItems != nil ||
		len(s.DependentRequired) > 0 || len(s.DependentSchemas) > 0 {
		return nil
	}

	not := s.Not

	// not: false (boolean false schema) → allow everything, no validation needed.
	if not.IsFalseSchema() {
		return nil
	}

	// not: {} (empty schema) or not: true → forbid everything.
	//
	// acceptsEveryInstance rather than isAcceptAllSchema, because `format` is the
	// one keyword whose reading is the generator's and not the schema's: where
	// the dialect asserts it, {"not":{"format":"email"}} forbids email addresses
	// alone, and reading the inner schema as accept-all refused every string
	// there is and every number besides. See acceptsEveryInstance.
	if g.acceptsEveryInstance(not) || not.IsTrueSchema() {
		return &NotSchemaDef{
			Name:        name,
			Description: s.Description,
			IsForbidden: true,
		}
	}

	// not: {not: {}} → double negation of accept-all = accept-all.
	// No validation needed.
	if not.Not != nil && g.acceptsEveryInstance(not.Not) {
		return nil
	}

	// not: {type: X} or not: {type: [X, Y]} → reject values of those types.
	if len(not.Type) > 0 && isTypeOnlySchema(not) {
		return &NotSchemaDef{
			Name:        name,
			Description: s.Description,
			NotTypes:    not.Type,
		}
	}

	// Draft 3 disallow arrays normalize to not:{anyOf:[...]}. Handle branches
	// with simple type constraints and object property type checks.
	if len(not.AnyOf) > 0 {
		branches := extractNotSchemaBranches(not.AnyOf)
		if len(branches) == len(not.AnyOf) {
			return &NotSchemaDef{
				Name:        name,
				Description: s.Description,
				NotBranches: branches,
			}
		}
	}

	// Complex not sub-schema — can't handle statically.
	return nil
}

func extractNotSchemaBranches(subs []*schema.Schema) []NotSchemaBranch {
	branches := make([]NotSchemaBranch, 0, len(subs))
	for _, sub := range subs {
		if sub == nil || sub.IsBooleanSchema() {
			return nil
		}
		if len(sub.Type) > 0 && isTypeOnlySchema(sub) {
			branches = append(branches, NotSchemaBranch{Types: append([]string(nil), sub.Type...)})
			continue
		}
		if len(sub.Type) == 1 && hasSimpleNotBranchValidations(sub) && isSimpleNotBranchSchema(sub) {
			branches = append(branches, NotSchemaBranch{
				Types:       append([]string(nil), sub.Type...),
				Validations: extractSimpleNotBranchValidations(sub),
			})
			continue
		}
		if len(sub.Properties) > 0 && len(sub.Type) <= 1 && (len(sub.Type) == 0 || sub.Type[0] == "object") {
			branch := NotSchemaBranch{}
			for _, name := range sortedKeys(sub.Properties) {
				prop := sub.Properties[name]
				if prop == nil || len(prop.Type) != 1 || !isTypeOnlySchema(prop) {
					return nil
				}
				branch.Properties = append(branch.Properties, NotPropertyBranch{Name: name, JSONType: prop.Type[0]})
			}
			branches = append(branches, branch)
			continue
		}
		return nil
	}
	return branches
}

func hasSimpleNotBranchValidations(s *schema.Schema) bool {
	return s.Minimum != nil || s.Maximum != nil || s.ExclusiveMinimum != nil || s.ExclusiveMaximum != nil ||
		s.MultipleOf != nil || s.MinLength != nil || s.MaxLength != nil || s.Pattern != nil ||
		s.MinItems != nil || s.MaxItems != nil
}

func extractSimpleNotBranchValidations(s *schema.Schema) []ValidationRule {
	rules := extractValidationRules("", "", s)
	out := make([]ValidationRule, 0, len(rules))
	for _, rule := range rules {
		switch rule.RuleType {
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
			"minLength", "maxLength", "pattern", "minItems", "maxItems":
			out = append(out, rule)
		}
	}
	return out
}

func isSimpleNotBranchSchema(s *schema.Schema) bool {
	return s.Ref == "" && s.DynamicRef == "" && s.RecursiveRef == "" &&
		len(s.AllOf) == 0 && len(s.AnyOf) == 0 && len(s.OneOf) == 0 && s.Not == nil &&
		s.If == nil && s.Then == nil && s.Else == nil &&
		len(s.Properties) == 0 && len(s.PatternProperties) == 0 && s.AdditionalProperties == nil &&
		s.Items == nil && len(s.PrefixItems) == 0 && s.AdditionalItems == nil && s.Contains == nil &&
		s.Enum == nil && s.Const == nil && !s.ConstIsNull && s.Format == nil &&
		s.UniqueItems == nil && s.MinProperties == nil && s.MaxProperties == nil &&
		len(s.Definitions) == 0 && len(s.Defs) == 0 &&
		s.PropertyNames == nil && s.UnevaluatedItems == nil && s.UnevaluatedProperties == nil &&
		s.DependentSchemas == nil && s.DependentRequired == nil && len(s.Dependencies) == 0
}

// isTypeOnlySchema returns true if the schema has only a "type" constraint and
// nothing else (used for not:{type:X} detection).
// The enum and const tests are what keep a schema that names a type *and*
// enumerates its values from being read as if it named only the type. Every
// caller is a `not`, where dropping a conjunct from the inner schema widens the
// negation instead of narrowing it: {"not":{"type":"string","enum":[]}} is a
// `not` over a schema no string satisfies and so admits every string, and
// {"not":{"type":"string","const":"x"}} forbids "x" alone -- both were read as
// {"not":{"type":"string"}} and refused every string there is. `const` was not
// tested at all, and `enum` was tested with len(), which cannot tell an absent
// enum from the empty one; see emptyEnumSchema.
func isTypeOnlySchema(s *schema.Schema) bool {
	return len(s.Properties) == 0 && s.Not == nil &&
		len(s.AllOf) == 0 && len(s.AnyOf) == 0 && len(s.OneOf) == 0 &&
		s.Minimum == nil && s.Maximum == nil && s.MinLength == nil && s.MaxLength == nil &&
		s.MinItems == nil && s.MaxItems == nil && s.Pattern == nil &&
		s.Enum == nil && s.Const == nil && !s.ConstIsNull &&
		s.Ref == "" && s.DynamicRef == "" && s.RecursiveRef == "" &&
		len(s.Required) == 0 && s.AdditionalProperties == nil &&
		s.Items == nil && len(s.PrefixItems) == 0 &&
		s.Contains == nil && s.PropertyNames == nil &&
		s.MinProperties == nil && s.MaxProperties == nil &&
		s.If == nil && s.UnevaluatedProperties == nil && s.UnevaluatedItems == nil &&
		len(s.DependentRequired) == 0 && len(s.DependentSchemas) == 0 &&
		s.MultipleOf == nil && s.ExclusiveMinimum == nil && s.ExclusiveMaximum == nil &&
		s.UniqueItems == nil
}

// extractTypeOnlySchemaDef returns a *TypeOnlySchemaDef if the schema has an
// explicit "type" constraint with types that don't map to a single Go type
// (multi-type arrays or null-only) and no other structural constraints.
// Returns nil for schemas that should be handled by other code paths.
func (g *Generator) extractTypeOnlySchemaDef(name string, s *schema.Schema) *TypeOnlySchemaDef {
	if len(s.Type) == 0 && len(s.TypeSchemas) == 0 {
		return nil
	}
	// Check if the type maps to a single Go type already handled elsewhere.
	// primarySchemaType returns non-empty for single non-null types and for "null".
	//
	// ["string","null"] beside a format is the exception: it does map to a
	// single Go type, and that type is the problem -- `type X *string` cannot
	// carry a Validate, so the format is lost exactly where the schema is given
	// a name. See nullableFormatUnion.
	pt := primarySchemaType(s)
	if pt != "" && pt != "null" && len(s.TypeSchemas) == 0 && !g.nullableFormatUnion(s) {
		return nil // Already handled by primitive type / object / array paths.
	}
	// At this point: either multi-type (pt == "") or null-only (pt == "null").

	// Only handle schemas where "type" is the sole constraint keyword.
	if s.Items != nil || hasConstraintsBesidesItems(s) {
		return nil
	}

	branches, allowed := g.typeUnionBranches(s, name)
	return &TypeOnlySchemaDef{
		Name:         name,
		Description:  s.Description,
		AllowedTypes: allowed,
		TypeBranches: branches,
	}
}

// representableKeywords names the keywords a caller can carry itself, and so is
// willing to see alongside the one it is handling.
type representableKeywords struct {
	items bool
	anyOf bool
}

// hasUnrepresentedConstraints reports whether the schema states a structural or
// validation keyword the caller has not said it can carry. The raw-value
// wrappers built from "type" and "anyOf" express only their own alternatives,
// so a schema carrying anything else must be handled by another path rather
// than silently losing the constraint.
func hasUnrepresentedConstraints(s *schema.Schema, allow representableKeywords) bool {
	if hasProperties(s) || len(s.PrefixItems) > 0 ||
		len(s.AllOf) > 0 || len(s.OneOf) > 0 ||
		s.Not != nil || s.If != nil || s.Ref != "" || s.DynamicRef != "" || s.RecursiveRef != "" ||
		len(s.Required) > 0 || s.AdditionalProperties != nil ||
		s.Minimum != nil || s.Maximum != nil || s.MinLength != nil || s.MaxLength != nil ||
		s.Pattern != nil || s.MinItems != nil || s.MaxItems != nil ||
		s.MinProperties != nil || s.MaxProperties != nil ||
		s.Contains != nil || s.PropertyNames != nil ||
		s.UnevaluatedProperties != nil || s.UnevaluatedItems != nil ||
		len(s.DependentRequired) > 0 || len(s.DependentSchemas) > 0 ||
		s.MultipleOf != nil || s.ExclusiveMinimum != nil || s.ExclusiveMaximum != nil ||
		s.UniqueItems != nil {
		return true
	}
	if !allow.items && s.Items != nil {
		return true
	}
	if !allow.anyOf && len(s.AnyOf) > 0 {
		return true
	}
	return false
}

// hasConstraintsBesidesItems reports whether the schema carries any structural
// or validation keyword other than "type" and "items".
func hasConstraintsBesidesItems(s *schema.Schema) bool {
	return hasUnrepresentedConstraints(s, representableKeywords{items: true})
}

// anyOfIsSoleConstraint reports whether "anyOf" is the only thing the schema
// states, so a wrapper that checks its alternatives and nothing else says
// everything the schema says.
func anyOfIsSoleConstraint(s *schema.Schema) bool {
	if len(s.AnyOf) < 2 {
		return false
	}
	if len(s.Type) > 0 || len(s.TypeSchemas) > 0 || len(s.Enum) > 0 || s.Const != nil || s.ConstIsNull {
		return false
	}
	return !hasUnrepresentedConstraints(s, representableKeywords{anyOf: true})
}

// delegatedBranchType materializes sub as a named generated type so a union
// wrapper can delegate a branch to it. A $ref alternative usually resolves to
// an existing named type; an inline one (a constrained array, say) resolves to
// an unnamed Go type and has to be given a name of its own. Reports false when
// neither produces a name, which is the signal to leave the schema to another
// path rather than emit a branch that checks nothing.
func (g *Generator) delegatedBranchType(sub *schema.Schema, contextName string) (string, bool) {
	if sub == nil || sub.IsBooleanSchema() {
		return "", false
	}
	if name := namedTypeName(g.resolveType(sub, contextName)); name != "" {
		return name, true
	}
	name, _ := g.materializeNamed(sub, contextName)
	if name == "" || !g.generated[name] {
		return "", false
	}
	return name, true
}

// anyOfUnionType represents an anyOf whose alternatives share no Go type as a
// raw-value wrapper: the value is kept verbatim and accepted when any one of
// the generated alternative types accepts it. Without this the property falls
// back to `any` and the alternatives constrain nothing at all.
func (g *Generator) anyOfUnionType(s *schema.Schema, contextName string) (GoType, bool) {
	if !g.validationKeywordsEnabled() || !anyOfIsSoleConstraint(s) {
		return nil, false
	}
	// Alternatives carrying properties are merged into a struct by the paths
	// below; taking them over here would throw those fields away.
	if g.anyOfHasProperties(s) {
		return nil, false
	}
	if g.generated[contextName] {
		return &NamedType{Name: contextName}, true
	}
	branches := make([]TypeSchemaBranch, 0, len(s.AnyOf))
	for i, variant := range s.AnyOf {
		name, ok := g.delegatedBranchType(variant, fmt.Sprintf("%sAlternative%d", contextName, i))
		if !ok {
			return nil, false
		}
		branches = append(branches, TypeSchemaBranch{TypeName: name})
	}
	g.generated[contextName] = true
	g.output.TypeDefs = append(g.output.TypeDefs, &TypeOnlySchemaDef{
		Name:         contextName,
		Description:  s.Description,
		TypeBranches: branches,
	})
	return &NamedType{Name: contextName}, true
}

// multiTypeUnionType represents a "type" listing several JSON types that no
// single Go type spans -- ["string","array"], say -- as a raw-value wrapper
// with one delegated branch per type.
//
// The sibling keywords are what make this necessary. A bare multi-type schema
// constrains nothing beyond the type, and the cheap AllowedTypes check already
// covers it; but "items" or "uniqueItems" alongside the union apply to whichever
// alternative they are meaningful for, and collapsing the property to the one
// type those keywords hint at drops the other alternative entirely. Each branch
// is generated from the schema with "type" narrowed to a single value, so the
// siblings are carried by the normal machinery rather than re-implemented here.
func (g *Generator) multiTypeUnionType(s *schema.Schema, contextName string) (GoType, bool) {
	if !g.validationKeywordsEnabled() || len(s.TypeSchemas) > 0 {
		return nil, false
	}
	nonNull := 0
	for _, t := range s.Type {
		if t != "null" {
			nonNull++
		}
	}
	if nonNull < 2 {
		return nil, false
	}
	// An applicator or a value constraint is not scoped to a type, so it cannot
	// be split across the branches -- leave those schemas to the paths that
	// already handle them.
	if hasNonTypeScopedConstraints(s) {
		return nil, false
	}
	// Without type-scoped siblings the alternatives carry no constraint of
	// their own, and the plain type check the wrapper already emits says
	// everything there is to say. Leave those where they are.
	if !hasTypeScopedConstraints(s) {
		return nil, false
	}
	return g.typeUnionWrapper(s, contextName)
}

// typeUnionWrapper materializes s as the raw-value wrapper a "type" union
// resolves to: the value is kept as JSON, each alternative that needs one gets a
// generated type of its own, and the rest are checked inline against the JSON
// type. Shared by every caller that has already decided the schema belongs here.
func (g *Generator) typeUnionWrapper(s *schema.Schema, contextName string) (GoType, bool) {
	if g.generated[contextName] {
		return &NamedType{Name: contextName}, true
	}
	branches, allowed := g.typeUnionBranches(s, contextName)
	g.generated[contextName] = true
	g.output.TypeDefs = append(g.output.TypeDefs, &TypeOnlySchemaDef{
		Name:         contextName,
		Description:  s.Description,
		AllowedTypes: allowed,
		TypeBranches: branches,
	})
	return &NamedType{Name: contextName}, true
}

// nullableFormatUnion reports whether s is the nullable spelling of a formatted
// string -- {"type":["string","null"],"format":"ipv4"} -- for a format this
// generator acts on.
//
// The pointer such a schema used to resolve to said less than every other
// spelling of the same idea. ["string","number"] beside a format already becomes
// a raw-value wrapper with one generated branch per alternative, and that branch
// is the ordinary {"type":"string","format":X} type, which asserts the format
// correctly. Adding "null" instead of "number" dropped all of it: the property
// became *string, losing the time.Time or netip.Addr the non-nullable spelling
// gets, and the named form became `type NullableStamp *string` -- a pointer
// underlying type, on which Go forbids methods, so the type carried no Validate
// at all and the format was enforced nowhere (issue #104). Routing it to the
// wrapper the sibling spelling already uses is what makes the two agree; nothing
// here is new machinery, only a spelling admitted to it.
//
// Deliberately not widened to every nullable schema carrying a type-scoped
// keyword. {"type":["string","null"],"minLength":3} has the same hole in its
// named form, but its *string is the right Go type for the value and its
// field-level check compiles and runs, so turning it into a wrapper would change
// the generated API of a great many documents to fix one position. A format is
// different in kind: it is the keyword that decides the Go type in the
// non-nullable spelling, so the nullable one is not merely missing a check but
// answering with a different type.
func (g *Generator) nullableFormatUnion(s *schema.Schema) bool {
	if s == nil || !g.validationKeywordsEnabled() || len(s.TypeSchemas) > 0 {
		return false
	}
	// Exactly ["string","null"]: primarySchemaType answers "" for two non-null
	// alternatives, which multiTypeUnionType already handles.
	if !isNullable(s) || primarySchemaType(s) != "string" {
		return false
	}
	if s.Format == nil || !FormatCheckableOnString(*s.Format) {
		return false
	}
	// An applicator, an enum or a const is not scoped to a type and so cannot be
	// divided among the branches; those schemas keep the paths that carry them.
	return !hasNonTypeScopedConstraints(s)
}

// nullableFormatUnionType materializes the wrapper nullableFormatUnion
// describes, for a position that resolves a Go type rather than declaring one.
func (g *Generator) nullableFormatUnionType(s *schema.Schema, contextName string) (GoType, bool) {
	if !g.nullableFormatUnion(s) {
		return nil, false
	}
	return g.typeUnionWrapper(s, contextName)
}

// stringAnnotationOnlySchema reports whether a keyword that speaks about a
// string and nothing else is what the schema is about, with no "type" beside it.
// Two vocabularies qualify: "format", and the content keywords
// contentEncoding/contentMediaType/contentSchema.
//
// Such a schema resolved to `any`, and Go forbids methods on `any`, so the
// keyword was enforced in no position at all (issue #106 for format, issue #115
// for content). The type it deserves is not "a string": both vocabularies apply
// only to strings, so a number, an object or a null satisfies {"format":"ipv4"}
// and {"contentEncoding":"base64"} trivially, and narrowing the Go type would
// reject documents the schema admits. What it deserves is "anything, but a
// string must be a valid IPv4 address" -- which is exactly the wrapper an
// inferred type already produces, whose Validate returns early for a value that
// turned out to be of some other type.
//
// The two vocabularies are one arm rather than two because they are one
// question: they are the keywords that describe a string without saying the
// value has to be one. A schema stating both gets one wrapper carrying both
// checks, which is what an arm per vocabulary could not produce.
//
// A length bound or a pattern is admitted alongside, because they are about the
// same string the format is and the wrapper carries all three. That spelling is
// not merely unenforced today but actively wrong: {"format":"ipv4","minLength":9}
// has its type inferred from the bound, so the property became a *string and a
// number -- which the schema permits, both keywords being vacuous for it -- was
// refused at decode time. The wrapper answers the format, the bound and the
// number together.
//
// Everything else is refused, and exactly rather than approximately:
// materializing under a schema some other arm would answer differently does not
// leave that answer alone, it replaces it. So there must be no "type", no
// reference, no enum or const, no applicator, and nothing that would make the
// schema about some *other* JSON type -- which is asked of
// inferTypeFromConstraints itself rather than by listing keywords, so the two
// cannot drift apart. {"format":"ipv4","minimum":3} is about numbers by that
// reading and keeps the arm that types it as one.
func (g *Generator) stringAnnotationOnlySchema(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	statesFormat := s.Format != nil && FormatCheckableOnString(*s.Format)
	if !statesFormat && !statesContentVocabulary(s) {
		return false
	}
	// The validation vocabulary decides whether the *validation* keywords bind,
	// and neither of the two this wrapper exists for is one of them: from
	// 2019-09 "format" is its own vocabulary and the content keywords are
	// another. A metaschema that declares format-assertion and omits validation
	// -- which is exactly what the suite's optional/format-assertion.json points
	// its schemas at -- is asking for the format to be checked, and reading its
	// silence about validation as silence about format left {"format":"ipv4"}
	// resolving to a bare `any` with no Validate at all under the one metaschema
	// written to demand one.
	//
	// The gate still stands for everything else the wrapper would carry: a
	// minLength beside the format is a validation keyword, and withoutValidationRules
	// takes it back out when the vocabulary does not bind.
	if !g.validationKeywordsEnabled() && !(statesFormat && g.formatAssertsFor(s)) {
		return false
	}
	if len(s.Type) > 0 || len(s.TypeSchemas) > 0 {
		return false
	}
	if hasNonTypeScopedConstraints(s) {
		return false
	}
	// The content keywords are stripped alongside "format" for the same reason:
	// inferTypeFromConstraints is being asked what *other* JSON type the schema
	// is about, and a keyword that only ever speaks about strings is not an
	// answer to that. (It does not read them today, so the assignment is
	// belt-and-braces against a later arm that does.)
	stripped := *s
	stripped.Format = nil
	stripped.ContentEncoding, stripped.ContentMediaType, stripped.ContentSchema = "", "", nil
	switch g.inferTypeFromConstraints(&stripped) {
	case "", "string":
		return true
	default:
		return false
	}
}

// stringAnnotationOnlyDef builds the wrapper stringAnnotationOnlySchema
// describes: a value held as a Go string when the instance is one, kept verbatim
// when it is not, and a Validate that asserts the format and the content
// keywords only in the first case.
//
// InferredGoType is written out rather than taken from resolveType, and it is
// always a plain string. netip.Addr would look like the closer type, and it is
// the one the declared spelling gets -- but here it would turn the fix into a
// silent acceptance: a malformed address fails netip.Addr's decoder, the wrapper
// files it under "not a string" and Validate then passes it, which is the exact
// hole this is meant to close. Keeping the JSON string means the value always
// decodes and the format check is what judges it.
//
// The rules come from the same extractor every other string position uses, so a
// length bound or a pattern stated beside the format is carried too, and by the
// same code that carries it when the schema is written with "type":"string" --
// including the dialect's format and content postures, so under an
// annotation-only dialect the wrapper is built and the check is not written into
// it.
//
// The wrapper is built either way, and the type is the same either way. That is
// deliberate: --format-assertion and the dialect decide what Validate does, not
// what the generated API is, and a flag that silently retyped a field would be a
// worse thing to hand a caller than one that silently checked less. It is also
// what makes the annotation-only content dialects worth claiming at all: the
// schema still constrains nothing there, but the caller gets a type with a
// Validate to call instead of a bare `any` that cannot have one.
func (g *Generator) stringAnnotationOnlyDef(name string, s *schema.Schema) *InferredAliasDef {
	if !g.stringAnnotationOnlySchema(s) {
		return nil
	}
	rules := g.aliasValidationRules(s, &PrimitiveType{Name: "string"})
	if !g.validationKeywordsEnabled() {
		rules = withoutValidationRules(rules)
	}
	return &InferredAliasDef{
		Name:             name,
		Description:      s.Description,
		InferredGoType:   &PrimitiveType{Name: "string"},
		InferredJSONType: "string",
		Validations:      rules,
	}
}

// withoutValidationRules keeps only the rules that come from a vocabulary other
// than the validation one -- "format" and the content keywords, each of which
// this generator gates separately and neither of which the validation
// vocabulary speaks for.
//
// It is reached only when a metaschema declared its vocabularies and left
// validation out, which is the one case where the wrapper can be built for a
// format the metaschema does assert while a minLength written beside it asserts
// nothing at all.
func withoutValidationRules(rules []ValidationRule) []ValidationRule {
	kept := rules[:0:0]
	for _, r := range rules {
		if r.RuleType == "format" || r.RuleType == "content" {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// declaredFormatStringSchema reports whether s is {"type":"string"} with a
// format the dialect asserts, and nothing else that would route it elsewhere.
//
// It exists for the two positions that judge a value without giving it a type of
// its own: a tuple slot, which falls back to checking the JSON type, and a oneOf
// branch, which falls back to a bare Go primitive. Both answered "string" for
// this schema -- true, and silent about the format -- so the assertion was lost
// in exactly the positions the sibling spellings had already been fixed in.
// Naming the position gives the check somewhere to live, which is the same
// answer a $ref to the identical definition has always produced.
//
// Gated on the posture: under an annotating dialect there is no check to place,
// and materializing a type to hold nothing would change the generated API for
// no reason.
func (g *Generator) declaredFormatStringSchema(s *schema.Schema) bool {
	if s == nil || !g.validationKeywordsEnabled() {
		return false
	}
	if s.Format == nil || !FormatCheckableOnString(*s.Format) {
		return false
	}
	if len(s.Type) != 1 || s.Type[0] != "string" || len(s.TypeSchemas) > 0 {
		return false
	}
	if !g.formatAssertsFor(s) {
		return false
	}
	// Anything that decides the value some other way keeps the arm that decides
	// it: an enum or const fixes the value, a reference names another type, an
	// applicator is judged by its branches.
	return !hasNonTypeScopedConstraints(s)
}

// stringAnnotationOnlyWrapperType materializes stringAnnotationOnlyDef for a
// position that resolves a Go type -- a property, an array element, a map value,
// a tuple slot -- so the check lives in the same one type wherever the schema is
// written. Without it the wrapper existed only where the schema was given a
// name, which is the "fixed in one position, not its sibling" shape these
// wrappers exist to avoid.
func (g *Generator) stringAnnotationOnlyWrapperType(s *schema.Schema, contextName string) (GoType, bool) {
	if g.generated[contextName] {
		if g.stringAnnotationOnlySchema(s) {
			return &NamedType{Name: contextName}, true
		}
		return nil, false
	}
	def := g.stringAnnotationOnlyDef(contextName, s)
	if def == nil {
		return nil, false
	}
	g.generated[contextName] = true
	g.output.TypeDefs = append(g.output.TypeDefs, def)
	return &NamedType{Name: contextName}, true
}

// nullOnlyWrapperType represents a {"type":"null"} property as the same
// raw-value wrapper the schema already becomes when it is named -- a $defs entry
// or a document root, via extractTypeOnlySchemaDef -- so an inline occurrence is
// not the one spelling that means less than the others.
//
// The pointer it would otherwise get says neither of the two things the schema
// needs said. *any accepts every JSON value, so nothing rejects a property that
// is not null; and encoding/json leaves the pointer nil for both a present null
// and an absent key, so no tag can emit the first without inventing the second.
// The wrapper keeps the bytes it was handed, which answers both: its Validate
// admits nothing but null, and an absent property leaves it empty, which is its
// zero value and so is dropped by the ",omitzero" tag the field carries.
//
// Only when "type" is the whole schema. A null-only schema stating anything else
// is left to the paths that carry those keywords, since the wrapper expresses
// the type constraint and nothing more.
func (g *Generator) nullOnlyWrapperType(s *schema.Schema, contextName string) (GoType, bool) {
	if !isNullOnly(s) {
		return nil, false
	}
	def := g.extractTypeOnlySchemaDef(contextName, s)
	if def == nil {
		return nil, false
	}
	if g.generated[contextName] {
		return &NamedType{Name: contextName}, true
	}
	g.generated[contextName] = true
	g.output.TypeDefs = append(g.output.TypeDefs, def)
	return &NamedType{Name: contextName}, true
}

// hasNonTypeScopedConstraints reports whether the schema states something that
// applies whatever the value's type is. Those cannot be divided among per-type
// branches: an applicator would have to be duplicated into every one, and an
// enum or const already decides the type by itself.
func hasNonTypeScopedConstraints(s *schema.Schema) bool {
	return len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 ||
		s.Not != nil || s.If != nil ||
		s.Ref != "" || s.DynamicRef != "" || s.RecursiveRef != "" ||
		len(s.Enum) > 0 || s.Const != nil || s.ConstIsNull
}

// hasTypeScopedConstraints reports whether the schema states a keyword that
// constrains values of one particular type.
func hasTypeScopedConstraints(s *schema.Schema) bool {
	return hasProperties(s) || len(s.PatternProperties) > 0 || s.AdditionalProperties != nil ||
		len(s.Required) > 0 || s.MinProperties != nil || s.MaxProperties != nil ||
		s.PropertyNames != nil || s.UnevaluatedProperties != nil ||
		len(s.DependentSchemas) > 0 || len(s.DependentRequired) > 0 || len(s.Dependencies) > 0 ||
		s.Items != nil || len(s.PrefixItems) > 0 || s.AdditionalItems != nil ||
		s.MinItems != nil || s.MaxItems != nil || s.UniqueItems != nil ||
		s.Contains != nil || s.MinContains != nil || s.MaxContains != nil || s.UnevaluatedItems != nil ||
		s.MinLength != nil || s.MaxLength != nil || s.Pattern != nil || s.Format != nil ||
		s.Minimum != nil || s.Maximum != nil ||
		s.ExclusiveMinimum != nil || s.ExclusiveMaximum != nil ||
		s.MultipleOf != nil || s.DivisibleBy != nil
}

// narrowedToType returns a copy of s whose "type" is the single named type,
// with the keywords scoped to the other types removed.
//
// JSON Schema scopes its assertions by type: uniqueItems says nothing about a
// string, minLength nothing about an array. A branch generated for one
// alternative must therefore not inherit the others' constraints -- keeping
// them makes the generator apply, say, a uniqueItems check to a Go string, so
// that "integer" is rejected for using the letter e twice.
func narrowedToType(s *schema.Schema, t string) *schema.Schema {
	c := *s
	c.Type = []string{t}
	c.TypeSchemas = nil
	if t != "object" {
		c.Properties, c.PatternProperties, c.AdditionalProperties = nil, nil, nil
		c.Required = nil
		c.MinProperties, c.MaxProperties = nil, nil
		c.PropertyNames, c.UnevaluatedProperties = nil, nil
		c.DependentSchemas, c.DependentRequired, c.Dependencies = nil, nil, nil
	}
	if t != "array" {
		c.Items, c.PrefixItems, c.AdditionalItems = nil, nil, nil
		c.MinItems, c.MaxItems, c.UniqueItems = nil, nil, nil
		c.Contains, c.MinContains, c.MaxContains = nil, nil, nil
		c.UnevaluatedItems = nil
	}
	if t != "string" {
		c.MinLength, c.MaxLength, c.Pattern, c.Format = nil, nil, nil, nil
		c.ContentMediaType, c.ContentEncoding, c.ContentSchema = "", "", nil
	}
	if t != "number" && t != "integer" {
		c.Minimum, c.Maximum = nil, nil
		c.ExclusiveMinimum, c.ExclusiveMaximum = nil, nil
		c.MultipleOf, c.DivisibleBy = nil, nil
	}
	return &c
}

// typeUnionBranches describes a schema whose "type" offers more than one
// alternative: the branches that need a generated type of their own, and the
// JSON types the wrapper can still check inline.
//
// Two kinds of alternative land here. A draft-3 entry is a whole schema sitting
// inside the type array. A plain JSON type is an alternative too, and when the
// schema carries keywords scoped to a particular type -- "items",
// "uniqueItems", "minLength" -- that type's branch has to carry them: those
// keywords are the reason the property cannot simply be typed as whichever
// alternative they hint at, since doing so drops all the others.
//
// A type whose branch cannot be materialized falls back to the inline check, so
// it is still accepted; only its siblings go unenforced, which is where it
// stood before.
func (g *Generator) typeUnionBranches(s *schema.Schema, name string) ([]TypeSchemaBranch, []string) {
	branches := g.extractTypeSchemaBranches(s.TypeSchemas, name)
	if hasNonTypeScopedConstraints(s) || !hasTypeScopedConstraints(s) {
		return branches, s.Type
	}
	var allowed []string
	for _, t := range s.Type {
		if t == "null" {
			allowed = append(allowed, t)
			continue
		}
		branchName, ok := g.delegatedBranchType(narrowedToType(s, t), name+SchemaNameToGoName(t))
		if !ok {
			allowed = append(allowed, t)
			continue
		}
		branches = append(branches, TypeSchemaBranch{TypeName: branchName})
	}
	return branches, allowed
}

func (g *Generator) extractTypeSchemaBranches(typeSchemas []*schema.Schema, contextName string) []TypeSchemaBranch {
	var branches []TypeSchemaBranch
	for i, typeSchema := range typeSchemas {
		if typeSchema == nil || typeSchema.IsBooleanSchema() {
			continue
		}
		// A reference names the whole constraint rather than spelling it out,
		// so there is nothing here to translate into an inline check. Generate
		// the referenced type and let the branch delegate to it; the shallow
		// path below would otherwise drop the alternative entirely and the
		// wrapper would reject every value the reference was meant to admit.
		if typeSchema.EffectiveRef() != "" {
			if name := namedTypeName(g.resolveType(typeSchema, fmt.Sprintf("%sTypeAlternative%d", contextName, i))); name != "" {
				branches = append(branches, TypeSchemaBranch{TypeName: name})
				continue
			}
		}
		if len(typeSchema.Type) == 0 && len(typeSchema.Properties) == 0 {
			continue
		}
		branch := TypeSchemaBranch{AllowedTypes: append([]string(nil), typeSchema.Type...)}
		required := make(map[string]bool, len(typeSchema.Required))
		for _, name := range typeSchema.Required {
			required[name] = true
		}
		ok := true
		for _, propName := range sortedKeys(typeSchema.Properties) {
			propSchema := typeSchema.Properties[propName]
			if propSchema == nil || len(propSchema.Type) != 1 {
				ok = false
				break
			}
			branch.Properties = append(branch.Properties, TypeSchemaProperty{
				Name:     propName,
				JSONType: propSchema.Type[0],
				Required: required[propName],
			})
		}
		if ok && (len(branch.AllowedTypes) > 0 || len(branch.Properties) > 0) {
			branches = append(branches, branch)
		}
	}
	return branches
}

// isNilCheckable returns true if a Go type can be compared to nil.
// This includes pointers, interfaces (including 'any'), slices, and maps.
func isNilCheckable(t GoType) bool {
	switch v := t.(type) {
	case *PointerType:
		return true
	case *PrimitiveType:
		return v.Name == "any" || v.Name == "json.RawMessage"
	case *ArrayType:
		return true
	case *MapType:
		return true
	case *NamedType:
		return v.Pointer
	default:
		return false
	}
}

// extractAliasValidationRules extracts validation rules applicable to a
// top-level type alias (defined type). Unlike struct field validation, the
// receiver IS the value, so FieldName and JSONName are empty — the template
// uses the receiver name directly.
// Returns nil if the Go type is "any" (untyped schemas can't be validated).
// extractInferredItemConstraints extracts item-level validation info from an inferred
// array schema. It returns the fields needed for InferredAliasDef item validation.
//
// elemGoType is what resolveType made of one element of this array, before the
// caller replaced the slice with []any so a non-array instance can still decode.
// It is read, not recomputed: when it names a generated type, that type was
// built from this very element sub-schema and its Validate is the whole of what
// the sub-schema says. See inferredItemTypeName.
func (g *Generator) extractInferredItemConstraints(s *schema.Schema, parentName string, elemGoType GoType) (
	itemsFalse bool,
	itemsType string,
	itemsTypeName string,
	itemsChecks []ContainsCheck,
	itemsNested *NestedItemsDef,
	tupleItems []InferredTupleItem,
	additionalItemsFalse bool,
	additionalItemsType string,
	additionalItemsTypeName string,
) {
	hasPrefixItems := len(s.PrefixItems) > 0
	hasTupleItems := s.Items != nil && s.Items.Schemas != nil
	hasSingleItems := s.Items != nil && s.Items.Schema != nil

	// Draft 2020-12: prefixItems defines tuple positions. Older drafts ignore it.
	if hasPrefixItems && g.supportsPrefixItems(s) {
		for i, sub := range s.PrefixItems {
			tupleItems = append(tupleItems, g.inferredTupleItemFromSchema(sub, fmt.Sprintf("%sItem%d", parentName, i)))
		}
		// In draft 2020-12, "items" (as single schema) acts as additionalItems.
		if hasSingleItems {
			itemSchema := s.Items.Schema
			if g.schemaForbidsEveryValue(itemSchema) {
				additionalItemsFalse = true
			} else if name := g.inferredItemTypeName(itemSchema, nil, parentName+"Rest"); name != "" {
				additionalItemsTypeName = name
			} else if len(itemSchema.Type) == 1 {
				additionalItemsType = itemSchema.Type[0]
			}
		}
		return
	}

	// Pre-2020-12: items as array of schemas = tuple form.
	if hasTupleItems {
		for i, sub := range s.Items.Schemas {
			tupleItems = append(tupleItems, g.inferredTupleItemFromSchema(sub, fmt.Sprintf("%sItem%d", parentName, i)))
		}
		// additionalItems constrains elements beyond the tuple.
		if s.AdditionalItems != nil {
			if s.AdditionalItems.Bool != nil && !*s.AdditionalItems.Bool {
				additionalItemsFalse = true
			} else if addlSchema := s.AdditionalItems.Schema; addlSchema != nil {
				if name := g.inferredItemTypeName(addlSchema, nil, parentName+"Rest"); name != "" {
					additionalItemsTypeName = name
				} else if len(addlSchema.Type) == 1 {
					additionalItemsType = addlSchema.Type[0]
				}
			}
		}
		return
	}

	// items as single schema — validates every element.
	if hasSingleItems {
		itemSchema := s.Items.Schema
		if g.schemaForbidsEveryValue(itemSchema) {
			itemsFalse = true
		} else if itemSchema.IsBooleanSchema() {
			// items: true — no constraint
		} else if name := g.inferredItemTypeName(itemSchema, elemGoType, parentName+"Item"); name != "" {
			// The element's own generated type carries everything the
			// sub-schema states. Reaching for it first is what makes an
			// inferred array agree with the declared one written beside it:
			// the arms below can each say one thing about the element -- its
			// JSON type, a numeric bound, the type one level further in -- and
			// every other keyword was dropped, so {"items":{"type":"object",
			// "required":["a"]}} accepted [{}] while the same sub-schema under
			// an explicit "type":"array" rejected it. Issue #166.
			itemsTypeName = name
		} else if effRef := itemSchema.EffectiveRef(); effRef != "" {
			// $ref — resolve and check for simple type or named type.
			resolved := g.resolveRefInContext(effRef, itemSchema)
			if resolved != nil && len(resolved.Type) == 1 {
				itemsType = resolved.Type[0]
			} else {
				refName := g.resolveRefTypeName(itemSchema)
				if refName != "" {
					itemsTypeName = refName
				}
			}
		} else if nested := g.extractNestedItemsDef(itemSchema); nested != nil {
			itemsNested = nested
		} else if len(itemSchema.Type) == 1 {
			itemsType = itemSchema.Type[0]
		} else {
			// No explicit type — extract validation checks if present.
			itemsChecks = extractSchemaChecks(itemSchema)
		}
		return
	}

	return
}

// inferredItemTypeName answers with the generated Go type that stands for one
// element of an inferred array -- the type whose Validate the wrapper delegates
// to -- or "" when the element has none and the lightweight arms must answer.
//
// Two sources, in this order:
//
//   - elemGoType, what resolveType already made of the element. A named type
//     there was built from this very sub-schema node, so it is the canonical
//     name for it, and asking for a second one under the position's own name
//     would either be refused (constraintOnlyNamedType declines a name already
//     generated -- which is how an element {"enum":["x","y"]} kept its dropped
//     enum) or emit the same declaration twice.
//   - failing that, the ladder every other element, map value, property and
//     tuple slot walks. It is what reaches the shapes resolveType types without
//     naming: {"type":"string","pattern":"^a+$"} is a Go string, and the string
//     carries no pattern.
//
// Only the ladder's *named* answer is taken. Its JSONType answer is left alone
// deliberately: it reads primarySchemaType, which calls {"type":["object",
// "null"]} an object, and the caller's arms ask len(Type) == 1 instead -- so
// taking it would start refusing the null such a sub-schema permits.
func (g *Generator) inferredItemTypeName(itemSchema *schema.Schema, elemGoType GoType, posName string) string {
	if itemSchema == nil || itemSchema.IsBooleanSchema() || !g.validationKeywordsEnabled() {
		return ""
	}
	if name := namedTypeName(elemGoType); name != "" && g.namedTypeIsValidatable(name) {
		return name
	}
	if def, ok := g.tupleItemDefFor(itemSchema, posName); ok && g.namedTypeIsValidatable(def.TypeName) {
		return def.TypeName
	}
	return ""
}

// namedTypeIsValidatable reports whether name is safe to delegate a check to,
// which for a generated type means it has a Validate method to call.
//
// Go forbids methods on a type whose underlying is an interface, so `type X
// any` -- what a schema constraining nothing resolves to, and what a bookended
// $dynamicRef's permissive anchor resolves to -- has none, and code calling
// X.Validate does not compile.
//
// A name with no definition yet is taken as validatable: that is the recursive
// case, where the type is still being generated further up the stack and will
// carry whatever its own arm gives it. Answering false there would drop the
// check on every self-referential array, which is the one shape that has always
// had it.
//
// An alias is judged by walking its underlying chain here rather than by asking
// CanHaveMethods, because the flag that answers reads is not set until
// resolveAliasMethodability runs, long after this. Asking the unresolved flag
// answers "yes" for every alias, including `type ItemType any` -- the type a
// bookended $dynamicRef's permissive anchor produces -- and the generated file
// then called Validate on it and did not compile.
func (g *Generator) namedTypeIsValidatable(name string) bool {
	if name == "" {
		return false
	}
	for _, td := range g.output.TypeDefs {
		if td.TypeName() != name {
			continue
		}
		ad, ok := td.(*AliasDef)
		if !ok {
			return localTypeIsValidatable(td)
		}
		aliases := make(map[string]*AliasDef, len(g.output.TypeDefs))
		for _, other := range g.output.TypeDefs {
			if oad, ok := other.(*AliasDef); ok {
				aliases[oad.Name] = oad
			}
		}
		return canHaveMethodsResolved(ad.Underlying, aliases)
	}
	return true
}

func (g *Generator) extractNestedItemsDef(s *schema.Schema) *NestedItemsDef {
	if s == nil || s.Items == nil || s.Items.Schema == nil || len(s.PrefixItems) > 0 || s.AdditionalItems != nil {
		return nil
	}
	itemSchema := s.Items.Schema
	if itemSchema == nil || itemSchema.IsBooleanSchema() {
		return nil
	}
	if effRef := itemSchema.EffectiveRef(); effRef != "" {
		if resolved := g.resolveRefInContext(effRef, itemSchema); resolved != nil && len(resolved.Type) == 1 {
			return &NestedItemsDef{ItemsType: resolved.Type[0]}
		}
	}
	if len(itemSchema.Type) == 1 {
		return &NestedItemsDef{ItemsType: itemSchema.Type[0]}
	}
	return nil
}

// inferredTupleItemFromSchema converts a sub-schema to an InferredTupleItem.
// The generator is needed to resolve $ref sub-schemas.
//
// posName is the type name the position mints if it has to. There is no
// resolveType answer to read here -- an inferred array's Go type is the slice,
// not the tuple -- so the ladder is the only source, and it is asked before the
// JSON-type arms for the reason the element position asks it first: a slot
// reduced to its declared type drops every other keyword the slot states.
func (g *Generator) inferredTupleItemFromSchema(sub *schema.Schema, posName string) InferredTupleItem {
	if g.schemaForbidsEveryValue(sub) {
		return InferredTupleItem{IsFalse: true}
	}
	if sub.IsTrueSchema() || sub.IsBooleanSchema() {
		return InferredTupleItem{} // true schema — no constraint
	}
	if name := g.inferredItemTypeName(sub, nil, posName); name != "" {
		return InferredTupleItem{TypeName: name}
	}
	// If the sub-schema has a $ref, resolve it and check the resolved type.
	if effRef := sub.EffectiveRef(); effRef != "" {
		resolved := g.resolveRefInContext(effRef, sub)
		if resolved != nil {
			if len(resolved.Type) == 1 {
				return InferredTupleItem{JSONType: resolved.Type[0]}
			}
			// Could be a named type — generate it and reference it.
			goName := refToGoName(effRef)
			goName = g.goNameForResolvedRef(effRef, resolved, goName)
			if !g.generated[goName] {
				_ = g.generateTypeDef(goName, resolved)
			}
			return InferredTupleItem{TypeName: goName}
		}
	}
	if len(sub.Type) == 1 {
		return InferredTupleItem{JSONType: sub.Type[0]}
	}
	return InferredTupleItem{} // complex schema — skip for now
}

// resolveRefTypeName resolves a $ref schema to a Go type name, generating the
// referenced type if needed. Returns empty string if the ref cannot be resolved.
func (g *Generator) resolveRefTypeName(s *schema.Schema) string {
	effRef := s.EffectiveRef()
	if effRef == "" {
		return ""
	}
	goName := refToGoName(effRef)
	if resolved := g.resolveRefInContext(effRef, s); resolved != nil {
		goName = g.goNameForResolvedRef(effRef, resolved, goName)
		if !g.generated[goName] {
			_ = g.generateTypeDef(goName, resolved)
		}
	}
	return goName
}

// extractPropertyNamesDef builds a PropertyNamesDef from a propertyNames sub-schema.
// Returns nil if the sub-schema is boolean true or has no actionable constraints.
func (g *Generator) extractPropertyNamesDef(pn *schema.Schema) *PropertyNamesDef {
	// A sub-schema admitting nothing: no property name is valid (empty objects
	// only). `{"enum":[]}` says that as much as `false` does, and reached
	// neither this arm nor the enum arm below, which asks len() > 0 -- so
	// {"propertyNames":{"enum":[]}} accepted every object. `{"not":{}}` and
	// {"oneOf":[false,false]} say it too, and reached nothing at all; that is
	// issue #146, and schemaForbidsEveryValue is where all four spellings meet.
	// Every caller is already behind validationKeywordsEnabled, which is the
	// gate the empty enum needs; see emptyEnumSchema.
	if g.schemaForbidsEveryValue(pn) {
		return &PropertyNamesDef{IsForbidden: true}
	}
	// Boolean true schema: no constraint.
	if pn.IsTrueSchema() {
		return nil
	}

	def := &PropertyNamesDef{}
	hasConstraint := false

	if pn.MaxLength != nil {
		v := int(*pn.MaxLength)
		def.MaxLength = &v
		hasConstraint = true
	}
	if pn.MinLength != nil {
		v := int(*pn.MinLength)
		def.MinLength = &v
		hasConstraint = true
	}
	if pn.Pattern != nil {
		def.Pattern = *pn.Pattern
		hasConstraint = true
	}
	// Handle const (convert to single-element enum) and enum.
	enumValues := pn.Enum
	if pn.Const != nil && len(enumValues) == 0 {
		enumValues = []any{*pn.Const}
	}
	if len(enumValues) > 0 {
		for _, e := range enumValues {
			if str, ok := e.(string); ok {
				def.Enum = append(def.Enum, str)
			}
		}
		if len(def.Enum) > 0 {
			hasConstraint = true
		}
	}

	if !hasConstraint {
		return nil
	}
	return def
}

// isAlwaysTrueSchema returns true if the schema is semantically equivalent to
// "true" — i.e., it matches every possible value. This includes:
// - boolean true schema
// - empty schema (no keywords)
// - {"if": false, "else": true} pattern (if always fails, else always passes)
func isAlwaysTrueSchema(s *schema.Schema) bool {
	if s.IsTrueSchema() {
		return true
	}
	if s.IsBooleanSchema() {
		return false // IsFalseSchema case
	}
	// {"if": false, "else": true} pattern:
	// if is boolean false → always fails → else branch applies → true → always matches.
	if s.If != nil && s.If.IsFalseSchema() && s.Else != nil && s.Else.IsTrueSchema() {
		return true
	}
	// Empty schema (no constraints) matches everything.
	// Check for the absence of any constraining keywords.
	if s.Type == nil && s.Enum == nil && s.Const == nil &&
		s.Minimum == nil && s.Maximum == nil && s.MultipleOf == nil &&
		s.ExclusiveMinimum == nil && s.ExclusiveMaximum == nil &&
		s.MinLength == nil && s.MaxLength == nil && s.Pattern == nil &&
		s.MinItems == nil && s.MaxItems == nil && s.UniqueItems == nil &&
		s.MinProperties == nil && s.MaxProperties == nil &&
		s.Items == nil && len(s.PrefixItems) == 0 && s.AdditionalItems == nil &&
		s.Contains == nil && s.PropertyNames == nil &&
		s.AdditionalProperties == nil && len(s.Properties) == 0 &&
		len(s.Required) == 0 && len(s.AllOf) == 0 && len(s.AnyOf) == 0 &&
		len(s.OneOf) == 0 && s.Not == nil && s.If == nil &&
		len(s.DependentRequired) == 0 && len(s.DependentSchemas) == 0 &&
		s.EffectiveRef() == "" {
		return true
	}
	return false
}

// extractContainsDef builds a ContainsDef from a contains sub-schema.
// Returns nil if the sub-schema cannot be analyzed or is always-true with no
// minContains/maxContains constraints.
// extractSchemaChecks extracts ContainsCheck-style validation checks from a schema.
// This is used for items sub-schemas that have validation keywords but no explicit type.
func extractSchemaChecks(s *schema.Schema) []ContainsCheck {
	var checks []ContainsCheck
	if s.Minimum != nil {
		checks = append(checks, ContainsCheck{CheckType: "minimum", Value: *s.Minimum})
	}
	if s.Maximum != nil {
		checks = append(checks, ContainsCheck{CheckType: "maximum", Value: *s.Maximum})
	}
	if s.ExclusiveMinimum != nil && s.ExclusiveMinimum.Number != nil {
		checks = append(checks, ContainsCheck{CheckType: "exclusiveMinimum", Value: *s.ExclusiveMinimum.Number})
	}
	if s.ExclusiveMaximum != nil && s.ExclusiveMaximum.Number != nil {
		checks = append(checks, ContainsCheck{CheckType: "exclusiveMaximum", Value: *s.ExclusiveMaximum.Number})
	}
	if s.MultipleOf != nil {
		checks = append(checks, ContainsCheck{CheckType: "multipleOf", Value: *s.MultipleOf})
	}
	if len(s.Type) == 1 {
		checks = append(checks, ContainsCheck{CheckType: "type", Value: s.Type[0]})
	}
	return checks
}

// containsChecksCarryTheWholeSchema reports whether the flat per-element tests
// extractContainsDef collects say everything the sub-schema says.
//
// They can carry one declared JSON type, the numeric bounds, multipleOf, and
// the two string lengths with a pattern -- and nothing else. Every other
// keyword was read as if it were not written, so a `contains` naming a shape
// counted every value of the right JSON kind: {"contains":{"type":"object",
// "required":["a"]}} matched {} and accepted an array with no matching element.
// A sub-schema this answers false for is delegated to its own generated type
// instead; see ContainsDef.TypeName.
//
// The exclusive bounds are asked for their *numeric* spelling, because draft 3
// writes them as booleans modifying minimum/maximum and the check has no arm
// for that. enum and const never reach here: the arms above return on both.
func containsChecksCarryTheWholeSchema(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	if len(s.Type) > 1 || len(s.TypeSchemas) > 0 {
		return false
	}
	if hasProperties(s) || len(s.Required) > 0 || s.AdditionalProperties != nil ||
		s.PropertyNames != nil || s.MinProperties != nil || s.MaxProperties != nil ||
		len(s.DependentRequired) > 0 || len(s.DependentSchemas) > 0 || len(s.Dependencies) > 0 {
		return false
	}
	if s.Items != nil || len(s.PrefixItems) > 0 || s.AdditionalItems != nil ||
		s.Contains != nil || s.MinItems != nil || s.MaxItems != nil || s.UniqueItems != nil {
		return false
	}
	if len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 || s.Not != nil ||
		s.If != nil || s.Then != nil || s.Else != nil {
		return false
	}
	if s.Ref != "" || s.DynamicRef != "" || s.RecursiveRef != "" || len(s.Extends) > 0 {
		return false
	}
	if s.UnevaluatedItems != nil || s.UnevaluatedProperties != nil {
		return false
	}
	if s.Format != nil || s.ContentEncoding != "" || s.ContentMediaType != "" || s.ContentSchema != nil {
		return false
	}
	if len(s.Enum) > 0 || s.Const != nil || s.ConstIsNull {
		return false
	}
	if s.DivisibleBy != nil || len(s.Disallow) > 0 {
		return false
	}
	if s.ExclusiveMinimum != nil && s.ExclusiveMinimum.Number == nil {
		return false
	}
	if s.ExclusiveMaximum != nil && s.ExclusiveMaximum.Number == nil {
		return false
	}
	return true
}

// extractDependentSchemaConstraints extracts dependentSchemas constraints from a schema.
// It handles boolean false schemas, additionalProperties:false (allowed-keys check),
// required properties, minProperties/maxProperties, and the rest of what the
// branch demands of the object's shape.
//
// The shape half is an ObjectConditionalBranch, the same definition an
// object-level `then` carries. A dependentSchemas branch is an ordinary
// subschema gated on a key's presence rather than on an `if`, so it is held to
// the same bar `then` is: what the branch states and the evaluator can express
// is enforced, and what it cannot is dropped rather than costing the branch its
// other conjuncts. A schema object's keywords are conjunctive, so enforcing a
// subset of them can only let a wrong document through -- never refuse a right
// one. What survives is `required`, `additionalProperties: false`,
// minProperties/maxProperties, and per-property type, const, numeric bounds,
// multipleOf, string length and pattern. What does not: a branch carrying a
// $ref or an unrecognized keyword (refused whole, see objectConditionalBranchLenient),
// and per-property keywords the dynamic evaluator does not model -- enum,
// nested properties/items, and the applicators.
func (g *Generator) extractDependentSchemaConstraints(s *schema.Schema) []DependentSchemaConstraint {
	if len(s.DependentSchemas) == 0 {
		return nil
	}
	var result []DependentSchemaConstraint
	for _, trigger := range sortedKeys(s.DependentSchemas) {
		depSchema := s.DependentSchemas[trigger]
		constraint := DependentSchemaConstraint{TriggerKey: trigger}
		hasConstraint := false

		// A sub-schema admitting nothing: always reject when the trigger is
		// present. `{"enum":[]}` says that as much as `false` does, and reached no
		// arm below, so {"dependentSchemas":{"k":{"enum":[]}}} accepted every
		// object carrying "k". `{"not":{}}` and {"oneOf":[false,false]} are the
		// same statement again and reached nothing either -- issue #146. Both
		// callers are behind validationKeywordsEnabled, which is the gate the
		// empty enum needs; see emptyEnumSchema and schemaForbidsEveryValue.
		if g.schemaForbidsEveryValue(depSchema) {
			constraint.IsFalse = true
			result = append(result, constraint)
			continue
		}
		// Boolean true or empty schema: no constraint.
		if depSchema.IsTrueSchema() || isAlwaysTrueSchema(depSchema) {
			continue
		}

		// additionalProperties: false — only listed keys are allowed.
		if depSchema.AdditionalProperties != nil &&
			depSchema.AdditionalProperties.Bool != nil &&
			!*depSchema.AdditionalProperties.Bool {
			constraint.AllowedKeys = sortedKeys(depSchema.Properties)
			hasConstraint = true
		}

		// What the branch demands of the properties it names. `required` is
		// answered by RequiredProps below, so the branch keeps only the shape
		// half and the two do not check the same thing twice.
		//
		// This reads _jsonRawProps, which holds every key the document carried.
		// The per-property type check it replaces read the *overflow* map, so it
		// saw only keys the struct did not declare -- a branch constraining a
		// declared property was checked against a map that could never hold it.
		if branch := objectConditionalBranchLenient(dependentSchemaKeyword(trigger), depSchema); branch != nil {
			branch.RequiredKeys = nil
			if !branch.Empty() {
				constraint.Branch = branch
				hasConstraint = true
			}
		}

		// Required properties from the sub-schema.
		if len(depSchema.Required) > 0 {
			sorted := make([]string, len(depSchema.Required))
			copy(sorted, depSchema.Required)
			sort.Strings(sorted)
			constraint.RequiredProps = sorted
			hasConstraint = true
		}

		// minProperties / maxProperties from the sub-schema.
		if depSchema.MinProperties != nil {
			v := depSchema.MinProperties.Int()
			constraint.MinProperties = &v
			hasConstraint = true
		}
		if depSchema.MaxProperties != nil {
			v := depSchema.MaxProperties.Int()
			constraint.MaxProperties = &v
			hasConstraint = true
		}

		if hasConstraint {
			result = append(result, constraint)
		}
	}
	return result
}

// parentName names the type a sub-schema too rich for Checks is materialized
// under; see ContainsDef.TypeName.
func (g *Generator) extractContainsDef(s *schema.Schema, parentName string) (*ContainsDef, *int, *int) {
	if s.Contains == nil {
		return nil, nil, nil
	}

	containsSch := s.Contains

	// Compute minContains and maxContains.
	var minC *int
	var maxC *int
	if s.MinContains != nil {
		v := int(*s.MinContains)
		minC = &v
	}
	if s.MaxContains != nil {
		v := int(*s.MaxContains)
		maxC = &v
	}

	// A sub-schema admitting nothing: no element can ever match. `{"enum":[]}`
	// says that as much as `false` does, and reached neither this arm nor the
	// enum arms below, which ask len() == 1 and len() > 0 -- so
	// {"contains":{"enum":[]}} accepted every array, including the empty one that
	// `contains` refuses outright. `{"not":{}}` and {"oneOf":[false,false]} are
	// the same statement once more and reached nothing -- issue #146. Every
	// caller either sits behind validationKeywordsEnabled or discards the result
	// when it is off, which is the gate the empty enum needs; see
	// emptyEnumSchema and schemaForbidsEveryValue.
	if g.schemaForbidsEveryValue(containsSch) {
		return &ContainsDef{IsFalse: true}, minC, maxC
	}

	// Boolean true or always-true schema: every element matches.
	if isAlwaysTrueSchema(containsSch) {
		return &ContainsDef{IsTrue: true}, minC, maxC
	}

	def := &ContainsDef{}

	// Const → marshal to JSON for exact matching.
	if containsSch.Const != nil {
		b, err := json.Marshal(*containsSch.Const)
		if err == nil {
			def.ConstJSON = string(b)
			return def, minC, maxC
		}
	}

	// Single-value enum → treat as const.
	if len(containsSch.Enum) == 1 {
		b, err := json.Marshal(containsSch.Enum[0])
		if err == nil {
			def.ConstJSON = string(b)
			return def, minC, maxC
		}
	}

	// Multi-value enum → marshal all values, check if element matches any.
	if len(containsSch.Enum) > 1 {
		var enumValues []string
		allOK := true
		for _, v := range containsSch.Enum {
			b, err := json.Marshal(v)
			if err != nil {
				allOK = false
				break
			}
			enumValues = append(enumValues, string(b))
		}
		if allOK {
			def.EnumJSON = enumValues
			return def, minC, maxC
		}
	}

	// A sub-schema the checks below cannot say: `required`, `properties`, its
	// own `items`, a $ref, a composition. Every one of those was silently
	// dropped, and what survived was the declared type on its own -- so
	// {"contains":{"type":"object","required":["a"]}} counted every object and
	// accepted [{}], which contains no matching element at all. The element
	// position's answer to the same reduction is issue #166; this is the
	// keyword beside it, and it delegates to the same generated type.
	//
	// Ahead of the checks rather than behind them, because a sub-schema this
	// arm claims is one the checks would have answered *partly* -- and a
	// partial answer is what the defect is. A sub-schema they answer whole is
	// left to them: it gets the same verdict either way, and a delegation there
	// would only trade a readable inline test for a decode and a method call.
	if !containsChecksCarryTheWholeSchema(containsSch) {
		if name := g.inferredItemTypeName(containsSch, nil, parentName+"Contains"); name != "" {
			def.TypeName = name
			return def, minC, maxC
		}
	}

	// Collect constraint checks.
	var checks []ContainsCheck

	if containsSch.Minimum != nil {
		checks = append(checks, ContainsCheck{CheckType: "minimum", Value: *containsSch.Minimum})
	}
	if containsSch.Maximum != nil {
		checks = append(checks, ContainsCheck{CheckType: "maximum", Value: *containsSch.Maximum})
	}
	if containsSch.ExclusiveMinimum != nil && containsSch.ExclusiveMinimum.Number != nil {
		checks = append(checks, ContainsCheck{CheckType: "exclusiveMinimum", Value: *containsSch.ExclusiveMinimum.Number})
	}
	if containsSch.ExclusiveMaximum != nil && containsSch.ExclusiveMaximum.Number != nil {
		checks = append(checks, ContainsCheck{CheckType: "exclusiveMaximum", Value: *containsSch.ExclusiveMaximum.Number})
	}
	if containsSch.MultipleOf != nil {
		checks = append(checks, ContainsCheck{CheckType: "multipleOf", Value: *containsSch.MultipleOf})
	}
	if len(containsSch.Type) == 1 {
		checks = append(checks, ContainsCheck{CheckType: "type", Value: containsSch.Type[0]})
	}
	// String constraints
	if containsSch.MinLength != nil {
		checks = append(checks, ContainsCheck{CheckType: "minLength", Value: *containsSch.MinLength})
	}
	if containsSch.MaxLength != nil {
		checks = append(checks, ContainsCheck{CheckType: "maxLength", Value: *containsSch.MaxLength})
	}
	if containsSch.Pattern != nil && *containsSch.Pattern != "" {
		checks = append(checks, ContainsCheck{CheckType: "pattern", Value: *containsSch.Pattern})
	}

	if len(checks) > 0 {
		def.Checks = checks
		return def, minC, maxC
	}

	// Complex schema we can't extract checks from — skip.
	return nil, nil, nil
}

func extractAliasValidationRules(s *schema.Schema, goType GoType) []ValidationRule {
	// Skip validation on untyped "any" fields — can't compile numeric/string checks.
	if pt, ok := goType.(*PrimitiveType); ok && pt.Name == "any" {
		return nil
	}
	var rules []ValidationRule
	for _, r := range extractValidationRules("", "", s) {
		// A keyword about some other JSON type is satisfied by every value this
		// alias can hold, so the check would be enforcing nothing the schema
		// says. See ruleVacuousForType.
		if ruleVacuousForType(goType, r.RuleType) {
			continue
		}
		if r.RuleType == "format" {
			stringBacked, ok := aliasFormatCheckable(goType, r)
			if !ok {
				continue
			}
			r.StringBacked = stringBacked
		}
		rules = append(rules, r)
	}
	if len(rules) == 0 {
		return nil
	}
	return rules
}

// formatRuleShape decides how -- or whether -- a "format" rule can be written
// against a value of the given Go type.
//
// It answers two things at once, because they are one question. ok reports
// whether the check has any expression here at all: a value typed `any`, a
// wrapper struct, a slice, a number -- none of them can be handed to a format
// parser, and a rule emitted against one does not compile. stringBacked reports
// which of the two spellings the emitter must use for ipv4, ipv6 and date-time,
// the three formats that also have a Go type of their own:
//
//   - netip.Addr or time.Time. Decoding already refused everything the parser
//     rejects, so all that is left is the address family, and date-time has
//     nothing left at all -- its rule is dropped rather than emitted as a check
//     that cannot fail.
//   - string. The value is the JSON string the document carried, so the parse
//     *is* the assertion, and it must be written out. A schema arrives here
//     whenever the Go type mapping was given up: a format with no "type" (issue
//     #106), or one stated beside minLength/maxLength/pattern (see
//     formatGoTypeForSchema).
//
// A named type whose underlying type is a string counts as string-backed; the
// caller sets StringConvert so the conversion is emitted.
func formatRuleShape(goType GoType, r ValidationRule, stringNamed bool) (stringBacked, ok bool) {
	format, isString := r.Value.(string)
	if !isString || !FormatCheckableOnString(format) {
		return false, false
	}
	base := goType
	for {
		pt, isPtr := base.(*PointerType)
		if !isPtr {
			break
		}
		base = pt.Inner
	}
	goTypeName := ""
	if pt, isPrimitive := base.(*PrimitiveType); isPrimitive {
		goTypeName = pt.Name
	}
	isPlainString := goTypeName == "string" || stringNamed
	switch format {
	case "ipv4", "ipv6":
		if goTypeName == "netip.Addr" {
			return false, true
		}
		return true, isPlainString
	case "date-time":
		// time.Time only decodes from RFC 3339, so a value that reached the
		// field is already of the format. Nothing to assert.
		return true, isPlainString
	default:
		return true, isPlainString
	}
}

// aliasFormatCheckable reports whether the alias template can express this
// format check against a value of the alias's own type, and how.
//
// The check is written over the receiver converted back to the underlying type,
// so the two have to agree: the string formats read a string, and ipv4/ipv6 ask
// netip.Addr whether the address it parsed is of the right family. A format rule
// that reached an alias of some other shape -- a `format` stated beside an
// allOf, say, which resolves by its branches and not by the format -- has no
// expression here, and emitting one anyway would not compile.
func aliasFormatCheckable(goType GoType, r ValidationRule) (stringBacked, ok bool) {
	if _, isPrimitive := goType.(*PrimitiveType); !isPrimitive {
		return false, false
	}
	return formatRuleShape(goType, r, false)
}

// aliasVariantKeywords are the keywords an anyOf/oneOf branch of a scalar or
// array alias may carry for the branch to be judged at all.
//
// "type" is here because the branch's own type is what decides, statically,
// whether the branch can match this alias. The bounds keywords are here because
// the alias templates emit a check for each. Everything else -- $ref, enum,
// properties, allOf, not, format, contains -- has no expression against the
// alias's single value, and a branch carrying one is not judged: see
// aliasVariantRules.
var aliasVariantKeywords = map[string]bool{
	"type": true, "minimum": true, "maximum": true,
	"exclusiveMinimum": true, "exclusiveMaximum": true, "multipleOf": true,
	"minLength": true, "maxLength": true, "pattern": true,
	"minItems": true, "maxItems": true,

	"$schema": true, "$id": true, "title": true, "description": true,
	"$comment": true, "default": true, "examples": true,
	"deprecated": true, "readOnly": true, "writeOnly": true,
}

// aliasVariantRules converts one anyOf/oneOf branch of a schema that resolved to
// a single Go type into the checks the alias's Validate must apply to decide
// whether that branch matched.
//
// Three answers are possible and they are not interchangeable:
//
//   - ok == false: the branch says something this position cannot express. The
//     caller must then drop the whole group, because a branch judged with one of
//     its keywords ignored is judged as matching more values than it does, and
//     under oneOf an inflated match count rejects documents the schema allows.
//   - a single "never" rule: the branch's `type` excludes every value this alias
//     can hold, so the branch matches nothing. It still has to be counted -- as
//     zero -- which is why it is kept rather than dropped: a oneOf all of whose
//     branches are impossible is satisfied by nothing at all.
//   - any other rule list, possibly empty: the checks that decide the branch. An
//     empty list is a branch every value of this type satisfies, which is what a
//     branch made only of keywords about other types means.
func aliasVariantRules(variant *schema.Schema, goType GoType) ([]ValidationRule, bool) {
	if variant == nil {
		return nil, false
	}
	if variant.IsTrueSchema() {
		return nil, true
	}
	if variant.IsFalseSchema() {
		return []ValidationRule{{RuleType: "never"}}, true
	}
	if len(variant.Extensions) > 0 || len(variant.TypeSchemas) > 0 {
		return nil, false
	}
	// Decided from the re-marshaled key set rather than a list of struct fields,
	// so a keyword the parser learns later fails closed here instead of being
	// silently ignored. Same rule, and same reason, as dynamicBranchChecks.
	raw, err := json.Marshal(variant)
	if err != nil {
		return nil, false
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
		return nil, false
	}
	for key := range present {
		if !aliasVariantKeywords[key] {
			return nil, false
		}
	}

	kind := jsonKindForGoType(goType)
	if kind == "" {
		return nil, false
	}
	if len(variant.Type) > 0 {
		switch branchTypeVerdict(variant.Type, kind) {
		case typeVerdictNever:
			return []ValidationRule{{RuleType: "never"}}, true
		case typeVerdictUnknown:
			return nil, false
		}
		// typeVerdictAlways: every value of this Go type has one of the branch's
		// types, so the keyword is satisfied and needs no emitted check.
	}

	var rules []ValidationRule
	for _, r := range extractValidationRules("", "", variant) {
		if ruleVacuousForType(goType, r.RuleType) {
			continue
		}
		if !aliasVariantKeywords[r.RuleType] {
			// A keyword the branch is allowed to carry but this position cannot
			// check (uniqueItems, const). Judging the branch without it would
			// count it as matching values it rejects.
			return nil, false
		}
		rules = append(rules, r)
	}
	return rules, true
}

type typeVerdict int

const (
	// typeVerdictAlways: every value of the Go type satisfies the branch's type.
	typeVerdictAlways typeVerdict = iota
	// typeVerdictNever: no value of the Go type does.
	typeVerdictNever
	// typeVerdictUnknown: some do and some do not, so nothing static decides it.
	typeVerdictUnknown
)

// branchTypeVerdict decides a branch's `type` against the single JSON type the
// alias's Go type carries.
//
// The one undecidable pairing is `type: integer` against a float64: JSON Schema
// counts 1.0 as an integer, so whether the branch holds depends on the value.
// It is reported as unknown rather than guessed either way.
func branchTypeVerdict(types []string, kind string) typeVerdict {
	unknown := false
	for _, t := range types {
		switch {
		case t == kind:
			return typeVerdictAlways
		case t == "number" && kind == "integer":
			return typeVerdictAlways
		case t == "integer" && kind == "number":
			unknown = true
		}
	}
	if unknown {
		return typeVerdictUnknown
	}
	return typeVerdictNever
}

// aliasBranchVariants converts a list of anyOf/oneOf branches of a scalar or
// array alias, failing closed if any one of them cannot be judged.
func aliasBranchVariants(subs []*schema.Schema, goType GoType) ([][]ValidationRule, bool) {
	// Skip for untyped "any" — nothing about the value's type is known, so no
	// branch can be judged against it.
	if pt, ok := goType.(*PrimitiveType); ok && pt.Name == "any" {
		return nil, false
	}
	variants := make([][]ValidationRule, 0, len(subs))
	for _, sub := range subs {
		rules, ok := aliasVariantRules(sub, goType)
		if !ok {
			return nil, false
		}
		variants = append(variants, rules)
	}
	return variants, true
}

// extractAnyOfVariantRules extracts the per-branch checks of an anyOf on a
// schema that resolved to a single Go type.
//
// Returns nil when the schema has no anyOf, when a branch cannot be judged (see
// aliasVariantRules), or when every branch is satisfied by every value -- an
// anyOf that nothing can fail needs no check emitted for it.
func extractAnyOfVariantRules(s *schema.Schema, goType GoType) [][]ValidationRule {
	if len(s.AnyOf) == 0 {
		return nil
	}
	variants, ok := aliasBranchVariants(s.AnyOf, goType)
	if !ok {
		return nil
	}
	for _, rules := range variants {
		if len(rules) > 0 {
			return variants
		}
	}
	return nil
}

// extractOneOfVariantRules extracts the per-branch checks of a oneOf on a schema
// that resolved to a single Go type.
//
// Returns nil when the schema has no oneOf or when a branch cannot be judged
// (see aliasVariantRules). Unlike anyOf, a group whose branches carry no checks
// is still emitted when there is more than one of them: every branch then
// matches every value, so the count is the branch count and "exactly one" fails
// for all of them. A lone check-free branch matches everything exactly once and
// needs nothing emitted.
func extractOneOfVariantRules(s *schema.Schema, goType GoType) [][]ValidationRule {
	if len(s.OneOf) == 0 {
		return nil
	}
	variants, ok := aliasBranchVariants(s.OneOf, goType)
	if !ok {
		return nil
	}
	if len(variants) == 1 && len(variants[0]) == 0 {
		return nil
	}
	return variants
}

// patternValueScalarKeywords is what extractPatternPropertyValidationRules can
// express by reading the raw JSON value in place, plus the annotations that
// assert nothing. It is the same set aliasVariantKeywords lists, and for the
// same reason: both describe checks that need no decode.
var patternValueScalarKeywords = map[string]bool{
	"type": true, "minimum": true, "maximum": true,
	"exclusiveMinimum": true, "exclusiveMaximum": true, "multipleOf": true,
	"minLength": true, "maxLength": true, "pattern": true,
	"minItems": true, "maxItems": true,

	"$schema": true, "$id": true, "title": true, "description": true,
	"$comment": true, "default": true, "examples": true,
	"deprecated": true, "readOnly": true, "writeOnly": true,
}

// patternRulesCoverSchema reports whether the scalar rule list already says
// everything a patternProperties sub-schema says, so the bucket needs no type of
// its own.
//
// Decided from schemaKeywordSet rather than from a list of struct fields, so a
// keyword the parser learns later fails closed -- it is not in the set, the
// answer is no, and the position gets a materialized type that does understand
// it. That is the rule aliasVariantRules and dynamicBranchChecks already follow,
// and here it is what stops this from becoming a second hand-maintained list of
// keywords that silently lags the first. It is also what answers the keywords the
// marshaled form drops: {"patternProperties":{"^a":{"const":null}}} showed no key
// at all, so the scalar rules were held to cover a sub-schema they say nothing
// about and every value under a matching key was accepted (issue #154).
func patternRulesCoverSchema(s *schema.Schema) bool {
	if s == nil {
		return true
	}
	// TypeSchemas stays a test of its own: schemaKeywordSet reports it as "type",
	// which this list allows, and the scalar type rule can only carry a name.
	if len(s.Extensions) > 0 || len(s.TypeSchemas) > 0 {
		return false
	}
	present, ok := schemaKeywordSet(s)
	if !ok {
		return false
	}
	for key := range present {
		if !patternValueScalarKeywords[key] {
			return false
		}
	}
	return true
}

// patternValueTypeName materializes the type a patternProperties sub-schema is
// checked through and returns its name, or "" when the sub-schema states nothing
// a type could carry.
//
// A pattern's keys are not known until a document arrives, so the values sit in
// a raw-JSON bucket and there is no field for the usual dispatch to reach. Until
// this existed the only thing checking them was a hand-listed set of scalar
// rules -- type, the numeric bounds, multipleOf, the length bounds, pattern and
// the item-count bounds -- so everything else a sub-schema can say was enforced
// nowhere: an enum, a const, required, nested properties, a $ref, the contents
// of items, format, uniqueItems, a composition, and a nested patternProperties
// or additionalProperties. {"patternProperties":{"^a":{"$ref":"#/$defs/D"}}}
// generated D with a correct Validate and never called it.
//
// Naming the position is what closes that, and it is the same move the tuple
// positions make (see tupleItemDefFor): decode the raw value into the type the
// sub-schema generated and let that type answer for its own schema. The routes
// into a sub-schema are the ones tupleItemDefFor takes, and for the same
// reasons -- a $ref carrying structural siblings is an implicit allOf and has to
// be named here, a plain $ref reuses the target's own type rather than minting a
// copy, and anything else is materialized under posName.
//
// It answers "" for a sub-schema the scalar rules already say everything about,
// so a bucket whose whole content is a `type` and a length bound keeps the
// cheaper in-place check and gains no exported name. Whether the type it does
// materialize actually carries a Validate is settled later, by
// resolvePatternPropertyTypes, which is the only point at which that is known.
func (g *Generator) patternValueTypeName(sub *schema.Schema, posName string) string {
	if patternRulesCoverSchema(sub) {
		return ""
	}
	return g.rawValueTypeName(sub, posName)
}

// branchOverflowValueTypeName materializes the type an unaccounted value is
// checked through for a schema-valued additionalProperties or
// unevaluatedProperties read off an applicator branch.
//
// Unlike a patternProperties bucket there is no in-place scalar fallback to fall
// back to here, so the scalar short-circuit patternValueTypeName takes is not
// taken: a sub-schema whose whole content is {"type":"integer","minimum":5} --
// the shape issue #101 reproduces with -- still gets a type, because dropping it
// would leave the keyword enforcing nothing at all. The type is an alias with
// the bound on its Validate, and decoding into it is what asserts the type.
func (g *Generator) branchOverflowValueTypeName(sub *schema.Schema, posName string) string {
	return g.rawValueTypeName(sub, posName)
}

// rawValueTypeName materializes the type a raw JSON value is decoded into and
// validated through, and returns its name, or "" when the sub-schema states
// nothing a type could carry.
func (g *Generator) rawValueTypeName(sub *schema.Schema, posName string) string {
	if sub == nil || sub.IsBooleanSchema() || !g.validationKeywordsEnabled() {
		return ""
	}
	if (sub.EffectiveRef() != "" || sub.DynamicRef != "") &&
		!g.refOverridesSiblingsForSchema(sub) && hasRefStructuralSiblings(sub) {
		_ = g.generateTypeDef(posName, sub)
		if g.generated[posName] {
			g.patternMintedTypes[posName] = sub
			return posName
		}
	}
	if ref := sub.EffectiveRef(); ref != "" {
		if r := g.resolveRefInContext(ref, sub); r != nil {
			refName := g.uniqueTypeName(g.goNameForResolvedRef(ref, r, refToGoName(ref)), r)
			_ = g.generateTypeDef(refName, r)
			if g.generated[refName] {
				return refName
			}
		}
		return ""
	}
	_ = g.generateTypeDef(posName, sub)
	if g.generated[posName] {
		g.patternMintedTypes[posName] = sub
		return posName
	}
	return ""
}

// resolvePatternPropertyTypes settles, for each patternProperties bucket and
// each per-branch overflow check,
// whether the type materialized for its sub-schema is dispatched to or the
// scalar rules are used instead. Both were prepared during generation because
// the answer depends on every type def existing: an alias whose underlying chain
// ends at `any` cannot carry a method, and resolveAliasMethodability only knows
// that once the chain is complete.
//
// Where the type is dispatched to, the scalar rules are dropped. They are a
// partial restatement of the same sub-schema, and keeping both would report one
// violation twice -- and would keep the raw-JSON `type` scan, which reads a
// number written 1.0 as "number" and so rejects it against {"type":"integer"}
// in every draft from 6 on, where the spec says a zero fractional part is an
// integer. The generated type decodes integers per draft and does not.
//
// A type this mechanism minted and then declined is withdrawn from the file. It
// happens where the sub-schema resolves to `any` or to a pointer -- a bare
// `format` with no type, a $ref to an empty schema -- which Go forbids methods
// on, so the bucket falls back to the scalar rules and nothing refers to the
// name any more. Only names minted here are taken back: a bucket whose
// sub-schema is a $ref uses the target's own type, which exists for reasons of
// its own. Nothing materializes after this pass runs, so no later lookup can
// find the withdrawn name.
//
// A per-branch overflow check has no scalar fallback, so a declined type leaves
// it with nothing to say about the value. It keeps a `false` keyword's rejection
// and drops the value check, which under-enforces rather than rejecting a
// document the schema admits.
func (g *Generator) resolvePatternPropertyTypes() {
	validatable := make(map[string]bool)
	for _, td := range g.output.TypeDefs {
		if localTypeIsValidatable(td) {
			validatable[td.TypeName()] = true
		}
	}
	declined := make(map[string]bool)
	for _, td := range g.output.TypeDefs {
		sd, ok := td.(*StructDef)
		if !ok {
			continue
		}
		for i := range sd.PatternProperties {
			pp := &sd.PatternProperties[i]
			if pp.TypeName == "" {
				continue
			}
			if !validatable[pp.TypeName] {
				declined[pp.TypeName] = true
				pp.TypeName = ""
				continue
			}
			pp.Validations = nil
		}
		kept := sd.BranchOverflowChecks[:0]
		for i := range sd.BranchOverflowChecks {
			bc := &sd.BranchOverflowChecks[i]
			if bc.TypeName != "" && !validatable[bc.TypeName] {
				declined[bc.TypeName] = true
				bc.TypeName = ""
			}
			// A check with neither a rejection nor a type left says nothing.
			if !bc.IsForbidden && bc.TypeName == "" {
				continue
			}
			kept = append(kept, *bc)
		}
		sd.BranchOverflowChecks = kept
	}
	if len(declined) == 0 {
		return
	}
	// No second bucket can still be using one of these: a minted name carries the
	// owning struct and the bucket's own index, so exactly one bucket ever refers
	// to it, and the names that are shared -- a $ref target's -- are not minted
	// here and are never withdrawn.
	kept := g.output.TypeDefs[:0]
	for _, td := range g.output.TypeDefs {
		node, minted := g.patternMintedTypes[td.TypeName()]
		if minted && declined[td.TypeName()] {
			g.config.CrossPackage.forgetType(node)
			continue
		}
		kept = append(kept, td)
	}
	g.output.TypeDefs = kept
}

// extractPatternPropertyValidationRules extracts validation rules from a
// patternProperties sub-schema. These rules are checked at runtime against
// json.RawMessage values, so we include a "type" rule when the sub-schema
// specifies a type constraint.
//
// They are the fallback route, taken only where patternValueTypeName found no
// type to dispatch through; see resolvePatternPropertyTypes.
func extractPatternPropertyValidationRules(s *schema.Schema) []ValidationRule {
	var rules []ValidationRule
	// Type constraint — checked by inspecting the raw JSON value at runtime.
	if len(s.Type) > 0 {
		// Collect all allowed types (e.g., ["string", "null"]).
		var types []string
		for _, t := range s.Type {
			types = append(types, t)
		}
		if len(types) == 1 {
			rules = append(rules, ValidationRule{
				RuleType: "ppType", Value: types[0],
			})
		} else if len(types) > 1 {
			rules = append(rules, ValidationRule{
				RuleType: "ppType", Value: types,
			})
		}
	}
	// Numeric constraints.
	if s.Minimum != nil {
		rules = append(rules, ValidationRule{RuleType: "ppMinimum", Value: *s.Minimum})
	}
	if s.Maximum != nil {
		rules = append(rules, ValidationRule{RuleType: "ppMaximum", Value: *s.Maximum})
	}
	if s.ExclusiveMinimum != nil && s.ExclusiveMinimum.Number != nil {
		rules = append(rules, ValidationRule{RuleType: "ppExclusiveMinimum", Value: *s.ExclusiveMinimum.Number})
	}
	if s.ExclusiveMaximum != nil && s.ExclusiveMaximum.Number != nil {
		rules = append(rules, ValidationRule{RuleType: "ppExclusiveMaximum", Value: *s.ExclusiveMaximum.Number})
	}
	if s.MultipleOf != nil {
		rules = append(rules, ValidationRule{RuleType: "ppMultipleOf", Value: *s.MultipleOf})
	}
	// String constraints.
	if s.MinLength != nil {
		rules = append(rules, ValidationRule{RuleType: "ppMinLength", Value: s.MinLength.Int()})
	}
	if s.MaxLength != nil {
		rules = append(rules, ValidationRule{RuleType: "ppMaxLength", Value: s.MaxLength.Int()})
	}
	if s.Pattern != nil {
		rules = append(rules, ValidationRule{RuleType: "ppPattern", Value: *s.Pattern})
	}
	// Array constraints.
	if s.MinItems != nil {
		rules = append(rules, ValidationRule{RuleType: "ppMinItems", Value: s.MinItems.Int()})
	}
	if s.MaxItems != nil {
		rules = append(rules, ValidationRule{RuleType: "ppMaxItems", Value: s.MaxItems.Int()})
	}
	return rules
}

// extractNonObjectValidationRules extracts validation rules from the schema
// that apply to non-object data. These use the same pp* rule types as
// patternProperties since both validate json.RawMessage values at runtime.
func extractNonObjectValidationRules(s *schema.Schema) []ValidationRule {
	return extractPatternPropertyValidationRules(s)
}

// buildUnevaluatedPropertiesDef constructs an UnevaluatedPropertiesDef for a schema
// that has an unevaluatedProperties keyword. It walks the schema tree to determine
// which properties are "evaluated" (covered by properties, patternProperties,
// additionalProperties, or nested unevaluatedProperties in applicator subschemas).
func (g *Generator) buildUnevaluatedPropertiesDef(s *schema.Schema) *UnevaluatedPropertiesDef {
	uneval := s.UnevaluatedProperties
	if uneval == nil {
		return nil
	}

	def := &UnevaluatedPropertiesDef{}

	// Check if unevaluatedProperties is a boolean schema.
	if uneval.IsTrueSchema() {
		def.IsAllowed = true
		return def
	}
	// A sub-schema admitting nothing forbids every unevaluated key. `{"enum":[]}`
	// says that as much as `false` does, and took the branch below instead, where
	// a schema with no type is "allowed permissively" -- so
	// {"unevaluatedProperties":{"enum":[]}} accepted every extra key.
	if g.schemaForbidsEveryValue(uneval) {
		def.IsForbidden = true
	} else {
		// unevaluatedProperties is a schema constraint (not boolean).
		// Extract validation rules from the schema to apply to each unevaluated value.
		unevalType := primarySchemaType(uneval)
		if unevalType == "" {
			unevalType = g.inferTypeFromConstraints(uneval)
		}
		if unevalType != "" {
			goType := PrimitiveTypeFromSchema(unevalType)
			if goType != nil {
				def.ValueType = goType.GoTypeName()
				rules := extractValidationRules("", "", uneval)
				def.Validations = rules
			} else {
				// Non-primitive type (object/array) — too complex, allow permissively.
				def.IsAllowed = true
				return def
			}
		} else {
			// No type constraint — allow permissively.
			def.IsAllowed = true
			return def
		}
	}

	// Collect evaluated properties from the schema tree.
	names, patterns, allEvaluated, conditionals := g.collectEvaluatedProperties(s)
	def.AllEvaluated = allEvaluated

	// Convert to sorted slices for deterministic output.
	def.EvaluatedNames = sortedKeys(names)
	def.EvaluatedPatterns = sortedKeys(patterns)
	def.ConditionalEvals = conditionals

	return def
}

// collectEvaluatedProperties walks the schema tree and collects property names
// and patterns that are "evaluated" for the purpose of unevaluatedProperties.
// The root schema's own unevaluatedProperties is NOT included (that's the
// constraint we're evaluating); only nested applicator subschemas contribute.
// It returns:
//   - names: set of property names evaluated by always-true sources (properties, allOf, $ref)
//   - patterns: set of regex patterns from always-true sources
//   - allEvaluated: true if additionalProperties or unevaluatedProperties in a nested
//     schema marks ALL remaining properties as evaluated
//   - conditionals: runtime-conditional evaluation branches for anyOf/oneOf/if-then-else/dependentSchemas
func (g *Generator) collectEvaluatedProperties(s *schema.Schema) (names map[string]bool, patterns map[string]bool, allEvaluated bool, conditionals []ConditionalEval) {
	names = make(map[string]bool)
	patterns = make(map[string]bool)

	if s == nil {
		return
	}

	// Direct properties on the root schema — these are always evaluated.
	for k := range s.Properties {
		names[k] = true
	}

	// Pattern properties on the root schema.
	for pattern := range s.PatternProperties {
		patterns[pattern] = true
	}

	// additionalProperties on the root schema marks ALL remaining as evaluated.
	if s.AdditionalProperties != nil {
		allEvaluated = true
		return
	}

	// $ref on the root — evaluated properties from the referenced schema.
	if effRef := s.EffectiveRef(); effRef != "" {
		if resolved := g.resolveEffectiveRefSchema(s); resolved != nil {
			g.collectEvaluatedFromNested(resolved, names, patterns, &allEvaluated)
		}
	}
	if s.DynamicRef != "" {
		if resolved := g.resolveDynamicRef(s.DynamicRef, s); resolved != nil {
			g.collectEvaluatedFromNested(resolved, names, patterns, &allEvaluated)
		}
	}

	// Recurse into allOf — all branches always apply.
	// We also check each allOf sub-schema for oneOf/anyOf/if-then-else and
	// build conditional evals for them instead of static over-approximation.
	for _, sub := range s.AllOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		// Collect static evaluated properties (properties, patternProperties, $ref, etc.)
		// but exclude oneOf/anyOf/if-then-else from the static collection.
		g.collectEvaluatedFromNestedExcludeConditional(resolved, names, patterns, &allEvaluated)

		// Build conditional evals for oneOf/anyOf/if-then-else inside allOf sub-schemas.
		if len(resolved.OneOf) > 0 {
			ce := g.collectMultiBranchEval("oneOf", resolved.OneOf)
			if ce != nil {
				conditionals = append(conditionals, *ce)
			} else {
				// Fallback: static over-approximation.
				for _, osub := range resolved.OneOf {
					oresolved := osub
					if effRef := osub.EffectiveRef(); effRef != "" {
						if r := g.resolveRefInContext(effRef, osub); r != nil {
							oresolved = r
						}
					}
					g.collectEvaluatedFromNested(oresolved, names, patterns, &allEvaluated)
				}
			}
		}
		if len(resolved.AnyOf) > 0 {
			ce := g.collectMultiBranchEval("anyOf", resolved.AnyOf)
			if ce != nil {
				conditionals = append(conditionals, *ce)
			} else {
				for _, asub := range resolved.AnyOf {
					aresolved := asub
					if effRef := asub.EffectiveRef(); effRef != "" {
						if r := g.resolveRefInContext(effRef, asub); r != nil {
							aresolved = r
						}
					}
					g.collectEvaluatedFromNested(aresolved, names, patterns, &allEvaluated)
				}
			}
		}
		if resolved.If != nil {
			ifCond := g.extractIfCondition(resolved.If)
			if ifCond != nil {
				thenBranch := g.collectBranchEval(resolved.Then)
				elseBranch := g.collectBranchEval(resolved.Else)
				ifBranch := g.collectBranchEval(resolved.If)
				if ifBranch != nil && thenBranch != nil {
					thenBranch = mergeEvalBranches(ifBranch, thenBranch)
				} else if ifBranch != nil && thenBranch == nil {
					thenBranch = ifBranch
				}
				hasThen := thenBranch != nil && (thenBranch.HasNames() || thenBranch.HasPatterns() || thenBranch.AllEvaluated)
				hasElse := elseBranch != nil && (elseBranch.HasNames() || elseBranch.HasPatterns() || elseBranch.AllEvaluated)
				if hasThen || hasElse {
					conditionals = append(conditionals, ConditionalEval{
						Kind:       "ifThenElse",
						IfBranch:   ifCond,
						ThenBranch: thenBranch,
						ElseBranch: elseBranch,
					})
				}
			} else {
				g.collectEvaluatedFromNested(resolved.If, names, patterns, &allEvaluated)
				if resolved.Then != nil {
					g.collectEvaluatedFromNested(resolved.Then, names, patterns, &allEvaluated)
				}
				if resolved.Else != nil {
					g.collectEvaluatedFromNested(resolved.Else, names, patterns, &allEvaluated)
				}
			}
		}
	}

	// Runtime-conditional branches: anyOf/oneOf/if-then-else/dependentSchemas.
	// Instead of merging all properties statically, we collect per-branch info
	// so the generated Validate() can build the evaluated set dynamically.

	// dependentSchemas: properties evaluated only when the trigger key is present.
	for triggerKey, depSchema := range s.DependentSchemas {
		branch := g.collectBranchEval(depSchema)
		if branch != nil && (branch.HasNames() || branch.HasPatterns() || branch.AllEvaluated) {
			conditionals = append(conditionals, ConditionalEval{
				Kind:       "dependentSchema",
				TriggerKey: triggerKey,
				Branch:     branch,
			})
		}
	}

	// if/then/else: try runtime conditional evaluation via IfConditionDef.
	// If the if-schema is too complex for runtime evaluation, fall back to
	// static over-approximation.
	if s.If != nil {
		ifCond := g.extractIfCondition(s.If)
		if ifCond != nil {
			// Runtime-evaluable if condition: create conditional branches.
			thenBranch := g.collectBranchEval(s.Then)
			elseBranch := g.collectBranchEval(s.Else)
			// Also collect properties from the if-schema itself into both branches,
			// since the if-schema's properties are evaluated when it matches.
			ifBranch := g.collectBranchEval(s.If)
			if ifBranch != nil && thenBranch != nil {
				thenBranch = mergeEvalBranches(ifBranch, thenBranch)
			} else if ifBranch != nil && thenBranch == nil {
				thenBranch = ifBranch
			}
			// Per JSON Schema spec: when if fails, its annotations are discarded.
			// So the else branch does NOT include if-schema properties.
			hasThen := thenBranch != nil && (thenBranch.HasNames() || thenBranch.HasPatterns() || thenBranch.AllEvaluated)
			hasElse := elseBranch != nil && (elseBranch.HasNames() || elseBranch.HasPatterns() || elseBranch.AllEvaluated)
			if hasThen || hasElse {
				conditionals = append(conditionals, ConditionalEval{
					Kind:       "ifThenElse",
					IfBranch:   ifCond,
					ThenBranch: thenBranch,
					ElseBranch: elseBranch,
				})
			}
		} else {
			// Fallback: static over-approximation.
			g.collectEvaluatedFromNested(s.If, names, patterns, &allEvaluated)
			if s.Then != nil {
				g.collectEvaluatedFromNested(s.Then, names, patterns, &allEvaluated)
			}
			if s.Else != nil {
				g.collectEvaluatedFromNested(s.Else, names, patterns, &allEvaluated)
			}
		}
	}

	// anyOf: try runtime conditional evaluation via branch matching.
	// If branches have evaluable matching criteria (required keys + const checks),
	// use runtime evaluation; otherwise fall back to static over-approximation.
	if len(s.AnyOf) > 0 {
		ce := g.collectMultiBranchEval("anyOf", s.AnyOf)
		if ce != nil {
			conditionals = append(conditionals, *ce)
		} else {
			// Fallback: static over-approximation.
			for _, sub := range s.AnyOf {
				resolved := sub
				if effRef := sub.EffectiveRef(); effRef != "" {
					if r := g.resolveRefInContext(effRef, sub); r != nil {
						resolved = r
					}
				}
				g.collectEvaluatedFromNested(resolved, names, patterns, &allEvaluated)
			}
		}
	}

	// oneOf: try runtime conditional evaluation via branch matching.
	// If branches have evaluable matching criteria (required keys + const checks),
	// use runtime evaluation; otherwise fall back to static over-approximation.
	if len(s.OneOf) > 0 {
		ce := g.collectMultiBranchEval("oneOf", s.OneOf)
		if ce != nil {
			conditionals = append(conditionals, *ce)
		} else {
			// Fallback: static over-approximation.
			for _, sub := range s.OneOf {
				resolved := sub
				if effRef := sub.EffectiveRef(); effRef != "" {
					if r := g.resolveRefInContext(effRef, sub); r != nil {
						resolved = r
					}
				}
				g.collectEvaluatedFromNested(resolved, names, patterns, &allEvaluated)
			}
		}
	}

	return
}

// collectEvaluatedFromNested collects evaluated property names and patterns from
// a nested schema (inside allOf, $ref, etc.). Unlike the root schema, nested
// schemas' additionalProperties and unevaluatedProperties DO mark all as evaluated.
func (g *Generator) collectEvaluatedFromNested(s *schema.Schema, names map[string]bool, patterns map[string]bool, allEvaluated *bool) {
	g.collectEvaluatedFromNestedOnPath(s, names, patterns, allEvaluated, nil)
}

// collectEvaluatedFromNestedOnPath is collectEvaluatedFromNested carrying the
// set of schemas the collection is already inside.
//
// The walk follows $ref, $dynamicRef and every in-place applicator, so a schema
// that references itself -- {"$ref": "#", "unevaluatedProperties": false} is
// the whole of it -- re-enters this function forever, a stack overflow. What is
// being built is a union of names and patterns plus a monotone allEvaluated
// flag, so a schema already on the path has nothing left to add: it contributed
// on the way in, and the frame above is still walking the rest of it.
//
// The mark comes off on the way out, so a schema reached down two separate
// branches is still collected from on each, exactly as before. The set is
// allocated once per top-level collection.
func (g *Generator) collectEvaluatedFromNestedOnPath(s *schema.Schema, names map[string]bool, patterns map[string]bool, allEvaluated *bool, onPath map[*schema.Schema]bool) {
	if s == nil || onPath[s] {
		return
	}
	if s.IsBooleanSchema() {
		return
	}

	// Direct properties.
	for k := range s.Properties {
		names[k] = true
	}

	// Pattern properties.
	for pattern := range s.PatternProperties {
		patterns[pattern] = true
	}

	// additionalProperties in a nested schema marks ALL remaining as evaluated.
	if s.AdditionalProperties != nil {
		*allEvaluated = true
	}

	// unevaluatedProperties in a nested schema marks ALL remaining as evaluated.
	if s.UnevaluatedProperties != nil {
		*allEvaluated = true
	}

	if onPath == nil {
		onPath = make(map[*schema.Schema]bool)
	}
	onPath[s] = true
	defer delete(onPath, s)

	// $ref — evaluated properties from the referenced schema.
	if effRef := s.EffectiveRef(); effRef != "" {
		if resolved := g.resolveEffectiveRefSchema(s); resolved != nil {
			g.collectEvaluatedFromNestedOnPath(resolved, names, patterns, allEvaluated, onPath)
		}
	}
	if s.DynamicRef != "" {
		if resolved := g.resolveDynamicRef(s.DynamicRef, s); resolved != nil {
			g.collectEvaluatedFromNestedOnPath(resolved, names, patterns, allEvaluated, onPath)
		}
	}

	// Recurse into allOf — all branches always apply.
	for _, sub := range s.AllOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		g.collectEvaluatedFromNestedOnPath(resolved, names, patterns, allEvaluated, onPath)
	}

	// Recurse into anyOf/oneOf — collect from all branches (over-approximation).
	for _, sub := range s.AnyOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		g.collectEvaluatedFromNestedOnPath(resolved, names, patterns, allEvaluated, onPath)
	}
	for _, sub := range s.OneOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		g.collectEvaluatedFromNestedOnPath(resolved, names, patterns, allEvaluated, onPath)
	}

	// Recurse into if/then/else.
	if s.If != nil {
		g.collectEvaluatedFromNestedOnPath(s.If, names, patterns, allEvaluated, onPath)
	}
	if s.Then != nil {
		g.collectEvaluatedFromNestedOnPath(s.Then, names, patterns, allEvaluated, onPath)
	}
	if s.Else != nil {
		g.collectEvaluatedFromNestedOnPath(s.Else, names, patterns, allEvaluated, onPath)
	}

	// Recurse into dependentSchemas.
	for _, depSchema := range s.DependentSchemas {
		g.collectEvaluatedFromNestedOnPath(depSchema, names, patterns, allEvaluated, onPath)
	}
}

// collectEvaluatedFromNestedExcludeConditional is like collectEvaluatedFromNested
// but skips oneOf, anyOf, and if/then/else processing. These are handled separately
// by the caller via conditional evaluation instead of static over-approximation.
func (g *Generator) collectEvaluatedFromNestedExcludeConditional(s *schema.Schema, names map[string]bool, patterns map[string]bool, allEvaluated *bool) {
	if s == nil {
		return
	}
	if s.IsBooleanSchema() {
		return
	}

	// Direct properties.
	for k := range s.Properties {
		names[k] = true
	}

	// Pattern properties.
	for pattern := range s.PatternProperties {
		patterns[pattern] = true
	}

	// additionalProperties in a nested schema marks ALL remaining as evaluated.
	if s.AdditionalProperties != nil {
		*allEvaluated = true
	}

	// unevaluatedProperties in a nested schema marks ALL remaining as evaluated.
	if s.UnevaluatedProperties != nil {
		*allEvaluated = true
	}

	// This entry point is not itself recursive, but everything it hands off to
	// is, and s is the node they can be led back to. Seeding the path with it
	// is what stops {"$ref": "#", ...} from re-entering through here.
	onPath := map[*schema.Schema]bool{s: true}

	// $ref — evaluated properties from the referenced schema.
	if effRef := s.EffectiveRef(); effRef != "" {
		if resolved := g.resolveEffectiveRefSchema(s); resolved != nil {
			g.collectEvaluatedFromNestedOnPath(resolved, names, patterns, allEvaluated, onPath)
		}
	}
	if s.DynamicRef != "" {
		if resolved := g.resolveDynamicRef(s.DynamicRef, s); resolved != nil {
			g.collectEvaluatedFromNestedOnPath(resolved, names, patterns, allEvaluated, onPath)
		}
	}

	// Recurse into allOf — all branches always apply.
	for _, sub := range s.AllOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		g.collectEvaluatedFromNestedOnPath(resolved, names, patterns, allEvaluated, onPath)
	}

	// NOTE: oneOf, anyOf, and if/then/else are NOT processed here.
	// The caller handles them via conditional evaluation.

	// Recurse into dependentSchemas.
	for _, depSchema := range s.DependentSchemas {
		g.collectEvaluatedFromNestedOnPath(depSchema, names, patterns, allEvaluated, onPath)
	}
}

// collectBranchEval collects evaluated property names and patterns from a single
// schema branch, returning an EvalBranchDef. Returns nil if the branch is nil.
func (g *Generator) collectBranchEval(s *schema.Schema) *EvalBranchDef {
	if s == nil {
		return nil
	}
	names := make(map[string]bool)
	patterns := make(map[string]bool)
	var allEvaluated bool

	// Collect from this schema and its nested applicators.
	g.collectEvaluatedFromNested(s, names, patterns, &allEvaluated)

	branch := &EvalBranchDef{
		Names:        sortedKeys(names),
		Patterns:     sortedKeys(patterns),
		AllEvaluated: allEvaluated,
	}

	// Collect branch-matching metadata: required keys and const checks.
	branch.RequiredKeys = append([]string(nil), s.Required...)
	sort.Strings(branch.RequiredKeys)
	for propName, propSchema := range s.Properties {
		if propSchema != nil && propSchema.Const != nil {
			jsonVal, err := json.Marshal(*propSchema.Const)
			if err == nil {
				branch.ConstChecks = append(branch.ConstChecks, ConstCheck{
					PropertyName: propName,
					GoFieldName:  JSONPropertyToGoName(propName),
					JSONValue:    string(jsonVal),
				})
			}
		}
	}
	// Sort const checks for deterministic output.
	sort.Slice(branch.ConstChecks, func(i, j int) bool {
		return branch.ConstChecks[i].PropertyName < branch.ConstChecks[j].PropertyName
	})

	return branch
}

// extractIfCondition extracts a runtime-evaluable condition from an if-schema.
// Returns nil if the if-schema is too complex for runtime evaluation.
func (g *Generator) extractIfCondition(s *schema.Schema) *IfConditionDef {
	if s == nil {
		return nil
	}
	// We can evaluate if-schemas that use properties with const constraints
	// and/or required fields.
	var constChecks []ConstCheck
	for propName, propSchema := range s.Properties {
		if propSchema != nil && propSchema.Const != nil {
			jsonVal, err := json.Marshal(*propSchema.Const)
			if err == nil {
				constChecks = append(constChecks, ConstCheck{
					PropertyName: propName,
					GoFieldName:  JSONPropertyToGoName(propName),
					JSONValue:    string(jsonVal),
				})
			}
		}
	}
	requiredKeys := append([]string(nil), s.Required...)
	sort.Strings(requiredKeys)
	sort.Slice(constChecks, func(i, j int) bool {
		return constChecks[i].PropertyName < constChecks[j].PropertyName
	})

	// Must have at least some condition to evaluate.
	if len(constChecks) == 0 && len(requiredKeys) == 0 {
		return nil
	}

	return &IfConditionDef{
		ConstChecks:  constChecks,
		RequiredKeys: requiredKeys,
	}
}

// collectMultiBranchEval collects evaluation branches for anyOf/oneOf.
// Returns a ConditionalEval or nil if any branch is too complex.
func (g *Generator) collectMultiBranchEval(kind string, subs []*schema.Schema) *ConditionalEval {
	if len(subs) == 0 {
		return nil
	}

	branches := g.flattenBranches(subs, 0)
	if branches == nil {
		return nil
	}

	// Check that at least some branches have evaluable properties.
	hasContent := false
	for _, b := range branches {
		if b.HasNames() || b.HasPatterns() || b.AllEvaluated {
			hasContent = true
			break
		}
	}
	if !hasContent {
		return nil
	}

	// Check that ALL branches have matching criteria (required keys or const checks).
	// Without matching criteria, we can't determine which branch matched at runtime
	// and must fall back to static over-approximation.
	for _, b := range branches {
		if len(b.RequiredKeys) == 0 && len(b.ConstChecks) == 0 {
			return nil
		}
	}

	return &ConditionalEval{
		Kind:     kind,
		Branches: branches,
	}
}

// oneOfUnionKeepsWholeSchema reports whether rendering s's oneOf as a
// sealed-interface union preserves everything s asserts about the value.
//
// generateOneOfForProperty builds that union out of s.OneOf and s.Discriminator
// and nothing else: no field, no check and no error message carries any other
// keyword s declares. So when s asserts anything beside the oneOf — its own
// properties and required, an allOf, an enum, a minLength — the union is not a
// translation of s, it is a translation of s.OneOf, and the rest is gone. The
// caller must then take the ordinary type path instead, where the object-level
// flattening (ObjectOneOfs) or the per-variant rule extraction
// (extractOneOfVariantRules) attaches the branches to a type that keeps the
// siblings. That is already what the document root does, and what anyOf in the
// same position has always done.
//
// "type" is deliberately not counted. It names the Go representation the union
// variants already commit to rather than adding an assertion of its own, and
// {"type":"object","oneOf":[{"$ref":...},...]} is a spelling of the ordinary
// discriminated union that must keep generating one.
func oneOfUnionKeepsWholeSchema(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	if hasTypeScopedConstraints(s) {
		return false
	}
	// hasNonTypeScopedConstraints, minus the oneOf being rendered.
	return len(s.AllOf) == 0 && len(s.AnyOf) == 0 &&
		s.Not == nil && s.If == nil && s.Then == nil && s.Else == nil &&
		s.Ref == "" && s.DynamicRef == "" && s.RecursiveRef == "" &&
		len(s.Enum) == 0 && s.Const == nil && !s.ConstIsNull
}

// oneOfBranchIsUnselectable reports whether a oneOf branch gives the
// sealed-interface union nothing to select on.
//
// A union decides which branch a value belongs to by trying to decode it into
// each variant's Go type, then applying whatever that variant carries:
// resolveOneOfVariant picks the type, oneOfVariantChecks picks the scalar
// bounds, and the union's required-key test picks the object shapes. A branch
// with no type, no $ref, no properties and no required reaches none of those —
// resolveOneOfVariant gives it `any`, oneOfVariantChecks returns nil for `any`,
// and there are no required keys — so the branch matches every value that is
// JSON at all.
func oneOfBranchIsUnselectable(v *schema.Schema) bool {
	if v == nil || v.IsBooleanSchema() {
		return false
	}
	return v.EffectiveRef() == "" && !hasProperties(v) &&
		primarySchemaType(v) == "" && len(v.Required) == 0
}

// oneOfIsUnselectableUnion reports whether every non-null branch of s's oneOf is
// unselectable, which makes the sealed-interface union not merely lossy but
// wrong: every branch matches every value, so the union reports "multiple oneOf
// variants matched" for each of them — for the values that satisfy exactly one
// branch as much as for the values that satisfy none. No document is accepted
// and the rejections name the wrong reason.
//
// {"type":"integer","oneOf":[{"minimum":10},{"maximum":5}]} is the shape: 20,
// 12 and 3 each match exactly one branch and are valid, 7 matches none, and the
// union rejects all four as "matched 2". The caller must take the ordinary type
// path instead, where extractOneOfVariantRules (for a declared or inferred
// type) or dynamicSchemaDef (for neither) evaluates the branches against the
// value rather than trying to pick one to decode into. That is what the
// document root already does with the identical schema.
//
// One branch short of all is deliberately left alone: a union with a typed
// branch beside an unselectable one still selects on the typed branch, and
// changing that shape would reach far past the construct this repairs.
func (g *Generator) oneOfIsUnselectableUnion(s *schema.Schema) bool {
	if s == nil || len(s.OneOf) == 0 {
		return false
	}
	nonNull, _ := g.separateNullFromOneOf(s.OneOf)
	// Fewer than two branches never reaches the union: one non-null branch
	// beside a null one is the nullable-pointer shape, and zero is not a union
	// at all.
	if len(nonNull) < 2 {
		return false
	}
	for _, v := range nonNull {
		if !oneOfBranchIsUnselectable(v) {
			return false
		}
	}
	return true
}

// oneOfVariantSelectionType returns the Go type resolveOneOfVariant would answer
// for a branch that gets no named type of its own, and nil for a branch that gets
// one.
//
// The distinction is who enforces the branch. A named type — a $ref, an inline
// object, an allOf merge, a formatted string — keeps its branch's constraints in
// its own Validate, which the union's second tally calls. Every other branch is
// judged by selection alone, and this is the type selection judges it with.
//
// It is a side-effect-free mirror of those arms' conditions, in their order,
// because the callers need the answer before any type is generated. The arms
// themselves must not be run to find out: each one that answers a named type
// generates it, and a predicate that emits a type definition as the price of
// being asked is no predicate at all. TestOneOfVariantSelectionTypeMirrorsResolution
// holds the two in step.
func (g *Generator) oneOfVariantSelectionType(v *schema.Schema) GoType {
	if v == nil {
		return nil
	}
	if v.IsBooleanSchema() {
		return &PrimitiveType{Name: "any"}
	}
	if v.EffectiveRef() != "" {
		return nil
	}
	if g.objectShapeNeedsNamedType(v) || g.allOfNeedsNamedType(v) {
		return nil
	}
	if g.stringAnnotationOnlySchema(v) || g.nullableFormatUnion(v) || g.declaredFormatStringSchema(v) {
		return nil
	}
	if pt := primarySchemaType(v); pt != "" {
		if goType := PrimitiveTypeFromSchema(pt); goType != nil {
			return goType
		}
	}
	return &PrimitiveType{Name: "any"}
}

// oneOfBranchOutrunsSelection reports whether the union's selection counts this
// branch as matched for values the branch itself refuses.
//
// It is one question, asked of oneOfVariantFullyChecked over the type and the
// checks selection actually has for the branch. A branch that resolves to `any`
// gets no checks at all, so a const, an enum, a bare bound or a not on it is
// tested nowhere and every instance matches. A branch that resolves to a scalar
// gets the bounds keywords and nothing else, so an enum beside its `type` is
// tested nowhere either and every instance of that type matches. And a `false`
// branch — the sharpest case, and the one issue #125 is written about — is
// answered by the same rule: no instance satisfies it, resolveOneOfVariant hands
// it the `any` every instance decodes into, and oneOfVariantFullyChecked refuses
// to speak for it. That refusal is what sends the group away from here.
//
// A branch with a named type is excluded by oneOfVariantSelectionType, since its
// own Validate carries what selection does not.
func (g *Generator) oneOfBranchOutrunsSelection(v *schema.Schema) bool {
	if v == nil {
		return false
	}
	goType := g.oneOfVariantSelectionType(v)
	if goType == nil {
		return false
	}
	return !oneOfVariantFullyChecked(v, goType, v.Required, oneOfVariantChecks(v, goType))
}

// oneOfUnionOutrunsBranches reports whether the sealed-interface union would
// reach the wrong verdict for s's oneOf because one of its branches outruns
// selection.
//
// One such branch is enough to break the whole group, in both directions at
// once. It is matched by every document, so a document that satisfies exactly
// one *other* branch takes the count to two and is refused — a false rejection —
// while a document that satisfies no branch at all is left at a count of one and
// is accepted. {"oneOf":[{object},{"const":"x"},false]} is the shape: {"k":"a"}
// satisfies one branch and is reported as matching three.
//
// A group of one is the same defect with only the second direction left, and it
// used to be waved through here on the reasoning that "fewer than two branches
// never reaches the union". Two of the three cases that reads do not: the group
// generateOneOfForProperty declines is the one with a null branch *beside* the
// single non-null one, which becomes the nullable pointer, and a group of no
// branches at all. A lone non-null branch does reach the union, as a union of one
// variant, and one variant that outruns selection is matched by every document
// the field can hold — so {"oneOf":[{"type":"string","const":"a"}]} on a property
// accepted {"p":"b"}, and so did {"oneOf":[false]} and {"oneOf":[{"enum":[]}]},
// which admit no document at all. That is issue #150; the same schemas at a
// document root have always been right, because the root never built a union.
//
// oneOfIsUnselectableUnion keeps its own arity test. A lone branch selection
// cannot see is this function's business, and it is answered here with the
// somewhere-better guard the caller applies — where that predicate fires
// unconditionally, on the strength of a miscount only a second branch can
// produce.
func (g *Generator) oneOfUnionOutrunsBranches(s *schema.Schema) bool {
	if s == nil || len(s.OneOf) == 0 {
		return false
	}
	// A metaschema that omits the validation vocabulary leaves the branches
	// asserting nothing, so there is nothing for selection to outrun and no
	// verdict to get wrong. Taking the union away there would change the type a
	// caller sees and buy no check, since nothing in this mode emits one.
	if !g.validationKeywordsEnabled() {
		return false
	}
	nonNull, hasNull := g.separateNullFromOneOf(s.OneOf)
	if len(nonNull) == 0 || (hasNull && len(nonNull) == 1) {
		return false
	}
	for _, v := range nonNull {
		if g.oneOfBranchOutrunsSelection(v) {
			return true
		}
	}
	return false
}

// oneOfHasSomewhereBetterThanTheUnion reports whether declining the union leaves
// s somewhere that enforces more than the union would, rather than at `type X
// any`.
//
// This is the condition on taking a union away for outrunning its branches, and
// only on that one. A union that miscounts still enforces something; the `any`
// alias enforces nothing at all and cannot even be given a Validate, so trading
// one for the other would answer a false rejection with a false acceptance of
// every document the schema forbids. The two better homes are the two arms that
// would claim the schema instead: a declared or inferred type takes the alias
// path, where the branches are evaluated against the typed value, and everything
// else has to be readable by one of the evaluators.
//
// rawWrapperDef is asked rather than reasoned about, since it is the code that
// decides, and a second copy of its reasoning here would be a second thing to
// keep in step. Asking costs the compiled form and nothing else: it appends no
// type definition and claims no name -- the name a definition would carry does
// not enter either evaluator's answer, so any name asks the same question -- and
// the one thing it can leave behind, a remote document its $ref resolution
// registered, is a document the branch would have made it fetch anyway.
func (g *Generator) oneOfHasSomewhereBetterThanTheUnion(s *schema.Schema) bool {
	if primarySchemaType(s) != "" || g.inferTypeFromConstraints(s) != "" {
		return true
	}
	// The forbidding arm is the third home, and leaving it off the list is what
	// kept {"oneOf":[false]} and {"oneOf":[{"enum":[]}]} on a property in the
	// union: both outrun selection, neither names a type, and the evaluator
	// declines a group whose only branch admits nothing -- so the union was
	// judged the best on offer when generateTypeDef would in fact have forbidden
	// every document (issue #150). compositionAdmitsNothing is the same question
	// resolveType already delegates for an element or a map value, asked on
	// exactly the terms that arm decides it.
	if g.compositionAdmitsNothing(s) {
		return true
	}
	return g.rawWrapperDef("", s) != nil
}

// oneOfRendersAsUnion reports whether a oneOf in a property (or property-like)
// position should be rendered as a sealed-interface union: the union must carry
// everything the schema asserts, it must have something to select on, and what
// it selects on must agree with what the branches say -- unless disagreeing is
// still the best on offer.
func (g *Generator) oneOfRendersAsUnion(s *schema.Schema) bool {
	if !oneOfUnionKeepsWholeSchema(s) || g.oneOfIsUnselectableUnion(s) {
		return false
	}
	return !(g.oneOfUnionOutrunsBranches(s) && g.oneOfHasSomewhereBetterThanTheUnion(s))
}

// isOneOfOnlySchema returns true if the schema contains ONLY a oneOf (no direct
// properties, patternProperties, required, additionalProperties, etc.) — just
// structural content that can be flattened.
func isOneOfOnlySchema(s *schema.Schema) bool {
	if s == nil || len(s.OneOf) == 0 {
		return false
	}
	return len(s.Properties) == 0 &&
		len(s.PatternProperties) == 0 &&
		len(s.Required) == 0 &&
		s.AdditionalProperties == nil &&
		len(s.AllOf) == 0 &&
		len(s.AnyOf) == 0 &&
		s.If == nil
}

// flattenBranches recursively collects EvalBranchDefs from a list of sub-schemas.
// When a sub-schema resolves to a oneOf-only schema (no direct properties),
// it is expanded into its inner branches. Returns nil if recursion exceeds depth limit.
func (g *Generator) flattenBranches(subs []*schema.Schema, depth int) []EvalBranchDef {
	if depth > 5 {
		return nil // prevent infinite recursion
	}
	var branches []EvalBranchDef
	for _, sub := range subs {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}

		// If the resolved schema is a oneOf-only schema, flatten recursively.
		if isOneOfOnlySchema(resolved) {
			inner := g.flattenBranches(resolved.OneOf, depth+1)
			if inner == nil {
				return nil // propagate failure
			}
			branches = append(branches, inner...)
			continue
		}

		branch := g.collectBranchEval(resolved)
		if branch == nil {
			branch = &EvalBranchDef{}
		}
		// For matching, we need required keys and/or const checks from the original
		// sub-schema (not the resolved one, since required is on the sub itself).
		if len(sub.Required) > 0 && len(branch.RequiredKeys) == 0 {
			branch.RequiredKeys = append([]string(nil), sub.Required...)
			sort.Strings(branch.RequiredKeys)
		}
		for propName, propSchema := range sub.Properties {
			if propSchema != nil && propSchema.Const != nil {
				jsonVal, err := json.Marshal(*propSchema.Const)
				if err == nil {
					// Check if this const check already exists from resolved schema.
					found := false
					for _, cc := range branch.ConstChecks {
						if cc.PropertyName == propName {
							found = true
							break
						}
					}
					if !found {
						branch.ConstChecks = append(branch.ConstChecks, ConstCheck{
							PropertyName: propName,
							GoFieldName:  JSONPropertyToGoName(propName),
							JSONValue:    string(jsonVal),
						})
					}
				}
			}
		}
		sort.Slice(branch.ConstChecks, func(i, j int) bool {
			return branch.ConstChecks[i].PropertyName < branch.ConstChecks[j].PropertyName
		})
		branches = append(branches, *branch)
	}
	return branches
}

// mergeEvalBranches merges two EvalBranchDef into one (union of names and patterns).
func mergeEvalBranches(a, b *EvalBranchDef) *EvalBranchDef {
	names := make(map[string]bool)
	patterns := make(map[string]bool)
	for _, n := range a.Names {
		names[n] = true
	}
	for _, n := range b.Names {
		names[n] = true
	}
	for _, p := range a.Patterns {
		patterns[p] = true
	}
	for _, p := range b.Patterns {
		patterns[p] = true
	}
	return &EvalBranchDef{
		Names:        sortedKeys(names),
		Patterns:     sortedKeys(patterns),
		AllEvaluated: a.AllEvaluated || b.AllEvaluated,
	}
}

// collectBranchOverflowChecks builds the per-branch view of the two keywords an
// allOf merge cannot express by folding them into the parent: a branch's
// `additionalProperties` and a branch's `unevaluatedProperties`.
//
// Both are scoped to the schema object that states them. The parent's overflow
// map is not that scope -- it holds the keys the *parent* does not declare -- so
// hanging either keyword off it checks a different set of keys from the one the
// schema names. A branch that declares nothing of its own speaks about every key
// of the instance, including the ones the parent declares and gives fields to,
// and no field-shaped or overflow-shaped check can reach those. Each branch gets
// its own accounted set here instead, and the emitted loop runs over the raw
// JSON, which is the only place every key of the instance still exists together.
//
// ownerName names the types minted for a schema-valued keyword.
func (g *Generator) collectBranchOverflowChecks(s *schema.Schema, ownerName string) []BranchOverflowCheck {
	var checks []BranchOverflowCheck

	// unevaluatedProperties, from a direct allOf branch. This is the long-standing
	// "cousin isolation" case: an unevaluatedProperties inside an applicator
	// branch sees the annotations of its own branch and not a sibling's.
	//
	// An allOf branch only. Every allOf branch has to hold, so its keyword and its
	// accounted set are both readable from the schema. An anyOf or oneOf branch
	// binds only where the instance satisfies it, so neither is knowable here; see
	// collectRuntimeBranchChecks, which answers those against the document.
	for _, sub := range s.AllOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		if resolved.UnevaluatedProperties == nil {
			continue
		}
		if check := g.buildBranchUnevalCheck(resolved, ownerName, len(checks)); check != nil {
			checks = append(checks, *check)
		}
	}

	// additionalProperties, from an allOf branch only. Every allOf branch binds,
	// so a keyword read off one can be enforced unconditionally; an anyOf branch
	// need not be the one the instance satisfies, and enforcing its keyword
	// anyway would reject documents the schema admits.
	checks = append(checks, g.collectBranchAdditionalChecks(s.AllOf, s.AdditionalProperties, ownerName, len(checks), make(map[*schema.Schema]bool))...)

	return checks
}

// collectRuntimeBranchChecks compiles an `anyOf` or `oneOf` whose branches state
// `unevaluatedProperties` to the runtime evaluator, because nothing static can
// answer it.
//
// The keyword in such a branch is scoped to that branch *and* conditional on it:
// a branch the document fails contributes nothing, neither its assertions nor
// the annotations its own `unevaluatedProperties` exempts. Which branches
// contribute is therefore a property of the document, and the generated code has
// to evaluate them to find out. Enforcing the keyword unconditionally was issue
// #111 -- a false rejection, which is worse than no check at all.
//
// The whole keyword is compiled rather than the branch stating it, for two
// reasons. The branch's own `properties`, `patternProperties`,
// `additionalProperties`, `$ref` and nested applicators are what exempt a key
// from its `unevaluatedProperties`, and only evaluating the branch produces that
// set; and the keyword's own assertion -- at least one branch matches, exactly
// one for `oneOf` -- has to be judged against the same branches, or a document
// could pass a branch that the evaluator then fails.
//
// A subtree the evaluator cannot model yields nothing and the check is dropped,
// which leaves the applicator judged by the static approximation alone. That is
// the same answer this keyword got before #111 in every position but the
// rejecting one.
//
// The allOf branches are read as well as the schema's own keywords, because they
// are merged into this same struct and their applicators land here or nowhere.
// extractObjectAnyOfDefs has always had that reach and this had not, so a branch
// `unevaluatedProperties` inside an allOf got the static approximation and never
// the exact check (issue #135). The two halves are not symmetric in what that
// costs: through `anyOf` the approximation simply admits what it cannot see, and
// {"allOf":[{"anyOf":[{"properties":{"b":{}},"unevaluatedProperties":false}]}]}
// accepted {"b":1,"c":2}, which no branch admits; through `oneOf` it counts a
// branch that does not hold, and an allOf branch whose oneOf reads
// [{"properties":{"b":{}},"required":["b"],"unevaluatedProperties":false},
// {"properties":{"a":{}},"required":["a"]}]
// *rejected* {"a":1,"b":1}, which satisfies the second branch alone -- #111's
// false rejection, surviving one level down. So this is not only a missing
// check, and the suppression below is as much of the fix as the check is.
//
// A branch reached through a $ref is resolved, by the same route
// extractObjectAnyOfDefs takes, so the two agree on which branches exist; and an
// owner reached twice -- a schema whose allOf refers back to itself -- is
// compiled once.
func (g *Generator) collectRuntimeBranchChecks(s *schema.Schema) []RuntimeBranchCheck {
	if s == nil || !g.validationKeywordsEnabled() {
		return nil
	}
	var checks []RuntimeBranchCheck
	seen := make(map[*schema.Schema]bool, 1+len(s.AllOf))
	collect := func(owner *schema.Schema) {
		if owner == nil || seen[owner] {
			return
		}
		seen[owner] = true
		for _, group := range []struct {
			keyword string
			field   string
			subs    []*schema.Schema
		}{{"anyOf", "AnyOf", owner.AnyOf}, {"oneOf", "OneOf", owner.OneOf}} {
			if len(group.subs) == 0 {
				continue
			}
			// Two reasons to compile an applicator rather than approximate it.
			//
			// The first is that a branch states unevaluatedProperties, which only
			// the document can settle (#111).
			//
			// The second is that the approximation declines to speak at all. For
			// `anyOf` that drops the "at least one branch matches" assertion
			// outright, so a document no branch admits is accepted -- see
			// anyOfSummaryCannotJudgeBranches and issue #133. The evaluator judges
			// every branch against the document, which the summary's vocabulary of
			// required keys and property checks cannot do for a `false`, a const,
			// an enum, a bare bound or a `not`.
			//
			// `oneOf` keeps its own approximation: objectOneOfDefFromVariants
			// answers nil for the same branches, and a group reaching here has
			// properties beside it, so the shape #125 repaired -- a bare oneOf
			// leaving the union for the evaluator -- was already decided elsewhere.
			needsRuntime := g.branchStatesUnevaluatedProperties(group.subs)
			if !needsRuntime && group.keyword == "anyOf" {
				needsRuntime = g.anyOfSummaryCannotJudgeBranches(group.subs)
			}
			if !needsRuntime {
				continue
			}
			// No hoistPrefix: this literal is a local variable inside a Validate
			// method, and a recursive node needs a package-level variable to
			// point back at. A schema that would want one is declined here and
			// keeps the static approximation, as it did before hoisting existed.
			b := &nodeBuilder{g: g, allowed: validatorKeywords, inlineRefs: true, stack: map[*schema.Schema]int{}}
			list, ok := b.list(group.subs, 2)
			if !ok {
				continue
			}
			checks = append(checks, RuntimeBranchCheck{
				Keyword:     group.keyword,
				NodeLiteral: fmt.Sprintf("_schemaNode{\n\t%s: %s,\n}", group.field, list),
				owner:       owner,
			})
		}
	}
	collect(s)
	for _, sub := range s.AllOf {
		collect(g.resolveSchemaForApplicator(sub))
	}
	return checks
}

// runtimeBranchTaken reports whether one schema object's applicator keyword was
// compiled to an exact check, so the static approximation of that same keyword
// must be dropped rather than run beside it.
//
// The question is about a single variant slice, not about the struct: with the
// allOf reach above, one struct can carry an `anyOf` the evaluator took over and
// an allOf branch's `anyOf` it could not read. Answering per struct would drop
// the second one's approximation too and leave it checked by nothing.
func runtimeBranchTaken(checks []RuntimeBranchCheck, owner *schema.Schema, keyword string) bool {
	for i := range checks {
		if checks[i].Keyword == keyword && checks[i].owner == owner {
			return true
		}
	}
	return false
}

// branchStatesUnevaluatedProperties reports whether any direct branch of an
// applicator states `unevaluatedProperties`, on the branch itself or through a
// $ref it carries.
//
// Both sides are read, and the draft decides whether the first one counts.
// Through draft-07 a $ref replaces the schema object it sits in, so a keyword
// beside it says nothing and only the target's counts; from 2019-09 the $ref is
// an ordinary applicator and the branch's own keyword applies as well. Reading
// only the target was what left
// {"anyOf":[{"$ref":"#/$defs/base","properties":{"y":{}},
//
//	"unevaluatedProperties":false}]}
//
// unchecked: the target states no such keyword, so nothing here fired.
//
// This is a probe, so the lookup is the uncounted one: a branch that turns out
// not to compile costs the caller nothing, and recording the reference would
// make an optimistic look here decide whether Generate reports an unresolved
// reference.
//
// Direct branches only, which is the same reach the static collector has. A
// keyword buried deeper inside a branch was never enforced and is not enforced
// now; taking the applicator over for it would change generated code for schemas
// that have no defect to fix.
func (g *Generator) branchStatesUnevaluatedProperties(subs []*schema.Schema) bool {
	for _, sub := range subs {
		if sub == nil {
			continue
		}
		effRef := sub.EffectiveRef()
		if effRef == "" || !g.refOverridesSiblingsForSchema(sub) {
			if sub.UnevaluatedProperties != nil {
				return true
			}
		}
		if effRef == "" {
			continue
		}
		if r := g.resolveRefInContextUncounted(effRef, sub); r != nil && r.UnevaluatedProperties != nil {
			return true
		}
	}
	return false
}

// collectBranchAdditionalChecks walks the allOf branches for schema objects that
// state `additionalProperties`, following the same routes into a branch that the
// merge itself follows: a $ref chain and a nested allOf.
//
// The accounted set is the `properties` and `patternProperties` written *in the
// same schema object* as the keyword, and nothing else. That adjacency is what
// additionalProperties means: it does not see through a $ref, and it does not
// see a sibling branch's or the parent's declarations. It is the one way this
// differs from unevaluatedProperties, which does collect what its own $ref and
// nested allOf evaluated.
//
// merged is the additionalProperties the surrounding merge already adopted, if
// any. Where a branch's keyword is that very node, generateAllOfDef proved the
// parent's overflow map holds exactly the keys the branch governs and the
// keyword is already enforced through it; a second check here would only report
// the same violation twice.
func (g *Generator) collectBranchAdditionalChecks(allOf []*schema.Schema, merged *schema.SchemaOrBool, ownerName string, firstIndex int, onPath map[*schema.Schema]bool) []BranchOverflowCheck {
	var checks []BranchOverflowCheck
	for _, sub := range allOf {
		if sub == nil || onPath[sub] {
			continue
		}
		onPath[sub] = true
		resolved := sub
		for {
			if ap := resolved.AdditionalProperties; ap != nil && ap != merged {
				if check := g.buildBranchAdditionalCheck(resolved, ownerName, firstIndex+len(checks)); check != nil {
					checks = append(checks, *check)
				}
			}
			checks = append(checks, g.collectBranchAdditionalChecks(resolved.AllOf, merged, ownerName, firstIndex+len(checks), onPath)...)
			effRef := resolved.EffectiveRef()
			if effRef == "" {
				break
			}
			r := g.resolveRefInContext(effRef, resolved)
			if r == nil || onPath[r] {
				break
			}
			onPath[r] = true
			resolved = r
		}
	}
	return checks
}

// buildBranchAdditionalCheck builds the check for the `additionalProperties`
// stated on one schema object. Returns nil when the keyword permits everything.
func (g *Generator) buildBranchAdditionalCheck(s *schema.Schema, ownerName string, index int) *BranchOverflowCheck {
	ap := s.AdditionalProperties
	if ap == nil || !g.validationKeywordsEnabled() {
		return nil
	}
	if ap.Bool != nil && *ap.Bool {
		// additionalProperties: true — every value is permitted.
		return nil
	}
	check := &BranchOverflowCheck{
		Keyword:           "additionalProperties",
		AccountedNames:    sortedKeys(s.Properties),
		AccountedPatterns: sortedKeys(s.PatternProperties),
	}
	if ap.Bool != nil && !*ap.Bool {
		check.IsForbidden = true
		return check
	}
	if ap.Schema == nil {
		return nil
	}
	check.TypeName = g.branchOverflowValueTypeName(ap.Schema, fmt.Sprintf("%sBranch%dValue", ownerName, index))
	if check.TypeName == "" {
		// Nothing left to check: the sub-schema materialized no type that could
		// carry a Validate, and there is no key to reject either.
		return nil
	}
	return check
}

// buildBranchUnevalCheck builds the check for the `unevaluatedProperties` stated
// on one applicator branch. Returns nil when the keyword permits everything.
func (g *Generator) buildBranchUnevalCheck(s *schema.Schema, ownerName string, index int) *BranchOverflowCheck {
	uneval := s.UnevaluatedProperties
	if uneval == nil {
		return nil
	}

	// Check boolean value.
	if uneval.IsBooleanSchema() {
		if uneval.BooleanSchema != nil && *uneval.BooleanSchema {
			// unevaluatedProperties: true — no constraint, skip.
			return nil
		}
		// unevaluatedProperties: false
	}

	// Collect evaluated properties scoped to this branch only.
	names := make(map[string]bool)
	patterns := make(map[string]bool)
	var allEvaluated bool

	// Direct properties on this sub-schema.
	for k := range s.Properties {
		names[k] = true
	}
	for pattern := range s.PatternProperties {
		patterns[pattern] = true
	}
	if s.AdditionalProperties != nil {
		allEvaluated = true
	}

	// $ref on this sub-schema.
	if effRef := s.EffectiveRef(); effRef != "" {
		if resolved := g.resolveEffectiveRefSchema(s); resolved != nil {
			g.collectEvaluatedFromNested(resolved, names, patterns, &allEvaluated)
		}
	}
	if s.DynamicRef != "" {
		if resolved := g.resolveDynamicRef(s.DynamicRef, s); resolved != nil {
			g.collectEvaluatedFromNested(resolved, names, patterns, &allEvaluated)
		}
	}

	// allOf within this sub-schema.
	for _, sub := range s.AllOf {
		resolved := sub
		if effRef := sub.EffectiveRef(); effRef != "" {
			if r := g.resolveRefInContext(effRef, sub); r != nil {
				resolved = r
			}
		}
		g.collectEvaluatedFromNested(resolved, names, patterns, &allEvaluated)
	}

	check := &BranchOverflowCheck{
		Keyword:           "unevaluatedProperties",
		AccountedNames:    sortedKeys(names),
		AccountedPatterns: sortedKeys(patterns),
		AllAccounted:      allEvaluated,
	}
	if uneval.IsBooleanSchema() {
		check.IsForbidden = uneval.BooleanSchema == nil || !*uneval.BooleanSchema
		return check
	}
	// A schema-valued unevaluatedProperties in a branch says what an unevaluated
	// value must be, and until the per-branch notion could carry a type there was
	// nowhere to put that: the check was built, emitted as an empty loop body and
	// enforced nothing.
	if !g.validationKeywordsEnabled() {
		return nil
	}
	check.TypeName = g.branchOverflowValueTypeName(uneval, fmt.Sprintf("%sBranch%dValue", ownerName, index))
	if check.TypeName == "" {
		return nil
	}
	return check
}

// tuplePositionSchemas returns the sub-schemas a tuple array states for its
// positions: prefixItems in draft 2020-12, items-as-array in draft 4-7.
func (g *Generator) tuplePositionSchemas(s *schema.Schema) []*schema.Schema {
	if s == nil {
		return nil
	}
	if g.isTupleArray(s) {
		return s.PrefixItems
	}
	if s.Items != nil && len(s.Items.Schemas) > 0 {
		return s.Items.Schemas
	}
	return nil
}

// tupleTailSchema returns the sub-schema governing every position past a
// tuple's prefix, or nil when nothing does.
//
// The keyword differs by draft and the two spellings must not be mixed up. In
// 2020-12 a schema-valued `items` beside prefixItems governs only the tail --
// it is the successor of additionalItems, not of the uniform items. Before
// 2020-12 the tuple is `items` as an array and the tail is additionalItems.
// Either way the tail is meaningless without a prefix to follow, so a caller
// that found no positions gets nil.
func (g *Generator) tupleTailSchema(s *schema.Schema) *schema.Schema {
	if s == nil {
		return nil
	}
	if g.isTupleArray(s) {
		if s.Items == nil {
			return nil
		}
		return s.Items.Schema
	}
	if s.Items != nil && len(s.Items.Schemas) > 0 && s.AdditionalItems != nil {
		return s.AdditionalItems.AsSchema()
	}
	return nil
}

// tupleItemDefFor resolves one tuple position's sub-schema into the check that
// position carries. posName is the type name to materialize under when the
// sub-schema needs one. Returns the zero def when the position constrains
// nothing, and reports whether anything is left to check.
func (g *Generator) tupleItemDefFor(posSch *schema.Schema, posName string) (TupleItemDef, bool) {
	if posSch == nil {
		return TupleItemDef{}, false
	}

	// A sub-schema admitting nothing — reject any value at this position.
	// `{"enum":[]}` is the same statement as `false` (see schemaForbidsEveryValue)
	// and reached none of the arms below: the enum arms all ask len() > 0, and
	// what was left read the rest of the schema as if the keyword were absent, so
	// {"prefixItems":[{"enum":[]}]} accepted [1] and {"prefixItems":
	// [{"type":"string","enum":[]}]} accepted every string at that position.
	if g.schemaForbidsEveryValue(posSch) {
		return TupleItemDef{IsFalse: true}, true
	}

	// Boolean true schema — no constraint.
	if posSch.IsTrueSchema() {
		return TupleItemDef{}, false
	}

	// Resolve $ref chain to find the target schema and its generated type name.
	resolved := posSch
	refName := ""
	if !g.refOverridesSiblingsForSchema(posSch) && hasRefStructuralSiblings(posSch) && (posSch.EffectiveRef() != "" || posSch.DynamicRef != "") {
		_ = g.generateTypeDef(posName, posSch)
		if g.generated[posName] {
			return TupleItemDef{TypeName: posName}, true
		}
	}
	if ref := posSch.EffectiveRef(); ref != "" {
		if r := g.resolveRefInContext(ref, posSch); r != nil {
			resolved = r
			refName = g.goNameForResolvedRef(ref, resolved, refToGoName(ref))
		}
	}

	// Ensure the ref target type is generated. This is safe from infinite
	// recursion because the caller (generateTypeDef for an array) marks the
	// parent type as generated BEFORE calling buildTupleItemDefs.
	if refName != "" {
		_ = g.generateTypeDef(refName, resolved)
		if g.generated[refName] {
			return TupleItemDef{TypeName: refName}, true
		}
	}

	// Non-ref position schema: for schemas with structural keywords (type,
	// properties, etc.), generate a named type so positional validation works.
	//
	// The two format shapes are named here for the same reason, and because the
	// JSONType fallback below cannot say what either of them means. It would
	// answer "string" for {"type":["string","null"],"format":"ipv4"}, which
	// rejects the null the schema permits and says nothing about the format; and
	// nothing at all for a format with no type. Both have a generated type that
	// answers correctly, so the position delegates to it.
	//
	// A declared string with a format is here for the same reason. The JSONType
	// fallback answers "string" for it, which is true and says nothing about the
	// format, so {"prefixItems":[{"type":"string","format":"ipv4"}]} accepted an
	// IPv6 address at that position while the identical subschema written as a
	// property was checked. hasStructuralKeywords does not count `format` among
	// the keywords that need a named type, and is left alone: it is read by
	// nothing else here and widening it would change which schemas every other
	// caller materializes.
	if g.nullableFormatUnion(resolved) || g.stringAnnotationOnlySchema(resolved) || g.declaredFormatStringSchema(resolved) {
		_ = g.generateTypeDef(posName, resolved)
		if g.generated[posName] {
			return TupleItemDef{TypeName: posName}, true
		}
	}
	if hasStructuralKeywords(resolved) || g.objectShapeNeedsNamedType(resolved) {
		_ = g.generateTypeDef(posName, resolved)
		if g.generated[posName] {
			return TupleItemDef{TypeName: posName}, true
		}
	}

	// Simple type-only schema (no structural keywords) — record the JSON type
	// for lightweight runtime type checking.
	//
	// Only when that one type is the whole of what the position allows. The
	// check this produces names a single JSON kind and refuses every other, and
	// primarySchemaType answers "object" for {"type":["object","null"]} because
	// that is the Go type such a schema takes -- a *T, or a map, either of which
	// holds the null. The position has no pointer to hold it, so the answer was
	// read as "object and nothing else" and {"prefixItems":[{"type":["object",
	// "null"]}]} refused the null it permits. Falling through instead reaches
	// the wrapper below, which carries the whole type list.
	if jsonType := primarySchemaType(resolved); jsonType != "" && len(resolved.Type) == 1 {
		return TupleItemDef{JSONType: jsonType}, true
	}

	// A position whose schema constrains the value without naming a type -- a
	// bare "not", a bare if/then/else, a composition over unrelated shapes, a
	// union of two JSON types. hasStructuralKeywords counts none of those and the
	// JSONType arm above has nothing to say about a schema with no single type,
	// so the position rendered nothing at all and its keywords were enforced
	// nowhere. This is the tuple slot of issue #126, and it defers to the same
	// ladder the element, map-value and property positions do, so the four agree
	// on what a given subschema becomes.
	//
	// Last, after the arms above, for the reason every other position puts it
	// last: a schema one of them can type is typed by it, and this must not take
	// the position away from an answer that is already right.
	if g.constraintOnlyNamedType(resolved, posName) != nil {
		return TupleItemDef{TypeName: posName}, true
	}

	return TupleItemDef{}, false
}

// buildTupleItemDefs extracts per-position type definitions for tuple-form arrays
// (prefixItems in draft 2020-12, or items-as-array in draft 4-7).
// For each position, it resolves the schema (following $ref if needed),
// determines the Go type name, and records it for per-position validation.
//
// The list is returned at full tuple length whenever anything at all is worth
// checking -- including when only the *tail* is, since the tail's first index is
// read off this list's length. Positions that constrain nothing render nothing.
// Returns nil if the schema has no tuple items, or if neither a position nor the
// tail has anything to check.
func (g *Generator) buildTupleItemDefs(s *schema.Schema, parentName string) []TupleItemDef {
	positionSchemas := g.tuplePositionSchemas(s)
	if len(positionSchemas) == 0 {
		return nil
	}

	tupleItems := make([]TupleItemDef, 0, len(positionSchemas))
	hasValidatable := false
	for i, posSch := range positionSchemas {
		def, ok := g.tupleItemDefFor(posSch, fmt.Sprintf("%sItem%d", parentName, i))
		tupleItems = append(tupleItems, def)
		hasValidatable = hasValidatable || ok
	}

	if !hasValidatable && g.buildTupleTailDef(s, parentName) == nil {
		return nil
	}
	return tupleItems
}

// buildTupleTailDef builds the check every position past a tuple's prefix
// carries, or nil when there is none to make.
//
// A false tail is normally left to the length bound: extractValidationRules
// turns "items": false beside a tuple into an implicit maxItems of the tuple's
// length, which rejects the same documents with a clearer message. That
// inference only fires when the schema states no maxItems of its own, so a
// stated bound *wider* than the prefix used to disable the keyword outright --
// {"prefixItems":[a,b],"items":false,"maxItems":5} accepted a third element.
// That, and only that, is what the false tail covers.
func (g *Generator) buildTupleTailDef(s *schema.Schema, parentName string) *TupleItemDef {
	positionSchemas := g.tuplePositionSchemas(s)
	if len(positionSchemas) == 0 {
		return nil
	}
	tail := g.tupleTailSchema(s)
	if tail == nil || tail.IsTrueSchema() {
		return nil
	}
	if tail.IsFalseSchema() {
		if s.MaxItems != nil && s.MaxItems.Int() > len(positionSchemas) {
			return &TupleItemDef{IsFalse: true}
		}
		return nil
	}
	def, ok := g.tupleItemDefFor(tail, parentName+"Rest")
	if !ok {
		return nil
	}
	return &def
}

// sortedKeys returns the sorted keys of a map[string]*schema.Schema.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// hasStructuralKeywords returns true if the schema has keywords that would
// produce a meaningful Go type with validation (properties, type constraints,
// validation keywords, etc.). Used to decide whether an inline tuple position
// schema is worth generating as a named type.
//
// It answers only for the keywords listed below; an object whose shape is
// patternProperties or a schema-valued additionalProperties is recognised by
// objectShapeNeedsNamedType, which the caller consults alongside this. Without
// that the position fell through to the JSONType arm and got "expected object"
// and nothing else -- the tuple slot accepted any object at all.
func hasStructuralKeywords(s *schema.Schema) bool {
	if s == nil || s.IsBooleanSchema() {
		return false
	}
	// Object with properties
	if len(s.Properties) > 0 {
		return true
	}
	// Has required fields
	if len(s.Required) > 0 {
		return true
	}
	// Typed schema with validation keywords
	if len(s.Type) > 0 {
		// Check for string constraints
		if s.MinLength != nil || s.MaxLength != nil || (s.Pattern != nil && *s.Pattern != "") {
			return true
		}
		// Numeric constraints
		if s.Minimum != nil || s.Maximum != nil || s.MultipleOf != nil {
			return true
		}
		if s.ExclusiveMinimum != nil || s.ExclusiveMaximum != nil {
			return true
		}
		// Enum/const
		if len(s.Enum) > 0 || s.Const != nil {
			return true
		}
		// Object type with properties or composition
		if len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 {
			return true
		}
	}
	// Composition keywords at top level
	if len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 {
		return true
	}
	return false
}

// uniqueTypeName returns name if it is unclaimed or already claimed by s;
// otherwise it disambiguates — first by prefixing the owning document's name
// (element in field.json → FieldElement), then with a numeric suffix — so
// same-named definitions from different documents do not silently collapse
// onto a single generated type.
func (g *Generator) uniqueTypeName(name string, s *schema.Schema) string {
	claimed, ok := g.typeSchemas[name]
	if !ok || claimed == s {
		return name
	}
	if doc := documentGoName(s); doc != "" && !strings.HasPrefix(name, doc) {
		candidate := doc + name
		if claimed, ok := g.typeSchemas[candidate]; !ok || claimed == s {
			return candidate
		}
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s%d", name, i)
		if claimed, ok := g.typeSchemas[candidate]; !ok || claimed == s {
			return candidate
		}
	}
}

// documentGoName derives a Go name prefix from the document a schema node
// belongs to: the basename of its base URI without extension.
func documentGoName(s *schema.Schema) string {
	u := s.BaseURI
	if s.DocumentRoot != nil && s.DocumentRoot.BaseURI != nil {
		u = s.DocumentRoot.BaseURI
	}
	if u == nil {
		return ""
	}
	base := path.Base(u.Path)
	if base == "." || base == "/" || base == "" {
		return ""
	}
	base = strings.TrimSuffix(base, path.Ext(base))
	return SchemaNameToGoName(base)
}

// discriminatorCandidateProps returns the property names to consider as
// discriminators, taken from the variant's own properties or, for a
// properties-less nested oneOf, from its first sub-variant.
func (g *Generator) discriminatorCandidateProps(resolved *schema.Schema, depth int) []string {
	if resolved == nil || depth > maxDiscriminatorNesting {
		return nil
	}
	if len(resolved.Properties) > 0 {
		return sortedKeys(resolved.Properties)
	}
	if len(resolved.OneOf) > 0 {
		return g.discriminatorCandidateProps(g.resolveVariantSchema(resolved.OneOf[0]), depth+1)
	}
	return nil
}

const maxDiscriminatorNesting = 4

// discriminatorValuesForVariant returns every value propName may take when it
// selects the variant: the property must be required and constrained to
// string values by const or enum. A variant that is itself a oneOf without
// properties contributes the union of its sub-variants' values, and only
// discriminates when every sub-variant does (sub-variants sharing a value is
// fine — any of them still selects this variant).
func (g *Generator) discriminatorValuesForVariant(resolved *schema.Schema, propName string, depth int) []string {
	if resolved == nil || depth > maxDiscriminatorNesting {
		return nil
	}
	if len(resolved.Properties) > 0 {
		if !variantRequiresProperty(resolved, propName) {
			return nil
		}
		return extractDiscriminatorValueSet(resolved.Properties[propName])
	}
	if len(resolved.OneOf) == 0 {
		return nil
	}
	var union []string
	seen := make(map[string]bool)
	for _, sub := range resolved.OneOf {
		vals := g.discriminatorValuesForVariant(g.resolveVariantSchema(sub), propName, depth+1)
		if len(vals) == 0 {
			return nil
		}
		for _, val := range vals {
			if !seen[val] {
				seen[val] = true
				union = append(union, val)
			}
		}
	}
	return union
}

// extractDiscriminatorValueSet returns the string values a property schema
// pins via const or enum; nil when any value is not a string.
func extractDiscriminatorValueSet(propSchema *schema.Schema) []string {
	if propSchema == nil {
		return nil
	}
	if propSchema.Const != nil {
		if s, ok := (*propSchema.Const).(string); ok {
			return []string{s}
		}
		return nil
	}
	if len(propSchema.Enum) > 0 {
		vals := make([]string, 0, len(propSchema.Enum))
		for _, e := range propSchema.Enum {
			s, ok := e.(string)
			if !ok {
				return nil
			}
			vals = append(vals, s)
		}
		return vals
	}
	return nil
}

// GenerateOption customizes a single Generate call, overriding the
// corresponding Config value for that call only in spirit (the option is
// applied to the generator's config; per-call use is intended for
// shared-types generation where several schemas run through one Generator).
type GenerateOption func(*generateOptions)

type generateOptions struct {
	rootTypeName  string
	resolver      schema.SchemaResolver
	fieldNames    FieldNameMap
	fieldNamesSet bool
}

// WithRootTypeName overrides Config.RootTypeName for this Generate call.
func WithRootTypeName(name string) GenerateOption {
	return func(o *generateOptions) { o.rootTypeName = name }
}

// WithResolver overrides Config.Resolver for this Generate call.
func WithResolver(r schema.SchemaResolver) GenerateOption {
	return func(o *generateOptions) { o.resolver = r }
}

// WithFieldNames overrides Config.FieldNames for this Generate call, also
// when m is nil (clearing any previously applied override).
func WithFieldNames(m FieldNameMap) GenerateOption {
	return func(o *generateOptions) {
		o.fieldNames = m
		o.fieldNamesSet = true
	}
}

// foreignTypeFor returns a qualified reference when the resolved schema was
// generated by another package of a cross-package run. The referencing file
// gains an import for that package. Returns false when cross-package mode is
// off, the document belongs to this package, or the target has not been
// generated yet (the caller then materializes a local copy, so ordering
// packages dependency-first is what avoids duplication).
func (g *Generator) foreignTypeFor(resolved *schema.Schema) (*NamedType, bool) {
	reg := g.config.CrossPackage
	if reg == nil || resolved == nil {
		return nil, false
	}
	pkg := reg.packageFor(resolved)
	if pkg == "" || pkg == g.config.ImportPath {
		return nil, false
	}
	qt, ok := reg.lookup(resolved)
	if !ok || qt.ImportPath != pkg {
		// The document is owned by another package of this run, so the type
		// belongs there — but it was not registered by its owner (or was
		// claimed by a package that does not own it). Materializing a local
		// copy here would duplicate the type silently and leave consumers of
		// the two packages with incompatible Go types, so record it and let
		// Generate report it.
		g.crossPackageMisses[crossPackageMiss{Package: pkg, Document: documentIdentityOf(resolved)}] = true
		return nil, false
	}
	alias := g.importAlias(qt.ImportPath)
	return &NamedType{
		Name:               qt.Name,
		PkgAlias:           alias,
		foreignZeroLiteral: qt.ZeroLiteral,
		foreignValidatable: qt.Validatable,
		foreignZeroLossy:   qt.ZeroLossy,
	}, true
}

// importAlias returns (and records) the alias under which importPath is
// imported by the file being generated, deduplicating base-name conflicts
// with a numeric suffix.
func (g *Generator) importAlias(importPath string) string {
	if alias, ok := g.crossImports[importPath]; ok {
		return alias
	}
	base := PackageNameForImportPath(importPath)
	alias := base
	for i := 2; ; i++ {
		taken := reservedImportNames[alias]
		if !taken {
			for _, existing := range g.crossImports {
				if existing == alias {
					taken = true
					break
				}
			}
		}
		if !taken {
			break
		}
		alias = fmt.Sprintf("%s%d", base, i)
	}
	g.crossImports[importPath] = alias
	return alias
}

// reservedImportNames are the package names the generated file may import for
// its own use (see addRequiredImports and the emitter templates). A foreign
// package whose last path segment collides with one of these must be aliased:
// the two would otherwise be imported under the same name and the file would
// not compile. The set is fixed rather than derived from the import list
// because addRequiredImports runs after aliases have been assigned.
var reservedImportNames = map[string]bool{
	"big":               true, // math/big
	"bytes":             true,
	"ecma262":           true, // github.com/mgilbir/goecma262
	"flags":             true, // github.com/mgilbir/goecma262/flags
	"fmt":               true,
	"json":              true, // encoding/json
	"mail":              true, // net/mail
	"math":              true,
	"netip":             true, // net/netip
	"regexp":            true,
	"strings":           true,
	"time":              true,
	"url":               true, // net/url
	"utf8":              true, // unicode/utf8
	"validationruntime": true,
}
