package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

// Android's VpnService owns the OS descriptor. Its foreground service exchanges
// complete IP packets on an authenticated per-session loopback datagram channel.
// The app UID is excluded by VpnService, keeping engine TLS sockets off the TUN.
func startAndroidTUN(parent context.Context, vpn *vpnDialer, dns []string, final Dial, allowUDP, localExit bool) (func(), <-chan error, error) {
	if len(dns) == 0 {
		return nil, nil, errors.New("Android VPN mode needs a VPN DNS server or IPv4 DNS override")
	}
	socket, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		return nil, nil, e
	}
	socket.SetReadBuffer(4 << 20)
	socket.SetWriteBuffer(4 << 20)
	var token [32]byte
	if _, e = rand.Read(token[:]); e != nil {
		socket.Close()
		return nil, nil, e
	}
	secret := hex.EncodeToString(token[:])
	emit("tun", "Starting Android VPN interface", map[string]any{"port": socket.LocalAddr().(*net.UDPAddr).Port, "token": secret, "address": "10.77.0.2", "prefix": 30, "mtu": 1300})
	stopCancel := context.AfterFunc(parent, func() { socket.Close() })
	defer stopCancel()
	socket.SetReadDeadline(time.Now().Add(20 * time.Second))
	hello := make([]byte, 256)
	var peer *net.UDPAddr
	for {
		n, source, err := socket.ReadFromUDP(hello)
		if err != nil {
			socket.Close()
			return nil, nil, errors.New("Android VPN interface handshake failed")
		}
		if source.IP.IsLoopback() && subtle.ConstantTimeCompare(hello[:n], []byte(secret)) == 1 {
			peer = source
			break
		}
	}
	socket.SetReadDeadline(time.Time{})
	if !localExit {
		return startPacketBridge(parent, socket, peer, final, vpn, dns[0], allowUDP)
	}
	return startAndroidExitBridge(parent, socket, peer, final, vpn, dns[0])
}

// startPacketBridge is also exercised on Windows with a synthetic Android host.
func startPacketBridge(parent context.Context, socket *net.UDPConn, peer *net.UDPAddr, final Dial, vpn *vpnDialer, dns string, allowUDP bool) (func(), <-chan error, error) {
	ctx, cancel := context.WithCancel(parent)
	socket.SetReadBuffer(4 << 20)
	socket.SetWriteBuffer(4 << 20)
	ns := stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol}, TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol, icmp.NewProtocol4, icmp.NewProtocol6}})
	link := channel.New(1024, 1300, "")
	if e := ns.CreateNIC(1, link); e != nil {
		cancel()
		socket.Close()
		ns.Destroy()
		return nil, nil, fmt.Errorf("Android IP bridge: %v", e)
	}
	ns.SetPromiscuousMode(1, true)
	ns.SetSpoofing(1, true)
	ns.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}, {Destination: header.IPv6EmptySubnet, NIC: 1}})
	errorsOut := make(chan error, 4)
	var workers sync.WaitGroup
	var flows sync.WaitGroup
	var life sync.Mutex
	closed := false
	begin := func() bool {
		life.Lock()
		defer life.Unlock()
		if closed {
			return false
		}
		flows.Add(1)
		return true
	}
	stopSocket := context.AfterFunc(ctx, func() { socket.Close() })
	slots := make(chan struct{}, 256)
	forwardTCP := tcp.NewForwarder(ns, 0, 256, func(r *tcp.ForwarderRequest) {
		if !begin() {
			r.Complete(true)
			return
		}
		defer flows.Done()
		select {
		case slots <- struct{}{}:
		default:
			r.Complete(true)
			return
		}
		defer func() { <-slots }()
		id := r.ID()
		host := id.LocalAddress.String()
		if net.ParseIP(host).To4() == nil {
			r.Complete(true)
			return
		}
		dial := final
		target := net.JoinHostPort(host, strconv.Itoa(int(id.LocalPort)))
		if id.LocalPort == 53 {
			target = net.JoinHostPort(dns, "53")
			dial = vpn.DialContext
		}
		var remote net.Conn
		if id.LocalPort != 53 {
			connect, done := context.WithTimeout(ctx, 25*time.Second)
			var e error
			remote, e = dial(connect, "tcp", target)
			done()
			if e != nil {
				r.Complete(true)
				return
			}
			stopRemote := context.AfterFunc(ctx, func() { remote.Close() })
			defer stopRemote()
			defer remote.Close()
		}
		var queue waiter.Queue
		endpoint, err := r.CreateEndpoint(&queue)
		r.Complete(err != nil)
		if err != nil {
			return
		}
		conn := gonet.NewTCPConn(&queue, endpoint)
		defer conn.Close()
		if id.LocalPort == 53 {
			serveBridgeTCPDNS(ctx, conn, vpn, dns)
			return
		}
		relay(ctx, conn, remote)
	})
	ns.SetTransportProtocolHandler(tcp.ProtocolNumber, forwardTCP.HandlePacket)
	forwardUDP := udp.NewForwarder(ns, func(r *udp.ForwarderRequest) bool {
		id := r.ID()
		if net.ParseIP(id.LocalAddress.String()).To4() == nil || id.LocalPort == 443 || id.LocalPort != 53 && !allowUDP {
			return false
		}
		if !begin() {
			return false
		}
		select {
		case slots <- struct{}{}:
		default:
			flows.Done()
			return false
		}
		var queue waiter.Queue
		endpoint, e := r.CreateEndpoint(&queue)
		if e != nil {
			<-slots
			flows.Done()
			return true
		}
		conn := gonet.NewUDPConn(&queue, endpoint)
		go func() {
			defer flows.Done()
			defer func() { <-slots }()
			defer conn.Close()
			stop := context.AfterFunc(ctx, func() { conn.Close() })
			defer stop()
			if id.LocalPort == 53 {
				serveBridgeDNS(ctx, conn, vpn, dns)
				return
			}
			connect, done := context.WithTimeout(ctx, 10*time.Second)
			remote, e := vpn.DialContext(connect, "udp", net.JoinHostPort(id.LocalAddress.String(), strconv.Itoa(int(id.LocalPort))))
			done()
			if e != nil {
				return
			}
			defer remote.Close()
			stopRemote := context.AfterFunc(ctx, func() { remote.Close() })
			defer stopRemote()
			finished := make(chan struct{}, 1)
			go func() {
				defer func() { finished <- struct{}{} }()
				data := make([]byte, 65535)
				for {
					remote.SetReadDeadline(time.Now().Add(time.Minute))
					n, e := remote.Read(data)
					if e != nil {
						return
					}
					if _, e = conn.Write(data[:n]); e != nil {
						return
					}
				}
			}()
			data := make([]byte, 65535)
			for {
				conn.SetReadDeadline(time.Now().Add(time.Minute))
				n, e := conn.Read(data)
				if e != nil {
					break
				}
				remote.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if _, e = remote.Write(data[:n]); e != nil {
					break
				}
			}
			remote.Close()
			conn.Close()
			<-finished
		}()
		return true
	})
	ns.SetTransportProtocolHandler(udp.ProtocolNumber, forwardUDP.HandlePacket)
	workers.Add(2)
	go func() {
		defer workers.Done()
		data := make([]byte, 65535)
		for {
			n, source, e := socket.ReadFromUDP(data)
			if e != nil {
				errorsOut <- e
				return
			}
			if source.Port != peer.Port || !source.IP.Equal(peer.IP) || n < 1 {
				continue
			}
			var protocol tcpip.NetworkProtocolNumber
			switch data[0] >> 4 {
			case 4:
				protocol = ipv4.ProtocolNumber
			case 6:
				protocol = ipv6.ProtocolNumber
			default:
				continue
			}
			packet := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), data[:n]...))})
			link.InjectInbound(protocol, packet)
			packet.DecRef()
		}
	}()
	go func() {
		defer workers.Done()
		for {
			packet := link.ReadContext(ctx)
			if packet == nil {
				return
			}
			b := packet.ToBuffer()
			data := append([]byte(nil), b.Flatten()...)
			b.Release()
			packet.DecRef()
			if _, e := socket.WriteToUDP(data, peer); e != nil {
				errorsOut <- e
				return
			}
		}
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			life.Lock()
			closed = true
			life.Unlock()
			cancel()
			socket.Close()
			ns.Destroy()
			workers.Wait()
			flows.Wait()
			stopSocket()
		})
	}
	return stop, errorsOut, nil
}

// DNS from Android's virtual resolver is sent as length-framed TCP over the raw
// VPN tunnel, never through the phone's physical resolver or a direct path.
func serveBridgeDNS(ctx context.Context, client net.Conn, vpn *vpnDialer, server string) {
	query := make([]byte, 65535)
	for {
		client.SetReadDeadline(time.Now().Add(time.Minute))
		n, e := client.Read(query)
		if e != nil {
			return
		}
		if n < 12 {
			continue
		}
		if answer, err := bridgeDNS(ctx, query[:n], vpn, server); err == nil {
			client.Write(answer)
		}
	}
}

func serveBridgeTCPDNS(ctx context.Context, client net.Conn, vpn *vpnDialer, server string) {
	for {
		client.SetDeadline(time.Now().Add(time.Minute))
		var size [2]byte
		if _, err := io.ReadFull(client, size[:]); err != nil {
			return
		}
		query := make([]byte, binary.BigEndian.Uint16(size[:]))
		if _, err := io.ReadFull(client, query); err != nil {
			return
		}
		answer, err := bridgeDNS(ctx, query, vpn, server)
		if err != nil {
			return
		}
		wire := binary.BigEndian.AppendUint16(nil, uint16(len(answer)))
		if _, err := client.Write(append(wire, answer...)); err != nil {
			return
		}
	}
}
