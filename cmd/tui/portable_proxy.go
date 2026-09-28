//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
)

// The proxy belongs to the launcher, not an individual Remote Desktop session.
// Readiness and termination are broadcast so startup and Connect can share it.
type portableProxy struct {
	ctx               context.Context
	cancel            context.CancelFunc
	once              sync.Once
	ready, done       chan struct{}
	events            chan sessionEvent
	connected         proxyEvent
	readyErr, exitErr error
	command           func(context.Context) (*exec.Cmd, error)
}

func newPortableProxy() *portableProxy {
	ctx, cancel := context.WithCancel(context.Background())
	return &portableProxy{ctx: ctx, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), events: make(chan sessionEvent, 32)}
}
func (p *portableProxy) start() { p.once.Do(func() { go p.run() }) }
func (p *portableProxy) close() { p.cancel(); p.start(); <-p.done }
func (p *portableProxy) emit(e sessionEvent) {
	select {
	case p.events <- e:
	case <-p.ctx.Done():
	}
}
func (p *portableProxy) run() {
	ready := false
	defer func() {
		if !ready {
			p.readyErr = p.exitErr
			close(p.ready)
		}
		close(p.done)
	}()
	if p.ctx.Err() != nil {
		p.exitErr = p.ctx.Err()
		return
	}
	command := p.command
	if command == nil {
		command = portableProxyCommand
	}
	cmd, err := command(p.ctx)
	if err != nil {
		p.exitErr = err
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		p.exitErr = err
		return
	}
	logs := newDiagnosticWriter("Tailscale", "", p.events)
	cmd.Stderr = logs
	if err = cmd.Start(); err != nil {
		p.exitErr = fmt.Errorf("start PortableTailscale: %w", err)
		return
	}
	connected, readyErr := waitForProxy(p.ctx, stdout, func(status string) { p.emit(sessionEvent{status: status}) })
	if readyErr != nil {
		p.cancel()
		_ = cmd.Wait()
		logs.Flush()
		p.exitErr = readyErr
		return
	}
	p.connected = connected
	ready = true
	close(p.ready)
	p.emit(sessionEvent{stage: "Ready", status: "Tailscale ready · " + connected.TailscaleIP})
	_, _ = io.Copy(io.Discard, stdout)
	err = cmd.Wait()
	logs.Flush()
	if p.ctx.Err() != nil {
		p.exitErr = p.ctx.Err()
	} else {
		p.exitErr = fmt.Errorf("PortableTailscale stopped unexpectedly: %v", err)
	}
}

type proxyMessage struct {
	proxy *portableProxy
	event sessionEvent
}

func waitForPortableProxy(p *portableProxy) tea.Cmd {
	return func() tea.Msg {
		select {
		case e := <-p.events:
			return proxyMessage{p, e}
		case <-p.done:
			return proxyMessage{p, sessionEvent{done: true, err: p.exitErr}}
		}
	}
}

func portableProxyCommand(ctx context.Context) (*exec.Cmd, error) {
	base, err := executableDirectory()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, filepath.Join(base, "PortableTailscale.exe"), "--json")
	cmd.Dir = base
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd, nil
}
func startPortableProxy(p *portableProxy) tea.Cmd {
	return func() tea.Msg { p.start(); return nil }
}

func (p *portableProxy) connection(ctx context.Context) (proxyEvent, error) {
	if err := ctx.Err(); err != nil {
		return proxyEvent{}, err
	}
	p.start()
	select {
	case <-ctx.Done():
		return proxyEvent{}, ctx.Err()
	case <-p.ready:
	}
	if p.readyErr != nil {
		return proxyEvent{}, p.readyErr
	}
	select {
	case <-p.done:
		return proxyEvent{}, p.exitErr
	default:
		return p.connected, nil
	}
}
