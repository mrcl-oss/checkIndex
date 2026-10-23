package checkindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	var mu sync.Mutex
	var got []string
	err := run([]string{"--all", "--cluster", "missing", "--uri", "broken", "--username", "x", "--password", "x", "--database", "x", "--auth", "x", "--collection", "items", "--hostMapping", "malformed", "--file", ignoredFile}, home, out, func(_ context.Context, p Profile, o Options, w io.Writer) error {
		mu.Lock()
		got = append(got, p.Name)
		mu.Unlock()
		if o.Cluster != "" || o.Collection != "items" || o.File != "" || o.Password != nil || o.Direct || p.InitialURI != "mongodb://"+p.Name || p.Username != "u" {
			t.Errorf("overrides were not ignored: profile=%+v options=%+v", p, o)
		}
		_, _ = io.WriteString(w, p.Name+" report\n")
		return nil
	})
	mu.Lock()
	sort.Strings(got)
	joined := strings.Join(got, ",")
	mu.Unlock()
	idxA := strings.Index(out.String(), "Profile: a")
	idxZ := strings.Index(out.String(), "Profile: z")
	if err != nil || joined != "a,z" || idxA == -1 || idxZ == -1 || idxA > idxZ || !strings.Contains(out.String(), "a report") || !strings.Contains(out.String(), "z report") {
		t.Fatalf("all profiles: got=%v output=%q err=%v", got, out.String(), err)
	}
	if _, statErr := os.Stat(ignoredFile); statErr == nil {
		t.Fatal("--all unexpectedly wrote the requested output file")
	}
}

func TestCLIAllJSONCombinesProfilesAndIgnoresOverrides(t *testing.T) {
	home := writeTestConfig(t, `{"dbName":"db","authSource":"admin","hostMapping":{},"cluster":{"z":{"initialUri":"mongodb://z","username":"u","password":"p"},"a":{"initialUri":"mongodb://a","username":"u","password":"p"}}}`)
	ignored := filepath.Join(t.TempDir(), "ignored.json")
	var out bytes.Buffer
	var mu sync.Mutex
	var calls int
	err := run([]string{"--all", "--json", "--cluster", "missing", "--collection", "contacts", "--file", ignored, "--uri", "broken"}, home, &out, func(_ context.Context, p Profile, o Options, w io.Writer) error {
		mu.Lock()
		calls++
		mu.Unlock()
		if !o.JSON || o.Cluster != "" || o.Collection != "contacts" || o.File != "" {
			t.Fatalf("flags not handled: %+v", o)
		}
		if o.Collection == "contacts" {
			_, _ = fmt.Fprintf(w, `{"contacts":{"shared":{"%s":2,"total":2},"index-%s":{"%s":3,"total":3}}}`, p.Name, p.Name, p.Name)
		} else {
			_, _ = fmt.Fprintf(w, `{"contacts":{"shared":{"%s":2,"total":2},"index-%s":{"%s":3,"total":3}},"other":{"not-contact":{"%s":9,"total":9}}}`, p.Name, p.Name, p.Name, p.Name)
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	var result map[string]any
	if err = json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("combined output is not JSON: %q: %v", out.String(), err)
	}
	if _, err = os.Stat(ignored); !os.IsNotExist(err) {
		t.Fatalf("--file should be ignored: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("collection filter not preserved in combined JSON: %#v", result)
	}
	contacts := result["contacts"].(map[string]any)
	shared := contacts["shared"].(map[string]any)
	indexA := contacts["index-a"].(map[string]any)
	indexZ := contacts["index-z"].(map[string]any)
	if len(contacts) != 3 || len(shared) != 3 || shared["a"] != float64(2) || shared["z"] != float64(2) || shared["total"] != float64(4) {
		t.Fatalf("same index counts/total not combined: %#v", contacts)
	}
	if len(indexA) != 2 || indexA["a"] != float64(3) || indexA["total"] != float64(3) || len(indexZ) != 2 || indexZ["z"] != float64(3) || indexZ["total"] != float64(3) {
		t.Fatalf("distinct index keys not preserved: %#v", contacts)
	}
}

func TestCLIAllForwardsCollectionForTextAndJSON(t *testing.T) {
	home := writeTestConfig(t, `{"dbName":"db","authSource":"admin","hostMapping":{},"cluster":{"a":{"initialUri":"mongodb://a","username":"u","password":"p"},"b":{"initialUri":"mongodb://b","username":"u","password":"p"}}}`)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "text", args: []string{"--all", "--collection", "contacts"}},
		{name: "JSON", args: []string{"--all", "--json", "--collection", "contacts"}},
		{name: "omitted", args: []string{"--all"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			got := make([]string, 0, 2)
			err := run(tc.args, home, io.Discard, func(_ context.Context, _ Profile, options Options, _ io.Writer) error {
				mu.Lock()
				got = append(got, options.Collection)
				mu.Unlock()
				return nil
			})
			mu.Lock()
			defer mu.Unlock()
			want := "contacts"
			if tc.name == "omitted" {
				want = ""
			}
			if err != nil || len(got) != 2 || got[0] != want || got[1] != want {
				t.Fatalf("reporter collections=%v want both %q, err=%v", got, want, err)
			}
		})
	}
}

func TestCLIAllContinuesAfterProfileErrorsAndDefaultRemainsUnchanged(t *testing.T) {
	home := writeTestConfig(t, `{"dbName":"db","authSource":"admin","hostMapping":{},"cluster":{"omega":{"initialUri":"mongodb://omega","username":"u","password":"p"},"alpha":{"initialUri":"mongodb://alpha","username":"u","password":"p"},"z":{"initialUri":"mongodb://z","username":"u","password":"p"}}}`)
	var mu sync.Mutex
	var attempted []string
	err := run([]string{"--all"}, home, io.Discard, func(_ context.Context, p Profile, _ Options, _ io.Writer) error {
		mu.Lock()
		attempted = append(attempted, p.Name)
		mu.Unlock()
		if p.Name == "omega" {
			return errors.New("report failed")
		}
		return nil
	})
	mu.Lock()
	sort.Strings(attempted)
	joined := strings.Join(attempted, ",")
	mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "report failed") || joined != "alpha,omega,z" {
		t.Fatalf("expected aggregated errors and continued reports: attempted=%v err=%v", attempted, err)
	}
	called := ""
	err = run(nil, home, io.Discard, func(_ context.Context, p Profile, _ Options, _ io.Writer) error { called = p.Name; return nil })
	if err != nil || called != "alpha" {
		t.Fatalf("default changed: profile=%q err=%v", called, err)
	}
}

func TestCLIAllRunsConcurrently(t *testing.T) {
	home := writeTestConfig(t, `{"dbName":"db","authSource":"admin","hostMapping":{},"cluster":{"c1":{"initialUri":"mongodb://c1","username":"u","password":"p"},"c2":{"initialUri":"mongodb://c2","username":"u","password":"p"}}}`)
	started := make(chan string, 2)
	proceed := make(chan struct{})

	errCh := make(chan error, 1)
	go func() {
		errCh <- run([]string{"--all"}, home, io.Discard, func(_ context.Context, p Profile, _ Options, _ io.Writer) error {
			started <- p.Name
			<-proceed
			return nil
		})
	}()

	c1 := <-started
	c2 := <-started
	if c1 == c2 {
		t.Fatalf("expected different clusters, got %s and %s", c1, c2)
	}
	close(proceed)
	if err := <-errCh; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCLIAlphabeticalDefaultAndPartialOverrides(t *testing.T) {
	home := writeTestConfig(t, `{"dbName":"base-db","authSource":"base-auth","hostMapping":{"old":"kept"},"cluster":{"z":{"initialUri":"mongodb://z","username":"z-user","password":"z-pass"},"omega":{"initialUri":"mongodb://omega","username":"v-user","password":"v-pass"},"alpha":{"initialUri":"mongodb://alpha","username":"a-user","password":"a-pass"}}}`)
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
