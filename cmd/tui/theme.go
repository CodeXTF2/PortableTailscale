//go:build windows

package main

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"golang.org/x/sys/windows"
)

var (
	accentColor         = lipgloss.AdaptiveColor{Light: "25", Dark: "81"}
	violetColor         = lipgloss.AdaptiveColor{Light: "91", Dark: "183"}
	greenColor          = lipgloss.AdaptiveColor{Light: "28", Dark: "84"}
	sectionStyle        = lipgloss.NewStyle().Bold(true).Foreground(violetColor)
	successStyle        = lipgloss.NewStyle().Foreground(greenColor)
	buttonStyle         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255")).Background(lipgloss.Color("25"))
	selectedButtonStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("81"))
	keyStyle            = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
)

// Windows consoles can support ANSI colors without advertising TERM. Enable
// their VT mode and a conservative 256-color profile, respecting opt-outs.
func configureColors() {
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled || os.Getenv("CLICOLOR") == "0" || os.Getenv("TERM") == "dumb" {
		return
	}
	handle := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) != nil {
		return
	}
	if windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) != nil {
		return
	}
	lipgloss.SetColorProfile(termenv.ANSI256)
}

func (m model) bannerHeight() int {
	if m.width >= 64 && m.height >= 22 {
		return 5
	}
	return 3
}

func (m model) headerView() string {
	if m.bannerHeight() == 5 {
		return banner()
	}
	monitor := titleStyle.Render("╭────╮\n│ ▀▀ │\n╰─┬┬─╯")
	dots := lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Render("· · ·\n● ● ●\n· ● ·")
	title := titleStyle.Render("Portable Tailscale RDP") + "\n" + mutedStyle.Render("Remote Desktop, anywhere")
	return lipgloss.JoinHorizontal(lipgloss.Center, monitor, " ", dots, "  ", title)
}

func shortcutLine(s string) string {
	parts := strings.Split(s, " · ")
	for i, part := range parts {
		key, description, ok := strings.Cut(part, " ")
		if ok {
			parts[i] = keyStyle.Render(key) + " " + mutedStyle.Render(description)
		} else {
			parts[i] = mutedStyle.Render(part)
		}
	}
	return strings.Join(parts, mutedStyle.Render(" · "))
}
