package checkindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type fakeDB struct {
	hello               Hello
	status              Status
	statusErr, statsErr error
	names               []string
	rows                []IndexStat
	closed, queries     int
}

func (f *fakeDB) Hello(context.Context) (Hello, error)                  { return f.hello, nil }
func (f *fakeDB) Status(context.Context) (Status, error)                { return f.status, f.statusErr }
func (f *fakeDB) Collections(context.Context, string) ([]string, error) { return f.names, nil }
func (f *fakeDB) Stats(context.Context, string, string) ([]IndexStat, error) {
	f.queries++
	return f.rows, f.statsErr
}
func (f *fakeDB) Close(context.Context) error { f.closed++; return nil }

func TestTopologyFallback(t *testing.T) {
	for _, tt := range []struct {
		name   string
		hello  Hello
		status Status
		err    error
		want   []Node
	}{
		{name: "standalone", hello: Hello{}, err: errors.New("not replica set"), want: []Node{{Name: "localhost:27017", Role: "STANDALONE"}}},
		{name: "unauthorized status", hello: Hello{Hosts: []string{"a:1", "b:2"}, Primary: "a:1"}, err: errors.New("unauthorized"), want: []Node{{Name: "a:1", Role: "PRIMARY"}, {Name: "b:2", Role: "SECONDARY"}}},
		{name: "healthy status only", status: Status{Members: []Member{{Name: "a:1", StateStr: "PRIMARY", Health: 1}, {Name: "arbiter", StateStr: "ARBITER", Health: 1}, {Name: "down", StateStr: "SECONDARY", Health: 0}}}, want: []Node{{Name: "a:1", Role: "PRIMARY"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			nodes, e := discover(context.Background(), &fakeDB{hello: tt.hello, status: tt.status, statusErr: tt.err}, "localhost:27017")
			if e != nil || len(nodes) != len(tt.want) {
				t.Fatalf("nodes %+v %v", nodes, e)
			}
			for i, n := range nodes {
				if n != tt.want[i] {
					t.Fatalf("node %+v", n)
				}
			}
		})
	}
}

func TestDirectClientsReusedAndClosed(t *testing.T) {
	seed := &fakeDB{hello: Hello{Hosts: []string{"a:1", "b:2"}, Primary: "a:1"}, statusErr: errors.New("unauthorized"), names: []string{"z", "system.profile", "a"}}
	first := &fakeDB{rows: []IndexStat{{Name: "compound", Key: bson.D{{Key: "z", Value: 1}, {Key: "a", Value: -1}}, Accesses: Accesses{Ops: 9007199254740993}}}}
	second := &fakeDB{rows: []IndexStat{{Name: "compound", Key: bson.D{{Key: "z", Value: 1}, {Key: "a", Value: -1}}, Accesses: Accesses{Ops: 7}}}}
	clients := map[string]*fakeDB{"": seed, "a:1": first, "b:2": second}
	calls := map[string]int{}
	dial := func(_ context.Context, _ Profile, host string) (Database, error) {
		calls[host]++
		return clients[host], nil
	}
	var out bytes.Buffer
	p := Profile{DBName: "actual"}
	if e := collect(context.Background(), p, Options{}, &out, dial); e != nil {
		t.Fatal(e)
	}
	for h, c := range clients {
		if calls[h] != 1 || c.closed != 1 {
			t.Fatalf("reuse/cleanup %s: %d %d", h, calls[h], c.closed)
		}
	}
	if first.queries != 2 || second.queries != 2 || seed.queries != 0 {
		t.Fatal("system collection or seed queried")
	}
	text := out.String()
	for _, want := range []string{"actual.a", "actual.z", "9007199254741000", "18014398509482000", `{"z":1,"a":-1}`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
}

func TestPartialFailureCleanupAndExplicitSystemCollection(t *testing.T) {
	seed := &fakeDB{statusErr: errors.New("standalone")}
	node := &fakeDB{statsErr: errors.New("sensitive connection text")}
	var out bytes.Buffer
	e := collect(context.Background(), Profile{DBName: "db", Credentials: Credentials{InitialURI: "mongodb://localhost:27017"}}, Options{Collection: "system.profile"}, &out, func(_ context.Context, _ Profile, h string) (Database, error) {
		if h == "" {
			return seed, nil
		}
		return node, nil
	})
	if e == nil || node.queries != 1 || seed.closed != 1 || node.closed != 1 {
		t.Fatalf("failure/cleanup: %v", e)
	}
	if strings.Contains(out.String(), "sensitive") {
		t.Fatal("raw driver error leaked")
	}
}

func TestMarkdownEscapesAndTotals(t *testing.T) {
	rows := []IndexUsage{{Name: "x|y\nnext", Key: bson.D{{Key: "z", Value: 1}, {Key: "a", Value: -1}}, Total: 42, PerNode: map[string]int64{"node": 42}}}
	text, err := renderTables(rows, []Node{{Name: "node", Role: "PRIMARY"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `x\|y next`) || !strings.Contains(text, "| Total |") || !strings.Contains(text, `{"z":1,"a":-1}`) {
		t.Fatal(text)
	}
	if _, e := addOps(9223372036854775807, 1); e == nil {
		t.Fatal("counter overflow silently accepted")
	}
}

func TestJSONReportIsPureAndUsesExactNumericCounts(t *testing.T) {
	seed := &fakeDB{hello: Hello{Me: "node"}, statusErr: errors.New("standalone"), names: []string{"items"}}
	node := &fakeDB{rows: []IndexStat{{Name: "ix", Accesses: Accesses{Ops: 9007199254740993}}}}
	var out bytes.Buffer
	err := collect(context.Background(), Profile{Name: "production", DBName: "db"}, Options{JSON: true}, &out, func(_ context.Context, _ Profile, host string) (Database, error) {
		if host == "" {
			return seed, nil
		}
		return node, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	d := json.NewDecoder(strings.NewReader(out.String()))
	d.UseNumber()
	if err = d.Decode(&parsed); err != nil {
		t.Fatalf("not valid JSON: %v: %s", err, out.String())
	}
	if strings.Contains(out.String(), "[INFO]") || strings.Contains(out.String(), "[WARN]") {
		t.Fatalf("diagnostics corrupted JSON: %s", out.String())
	}
	if len(parsed) != 1 || parsed["items"] == nil {
		t.Fatalf("unexpected top-level shape: %#v", parsed)
	}
	collection := parsed["items"].(map[string]any)
	index := collection["ix"].(map[string]any)
	if len(collection) != 1 || index["production"] != json.Number("9007199254740993") || index["total"] != json.Number("9007199254740993") || len(index) != 2 {
		t.Fatalf("unexpected direct nesting or numeric values: %#v", parsed)
	}
}

func TestJSONFileContainsDocumentAndConsoleStaysClean(t *testing.T) {
	seed := &fakeDB{hello: Hello{Me: "node"}, statusErr: errors.New("standalone"), names: []string{"items"}}
	node := &fakeDB{rows: []IndexStat{{Name: "ix", Accesses: Accesses{Ops: 4}}}}
	path := t.TempDir() + "/report.json"
	var out bytes.Buffer
	err := collect(context.Background(), Profile{DBName: "db"}, Options{JSON: true, File: path}, &out, func(_ context.Context, _ Profile, host string) (Database, error) {
		if host == "" {
			return seed, nil
		}
		return node, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("JSON file mode wrote console diagnostics: %s", out.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]map[string]map[string]int64
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("invalid file JSON: %s", data)
	}
	if len(decoded) != 1 || len(decoded["items"]) != 1 || len(decoded["items"]["ix"]) != 2 || decoded["items"]["ix"]["direct"] != 4 || decoded["items"]["ix"]["total"] != 4 {
		t.Fatalf("unexpected file schema: %s", data)
	}
}

func TestJSONPartialFailurePreservesSuccessfulNodeData(t *testing.T) {
	seed := &fakeDB{hello: Hello{Hosts: []string{"a", "b"}, Primary: "a"}, statusErr: errors.New("status unavailable")}
	good := &fakeDB{rows: []IndexStat{{Name: "ix", Accesses: Accesses{Ops: 5}}}}
	bad := &fakeDB{statsErr: errors.New("sensitive error")}
	var out bytes.Buffer
	err := collect(context.Background(), Profile{Name: "cluster", DBName: "db"}, Options{JSON: true, Collection: "items"}, &out, func(_ context.Context, _ Profile, host string) (Database, error) {
		switch host {
		case "":
			return seed, nil
		case "a":
			return good, nil
		default:
			return bad, nil
		}
	})
	if err == nil {
		t.Fatal("expected partial error")
	}
	var decoded map[string]map[string]map[string]int64
	if e := json.Unmarshal(out.Bytes(), &decoded); e != nil {
		t.Fatalf("invalid JSON: %v", e)
	}
	if decoded["items"]["ix"]["cluster"] != 5 || decoded["items"]["ix"]["total"] != 5 || len(decoded) != 1 {
		t.Fatalf("partial data lost: %+v", decoded)
	}
}
