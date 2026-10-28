// bandwidth.go - TCP 单向带宽测试；两端使用 Forward 带宽模式。
// 数据帧结束后回报接收量，不依赖 TCP 半关闭，支持经过转发链路测试。
package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	bandwidthHello     = "FWBW0001"
	bandwidthReady     = "FWBWOK01"
	bandwidthBlockSize = 64 * 1024
	bandwidthIOTimeout = 5 * time.Second
)

// BandwidthStats 保留最近一次测试结果，等待下次测试时仍可查看。
// 速率只计算测试载荷，不包含握手、帧头或结果回报。
type BandwidthStats struct {
	Enabled        bool
	Generation     uint64
	Active         bool
	Started        time.Time
	Elapsed        time.Duration
	Sent, Received int64
	PeerReceived   int64
	PeerElapsed    time.Duration
	Status         string
	Error          string
}

func (p *Probe) resetBandwidthStats(enabled bool) {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	p.sent.Store(0)
	p.recv.Store(0)
	p.stats = BandwidthStats{Enabled: enabled, Generation: p.stats.Generation + 1, Status: "等待连接"}
}

func (p *Probe) beginBandwidth() {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	p.sent.Store(0)
	p.recv.Store(0)
	p.stats = BandwidthStats{Enabled: true, Generation: p.stats.Generation + 1, Active: true, Started: time.Now(), Status: "测试中"}
}

func (p *Probe) finishBandwidth(err error) {
	p.statsMu.Lock()
	if !p.stats.Started.IsZero() {
		p.stats.Elapsed = time.Since(p.stats.Started)
	}
	p.stats.Active = false
	p.stats.Status = "已完成"
	if err != nil {
		p.stats.Status = "失败"
		p.stats.Error = err.Error()
		if p.ctx.Err() != nil {
			p.stats.Status = "已停止"
			p.stats.Error = ""
		}
	}
	status := p.stats.Status
	p.statsMu.Unlock()
	if err != nil {
		if p.ctx.Err() != nil {
			p.log("[带宽] 测试已停止")
		} else {
			p.log("[带宽] %s: %v", status, err)
		}
	}
}

func (p *Probe) BandwidthSnapshot() BandwidthStats {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	s := p.stats
	if s.Active {
		s.Elapsed = time.Since(s.Started)
	}
	s.Sent, s.Received = p.Sent(), p.Recv()
	return s
}

func bandwidthMbps(bytes int64, elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	return float64(bytes) * 8 / elapsed.Seconds() / 1e6
}

func (p *Probe) sendBandwidth(cfg ProbeConfig) {
	err := p.transferBandwidth(cfg)
	p.finishBandwidth(err)
}

func (p *Probe) transferBandwidth(cfg ProbeConfig) error {
	p.log("[带宽] 连接 %s，计划发送 %s", cfg.Addr, cfg.Duration)
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(p.ctx, "tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("连接失败: %w", err)
	}
	p.trackCloser(conn)
	defer p.releaseCloser(conn)
	conn.SetDeadline(time.Now().Add(bandwidthIOTimeout))
	if _, err := io.WriteString(conn, bandwidthHello); err != nil {
		return fmt.Errorf("发送握手失败: %w", err)
	}
	var hello [len(bandwidthHello)]byte
	if _, err := io.ReadFull(conn, hello[:]); err != nil {
		return fmt.Errorf("对端未响应带宽握手，请确认两端均启用带宽测试: %w", err)
	}
	if string(hello[:]) != bandwidthReady {
		return fmt.Errorf("对端不是 Forward 带宽监听端")
	}
	conn.SetDeadline(time.Time{})

	frame := make([]byte, 4+bandwidthBlockSize)
	binary.BigEndian.PutUint32(frame, bandwidthBlockSize)
	if _, err := rand.Read(frame[4:]); err != nil {
		return err
	}
	p.beginBandwidth()
	end := time.Now().Add(cfg.Duration)
	for time.Now().Before(end) {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		// 单次写入有超时；到达测试时长后完成当前帧，再请求接收端结果。
		conn.SetWriteDeadline(time.Now().Add(bandwidthIOTimeout))
		n, err := conn.Write(frame)
		if n > 4 {
			p.sent.Add(int64(n - 4))
		}
		if err != nil {
			return fmt.Errorf("发送失败，测试未完成: %w", err)
		}
		if n != len(frame) {
			return io.ErrShortWrite
		}
	}
	// 零长度帧结束数据流；不发送 FIN，避免转发器提前关闭回程连接。
	conn.SetDeadline(time.Now().Add(bandwidthIOTimeout))
	if err := binary.Write(conn, binary.BigEndian, uint32(0)); err != nil {
		return fmt.Errorf("结束测试失败: %w", err)
	}
	var report [2]uint64
	if err := binary.Read(conn, binary.BigEndian, &report); err != nil {
		return fmt.Errorf("未收到接收端结果: %w", err)
	}
	if report[0] != uint64(p.Sent()) || report[0] == 0 || report[1] == 0 || report[1] > uint64(2*time.Hour) {
		return fmt.Errorf("接收端回报无效或收发字节数不一致")
	}
	received, elapsed := int64(report[0]), time.Duration(report[1])
	p.statsMu.Lock()
	p.stats.PeerReceived, p.stats.PeerElapsed = received, elapsed
	p.statsMu.Unlock()
	p.log("[带宽] 完成：对端实收 %s，耗时 %.2f 秒，平均 %.2f Mbps", humanBytes(received), elapsed.Seconds(), bandwidthMbps(received, elapsed))
	return nil
}

func (p *Probe) receiveBandwidth(conn net.Conn) {
	p.trackCloser(conn)
	defer p.releaseCloser(conn)
	if !p.bandwidthBusy.CompareAndSwap(false, true) {
		p.log("[带宽] 已有测试连接，拒绝 %s", conn.RemoteAddr())
		return
	}
	defer p.bandwidthBusy.Store(false)
	// 握手错误不会覆盖上次有效测试结果。
	conn.SetDeadline(time.Now().Add(bandwidthIOTimeout))
	var hello [len(bandwidthHello)]byte
	if _, err := io.ReadFull(conn, hello[:]); err != nil || string(hello[:]) != bandwidthHello {
		if p.ctx.Err() == nil {
			p.log("[带宽] %s 握手失败，请在发送端启用带宽测试", conn.RemoteAddr())
		}
		return
	}
	if _, err := io.WriteString(conn, bandwidthReady); err != nil {
		return
	}
	conn.SetDeadline(time.Time{})
	p.beginBandwidth()
	p.log("[带宽] 接收 %s 的测试数据", conn.RemoteAddr())
	err := p.readBandwidth(conn)
	p.finishBandwidth(err)
}

func (p *Probe) readBandwidth(conn net.Conn) error {
	buf := make([]byte, bandwidthBlockSize)
	for {
		conn.SetReadDeadline(time.Now().Add(bandwidthIOTimeout))
		var size uint32
		if err := binary.Read(conn, binary.BigEndian, &size); err != nil {
			return fmt.Errorf("接收中断，测试未完成: %w", err)
		}
		if size > bandwidthBlockSize {
			return fmt.Errorf("无效的测试数据帧长度: %d", size)
		}
		if size == 0 {
			break
		}
		n, err := io.ReadFull(conn, buf[:size])
		p.recv.Add(int64(n))
		if err != nil {
			return fmt.Errorf("接收中断，测试未完成: %w", err)
		}
	}
	s := p.BandwidthSnapshot()
	if s.Received == 0 {
		return fmt.Errorf("未收到测试数据")
	}
	conn.SetWriteDeadline(time.Now().Add(bandwidthIOTimeout))
	if err := binary.Write(conn, binary.BigEndian, [2]uint64{uint64(s.Received), uint64(s.Elapsed)}); err != nil {
		return fmt.Errorf("回报结果失败: %w", err)
	}
	p.log("[带宽] 完成：实收 %s，耗时 %.2f 秒，平均 %.2f Mbps；继续等待下一次测试", humanBytes(s.Received), s.Elapsed.Seconds(), bandwidthMbps(s.Received, s.Elapsed))
	return nil
}
