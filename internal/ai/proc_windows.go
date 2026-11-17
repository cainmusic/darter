//go:build windows

package ai

// Windows 上不实现进程启动时间/命令行校验（依赖的 ps 命令不存在），
// 孤儿进程清理退化为 no-op：本地 claude CLI 主要跑在 macOS/Linux，Windows 仅保证可交叉编译。

func procStartTime(pid int) string { return "" }

func procCommand(pid int) string { return "" }

func KillOrphan(pid int, start, cmdName string) bool { return false }
