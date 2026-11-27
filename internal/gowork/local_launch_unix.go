//go:build !windows

package gowork

import (
	"os/exec"
	"syscall"
)

// configureDetachedProcess puts the server in its own process group so signals
// aimed at the TUI (and terminal job control) do not reach it, and so Stop can
// terminate the whole tree with a negative PID.
func configureDetachedProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalProcessTree sends SIGTERM (or SIGKILL when force is set) to the child's
// process group, falling back to the direct process if the group lookup fails.
func signalProcessTree(cmd *exec.Cmd, force bool) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	// Only use the group form when the child really is the group leader.
	// Signalling a guessed group could hit the TUI's own process group.
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil && pgid == cmd.Process.Pid {
		if err := syscall.Kill(-pgid, sig); err == nil {
			return nil
		}
	}
	return cmd.Process.Signal(sig)
}
