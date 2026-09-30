package identity

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
)

// TestCycloneDXValidateBenchmark measures Validate over the CycloneDX 1.6
// example BOMs: time and allocations per BOM, and per pass over all of them. It
// is a measurement, not a check, so it runs only when asked for:
//
//	make bench-cyclonedx
//
// which is SCHEMAGEN_BENCH_CYCLONEDX=1 and this test. SCHEMAGEN_BENCH_COUNT sets
// how many times each benchmark runs (default 5), for benchstat.
func TestCycloneDXValidateBenchmark(t *testing.T) {
	if os.Getenv("SCHEMAGEN_BENCH_CYCLONEDX") == "" {
		t.Skip("a measurement; run it with make bench-cyclonedx")
	}
	root, boms := cycloneDXModule(t, map[string]string{"cdx/validate_bench_test.go": cycloneDXValidateBench})
	count := os.Getenv("SCHEMAGEN_BENCH_COUNT")
	if count == "" {
		count = "5"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	args := []string{"test", "-mod=mod", "-run", "^$", "-bench", ".", "-benchmem", "-count", count}
	// SCHEMAGEN_BENCH_FLAGS adds go test flags, split on spaces: -bench to pick
	// a benchmark, -memprofile with -o to find where the bytes go.
	args = append(args, strings.Fields(os.Getenv("SCHEMAGEN_BENCH_FLAGS"))...)
	cmd := testgo.Command(ctx, root, append(args, "./cdx")...)
	cmd.Env = append(cmd.Environ(), "CDX_BOMS="+strings.Join(boms, string(os.PathListSeparator)))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("benchmark: %v\n%s", err, out)
	}
	t.Logf("Validate over the %d CycloneDX 1.6 example BOMs:\n%s", len(boms), out)
}

// cycloneDXValidateBench is the benchmark itself, in the generated package: each
// BOM decoded once, then validated b.N times.
const cycloneDXValidateBench = `package cdx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadBOMs(b *testing.B) ([]string, []Bom) {
	var names []string
	var boms []Bom
	for _, path := range filepath.SplitList(os.Getenv("CDX_BOMS")) {
		in, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		var v Bom
		if err := json.Unmarshal(in, &v); err != nil {
			b.Fatalf("%s: %v", path, err)
		}
		if err := v.Validate(); err != nil {
			b.Fatalf("%s: %v", path, err)
		}
		names = append(names, strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "valid-"), "-1.6.json"))
		boms = append(boms, v)
	}
	return names, boms
}

// BenchmarkValidateAll validates every BOM once per iteration.
func BenchmarkValidateAll(b *testing.B) {
	_, boms := loadBOMs(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range boms {
			if err := boms[j].Validate(); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkValidate validates one BOM per iteration, one sub-benchmark a BOM.
func BenchmarkValidate(b *testing.B) {
	names, boms := loadBOMs(b)
	for j := range boms {
		bom := boms[j]
		b.Run(names[j], func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := bom.Validate(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
`
