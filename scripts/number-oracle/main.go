// Command number-oracle freezes the verdicts tests/number_verdict_grid_test.go
// holds the generated code to.
//
// The grid puts numeric keywords to the instances that separate an exact
// reading of a JSON number from an approximate one, and needs an answer for
// each that does not come from schemagen. This computes it twice over:
//
//   - santhosh-tekuri/jsonschema v6.0.2, run in process on the exact text of
//     every schema and instance. It decodes with UseNumber and does its
//     arithmetic in big.Rat, which is what the specification describes: the
//     numeric keywords are defined over numbers as mathematical values. This
//     is the verdict the grid expects.
//
//   - Bowtie (https://bowtie.report), which runs the same library as
//     go-jsonschema and, beside it, python-jsonschema and ajv, in containers.
//     Bowtie's own harness is Python and reads every instance with Python's
//     json, which holds a non-integer as a float64 -- so only the pairs whose
//     every number survives that (an integer, or a decimal that is exactly its
//     own float64's shortest spelling) are sent. For those, go-jsonschema run
//     through Bowtie must give the verdict it gave in process, or this refuses
//     to write anything; python-jsonschema's and ajv's verdicts are recorded
//     beside it. Both work in binary floating point, and the pairs where they
//     part from the exact verdict are the precision cases the grid exists for.
//
// It is a module of its own, so the validator never reaches the repository's
// go.mod, and it needs docker and uv only for the Bowtie half (-no-bowtie
// skips it). Run from the repository root:
//
//	SCHEMAGEN_NUMBER_GRID_DUMP=/tmp/pairs.json go test ./tests/numbers -run TestNumberVerdictGrid
//	(cd scripts/number-oracle && go run . -pairs /tmp/pairs.json -out ../../testdata/number_oracle/verdicts.json)
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type entry struct {
	Schema string          `json:"schema"`
	Doc    string          `json:"instance"`
	Valid  bool            `json:"valid"`
	Bowtie map[string]bool `json:"bowtie,omitempty"`
}

var implementations = []string{"go-jsonschema", "python-jsonschema", "js-ajv"}

func main() {
	pairsPath := flag.String("pairs", "", "the pairs the grid dumped")
	outPath := flag.String("out", "", "where to write the verdicts")
	noBowtie := flag.Bool("no-bowtie", false, "skip the Bowtie cross-check")
	flag.Parse()
	if *pairsPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "usage: number-oracle -pairs pairs.json -out verdicts.json")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*pairsPath)
	must(err)
	var entries []entry
	must(json.Unmarshal(raw, &entries))

	compiled := map[string]*jsonschema.Schema{}
	for i := range entries {
		e := &entries[i]
		sch, ok := compiled[e.Schema]
		if !ok {
			sch, err = compile(e.Schema)
			if err != nil {
				fail("compiling %s: %v", e.Schema, err)
			}
			compiled[e.Schema] = sch
		}
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(e.Doc))
		if err != nil {
			fail("reading %s: %v", e.Doc, err)
		}
		e.Valid = sch.Validate(inst) == nil
	}

	if !*noBowtie {
		crossCheck(entries)
		draft4Integers(entries)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Schema != entries[j].Schema {
			return entries[i].Schema < entries[j].Schema
		}
		return entries[i].Doc < entries[j].Doc
	})
	out, err := json.MarshalIndent(entries, "", " ")
	must(err)
	must(os.WriteFile(*outPath, append(out, '\n'), 0o644))
	fmt.Fprintf(os.Stderr, "wrote %d verdicts to %s\n", len(entries), *outPath)
}

func compile(schemaText string) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schemaText))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("mem://grid/schema.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("mem://grid/schema.json")
}

// crossCheck runs every transportable pair through Bowtie, one run per
// dialect, and records each implementation's verdict.
func crossCheck(entries []entry) {
	byDialect := map[string][]int{}
	for i, e := range entries {
		if !transportable(e.Schema) || !transportable(e.Doc) {
			continue
		}
		byDialect[dialectOf(e.Schema)] = append(byDialect[dialectOf(e.Schema)], i)
	}
	sent, disagreed := 0, 0
	dialects := make([]string, 0, len(byDialect))
	for d := range byDialect {
		dialects = append(dialects, d)
	}
	sort.Strings(dialects)
	for _, dialect := range dialects {
		idxs := byDialect[dialect]
		// One case per schema, holding every instance sent against it.
		var order []string
		cases := map[string][]int{}
		for _, i := range idxs {
			s := entries[i].Schema
			if _, ok := cases[s]; !ok {
				order = append(order, s)
			}
			cases[s] = append(cases[s], i)
		}
		var input bytes.Buffer
		for n, s := range order {
			var c struct {
				Description string            `json:"description"`
				Schema      json.RawMessage   `json:"schema"`
				Tests       []json.RawMessage `json:"tests"`
			}
			c.Description = strconv.Itoa(n)
			c.Schema = json.RawMessage(s)
			for k, i := range cases[s] {
				t, _ := json.Marshal(map[string]any{"description": strconv.Itoa(k), "instance": json.RawMessage(entries[i].Doc)})
				c.Tests = append(c.Tests, t)
			}
			line, err := json.Marshal(c)
			must(err)
			input.Write(line)
			input.WriteByte('\n')
		}
		args := []string{"--from", "bowtie-json-schema", "bowtie", "run", "-D", dialect}
		for _, impl := range implementations {
			args = append(args, "-i", impl)
		}
		cmd := exec.Command("uvx", args...)
		cmd.Stdin = &input
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil && len(out) == 0 {
			fail("bowtie run (%s): %v\n%s", dialect, err, stderr.String())
		}
		// seq -> the case's description, which is the index into order.
		seqCase := map[int]int{}
		sc := bufio.NewScanner(bytes.NewReader(out))
		sc.Buffer(make([]byte, 64<<20), 64<<20)
		for sc.Scan() {
			var line struct {
				Seq  *int `json:"seq"`
				Case *struct {
					Description string `json:"description"`
				} `json:"case"`
				Implementation string `json:"implementation"`
				Results        []struct {
					Valid *bool `json:"valid"`
				} `json:"results"`
			}
			if json.Unmarshal(sc.Bytes(), &line) != nil || line.Seq == nil {
				continue
			}
			if line.Case != nil {
				n, _ := strconv.Atoi(line.Case.Description)
				seqCase[*line.Seq] = n
				continue
			}
			if line.Implementation == "" || line.Results == nil {
				continue
			}
			n, ok := seqCase[*line.Seq]
			if !ok {
				continue
			}
			for k, r := range line.Results {
				if r.Valid == nil || k >= len(cases[order[n]]) {
					continue
				}
				e := &entries[cases[order[n]][k]]
				if e.Bowtie == nil {
					e.Bowtie = map[string]bool{}
				}
				e.Bowtie[line.Implementation] = *r.Valid
			}
		}
		must(sc.Err())
		for _, i := range idxs {
			e := entries[i]
			sent++
			if v, ok := e.Bowtie["go-jsonschema"]; !ok {
				fail("Bowtie returned no go-jsonschema verdict for %s against %s", e.Doc, e.Schema)
			} else if v != e.Valid {
				disagreed++
				fmt.Fprintf(os.Stderr, "go-jsonschema through Bowtie says %v, in process %v: %s against %s\n", v, e.Valid, e.Doc, e.Schema)
			}
		}
	}
	if disagreed > 0 {
		fail("%d of %d transported pairs disagree between Bowtie and the in-process run", disagreed, sent)
	}
	fmt.Fprintf(os.Stderr, "Bowtie: %d pairs transported, go-jsonschema agrees on all of them\n", sent)
}

// draft4Integers settles the one question the in-process run answers
// differently from the specification it is standing in for.
//
// Draft 4 defines "integer" as "a JSON number without a fraction or exponent
// part" -- the token, not the value -- and the official suite's
// zeroTerminatedFloats says 1.0 is not one there. santhosh-tekuri reads the
// value under every draft; python-jsonschema reads the token under draft 3 and
// draft 4. So for a draft-4 schema naming "integer", python-jsonschema's
// verdict through Bowtie is the answer wherever the pair could be sent, and
// every pair that could not be sent carries a number with a fraction or an
// exponent -- a decimal float64 cannot hold, or a magnitude past it -- which
// is not an integer under the token rule, so the schema is not satisfied. That
// last claim is checked for each such pair rather than assumed.
func draft4Integers(entries []entry) {
	overridden := 0
	for i := range entries {
		e := &entries[i]
		if dialectOf(e.Schema) != "http://json-schema.org/draft-04/schema#" || !strings.Contains(e.Schema, `"integer"`) {
			continue
		}
		if v, ok := e.Bowtie["python-jsonschema"]; ok {
			if v != e.Valid {
				overridden++
			}
			e.Valid = v
			continue
		}
		if !everyNumberHasFractionOrExponent(e.Doc) {
			fail("draft 4 pair %s against %s could not be sent through Bowtie, and names an integer token", e.Doc, e.Schema)
		}
		if e.Valid {
			overridden++
		}
		e.Valid = false
	}
	fmt.Fprintf(os.Stderr, "draft 4 integer: %d verdicts read by the token rather than the value\n", overridden)
}

func everyNumberHasFractionOrExponent(text string) bool {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return false
	}
	found, all := false, true
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case json.Number:
			found = true
			if !strings.ContainsAny(string(t), ".eE") {
				all = false
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			// maporder: a predicate over every member; order changes nothing.
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return found && all
}

func dialectOf(schemaText string) string {
	var m struct {
		Schema string `json:"$schema"`
	}
	_ = json.Unmarshal([]byte(schemaText), &m)
	if m.Schema == "" {
		return "https://json-schema.org/draft/2020-12/schema"
	}
	return m.Schema
}

// transportable reports whether every number in a JSON text reaches an
// implementation unchanged through Bowtie's Python harness: an integer, which
// Python holds exactly, or a decimal whose value is exactly that of the
// shortest spelling of its float64, which is what Python writes back.
func transportable(text string) bool {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return false
	}
	ok := true
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case json.Number:
			s := string(t)
			if !strings.ContainsAny(s, ".eE") {
				return
			}
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				ok = false
				return
			}
			a, _ := new(big.Rat).SetString(s)
			b, _ := new(big.Rat).SetString(strconv.FormatFloat(f, 'g', -1, 64))
			if a == nil || b == nil || a.Cmp(b) != 0 {
				ok = false
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			// maporder: a predicate over every member; order changes nothing.
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return ok
}

func must(err error) {
	if err != nil {
		fail("%v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
