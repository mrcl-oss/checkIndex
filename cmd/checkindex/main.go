package main

import (
	"fmt"
	"os"

	"github.com/mrcl-oss/checkIndex/internal/checkindex"
)

func main() {
	if err := checkindex.RunCLI(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "[ERROR]", err)
		os.Exit(1)
	}
}
