package checkindex

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIHelpTakesPrecedenceOverMalformedArguments(t *testing.T) {
	var out bytes.Buffer
	err := RunCLI([]string{"--cluster", "--bogus", "--help"}, &out)
	if err != nil || !strings.Contains(out.String(), "USAGE:") {
		t.Fatalf("help should win over malformed arguments: err=%v output=%q", err, out.String())
	}
}

func TestCLIHelpUsesConfiguredWriter(t *testing.T) {
	var out bytes.Buffer
	if err := RunCLI([]string{"--help"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--cluster") || !strings.Contains(out.String(), "--file") || !strings.Contains(out.String(), "DATABASE OPTIONS") {
		t.Fatalf("help omitted flags: %s", out.String())
	}
}

func TestCLIMissingFlagValueIsReported(t *testing.T) {
	err := RunCLI([]string{"--cluster"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "cluster") {
		t.Fatalf("expected missing cluster value error, got %v", err)
	}
}

func TestCLIFlagsReachReporter(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".checkIndex")
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	config := `{"dbName":"db","authSource":"admin","hostMapping":{},"cluster":{"local":{"initialUri":"mongodb://localhost","username":"user","password":"stored"}}}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	password := "override"
	for _, tc := range []struct {
		name, filename string
		args           []string
	}{
		{name: "default output", filename: "output.md", args: []string{"--cluster", "local", "-p", password, "-c", "items", "--file"}},
		{name: "custom output", filename: "report.md", args: []string{"--cluster", "local", "--password", password, "--collection", "items", "--file", "report.md"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			called := false
			err := run(tc.args, home, &out, func(_ context.Context, profile Profile, options Options, _ io.Writer) error {
				called = true
				if profile.Name != "local" || options.Password == nil || *options.Password != password || options.Collection != "items" || options.File != tc.filename {
					t.Fatalf("unexpected reporter inputs: profile=%q options=%+v", profile.Name, options)
				}
				return nil
			})
			if err != nil || !called {
				t.Fatalf("reporter not called: called=%v err=%v", called, err)
			}
		})
	}
}

func TestCLIAllProfilesAreSortedLabeledAndIgnoreOverrides(t *testing.T) {
	home := writeTestConfig(t, `{"dbName":"db","authSource":"admin","hostMapping":{},"cluster":{"z":{"initialUri":"mongodb://z","username":"u","password":"p"},"a":{"initialUri":"mongodb://a","username":"u","password":"p"}}}`)
	out := new(strings.Builder)
	ignoredFile := filepath.Join(t.TempDir(), "ignored.md")
	var got []string
	err := run([]string{"--all", "--cluster", "missing", "--uri", "broken", "--username", "x", "--password", "x", "--database", "x", "--auth", "x", "--collection", "items", "--hostMapping", "malformed", "--file", ignoredFile}, home, out, func(_ context.Context, p Profile, o Options, w io.Writer) error {
		got = append(got, p.Name)
		if o.Cluster != "" || o.Collection != "" || o.File != "" || o.Password != nil || o.Direct || p.InitialURI != "mongodb://"+p.Name || p.Username != "u" {
			t.Errorf("overrides were not ignored: profile=%+v options=%+v", p, o)
		}
		_, _ = io.WriteString(w, p.Name+" report\n")
		return nil
	})
	if err != nil || strings.Join(got, ",") != "a,z" || !strings.Contains(out.String(), "Profile: a") || !strings.Contains(out.String(), "Profile: z") || !strings.Contains(out.String(), "a report") || !strings.Contains(out.String(), "z report") {
		t.Fatalf("all profiles: got=%v output=%q err=%v", got, out.String(), err)
	}
	if _, statErr := os.Stat(ignoredFile); statErr == nil {
		t.Fatal("--all unexpectedly wrote the requested output file")
	}
}

func TestCLIAllContinuesAfterProfileErrorsAndDefaultRemainsUnchanged(t *testing.T) {
	home := writeTestConfig(t, `{"dbName":"db","authSource":"admin","hostMapping":{},"cluster":{"valiant":{"initialUri":"mongodb://valiant","username":"u","password":"p"},"alpha":{"initialUri":"mongodb://alpha","username":"u","password":"p"},"z":{"initialUri":"mongodb://z","username":"u","password":"p"}}}`)
	var attempted []string
	err := run([]string{"--all"}, home, io.Discard, func(_ context.Context, p Profile, _ Options, _ io.Writer) error {
		attempted = append(attempted, p.Name)
		if p.Name == "valiant" {
			return errors.New("report failed")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "report failed") || strings.Join(attempted, ",") != "alpha,valiant,z" {
		t.Fatalf("expected aggregated errors and continued reports: attempted=%v err=%v", attempted, err)
	}
	called := ""
	err = run(nil, home, io.Discard, func(_ context.Context, p Profile, _ Options, _ io.Writer) error { called = p.Name; return nil })
	if err != nil || called != "alpha" {
		t.Fatalf("default changed: profile=%q err=%v", called, err)
	}
}

func TestCLIAlphabeticalDefaultAndPartialOverrides(t *testing.T) {
	home := writeTestConfig(t, `{"dbName":"base-db","authSource":"base-auth","hostMapping":{"old":"kept"},"cluster":{"z":{"initialUri":"mongodb://z","username":"z-user","password":"z-pass"},"valiant":{"initialUri":"mongodb://valiant","username":"v-user","password":"v-pass"},"alpha":{"initialUri":"mongodb://alpha","username":"a-user","password":"a-pass"}}}`)
	for _, tc := range []struct {
		name string
		args []string
		want Profile
	}{
		{name: "no overrides", want: Profile{Credentials: Credentials{InitialURI: "mongodb://alpha", Username: "a-user", Password: "a-pass"}, Name: "alpha", DBName: "base-db", AuthSource: "base-auth", HostMapping: map[string]string{"old": "kept"}}},
		{name: "partial overlay", args: []string{"--username", "override-user", "--database", "override-db", "--hostMapping", "new=host"}, want: Profile{Credentials: Credentials{InitialURI: "mongodb://alpha", Username: "override-user", Password: "a-pass"}, Name: "alpha", DBName: "override-db", AuthSource: "base-auth", HostMapping: map[string]string{"old": "kept", "new": "host"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(tc.args, home, io.Discard, func(_ context.Context, got Profile, _ Options, _ io.Writer) error {
				if got.Name != tc.want.Name || got.InitialURI != tc.want.InitialURI || got.Username != tc.want.Username || got.Password != tc.want.Password || got.DBName != tc.want.DBName || got.AuthSource != tc.want.AuthSource || len(got.HostMapping) != len(tc.want.HostMapping) {
					t.Fatalf("profile=%+v want=%+v", got, tc.want)
				}
				for k, v := range tc.want.HostMapping {
					if got.HostMapping[k] != v {
						t.Fatalf("mapping=%v want=%v", got.HostMapping, tc.want.HostMapping)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCLIFullDirectConnectionWorksWithBlankBootstrapConfig(t *testing.T) {
	home := writeTestConfig(t, `{"dbName":"database","authSource":"admin","hostMapping":{"mongo:27017":"localhost:27017"},"cluster":{"":{"initialUri":"","username":"","password":""}}}`)
	args := []string{"--uri", "mongodb://seed/db", "--username", "user", "--password", "secret", "--database", "db", "--auth", "admin"}
	err := run(args, home, io.Discard, func(_ context.Context, p Profile, o Options, _ io.Writer) error {
		if !o.Direct || p.Name != "" || p.InitialURI != "mongodb://seed/db" {
			t.Fatalf("profile=%+v options=%+v", p, o)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCLIPartialConnectionFlagsOverlayOnlyWithNamedProfile(t *testing.T) {
	home := writeTestConfig(t, `{"dbName":"db","authSource":"admin","hostMapping":{},"cluster":{"":{"initialUri":"","username":"","password":""}}}`)
	err := run([]string{"--username", "user"}, home, io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "named profile") {
		t.Fatalf("expected clear no-profile error, got %v", err)
	}
}

func writeTestConfig(t *testing.T, data string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".checkIndex")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestCLIDirectConnectionSkipsMissingConfig(t *testing.T) {
	home := t.TempDir()
	args := []string{"--uri", "mongodb://seed/db", "--username", "user", "--password", "secret", "--database", "db", "--auth", "admin", "--hostMapping", "node:27017=localhost:27017"}
	called := false
	err := run(args, home, &bytes.Buffer{}, func(_ context.Context, profile Profile, _ Options, _ io.Writer) error {
		called = true
		if profile.InitialURI != "mongodb://seed/db" || profile.Username != "user" || profile.Password != "secret" || profile.DBName != "db" || profile.AuthSource != "admin" || profile.HostMapping["node:27017"] != "localhost:27017" {
			t.Fatalf("unexpected direct profile: %+v", profile)
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("reporter call=%v err=%v", called, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".checkIndex", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("direct mode touched config: %v", err)
	}
}

func TestCLIPartialDirectConnectionFailsBeforeConfigBootstrap(t *testing.T) {
	home := t.TempDir()
	err := run([]string{"--uri", "mongodb://seed/db"}, home, &bytes.Buffer{}, nil)
	if err == nil || !strings.Contains(err.Error(), "named profile") {
		t.Fatalf("expected direct requirements error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".checkIndex")); !os.IsNotExist(statErr) {
		t.Fatalf("partial direct mode touched config: %v", statErr)
	}
}

func TestCLIHelpIncludesDirectFlagsWithoutConfigSideEffects(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer
	err := newCommand(home, &out, io.Discard, nil).Run(context.Background(), []string{"checkindex", "--help"})
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--uri", "--username", "--password", "--database", "--auth", "--hostMapping", "--all", "DATABASE OPTIONS", "REPORT OPTIONS"} {
		if !strings.Contains(out.String(), flag) {
			t.Errorf("help missing %q", flag)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".checkIndex")); !os.IsNotExist(err) {
		t.Fatalf("help touched config: %v", err)
	}
}

func TestCLIProfileOverridesAndRepeatedHostMappings(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".checkIndex")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data := `{"dbName":"base","authSource":"base-auth","hostMapping":{"same":"stored","other":"kept"},"cluster":{"local":{"initialUri":"mongodb://stored","username":"stored-user","password":"stored-pass"}}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--cluster", "local", "--uri", "mongodb://override", "--username", "override-user", "--password", "override-pass", "--database", "override-db", "--auth", "override-auth", "--hostMapping", "same=cli", "--hostMapping", "new=host"}
	err := run(args, home, &bytes.Buffer{}, func(_ context.Context, p Profile, _ Options, _ io.Writer) error {
		if p.InitialURI != "mongodb://override" || p.Username != "override-user" || p.Password != "override-pass" || p.DBName != "override-db" || p.AuthSource != "override-auth" || p.HostMapping["same"] != "cli" || p.HostMapping["other"] != "kept" || p.HostMapping["new"] != "host" {
			t.Fatalf("unexpected profile: %+v", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
