package services

import (
	"os"
	"path/filepath"
	"testing"
)

// TestVPNDHCPAllocate_BasicAndPersist 基础分配 + 幂等 + 持久化
func TestVPNDHCPAllocate_BasicAndPersist(t *testing.T) {
	dir := t.TempDir()
	leaseFile := filepath.Join(dir, "leases.json")

	// 重置全局单例用于测试
	oldSvc := vpnDHCPSvc
	t.Cleanup(func() { vpnDHCPSvc = oldSvc })

	if err := InitVPNDHCP("10.0.0.0/24", []string{"10.0.0.1"}, leaseFile); err != nil {
		t.Fatalf("init: %v", err)
	}
	svc := GetVPNDHCP()
	if svc == nil {
		t.Fatal("svc nil")
	}

	// 第一次分配 node-a
	cidr1, err := svc.Allocate("node-a", "")
	if err != nil {
		t.Fatalf("alloc: %v", err)
	}
	if cidr1 == "" || cidr1 == "10.0.0.0/24" || cidr1 == "10.0.0.1/24" {
		t.Errorf("分配到的 IP 不合理: %s", cidr1)
	}

	// 幂等：同名再次分配应返回相同 CIDR
	cidr2, err := svc.Allocate("node-a", "")
	if err != nil {
		t.Fatalf("alloc2: %v", err)
	}
	if cidr1 != cidr2 {
		t.Errorf("幂等失败: %s vs %s", cidr1, cidr2)
	}

	// 第二个节点拿不同的 IP
	cidr3, err := svc.Allocate("node-b", "")
	if err != nil {
		t.Fatalf("alloc3: %v", err)
	}
	if cidr3 == cidr1 {
		t.Errorf("node-b 与 node-a 冲突: %s", cidr3)
	}

	// 文件已持久化
	data, err := os.ReadFile(leaseFile)
	if err != nil || len(data) == 0 {
		t.Fatalf("持久化失败: err=%v len=%d", err, len(data))
	}

	// Release 后可以重新分配
	svc.Release("node-a")
	cidr4, err := svc.Allocate("node-c", cidr1) // hint 为刚释放的 IP
	if err != nil {
		t.Fatalf("alloc4: %v", err)
	}
	if cidr4 != cidr1 {
		t.Errorf("hint 复用失败: want %s got %s", cidr1, cidr4)
	}
}

// TestVPNDHCPAllocate_ExhaustPool 池耗尽
func TestVPNDHCPAllocate_ExhaustPool(t *testing.T) {
	dir := t.TempDir()
	leaseFile := filepath.Join(dir, "leases.json")
	oldSvc := vpnDHCPSvc
	t.Cleanup(func() { vpnDHCPSvc = oldSvc })

	// /30: 10.0.0.0 (net) / 10.0.0.1 / 10.0.0.2 / 10.0.0.3 (bcast)
	// 可分配 10.0.0.1, 10.0.0.2
	if err := InitVPNDHCP("10.0.0.0/30", nil, leaseFile); err != nil {
		t.Fatalf("init: %v", err)
	}
	svc := GetVPNDHCP()
	if _, err := svc.Allocate("n1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Allocate("n2", ""); err != nil {
		t.Fatal(err)
	}
	// 第三个应该失败
	if _, err := svc.Allocate("n3", ""); err == nil {
		t.Error("预期 pool 耗尽错误, 得到 nil")
	}
}
