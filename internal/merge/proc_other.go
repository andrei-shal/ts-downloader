//go:build !windows

package merge

import "os/exec"

func HideWindow(*exec.Cmd) {}
