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
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	code := app.RunWithSignals(context.Background(), os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr, interrupts)
	os.Exit(code)
}
