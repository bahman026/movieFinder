//go:build windows

package mux

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps ffmpeg, a console program, from flashing a console window
// over a GUI app built with -H windowsgui.
func hideWindow(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
