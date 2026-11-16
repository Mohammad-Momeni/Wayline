package main

import (
	"context"
	"net"
	"sync"
)

// Android marks packets owned by the selected proxy app. A separate IP stack
// sends those flows through the raw VPN, keeping them out of the After chain.
func startAndroidExitBridge(parent context.Context, socket *net.UDPConn, peer *net.UDPAddr, final Dial, vpn *vpnDialer, dns string) (func(), <-chan error, error) {
	ctx, cancel := context.WithCancel(parent)
	front, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		cancel()
		socket.Close()
		return nil, nil, err
	}
	normal, err := net.DialUDP("udp4", nil, front.LocalAddr().(*net.UDPAddr))
	if err != nil {
		cancel()
		front.Close()
		socket.Close()
		return nil, nil, err
	}
	raw, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		cancel()
		normal.Close()
		front.Close()
		socket.Close()
		return nil, nil, err
	}
	detour, err := net.DialUDP("udp4", nil, raw.LocalAddr().(*net.UDPAddr))
	if err != nil {
		cancel()
		raw.Close()
		normal.Close()
		front.Close()
		socket.Close()
		return nil, nil, err
	}
	stopNormal, normalErrors, err := startPacketBridge(ctx, front, normal.LocalAddr().(*net.UDPAddr), final, vpn, dns, false)
	if err != nil {
		cancel()
		detour.Close()
		raw.Close()
		normal.Close()
		socket.Close()
		return nil, nil, err
	}
	stopRaw, rawErrors, err := startPacketBridge(ctx, raw, detour.LocalAddr().(*net.UDPAddr), vpn.DialContext, vpn, dns, true)
	if err != nil {
		cancel()
		stopNormal()
		detour.Close()
		normal.Close()
		socket.Close()
		return nil, nil, err
	}
	errorsOut := make(chan error, 5)
	var workers sync.WaitGroup
	stopSockets := context.AfterFunc(ctx, func() { socket.Close(); normal.Close(); detour.Close() })
	for _, lane := range []*net.UDPConn{normal, detour} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			packet := make([]byte, 65535)
			for {
				n, err := lane.Read(packet)
				if err != nil {
					errorsOut <- err
					return
				}
				if _, err := socket.WriteToUDP(packet[:n], peer); err != nil {
					errorsOut <- err
					return
				}
			}
		}()
	}
	workers.Add(2)
	go func() {
		defer workers.Done()
		packet := make([]byte, 65536)
		for {
			n, source, err := socket.ReadFromUDP(packet)
			if err != nil {
				errorsOut <- err
				return
			}
			if !source.IP.Equal(peer.IP) || source.Port != peer.Port || n == 0 {
				continue
			}
			lane, data := normal, packet[:n]
			if data[0] == 0 {
				lane, data = detour, data[1:]
			}
			if len(data) > 0 {
				if _, err := lane.Write(data); err != nil {
					errorsOut <- err
					return
				}
			}
		}
	}()
	go func() {
		defer workers.Done()
		select {
		case err := <-normalErrors:
			errorsOut <- err
		case err := <-rawErrors:
			errorsOut <- err
		case <-ctx.Done():
		}
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			socket.Close()
			normal.Close()
			detour.Close()
			stopNormal()
			stopRaw()
			workers.Wait()
			stopSockets()
		})
	}
	return stop, errorsOut, nil
}
