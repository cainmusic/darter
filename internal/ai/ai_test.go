package ai

import (
	"reflect"
	"strings"
	"testing"
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
