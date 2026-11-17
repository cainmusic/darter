package syntaxcheck

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Check 对改动的文件做基础语法/编译自检，返回问题描述列表（空表示通过）。
// 只做轻量、可离线、不依赖特定项目构建系统的检查；无法识别的语言跳过（交给对抗性审查兜底），不误报。
func Check(repoDir string, files []string) []string {
	var problems []string
	for _, f := range files {
		path := filepath.Join(repoDir, f)
		switch strings.ToLower(filepath.Ext(f)) {
		case ".go":
			// 语法检查逐文件做；编译检查在下面统一跑一次
			problems = append(problems, run(repoDir, "gofmt", "-e", f)...)
		case ".py":
			problems = append(problems, run(repoDir, "python3", "-m", "py_compile", path)...)
		case ".js", ".mjs", ".cjs":
			problems = append(problems, run(repoDir, "node", "--check", path)...)
		case ".ts", ".tsx":
			// tsc 依赖项目 tsconfig，逐文件不可靠，跳过（由审查兜底）
		case ".sh", ".bash":
			problems = append(problems, run(repoDir, "bash", "-n", path)...)
		default:
			// 未知语言：跳过
		}
	}
	if hasExt(files, ".go") {
		// go build ./... 能发现缺 import、undefined、语法错误等语义问题
		problems = append(problems, goBuild(repoDir)...)
	}
	return dedup(problems)
}

func hasExt(files []string, ext string) bool {
	for _, f := range files {
		if strings.EqualFold(filepath.Ext(f), ext) {
			return true
		}
	}
	return false
}

func goBuild(repoDir string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return []string{"go build ./... 超时（180s）"}
	}
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = err.Error()
	}
	return []string{"go build: " + truncate(msg, 3000)}
}

func run(repoDir, name string, args ...string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return []string{name + " 超时（60s）"}
	}
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = err.Error()
	}
	return []string{name + ": " + truncate(msg, 2000)}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…（已截断）"
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
