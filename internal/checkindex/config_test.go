package checkindex

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutableHelpWithoutHome(t *testing.T) {
	if os.Getenv("CHECKINDEX_HELP_CHILD") == "1" {
		if err := RunCLI([]string{"--help"}, os.Stdout); err != nil {
			os.Exit(1)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestExecutableHelpWithoutHome$")
	cmd.Env = []string{"CHECKINDEX_HELP_CHILD=1"}
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("USAGE:")) {
		t.Fatalf("help without HOME failed: %s %v", out, err)
	}
}

func TestBootstrapPrivateAndNoOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".checkIndex", "config.json")
	_, err := loadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "Edit") {
		t.Fatalf("expected setup instructions: %v", err)
	}
	for p, mode := range map[string]os.FileMode{filepath.Dir(path): 0700, path: 0600} {
		s, e := os.Stat(p)
		if e != nil || s.Mode().Perm() != mode {
			t.Fatalf("permissions %s: %v", p, e)
		}
	}
	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte(`"": {`)) || bytes.Contains(data, []byte("omega")) {
		t.Fatal("bootstrap must contain only blank profile")
	}
	_, _ = loadConfig(path)
	after, _ := os.ReadFile(path)
	if !bytes.Equal(data, after) {
		t.Fatal("existing config overwritten")
	}
}

func TestBootstrapRestrictsExistingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".checkIndex")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	_, _ = loadConfig(filepath.Join(dir, "config.json"))
	s, err := os.Stat(dir)
	if err != nil || s.Mode().Perm() != 0700 {
		t.Fatal("bootstrap directory must be private even when already present")
	}
}

func TestConfigSchemaAndSelection(t *testing.T) {
	valid := `{"dbName":"db","authSource":"admin","hostMapping":{},"cluster":{"alpha":{"initialUri":"mongodb://localhost","username":"u","password":"p"},"omega":{"initialUri":"mongodb://omega","username":"u","password":"p"},"unused":{},"z":{"initialUri":"mongodb://localhost","username":"u","password":"p"}}}`
	c, e := decodeConfig([]byte(valid))
	if e != nil {
		t.Fatal(e)
	}
	p, e := c.selectProfile("", nil)
	if e != nil || p.Name != "alpha" {
		t.Fatalf("default: %+v %v", p, e)
	}
	delete(c.Cluster, "omega")
	p, e = c.selectProfile("", nil)
	if e != nil || p.Name != "alpha" {
		t.Fatal("sorted default")
	}
	for _, input := range []string{valid + `{}`, strings.Replace(valid, `"dbName":"db"`, `"dbName":9`, 1), strings.Replace(valid, `"dbName":"db"`, `"typo":"db"`, 1), `null`} {
		if _, e := decodeConfig([]byte(input)); e == nil {
			t.Fatal("accepted invalid schema")
		}
	}
	for _, replacement := range []string{`"unused":null`, `"unused":{"username":null}`} {
		if _, e := decodeConfig([]byte(strings.Replace(valid, `"unused":{}`, replacement, 1))); e == nil {
			t.Fatal("null profile or explicit null field is not a valid type")
		}
	}
	c, _ = decodeConfig([]byte(valid))
	if _, e = c.selectProfile("unused", nil); e == nil {
		t.Fatal("accepted incomplete selected profile")
	}
	override := "override"
	p, e = c.selectProfile("z", &override)
	if e != nil || p.Password != override {
		t.Fatal("password override")
	}
}

func TestCLIAndHelpNoSideEffects(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer
	if e := run([]string{"--help"}, home, &out, nil); e != nil || !strings.Contains(out.String(), "USAGE:") {
		t.Fatalf("help: %v", e)
	}
	if _, e := os.Stat(filepath.Join(home, ".checkIndex")); !os.IsNotExist(e) {
		t.Fatal("help mutated config")
	}
}

func TestReportCannotOverwriteConfig(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".checkIndex", "config.json")
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	data := []byte(`{"dbName":"db","authSource":"admin","hostMapping":{},"cluster":{"local":{"initialUri":"mongodb://localhost","username":"u","password":"p"}}}`)
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	called := false
	e := run([]string{"--file", path}, home, io.Discard, func(context.Context, Profile, Options, io.Writer) error { called = true; return nil })
	if e == nil || called {
		t.Fatal("report output may overwrite config")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(data, after) {
		t.Fatal("config overwritten")
	}
}
