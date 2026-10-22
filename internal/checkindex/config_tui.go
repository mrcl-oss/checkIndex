package checkindex

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type configScreen int

const (
	screenMain configScreen = iota
	screenShared
	screenProfiles
	screenProfile
	screenEditField
	screenConfirm
	screenHostMappings
)

type configModel struct {
	config, original              Config
	screen, previous              configScreen
	cursor                        int
	field, value, profile, action string
	errorText                     string
	done, save                    bool
}

func newConfigModel(c Config) configModel { return configModel{config: cloneConfig(c), original: c} }
func (m configModel) Init() tea.Cmd       { return nil }
func (m configModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	key := k.String()
	if key == "ctrl+c" {
		m.done, m.save = true, false
		return m, tea.Quit
	}
	if m.screen == screenEditField {
		switch key {
		case "esc":
			m.screen = m.previous
			m.value = ""
			return m, nil
		case "enter":
			m.commitField()
			return m, nil
		case "backspace", "ctrl+h":
			if len(m.value) > 0 {
				runes := []rune(m.value)
				m.value = string(runes[:len(runes)-1])
			}
			return m, nil
		default:
			if len(k.Runes) > 0 {
				m.value += string(k.Runes)
			}
			return m, nil
		}
	}
	if m.screen == screenConfirm {
		if key == "y" || key == "enter" {
			delete(m.config.Cluster, m.profile)
			m.screen = screenProfiles
		}
		if key == "n" || key == "esc" {
			m.screen = screenProfiles
		}
		return m, nil
	}
	if key == "esc" {
		switch m.screen {
		case screenMain:
			m.done, m.save = true, false
			return m, tea.Quit
		case screenShared, screenProfiles, screenHostMappings:
			m.screen, m.cursor = screenMain, 0
		case screenProfile:
			m.screen, m.cursor = screenProfiles, 0
		default:
			m.screen, m.cursor = screenMain, 0
		}
		return m, nil
	}
	items := m.items()
	switch key {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j", "tab":
		if m.cursor < len(items)-1 {
			m.cursor++
		}
	case "shift+tab":
		if m.cursor > 0 {
			m.cursor--
		}
	case "enter", " ":
		cmd := m.activate()
		return m, cmd
	case "a":
		switch m.screen {
		case screenProfiles:
			m.beginField("Profile name", "", "profile name")
		case screenHostMappings:
			m.beginField("Host mapping key", "", "host-key")
		}
	case "d":
		if m.screen == screenHostMappings {
			hosts := sortedHostMappingKeys(m.config.HostMapping)
			if m.cursor < len(hosts) {
				delete(m.config.HostMapping, hosts[m.cursor])
				if m.cursor >= len(hosts)-1 {
					m.cursor = len(hosts) - 2
				}
				if m.cursor < 0 {
					m.cursor = 0
				}
			}
		}
		if m.screen == screenProfile {
			m.action = "delete"
			if len(m.config.Cluster) == 1 {
				m.errorText = "Cannot delete the final profile."
			} else {
				m.screen = screenConfirm
			}
		}
	}
	return m, nil
}
func (m configModel) items() []string {
	switch m.screen {
	case screenMain:
		return []string{"Shared settings", "Host mappings", "Cluster profiles", "Save and quit", "Quit without saving"}
	case screenShared:
		return []string{fmt.Sprintf("Database name: %s", m.config.DBName), fmt.Sprintf("Authentication source: %s", m.config.AuthSource), "Back"}
	case screenHostMappings:
		hosts := sortedHostMappingKeys(m.config.HostMapping)
		items := make([]string, 0, len(hosts)+2)
		for _, host := range hosts {
			items = append(items, host+" → "+m.config.HostMapping[host])
		}
		return append(append(items, "Add mapping"), "Back")
	case screenProfiles:
		names := make([]string, 0, len(m.config.Cluster))
		for n := range m.config.Cluster {
			names = append(names, n)
		}
		sort.Strings(names)
		return append(append(names, "Add profile"), "Back")
	case screenProfile:
		p := m.config.Cluster[m.profile]
		pass := "<empty>"
		if p.Password != "" {
			pass = "<configured>"
		}
		return []string{"URI: " + redactURI(p.InitialURI), "Username: " + p.Username, "Password: " + pass, "Delete profile", "Back"}
	}
	return nil
}
func sortedHostMappingKeys(mapping map[string]string) []string {
	hosts := make([]string, 0, len(mapping))
	for host := range mapping {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}
func (m *configModel) activate() tea.Cmd {
	items := m.items()
	if m.cursor >= len(items) {
		return nil
	}
	switch m.screen {
	case screenMain:
		switch m.cursor {
		case 0:
			m.screen = screenShared
		case 1:
			m.screen = screenHostMappings
			m.cursor = 0
		case 2:
			m.screen = screenProfiles
			m.cursor = 0
		case 3:
			if err := validateConfig(m.config); err != nil {
				m.errorText = err.Error()
			} else {
				m.done = true
				m.save = true
				return tea.Quit
			}
		case 4:
			m.done = true
			m.save = false
			return tea.Quit
		}
	case screenShared:
		switch m.cursor {
		case 0:
			m.beginField("Database name", m.config.DBName, "dbName")
		case 1:
			m.beginField("Authentication source", m.config.AuthSource, "authSource")
		default:
			m.screen = screenMain
			m.cursor = 0
		}
	case screenHostMappings:
		hosts := sortedHostMappingKeys(m.config.HostMapping)
		if m.cursor < len(hosts) {
			m.profile = hosts[m.cursor]
			m.beginField("Host mapping value", m.config.HostMapping[m.profile], "host-value")
		} else if m.cursor == len(hosts) {
			m.beginField("Host mapping key", "", "host-key")
		} else {
			m.screen = screenMain
			m.cursor = 0
		}
	case screenProfiles:
		if m.cursor == len(m.config.Cluster) {
			m.beginField("Profile name", "", "profile name")
		} else if m.cursor < len(m.config.Cluster) {
			names := make([]string, 0, len(m.config.Cluster))
			for n := range m.config.Cluster {
				names = append(names, n)
			}
			sort.Strings(names)
			m.profile = names[m.cursor]
			m.screen = screenProfile
			m.cursor = 0
		} else {
			m.screen = screenMain
			m.cursor = 0
		}
	case screenProfile:
		switch m.cursor {
		case 0:
			m.beginField("URI", m.config.Cluster[m.profile].InitialURI, "uri")
		case 1:
			m.beginField("Username", m.config.Cluster[m.profile].Username, "username")
		case 2:
			m.beginField("Password", "", "password")
		case 3:
			if len(m.config.Cluster) > 1 {
				m.screen = screenConfirm
			} else {
				m.errorText = "Cannot delete the final profile."
			}
		case 4:
			m.screen = screenProfiles
			m.cursor = 0
		}
	}
	return nil
}
func (m *configModel) beginField(label, value, field string) {
	m.previous = m.screen
	m.screen = screenEditField
	m.field = field
	m.value = value
}
func (m *configModel) commitField() {
	switch m.field {
	case "dbName":
		m.config.DBName = m.value
	case "authSource":
		m.config.AuthSource = m.value
	case "uri":
		p := m.config.Cluster[m.profile]
		p.InitialURI = m.value
		m.config.Cluster[m.profile] = p
	case "username":
		p := m.config.Cluster[m.profile]
		p.Username = m.value
		m.config.Cluster[m.profile] = p
	case "password":
		p := m.config.Cluster[m.profile]
		p.Password = m.value
		m.config.Cluster[m.profile] = p
	case "profile name":
		if strings.TrimSpace(m.value) != "" && m.value == strings.TrimSpace(m.value) {
			if _, exists := m.config.Cluster[m.value]; exists {
				m.errorText = "Profile already exists."
				return
			}
			m.profile = m.value
			m.config.Cluster[m.profile] = Credentials{}
			m.screen = screenProfile
			m.cursor = 0
			m.value = ""
			return
		}
	case "host-key":
		if strings.TrimSpace(m.value) == "" {
			m.errorText = "Host mapping key cannot be blank."
			return
		}
		if _, exists := m.config.HostMapping[m.value]; exists {
			m.errorText = "Host mapping key already exists."
			return
		}
		m.profile = m.value
		m.screen = m.previous
		m.beginField("Host mapping value", "", "host-new-value")
		return
	case "host-new-value":
		if strings.TrimSpace(m.value) == "" {
			m.errorText = "Host mapping value cannot be blank."
			return
		}
		if m.config.HostMapping == nil {
			m.config.HostMapping = make(map[string]string)
		}
		m.config.HostMapping[m.profile] = m.value
	case "host-value":
		if strings.TrimSpace(m.value) == "" {
			m.errorText = "Host mapping value cannot be blank."
			return
		}
		m.config.HostMapping[m.profile] = m.value
	}
	m.screen = m.previous
	m.value = ""
}
func (m configModel) View() string {
	if m.done {
		return ""
	}
	titles := map[configScreen]string{screenMain: "Configuration", screenShared: "Shared settings", screenHostMappings: "Host mappings", screenProfiles: "Cluster profiles", screenProfile: "Profile: " + m.profile, screenEditField: "Edit setting", screenConfirm: "Delete profile?"}
	var b strings.Builder
	b.WriteString(titles[m.screen] + "\n\n")
	if m.screen == screenEditField {
		display := m.value
		if m.field == "password" {
			display = strings.Repeat("•", len(m.value))
		}
		fmt.Fprintf(&b, "%s: %s\n", m.field, display)
		b.WriteString("Enter: apply   Esc: back\n")
		return b.String()
	}
	if m.screen == screenConfirm {
		b.WriteString("Delete " + m.profile + "?  y: confirm   n/Esc: cancel\n")
		return b.String()
	}
	for i, item := range m.items() {
		prefix := "  "
		if i == m.cursor {
			prefix = "> "
		}
		fmt.Fprintf(&b, "%s%s\n", prefix, item)
	}
	b.WriteString("\n↑/↓ or Tab: navigate   Enter: select   Esc: back/cancel")
	if m.screen == screenProfiles {
		b.WriteString("   a: add")
	}
	if m.screen == screenProfile {
		b.WriteString("   d: delete")
	}
	if m.screen == screenHostMappings {
		b.WriteString("   a: add   d: remove")
	}
	if m.errorText != "" {
		b.WriteString("\n" + m.errorText)
	}
	return b.String()
}
