// Package e2e exercises the installed Windows service only on explicitly enabled test hosts.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"wayline/netstack"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"wayline/native"
	"wayline/ppp"
)

// simulatorPacket encodes bounded PPP control packets for the fake gateway peer.
func simulatorPacket(protocol uint16, code, id byte, data []byte) []byte {
	packet := make([]byte, 6+len(data))
	binary.BigEndian.PutUint16(packet, protocol)
	packet[2], packet[3] = code, id
	binary.BigEndian.PutUint16(packet[4:6], uint16(4+len(data)))
	copy(packet[6:], data)
	return packet
}

// simulatePPP adapts the existing helper assembly peer to documentation-only IPv4 ranges.
// Every option is bounds-checked, reads have deadlines, and shutdown acknowledges LCP termination.
func simulatePPP(conn net.Conn, reader io.Reader) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ns, err := netstack.New("198.51.100.1", 1354, "")
	if err != nil {
		return
	}
	defer ns.Close()
	listener, err := gonet.ListenTCP(ns.Stack, tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{198, 51, 100, 1}), Port: 8080}, ipv4.ProtocolNumber)
	if err != nil {
		return
	}
	defer listener.Close()
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	var writeMu sync.Mutex
	write := func(packet []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return native.WriteFrame(conn, packet)
	}
	go func() {
		for {
			pb := ns.Link.ReadContext(ctx)
			if pb == nil {
				return
			}
			b := pb.ToBuffer()
			data := append([]byte(nil), b.Flatten()...)
			b.Release()
			pb.DecRef()
			if write(append([]byte{0, 0x21}, data...)) != nil {
				return
			}
		}
	}()

	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Minute))
	lcpSent, ipcpSent := false, false
	for {
		packet, err := native.ReadFrame(reader)
		if err != nil || len(packet) < 2 {
			return
		}
		protocol := binary.BigEndian.Uint16(packet)
		if protocol == ppp.ProtocolIPv4 {
			pb := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(packet[2:])})
			ns.Link.InjectInbound(ipv4.ProtocolNumber, pb)
			pb.DecRef()
			continue
		}
		control, err := ppp.DecodeControl(packet[2:])
		if err != nil {
			return
		}
		switch control.Code {
		case ppp.ConfigureRequest:
			code := byte(ppp.ConfigureAck)
			data := append([]byte(nil), control.Data...)
			if protocol == ppp.ProtocolIPCP {
				for i := 0; i < len(data); {
					if i+2 > len(data) || data[i+1] < 2 || i+int(data[i+1]) > len(data) {
						return
					}
					if data[i+1] == 6 {
						switch data[i] {
						case 3:
							if binary.BigEndian.Uint32(data[i+2:i+6]) == 0 {
								copy(data[i+2:i+6], []byte{198, 51, 100, 10})
							}
						case 129, 131:
							copy(data[i+2:i+6], []byte{198, 51, 100, 53})
						}
					}
					i += int(data[i+1])
				}
				if !reflect.DeepEqual(data, control.Data) {
					code = ppp.ConfigureNak
				}
			}
			if write(simulatorPacket(protocol, code, control.ID, data)) != nil {
				return
			}
			if protocol == ppp.ProtocolLCP && !lcpSent {
				lcpSent = true
				if write(simulatorPacket(protocol, ppp.ConfigureRequest, 71, []byte{1, 4, 5, 74, 5, 6, 0, 0, 0, 42})) != nil {
					return
				}
			}
			if protocol == ppp.ProtocolIPCP && !ipcpSent {
				ipcpSent = true
				if write(simulatorPacket(protocol, ppp.ConfigureRequest, 72, []byte{3, 6, 198, 51, 100, 1})) != nil {
					return
				}
			}
		case ppp.EchoRequest:
			if protocol != ppp.ProtocolLCP || len(control.Data) < 4 {
				return
			}
			data := append([]byte(nil), control.Data...)
			binary.BigEndian.PutUint32(data[:4], 42)
			if write(simulatorPacket(protocol, ppp.EchoReply, control.ID, data)) != nil {
				return
			}
		case ppp.TerminateRequest:
			_ = write(simulatorPacket(protocol, ppp.TerminateAck, control.ID, control.Data))
			return
		}
	}
}

// fakeGateway serves only loopback TLS and closes hijacked connections during test cleanup.
// Login, cookie validation, XML policy and PPP follow the existing helper assembly fixture.
func fakeGateway(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	var mu sync.Mutex
	connections := make(map[net.Conn]bool)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/remote/logincheck" {
			if r.ParseForm() != nil || r.Form.Get("username") != "fixture-user" || r.Form.Get("credential") != "fixture-password" {
				_, _ = io.WriteString(w, "ret=0")
				return
			}
			w.Header().Set("Set-Cookie", "SVPNCOOKIE=test-token")
			_, _ = io.WriteString(w, "ret=1")
			return
		}
		if r.Header.Get("Cookie") != "SVPNCOOKIE=test-token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/remote/fortisslvpn_xml":
			_, _ = io.WriteString(w, `<sslvpn><assigned-addr ipv4="198.51.100.10"/><dns ip="198.51.100.53" domain="example.test"/><split-tunnel-info><addr ip="203.0.113.0" mask="255.255.255.0"/></split-tunnel-info></sslvpn>`)
		case "/remote/sslvpn-tunnel":
			conn, buffer, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			simulatePPP(conn, buffer)
			mu.Lock()
			delete(connections, conn)
			mu.Unlock()
		default:
			_, _ = io.WriteString(w, "ok")
		}
	}))
	server.Config.ReadHeaderTimeout = 5 * time.Second
	server.Config.ReadTimeout = 15 * time.Second
	server.Config.WriteTimeout = 15 * time.Second
	server.StartTLS()
	t.Cleanup(func() {
		mu.Lock()
		for conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		server.Close()
	})
	digest := sha256.Sum256(server.Certificate().Raw)
	return server, hex.EncodeToString(digest[:])
}

func TestEndToEndProxy(t *testing.T) {
	server, pin := fakeGateway(t)
	addr := server.Listener.Addr().(*net.TCPAddr)
	probe, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, Config{Gateway: "127.0.0.1", Port: addr.Port, Username: "fixture-user", Password: "fixture-password", CertificateSHA256: pin, Mode: "proxy", ListenPort: port})
	}()
	var c net.Conn
	for i := 0; i < 100; i++ {
		c, e = net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 50*time.Millisecond)
		if e == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatal(err)
		case <-time.After(25 * time.Millisecond):
		}
	}
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if e = socksConnect(c, Hop{}, "198.51.100.1:8080"); e != nil {
		t.Fatal(e)
	}
	payload := bytes.Repeat([]byte("wayline-through-ppp-"), 300)
	if _, e = c.Write(payload); e != nil {
		t.Fatal(e)
	}
	got := make([]byte, len(payload))
	if _, e = io.ReadFull(c, got); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("corrupted tunnel data")
	}
	c.Close()
	cancel()
	select {
	case e := <-done:
		if e != nil && e != context.Canceled {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("engine did not stop")
	}
	fmt.Println("TLS + Fortinet login + PPP + SOCKS + VPN TCP echo verified")
}
