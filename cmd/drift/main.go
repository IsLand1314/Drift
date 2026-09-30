package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/IsLand1314/Drift/internal/app"
)

// main 只负责把操作系统事件和标准输入输出接到应用层。
// 具体的参数解析、配置加载、模型请求和 Agent Loop 都在 internal/app 中完成。
func main() {
	// Ctrl+C 会取消传给模型请求的 context，最终由 app.Run 转换为退出码 130。
	// 创建一个 Context, 并监听 Ctrl+C(os.Interrupt)。收到信号时,这个 ctx 会被自动 cancel
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := app.Run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
