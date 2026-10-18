//go:build !windows

package merge

import "os/exec"

func hideWindow(*exec.Cmd) {}
