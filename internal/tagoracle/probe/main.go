// Command probe answers tagoracle.Carries for a list of names, in whichever
// encoding/json it was built with. pkg/generator's tag tests build it under the
// GOEXPERIMENT setting their own binary was not built with, so that one test
// run can see both implementations a caller of the generated code might use.
//
// It reads a tagoracle.Request as JSON on stdin and writes a
// tagoracle.Response on stdout.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/mgilbir/schemagen/internal/tagoracle"
)

func main() {
	var req tagoracle.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "probe: reading request: %v\n", err)
		os.Exit(2)
	}
	resp := tagoracle.Response{JSONv2: tagoracle.JSONv2, Names: req.Names, Carried: make([]bool, len(req.Names))}
	for i, name := range req.Names {
		resp.Carried[i] = tagoracle.Carries(name, req.Spellings)
	}
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		fmt.Fprintf(os.Stderr, "probe: writing response: %v\n", err)
		os.Exit(2)
	}
}
