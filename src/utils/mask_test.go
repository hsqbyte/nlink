package utils

import "testing"

func TestMaskSecret(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"abc", "••••••••"},
		{"12345678", "••••••••"},
		{"123456789", "••••••••"}, // 9 位：按 >8 规则，前4+mask+后4，但与整体8字符不同
		{"abcdefghijklmnop", "abcd••••••••mnop"},
	}
	for _, c := range cases {
		// 对 9 位特殊：按长度>8 规则拼接
		if c.in == "123456789" {
			c.want = "1234••••••••6789"
		}
		got := MaskSecret(c.in)
		if got != c.want {
			t.Errorf("MaskSecret(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
