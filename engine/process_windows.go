package main

import (
	"golang.org/x/sys/windows"
	"os/exec"
	"syscall"
	"time"
)

var childConsoleSignal = make(chan struct{}, 1)
var ignoreChildConsoleSignal = syscall.NewCallback(func(kind uint32) uintptr {
	select {
	case childConsoleSignal <- struct{}{}:
	default:
	}
	return 1
})

func configureProcess(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NEW_CONSOLE}
}

func configureXrayProcess(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
func stopProcess(c *exec.Cmd) {
	if c.Process != nil {
		// The GUI starts us without a console. Attach only to the hidden child
		// console so CTRL_BREAK reaches sing-box and runs its route cleanup.
		k := windows.NewLazySystemDLL("kernel32.dll")
		k.NewProc("FreeConsole").Call()
		attached, _, _ := k.NewProc("AttachConsole").Call(uintptr(c.Process.Pid))
		if attached != 0 {
			select {
			case <-childConsoleSignal:
			default:
			}
			k.NewProc("SetConsoleCtrlHandler").Call(ignoreChildConsoleSignal, 1)
			windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, 0)
			// Console events are asynchronous. Do not detach (which resets the
			// handlers) until our own event has been consumed.
			select {
			case <-childConsoleSignal:
			case <-time.After(time.Second):
			}
			k.NewProc("FreeConsole").Call()
		}
	}
}
