package tests

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// goldenTestCase defines a test case: schema input → expected golden output.
type goldenTestCase struct {
	Name       string
	SchemaPath string
	GoldenPath string
}

func allGoldenTests() []goldenTestCase {
	return []goldenTestCase{
		{"basic/simple_object", "testdata/schemas/basic/simple_object.json", "testdata/golden/basic/simple_object.go"},
		{"basic/nested_object", "testdata/schemas/basic/nested_object.json", "testdata/golden/basic/nested_object.go"},
		{"basic/primitive_types", "testdata/schemas/basic/primitive_types.json", "testdata/golden/basic/primitive_types.go"},
		{"basic/array_types", "testdata/schemas/basic/array_types.json", "testdata/golden/basic/array_types.go"},
		{"basic/additional_properties", "testdata/schemas/basic/additional_properties.json", "testdata/golden/basic/additional_properties.go"},
		{"basic/additional_properties_bool", "testdata/schemas/basic/additional_properties_bool.json", "testdata/golden/basic/additional_properties_bool.go"},
		{"refs/defs_ref", "testdata/schemas/refs/defs_ref.json", "testdata/golden/refs/defs_ref.go"},
		{"refs/definitions_ref", "testdata/schemas/refs/definitions_ref.json", "testdata/golden/refs/definitions_ref.go"},
		{"enum/string_enum", "testdata/schemas/enum/string_enum.json", "testdata/golden/enum/string_enum.go"},
		{"composition/allof_simple", "testdata/schemas/composition/allof_simple.json", "testdata/golden/composition/allof_simple.go"},
		{"composition/oneof_simple", "testdata/schemas/composition/oneof_simple.json", "testdata/golden/composition/oneof_simple.go"},
		{"composition/oneof_complex", "testdata/schemas/composition/oneof_complex.json", "testdata/golden/composition/oneof_complex.go"},
		{"composition/oneof_array_items", "testdata/schemas/composition/oneof_array_items.json", "testdata/golden/composition/oneof_array_items.go"},
		{"composition/anyof_simple", "testdata/schemas/composition/anyof_simple.json", "testdata/golden/composition/anyof_simple.go"},
		{"composition/oneof_with_null", "testdata/schemas/composition/oneof_with_null.json", "testdata/golden/composition/oneof_with_null.go"},
		{"validation/string_constraints", "testdata/schemas/validation/string_constraints.json", "testdata/golden/validation/string_constraints.go"},
		{"validation/numeric_constraints", "testdata/schemas/validation/numeric_constraints.json", "testdata/golden/validation/numeric_constraints.go"},
		{"formats/datetime", "testdata/schemas/formats/datetime.json", "testdata/golden/formats/datetime.go"},
		{"formats/all_formats", "testdata/schemas/formats/all_formats.json", "testdata/golden/formats/all_formats.go"},
		{"defaults/server_config", "testdata/schemas/defaults/server_config.json", "testdata/golden/defaults/server_config.go"},
		{"composition/oneof_discriminator", "testdata/schemas/composition/oneof_discriminator.json", "testdata/golden/composition/oneof_discriminator.go"},
		{"composition/oneof_discriminator_heuristic", "testdata/schemas/composition/oneof_discriminator_heuristic.json", "testdata/golden/composition/oneof_discriminator_heuristic.go"},
		{"validation/unevaluated_items", "testdata/schemas/validation/unevaluated_items.json", "testdata/golden/validation/unevaluated_items.go"},
		{"advanced/recursive_tree", "testdata/schemas/advanced/recursive_tree.json", "testdata/golden/advanced/recursive_tree.go"},
		{"advanced/pattern_properties", "testdata/schemas/advanced/pattern_properties.json", "testdata/golden/advanced/pattern_properties.go"},
		{"advanced/nullable_const", "testdata/schemas/advanced/nullable_const.json", "testdata/golden/advanced/nullable_const.go"},
		{"advanced/tuple_array", "testdata/schemas/advanced/tuple_array.json", "testdata/golden/advanced/tuple_array.go"},
		{"advanced/cross_refs", "testdata/schemas/advanced/cross_refs.json", "testdata/golden/advanced/cross_refs.go"},
		{"advanced/complex_tuple", "testdata/schemas/advanced/complex_tuple.json", "testdata/golden/advanced/complex_tuple.go"},
		{"validation/nested_errors", "testdata/schemas/validation/nested_errors.json", "testdata/golden/validation/nested_errors.go"},
		{"regression/allof_oneof_variants", "testdata/schemas/regression/allof_oneof_variants.json", "testdata/golden/regression/allof_oneof_variants.go"},
		{"regression/allof_oneof_crossed_types", "testdata/schemas/regression/allof_oneof_crossed_types.json", "testdata/golden/regression/allof_oneof_crossed_types.go"},
		{"regression/allof_if_then_branches", "testdata/schemas/regression/allof_if_then_branches.json", "testdata/golden/regression/allof_if_then_branches.go"},
		{"regression/nullable_array_items", "testdata/schemas/regression/nullable_array_items.json", "testdata/golden/regression/nullable_array_items.go"},
		{"regression/draft3_type_union", "testdata/schemas/regression/draft3_type_union.json", "testdata/golden/regression/draft3_type_union.go"},
		{"regression/draft3_type_multi", "testdata/schemas/regression/draft3_type_multi.json", "testdata/golden/regression/draft3_type_multi.go"},
		{"regression/property_count", "testdata/schemas/regression/property_count.json", "testdata/golden/regression/property_count.go"},
		{"regression/allof_tightest_constraints", "testdata/schemas/regression/allof_tightest_constraints.json", "testdata/golden/regression/allof_tightest_constraints.go"},
		{"regression/anyof_required_branches", "testdata/schemas/regression/anyof_required_branches.json", "testdata/golden/regression/anyof_required_branches.go"},
		{"regression/anyof_required_only", "testdata/schemas/regression/anyof_required_only.json", "testdata/golden/regression/anyof_required_only.go"},
		{"regression/validatable_field_fmt", "testdata/schemas/regression/validatable_field_fmt.json", "testdata/golden/regression/validatable_field_fmt.go"},
		{"regression/quoted_property_name", "testdata/schemas/regression/quoted_property_name.json", "testdata/golden/regression/quoted_property_name.go"},
		{"regression/optional_empty_array", "testdata/schemas/regression/optional_empty_array.json", "testdata/golden/regression/optional_empty_array.go"},
		{"regression/dynamicref_recursive", "testdata/schemas/regression/dynamicref_recursive.json", "testdata/golden/regression/dynamicref_recursive.go"},
		{"regression/oneof_optional_const", "testdata/schemas/regression/oneof_optional_const.json", "testdata/golden/regression/oneof_optional_const.go"},
		{"regression/integer_oneof_constraints", "testdata/schemas/regression/integer_oneof_constraints.json", "testdata/golden/regression/integer_oneof_constraints.go"},
		{"regression/oneof_required_only_object", "testdata/schemas/regression/oneof_required_only_object.json", "testdata/golden/regression/oneof_required_only_object.go"},
		{"regression/oneof_object_variant_constraints", "testdata/schemas/regression/oneof_object_variant_constraints.json", "testdata/golden/regression/oneof_object_variant_constraints.go"},
		{"regression/oneof_string_length_variants", "testdata/schemas/regression/oneof_string_length_variants.json", "testdata/golden/regression/oneof_string_length_variants.go"},
		{"regression/pp_pattern_ecma", "testdata/schemas/regression/pp_pattern_ecma.json", "testdata/golden/regression/pp_pattern_ecma.go"},
		{"regression/unevaluated_properties_pattern", "testdata/schemas/regression/unevaluated_properties_pattern.json", "testdata/golden/regression/unevaluated_properties_pattern.go"},
		{"regression/pp_type_list", "testdata/schemas/regression/pp_type_list.json", "testdata/golden/regression/pp_type_list.go"},
		// Pins which patternProperties buckets get a type of their own and which
		// keep the in-place scalar rules, and that a type minted for a bucket that
		// cannot carry a Validate is not left declared in the file.
		{"regression/pattern_value_subschemas", "testdata/schemas/regression/pattern_value_subschemas.json", "testdata/golden/regression/pattern_value_subschemas.go"},
		{"regression/field_name_collisions", "testdata/schemas/regression/field_name_collisions.json", "testdata/golden/regression/field_name_collisions.go"},
		{"regression/struct_reuse", "testdata/schemas/regression/struct_reuse.json", "testdata/golden/regression/struct_reuse.go"},
		{"regression/untyped_oneof_branches", "testdata/schemas/regression/untyped_oneof_branches.json", "testdata/golden/regression/untyped_oneof_branches.go"},
		{"regression/untyped_if_then", "testdata/schemas/regression/untyped_if_then.json", "testdata/golden/regression/untyped_if_then.go"},
		{"regression/unevaluated_items_anyof", "testdata/schemas/regression/unevaluated_items_anyof.json", "testdata/golden/regression/unevaluated_items_anyof.go"},
		{"regression/unevaluated_items_cousins", "testdata/schemas/regression/unevaluated_items_cousins.json", "testdata/golden/regression/unevaluated_items_cousins.go"},
		{"regression/array_item_constraints", "testdata/schemas/regression/array_item_constraints.json", "testdata/golden/regression/array_item_constraints.go"},
		{"regression/format_alias_positions", "testdata/schemas/regression/format_alias_positions.json", "testdata/golden/regression/format_alias_positions.go"},
		{"regression/format_alias_root", "testdata/schemas/regression/format_alias_root.json", "testdata/golden/regression/format_alias_root.go"},
		{"regression/format_map_values", "testdata/schemas/regression/format_map_values.json", "testdata/golden/regression/format_map_values.go"},
		{"regression/enum_alias_delegation", "testdata/schemas/regression/enum_alias_delegation.json", "testdata/golden/regression/enum_alias_delegation.go"},
		{"regression/format_alias_assertions", "testdata/schemas/regression/format_alias_assertions.json", "testdata/golden/regression/format_alias_assertions.go"},
		{"regression/allof_single_branch_type", "testdata/schemas/regression/allof_single_branch_type.json", "testdata/golden/regression/allof_single_branch_type.go"},
		{"regression/allof_inline_positions", "testdata/schemas/regression/allof_inline_positions.json", "testdata/golden/regression/allof_inline_positions.go"},
		{"regression/allof_bound_only", "testdata/schemas/regression/allof_bound_only.json", "testdata/golden/regression/allof_bound_only.go"},
		{"regression/explicit_null_positions", "testdata/schemas/regression/explicit_null_positions.json", "testdata/golden/regression/explicit_null_positions.go"},
		// The other half of the same distinction: where the schema permits the
		// null there is nothing to refuse, so it is recorded instead -- and the
		// tag is then free to say what it should about an absent property.
		{"regression/present_null_positions", "testdata/schemas/regression/present_null_positions.json", "testdata/golden/regression/present_null_positions.go"},
		// The content vocabulary in both postures. Draft 7 asserts it, 2019-09
		// and later annotate it, and the generated type is the same either way.
		{"regression/content_posture_draft7", "testdata/schemas/regression/content_posture_draft7.json", "testdata/golden/regression/content_posture_draft7.go"},
		{"regression/content_posture_2020", "testdata/schemas/regression/content_posture_2020.json", "testdata/golden/regression/content_posture_2020.go"},
		// Pins which allOf-of-overflow positions get the runtime evaluator and
		// which keep the typed overflow map the merge already produced.
		{"regression/allof_overflow_positions", "testdata/schemas/regression/allof_overflow_positions.json", "testdata/golden/regression/allof_overflow_positions.go"},
		// Pins which allOf branches get a per-branch overflow check and which do
		// not: the parent's own additionalProperties and the narrow merge keep
		// the overflow map they always had, and a branch stating no overflow
		// keyword gains nothing.
		{"regression/allof_branch_overflow", "testdata/schemas/regression/allof_branch_overflow.json", "testdata/golden/regression/allof_branch_overflow.go"},
		// Pins that an enum a branch stated over whole objects is carried by the
		// merged struct, and that a branch stating none leaves it alone.
		{"regression/allof_object_enum", "testdata/schemas/regression/allof_object_enum.json", "testdata/golden/regression/allof_object_enum.go"},
		// Pins that a root composition whose branches the static evaluator
		// refuses -- a boolean, a $ref, a nested composition, an enum -- is
		// compiled to the runtime evaluator instead of becoming `type X any`,
		// which carries no Validate at all.
		{"regression/root_composition_branches", "testdata/schemas/regression/root_composition_branches.json", "testdata/golden/regression/root_composition_branches.go"},
		// The same for a root "not" whose sub-schema states object structure.
		{"regression/root_not_object_shape", "testdata/schemas/regression/root_not_object_shape.json", "testdata/golden/regression/root_not_object_shape.go"},
		// The other side of it: a schema schemagen still cannot compile keeps
		// `any`, and says so in the generated source instead of passing for a
		// type that was never constrained.
		{"regression/unenforced_any", "testdata/schemas/regression/unenforced_any.json", "testdata/golden/regression/unenforced_any.go"},
		// Pins the two halves of naming a definition that compiles to the runtime
		// evaluator: an alias over it delegates its JSON both ways, and a bare
		// boolean definition beside it keeps the `any` its own paths are written
		// for rather than gaining a wrapper.
		{"regression/ref_to_runtime_wrapper", "testdata/schemas/regression/ref_to_runtime_wrapper.json", "testdata/golden/regression/ref_to_runtime_wrapper.go"},
		{"regression/boolean_defs_keep_any", "testdata/schemas/regression/boolean_defs_keep_any.json", "testdata/golden/regression/boolean_defs_keep_any.go"},
		// Pins that a boolean `false` reached through a $ref gets the same
		// forbidding wrapper the document root has always had, in every position
		// -- and that a $ref to boolean `true` still aliases to `any`.
		{"regression/ref_to_false_schema", "testdata/schemas/regression/ref_to_false_schema.json", "testdata/golden/regression/ref_to_false_schema.go"},
		{"regression/ref_to_false_root", "testdata/schemas/regression/ref_to_false_root.json", "testdata/golden/regression/ref_to_false_root.go"},
		// Pins the draft-3 spelling of a single dependency, a bare property name
		// where later drafts write a one-element array.
		{"regression/draft3_dependencies_string", "testdata/schemas/regression/draft3_dependencies_string.json", "testdata/golden/regression/draft3_dependencies_string.go"},
		// Pins that a $ref beside a "type" merges the two from 2019-09 on, and
		// the draft-07 half that still lets the $ref suppress its siblings.
		{"regression/ref_sibling_type", "testdata/schemas/regression/ref_sibling_type.json", "testdata/golden/regression/ref_sibling_type.go"},
		{"regression/ref_sibling_type_draft7", "testdata/schemas/regression/ref_sibling_type_draft7.json", "testdata/golden/regression/ref_sibling_type_draft7.go"},
		// Pins which allOf branch array keywords the merge adopts: a lone
		// branch's when nothing else describes the array's positions, and
		// neither the parent's own nor two branches' competing ones.
		{"regression/allof_branch_array_keywords", "testdata/schemas/regression/allof_branch_array_keywords.json", "testdata/golden/regression/allof_branch_array_keywords.go"},
		// An alias whose underlying resolves to a pointer carries no methods, so
		// its rules are emitted nowhere and must not pull in the packages they
		// would have used. The golden pins the import block; TestCompile then
		// proves the pinned file builds, which is what the emitted file did not
		// do -- "fmt" and "unicode/utf8" imported and not used.
		{"regression/nullable_scalar_bound", "testdata/schemas/regression/nullable_scalar_bound.json", "testdata/golden/regression/nullable_scalar_bound.go"},
		{"regression/nullable_format_positions", "testdata/schemas/regression/nullable_format_positions.json", "testdata/golden/regression/nullable_format_positions.go"},
		{"regression/untyped_format_positions", "testdata/schemas/regression/untyped_format_positions.json", "testdata/golden/regression/untyped_format_positions.go"},
		{"regression/format_beside_length", "testdata/schemas/regression/format_beside_length.json", "testdata/golden/regression/format_beside_length.go"},
		{"regression/typed_format_positions", "testdata/schemas/regression/typed_format_positions.json", "testdata/golden/regression/typed_format_positions.go"},
		{"regression/format_helper_positions", "testdata/schemas/regression/format_helper_positions.json", "testdata/golden/regression/format_helper_positions.go"},
		// Every annotation-vocabulary keyword on every named-type kind. The
		// golden pins the paragraph layout the keywords are written in, which is
		// the part that decides whether gopls, staticcheck and `go doc` see a
		// "Deprecated: " notice at all; TestAnnotationKeywordsReachEveryNamedTypeKind
		// names the cells. It is also what compiles the runtime-annotation kind
		// as a $defs entry, which used to be emitted three times over.
		{"regression/annotation_positions", "testdata/schemas/regression/annotation_positions.json", "testdata/golden/regression/annotation_positions.go"},
		// How far each of those keywords reaches through an applicator. The
		// golden is what pins the two halves together in one file: the
		// SetDefaults body, which is where "default" is consumed, and the field
		// comments, which is where the other five are, so a change that moved
		// one reading without the other shows up as a diff in the same
		// function. TestAnnotationReachThroughApplicators names the cells.
		{"regression/annotation_reach_positions", "testdata/schemas/regression/annotation_reach_positions.json", "testdata/golden/regression/annotation_reach_positions.go"},
		// Pins which oneOf groups keep the sealed-interface union and which
		// leave it for the evaluator: a branch selection would count wrongly --
		// a `false`, a `const`, an enum beside a `type` -- takes the group away,
		// while the all-object, all-scalar and `true`-branch groups beside them
		// keep the union they already select correctly on.
		{"regression/oneof_boolean_and_const_branches", "testdata/schemas/regression/oneof_boolean_and_const_branches.json", "testdata/golden/regression/oneof_boolean_and_const_branches.go"},
		{"regression/oneof_boolean_and_const_root", "testdata/schemas/regression/oneof_boolean_and_const_root.json", "testdata/golden/regression/oneof_boolean_and_const_root.go"},
		// A root union that does select correctly keeps its variants, and loses
		// only the decode into the struct that refused every scalar first.
		{"regression/oneof_root_scalar_branch", "testdata/schemas/regression/oneof_root_scalar_branch.json", "testdata/golden/regression/oneof_root_scalar_branch.go"},
		// Pins that a property whose schema is `false` is refused on the
		// document's own keys rather than on the decoded field being non-nil.
		{"regression/false_property_explicit_null", "testdata/schemas/regression/false_property_explicit_null.json", "testdata/golden/regression/false_property_explicit_null.go"},
		{"regression/anyof_branch_unevaluated_properties", "testdata/schemas/regression/anyof_branch_unevaluated_properties.json", "testdata/golden/regression/anyof_branch_unevaluated_properties.go"},
		{"regression/anyof_branch_unevaluated_no_properties", "testdata/schemas/regression/anyof_branch_unevaluated_no_properties.json", "testdata/golden/regression/anyof_branch_unevaluated_no_properties.go"},
		{"regression/oneof_branch_unevaluated_properties", "testdata/schemas/regression/oneof_branch_unevaluated_properties.json", "testdata/golden/regression/oneof_branch_unevaluated_properties.go"},
		{"regression/constraint_only_positions", "testdata/schemas/regression/constraint_only_positions.json", "testdata/golden/regression/constraint_only_positions.go"},
		{"regression/overflow_map_untyped_value", "testdata/schemas/regression/overflow_map_untyped_value.json", "testdata/golden/regression/overflow_map_untyped_value.go"},
		{"regression/allof_nested_anyof_unevaluated", "testdata/schemas/regression/allof_nested_anyof_unevaluated.json", "testdata/golden/regression/allof_nested_anyof_unevaluated.go"},
		{"regression/allof_nested_oneof_unevaluated", "testdata/schemas/regression/allof_nested_oneof_unevaluated.json", "testdata/golden/regression/allof_nested_oneof_unevaluated.go"},
		{"regression/allof_nested_anyof_unevaluated_items", "testdata/schemas/regression/allof_nested_anyof_unevaluated_items.json", "testdata/golden/regression/allof_nested_anyof_unevaluated_items.go"},
		// The API promise of issue #139, which is the reason it has a golden at
		// all: which inline positions trade a convenient Go type for the wrapper
		// that keeps a value of any kind. A property and an element whose
		// sub-schema states no type take it; the one that states a type keeps
		// float64, and the prefixItems slot beside them keeps the raw wrapper it
		// already had.
		{"regression/inline_untyped_positions", "testdata/schemas/regression/inline_untyped_positions.json", "testdata/golden/regression/inline_untyped_positions.go"},
		// The API promise of issue #142, and the reason that one has a golden
		// too: an element and a map value whose sub-schema admits nothing stop
		// being []any and map[string]any and become the wrapper that refuses
		// every value, which is the type the same sub-schema has always had
		// behind a $ref. The slot, branch and keyword positions beside them
		// change no type at all -- they gain the check they were missing -- and
		// this is where the difference is pinned.
		{"regression/inline_forbidding_positions", "testdata/schemas/regression/inline_forbidding_positions.json", "testdata/golden/regression/inline_forbidding_positions.go"},
		// The API promise of issue #146, which is the one the six keywords do not
		// have: they gain a check and change no type, so the difference this
		// golden pins is the inline object. A propertyless object that constrains
		// its shape stops being map[string]any and becomes a struct of its own --
		// the type the same schema has always had behind a $ref -- which is what
		// gives propertyNames and dependentSchemas somewhere to live inline, and
		// carries required, minProperties, maxProperties and dependentRequired
		// with them. See constrainsObjectShape.
		{"regression/forbidding_subschema_spellings", "testdata/schemas/regression/forbidding_subschema_spellings.json", "testdata/golden/regression/forbidding_subschema_spellings.go"},
		// The API promise of issue #145. A schema whose declared type forbids
		// every member it lists admits nothing, so it takes the same wrapper
		// #142 gave `false` and the empty enum -- and a schema whose type forbids
		// only some of them keeps the members it admits, so a raw enum listing
		// "a" and 5 becomes a string enum listing "a". Both are type changes a
		// caller sees, and the untyped enum beside them keeps the raw form,
		// which is what says the filter reads a stated type and not an inferred
		// one.
		{"regression/enum_outside_declared_type", "testdata/schemas/regression/enum_outside_declared_type.json", "testdata/golden/regression/enum_outside_declared_type.go"},
		// Pins which anyOf groups keep the merged struct and which leave it for
		// the evaluator: a branch the struct cannot hold -- a scalar, a const, a
		// `not`, the `true` schema -- takes the group away, a `false` branch
		// keeps the struct and gains the runtime applicator instead, and the
		// all-object group beside them keeps the summary it already judges
		// correctly.
		{"regression/anyof_boolean_and_scalar_branches", "testdata/schemas/regression/anyof_boolean_and_scalar_branches.json", "testdata/golden/regression/anyof_boolean_and_scalar_branches.go"},
		{"regression/anyof_scalar_branch_root", "testdata/schemas/regression/anyof_scalar_branch_root.json", "testdata/golden/regression/anyof_scalar_branch_root.go"},
		{"regression/anyof_false_branch_root", "testdata/schemas/regression/anyof_false_branch_root.json", "testdata/golden/regression/anyof_false_branch_root.go"},
		// Pins that an if/then/else with a boolean `if` or a boolean branch is
		// given a name to live in wherever it is written, not only at a root.
		{"regression/if_boolean_branch_positions", "testdata/schemas/regression/if_boolean_branch_positions.json", "testdata/golden/regression/if_boolean_branch_positions.go"},
		// Issue #150, the two decision points where the composition machinery
		// declined to name a type for a variant. The first pins which
		// single-branch groups leave the sealed-interface union and which keep
		// it; the second and third pin which {X, "null"} collapses keep the
		// pointer at X's type and which are read by the evaluator instead. The
		// narrowness is the point of pinning them: a wider reading takes the
		// pointer away from every nullable property in the corpus.
		{"regression/oneof_single_branch_positions", "testdata/schemas/regression/oneof_single_branch_positions.json", "testdata/golden/regression/oneof_single_branch_positions.go"},
		{"regression/nullable_composition_branches", "testdata/schemas/regression/nullable_composition_branches.json", "testdata/golden/regression/nullable_composition_branches.go"},
		{"regression/nullable_anyof_named_branch", "testdata/schemas/regression/nullable_anyof_named_branch.json", "testdata/golden/regression/nullable_anyof_named_branch.go"},
		// Issue #151, both sides of the draft split: through draft 7 the enum
		// arms stand behind the ref arms and the sibling names no type at all,
		// from 2019-09 on they run first and it does.
		{"regression/ref_sibling_values_draft7", "testdata/schemas/regression/ref_sibling_values_draft7.json", "testdata/golden/regression/ref_sibling_values_draft7.go"},
		{"regression/ref_sibling_values_2020", "testdata/schemas/regression/ref_sibling_values_2020.json", "testdata/golden/regression/ref_sibling_values_2020.go"},
		// Issue #153, the same split read the other way: from 2019-09 on both
		// halves bind, and the target here states something the sibling does not
		// so that a type carrying only the sibling can be seen doing it.
		{"regression/ref_sibling_target_2020", "testdata/schemas/regression/ref_sibling_target_2020.json", "testdata/golden/regression/ref_sibling_target_2020.go"},
		// Issue #154: the two keyword spellings a re-marshaled schema does not
		// show, beside the spellings it does, at each gate that reads one.
		{"regression/hidden_keyword_spellings", "testdata/schemas/regression/hidden_keyword_spellings.json", "testdata/golden/regression/hidden_keyword_spellings.go"},
		// The two things a schema needs when it is not a finite tree, in one
		// file: node variables assigned in init() because the schema contains
		// itself, and the resource frames a $recursiveRef is resolved against.
		// The roundtrip tests say what the code decides; this says what it looks
		// like, which is where a frame published by the wrong resource shows up.
		{"regression/recursive_ref_outermost_anchor", "testdata/schemas/regression/recursive_ref_outermost_anchor.json", "testdata/golden/regression/recursive_ref_outermost_anchor.go"},
		// Issue #166, in the two smallest shapes that show what the fix emits
		// rather than what it decides: the wrapper for an inferred array now
		// carries a type for its element and delegates to that type's Validate,
		// where it used to inline a test naming the element's JSON type and
		// nothing else. The draft-7 file adds the tuple spelling, where the
		// same delegation appears once per position and once for the tail --
		// the arm no 2020-12 document reaches. The positions fixture the
		// roundtrip tests use is deliberately *not* here: 28 properties of it
		// would pin a great deal that has nothing to do with the element.
		{"regression/inferred_array_root", "testdata/schemas/regression/inferred_array_root.json", "testdata/golden/regression/inferred_array_root.go"},
		{"regression/inferred_array_tuple_draft7", "testdata/schemas/regression/inferred_array_tuple_draft7.json", "testdata/golden/regression/inferred_array_tuple_draft7.go"},
	}
}

func TestGoldenFiles(t *testing.T) {
	for _, tc := range allGoldenTests() {
		t.Run(tc.Name, func(t *testing.T) {
			got := generateFromSchema(t, tc.SchemaPath)

			goldenPath := filepath.Join("..", tc.GoldenPath)
			if os.Getenv("UPDATE_GOLDEN") == "true" {
				dir := filepath.Dir(goldenPath)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("creating golden dir: %v", err)
				}
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatalf("updating golden file: %v", err)
				}
				t.Logf("Updated golden file: %s", goldenPath)
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("reading golden file %s: %v\nRun with UPDATE_GOLDEN=true to create it", goldenPath, err)
			}

			if string(got) != string(want) {
				t.Errorf("generated output differs from golden file %s", tc.GoldenPath)
				// Show a simple diff
				gotLines := strings.Split(string(got), "\n")
				wantLines := strings.Split(string(want), "\n")
				maxLines := len(gotLines)
				if len(wantLines) > maxLines {
					maxLines = len(wantLines)
				}
				for i := 0; i < maxLines; i++ {
					var gotLine, wantLine string
					if i < len(gotLines) {
						gotLine = gotLines[i]
					}
					if i < len(wantLines) {
						wantLine = wantLines[i]
					}
					if gotLine != wantLine {
						t.Errorf("  line %d:\n    got:  %q\n    want: %q", i+1, gotLine, wantLine)
					}
				}
			}
		})
	}
}

// generateFromSchema runs the full pipeline: load → normalize → generate → emit.
func generateFromSchema(t *testing.T, schemaPath string) []byte {
	t.Helper()
	return generateFromSchemaWithConfig(t, schemaPath, generator.Config{
		PackageName: "testpkg",
		OmitEmpty:   true,
	})
}

// generateFromSchemaWithConfig runs the pipeline with a custom generator config.
func generateFromSchemaWithConfig(t *testing.T, schemaPath string, cfg generator.Config) []byte {
	t.Helper()

	fullPath := filepath.Join("..", schemaPath)
	s, err := schema.LoadFromFile(fullPath)
	if err != nil {
		t.Fatalf("loading schema %s: %v", schemaPath, err)
	}

	// The draft the config names is normalized under too, exactly as cmd/schemagen
	// does it: normalization is where a keyword the dialect does not define is
	// dropped, and a draft answered from two sources is issue #203 in miniature.
	// DraftUnknown means "read it from the document", which is what every fixture
	// that names no draft gets.
	s.NormalizeForDraft(cfg.Draft)

	gen := generator.New(cfg)
	ir, err := gen.Generate(s)
	if err != nil {
		t.Fatalf("generating IR for %s: %v", schemaPath, err)
	}

	em, err := emitter.New()
	if err != nil {
		t.Fatalf("creating emitter: %v", err)
	}

	src, err := em.Emit(ir)
	if err != nil {
		t.Fatalf("emitting code for %s: %v", schemaPath, err)
	}

	return src
}

// TestGoldenBigInt tests golden output with --big-int enabled.
func TestGoldenBigInt(t *testing.T) {
	tests := []goldenTestCase{
		{"bigint/integer_constraints", "testdata/schemas/bigint/integer_constraints.json", "testdata/golden/bigint/integer_constraints.go"},
		// The big-int alias is the one named-type kind that needs a non-default
		// configuration to exist at all -- under the default the same $defs entry
		// comes out a plain int64 alias. It is the same
		// position-matrix document read twice, so the two goldens differ in that
		// kind and nothing else.
		{"bigint/annotation_positions", "testdata/schemas/regression/annotation_positions.json", "testdata/golden/bigint/annotation_positions.go"},
	}
	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			got := generateFromSchemaWithConfig(t, tc.SchemaPath, generator.Config{
				PackageName:   "testpkg",
				OmitEmpty:     true,
				BigIntSupport: true,
			})

			goldenPath := filepath.Join("..", tc.GoldenPath)
			if os.Getenv("UPDATE_GOLDEN") == "true" {
				dir := filepath.Dir(goldenPath)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("creating golden dir: %v", err)
				}
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatalf("updating golden file: %v", err)
				}
				t.Logf("Updated golden file: %s", goldenPath)
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("reading golden file %s: %v\nRun with UPDATE_GOLDEN=true to create it", goldenPath, err)
			}
			if string(got) != string(want) {
				t.Errorf("generated output differs from golden file %s", tc.GoldenPath)
				gotLines := strings.Split(string(got), "\n")
				wantLines := strings.Split(string(want), "\n")
				for i := range gotLines {
					if i >= len(wantLines) {
						t.Logf("  line %d:\n\tgot:  %q\n\twant: %q", i+1, gotLines[i], "")
						continue
					}
					if gotLines[i] != wantLines[i] {
						t.Logf("  line %d:\n\tgot:  %q\n\twant: %q", i+1, gotLines[i], wantLines[i])
					}
				}
			}
		})
	}
}

// TestBigIntRoundTrip tests marshal/unmarshal round-trip for BigInt types
// using various values: small int, large int (overflow int64), boundary values.
func TestBigIntRoundTrip(t *testing.T) {
	schemaPath := "testdata/schemas/bigint/integer_constraints.json"
	generated := generateFromSchemaWithConfig(t, schemaPath, generator.Config{
		PackageName:   "testpkg",
		OmitEmpty:     true,
		BigIntSupport: true,
	})

	tmpDir := t.TempDir()

	generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}
	writeSharedHelpers(t, tmpDir, generatedMain)

	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	testCases := []struct {
		name  string
		input string
	}{
		{"zero", "0"},
		{"small_int", "42"},
		{"max_int64", "9223372036854775807"},
		{"overflow_int64", "9223372036854775808"},
		{"large_bigint", "123456789012345678901234567890"},
		{"just_under_max", "999999999999999999999999999999"},
	}

	var errs []string
	for _, tc := range testCases {
		var c Counter
		if err := json.Unmarshal([]byte(tc.input), &c); err != nil {
			errs = append(errs, fmt.Sprintf("%s: unmarshal failed: %v", tc.name, err))
			continue
		}

		// Validate
		if err := c.Validate(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: validate failed: %v", tc.name, err))
			continue
		}

		// Marshal back
		out, err := json.Marshal(c)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: marshal failed: %v", tc.name, err))
			continue
		}

		// Compare: the marshaled value should equal the input
		if string(out) != tc.input {
			errs = append(errs, fmt.Sprintf("%s: round-trip mismatch: got %s, want %s", tc.name, string(out), tc.input))
		}
	}

	// Test validation failures (exclusiveMaximum = 1e30, minimum = 0)
	invalidCases := []struct {
		name  string
		input string
	}{
		{"negative", "-1"},
		{"at_exclusive_max", "1000000000000000000000000000000"},
		{"over_max", "1000000000000000000000000000001"},
	}

	for _, tc := range invalidCases {
		var c Counter
		if err := json.Unmarshal([]byte(tc.input), &c); err != nil {
			errs = append(errs, fmt.Sprintf("%s: unmarshal should succeed: %v", tc.name, err))
			continue
		}
		if err := c.Validate(); err == nil {
			errs = append(errs, fmt.Sprintf("%s: expected validation error but got nil", tc.name))
		}
	}

	// Test invalid types
	invalidTypes := []struct {
		name  string
		input string
	}{
		{"null", "null"},
		{"string", "\"42\""},
		{"float", "3.14"},
	}

	for _, tc := range invalidTypes {
		var c Counter
		if err := json.Unmarshal([]byte(tc.input), &c); err == nil {
			// For float, check if it was accepted (it shouldn't be since 3.14 has fractional part)
			if tc.name == "float" {
				// 3.14 should fail because it's not an integer
				errs = append(errs, fmt.Sprintf("%s: expected unmarshal error for non-integer float", tc.name))
			}
		}
	}

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "FAIL: %s\n", e)
		}
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}

	if err := writeTestGoMod(tmpDir, "bigint_roundtrip_test"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("BigInt round-trip test failed:\n%s\nerror: %v", string(output), err)
	}

	outputStr := programOutput(output)
	if outputStr != "PASS" {
		t.Fatalf("BigInt round-trip test output:\n%s", outputStr)
	}
}

// TestValidationErrorPaths verifies that nested validation errors include the full JSON path.
func TestValidationErrorPaths(t *testing.T) {
	schemaPath := "testdata/schemas/validation/nested_errors.json"
	generated := generateFromSchema(t, schemaPath)

	tmpDir := t.TempDir()

	generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}
	writeSharedHelpers(t, tmpDir, generatedMain)

	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	testCases := []struct {
		name        string
		input       string
		wantErr     string
	}{
		{
			name:    "nested_object_field",
			input:   ` + "`" + `{"name":"Acme","address":{"street":"Main","city":"A"}}` + "`" + `,
			wantErr: "address.city:",
		},
		{
			name:    "nested_object_pattern",
			input:   ` + "`" + `{"name":"Acme","address":{"street":"Main","city":"NY","zip":"bad"}}` + "`" + `,
			wantErr: "address.zip:",
		},
		{
			name:    "array_element_field",
			input:   ` + "`" + `{"name":"Acme","address":{"street":"Main","city":"NY"},"employees":[{"name":"Alice","age":200}]}` + "`" + `,
			wantErr: "employees[0].age:",
		},
		{
			name:    "array_second_element",
			input:   ` + "`" + `{"name":"Acme","address":{"street":"Main","city":"NY"},"employees":[{"name":"Alice","age":25},{"name":"","age":30}]}` + "`" + `,
			wantErr: "employees[1].name:",
		},
		{
			name:    "root_level_field",
			input:   ` + "`" + `{"name":"","address":{"street":"Main","city":"NY"}}` + "`" + `,
			wantErr: "name:",
		},
		{
			// A required property missing from a nested object must be reported
			// with the parent's path, as a validation error (not a parse error).
			name:    "nested_missing_required",
			input:   ` + "`" + `{"name":"Acme","address":{"city":"NY"}}` + "`" + `,
			wantErr: "address.street: required property is missing",
		},
		{
			// A required property missing from an array element carries the index.
			name:    "array_element_missing_required",
			input:   ` + "`" + `{"name":"Acme","address":{"street":"Main","city":"NY"},"employees":[{"age":25}]}` + "`" + `,
			wantErr: "employees[0].name: required property is missing",
		},
		{
			// A required property missing at the root is reported without a prefix.
			name:    "root_missing_required",
			input:   ` + "`" + `{"name":"Acme"}` + "`" + `,
			wantErr: "address: required property is missing",
		},
	}

	var errs []string
	for _, tc := range testCases {
		var c Company
		if err := json.Unmarshal([]byte(tc.input), &c); err != nil {
			errs = append(errs, fmt.Sprintf("%s: unmarshal failed: %v", tc.name, err))
			continue
		}
		err := c.Validate()
		if err == nil {
			errs = append(errs, fmt.Sprintf("%s: expected validation error but got nil", tc.name))
			continue
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			errs = append(errs, fmt.Sprintf("%s: error %q does not contain %q", tc.name, err.Error(), tc.wantErr))
		}
	}

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "FAIL: %s\n", e)
		}
		os.Exit(1)
	}
	fmt.Println("PASS")
}

`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}

	if err := writeTestGoMod(tmpDir, "error_path_test"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("validation error path test failed:\n%s\nerror: %v", string(output), err)
	}

	outputStr := programOutput(output)
	if outputStr != "PASS" {
		t.Fatalf("validation error path test output:\n%s", outputStr)
	}
}

func TestNestedRemoteItemsValidation(t *testing.T) {
	input := `{
		"id": "http://localhost:1234/",
		"items": {
			"id": "baseUriChange/",
			"items": {"$ref": "folderInteger.json"}
		}
	}`
	var s schema.Schema
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	s.Normalize()
	base, err := url.Parse("http://localhost:1234/")
	if err != nil {
		t.Fatalf("parse base uri: %v", err)
	}
	s.ComputeBaseURIs(base, &s)
	remote := &schema.Schema{Type: schema.TypeList{"integer"}}
	gen := generator.New(generator.Config{
		PackageName: "testpkg",
		OmitEmpty:   true,
		Draft:       schema.Draft03,
		Resolver: schema.NewMappingResolver(map[string]*schema.Schema{
			"http://localhost:1234/baseUriChange/folderInteger.json": remote,
		}),
	})
	ir, err := gen.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// The outer array delegates one element to the type generated for the inner
	// array, whose own Validate carries the remote integer. That replaced the
	// ItemsNested this used to assert, which could only repeat the inner
	// element's declared type and dropped every other keyword beside it (#166).
	aliases := map[string]*generator.InferredAliasDef{}
	for _, td := range ir.TypeDefs {
		if alias, ok := td.(*generator.InferredAliasDef); ok {
			aliases[alias.Name] = alias
		}
	}
	root := aliases["Root"]
	if root == nil {
		t.Fatalf("no root InferredAliasDef in IR")
	}
	if root.ItemsTypeName == "" {
		t.Fatalf("root IR missing nested item validation: %#v", root)
	}
	inner := aliases[root.ItemsTypeName]
	if inner == nil {
		t.Fatalf("root delegates to %q, which is no inferred array", root.ItemsTypeName)
	}
	if inner.ItemsType != "integer" && inner.ItemsTypeName == "" {
		t.Fatalf("nested element checks nothing: %#v", inner)
	}
	em, err := emitter.New()
	if err != nil {
		t.Fatalf("emitter: %v", err)
	}
	generated, err := em.Emit(ir)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(string(generated), "var _typed "+root.ItemsTypeName) {
		t.Fatalf("generated code missing nested item validation:\n%s", string(generated))
	}

	tmpDir := t.TempDir()
	generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
		t.Fatalf("writing types.go: %v", err)
	}
	writeSharedHelpers(t, tmpDir, generatedMain)

	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	valid := []byte(` + "`" + `[[1]]` + "`" + `)
	var validObj Root
	if err := json.Unmarshal(valid, &validObj); err != nil {
		fmt.Fprintf(os.Stderr, "valid unmarshal: %v\n", err)
		os.Exit(1)
	}
	if err := validObj.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "valid validate: %v\n", err)
		os.Exit(1)
	}

	invalid := []byte(` + "`" + `[["a"]]` + "`" + `)
	var invalidObj Root
	if err := json.Unmarshal(invalid, &invalidObj); err != nil {
		fmt.Fprintf(os.Stderr, "invalid unmarshal should succeed: %v\n", err)
		os.Exit(1)
	}
	if err := invalidObj.Validate(); err == nil {
		fmt.Fprintf(os.Stderr, "invalid validate: expected error\n")
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}
	if err := writeTestGoMod(tmpDir, "nested_remote_items_test"); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("nested remote items test failed:\n%s\nerror: %v", string(output), err)
	}
	if programOutput(output) != "PASS" {
		t.Fatalf("nested remote items output:\n%s", string(output))
	}
}
