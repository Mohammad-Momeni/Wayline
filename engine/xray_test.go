package main

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Exercise the actual Xray binary with an outbound address reachable only via
// the simulated VPN SOCKS listener. A direct dial to the fixture address fails.
func TestManagedXrayUsesVpnBridge(t *testing.T) {
	binary := os.Getenv("WAYLINE_XRAY")
	if binary == "" {
		t.Skip("set WAYLINE_XRAY to test the actual Xray child")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		conn, err := echo.Accept()
		if err == nil {
			defer conn.Close()
			io.Copy(conn, conn)
		}
	}()
	remote := &ProxyServer{dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "example.test:443" {
			return nil, errors.New("unexpected final target")
		}
		return (&net.Dialer{}).DialContext(ctx, network, echo.Addr().String())
	}}
	remoteListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer remoteListener.Close()
	go remote.Serve(ctx, remoteListener)
	bridge, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	seen := make(chan string, 1)
	raw := &ProxyServer{dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "vpn-only.test:12345" {
			return nil, errors.New("unexpected Xray upstream target")
		}
		select {
		case seen <- address:
		default:
		}
		return (&net.Dialer{}).DialContext(ctx, network, remoteListener.Addr().String())
	}}
	go raw.Serve(ctx, bridge)
	config := filepath.Join(t.TempDir(), "exit.json")
	data := []byte(`{"inbounds":[{"protocol":"ignore-me"}],"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"exit","protocol":"socks","settings":{"servers":[{"address":"vpn-only.test","port":12345}]}}],"routing":{"rules":[{"outboundTag":"direct"}]}}`)
	if err := os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	hop, stop, _, err := startXray(ctx, XrayExit{Executable: binary, ConfigFile: config, OutboundTag: "exit"}, bridge.Addr().(*net.TCPAddr).Port)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	conn, err := chain((&net.Dialer{}).DialContext, []Hop{hop})(ctx, "tcp", "example.test:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(conn, "through-vpn"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len("through-vpn"))
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "through-vpn" {
		t.Fatalf("unexpected echo %q: %v", buf, err)
	}
	select {
	case <-seen:
	default:
		t.Fatal("the Xray server dial did not go through the VPN bridge")
	}
}
