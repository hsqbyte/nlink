package services

import (
	"testing"
)

func TestACLAllowOnly(t *testing.T) {
	a, err := ParseACL([]string{"10.0.0.0/8"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Allow("10.1.2.3:5000") {
		t.Error("10.1.2.3 应被允许")
	}
	if a.Allow("8.8.8.8:5000") {
		t.Error("8.8.8.8 应被拒绝（不在 allow 列表）")
	}
}

func TestACLDenyOnly(t *testing.T) {
	a, err := ParseACL(nil, []string{"1.2.3.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Allow("1.2.3.4:1") {
		t.Error("1.2.3.4 应被拒绝")
	}
	if !a.Allow("9.9.9.9:1") {
		t.Error("9.9.9.9 应被放行")
	}
}

func TestACLAllowThenDeny(t *testing.T) {
	a, err := ParseACL([]string{"10.0.0.0/8"}, []string{"10.1.0.0/16"})
	if err != nil {
		t.Fatal(err)
	}
	if !a.Allow("10.2.0.1:1") {
		t.Error("10.2.0.1 应被放行")
	}
	if a.Allow("10.1.2.3:1") {
		t.Error("10.1.2.3 被 allow 命中但又在 deny 内，应拒绝")
	}
}

func TestACLSingleIP(t *testing.T) {
	a, err := ParseACL([]string{"1.2.3.4"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Allow("1.2.3.4:80") {
		t.Error("单 IP 放行失败")
	}
	if a.Allow("1.2.3.5:80") {
		t.Error("不在列表应拒绝")
	}
}

func TestACLBadCIDR(t *testing.T) {
	if _, err := ParseACL([]string{"not-a-cidr"}, nil); err == nil {
		t.Error("非法 CIDR 应报错")
	}
}

func TestACLEmpty(t *testing.T) {
	var a *ACL
	if !a.Allow("1.2.3.4:1") {
		t.Error("nil ACL 应放行所有")
	}
}
