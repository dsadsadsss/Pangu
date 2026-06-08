package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const reconnectDelay = 10 * time.Second

// ---- gRPC method paths ----
const (
	methodReportSystemState  = "/proto.NezhaService/ReportSystemState"
	methodReportSystemInfo2  = "/proto.NezhaService/ReportSystemInfo2"
	methodReportGeoIP        = "/proto.NezhaService/ReportGeoIP"
	methodRequestTask        = "/proto.NezhaService/RequestTask"
)

type NezhaClient struct {
	cfg         *AgentConfig
	monitor     *SystemMonitor
	taskHandler *TaskHandler
	log         logger

	stopCh chan struct{}
}

func NewNezhaClient(cfg *AgentConfig, mon *SystemMonitor, th *TaskHandler, l logger) *NezhaClient {
	return &NezhaClient{
		cfg:         cfg,
		monitor:     mon,
		taskHandler: th,
		log:         l,
		stopCh:      make(chan struct{}),
	}
}

func (c *NezhaClient) Start() {
	go c.connectLoop()
}

func (c *NezhaClient) Stop() {
	close(c.stopCh)
	c.log.Printf("NezhaClient 已停止")
}

// ---- 连接循环 ----

func (c *NezhaClient) connectLoop() {
	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		if err := c.connect(); err != nil {
			c.log.Printf("[WARN] 连接异常: %v", err)
		}

		select {
		case <-c.stopCh:
			return
		case <-time.After(reconnectDelay):
			c.log.Printf("重新连接到面板...")
		}
	}
}

func (c *NezhaClient) connect() error {
	server := c.cfg.Server
	c.log.Printf("正在连接到面板: %s", server)

	target := parseServer(server)
	meta := map[string]string{
		"client_secret": c.cfg.ClientSecret,
		"client_uuid":   c.cfg.UUID,
	}

	conn := newGRPCConn(target, c.cfg.TLS, c.cfg.InsecureTLS, meta)
	defer conn.Close()

	ctx, cancelAll := context.WithCancel(context.Background())
	defer cancelAll()

	// 1. 上报主机静态信息（unary）
	c.log.Printf("上报主机信息...")
	hostMsg := c.monitor.BuildHost(agentVersion)
	receipt := &Uint64Receipt{}
	if err := conn.Invoke(ctx, methodReportSystemInfo2, hostMsg.Marshal(), receipt); err != nil {
		return fmt.Errorf("ReportSystemInfo2: %w", err)
	}
	c.log.Printf("主机信息上报成功, boot_time=%d", receipt.Data)

	// 2. 上报 GeoIP（async，失败不影响主流程）
	go func() {
		ipv4 := c.monitor.GetPublicIPv4()
		geoReq := &GeoIP{Ip: &IP{Ipv4: ipv4, Ipv6: ""}}
		geoResp := &GeoIP{}
		if err := conn.Invoke(ctx, methodReportGeoIP, geoReq.Marshal(), geoResp); err != nil {
			c.log.Printf("[WARN] GeoIP 上报失败: %v", err)
			return
		}
		ipv4Result := ""
		if geoResp.Ip != nil {
			ipv4Result = geoResp.Ip.Ipv4
		}
		c.log.Printf("GeoIP 上报成功: ipv4=%s country=%s",
			ipv4Result, geoResp.CountryCode)
	}()

	// 3. 并发启动任务流 + 状态上报流
	sessionDone := make(chan struct{})
	var once sync.Once
	endSession := func() { once.Do(func() { close(sessionDone) }) }

	go func() {
		defer endSession()
		if err := c.taskStream(ctx, conn); err != nil {
			c.log.Printf("[WARN] 任务流退出: %v", err)
		}
	}()

	go func() {
		defer endSession()
		if err := c.stateStream(ctx, conn); err != nil {
			c.log.Printf("[WARN] 状态流退出: %v", err)
		}
	}()

	select {
	case <-sessionDone:
		c.log.Printf("会话断开，准备重连")
	case <-c.stopCh:
	}
	return nil
}

// ---- 任务流（双向流：接收 Task，发送 TaskResult）----

func (c *NezhaClient) taskStream(ctx context.Context, conn *GRPCConn) error {
	stream, err := conn.NewBidiStream(ctx, methodRequestTask)
	if err != nil {
		return err
	}
	defer stream.Close()

	sendCh := make(chan *TaskResult, 32)

	// 发送 goroutine
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case r, ok := <-sendCh:
				if !ok {
					return
				}
				if err := stream.Send(r.Marshal()); err != nil {
					c.log.Printf("[WARN] 发送任务结果失败: %v", err)
					return
				}
			}
		}
	}()

	// 接收任务
	for {
		payload, err := stream.Recv()
		if err != nil {
			return err
		}
		task := &Task{}
		if err := task.Unmarshal(payload); err != nil {
			c.log.Printf("[WARN] 解析 Task 失败: %v", err)
			continue
		}
		go func(t *Task) {
			result := c.taskHandler.Handle(t)
			if result == nil {
				return
			}
			select {
			case sendCh <- result:
			case <-ctx.Done():
			}
		}(task)
	}
}

// ---- 状态上报流（client streaming）----

func (c *NezhaClient) stateStream(ctx context.Context, conn *GRPCConn) error {
	stream, err := conn.NewClientStream(ctx, methodReportSystemState)
	if err != nil {
		return err
	}
	defer stream.Close()

	// 丢弃服务端 Receipt（后台读，防止服务端 flow control 阻塞）
	go func() {
		for {
			if _, err := stream.Recv(); err != nil {
				return
			}
		}
	}()

	delay  := time.Duration(c.cfg.ReportDelay) * time.Second
	ticker := time.NewTicker(delay)
	defer ticker.Stop()

	// 与 Python 版一致：先立即发第一帧，再按 delay 周期发送
	sendState := func() error {
		state := c.monitor.BuildState(
			c.cfg.SkipConnectionCount,
			c.cfg.SkipProcsCount,
		)
		if err := stream.Send(state.Marshal()); err != nil {
			return err
		}
		if c.cfg.Debug {
			c.log.Printf("[DEBUG] 状态已上报 CPU=%.1f%%", state.Cpu)
		}
		return nil
	}

	// 立即发第一帧（对应 Python 的先 yield 再 wait）
	if err := sendState(); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := sendState(); err != nil {
				return err
			}
		}
	}
}

// ---- 工具 ----

func parseServer(server string) string {
	// 已经是 host:port 格式则直接返回
	if server == "" {
		return "localhost:5555"
	}
	// IPv6: [::1]:5555 → 保持不变
	if len(server) > 0 && server[0] == '[' {
		return server
	}
	// 检查是否含端口
	lastColon := -1
	for i := len(server) - 1; i >= 0; i-- {
		if server[i] == ':' {
			lastColon = i
			break
		}
	}
	if lastColon < 0 {
		return server + ":5555"
	}
	return server
}
