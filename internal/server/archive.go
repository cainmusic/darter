package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"darter/internal/job"
)

// archiveCategory 返回任务所属的归档分类（通过/失败/跳过）；非终态返回空串（不在归档里）。
func archiveCategory(t job.Task) string {
	switch t.Status {
	case job.TaskPassed, job.TaskApproved:
		return "passed"
	case job.TaskFailed:
		return "failed"
	case job.TaskSkipped, job.TaskDeferred:
		return "skipped"
	default:
		return ""
	}
}

var archiveGroupNames = map[string]string{
	"passed":  "通过",
	"failed":  "失败",
	"skipped": "跳过",
}

// buildProblemMarkdown 生成「问题描述」markdown：只列每个任务的标题/严重度/涉及文件/问题描述，
// 不含修复结果（提交、审查结论、失败原因等）。group 为空则列出全部任务，否则只列出该分类。
func buildProblemMarkdown(j *job.Job, tasks []job.Task, group string) string {
	title := "问题描述 — " + j.Name
	if group != "" {
		title = "问题描述 — " + j.Name + "（" + archiveGroupNames[group] + "）"
	}

	var list []job.Task
	for _, t := range tasks {
		if group != "" && archiveCategory(t) != group {
			continue
		}
		list = append(list, t)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	if j.ReportSource.Value != "" {
		fmt.Fprintf(&b, "- 报告来源：%s\n", j.ReportSource.Value)
	}
	fmt.Fprintf(&b, "- 基础分支：`%s`\n", j.BaseBranch)
	fmt.Fprintf(&b, "- 生成时间：%s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "\n## 问题清单（%d）\n\n", len(list))
	if len(list) == 0 {
		b.WriteString("（无）\n")
		return b.String()
	}
	for _, t := range list {
		fmt.Fprintf(&b, "### [%s] %s\n", t.ID, t.Title)
		fmt.Fprintf(&b, "- 严重度：%s\n", t.Severity)
		if len(t.Files) > 0 {
			fmt.Fprintf(&b, "- 涉及文件：%s\n", strings.Join(t.Files, ", "))
		}
		if t.Description != "" {
			fmt.Fprintf(&b, "- 问题描述：\n%s\n", t.Description)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// downloadProblems 下载「问题描述」markdown：?group=passed|failed|skipped 只下载对应分类，缺省下载全部。
func (a *App) downloadProblems(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadJob(w, r)
	if !ok {
		return
	}
	tasks, _ := a.store.LoadTasks(j.ID)
	a.mergeAnnotations(j.ID, tasks)
	group := r.URL.Query().Get("group")
	md := buildProblemMarkdown(j, tasks, group)
	filename := "problems.md"
	if group != "" {
		filename = "problems-" + group + ".md"
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(md))
}
