package checkindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type fakeDB struct {
	mu                  sync.Mutex
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
	f.mu.Lock()
	f.queries++
	f.mu.Unlock()
	return f.rows, f.statsErr
}
func (f *fakeDB) Close(context.Context) error {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
	return nil
}

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
	var callsMu sync.Mutex
	dial := func(_ context.Context, _ Profile, host string) (Database, error) {
		callsMu.Lock()
		calls[host]++
		callsMu.Unlock()
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

func TestStatsFailureIncludesSafeNodeContext(t *testing.T) {
	seed := &fakeDB{statusErr: errors.New("standalone")}
	node := &fakeDB{statsErr: errors.New("$indexStats unauthorized")}
	var out bytes.Buffer
	err := collect(context.Background(), Profile{DBName: "db", Credentials: Credentials{InitialURI: "mongodb://user:secret@localhost:27017/db?authSource=secret"}}, Options{Collection: "contacts"}, &out, func(_ context.Context, _ Profile, host string) (Database, error) {
		if host == "" {
			return seed, nil
		}
		return node, nil
	})
	if err == nil {
		t.Fatal("expected partial error")
	}
	text := out.String() + err.Error()
	for _, want := range []string{"localhost:27017", "contacts", "$indexStats", "unauthorized"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q from diagnostic: %s", want, text)
		}
	}
	for _, secret := range []string{"user", "secret", "mongodb://"} {
		if strings.Contains(text, secret) {
			t.Fatalf("credential material %q leaked: %s", secret, text)
		}
	}
}

func TestConsoleTablePreservesLongValues(t *testing.T) {
	longKey := strings.Repeat("long_key_", 20)
	rows := []IndexUsage{{Name: strings.Repeat("index_label_", 10), Key: bson.D{{Key: longKey, Value: 1}}, Total: 3, PerNode: map[string]int64{"node-label-that-is-long": 3}}}
	text, err := renderTables(rows, []Node{{Name: "node-label-that-is-long", Role: "PRIMARY"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		column int
		value  string
	}{{1, rows[0].Name}, {2, longKey}, {3, "node-label-that-is-long"}, {4, "3"}} {
		if !strings.Contains(consoleTableColumn(text, check.column), check.value) {
			t.Fatalf("table lost %q in column %d: %s", check.value, check.column, text)
		}
	}
}

func TestConsoleTableWrapsLongValuesWithinWidth(t *testing.T) {
	longKey := strings.Repeat("long_key_", 20)
	longName := strings.Repeat("index_label_", 10)
	nodes := []Node{{Name: "long-primary-node", Role: "PRIMARY"}, {Name: "long-secondary-node", Role: "SECONDARY"}}
	text, err := renderTables([]IndexUsage{{Name: longName, Key: bson.D{{Key: longKey, Value: 1}}, Total: 5, PerNode: map[string]int64{nodes[0].Name: 2, nodes[1].Name: 3}}}, nodes, false)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected wrapped rows: %s", text)
	}
	for _, line := range lines {
		if width := utf8.RuneCountInString(line); width > 80 {
			t.Fatalf("line exceeds configured display width: %d: %q", width, line)
		}
	}
	for _, check := range []struct {
		column int
		value  string
	}{{1, longName}, {2, longKey}, {3, nodes[0].Name}, {4, nodes[1].Name}, {5, "5"}} {
		if strings.Count(consoleTableColumn(text, check.column), check.value) != 1 {
			t.Fatalf("value should occur once in column %d: %q", check.column, check.value)
		}
	}
}

func consoleTableColumn(text string, column int) string {
	var content strings.Builder
	for _, line := range strings.Split(text, "\n") {
		cells := strings.Split(line, "│")
		if len(cells) > column {
			content.WriteString(strings.ReplaceAll(strings.TrimSpace(cells[column]), "↩", ""))
		}
	}
	return content.String()
}

func TestMarkdownTableDoesNotWrapCells(t *testing.T) {
	name := strings.Repeat("markdown_name_", 10)
	text, err := renderTables([]IndexUsage{{Name: name, Key: bson.D{{Key: "key", Value: 1}}}}, []Node{{Name: "node", Role: "PRIMARY"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "| "+name+" | ") || strings.Count(text, "| ") < 4 {
		t.Fatalf("markdown pipe table changed: %s", text)
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

type concurrentFakeDB struct {
	started chan string
	proceed chan struct{}
}

func (c *concurrentFakeDB) Hello(context.Context) (Hello, error) { return Hello{Me: "node"}, nil }
func (c *concurrentFakeDB) Status(context.Context) (Status, error) {
	return Status{}, errors.New("standalone")
}
func (c *concurrentFakeDB) Collections(context.Context, string) ([]string, error) { return nil, nil }
func (c *concurrentFakeDB) Stats(_ context.Context, _, col string) ([]IndexStat, error) {
	c.started <- col
	<-c.proceed
	return []IndexStat{{Name: "ix", Accesses: Accesses{Ops: 10}}}, nil
}
func (c *concurrentFakeDB) Close(context.Context) error { return nil }

func TestCollectCollectionsConcurrently(t *testing.T) {
	seed := &fakeDB{hello: Hello{Me: "node"}, statusErr: errors.New("standalone"), names: []string{"colB", "colA"}}
	started := make(chan string, 2)
	proceed := make(chan struct{})
	node := &concurrentFakeDB{started: started, proceed: proceed}

	dial := func(_ context.Context, _ Profile, host string) (Database, error) {
		if host == "" {
			return seed, nil
		}
		return node, nil
	}

	var out bytes.Buffer
	errCh := make(chan error, 1)
	go func() {
		errCh <- collect(context.Background(), Profile{DBName: "testdb"}, Options{}, &out, dial)
	}()

	first := <-started
	second := <-started
	if first == second {
		t.Fatalf("expected different collections, got %s and %s", first, second)
	}
	close(proceed)
	if err := <-errCh; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := out.String()
	idxA := strings.Index(text, "=== testdb.colA ===")
	idxB := strings.Index(text, "=== testdb.colB ===")
	if idxA == -1 || idxB == -1 || idxA > idxB {
		t.Fatalf("expected colA before colB in output:\n%s", text)
	}
}

type nodeOverlapDB struct {
	started chan<- string
	ready   <-chan struct{}
	name    string
	closed  atomic.Int32
}

func (d *nodeOverlapDB) Hello(context.Context) (Hello, error) { return Hello{}, nil }
func (d *nodeOverlapDB) Status(context.Context) (Status, error) {
	return Status{}, errors.New("standalone")
}
func (d *nodeOverlapDB) Collections(context.Context, string) ([]string, error) { return nil, nil }
func (d *nodeOverlapDB) Stats(context.Context, string, string) ([]IndexStat, error) {
	d.started <- d.name
	select {
	case <-d.ready:
		if d.name == "b" {
			return nil, errors.New("$indexStats unauthorized")
		}
		return []IndexStat{{Name: "ix", Accesses: Accesses{Ops: 7}}}, nil
	case <-time.After(300 * time.Millisecond):
		return nil, errors.New("timed out waiting for other node")
	}
}
func (d *nodeOverlapDB) Close(context.Context) error { d.closed.Add(1); return nil }

func TestCollectNodeStatsOverlapAndPartialCleanup(t *testing.T) {
	started := make(chan string, 2)
	ready := make(chan struct{})
	a := &nodeOverlapDB{started: started, ready: ready, name: "a"}
	b := &nodeOverlapDB{started: started, ready: ready, name: "b"}
	seed := &fakeDB{hello: Hello{Hosts: []string{"a", "b"}}, statusErr: errors.New("standalone")}
	var dialMu sync.Mutex
	dialCounts := make(map[string]int)
	dial := func(_ context.Context, _ Profile, host string) (Database, error) {
		dialMu.Lock()
		dialCounts[host]++
		dialMu.Unlock()
		switch host {
		case "":
			return seed, nil
		case "a":
			return a, nil
		default:
			return b, nil
		}
	}

	result := make(chan error, 1)
	var out bytes.Buffer
	go func() {
		result <- collect(context.Background(), Profile{DBName: "db"}, Options{JSON: true, Collection: "items"}, &out, dial)
	}()

	seen := make(map[string]bool)
	for range 2 {
		select {
		case node := <-started:
			seen[node] = true
		case <-time.After(200 * time.Millisecond):
			close(ready)
			<-result
			t.Fatal("Stats did not start on both nodes concurrently")
		}
	}
	close(ready)
	if err := <-result; !errors.Is(err, ErrPartialReport) {
		t.Fatalf("collect error = %v, want partial report", err)
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("Stats started on nodes %v", seen)
	}
	if a.closed.Load() != 1 || b.closed.Load() != 1 || seed.closed != 1 {
		t.Fatalf("clients not closed once: a=%d b=%d seed=%d", a.closed.Load(), b.closed.Load(), seed.closed)
	}
	if dialCounts["a"] != 1 || dialCounts["b"] != 1 {
		t.Fatalf("node dial counts = %v", dialCounts)
	}
	var got jsonReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["items"]["ix"]["direct"] != 7 {
		t.Fatalf("successful node data lost: %v", got)
	}
}

func TestCollectCommaSeparatedCollections(t *testing.T) {
	seed := &fakeDB{hello: Hello{Me: "node"}, statusErr: errors.New("standalone")}
	node := &fakeDB{rows: []IndexStat{{Name: "ix", Accesses: Accesses{Ops: 5}}}}
	dial := func(_ context.Context, _ Profile, host string) (Database, error) {
		if host == "" {
			return seed, nil
		}
		return node, nil
	}
	var out bytes.Buffer
	err := collect(context.Background(), Profile{DBName: "testdb"}, Options{Collection: "orders, users"}, &out, dial)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	idxOrders := strings.Index(text, "=== testdb.orders ===")
	idxUsers := strings.Index(text, "=== testdb.users ===")
	if idxOrders == -1 || idxUsers == -1 || idxOrders > idxUsers {
		t.Fatalf("expected orders before users in output:\n%s", text)
	}
}
