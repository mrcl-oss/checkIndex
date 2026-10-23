package checkindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mrcl-oss/checkIndex/internal/reportdata"
)

func TestTypedCollectorDiscoveredRolesIdentityAndSelection(t *testing.T) {
	for _, filter := range []string{"", "items"} {
		t.Run("filter="+filter, func(t *testing.T) {
			seed := &fakeDB{status: Status{Members: []Member{{Name: "internal-a", StateStr: "PRIMARY", Health: 1}, {Name: "internal-b", StateStr: "SECONDARY", Health: 1}}}, names: []string{"items"}}
			a := &fakeDB{rows: []IndexStat{{Name: "ix", Accesses: Accesses{Ops: 9007199254740993}}}}
			b := &fakeDB{rows: []IndexStat{{Name: "ix", Accesses: Accesses{Ops: 7}}}}
			dial := func(_ context.Context, p Profile, host string) (Database, error) {
				if p.HostMapping["internal-a"] != "localhost:1" {
					t.Error("mapping not passed to dial boundary")
				}
				switch host {
				case "":
					return seed, nil
				case "internal-a":
					return a, nil
				case "internal-b":
					return b, nil
				default:
					t.Errorf("unexpected identity %q", host)
					return nil, errors.New("unknown")
				}
			}
			r, err := collectData(context.Background(), Profile{Name: "production", DBName: "db", HostMapping: map[string]string{"internal-a": "localhost:1"}}, filter, dial)
			if err != nil || r.Partial {
				t.Fatalf("report error = %v", err)
			}
			ix := r.Collections["items"]["ix"]
			cluster := ix.Clusters["production"]
			if ix.Total != 9007199254741000 || cluster.Total != ix.Total || len(cluster.Nodes) != 2 || !cluster.Nodes["internal-a"].Primary || cluster.Nodes["internal-b"].Primary || cluster.Nodes["internal-b"].Count != 7 {
				t.Fatalf("index = %+v", ix)
			}
			if seed.closed != 1 || a.closed != 1 || b.closed != 1 {
				t.Fatal("clients not closed")
			}
		})
	}
}

type collectionCounterDB struct{ fakeDB }

func (d *collectionCounterDB) Stats(_ context.Context, _, collection string) ([]IndexStat, error) {
	if collection == "bad" {
		return []IndexStat{{Name: "overflow", Accesses: Accesses{Ops: math.MaxInt64}}, {Name: "overflow", Accesses: Accesses{Ops: 1}}}, nil
	}
	return []IndexStat{{Name: "ix", Accesses: Accesses{Ops: 4}}}, nil
}

func TestTypedCollectorOverflowRetainsOtherCollections(t *testing.T) {
	seed := &fakeDB{hello: Hello{Me: "node"}, names: []string{"bad", "good"}}
	node := &collectionCounterDB{}
	r, err := collectData(context.Background(), Profile{Name: "cluster"}, "", func(_ context.Context, _ Profile, host string) (Database, error) {
		if host == "" {
			return seed, nil
		}
		return node, nil
	})
	if err == nil || !r.Partial || r.Collections["good"]["ix"].Total != 4 {
		t.Fatalf("available data lost: %+v %v", r, err)
	}
	if _, ok := r.Collections["bad"]; ok {
		t.Fatal("overflowing collection emitted")
	}
}

func TestTypedCollectorEmptyAndAllNodesFailed(t *testing.T) {
	for _, failed := range []bool{false, true} {
		seed := &fakeDB{hello: Hello{Me: "node"}, names: []string{"items"}}
		node := &fakeDB{}
		r, err := collectData(context.Background(), Profile{}, "", func(_ context.Context, _ Profile, host string) (Database, error) {
			if host == "" {
				return seed, nil
			}
			if failed {
				return nil, errors.New("dial failed")
			}
			return node, nil
		})
		if (err != nil) != failed || r.Partial != failed {
			t.Fatalf("partial=%v err=%v", r.Partial, err)
		}
		data, _ := json.Marshal(r)
		if string(data) != `{"items":{}}` {
			t.Fatalf("empty data=%s", data)
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("writer failed") }

func TestCLIAllJSONRejectsMalformedAndPropagatesWriterErrors(t *testing.T) {
	home := writeTestConfig(t, `{"dbName":"db","authSource":"admin","hostMapping":{},"cluster":{"a":{"initialUri":"mongodb://a","username":"u","password":"p"}}}`)
	for _, data := range []string{"not-json", "", "null", `{"items":{"ix":{"unexpected":1}}}`, `{"items":{"ix":{"clusters":{},"total":1}}}`} {
		var out bytes.Buffer
		err := run([]string{"--all", "--json"}, home, &out, func(_ context.Context, _ Profile, _ Options, w io.Writer) error {
			_, err := io.WriteString(w, data)
			return err
		})
		if err == nil || strings.TrimSpace(out.String()) != "{}" {
			t.Fatalf("malformed report accepted: %q %v", out.String(), err)
		}
	}
	err := run([]string{"--all", "--json"}, home, failingWriter{}, func(_ context.Context, _ Profile, _ Options, w io.Writer) error {
		_, err := io.WriteString(w, "{}")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "writer failed") {
		t.Fatalf("writer error lost: %v", err)
	}
}

func TestNodeDialsOverlapAndCleanup(t *testing.T) {
	seed := &fakeDB{hello: Hello{Hosts: []string{"a", "b"}}, names: []string{"items"}}
	a, b := &fakeDB{}, &fakeDB{}
	started := make(chan string, 2)
	proceed := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := collectData(context.Background(), Profile{}, "", func(_ context.Context, _ Profile, host string) (Database, error) {
			if host == "" {
				return seed, nil
			}
			started <- host
			<-proceed
			if host == "a" {
				return a, nil
			}
			return b, nil
		})
		result <- err
	}()
	seen := map[string]bool{}
	for range 2 {
		select {
		case host := <-started:
			seen[host] = true
		case <-time.After(2 * time.Second):
			close(proceed)
			<-result
			t.Fatal("node dials did not overlap")
		}
	}
	close(proceed)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if !seen["a"] || !seen["b"] || seed.closed != 1 || a.closed != 1 || b.closed != 1 {
		t.Fatalf("dial identities/cleanup: %v %d %d %d", seen, seed.closed, a.closed, b.closed)
	}
}

func TestSingleJSONWriterAndFileErrors(t *testing.T) {
	for _, file := range []bool{false, true} {
		seed := &fakeDB{hello: Hello{Me: "node"}}
		node := &fakeDB{rows: []IndexStat{{Name: "ix", Accesses: Accesses{Ops: 1}}}}
		o := Options{JSON: true, Collection: "items"}
		if file {
			o.File = t.TempDir()
		}
		err := collect(context.Background(), Profile{}, o, failingWriter{}, func(_ context.Context, _ Profile, host string) (Database, error) {
			if host == "" {
				return seed, nil
			}
			return node, nil
		})
		if err == nil {
			t.Fatal("output failure lost")
		}
		if seed.closed != 1 || node.closed != 1 {
			t.Fatal("output failure skipped cleanup")
		}
	}
}

func TestAllJSONParityWithTypedProfiles(t *testing.T) {
	home := writeTestConfig(t, `{"dbName":"db","authSource":"admin","hostMapping":{},"cluster":{"a":{"initialUri":"mongodb://a","username":"u","password":"p"},"b":{"initialUri":"mongodb://b","username":"u","password":"p"}}}`)
	data, err := os.ReadFile(home + "/.checkIndex/config.json")
	if err != nil {
		t.Fatal(err)
	}
	config, err := decodeConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	want, err := CollectConfigured(context.Background(), config, "items", func(_ context.Context, p Profile, _ string) (reportdata.Report, error) {
		return profileReport(p.Name, 9007199254740993), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = run([]string{"--all", "--json", "--collection", "items"}, home, &out, func(_ context.Context, p Profile, _ Options, w io.Writer) error {
		return json.NewEncoder(w).Encode(profileReport(p.Name, 9007199254740993))
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(want)
	if strings.TrimSpace(out.String()) != string(encoded) {
		t.Fatalf("CLI=%s typed=%s", out.String(), encoded)
	}
}

func TestJSONDoesNotSumUnrelatedIndexes(t *testing.T) {
	seed := &fakeDB{hello: Hello{Me: "node"}, names: []string{"items"}}
	node := &fakeDB{rows: []IndexStat{{Name: "one", Accesses: Accesses{Ops: math.MaxInt64}}, {Name: "two", Accesses: Accesses{Ops: math.MaxInt64}}}}
	r, err := collectData(context.Background(), Profile{}, "", func(_ context.Context, _ Profile, host string) (Database, error) {
		if host == "" {
			return seed, nil
		}
		return node, nil
	})
	if err != nil || r.Partial || len(r.Collections["items"]) != 2 {
		t.Fatalf("independent counters lost: %+v %v", r, err)
	}
}

func TestAllJSONSelectionSchema(t *testing.T) {
	home := writeTestConfig(t, `{"dbName":"db","authSource":"admin","hostMapping":{},"cluster":{"a":{"initialUri":"mongodb://a","username":"u","password":"p"},"b":{"initialUri":"mongodb://b","username":"u","password":"p"}}}`)
	for _, filter := range []string{"", "items"} {
		t.Run("filter="+filter, func(t *testing.T) {
			var out bytes.Buffer
			args := []string{"--all", "--json"}
			if filter != "" {
				args = append(args, "--collection", filter)
			}
			err := run(args, home, &out, func(_ context.Context, p Profile, options Options, w io.Writer) error {
				r := profileReport(p.Name, 9)
				if options.Collection == "" {
					r.Collections["other"] = reportdata.Collection{}
				}
				return json.NewEncoder(w).Encode(r)
			})
			if err != nil {
				t.Fatal(err)
			}
			var got reportdata.Report
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			wantCollections := 1
			if filter == "" {
				wantCollections = 2
			}
			index := got.Collections["items"]["ix"]
			if len(got.Collections) != wantCollections || len(index.Clusters) != 2 || index.Total != 18 {
				t.Fatalf("selection = %s", out.String())
			}
			for _, name := range []string{"a", "b"} {
				cluster := index.Clusters[name]
				if cluster.Total != 9 || len(cluster.Nodes) != 1 || cluster.Nodes["discovered"].Count != 9 || !cluster.Nodes["discovered"].Primary {
					t.Fatalf("cluster = %+v", cluster)
				}
			}
		})
	}
}
