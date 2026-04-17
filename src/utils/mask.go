// Package utils 提供通用辅助函数。
package utils

// MaskSecret 将敏感字符串（如 token）脱敏：保留前/后 4 位，中间以 • 填充。
// 长度 <=8 时整体替换为固定掩码，避免泄漏长度信息过多。
func MaskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••••••"
	}
	return s[:4] + "••••••••" + s[len(s)-4:]
}
