package main

// ============================================================
// 纯标准库 gRPC 客户端传输层
//
// gRPC wire protocol:
//   - HTTP/2 POST /<service>/<method>
//   - Content-Type: application/grpc
//   - 每帧：1字节压缩标志 + 4字节大端长度 + N字节 protobuf
//
// TLS 模式：net/http Transport 自动 ALPN h2 协商
// 两种流模式：
//   NewBidiStream    — 等待服务端响应头（RequestTask 等双向流）
//   NewClientStream  — 立即返回，不等响应头（ReportSystemState 等 client-streaming）
// ============================================================

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net/http"
	"sync"
	"time"
)

const grpcContentType = "application/grpc"

// grpcFrame 编码一个 gRPC data frame
func grpcFrame(payload []byte) []byte {
	frame := make([]byte, 5+len(payload))
	frame[0] = 0 // no compression
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)
	return frame
}

// readGRPCFrame 从 reader 读一个 gRPC data frame，返回 payload
func readGRPCFrame(r io.Reader) ([]byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[1:5])
	if length == 0 {
		return []byte{}, nil
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// ============================================================
// GRPCConn
// ============================================================

type GRPCConn struct {
	target string
	meta   map[string]string
	client *http.Client
}

func newGRPCConn(target string, useTLS, insecure bool, meta map[string]string) *GRPCConn {
	tr := &http.Transport{
		ForceAttemptHTTP2:   true,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
	if useTLS {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: insecure}
	}
	scheme := "https"
	if !useTLS {
		scheme = "http"
	}
	return &GRPCConn{
		target: scheme + "://" + target,
		meta:   meta,
		client: &http.Client{Transport: tr},
	}
}

func (c *GRPCConn) Close() {
	c.client.CloseIdleConnections()
}

func (c *GRPCConn) newRequest(ctx context.Context, method string, body io.Reader) *http.Request {
	req, _ := http.NewRequestWithContext(ctx, "POST", c.target+method, body)
	req.Header.Set("Content-Type", grpcContentType)
	req.Header.Set("TE", "trailers")
	req.Header.Set("User-Agent", "pangu-agent/"+agentVersion)
	for k, v := range c.meta {
		req.Header.Set(k, v)
	}
	return req
}

// ============================================================
// Unary RPC
// ============================================================

func (c *GRPCConn) Invoke(ctx context.Context, method string, reqMsg []byte, respMsg interface {
	Unmarshal([]byte) error
}) error {
	pr, pw := io.Pipe()
	go func() {
		pw.Write(grpcFrame(reqMsg))
		pw.Close()
	}()
	req := c.newRequest(ctx, method, pr)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("grpc invoke %s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("grpc status %d for %s", resp.StatusCode, method)
	}
	payload, err := readGRPCFrame(resp.Body)
	if err != nil {
		return fmt.Errorf("grpc read frame: %w", err)
	}
	return respMsg.Unmarshal(payload)
}

// ============================================================
// BidiStream — 双向 / client-streaming 流
// ============================================================

type BidiStream struct {
	pw     *io.PipeWriter
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool

	// 懒初始化：NewClientStream 时这两个有值，reader/resp 为 nil
	respCh <-chan *http.Response
	errCh  <-chan error

	// 正常情况下由 NewBidiStream 或首次 Recv() 填充
	resp   *http.Response
	reader *bufio.Reader
}

// ensureReader 等待响应头到达（用于 client-streaming 的首次 Recv）
func (s *BidiStream) ensureReader() error {
	if s.reader != nil {
		return nil
	}
	// 等待后台 Do() 完成
	select {
	case resp := <-s.respCh:
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("grpc stream status %d", resp.StatusCode)
		}
		s.resp   = resp
		s.reader = bufio.NewReader(resp.Body)
		return nil
	case err := <-s.errCh:
		return err
	}
}

func (s *BidiStream) Send(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	_, err := s.pw.Write(grpcFrame(payload))
	return err
}

func (s *BidiStream) Recv() ([]byte, error) {
	if err := s.ensureReader(); err != nil {
		return nil, err
	}
	return readGRPCFrame(s.reader)
}

func (s *BidiStream) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.pw.Close()
	if s.resp != nil {
		s.resp.Body.Close()
	}
	s.cancel()
}

// ============================================================
// NewBidiStream — 双向流（RequestTask）
// 等服务端响应头到达（最多 15s）再返回
// ============================================================

func (c *GRPCConn) NewBidiStream(ctx context.Context, method string) (*BidiStream, error) {
	childCtx, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	req := c.newRequest(childCtx, method, pr)

	respCh := make(chan *http.Response, 1)
	errCh  := make(chan error, 1)
	go func() {
		resp, err := c.client.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	select {
	case resp := <-respCh:
		if resp.StatusCode != http.StatusOK {
			cancel()
			resp.Body.Close()
			return nil, fmt.Errorf("grpc stream %s: status %d", method, resp.StatusCode)
		}
		return &BidiStream{
			pw:     pw,
			cancel: cancel,
			resp:   resp,
			reader: bufio.NewReader(resp.Body),
		}, nil
	case err := <-errCh:
		cancel()
		return nil, fmt.Errorf("grpc stream %s: %w", method, err)
	case <-time.After(15 * time.Second):
		cancel()
		pw.Close()
		return nil, fmt.Errorf("grpc stream %s: dial timeout", method)
	case <-ctx.Done():
		cancel()
		pw.Close()
		return nil, ctx.Err()
	}
}

// ============================================================
// NewClientStream — client-streaming（ReportSystemState）
// 立即返回，不等服务端响应头；首次 Recv() 时才等
// ============================================================

func (c *GRPCConn) NewClientStream(ctx context.Context, method string) (*BidiStream, error) {
	childCtx, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	req := c.newRequest(childCtx, method, pr)

	respCh := make(chan *http.Response, 1)
	errCh  := make(chan error, 1)
	go func() {
		resp, err := c.client.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	return &BidiStream{
		pw:     pw,
		cancel: cancel,
		respCh: respCh,
		errCh:  errCh,
	}, nil
}

// suppress unused import
var _ = math.Float64bits
