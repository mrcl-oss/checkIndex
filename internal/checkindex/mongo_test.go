package checkindex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

func TestDirectOptionsPreserveSecurityWithoutSRVLookup(t *testing.T) {
	p := Profile{Credentials: Credentials{InitialURI: "mongodb+srv://unused.invalid/db?authSource=old&retryWrites=true", Username: "u@x", Password: "p:/@"}, DBName: "db", AuthSource: "admin", HostMapping: map[string]string{"internal:27017": "localhost:27018"}}
	o, e := clientOptions(p, "internal:27017")
	if e != nil {
		t.Fatal(e)
	}
	if len(o.Hosts) != 1 || o.Hosts[0] != "localhost:27018" || o.Direct == nil || !*o.Direct {
		t.Fatal("not mapped/direct")
	}
	if o.TLSConfig == nil || o.TLSConfig.InsecureSkipVerify || o.Auth.Username != "u@x" || o.Auth.Password != "p:/@" || o.Auth.AuthSource != "admin" {
		t.Fatal("TLS/auth not preserved")
	}
	if o.ReadPreference.Mode() != readpref.SecondaryPreferredMode {
		t.Fatal("node read preference")
	}
	p.InitialURI = "mongodb://localhost:27017/?tls=false"
	o, e = clientOptions(p, "")
	if e != nil || o.Direct != nil {
		t.Fatalf("seed options: %v", e)
	}
	p.InitialURI = "not a uri"
	if _, e = clientOptions(p, "node:27017"); e == nil {
		t.Fatal("malformed URI accepted")
	}
}

func TestBSONOrderedKeysAndInt64Decode(t *testing.T) {
	raw, e := bson.Marshal(bson.D{{Key: "name", Value: "compound"}, {Key: "key", Value: bson.D{{Key: "z", Value: 1}, {Key: "a", Value: -1}}}, {Key: "accesses", Value: bson.D{{Key: "ops", Value: int64(9007199254740993)}}}})
	if e != nil {
		t.Fatal(e)
	}
	var stat IndexStat
	if e = bson.Unmarshal(raw, &stat); e != nil {
		t.Fatal(e)
	}
	if stat.Accesses.Ops != 9007199254740993 || stat.Key[0].Key != "z" || stat.Key[1].Key != "a" {
		t.Fatal("BSON precision/order lost")
	}
}

func TestMarkdownFileUsesActualDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.md")
	seed := &fakeDB{hello: Hello{Me: "local:27017"}, names: []string{"items"}}
	node := &fakeDB{rows: []IndexStat{{Name: "_id_", Key: bson.D{{Key: "_id", Value: 1}}, Accesses: Accesses{Ops: 3}}}}
	var out strings.Builder
	e := collect(context.Background(), Profile{DBName: "realDB"}, Options{File: path}, &out, func(_ context.Context, _ Profile, h string) (Database, error) {
		if h == "" {
			return seed, nil
		}
		return node, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	text := string(data)
	if !strings.Contains(text, "## realDB.items") || !strings.Contains(text, "Final sum of operations: 3") || strings.Contains(out.String(), "| Index") {
		t.Fatal("bad export")
	}
}
