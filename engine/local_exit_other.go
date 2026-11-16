//go:build !windows && (!linux || android)

package main

import "errors"

func localExitProcess(Hop) (string, error) {
	return "", errors.New("a local exit proxy requires desktop VPN mode")
}
