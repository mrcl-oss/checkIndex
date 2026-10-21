package checkindex

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigEditorWorkflows(t *testing.T) {
	for _, tc := range []struct {
		name      string
		input     string
		passwords string
		wantErr   bool
		check     func(*testing.T, Config)
	}{
		{name: "shared and profile edit", input: "1\n1\nnewdb\n0\n2\n1\nlocal\n1\nmongodb://newhost\n0\n0\n3\n", check: func(t *testing.T, c Config) {
			if c.DBName != "newdb" || c.Cluster["local"].InitialURI != "mongodb://newhost" {
				t.Fatalf("unexpected config: %+v", c)
			}
		}},
		{name: "add profile", input: "2\n2\nnew\nuri\nuser\n0\n3\n", check: func(t *testing.T, c Config) {
			if c.Cluster["new"].Password != "secret" {
				t.Fatal("profile was not added")
			}
		}},
		{name: "delete profile", input: "2\n1\nlocal\n4\ny\n0\n3\n", check: func(t *testing.T, c Config) {
			if len(c.Cluster) != 1 {
				t.Fatalf("unexpected profiles: %v", c.Cluster)
			}
		}},
		{name: "cannot delete final profile", input: "2\n1\nother\n4\ny\n0\n1\nlocal\n4\ny\n0\n0\n3\n", check: func(t *testing.T, c Config) {
			if len(c.Cluster) != 1 {
				t.Fatal("final profile deletion was not prevented")
			}
		}},
		{name: "edit profile password", input: "2\n1\nlocal\n3\n0\n0\n3\n", passwords: "new-value\n", check: func(t *testing.T, c Config) {
			if c.Cluster["local"].Password != "new-value" {
				t.Fatal("password was not updated")
			}
		}},
		{name: "edit auth source", input: "1\n2\nnew-auth\n0\n3\n", check: func(t *testing.T, c Config) {
			if c.AuthSource != "new-auth" {
				t.Fatal("authSource was not updated")
			}
		}},
		{name: "edit profile username", input: "2\n1\nlocal\n2\nnew-user\n0\n0\n3\n", check: func(t *testing.T, c Config) {
			if c.Cluster["local"].Username != "new-user" {
				t.Fatal("username was not updated")
			}
		}},
		{name: "cancel", input: "4\n", check: func(t *testing.T, c Config) {
			if c.DBName != "db" {
				t.Fatal("cancel changed config")
			}
		}},
		{name: "invalid shared field is rejected", input: "1\n1\n \n0\n3\n", check: func(t *testing.T, c Config) {
			if c.DBName != "db" {
				t.Fatal("invalid field was retained")
			}
		}},
		{name: "host mapping add and remove", input: "1\n3\n1\nmongo\nlocalhost\n3\n2\nmongo\n0\n0\n3\n", check: func(t *testing.T, c Config) {
			if len(c.HostMapping) != 0 {
				t.Fatalf("host mapping was not removed: %v", c.HostMapping)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initial := Config{DBName: "db", AuthSource: "admin", HostMapping: map[string]string{"mongo": "oldhost"}, Cluster: map[string]Credentials{"local": {InitialURI: "mongodb://localhost", Username: "u", Password: "old"}, "other": {}}}
			var out bytes.Buffer
			passwords := tc.passwords
			if passwords == "" {
				passwords = "secret\n"
			}
			passwordScanner := bufio.NewScanner(strings.NewReader(passwords))
			readPassword := func() (string, error) {
				if !passwordScanner.Scan() {
					if err := passwordScanner.Err(); err != nil {
						return "", err
					}
					return "", io.EOF
				}
				return passwordScanner.Text(), nil
			}
			got, save, err := editConfig(strings.NewReader(tc.input), &out, readPassword, initial)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v output=%s", err, out.String())
			}
			if err == nil && tc.check != nil {
				tc.check(t, got)
			}
			if strings.Contains(out.String(), "old\n") || strings.Contains(out.String(), "secret") {
				t.Fatal("password leaked to output")
			}
			if tc.name == "cancel" && save {
				t.Fatal("cancel requested save")
			}
		})
	}
}

func TestConfigEditorDisplaysCurrentValuesWithoutSecrets(t *testing.T) {
	initial := Config{DBName: "appdb", AuthSource: "accounts", HostMapping: map[string]string{"mongo": "db.internal"}, Cluster: map[string]Credentials{
		"zeta":  {InitialURI: "mongodb://uri-user:uri-pass@db.internal/app", Username: "alice", Password: "password-secret"},
		"alpha": {InitialURI: "mongodb://db.internal/other", Username: "bob"},
	}}
	var out bytes.Buffer
	_, _, err := editConfig(strings.NewReader("1\n3\n0\n0\n2\n0\n4\n"), &out, func() (string, error) { return "", nil }, initial)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"dbName [appdb]", "authSource [accounts]", "mongo = db.internal", "alpha", "bob", "zeta", "alice", "<configured>", "mongodb://***:***@db.internal/app"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q: %s", want, got)
		}
	}
	for _, secret := range []string{"password-secret", "uri-user", "uri-pass"} {
		if strings.Contains(got, secret) {
			t.Errorf("secret %q leaked: %s", secret, got)
		}
	}
}

func TestConfigEditorCanStartFromBootstrapAndCancel(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer
	err := runConfigCommand(home, strings.NewReader("4\n"), &out, func() (string, error) { return "unused", nil })
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".checkIndex", "config.json")
	if _, err := loadConfig(path); err != nil {
		t.Fatalf("bootstrap config was not preserved: %v", err)
	}
}

func TestRunConfigCommandUsesInjectedHiddenPasswordReader(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
	}{
		{name: "add profile", input: "2\n2\nnew\nuri\nuser\n0\n3\n"},
		{name: "edit profile", input: "2\n1\nlocal\n3\n0\n0\n3\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.name == "edit profile" {
				path := filepath.Join(home, ".checkIndex", "config.json")
				initial := defaultConfig()
				initial.Cluster = map[string]Credentials{"local": {InitialURI: "mongodb://localhost", Username: "user", Password: "old"}}
				if err := saveConfig(path, initial); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			calls := 0
			const password = "sensitive-test-password"
			err := runConfigCommand(home, strings.NewReader(tc.input), &out, func() (string, error) {
				calls++
				return password, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("password reader called %d times, want 1", calls)
			}
			c, err := loadConfig(filepath.Join(home, ".checkIndex", "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			profile := "new"
			if tc.name == "edit profile" {
				profile = "local"
			}
			if got := c.Cluster[profile].Password; got != password {
				t.Fatalf("saved password mismatch for %q", profile)
			}
			if strings.Contains(out.String(), password) {
				t.Fatal("password leaked to captured output")
			}
		})
	}
}

func TestSaveConfigAtomicValidationAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".checkIndex", "config.json")
	c := Config{DBName: "db", AuthSource: "admin", HostMapping: map[string]string{}, Cluster: map[string]Credentials{"local": {InitialURI: "mongodb://localhost", Username: "u", Password: "secret"}}}
	if err := saveConfig(path, c); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%v err=%v", info, err)
	}
	before, _ := os.ReadFile(path)
	c.DBName = " "
	if err := saveConfig(path, c); err == nil {
		t.Fatal("invalid config saved")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("invalid save changed existing config")
	}
	c.DBName = "db"
	if err := saveConfig(filepath.Join(t.TempDir(), "missing", "config.json"), Config{}); err == nil {
		t.Fatal("write failure not surfaced")
	}
	linkHome := t.TempDir()
	privateDir := filepath.Join(linkHome, "private")
	if err := os.Mkdir(privateDir, 0700); err != nil {
		t.Fatal(err)
	}
	old := []byte("preserve existing config")
	if err := os.WriteFile(filepath.Join(privateDir, "config.json"), old, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkHome, ".checkIndex")
	if err := os.Symlink(privateDir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := saveConfig(filepath.Join(link, "config.json"), c); err == nil {
		t.Fatal("symlink path was accepted")
	}
	after, err = os.ReadFile(filepath.Join(privateDir, "config.json"))
	if err != nil || !bytes.Equal(after, old) {
		t.Fatal("failed write changed existing config")
	}
}

func TestConfigSubcommandRejectsNonTerminalWithoutSideEffects(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer
	called := false
	err := newCommand(home, &out, &out, func(context.Context, Profile, Options, io.Writer) error { called = true; return nil }).Run(context.Background(), []string{"checkindex", "config"})
	if err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("expected clear terminal error, got %v", err)
	}
	if called {
		t.Fatal("config command called reporter")
	}
	if _, err := os.Stat(filepath.Join(home, ".checkIndex")); !os.IsNotExist(err) {
		t.Fatal("non-terminal command touched configuration")
	}
}
