package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManagedXrayHopsPreservePrefixOrder(t *testing.T) {
	binary := os.Getenv("WAYLINE_XRAY")
	if binary == "" {
		t.Skip("set WAYLINE_XRAY to test Xray hops")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	echo := startTestPeer(t, func(conn net.Conn) error { _, err := io.Copy(conn, conn); return err })
	server2, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server2.Close()
	go (&ProxyServer{dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "destination.test:443" {
			return nil, errors.New("wrong final destination")
		}
		return (&net.Dialer{}).DialContext(ctx, network, echo.address())
	}}).Serve(ctx, server2)
	server1, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server1.Close()
	go (&ProxyServer{dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "exit-two.test:2002" {
			return nil, errors.New("second Xray bypassed the first")
		}
		return (&net.Dialer{}).DialContext(ctx, network, server2.Addr().String())
	}}).Serve(ctx, server1)
	base := func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "exit-one.test:2001" {
			return nil, errors.New("Xray bypassed its VPN prefix")
		}
		return (&net.Dialer{}).DialContext(ctx, network, server1.Addr().String())
	}
	var hops []Hop
	for i, host := range []string{"exit-one.test", "exit-two.test"} {
		path := filepath.Join(t.TempDir(), "xray.json")
		data, _ := json.Marshal(map[string]any{"outbounds": []any{map[string]any{
			"protocol": "socks", "settings": map[string]any{"servers": []any{map[string]any{"address": host, "port": 2001 + i}}},
		}}})
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		hops = append(hops, Hop{Type: "xray", Xray: &XrayExit{Executable: binary, ConfigFile: path}})
	}
	dial, stop, _, err := prepareChain(ctx, base, (&net.Dialer{}).DialContext, hops)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	conn, err := dial(ctx, "tcp", "destination.test:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	payload := []byte("two ordered managed Xray hops")
	conn.Write(payload)
	answer := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, answer); err != nil || string(answer) != string(payload) {
		t.Fatalf("chained Xray payload failed: %v", err)
	}
}
