package report

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internal "github.com/mrcl-oss/checkIndex/internal/checkindex"
	"github.com/mrcl-oss/checkIndex/internal/reportdata"
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
	// Valid direct input must proceed to the injected collector, never a driver.
	home := t.TempDir()
	t.Setenv("HOME", home)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err := collectWith(ctx, Options{URI: "mongodb://localhost", Username: "user", Password: "secret", Database: "db", AuthSource: "admin"}, func(context.Context, internal.Profile, string) (reportdata.Report, error) {
		return Report{Collections: map[string]Collection{}}, errors.New("cancelled")
	})
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
	b, err := Marshal(Report{Collections: map[string]Collection{"items": {"ix": Index{Clusters: map[string]Cluster{"cluster": {Nodes: map[string]NodeUsage{"node": {Count: 3, Primary: true}}, Total: 3}}, Total: 3}}}, Partial: true})
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	index := v["items"].(map[string]any)["ix"].(map[string]any)
	if len(v) != 1 || len(v["items"].(map[string]any)) != 1 || index["total"] != float64(3) || index["clusters"] == nil || len(index) != 2 {
		t.Fatalf("counts are not numeric or incorrect: %s", b)
	}
}

func TestCollectAllTypedAndFiltered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, ".checkIndex"), 0700); err != nil {
		t.Fatal(err)
	}
	config := `{"dbName":"db","authSource":"admin","hostMapping":{},"cluster":{"a":{"initialUri":"mongodb://a","username":"u","password":"p"},"b":{"initialUri":"mongodb://b","username":"u","password":"p"}}}`
	if err := os.WriteFile(filepath.Join(home, ".checkIndex", "config.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := collectWith(context.Background(), Options{All: true, Collection: "contacts"}, func(_ context.Context, p internal.Profile, filter string) (reportdata.Report, error) {
		if filter != "contacts" {
			t.Errorf("filter = %q", filter)
		}
		return Report{Collections: map[string]Collection{filter: {"ix": {Clusters: map[string]Cluster{p.Name: {Nodes: map[string]NodeUsage{"discovered": {Count: 9007199254740993, Primary: true}}, Total: 9007199254740993}}, Total: 9007199254740993}}}}, nil
	})
	if err != nil || r.Partial || len(r.Collections) != 1 || r.Collections["contacts"]["ix"].Total != 18014398509481986 {
		t.Fatalf("result=%+v error=%v", r, err)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Collections["contacts"]["ix"].Clusters["a"].Nodes["discovered"].Count != 9007199254740993 {
		t.Fatalf("typed JSON lost precision: %s", b)
	}
}

func TestCollectRejectsConflictingSelectionWithoutCollector(t *testing.T) {
	for _, options := range []Options{
		{All: true, ProfileName: "a"}, {All: true, Direct: Profile{Name: "direct-name"}}, {All: true, URI: "mongodb://unused"},
		{ProfileName: "a", Direct: Profile{Name: "direct-name"}}, {Direct: Profile{Name: "direct-name"}, URI: "mongodb://unused"},
	} {
		_, err := collectWith(context.Background(), options, func(context.Context, internal.Profile, string) (reportdata.Report, error) {
			t.Error("collector called for conflicting options")
			return Report{}, nil
		})
		if err == nil {
			t.Fatalf("accepted conflicting options %+v", options)
		}
	}
}

func TestCollectPreservesDirectName(t *testing.T) {
	for _, name := range []string{"custom", ""} {
		_, err := collectWith(context.Background(), Options{Direct: Profile{Name: name, URI: "mongodb://unused", Username: "u", Password: "p", Database: "db", AuthSource: "admin"}}, func(_ context.Context, p internal.Profile, _ string) (reportdata.Report, error) {
			want := name
			if want == "" {
				want = "direct"
			}
			if p.Name != want {
				t.Errorf("name = %q, want %q", p.Name, want)
			}
			return Report{}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCollectAllConfigFailureIsIncomplete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	r, err := collectWith(context.Background(), Options{All: true}, func(context.Context, internal.Profile, string) (reportdata.Report, error) {
		t.Error("collector called after config failure")
		return Report{}, nil
	})
	if err == nil || !r.Partial || r.Collections == nil {
		t.Fatalf("failed all-profile report = %+v, %v", r, err)
	}
}

func TestCollectNamedUsesTypedCollector(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, ".checkIndex"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".checkIndex", "config.json"), []byte(`{"dbName":"configured-db","authSource":"admin","hostMapping":{},"cluster":{"production":{"initialUri":"mongodb://unused","username":"u","password":"p"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := collectWith(context.Background(), Options{ProfileName: "production", Collection: "items"}, func(_ context.Context, p internal.Profile, filter string) (reportdata.Report, error) {
		if p.Name != "production" || p.DBName != "configured-db" || filter != "items" {
			t.Errorf("profile=%+v filter=%q", p, filter)
		}
		return Report{Collections: map[string]Collection{"items": {}}}, nil
	})
	if err != nil || r.Partial || r.Collections["items"] == nil {
		t.Fatalf("named report = %+v %v", r, err)
	}
}
