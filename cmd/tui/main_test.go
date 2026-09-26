//go:build windows

package main

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestBuildRDPArgs(t *testing.T) {
	got := buildRDPArgs(sessionConfig{
		target:     "home:3389",
		username:   "alice",
		domain:     "DOMAIN",
		password:   "not-on-command-line",
		clipboard:  false,
		fullscreen: true,
	}, "127.0.0.1:49152")
	want := []string{
		"/v:127.0.0.1:49152",
		"/server-name:home",
		"/cert:ignore",
		"/dynamic-resolution",
		"/log-level:OFF",
		"/u:alice",
		"/p:not-on-command-line",
		"/d:DOMAIN",
		"-clipboard",
		"/f",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestRDPAddress(t *testing.T) {
	for input, want := range map[string]string{
		"home":      "home:3389",
		"home:3390": "home:3390",
		"100.1.2.3": "100.1.2.3:3389",
	} {
		if got := rdpAddress(input); got != want {
			t.Errorf("rdpAddress(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestInitialViewAndToggle(t *testing.T) {
	m := initialModel()
	if !strings.Contains(m.View(), "Portable Tailscale RDP") || !strings.Contains(m.View(), "Tailscale computer name or IP") {
		t.Fatal("initial view is missing its title or target placeholder")
	}
	if m.inputs[targetField].Value() != "" {
		t.Fatal("target should not be prefilled")
	}
	m.setFocus(clipboardField)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	got := updated.(model)
	if got.clipboard {
		t.Fatal("clipboard did not toggle off")
	}
}

func TestEnterAlwaysAdvancesUntilConnect(t *testing.T) {
	m := initialModel()
	for want := 1; want <= connectField; want++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = updated.(model)
		if m.focus != want {
			t.Fatalf("enter from row %d focused row %d, want %d", want-1, m.focus, want)
		}
		if m.running {
			t.Fatalf("enter started a connection before Connect was selected")
		}
	}
}

func TestArrowNavigationIsConsistent(t *testing.T) {
	m := initialModel()
	for want := 1; want < focusCount; want++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(model)
		if m.focus != want {
			t.Fatalf("down from row %d focused row %d, want %d", want-1, m.focus, want)
		}
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	if m.focus != 0 {
		t.Fatalf("down from last row focused row %d, want wrap to 0", m.focus)
	}
}

func TestPasswordIsMasked(t *testing.T) {
	m := initialModel()
	m.inputs[passwordField].SetValue("very-secret")
	if strings.Contains(m.View(), "very-secret") {
		t.Fatal("password is visible in the TUI")
	}
}
