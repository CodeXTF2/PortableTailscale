//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type profile struct {
	Name       string      `json:"name"`
	Target     string      `json:"target"`
	Username   string      `json:"username"`
	Domain     string      `json:"domain,omitempty"`
	Clipboard  bool        `json:"clipboard"`
	Fullscreen bool        `json:"fullscreen"`
	Options    *rdpOptions `json:"options,omitempty"`
}

func (p profile) Title() string       { return p.Name }
func (p profile) Description() string { return p.Username + " @ " + p.Target }
func (p profile) FilterValue() string { return p.Name + " " + p.Target + " " + p.Username }

func loadProfiles(path string) ([]profile, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var profiles []profile
	err = json.Unmarshal(data, &profiles)
	return profiles, err
}
func saveProfiles(path string, profiles []profile) error {
	data, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".profiles-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (m *model) initWorkspace() {
	m.profileName = textinput.New()
	m.profileName.Placeholder = "Profile name"
	m.profileName.CharLimit = 80
	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.Foreground(accentColor).BorderForeground(accentColor)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.Foreground(violetColor).BorderForeground(accentColor)
	m.profileList = list.New(nil, delegate, 70, 15)
	m.profileList.Styles.Title = buttonStyle.Padding(0, 1)
	m.profileList.Title = "Saved computers"
	m.profileList.DisableQuitKeybindings()
	m.diagnostics = viewport.New(70, 14)
	base, err := executableDirectory()
	if err == nil {
		m.profilePath = filepath.Join(base, "data", "rdp-profiles.json")
		m.profiles, err = loadProfiles(m.profilePath)
	}
	m.profileLoadErr = err
	if err != nil {
		m.status = "Error: cannot load profiles: " + err.Error()
	}
	m.refreshProfiles()
	m.resize(m.width, m.height)
}
func (m *model) refreshProfiles() {
	m.profileList.ResetFilter()
	items := make([]list.Item, len(m.profiles))
	for i, p := range m.profiles {
		items[i] = p
	}
	m.profileList.SetItems(items)
}
func (m *model) resize(w, h int) {
	m.width, m.height = w, h
	content := max(12, w-8)
	if w >= 110 {
		content -= 30
	}
	for i := range m.inputs {
		m.inputs[i].Width = max(8, content-24)
	}
	m.folder.Width = max(8, content-24)
	m.profileName.Width = max(8, min(60, w-12))
	m.profileList.SetSize(max(10, w-8), max(4, h-9-m.bannerHeight()))
	m.diagnostics.Width = max(10, w-8)
	m.diagnostics.Height = max(3, h-10-m.bannerHeight())
	m.diagnostics.SetContent(m.diagnosticsContent())
}

type clockTick time.Time

func sessionTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return clockTick(t) })
}

func (m *model) addLog(s string) {
	s = redact(s, m.inputs[passwordField].Value())
	m.logs = append(m.logs, time.Now().Format("15:04:05")+"  "+s)
	if len(m.logs) > 250 {
		m.logs = m.logs[len(m.logs)-250:]
	}
	atBottom := m.diagnostics.AtBottom()
	m.diagnostics.SetContent(m.diagnosticsContent())
	if atBottom {
		m.diagnostics.GotoBottom()
	}
}
// diagnosticsContent wraps each event to the viewport width, indenting
// continuation lines under the message so long FreeRDP lines stay readable.
func (m model) diagnosticsContent() string {
	const indent = 10 // "15:04:05  "
	width := max(indent+10, m.diagnostics.Width)
	lines := make([]string, 0, len(m.logs))
	for _, entry := range m.logs {
		wrapped := strings.Split(ansi.Wrap(entry, width, ""), "\n")
		lines = append(lines, wrapped[0])
		for _, rest := range wrapped[1:] {
			for _, part := range strings.Split(ansi.Wrap(rest, width-indent, ""), "\n") {
				lines = append(lines, strings.Repeat(" ", indent)+part)
			}
		}
	}
	return strings.Join(lines, "\n")
}
func (m model) updateScreen(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" && !(m.screen == "profiles" && m.profileList.FilterState() != list.Unfiltered) {
		m.screen = ""
		m.deleteName = ""
		return m, nil
	}
	switch m.screen {
	case "logs":
		if msg.String() == "ctrl+e" {
			path := filepath.Join(filepath.Dir(m.profilePath), "rdp-diagnostics-"+time.Now().Format("20060102-150405.000000000")+".log")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				m.status = "Error: export diagnostics: " + err.Error()
				return m, nil
			}
			if err := os.WriteFile(path, []byte(strings.Join(m.logs, "\n")+"\n"), 0600); err != nil {
				m.status = "Error: export diagnostics: " + err.Error()
			} else {
				m.status = "Exported " + filepath.Base(path) + " in data/"
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.diagnostics, cmd = m.diagnostics.Update(msg)
		return m, cmd
	case "save":
		if msg.String() == "enter" {
			name := strings.TrimSpace(m.profileName.Value())
			if name == "" {
				name = strings.TrimSpace(m.inputs[targetField].Value())
			}
			if name == "" || strings.TrimSpace(m.inputs[targetField].Value()) == "" || strings.TrimSpace(m.inputs[usernameField].Value()) == "" {
				m.status = "Error: name, computer and username are required"
				return m, nil
			}
			if m.profileLoadErr != nil {
				m.status = "Error: repair the unreadable profile file before saving"
				return m, nil
			}
			options := m.currentOptions()
			if err := validateRDPOptions(options); err != nil {
				m.status = "Error: " + err.Error()
				return m, nil
			}
			p := profile{Name: name, Target: strings.TrimSpace(m.inputs[0].Value()), Username: strings.TrimSpace(m.inputs[1].Value()), Domain: strings.TrimSpace(m.inputs[2].Value()), Clipboard: m.clipboard, Fullscreen: m.fullscreen, Options: &options}
			next := append([]profile(nil), m.profiles...)
			found := false
			for i := range next {
				if strings.EqualFold(next[i].Name, name) {
					next[i] = p
					found = true
					break
				}
			}
			if !found {
				next = append(next, p)
			}
			if err := saveProfiles(m.profilePath, next); err != nil {
				m.status = "Error: save profiles: " + err.Error()
				return m, nil
			}
			m.profiles = next
			m.refreshProfiles()
			m.screen = ""
			m.status = "Saved profile: " + name
			return m, nil
		}
		var cmd tea.Cmd
		m.profileName, cmd = m.profileName.Update(msg)
		return m, cmd
	case "delete":
		if msg.String() == "enter" {
			next := make([]profile, 0, len(m.profiles))
			for _, p := range m.profiles {
				if p.Name != m.deleteName {
					next = append(next, p)
				}
			}
			if m.profileLoadErr != nil {
				m.status = "Error: profile file could not be read"
				return m, nil
			}
			if err := saveProfiles(m.profilePath, next); err != nil {
				m.status = "Error: delete profile: " + err.Error()
				return m, nil
			}
			m.profiles = next
			m.refreshProfiles()
			m.screen = "profiles"
			m.status = "Profile deleted"
			m.deleteName = ""
		}
		return m, nil
	case "profiles":
		if m.profileList.FilterState() != list.Filtering {
			if p, ok := m.profileList.SelectedItem().(profile); ok {
				switch msg.String() {
				case "enter":
					m.inputs[0].SetValue(p.Target)
					m.inputs[1].SetValue(p.Username)
					m.inputs[2].SetValue(p.Domain)
					m.inputs[3].SetValue("")
					m.clipboard, m.fullscreen = p.Clipboard, p.Fullscreen
					m.options = defaultRDPOptions()
					if p.Options != nil {
						m.options = *p.Options
					}
					m.folder.SetValue(m.options.Folder)
					m.tab = 0
					m.profileName.SetValue(p.Name)
					m.screen = ""
					m.status = "Loaded " + p.Name
					m.setFocus(passwordField)
					return m, nil
				case "backspace":
					m.deleteName = p.Name
					m.screen = "delete"
					return m, nil
				}
			}
		}
		var cmd tea.Cmd
		m.profileList, cmd = m.profileList.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	if m.width < 44 || m.height < 18 {
		return lipgloss.NewStyle().MaxWidth(max(1, m.width)).Render("Enlarge terminal to 44 × 18.\nCtrl+C exits; Esc disconnects.")
	}
	w := m.width - 6
	header := m.headerView()
	if m.proxyStatus != "" {
		network := successStyle.Render("● " + m.proxyStatus)
		if m.proxyStarting {
			network = m.spinner.View() + " " + sectionStyle.Render(m.proxyStatus)
		}
		header += "\n" + ansi.Truncate(network, w, "…")
	}
	footer := "Tab/F1–F4 tabs · ↑↓ move · Space/←→ change · Enter next · Esc exit"
	var body string
	switch m.screen {
	case "profiles":
		body = m.profileList.View()
		footer = "Enter load · / search · Backspace remove · Esc back"
		if len(m.profiles) == 0 {
			body = titleStyle.Render("Saved computers") + "\n\nNo saved connections yet.\nFill in a connection, then press Ctrl+S to save it.\nPasswords stay out of saved profiles."
			footer = "Esc back to connection"
		}
	case "save":
		body = titleStyle.Render("Save connection") + "\n\n" + labelStyle.Render("Profile name") + m.profileName.View() + "\n\n" + mutedStyle.Render("Type any name, or press Enter to use the suggestion.\nPasswords are never saved.\nAn existing name updates that profile.")
		footer = "Enter save · Esc cancel"
	case "delete":
		body = "Delete profile " + m.deleteName + "?\n\nThe saved settings will be removed."
		footer = "Enter delete · Esc cancel"
	case "logs":
		body = titleStyle.Render("Diagnostics · last 250 events") + "\n" + m.diagnostics.View()
		if len(m.logs) == 0 {
			body = titleStyle.Render("Diagnostics") + "\n\nNo connection events yet.\nStart a connection to see its progress here."
		}
		footer = "↑↓ / PgUp/PgDn scroll · Ctrl+E export · Esc back"
	default:
		rows := m.optionRows()
		body = strings.Join(rows, "\n")
		if m.running {
			footer = "Esc disconnect · Ctrl+L diagnostics · Ctrl+C exit"
		}
		if m.width >= 110 {
			sidebar := []string{sectionStyle.Render("SAVED COMPUTERS"), "", successStyle.Render("◆ Quick Connect")}
			for i, p := range m.profiles {
				if i == 7 {
					break
				}
				sidebar = append(sidebar, titleStyle.Render("› ")+ansi.Truncate(p.Name, 21, "…"))
			}
			if len(m.profiles) == 0 {
				sidebar = append(sidebar, mutedStyle.Render("No saved profiles"))
			}
			sidebar = append(sidebar, "", mutedStyle.Render("Ctrl+P browse"))
			body = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(28).Render(strings.Join(sidebar, "\n")), body)
		}
	}
	borderColor := lipgloss.TerminalColor(accentColor)
	if m.screen != "" {
		borderColor = violetColor
	}
	panelStyle := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(borderColor).Padding(0, 1).Width(w - 2)
	var panel string
	if m.screen == "" {
		// The settings tabs sit in the panel's top edge, above the form column.
		offset := 0
		if m.width >= 110 {
			offset = 29
		}
		// Folder tabs need two extra rows; short terminals keep them in the edge.
		tabs := m.tabsBorder(w, offset, borderColor)
		if m.height >= 24 {
			tabs = m.folderTabs(w, max(1, offset), borderColor)
			body = "\n" + body // breathing room under the tabs
		}
		panel = tabs + "\n" + panelStyle.BorderTop(false).Render(body)
	} else {
		panel = panelStyle.Render(body)
	}
	status := m.status
	if m.running && m.stage != "FreeRDP running" {
		status = m.spinner.View() + " " + status
	}
	status = ansi.Truncate(strings.ReplaceAll(status, "\n", " "), w, "…")
	if strings.HasPrefix(m.status, "Error:") {
		status = errorStyle.Render(status)
	} else {
		status = successStyle.Render(status)
	}
	if m.running {
		status = sectionStyle.Render("● "+m.stage) + mutedStyle.Render(" · "+time.Since(m.started).Truncate(time.Second).String()) + "  " + status
	} else {
		status = successStyle.Render("● ") + status
	}
	nav := "Ctrl+P profiles · Ctrl+S save · Ctrl+N new · Ctrl+L diagnostics"
	if m.width < 65 {
		nav = "^P profiles · ^S save · ^N new · ^L logs"
		if m.screen == "" && !m.running {
			footer = "Tab tabs · ↑↓ move · Space toggle"
		}
	}
	if m.screen != "" || m.running {
		nav = "Ctrl+C exit"
	}
	result := header + "\n" + panel + "\n" + status + "\n" + shortcutLine(nav) + "\n" + shortcutLine(footer)
	lines := strings.Split(result, "\n")
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], m.width-2, "")
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return lipgloss.NewStyle().PaddingLeft(1).Render(strings.Join(lines, "\n"))
}
