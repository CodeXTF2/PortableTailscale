//go:build windows

package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestMenuTabsAndOptionNavigation(t *testing.T) {
	m := isolatedModel(t)
	for tab, key := range []tea.KeyType{tea.KeyF1, tea.KeyF2, tea.KeyF3, tea.KeyF4} {
		m = press(m, key)
		if m.tab != tab {
			t.Fatal("tab shortcut failed")
		}
		fields := m.tabFields()
		for _, f := range fields {
			if m.focus != f {
				t.Fatalf("tab %d focus=%d want=%d", tab, m.focus, f)
			}
			m = press(m, tea.KeyDown)
		}
		if m.focus != fields[0] {
			t.Fatal("navigation did not wrap within tab")
		}
	}
	m = press(m, tea.KeyF2)
	m = press(m, tea.KeyDown)
	m = press(m, tea.KeyRight)
	if m.options.Resolution != "1280x720" {
		t.Fatal("resolution selector failed")
	}
	m = press(m, tea.KeyLeft)
	if m.options.Resolution != "auto" {
		t.Fatal("reverse selection failed")
	}
}

func TestAdvancedArgumentsAndProfiles(t *testing.T) {
	m := isolatedModel(t)
	m.inputs[0].SetValue("host")
	m.inputs[1].SetValue("user")
	m.options = rdpOptions{Resolution: "1920x1080", Scale: "150", Sound: true, Microphone: true, Certificate: "deny", Network: "wan", Timeout: "30000"}
	folder := t.TempDir()
	m.folder.SetValue(folder)
	m.profileName.SetValue("Advanced")
	m.screen = "save"
	m = press(m, tea.KeyEnter)
	saved, err := loadProfiles(m.profilePath)
	if err != nil || len(saved) != 1 || saved[0].Options == nil {
		t.Fatal("advanced profile not saved")
	}
	m.profiles = saved
	m.refreshProfiles()
	m.initOptions()
	m.screen = "profiles"
	m = press(m, tea.KeyEnter)
	opts := m.currentOptions()
	if opts.Dynamic || opts.Scale != "150" || opts.Folder != folder || !opts.Sound {
		t.Fatal("advanced profile failed to round-trip")
	}
	args := buildRDPArgs(sessionConfig{target: "host", username: "user", options: &opts}, "127.0.0.1:1234")
	has := func(want string) bool {
		for _, a := range args {
			if a == want {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"/cert:deny", "-dynamic-resolution", "/size:1920x1080", "/scale-desktop:150", "/sound", "/microphone", "/network:wan", "/timeout:30000", "/drive:Shared," + folder} {
		if !has(want) {
			t.Errorf("missing %s", want)
		}
	}
	if has("/cert:ignore") || has("/dynamic-resolution") {
		t.Fatal("legacy arguments override selected settings")
	}
}

func TestLegacyProfilesAndOptionValidation(t *testing.T) {
	m := isolatedModel(t)
	m.options.Dynamic = false
	m.profiles = []profile{{Name: "Old", Target: "host", Username: "user", Clipboard: true}}
	m.refreshProfiles()
	m.screen = "profiles"
	m = press(m, tea.KeyEnter)
	if m.options != defaultRDPOptions() {
		t.Fatal("legacy profile did not receive defaults")
	}
	opts := defaultRDPOptions()
	opts.Certificate = "ignore\n/p:injected"
	if validateRDPOptions(opts) == nil {
		t.Fatal("accepted invalid option")
	}
	opts = defaultRDPOptions()
	opts.Folder = "C:\\nonexistent-folder-for-portable-rdp-test"
	if validateRDPOptions(opts) == nil {
		t.Fatal("accepted missing shared folder")
	}
}

func TestEveryTabFitsSmallAndWideTerminals(t *testing.T) {
	for _, size := range [][2]int{{44, 18}, {80, 24}, {120, 32}} {
		for tab := 0; tab < 4; tab++ {
			m := isolatedModel(t)
			m.resize(size[0], size[1])
			m.switchTab(tab)
			view := m.View()
			if !strings.Contains(view, "Connect / Retry") {
				t.Fatalf("%v tab%d hides Connect", size, tab)
			}
			if !strings.Contains(view, "F4") {
				t.Fatalf("%v tab%d hides tab shortcuts", size, tab)
			}
			if size[0] >= 80 && !strings.Contains(view, "Esc exit") {
				t.Fatalf("%v tab%d hides footer", size, tab)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatal("overflow")
				}
			}
		}
	}
}

func TestTabKeysSwitchTabs(t *testing.T) {
	m := isolatedModel(t)
	for _, want := range []int{1, 2, 3, 0} {
		m = press(m, tea.KeyTab)
		if m.tab != want || m.focus != m.tabFields()[0] {
			t.Fatalf("tab=%d focus=%d want tab %d", m.tab, m.focus, want)
		}
	}
	m = press(m, tea.KeyF2)
	m = press(m, tea.KeyRight)
	if m.tab != 1 || !m.fullscreen {
		t.Fatal("right should toggle the checkbox, not switch tabs")
	}
}

func TestSaveUsesTypedNameOrComputerSuggestion(t *testing.T) {
	m := isolatedModel(t)
	m.inputs[targetField].SetValue("workstation")
	m.inputs[usernameField].SetValue("alice")
	m = press(m, tea.KeyCtrlS)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Office PC")})
	m = press(next.(model), tea.KeyEnter)
	m.profileName.SetValue("")
	m = press(m, tea.KeyCtrlS)
	m = press(m, tea.KeyEnter)
	saved, err := loadProfiles(m.profilePath)
	if err != nil || len(saved) != 2 || saved[0].Name != "Office PC" || saved[1].Name != "workstation" {
		t.Fatalf("saved %+v", saved)
	}
}

func TestLogonFailureExplainedAndNoiseDropped(t *testing.T) {
	events := make(chan sessionEvent, 8)
	w := newDiagnosticWriter("FreeRDP", "pw", events)
	w.Write([]byte("[WARN][com.winpr.utils.ssl] - [winpr_openssl_initialize]: OpenSSL LEGACY provider failed to load, no md4 support available!\n"))
	w.Write([]byte("[WARN][com.freerdp.core.rdp] - [log_build_warn][0000]: *****\n"))
	w.Write([]byte("[ERROR][com.freerdp.core] - [nla_recv_pdu]: ERRCONNECT_LOGON_FAILURE [0x00020014]\n"))
	if len(events) != 1 || !strings.Contains((<-events).log, "LOGON_FAILURE") {
		t.Fatal("start-up noise not filtered")
	}
	if w.Failure() != errLogonFailed {
		t.Fatal("logon failure not recognised")
	}
	m := isolatedModel(t)
	m.running = true
	m.tab = 2
	next, _ := m.Update(sessionEvent{done: true, err: errLogonFailed})
	m = next.(model)
	if m.tab != 0 || m.focus != passwordField || !strings.Contains(m.status, "username or password") {
		t.Fatalf("logon failure should return to the password, got tab=%d focus=%d %q", m.tab, m.focus, m.status)
	}
}

func TestDiagnosticsWrapWithIndent(t *testing.T) {
	m := isolatedModel(t)
	m.resize(60, 24)
	m.addLog("FreeRDP: " + strings.Repeat("word ", 40))
	lines := strings.Split(m.diagnosticsContent(), "\n")
	if len(lines) < 3 {
		t.Fatal("long event was not wrapped")
	}
	for i, line := range lines {
		if ansi.StringWidth(line) > m.diagnostics.Width {
			t.Fatalf("line %d overflows the viewport", i)
		}
		if i > 0 && !strings.HasPrefix(line, strings.Repeat(" ", 10)) {
			t.Fatalf("continuation line %d not indented", i)
		}
	}
}
