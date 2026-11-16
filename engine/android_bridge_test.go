package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	hostnet "wayline/netstack"
)

// syntheticAndroid exchanges raw IPv4 datagrams exactly like Android's Java
// VpnService bridge, but owns no OS adapter and makes no routing changes.
type syntheticAndroid struct {
	host       *hostnet.NetStack
	bridgeUDP  *net.UDPConn
	hostUDP    *net.UDPConn
	cancel     context.CancelFunc
	stopBridge func()
	hostDone   chan error
}

func newSyntheticAndroid(t *testing.T, final Dial) *syntheticAndroid {
	return newSyntheticAndroidVPN(t, final, nil, "10.77.0.1", false)
}

func newSyntheticAndroidVPN(t *testing.T, final Dial, vpn *vpnDialer, dns string, allowUDP bool, localExit ...bool) *syntheticAndroid {
	t.Helper()
	bridgeUDP, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	hostUDP, err := net.DialUDP("udp4", nil, bridgeUDP.LocalAddr().(*net.UDPAddr))
	if err != nil {
		bridgeUDP.Close()
		t.Fatal(err)
	}
	host, err := hostnet.New("10.77.0.2", 1300, "")
	if err != nil {
		hostUDP.Close()
		bridgeUDP.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop, _, err := startPacketBridge(ctx, bridgeUDP, hostUDP.LocalAddr().(*net.UDPAddr), final, vpn, dns, allowUDP)
	var transport net.Conn = hostUDP
	if len(localExit) > 0 && localExit[0] {
		stop()
		bridgeUDP, err = net.ListenUDP("udp4", bridgeUDP.LocalAddr().(*net.UDPAddr))
		if err != nil {
			t.Fatal(err)
		}
		stop, _, err = startAndroidExitBridge(ctx, bridgeUDP, hostUDP.LocalAddr().(*net.UDPAddr), final, vpn, dns)
		transport = proxyAppPackets{hostUDP}
	}
	if err != nil {
		cancel()
		host.Close()
		hostUDP.Close()
		bridgeUDP.Close()
		t.Fatal(err)
	}
	s := &syntheticAndroid{host: host, bridgeUDP: bridgeUDP, hostUDP: hostUDP, cancel: cancel, stopBridge: stop, hostDone: make(chan error, 1)}
	go func() { s.hostDone <- host.Run(ctx, transport) }()
	t.Cleanup(func() {
		cancel()
		done := make(chan struct{})
		go func() { stop(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Android packet bridge did not stop")
		}
		hostUDP.Close()
		host.Close()
	})
	return s
}

type proxyAppPackets struct{ net.Conn }

func (c proxyAppPackets) Write(packet []byte) (int, error) {
	if len(packet) >= 24 && packet[0]>>4 == 4 {
		offset := int(packet[0]&15) * 4
		if len(packet) >= offset+4 && binary.BigEndian.Uint16(packet[offset+2:]) == 8081 {
			_, err := c.Conn.Write(append([]byte{0}, packet...))
			if err != nil {
				return 0, err
			}
			return len(packet), nil
		}
	}
	return c.Conn.Write(packet)
}

func TestAndroidLocalExitAppGetsRawVPNWhileAppsGetAfterChain(t *testing.T) {
	vpn, network := testVPNWire(t)
	local := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 53}), Port: 8081}
	raw, err := gonet.ListenTCP(network.Stack, local, ipv4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	go func() {
		conn, err := raw.Accept()
		if err == nil {
			defer conn.Close()
			io.Copy(conn, conn)
		}
	}()
	echo := startTestPeer(t, func(conn net.Conn) error { _, err := io.Copy(conn, conn); return err })
	synthetic := newSyntheticAndroidVPN(t, func(ctx context.Context, _, address string) (net.Conn, error) {
		if address != "203.0.113.5:8080" {
			return nil, fmt.Errorf("proxy app traffic looped into After chain: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", echo.address())
	}, vpn, "203.0.113.53", false, true)
	normal := synthetic.dialTCP(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rawConn, err := synthetic.host.DialTCP(ctx, &local)
	if err != nil {
		t.Fatal(err)
	}
	defer rawConn.Close()
	for _, conn := range []net.Conn{normal, rawConn} {
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		payload := []byte("independent routing lanes")
		if _, err := conn.Write(payload); err != nil {
			t.Fatal(err)
		}
		answer := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, answer); err != nil || !bytes.Equal(answer, payload) {
			t.Fatalf("routing lane roundtrip failed: %v", err)
		}
	}
}

// testVPNWire is a completely userspace fake company network. Two gVisor stacks
// exchange owned packet buffers; neither side accesses an OS network interface.
func testVPNWire(t *testing.T) (*vpnDialer, *hostnet.NetStack) {
	t.Helper()
	client, err := hostnet.New("10.88.0.2", 1300, "")
	if err != nil {
		t.Fatal(err)
	}
	server, err := hostnet.New("203.0.113.53", 1300, "")
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	copyPackets := func(from, to *hostnet.NetStack) {
		defer workers.Done()
		for {
			packet := from.Link.ReadContext(ctx)
			if packet == nil {
				return
			}
			b := packet.ToBuffer()
			data := append([]byte(nil), b.Flatten()...)
			b.Release()
			packet.DecRef()
			incoming := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(data)})
			to.Link.InjectInbound(ipv4.ProtocolNumber, incoming)
			incoming.DecRef()
		}
	}
	workers.Add(2)
	go copyPackets(client, server)
	go copyPackets(server, client)
	t.Cleanup(func() {
		cancel()
		workers.Wait()
		client.Close()
		server.Close()
	})
	return &vpnDialer{ns: client, dns: []string{"203.0.113.53"}}, server
}

func TestAndroidPacketBridgeDNSUsesLengthFramedRawVPN(t *testing.T) {
	vpn, network := testVPNWire(t)
	listener, err := gonet.ListenTCP(network.Stack, tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 53}), Port: 53}, ipv4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	// A minimal standard A query for example.test, preserved byte-for-byte.
	query := []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 4, 't', 'e', 's', 't', 0, 0, 1, 0, 1}
	answer := append([]byte(nil), query...)
	answer[2] = 0x81
	answer[3] = 0x80
	seen := make(chan error, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			seen <- err
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		var size [2]byte
		if _, err = io.ReadFull(c, size[:]); err == nil {
			data := make([]byte, binary.BigEndian.Uint16(size[:]))
			_, err = io.ReadFull(c, data)
			if err == nil && !bytes.Equal(data, query) {
				err = fmt.Errorf("DNS TCP framing changed query: %x", data)
			}
		}
		if err == nil {
			wire := binary.BigEndian.AppendUint16(nil, uint16(len(answer)))
			wire = append(wire, answer...)
			_, err = c.Write(wire)
		}
		seen <- err
	}()
	finalCalls := make(chan string, 1)
	synthetic := newSyntheticAndroidVPN(t, func(_ context.Context, _, address string) (net.Conn, error) {
		finalCalls <- address
		return nil, errors.New("DNS must use raw VPN, not after-VPN proxy chain")
	}, vpn, "203.0.113.53", false)
	client, err := synthetic.host.DialUDP(context.Background(), &tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{10, 77, 0, 1}), Port: 53})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.Write(query); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 1024)
	n, err := client.Read(data)
	if err != nil || !bytes.Equal(data[:n], answer) {
		t.Fatalf("DNS UDP reply=%x err=%v", data[:n], err)
	}
	if err := <-seen; err != nil {
		t.Fatal(err)
	}
	select {
	case target := <-finalCalls:
		t.Fatalf("DNS leaked to final proxy dial: %s", target)
	default:
	}
}

func TestAndroidPacketBridgeUDPAllowedAndRejectedWithChain(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		t.Run(fmt.Sprintf("allowUDP=%t", allowed), func(t *testing.T) {
			vpn, network := testVPNWire(t)
			local := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 53}), Port: 8080}
			listener, err := gonet.DialUDP(network.Stack, &local, nil, ipv4.ProtocolNumber)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { listener.Close() })
			received := make(chan []byte, 1)
			go func() {
				listener.SetReadDeadline(time.Now().Add(2 * time.Second))
				data := make([]byte, 1024)
				n, source, err := listener.ReadFrom(data)
				if err == nil {
					received <- append([]byte(nil), data[:n]...)
					listener.WriteTo([]byte(strings.ToUpper(string(data[:n]))), source)
				}
			}()
			finalCalls := make(chan string, 1)
			synthetic := newSyntheticAndroidVPN(t, func(_ context.Context, _, address string) (net.Conn, error) {
				finalCalls <- address
				return nil, errors.New("UDP must not fall back to TCP/proxy dial")
			}, vpn, "203.0.113.53", allowed)
			client, err := synthetic.host.DialUDP(context.Background(), &local)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			client.SetDeadline(time.Now().Add(500 * time.Millisecond))
			if _, err := client.Write([]byte("vpn datagram")); err != nil {
				t.Fatal(err)
			}
			data := make([]byte, 1024)
			n, err := client.Read(data)
			if allowed {
				if err != nil || string(data[:n]) != "VPN DATAGRAM" {
					t.Fatalf("UDP echo=%q err=%v", data[:n], err)
				}
				if got := <-received; string(got) != "vpn datagram" {
					t.Fatalf("VPN network received=%q", got)
				}
			} else {
				if err == nil {
					t.Fatalf("UDP unexpectedly traversed a TCP-only after chain: %q", data[:n])
				}
				select {
				case got := <-received:
					t.Fatalf("rejected UDP leaked to raw VPN: %q", got)
				default:
				}
			}
			select {
			case target := <-finalCalls:
				t.Fatalf("UDP attempted TCP/proxy fallback: %s", target)
			default:
			}
		})
	}
}

func TestAndroidPacketBridgeCancellationClosesPendingDNS(t *testing.T) {
	vpn, network := testVPNWire(t)
	listener, err := gonet.ListenTCP(network.Stack, tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 53}), Port: 53}, ipv4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	queryArrived := make(chan struct{})
	remoteDone := make(chan error, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			remoteDone <- err
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		var size [2]byte
		if _, err = io.ReadFull(c, size[:]); err == nil {
			_, err = io.CopyN(io.Discard, c, int64(binary.BigEndian.Uint16(size[:])))
		}
		if err != nil {
			remoteDone <- err
			return
		}
		close(queryArrived)
		var next [1]byte
		_, err = c.Read(next[:])
		remoteDone <- err
	}()
	synthetic := newSyntheticAndroidVPN(t, func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("DNS must use raw VPN")
	}, vpn, "203.0.113.53", false)
	client, err := synthetic.host.DialUDP(context.Background(), &tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{10, 77, 0, 1}), Port: 53})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(time.Second))
	if _, err := client.Write([]byte{0x12, 0x34, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-queryArrived:
	case <-time.After(time.Second):
		t.Fatal("raw VPN DNS server did not receive the pending query")
	}
	synthetic.cancel()
	stopped := make(chan struct{})
	go func() { synthetic.stopBridge(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("DNS flow delayed bridge cleanup until lookup timeout")
	}
	select {
	case err := <-remoteDone:
		if err == nil {
			t.Fatal("pending DNS connection remained open")
		}
	case <-time.After(time.Second):
		t.Fatal("pending raw VPN DNS connection did not close on cancellation")
	}
}

func (s *syntheticAndroid) dialTCP(t *testing.T) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := s.host.DialTCP(ctx, &tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 5}), Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestAndroidPacketBridgeTCPRoundTrip(t *testing.T) {
	const payload = "synthetic Android IP tunnel"
	echo := startTestPeer(t, func(c net.Conn) error {
		data := make([]byte, len(payload))
		if _, err := io.ReadFull(c, data); err != nil {
			return err
		}
		_, err := io.WriteString(c, strings.ToUpper(string(data)))
		return err
	})
	destinations := make(chan string, 2)
	synthetic := newSyntheticAndroid(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		destinations <- address
		if network != "tcp" || address != "203.0.113.5:8080" {
			return nil, fmt.Errorf("unexpected bridged destination %s %s", network, address)
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", echo.address())
	})
	c := synthetic.dialTCP(t)
	testExchange(t, c, payload)
	c.Close()
	select {
	case got := <-destinations:
		if got != "203.0.113.5:8080" {
			t.Fatalf("final tunnel dial target=%s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("bridge did not invoke final tunnel dial")
	}
	select {
	case extra := <-destinations:
		t.Fatalf("unexpected second tunnel dial: %s", extra)
	default:
	}
	echo.wait(t)
}

func TestAndroidPacketBridgeCancellationClosesActiveTCP(t *testing.T) {
	remoteDone := make(chan error, 1)
	synthetic := newSyntheticAndroid(t, func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			var one [1]byte
			_, err := b.Read(one[:])
			remoteDone <- err
		}()
		return a, nil
	})
	c := synthetic.dialTCP(t)
	synthetic.cancel()
	select {
	case err := <-remoteDone:
		if err == nil {
			t.Fatal("active tunnel remained open after parent cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not close the bridge's active TCP tunnel")
	}
	c.Close()
	select {
	case <-synthetic.hostDone:
	case <-time.After(time.Second):
		t.Fatal("synthetic Android packet pumps did not react to cancellation")
	}
	stopped := make(chan struct{})
	go func() { synthetic.stopBridge(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("bridge cleanup did not finish after parent cancellation")
	}
	if _, err := synthetic.bridgeUDP.WriteToUDP([]byte{0}, synthetic.hostUDP.LocalAddr().(*net.UDPAddr)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("bridge transport remained open after cleanup: %v", err)
	}
}

// Exercise packet ownership and local UDP buffering with concurrent streams,
// then let the fixture verify bounded shutdown. This is not a phone benchmark.
func TestAndroidPacketBridgeConcurrentTransfer(t *testing.T) {
	addresses := make(chan string, 4)
	for range 4 {
		echo := startTestPeer(t, func(conn net.Conn) error {
			conn.SetDeadline(time.Now().Add(20 * time.Second))
			_, err := io.Copy(conn, conn)
			return err
		})
		addresses <- echo.address()
	}
	synthetic := newSyntheticAndroid(t, func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", <-addresses)
	})
	connections := make([]net.Conn, 4)
	for i := range connections {
		connections[i] = synthetic.dialTCP(t)
	}
	results := make(chan error, len(connections))
	for i, conn := range connections {
		go func(index int, conn net.Conn) {
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(20 * time.Second))
			payload := bytes.Repeat([]byte{byte(index + 1), 0x55, 0xaa, 0xff}, 256*1024)
			written := make(chan error, 1)
			go func() { _, err := io.Copy(conn, bytes.NewReader(payload)); written <- err }()
			received := make([]byte, len(payload))
			_, err := io.ReadFull(conn, received)
			if err == nil && !bytes.Equal(received, payload) {
				err = fmt.Errorf("stream %d packet data corrupted", index)
			}
			if writeErr := <-written; err == nil {
				err = writeErr
			}
			results <- err
		}(i, conn)
	}
	for range connections {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}
