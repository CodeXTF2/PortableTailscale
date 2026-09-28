//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestPortableProxyHelper(t *testing.T) {
	if os.Getenv("PORTABLE_PROXY_TEST_HELPER") != "1" {
		return
	}
	fmt.Println(`{"event":"connected","proxyUrl":"socks5://test:secret@127.0.0.1:12345","tailscaleIp":"100.64.0.1"}`)
	for {
		time.Sleep(time.Second)
	}
}

func TestWarmProxyIsReusedAndClosed(t *testing.T) {
	p := newPortableProxy()
	launches := 0
	p.command = func(ctx context.Context) (*exec.Cmd, error) {
		launches++
		binary, err := os.Executable()
		if err != nil {
			return nil, err
		}
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestPortableProxyHelper$")
		cmd.Env = append(os.Environ(), "PORTABLE_PROXY_TEST_HELPER=1")
		return cmd, nil
	}
	defer p.close()
	p.start() // launcher startup, before any RDP connection is requested
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := p.connection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessionCtx, disconnect := context.WithCancel(context.Background())
	disconnect()
	_, _ = p.connection(sessionCtx)
	second, err := p.connection(ctx)
	if err != nil || first != second || launches != 1 || p.ctx.Err() != nil {
		t.Fatal("RDP cancellation restarted or stopped the shared proxy")
	}
	p.close()
	select {
	case <-p.done:
	default:
		t.Fatal("proxy not reaped on shutdown")
	}
}

func TestWarmupFailureIsReported(t *testing.T) {
	p := newPortableProxy()
	p.command = func(context.Context) (*exec.Cmd, error) { return nil, errors.New("missing proxy executable") }
	defer p.close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := p.connection(ctx)
	if err == nil || !strings.Contains(err.Error(), "missing proxy") {
		t.Fatal("startup error not propagated")
	}
	msg := waitForPortableProxy(p)().(proxyMessage)
	if !msg.event.done || msg.event.err == nil {
		t.Fatal("UI did not receive startup failure")
	}
}

func TestWarmupLeavesFormEditableAndRetriesFailedProxy(t *testing.T) {
	m := isolatedModel(t)
	defer m.proxy.close()
	m.proxy.command = func(context.Context) (*exec.Cmd, error) { return nil, errors.New("offline") }
	if m.Init() == nil || !m.proxyStarting || m.running {
		t.Fatal("startup must warm proxy without locking form")
	}
	m = press(m, tea.KeyDown)
	if m.focus != usernameField {
		t.Fatal("warmup locked form")
	}
	m.proxy.start()
	<-m.proxy.done
	old := m.proxy
	m.inputs[targetField].SetValue("host")
	m.inputs[usernameField].SetValue("user")
	m.setFocus(connectField)
	updated, cmd := m.activateFocused()
	next := updated.(model)
	if next.proxy == old || cmd == nil || !next.running {
		t.Fatal("Connect did not prepare a fresh proxy after failure")
	}
	next.cancel()
	next.proxy.cancel() // Commands intentionally not executed: no real network.
}
