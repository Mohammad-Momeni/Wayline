package main

import (
	"net"
	"os"
	"strings"
	"testing"
)

func TestLocalExitDetectsRealListener(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	want, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got, err := localExitProcess(Hop{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port})
	if err != nil || !strings.EqualFold(got, want) {
		t.Fatalf("listener owner = %q, want %q: %v", got, want, err)
	}
}
