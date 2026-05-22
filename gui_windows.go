//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// ============ 规则表 Model ============

type RuleModel struct {
	walk.TableModelBase
	items []Rule
}

func (m *RuleModel) RowCount() int { return len(m.items) }

func (m *RuleModel) Value(row, col int) interface{} {
	r := m.items[row]
	switch col {
	case 0:
		return r.Listen
	case 1:
		return r.Target
	case 2:
		return r.Proto
	}
	return ""
}

func (m *RuleModel) Add(r Rule) {
	m.items = append(m.items, r)
	m.PublishRowsReset()
}

func (m *RuleModel) Delete(idx int) {
	if idx < 0 || idx >= len(m.items) {
		return
	}
	m.items = append(m.items[:idx], m.items[idx+1:]...)
	m.PublishRowsReset()
}

func (m *RuleModel) Update(idx int, r Rule) {
	if idx < 0 || idx >= len(m.items) {
		return
	}
	m.items[idx] = r
	m.PublishRowsReset()
}

// ============ 会话表 Model ============

type SessionTableModel struct {
	walk.TableModelBase
	items []SessionSnapshot
}

func (m *SessionTableModel) RowCount() int { return len(m.items) }

func (m *SessionTableModel) Value(row, col int) interface{} {
	s := m.items[row]
	switch col {
	case 0:
		return s.ID
	case 1:
		return s.Proto
	case 2:
		return s.Client
	case 3:
		return s.Listen
	case 4:
		return s.Target
	case 5:
		return humanBytes(s.BytesIn)
	case 6:
		return humanBytes(s.BytesOut)
	case 7:
		return humanRate(s.InRate)
	case 8:
		return humanRate(s.OutRate)
	case 9:
		return formatDuration(time.Since(s.StartedAt))
	case 10:
		return formatDuration(time.Since(s.LastActive))
	}
	return ""
}

func (m *SessionTableModel) SetItems(items []SessionSnapshot) {
	// 按 ID 升序，避免每次刷新顺序乱跳
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j-1].ID > items[j].ID; j-- {
			items[j-1], items[j] = items[j], items[j-1]
		}
	}
	m.items = items
	m.PublishRowsReset()
}

// ============ 添加/编辑规则对话框 ============

func runRuleDialog(owner walk.Form, title string, initial Rule) (Rule, bool) {
	var dlg *walk.Dialog
	var listenIPEdit, listenPortEdit, targetIPEdit, targetPortEdit *walk.LineEdit
	var protoCombo *walk.ComboBox
	var okBtn, cancelBtn *walk.PushButton

	result := initial
	if result.Proto == "" {
		result.Proto = "both"
	}
	listenIP, listenPort := splitHostPort(result.Listen)
	targetIP, targetPort := splitHostPort(result.Target)
	accepted := false

	err := Dialog{
		AssignTo:      &dlg,
		Title:         title,
		DefaultButton: &okBtn,
		CancelButton:  &cancelBtn,
		MinSize:       Size{Width: 500, Height: 240},
		Layout:        VBox{},
		Children: []Widget{
			Composite{
				Layout: Grid{Columns: 4},
				Children: []Widget{
					Label{Text: "监听 IP:"},
					LineEdit{AssignTo: &listenIPEdit, Text: listenIP, CueBanner: "0.0.0.0"},
					Label{Text: "端口:"},
					LineEdit{AssignTo: &listenPortEdit, Text: listenPort, CueBanner: "19100 或 19100-19119"},

					Label{Text: "目标 IP:"},
					LineEdit{AssignTo: &targetIPEdit, Text: targetIP, CueBanner: "192.200.190.41"},
					Label{Text: "端口:"},
					LineEdit{AssignTo: &targetPortEdit, Text: targetPort, CueBanner: "19100 或 19100-19119"},

					Label{Text: "协议:"},
					ComboBox{
						AssignTo:   &protoCombo,
						Model:      []string{"both", "tcp", "udp"},
						Value:      result.Proto,
						ColumnSpan: 3,
					},
					Label{Text: "", ColumnSpan: 1},
					Label{Text: "端口可写范围，如 19100-19119；两侧范围长度需相等，或目标端口为单端口", ColumnSpan: 3},
				},
			},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &okBtn, Text: "确定", OnClicked: func() {
						lIP := strings.TrimSpace(listenIPEdit.Text())
						lPort := strings.TrimSpace(listenPortEdit.Text())
						tIP := strings.TrimSpace(targetIPEdit.Text())
						tPort := strings.TrimSpace(targetPortEdit.Text())
						if lIP == "" || lPort == "" || tIP == "" || tPort == "" {
							walk.MsgBox(dlg, "提示", "IP 和端口都不能为空", walk.MsgBoxIconWarning)
							return
						}
						result.Listen = lIP + ":" + lPort
						result.Target = tIP + ":" + tPort
						result.Proto = protoCombo.Text()
						if result.Proto == "" {
							result.Proto = "both"
						}
						if _, err := parseAddrSpec(result.Listen); err != nil {
							walk.MsgBox(dlg, "提示", "监听端口: "+err.Error(), walk.MsgBoxIconWarning)
							return
						}
						if _, err := parseAddrSpec(result.Target); err != nil {
							walk.MsgBox(dlg, "提示", "目标端口: "+err.Error(), walk.MsgBoxIconWarning)
							return
						}
						accepted = true
						dlg.Accept()
					}},
					PushButton{AssignTo: &cancelBtn, Text: "取消", OnClicked: func() {
						dlg.Cancel()
					}},
				},
			},
		},
	}.Create(owner)
	if err != nil {
		return initial, false
	}
	dlg.Run()
	return result, accepted
}

// ============ GUI 主循环 ============

func runGUI() {
	var mw *walk.MainWindow
	var rulesTable *walk.TableView
	var sessionTable *walk.TableView
	var logEdit *walk.TextEdit
	var startBtn, stopBtn *walk.PushButton
	var lblTcp, lblUdp, lblIn, lblOut, lblInRate, lblOutRate *walk.Label

	model := &RuleModel{}
	sessionModel := &SessionTableModel{}
	fwd := NewForwarder(nil)
	_ = sessionTable

	// 加载已有配置
	loadedCfg := Config{UDPBuffer: defaultUDPBuffer, UDPTimeout: defaultUDPTimeout}
	if data, err := os.ReadFile(configFileName); err == nil {
		var c Config
		if err := json.Unmarshal(data, &c); err == nil {
			if c.UDPBuffer > 0 {
				loadedCfg.UDPBuffer = c.UDPBuffer
			}
			if c.UDPTimeout > 0 {
				loadedCfg.UDPTimeout = c.UDPTimeout
			}
			model.items = append(model.items, c.Rules...)
		}
	}

	uiDone := make(chan struct{})
	uiClosed := atomic.Bool{}

	const (
		logHardCap = 64 * 1024
		logKeep    = 48 * 1024
	)
	logAppendCount := 0
	appendLog := func(s string) {
		if mw == nil || uiClosed.Load() {
			return
		}
		mw.Synchronize(func() {
			if logEdit == nil {
				return
			}
			ts := time.Now().Format("15:04:05")
			logEdit.AppendText(fmt.Sprintf("[%s] %s\r\n", ts, s))
			logAppendCount++
			if logAppendCount >= 50 {
				logAppendCount = 0
				cur := logEdit.Text()
				if len(cur) > logHardCap {
					trim := len(cur) - logKeep
					if idx := strings.Index(cur[trim:], "\r\n"); idx >= 0 {
						trim += idx + 2
					}
					logEdit.SetText(cur[trim:])
				}
			}
		})
	}
	fwd.onLog = appendLog

	err := MainWindow{
		AssignTo: &mw,
		Title:    "Forward",
		Size:     Size{Width: 980, Height: 680},
		MinSize:  Size{Width: 700, Height: 480},
		Layout:   VBox{},
		Children: []Widget{
			Composite{
				Layout: HBox{},
				Children: []Widget{
					PushButton{
						AssignTo: &startBtn,
						Text:     "启动",
						OnClicked: func() {
							if model.RowCount() == 0 {
								walk.MsgBox(mw, "提示", "请先添加至少一条转发规则", walk.MsgBoxIconWarning)
								return
							}
							cfg := Config{
								Rules:      append([]Rule(nil), model.items...),
								UDPBuffer:  loadedCfg.UDPBuffer,
								UDPTimeout: loadedCfg.UDPTimeout,
							}
							if err := fwd.Start(cfg); err != nil {
								walk.MsgBox(mw, "错误", err.Error(), walk.MsgBoxIconError)
								return
							}
							startBtn.SetEnabled(false)
							stopBtn.SetEnabled(true)
						},
					},
					PushButton{
						AssignTo: &stopBtn,
						Text:     "停止",
						Enabled:  false,
						OnClicked: func() {
							fwd.Stop()
							startBtn.SetEnabled(true)
							stopBtn.SetEnabled(false)
						},
					},
					VSeparator{},
					PushButton{
						Text: "添加规则",
						OnClicked: func() {
							if fwd.Running() {
								walk.MsgBox(mw, "提示", "请先停止再修改规则", walk.MsgBoxIconWarning)
								return
							}
							if r, ok := runRuleDialog(mw, "添加规则", Rule{Proto: "both"}); ok {
								model.Add(r)
							}
						},
					},
					PushButton{
						Text: "编辑规则",
						OnClicked: func() {
							if fwd.Running() {
								walk.MsgBox(mw, "提示", "请先停止再修改规则", walk.MsgBoxIconWarning)
								return
							}
							idx := rulesTable.CurrentIndex()
							if idx < 0 {
								walk.MsgBox(mw, "提示", "请选中一条规则", walk.MsgBoxIconInformation)
								return
							}
							if r, ok := runRuleDialog(mw, "编辑规则", model.items[idx]); ok {
								model.Update(idx, r)
							}
						},
					},
					PushButton{
						Text: "删除规则",
						OnClicked: func() {
							if fwd.Running() {
								walk.MsgBox(mw, "提示", "请先停止再修改规则", walk.MsgBoxIconWarning)
								return
							}
							idx := rulesTable.CurrentIndex()
							if idx < 0 {
								return
							}
							model.Delete(idx)
						},
					},
					VSeparator{},
					PushButton{
						Text: "保存配置",
						OnClicked: func() {
							cfg := Config{
								Rules:      model.items,
								UDPBuffer:  loadedCfg.UDPBuffer,
								UDPTimeout: loadedCfg.UDPTimeout,
							}
							data, _ := json.MarshalIndent(cfg, "", "  ")
							if err := os.WriteFile(configFileName, data, 0644); err != nil {
								walk.MsgBox(mw, "错误", err.Error(), walk.MsgBoxIconError)
							} else {
								appendLog(fmt.Sprintf("已保存配置到 %s", configFileName))
							}
						},
					},
					PushButton{
						Text: "清空日志",
						OnClicked: func() {
							logEdit.SetText("")
						},
					},
					HSpacer{},
				},
			},
			GroupBox{
				Title:  "转发规则",
				Layout: VBox{},
				Children: []Widget{
					TableView{
						AssignTo:         &rulesTable,
						AlternatingRowBG: true,
						ColumnsOrderable: false,
						Model:            model,
						Columns: []TableViewColumn{
							{Title: "监听地址", Width: 220},
							{Title: "目标地址", Width: 240},
							{Title: "协议", Width: 80},
						},
					},
				},
			},
			GroupBox{
				Title:  "运行统计",
				Layout: VBox{},
				Children: []Widget{
					Composite{
						Layout: HBox{},
						Children: []Widget{
							Label{Text: "TCP连接:"},
							Label{AssignTo: &lblTcp, Text: "0", MinSize: Size{Width: 50}},
							VSeparator{},
							Label{Text: "UDP会话:"},
							Label{AssignTo: &lblUdp, Text: "0", MinSize: Size{Width: 50}},
							VSeparator{},
							Label{Text: "入流量:"},
							Label{AssignTo: &lblIn, Text: "0 B", MinSize: Size{Width: 90}},
							Label{Text: "@"},
							Label{AssignTo: &lblInRate, Text: "0 B/s", MinSize: Size{Width: 90}},
							VSeparator{},
							Label{Text: "出流量:"},
							Label{AssignTo: &lblOut, Text: "0 B", MinSize: Size{Width: 90}},
							Label{Text: "@"},
							Label{AssignTo: &lblOutRate, Text: "0 B/s", MinSize: Size{Width: 90}},
							HSpacer{},
						},
					},
					TableView{
						AssignTo:         &sessionTable,
						AlternatingRowBG: true,
						ColumnsOrderable: true,
						Model:            sessionModel,
						MinSize:          Size{Height: 140},
						Columns: []TableViewColumn{
							{Title: "ID", Width: 50},
							{Title: "协议", Width: 50},
							{Title: "客户端", Width: 140},
							{Title: "监听", Width: 130},
							{Title: "目标", Width: 140},
							{Title: "入字节", Width: 85},
							{Title: "出字节", Width: 85},
							{Title: "入速率", Width: 90},
							{Title: "出速率", Width: 90},
							{Title: "持续", Width: 60},
							{Title: "最近活跃", Width: 80},
						},
					},
				},
			},
			GroupBox{
				Title:  "日志",
				Layout: VBox{},
				Children: []Widget{
					TextEdit{
						AssignTo: &logEdit,
						ReadOnly: true,
						VScroll:  true,
						MinSize:  Size{Height: 120},
					},
				},
			},
		},
	}.Create()
	if err != nil {
		fmt.Println("创建主窗口失败:", err)
		return
	}

	// 定时刷新统计
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		var prevSampleTime time.Time
		var prevIn, prevOut int64
		prevSessBytes := make(map[int64][2]int64)
		for {
			select {
			case <-uiDone:
				return
			case <-ticker.C:
				if uiClosed.Load() {
					return
				}
				tcp := fwd.tcpConns.Load()
				udp := fwd.udpSessions.Load()
				in := fwd.bytesIn.Load()
				out := fwd.bytesOut.Load()
				sessions := fwd.snapshotSessions()

				now := time.Now()
				var inRate, outRate float64
				if !prevSampleTime.IsZero() {
					elapsed := now.Sub(prevSampleTime).Seconds()
					if elapsed > 0 {
						inRate = float64(in-prevIn) / elapsed
						outRate = float64(out-prevOut) / elapsed
						newPrevSess := make(map[int64][2]int64, len(sessions))
						for i := range sessions {
							s := &sessions[i]
							if prev, ok := prevSessBytes[s.ID]; ok {
								s.InRate = float64(s.BytesIn-prev[0]) / elapsed
								s.OutRate = float64(s.BytesOut-prev[1]) / elapsed
							}
							newPrevSess[s.ID] = [2]int64{s.BytesIn, s.BytesOut}
						}
						prevSessBytes = newPrevSess
					}
				} else {
					newPrevSess := make(map[int64][2]int64, len(sessions))
					for _, s := range sessions {
						newPrevSess[s.ID] = [2]int64{s.BytesIn, s.BytesOut}
					}
					prevSessBytes = newPrevSess
				}
				prevSampleTime = now
				prevIn = in
				prevOut = out

				mw.Synchronize(func() {
					lblTcp.SetText(fmt.Sprintf("%d", tcp))
					lblUdp.SetText(fmt.Sprintf("%d", udp))
					lblIn.SetText(humanBytes(in))
					lblOut.SetText(humanBytes(out))
					lblInRate.SetText(humanRate(inRate))
					lblOutRate.SetText(humanRate(outRate))
					sessionModel.SetItems(sessions)
				})
			}
		}
	}()

	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		uiClosed.Store(true)
		close(uiDone)
		fwd.Stop()
	})

	appendLog("程序就绪。点击 \"启动\" 开始转发。")
	mw.Run()
}
