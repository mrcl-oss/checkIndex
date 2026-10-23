package report_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mrcl-oss/checkIndex/report"
)

// This consumer-package test ensures callers never need an internal import.
func TestExportedTypedReport(t *testing.T) {
	var collect func(context.Context, report.Options) (report.Report, error) = report.Collect
	_ = collect
	options := report.Options{All: true, Collection: "items"}
	if !options.All {
		t.Fatal("All selection missing")
	}
	result := report.Report{Collections: map[string]report.Collection{"items": {"ix": report.Index{Clusters: map[string]report.Cluster{"production": {Nodes: map[string]report.NodeUsage{"discovered": {Count: 9007199254740993, Primary: true}}, Total: 9007199254740993}}, Total: 9007199254740993}}}}
	data, err := report.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded report.Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	var usage report.NodeUsage = decoded.Collections["items"]["ix"].Clusters["production"].Nodes["discovered"]
	if usage.Count != 9007199254740993 || !usage.Primary {
		t.Fatalf("usage=%+v", usage)
	}
}
