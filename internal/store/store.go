package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"darter/internal/events"
	"darter/internal/job"
)

// Store 用 JSON 文件持久化所有数据。单进程 + 单写锁保证安全。
type Store struct {
	base string
	mu   sync.Mutex
}

func New(base string) *Store { return &Store{base: base} }

func (s *Store) jobDir(id string) string { return filepath.Join(s.base, "jobs", id) }

// SaveJob 原子写入 job.json；同时把报告内容落到 report.md（若有）。
func (s *Store) SaveJob(j *job.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.jobDir(j.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "job.json"), j); err != nil {
		return err
	}
	if j.ReportSource.Value != "" {
		if err := writeText(filepath.Join(dir, "report.md"), j.ReportSource.Value); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) LoadJob(id string) (*job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadJob(id)
}

func (s *Store) loadJob(id string) (*job.Job, error) {
	var j job.Job
	if err := readJSON(filepath.Join(s.jobDir(id), "job.json"), &j); err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *Store) ListJobs() ([]*job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root := filepath.Join(s.base, "jobs")
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []*job.Job{}, nil
		}
		return nil, err
	}
	jobs := make([]*job.Job, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		j, err := s.loadJob(e.Name())
		if err != nil {
			continue
		}
		jobs = append(jobs, j)
	}
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].CreatedAt.After(jobs[b].CreatedAt) })
	return jobs, nil
}

func (s *Store) SaveTasks(jobID string, tasks []job.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(filepath.Join(s.jobDir(jobID), "tasks.json"), tasks)
}

func (s *Store) LoadTasks(jobID string) ([]job.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var tasks []job.Task
	if err := readJSON(filepath.Join(s.jobDir(jobID), "tasks.json"), &tasks); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []job.Task{}, nil
		}
		return nil, err
	}
	return tasks, nil
}

func (s *Store) SavePatch(jobID, taskID, diff string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.jobDir(jobID), "patches")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeText(filepath.Join(dir, taskID+".diff"), diff)
}

func (s *Store) LoadPatch(jobID, taskID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(filepath.Join(s.jobDir(jobID), "patches", taskID+".diff"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *Store) AppendEvent(jobID string, ev events.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.jobDir(jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// LoadEvents 读取某 job 的历史事件（按写入顺序），供前端进页面时回放日志。
// 崩溃可能残留半行，损坏行直接跳过。
func (s *Store) LoadEvents(jobID string) ([]events.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(filepath.Join(s.jobDir(jobID), "events.jsonl"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []events.Event{}, nil
		}
		return nil, err
	}
	lines := bytes.Split(b, []byte{'\n'})
	out := make([]events.Event, 0, len(lines))
	for _, ln := range lines {
		if len(bytes.TrimSpace(ln)) == 0 {
			continue
		}
		var ev events.Event
		if err := json.Unmarshal(ln, &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

type Settings struct {
	ClaudeCmd      string `json:"claude_cmd"`
	ClaudeModel    string `json:"claude_model"`
	DefaultRetries int    `json:"default_retries"`
}

func (s *Store) LoadSettings() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st Settings
	if err := readJSON(filepath.Join(s.base, "settings.json"), &st); err != nil {
		return Settings{ClaudeCmd: "claude", DefaultRetries: 3}, nil
	}
	if st.ClaudeCmd == "" {
		st.ClaudeCmd = "claude"
	}
	if st.DefaultRetries <= 0 {
		st.DefaultRetries = 3
	}
	return st, nil
}

func (s *Store) SaveSettings(st Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(filepath.Join(s.base, "settings.json"), st)
}

// ConstraintModule 约束模板的一个「段」：段名 + 详情。模板由若干段有序组成，
// 生效时按数组顺序把各段详情拼成一份约束文本。
type ConstraintModule struct {
	ID      string `json:"id"`
	Name    string `json:"name"`    // 段名（目录/索引里显示的标题），可为空
	Content string `json:"content"` // 详情文本
}

// ConstraintTemplate 项目约束模板：预存一段项目约束文本，新建修复任务时可一键套用。
// 采用「两层」结构：第一层是模板本身，第二层是有序的模块列表（分段），
// 使用时把各模块详情按顺序拼接成最终约束文本。
type ConstraintTemplate struct {
	ID      string             `json:"id"`
	Name    string             `json:"name"`
	Modules []ConstraintModule `json:"modules"`
	// Content 遗留字段：旧数据是单段文本。读取旧数据时若 Modules 为空而 Content 非空，自动包装成单模块。
	// 新数据只写 Modules，此字段不再落盘（omitempty）。
	Content string `json:"content,omitempty"`
}

// normalize 兼容旧数据并保证模块 ID 存在：旧模板的整段 content 包装成单模块；空 ID 的模块补一个稳定 ID。
func normalizeTemplate(t *ConstraintTemplate) {
	if len(t.Modules) == 0 && strings.TrimSpace(t.Content) != "" {
		t.Modules = []ConstraintModule{{ID: "m0", Name: "内容", Content: t.Content}}
		t.Content = ""
	}
	for i := range t.Modules {
		if t.Modules[i].ID == "" {
			t.Modules[i].ID = fmt.Sprintf("m%d", i)
		}
	}
}

// Concat 把各模块详情按顺序拼成一份约束文本（段间用空行分隔）。
func (t *ConstraintTemplate) Concat() string {
	parts := make([]string, 0, len(t.Modules))
	for _, m := range t.Modules {
		if c := strings.TrimSpace(m.Content); c != "" {
			parts = append(parts, c)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (s *Store) constraintTemplatesPath() string {
	return filepath.Join(s.base, "constraint-templates.json")
}

func (s *Store) LoadConstraintTemplates() ([]ConstraintTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.constraintTemplatesPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []ConstraintTemplate{}, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return []ConstraintTemplate{}, nil
	}
	var ts []ConstraintTemplate
	if err := json.Unmarshal(b, &ts); err != nil {
		return nil, err
	}
	for i := range ts {
		normalizeTemplate(&ts[i])
	}
	return ts, nil
}

func (s *Store) SaveConstraintTemplates(ts []ConstraintTemplate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ts == nil {
		ts = []ConstraintTemplate{}
	}
	for i := range ts {
		normalizeTemplate(&ts[i])
	}
	return writeJSON(s.constraintTemplatesPath(), ts)
}

// ResolveConstraint 返回 job 生效的约束文本：有模板引用则读模板当前内容（live，分段拼接），
// 否则退回内联文本。读模板失败或模板已被删除时同样退回内联，保证不因悬空引用而丢约束。
func (s *Store) ResolveConstraint(templateID, inline string) string {
	if templateID == "" {
		return inline
	}
	ts, err := s.LoadConstraintTemplates()
	if err != nil {
		return inline
	}
	for _, t := range ts {
		if t.ID == templateID {
			return t.Concat()
		}
	}
	return inline
}

// Annotation 任务的人工备注/建议：与任务处理状态分离存储，不参与修复循环，也不会被循环覆盖。
type Annotation struct {
	TaskID     string `json:"task_id"`
	Note       string `json:"note,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

func (s *Store) annotationsPath(jobID string) string {
	return filepath.Join(s.jobDir(jobID), "annotations.json")
}

func (s *Store) LoadAnnotations(jobID string) ([]Annotation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.annotationsPath(jobID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Annotation{}, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return []Annotation{}, nil
	}
	var anns []Annotation
	if err := json.Unmarshal(b, &anns); err != nil {
		return nil, err
	}
	return anns, nil
}

func (s *Store) SaveAnnotations(jobID string, anns []Annotation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if anns == nil {
		anns = []Annotation{}
	}
	return writeJSON(s.annotationsPath(jobID), anns)
}

// Attempt 记录一次修复尝试的 diff 与对抗性审查结论。
// 审查被驳回时 diff 会被丢弃（工作区回滚），这里持久化下来，供重试反馈与后续分析复用。
type Attempt struct {
	TaskID  string     `json:"task_id"`
	Attempt int        `json:"attempt"`
	Review  job.Review `json:"review"`
	Diff    string     `json:"diff"`
	Error   string     `json:"error,omitempty"` // 失败原因（修复/自检/审查/提交等错误）；审查驳回类时 Review/Diff 有值，其余错误时可能为空
}

func (s *Store) attemptsPath(jobID string) string {
	return filepath.Join(s.jobDir(jobID), "attempts.json")
}

func (s *Store) LoadAttempts(jobID string) ([]Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.attemptsPath(jobID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Attempt{}, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return []Attempt{}, nil
	}
	var as []Attempt
	if err := json.Unmarshal(b, &as); err != nil {
		return nil, err
	}
	return as, nil
}

func (s *Store) SaveAttempts(jobID string, as []Attempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if as == nil {
		as = []Attempt{}
	}
	return writeJSON(s.attemptsPath(jobID), as)
}

// AiReview 记录一次人工发起的 AI 审查：人类附加提示词 + AI 审查结论。
// 不限次数，每次发起都追加一条记录，供页面回溯每一次的审查需求与 AI 响应。
type AiReview struct {
	ID        string     `json:"id"`
	TaskID    string     `json:"task_id"`
	Prompt    string     `json:"prompt,omitempty"` // 人类附加提示词（审查方向/疑问），可为空（仅用默认审查标准）
	Review    job.Review `json:"review"`           // AI 审查结论（verdict/summary/findings）
	Error     string     `json:"error,omitempty"`  // 调用失败原因
	CreatedAt time.Time  `json:"created_at"`
}

func (s *Store) aiReviewsPath(jobID string) string {
	return filepath.Join(s.jobDir(jobID), "ai-reviews.json")
}

// LoadAiReviews 返回某 job 的全部 AI 审查记录（跨任务）。
func (s *Store) LoadAiReviews(jobID string) ([]AiReview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.aiReviewsPath(jobID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []AiReview{}, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return []AiReview{}, nil
	}
	var rs []AiReview
	if err := json.Unmarshal(b, &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

// AppendAiReview 追加一条审查记录并返回追加后的全量列表。
// 单锁内 read-modify-write，避免并发发起时丢记录。
func (s *Store) AppendAiReview(jobID string, r AiReview) ([]AiReview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []AiReview
	if b, err := os.ReadFile(s.aiReviewsPath(jobID)); err == nil && len(b) > 0 {
		if err := json.Unmarshal(b, &all); err != nil {
			return nil, err
		}
	}
	all = append(all, r)
	if err := writeJSON(s.aiReviewsPath(jobID), all); err != nil {
		return nil, err
	}
	return all, nil
}

// Child 记录一个正在运行的 claude 子进程，用于崩溃恢复时清理孤儿进程，
// 并供前端跟踪「后台 AI 正在做什么」（阶段/任务/启动时间）。
type Child struct {
	PID       int       `json:"pid"`
	Start     string    `json:"start"` // ps -o lstart= 输出，唯一标识进程实例
	Cmd       string    `json:"cmd"`   // 启动命令名（如 "claude"），用于命令行匹配
	JobID     string    `json:"job_id"`
	TaskID    string    `json:"task_id,omitempty"`    // 所属任务 ID（拆解/质检等 job 级阶段为空）
	Phase     string    `json:"phase,omitempty"`      // 阶段描述（如"修复任务"/"对抗审查"）
	StartedAt time.Time `json:"started_at,omitempty"` // 进程启动的墙上时钟，用于计算已运行时长
}

func (s *Store) childrenPath() string { return filepath.Join(s.base, "children.json") }

// SaveChild 记录（或按 PID 覆盖）一个子进程。
func (s *Store) SaveChild(c Child) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	children, err := s.listChildrenLocked()
	if err != nil {
		return err
	}
	for i := range children {
		if children[i].PID == c.PID {
			children[i] = c
			return writeJSON(s.childrenPath(), children)
		}
	}
	children = append(children, c)
	return writeJSON(s.childrenPath(), children)
}

// DeleteChild 移除某 PID 的记录。
func (s *Store) DeleteChild(pid int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	children, err := s.listChildrenLocked()
	if err != nil {
		return err
	}
	out := make([]Child, 0, len(children))
	for _, c := range children {
		if c.PID != pid {
			out = append(out, c)
		}
	}
	return writeJSON(s.childrenPath(), out)
}

// ListChildren 返回所有已记录的子进程。
func (s *Store) ListChildren() ([]Child, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listChildrenLocked()
}

func (s *Store) listChildrenLocked() ([]Child, error) {
	b, err := os.ReadFile(s.childrenPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Child{}, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return []Child{}, nil
	}
	var children []Child
	if err := json.Unmarshal(b, &children); err != nil {
		return nil, err
	}
	return children, nil
}

// ---- atomic helpers ----

func writeJSON(path string, v interface{}) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, b)
}

func writeText(path string, s string) error {
	return atomicWrite(path, []byte(s))
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, v interface{}) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return fmt.Errorf("%s: empty file", path)
	}
	return json.Unmarshal(b, v)
}

// ---- 代码迁移（kind=migration）----

func (s *Store) migrationsPath(jobID string) string {
	return filepath.Join(s.jobDir(jobID), "migrations.json")
}

// SaveMigrations 持久化源分支 commit 迁移条目（含状态与 source→target 映射）。
func (s *Store) SaveMigrations(jobID string, commits []job.CommitEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(s.migrationsPath(jobID), commits)
}

func (s *Store) LoadMigrations(jobID string) ([]job.CommitEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var commits []job.CommitEntry
	if err := readJSON(s.migrationsPath(jobID), &commits); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []job.CommitEntry{}, nil
		}
		return nil, err
	}
	return commits, nil
}

func (s *Store) conflictsPath(jobID string) string {
	return filepath.Join(s.jobDir(jobID), "conflicts.json")
}

// SaveConflicts 持久化冲突记录（可变：解决后回填 handler/resolution）。
func (s *Store) SaveConflicts(jobID string, cs []job.ConflictRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(s.conflictsPath(jobID), cs)
}

func (s *Store) LoadConflicts(jobID string) ([]job.ConflictRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var cs []job.ConflictRecord
	if err := readJSON(s.conflictsPath(jobID), &cs); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []job.ConflictRecord{}, nil
		}
		return nil, err
	}
	return cs, nil
}
