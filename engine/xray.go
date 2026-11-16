package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type XrayExit struct {
	Executable  string `json:"executable"`
	ConfigFile  string `json:"configFile"`
	OutboundTag string `json:"outboundTag"`
}

// Import only the chosen outbound. The source config's listeners, routing, DNS,
// API and other outbounds are intentionally outside the managed process.
func xrayConfig(data []byte, tag string, local Hop, vpnPort int) ([]byte, error) {
	var source struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), &source); err != nil {
		return nil, errors.New("Xray import needs a valid JSON config file")
	}
	var selected map[string]any
	for _, outbound := range source.Outbounds {
		if tag != "" && outbound["tag"] != tag {
			continue
		}
		switch outbound["protocol"] {
		case "vless", "vmess", "trojan", "shadowsocks", "socks", "http":
			if selected != nil {
				return nil, errors.New("Xray config has multiple matching outbounds; enter the exact outbound tag")
			}
			selected = outbound
		}
	}
	if selected == nil {
		return nil, errors.New("Xray config has no matching VLESS, VMess, Trojan, Shadowsocks, SOCKS or HTTP outbound")
	}
	if selected["proxySettings"] != nil {
		return nil, errors.New("the selected Xray outbound already depends on another outbound")
	}
	stream, ok := selected["streamSettings"].(map[string]any)
	if !ok {
		if selected["streamSettings"] != nil {
			return nil, errors.New("invalid Xray stream settings")
		}
		stream = map[string]any{}
	}
	switch stream["network"] {
	case nil, "", "raw", "tcp", "ws", "websocket", "grpc", "httpupgrade", "xhttp", "splithttp":
	default:
		return nil, errors.New("the selected Xray outbound needs a supported TCP transport")
	}
	if stream["finalmask"] != nil {
		return nil, errors.New("the selected Xray outbound uses unsupported transport masking")
	}
	for _, key := range []string{"xhttpSettings", "splithttpSettings"} {
		if settings, ok := stream[key].(map[string]any); ok {
			extra, _ := settings["extra"].(map[string]any)
			if settings["downloadSettings"] != nil || extra["downloadSettings"] != nil {
				return nil, errors.New("separate Xray download transports cannot use the managed VPN detour")
			}
		}
	}
	stream["sockopt"] = map[string]any{"dialerProxy": "wayline-vpn", "domainStrategy": "AsIs"}
	return json.Marshal(map[string]any{
		"log": map[string]any{"loglevel": "none"},
		"inbounds": []any{map[string]any{
			"listen": local.Host, "port": local.Port, "protocol": "socks",
			"settings": map[string]any{"auth": "password", "accounts": []any{map[string]string{"user": local.Username, "pass": local.Password}}, "udp": false},
		}},
		"outbounds": []any{
			map[string]any{"tag": "wayline-exit", "protocol": selected["protocol"], "settings": selected["settings"], "streamSettings": stream},
			map[string]any{"tag": "wayline-vpn", "protocol": "socks", "settings": map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": vpnPort}}}},
		},
	})
}

func (x XrayExit) validate() error {
	if !filepath.IsAbs(x.Executable) || !filepath.IsAbs(x.ConfigFile) ||
		runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(x.Executable), ".exe") {
		return errors.New("choose the Xray executable and its local JSON config in Wayline")
	}
	if runtime.GOOS == "android" {
		engine, err := os.Executable()
		if err != nil || filepath.Clean(x.Executable) != filepath.Join(filepath.Dir(engine), "libxray.so") {
			return errors.New("Android requires the Xray core bundled with Wayline")
		}
	}
	return nil
}

func startXray(parent context.Context, x XrayExit, vpnPort int) (Hop, func(), <-chan error, error) {
	var hop Hop
	f, err := os.Open(x.ConfigFile)
	if err != nil {
		return hop, nil, nil, errors.New("cannot open the selected Xray JSON config")
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	f.Close()
	if err != nil || len(data) > 1<<20 {
		return hop, nil, nil, errors.New("cannot read the Xray JSON config or it exceeds 1 MiB")
	}
	defer clear(data)
	reserved, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return hop, nil, nil, err
	}
	defer reserved.Close()
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		return hop, nil, nil, err
	}
	hop = Hop{Type: "socks5", Host: "127.0.0.1", Port: reserved.Addr().(*net.TCPAddr).Port, Username: "wayline", Password: hex.EncodeToString(secret)}
	clear(secret)
	config, err := xrayConfig(data, x.OutboundTag, hop, vpnPort)
	if err != nil {
		return hop, nil, nil, err
	}
	defer clear(config)
	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, x.Executable, "run", "-config", "stdin:")
	cmd.Dir = filepath.Dir(x.Executable)
	cmd.Stdin = bytes.NewReader(config)
	cmd.WaitDelay = 2 * time.Second
	configureXrayProcess(cmd)
	reserved.Close()
	if err := cmd.Start(); err != nil {
		cancel()
		return hop, nil, nil, errors.New("could not start the selected Xray executable")
	}
	done := make(chan struct{})
	exited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		exited <- fmt.Errorf("managed Xray exited (%v); check the selected config and Xray version", err)
		close(done)
	}()
	stop := func() { cancel(); <-done }
	deadline, finish := context.WithTimeout(ctx, 8*time.Second)
	defer finish()
	for {
		conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(deadline, "tcp", hop.address())
		if err == nil {
			conn.SetDeadline(time.Now().Add(200 * time.Millisecond))
			err = socksAuthenticate(conn, hop)
			conn.Close()
			if err == nil {
				return hop, stop, exited, nil
			}
		}
		select {
		case err := <-exited:
			stop()
			return hop, nil, nil, err
		case <-deadline.Done():
			stop()
			return hop, nil, nil, errors.New("managed Xray did not start its authenticated local SOCKS listener")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
