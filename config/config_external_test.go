package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mrcl-oss/checkIndex/config"
)

func TestExternalConfigAPIValidatesAndRoundTripsJSON(t *testing.T) {
	got := config.Config{
		DBName:      "inventory",
		AuthSource:  "admin",
		HostMapping: map[string]string{"mongo.internal": "db.internal"},
		Cluster: map[string]config.Credentials{
			"production": {
				InitialURI: "mongodb://mongo.internal:27017",
				Username:   "app-user",
				Password:   "secret",
			},
		},
	}
	if err := config.ValidateConfig(got); err != nil {
		t.Fatalf("ValidateConfig(valid config): %v", err)
	}

	path := filepath.Join(t.TempDir(), "private", "config.json")
	if err := config.SaveConfig(path, got); err != nil {
		t.Fatalf("SaveConfig(): %v", err)
	}

	loaded, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig(): %v", err)
	}
	if !reflect.DeepEqual(loaded, got) {
		t.Fatalf("round trip mismatch: got %#v, want %#v", loaded, got)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"dbName"`, `"authSource"`, `"hostMapping"`, `"cluster"`, `"initialUri"`} {
		if !strings.Contains(string(data), field) {
			t.Errorf("saved JSON missing existing field %s: %s", field, data)
		}
	}
	var encoded map[string]any
	if err := json.Unmarshal(data, &encoded); err != nil {
		t.Fatalf("saved config is not JSON: %v", err)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if gotMode := fileInfo.Mode().Perm(); gotMode != 0600 {
		t.Errorf("config mode = %04o, want 0600", gotMode)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if gotMode := dirInfo.Mode().Perm(); gotMode != 0700 {
		t.Errorf("config directory mode = %04o, want 0700", gotMode)
	}
}

func TestExternalConfigAPIRejectsInvalidConfigAndSymlink(t *testing.T) {
	invalid := config.Config{DBName: "", AuthSource: "admin", HostMapping: map[string]string{}, Cluster: map[string]config.Credentials{"production": {}}}
	if err := config.ValidateConfig(invalid); err == nil {
		t.Fatal("ValidateConfig() accepted an invalid config")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte("do not replace"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "link.json")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	valid := config.Config{DBName: "inventory", AuthSource: "admin", HostMapping: map[string]string{}, Cluster: map[string]config.Credentials{"production": {InitialURI: "mongodb://db", Username: "user", Password: "secret"}}}
	if err := config.SaveConfig(path, valid); err == nil {
		t.Fatal("SaveConfig() replaced a symlink config path")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "do not replace" {
		t.Fatalf("symlink target changed: %q", data)
	}
}
