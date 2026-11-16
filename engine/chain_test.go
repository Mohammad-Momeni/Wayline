package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Loopback fixtures model protocol peers rather than inspecting implementation.
// No test resolves or connects to a VPN/public endpoint.
type testPeer struct {
	listener net.Listener
	done     chan error
	mu       sync.Mutex
	conn     net.Conn
}

func startTestPeer(t *testing.T, handler func(net.Conn) error) *testPeer {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &testPeer{listener: l, done: make(chan error, 1)}
	go func() {
		c, err := l.Accept()
		if err == nil {
			p.mu.Lock()
			p.conn = c
			p.mu.Unlock()
			c.SetDeadline(time.Now().Add(4 * time.Second))
			err = handler(c)
			c.Close()
		}
		p.done <- err
	}()
	t.Cleanup(func() {
		l.Close()
		p.mu.Lock()
		if p.conn != nil {
			p.conn.Close()
		}
		p.mu.Unlock()
	})
	return p
}

func (p *testPeer) address() string { return p.listener.Addr().String() }
func (p *testPeer) hop(kind string) Hop {
	host, port, _ := net.SplitHostPort(p.address())
	n, _ := strconv.Atoi(port)
	return Hop{Type: kind, Host: host, Port: n}
}
func (p *testPeer) wait(t *testing.T) {
	t.Helper()
	select {
	case err := <-p.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loopback peer did not finish")
	}
}

func testExchange(t *testing.T, c net.Conn, payload string) {
	t.Helper()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(c, payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != strings.ToUpper(payload) {
		t.Fatalf("response %q, want %q", got, strings.ToUpper(payload))
	}
}

func testBridge(a, b net.Conn) {
	done := make(chan struct{})
	go func() { io.Copy(b, a); b.Close(); close(done) }()
	io.Copy(a, b)
	a.Close()
	<-done
}

func socksTestHandshake(c net.Conn, username, password string) (string, error) {
	var greeting [2]byte
	if _, err := io.ReadFull(c, greeting[:]); err != nil {
		return "", err
	}
	methods := make([]byte, greeting[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return "", err
	}
	method := byte(0)
	if username != "" {
		method = 2
	}
	if greeting[0] != 5 || len(methods) != 1 || methods[0] != method {
		return "", fmt.Errorf("unexpected SOCKS auth offer: %v %v", greeting, methods)
	}
	c.Write([]byte{5, method})
	if method == 2 {
		var auth [2]byte
		if _, err := io.ReadFull(c, auth[:]); err != nil {
			return "", err
		}
		user := make([]byte, auth[1])
		if _, err := io.ReadFull(c, user); err != nil {
			return "", err
		}
		var n [1]byte
		if _, err := io.ReadFull(c, n[:]); err != nil {
			return "", err
		}
		pass := make([]byte, n[0])
		if _, err := io.ReadFull(c, pass); err != nil {
			return "", err
		}
		if auth[0] != 1 || string(user) != username || string(pass) != password {
			c.Write([]byte{1, 1})
			return "", errors.New("test peer rejected credentials")
		}
		c.Write([]byte{1, 0})
	}
	var request [4]byte
	if _, err := io.ReadFull(c, request[:]); err != nil {
		return "", err
	}
	if request[0] != 5 || request[1] != 1 || request[2] != 0 {
		return "", errors.New("invalid SOCKS CONNECT")
	}
	address, err := readSocksAddress(c, request[3])
	return address, err
}

func TestChainSOCKSAndHTTPInTrafficOrder(t *testing.T) {
	payload := "ordered tunnel payload"
	destination := startTestPeer(t, func(c net.Conn) error {
		data := make([]byte, len(payload))
		if _, err := io.ReadFull(c, data); err != nil {
			return err
		}
		_, err := io.WriteString(c, strings.ToUpper(string(data)))
		return err
	})
	second := startTestPeer(t, func(c net.Conn) error {
		r, err := http.ReadRequest(bufio.NewReader(c))
		if err != nil {
			return err
		}
		if r.Method != "CONNECT" || r.Host != destination.address() {
			return fmt.Errorf("second hop target %s %s", r.Method, r.Host)
		}
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("http-user:http-pass"))
		if r.Header.Get("Proxy-Authorization") != wantAuth {
			return errors.New("HTTP hop authentication missing")
		}
		remote, err := net.Dial("tcp", r.Host)
		if err != nil {
			return err
		}
		defer remote.Close()
		fmt.Fprint(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
		testBridge(c, remote)
		return nil
	})
	first := startTestPeer(t, func(c net.Conn) error {
		address, err := socksTestHandshake(c, "socks-user", "socks-pass")
		if err != nil {
			return err
		}
		if address != second.address() {
			return fmt.Errorf("first hop target %s, want second proxy", address)
		}
		remote, err := net.Dial("tcp", address)
		if err != nil {
			return err
		}
		defer remote.Close()
		c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		testBridge(c, remote)
		return nil
	})
	firstHop, secondHop := first.hop("socks5"), second.hop("http")
	firstHop.Username, firstHop.Password = "socks-user", "socks-pass"
	secondHop.Username, secondHop.Password = "http-user", "http-pass"
	var calls atomic.Int32
	base := func(ctx context.Context, network, address string) (net.Conn, error) {
		calls.Add(1)
		if address != first.address() {
			return nil, fmt.Errorf("base dial bypassed first hop: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	c, err := chain(base, []Hop{firstHop, secondHop})(context.Background(), "tcp", destination.address())
	if err != nil {
		t.Fatal(err)
	}
	testExchange(t, c, payload)
	c.Close()
	if calls.Load() != 1 {
		t.Fatalf("base dial calls=%d, want 1", calls.Load())
	}
	destination.wait(t)
	second.wait(t)
	first.wait(t)
}

func TestChainAuthenticationFailureNeverDialsTarget(t *testing.T) {
	for _, kind := range []string{"socks5", "http"} {
		t.Run(kind, func(t *testing.T) {
			peer := startTestPeer(t, func(c net.Conn) error {
				if kind == "http" {
					if _, err := http.ReadRequest(bufio.NewReader(c)); err != nil {
						return err
					}
					fmt.Fprint(c, "HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n")
					return nil
				}
				_, err := socksTestHandshake(c, "user", "right-password")
				if err == nil {
					return errors.New("wrong password unexpectedly accepted")
				}
				return nil
			})
			hop := peer.hop(kind)
			hop.Username, hop.Password = "user", "wrong-password"
			var calls atomic.Int32
			base := func(ctx context.Context, network, address string) (net.Conn, error) {
				calls.Add(1)
				if address != peer.address() {
					return nil, errors.New("direct fallback was attempted")
				}
				return (&net.Dialer{}).DialContext(ctx, network, address)
			}
			c, err := chain(base, []Hop{hop})(context.Background(), "tcp", "must-not-resolve.invalid:443")
			if err == nil || c != nil {
				t.Fatalf("authentication rejection returned conn=%v err=%v", c, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("base dial calls=%d, want exactly proxy dial", calls.Load())
			}
			peer.wait(t)
		})
	}
}

func TestHTTPConnectPreservesEarlyTunnelBytes(t *testing.T) {
	peer := startTestPeer(t, func(c net.Conn) error {
		if _, err := http.ReadRequest(bufio.NewReader(c)); err != nil {
			return err
		}
		_, err := io.WriteString(c, "HTTP/1.1 200 OK\r\n\r\nearly-payload")
		return err
	})
	c, err := chain((&net.Dialer{}).DialContext, []Hop{peer.hop("http")})(context.Background(), "tcp", "target.invalid:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(time.Second))
	data := make([]byte, len("early-payload"))
	if _, err := io.ReadFull(c, data); err != nil || string(data) != "early-payload" {
		t.Fatalf("early payload %q, err=%v", data, err)
	}
	peer.wait(t)
}

func TestChainCancellationInterruptsHandshake(t *testing.T) {
	accepted := make(chan struct{})
	peer := startTestPeer(t, func(c net.Conn) error {
		close(accepted)
		_, err := io.Copy(io.Discard, c)
		return err
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		c, err := chain((&net.Dialer{}).DialContext, []Hop{peer.hop("socks5")})(ctx, "tcp", "target.invalid:443")
		if c != nil {
			c.Close()
		}
		done <- err
	}()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("proxy was not dialed")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled handshake returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt proxy handshake")
	}
	peer.wait(t)
}
