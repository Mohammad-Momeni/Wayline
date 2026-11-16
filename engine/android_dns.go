package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"time"
)

// Return NODATA for records which direct apps toward unsupported IPv6 or QUIC.
// A/AAAA clients can then select the IPv4 TCP path without waiting for a timeout.
func ipv4DNSAnswer(query []byte) []byte {
	end, kind := dnsQuestion(query)
	if end == 0 || kind != 28 && kind != 64 && kind != 65 || query[2]&0xf8 != 0 {
		return nil
	}
	answer := append([]byte(nil), query[:end]...)
	answer[2] = 0x80 | query[2]&1
	answer[3] = 0x80
	clear(answer[6:12])
	return answer
}

func dnsQuestion(query []byte) (int, uint16) {
	if len(query) < 17 || binary.BigEndian.Uint16(query[4:6]) != 1 {
		return 0, 0
	}
	position := 12
	for position < len(query) {
		length := int(query[position])
		position++
		if length == 0 {
			if position+4 > len(query) {
				return 0, 0
			}
			return position + 4, binary.BigEndian.Uint16(query[position:])
		}
		if length > 63 || position+length > len(query) {
			return 0, 0
		}
		position += length
	}
	return 0, 0
}

func bridgeDNS(ctx context.Context, query []byte, vpn *vpnDialer, server string) ([]byte, error) {
	if len(query) < 12 {
		return nil, errors.New("invalid DNS query")
	}
	if answer := ipv4DNSAnswer(query); answer != nil {
		return answer, nil
	}
	valid := func(answer []byte) bool {
		if len(answer) < 12 || !bytes.Equal(query[:2], answer[:2]) || answer[2]&0x80 == 0 {
			return false
		}
		end, _ := dnsQuestion(query)
		return end == 0 || len(answer) >= end && bytes.Equal(query[12:end], answer[12:end])
	}
	// Company resolvers often only admit UDP. Retry TCP for truncation or loss.
	for _, network := range []string{"udp", "tcp"} {
		timeout := 700 * time.Millisecond
		if network == "tcp" {
			timeout = 7 * time.Second
		}
		lookup, cancel := context.WithTimeout(ctx, timeout)
		conn, err := vpn.DialContext(lookup, network, net.JoinHostPort(server, "53"))
		if err != nil {
			cancel()
			continue
		}
		deadline, _ := lookup.Deadline()
		conn.SetDeadline(deadline)
		stop := context.AfterFunc(lookup, func() { conn.Close() })
		answer := make([]byte, 65535)
		if network == "udp" {
			_, err = conn.Write(query)
			if err == nil {
				var count int
				count, err = conn.Read(answer)
				answer = answer[:count]
			}
		} else {
			wire := binary.BigEndian.AppendUint16(nil, uint16(len(query)))
			_, err = conn.Write(append(wire, query...))
			if err == nil {
				var size [2]byte
				_, err = io.ReadFull(conn, size[:])
				if err == nil {
					answer = answer[:binary.BigEndian.Uint16(size[:])]
					_, err = io.ReadFull(conn, answer)
				}
			}
		}
		conn.Close()
		stop()
		cancel()
		if err == nil && valid(answer) && answer[2]&2 == 0 {
			return answer, nil
		}
	}
	return nil, errors.New("company DNS did not answer over UDP or TCP")
}
