//go:build windows

package plugin

import (
	"os/exec"
	"strconv"
)

// Windows 没有进程组；用 taskkill /T 杀进程树。
func setProcAttr(cmd *exec.Cmd) {}

func terminateGroup(pid int) { taskkill(pid) }
func killGroup(pid int)      { taskkill(pid) }

func taskkill(pid int) {
	if pid <= 0 {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
