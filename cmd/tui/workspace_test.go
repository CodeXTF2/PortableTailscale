//go:build windows

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func isolatedModel(t *testing.T) model {
	t.Helper()
	m := initialModel()
	m.profilePath = filepath.Join(t.TempDir(), "data", "rdp-profiles.json")
	m.profiles = nil
	m.profileLoadErr = nil
	m.refreshProfiles()
	return m
}
func press(m model, key tea.KeyType) model {
	next, _ := m.Update(tea.KeyMsg{Type: key})
	return next.(model)
}

func TestDisconnectAndExitAreDistinct(t *testing.T) {
	for _, quit := range []bool{false, true} {
		m := isolatedModel(t)
		ctx, cancel := context.WithCancel(context.Background())
		m.running, m.cancel = true, cancel
		key := tea.KeyEsc
		if quit {
			key = tea.KeyCtrlC
		}
		m = press(m, key)
		if ctx.Err() == nil || m.exitWhenDone != quit {
			t.Fatal("incorrect cancellation semantics")
		}
		next, _ := m.Update(sessionEvent{done: true, err: context.Canceled})
		m = next.(model)
		if m.quitting != quit || m.running {
			t.Fatal("incorrect completion semantics")
		}
		if !quit && (m.focus != connectField || m.status != "Disconnected") {
			t.Fatal("disconnect must leave reconnect available")
		}
	}
}

func TestFailureRetainsFormAndError(t *testing.T) {
	m := isolatedModel(t)
	m.running = true
	m.inputs[0].SetValue("workstation")
	next, _ := m.Update(sessionEvent{done: true, err: errors.New("authentication failed")})
	m = next.(model)
	if m.quitting || m.running || m.stage != "Failed" || !strings.Contains(m.View(), "authentication failed") || m.inputs[0].Value() != "workstation" {
		t.Fatal("failure lost usable form")
	}
}

func TestProfilesSaveUpdateLoadAndDelete(t *testing.T) {
	m := isolatedModel(t)
	m.inputs[0].SetValue("workstation")
	m.inputs[1].SetValue("alice")
	m.inputs[3].SetValue("NEVER-PERSIST-THIS")
	m.profileName.SetValue("Office")
	m.screen = "save"
	m = press(m, tea.KeyEnter)
	data, err := os.ReadFile(m.profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "NEVER-PERSIST-THIS") || strings.Contains(string(data), "password") {
		t.Fatal("persisted password")
	}
	m.inputs[0].SetValue("updated")
	m.screen = "save"
	m = press(m, tea.KeyEnter)
	saved, err := loadProfiles(m.profilePath)
	if err != nil || len(saved) != 1 || saved[0].Target != "updated" {
		t.Fatal("profile update failed")
	}
	m.screen = "profiles"
	m = press(m, tea.KeyEnter)
	if m.inputs[3].Value() != "" || m.inputs[0].Value() != "updated" {
		t.Fatal("profile load must clear password")
	}
	m.screen = "profiles"
	m = press(m, tea.KeyBackspace)
	m = press(m, tea.KeyEsc)
	if len(m.profiles) != 1 {
		t.Fatal("cancel deleted a profile")
	}
	m.screen = "profiles"
	m = press(m, tea.KeyBackspace)
	m = press(m, tea.KeyEnter)
	saved, err = loadProfiles(m.profilePath)
	if err != nil || len(saved) != 0 {
		t.Fatal("profile deletion failed")
	}
}

func TestUnreadableProfilesNotOverwritten(t *testing.T) {
	m := isolatedModel(t)
	if err := os.MkdirAll(filepath.Dir(m.profilePath), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("broken json")
	if err := os.WriteFile(m.profilePath, original, 0600); err != nil {
		t.Fatal(err)
	}
	_, m.profileLoadErr = loadProfiles(m.profilePath)
	m.inputs[0].SetValue("host")
	m.inputs[1].SetValue("user")
	m.profileName.SetValue("name")
	m.screen = "save"
	m = press(m, tea.KeyEnter)
	data, _ := os.ReadFile(m.profilePath)
	if string(data) != string(original) {
		t.Fatal("overwrote unreadable profiles")
	}
}

func TestDiagnosticRedactionAcrossWrites(t *testing.T) {
	events := make(chan sessionEvent, 8)
	w := newDiagnosticWriter("FreeRDP", "secret-password", events)
	w.Write([]byte("failed secret-"))
	w.Write([]byte("password socks5://tsnet:token@localhost https://login.tailscale.com/a/token\n"))
	msg := <-events
	if strings.Contains(msg.log, "secret-password") || strings.Contains(msg.log, "token") {
		t.Fatal("secret leaked")
	}
	w.Write([]byte(strings.Repeat("x", 9000) + "\n"))
	if !strings.Contains((<-events).log, "omitted") {
		t.Fatal("unbounded log line")
	}
	m := isolatedModel(t)
	for i := 0; i < 400; i++ {
		m.addLog("event")
	}
	if len(m.logs) != 250 {
		t.Fatal("unbounded log history")
	}
}

func TestLayoutsFitTerminal(t *testing.T) {
	for _, size := range [][2]int{{44, 18}, {80, 24}, {120, 32}} {
		for _, screen := range []string{"", "profiles", "save", "logs", "delete"} {
			m := isolatedModel(t)
			m.resize(size[0], size[1])
			m.screen = screen
			view := m.View()
			if !strings.Contains(view, "╭────") || !strings.Contains(view, "Portable Tailscale RDP") {
				t.Fatalf("%v %s lost banner artwork", size, screen)
			}
			if screen == "" && size[0] >= 80 && !strings.Contains(view, "Esc exit") {
				t.Fatalf("%v clipped footer", size)
			}
			lines := strings.Split(view, "\n")
			if len(lines) > size[1] {
				t.Fatalf("%v %s exceeds height", size, screen)
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("%v %s exceeds width", size, screen)
				}
			}
			if screen == "" && !strings.Contains(view, "Connect / Retry") {
				t.Fatalf("%v hides connect", size)
			}
		}
	}
}

func TestDiagnosticsExport(t *testing.T) {
	m := isolatedModel(t)
	m.inputs[passwordField].SetValue("test-secret")
	m.addLog("test-secret https://login.tailscale.com/a/private")
	m.screen = "logs"
	m = press(m, tea.KeyCtrlE)
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(m.profilePath), "rdp-diagnostics-*.log"))
	if err != nil || len(paths) != 1 {
		t.Fatal("diagnostics were not exported")
	}
	data, err := os.ReadFile(paths[0])
	if err != nil || strings.Contains(string(data), "test-secret") || strings.Contains(string(data), "private") {
		t.Fatal("export contains secret")
	}
}

func TestBridgeCancellationDuringSOCKSHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, e := listener.Accept()
		if e == nil {
			accepted <- c
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge, err := startSOCKSBridge(ctx, "socks5://"+listener.Addr().String(), "example:3389", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	local, err := net.Dial("tcp", bridge.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	select {
	case c := <-accepted:
		defer c.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("SOCKS connection did not start")
	}
	cancel()
	done := make(chan struct{})
	go func() { bridge.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect blocked on SOCKS handshake")
	}
}
