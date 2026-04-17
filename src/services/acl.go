package services

import (
	"fmt"
	"net"
	"strings"
)

// ACL 基于 CIDR 的入站连接过滤
//
// 规则优先级：
//  1. Allow 列表非空时，remote IP 必须命中 Allow 才能通过
//  2. Deny 列表命中则拒绝
//  3. 否则放行
type ACL struct {
	allow []*net.IPNet
	deny  []*net.IPNet
}

// ParseACL 把字符串 CIDR 列表编译成 ACL；非法条目会返回错误
func ParseACL(allow, deny []string) (*ACL, error) {
	a := &ACL{}
	var err error
	a.allow, err = parseCIDRList(allow)
	if err != nil {
		return nil, fmt.Errorf("allow_cidr: %w", err)
	}
	a.deny, err = parseCIDRList(deny)
	if err != nil {
		return nil, fmt.Errorf("deny_cidr: %w", err)
	}
	return a, nil
}

func parseCIDRList(items []string) ([]*net.IPNet, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]*net.IPNet, 0, len(items))
	for _, raw := range items {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "/") {
			// 单个 IP，补成 /32 或 /128
			if ip := net.ParseIP(raw); ip != nil {
				if ip.To4() != nil {
					raw += "/32"
				} else {
					raw += "/128"
				}
			}
		}
		_, ipnet, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("非法 CIDR %q: %w", raw, err)
		}
		out = append(out, ipnet)
	}
	return out, nil
}

// Allow 判断远端地址是否被允许
func (a *ACL) Allow(remoteIP string) bool {
	if a == nil {
		return true
	}
	ip := net.ParseIP(extractHost(remoteIP))
	if ip == nil {
		return false
	}
	if len(a.allow) > 0 {
		hit := false
		for _, n := range a.allow {
			if n.Contains(ip) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	for _, n := range a.deny {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// IsEmpty 判断是否没有任何规则
func (a *ACL) IsEmpty() bool {
	return a == nil || (len(a.allow) == 0 && len(a.deny) == 0)
}

// extractHost 支持 "host:port" 或纯 IP
func extractHost(s string) string {
	if host, _, err := net.SplitHostPort(s); err == nil {
		return host
	}
	return s
}
