package generator

import (
	"fmt"
	"math"
	"strconv"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// CountBound is the bound of a count keyword -- minLength and maxLength,
// minItems and maxItems, minProperties and maxProperties, minContains and
// maxContains -- in the two forms generated code needs it in, which are not
// the same form.
//
// Code compares a length against it. A count the schema wrote past int64 is
// held saturated (see schema.FlexInt), which keeps every verdict; but the
// generator's int is not the target's, and a literal that is an int here --
// 9223372036854775807, or 3000000000 -- is a constant that overflows int on a
// 32-bit target, so the package would not compile there. GoExpr writes a bound
// past int32 as an expression that is the smaller of the bound and the
// target's own MaxInt, which is a constant every target can hold and the same
// verdict on each, for the reason saturation is: no length reaches MaxInt.
//
// A message states it, and must state the number the schema wrote. String is
// the schema's literal for a saturated bound and the decimal otherwise, and it
// is what a template prints by default -- so the one place a template has to
// ask for the code form is where the bound is compared, and a code position
// that forgets to fails to compile rather than silently comparing against the
// wrong number: "len(s) < 1e19" is an overflowing constant.
type CountBound struct {
	// N is the bound as compared, saturated at the generator's int range.
	N int
	// Literal is the schema's own spelling of a saturated bound, and empty for
	// one N states exactly.
	Literal string
}

// countBound converts a parsed count keyword.
func countBound(f schema.FlexInt) CountBound {
	b := CountBound{N: f.Int()}
	if f.Saturated() {
		b.Literal = f.String()
	}
	return b
}

// countBoundPtr is countBound for an optional keyword.
func countBoundPtr(f *schema.FlexInt) *CountBound {
	if f == nil {
		return nil
	}
	b := countBound(*f)
	return &b
}

// String is the bound as a message states it.
func (b CountBound) String() string {
	if b.Literal != "" {
		return b.Literal
	}
	return strconv.Itoa(b.N)
}

// GoExpr is the bound as a Go constant expression of type int that compiles
// on every target. See CountBound.
func (b CountBound) GoExpr() string {
	if b.N >= math.MinInt32 && b.N <= math.MaxInt32 {
		return strconv.Itoa(b.N)
	}
	if b.N > 0 {
		return fmt.Sprintf("int(min(int64(%d), int64(^uint(0)>>1)))", b.N)
	}
	return fmt.Sprintf("int(max(int64(%d), -int64(^uint(0)>>1)-1))", b.N)
}
