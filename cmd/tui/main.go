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
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
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
	options    *rdpOptions
}

type sessionEvent struct {
	status string
	err    error
	done   bool
	stage  string
	log    string
}

type model struct {
	inputs         []textinput.Model
	focus          int
	clipboard      bool
	fullscreen     bool
	status         string
	running        bool
	quitting       bool
	exitWhenDone   bool
	spinner        spinner.Model
	cancel         context.CancelFunc
	events         <-chan sessionEvent
	width, height  int
	screen         string
	stage          string
	started        time.Time
	logs           []string
	diagnostics    viewport.Model
	profiles       []profile
	profileList    list.Model
	profileName    textinput.Model
	profilePath    string
	profileLoadErr error
	deleteName     string
	proxy          *portableProxy
	proxyStarting  bool
	proxyStatus    string
	tab            int
	options        rdpOptions
	folder         textinput.Model
}

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "25", Dark: "81"})
	labelStyle   = lipgloss.NewStyle().Width(19).Foreground(lipgloss.AdaptiveColor{Light: "238", Dark: "250"})
	focusedStyle = titleStyle
	mutedStyle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "242", Dark: "245"})
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
	configureColors()
	if len(os.Args) == 2 && os.Args[1] == "--smoke-test" {
		m := initialModel()
		if strings.TrimSpace(m.View()) == "" {
			os.Exit(1)
		}
		return
	}

	_ = killChildrenOnExit()

	m := initialModel()
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if finalModel, ok := final.(model); ok {
		finalModel.proxy.close()
	}
	m.proxy.close()
	if err != nil {
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

	m := model{
		inputs:    []textinput.Model{target, username, domain, password},
		clipboard: true,
		status:    "Ready",
		spinner:   spin,
		width:     80, height: 24, stage: "Ready",
		proxy: newPortableProxy(), proxyStarting: true, proxyStatus: "Starting Tailscale…",
	}
	for i := range m.inputs {
		m.inputs[i].Prompt = ""
		m.inputs[i].TextStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "235", Dark: "255"})
		m.inputs[i].PlaceholderStyle = mutedStyle
		m.inputs[i].Cursor.Style = focusedStyle
	}
	m.initOptions()
	m.initWorkspace()
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, startPortableProxy(m.proxy), waitForPortableProxy(m.proxy), m.spinner.Tick)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case proxyMessage:
		if msg.proxy != m.proxy {
			return m, nil
		}
		if msg.event.log != "" {
			m.addLog(msg.event.log)
		}
		if msg.event.status != "" {
			m.proxyStatus = msg.event.status
			m.addLog(msg.event.status)
		}
		if msg.event.stage == "Ready" {
			m.proxyStarting = false
		}
		if msg.event.done {
			m.proxyStarting = false
			m.proxyStatus = "Tailscale stopped · Connect retries"
			if msg.event.err != nil && !errors.Is(msg.event.err, context.Canceled) {
				m.proxyStatus = "Tailscale error · Connect retries"
				m.addLog(msg.event.err.Error())
				if !m.running {
					m.status = "Error: " + redact(msg.event.err.Error())
				}
			}
			return m, nil
		}
		return m, waitForPortableProxy(m.proxy)
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case clockTick:
		if m.running {
			return m, sessionTick()
		}
		return m, nil
	case spinner.TickMsg:
		if m.running || m.proxyStarting {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	case sessionEvent:
		if msg.log != "" {
			m.addLog(msg.log)
		}
		if msg.stage != "" {
			m.stage = msg.stage
		}
		if msg.done {
			m.running = false
			m.cancel = nil
			m.events = nil
			if m.exitWhenDone {
				m.quitting = true
				return m, tea.Quit
			}
			if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
				m.status = "Error: " + redact(msg.err.Error(), m.inputs[passwordField].Value())
			} else if errors.Is(msg.err, context.Canceled) {
				m.status = "Disconnected"
			} else {
				m.status = "Remote Desktop closed"
			}
			m.stage = "Ready"
			if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
				m.stage = "Failed"
			}
			m.addLog(m.status)
			m.setFocus(connectField)
			if errors.Is(msg.err, errLogonFailed) {
				m.tab = 0
				m.setFocus(passwordField)
			}
			return m, nil
		}
		if msg.status != "" {
			m.status = msg.status
			if password := m.inputs[passwordField].Value(); password != "" {
				m.status = strings.ReplaceAll(m.status, password, "[redacted]")
			}
			if strings.Contains(msg.status, "authentication") {
				m.stage = "Awaiting authentication"
			}
			m.addLog(msg.status)
		}
		return m, waitForSessionEvent(m.events)
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m.quit()
		}
		if m.screen != "" {
			return m.updateScreen(msg)
		}
		switch msg.String() {
		case "f1", "f2", "f3", "f4":
			if !m.running {
				m.switchTab(int(msg.String()[1] - '1'))
			}
			return m, nil
		case "ctrl+left", "ctrl+right":
			if !m.running {
				delta := 1
				if msg.String() == "ctrl+left" {
					delta = -1
				}
				m.switchTab(m.tab + delta)
			}
			return m, nil
		case "left", "right":
			if !m.running && isOptionField(m.focus) {
				delta := 1
				if msg.String() == "left" {
					delta = -1
				}
				m.adjustOption(delta)
				return m, nil
			}
		case "ctrl+p":
			if !m.running {
				m.screen = "profiles"
				m.refreshProfiles()
			}
			return m, nil
		case "ctrl+s":
			if !m.running {
				m.screen = "save"
				// Suggest the computer name without pre-typing it, so typing starts a
				// fresh custom name and a blank Enter still accepts the suggestion.
				m.profileName.Placeholder = "Profile name"
				if target := strings.TrimSpace(m.inputs[targetField].Value()); target != "" {
					m.profileName.Placeholder = target
				}
				m.profileName.CursorEnd()
				return m, m.profileName.Focus()
			}
			return m, nil
		case "ctrl+n":
			if !m.running {
				m.initOptions()
				m.resize(m.width, m.height)
				m.tab = 0
				m.profileName.SetValue("")
				for i := range m.inputs {
					m.inputs[i].SetValue("")
				}
				m.clipboard = true
				m.fullscreen = false
				m.status = "New connection"
				m.setFocus(0)
			}
			return m, nil
		case "ctrl+l":
			m.screen = "logs"
			return m, nil
		case "ctrl+c":
			return m.quit()
		case "esc":
			if m.running {
				m.cancel()
				m.stage = "Stopping"
				m.status = "Disconnecting..."
				return m, nil
			}
			return m.quit()
		case "tab":
			if !m.running {
				m.switchTab(m.tab + 1)
			}
			return m, nil
		case "up", "down":
			if !m.running {
				delta := -1
				if msg.String() == "down" {
					delta = 1
				}
				m.moveFocus(delta)
				return m, nil
			}
		case "enter":
			if !m.running {
				if m.focus != connectField {
					m.moveFocus(1)
					return m, nil
				}
				return m.activateFocused()
			}
		case " ":
			if !m.running && isOptionField(m.focus) {
				m.adjustOption(1)
				return m, nil
			}
		}
	}

	if m.screen == "profiles" {
		var cmd tea.Cmd
		m.profileList, cmd = m.profileList.Update(msg)
		return m, cmd
	}
	if m.screen == "save" {
		var cmd tea.Cmd
		m.profileName, cmd = m.profileName.Update(msg)
		return m, cmd
	}
	if m.screen != "" {
		return m, nil
	}
	if !m.running && m.focus == folderField {
		var cmd tea.Cmd
		m.folder, cmd = m.folder.Update(msg)
		return m, cmd
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
			m.tab = 0
			m.status = "Error: enter a Tailscale computer name or IP address"
			m.setFocus(targetField)
			return m, nil
		}
		username := strings.TrimSpace(m.inputs[usernameField].Value())
		if username == "" {
			m.tab = 0
			m.status = "Error: enter the Windows username"
			m.setFocus(usernameField)
			return m, nil
		}
		options := m.currentOptions()
		if err := validateRDPOptions(options); err != nil {
			m.status = "Error: " + err.Error()
			return m, nil
		}
		config := sessionConfig{
			target:     target,
			username:   username,
			domain:     strings.TrimSpace(m.inputs[domainField].Value()),
			password:   m.inputs[passwordField].Value(),
			clipboard:  m.clipboard,
			fullscreen: m.fullscreen,
			options:    &options,
		}
		for _, value := range []string{config.target, config.username, config.domain, config.password} {
			if strings.ContainsAny(value, "\r\n") {
				m.status = "Error: connection fields cannot contain line breaks"
				return m, nil
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		events := make(chan sessionEvent, 16)
		m.cancel = cancel
		m.events = events
		m.running = true
		m.exitWhenDone = false
		m.status = "Preparing Remote Desktop..."
		m.stage = "Starting"
		m.started = time.Now()
		m.addLog("Starting connection to " + target)
		for i := range m.inputs {
			m.inputs[i].Blur()
		}
		m.folder.Blur()
		var proxyCmd tea.Cmd
		var spinCmd tea.Cmd
		if !m.proxyStarting {
			spinCmd = m.spinner.Tick
		}
		select {
		case <-m.proxy.done:
			m.proxy = newPortableProxy()
			m.proxyStarting = true
			m.proxyStatus = "Starting Tailscale…"
			proxyCmd = tea.Batch(startPortableProxy(m.proxy), waitForPortableProxy(m.proxy))
		default:
		}
		return m, tea.Batch(startSession(ctx, config, events, m.proxy), spinCmd, sessionTick(), proxyCmd)
	}
	return m, nil
}

func (m *model) setFocus(index int) {
	m.focus = index
	if index == folderField {
		m.folder.Focus()
	} else {
		m.folder.Blur()
	}
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
		m.stage = "Stopping"
		m.status = "Disconnecting..."
		return m, nil
	}
	m.quitting = true
	return m, tea.Quit
}

// banner draws a small Remote Desktop icon (monitor with a blue screen and a
// green connection badge), an arrow, and the Tailscale logo beside the title.
// Every part is vertically centred on the monitor.
func banner() string {
	bezel := lipgloss.NewStyle().Foreground(accentColor)
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
	off := mutedStyle.Render("●")
	logo := strings.Join([]string{
		off + " " + off + " " + off,
		on + " " + on + " " + on,
		off + " " + on + " " + off,
	}, "\n")
	title := strings.Join([]string{
		titleStyle.Render("Portable Tailscale RDP"),
		sectionStyle.Render("RDP through Portable Tailscale"),
	}, "\n")
	return lipgloss.JoinHorizontal(lipgloss.Center, icon, "  ", arrow, "  ", logo, "    ", title)
}

func (m model) row(index int, label, value string) string {
	pointer := "  "
	style := lipgloss.NewStyle()
	if m.focus == index && !m.running {
		pointer = "> "
		style = focusedStyle
		value = focusedStyle.Render(value)
	}
	labels := labelStyle
	if m.focus == index && !m.running {
		labels = labels.Foreground(accentColor).Bold(true).Background(lipgloss.AdaptiveColor{Light: "153", Dark: "24"})
	}
	if index == connectField {
		if m.focus == index && !m.running {
			value = selectedButtonStyle.Render("[ Connect / Retry ]")
		} else {
			value = buttonStyle.Render("[ Connect / Retry ]")
		}
	}
	if m.width < 65 {
		labels = labels.Width(12)
		if index == domainField {
			label = "Domain"
		}
		if index == clipboardField {
			label = "Clipboard"
		}
	}
	return style.Render(pointer) + labels.Render(label) + value
}

func checkbox(checked bool) string {
	if checked {
		return successStyle.Bold(true).Render("[✓] ON")
	}
	return mutedStyle.Render("[ ] OFF")
}

func startSession(ctx context.Context, config sessionConfig, events chan sessionEvent, proxy *portableProxy) tea.Cmd {
	return func() tea.Msg {
		go runSession(ctx, config, events, proxy)
		return <-events
	}
}

func waitForSessionEvent(events <-chan sessionEvent) tea.Cmd {
	if events == nil {
		return nil
	}
	return func() tea.Msg { return <-events }
}

func runSession(ctx context.Context, config sessionConfig, events chan<- sessionEvent, proxy *portableProxy) {
	var finalErr error
	defer func() { events <- sessionEvent{err: finalErr, done: true} }()
	base, err := executableDirectory()
	if err != nil {
		finalErr = err
		return
	}
	freeRDPPath := filepath.Join(base, "freerdp", "sdl-freerdp.exe")
	if _, err = os.Stat(freeRDPPath); err != nil {
		finalErr = fmt.Errorf("required program not found: %s", freeRDPPath)
		return
	}
	events <- sessionEvent{stage: "Waiting for Tailscale", status: "Waiting for Tailscale readiness…"}
	connected, err := proxy.connection(ctx)
	if err != nil {
		finalErr = err
		return
	}
	events <- sessionEvent{stage: "Preparing bridge", status: "Tailscale ready; opening Remote Desktop…"}
	bridge, err := startSOCKSBridge(ctx, connected.ProxyURL, rdpAddress(config.target), func(err error) { events <- sessionEvent{log: "RDP bridge: " + err.Error()} })
	if err != nil {
		finalErr = err
		return
	}
	defer bridge.Close()
	rdpCtx, stopRDP := context.WithCancel(ctx)
	defer stopRDP()
	rdp := exec.CommandContext(rdpCtx, freeRDPPath, "/args-from:stdin")
	rdp.Dir = filepath.Dir(freeRDPPath)
	rdp.Stdin = strings.NewReader(strings.Join(buildRDPArgs(config, bridge.Address()), "\n") + "\n")
	logs := newDiagnosticWriter("FreeRDP", config.password, events)
	rdp.Stdout = logs
	rdp.Stderr = logs
	if err = rdp.Start(); err != nil {
		finalErr = fmt.Errorf("start FreeRDP: %w", err)
		return
	}
	events <- sessionEvent{stage: "FreeRDP running", status: "FreeRDP running · Tailscale " + connected.TailscaleIP}
	done := make(chan error, 1)
	go func() { done <- rdp.Wait() }()
	select {
	case err = <-done:
		if err != nil {
			finalErr = fmt.Errorf("FreeRDP exited: %w; open diagnostics with Ctrl+L", err)
		}
	case <-proxy.done:
		stopRDP()
		<-done
		finalErr = proxy.exitErr
	}
	logs.Flush()
	if failure := logs.Failure(); failure != nil && finalErr != nil {
		finalErr = failure
	}
	if ctx.Err() != nil {
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
		"/log-level:WARN",
		"/u:" + config.username,
		"/p:" + config.password,
	}
	if config.options != nil {
		// Replace the legacy certificate and dynamic-resolution defaults.
		args = append(args[:2], args[4:]...)
		args = append(args, optionArgs(*config.options)...)
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
	return net.JoinHostPort(strings.Trim(target, "[]"), "3389")
}

type socksBridge struct {
	listener net.Listener
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	report   func(error)
}

func startSOCKSBridge(parent context.Context, proxyURL, target string, report func(error)) (*socksBridge, error) {
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
	b := &socksBridge{listener: listener, cancel: cancel, report: report}
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
	remote, err := dialer.(xproxy.ContextDialer).DialContext(ctx, "tcp", target)
	if err != nil {
		if b.report != nil && ctx.Err() == nil {
			b.report(err)
		}
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
