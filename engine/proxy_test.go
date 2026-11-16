package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type runningTestProxy struct {
	listener net.Listener
	cancel   context.CancelFunc
	done     chan struct{}
	err      error
}

func startTestProxy(t *testing.T, dial Dial) *runningTestProxy {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &runningTestProxy{listener: l, cancel: cancel, done: make(chan struct{})}
	go func() {
		p.err = (&ProxyServer{dial: dial}).Serve(ctx, l)
		close(p.done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
			t.Error("proxy did not release listener and handlers on cancellation")
		}
	})
	return p
}

func (p *runningTestProxy) connect(t *testing.T) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", p.listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(2 * time.Second))
	t.Cleanup(func() { c.Close() })
	return c
}

func TestProxySOCKSUsesOnlyInjectedTunnelDial(t *testing.T) {
	const target = "private-service.invalid:9443"
	const payload = "through tunnel only"
	var calls atomic.Int32
	remoteDone := make(chan error, 1)
	p := startTestProxy(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		calls.Add(1)
		if network != "tcp" || address != target {
			return nil, fmt.Errorf("unexpected tunnel destination: %s %s", network, address)
		}
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			b.SetDeadline(time.Now().Add(2 * time.Second))
			data := make([]byte, len(payload))
			_, err := io.ReadFull(b, data)
			if err == nil {
				_, err = io.WriteString(b, strings.ToUpper(string(data)))
			}
			remoteDone <- err
		}()
		return a, nil
	})
	c := p.connect(t)
	if err := socksConnect(c, Hop{}, target); err != nil {
		t.Fatal(err)
	}
	testExchange(t, c, payload)
	c.Close()
	if calls.Load() != 1 {
		t.Fatalf("tunnel dial calls=%d, want1", calls.Load())
	}
	if err := <-remoteDone; err != nil {
		t.Fatal(err)
	}
}

func TestProxyHTTPConnectPreservesClientEarlyBytes(t *testing.T) {
	const payload = "early hello"
	var calls atomic.Int32
	p := startTestProxy(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		calls.Add(1)
		if address != "private.invalid:443" {
			return nil, fmt.Errorf("unexpected tunnel target %s", address)
		}
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			b.SetDeadline(time.Now().Add(time.Second))
			data := make([]byte, len(payload))
			if _, err := io.ReadFull(b, data); err == nil {
				io.WriteString(b, strings.ToUpper(string(data)))
			}
		}()
		return a, nil
	})
	c := p.connect(t)
	io.WriteString(c, "CONNECT private.invalid:443 HTTP/1.1\r\nHost: private.invalid:443\r\n\r\n"+payload)
	br := bufio.NewReader(c)
	response, err := http.ReadResponse(br, &http.Request{Method: "CONNECT"})
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("CONNECT response=%v err=%v", response, err)
	}
	data := make([]byte, len(payload))
	if _, err := io.ReadFull(br, data); err != nil || string(data) != strings.ToUpper(payload) {
		t.Fatalf("early tunneled payload=%q err=%v", data, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("tunnel dial calls=%d, want1", calls.Load())
	}
}

func TestProxyHTTPForwardUsesTunnelAndPreservesPOSTBody(t *testing.T) {
	const body = "a=hello&b=world"
	observed := make(chan error, 1)
	var calls atomic.Int32
	p := startTestProxy(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		calls.Add(1)
		if address != "private.invalid:80" {
			return nil, fmt.Errorf("unexpected tunnel target %s", address)
		}
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			b.SetDeadline(time.Now().Add(time.Second))
			req, err := http.ReadRequest(bufio.NewReader(b))
			if err == nil {
				data, readErr := io.ReadAll(req.Body)
				req.Body.Close()
				if readErr != nil || string(data) != body || req.URL.String() != "/path?q=1" || req.Header.Get("Proxy-Authorization") != "" {
					err = fmt.Errorf("forwarded request lost body/path or leaked proxy credentials: %q %s auth=%q err=%v", data, req.URL, req.Header.Get("Proxy-Authorization"), readErr)
				}
			}
			if err == nil {
				_, err = fmt.Fprintf(b, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
			}
			observed <- err
		}()
		return a, nil
	})
	c := p.connect(t)
	fmt.Fprintf(c, "POST http://private.invalid/path?q=1 HTTP/1.1\r\nHost: private.invalid\r\nProxy-Authorization: Basic do-not-forward\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	response, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: "POST"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(data) != body {
		t.Fatalf("forward response status=%d body=%q err=%v", response.StatusCode, data, err)
	}
	if err := <-observed; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("tunnel dial calls=%d, want1", calls.Load())
	}
}

func TestProxyDialFailureReturnsErrorWithoutFallback(t *testing.T) {
	var calls atomic.Int32
	p := startTestProxy(t, func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, errors.New("tunnel unavailable")
	})
	c := p.connect(t)
	io.WriteString(c, "CONNECT must-not-resolve.invalid:443 HTTP/1.1\r\nHost: must-not-resolve.invalid:443\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: "CONNECT"})
	if err != nil || response.StatusCode != 502 {
		t.Fatalf("failure response=%v err=%v", response, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("failed tunnel caused %d dials", calls.Load())
	}
}

func TestProxyCancellationClosesIdleClientAndListener(t *testing.T) {
	p := startTestProxy(t, func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("idle client must not dial")
	})
	c := p.connect(t)
	p.cancel()
	select {
	case <-p.done:
		if !errors.Is(p.err, context.Canceled) {
			t.Fatalf("shutdown returned %v", p.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not finish on cancellation")
	}
	var b [1]byte
	if _, err := c.Read(b[:]); err == nil {
		t.Fatal("idle connection remained open")
	}
	other, err := net.DialTimeout("tcp", p.listener.Addr().String(), 200*time.Millisecond)
	if err == nil {
		other.Close()
		t.Fatal("proxy listener remained open")
	}
}

func TestProxyCancellationClosesActiveTunnel(t *testing.T) {
	remoteDone := make(chan error, 1)
	p := startTestProxy(t, func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			var one [1]byte
			_, err := b.Read(one[:])
			remoteDone <- err
		}()
		return a, nil
	})
	c := p.connect(t)
	if err := socksConnect(c, Hop{}, "active.invalid:443"); err != nil {
		t.Fatal(err)
	}
	p.cancel()
	select {
	case <-p.done:
	case <-time.After(time.Second):
		t.Fatal("Serve did not join an active tunnel on cancellation")
	}
	select {
	case err := <-remoteDone:
		if err == nil {
			t.Fatal("active remote tunnel remained open")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not close remote tunnel")
	}
}
