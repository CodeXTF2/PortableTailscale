//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	resolutionField = 10 + iota
	dynamicField
	scaleField
	soundField
	microphoneField
	folderField
	certificateField
	networkField
	timeoutField
)

type rdpOptions struct {
	Resolution  string `json:"resolution"`
	Dynamic     bool   `json:"dynamicResolution"`
	Scale       string `json:"scale"`
	Sound       bool   `json:"sound"`
	Microphone  bool   `json:"microphone"`
	Folder      string `json:"sharedFolder,omitempty"`
	Certificate string `json:"certificate"`
	Network     string `json:"network"`
	Timeout     string `json:"timeoutMs"`
}

var resolutions = []string{"auto", "1280x720", "1600x900", "1920x1080", "2560x1440"}
var scales = []string{"100", "125", "150", "175", "200"}
var certificates = []string{"ignore", "deny"}
var networks = []string{"auto", "lan", "wan", "broadband", "modem"}
var timeouts = []string{"5000", "10000", "30000", "60000"}
var tabNames = []string{"Connection", "Display", "Sharing", "Advanced"}

func defaultRDPOptions() rdpOptions {
	return rdpOptions{Resolution: "auto", Dynamic: true, Scale: "100", Certificate: "ignore", Network: "auto", Timeout: "10000"}
}
func (m *model) initOptions() {
	m.options = defaultRDPOptions()
	m.folder = textinput.New()
	m.folder.Placeholder = "Folder path (blank = off)"
	m.folder.Prompt = ""
	m.folder.CharLimit = 1024
	m.folder.PlaceholderStyle = mutedStyle
	m.folder.Cursor.Style = focusedStyle
}
func (m model) currentOptions() rdpOptions {
	o := m.options
	o.Folder = strings.TrimSpace(m.folder.Value())
	if o.Folder != "" {
		if absolute, err := filepath.Abs(o.Folder); err == nil {
			o.Folder = absolute
		}
	}
	return o
}
func (m model) tabFields() []int {
	switch m.tab {
	case 1:
		return []int{fullscreenField, resolutionField, dynamicField, scaleField, connectField}
	case 2:
		return []int{clipboardField, soundField, microphoneField, folderField, connectField}
	case 3:
		return []int{certificateField, networkField, timeoutField, connectField}
	default:
		return []int{targetField, usernameField, domainField, passwordField, connectField}
	}
}
func (m *model) moveFocus(delta int) {
	fields := m.tabFields()
	for i, f := range fields {
		if f == m.focus {
			m.setFocus(fields[(i+delta+len(fields))%len(fields)])
			return
		}
	}
	m.setFocus(fields[0])
}
func (m *model) switchTab(tab int) { m.tab = (tab + 4) % 4; m.setFocus(m.tabFields()[0]) }
func cycle(value string, values []string, delta int) string {
	for i, v := range values {
		if v == value {
			return values[(i+delta+len(values))%len(values)]
		}
	}
	return values[0]
}
func (m *model) adjustOption(delta int) {
	switch m.focus {
	case fullscreenField:
		m.fullscreen = !m.fullscreen
	case clipboardField:
		m.clipboard = !m.clipboard
	case resolutionField:
		m.options.Resolution = cycle(m.options.Resolution, resolutions, delta)
	case dynamicField:
		m.options.Dynamic = !m.options.Dynamic
	case scaleField:
		m.options.Scale = cycle(m.options.Scale, scales, delta)
	case soundField:
		m.options.Sound = !m.options.Sound
	case microphoneField:
		m.options.Microphone = !m.options.Microphone
	case certificateField:
		m.options.Certificate = cycle(m.options.Certificate, certificates, delta)
	case networkField:
		m.options.Network = cycle(m.options.Network, networks, delta)
	case timeoutField:
		m.options.Timeout = cycle(m.options.Timeout, timeouts, delta)
	}
}
func isOptionField(f int) bool {
	return f == clipboardField || f == fullscreenField || (f >= resolutionField && f <= timeoutField && f != folderField)
}

// tabsBorder draws the panel's top edge with the tab labels set into it,
// starting offset columns in so they line up with the form column.
func (m model) tabsBorder(width, offset int, border lipgloss.TerminalColor) string {
	line := lipgloss.NewStyle().Foreground(border)
	inner := width - 2
	short := []string{"Conn", "Disp", "Share", "Adv"}
	// Try full names, then short ones, then names on the selected tab only.
	var tabs string
	for level := 0; level < 3; level++ {
		var b strings.Builder
		b.WriteString(line.Render("─"))
		for i, name := range tabNames {
			if level > 0 {
				name = short[i]
			}
			label := fmt.Sprintf(" F%d %s ", i+1, name)
			if level == 2 && i != m.tab {
				label = fmt.Sprintf(" F%d ", i+1)
			}
			if i == m.tab {
				b.WriteString(line.Render("┤") + selectedButtonStyle.Render(label) + line.Render("├"))
			} else {
				b.WriteString(line.Render("─") + lipgloss.NewStyle().Foreground(violetColor).Render(label) + line.Render("─"))
			}
		}
		tabs = b.String()
		if lipgloss.Width(tabs) <= inner {
			break
		}
	}
	offset = max(0, min(offset, inner-lipgloss.Width(tabs)))
	fill := max(0, inner-offset-lipgloss.Width(tabs))
	return ansi.Truncate(line.Render("╭"+strings.Repeat("─", offset))+tabs+line.Render(strings.Repeat("─", fill)), width-1, "") + line.Render("╮")
}

// folderTabs draws the tabs as folder tabs standing on the panel's top edge:
// every tab has a lid, and the selected one opens into the panel below.
//
//	  ╭───────────────╮╭────────────╮
//	  │ F1 Connection ││ F2 Display │
//	╭─┘               └┴────────────┴───╮
func (m model) folderTabs(width, offset int, border lipgloss.TerminalColor) string {
	line := lipgloss.NewStyle().Foreground(border)
	inner := width - 2
	short := []string{"Conn", "Disp", "Share", "Adv"}
	// Try full names, then short ones, then names on the selected tab only.
	var labels []string
	for level := 0; level < 3; level++ {
		labels = labels[:0]
		total := 1 // the edge before the first tab
		for i, name := range tabNames {
			if level > 0 {
				name = short[i]
			}
			label := fmt.Sprintf(" F%d %s ", i+1, name)
			if level == 2 && i != m.tab {
				label = fmt.Sprintf(" F%d ", i+1)
			}
			labels = append(labels, label)
			total += lipgloss.Width(label) + 2
		}
		if total <= inner {
			break
		}
	}
	used := 0
	for _, label := range labels {
		used += lipgloss.Width(label) + 2
	}
	offset = max(1, min(offset, inner-used))
	var lid, face, edge strings.Builder
	lid.WriteString(strings.Repeat(" ", offset+1))
	face.WriteString(strings.Repeat(" ", offset+1))
	edge.WriteString(line.Render("╭" + strings.Repeat("─", offset)))
	for i, label := range labels {
		bar := strings.Repeat("─", lipgloss.Width(label))
		lid.WriteString(line.Render("╭" + bar + "╮"))
		if i == m.tab {
			face.WriteString(line.Render("│") + selectedButtonStyle.Render(label) + line.Render("│"))
			edge.WriteString(line.Render("┘" + strings.Repeat(" ", lipgloss.Width(label)) + "└"))
		} else {
			face.WriteString(line.Render("│") + lipgloss.NewStyle().Foreground(violetColor).Render(label) + line.Render("│"))
			edge.WriteString(line.Render("┴" + bar + "┴"))
		}
	}
	edge.WriteString(line.Render(strings.Repeat("─", max(0, inner-offset-used)) + "╮"))
	return strings.Join([]string{lid.String(), face.String(), edge.String()}, "\n")
}
func (m model) optionRows() []string {
	rows := []string{}
	add := func(id int, label, value string) { rows = append(rows, m.row(id, label, value)) }
	o := m.options
	switch m.tab {
	case 1:
		add(fullscreenField, "Full screen", checkbox(m.fullscreen))
		add(resolutionField, "Initial size", "‹ "+o.Resolution+" ›")
		add(dynamicField, "Resize desktop", checkbox(o.Dynamic))
		add(scaleField, "Desktop scale", "‹ "+o.Scale+"% ›")
	case 2:
		add(clipboardField, "Clipboard", checkbox(m.clipboard))
		add(soundField, "Remote audio", checkbox(o.Sound))
		add(microphoneField, "Microphone", checkbox(o.Microphone))
		add(folderField, "Share folder", m.folder.View())
	case 3:
		cert := "Ignore (legacy)"
		if o.Certificate == "deny" {
			cert = "Verify / deny"
		}
		add(certificateField, "Certificate", "‹ "+cert+" ›")
		add(networkField, "Network", "‹ "+o.Network+" ›")
		add(timeoutField, "Timeout (ms)", "‹ "+o.Timeout+" ›")
	default:
		for i, label := range []string{"Computer", "Username", "Domain", "Password"} {
			add(i, label, m.inputs[i].View())
		}
	}
	rows = append(rows, "", m.row(connectField, "", "[ Connect / Retry ]"))
	hints := []string{"F1–F4 or Tab switch tabs · host:port supports custom ports", "←/→ change · Size applies at launch", "Blank folder disables sharing", "Verify rejects untrusted certificates"}
	if m.height >= 22 {
		rows = append(rows, "", mutedStyle.Render(hints[m.tab]))
	}
	if m.height < 22 {
		compact := []string{}
		for _, r := range rows {
			if r != "" {
				compact = append(compact, r)
			}
		}
		rows = compact
	}
	return rows
}
func validateRDPOptions(o rdpOptions) error {
	for _, field := range []struct {
		label, value string
		allowed      []string
	}{{"resolution", o.Resolution, resolutions}, {"scale", o.Scale, scales}, {"certificate", o.Certificate, certificates}, {"network", o.Network, networks}, {"timeout", o.Timeout, timeouts}} {
		found := false
		for _, v := range field.allowed {
			if v == field.value {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unsupported %s setting", field.label)
		}
	}
	if o.Folder != "" {
		if strings.ContainsAny(o.Folder, "\r\n,\"") {
			return fmt.Errorf("shared folder cannot contain commas, quotes or line breaks")
		}
		info, err := os.Stat(o.Folder)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("shared folder must be an existing directory")
		}
	}
	return nil
}
func optionArgs(o rdpOptions) []string {
	args := []string{"/cert:" + o.Certificate, "/scale-desktop:" + o.Scale, "/network:" + o.Network, "/timeout:" + o.Timeout}
	if o.Dynamic {
		args = append(args, "/dynamic-resolution")
	} else {
		args = append(args, "-dynamic-resolution")
	}
	if o.Resolution != "auto" {
		args = append(args, "/size:"+o.Resolution)
	}
	if o.Sound {
		args = append(args, "/sound")
	}
	if o.Microphone {
		args = append(args, "/microphone")
	}
	if o.Folder != "" {
		args = append(args, "/drive:Shared,"+o.Folder)
	}
	return args
}
