package main

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func startTestProbe(t *testing.T, cfg ProbeConfig) (*Probe, string) {
	t.Helper()
	p := NewProbe(nil)
	if err := p.Start(cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Stop)
	p.closeMu.Lock()
	defer p.closeMu.Unlock()
	for c := range p.closers {
		switch c := c.(type) {
		case net.Listener:
			return p, c.Addr().String()
		case *net.UDPConn:
			return p, c.LocalAddr().String()
		}
	}
	return p, ""
}

func waitProbe(t *testing.T, p *Probe) {
	t.Helper()
	select {
	case <-p.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("测试未自动结束")
	}
	if p.Running() {
		t.Fatal("结束后仍处于运行状态")
	}
}

func waitCondition(t *testing.T, condition func() bool) {
	t.Helper()
	end := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(end) {
			t.Fatal("等待状态变化超时")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBandwidthTransferAndReuse(t *testing.T) {
	receiver, addr := startTestProbe(t, ProbeConfig{Mode: ProbeListen, Proto: "tcp", Addr: "127.0.0.1:0", Bandwidth: true})
	sender := NewProbe(nil)
	t.Cleanup(sender.Stop)
	for i := 0; i < 2; i++ {
		if err := sender.Start(ProbeConfig{Mode: ProbeSend, Proto: "tcp", Addr: addr, Bandwidth: true, Duration: 100 * time.Millisecond}); err != nil {
			t.Fatal(err)
		}
		waitProbe(t, sender)
		s := sender.BandwidthSnapshot()
		if s.Error != "" || s.Status != "已完成" || s.PeerReceived <= 0 || s.Sent != s.PeerReceived {
			t.Fatalf("发送结果无效: %+v", s)
		}
		if s.PeerElapsed <= 0 || s.Elapsed < 100*time.Millisecond {
			t.Fatalf("测速时长无效: %+v", s)
		}
		waitCondition(t, func() bool { return !receiver.bandwidthBusy.Load() })
		r := receiver.BandwidthSnapshot()
		if r.Error != "" || r.Received != s.Sent || r.Active {
			t.Fatalf("接收结果无效: %+v", r)
		}
		if !receiver.Running() {
			t.Fatal("监听端应继续等待下一次测试")
		}
		if r.Sent != 0 || s.Received != 0 {
			t.Fatal("握手及结果报文不能计入载荷流量")
		}
		time.Sleep(5 * time.Millisecond)
		if sender.BandwidthSnapshot().Elapsed != s.Elapsed {
			t.Fatal("完成后平均速率的时间基准仍在变化")
		}
	}
}

func TestBandwidthThroughForwarder(t *testing.T) {
	_, addr := startTestProbe(t, ProbeConfig{Mode: ProbeListen, Proto: "tcp", Addr: "127.0.0.1:0", Bandwidth: true})
	// 直接启动 TCP 核心，使用系统分配端口，避免预占端口后再释放的竞争。
	f := NewForwarder(nil)
	if err := f.Start(Config{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Stop)
	if err := f.startTCP(Rule{Listen: "127.0.0.1:0", Target: addr, Proto: "tcp"}, compiledACL{}); err != nil {
		t.Fatal(err)
	}
	forwardAddr := f.listeners[0].Addr().String()
	p, _ := startTestProbe(t, ProbeConfig{Mode: ProbeSend, Proto: "tcp", Addr: forwardAddr, Bandwidth: true, Duration: 100 * time.Millisecond})
	waitProbe(t, p)
	s := p.BandwidthSnapshot()
	if s.Error != "" || s.PeerReceived != s.Sent || s.Sent == 0 {
		t.Fatalf("经过转发器测速失败: %+v", s)
	}
}

func TestBandwidthStopBlockedSender(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ready := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var hello [len(bandwidthHello)]byte
		if _, err := io.ReadFull(c, hello[:]); err != nil {
			return
		}
		io.WriteString(c, bandwidthReady)
		close(ready)
		<-release // 完成握手但不读取载荷，制造阻塞写入。
	}()
	p, _ := startTestProbe(t, ProbeConfig{Mode: ProbeSend, Proto: "tcp", Addr: ln.Addr().String(), Bandwidth: true, Duration: time.Hour})
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("握手超时")
	}
	waitCondition(t, func() bool { return p.Sent() > 0 })
	stopped := make(chan struct{})
	go func() { p.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("停止阻塞发送超时")
	}
	if s := p.BandwidthSnapshot(); s.Status != "已停止" || s.Active {
		t.Fatalf("停止后状态不正确: %+v", s)
	}
}

func TestProbeStopDuringAccept(t *testing.T) {
	// 覆盖 Accept 刚返回、连接尚未加入跟踪表时停止的竞争。
	p := NewProbe(nil)
	for i := 0; i < 20; i++ {
		if err := p.Start(ProbeConfig{Mode: ProbeListen, Proto: "tcp", Addr: "127.0.0.1:0", Bandwidth: true}); err != nil {
			t.Fatal(err)
		}
		p.closeMu.Lock()
		var addr string
		for c := range p.closers {
			if ln, ok := c.(net.Listener); ok {
				addr = ln.Addr().String()
			}
		}
		p.closeMu.Unlock()
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { p.Stop(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			c.Close()
			t.Fatal("停止监听超时")
		}
		c.Close()
	}
}

func TestBandwidthInvalidAndInterrupted(t *testing.T) {
	p := NewProbe(nil)
	for _, cfg := range []ProbeConfig{
		{Mode: ProbeSend, Proto: "udp", Bandwidth: true},
		{Mode: ProbeSend, Proto: "tcp", Bandwidth: true, Duration: -time.Second},
		{Mode: ProbeSend, Proto: "tcp", Bandwidth: true, Duration: 2 * time.Hour},
	} {
		if err := p.Start(cfg); err == nil {
			p.Stop()
			t.Fatal("无效参数应被拒绝")
		}
	}
	receiver, addr := startTestProbe(t, ProbeConfig{Mode: ProbeListen, Proto: "tcp", Addr: "127.0.0.1:0", Bandwidth: true})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(time.Second))
	io.WriteString(c, bandwidthHello)
	var hello [len(bandwidthHello)]byte
	if _, err := io.ReadFull(c, hello[:]); err != nil {
		t.Fatal(err)
	}
	binary.Write(c, binary.BigEndian, uint32(bandwidthBlockSize))
	c.Write([]byte("partial"))
	c.Close()
	waitCondition(t, func() bool { return receiver.BandwidthSnapshot().Error != "" })
	s := receiver.BandwidthSnapshot()
	if s.Status != "失败" || s.Active {
		t.Fatalf("中断被误报为完成: %+v", s)
	}
}

func TestBandwidthWrongPeer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		io.WriteString(c, "NOT-FWBW")
	}()
	p, _ := startTestProbe(t, ProbeConfig{Mode: ProbeSend, Proto: "tcp", Addr: ln.Addr().String(), Bandwidth: true, Duration: 100 * time.Millisecond})
	waitProbe(t, p)
	wg.Wait()
	if s := p.BandwidthSnapshot(); s.Error == "" || s.PeerReceived != 0 {
		t.Fatalf("错误对端应失败: %+v", s)
	}
}

func TestProbeEchoRegression(t *testing.T) {
	for _, proto := range []string{"tcp", "udp"} {
		t.Run(proto, func(t *testing.T) {
			p, addr := startTestProbe(t, ProbeConfig{Mode: ProbeListen, Proto: proto, Addr: "127.0.0.1:0", Echo: true})
			c, err := net.Dial(proto, addr)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(time.Second))
			payload := []byte("normal-probe")
			if _, err := c.Write(payload); err != nil {
				t.Fatal(err)
			}
			result := make([]byte, len(payload))
			if _, err := io.ReadFull(c, result); err != nil {
				t.Fatal(err)
			}
			if string(result) != string(payload) {
				t.Fatalf("echo 不一致: %q", result)
			}
			waitCondition(t, func() bool { return p.Sent() == int64(len(payload)) && p.Recv() == int64(len(payload)) })
		})
	}
}
