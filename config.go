package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// AgentConfig 配置优先级：环境变量 > config.yml > 内置默认值
type AgentConfig struct {
	Server              string
	ClientSecret        string
	UUID                string
	TLS                 bool
	InsecureTLS         bool
	ReportDelay         int
	SkipConnectionCount bool
	SkipProcsCount      bool
	DisableCommandExec  bool
	EnableLog           bool
	Debug               bool
}

func defaultConfig() AgentConfig {
	return AgentConfig{
		ReportDelay: 3,
		EnableLog:   true,
	}
}

// LoadConfig 按优先级加载：内置默认值 → config.yml → 环境变量
func LoadConfig(path string) *AgentConfig {
	cfg := defaultConfig()

	// 1. yaml 文件
	if _, err := os.Stat(path); err == nil {
		loadYAML(path, &cfg)
	} else {
		writeConfigTemplate(path)
	}

	// 2. 环境变量（最高优先级）
	if v := os.Getenv("NEZHA_SERVER"); v != "" {
		cfg.Server = v
	}
	if v := os.Getenv("NEZHA_SECRET"); v != "" {
		cfg.ClientSecret = v
	}
	if v := os.Getenv("NEZHA_UUID"); v != "" {
		cfg.UUID = v
	}
	if v := os.Getenv("NEZHA_TLS"); v != "" {
		cfg.TLS = parseBool(v)
	}
	if v := os.Getenv("NEZHA_INSECURE_TLS"); v != "" {
		cfg.InsecureTLS = parseBool(v)
	}
	if v := os.Getenv("NEZHA_REPORT_DELAY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.ReportDelay = n
		}
	}
	if v := os.Getenv("NEZHA_SKIP_CONNECTION_COUNT"); v != "" {
		cfg.SkipConnectionCount = parseBool(v)
	}
	if v := os.Getenv("NEZHA_SKIP_PROCS_COUNT"); v != "" {
		cfg.SkipProcsCount = parseBool(v)
	}
	if v := os.Getenv("NEZHA_DISABLE_COMMAND_EXECUTE"); v != "" {
		cfg.DisableCommandExec = parseBool(v)
	}
	if v := os.Getenv("NEZHA_ENABLE_LOG"); v != "" {
		cfg.EnableLog = parseBool(v)
	}
	if v := os.Getenv("NEZHA_DEBUG"); v != "" {
		cfg.Debug = parseBool(v)
	}

	// 3. 自动生成 UUID
	if cfg.UUID == "" {
		cfg.UUID = newUUID()
		if _, err := os.Stat(path); err == nil && os.Getenv("NEZHA_UUID") == "" {
			saveUUID(path, cfg.UUID)
		}
	}

	// 4. 合法化 report_delay
	if cfg.ReportDelay < 1 || cfg.ReportDelay > 4 {
		cfg.ReportDelay = 3
	}

	return &cfg
}

// ---- 极简 YAML 解析（只处理 key: value 的平铺结构）----

func loadYAML(path string, cfg *AgentConfig) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// 去掉行内注释
		if ci := strings.Index(val, " #"); ci >= 0 {
			val = strings.TrimSpace(val[:ci])
		}
		// 去掉引号
		val = strings.Trim(val, `"'`)

		switch key {
		case "server":
			cfg.Server = val
		case "client_secret":
			cfg.ClientSecret = val
		case "uuid":
			cfg.UUID = val
		case "tls":
			cfg.TLS = parseBool(val)
		case "insecure_tls":
			cfg.InsecureTLS = parseBool(val)
		case "report_delay":
			if n, err := strconv.Atoi(val); err == nil {
				cfg.ReportDelay = n
			}
		case "skip_connection_count":
			cfg.SkipConnectionCount = parseBool(val)
		case "skip_procs_count":
			cfg.SkipProcsCount = parseBool(val)
		case "disable_command_execute":
			cfg.DisableCommandExec = parseBool(val)
		case "enable_log":
			cfg.EnableLog = parseBool(val)
		case "debug":
			cfg.Debug = parseBool(val)
		}
	}
}

func parseBool(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "1" || s == "true" || s == "yes" || s == "on"
}

func writeConfigTemplate(path string) {
	tmpl := `# 盘古监控 Agent - 配置文件
# 配置优先级: 环境变量 > 本文件 > 内置默认值
#
# 环境变量对照:
#   NEZHA_SERVER                  -> server
#   NEZHA_SECRET                  -> client_secret
#   NEZHA_TLS                     -> tls
#   NEZHA_INSECURE_TLS            -> insecure_tls
#   NEZHA_REPORT_DELAY            -> report_delay
#   NEZHA_SKIP_CONNECTION_COUNT   -> skip_connection_count
#   NEZHA_SKIP_PROCS_COUNT        -> skip_procs_count
#   NEZHA_DISABLE_COMMAND_EXECUTE -> disable_command_execute
#   NEZHA_ENABLE_LOG              -> enable_log
#   NEZHA_DEBUG                   -> debug

server: ""
client_secret: ""
uuid: ""
tls: false
insecure_tls: false
report_delay: 3
skip_connection_count: false
skip_procs_count: false
disable_command_execute: false
enable_log: true
debug: false
`
	_ = os.WriteFile(path, []byte(tmpl), 0644)
}

func saveUUID(path, id string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "uuid:") {
			lines[i] = fmt.Sprintf("uuid: %s", id)
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, fmt.Sprintf("uuid: %s", id))
	}
	_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
}
