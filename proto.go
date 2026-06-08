package main

// ============================================================
// 手写 protobuf wire format 编解码（纯标准库，无任何外部依赖）
//
// Wire types:
//   0 = varint
//   1 = 64-bit
//   2 = length-delimited (string, bytes, embedded message, repeated)
//   5 = 32-bit
// ============================================================

import (
	"encoding/binary"
	"fmt"
	"math"
)

// ---- varint ----

func encodeVarint(v uint64) []byte {
	buf := make([]byte, 10)
	n := binary.PutUvarint(buf, v)
	return buf[:n]
}

func decodeVarint(b []byte) (uint64, int) {
	v, n := binary.Uvarint(b)
	return v, n
}

// ---- field tag: (fieldNum << 3) | wireType ----

const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

func fieldTag(num, wtype uint64) []byte {
	return encodeVarint((num << 3) | wtype)
}

// ---- primitive encoders ----

func encodeString(fieldNum uint64, s string) []byte {
	b := []byte(s)
	return append(append(fieldTag(fieldNum, wireBytes), encodeVarint(uint64(len(b)))...), b...)
}

func encodeBytes(fieldNum uint64, b []byte) []byte {
	return append(append(fieldTag(fieldNum, wireBytes), encodeVarint(uint64(len(b)))...), b...)
}

func encodeUint64(fieldNum, v uint64) []byte {
	if v == 0 {
		return nil
	}
	return append(fieldTag(fieldNum, wireVarint), encodeVarint(v)...)
}

func encodeBool(fieldNum uint64, v bool) []byte {
	if !v {
		return nil
	}
	return append(fieldTag(fieldNum, wireVarint), 0x01)
}

func encodeDouble(fieldNum uint64, v float64) []byte {
	if v == 0 {
		return nil
	}
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, math.Float64bits(v))
	return append(fieldTag(fieldNum, wireFixed64), b...)
}

func encodeFloat(fieldNum uint64, v float32) []byte {
	if v == 0 {
		return nil
	}
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, math.Float32bits(v))
	return append(fieldTag(fieldNum, wireFixed32), b...)
}

func encodeMessage(fieldNum uint64, msg []byte) []byte {
	if len(msg) == 0 {
		return nil
	}
	return append(append(fieldTag(fieldNum, wireBytes), encodeVarint(uint64(len(msg)))...), msg...)
}

func encodeStringSlice(fieldNum uint64, ss []string) []byte {
	var out []byte
	for _, s := range ss {
		out = append(out, encodeString(fieldNum, s)...)
	}
	return out
}

// ---- decoder helpers ----

type protoDecoder struct {
	buf []byte
	pos int
}

func newDecoder(b []byte) *protoDecoder { return &protoDecoder{buf: b} }

func (d *protoDecoder) done() bool { return d.pos >= len(d.buf) }

// next returns (fieldNum, wireType, value_bytes_or_nil, varint_value)
func (d *protoDecoder) next() (fieldNum, wireType uint64, raw []byte, varintVal uint64, err error) {
	if d.done() {
		return 0, 0, nil, 0, fmt.Errorf("EOF")
	}
	tag, n := decodeVarint(d.buf[d.pos:])
	if n <= 0 {
		return 0, 0, nil, 0, fmt.Errorf("bad tag")
	}
	d.pos += n
	fieldNum = tag >> 3
	wireType = tag & 0x7

	switch wireType {
	case wireVarint:
		v, n2 := decodeVarint(d.buf[d.pos:])
		if n2 <= 0 {
			return 0, 0, nil, 0, fmt.Errorf("bad varint")
		}
		d.pos += n2
		return fieldNum, wireType, nil, v, nil
	case wireFixed64:
		if d.pos+8 > len(d.buf) {
			return 0, 0, nil, 0, fmt.Errorf("short fixed64")
		}
		raw = d.buf[d.pos : d.pos+8]
		d.pos += 8
		return fieldNum, wireType, raw, 0, nil
	case wireBytes:
		l, n2 := decodeVarint(d.buf[d.pos:])
		if n2 <= 0 {
			return 0, 0, nil, 0, fmt.Errorf("bad length")
		}
		d.pos += n2
		if d.pos+int(l) > len(d.buf) {
			return 0, 0, nil, 0, fmt.Errorf("short bytes")
		}
		raw = d.buf[d.pos : d.pos+int(l)]
		d.pos += int(l)
		return fieldNum, wireType, raw, 0, nil
	case wireFixed32:
		if d.pos+4 > len(d.buf) {
			return 0, 0, nil, 0, fmt.Errorf("short fixed32")
		}
		raw = d.buf[d.pos : d.pos+4]
		d.pos += 4
		return fieldNum, wireType, raw, 0, nil
	default:
		return 0, 0, nil, 0, fmt.Errorf("unknown wire type %d", wireType)
	}
}

// ============================================================
// Proto 消息类型
// ============================================================

// Host （field numbers 1-10）
type Host struct {
	Platform        string
	PlatformVersion string
	Cpu             []string
	MemTotal        uint64
	DiskTotal       uint64
	SwapTotal       uint64
	Arch            string
	Virtualization  string
	BootTime        uint64
	Version         string
}

func (h *Host) Marshal() []byte {
	var b []byte
	b = append(b, encodeString(1, h.Platform)...)
	b = append(b, encodeString(2, h.PlatformVersion)...)
	b = append(b, encodeStringSlice(3, h.Cpu)...)
	b = append(b, encodeUint64(4, h.MemTotal)...)
	b = append(b, encodeUint64(5, h.DiskTotal)...)
	b = append(b, encodeUint64(6, h.SwapTotal)...)
	b = append(b, encodeString(7, h.Arch)...)
	b = append(b, encodeString(8, h.Virtualization)...)
	b = append(b, encodeUint64(9, h.BootTime)...)
	b = append(b, encodeString(10, h.Version)...)
	return b
}

// Uint64Receipt
type Uint64Receipt struct {
	Data uint64
}

func (r *Uint64Receipt) Unmarshal(b []byte) error {
	d := newDecoder(b)
	for !d.done() {
		fn, _, _, v, err := d.next()
		if err != nil {
			return err
		}
		if fn == 1 {
			r.Data = v
		}
	}
	return nil
}

// IP
type IP struct {
	Ipv4 string
	Ipv6 string
}

func (ip *IP) Marshal() []byte {
	var b []byte
	b = append(b, encodeString(1, ip.Ipv4)...)
	b = append(b, encodeString(2, ip.Ipv6)...)
	return b
}

func (ip *IP) Unmarshal(b []byte) error {
	d := newDecoder(b)
	for !d.done() {
		fn, _, raw, _, err := d.next()
		if err != nil {
			return err
		}
		switch fn {
		case 1:
			ip.Ipv4 = string(raw)
		case 2:
			ip.Ipv6 = string(raw)
		}
	}
	return nil
}

// GeoIP
type GeoIP struct {
	Use6              bool
	Ip                *IP
	CountryCode       string
	DashboardBootTime uint64
}

func (g *GeoIP) Marshal() []byte {
	var b []byte
	b = append(b, encodeBool(1, g.Use6)...)
	if g.Ip != nil {
		b = append(b, encodeMessage(2, g.Ip.Marshal())...)
	}
	b = append(b, encodeString(3, g.CountryCode)...)
	b = append(b, encodeUint64(4, g.DashboardBootTime)...)
	return b
}

func (g *GeoIP) Unmarshal(b []byte) error {
	d := newDecoder(b)
	for !d.done() {
		fn, wt, raw, v, err := d.next()
		if err != nil {
			return err
		}
		switch fn {
		case 1:
			g.Use6 = v != 0
		case 2:
			if wt == wireBytes {
				g.Ip = &IP{}
				if err := g.Ip.Unmarshal(raw); err != nil {
					return err
				}
			}
		case 3:
			g.CountryCode = string(raw)
		case 4:
			g.DashboardBootTime = v
		}
	}
	return nil
}

// State
type State struct {
	Cpu            float64
	MemUsed        uint64
	SwapUsed       uint64
	DiskUsed       uint64
	NetInTransfer  uint64
	NetOutTransfer uint64
	NetInSpeed     uint64
	NetOutSpeed    uint64
	Uptime         uint64
	Load1          float64
	Load5          float64
	Load15         float64
	TcpConnCount   uint64
	UdpConnCount   uint64
	ProcessCount   uint64
}

func (s *State) Marshal() []byte {
	var b []byte
	b = append(b, encodeDouble(1, s.Cpu)...)
	b = append(b, encodeUint64(2, s.MemUsed)...)
	b = append(b, encodeUint64(3, s.SwapUsed)...)
	b = append(b, encodeUint64(4, s.DiskUsed)...)
	b = append(b, encodeUint64(5, s.NetInTransfer)...)
	b = append(b, encodeUint64(6, s.NetOutTransfer)...)
	b = append(b, encodeUint64(7, s.NetInSpeed)...)
	b = append(b, encodeUint64(8, s.NetOutSpeed)...)
	b = append(b, encodeUint64(9, s.Uptime)...)
	b = append(b, encodeDouble(10, s.Load1)...)
	b = append(b, encodeDouble(11, s.Load5)...)
	b = append(b, encodeDouble(12, s.Load15)...)
	b = append(b, encodeUint64(13, s.TcpConnCount)...)
	b = append(b, encodeUint64(14, s.UdpConnCount)...)
	b = append(b, encodeUint64(15, s.ProcessCount)...)
	return b
}

// Receipt
type Receipt struct {
	Proced bool
}

func (r *Receipt) Unmarshal(b []byte) error {
	d := newDecoder(b)
	for !d.done() {
		fn, _, _, v, err := d.next()
		if err != nil {
			return err
		}
		if fn == 1 {
			r.Proced = v != 0
		}
	}
	return nil
}

// Task
type Task struct {
	Id   uint64
	Type uint64
	Data string
}

func (t *Task) Unmarshal(b []byte) error {
	d := newDecoder(b)
	for !d.done() {
		fn, _, raw, v, err := d.next()
		if err != nil {
			return err
		}
		switch fn {
		case 1:
			t.Id = v
		case 2:
			t.Type = v
		case 3:
			t.Data = string(raw)
		}
	}
	return nil
}

// TaskResult
type TaskResult struct {
	Id         uint64
	Type       uint64
	Delay      float32
	Data       string
	Successful bool
}

func (r *TaskResult) Marshal() []byte {
	var b []byte
	b = append(b, encodeUint64(1, r.Id)...)
	b = append(b, encodeUint64(2, r.Type)...)
	b = append(b, encodeFloat(3, r.Delay)...)
	b = append(b, encodeString(4, r.Data)...)
	b = append(b, encodeBool(5, r.Successful)...)
	return b
}
