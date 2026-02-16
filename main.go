package main

import (
	"fmt"
	"os"

	"github.com/mgilbir/schemagen/cmd/schemagen"
)

func main() {
	if err := schemagen.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
