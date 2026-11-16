package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ProxyServer struct {
	dial       Dial
	vpn        *vpnDialer
	udpAllowed bool
}

func (s *ProxyServer) Serve(ctx context.Context, l net.Listener) error {
	stop := context.AfterFunc(ctx, func() { l.Close() })
	defer stop()
	slots := make(chan struct{}, 256)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		c, e := l.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return e
		}
		select {
		case slots <- struct{}{}:
			wg.Add(1)
			go func() { defer wg.Done(); defer func() { <-slots }(); s.handle(ctx, c) }()
		default:
			c.Close()
		}
	}
}
func (s *ProxyServer) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	c.SetDeadline(time.Now().Add(20 * time.Second))
	br := bufio.NewReaderSize(c, 32768)
	b, e := br.Peek(1)
	if e != nil {
		return
	}
	wrapped := &bufferedConn{Conn: c, r: br}
	if b[0] == 5 {
		s.socks(ctx, wrapped)
	} else {
		s.http(ctx, wrapped, br)
	}
}
func (s *ProxyServer) connect(ctx context.Context, address string) (net.Conn, error) {
	dialCtx, done := context.WithTimeout(ctx, 25*time.Second)
	defer done()
	return s.dial(dialCtx, "tcp", address)
}
func relay(ctx context.Context, a, b net.Conn) {
	defer b.Close()
	a.SetDeadline(time.Time{})
	b.SetDeadline(time.Time{})
	stop := context.AfterFunc(ctx, func() { a.Close(); b.Close() })
	defer stop()
	done := make(chan struct{}, 1)
	go func() {
		io.Copy(b, a)
		if c, ok := b.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		} else {
			b.Close()
		}
		done <- struct{}{}
	}()
	io.Copy(a, b)
	a.Close()
	b.Close()
	<-done
}
func readSocksAddress(r io.Reader, kind byte) (string, error) {
	var host string
	switch kind {
	case 1:
		var b [4]byte
		if _, e := io.ReadFull(r, b[:]); e != nil {
			return "", e
		}
		host = net.IP(b[:]).String()
	case 4:
		var b [16]byte
		if _, e := io.ReadFull(r, b[:]); e != nil {
			return "", e
		}
		host = net.IP(b[:]).String()
	case 3:
		var length [1]byte
		if _, e := io.ReadFull(r, length[:]); e != nil {
			return "", e
		}
		if length[0] == 0 {
			return "", errors.New("empty domain")
		}
		name := make([]byte, length[0])
		if _, e := io.ReadFull(r, name); e != nil {
			return "", e
		}
		host = string(name)
		if !validHost(host) {
			return "", errors.New("invalid domain")
		}
	default:
		return "", errors.New("invalid SOCKS address")
	}
	var p [2]byte
	if _, e := io.ReadFull(r, p[:]); e != nil {
		return "", e
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(p[:])))), nil
}
func reply(c net.Conn, code byte) { c.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0}) }
func (s *ProxyServer) socks(ctx context.Context, c net.Conn) {
	var h [2]byte
	if _, e := io.ReadFull(c, h[:]); e != nil {
		return
	}
	if h[0] != 5 || h[1] == 0 {
		return
	}
	methods := make([]byte, h[1])
	if _, e := io.ReadFull(c, methods); e != nil {
		return
	}
	accepted := false
	for _, m := range methods {
		if m == 0 {
			accepted = true
		}
	}
	if !accepted {
		c.Write([]byte{5, 255})
		return
	}
	if _, e := c.Write([]byte{5, 0}); e != nil {
		return
	}
	var request [4]byte
	if _, e := io.ReadFull(c, request[:]); e != nil {
		return
	}
	if request[0] != 5 || request[2] != 0 {
		reply(c, 1)
		return
	}
	address, e := readSocksAddress(c, request[3])
	if e != nil {
		reply(c, 8)
		return
	}
	if request[1] == 3 {
		if s.udpAllowed && s.vpn != nil {
			s.udp(ctx, c, address)
		} else {
			reply(c, 7)
		}
		return
	}
	if request[1] != 1 {
		reply(c, 7)
		return
	}
	remote, e := s.connect(ctx, address)
	if e != nil {
		reply(c, 4)
		return
	}
	reply(c, 0)
	relay(ctx, c, remote)
}
func (s *ProxyServer) http(ctx context.Context, c net.Conn, br *bufio.Reader) {
	// Read bounded headers before net/http parses them; preserve buffered body bytes.
	var headers strings.Builder
	for headers.Len() <= 32768 {
		part, e := br.ReadSlice('\n')
		line := string(part)
		if e != nil {
			return
		}
		headers.WriteString(line)
		if line == "\r\n" {
			break
		}
	}
	if headers.Len() > 32768 {
		fmt.Fprint(c, "HTTP/1.1 431 Request Header Fields Too Large\r\nContent-Length: 0\r\n\r\n")
		return
	}
	requestReader := bufio.NewReader(io.MultiReader(strings.NewReader(headers.String()), br))
	req, e := http.ReadRequest(requestReader)
	if e != nil {
		return
	}
	if req.Method == "CONNECT" {
		address := req.Host
		if _, _, e := net.SplitHostPort(address); e != nil {
			address = net.JoinHostPort(address, "443")
		}
		remote, e := s.connect(ctx, address)
		if e != nil {
			fmt.Fprint(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
			return
		}
		fmt.Fprint(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
		relay(ctx, &bufferedConn{Conn: c, r: requestReader}, remote)
		return
	}
	if req.URL.Scheme != "http" || req.URL.Host == "" {
		fmt.Fprint(c, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
		return
	}
	req.RequestURI = ""
	req.Close = true
	req.Header.Del("Proxy-Authorization")
	req.Header.Del("Proxy-Connection")
	transport := &http.Transport{DialContext: s.dial, DisableKeepAlives: true, ResponseHeaderTimeout: 30 * time.Second}
	defer transport.CloseIdleConnections()
	c.SetDeadline(time.Time{})
	response, e := transport.RoundTrip(req.WithContext(ctx))
	if e != nil {
		fmt.Fprint(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer response.Body.Close()
	response.Close = true
	response.Write(c)
}

// UDP is available only without an after-VPN proxy chain. Each flow's DNS and
// datagrams stay in the VPN stack; no fallback uses the host's network.
func (s *ProxyServer) udp(ctx context.Context, c net.Conn, _ string) {
	socket, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		reply(c, 1)
		return
	}
	defer socket.Close()
	port := socket.LocalAddr().(*net.UDPAddr).Port
	c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, byte(port >> 8), byte(port)})
	c.SetDeadline(time.Time{})
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(child, func() { socket.Close() })
	defer stop()
	go func() { io.Copy(io.Discard, c); cancel() }()
	type flow struct {
		c      net.Conn
		header []byte
	}
	flows := map[string]flow{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	defer func() {
		cancel()
		mu.Lock()
		for _, f := range flows {
			f.c.Close()
		}
		mu.Unlock()
		wg.Wait()
	}()
	var peer *net.UDPAddr
	packet := make([]byte, 65535)
	for {
		n, source, e := socket.ReadFromUDP(packet)
		if e != nil {
			return
		}
		if !source.IP.IsLoopback() {
			continue
		}
		if peer == nil {
			peer = source
		}
		if source.Port != peer.Port || !source.IP.Equal(peer.IP) {
			continue
		}
		if n < 4 || packet[0] != 0 || packet[1] != 0 || packet[2] != 0 {
			continue
		}
		r := strings.NewReader(string(packet[4:n]))
		address, e := readSocksAddress(r, packet[3])
		if e != nil {
			continue
		}
		offset := n - r.Len()
		header := append([]byte(nil), packet[:offset]...)
		payload := append([]byte(nil), packet[offset:n]...)
		mu.Lock()
		f, ok := flows[address]
		mu.Unlock()
		if !ok {
			mu.Lock()
			full := len(flows) >= 128
			mu.Unlock()
			if full {
				continue
			}
			dialCtx, done := context.WithTimeout(child, 5*time.Second)
			remote, e := s.vpn.DialContext(dialCtx, "udp", address)
			done()
			if e != nil {
				continue
			}
			f = flow{remote, header}
			mu.Lock()
			flows[address] = f
			mu.Unlock()
			wg.Add(1)
			go func(key string, f flow, peer net.UDPAddr) {
				defer wg.Done()
				defer f.c.Close()
				defer func() { mu.Lock(); delete(flows, key); mu.Unlock() }()
				data := make([]byte, 65535)
				for {
					f.c.SetReadDeadline(time.Now().Add(60 * time.Second))
					n, e := f.c.Read(data)
					if e != nil {
						return
					}
					if _, e = socket.WriteToUDP(append(append([]byte(nil), f.header...), data[:n]...), &peer); e != nil {
						return
					}
				}
			}(address, f, *peer)
		}
		f.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		f.c.Write(payload)
	}
}
