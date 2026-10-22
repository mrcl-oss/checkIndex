package report

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCollectInvalidDirectReturnsDefinedReport(t *testing.T) {
	r, err := Collect(context.Background(), Options{Direct: Profile{Password: "must-not-leak"}})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if r.Collections == nil {
		t.Fatal("expected defined collections map")
	}
	if got := err.Error(); got == "" || got == "must-not-leak" {
		t.Fatalf("unsafe error: %q", got)
	}
}

func TestIndexJSONUsesNumericCountsAndTotal(t *testing.T) {
	b, err := Marshal(Report{Collections: map[string]Collection{"items": {"ix": Index{"node": 3, "total": 3}}}, Partial: true})
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	index := v["items"].(map[string]any)["ix"].(map[string]any)
	if len(v) != 1 || len(v["items"].(map[string]any)) != 1 || index["total"] != float64(3) || index["node"] != float64(3) || len(index) != 2 {
		t.Fatalf("counts are not numeric or incorrect: %s", b)
	}
}
