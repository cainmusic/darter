// Package webui 内嵌构建好的前端静态资源，使交付物为单个可执行文件。
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// FS 返回去掉 dist 前缀后的前端文件系统。
func FS() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}
