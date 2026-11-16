package main

import (
	"encoding/binary"
	"errors"
	"net"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var getTCPListeners = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

// localExitProcess identifies the actual owner of the configured IPv4 listener.
// We route that executable's traffic into the VPN, without a direct route rule.
func localExitProcess(hop Hop) (string, error) {
	ip := net.ParseIP(hop.Host).To4()
	if hop.Host == "localhost" {
		ip = net.IPv4(127, 0, 0, 1).To4()
	}
	if ip == nil {
		return "", errors.New("use an IPv4 loopback address for the local exit proxy")
	}
	const ownerPIDListener = 3
	const rowSize = 24
	var size uint32
	var table []byte
	for range 4 {
		var ptr uintptr
		if len(table) > 0 {
			ptr = uintptr(unsafe.Pointer(&table[0]))
		}
		code, _, _ := getTCPListeners.Call(ptr, uintptr(unsafe.Pointer(&size)), 0, windows.AF_INET, ownerPIDListener, 0)
		if code == 0 {
			break
		}
		if syscall.Errno(code) != windows.ERROR_INSUFFICIENT_BUFFER || size < 4 || size > 8<<20 {
			return "", errors.New("could not inspect the local proxy listener")
		}
		table = make([]byte, size)
	}
	if len(table) < 4 {
		return "", errors.New("could not inspect the local proxy listener")
	}
	var exact, wildcard uint32
	for i, count := 0, int(binary.LittleEndian.Uint32(table)); i < count && 4+(i+1)*rowSize <= len(table); i++ {
		row := table[4+i*rowSize:]
		if int(binary.BigEndian.Uint16(row[8:10])) != hop.Port {
			continue
		}
		address := net.IP(row[4:8])
		pid := binary.LittleEndian.Uint32(row[20:24])
		if address.Equal(ip) {
			exact = pid
		} else if address.IsUnspecified() {
			wildcard = pid
		}
	}
	pid := exact
	if pid == 0 {
		pid = wildcard
	}
	if pid == 0 {
		return "", errors.New("no IPv4 TCP listener found on the local exit proxy port; start Xray or v2rayN first")
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", errors.New("cannot identify the process listening on the local exit proxy port")
	}
	defer windows.CloseHandle(process)
	path := make([]uint16, 32768)
	length := uint32(len(path))
	if err := windows.QueryFullProcessImageName(process, 0, &path[0], &length); err != nil || length == 0 {
		return "", errors.New("cannot identify the executable listening on the local exit proxy port")
	}
	return windows.UTF16ToString(path[:length]), nil
}
