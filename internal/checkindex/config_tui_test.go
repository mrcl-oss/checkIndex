package checkindex

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestConfigTUIKeyboardNavigationAndSecretRendering(t *testing.T) {
	c := Config{DBName: "app", AuthSource: "admin", HostMapping: map[string]string{}, Cluster: map[string]Credentials{"prod": {InitialURI: "mongodb://uri-user:uri-pass@db/app", Username: "alice", Password: "password-secret"}}}
	m := newConfigModel(c)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(configModel)
	if m.cursor != 1 {
		t.Fatalf("down cursor=%d, want 1", m.cursor)
	}
	view := m.View()
	for _, secret := range []string{"password-secret", "uri-user", "uri-pass"} {
		if strings.Contains(view, secret) {
			t.Errorf("view leaked %q", secret)
		}
	}
}

func TestConfigTUIAddProfileAndCancel(t *testing.T) {
	m := newConfigModel(Config{DBName: "db", AuthSource: "admin", HostMapping: map[string]string{}, Cluster: map[string]Credentials{"local": {}}})
	m.screen = screenProfiles
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(configModel)
	if m.screen != screenEditField {
		t.Fatalf("add profile screen=%v", m.screen)
	}
	if m.config.Cluster["local"].InitialURI != "" {
		t.Fatal("unexpected mutation")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(configModel)
	if m.screen != screenProfiles || m.done {
		t.Fatalf("escape should return to profiles: screen=%v done=%v", m.screen, m.done)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(configModel)
	if m.screen != screenMain || m.done {
		t.Fatalf("escape should return to root: screen=%v done=%v", m.screen, m.done)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(configModel)
	if !m.done || m.save {
		t.Fatalf("escape should cancel: done=%v save=%v", m.done, m.save)
	}
}

func TestConfigTUIAddProfileEditFieldsAndSaveDraft(t *testing.T) {
	m := newConfigModel(Config{DBName: "db", AuthSource: "admin", HostMapping: map[string]string{}, Cluster: map[string]Credentials{"local": {}}})
	press := func(key tea.KeyType, runes ...rune) {
		t.Helper()
		next, _ := m.Update(tea.KeyMsg{Type: key, Runes: runes})
		m = next.(configModel)
	}
	press(tea.KeyDown)
	press(tea.KeyDown)
	press(tea.KeyEnter)
	press(tea.KeyDown)
	press(tea.KeyEnter)
	for _, r := range "remote" {
		press(tea.KeyRunes, r)
	}
	press(tea.KeyEnter)
	for _, field := range []struct {
		cursor int
		text   string
	}{{0, "mongodb://db"}, {1, "alice"}, {2, "secret"}} {
		m.cursor = field.cursor
		press(tea.KeyEnter)
		for _, r := range field.text {
			press(tea.KeyRunes, r)
		}
		press(tea.KeyEnter)
	}
	if got := m.config.Cluster["remote"]; got.InitialURI != "mongodb://db" || got.Username != "alice" || got.Password != "secret" {
		t.Fatalf("draft profile = %#v", got)
	}
	press(tea.KeyEsc)
	press(tea.KeyEsc)
	m.cursor = 3
	press(tea.KeyDown)
	press(tea.KeyUp)
	press(tea.KeyEnter)
	if !m.done || !m.save {
		t.Fatalf("save state done=%v save=%v", m.done, m.save)
	}
	if m.original.Cluster["remote"].Password != "" {
		t.Fatal("draft mutated persisted input")
	}
}

func TestConfigTUIEscapeReturnsToParentThenCancelsAtRoot(t *testing.T) {
	m := newConfigModel(Config{DBName: "db", AuthSource: "admin", HostMapping: map[string]string{}, Cluster: map[string]Credentials{"local": {}}})
	press := func(key tea.KeyType) { next, _ := m.Update(tea.KeyMsg{Type: key}); m = next.(configModel) }
	press(tea.KeyEnter) // shared settings
	press(tea.KeyEsc)
	if m.screen != screenMain || m.done {
		t.Fatalf("escape nested: screen=%v done=%v", m.screen, m.done)
	}
	press(tea.KeyEsc)
	if !m.done || m.save {
		t.Fatalf("escape root: done=%v save=%v", m.done, m.save)
	}
}

func TestConfigTUICompletionQuitsProgramAndBackNavigates(t *testing.T) {
	base := Config{DBName: "db", AuthSource: "admin", HostMapping: map[string]string{}, Cluster: map[string]Credentials{"local": {}}}
	t.Run("profile back", func(t *testing.T) {
		m := newConfigModel(base)
		m.screen = screenProfiles
		m.cursor = len(m.items()) - 1
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(configModel)
		if m.screen != screenMain || m.done || cmd != nil {
			t.Fatalf("screen=%v done=%v cmd=%v", m.screen, m.done, cmd)
		}
	})
	for _, tc := range []struct {
		name  string
		setup func(*configModel)
		key   tea.KeyType
		save  bool
	}{
		{"save", func(m *configModel) { m.cursor = 3 }, tea.KeyEnter, true},
		{"root escape cancel", func(m *configModel) {}, tea.KeyEsc, false},
		{"ctrl+c cancel", func(m *configModel) {}, tea.KeyCtrlC, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newConfigModel(base)
			tc.setup(&m)
			next, cmd := m.Update(tea.KeyMsg{Type: tc.key})
			m = next.(configModel)
			if !m.done || m.save != tc.save || cmd == nil {
				t.Fatalf("done=%v save=%v cmd=%v", m.done, m.save, cmd)
			}
			if msg := cmd(); msg == nil {
				t.Fatal("quit command returned nil")
			} else if _, ok := msg.(tea.QuitMsg); !ok {
				t.Fatalf("command message=%T, want tea.QuitMsg", msg)
			}
			if !tc.save && m.config.DBName != base.DBName {
				t.Fatal("cancel changed config")
			}
		})
	}
}

func TestConfigTUIHostMappingsCanBeViewedEditedAndRemoved(t *testing.T) {
	m := newConfigModel(Config{HostMapping: map[string]string{"z.example:27017": "z.local:27017", "a.example:27017": "a.local:27017"}, Cluster: map[string]Credentials{"local": {}}})
	press := func(key tea.KeyType, runes ...rune) {
		t.Helper()
		updated, _ := m.Update(tea.KeyMsg{Type: key, Runes: runes})
		m = updated.(configModel)
	}
	press(tea.KeyDown)
	press(tea.KeyEnter)
	if m.screen != screenHostMappings {
		t.Fatalf("screen=%v, want host mappings", m.screen)
	}
	view := m.View()
	if strings.Index(view, "a.example:27017") > strings.Index(view, "z.example:27017") {
		t.Fatalf("host mappings are not sorted: %s", view)
	}
	press(tea.KeyRunes, 'a')
	for _, r := range "new.example" {
		press(tea.KeyRunes, r)
	}
	press(tea.KeyEnter)
	for _, r := range "new.local" {
		press(tea.KeyRunes, r)
	}
	press(tea.KeyEnter)
	if m.config.HostMapping["new.example"] != "new.local" {
		t.Fatalf("add mapping failed: %#v", m.config.HostMapping)
	}
	press(tea.KeyDown)
	press(tea.KeyEnter)
	for range "new.local" {
		press(tea.KeyBackspace)
	}
	for _, r := range "updated.local" {
		press(tea.KeyRunes, r)
	}
	press(tea.KeyEnter)
	if m.config.HostMapping["new.example"] != "updated.local" {
		t.Fatalf("edit mapping failed: %#v", m.config.HostMapping)
	}
	press(tea.KeyRunes, 'd')
	if _, ok := m.config.HostMapping["new.example"]; ok {
		t.Fatalf("remove mapping failed: %#v", m.config.HostMapping)
	}
}
