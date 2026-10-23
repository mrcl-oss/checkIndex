// Command all-report prints typed index observations from all saved profiles.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mrcl-oss/checkIndex/report"
)

func run(ctx context.Context, collection string, out io.Writer, collect func(context.Context, report.Options) (report.Report, error)) error {
	result, collectErr := collect(ctx, report.Options{All: true, Collection: collection})
	data, err := report.Marshal(result)
	if err != nil {
		return err
	}
	if _, err := out.Write(data); err != nil {
		return err
	}
	return collectErr
}

func main() {
	collection := flag.String("collection", "", "optional collection name or comma-separated names")
	flag.Parse()
	if err := run(context.Background(), *collection, os.Stdout, report.Collect); err != nil {
		fmt.Fprintf(os.Stderr, "collect report: %v\n", err)
		os.Exit(1)
	}
}
