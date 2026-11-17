//go:build unix

package ai

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// procStartTime 返回进程启动时间字符串（ps -o lstart=），进程不存在或查询失败时返回空串。
func procStartTime(pid int) string {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// procCommand 返回进程完整命令行（ps -o command=），进程不存在或查询失败时返回空串。
func procCommand(pid int) string {
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// KillOrphan 校验并清理遗留的 claude 子进程。返回是否真正执行了 kill。
// 三重校验防止 PID 复用误杀：PID 仍存活 + 启动时间完全一致 + 命令行包含命令名。
func KillOrphan(pid int, start, cmdName string) bool {
	if pid <= 0 || start == "" {
		return false
	}
	if procStartTime(pid) != start {
		return false // 进程已不存在，或该 PID 已被复用（启动时间不一致）
	}
	if cmdName != "" {
		cmdline := procCommand(pid)
		if cmdline == "" || !strings.Contains(cmdline, filepath.Base(cmdName)) {
			return false
		}
	}
	return syscall.Kill(pid, syscall.SIGKILL) == nil
}
