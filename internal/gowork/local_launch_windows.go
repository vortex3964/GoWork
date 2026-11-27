//go:build windows

package gowork

import (
	"os/exec"
	"strconv"
	"syscall"
)

const (
	// CREATE_NEW_PROCESS_GROUP detaches the child from Ctrl+C delivered to
	// the TUI's console; CREATE_NO_WINDOW keeps it from opening its own.
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

func configureDetachedProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | createNoWindow,
	}
}

// signalProcessTree uses taskkill /T so grandchildren spawned by the server are
// terminated too. /F is only added on the forced escalation; the graceful
// attempt lets well-behaved console servers shut down first.
func signalProcessTree(cmd *exec.Cmd, force bool) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	args := []string{"/PID", strconv.Itoa(cmd.Process.Pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	kill := exec.Command("taskkill", args...)
	if err := kill.Run(); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
