//go:build linux && !android

package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Match the listener inode to an executable visible to the current user.
func localExitProcess(hop Hop) (string, error) {
	host := net.ParseIP(hop.Host).To4()
	if strings.EqualFold(hop.Host, "localhost") {
		host = net.IPv4(127, 0, 0, 1).To4()
	}
	if host == nil {
		return "", errors.New("use an IPv4 loopback address for the local exit proxy")
	}
	endpoint := fmt.Sprintf("%02X%02X%02X%02X:%04X", host[3], host[2], host[1], host[0], hop.Port)
	wildcard := fmt.Sprintf("00000000:%04X", hop.Port)
	table, err := os.Open("/proc/net/tcp")
	if err != nil {
		return "", errors.New("could not inspect IPv4 listeners")
	}
	defer table.Close()
	var exactInode, wildcardInode string
	scanner := bufio.NewScanner(table)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 || fields[3] != "0A" {
			continue
		}
		switch fields[1] {
		case endpoint:
			exactInode = fields[9]
		case wildcard:
			wildcardInode = fields[9]
		}
	}
	if scanner.Err() != nil {
		return "", errors.New("could not read IPv4 listeners")
	}
	inode := exactInode
	if inode == "" {
		inode = wildcardInode
	}
	if inode == "" {
		return "", errors.New("no IPv4 TCP listener found on the local exit proxy port; start the proxy first")
	}
	processes, err := os.ReadDir("/proc")
	if err != nil {
		return "", errors.New("could not list local proxy processes")
	}
	socket := "socket:[" + inode + "]"
	for _, process := range processes {
		if _, err := strconv.Atoi(process.Name()); err != nil {
			continue
		}
		dir := filepath.Join("/proc", process.Name())
		fds, _ := os.ReadDir(filepath.Join(dir, "fd"))
		for _, fd := range fds {
			if target, _ := os.Readlink(filepath.Join(dir, "fd", fd.Name())); target != socket {
				continue
			}
			if executable, err := os.Readlink(filepath.Join(dir, "exe")); err == nil && filepath.IsAbs(executable) && !strings.HasSuffix(executable, " (deleted)") {
				return executable, nil
			}
		}
	}
	return "", errors.New("cannot identify the executable listening on the local exit port; run the proxy as your user")
}
