# 盘古 Agent · Pangu2.2.0

哪吒监控 Agent 的 Go 精简版，**纯标准库**，无任何外部依赖。

仅为哪吒V1客户端的精简版，仅为适配anaconda.com, 仅为装逼而装逼，都是为了挂针

原版哪吒客户端地址： https://github.com/nezhahq/agent

## 特性

- 零依赖：不使用 grpc-go / protobuf 库，手写 gRPC over HTTP/2 和 protobuf 编解码
- 纯监控模式：CPU / 内存 / 磁盘 / 网络 / 负载 / 连接数 / 进程数
- 支持 HTTP 探测、TCP Ping、ICMP Ping（TCP 模拟）、命令执行
- 配置优先级：**环境变量 > config.yml > 内置默认值**
- 断线自动重连（10s 间隔）

---

## 快速开始

### 方式一：下载预编译二进制

在 [Releases](../../releases) 页面下载对应平台的二进制文件，解压后直接运行。

```bash
# Linux amd64 示例
chmod +x pangu-agent_linux_amd64
NEZHA_SERVER=dash.example.com:443 NEZHA_SECRET=your_secret ./pangu-agent_linux_amd64
```

### 方式二：从源码编译

需要 Go 1.21+，无需安装任何额外工具。

```bash
git clone https://github.com/your-repo/pangu-agent
cd pangu-agent
go build -o pangu-agent .
./pangu-agent
```

---

## 配置

### 配置优先级

```
环境变量  >  config.yml  >  内置默认值
```

首次运行时若 `config.yml` 不存在，会自动在当前目录生成模板。

### 环境变量

| 环境变量 | 对应字段 | 默认值 | 说明 |
|---|---|---|---|
| `NEZHA_SERVER` | server | `` | 面板地址，格式 `host:port` |
| `NEZHA_SECRET` | client_secret | `` | 面板中配置的 Agent 密钥 |
| `NEZHA_UUID` | uuid | 自动生成 | Agent 唯一标识，留空自动生成并写回文件 |
| `NEZHA_TLS` | tls | `false` | 是否启用 TLS（面板 443 端口时设为 true） |
| `NEZHA_INSECURE_TLS` | insecure_tls | `false` | 跳过 TLS 证书验证 |
| `NEZHA_REPORT_DELAY` | report_delay | `3` | 状态上报间隔（秒），范围 1-4 |
| `NEZHA_SKIP_CONNECTION_COUNT` | skip_connection_count | `false` | 跳过 TCP 连接数统计 |
| `NEZHA_SKIP_PROCS_COUNT` | skip_procs_count | `false` | 跳过进程数统计 |
| `NEZHA_DISABLE_COMMAND_EXECUTE` | disable_command_execute | `false` | 禁止面板下发命令执行 |
| `NEZHA_ENABLE_LOG` | enable_log | `true` | 是否输出日志 |
| `NEZHA_DEBUG` | debug | `false` | 输出调试日志 |

布尔值接受：`true` / `false` / `1` / `0` / `yes` / `no` / `on` / `off`

### config.yml 示例

```yaml
# 盘古监控 Agent 配置文件

server: "dash.example.com:443"
client_secret: "your_secret_here"
uuid: ""                  # 留空则自动生成

tls: true                 # 面板使用 443 端口时开启
insecure_tls: false       # 自签证书时开启

report_delay: 3           # 上报间隔（秒），1-4

skip_connection_count: false
skip_procs_count: false
disable_command_execute: false

enable_log: true
debug: false
```

---

## 运行方式

### 直接运行

```bash
# 使用环境变量（最简方式）
NEZHA_SERVER=dash.example.com:443 NEZHA_SECRET=xxx NEZHA_TLS=true ./pangu-agent

# 使用同目录默认配置文件config.yml
./pangu-agent

# 使用别的目录配置文件
./pangu-agent --config /etc/pangu/config.yml

# 查看帮助
./pangu-agent --help
```

### systemd 服务

```ini
# /etc/systemd/system/pangu-agent.service
[Unit]
Description=Pangu Monitor Agent
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/pangu-agent --config /etc/pangu/config.yml
Restart=always
RestartSec=10
Environment=NEZHA_SERVER=dash.example.com:443
Environment=NEZHA_SECRET=your_secret
Environment=NEZHA_TLS=true

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now pangu-agent
sudo systemctl status pangu-agent
```

### Docker

```dockerfile
FROM scratch
COPY pangu-agent_linux_amd64 /pangu-agent
ENTRYPOINT ["/pangu-agent"]
```

```bash
docker run -d \
  -e NEZHA_SERVER=dash.example.com:443 \
  -e NEZHA_SECRET=your_secret \
  -e NEZHA_TLS=true \
  --name pangu-agent \
  your-image
```

---

## 编译指南

### 本地交叉编译

```bash
# Linux amd64
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o pangu-agent_linux_amd64 .

# Linux arm64（树莓派 4 等）
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o pangu-agent_linux_arm64 .

# Linux armv7（树莓派 3 等）
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o pangu-agent_linux_armv7 .

# Linux mips（软路由）
GOOS=linux GOARCH=mips GOMIPS=softfloat CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o pangu-agent_linux_mips .

# Windows amd64
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o pangu-agent_windows_amd64.exe .

# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o pangu-agent_darwin_arm64 .
```

## 常见问题

**Q：连接面板报 `dial timeout`？**
检查 `NEZHA_SERVER` 格式是否正确（`host:port`），以及 `NEZHA_TLS` 是否与面板端口匹配（443 端口通常需要 `TLS=true`）。

**Q：CPU 始终显示 0%？**
第一次采样没有差值，属于正常现象，下一个周期后会显示正确数值。

**Q：磁盘/连接数不准确？**
磁盘读取 `/proc/mounts`，连接数读取 `/proc/net/tcp`，需要有对应读取权限。可尝试 `sudo` 运行，或开启 `skip_connection_count: true` 跳过。

**Q：如何完全静默运行？**
设置 `NEZHA_ENABLE_LOG=false` 或在 config.yml 中设置 `enable_log: false`。
