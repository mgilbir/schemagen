package corpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
	"github.com/mgilbir/schemagen/tests/internal/testsupport"
)

// The generation benchmarks. They measure what a caller pays for Generate --
// the ledger it runs at the end included -- over the corpus and over one large
// real-world schema, so a change to the generator's cost shows up as a number:
//
//	go test ./tests/corpus -run '^$' -bench 'BenchmarkGenerate' -benchmem -count=10 > new.txt
//	benchstat old.txt new.txt
//
// Parsing and normalizing each document is outside the timer: it is the schema
// package's cost, and the same for every generator.

// benchDocument is one input: its bytes, and the directory its relative
// references resolve against.
type benchDocument struct {
	path string
	data []byte
}

func loadBenchDocuments(b *testing.B, root string, skipAdversarial bool) []benchDocument {
	b.Helper()
	var docs []benchDocument
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}
		if skipAdversarial && strings.Contains(filepath.ToSlash(path), "/adversarial/") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		docs = append(docs, benchDocument{path: abs, data: data})
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].path < docs[j].path })
	return docs
}

// generateBench generates one document, counting only Generate.
func generateBench(b *testing.B, doc benchDocument) bool {
	b.StopTimer()
	// What schema.LoadFromFile does, from bytes read once.
	var s schema.Schema
	if err := json.Unmarshal(doc.data, &s); err != nil {
		b.StartTimer()
		return false
	}
	if u, err := schema.FileURI(doc.path); err == nil {
		s.RetrievalURI = u
	}
	s.Normalize()
	g := generator.New(generator.Config{PackageName: "bench", OmitEmpty: true, Resolver: schema.NewFileResolver(filepath.Dir(doc.path))})
	b.StartTimer()
	_, err := g.Generate(&s)
	return err == nil
}

// BenchmarkGenerateCorpus generates every schema of testdata/schemas but the
// adversarial ones, which are there to be pathological rather than typical.
func BenchmarkGenerateCorpus(b *testing.B) {
	docs := loadBenchDocuments(b, testsupport.SchemaDir, true)
	if len(docs) < 300 {
		b.Fatalf("only %d corpus schemas found", len(docs))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, doc := range docs {
			generateBench(b, doc)
		}
	}
}

// BenchmarkGenerateCycloneDX generates the CycloneDX 1.6 BOM schema, vendored
// with the two documents it references under testdata/cyclonedx-1.6/schema
// (Apache-2.0; see the LICENSE and NOTICE there): real-world schema, the
// largest this repository generates.
func BenchmarkGenerateCycloneDX(b *testing.B) {
	path := testsupport.RepoPath("testdata", "cyclonedx-1.6", "schema", "bom-1.6.schema.json")
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		b.Fatal(err)
	}
	doc := benchDocument{path: abs, data: data}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !generateBench(b, doc) {
			b.Fatal("the CycloneDX schema did not generate")
		}
	}
}
