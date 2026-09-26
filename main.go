// PortableTailscale exposes an embedded Tailscale node as an authenticated
// SOCKS5 proxy on the Windows loopback interface.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"syscall"

	"tailscale.com/ipn/store"
	"tailscale.com/tsnet"
	"tailscale.com/types/logger"
)

var authURLPattern = regexp.MustCompile(`https://login\.tailscale\.com/a/[A-Za-z0-9_-]+`)

type options struct {
	stateDir  string
	hostname  string
	json      bool
	noBrowser bool
}

type event struct {
	Event       string `json:"event"`
	Message     string `json:"message,omitempty"`
	AuthURL     string `json:"authUrl,omitempty"`
	ProxyURL    string `json:"proxyUrl,omitempty"`
	ProxyAddr   string `json:"proxyAddress,omitempty"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	TailscaleIP string `json:"tailscaleIp,omitempty"`
	StateDir    string `json:"stateDirectory,omitempty"`
}

type reporter struct {
	json bool
	mu   sync.Mutex
}

func (r *reporter) emit(e event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.json {
		_ = json.NewEncoder(os.Stdout).Encode(e)
		return
	}

	switch e.Event {
	case "starting":
		fmt.Printf("Starting Tailscale (state: %s)\n", e.StateDir)
	case "auth":
		fmt.Printf("Authenticate this device:\n%s\n", e.AuthURL)
	case "connected":
		fmt.Printf("\nTailscale connected (%s)\n", e.TailscaleIP)
		fmt.Printf("SOCKS5 address: %s\n", e.ProxyAddr)
		fmt.Printf("Username:       %s\n", e.Username)
		fmt.Printf("Password:       %s\n", e.Password)
		fmt.Printf("Proxy URL:      %s\n", e.ProxyURL)
		fmt.Println("\nKeep this window open while using the proxy. Press Ctrl+C to stop.")
	case "stopping":
		fmt.Println("\nStopping proxy...")
	case "error":
		fmt.Fprintf(os.Stderr, "Error: %s\n", e.Message)
	}
}

func main() {
	os.Exit(run())
}

func run() int {
	opts, err := parseOptions()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	report := &reporter{json: opts.json}
	if err := os.MkdirAll(opts.stateDir, 0700); err != nil {
		report.emit(event{Event: "error", Message: fmt.Sprintf("create state directory: %v", err)})
		return 1
	}
	report.emit(event{Event: "starting", StateDir: opts.stateDir})
	stateStore, err := store.NewFileStore(logger.Discard, filepath.Join(opts.stateDir, "tailscaled.state"))
	if err != nil {
		report.emit(event{Event: "error", Message: fmt.Sprintf("open Tailscale identity: %v", err)})
		return 1
	}
	runtimeDir, err := os.MkdirTemp("", "portable-tailscale-")
	if err != nil {
		report.emit(event{Event: "error", Message: fmt.Sprintf("create temporary runtime directory: %v", err)})
		return 1
	}
	defer os.RemoveAll(runtimeDir)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var browserOnce sync.Once
	userLogf := func(format string, args ...any) {
		message := fmt.Sprintf(format, args...)
		authURL := authURLPattern.FindString(message)
		if authURL == "" {
			return
		}

		browserOnce.Do(func() {
			report.emit(event{Event: "auth", AuthURL: authURL})
			if !opts.noBrowser {
				if err := openBrowser(authURL); err != nil {
					report.emit(event{Event: "error", Message: fmt.Sprintf("open browser: %v", err)})
				}
			}
		})
	}

	srv := &tsnet.Server{
		Dir:      runtimeDir,
		Store:    stateStore,
		Hostname: opts.hostname,
		UserLogf: userLogf,
		Logf:     logger.Discard,
	}
	defer srv.Close()

	status, err := srv.Up(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 0
		}
		report.emit(event{Event: "error", Message: fmt.Sprintf("connect to Tailscale: %v", err)})
		return 1
	}

	addr, proxyPassword, _, err := srv.Loopback()
	if err != nil {
		report.emit(event{Event: "error", Message: fmt.Sprintf("start SOCKS5 proxy: %v", err)})
		return 1
	}

	proxyURL := (&url.URL{
		Scheme: "socks5",
		User:   url.UserPassword("tsnet", proxyPassword),
		Host:   addr,
	}).String()

	tailscaleIP := "unknown"
	if status != nil && len(status.TailscaleIPs) > 0 {
		tailscaleIP = status.TailscaleIPs[0].String()
	} else if ip4, ip6 := srv.TailscaleIPs(); ip4.IsValid() {
		tailscaleIP = ip4.String()
	} else if ip6.IsValid() {
		tailscaleIP = ip6.String()
	}

	report.emit(event{
		Event:       "connected",
		ProxyURL:    proxyURL,
		ProxyAddr:   addr,
		Username:    "tsnet",
		Password:    proxyPassword,
		TailscaleIP: tailscaleIP,
		StateDir:    opts.stateDir,
	})

	<-ctx.Done()
	report.emit(event{Event: "stopping"})
	return 0
}

func parseOptions() (options, error) {
	defaultStateDir, err := portableStateDir()
	if err != nil {
		return options{}, err
	}

	var opts options
	flag.StringVar(&opts.stateDir, "state-dir", defaultStateDir, "directory for the persistent Tailscale identity")
	flag.StringVar(&opts.hostname, "hostname", "portable-tailscale", "device name shown in the Tailscale admin console")
	flag.BoolVar(&opts.json, "json", false, "write newline-delimited JSON events")
	flag.BoolVar(&opts.noBrowser, "no-browser", false, "print the authentication URL without opening it")
	flag.Parse()

	absStateDir, err := filepath.Abs(opts.stateDir)
	if err != nil {
		return options{}, fmt.Errorf("resolve state directory: %w", err)
	}
	opts.stateDir = absStateDir
	return opts, nil
}

func portableStateDir() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find executable directory: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	return filepath.Join(filepath.Dir(executable), "data"), nil
}

func openBrowser(address string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("automatic browser launch is only supported on Windows")
	}
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", address)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}
