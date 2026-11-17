.PHONY: web build dev clean

# 构建前端（输出到 internal/webui/dist，供 go:embed 内嵌）
# vite 的 emptyOutDir 会清空 dist，故构建后补回 .gitkeep 占位符（保留空目录供 go:embed）
web:
	cd web && npm install && npm run build
	touch internal/webui/dist/.gitkeep

# 单可执行文件交付：先构建前端再编译 Go（内嵌）
build: web
	go build -o darter ./cmd/server

# 开发：前端热更新（Vite 代理 /api → :8080）
dev-web:
	cd web && npm run dev

# 开发：后端
dev-server:
	go run ./cmd/server

clean:
	rm -f darter
	rm -rf internal/webui/dist/* data
	touch internal/webui/dist/.gitkeep
