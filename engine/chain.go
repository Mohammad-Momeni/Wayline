package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Hop struct {
	Enabled  *bool     `json:"enabled,omitempty"`
	Type     string    `json:"type"`
	Host     string    `json:"host"`
	Port     int       `json:"port"`
	Username string    `json:"username"`
	Password string    `json:"password"`
	Xray     *XrayExit `json:"xray,omitempty"`
}

func activeHops(hops []Hop) []Hop {
	active := make([]Hop, 0, len(hops))
	for _, hop := range hops {
		if hop.Enabled == nil || *hop.Enabled {
			active = append(active, hop)
		}
	}
	return active
}

type Dial func(context.Context, string, string) (net.Conn, error)

func (h Hop) address() string { return net.JoinHostPort(h.Host, strconv.Itoa(h.Port)) }
func validHost(h string) bool {
	if net.ParseIP(h) != nil {
		return true
	}
	if len(h) < 1 || len(h) > 253 {
		return false
	}
	for _, r := range h {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}
func validateHops(hops []Hop) error {
	if len(hops) > 8 {
		return errors.New("at most eight proxy hops are supported on each side")
	}
	for _, h := range hops {
		if h.Type == "xray" {
			if h.Xray == nil {
				return errors.New("select a saved Xray configuration for this hop")
			}
			if err := h.Xray.validate(); err != nil {
				return err
			}
			continue
		}
		if h.Type != "http" && h.Type != "https" && h.Type != "socks5" {
			return errors.New("proxy type must be socks5, http or https")
		}
		if !validHost(h.Host) || h.Port < 1 || h.Port > 65535 || len(h.Username) > 255 || len(h.Password) > 255 {
			return errors.New("invalid proxy endpoint or credentials")
		}
		if strings.ContainsAny(h.Username, "\r\n") || strings.ContainsAny(h.Password, "\r\n") {
			return errors.New("invalid proxy credentials")
		}
	}
	return nil
}

// chain connects each hop in traffic order; every target name is resolved by the
// next proxy. Only the first hop is resolved by the base transport.
func chain(base Dial, hops []Hop) Dial {
	hops = activeHops(hops)
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" {
			return nil, errors.New("proxy chains support TCP only")
		}
		if len(hops) == 0 {
			return base(ctx, network, address)
		}
		c, err := base(ctx, "tcp", hops[0].address())
		if err != nil {
			return nil, err
		}
		success := false
		defer func() {
			if !success {
				c.Close()
			}
		}()
		end := time.Now().Add(25 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(end) {
			end = d
		}
		c.SetDeadline(end)
		outer := c
		stop := context.AfterFunc(ctx, func() { outer.Close() })
		defer stop()
		for i, h := range hops {
			target := address
			if i+1 < len(hops) {
				target = hops[i+1].address()
			}
			if h.Type == "https" {
				secure := tls.Client(c, &tls.Config{ServerName: h.Host, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
				if err = secure.HandshakeContext(ctx); err != nil {
					return nil, fmt.Errorf("proxy hop %d TLS verification failed", i+1)
				}
				c = secure
			}
			if h.Type == "socks5" {
				err = socksConnect(c, h, target)
			} else {
				var next net.Conn
				next, err = httpConnect(c, h, target)
				if err == nil {
					c = next
				}
			}
			if err != nil {
				return nil, fmt.Errorf("proxy hop %d: %w", i+1, err)
			}
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		c.SetDeadline(time.Time{})
		success = true
		return &destinationConn{Conn: c, destination: address}, nil
	}
}

// Report the logical peer rather than the first proxy: Fortinet logout reuses
// RemoteAddr as its target and must reconnect to the gateway, through the chain.
type destinationConn struct {
	net.Conn
	destination string
}
type logicalAddress string

func (a logicalAddress) Network() string        { return "tcp" }
func (a logicalAddress) String() string         { return string(a) }
func (c *destinationConn) RemoteAddr() net.Addr { return logicalAddress(c.destination) }
func socksConnect(c net.Conn, h Hop, target string) error {
	if err := socksAuthenticate(c, h); err != nil {
		return err
	}
	return socksRequest(c, target)
}

func socksAuthenticate(c net.Conn, h Hop) error {
	methods := []byte{5, 1, 0}
	if h.Username != "" || h.Password != "" {
		methods = []byte{5, 1, 2}
	}
	if _, e := c.Write(methods); e != nil {
		return e
	}
	var answer [2]byte
	if _, e := io.ReadFull(c, answer[:]); e != nil {
		return e
	}
	if answer[0] != 5 {
		return errors.New("invalid SOCKS version")
	}
	if answer[1] == 2 && methods[2] == 2 {
		auth := []byte{1, byte(len(h.Username))}
		auth = append(auth, []byte(h.Username)...)
		auth = append(auth, byte(len(h.Password)))
		auth = append(auth, []byte(h.Password)...)
		if _, e := c.Write(auth); e != nil {
			return e
		}
		clear(auth)
		if _, e := io.ReadFull(c, answer[:]); e != nil {
			return e
		}
		if answer[0] != 1 || answer[1] != 0 {
			return errors.New("SOCKS authentication rejected")
		}
	} else if answer[1] != methods[2] {
		return errors.New("SOCKS authentication method rejected")
	}
	return nil
}

func socksRequest(c net.Conn, target string) error {
	host, port, e := net.SplitHostPort(target)
	if e != nil {
		return e
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 1 || n > 65535 {
		return errors.New("invalid target port")
	}
	request := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip != nil {
		if v := ip.To4(); v != nil {
			request = append(request, 1)
			request = append(request, v...)
		} else {
			request = append(request, 4)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return errors.New("target name too long")
		}
		request = append(request, 3, byte(len(host)))
		request = append(request, []byte(host)...)
	}
	request = binary.BigEndian.AppendUint16(request, uint16(n))
	if _, e = c.Write(request); e != nil {
		return e
	}
	var header [4]byte
	if _, e = io.ReadFull(c, header[:]); e != nil {
		return e
	}
	if header[0] != 5 || header[1] != 0 || header[2] != 0 {
		return errors.New("SOCKS CONNECT rejected")
	}
	length := 0
	switch header[3] {
	case 1:
		length = 4
	case 4:
		length = 16
	case 3:
		var b [1]byte
		if _, e = io.ReadFull(c, b[:]); e != nil {
			return e
		}
		length = int(b[0])
	default:
		return errors.New("invalid SOCKS address")
	}
	_, e = io.CopyN(io.Discard, c, int64(length+2))
	return e
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
func httpConnect(c net.Conn, h Hop, target string) (net.Conn, error) {
	if strings.ContainsAny(target, "\r\n") {
		return nil, errors.New("invalid CONNECT target")
	}
	auth := ""
	if h.Username != "" || h.Password != "" {
		auth = "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(h.Username+":"+h.Password)) + "\r\n"
	}
	if _, e := fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", target, target, auth); e != nil {
		return nil, e
	}
	// Limit headers without consuming tunnel bytes; net/http may buffer data.
	br := bufio.NewReaderSize(c, 4096)
	var headers strings.Builder
	for headers.Len() <= 32768 {
		part, e := br.ReadSlice('\n')
		line := string(part)
		if e != nil {
			return nil, e
		}
		headers.WriteString(line)
		if line == "\r\n" {
			break
		}
	}
	if headers.Len() > 32768 {
		return nil, errors.New("proxy headers too large")
	}
	response, e := http.ReadResponse(bufio.NewReader(strings.NewReader(headers.String())), &http.Request{Method: "CONNECT"})
	if e != nil {
		return nil, errors.New("invalid HTTP proxy response")
	}
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP CONNECT returned %d", response.StatusCode)
	}
	return &bufferedConn{Conn: c, r: br}, nil
}
