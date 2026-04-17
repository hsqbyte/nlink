package middleware

import (
	"testing"
)

func resetLimiter() {
	loginAttemptsMu.Lock()
	loginAttempts = make(map[string]*loginAttempt)
	loginAttemptsMu.Unlock()
}

func TestLoginLimiterBelowThreshold(t *testing.T) {
	resetLimiter()
	ip := "1.1.1.1"
	for i := 0; i < loginMaxFails-1; i++ {
		locked, _ := loginLimiterRecordFail(ip)
		if locked {
			t.Fatalf("第 %d 次失败不应锁定", i+1)
		}
	}
	if locked, _ := loginLimiterCheck(ip); locked {
		t.Fatalf("未达到阈值不应处于锁定状态")
	}
}

func TestLoginLimiterLockAtThreshold(t *testing.T) {
	resetLimiter()
	ip := "2.2.2.2"
	var locked bool
	for i := 0; i < loginMaxFails; i++ {
		locked, _ = loginLimiterRecordFail(ip)
	}
	if !locked {
		t.Fatal("达到阈值应返回 locked=true")
	}
	if l, _ := loginLimiterCheck(ip); !l {
		t.Fatal("锁定后 Check 应返回 true")
	}
}

func TestLoginLimiterResetOnSuccess(t *testing.T) {
	resetLimiter()
	ip := "3.3.3.3"
	loginLimiterRecordFail(ip)
	loginLimiterRecordFail(ip)
	loginLimiterReset(ip)
	if l, _ := loginLimiterCheck(ip); l {
		t.Fatal("重置后不应锁定")
	}
	loginAttemptsMu.Lock()
	_, exists := loginAttempts[ip]
	loginAttemptsMu.Unlock()
	if exists {
		t.Fatal("重置后记录应被删除")
	}
}

func TestLoginLimiterEmptyIP(t *testing.T) {
	if l, _ := loginLimiterCheck(""); l {
		t.Fatal("空 IP 不应被锁定")
	}
	if l, _ := loginLimiterRecordFail(""); l {
		t.Fatal("空 IP 记录失败应是 no-op")
	}
}
