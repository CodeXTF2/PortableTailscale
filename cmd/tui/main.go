//go:build windows

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xproxy "golang.org/x/net/proxy"
)

type proxyEvent struct {
	Event       string `json:"event"`
	Message     string `json:"message"`
	AuthURL     string `json:"authUrl"`
	ProxyURL    string `json:"proxyUrl"`
	TailscaleIP string `json:"tailscaleIp"`
}

type sessionConfig struct {
	target     string
	username   string
	domain     string
	password   string
	clipboard  bool
	fullscreen bool
}

type sessionEvent struct {
	status string
	err    error
	done   bool
	exit   bool
}

type model struct {
	inputs       []textinput.Model
	focus        int
	clipboard    bool
	fullscreen   bool
	status       string
	running      bool
	quitting     bool
	exitWhenDone bool
	spinner      spinner.Model
	cancel       context.CancelFunc
	events       <-chan sessionEvent
}

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	labelStyle   = lipgloss.NewStyle().Width(24).Foreground(lipgloss.Color("245"))
	focusedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	mutedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

const (
	targetField = iota
	usernameField
	domainField
	passwordField
	clipboardField
	fullscreenField
	connectField
	focusCount
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--smoke-test" {
		m := initialModel()
		if strings.TrimSpace(m.View()) == "" {
			os.Exit(1)
		}
		return
	}

	_ = killChildrenOnExit()

	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Portable Tailscale RDP:", err)
		os.Exit(1)
	}
}

func initialModel() model {
	target := textinput.New()
	target.Placeholder = "Tailscale computer name or IP"
	target.CharLimit = 253
	target.Width = 42
	target.Focus()

	username := textinput.New()
	username.Placeholder = "Windows account name"
	username.CharLimit = 256
	username.Width = 42

	domain := textinput.New()
	domain.Placeholder = "Optional Windows domain"
	domain.CharLimit = 256
	domain.Width = 42

	password := textinput.New()
	password.Placeholder = "Windows password"
	password.CharLimit = 256
	password.Width = 42
	password.EchoMode = textinput.EchoPassword
	password.EchoCharacter = '*'

	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))

	return model{
		inputs:    []textinput.Model{target, username, domain, password},
		clipboard: true,
		status:    "Ready",
		spinner:   spin,
	}
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if m.running {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	case sessionEvent:
		if msg.done {
			m.running = false
			m.cancel = nil
			m.events = nil
			if msg.exit || m.exitWhenDone {
				m.quitting = true
				return m, tea.Quit
			}
			if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
				m.status = "Error: " + msg.err.Error()
			} else if errors.Is(msg.err, context.Canceled) {
				m.status = "Disconnected"
			} else {
				m.status = "Remote Desktop closed"
			}
			m.setFocus(0)
			return m, nil
		}
		if msg.status != "" {
			m.status = msg.status
		}
		return m, waitForSessionEvent(m.events)
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m.quit()
		case "esc":
			return m.quit()
		case "tab", "shift+tab":
			if !m.running {
				delta := 1
				if msg.String() == "shift+tab" {
					delta = -1
				}
				m.setFocus((m.focus + delta + focusCount) % focusCount)
			}
			return m, nil
		case "up", "down":
			if !m.running {
				delta := -1
				if msg.String() == "down" {
					delta = 1
				}
				m.setFocus((m.focus + delta + focusCount) % focusCount)
				return m, nil
			}
		case "enter":
			if !m.running {
				if m.focus != connectField {
					m.setFocus((m.focus + 1) % focusCount)
					return m, nil
				}
				return m.activateFocused()
			}
		case " ":
			if !m.running && (m.focus == clipboardField || m.focus == fullscreenField) {
				return m.activateFocused()
			}
		}
	}

	if !m.running && m.focus < len(m.inputs) {
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) activateFocused() (tea.Model, tea.Cmd) {
	switch m.focus {
	case clipboardField:
		m.clipboard = !m.clipboard
		return m, nil
	case fullscreenField:
		m.fullscreen = !m.fullscreen
		return m, nil
	case connectField:
		target := strings.TrimSpace(m.inputs[targetField].Value())
		if target == "" {
			m.status = "Error: enter a Tailscale computer name or IP address"
			m.setFocus(targetField)
			return m, nil
		}
		username := strings.TrimSpace(m.inputs[usernameField].Value())
		if username == "" {
			m.status = "Error: enter the Windows username"
			m.setFocus(usernameField)
			return m, nil
		}
		config := sessionConfig{
			target:     target,
			username:   username,
			domain:     strings.TrimSpace(m.inputs[domainField].Value()),
			password:   m.inputs[passwordField].Value(),
			clipboard:  m.clipboard,
			fullscreen: m.fullscreen,
		}
		ctx, cancel := context.WithCancel(context.Background())
		events := make(chan sessionEvent, 16)
		m.cancel = cancel
		m.events = events
		m.running = true
		m.exitWhenDone = false
		m.status = "Starting portable Tailscale..."
		for i := range m.inputs {
			m.inputs[i].Blur()
		}
		return m, tea.Batch(startSession(ctx, config, events), m.spinner.Tick)
	}
	return m, nil
}

func (m *model) setFocus(index int) {
	m.focus = index
	for i := range m.inputs {
		if i == index {
			m.inputs[i].Focus()
		} else {
			m.inputs[i].Blur()
		}
	}
}

func (m model) quit() (tea.Model, tea.Cmd) {
	if m.running && m.cancel != nil {
		m.cancel()
		m.exitWhenDone = true
		m.status = "Disconnecting..."
		return m, nil
	}
	m.quitting = true
	return m, tea.Quit
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString(banner())
	b.WriteString("\n\n")
	b.WriteString(m.row(targetField, "Computer", m.inputs[targetField].View()))
	b.WriteString("\n")
	b.WriteString(m.row(usernameField, "Username", m.inputs[usernameField].View()))
	b.WriteString("\n")
	b.WriteString(m.row(domainField, "Domain (optional)", m.inputs[domainField].View()))
	b.WriteString("\n")
	b.WriteString(m.row(passwordField, "Password", m.inputs[passwordField].View()))
	b.WriteString("\n\n")
	b.WriteString(m.row(clipboardField, "Share clipboard", checkbox(m.clipboard)))
	b.WriteString("\n")
	b.WriteString(m.row(fullscreenField, "Full screen", checkbox(m.fullscreen)))
	b.WriteString("\n\n")
	b.WriteString(m.row(connectField, "", "[ Connect ]"))
	b.WriteString("\n\n")

	status := m.status
	if m.running {
		status = m.spinner.View() + " " + status
	}
	if strings.HasPrefix(m.status, "Error:") {
		b.WriteString(errorStyle.Render(status))
	} else {
		b.WriteString(status)
	}
	b.WriteString("\n\n")
	if m.running {
		b.WriteString(mutedStyle.Render("Esc: disconnect  |  Ctrl+C: exit"))
	} else {
		b.WriteString(mutedStyle.Render("Up/Down or Tab/Shift+Tab: move"))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("Enter: next/connect  |  Space: toggle  |  Esc: exit"))
	}
	b.WriteString("\n")
	return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
}

// banner draws a small Remote Desktop icon (monitor with a blue screen and a
// green connection badge), an arrow, and the Tailscale logo beside the title.
// Every part is vertically centred on the monitor.
func banner() string {
	bezel := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	screenTop := lipgloss.NewStyle().Foreground(lipgloss.Color("45")).Background(lipgloss.Color("33"))
	screenBottom := lipgloss.NewStyle().Foreground(lipgloss.Color("33")).Background(lipgloss.Color("25"))
	badge := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)

	icon := strings.Join([]string{
		bezel.Render("╭──────╮"),
		bezel.Render("│") + screenTop.Render("▀▀▀▀▀▀") + bezel.Render("│"),
		bezel.Render("│") + screenBottom.Render("▀▀▀▀▀▀") + bezel.Render("│"),
		bezel.Render("╰──┬┬──╯") + badge.Render("⇄"),
		bezel.Render("  ════"),
	}, "\n")
	arrow := badge.Render("──▶")
	// Tailscale logo: a 3x3 dot grid with the middle row and bottom centre lit.
	on := lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Render("●")
	off := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("●")
	logo := strings.Join([]string{
		off + " " + off + " " + off,
		on + " " + on + " " + on,
		off + " " + on + " " + off,
	}, "\n")
	title := strings.Join([]string{
		titleStyle.Render("Portable Tailscale RDP"),
		mutedStyle.Render("RDP through Portable Tailscale"),
	}, "\n")
	return lipgloss.JoinHorizontal(lipgloss.Center, icon, "  ", arrow, "  ", logo, "    ", title)
}

func (m model) row(index int, label, value string) string {
	pointer := "  "
	style := lipgloss.NewStyle()
	if m.focus == index && !m.running {
		pointer = "> "
		style = focusedStyle
	}
	return style.Render(pointer) + labelStyle.Render(label) + value
}

func checkbox(checked bool) string {
	if checked {
		return "[x]"
	}
	return "[ ]"
}

func startSession(ctx context.Context, config sessionConfig, events chan sessionEvent) tea.Cmd {
	return func() tea.Msg {
		go runSession(ctx, config, events)
		return <-events
	}
}

func waitForSessionEvent(events <-chan sessionEvent) tea.Cmd {
	return func() tea.Msg { return <-events }
}

func runSession(ctx context.Context, config sessionConfig, events chan<- sessionEvent) {
	var finalErr error
	var rdpStarted bool
	defer func() { events <- sessionEvent{err: finalErr, done: true, exit: rdpStarted} }()
	emit := func(status string) { events <- sessionEvent{status: status} }

	baseDir, err := executableDirectory()
	if err != nil {
		finalErr = err
		return
	}
	proxyPath := filepath.Join(baseDir, "PortableTailscale.exe")
	freeRDPPath := filepath.Join(baseDir, "freerdp", "sdl-freerdp.exe")
	for _, required := range []string{proxyPath, freeRDPPath} {
		if _, err := os.Stat(required); err != nil {
			finalErr = fmt.Errorf("required program not found: %s", required)
			return
		}
	}

	proxyCtx, stopProxy := context.WithCancel(ctx)
	defer stopProxy()
	proxy := exec.CommandContext(proxyCtx, proxyPath, "--json")
	proxy.Dir = baseDir
	proxy.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	stdout, err := proxy.StdoutPipe()
	if err != nil {
		finalErr = fmt.Errorf("capture proxy output: %w", err)
		return
	}
	proxy.Stderr = io.Discard
	if err := proxy.Start(); err != nil {
		finalErr = fmt.Errorf("start portable Tailscale: %w", err)
		return
	}

	connected, err := waitForProxy(ctx, stdout, emit)
	if err != nil {
		stopProxy()
		_ = proxy.Wait()
		finalErr = err
		return
	}

	bridge, err := startSOCKSBridge(ctx, connected.ProxyURL, rdpAddress(config.target))
	if err != nil {
		finalErr = fmt.Errorf("create local RDP bridge: %w", err)
		stopProxy()
		_ = proxy.Wait()
		return
	}
	defer bridge.Close()

	emit("Connected as " + connected.TailscaleIP + "; opening Remote Desktop...")
	rdpArgs := buildRDPArgs(config, bridge.Address())
	rdp := exec.CommandContext(ctx, freeRDPPath, "/args-from:stdin")
	rdp.Dir = filepath.Dir(freeRDPPath)
	rdp.Stdin = strings.NewReader(strings.Join(rdpArgs, "\n") + "\n")
	rdp.Stdout = io.Discard
	rdp.Stderr = io.Discard
	err = rdp.Start()
	if err != nil {
		finalErr = fmt.Errorf("start FreeRDP: %w", err)
		stopProxy()
		_ = proxy.Wait()
		return
	}
	rdpStarted = true
	err = rdp.Wait()
	stopProxy()
	_ = proxy.Wait()
	if err != nil && ctx.Err() == nil {
		finalErr = fmt.Errorf("FreeRDP exited with an error: %w", err)
	} else {
		finalErr = ctx.Err()
	}
}

func waitForProxy(ctx context.Context, stdout io.Reader, emit func(string)) (proxyEvent, error) {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var event proxyEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		switch event.Event {
		case "starting":
			emit("Starting portable Tailscale...")
		case "auth":
			status := "Complete Tailscale authentication in your browser..."
			if event.AuthURL != "" {
				status += " " + event.AuthURL
			}
			emit(status)
		case "connected":
			if event.ProxyURL == "" {
				return proxyEvent{}, errors.New("portable Tailscale returned no SOCKS proxy address")
			}
			return event, nil
		case "error":
			return proxyEvent{}, errors.New(event.Message)
		}
		select {
		case <-ctx.Done():
			return proxyEvent{}, ctx.Err()
		default:
		}
	}
	if err := scanner.Err(); err != nil {
		return proxyEvent{}, fmt.Errorf("read portable Tailscale output: %w", err)
	}
	if ctx.Err() != nil {
		return proxyEvent{}, ctx.Err()
	}
	return proxyEvent{}, errors.New("portable Tailscale stopped before connecting")
}

func buildRDPArgs(config sessionConfig, endpoint string) []string {
	args := []string{
		"/v:" + endpoint,
		"/server-name:" + serverName(config.target),
		"/cert:ignore",
		"/dynamic-resolution",
		"/log-level:OFF",
		"/u:" + config.username,
		"/p:" + config.password,
	}
	if config.domain != "" {
		args = append(args, "/d:"+config.domain)
	}
	if config.clipboard {
		args = append(args, "+clipboard")
	} else {
		args = append(args, "-clipboard")
	}
	if config.fullscreen {
		args = append(args, "/f")
	}
	return args
}

func serverName(target string) string {
	if host, _, err := net.SplitHostPort(target); err == nil {
		return host
	}
	return strings.Trim(target, "[]")
}

func rdpAddress(target string) string {
	if _, _, err := net.SplitHostPort(target); err == nil {
		return target
	}
	return net.JoinHostPort(target, "3389")
}

type socksBridge struct {
	listener net.Listener
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func startSOCKSBridge(parent context.Context, proxyURL, target string) (*socksBridge, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parse SOCKS URL: %w", err)
	}
	if u.Scheme != "socks5" || u.Host == "" {
		return nil, fmt.Errorf("unsupported SOCKS URL %q", proxyURL)
	}

	var auth *xproxy.Auth
	if u.User != nil {
		password, _ := u.User.Password()
		auth = &xproxy.Auth{User: u.User.Username(), Password: password}
	}
	dialer, err := xproxy.SOCKS5("tcp", u.Host, auth, &net.Dialer{Timeout: 10 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("configure SOCKS5: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen on loopback: %w", err)
	}

	ctx, cancel := context.WithCancel(parent)
	b := &socksBridge{listener: listener, cancel: cancel}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		<-ctx.Done()
		_ = listener.Close()
	}()
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		for {
			local, err := listener.Accept()
			if err != nil {
				return
			}
			b.wg.Add(1)
			go b.forward(ctx, dialer, local, target)
		}
	}()
	return b, nil
}

func (b *socksBridge) Address() string { return b.listener.Addr().String() }

func (b *socksBridge) Close() {
	b.cancel()
	_ = b.listener.Close()
	b.wg.Wait()
}

func (b *socksBridge) forward(ctx context.Context, dialer xproxy.Dialer, local net.Conn, target string) {
	defer b.wg.Done()
	remote, err := dialer.Dial("tcp", target)
	if err != nil {
		_ = local.Close()
		return
	}
	defer local.Close()
	defer remote.Close()

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, local); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, remote); done <- struct{}{} }()
	select {
	case <-ctx.Done():
	case <-done:
	}
}

func executableDirectory() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate launcher: %w", err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve launcher path: %w", err)
	}
	return filepath.Dir(path), nil
}
