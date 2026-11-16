package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

func tunConfig(c Config, dns, excluded []string, rawPort int) map[string]any {
	rules := []any{map[string]any{"port": 53, "action": "hijack-dns"}}
	if c.controlExecutable != "" {
		rules = append([]any{map[string]any{"process_path": []string{c.controlExecutable}, "action": "route", "outbound": "control-direct"}}, rules...)
	}

	interfaceName := "Wayline"
	pathPattern := "(?i)^"
	if runtime.GOOS == "linux" {
		interfaceName = "wayline0"
		pathPattern = "^"
	}
	if c.localExitExecutable != "" {
		rules = append(rules, map[string]any{
			"process_path_regex": []string{pathPattern + regexp.QuoteMeta(c.localExitExecutable) + "$"},
			"action":             "route", "outbound": "raw-vpn",
		})
	}
	if (c.AppFilterMode == "include" || c.AppFilterMode == "exclude") && len(c.AppFilterApps) > 0 {
		paths := make([]string, 0, len(c.AppFilterApps))
		for _, path := range c.AppFilterApps {
			paths = append(paths, pathPattern+regexp.QuoteMeta(path)+"$")
		}
		rule := map[string]any{"process_path_regex": paths, "action": "route", "outbound": "control-direct"}
		if c.AppFilterMode == "include" {
			rule["invert"] = true
		}
		rules = append(rules, rule)
	}
	rules = append(rules, map[string]any{"network": "udp", "port": 443, "action": "reject"})
	if len(c.After) > 0 {
		rules = append(rules, map[string]any{"network": "udp", "action": "reject"})
	}
	return map[string]any{
		"log":       map[string]any{"level": "info", "timestamp": false},
		"dns":       map[string]any{"servers": []any{map[string]any{"type": "tcp", "tag": "vpn-dns", "server": dns[0], "server_port": 53, "detour": "raw-vpn"}}, "final": "vpn-dns", "strategy": "ipv4_only"},
		"inbounds":  []any{map[string]any{"type": "tun", "tag": "tun-in", "interface_name": interfaceName, "address": []string{"172.31.255.1/30", "fdfe:dcba:9876::1/126"}, "mtu": 1300, "auto_route": true, "strict_route": true, "stack": "gvisor", "route_exclude_address": excluded}},
		"outbounds": []any{map[string]any{"type": "socks", "tag": "company", "server": "127.0.0.1", "server_port": c.ListenPort, "version": "5"}, map[string]any{"type": "socks", "tag": "raw-vpn", "server": "127.0.0.1", "server_port": rawPort, "version": "5"}, map[string]any{"type": "direct", "tag": "control-direct"}},
		"route":     map[string]any{"rules": rules, "final": "company", "auto_detect_interface": true},
	}
}

// sing-box owns the temporary adapter/routes. Normal cancellation interrupts it
// first, allowing its deferred network cleanup before any forced termination.
func startTUN(ctx context.Context, c Config, dns []string, rawPort int) (func(), <-chan error, error) {
	if len(dns) == 0 {
		return nil, nil, errors.New("VPN mode requires a gateway DNS server or an IPv4 DNS override")
	}
	executable, e := os.Executable()
	if e != nil {
		return nil, nil, e
	}
	binary := filepath.Join(filepath.Dir(executable), "sing-box.exe")
	c.controlExecutable = executable
	tempRoot := filepath.Dir(executable)
	setupHint := "run the app as Administrator and check for another TUN client"
	if runtime.GOOS == "linux" {
		binary = "/usr/lib/wayline/sing-box"
		tempRoot = ""
		setupHint = "check sudo access, /dev/net/tun and other active TUN clients"
	}
	if _, e = os.Stat(binary); e != nil {
		return nil, nil, fmt.Errorf("sing-box is missing at %s", binary)
	}
	host := c.Gateway
	if len(c.Before) > 0 && c.Before[0].Type != "xray" {
		host = c.Before[0].Host
	}
	addresses, e := net.DefaultResolver.LookupIPAddr(ctx, host)
	if e != nil {
		return nil, nil, errors.New("cannot resolve outer VPN endpoint before changing routes")
	}
	excluded := []string{"127.0.0.0/8", "::1/128"}
	for _, ip := range addresses {
		bits := "/128"
		if ip.IP.To4() != nil {
			bits = "/32"
		}
		excluded = append(excluded, ip.IP.String()+bits)
	}
	data, e := json.Marshal(tunConfig(c, dns, excluded, rawPort))
	if e != nil {
		return nil, nil, e
	}
	dir, e := os.MkdirTemp(tempRoot, "tun-session-")
	if e != nil {
		return nil, nil, e
	}
	configPath := filepath.Join(dir, "config.json")
	if e = os.WriteFile(configPath, data, 0600); e != nil {
		os.RemoveAll(dir)
		return nil, nil, e
	}
	cmd := exec.Command(binary, "run", "-c", configPath)
	startupTimeout := 15 * time.Second
	if runtime.GOOS == "linux" && os.Geteuid() != 0 {
		sudoMode := "-n"
		if os.Getenv("WAYLINE_DESKTOP_SESSION") == "1" {
			sudoMode = "-A"
			startupTimeout = 90 * time.Second
		}
		cmd = exec.Command("/usr/bin/sudo", sudoMode, "--", binary, "run", "-c", configPath)
		if sudoMode == "-A" {
			cmd.Env = append(os.Environ(), "SUDO_ASKPASS=/usr/lib/wayline/askpass")
		}
	}
	configureProcess(cmd)
	// Capture only bounded, fixed-format diagnostics; the config contains no secrets.
	logs := newStartupLog()
	cmd.Stdout = logs
	cmd.Stderr = logs
	if e = cmd.Start(); e != nil {
		os.RemoveAll(dir)
		return nil, nil, fmt.Errorf("start VPN mode (%s): %w", setupHint, e)
	}
	done := make(chan struct{})
	exited := make(chan error, 1)
	var exitErr error
	go func() {
		exitErr = cmd.Wait()
		exited <- fmt.Errorf("VPN adapter process stopped: %v", exitErr)
		close(done)
	}()
	stop := func() {
		stopProcess(cmd)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
		os.RemoveAll(dir)
	}
	select {
	case <-done:
		os.RemoveAll(dir)
		return nil, nil, fmt.Errorf("VPN adapter setup failed (%s): %v", setupHint, exitErr)
	case <-ctx.Done():
		stop()
		return nil, nil, ctx.Err()
	case <-logs.ready:
	case <-time.After(startupTimeout):
		stop()
		return nil, nil, errors.New("VPN adapter startup timed out")
	}
	return stop, exited, nil
}

type startupLog struct {
	mu    sync.Mutex
	text  string
	ready chan struct{}
	once  sync.Once
}

func newStartupLog() *startupLog { return &startupLog{ready: make(chan struct{})} }
func (l *startupLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.text += string(p)
	if strings.Contains(l.text, "sing-box started") {
		l.once.Do(func() { close(l.ready) })
	}
	if len(l.text) > 8192 {
		l.text = l.text[len(l.text)-8192:]
	}
	return len(p), nil
}
