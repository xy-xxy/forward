// probe.go - 接口连通性测试(跨平台)
//
// 一台机器开"监听"模式(可选 echo 回写),另一台开"发送"模式发送 payload,
// 用来验证一条转发链路是否真的通。功能等价于 mini-netcat。
//
// 支持 TCP / UDP,发送模式可选单次或定时重发。

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type ProbeMode int

const (
	ProbeListen ProbeMode = iota
	ProbeSend
)

type ProbeConfig struct {
	Mode        ProbeMode
	Proto       string        // "tcp" / "udp"
	Addr        string        // listen: 本地监听 "0.0.0.0:9999";send: 目标 "1.2.3.4:9999"
	Payload     []byte        // 发送内容
	Interval    time.Duration // 0 = 单次;>0 = 定时循环
	Echo        bool          // listen 模式:收到后回 echo
	ReadTimeout time.Duration // send 模式:等待响应的时长,默认 1s
}

type Probe struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	running atomic.Bool
	sent    atomic.Int64
	recv    atomic.Int64

	closeMu sync.Mutex
	closers []io.Closer

	onLog func(string)
}

func NewProbe(onLog func(string)) *Probe {
	return &Probe{onLog: onLog}
}

func (p *Probe) Sent() int64   { return p.sent.Load() }
func (p *Probe) Recv() int64   { return p.recv.Load() }
func (p *Probe) Running() bool { return p.running.Load() }

func (p *Probe) log(format string, args ...interface{}) {
	if p.onLog != nil {
		p.onLog(fmt.Sprintf(format, args...))
	}
}

func (p *Probe) trackCloser(c io.Closer) {
	p.closeMu.Lock()
	p.closers = append(p.closers, c)
	p.closeMu.Unlock()
}

func (p *Probe) closeAll() {
	p.closeMu.Lock()
	cs := p.closers
	p.closers = nil
	p.closeMu.Unlock()
	for _, c := range cs {
		c.Close()
	}
}

func (p *Probe) Start(cfg ProbeConfig) error {
	if !p.running.CompareAndSwap(false, true) {
		return fmt.Errorf("已经在运行")
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.sent.Store(0)
	p.recv.Store(0)
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = time.Second
	}

	var err error
	switch cfg.Mode {
	case ProbeListen:
		switch cfg.Proto {
		case "tcp":
			err = p.startTCPListen(cfg)
		case "udp":
			err = p.startUDPListen(cfg)
		default:
			err = fmt.Errorf("未知协议: %q", cfg.Proto)
		}
	case ProbeSend:
		switch cfg.Proto {
		case "tcp":
			p.startTCPSend(cfg)
		case "udp":
			p.startUDPSend(cfg)
		default:
			err = fmt.Errorf("未知协议: %q", cfg.Proto)
		}
	default:
		err = fmt.Errorf("未知模式")
	}

	if err != nil {
		p.running.Store(false)
		p.cancel()
		return err
	}
	return nil
}

func (p *Probe) Stop() {
	if !p.running.CompareAndSwap(true, false) {
		return
	}
	p.cancel()
	p.closeAll()
	p.wg.Wait()
	p.log("已停止")
}

// ============ TCP 监听 ============

func (p *Probe) startTCPListen(cfg ProbeConfig) error {
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	p.trackCloser(ln)
	p.log("[TCP 监听] %s 启动 (echo=%v)", cfg.Addr, cfg.Echo)

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-p.ctx.Done():
					return
				default:
					p.log("[TCP 监听] accept 错误: %v", err)
					return
				}
			}
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				p.handleTCPConn(conn, cfg.Echo)
			}()
		}
	}()
	return nil
}

func (p *Probe) handleTCPConn(conn net.Conn, echo bool) {
	defer conn.Close()
	p.trackCloser(conn)
	remote := conn.RemoteAddr().String()
	p.log("[TCP 监听] 客户端接入: %s", remote)
	buf := make([]byte, 64*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			p.recv.Add(int64(n))
			p.log("[TCP 监听] 收到 %s -> %d 字节: %s", remote, n, previewBytes(buf[:n]))
			if echo {
				if _, werr := conn.Write(buf[:n]); werr == nil {
					p.sent.Add(int64(n))
				}
			}
		}
		if err != nil {
			p.log("[TCP 监听] 客户端断开: %s (%v)", remote, err)
			return
		}
	}
}

// ============ UDP 监听 ============

func (p *Probe) startUDPListen(cfg ProbeConfig) error {
	laddr, err := net.ResolveUDPAddr("udp", cfg.Addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return err
	}
	p.trackCloser(conn)
	p.log("[UDP 监听] %s 启动 (echo=%v)", cfg.Addr, cfg.Echo)

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		buf := make([]byte, 64*1024)
		for {
			n, ra, err := conn.ReadFromUDP(buf)
			if err != nil {
				select {
				case <-p.ctx.Done():
					return
				default:
					p.log("[UDP 监听] read 错误: %v", err)
					return
				}
			}
			p.recv.Add(int64(n))
			p.log("[UDP 监听] 收到 %s -> %d 字节: %s", ra.String(), n, previewBytes(buf[:n]))
			if cfg.Echo {
				if _, werr := conn.WriteToUDP(buf[:n], ra); werr == nil {
					p.sent.Add(int64(n))
				}
			}
		}
	}()
	return nil
}

// ============ TCP 发送 ============

func (p *Probe) startTCPSend(cfg ProbeConfig) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.doTCPSend(cfg)
		if cfg.Interval > 0 {
			ticker := time.NewTicker(cfg.Interval)
			defer ticker.Stop()
			for {
				select {
				case <-p.ctx.Done():
					return
				case <-ticker.C:
					p.doTCPSend(cfg)
				}
			}
		} else {
			// 单次发送完毕后自动归位 running=false 由 Stop 调用方触发
			// 让 GUI 显示"已结束"
			<-p.ctx.Done()
		}
	}()
}

func (p *Probe) doTCPSend(cfg ProbeConfig) {
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(p.ctx, "tcp", cfg.Addr)
	if err != nil {
		p.log("[TCP 发送] 连接 %s 失败: %v", cfg.Addr, err)
		return
	}
	p.trackCloser(conn)
	defer conn.Close()

	n, err := conn.Write(cfg.Payload)
	if err != nil {
		p.log("[TCP 发送] 写入失败: %v", err)
		return
	}
	p.sent.Add(int64(n))
	p.log("[TCP 发送] -> %s 发送 %d 字节: %s", cfg.Addr, n, previewBytes(cfg.Payload))

	// 读响应
	conn.SetReadDeadline(time.Now().Add(cfg.ReadTimeout))
	buf := make([]byte, 64*1024)
	rn, rerr := conn.Read(buf)
	if rn > 0 {
		p.recv.Add(int64(rn))
		p.log("[TCP 发送] <- %s 收到 %d 字节: %s", cfg.Addr, rn, previewBytes(buf[:rn]))
	} else if rerr != nil && !isTimeout(rerr) {
		p.log("[TCP 发送] 读响应错误: %v", rerr)
	} else {
		p.log("[TCP 发送] %s 无响应(超时 %v)", cfg.Addr, cfg.ReadTimeout)
	}
}

// ============ UDP 发送 ============

func (p *Probe) startUDPSend(cfg ProbeConfig) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.doUDPSend(cfg)
		if cfg.Interval > 0 {
			ticker := time.NewTicker(cfg.Interval)
			defer ticker.Stop()
			for {
				select {
				case <-p.ctx.Done():
					return
				case <-ticker.C:
					p.doUDPSend(cfg)
				}
			}
		} else {
			<-p.ctx.Done()
		}
	}()
}

func (p *Probe) doUDPSend(cfg ProbeConfig) {
	raddr, err := net.ResolveUDPAddr("udp", cfg.Addr)
	if err != nil {
		p.log("[UDP 发送] 解析 %s 失败: %v", cfg.Addr, err)
		return
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		p.log("[UDP 发送] 连接 %s 失败: %v", cfg.Addr, err)
		return
	}
	p.trackCloser(conn)
	defer conn.Close()

	n, err := conn.Write(cfg.Payload)
	if err != nil {
		p.log("[UDP 发送] 写入失败: %v", err)
		return
	}
	p.sent.Add(int64(n))
	p.log("[UDP 发送] -> %s 发送 %d 字节: %s", cfg.Addr, n, previewBytes(cfg.Payload))

	conn.SetReadDeadline(time.Now().Add(cfg.ReadTimeout))
	buf := make([]byte, 64*1024)
	rn, rerr := conn.Read(buf)
	if rn > 0 {
		p.recv.Add(int64(rn))
		p.log("[UDP 发送] <- %s 收到 %d 字节: %s", cfg.Addr, rn, previewBytes(buf[:rn]))
	} else if rerr != nil && !isTimeout(rerr) {
		p.log("[UDP 发送] 读响应错误: %v", rerr)
	} else {
		p.log("[UDP 发送] %s 无响应(超时 %v)", cfg.Addr, cfg.ReadTimeout)
	}
}

// ============ 工具 ============

func previewBytes(b []byte) string {
	const maxShow = 80
	show := b
	more := ""
	if len(show) > maxShow {
		show = show[:maxShow]
		more = "..."
	}
	// 可显示字符直接用,不可显示用 .
	out := make([]byte, 0, len(show))
	for _, c := range show {
		if c >= 0x20 && c < 0x7f {
			out = append(out, c)
		} else {
			out = append(out, '.')
		}
	}
	return string(out) + more
}

func isTimeout(err error) bool {
	type timeouter interface{ Timeout() bool }
	if t, ok := err.(timeouter); ok {
		return t.Timeout()
	}
	return false
}
