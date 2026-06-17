//go:build windows

//go:generate go run ./tools/genicon -o forward.ico

package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

//go:embed forward.ico
var iconBytes []byte

// loadAppIcon 把 embed 的 ico 释放到临时文件再加载成 walk.Icon。
// walk 老版本只支持从文件路径加载图标。
func loadAppIcon() *walk.Icon {
	p := filepath.Join(os.TempDir(), "forward_app.ico")
	if err := os.WriteFile(p, iconBytes, 0644); err != nil {
		return nil
	}
	icon, err := walk.NewIconFromFile(p)
	if err != nil {
		return nil
	}
	return icon
}

// ============ 规则表 Model ============

type RuleModel struct {
	walk.TableModelBase
	items    []Rule
	onChange func()
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

func (m *RuleModel) notify() {
	if m.onChange != nil {
		m.onChange()
	}
}

func (m *RuleModel) Add(r Rule) {
	m.items = append(m.items, r)
	m.PublishRowsReset()
	m.notify()
}

func (m *RuleModel) Delete(idx int) {
	if idx < 0 || idx >= len(m.items) {
		return
	}
	m.items = append(m.items[:idx], m.items[idx+1:]...)
	m.PublishRowsReset()
	m.notify()
}

func (m *RuleModel) Update(idx int, r Rule) {
	if idx < 0 || idx >= len(m.items) {
		return
	}
	m.items[idx] = r
	m.PublishRowsReset()
	m.notify()
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
	var allowEdit, denyEdit *walk.TextEdit
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
		MinSize:       Size{Width: 460, Height: 360},
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
			GroupBox{
				Title:  "ACL（每行一个 IP 或 CIDR，留空表示不限制）",
				Layout: Grid{Columns: 2},
				Children: []Widget{
					Label{Text: "白名单(allow):"},
					Label{Text: "黑名单(deny):"},
					TextEdit{
						AssignTo: &allowEdit,
						Text:     strings.Join(result.Allow, "\r\n"),
						VScroll:  true,
						MinSize:  Size{Height: 80},
					},
					TextEdit{
						AssignTo: &denyEdit,
						Text:     strings.Join(result.Deny, "\r\n"),
						VScroll:  true,
						MinSize:  Size{Height: 80},
					},
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
						result.Allow = parseACLLines(allowEdit.Text())
						result.Deny = parseACLLines(denyEdit.Text())
						if _, err := parseAddrSpec(result.Listen); err != nil {
							walk.MsgBox(dlg, "提示", "监听端口: "+err.Error(), walk.MsgBoxIconWarning)
							return
						}
						if _, err := parseAddrSpec(result.Target); err != nil {
							walk.MsgBox(dlg, "提示", "目标端口: "+err.Error(), walk.MsgBoxIconWarning)
							return
						}
						if _, err := parseACLList(result.Allow); err != nil {
							walk.MsgBox(dlg, "提示", "白名单: "+err.Error(), walk.MsgBoxIconWarning)
							return
						}
						if _, err := parseACLList(result.Deny); err != nil {
							walk.MsgBox(dlg, "提示", "黑名单: "+err.Error(), walk.MsgBoxIconWarning)
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

// parseACLLines 把多行文本拆为非空字符串列表(去掉每行首尾空白)
func parseACLLines(text string) []string {
	out := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// runGlobalACLDialog 编辑全局 ACL(defaultPolicy + 全局 allow/deny)
func runGlobalACLDialog(owner walk.Form, cfg *Config) bool {
	var dlg *walk.Dialog
	var policyCombo *walk.ComboBox
	var allowEdit, denyEdit *walk.TextEdit
	var okBtn, cancelBtn *walk.PushButton

	initPolicy := strings.ToLower(strings.TrimSpace(cfg.DefaultPolicy))
	if initPolicy != "deny" {
		initPolicy = "allow"
	}
	accepted := false

	err := Dialog{
		AssignTo:      &dlg,
		Title:         "全局黑/白名单",
		DefaultButton: &okBtn,
		CancelButton:  &cancelBtn,
		MinSize:       Size{Width: 520, Height: 520},
		Layout:        VBox{},
		Children: []Widget{
			Composite{
				Layout: HBox{},
				Children: []Widget{
					Label{Text: "默认策略:"},
					ComboBox{
						AssignTo: &policyCombo,
						Model:    []string{"allow", "deny"},
						Value:    initPolicy,
						MinSize:  Size{Width: 100},
					},
					HSpacer{},
				},
			},
			GroupBox{
				Title:  "默认策略含义",
				Layout: VBox{},
				Children: []Widget{
					Label{Text: "默认策略 = allow(默认放行):"},
					Label{Text: "    没明确写在 deny 名单里的客户端都能通过。"},
					Label{Text: "    场景:对外开放的服务，只拉黑少数恶意 IP。"},
					Label{Text: ""},
					Label{Text: "默认策略 = deny(默认拒绝):"},
					Label{Text: "    任何客户端默认都被拒,必须命中 allow 才能通过。"},
					Label{Text: "    场景:仅限内部访问,白名单准入。"},
					Label{Text: ""},
					Label{Text: "判定顺序(命中即返):"},
					Label{Text: "    1) 规则级 deny 或 全局 deny 命中 → 拒绝"},
					Label{Text: "    2) 任一级存在非空 allow → 必须命中,否则拒绝"},
					Label{Text: "    3) 否则按上面的默认策略"},
					Label{Text: ""},
					Label{Text: "提示:每条转发规则有自己独立的 allow/deny,在规则\"编辑\"里设置。"},
				},
			},
			GroupBox{
				Title:  "全局白名单（每行一个 IP 或 CIDR）",
				Layout: VBox{},
				Children: []Widget{
					TextEdit{
						AssignTo: &allowEdit,
						Text:     strings.Join(cfg.Allow, "\r\n"),
						VScroll:  true,
						MinSize:  Size{Height: 120},
					},
				},
			},
			GroupBox{
				Title:  "全局黑名单（每行一个 IP 或 CIDR）",
				Layout: VBox{},
				Children: []Widget{
					TextEdit{
						AssignTo: &denyEdit,
						Text:     strings.Join(cfg.Deny, "\r\n"),
						VScroll:  true,
						MinSize:  Size{Height: 120},
					},
				},
			},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &okBtn, Text: "确定", OnClicked: func() {
						newAllow := parseACLLines(allowEdit.Text())
						newDeny := parseACLLines(denyEdit.Text())
						if _, err := parseACLList(newAllow); err != nil {
							walk.MsgBox(dlg, "提示", "白名单: "+err.Error(), walk.MsgBoxIconWarning)
							return
						}
						if _, err := parseACLList(newDeny); err != nil {
							walk.MsgBox(dlg, "提示", "黑名单: "+err.Error(), walk.MsgBoxIconWarning)
							return
						}
						cfg.DefaultPolicy = policyCombo.Text()
						cfg.Allow = newAllow
						cfg.Deny = newDeny
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
		return false
	}
	dlg.Run()
	return accepted
}

// ============ 接口测试对话框 ============
//
// 单例对话框:用户打开后,可以在里面持续启动/停止 监听 / 发送,实时看日志。
// 两台机器:一台开 "监听"(可选 echo),另一台开 "发送" 指向监听端,
// 中间穿过 forward 转发链,就能验证整条链路通不通。

func runProbeDialog(owner walk.Form) {
	var dlg *walk.Dialog
	var modeListenRadio, modeSendRadio *walk.RadioButton
	var protoCombo *walk.ComboBox
	var addrEdit *walk.LineEdit
	var payloadEdit *walk.LineEdit
	var echoCheck, repeatCheck *walk.CheckBox
	var intervalEdit *walk.LineEdit
	var startBtn, stopBtn, closeBtn *walk.PushButton
	var statLbl *walk.Label
	var logEdit *walk.TextEdit

	appendLog := func(s string) {
		if dlg == nil || logEdit == nil {
			return
		}
		dlg.Synchronize(func() {
			if logEdit == nil {
				return
			}
			ts := time.Now().Format("15:04:05")
			logEdit.AppendText(fmt.Sprintf("[%s] %s\r\n", ts, s))
			cur := logEdit.Text()
			if len(cur) > 32*1024 {
				logEdit.SetText(cur[len(cur)-24*1024:])
			}
		})
	}

	probe := NewProbe(appendLog)

	updateUI := func() {
		isListen := modeListenRadio.Checked()
		echoCheck.SetEnabled(isListen)
		payloadEdit.SetEnabled(!isListen)
		repeatCheck.SetEnabled(!isListen)
		intervalEdit.SetEnabled(!isListen && repeatCheck.Checked())

		running := probe.Running()
		startBtn.SetEnabled(!running)
		stopBtn.SetEnabled(running)
		modeListenRadio.SetEnabled(!running)
		modeSendRadio.SetEnabled(!running)
		protoCombo.SetEnabled(!running)
		addrEdit.SetEnabled(!running)
		if running {
			echoCheck.SetEnabled(false)
			payloadEdit.SetEnabled(false)
			repeatCheck.SetEnabled(false)
			intervalEdit.SetEnabled(false)
		}
	}

	statTicker := make(chan struct{})

	err := Dialog{
		AssignTo:      &dlg,
		Title:         "接口测试",
		DefaultButton: &startBtn,
		CancelButton:  &closeBtn,
		MinSize:       Size{Width: 560, Height: 480},
		Layout:        VBox{},
		Children: []Widget{
			GroupBox{
				Title:  "模式",
				Layout: HBox{},
				Children: []Widget{
					RadioButton{
						AssignTo:  &modeListenRadio,
						Text:      "监听 (本机收包)",
						Value:     true,
						OnClicked: func() { updateUI() },
					},
					RadioButton{
						AssignTo:  &modeSendRadio,
						Text:      "发送 (主动发包)",
						OnClicked: func() { updateUI() },
					},
					HSpacer{},
				},
			},
			Composite{
				Layout: Grid{Columns: 4},
				Children: []Widget{
					Label{Text: "协议:"},
					ComboBox{
						AssignTo: &protoCombo,
						Model:    []string{"tcp", "udp"},
						Value:    "tcp",
						MinSize:  Size{Width: 80},
					},
					Label{Text: "地址:"},
					LineEdit{
						AssignTo:  &addrEdit,
						Text:      "0.0.0.0:9999",
						CueBanner: "监听: 0.0.0.0:9999  发送: 1.2.3.4:9999",
					},

					Label{Text: "发送内容:"},
					LineEdit{
						AssignTo:   &payloadEdit,
						Text:       "ping",
						CueBanner:  "要发送的字符串",
						ColumnSpan: 3,
					},

					Label{Text: ""},
					CheckBox{
						AssignTo: &echoCheck,
						Text:     "收到后回 echo",
					},
					CheckBox{
						AssignTo:         &repeatCheck,
						Text:             "持续发送,间隔",
						OnCheckedChanged: func() { updateUI() },
					},
					LineEdit{
						AssignTo: &intervalEdit,
						Text:     "1",
						MinSize:  Size{Width: 60},
					},
				},
			},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					PushButton{AssignTo: &startBtn, Text: "开始", OnClicked: func() {
						cfg := ProbeConfig{
							Proto: protoCombo.Text(),
							Addr:  strings.TrimSpace(addrEdit.Text()),
						}
						if cfg.Addr == "" {
							walk.MsgBox(dlg, "提示", "地址不能为空", walk.MsgBoxIconWarning)
							return
						}
						if modeListenRadio.Checked() {
							cfg.Mode = ProbeListen
							cfg.Echo = echoCheck.Checked()
						} else {
							cfg.Mode = ProbeSend
							cfg.Payload = []byte(payloadEdit.Text())
							if len(cfg.Payload) == 0 {
								walk.MsgBox(dlg, "提示", "发送内容不能为空", walk.MsgBoxIconWarning)
								return
							}
							if repeatCheck.Checked() {
								sec, _ := strconv.Atoi(strings.TrimSpace(intervalEdit.Text()))
								if sec < 1 {
									sec = 1
								}
								cfg.Interval = time.Duration(sec) * time.Second
							}
						}
						if err := probe.Start(cfg); err != nil {
							walk.MsgBox(dlg, "错误", err.Error(), walk.MsgBoxIconError)
							return
						}
						updateUI()
					}},
					PushButton{AssignTo: &stopBtn, Text: "停止", Enabled: false, OnClicked: func() {
						stopBtn.SetEnabled(false)
						stopBtn.SetText("停止中...")
						go func() {
							probe.Stop()
							dlg.Synchronize(func() {
								stopBtn.SetText("停止")
								updateUI()
							})
						}()
					}},
					Label{AssignTo: &statLbl, Text: "已发送: 0 B   已接收: 0 B"},
					HSpacer{},
					PushButton{Text: "清空日志", OnClicked: func() {
						logEdit.SetText("")
					}},
					PushButton{AssignTo: &closeBtn, Text: "关闭", OnClicked: func() {
						dlg.Cancel()
					}},
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
						MinSize:  Size{Height: 180},
					},
				},
			},
		},
	}.Create(owner)
	if err != nil {
		return
	}

	// 定时刷新统计
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-statTicker:
				return
			case <-ticker.C:
				sent := probe.Sent()
				recv := probe.Recv()
				dlg.Synchronize(func() {
					if statLbl != nil {
						statLbl.SetText(fmt.Sprintf("已发送: %s   已接收: %s", humanBytes(sent), humanBytes(recv)))
					}
				})
			}
		}
	}()

	updateUI()
	appendLog("接口测试就绪。先选模式 -> 填地址 -> 点 \"开始\"。")
	appendLog("两台机器一台监听 / 一台发送,中间穿 forward 转发,可验证链路。")

	dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		close(statTicker)
		probe.Stop()
	})
	dlg.Run()
}

// ============ GUI 主循环 ============

func runGUI() {
	var mw *walk.MainWindow
	var rulesTable *walk.TableView
	var sessionTable *walk.TableView
	var logEdit *walk.TextEdit
	var startBtn, stopBtn *walk.PushButton
	var lblTcp, lblUdp, lblIn, lblOut, lblInRate, lblOutRate, lblDenied *walk.Label
	var lblStatus, lblRules *walk.Label

	// Probe tab 的控件
	var probeModeListen, probeModeSend *walk.RadioButton
	var probeProto *walk.ComboBox
	var probeAddr, probePayload, probeInterval *walk.LineEdit
	var probeEcho, probeRepeat *walk.CheckBox
	var probeStartBtn, probeStopBtn *walk.PushButton
	var probeStat *walk.Label
	var probe *Probe

	// updateProbeUI 的前向声明,setStatus 内部会调用它(fwd 启动/停止时同步)
	var updateProbeUI func()

	// Tab 切换拦截
	var tabWidget *walk.TabWidget
	const probeTabIndex = 2 // 第 3 个 tab(0=规则, 1=会话, 2=测试)

	// 颜色规范
	colorRunning := walk.RGB(0x2f, 0xa0, 0x4d) // 绿
	colorStopped := walk.RGB(0x80, 0x80, 0x80) // 灰
	colorAccent := walk.RGB(0x1f, 0x6f, 0xeb)  // 蓝(数字)
	colorWarn := walk.RGB(0xc4, 0x3a, 0x3a)    // 红(拒绝)
	statFont := Font{Bold: true, PointSize: 10}

	setStatus := func(running bool) {
		if lblStatus != nil {
			if running {
				lblStatus.SetText("● 运行中")
				lblStatus.SetTextColor(colorRunning)
			} else {
				lblStatus.SetText("● 待机")
				lblStatus.SetTextColor(colorStopped)
			}
		}
		// 转发启动时如果用户在测试 tab,踢回规则 tab
		if running && tabWidget != nil && tabWidget.CurrentIndex() == probeTabIndex {
			tabWidget.SetCurrentIndex(0)
		}
		if updateProbeUI != nil {
			updateProbeUI()
		}
	}
	model := &RuleModel{}
	sessionModel := &SessionTableModel{}
	updateRulesCount := func() {
		if lblRules != nil {
			lblRules.SetText(fmt.Sprintf("%d 条规则", model.RowCount()))
		}
	}
	model.onChange = updateRulesCount
	fwd := NewForwarder(nil)
	_ = sessionTable

	// 加载已有配置
	loadedCfg := Config{UDPBuffer: defaultUDPBuffer, UDPTimeout: defaultUDPTimeout, DefaultPolicy: "allow"}
	if data, err := os.ReadFile(configFileName); err == nil {
		var c Config
		if err := json.Unmarshal(data, &c); err == nil {
			if c.UDPBuffer > 0 {
				loadedCfg.UDPBuffer = c.UDPBuffer
			}
			if c.UDPTimeout > 0 {
				loadedCfg.UDPTimeout = c.UDPTimeout
			}
			if c.DefaultPolicy != "" {
				loadedCfg.DefaultPolicy = c.DefaultPolicy
			}
			loadedCfg.Allow = c.Allow
			loadedCfg.Deny = c.Deny
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
	probe = NewProbe(appendLog)

	// 同步 probe 控件的启用状态。转发运行时整个 tab 锁定。
	updateProbeUI = func() {
		if probeModeListen == nil {
			return
		}
		isListen := probeModeListen.Checked()
		probeRunning := probe.Running()
		fwdRunning := fwd.Running()
		locked := probeRunning || fwdRunning // 锁定所有输入

		probeStartBtn.SetEnabled(!locked)
		probeStopBtn.SetEnabled(probeRunning)
		probeModeListen.SetEnabled(!locked)
		probeModeSend.SetEnabled(!locked)
		probeProto.SetEnabled(!locked)
		probeAddr.SetEnabled(!locked)
		probeEcho.SetEnabled(!locked && isListen)
		probePayload.SetEnabled(!locked && !isListen)
		probeRepeat.SetEnabled(!locked && !isListen)
		probeInterval.SetEnabled(!locked && !isListen && probeRepeat.Checked())
	}

	const btnW = 72

	err := MainWindow{
		AssignTo: &mw,
		Title:    "Forward",
		Size:     Size{Width: 880, Height: 600},
		MinSize:  Size{Width: 520, Height: 340},
		Layout:   VBox{Margins: Margins{Left: 8, Top: 8, Right: 8, Bottom: 8}, Spacing: 6},
		Children: []Widget{
			// ===== 顶部:运行控制 + 状态指示 =====
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 4},
				Children: []Widget{
					PushButton{
						AssignTo:    &startBtn,
						Text:        "启动",
						MinSize:     Size{Width: btnW},
						ToolTipText: "按当前规则启动所有 TCP/UDP 转发",
						OnClicked: func() {
							if model.RowCount() == 0 {
								walk.MsgBox(mw, "提示", "请先添加至少一条转发规则", walk.MsgBoxIconWarning)
								return
							}
							cfg := Config{
								Rules:         append([]Rule(nil), model.items...),
								UDPBuffer:     loadedCfg.UDPBuffer,
								UDPTimeout:    loadedCfg.UDPTimeout,
								DefaultPolicy: loadedCfg.DefaultPolicy,
								Allow:         loadedCfg.Allow,
								Deny:          loadedCfg.Deny,
							}
							if err := fwd.Start(cfg); err != nil {
								walk.MsgBox(mw, "错误", err.Error(), walk.MsgBoxIconError)
								return
							}
							startBtn.SetEnabled(false)
							stopBtn.SetEnabled(true)
							setStatus(true)
						},
					},
					PushButton{
						AssignTo:    &stopBtn,
						Text:        "停止",
						MinSize:     Size{Width: btnW},
						Enabled:     false,
						ToolTipText: "停止所有转发,关闭活跃的 TCP/UDP 会话",
						OnClicked: func() {
							stopBtn.SetEnabled(false)
							stopBtn.SetText("停止…")
							go func() {
								fwd.Stop()
								mw.Synchronize(func() {
									startBtn.SetEnabled(true)
									stopBtn.SetText("停止")
									setStatus(false)
								})
							}()
						},
					},
					VSeparator{},
					PushButton{
						Text:        "保存",
						MinSize:     Size{Width: btnW},
						ToolTipText: "保存当前规则与 ACL 到 forward.json",
						OnClicked: func() {
							cfg := Config{
								Rules:         model.items,
								UDPBuffer:     loadedCfg.UDPBuffer,
								UDPTimeout:    loadedCfg.UDPTimeout,
								DefaultPolicy: loadedCfg.DefaultPolicy,
								Allow:         loadedCfg.Allow,
								Deny:          loadedCfg.Deny,
							}
							data, _ := json.MarshalIndent(cfg, "", "  ")
							if err := os.WriteFile(configFileName, data, 0644); err != nil {
								walk.MsgBox(mw, "错误", err.Error(), walk.MsgBoxIconError)
							} else {
								appendLog(fmt.Sprintf("已保存到 %s", configFileName))
							}
						},
					},
					HSpacer{},
					Label{
						AssignTo:  &lblStatus,
						Text:      "● 待机",
						Font:      Font{Bold: true, PointSize: 10},
						TextColor: colorStopped,
						MinSize:   Size{Width: 70},
					},
					VSeparator{},
					Label{AssignTo: &lblRules, Text: "0 条规则", Font: statFont, MinSize: Size{Width: 70}},
				},
			},
			// ===== 主区:Tab(上)+ 日志(下),VSplitter 可拖 =====
			VSplitter{
				HandleWidth: 4,
				Children: []Widget{
					TabWidget{
						AssignTo: &tabWidget,
						OnCurrentIndexChanged: func() {
							if tabWidget == nil {
								return
							}
							if tabWidget.CurrentIndex() == probeTabIndex && fwd.Running() {
								walk.MsgBox(mw, "提示", "转发运行中,请先停止再使用接口测试", walk.MsgBoxIconWarning)
								tabWidget.SetCurrentIndex(0)
							}
						},
						Pages: []TabPage{
							// --- 规则 ---
							TabPage{
								Title:  "  转 发 规 则  ",
								Layout: VBox{Margins: Margins{Left: 4, Top: 6, Right: 4, Bottom: 4}, Spacing: 4},
								Children: []Widget{
									Composite{
										Layout: HBox{MarginsZero: true, Spacing: 4},
										Children: []Widget{
											PushButton{
												Text:        "添加",
												MinSize:     Size{Width: btnW},
												ToolTipText: "新增一条转发规则(监听地址、目标地址、协议、规则级 ACL)",
												OnClicked: func() {
													if fwd.Running() {
														walk.MsgBox(mw, "提示", "请先停止转发再修改规则", walk.MsgBoxIconWarning)
														return
													}
													if r, ok := runRuleDialog(mw, "添加规则", Rule{Proto: "both"}); ok {
														model.Add(r)
													}
												},
											},
											PushButton{
												Text:        "编辑",
												MinSize:     Size{Width: btnW},
												ToolTipText: "编辑选中规则的地址、协议与规则级 ACL",
												OnClicked: func() {
													if fwd.Running() {
														walk.MsgBox(mw, "提示", "请先停止转发再修改规则", walk.MsgBoxIconWarning)
														return
													}
													idx := rulesTable.CurrentIndex()
													if idx < 0 {
														walk.MsgBox(mw, "提示", "请先选中一条规则", walk.MsgBoxIconInformation)
														return
													}
													if r, ok := runRuleDialog(mw, "编辑规则", model.items[idx]); ok {
														model.Update(idx, r)
													}
												},
											},
											PushButton{
												Text:        "删除",
												MinSize:     Size{Width: btnW},
												ToolTipText: "删除选中的规则",
												OnClicked: func() {
													if fwd.Running() {
														walk.MsgBox(mw, "提示", "请先停止转发再修改规则", walk.MsgBoxIconWarning)
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
												Text:        "名单",
												MinSize:     Size{Width: btnW},
												ToolTipText: "编辑全局黑/白名单(ACL)与默认策略",
												OnClicked: func() {
													if fwd.Running() {
														walk.MsgBox(mw, "提示", "请先停止转发再修改名单", walk.MsgBoxIconWarning)
														return
													}
													if runGlobalACLDialog(mw, &loadedCfg) {
														appendLog(fmt.Sprintf("名单已更新:默认=%s,allow=%d,deny=%d",
															loadedCfg.DefaultPolicy, len(loadedCfg.Allow), len(loadedCfg.Deny)))
													}
												},
											},
											HSpacer{},
										},
									},
									TableView{
										AssignTo:         &rulesTable,
										AlternatingRowBG: true,
										ColumnsOrderable: false,
										Model:            model,
										Columns: []TableViewColumn{
											{Title: "监听地址", Width: 200},
											{Title: "目标地址", Width: 220},
											{Title: "协议", Width: 70},
										},
									},
								},
							},
							// --- 会话 ---
							TabPage{
								Title:  "  活 动 会 话  ",
								Layout: VBox{Margins: Margins{Left: 4, Top: 6, Right: 4, Bottom: 4}, Spacing: 4},
								Children: []Widget{
									TableView{
										AssignTo:         &sessionTable,
										AlternatingRowBG: true,
										ColumnsOrderable: true,
										Model:            sessionModel,
										Columns: []TableViewColumn{
											{Title: "ID", Width: 50},
											{Title: "协议", Width: 50},
											{Title: "客户端", Width: 140},
											{Title: "监听", Width: 130},
											{Title: "目标", Width: 140},
											{Title: "入字节", Width: 80},
											{Title: "出字节", Width: 80},
											{Title: "入速率", Width: 90},
											{Title: "出速率", Width: 90},
											{Title: "持续", Width: 60},
											{Title: "活跃", Width: 60},
										},
									},
								},
							},
							// --- 测试 ---
							TabPage{
								Title:  "  接 口 测 试  ",
								Layout: VBox{Margins: Margins{Left: 4, Top: 6, Right: 4, Bottom: 4}, Spacing: 6},
								Children: []Widget{
									Composite{
										Layout: HBox{MarginsZero: true, Spacing: 8},
										Children: []Widget{
											RadioButton{
												AssignTo:    &probeModeListen,
												Text:        "监听(收包)",
												Value:       true,
												ToolTipText: "本机监听端口,等待对方发送数据",
												OnClicked:   func() { updateProbeUI() },
											},
											RadioButton{
												AssignTo:    &probeModeSend,
												Text:        "发送(发包)",
												ToolTipText: "向目标地址主动发送数据,验证链路",
												OnClicked:   func() { updateProbeUI() },
											},
											HSpacer{},
										},
									},
									Composite{
										Layout: Grid{Columns: 4, MarginsZero: true, Spacing: 6},
										Children: []Widget{
											Label{Text: "协议:"},
											ComboBox{
												AssignTo: &probeProto,
												Model:    []string{"tcp", "udp"},
												Value:    "tcp",
												MinSize:  Size{Width: 70},
											},
											Label{Text: "地址:"},
											LineEdit{
												AssignTo:    &probeAddr,
												Text:        "0.0.0.0:9999",
												CueBanner:   "监听:0.0.0.0:9999  发送:1.2.3.4:9999",
												ToolTipText: "监听:本机绑定地址;发送:对端目标地址",
											},

											Label{Text: "发送内容:"},
											LineEdit{
												AssignTo:    &probePayload,
												Text:        "ping",
												ColumnSpan:  3,
												ToolTipText: "发送模式要发出的字符串内容",
											},

											Label{Text: ""},
											CheckBox{
												AssignTo:    &probeEcho,
												Text:        "收到回 echo",
												ToolTipText: "监听模式下,收到包后原样回发(便于另一端验证)",
											},
											CheckBox{
												AssignTo:         &probeRepeat,
												Text:             "持续发送 间隔(秒)",
												ToolTipText:      "勾选后按指定间隔循环发送,直到点停止",
												OnCheckedChanged: func() { updateProbeUI() },
											},
											LineEdit{
												AssignTo: &probeInterval,
												Text:     "1",
												MinSize:  Size{Width: 50},
											},
										},
									},
									Composite{
										Layout: HBox{MarginsZero: true, Spacing: 6},
										Children: []Widget{
											PushButton{
												AssignTo:    &probeStartBtn,
												Text:        "开始",
												MinSize:     Size{Width: btnW},
												ToolTipText: "开始监听或开始发送",
												OnClicked: func() {
													if fwd.Running() {
														walk.MsgBox(mw, "提示", "请先停止转发再使用接口测试", walk.MsgBoxIconWarning)
														return
													}
													cfg := ProbeConfig{
														Proto: probeProto.Text(),
														Addr:  strings.TrimSpace(probeAddr.Text()),
													}
													if cfg.Addr == "" {
														walk.MsgBox(mw, "提示", "请填写地址", walk.MsgBoxIconWarning)
														return
													}
													if probeModeListen.Checked() {
														cfg.Mode = ProbeListen
														cfg.Echo = probeEcho.Checked()
													} else {
														cfg.Mode = ProbeSend
														cfg.Payload = []byte(probePayload.Text())
														if len(cfg.Payload) == 0 {
															walk.MsgBox(mw, "提示", "请填写发送内容", walk.MsgBoxIconWarning)
															return
														}
														if probeRepeat.Checked() {
															sec, _ := strconv.Atoi(strings.TrimSpace(probeInterval.Text()))
															if sec < 1 {
																sec = 1
															}
															cfg.Interval = time.Duration(sec) * time.Second
														}
													}
													if err := probe.Start(cfg); err != nil {
														walk.MsgBox(mw, "错误", err.Error(), walk.MsgBoxIconError)
														return
													}
													updateProbeUI()
												},
											},
											PushButton{
												AssignTo:    &probeStopBtn,
												Text:        "停止",
												MinSize:     Size{Width: btnW},
												Enabled:     false,
												ToolTipText: "停止当前测试",
												OnClicked: func() {
													probeStopBtn.SetEnabled(false)
													probeStopBtn.SetText("停止…")
													go func() {
														probe.Stop()
														mw.Synchronize(func() {
															probeStopBtn.SetText("停止")
															updateProbeUI()
														})
													}()
												},
											},
											Label{AssignTo: &probeStat, Text: "已发: 0 B   已收: 0 B"},
											HSpacer{},
										},
									},
									VSpacer{},
								},
							},
						},
					},
					// ===== 日志(常驻) =====
					Composite{
						Layout:  VBox{MarginsZero: true, Spacing: 4},
						MinSize: Size{Height: 80},
						Children: []Widget{
							Composite{
								Layout: HBox{MarginsZero: true, Spacing: 4},
								Children: []Widget{
									Label{Text: "日志"},
									HSpacer{},
									PushButton{
										Text:        "导出",
										MinSize:     Size{Width: btnW},
										ToolTipText: "把当前日志内容另存为 .log 文件",
										OnClicked: func() {
											if logEdit == nil {
												return
											}
											dlg := new(walk.FileDialog)
											dlg.Title = "保存日志"
											dlg.Filter = "日志文件 (*.log)|*.log|文本文件 (*.txt)|*.txt|所有文件 (*.*)|*.*"
											dlg.FilePath = fmt.Sprintf("forward-%s.log", time.Now().Format("20060102-150405"))
											ok, err := dlg.ShowSave(mw)
											if err != nil {
												walk.MsgBox(mw, "错误", err.Error(), walk.MsgBoxIconError)
												return
											}
											if !ok || dlg.FilePath == "" {
												return
											}
											path := dlg.FilePath
											if !strings.Contains(filepath.Base(path), ".") {
												path += ".log"
											}
											if err := os.WriteFile(path, []byte(logEdit.Text()), 0644); err != nil {
												walk.MsgBox(mw, "错误", err.Error(), walk.MsgBoxIconError)
												return
											}
											appendLog(fmt.Sprintf("日志已导出到 %s", path))
										},
									},
									PushButton{
										Text:        "清空",
										MinSize:     Size{Width: btnW},
										ToolTipText: "清空日志显示区",
										OnClicked: func() {
											if logEdit != nil {
												logEdit.SetText("")
											}
										},
									},
								},
							},
							TextEdit{
								AssignTo: &logEdit,
								ReadOnly: true,
								VScroll:  true,
							},
						},
					},
				},
			},
			// ===== 底部:统计栏 =====
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 10},
				Children: []Widget{
					Label{Text: "TCP"},
					Label{AssignTo: &lblTcp, Text: "0", Font: statFont, TextColor: colorAccent},
					VSeparator{},
					Label{Text: "UDP"},
					Label{AssignTo: &lblUdp, Text: "0", Font: statFont, TextColor: colorAccent},
					VSeparator{},
					Label{Text: "入"},
					Label{AssignTo: &lblIn, Text: "0 B", Font: statFont, TextColor: colorAccent},
					Label{Text: "@"},
					Label{AssignTo: &lblInRate, Text: "0 B/s", Font: statFont},
					VSeparator{},
					Label{Text: "出"},
					Label{AssignTo: &lblOut, Text: "0 B", Font: statFont, TextColor: colorAccent},
					Label{Text: "@"},
					Label{AssignTo: &lblOutRate, Text: "0 B/s", Font: statFont},
					VSeparator{},
					Label{Text: "拒绝"},
					Label{AssignTo: &lblDenied, Text: "0", Font: statFont, TextColor: colorWarn},
					HSpacer{},
				},
			},
		},
	}.Create()
	if err != nil {
		fmt.Println("创建主窗口失败:", err)
		return
	}

	// 给主窗口设置图标(任务栏 + 标题栏)
	if icon := loadAppIcon(); icon != nil {
		mw.SetIcon(icon)
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
				denied := fwd.denied.Load()
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

				probeSent := probe.Sent()
				probeRecv := probe.Recv()
				mw.Synchronize(func() {
					lblTcp.SetText(fmt.Sprintf("%d", tcp))
					lblUdp.SetText(fmt.Sprintf("%d", udp))
					lblIn.SetText(humanBytes(in))
					lblOut.SetText(humanBytes(out))
					lblInRate.SetText(humanRate(inRate))
					lblOutRate.SetText(humanRate(outRate))
					lblDenied.SetText(fmt.Sprintf("%d", denied))
					sessionModel.SetItems(sessions)
					if probeStat != nil {
						probeStat.SetText(fmt.Sprintf("已发: %s   已收: %s", humanBytes(probeSent), humanBytes(probeRecv)))
					}
				})
			}
		}
	}()

	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		uiClosed.Store(true)
		close(uiDone)
		fwd.Stop()
		probe.Stop()
	})

	setStatus(false)
	updateRulesCount()
	updateProbeUI()
	appendLog("就绪。点击\"启动\"开始转发。")
	mw.Run()
}
