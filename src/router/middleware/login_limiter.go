package middleware

import (
	"sync"
	"time"
)

// 登录失败限流策略：
// - 每个客户端 IP 在 loginWindow 内最多允许 loginMaxFails 次失败
// - 超过阈值则锁定 loginLockDuration
// - 登录成功后清零
//
// 这些常量可以在将来改为从配置读取。
const (
	loginWindow       = 10 * time.Minute
	loginMaxFails     = 5
	loginLockDuration = 5 * time.Minute
)

type loginAttempt struct {
	fails      int
	firstFail  time.Time
	lockedTill time.Time
}

var (
	loginAttempts   = make(map[string]*loginAttempt)
	loginAttemptsMu sync.Mutex
)

func init() {
	go loginLimiterJanitor()
}

func loginLimiterJanitor() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cleanupLoginAttempts()
	}
}

func cleanupLoginAttempts() {
	now := time.Now()
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()
	for k, a := range loginAttempts {
		// 未锁定 + 窗口过期，或 锁定已解除
		if (a.lockedTill.IsZero() && now.Sub(a.firstFail) > loginWindow) ||
			(!a.lockedTill.IsZero() && now.After(a.lockedTill)) {
			delete(loginAttempts, k)
		}
	}
}

// loginLimiterCheck 判断该 IP 当前是否被锁定；返回 (locked, retryAfter)。
func loginLimiterCheck(ip string) (bool, time.Duration) {
	if ip == "" {
		return false, 0
	}
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()
	a, ok := loginAttempts[ip]
	if !ok {
		return false, 0
	}
	now := time.Now()
	if !a.lockedTill.IsZero() {
		if now.Before(a.lockedTill) {
			return true, a.lockedTill.Sub(now)
		}
		// 锁已过期，重置
		delete(loginAttempts, ip)
	}
	return false, 0
}

// loginLimiterRecordFail 记录一次失败；若达到阈值则锁定。
// 返回 (locked, retryAfter)。
func loginLimiterRecordFail(ip string) (bool, time.Duration) {
	if ip == "" {
		return false, 0
	}
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()
	now := time.Now()
	a, ok := loginAttempts[ip]
	if !ok || now.Sub(a.firstFail) > loginWindow {
		loginAttempts[ip] = &loginAttempt{fails: 1, firstFail: now}
		return false, 0
	}
	a.fails++
	if a.fails >= loginMaxFails {
		a.lockedTill = now.Add(loginLockDuration)
		return true, loginLockDuration
	}
	return false, 0
}

// loginLimiterReset 登录成功后清除该 IP 的失败记录。
func loginLimiterReset(ip string) {
	if ip == "" {
		return
	}
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()
	delete(loginAttempts, ip)
}
