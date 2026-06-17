// acl.go - 客户端访问控制(黑/白名单)
//
// 支持配置:
//   - 全局 ACL(Config.Allow / Config.Deny / Config.DefaultPolicy)
//   - 每条规则 ACL(Rule.Allow / Rule.Deny)
//
// 匹配语义(命中即返):
//   1) 客户端 IP 命中 规则 deny 或 全局 deny -> 拒绝
//   2) 若任意一级存在非空 allow 列表 -> 必须命中 allow 才放行,否则拒绝
//   3) 否则按 defaultPolicy("allow" / "deny",缺省 "allow")
//
// 列表项格式:
//   - 单 IP:      "192.168.1.10" 或 "fe80::1"
//   - CIDR:       "10.0.0.0/8" 或 "2001:db8::/32"
//   - 行内空白和空项忽略

package main

import (
	"fmt"
	"net"
	"strings"
)

type compiledACL struct {
	allow []*net.IPNet
	deny  []*net.IPNet
}

func (c compiledACL) empty() bool {
	return len(c.allow) == 0 && len(c.deny) == 0
}

// parseACLList 把字符串列表编译为 IPNet 列表。单 IP 会被规范化为 /32(IPv4)或 /128(IPv6)。
func parseACLList(items []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(items))
	for _, raw := range items {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if strings.Contains(s, "/") {
			_, ipnet, err := net.ParseCIDR(s)
			if err != nil {
				return nil, fmt.Errorf("CIDR 非法: %q (%v)", s, err)
			}
			out = append(out, ipnet)
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, fmt.Errorf("IP 非法: %q", s)
		}
		bits := 32
		if ip.To4() == nil {
			bits = 128
		} else {
			ip = ip.To4()
		}
		out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	return out, nil
}

func compileACL(allow, deny []string) (compiledACL, error) {
	a, err := parseACLList(allow)
	if err != nil {
		return compiledACL{}, fmt.Errorf("allow: %w", err)
	}
	d, err := parseACLList(deny)
	if err != nil {
		return compiledACL{}, fmt.Errorf("deny: %w", err)
	}
	return compiledACL{allow: a, deny: d}, nil
}

func matchAny(ip net.IP, nets []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// aclPermit 判断给定客户端 IP 是否允许通过。
// ruleACL 是当前规则的 ACL,globalACL 是全局 ACL,defaultAllow 是默认策略。
func aclPermit(ip net.IP, ruleACL, globalACL compiledACL, defaultAllow bool) bool {
	// 1. 任一级 deny 命中 -> 拒绝
	if matchAny(ip, ruleACL.deny) || matchAny(ip, globalACL.deny) {
		return false
	}
	// 2. 存在非空 allow -> 必须命中
	hasAllow := len(ruleACL.allow) > 0 || len(globalACL.allow) > 0
	if hasAllow {
		return matchAny(ip, ruleACL.allow) || matchAny(ip, globalACL.allow)
	}
	// 3. 默认策略
	return defaultAllow
}

// extractIP 把 "ip:port" / "[ip]:port" 拆出 IP 部分。失败返回 nil。
func extractIP(addr string) net.IP {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	return net.ParseIP(host)
}
