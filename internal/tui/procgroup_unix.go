//go:build unix

package tui

import (
	"os/exec"
	"syscall"
)

// detach gives the job a process group of its own, so an interrupt reaches the
// docker compose wtm runs and not wtm alone, which would leave compose going.
func detach(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func interrupt(c *exec.Cmd) error { return syscall.Kill(-c.Process.Pid, syscall.SIGINT) }

func kill(c *exec.Cmd) error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
