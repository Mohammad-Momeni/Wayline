package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

func TestAndroidIPv4DNSDoesNotAdvertiseIPv6OrHTTP3Hints(t *testing.T) {
	query := []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 1, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 4, 't', 'e', 's', 't', 0, 0, 28, 0, 1}
	for _, kind := range []uint16{28, 64, 65} {
		binary.BigEndian.PutUint16(query[len(query)-4:], kind)
		answer, err := bridgeDNS(context.Background(), query, nil, "")
		if err != nil || len(answer) != len(query) || !bytes.Equal(answer[12:], query[12:]) || answer[2] != 0x81 || answer[3] != 0x80 || answer[11] != 0 {
			t.Fatalf("invalid IPv4-only DNS answer, type=%d", kind)
		}
	}
	binary.BigEndian.PutUint16(query[len(query)-4:], 1)
	if ipv4DNSAnswer(query) != nil {
		t.Fatal("IPv4 DNS question was suppressed")
	}
}

func TestAndroidDNSWorksWithUDPOnlyCompanyResolver(t *testing.T) {
	vpn, network := testVPNWire(t)
	local := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 53}), Port: 53}
	listener, err := gonet.DialUDP(network.Stack, &local, nil, ipv4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	query := []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 4, 't', 'e', 's', 't', 0, 0, 1, 0, 1}
	answer := append([]byte(nil), query...)
	answer[2], answer[3] = 0x81, 0x80
	go func() {
		listener.SetReadDeadline(time.Now().Add(time.Second))
		data := make([]byte, 512)
		n, source, err := listener.ReadFrom(data)
		if err == nil && bytes.Equal(data[:n], query) {
			listener.WriteTo(answer, source)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reply, err := bridgeDNS(ctx, query, vpn, net.IPv4(203, 0, 113, 53).String())
	if err != nil || !bytes.Equal(reply, answer) {
		t.Fatalf("UDP-only DNS response failed: %v", err)
	}
}

func TestProxyDestinationResolutionUsesUDPOnlyCompanyDNS(t *testing.T) {
	vpn, network := testVPNWire(t)
	local := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 53}), Port: 53}
	listener, err := gonet.DialUDP(network.Stack, &local, nil, ipv4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		listener.SetReadDeadline(time.Now().Add(time.Second))
		query := make([]byte, 1024)
		n, peer, err := listener.ReadFrom(query)
		if err != nil {
			return
		}
		end, kind := dnsQuestion(query[:n])
		if end == 0 || kind != 1 {
			return
		}
		answer := append([]byte(nil), query[:end]...)
		answer[2], answer[3] = 0x81, 0x80
		clear(answer[6:12])
		binary.BigEndian.PutUint16(answer[6:], 1)
		answer = append(answer, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 30, 0, 4, 203, 0, 113, 88)
		listener.WriteTo(answer, peer)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ip, err := vpn.resolve(ctx, "example.test")
	if err != nil || ip != "203.0.113.88" {
		t.Fatalf("UDP-only destination lookup failed: %s %v", ip, err)
	}
}
