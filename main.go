// main.go - Forward: TCP/UDP 端口转发核心
//
// 跨平台代码（Windows + Linux/macOS 都能编译）。
// 平台相关：
//   - GUI:     gui_windows.go (Walk) / gui_other.go (stub)
//   - Console: console_windows.go (AttachConsole) / console_other.go (stub)
//   - 入口:    cli.go (跨平台)
//
// Windows 兼容 Server 2012：必须用 Go 1.20.x 编译

package main

import (
	"context"
	"fmt"
	"net"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ============ 配置 ============

type Rule struct {
	Listen string `json:"listen"`
	Target string `json:"target"`
	Proto  string `json:"proto"`
}

type Config struct {
	Rules      []Rule `json:"rules"`
	UDPBuffer  int    `json:"udpBuffer"`
	UDPTimeout int    `json:"udpTimeout"`
}

const (
	defaultUDPBuffer  = 65536
	defaultUDPTimeout = 600
	configFileName    = "forward.json"
)

// ============ 端口范围解析 ============

type addrSpec struct {
	Host      string
	StartPort int
	EndPort   int
}

func (a addrSpec) Count() int { return a.EndPort - a.StartPort + 1 }

func parseAddrSpec(s string) (addrSpec, error) {
	s = strings.TrimSpace(s)
	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return addrSpec{}, fmt.Errorf("地址缺少端口: %q", s)
	}
	host := s[:idx]
	portPart := s[idx+1:]

	if dash := strings.Index(portPart, "-"); dash > 0 {
		startStr := strings.TrimSpace(portPart[:dash])
		endStr := strings.TrimSpace(portPart[dash+1:])
		start, err1 := strconv.Atoi(startStr)
		end, err2 := strconv.Atoi(endStr)
		if err1 != nil || err2 != nil {
			return addrSpec{}, fmt.Errorf("端口范围解析失败: %q", portPart)
		}
		if start < 1 || end > 65535 || start > end {
			return addrSpec{}, fmt.Errorf("端口范围非法: %q（必须 1..65535 且起点 <= 终点）", portPart)
		}
		return addrSpec{Host: host, StartPort: start, EndPort: end}, nil
	}

	port, err := strconv.Atoi(strings.TrimSpace(portPart))
	if err != nil || port < 1 || port > 65535 {
		return addrSpec{}, fmt.Errorf("端口非法: %q", portPart)
	}
	return addrSpec{Host: host, StartPort: port, EndPort: port}, nil
}

func expandRule(r Rule) ([]Rule, error) {
	ls, err := parseAddrSpec(r.Listen)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	ts, err := parseAddrSpec(r.Target)
	if err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}

	lCount := ls.Count()
	tCount := ts.Count()

	if lCount == 1 && tCount == 1 {
		return []Rule{r}, nil
	}
	if lCount == 1 && tCount > 1 {
		return nil, fmt.Errorf("listen 是单端口时 target 不能是范围")
	}
	if tCount == 1 {
		out := make([]Rule, 0, lCount)
		for p := ls.StartPort; p <= ls.EndPort; p++ {
			out = append(out, Rule{
				Listen: fmt.Sprintf("%s:%d", ls.Host, p),
				Target: fmt.Sprintf("%s:%d", ts.Host, ts.StartPort),
				Proto:  r.Proto,
			})
		}
		return out, nil
	}
	if lCount != tCount {
		return nil, fmt.Errorf("端口范围长度不一致: listen=%d, target=%d", lCount, tCount)
	}
	out := make([]Rule, 0, lCount)
	for i := 0; i < lCount; i++ {
		out = append(out, Rule{
			Listen: fmt.Sprintf("%s:%d", ls.Host, ls.StartPort+i),
			Target: fmt.Sprintf("%s:%d", ts.Host, ts.StartPort+i),
			Proto:  r.Proto,
		})
	}
	return out, nil
}

// ============ 转发器 ============

type Forwarder struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	closeMu   sync.Mutex
	listeners []net.Listener
	udpConns  []*net.UDPConn

	running atomic.Bool

	tcpConns    atomic.Int64
	udpSessions atomic.Int64
	bytesIn     atomic.Int64
	bytesOut    atomic.Int64

	sessionsMu sync.Mutex
	sessions   map[int64]*SessionInfo
	nextID     atomic.Int64

	onLog func(string)
}

type SessionInfo struct {
	ID         int64
	Proto      string
	Client     string
	Listen     string
	Target     string
	StartedAt  time.Time
	BytesIn    atomic.Int64
	BytesOut   atomic.Int64
	LastActive atomic.Int64
}

type SessionSnapshot struct {
	ID         int64
	Proto      string
	Client     string
	Listen     string
	Target     string
	StartedAt  time.Time
	LastActive time.Time
	BytesIn    int64
	BytesOut   int64
	InRate     float64
	OutRate    float64
}

func NewForwarder(onLog func(string)) *Forwarder {
	return &Forwarder{onLog: onLog, sessions: make(map[int64]*SessionInfo)}
}

func (f *Forwarder) registerSession(proto, client, listen, target string) *SessionInfo {
	s := &SessionInfo{
		ID:        f.nextID.Add(1),
		Proto:     proto,
		Client:    client,
		Listen:    listen,
		Target:    target,
		StartedAt: time.Now(),
	}
	s.LastActive.Store(time.Now().UnixNano())
	f.sessionsMu.Lock()
	f.sessions[s.ID] = s
	f.sessionsMu.Unlock()
	return s
}

func (f *Forwarder) unregisterSession(id int64) {
	f.sessionsMu.Lock()
	delete(f.sessions, id)
	f.sessionsMu.Unlock()
}

func (f *Forwarder) snapshotSessions() []SessionSnapshot {
	f.sessionsMu.Lock()
	out := make([]SessionSnapshot, 0, len(f.sessions))
	for _, s := range f.sessions {
		out = append(out, SessionSnapshot{
			ID:         s.ID,
			Proto:      s.Proto,
			Client:     s.Client,
			Listen:     s.Listen,
			Target:     s.Target,
			StartedAt:  s.StartedAt,
			LastActive: time.Unix(0, s.LastActive.Load()),
			BytesIn:    s.BytesIn.Load(),
			BytesOut:   s.BytesOut.Load(),
		})
	}
	f.sessionsMu.Unlock()
	return out
}

func (f *Forwarder) Running() bool { return f.running.Load() }

func (f *Forwarder) log(format string, args ...interface{}) {
	if f.onLog != nil {
		f.onLog(fmt.Sprintf(format, args...))
	}
}

func (f *Forwarder) Start(cfg Config) error {
	if !f.running.CompareAndSwap(false, true) {
		return fmt.Errorf("已经在运行")
	}
	f.ctx, f.cancel = context.WithCancel(context.Background())
	f.tcpConns.Store(0)
	f.udpSessions.Store(0)
	f.bytesIn.Store(0)
	f.bytesOut.Store(0)
	f.listeners = nil
	f.udpConns = nil
	f.sessionsMu.Lock()
	f.sessions = make(map[int64]*SessionInfo)
	f.sessionsMu.Unlock()

	if cfg.UDPBuffer <= 0 {
		cfg.UDPBuffer = defaultUDPBuffer
	}
	if cfg.UDPTimeout <= 0 {
		cfg.UDPTimeout = defaultUDPTimeout
	}

	// 展开端口范围
	expanded := make([]Rule, 0, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		ex, err := expandRule(rule)
		if err != nil {
			f.log("规则展开失败 %s -> %s: %v", rule.Listen, rule.Target, err)
			continue
		}
		if len(ex) > 1 {
			f.log("展开端口范围 %s -> %s (%d 个端口)", rule.Listen, rule.Target, len(ex))
		}
		expanded = append(expanded, ex...)
	}

	for _, rule := range expanded {
		p := rule.Proto
		if p == "" {
			p = "both"
		}
		if p == "tcp" || p == "both" {
			if err := f.startTCP(rule); err != nil {
				f.log("[TCP] %s 监听失败: %v", rule.Listen, err)
			}
		}
		if p == "udp" || p == "both" {
			if err := f.startUDP(rule, cfg.UDPBuffer, time.Duration(cfg.UDPTimeout)*time.Second); err != nil {
				f.log("[UDP] %s 监听失败: %v", rule.Listen, err)
			}
		}
	}
	f.log("转发已启动")
	return nil
}

func (f *Forwarder) Stop() {
	if !f.running.CompareAndSwap(true, false) {
		return
	}
	f.cancel()
	f.closeMu.Lock()
	for _, l := range f.listeners {
		l.Close()
	}
	for _, c := range f.udpConns {
		c.Close()
	}
	f.listeners = nil
	f.udpConns = nil
	f.closeMu.Unlock()
	f.wg.Wait()
	debug.FreeOSMemory()
	f.log("转发已停止")
}

// ---- TCP ----

func (f *Forwarder) startTCP(rule Rule) error {
	ln, err := net.Listen("tcp", rule.Listen)
	if err != nil {
		return err
	}
	f.closeMu.Lock()
	f.listeners = append(f.listeners, ln)
	f.closeMu.Unlock()
	f.log("[TCP] 监听 %s -> %s", rule.Listen, rule.Target)

	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-f.ctx.Done():
					return
				default:
					f.log("[TCP] accept 错误: %v", err)
					return
				}
			}
			f.wg.Add(1)
			go func() {
				defer f.wg.Done()
				f.handleTCP(conn, rule.Listen, rule.Target)
			}()
		}
	}()
	return nil
}

func (f *Forwarder) handleTCP(client net.Conn, listen, target string) {
	defer client.Close()
	server, err := net.Dial("tcp", target)
	if err != nil {
		f.log("[TCP] 连接目标 %s 失败: %v", target, err)
		return
	}
	defer server.Close()

	sess := f.registerSession("tcp", client.RemoteAddr().String(), listen, target)
	defer f.unregisterSession(sess.ID)

	f.tcpConns.Add(1)
	defer f.tcpConns.Add(-1)

	done := make(chan struct{}, 2)
	go func() {
		f.copyTCP(server, client, sess, &f.bytesIn, &sess.BytesIn)
		server.Close()
		client.Close()
		done <- struct{}{}
	}()
	go func() {
		f.copyTCP(client, server, sess, &f.bytesOut, &sess.BytesOut)
		server.Close()
		client.Close()
		done <- struct{}{}
	}()
	<-done
	<-done
}

func (f *Forwarder) copyTCP(dst net.Conn, src net.Conn, sess *SessionInfo, counters ...*atomic.Int64) {
	buf := make([]byte, 16*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			dst.Write(buf[:n])
			for _, c := range counters {
				c.Add(int64(n))
			}
			sess.LastActive.Store(time.Now().UnixNano())
		}
		if err != nil {
			return
		}
	}
}

// ---- UDP ----

type udpSession struct {
	serverConn *net.UDPConn
	lastActive atomic.Int64
	info       *SessionInfo
}

func (f *Forwarder) startUDP(rule Rule, bufSize int, timeout time.Duration) error {
	listenAddr, err := net.ResolveUDPAddr("udp", rule.Listen)
	if err != nil {
		return err
	}
	targetAddr, err := net.ResolveUDPAddr("udp", rule.Target)
	if err != nil {
		return err
	}
	clientConn, err := net.ListenUDP("udp", listenAddr)
	if err != nil {
		return err
	}
	// 主监听 socket：聚合所有客户端入站流量，给 2MB
	clientConn.SetReadBuffer(2 * 1024 * 1024)
	clientConn.SetWriteBuffer(2 * 1024 * 1024)

	f.closeMu.Lock()
	f.udpConns = append(f.udpConns, clientConn)
	f.closeMu.Unlock()
	f.log("[UDP] 监听 %s -> %s", rule.Listen, rule.Target)

	sessions := make(map[string]*udpSession)
	var sessMu sync.Mutex

	// 清理超时会话
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-f.ctx.Done():
				return
			case <-ticker.C:
				sessMu.Lock()
				now := time.Now().UnixNano()
				for k, s := range sessions {
					if time.Duration(now-s.lastActive.Load()) > timeout {
						s.serverConn.Close()
						delete(sessions, k)
						f.udpSessions.Add(-1)
						if s.info != nil {
							f.unregisterSession(s.info.ID)
						}
					}
				}
				sessMu.Unlock()
			}
		}
	}()

	// 主循环：读客户端 -> 转目标
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		buf := make([]byte, bufSize)
		for {
			n, clientAddr, err := clientConn.ReadFromUDP(buf)
			if err != nil {
				select {
				case <-f.ctx.Done():
					return
				default:
					return
				}
			}
			f.bytesIn.Add(int64(n))
			key := clientAddr.String()

			sessMu.Lock()
			sess, ok := sessions[key]
			if !ok {
				srvConn, err := net.DialUDP("udp", nil, targetAddr)
				if err != nil {
					sessMu.Unlock()
					f.log("[UDP] 连接目标失败: %v", err)
					continue
				}
				// 每会话 socket：单条客户端流量，512KB 足够
				srvConn.SetReadBuffer(512 * 1024)
				srvConn.SetWriteBuffer(512 * 1024)
				sess = &udpSession{serverConn: srvConn}
				sess.lastActive.Store(time.Now().UnixNano())
				sess.info = f.registerSession("udp", key, rule.Listen, rule.Target)
				sessions[key] = sess
				f.udpSessions.Add(1)

				// 回包 goroutine
				f.wg.Add(1)
				go func(ca *net.UDPAddr, s *udpSession) {
					defer f.wg.Done()
					rbuf := make([]byte, bufSize)
					for {
						s.serverConn.SetReadDeadline(time.Now().Add(timeout))
						rn, err := s.serverConn.Read(rbuf)
						if err != nil {
							return
						}
						clientConn.WriteToUDP(rbuf[:rn], ca)
						f.bytesOut.Add(int64(rn))
						s.lastActive.Store(time.Now().UnixNano())
						if s.info != nil {
							s.info.BytesOut.Add(int64(rn))
							s.info.LastActive.Store(time.Now().UnixNano())
						}
					}
				}(clientAddr, sess)
			}
			sess.lastActive.Store(time.Now().UnixNano())
			if sess.info != nil {
				sess.info.BytesIn.Add(int64(n))
				sess.info.LastActive.Store(time.Now().UnixNano())
			}
			srvConn := sess.serverConn
			sessMu.Unlock()
			srvConn.Write(buf[:n])
		}
	}()
	return nil
}

// ============ 工具 ============

// splitHostPort 把 "ip:port" 或 "ip:port-port" 拆成两段
func splitHostPort(s string) (host, port string) {
	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return s, ""
	}
	return s[:idx], s[idx+1:]
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func humanBytes(b int64) string {
	const (
		KB = 1024
		MB = 1024 * 1024
		GB = 1024 * 1024 * 1024
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func humanRate(bps float64) string {
	const (
		KB = 1024.0
		MB = 1024.0 * 1024.0
		GB = 1024.0 * 1024.0 * 1024.0
	)
	switch {
	case bps >= GB:
		return fmt.Sprintf("%.2f GB/s", bps/GB)
	case bps >= MB:
		return fmt.Sprintf("%.2f MB/s", bps/MB)
	case bps >= KB:
		return fmt.Sprintf("%.2f KB/s", bps/KB)
	default:
		return fmt.Sprintf("%.0f B/s", bps)
	}
}
