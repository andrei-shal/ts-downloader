package merge

import (
	"os/exec"
	"syscall"
)

// HideWindow keeps ffmpeg from flashing a console window.
func HideWindow(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
