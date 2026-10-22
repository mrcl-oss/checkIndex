package report

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCollectDirectOptionsValidateAndAvoidConfig(t *testing.T) {
	tests := []struct {
		name    string
		options Options
		want    string
	}{
		{name: "missing fields", options: Options{URI: "mongodb://localhost", Password: "secret"}, want: "requires URI"},
		{name: "invalid URI", options: Options{URI: "not-mongodb", Username: "user", Password: "secret", Database: "db", AuthSource: "admin"}, want: "MongoDB URI"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Collect(context.Background(), tt.options)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Collect() error = %v, want containing %q", err, tt.want)
			}
			if r.Collections == nil {
				t.Fatal("expected defined collections map")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("error exposed password")
			}
		})
	}
		// Valid direct input must proceed to collection, not local config loading.
		// A canceled context prevents any network operation during this test.
		home := t.TempDir()
		t.Setenv("HOME", home)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r, err := Collect(ctx, Options{URI: "mongodb://localhost", Username: "user", Password: "secret", Database: "db", AuthSource: "admin"})
	if err == nil || strings.Contains(err.Error(), "configured profile") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("direct collection error = %v; want safe connection error without config lookup", err)
	}
	if r.Collections == nil {
		t.Fatal("expected defined collections map")
	}
}

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
