package ai

import (
	"reflect"
	"strings"
	"testing"

	"darter/internal/job"
)

func TestNewPaths(t *testing.T) {
	tests := []struct {
		name string
		diff string
		want []string
	}{
		{
			name: "文本新建文件",
			diff: `diff --git a/foo.go b/foo.go
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/foo.go
@@ -0,0 +1,3 @@
+package foo
+
+func Bar() {}
`,
			want: []string{"foo.go"},
		},
		{
			name: "二进制新建文件（可执行文件）",
			diff: `diff --git a/build/foo b/build/foo
new file mode 100755
index 0000000..e69de29
Binary files /dev/null and b/build/foo differ
`,
			want: []string{"build/foo"},
		},
		{
			name: "修改已有文件（不算新建）",
			diff: `diff --git a/existing.go b/existing.go
index 1234567..abcdef0 100644
--- a/existing.go
+++ b/existing.go
@@ -1,3 +1,4 @@
 package foo
+// new line
`,
			want: nil,
		},
		{
			name: "已有文件改权限（old mode/new mode 不算新建）",
			diff: `diff --git a/script.sh b/script.sh
old mode 100644
new mode 100755
`,
			want: nil,
		},
		{
			name: "混合：文本新文件 + 二进制新文件 + 修改已有文件",
			diff: `diff --git a/a.go b/a.go
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/a.go
@@ -0,0 +1 @@
+package a
diff --git a/build/b b/build/b
new file mode 100755
index 0000000..2222222
Binary files /dev/null and b/build/b differ
diff --git a/existing.go b/existing.go
index 3333333..4444444 100644
--- a/existing.go
+++ b/existing.go
@@ -1 +1,2 @@
+// change
`,
			want: []string{"a.go", "build/b"},
		},
		{
			name: "空 diff",
			diff: "",
			want: nil,
		},
		{
			name: "深层路径的二进制文件",
			diff: `diff --git a/cmd/tools/runner/runner b/cmd/tools/runner/runner
new file mode 100755
index 0000000..e69de29
Binary files /dev/null and b/cmd/tools/runner/runner differ
`,
			want: []string{"cmd/tools/runner/runner"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewPaths(tt.diff)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewPaths() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewFilesBlock(t *testing.T) {
	if got := newFilesBlock(nil); got != "" {
		t.Errorf("newFilesBlock(nil) = %q, want empty", got)
	}
	got := newFilesBlock([]string{"a.go", "build/b"})
	if got == "" {
		t.Fatal("newFilesBlock with files should be non-empty")
	}
	for _, p := range []string{"a.go", "build/b"} {
		if !strings.Contains(got, p) {
			t.Errorf("newFilesBlock output missing %q: %q", p, got)
		}
	}
}

func TestRenderConstraints(t *testing.T) {
	tmpl := "源={{migration.source_branch}} 基底={{migration.target_base}} 目标={{migration.target_branch}} commit={{migration.source_commit}} 标题={{migration.source_commit_subject}} 信息={{migration.source_commit_message}} 冲突={{migration.conflict_files}}"
	vals := MigrationConstraintValues(
		&job.MigrationSpec{SourceBranch: "src", TargetBase: "base", TargetBranch: "dst"},
		&job.CommitEntry{Hash: "abc123", Subject: "subject", Message: "body"},
		[]string{"a.go", "b.go"},
	)
	got := RenderConstraints(tmpl, vals)
	for _, want := range []string{
		"源=src", "基底=base", "目标=dst", "commit=abc123", "标题=subject", "信息=body", "冲突=a.go\nb.go",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderConstraints 缺少 %q：\n%s", want, got)
		}
	}
	if strings.Contains(got, "{{migration.") {
		t.Errorf("替换后不应残留任何 {{migration.*}} 字面量：\n%s", got)
	}

	// 无单一源 commit（全量回归阶段）：commit/冲突文件占位符替换为空串，而非残留字面量。
	got2 := RenderConstraints(tmpl, MigrationConstraintValues(&job.MigrationSpec{SourceBranch: "s", TargetBase: "b", TargetBranch: "d"}, nil, nil))
	if strings.Contains(got2, "{{migration.") {
		t.Errorf("无 commit 时仍残留字面量：\n%s", got2)
	}
	if !strings.Contains(got2, "commit= 标题=") {
		t.Errorf("commit/标题占位符应替换为空串：\n%s", got2)
	}
}

func TestFixerConstraintValues(t *testing.T) {
	got := RenderConstraints("基线={{fixer.base_branch}} 修复={{fixer.fix_branch}}", FixerConstraintValues(&job.Job{BaseBranch: "main", FixBranch: "fix/1"}))
	if got != "基线=main 修复=fix/1" {
		t.Errorf("FixerConstraintValues 替换错误：%q", got)
	}
}
