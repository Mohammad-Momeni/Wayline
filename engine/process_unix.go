//go:build !windows

package main

import (
	"os"
	"os/exec"
)

func configureProcess(c *exec.Cmd)     {}
func configureXrayProcess(c *exec.Cmd) {}
func stopProcess(c *exec.Cmd) {
	if c.Process != nil {
		c.Process.Signal(os.Interrupt)
	}
}
