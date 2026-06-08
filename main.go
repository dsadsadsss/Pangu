package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const agentVersion = "Pangu2.2.0"

func main() {
	configPath := flag.String("config", "config.yml", "配置文件路径（默认: config.yml）")
	flag.Parse()

	cfg := LoadConfig(*configPath)
	log := newLogger(cfg.EnableLog, cfg.Debug)

	log.Printf("=================================")
	log.Printf("  盘古 Agent  %s", agentVersion)
	log.Printf("=================================")
	log.Printf("配置优先级: 环境变量 > %s > 内置默认值", *configPath)

	if cfg.Server == "" {
		fmt.Fprintln(os.Stderr, "[ERROR] 未配置面板地址！请设置 NEZHA_SERVER 或在 config.yml 中填写 server。")
		os.Exit(1)
	}
	if cfg.ClientSecret == "" {
		log.Printf("[WARN] 未配置 client_secret，连接可能被面板拒绝")
	}

	log.Printf("面板地址: %s  TLS=%v  UUID=%s", cfg.Server, cfg.TLS, cfg.UUID)

	monitor     := NewSystemMonitor(log)
	taskHandler := NewTaskHandler(cfg)
	client      := NewNezhaClient(cfg, monitor, taskHandler, log)

	client.Start()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("收到退出信号，正在停止...")
	client.Stop()
}
