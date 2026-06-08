package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var pseudoFS = []string{"/sys", "/proc", "/dev", "/run"}

type SystemMonitor struct {
	logger logger

	mu             sync.Mutex
	netInTransfer  uint64
	netOutTransfer uint64
	lastNetIn      uint64
	lastNetOut     uint64
	lastNetMs      int64
	netInSpeed     uint64
	netOutSpeed    uint64

	prevCPUStat cpuStat
	prevCPUTime time.Time
}

func NewSystemMonitor(l logger) *SystemMonitor {
	return &SystemMonitor{logger: l}
}

// ---- Host 静态信息 ----

func (m *SystemMonitor) BuildHost(version string) *Host {
	h := &Host{Version: version}
	h.Arch = runtime.GOARCH

	// OS
	h.Platform, h.PlatformVersion = readOSRelease()

	// boot time
	h.BootTime = uint64(readBootTime())

	// virtualization
	h.Virtualization = detectVirt()

	// CPU model
	cpuModel, physCores := readCPUInfo()
	if cpuModel == "" {
		cpuModel = "Unknown CPU"
	}
	if physCores <= 0 {
		physCores = runtime.NumCPU()
	}
	h.Cpu = []string{fmt.Sprintf("%s %d Physical Core", cpuModel, physCores)}

	// memory
	h.MemTotal, h.SwapTotal = readMemInfo()

	// disk total
	h.DiskTotal = getDiskTotal()

	return h
}

// ---- 实时状态 ----

func (m *SystemMonitor) BuildState(skipConns, skipProcs bool) *State {
	s := &State{}

	s.Cpu = m.getCPUPercent()

	memUsed, swapUsed := readMemUsed()
	s.MemUsed  = memUsed
	s.SwapUsed = swapUsed

	s.DiskUsed = getDiskUsed()

	m.updateNetwork()
	s.NetInTransfer  = m.netInTransfer
	s.NetOutTransfer = m.netOutTransfer
	s.NetInSpeed     = m.netInSpeed
	s.NetOutSpeed    = m.netOutSpeed

	s.Uptime = uint64(time.Now().Unix() - readBootTime())

	s.Load1, s.Load5, s.Load15 = readLoadAvg()

	if !skipConns {
		s.TcpConnCount = countTCPEstablished()
	}
	if !skipProcs {
		s.ProcessCount = countProcesses()
	}

	return s
}

// ---- 公网 IP ----

func (m *SystemMonitor) GetPublicIPv4() string {
	services := []string{
		"https://api4.ipify.org",
		"https://ipv4.icanhazip.com",
		"https://api4.my-ip.io/ip",
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, url := range services {
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("User-Agent", "pangu-agent/"+agentVersion)
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 {
			continue
		}
		ip := strings.TrimSpace(string(body))
		if ip != "" {
			m.logger.Printf("公网 IPv4: %s (via %s)", ip, url)
			return ip
		}
	}
	m.logger.Printf("[WARN] 无法获取公网 IPv4")
	return ""
}

// ============================================================
// /proc 读取工具（Linux）
// ============================================================

// ---- CPU ----

type cpuStat struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (c cpuStat) total() uint64 {
	return c.user + c.nice + c.system + c.idle + c.iowait + c.irq + c.softirq + c.steal
}

func readCPUStat() (cpuStat, bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuStat{}, false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			return cpuStat{}, false
		}
		parse := func(i int) uint64 {
			v, _ := strconv.ParseUint(fields[i], 10, 64)
			return v
		}
		return cpuStat{
			user:    parse(1),
			nice:    parse(2),
			system:  parse(3),
			idle:    parse(4),
			iowait:  parse(5),
			irq:     parse(6),
			softirq: parse(7),
			steal:   parse(8),
		}, true
	}
	return cpuStat{}, false
}

func (m *SystemMonitor) getCPUPercent() float64 {
	cur, ok := readCPUStat()
	if !ok {
		return 0
	}
	now := time.Now()

	if m.prevCPUTime.IsZero() {
		m.prevCPUStat = cur
		m.prevCPUTime = now
		return 0
	}

	prev := m.prevCPUStat
	m.prevCPUStat = cur
	m.prevCPUTime = now

	totalDelta := float64(cur.total() - prev.total())
	idleDelta  := float64(cur.idle - prev.idle)
	if totalDelta <= 0 {
		return 0
	}
	pct := (1.0 - idleDelta/totalDelta) * 100.0
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

func readCPUInfo() (model string, physCores int) {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "", runtime.NumCPU()
	}
	defer f.Close()

	coreIDs := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if model == "" && strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				model = strings.TrimSpace(parts[1])
			}
		}
		if strings.HasPrefix(line, "core id") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				coreIDs[strings.TrimSpace(parts[1])] = true
			}
		}
	}
	physCores = len(coreIDs)
	if physCores == 0 {
		physCores = runtime.NumCPU()
	}
	return model, physCores
}

// ---- Memory ----

func readMemInfo() (memTotal, swapTotal uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		val, _ := strconv.ParseUint(fields[1], 10, 64)
		val *= 1024 // kB → bytes
		switch fields[0] {
		case "MemTotal:":
			memTotal = val
		case "SwapTotal:":
			swapTotal = val
		}
		if memTotal > 0 && swapTotal > 0 {
			break
		}
	}
	return
}

func readMemUsed() (memUsed, swapUsed uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	vals := map[string]uint64{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(fields[1], 10, 64)
		vals[fields[0]] = v * 1024
	}
	memTotal := vals["MemTotal:"]
	memFree  := vals["MemFree:"]
	buffers  := vals["Buffers:"]
	cached   := vals["Cached:"]
	memUsed  = memTotal - memFree - buffers - cached

	swapTotal := vals["SwapTotal:"]
	swapFree  := vals["SwapFree:"]
	swapUsed  = swapTotal - swapFree
	return
}

// ---- Load average ----

func readLoadAvg() (l1, l5, l15 float64) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0
	}
	l1, _  = strconv.ParseFloat(fields[0], 64)
	l5, _  = strconv.ParseFloat(fields[1], 64)
	l15, _ = strconv.ParseFloat(fields[2], 64)
	return
}

// ---- Boot time ----

func readBootTime() int64 {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return time.Now().Unix() - 60
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "btime ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				v, _ := strconv.ParseInt(fields[1], 10, 64)
				return v
			}
		}
	}
	return time.Now().Unix() - 60
}

// ---- OS release ----

func readOSRelease() (platform, version string) {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return runtime.GOOS, ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "ID=") {
			platform = strings.Trim(line[3:], `"`)
		}
		if strings.HasPrefix(line, "VERSION_ID=") {
			version = strings.Trim(line[11:], `"`)
		}
	}
	if platform == "" {
		platform = runtime.GOOS
	}
	return
}

// ---- Disk ----

func getDiskTotal() uint64 {
	return sumDisk(false)
}

func getDiskUsed() uint64 {
	return sumDisk(true)
}

func sumDisk(usedOnly bool) uint64 {
	// 读 /proc/mounts
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return 0
	}
	defer f.Close()

	var total uint64
	seen := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		mount := fields[1]
		if seen[mount] || isPseudoFS(mount) {
			continue
		}
		seen[mount] = true
		t, u, err := diskUsage(mount)
		if err != nil || t == 0 {
			continue
		}
		if usedOnly {
			total += u
		} else {
			total += t
		}
	}
	return total
}

func isPseudoFS(mount string) bool {
	for _, p := range pseudoFS {
		if mount == p || strings.HasPrefix(mount, p+"/") {
			return true
		}
	}
	return false
}

// ---- Network ----

type netStat struct {
	bytesRecv uint64
	bytesSent uint64
}

func readNetStat() netStat {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return netStat{}
	}
	defer f.Close()

	var recv, sent uint64
	scanner := bufio.NewScanner(f)
	// skip 2 header lines
	scanner.Scan()
	scanner.Scan()
	for scanner.Scan() {
		line := scanner.Text()
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:idx])
		// skip loopback
		if iface == "lo" {
			continue
		}
		fields := strings.Fields(line[idx+1:])
		if len(fields) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(fields[0], 10, 64)
		s, _ := strconv.ParseUint(fields[8], 10, 64)
		recv += r
		sent += s
	}
	return netStat{bytesRecv: recv, bytesSent: sent}
}

func (m *SystemMonitor) updateNetwork() {
	m.mu.Lock()
	defer m.mu.Unlock()

	nowMs   := time.Now().UnixMilli()
	elapsed := nowMs - m.lastNetMs
	if elapsed < 500 {
		return
	}

	stat := readNetStat()
	curIn  := stat.bytesRecv
	curOut := stat.bytesSent

	if m.lastNetMs > 0 && elapsed > 0 {
		di := safeSubU64(curIn,  m.lastNetIn)
		do := safeSubU64(curOut, m.lastNetOut)
		m.netInTransfer  += di
		m.netOutTransfer += do
		m.netInSpeed  = di * 1000 / uint64(elapsed)
		m.netOutSpeed = do * 1000 / uint64(elapsed)
	}
	m.lastNetIn  = curIn
	m.lastNetOut = curOut
	m.lastNetMs  = nowMs
}

// ---- Connections ----

func countTCPEstablished() uint64 {
	// /proc/net/tcp + /proc/net/tcp6
	var count uint64
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Scan() // skip header
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 {
				continue
			}
			// state field: 01 = ESTABLISHED
			if fields[3] == "01" {
				count++
			}
		}
		f.Close()
	}
	return count
}

// ---- Processes ----

func countProcesses() uint64 {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	var count uint64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// only numeric directories are process IDs
		allDigit := true
		for _, c := range e.Name() {
			if c < '0' || c > '9' {
				allDigit = false
				break
			}
		}
		if allDigit {
			count++
		}
	}
	return count
}

// ---- Virtualization ----

func detectVirt() string {
	data, err := os.ReadFile("/sys/class/dmi/id/product_name")
	if err != nil {
		return ""
	}
	name := strings.ToLower(strings.TrimSpace(string(data)))
	for _, pair := range [][2]string{
		{"vmware", "vmware"},
		{"virtualbox", "virtualbox"},
		{"kvm", "kvm"},
		{"qemu", "kvm"},
		{"xen", "xen"},
		{"hyperv", "hyperv"},
		{"bochs", "bochs"},
	} {
		if strings.Contains(name, pair[0]) {
			return pair[1]
		}
	}
	return ""
}

// ---- Helpers ----

func safeSubU64(a, b uint64) uint64 {
	if a >= b {
		return a - b
	}
	return 0
}
