package reportdata

import (
	"encoding/json"
	"math"
	"testing"
)

func observation(cluster, node string, count int64, primary bool) Report {
	return Report{Collections: map[string]Collection{"items": {"ix": {Clusters: map[string]Cluster{cluster: {Nodes: map[string]NodeUsage{node: {Count: count, Primary: primary}}, Total: count}}, Total: count}}}}
}

func TestMergeCheckedNameBasedCounts(t *testing.T) {
	r := observation("same", "node", 9007199254740993, false)
	if err := Merge(&r, observation("same", "node", 7, true)); err != nil {
		t.Fatal(err)
	}
	ix := r.Collections["items"]["ix"]
	if ix.Total != 9007199254741000 || ix.Clusters["same"].Total != ix.Total || ix.Clusters["same"].Nodes["node"] != (NodeUsage{Count: ix.Total, Primary: true}) {
		t.Fatalf("merged = %+v", ix)
	}
}

func TestMergeRejectsInvalidCountersAtomically(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input Report
	}{
		{"overflow", observation("same", "node", math.MaxInt64, false)},
		{"negative", observation("other", "node", -1, false)},
		{"inconsistent totals", Report{Collections: map[string]Collection{"items": {"ix": {Clusters: map[string]Cluster{}, Total: 1}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := observation("same", "node", 1, false)
			before, _ := json.Marshal(r)
			if err := Merge(&r, tc.input); err == nil {
				t.Fatal("invalid merge accepted")
			}
			after, _ := json.Marshal(r)
			if string(before) != string(after) {
				t.Fatalf("failed merge changed report: %s", after)
			}
		})
	}
}

func TestEmptyReportJSONAndRoundTrip(t *testing.T) {
	data, err := json.Marshal(Report{})
	if err != nil || string(data) != "{}" {
		t.Fatalf("empty = %s, %v", data, err)
	}
	original := observation("cluster", "node", 9007199254740993, true)
	data, _ = json.Marshal(original)
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.Collections["items"]["ix"].Total != 9007199254740993 || r.Partial {
		t.Fatalf("round trip = %+v", r)
	}
}

func TestNilNestedMapsEncodeAsObjects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"collection", Collection(nil), `{}`},
		{"index", Index{}, `{"clusters":{},"total":0}`},
		{"cluster", Cluster{}, `{"nodes":{},"total":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.value)
			if err != nil || string(data) != tc.want {
				t.Fatalf("JSON=%s error=%v, want %s", data, err, tc.want)
			}
		})
	}
}
