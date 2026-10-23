package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/mrcl-oss/checkIndex/report"
)

func TestRunAllProfileExample(t *testing.T) {
	for _, partial := range []bool{false, true} {
		var out bytes.Buffer
		failure := errors.New("partial collection")
		err := run(context.Background(), "items", &out, func(_ context.Context, options report.Options) (report.Report, error) {
			if !options.All || options.Collection != "items" {
				t.Errorf("options=%+v", options)
			}
			r := report.Report{Collections: map[string]report.Collection{"items": {}}, Partial: partial}
			if partial {
				return r, failure
			}
			return r, nil
		})
		if (err != nil) != partial || out.String() != "{\n  \"items\": {}\n}\n" {
			t.Fatalf("output=%q error=%v", out.String(), err)
		}
	}
}
