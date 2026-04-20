package services

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/fastgox/utils/logger"
)

// VPNLease 一条 DHCP 租约
type VPNLease struct {
	NodeName   string    `json:"node_name"`
	VirtualIP  string    `json:"virtual_ip"` // CIDR, 如 "10.0.0.5/24"
	AssignedAt time.Time `json:"assigned_at"`
}

// VPNDHCPService VPN DHCP 分配器
type VPNDHCPService struct {
	mu         sync.Mutex
	subnet     *net.IPNet
	prefix     int // 前缀长度（用于拼接 CIDR 返回）
	excludeIPs map[string]struct{}
	leaseFile  string
	leases     map[string]*VPNLease // nodeName -> lease
	ipToNode   map[string]string    // virtualIP (bare) -> nodeName
}

var vpnDHCPSvc *VPNDHCPService

// InitVPNDHCP 初始化 DHCP 服务（在 listen 端启用时调用）
func InitVPNDHCP(subnetCIDR string, excludeIPs []string, leaseFile string) error {
	if subnetCIDR == "" {
		return fmt.Errorf("DHCP 需要 subnet")
	}
	_, subnet, err := net.ParseCIDR(subnetCIDR)
	if err != nil {
		return fmt.Errorf("解析 subnet 失败: %w", err)
	}
	prefix, _ := subnet.Mask.Size()

	if leaseFile == "" {
		leaseFile = "data/vpn_leases.json"
	}

	exclude := make(map[string]struct{}, len(excludeIPs)+2)
	for _, s := range excludeIPs {
		ip := net.ParseIP(s).To4()
		if ip != nil {
			exclude[ip.String()] = struct{}{}
		}
	}
	// 默认排除网络号 / 广播号
	exclude[subnet.IP.String()] = struct{}{}
	exclude[broadcastAddr(subnet).String()] = struct{}{}

	svc := &VPNDHCPService{
		subnet:     subnet,
		prefix:     prefix,
		excludeIPs: exclude,
		leaseFile:  leaseFile,
		leases:     make(map[string]*VPNLease),
		ipToNode:   make(map[string]string),
	}
	if err := svc.load(); err != nil {
		logger.Warn("[DHCP] 加载租约失败: %v (从空开始)", err)
	}
	vpnDHCPSvc = svc
	logger.Info("[DHCP] 已启动 subnet=%s leases=%d file=%s", subnetCIDR, len(svc.leases), leaseFile)
	return nil
}

// GetVPNDHCP 返回 DHCP 单例（nil 表示未启用）
func GetVPNDHCP() *VPNDHCPService { return vpnDHCPSvc }

// Allocate 为 nodeName 分配 IP（幂等：同名返回已有租约）。hint 为期望 IP，非空时若可用优先返回。
// 返回 CIDR 形式，如 "10.0.0.5/24"
func (s *VPNDHCPService) Allocate(nodeName, hint string) (string, error) {
	if nodeName == "" {
		return "", fmt.Errorf("node_name 不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if lease, ok := s.leases[nodeName]; ok {
		return lease.VirtualIP, nil
	}

	// 尝试 hint
	if hint != "" {
		ip, _, err := net.ParseCIDR(hint)
		if err == nil {
			ip4 := ip.To4()
			if ip4 != nil && s.subnet.Contains(ip4) {
				key := ip4.String()
				if _, excluded := s.excludeIPs[key]; !excluded {
					if _, used := s.ipToNode[key]; !used {
						return s.assign(nodeName, ip4), nil
					}
				}
			}
		}
	}

	// 线性扫描子网找空闲
	for ip := nextIP(s.subnet.IP.To4()); s.subnet.Contains(ip); ip = nextIP(ip) {
		key := ip.String()
		if _, excluded := s.excludeIPs[key]; excluded {
			continue
		}
		if _, used := s.ipToNode[key]; used {
			continue
		}
		return s.assign(nodeName, ip), nil
	}
	return "", fmt.Errorf("DHCP 池已耗尽")
}

// Release 释放 nodeName 的租约
func (s *VPNDHCPService) Release(nodeName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lease, ok := s.leases[nodeName]; ok {
		ip, _, _ := net.ParseCIDR(lease.VirtualIP)
		if ip != nil {
			delete(s.ipToNode, ip.String())
		}
		delete(s.leases, nodeName)
		s.persistLocked()
	}
}

// List 返回所有租约（副本）
func (s *VPNDHCPService) List() []VPNLease {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]VPNLease, 0, len(s.leases))
	for _, l := range s.leases {
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeName < out[j].NodeName })
	return out
}

// assign 必须在持锁下调用
func (s *VPNDHCPService) assign(nodeName string, ip net.IP) string {
	cidr := fmt.Sprintf("%s/%d", ip.String(), s.prefix)
	s.leases[nodeName] = &VPNLease{
		NodeName:   nodeName,
		VirtualIP:  cidr,
		AssignedAt: time.Now(),
	}
	s.ipToNode[ip.String()] = nodeName
	s.persistLocked()
	logger.Info("[DHCP] 分配 %s -> %s", nodeName, cidr)
	return cidr
}

func (s *VPNDHCPService) persistLocked() {
	if err := os.MkdirAll(filepath.Dir(s.leaseFile), 0o755); err != nil {
		logger.Warn("[DHCP] 创建目录失败: %v", err)
		return
	}
	data, err := json.MarshalIndent(s.leases, "", "  ")
	if err != nil {
		logger.Warn("[DHCP] 序列化失败: %v", err)
		return
	}
	tmp := s.leaseFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		logger.Warn("[DHCP] 写入失败: %v", err)
		return
	}
	if err := os.Rename(tmp, s.leaseFile); err != nil {
		logger.Warn("[DHCP] rename 失败: %v", err)
	}
}

func (s *VPNDHCPService) load() error {
	data, err := os.ReadFile(s.leaseFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var m map[string]*VPNLease
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	for name, lease := range m {
		s.leases[name] = lease
		ip, _, err := net.ParseCIDR(lease.VirtualIP)
		if err == nil {
			s.ipToNode[ip.To4().String()] = name
		}
	}
	return nil
}

// ---- 工具函数 ----

func nextIP(ip net.IP) net.IP {
	out := make(net.IP, len(ip))
	copy(out, ip)
	for i := len(out) - 1; i >= 0; i-- {
		out[i]++
		if out[i] != 0 {
			break
		}
	}
	return out
}

func broadcastAddr(n *net.IPNet) net.IP {
	ip := n.IP.To4()
	mask := n.Mask
	out := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		out[i] = ip[i] | ^mask[i]
	}
	return out
}
