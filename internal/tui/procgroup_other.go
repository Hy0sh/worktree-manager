//go:build !unix

package tui

import "os/exec"

func detach(*exec.Cmd) {}

func interrupt(c *exec.Cmd) error { return c.Process.Kill() }

func kill(c *exec.Cmd) error { return c.Process.Kill() }
