package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"darter/internal/server"
)

func main() {
	dataDir := flag.String("data-dir", "data", "数据目录")
	port := flag.Int("port", 8080, "监听端口")
	flag.Parse()

	app := server.New(*dataDir)
	addr := fmt.Sprintf(":%d", *port)

	// 优雅退出：收到 SIGINT/SIGTERM 时取消所有在跑任务的 context，
	// 让 exec.CommandContext 一并杀掉 claude 子进程，避免留下孤儿进程。
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("收到信号 %s，正在取消在跑任务并退出...", sig)
		app.Shutdown()
		log.Printf("任务已收尾，进程退出")
		os.Exit(0)
	}()

	log.Printf("darter server listening on http://localhost%s (data: %s)", addr, *dataDir)
	if err := http.ListenAndServe(addr, app.Handler()); err != nil {
		log.Fatal(err)
	}
}
