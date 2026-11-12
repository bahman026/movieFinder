//go:build !windows

package mux

import "os/exec"

func hideWindow(*exec.Cmd) {}
