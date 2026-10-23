package checkindex

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mrcl-oss/checkIndex/internal/reportdata"
)

func profileReport(cluster string, count int64) reportdata.Report {
	return reportdata.Report{Collections: map[string]reportdata.Collection{"items": {"ix": {Clusters: map[string]reportdata.Cluster{cluster: {Nodes: map[string]reportdata.NodeUsage{"discovered": {Count: count, Primary: true}}, Total: count}}, Total: count}}}}
}

func TestCollectConfiguredRetainsPartialDataAndErrors(t *testing.T) {
	config, err := decodeConfig([]byte(`{"dbName":"db","authSource":"admin","hostMapping":{},"cluster":{"a":{"initialUri":"mongodb://a","username":"u","password":"p"},"b":{"initialUri":"mongodb://b","username":"u","password":"p"},"invalid":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	r, err := CollectConfigured(context.Background(), config, "items", func(_ context.Context, p Profile, filter string) (reportdata.Report, error) {
		if filter != "items" {
			t.Errorf("filter = %q", filter)
		}
		if p.Name == "b" {
			return profileReport("b", 7), ErrPartialReport
		}
		return profileReport("a", 9007199254740993), nil
	})
	if err == nil || !r.Partial || !errors.Is(err, ErrPartialReport) || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("partial=%v error=%v", r.Partial, err)
	}
	ix := r.Collections["items"]["ix"]
	if ix.Total != 9007199254741000 || len(ix.Clusters) != 2 {
		t.Fatalf("results = %+v", ix)
	}
}

func TestCollectConfiguredAllFailedAndOverflow(t *testing.T) {
	config := Config{DBName: "db", AuthSource: "admin", Cluster: map[string]Credentials{"a": {InitialURI: "mongodb://a", Username: "u", Password: "p"}, "b": {InitialURI: "mongodb://b", Username: "u", Password: "p"}}}
	for _, failure := range []bool{true, false} {
		r, err := CollectConfigured(context.Background(), config, "", func(_ context.Context, p Profile, _ string) (reportdata.Report, error) {
			if failure {
				return reportdata.Report{}, errors.New("unavailable")
			}
			return profileReport(p.Name, math.MaxInt64), nil
		})
		if err == nil || !r.Partial || r.Collections == nil {
			t.Fatalf("expected defined incomplete report: %+v %v", r, err)
		}
		if failure && len(r.Collections) != 0 {
			t.Fatal("invented failed profile data")
		}
		if !failure && r.Collections["items"]["ix"].Total != math.MaxInt64 {
			t.Fatal("overflow damaged successful data")
		}
	}
}

func TestCollectConfiguredProfilesOverlap(t *testing.T) {
	config := Config{DBName: "db", AuthSource: "admin", Cluster: map[string]Credentials{"a": {InitialURI: "mongodb://a", Username: "u", Password: "p"}, "b": {InitialURI: "mongodb://b", Username: "u", Password: "p"}}}
	started := make(chan string, 2)
	proceed := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := CollectConfigured(context.Background(), config, "", func(_ context.Context, p Profile, _ string) (reportdata.Report, error) {
			started <- p.Name
			<-proceed
			return profileReport(p.Name, 1), nil
		})
		finished <- err
	}()
	seen := map[string]bool{}
	for range 2 {
		select {
		case name := <-started:
			seen[name] = true
		case <-time.After(2 * time.Second):
			close(proceed)
			<-finished
			t.Fatal("profile collection did not overlap")
		}
	}
	close(proceed)
	if err := <-finished; err != nil || len(seen) != 2 {
		t.Fatalf("seen=%v error=%v", seen, err)
	}
}
